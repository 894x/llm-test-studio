package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// BackupRunnerUpgradeDatabase preserves a supported original schema, including
// committed WAL data. Historical schemas are rehearsed on a disposable copy;
// neither their backup nor their source is migrated in place.
func BackupRunnerUpgradeDatabase(ctx context.Context, sourcePath, destinationPath string) (RunnerBackup, error) {
	backup, err := backupRunnerDatabase(ctx, sourcePath, destinationPath, runnerUpgradeSnapshotFacts)
	if err != nil {
		return RunnerBackup{}, err
	}
	if backup.SchemaVersion == CurrentSchemaVersion {
		return backup, nil
	}
	created, err := os.Lstat(destinationPath)
	if err != nil {
		return RunnerBackup{}, err
	}
	if err := PreflightRunnerUpgradeDatabase(ctx, destinationPath); err != nil {
		if current, statErr := os.Lstat(destinationPath); statErr == nil && os.SameFile(created, current) {
			_ = os.Remove(destinationPath)
		}
		return RunnerBackup{}, err
	}
	return backup, nil
}

// VerifyRunnerUpgradeDatabase validates backup facts in their original version.
// It does not initialize, migrate, or reinterpret historical business documents.
// Current-schema backups retain VerifyRunnerDatabase's full validation contract.
func VerifyRunnerUpgradeDatabase(ctx context.Context, path string) (RunnerBackup, error) {
	return verifyRunnerDatabase(ctx, path, runnerUpgradeSnapshotFacts)
}

// PreflightRunnerUpgradeDatabase checks compatibility before admission or service
// state changes. Only a private, disposable snapshot enters the shipped Migrate
// implementation; its recoverable migration backups are disposed of with it.
func PreflightRunnerUpgradeDatabase(ctx context.Context, path string) error {
	source, err := openRunnerBackupSource(ctx, path)
	if err != nil {
		return err
	}
	defer source.close()
	state, err := inspectRunnerUpgradeState(ctx, source.conn)
	if err != nil {
		return runnerBackupError(ctx, "inspect upgrade source", err)
	}
	if state.version == CurrentSchemaVersion {
		if _, err := runnerSnapshotFacts(ctx, source.conn); err != nil {
			return runnerBackupError(ctx, "validate upgrade source", err)
		}
		return source.verifyIdentity()
	}
	if err := validateIntegrity(ctx, source.conn); err != nil {
		return runnerBackupError(ctx, "validate historical upgrade source", err)
	}
	directory, err := os.MkdirTemp("", "llm-test-runner-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	preview := filepath.Join(directory, "preview.sqlite")
	file, err := os.OpenFile(preview, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := copyRunnerSnapshot(ctx, source.conn, preview); err != nil {
		return runnerBackupError(ctx, "copy upgrade preview", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := source.verifyIdentity(); err != nil {
		return err
	}
	// Release the live WAL snapshot before the more expensive migration rehearsal.
	source.close()
	if err := Migrate(ctx, preview, MigrateOptions{AppVersion: "runner-upgrade-preflight"}); err != nil {
		if errors.Is(err, ErrMigrationDataIncompatible) {
			return fmt.Errorf("%w: runner upgrade preview", ErrMigrationDataIncompatible)
		}
		return runnerBackupError(ctx, "migrate upgrade preview", err)
	}
	// The v1 transformation already checks every target document and relation
	// through runnerSnapshotFacts before committing. Repeating the same deep
	// scan of this private preview adds no evidence. Additive v2/v3 migrations
	// intentionally skip that scan, so they still need the check below.
	if state.version == 1 {
		return nil
	}
	if _, err := VerifyRunnerDatabase(ctx, preview); err != nil {
		return err
	}
	return nil
}

func inspectRunnerUpgradeState(ctx context.Context, conn *sql.Conn) (migrationState, error) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		return migrationState{}, err
	}
	state, err := inspectMigrationState(ctx, conn, catalog)
	if err != nil {
		return migrationState{}, err
	}
	if state.version == 0 {
		return migrationState{}, ErrSchemaResetRequired
	}
	return state, nil
}

func runnerUpgradeSnapshotFacts(ctx context.Context, conn *sql.Conn) (RunnerBackup, error) {
	state, err := inspectRunnerUpgradeState(ctx, conn)
	if err != nil {
		return RunnerBackup{}, err
	}
	if state.version == CurrentSchemaVersion {
		return runnerSnapshotFacts(ctx, conn)
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return RunnerBackup{}, err
	}
	objects := state.baseline.objects
	facts := RunnerBackup{
		SchemaVersion: state.version, SchemaName: state.baseline.Name, SchemaSHA256: state.baseline.Checksum,
	}
	switch state.version {
	case 2:
		objects = schemaV2Objects
		facts.SchemaName, facts.SchemaSHA256 = schemaV2Name, schemaV2Checksum()
	case 3:
		objects = schemaV3Objects
		facts.SchemaName, facts.SchemaSHA256 = schemaV3Name, schemaV3Checksum()
	}
	// Reuse the migration preservation digest without decoding old documents
	// against today's domain. Include the original ledger and sequence values.
	tables, err := migrationFacts(ctx, conn, objects, false)
	if err != nil {
		return RunnerBackup{}, err
	}
	digest := sha256.New()
	for _, table := range tables {
		writeRunnerDigest(digest, 'T', []byte(table.table))
		for _, column := range table.columns {
			writeRunnerDigest(digest, 'C', []byte(column))
		}
		writeRunnerDigest(digest, 'D', []byte(table.digest))
		var count int64
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteMigrationName(table.table)).Scan(&count); err != nil {
			return RunnerBackup{}, err
		}
		facts.Rows.add(table.table, count)
	}
	facts.BusinessSHA256 = hex.EncodeToString(digest.Sum(nil))
	return facts, nil
}
