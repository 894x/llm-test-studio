package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func queryIndexesV2Fixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/migrations/e72dc1fc.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Commit string             `json:"source_commit"`
		Schema []historicalObject `json:"schema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	baseline := historicalBaseline{Commit: fixture.Commit, Version: 2}
	for _, object := range fixture.Schema {
		baseline.objects = append(baseline.objects, schemaObject{
			kind: object.Kind, name: object.Name, table: object.Table, ddl: object.DDL,
		})
	}
	if got := schemaChecksum(schemaV2Name, baseline.objects); got != schemaV2Checksum() ||
		got != "b51d41e07c0dfb885a7198c092bef3631b7734fb129159011a6575d10774699d" {
		t.Fatalf("released v2 schema changed: %s", got)
	}
	return historicalMigrationFixture(t, baseline)
}

func TestQueryIndexUpgradePreservesV2DataAndRestorableBackup(t *testing.T) {
	path := queryIndexesV2Fixture(t)
	db := migrationTestDB(t, path)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := migrationFacts(t.Context(), conn, schemaV2Objects, false)
	conn.Close()
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "query-index-upgrade"}); err != nil {
		t.Fatal(err)
	}
	assertMigratedHistoryReadable(t, path)
	db = migrationTestDB(t, path)
	conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.table != "schema_migrations" {
			if err := verifyMigrationFacts(t.Context(), conn, []migrationTableFacts{fact}); err != nil {
				t.Fatal(err)
			}
		}
	}
	conn.Close()
	db.Close()
	backups := migrationBackups(t, path)
	if len(backups) != 1 {
		t.Fatalf("backup count = %d", len(backups))
	}
	if version, err := SchemaVersion(t.Context(), backups[0]); err != nil || version != 2 {
		t.Fatalf("backup version = %d: %v", version, err)
	}
	before := migrationFileHash(t, path)
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "repeat"}); err != nil {
		t.Fatal(err)
	}
	if migrationFileHash(t, path) != before || len(migrationBackups(t, path)) != 1 {
		t.Fatal("repeat open mutated data or made another backup")
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	raw, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restored, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), restored, MigrateOptions{AppVersion: "restored"}); err != nil {
		t.Fatal(err)
	}
	assertMigratedHistoryReadable(t, restored)
}

func TestQueryIndexUpgradeCancellationRollsBackAndCanRetry(t *testing.T) {
	path := queryIndexesV2Fixture(t)
	before := migrationFileHash(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := Migrate(ctx, path, MigrateOptions{
		AppVersion: "cancel-index-build",
		Progress: func(progress MigrationProgress) {
			if progress.Stage == "validating" {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || migrationFileHash(t, path) != before {
		t.Fatalf("cancelled upgrade mutated source: %v", err)
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 2 {
		t.Fatalf("rollback version = %d: %v", version, err)
	}
	if len(migrationBackups(t, path)) != 1 {
		t.Fatal("verified recovery backup missing")
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "retry"}); err != nil {
		t.Fatal(err)
	}
}
