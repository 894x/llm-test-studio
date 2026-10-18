package bridge

import (
	"context"
	"time"
)

// StartupProgress is safe to query while ordinary bindings wait for startup.
// Stage boundaries are real work events, not an estimated percentage.
type StartupProgress struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Stage         string `json:"stage"`
	FromVersion   int    `json:"from_version"`
	ToVersion     int    `json:"to_version"`
	ElapsedMS     int64  `json:"elapsed_ms"`
}

type startupObserverKey struct{}

// ReportStartupProgress carries composition progress without a global observer.
func ReportStartupProgress(ctx context.Context, stage string, from, to int) {
	if observe, ok := ctx.Value(startupObserverKey{}).(func(string, int, int)); ok {
		observe(stage, from, to)
	}
}

func (app *DesktopApp) GetStartupProgress() StartupProgress {
	app.mu.Lock()
	defer app.mu.Unlock()
	progress := app.startupProgress
	if progress.SchemaVersion == 0 {
		progress = StartupProgress{SchemaVersion: 1, Status: "pending", Stage: "opening"}
	}
	if progress.Status == "running" {
		progress.ElapsedMS = time.Since(app.startupStarted).Milliseconds()
	}
	return progress
}

func (app *DesktopApp) startupContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, startupObserverKey{}, func(stage string, from, to int) {
		app.mu.Lock()
		defer app.mu.Unlock()
		app.startupProgress.Stage = stage
		app.startupProgress.FromVersion = from
		app.startupProgress.ToVersion = to
		app.startupProgress.ElapsedMS = time.Since(app.startupStarted).Milliseconds()
	})
}
