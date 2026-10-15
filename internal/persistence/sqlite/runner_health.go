package sqlite

import (
	"context"
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runner write probe: %w", err)
	}
	return nil
}
