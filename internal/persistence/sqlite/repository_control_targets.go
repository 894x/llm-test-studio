package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
)

func createControlTarget(
	ctx context.Context,
	tx *sql.Tx,
	record jobs.ControlRecord,
	read executionRead,
) error {
	current, err := read.ControlTarget()
	if err != nil {
		return err
	}
	if !record.Target.Equal(current) {
		return jobs.ErrConflict
	}
	if err := record.ValidateFor(read.JobRead); err != nil {
		return err
	}
	document, err := marshalCanonical(record.Target)
	if err != nil || len(document) > jobs.MaxControlTargetBytes {
		return jobs.ErrInvalid
	}
	var submissionID, mirrorID any
	if read.Submission != nil {
		submissionID = record.Command.JobID
	} else {
		mirrorID = record.Command.JobID
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO job_control_targets(command_id, job_id, submission_job_id, mirror_job_id, document_json)
		VALUES (?, ?, ?, ?, ?)`,
		record.Command.CommandID,
		record.Command.JobID,
		submissionID,
		mirrorID,
		document,
	)
	return classifySubmissionWrite("create control target", err)
}

// Every persisted instruction retains a real source FK and its original target.
// New observations may advance execution but cannot redirect durable commands.
func validateStoredControlOwner(ctx context.Context, tx *sql.Tx, record jobs.ControlRecord) error {
	var jobID string
	var submissionID, mirrorID sql.NullString
	var document []byte
	err := tx.QueryRowContext(
		ctx,
		`SELECT job_id, submission_job_id, mirror_job_id,
		CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE '' END
		FROM job_control_targets WHERE command_id = ?`,
		jobs.MaxControlTargetBytes,
		record.Command.CommandID,
	).Scan(&jobID, &submissionID, &mirrorID, &document)
	if errors.Is(err, sql.ErrNoRows) {
		return storageCorrupt("control target missing")
	}
	if err != nil {
		return fmt.Errorf("read control target: %w", err)
	}
	var target jobs.ControlTarget
	if err := decodeCanonical(document, &target, func() error { return target.Validate() }); err != nil {
		return storageCorrupt("control target document")
	}
	submissionSource := submissionID.Valid && !mirrorID.Valid && submissionID.String == jobID
	mirrorSource := !submissionID.Valid && mirrorID.Valid && mirrorID.String == jobID
	matching := jobID == record.Command.JobID && target.Equal(record.Target)
	if !matching || !(submissionSource || mirrorSource) {
		return storageCorrupt("control target identity")
	}
	read, err := queryExecution(ctx, tx, jobID)
	if err == nil {
		missingSource := submissionSource && read.Submission == nil || mirrorSource && read.mirror == nil
		if missingSource {
			return storageCorrupt("control target source")
		}
		err = record.ValidateFor(read.JobRead)
	}
	invalidSource := errors.Is(err, jobs.ErrInvalid) || errors.Is(err, jobs.ErrConflict) ||
		errors.Is(err, jobs.ErrNotFound)
	if invalidSource {
		return storageCorrupt("control target owner")
	}
	return err
}
