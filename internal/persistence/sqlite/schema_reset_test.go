package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestMigrateFreshDatabaseCreatesOperationalSchemaV1(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "operational-v1.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{
		AppVersion: "schema-reset-test",
	}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	db := openDatabase(t, path)
	defer db.Close()

	if got := queryInt(t, db, "PRAGMA user_version"); got != 1 {
		t.Fatalf("PRAGMA user_version = %d, want 1", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 1 {
		t.Fatalf("schema migration count = %d, want 1", got)
	}
	for _, table := range []string{
		"runs",
		"run_results",
		"audit_runs",
		"audit_case_results",
		"execution_runs",
		"execution_run_revisions",
		"evidence",
		"case_results",
		"reports",
		"artifacts",
		"report_attachments",
		"comparisons",
		"comparison_revisions",
		"comparison_runs",
		"quick_performance_reports",
	} {
		if !tableExists(t, db, table) {
			t.Fatalf("fresh operational schema is missing table %q", table)
		}
	}
	for _, table := range []string{
		"models",
		"credential_refs",
		"channels",
		"channel_models",
		"test_cases",
		"test_suites",
		"test_plans",
		"integrations",
		"test_case_import_sources",
		"catalog_tombstones",
		"case_catalog_cutover",
		"pending_test_case_snapshots",
		"builtin_catalog_seeds",
	} {
		if tableExists(t, db, table) {
			t.Fatalf("fresh operational schema contains authored table %q", table)
		}
	}
}

func TestMigrateOperationalSchemaV1IsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "operational-v1.db")
	for _, appVersion := range []string{"schema-reset-test", "schema-reset-test-second-open"} {
		if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: appVersion}); err != nil {
			t.Fatalf("Migrate(%q) error = %v", appVersion, err)
		}
	}
	if version, err := persistence.SchemaVersion(context.Background(), path); err != nil || version != 1 {
		t.Fatalf("SchemaVersion() = %d, %v; want 1", version, err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMigrateRejectsPreResetMigrationHistory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "old-schema.db")
	db := openDatabase(t, path)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, checksum TEXT NOT NULL, applied_at TEXT NOT NULL, app_version TEXT NOT NULL)`); err != nil {
		t.Fatalf("create old migration table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(11, '0011_retire_authored_catalog', 'old', '2026-09-06T00:00:00Z', 'old')`); err != nil {
		t.Fatalf("insert old migration row: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 11`); err != nil {
		t.Fatalf("set old user version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "schema-reset-test"})
	if !errors.Is(err, persistence.ErrSchemaResetRequired) || !strings.Contains(err.Error(), "reset the operational database") {
		t.Fatalf("Migrate(old schema) error = %v, want reset-required error", err)
	}
}

func TestMigrateClassifiesEveryIncompatibleSchemaAsResetRequired(t *testing.T) {
	t.Parallel()

	t.Run("legacy v1 identity", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-v1.db")
		db := openDatabase(t, path)
		if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, checksum TEXT NOT NULL, applied_at TEXT NOT NULL, app_version TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES(1, '0001_legacy_baseline', 'legacy', '2026-09-06T00:00:00Z', 'dev')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		requireSchemaResetRequired(t, path)

		db = openDatabase(t, path)
		defer db.Close()
		if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE name = '0001_legacy_baseline'`); got != 1 {
			t.Fatalf("legacy migration row count after rejection = %d, want 1", got)
		}
		if tableExists(t, db, "execution_runs") {
			t.Fatal("failed rejection partially installed the operational schema")
		}
	})

	for _, test := range []struct {
		name   string
		tamper string
		check  func(*testing.T, string)
	}{
		{
			name:   "checksum mismatch",
			tamper: `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`,
			check: func(t *testing.T, path string) {
				db := openDatabase(t, path)
				defer db.Close()
				if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE checksum = 'tampered'`); got != 1 {
					t.Fatalf("tampered checksum row count after rejection = %d, want 1", got)
				}
			},
		},
		{
			name:   "user version mismatch",
			tamper: `PRAGMA user_version = 99`,
			check: func(t *testing.T, path string) {
				db := openDatabase(t, path)
				defer db.Close()
				if got := queryInt(t, db, `PRAGMA user_version`); got != 99 {
					t.Fatalf("user_version after rejection = %d, want 99", got)
				}
			},
		},
		{
			name:   "missing object",
			tamper: `DROP INDEX idx_execution_run_revisions_updated`,
			check: func(t *testing.T, path string) {
				db := openDatabase(t, path)
				defer db.Close()
				if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'idx_execution_run_revisions_updated'`); got != 0 {
					t.Fatalf("missing index was recreated after rejection")
				}
			},
		},
		{
			name:   "unexpected object",
			tamper: `CREATE TABLE legacy_authored_data(id TEXT PRIMARY KEY)`,
			check: func(t *testing.T, path string) {
				db := openDatabase(t, path)
				defer db.Close()
				if !tableExists(t, db, "legacy_authored_data") {
					t.Fatal("unexpected table was removed after rejection")
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "incompatible.db")
			if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "schema-reset-test"}); err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t, path)
			if _, err := db.Exec(test.tamper); err != nil {
				db.Close()
				t.Fatalf("tamper schema: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			requireSchemaResetRequired(t, path)
			test.check(t, path)
		})
	}
}

func requireSchemaResetRequired(t *testing.T, path string) {
	t.Helper()
	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "schema-reset-test"})
	if !errors.Is(err, persistence.ErrSchemaResetRequired) || !strings.Contains(err.Error(), "reset the operational database") {
		t.Fatalf("Migrate(incompatible schema) error = %v, want reset-required classification and remediation", err)
	}
}
