package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrDesktopNotStarted           = errors.New("desktop application has not started")
	ErrDesktopStartup              = errors.New("desktop application startup failed")
	ErrDesktopStopped              = errors.New("desktop application is shutting down or stopped")
	ErrWorkspaceUnavailable        = errors.New("workspace query is unavailable")
	ErrCatalogUnavailable          = errors.New("catalog query is unavailable")
	ErrReportingUnavailable        = errors.New("reporting query is unavailable")
	ErrQuickTaskServiceUnavailable = errors.New("run commands are unavailable")
	ErrComparisonUnavailable       = errors.New("comparison service is unavailable")
	ErrDiagnosticsUnavailable      = errors.New("desktop diagnostics are unavailable")
	ErrQuickTestUnavailable        = errors.New("quick test service is unavailable")
	ErrRemoteNodesUnavailable      = errors.New("remote node manager is unavailable")
	ErrCatalogSavedRefreshFailed   = errors.New("catalog mutation was saved but the refreshed snapshot is unavailable")
	ErrInvalidIdentifier           = errors.New("desktop command identifier is invalid")
)

const (
	desktopCodeNotStarted                = "desktop_not_started"
	desktopCodeStartupFailed             = "desktop_startup_failed"
	desktopCodeStopped                   = "desktop_stopped"
	desktopCodeWorkspaceMissing          = "workspace_unavailable"
	desktopCodeCatalogMissing            = "catalog_unavailable"
	desktopCodeReportsMissing            = "reports_unavailable"
	desktopCodeCommandsMissing           = "run_commands_unavailable"
	desktopCodeComparisonMissing         = "comparison_unavailable"
	desktopCodeDiagnosticsMissing        = "diagnostics_unavailable"
	desktopCodeQuickTestMissing          = "quick_test_unavailable"
	desktopCodeInvalidIdentifier         = "invalid_identifier"
	desktopCodeOperationCancelled        = "operation_cancelled"
	desktopCodeOperationFailed           = "operation_failed"
	desktopCodeRunInvalid                = "run_invalid"
	desktopCodeRunNotRunnable            = "run_not_runnable"
	desktopCodeRunNotActive              = "run_not_active"
	desktopCodeQuickTaskCredential       = "quick_task_credential_unavailable"
	desktopCodePlanProtocolMismatch      = "plan_protocol_mismatch"
	desktopCodeCatalogInvalid            = "catalog_invalid"
	desktopCodeCatalogConflict           = "catalog_revision_conflict"
	desktopCodeCatalogNotFound           = "catalog_not_found"
	desktopCodeCatalogSavedRefreshFailed = "catalog_saved_refresh_failed"
	desktopCodeComparisonInvalid         = "comparison_invalid"
	desktopCodeComparisonNotReady        = "comparison_not_ready"
	desktopCodeComparisonTargetUnmapped  = "comparison_target_unmapped"
	desktopCodeComparisonChannelNotReady = "comparison_channel_not_ready"
	desktopCodeRemoteNodesMissing        = "remote_nodes_unavailable"
	desktopCodeRemoteNodeInvalid         = "remote_node_invalid"
	desktopCodeRemoteNodeConflict        = "remote_node_revision_conflict"
	desktopCodeRemoteNodeNotFound        = "remote_node_not_found"
	desktopCodeRemoteNodeRePair          = "remote_node_re_pair_required"
	desktopCodeRemoteNodeReferenced      = "remote_node_still_referenced"
	desktopCodeRemoteNodeUsageUnknown    = "remote_node_usage_unavailable"
	desktopCodeRemoteNodeCredential      = "remote_node_credential_unavailable"
	desktopCodeRemoteNodeBusy            = "remote_node_busy"
	desktopCodeRemoteNodeUnconfirmed     = "remote_node_commit_unconfirmed"
	desktopCodeRemoteNodeUpgrade         = "remote_node_upgrade_required"
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

type ReportGenerationSource interface {
	GenerationSnapshot() reporting.GenerationSnapshot
	SubscribeProgress(func(reporting.GenerationProgress)) func()
}

type ReportDocumentQuery interface {
	ReportingQuery
	Detail(context.Context, string) (reporting.Detail, error)
	Export(context.Context, string, reporting.ExportFormat, string) (reporting.ExportedDocument, error)
}

type LocalizedReportExporter interface {
	ExportLocalized(context.Context, string, reporting.ExportFormat, string, string) (reporting.ExportedDocument, error)
}

// QuickTaskService retains saved-task queries and credential management.
type QuickTaskService interface {
	QuickTaskHistoryQuery
	QuickTaskCredentialCommands
}

type ComparisonService interface {
	Snapshot(context.Context) (comparisons.Snapshot, error)
}

type desktopEventEmitter func(context.Context, string, ...interface{})

type desktopDependencies struct {
	query                  WorkspaceQuery
	catalog                CatalogQuery
	catalogCommands        CatalogCommands
	reports                ReportingQuery
	reportGeneration       ReportGenerationSource
	commands               QuickTaskService
	comparisons            ComparisonService
	performancePreparation jobs.PerformancePreparation
	jobQuery               jobs.Query
	controlRecords         jobs.ControlRepository
	remoteNodes            *remotenodes.Manager
	remoteSync             *remotesync.Watcher
	jobs                   *jobs.Coordinator
	launcher               JobLauncher
	controls               JobController
	confirmations          *jobs.ConfirmationWatcher
	controlDelivery        *jobs.ControlDeliveryWatcher
	close                  func() error
}

type desktopInitializer func(context.Context) (desktopDependencies, error)

// DesktopApp is the Wails binding. It owns only desktop lifecycle and
// delegation; business decisions remain in Application services.
type DesktopApp struct {
	lifecycleMu              sync.Mutex
	executionLocationMu      sync.RWMutex
	mu                       sync.Mutex
	drained                  *sync.Cond
	startupDone              *sync.Cond
	initialize               desktopInitializer
	starting                 bool
	started                  bool
	stopping                 bool
	stopped                  bool
	active                   int
	ctx                      context.Context
	cancel                   context.CancelFunc
	query                    WorkspaceQuery
	catalog                  CatalogQuery
	catalogCommands          CatalogCommands
	reports                  ReportingQuery
	reportGeneration         ReportGenerationSource
	commands                 QuickTaskService
	comparisons              ComparisonService
	launcher                 JobLauncher
	jobQuery                 jobs.Query
	controls                 JobController
	controlRecords           jobs.ControlRepository
	controlDelivery          *jobs.ControlDeliveryWatcher
	remoteNodes              *remotenodes.Manager
	remoteSync               *remotesync.Watcher
	jobs                     *jobs.Coordinator
	confirmations            *jobs.ConfirmationWatcher
	close                    func() error
	startupErr               error
	shutdownErr              error
	reportError              func(error)
	diagnostics              DesktopDiagnosticsSnapshot
	openDiagnostics          func() error
	reportFrontendDiagnostic func(FrontendDiagnostic) error
	emitEvent                desktopEventEmitter
	saveReportExport         reportExportSaver
	copyReportPNG            reportPNGClipboardWriter
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

// FrontendDiagnostic is the bounded, secret-redacted diagnostic payload that
// React may send back to the desktop adapter when a successful binding response
// cannot be consumed locally.
type FrontendDiagnostic struct {
	Operation string `json:"operation"`
	ErrorCode string `json:"error_code"`
	Detail    string `json:"detail"`
}

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

func NewDesktopApp(query WorkspaceQuery, commands QuickTaskService) *DesktopApp {
	return newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{query: query, commands: commands}, nil
	})
}

func newDesktopApp(initialize desktopInitializer) *DesktopApp {
	app := &DesktopApp{
		initialize:       initialize,
		saveReportExport: saveReportExportToFile,
		copyReportPNG:    writePNGToClipboard,
	}
	app.drained = sync.NewCond(&app.mu)
	app.startupDone = sync.NewCond(&app.mu)
	return app
}

func (app *DesktopApp) setEventEmitter(emit desktopEventEmitter) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.emitEvent = emit
	app.mu.Unlock()
}

func (app *DesktopApp) emitDesktopEvent(ctx context.Context, name string, data ...interface{}) {
	app.mu.Lock()
	emit := app.emitEvent
	app.mu.Unlock()
	if emit != nil {
		emit(ctx, name, data...)
	}
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

func (app *DesktopApp) setFrontendDiagnosticReporter(report func(FrontendDiagnostic) error) {
	if app == nil {
		return
	}
	app.mu.Lock()
	app.reportFrontendDiagnostic = report
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

func (app *DesktopApp) ReportFrontendDiagnostic(diagnostic FrontendDiagnostic) error {
	if app == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	diagnostic.Operation = strings.TrimSpace(diagnostic.Operation)
	diagnostic.ErrorCode = strings.TrimSpace(diagnostic.ErrorCode)
	diagnostic.Detail = strings.TrimSpace(diagnostic.Detail)
	if !isFrontendDiagnosticOperation(diagnostic.Operation) ||
		!isFrontendDiagnosticCode(diagnostic.ErrorCode) ||
		diagnostic.Detail == "" || len(diagnostic.Detail) > 2048 {
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	}
	app.mu.Lock()
	report := app.reportFrontendDiagnostic
	app.mu.Unlock()
	if report == nil {
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	}
	if err := report(diagnostic); err != nil {
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
	return nil
}

func isFrontendDiagnosticOperation(operation string) bool {
	switch operation {
	case "load_workspace", "load_catalog", "load_reports", "load_comparisons":
		return true
	default:
		return false
	}
}

func isFrontendDiagnosticCode(code string) bool {
	switch code {
	case "frontend_data_invalid", "frontend_operation_failed":
		return true
	default:
		return false
	}
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
	app.reportGeneration = dependencies.reportGeneration
	app.commands = dependencies.commands
	app.comparisons = dependencies.comparisons
	app.launcher = dependencies.launcher
	app.jobQuery = dependencies.jobQuery
	app.controls = dependencies.controls
	app.controlRecords = dependencies.controlRecords
	app.controlDelivery = dependencies.controlDelivery
	app.remoteNodes = dependencies.remoteNodes
	app.remoteSync = dependencies.remoteSync
	app.jobs = dependencies.jobs
	app.confirmations = dependencies.confirmations
	app.close = dependencies.close
	if dependencies.reportGeneration != nil {
		unsubscribe := dependencies.reportGeneration.SubscribeProgress(func(progress reporting.GenerationProgress) {
			if lifecycleContext.Err() == nil {
				app.emitDesktopEvent(lifecycleContext, "report-generation-progress", progress)
			}
		})
		app.close = func() error {
			unsubscribe()
			if dependencies.close != nil {
				return dependencies.close()
			}
			return nil
		}
	}
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

func (app *DesktopApp) GetReportGeneration() (reporting.GenerationSnapshot, error) {
	lease, err := app.acquire(desktopRequirements{})
	if err != nil {
		return reporting.GenerationSnapshot{}, app.safeBindingError(err)
	}
	defer lease.release()
	app.mu.Lock()
	source := app.reportGeneration
	app.mu.Unlock()
	if source == nil {
		return reporting.GenerationSnapshot{SchemaVersion: 1, Runs: []reporting.GenerationProgress{}}, nil
	}
	return source.GenerationSnapshot(), nil
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

func (err catalogCommandDiagnosticError) Error() string {
	return fmt.Sprintf("%s: %v", err.operation, err.err)
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
	snapshot, err := lease.catalog.Snapshot(lease.ctx)
	if err != nil {
		return catalog.Snapshot{}, app.safeBindingError(catalogCommandDiagnosticError{
			operation: name,
			err:       fmt.Errorf("refresh catalog snapshot: %w", err),
			committed: true,
		})
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

func (app *DesktopApp) ExportReport(reportID, format, watermark, locale string) (reporting.ExportedDocument, error) {
	if !domain.IsUUID(reportID) {
		return reporting.ExportedDocument{}, app.safeBindingError(ErrInvalidIdentifier)
	}
	if locale != "zh-CN" && locale != "en-US" {
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
	var exported reporting.ExportedDocument
	if localized, ok := lease.reports.(LocalizedReportExporter); ok && !isNilInterface(localized) {
		exported, err = localized.ExportLocalized(lease.ctx, reportID, exportFormat, watermark, locale)
	} else {
		exported, err = documents.Export(lease.ctx, reportID, exportFormat, watermark)
	}
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
	case errors.Is(internal, ErrJobCommitUnconfirmed):
		return DesktopBindingError{Code: desktopCodeJobCommitUnconfirmed}
	case errors.Is(internal, ErrJobQueryUnavailable):
		return DesktopBindingError{Code: desktopCodeJobQueryUnavailable}
	case errors.Is(internal, ErrJobCommandsUnavailable):
		return DesktopBindingError{Code: desktopCodeJobCommandsUnavailable}
	case errors.Is(internal, jobs.ErrUpgradeRequired):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUpgrade}
	case errors.Is(internal, jobs.ErrTargetChanged):
		return DesktopBindingError{Code: desktopCodeRemoteNodeRePair}
	case errors.Is(internal, jobs.ErrPending):
		return DesktopBindingError{Code: desktopCodeJobPending}
	case errors.Is(internal, jobs.ErrConflict):
		return DesktopBindingError{Code: desktopCodeJobConflict}
	case errors.Is(internal, jobs.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeJobNotFound}
	case errors.Is(internal, jobs.ErrUnavailable):
		return DesktopBindingError{Code: desktopCodeJobUnavailable}
	case errors.Is(internal, jobs.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeJobInvalid}
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
	case errors.Is(internal, ErrQuickTaskServiceUnavailable):
		return DesktopBindingError{Code: desktopCodeCommandsMissing}
	case errors.Is(internal, ErrComparisonUnavailable):
		return DesktopBindingError{Code: desktopCodeComparisonMissing}
	case errors.Is(internal, ErrDiagnosticsUnavailable):
		return DesktopBindingError{Code: desktopCodeDiagnosticsMissing}
	case errors.Is(internal, ErrQuickTestUnavailable):
		return DesktopBindingError{Code: desktopCodeQuickTestMissing}
	case errors.Is(internal, ErrRemoteNodesUnavailable):
		return DesktopBindingError{Code: desktopCodeRemoteNodesMissing}
	case errors.Is(internal, remotenodes.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeRemoteNodeInvalid}
	case errors.Is(internal, remotenodes.ErrConflict):
		return DesktopBindingError{Code: desktopCodeRemoteNodeConflict}
	case errors.Is(internal, remotenodes.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeRemoteNodeNotFound}
	case errors.Is(internal, remotenodes.ErrRePairRequired), errors.Is(internal, remotenodes.ErrDuplicate):
		return DesktopBindingError{Code: desktopCodeRemoteNodeRePair}
	case errors.Is(internal, remotenodes.ErrReferenced):
		return DesktopBindingError{Code: desktopCodeRemoteNodeReferenced}
	case errors.Is(internal, remotenodes.ErrUsageUnknown):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUsageUnknown}
	case errors.Is(internal, remotenodes.ErrCredential):
		return DesktopBindingError{Code: desktopCodeRemoteNodeCredential}
	case errors.Is(internal, remotenodes.ErrBusy):
		return DesktopBindingError{Code: desktopCodeRemoteNodeBusy}
	case errors.Is(internal, remotenodes.ErrCommitUnconfirmed):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUnconfirmed}
	case errors.Is(internal, remotenodes.ErrUnsupported):
		return DesktopBindingError{Code: desktopCodeRemoteNodeUpgrade}
	case errors.Is(internal, ErrInvalidIdentifier):
		return DesktopBindingError{Code: desktopCodeInvalidIdentifier}
	case errors.Is(internal, ErrCatalogSavedRefreshFailed):
		return DesktopBindingError{Code: desktopCodeCatalogSavedRefreshFailed}
	case errors.Is(internal, catalog.ErrPlanProtocolMismatch):
		return DesktopBindingError{Code: desktopCodePlanProtocolMismatch}
	case errors.Is(internal, catalog.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeCatalogInvalid}
	case errors.Is(internal, catalog.ErrConflict):
		return DesktopBindingError{Code: desktopCodeCatalogConflict}
	case errors.Is(internal, catalog.ErrNotFound):
		return DesktopBindingError{Code: desktopCodeCatalogNotFound}
	case errors.Is(internal, comparisons.ErrTargetUnmapped):
		return DesktopBindingError{Code: desktopCodeComparisonTargetUnmapped}
	case errors.Is(internal, comparisons.ErrChannelNotReady):
		return DesktopBindingError{Code: desktopCodeComparisonChannelNotReady}
	case errors.Is(internal, comparisons.ErrNotReady):
		return DesktopBindingError{Code: desktopCodeComparisonNotReady}
	case errors.Is(internal, comparisons.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeComparisonInvalid}
	case errors.Is(internal, runs.ErrInvalid):
		return DesktopBindingError{Code: desktopCodeRunInvalid}
	case errors.Is(internal, runs.ErrNotRunnable):
		return DesktopBindingError{Code: desktopCodeRunNotRunnable}
	case errors.Is(internal, runs.ErrNotActive):
		return DesktopBindingError{Code: desktopCodeRunNotActive}
	case errors.Is(internal, runs.ErrClosed):
		return DesktopBindingError{Code: desktopCodeCommandsMissing}
	case errors.Is(internal, runs.ErrQuickTaskCredential):
		return DesktopBindingError{Code: desktopCodeQuickTaskCredential}
	case errors.Is(internal, context.Canceled), errors.Is(internal, context.DeadlineExceeded):
		return DesktopBindingError{Code: desktopCodeOperationCancelled}
	default:
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
}

func isDesktopBindingCode(code string) bool {
	switch code {
	case desktopCodeJobInvalid, desktopCodeJobNotFound, desktopCodeJobConflict, desktopCodeJobUnavailable,
		desktopCodeJobPending, desktopCodeJobCommitUnconfirmed, desktopCodeJobQueryUnavailable, desktopCodeJobCommandsUnavailable,
		desktopCodeNotStarted,
		desktopCodeStartupFailed,
		desktopCodeStopped,
		desktopCodeWorkspaceMissing,
		desktopCodeCatalogMissing,
		desktopCodeReportsMissing,
		desktopCodeCommandsMissing,
		desktopCodeComparisonMissing,
		desktopCodeDiagnosticsMissing,
		desktopCodeQuickTestMissing,
		desktopCodeInvalidIdentifier,
		desktopCodeOperationCancelled,
		desktopCodeOperationFailed,
		desktopCodeRunInvalid,
		desktopCodeRunNotRunnable,
		desktopCodeRunNotActive,
		desktopCodeQuickTaskCredential,
		desktopCodePlanProtocolMismatch,
		desktopCodeCatalogInvalid,
		desktopCodeCatalogConflict,
		desktopCodeCatalogNotFound,
		desktopCodeCatalogSavedRefreshFailed,
		desktopCodeComparisonInvalid,
		desktopCodeComparisonNotReady,
		desktopCodeComparisonTargetUnmapped,
		desktopCodeComparisonChannelNotReady,
		desktopCodeRemoteNodesMissing,
		desktopCodeRemoteNodeInvalid,
		desktopCodeRemoteNodeConflict,
		desktopCodeRemoteNodeNotFound,
		desktopCodeRemoteNodeRePair,
		desktopCodeRemoteNodeReferenced,
		desktopCodeRemoteNodeUsageUnknown,
		desktopCodeRemoteNodeCredential,
		desktopCodeRemoteNodeBusy,
		desktopCodeRemoteNodeUnconfirmed,
		desktopCodeRemoteNodeUpgrade:
		return true
	default:
		return false
	}
}
