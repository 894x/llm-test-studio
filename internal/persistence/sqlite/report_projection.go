package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
)

const (
	MaxReportProjectionDocumentBytes = 32 << 20
	MaxReportProjectionItemBytes     = 8 << 20
)

// ListReportProjections returns the bounded report-list read model consumed by
// the reporting application service. At most MaxSnapshotReports documents are
// decoded, and every document and nested item is subject to a hard byte budget.
func (repository *Repository) ListReportProjections(ctx context.Context) ([]reporting.ReportProjection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, reportProjectionReadError(ctx, "begin report projection read", err)
	}
	defer tx.Rollback()
	if err := validateReportProjectionOrderingKeys(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(
		ctx,
		reportProjectionQuery,
		reporting.MaxSnapshotReports,
		domain.CurrentReportSchemaVersion,
		MaxReportProjectionDocumentBytes,
		domain.CurrentEntitySchemaVersion,
		domain.CurrentEntitySchemaVersion,
		MaxReportProjectionDocumentBytes,
	)
	if err != nil {
		return nil, reportProjectionReadError(ctx, "query report projections", err)
	}
	projections := make([]reporting.ReportProjection, 0, reporting.MaxSnapshotReports)
	currentRuns := make(map[string]reportRunExpectation, reporting.MaxSnapshotReports)
	referenceRunIDs := make(map[[sha256.Size]byte]string)
	for rows.Next() {
		var row storedReportProjection
		if err := row.scan(rows); err != nil {
			_ = rows.Close()
			return nil, reportProjectionCorrupt(ctx, "scan report projection", err)
		}
		projection, expectedRun, err := decodeStoredReportProjection(ctx, tx, row)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if _, exists := currentRuns[expectedRun.id]; exists {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: duplicate report run", ErrCorrupt)
		}
		projections = append(projections, projection)
		currentRuns[expectedRun.id] = expectedRun
		if _, exists := referenceRunIDs[expectedRun.snapshotDigest]; !exists {
			referenceRunIDs[expectedRun.snapshotDigest] = expectedRun.id
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, reportProjectionReadError(ctx, "iterate report projections", err)
	}
	if err := rows.Close(); err != nil {
		return nil, reportProjectionReadError(ctx, "close report projections", err)
	}

	var reportCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM reports ORDER BY `+reportGeneratedAtSortKeySQL+` DESC, id DESC LIMIT ?
		)
	`, reporting.MaxSnapshotReports).Scan(&reportCount); err != nil {
		return nil, reportProjectionReadError(ctx, "count report projection roots", err)
	}
	if reportCount != len(projections) {
		return nil, fmt.Errorf("%w: report projection owner", ErrCorrupt)
	}
	for snapshotDigest, runID := range referenceRunIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		run, err := queryCurrentRun(ctx, tx, runID)
		if err != nil {
			return nil, reportProjectionCorrupt(ctx, "reload report run", err)
		}
		runDocument, err := marshalCanonical(run)
		if err != nil || sha256.Sum256(runDocument) != currentRuns[runID].documentDigest {
			return nil, fmt.Errorf("%w: reloaded report run", ErrCorrupt)
		}
		snapshotDocument, err := marshalCanonical(run.Snapshot())
		if err != nil || sha256.Sum256(snapshotDocument) != snapshotDigest {
			return nil, fmt.Errorf("%w: reloaded report snapshot", ErrCorrupt)
		}
		if err := validateStoredRunReferences(ctx, tx, run); err != nil {
			return nil, reportProjectionCorrupt(ctx, "report pinned references", err)
		}
	}
	if err := validateReportRunHistories(ctx, tx, currentRuns); err != nil {
		return nil, err
	}
	sort.Slice(projections, func(left, right int) bool {
		return currentRuns[projections[left].RunID].ordinal < currentRuns[projections[right].RunID].ordinal
	})
	if err := tx.Commit(); err != nil {
		return nil, reportProjectionReadError(ctx, "commit report projection read", err)
	}
	return projections, nil
}

func validateReportProjectionOrderingKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT report.id, report.generated_at,
		       CASE WHEN json_valid(report.document_json) THEN CASE WHEN
		         json_type(report.document_json, '$') = 'object' AND
		         (SELECT
		            SUM(CASE WHEN key = 'id' THEN 1 ELSE 0 END) = 1 AND
		            SUM(CASE WHEN key = 'id' AND type = 'text' AND value = report.id THEN 1 ELSE 0 END) = 1 AND
		            SUM(CASE WHEN key = 'generated_at' THEN 1 ELSE 0 END) = 1 AND
		            SUM(CASE WHEN key = 'generated_at' AND type = 'text' AND value = report.generated_at THEN 1 ELSE 0 END) = 1
		          FROM json_each(report.document_json))
		       THEN 1 ELSE 0 END ELSE 0 END AS valid_ordering_identity
		FROM reports AS report
	`)
	if err != nil {
		return reportProjectionReadError(ctx, "query report ordering keys", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var id, generatedAt string
		var validOrderingIdentity int64
		if err := rows.Scan(&id, &generatedAt, &validOrderingIdentity); err != nil {
			return reportProjectionCorrupt(ctx, "scan report ordering key", err)
		}
		if validOrderingIdentity != 1 {
			return fmt.Errorf("%w: report ordering identity", ErrCorrupt)
		}
		if !domain.IsUUID(id) {
			return fmt.Errorf("%w: report ordering id", ErrCorrupt)
		}
		if _, err := parseProjectionTimestamp(generatedAt); err != nil {
			return fmt.Errorf("%w: report ordering timestamp", ErrCorrupt)
		}
	}
	if err := rows.Err(); err != nil {
		return reportProjectionReadError(ctx, "iterate report ordering keys", err)
	}
	if err := rows.Close(); err != nil {
		return reportProjectionReadError(ctx, "close report ordering keys", err)
	}
	return nil
}

type reportRunExpectation struct {
	id             string
	createdAt      string
	ordinal        uint64
	revision       uint64
	documentDigest [sha256.Size]byte
	snapshotDigest [sha256.Size]byte
}

func decodeStoredReportProjection(ctx context.Context, tx *sql.Tx, row storedReportProjection) (reporting.ReportProjection, reportRunExpectation, error) {
	if row.documentBytes < 1 || row.documentBytes > MaxReportProjectionDocumentBytes || len(row.document) != int(row.documentBytes) {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report document byte budget", ErrCorrupt)
	}
	report, err := decodeReportDocument(row.document)
	if err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, reportProjectionCorrupt(ctx, "report canonical document", err)
	}
	if err := checkReportProjectionItemBudgets(report); err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	run, err := row.run.decode(true)
	if err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, reportProjectionCorrupt(ctx, "report run", err)
	}
	if run.Meta().ID != row.runID {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report run owner", ErrCorrupt)
	}
	planName, err := reportProjectionPlanName(run)
	if err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, err
	}
	row.planName = planName
	projection, err := row.decode()
	if err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, err
	}
	verification := domain.SummarizeVerification(report.CaseResults)
	projection.PassedCaseCount = int64(verification.Passed)
	projection.VerifiedCaseCount = int64(verification.Passed + verification.Failed)
	projection.ObservedCaseCount = int64(verification.Observed)
	projection.IndeterminateCaseCount = int64(verification.Indeterminate)
	if report.ID != row.id || report.RunID != row.runID || formatTime(report.GeneratedAt) != row.generatedAt ||
		report.RunStatus != projection.RunStatus || report.Conclusion.Passed != projection.Passed ||
		report.Conclusion.Verdict != projection.Verdict {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report canonical summary", ErrCorrupt)
	}
	if run.Status() != projection.RunStatus || formatTime(run.Meta().UpdatedAt) != row.runUpdatedAt {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report run summary", ErrCorrupt)
	}
	validated := validatedReportProjectionFromReport(report)
	if validated.modelName != projection.ModelName || validated.channelName != projection.ChannelName ||
		validated.passed != projection.Passed || validated.verdict != projection.Verdict ||
		validated.issueCount != projection.IssueCount || validated.caseCount != projection.CaseCount ||
		validated.failedCaseCount != projection.FailedCaseCount || validated.attachmentCount != projection.AttachmentCount {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report projection validated summary", ErrCorrupt)
	}
	snapshotDocument, err := marshalCanonical(run.Snapshot())
	if err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report pinned snapshot", ErrCorrupt)
	}
	return projection, reportRunExpectation{
		id:             run.Meta().ID,
		createdAt:      formatTime(run.Meta().CreatedAt),
		ordinal:        uint64(row.ordinal),
		revision:       run.Meta().Revision,
		documentDigest: sha256.Sum256(row.run.document),
		snapshotDigest: sha256.Sum256(snapshotDocument),
	}, nil
}

func reportProjectionPlanName(run domain.Run) (string, error) {
	snapshot := run.Snapshot()
	if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion || snapshot.PlanDocument == nil {
		return "", fmt.Errorf("%w: report pinned plan document", ErrCorrupt)
	}
	return snapshot.PlanDocument.Name, nil
}

func validateReportRunHistories(ctx context.Context, tx *sql.Tx, currentRuns map[string]reportRunExpectation) error {
	if len(currentRuns) == 0 {
		return nil
	}
	arguments := make([]any, 0, len(currentRuns))
	for runID := range currentRuns {
		arguments = append(arguments, runID)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT run_id, schema_version, revision, created_at, updated_at,
		       plan_id, plan_revision, status, snapshot_json, document_json
		FROM execution_run_revisions
		WHERE run_id IN (`+reportProjectionPlaceholders(len(arguments))+`)
		ORDER BY run_id, revision
	`, arguments...)
	if err != nil {
		return reportProjectionReadError(ctx, "query report run history", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, len(currentRuns))
	var activeRunID string
	var previous domain.Run
	var revisionCount uint64
	var lastDocumentDigest [sha256.Size]byte
	finishRun := func() error {
		if activeRunID == "" {
			return nil
		}
		expected, exists := currentRuns[activeRunID]
		if !exists || revisionCount != expected.revision || lastDocumentDigest != expected.documentDigest {
			return fmt.Errorf("%w: report run current revision", ErrCorrupt)
		}
		seen[activeRunID] = struct{}{}
		return nil
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var schemaVersion, revision, planRevision int64
		var runID, createdAt, updatedAt, planID, status string
		var snapshotDocument, document []byte
		if err := rows.Scan(
			&runID, &schemaVersion, &revision, &createdAt, &updatedAt,
			&planID, &planRevision, &status, &snapshotDocument, &document,
		); err != nil {
			return reportProjectionReadError(ctx, "scan report run history", err)
		}
		if runID != activeRunID {
			if err := finishRun(); err != nil {
				return err
			}
			if _, duplicate := seen[runID]; duplicate {
				return fmt.Errorf("%w: report run revision order", ErrCorrupt)
			}
			activeRunID = runID
			previous = domain.Run{}
			revisionCount = 0
			lastDocumentDigest = [sha256.Size]byte{}
		}
		expected, exists := currentRuns[runID]
		if !exists || revision < 1 || uint64(revision) != revisionCount+1 {
			return fmt.Errorf("%w: report run revision order", ErrCorrupt)
		}
		run, err := decodeRunDocument(document)
		if err != nil {
			return err
		}
		var snapshot domain.RunSnapshot
		if err := decodeCanonical(snapshotDocument, &snapshot, func() error { return snapshot.Validate() }); err != nil {
			return fmt.Errorf("%w: report run snapshot document", ErrCorrupt)
		}
		meta := run.Meta()
		if int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
			formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt ||
			run.PlanID() != planID || int64(run.Snapshot().Plan.Revision) != planRevision ||
			string(run.Status()) != status || !equalCanonicalDocuments(run.Snapshot(), snapshot) {
			return fmt.Errorf("%w: report run history columns", ErrCorrupt)
		}
		if revisionCount == 0 {
			if run.Status() != domain.RunQueued || formatTime(meta.CreatedAt) != expected.createdAt {
				return fmt.Errorf("%w: report run initial revision", ErrCorrupt)
			}
		} else {
			want, transitionErr := previous.Transition(run.Status(), meta.UpdatedAt)
			if transitionErr != nil || !equalCanonicalDocuments(want, run) {
				return fmt.Errorf("%w: report run revision transition", ErrCorrupt)
			}
		}
		previous = run
		revisionCount++
		lastDocumentDigest = sha256.Sum256(document)
	}
	if err := rows.Err(); err != nil {
		return reportProjectionReadError(ctx, "iterate report run history", err)
	}
	if err := rows.Close(); err != nil {
		return reportProjectionReadError(ctx, "close report run history", err)
	}
	if err := finishRun(); err != nil {
		return err
	}
	if len(seen) != len(currentRuns) {
		return fmt.Errorf("%w: report run history membership", ErrCorrupt)
	}
	return nil
}

func reportProjectionPlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

type validatedReportProjection struct {
	modelName, channelName           string
	verdict                          string
	passed                           bool
	issueCount, caseCount            int64
	failedCaseCount, attachmentCount int64
}

func validatedReportProjectionFromReport(report domain.Report) validatedReportProjection {
	failedCaseCount := int64(0)
	for _, result := range report.CaseResults {
		if result.Verification.Status == "failed" || result.Verification.Status == "indeterminate" {
			failedCaseCount++
		}
	}
	return validatedReportProjection{
		modelName: report.Model.Name, channelName: report.Channel.Name,
		passed: report.Conclusion.Passed, verdict: report.Conclusion.Verdict,
		issueCount: int64(len(report.Conclusion.Issues)), caseCount: int64(len(report.CaseResults)),
		failedCaseCount: failedCaseCount, attachmentCount: int64(len(report.Attachments)),
	}
}

func checkReportProjectionItemBudgets(report domain.Report) error {
	for index, result := range report.CaseResults {
		if err := checkReportProjectionEncodedItem("result", index, result); err != nil {
			return err
		}
	}
	for index, evidence := range report.Evidence {
		if err := checkReportProjectionEncodedItem("evidence", index, evidence); err != nil {
			return err
		}
	}
	for index, attachment := range report.Attachments {
		if err := checkReportProjectionEncodedItem("artifact", index, attachment); err != nil {
			return err
		}
	}
	for suiteIndex, suite := range report.EntryReports {
		for resultIndex, result := range suite.CaseResults {
			if err := checkReportProjectionEncodedItem(
				fmt.Sprintf("suite %d result", suiteIndex),
				resultIndex,
				result,
			); err != nil {
				return err
			}
		}
		for _, section := range []struct {
			kind  string
			items []json.RawMessage
		}{
			{kind: fmt.Sprintf("suite %d timeline", suiteIndex), items: suite.Timeline},
			{kind: fmt.Sprintf("suite %d distribution", suiteIndex), items: suite.Distributions},
		} {
			for itemIndex, item := range section.items {
				if len(item) > MaxReportProjectionItemBytes {
					return fmt.Errorf("report %s item %d exceeds byte budget", section.kind, itemIndex)
				}
				if err := checkReportProjectionEncodedItem(section.kind, itemIndex, item); err != nil {
					return err
				}
			}
		}
	}
	for _, section := range []struct {
		kind  string
		items []json.RawMessage
	}{
		{kind: "timeline", items: report.Timeline},
		{kind: "distribution", items: report.Distributions},
		{kind: "error cluster", items: report.ErrorClusters},
	} {
		for index, item := range section.items {
			if len(item) > MaxReportProjectionItemBytes {
				return fmt.Errorf("report %s item %d exceeds byte budget", section.kind, index)
			}
			if err := checkReportProjectionEncodedItem(section.kind, index, item); err != nil {
				return err
			}
		}
	}
	if len(report.Baseline) > MaxReportProjectionItemBytes {
		return fmt.Errorf("report baseline exceeds byte budget")
	}
	if err := checkReportProjectionEncodedItem("baseline", 0, report.Baseline); err != nil {
		return err
	}
	return nil
}

func checkReportProjectionEncodedItem(kind string, index int, item any) error {
	document, err := marshalCanonical(item)
	if err != nil {
		return fmt.Errorf("encode report %s item %d: %w", kind, index, err)
	}
	if len(document) > MaxReportProjectionItemBytes {
		return fmt.Errorf("report %s item %d exceeds byte budget", kind, index)
	}
	return nil
}

func reportProjectionCorrupt(ctx context.Context, operation string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return fmt.Errorf("%w: %s: %v", ErrCorrupt, operation, err)
}

type storedReportProjection struct {
	ordinal                              int64
	id, runID, generatedAt, runUpdatedAt string
	runStatus                            string
	planName, modelName, channelName     string
	verdict                              string
	passed                               int64
	issueCount, caseCount                int64
	failedCaseCount, attachmentCount     int64
	corrupt                              int64
	documentBytes                        int64
	document                             []byte
	run                                  storedRunRow
}

func (row *storedReportProjection) scan(scanner rowScanner) error {
	return scanner.Scan(
		&row.ordinal, &row.id, &row.runID, &row.generatedAt, &row.runUpdatedAt,
		&row.runStatus, &row.modelName, &row.channelName,
		&row.passed, &row.verdict, &row.issueCount, &row.caseCount,
		&row.failedCaseCount, &row.attachmentCount, &row.corrupt,
		&row.documentBytes, &row.document,
		&row.run.id, &row.run.rootCurrent, &row.run.rootCreated, &row.run.sealed, &row.run.reportCount,
		&row.run.historyCount, &row.run.historyMin, &row.run.historyMax,
		&row.run.schemaVersion, &row.run.revision, &row.run.revisionCreated, &row.run.revisionUpdated,
		&row.run.planID, &row.run.planRevision, &row.run.status, &row.run.snapshotDocument, &row.run.document,
	)
}

func (row storedReportProjection) decode() (reporting.ReportProjection, error) {
	if row.ordinal < 1 || row.ordinal > reporting.MaxSnapshotReports || row.corrupt != 0 ||
		row.passed < 0 || row.passed > 1 || row.issueCount < 0 || row.caseCount < 0 ||
		row.failedCaseCount < 0 || row.attachmentCount < 0 || row.failedCaseCount > row.caseCount {
		return reporting.ReportProjection{}, fmt.Errorf("%w: report projection source", ErrCorrupt)
	}
	if !domain.IsUUID(row.id) || !domain.IsUUID(row.runID) || strings.TrimSpace(row.planName) == "" ||
		strings.TrimSpace(row.modelName) == "" || strings.TrimSpace(row.channelName) == "" || strings.TrimSpace(row.verdict) == "" {
		return reporting.ReportProjection{}, fmt.Errorf("%w: report projection identity", ErrCorrupt)
	}
	switch domain.RunStatus(row.runStatus) {
	case domain.RunCompleted:
	case domain.RunFailed, domain.RunCancelled:
		if row.passed != 0 {
			return reporting.ReportProjection{}, fmt.Errorf("%w: report projection conclusion", ErrCorrupt)
		}
	default:
		return reporting.ReportProjection{}, fmt.Errorf("%w: report projection run status", ErrCorrupt)
	}
	if row.passed == 1 && row.failedCaseCount != 0 {
		return reporting.ReportProjection{}, fmt.Errorf("%w: passing report projection failures", ErrCorrupt)
	}
	generatedAt, err := parseProjectionTimestamp(row.generatedAt)
	if err != nil {
		return reporting.ReportProjection{}, fmt.Errorf("%w: report projection generated timestamp", ErrCorrupt)
	}
	runUpdatedAt, err := parseProjectionTimestamp(row.runUpdatedAt)
	if err != nil || generatedAt.Before(runUpdatedAt) {
		return reporting.ReportProjection{}, fmt.Errorf("%w: report projection run timestamp", ErrCorrupt)
	}
	projection := reporting.ReportProjection{
		ID: row.id, RunID: row.runID, GeneratedAt: generatedAt,
		RunStatus: domain.RunStatus(row.runStatus), PlanName: row.planName,
		ModelName: row.modelName, ChannelName: row.channelName,
		Passed: row.passed == 1, Verdict: row.verdict,
		IssueCount: row.issueCount, CaseCount: row.caseCount,
		FailedCaseCount: row.failedCaseCount, AttachmentCount: row.attachmentCount,
	}
	return projection, nil
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

// The query is the summary/shape prefilter: it validates every UI scalar against
// its durable owner and rejects non-minified JSON early. The second phase above
// performs the authoritative Go canonical/domain checks row by row; SQLite's
// json() output is deliberately not treated as project-canonical JSON.
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

const reportProjectionQuery = `
WITH latest_reports AS MATERIALIZED (
	SELECT id, ROW_NUMBER() OVER (ORDER BY ` + reportGeneratedAtSortKeySQL + ` DESC, id DESC) AS ordinal
	FROM reports
	ORDER BY ` + reportGeneratedAtSortKeySQL + ` DESC, id DESC
	LIMIT ?
),
report_rows AS (
	SELECT latest.ordinal,
	       report.id,
	       report.run_id,
	       report.generated_at,
	       revision.updated_at AS run_updated_at,
	       CASE WHEN json_type(report.document_json, '$.run_status') = 'text'
	         THEN json_extract(report.document_json, '$.run_status') ELSE '' END AS run_status,
	       CASE WHEN json_type(report.document_json, '$.plan_snapshot.model.name') = 'text'
	         THEN json_extract(report.document_json, '$.plan_snapshot.model.name') ELSE '' END AS model_name,
	       CASE WHEN json_type(report.document_json, '$.plan_snapshot.channel.name') = 'text'
	         THEN json_extract(report.document_json, '$.plan_snapshot.channel.name') ELSE '' END AS channel_name,
	       CASE WHEN json_type(report.document_json, '$.conclusion.passed') IN ('true', 'false')
	         THEN json_extract(report.document_json, '$.conclusion.passed') ELSE -1 END AS passed,
	       CASE WHEN json_type(report.document_json, '$.conclusion.verdict') = 'text'
	         THEN json_extract(report.document_json, '$.conclusion.verdict') ELSE '' END AS verdict,
	       CASE WHEN json_type(report.document_json, '$.conclusion.issues') = 'array'
	         THEN json_array_length(report.document_json, '$.conclusion.issues') ELSE -1 END AS issue_count,
	       CASE WHEN json_type(report.document_json, '$.case_results') = 'array'
	         THEN json_array_length(report.document_json, '$.case_results') ELSE -1 END AS case_count,
	       CASE WHEN json_type(report.document_json, '$.case_results') = 'array' THEN (
	         SELECT COUNT(*)
	         FROM json_each(report.document_json, '$.case_results') AS result
	         WHERE json_extract(result.value, '$.verification.status') = 'failed'
	       ) ELSE -1 END AS failed_case_count,
	       CASE WHEN json_type(report.document_json, '$.attachments') = 'array'
	         THEN json_array_length(report.document_json, '$.attachments') ELSE -1 END AS attachment_count,
	       CASE WHEN
	         report.schema_version = ? AND
	         length(CAST(report.document_json AS BLOB)) BETWEEN 1 AND ? AND
	         json_type(report.document_json, '$') = 'object' AND CAST(report.document_json AS TEXT) = json(report.document_json) AND
	         (SELECT COUNT(*) FROM json_each(report.document_json)) = 22 AND
	         NOT EXISTS (
	           SELECT 1 FROM json_each(report.document_json) AS member
	           WHERE member.key NOT IN (
	             'schema_version', 'protocol', 'verification', 'id', 'run_id', 'run_status', 'generated_at', 'plan_snapshot',
	             'model', 'channel', 'environment', 'conclusion', 'sla', 'metrics', 'timeline',
	             'distributions', 'case_results', 'entry_reports', 'error_clusters', 'evidence', 'baseline', 'attachments'
	           )
	         ) AND
	         json_type(report.document_json, '$.entry_reports') = 'array' AND
	         json_type(report.document_json, '$.schema_version') = 'integer' AND
	           json_extract(report.document_json, '$.schema_version') = report.schema_version AND
	         json_type(report.document_json, '$.id') = 'text' AND
	           json_extract(report.document_json, '$.id') = report.id AND
	         json_type(report.document_json, '$.run_id') = 'text' AND
	           json_extract(report.document_json, '$.run_id') = report.run_id AND
	         json_type(report.document_json, '$.generated_at') = 'text' AND
	           json_extract(report.document_json, '$.generated_at') = report.generated_at AND
	         root.id = report.run_id AND root.sealed = 1 AND
	         (SELECT COUNT(*) FROM reports AS owner WHERE owner.run_id = root.id) = 1 AND
	         root.current_revision = revision.revision AND revision.run_id = root.id AND
	         revision.status IN ('completed', 'failed', 'cancelled') AND
	         json_type(report.document_json, '$.run_status') = 'text' AND
	           json_extract(report.document_json, '$.run_status') = revision.status AND
	         json_type(report.document_json, '$.plan_snapshot') = 'object' AND
	           json_extract(report.document_json, '$.plan_snapshot') = json(revision.snapshot_json) AND
	         json_type(report.document_json, '$.plan_snapshot.plan.id') = 'text' AND
	           json_extract(report.document_json, '$.plan_snapshot.plan.id') = revision.plan_id AND
	         json_type(report.document_json, '$.plan_snapshot.plan.revision') = 'integer' AND
	           json_extract(report.document_json, '$.plan_snapshot.plan.revision') = revision.plan_revision AND
	         json_type(report.document_json, '$.model') = 'object' AND
	           (SELECT COUNT(*) FROM json_each(report.document_json, '$.model')) = 2 AND
	           json_extract(report.document_json, '$.model.id') = json_extract(report.document_json, '$.plan_snapshot.model.id') AND
	           json_extract(report.document_json, '$.model.name') = json_extract(report.document_json, '$.plan_snapshot.model.name') AND
	         json_type(report.document_json, '$.channel') = 'object' AND
	           (SELECT COUNT(*) FROM json_each(report.document_json, '$.channel')) = 2 AND
	           json_extract(report.document_json, '$.channel.id') = json_extract(report.document_json, '$.plan_snapshot.channel.id') AND
	           json_extract(report.document_json, '$.channel.name') = json_extract(report.document_json, '$.plan_snapshot.channel.name') AND
	         json_type(report.document_json, '$.environment') = 'object' AND
	           json_extract(report.document_json, '$.environment') = json_extract(report.document_json, '$.plan_snapshot.environment') AND
	         json_type(report.document_json, '$.conclusion') = 'object' AND
	           (SELECT COUNT(*) FROM json_each(report.document_json, '$.conclusion')) = 3 AND
	         json_type(report.document_json, '$.conclusion.passed') IN ('true', 'false') AND
	           (revision.status = 'completed' OR json_extract(report.document_json, '$.conclusion.passed') = 0) AND
	         json_type(report.document_json, '$.conclusion.verdict') = 'text' AND
	           trim(json_extract(report.document_json, '$.conclusion.verdict')) != '' AND
	         json_type(report.document_json, '$.conclusion.issues') = 'array' AND
	           NOT EXISTS (
	             SELECT 1 FROM json_each(report.document_json, '$.conclusion.issues') AS issue
	             WHERE issue.type != 'text' OR trim(issue.value) = ''
	           ) AND
	         json_type(report.document_json, '$.case_results') = 'array' AND
	           json_array_length(report.document_json, '$.case_results') =
	             (SELECT COUNT(*) FROM case_results AS stored
	              WHERE stored.run_id = report.run_id AND stored.request_id IS NULL AND stored.case_id IS NOT NULL) AND
	           json_array_length(report.document_json, '$.case_results') =
	             (SELECT COUNT(DISTINCT json_extract(result.value, '$.id')) FROM json_each(report.document_json, '$.case_results') AS result) AND
	           NOT EXISTS (
	             SELECT 1
	             FROM json_each(report.document_json, '$.case_results') AS result
	             LEFT JOIN case_results AS stored
	               ON stored.run_id = report.run_id AND stored.id = json_extract(result.value, '$.id') AND
	                  stored.request_id IS NULL AND stored.case_id IS NOT NULL
	             WHERE result.type != 'object' OR stored.id IS NULL OR
	                   stored.schema_version != ? OR stored.revision != 1 OR
	                   CAST(stored.document_json AS TEXT) != json(stored.document_json) OR
	                   json(stored.document_json) != json(result.value) OR
	                   COALESCE(json_type(stored.document_json, '$.id') = 'text', 0) = 0 OR
	                   stored.id != json_extract(stored.document_json, '$.id') OR
	                   COALESCE(json_type(stored.document_json, '$.schema_version') = 'integer', 0) = 0 OR
	                   stored.schema_version != json_extract(stored.document_json, '$.schema_version') OR
	                   COALESCE(json_type(stored.document_json, '$.revision') = 'integer', 0) = 0 OR
	                   stored.revision != json_extract(stored.document_json, '$.revision') OR
	                   COALESCE(json_type(stored.document_json, '$.created_at') = 'text', 0) = 0 OR
	                   stored.created_at != json_extract(stored.document_json, '$.created_at') OR
	                   COALESCE(json_type(stored.document_json, '$.updated_at') = 'text', 0) = 0 OR
	                   stored.updated_at != json_extract(stored.document_json, '$.updated_at') OR
	                   COALESCE(json_type(stored.document_json, '$.run_id') = 'text', 0) = 0 OR
	                   stored.run_id != json_extract(stored.document_json, '$.run_id') OR
	                   COALESCE((stored.case_id IS NULL AND json_type(stored.document_json, '$.case_id') IS NULL) OR
	                        (stored.case_id IS NOT NULL AND json_type(stored.document_json, '$.case_id') = 'text' AND stored.case_id = json_extract(stored.document_json, '$.case_id')), 0) = 0 OR
	                   COALESCE((stored.entry_id IS NULL AND json_type(stored.document_json, '$.entry_id') IS NULL) OR
	                        (stored.entry_id IS NOT NULL AND json_type(stored.document_json, '$.entry_id') = 'text' AND stored.entry_id = json_extract(stored.document_json, '$.entry_id')), 0) = 0 OR
	                   COALESCE((stored.request_id IS NULL AND json_type(stored.document_json, '$.request_id') IS NULL) OR
	                        (stored.request_id IS NOT NULL AND json_type(stored.document_json, '$.request_id') = 'text' AND stored.request_id = json_extract(stored.document_json, '$.request_id')), 0) = 0 OR
                   COALESCE(json_type(stored.document_json, '$.execution_status') = 'text', 0) = 0 OR
                   json_extract(stored.document_json, '$.execution_status') NOT IN ('completed','failed','cancelled') OR
                   COALESCE(json_type(stored.document_json, '$.verification') = 'object', 0) = 0 OR
                   COALESCE(json_type(stored.document_json, '$.verification.status') = 'text', 0) = 0 OR
                   json_extract(stored.document_json, '$.verification.status') NOT IN ('passed','failed','not_applicable','indeterminate') OR
                   COALESCE(json_type(stored.document_json, '$.verification.assertions') = 'array', 0) = 0
	           ) AND
	           (json_extract(report.document_json, '$.conclusion.passed') = 0 OR
	             NOT EXISTS (
	               SELECT 1 FROM json_each(report.document_json, '$.case_results') AS result
	               WHERE json_extract(result.value, '$.verification.status') = 'failed'
	             )) AND
	         json_type(report.document_json, '$.evidence') = 'array' AND
	           json_array_length(report.document_json, '$.evidence') =
	             (SELECT COUNT(*) FROM evidence AS stored WHERE stored.run_id = report.run_id) AND
	           json_array_length(report.document_json, '$.evidence') =
	             (SELECT COUNT(DISTINCT json_extract(item.value, '$.id')) FROM json_each(report.document_json, '$.evidence') AS item) AND
	           NOT EXISTS (
	             SELECT 1
	             FROM json_each(report.document_json, '$.evidence') AS item
	             LEFT JOIN evidence AS stored
	               ON stored.run_id = report.run_id AND stored.id = json_extract(item.value, '$.id')
	             WHERE item.type != 'object' OR stored.id IS NULL OR stored.schema_version != ? OR stored.revision != 1 OR
	                   CAST(stored.document_json AS TEXT) != json(stored.document_json) OR
	                   json(stored.document_json) != json(item.value) OR
	                   COALESCE(json_type(stored.document_json, '$.id') = 'text', 0) = 0 OR
	                   stored.id != json_extract(stored.document_json, '$.id') OR
	                   COALESCE(json_type(stored.document_json, '$.schema_version') = 'integer', 0) = 0 OR
	                   stored.schema_version != json_extract(stored.document_json, '$.schema_version') OR
	                   COALESCE(json_type(stored.document_json, '$.revision') = 'integer', 0) = 0 OR
	                   stored.revision != json_extract(stored.document_json, '$.revision') OR
	                   COALESCE(json_type(stored.document_json, '$.created_at') = 'text', 0) = 0 OR
	                   stored.created_at != json_extract(stored.document_json, '$.created_at') OR
	                   COALESCE(json_type(stored.document_json, '$.updated_at') = 'text', 0) = 0 OR
	                   stored.updated_at != json_extract(stored.document_json, '$.updated_at') OR
	                   COALESCE(json_type(stored.document_json, '$.run_id') = 'text', 0) = 0 OR
	                   stored.run_id != json_extract(stored.document_json, '$.run_id') OR
	                   COALESCE(json_type(stored.document_json, '$.relative_path') = 'text', 0) = 0 OR trim(json_extract(stored.document_json, '$.relative_path')) = '' OR
	                   COALESCE(json_type(stored.document_json, '$.sha256') = 'text', 0) = 0 OR length(json_extract(stored.document_json, '$.sha256')) != 64 OR
	                   COALESCE(json_type(stored.document_json, '$.media_type') = 'text', 0) = 0 OR trim(json_extract(stored.document_json, '$.media_type')) = '' OR
	                   COALESCE(json_type(stored.document_json, '$.redacted') = 'true', 0) = 0
	           ) AND
	         json_type(report.document_json, '$.attachments') = 'array' AND
	           json_array_length(report.document_json, '$.attachments') =
	             (SELECT COUNT(*) FROM report_attachments AS link WHERE link.report_id = report.id) AND
	           json_array_length(report.document_json, '$.attachments') =
	             (SELECT COUNT(DISTINCT json_extract(item.value, '$.artifact_id')) FROM json_each(report.document_json, '$.attachments') AS item) AND
	           NOT EXISTS (
	             SELECT 1
	             FROM json_each(report.document_json, '$.attachments') AS item
	             LEFT JOIN report_attachments AS link
	               ON link.report_id = report.id AND link.position = CAST(item.key AS INTEGER)
	             LEFT JOIN artifacts AS artifact
	               ON artifact.id = link.artifact_id AND artifact.run_id = report.run_id
	             WHERE item.type != 'object' OR artifact.id IS NULL OR
	                   CAST(artifact.document_json AS TEXT) != json(artifact.document_json) OR
	                   json(artifact.document_json) != json(item.value) OR
	                   COALESCE(json_type(artifact.document_json, '$.artifact_id') = 'text', 0) = 0 OR
	                   artifact.id != json_extract(artifact.document_json, '$.artifact_id') OR
	                   COALESCE(json_type(artifact.document_json, '$.run_id') = 'text', 0) = 0 OR
	                   artifact.run_id != json_extract(artifact.document_json, '$.run_id') OR
	                   COALESCE(json_type(artifact.document_json, '$.name') = 'text', 0) = 0 OR
	                   artifact.name != json_extract(artifact.document_json, '$.name') OR
	                   COALESCE(json_type(artifact.document_json, '$.relative_path') = 'text', 0) = 0 OR
	                   artifact.relative_path != json_extract(artifact.document_json, '$.relative_path') OR
	                   COALESCE(json_type(artifact.document_json, '$.sha256') = 'text', 0) = 0 OR
	                   artifact.sha256 != json_extract(artifact.document_json, '$.sha256') OR
	                   COALESCE(json_type(artifact.document_json, '$.media_type') = 'text', 0) = 0 OR
	                   artifact.media_type != json_extract(artifact.document_json, '$.media_type') OR
	                   artifact.redacted != 1 OR COALESCE(json_type(artifact.document_json, '$.redacted') = 'true', 0) = 0
	           ) AND
	         json_type(report.document_json, '$.sla') = 'object' AND
	         json_type(report.document_json, '$.metrics') = 'object' AND
	         json_type(report.document_json, '$.timeline') = 'array' AND
	           NOT EXISTS (SELECT 1 FROM json_each(report.document_json, '$.timeline') WHERE type != 'object') AND
	         json_type(report.document_json, '$.distributions') = 'array' AND
	           NOT EXISTS (SELECT 1 FROM json_each(report.document_json, '$.distributions') WHERE type != 'object') AND
	         json_type(report.document_json, '$.error_clusters') = 'array' AND
	           NOT EXISTS (SELECT 1 FROM json_each(report.document_json, '$.error_clusters') WHERE type != 'object') AND
	         json_type(report.document_json, '$.baseline') = 'object'
	       THEN 0 ELSE 1 END AS corrupt,
	       length(CAST(report.document_json AS BLOB)) AS report_document_bytes,
	       CASE WHEN length(CAST(report.document_json AS BLOB)) BETWEEN 1 AND ?
	         THEN report.document_json ELSE NULL END AS report_document,
	       root.id AS owner_run_id,
	       root.current_revision AS owner_current_revision,
	       root.created_at AS owner_created_at,
	       root.sealed AS owner_sealed,
	       (SELECT COUNT(*) FROM reports AS owner_report WHERE owner_report.run_id = root.id) AS owner_report_count,
	       (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id) AS owner_history_count,
	       (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id) AS owner_history_min,
	       (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id) AS owner_history_max,
	       revision.schema_version AS owner_schema_version,
	       revision.revision AS owner_revision,
	       revision.created_at AS owner_revision_created_at,
	       revision.updated_at AS owner_revision_updated_at,
	       revision.plan_id AS owner_plan_id,
	       revision.plan_revision AS owner_plan_revision,
	       revision.status AS owner_status,
	       revision.snapshot_json AS owner_snapshot_document,
	       revision.document_json AS owner_run_document
	FROM latest_reports AS latest
	JOIN reports AS report ON report.id = latest.id
	LEFT JOIN execution_runs AS root ON root.id = report.run_id
	LEFT JOIN execution_run_revisions AS revision
	  ON revision.run_id = root.id AND revision.revision = root.current_revision
)
SELECT ordinal, id, run_id, generated_at, run_updated_at, run_status,
	   model_name, channel_name, passed, verdict,
	   issue_count, case_count, failed_case_count, attachment_count, corrupt,
	   report_document_bytes, report_document,
	   owner_run_id, owner_current_revision, owner_created_at, owner_sealed, owner_report_count,
	   owner_history_count, owner_history_min, owner_history_max,
	   owner_schema_version, owner_revision, owner_revision_created_at, owner_revision_updated_at,
	   owner_plan_id, owner_plan_revision, owner_status, owner_snapshot_document, owner_run_document
FROM report_rows
`
