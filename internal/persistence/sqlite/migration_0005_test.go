package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	persistence "github.com/894x/llm-studio/internal/persistence/sqlite"
)

func dropMigration0005Objects(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"quick_performance_reports", "comparison_runs", "comparisons", "comparison_revisions", "pending_test_case_snapshots", "case_catalog_cutover"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatalf("drop v5 table %s: %v", table, err)
		}
	}
}

func TestMigrateAddsChannelComparisonSchemaV5(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v5.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v5"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	for _, table := range []string{"case_catalog_cutover", "pending_test_case_snapshots", "comparisons", "comparison_revisions", "comparison_runs"} {
		if !tableExists(t, db, table) {
			t.Fatalf("migration did not create %s", table)
		}
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 5 AND name = '0005_channel_comparisons'`); got != 1 {
		t.Fatalf("comparison migration rows = %d, want 1", got)
	}
}
