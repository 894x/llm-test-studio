package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	persistence "github.com/894x/llm-studio/internal/persistence/sqlite"
)

func TestMigrateFreshDatabaseAppliesDomainSchemaV2(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fresh-v2.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v2"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	db := openDatabase(t, path)
	defer db.Close()

	if got := queryInt(t, db, "PRAGMA user_version"); got != 3 {
		t.Fatalf("user_version = %d, want 3", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 3 {
		t.Fatalf("migration count = %d, want 3", got)
	}
	for _, table := range []string{
		"models", "channels", "channel_models", "credential_refs", "test_cases",
		"test_suites", "suite_cases", "test_plans", "plan_models", "plan_channels", "plan_cases", "plan_channel_models", "execution_runs", "execution_run_revisions",
		"case_results", "evidence", "reports", "report_attachments", "artifacts", "integrations",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("domain table %q does not exist", table)
		}
	}
	for _, legacyTable := range []string{"runs", "run_results", "audit_runs", "audit_case_results"} {
		if !tableExists(t, db, legacyTable) {
			t.Errorf("legacy table %q was not preserved", legacyTable)
		}
	}
	for _, trigger := range []string{
		"trg_execution_run_revisions_no_update",
		"trg_execution_run_revisions_no_delete",
		"trg_execution_run_revisions_contiguous_insert",
	} {
		if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = '`+trigger+`'`); got != 1 {
			t.Errorf("domain trigger %q count = %d, want 1", trigger, got)
		}
	}
}

func TestMigrateUpgrades0001WithoutChangingLegacyRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "upgrade-v1.db")
	createMigration0001Database(t, path)
	db := openDatabase(t, path)
	if _, err := db.Exec(`INSERT INTO runs(id, model, requests) VALUES(42, 'legacy-preserved', 9)`); err != nil {
		db.Close()
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close 0001 database: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	if got := queryString(t, db, "SELECT model FROM runs WHERE id = 42"); got != "legacy-preserved" {
		t.Fatalf("legacy model = %q, want preserved", got)
	}
	if got := queryInt(t, db, "SELECT requests FROM runs WHERE id = 42"); got != 9 {
		t.Fatalf("legacy requests = %d, want 9", got)
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != 3 {
		t.Fatalf("user_version = %d, want 3", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 3 {
		t.Fatalf("migration count = %d, want 3", got)
	}
}

func TestMigrateV2IsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "idempotent-v2.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	before := migrationHistory(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"}); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	after := migrationHistory(t, db)
	if before != after {
		t.Fatalf("migration history changed: before=%q after=%q", before, after)
	}
}

func TestMigrateRejectsV2ChecksumAndSchemaDrift(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "checksum-v2.db")
		if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
			t.Fatalf("initial Migrate() error = %v", err)
		}
		db := openDatabase(t, path)
		if _, err := db.Exec("UPDATE schema_migrations SET checksum = 'tampered-v2' WHERE version = 2"); err != nil {
			db.Close()
			t.Fatalf("tamper checksum: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close tampered database: %v", err)
		}

		err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
		if err == nil || !strings.Contains(err.Error(), "0002 checksum mismatch") {
			t.Fatalf("Migrate() error = %v, want 0002 checksum mismatch", err)
		}
		db = openDatabase(t, path)
		defer db.Close()
		if got := queryString(t, db, "SELECT checksum FROM schema_migrations WHERE version = 2"); got != "tampered-v2" {
			t.Fatalf("checksum = %q, want tampered-v2 preserved", got)
		}
	})

	t.Run("schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "schema-drift-v2.db")
		if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
			t.Fatalf("initial Migrate() error = %v", err)
		}
		db := openDatabase(t, path)
		if _, err := db.Exec("ALTER TABLE models ADD COLUMN unexpected TEXT"); err != nil {
			db.Close()
			t.Fatalf("inject schema drift: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close drifted database: %v", err)
		}

		err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
		if err == nil || !strings.Contains(err.Error(), "schema drift") || !strings.Contains(err.Error(), "models") {
			t.Fatalf("Migrate() error = %v, want models schema drift", err)
		}
		db = openDatabase(t, path)
		defer db.Close()
		if got := tableColumnType(t, db, "models", "unexpected"); got != "TEXT" {
			t.Fatalf("unexpected column type = %q, want preserved TEXT", got)
		}
	})

	t.Run("trigger", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "trigger-drift-v2.db")
		if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
			t.Fatalf("initial Migrate() error = %v", err)
		}
		db := openDatabase(t, path)
		if _, err := db.Exec("DROP TRIGGER trg_execution_run_revisions_no_delete"); err != nil {
			db.Close()
			t.Fatalf("drop trigger: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close drifted database: %v", err)
		}

		err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
		if err == nil || !strings.Contains(err.Error(), "missing trigger") || !strings.Contains(err.Error(), "no_delete") {
			t.Fatalf("Migrate() error = %v, want missing trigger drift", err)
		}
	})
}

func TestMigrateV2FailureRollsBackAllV2ObjectsAndHistory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rollback-v2.db")
	createMigration0001Database(t, path)
	db := openDatabase(t, path)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		db.Close()
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := db.Exec("INSERT INTO run_results(id, run_id, request_id) VALUES(88, 999, 1)"); err != nil {
		db.Close()
		t.Fatalf("insert orphan legacy result: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close 0001 database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade"})
	if err == nil || !strings.Contains(err.Error(), "foreign key check failed") {
		t.Fatalf("Migrate() error = %v, want foreign key integrity failure", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	if tableExists(t, db, "models") {
		t.Fatal("failed migration left v2 table models")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'trg_execution_run_revisions_%'`); got != 0 {
		t.Fatalf("failed migration left %d run revision triggers", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 1 {
		t.Fatalf("migration count = %d, want rolled back to 1", got)
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != 1 {
		t.Fatalf("user_version = %d, want rolled back to 1", got)
	}
	if got := queryInt(t, db, "SELECT run_id FROM run_results WHERE id = 88"); got != 999 {
		t.Fatalf("legacy orphan run id = %d, want preserved 999", got)
	}
}

func createMigration0001Database(t *testing.T, path string) {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v1"}); err != nil {
		t.Fatalf("seed v2 database: %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		db.Close()
		t.Fatalf("disable foreign keys while constructing v1 fixture: %v", err)
	}
	for _, table := range []string{
		"test_case_import_sources",
		"report_attachments", "artifacts", "reports", "case_results", "evidence", "execution_run_revisions", "execution_runs",
		"plan_channel_models", "plan_cases", "plan_channels", "plan_models", "test_plans", "suite_cases", "test_suites",
		"channel_models", "integrations", "channels", "credential_refs", "test_cases", "models",
	} {
		if _, err := db.Exec("DROP TABLE " + table); err != nil {
			db.Close()
			t.Fatalf("drop v2 table %s: %v", table, err)
		}
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version >= 2"); err != nil {
		db.Close()
		t.Fatalf("remove post-v1 migration records: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		db.Close()
		t.Fatalf("set v1 user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v1 fixture: %v", err)
	}
}

func migrationHistory(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT version, name, checksum, applied_at, app_version
		FROM schema_migrations ORDER BY version
	`)
	if err != nil {
		t.Fatalf("query migration history: %v", err)
	}
	defer rows.Close()
	var result strings.Builder
	for rows.Next() {
		var version int
		var name, checksum, appliedAt, appVersion string
		if err := rows.Scan(&version, &name, &checksum, &appliedAt, &appVersion); err != nil {
			t.Fatalf("scan migration history: %v", err)
		}
		result.WriteString(name)
		result.WriteByte('|')
		result.WriteString(checksum)
		result.WriteByte('|')
		result.WriteString(appliedAt)
		result.WriteByte('|')
		result.WriteString(appVersion)
		result.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration history: %v", err)
	}
	return result.String()
}
