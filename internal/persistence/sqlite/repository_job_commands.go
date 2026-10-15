package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) ApplyJobCommand(
	ctx context.Context,
	command jobcontract.Command,
	at time.Time,
) (jobcontract.CommandConfirmation, bool, error) {
	if err := command.Validate(); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	_, offset := at.Zone()
	if at.IsZero() || offset != 0 {
		return jobcontract.CommandConfirmation{}, false, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, fmt.Errorf("begin job command: %w", err)
	}
	defer tx.Rollback()
	existing, err := queryJobCommand(ctx, tx, command.CommandID)
	if err == nil {
		if existing.Command != command {
			return jobcontract.CommandConfirmation{}, false, jobhost.ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(err, jobhost.ErrNotFound) {
		return jobcontract.CommandConfirmation{}, false, err
	}
	row, err := queryJobRow(ctx, tx, command.JobID)
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	snapshot, err := row.decode()
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	if snapshot.Receipt.Revision != command.ExpectedRevision || jobcontract.Terminal(snapshot.Receipt.Status) {
		return jobcontract.CommandConfirmation{}, false, jobhost.ErrConflict
	}
	if at.Before(snapshot.UpdatedAt) {
		return jobcontract.CommandConfirmation{}, false, jobhost.ErrInvalid
	}
	nextStatus := domain.RunCancelled
	if command.Kind == jobcontract.CommandStopSending && snapshot.Receipt.Status != domain.RunQueued {
		nextStatus = domain.RunDraining
	}
	if nextStatus != snapshot.Receipt.Status {
		snapshot, err = changeJobTx(ctx, tx, jobhost.Change{
			JobID: command.JobID, ExpectedRevision: command.ExpectedRevision, Status: nextStatus, At: at,
		})
		if err != nil {
			return jobcontract.CommandConfirmation{}, false, err
		}
	}
	if snapshot.LatestSequence >= math.MaxInt64 {
		return jobcontract.CommandConfirmation{}, false, jobhost.ErrInvalid
	}
	confirmed := snapshot
	confirmed.LatestSequence++
	confirmed.UpdatedAt = at
	confirmation := jobcontract.CommandConfirmation{Command: command, AcceptedAt: at, Snapshot: confirmed}
	if err := confirmation.Validate(); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	event, err := newJobEvent(jobEventWrite{
		JobID: command.JobID, Kind: jobcontract.EventCommand, At: at, Payload: confirmation,
	}, confirmed.LatestSequence)
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	if err := writeJobSnapshotTx(
		ctx,
		tx,
		snapshot,
		confirmed,
	); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	if err := insertJobEventTx(ctx, tx, event); err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	document, err := marshalCanonical(confirmation)
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO execution_job_commands(
			id, job_id, kind, expected_revision, accepted_at, document_json
		) VALUES(?, ?, ?, ?, ?, ?)`,
		command.CommandID,
		command.JobID,
		command.Kind,
		command.ExpectedRevision,
		formatTime(at),
		document,
	)
	if err != nil {
		return jobcontract.CommandConfirmation{}, false, classifyJobWriteError("write job command", err)
	}
	if err := tx.Commit(); err != nil {
		return jobcontract.CommandConfirmation{}, false, classifyJobWriteError("commit job command", err)
	}
	return confirmation, true, nil
}

func (repository *Repository) GetJobCommand(ctx context.Context, id string) (jobcontract.CommandConfirmation, error) {
	if !domain.IsUUID(id) {
		return jobcontract.CommandConfirmation{}, jobhost.ErrInvalid
	}
	return queryJobCommand(ctx, repository.db, id)
}

func queryJobCommand(ctx context.Context, queryer rowQueryer, id string) (jobcontract.CommandConfirmation, error) {
	var jobID, kind, acceptedAt string
	var expected int64
	var document []byte
	err := queryer.QueryRowContext(ctx, `
		SELECT job_id, kind, expected_revision, accepted_at, document_json
		FROM execution_job_commands WHERE id = ?
	`, id).Scan(
		&jobID,
		&kind,
		&expected,
		&acceptedAt,
		&document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontract.CommandConfirmation{}, jobhost.ErrNotFound
	}
	if err != nil {
		return jobcontract.CommandConfirmation{}, fmt.Errorf("read job command: %w", err)
	}
	var confirmation jobcontract.CommandConfirmation
	if err := decodeCanonical(document, &confirmation, func() error { return confirmation.Validate() }); err != nil {
		return jobcontract.CommandConfirmation{}, fmt.Errorf("%w: job command", ErrCorrupt)
	}
	command := confirmation.Command
	validIdentity := command.CommandID == id && command.JobID == jobID && string(command.Kind) == kind
	validAcceptance := int64(command.ExpectedRevision) == expected && formatTime(confirmation.AcceptedAt) == acceptedAt
	if !validIdentity || !validAcceptance {
		return jobcontract.CommandConfirmation{}, fmt.Errorf("%w: job command identity", ErrCorrupt)
	}
	return confirmation, nil
}
