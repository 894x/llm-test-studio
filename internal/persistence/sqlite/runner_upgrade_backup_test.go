package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunnerUpgradeBackupSupportsEveryPublishedMigrationSource(t *testing.T) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]func(*testing.T) string{
		"v2": func(t *testing.T) string { return queryIndexesV2Fixture(t) },
		"v3": func(t *testing.T) string { return historyRecycleV3Fixture(t) },
		"current": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "current.db")
			if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "upgrade-backup-test"}); err != nil {
				t.Fatal(err)
			}
			return path
		},
	}
	for _, baseline := range catalog.Baselines {
		fixtures["v1-"+baseline.Commit[:8]] = func(t *testing.T) string {
			return historicalMigrationFixture(t, baseline)
		}
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := fixture(t)
			version, err := SchemaVersion(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			before := migrationFileHash(t, path)
			if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "runner.db")
			proof, err := BackupRunnerUpgradeDatabase(t.Context(), path, destination)
			if err != nil {
				t.Fatal(err)
			}
			if proof.SchemaVersion != version || proof.DatabaseSHA256 != migrationFileHash(t, destination) {
				t.Fatalf("backup lost original format or file digest: %+v", proof)
			}
			verified, err := VerifyRunnerUpgradeDatabase(t.Context(), destination)
			if err != nil || !reflect.DeepEqual(proof, verified) {
				t.Fatalf("repeat verification: %+v %v", verified, err)
			}
			if version == CurrentSchemaVersion {
				current, err := VerifyRunnerDatabase(t.Context(), destination)
				if err != nil || !reflect.DeepEqual(proof, current) {
					t.Fatalf("current backup journal contract changed: %+v %v", current, err)
				}
			} else {
				originalReport := readMigrationText(t, path, "SELECT document_json FROM reports WHERE id = ?", historicalReportID)
				backupReport := readMigrationText(t, destination, "SELECT document_json FROM reports WHERE id = ?", historicalReportID)
				if backupReport != originalReport {
					t.Fatal("backup reinterpreted a historical report")
				}
				if _, err := BackupRunnerDatabase(t.Context(), path, filepath.Join(t.TempDir(), "current-only.db")); !errors.Is(err, ErrSchemaResetRequired) {
					t.Fatalf("current-only backup contract changed: %v", err)
				}
			}
			if _, err := BackupRunnerUpgradeDatabase(t.Context(), path, destination); !errors.Is(err, ErrConflict) {
				t.Fatalf("existing upgrade backup was replaced: %v", err)
			}
			if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err != nil {
				t.Fatal(err)
			}
			if migrationFileHash(t, path) != before || len(migrationBackups(t, path)) != 0 {
				t.Fatal("upgrade preflight changed the source or published migration backups beside it")
			}
			if got, err := SchemaVersion(t.Context(), path); err != nil || got != version {
				t.Fatalf("source version changed: %d %v", got, err)
			}
			// Restoring the original-version backup remains a normal supported
			// upgrade, including skipping releases and reopening after completion.
			if err := Migrate(t.Context(), destination, MigrateOptions{AppVersion: "restored-upgrade-backup"}); err != nil {
				t.Fatal(err)
			}
			if version != CurrentSchemaVersion {
				assertMigratedHistoryReadable(t, destination)
			}
			if err := Migrate(t.Context(), destination, MigrateOptions{AppVersion: "repeat-open"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunnerUpgradeBackupReadsHistoricalWALWithoutBlockingItsWriter(t *testing.T) {
	catalog, _ := loadMigrationCatalog()
	path := historicalMigrationFixture(t, catalog.Baselines[0])
	db := migrationTestDB(t, path)
	defer db.Close()
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA wal_autocheckpoint = 0",
		"INSERT INTO runs(id, config) VALUES(1001, 'committed-wal-only')",
		"BEGIN IMMEDIATE",
		"INSERT INTO runs(id, config) VALUES(1002, 'uncommitted-writer')",
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	defer db.ExecContext(context.Background(), "ROLLBACK")
	mainBefore, walBefore := migrationFileHash(t, path), migrationFileHash(t, path+"-wal")
	if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "runner.db")
	backup, err := BackupRunnerUpgradeDatabase(t.Context(), path, destination)
	if err != nil || backup.SchemaVersion != 1 {
		t.Fatalf("historical WAL backup: %+v %v", backup, err)
	}
	if got := readMigrationText(t, destination, "SELECT config FROM runs WHERE id=1001"); got != "committed-wal-only" {
		t.Fatal("backup omitted committed WAL data")
	}
	if got := readMigrationText(t, destination, "SELECT CAST(COUNT(*) AS TEXT) FROM runs WHERE id=1002"); got != "0" {
		t.Fatal("backup included an uncommitted writer")
	}
	if mainBefore != migrationFileHash(t, path) || walBefore != migrationFileHash(t, path+"-wal") {
		t.Fatal("preflight or backup modified the live source")
	}
	if _, err := db.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO runs(id, config) VALUES(1003, 'after-backup')"); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyRunnerUpgradeDatabase(t.Context(), destination)
	if err != nil || !reflect.DeepEqual(backup, verified) {
		t.Fatalf("later source writes changed the backup: %v", err)
	}
}

func TestRunnerUpgradePreflightRejectsUnsupportedAndIncompatibleSourcesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name, statement string
	}{
		{name: "newer", statement: "PRAGMA user_version = 999"},
		{name: "unknown-baseline", statement: "UPDATE schema_migrations SET checksum = 'unknown'"},
		{name: "changed-structure", statement: "CREATE TABLE unknown_format(value TEXT)"},
		{name: "foreign-key-corruption", statement: "DELETE FROM execution_runs"},
		{name: "incompatible-report", statement: "UPDATE reports SET document_json = '{}'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := loadMigrationCatalog()
			path := historicalMigrationFixture(t, catalog.Baselines[0])
			db := migrationTestDB(t, path)
			if _, err := db.ExecContext(t.Context(), test.statement); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := migrationFileHash(t, path)
			if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err == nil {
				t.Fatal("unsupported or incompatible source passed preflight")
			}
			destination := filepath.Join(t.TempDir(), "rejected.db")
			if _, err := BackupRunnerUpgradeDatabase(t.Context(), path, destination); err == nil {
				t.Fatal("unsupported or incompatible source was backed up as upgradeable")
			}
			if migrationFileHash(t, path) != before || len(migrationBackups(t, path)) != 0 {
				t.Fatal("rejected source was changed")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed upgrade left a published backup: %v", err)
			}
		})
	}
	t.Run("physical-corruption", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.db")
		if err := os.WriteFile(path, []byte("physically invalid SQLite file"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := migrationFileHash(t, path)
		if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err == nil || migrationFileHash(t, path) != before {
			t.Fatal("corrupt source was accepted or changed")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		catalog, _ := loadMigrationCatalog()
		path := historicalMigrationFixture(t, catalog.Baselines[0])
		before := migrationFileHash(t, path)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := PreflightRunnerUpgradeDatabase(ctx, path); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled preflight: %v", err)
		}
		if migrationFileHash(t, path) != before {
			t.Fatal("cancelled preflight changed its source")
		}
	})
}

func TestRunnerUpgradePreflightRejectsIncompatibleDocumentsInEverySupportedVersion(t *testing.T) {
	fixtures := map[string]func(*testing.T) string{
		"v1": func(t *testing.T) string {
			catalog, _ := loadMigrationCatalog()
			return historicalMigrationFixture(t, catalog.Baselines[len(catalog.Baselines)-1])
		},
		"v2": func(t *testing.T) string { return queryIndexesV2Fixture(t) },
		"v3": func(t *testing.T) string { return historyRecycleV3Fixture(t) },
		"current": func(t *testing.T) string {
			path := historyRecycleV3Fixture(t)
			if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "current-preflight-test"}); err != nil {
				t.Fatal(err)
			}
			return path
		},
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := fixture(t)
			db := migrationTestDB(t, path)
			if _, err := db.ExecContext(t.Context(), "UPDATE reports SET document_json = '{}'"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := migrationFileHash(t, path)
			if err := PreflightRunnerUpgradeDatabase(t.Context(), path); err == nil {
				t.Fatal("preflight accepted an incompatible historical report")
			}
			if migrationFileHash(t, path) != before {
				t.Fatal("rejected preflight changed its source")
			}
		})
	}
}
