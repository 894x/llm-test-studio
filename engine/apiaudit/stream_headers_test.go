package apiaudit

import (
	"context"
	"net/http"
	"testing"
)

func TestObserveStreamForwardsCaseHeadersWithCredentialPrecedence(t *testing.T) {
	tests := []struct {
		name              string
		apiKey            string
		wantAuthorization string
	}{
		{
			name:              "case authorization without configured credentials",
			wantAuthorization: "Bearer case-token",
		},
		{
			name:              "configured credentials override case authorization",
			apiKey:            "configured-token",
			wantAuthorization: "Bearer configured-token",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doer := &userAgentCaptureDoer{responseBody: "data: [DONE]\n\n"}
			_, err := observeStream(
				context.Background(),
				doer,
				RunConfig{BaseURL: "https://example.test", APIKey: test.apiKey},
				RequestDefinition{
					Method: http.MethodPost,
					Path:   "/v1/chat/completions",
					Headers: map[string]string{
						"X-Custom-Routing": "tenant-7",
						"Content-Type":     "text/plain",
						"authorization":    "Bearer case-token",
					},
				},
				map[string]any{"stream": true},
			)
			if err != nil {
				t.Fatalf("observe stream: %v", err)
			}
			if doer.request == nil {
				t.Fatal("expected an HTTP request")
			}
			wantHeaders := map[string]string{
				"X-Custom-Routing": "tenant-7",
				"Content-Type":     "text/plain",
				"Authorization":    test.wantAuthorization,
			}
			for name, want := range wantHeaders {
				if got := doer.request.Header.Get(name); got != want {
					t.Errorf(
						"header %q = %q, want %q",
						name,
						got,
						want,
					)
				}
			}
		})
	}
}
