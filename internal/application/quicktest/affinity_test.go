package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPerformanceAffinityAcrossProtocolsWorkloadsAndPhases(t *testing.T) {
	for _, selected := range []domain.Protocol{
		domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages,
	} {
		for _, workload := range []string{"fixed", "random", "normal", "capacity"} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", selected, workload, enabled), func(t *testing.T) {
					var mu sync.Mutex
					keys := []string{}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						key, _ := body["prompt_cache_key"].(string)
						if selected == domain.ProtocolAnthropicMessages {
							if body["prompt_cache_key"] != nil {
								t.Error("Messages received a Chat/Responses affinity field")
							}
							if metadata, ok := body["metadata"].(map[string]any); ok {
								key, _ = metadata["user_id"].(string)
							}
						} else if body["metadata"] != nil {
							t.Error("Chat/Responses received a Messages affinity field")
						}
						mu.Lock()
						keys = append(keys, key)
						mu.Unlock()
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, affinityTestStream(selected))
					}))
					defer server.Close()
					command := PerformanceCommand{
						Protocol: selected, AddressMode: AddressModeBaseURL, URL: server.URL,
						APIKey: "affinity-test-secret", ModelID: "test", AffinityEnabled: enabled,
						RequestCount: 2, Concurrency: 1, TimeoutMS: 2000, InputTokens: 4, OutputTokens: 2,
						WarmupRequests: 1, RampDurationMS: 10, RampRequestCap: 1,
					}
					if workload == "random" || workload == "capacity" {
						command.RandomInput = true
					}
					if workload == "normal" {
						command.WorkloadMode, command.RandomSeed = PerformanceWorkloadNormal, 1
						command.SharedPrefixTokens = 1
					}
					if workload == "capacity" {
						command.RampDurationMS, command.RampRequestCap = 0, 0
						command.CapacityEnabled, command.Concurrency = true, 2
						command.CapacityStart, command.CapacityStep = 1, 1
						command.SLOE2EMS, command.SLOTargetPercent = 5000, 100
					}
					archive := &capturingPerformanceArchive{}
					service := New(Dependencies{
						Archive: archive,
						// Plan entries can retain their identity across executions.
						IDFactory: func(time.Time) (string, error) {
							return "77777777-7777-4777-8777-777777777777", nil
						},
					})
					previousKey := ""
					for range 2 {
						report, err := service.RunPerformance(context.Background(), command)
						if err != nil || !report.Success || report.Profile.AffinityEnabled != enabled {
							t.Fatalf("performance failed: error=%v, code=%s", err, report.ErrorCode)
						}
						if _, err := ValidateArchivedPerformanceReport(report); err != nil {
							t.Fatalf("invalid archived report: %v", err)
						}
						mu.Lock()
						observed := append([]string{}, keys...)
						keys = []string{}
						mu.Unlock()
						if len(observed) < 4 {
							t.Fatalf("expected preparation and measured requests, got %d", len(observed))
						}
						for _, key := range observed {
							if enabled && (key == "" || key != observed[0] || key == previousKey) {
								t.Fatal("affinity key was missing, changed within a run, or reused across runs")
							}
							if !enabled && key != "" {
								t.Fatal("disabled affinity added a session key")
							}
						}
						previousKey = observed[0]
					}
					if len(archive.saved) != 2 || archive.saved[0].Profile.AffinityEnabled != enabled {
						t.Fatal("archive lost the affinity setting")
					}
				})
			}
		}
	}
}

func affinityTestStream(selected domain.Protocol) string {
	if selected == domain.ProtocolOpenAIChat {
		return "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
	}
	events := []string{}
	if selected == domain.ProtocolOpenAIResponses {
		events = []string{
			`{"type":"response.created","response":{"id":"resp1","status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","content":[]}}`,
			`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"ok"}`,
			`{"type":"response.completed","response":{"id":"resp1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
		}
	} else {
		events = []string{
			`{"type":"message_start","message":{"id":"msg1","type":"message","content":[],"usage":{"input_tokens":4,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`{"type":"message_stop"}`,
		}
	}
	var stream string
	for _, event := range events {
		stream += "data: " + event + "\n\n"
	}
	return stream
}
