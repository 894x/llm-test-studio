package bridge

import (
	"context"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/catalog"
)

func (app *DesktopApp) GetCatalog(scope catalog.Scope) (catalog.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{catalog: true})
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.catalog.Snapshot(lease.ctx, scope)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop catalog: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) CreateModel(command catalog.CreateModelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create model", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreateModel(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdateModel(command catalog.UpdateModelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update model", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdateModel(ctx, command)
		return err
	})
}

func (app *DesktopApp) DeleteModel(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete model", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeleteModel(ctx, command)
	})
}

func (app *DesktopApp) CreateChannel(command catalog.CreateChannelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create channel", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreateChannel(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdateChannel(command catalog.UpdateChannelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update channel", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdateChannel(ctx, command)
		return err
	})
}

func (app *DesktopApp) SaveQuickTestTarget(command catalog.SaveQuickTestTargetCommand) (catalog.SavedQuickTestTarget, error) {
	var channelID string
	snapshot, err := app.executeCatalogCommand("save quick test target", func(ctx context.Context, commands CatalogCommands) error {
		targets, ok := commands.(interface {
			SaveQuickTestTarget(context.Context, catalog.SaveQuickTestTargetCommand) (catalog.MutationResult, error)
		})
		if !ok {
			return ErrCatalogUnavailable
		}
		result, err := targets.SaveQuickTestTarget(ctx, command)
		channelID = result.ID
		return err
	})
	return catalog.SavedQuickTestTarget{Catalog: snapshot, ChannelID: channelID}, err
}

func (app *DesktopApp) DeleteChannel(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete channel", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeleteChannel(ctx, command)
	})
}

func (app *DesktopApp) CreateChannelModel(command catalog.CreateChannelModelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create channel model", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreateChannelModel(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdateChannelModel(command catalog.UpdateChannelModelCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update channel model", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdateChannelModel(ctx, command)
		return err
	})
}

func (app *DesktopApp) DeleteChannelModel(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete channel model", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeleteChannelModel(ctx, command)
	})
}

func (app *DesktopApp) CreateTestCase(command catalog.CreateTestCaseCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create test case", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreateTestCase(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdateTestCase(command catalog.UpdateTestCaseCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update test case", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdateTestCase(ctx, command)
		return err
	})
}

func (app *DesktopApp) DeleteTestCase(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete test case", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeleteTestCase(ctx, command)
	})
}

func (app *DesktopApp) CreateSuite(command catalog.CreateSuiteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create suite", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreateSuite(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdateSuite(command catalog.UpdateSuiteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update suite", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdateSuite(ctx, command)
		return err
	})
}

func (app *DesktopApp) DeleteSuite(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete suite", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeleteSuite(ctx, command)
	})
}

func (app *DesktopApp) CreatePlan(command catalog.CreatePlanCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("create plan", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.CreatePlan(ctx, command)
		return err
	})
}

func (app *DesktopApp) UpdatePlan(command catalog.UpdatePlanCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("update plan", func(ctx context.Context, commands CatalogCommands) error {
		_, err := commands.UpdatePlan(ctx, command)
		return err
	})
}

func (app *DesktopApp) DeletePlan(command catalog.DeleteCommand) (catalog.Snapshot, error) {
	return app.executeCatalogCommand("delete plan", func(ctx context.Context, commands CatalogCommands) error {
		return commands.DeletePlan(ctx, command)
	})
}

// catalogCommandDiagnosticError preserves the catalog operation and its
// internal cause until the desktop diagnostic boundary. The binding still
// returns only a stable public error code, while the local log can name the
// failed operation and retain a redacted, actionable cause.
type catalogCommandDiagnosticError struct {
	operation string
	err       error
	committed bool
}

func (err catalogCommandDiagnosticError) Unwrap() error {
	return err.err
}

func (err catalogCommandDiagnosticError) Is(target error) bool {
	return err.committed && target == ErrCatalogSavedRefreshFailed
}

func (app *DesktopApp) executeCatalogCommand(name string, execute func(context.Context, CatalogCommands) error) (catalog.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{catalog: true, catalogCommands: true})
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	if err := execute(lease.ctx, lease.catalogCommands); err != nil {
		return catalog.Snapshot{}, app.safeBindingError(catalogCommandDiagnosticError{operation: name, err: err})
	}
	snapshot, err := lease.catalog.Snapshot(lease.ctx, catalog.ScopeAll)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(catalogCommandDiagnosticError{
			operation: name,
			err:       fmt.Errorf("refresh catalog snapshot: %w", err),
			committed: true,
		})
	}
	return snapshot, nil
}
