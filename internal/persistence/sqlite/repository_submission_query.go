package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ jobs.Query = (*Repository)(nil)
var _ jobs.JobReader = (*Repository)(nil)

type executionRead struct {
	jobs.JobRead
	mirror            *remoteresults.Mirror
	performanceResult *jobcontract.PerformanceResult
}

func (repository *Repository) Job(ctx context.Context, id string) (jobs.JobView, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.JobView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.JobView{}, fmt.Errorf("begin job read: %w", err)
	}
	defer tx.Rollback()
	view, err := queryJobView(ctx, tx, id)
	if err != nil {
		return jobs.JobView{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.JobView{}, fmt.Errorf("commit job read: %w", err)
	}
	return view, nil
}

func (repository *Repository) ReadJob(ctx context.Context, id string) (jobs.JobRead, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.JobRead{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.JobRead{}, fmt.Errorf("begin job source read: %w", err)
	}
	defer tx.Rollback()
	read, err := queryExecution(ctx, tx, id)
	if err != nil {
		return jobs.JobRead{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.JobRead{}, fmt.Errorf("commit job source read: %w", err)
	}
	return read.JobRead, nil
}

func queryJobView(ctx context.Context, tx *sql.Tx, id string) (jobs.JobView, error) {
	stored, err := queryStoredJobView(ctx, tx, id)
	if err != nil {
		return jobs.JobView{}, err
	}
	return projectStoredJobView(ctx, stored)
}

func (repository *Repository) PerformanceResult(
	ctx context.Context,
	id string,
) (jobs.PerformanceResultView, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.PerformanceResultView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("begin job result read: %w", err)
	}
	defer tx.Rollback()
	read, err := queryExecution(ctx, tx, id)
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	if read.Spec.Kind != jobcontract.KindPerformance {
		return jobs.PerformanceResultView{}, jobs.ErrInvalid
	}
	if read.Execution == nil {
		return jobs.PerformanceResultView{}, jobs.ErrNotFound
	}
	ready, err := queryExecutionResultPresence(ctx, tx, &read)
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	if !ready {
		return jobs.PerformanceResultView{}, jobs.ErrNotFound
	}
	var result jobcontract.PerformanceResult
	if read.performanceResult != nil {
		result = *read.performanceResult
	} else {
		document, readErr := queryPerformanceResult(
			ctx,
			tx,
			read.Spec,
			*read.Execution,
		)
		result, err = document.Result, readErr
	}
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	view, err := jobs.ProjectPerformanceResult(read.JobRead, result)
	if err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("%w: job performance projection", ErrCorrupt)
	}
	if err := tx.Commit(); err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("commit job result read: %w", err)
	}
	return view, nil
}

func queryExecution(ctx context.Context, tx *sql.Tx, id string) (executionRead, error) {
	stored, err := queryStoredExecution(ctx, tx, id)
	if err != nil {
		return executionRead{}, err
	}
	return stored.decode()
}

func boundExecutionDocuments(ctx context.Context, tx *sql.Tx, id string, local bool) error {
	statement := `SELECT length(CAST(spec_json AS BLOB)) BETWEEN 1 AND ?
		AND length(CAST(snapshot_json AS BLOB)) BETWEEN 1 AND ?
		FROM remote_job_mirrors WHERE job_id = ?`
	if local {
		statement = `SELECT length(CAST(spec_json AS BLOB)) BETWEEN 1 AND ?
			AND length(CAST(snapshot_json AS BLOB)) BETWEEN 1 AND ?
			FROM execution_jobs WHERE id = ?`
	}
	var bounded bool
	if err := tx.QueryRowContext(
		ctx,
		statement,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxStateBytes,
		id,
	).Scan(&bounded); err != nil {
		return fmt.Errorf("read job execution byte bounds: %w", err)
	}
	if !bounded {
		return storageCorrupt("job execution document byte limit")
	}
	return nil
}

func queryExecutionResultPresence(ctx context.Context, tx *sql.Tx, read *executionRead) (bool, error) {
	stored, err := queryStoredJobPerformance(
		ctx,
		tx,
		read.Spec.JobID,
		read.mirror != nil,
	)
	if err != nil {
		return false, err
	}
	return stored.decode(read)
}
