package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	persistence "github.com/894x/llm-studio/internal/persistence/sqlite"
)

func TestMigrateAddsBuiltInCatalogSeedStateV7(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v7.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "test-v7"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != persistence.CurrentSchemaVersion {
		t.Fatalf("user_version = %d, want %d", got, persistence.CurrentSchemaVersion)
	}
	if !tableExists(t, db, "builtin_catalog_seeds") {
		t.Fatal("migration did not create builtin_catalog_seeds")
	}

	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	completed, err := repository.BuiltinCatalogSeedCompleted(context.Background(), "kimi-k3-official-v1")
	if err != nil || completed {
		t.Fatalf("initial seed state = %v, %v, want false", completed, err)
	}
	if err := repository.CompleteBuiltinCatalogSeed(context.Background(), "kimi-k3-official-v1", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	completed, err = repository.BuiltinCatalogSeedCompleted(context.Background(), "kimi-k3-official-v1")
	if err != nil || !completed {
		t.Fatalf("completed seed state = %v, %v, want true", completed, err)
	}
}
