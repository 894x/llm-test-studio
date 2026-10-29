package bridge

import (
	"errors"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
)

var ErrHistoryRecycleUnavailable = errors.New("history recycle service unavailable")

const (
	desktopCodeHistoryRecycleInvalid     = "history_recycle_invalid"
	desktopCodeHistoryRecycleNotFound    = "history_recycle_not_found"
	desktopCodeHistoryRecycleActive      = "history_recycle_active"
	desktopCodeHistoryRecycleUnconfirmed = "history_recycle_unconfirmed"
	desktopCodeHistoryRecycleUnavailable = "history_recycle_unavailable"
)

func (app *DesktopApp) GetRecycledHistory(after string) (historyrecycle.Page, error) {
	lease, err := app.acquire(desktopRequirements{historyRecycle: true})
	if err != nil {
		return historyrecycle.Page{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.historyRecycle.Page(lease.ctx, after)
	if err != nil {
		return historyrecycle.Page{}, app.historyRecycleError(err)
	}
	return page, nil
}

func (app *DesktopApp) PreviewHistoryRecycle(target historyrecycle.Target) (historyrecycle.Preview, error) {
	lease, err := app.acquire(desktopRequirements{historyRecycle: true})
	if err != nil {
		return historyrecycle.Preview{}, app.safeBindingError(err)
	}
	defer lease.release()
	preview, err := lease.historyRecycle.Preview(lease.ctx, target)
	if err != nil {
		return historyrecycle.Preview{}, app.historyRecycleError(err)
	}
	return preview, nil
}

func (app *DesktopApp) RecycleHistory(target historyrecycle.Target) (historyrecycle.Entry, error) {
	lease, err := app.acquire(desktopRequirements{historyRecycle: true})
	if err != nil {
		return historyrecycle.Entry{}, app.safeBindingError(err)
	}
	defer lease.release()
	entry, err := lease.historyRecycle.Recycle(lease.ctx, target)
	if err != nil {
		return historyrecycle.Entry{}, app.historyRecycleError(err)
	}
	app.emitDesktopEvent(lease.ctx, "history-visibility-changed")
	return entry, nil
}

func (app *DesktopApp) RestoreHistory(target historyrecycle.Target) (historyrecycle.Entry, error) {
	lease, err := app.acquire(desktopRequirements{historyRecycle: true})
	if err != nil {
		return historyrecycle.Entry{}, app.safeBindingError(err)
	}
	defer lease.release()
	entry, err := lease.historyRecycle.Restore(lease.ctx, target)
	if err != nil {
		return historyrecycle.Entry{}, app.historyRecycleError(err)
	}
	app.emitDesktopEvent(lease.ctx, "history-visibility-changed")
	return entry, nil
}

func (app *DesktopApp) historyRecycleError(err error) error {
	invalidTarget := errors.Is(err, historyrecycle.ErrInvalid) || errors.Is(err, historyrecycle.ErrNotFound)
	blockedExecution := errors.Is(err, historyrecycle.ErrActive) || errors.Is(err, historyrecycle.ErrUnconfirmed)
	if invalidTarget || blockedExecution {
		return app.safeBindingError(err)
	}
	return app.safeBindingError(errors.Join(ErrHistoryRecycleUnavailable, err))
}
