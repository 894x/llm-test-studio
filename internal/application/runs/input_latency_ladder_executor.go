package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/execution/openai"
)

// InputLatencyLadderExecutor owns the stage schedule described by a
// latency.input_ladder case. Plan load settings do not multiply its samples.
type InputLatencyLadderExecutor struct {
	transport http.RoundTripper
}

func NewInputLatencyLadderExecutor(transport http.RoundTripper) *InputLatencyLadderExecutor {
	return &InputLatencyLadderExecutor{transport: transport}
}

func (executor *InputLatencyLadderExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	if executor == nil || ctx == nil || request.Credential == nil || emit == nil || len(request.Cases) == 0 {
		return ErrInvalid
	}
	snapshot := request.Run.Snapshot()
	if snapshot.Channel.Protocol != domain.ProtocolOpenAIChat {
		return ErrUnsupportedExecutionProtocol
	}
	client, err := openai.NewClient(snapshot.Channel, request.Credential, executor.transport)
	if err != nil {
		return fmt.Errorf("create protocol client: %w", err)
	}
	defer client.Close()

	started := time.Now()
	for _, testCase := range request.Cases {
		if !testCase.Enabled || testCase.Protocol != snapshot.Channel.Protocol ||
			testCase.Definition.Type != casetypes.TypeInputLatencyLadder || testCase.Definition.TypeVersion != 2 {
			return ErrNotRunnable
		}
		var spec casetypes.InputLatencyLadderSpec
		if err := json.Unmarshal(testCase.Definition.Spec, &spec); err != nil {
			return fmt.Errorf("decode case %s: %w", testCase.ID, err)
		}
		if err := executeInputLadderCase(ctx, client, testCase, spec, request.StopSending, started, emit); err != nil {
			return err
		}
	}
	return nil
}

func executeInputLadderCase(ctx context.Context, client *openai.Client, testCase domain.TestCase, spec casetypes.InputLatencyLadderSpec, stop <-chan struct{}, started time.Time, emit func(ResultDraft) error) error {
	requestIndex := uint64(0)
	for stageIndex, stage := range spec.Stages {
		target := stage.InputTokens
		warmups, samples := spec.Schedule(stage)
		attempts := int(warmups + samples)
		for attempt := 0; attempt < attempts; attempt++ {
			if stopped(stop) {
				return nil
			}
			prepared, err := ladderRequest(spec, stageIndex, attempt, target)
			if err != nil {
				return fmt.Errorf("prepare case %s stage %d: %w", testCase.ID, stageIndex+1, err)
			}
			execute, err := client.Executor(prepared)
			if err != nil {
				return fmt.Errorf("prepare case %s stage %d transport: %w", testCase.ID, stageIndex+1, err)
			}
			requestContext, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
			startedOffset := time.Since(started)
			observation := execute(requestContext, load.Request{Index: requestIndex})
			observation.ScheduledOffset, observation.StartedOffset, observation.FinishedOffset = startedOffset, startedOffset, time.Since(started)
			cancel()
			requestIndex++
			if attempt < int(warmups) {
				continue
			}
			sampleIndex := attempt - int(warmups) + 1
			draft := draftFromObservation(testCase.ID, observation)
			draft.RequestID = fmt.Sprintf("%s-stage-%02d-sample-%03d", testCase.Key, stageIndex+1, sampleIndex)
			draft.Dimensions = map[string]string{
				"case_type":           string(casetypes.TypeInputLatencyLadder),
				"case_key":            testCase.Key,
				"stage":               fmt.Sprintf("input-%d", target),
				"stage_index":         strconv.Itoa(stageIndex + 1),
				"input_tokens_target": strconv.FormatUint(uint64(target), 10),
				"stage_warmups":       strconv.FormatUint(uint64(warmups), 10),
				"stage_samples":       strconv.FormatUint(uint64(samples), 10),
				"sample":              strconv.Itoa(sampleIndex),
				"cache_mode":          string(spec.CacheMode),
			}
			draft.Metrics["input_tokens_target"] = float64(target)
			if err := emit(draft); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func ladderRequest(spec casetypes.InputLatencyLadderSpec, stageIndex, attempt int, target uint32) (domain.TestRequest, error) {
	var body map[string]any
	if err := json.Unmarshal(spec.Request.Body, &body); err != nil || body == nil {
		return domain.TestRequest{}, ErrNotRunnable
	}
	variation := 0
	if spec.CacheMode == casetypes.CacheModeCold {
		variation = attempt + stageIndex*1000
	}
	prompt := fmt.Sprintf("probe-%06d ", variation) + strings.Repeat("token ", int(target))
	body["messages"] = replaceLastUserMessage(body["messages"], prompt)
	body["stream"] = true
	body["max_tokens"] = spec.OutputTokens
	encoded, err := json.Marshal(body)
	if err != nil {
		return domain.TestRequest{}, err
	}
	prepared := spec.Request
	prepared.Headers = cloneStringMap(spec.Request.Headers)
	prepared.Body = encoded
	return prepared, nil
}

func replaceLastUserMessage(raw any, prompt string) []any {
	messages, ok := raw.([]any)
	if !ok {
		return []any{map[string]any{"role": "user", "content": prompt}}
	}
	cloned := append([]any(nil), messages...)
	for index := len(cloned) - 1; index >= 0; index-- {
		message, ok := cloned[index].(map[string]any)
		if !ok || message["role"] != "user" {
			continue
		}
		updated := make(map[string]any, len(message))
		for key, value := range message {
			updated[key] = value
		}
		updated["content"] = prompt
		cloned[index] = updated
		return cloned
	}
	return append(cloned, map[string]any{"role": "user", "content": prompt})
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}
