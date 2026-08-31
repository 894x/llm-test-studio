// Package quicktest provides a zero-persistence Application Core workflow for
// checking one OpenAI-compatible chat-completions connection.
package quicktest

import (
	"errors"
	"net/http"

	"github.com/894x/llm-studio/internal/domain"
)

const (
	SchemaVersion          = 1
	DefaultPrompt          = "Reply with OK only."
	DefaultTimeoutMS int64 = 30_000
	MaxTimeoutMS     int64 = 120_000
)

type AddressMode string

const (
	AddressModeBaseURL AddressMode = "base_url"
	AddressModeFullURL AddressMode = "full_url"
)

const (
	ErrorInvalidRequest       domain.ErrorCode = "invalid_request"
	ErrorInsecureEndpoint     domain.ErrorCode = "insecure_endpoint"
	ErrorCredentialRequired   domain.ErrorCode = "credential_required"
	ErrorAuthenticationFailed domain.ErrorCode = "authentication_failed"
)

var ErrServiceUnavailable = errors.New("quick test service is unavailable")

type Command struct {
	AddressMode AddressMode `json:"address_mode"`
	URL         string      `json:"url"`
	APIKey      string      `json:"api_key"`
	ModelID     string      `json:"model_id"`
	Prompt      string      `json:"prompt"`
	TimeoutMS   int64       `json:"timeout_ms"`
}

// Result is deliberately allowlisted. Provider errors, credentials, and
// request/response payloads never cross this Application Core boundary.
type Result struct {
	SchemaVersion    int              `json:"schema_version"`
	Success          bool             `json:"success"`
	AddressMode      AddressMode      `json:"address_mode"`
	BaseURL          string           `json:"base_url"`
	Endpoint         string           `json:"endpoint"`
	HTTPStatus       int              `json:"http_status"`
	E2EMS            float64          `json:"e2e_ms"`
	PromptTokens     uint64           `json:"prompt_tokens"`
	CompletionTokens uint64           `json:"completion_tokens"`
	CachedTokens     uint64           `json:"cached_tokens"`
	ErrorCode        domain.ErrorCode `json:"error_code,omitempty"`
}

type Dependencies struct {
	Transport                   http.RoundTripper
	AllowLoopbackHTTPForTesting bool
}
