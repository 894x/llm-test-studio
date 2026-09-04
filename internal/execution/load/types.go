// Package load schedules and measures local load-test work without knowing the
// HTTP protocol, persistence adapter, CLI, or desktop shell.
package load

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxConcurrency      = 2_000
	MaxOpenLoopInFlight = 2_000
	MaxRequests         = 1_000_000
)

type Phase string

const (
	PhaseSending   Phase = "sending"
	PhaseDraining  Phase = "draining"
	PhaseCompleted Phase = "completed"
	PhaseCancelled Phase = "cancelled"
)

const (
	ErrorNetwork           domain.ErrorCode = "network_error"
	ErrorTimeout           domain.ErrorCode = "timeout"
	ErrorCancelled         domain.ErrorCode = "cancelled"
	ErrorHTTP              domain.ErrorCode = "http_error"
	ErrorRateLimited       domain.ErrorCode = "rate_limited"
	ErrorProtocol          domain.ErrorCode = "protocol_error"
	ErrorIncompleteStream  domain.ErrorCode = "incomplete_stream"
	ErrorSemanticEmpty     domain.ErrorCode = "semantic_empty"
	ErrorResponseTooLarge  domain.ErrorCode = "response_too_large"
	ErrorClientClosed      domain.ErrorCode = "client_closed"
	ErrorSchedulerOverload domain.ErrorCode = "scheduler_overload"
	ErrorExecutorPanic     domain.ErrorCode = "executor_panic"
	ErrorRequestFailed     domain.ErrorCode = "request_failed"
	ErrorUnclassified      domain.ErrorCode = "unclassified_error"
)

type Request struct {
	Index           uint64        `json:"index"`
	ScheduledOffset time.Duration `json:"scheduled_offset"`
}

// Observation contains only measurement and classification data. Request and
// response evidence belongs to the redacted artifact boundary, not the scheduler.
type Observation struct {
	Index            uint64            `json:"index"`
	ScheduledOffset  time.Duration     `json:"scheduled_offset"`
	StartedOffset    time.Duration     `json:"started_offset"`
	FinishedOffset   time.Duration     `json:"finished_offset"`
	ScheduleLag      time.Duration     `json:"schedule_lag"`
	E2E              time.Duration     `json:"e2e"`
	TTFT             time.Duration     `json:"ttft"`
	HTTPStatus       int               `json:"http_status"`
	Success          bool              `json:"success"`
	TimedOut         bool              `json:"timed_out"`
	StreamComplete   bool              `json:"stream_complete"`
	PromptTokens     uint64            `json:"prompt_tokens"`
	CompletionTokens uint64            `json:"completion_tokens"`
	CachedTokens     uint64            `json:"cached_tokens"`
	ErrorCode        domain.ErrorCode  `json:"error_code,omitempty"`
	Dimensions       map[string]string `json:"dimensions,omitempty"`
}

type Executor func(context.Context, Request) Observation

type Progress struct {
	Phase         Phase         `json:"phase"`
	Planned       uint64        `json:"planned"`
	Launched      uint64        `json:"launched"`
	Completed     uint64        `json:"completed"`
	InFlight      uint64        `json:"in_flight"`
	PeakInFlight  uint64        `json:"peak_in_flight"`
	Succeeded     uint64        `json:"succeeded"`
	Failed        uint64        `json:"failed"`
	Rejected      uint64        `json:"rejected"`
	Stopped       bool          `json:"stopped"`
	SendDuration  time.Duration `json:"send_duration"`
	DrainDuration time.Duration `json:"drain_duration"`
	TotalDuration time.Duration `json:"total_duration"`
}

type Outcome struct {
	Progress Progress      `json:"progress"`
	Results  []Observation `json:"results"`
	Metrics  Metrics       `json:"metrics"`
}

type Options struct {
	// Closing StopSending prevents new work from launching while allowing
	// already-started requests to drain normally.
	StopSending <-chan struct{}
	OnProgress  func(Progress)
	// MaxOpenLoopInFlight may lower, but never raise, the process safety limit.
	// It is primarily useful for constrained hosts and deterministic tests.
	MaxOpenLoopInFlight uint64
}
