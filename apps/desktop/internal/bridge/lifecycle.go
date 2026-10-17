package bridge

import (
	"context"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/reporting"
)

type desktopRequirements struct {
	workspace       bool
	catalog         bool
	catalogCommands bool
	reports         bool
	commands        bool
	comparisons     bool
	launcher        bool
	jobQuery        bool
	controls        bool
	controlRecords  bool
	remoteNodes     bool
}

type desktopLease struct {
	ctx             context.Context
	workspace       WorkspaceQuery
	catalog         CatalogQuery
	catalogCommands CatalogCommands
	reports         ReportingQuery
	commands        QuickTaskService
	comparisons     ComparisonService
	launcher        JobLauncher
	jobQuery        jobs.Query
	controls        JobController
	controlRecords  jobs.ControlRepository
	remoteNodes     *remotenodes.Manager
	release         func()
}

func (app *DesktopApp) onStartup(ctx context.Context) {
	if app == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	app.lifecycleMu.Lock()
	defer app.lifecycleMu.Unlock()
	app.mu.Lock()
	if app.started || app.stopping || app.stopped {
		app.mu.Unlock()
		return
	}
	app.starting = true
	app.mu.Unlock()

	lifecycleContext, cancel := context.WithCancel(ctx)
	if app.initialize == nil {
		app.mu.Lock()
		app.starting = false
		app.started = true
		app.ctx = lifecycleContext
		app.cancel = cancel
		app.startupErr = ErrWorkspaceUnavailable
		app.startupDone.Broadcast()
		app.mu.Unlock()
		return
	}
	dependencies, err := app.initialize(lifecycleContext)
	app.mu.Lock()
	app.starting = false
	app.started = true
	app.ctx = lifecycleContext
	app.cancel = cancel
	if err != nil {
		startupErr := fmt.Errorf("%w: %w", ErrDesktopStartup, err)
		app.startupErr = startupErr
		report := app.reportError
		app.startupDone.Broadcast()
		app.mu.Unlock()
		if report != nil {
			report(startupErr)
		}
		if dependencies.Close != nil {
			_ = dependencies.Close()
		}
		return
	}
	app.query = dependencies.Query
	app.catalog = dependencies.Catalog
	app.catalogCommands = dependencies.CatalogCommands
	app.reports = dependencies.Reports
	app.reportGeneration = dependencies.ReportGeneration
	app.commands = dependencies.Commands
	app.comparisons = dependencies.Comparisons
	app.launcher = dependencies.Launcher
	app.jobQuery = dependencies.JobQuery
	app.controls = dependencies.Controls
	app.controlRecords = dependencies.ControlRecords
	app.controlDelivery = dependencies.ControlDelivery
	app.remoteNodes = dependencies.RemoteNodes
	app.remoteSync = dependencies.RemoteSync
	app.jobs = dependencies.Jobs
	app.confirmations = dependencies.Confirmations
	app.close = dependencies.Close
	if dependencies.ReportGeneration != nil {
		unsubscribe := dependencies.ReportGeneration.SubscribeProgress(func(progress reporting.GenerationProgress) {
			if lifecycleContext.Err() == nil {
				app.emitDesktopEvent(lifecycleContext, "report-generation-progress", progress)
			}
		})
		app.close = func() error {
			unsubscribe()
			if dependencies.Close != nil {
				return dependencies.Close()
			}
			return nil
		}
	}
	app.startupDone.Broadcast()
	app.mu.Unlock()
}

func (app *DesktopApp) acquire(require desktopRequirements) (desktopLease, error) {
	if app == nil {
		return desktopLease{}, ErrDesktopNotStarted
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	for app.starting && !app.stopping && !app.stopped {
		app.startupDone.Wait()
	}
	if app.stopping || app.stopped {
		return desktopLease{}, ErrDesktopStopped
	}
	if !app.started {
		return desktopLease{}, ErrDesktopNotStarted
	}
	if app.startupErr != nil {
		return desktopLease{}, app.startupErr
	}
	if require.workspace && isNilInterface(app.query) {
		return desktopLease{}, ErrWorkspaceUnavailable
	}
	if require.catalog && isNilInterface(app.catalog) {
		return desktopLease{}, ErrCatalogUnavailable
	}
	if require.catalogCommands && isNilInterface(app.catalogCommands) {
		return desktopLease{}, ErrCatalogUnavailable
	}
	if require.reports && isNilInterface(app.reports) {
		return desktopLease{}, ErrReportingUnavailable
	}
	if require.commands && isNilInterface(app.commands) {
		return desktopLease{}, ErrQuickTaskServiceUnavailable
	}
	if require.comparisons && isNilInterface(app.comparisons) {
		return desktopLease{}, ErrComparisonUnavailable
	}
	if require.launcher && isNilInterface(app.launcher) {
		return desktopLease{}, ErrJobCommandsUnavailable
	}
	if require.jobQuery && isNilInterface(app.jobQuery) {
		return desktopLease{}, ErrJobQueryUnavailable
	}
	if require.controls && isNilInterface(app.controls) {
		return desktopLease{}, ErrJobCommandsUnavailable
	}
	if require.controlRecords && isNilInterface(app.controlRecords) {
		return desktopLease{}, ErrJobCommandsUnavailable
	}
	if require.remoteNodes && app.remoteNodes == nil {
		return desktopLease{}, ErrRemoteNodesUnavailable
	}
	app.active++
	released := false
	release := func() {
		app.mu.Lock()
		defer app.mu.Unlock()
		if released {
			return
		}
		released = true
		app.active--
		if app.active == 0 {
			app.drained.Broadcast()
		}
	}
	return desktopLease{
		ctx: app.ctx, workspace: app.query, catalog: app.catalog, catalogCommands: app.catalogCommands,
		reports: app.reports, commands: app.commands, release: release,
		comparisons: app.comparisons,
		launcher:    app.launcher, jobQuery: app.jobQuery, controls: app.controls, controlRecords: app.controlRecords,
		remoteNodes: app.remoteNodes,
	}, nil
}

func (app *DesktopApp) shutdown() error {
	if app == nil {
		return nil
	}
	app.lifecycleMu.Lock()
	defer app.lifecycleMu.Unlock()

	app.mu.Lock()
	if app.stopped {
		err := app.shutdownErr
		app.mu.Unlock()
		return err
	}
	app.stopping = true
	cancel := app.cancel
	app.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	app.mu.Lock()
	for app.active > 0 {
		app.drained.Wait()
	}
	closeResources := app.close
	selections := app.fileSelections
	app.fileSelections = nil
	app.query = nil
	app.catalog = nil
	app.catalogCommands = nil
	app.reports = nil
	app.commands = nil
	app.comparisons = nil
	app.launcher = nil
	app.jobQuery = nil
	app.controls = nil
	app.controlRecords = nil
	app.controlDelivery = nil
	app.jobs = nil
	app.confirmations = nil
	app.remoteSync = nil
	app.reportGeneration = nil
	app.remoteNodes = nil
	app.close = nil
	app.mu.Unlock()

	var shutdownErr error
	if selections != nil {
		shutdownErr = selections.Close()
	}
	if closeResources != nil {
		shutdownErr = errors.Join(shutdownErr, closeResources())
	}
	app.mu.Lock()
	app.shutdownErr = shutdownErr
	app.stopping = false
	app.stopped = true
	app.mu.Unlock()
	return shutdownErr
}
