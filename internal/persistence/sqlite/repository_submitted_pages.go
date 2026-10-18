package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func validJobPage(ctx context.Context, after string, limit uint32) bool {
	validCursor := after == "" || domain.IsUUID(after)
	validLimit := limit > 0 && limit <= jobs.MaxSubmissionPageSize
	return ctx != nil && validCursor && validLimit
}

func (repository *Repository) Jobs(ctx context.Context, after string, limit uint32) (jobs.JobPage, error) {
	return repository.readJobPage(
		ctx,
		after,
		limit,
		projectStoredJobView,
	)
}

func (repository *Repository) readJobPage(
	ctx context.Context,
	after string,
	limit uint32,
	project func(context.Context, storedJobView) (jobs.JobView, error),
) (jobs.JobPage, error) {
	if !validJobPage(ctx, after, limit) {
		return jobs.JobPage{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.JobPage{}, fmt.Errorf("begin job page: %w", err)
	}
	defer tx.Rollback()
	ids, more, err := jobPageIDs(
		ctx,
		tx,
		after,
		limit,
	)
	if err != nil {
		return jobs.JobPage{}, err
	}
	stored := make([]storedJobView, 0, len(ids))
	for _, entry := range ids {
		source, err := queryStoredJobView(ctx, tx, entry.id)
		if err != nil {
			return jobs.JobPage{}, err
		}
		// All retained documents were counted by the same snapshot's ID query.
		// Scalars have fixed SQL bounds and fit in the per-item metadata allowance.
		if source.byteSize() > entry.bytes+jobPageMetadataBytes {
			return jobs.JobPage{}, storageCorrupt("job page raw bundle byte limit")
		}
		stored = append(stored, source)
	}
	if err := tx.Commit(); err != nil {
		return jobs.JobPage{}, fmt.Errorf("commit job page: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return jobs.JobPage{}, err
	}
	page := jobs.JobPage{SchemaVersion: 1, Jobs: []jobs.JobView{}, NextCursor: after, HasMore: more}
	for index, source := range stored {
		view, err := project(ctx, source)
		stored[index] = storedJobView{}
		if err != nil {
			return jobs.JobPage{}, err
		}
		page.Jobs = append(page.Jobs, view)
		page.NextCursor = view.JobID
	}
	return page, nil
}

// SQL bounds every retained scalar. This allowance covers their fixed size
// outside the existing page budget, which counts complete JSON documents.
const jobPageMetadataBytes = 4096

type jobPageIdentity struct {
	id    string
	bytes int64
}

// Close the bounded ID cursor before loading frozen job sources on pool=1.
// The raw document budget also limits work before expensive typed projection.
func jobPageIDs(ctx context.Context, tx *sql.Tx, after string, limit uint32) ([]jobPageIdentity, bool, error) {
	rows, err := tx.QueryContext(
		ctx,
		`WITH submissions AS MATERIALIZED (
 SELECT job_id FROM job_submissions WHERE job_id > ?1 ORDER BY job_id LIMIT ?3
), mirrors AS MATERIALIZED (
 SELECT job_id FROM remote_job_mirrors WHERE job_id > ?2 ORDER BY job_id LIMIT ?3
), candidates AS MATERIALIZED (
 SELECT job_id FROM (SELECT job_id FROM submissions UNION SELECT job_id FROM mirrors)
 ORDER BY job_id LIMIT ?3)
		SELECT CASE WHEN length(CAST(candidate.job_id AS BLOB)) = 36 THEN candidate.job_id ELSE '' END,
		COALESCE(length(CAST(submission.document_json AS BLOB)), 0),
		COALESCE(length(CAST(mirror.spec_json AS BLOB)), 0),
		COALESCE(length(CAST(mirror.snapshot_json AS BLOB)), 0),
		COALESCE(length(CAST(local.spec_json AS BLOB)), 0),
		COALESCE(length(CAST(local.snapshot_json AS BLOB)), 0),
		COALESCE(length(CAST(remote_result.document_json AS BLOB)), 0),
		COALESCE(length(CAST(local_result.document_json AS BLOB)), 0),
		COALESCE((SELECT SUM(length(CAST(document_json AS BLOB))) FROM remote_job_report_refs
			WHERE job_id = candidate.job_id AND reference_type = 'performance_result'), 0),
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM remote_job_events WHERE job_id = candidate.job_id AND sequence=1), 0) +
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM remote_job_events WHERE job_id = candidate.job_id AND kind='progress' ORDER BY sequence DESC LIMIT 1), 0) +
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM remote_job_events WHERE job_id = candidate.job_id AND kind='performance_result' ORDER BY sequence DESC LIMIT 1), 0) +
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM remote_job_events WHERE job_id = candidate.job_id AND kind='performance_checkpoint' AND sequence < (SELECT sequence FROM remote_job_events WHERE job_id=candidate.job_id AND kind='performance_result' ORDER BY sequence DESC LIMIT 1) ORDER BY sequence DESC LIMIT 1), 0),
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM execution_job_events WHERE job_id = candidate.job_id AND sequence=1), 0) +
		COALESCE((SELECT length(CAST(document_json AS BLOB)) FROM execution_job_events WHERE job_id = candidate.job_id AND kind='progress' ORDER BY sequence DESC LIMIT 1), 0)
		FROM candidates candidate LEFT JOIN job_submissions submission ON submission.job_id = candidate.job_id
		LEFT JOIN remote_job_mirrors mirror ON mirror.job_id = candidate.job_id
		LEFT JOIN execution_jobs local ON local.id = candidate.job_id
		LEFT JOIN remote_performance_results remote_result ON remote_result.job_id = candidate.job_id
		LEFT JOIN execution_performance_results local_result ON local_result.job_id = candidate.job_id
		ORDER BY candidate.job_id`,
		after,
		after,
		limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("read job page ids: %w", err)
	}
	defer rows.Close()
	ids := []jobPageIdentity{}
	var used int64
	var more bool
	for rows.Next() {
		var id string
		var submissionSize, mirrorSpecSize, mirrorSnapshotSize, localSpecSize, localSnapshotSize int64
		var remoteResultSize, localResultSize, referenceSize, remoteEventsSize, localEventsSize int64
		if err := rows.Scan(
			&id,
			&submissionSize,
			&mirrorSpecSize,
			&mirrorSnapshotSize,
			&localSpecSize,
			&localSnapshotSize,
			&remoteResultSize,
			&localResultSize,
			&referenceSize,
			&remoteEventsSize,
			&localEventsSize,
		); err != nil {
			return nil, false, fmt.Errorf("scan job page ids: %w", err)
		}
		validSubmission := submissionSize >= 0 && submissionSize <= maxSubmissionRecordBytes
		validSpecs := mirrorSpecSize <= jobcontract.MaxSubmissionBytes && localSpecSize <= jobcontract.MaxSubmissionBytes
		validSnapshots := mirrorSnapshotSize <= jobcontract.MaxStateBytes && localSnapshotSize <= jobcontract.MaxStateBytes
		validResults := remoteResultSize <= jobcontract.MaxPerformanceResultBytes &&
			localResultSize <= jobcontract.MaxPerformanceResultBytes
		validRemoteJournal := referenceSize <= maxPerformanceReferenceBytes && remoteEventsSize <= 4*jobcontract.MaxEventBytes
		validLocalJournal := localEventsSize <= jobcontract.MaxEventBytes+jobs.MaxProgressViewBytes
		size := submissionSize + mirrorSpecSize + mirrorSnapshotSize + localSpecSize + localSnapshotSize
		size += remoteResultSize + localResultSize + referenceSize + remoteEventsSize + localEventsSize
		validSources := validSubmission && validSpecs && validSnapshots
		validProjection := validResults && validRemoteJournal && validLocalJournal
		validSizes := validSources && validProjection && size > 0
		if !domain.IsUUID(id) || !validSizes {
			return nil, false, storageCorrupt("job page identity or byte limit")
		}
		// One individually valid job must advance even when both stored sources
		// exceed the shared page budget together. Every source remains bounded.
		overBudget := len(ids) > 0 && used+size > jobs.MaxSubmissionPageBytes
		if len(ids) == int(limit) || overBudget {
			more = true
			break
		}
		ids = append(ids, jobPageIdentity{id: id, bytes: size})
		used += size
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate job page ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close job page ids: %w", err)
	}
	return ids, more, nil
}

func (repository *Repository) JobControls(
	ctx context.Context,
	jobID string,
	after string,
	limit uint32,
) (jobs.ControlPageView, error) {
	if !validJobPage(ctx, after, limit) || !domain.IsUUID(jobID) {
		return jobs.ControlPageView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("begin job controls page: %w", err)
	}
	defer tx.Rollback()
	_, err = queryExecution(ctx, tx, jobID)
	if err != nil {
		return jobs.ControlPageView{}, err
	}
	page, err := jobControlsPage(
		ctx,
		tx,
		jobID,
		after,
		limit,
	)
	if err != nil {
		return jobs.ControlPageView{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("commit job controls page: %w", err)
	}
	return page, nil
}

func jobControlsPage(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	after string,
	limit uint32,
) (jobs.ControlPageView, error) {
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
		return jobs.ControlPageView{}, fmt.Errorf("read job controls page: %w", err)
	}
	defer rows.Close()
	records := []jobs.ControlRecord{}
	for rows.Next() {
		record, err := scanControl(rows)
		if err != nil {
			return jobs.ControlPageView{}, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("iterate job controls page: %w", err)
	}
	if err := rows.Close(); err != nil {
		return jobs.ControlPageView{}, fmt.Errorf("close job controls page: %w", err)
	}
	page := jobs.ControlPageView{SchemaVersion: 1, JobID: jobID, Commands: []jobs.ControlView{}, NextCursor: after}
	for _, record := range records {
		if err := validateStoredControlOwner(ctx, tx, record); err != nil {
			return jobs.ControlPageView{}, err
		}
		if len(page.Commands) == int(limit) {
			page.HasMore = true
			break
		}
		view, err := jobs.ProjectControl(record)
		if err != nil {
			return jobs.ControlPageView{}, storageCorrupt("job control projection")
		}
		page.Commands = append(page.Commands, view)
		page.NextCursor = record.Command.CommandID
	}
	return page, nil
}
