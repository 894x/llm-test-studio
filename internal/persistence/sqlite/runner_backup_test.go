package sqlite_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRunnerBackupIncludesUncheckpointedWALAndVerifiesSafeFacts(t *testing.T) {
	path, db, repository := runnerBackupFixture(t)
	defer db.Close()
	defer repository.Close()
	const private = "private-request-body-never-in-runner-backup-facts"
	if _, err := db.ExecContext(t.Context(), `INSERT INTO runs(config) VALUES(?)`, private); err != nil {
		t.Fatal(err)
	}
	mainBefore := runnerBackupHash(t, path)
	walBefore := runnerBackupHash(t, path+"-wal")
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture has no committed WAL: info=%v err=%v", info, err)
	}
	destination := filepath.Join(filepath.Dir(path), "new-backup.db")
	backup, err := persistence.BackupRunnerDatabase(t.Context(), path, destination)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion, wantName, wantChecksum := persistence.CurrentSchemaIdentity()
	if backup.SchemaVersion != wantVersion || backup.SchemaName != wantName || backup.SchemaSHA256 != wantChecksum {
		t.Fatalf("backup schema identity differs: %+v", backup)
	}
	if backup.Rows.Jobs != 1 || backup.Rows.Runs != 1 || backup.Rows.RunRevisions != 4 ||
		backup.Rows.CaseResults != 1 || backup.Rows.Evidence != 1 || backup.Rows.Reports != 1 || backup.Rows.JobEvents != 7 {
		t.Fatalf("WAL-visible typed facts missing: %+v", backup.Rows)
	}
	validDigest := len(backup.BusinessSHA256) == 64 && backup.DatabaseSHA256 == runnerBackupHash(t, destination)
	if backup.DatabaseBytes <= 0 || !validDigest {
		t.Fatalf("backup file verification is incomplete: %+v", backup)
	}
	if mainBefore != runnerBackupHash(t, path) || walBefore != runnerBackupHash(t, path+"-wal") {
		t.Fatal("read-only backup changed original main database or WAL")
	}
	verified, err := persistence.VerifyRunnerDatabase(t.Context(), destination)
	if err != nil || !reflect.DeepEqual(backup, verified) {
		t.Fatalf("independent verification differs: verified=%+v err=%v", verified, err)
	}
	encoded, err := json.Marshal(backup)
	if err != nil || strings.Contains(string(encoded), private) || strings.Contains(string(encoded), path) {
		t.Fatalf("verification facts expose input material: err=%v", err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Size() != backup.DatabaseBytes {
		t.Fatalf("backup byte size differs: info=%v err=%v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions=%o, want 600", info.Mode().Perm())
	}
	if info, err := os.Stat(destination + "-wal"); err == nil && info.Size() != 0 {
		t.Fatal("backup depends on a separate WAL file")
	}
	if _, err := db.ExecContext(
		t.Context(), `INSERT INTO runs(config) VALUES('source changed after snapshot')`,
	); err != nil {
		t.Fatal(err)
	}
	again, err := persistence.VerifyRunnerDatabase(t.Context(), destination)
	if err != nil || !reflect.DeepEqual(again, backup) {
		t.Fatalf("source writes changed independent backup: err=%v", err)
	}
	_, err = persistence.BackupRunnerDatabase(t.Context(), path, destination)
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("existing destination was not a conflict: %v", err)
	}
	if runnerBackupHash(t, destination) != backup.DatabaseSHA256 {
		t.Fatal("conflicting backup overwrote the prior backup")
	}
}

func TestRunnerBackupRejectsUnsupportedOrDamagedSourcesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "unknown user version", query: `PRAGMA user_version = 999`},
		{name: "unsupported marker", query: `UPDATE schema_migrations SET checksum = 'private-malformed-marker'`},
		{name: "actual schema differs", query: `DROP INDEX idx_case_results_run`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.db")
			if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "backup-test"}); err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t, path)
			if _, err := db.ExecContext(t.Context(), test.query); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := runnerBackupHash(t, path)
			destination := filepath.Join(filepath.Dir(path), "backup.db")
			_, err := persistence.BackupRunnerDatabase(t.Context(), path, destination)
			if !errors.Is(err, persistence.ErrSchemaResetRequired) || strings.Contains(err.Error(), "private-malformed-marker") {
				t.Fatalf("unsupported source error=%v", err)
			}
			if before != runnerBackupHash(t, path) {
				t.Fatal("unsupported source was changed")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected source left a destination: %v", err)
			}
		})
	}
	t.Run("unversioned empty database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.db")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		before := runnerBackupHash(t, path)
		_, err := persistence.VerifyRunnerDatabase(t.Context(), path)
		if !errors.Is(err, persistence.ErrSchemaResetRequired) || runnerBackupHash(t, path) != before {
			t.Fatalf("empty database was initialized or accepted: %v", err)
		}
	})
	t.Run("missing database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.db")
		if _, err := persistence.VerifyRunnerDatabase(t.Context(), path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing source error=%v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing source was created: %v", err)
		}
	})
}

func TestRunnerBackupRejectsOldBusinessDocumentAndBrokenJournal(t *testing.T) {
	for _, query := range []string{
		`UPDATE case_results SET document_json = json_set(document_json, '$.schema_version', 999)`,
		`UPDATE case_results SET document_json = json_set(document_json, '$.id', 'private-invalid-id')`,
		`DELETE FROM execution_job_events WHERE sequence = 4`,
		`UPDATE execution_job_events SET kind = 'state' WHERE sequence = 3`,
		`UPDATE case_results SET run_id = '75000000-0000-4000-8000-000000000099'`,
		`UPDATE execution_job_runs SET position = 2`,
		`DELETE FROM report_attachments`,
		`UPDATE execution_job_events SET document_json = json_set(document_json,
			'$.payload.sha256', lower(hex(zeroblob(32)))) WHERE kind = 'report'`,
	} {
		t.Run(query, func(t *testing.T) {
			path, db, repository := runnerBackupFixture(t)
			defer db.Close()
			defer repository.Close()
			if _, err := db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			before, walBefore := runnerBackupHash(t, path), runnerBackupHash(t, path+"-wal")
			_, err := persistence.VerifyRunnerDatabase(t.Context(), path)
			if !errors.Is(err, persistence.ErrCorrupt) || strings.Contains(err.Error(), "private-invalid-id") {
				t.Fatalf("corrupt current facts error=%v", err)
			}
			if before != runnerBackupHash(t, path) || walBefore != runnerBackupHash(t, path+"-wal") {
				t.Fatal("rejected business facts were mutated")
			}
		})
	}
}

func TestRunnerBackupSnapshotRemainsConsistentWithConcurrentWALWriter(t *testing.T) {
	path, db, repository := runnerBackupFixture(t)
	defer db.Close()
	defer repository.Close()
	// Materialize enough committed pages to overlap snapshot copying and writes.
	if _, err := db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS
		(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x < 300)
		INSERT INTO runs(config) SELECT printf('%04096d', x) FROM n`); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	ctx, cancel := context.WithCancel(t.Context())
	var group sync.WaitGroup
	var writes atomic.Int64
	group.Add(1)
	go func() {
		defer group.Done()
		for ctx.Err() == nil {
			_, err := writer.ExecContext(ctx, `INSERT INTO runs(config) VALUES('concurrent writer')`)
			if err != nil {
				return
			}
			writes.Add(1)
		}
	}()
	destination := filepath.Join(filepath.Dir(path), "consistent.db")
	backup, backupErr := persistence.BackupRunnerDatabase(t.Context(), path, destination)
	cancel()
	group.Wait()
	if backupErr != nil {
		t.Fatal(backupErr)
	}
	if writes.Load() == 0 {
		t.Fatal("concurrent writer did not commit during the snapshot exercise")
	}
	verified, err := persistence.VerifyRunnerDatabase(t.Context(), destination)
	if err != nil || !reflect.DeepEqual(backup, verified) {
		t.Fatalf("concurrent source produced an inconsistent backup: %v", err)
	}
}

func TestRunnerBackupCancellationAndExistingFilePreserveData(t *testing.T) {
	path, db, repository := runnerBackupFixture(t)
	defer db.Close()
	defer repository.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	destination := filepath.Join(filepath.Dir(path), "cancelled.db")
	if _, err := persistence.BackupRunnerDatabase(ctx, path, destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backup error=%v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled backup created a destination: %v", err)
	}
	if _, err := persistence.BackupRunnerDatabase(t.Context(), path, path); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("source itself was accepted as a destination: %v", err)
	}
	const sentinel = "preserve existing unrelated file"
	if err := os.WriteFile(destination, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := persistence.BackupRunnerDatabase(t.Context(), path, destination)
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("existing unrelated file error=%v", err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != sentinel {
		t.Fatal("existing unrelated destination was changed")
	}
}

func TestRunnerBackupRejectsExistingDestinationSidecars(t *testing.T) {
	path, db, repository := runnerBackupFixture(t)
	defer db.Close()
	defer repository.Close()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			destination := filepath.Join(filepath.Dir(path), "preserve"+suffix+".db")
			sidecar := destination + suffix
			const unrelated = "unrelated sidecar must not be opened by SQLite"
			if err := os.WriteFile(sidecar, []byte(unrelated), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := persistence.BackupRunnerDatabase(t.Context(), path, destination)
			if !errors.Is(err, persistence.ErrConflict) {
				t.Fatalf("existing sidecar was not a conflict: %v", err)
			}
			if data, err := os.ReadFile(sidecar); err != nil || string(data) != unrelated {
				t.Fatal("existing sidecar was changed")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected sidecar left a new main file: %v", err)
			}
		})
	}
}

func TestRunnerBackupPreservesSealedPerformanceFactsWithoutExecution(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	fixture.recordThrough(t, len(fixture.checkpoints)-1)
	terminal := fixture.change(t, domain.RunCompleted)
	sealed, err := fixture.repository.SealPerformanceResult(
		t.Context(), fixture.spec.JobID, terminal.UpdatedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	archive, eligible, err := sealed.Result.StatisticalArchive()
	if err != nil || !eligible {
		t.Fatalf("local performance fixture has no archive: %v", err)
	}
	if err := fixture.repository.SaveQuickPerformanceReport(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	requestsBefore := fixture.requests.Load()
	destination := filepath.Join(filepath.Dir(fixture.path), "performance-backup.db")
	backup, err := persistence.BackupRunnerDatabase(t.Context(), fixture.path, destination)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Rows.Jobs != 1 || backup.Rows.Runs != 0 || backup.Rows.PerformanceReports != 1 {
		t.Fatalf("sealed performance facts missing: %+v", backup.Rows)
	}
	verified, err := persistence.VerifyRunnerDatabase(t.Context(), destination)
	if err != nil || !reflect.DeepEqual(backup, verified) {
		t.Fatalf("sealed performance verification differs: %v", err)
	}
	if fixture.requests.Load() != requestsBefore {
		t.Fatal("backup or verification executed provider work")
	}
}

func TestRunnerBackupPreservesComparisonOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comparison.db")
	if err := persistence.Migrate(
		t.Context(), path, persistence.MigrateOptions{AppVersion: "runner-backup-test"},
	); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(comparisonJobSpec(t))); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(path), "comparison-backup.db")
	backup, err := persistence.BackupRunnerDatabase(t.Context(), path, destination)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Rows.Jobs != 1 || backup.Rows.Comparisons != 1 || backup.Rows.Runs != 2 || backup.Rows.RunRevisions != 2 {
		t.Fatalf("comparison ownership facts missing: %+v", backup.Rows)
	}
}

func TestRunnerBackupReadsStoppedDatabaseWithRetainedWAL(t *testing.T) {
	path, db, repository := runnerBackupFixture(t)
	defer db.Close()
	defer repository.Close()
	// No connection is opened to this copy. It models a stopped process whose
	// committed transactions remain in WAL rather than the main database file.
	stopped := filepath.Join(t.TempDir(), "stopped.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stopped+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mainBefore, walBefore := runnerBackupHash(t, stopped), runnerBackupHash(t, stopped+"-wal")
	verified, err := persistence.VerifyRunnerDatabase(t.Context(), stopped)
	if err != nil || verified.Rows.Jobs != 1 || verified.Rows.CaseResults != 1 {
		t.Fatalf("stopped WAL facts were not readable: rows=%+v error=%v", verified.Rows, err)
	}
	destination := filepath.Join(filepath.Dir(stopped), "snapshot.db")
	backup, err := persistence.BackupRunnerDatabase(t.Context(), stopped, destination)
	if err != nil || backup.BusinessSHA256 != verified.BusinessSHA256 || backup.Rows != verified.Rows {
		t.Fatalf("stopped WAL backup differs: error=%v", err)
	}
	if mainBefore != runnerBackupHash(t, stopped) || walBefore != runnerBackupHash(t, stopped+"-wal") {
		t.Fatal("read-only stopped database inspection checkpointed or modified its main file or WAL")
	}
}

func runnerBackupFixture(t *testing.T) (string, *sql.DB, *persistence.Repository) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.db")
	if err := persistence.Migrate(
		t.Context(), path, persistence.MigrateOptions{AppVersion: "runner-backup-test"},
	); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(t.Context(), `PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	fixture := newRepositoryFixture(t)
	spec := jobSpecForRun(t, fixture.run)
	origin := executionOrigin(spec)
	fixture.report.ExecutionOrigin = &origin
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	starting, err := fixture.run.Transition(domain.RunStarting, fixture.run.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), 1, starting); err != nil {
		t.Fatal(err)
	}
	running, err := starting.Transition(domain.RunRunning, starting.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), 2, running); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateEvidence(t.Context(), fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResults(t.Context(), fixture.result); err != nil {
		t.Fatal(err)
	}
	completed, err := running.Transition(domain.RunCompleted, running.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), 3, completed); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateReport(t.Context(), fixture.report); err != nil {
		t.Fatal(err)
	}
	return path, db, repository
}

func runnerBackupHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
