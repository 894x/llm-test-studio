package bridge

import (
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (app *DesktopApp) GetComparisons() (comparisons.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{comparisons: true})
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.comparisons.Snapshot(lease.ctx)
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop comparisons: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) GetComparisonHistory(after, document string) (comparisons.HistoryPage, error) {
	ids := []string{}
	if len(document) > 4096 || json.Unmarshal([]byte(document), &ids) != nil || len(ids) > 100 {
		return comparisons.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	if cursor, err := quickhistory.ParseCursor(after); err != nil || cursor.Kind != "" && cursor.Kind != "run" {
		return comparisons.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return comparisons.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
		}
	}
	lease, err := app.acquire(desktopRequirements{comparisonHistory: true})
	if err != nil {
		return comparisons.HistoryPage{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.comparisonHistory.ComparisonHistory(lease.ctx, after, ids)
	return page, app.safeBindingError(err)
}
