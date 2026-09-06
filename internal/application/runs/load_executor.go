package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/execution/openai"
)

var ErrUnsupportedExecutionProtocol = errors.New("runs: unsupported execution protocol")

// LoadExecutor adapts the protocol-neutral scheduler and the OpenAI-compatible
// transport into the Run Application boundary. Legacy semantic evaluators and
// asynchronous video tasks are selected by a higher-level executor router.
type LoadExecutor struct {
	transport http.RoundTripper
}

func NewLoadExecutor(transport http.RoundTripper) *LoadExecutor {
	return &LoadExecutor{transport: transport}
}

func (executor *LoadExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if executor == nil || ctx == nil || request.Credential == nil || emit == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	snapshot := request.Run.Snapshot()
	if snapshot.Channel.Protocol != domain.ProtocolOpenAIChat && snapshot.Channel.Protocol != domain.ProtocolKimiK3 {
		return ErrUnsupportedExecutionProtocol
	}
	client, err := openai.NewClient(snapshot.Channel, request.Credential, executor.transport)
	if err != nil {
		return fmt.Errorf("create protocol client: %w", err)
	}
	defer client.Close()

	caseExecutors := make([]load.Executor, len(request.Cases))
	for index, testCase := range request.Cases {
		if !testCase.Enabled || testCase.Protocol != snapshot.Channel.Protocol ||
			testCase.Definition.Type != casetypes.TypeRequestSingle || testCase.Definition.TypeVersion != 1 {
			return ErrNotRunnable
		}
		var spec casetypes.RequestSingleSpec
		if decodeErr := json.Unmarshal(testCase.Definition.Spec, &spec); decodeErr != nil {
			return fmt.Errorf("decode case %s: %w", testCase.ID, decodeErr)
		}
		caseExecutor, buildErr := client.Executor(spec.Request)
		if buildErr != nil {
			return fmt.Errorf("prepare case %s: %w", testCase.ID, buildErr)
		}
		caseExecutors[index] = caseExecutor
	}
	composite := func(executionContext context.Context, scheduled load.Request) load.Observation {
		return caseExecutors[int(scheduled.Index%uint64(len(caseExecutors)))](executionContext, scheduled)
	}
	outcome, runErr := load.Run(ctx, snapshot.Load, composite, load.Options{StopSending: request.StopSending})
	for _, observation := range outcome.Results {
		caseIndex := int(observation.Index % uint64(len(request.Cases)))
		if err := emit(draftFromObservation(request.Cases[caseIndex].ID, observation)); err != nil {
			return err
		}
	}
	return runErr
}

func draftFromObservation(caseID string, observation load.Observation) ResultDraft {
	draft := ResultDraft{
		CaseID: caseID, RequestID: fmt.Sprintf("request-%d", observation.Index+1),
		Dimensions: cloneDimensions(observation.Dimensions),
		Metrics: map[string]float64{
			"scheduled_offset_ms": milliseconds(observation.ScheduledOffset),
			"started_offset_ms":   milliseconds(observation.StartedOffset),
			"finished_offset_ms":  milliseconds(observation.FinishedOffset),
			"schedule_lag_ms":     milliseconds(observation.ScheduleLag),
			"e2e_ms":              milliseconds(observation.E2E),
			"http_status":         float64(observation.HTTPStatus),
			"prompt_tokens":       float64(observation.PromptTokens),
			"completion_tokens":   float64(observation.CompletionTokens),
			"cached_tokens":       float64(observation.CachedTokens),
		},
	}
	if observation.TTFB > 0 {
		draft.Metrics["ttfb_ms"] = milliseconds(observation.TTFB)
	}
	ttftAny := observation.TTFTAny
	if ttftAny <= 0 {
		ttftAny = observation.TTFT
	}
	if ttftAny > 0 {
		draft.Metrics["ttft_any_ms"] = milliseconds(ttftAny)
		draft.Metrics["ttft_ms"] = draft.Metrics["ttft_any_ms"]
	}
	if observation.TTFTVisible > 0 {
		draft.Metrics["ttft_visible_ms"] = milliseconds(observation.TTFTVisible)
	}
	if observation.SemanticChunkCount >= 2 && observation.TTST > 0 {
		draft.Metrics["ttst_ms"] = milliseconds(observation.TTST)
		draft.Metrics["observed_icl_ms"] = milliseconds(observation.ObservedICL)
	}
	if observation.Streaming {
		draft.Metrics["semantic_chunk_count"] = float64(observation.SemanticChunkCount)
	}
	if observation.CompletionTokens > 1 && observation.E2E > ttftAny && ttftAny > 0 {
		draft.Metrics["tpot_ms"] = milliseconds(observation.E2E-ttftAny) / float64(observation.CompletionTokens-1)
	}
	if observation.StreamComplete {
		draft.Metrics["stream_complete"] = 1
	}
	if observation.Success {
		draft.Success = domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}
		return draft
	}
	draft.ErrorCode = observation.ErrorCode
	if draft.ErrorCode == "" {
		draft.ErrorCode = load.ErrorUnclassified
	}
	switch observation.ErrorCode {
	case load.ErrorTimeout:
		draft.Failure = domain.FailureTimeout
	case load.ErrorCancelled:
		draft.Failure = domain.FailureCancelled
	case load.ErrorHTTP:
		draft.Success.Transport = true
		draft.Failure = domain.FailureHTTP
	case load.ErrorRateLimited:
		draft.Success.Transport = true
		draft.Failure = domain.FailureRateLimit
	case load.ErrorProtocol, load.ErrorIncompleteStream, load.ErrorResponseTooLarge:
		draft.Success.Transport = true
		draft.Failure = domain.FailureProtocol
	case load.ErrorSemanticEmpty:
		draft.Success.Transport = true
		draft.Success.Protocol = true
		draft.Failure = domain.FailureSemantic
	default:
		draft.Failure = domain.FailureNetwork
	}
	return draft
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
