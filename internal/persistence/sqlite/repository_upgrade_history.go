package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
)

// ErrHistoricalJob identifies preserved, read-only jobs which cannot safely be
// resumed because their original producer did not record executor provenance.
var ErrHistoricalJob = errors.New("historical job is read-only after upgrade; create a new job to execute again")

func rejectHistoricalResource(ctx context.Context, queryer rowQueryer, kind, id string) error {
	var query string
	switch kind {
	case "run":
		query = "SELECT EXISTS(SELECT 1 FROM upgrade_history_runs WHERE run_id = ?)"
	case "comparison":
		query = "SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE comparison_id = ?)"
	case "performance":
		query = "SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE performance_report_id = ?)"
	default:
		return ErrCorrupt
	}
	var found bool
	if err := queryer.QueryRowContext(ctx, query, id).Scan(&found); err != nil {
		return err
	}
	if found {
		return errors.Join(ErrConflict, ErrHistoricalJob)
	}
	return nil
}

func rejectArchivedJob(ctx context.Context, queryer rowQueryer, id string) error {
	var found bool
	row := queryer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE job_id = ?)", id)
	if err := row.Scan(&found); err != nil {
		return fmt.Errorf("inspect historical job: %w", err)
	}
	if found {
		return errors.Join(jobhost.ErrConflict, ErrHistoricalJob)
	}
	return nil
}

// UpgradeHistoryPage exposes losslessly preserved SQL cells for inspection and
// export. Blob cells are []byte, integer cells int64, and original JSON remains
// raw text/blob. These values must never be sent to current job admission.
type UpgradeHistoryPage struct {
	SourceChecksum string
	Columns        []string
	TotalRows      int64
	After          int64
	Rows           [][]any
}

func (repository *Repository) ReadUpgradeHistory(
	ctx context.Context,
	table string,
	after int64,
	limit int,
) (UpgradeHistoryPage, error) {
	page := UpgradeHistoryPage{After: after, Rows: [][]any{}}
	if !historicalJobTable(table) || after < 0 || limit < 1 || limit > 100 {
		return page, errors.New("invalid upgrade history page")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var columns string
	err = tx.QueryRowContext(ctx, `SELECT source_checksum, columns_json, row_count
		FROM upgrade_history_tables WHERE table_name = ?`, table).
		Scan(&page.SourceChecksum, &columns, &page.TotalRows)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	if json.Unmarshal([]byte(columns), &page.Columns) != nil {
		return page, ErrCorrupt
	}
	rows, err := tx.QueryContext(
		ctx,
		`SELECT position, values_json FROM upgrade_history_rows
		WHERE table_name = ? AND position > ? ORDER BY position LIMIT ?`,
		table,
		after,
		limit,
	)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var document []byte
		if err := rows.Scan(&page.After, &document); err != nil {
			return page, err
		}
		values, err := decodeHistoryValues(document)
		if err != nil || len(values) != len(page.Columns) {
			return page, ErrCorrupt
		}
		page.Rows = append(page.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	return page, tx.Commit()
}
