package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestDatabaseRecoveryRequiresIncompatibleStartupAndSerializesRequests(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, &StartupFailure{Code: "database_unsupported", Cause: errors.New("private source")}
	})
	entered, release := make(chan struct{}), make(chan struct{})
	prepared, closed := 0, 0
	ConfigureDatabaseRecovery(app, DatabaseRecovery{Prepare: func(ctx context.Context) (Initializer, error) {
		prepared++
		ReportStartupProgress(ctx, "database_backing_up", 0, 0)
		close(entered)
		<-release
		return func(context.Context) (Dependencies, error) {
			return Dependencies{Close: func() error { closed++; return nil }}, nil
		}, nil
	}})
	if _, err := app.BackupAndCreateDatabase(); err == nil || prepared != 0 {
		t.Fatal("recovery was allowed before startup")
	}
	app.onStartup(t.Context())
	if !app.GetStartupProgress().RecoveryAvailable {
		t.Fatal("recovery was not available after incompatible startup")
	}
	done := make(chan DatabaseRecoveryResult, 1)
	go func() {
		result, _ := app.BackupAndCreateDatabase()
		done <- result
	}()
	<-entered
	duplicate, err := app.BackupAndCreateDatabase()
	if err != nil || duplicate.Status != "failed" || duplicate.FailureCode != "workspace_busy" {
		t.Fatal("duplicate recovery was not rejected")
	}
	close(release)
	result := <-done
	if result.Status != "completed" || !result.WorkspaceReady || app.GetStartupProgress().RecoveryAvailable {
		t.Fatalf("recovery result = %+v", result)
	}
	if _, err := app.BackupAndCreateDatabase(); err == nil || prepared != 1 {
		t.Fatal("successful workspace was reset again")
	}
	if err := app.shutdown(); err != nil || closed != 1 {
		t.Fatal("recovered dependencies were not closed exactly once")
	}
}

func TestDatabaseRecoveryFailureKeepsOriginalStartupAndReportsCause(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, &StartupFailure{Code: "database_unsupported", Cause: errors.New("unsupported source")}
	})
	cause := errors.New("private/path sk-secret")
	ConfigureDatabaseRecovery(app, DatabaseRecovery{Prepare: func(ctx context.Context) (Initializer, error) {
		ReportStartupProgress(ctx, "database_backing_up", 0, 0)
		return nil, &StartupFailure{Code: "disk_full", Cause: cause}
	}})
	var reported error
	SetErrorReporter(app, func(err error) { reported = err })
	app.onStartup(t.Context())
	defer app.shutdown()
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "failed" || result.FailureCode != "disk_full" || !errors.Is(reported, cause) {
		t.Fatalf("failure result = %+v, %v", result, err)
	}
	progress := app.GetStartupProgress()
	if progress.Stage != "opening" || progress.FailureCode != "database_unsupported" || !progress.RecoveryAvailable {
		t.Fatal("failed preparation lost the original recoverable state")
	}
}

func TestDatabaseRecoveryDistinguishesPreparedDatabaseFromWorkspaceStartup(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, &StartupFailure{Code: "database_unsupported", Cause: errors.New("unsupported source")}
	})
	ConfigureDatabaseRecovery(app, DatabaseRecovery{Prepare: func(context.Context) (Initializer, error) {
		return func(context.Context) (Dependencies, error) {
			return Dependencies{}, &StartupFailure{Code: "permission_denied", Cause: errors.New("private path")}
		}, nil
	}})
	app.onStartup(t.Context())
	defer app.shutdown()
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "completed" || result.WorkspaceReady {
		t.Fatal("fresh database preparation was confused with successful workspace startup")
	}
	if app.GetStartupProgress().FailureCode != "permission_denied" {
		t.Fatal("new workspace initialization failure was hidden")
	}
}

func TestDatabaseRecoveryUncertainPublicationRequiresReopen(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, &StartupFailure{Code: "database_unsupported", Cause: errors.New("unsupported")}
	})
	prepared := 0
	ConfigureDatabaseRecovery(app, DatabaseRecovery{Prepare: func(ctx context.Context) (Initializer, error) {
		prepared++
		ReportStartupProgress(ctx, "database_activating", 0, 0)
		return nil, &DatabaseActivationUncertain{Cause: errors.New("private publication error")}
	}})
	app.onStartup(t.Context())
	defer app.shutdown()
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "failed" || !result.ReopenRequired || app.GetStartupProgress().RecoveryAvailable {
		t.Fatalf("uncertain result = %+v, %v", result, err)
	}
	if _, err := app.BackupAndCreateDatabase(); err == nil || prepared != 1 {
		t.Fatal("uncertain activation could be repeated")
	}
}

func TestDatabaseRecoveryClassifiesTheFailedBackupStage(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) {
		return Dependencies{}, &StartupFailure{Code: "database_unsupported", Cause: errors.New("unsupported")}
	})
	ConfigureDatabaseRecovery(app, DatabaseRecovery{Prepare: func(ctx context.Context) (Initializer, error) {
		ReportStartupProgress(ctx, "database_backing_up", 0, 0)
		return nil, errors.New("generic snapshot validation error")
	}})
	app.onStartup(t.Context())
	defer app.shutdown()
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.FailureCode != "backup_failed" {
		t.Fatal("actual failed backup stage was discarded")
	}
}
