package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
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
		ConfirmPaidSuite: true, PollInterval: 10 * time.Second,
		Timeout: time.Duration(snapshot.Load.RequestTimeoutMS) * time.Millisecond,
	}
	for index, testCase := range request.Cases {
		if stopped(request.StopSending) {
			return nil
		}
		definition, convertErr := legacyCaseDefinition(testCase)
		if convertErr != nil {
			return fmt.Errorf("prepare legacy case %s: %w", testCase.ID, convertErr)
		}
		caseContext, cancel := context.WithTimeout(ctx, config.Timeout)
		var result apiaudit.CaseResult
		switch testCase.Protocol {
		case domain.ProtocolOpenAIChat:
			result = apiaudit.RunOpenAIChatCase(caseContext, executor.httpDoer, config, definition)
		case domain.ProtocolKimiK3:
			result = apiaudit.RunKimiK3Case(caseContext, executor.httpDoer, config, definition)
		case domain.ProtocolSeedance:
			result = apiaudit.RunSeedanceCase(caseContext, executor.httpDoer, config, apiaudit.PlannedRun{Case: definition, Model: config.Model, ResultID: definition.ID})
		default:
			cancel()
			return ErrUnsupportedExecutionProtocol
		}
		cancel()
		if err := emit(draftFromLegacyResult(testCase.ID, index, result)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func legacyCaseDefinition(testCase domain.TestCase) (apiaudit.CaseDefinition, error) {
	if err := testCase.Validate(); err != nil || testCase.ExecutionMode != domain.CaseExecutionAutomatic || len(testCase.Definition.Assertions) != 1 {
		return apiaudit.CaseDefinition{}, ErrNotRunnable
	}
	assertion := testCase.Definition.Assertions[0]
	if assertion.Kind != domain.AssertionCustom {
		return apiaudit.CaseDefinition{}, ErrNotRunnable
	}
	var driver legacyDriverConfig
	if err := json.Unmarshal(assertion.Config, &driver); err != nil || driver.Driver != legacyAPIAuditDriver || driver.DriverVersion != 1 || strings.TrimSpace(driver.LegacyKind) == "" {
		return apiaudit.CaseDefinition{}, ErrNotRunnable
	}
	body := map[string]any{}
	if len(testCase.Definition.Request.Body) != 0 {
		if err := json.Unmarshal(testCase.Definition.Request.Body, &body); err != nil {
			return apiaudit.CaseDefinition{}, ErrNotRunnable
		}
	}
	if driver.Options == nil {
		driver.Options = map[string]any{}
	}
	return apiaudit.CaseDefinition{
		ID: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
		Protocol: string(testCase.Protocol), Kind: driver.LegacyKind,
		Default: testCase.Default, Disabled: !testCase.Enabled, Severity: string(testCase.Severity),
		Request: apiaudit.RequestDefinition{Method: string(testCase.Definition.Request.Method), Path: testCase.Definition.Request.Path, Body: body},
		Options: driver.Options,
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
