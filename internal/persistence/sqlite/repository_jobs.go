package sqlite

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ jobhost.Repository = (*Repository)(nil)

func (repository *Repository) AcceptJob(
	ctx context.Context,
	input jobhost.Acceptance,
) (jobcontract.Snapshot, bool, error) {
	if !jobcontract.IsSHA256(input.SecretCommitment) {
		return jobcontract.Snapshot{}, false, jobhost.ErrInvalid
	}
	spec, err := input.Spec.Freeze()
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	snapshot, err := spec.InitialSnapshot(input.AcceptedAt, input.Origin)
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	digest, err := spec.Digest()
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	specDocument, err := marshalCanonical(spec)
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	snapshotDocument, err := marshalCanonical(snapshot)
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	preparedRuns := make([]preparedRunCreate, 0, len(snapshot.Receipt.RunIDs))
	for _, input := range spec.PreparedRuns() {
		run, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
		if err != nil {
			return jobcontract.Snapshot{}, false, err
		}
		prepared, err := prepareRunCreate(run)
		if err != nil {
			return jobcontract.Snapshot{}, false, err
		}
		preparedRuns = append(preparedRuns, prepared)
	}
	var preparedComparison *preparedComparisonCreate
	if spec.Kind == jobcontract.KindComparison {
		comparison, err := spec.Spec.Comparison.Comparison()
		if err != nil {
			return jobcontract.Snapshot{}, false, err
		}
		prepared, err := prepareComparisonCreate(comparison)
		if err != nil {
			return jobcontract.Snapshot{}, false, err
		}
		preparedComparison = &prepared
	}
	event, err := newJobEvent(jobEventWrite{
		JobID: spec.JobID, Kind: jobcontract.EventAccepted, At: input.AcceptedAt,
		Payload: jobcontract.AcceptedEvent{Spec: spec, Snapshot: snapshot},
	}, 1)
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobcontract.Snapshot{}, false, fmt.Errorf("begin job admission: %w", err)
	}
	defer tx.Rollback()
	var remoteOwner bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE job_id = ?)`, spec.JobID).Scan(&remoteOwner); err != nil {
		return jobcontract.Snapshot{}, false, fmt.Errorf("read remote job owner: %w", err)
	}
	if remoteOwner {
		return jobcontract.Snapshot{}, false, jobhost.ErrConflict
	}
	if spec.Kind == jobcontract.KindPerformance {
		if err := requireLocalPerformanceReport(ctx, tx, spec.Spec.Performance.ReportID); err != nil {
			return jobcontract.Snapshot{}, false, err
		}
	}

	stored, err := queryJobRow(ctx, tx, spec.JobID)
	if err == nil {
		sameSpec := stored.specSHA256 == digest
		sameCredential := subtle.ConstantTimeCompare([]byte(stored.commitment), []byte(input.SecretCommitment)) == 1
		if !sameSpec || !sameCredential {
			return jobcontract.Snapshot{}, false, jobhost.ErrConflict
		}
		previous, err := stored.decode()
		return previous, false, err
	}
	if !errors.Is(err, jobhost.ErrNotFound) {
		return jobcontract.Snapshot{}, false, err
	}
	if spec.Kind == jobcontract.KindPerformance {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM quick_performance_reports WHERE id = ?`,
			spec.Spec.Performance.ReportID).Scan(&existing); err != nil {
			return jobcontract.Snapshot{}, false, fmt.Errorf("inspect performance report identity: %w", err)
		}
		if existing != 0 {
			return jobcontract.Snapshot{}, false, jobhost.ErrConflict
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`
		INSERT INTO execution_jobs(
			id, installation_id, engine_build_id, kind, revision, status, accepted_at,
			spec_sha256, secret_commitment, spec_json, snapshot_json, last_sequence
		) VALUES(?, ?, ?, ?, 1, 'queued', ?, ?, ?, ?, ?, 1)
	`,
		spec.JobID,
		spec.NodeInstallationID,
		spec.EngineBuildID,
		spec.Kind,
		formatTime(input.AcceptedAt),
		digest,
		input.SecretCommitment,
		specDocument,
		snapshotDocument,
	); err != nil {
		return jobcontract.Snapshot{}, false, classifyJobWriteError("create job", err)
	}
	for index, prepared := range preparedRuns {
		if err := createRunTx(ctx, tx, prepared); err != nil {
			return jobcontract.Snapshot{}, false, classifyJobWriteError("create job run", err)
		}
		if _, err := tx.ExecContext(
			ctx,
			`
			INSERT INTO execution_job_runs(run_id, job_id, position) VALUES(?, ?, ?)
		`,
			prepared.run.Meta().ID,
			spec.JobID,
			index,
		); err != nil {
			return jobcontract.Snapshot{}, false, classifyJobWriteError("link job run", err)
		}
	}
	if preparedComparison != nil {
		if err := createComparisonTx(ctx, tx, *preparedComparison); err != nil {
			return jobcontract.Snapshot{}, false, classifyJobWriteError("create job comparison", err)
		}
	}
	if err := insertJobEventTx(ctx, tx, event); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return jobcontract.Snapshot{}, false, classifyJobWriteError("commit job admission", err)
	}
	return snapshot, true, nil
}

func (repository *Repository) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	if !domain.IsUUID(id) {
		return jobcontract.Snapshot{}, jobhost.ErrInvalid
	}
	row, err := queryJobRow(ctx, repository.db, id)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	return row.decode()
}

func (repository *Repository) GetJobSpec(ctx context.Context, id string) (jobcontract.JobSpec, error) {
	if !domain.IsUUID(id) {
		return jobcontract.JobSpec{}, jobhost.ErrInvalid
	}
	return queryJobSpec(ctx, repository.db, id)
}

func queryJobSpec(ctx context.Context, queryer rowQueryer, id string) (jobcontract.JobSpec, error) {
	var document []byte
	var installation, build, kind, digest string
	err := queryer.QueryRowContext(ctx, `
		SELECT installation_id, engine_build_id, kind, spec_sha256,
		CASE WHEN length(CAST(spec_json AS BLOB)) <= ? THEN spec_json ELSE '' END
		FROM execution_jobs WHERE id = ?
	`, jobcontract.MaxSubmissionBytes, id).Scan(&installation, &build, &kind, &digest, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontract.JobSpec{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobcontract.JobSpec{}, fmt.Errorf("read job spec: %w", err)
	}
	var spec jobcontract.JobSpec
	if err := decodeCanonical(document, &spec, func() error { return spec.Validate() }); err != nil {
		return jobcontract.JobSpec{}, fmt.Errorf("%w: job spec", ErrCorrupt)
	}
	actualDigest, err := spec.Digest()
	sameIdentity := spec.JobID == id && spec.NodeInstallationID == installation
	sameEngine := spec.EngineBuildID == build && string(spec.Kind) == kind
	if err != nil || !sameIdentity || !sameEngine || actualDigest != digest {
		return jobcontract.JobSpec{}, fmt.Errorf("%w: job spec identity", ErrCorrupt)
	}
	return spec, nil
}

var jobColumns = fmt.Sprintf(`id, installation_id, engine_build_id, kind, revision, status,
	accepted_at, spec_sha256, secret_commitment,
	CASE WHEN length(CAST(snapshot_json AS BLOB)) <= %d THEN snapshot_json ELSE '' END, last_sequence,
	(SELECT CASE WHEN length(CAST(document_json AS BLOB)) <= %d
	 THEN document_json ELSE '' END FROM execution_job_events
	 WHERE job_id = execution_jobs.id AND sequence = 1)`, jobcontract.MaxEventBytes, jobcontract.MaxEventBytes)

type storedJobRow struct {
	id, installation, build, kind, status, acceptedAt string
	specSHA256, commitment                            string
	revision, sequence                                int64
	document                                          []byte
	acceptedDocument                                  []byte
}

func (row *storedJobRow) scan(scanner interface{ Scan(...any) error }) error {
	return scanner.Scan(&row.id, &row.installation, &row.build, &row.kind, &row.revision, &row.status,
		&row.acceptedAt, &row.specSHA256, &row.commitment, &row.document, &row.sequence, &row.acceptedDocument)
}

func queryJobRow(ctx context.Context, queryer rowQueryer, id string) (storedJobRow, error) {
	var row storedJobRow
	err := row.scan(queryer.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM execution_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return storedJobRow{}, jobhost.ErrNotFound
	}
	if err != nil {
		return storedJobRow{}, fmt.Errorf("read job: %w", err)
	}
	return row, nil
}

func (row storedJobRow) decode() (jobcontract.Snapshot, error) {
	var snapshot jobcontract.Snapshot
	if err := decodeCanonical(row.document, &snapshot, func() error { return snapshot.ValidateState() }); err != nil {
		return jobcontract.Snapshot{}, fmt.Errorf("%w: job snapshot", ErrCorrupt)
	}
	receipt := snapshot.Receipt
	sameIdentity := receipt.JobID == row.id && receipt.Origin.InstallationID == row.installation
	sameEngine := receipt.Origin.EngineBuildID == row.build && string(receipt.Kind) == row.kind
	sameState := string(receipt.Status) == row.status && int64(receipt.Revision) == row.revision
	sameJournal := int64(snapshot.LatestSequence) == row.sequence && formatTime(receipt.AcceptedAt) == row.acceptedAt
	validDigests := jobcontract.IsSHA256(row.specSHA256) && jobcontract.IsSHA256(row.commitment)
	if !sameIdentity || !sameEngine || !sameState || !sameJournal || !validDigests {
		return jobcontract.Snapshot{}, fmt.Errorf("%w: job identity", ErrCorrupt)
	}
	accepted, err := decodeAcceptedOrigin(row.acceptedDocument, snapshot)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	if accepted.specSHA256 != row.specSHA256 {
		return jobcontract.Snapshot{}, storageCorrupt("accepted job spec")
	}
	return snapshot, nil
}

func (repository *Repository) ActiveJobs(ctx context.Context) ([]jobcontract.Snapshot, error) {
	rows, err := repository.db.QueryContext(ctx, `SELECT `+jobColumns+`
		FROM execution_jobs WHERE status IN ('queued', 'starting', 'running', 'draining')
		ORDER BY accepted_sequence`)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	defer rows.Close()
	jobs := []jobcontract.Snapshot{}
	for rows.Next() {
		var row storedJobRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan active job: %w", err)
		}
		snapshot, err := row.decode()
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active jobs: %w", err)
	}
	return jobs, nil
}

func (repository *Repository) ChangeJob(ctx context.Context, change jobhost.Change) (jobcontract.Snapshot, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobcontract.Snapshot{}, fmt.Errorf("begin job state change: %w", err)
	}
	defer tx.Rollback()
	next, err := changeJobTx(ctx, tx, change)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobcontract.Snapshot{}, classifyJobWriteError("commit job state", err)
	}
	return next, nil
}

func changeJobTx(ctx context.Context, tx *sql.Tx, change jobhost.Change) (jobcontract.Snapshot, error) {
	validIdentity := domain.IsUUID(change.JobID) && change.ExpectedRevision > 0
	if !validIdentity || change.ExpectedRevision >= math.MaxInt64 {
		return jobcontract.Snapshot{}, jobhost.ErrInvalid
	}
	row, err := queryJobRow(ctx, tx, change.JobID)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	previous, err := row.decode()
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	if previous.Receipt.Revision != change.ExpectedRevision || !validJobTransition(previous.Receipt.Status, change.Status) {
		return jobcontract.Snapshot{}, jobhost.ErrConflict
	}
	if change.At.Before(previous.UpdatedAt) || previous.LatestSequence >= math.MaxInt64 {
		return jobcontract.Snapshot{}, jobhost.ErrInvalid
	}
	next := previous
	next.Receipt.Status = change.Status
	next.Receipt.Revision++
	next.LatestSequence++
	next.UpdatedAt = change.At
	next.Failure = nil
	if change.Failure != nil {
		failure := *change.Failure
		next.Failure = &failure
	}
	if err := next.ValidateState(); err != nil {
		return jobcontract.Snapshot{}, err
	}
	event, err := newJobEvent(jobEventWrite{
		JobID: change.JobID, Kind: jobcontract.EventState, At: change.At, Payload: next,
	}, next.LatestSequence)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	if err := writeJobSnapshotTx(ctx, tx, previous, next); err != nil {
		return jobcontract.Snapshot{}, err
	}
	if err := insertJobEventTx(ctx, tx, event); err != nil {
		return jobcontract.Snapshot{}, err
	}
	return next, nil
}

func validJobTransition(current, next domain.RunStatus) bool {
	switch current {
	case domain.RunQueued:
		return next == domain.RunStarting || next == domain.RunCancelled || next == domain.RunFailed
	case domain.RunStarting:
		return next == domain.RunRunning || next == domain.RunDraining || next == domain.RunCancelled || next == domain.RunFailed
	case domain.RunRunning:
		return next == domain.RunDraining || jobcontract.Terminal(next)
	case domain.RunDraining:
		return jobcontract.Terminal(next)
	default:
		return false
	}
}

func writeJobSnapshotTx(
	ctx context.Context,
	tx *sql.Tx,
	previous jobcontract.Snapshot,
	next jobcontract.Snapshot,
) error {
	if previous.Receipt.Origin != next.Receipt.Origin {
		return storageCorrupt("job execution origin changed")
	}
	document, err := marshalCanonical(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE execution_jobs SET revision = ?, status = ?, snapshot_json = ?, last_sequence = ?
		WHERE id = ? AND revision = ? AND last_sequence = ?
	`, next.Receipt.Revision, next.Receipt.Status, document, next.LatestSequence,
		next.Receipt.JobID, previous.Receipt.Revision, previous.LatestSequence)
	if err != nil {
		return classifyJobWriteError("update job snapshot", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read job update count: %w", err)
	}
	if count != 1 {
		return jobhost.ErrConflict
	}
	return nil
}

type jobEventWrite struct {
	JobID   string
	Kind    jobcontract.EventKind
	At      time.Time
	Payload any
}

func newJobEvent(write jobEventWrite, sequence uint64) (jobcontract.Event, error) {
	payload, err := marshalCanonical(write.Payload)
	if err != nil {
		return jobcontract.Event{}, fmt.Errorf("encode job event payload: %w", err)
	}
	event := jobcontract.Event{
		Contract: jobcontract.CurrentContract, JobID: write.JobID, Sequence: sequence,
		Kind: write.Kind, CreatedAt: write.At, Payload: payload,
	}
	if err := event.Validate(); err != nil {
		return jobcontract.Event{}, err
	}
	return event, nil
}

func insertJobEventTx(ctx context.Context, tx *sql.Tx, event jobcontract.Event) error {
	document, err := marshalCanonical(event)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_job_events(job_id, sequence, kind, created_at, document_json)
		VALUES(?, ?, ?, ?, ?)
	`, event.JobID, event.Sequence, event.Kind, formatTime(event.CreatedAt), document); err != nil {
		return classifyJobWriteError("append job event", err)
	}
	return nil
}

func classifyJobWriteError(operation string, cause error) error {
	err := classifyWriteError(operation, cause)
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: %w", jobhost.ErrConflict, err)
	}
	return err
}

// JSON payloads remain typed by Event.Kind when consumed. This check protects
// the persisted envelope before any projection interprets its current payload.
func decodeStoredJobEvent(document []byte) (jobcontract.Event, error) {
	var event jobcontract.Event
	if err := decodeCanonical(document, &event, func() error { return event.Validate() }); err != nil {
		return jobcontract.Event{}, fmt.Errorf("%w: job event", ErrCorrupt)
	}
	return event, nil
}
