// Package quicktest provides a zero-persistence Application Core workflow for
// checking one OpenAI-compatible chat-completions connection.
package quicktest

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
)

const (
	SchemaVersion          = 1
	DefaultPrompt          = "Reply with OK only."
	DefaultTimeoutMS int64 = 30_000
	MaxTimeoutMS     int64 = 120_000

	PerformanceSchemaVersion          = 1
	MaxPerformanceRequests     uint64 = 10_000
	MaxPerformanceConcurrency  uint32 = 256
	MaxPerformanceDurationMS   uint64 = 3_600_000
	MaxPerformanceTimeoutMS    uint64 = 600_000
	MaxPerformanceInputTokens  uint32 = 200_000
	MaxPerformanceOutputTokens uint32 = 65_536

	PerformancePhaseNotStarted load.Phase = "not_started"
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

var ErrPerformanceArchiveNotFound = errors.New("quick performance report not found")

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

// PerformanceCommand deliberately repeats only the tested connection fields.
// It must remain independent from persisted Model, Channel, Case, and Plan
// entities so a quick performance run stays zero-persistence.
type PerformanceCommand struct {
	AddressMode  AddressMode `json:"address_mode"`
	URL          string      `json:"url"`
	APIKey       string      `json:"api_key"`
	ModelID      string      `json:"model_id"`
	RequestCount uint64      `json:"request_count"`
	DurationMS   uint64      `json:"duration_ms"`
	Concurrency  uint32      `json:"concurrency"`
	TimeoutMS    uint64      `json:"timeout_ms"`
	InputTokens  uint32      `json:"input_tokens"`
	OutputTokens uint32      `json:"output_tokens"`
}

type PerformanceProfile struct {
	RequestCount uint64 `json:"request_count"`
	DurationMS   uint64 `json:"duration_ms"`
	Concurrency  uint32 `json:"concurrency"`
	TimeoutMS    uint64 `json:"timeout_ms"`
	InputTokens  uint32 `json:"input_tokens"`
	OutputTokens uint32 `json:"output_tokens"`
}

type PerformanceProgress struct {
	Phase           load.Phase `json:"phase"`
	Planned         uint64     `json:"planned"`
	Launched        uint64     `json:"launched"`
	Completed       uint64     `json:"completed"`
	PeakInFlight    uint64     `json:"peak_in_flight"`
	Succeeded       uint64     `json:"succeeded"`
	Failed          uint64     `json:"failed"`
	Rejected        uint64     `json:"rejected"`
	SendDurationMS  float64    `json:"send_duration_ms"`
	DrainDurationMS float64    `json:"drain_duration_ms"`
	TotalDurationMS float64    `json:"total_duration_ms"`
}

type PerformanceFailure struct {
	ErrorCode domain.ErrorCode `json:"error_code"`
	Count     uint64           `json:"count"`
}

type PerformanceArchiveStatus string

const (
	PerformanceArchiveNotAttempted PerformanceArchiveStatus = "not_attempted"
	PerformanceArchiveArchived     PerformanceArchiveStatus = "archived"
	PerformanceArchiveFailed       PerformanceArchiveStatus = "failed"
)

// PerformanceSample is the request-level measurement boundary used by charts.
// It deliberately excludes request/response bodies, prompts, credentials, and
// provider error text.
type PerformanceSample struct {
	RequestIndex      uint64           `json:"request_index"`
	ScheduledOffsetMS float64          `json:"scheduled_offset_ms"`
	StartedOffsetMS   float64          `json:"started_offset_ms"`
	FinishedOffsetMS  float64          `json:"finished_offset_ms"`
	ScheduleLagMS     float64          `json:"schedule_lag_ms"`
	E2EMS             float64          `json:"e2e_ms"`
	TTFTMS            float64          `json:"ttft_ms"`
	TPOTMS            float64          `json:"tpot_ms"`
	HTTPStatus        int              `json:"http_status"`
	Success           bool             `json:"success"`
	TimedOut          bool             `json:"timed_out"`
	PromptTokens      uint64           `json:"prompt_tokens"`
	CompletionTokens  uint64           `json:"completion_tokens"`
	CachedTokens      uint64           `json:"cached_tokens"`
	ErrorCode         domain.ErrorCode `json:"error_code,omitempty"`
}

// PerformanceReport is an ephemeral, bounded report. It contains no Model,
// Channel, Case, Plan, credential, prompt, provider payload, or raw error.
type PerformanceReport struct {
	SchemaVersion int                      `json:"schema_version"`
	ReportID      string                   `json:"report_id,omitempty"`
	GeneratedAt   string                   `json:"generated_at,omitempty"`
	Archived      bool                     `json:"archived"`
	ArchiveStatus PerformanceArchiveStatus `json:"archive_status"`
	Success       bool                     `json:"success"`
	AddressMode   AddressMode              `json:"address_mode"`
	BaseURL       string                   `json:"base_url"`
	Endpoint      string                   `json:"endpoint"`
	ModelID       string                   `json:"model_id"`
	Profile       PerformanceProfile       `json:"profile"`
	Progress      PerformanceProgress      `json:"progress"`
	Metrics       load.Metrics             `json:"metrics"`
	Failures      []PerformanceFailure     `json:"failures"`
	Samples       []PerformanceSample      `json:"samples"`
	ErrorCode     domain.ErrorCode         `json:"error_code,omitempty"`
}

type PerformanceArchive interface {
	SaveQuickPerformanceReport(context.Context, PerformanceReport) error
}

// PerformanceArchiveSummary is the bounded scalar projection used by report
// lists. Full samples are loaded only for an explicitly opened detail.
type PerformanceArchiveSummary struct {
	ReportID    string     `json:"report_id"`
	GeneratedAt string     `json:"generated_at"`
	Success     bool       `json:"success"`
	ModelID     string     `json:"model_id"`
	BaseURL     string     `json:"base_url"`
	Phase       load.Phase `json:"phase"`
	Completed   uint64     `json:"completed"`
	Failed      uint64     `json:"failed"`
}

type PerformanceClock interface {
	Now() time.Time
}

type PerformanceReportIDFactory func(time.Time) (string, error)

type Dependencies struct {
	Transport                   http.RoundTripper
	AllowLoopbackHTTPForTesting bool
	Archive                     PerformanceArchive
	Clock                       PerformanceClock
	IDFactory                   PerformanceReportIDFactory
}
