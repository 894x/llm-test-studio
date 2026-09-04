package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const maxQuickPerformanceReportBytes = 32 << 20

func (repository *Repository) SaveQuickPerformanceReport(ctx context.Context, report quicktest.PerformanceReport) error {
	if report.SchemaVersion != quicktest.PerformanceSchemaVersion {
		return errors.New("save quick performance report requires the current schema version")
	}
	generatedAt, err := validateQuickPerformanceReport(report)
	if err != nil {
		return fmt.Errorf("validate quick performance report before save: %w", err)
	}
	document, err := marshalCanonical(report)
	if err != nil {
		return fmt.Errorf("encode quick performance report: %w", err)
	}
	if len(document) > maxQuickPerformanceReportBytes {
		return errors.New("quick performance report exceeds storage byte limit")
	}
	_, err = repository.conn.ExecContext(ctx, `
		INSERT INTO quick_performance_reports(
			id, generated_at, generated_at_unix_nano, success, model_id, base_url, phase, completed, failed, document_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, report.ReportID, report.GeneratedAt, generatedAt.UnixNano(), report.Success, report.ModelID, report.BaseURL,
		report.Progress.Phase, report.Metrics.Completed, report.Metrics.Failed, document)
	if err != nil {
		return classifyWriteError("save quick performance report", err)
	}
	return nil
}

func (repository *Repository) GetQuickPerformanceReport(ctx context.Context, reportID string) (quicktest.PerformanceReport, error) {
	if !domain.IsUUID(reportID) {
		return quicktest.PerformanceReport{}, errors.New("quick performance report id must be a canonical UUID")
	}
	var generatedAt string
	var generatedAtUnixNano int64
	var document []byte
	err := repository.conn.QueryRowContext(ctx, `
		SELECT generated_at, generated_at_unix_nano, document_json
		FROM quick_performance_reports WHERE id = ?
	`, reportID).Scan(&generatedAt, &generatedAtUnixNano, &document)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return quicktest.PerformanceReport{}, err
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return quicktest.PerformanceReport{}, quicktest.ErrPerformanceArchiveNotFound
		}
		return quicktest.PerformanceReport{}, fmt.Errorf("get quick performance report: %w", err)
	}
	report, parsedAt, err := decodeQuickPerformanceReport(document)
	if err != nil || report.ReportID != reportID || report.GeneratedAt != generatedAt || parsedAt.UnixNano() != generatedAtUnixNano {
		return quicktest.PerformanceReport{}, fmt.Errorf("%w: quick performance report document", ErrCorrupt)
	}
	return report, nil
}

func (repository *Repository) ListQuickPerformanceReportSummaries(ctx context.Context) ([]quicktest.PerformanceArchiveSummary, error) {
	rows, err := repository.conn.QueryContext(ctx, `
		SELECT id, generated_at, generated_at_unix_nano, success, model_id, base_url, phase, completed, failed
		FROM quick_performance_reports
		ORDER BY generated_at_unix_nano DESC, id DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("list quick performance reports: %w", err)
	}
	defer rows.Close()
	summaries := make([]quicktest.PerformanceArchiveSummary, 0)
	for rows.Next() {
		var summary quicktest.PerformanceArchiveSummary
		var generatedAtUnixNano int64
		if err := rows.Scan(
			&summary.ReportID, &summary.GeneratedAt, &generatedAtUnixNano, &summary.Success,
			&summary.ModelID, &summary.BaseURL, &summary.Phase, &summary.Completed, &summary.Failed,
		); err != nil {
			return nil, fmt.Errorf("scan quick performance report: %w", err)
		}
		parsedAt, err := quicktest.ValidatePerformanceArchiveSummary(summary)
		if err != nil || parsedAt.UnixNano() != generatedAtUnixNano {
			return nil, fmt.Errorf("%w: quick performance report summary", ErrCorrupt)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quick performance reports: %w", err)
	}
	return summaries, nil
}

func decodeQuickPerformanceReport(document []byte) (quicktest.PerformanceReport, time.Time, error) {
	if len(document) == 0 || len(document) > maxQuickPerformanceReportBytes {
		return quicktest.PerformanceReport{}, time.Time{}, errors.New("quick performance report document byte limit")
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		return quicktest.PerformanceReport{}, time.Time{}, err
	}

	var report quicktest.PerformanceReport
	var canonical []byte
	switch envelope.SchemaVersion {
	case quicktest.LegacyPerformanceSchemaVersion:
		var legacy quickPerformanceReportV1
		if err := decodeStrictQuickPerformanceDocument(document, &legacy); err != nil {
			return quicktest.PerformanceReport{}, time.Time{}, err
		}
		var err error
		canonical, err = marshalCanonical(legacy)
		if err != nil {
			return quicktest.PerformanceReport{}, time.Time{}, err
		}
		if err := json.Unmarshal(canonical, &report); err != nil {
			return quicktest.PerformanceReport{}, time.Time{}, err
		}
	case quicktest.PerformanceSchemaVersion:
		if err := decodeStrictQuickPerformanceDocument(document, &report); err != nil {
			return quicktest.PerformanceReport{}, time.Time{}, err
		}
		var err error
		canonical, err = marshalCanonical(report)
		if err != nil {
			return quicktest.PerformanceReport{}, time.Time{}, err
		}
	default:
		return quicktest.PerformanceReport{}, time.Time{}, errors.New("quick performance report schema version is unsupported")
	}
	if !bytes.Equal(canonical, document) {
		return quicktest.PerformanceReport{}, time.Time{}, errors.New("quick performance report document is not canonical")
	}
	generatedAt, err := validateQuickPerformanceReport(report)
	if err != nil {
		return quicktest.PerformanceReport{}, time.Time{}, err
	}
	return report, generatedAt, nil
}

func decodeStrictQuickPerformanceDocument(document []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// quickPerformanceReportV1 freezes the exact JSON contract written before
// load-mode and throughput-window reporting were introduced. Decoding legacy
// documents through this wire type keeps fields added in later schemas unknown
// instead of silently accepting them under schema_version 1.
type quickPerformanceReportV1 struct {
	SchemaVersion int                                `json:"schema_version"`
	ReportID      string                             `json:"report_id,omitempty"`
	GeneratedAt   string                             `json:"generated_at,omitempty"`
	Archived      bool                               `json:"archived"`
	ArchiveStatus quicktest.PerformanceArchiveStatus `json:"archive_status"`
	Success       bool                               `json:"success"`
	AddressMode   quicktest.AddressMode              `json:"address_mode"`
	BaseURL       string                             `json:"base_url"`
	Endpoint      string                             `json:"endpoint"`
	ModelID       string                             `json:"model_id"`
	Profile       quickPerformanceProfileV1          `json:"profile"`
	Progress      quickPerformanceProgressV1         `json:"progress"`
	Metrics       quickPerformanceMetricsV1          `json:"metrics"`
	Failures      []quickPerformanceFailureV1        `json:"failures"`
	Samples       []quickPerformanceSampleV1         `json:"samples"`
	ErrorCode     domain.ErrorCode                   `json:"error_code,omitempty"`
}

type quickPerformanceProfileV1 struct {
	RequestCount uint64 `json:"request_count"`
	DurationMS   uint64 `json:"duration_ms"`
	Concurrency  uint32 `json:"concurrency"`
	TimeoutMS    uint64 `json:"timeout_ms"`
	InputTokens  uint32 `json:"input_tokens"`
	OutputTokens uint32 `json:"output_tokens"`
}

type quickPerformanceProgressV1 struct {
	Phase           load.Phase `json:"phase"`
	Planned         uint64     `json:"planned"`
	Launched        uint64     `json:"launched"`
	Completed       uint64     `json:"completed"`
	InFlight        uint64     `json:"in_flight,omitempty"`
	PeakInFlight    uint64     `json:"peak_in_flight"`
	Succeeded       uint64     `json:"succeeded"`
	Failed          uint64     `json:"failed"`
	Rejected        uint64     `json:"rejected"`
	SendDurationMS  float64    `json:"send_duration_ms"`
	DrainDurationMS float64    `json:"drain_duration_ms"`
	TotalDurationMS float64    `json:"total_duration_ms"`
}

type quickPerformanceMetricsV1 struct {
	Completed          uint64  `json:"completed"`
	Succeeded          uint64  `json:"succeeded"`
	Failed             uint64  `json:"failed"`
	TimedOut           uint64  `json:"timed_out"`
	SuccessRatePercent float64 `json:"success_rate_percent"`
	RequestQPS         float64 `json:"request_qps"`
	RPM                float64 `json:"rpm"`
	InputTPM           float64 `json:"input_tpm"`
	OutputTPM          float64 `json:"output_tpm"`
	TotalTPM           float64 `json:"total_tpm"`
	GenerationTPS      float64 `json:"generation_tps"`
	TTFTP50            float64 `json:"ttft_p50_ms"`
	TTFTP90            float64 `json:"ttft_p90_ms"`
	TTFTP95            float64 `json:"ttft_p95_ms"`
	TTFTP99            float64 `json:"ttft_p99_ms"`
	TTFTAverage        float64 `json:"ttft_average_ms"`
	TPOTP50            float64 `json:"tpot_p50_ms"`
	TPOTP90            float64 `json:"tpot_p90_ms"`
	TPOTP95            float64 `json:"tpot_p95_ms"`
	TPOTP99            float64 `json:"tpot_p99_ms"`
	TPOTAverage        float64 `json:"tpot_average_ms"`
	E2EP50             float64 `json:"e2e_p50_ms"`
	E2EP90             float64 `json:"e2e_p90_ms"`
	E2EP95             float64 `json:"e2e_p95_ms"`
	E2EP99             float64 `json:"e2e_p99_ms"`
	E2EAverage         float64 `json:"e2e_average_ms"`
	ScheduleLagP50     float64 `json:"schedule_lag_p50_ms"`
	ScheduleLagP90     float64 `json:"schedule_lag_p90_ms,omitempty"`
	ScheduleLagP95     float64 `json:"schedule_lag_p95_ms"`
	ScheduleLagP99     float64 `json:"schedule_lag_p99_ms,omitempty"`
	ScheduleLagAverage float64 `json:"schedule_lag_average_ms"`
	PromptTokens       uint64  `json:"prompt_tokens"`
	CompletionTokens   uint64  `json:"completion_tokens"`
	CachedTokens       uint64  `json:"cached_tokens"`
	CacheRatePercent   float64 `json:"cache_rate_percent"`
}

type quickPerformanceFailureV1 struct {
	ErrorCode domain.ErrorCode `json:"error_code"`
	Count     uint64           `json:"count"`
}

type quickPerformanceResponseEvidenceV1 struct {
	CaptureStatus quicktest.PerformanceEvidenceCaptureStatus `json:"capture_status"`
	ContentType   string                                     `json:"content_type,omitempty"`
	RequestID     string                                     `json:"request_id,omitempty"`
	Body          string                                     `json:"body,omitempty"`
	BodyBytes     uint64                                     `json:"body_bytes"`
	Truncated     bool                                       `json:"truncated"`
	Redacted      bool                                       `json:"redacted"`
}

type quickPerformanceSampleV1 struct {
	RequestIndex      uint64                              `json:"request_index"`
	ScheduledOffsetMS float64                             `json:"scheduled_offset_ms"`
	StartedOffsetMS   float64                             `json:"started_offset_ms"`
	FinishedOffsetMS  float64                             `json:"finished_offset_ms"`
	ScheduleLagMS     float64                             `json:"schedule_lag_ms"`
	E2EMS             float64                             `json:"e2e_ms"`
	TTFTMS            float64                             `json:"ttft_ms"`
	TPOTMS            float64                             `json:"tpot_ms"`
	HTTPStatus        int                                 `json:"http_status"`
	Success           bool                                `json:"success"`
	TimedOut          bool                                `json:"timed_out"`
	PromptTokens      uint64                              `json:"prompt_tokens"`
	CompletionTokens  uint64                              `json:"completion_tokens"`
	CachedTokens      uint64                              `json:"cached_tokens"`
	ErrorCode         domain.ErrorCode                    `json:"error_code,omitempty"`
	ResponseEvidence  *quickPerformanceResponseEvidenceV1 `json:"response_evidence,omitempty"`
}

func validateQuickPerformanceReport(report quicktest.PerformanceReport) (time.Time, error) {
	return quicktest.ValidateArchivedPerformanceReport(report)
}
