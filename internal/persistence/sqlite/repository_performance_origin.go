package sqlite

import (
	"context"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

func validatePerformanceArchiveOrigin(ctx context.Context, queryer relationQueryer, report quicktest.PerformanceReport) error {
	rows, err := queryer.QueryContext(ctx, `SELECT 'owned', id FROM execution_jobs
		WHERE kind = 'performance' AND json_extract(spec_json, '$.spec.performance.report_id') = ?
		UNION ALL SELECT 'mirror', job_id FROM remote_job_mirrors WHERE performance_report_id = ? LIMIT 3`,
		report.ReportID, report.ReportID)
	if err != nil {
		return fmt.Errorf("read performance archive origin owner: %w", err)
	}
	defer rows.Close()
	var namespace, jobID string
	var count int
	for rows.Next() {
		if err := rows.Scan(&namespace, &jobID); err != nil {
			return fmt.Errorf("scan performance archive origin owner: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate performance archive origin owners: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close performance archive origin owners: %w", err)
	}
	if count > 1 {
		return storageCorrupt("duplicate performance execution namespace")
	}
	var origin *domain.ExecutionOrigin
	if count == 1 {
		if namespace == "owned" {
			_, snapshot, err := queryPerformanceOwner(ctx, queryer, jobID)
			if err != nil {
				return relationStorageError(ctx, "performance archive origin", err)
			}
			frozen := snapshot.Receipt.Origin
			origin = &frozen
		} else {
			mirror, err := queryRemoteMirror(ctx, queryer, jobID)
			if err != nil {
				return relationStorageError(ctx, "performance archive origin", err)
			}
			if mirror.Spec.Spec.Performance == nil || mirror.Spec.Spec.Performance.ReportID != report.ReportID {
				return storageCorrupt("performance archive frozen owner")
			}
			frozen := mirror.Snapshot.Receipt.Origin
			origin = &frozen
		}
		var duplicate bool
		if err := queryer.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?) AND
			EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`, jobID, jobID).Scan(&duplicate); err != nil {
			return fmt.Errorf("inspect performance archive origin namespace: %w", err)
		}
		if duplicate {
			return storageCorrupt("duplicate performance job namespace")
		}
	}
	return validateStoredOrigin(origin, report.ExecutionOrigin)
}
