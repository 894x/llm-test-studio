package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

// ListRunProjections returns the bounded run-list read model expected by the
// workspace application service. Result, evidence, artifact, and report JSON
// documents remain in SQLite; this query reads only aggregate and
// summary-critical scalar values from those documents.
func (repository *Repository) ListRunProjections(ctx context.Context) ([]workspace.RunProjection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin workspace projection read: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(
		ctx,
		workspaceProjectionQuery,
		domain.CurrentEntitySchemaVersion,
		domain.CurrentEntitySchemaVersion,
		domain.CurrentReportSchemaVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("query workspace projections: %w", err)
	}
	stored := make([]storedWorkspaceProjection, 0)
	for rows.Next() {
		var row storedWorkspaceProjection
		if err := row.scan(rows); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan workspace projection: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate workspace projections: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close workspace projections: %w", err)
	}

	var rootCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_runs`).Scan(&rootCount); err != nil {
		return nil, fmt.Errorf("count workspace run roots: %w", err)
	}
	if rootCount != len(stored) {
		return nil, fmt.Errorf("%w: workspace run current revision pointer", ErrCorrupt)
	}

	projections := make([]workspace.RunProjection, 0, len(stored))
	for _, row := range stored {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		projection, err := row.decode(ctx, tx)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, err
		}
		projections = append(projections, projection)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace projection read: %w", err)
	}
	return projections, nil
}

type storedWorkspaceProjection struct {
	run storedRunRow

	completed, passed, failed, artifactCount int64
	resultCorrupt, evidenceCorrupt           int64
	artifactCorrupt, reportCorrupt           int64
	conclusionPassed                         int64
}

func (row *storedWorkspaceProjection) scan(scanner rowScanner) error {
	return scanner.Scan(
		&row.run.id, &row.run.rootCurrent, &row.run.rootCreated, &row.run.sealed, &row.run.reportCount,
		&row.run.historyCount, &row.run.historyMin, &row.run.historyMax,
		&row.run.schemaVersion, &row.run.revision, &row.run.revisionCreated, &row.run.revisionUpdated,
		&row.run.planID, &row.run.planRevision, &row.run.status, &row.run.snapshotDocument, &row.run.document,
		&row.completed, &row.passed, &row.failed, &row.artifactCount,
		&row.resultCorrupt, &row.evidenceCorrupt, &row.artifactCorrupt, &row.reportCorrupt,
		&row.conclusionPassed,
	)
}

func (row storedWorkspaceProjection) decode(ctx context.Context, queryer rowQueryer) (workspace.RunProjection, error) {
	for _, value := range []int64{
		row.completed, row.passed, row.failed, row.artifactCount,
		row.resultCorrupt, row.evidenceCorrupt, row.artifactCorrupt, row.reportCorrupt,
	} {
		if value < 0 {
			return workspace.RunProjection{}, fmt.Errorf("%w: negative workspace aggregate", ErrCorrupt)
		}
	}
	if row.resultCorrupt != 0 || row.evidenceCorrupt != 0 || row.artifactCorrupt != 0 || row.reportCorrupt != 0 {
		return workspace.RunProjection{}, fmt.Errorf("%w: workspace aggregate source", ErrCorrupt)
	}
	if row.completed != row.passed+row.failed {
		return workspace.RunProjection{}, fmt.Errorf("%w: workspace result aggregate", ErrCorrupt)
	}

	run, err := row.run.decode(true)
	if err != nil {
		return workspace.RunProjection{}, err
	}
	pinnedPlan, err := workspacePinnedPlan(ctx, queryer, run)
	if err != nil {
		return workspace.RunProjection{}, err
	}
	if err := validateWorkspacePinnedPlan(run, pinnedPlan); err != nil {
		return workspace.RunProjection{}, fmt.Errorf("%w: workspace pinned plan", ErrCorrupt)
	}

	conclusion := workspace.ConclusionNone
	switch row.conclusionPassed {
	case -1:
		if row.run.sealed != 0 || row.run.reportCount != 0 {
			return workspace.RunProjection{}, fmt.Errorf("%w: workspace report conclusion", ErrCorrupt)
		}
	case 0:
		conclusion = workspace.ConclusionFailed
	case 1:
		conclusion = workspace.ConclusionPassed
	default:
		return workspace.RunProjection{}, fmt.Errorf("%w: workspace report conclusion", ErrCorrupt)
	}
	if err := conclusion.Validate(); err != nil {
		return workspace.RunProjection{}, fmt.Errorf("%w: workspace report conclusion", ErrCorrupt)
	}
	if conclusion != workspace.ConclusionNone {
		if row.run.sealed != 1 || row.run.reportCount != 1 {
			return workspace.RunProjection{}, fmt.Errorf("%w: workspace report seal", ErrCorrupt)
		}
		switch run.Status() {
		case domain.RunCompleted:
		case domain.RunFailed, domain.RunCancelled:
			if conclusion == workspace.ConclusionPassed {
				return workspace.RunProjection{}, fmt.Errorf("%w: passing non-completed report", ErrCorrupt)
			}
		default:
			return workspace.RunProjection{}, fmt.Errorf("%w: non-terminal report conclusion", ErrCorrupt)
		}
	}

	return workspace.RunProjection{
		Run: run, PinnedPlan: pinnedPlan,
		Completed: uint64(row.completed), Passed: uint64(row.passed), Failed: uint64(row.failed),
		ArtifactCount: uint64(row.artifactCount), Conclusion: conclusion,
	}, nil
}

func workspacePinnedPlan(_ context.Context, _ rowQueryer, run domain.Run) (domain.Plan, error) {
	snapshot := run.Snapshot()
	if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion || snapshot.PlanDocument == nil {
		return domain.Plan{}, fmt.Errorf("%w: workspace plan document", ErrCorrupt)
	}
	return *snapshot.PlanDocument, nil
}

func validateWorkspacePinnedPlan(run domain.Run, plan domain.Plan) error {
	if err := run.Validate(); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	snapshot := run.Snapshot()
	if plan.ID != run.PlanID() || plan.ID != snapshot.Plan.ID || plan.Revision != snapshot.Plan.Revision {
		return errors.New("run plan reference differs from pinned plan")
	}
	if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion || snapshot.PlanDocument == nil ||
		!reflect.DeepEqual(plan, *snapshot.PlanDocument) {
		return errors.New("run snapshot differs from pinned plan")
	}
	return nil
}

const workspaceProjectionQuery = `
WITH result_observations AS (
	SELECT item.*,
	       CASE WHEN item.request_id IS NOT NULL OR
	         MAX(item.request_id IS NOT NULL) OVER (PARTITION BY item.run_id) = 0
	         THEN 1 ELSE 0 END AS observation
	FROM case_results AS item
),
result_stats AS (
	SELECT item.run_id,
	       SUM(item.observation) AS completed,
	       SUM(CASE WHEN
	           item.observation = 1 AND
	           json_extract(item.document_json, '$.success.transport') = 1 AND
	           json_extract(item.document_json, '$.success.protocol') = 1 AND
	           json_extract(item.document_json, '$.success.semantic') = 1 AND
	           json_extract(item.document_json, '$.success.sla') = 1
	         THEN 1 ELSE 0 END) AS passed,
	       SUM(CASE WHEN
	           item.observation = 0 OR (
	           json_extract(item.document_json, '$.success.transport') = 1 AND
	           json_extract(item.document_json, '$.success.protocol') = 1 AND
	           json_extract(item.document_json, '$.success.semantic') = 1 AND
	           json_extract(item.document_json, '$.success.sla') = 1)
	         THEN 0 ELSE 1 END) AS failed,
	       SUM(CASE WHEN
	           item.schema_version = ? AND item.revision = 1 AND
	           json_type(item.document_json, '$.id') = 'text' AND json_extract(item.document_json, '$.id') = item.id AND
	           json_type(item.document_json, '$.schema_version') = 'integer' AND json_extract(item.document_json, '$.schema_version') = item.schema_version AND
	           json_type(item.document_json, '$.revision') = 'integer' AND json_extract(item.document_json, '$.revision') = item.revision AND
	           json_type(item.document_json, '$.created_at') = 'text' AND json_extract(item.document_json, '$.created_at') = item.created_at AND
	           json_type(item.document_json, '$.updated_at') = 'text' AND json_extract(item.document_json, '$.updated_at') = item.updated_at AND
	           json_type(item.document_json, '$.run_id') = 'text' AND json_extract(item.document_json, '$.run_id') = item.run_id AND
	           ((item.case_id IS NULL AND json_type(item.document_json, '$.case_id') IS NULL) OR
	            (item.case_id IS NOT NULL AND json_type(item.document_json, '$.case_id') = 'text' AND json_extract(item.document_json, '$.case_id') = item.case_id)) AND
	           ((item.request_id IS NULL AND json_type(item.document_json, '$.request_id') IS NULL) OR
	            (item.request_id IS NOT NULL AND json_type(item.document_json, '$.request_id') = 'text' AND json_extract(item.document_json, '$.request_id') = item.request_id)) AND
	           json_type(item.document_json, '$.success') = 'object' AND
	           json_type(item.document_json, '$.success.transport') IN ('true', 'false') AND
	           json_type(item.document_json, '$.success.protocol') IN ('true', 'false') AND
	           json_type(item.document_json, '$.success.semantic') IN ('true', 'false') AND
	           json_type(item.document_json, '$.success.sla') IN ('true', 'false') AND
	           (json_extract(item.document_json, '$.success.protocol') = 0 OR json_extract(item.document_json, '$.success.transport') = 1) AND
	           (json_extract(item.document_json, '$.success.semantic') = 0 OR json_extract(item.document_json, '$.success.protocol') = 1) AND
	           (json_extract(item.document_json, '$.success.sla') = 0 OR json_extract(item.document_json, '$.success.semantic') = 1) AND
	           (json_type(item.document_json, '$.evidence_ids') IS NULL OR json_type(item.document_json, '$.evidence_ids') = 'array') AND
	           (SELECT COUNT(*) FROM json_each(item.document_json, '$.evidence_ids')) =
	             (SELECT COUNT(DISTINCT reference.value) FROM json_each(item.document_json, '$.evidence_ids') AS reference) AND
	           NOT EXISTS (
	             SELECT 1 FROM json_each(item.document_json, '$.evidence_ids') AS reference
	             LEFT JOIN evidence AS owner
	               ON owner.id = reference.value AND owner.run_id = item.run_id
	             WHERE reference.type != 'text' OR owner.id IS NULL
	           ) AND
	           (item.case_id IS NULL OR EXISTS (
	             SELECT 1
	             FROM execution_runs AS result_root
	             JOIN execution_run_revisions AS result_revision
	               ON result_revision.run_id = result_root.id AND result_revision.revision = result_root.current_revision
	             JOIN json_each(result_revision.snapshot_json, '$.cases') AS planned_case
	             WHERE result_root.id = item.run_id AND
	                   planned_case.type = 'object' AND
	                   json_type(planned_case.value, '$.case_id') = 'text' AND
	                   json_extract(planned_case.value, '$.case_id') = item.case_id
	           ))
	         THEN 0 ELSE 1 END) AS corrupt
	FROM result_observations AS item
	GROUP BY item.run_id
),
evidence_stats AS (
	SELECT item.run_id, COUNT(*) AS evidence_count,
	       SUM(CASE WHEN
	           item.schema_version = ? AND item.revision = 1 AND
	           json_type(item.document_json, '$.id') = 'text' AND json_extract(item.document_json, '$.id') = item.id AND
	           json_type(item.document_json, '$.schema_version') = 'integer' AND json_extract(item.document_json, '$.schema_version') = item.schema_version AND
	           json_type(item.document_json, '$.revision') = 'integer' AND json_extract(item.document_json, '$.revision') = item.revision AND
	           json_type(item.document_json, '$.created_at') = 'text' AND json_extract(item.document_json, '$.created_at') = item.created_at AND
	           json_type(item.document_json, '$.updated_at') = 'text' AND json_extract(item.document_json, '$.updated_at') = item.updated_at AND
	           json_type(item.document_json, '$.run_id') = 'text' AND json_extract(item.document_json, '$.run_id') = item.run_id AND
	           json_type(item.document_json, '$.relative_path') = 'text' AND trim(json_extract(item.document_json, '$.relative_path')) != '' AND
	           json_type(item.document_json, '$.sha256') = 'text' AND length(json_extract(item.document_json, '$.sha256')) = 64 AND
	           json_type(item.document_json, '$.media_type') = 'text' AND trim(json_extract(item.document_json, '$.media_type')) != '' AND
	           json_type(item.document_json, '$.redacted') = 'true'
	         THEN 0 ELSE 1 END) AS corrupt
	FROM evidence AS item
	GROUP BY item.run_id
),
artifact_stats AS (
	SELECT item.run_id, COUNT(*) AS artifact_count,
	       SUM(CASE WHEN
	           json_type(item.document_json, '$.artifact_id') = 'text' AND json_extract(item.document_json, '$.artifact_id') = item.id AND
	           json_type(item.document_json, '$.run_id') = 'text' AND json_extract(item.document_json, '$.run_id') = item.run_id AND
	           json_type(item.document_json, '$.name') = 'text' AND json_extract(item.document_json, '$.name') = item.name AND
	           json_type(item.document_json, '$.relative_path') = 'text' AND json_extract(item.document_json, '$.relative_path') = item.relative_path AND
	           json_type(item.document_json, '$.sha256') = 'text' AND json_extract(item.document_json, '$.sha256') = item.sha256 AND
	           json_type(item.document_json, '$.media_type') = 'text' AND json_extract(item.document_json, '$.media_type') = item.media_type AND
	           item.redacted = 1 AND json_type(item.document_json, '$.redacted') = 'true' AND
	           (SELECT COUNT(*) FROM report_attachments AS link WHERE link.artifact_id = item.id) = 1 AND
	           EXISTS (
	             SELECT 1 FROM report_attachments AS link
	             JOIN reports AS report ON report.id = link.report_id AND report.run_id = item.run_id
	             WHERE link.artifact_id = item.id
	           )
	         THEN 0 ELSE 1 END) AS corrupt
	FROM artifacts AS item
	GROUP BY item.run_id
),
report_stats AS (
	SELECT item.run_id, COUNT(*) AS report_count,
	       MAX(CASE WHEN json_extract(item.document_json, '$.conclusion.passed') = 1 THEN 1 ELSE 0 END) AS conclusion_passed,
	       SUM(CASE WHEN
	           item.schema_version = ? AND
	           json_type(item.document_json, '$.id') = 'text' AND json_extract(item.document_json, '$.id') = item.id AND
	           json_type(item.document_json, '$.schema_version') = 'integer' AND json_extract(item.document_json, '$.schema_version') = item.schema_version AND
	           json_type(item.document_json, '$.run_id') = 'text' AND json_extract(item.document_json, '$.run_id') = item.run_id AND
	           json_type(item.document_json, '$.generated_at') = 'text' AND json_extract(item.document_json, '$.generated_at') = item.generated_at AND
	           json_type(item.document_json, '$.run_status') = 'text' AND
	           json_type(item.document_json, '$.conclusion') = 'object' AND
	           json_type(item.document_json, '$.conclusion.passed') IN ('true', 'false') AND
	           json_type(item.document_json, '$.conclusion.verdict') = 'text' AND trim(json_extract(item.document_json, '$.conclusion.verdict')) != '' AND
	           json_type(item.document_json, '$.conclusion.issues') = 'array' AND
	           NOT EXISTS (
	             SELECT 1 FROM json_each(item.document_json, '$.conclusion.issues') AS issue
	             WHERE issue.type != 'text' OR trim(issue.value) = ''
	           ) AND
	           EXISTS (
	             SELECT 1 FROM execution_runs AS report_root
	             JOIN execution_run_revisions AS report_revision
	               ON report_revision.run_id = report_root.id AND report_revision.revision = report_root.current_revision
	             WHERE report_root.id = item.run_id AND report_root.sealed = 1 AND
	                   json_extract(item.document_json, '$.run_status') = report_revision.status AND
	                   json_extract(item.document_json, '$.plan_snapshot') = json(report_revision.snapshot_json) AND
	                   report_revision.status IN ('completed', 'failed', 'cancelled') AND
	                   (report_revision.status = 'completed' OR json_extract(item.document_json, '$.conclusion.passed') = 0)
	           )
	         THEN 0 ELSE 1 END) AS corrupt
	FROM reports AS item
	GROUP BY item.run_id
)
SELECT root.id, root.current_revision, root.created_at, root.sealed,
	   COALESCE(report_stats.report_count, 0),
	   (SELECT COUNT(*) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
	   (SELECT COALESCE(MIN(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
	   (SELECT COALESCE(MAX(history.revision), 0) FROM execution_run_revisions AS history WHERE history.run_id = root.id),
	   revision.schema_version, revision.revision, revision.created_at, revision.updated_at,
	   revision.plan_id, revision.plan_revision, revision.status,
	   revision.snapshot_json, revision.document_json,
	   COALESCE(result_stats.completed, 0), COALESCE(result_stats.passed, 0), COALESCE(result_stats.failed, 0),
	   COALESCE(evidence_stats.evidence_count, 0) + COALESCE(artifact_stats.artifact_count, 0),
	   COALESCE(result_stats.corrupt, 0), COALESCE(evidence_stats.corrupt, 0),
	   COALESCE(artifact_stats.corrupt, 0), COALESCE(report_stats.corrupt, 0),
	   CASE WHEN COALESCE(report_stats.report_count, 0) = 0 THEN -1 ELSE report_stats.conclusion_passed END
FROM execution_runs AS root
JOIN execution_run_revisions AS revision
	ON revision.run_id = root.id AND revision.revision = root.current_revision
LEFT JOIN result_stats ON result_stats.run_id = root.id
LEFT JOIN evidence_stats ON evidence_stats.run_id = root.id
LEFT JOIN artifact_stats ON artifact_stats.run_id = root.id
LEFT JOIN report_stats ON report_stats.run_id = root.id
ORDER BY root.created_at, root.id
`
