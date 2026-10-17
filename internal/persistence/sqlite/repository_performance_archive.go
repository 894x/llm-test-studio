package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) ReadPerformanceArchive(
	ctx context.Context,
	jobID string,
	reportID string,
) (jobhost.PerformanceArchiveDocument, error) {
	if !domain.IsUUID(jobID) || !domain.IsUUID(reportID) {
		return jobhost.PerformanceArchiveDocument{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobhost.PerformanceArchiveDocument{}, fmt.Errorf("begin performance archive read: %w", err)
	}
	defer tx.Rollback()
	spec, snapshot, err := queryPerformanceOwner(ctx, tx, jobID)
	if err != nil {
		return jobhost.PerformanceArchiveDocument{}, err
	}
	if spec.Spec.Performance.ReportID != reportID {
		return jobhost.PerformanceArchiveDocument{}, jobhost.ErrNotFound
	}
	sealed, err := queryPerformanceResult(
		ctx,
		tx,
		spec,
		snapshot,
	)
	if err != nil {
		return jobhost.PerformanceArchiveDocument{}, err
	}
	expected, eligible, err := sealed.Result.StatisticalArchive()
	if err != nil {
		return jobhost.PerformanceArchiveDocument{}, err
	}
	if !eligible {
		return jobhost.PerformanceArchiveDocument{}, jobhost.ErrNotFound
	}
	var document []byte
	var generatedAt string
	var generatedAtNano int64
	err = tx.QueryRowContext(
		ctx,
		`SELECT generated_at, generated_at_unix_nano,
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM quick_performance_reports WHERE id = ?`,
		maxQuickPerformanceReportBytes,
		reportID,
	).Scan(
		&generatedAt, &generatedAtNano, &document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobhost.PerformanceArchiveDocument{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobhost.PerformanceArchiveDocument{}, fmt.Errorf("read performance archive: %w", err)
	}
	report, parsedAt, err := decodeQuickPerformanceReport(document)
	validTime := generatedAt == report.GeneratedAt && generatedAtNano == parsedAt.UnixNano()
	if err != nil || !validTime || !equalCanonicalDocuments(expected, report) {
		return jobhost.PerformanceArchiveDocument{}, storageCorrupt("performance archive does not match sealed measurements")
	}
	reference := reportReference(
		reportID,
		jobcontract.KindPerformance,
		"",
		document,
	)
	if err := validateReportJournal(
		ctx,
		tx,
		snapshot,
		reference,
	); err != nil {
		return jobhost.PerformanceArchiveDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobhost.PerformanceArchiveDocument{}, fmt.Errorf("commit performance archive read: %w", err)
	}
	return jobhost.PerformanceArchiveDocument{Report: report, Reference: reference, Document: document}, nil
}
