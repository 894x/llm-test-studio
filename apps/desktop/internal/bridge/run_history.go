package bridge

import (
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

func (app *DesktopApp) GetRunHistory(after string) (workspace.HistoryPage, error) {
	if cursor, err := quickhistory.ParseCursor(after); err != nil || cursor.Kind != "" && cursor.Kind != "run" {
		return workspace.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{runHistory: true})
	if err != nil {
		return workspace.HistoryPage{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.runHistory.Page(lease.ctx, after)
	return page, app.safeBindingError(err)
}

func (app *DesktopApp) GetRunSummaries(document string) (workspace.HistoryPage, error) {
	ids := []string{}
	if len(document) > 4096 || json.Unmarshal([]byte(document), &ids) != nil || len(ids) > workspace.MaxHistoryRefresh {
		return workspace.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return workspace.HistoryPage{}, app.safeBindingError(ErrInvalidIdentifier)
		}
	}
	lease, err := app.acquire(desktopRequirements{runHistory: true})
	if err != nil {
		return workspace.HistoryPage{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.runHistory.Refresh(lease.ctx, ids)
	return page, app.safeBindingError(err)
}

func (app *DesktopApp) GetAvailableReports(document string) ([]string, error) {
	ids := []string{}
	if len(document) > 4096 || json.Unmarshal([]byte(document), &ids) != nil || len(ids) > workspace.MaxHistoryRefresh {
		return nil, app.safeBindingError(ErrInvalidIdentifier)
	}
	for _, id := range ids {
		if !domain.IsUUID(id) {
			return nil, app.safeBindingError(ErrInvalidIdentifier)
		}
	}
	lease, err := app.acquire(desktopRequirements{runHistory: true})
	if err != nil {
		return nil, app.safeBindingError(err)
	}
	defer lease.release()
	result, err := lease.runHistory.AvailableReports(lease.ctx, ids)
	return result, app.safeBindingError(err)
}
