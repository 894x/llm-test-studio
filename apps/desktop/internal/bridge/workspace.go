package bridge

import (
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/workspace"
)

func (app *DesktopApp) GetWorkspace() (workspace.Snapshot, error) {
	snapshot, err := app.getWorkspace()
	if err != nil {
		return workspace.Snapshot{}, app.safeBindingError(err)
	}
	return snapshot, nil
}

func (app *DesktopApp) getWorkspace() (workspace.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{workspace: true})
	if err != nil {
		return workspace.Snapshot{}, err
	}
	defer lease.release()
	snapshot, err := lease.workspace.Snapshot(lease.ctx)
	if err != nil {
		return workspace.Snapshot{}, fmt.Errorf("query desktop workspace: %w", err)
	}
	return snapshot, nil
}
