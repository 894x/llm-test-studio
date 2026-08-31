package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/comparisons"
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
	ErrComparisonUnavailable  = errors.New("comparison service is unavailable")
	ErrDiagnosticsUnavailable = errors.New("desktop diagnostics are unavailable")
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
	desktopCodeComparisonMissing  = "comparison_unavailable"
	desktopCodeDiagnosticsMissing = "diagnostics_unavailable"
	desktopCodeInvalidIdentifier  = "invalid_identifier"
	desktopCodeOperationCancelled = "operation_cancelled"
	desktopCodeOperationFailed    = "operation_failed"
	desktopCodeCatalogInvalid     = "catalog_invalid"
	desktopCodeCatalogConflict    = "catalog_revision_conflict"
	desktopCodeCatalogNotFound    = "catalog_not_found"
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

// CatalogCommands mutates the six versioned catalog entity kinds. The
// desktop adapter refreshes CatalogQuery after every successful command.
type CatalogCommands interface {
	CreateModel(context.Context, catalog.CreateModelCommand) (catalog.MutationResult, error)
	UpdateModel(context.Context, catalog.UpdateModelCommand) (catalog.MutationResult, error)
	DeleteModel(context.Context, catalog.DeleteCommand) error
	CreateChannel(context.Context, catalog.CreateChannelCommand) (catalog.MutationResult, error)
	UpdateChannel(context.Context, catalog.UpdateChannelCommand) (catalog.MutationResult, error)
	DeleteChannel(context.Context, catalog.DeleteCommand) error
	CreateChannelModel(context.Context, catalog.CreateChannelModelCommand) (catalog.MutationResult, error)
	UpdateChannelModel(context.Context, catalog.UpdateChannelModelCommand) (catalog.MutationResult, error)
	DeleteChannelModel(context.Context, catalog.DeleteCommand) error
	CreateTestCase(context.Context, catalog.CreateTestCaseCommand) (catalog.MutationResult, error)
	UpdateTestCase(context.Context, catalog.UpdateTestCaseCommand) (catalog.MutationResult, error)
	DeleteTestCase(context.Context, catalog.DeleteCommand) error
	CreateSuite(context.Context, catalog.CreateSuiteCommand) (catalog.MutationResult, error)
	UpdateSuite(context.Context, catalog.UpdateSuiteCommand) (catalog.MutationResult, error)
	DeleteSuite(context.Context, catalog.DeleteCommand) error
	CreatePlan(context.Context, catalog.CreatePlanCommand) (catalog.MutationResult, error)
	UpdatePlan(context.Context, catalog.UpdatePlanCommand) (catalog.MutationResult, error)
	DeletePlan(context.Context, catalog.DeleteCommand) error
}

// ReportingQuery exposes bounded report summaries rather than complete
// evidence or provider payload documents.
type ReportingQuery interface {
	Snapshot(context.Context) (reporting.Snapshot, error)
}

type ReportDocumentQuery interface {
	ReportingQuery
	Detail(context.Context, string) (reporting.Detail, error)
	Export(context.Context, string, reporting.ExportFormat) (reporting.ExportedDocument, error)
}

// RunCommands is the Application command boundary used by the desktop
// adapter. A command mutates Core state; the adapter then obtains the
// authoritative state through WorkspaceQuery.
type RunCommands interface {
	StartRun(context.Context, string) error
	StopSending(context.Context, string) error
	CancelRun(context.Context, string) error
}

type ComparisonService interface {
	Start(context.Context, comparisons.StartCommand) (string, error)
	Snapshot(context.Context) (comparisons.Snapshot, error)
}

type desktopDependencies struct {
	query           WorkspaceQuery
	catalog         CatalogQuery
	catalogCommands CatalogCommands
	reports         ReportingQuery
	commands        RunCommands
	comparisons     ComparisonService
	close           func() error
}

type desktopInitializer func(context.Context) (desktopDependencies, error)

// DesktopApp is the Wails binding. It owns only desktop lifecycle and
// delegation; business decisions remain in Application services.
type DesktopApp struct {
	lifecycleMu     sync.Mutex
	mu              sync.Mutex
	drained         *sync.Cond
	startupDone     *sync.Cond
	initialize      desktopInitializer
	starting        bool
	started         bool
	stopping        bool
	stopped         bool
	active          int
	ctx             context.Context
	cancel          context.CancelFunc
	query           WorkspaceQuery
	catalog         CatalogQuery
	catalogCommands CatalogCommands
	reports         ReportingQuery
	commands        RunCommands
	comparisons     ComparisonService
	close           func() error
	startupErr      error
	shutdownErr     error
	reportError     func(error)
	diagnostics     DesktopDiagnosticsSnapshot
	openDiagnostics func() error
}

// DesktopDiagnosticsSnapshot is an allow-listed operator view. The filesystem
// path stays inside the Go desktop adapter; React can only request that the
// application open the owned directory.
type DesktopDiagnosticsSnapshot struct {
	SchemaVersion      int    `json:"schema_version"`
	Available          bool   `json:"available"`
	Format             string `json:"format"`
	MaxFileBytes       int64  `json:"max_file_bytes"`
	BackupFiles        int    `json:"backup_files"`
	RunCorrelation     bool   `json:"run_correlation"`
	RequestCorrelation bool   `json:"request_correlation"`
}

type desktopRequirements struct {
	workspace       bool
	catalog         bool
	catalogCommands bool
	reports         bool
	commands        bool
	comparisons     bool
}

type desktopLease struct {
	ctx             context.Context
	workspace       WorkspaceQuery
	catalog         CatalogQuery
	catalogCommands CatalogCommands
	reports         ReportingQuery
	commands        RunCommands
	comparisons     ComparisonService
	release         func()
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
	app.startupDone = sync.NewCond(&app.mu)
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

func (app *DesktopApp) setDiagnostics(snapshot DesktopDiagnosticsSnapshot, open func() error) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.diagnostics = snapshot
	app.openDiagnostics = open
	app.mu.Unlock()
}

func (app *DesktopApp) GetDiagnostics() DesktopDiagnosticsSnapshot {
	if app == nil {
		return DesktopDiagnosticsSnapshot{}
	}
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.diagnostics
}

func (app *DesktopApp) OpenDiagnosticsDirectory() error {
	if app == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	app.mu.Lock()
	available := app.diagnostics.Available
	open := app.openDiagnostics
	app.mu.Unlock()
	if !available || open == nil {
		return app.safeBindingError(ErrDiagnosticsUnavailable)
	}
	if err := open(); err != nil {
		return app.safeBindingError(fmt.Errorf("open diagnostics directory: %w", err))
	}
	return nil
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
		if dependencies.close != nil {
			_ = dependencies.close()
		}
		return
	}
	app.query = dependencies.query
	app.catalog = dependencies.catalog
	app.catalogCommands = dependencies.catalogCommands
	app.reports = dependencies.reports
	app.commands = dependencies.commands
	app.comparisons = dependencies.comparisons
	app.close = dependencies.close
	app.startupDone.Broadcast()
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

func (app *DesktopApp) executeCatalogCommand(name string, execute func(context.Context, CatalogCommands) error) (catalog.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{catalog: true, catalogCommands: true})
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	if err := execute(lease.ctx, lease.catalogCommands); err != nil {
		return catalog.Snapshot{}, app.safeBindingError(fmt.Errorf("%s: %w", name, err))
	}
	snapshot, err := lease.catalog.Snapshot(lease.ctx)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(fmt.Errorf("query catalog after %s: %w", name, err))
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

func (app *DesktopApp) GetReportDetail(reportID string) (reporting.Detail, error) {
	if !domain.IsUUID(reportID) {
		return reporting.Detail{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.Detail{}, app.safeBindingError(err)
	}
	defer lease.release()
	documents, ok := lease.reports.(ReportDocumentQuery)
	if !ok || isNilInterface(documents) {
		return reporting.Detail{}, app.safeBindingError(ErrReportingUnavailable)
	}
	detail, err := documents.Detail(lease.ctx, reportID)
	if err != nil {
		return reporting.Detail{}, app.safeBindingError(fmt.Errorf("query desktop report detail: %w", err))
	}
	return detail, nil
}

func (app *DesktopApp) ExportReport(reportID, format string) (reporting.ExportedDocument, error) {
	if !domain.IsUUID(reportID) {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	exportFormat := reporting.ExportFormat(format)
	switch exportFormat {
	case reporting.ExportJSON, reporting.ExportHTML, reporting.ExportPNG, reporting.ExportPDF:
	default:
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	lease, err := app.acquire(desktopRequirements{reports: true})
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(err)
	}
	defer lease.release()
	documents, ok := lease.reports.(ReportDocumentQuery)
	if !ok || isNilInterface(documents) {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrReportingUnavailable)
	}
	exported, err := documents.Export(lease.ctx, reportID, exportFormat)
	if err != nil {
		return reporting.ExportedDocument{}, app.safeBindingError(fmt.Errorf("export desktop report: %w", err))
	}
	return exported, nil
}

func (app *DesktopApp) GetComparisons() (comparisons.Snapshot, error) {
	lease, err := app.acquire(desktopRequirements{comparisons: true})
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	snapshot, err := lease.comparisons.Snapshot(lease.ctx)
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(fmt.Errorf("query desktop comparisons: %w", err))
	}
	return snapshot, nil
}

func (app *DesktopApp) StartComparison(command comparisons.StartCommand) (comparisons.Snapshot, error) {
	if !domain.IsUUID(command.PlanID) || !domain.IsUUID(command.ModelID) || len(command.ChannelIDs) < 2 {
		return comparisons.Snapshot{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	for _, id := range command.ChannelIDs {
		if !domain.IsUUID(id) {
			return comparisons.Snapshot{}, app.safeBindingError(ErrInvalidIdentifier)
		}
	}
	lease, err := app.acquire(desktopRequirements{comparisons: true})
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	if _, err := lease.comparisons.Start(lease.ctx, command); err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(fmt.Errorf("start comparison: %w", err))
	}
	snapshot, err := lease.comparisons.Snapshot(lease.ctx)
	if err != nil {
		return comparisons.Snapshot{}, app.safeBindingError(fmt.Errorf("query comparisons after start: %w", err))
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
		return desktopLease{}, ErrRunCommandsUnavailable
	}
	if require.comparisons && isNilInterface(app.comparisons) {
		return desktopLease{}, ErrComparisonUnavailable
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
	app.catalogCommands = nil
	app.reports = nil
	app.commands = nil
	app.comparisons = nil
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
	if app != nil && !errors.Is(internal, ErrDesktopStartup) {
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
	case errors.Is(internal, ErrComparisonUnavailable):
		return DesktopBindingError{Code: desktopCodeComparisonMissing}
	case errors.Is(internal, ErrDiagnosticsUnavailable):
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	case errors.Is(internal, ErrInvalidIdentifier):
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	case errors.Is(internal, catalog.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeCatalogInvalid}
	case errors.Is(internal, catalog.ErrConflict):
		return DesktopBindingError{Code: desktopCodeCatalogConflict}
	case errors.Is(internal, catalog.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeCatalogNotFound}
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
		desktopCodeComparisonMissing,
		desktopCodeDiagnosticsMissing,
		desktopCodeInvalidIdentifier,
		desktopCodeOperationCancelled,
		desktopCodeOperationFailed,
		desktopCodeCatalogInvalid,
		desktopCodeCatalogConflict,
		desktopCodeCatalogNotFound:
		return true
	default:
		return false
	}
}
