package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/apps/desktop/internal/native"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func NewDatabaseRecovery(options Options) bridge.DatabaseRecovery {
	return bridge.DatabaseRecovery{
		Prepare: func(ctx context.Context) (bridge.Initializer, error) {
			return prepareDatabaseRecovery(ctx, options, defaultDatabaseRecoveryIO())
		},
		OpenBackupDirectory: func() error {
			directory, _, err := recoveryStoragePaths(options)
			if err != nil {
				return err
			}
			selection, exists, err := readDatabaseSelection(directory)
			if err != nil {
				return err
			}
			if !exists {
				return errors.New("database recovery backup is unavailable")
			}
			if err := validateRecoveryDirectory(directory, selection.BackupDirectory); err != nil {
				return err
			}
			return native.OpenDirectory(filepath.Join(directory, selection.BackupDirectory))
		},
	}
}

type databaseRecoveryIO struct {
	write    func(context.Context, string, []byte) error
	migrate  func(context.Context, string, sqlite.MigrateOptions) error
	finalize func(context.Context, string) (string, error)
}

func defaultDatabaseRecoveryIO() databaseRecoveryIO {
	return databaseRecoveryIO{
		write: fileconfig.WriteAtomically, migrate: sqlite.Migrate, finalize: sqlite.FinalizeRecoveryDirectory,
	}
}

func recoveryStoragePaths(options Options) (string, string, error) {
	if options.UserConfigDir == nil || options.AppVersion == "" {
		return "", "", errors.New("database recovery configuration unavailable")
	}
	root, err := options.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	directory, path, err := native.StoragePaths(root)
	if err != nil {
		return "", "", err
	}
	path, err = resolveDesktopDatabase(directory, path)
	return directory, path, err
}

func prepareDatabaseRecovery(
	ctx context.Context,
	options Options,
	storage databaseRecoveryIO,
) (initializer bridge.Initializer, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = classifyStartupFailure(resultErr)
		}
	}()
	directory, source, err := recoveryStoragePaths(options)
	if err != nil {
		return nil, err
	}
	ownership, err := fileconfig.TryAcquireExclusiveLock(ctx, filepath.Join(directory, "desktop-executor.lock"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, ownership.Close())
		}
	}()
	defer func() {
		if resultErr != nil && initializer != nil {
			resultErr = &bridge.DatabaseActivationUncertain{Cause: resultErr}
			initializer = nil
		}
	}()
	// Resolve again under ownership: a different desktop may have recovered since
	// this window failed. A current or damaged database must not be reset here.
	_, source, err = recoveryStoragePaths(options)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("recovery source is not a regular database file")
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("lock recovery source: %w", err)
	}
	defer func() {
		_, err := conn.ExecContext(context.Background(), "ROLLBACK")
		resultErr = errors.Join(resultErr, err)
	}()
	version, err := sqlite.SchemaVersion(ctx, source)
	schemaIncompatible := errors.Is(err, sqlite.ErrSchemaResetRequired)
	if err != nil && !schemaIncompatible {
		return nil, err
	}
	if err == nil && (version == 0 || version >= sqlite.CurrentSchemaVersion) {
		return nil, errors.New("database is no longer an incompatible recovery candidate; reopen the application")
	}
	backupDirectory, err := os.MkdirTemp(directory, "database-recovery-")
	if err != nil {
		return nil, err
	}
	bridge.ReportStartupProgress(ctx, "database_backing_up", 0, 0)
	backup, err := sqlite.BackupIncompatibleDatabase(ctx, source, filepath.Join(backupDirectory, "original.sqlite"))
	if err != nil {
		return nil, err
	}
	if !schemaIncompatible {
		if err := verifyMigrationIncompatibility(ctx, backupDirectory, options.AppVersion); err != nil {
			return nil, err
		}
	}
	relativeSource, err := filepath.Rel(directory, source)
	if err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(struct {
		SchemaVersion int                   `json:"schema_version"`
		CreatedAt     string                `json:"created_at"`
		AppVersion    string                `json:"app_version"`
		File          string                `json:"file"`
		Source        string                `json:"source_database"`
		Backup        sqlite.RecoveryBackup `json:"backup"`
	}{
		SchemaVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AppVersion: options.AppVersion, File: "original.sqlite", Backup: backup,
		Source: filepath.ToSlash(relativeSource),
	})
	if err != nil {
		return nil, err
	}
	if err := storage.write(ctx, filepath.Join(backupDirectory, "backup.json"), manifest); err != nil {
		return nil, err
	}
	backupDirectory, err = storage.finalize(ctx, backupDirectory)
	if err != nil {
		return nil, err
	}
	freshDirectory, err := os.MkdirTemp(directory, "database-workspace-")
	if err != nil {
		return nil, err
	}
	freshPath := filepath.Join(freshDirectory, "llm-test-studio.db")
	bridge.ReportStartupProgress(ctx, "database_initializing", 0, sqlite.CurrentSchemaVersion)
	if err := storage.migrate(ctx, freshPath, sqlite.MigrateOptions{AppVersion: options.AppVersion}); err != nil {
		return nil, fmt.Errorf("initialize fresh database: %w", err)
	}
	bridge.ReportStartupProgress(ctx, "database_validating", 0, sqlite.CurrentSchemaVersion)
	if version, err := sqlite.SchemaVersion(ctx, freshPath); err != nil || version != sqlite.CurrentSchemaVersion {
		return nil, errors.New("fresh database validation failed")
	}
	freshDirectory, err = storage.finalize(ctx, freshDirectory)
	if err != nil {
		return nil, err
	}
	selection, err := json.Marshal(databaseSelection{
		SchemaVersion: 1, DatabaseDirectory: filepath.Base(freshDirectory), BackupDirectory: filepath.Base(backupDirectory),
	})
	if err != nil {
		return nil, err
	}
	bridge.ReportStartupProgress(ctx, "database_activating", 0, sqlite.CurrentSchemaVersion)
	currentSource, err := os.Stat(source)
	if err != nil || !os.SameFile(info, currentSource) {
		return nil, errors.New("recovery source file changed before activation")
	}
	if err := storage.write(ctx, filepath.Join(directory, databaseSelectionName), selection); err != nil {
		current, exists, readErr := readDatabaseSelection(directory)
		published := exists && current.DatabaseDirectory == filepath.Base(freshDirectory)
		if readErr != nil || published {
			return nil, &bridge.DatabaseActivationUncertain{Cause: err}
		}
		return nil, err
	}
	return newInitializer(options, ownership), nil
}

// Reproduce content incompatibility on a disposable snapshot under desktop
// ownership. A changed, now-compatible source must never be switched away.
func verifyMigrationIncompatibility(ctx context.Context, backupDirectory, appVersion string) (resultErr error) {
	probeDirectory, err := os.MkdirTemp(backupDirectory, "migration-probe-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(probeDirectory)) }()
	probe := filepath.Join(probeDirectory, "probe.sqlite")
	if _, err := sqlite.BackupIncompatibleDatabase(ctx, filepath.Join(backupDirectory, "original.sqlite"), probe); err != nil {
		return err
	}
	err = sqlite.Migrate(ctx, probe, sqlite.MigrateOptions{AppVersion: appVersion})
	if errors.Is(err, sqlite.ErrMigrationDataIncompatible) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recheck historical data incompatibility: %w", err)
	}
	return errors.New("database is no longer an incompatible recovery candidate; reopen the application")
}
