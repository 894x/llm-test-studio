package runs

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// ProtocolExecutor schedules complete Case workflows and immediately emits their
// bounded results. HTTP status and scheduler completion never fabricate a verdict.
type ProtocolExecutor struct {
	registry  *protocols.Registry
	transport http.RoundTripper
}

func NewProtocolExecutor(transport http.RoundTripper) *ProtocolExecutor {
	return &ProtocolExecutor{registry: protocols.NewRegistry(), transport: transport}
}

func (executor *ProtocolExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	started := time.Now()
	if request.Entry.WarmupCount > 0 {
		warmup := request
		warmup.Entry.Load = request.LoadProfile()
		warmup.Entry.Load.Mode = domain.LoadFixedConcurrency
		warmup.Entry.Load.RequestCount = uint64(request.Entry.WarmupCount)
		warmup.Entry.Load.DurationMS = 0
		warmup.Entry.Load.RatePerSecond = 0
		if err := executor.executePhase(ctx, warmup, emit, started, "warmup"); err != nil {
			return err
		}
	}
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if stopped(request.StopSending) {
		return nil
	}
	return executor.executePhase(ctx, request, emit, started, "measured")
}

func (executor *ProtocolExecutor) executePhase(
	ctx context.Context,
	request ExecutionRequest,
	emit func(ResultDraft) error,
	started time.Time,
	phase string,
) error {
	invalid := executor == nil || ctx == nil || request.Credential == nil
	if invalid || emit == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	snapshot := request.Run.Snapshot()
	client, err := protocols.NewClient(executor.registry, request.Credential, snapshot.Channel, executor.transport)
	if err != nil {
		return err
	}
	defer client.Close()
	specs := make([]testspec.Spec, len(request.Cases))
	for index, testCase := range request.Cases {
		if !testCase.Enabled || testCase.Protocol != snapshot.Channel.Protocol {
			return ErrNotRunnable
		}
		spec, err := testspec.Decode(testCase.Definition.Spec)
		if err != nil {
			return err
		}
		if err := executor.registry.Validate(string(testCase.Protocol), spec); err != nil {
			return err
		}
		if _, err := testspec.ValidateInputs(spec.Inputs, request.Entry.CaseInputs[testCase.ID]); err != nil {
			return err
		}
		specs[index] = spec
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	state := executionEmitter{emit: emit, emitted: map[uint64]bool{}, cancel: cancel}
	phaseOffset := time.Since(started)
	settings := request.Entry.Settings
	settings.TimeoutMS = request.LoadProfile().RequestTimeoutMS
	randomItemID := request.Entry.EntryID
	if phase == "warmup" {
		randomItemID += ":warmup"
	}
	seed := uint64(0)
	if snapshot.PlanDocument != nil {
		seed = snapshot.PlanDocument.Seed
	}
	composite := func(executionContext context.Context, scheduled load.Request) load.Observation {
		index := int(scheduled.Index % uint64(len(specs)))
		testCase := request.Cases[index]
		startedOffset := time.Since(started)
		result, executeErr := client.Execute(executionContext, protocols.Execution{
			Spec: specs[index], Inputs: request.Entry.CaseInputs[testCase.ID],
			Random: testspec.RandomContext{
				Seed: seed, ExecutionItemID: randomItemID, MemberID: testCase.ID,
				Iteration: scheduled.Index / uint64(len(specs)),
			},
			Settings: settings,
		})
		draft := draftFromProtocol(testCase.ID, result)
		if executeErr != nil {
			draft.ExecutionStatus = domain.ExecutionFailed
			draft.ErrorCode, draft.Failure = "execution_configuration_failed", domain.FailureProtocol
			draft.Verification = testspec.Evaluate(specs[index].Assertions, testspec.Observation{})
		}
		finishedOffset := time.Since(started)
		draft.RequestID = fmt.Sprintf("%s-%d", phase, scheduled.Index+1)
		draft.Dimensions["phase"] = phase
		draft.Metrics["scheduled_offset_ms"] = milliseconds(phaseOffset + scheduled.ScheduledOffset)
		draft.Metrics["started_offset_ms"] = milliseconds(startedOffset)
		draft.Metrics["finished_offset_ms"] = milliseconds(finishedOffset)
		draft.Metrics["schedule_lag_ms"] = max(0, milliseconds(startedOffset-phaseOffset-scheduled.ScheduledOffset))
		state.send(scheduled.Index, draft)
		return schedulerObservation(scheduled.Index, draft, finishedOffset-startedOffset)
	}
	outcome, runErr := load.Run(runContext, request.LoadProfile(), composite, load.Options{
		StopSending: request.StopSending, RandomSeed: uint32(seed),
	})
	for _, observation := range outcome.Results {
		if state.sent(observation.Index) {
			continue
		}
		index := int(observation.Index % uint64(len(specs)))
		draft := ResultDraft{
			CaseID: request.Cases[index].ID, RequestID: fmt.Sprintf("%s-%d", phase, observation.Index+1),
			Dimensions:      map[string]string{"phase": phase},
			ExecutionStatus: domain.ExecutionFailed, ErrorCode: observation.ErrorCode,
			Failure: domain.FailureNetwork, Verification: testspec.Evaluate(specs[index].Assertions, testspec.Observation{}),
			Metrics: map[string]float64{
				"scheduled_offset_ms": milliseconds(phaseOffset + observation.ScheduledOffset),
				"started_offset_ms":   milliseconds(phaseOffset + observation.StartedOffset),
				"finished_offset_ms":  milliseconds(phaseOffset + observation.FinishedOffset),
				"e2e_ms":              milliseconds(observation.E2E),
			},
		}
		if draft.ErrorCode == "" {
			draft.ErrorCode = "scheduler_did_not_execute"
		}
		state.send(observation.Index, draft)
	}
	if err := state.err(); err != nil {
		return err
	}
	return runErr
}

type executionEmitter struct {
	mu         sync.Mutex
	emit       func(ResultDraft) error
	emitted    map[uint64]bool
	cancel     context.CancelFunc
	firstError error
}

func (state *executionEmitter) send(index uint64, draft ResultDraft) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.firstError != nil {
		return
	}
	if err := state.emit(draft); err != nil {
		state.firstError = err
		state.cancel()
		return
	}
	state.emitted[index] = true
}

func (state *executionEmitter) sent(index uint64) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.emitted[index]
}

func (state *executionEmitter) err() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.firstError
}

func draftFromProtocol(caseID string, result testspec.Result) ResultDraft {
	draft := ResultDraft{
		CaseID: caseID, ExecutionStatus: domain.ExecutionCompleted,
		Verification: result.Verdict, Observation: &result.Observation,
		Metrics: cloneMetrics(result.Observation.Metrics), Dimensions: map[string]string{}, EvidenceIDs: []string{},
	}
	if draft.Metrics == nil {
		draft.Metrics = map[string]float64{}
	}
	if status := result.Observation.HTTPStatus; status != nil {
		draft.Metrics["http_status"] = float64(*status)
	}
	for _, issue := range result.Observation.Issues {
		draft.ExecutionStatus = domain.ExecutionFailed
		draft.ErrorCode = domain.ErrorCode(issue.Code)
		switch issue.Code {
		case "timeout":
			draft.Failure = domain.FailureTimeout
		case "cancelled":
			draft.Failure = domain.FailureCancelled
			draft.ExecutionStatus = domain.ExecutionCancelled
		case "transport_error":
			draft.Failure = domain.FailureNetwork
		default:
			draft.Failure = domain.FailureProtocol
		}
		break
	}
	if draft.ErrorCode == "" && result.Verdict.Status == testspec.VerdictFailed {
		draft.ErrorCode, draft.Failure = "assertion_failed", domain.FailureSemantic
	}
	if draft.ErrorCode == "" && result.Verdict.Status == testspec.VerdictIndeterminate {
		draft.ErrorCode, draft.Failure = "assertion_unavailable", domain.FailureProtocol
	}
	return draft
}

func schedulerObservation(index uint64, draft ResultDraft, elapsed time.Duration) load.Observation {
	observation := load.Observation{
		Index: index, E2E: elapsed, Success: draft.ExecutionStatus == domain.ExecutionCompleted,
		ErrorCode: draft.ErrorCode, Dimensions: map[string]string{},
	}
	if draft.Observation == nil {
		return observation
	}
	if status := draft.Observation.HTTPStatus; status != nil {
		observation.HTTPStatus = *status
	}
	observation.TTFB = time.Duration(draft.Metrics["ttfb_ms"] * float64(time.Millisecond))
	observation.TTFT = time.Duration(draft.Metrics["ttft_ms"] * float64(time.Millisecond))
	observation.PromptTokens = uint64(draft.Metrics["prompt_tokens"])
	observation.CompletionTokens = uint64(draft.Metrics["completion_tokens"])
	observation.CachedTokens = uint64(draft.Metrics["cached_tokens"])
	observation.TimedOut = draft.Failure == domain.FailureTimeout
	return observation
}

func milliseconds(value time.Duration) float64 { return float64(value) / float64(time.Millisecond) }

func stopped(signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

var _ Executor = (*ProtocolExecutor)(nil)
