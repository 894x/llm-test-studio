package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestQuickHistoryCandidateQueryUsesBoundedPerformanceIndex(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(t.Context(), `
		CREATE TABLE execution_runs (id TEXT PRIMARY KEY, current_revision INTEGER, created_at TEXT);
		CREATE TABLE execution_run_revisions (run_id TEXT, revision INTEGER, snapshot_json TEXT, PRIMARY KEY(run_id,revision));
		CREATE TABLE quick_performance_reports (id TEXT PRIMARY KEY, generated_at TEXT, generated_at_unix_nano INTEGER);
		CREATE INDEX idx_quick_performance_reports_generated ON quick_performance_reports(generated_at_unix_nano DESC,id DESC);`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+quickHistoryIDQuery,
		"2026-01-01T00:00:00.000000000Z", "run", "70000000-0000-4000-8000-000000000001", 26, int64(1767225600000000000))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	details := strings.Join(plan, "\n")
	if !strings.Contains(details, "SEARCH quick_performance_reports USING INDEX idx_quick_performance_reports_generated (generated_at_unix_nano<?)") {
		t.Fatalf("performance keyset must seek the existing time index:\n%s", details)
	}
	if strings.Contains(details, "SCAN revision") {
		t.Fatalf("history must look up current revisions only:\n%s", details)
	}
}
