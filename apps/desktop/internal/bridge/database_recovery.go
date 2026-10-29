package bridge

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DatabaseRecovery is host composition, not a path-taking frontend API.
type DatabaseRecovery struct {
	Prepare             func(context.Context) (Initializer, error)
	OpenBackupDirectory func() error
}

type DatabaseRecoveryResult struct {
	SchemaVersion  int    `json:"schema_version"`
	Status         string `json:"status"`
	FailureCode    string `json:"failure_code,omitempty"`
	WorkspaceReady bool   `json:"workspace_ready"`
	ReopenRequired bool   `json:"reopen_required,omitempty"`
}

type DatabaseActivationUncertain struct{ Cause error }

func (failure *DatabaseActivationUncertain) Error() string { return failure.Cause.Error() }
func (failure *DatabaseActivationUncertain) Unwrap() error { return failure.Cause }

func ConfigureDatabaseRecovery(app *DesktopApp, recovery DatabaseRecovery) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.databaseRecovery = recovery
}

func (app *DesktopApp) BackupAndCreateDatabase() (DatabaseRecoveryResult, error) {
	if !app.lifecycleMu.TryLock() {
		return DatabaseRecoveryResult{
			SchemaVersion: 1, Status: "failed", FailureCode: "workspace_busy",
		}, nil
	}
	defer app.lifecycleMu.Unlock()
	app.mu.Lock()
	failedIncompatible := app.startupProgress.Status == "failed" &&
		app.startupProgress.FailureCode == "database_unsupported"
	available := failedIncompatible && app.databaseRecovery.Prepare != nil
	shuttingDown := app.stopping || app.stopped
	blocked := app.recoveryNeedsReopen || app.active != 0
	if !available || shuttingDown || blocked {
		app.mu.Unlock()
		return DatabaseRecoveryResult{}, DesktopBindingError{Code: desktopCodeOperationFailed}
	}
	previous := app.startupProgress
	parent := app.startupParent
	prepare := app.databaseRecovery.Prepare
	report := app.reportError
	app.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	initialize, err := prepare(app.startupContext(ctx))
	if err == nil && initialize == nil {
		err = errors.New("database recovery returned no initializer")
	}
	if err != nil {
		var uncertain *DatabaseActivationUncertain
		reopen := errors.As(err, &uncertain)
		app.mu.Lock()
		failedProgress := app.startupProgress
		app.startupProgress = previous
		app.recoveryNeedsReopen = reopen
		app.mu.Unlock()
		if report != nil {
			report(fmt.Errorf("%w: backup and create database: %w", ErrDesktopStartup, err))
		}
		return DatabaseRecoveryResult{
			SchemaVersion: 1, Status: "failed",
			FailureCode: startupFailureCode(err, failedProgress), ReopenRequired: reopen,
		}, nil
	}
	app.mu.Lock()
	if app.cancel != nil {
		app.cancel()
	}
	app.started = false
	app.startupErr = nil
	app.mu.Unlock()
	app.startLocked(parent, initialize)
	return DatabaseRecoveryResult{
		SchemaVersion: 1, Status: "completed", WorkspaceReady: app.GetStartupProgress().Status == "completed",
	}, nil
}

func (app *DesktopApp) OpenDatabaseRecoveryDirectory() error {
	app.mu.Lock()
	open := app.databaseRecovery.OpenBackupDirectory
	app.mu.Unlock()
	if open == nil {
		return DesktopBindingError{Code: desktopCodeOperationFailed}
	}
	if err := open(); err != nil {
		return app.safeBindingError(fmt.Errorf("open database recovery directory: %w", err))
	}
	return nil
}
