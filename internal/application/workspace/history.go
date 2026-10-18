package workspace

import (
	"context"

	"github.com/894x/llm-test-studio/internal/domain"
)

const HistoryPageSize = 25
const MaxHistoryRefresh = 100

type HistoryPage struct {
	SchemaVersion int          `json:"schema_version"`
	Runs          []RunSummary `json:"runs"`
	NextCursor    string       `json:"next_cursor"`
}

type HistoryRepository interface {
	AvailableReports(context.Context, []string) ([]string, error)
	ListRunHistory(context.Context, string) ([]RunProjection, string, error)
	ReadRunSummaries(context.Context, []string) ([]RunProjection, error)
}

type History struct{ repository HistoryRepository }

func NewHistory(repository HistoryRepository) History { return History{repository: repository} }

func (history History) Page(ctx context.Context, after string) (HistoryPage, error) {
	projections, cursor, err := history.repository.ListRunHistory(ctx, after)
	if err != nil {
		return HistoryPage{}, err
	}
	return historyPage(projections, cursor)
}

func (history History) Refresh(ctx context.Context, ids []string) (HistoryPage, error) {
	if len(ids) > MaxHistoryRefresh {
		return HistoryPage{}, ErrInconsistent
	}
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return HistoryPage{}, ErrInconsistent
		}
	}
	projections, err := history.repository.ReadRunSummaries(ctx, ids)
	if err != nil {
		return HistoryPage{}, err
	}
	return historyPage(projections, "")
}

func historyPage(projections []RunProjection, cursor string) (HistoryPage, error) {
	page := HistoryPage{SchemaVersion: 1, Runs: []RunSummary{}, NextCursor: cursor}
	for _, projection := range projections {
		summary, err := projection.Summary()
		if err != nil {
			return HistoryPage{}, err
		}
		page.Runs = append(page.Runs, summary)
	}
	return page, nil
}

func (history History) AvailableReports(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) > MaxHistoryRefresh {
		return nil, ErrInconsistent
	}
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return nil, ErrInconsistent
		}
	}
	return history.repository.AvailableReports(ctx, ids)
}
