package sqlite

import (
	"context"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
)

func (repository *Repository) IsLocalComparison(ctx context.Context, id string) (bool, error) {
	var remote bool
	if err := repository.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE comparison_id = ?)`, id,
	).Scan(&remote); err != nil {
		return false, fmt.Errorf("read comparison owner: %w", err)
	}
	return !remote, nil
}

func requireLocalRun(ctx context.Context, queryer rowQueryer, id string) error {
	var remote bool
	if err := queryer.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM remote_job_runs WHERE run_id = ?)`, id,
	).Scan(&remote); err != nil {
		return fmt.Errorf("read run owner: %w", err)
	}
	if remote {
		return fmt.Errorf("%w: %w: run belongs to a remote node", ErrConflict, remoteresults.ErrConflict)
	}
	return nil
}

func requireLocalComparison(ctx context.Context, queryer rowQueryer, id string) error {
	var remote bool
	if err := queryer.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE comparison_id = ?)`, id,
	).Scan(&remote); err != nil {
		return fmt.Errorf("read comparison owner: %w", err)
	}
	if remote {
		return fmt.Errorf("%w: %w: comparison belongs to a remote node", ErrConflict, remoteresults.ErrConflict)
	}
	return nil
}

func requireLocalPerformanceReport(ctx context.Context, queryer rowQueryer, id string) error {
	var remote bool
	if err := queryer.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM remote_job_mirrors WHERE performance_report_id = ?)`, id,
	).Scan(&remote); err != nil {
		return fmt.Errorf("read performance report owner: %w", err)
	}
	if remote {
		return fmt.Errorf("%w: %w: performance report belongs to a remote node", ErrConflict, remoteresults.ErrConflict)
	}
	return nil
}
