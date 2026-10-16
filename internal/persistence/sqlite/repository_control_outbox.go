package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
)

var _ jobs.ControlRepository = (*Repository)(nil)

const controlColumns = `command_id, job_id, revision, state, document_json`

const controlReadColumns = `command_id, job_id, revision, state,
	CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE NULL END`

func (repository *Repository) CreateControl(
	ctx context.Context,
	record jobs.ControlRecord,
) (jobs.ControlRecord, error) {
	initial := record.Revision == 1 && record.State == jobs.ControlPending && record.Observation == jobs.ObservationNone &&
		record.CreatedAt.Equal(record.UpdatedAt)
	validRecord := initial && record.Validate() == nil
	if ctx == nil || !validRecord {
		return jobs.ControlRecord{}, jobs.ErrInvalid
	}
	document, err := marshalCanonical(record)
	if err != nil || len(document) > jobs.MaxControlRecordBytes {
		return jobs.ControlRecord{}, jobs.ErrInvalid
	}
	record, err = decodeControlRecord(document)
	if err != nil {
		return jobs.ControlRecord{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobs.ControlRecord{}, fmt.Errorf("begin control intent: %w", err)
	}
	defer tx.Rollback()
	previous, err := queryControl(ctx, tx, record.Command.CommandID)
	if err == nil {
		if previous.Command != record.Command {
			return jobs.ControlRecord{}, jobs.ErrConflict
		}
		return previous, nil
	}
	if !errors.Is(err, jobs.ErrNotFound) {
		return jobs.ControlRecord{}, err
	}
	if err := validateControlOwner(ctx, tx, record); err != nil {
		return jobs.ControlRecord{}, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO job_control_outbox (`+controlColumns+`) VALUES (?, ?, ?, ?, ?)`,
		record.Command.CommandID,
		record.Command.JobID,
		record.Revision,
		record.State,
		document,
	)
	if err != nil {
		return jobs.ControlRecord{}, classifySubmissionWrite("create control intent", err)
	}
	if err := tx.Commit(); err != nil {
		return jobs.ControlRecord{}, classifySubmissionWrite("commit control intent", err)
	}
	return record, nil
}

func (repository *Repository) GetControl(ctx context.Context, id string) (jobs.ControlRecord, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return jobs.ControlRecord{}, jobs.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobs.ControlRecord{}, fmt.Errorf("begin control read: %w", err)
	}
	defer tx.Rollback()
	record, err := queryControl(ctx, tx, id)
	if err != nil {
		return jobs.ControlRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return jobs.ControlRecord{}, fmt.Errorf("commit control read: %w", err)
	}
	return record, nil
}

func (repository *Repository) UpdateControl(ctx context.Context, next jobs.ControlRecord, expected uint64) error {
	validRevision := expected > 0 && next.Revision == expected+1
	validNext := validRevision && next.Validate() == nil
	if ctx == nil || !validNext {
		return jobs.ErrInvalid
	}
	document, err := marshalCanonical(next)
	if err != nil || len(document) > jobs.MaxControlRecordBytes {
		return jobs.ErrInvalid
	}
	next, err = decodeControlRecord(document)
	if err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin control update: %w", err)
	}
	defer tx.Rollback()
	previous, err := queryControl(ctx, tx, next.Command.CommandID)
	if err != nil {
		return err
	}
	if previous.Revision != expected {
		return jobs.ErrConflict
	}
	if err := previous.ValidateSuccessor(next); err != nil {
		return err
	}
	if err := validateControlOwner(ctx, tx, next); err != nil {
		return err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE job_control_outbox SET revision = ?, state = ?, document_json = ?
		WHERE command_id = ? AND revision = ?`,
		next.Revision,
		next.State,
		document,
		next.Command.CommandID,
		expected,
	)
	if err != nil {
		return classifySubmissionWrite("update control intent", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read control update count: %w", err)
	}
	if count != 1 {
		return jobs.ErrConflict
	}
	return classifySubmissionWrite("commit control update", tx.Commit())
}

func (repository *Repository) ListPendingControls(
	ctx context.Context,
	after string,
	limit uint32,
) (jobs.ControlPage, error) {
	validCursor := after == "" || domain.IsUUID(after)
	validLimit := limit > 0 && limit <= jobs.MaxSubmissionPageSize
	validPage := validCursor && validLimit
	if ctx == nil || !validPage {
		return jobs.ControlPage{}, jobs.ErrInvalid
	}
	rows, err := repository.db.QueryContext(
		ctx,
		`SELECT `+controlReadColumns+`
		FROM job_control_outbox WHERE state = 'pending' AND command_id > ? ORDER BY command_id LIMIT ?`,
		jobs.MaxControlRecordBytes,
		after,
		limit+1,
	)
	if err != nil {
		return jobs.ControlPage{}, fmt.Errorf("list pending controls: %w", err)
	}
	defer rows.Close()
	page := jobs.ControlPage{Records: []jobs.ControlRecord{}, NextCursor: after}
	for rows.Next() {
		record, err := scanControl(rows)
		if err != nil {
			return jobs.ControlPage{}, err
		}
		if len(page.Records) == int(limit) {
			page.HasMore = true
			break
		}
		page.Records = append(page.Records, record)
		page.NextCursor = record.Command.CommandID
	}
	if err := rows.Err(); err != nil {
		return jobs.ControlPage{}, fmt.Errorf("iterate pending controls: %w", err)
	}
	return page, nil
}

func queryControl(ctx context.Context, tx *sql.Tx, id string) (jobs.ControlRecord, error) {
	record, err := scanControl(tx.QueryRowContext(
		ctx,
		`SELECT `+controlReadColumns+`
		FROM job_control_outbox WHERE command_id = ?`,
		jobs.MaxControlRecordBytes,
		id,
	))
	if err != nil {
		return jobs.ControlRecord{}, err
	}
	if err := validateControlOwner(ctx, tx, record); err != nil {
		invalidOwner := errors.Is(err, jobs.ErrInvalid) || errors.Is(err, jobs.ErrConflict) ||
			errors.Is(err, jobs.ErrNotFound)
		if invalidOwner {
			return jobs.ControlRecord{}, storageCorrupt("control intent owner")
		}
		return jobs.ControlRecord{}, err
	}
	return record, nil
}

func validateControlOwner(ctx context.Context, tx *sql.Tx, record jobs.ControlRecord) error {
	submission, err := querySubmission(ctx, tx, record.Command.JobID)
	if err != nil {
		return err
	}
	return record.ValidateFor(submission)
}

func scanControl(row interface{ Scan(...any) error }) (jobs.ControlRecord, error) {
	var id, jobID, state string
	var revision uint64
	var document []byte
	err := row.Scan(
		&id,
		&jobID,
		&revision,
		&state,
		&document,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.ControlRecord{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.ControlRecord{}, fmt.Errorf("read control intent: %w", err)
	}
	record, err := decodeControlRecord(document)
	if err != nil {
		return jobs.ControlRecord{}, err
	}
	matching := record.Command.CommandID == id && record.Command.JobID == jobID &&
		record.Revision == revision && string(record.State) == state
	if !matching {
		return jobs.ControlRecord{}, storageCorrupt("control intent columns")
	}
	return record, nil
}

func decodeControlRecord(document []byte) (jobs.ControlRecord, error) {
	if len(document) == 0 || len(document) > jobs.MaxControlRecordBytes {
		return jobs.ControlRecord{}, storageCorrupt("control intent byte limit")
	}
	var record jobs.ControlRecord
	if err := decodeCanonical(document, &record, func() error { return record.Validate() }); err != nil {
		return jobs.ControlRecord{}, storageCorrupt("control intent document")
	}
	return record, nil
}
