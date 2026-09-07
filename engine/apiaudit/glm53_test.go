package apiaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const glm53Good = `{"id":"chat-1","request_id":"request-1","created":123,"model":"glm-5.3",
	"choices":[{"index":0,"message":{"role":"assistant","content":"GLM_OK","reasoning_content":"Compute."},
	"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10,
	"prompt_tokens_details":{"cached_tokens":0}}}`

type glm53Reply struct {
	status int
	body   string
}

type glm53Doer struct {
	t        *testing.T
	replies  []glm53Reply
	bodies   []map[string]any
	requests []*http.Request
}

func (d *glm53Doer) Do(request *http.Request) (*http.Response, error) {
	d.t.Helper()
	body := map[string]any{}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		d.t.Fatal(err)
	}
	d.bodies = append(d.bodies, body)
	d.requests = append(d.requests, request)
	if len(d.replies) == 0 {
		d.t.Fatal("unexpected extra HTTP request")
	}
	reply := d.replies[0]
	d.replies = d.replies[1:]
	return &http.Response{StatusCode: reply.status, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
}

func glm53Definition(kind string) CaseDefinition {
	return CaseDefinition{
		ID: "glm53.fixture", Protocol: "openai-chat", Kind: kind,
		Request: RequestDefinition{
			Method: "POST", Path: "/chat/completions", Headers: map[string]string{},
			Body: map[string]any{"messages": []any{map[string]any{"role": "user", "content": "fixture"}}},
		},
		Options: map[string]any{},
	}
}

func glm53Run(t *testing.T, doer *glm53Doer, definition CaseDefinition) CaseResult {
	t.Helper()
	result, err := RunCase(context.Background(), doer, RunConfig{
		Suite: "openai-chat", BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKey: "test-key",
	}, PlannedRun{Model: "glm-5.3", ResultID: "fixture", Case: definition})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGLM53RejectionDoesNotPassOnUnrelatedFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		pass   bool
	}{
		{name: "parameter", status: 400, body: `{"error":{"code":"1214","message":"invalid field"}}`, pass: true},
		{name: "auth", status: 401, body: `{"error":{"code":"1000","message":"auth"}}`},
		{name: "safety", status: 400, body: `{"error":{"code":"1301","message":"safety"}}`},
		{name: "routing", status: 400, body: `{"error":{"code":"1211","message":"model absent"}}`},
		{name: "empty message", status: 400, body: `{"error":{"code":"1214","message":" "}}`},
		{name: "HTML", status: 400, body: `<html>Bad request</html>`},
		{name: "2xx error", status: 200, body: `{"error":{"code":"1214","message":"invalid"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := &glm53Doer{t: t, replies: []glm53Reply{{status: test.status, body: test.body}}}
			definition := glm53Definition("glm53_rejected")
			definition.Options = map[string]any{"expected_http_status": float64(400), "error_codes": []any{"1214"}}
			result := glm53Run(t, d, definition)
			if (result.Status == StatusPass) != test.pass {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestGLM53ModelBoundaryAndAuthorizationReachTransport(t *testing.T) {
	for _, mode := range []string{"missing", "invalid"} {
		d := &glm53Doer{t: t, replies: []glm53Reply{{status: 401, body: `{"error":{"code":"1000","message":"auth"}}`}}}
		definition := glm53Definition("glm53_auth_rejected")
		definition.Options = map[string]any{"auth_mode": mode, "expected_http_status": float64(401), "error_codes": []any{"1000"}}
		glm53Run(t, d, definition)
		want := ""
		if mode == "invalid" {
			want = "Bearer glm-audit-invalid-credential"
		}
		if d.requests[0].Header.Get("Authorization") != want {
			t.Fatal("authentication fixture was overwritten")
		}
	}
	for _, value := range []any{nil, "unknown", float64(5)} {
		d := &glm53Doer{t: t, replies: []glm53Reply{{status: 400, body: `{}`}}}
		definition := glm53Definition("glm53_rejected")
		definition.Options["model_mode"] = "preserve"
		if value != nil {
			definition.Request.Body["model"] = value
		}
		glm53Run(t, d, definition)
		if d.bodies[0]["model"] != value {
			t.Fatal("model boundary was overwritten")
		}
		if d.requests[0].URL.String() != "https://open.bigmodel.cn/api/paas/v4/chat/completions" {
			t.Fatal("wrong endpoint")
		}
	}
}

func TestGLM53SuccessRequiresFinalBusinessOutput(t *testing.T) {
	for _, test := range []struct {
		name, body string
		pass       bool
	}{
		{name: "good", body: glm53Good, pass: true},
		{name: "reasoning only", body: strings.Replace(glm53Good, `"content":"GLM_OK"`, `"content":""`, 1)},
		{name: "network finish", body: strings.Replace(glm53Good, `"stop"`, `"network_error"`, 1)},
		{name: "bad usage", body: strings.Replace(glm53Good, `"total_tokens":10`, `"total_tokens":11`, 1)},
		{name: "bad cache", body: strings.Replace(glm53Good, `"cached_tokens":0`, `"cached_tokens":5`, 1)},
		{name: "no reasoning", body: strings.Replace(glm53Good, `"reasoning_content":"Compute."`, `"reasoning_content":""`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := &glm53Doer{t: t, replies: []glm53Reply{{status: 200, body: test.body}}}
			result := glm53Run(t, d, glm53Definition("glm53_sync"))
			if (result.Status == StatusPass) != test.pass {
				t.Fatalf("result = %s: %s", result.Status, result.Evidence)
			}
		})
	}
}

func TestGLM53TinyBudgetAndJSONAssertions(t *testing.T) {
	body := strings.ReplaceAll(glm53Good, `"stop"`, `"length"`)
	body = strings.ReplaceAll(body, `"content":"GLM_OK"`, `"content":""`)
	body = strings.ReplaceAll(body, `"completion_tokens":6`, `"completion_tokens":1`)
	body = strings.ReplaceAll(body, `"total_tokens":10`, `"total_tokens":5`)
	definition := glm53Definition("glm53_sync")
	definition.Options = map[string]any{"allow_length": true, "max_completion_tokens": float64(1)}
	d := &glm53Doer{t: t, replies: []glm53Reply{{status: 200, body: body}}}
	if result := glm53Run(t, d, definition); result.Status != StatusPass {
		t.Fatal(result.Evidence)
	}
	for _, content := range []string{`{"ok":true}`, `{"ok":false}`, `null`, `[]`} {
		response := map[string]any{}
		json.Unmarshal([]byte(glm53Good), &response)
		message := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
		message["content"] = content
		err := glm53Response(response, map[string]any{"expected_json": map[string]any{"ok": true}})
		if (err == nil) != (content == `{"ok":true}`) {
			t.Fatalf("JSON %s: %v", content, err)
		}
	}
}

func glm53SSE(delta, finish string) string {
	return fmt.Sprintf("data: {\"id\":\"id-1\",\"model\":\"glm-5.3\",\"created\":123,\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, finish)
}

const glm53SSEUsage = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":6,\"total_tokens\":10}}\n\n"

func TestGLM53StreamTerminationAndToolFragments(t *testing.T) {
	start := glm53SSE(`{"reasoning_content":"Think."}`, "null")
	end := glm53SSE(`{"content":"GLM_OK"}`, `"stop"`)
	good := start + end + glm53SSEUsage + "data: [DONE]\n\n"
	for _, raw := range []string{
		strings.Replace(good, "data: [DONE]", "", 1),
		strings.Replace(good, `"stop"`, "null", 1),
		strings.Replace(good, `"content":"GLM_OK"`, `"content":""`, 1),
		start + end + end + glm53SSEUsage + "data: [DONE]\n\n",
		good + "data: {}\n\n",
		strings.Replace(good, `"total_tokens":10`, `"total_tokens":9`, 1),
		strings.Replace(good, `"content":"GLM_OK"`, `"content":42`, 1),
		strings.Replace(good, `"created":123`, `"created":"yesterday"`, 1),
	} {
		if _, err := glm53Stream([]byte(raw), map[string]any{}); err == nil {
			t.Fatal("malformed stream passed")
		}
	}
	if _, err := glm53Stream([]byte(good), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	tool := start + glm53SSE(`{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_fixture","arguments":"{\"label\":"}}]}`, "null")
	tool += glm53SSE(`{"tool_calls":[{"index":0,"function":{"arguments":"\"sample\"}"}}]}`, `"tool_calls"`)
	tool += glm53SSEUsage + "data: [DONE]\n\n"
	options := map[string]any{"expected_tool_name": "read_fixture", "expected_arguments": map[string]any{"label": "sample"}}
	if _, err := glm53Stream([]byte(tool), options); err != nil {
		t.Fatal(err)
	}
	if _, err := glm53Stream([]byte(strings.Replace(tool, "sample", "wrong", 1)), options); err == nil {
		t.Fatal("wrong tool arguments passed")
	}
}

func TestGLM53ToolReplayPreservesReasoningAndSnapshots(t *testing.T) {
	tool := strings.Replace(glm53Good, `"content":"GLM_OK"`, `"content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_fixture","arguments":"{\"label\":\"sample\"}"}}]`, 1)
	tool = strings.Replace(tool, `"stop"`, `"tool_calls"`, 1)
	final := strings.Replace(glm53Good, "GLM_OK", "GLM_FIXTURE_42", 1)
	d := &glm53Doer{t: t, replies: []glm53Reply{{status: 200, body: tool}, {status: 200, body: final}}}
	definition := glm53Definition("glm53_tool_roundtrip")
	definition.Options = map[string]any{
		"expected_tool_name": "read_fixture", "expected_arguments": map[string]any{"label": "sample"},
		"tool_result": "GLM_FIXTURE_42", "expected_final": "GLM_FIXTURE_42",
	}
	result := glm53Run(t, d, definition)
	if result.Status != StatusPass || len(result.Exchanges) != 2 || result.Usage["total_tokens"] != float64(20) {
		t.Fatalf("roundtrip result = %#v", result)
	}
	first := result.Exchanges[0].RequestBody["messages"].([]any)
	second := d.bodies[1]["messages"].([]any)
	if len(first) != 1 || len(second) != 4 {
		t.Fatal("request snapshots were mutated")
	}
	if second[1].(map[string]any)["reasoning_content"] != "Compute." {
		t.Fatal("reasoning was dropped")
	}
	if second[2].(map[string]any)["tool_call_id"] != "call-1" {
		t.Fatal("tool result ID mismatch")
	}
	if strings.Contains(second[3].(map[string]any)["content"].(string), "GLM_FIXTURE_42") {
		t.Fatal("answer leaked into the user prompt instead of coming from the tool result")
	}
}
