package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type chatResponseDoer struct {
	status int
	body   string
}

func (doer chatResponseDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: doer.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(doer.body)),
	}, nil
}

func TestChatErrorSchemaRequiresExpectedStatus(t *testing.T) {
	result := RunOpenAIChatCase(
		context.Background(),
		chatResponseDoer{status: http.StatusBadRequest, body: `{"error":{"message":"invalid request","type":"invalid_request_error"}}`},
		RunConfig{BaseURL: "https://example.test", Model: "kimi-k3"},
		CaseDefinition{
			Options: map[string]any{"expected_http_status": float64(400)},
			ID:      "invalid", Name: "invalid", Dimension: "boundary", Protocol: "openai-chat", Kind: "error_schema",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/v1/chat/completions", Body: map[string]any{"messages": []any{}}},
		},
	)

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want %q; evidence=%s", result.Status, StatusPass, result.Evidence)
	}
	if result.Evidence != "HTTP 400 returned OpenAI-style error.message" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
}

func TestChatSuccessRequiresContent(t *testing.T) {
	result := RunOpenAIChatCase(
		context.Background(),
		chatResponseDoer{status: http.StatusOK, body: `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`},
		RunConfig{BaseURL: "https://example.test", Model: "kimi-k3"},
		CaseDefinition{
			ID: "partial", Name: "partial", Dimension: "workflow", Protocol: "openai-chat", Kind: "chat_sync",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/v1/chat/completions", Body: map[string]any{"messages": []any{}}},
			Options: map[string]any{"require_content": true},
		},
	)

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want %q; evidence=%s", result.Status, StatusPass, result.Evidence)
	}
	if result.Evidence != "HTTP 200, 2 content bytes, finish_reason=stop" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
}

func TestChatContractOptionsRejectIncorrectResponses(t *testing.T) {
	tests := []struct {
		name, kind, body string
		status           int
		options          map[string]any
		want             string
	}{
		{"wrong error status", "error_schema", `{"error":{"message":"invalid"}}`, 422, map[string]any{"expected_http_status": float64(400)}, StatusFail},
		{"required content", "chat_sync", `{"choices":[{"message":{"content":""}}]}`, 200, map[string]any{"require_content": true}, StatusFail},
		{"optional content", "chat_sync", `{"choices":[{"message":{"content":""}}]}`, 200, map[string]any{"require_content": false}, StatusPass},
		{"missing image usage", "chat_sync", `{"choices":[{"message":{"content":"7"}}]}`, 200, map[string]any{"require_image_input": true}, StatusFail},
		{"low prompt usage", "chat_sync", `{"choices":[{"message":{"content":"7"}}],"usage":{"prompt_tokens":5}}`, 200, map[string]any{"min_prompt_tokens": float64(10)}, StatusFail},
		{"wrong digit sequence", "chat_sync", `{"choices":[{"message":{"content":"3, 4, 7"}}]}`, 200, map[string]any{"expected_digit_sequence": "374"}, StatusFail},
		{"valid digit sequence", "chat_sync", `{"choices":[{"message":{"content":"3, 7, 4"}}]}`, 200, map[string]any{"expected_digit_sequence": "374"}, StatusPass},
		{"wrong tool", "tool_call", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"wrong"}}]}}]}`, 200, map[string]any{"expected_tool_name": "expected"}, StatusFail},
		{"missing reasoning", "reasoning_visibility", `{"choices":[{"message":{"content":"ok"}}]}`, 200, map[string]any{"expected_reasoning_visible": true}, StatusFail},
		{"unexpected reasoning", "reasoning_visibility", `{"choices":[{"message":{"reasoning_content":"reason"}}]}`, 200, map[string]any{"expected_reasoning_visible": false}, StatusFail},
		{"hidden reasoning", "reasoning_visibility", `{"choices":[{"message":{"content":"ok"}}]}`, 200, map[string]any{"expected_reasoning_visible": false}, StatusPass},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := RunOpenAIChatCase(
				context.Background(),
				chatResponseDoer{status: test.status, body: test.body},
				RunConfig{BaseURL: "https://example.test", Model: "any-chat-model"},
				CaseDefinition{ID: test.name, Protocol: "openai-chat", Kind: test.kind, Options: test.options,
					Request: RequestDefinition{Method: "POST", Path: "/v1/chat/completions", Body: map[string]any{}}},
			)
			if string(result.Status) != test.want {
				t.Fatalf("result = %+v, want %s", result, test.want)
			}
		})
	}
}
