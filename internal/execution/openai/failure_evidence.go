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
	body := sanitizeFailureBody(capture.data.Bytes(), contentType, truncated)
	if int64(len(body)) > state.maxFailureEvidenceBytes {
		body = truncateEvidenceUTF8(body, int(state.maxFailureEvidenceBytes))
		truncated = true
	}
	evidence := FailureResponseEvidence{
		RequestIndex: index,
		HTTPStatus:   response.StatusCode,
		ContentType:  contentType,
		RequestID:    safeRequestID(response.Header),
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

func safeRequestID(header http.Header) string {
	for _, name := range []string{"X-Request-Id", "Request-Id", "Trace-Id"} {
		value := strings.TrimSpace(header.Get(name))
		if value == "" {
			continue
		}
		value = diagnostics.RedactText(value)
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

func sanitizeFailureBody(body []byte, contentType string, truncated bool) string {
	if len(body) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(body, &value) == nil {
		if encoded, err := json.Marshal(redactFailureJSON(value)); err == nil {
			return string(encoded)
		}
	}
	trimmed := bytes.TrimSpace(body)
	looksLikeJSON := len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
	if contentType == "application/json" || strings.HasSuffix(contentType, "+json") || (truncated && looksLikeJSON) {
		return "[response omitted: JSON could not be safely decoded]"
	}
	text := strings.ToValidUTF8(string(body), "�")
	if !utf8.ValidString(text) {
		return ""
	}
	return diagnostics.RedactText(text)
}

func redactFailureJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveEvidenceKey(key) {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = redactFailureJSON(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactFailureJSON(item)
		}
		return redacted
	case string:
		return diagnostics.RedactText(typed)
	default:
		return typed
	}
}

func sensitiveEvidenceKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{"authorization", "apikey", "accesskey", "secret", "token", "password", "cookie"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
