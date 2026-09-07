package main

import (
	"context"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

type QuickTaskHistoryQuery interface {
	QuickTask(context.Context, string) (runs.QuickTaskDetail, error)
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
	query, ok := lease.commands.(QuickTaskHistoryQuery)
	if !ok {
		return runs.QuickTaskDetail{}, app.safeBindingError(ErrQuickTestUnavailable)
	}
	detail, err := query.QuickTask(lease.ctx, runID)
	if err != nil {
		return runs.QuickTaskDetail{}, app.safeBindingError(fmt.Errorf("query quick task: %w", err))
	}
	return detail, nil
}

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
