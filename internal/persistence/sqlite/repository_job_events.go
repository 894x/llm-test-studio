package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func (repository *Repository) JobEvents(
	ctx context.Context,
	id string,
	after uint64,
	limit uint32,
) (jobhost.EventPage, error) {
	validIdentity := domain.IsUUID(id) && after <= math.MaxInt64
	validLimit := limit >= 1 && limit <= 200
	if !validIdentity || !validLimit {
		return jobhost.EventPage{}, jobhost.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobhost.EventPage{}, fmt.Errorf("begin job event read: %w", err)
	}
	defer tx.Rollback()
	stored, err := queryJobRow(ctx, tx, id)
	if err != nil {
		return jobhost.EventPage{}, err
	}
	snapshot, err := stored.decode()
	if err != nil {
		return jobhost.EventPage{}, err
	}
	if after > snapshot.LatestSequence {
		return jobhost.EventPage{}, jobhost.ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT sequence, kind, created_at, document_json FROM execution_job_events
		WHERE job_id = ? AND sequence > ? ORDER BY sequence LIMIT ?
	`, id, after, limit)
	if err != nil {
		return jobhost.EventPage{}, fmt.Errorf("read job events: %w", err)
	}
	defer rows.Close()
	page := jobhost.EventPage{Events: []jobcontract.Event{}, LastSequence: after}
	var bytesRead int
	byteCapped := false
	for rows.Next() {
		var sequence int64
		var kind, createdAt string
		var document []byte
		if err := rows.Scan(&sequence, &kind, &createdAt, &document); err != nil {
			return jobhost.EventPage{}, fmt.Errorf("scan job event: %w", err)
		}
		if len(document) > jobcontract.MaxEventBytes {
			return jobhost.EventPage{}, fmt.Errorf("%w: oversized job event", ErrCorrupt)
		}
		if len(page.Events) > 0 && bytesRead+len(document) > jobcontract.MaxEventBytes {
			byteCapped = true
			break
		}
		event, err := decodeStoredJobEvent(document)
		if err != nil {
			return jobhost.EventPage{}, err
		}
		contiguous := event.Sequence == page.LastSequence+1 && int64(event.Sequence) == sequence
		validSequence := contiguous && event.Sequence <= snapshot.LatestSequence
		validIdentity := event.JobID == id && string(event.Kind) == kind
		if !validSequence || !validIdentity || formatTime(event.CreatedAt) != createdAt {
			return jobhost.EventPage{}, fmt.Errorf("%w: job event identity or gap", ErrCorrupt)
		}
		page.Events = append(page.Events, event)
		page.LastSequence = event.Sequence
		bytesRead += len(document)
	}
	if err := rows.Err(); err != nil {
		return jobhost.EventPage{}, fmt.Errorf("iterate job events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return jobhost.EventPage{}, fmt.Errorf("close job events: %w", err)
	}
	page.HasMore = page.LastSequence < snapshot.LatestSequence
	if page.HasMore && len(page.Events) < int(limit) && !byteCapped {
		return jobhost.EventPage{}, fmt.Errorf("%w: missing job events", ErrCorrupt)
	}
	document, err := json.Marshal(page)
	if err != nil || len(document) > jobcontract.MaxEventPageBytes {
		return jobhost.EventPage{}, fmt.Errorf("%w: oversized job event page", ErrCorrupt)
	}
	if err := tx.Commit(); err != nil {
		return jobhost.EventPage{}, fmt.Errorf("commit job event read: %w", err)
	}
	return page, nil
}

func reportReference(id string, kind jobcontract.Kind, runID string, document []byte) jobcontract.ReportReference {
	digest := sha256.Sum256(document)
	return jobcontract.ReportReference{
		ID: id, Kind: kind, RunID: runID, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(document)),
	}
}

func journalJobEventTx(ctx context.Context, tx *sql.Tx, write jobEventWrite) error {
	stored, err := queryJobRow(ctx, tx, write.JobID)
	if err != nil {
		return err
	}
	previous, err := stored.decode()
	if err != nil {
		return err
	}
	if previous.LatestSequence >= math.MaxInt64 {
		return jobhost.ErrInvalid
	}
	next := previous
	next.LatestSequence++
	if write.At.After(next.UpdatedAt) {
		next.UpdatedAt = write.At
	}
	event, err := newJobEvent(write, next.LatestSequence)
	if err != nil {
		return err
	}
	if err := writeJobSnapshotTx(ctx, tx, previous, next); err != nil {
		return err
	}
	return insertJobEventTx(ctx, tx, event)
}

// Catalog-free Core callers may persist execution facts without owning a Job.
// Owned runs always journal their change in the same transaction as that fact.
func journalRunEventTx(ctx context.Context, tx *sql.Tx, runID string, write jobEventWrite) error {
	var jobID string
	err := tx.QueryRowContext(ctx, `SELECT job_id FROM execution_job_runs WHERE run_id = ?`, runID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read execution job owner: %w", err)
	}
	write.JobID = jobID
	return journalJobEventTx(ctx, tx, write)
}
