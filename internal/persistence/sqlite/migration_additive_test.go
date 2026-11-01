package sqlite

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdditiveUpgradeRejectsBrokenForeignKeysWithoutMutation(t *testing.T) {
	for _, source := range []struct {
		name    string
		fixture func(testing.TB) string
	}{
		{name: "v2", fixture: queryIndexesV2Fixture},
		{name: "v3", fixture: historyRecycleV3Fixture},
	} {
		t.Run(source.name, func(t *testing.T) {
			path := source.fixture(t)
			db := migrationTestDB(t, path)
			if _, err := db.ExecContext(t.Context(), "UPDATE run_results SET run_id=1000000"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before := migrationFileHash(t, path)
			migrating := false
			err := Migrate(t.Context(), path, MigrateOptions{
				AppVersion: "broken-additive-source",
				Progress: func(progress MigrationProgress) {
					migrating = migrating || progress.Stage == "migrating"
				},
			})
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("broken source was not rejected by backup verification: %v", err)
			}
			if migrating || migrationFileHash(t, path) != before {
				t.Fatal("invalid source reached migration or changed database bytes")
			}
		})
	}
}

// The optional source must be a standalone, consistent backup, never a live
// WAL database. Each iteration upgrades its own copy and retains no user data.
func BenchmarkAdditiveMigration(b *testing.B) {
	for _, source := range []struct {
		name    string
		fixture func(testing.TB) string
	}{
		{name: "v2_32MiB", fixture: queryIndexesV2Fixture},
		{name: "v3_32MiB", fixture: historyRecycleV3Fixture},
	} {
		b.Run(source.name, func(b *testing.B) {
			b.StopTimer()
			path := source.fixture(b)
			db := migrationTestDB(b, path)
			payload := strings.Repeat("h", 32*1024*1024)
			if _, err := db.ExecContext(b.Context(), "UPDATE run_results SET response_body=?", payload); err != nil {
				b.Fatal(err)
			}
			db.Close()
			benchmarkAdditiveMigration(b, path)
		})
	}
	if source := os.Getenv("LLM_TEST_MIGRATION_BENCH_SOURCE"); source != "" {
		b.Run("backup_copy", func(b *testing.B) {
			b.StopTimer()
			for _, suffix := range []string{"-wal", "-journal"} {
				if info, err := os.Stat(source + suffix); err == nil && info.Size() > 0 {
					b.Fatal("benchmark source must be a standalone backup")
				}
			}
			benchmarkAdditiveMigration(b, source)
		})
	}
}

func benchmarkAdditiveMigration(b *testing.B, source string) {
	b.Helper()
	info, err := os.Stat(source)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(info.Size())
	b.ReportAllocs()
	stages := map[string]time.Duration{}
	for range b.N {
		path := filepath.Join(b.TempDir(), "workspace.db")
		input, err := os.Open(source)
		if err != nil {
			b.Fatal(err)
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			b.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		input.Close()
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			b.Fatalf("copy benchmark source: %v, close: %v", copyErr, closeErr)
		}
		var stage string
		var started time.Time
		b.StartTimer()
		err = Migrate(b.Context(), path, MigrateOptions{
			AppVersion: "migration-benchmark",
			Progress: func(progress MigrationProgress) {
				now := time.Now()
				if stage != "" {
					stages[stage] += now.Sub(started)
				}
				stage, started = progress.Stage, now
			},
		})
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if version, err := SchemaVersion(b.Context(), path); err != nil || version != CurrentSchemaVersion {
			b.Fatalf("benchmark target version=%d: %v", version, err)
		}
		verifyBenchmarkMigrationPreservation(b, source, path)
	}
	for stage, elapsed := range stages {
		b.ReportMetric(float64(elapsed.Microseconds())/1000/float64(b.N), stage+"-ms/op")
	}
}

func verifyBenchmarkMigrationPreservation(b *testing.B, sourcePath, targetPath string) {
	b.Helper()
	version, err := SchemaVersion(b.Context(), sourcePath)
	if err != nil || (version != 2 && version != 3) {
		b.Fatalf("benchmark requires a v2/v3 source, got version=%d: %v", version, err)
	}
	objects := schemaV3Objects
	if version == 2 {
		objects = schemaV2Objects
	}
	source, err := openRunnerBackupSource(b.Context(), sourcePath)
	if err != nil {
		b.Fatal(err)
	}
	facts, err := migrationFacts(b.Context(), source.conn, objects, false)
	source.close()
	if err != nil {
		b.Fatal(err)
	}
	backups, err := filepath.Glob(targetPath + ".upgrade-backup-*/database.sqlite")
	if err != nil || len(backups) != 1 {
		b.Fatalf("benchmark backup count=%d: %v", len(backups), err)
	}
	for _, path := range []string{backups[0], targetPath} {
		target, err := openRunnerBackupSource(b.Context(), path)
		if err != nil {
			b.Fatal(err)
		}
		for _, fact := range facts {
			if path == targetPath && fact.table == "schema_migrations" {
				continue
			}
			if err := verifyMigrationFacts(b.Context(), target.conn, []migrationTableFacts{fact}); err != nil {
				target.close()
				b.Fatal(err)
			}
		}
		target.close()
	}
}
