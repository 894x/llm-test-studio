package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ jobhost.PerformanceRepository = (*Repository)(nil)

func (repository *Repository) RecordPerformanceObservation(
	ctx context.Context,
	input jobhost.PerformanceObservationWrite,
) (jobcontract.PerformanceObservation, error) {
	if !domain.IsUUID(input.JobID) || input.ExpectedRevision >= math.MaxInt64 {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	next := jobcontract.PerformanceObservation{
		Contract: jobcontract.CurrentContract, JobID: input.JobID, Revision: input.ExpectedRevision + 1,
		RecordedAt: input.At, Checkpoint: input.Checkpoint,
	}
	if err := next.Validate(); err != nil {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	document, err := marshalCanonical(next)
	if err != nil || len(document) > jobcontract.MaxPerformanceObservationBytes {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	// Decode our canonical document to detach every caller-owned pointer before
	// taking the transaction. The persisted value is this exact frozen input.
	var frozen jobcontract.PerformanceObservation
	if err := decodeCanonical(document, &frozen, func() error { return frozen.Validate() }); err != nil {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	next = frozen
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobcontract.PerformanceObservation{}, fmt.Errorf("begin performance observation: %w", err)
	}
	defer tx.Rollback()
	spec, snapshot, err := queryPerformanceOwner(ctx, tx, input.JobID)
	if err != nil {
		return jobcontract.PerformanceObservation{}, err
	}
	if next.ValidateFor(spec) != nil || input.At.Before(snapshot.Receipt.AcceptedAt) {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	previous, previousErr := queryPerformanceObservation(
		ctx,
		tx,
		spec,
		snapshot.Receipt.AcceptedAt,
	)
	if previousErr != nil && !errors.Is(previousErr, jobhost.ErrNotFound) {
		return jobcontract.PerformanceObservation{}, previousErr
	}
	if previousErr == nil && previous.Revision == next.Revision {
		before, err := marshalCanonical(previous.Checkpoint)
		if err != nil {
			return jobcontract.PerformanceObservation{}, err
		}
		after, err := marshalCanonical(next.Checkpoint)
		if err != nil {
			return jobcontract.PerformanceObservation{}, err
		}
		if !bytes.Equal(before, after) {
			return jobcontract.PerformanceObservation{}, jobhost.ErrConflict
		}
		return previous, nil
	}
	if previous.Revision != input.ExpectedRevision {
		return jobcontract.PerformanceObservation{}, jobhost.ErrConflict
	}
	var settled, sealed int
	if err := tx.QueryRowContext(ctx, `SELECT settled, EXISTS(
		SELECT 1 FROM execution_performance_results WHERE job_id = execution_jobs.id
	) FROM execution_jobs WHERE id = ?`, input.JobID).Scan(&settled, &sealed); err != nil {
		return jobcontract.PerformanceObservation{}, fmt.Errorf("inspect performance seal: %w", err)
	}
	started, err := performanceStartedTx(ctx, tx, input.JobID)
	if err != nil {
		return jobcontract.PerformanceObservation{}, err
	}
	if settled != 0 || sealed != 0 || !started {
		return jobcontract.PerformanceObservation{}, jobhost.ErrConflict
	}
	var previousCheckpoint *quicktest.PerformanceCheckpoint
	if previousErr == nil {
		previousCheckpoint = &previous.Checkpoint
		if input.At.Before(previous.RecordedAt) {
			return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
		}
	}
	if quicktest.ValidatePerformanceCheckpointTransition(previousCheckpoint, next.Checkpoint) != nil {
		return jobcontract.PerformanceObservation{}, jobhost.ErrConflict
	}
	if previousErr != nil {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO execution_performance_observations(
			job_id, revision, recorded_at, document_json
		) VALUES(?, ?, ?, ?)`,
			input.JobID,
			next.Revision,
			formatTime(next.RecordedAt),
			document,
		)
	} else {
		var write sql.Result
		write, err = tx.ExecContext(
			ctx,
			`UPDATE execution_performance_observations
			SET revision = ?, recorded_at = ?, document_json = ? WHERE job_id = ? AND revision = ?`,
			next.Revision,
			formatTime(next.RecordedAt),
			document,
			input.JobID,
			input.ExpectedRevision,
		)
		if err == nil {
			count, countErr := write.RowsAffected()
			if countErr != nil {
				return jobcontract.PerformanceObservation{}, fmt.Errorf("read performance observation update count: %w", countErr)
			}
			if count != 1 {
				return jobcontract.PerformanceObservation{}, jobhost.ErrConflict
			}
		}
	}
	if err != nil {
		return jobcontract.PerformanceObservation{}, classifyJobWriteError("write performance observation", err)
	}
	checkpoint := next.Checkpoint
	if err := journalJobEventTx(ctx, tx, jobEventWrite{
		JobID: input.JobID, Kind: jobcontract.EventPerformanceCheckpoint, At: input.At,
		Payload: jobcontract.PerformanceCheckpointEvent{
			ReportID: checkpoint.Report.ReportID, Revision: next.Revision,
			Stage: checkpoint.Stage, Boundary: checkpoint.Boundary, RungIndex: checkpoint.RungIndex,
		},
	}); err != nil {
		return jobcontract.PerformanceObservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobcontract.PerformanceObservation{}, classifyJobWriteError("commit performance observation", err)
	}
	return next, nil
}

func queryPerformanceOwner(
	ctx context.Context,
	queryer rowQueryer,
	id string,
) (jobcontract.JobSpec, jobcontract.Snapshot, error) {
	spec, err := queryJobSpec(ctx, queryer, id)
	if err != nil {
		return jobcontract.JobSpec{}, jobcontract.Snapshot{}, err
	}
	if spec.Kind != jobcontract.KindPerformance {
		return jobcontract.JobSpec{}, jobcontract.Snapshot{}, jobhost.ErrInvalid
	}
	row, err := queryJobRow(ctx, queryer, id)
	if err != nil {
		return jobcontract.JobSpec{}, jobcontract.Snapshot{}, err
	}
	snapshot, err := row.decode()
	if err != nil {
		return jobcontract.JobSpec{}, jobcontract.Snapshot{}, err
	}
	if err := snapshot.Validate(spec); err != nil {
		return jobcontract.JobSpec{}, jobcontract.Snapshot{}, fmt.Errorf("%w: performance owner", ErrCorrupt)
	}
	return spec, snapshot, nil
}

func queryPerformanceObservation(
	ctx context.Context,
	queryer rowQueryer,
	spec jobcontract.JobSpec,
	acceptedAt time.Time,
) (jobcontract.PerformanceObservation, error) {
	var revision int64
	var recordedAt string
	var document []byte
	err := queryer.QueryRowContext(
		ctx,
		`SELECT revision, recorded_at,
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM execution_performance_observations WHERE job_id = ?`,
		jobcontract.MaxPerformanceObservationBytes,
		spec.JobID,
	).Scan(&revision, &recordedAt, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontract.PerformanceObservation{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobcontract.PerformanceObservation{}, fmt.Errorf("read performance observation: %w", err)
	}
	var observation jobcontract.PerformanceObservation
	if err := decodeCanonical(document, &observation, func() error { return observation.ValidateFor(spec) }); err != nil {
		return jobcontract.PerformanceObservation{}, fmt.Errorf("%w: performance observation", ErrCorrupt)
	}
	matching := observation.Revision == uint64(revision) && formatTime(observation.RecordedAt) == recordedAt &&
		!observation.RecordedAt.Before(acceptedAt)
	if !matching {
		return jobcontract.PerformanceObservation{}, fmt.Errorf("%w: performance observation identity", ErrCorrupt)
	}
	return observation, nil
}

func (repository *Repository) GetPerformanceObservation(
	ctx context.Context,
	id string,
) (jobcontract.PerformanceObservation, error) {
	if !domain.IsUUID(id) {
		return jobcontract.PerformanceObservation{}, jobhost.ErrInvalid
	}
	spec, snapshot, err := queryPerformanceOwner(ctx, repository.db, id)
	if err != nil {
		return jobcontract.PerformanceObservation{}, err
	}
	return queryPerformanceObservation(
		ctx,
		repository.db,
		spec,
		snapshot.Receipt.AcceptedAt,
	)
}

func performanceStartedTx(ctx context.Context, queryer rowQueryer, id string) (bool, error) {
	var started bool
	err := queryer.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM execution_job_events WHERE job_id = ? AND kind = 'state'
		AND json_extract(document_json, '$.payload.receipt.status') = 'starting'
	)`, id).Scan(&started)
	if err != nil {
		return false, fmt.Errorf("inspect performance start: %w", err)
	}
	return started, nil
}

func (repository *Repository) SealPerformanceResult(
	ctx context.Context,
	id string,
	at time.Time,
) (jobhost.PerformanceResultDocument, error) {
	if !domain.IsUUID(id) {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("begin performance result: %w", err)
	}
	defer tx.Rollback()
	spec, snapshot, err := queryPerformanceOwner(ctx, tx, id)
	if err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	existing, err := queryPerformanceResult(
		ctx,
		tx,
		spec,
		snapshot,
	)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, jobhost.ErrNotFound) {
		return jobhost.PerformanceResultDocument{}, err
	}
	if !jobcontract.Terminal(snapshot.Receipt.Status) {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrConflict
	}
	started, err := performanceStartedTx(ctx, tx, id)
	if err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	if !started && snapshot.Receipt.Revision > 2 {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("%w: missing performance start fact", ErrCorrupt)
	}
	result := jobcontract.PerformanceResult{
		Contract: jobcontract.CurrentContract, JobID: id, ReportID: spec.Spec.Performance.ReportID,
		Terminal: snapshot, SealedAt: at, ExecutionStarted: started, ObservationState: jobcontract.PerformanceNotStarted,
	}
	if started {
		result.ObservationState = jobcontract.PerformanceInterrupted
	}
	observation, err := queryPerformanceObservation(
		ctx,
		tx,
		spec,
		snapshot.Receipt.AcceptedAt,
	)
	if err == nil {
		if !started {
			return jobhost.PerformanceResultDocument{}, fmt.Errorf("%w: performance observation without start", ErrCorrupt)
		}
		result.Observation = &observation
		if observation.Checkpoint.Boundary == quicktest.PerformanceBoundaryReturned {
			result.ObservationState = jobcontract.PerformanceAvailable
		}
	} else if !errors.Is(err, jobhost.ErrNotFound) {
		return jobhost.PerformanceResultDocument{}, err
	}
	if result.Validate() != nil {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrInvalid
	}
	document, err := marshalCanonical(result)
	if err != nil || len(document) > jobcontract.MaxPerformanceResultBytes {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrInvalid
	}
	reference := performanceResultReference(result, document)
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO execution_performance_results(
		job_id, report_id, job_revision, status, sealed_at, sha256, byte_size, document_json
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		result.ReportID,
		snapshot.Receipt.Revision,
		snapshot.Receipt.Status,
		formatTime(at),
		reference.SHA256,
		reference.Size,
		document,
	); err != nil {
		return jobhost.PerformanceResultDocument{}, classifyJobWriteError("write performance result", err)
	}
	if err := journalJobEventTx(ctx, tx, jobEventWrite{
		JobID: id, Kind: jobcontract.EventPerformanceResult, At: at, Payload: reference,
	}); err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobhost.PerformanceResultDocument{}, classifyJobWriteError("commit performance result", err)
	}
	return jobhost.PerformanceResultDocument{Result: result, Reference: reference, Document: document}, nil
}

func performanceResultReference(
	result jobcontract.PerformanceResult,
	document []byte,
) jobcontract.PerformanceResultReference {
	digest := sha256.Sum256(document)
	return jobcontract.PerformanceResultReference{
		JobID: result.JobID, ReportID: result.ReportID, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(document)),
	}
}

func queryPerformanceResult(
	ctx context.Context,
	queryer rowQueryer,
	spec jobcontract.JobSpec,
	snapshot jobcontract.Snapshot,
) (jobhost.PerformanceResultDocument, error) {
	var revision int64
	var reportID, status, sealedAt, digest string
	var size int64
	var document []byte
	row := queryer.QueryRowContext(
		ctx,
		`SELECT report_id, job_revision, status, sealed_at, sha256, byte_size,
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM execution_performance_results WHERE job_id = ?`,
		jobcontract.MaxPerformanceResultBytes,
		spec.JobID,
	)
	err := row.Scan(
		&reportID,
		&revision,
		&status,
		&sealedAt,
		&digest,
		&size,
		&document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("read performance result: %w", err)
	}
	var result jobcontract.PerformanceResult
	if err := decodeCanonical(document, &result, func() error { return result.Validate() }); err != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("%w: performance result", ErrCorrupt)
	}
	reference := performanceResultReference(result, document)
	matchingRow := reportID == result.ReportID && revision == int64(result.Terminal.Receipt.Revision) &&
		status == string(result.Terminal.Receipt.Status) && sealedAt == formatTime(result.SealedAt)
	matchingDocument := digest == reference.SHA256 && size == reference.Size && reference.Validate() == nil
	matchingOwner := result.JobID == spec.JobID && result.ReportID == spec.Spec.Performance.ReportID &&
		result.Terminal.Validate(spec) == nil
	matchingTerminal := reflect.DeepEqual(result.Terminal.Receipt, snapshot.Receipt) &&
		reflect.DeepEqual(result.Terminal.Failure, snapshot.Failure) &&
		result.Terminal.LatestSequence < snapshot.LatestSequence
	if !matchingRow || !matchingDocument || !matchingOwner || !matchingTerminal {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("%w: performance result identity", ErrCorrupt)
	}
	if result.Observation != nil && result.Observation.ValidateFor(spec) != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("%w: performance result input", ErrCorrupt)
	}
	return jobhost.PerformanceResultDocument{Result: result, Reference: reference, Document: document}, nil
}

func (repository *Repository) ReadPerformanceResult(
	ctx context.Context,
	id string,
) (jobhost.PerformanceResultDocument, error) {
	if !domain.IsUUID(id) {
		return jobhost.PerformanceResultDocument{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("begin performance result read: %w", err)
	}
	defer tx.Rollback()
	spec, snapshot, err := queryPerformanceOwner(ctx, tx, id)
	if err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	result, err := queryPerformanceResult(
		ctx,
		tx,
		spec,
		snapshot,
	)
	if err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobhost.PerformanceResultDocument{}, fmt.Errorf("commit performance result read: %w", err)
	}
	return result, nil
}
