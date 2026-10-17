package bridge

import (
	"context"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

type QuickTaskHistoryQuery interface {
	QuickTask(context.Context, string) (runs.QuickTaskDetail, error)
}

type QuickTaskCredentialCommands interface {
	RememberQuickTaskCredential(context.Context, runs.RememberQuickTaskCredentialCommand) error
	ForgetQuickTaskCredential(context.Context, string) error
}

func (app *DesktopApp) RememberQuickTaskCredential(command runs.RememberQuickTaskCredentialCommand) error {
	if !domain.IsUUID(command.RunID) {
		return app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{commands: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.commands.RememberQuickTaskCredential(lease.ctx, command))
}

func (app *DesktopApp) ForgetQuickTaskCredential(runID string) error {
	if !domain.IsUUID(runID) {
		return app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{commands: true})
	if err != nil {
		return app.safeBindingError(err)
	}
	defer lease.release()
	return app.safeBindingError(lease.commands.ForgetQuickTaskCredential(lease.ctx, runID))
}

func (app *DesktopApp) GetQuickTask(runID string) (runs.QuickTaskDetail, error) {
	if !domain.IsUUID(runID) {
		return runs.QuickTaskDetail{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{commands: true})
	if err != nil {
		return runs.QuickTaskDetail{}, app.safeBindingError(err)
	}
	defer lease.release()
	detail, err := lease.commands.QuickTask(lease.ctx, runID)
	if err != nil {
		return runs.QuickTaskDetail{}, app.safeBindingError(fmt.Errorf("query quick task: %w", err))
	}
	return detail, nil
}
