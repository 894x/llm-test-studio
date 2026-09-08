package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/domain"
)

type relationQueryer interface {
	rowQueryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validateReportStorage(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	run, err := queryCurrentRun(ctx, queryer, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report run relation", err)
	}
	if run.Status() != report.RunStatus || !reflect.DeepEqual(run.Snapshot(), report.PlanSnapshot) {
		return storageCorrupt("report run relation")
	}
	if err := validateStoredRunReferences(ctx, queryer, run); err != nil {
		return err
	}
	var sealed int
	if err := queryer.QueryRowContext(ctx, `SELECT sealed FROM execution_runs WHERE id = ?`, report.RunID).Scan(&sealed); err != nil {
		return relationStorageError(ctx, "report run seal", err)
	}
	if sealed != 1 {
		return storageCorrupt("report run seal")
	}
	if err := validateReportAttachments(ctx, queryer, report); err != nil {
		return err
	}
	if err := validateReportResults(ctx, queryer, report); err != nil {
		return err
	}
	return validateReportEvidence(ctx, queryer, report)
}

func validateReportAttachments(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT link.position, artifact.id, artifact.run_id, artifact.name,
		       artifact.relative_path, artifact.sha256, artifact.media_type,
		       artifact.redacted, artifact.document_json
		FROM report_attachments AS link
		JOIN artifacts AS artifact ON artifact.id = link.artifact_id
		WHERE link.report_id = ? ORDER BY link.position
	`, report.ID)
	if err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	defer rows.Close()
	position := 0
	for rows.Next() {
		var storedPosition, redacted int
		var id, runID, name, relativePath, digest, mediaType string
		var document []byte
		if err := rows.Scan(&storedPosition, &id, &runID, &name, &relativePath, &digest, &mediaType, &redacted, &document); err != nil {
			return relationStorageError(ctx, "report attachment relations", err)
		}
		if position >= len(report.Attachments) {
			return storageCorrupt("report attachment relations")
		}
		var attachment domain.ReportAttachment
		if err := decodeCanonical(document, &attachment, func() error { return attachment.Validate(report.RunID) }); err != nil {
			return storageCorrupt("report attachment document")
		}
		want := report.Attachments[position]
		if storedPosition != position || !reflect.DeepEqual(attachment, want) || id != attachment.ArtifactID ||
			runID != attachment.RunID || name != attachment.Name || relativePath != attachment.RelativePath ||
			digest != attachment.SHA256 || mediaType != attachment.MediaType || redacted != boolInt(attachment.Redacted) {
			return storageCorrupt("report attachment relations")
		}
		position++
	}
	if err := rows.Err(); err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	if position != len(report.Attachments) {
		return storageCorrupt("report attachment relations")
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report attachment relations", err)
	}
	var artifactCount int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts WHERE run_id = ?`, report.RunID).Scan(&artifactCount); err != nil {
		return relationStorageError(ctx, "report attachment artifacts", err)
	}
	if artifactCount != len(report.Attachments) {
		return storageCorrupt("report attachment artifacts")
	}
	return nil
}

func validateReportResults(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, suite_entry_id, case_id, request_id, document_json
		FROM case_results
		WHERE run_id = ? AND request_id IS NULL AND case_id IS NOT NULL
		ORDER BY created_at, id
	`, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report result collection", err)
	}
	storedRows := make([]storedResultRow, 0, len(report.CaseResults))
	for rows.Next() {
		var row storedResultRow
		if err := row.scan(rows); err != nil {
			rows.Close()
			return relationStorageError(ctx, "report result collection", err)
		}
		storedRows = append(storedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "report result collection", err)
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report result collection", err)
	}
	if len(storedRows) != len(report.CaseResults) {
		return storageCorrupt("report result collection")
	}
	stored := make(map[string]domain.Result, len(storedRows))
	for _, row := range storedRows {
		result, err := row.decode(report.RunID)
		if err != nil {
			return storageCorrupt("report result collection")
		}
		if report.PlanSnapshot.QuickTask != nil {
			if result.SuiteEntryID != "" {
				return storageCorrupt("report result collection")
			}
			result.SuiteEntryID = report.RunID
		}
		stored[result.ID] = result
	}
	for _, want := range report.CaseResults {
		if got, exists := stored[want.ID]; !exists || !canonicalValuesEqual(got, want) {
			return storageCorrupt("report result collection")
		}
	}
	return nil
}

func validateReportEvidence(ctx context.Context, queryer relationQueryer, report domain.Report) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
		FROM evidence WHERE run_id = ? ORDER BY created_at, id
	`, report.RunID)
	if err != nil {
		return relationStorageError(ctx, "report evidence collection", err)
	}
	storedRows := make([]storedEvidenceRow, 0, len(report.Evidence))
	for rows.Next() {
		var row storedEvidenceRow
		if err := row.scan(rows); err != nil {
			rows.Close()
			return relationStorageError(ctx, "report evidence collection", err)
		}
		storedRows = append(storedRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return relationStorageError(ctx, "report evidence collection", err)
	}
	if err := rows.Close(); err != nil {
		return relationStorageError(ctx, "report evidence collection", err)
	}
	if len(storedRows) != len(report.Evidence) {
		return storageCorrupt("report evidence collection")
	}
	stored := make(map[string]domain.Evidence, len(storedRows))
	for _, row := range storedRows {
		evidence, err := row.decode(report.RunID)
		if err != nil {
			return storageCorrupt("report evidence collection")
		}
		stored[evidence.ID] = evidence
	}
	for _, want := range report.Evidence {
		if got, exists := stored[want.ID]; !exists || !reflect.DeepEqual(got, want) {
			return storageCorrupt("report evidence collection")
		}
	}
	return nil
}

func storageCorrupt(kind string) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, kind)
}

func relationStorageError(ctx context.Context, kind string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return storageCorrupt(kind)
}

func canonicalValuesEqual(left, right any) bool {
	leftDocument, leftErr := marshalCanonical(left)
	rightDocument, rightErr := marshalCanonical(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftDocument, rightDocument)
}
