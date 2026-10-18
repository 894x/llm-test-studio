package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

// ListRunHistory pages standalone runs. Managed children are fetched only for
// the jobs the user has loaded, through ReadRunSummaries.
func (repository *Repository) ListRunHistory(ctx context.Context, after string) ([]workspace.RunProjection, string, error) {
	cursor, err := quickhistory.ParseCursor(after)
	if err != nil || cursor.Kind != "" && cursor.Kind != "run" {
		return nil, "", quickhistory.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, runHistoryIDsQuery, cursor.Time, cursor.ID, workspace.HistoryPageSize+1)
	if err != nil {
		return nil, "", err
	}
	ids := []string{}
	keys := []string{}
	for rows.Next() {
		var id, stamp string
		if err := rows.Scan(&id, &stamp); err != nil {
			rows.Close()
			return nil, "", err
		}
		ids = append(ids, id)
		keys = append(keys, stamp+"|run|"+id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", err
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(ids) > workspace.HistoryPageSize {
		ids = ids[:workspace.HistoryPageSize]
		next = keys[workspace.HistoryPageSize-1]
	}
	projections, err := runSummaries(ctx, tx, ids)
	if err != nil {
		return nil, "", err
	}
	if len(projections) != len(ids) {
		return nil, "", storageCorrupt("run history window")
	}
	return projections, next, tx.Commit()
}

func (repository *Repository) ReadRunSummaries(ctx context.Context, ids []string) ([]workspace.RunProjection, error) {
	if len(ids) > workspace.MaxHistoryRefresh {
		return nil, workspace.ErrInconsistent
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	projections, err := runSummaries(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return projections, tx.Commit()
}

func runSummaries(ctx context.Context, tx *sql.Tx, ids []string) ([]workspace.RunProjection, error) {
	if len(ids) == 0 {
		return []workspace.RunProjection{}, nil
	}
	document, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	return readWorkspaceProjections(ctx, tx, quickHistoryRunQuery, string(document))
}

var runHistoryIDsQuery = `SELECT root.id, ` + historyTimeKey("root.created_at") + ` AS stamp
 FROM execution_runs root
 WHERE (` + historyTimeKey("root.created_at") + `) <= CASE WHEN ?1='' THEN '9999-12-31T23:59:59.999999999Z' ELSE ?1 END
 AND (?1='' OR (` + historyTimeKey("root.created_at") + `,root.id) < (?1,?2))
 AND NOT EXISTS (SELECT 1 FROM execution_job_runs WHERE run_id=root.id)
 AND NOT EXISTS (SELECT 1 FROM remote_job_runs WHERE run_id=root.id)
 ORDER BY stamp DESC, root.id DESC LIMIT ?3`

// AvailableReports checks local archive existence without decoding report bodies.
func (repository *Repository) AvailableReports(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) > workspace.MaxHistoryRefresh {
		return nil, workspace.ErrInconsistent
	}
	document, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := repository.db.QueryContext(ctx, `SELECT id FROM reports WHERE id IN (SELECT value FROM json_each(?1))
 UNION SELECT id FROM quick_performance_reports WHERE id IN (SELECT value FROM json_each(?1))`, string(document))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
