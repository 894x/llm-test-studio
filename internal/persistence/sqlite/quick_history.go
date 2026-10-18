package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

// ListQuickHistory selects a bounded mixed ID window before projecting results.
// It never loads report bodies, evidence payloads, or authored catalog files.
func (repository *Repository) ListQuickHistory(ctx context.Context, after string) (quickhistory.Projection, error) {
	cursor, err := quickhistory.ParseCursor(after)
	if err != nil {
		return quickhistory.Projection{}, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return quickhistory.Projection{}, err
	}
	defer tx.Rollback()
	ids, err := quickHistoryIDs(ctx, tx, cursor)
	if err != nil {
		return quickhistory.Projection{}, err
	}
	page := quickhistory.Projection{Runs: []workspace.RunProjection{}, Performance: []quicktest.PerformanceArchiveSummary{}}
	if len(ids) > quickhistory.PageSize {
		ids = ids[:quickhistory.PageSize]
		last := ids[len(ids)-1]
		page.NextCursor = last.Time + "|" + last.Kind + "|" + last.ID
	}
	runIDs, reportIDs := []string{}, []string{}
	for _, id := range ids {
		if id.Kind == "run" {
			runIDs = append(runIDs, id.ID)
		} else {
			reportIDs = append(reportIDs, id.ID)
		}
	}
	if len(runIDs) > 0 {
		document, err := json.Marshal(runIDs)
		if err != nil {
			return quickhistory.Projection{}, err
		}
		page.Runs, err = readWorkspaceProjections(ctx, tx, quickHistoryRunQuery, string(document))
		if err != nil {
			return quickhistory.Projection{}, err
		}
		if len(page.Runs) != len(runIDs) {
			return quickhistory.Projection{}, storageCorrupt("quick history run window")
		}
	}
	if len(reportIDs) > 0 {
		page.Performance, err = quickHistoryPerformance(ctx, tx, reportIDs)
		if err != nil {
			return quickhistory.Projection{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return quickhistory.Projection{}, err
	}
	return page, nil
}

func quickHistoryIDs(ctx context.Context, tx *sql.Tx, cursor quickhistory.Cursor) ([]quickhistory.Cursor, error) {
	beforeNanos := int64(math.MaxInt64)
	if cursor.Time != "" {
		stamp, err := time.Parse(quickhistory.TimeLayout, cursor.Time)
		if err != nil {
			return nil, err
		}
		switch {
		case stamp.Before(time.Unix(0, math.MinInt64)):
			beforeNanos = math.MinInt64
		case stamp.After(time.Unix(0, math.MaxInt64)):
			beforeNanos = math.MaxInt64
		default:
			beforeNanos = stamp.UnixNano()
		}
	}
	rows, err := tx.QueryContext(ctx, quickHistoryIDQuery, cursor.Time, cursor.Kind, cursor.ID, quickhistory.PageSize+1, beforeNanos)
	if err != nil {
		return nil, reportProjectionReadError(ctx, "quick history IDs", err)
	}
	defer rows.Close()
	ids := []quickhistory.Cursor{}
	for rows.Next() {
		var id quickhistory.Cursor
		if err := rows.Scan(&id.Time, &id.Kind, &id.ID); err != nil {
			return nil, err
		}
		if _, err := quickhistory.ParseCursor(id.Time + "|" + id.Kind + "|" + id.ID); err != nil {
			return nil, storageCorrupt("quick history cursor fields")
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func quickHistoryPerformance(ctx context.Context, tx *sql.Tx, ids []string) ([]quicktest.PerformanceArchiveSummary, error) {
	document, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, generated_at, generated_at_unix_nano, success,
		model_id, base_url, phase, completed, failed FROM quick_performance_reports
		WHERE id IN (SELECT value FROM json_each(?))`, string(document))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := []quicktest.PerformanceArchiveSummary{}
	for rows.Next() {
		var summary quicktest.PerformanceArchiveSummary
		var nanos int64
		if err := rows.Scan(
			&summary.ReportID, &summary.GeneratedAt, &nanos, &summary.Success,
			&summary.ModelID, &summary.BaseURL, &summary.Phase, &summary.Completed, &summary.Failed,
		); err != nil {
			return nil, err
		}
		stamp, err := quicktest.ValidatePerformanceArchiveSummary(summary)
		if err != nil || stamp.UnixNano() != nanos {
			return nil, fmt.Errorf("%w: quick history performance summary", ErrCorrupt)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(summaries) != len(ids) {
		return nil, storageCorrupt("quick history report window")
	}
	return summaries, nil
}

// Only trusted SQL fragments are composed here; caller values remain parameters.
var quickHistoryRunQuery = strings.Replace(workspaceProjectionQuery,
	"revision.revision = root.current_revision\n",
	"revision.revision = root.current_revision\n WHERE root.id IN (SELECT value FROM json_each(?4))\n", 1)

func historyTimeKey(column string) string {
	return "substr(" + column + ",1,19) || '.' || substr(CASE WHEN substr(" + column +
		",20,1)='.' THEN substr(" + column + ",21,length(" + column +
		")-21) ELSE '' END || '000000000',1,9) || 'Z'"
}

var quickHistoryIDQuery = `WITH run_candidates AS MATERIALIZED (
 SELECT ` + historyTimeKey("root.created_at") + ` AS stamp, 'run' AS kind, root.id AS id
 FROM execution_runs AS root
 CROSS JOIN execution_run_revisions AS revision
 WHERE revision.run_id=root.id AND revision.revision=root.current_revision
 AND json_type(revision.snapshot_json,'$.quick_task')='object'
 AND (?1='' OR (` + historyTimeKey("root.created_at") + `,'run',root.id) < (?1,?2,?3))
 ORDER BY stamp DESC, id DESC LIMIT ?4
), performance_candidates AS MATERIALIZED (
 SELECT id, generated_at FROM quick_performance_reports
 WHERE generated_at_unix_nano <= ?5
 AND (?1='' OR generated_at_unix_nano < ?5
      OR (` + historyTimeKey("generated_at") + `,'performance',id) < (?1,?2,?3))
 ORDER BY generated_at_unix_nano DESC, id DESC LIMIT ?4
), candidates AS (
 SELECT stamp,kind,id FROM run_candidates
 UNION ALL
 SELECT ` + historyTimeKey("generated_at") + ` AS stamp, 'performance' AS kind, id FROM performance_candidates
)
SELECT stamp,kind,id FROM candidates
ORDER BY stamp DESC,kind DESC,id DESC LIMIT ?4`
