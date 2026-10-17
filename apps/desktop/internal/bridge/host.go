package bridge

import "context"

// Startup and Shutdown are host hooks, not methods exposed through Wails bindings.
func Startup(app *DesktopApp, ctx context.Context) { app.onStartup(ctx) }
func Shutdown(app *DesktopApp) error               { return app.shutdown() }

// SetEventEmitter installs the host event transport before startup.
func SetEventEmitter(app *DesktopApp, emit func(context.Context, string, ...interface{})) {
	app.setEventEmitter(emit)
}

// SetErrorReporter installs the local diagnostic sink without exposing it to the frontend.
func SetErrorReporter(app *DesktopApp, report func(error)) { app.setErrorReporter(report) }

// DiagnosticsOptions supplies the host's diagnostic storage location and build version.
type DiagnosticsOptions struct {
	UserConfigDir func() (string, error)
	AppVersion    string
}

// ActiveCalls reports calls currently holding a desktop lifecycle lease.
// It exposes no mutable lifecycle state and is not a Wails binding.
func ActiveCalls(app *DesktopApp) int {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.active
}
