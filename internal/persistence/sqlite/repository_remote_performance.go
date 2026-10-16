package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const maxPerformanceReferenceBytes = 4096

func (repository *Repository) ImportRemotePerformanceDocument(ctx context.Context, input remoteresults.PerformanceDocumentImport) error {
	validIdentity := input.Origin.Validate() == nil && domain.IsUUID(input.JobID)
	validKind := input.Kind == jobcontract.EventReport || input.Kind == jobcontract.EventPerformanceResult
	limit := jobcontract.MaxPerformanceResultBytes
	if input.Kind == jobcontract.EventReport {
		limit = maxQuickPerformanceReportBytes
	}
	validBytes := len(input.Document) > 0 && len(input.Document) <= limit
	if !validIdentity || !validKind || !validBytes {
		return remoteresults.ErrInvalid
	}
	input.Document = bytes.Clone(input.Document)
	digest := sha256.Sum256(input.Document)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remote performance import: %w", err)
	}
	defer tx.Rollback()
	mirror, err := queryRemoteMirror(ctx, tx, input.JobID)
	if err != nil {
		return err
	}
	if mirror.Origin != input.Origin || mirror.Spec.Kind != jobcontract.KindPerformance {
		return remoteresults.ErrConflict
	}
	reportID := mirror.Spec.Spec.Performance.ReportID
	var rawReference []byte
	var imported bool
	err = tx.QueryRowContext(
		ctx,
		`SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END, imported
		FROM remote_job_report_refs
		WHERE job_id = ? AND reference_type = ? AND report_id = ?`,
		maxPerformanceReferenceBytes,
		input.JobID,
		input.Kind,
		reportID,
	).Scan(&rawReference, &imported)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteresults.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read performance document reference: %w", err)
	}
	var expectedSHA string
	var expectedSize int64
	if input.Kind == jobcontract.EventPerformanceResult {
		var reference jobcontract.PerformanceResultReference
		if err := decodeCanonical(rawReference, &reference, func() error { return reference.Validate() }); err != nil {
			return storageCorrupt("performance result reference")
		}
		if reference.JobID != input.JobID || reference.ReportID != reportID {
			return storageCorrupt("performance result reference owner")
		}
		expectedSHA, expectedSize = reference.SHA256, reference.Size
	} else {
		var reference jobcontract.ReportReference
		if err := decodeCanonical(rawReference, &reference, nil); err != nil {
			return storageCorrupt("performance archive reference")
		}
		validOwner := reference.Kind == jobcontract.KindPerformance && reference.ID == reportID && reference.RunID == ""
		if !validOwner {
			return storageCorrupt("performance archive reference owner")
		}
		expectedSHA, expectedSize = reference.SHA256, reference.Size
	}
	if expectedSHA != hex.EncodeToString(digest[:]) || expectedSize != int64(len(input.Document)) {
		return remoteresults.ErrConflict
	}
	if err := remoteresults.CheckPerformanceBudget(input.Document); err != nil {
		return err
	}
	if input.Kind == jobcontract.EventPerformanceResult {
		if err := importRemotePerformanceResultTx(
			ctx,
			tx,
			mirror,
			input.Document,
			imported,
		); err != nil {
			return err
		}
	} else {
		if err := importRemotePerformanceArchiveTx(
			ctx,
			tx,
			mirror,
			input.Document,
			imported,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE remote_job_report_refs SET imported = 1
		WHERE job_id = ? AND reference_type = ? AND report_id = ?`,
		input.JobID,
		input.Kind,
		reportID,
	); err != nil {
		return fmt.Errorf("record performance document import: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit performance document import", err)
	}
	return nil
}

func validateRemotePerformanceResult(mirror remoteresults.Mirror, result jobcontract.PerformanceResult) error {
	if result.Validate() != nil || result.Terminal.Validate(mirror.Spec) != nil {
		return remoteresults.ErrInvalid
	}
	validOwner := result.JobID == mirror.Spec.JobID && result.ReportID == mirror.Spec.Spec.Performance.ReportID
	validTerminal := reflect.DeepEqual(result.Terminal.Receipt, mirror.Snapshot.Receipt) &&
		reflect.DeepEqual(result.Terminal.Failure, mirror.Snapshot.Failure) && result.Terminal.LatestSequence < mirror.Snapshot.LatestSequence
	if !validOwner || !validTerminal {
		return remoteresults.ErrConflict
	}
	if result.Observation != nil && result.Observation.ValidateFor(mirror.Spec) != nil {
		return remoteresults.ErrConflict
	}
	return nil
}

func importRemotePerformanceResultTx(
	ctx context.Context,
	tx *sql.Tx,
	mirror remoteresults.Mirror,
	raw []byte,
	imported bool,
) error {
	var result jobcontract.PerformanceResult
	if err := decodeCanonical(raw, &result, func() error { return validateRemotePerformanceResult(mirror, result) }); err != nil {
		return err
	}
	if err := validateRemotePerformanceJournal(
		ctx,
		tx,
		result,
		raw,
	); err != nil {
		return err
	}
	if imported {
		matched, err := importedDocumentMatches(ctx, tx.QueryRowContext(ctx,
			`SELECT document_json FROM remote_performance_results WHERE job_id = ?`, mirror.Spec.JobID), raw)
		if err != nil {
			return err
		}
		if !matched {
			return storageCorrupt("imported performance result missing")
		}
		return nil
	}
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO remote_performance_results(job_id, report_id, document_json) VALUES(?, ?, ?)`,
		mirror.Spec.JobID,
		result.ReportID,
		raw,
	)
	if err != nil {
		return classifyWriteError("import full performance result", err)
	}
	return nil
}

func queryRemotePerformanceResult(ctx context.Context, tx *sql.Tx, mirror remoteresults.Mirror) (jobcontract.PerformanceResult, error) {
	var document []byte
	var reportID string
	err := tx.QueryRowContext(
		ctx,
		`SELECT report_id,
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM remote_performance_results WHERE job_id = ?`,
		jobcontract.MaxPerformanceResultBytes,
		mirror.Spec.JobID,
	).Scan(&reportID, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontract.PerformanceResult{}, remoteresults.ErrNotFound
	}
	if err != nil {
		return jobcontract.PerformanceResult{}, fmt.Errorf("read imported performance result: %w", err)
	}
	if reportID != mirror.Spec.Spec.Performance.ReportID {
		return jobcontract.PerformanceResult{}, storageCorrupt("imported result report identity")
	}
	var referenceRaw []byte
	var imported bool
	err = tx.QueryRowContext(
		ctx,
		`SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END, imported
		FROM remote_job_report_refs
		WHERE job_id = ? AND reference_type = 'performance_result' AND report_id = ?`,
		maxPerformanceReferenceBytes,
		mirror.Spec.JobID,
		reportID,
	).Scan(&referenceRaw, &imported)
	if err != nil {
		return jobcontract.PerformanceResult{}, fmt.Errorf("read imported result reference: %w", err)
	}
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(referenceRaw, &reference, func() error { return reference.Validate() }); err != nil {
		return jobcontract.PerformanceResult{}, storageCorrupt("imported result reference")
	}
	digest := sha256.Sum256(document)
	validOwner := reference.JobID == mirror.Spec.JobID && reference.ReportID == reportID && imported
	validBytes := reference.SHA256 == hex.EncodeToString(digest[:]) && reference.Size == int64(len(document))
	if !validOwner || !validBytes {
		return jobcontract.PerformanceResult{}, storageCorrupt("imported result bytes or state")
	}
	if err := remoteresults.CheckPerformanceBudget(document); err != nil {
		return jobcontract.PerformanceResult{}, err
	}
	var result jobcontract.PerformanceResult
	if err := decodeCanonical(document, &result, func() error { return validateRemotePerformanceResult(mirror, result) }); err != nil {
		return jobcontract.PerformanceResult{}, storageCorrupt("imported performance result")
	}
	if err := validateRemotePerformanceJournal(
		ctx,
		tx,
		result,
		document,
	); err != nil {
		return jobcontract.PerformanceResult{}, err
	}
	return result, nil
}

func validateRemotePerformanceJournal(
	ctx context.Context,
	tx *sql.Tx,
	result jobcontract.PerformanceResult,
	raw []byte,
) error {
	var started bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_job_events WHERE job_id = ?
		AND kind = 'state' AND json_extract(document_json, '$.payload.receipt.status') = 'starting')`, result.JobID).Scan(&started); err != nil {
		return fmt.Errorf("read mirrored performance start: %w", err)
	}
	if result.ExecutionStarted != started {
		return remoteresults.ErrConflict
	}
	var eventRaw []byte
	err := tx.QueryRowContext(
		ctx,
		`SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM remote_job_events WHERE job_id = ? AND sequence = ?`,
		jobcontract.MaxEventBytes,
		result.JobID,
		result.Terminal.LatestSequence+1,
	).Scan(&eventRaw)
	if err != nil {
		return fmt.Errorf("read mirrored result event: %w", err)
	}
	event, err := decodeStoredJobEvent(eventRaw)
	if err != nil {
		return err
	}
	validIdentity := event.Kind == jobcontract.EventPerformanceResult && event.JobID == result.JobID
	validEvent := validIdentity && event.CreatedAt.Equal(result.SealedAt) && event.Sequence == result.Terminal.LatestSequence+1
	var reference jobcontract.PerformanceResultReference
	if err := decodeCanonical(event.Payload, &reference, func() error { return reference.Validate() }); err != nil {
		return storageCorrupt("mirrored performance result event")
	}
	if !validEvent || reference != performanceResultReference(result, raw) {
		return remoteresults.ErrConflict
	}
	var checkpointRaw []byte
	var checkpointSequence uint64
	err = tx.QueryRowContext(
		ctx,
		`SELECT sequence, CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM remote_job_events WHERE job_id = ?
		AND kind = 'performance_checkpoint' AND sequence <= ? ORDER BY sequence DESC LIMIT 1`,
		jobcontract.MaxEventBytes,
		result.JobID,
		result.Terminal.LatestSequence,
	).Scan(&checkpointSequence, &checkpointRaw)
	if errors.Is(err, sql.ErrNoRows) {
		if result.Observation == nil {
			return nil
		}
		return remoteresults.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("read mirrored performance checkpoint: %w", err)
	}
	if result.Observation == nil {
		return remoteresults.ErrConflict
	}
	checkpointEvent, err := decodeStoredJobEvent(checkpointRaw)
	if err != nil {
		return err
	}
	validCheckpointEvent := checkpointEvent.JobID == result.JobID && checkpointEvent.Kind == jobcontract.EventPerformanceCheckpoint &&
		checkpointEvent.Sequence == checkpointSequence
	if !validCheckpointEvent {
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

func importRemotePerformanceArchiveTx(
	ctx context.Context,
	tx *sql.Tx,
	mirror remoteresults.Mirror,
	raw []byte,
	imported bool,
) error {
	report, generatedAt, err := decodeQuickPerformanceReport(raw)
	if err != nil {
		return err
	}
	result, err := queryRemotePerformanceResult(ctx, tx, mirror)
	if err != nil {
		return err
	}
	expected, eligible, err := result.StatisticalArchive()
	if err != nil {
		return err
	}
	if !eligible || !equalCanonicalDocuments(expected, report) {
		return remoteresults.ErrConflict
	}
	if imported {
		matched, err := importedDocumentMatches(ctx, tx.QueryRowContext(ctx,
			`SELECT document_json FROM quick_performance_reports WHERE id = ?`, report.ReportID), raw)
		if err != nil {
			return err
		}
		if !matched {
			return storageCorrupt("imported performance archive missing")
		}
		return nil
	}
	return insertQuickPerformanceReportTx(
		ctx,
		tx,
		report,
		generatedAt,
		raw,
	)
}

func (repository *Repository) ReadRemotePerformanceResult(ctx context.Context, jobID string) (jobcontract.PerformanceResult, error) {
	if !domain.IsUUID(jobID) {
		return jobcontract.PerformanceResult{}, remoteresults.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobcontract.PerformanceResult{}, fmt.Errorf("begin imported performance result read: %w", err)
	}
	defer tx.Rollback()
	mirror, err := queryRemoteMirror(ctx, tx, jobID)
	if err != nil {
		return jobcontract.PerformanceResult{}, err
	}
	if mirror.Spec.Kind != jobcontract.KindPerformance {
		return jobcontract.PerformanceResult{}, remoteresults.ErrInvalid
	}
	result, err := queryRemotePerformanceResult(ctx, tx, mirror)
	if err != nil {
		return jobcontract.PerformanceResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobcontract.PerformanceResult{}, fmt.Errorf("commit imported result read: %w", err)
	}
	return result, nil
}
