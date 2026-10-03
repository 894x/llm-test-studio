package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
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
		MaxReportProjectionDocumentBytes,
	)
	if err != nil {
		return nil, reportProjectionReadError(ctx, "query report projections", err)
	}
	stored := make([]storedReportProjection, 0, reporting.MaxSnapshotReports)
	for rows.Next() {
		var row storedReportProjection
		if err := row.scan(rows); err != nil {
			_ = rows.Close()
			return nil, reportProjectionCorrupt(ctx, "scan report projection", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, reportProjectionReadError(ctx, "iterate report projections", err)
	}
	if err := rows.Close(); err != nil {
		return nil, reportProjectionReadError(ctx, "close report projections", err)
	}
	projections := make([]reporting.ReportProjection, 0, len(stored))
	currentRuns := make(map[string]reportRunExpectation, len(stored))
	nextCache := make(map[string]cachedReportProjection, len(stored))
	for _, row := range stored {
		digest, err := reportProjectionDigest(ctx, tx, row)
		if err != nil {
			return nil, err
		}
		repository.reportProjectionMu.Lock()
		cached, exists := repository.reportProjections[row.id]
		repository.reportProjectionMu.Unlock()
		if exists && cached.digest == digest {
			cached.expected.ordinal = uint64(row.ordinal)
			projections = append(projections, cached.projection)
			nextCache[row.id] = cached
			continue
		}
		projection, expectedRun, err := decodeStoredReportProjection(ctx, tx, row)
		if err != nil {
			return nil, err
		}
		if _, exists := currentRuns[expectedRun.id]; exists {
			return nil, fmt.Errorf("%w: duplicate report run", ErrCorrupt)
		}
		projections = append(projections, projection)
		currentRuns[expectedRun.id] = expectedRun
		nextCache[row.id] = cachedReportProjection{digest: digest, projection: projection, expected: expectedRun}
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
	// Each owner was validated while decoding its row in this same transaction.
	// History validation below also checks the current document and frozen snapshot.
	if err := validateReportRunHistories(ctx, tx, currentRuns); err != nil {
		return nil, err
	}
	sort.Slice(projections, func(left, right int) bool {
		return nextCache[projections[left].ID].expected.ordinal < nextCache[projections[right].ID].expected.ordinal
	})
	if err := tx.Commit(); err != nil {
		return nil, reportProjectionReadError(ctx, "commit report projection read", err)
	}
	repository.reportProjectionMu.Lock()
	repository.reportProjections = nextCache
	repository.reportProjectionMu.Unlock()
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
	generatedAt, err := parseProjectionTimestamp(row.generatedAt)
	if err != nil || generatedAt.Before(run.Meta().UpdatedAt) {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report generation timestamp", ErrCorrupt)
	}
	identityMatches := report.ID == row.id && report.RunID == row.runID &&
		report.SchemaVersion == int(row.schemaVersion) && report.GeneratedAt.Equal(generatedAt)
	ownerMatches := run.Status() == report.RunStatus &&
		equalCanonicalDocuments(run.Snapshot(), report.PlanSnapshot)
	if !identityMatches || !ownerMatches {
		return reporting.ReportProjection{}, reportRunExpectation{}, fmt.Errorf("%w: report persisted owner", ErrCorrupt)
	}
	// Validate durable relations once in Go rather than reparsing the entire
	// report for every SQLite JSON scalar and nested result.
	if err := validateReportAttachments(ctx, tx, report); err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, err
	}
	if err := validateReportResults(ctx, tx, report); err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, err
	}
	if err := validateReportEvidence(ctx, tx, report); err != nil {
		return reporting.ReportProjection{}, reportRunExpectation{}, err
	}
	verification := domain.SummarizeVerification(report.CaseResults)
	var failedCaseCount int64
	for _, result := range report.CaseResults {
		if result.Verification.Status == "failed" {
			failedCaseCount++
		}
	}
	projection := reporting.ReportProjection{
		ID: report.ID, RunID: report.RunID, GeneratedAt: generatedAt,
		RunStatus: report.RunStatus, PlanName: planName,
		ModelName: report.Model.Name, ChannelName: report.Channel.Name,
		Passed: report.Conclusion.Passed, Verdict: report.Conclusion.Verdict,
		IssueCount: int64(len(report.Conclusion.Issues)), CaseCount: int64(len(report.CaseResults)),
		FailedCaseCount: failedCaseCount, AttachmentCount: int64(len(report.Attachments)),
		PassedCaseCount:        int64(verification.Passed),
		VerifiedCaseCount:      int64(verification.Passed + verification.Failed),
		ObservedCaseCount:      int64(verification.Observed),
		IndeterminateCaseCount: int64(verification.Indeterminate),
	}
	return projection, reportRunExpectation{
		id:             run.Meta().ID,
		createdAt:      formatTime(run.Meta().CreatedAt),
		ordinal:        uint64(row.ordinal),
		revision:       run.Meta().Revision,
		documentDigest: sha256.Sum256(row.run.document),
		snapshotDigest: sha256.Sum256(row.run.snapshotDocument),
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
		snapshot := run.Snapshot()
		canonicalSnapshot, err := marshalCanonical(snapshot)
		if err != nil || !bytes.Equal(canonicalSnapshot, snapshotDocument) ||
			sha256.Sum256(snapshotDocument) != expected.snapshotDigest {
			return fmt.Errorf("%w: report run snapshot document", ErrCorrupt)
		}
		meta := run.Meta()
		if int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
			formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt ||
			run.PlanID() != planID || int64(snapshot.Plan.Revision) != planRevision ||
			string(run.Status()) != status {
			return fmt.Errorf("%w: report run history columns", ErrCorrupt)
		}
		if revisionCount == 0 {
			if run.Status() != domain.RunQueued || formatTime(meta.CreatedAt) != expected.createdAt {
				return fmt.Errorf("%w: report run initial revision", ErrCorrupt)
			}
		} else {
			want, transitionErr := previous.Transition(run.Status(), meta.UpdatedAt)
			if transitionErr != nil || !reflect.DeepEqual(want, run) {
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
	ordinal                int64
	id, runID, generatedAt string
	schemaVersion          int64
	documentBytes          int64
	document               []byte
	run                    storedRunRow
}

func (row *storedReportProjection) scan(scanner rowScanner) error {
	return scanner.Scan(
		&row.ordinal, &row.id, &row.runID, &row.generatedAt, &row.schemaVersion,
		&row.documentBytes, &row.document,
		&row.run.id, &row.run.rootCurrent, &row.run.rootCreated, &row.run.sealed, &row.run.reportCount,
		&row.run.historyCount, &row.run.historyMin, &row.run.historyMax,
		&row.run.schemaVersion, &row.run.revision, &row.run.revisionCreated, &row.run.revisionUpdated,
		&row.run.planID, &row.run.planRevision, &row.run.status, &row.run.snapshotDocument, &row.run.document,
	)
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

// Select bounded documents and owner metadata. Canonical/domain/relation
// validation and scalar summaries share the single Go path above.
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
)
SELECT latest.ordinal, report.id, report.run_id, report.generated_at, report.schema_version,
	length(CAST(report.document_json AS BLOB)),
	CASE WHEN length(CAST(report.document_json AS BLOB)) BETWEEN 1 AND ?
		THEN report.document_json ELSE NULL END,
	root.id, root.current_revision, root.created_at, root.sealed,
	(SELECT COUNT(*) FROM reports WHERE run_id = root.id),
	(SELECT COUNT(*) FROM execution_run_revisions WHERE run_id = root.id),
	(SELECT COALESCE(MIN(revision), 0) FROM execution_run_revisions WHERE run_id = root.id),
	(SELECT COALESCE(MAX(revision), 0) FROM execution_run_revisions WHERE run_id = root.id),
	revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
	revision.plan_id, revision.plan_revision, revision.status, revision.snapshot_json, revision.document_json
FROM latest_reports AS latest
JOIN reports AS report ON report.id = latest.id
LEFT JOIN execution_runs AS root ON root.id = report.run_id
LEFT JOIN execution_run_revisions AS revision
	ON revision.run_id = root.id AND revision.revision = root.current_revision
`
