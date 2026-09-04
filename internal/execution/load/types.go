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

// ArrivalPattern controls only open-loop scheduling. The empty value is kept
// backward compatible and is interpreted as ArrivalConstant.
type ArrivalPattern string

const (
	ArrivalConstant ArrivalPattern = "constant"
	ArrivalPoisson  ArrivalPattern = "poisson"
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
	Index              uint64           `json:"index"`
	ScheduledOffset    time.Duration    `json:"scheduled_offset"`
	StartedOffset      time.Duration    `json:"started_offset"`
	FinishedOffset     time.Duration    `json:"finished_offset"`
	ScheduleLag        time.Duration    `json:"schedule_lag"`
	E2E                time.Duration    `json:"e2e"`
	Streaming          bool             `json:"streaming"`
	TTFB               time.Duration    `json:"ttfb"`
	TTFT               time.Duration    `json:"ttft"`
	TTFTAny            time.Duration    `json:"ttft_any"`
	TTFTVisible        time.Duration    `json:"ttft_visible"`
	TTST               time.Duration    `json:"ttst"`
	ObservedICL        time.Duration    `json:"observed_icl"`
	SemanticChunkCount uint64           `json:"semantic_chunk_count"`
	HTTPStatus         int              `json:"http_status"`
	Success            bool             `json:"success"`
	TimedOut           bool             `json:"timed_out"`
	StreamComplete     bool             `json:"stream_complete"`
	PromptTokens       uint64           `json:"prompt_tokens"`
	CompletionTokens   uint64           `json:"completion_tokens"`
	CachedTokens       uint64           `json:"cached_tokens"`
	ErrorCode          domain.ErrorCode `json:"error_code,omitempty"`
}

type Executor func(context.Context, Request) Observation

type Progress struct {
	Phase         Phase         `json:"phase"`
	Planned       uint64        `json:"planned"`
	Offered       uint64        `json:"offered,omitempty"`
	Launched      uint64        `json:"launched"`
	Completed     uint64        `json:"completed"`
	InFlight      uint64        `json:"in_flight"`
	PeakInFlight  uint64        `json:"peak_in_flight"`
	Succeeded     uint64        `json:"succeeded"`
	Failed        uint64        `json:"failed"`
	Rejected      uint64        `json:"rejected"`
	Stopped       bool          `json:"stopped"`
	Capped        bool          `json:"capped,omitempty"`
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
	// ArrivalPattern selects constant-spacing or a seeded Poisson process for
	// open-loop work. RandomSeed is intentionally request-order independent.
	ArrivalPattern ArrivalPattern
	RandomSeed     uint32
	// Ramp turns a duration-bounded fixed-concurrency or open-loop run into a
	// linear staircase from its minimum load to the configured target.
	Ramp bool
	// MaxScheduledRequests may lower, but never raise, the process-wide request
	// cap. Explicit count-limited profiles above it fail; duration-only profiles
	// stop offering at the cap. Open-loop runs keep their configured send window;
	// fixed-concurrency runs drain immediately once the cap is exhausted.
	MaxScheduledRequests uint64
}
