package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type kimiResponseDoer struct {
	status int
	body   string
}

func (doer kimiResponseDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: doer.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(doer.body)),
	}, nil
}

func TestKimiError400UsesGenericInvalidRequestEvidence(t *testing.T) {
	result := RunKimiK3Case(
		context.Background(),
		kimiResponseDoer{status: http.StatusBadRequest, body: `{"error":{"message":"invalid request","type":"invalid_request_error"}}`},
		RunConfig{BaseURL: "https://example.test", Model: "kimi-k3"},
		CaseDefinition{
			ID: "invalid", Name: "invalid", Dimension: "boundary", Protocol: "kimi-k3", Kind: "kimi_error_400",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/v1/chat/completions", Body: map[string]any{"messages": []any{}}},
		},
	)

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want %q; evidence=%s", result.Status, StatusPass, result.Evidence)
	}
	if result.Evidence != "invalid request returned OpenAI-style HTTP 400" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
}

func TestKimiSuccessRequireContentUsesGenericEvidence(t *testing.T) {
	result := RunKimiK3Case(
		context.Background(),
		kimiResponseDoer{status: http.StatusOK, body: `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`},
		RunConfig{BaseURL: "https://example.test", Model: "kimi-k3"},
		CaseDefinition{
			ID: "partial", Name: "partial", Dimension: "workflow", Protocol: "kimi-k3", Kind: "kimi_success",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/v1/chat/completions", Body: map[string]any{"messages": []any{}}},
			Options: map[string]any{"require_content": true},
		},
	)

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want %q; evidence=%s", result.Status, StatusPass, result.Evidence)
	}
	if result.Evidence != "HTTP 200 returned non-empty assistant content" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
}
