package sqlite

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) LookupJob(ctx context.Context, input jobhost.Acceptance) (jobcontract.Snapshot, error) {
	if !jobcontract.IsSHA256(input.SecretCommitment) {
		return jobcontract.Snapshot{}, jobhost.ErrInvalid
	}
	digest, err := input.Spec.Digest()
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	row, err := queryJobRow(ctx, repository.db, input.Spec.JobID)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	sameCommitment := subtle.ConstantTimeCompare([]byte(input.SecretCommitment), []byte(row.commitment)) == 1
	if digest != row.specSHA256 || !sameCommitment {
		return jobcontract.Snapshot{}, jobhost.ErrConflict
	}
	return row.decode()
}

// Include terminal Jobs whose command or shutdown decision committed before
// their child Run/Comparison cleanup. Recovery never executes their requests.
func (repository *Repository) RecoveryJobs(ctx context.Context) ([]jobcontract.Snapshot, error) {
	rows, err := repository.db.QueryContext(ctx, `SELECT `+jobColumns+`
		FROM execution_jobs WHERE settled = 0 OR status IN ('queued', 'starting', 'running', 'draining')
		OR id IN (
			SELECT owners.job_id FROM execution_job_runs owners
			JOIN execution_runs runs ON runs.id = owners.run_id
			JOIN execution_run_revisions revisions ON revisions.run_id = runs.id
			AND revisions.revision = runs.current_revision
			WHERE revisions.status IN ('queued', 'starting', 'running', 'draining')
		)
		OR (kind = 'comparison' AND EXISTS (
			SELECT 1 FROM comparisons
			JOIN comparison_revisions revisions ON revisions.comparison_id = comparisons.id
			AND revisions.revision = comparisons.current_revision
			WHERE comparisons.id = json_extract(spec_json, '$.spec.comparison.meta.id') AND revisions.status = 'running'
		))
		ORDER BY accepted_sequence`)
	if err != nil {
		return nil, fmt.Errorf("list recoverable jobs: %w", err)
	}
	defer rows.Close()
	jobs := []jobcontract.Snapshot{}
	for rows.Next() {
		var row storedJobRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan recoverable job: %w", err)
		}
		snapshot, err := row.decode()
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable jobs: %w", err)
	}
	return jobs, nil
}

// Settlement is a durable boundary after Core report work and reservation
// release. A crash before this write must retry settlement, never execution.
func (repository *Repository) MarkJobSettled(ctx context.Context, id string) error {
	if !domain.IsUUID(id) {
		return jobhost.ErrInvalid
	}
	result, err := repository.db.ExecContext(ctx, `UPDATE execution_jobs SET settled = 1
		WHERE id = ? AND status IN ('completed', 'failed', 'cancelled')`, id)
	if err != nil {
		return classifyJobWriteError("mark job settled", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read job settlement result: %w", err)
	}
	if count != 1 {
		return jobhost.ErrConflict
	}
	return nil
}

func (repository *Repository) RecordJobProgress(
	ctx context.Context,
	id string,
	progress quicktest.PerformanceProgress,
	at time.Time,
) error {
	if !domain.IsUUID(id) {
		return jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin job progress: %w", err)
	}
	defer tx.Rollback()
	spec, err := queryJobSpec(ctx, tx, id)
	if err != nil {
		return err
	}
	if spec.Kind != jobcontract.KindPerformance && !spec.FunctionalProgress {
		return jobhost.ErrInvalid
	}
	if err := journalJobEventTx(ctx, tx, jobEventWrite{
		JobID: id, Kind: jobcontract.EventProgress, At: at, Payload: progress,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return classifyJobWriteError("commit job progress", err)
	}
	return nil
}
