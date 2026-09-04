package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/engine/common"
)

type userAgentCaptureDoer struct {
	request      *http.Request
	responseBody string
}

func (doer *userAgentCaptureDoer) Do(request *http.Request) (*http.Response, error) {
	doer.request = request
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(doer.responseBody)),
	}, nil
}

func TestAuditRequestsUseLLMTestStudioUserAgent(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		run          func(HTTPDoer) error
	}{
		{
			name:         "regular request",
			responseBody: `{}`,
			run: func(doer HTTPDoer) error {
				_, _, err := performRequest(
					context.Background(),
					doer,
					RunConfig{BaseURL: "https://example.com"},
					RequestDefinition{Method: http.MethodGet, Path: "/v1/models"},
					nil,
				)
				return err
			},
		},
		{
			name:         "stream request",
			responseBody: "data: [DONE]\n\n",
			run: func(doer HTTPDoer) error {
				_, err := observeStream(
					context.Background(),
					doer,
					RunConfig{BaseURL: "https://example.com"},
					RequestDefinition{Method: http.MethodPost, Path: "/v1/chat/completions"},
					map[string]any{"stream": true},
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doer := &userAgentCaptureDoer{responseBody: test.responseBody}
			if err := test.run(doer); err != nil {
				t.Fatalf("run request: %v", err)
			}
			if doer.request == nil {
				t.Fatal("expected an HTTP request")
			}
			if got := doer.request.Header.Get("User-Agent"); got != "llm-test-studio/1.0" {
				t.Fatalf("User-Agent = %q, want %q", got, "llm-test-studio/1.0")
			}
		})
	}
}

func TestPerformRequestForwardsCaseDefinedHeaders(t *testing.T) {
	var definition RequestDefinition
	if err := common.Unmarshal([]byte(`{"method":"POST","path":"/video","headers":{"X-DashScope-Async":"enable"}}`), &definition); err != nil {
		t.Fatalf("decode request definition: %v", err)
	}
	doer := &userAgentCaptureDoer{responseBody: `{}`}
	if _, _, err := performRequest(context.Background(), doer, RunConfig{BaseURL: "https://example.test"}, definition, map[string]any{}); err != nil {
		t.Fatalf("perform request: %v", err)
	}
	if got := doer.request.Header.Get("X-DashScope-Async"); got != "enable" {
		t.Fatalf("X-DashScope-Async = %q, want enable", got)
	}
}

func TestPerformRequestAllowsCaseToOverrideContentTypeForBoundaryTesting(t *testing.T) {
	doer := &userAgentCaptureDoer{responseBody: `{}`}
	definition := RequestDefinition{
		Method:  http.MethodPost,
		Path:    "/boundary",
		Headers: map[string]string{"Content-Type": "text/plain"},
	}
	_, _, err := performRequest(context.Background(), doer, RunConfig{BaseURL: "https://workspace.example"}, definition, map[string]any{"input": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := doer.request.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want case-defined boundary value", got)
	}
}
