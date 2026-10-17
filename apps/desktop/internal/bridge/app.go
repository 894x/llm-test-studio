package bridge

import (
	"context"
	"reflect"
	"sync"

	native "github.com/894x/llm-test-studio/apps/desktop/internal/native"
	clipboard "github.com/894x/llm-test-studio/apps/desktop/internal/native/clipboard"
	"github.com/894x/llm-test-studio/internal/application/jobfiles"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
)

// DesktopApp is the Wails binding. It owns only desktop lifecycle and
// delegation; business decisions remain in Application services.
type DesktopApp struct {
	lifecycleMu              sync.Mutex
	executionLocationMu      sync.RWMutex
	mu                       sync.Mutex
	drained                  *sync.Cond
	startupDone              *sync.Cond
	initialize               Initializer
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
	fileSelections           *jobfiles.Registry
}

func NewDesktopApp(query WorkspaceQuery, commands QuickTaskService) *DesktopApp {
	return New(func(context.Context) (Dependencies, error) {
		return Dependencies{Query: query, Commands: commands}, nil
	})
}

func New(initialize Initializer) *DesktopApp {
	app := &DesktopApp{
		initialize:       initialize,
		saveReportExport: native.SaveReportExport,
		copyReportPNG:    clipboard.WritePNG,
		fileSelections:   jobfiles.NewRegistry(),
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
