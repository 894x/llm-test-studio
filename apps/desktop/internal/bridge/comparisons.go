package bridge

import (
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
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
