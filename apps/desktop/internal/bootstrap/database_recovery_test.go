package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func incompatibleRecoveryFixture(t *testing.T) (Options, string, string) {
	t.Helper()
	root, executable := t.TempDir(), t.TempDir()
	directory := filepath.Join(root, "llm-test-studio")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "llm-test-studio.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, createErr := db.ExecContext(t.Context(), "CREATE TABLE customer_history(value TEXT); INSERT INTO customer_history VALUES ('preserved history')")
	if err := errors.Join(createErr, db.Close()); err != nil {
		t.Fatal(err)
	}
	return Options{
		UserConfigDir: func() (string, error) { return root, nil }, AppVersion: "recovery-test",
		ExecutablePath:  func() (string, error) { return filepath.Join(executable, "desktop.exe"), nil },
		CredentialStore: credentials.NewMemoryStore(),
	}, directory, path
}

func TestDatabaseRecoveryBacksUpThenActivatesAndReopensFreshWorkspace(t *testing.T) {
	options, directory, original := incompatibleRecoveryFixture(t)
	before, _ := os.ReadFile(original)
	app := bridge.New(NewInitializer(options))
	bridge.ConfigureDatabaseRecovery(app, NewDatabaseRecovery(options))
	bridge.Startup(app, t.Context())
	if !app.GetStartupProgress().RecoveryAvailable {
		t.Fatal("unsupported startup did not expose recovery")
	}
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "completed" || !result.WorkspaceReady {
		t.Fatalf("recovery = %+v, %v; progress %+v", result, err, app.GetStartupProgress())
	}
	selection, exists, err := readDatabaseSelection(directory)
	if err != nil || !exists {
		t.Fatal("fresh database activation was not persisted", err)
	}
	backupPath := filepath.Join(directory, selection.BackupDirectory, "original.sqlite")
	db, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM customer_history").Scan(&value); err != nil || value != "preserved history" {
		t.Fatal("backup lost original history")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("recovery changed the original database")
	}
	fresh, err := resolveDesktopDatabase(directory, original)
	if err != nil || fresh == original {
		t.Fatal("recovery did not select a different database")
	}
	if version, err := sqlite.SchemaVersion(t.Context(), fresh); err != nil || version != sqlite.CurrentSchemaVersion {
		t.Fatal("fresh database does not use the current schema", err)
	}
	if _, err := app.BackupAndCreateDatabase(); err == nil {
		t.Fatal("duplicate recovery was accepted after successful startup")
	}
	if lock, err := fileconfig.TryAcquireExclusiveLock(t.Context(), filepath.Join(directory, "desktop-executor.lock")); err == nil {
		_ = lock.Close()
		t.Fatal("recovery released ownership before the fresh application closed")
	}
	if err := bridge.Shutdown(app); err != nil {
		t.Fatal(err)
	}
	reopened := bridge.New(NewInitializer(options))
	bridge.Startup(reopened, t.Context())
	defer bridge.Shutdown(reopened)
	if reopened.GetStartupProgress().Status != "completed" {
		t.Fatal("repeat open did not use the selected fresh database")
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatal("repeat open removed the backup")
	}
}

func TestDatabaseRecoveryFailuresDoNotActivateOrChangeTheOriginal(t *testing.T) {
	for _, stage := range []string{"backup", "backup_publication", "fresh", "fresh_publication", "activation", "occupied", "cancelled"} {
		t.Run(stage, func(t *testing.T) {
			options, directory, original := incompatibleRecoveryFixture(t)
			before, _ := os.ReadFile(original)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "cancelled" {
				cancel()
			}
			if stage == "occupied" {
				lock, err := fileconfig.TryAcquireExclusiveLock(ctx, filepath.Join(directory, "desktop-executor.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			write := func(ctx context.Context, path string, payload []byte) error {
				failBackup := stage == "backup" && filepath.Base(path) == "backup.json"
				failActivation := stage == "activation" && filepath.Base(path) == databaseSelectionName
				if failBackup || failActivation {
					return syscall.ENOSPC
				}
				return fileconfig.WriteAtomically(ctx, path, payload)
			}
			migrate := func(ctx context.Context, path string, options sqlite.MigrateOptions) error {
				if stage == "fresh" {
					return syscall.ENOSPC
				}
				return sqlite.Migrate(ctx, path, options)
			}
			storage := defaultDatabaseRecoveryIO()
			storage.write, storage.migrate = write, migrate
			storage.finalize = func(ctx context.Context, path string) (string, error) {
				failBackup := stage == "backup_publication" && strings.HasPrefix(filepath.Base(path), "database-recovery-")
				failFresh := stage == "fresh_publication" && strings.HasPrefix(filepath.Base(path), "database-workspace-")
				if failBackup || failFresh {
					return "", syscall.ENOSPC
				}
				return sqlite.FinalizeRecoveryDirectory(ctx, path)
			}
			if initialize, err := prepareDatabaseRecovery(ctx, options, storage); err == nil || initialize != nil {
				t.Fatal("faulted recovery succeeded")
			}
			if selected, err := resolveDesktopDatabase(directory, original); err != nil || selected != original {
				t.Fatal("failed recovery changed active selection", err)
			}
			after, _ := os.ReadFile(original)
			if string(before) != string(after) {
				t.Fatal("failed recovery changed original data")
			}
		})
	}
}

func TestDatabaseRecoveryRefusesChangedSourceAndCorruptSelection(t *testing.T) {
	options, directory, original := incompatibleRecoveryFixture(t)
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(t.Context(), original, sqlite.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDatabaseRecovery(options).Prepare(t.Context()); err == nil {
		t.Fatal("a now-compatible database was reset")
	}
	for _, payload := range []string{
		`{"schema_version":2,"database_directory":"database-workspace-1","backup_directory":"database-recovery-1"}`,
		`{"schema_version":1,"schema_version":1,"database_directory":"database-workspace-1","backup_directory":"database-recovery-1"}`,
		`{"schema_version":1,"database_directory":"../outside","backup_directory":"database-recovery-1"}`,
		`{"schema_version":1,"database_directory":"database-workspace-1","backup_directory":"database-recovery-1","unknown":true}`,
		`{"schema_version":1}`, `null`, strings.Repeat(" ", 5000),
	} {
		path := filepath.Join(directory, databaseSelectionName)
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveDesktopDatabase(directory, original); err == nil {
			t.Fatal("invalid selection silently fell back to the original database")
		}
		if after, _ := os.ReadFile(path); string(after) != payload {
			t.Fatal("invalid selection was rewritten")
		}
	}
}

func TestDatabaseRecoveryReopensAfterPublicationReportedAnError(t *testing.T) {
	options, directory, original := incompatibleRecoveryFixture(t)
	before, _ := os.ReadFile(original)
	write := func(ctx context.Context, path string, payload []byte) error {
		if err := fileconfig.WriteAtomically(ctx, path, payload); err != nil {
			return err
		}
		if filepath.Base(path) == databaseSelectionName {
			// POSIX directory sync can fail after the atomic rename has committed.
			return errors.New("injected directory sync failure after publication")
		}
		return nil
	}
	storage := defaultDatabaseRecoveryIO()
	storage.write = write
	if initialize, err := prepareDatabaseRecovery(t.Context(), options, storage); err == nil || initialize != nil {
		t.Fatal("unconfirmed activation was reported successful")
	}
	selection, exists, err := readDatabaseSelection(directory)
	if err != nil || !exists {
		t.Fatal("published complete selection could not be recovered")
	}
	app := bridge.New(NewInitializer(options))
	bridge.Startup(app, t.Context())
	defer bridge.Shutdown(app)
	if app.GetStartupProgress().Status != "completed" {
		t.Fatal("reopen failed after publication")
	}
	if _, err := os.Stat(filepath.Join(directory, selection.BackupDirectory, "original.sqlite")); err != nil {
		t.Fatal("activation did not retain its completed backup")
	}
	if after, _ := os.ReadFile(original); string(after) != string(before) {
		t.Fatal("recovery after publication changed original data")
	}
}
