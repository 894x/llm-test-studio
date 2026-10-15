package sqlite_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRecoveryJobsRetainsTerminalDecisionUntilChildrenAreSettled(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recovery.db")
			options := persistence.MigrateOptions{AppVersion: "recovery-test"}
			if err := persistence.Migrate(t.Context(), path, options); err != nil {
				t.Fatal(err)
			}
			repository := reopenHardeningRepository(t, path)
			defer repository.Close()
			spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
			if kind == jobcontract.KindComparison {
				spec = comparisonJobSpec(t)
			}
			initial, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec))
			if err != nil {
				t.Fatal(err)
			}
			command := commandFor(initial, jobcontract.CommandCancel)
			command.CommandID = "77777777-7777-4777-8777-777777777776"
			if _, _, err := repository.ApplyJobCommand(t.Context(), command, initial.UpdatedAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			recoverable, err := repository.RecoveryJobs(t.Context())
			if err != nil || len(recoverable) != 1 || recoverable[0].Receipt.Status != domain.RunCancelled {
				t.Fatalf("terminal decision hid unfinished children: count=%d err=%v", len(recoverable), err)
			}
			for _, input := range spec.PreparedRuns() {
				run, err := repository.GetRun(t.Context(), input.Meta.ID)
				if err != nil {
					t.Fatal(err)
				}
				cancelled, err := run.Transition(domain.RunCancelled, run.Meta().UpdatedAt.Add(time.Second))
				if err != nil {
					t.Fatal(err)
				}
				if err := repository.UpdateRun(t.Context(), 1, cancelled); err != nil {
					t.Fatal(err)
				}
			}
			if kind == jobcontract.KindComparison {
				recoverable, err := repository.RecoveryJobs(t.Context())
				if err != nil || len(recoverable) != 1 {
					t.Fatalf("unfinished comparison root was lost: %v", err)
				}
				comparison, err := repository.GetComparison(t.Context(), initial.Receipt.ComparisonID)
				if err != nil {
					t.Fatal(err)
				}
				cancelled, err := comparison.Transition(domain.ComparisonCancelled, comparison.Meta().UpdatedAt.Add(time.Second))
				if err != nil {
					t.Fatal(err)
				}
				if err := repository.UpdateComparison(t.Context(), 1, cancelled); err != nil {
					t.Fatal(err)
				}
			}
			recoverable, err = repository.RecoveryJobs(t.Context())
			if err != nil || len(recoverable) != 1 {
				t.Fatal("unconfirmed report settlement was lost")
			}
			if err := repository.MarkJobSettled(t.Context(), spec.JobID); err != nil {
				t.Fatal(err)
			}
			recoverable, err = repository.RecoveryJobs(t.Context())
			if err != nil || len(recoverable) != 0 {
				t.Fatalf("settled job remains recoverable: %v", err)
			}
		})
	}
}

func TestLookupJobOnlyReadsMatchingImmutableAdmission(t *testing.T) {
	repository, _, snapshot := openJournalFixture(t)
	spec, err := repository.GetJobSpec(t.Context(), snapshot.Receipt.JobID)
	if err != nil {
		t.Fatal(err)
	}
	input := jobAcceptance(spec)
	input.AcceptedAt = time.Time{}
	if found, err := repository.LookupJob(t.Context(), input); err != nil || !reflect.DeepEqual(found, snapshot) {
		t.Fatalf("read-only lookup changed the persisted receipt: %v", err)
	}
	changed := input
	changed.SecretCommitment = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := repository.LookupJob(t.Context(), changed); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal(err)
	}
	input.Spec.JobID = "77777777-7777-4777-8777-777777777779"
	if _, err := repository.LookupJob(t.Context(), input); !errors.Is(err, jobhost.ErrNotFound) {
		t.Fatal(err)
	}
	page, err := repository.JobEvents(
		t.Context(),
		snapshot.Receipt.JobID,
		0,
		200,
	)
	if err != nil || len(page.Events) != 1 {
		t.Fatal("read-only lookups appended events")
	}
}

func TestProgressEventAndCursorRollbackTogether(t *testing.T) {
	repository, db, snapshot := openJournalFixture(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_progress BEFORE INSERT ON execution_job_events
		WHEN NEW.kind = 'progress' BEGIN SELECT RAISE(ABORT, 'injected progress failure'); END`); err != nil {
		t.Fatal(err)
	}
	progress := quicktest.PerformanceProgress{Phase: load.PhaseSending, Planned: 10, Launched: 2}
	at := snapshot.UpdatedAt.Add(time.Second)
	if err := repository.RecordJobProgress(
		t.Context(),
		snapshot.Receipt.JobID,
		progress,
		at,
	); err == nil {
		t.Fatal("failed progress journal committed")
	}
	after, err := repository.GetJob(t.Context(), snapshot.Receipt.JobID)
	if err != nil || after.LatestSequence != snapshot.LatestSequence || !after.UpdatedAt.Equal(snapshot.UpdatedAt) {
		t.Fatal("failed progress advanced the cursor or timestamp")
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER reject_progress`); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordJobProgress(
		t.Context(),
		snapshot.Receipt.JobID,
		progress,
		at,
	); err != nil {
		t.Fatal(err)
	}
	after, err = repository.GetJob(t.Context(), snapshot.Receipt.JobID)
	if err != nil || after.LatestSequence != 2 || after.Receipt.Revision != 1 {
		t.Fatal("progress changed lifecycle revision")
	}
}
