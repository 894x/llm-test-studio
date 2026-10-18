package bridge

import "github.com/894x/llm-test-studio/internal/application/overview"

func (app *DesktopApp) GetOverview() (overview.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{overview: true})
	if err != nil {
		return overview.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	result, err := lease.overview.Snapshot(lease.ctx)
	return result, app.safeBindingError(err)
}
