package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestNativePerformancePreservesSemanticTimingDefinitions(t *testing.T) {
	observation := nativePerformanceObservation(0, testspec.Observation{Metrics: map[string]float64{
		"first_frame_ms": 1, "ttft_ms": 10, "ttft_text_ms": 10,
		"second_semantic_ms": 30, "last_semantic_ms": 70, "semantic_chunk_count": 3,
	}})
	if observation.TTST != 30*time.Millisecond || observation.ObservedICL != 30*time.Millisecond {
		t.Fatalf("semantic timing = %+v", observation)
	}
}

type cancellingStream struct{ cancel context.CancelFunc }

func (stream cancellingStream) Read([]byte) (int, error) {
	stream.cancel()
	return 0, context.Canceled
}

func TestNativePerformanceClassifiesCancellationDuringBodyRead(t *testing.T) {
	for _, selected := range []domain.Protocol{domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages} {
		t.Run(string(selected), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(cancellingStream{cancel: cancel}),
				}, nil
			})
			report, err := New(Dependencies{Transport: transport}).RunPerformance(ctx, PerformanceCommand{
				Protocol: selected, AddressMode: AddressModeBaseURL, URL: "https://example.com/v1",
				APIKey: "private-key", ModelID: "k3", RequestCount: 1, Concurrency: 1,
				TimeoutMS: 2000, InputTokens: 2, OutputTokens: 3,
			})
			if err != nil || len(report.Samples) != 1 {
				t.Fatalf("cancelled performance report = %+v, %v", report, err)
			}
			if sample := report.Samples[0]; sample.Success || sample.TimedOut || sample.ErrorCode != load.ErrorCancelled {
				t.Fatalf("body cancellation = %+v", sample)
			}
		})
	}
}

func TestPerformanceUsesNativeTextProtocolAndClassifiesDisconnectedStreams(t *testing.T) {
	for _, selected := range []domain.Protocol{domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages} {
		for _, state := range []string{"valid", "disconnected", "non_terminal"} {
			complete := state == "valid"
			withTerminal := state != "disconnected"
			t.Run(fmt.Sprintf("%s/%s", selected, state), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/proxy"+performancePath(selected) {
						t.Errorf("native endpoint = %s", r.URL.Path)
					}
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["model"] != "k3" || body["stream"] != true {
						t.Errorf("native runtime binding = %+v", body)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					events := []string{}
					if selected == domain.ProtocolOpenAIResponses {
						if body["max_output_tokens"] != float64(3) || body["input"] != "test test" || body["messages"] != nil {
							t.Errorf("Responses body = %+v", body)
						}
						events = []string{
							`{"type":"response.created","response":{"id":"resp1","status":"in_progress","output":[]}}`,
							`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","content":[]}}`,
							`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
							`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ok"}`,
						}
						if withTerminal {
							events = append(events, `{"type":"response.completed","response":{"id":"resp1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`)
						}
					} else {
						if r.Header.Get("anthropic-version") == "" || body["max_tokens"] != float64(3) || body["input"] != nil {
							t.Errorf("Messages body/header = %+v", body)
						}
						events = []string{
							`{"type":"message_start","message":{"id":"msg1","type":"message","content":[],"usage":{"input_tokens":2,"output_tokens":1}}}`,
							`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
							`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
						}
						if withTerminal {
							events = append(events,
								`{"type":"content_block_stop","index":0}`,
								`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
								`{"type":"message_stop"}`,
							)
						}
					}
					if state == "non_terminal" {
						for i, event := range events {
							events[i] = strings.ReplaceAll(strings.ReplaceAll(event, `"status":"completed"`, `"status":"in_progress"`), `"stop_reason":"end_turn"`, `"stop_reason":null`)
						}
					}
					for _, event := range events {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
					}
				}))
				defer server.Close()
				report, err := New(Dependencies{}).RunPerformance(context.Background(), PerformanceCommand{
					Protocol: selected, AddressMode: AddressModeBaseURL, URL: server.URL + "/proxy",
					APIKey: "private-key", ModelID: "k3", RequestCount: 1, Concurrency: 1,
					TimeoutMS: 2000, InputTokens: 2, OutputTokens: 3,
				})
				if err != nil || report.Protocol != selected || report.Success != complete || len(report.Samples) != 1 {
					t.Fatalf("native performance report = %+v, %v", report, err)
				}
				if complete {
					if report.Metrics.PromptTokens != 2 || report.Metrics.CompletionTokens != 3 {
						t.Fatal("native usage lost")
					}
				} else if state == "non_terminal" && report.Samples[0].ErrorCode != load.ErrorProtocol {
					t.Fatalf("invalid native terminal = %+v", report.Samples[0])
				} else if state == "disconnected" && report.Samples[0].ErrorCode != load.ErrorIncompleteStream {
					t.Fatalf("disconnected stream = %+v", report.Samples[0])
				}
				encoded, _ := json.Marshal(report)
				if strings.Contains(string(encoded), "private-key") {
					t.Fatal("report leaked credentials")
				}
			})
		}
	}
}
