package apiaudit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactCaseResultRemovesNestedCredentialAliases(t *testing.T) {
	t.Parallel()

	secrets := []string{
		"opaque-x-api-key",
		"opaque-client-secret",
		"opaque-refresh-token",
		"opaque-id-token",
		"opaque-password",
		"opaque-proxy-authorization",
	}
	result := CaseResult{
		Evidence: "response captured",
		Exchanges: []HTTPExchange{{
			RequestBody: map[string]any{
				"x-api-key": secrets[0],
				"nested": []any{map[string]any{
					"client_secret": secrets[1],
					"refresh_token": secrets[2],
				}},
				"prompt_tokens": float64(12),
			},
			ResponseBody: `{"id_token":"opaque-id-token","password":"opaque-password","usage":{"completion_tokens":4}}`,
		}},
		Usage: map[string]any{
			"proxy_authorization": secrets[5],
			"total_tokens":        float64(16),
		},
	}

	redacted := RedactCaseResult(result, "unrelated-runtime-key")
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal(redacted): %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("redacted result still contains %q: %s", secret, encoded)
		}
	}
	if got := redacted.Exchanges[0].RequestBody["prompt_tokens"]; got != float64(12) {
		t.Errorf("prompt_tokens = %#v, want 12", got)
	}
	if got := redacted.Usage["total_tokens"]; got != float64(16) {
		t.Errorf("total_tokens = %#v, want 16", got)
	}
}
