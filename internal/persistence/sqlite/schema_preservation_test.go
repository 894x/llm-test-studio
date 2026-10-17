package sqlite_test

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestUnsupportedSchemaIsRejectedWithoutChangingDatabaseFiles(t *testing.T) {
	for _, operation := range []string{"migrate", "inspect", "repository"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "previous-format.db")
			options := persistence.MigrateOptions{AppVersion: "preservation-test"}
			if err := persistence.Migrate(t.Context(), path, options); err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t, path)
			_, err := db.ExecContext(
				t.Context(), `UPDATE schema_migrations SET checksum = ?`, strings.Repeat("0", 64),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := directoryHashes(t, directory)
			err = nil
			switch operation {
			case "migrate":
				err = persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "current-test"})
			case "inspect":
				_, err = persistence.SchemaVersion(t.Context(), path)
			case "repository":
				_, err = persistence.OpenRepository(t.Context(), path, persistence.RepositoryOptions{})
			}
			if !errors.Is(err, persistence.ErrSchemaResetRequired) {
				t.Fatalf("unsupported format was accepted: %v", err)
			}
			if !reflect.DeepEqual(before, directoryHashes(t, directory)) {
				t.Fatal("rejected format changed database bytes or sidecar files")
			}
		})
	}
}

func directoryHashes(t *testing.T, directory string) map[string][32]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make(map[string][32]byte, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected fixture entry: %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		hashes[entry.Name()] = sha256.Sum256(data)
	}
	return hashes
}
