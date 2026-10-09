package quicktest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/diagnostics"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/execution/openai"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func SupportsPerformance(selected domain.Protocol) bool {
	switch selected {
	case domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages:
		return true
	default:
		return false
	}
}

func performancePath(selected domain.Protocol) string {
	descriptor, ok := protocol.Lookup(string(selected))
	if !ok || !SupportsPerformance(selected) {
		return ""
	}
	return descriptor.RequestPaths[0].Path
}

func performanceBody(selected domain.Protocol, prompt string, outputTokens uint32) (json.RawMessage, error) {
	if selected == domain.ProtocolOpenAIResponses {
		return json.Marshal(map[string]any{"input": prompt, "max_output_tokens": outputTokens, "stream": true})
	}
	return json.Marshal(map[string]any{
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": outputTokens, "stream": true,
	})
}

func (service *Service) nativePerformanceExecutor(
	config performanceExecutionConfig,
	lease *credentials.Lease,
	releaseCredential func(),
) (load.Executor, func(), domain.ErrorCode) {
	selected, address := config.Protocol, config.Address
	model, body := config.Model, config.Body
	workload, onFailure := config.Workload, config.OnFailure

	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: quickTestChannelID, Revision: 1},
		Name:              "quick-performance-test", BaseURL: address.baseURL,
		Protocol: selected, UpstreamModelName: model,
	}
	client, err := protocols.NewClient(protocols.NewRegistry(), lease, channel, service.transport)
	_ = lease.Close()
	cleanup := func() {
		if client != nil {
			_ = client.Close()
		}
		releaseCredential()
	}
	if err != nil {
		cleanup()
		return nil, func() {}, ErrorInvalidRequest
	}
	executor := func(ctx context.Context, request load.Request) load.Observation {
		requestBody := body
		if workload != nil {
			target := workload.target(request.Index)
			release, err := sharedPerformanceInputTokenBudget.acquire(ctx, target.InputTokens)
			if err != nil {
				return load.Observation{Index: request.Index, ErrorCode: classifyContext(err)}
			}
			defer release()
			requestBody, err = performanceBody(selected, workload.prompt(request.Index, target.InputTokens), target.OutputTokens)
			if err != nil {
				return load.Observation{Index: request.Index, ErrorCode: load.ErrorRequestFailed}
			}
		}
		started := time.Now()
		result, err := client.Execute(ctx, protocols.Execution{Spec: testspec.Spec{
			Inputs: map[string]testspec.Input{}, Request: testspec.Request{Body: requestBody},
			Assertions: []testspec.Assertion{},
		}})
		observation := nativePerformanceObservation(request.Index, result.Observation)
		observation.E2E = time.Since(started)
		if contextErr := ctx.Err(); contextErr != nil {
			observation.ErrorCode = classifyContext(contextErr)
			observation.Success = false
			observation.TimedOut = observation.ErrorCode == load.ErrorTimeout
		} else if err != nil {
			observation.ErrorCode = load.ErrorRequestFailed
			observation.Success = false
			observation.TimedOut = false
		}
		if !observation.Success && onFailure != nil && observation.HTTPStatus != 0 {
			// The protocol client already removes credential-bearing values. Never
			// retain undecodable raw provider bytes at this reporting boundary.
			payload := string(result.Observation.Response)
			if payload == "" {
				payload = "[response omitted: body could not be safely decoded]"
			}
			payload = diagnostics.RedactText(payload)
			onFailure(openai.FailureResponseEvidence{
				RequestIndex: request.Index, HTTPStatus: observation.HTTPStatus,
				Body:      truncateUTF8(payload, MaxPerformanceEvidenceBodyBytes),
				BodyBytes: uint64(len(payload)), Truncated: len(payload) > MaxPerformanceEvidenceBodyBytes,
			})
		}
		return observation
	}
	return executor, cleanup, ""
}

func nativePerformanceObservation(index uint64, native testspec.Observation) load.Observation {
	metrics := native.Metrics
	duration := func(name string) time.Duration { return time.Duration(metrics[name] * float64(time.Millisecond)) }
	observation := load.Observation{
		Index: index, Streaming: true, TTFB: duration("ttfb_ms"),
		TTFT: duration("ttft_ms"), TTFTAny: duration("ttft_ms"), TTFTVisible: duration("ttft_text_ms"),
		TTST: duration("second_semantic_ms"), SemanticChunkCount: uint64(metrics["semantic_chunk_count"]),
		PromptTokens: uint64(metrics["prompt_tokens"]), CompletionTokens: uint64(metrics["completion_tokens"]),
		CachedTokens: uint64(metrics["cached_tokens"]),
	}
	if observation.SemanticChunkCount > 1 {
		observation.ObservedICL = (duration("last_semantic_ms") - observation.TTFTAny) /
			time.Duration(observation.SemanticChunkCount-1)
	}
	if native.HTTPStatus != nil {
		observation.HTTPStatus = *native.HTTPStatus
	}
	observation.StreamComplete = native.StreamCompleted != nil && *native.StreamCompleted
	observation.ErrorCode = nativePerformanceError(native, observation)
	observation.Success = observation.ErrorCode == ""
	observation.TimedOut = observation.ErrorCode == load.ErrorTimeout
	return observation
}

func nativePerformanceError(native testspec.Observation, observation load.Observation) domain.ErrorCode {
	if observation.HTTPStatus == http.StatusTooManyRequests {
		return load.ErrorRateLimited
	}
	if observation.HTTPStatus != 0 && (observation.HTTPStatus < 200 || observation.HTTPStatus >= 300) {
		return load.ErrorHTTP
	}
	for _, issue := range native.Issues {
		switch issue.Code {
		case "timeout":
			return load.ErrorTimeout
		case "cancelled":
			return load.ErrorCancelled
		case "transport_error":
			return load.ErrorNetwork
		case "response_limit_exceeded", "stream_event_limit_exceeded", "stream_line_limit_exceeded":
			return load.ErrorResponseTooLarge
		case "response_not_terminal":
			if observation.StreamComplete {
				return load.ErrorProtocol
			}
			continue
		case "stream_incomplete":
			return load.ErrorIncompleteStream
		default:
			if strings.Contains(issue.Code, "stream_read") && !observation.StreamComplete {
				return load.ErrorIncompleteStream
			}
			return load.ErrorProtocol
		}
	}
	if !observation.StreamComplete {
		return load.ErrorIncompleteStream
	}
	if observation.SemanticChunkCount == 0 {
		return load.ErrorSemanticEmpty
	}
	return ""
}
