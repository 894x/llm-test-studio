package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
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
	read, err := queryExecution(ctx, tx, id)
	if err != nil {
		return jobs.JobView{}, err
	}
	var progress *jobs.ProgressView
	var resultReady bool
	if read.Execution != nil && read.Spec.Kind == jobcontract.KindPerformance {
		progress, err = queryExecutionProgress(ctx, tx, read)
		if err == nil {
			resultReady, err = queryExecutionResultPresence(ctx, tx, &read)
		}
		if err != nil {
			return jobs.JobView{}, err
		}
	}
	view, err := jobs.ProjectJob(
		read.JobRead,
		progress,
		resultReady,
	)
	if err != nil {
		return jobs.JobView{}, fmt.Errorf("%w: job execution projection", ErrCorrupt)
	}
	return view, nil
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
	record, submissionErr := querySubmission(ctx, tx, id)
	if submissionErr != nil && !errors.Is(submissionErr, jobs.ErrNotFound) {
		return executionRead{}, submissionErr
	}
	read := executionRead{}
	if submissionErr == nil {
		read.Spec, read.Submission = record.Prepared.Spec, &record
		read.Location = jobs.Location{Kind: record.Prepared.Spec.Target.Kind, ConfigurationID: record.Prepared.ConfigurationID}
	}
	var local, remote bool
	err := tx.QueryRowContext(
		ctx,
		`SELECT
		EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?),
		EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`,
		id,
		id,
	).Scan(&local, &remote)
	if err != nil {
		return executionRead{}, fmt.Errorf("read execution namespace: %w", err)
	}
	if local && remote {
		return executionRead{}, storageCorrupt("conflicting execution namespaces")
	}
	if read.Submission == nil && !remote {
		return executionRead{}, jobs.ErrNotFound
	}
	wantsLocal := read.Spec.Target.Kind == jobcontract.TargetLocal
	if read.Submission != nil {
		wrongNamespace := wantsLocal && remote || !wantsLocal && local
		executedBeforeSending := record.State == jobs.SubmissionPrepared && (local || remote)
		if wrongNamespace || executedBeforeSending {
			return executionRead{}, storageCorrupt("submission execution namespace")
		}
	}
	if !local && !remote {
		if wantsLocal && record.State == jobs.SubmissionAccepted {
			return executionRead{}, storageCorrupt("accepted local execution missing")
		}
		return read, nil
	}
	if err := boundExecutionDocuments(
		ctx,
		tx,
		id,
		wantsLocal,
	); err != nil {
		return executionRead{}, err
	}
	var spec jobcontract.JobSpec
	var snapshot jobcontract.Snapshot
	if wantsLocal {
		spec, err = queryJobSpec(ctx, tx, id)
		if err == nil {
			row, readErr := queryJobRow(ctx, tx, id)
			err = readErr
			if err == nil {
				snapshot, err = row.decode()
			}
		}
	} else {
		mirror, readErr := queryRemoteMirror(ctx, tx, id)
		err = readErr
		if err == nil {
			origin := mirror.Origin
			if read.Submission != nil {
				matchingOrigin := origin.ConfigurationID == record.Prepared.ConfigurationID &&
					origin.NodeID == record.Prepared.Runner.NodeID && origin.InstallationID == record.Prepared.Runner.NodeInstallationID
				if !matchingOrigin {
					return executionRead{}, storageCorrupt("submission mirror origin")
				}
			}
			read.Location = jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: origin.ConfigurationID}
			spec, snapshot, read.mirror = mirror.Spec, mirror.Snapshot, &mirror
		}
	}
	if err != nil {
		return executionRead{}, err
	}
	read.Spec, read.Execution = spec, &snapshot
	if read.JobRead.Validate() != nil {
		return executionRead{}, storageCorrupt("execution differs from immutable source")
	}
	return read, nil
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

func queryExecutionProgress(ctx context.Context, tx *sql.Tx, read executionRead) (*jobs.ProgressView, error) {
	statement := `SELECT sequence, kind, '', CASE WHEN length(CAST(document_json AS BLOB)) <= ?
		THEN document_json ELSE '' END FROM remote_job_events
		WHERE job_id = ? AND kind = 'progress' ORDER BY sequence DESC LIMIT 1`
	if read.mirror == nil {
		statement = `SELECT sequence, kind, created_at, CASE WHEN length(CAST(document_json AS BLOB)) <= ?
			THEN document_json ELSE '' END FROM execution_job_events
			WHERE job_id = ? AND kind = 'progress' ORDER BY sequence DESC LIMIT 1`
	}
	var sequence uint64
	var kind, createdAt string
	var document []byte
	err := tx.QueryRowContext(
		ctx,
		statement,
		jobs.MaxProgressViewBytes,
		read.Spec.JobID,
	).Scan(
		&sequence,
		&kind,
		&createdAt,
		&document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read job progress: %w", err)
	}
	event, err := decodeStoredJobEvent(document)
	if err != nil {
		return nil, err
	}
	matching := event.JobID == read.Spec.JobID && event.Kind == jobcontract.EventProgress &&
		event.Sequence == sequence && string(event.Kind) == kind
	if !matching || read.mirror == nil && formatTime(event.CreatedAt) != createdAt {
		return nil, storageCorrupt("job progress envelope")
	}
	var progress quicktest.PerformanceProgress
	if err := decodeCanonical(event.Payload, &progress, nil); err != nil {
		return nil, storageCorrupt("job progress payload")
	}
	view := &jobs.ProgressView{Sequence: sequence,
		RecordedAt: event.CreatedAt.Format(time.RFC3339Nano), Progress: progress}
	if view.Validate(*read.Execution) != nil {
		return nil, storageCorrupt("job progress state")
	}
	return view, nil
}

func queryExecutionResultPresence(ctx context.Context, tx *sql.Tx, read *executionRead) (bool, error) {
	if read.mirror == nil {
		return queryLocalResultPresence(ctx, tx, *read)
	}
	var rows, results int
	var imported int
	err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(imported), 0),
		(SELECT COUNT(*) FROM remote_performance_results WHERE job_id = ?)
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		read.Spec.JobID,
		read.Spec.JobID,
	).Scan(&rows, &imported, &results)
	if err != nil {
		return false, fmt.Errorf("read job result import presence: %w", err)
	}
	validImport := rows <= 1 && imported == results && imported <= 1
	if !validImport {
		return false, storageCorrupt("job result import marker")
	}
	if rows == 0 {
		return false, nil
	}
	var document []byte
	var reportID string
	err = tx.QueryRowContext(ctx, `SELECT report_id,
		CASE WHEN length(CAST(document_json AS BLOB)) <= 4096 THEN document_json ELSE '' END
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		read.Spec.JobID).Scan(&reportID, &document)
	if err != nil {
		return false, fmt.Errorf("read job result import reference: %w", err)
	}
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(document, &reference, func() error { return reference.Validate() }); err != nil {
		return false, storageCorrupt("job result reference")
	}
	validOwner := reference.JobID == read.Spec.JobID &&
		reference.ReportID == read.Spec.Spec.Performance.ReportID && reference.ReportID == reportID
	if !validOwner || !jobcontract.Terminal(read.Execution.Receipt.Status) {
		return false, storageCorrupt("job result reference owner")
	}
	if imported == 0 {
		return false, nil
	}
	var actualReport string
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT report_id, length(CAST(document_json AS BLOB))
		FROM remote_performance_results WHERE job_id = ?`, reference.JobID).Scan(&actualReport, &size)
	if err != nil {
		return false, fmt.Errorf("read job result document presence: %w", err)
	}
	if actualReport != reportID || size != reference.Size {
		return false, storageCorrupt("job result import document")
	}
	result, err := queryRemotePerformanceResult(ctx, tx, *read.mirror)
	if err != nil {
		return false, err
	}
	read.performanceResult = &result
	return true, nil
}

func queryLocalResultPresence(ctx context.Context, tx *sql.Tx, read executionRead) (bool, error) {
	var reportID, digest, status, sealedAt string
	var revision uint64
	var size, actualSize int64
	err := tx.QueryRowContext(ctx, `SELECT report_id, job_revision, status, sealed_at, sha256, byte_size,
		length(CAST(document_json AS BLOB)) FROM execution_performance_results WHERE job_id = ?`,
		read.Spec.JobID).Scan(
		&reportID,
		&revision,
		&status,
		&sealedAt,
		&digest,
		&size,
		&actualSize,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read job local result presence: %w", err)
	}
	reference := jobcontract.PerformanceResultReference{JobID: read.Spec.JobID,
		ReportID: reportID, SHA256: digest, Size: size}
	at, err := time.Parse(time.RFC3339Nano, sealedAt)
	validTime := err == nil && sealedAt == formatTime(at) && !at.Before(read.Execution.Receipt.AcceptedAt)
	validOwner := reportID == read.Spec.Spec.Performance.ReportID &&
		revision == read.Execution.Receipt.Revision && status == string(read.Execution.Receipt.Status)
	validResult := reference.Validate() == nil && actualSize == size && jobcontract.Terminal(read.Execution.Receipt.Status)
	validRecord := validTime && validOwner && validResult
	if !validRecord {
		return false, storageCorrupt("local job result identity")
	}
	return true, nil
}
