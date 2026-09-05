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
	v8ExistingRunID = "88888888-8888-4888-8888-888888888881"
	v8NewRunID      = "88888888-8888-4888-8888-888888888882"
	v8PlanID        = "88888888-8888-4888-8888-888888888883"
)

func TestMigrateV8ExecutionRunSnapshotsDoNotRequireCatalogPlan(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v8.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v8"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()

	if err := insertCataloglessV2Run(t, db, v8NewRunID); err != nil {
		t.Fatalf("insert v2 execution run without catalog rows: %v", err)
	}
	assertExecutionRunRevisionForeignKeysV8(t, db)
	assertExecutionRunRevisionGuardsV8(t, db, v8NewRunID)
	assertDatabaseIntegrity(t, db)
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 8 AND name = '0008_execution_run_snapshot_plan_decoupling'`); got != 1 {
		t.Fatalf("execution run snapshot migration rows = %d, want 1", got)
	}
}

func TestMigrateV8PreservesExistingExecutionRunRevisions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v7.db")
	createMigration0007Fixture(t, path)

	db := openDatabase(t, path)
	insertV7PlanAndRun(t, db)
	if !hasForeignKey(t, db, "execution_run_revisions", "plan_id", "test_plans", "id") {
		db.Close()
		t.Fatal("v7 fixture is missing the execution_run_revisions plan foreign key")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v8"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	wantSnapshot, wantDocument := validV2RunDocuments(t, v8ExistingRunID, v8PlanID)

	var schemaVersion, revision int
	var planID, snapshotJSON, documentJSON string
	if err := db.QueryRow(`
		SELECT schema_version, revision, plan_id, snapshot_json, document_json
		FROM execution_run_revisions WHERE run_id = ? AND revision = 1
	`, v8ExistingRunID).Scan(&schemaVersion, &revision, &planID, &snapshotJSON, &documentJSON); err != nil {
		t.Fatalf("read preserved execution run revision: %v", err)
	}
	if schemaVersion != 1 || revision != 1 || planID != v8PlanID || snapshotJSON != string(wantSnapshot) || documentJSON != string(wantDocument) {
		t.Fatalf("preserved execution run revision = (%d, %d, %q, %q, %q)", schemaVersion, revision, planID, snapshotJSON, documentJSON)
	}
	assertExecutionRunRevisionForeignKeysV8(t, db)
	assertExecutionRunRevisionGuardsV8(t, db, v8ExistingRunID)
	assertDatabaseIntegrity(t, db)
}

func insertCataloglessV2Run(t *testing.T, db *sql.DB, runID string) error {
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
	snapshotJSON, documentJSON := validV2RunDocuments(t, runID, v8PlanID)
	if _, err := tx.Exec(`
		INSERT INTO execution_runs(id, current_revision, created_at, sealed)
		VALUES(?, 1, '2026-09-04T00:00:00Z', 0)
	`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', ?, 1, 'queued', ?, ?)
	`, runID, v8PlanID, snapshotJSON, documentJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func insertV7PlanAndRun(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO test_plans(
			id, schema_version, revision, created_at, updated_at,
			suite_id, suite_revision, document_json
		) VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', NULL, NULL, '{}')
	`, v8PlanID); err != nil {
		t.Fatalf("insert v7 plan: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshotJSON, documentJSON := validV2RunDocuments(t, v8ExistingRunID, v8PlanID)
	if _, err := tx.Exec(`
		INSERT INTO execution_runs(id, current_revision, created_at, sealed)
		VALUES(?, 1, '2026-09-04T00:00:00Z', 0)
	`, v8ExistingRunID); err != nil {
		t.Fatalf("insert v7 execution run: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(?, 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', ?, 1, 'queued', ?, ?)
	`, v8ExistingRunID, v8PlanID, snapshotJSON, documentJSON); err != nil {
		t.Fatalf("insert v7 execution run revision: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit v7 execution run: %v", err)
	}
}

func assertExecutionRunRevisionForeignKeysV8(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(execution_run_revisions)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type foreignKey struct {
		parent, from, to, onUpdate, onDelete, match string
	}
	var got []foreignKey
	for rows.Next() {
		var id, sequence int
		var item foreignKey
		if err := rows.Scan(&id, &sequence, &item.parent, &item.from, &item.to, &item.onUpdate, &item.onDelete, &item.match); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (foreignKey{
		parent: "execution_runs", from: "run_id", to: "id",
		onUpdate: "NO ACTION", onDelete: "CASCADE", match: "NONE",
	}) {
		t.Fatalf("execution_run_revisions foreign keys = %+v, want only run_id -> execution_runs.id", got)
	}
}

func assertExecutionRunRevisionGuardsV8(t *testing.T, db *sql.DB, runID string) {
	t.Helper()
	if !indexExists(t, db, "idx_execution_run_revisions_updated") {
		t.Fatal("idx_execution_run_revisions_updated does not exist")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'trg_execution_run_revisions_%'`); got != 3 {
		t.Fatalf("execution run revision trigger count = %d, want 3", got)
	}
	if _, err := db.Exec(`UPDATE execution_run_revisions SET status = 'running' WHERE run_id = ? AND revision = 1`, runID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("update execution run revision error = %v, want immutable rejection", err)
	}
	if _, err := db.Exec(`DELETE FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, runID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delete execution run revision error = %v, want immutable rejection", err)
	}
	if _, err := db.Exec(`
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(?, 2, 3, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z', ?, 1, 'pending', '{}', '{}')
	`, runID, v8PlanID); err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("non-contiguous execution run revision error = %v, want contiguous rejection", err)
	}
}

func assertDatabaseIntegrity(t *testing.T, db *sql.DB) {
	t.Helper()
	if got := queryInt(t, db, "SELECT COUNT(*) FROM pragma_foreign_key_check"); got != 0 {
		t.Fatalf("foreign_key_check rows = %d, want 0", got)
	}
	if got := queryString(t, db, "PRAGMA quick_check"); got != "ok" {
		t.Fatalf("quick_check = %q, want ok", got)
	}
}

func hasForeignKey(t *testing.T, db *sql.DB, table, fromColumn, parentTable, toColumn string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if parent == parentTable && from == fromColumn && to == toColumn {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

// createMigration0007Fixture seeds the current schema and, once v8 exists,
// restores only the v7 execution_run_revisions definition and migration marker.
// That keeps the upgrade test independent from unexported migration functions.
func createMigration0007Fixture(t *testing.T, path string) {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v7"}); err != nil {
		t.Fatal(err)
	}
	if persistence.CurrentSchemaVersion == 7 {
		return
	}
	db := openDatabase(t, path)
	db.SetMaxOpenConns(1)
	defer db.Close()
	restoreExecutionRunRevisionsV7(t, db)
	restoreComparisonTablesV8(t, db)
	for _, statement := range []string{
		"DELETE FROM schema_migrations WHERE version >= 8",
		"PRAGMA user_version = 7",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("construct v7 fixture with %q: %v", statement, err)
		}
	}
}

func restoreExecutionRunRevisionsV7(t *testing.T, db *sql.DB) {
	t.Helper()
	db.SetMaxOpenConns(1)
	statements := []string{
		"PRAGMA foreign_keys = OFF",
		"DROP TRIGGER trg_execution_run_revisions_no_update",
		"DROP TRIGGER trg_execution_run_revisions_no_delete",
		"DROP TRIGGER trg_execution_run_revisions_contiguous_insert",
		"DROP INDEX idx_execution_run_revisions_updated",
		`CREATE TABLE execution_run_revisions_v7_backup AS SELECT * FROM execution_run_revisions`,
		"DROP TABLE execution_run_revisions",
		`CREATE TABLE execution_run_revisions (
			run_id TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			plan_id TEXT NOT NULL,
			plan_revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
			document_json TEXT NOT NULL CHECK(json_valid(document_json)),
			PRIMARY KEY(run_id, revision),
			FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
			FOREIGN KEY(plan_id, plan_revision) REFERENCES test_plans(id, revision)
		)`,
		`INSERT INTO execution_run_revisions SELECT * FROM execution_run_revisions_v7_backup`,
		"DROP TABLE execution_run_revisions_v7_backup",
		`CREATE INDEX idx_execution_run_revisions_updated ON execution_run_revisions(updated_at DESC, run_id)`,
		`CREATE TRIGGER trg_execution_run_revisions_no_update
			BEFORE UPDATE ON execution_run_revisions
			BEGIN
				SELECT RAISE(ABORT, 'execution run revisions are immutable');
			END`,
		`CREATE TRIGGER trg_execution_run_revisions_no_delete
			BEFORE DELETE ON execution_run_revisions
			BEGIN
				SELECT RAISE(ABORT, 'execution run revisions are immutable');
			END`,
		`CREATE TRIGGER trg_execution_run_revisions_contiguous_insert
			BEFORE INSERT ON execution_run_revisions
			WHEN NEW.revision != COALESCE(
				(SELECT MAX(revision) + 1 FROM execution_run_revisions WHERE run_id = NEW.run_id),
				1
			)
			BEGIN
				SELECT RAISE(ABORT, 'execution run revisions must be contiguous');
			END`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("restore v7 execution_run_revisions with %q: %v", statement, err)
		}
	}
}
