package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/domain"
)

// GetReportWithResults reads one consistent sealed report and its request
// results. Reuse the report's validated owner and evidence rather than opening
// a second read and loading the same documents through ListResults again.
func (repository *Repository) GetReportWithResults(
	ctx context.Context,
	id string,
) (domain.Report, []domain.Result, error) {
	if err := ctx.Err(); err != nil {
		return domain.Report{}, nil, err
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.Report{}, nil, err
	}
	defer tx.Rollback()
	report, run, err := readStoredReport(ctx, tx, id)
	if err != nil {
		return domain.Report{}, nil, err
	}
	results, err := readReportRequestResults(
		ctx,
		tx,
		report,
		run.Snapshot(),
	)
	if err != nil {
		return domain.Report{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Report{}, nil, err
	}
	return report, results, nil
}

func readStoredReport(ctx context.Context, queryer relationQueryer, id string) (domain.Report, domain.Run, error) {
	var row storedReportRow
	err := row.scan(queryer.QueryRowContext(ctx, `
		SELECT id, schema_version, run_id, generated_at, document_json FROM reports WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Report{}, domain.Run{}, fmt.Errorf("%w: report", ErrNotFound)
	}
	if err != nil {
		return domain.Report{}, domain.Run{}, fmt.Errorf("get report: %w", err)
	}
	report, err := row.decode()
	if err != nil {
		return domain.Report{}, domain.Run{}, err
	}
	run, err := readReportStorage(ctx, queryer, report)
	if err != nil {
		return domain.Report{}, domain.Run{}, err
	}
	return report, run, nil
}

func readReportRequestResults(
	ctx context.Context,
	queryer relationQueryer,
	report domain.Report,
	snapshot domain.RunSnapshot,
) ([]domain.Result, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
		FROM case_results WHERE run_id = ? AND (request_id IS NOT NULL OR case_id IS NULL) ORDER BY created_at, id
	`, report.RunID)
	if err != nil {
		return nil, fmt.Errorf("query report requests: %w", err)
	}
	defer rows.Close()
	stored := []storedResultRow{}
	for rows.Next() {
		var row storedResultRow
		if err := row.scan(rows); err != nil {
			return nil, fmt.Errorf("scan report request: %w", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate report requests: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close report requests: %w", err)
	}
	evidence := make(map[string]domain.Evidence, len(report.Evidence))
	for _, item := range report.Evidence {
		evidence[item.ID] = item
	}
	// Summary rows were decoded and compared to the report already. Validate
	// their references using the same owner and evidence without decoding again.
	for _, result := range report.CaseResults {
		if err := validateStoredResultAgainstSnapshot(
			ctx,
			queryer,
			result,
			snapshot,
			evidence,
		); err != nil {
			return nil, err
		}
	}
	results := make([]domain.Result, 0, len(stored))
	for _, row := range stored {
		result, err := row.decode(report.RunID)
		if err != nil {
			return nil, err
		}
		if err := validateStoredResultAgainstSnapshot(
			ctx,
			queryer,
			result,
			snapshot,
			evidence,
		); err != nil {
			return nil, err
		}
		if result.RequestID != "" && len(result.Performance) == 0 {
			results = append(results, result)
		}
	}
	return results, nil
}
