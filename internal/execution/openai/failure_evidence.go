package openai

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/diagnostics"
)

const maxAllowedFailureEvidenceBytes int64 = 64 << 10

// FailureResponseEvidence contains only bounded response metadata and a
// redacted body; it never includes request headers or credentials.
type FailureResponseEvidence struct {
	RequestIndex uint64
	HTTPStatus   int
	ContentType  string
	RequestID    string
	Body         string
	BodyBytes    uint64
	Truncated    bool
}

// FailureResponseEvidenceSink receives one safe response snapshot for each
// request that fails after an HTTP response is available.
type FailureResponseEvidenceSink func(FailureResponseEvidence)

// WithFailureResponseEvidence enables opt-in, bounded, redacted capture for
// failed HTTP responses. The default executor behavior does not retain bodies.
func WithFailureResponseEvidence(limit int64, sink FailureResponseEvidenceSink) Option {
	return func(client *Client) error {
		if limit < 1 || limit > maxAllowedFailureEvidenceBytes || sink == nil {
			return ErrInvalidOption
		}
		client.state.maxFailureEvidenceBytes = limit
		client.state.failureEvidenceSink = sink
		return nil
	}
}

type boundedEvidenceCapture struct {
	limit int64
	data  bytes.Buffer
	total uint64
}

func newBoundedEvidenceCapture(limit int64) *boundedEvidenceCapture {
	if limit < 1 {
		return nil
	}
	return &boundedEvidenceCapture{limit: limit}
}

func (capture *boundedEvidenceCapture) Write(value []byte) (int, error) {
	if capture == nil {
		return len(value), nil
	}
	capture.total += uint64(len(value))
	remaining := capture.limit - int64(capture.data.Len())
	if remaining > 0 {
		if int64(len(value)) < remaining {
			remaining = int64(len(value))
		}
		_, _ = capture.data.Write(value[:remaining])
	}
	return len(value), nil
}

func (state *executionState) publishFailureEvidence(index uint64, response *http.Response, capture *boundedEvidenceCapture) {
	if state == nil || state.failureEvidenceSink == nil || response == nil || capture == nil {
		return
	}
	contentType := safeContentType(response.Header.Get("Content-Type"))
	truncated := capture.total > uint64(capture.data.Len())
	body := sanitizeFailureBody(
		capture.data.Bytes(),
		contentType,
		truncated,
		state.secret,
	)
	if redactEvidenceText(contentType, state.secret, false) != contentType {
		contentType = ""
	}
	if int64(len(body)) > state.maxFailureEvidenceBytes {
		body = truncateEvidenceUTF8(body, int(state.maxFailureEvidenceBytes))
		truncated = true
	}
	evidence := FailureResponseEvidence{
		RequestIndex: index,
		HTTPStatus:   response.StatusCode,
		ContentType:  contentType,
		RequestID:    safeRequestID(response.Header, state.secret),
		Body:         body,
		BodyBytes:    capture.total,
		Truncated:    truncated,
	}
	state.failureEvidenceSink(evidence)
}

func safeContentType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || len(mediaType) > 128 {
		return ""
	}
	return strings.ToLower(mediaType)
}

func safeRequestID(header http.Header, secret []byte) string {
	for _, name := range []string{"X-Request-Id", "Request-Id", "Trace-Id"} {
		value := strings.TrimSpace(header.Get(name))
		if value == "" {
			continue
		}
		value = diagnostics.RedactText(redactEvidenceText(value, secret, false))
		value = strings.ToValidUTF8(value, "�")
		return truncateEvidenceUTF8(value, 256)
	}
	return ""
}

func truncateEvidenceUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func sanitizeFailureBody(body []byte, contentType string, truncated bool, secret []byte) string {
	if len(body) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(body, &value) == nil {
		if encoded, err := json.Marshal(redactFailureJSON(value, secret)); err == nil {
			return string(encoded)
		}
	}
	trimmed := bytes.TrimSpace(body)
	looksLikeJSON := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
	if contentType == "application/json" || strings.HasSuffix(contentType, "+json") || (truncated && looksLikeJSON) {
		return "[response omitted: JSON could not be safely decoded]"
	}
	text := strings.ToValidUTF8(redactEvidenceText(string(body), secret, truncated), "�")
	if !utf8.ValidString(text) {
		return ""
	}
	return diagnostics.RedactText(text)
}

func redactFailureJSON(value any, secret []byte) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			redactedKey := redactEvidenceText(key, secret, false)
			if sensitiveEvidenceKey(key) {
				redacted[redactedKey] = "[REDACTED]"
				continue
			}
			redacted[redactedKey] = redactFailureJSON(item, secret)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactFailureJSON(item, secret)
		}
		return redacted
	case string:
		return diagnostics.RedactText(redactEvidenceText(typed, secret, false))
	default:
		return typed
	}
}

func redactEvidenceText(value string, secret []byte, truncated bool) string {
	if len(secret) == 0 {
		return value
	}
	value = strings.ReplaceAll(value, string(secret), "[REDACTED]")
	if truncated {
		// A capture may end in the middle of the known credential. Remove its
		// longest matching prefix before UTF-8 repair or output truncation.
		if size := credentialPrefixAtEnd(value, secret); size > 0 {
			return value[:len(value)-size] + "[REDACTED]"
		}
	}
	return value
}

func credentialPrefixAtEnd(value string, secret []byte) int {
	limit := min(len(secret)-1, len(value))
	if limit <= 0 {
		return 0
	}
	// Keep failure processing linear for long, repetitive credentials.
	fallback := make([]int, limit)
	matched := 0
	for index := 1; index < limit; index++ {
		for matched > 0 && secret[index] != secret[matched] {
			matched = fallback[matched-1]
		}
		if secret[index] == secret[matched] {
			matched++
		}
		fallback[index] = matched
	}
	matched = 0
	for index := len(value) - limit; index < len(value); index++ {
		for matched > 0 && value[index] != secret[matched] {
			matched = fallback[matched-1]
		}
		if value[index] == secret[matched] {
			matched++
		}
	}
	return matched
}

func sensitiveEvidenceKey(key string) bool {
	normalized := strings.NewReplacer(
		"-",
		"",
		"_",
		"",
		" ",
		"",
	).Replace(strings.ToLower(key))
	for _, fragment := range []string{"authorization", "apikey", "accesskey", "secret", "token", "password", "cookie"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
