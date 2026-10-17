package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestFailureEvidenceRemovesActualCredentialBeforePersistence(t *testing.T) {
	for _, format := range []string{"json-value", "json-key", "escaped-json", "text", "request-id", "content-type", "truncated-text"} {
		t.Run(format, func(t *testing.T) {
			secret := "echo-only-credential"
			if format == "escaped-json" {
				secret = "echo-quote\"and\\slash"
			}
			body := `{"message":"controlled error"}`
			contentType := "application/json"
			requestID := "request-1"
			switch format {
			case "json-value", "escaped-json":
				encoded, err := json.Marshal(map[string]string{"message": "controlled error with " + secret})
				if err != nil {
					t.Fatal(err)
				}
				body = string(encoded)
			case "json-key":
				body = `{"` + secret + `":"controlled error"}`
			case "text":
				contentType, body = "text/plain", "controlled error with "+secret
			case "request-id":
				requestID = "request-with-" + secret
			case "content-type":
				contentType = secret + "/plain"
			case "truncated-text":
				contentType, body = "text/plain", strings.Repeat("x", 58)+secret+" end"
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", contentType)
				writer.Header().Set("X-Request-Id", requestID)
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			lease := testLease(t, secret)
			defer lease.Close()
			var evidence FailureResponseEvidence
			client, err := NewClient(
				testChannel(server.URL),
				lease,
				nil,
				WithFailureResponseEvidence(64, func(value FailureResponseEvidence) { evidence = value }),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			executor, err := client.Executor(domain.TestRequest{
				Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{},
				Body: json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{Index: 1})
			if observation.Success || evidence.HTTPStatus != http.StatusInternalServerError {
				t.Fatal("failure did not produce its bounded evidence")
			}
			encoded, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, value := range decoded {
				if text, ok := value.(string); ok && strings.Contains(text, secret) {
					t.Fatal("upstream credential echo survived evidence redaction")
				}
			}
			if format == "truncated-text" && strings.Contains(evidence.Body, secret[:6]) {
				t.Fatal("truncated evidence retained a credential prefix")
			}
		})
	}
}

func TestTruncatedFailureEvidenceRemovesCredentialPrefixes(t *testing.T) {
	for _, secret := range []string{"echo-credential", "ababababx", "凭据测试值", "quote\"and\\slash"} {
		for size := 1; size < len(secret); size++ {
			value := "context:" + secret[:size]
			if actual := redactEvidenceText(value, []byte(secret), true); actual != "context:[REDACTED]" {
				t.Fatal("credential prefix survived a byte-limited capture")
			}
		}
	}
	secret := strings.Repeat("a", 8191) + "b"
	if actual := redactEvidenceText("context:"+secret[:7000], []byte(secret), true); actual != "context:[REDACTED]" {
		t.Fatal("long credential prefix survived capture")
	}
}
