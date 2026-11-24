package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

const releasedV4Checksum = "6f11bfabc889db5200b42fabce144729b49e51124b57d603a04bc6762ed73143"

func protocolComparisonV4Fixture(t *testing.T) string {
	t.Helper()
	if checksum := schemaV4Checksum(); checksum != releasedV4Checksum {
		t.Fatalf("released version 4 schema changed: %s", checksum)
	}
	path := historyRecycleV3Fixture(t)
	db := migrationTestDB(t, path)
	defer db.Close()
	for _, object := range historyRecycleObjects {
		if _, err := db.ExecContext(t.Context(), object.ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(4, '0004_history_recycle', ?, '2026-11-01T00:00:00Z', 'v4-fixture')`, releasedV4Checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedLegacyChannelComparison(t *testing.T, path string) domain.Comparison {
	t.Helper()
	ctx := t.Context()
	db := migrationTestDB(t, path)
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := queryCurrentRun(ctx, tx, historicalRunID)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := first.Snapshot()
	secondSnapshot := first.Snapshot()
	secondSnapshot.Channel.ID = "10000000-0000-4000-8000-000000000057"
	secondSnapshot.Mapping.ChannelID = secondSnapshot.Channel.ID
	secondMeta := first.Meta()
	secondMeta.ID, secondMeta.Revision, secondMeta.UpdatedAt = "10000000-0000-4000-8000-000000000058", 1, secondMeta.CreatedAt
	second, err := domain.NewRun(secondMeta, first.PlanID(), secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareRunCreate(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := createRunTx(ctx, tx, prepared); err != nil {
		t.Fatal(err)
	}
	meta := secondMeta
	meta.ID = "10000000-0000-4000-8000-000000000059"
	comparison, err := domain.NewComparison(
		meta,
		firstSnapshot.Plan,
		firstSnapshot.Model.EntityRevisionRef,
		[]domain.ComparisonRunRef{
			{Channel: firstSnapshot.Channel.EntityRevisionRef, RunID: first.Meta().ID},
			{Channel: secondSnapshot.Channel.EntityRevisionRef, RunID: second.Meta().ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	document, err := marshalCanonical(comparison)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comparisons(id, current_revision, created_at, sealed)
		VALUES(?,1,?,0)`, meta.ID, formatTime(meta.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	plan, model := comparison.Plan(), comparison.Model()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO comparison_revisions(comparison_id, schema_version, revision, created_at, updated_at,
			plan_id, plan_revision, model_id, model_revision, status, document_json)
		VALUES(?,?,1,?,?,?,?,?,?,'running',?)`,
		meta.ID, meta.SchemaVersion, formatTime(meta.CreatedAt), formatTime(meta.UpdatedAt),
		plan.ID, plan.Revision, model.ID, model.Revision, document,
	); err != nil {
		t.Fatal(err)
	}
	for position, item := range comparison.Runs() {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO comparison_runs(comparison_id, comparison_revision, position, channel_id, channel_revision, run_id)
			VALUES(?,1,?,?,?,?)`,
			meta.ID, position, item.Channel.ID, item.Channel.Revision, item.RunID,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return comparison
}

func TestProtocolComparisonUpgradePreservesV4AndRestorableBackup(t *testing.T) {
	path := protocolComparisonV4Fixture(t)
	want := seedLegacyChannelComparison(t, path)
	before, err := VerifyRunnerUpgradeDatabase(t.Context(), path)
	const v4BusinessSHA = "4451e7174b44ce1028ca6da7f2b114c9697175d4a6eb4312848f2e7dfa706795"
	if err != nil || before.SchemaVersion != 4 || before.SchemaSHA256 != releasedV4Checksum ||
		before.BusinessSHA256 != v4BusinessSHA {
		t.Fatalf("legacy backup proof: %+v %v", before, err)
	}
	options := MigrateOptions{AppVersion: "protocol-comparison-upgrade"}
	if err := Migrate(t.Context(), path, options); err != nil {
		t.Fatal(err)
	}
	assertMigratedLegacyComparison(t, path, want)
	backups := migrationBackups(t, path)
	if len(backups) != 1 {
		t.Fatalf("backups = %d, want 1", len(backups))
	}
	backup, err := VerifyRunnerUpgradeDatabase(t.Context(), backups[0])
	if err != nil || backup.BusinessSHA256 != before.BusinessSHA256 || backup.Rows != before.Rows {
		t.Fatalf("legacy backup changed: %+v %v", backup, err)
	}
	checksum := migrationFileHash(t, path)
	if err := Migrate(t.Context(), path, options); err != nil {
		t.Fatal(err)
	}
	if migrationFileHash(t, path) != checksum || len(migrationBackups(t, path)) != 1 {
		t.Fatal("repeat open changed the migrated database or created another backup")
	}
	backupData, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, backupData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), restored, options); err != nil {
		t.Fatal(err)
	}
	assertMigratedLegacyComparison(t, restored, want)
}

func assertMigratedLegacyComparison(t *testing.T, path string, want domain.Comparison) {
	t.Helper()
	assertMigratedHistoryReadable(t, path)
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	got, err := repository.GetComparison(t.Context(), want.Meta().ID)
	if err != nil || !reflect.DeepEqual(got.Runs(), want.Runs()) || got.Meta() != want.Meta() {
		t.Fatalf("legacy comparison changed: %+v %v", got.Runs(), err)
	}
	if checksum := readMigrationText(t, path, "SELECT checksum FROM schema_migrations WHERE version=4"); checksum != releasedV4Checksum {
		t.Fatal("released version 4 ledger was rewritten")
	}
	if count := readMigrationText(t, path, "SELECT count(*) FROM comparison_runs WHERE protocol = ''"); count != "2" {
		t.Fatalf("expected both legacy links to retain the channel axis, got %s", count)
	}
}

func TestProtocolComparisonUpgradeCancellationRollsBackAndCanRetry(t *testing.T) {
	path := protocolComparisonV4Fixture(t)
	before := migrationFileHash(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := Migrate(ctx, path, MigrateOptions{
		AppVersion: "protocol-comparison-cancelled",
		Progress: func(progress MigrationProgress) {
			if progress.Stage == "validating" {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || migrationFileHash(t, path) != before {
		t.Fatalf("cancelled upgrade changed source: %v", err)
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 4 {
		t.Fatalf("source version = %d: %v", version, err)
	}
	if len(migrationBackups(t, path)) != 1 {
		t.Fatal("verified recovery backup missing")
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "protocol-comparison-retry"}); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolComparisonUpgradeRejectsV4LedgerMismatch(t *testing.T) {
	path := protocolComparisonV4Fixture(t)
	db := migrationTestDB(t, path)
	if _, err := db.ExecContext(t.Context(), `UPDATE schema_migrations SET checksum='unknown' WHERE version=4`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	before := migrationFileHash(t, path)
	err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "protocol-comparison-reject"})
	if !errors.Is(err, ErrSchemaResetRequired) || migrationFileHash(t, path) != before {
		t.Fatalf("rejected source was mutated or accepted: %v", err)
	}
	if len(migrationBackups(t, path)) != 0 {
		t.Fatal("rejected schema created a backup")
	}
}

func TestProtocolComparisonStorageConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "protocol-comparison-fresh"}); err != nil {
		t.Fatal(err)
	}
	db := migrationTestDB(t, path)
	defer db.Close()
	const insert = `INSERT INTO comparison_runs(comparison_id, comparison_revision, position, channel_id,
		channel_revision, run_id, protocol) VALUES(?, 1, ?, ?, 1, ?, ?)`
	// Repository tests cover ownership. These SQL inserts isolate uniqueness and enum checks.
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"group", 0, "shared", "run-chat", "openai-chat"},
		{"group", 1, "shared", "run-responses", "openai-responses"},
		{"group", 2, "shared", "run-messages", "anthropic-messages"},
		{"legacy", 0, "channel-a", "run-a", ""},
		{"legacy", 1, "channel-b", "run-b", ""},
	} {
		if _, err := db.ExecContext(t.Context(), insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{
		{"group", 3, "other", "run-d", "openai-chat"},
		{"group", 3, "shared", "run-d", "unknown-protocol"},
		{"group", 3, "shared", "run-d", nil},
		{"group", 3, "shared", "run-chat", ""},
		{"legacy", 2, "channel-a", "run-c", ""},
	} {
		if _, err := db.ExecContext(t.Context(), insert, row...); err == nil {
			t.Fatalf("invalid comparison link accepted: %v", row)
		}
	}
	var selectID, parentID, unused int
	var detail string
	query := `EXPLAIN QUERY PLAN SELECT 1 FROM comparison_runs WHERE run_id=? AND protocol<>''`
	err := db.QueryRowContext(t.Context(), query, "run-chat").Scan(&selectID, &parentID, &unused, &detail)
	if err != nil || !strings.Contains(detail, "idx_comparison_runs_protocol_child") {
		t.Fatalf("protocol child lookup query plan = %q: %v", detail, err)
	}
}
