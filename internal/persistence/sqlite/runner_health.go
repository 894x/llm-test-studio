package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CheckWritable acquires the current writer and validates a write statement
// without changing any task or journal. Disk headroom is checked separately.
func (repository *Repository) CheckWritable(ctx context.Context) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runner write probe: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE execution_jobs SET revision = revision WHERE 0`); err != nil {
		return fmt.Errorf("probe runner writer: %w", err)
	}
	if err := rejectInactiveContentFacts(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		// database/sql may finish its cancellation rollback before Commit,
		// returning ErrTxDone instead of the transaction context's error.
		// Only that exact sentinel is ambiguous; other database failures remain
		// faults even when the context happens to expire at the same time.
		if contextErr := ctx.Err(); err == sql.ErrTxDone && contextErr != nil {
			err = errors.Join(err, contextErr)
		}
		return fmt.Errorf("commit runner write probe: %w", err)
	}
	return nil
}
