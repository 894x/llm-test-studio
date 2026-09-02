package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestMigrateAddsIndependentQuickPerformanceReportsSchemaV6(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v6.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v6"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	if !tableExists(t, db, "quick_performance_reports") {
		t.Fatal("migration did not create quick_performance_reports")
	}
	if got := tableColumns(t, db, "quick_performance_reports"); !equalStrings(got, []string{
		"id", "generated_at", "generated_at_unix_nano", "success", "model_id", "base_url", "phase", "completed", "failed", "document_json",
	}) {
		t.Fatalf("quick_performance_reports columns = %v", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 6 AND name = '0006_quick_performance_reports'`); got != 1 {
		t.Fatalf("quick performance migration rows = %d, want 1", got)
	}

	// Quick reports are deliberately independent from plans, runs, and formal
	// reports. A fresh database can archive one without creating catalog rows.
	if _, err := db.Exec(`INSERT INTO quick_performance_reports(id, generated_at, generated_at_unix_nano, success, model_id, base_url, phase, completed, failed, document_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"77777777-7777-4777-8777-777777777777", "2026-08-31T15:30:00Z", int64(1788190200000000000), 1, "model", "https://example.com/v1", "completed", 1, 0, `{"report_id":"77777777-7777-4777-8777-777777777777"}`); err != nil {
		t.Fatalf("insert independent quick report: %v", err)
	}
	for _, table := range []string{"models", "channels", "test_plans", "execution_runs", "reports"} {
		if got := queryInt(t, db, "SELECT COUNT(*) FROM "+table); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
}
