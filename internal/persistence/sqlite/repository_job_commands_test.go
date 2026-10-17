package sqlite_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func commandFor(snapshot jobcontract.Snapshot, kind jobcontract.CommandKind) jobcontract.Command {
	return jobcontract.Command{
		Contract: jobcontract.CurrentContract, CommandID: "77777777-7777-4777-8777-777777777771",
		JobID: snapshot.Receipt.JobID, Kind: kind, ExpectedRevision: snapshot.Receipt.Revision,
	}
}

func advanceJobFixture(
	t *testing.T,
	repository *persistence.Repository,
	snapshot jobcontract.Snapshot,
	status domain.RunStatus,
) jobcontract.Snapshot {
	t.Helper()
	if status == domain.RunQueued {
		return snapshot
	}
	statuses := []domain.RunStatus{domain.RunStarting}
	if status != domain.RunStarting {
		statuses = append(statuses, domain.RunRunning)
		if status != domain.RunRunning {
			statuses = append(statuses, status)
		}
	}
	for _, next := range statuses {
		change := jobhost.Change{
			JobID: snapshot.Receipt.JobID, ExpectedRevision: snapshot.Receipt.Revision,
			Status: next, At: snapshot.UpdatedAt.Add(time.Second),
		}
		if next == domain.RunFailed {
			change.Failure = &domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}
		}
		var err error
		snapshot, err = repository.ChangeJob(t.Context(), change)
		if err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func TestJobCommandStateDecisionTableAndStableConfirmations(t *testing.T) {
	for _, status := range []domain.RunStatus{
		domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining,
		domain.RunCompleted, domain.RunFailed, domain.RunCancelled,
	} {
		for _, kind := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
			t.Run(string(status)+"/"+string(kind), func(t *testing.T) {
				repository, _, initial := openJournalFixture(t)
				before := advanceJobFixture(
					t,
					repository,
					initial,
					status,
				)
				command := commandFor(before, kind)
				at := before.UpdatedAt.Add(time.Second)
				confirmation, created, err := repository.ApplyJobCommand(t.Context(), command, at)
				if jobcontract.Terminal(status) {
					if !errors.Is(err, jobhost.ErrConflict) || created {
						t.Fatalf("terminal accepted a new command: created=%v err=%v", created, err)
					}
					if _, err := repository.GetJobCommand(t.Context(), command.CommandID); !errors.Is(err, jobhost.ErrNotFound) {
						t.Fatalf("rejected command was persisted: %v", err)
					}
					return
				}
				if err != nil || !created || confirmation.Validate() != nil {
					t.Fatalf("valid command rejected: created=%v err=%v", created, err)
				}
				wantStatus := domain.RunCancelled
				if kind == jobcontract.CommandStopSending && status != domain.RunQueued {
					wantStatus = domain.RunDraining
				}
				wantRevision := before.Receipt.Revision + 1
				wantSequence := before.LatestSequence + 2
				if wantStatus == status {
					wantRevision--
					wantSequence--
				}
				got := confirmation.Snapshot
				if got.Receipt.Status != wantStatus || got.Receipt.Revision != wantRevision || got.LatestSequence != wantSequence {
					t.Fatalf("command decision differs: %+v", got.Receipt)
				}
				stored, err := repository.GetJobCommand(t.Context(), command.CommandID)
				if err != nil || !reflect.DeepEqual(confirmation, stored) {
					t.Fatalf("command confirmation was not stored exactly: %v", err)
				}
				current, err := repository.GetJob(t.Context(), command.JobID)
				if err != nil || !reflect.DeepEqual(current, confirmation.Snapshot) {
					t.Fatalf("command and state were not committed together: %v", err)
				}
				if !jobcontract.Terminal(wantStatus) {
					_, err = repository.ChangeJob(t.Context(), jobhost.Change{
						JobID: command.JobID, ExpectedRevision: wantRevision,
						Status: domain.RunCompleted, At: at.Add(time.Second),
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				repeated, created, err := repository.ApplyJobCommand(t.Context(), command, at.Add(time.Hour))
				if err != nil || created || !reflect.DeepEqual(repeated, confirmation) {
					t.Fatalf("retry did not return the original confirmation: created=%v err=%v", created, err)
				}
				for _, field := range []string{"kind", "revision", "job"} {
					changed := command
					switch field {
					case "kind":
						changed.Kind = jobcontract.CommandStopSending
						if command.Kind == changed.Kind {
							changed.Kind = jobcontract.CommandCancel
						}
					case "revision":
						changed.ExpectedRevision++
					case "job":
						changed.JobID = "77777777-7777-4777-8777-777777777779"
					}
					_, _, err := repository.ApplyJobCommand(t.Context(), changed, at)
					if !errors.Is(err, jobhost.ErrConflict) {
						t.Fatalf("same command changed %s: %v", field, err)
					}
				}
			})
		}
	}
}

func TestJobCommandRollsBackStateEventsAndConfirmationTogether(t *testing.T) {
	for _, stage := range []string{"state_event", "command_event", "command_row"} {
		t.Run(stage, func(t *testing.T) {
			repository, db, before := openJournalFixture(t)
			trigger := `CREATE TRIGGER reject_command BEFORE INSERT ON execution_job_commands
				BEGIN SELECT RAISE(ABORT, 'injected command failure'); END`
			if stage != "command_row" {
				kind := "state"
				if stage == "command_event" {
					kind = "command"
				}
				trigger = `CREATE TRIGGER reject_command BEFORE INSERT ON execution_job_events
					WHEN NEW.kind = '` + kind + `' BEGIN SELECT RAISE(ABORT, 'injected event failure'); END`
			}
			if _, err := db.ExecContext(t.Context(), trigger); err != nil {
				t.Fatal(err)
			}
			command := commandFor(before, jobcontract.CommandCancel)
			_, _, err := repository.ApplyJobCommand(t.Context(), command, before.UpdatedAt.Add(time.Second))
			if err == nil {
				t.Fatal("injected command failure committed")
			}
			after, err := repository.GetJob(t.Context(), before.Receipt.JobID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed command changed state or sequence: %v", err)
			}
			page, err := repository.JobEvents(
				t.Context(),
				command.JobID,
				0,
				200,
			)
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("failed command left journal events: %v", err)
			}
			if _, err := repository.GetJobCommand(t.Context(), command.CommandID); !errors.Is(err, jobhost.ErrNotFound) {
				t.Fatalf("failed command left confirmation: %v", err)
			}
		})
	}
}

func TestCancelCommandAndCompletionHaveOneDurableWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command-race.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "command-race"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	second := reopenHardeningRepository(t, path)
	defer second.Close()
	initial, _, err := repository.AcceptJob(t.Context(), jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t))))
	if err != nil {
		t.Fatal(err)
	}
	running := advanceJobFixture(
		t,
		repository,
		initial,
		domain.RunRunning,
	)
	command := commandFor(running, jobcontract.CommandCancel)
	at := running.UpdatedAt.Add(time.Second)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, _, err := repository.ApplyJobCommand(t.Context(), command, at)
		results <- err
	}()
	go func() {
		defer workers.Done()
		<-start
		_, err := second.ChangeJob(t.Context(), jobhost.Change{
			JobID: command.JobID, ExpectedRevision: running.Receipt.Revision, Status: domain.RunCompleted, At: at,
		})
		results <- err
	}()
	close(start)
	workers.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, jobhost.ErrConflict) {
			t.Fatal(err)
		}
		conflicts++
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("terminal race: winners=%d conflicts=%d", winners, conflicts)
	}
	final, err := repository.GetJob(t.Context(), command.JobID)
	if err != nil || !jobcontract.Terminal(final.Receipt.Status) || final.Receipt.Revision != running.Receipt.Revision+1 {
		t.Fatalf("terminal race did not retain one state: %v", err)
	}
}
