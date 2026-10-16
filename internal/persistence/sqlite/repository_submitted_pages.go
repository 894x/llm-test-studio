package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func validSubmittedPage(ctx context.Context, after string, limit uint32) bool {
	validCursor := after == "" || domain.IsUUID(after)
	validLimit := limit > 0 && limit <= jobs.MaxSubmissionPageSize
	return ctx != nil && validCursor && validLimit
}

func (repository *Repository) SubmittedJobs(ctx context.Context, after string, limit uint32) (jobs.JobPage, error) {
	if !validSubmittedPage(ctx, after, limit) {
		return jobs.JobPage{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.JobPage{}, fmt.Errorf("begin submitted page: %w", err)
	}
	defer tx.Rollback()
	ids, more, err := submittedPageIDs(
		ctx,
		tx,
		after,
		limit,
	)
	if err != nil {
		return jobs.JobPage{}, err
	}
	page := jobs.JobPage{SchemaVersion: 1, Jobs: []jobs.JobView{}, NextCursor: after, HasMore: more}
	for _, id := range ids {
		view, err := querySubmittedJobView(ctx, tx, id)
		if err != nil {
			return jobs.JobPage{}, err
		}
		page.Jobs = append(page.Jobs, view)
		page.NextCursor = id
	}
	if err := tx.Commit(); err != nil {
		return jobs.JobPage{}, fmt.Errorf("commit submitted page: %w", err)
	}
	return page, nil
}

// Close the bounded ID cursor before loading frozen submissions on pool=1.
// The raw document budget also limits work before expensive typed projection.
func submittedPageIDs(ctx context.Context, tx *sql.Tx, after string, limit uint32) ([]string, bool, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
		CASE WHEN length(CAST(job_id AS BLOB)) = 36 THEN job_id ELSE '' END,
		length(CAST(document_json AS BLOB))
		FROM job_submissions WHERE job_id > ? ORDER BY job_id LIMIT ?`,
		after,
		limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("read submitted page ids: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	var used int64
	var more bool
	for rows.Next() {
		var id string
		var size int64
		if err := rows.Scan(&id, &size); err != nil {
			return nil, false, fmt.Errorf("scan submitted page ids: %w", err)
		}
		validSize := size > 0 && size <= maxSubmissionRecordBytes
		if !domain.IsUUID(id) || !validSize {
			return nil, false, storageCorrupt("submitted page identity or byte limit")
		}
		if len(ids) == int(limit) || used+size > jobs.MaxSubmissionPageBytes {
			more = true
			break
		}
		ids = append(ids, id)
		used += size
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate submitted page ids: %w", err)
	}
	return ids, more, nil
}

func (repository *Repository) SubmittedJobControls(
	ctx context.Context,
	jobID string,
	after string,
	limit uint32,
) (jobs.ControlPageView, error) {
	if !validSubmittedPage(ctx, after, limit) || !domain.IsUUID(jobID) {
		return jobs.ControlPageView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("begin submitted controls page: %w", err)
	}
	defer tx.Rollback()
	submission, err := querySubmission(ctx, tx, jobID)
	if err != nil {
		return jobs.ControlPageView{}, err
	}
	page, err := submittedControlsPage(
		ctx,
		tx,
		submission,
		after,
		limit,
	)
	if err != nil {
		return jobs.ControlPageView{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("commit submitted controls page: %w", err)
	}
	return page, nil
}

func submittedControlsPage(
	ctx context.Context,
	tx *sql.Tx,
	submission jobs.SubmissionRecord,
	after string,
	limit uint32,
) (jobs.ControlPageView, error) {
	jobID := submission.Prepared.Spec.JobID
	rows, err := tx.QueryContext(
		ctx,
		`SELECT `+controlReadColumns+` FROM job_control_outbox
		WHERE job_id = ? AND command_id > ? ORDER BY command_id LIMIT ?`,
		jobs.MaxControlRecordBytes,
		jobID,
		after,
		limit+1,
	)
	if err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("read submitted controls page: %w", err)
	}
	defer rows.Close()
	page := jobs.ControlPageView{SchemaVersion: 1, JobID: jobID, Commands: []jobs.ControlView{}, NextCursor: after}
	for rows.Next() {
		record, err := scanControl(rows)
		if err != nil {
			return jobs.ControlPageView{}, err
		}
		if record.ValidateFor(submission) != nil {
			return jobs.ControlPageView{}, storageCorrupt("submitted control owner")
		}
		if len(page.Commands) == int(limit) {
			page.HasMore = true
			break
		}
		view, err := jobs.ProjectControl(record)
		if err != nil {
			return jobs.ControlPageView{}, storageCorrupt("submitted control projection")
		}
		page.Commands = append(page.Commands, view)
		page.NextCursor = record.Command.CommandID
	}
	if err := rows.Err(); err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("iterate submitted controls page: %w", err)
	}
	return page, nil
}
