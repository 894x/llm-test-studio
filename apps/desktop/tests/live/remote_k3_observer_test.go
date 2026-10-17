package live

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func liveK3AuditFixture() (jobs.JobView, jobcontract.Snapshot) {
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: "83000000-0000-4000-8000-000000000001",
		InstallationID: "83000000-0000-4000-8000-000000000002", EngineBuildID: strings.Repeat("b", 64),
		RunnerVersion: "runner-audit-fixture", OS: "linux", Arch: "amd64"}
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	runID := "83000000-0000-4000-8000-000000000004"
	view := jobs.JobView{JobID: "83000000-0000-4000-8000-000000000003", Kind: jobcontract.KindRun,
		RunIDs: []string{runID}, ReportIDs: []string{}, Executor: jobs.ExecutorView{NodeID: origin.NodeID,
			InstallationID: origin.InstallationID, Version: origin.RunnerVersion, BuildID: origin.EngineBuildID,
			OS: origin.OS, Arch: origin.Arch}}
	snapshot := jobcontract.Snapshot{Receipt: jobcontract.Receipt{Contract: jobcontract.CurrentContract,
		JobID: view.JobID, Kind: view.Kind, Origin: origin, AcceptedAt: at, Status: domain.RunRunning,
		Revision: 3, RunIDs: []string{runID}, ReportIDs: []string{}},
		LatestSequence: 10, UpdatedAt: at.Add(time.Hour)}
	return view, snapshot
}

func TestLiveK3PerformanceOriginUsesTerminalAndAllowsAbsentCheckpointOrigin(t *testing.T) {
	view, terminal := liveK3AuditFixture()
	view.Kind, terminal.Receipt.Kind = jobcontract.KindPerformance, jobcontract.KindPerformance
	view.RunIDs, terminal.Receipt.RunIDs = []string{}, []string{}
	view.ReportIDs = []string{"83000000-0000-4000-8000-000000000009"}
	terminal.Receipt.ReportIDs = view.ReportIDs
	terminal.Receipt.Status, terminal.Receipt.Revision = domain.RunCompleted, 4
	view.Execution = &jobs.ExecutionView{Status: terminal.Receipt.Status, Revision: terminal.Receipt.Revision,
		LatestSequence: terminal.LatestSequence + 2, AcceptedAt: terminal.Receipt.AcceptedAt.Format(time.RFC3339Nano)}
	initial := jobs.PerformanceResultView{JobID: view.JobID, Location: view.Location, Executor: view.Executor,
		SealedAt: terminal.UpdatedAt.Format(time.RFC3339Nano), Terminal: jobs.ExecutionView{
			Status: terminal.Receipt.Status, Revision: terminal.Receipt.Revision, LatestSequence: terminal.LatestSequence,
			AcceptedAt: terminal.Receipt.AcceptedAt.Format(time.RFC3339Nano)},
		Observation: &jobs.PerformanceObservationView{Report: quicktest.PerformanceReport{}}}
	for _, test := range []struct {
		name   string
		change func(*jobs.PerformanceResultView)
		want   bool
	}{
		{name: "checkpoint origin is explicitly absent", want: true},
		{name: "checkpoint carries matching actual origin", change: func(result *jobs.PerformanceResultView) {
			origin := terminal.Receipt.Origin
			result.Observation.Report.ExecutionOrigin = &origin
		}, want: true},
		{name: "checkpoint carries another origin", change: func(result *jobs.PerformanceResultView) {
			origin := terminal.Receipt.Origin
			origin.RunnerVersion = "different-runner"
			result.Observation.Report.ExecutionOrigin = &origin
		}},
		{name: "outer authoritative executor differs", change: func(result *jobs.PerformanceResultView) {
			result.Executor.Version = "different-runner"
		}},
		{name: "terminal revision differs", change: func(result *jobs.PerformanceResultView) {
			result.Terminal.Revision++
		}},
		{name: "terminal belongs to another job", change: func(result *jobs.PerformanceResultView) {
			result.JobID = "83000000-0000-4000-8000-000000000008"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := initial
			observation := *initial.Observation
			result.Observation = &observation
			if test.change != nil {
				test.change(&result)
			}
			if got := liveK3PerformanceOriginMatches(result, view); got != test.want {
				t.Fatalf("origin check=%v, want %v", got, test.want)
			}
		})
	}
}

func TestLiveK3SQLiteBackupUsesReadOnlyConsistentSource(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source.db"), filepath.Join(root, "clone.db")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE sample(value INTEGER NOT NULL); INSERT INTO sample VALUES(7)"); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := liveK3SQLiteBackup(t.Context(), source, destination); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("consistent backup changed its read-only source")
	}
	clone, err := sql.Open("sqlite", "file:"+filepath.ToSlash(destination)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	var value int
	if err := clone.QueryRow("SELECT value FROM sample").Scan(&value); err != nil || value != 7 {
		t.Fatal("consistent clone lost committed source data")
	}
	if err := liveK3SQLiteBackup(t.Context(), source, source); err == nil {
		t.Fatal("backup accepted its source as destination")
	}
	if err := liveK3SQLiteBackup(t.Context(), source, destination); err == nil {
		t.Fatal("backup overwrote an existing destination")
	}
}

func TestLiveK3ClosedObserverRequiresDurableSequenceGrowth(t *testing.T) {
	view, baseline := liveK3AuditFixture()
	for _, test := range []struct {
		name   string
		change func(*jobcontract.Snapshot)
		want   string
	}{
		{name: "unchanged snapshot", want: "unchanged"},
		{name: "new wall time alone", change: func(current *jobcontract.Snapshot) {
			current.UpdatedAt = current.UpdatedAt.Add(24 * time.Hour)
		}, want: "unchanged"},
		{name: "sequence growth despite wall clock rollback", change: func(current *jobcontract.Snapshot) {
			current.LatestSequence++
			current.UpdatedAt = current.Receipt.AcceptedAt.Add(time.Minute)
		}, want: "advanced"},
		{name: "terminal transition", change: func(current *jobcontract.Snapshot) {
			current.LatestSequence++
			current.Receipt.Status, current.Receipt.Revision = domain.RunCompleted, 4
		}, want: "advanced"},
		{name: "sequence rollback", change: func(current *jobcontract.Snapshot) {
			current.LatestSequence--
		}, want: "sequence_regressed"},
		{name: "other job", change: func(current *jobcontract.Snapshot) {
			current.Receipt.JobID = "83000000-0000-4000-8000-000000000005"
			current.LatestSequence++
		}, want: "identity_changed"},
		{name: "other runner", change: func(current *jobcontract.Snapshot) {
			current.Receipt.Origin.RunnerVersion = "another-runner"
			current.LatestSequence++
		}, want: "identity_changed"},
		{name: "other admission", change: func(current *jobcontract.Snapshot) {
			current.Receipt.AcceptedAt = current.Receipt.AcceptedAt.Add(time.Second)
			current.LatestSequence++
		}, want: "identity_changed"},
		{name: "other child run", change: func(current *jobcontract.Snapshot) {
			current.Receipt.RunIDs = []string{"83000000-0000-4000-8000-000000000006"}
			current.LatestSequence++
		}, want: "identity_changed"},
		{name: "invalid snapshot", change: func(current *jobcontract.Snapshot) {
			current.Receipt.Origin = domain.ExecutionOrigin{}
			current.LatestSequence++
		}, want: "identity_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := baseline
			if test.change != nil {
				test.change(&current)
			}
			if got := classifyLiveK3Audit(view, baseline, current); got != test.want {
				t.Fatalf("audit classification=%s, want %s", got, test.want)
			}
		})
	}
}
