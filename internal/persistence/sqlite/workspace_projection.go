package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

// ListRunProjections transfers only the list's scalar fields and compact entry
// progress. Full snapshots, Case definitions and revision history stay behind
// GetRun/GetRunRevision and the report/result detail read boundaries.
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
		domain.CurrentReportSchemaVersion,
		domain.CurrentRunSnapshotSchemaVersion,
	)
	if err != nil {
		return nil, reportProjectionReadError(ctx, "query workspace projections", err)
	}
	defer rows.Close()
	projections := []workspace.RunProjection{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var document []byte
		var valid, sealed, reportCount int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&document,
			&valid,
			&sealed,
			&reportCount,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, reportProjectionCorrupt(ctx, "workspace summary fields", err)
		}
		if valid != 1 {
			return nil, storageCorrupt("workspace summary requires the current format and consistent owner fields")
		}
		var projection workspace.RunProjection
		if err := json.Unmarshal(document, &projection); err != nil {
			return nil, reportProjectionCorrupt(ctx, "workspace summary document", err)
		}
		if _, err := parseProjectionTimestamp(createdAt); err != nil {
			return nil, reportProjectionCorrupt(ctx, "workspace creation timestamp", err)
		}
		if _, err := parseProjectionTimestamp(updatedAt); err != nil {
			return nil, reportProjectionCorrupt(ctx, "workspace update timestamp", err)
		}
		if err := projection.Validate(); err != nil {
			return nil, reportProjectionCorrupt(ctx, "workspace summary", err)
		}
		validSealValue := sealed == 0 || sealed == 1
		if !validSealValue || sealed != reportCount {
			return nil, storageCorrupt("workspace report seal")
		}
		if (sealed == 0) != (projection.Conclusion == workspace.ConclusionNone) {
			return nil, storageCorrupt("workspace report conclusion")
		}
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, reportProjectionReadError(ctx, "read workspace summaries", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var rootCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_runs`).Scan(&rootCount); err != nil {
		return nil, err
	}
	if rootCount != len(projections) {
		return nil, storageCorrupt("workspace current revision pointer")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return projections, nil
}

// Materialized Result, Report and entry fields contain only requested values.
// Repeated aggregates reuse these small fields without sorting full documents.
const workspaceProjectionQuery = `
WITH current_runs AS MATERIALIZED (
    SELECT root.id, root.current_revision, root.created_at, root.sealed,
           revision.schema_version, revision.created_at AS revision_created_at, revision.updated_at,
           revision.plan_id, revision.plan_revision, revision.status,
           jsonb(revision.snapshot_json) AS snapshot,
           json_extract(revision.document_json,
             '$.schema_version','$.id','$.revision','$.created_at','$.updated_at',
             '$.plan_id','$.status','$.plan_snapshot.schema_version','$.failure') AS header
    FROM execution_runs AS root
    JOIN execution_run_revisions AS revision ON revision.run_id = root.id AND revision.revision = root.current_revision
),
run_owner_claims AS (
    SELECT run_id, job_id, position, 'owned' AS namespace FROM execution_job_runs
    UNION ALL SELECT run_id, job_id, position, 'mirror' AS namespace FROM remote_job_runs
),
run_owner_fields AS MATERIALIZED (
    SELECT owner.run_id, owner.job_id, owner.position,
           CASE owner.namespace WHEN 'owned' THEN owned.id IS NOT NULL AND mirror.job_id IS NULL
               AND owned.kind = json_extract(owned.spec_json,'$.kind')
             ELSE mirror.job_id IS NOT NULL AND owned.id IS NULL END AS namespace_valid,
           CASE owner.namespace WHEN 'owned' THEN json_extract(owned.spec_json,
             '$.job_id','$.kind','$.spec.run.meta.id',
             '$.spec.comparison.runs[' || CAST(MAX(CAST(owner.position AS INTEGER),0) AS TEXT) || '].meta.id')
             ELSE json_extract(mirror.spec_json,
             '$.job_id','$.kind','$.spec.run.meta.id',
             '$.spec.comparison.runs[' || CAST(MAX(CAST(owner.position AS INTEGER),0) AS TEXT) || '].meta.id')
             END AS fields
    FROM run_owner_claims AS owner
    LEFT JOIN execution_jobs AS owned ON owned.id = owner.job_id
    LEFT JOIN remote_job_mirrors AS mirror ON mirror.job_id = owner.job_id
),
run_owner_stats AS (
    SELECT run_id, MIN(job_id) AS job_id, COUNT(*) AS owner_count,
           MIN(COALESCE(namespace_valid AND json_extract(fields,'$[0]') = job_id
             AND typeof(position) = 'integer' AND position >= 0
             AND CASE json_extract(fields,'$[1]')
               WHEN 'run' THEN position = 0 AND json_extract(fields,'$[2]') = run_id
               WHEN 'comparison' THEN json_extract(fields,'$[3]') = run_id
               ELSE 0 END,0)) AS valid
    FROM run_owner_fields GROUP BY run_id
),
entry_documents AS MATERIALIZED (
    SELECT run.id AS run_id, entry.key AS position,
           json_extract(entry.value,'$.entry_id','$.name','$.cases','$.load','$.target_kind') AS fields
    FROM current_runs AS run JOIN json_each(run.snapshot,'$.entries') AS entry
    WHERE entry.type = 'object'
),
planned_entries AS MATERIALIZED (
    SELECT run_id, position, json_extract(fields,'$[0]') AS entry_id,
           json_extract(fields,'$[1]') AS name,
           json_extract(fields,'$[2]') AS cases,
           json_extract(fields,'$[3]') AS load, json_extract(fields,'$[4]') AS target_kind
    FROM entry_documents
),
planned_cases AS MATERIALIZED (
    SELECT entry.run_id, entry.entry_id, json_extract(item.value,'$.case_id') AS case_id
    FROM planned_entries AS entry JOIN json_each(entry.cases) AS item
    WHERE item.type = 'object'
),
request_runs AS MATERIALIZED (
    SELECT run_id, MAX(request_id IS NOT NULL) AS has_requests FROM case_results GROUP BY run_id
),
result_documents AS MATERIALIZED (
    SELECT run_id, entry_id, case_id, request_id, schema_version,
           json_extract(document_json,'$.schema_version','$.run_id','$.entry_status',
             '$.dimensions.phase','$.verification.status','$.execution_status','$.performance.progress.completed') AS fields
    FROM case_results
),
result_fields AS MATERIALIZED (
    SELECT item.run_id, item.entry_id, item.case_id, item.request_id, item.schema_version,
           json_extract(item.fields,'$[0]') AS document_version,
           json_extract(item.fields,'$[1]') AS document_run_id,
           json_extract(item.fields,'$[2]') AS entry_status,
           json_extract(item.fields,'$[4]') AS verdict,
           json_extract(item.fields,'$[5]') AS execution_status,
           json_extract(item.fields,'$[6]') IS NOT NULL AS performance,
           CASE WHEN json_extract(item.fields,'$[6]') IS NOT NULL OR json_type(item.fields,'$[2]') = 'text' OR json_extract(item.fields,'$[3]') IN ('warmup','checkpoint') THEN 0
             WHEN item.request_id IS NOT NULL OR request_runs.has_requests = 0 THEN 1 ELSE 0 END AS observation
    FROM result_documents AS item JOIN request_runs ON request_runs.run_id = item.run_id
),
result_stats AS (
    SELECT item.run_id, SUM(item.observation) AS completed,
           SUM(CASE WHEN item.observation = 1 AND verdict = 'passed' THEN 1 ELSE 0 END) AS passed,
           SUM(CASE WHEN item.observation = 1 AND verdict = 'failed' THEN 1 ELSE 0 END) AS failed,
           SUM(CASE WHEN item.observation = 1 AND verdict = 'not_applicable' THEN 1 ELSE 0 END) AS observed,
           SUM(CASE WHEN item.observation = 1 AND verdict = 'indeterminate' THEN 1 ELSE 0 END) AS indeterminate,
           SUM(CASE WHEN item.schema_version = ?1 AND item.document_version = ?1 AND item.document_run_id = item.run_id
             AND EXISTS (SELECT 1 FROM planned_entries AS entry
               WHERE entry.run_id = item.run_id AND entry.entry_id = item.entry_id AND (
                 (item.entry_status IN ('completed','failed') AND item.case_id IS NULL AND item.request_id IS NULL) OR
                 (item.entry_status IS NULL AND item.execution_status IN ('completed','failed','cancelled')
                   AND item.verdict IN ('passed','failed','not_applicable','indeterminate') AND ((entry.target_kind = 'performance' AND item.performance = 1 AND item.case_id IS NULL AND item.request_id IS NOT NULL) OR EXISTS (
                     SELECT 1 FROM planned_cases AS planned WHERE planned.run_id = item.run_id
                       AND planned.entry_id = item.entry_id AND planned.case_id = item.case_id)))
               )) THEN 0 ELSE 1 END) AS corrupt
    FROM result_fields AS item GROUP BY item.run_id
),
performance_latest AS (
    SELECT MAX(rowid) AS result_row FROM case_results
    WHERE json_type(document_json,'$.performance') = 'object' GROUP BY run_id, entry_id
),
performance_stats AS (
    SELECT result.run_id,
      SUM(json_extract(result.document_json,'$.performance.progress.completed')) AS completed,
      SUM(json_extract(result.document_json,'$.performance.progress.succeeded')) AS passed,
      SUM(json_extract(result.document_json,'$.performance.progress.failed')) AS failed
    FROM case_results AS result JOIN performance_latest ON result.rowid = performance_latest.result_row
    GROUP BY result.run_id
),
entry_results AS (
    SELECT run_id, entry_id, COUNT(DISTINCT case_id) AS observed_cases, COALESCE(MAX(entry_status),'') AS status
    FROM result_fields GROUP BY run_id, entry_id
),
entry_stats AS (
    SELECT entry.run_id,
           json_group_array(json_object('entry_id',entry.entry_id,'name',entry.name,
             'case_count',json_array_length(entry.cases),'performance',json(CASE WHEN entry.target_kind = 'performance' THEN 'true' ELSE 'false' END),'load',json(entry.load)) ORDER BY entry.position) AS entries,
           json_group_array(json_object('EntryID',entry.entry_id,'ObservedCases',COALESCE(result.observed_cases,0),
             'Status',COALESCE(result.status,'')) ORDER BY entry.position) AS results
    FROM planned_entries AS entry LEFT JOIN entry_results AS result
      ON result.run_id = entry.run_id AND result.entry_id = entry.entry_id
    GROUP BY entry.run_id
),
report_documents AS MATERIALIZED (
    SELECT run_id, id, schema_version, json_type(document_json,'$.entry_reports') = 'array' AS current_tree,
           json_extract(document_json,'$.schema_version','$.conclusion.passed') AS fields FROM reports
),
report_stats AS (
    SELECT run_id, COUNT(*) AS report_count, MIN(id) AS report_id,
           MIN(json_extract(fields,'$[1]')) AS conclusion_passed,
           SUM(CASE WHEN current_tree AND schema_version = ?2 AND json_extract(fields,'$[0]') = ?2
             AND json_type(fields,'$[1]') IN ('true','false') THEN 0 ELSE 1 END) AS corrupt
    FROM report_documents GROUP BY run_id
),
evidence_stats AS (SELECT run_id,COUNT(*) AS count FROM evidence GROUP BY run_id),
artifact_stats AS (SELECT run_id,COUNT(*) AS count FROM artifacts GROUP BY run_id)
SELECT json_object(
    'Meta',json_object('id',run.id,'schema_version',run.schema_version,'revision',run.current_revision,
      'created_at',run.created_at,'updated_at',run.updated_at),
    'JobID',COALESCE(run_owner_stats.job_id,''),
    'Plan',json_object('id',run.plan_id,'revision',run.plan_revision),
    'PlanName',json_extract(run.snapshot,'$.plan_document.name'),
    'Model',json_object('id',json_extract(run.snapshot,'$.model.id'),'revision',json_extract(run.snapshot,'$.model.revision'),
      'Name',json_extract(run.snapshot,'$.model.name')),
    'Channel',json_object('id',json_extract(run.snapshot,'$.channel.id'),'revision',json_extract(run.snapshot,'$.channel.revision'),
      'Name',json_extract(run.snapshot,'$.channel.name')),
    'Status',run.status,'Source',CASE WHEN json_type(run.snapshot,'$.quick_task') = 'object' THEN 'quick_task' ELSE '' END,
    'Failure',json_extract(run.header,'$[8]'),
    'Entries',json(COALESCE(entry_stats.entries,'[]')),'EntryResults',json(COALESCE(entry_stats.results,'[]')),
    'ReportID',COALESCE(report_stats.report_id,''),
    'Completed',COALESCE(result_stats.completed,0)+COALESCE(performance_stats.completed,0),'Passed',COALESCE(result_stats.passed,0)+COALESCE(performance_stats.passed,0),
    'Failed',COALESCE(result_stats.failed,0)+COALESCE(performance_stats.failed,0),'Observed',COALESCE(result_stats.observed,0),
    'Indeterminate',COALESCE(result_stats.indeterminate,0),
    'ArtifactCount',COALESCE(evidence_stats.count,0)+COALESCE(artifact_stats.count,0),
    'Conclusion',CASE WHEN COALESCE(report_stats.report_count,0) = 0 THEN 'none'
      WHEN report_stats.conclusion_passed = 1 THEN 'passed' ELSE 'failed' END
), COALESCE(
    run.schema_version = ?1 AND json_extract(run.header,'$[0]') = ?1 AND json_extract(run.header,'$[1]') = run.id
    AND json_extract(run.header,'$[2]') = run.current_revision AND json_extract(run.header,'$[3]') = run.created_at
    AND run.revision_created_at = run.created_at AND json_extract(run.header,'$[4]') = run.updated_at
    AND json_extract(run.header,'$[5]') = run.plan_id AND json_extract(run.header,'$[6]') = run.status
    AND json_extract(run.header,'$[7]') = ?3 AND json_extract(run.snapshot,'$.schema_version') = ?3
    AND json_extract(run.snapshot,'$.plan.id') = run.plan_id AND json_extract(run.snapshot,'$.plan.revision') = run.plan_revision
    AND json_extract(run.snapshot,'$.plan_document.id') = run.plan_id
    AND json_extract(run.snapshot,'$.plan_document.revision') = run.plan_revision
    AND json_type(run.snapshot,'$.entries') = 'array'
    AND COALESCE(run_owner_stats.owner_count,0) <= 1 AND COALESCE(run_owner_stats.valid,1) = 1
    AND COALESCE(result_stats.corrupt,0) = 0 AND COALESCE(report_stats.corrupt,0) = 0, 0
), run.sealed, COALESCE(report_stats.report_count,0), run.created_at, run.updated_at
FROM current_runs AS run
LEFT JOIN run_owner_stats ON run_owner_stats.run_id = run.id
LEFT JOIN result_stats ON result_stats.run_id = run.id
LEFT JOIN performance_stats ON performance_stats.run_id = run.id
LEFT JOIN entry_stats ON entry_stats.run_id = run.id
LEFT JOIN report_stats ON report_stats.run_id = run.id
LEFT JOIN evidence_stats ON evidence_stats.run_id = run.id
LEFT JOIN artifact_stats ON artifact_stats.run_id = run.id
ORDER BY run.created_at,run.id
`
