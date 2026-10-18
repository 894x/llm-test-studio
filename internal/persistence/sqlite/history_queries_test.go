package sqlite

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryQueriesSeekTimeIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "indexed.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "query-plan"}); err != nil {
		t.Fatal(err)
	}
	db := migrationTestDB(t, path)
	defer db.Close()
	time := "2026-01-01T00:00:00.000000000Z"
	id := "70000000-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name, query, expected string
		args                  []any
	}{
		{"runs", runHistoryIDsQuery, "SEARCH root USING INDEX idx_runs_history (<expr><?)", []any{time, id, 26}},
		{"comparisons", comparisonHistoryIDsQuery, "SEARCH comparisons USING INDEX idx_comparisons_history (<expr><?)", []any{time, id}},
		{"quick", quickHistoryIDQuery, "SEARCH root USING INDEX idx_runs_history (<expr><?)", []any{time, "run", id, 26, int64(1767225600000000000)}},
		{"reports", reportProjectionQuery, "USING INDEX idx_reports_history", []any{100, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+test.query, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			plan := ""
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				plan += fmt.Sprintln(detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ReplaceAll(plan, "USING COVERING INDEX", "USING INDEX"), test.expected) {
				t.Fatalf("missing index range: %s", plan)
			}
			if test.name == "quick" && !strings.Contains(plan, "idx_quick_run_revisions") {
				t.Fatalf("missing quick predicate index: %s", plan)
			}
		})
	}
}
