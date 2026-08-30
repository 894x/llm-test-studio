package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/workspace"
	"github.com/894x/llm-studio/internal/domain"
)

var (
	ErrDesktopNotStarted      = errors.New("desktop application has not started")
	ErrDesktopStartup         = errors.New("desktop application startup failed")
	ErrDesktopStopped         = errors.New("desktop application is shutting down or stopped")
	ErrWorkspaceUnavailable   = errors.New("workspace query is unavailable")
	ErrCatalogUnavailable     = errors.New("catalog query is unavailable")
	ErrReportingUnavailable   = errors.New("reporting query is unavailable")
	ErrRunCommandsUnavailable = errors.New("run commands are unavailable")
	ErrInvalidIdentifier      = errors.New("desktop command identifier is invalid")
)

const (
	desktopCodeNotStarted         = "desktop_not_started"
	desktopCodeStartupFailed      = "desktop_startup_failed"
	desktopCodeStopped            = "desktop_stopped"
	desktopCodeWorkspaceMissing   = "workspace_unavailable"
	desktopCodeCatalogMissing     = "catalog_unavailable"
	desktopCodeReportsMissing     = "reports_unavailable"
	desktopCodeCommandsMissing    = "run_commands_unavailable"
	desktopCodeInvalidIdentifier  = "invalid_identifier"
	desktopCodeOperationCancelled = "operation_cancelled"
	desktopCodeOperationFailed    = "operation_failed"
)

// WorkspaceQuery is the presentation-neutral Application query exposed to
// the desktop adapter. Implementations must not return credentials or raw
// provider configuration.
type WorkspaceQuery interface {
	Snapshot(context.Context) (workspace.Snapshot, error)
}

// CatalogQuery exposes the secret-free catalog projection used by the
// desktop presentation layer.
type CatalogQuery interface {
	Snapshot(context.Context) (catalog.Snapshot, error)
}

// ReportingQuery exposes bounded report summaries rather than complete
// evidence or provider payload documents.
type ReportingQuery interface {
	Snapshot(context.Context) (reporting.Snapshot, error)
}

// RunCommands is the Application command boundary used by the desktop
// adapter. A command mutates Core state; the adapter then obtains the
// authoritative state through WorkspaceQuery.
type RunCommands interface {
	StartRun(context.Context, string) error
	StopSending(context.Context, string) error
	CancelRun(context.Context, string) error
}

type desktopDependencies struct {
	query    WorkspaceQuery
	catalog  CatalogQuery
	reports  ReportingQuery
	commands RunCommands
	close    func() error
}

type desktopInitializer func(context.Context) (desktopDependencies, error)

// DesktopApp is the Wails binding. It owns only desktop lifecycle and
// delegation; business decisions remain in Application services.
type DesktopApp struct {
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	drained     *sync.Cond
	initialize  desktopInitializer
	started     bool
	stopping    bool
	stopped     bool
	active      int
	ctx         context.Context
	cancel      context.CancelFunc
	query       WorkspaceQuery
	catalog     CatalogQuery
	reports     ReportingQuery
	commands    RunCommands
	close       func() error
	startupErr  error
	shutdownErr error
	reportError func(error)
}

type desktopRequirements struct {
	workspace bool
	catalog   bool
	reports   bool
	commands  bool
}

type desktopLease struct {
	ctx       context.Context
	workspace WorkspaceQuery
	catalog   CatalogQuery
	reports   ReportingQuery
	commands  RunCommands
	release   func()
}

// DesktopBindingError is the complete error surface exposed to JavaScript.
// It intentionally does not unwrap internal errors, so provider, credential,
// filesystem, and database details cannot cross the Wails boundary.
type DesktopBindingError struct {
	Code string `json:"code"`
}

func (err DesktopBindingError) Error() string {
	if isDesktopBindingCode(err.Code) {
		return err.Code
	}
	return desktopCodeOperationFailed
}

func NewDesktopApp(query WorkspaceQuery, commands RunCommands) *DesktopApp {
	return newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{query: query, commands: commands}, nil
	})
}

func newDesktopApp(initialize desktopInitializer) *DesktopApp {
	app := &DesktopApp{initialize: initialize}
	app.drained = sync.NewCond(&app.mu)
	return app
}

func (app *DesktopApp) setErrorReporter(report func(error)) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.reportError = report
	app.mu.Unlock()
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
	app.mu.Unlock()

	lifecycleContext, cancel := context.WithCancel(ctx)
	if app.initialize == nil {
		app.mu.Lock()
		app.started = true
		app.ctx = lifecycleContext
		app.cancel = cancel
		app.startupErr = ErrWorkspaceUnavailable
		app.mu.Unlock()
		return
	}
	dependencies, err := app.initialize(lifecycleContext)
	app.mu.Lock()
	app.started = true
	app.ctx = lifecycleContext
	app.cancel = cancel
	if err != nil {
		app.startupErr = fmt.Errorf("%w: %w", ErrDesktopStartup, err)
		app.mu.Unlock()
		if dependencies.close != nil {
			_ = dependencies.close()
		}
		return
	}
	app.query = dependencies.query
	app.catalog = dependencies.catalog
	app.reports = dependencies.reports
	app.commands = dependencies.commands
	app.close = dependencies.close
	app.mu.Unlock()
}

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

func (app *DesktopApp) GetCatalog() (catalog.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{catalog: true})
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.catalog.Snapshot(lease.ctx)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop catalog: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) GetReports() (reporting.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.reports.Snapshot(lease.ctx)
	if err != nil {
		return reporting.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop reports: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) StartRun(planID string) (workspace.Snapshot, error) {
	snapshot, err := app.executeRunCommand("start run", planID, func(ctx context.Context, commands RunCommands) error {
		return commands.StartRun(ctx, planID)
	})
	if err != nil {
		return workspace.Snapshot{}, app.safeBindingError(err)
	}
	return snapshot, nil
}

func (app *DesktopApp) StopSending(runID string) (workspace.Snapshot, error) {
	snapshot, err := app.executeRunCommand("stop sending", runID, func(ctx context.Context, commands RunCommands) error {
		return commands.StopSending(ctx, runID)
	})
	if err != nil {
		return workspace.Snapshot{}, app.safeBindingError(err)
	}
	return snapshot, nil
}

func (app *DesktopApp) CancelRun(runID string) (workspace.Snapshot, error) {
	snapshot, err := app.executeRunCommand("cancel run", runID, func(ctx context.Context, commands RunCommands) error {
		return commands.CancelRun(ctx, runID)
	})
	if err != nil {
		return workspace.Snapshot{}, app.safeBindingError(err)
	}
	return snapshot, nil
}

func (app *DesktopApp) executeRunCommand(name, id string, execute func(context.Context, RunCommands) error) (workspace.Snapshot, error) {
	if !domain.IsUUID(id) {
		return workspace.Snapshot{}, fmt.Errorf("%w: %s", ErrInvalidIdentifier, name)
	}
	lease, err := app.acquire(desktopRequirements{workspace: true, commands: true})
	if err != nil {
		return workspace.Snapshot{}, err
	}
	defer lease.release()
	if err := execute(lease.ctx, lease.commands); err != nil {
		return workspace.Snapshot{}, fmt.Errorf("%s: %w", name, err)
	}
	snapshot, err := lease.workspace.Snapshot(lease.ctx)
	if err != nil {
		return workspace.Snapshot{}, fmt.Errorf("query workspace after %s: %w", name, err)
	}
	return snapshot, nil
}

func (app *DesktopApp) acquire(require desktopRequirements) (desktopLease, error) {
	if app == nil {
		return desktopLease{}, ErrDesktopNotStarted
	}
	app.mu.Lock()
	defer app.mu.Unlock()
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
	if require.reports && isNilInterface(app.reports) {
		return desktopLease{}, ErrReportingUnavailable
	}
	if require.commands && isNilInterface(app.commands) {
		return desktopLease{}, ErrRunCommandsUnavailable
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
		ctx: app.ctx, workspace: app.query, catalog: app.catalog,
		reports: app.reports, commands: app.commands, release: release,
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
	app.query = nil
	app.catalog = nil
	app.reports = nil
	app.commands = nil
	app.close = nil
	app.mu.Unlock()

	var shutdownErr error
	if closeResources != nil {
		shutdownErr = closeResources()
	}
	app.mu.Lock()
	app.shutdownErr = shutdownErr
	app.stopping = false
	app.stopped = true
	app.mu.Unlock()
	return shutdownErr
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (app *DesktopApp) safeBindingError(internal error) error {
	if internal == nil {
		return nil
	}
	if app != nil {
		app.mu.Lock()
		report := app.reportError
		app.mu.Unlock()
		if report != nil {
			report(internal)
		}
	}

	switch {
	case errors.Is(internal, ErrDesktopNotStarted):
		return DesktopBindingError{Code: desktopCodeNotStarted}
	case errors.Is(internal, ErrDesktopStartup):
		return DesktopBindingError{Code: desktopCodeStartupFailed}
	case errors.Is(internal, ErrDesktopStopped):
		return DesktopBindingError{Code: desktopCodeStopped}
	case errors.Is(internal, ErrWorkspaceUnavailable):
		return DesktopBindingError{Code: desktopCodeWorkspaceMissing}
	case errors.Is(internal, ErrCatalogUnavailable):
		return DesktopBindingError{Code: desktopCodeCatalogMissing}
	case errors.Is(internal, ErrReportingUnavailable):
		return DesktopBindingError{Code: desktopCodeReportsMissing}
	case errors.Is(internal, ErrRunCommandsUnavailable):
		return DesktopBindingError{Code: desktopCodeCommandsMissing}
	case errors.Is(internal, ErrInvalidIdentifier):
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	case errors.Is(internal, context.Canceled), errors.Is(internal, context.DeadlineExceeded):
		return DesktopBindingError{Code: desktopCodeOperationCancelled}
	default:
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
}

func isDesktopBindingCode(code string) bool {
	switch code {
	case desktopCodeNotStarted,
		desktopCodeStartupFailed,
		desktopCodeStopped,
		desktopCodeWorkspaceMissing,
		desktopCodeCatalogMissing,
		desktopCodeReportsMissing,
		desktopCodeCommandsMissing,
		desktopCodeInvalidIdentifier,
		desktopCodeOperationCancelled,
		desktopCodeOperationFailed:
		return true
	default:
		return false
	}
}
