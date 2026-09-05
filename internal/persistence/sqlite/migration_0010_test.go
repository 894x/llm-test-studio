package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const (
	v10PlanID       = "10101010-1010-4010-8010-101010101001"
	v10ModelID      = "10101010-1010-4010-8010-101010101002"
	v10ChannelOneID = "10101010-1010-4010-8010-101010101003"
	v10ChannelTwoID = "10101010-1010-4010-8010-101010101004"
	v10MappingOneID = "10101010-1010-4010-8010-101010101005"
	v10MappingTwoID = "10101010-1010-4010-8010-101010101006"
	v10CaseID       = "10101010-1010-4010-8010-101010101007"
	v10RunOneID     = "10101010-1010-4010-8010-101010101008"
	v10RunTwoID     = "10101010-1010-4010-8010-101010101009"
	v10ReportID     = "10101010-1010-4010-8010-10101010100a"
	v10ComparisonID = "10101010-1010-4010-8010-10101010100b"
)

type v10Fixture struct {
	model      domain.Model
	channels   []domain.Channel
	plan       domain.Plan
	mappings   []domain.ChannelModel
	testCase   domain.TestCase
	runs       []domain.Run
	report     domain.Report
	comparison domain.Comparison
}

func TestMigrateV10FreshDatabaseRecordsSnapshotBackfill(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh-v10.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "fresh-v10"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != 10 {
		t.Fatalf("user_version = %d, want 10", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 10 AND name = '0010_run_snapshot_v2_backfill'`); got != 1 {
		t.Fatalf("snapshot backfill migration rows = %d, want 1", got)
	}
	assertDatabaseIntegrity(t, db)
}

func TestMigrateV10BackfillsEveryLegacyRunRevisionAndReport(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-legacy.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	fixture := seedV10Fixture(t, db, 1)
	beforeChecksums := migrationChecksums(t, db, 9)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()

	wantSnapshots := map[string]domain.RunSnapshot{}
	for _, run := range fixture.runs {
		wantSnapshots[run.Meta().ID] = completeV10Snapshot(t, fixture, run.Snapshot())
	}
	rows, err := db.Query(`SELECT run_id, revision, snapshot_json, document_json FROM execution_run_revisions ORDER BY run_id, revision`)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for rows.Next() {
		var runID string
		var revision int
		var snapshotJSON, documentJSON []byte
		if err := rows.Scan(&runID, &revision, &snapshotJSON, &documentJSON); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var snapshot domain.RunSnapshot
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil || snapshot.Validate() != nil {
			rows.Close()
			t.Fatalf("run %s revision %d snapshot is not valid v2: decode=%v validate=%v", runID, revision, err, snapshot.Validate())
		}
		if snapshot.SchemaVersion != domain.CurrentRunSnapshotSchemaVersion ||
			!bytes.Equal(canonicalV10JSON(t, snapshot), canonicalV10JSON(t, wantSnapshots[runID])) {
			rows.Close()
			t.Fatalf("run %s revision %d snapshot = %#v, want %#v", runID, revision, snapshot, wantSnapshots[runID])
		}
		var document domain.Run
		if err := json.Unmarshal(documentJSON, &document); err != nil {
			rows.Close()
			t.Fatalf("decode run %s revision %d document: %v", runID, revision, err)
		}
		if err := document.Validate(); err != nil || !reflect.DeepEqual(document.Snapshot(), snapshot) {
			rows.Close()
			t.Fatalf("run %s revision %d document mismatch: validate=%v", runID, revision, err)
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if seen != 3 {
		t.Fatalf("upgraded run revisions = %d, want 3", seen)
	}

	var reportJSON []byte
	if err := db.QueryRow(`SELECT document_json FROM reports WHERE id = ?`, v10ReportID).Scan(&reportJSON); err != nil {
		t.Fatal(err)
	}
	var report domain.Report
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("upgraded report validation error = %v", err)
	}
	if !bytes.Equal(canonicalV10JSON(t, report.PlanSnapshot), canonicalV10JSON(t, wantSnapshots[v10RunOneID])) {
		t.Fatalf("report snapshot = %#v, want current run snapshot %#v", report.PlanSnapshot, wantSnapshots[v10RunOneID])
	}
	if got := migrationChecksums(t, db, 9); !equalStrings(got, beforeChecksums) {
		t.Fatalf("historical migration checksums changed: before=%v after=%v", beforeChecksums, got)
	}
	assertExecutionRunRevisionGuardsV8(t, db, v10RunTwoID)
	assertV10ComparisonGuards(t, db)
	assertDatabaseIntegrity(t, db)

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err := persistence.OpenLegacyCatalogRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenLegacyCatalogRepository() error = %v", err)
	}
	if _, err := repository.GetRun(context.Background(), v10RunOneID); err != nil {
		repository.Close()
		t.Fatalf("GetRun() after backfill error = %v", err)
	}
	if projections, err := repository.ListRunProjections(context.Background()); err != nil || len(projections) != 2 {
		repository.Close()
		t.Fatalf("ListRunProjections() = (%d, %v), want two valid projections", len(projections), err)
	}
	if projections, err := repository.ListReportProjections(context.Background()); err != nil || len(projections) != 1 {
		repository.Close()
		t.Fatalf("ListReportProjections() = (%d, %v), want one valid projection", len(projections), err)
	}
	if _, err := repository.GetComparison(context.Background(), v10ComparisonID); err != nil {
		repository.Close()
		t.Fatalf("GetComparison() after backfill error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV10LeavesV2RunAndReportBytesUntouched(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-v2.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	seedV10Fixture(t, db, domain.CurrentRunSnapshotSchemaVersion)
	beforeRuns := queryV10Documents(t, db, `SELECT run_id || ':' || revision, snapshot_json || char(0) || document_json FROM execution_run_revisions ORDER BY run_id, revision`)
	beforeReports := queryV10Documents(t, db, `SELECT id, document_json FROM reports ORDER BY id`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-v2"}); err != nil {
		t.Fatalf("upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	afterRuns := queryV10Documents(t, db, `SELECT run_id || ':' || revision, snapshot_json || char(0) || document_json FROM execution_run_revisions ORDER BY run_id, revision`)
	afterReports := queryV10Documents(t, db, `SELECT id, document_json FROM reports ORDER BY id`)
	if !reflect.DeepEqual(afterRuns, beforeRuns) {
		t.Fatalf("v2 run bytes changed: before=%q after=%q", beforeRuns, afterRuns)
	}
	if !reflect.DeepEqual(afterReports, beforeReports) {
		t.Fatalf("v2 report bytes changed: before=%q after=%q", beforeReports, afterReports)
	}
}

func TestMigrateV10BackfillsTargetlessLegacyRunFromExactBinding(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-targetless.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	fixture := seedV10Fixture(t, db, 1)
	fixture = makeV10FixtureTargetless(t, db, fixture)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-targetless"}); err != nil {
		t.Fatalf("targetless upgrade Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	var snapshotJSON []byte
	if err := db.QueryRow(`SELECT snapshot_json FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, v10RunOneID).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("targetless upgraded snapshot validation error = %v", err)
	}
	if snapshot.PlanDocument == nil || len(snapshot.PlanDocument.ModelIDs) != 0 || len(snapshot.PlanDocument.ChannelIDs) != 0 {
		t.Fatalf("targetless plan document = %#v, want empty allowlists", snapshot.PlanDocument)
	}
	if snapshot.Mapping == nil || snapshot.Mapping.ID != fixture.mappings[0].ID || snapshot.Mapping.Revision != fixture.mappings[0].Revision {
		t.Fatalf("targetless exact mapping = %#v, want %s revision %d", snapshot.Mapping, fixture.mappings[0].ID, fixture.mappings[0].Revision)
	}
	assertDatabaseIntegrity(t, db)
}

func TestMigrateV10BackfillsTargetlessRunFromMappingRevisionCurrentAtRunCreation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-targetless-ambiguous.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	fixture := makeV10FixtureTargetless(t, db, seedV10Fixture(t, db, 1))
	second := fixture.mappings[0]
	second.Revision = 2
	second.UpdatedAt = second.UpdatedAt.Add(time.Second)
	second.UpstreamModelName = "updated-after-run"
	if err := second.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO channel_models(
			id, schema_version, revision, created_at, updated_at,
			channel_id, channel_revision, model_id, model_revision, binding_key, document_json
		) VALUES(?, 1, 2, ?, ?, ?, 1, ?, 1, ?, ?)
	`, second.ID, formatV10Time(second.CreatedAt), formatV10Time(second.UpdatedAt), second.ChannelID, second.ModelID,
		second.ChannelID+"|"+second.ModelID, canonicalV10JSON(t, second)); err != nil {
		db.Close()
		t.Fatalf("insert ambiguous exact mapping revision: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-targetless-history"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != 10 {
		t.Fatalf("user_version after targetless mapping history migration = %d, want 10", got)
	}
	var snapshotJSON []byte
	if err := db.QueryRow(`SELECT snapshot_json FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, v10RunOneID).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("upgraded targetless snapshot validation error = %v", err)
	}
	if snapshot.Mapping == nil || snapshot.Mapping.ID != fixture.mappings[0].ID || snapshot.Mapping.Revision != fixture.mappings[0].Revision ||
		snapshot.Mapping.UpstreamModelName != fixture.mappings[0].UpstreamModelName {
		t.Fatalf("targetless mapping = %#v, want revision current at run creation %#v", snapshot.Mapping, fixture.mappings[0])
	}
}

func TestMigrateV10BackfillsTargetlessRunAfterModelAndChannelAdvanceWithoutMappingRewrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-targetless-independent-revisions.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	fixture := makeV10FixtureTargetless(t, db, seedV10Fixture(t, db, 1))

	model := fixture.model
	model.Revision = 2
	channel := fixture.channels[0]
	channel.Revision = 2
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := channel.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, 1, 2, ?, ?, ?)
	`, model.ID, formatV10Time(model.CreatedAt), formatV10Time(model.UpdatedAt), canonicalV10JSON(t, model)); err != nil {
		db.Close()
		t.Fatalf("insert independent model revision: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO channels(id, schema_version, revision, created_at, updated_at, credential_id, credential_revision, document_json)
		VALUES(?, 1, 2, ?, ?, NULL, NULL, ?)
	`, channel.ID, formatV10Time(channel.CreatedAt), formatV10Time(channel.UpdatedAt), canonicalV10JSON(t, channel)); err != nil {
		db.Close()
		t.Fatalf("insert independent channel revision: %v", err)
	}
	replaceV10RunUpdateGuard(t, db, false)
	if _, err := db.Exec(`
		UPDATE execution_run_revisions
		SET snapshot_json = CAST(json_set(snapshot_json, '$.model.revision', 2, '$.channel.revision', 2) AS BLOB),
		    document_json = CAST(json_set(document_json, '$.plan_snapshot.model.revision', 2, '$.plan_snapshot.channel.revision', 2) AS BLOB)
		WHERE run_id = ?
	`, v10RunOneID); err != nil {
		db.Close()
		t.Fatalf("advance targetless run subject revisions: %v", err)
	}
	replaceV10RunUpdateGuard(t, db, true)
	if _, err := db.Exec(`
		UPDATE reports
		SET document_json = CAST(json_set(document_json, '$.plan_snapshot.model.revision', 2, '$.plan_snapshot.channel.revision', 2) AS BLOB)
		WHERE id = ?
	`, v10ReportID); err != nil {
		db.Close()
		t.Fatalf("advance targetless report subject revisions: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-targetless-independent-revisions"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	var snapshotJSON []byte
	if err := db.QueryRow(`SELECT snapshot_json FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, v10RunOneID).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("upgraded targetless snapshot validation error = %v", err)
	}
	if snapshot.Model.Revision != 2 || snapshot.Channel.Revision != 2 || snapshot.Mapping == nil ||
		snapshot.Mapping.ID != fixture.mappings[0].ID || snapshot.Mapping.Revision != fixture.mappings[0].Revision {
		t.Fatalf("targetless snapshot = %#v, want subject revisions 2 and original mapping %#v", snapshot, fixture.mappings[0])
	}
}

func TestMigrateV10BackfillsTargetlessRunUsingMappingTombstoneStateAtRunCreation(t *testing.T) {
	for _, test := range []struct {
		name            string
		replacementTime func(time.Time) time.Time
		wantReplacement bool
	}{
		{
			name:            "replacement active when run was created",
			replacementTime: func(at time.Time) time.Time { return at },
			wantReplacement: true,
		},
		{
			name:            "replacement created after run",
			replacementTime: func(at time.Time) time.Time { return at.Add(time.Second) },
			wantReplacement: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upgrade-v9-targetless-tombstone.db")
			createMigration0009Fixture(t, path)
			db := openDatabase(t, path)
			fixture := makeV10FixtureTargetless(t, db, seedV10Fixture(t, db, 1))
			runCreatedAt := fixture.runs[0].Meta().CreatedAt
			replacement := fixture.mappings[0]
			replacement.ID = "10101010-1010-4010-8010-10101010100c"
			replacement.Revision = 2
			replacement.CreatedAt = test.replacementTime(runCreatedAt)
			replacement.UpdatedAt = replacement.CreatedAt
			if err := replacement.Validate(); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := db.Exec(`
				INSERT INTO catalog_tombstones(entity_table, entity_id, deleted_revision, deleted_at)
				VALUES('channel_models', ?, 2, ?)
			`, fixture.mappings[0].ID, formatV10Time(replacement.CreatedAt)); err != nil {
				db.Close()
				t.Fatalf("insert mapping tombstone: %v", err)
			}
			if _, err := db.Exec(`
				INSERT INTO channel_models(
					id, schema_version, revision, created_at, updated_at,
					channel_id, channel_revision, model_id, model_revision, binding_key, document_json
				) VALUES(?, 1, 2, ?, ?, ?, 1, ?, 1, ?, ?)
			`, replacement.ID, formatV10Time(replacement.CreatedAt), formatV10Time(replacement.UpdatedAt),
				replacement.ChannelID, replacement.ModelID, replacement.ChannelID+"|"+replacement.ModelID,
				canonicalV10JSON(t, replacement)); err != nil {
				db.Close()
				t.Fatalf("insert replacement mapping: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-targetless-tombstone"}); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			db = openDatabase(t, path)
			defer db.Close()
			var snapshotJSON []byte
			if err := db.QueryRow(`SELECT snapshot_json FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, v10RunOneID).Scan(&snapshotJSON); err != nil {
				t.Fatal(err)
			}
			var snapshot domain.RunSnapshot
			if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			want := fixture.mappings[0]
			if test.wantReplacement {
				want = replacement
			}
			if snapshot.Mapping == nil || snapshot.Mapping.ID != want.ID || snapshot.Mapping.Revision != want.Revision {
				t.Fatalf("targetless mapping = %#v, want mapping active at run creation %#v", snapshot.Mapping, want)
			}
		})
	}
}

func TestMigrateV10BackfillsTargetlessRunUsingLegacyMappingSelectionOrder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-targetless-selection-order.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	fixture := makeV10FixtureTargetless(t, db, seedV10Fixture(t, db, 1))
	second := fixture.mappings[0]
	second.ID = "10101010-1010-4010-8010-10101010100c"
	second.Revision = 2
	if err := second.Validate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO channel_models(
			id, schema_version, revision, created_at, updated_at,
			channel_id, channel_revision, model_id, model_revision, binding_key, document_json
		) VALUES(?, 1, 2, ?, ?, ?, 1, ?, 1, ?, ?)
	`, second.ID, formatV10Time(second.CreatedAt), formatV10Time(second.UpdatedAt), second.ChannelID, second.ModelID,
		second.ChannelID+"|"+second.ModelID, canonicalV10JSON(t, second)); err != nil {
		db.Close()
		t.Fatalf("insert second live mapping: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-targetless-selection-order"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	var snapshotJSON []byte
	if err := db.QueryRow(`SELECT snapshot_json FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, v10RunOneID).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	var snapshot domain.RunSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Mapping == nil || snapshot.Mapping.ID != fixture.mappings[0].ID || snapshot.Mapping.Revision != fixture.mappings[0].Revision {
		t.Fatalf("targetless mapping = %#v, want deterministic first legacy mapping %#v", snapshot.Mapping, fixture.mappings[0])
	}
}

func TestMigrateV10RevalidatesCompletedSnapshotPostconditions(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB)
		want   string
	}{
		{
			name: "run snapshot",
			tamper: func(t *testing.T, db *sql.DB) {
				replaceV10RunUpdateGuard(t, db, false)
				if _, err := db.Exec(`UPDATE execution_run_revisions SET snapshot_json = json_set(snapshot_json, '$.schema_version', 1) WHERE run_id = ?`, v10RunOneID); err != nil {
					t.Fatal(err)
				}
				replaceV10RunUpdateGuard(t, db, true)
			},
			want: "run snapshot completion invariant failed",
		},
		{
			name: "run document",
			tamper: func(t *testing.T, db *sql.DB) {
				replaceV10RunUpdateGuard(t, db, false)
				if _, err := db.Exec(`UPDATE execution_run_revisions SET document_json = json_set(document_json, '$.plan_snapshot.schema_version', NULL) WHERE run_id = ?`, v10RunOneID); err != nil {
					t.Fatal(err)
				}
				replaceV10RunUpdateGuard(t, db, true)
			},
			want: "run document completion invariant failed",
		},
		{
			name: "report snapshot",
			tamper: func(t *testing.T, db *sql.DB) {
				if _, err := db.Exec(`UPDATE reports SET document_json = json_set(document_json, '$.plan_snapshot.schema_version', 1) WHERE id = ?`, v10ReportID); err != nil {
					t.Fatal(err)
				}
			},
			want: "report snapshot completion invariant failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "completed-v10.db")
			createMigration0009Fixture(t, path)
			db := openDatabase(t, path)
			seedV10Fixture(t, db, 1)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "complete-v10"}); err != nil {
				t.Fatal(err)
			}
			db = openDatabase(t, path)
			test.tamper(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "revalidate-v10"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Migrate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func replaceV10RunUpdateGuard(t *testing.T, db *sql.DB, create bool) {
	t.Helper()
	if !create {
		if _, err := db.Exec(`DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := db.Exec(`
		CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END
	`); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV10InvalidLegacyCatalogDocumentRollsBackAtomically(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "upgrade-v9-corrupt.db")
	createMigration0009Fixture(t, path)
	db := openDatabase(t, path)
	seedV10Fixture(t, db, 1)
	before := queryV10Documents(t, db, `SELECT run_id || ':' || revision, snapshot_json || char(0) || document_json FROM execution_run_revisions ORDER BY run_id, revision`)
	if _, err := db.Exec(`UPDATE test_cases SET document_json = '{"api_key":"sk-sensitive-value"}' WHERE id = ? AND revision = 1`, v10CaseID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "upgrade-v10-corrupt"})
	if err == nil || !strings.Contains(err.Error(), "exact test case document is invalid") {
		t.Fatalf("Migrate() error = %v, want exact test case corruption", err)
	}
	if strings.Contains(err.Error(), "sk-sensitive-value") {
		t.Fatalf("Migrate() error leaked catalog content: %v", err)
	}
	db = openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, "PRAGMA user_version"); got != 9 {
		t.Fatalf("user_version after failed migration = %d, want 9", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 10`); got != 0 {
		t.Fatalf("failed migration history rows = %d, want 0", got)
	}
	after := queryV10Documents(t, db, `SELECT run_id || ':' || revision, snapshot_json || char(0) || document_json FROM execution_run_revisions ORDER BY run_id, revision`)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed migration changed run bytes: before=%q after=%q", before, after)
	}
	assertExecutionRunRevisionGuardsV8(t, db, v10RunTwoID)
}

func assertV10ComparisonGuards(t *testing.T, db *sql.DB) {
	t.Helper()
	if !indexExists(t, db, "idx_comparison_revisions_updated") {
		t.Fatal("idx_comparison_revisions_updated does not exist")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'comparison_revisions_no_%'`); got != 2 {
		t.Fatalf("comparison revision trigger count = %d, want 2", got)
	}
	if _, err := db.Exec(`UPDATE comparison_revisions SET status = 'completed' WHERE comparison_id = ? AND revision = 1`, v10ComparisonID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("update comparison revision error = %v, want immutable rejection", err)
	}
	if _, err := db.Exec(`DELETE FROM comparison_revisions WHERE comparison_id = ? AND revision = 1`, v10ComparisonID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("delete comparison revision error = %v, want immutable rejection", err)
	}
}

func createMigration0009Fixture(t *testing.T, path string) {
	t.Helper()
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "seed-v9"}); err != nil {
		t.Fatal(err)
	}
	if persistence.CurrentSchemaVersion == 9 {
		return
	}
	db := openDatabase(t, path)
	defer db.Close()
	for _, statement := range []string{
		"DELETE FROM schema_migrations WHERE version >= 10",
		"PRAGMA user_version = 9",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("construct v9 fixture with %q: %v", statement, err)
		}
	}
}

func seedV10Fixture(t *testing.T, db *sql.DB, snapshotVersion int) v10Fixture {
	t.Helper()
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	model := domain.Model{EntityMeta: meta(v10ModelID), Name: "v10 model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}}
	channels := []domain.Channel{
		{EntityMeta: meta(v10ChannelOneID), Name: "v10 primary", BaseURL: "https://primary.example/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true},
		{EntityMeta: meta(v10ChannelTwoID), Name: "v10 secondary", BaseURL: "https://secondary.example/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true},
	}
	mappings := []domain.ChannelModel{
		{EntityMeta: meta(v10MappingOneID), ChannelID: channels[0].ID, ModelID: model.ID, UpstreamModelName: "v10-upstream-primary"},
		{EntityMeta: meta(v10MappingTwoID), ChannelID: channels[1].ID, ModelID: model.ID, UpstreamModelName: "v10-upstream-secondary"},
	}
	testCase := domain.TestCase{
		EntityMeta: meta(v10CaseID), Key: "V10", Name: "v10 case", Dimension: "migration",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("request.single"), TypeVersion: 1,
			Spec: json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`),
		},
	}
	caseRef := domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision}
	plan := domain.Plan{
		EntityMeta: meta(v10PlanID), Name: "v10 plan", ModelIDs: []string{model.ID},
		ChannelIDs: []string{channels[0].ID, channels[1].ID}, Cases: []domain.CaseRevisionRef{caseRef},
		Load: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000},
		SLA:  domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1000}},
	}
	for kind, value := range map[string]interface{ Validate() error }{
		"model": model, "channel one": channels[0], "channel two": channels[1],
		"mapping one": mappings[0], "mapping two": mappings[1], "case": testCase, "plan": plan,
	} {
		if err := value.Validate(); err != nil {
			t.Fatalf("invalid %s fixture: %v", kind, err)
		}
	}

	for _, value := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json) VALUES(?, 1, 1, ?, ?, ?)`, []any{model.ID, formatV10Time(now), formatV10Time(now), canonicalV10JSON(t, model)}},
		{`INSERT INTO channels(id, schema_version, revision, created_at, updated_at, credential_id, credential_revision, document_json) VALUES(?, 1, 1, ?, ?, NULL, NULL, ?)`, []any{channels[0].ID, formatV10Time(now), formatV10Time(now), canonicalV10JSON(t, channels[0])}},
		{`INSERT INTO channels(id, schema_version, revision, created_at, updated_at, credential_id, credential_revision, document_json) VALUES(?, 1, 1, ?, ?, NULL, NULL, ?)`, []any{channels[1].ID, formatV10Time(now), formatV10Time(now), canonicalV10JSON(t, channels[1])}},
		{`INSERT INTO channel_models(id, schema_version, revision, created_at, updated_at, channel_id, channel_revision, model_id, model_revision, binding_key, document_json) VALUES(?, 1, 1, ?, ?, ?, 1, ?, 1, ?, ?)`, []any{mappings[0].ID, formatV10Time(now), formatV10Time(now), mappings[0].ChannelID, model.ID, mappings[0].ChannelID + "|" + model.ID, canonicalV10JSON(t, mappings[0])}},
		{`INSERT INTO channel_models(id, schema_version, revision, created_at, updated_at, channel_id, channel_revision, model_id, model_revision, binding_key, document_json) VALUES(?, 1, 1, ?, ?, ?, 1, ?, 1, ?, ?)`, []any{mappings[1].ID, formatV10Time(now), formatV10Time(now), mappings[1].ChannelID, model.ID, mappings[1].ChannelID + "|" + model.ID, canonicalV10JSON(t, mappings[1])}},
		{`INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json) VALUES(?, 1, 1, ?, ?, ?)`, []any{testCase.ID, formatV10Time(now), formatV10Time(now), canonicalV10JSON(t, testCase)}},
		{`INSERT INTO test_plans(id, schema_version, revision, created_at, updated_at, suite_id, suite_revision, document_json) VALUES(?, 1, 1, ?, ?, NULL, NULL, ?)`, []any{plan.ID, formatV10Time(now), formatV10Time(now), canonicalV10JSON(t, plan)}},
		{`INSERT INTO plan_models(plan_id, plan_revision, position, model_id, model_revision) VALUES(?, 1, 0, ?, 1)`, []any{plan.ID, model.ID}},
		{`INSERT INTO plan_channels(plan_id, plan_revision, position, channel_id, channel_revision) VALUES(?, 1, 0, ?, 1)`, []any{plan.ID, channels[0].ID}},
		{`INSERT INTO plan_channels(plan_id, plan_revision, position, channel_id, channel_revision) VALUES(?, 1, 1, ?, 1)`, []any{plan.ID, channels[1].ID}},
		{`INSERT INTO plan_cases(plan_id, plan_revision, position, case_id, case_revision) VALUES(?, 1, 0, ?, 1)`, []any{plan.ID, testCase.ID}},
		{`INSERT INTO plan_channel_models(plan_id, plan_revision, position, channel_id, model_id, mapping_id, mapping_revision) VALUES(?, 1, 0, ?, ?, ?, 1)`, []any{plan.ID, channels[0].ID, model.ID, mappings[0].ID}},
		{`INSERT INTO plan_channel_models(plan_id, plan_revision, position, channel_id, model_id, mapping_id, mapping_revision) VALUES(?, 1, 1, ?, ?, ?, 1)`, []any{plan.ID, channels[1].ID, model.ID, mappings[1].ID}},
	} {
		if _, err := db.Exec(value.query, value.args...); err != nil {
			t.Fatalf("seed v10 catalog: %v\nSQL: %s", err, value.query)
		}
	}

	environment := domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "v10-test", EngineVersion: "v10-test"}
	snapshot := func(index int) domain.RunSnapshot {
		value := domain.RunSnapshot{
			SchemaVersion: snapshotVersion,
			Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
			Model: domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: model.Revision},
				Name: model.Name, Protocol: model.Protocol, Capabilities: append([]string(nil), model.Capabilities...)},
			Channel: domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channels[index].ID, Revision: channels[index].Revision},
				Name: channels[index].Name, BaseURL: channels[index].BaseURL, Protocol: channels[index].Protocol, UpstreamModelName: mappings[index].UpstreamModelName},
			Cases: []domain.CaseRevisionRef{caseRef}, Load: plan.Load, SLA: plan.SLA, Environment: environment,
		}
		if snapshotVersion == domain.CurrentRunSnapshotSchemaVersion {
			planCopy, mappingCopy := plan, mappings[index]
			value.PlanDocument = &planCopy
			value.Mapping = &mappingCopy
			value.CaseDefinitions = []domain.TestCase{testCase}
		}
		if err := value.Validate(); err != nil {
			t.Fatalf("invalid v10 snapshot: %v", err)
		}
		return value
	}
	runOne, err := domain.NewRun(meta(v10RunOneID), plan.ID, snapshot(0))
	if err != nil {
		t.Fatal(err)
	}
	runOneTerminal, err := runOne.Transition(domain.RunCancelled, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	runTwo, err := domain.NewRun(meta(v10RunTwoID), plan.ID, snapshot(1))
	if err != nil {
		t.Fatal(err)
	}
	insertV10Run(t, db, []domain.Run{runOne, runOneTerminal}, true)
	insertV10Run(t, db, []domain.Run{runTwo}, false)

	report := domain.Report{
		SchemaVersion: domain.CurrentReportSchemaVersion, ID: v10ReportID, RunID: runOne.Meta().ID,
		RunStatus: domain.RunCancelled, GeneratedAt: now.Add(2 * time.Second), PlanSnapshot: runOneTerminal.Snapshot(),
		Model: domain.ReportSubject{ID: model.ID, Name: model.Name}, Channel: domain.ReportSubject{ID: channels[0].ID, Name: channels[0].Name}, Environment: environment,
		Conclusion: domain.ReportConclusion{Passed: false, Verdict: "cancelled", Issues: []string{"cancelled by test"}},
		SLA:        map[string]domain.MetricValue{}, Metrics: map[string]domain.MetricValue{}, Timeline: []json.RawMessage{}, Distributions: []json.RawMessage{},
		CaseResults: []domain.Result{}, ErrorClusters: []json.RawMessage{}, Evidence: []domain.Evidence{}, Baseline: json.RawMessage(`{}`), Attachments: []domain.ReportAttachment{},
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("invalid v10 report fixture: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO reports(id, schema_version, run_id, generated_at, document_json) VALUES(?, 1, ?, ?, ?)`, report.ID, report.RunID, formatV10Time(report.GeneratedAt), canonicalV10JSON(t, report)); err != nil {
		t.Fatalf("seed v10 report: %v", err)
	}

	comparison, err := domain.NewComparison(meta(v10ComparisonID), domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision}, domain.EntityRevisionRef{ID: model.ID, Revision: model.Revision}, []domain.ComparisonRunRef{
		{Channel: domain.EntityRevisionRef{ID: channels[0].ID, Revision: channels[0].Revision}, RunID: runOne.Meta().ID},
		{Channel: domain.EntityRevisionRef{ID: channels[1].ID, Revision: channels[1].Revision}, RunID: runTwo.Meta().ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO comparisons(id, current_revision, created_at, sealed) VALUES(?, 1, ?, 0)`, comparison.Meta().ID, formatV10Time(comparison.Meta().CreatedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO comparison_revisions(comparison_id, schema_version, revision, created_at, updated_at, plan_id, plan_revision, model_id, model_revision, status, document_json) VALUES(?, 1, 1, ?, ?, ?, 1, ?, 1, 'running', ?)`, comparison.Meta().ID, formatV10Time(now), formatV10Time(now), plan.ID, model.ID, canonicalV10JSON(t, comparison)); err != nil {
		t.Fatal(err)
	}
	for position, run := range comparison.Runs() {
		if _, err := db.Exec(`INSERT INTO comparison_runs(comparison_id, comparison_revision, position, channel_id, channel_revision, run_id) VALUES(?, 1, ?, ?, 1, ?)`, comparison.Meta().ID, position, run.Channel.ID, run.RunID); err != nil {
			t.Fatal(err)
		}
	}
	return v10Fixture{model: model, channels: channels, plan: plan, mappings: mappings, testCase: testCase, runs: []domain.Run{runOne, runOneTerminal, runTwo}, report: report, comparison: comparison}
}

func makeV10FixtureTargetless(t *testing.T, db *sql.DB, fixture v10Fixture) v10Fixture {
	t.Helper()
	for _, statement := range []string{
		"DELETE FROM plan_channel_models WHERE plan_id = '" + v10PlanID + "' AND plan_revision = 1",
		"DELETE FROM plan_channels WHERE plan_id = '" + v10PlanID + "' AND plan_revision = 1",
		"DELETE FROM plan_models WHERE plan_id = '" + v10PlanID + "' AND plan_revision = 1",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("make v10 plan targetless with %q: %v", statement, err)
		}
	}
	fixture.plan.ModelIDs = []string{}
	fixture.plan.ChannelIDs = []string{}
	if err := fixture.plan.Validate(); err != nil {
		t.Fatalf("targetless v10 plan validation error = %v", err)
	}
	if _, err := db.Exec(`UPDATE test_plans SET document_json = ? WHERE id = ? AND revision = 1`, canonicalV10JSON(t, fixture.plan), fixture.plan.ID); err != nil {
		t.Fatalf("write targetless v10 plan: %v", err)
	}
	return fixture
}

func insertV10Run(t *testing.T, db *sql.DB, revisions []domain.Run, sealed bool) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	latest := revisions[len(revisions)-1]
	if _, err := tx.Exec(`INSERT INTO execution_runs(id, current_revision, created_at, sealed) VALUES(?, ?, ?, ?)`, latest.Meta().ID, latest.Meta().Revision, formatV10Time(revisions[0].Meta().CreatedAt), boolV10Int(sealed)); err != nil {
		t.Fatal(err)
	}
	for _, run := range revisions {
		meta, snapshot := run.Meta(), run.Snapshot()
		if _, err := tx.Exec(`INSERT INTO execution_run_revisions(run_id, schema_version, revision, created_at, updated_at, plan_id, plan_revision, status, snapshot_json, document_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			meta.ID, meta.SchemaVersion, meta.Revision, formatV10Time(meta.CreatedAt), formatV10Time(meta.UpdatedAt), run.PlanID(), snapshot.Plan.Revision, run.Status(), canonicalV10JSON(t, snapshot), canonicalV10JSON(t, run)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func completeV10Snapshot(t *testing.T, fixture v10Fixture, legacy domain.RunSnapshot) domain.RunSnapshot {
	t.Helper()
	legacy.SchemaVersion = domain.CurrentRunSnapshotSchemaVersion
	plan := fixture.plan
	legacy.PlanDocument = &plan
	for index := range fixture.mappings {
		if fixture.mappings[index].ChannelID == legacy.Channel.ID {
			mapping := fixture.mappings[index]
			legacy.Mapping = &mapping
			break
		}
	}
	legacy.CaseDefinitions = []domain.TestCase{fixture.testCase}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("complete v10 snapshot: %v", err)
	}
	return legacy
}

func canonicalV10JSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func validV2RunDocuments(t *testing.T, runID, planID string) ([]byte, []byte) {
	t.Helper()
	now := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	modelID := "80808080-8080-4080-8080-808080808001"
	channelID := "80808080-8080-4080-8080-808080808002"
	mappingID := "80808080-8080-4080-8080-808080808003"
	caseID := "80808080-8080-4080-8080-808080808004"
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	testCase := domain.TestCase{
		EntityMeta: meta(caseID), Key: "migration-v2", Name: "migration v2 case", Dimension: "migration",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("request.single"), TypeVersion: 1,
			Spec: json.RawMessage(`{"request":{"method":"POST","path":"/chat/completions","headers":{},"body":{}},"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"assertions":[{"kind":"text","config":{"non_empty":true}}]}`),
		},
	}
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	plan := domain.Plan{
		EntityMeta: meta(planID), Name: "file-only migration plan",
		ModelIDs: []string{}, ChannelIDs: []string{}, Cases: []domain.CaseRevisionRef{caseRef},
		Load: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000},
		SLA:  domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1000}},
	}
	mapping := domain.ChannelModel{EntityMeta: meta(mappingID), ChannelID: channelID, ModelID: modelID, UpstreamModelName: "upstream-v2"}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: planID, Revision: 1},
		Model: domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1},
			Name: "model-v2", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{}},
		Channel: domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1},
			Name: "channel-v2", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: mapping.UpstreamModelName},
		Cases: []domain.CaseRevisionRef{caseRef}, Load: plan.Load, SLA: plan.SLA,
		Environment:  domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", AppVersion: "test", EngineVersion: "test"},
		PlanDocument: &plan, Mapping: &mapping, CaseDefinitions: []domain.TestCase{testCase},
	}
	run, err := domain.NewRun(meta(runID), planID, snapshot)
	if err != nil {
		t.Fatalf("create valid v2 migration run: %v", err)
	}
	return canonicalV10JSON(t, run.Snapshot()), canonicalV10JSON(t, run)
}

func queryV10Documents(t *testing.T, db *sql.DB, query string) map[string][]byte {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string][]byte{}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatal(err)
		}
		result[key] = append([]byte(nil), value...)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func formatV10Time(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func boolV10Int(value bool) int {
	if value {
		return 1
	}
	return 0
}
