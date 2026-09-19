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
	_, err = repository.db.ExecContext(ctx, `
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
	err := repository.db.QueryRowContext(ctx, `
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
	rows, err := repository.db.QueryContext(ctx, `
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
	var report quicktest.PerformanceReport
	if err := decodeStrictQuickPerformanceDocument(document, &report); err != nil {
		return quicktest.PerformanceReport{}, time.Time{}, err
	}
	if report.SchemaVersion != quicktest.PerformanceSchemaVersion {
		return quicktest.PerformanceReport{}, time.Time{}, errors.New("quick performance report schema version is unsupported")
	}
	canonical, err := marshalCanonical(report)
	if err != nil {
		return quicktest.PerformanceReport{}, time.Time{}, err
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

func validateQuickPerformanceReport(report quicktest.PerformanceReport) (time.Time, error) {
	return quicktest.ValidateArchivedPerformanceReport(report)
}
