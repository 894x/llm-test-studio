package protocols

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/testspec"
)

type headerEvidenceKey struct{}

// WithHeaderEvidence is used only for jobs whose submitting client negotiated
// header evidence support. Older jobs retain their original observation shape.
func WithHeaderEvidence(ctx context.Context) context.Context {
	return context.WithValue(ctx, headerEvidenceKey{}, true)
}

func captureHeaderEvidence(headers http.Header, secret string) *testspec.HeaderEvidence {
	evidence := &testspec.HeaderEvidence{Values: map[string][]string{}}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)
	remaining := 32 << 10
	for index, name := range names {
		if index >= 128 || len(name) > 256 {
			evidence.Truncated = true
			continue
		}
		values := []string{}
		for i, value := range headers[name] {
			if i >= 16 {
				evidence.Truncated = true
				break
			}
			if sensitiveHeader(name) {
				value = "[redacted]"
			} else {
				value = redactText(value, secret)
			}
			if len(value) > 4096 {
				value = "[omitted_size_limit]"
				evidence.Truncated = true
			}
			if len(name)+len(value) > remaining {
				evidence.Truncated = true
				break
			}
			remaining -= len(name) + len(value)
			values = append(values, value)
		}
		if len(values) > 0 {
			evidence.Values[name] = values
		}
	}
	return evidence
}

func sensitiveHeader(name string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
	for _, fragment := range []string{
		"authorization", "apikey", "secret", "password", "credential", "signature", "cookie",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return strings.HasSuffix(normalized, "token") || strings.HasSuffix(normalized, "authenticationinfo")
}
