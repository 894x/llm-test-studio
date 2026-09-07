package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

const legacyAPIAuditDriver = "legacy.apiaudit"

type legacyDriverConfig struct {
	Driver        string         `json:"driver"`
	DriverVersion int            `json:"driver_version"`
	LegacyKind    string         `json:"legacy_kind"`
	BodyPresent   bool           `json:"body_present"`
	Options       map[string]any `json:"options"`
}

type LegacyAPIAuditExecutor struct {
	httpDoer apiaudit.HTTPDoer
}

func NewLegacyAPIAuditExecutor(httpDoer apiaudit.HTTPDoer) *LegacyAPIAuditExecutor {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	return &LegacyAPIAuditExecutor{httpDoer: httpDoer}
}

func (executor *LegacyAPIAuditExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if executor == nil || executor.httpDoer == nil || ctx == nil || request.Credential == nil || emit == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	secret, err := request.Credential.Bytes()
	if err != nil || len(secret) == 0 {
		clear(secret)
		return ErrNotRunnable
	}
	defer clear(secret)
	snapshot := request.Run.Snapshot()
	config := apiaudit.RunConfig{
		Suite: string(snapshot.Channel.Protocol), BaseURL: snapshot.Channel.BaseURL,
		APIKey: string(secret), Model: snapshot.Channel.UpstreamModelName,
		PollInterval: 10 * time.Second,
		Timeout:      time.Duration(snapshot.Load.RequestTimeoutMS) * time.Millisecond,
	}
	started := time.Now()
	for index, testCase := range request.Cases {
		if stopped(request.StopSending) {
			return nil
		}
		definition, convertErr := legacyCaseDefinition(testCase)
		if convertErr != nil {
			return fmt.Errorf("prepare legacy case %s: %w", testCase.ID, convertErr)
		}
		caseContext, cancel := context.WithTimeout(ctx, config.Timeout)
		startedOffset := milliseconds(time.Since(started))
		result, runErr := apiaudit.RunCase(caseContext, executor.httpDoer, config, apiaudit.PlannedRun{Case: definition, Model: config.Model, ResultID: definition.ID})
		finishedOffset := milliseconds(time.Since(started))
		cancel()
		if runErr != nil {
			return fmt.Errorf("%w: %v", ErrUnsupportedExecutionProtocol, runErr)
		}
		draft := draftFromLegacyResult(testCase.ID, index, result)
		draft.Metrics["scheduled_offset_ms"], draft.Metrics["started_offset_ms"], draft.Metrics["finished_offset_ms"] = startedOffset, startedOffset, finishedOffset
		if err := emit(draft); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func legacyCaseDefinition(testCase domain.TestCase) (apiaudit.CaseDefinition, error) {
	if err := testCase.Validate(); err != nil || testCase.ExecutionMode != domain.CaseExecutionAutomatic ||
		testCase.Definition.Type != casetypes.TypeLegacyAPIAudit || testCase.Definition.TypeVersion != 1 {
		return apiaudit.CaseDefinition{}, ErrNotRunnable
	}
	var spec casetypes.LegacyAPIAuditSpec
	if err := json.Unmarshal(testCase.Definition.Spec, &spec); err != nil || strings.TrimSpace(spec.Kind) == "" {
		return apiaudit.CaseDefinition{}, ErrNotRunnable
	}
	body := map[string]any{}
	if len(spec.Request.Body) != 0 {
		if err := json.Unmarshal(spec.Request.Body, &body); err != nil {
			return apiaudit.CaseDefinition{}, ErrNotRunnable
		}
	}
	if spec.Options == nil {
		spec.Options = map[string]any{}
	}
	return apiaudit.CaseDefinition{
		ID: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
		Protocol: string(testCase.Protocol), ModelTargets: append([]string(nil), testCase.ModelTargets...), Kind: spec.Kind,
		Default: testCase.Default, Disabled: !testCase.Enabled, Severity: string(testCase.Severity),
		Request: apiaudit.RequestDefinition{Method: string(spec.Request.Method), Path: spec.Request.Path, Headers: spec.Request.Headers, Body: body},
		Options: spec.Options,
	}, nil
}

func draftFromLegacyResult(caseID string, index int, result apiaudit.CaseResult) ResultDraft {
	draft := ResultDraft{
		CaseID: caseID, RequestID: fmt.Sprintf("legacy-%d", index+1),
		Metrics: map[string]float64{"e2e_ms": float64(result.ElapsedMS), "http_status": float64(result.HTTPStatus)},
	}
	for name, value := range result.Metrics {
		if numeric, ok := value.(float64); ok {
			draft.Metrics[name] = numeric
		}
	}
	if result.Status == apiaudit.StatusPass {
		draft.Success = domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
		return draft
	}
	switch {
	case strings.Contains(strings.ToLower(result.Evidence), "cancel"):
		draft.Failure, draft.ErrorCode = domain.FailureCancelled, "legacy_cancelled"
	case result.HTTPStatus == 0:
		draft.Failure, draft.ErrorCode = domain.FailureNetwork, "legacy_network_error"
	case result.HTTPStatus < 200 || result.HTTPStatus >= 300:
		draft.Success.Transport = true
		draft.Failure, draft.ErrorCode = domain.FailureProtocol, "legacy_http_failure"
	default:
		draft.Success.Transport = true
		draft.Success.Protocol = true
		draft.Failure = domain.FailureSemantic
		if result.Status == apiaudit.StatusWarning {
			draft.ErrorCode = "legacy_warning"
		} else if result.Status == apiaudit.StatusUnknown {
			draft.ErrorCode = "legacy_unknown"
		} else {
			draft.ErrorCode = "legacy_assertion_failed"
		}
	}
	return draft
}

func stopped(signal <-chan struct{}) bool {
	if signal == nil {
		return false
	}
	select {
	case <-signal:
		return true
	default:
		return false
	}
}
