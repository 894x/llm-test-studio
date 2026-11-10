package casebundle_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestKimiProtocolCasesThroughHTTP(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-protocol/*/case.json")
	if err != nil || len(paths) != 4 {
		t.Fatalf("protocol cases = %v, %v", paths, err)
	}
	for _, path := range paths {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		item, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		if item.Default || !item.Enabled || len(item.Definitions) != 3 {
			t.Fatalf("invalid protocol probe configuration: %s", item.Key)
		}
		stream := strings.HasSuffix(item.Key, "_stream")
		usage := strings.Contains(item.Key, "thinking_usage")
		for _, proto := range item.Protocols() {
			spec, err := item.SpecFor(proto)
			if err != nil {
				t.Fatal(err)
			}
			faults := []string{"valid", "wrong_usage", "http_error"}
			if usage {
				faults = append(faults,
					"zero", "missing_usage", "missing_details", "missing_thinking", "null_thinking",
					"string_thinking", "negative_thinking", "fractional_thinking", "excess_thinking",
					"negative_output", "fractional_output", "string_output",
				)
				if proto != domain.ProtocolAnthropicMessages {
					faults = append(faults, "wrong_total")
				}
			} else {
				faults = append(faults,
					"optional_absent", "wrong_id", "wrong_model", "wrong_content",
					"negative_output", "fractional_output", "string_output",
				)
				if proto == domain.ProtocolOpenAIResponses {
					faults = append(faults, "null_usage")
				}
			}
			if stream {
				faults = append(faults, "interrupted", "invalid_event", "long_stream")
				if proto == domain.ProtocolOpenAIChat {
					faults = append(faults, "missing_finish", "after_done")
					if usage {
						faults = append(faults, "usage_only_initial")
					}
				}
				if proto == domain.ProtocolAnthropicMessages && usage {
					faults = append(faults, "partial_usage")
				}
			}
			for _, model := range []string{"kimi-k3", "kimi-k3.1-pro"} {
				for _, fault := range faults {
					t.Run(item.Key+"/"+string(proto)+"/"+model+"/"+fault, func(t *testing.T) {
						fixture := kimiProtocolHTTP{protocol: proto, model: model, stream: stream, fault: fault}
						result := executeKimiProtocolFixture(t, spec, fixture)
						wantPassed := kimiProtocolExpectedPass(fault)
						if (result.Verdict.Status == testspec.VerdictPassed) != wantPassed {
							t.Fatalf("verdict = %s, want passed = %v; issues = %+v",
								result.Verdict.Status, wantPassed, result.Observation.Issues)
						}
						if !wantPassed {
							return
						}
						encoded, err := json.Marshal(result)
						if err != nil {
							t.Fatal(err)
						}
						var restored testspec.Result
						if err := json.Unmarshal(encoded, &restored); err != nil {
							t.Fatal(err)
						}
						if testspec.Evaluate(spec.Assertions, restored.Observation).Status != testspec.VerdictPassed {
							t.Fatal("persisted evidence lost the protocol assertions")
						}
						if usage && proto == domain.ProtocolAnthropicMessages {
							var response struct {
								Usage struct {
									Details struct {
										Thinking int `json:"thinking_tokens"`
									} `json:"output_tokens_details"`
								} `json:"usage"`
							}
							if err := json.Unmarshal(restored.Observation.Response, &response); err != nil {
								t.Fatal(err)
							}
							want := 255
							if fault == "zero" {
								want = 0
							}
							if fault == "partial_usage" {
								want = 25
							}
							if response.Usage.Details.Thinking != want {
								t.Fatalf("thinking = %d, want %d", response.Usage.Details.Thinking, want)
							}
						}
					})
				}
			}
		}
	}
}

func kimiProtocolExpectedPass(fault string) bool {
	switch fault {
	case "valid", "zero", "optional_absent", "null_usage", "partial_usage", "long_stream":
		return true
	default:
		return false
	}
}

type kimiProtocolHTTP struct {
	protocol domain.Protocol
	model    string
	stream   bool
	fault    string
}

func executeKimiProtocolFixture(t *testing.T, spec testspec.Spec, fixture kimiProtocolHTTP) testspec.Result {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["model"] != fixture.model || (body["stream"] == true) != fixture.stream {
			t.Errorf("request model/stream = %v / %v", body["model"], body["stream"])
		}
		if fixture.stream && fixture.protocol == domain.ProtocolOpenAIChat {
			options, _ := body["stream_options"].(map[string]any)
			if options["include_usage"] != true {
				t.Error("streaming probe must request final usage")
			}
		}
		fixture.respond(w)
	}))
	defer server.Close()
	lease, err := credentials.NewTemporaryLease([]byte("local-protocol-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	client, err := protocols.NewClient(
		protocols.NewRegistry(),
		lease,
		domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
			Name:              "local protocol fixture", Protocol: fixture.protocol, BaseURL: server.URL,
			UpstreamModelName: fixture.model,
		},
		server.Client().Transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (fixture kimiProtocolHTTP) root() map[string]any {
	root := map[string]any{"id": "fixture", "model": fixture.model}
	usage := map[string]any{"input_tokens": 126, "output_tokens": 271}
	details := map[string]any{"reasoning_tokens": 255}
	detailName, thinkingName, outputName := "output_tokens_details", "reasoning_tokens", "output_tokens"
	switch fixture.protocol {
	case domain.ProtocolOpenAIChat:
		root["object"], root["created"] = "chat.completion", 1794000000
		root["choices"] = []any{map[string]any{
			"index": 0, "finish_reason": "stop", "message": map[string]any{
				"role": "assistant", "content": "391", "reasoning_content": "17 * 23 = 391",
			},
		}}
		usage = map[string]any{"prompt_tokens": 126, "completion_tokens": 271, "total_tokens": 397}
		detailName, outputName = "completion_tokens_details", "completion_tokens"
	case domain.ProtocolOpenAIResponses:
		root["object"], root["created_at"], root["status"] = "response", 1794000000, "completed"
		root["output"] = []any{map[string]any{
			"type": "message", "id": "message_fixture", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "391"}},
		}}
		usage["total_tokens"] = 397
	case domain.ProtocolAnthropicMessages:
		root["type"], root["role"], root["stop_reason"], root["stop_sequence"] = "message", "assistant", "end_turn", nil
		root["content"] = []any{
			map[string]any{"type": "thinking", "thinking": "17 * 23 = 391", "signature": "fixture"},
			map[string]any{"type": "text", "text": "391"},
		}
		thinkingName = "thinking_tokens"
		details = map[string]any{thinkingName: 255}
	}
	usage[detailName] = details
	root["usage"] = usage
	switch fixture.fault {
	case "optional_absent":
		delete(root, "id")
		delete(root, "model")
		delete(root, "usage")
	case "wrong_id":
		root["id"] = 42
	case "wrong_model":
		root["model"] = 42
	case "wrong_content":
		root["choices"], root["content"], root["output"] = "invalid", "invalid", "invalid"
	case "wrong_usage":
		root["usage"] = "invalid"
	case "null_usage":
		root["usage"] = nil
	case "missing_usage":
		delete(root, "usage")
	case "missing_details":
		delete(usage, detailName)
	case "missing_thinking":
		delete(details, thinkingName)
	case "null_thinking":
		details[thinkingName] = nil
	case "zero":
		details[thinkingName] = 0
	case "string_thinking":
		details[thinkingName] = "255"
	case "negative_thinking":
		details[thinkingName] = -1
	case "fractional_thinking":
		details[thinkingName] = 1.5
	case "excess_thinking":
		details[thinkingName] = 272
	case "negative_output":
		usage[outputName] = -1
	case "fractional_output":
		usage[outputName] = 271.5
	case "string_output":
		usage[outputName] = "271"
	case "wrong_total":
		usage["total_tokens"] = 652
	}
	return root
}

func (fixture kimiProtocolHTTP) respond(w http.ResponseWriter) {
	root := fixture.root()
	if fixture.fault == "http_error" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "fixture rejection"}})
		return
	}
	if !fixture.stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(root)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	events := fixture.events(root)
	if fixture.fault == "interrupted" {
		events = events[:len(events)-1]
	}
	if fixture.fault == "invalid_event" {
		field := "type"
		if fixture.protocol == domain.ProtocolOpenAIChat {
			field = "object"
		}
		if fixture.protocol == domain.ProtocolOpenAIResponses {
			field = "sequence_number"
		}
		events[0][field] = []any{}
	}
	if fixture.fault == "long_stream" {
		padding := make([]map[string]any, 0, 260)
		for index := range 260 {
			event := map[string]any{"type": "ping", "sequence_number": index + 1}
			if fixture.protocol == domain.ProtocolOpenAIChat {
				event = map[string]any{"choices": []any{}}
			}
			padding = append(padding, event)
		}
		events = append(events[:1], append(padding, events[1:]...)...)
	}
	for _, event := range events {
		raw, _ := json.Marshal(event)
		_, _ = w.Write(append(append([]byte("data: "), raw...), '\n', '\n'))
	}
	if fixture.protocol == domain.ProtocolOpenAIChat && fixture.fault != "interrupted" {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fixture.fault == "after_done" {
			_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
		}
	}
}

func (fixture kimiProtocolHTTP) events(root map[string]any) []map[string]any {
	if fixture.protocol == domain.ProtocolOpenAIResponses {
		return []map[string]any{
			{"type": "response.created", "sequence_number": 0, "response": map[string]any{"status": "in_progress"}},
			{"type": "response.completed", "sequence_number": 1, "response": root},
		}
	}
	if fixture.protocol == domain.ProtocolOpenAIChat {
		chunk := func(choices []any) map[string]any {
			return map[string]any{
				"id": root["id"], "model": root["model"], "created": root["created"],
				"object": "chat.completion.chunk", "choices": choices,
			}
		}
		first := chunk([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}})
		last := chunk([]any{map[string]any{
			"index": 0, "delta": map[string]any{"content": "391", "reasoning_content": "17 * 23 = 391"},
			"finish_reason": "stop",
		}})
		usage := chunk([]any{})
		if value, exists := root["usage"]; exists {
			usage["usage"] = value
		}
		if fixture.fault == "wrong_content" {
			last["choices"].([]any)[0].(map[string]any)["delta"] = map[string]any{"content": 42}
		}
		if fixture.fault == "missing_finish" {
			delete(last["choices"].([]any)[0].(map[string]any), "finish_reason")
		}
		if fixture.fault == "usage_only_initial" {
			first["usage"] = usage["usage"]
			delete(usage, "usage")
		}
		if fixture.fault == "optional_absent" {
			for _, event := range []map[string]any{first, last, usage} {
				delete(event, "id")
				delete(event, "model")
			}
		}
		return []map[string]any{first, last, usage}
	}
	usage, _ := root["usage"].(map[string]any)
	start := map[string]any{}
	for key, value := range root {
		start[key] = value
	}
	start["content"] = []any{}
	start["stop_reason"] = nil
	initialUsage := map[string]any{"input_tokens": 126, "output_tokens": 0}
	start["usage"] = initialUsage
	if fixture.fault == "optional_absent" || fixture.fault == "missing_usage" {
		delete(start, "usage")
	}
	if fixture.fault == "zero" || fixture.fault == "partial_usage" {
		initialUsage["output_tokens_details"] = map[string]any{"thinking_tokens": 25}
	}
	if fixture.fault == "partial_usage" {
		delete(usage, "output_tokens_details")
	}
	text := any("391")
	if fixture.fault == "wrong_content" {
		text = 42
	}
	deltaUsage := any(usage)
	if fixture.fault == "wrong_usage" {
		// Preserve the malformed field on message_start too; the decoder must not make it valid evidence.
		start["usage"] = "invalid"
		deltaUsage = "invalid"
	}
	events := []map[string]any{
		{"type": "message_start", "message": start},
		{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
		{"type": "content_block_stop", "index": 0},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": deltaUsage},
		{"type": "message_stop"},
	}
	if fixture.fault == "optional_absent" || fixture.fault == "missing_usage" {
		delete(events[4], "usage")
	}
	return events
}
