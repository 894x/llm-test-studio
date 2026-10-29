package bootstrap

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestStartupFailureClassificationPreservesCause(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{name: "occupied workspace", err: fileconfig.ErrLockBusy, code: "workspace_busy"},
		{name: "permission", err: os.ErrPermission, code: "permission_denied"},
		{name: "disk full", err: syscall.ENOSPC, code: "disk_full"},
		{name: "missing storage", err: os.ErrNotExist, code: "storage_unavailable"},
		{name: "unsupported schema", err: sqlite.ErrSchemaResetRequired, code: "database_unsupported"},
		{name: "unknown cause", err: errors.New("private path and credential"), code: "unknown"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("startup operation: %w", test.err)
			classified := classifyStartupFailure(errors.Join(wrapped, errors.New("cleanup failure")))
			var failure *bridge.StartupFailure
			if !errors.As(classified, &failure) || failure.Code != test.code {
				t.Fatalf("classification = %v, want %s", classified, test.code)
			}
			if !errors.Is(classified, test.err) || failure.Cause.Error() != classified.Error() {
				t.Fatal("classification lost the diagnostic cause")
			}
		})
	}
}

func TestStartupFailureRecognizesSQLiteStorageErrors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA max_page_count = 1"); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE cannot_fit (value TEXT)")
	var failure *bridge.StartupFailure
	if err == nil || !errors.As(classifyStartupFailure(err), &failure) || failure.Code != "disk_full" {
		t.Fatalf("full SQLite database classification = %+v, %v", failure, err)
	}
}

func TestProductionStartupReportsActionableFailureWithoutChangingRejectedData(t *testing.T) {
	for _, kind := range []string{"workspace_busy", "database_corrupt", "database_unsupported"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "llm-test-studio")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "llm-test-studio.db")
			switch kind {
			case "workspace_busy":
				lock, err := fileconfig.TryAcquireExclusiveLock(t.Context(), filepath.Join(directory, "desktop-executor.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "database_corrupt":
				if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "database_unsupported":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, createErr := db.Exec("CREATE TABLE unrecognized_data (value TEXT)")
				if err := errors.Join(createErr, db.Close()); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			app := bridge.New(NewInitializer(Options{
				UserConfigDir: func() (string, error) { return root, nil }, AppVersion: "startup-recovery-test",
			}))
			var reported error
			bridge.SetErrorReporter(app, func(err error) { reported = err })
			bridge.Startup(app, t.Context())
			defer bridge.Shutdown(app)
			progress := app.GetStartupProgress()
			if progress.Status != "failed" || progress.FailureCode != kind || reported == nil {
				t.Fatalf("startup failure = %+v, diagnostic = %v", progress, reported)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("rejected startup changed the original database")
			}
		})
	}
}
