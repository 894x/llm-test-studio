package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// storedJobPerformance contains only bounded stored bytes and scalar facts.
// Its decoder needs no database connection after the page read transaction ends.
type storedJobPerformance struct {
	jobID  string
	remote bool

	referenceCount    int
	imported          int
	resultCount       int
	reportID          string
	referenceDocument []byte
	resultReportID    string
	resultDocument    []byte

	started            bool
	resultEventCount   int
	resultSequence     uint64
	resultEvent        []byte
	checkpointPresent  bool
	checkpointSequence uint64
	checkpointEvent    []byte

	localPresent    bool
	localDigest     string
	localStatus     string
	localSealedAt   string
	localRevision   uint64
	localSize       int64
	localActualSize int64
}

func queryStoredJobPerformance(
	ctx context.Context,
	tx *sql.Tx,
	id string,
	remote bool,
) (storedJobPerformance, error) {
	stored := storedJobPerformance{jobID: id, remote: remote}
	if remote {
		return queryStoredRemoteJobPerformance(ctx, tx, stored)
	}
	err := tx.QueryRowContext(ctx, `SELECT
		CASE WHEN length(CAST(report_id AS BLOB)) = 36 THEN report_id ELSE '' END,
		CASE WHEN typeof(job_revision) = 'integer' AND job_revision >= 0 THEN job_revision ELSE -1 END,
		CASE WHEN length(CAST(status AS BLOB)) BETWEEN 1 AND 9 THEN status ELSE '' END,
		CASE WHEN length(CAST(sealed_at AS BLOB)) BETWEEN 1 AND 40 THEN sealed_at ELSE '' END,
		CASE WHEN length(CAST(sha256 AS BLOB)) = 64 THEN sha256 ELSE '' END,
		CASE WHEN typeof(byte_size) = 'integer' AND byte_size >= 0 THEN byte_size ELSE -1 END,
		length(CAST(document_json AS BLOB))
		FROM execution_performance_results WHERE job_id = ?`, id).Scan(
		&stored.reportID,
		&stored.localRevision,
		&stored.localStatus,
		&stored.localSealedAt,
		&stored.localDigest,
		&stored.localSize,
		&stored.localActualSize,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, nil
	}
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read job local result presence: %w", err)
	}
	stored.localPresent = true
	return stored, nil
}

func queryStoredRemoteJobPerformance(
	ctx context.Context,
	tx *sql.Tx,
	stored storedJobPerformance,
) (storedJobPerformance, error) {
	err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*), COALESCE(SUM(imported), 0),
		(SELECT COUNT(*) FROM remote_performance_results WHERE job_id = ?)
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		stored.jobID,
		stored.jobID,
	).Scan(&stored.referenceCount, &stored.imported, &stored.resultCount)
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read job result import presence: %w", err)
	}
	// Invalid counts are rejected by decode; they need no arbitrarily selected
	// reference or result document retained in the page bundle.
	if stored.referenceCount != 1 {
		return stored, nil
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT
		CASE WHEN length(CAST(report_id AS BLOB)) = 36 THEN report_id ELSE '' END,
		CASE WHEN length(CAST(document_json AS BLOB)) BETWEEN 1 AND ? THEN document_json ELSE '' END
		FROM remote_job_report_refs WHERE job_id = ? AND reference_type = 'performance_result'`,
		maxPerformanceReferenceBytes,
		stored.jobID,
	).Scan(&stored.reportID, &stored.referenceDocument)
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read job result import reference: %w", err)
	}
	if stored.imported != 1 || stored.resultCount != 1 {
		return stored, nil
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT
		CASE WHEN length(CAST(report_id AS BLOB)) = 36 THEN report_id ELSE '' END,
		CASE WHEN length(CAST(document_json AS BLOB)) BETWEEN 1 AND ? THEN document_json ELSE '' END
		FROM remote_performance_results WHERE job_id = ?`,
		jobcontract.MaxPerformanceResultBytes,
		stored.jobID,
	).Scan(&stored.resultReportID, &stored.resultDocument)
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read imported performance result: %w", err)
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT
		EXISTS(SELECT 1 FROM remote_job_events WHERE job_id = ?
		AND kind = 'state' AND json_extract(document_json, '$.payload.receipt.status') = 'starting'),
		(SELECT COUNT(*) FROM remote_job_events WHERE job_id = ? AND kind = 'performance_result')`,
		stored.jobID,
		stored.jobID,
	).Scan(&stored.started, &stored.resultEventCount)
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read mirrored performance journal facts: %w", err)
	}
	if stored.resultEventCount != 1 {
		return stored, nil
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT CASE WHEN typeof(sequence) = 'integer' AND sequence >= 0 THEN sequence ELSE -1 END,
		CASE WHEN length(CAST(document_json AS BLOB)) BETWEEN 1 AND ? THEN document_json ELSE '' END
		FROM remote_job_events WHERE job_id = ? AND kind = 'performance_result'`,
		jobcontract.MaxEventBytes,
		stored.jobID,
	).Scan(&stored.resultSequence, &stored.resultEvent)
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read mirrored result event: %w", err)
	}
	err = tx.QueryRowContext(
		ctx,
		`SELECT CASE WHEN typeof(sequence) = 'integer' AND sequence >= 0 THEN sequence ELSE -1 END,
		CASE WHEN length(CAST(document_json AS BLOB)) BETWEEN 1 AND ? THEN document_json ELSE '' END
		FROM remote_job_events WHERE job_id = ? AND kind = 'performance_checkpoint'
		AND sequence < ? ORDER BY sequence DESC LIMIT 1`,
		jobcontract.MaxEventBytes,
		stored.jobID,
		stored.resultSequence,
	).Scan(
		&stored.checkpointSequence,
		&stored.checkpointEvent,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, nil
	}
	if err != nil {
		return storedJobPerformance{}, fmt.Errorf("read mirrored performance checkpoint: %w", err)
	}
	stored.checkpointPresent = true
	return stored, nil
}

func (stored storedJobPerformance) byteSize() int64 {
	return int64(len(stored.referenceDocument)) + int64(len(stored.resultDocument)) +
		int64(len(stored.resultEvent)) + int64(len(stored.checkpointEvent))
}

func (stored storedJobPerformance) decode(read *executionRead) (bool, error) {
	if read == nil || read.Execution == nil || read.Spec.Spec.Performance == nil {
		return false, storageCorrupt("job performance projection owner")
	}
	validOwner := read.Spec.Kind == jobcontract.KindPerformance && read.Spec.JobID == stored.jobID
	if !validOwner || stored.remote != (read.mirror != nil) {
		return false, storageCorrupt("job performance projection namespace")
	}
	if !stored.remote {
		return stored.decodeLocal(read)
	}
	validImport := stored.referenceCount >= 0 && stored.referenceCount <= 1 && stored.imported == stored.resultCount
	if !validImport || stored.imported < 0 || stored.imported > 1 {
		return false, storageCorrupt("job result import marker")
	}
	if stored.referenceCount == 0 {
		return false, nil
	}
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(
		stored.referenceDocument,
		&reference,
		func() error { return reference.Validate() },
	); err != nil {
		return false, storageCorrupt("job result reference")
	}
	validReference := reference.JobID == read.Spec.JobID && reference.ReportID == read.Spec.Spec.Performance.ReportID &&
		reference.ReportID == stored.reportID
	if !validReference || !jobcontract.Terminal(read.Execution.Receipt.Status) {
		return false, storageCorrupt("job result reference owner")
	}
	if stored.imported == 0 {
		return false, nil
	}
	if stored.resultReportID != stored.reportID || int64(len(stored.resultDocument)) != reference.Size {
		return false, storageCorrupt("job result import document")
	}
	digest := sha256.Sum256(stored.resultDocument)
	if reference.SHA256 != hex.EncodeToString(digest[:]) {
		return false, storageCorrupt("imported result bytes or state")
	}
	if err := remoteresults.CheckPerformanceBudget(stored.resultDocument); err != nil {
		return false, err
	}
	var result jobcontract.PerformanceResult
	if err := decodeCanonical(stored.resultDocument, &result, func() error {
		return validateRemotePerformanceResult(*read.mirror, result)
	}); err != nil {
		return false, storageCorrupt("imported performance result")
	}
	if result.ExecutionStarted != stored.started || stored.resultEventCount != 1 {
		return false, remoteresults.ErrConflict
	}
	if err := validateStoredPerformanceResultEvent(
		result,
		stored.resultDocument,
		stored.resultEvent,
		stored.resultSequence,
	); err != nil {
		return false, err
	}
	if err := validateStoredPerformanceCheckpoint(
		result,
		stored.checkpointEvent,
		stored.checkpointSequence,
		stored.checkpointPresent,
	); err != nil {
		return false, err
	}
	read.performanceResult = &result
	return true, nil
}

func (stored storedJobPerformance) decodeLocal(read *executionRead) (bool, error) {
	if !stored.localPresent {
		return false, nil
	}
	reference := jobcontract.PerformanceResultReference{
		JobID: read.Spec.JobID, ReportID: stored.reportID, SHA256: stored.localDigest, Size: stored.localSize,
	}
	at, err := time.Parse(time.RFC3339Nano, stored.localSealedAt)
	validTime := err == nil && stored.localSealedAt == formatTime(at) && !at.Before(read.Execution.Receipt.AcceptedAt)
	validOwner := stored.reportID == read.Spec.Spec.Performance.ReportID &&
		stored.localRevision == read.Execution.Receipt.Revision && stored.localStatus == string(read.Execution.Receipt.Status)
	validResult := reference.Validate() == nil && stored.localActualSize == stored.localSize &&
		jobcontract.Terminal(read.Execution.Receipt.Status)
	if !validTime || !validOwner || !validResult {
		return false, storageCorrupt("local job result identity")
	}
	return true, nil
}

func validateStoredPerformanceResultEvent(
	result jobcontract.PerformanceResult,
	raw []byte,
	eventRaw []byte,
	storedSequence uint64,
) error {
	event, err := decodeStoredJobEvent(eventRaw)
	if err != nil {
		return err
	}
	validIdentity := event.Kind == jobcontract.EventPerformanceResult && event.JobID == result.JobID
	validSequence := event.Sequence == storedSequence && event.Sequence == result.Terminal.LatestSequence+1
	validEvent := validIdentity && validSequence && event.CreatedAt.Equal(result.SealedAt)
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(event.Payload, &reference, func() error { return reference.Validate() }); err != nil {
		return storageCorrupt("mirrored performance result event")
	}
	if !validEvent || reference != performanceResultReference(result, raw) {
		return remoteresults.ErrConflict
	}
	return nil
}

func validateStoredPerformanceCheckpoint(
	result jobcontract.PerformanceResult,
	checkpointRaw []byte,
	checkpointSequence uint64,
	present bool,
) error {
	if !present {
		if result.Observation == nil {
			return nil
		}
		return remoteresults.ErrConflict
	}
	if result.Observation == nil {
		return remoteresults.ErrConflict
	}
	checkpointEvent, err := decodeStoredJobEvent(checkpointRaw)
	if err != nil {
		return err
	}
	validCheckpointEvent := checkpointEvent.JobID == result.JobID &&
		checkpointEvent.Kind == jobcontract.EventPerformanceCheckpoint && checkpointEvent.Sequence == checkpointSequence
	if !validCheckpointEvent || checkpointSequence > result.Terminal.LatestSequence {
		return storageCorrupt("mirrored checkpoint envelope")
	}
	var checkpoint jobcontract.PerformanceCheckpointEvent
	if err := decodeCanonical(checkpointEvent.Payload, &checkpoint, nil); err != nil {
		return storageCorrupt("mirrored checkpoint event")
	}
	observation := result.Observation
	matchingCheckpoint := checkpoint.ReportID == result.ReportID && checkpoint.Revision == observation.Revision &&
		checkpoint.Stage == observation.Checkpoint.Stage && checkpoint.Boundary == observation.Checkpoint.Boundary
	matchingTime := checkpointEvent.CreatedAt.Equal(observation.RecordedAt) &&
		reflect.DeepEqual(checkpoint.RungIndex, observation.Checkpoint.RungIndex)
	if !matchingCheckpoint || !matchingTime {
		return remoteresults.ErrConflict
	}
	return nil
}
