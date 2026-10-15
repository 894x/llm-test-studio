package sqlite_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func jobSpecForRun(t *testing.T, run domain.Run) jobcontract.JobSpec {
	t.Helper()
	return jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: "72000000-0000-4000-8000-000000000001",
		NodeInstallationID: "72000000-0000-4000-8000-000000000002",
		EngineBuildID:      strings.Repeat("a", 64), Kind: jobcontract.KindRun,
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}, Assets: []jobcontract.Asset{},
		Spec: jobcontract.Payload{Run: &runs.PreparedRun{
			Meta: run.Meta(), PlanID: run.PlanID(), Snapshot: run.Snapshot(),
		}},
	}
}

func jobAcceptance(spec jobcontract.JobSpec) jobhost.Acceptance {
	return jobhost.Acceptance{
		Spec: spec, SecretCommitment: strings.Repeat("b", 64), AcceptedAt: repositoryEpoch.Add(time.Minute),
	}
}

func TestJobAdmissionPersistsRunAndConfirmationAndRejectsConflictingRetries(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx := context.Background()
	run := runWithCompleteSnapshot(t)
	spec := jobSpecForRun(t, run)
	input := jobAcceptance(spec)
	accepted, created, err := repository.AcceptJob(ctx, input)
	if err != nil || !created {
		t.Fatalf("AcceptJob() created=%v err=%v", created, err)
	}
	if err := accepted.Validate(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRun(ctx, run.Meta().ID); err != nil {
		t.Fatalf("acknowledged job has no initial run: %v", err)
	}
	storedSpec, err := repository.GetJobSpec(ctx, spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(t, "job spec", spec, storedSpec)
	input.AcceptedAt = input.AcceptedAt.Add(time.Hour)
	retried, created, err := repository.AcceptJob(ctx, input)
	if err != nil || created || !retried.Receipt.AcceptedAt.Equal(accepted.Receipt.AcceptedAt) {
		t.Fatalf("identical retry changed its receipt: created=%v err=%v", created, err)
	}
	input.SecretCommitment = strings.Repeat("c", 64)
	if _, _, err := repository.AcceptJob(ctx, input); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("changed credential commitment accepted: %v", err)
	}
	input.SecretCommitment = strings.Repeat("b", 64)
	input.Spec.Spec.Run.Snapshot.Entries[0].CaseDefinitions[0].Name = "different input"
	if _, _, err := repository.AcceptJob(ctx, input); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("changed input accepted: %v", err)
	}
	page, err := repository.JobEvents(ctx, spec.JobID, 0, 200)
	if err != nil || len(page.Events) != 1 || page.HasMore || page.LastSequence != 1 {
		t.Fatalf("admission journal=%+v err=%v", page, err)
	}
	var event jobcontract.AcceptedEvent
	if err := json.Unmarshal(page.Events[0].Payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Spec.Spec.Run.Snapshot.Entries[0].CaseDefinitions[0].Name == "different input" {
		t.Fatal("submission mutation changed durable acceptance")
	}
}

func TestConcurrentJobAdmissionCreatesOnePersistentInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "job-test"}); err != nil {
		t.Fatal(err)
	}
	first := reopenHardeningRepository(t, path)
	defer first.Close()
	second := reopenHardeningRepository(t, path)
	defer second.Close()
	repositories := []*persistence.Repository{first, second}
	input := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	var createdCount atomic.Int64
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, created, err := repositories[index%len(repositories)].AcceptJob(t.Context(), input)
			if err != nil {
				t.Errorf("concurrent admission: %v", err)
			}
			if created {
				createdCount.Add(1)
			}
		}()
	}
	group.Wait()
	if createdCount.Load() != 1 {
		t.Fatalf("created %d persistent instances", createdCount.Load())
	}
	page, err := first.JobEvents(t.Context(), input.Spec.JobID, 0, 200)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("retries appended events: %v", err)
	}
}

func comparisonJobSpec(t *testing.T) jobcontract.JobSpec {
	t.Helper()
	base := runWithCompleteSnapshot(t)
	secondSnapshot := base.Snapshot()
	secondSnapshot.Channel.ID = "72000000-0000-4000-8000-000000000003"
	secondSnapshot.Mapping.ID = "72000000-0000-4000-8000-000000000004"
	secondSnapshot.Mapping.ChannelID = secondSnapshot.Channel.ID
	second, err := domain.NewRun(
		entityMeta("72000000-0000-4000-8000-000000000005", 1), base.PlanID(), secondSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	spec := jobSpecForRun(t, base)
	spec.Kind = jobcontract.KindComparison
	spec.Spec = jobcontract.Payload{Comparison: &comparisons.PreparedComparison{
		Meta: entityMeta("72000000-0000-4000-8000-000000000006", 1),
		Plan: base.Snapshot().Plan, Model: base.Snapshot().Model.EntityRevisionRef,
		Runs: []runs.PreparedRun{
			{Meta: base.Meta(), PlanID: base.PlanID(), Snapshot: base.Snapshot()},
			{Meta: second.Meta(), PlanID: second.PlanID(), Snapshot: second.Snapshot()},
		},
	}}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestComparisonAdmissionRollsBackWholeGroupOnLateChildConflict(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	spec := comparisonJobSpec(t)
	second := spec.Spec.Comparison.Runs[1]
	existing, err := domain.NewRun(second.Meta, second.PlanID, second.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), existing); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("late child conflict=%v", err)
	}
	if _, err := repository.GetJob(t.Context(), spec.JobID); !errors.Is(err, jobhost.ErrNotFound) {
		t.Fatalf("failed admission left a job: %v", err)
	}
	firstRunID := spec.Spec.Comparison.Runs[0].Meta.ID
	if _, err := repository.GetRun(t.Context(), firstRunID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("failed admission left the earlier child: %v", err)
	}
	if _, err := repository.GetComparison(t.Context(), spec.Spec.Comparison.Meta.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("failed admission left a comparison: %v", err)
	}
	if _, err := repository.GetRun(t.Context(), existing.Meta().ID); err != nil {
		t.Fatalf("rollback changed pre-existing child: %v", err)
	}
}

func TestComparisonAdmissionPersistsEveryInitialObjectTogether(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	spec := comparisonJobSpec(t)
	snapshot, created, err := repository.AcceptJob(t.Context(), jobAcceptance(spec))
	if err != nil || !created {
		t.Fatalf("comparison admission created=%v err=%v", created, err)
	}
	if err := snapshot.Validate(spec); err != nil {
		t.Fatal(err)
	}
	for _, id := range snapshot.Receipt.RunIDs {
		if _, err := repository.GetRun(t.Context(), id); err != nil {
			t.Fatalf("accepted comparison lost child: %v", err)
		}
	}
	comparison, err := repository.GetComparison(t.Context(), snapshot.Receipt.ComparisonID)
	if err != nil || len(comparison.Runs()) != len(snapshot.Receipt.RunIDs) {
		t.Fatalf("accepted comparison root: %v", err)
	}
}

func TestOwnedRunFactsAndReportsCannotCommitWithoutTheirJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "job-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	fixture := newRepositoryFixture(t)
	spec := jobSpecForRun(t, fixture.run)
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER inject_run_event_failure BEFORE INSERT ON execution_job_events
		WHEN NEW.kind = 'run' BEGIN SELECT RAISE(ABORT, 'injected event failure'); END`); err != nil {
		t.Fatal(err)
	}
	starting, err := fixture.run.Transition(domain.RunStarting, fixture.run.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), 1, starting); err == nil {
		t.Fatal("run transition committed without its event")
	}
	unchanged, err := repository.GetRun(t.Context(), fixture.run.Meta().ID)
	if err != nil || unchanged.Meta().Revision != 1 {
		t.Fatalf("failed journal changed run revision: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER inject_run_event_failure`); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), 1, starting); err != nil {
		t.Fatal(err)
	}
	running, err := starting.Transition(domain.RunRunning, starting.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(t.Context(), starting.Meta().Revision, running); err != nil {
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
	if err := repository.UpdateRun(t.Context(), running.Meta().Revision, completed); err != nil {
		t.Fatal(err)
	}
	before, err := repository.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER inject_report_event_failure BEFORE INSERT ON execution_job_events
		WHEN NEW.kind = 'report' BEGIN SELECT RAISE(ABORT, 'injected event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateReport(t.Context(), fixture.report); err == nil {
		t.Fatal("report committed without its event")
	}
	if _, err := repository.GetReport(t.Context(), fixture.report.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("failed journal left a report: %v", err)
	}
	failed, err := repository.GetJob(t.Context(), spec.JobID)
	if err != nil || failed.LatestSequence != before.LatestSequence {
		t.Fatalf("failed report advanced journal: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER inject_report_event_failure`); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateReport(t.Context(), fixture.report); err != nil {
		t.Fatalf("failed archive left run sealed or attachment rows: %v", err)
	}
	page, err := repository.JobEvents(t.Context(), spec.JobID, 0, 200)
	if err != nil || len(page.Events) != 7 || page.HasMore {
		t.Fatalf("facts journal: count=%d err=%v", len(page.Events), err)
	}
	for index, kind := range []jobcontract.EventKind{
		jobcontract.EventAccepted, jobcontract.EventRun, jobcontract.EventRun,
		jobcontract.EventEvidence, jobcontract.EventResults, jobcontract.EventRun, jobcontract.EventReport,
	} {
		if page.Events[index].Kind != kind || page.Events[index].Sequence != uint64(index+1) {
			t.Fatalf("fact order differs at event %d", index)
		}
	}
	var reference jobcontract.ReportReference
	if err := json.Unmarshal(page.Events[6].Payload, &reference); err != nil {
		t.Fatal(err)
	}
	var reportDocument []byte
	err = db.QueryRowContext(
		t.Context(), `SELECT document_json FROM reports WHERE id = ?`, fixture.report.ID,
	).Scan(&reportDocument)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(reportDocument)
	if reference.ID != fixture.report.ID || reference.Size != int64(len(reportDocument)) {
		t.Fatal("report reference does not identify the stored artifact")
	}
	if reference.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("report reference does not hash the downloadable bytes")
	}
}

func TestJobStateAndJournalCommitTogetherAndTerminalStateWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "job-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	input := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	if _, _, err := repository.AcceptJob(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER inject_job_event_failure BEFORE INSERT ON execution_job_events
		WHEN NEW.kind = 'state' BEGIN SELECT RAISE(ABORT, 'injected event failure'); END`); err != nil {
		t.Fatal(err)
	}
	change := jobhost.Change{
		JobID: input.Spec.JobID, ExpectedRevision: 1, Status: domain.RunStarting,
		At: input.AcceptedAt.Add(time.Second),
	}
	if _, err := repository.ChangeJob(t.Context(), change); err == nil {
		t.Fatal("injected journal failure was ignored")
	}
	unchanged, err := repository.GetJob(t.Context(), input.Spec.JobID)
	if err != nil || unchanged.Receipt.Revision != 1 || unchanged.LatestSequence != 1 {
		t.Fatalf("failed journal advanced state: %+v err=%v", unchanged, err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER inject_job_event_failure`); err != nil {
		t.Fatal(err)
	}
	starting, err := repository.ChangeJob(t.Context(), change)
	if err != nil || starting.Receipt.Revision != 2 || starting.LatestSequence != 2 {
		t.Fatalf("starting state=%+v err=%v", starting, err)
	}
	change.ExpectedRevision, change.Status = 2, domain.RunCancelled
	change.At = change.At.Add(time.Second)
	if _, err := repository.ChangeJob(t.Context(), change); err != nil {
		t.Fatal(err)
	}
	change.ExpectedRevision, change.Status = 3, domain.RunRunning
	if _, err := repository.ChangeJob(t.Context(), change); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("terminal state was overwritten: %v", err)
	}
	page, err := repository.JobEvents(t.Context(), input.Spec.JobID, 0, 2)
	if err != nil || len(page.Events) != 2 || !page.HasMore || page.LastSequence != 2 {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	next, err := repository.JobEvents(t.Context(), input.Spec.JobID, page.LastSequence, 2)
	if err != nil || len(next.Events) != 1 || next.HasMore || next.LastSequence != 3 {
		t.Fatalf("second page=%+v err=%v", next, err)
	}
	if _, err := repository.JobEvents(t.Context(), input.Spec.JobID, 4, 2); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatalf("future event cursor was accepted: %v", err)
	}
	active, err := repository.ActiveJobs(t.Context())
	if err != nil || len(active) != 0 {
		t.Fatalf("terminal job remained active: count=%d err=%v", len(active), err)
	}
}

func TestOwnedPerformanceArchiveAndReferenceAreWrittenTogether(t *testing.T) {
	for _, admitFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("admit-first-%v", admitFirst), func(t *testing.T) {
			repository := openOperationalRepository(t)
			defer repository.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
				fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n")
				fmt.Fprint(writer, "data: [DONE]\n\n")
			}))
			defer server.Close()
			prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
				t.Context(),
				quicktest.PerformanceCommand{
					Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
					URL: server.URL, ModelID: "model", APIKey: "performance-local-test-secret",
					RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
				},
			)
			if code != "" {
				t.Fatal(code)
			}
			defer lease.Close()
			spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
			spec.Kind = jobcontract.KindPerformance
			spec.Spec = jobcontract.Payload{Performance: &prepared}
			if admitFirst {
				if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
					t.Fatal(err)
				}
			}
			host := quicktest.NewHost(quicktest.HostDependencies{Transport: server.Client().Transport, Archive: repository})
			report, err := host.ExecutePrepared(t.Context(), prepared, lease, quicktest.ExecutionOptions{})
			if err != nil || !report.Archived {
				t.Fatalf("owned performance archive failed: status=%s err=%v", report.ArchiveStatus, err)
			}
			if _, err := repository.GetQuickPerformanceReport(t.Context(), prepared.ReportID); err != nil {
				t.Fatalf("journal reference has no stored performance report: %v", err)
			}
			if !admitFirst {
				_, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec))
				if !errors.Is(err, jobhost.ErrConflict) {
					t.Fatalf("existing archive identity was accepted: %v", err)
				}
				if _, err := repository.GetJob(t.Context(), spec.JobID); !errors.Is(err, jobhost.ErrNotFound) {
					t.Fatalf("archive identity conflict left an accepted job: %v", err)
				}
				return
			}
			page, err := repository.JobEvents(t.Context(), spec.JobID, 0, 200)
			if err != nil || len(page.Events) != 2 || page.Events[1].Kind != jobcontract.EventReport {
				t.Fatalf("performance report journal: count=%d err=%v", len(page.Events), err)
			}
			var reference jobcontract.ReportReference
			if err := json.Unmarshal(page.Events[1].Payload, &reference); err != nil {
				t.Fatal(err)
			}
			if reference.ID != prepared.ReportID || reference.Kind != jobcontract.KindPerformance {
				t.Fatal("performance reference does not preserve prepared identity and type")
			}
		})
	}
}
