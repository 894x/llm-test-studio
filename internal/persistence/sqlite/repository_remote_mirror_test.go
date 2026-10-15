package sqlite_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

var remoteOrigin = remoteresults.Origin{
	ConfigurationID: "81000000-0000-4000-8000-000000000001",
	NodeID:          "81000000-0000-4000-8000-000000000002",
	InstallationID:  "72000000-0000-4000-8000-000000000002",
}

func remoteSpec(spec jobcontract.JobSpec) jobcontract.JobSpec {
	spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: remoteOrigin.NodeID}
	return spec
}

func remoteEvents(t *testing.T, repository *persistence.Repository, spec jobcontract.JobSpec) []jobcontract.Event {
	t.Helper()
	page, err := repository.JobEvents(
		t.Context(),
		spec.JobID,
		0,
		200,
	)
	if err != nil || page.HasMore {
		t.Fatalf("node journal: more=%v error=%v", page.HasMore, err)
	}
	return page.Events
}

func applyRemoteEvents(t *testing.T, repository *persistence.Repository, events []jobcontract.Event) remoteresults.Mirror {
	t.Helper()
	value, err := repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
		Origin: remoteOrigin, JobID: events[0].JobID, AfterSequence: events[0].Sequence - 1, Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func populateRemoteRun(t *testing.T, node *persistence.Repository, fixture repositoryFixture) jobcontract.JobSpec {
	t.Helper()
	spec := remoteSpec(jobSpecForRun(t, fixture.run))
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	current := fixture.run
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		next, err := current.Transition(status, current.Meta().UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := node.UpdateRun(t.Context(), current.Meta().Revision, next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
		previous, err := node.GetJob(t.Context(), spec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := node.ChangeJob(t.Context(), jobhost.Change{
			JobID: spec.JobID, ExpectedRevision: previous.Receipt.Revision, Status: status, At: previous.UpdatedAt.Add(time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The job's terminal decision may precede result/report settlement.
	if err := node.CreateEvidence(t.Context(), fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := node.AppendResults(t.Context(), fixture.result); err != nil {
		t.Fatal(err)
	}
	completed, err := current.Transition(domain.RunCompleted, current.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateRun(t.Context(), current.Meta().Revision, completed); err != nil {
		t.Fatal(err)
	}
	if err := node.CreateReport(t.Context(), fixture.report); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestRemoteMirrorReplaysFactsAndImportsOriginalRunReport(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	fixture := newRepositoryFixture(t)
	spec := populateRemoteRun(t, node, fixture)
	events := remoteEvents(t, node, spec)
	path := filepath.Join(t.TempDir(), "mirror.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "mirror-test"}); err != nil {
		t.Fatal(err)
	}
	desktop := reopenHardeningRepository(t, path)
	applyRemoteEvents(t, desktop, events[:3])
	if err := desktop.Close(); err != nil {
		t.Fatal(err)
	}
	desktop = reopenHardeningRepository(t, path)
	defer desktop.Close()
	// A stale overlapping page is an idempotent acknowledgement retry.
	got := applyRemoteEvents(t, desktop, events[1:])
	want, err := node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"mirrored job",
		want,
		got.Snapshot,
	)
	for _, page := range [][]jobcontract.Event{events, events[2:6], events[5:]} {
		applyRemoteEvents(t, desktop, page)
	}
	result, err := desktop.GetResult(t.Context(), fixture.result.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"mirrored result",
		fixture.result,
		result,
	)
	evidence, err := desktop.GetEvidence(t.Context(), fixture.evidence.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"mirrored evidence index",
		fixture.evidence,
		evidence,
	)
	imported, err := desktop.RemoteRunReportImported(t.Context(), spec.JobID, fixture.report.ID)
	if err != nil || imported {
		t.Fatalf("pending report marked complete: %v", err)
	}
	if _, err := desktop.GetReport(t.Context(), fixture.report.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("metadata alone published report: %v", err)
	}
	document, err := jobcontract.EncodeRunReport(fixture.report)
	if err != nil {
		t.Fatal(err)
	}
	input := remoteresults.RunReportImport{Origin: remoteOrigin, JobID: spec.JobID, Document: document}
	if err := desktop.CreateReport(t.Context(), fixture.report); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatalf("local report writer modified remote run: %v", err)
	}
	changedReport := fixture.report
	changedReport.Conclusion.Verdict = "changed"
	changedDocument := canonicalFixtureJSON(t, changedReport)
	wrongOrigin := remoteOrigin
	wrongOrigin.InstallationID = remoteOrigin.NodeID
	for _, rejected := range []remoteresults.RunReportImport{
		{Origin: remoteOrigin, JobID: spec.JobID, Document: changedDocument},
		{Origin: wrongOrigin, JobID: spec.JobID, Document: document},
	} {
		if err := desktop.ImportRemoteRunReport(t.Context(), rejected); err == nil {
			t.Fatal("unverified report imported")
		}
		if _, err := desktop.GetReport(t.Context(), fixture.report.ID); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if err := desktop.ImportRemoteRunReport(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err := desktop.ImportRemoteRunReport(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	report, err := desktop.GetReport(t.Context(), fixture.report.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"offline report",
		fixture.report,
		report,
	)
	imported, err = desktop.RemoteRunReportImported(t.Context(), spec.JobID, fixture.report.ID)
	if err != nil || !imported {
		t.Fatalf("verified report remained pending: %v", err)
	}
	// Mirrors are observable but never become locally executable/recoverable jobs.
	active, err := desktop.ActiveJobs(t.Context())
	if err != nil || len(active) != 0 {
		t.Fatalf("mirror became local job: %v", err)
	}
	if _, err := desktop.GetJob(t.Context(), spec.JobID); !errors.Is(err, jobhost.ErrNotFound) {
		t.Fatal(err)
	}
	if _, _, err := desktop.AcceptJob(t.Context(), jobAcceptance(spec)); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal(err)
	}
	// A new sequence can repeat the same immutable fact after report publication.
	repeated := events[7]
	repeated.Sequence = uint64(len(events) + 1)
	if repeated.Kind != jobcontract.EventResults {
		t.Fatalf("fixture result event: %s", repeated.Kind)
	}
	applyRemoteEvents(t, desktop, []jobcontract.Event{repeated})
}

func TestRemoteMirrorRejectsInvalidCurrentPayloadsWithoutMutation(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	fixture := newRepositoryFixture(t)
	spec := populateRemoteRun(t, node, fixture)
	events := remoteEvents(t, node, spec)
	desktop := openOperationalRepository(t)
	defer desktop.Close()
	applyRemoteEvents(t, desktop, events)
	changedResult := fixture.result
	changedResult.Metrics = map[string]float64{"e2e_ms": 999}
	foreignCase := fixture.result
	foreignCase.ID = "81000000-0000-4000-8000-000000000011"
	foreignCase.CaseID = "81000000-0000-4000-8000-000000000012"
	var unsupportedRun map[string]any
	if err := json.Unmarshal(events[2].Payload, &unsupportedRun); err != nil {
		t.Fatal(err)
	}
	unsupportedRun["schema_version"] = 0
	for _, test := range []struct {
		name    string
		kind    jobcontract.EventKind
		payload []byte
	}{
		{name: "changed immutable result", kind: jobcontract.EventResults, payload: canonicalFixtureJSON(t, changedResult)},
		{name: "case outside frozen snapshot", kind: jobcontract.EventResults, payload: canonicalFixtureJSON(t, foreignCase)},
		{name: "unsupported run format", kind: jobcontract.EventRun, payload: canonicalFixtureJSON(t, unsupportedRun)},
		{name: "missing current run fields", kind: jobcontract.EventRun, payload: []byte(`{}`)},
		{name: "second acceptance", kind: jobcontract.EventAccepted, payload: events[0].Payload},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := events[len(events)-1]
			event.Sequence, event.Kind, event.Payload = event.Sequence+1, test.kind, test.payload
			if _, err := desktop.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
				Origin: remoteOrigin, JobID: spec.JobID, AfterSequence: uint64(len(events)), Events: []jobcontract.Event{event},
			}); err == nil {
				t.Fatal("invalid current payload accepted")
			}
			mirror, err := desktop.GetRemoteMirror(t.Context(), spec.JobID)
			if err != nil || mirror.Snapshot.LatestSequence != uint64(len(events)) {
				t.Fatalf("rejection advanced cursor: %v", err)
			}
			stored, err := desktop.GetResult(t.Context(), fixture.result.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(
				t,
				"rejected data unchanged",
				fixture.result,
				stored,
			)
		})
	}
}

func TestRemoteMirrorFailureRollsBackWholePageAndCursor(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	fixture := newRepositoryFixture(t)
	spec := populateRemoteRun(t, node, fixture)
	events := remoteEvents(t, node, spec)
	path := filepath.Join(t.TempDir(), "mirror-rollback.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "mirror-test"}); err != nil {
		t.Fatal(err)
	}
	desktop := reopenHardeningRepository(t, path)
	defer desktop.Close()
	db := openDatabase(t, path)
	defer db.Close()
	applyRemoteEvents(t, desktop, events[:1])
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_remote_result BEFORE INSERT ON remote_job_events
		WHEN NEW.kind = 'results' BEGIN SELECT RAISE(ABORT, 'test import failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
		Origin: remoteOrigin, JobID: spec.JobID, AfterSequence: 1, Events: events[1:],
	}); err == nil {
		t.Fatal("failed event journal committed facts")
	}
	mirror, err := desktop.GetRemoteMirror(t.Context(), spec.JobID)
	if err != nil || mirror.Snapshot.LatestSequence != 1 {
		t.Fatalf("failed import advanced cursor: %v", err)
	}
	run, err := desktop.GetRun(t.Context(), fixture.run.Meta().ID)
	if err != nil || run.Meta().Revision != 1 {
		t.Fatalf("failed import changed run: %v", err)
	}
	if _, err := desktop.GetEvidence(t.Context(), fixture.evidence.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := desktop.GetResult(t.Context(), fixture.result.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_remote_result`); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, events[1:])
	// A failure while marking the file imported also rolls back the seal/artifacts.
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_remote_report BEFORE UPDATE OF imported ON remote_job_report_refs
		BEGIN SELECT RAISE(ABORT, 'test report failure'); END`); err != nil {
		t.Fatal(err)
	}
	document, err := jobcontract.EncodeRunReport(fixture.report)
	if err != nil {
		t.Fatal(err)
	}
	input := remoteresults.RunReportImport{Origin: remoteOrigin, JobID: spec.JobID, Document: document}
	if err := desktop.ImportRemoteRunReport(t.Context(), input); err == nil {
		t.Fatal("report import partially committed")
	}
	if _, err := desktop.GetReport(t.Context(), fixture.report.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_remote_report`); err != nil {
		t.Fatal(err)
	}
	if err := desktop.ImportRemoteRunReport(t.Context(), input); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteMirrorRejectsOriginGapsChangedEventsAndLocalCollisions(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	fixture := newRepositoryFixture(t)
	spec := populateRemoteRun(t, node, fixture)
	events := remoteEvents(t, node, spec)
	desktop := openOperationalRepository(t)
	defer desktop.Close()
	applyRemoteEvents(t, desktop, events[:3])
	base := remoteresults.Batch{Origin: remoteOrigin, JobID: spec.JobID, Events: events}
	changed := append([]jobcontract.Event{}, events...)
	changed[1].CreatedAt = changed[1].CreatedAt.Add(time.Hour)
	for _, input := range []remoteresults.Batch{
		{Origin: remoteresults.Origin{ConfigurationID: remoteOrigin.NodeID, NodeID: remoteOrigin.NodeID, InstallationID: remoteOrigin.InstallationID}, JobID: spec.JobID, Events: events},
		{Origin: remoteOrigin, JobID: spec.JobID, AfterSequence: 4, Events: events[4:]},
		{Origin: remoteOrigin, JobID: spec.JobID, Events: changed},
	} {
		if _, err := desktop.ApplyRemoteBatch(t.Context(), input); err == nil {
			t.Fatal("conflicting remote page accepted")
		}
		got, err := desktop.GetRemoteMirror(t.Context(), spec.JobID)
		if err != nil || got.Snapshot.LatestSequence != 3 {
			t.Fatalf("rejection changed cursor: %v", err)
		}
	}
	// Exactly matching local data is still locally owned and cannot be adopted.
	local := openOperationalRepository(t)
	defer local.Close()
	if err := local.CreateRun(t.Context(), fixture.run); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ApplyRemoteBatch(t.Context(), base); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("local owner replaced: %v", err)
	}
	if _, err := local.GetRemoteMirror(t.Context(), spec.JobID); !errors.Is(err, remoteresults.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := desktop.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
		Origin: remoteOrigin, JobID: spec.JobID, AfterSequence: 3, Events: []jobcontract.Event{},
	}); err != nil {
		t.Fatal(err)
	}
}

type remoteRecoveryClock struct{}

func (remoteRecoveryClock) Now() time.Time { return repositoryEpoch.Add(time.Hour) }

func TestRemoteMirrorLocalRecoveryAndComparisonRefreshKeepRemoteOwnership(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	spec := remoteSpec(comparisonJobSpec(t))
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range spec.PreparedRuns() {
		current, err := node.GetRun(t.Context(), prepared.Meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		cancelled, err := current.Transition(domain.RunCancelled, repositoryEpoch.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := node.UpdateRun(t.Context(), current.Meta().Revision, cancelled); err != nil {
			t.Fatal(err)
		}
	}
	desktop := openOperationalRepository(t)
	defer desktop.Close()
	// Stop at the real page boundary before the node's comparison event.
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	refresher, err := comparisons.NewRefresher(comparisons.RefresherDependencies{
		Repository: desktop, Runs: desktop, Clock: remoteRecoveryClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := refresher.Refresh(t.Context(), spec.Spec.Comparison.Meta.ID)
	if err != nil || comparison.Status() != domain.ComparisonRunning {
		t.Fatalf("desktop aggregated node state: %v", err)
	}
	terminal, err := comparison.Transition(domain.ComparisonCancelled, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := desktop.UpdateComparison(t.Context(), 1, terminal); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal(err)
	}
	if err := node.UpdateComparison(t.Context(), 1, terminal); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	comparison, err = refresher.Refresh(t.Context(), spec.Spec.Comparison.Meta.ID)
	if err != nil || comparison.Status() != domain.ComparisonCancelled {
		t.Fatal(err)
	}
	// A separate still-active mirror must survive desktop startup recovery.
	activeNode := openOperationalRepository(t)
	defer activeNode.Close()
	fixture := newRepositoryFixture(t)
	activeSpec := remoteSpec(jobSpecForRun(t, fixture.run))
	activeSpec.JobID = "81000000-0000-4000-8000-000000000003"
	if _, _, err := activeNode.AcceptJob(t.Context(), jobAcceptance(activeSpec)); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, remoteEvents(t, activeNode, activeSpec))
	if err := runs.RecoverInterrupted(t.Context(), desktop, remoteRecoveryClock{}); err != nil {
		t.Fatal(err)
	}
	activeRun, err := desktop.GetRun(t.Context(), fixture.run.Meta().ID)
	if err != nil || activeRun.Status() != domain.RunQueued {
		t.Fatalf("startup cancelled node task: %v", err)
	}
	starting, err := activeRun.Transition(domain.RunStarting, repositoryEpoch.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := desktop.UpdateRun(t.Context(), 1, starting); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal(err)
	}
	if err := desktop.AppendResults(t.Context(), fixture.result); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal(err)
	}
	if err := desktop.CreateEvidence(t.Context(), fixture.evidence); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal(err)
	}
}

func TestRemoteMirrorConcurrentIndependentRepositoriesReplayOneJournal(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	spec := populateRemoteRun(t, node, newRepositoryFixture(t))
	events := remoteEvents(t, node, spec)
	path := filepath.Join(t.TempDir(), "shared-mirror.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "mirror-test"}); err != nil {
		t.Fatal(err)
	}
	first, second := reopenHardeningRepository(t, path), reopenHardeningRepository(t, path)
	defer first.Close()
	defer second.Close()
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			repository := []*persistence.Repository{first, second}[index%2]
			_, err := repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{Origin: remoteOrigin, JobID: spec.JobID, Events: events})
			if err != nil {
				t.Errorf("concurrent import: %v", err)
			}
		}()
	}
	group.Wait()
	db := openDatabase(t, path)
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM remote_job_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(events) {
		t.Fatalf("replay duplicated journal: %d", count)
	}
}

func TestRemoteMirrorPerformanceJournalKeepsArchiveAndResultReferencesSeparate(t *testing.T) {
	core := newPerformanceStorageFixture(t)
	node := openOperationalRepository(t)
	defer node.Close()
	spec := remoteSpec(core.spec)
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		previous, err := node.GetJob(t.Context(), spec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := node.ChangeJob(t.Context(), jobhost.Change{
			JobID: spec.JobID, ExpectedRevision: previous.Receipt.Revision,
			Status: status, At: previous.UpdatedAt.Add(time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range core.checkpoints {
		if _, err := node.RecordPerformanceObservation(t.Context(), core.input(index)); err != nil {
			t.Fatal(err)
		}
	}
	finalReport := core.checkpoints[len(core.checkpoints)-1].Report
	finalReport.Archived = true
	finalReport.ArchiveStatus = quicktest.PerformanceArchiveArchived
	finalReport.GeneratedAt = jobAcceptance(spec).AcceptedAt.Add(time.Minute).Format(time.RFC3339Nano)
	if err := node.SaveQuickPerformanceReport(t.Context(), finalReport); err != nil {
		t.Fatal(err)
	}
	previous, err := node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.RecordJobProgress(
		t.Context(),
		spec.JobID,
		finalReport.Progress,
		previous.UpdatedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	previous, err = node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.ChangeJob(t.Context(), jobhost.Change{
		JobID: spec.JobID, ExpectedRevision: previous.Receipt.Revision,
		Status: domain.RunCompleted, At: previous.UpdatedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	previous, err = node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.SealPerformanceResult(t.Context(), spec.JobID, previous.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "performance-mirror.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "mirror-test"}); err != nil {
		t.Fatal(err)
	}
	desktop := reopenHardeningRepository(t, path)
	defer desktop.Close()
	events := remoteEvents(t, node, spec)
	for start := 0; start < len(events); start += 3 {
		applyRemoteEvents(t, desktop, events[start:min(start+3, len(events))])
	}
	applyRemoteEvents(t, desktop, events)
	db := openDatabase(t, path)
	defer db.Close()
	var referenceCount, importedCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), COALESCE(SUM(imported), 0)
		FROM remote_job_report_refs WHERE job_id = ?`, spec.JobID).Scan(&referenceCount, &importedCount); err != nil {
		t.Fatal(err)
	}
	if referenceCount != 2 || importedCount != 0 {
		t.Fatalf("conflated performance documents: refs=%d imported=%d", referenceCount, importedCount)
	}
	if err := desktop.SaveQuickPerformanceReport(t.Context(), finalReport); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatalf("local archive writer adopted remote ID: %v", err)
	}
	// An ordinary late progress event remains an observation of the terminal job.
	last := events[len(events)-1]
	last.Kind, last.Sequence = jobcontract.EventProgress, last.Sequence+1
	last.Payload = canonicalFixtureJSON(t, finalReport.Progress)
	applyRemoteEvents(t, desktop, []jobcontract.Event{last})
}

func TestRemoteMirrorImportsRealCoreWarmupAndRampProgress(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: server.URL, ModelID: "mirror-progress", APIKey: "mirror-test-secret",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		WarmupRequests: 1, RampDurationMS: 10, RampRequestCap: 1,
	})
	if code != "" {
		t.Fatal(code)
	}
	defer lease.Close()
	node := openOperationalRepository(t)
	defer node.Close()
	spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	spec.Kind, spec.Spec = jobcontract.KindPerformance, jobcontract.Payload{Performance: &prepared}
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	host := quicktest.NewHost(quicktest.HostDependencies{Transport: server.Client().Transport})
	seen := map[string]bool{}
	var mu sync.Mutex
	report, err := host.ExecutePrepared(
		t.Context(),
		prepared,
		lease,
		quicktest.ExecutionOptions{
			OnProgress: func(progress quicktest.PerformanceProgress) {
				mu.Lock()
				defer mu.Unlock()
				seen[string(progress.Phase)] = true
				if err := node.RecordJobProgress(
					t.Context(),
					spec.JobID,
					progress,
					time.Now().UTC(),
				); err != nil {
					t.Error(err)
				}
			},
		},
	)
	if err != nil || !report.Success {
		t.Fatalf("actual performance Core: success=%v error=%v", report.Success, err)
	}
	if !seen[string(quicktest.PerformancePhaseWarmingUp)] || !seen[string(quicktest.PerformancePhaseRamping)] {
		t.Fatalf("actual phase callbacks missing: %v", seen)
	}
	desktop := openOperationalRepository(t)
	defer desktop.Close()
	events := remoteEvents(t, node, spec)
	applyRemoteEvents(t, desktop, events)
	bad := events[len(events)-1]
	bad.Sequence++
	bad.Payload = canonicalFixtureJSON(t, quicktest.PerformanceProgress{Phase: "unsupported_phase"})
	if _, err := desktop.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
		Origin: remoteOrigin, JobID: spec.JobID, AfterSequence: uint64(len(events)), Events: []jobcontract.Event{bad},
	}); err == nil {
		t.Fatal("unsupported current phase accepted")
	}
}
