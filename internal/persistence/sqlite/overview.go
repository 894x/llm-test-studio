package sqlite

import (
	"context"
	"database/sql"
	"github.com/894x/llm-test-studio/internal/application/overview"
)

func (repository *Repository) OverviewActivity(ctx context.Context) (overview.Activity, error) {
	result := overview.Activity{}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	countQuery := `SELECT (SELECT count(*) FROM execution_runs WHERE ` +
		repository.historyVisibleSQL("run", "execution_runs.id") + `),
	 (SELECT count(*) FROM reports WHERE ` + repository.reportVisibleSQL("reports.id", "reports.run_id") +
		`)+(SELECT count(*) FROM quick_performance_reports WHERE ` +
		repository.historyVisibleSQL("report", "quick_performance_reports.id") + `)`
	if err := tx.QueryRowContext(ctx, countQuery).Scan(&result.RunCount, &result.ReportCount); err != nil {
		return result, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT root.id FROM execution_runs root
	 JOIN execution_run_revisions revision ON revision.run_id=root.id AND revision.revision=root.current_revision
	 WHERE revision.status IN ('queued','starting','running','draining')
	 AND `+repository.historyVisibleSQL("run", "root.id")+`
	 ORDER BY `+historyTimeKey("root.created_at")+` DESC,root.id DESC LIMIT 1`).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return result, err
	}
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	}
	result.Active, err = repository.runSummaries(ctx, tx, ids)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
