package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyMigration0011RejectsNonEmptyIntegrations(t *testing.T) {
	t.Parallel()
	db := openMigration0011V10Database(t)
	defer db.Close()

	if _, err := db.Exec(`
		INSERT INTO integrations(
			id, schema_version, revision, created_at, updated_at,
			credential_id, credential_revision, document_json
		) VALUES(
			'integration-1', 1, 1,
			'2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z',
			NULL, NULL, '{}'
		)
	`); err != nil {
		t.Fatalf("insert integration fixture: %v", err)
	}

	err := runMigration0011(t, db)
	if err == nil || !strings.Contains(err.Error(), "integrations") || !strings.Contains(err.Error(), "1") {
		t.Fatalf("applyMigration0011() error = %v, want non-empty integrations rejection", err)
	}
	if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != 10 {
		t.Fatalf("user_version after rejected retirement = %d, want 10", got)
	}
	if !migration0011ObjectExists(t, db, "table", "integrations") {
		t.Fatal("integrations table was dropped despite rejected retirement")
	}
}

func TestApplyMigration0011RejectsMissingOrMalformedV2Snapshots(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		wantReason string
		seed       func(*testing.T, *sql.DB)
	}{
		{
			name:       "run snapshot missing schema version",
			wantReason: "run snapshot",
			seed: func(t *testing.T, db *sql.DB) {
				insertMigration0011RunRows(t, db, `{}`, `{"plan_snapshot":{"schema_version":2}}`)
			},
		},
		{
			name:       "run document snapshot version is not an integer",
			wantReason: "run document",
			seed: func(t *testing.T, db *sql.DB) {
				insertMigration0011RunRows(t, db, `{"schema_version":2}`, `{"plan_snapshot":{"schema_version":"2"}}`)
			},
		},
		{
			name:       "integer version does not bypass full decode",
			wantReason: "snapshot document is invalid",
			seed: func(t *testing.T, db *sql.DB) {
				insertMigration0011RunRows(t, db, `{"schema_version":2}`, `{"plan_snapshot":{"schema_version":2}}`)
			},
		},
		{
			name:       "report snapshot version is not an integer",
			wantReason: "report snapshot",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`
					INSERT INTO reports(id, schema_version, run_id, generated_at, document_json)
					VALUES('report-1', 1, 'missing-run', '2026-09-04T00:00:00Z',
					       '{"plan_snapshot":{"schema_version":"2"}}')
				`); err != nil {
					t.Fatalf("insert malformed report fixture: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := openMigration0011V10Database(t)
			defer db.Close()
			test.seed(t, db)

			err := runMigration0011(t, db)
			if err == nil || !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("applyMigration0011() error = %v, want %q rejection", err, test.wantReason)
			}
			if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != 10 {
				t.Fatalf("user_version after rejected retirement = %d, want 10", got)
			}
		})
	}
}

func TestApplyMigration0011RejectsForeignKeyCorruption(t *testing.T) {
	t.Parallel()
	db := openMigration0011V10Database(t)
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO evidence(
			id, schema_version, revision, created_at, updated_at, run_id, document_json
		) VALUES(
			'evidence-1', 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z',
			'missing-run', '{}'
		)
	`); err != nil {
		t.Fatalf("insert orphan evidence fixture: %v", err)
	}

	err := runMigration0011(t, db)
	if err == nil || !strings.Contains(err.Error(), "foreign key check") {
		t.Fatalf("applyMigration0011() error = %v, want foreign-key corruption rejection", err)
	}
	if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != 10 {
		t.Fatalf("user_version after rejected retirement = %d, want 10", got)
	}
}

func TestApplyMigration0011DropsAuthoredCatalogAndKeepsOperationalSchema(t *testing.T) {
	t.Parallel()
	db := openMigration0011V10Database(t)
	defer db.Close()
	seedMigration0011AuthoredRows(t, db)
	beforeChecksums := migration0011Checksums(t, db, 10)

	if err := runMigration0011(t, db); err != nil {
		t.Fatalf("applyMigration0011() error = %v", err)
	}
	if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != 11 {
		t.Fatalf("user_version = %d, want 11", got)
	}
	if got := migration0011QueryInt(t, db, `
		SELECT COUNT(*) FROM schema_migrations
		WHERE version = 11 AND name = '0011_authored_catalog_retirement'
	`); got != 1 {
		t.Fatalf("v11 migration rows = %d, want 1", got)
	}
	if afterChecksums := migration0011Checksums(t, db, 10); !migration0011EqualStrings(afterChecksums, beforeChecksums) {
		t.Fatalf("historical migration checksums changed: before=%v after=%v", beforeChecksums, afterChecksums)
	}
	for _, table := range migration0011RetiredTables {
		if migration0011ObjectExists(t, db, "table", table) {
			t.Fatalf("retired authored table %q still exists", table)
		}
	}
	for _, table := range []string{
		"schema_migrations", "runs", "run_results", "audit_runs", "audit_case_results",
		"execution_runs", "execution_run_revisions", "evidence", "case_results", "reports",
		"artifacts", "report_attachments", "comparisons", "comparison_revisions",
		"comparison_runs", "quick_performance_reports",
	} {
		if !migration0011ObjectExists(t, db, "table", table) {
			t.Fatalf("operational table %q was not retained", table)
		}
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := validateAppliedSchema0011(context.Background(), conn); err != nil {
		t.Fatalf("validateAppliedSchema0011() error = %v", err)
	}
	if err := validateIntegrity(context.Background(), conn); err != nil {
		t.Fatalf("validateIntegrity() error = %v", err)
	}
}

func TestValidateAppliedSchema0011RejectsRecreatedRetiredTable(t *testing.T) {
	t.Parallel()
	db := openMigration0011V10Database(t)
	defer db.Close()
	if err := runMigration0011(t, db); err != nil {
		t.Fatalf("applyMigration0011() error = %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE models (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("recreate retired table: %v", err)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = validateAppliedSchema0011(context.Background(), conn)
	if err == nil || !strings.Contains(err.Error(), "retired authored table") || !strings.Contains(err.Error(), "models") {
		t.Fatalf("validateAppliedSchema0011() error = %v, want recreated models rejection", err)
	}
}

func TestApplyMigration0011RequiresCompletedV10(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "unmigrated.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = runMigration0011(t, db)
	if err == nil || !strings.Contains(err.Error(), "version 10") {
		t.Fatalf("applyMigration0011() error = %v, want completed v10 requirement", err)
	}
}

func openMigration0011V10Database(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migration-v10.db")
	if err := Migrate(context.Background(), path, MigrateOptions{AppVersion: "migration-0011-test"}); err != nil {
		t.Fatalf("create v10 database: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if got := migration0011QueryInt(t, db, "PRAGMA user_version"); got != 10 {
		db.Close()
		t.Fatalf("fixture user_version = %d, want 10", got)
	}
	return db
}

func runMigration0011(t *testing.T, db *sql.DB) error {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration0011(ctx, conn, "migration-0011-test"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	return nil
}

func insertMigration0011RunRows(t *testing.T, db *sql.DB, snapshotJSON, documentJSON string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO execution_runs(id, current_revision, created_at, sealed)
		VALUES('run-1', 1, '2026-09-04T00:00:00Z', 0)
	`); err != nil {
		t.Fatalf("insert run root: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO execution_run_revisions(
			run_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, status, snapshot_json, document_json
		) VALUES(
			'run-1', 1, 1, '2026-09-04T00:00:00Z', '2026-09-04T00:00:00Z',
			'plan-1', 1, 'queued', ?, ?
		)
	`, snapshotJSON, documentJSON); err != nil {
		t.Fatalf("insert run revision: %v", err)
	}
}

func seedMigration0011AuthoredRows(t *testing.T, db *sql.DB) {
	t.Helper()
	const timestamp = "2026-09-04T00:00:00Z"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO credential_refs(id, schema_version, revision, created_at, updated_at, store_ref, purpose, masked_suffix, fingerprint, document_json)
		  VALUES('credential-1', 1, 1, ?, ?, 'keyring:credential-1', 'channel_api_key', '1234', 'fingerprint', '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		  VALUES('model-1', 1, 1, ?, ?, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO channels(id, schema_version, revision, created_at, updated_at, credential_id, credential_revision, document_json)
		  VALUES('channel-1', 1, 1, ?, ?, 'credential-1', 1, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO channel_models(id, schema_version, revision, created_at, updated_at, channel_id, channel_revision, model_id, model_revision, binding_key, document_json)
		  VALUES('mapping-1', 1, 1, ?, ?, 'channel-1', 1, 'model-1', 1, 'channel-1|model-1', '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json)
		  VALUES('case-1', 1, 1, ?, ?, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO test_suites(id, schema_version, revision, created_at, updated_at, document_json)
		  VALUES('suite-1', 1, 1, ?, ?, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO suite_cases(suite_id, suite_revision, position, case_id, case_revision)
		  VALUES('suite-1', 1, 0, 'case-1', 1)`, nil},
		{`INSERT INTO test_plans(id, schema_version, revision, created_at, updated_at, suite_id, suite_revision, document_json)
		  VALUES('plan-1', 1, 1, ?, ?, 'suite-1', 1, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO plan_models(plan_id, plan_revision, position, model_id, model_revision)
		  VALUES('plan-1', 1, 0, 'model-1', 1)`, nil},
		{`INSERT INTO plan_channels(plan_id, plan_revision, position, channel_id, channel_revision)
		  VALUES('plan-1', 1, 0, 'channel-1', 1)`, nil},
		{`INSERT INTO plan_cases(plan_id, plan_revision, position, case_id, case_revision)
		  VALUES('plan-1', 1, 0, 'case-1', 1)`, nil},
		{`INSERT INTO plan_channel_models(plan_id, plan_revision, position, channel_id, model_id, mapping_id, mapping_revision)
		  VALUES('plan-1', 1, 0, 'channel-1', 'model-1', 'mapping-1', 1)`, nil},
		{`INSERT INTO test_case_import_sources(
			namespace, source_key, source_path, source_bytes_sha256, semantic_sha256,
			materialized_sha256, converter_version, entity_id, imported_revision,
			bundle_manifest_sha256, imported_at, retired_at
		  ) VALUES('fixture', 'case-1', 'fixture/case-1.json', ?, ?, ?, 1, 'case-1', 1, ?, ?, NULL)`,
			[]any{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), timestamp}},
		{`INSERT INTO catalog_tombstones(entity_table, entity_id, deleted_revision, deleted_at)
		  VALUES('models', 'deleted-model', 2, ?)`, []any{timestamp}},
		{`INSERT INTO case_catalog_cutover(id, completed_at) VALUES(1, ?)`, []any{timestamp}},
		{`INSERT INTO pending_test_case_snapshots(id, schema_version, revision, created_at, updated_at, document_json)
		  VALUES('pending-case', 1, 1, ?, ?, '{}')`, []any{timestamp, timestamp}},
		{`INSERT INTO builtin_catalog_seeds(seed_key, completed_at) VALUES('fixture', ?)`, []any{timestamp}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed authored v10 catalog with %q: %v", statement.query, err)
		}
	}
}

func migration0011QueryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func migration0011ObjectExists(t *testing.T, db *sql.DB, objectType, name string) bool {
	t.Helper()
	return migration0011QueryInt(t, db,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, objectType, name) == 1
}

func migration0011Checksums(t *testing.T, db *sql.DB, through int) []string {
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

func migration0011EqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
