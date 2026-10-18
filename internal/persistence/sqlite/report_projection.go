package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxReportProjectionDocumentBytes = 32 << 20
	MaxReportProjectionItemBytes     = 8 << 20
)

// ListReportProjections reads only the latest report-list scalars. Full document,
// history and relation validation belongs to GetReport, when the user opens it.
func (repository *Repository) ListReportProjections(ctx context.Context) ([]reporting.ReportProjection, error) {
	return repository.ListRecentReportProjections(ctx, reporting.MaxSnapshotReports)
}

func (repository *Repository) ListRecentReportProjections(ctx context.Context, limit int) ([]reporting.ReportProjection, error) {
	if limit < 1 || limit > reporting.MaxSnapshotReports {
		return nil, reporting.ErrInconsistent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := repository.db.QueryContext(ctx, reportProjectionQuery, limit, domain.CurrentReportSchemaVersion)
	if err != nil {
		return nil, reportProjectionReadError(ctx, "query report summaries", err)
	}
	defer rows.Close()
	projections := make([]reporting.ReportProjection, 0)
	for rows.Next() {
		var item reporting.ReportProjection
		var generatedAt string
		var currentFormat, passed int
		err := rows.Scan(&item.ID, &item.RunID, &generatedAt, &currentFormat,
			&item.RunStatus, &item.PlanName, &item.ModelName, &item.ChannelName,
			&passed, &item.Verdict, &item.IssueCount, &item.CaseCount,
			&item.FailedCaseCount, &item.AttachmentCount, &item.PassedCaseCount,
			&item.ObservedCaseCount, &item.IndeterminateCaseCount)
		if err != nil {
			return nil, reportProjectionCorrupt(ctx, "report summary fields", err)
		}
		if currentFormat != 1 || (passed != 0 && passed != 1) {
			return nil, fmt.Errorf("%w: report summary requires the current report format; open or repair the affected report", ErrCorrupt)
		}
		item.GeneratedAt, err = parseProjectionTimestamp(generatedAt)
		if err != nil {
			return nil, reportProjectionCorrupt(ctx, "report summary timestamp", err)
		}
		item.Passed = passed == 1
		item.VerifiedCaseCount = item.PassedCaseCount + item.FailedCaseCount
		projections = append(projections, item)
	}
	if err := rows.Err(); err != nil {
		return nil, reportProjectionReadError(ctx, "read report summaries", err)
	}
	return projections, nil
}

func reportProjectionCorrupt(ctx context.Context, operation string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return fmt.Errorf("%w: %s: %v", ErrCorrupt, operation, err)
}

func parseProjectionTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	_, offset := parsed.Zone()
	if parsed.IsZero() || offset != 0 || formatTime(parsed) != value {
		return time.Time{}, fmt.Errorf("timestamp is not canonical UTC")
	}
	return parsed, nil
}

func reportProjectionReadError(ctx context.Context, operation string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if strings.Contains(strings.ToLower(err.Error()), "malformed json") {
		return fmt.Errorf("%w: %s: %v", ErrCorrupt, operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

const reportGeneratedAtSortKeySQL = `
	substr(generated_at, 1, 19) ||
	substr(
		CASE WHEN substr(generated_at, 20, 1) = '.'
			THEN substr(generated_at, 21, length(generated_at) - 21)
			ELSE ''
		END || '000000000',
		1, 9
	)
`

// Materialize the bounded ID window before accessing report JSON. No result,
// evidence, artifact, revision-history or authored-catalog reads occur here.
const reportProjectionQuery = `
WITH latest_reports AS MATERIALIZED (
 SELECT id FROM reports
 ORDER BY ` + reportGeneratedAtSortKeySQL + ` DESC, id DESC LIMIT ?
)
SELECT report.id, report.run_id, report.generated_at,
 CASE WHEN json_valid(report.document_json) THEN
   report.schema_version = ?2 AND
   json_extract(report.document_json, '$.schema_version') = ?2 AND
   json_extract(report.document_json, '$.id') = report.id AND
   json_extract(report.document_json, '$.run_id') = report.run_id AND
   json_extract(report.document_json, '$.generated_at') = report.generated_at AND
   json_type(report.document_json, '$.entry_reports') = 'array' AND
   json_type(report.document_json, '$.case_results') = 'array' AND
   json_type(report.document_json, '$.attachments') = 'array' AND
   json_type(report.document_json, '$.conclusion.issues') = 'array' AND
   json_type(report.document_json, '$.conclusion.passed') IN ('true', 'false') AND
   json_type(report.document_json, '$.verification.passed') = 'integer' AND
   json_type(report.document_json, '$.verification.failed') = 'integer' AND
   json_type(report.document_json, '$.verification.observed') = 'integer' AND
   json_type(report.document_json, '$.verification.indeterminate') = 'integer' AND
   json_extract(report.document_json, '$.plan_snapshot.schema_version') = 1
 ELSE 0 END,
 json_extract(report.document_json, '$.run_status'),
 json_extract(report.document_json, '$.plan_snapshot.plan_document.name'),
 json_extract(report.document_json, '$.model.name'),
 json_extract(report.document_json, '$.channel.name'),
 json_extract(report.document_json, '$.conclusion.passed'),
 json_extract(report.document_json, '$.conclusion.verdict'),
 json_array_length(report.document_json, '$.conclusion.issues'),
 json_array_length(report.document_json, '$.case_results'),
 json_extract(report.document_json, '$.verification.failed'),
 json_array_length(report.document_json, '$.attachments'),
 json_extract(report.document_json, '$.verification.passed'),
 json_extract(report.document_json, '$.verification.observed'),
 json_extract(report.document_json, '$.verification.indeterminate')
FROM latest_reports AS latest JOIN reports AS report ON report.id = latest.id
`
