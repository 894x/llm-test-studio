package protocols_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestNativeTextJSONPreservesShapeAndUsesNativeBindings(t *testing.T) {
	for _, test := range []struct {
		protocol                   domain.Protocol
		path, request, response    string
		prompt, completion, cached float64
	}{
		{
			protocol: domain.ProtocolOpenAIResponses, path: "/v1/responses",
			request:  `{"input":"hello","instructions":"brief","max_output_tokens":32}`,
			response: `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"function_call","name":"weather","call_id":"call1","arguments":"{}"},{"type":"message","content":[{"type":"output_text","text":"one"}]},{"type":"message","content":[{"type":"output_text","text":"two"}]}],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}`,
			prompt:   12, completion: 4, cached: 3,
		},
		{
			protocol: domain.ProtocolAnthropicMessages, path: "/v1/messages",
			request:  `{"messages":[{"role":"user","content":"hello"}],"system":"brief","max_tokens":32}`,
			response: `{"id":"msg1","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"one"},{"type":"tool_use","id":"call1","name":"weather","input":{}},{"type":"text","text":"two"}],"stop_reason":"tool_use","usage":{"input_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens":4}}`,
			prompt:   12, completion: 4, cached: 3,
		},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path || r.Method != http.MethodPost {
					t.Errorf("request route = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer private-test-api-key" {
					t.Error("missing runtime credential")
				}
				if test.protocol == domain.ProtocolAnthropicMessages && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing Messages version header")
				}
				var actual, expected map[string]any
				if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
					t.Error(err)
				}
				_ = json.Unmarshal([]byte(test.request), &expected)
				if actual["model"] != "bound-model" {
					t.Error("model binding missing")
				}
				delete(actual, "model")
				actualJSON, _ := json.Marshal(actual)
				expectedJSON, _ := json.Marshal(expected)
				if string(actualJSON) != string(expectedJSON) {
					t.Errorf("native request changed: %s", actualJSON)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			client := testClient(t, server, test.protocol)
			spec := decodeSpec(t, `{"inputs":{},"request":{"body":`+test.request+`},"assertions":[{"id":"text","source":"text","operator":"equals","value":"onetwo"}]}`)
			result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
			if err != nil || len(result.Observation.Issues) > 0 || result.Verdict.Status != testspec.VerdictPassed {
				t.Fatalf("native JSON: %+v, %v", result, err)
			}
			metrics := result.Observation.Metrics
			if metrics["prompt_tokens"] != test.prompt || metrics["completion_tokens"] != test.completion || metrics["cached_tokens"] != test.cached || metrics["tool_call_count"] != 1 {
				t.Fatalf("native usage = %v", metrics)
			}
			var expected, actual any
			_ = json.Unmarshal([]byte(test.response), &expected)
			_ = json.Unmarshal(result.Observation.Response, &actual)
			expectedJSON, _ := json.Marshal(expected)
			actualJSON, _ := json.Marshal(actual)
			if string(expectedJSON) != string(actualJSON) {
				t.Fatal("native response shape was rewritten")
			}
		})
	}
}

func streamEvents(events ...string) string {
	var stream strings.Builder
	for _, event := range events {
		fmt.Fprintf(&stream, "data: %s\n\n", event)
	}
	return stream.String()
}

const responsesStart = `{"type":"response.created","response":{"id":"r1","object":"response","status":"in_progress","output":[]}}`
const responsesFinish = `{"type":"response.completed","response":{"id":"r1","object":"response","status":"completed","output":[{"type":"function_call","name":"weather","arguments":"{\"city\":\"Beijing\"}"},{"type":"message","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}}`
const messagesStart = `{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"stop_reason":null,"usage":{"input_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens":1}}}`

func TestTextStreamsAssembleNativeToolsThinkingAndCumulativeUsage(t *testing.T) {
	for _, test := range []struct {
		protocol domain.Protocol
		stream   string
	}{
		{protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(
			responsesStart,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"weather","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":\"Beijing\"}"}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","content":[]}}`,
			`{"type":"response.content_part.added","output_index":1,"content_index":0,"part":{"type":"output_text","text":""}}`,
			`{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"hi"}`,
			responsesFinish,
		)},
		{protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(
			messagesStart,
			`{"type":"ping"}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call1","name":"weather","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Beijing\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"hi"}}`,
			`{"type":"content_block_stop","index":2}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
			`{"type":"message_stop"}`,
		)},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()
			client := testClient(t, server, test.protocol)
			result, err := client.Execute(context.Background(), protocols.Execution{Spec: decodeSpec(t, `{"inputs":{},"request":{"body":{"stream":true}},"assertions":[]}`)})
			observation := result.Observation
			if err != nil || len(observation.Issues) > 0 || observation.StreamCompleted == nil || !*observation.StreamCompleted || observation.Text == nil || *observation.Text != "hi" {
				t.Fatalf("stream = %+v, %v", observation, err)
			}
			if observation.Metrics["prompt_tokens"] != 12 || observation.Metrics["completion_tokens"] != 4 || observation.Metrics["tool_call_count"] != 1 {
				t.Fatalf("cumulative usage = %v", observation.Metrics)
			}
			_, hasSemanticTTFT := observation.Metrics["ttft_ms"]
			_, hasTextTTFT := observation.Metrics["ttft_text_ms"]
			if !hasSemanticTTFT || !hasTextTTFT || observation.Metrics["ttft_text_ms"] < observation.Metrics["ttft_ms"] {
				t.Fatalf("semantic timings = %v", observation.Metrics)
			}
			if !strings.Contains(string(observation.Response), "Beijing") || len(observation.Exchanges[0].Events) == 0 {
				t.Fatal("tool input or raw event evidence lost")
			}
			if test.protocol == domain.ProtocolAnthropicMessages && !strings.Contains(string(observation.Response), `"signature":"sig"`) {
				t.Fatal("thinking signature lost")
			}
		})
	}
}

func TestTextStreamFailuresRemainObservableAtHTTP200(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol domain.Protocol
		stream   string
		code     string
	}{
		{name: "Responses huge index", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart, `{"type":"response.output_text.delta","output_index":0,"content_index":1e100,"delta":"x"}`), code: "invalid_stream_index"},
		{name: "Responses negative index", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart, `{"type":"response.output_item.added","output_index":-1,"item":{}}`), code: "invalid_stream_index"},
		{name: "Messages fractional index", protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(messagesStart, `{"type":"content_block_start","index":0.5,"content_block":{}}`), code: "invalid_stream_index"},
		{name: "Messages duplicate null block", protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(messagesStart, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`, `{"type":"content_block_start","index":0,"content_block":null}`, `{"type":"content_block_stop","index":0}`), code: "invalid_stream_state"},
		{name: "Responses disconnected", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart), code: "stream_incomplete"},
		{name: "Messages disconnected", protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(messagesStart), code: "stream_incomplete"},
		{name: "Responses incomplete", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart, `{"type":"response.incomplete","response":{"status":"incomplete","output":[]}}`), code: "provider_incomplete"},
		{name: "Responses failed", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart, `{"type":"response.failed","response":{"status":"failed","output":[]}}`), code: "provider_error"},
		{name: "Messages error", protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(messagesStart, `{"type":"error","error":{"type":"overloaded_error"}}`), code: "provider_error"},
		{name: "invalid tool input", protocol: domain.ProtocolAnthropicMessages, stream: streamEvents(messagesStart, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`, `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, `{"type":"message_stop"}`), code: "invalid_tool_arguments"},
		{name: "terminal before start", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesFinish), code: "invalid_stream_state"},
		{name: "data after terminal", protocol: domain.ProtocolOpenAIResponses, stream: streamEvents(responsesStart, responsesFinish, `{"type":"response.output_text.delta","delta":"extra"}`), code: "stream_data_after_terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.stream)
			}))
			defer server.Close()
			result, err := testClient(t, server, test.protocol).Execute(context.Background(), protocols.Execution{Spec: decodeSpec(t, `{"inputs":{},"request":{"body":{"stream":true}},"assertions":[{"id":"clean","source":"issues","operator":"length_equals","value":0}]}`)})
			if err != nil || result.Observation.HTTPStatus == nil || *result.Observation.HTTPStatus != 200 {
				t.Fatalf("HTTP = %+v, %v", result, err)
			}
			found := false
			for _, issue := range result.Observation.Issues {
				if issue.Code == test.code {
					found = true
				}
			}
			if !found || result.Verdict.Status == testspec.VerdictPassed {
				t.Fatalf("protocol failure lost: %+v", result)
			}
		})
	}
}

func TestResponsesSequenceRendersExplicitPreviousResponseReference(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls == 2 && body["previous_response_id"] != "resp_initial" {
			t.Errorf("followup body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_initial","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer server.Close()
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{"input":"first"}},"assertions":[{"id":"text","source":"text","operator":"equals","value":"ok"}],"workflow":{"mode":"sequence","steps":[{"id":"followup","request":{"body":{"input":"second","previous_response_id":{"$response":"initial","pointer":"/id"}}}}]}}`)
	result, err := testClient(t, server, domain.ProtocolOpenAIResponses).Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil || calls != 2 || len(result.Observation.Exchanges) != 2 || result.Verdict.Status != testspec.VerdictPassed {
		t.Fatalf("explicit sequence = %+v, %v; calls=%d", result, err, calls)
	}
}
