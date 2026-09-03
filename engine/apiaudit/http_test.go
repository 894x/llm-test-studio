package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
