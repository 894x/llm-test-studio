package bridge

import "github.com/894x/llm-test-studio/internal/application/quickhistory"

func (app *DesktopApp) GetQuickTestHistory(after string) (quickhistory.Page, error) {
	if _, err := quickhistory.ParseCursor(after); err != nil {
		return quickhistory.Page{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{quickHistory: true})
	if err != nil {
		return quickhistory.Page{}, app.safeBindingError(err)
	}
	defer lease.release()
	page, err := lease.quickHistory.Page(lease.ctx, after)
	if err != nil {
		return quickhistory.Page{}, app.safeBindingError(err)
	}
	return page, nil
}
