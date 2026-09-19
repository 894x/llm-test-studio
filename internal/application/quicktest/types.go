// Package quicktest executes bounded performance tests for OpenAI-compatible
// connections and archives operational reports without creating authored targets.
package quicktest

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const (
	PerformanceSchemaVersion = 1
	MaxPerformanceRequests           uint64 = 10_000
	MaxPerformanceConcurrency        uint32 = 256
	MaxPerformanceInFlight           uint32 = load.MaxOpenLoopInFlight
	MinPerformanceRatePerSecond             = 0.01
	MaxPerformanceRatePerSecond             = 100_000.0
	MaxPerformanceDurationMS         uint64 = 3_600_000
	MaxPerformanceTimeoutMS          uint64 = 600_000
	MaxPerformanceInputTokens        uint32 = 1_000_000
	MaxPerformanceOutputTokens       uint32 = 65_536
	MaxPerformanceEvidenceBodyBytes         = 16 << 10
	MaxPerformanceEvidenceTotalBytes        = 2 << 20

	PerformancePhaseNotStarted load.Phase = "not_started"
	PerformancePhaseWarmingUp  load.Phase = "warming_up"
	PerformancePhaseRamping    load.Phase = "ramping"
)

type AddressMode string

const (
	AddressModeBaseURL AddressMode = "base_url"
	AddressModeFullURL AddressMode = "full_url"
)

const (
	ErrorInvalidRequest       domain.ErrorCode = "invalid_request"
	ErrorCredentialRequired   domain.ErrorCode = "credential_required"
	ErrorAuthenticationFailed domain.ErrorCode = "authentication_failed"
)

var ErrServiceUnavailable = errors.New("quick test service is unavailable")

var ErrPerformanceArchiveNotFound = errors.New("quick performance report not found")

// PerformanceCommand accepts a temporary or saved connection and optional Suite
// provenance. It does not create authored Model, Channel, Case, or Plan entities.
type PerformanceCommand struct {
	CredentialRunID    string                  `json:"credential_run_id,omitempty"`
	Task               *TaskReference          `json:"task,omitempty"`
	AddressMode        AddressMode             `json:"address_mode"`
	URL                string                  `json:"url"`
	APIKey             string                  `json:"api_key"`
	ChannelID          string                  `json:"channel_id,omitempty"`
	ModelID            string                  `json:"model_id"`
	LoadMode           domain.LoadMode         `json:"load_mode,omitempty"`
	ArrivalPattern     load.ArrivalPattern     `json:"arrival_pattern,omitempty"`
	WorkloadMode       PerformanceWorkloadMode `json:"workload_mode,omitempty"`
	RandomSeed         uint32                  `json:"random_seed,omitempty"`
	RequestCount       uint64                  `json:"request_count"`
	DurationMS         uint64                  `json:"duration_ms"`
	Concurrency        uint32                  `json:"concurrency"`
	RatePerSecond      float64                 `json:"rate_per_second,omitempty"`
	MaxInFlight        uint32                  `json:"max_in_flight,omitempty"`
	TimeoutMS          uint64                  `json:"timeout_ms"`
	InputTokens        uint32                  `json:"input_tokens"`
	OutputTokens       uint32                  `json:"output_tokens"`
	InputTokensStdDev  uint32                  `json:"input_tokens_stddev,omitempty"`
	OutputTokensStdDev uint32                  `json:"output_tokens_stddev,omitempty"`
	SharedPrefixTokens uint32                  `json:"shared_prefix_tokens,omitempty"`
	WarmupRequests     uint64                  `json:"warmup_requests,omitempty"`
	RampDurationMS     uint64                  `json:"ramp_duration_ms,omitempty"`
	RampRequestCap     uint64                  `json:"ramp_request_cap,omitempty"`
	SliceDurationMS    uint64                  `json:"slice_duration_ms,omitempty"`
	SLOTTFTMS          float64                 `json:"slo_ttft_ms,omitempty"`
	SLOTPOTMS          float64                 `json:"slo_tpot_ms,omitempty"`
	SLOE2EMS           float64                 `json:"slo_e2e_ms,omitempty"`
	SLOTargetPercent   float64                 `json:"slo_target_percent,omitempty"`
	CapacityEnabled    bool                    `json:"capacity_enabled,omitempty"`
	CapacityStart      float64                 `json:"capacity_start,omitempty"`
	CapacityStep       float64                 `json:"capacity_step,omitempty"`
}

type PerformanceProfile struct {
	LoadMode           domain.LoadMode         `json:"load_mode,omitempty"`
	ArrivalPattern     load.ArrivalPattern     `json:"arrival_pattern,omitempty"`
	WorkloadMode       PerformanceWorkloadMode `json:"workload_mode,omitempty"`
	RandomSeed         uint32                  `json:"random_seed,omitempty"`
	RequestCount       uint64                  `json:"request_count"`
	DurationMS         uint64                  `json:"duration_ms"`
	Concurrency        uint32                  `json:"concurrency"`
	RatePerSecond      float64                 `json:"rate_per_second,omitempty"`
	MaxInFlight        uint32                  `json:"max_in_flight,omitempty"`
	TimeoutMS          uint64                  `json:"timeout_ms"`
	InputTokens        uint32                  `json:"input_tokens"`
	OutputTokens       uint32                  `json:"output_tokens"`
	InputTokensStdDev  uint32                  `json:"input_tokens_stddev,omitempty"`
	OutputTokensStdDev uint32                  `json:"output_tokens_stddev,omitempty"`
	SharedPrefixTokens uint32                  `json:"shared_prefix_tokens,omitempty"`
	WarmupRequests     uint64                  `json:"warmup_requests,omitempty"`
	RampDurationMS     uint64                  `json:"ramp_duration_ms,omitempty"`
	RampRequestCap     uint64                  `json:"ramp_request_cap,omitempty"`
	SliceDurationMS    uint64                  `json:"slice_duration_ms,omitempty"`
	SLOTTFTMS          float64                 `json:"slo_ttft_ms,omitempty"`
	SLOTPOTMS          float64                 `json:"slo_tpot_ms,omitempty"`
	SLOE2EMS           float64                 `json:"slo_e2e_ms,omitempty"`
	SLOTargetPercent   float64                 `json:"slo_target_percent,omitempty"`
	CapacityEnabled    bool                    `json:"capacity_enabled,omitempty"`
	CapacityStart      float64                 `json:"capacity_start,omitempty"`
	CapacityStep       float64                 `json:"capacity_step,omitempty"`
}

type PerformanceProgress struct {
	Phase              load.Phase `json:"phase"`
	Planned            uint64     `json:"planned"`
	Offered            uint64     `json:"offered,omitempty"`
	Launched           uint64     `json:"launched"`
	Completed          uint64     `json:"completed"`
	InFlight           uint64     `json:"in_flight,omitempty"`
	PeakInFlight       uint64     `json:"peak_in_flight"`
	Succeeded          uint64     `json:"succeeded"`
	Failed             uint64     `json:"failed"`
	Rejected           uint64     `json:"rejected"`
	Stopped            bool       `json:"stopped,omitempty"`
	Capped             bool       `json:"capped,omitempty"`
	CapacityRungNumber uint32     `json:"capacity_rung_number,omitempty"`
	CapacityRungCount  uint32     `json:"capacity_rung_count,omitempty"`
	CapacityTarget     float64    `json:"capacity_target,omitempty"`
	SendDurationMS     float64    `json:"send_duration_ms"`
	DrainDurationMS    float64    `json:"drain_duration_ms"`
	TotalDurationMS    float64    `json:"total_duration_ms"`
}

type PerformanceFailure struct {
	ErrorCode domain.ErrorCode `json:"error_code"`
	Count     uint64           `json:"count"`
}

type PerformanceRequestBudget struct {
	Limit       uint64 `json:"limit"`
	WarmupCap   uint64 `json:"warmup_cap"`
	RampCap     uint64 `json:"ramp_cap"`
	MeasuredCap uint64 `json:"measured_cap"`
	TotalCap    uint64 `json:"total_cap"`
}

type PerformanceTrafficSummary struct {
	RequestCap       uint64               `json:"request_cap"`
	Offered          uint64               `json:"offered"`
	Launched         uint64               `json:"launched"`
	Completed        uint64               `json:"completed"`
	Succeeded        uint64               `json:"succeeded"`
	Failed           uint64               `json:"failed"`
	TimedOut         uint64               `json:"timed_out"`
	Rejected         uint64               `json:"rejected"`
	PeakInFlight     uint64               `json:"peak_in_flight"`
	PromptTokens     uint64               `json:"prompt_tokens"`
	CompletionTokens uint64               `json:"completion_tokens"`
	CachedTokens     uint64               `json:"cached_tokens"`
	SendDurationMS   float64              `json:"send_duration_ms"`
	DrainDurationMS  float64              `json:"drain_duration_ms"`
	TotalDurationMS  float64              `json:"total_duration_ms"`
	Failures         []PerformanceFailure `json:"failures"`
	Stopped          bool                 `json:"stopped"`
	Capped           bool                 `json:"capped"`
}

type PerformanceRampSummary struct {
	Shape               string                    `json:"shape"`
	DurationMS          uint64                    `json:"duration_ms"`
	Steps               uint32                    `json:"steps"`
	TargetConcurrency   uint32                    `json:"target_concurrency,omitempty"`
	TargetRatePerSecond float64                   `json:"target_rate_per_second,omitempty"`
	CompletedWindow     bool                      `json:"completed_window"`
	Traffic             PerformanceTrafficSummary `json:"traffic"`
}

type PerformanceLatencySlice struct {
	Count     uint64  `json:"count"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
	AverageMS float64 `json:"average_ms"`
}

type PerformanceCountSlice struct {
	Count   uint64  `json:"count"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Average float64 `json:"average"`
}

type PerformanceSLOStatus string

const (
	PerformanceSLONotEvaluated PerformanceSLOStatus = "not_evaluated"
	PerformanceSLOPassed       PerformanceSLOStatus = "passed"
	PerformanceSLOFailed       PerformanceSLOStatus = "failed"
)

type PerformanceSLOThresholds struct {
	TTFTMS float64 `json:"ttft_ms"`
	TPOTMS float64 `json:"tpot_ms"`
	E2EMS  float64 `json:"e2e_ms"`
}

type PerformanceSLOViolations struct {
	Transport uint64 `json:"transport"`
	TTFT      uint64 `json:"ttft"`
	TPOT      uint64 `json:"tpot"`
	E2E       uint64 `json:"e2e"`
}

type PerformanceSLOAssessment struct {
	Status             PerformanceSLOStatus     `json:"status"`
	Thresholds         PerformanceSLOThresholds `json:"thresholds"`
	TargetPercent      float64                  `json:"target_percent"`
	TotalRequests      uint64                   `json:"total_requests"`
	GoodRequests       uint64                   `json:"good_requests"`
	BadRequests        uint64                   `json:"bad_requests"`
	GoodRequestPercent float64                  `json:"good_request_percent"`
	GoodputQPS         float64                  `json:"goodput_qps"`
	Violations         PerformanceSLOViolations `json:"violations"`
}

type PerformanceCapacityRung struct {
	Index         uint32                   `json:"index"`
	Target        float64                  `json:"target"`
	Success       bool                     `json:"success"`
	Progress      PerformanceProgress      `json:"progress"`
	Metrics       load.Metrics             `json:"metrics"`
	Failures      []PerformanceFailure     `json:"failures"`
	SLOAssessment PerformanceSLOAssessment `json:"slo_assessment"`
}

type PerformanceCapacityResult struct {
	Status                  PerformanceSLOStatus      `json:"status"`
	SelectedRungIndex       *uint32                   `json:"selected_rung_index,omitempty"`
	HighestPassingRungIndex *uint32                   `json:"highest_passing_rung_index,omitempty"`
	Rungs                   []PerformanceCapacityRung `json:"rungs"`
}

type PerformanceTimeSlice struct {
	SliceIndex         uint64                  `json:"slice_index"`
	StartMS            float64                 `json:"start_ms"`
	EndMS              float64                 `json:"end_ms"`
	Partial            bool                    `json:"partial"`
	Offered            uint64                  `json:"offered"`
	Launched           uint64                  `json:"launched"`
	Completed          uint64                  `json:"completed"`
	Succeeded          uint64                  `json:"succeeded"`
	Failed             uint64                  `json:"failed"`
	Rejected           uint64                  `json:"rejected"`
	PromptTokens       uint64                  `json:"prompt_tokens"`
	CompletionTokens   uint64                  `json:"completion_tokens"`
	CachedTokens       uint64                  `json:"cached_tokens"`
	TTFB               PerformanceLatencySlice `json:"ttfb"`
	TTFTAny            PerformanceLatencySlice `json:"ttft_any"`
	TTFTVisible        PerformanceLatencySlice `json:"ttft_visible"`
	TTFT               PerformanceLatencySlice `json:"ttft"`
	TTST               PerformanceLatencySlice `json:"ttst"`
	ObservedICL        PerformanceLatencySlice `json:"observed_icl"`
	SemanticChunkCount PerformanceCountSlice   `json:"semantic_chunk_count"`
	TPOT               PerformanceLatencySlice `json:"tpot"`
	E2E                PerformanceLatencySlice `json:"e2e"`
}

type PerformanceEvidenceCaptureStatus string

const (
	PerformanceEvidenceCaptured PerformanceEvidenceCaptureStatus = "captured"
	PerformanceEvidenceEmpty    PerformanceEvidenceCaptureStatus = "empty"
	PerformanceEvidenceOmitted  PerformanceEvidenceCaptureStatus = "omitted"
)

// PerformanceResponseEvidence is a bounded, redacted response captured only
// for a failed request. It never contains request headers or credentials.
type PerformanceResponseEvidence struct {
	CaptureStatus PerformanceEvidenceCaptureStatus `json:"capture_status"`
	ContentType   string                           `json:"content_type,omitempty"`
	RequestID     string                           `json:"request_id,omitempty"`
	Body          string                           `json:"body,omitempty"`
	BodyBytes     uint64                           `json:"body_bytes"`
	Truncated     bool                             `json:"truncated"`
	Redacted      bool                             `json:"redacted"`
}

type PerformanceArchiveStatus string

const (
	PerformanceArchiveNotAttempted PerformanceArchiveStatus = "not_attempted"
	PerformanceArchiveArchived     PerformanceArchiveStatus = "archived"
	PerformanceArchiveFailed       PerformanceArchiveStatus = "failed"
)

// PerformanceSample is the request-level measurement boundary used by charts.
// Failed samples may carry a bounded, redacted response. Request bodies,
// prompts, credentials, headers, and unredacted provider text remain excluded.
type PerformanceSample struct {
	RequestIndex       uint64                       `json:"request_index"`
	TargetInputTokens  uint32                       `json:"target_input_tokens,omitempty"`
	TargetOutputTokens uint32                       `json:"target_output_tokens,omitempty"`
	ScheduledOffsetMS  float64                      `json:"scheduled_offset_ms"`
	StartedOffsetMS    float64                      `json:"started_offset_ms"`
	FinishedOffsetMS   float64                      `json:"finished_offset_ms"`
	ScheduleLagMS      float64                      `json:"schedule_lag_ms"`
	E2EMS              float64                      `json:"e2e_ms"`
	TTFBMS             float64                      `json:"ttfb_ms"`
	TTFTAnyMS          float64                      `json:"ttft_any_ms"`
	TTFTVisibleMS      float64                      `json:"ttft_visible_ms"`
	TTFTMS             float64                      `json:"ttft_ms"`
	TTSTMS             float64                      `json:"ttst_ms"`
	ObservedICLMS      float64                      `json:"observed_icl_ms"`
	SemanticChunkCount uint64                       `json:"semantic_chunk_count"`
	TPOTMS             float64                      `json:"tpot_ms"`
	HTTPStatus         int                          `json:"http_status"`
	Success            bool                         `json:"success"`
	TimedOut           bool                         `json:"timed_out"`
	PromptTokens       uint64                       `json:"prompt_tokens"`
	CompletionTokens   uint64                       `json:"completion_tokens"`
	CachedTokens       uint64                       `json:"cached_tokens"`
	ErrorCode          domain.ErrorCode             `json:"error_code,omitempty"`
	ResponseEvidence   *PerformanceResponseEvidence `json:"response_evidence,omitempty"`
}

// PerformanceReport is an ephemeral, bounded report. It contains no Model,
// Channel, Case, Plan, credential, prompt, provider payload, or raw error.
type PerformanceReport struct {
	SchemaVersion  int                        `json:"schema_version"`
	ReportID       string                     `json:"report_id,omitempty"`
	GeneratedAt    string                     `json:"generated_at,omitempty"`
	Archived       bool                       `json:"archived"`
	ArchiveStatus  PerformanceArchiveStatus   `json:"archive_status"`
	Success        bool                       `json:"success"`
	AddressMode    AddressMode                `json:"address_mode"`
	BaseURL        string                     `json:"base_url"`
	Endpoint       string                     `json:"endpoint"`
	ModelID        string                     `json:"model_id"`
	Profile        PerformanceProfile         `json:"profile"`
	RequestBudget  *PerformanceRequestBudget  `json:"request_budget,omitempty"`
	Warmup         *PerformanceTrafficSummary `json:"warmup,omitempty"`
	Ramp           *PerformanceRampSummary    `json:"ramp,omitempty"`
	TimeSlices     []PerformanceTimeSlice     `json:"time_slices,omitempty"`
	SLOAssessment  *PerformanceSLOAssessment  `json:"slo_assessment,omitempty"`
	CapacityResult *PerformanceCapacityResult `json:"capacity_result,omitempty"`
	Progress       PerformanceProgress        `json:"progress"`
	Metrics        load.Metrics               `json:"metrics"`
	Failures       []PerformanceFailure       `json:"failures"`
	Samples        []PerformanceSample        `json:"samples"`
	ErrorCode      domain.ErrorCode           `json:"error_code,omitempty"`
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
	TaskCredential     func(context.Context, string, string) (*credentials.Lease, error)
	TaskPath           func(context.Context, TaskReference, string) (string, error)
	Transport          http.RoundTripper
	ChannelConnections ChannelConnectionResolver
	Archive            PerformanceArchive
	Clock              PerformanceClock
	IDFactory          PerformanceReportIDFactory
}

// TaskReference pins the same Suite definitions used by a quick task, including
// historical runs whose authored definitions may no longer be in the catalog.
type TaskReference struct {
	SuiteID       string `json:"suite_id"`
	SuiteRevision uint64 `json:"suite_revision"`
	SourceRunID   string `json:"source_run_id,omitempty"`
}
