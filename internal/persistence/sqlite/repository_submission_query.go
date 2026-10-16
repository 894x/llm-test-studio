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

type submittedExecutionRead struct {
	record   jobs.SubmissionRecord
	snapshot *jobcontract.Snapshot
	mirror   *remoteresults.Mirror
}

func (repository *Repository) SubmittedJob(ctx context.Context, id string) (jobs.JobView, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.JobView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.JobView{}, fmt.Errorf("begin submitted job read: %w", err)
	}
	defer tx.Rollback()
	read, err := querySubmittedExecution(ctx, tx, id)
	if err != nil {
		return jobs.JobView{}, err
	}
	var progress *jobs.ProgressView
	var resultReady bool
	if read.snapshot != nil && read.record.Prepared.Spec.Kind == jobcontract.KindPerformance {
		progress, err = querySubmittedProgress(ctx, tx, read)
		if err == nil {
			resultReady, err = querySubmittedResultPresence(ctx, tx, read)
		}
		if err != nil {
			return jobs.JobView{}, err
		}
	}
	view, err := jobs.ProjectSubmittedJob(
		read.record,
		read.snapshot,
		progress,
		resultReady,
	)
	if err != nil {
		return jobs.JobView{}, fmt.Errorf("%w: submitted execution projection", ErrCorrupt)
	}
	if err := tx.Commit(); err != nil {
		return jobs.JobView{}, fmt.Errorf("commit submitted job read: %w", err)
	}
	return view, nil
}

func (repository *Repository) SubmittedPerformanceResult(
	ctx context.Context,
	id string,
) (jobs.PerformanceResultView, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.PerformanceResultView{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("begin submitted result read: %w", err)
	}
	defer tx.Rollback()
	read, err := querySubmittedExecution(ctx, tx, id)
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	if read.record.Prepared.Spec.Kind != jobcontract.KindPerformance {
		return jobs.PerformanceResultView{}, jobs.ErrInvalid
	}
	if read.snapshot == nil {
		return jobs.PerformanceResultView{}, jobs.ErrNotFound
	}
	ready, err := querySubmittedResultPresence(ctx, tx, read)
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	if !ready {
		return jobs.PerformanceResultView{}, jobs.ErrNotFound
	}
	var result jobcontract.PerformanceResult
	if read.mirror != nil {
		result, err = queryRemotePerformanceResult(ctx, tx, *read.mirror)
		if errors.Is(err, remoteresults.ErrNotFound) {
			err = jobs.ErrNotFound
		}
	} else {
		document, readErr := queryPerformanceResult(
			ctx,
			tx,
			read.record.Prepared.Spec,
			*read.snapshot,
		)
		result, err = document.Result, readErr
	}
	if err != nil {
		return jobs.PerformanceResultView{}, err
	}
	view, err := jobs.ProjectSubmittedPerformanceResult(read.record, result)
	if err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("%w: submitted performance projection", ErrCorrupt)
	}
	if err := tx.Commit(); err != nil {
		return jobs.PerformanceResultView{}, fmt.Errorf("commit submitted result read: %w", err)
	}
	return view, nil
}

func querySubmittedExecution(ctx context.Context, tx *sql.Tx, id string) (submittedExecutionRead, error) {
	record, err := querySubmission(ctx, tx, id)
	if err != nil {
		return submittedExecutionRead{}, err
	}
	read := submittedExecutionRead{record: record}
	var local, remote bool
	err = tx.QueryRowContext(
		ctx,
		`SELECT
		EXISTS(SELECT 1 FROM execution_jobs WHERE id = ?),
		EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`,
		id,
		id,
	).Scan(&local, &remote)
	if err != nil {
		return submittedExecutionRead{}, fmt.Errorf("read submitted execution namespace: %w", err)
	}
	wantsLocal := record.Prepared.Spec.Target.Kind == jobcontract.TargetLocal
	wrongNamespace := wantsLocal && remote || !wantsLocal && local
	executedBeforeSending := record.State == jobs.SubmissionPrepared && (local || remote)
	if wrongNamespace || executedBeforeSending {
		return submittedExecutionRead{}, storageCorrupt("submitted execution namespace")
	}
	if !local && !remote {
		if wantsLocal && record.State == jobs.SubmissionAccepted {
			return submittedExecutionRead{}, storageCorrupt("accepted local execution missing")
		}
		return read, nil
	}
	if err := boundSubmittedExecutionDocuments(
		ctx,
		tx,
		id,
		wantsLocal,
	); err != nil {
		return submittedExecutionRead{}, err
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
			matchingOrigin := origin.ConfigurationID == record.Prepared.ConfigurationID &&
				origin.NodeID == record.Prepared.Runner.NodeID && origin.InstallationID == record.Prepared.Runner.NodeInstallationID
			if !matchingOrigin {
				return submittedExecutionRead{}, storageCorrupt("submitted mirror origin")
			}
			spec, snapshot, read.mirror = mirror.Spec, mirror.Snapshot, &mirror
		}
	}
	if err != nil {
		return submittedExecutionRead{}, err
	}
	digest, err := spec.Digest()
	if err != nil || digest != record.SpecSHA256 || snapshot.Validate(record.Prepared.Spec) != nil {
		return submittedExecutionRead{}, storageCorrupt("submitted execution differs from frozen input")
	}
	read.snapshot = &snapshot
	return read, nil
}

func boundSubmittedExecutionDocuments(ctx context.Context, tx *sql.Tx, id string, local bool) error {
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
		return fmt.Errorf("read submitted execution byte bounds: %w", err)
	}
	if !bounded {
		return storageCorrupt("submitted execution document byte limit")
	}
	return nil
}

func querySubmittedProgress(ctx context.Context, tx *sql.Tx, read submittedExecutionRead) (*jobs.ProgressView, error) {
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
		read.record.Prepared.Spec.JobID,
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
		return nil, fmt.Errorf("read submitted progress: %w", err)
	}
	event, err := decodeStoredJobEvent(document)
	if err != nil {
		return nil, err
	}
	matching := event.JobID == read.record.Prepared.Spec.JobID && event.Kind == jobcontract.EventProgress &&
		event.Sequence == sequence && string(event.Kind) == kind
	if !matching || read.mirror == nil && formatTime(event.CreatedAt) != createdAt {
		return nil, storageCorrupt("submitted progress envelope")
	}
	var progress quicktest.PerformanceProgress
	if err := decodeCanonical(event.Payload, &progress, nil); err != nil {
		return nil, storageCorrupt("submitted progress payload")
	}
	view := &jobs.ProgressView{Sequence: sequence,
		RecordedAt: event.CreatedAt.Format(time.RFC3339Nano), Progress: progress}
	if view.Validate(*read.snapshot) != nil {
		return nil, storageCorrupt("submitted progress state")
	}
	return view, nil
}

func querySubmittedResultPresence(ctx context.Context, tx *sql.Tx, read submittedExecutionRead) (bool, error) {
	if read.mirror == nil {
		return queryLocalResultPresence(ctx, tx, read)
	}
	var rows, results int
	var imported int
	err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(imported), 0),
		(SELECT COUNT(*) FROM remote_performance_results WHERE job_id = ?)
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		read.record.Prepared.Spec.JobID,
		read.record.Prepared.Spec.JobID,
	).Scan(&rows, &imported, &results)
	if err != nil {
		return false, fmt.Errorf("read submitted result import presence: %w", err)
	}
	validImport := rows <= 1 && imported == results && imported <= 1
	if !validImport {
		return false, storageCorrupt("submitted result import marker")
	}
	if rows == 0 {
		return false, nil
	}
	var document []byte
	var reportID string
	err = tx.QueryRowContext(ctx, `SELECT report_id,
		CASE WHEN length(CAST(document_json AS BLOB)) <= 4096 THEN document_json ELSE '' END
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		read.record.Prepared.Spec.JobID).Scan(&reportID, &document)
	if err != nil {
		return false, fmt.Errorf("read submitted result import reference: %w", err)
	}
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(document, &reference, func() error { return reference.Validate() }); err != nil {
		return false, storageCorrupt("submitted result reference")
	}
	validOwner := reference.JobID == read.record.Prepared.Spec.JobID &&
		reference.ReportID == read.record.Prepared.Spec.Spec.Performance.ReportID && reference.ReportID == reportID
	if !validOwner || !jobcontract.Terminal(read.snapshot.Receipt.Status) {
		return false, storageCorrupt("submitted result reference owner")
	}
	if imported == 0 {
		return false, nil
	}
	var actualReport string
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT report_id, length(CAST(document_json AS BLOB))
		FROM remote_performance_results WHERE job_id = ?`, reference.JobID).Scan(&actualReport, &size)
	if err != nil {
		return false, fmt.Errorf("read submitted result document presence: %w", err)
	}
	if actualReport != reportID || size != reference.Size {
		return false, storageCorrupt("submitted result import document")
	}
	return true, nil
}

func queryLocalResultPresence(ctx context.Context, tx *sql.Tx, read submittedExecutionRead) (bool, error) {
	var reportID, digest, status, sealedAt string
	var revision uint64
	var size, actualSize int64
	err := tx.QueryRowContext(ctx, `SELECT report_id, job_revision, status, sealed_at, sha256, byte_size,
		length(CAST(document_json AS BLOB)) FROM execution_performance_results WHERE job_id = ?`,
		read.record.Prepared.Spec.JobID).Scan(
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
		return false, fmt.Errorf("read submitted local result presence: %w", err)
	}
	reference := jobcontract.PerformanceResultReference{JobID: read.record.Prepared.Spec.JobID,
		ReportID: reportID, SHA256: digest, Size: size}
	at, err := time.Parse(time.RFC3339Nano, sealedAt)
	validTime := err == nil && sealedAt == formatTime(at) && !at.Before(read.snapshot.Receipt.AcceptedAt)
	validOwner := reportID == read.record.Prepared.Spec.Spec.Performance.ReportID &&
		revision == read.snapshot.Receipt.Revision && status == string(read.snapshot.Receipt.Status)
	validResult := reference.Validate() == nil && actualSize == size && jobcontract.Terminal(read.snapshot.Receipt.Status)
	validRecord := validTime && validOwner && validResult
	if !validRecord {
		return false, storageCorrupt("submitted local result identity")
	}
	return true, nil
}
