package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestMigrateAddsCatalogTombstonesSchemaV4(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v4.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v4"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'catalog_tombstones'`); got != 1 {
		t.Fatalf("catalog_tombstones table count = %d, want 1", got)
	}
}

func TestMigrateBacksUpExistingV3BeforeApplyingLatestSchema(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "upgrade-v3.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v4"}); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	dropMigration0005Objects(t, db)
	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version >= 4"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE catalog_tombstones"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v4"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, "backups", "upgrade-v3-before-v7-*.db"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("pre-migration backups = %v, error = %v, want one", matches, err)
	}
	backup := openDatabase(t, matches[0])
	defer backup.Close()
	if got := queryInt(t, backup, "PRAGMA user_version"); got != 3 {
		t.Fatalf("backup user_version = %d, want 3", got)
	}
	if tableExists(t, backup, "catalog_tombstones") {
		t.Fatal("v3 backup unexpectedly contains v4 tombstones table")
	}
}
