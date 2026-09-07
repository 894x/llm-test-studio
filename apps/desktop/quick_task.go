package main

import (
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

// StartQuickTask returns the accepted Run identity. Reading progress and history
// is a separate query, so a refresh failure cannot hide a successful start.
func (app *DesktopApp) StartQuickTask(command runs.QuickTaskCommand) (string, error) {
	if !domain.IsUUID(command.SuiteID) || (command.ChannelID != "" && !domain.IsUUID(command.ChannelID)) {
		return "", app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{commands: true})
	if err != nil {
		return "", app.safeBindingError(err)
	}
	defer lease.release()
	id, err := lease.commands.StartQuickTask(lease.ctx, command)
	if err != nil {
		return "", app.safeBindingError(fmt.Errorf("start quick task: %w", err))
	}
	return id, nil
}
