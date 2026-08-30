package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test/internal/domain"
	persistence "github.com/894x/llm-test/internal/persistence/sqlite"
)

func TestMigrateAppliesCaseImportTrackingSchemaV3(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "fresh-v3.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v3"}); err != nil {
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
	if got := tableColumns(t, db, "test_case_import_sources"); !equalStrings(got, []string{
		"namespace", "source_key", "source_path", "source_bytes_sha256", "semantic_sha256",
		"materialized_sha256", "converter_version", "entity_id", "imported_revision",
		"bundle_manifest_sha256", "imported_at", "retired_at",
	}) {
		t.Fatalf("test_case_import_sources columns = %v", got)
	}
	if got := foreignKeyTargetColumn(t, db, "test_case_import_sources", "entity_id"); got != "id" {
		t.Fatalf("test_case_import_sources entity_id target = %q, want id", got)
	}
	if got := foreignKeyTargetColumn(t, db, "test_case_import_sources", "imported_revision"); got != "revision" {
		t.Fatalf("test_case_import_sources imported_revision target = %q, want revision", got)
	}
}

func TestMigrateUpgradesV2ToCaseImportTrackingWithoutChangingCoreRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "upgrade-v2-v3.db")
	createMigration0002Database(t, path)
	db := openDatabase(t, path)
	if _, err := db.Exec(`INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES('11111111-1111-4111-8111-111111111111', 1, 1,
		'2026-08-30T00:00:00Z', '2026-08-30T00:00:00Z',
		'{"id":"11111111-1111-4111-8111-111111111111","schema_version":1,"revision":1,"created_at":"2026-08-30T00:00:00Z","updated_at":"2026-08-30T00:00:00Z","name":"preserved","protocol":"openai-chat","capabilities":["chat"]}')`); err != nil {
		db.Close()
		t.Fatalf("insert v2 model: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v2 fixture: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v3"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	if got := queryString(t, db, "SELECT json_extract(document_json, '$.name') FROM models"); got != "preserved" {
		t.Fatalf("model name = %q, want preserved", got)
	}
	if !tableExists(t, db, "test_case_import_sources") {
		t.Fatal("test_case_import_sources table does not exist")
	}
}

func TestMigrateUpgradesV2TestCaseDocumentsToPolicyShape(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "upgrade-v2-test-case.db")
	createMigration0002Database(t, path)
	db := openDatabase(t, path)
	const id = "44444444-4444-4444-8444-444444444444"
	const stamp = "2026-08-30T00:00:00Z"
	const legacyDocument = `{"created_at":"2026-08-30T00:00:00Z","definition":{"assertions":[{"config":{"required":true},"kind":"response_schema"}],"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"request":{"body":{},"headers":{},"method":"POST","path":"/chat/completions"},"schema_version":1},"id":"44444444-4444-4444-8444-444444444444","name":"preserved v2 case","protocol":"openai-chat","revision":1,"schema_version":1,"updated_at":"2026-08-30T00:00:00Z"}`
	if _, err := db.Exec(`INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, 1, 1, ?, ?, ?)`, id, stamp, stamp, legacyDocument); err != nil {
		db.Close()
		t.Fatalf("insert v2 test case: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v2 test-case fixture: %v", err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v3"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	defer repository.Close()
	testCases, err := repository.ListTestCases(context.Background())
	if err != nil {
		t.Fatalf("ListTestCases() error = %v", err)
	}
	if len(testCases) != 1 {
		t.Fatalf("test case count = %d, want 1", len(testCases))
	}
	testCase := testCases[0]
	if testCase.ID != id || testCase.Revision != 1 || testCase.Name != "preserved v2 case" ||
		testCase.Key != "migrated."+id || testCase.Dimension != "legacy" || !testCase.Enabled || testCase.Default ||
		testCase.Severity != domain.CaseSeverityNormal || testCase.ExecutionMode != domain.CaseExecutionAutomatic {
		t.Fatalf("upgraded test case = %+v", testCase)
	}
}

func TestMigrateRejectsCaseImportSchemaDrift(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "drift-v3.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "first"}); err != nil {
		t.Fatalf("initial Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec("ALTER TABLE test_case_import_sources ADD COLUMN unexpected TEXT"); err != nil {
		db.Close()
		t.Fatalf("inject case_imports drift: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close drifted database: %v", err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "second"})
	if err == nil || !strings.Contains(err.Error(), "schema drift") || !strings.Contains(err.Error(), "test_case_import_sources") {
		t.Fatalf("Migrate() error = %v, want test_case_import_sources schema drift", err)
	}
}

func createMigration0002Database(t *testing.T, path string) {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v2"}); err != nil {
		t.Fatalf("seed latest database: %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec("DROP TABLE IF EXISTS test_case_import_sources"); err != nil {
		db.Close()
		t.Fatalf("drop v3 case_imports table: %v", err)
	}
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = 3"); err != nil {
		db.Close()
		t.Fatalf("remove v3 migration record: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		db.Close()
		t.Fatalf("set v2 user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v2 fixture: %v", err)
	}
}
