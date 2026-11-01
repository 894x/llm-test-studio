package bootstrap

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// Start with the frozen v1 producer fixture, changing only a historical report
// to exercise a format that the source schema accepts but migration cannot use.
func historicalContentRecoveryFixture(t *testing.T) (Options, string, string) {
	t.Helper()
	options, directory, path := incompatibleRecoveryFixture(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "..", "..", "internal", "persistence", "sqlite")
	var catalog struct {
		Objects map[string]struct {
			DDL string `json:"ddl"`
		} `json:"objects"`
		Baselines []struct {
			Commit  string   `json:"commit"`
			Objects []string `json:"objects"`
		} `json:"baselines"`
	}
	payload, err := os.ReadFile(filepath.Join(root, "migrations", "operational_baselines.json"))
	if err != nil || json.Unmarshal(payload, &catalog) != nil {
		t.Fatal("cannot load frozen migration catalog", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, baseline := range catalog.Baselines {
		if strings.HasPrefix(baseline.Commit, "8190caae") {
			for _, key := range baseline.Objects {
				if _, err := db.ExecContext(t.Context(), catalog.Objects[key].DDL); err != nil {
					t.Fatal(err)
				}
			}
			break
		}
	}
	file, err := os.Open(filepath.Join(root, "testdata", "migrations", "8190caae.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture struct {
		Tables []struct {
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"tables"`
	}
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	for _, table := range fixture.Tables {
		query := fmt.Sprintf("INSERT INTO \"%s\" (\"%s\") VALUES (%s)", table.Table,
			strings.Join(table.Columns, `","`), strings.TrimSuffix(strings.Repeat("?,", len(table.Columns)), ","))
		for _, row := range table.Rows {
			for index, value := range row {
				switch value := value.(type) {
				case json.Number:
					integer, err := value.Int64()
					if err == nil {
						row[index] = integer
					} else if row[index], err = value.Float64(); err != nil {
						t.Fatal(err)
					}
				case map[string]any:
					row[index], err = base64.StdEncoding.DecodeString(value["blob"].(string))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := db.ExecContext(t.Context(), query, row...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA user_version = 1;
		UPDATE reports SET document_json = json_set(document_json, '$.execution_origin', 'incompatible')`); err != nil {
		t.Fatal(err)
	}
	return options, directory, path
}

func TestHistoricalContentFailureCanRecoverWithoutChangingOriginal(t *testing.T) {
	options, directory, original := historicalContentRecoveryFixture(t)
	before, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	app := bridge.New(NewInitializer(options))
	bridge.ConfigureDatabaseRecovery(app, NewDatabaseRecovery(options))
	bridge.Startup(app, t.Context())
	defer bridge.Shutdown(app)
	if progress := app.GetStartupProgress(); progress.FailureCode != "database_unsupported" || !progress.RecoveryAvailable {
		t.Fatalf("historical content recovery unavailable: %+v", progress)
	}
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "completed" || !result.WorkspaceReady {
		t.Fatalf("recovery = %+v, %v", result, err)
	}
	selection, exists, err := readDatabaseSelection(directory)
	if err != nil || !exists {
		t.Fatal("missing selection", err)
	}
	backup := filepath.Join(directory, selection.BackupDirectory, "original.sqlite")
	db, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	var origin string
	err = db.QueryRowContext(t.Context(), `SELECT json_extract(document_json, '$.execution_origin') FROM reports LIMIT 1`).Scan(&origin)
	if closeErr := db.Close(); err != nil || closeErr != nil || origin != "incompatible" {
		t.Fatal("backup lost historical report", origin, err, closeErr)
	}
	after, err := os.ReadFile(original)
	if err != nil || string(before) != string(after) {
		t.Fatal("recovery changed original", err)
	}
	if err := bridge.Shutdown(app); err != nil {
		t.Fatal(err)
	}
	reopened := bridge.New(NewInitializer(options))
	bridge.Startup(reopened, t.Context())
	defer bridge.Shutdown(reopened)
	if reopened.GetStartupProgress().Status != "completed" {
		t.Fatal("new workspace did not survive reopening")
	}
}

func TestHistoricalContentRecoveryRejectsSourceFixedAfterStartup(t *testing.T) {
	options, directory, original := historicalContentRecoveryFixture(t)
	app := bridge.New(NewInitializer(options))
	bridge.ConfigureDatabaseRecovery(app, NewDatabaseRecovery(options))
	bridge.Startup(app, t.Context())
	defer bridge.Shutdown(app)
	if !app.GetStartupProgress().RecoveryAvailable {
		t.Fatal("expected recoverable failure")
	}
	db, err := sql.Open("sqlite", original)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `UPDATE reports SET document_json = json_remove(document_json, '$.execution_origin')`)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	result, err := app.BackupAndCreateDatabase()
	if err != nil || result.Status != "failed" {
		t.Fatalf("fixed source was switched: %+v %v", result, err)
	}
	if _, exists, err := readDatabaseSelection(directory); err != nil || exists {
		t.Fatal("fixed source was replaced", err)
	}
	if version, err := sqlite.SchemaVersion(t.Context(), original); err != nil || version != 1 {
		t.Fatal("source was migrated", version, err)
	}
}

func TestRecoveryKeepsSourceWriteLockedUntilActivation(t *testing.T) {
	options, _, original := incompatibleRecoveryFixture(t)
	storage := defaultDatabaseRecoveryIO()
	write := storage.write
	checked := false
	storage.write = func(ctx context.Context, path string, payload []byte) error {
		if filepath.Base(path) == databaseSelectionName {
			db, err := sql.Open("sqlite", original)
			if err != nil {
				return err
			}
			_, writeErr := db.ExecContext(ctx, "INSERT INTO customer_history VALUES ('concurrent writer')")
			closeErr := db.Close()
			if writeErr == nil {
				return errors.New("recovery did not protect its source from a concurrent writer")
			}
			if closeErr != nil {
				return closeErr
			}
			checked = true
		}
		return write(ctx, path, payload)
	}
	initialize, err := prepareDatabaseRecovery(t.Context(), options, storage)
	if err != nil || initialize == nil || !checked {
		t.Fatalf("recovery lock verification failed: checked=%t err=%v", checked, err)
	}
	app := bridge.New(initialize)
	bridge.Startup(app, t.Context())
	if err := bridge.Shutdown(app); err != nil {
		t.Fatal(err)
	}
}
