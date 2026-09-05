package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const (
	v9ComparisonID = "99999999-9999-4999-8999-999999999991"
	v9PlanID       = "99999999-9999-4999-8999-999999999992"
	v9ModelID      = "99999999-9999-4999-8999-999999999993"
	v9ChannelID    = "99999999-9999-4999-8999-999999999994"
	v9RunID        = "99999999-9999-4999-8999-999999999995"
)

func TestMigrateV9ComparisonsDoNotRequireCatalogRows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v9.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v9"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()

	insertV9ExecutionRun(t, db, v9RunID, v9PlanID)
	if err := insertV9Comparison(t, db, false); err != nil {
		t.Fatalf("insert comparison without catalog rows: %v", err)
	}
	assertComparisonForeignKeysV9(t, db)
	assertComparisonGuardsV9(t, db)
	assertDatabaseIntegrity(t, db)
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 9 AND name = '0009_comparison_snapshot_catalog_decoupling'`); got != 1 {
		t.Fatalf("comparison snapshot migration rows = %d, want 1", got)
	}
}

func TestMigrateV9PreservesV8ComparisonsAndHistoricalChecksums(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v8.db")
	createMigration0008Fixture(t, path)
	db := openDatabase(t, path)
	insertV9CatalogRows(t, db)
	insertV9ExecutionRun(t, db, v9RunID, v9PlanID)
	if err := insertV9Comparison(t, db, true); err != nil {
		db.Close()
		t.Fatalf("insert v8 comparison: %v", err)
	}
	if !hasForeignKey(t, db, "comparison_revisions", "plan_id", "test_plans", "id") ||
		!hasForeignKey(t, db, "comparison_revisions", "model_id", "models", "id") ||
		!hasForeignKey(t, db, "comparison_runs", "channel_id", "channels", "id") {
		db.Close()
		t.Fatal("v8 fixture is missing catalog foreign keys")
	}
	beforeChecksums := migrationChecksums(t, db, 8)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v9"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	var schemaVersion, revision, planRevision, modelRevision int
	var planID, modelID, status, documentJSON string
	if err := db.QueryRow(`
		SELECT schema_version, revision, plan_id, plan_revision,
		       model_id, model_revision, status, document_json
		FROM comparison_revisions
		WHERE comparison_id = ? AND revision = 1
	`, v9ComparisonID).Scan(
		&schemaVersion, &revision, &planID, &planRevision,
		&modelID, &modelRevision, &status, &documentJSON,
	); err != nil {
		t.Fatalf("read preserved comparison revision: %v", err)
	}
	if schemaVersion != 1 || revision != 1 || planID != v9PlanID || planRevision != 1 ||
		modelID != v9ModelID || modelRevision != 1 || status != "running" || documentJSON != `{"source":"v8"}` {
		t.Fatalf("preserved comparison revision = (%d, %d, %q, %d, %q, %d, %q, %q)",
			schemaVersion, revision, planID, planRevision, modelID, modelRevision, status, documentJSON)
	}
	var position, channelRevision int
	var channelID, runID string
	if err := db.QueryRow(`
		SELECT position, channel_id, channel_revision, run_id
		FROM comparison_runs
		WHERE comparison_id = ? AND comparison_revision = 1
	`, v9ComparisonID).Scan(&position, &channelID, &channelRevision, &runID); err != nil {
		t.Fatalf("read preserved comparison run: %v", err)
	}
	if position != 0 || channelID != v9ChannelID || channelRevision != 1 || runID != v9RunID {
		t.Fatalf("preserved comparison run = (%d, %q, %d, %q)", position, channelID, channelRevision, runID)
	}
	if got := migrationChecksums(t, db, 8); !equalStrings(got, beforeChecksums) {
		t.Fatalf("historical migration checksums changed: before=%v after=%v", beforeChecksums, got)
	}
	assertComparisonForeignKeysV9(t, db)
	assertComparisonGuardsV9(t, db)
	assertDatabaseIntegrity(t, db)

	for _, statement := range []string{
		"DELETE FROM test_plans WHERE id = '" + v9PlanID + "'",
		"DELETE FROM models WHERE id = '" + v9ModelID + "'",
		"DELETE FROM channels WHERE id = '" + v9ChannelID + "'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("delete obsolete catalog row with %q: %v", statement, err)
		}
	}
	assertDatabaseIntegrity(t, db)
}

func createMigration0008Fixture(t *testing.T, path string) {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v8"}); err != nil {
		t.Fatal(err)
	}
	if persistence.CurrentSchemaVersion == 8 {
		return
	}
	db := openDatabase(t, path)
	defer db.Close()
	restoreComparisonTablesV8(t, db)
	for _, statement := range []string{
		"DELETE FROM schema_migrations WHERE version >= 9",
		"PRAGMA user_version = 8",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("construct v8 fixture with %q: %v", statement, err)
		}
	}
}

func restoreComparisonTablesV8(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	statements := []string{
		"PRAGMA foreign_keys = OFF",
		"DROP TRIGGER comparison_revisions_no_update",
		"DROP TRIGGER comparison_revisions_no_delete",
		"DROP INDEX idx_comparison_revisions_updated",
		"DROP TABLE comparison_runs",
		"DROP TABLE comparison_revisions",
		`CREATE TABLE comparison_revisions (
			comparison_id TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			plan_id TEXT NOT NULL,
			plan_revision INTEGER NOT NULL,
			model_id TEXT NOT NULL,
			model_revision INTEGER NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'cancelled')),
			document_json TEXT NOT NULL CHECK(json_valid(document_json)),
			PRIMARY KEY(comparison_id, revision),
			FOREIGN KEY(comparison_id) REFERENCES comparisons(id) ON DELETE CASCADE,
			FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision),
			FOREIGN KEY(model_id, model_revision) REFERENCES models(id, revision)
		)`,
		`CREATE TABLE comparison_runs (
			comparison_id TEXT NOT NULL,
			comparison_revision INTEGER NOT NULL,
			position INTEGER NOT NULL CHECK(position >= 0),
			channel_id TEXT NOT NULL,
			channel_revision INTEGER NOT NULL,
			run_id TEXT NOT NULL,
			PRIMARY KEY(comparison_id, comparison_revision, position),
			UNIQUE(comparison_id, comparison_revision, channel_id),
			UNIQUE(comparison_id, comparison_revision, run_id),
			FOREIGN KEY(comparison_id, comparison_revision) REFERENCES comparison_revisions(comparison_id, revision) ON DELETE CASCADE,
			FOREIGN KEY(channel_id, channel_revision) REFERENCES channels(id, revision),
			FOREIGN KEY(run_id) REFERENCES execution_runs(id)
		)`,
		"CREATE INDEX idx_comparison_revisions_updated ON comparison_revisions(updated_at DESC, comparison_id)",
		`CREATE TRIGGER comparison_revisions_no_update
		BEFORE UPDATE ON comparison_revisions
		BEGIN
			SELECT RAISE(ABORT, 'comparison revisions are immutable');
		END`,
		`CREATE TRIGGER comparison_revisions_no_delete
		BEFORE DELETE ON comparison_revisions
		BEGIN
			SELECT RAISE(ABORT, 'comparison revisions are immutable');
		END`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("restore v8 comparison tables with %q: %v", statement, err)
		}
	}
}

func insertV9CatalogRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		  VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', '{}')`, []any{v9ModelID}},
		{`INSERT INTO channels(id, schema_version, revision, created_at, updated_at, credential_id, credential_revision, document_json)
		  VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', NULL, NULL, '{}')`, []any{v9ChannelID}},
		{`INSERT INTO test_plans(id, schema_version, revision, created_at, updated_at, suite_id, suite_revision, document_json)
		  VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', NULL, NULL, '{}')`, []any{v9PlanID}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("insert v8 catalog row: %v", err)
		}
	}
}

func insertV9ExecutionRun(t *testing.T, db *sql.DB, runID, planID string) {
	t.Helper()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshotJSON, documentJSON := validV2RunDocuments(t, runID, planID)
	if _, err := tx.Exec(`INSERT INTO execution_runs(id, current_revision, created_at, sealed)
		VALUES(?, 1, '2026-09-04T00:00:00Z', 0)`, runID); err != nil {
		t.Fatalf("insert execution run root: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO execution_run_revisions(
		run_id, schema_version, revision, created_at, updated_at,
		plan_id, plan_revision, status, snapshot_json, document_json
	) VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', ?, 1, 'queued', ?, ?)`, runID, planID, snapshotJSON, documentJSON); err != nil {
		t.Fatalf("insert execution run revision: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit execution run: %v", err)
	}
}

func insertV9Comparison(t *testing.T, db *sql.DB, fromV8 bool) error {
	t.Helper()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO comparisons(id, current_revision, created_at, sealed)
		VALUES(?, 1, '2026-09-04T00:00:00Z', 0)`, v9ComparisonID); err != nil {
		return err
	}
	documentJSON := `{"source":"file-catalog"}`
	if fromV8 {
		documentJSON = `{"source":"v8"}`
	}
	if _, err := tx.Exec(`INSERT INTO comparison_revisions(
		comparison_id, schema_version, revision, created_at, updated_at,
		plan_id, plan_revision, model_id, model_revision, status, document_json
	) VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', ?, 1, ?, 1, 'running', ?)`,
		v9ComparisonID, v9PlanID, v9ModelID, documentJSON); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO comparison_runs(
		comparison_id, comparison_revision, position, channel_id, channel_revision, run_id
	) VALUES(?, 1, 0, ?, 1, ?)`, v9ComparisonID, v9ChannelID, v9RunID); err != nil {
		return err
	}
	return tx.Commit()
}

func assertComparisonForeignKeysV9(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, removed := range []struct{ table, from, parent, to string }{
		{"comparison_revisions", "plan_id", "test_plans", "id"},
		{"comparison_revisions", "model_id", "models", "id"},
		{"comparison_runs", "channel_id", "channels", "id"},
	} {
		if hasForeignKey(t, db, removed.table, removed.from, removed.parent, removed.to) {
			t.Fatalf("%s still has catalog foreign key %s -> %s.%s", removed.table, removed.from, removed.parent, removed.to)
		}
	}
	for _, retained := range []struct{ table, from, parent, to string }{
		{"comparison_revisions", "comparison_id", "comparisons", "id"},
		{"comparison_runs", "comparison_id", "comparison_revisions", "comparison_id"},
		{"comparison_runs", "comparison_revision", "comparison_revisions", "revision"},
		{"comparison_runs", "run_id", "execution_runs", "id"},
	} {
		if !hasForeignKey(t, db, retained.table, retained.from, retained.parent, retained.to) {
			t.Fatalf("%s is missing runtime foreign key %s -> %s.%s", retained.table, retained.from, retained.parent, retained.to)
		}
	}
}

func assertComparisonGuardsV9(t *testing.T, db *sql.DB) {
	t.Helper()
	if !indexExists(t, db, "idx_comparison_revisions_updated") {
		t.Fatal("idx_comparison_revisions_updated does not exist")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'comparison_revisions_no_%'`); got != 2 {
		t.Fatalf("comparison revision trigger count = %d, want 2", got)
	}
	if _, err := db.Exec(`UPDATE comparison_revisions SET status = 'completed' WHERE comparison_id = ? AND revision = 1`, v9ComparisonID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("update comparison revision error = %v, want immutable rejection", err)
	}
	if _, err := db.Exec(`DELETE FROM comparison_revisions WHERE comparison_id = ? AND revision = 1`, v9ComparisonID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delete comparison revision error = %v, want immutable rejection", err)
	}
	if _, err := db.Exec(`UPDATE comparisons SET current_revision = 2 WHERE id = ?`, v9ComparisonID); err == nil {
		t.Fatal("comparison root accepted a missing current revision")
	}
	if _, err := db.Exec(`DELETE FROM execution_runs WHERE id = ?`, v9RunID); err == nil {
		t.Fatal("comparison run accepted deletion of its execution run")
	}
}

func migrationChecksums(t *testing.T, db *sql.DB, through int) []string {
	t.Helper()
	rows, err := db.Query(`SELECT checksum FROM schema_migrations WHERE version <= ? ORDER BY version`, through)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}
