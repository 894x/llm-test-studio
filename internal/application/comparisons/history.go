package comparisons

import (
	"context"

	"github.com/894x/llm-test-studio/internal/domain"
)

type HistoryPage struct {
	SchemaVersion int       `json:"schema_version"`
	Comparisons   []Summary `json:"comparisons"`
	NextCursor    string    `json:"next_cursor"`
}

type HistoryRepository interface {
	ComparisonHistory(context.Context, string, []string) (HistoryPage, error)
}

type History struct {
	repository HistoryRepository
	refresher  *Refresher
}

func NewHistory(repository HistoryRepository, refresher *Refresher) *History {
	return &History{repository: repository, refresher: refresher}
}

func (service *History) ComparisonHistory(ctx context.Context, after string, ids []string) (HistoryPage, error) {
	page, err := service.repository.ComparisonHistory(ctx, after, ids)
	if err != nil {
		return page, err
	}
	for index, summary := range page.Comparisons {
		if summary.Status != string(domain.ComparisonRunning) {
			continue
		}
		// Active child runs cannot settle a group. Refresh only the bounded
		// terminal candidates; mirrors retain the remote owner's committed state.
		terminal := true
		for _, channel := range summary.Channels {
			switch channel.RunStatus {
			case domain.RunCompleted, domain.RunCancelled, domain.RunFailed:
			default:
				terminal = false
			}
		}
		if !terminal {
			continue
		}
		updated, err := service.refresher.Refresh(ctx, summary.ID)
		if err != nil {
			return HistoryPage{}, err
		}
		page.Comparisons[index].Status = string(updated.Status())
	}
	return page, nil
}
