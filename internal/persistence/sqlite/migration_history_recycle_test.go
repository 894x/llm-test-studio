package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func historyRecycleV3Fixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/migrations/82e10184.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Commit   string             `json:"source_commit"`
		Version  int                `json:"schema_version"`
		Checksum string             `json:"schema_checksum"`
		Schema   []historicalObject `json:"schema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	baseline := historicalBaseline{Commit: fixture.Commit, Version: fixture.Version}
	for _, object := range fixture.Schema {
		baseline.objects = append(baseline.objects, schemaObject{
			kind: object.Kind, name: object.Name, table: object.Table, ddl: object.DDL,
		})
	}
	const releasedChecksum = "ceca1e8965986f9722ba7d8ace907a98d316b849c311fa1066f260963055eafb"
	frozenChecksum := schemaChecksum(schemaV3Name, baseline.objects)
	if fixture.Version != 3 || frozenChecksum != releasedChecksum || fixture.Checksum != releasedChecksum {
		t.Fatalf("frozen version 3 fixture changed: version=%d checksum=%s", fixture.Version, frozenChecksum)
	}
	if schemaV3Checksum() != releasedChecksum {
		t.Fatalf("released version 3 implementation changed: %s", schemaV3Checksum())
	}
	return historicalMigrationFixture(t, baseline)
}

func TestHistoryRecycleUpgradePreservesV3AndRestorableBackup(t *testing.T) {
	path := historyRecycleV3Fixture(t)
	db := migrationTestDB(t, path)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := migrationFacts(t.Context(), conn, schemaV3Objects, false)
	conn.Close()
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 3 {
		t.Fatalf("source version = %d: %v", version, err)
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "history-recycle-upgrade"}); err != nil {
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
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 4 {
		t.Fatalf("upgraded version = %d: %v", version, err)
	}
	if got := readMigrationText(t, path, "SELECT checksum FROM schema_migrations WHERE version=3"); got != schemaV3Checksum() {
		t.Fatal("released version 3 ledger was rewritten")
	}
	backups := migrationBackups(t, path)
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1", len(backups))
	}
	if version, err := SchemaVersion(t.Context(), backups[0]); err != nil || version != 3 {
		t.Fatalf("source backup version = %d: %v", version, err)
	}
	before := migrationFileHash(t, path)
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "repeat"}); err != nil {
		t.Fatal(err)
	}
	if migrationFileHash(t, path) != before || len(migrationBackups(t, path)) != 1 {
		t.Fatal("repeat open changed data or created another backup")
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

func TestHistoryRecycleUpgradeCancellationRollsBackAndCanRetry(t *testing.T) {
	path := historyRecycleV3Fixture(t)
	before := migrationFileHash(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := Migrate(ctx, path, MigrateOptions{
		AppVersion: "cancel-recycle-upgrade",
		Progress: func(progress MigrationProgress) {
			if progress.Stage == "validating" {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || migrationFileHash(t, path) != before {
		t.Fatalf("cancelled upgrade changed source: %v", err)
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 3 {
		t.Fatalf("rollback version = %d: %v", version, err)
	}
	if len(migrationBackups(t, path)) != 1 {
		t.Fatal("verified recovery backup missing")
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "retry"}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRecycleUpgradeRejectsUnsupportedV3WithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "newer", query: "PRAGMA user_version=99"},
		{name: "unknown ledger", query: "UPDATE schema_migrations SET checksum='unknown' WHERE version=3"},
		{name: "corrupt schema", query: "DROP INDEX idx_reports_history"},
		{name: "unknown table", query: "CREATE TABLE unknown_data(id TEXT)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := historyRecycleV3Fixture(t)
			db := migrationTestDB(t, path)
			if _, err := db.ExecContext(t.Context(), test.query); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before := migrationFileHash(t, path)
			err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "reject"})
			if !errors.Is(err, ErrSchemaResetRequired) || migrationFileHash(t, path) != before {
				t.Fatalf("rejected source was mutated or accepted: %v", err)
			}
			if len(migrationBackups(t, path)) != 0 {
				t.Fatal("rejected schema created a backup")
			}
		})
	}
}

func TestHistoryRecycleUpgradeProtectsConcurrentWritersAndWALBackup(t *testing.T) {
	path := historyRecycleV3Fixture(t)
	db := migrationTestDB(t, path)
	defer db.Close()
	for _, query := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"INSERT INTO runs(id, config) VALUES(1001, 'recycle-wal-only')", "BEGIN IMMEDIATE",
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(t.Context(), path, MigrateOptions{
		AppVersion: "competing-writer", BusyTimeout: time.Millisecond,
	}); err == nil {
		t.Fatal("migration ignored competing writer")
	}
	if len(migrationBackups(t, path)) != 0 {
		t.Fatal("blocked migration created a backup")
	}
	if _, err := db.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsFound <- Migrate(t.Context(), path, MigrateOptions{AppVersion: "concurrent"})
		}()
	}
	workers.Wait()
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
	backups := migrationBackups(t, path)
	if len(backups) != 1 {
		t.Fatalf("concurrent migrations created %d backups", len(backups))
	}
	if got := readMigrationText(t, backups[0], "SELECT config FROM runs WHERE id=1001"); got != "recycle-wal-only" {
		t.Fatal("backup omitted committed WAL data")
	}
}

func TestHistoryRecycleFreshSchemaConstraintsAndRestoreCascade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "recycle-fresh"}); err != nil {
		t.Fatal(err)
	}
	db := migrationTestDB(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO history_recycle_entries VALUES('unknown', 'id', 'title', '2026-10-30T00:00:00Z', 0)`,
		`INSERT INTO history_recycle_entries VALUES('job', 'id', 'title', '2026-10-30T00:00:00Z', 2)`,
		`INSERT INTO history_recycle_members VALUES('job', 'missing', 'run', 'id')`,
	} {
		if _, err := db.ExecContext(t.Context(), query); err == nil {
			t.Fatalf("invalid recycle row accepted: %s", query)
		}
	}
	for _, query := range []string{
		`INSERT INTO history_recycle_entries VALUES('job', 'owner', 'title', '2026-10-30T00:00:00Z', 0)`,
		`INSERT INTO history_recycle_members VALUES('job', 'owner', 'run', 'run-id')`,
		`INSERT INTO history_recycle_members VALUES('job', 'owner', 'report', 'report-id')`,
		`DELETE FROM history_recycle_entries WHERE kind='job' AND id='owner'`,
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM history_recycle_members").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("restoring an owner left hidden members")
	}
}
