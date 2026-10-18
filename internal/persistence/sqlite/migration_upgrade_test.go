package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const historicalRunID = "10000000-0000-4000-8000-000000000008"
const historicalReportID = "10000000-0000-4000-8000-00000000000b"

func TestUpgradeTenHistoricalOperationalFormatsPreservesReadableHistory(t *testing.T) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range catalog.Baselines {
		t.Run(baseline.Commit[:8], func(t *testing.T) {
			t.Parallel()
			path := historicalMigrationFixture(t, baseline)
			before := readMigrationText(t, path, "SELECT document_json FROM reports WHERE id = ?", historicalReportID)
			originalLedger := readMigrationText(t, path, "SELECT checksum FROM schema_migrations WHERE version = 1")
			originalJob := readMigrationText(t, path, "SELECT snapshot_json FROM execution_jobs LIMIT 1")
			if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "upgrade-test"}); err != nil {
				t.Fatal(err)
			}
			assertMigratedHistoryReadable(t, path)
			assertMigratedJobsPreserved(t, path, baseline, originalJob)
			expected := before
			if baseline.lacksExecutionOrigin() {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(before), &fields); err != nil {
					t.Fatal(err)
				}
				fields["execution_origin"] = json.RawMessage("null")
				encoded, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				expected = string(encoded)
			}
			if after := readMigrationText(t, path, "SELECT document_json FROM reports WHERE id = ?", historicalReportID); after != expected {
				t.Fatal("historical report facts were changed")
			}
			if after := readMigrationText(t, path, "SELECT checksum FROM schema_migrations WHERE version = 1"); after != originalLedger {
				t.Fatal("historical migration record was rewritten")
			}
			if got := readMigrationText(t, path, "SELECT CAST(seq AS TEXT) FROM sqlite_sequence WHERE name = 'runs'"); got != "999" {
				t.Fatal("AUTOINCREMENT high-water mark was lost")
			}
			backups := migrationBackups(t, path)
			if len(backups) != 1 {
				t.Fatalf("backups=%d, want 1", len(backups))
			}
			if version, err := SchemaVersion(t.Context(), backups[0]); err != nil || version != 1 {
				t.Fatalf("original backup version=%d err=%v", version, err)
			}
			if got := readMigrationText(t, backups[0], "SELECT document_json FROM reports WHERE id = ?", historicalReportID); got != before {
				t.Fatal("backup did not preserve report bytes")
			}
			bytesBefore := migrationFileHash(t, path)
			if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "repeat-open"}); err != nil {
				t.Fatal(err)
			}
			if migrationFileHash(t, path) != bytesBefore || len(migrationBackups(t, path)) != 1 {
				t.Fatal("repeat open changed data or made another backup")
			}
			// Restore a copy of the old backup and independently upgrade it again.
			restored := filepath.Join(t.TempDir(), "restored.db")
			data, err := os.ReadFile(backups[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(restored, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(t.Context(), restored, MigrateOptions{AppVersion: "restored"}); err != nil {
				t.Fatal(err)
			}
			assertMigratedHistoryReadable(t, restored)
		})
	}
}

func TestUpgradeRollbackPreservesSourceOnUnsupportedDocument(t *testing.T) {
	catalog, _ := loadMigrationCatalog()
	path := historicalMigrationFixture(t, catalog.Baselines[0])
	db := migrationTestDB(t, path)
	if _, err := db.ExecContext(t.Context(), `UPDATE reports SET document_json = '{}'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := migrationFileHash(t, path)
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "failure"}); err == nil {
		t.Fatal("unreadable historical document was accepted")
	}
	if migrationFileHash(t, path) != before {
		t.Fatal("failed upgrade changed original database")
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 1 {
		t.Fatalf("failed upgrade did not roll back: %d %v", version, err)
	}
	if len(migrationBackups(t, path)) != 1 {
		t.Fatal("failed upgrade did not retain the recovery backup")
	}
}

func TestUpgradeArchiveDeletionIsRejectedByBackupAndExport(t *testing.T) {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		t.Fatal(err)
	}
	path := historicalMigrationFixture(t, catalog.Baselines[0])
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "archive-check"}); err != nil {
		t.Fatal(err)
	}
	db := migrationTestDB(t, path)
	for _, table := range []string{"upgrade_history_runs", "upgrade_history_jobs", "upgrade_history_rows", "upgrade_history_tables"} {
		if _, err := db.ExecContext(t.Context(), "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRunnerDatabase(t.Context(), path); err == nil {
		t.Fatal("missing archive was verified")
	}
	output := filepath.Join(t.TempDir(), "missing-history.jsonl")
	if err := ExportUpgradeHistory(t.Context(), path, output); err == nil {
		t.Fatal("missing archive was exported as empty history")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("rejected export created output")
	}
}

func TestUpgradeWALBackupAndConcurrentOpen(t *testing.T) {
	catalog, _ := loadMigrationCatalog()
	path := historicalMigrationFixture(t, catalog.Baselines[0])
	db := migrationTestDB(t, path)
	defer db.Close()
	for _, statement := range []string{"PRAGMA journal_mode = WAL", "PRAGMA wal_autocheckpoint = 0", "INSERT INTO runs(id, config) VALUES(1001, 'wal-only')"} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
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
	if value := readMigrationText(t, backups[0], "SELECT config FROM runs WHERE id=1001"); value != "wal-only" {
		t.Fatal("backup omitted committed WAL data")
	}
}

func TestUpgradeCompetingWriterAndCancellationDoNotMutate(t *testing.T) {
	catalog, _ := loadMigrationCatalog()
	path := historicalMigrationFixture(t, catalog.Baselines[0])
	db := migrationTestDB(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "locked", BusyTimeout: time.Millisecond}); err == nil {
		t.Fatal("migration ignored competing writer")
	}
	if _, err := db.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Migrate(ctx, path, MigrateOptions{AppVersion: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upgrade returned %v", err)
	}
	if len(migrationBackups(t, path)) != 0 {
		t.Fatal("blocked/cancelled preflight created backup")
	}
	if version, err := SchemaVersion(t.Context(), path); err != nil || version != 1 {
		t.Fatalf("source changed: %d %v", version, err)
	}
}

func historicalMigrationFixture(t *testing.T, baseline historicalBaseline, suffix ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "historical.db")
	db := migrationTestDB(t, path)
	defer db.Close()
	for _, object := range baseline.objects {
		if _, err := db.ExecContext(t.Context(), object.ddl); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(filepath.Join("testdata", "migrations", baseline.Commit[:8]+strings.Join(suffix, "")+".json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture struct {
		SourceCommit string `json:"source_commit"`
		Tables       []struct {
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
			Rows    [][]any  `json:"rows"`
		} `json:"tables"`
	}
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil || fixture.SourceCommit != baseline.Commit {
		t.Fatalf("historical producer fixture identity: %v", err)
	}
	for _, table := range fixture.Tables {
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, quoteMigrationName(column))
		}
		query := "INSERT INTO " + quoteMigrationName(table.Table) + " (" + strings.Join(columns, ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		for _, row := range table.Rows {
			for index, value := range row {
				switch typed := value.(type) {
				case json.Number:
					integer, err := typed.Int64()
					if err == nil {
						row[index] = integer
					} else {
						row[index], err = typed.Float64()
						if err != nil {
							t.Fatal(err)
						}
					}
				case map[string]any:
					row[index], err = base64.StdEncoding.DecodeString(typed["blob"].(string))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := db.ExecContext(t.Context(), query, row...); err != nil {
				t.Fatalf("load %s: %v", table.Table, err)
			}
		}
	}
	for _, query := range []string{
		fmt.Sprintf("PRAGMA user_version = %d", max(1, baseline.Version)),
		"INSERT INTO runs(id, config) VALUES(73, 'historical-config'), (999, 'removed')",
		"DELETE FROM runs WHERE id=999",
		"INSERT INTO run_results(id, run_id, request_id, response_body) VALUES(81, 73, 1, 'historical-response')",
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func assertMigratedHistoryReadable(t *testing.T, path string) {
	t.Helper()
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if _, err := repository.GetRun(t.Context(), historicalRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetReport(t.Context(), historicalReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetQuickPerformanceReport(t.Context(), "77777777-7777-4777-8777-777777777771"); err != nil {
		t.Fatal(err)
	}
}

func assertMigratedJobsPreserved(t *testing.T, path string, baseline historicalBaseline, snapshot string) {
	t.Helper()
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	const runID = "30000000-0000-4000-8000-000000000006"
	const jobID = "72000000-0000-4000-8000-000000000001"
	run, err := repository.GetRun(t.Context(), runID)
	if err != nil || run.Status() != domain.RunQueued {
		t.Fatalf("historical run changed: %v", err)
	}
	origin, err := repository.RunExecutionOrigin(t.Context(), runID)
	if err != nil || (origin == nil) != baseline.lacksExecutionOrigin() {
		t.Fatalf("unexpected historical provenance: %v", err)
	}
	if !baseline.lacksExecutionOrigin() {
		stored, err := repository.GetJob(t.Context(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := marshalCanonical(stored)
		if err != nil || string(encoded) != snapshot {
			t.Fatal("current-compatible job changed")
		}
		return
	}
	page, err := repository.ReadUpgradeHistory(t.Context(), "execution_jobs", 0, 100)
	if err != nil || page.TotalRows != 1 || len(page.Rows) != 1 {
		t.Fatalf("historical job archive unavailable: %v", err)
	}
	cellText := func(value any) string {
		switch typed := value.(type) {
		case string:
			return typed
		case []byte:
			return string(typed)
		}
		t.Fatal("invalid archived cell type")
		return ""
	}
	if cellText(page.Rows[0][slices.Index(page.Columns, "snapshot_json")]) != snapshot {
		t.Fatal("original receipt bytes lost")
	}
	var spec jobcontract.JobSpec
	if err := json.Unmarshal([]byte(cellText(page.Rows[0][slices.Index(page.Columns, "spec_json")])), &spec); err != nil {
		t.Fatal(err)
	}
	input := jobhost.Acceptance{Spec: spec, SecretCommitment: strings.Repeat("b", 64), ContentClaims: []jobcontent.Claim{},
		AcceptedAt: time.Now().UTC(), Origin: domain.ExecutionOrigin{Kind: "local", NodeID: "72000000-0000-4000-8000-000000000009",
			InstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID, RunnerVersion: "new", OS: "linux", Arch: "amd64"}}
	if _, _, err := repository.AcceptJob(t.Context(), input); !errors.Is(err, ErrHistoricalJob) {
		t.Fatalf("archived job was resubmitted: %v", err)
	}
	if recovered, err := repository.RecoveryJobs(t.Context()); err != nil || len(recovered) != 0 {
		t.Fatalf("archived jobs entered recovery: %v", err)
	}
	if recovered, err := repository.ListRecoveryRuns(t.Context()); err != nil || len(recovered) != 0 {
		t.Fatalf("archived runs entered recovery: %v", err)
	}
	starting, err := run.Transition(domain.RunStarting, run.Meta().UpdatedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), run.Meta().Revision, starting); !errors.Is(err, ErrHistoricalJob) {
		t.Fatalf("historical run was writable: %v", err)
	}
	backup := filepath.Join(t.TempDir(), "current-with-archive.sqlite")
	if _, err := BackupRunnerDatabase(t.Context(), path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRunnerDatabase(t.Context(), backup); err != nil {
		t.Fatal(err)
	}
	export := filepath.Join(t.TempDir(), "history.jsonl")
	before := migrationFileHash(t, path)
	if err := ExportUpgradeHistory(t.Context(), path, export); err != nil {
		t.Fatal(err)
	}
	if migrationFileHash(t, path) != before {
		t.Fatal("export changed source")
	}
	data, err := os.ReadFile(export)
	if err != nil || !strings.Contains(string(data), `"type":"complete"`) || !strings.Contains(string(data), `"table":"execution_job_events"`) {
		t.Fatalf("archive export incomplete: %v", err)
	}
	if err := ExportUpgradeHistory(t.Context(), path, export); err == nil {
		t.Fatal("export overwrote existing file")
	}
}

func migrationTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func readMigrationText(t *testing.T, path, query string, args ...any) string {
	t.Helper()
	db := migrationTestDB(t, path)
	defer db.Close()
	var value string
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func migrationBackups(t *testing.T, path string) []string {
	t.Helper()
	matches, err := filepath.Glob(path + ".upgrade-backup-*/database.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func migrationFileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
