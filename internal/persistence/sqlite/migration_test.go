package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestMigrateFreshDatabaseCreatesLegacyBaseline(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fresh.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{
		AppVersion: "test",
	}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	db := openDatabase(t, path)
	defer db.Close()

	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("PRAGMA user_version = %d, want %d", got, persistence.CurrentSchemaVersion)
	}

	for _, table := range []string{
		"schema_migrations",
		"runs",
		"run_results",
		"audit_runs",
		"audit_case_results",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q does not exist", table)
		}
	}

	var version int
	var name, checksum, appliedAt, appVersion string
	if err := db.QueryRow(`
		SELECT version, name, checksum, applied_at, app_version
		FROM schema_migrations WHERE version = 1
	`).Scan(&version, &name, &checksum, &appliedAt, &appVersion); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != 1 || name != "0001_legacy_baseline" || len(checksum) != 64 || appliedAt == "" || appVersion != "test" {
		t.Fatalf("migration record = (%d, %q, %q, %q, %q), want complete 0001 metadata", version, name, checksum, appliedAt, appVersion)
	}

	wantRunColumns := []string{
		"id", "ts", "url", "model", "requests", "duration", "max_tokens",
		"input_tokens", "stream", "random", "success", "total", "ttft_p50",
		"ttft_p90", "tpot_p50", "tpot_p90", "e2e_p50", "e2e_p90", "cached",
		"prompt_total", "config", "concurrency", "timeout", "failures", "elapsed",
		"qps", "total_tpm", "peak_in_flight", "request_template", "capture_policy",
		"capture_sample_limit",
	}
	if got := tableColumns(t, db, "runs"); !equalStrings(got, wantRunColumns) {
		t.Fatalf("runs columns = %v, want %v", got, wantRunColumns)
	}

	for _, index := range []string{
		"idx_run_results_run_id",
		"idx_audit_runs_ts",
		"idx_audit_case_results_run_id",
	} {
		if !indexExists(t, db, index) {
			t.Errorf("index %q does not exist", index)
		}
	}

	if got := queryString(t, db, "PRAGMA quick_check"); got != "ok" {
		t.Fatalf("PRAGMA quick_check = %q, want ok", got)
	}
}

func TestMigrateLegacyDatabasePreservesRunsAndAddsOnlyMissingBaselineObjects(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.db")
	db := openDatabase(t, path)
	if _, err := db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT
	)`); err != nil {
		t.Fatalf("create legacy runs: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO runs(id, ts, url, model, requests, success, total, config)
		VALUES(42, '2026-08-01T00:00:00Z', 'https://example.test/v1', 'legacy-model', 3, 2, 3, '{}')
	`); err != nil {
		t.Fatalf("insert legacy run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{
		AppVersion: "test",
	}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	var id, requests, success, total int
	var timestamp, url, model, config string
	var concurrency sql.NullInt64
	var requestTemplate sql.NullString
	if err := db.QueryRow(`
		SELECT id, ts, url, model, requests, success, total, config,
		       concurrency, request_template
		FROM runs WHERE id = 42
	`).Scan(&id, &timestamp, &url, &model, &requests, &success, &total, &config, &concurrency, &requestTemplate); err != nil {
		t.Fatalf("read migrated legacy run: %v", err)
	}
	if id != 42 || timestamp != "2026-08-01T00:00:00Z" || url != "https://example.test/v1" || model != "legacy-model" || requests != 3 || success != 2 || total != 3 || config != "{}" {
		t.Fatalf("legacy run changed: (%d, %q, %q, %q, %d, %d, %d, %q)", id, timestamp, url, model, requests, success, total, config)
	}
	if concurrency.Valid || requestTemplate.Valid {
		t.Fatalf("new legacy columns = (%v, %v), want NULL", concurrency, requestTemplate)
	}
	for _, table := range []string{"runs", "run_results", "audit_runs", "audit_case_results", "schema_migrations"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q does not exist", table)
		}
	}
}

func TestMigratePreservesRowsAndIDsAcrossAllFourLegacyTables(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "complete-legacy.db")
	db := openDatabase(t, path)
	createCompleteLegacySchema(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close complete legacy database: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	checks := []struct {
		query string
		want  int
	}{
		{query: "SELECT id FROM runs WHERE model = 'legacy-model'", want: 42},
		{query: "SELECT id FROM run_results WHERE run_id = 42 AND request_id = 9", want: 77},
		{query: "SELECT id FROM audit_runs WHERE model = 'legacy-audit-model'", want: 84},
		{query: "SELECT id FROM audit_case_results WHERE audit_run_id = 84 AND case_id = 'case-1'", want: 99},
	}
	for _, check := range checks {
		if got := queryInt(t, db, check.query); got != check.want {
			t.Errorf("query %q = %d, want %d", check.query, got, check.want)
		}
	}
	if got := queryString(t, db, "SELECT response_text FROM run_results WHERE id = 77"); got != "legacy response" {
		t.Fatalf("run result payload = %q, want preserved payload", got)
	}
	if got := queryString(t, db, "SELECT evidence FROM audit_case_results WHERE id = 99"); got != "legacy evidence" {
		t.Fatalf("audit evidence = %q, want preserved evidence", got)
	}
}

func createCompleteLegacySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT, url TEXT, model TEXT, requests INTEGER,
			duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
			stream INTEGER, random INTEGER,
			success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
			tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
			cached INTEGER, prompt_total INTEGER, config TEXT
		)`,
		`CREATE TABLE run_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			request_id INTEGER NOT NULL,
			status INTEGER,
			e2e_ms REAL,
			ttft_ms REAL,
			tpot_ms REAL,
			queue_ms REAL,
			started_at_s REAL,
			completed_at_s REAL,
			prompt_tokens INTEGER,
			completion_tokens INTEGER,
			cached_tokens INTEGER,
			chunks INTEGER,
			error TEXT,
			request_body TEXT,
			response_body TEXT,
			response_text TEXT,
			FOREIGN KEY(run_id) REFERENCES runs(id)
		)`,
		`CREATE TABLE audit_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			suite TEXT NOT NULL,
			base_url TEXT NOT NULL,
			model TEXT NOT NULL,
			total INTEGER NOT NULL,
			overall TEXT NOT NULL,
			verdict TEXT NOT NULL,
			summary_json TEXT NOT NULL,
			config_json TEXT NOT NULL,
			report_dir TEXT NOT NULL,
			report_json TEXT NOT NULL,
			report_html TEXT NOT NULL
		)`,
		`CREATE TABLE audit_case_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			audit_run_id INTEGER NOT NULL,
			sequence INTEGER NOT NULL,
			case_id TEXT NOT NULL,
			name TEXT NOT NULL,
			dimension TEXT NOT NULL,
			protocol TEXT NOT NULL,
			model TEXT NOT NULL,
			status TEXT NOT NULL,
			severity TEXT NOT NULL,
			elapsed_ms INTEGER NOT NULL,
			evidence TEXT NOT NULL,
			http_status INTEGER NOT NULL,
			metrics_json TEXT NOT NULL,
			result_json TEXT NOT NULL,
			artifact_dir TEXT NOT NULL,
			FOREIGN KEY(audit_run_id) REFERENCES audit_runs(id) ON DELETE CASCADE,
			UNIQUE(audit_run_id, sequence)
		)`,
		`INSERT INTO runs(id, ts, model, requests, config)
		 VALUES(42, '2026-08-01T00:00:00Z', 'legacy-model', 1, '{}')`,
		`INSERT INTO run_results(id, run_id, request_id, status, response_text)
		 VALUES(77, 42, 9, 200, 'legacy response')`,
		`INSERT INTO audit_runs(
			id, ts, suite, base_url, model, total, overall, verdict,
			summary_json, config_json, report_dir, report_json, report_html
		) VALUES(
			84, '2026-08-01T00:00:00Z', 'openai-chat', 'https://example.test/v1',
			'legacy-audit-model', 1, 'pass', 'qualified', '{}', '{}', '', '', ''
		)`,
		`INSERT INTO audit_case_results(
			id, audit_run_id, sequence, case_id, name, dimension, protocol, model,
			status, severity, elapsed_ms, evidence, http_status, metrics_json,
			result_json, artifact_dir
		) VALUES(
			99, 84, 0, 'case-1', 'Legacy case', 'protocol', 'openai-chat',
			'legacy-audit-model', 'pass', 'must', 12, 'legacy evidence', 200, '{}', '{}', ''
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare complete legacy database: %v\nSQL: %s", err, statement)
		}
	}
}

func TestMigrateIsIdempotentAndDoesNotRewriteMigrationHistory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "idempotent.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	before := migrationRecord(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close after first migration: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"}); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	after := migrationRecord(t, db)
	if before != after {
		t.Fatalf("migration record changed on second run: before=%+v after=%+v", before, after)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("schema_migrations count = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
}

func TestMigrateRejectsDriftAfterMigrationHistoryWasRecorded(t *testing.T) {
	tests := []struct {
		name        string
		mutate      string
		assertDrift func(t *testing.T, db *sql.DB)
	}{
		{
			name:   "missing table",
			mutate: "DROP TABLE audit_case_results",
			assertDrift: func(t *testing.T, db *sql.DB) {
				if tableExists(t, db, "audit_case_results") {
					t.Fatal("rejected migration recreated dropped table")
				}
			},
		},
		{
			name:   "missing index",
			mutate: "DROP INDEX idx_run_results_run_id",
			assertDrift: func(t *testing.T, db *sql.DB) {
				if indexExists(t, db, "idx_run_results_run_id") {
					t.Fatal("rejected migration recreated dropped index")
				}
			},
		},
		{
			name:   "unexpected column",
			mutate: "ALTER TABLE runs ADD COLUMN unexpected TEXT",
			assertDrift: func(t *testing.T, db *sql.DB) {
				if got := tableColumnType(t, db, "runs", "unexpected"); got != "TEXT" {
					t.Fatalf("unexpected column type = %q, want preserved TEXT", got)
				}
			},
		},
		{
			name:   "unexpected generated column",
			mutate: "ALTER TABLE runs ADD COLUMN ghost TEXT GENERATED ALWAYS AS ('x') VIRTUAL",
			assertDrift: func(t *testing.T, db *sql.DB) {
				if !tableXInfoHasColumn(t, db, "runs", "ghost") {
					t.Fatal("rejected migration removed generated ghost column")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "drift.db")
			if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
				t.Fatalf("initial Migrate() error = %v", err)
			}
			db := openDatabase(t, path)
			before := migrationRecord(t, db)
			if _, err := db.Exec(test.mutate); err != nil {
				t.Fatalf("inject schema drift: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close drifted database: %v", err)
			}

			err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
			if err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("Migrate() error = %v, want unknown schema drift", err)
			}

			db = openDatabase(t, path)
			defer db.Close()
			test.assertDrift(t, db)
			if after := migrationRecord(t, db); after != before {
				t.Fatalf("migration history changed after rejected drift: before=%+v after=%+v", before, after)
			}
			if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
				t.Fatalf("user_version after rejected drift = %d, want %d", got, persistence.CurrentSchemaVersion)
			}
		})
	}
}

func TestMigrateRejectsChecksumMismatchWithoutChangingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "checksum.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
		t.Fatalf("initial Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec("UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1"); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Migrate() error = %v, want checksum mismatch", err)
	}
	if got := queryString(t, db, "SELECT checksum FROM schema_migrations WHERE version = 1"); got != "tampered" {
		t.Fatalf("checksum after rejected migration = %q, want tampered", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("migration rows after rejection = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("user_version after rejection = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
}

func TestMigrateRejectsUnknownSchemaWithoutChangingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "unknown.db")
	db := openDatabase(t, path)
	if _, err := db.Exec("CREATE TABLE mystery(id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create unknown table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO mystery(id, value) VALUES(7, 'preserve me')"); err != nil {
		t.Fatalf("insert unknown row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close unknown database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Migrate() error = %v, want unknown schema", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	if got := queryString(t, db, "SELECT value FROM mystery WHERE id = 7"); got != "preserve me" {
		t.Fatalf("unknown table row = %q, want preserved value", got)
	}
	if tableExists(t, db, "schema_migrations") || tableExists(t, db, "runs") {
		t.Fatal("rejected migration created baseline tables")
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != 0 {
		t.Fatalf("user_version after rejection = %d, want 0", got)
	}
}

func TestMigrateRejectsLegacyTableWithWrongColumnShapeWithoutChangingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wrong-column-shape.db")
	db := openDatabase(t, path)
	if _, err := db.Exec(`CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT
	)`); err != nil {
		t.Fatalf("create wrong legacy runs: %v", err)
	}
	if _, err := db.Exec("INSERT INTO runs(id, model) VALUES('legacy-id', 'preserve me')"); err != nil {
		t.Fatalf("insert wrong legacy run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close wrong legacy database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
	if err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "id") {
		t.Fatalf("Migrate() error = %v, want unknown id column shape", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	if got := queryString(t, db, "SELECT model FROM runs WHERE id = 'legacy-id'"); got != "preserve me" {
		t.Fatalf("legacy row after rejection = %q, want preserved value", got)
	}
	if got := tableColumnType(t, db, "runs", "id"); got != "TEXT" {
		t.Fatalf("runs.id type after rejection = %q, want TEXT", got)
	}
	if tableExists(t, db, "schema_migrations") {
		t.Fatal("rejected migration created schema_migrations")
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != 0 {
		t.Fatalf("user_version after rejection = %d, want 0", got)
	}
}

func TestMigrateRejectsLegacyTableWithNonCanonicalDDLWithoutChangingDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "noncanonical-ddl.db")
	db := openDatabase(t, path)
	if _, err := db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY,
		ts TEXT, url TEXT, model TEXT, requests INTEGER CHECK(requests >= 10),
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT
	)`); err != nil {
		t.Fatalf("create noncanonical legacy runs: %v", err)
	}
	if _, err := db.Exec("INSERT INTO runs(id, model, requests) VALUES(42, 'preserve me', 10)"); err != nil {
		t.Fatalf("insert noncanonical legacy run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close noncanonical legacy database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
	if err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "DDL") {
		t.Fatalf("Migrate() error = %v, want unknown legacy DDL", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	if got := queryString(t, db, "SELECT model FROM runs WHERE id = 42"); got != "preserve me" {
		t.Fatalf("legacy row after rejection = %q, want preserved value", got)
	}
	if tableExists(t, db, "schema_migrations") {
		t.Fatal("rejected migration created schema_migrations")
	}
}

func TestMigrateRejectsLegacySchemaWithWrongIndexOrForeignKeyWithoutChangingDatabase(t *testing.T) {
	t.Run("same-name index targets wrong column", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wrong-index.db")
		db := openDatabase(t, path)
		createCompleteLegacySchema(t, db)
		if _, err := db.Exec("CREATE INDEX idx_run_results_run_id ON run_results(request_id)"); err != nil {
			t.Fatalf("create wrong legacy index: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close wrong index database: %v", err)
		}

		err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
		if err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "index") {
			t.Fatalf("Migrate() error = %v, want unknown index definition", err)
		}

		db = openDatabase(t, path)
		defer db.Close()
		if got := indexColumns(t, db, "idx_run_results_run_id"); !equalStrings(got, []string{"request_id"}) {
			t.Fatalf("wrong index after rejection = %v, want original request_id", got)
		}
		if tableExists(t, db, "schema_migrations") {
			t.Fatal("rejected migration created schema_migrations")
		}
	})

	t.Run("foreign key targets wrong parent column", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "wrong-foreign-key.db")
		db := openDatabase(t, path)
		createCompleteLegacySchema(t, db)
		if _, err := db.Exec("DROP TABLE run_results"); err != nil {
			t.Fatalf("drop correct legacy run_results: %v", err)
		}
		if _, err := db.Exec(`CREATE TABLE run_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			request_id INTEGER NOT NULL,
			status INTEGER,
			e2e_ms REAL,
			ttft_ms REAL,
			tpot_ms REAL,
			queue_ms REAL,
			started_at_s REAL,
			completed_at_s REAL,
			prompt_tokens INTEGER,
			completion_tokens INTEGER,
			cached_tokens INTEGER,
			chunks INTEGER,
			error TEXT,
			request_body TEXT,
			response_body TEXT,
			response_text TEXT,
			FOREIGN KEY(run_id) REFERENCES runs(requests)
		)`); err != nil {
			t.Fatalf("create wrong legacy foreign key: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close wrong foreign key database: %v", err)
		}

		err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
		if err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "foreign key") {
			t.Fatalf("Migrate() error = %v, want unknown foreign key definition", err)
		}

		db = openDatabase(t, path)
		defer db.Close()
		if got := foreignKeyTargetColumn(t, db, "run_results", "run_id"); got != "requests" {
			t.Fatalf("foreign key target after rejection = %q, want original requests", got)
		}
		if tableExists(t, db, "schema_migrations") {
			t.Fatal("rejected migration created schema_migrations")
		}
	})
}

func TestMigrateRollsBackEveryChangeWhenPostMigrationIntegrityCheckFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "integrity-failure.db")
	db := openDatabase(t, path)
	if _, err := db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT
	)`); err != nil {
		t.Fatalf("create legacy runs: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE run_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL,
		request_id INTEGER NOT NULL,
		status INTEGER,
		e2e_ms REAL,
		ttft_ms REAL,
		tpot_ms REAL,
		queue_ms REAL,
		started_at_s REAL,
		completed_at_s REAL,
		prompt_tokens INTEGER,
		completion_tokens INTEGER,
		cached_tokens INTEGER,
		chunks INTEGER,
		error TEXT,
		request_body TEXT,
		response_body TEXT,
		response_text TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id)
	)`); err != nil {
		t.Fatalf("create legacy run_results: %v", err)
	}
	if _, err := db.Exec("INSERT INTO run_results(id, run_id, request_id) VALUES(8, 999, 1)"); err != nil {
		t.Fatalf("insert orphan result: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test"})
	if err == nil || !strings.Contains(err.Error(), "foreign key check failed") {
		t.Fatalf("Migrate() error = %v, want foreign key integrity failure", err)
	}

	db = openDatabase(t, path)
	defer db.Close()
	wantLegacyColumns := []string{
		"id", "ts", "url", "model", "requests", "duration", "max_tokens",
		"input_tokens", "stream", "random", "success", "total", "ttft_p50",
		"ttft_p90", "tpot_p50", "tpot_p90", "e2e_p50", "e2e_p90", "cached",
		"prompt_total", "config",
	}
	if got := tableColumns(t, db, "runs"); !equalStrings(got, wantLegacyColumns) {
		t.Fatalf("runs columns after rollback = %v, want original %v", got, wantLegacyColumns)
	}
	if got := queryInt(t, db, "SELECT run_id FROM run_results WHERE id = 8"); got != 999 {
		t.Fatalf("orphan row after rollback = %d, want 999", got)
	}
	for _, table := range []string{"schema_migrations", "audit_runs", "audit_case_results"} {
		if tableExists(t, db, table) {
			t.Errorf("rolled-back migration left table %q", table)
		}
	}
	if got := queryInt(t, db, "PRAGMA user_version"); got != 0 {
		t.Fatalf("user_version after rollback = %d, want 0", got)
	}
}

func TestBackupCreatesConsistentPreMigrationSnapshot(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-source.db")
	backupPath := filepath.Join(directory, "backups", "before-0001.db")
	db := openDatabase(t, sourcePath)
	if _, err := db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT
	)`); err != nil {
		t.Fatalf("create legacy runs: %v", err)
	}
	if _, err := db.Exec("INSERT INTO runs(id, model, requests) VALUES(42, 'before-backup', 3)"); err != nil {
		t.Fatalf("insert legacy run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source before backup: %v", err)
	}

	if err := persistence.Backup(context.Background(), sourcePath, backupPath); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	db = openDatabase(t, sourcePath)
	if _, err := db.Exec("UPDATE runs SET model = 'after-backup' WHERE id = 42"); err != nil {
		t.Fatalf("mutate source after backup: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source after mutation: %v", err)
	}
	if err := persistence.Migrate(context.Background(), sourcePath, persistence.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatalf("migrate source after backup: %v", err)
	}

	backup := openDatabase(t, backupPath)
	defer backup.Close()
	var id, requests int
	var model string
	if err := backup.QueryRow("SELECT id, model, requests FROM runs WHERE id = 42").Scan(&id, &model, &requests); err != nil {
		t.Fatalf("read backup row: %v", err)
	}
	if id != 42 || model != "before-backup" || requests != 3 {
		t.Fatalf("backup row = (%d, %q, %d), want pre-migration snapshot", id, model, requests)
	}
	if tableExists(t, backup, "schema_migrations") {
		t.Fatal("pre-migration backup unexpectedly contains migration history")
	}
	if got := queryInt(t, backup, "PRAGMA user_version"); got != 0 {
		t.Fatalf("backup user_version = %d, want 0", got)
	}
	if got := queryString(t, backup, "PRAGMA quick_check"); got != "ok" {
		t.Fatalf("backup quick_check = %q, want ok", got)
	}
}

func TestBackupPublishesOnlyAfterSnapshotCompletes(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	backupPath := filepath.Join(directory, "backup.db")
	db := openDatabase(t, sourcePath)
	if _, err := db.Exec("CREATE TABLE values_table(id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		db.Close()
		t.Fatalf("create source table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO values_table(id, value) VALUES(1, 'consistent')"); err != nil {
		db.Close()
		t.Fatalf("insert source value: %v", err)
	}
	connection, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatalf("acquire exclusive source connection: %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		connection.Close()
		db.Close()
		t.Fatalf("lock source database: %v", err)
	}
	locked := true
	t.Cleanup(func() {
		if locked {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
		_ = connection.Close()
		_ = db.Close()
	})

	done := make(chan error, 1)
	go func() {
		done <- persistence.Backup(context.Background(), sourcePath, backupPath)
	}()
	select {
	case err := <-done:
		t.Fatalf("Backup() returned before source lock was released: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	_, statErr := os.Stat(backupPath)
	finalVisibleEarly := statErr == nil
	unexpectedStatError := statErr != nil && !errors.Is(statErr, os.ErrNotExist)

	if _, err := connection.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("release source lock: %v", err)
	}
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Backup() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Backup() did not finish after source lock was released")
	}
	if unexpectedStatError {
		t.Fatalf("stat backup final path before completion: %v", statErr)
	}
	if finalVisibleEarly {
		t.Fatal("backup final path was visible before completion")
	}
	backup := openDatabase(t, backupPath)
	defer backup.Close()
	if got := queryString(t, backup, "SELECT value FROM values_table WHERE id = 1"); got != "consistent" {
		t.Fatalf("published backup value = %q, want consistent", got)
	}
}

func TestMigrateHonorsConfiguredBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	locker := openDatabase(t, path)
	connection, err := locker.Conn(context.Background())
	if err != nil {
		locker.Close()
		t.Fatalf("acquire locking connection: %v", err)
	}
	if _, err := connection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		connection.Close()
		locker.Close()
		t.Fatalf("hold sqlite writer lock: %v", err)
	}
	defer func() {
		_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		_ = connection.Close()
		_ = locker.Close()
	}()

	started := time.Now()
	err = persistence.Migrate(context.Background(), path, persistence.MigrateOptions{
		AppVersion:  "test",
		BusyTimeout: 120 * time.Millisecond,
	})
	elapsed := time.Since(started)
	if err == nil || (!strings.Contains(strings.ToLower(err.Error()), "locked") && !strings.Contains(strings.ToLower(err.Error()), "busy")) {
		t.Fatalf("Migrate() error = %v, want SQLite busy error", err)
	}
	if elapsed < 80*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("Migrate() waited %v, want approximately configured 120ms timeout", elapsed)
	}
}

type storedMigration struct {
	Version    int
	Name       string
	Checksum   string
	AppliedAt  string
	AppVersion string
}

func migrationRecord(t *testing.T, db *sql.DB) storedMigration {
	t.Helper()
	var record storedMigration
	if err := db.QueryRow(`
		SELECT version, name, checksum, applied_at, app_version
		FROM schema_migrations WHERE version = 1
	`).Scan(&record.Version, &record.Name, &record.Checksum, &record.AppliedAt, &record.AppVersion); err != nil {
		t.Fatalf("read migration record: %v", err)
	}
	return record
}

func openDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(): %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("db.Ping(): %v", err)
	}
	return db
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func queryString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name) == 1
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	return queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name) == 1
}

func indexColumns(t *testing.T, db *sql.DB, name string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA index_info(" + name + ")")
	if err != nil {
		t.Fatalf("index_info(%s): %v", name, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var sequence, cid int
		var column string
		if err := rows.Scan(&sequence, &cid, &column); err != nil {
			t.Fatalf("scan index_info(%s): %v", name, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_info(%s): %v", name, err)
	}
	return columns
}

func foreignKeyTargetColumn(t *testing.T, db *sql.DB, table, fromColumn string) string {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_list(" + table + ")")
	if err != nil {
		t.Fatalf("foreign_key_list(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sequence int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list(%s): %v", table, err)
		}
		if from == fromColumn {
			return to
		}
	}
	t.Fatalf("foreign key %s.%s does not exist", table, fromColumn)
	return ""
}

func tableColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	return columns
}

func tableColumnType(t *testing.T, db *sql.DB, table, column string) string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return columnType
		}
	}
	t.Fatalf("column %s.%s does not exist", table, column)
	return ""
}

func tableXInfoHasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_xinfo(" + table + ")")
	if err != nil {
		t.Fatalf("table_xinfo(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			t.Fatalf("scan table_xinfo(%s): %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_xinfo(%s): %v", table, err)
	}
	return false
}

func equalStrings(left, right []string) bool {
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
