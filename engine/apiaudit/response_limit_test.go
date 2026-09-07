package apiaudit

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

type responseLimitBody struct {
	*strings.Reader
	closed bool
}

func (body *responseLimitBody) Close() error {
	body.closed = true
	return nil
}

type responseLimitDoer struct {
	body *responseLimitBody
}

func (doer responseLimitDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       doer.body,
	}, nil
}

func TestAuditResponseLimitRejectsTruncatedSuccess(t *testing.T) {
	const limit = 32 << 20
	protocols := []struct {
		name    string
		kind    string
		prefix  string
		padding string
	}{
		{
			name:    "JSON",
			kind:    "chat",
			prefix:  `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`,
			padding: " ",
		},
		{
			name: "SSE",
			kind: "sse_integrity",
			prefix: "data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"OK\"}," +
				"\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			padding: "\n",
		},
	}
	boundaries := []struct {
		name       string
		suffix     string
		wantStatus string
	}{
		{name: "exact limit", wantStatus: StatusPass},
		{name: "one byte over", suffix: "\n", wantStatus: StatusFail},
		{name: "hidden invalid tail", suffix: "data: invalid trailing payload\n\n", wantStatus: StatusFail},
	}
	for _, protocol := range protocols {
		t.Run(protocol.name, func(t *testing.T) {
			prefix := protocol.prefix + strings.Repeat(protocol.padding, limit-len(protocol.prefix))
			for _, boundary := range boundaries {
				t.Run(boundary.name, func(t *testing.T) {
					responseText := prefix + boundary.suffix
					body := &responseLimitBody{Reader: strings.NewReader(responseText)}
					result := RunOpenAIChatCase(
						context.Background(),
						responseLimitDoer{body: body},
						RunConfig{BaseURL: "https://example.test", Model: "model"},
						CaseDefinition{
							Kind: protocol.kind,
							Request: RequestDefinition{
								Method: http.MethodPost,
								Path:   "/chat/completions",
								Body:   map[string]any{},
							},
							Options: map[string]any{"expected_exact": "OK"},
						},
					)
					if result.Status != boundary.wantStatus {
						t.Errorf(
							"status = %q, want %q; evidence: %s",
							result.Status,
							boundary.wantStatus,
							result.Evidence,
						)
					}
					if boundary.wantStatus == StatusFail && !strings.Contains(result.Evidence, "response exceeds") {
						t.Errorf("oversized response lacks a size error: %s", result.Evidence)
					}
					if read := len(responseText) - body.Len(); read > limit+1 {
						t.Errorf("read %d bytes, limit with overflow detection is %d", read, limit+1)
					}
					if !body.closed {
						t.Error("response body was not closed")
					}
				})
			}
		})
	}
}
