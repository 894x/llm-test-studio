package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

func historyRemoval(entry historyrecycle.Entry) historyrecycle.Removal {
	return historyrecycle.Removal{Target: historyrecycle.Target{Kind: entry.Kind, ID: entry.ID}, RemovedAt: entry.RemovedAt}
}

func TestHistoryDeleteReportBatchPersistsAndPreservesOtherReports(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	ids := []string{"79000000-0000-4000-8000-000000000001", "79000000-0000-4000-8000-000000000002"}
	removals := []historyrecycle.Removal{}
	for _, id := range ids {
		if err := repository.SaveQuickPerformanceReport(t.Context(), validQuickPerformanceReport(id, repositoryEpoch.Format(time.RFC3339Nano))); err != nil {
			t.Fatal(err)
		}
		entry, err := repository.RecycleHistory(t.Context(), historyrecycle.Target{Kind: "report", ID: id}, repositoryEpoch)
		if err != nil {
			t.Fatal(err)
		}
		removals = append(removals, historyRemoval(entry))
	}
	if _, err := repository.RestoreHistory(t.Context(), removals[1].Target); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteRecycledHistory(t.Context(), removals); !errors.Is(err, historyrecycle.ErrNotFound) {
		t.Fatalf("restored target accepted: %v", err)
	}
	if _, err := repository.GetQuickPerformanceReport(t.Context(), ids[0]); err != nil {
		t.Fatalf("partial batch deletion: %v", err)
	}
	if _, err := repository.DeleteRecycledHistory(t.Context(), removals[:1]); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	// Opening verifies the unchanged published schema; explicitly check integrity.
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	if err := repository.CheckIntegrity(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetQuickPerformanceReport(t.Context(), ids[0]); !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
		t.Fatalf("deleted report returned: %v", err)
	}
	if _, err := repository.GetQuickPerformanceReport(t.Context(), ids[1]); err != nil {
		t.Fatalf("unselected report lost: %v", err)
	}
	page, err := repository.ListRecycledHistory(t.Context(), "")
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("recycle records remain: %+v %v", page, err)
	}
	if _, err := repository.DeleteRecycledHistory(t.Context(), removals[:1]); !errors.Is(err, historyrecycle.ErrNotFound) {
		t.Fatalf("repeat deletion: %v", err)
	}
}

func settledRecycledFixture(t *testing.T, remote bool) (submittedReadFixture, historyrecycle.Entry) {
	t.Helper()
	fixture := newSubmittedReadFixture(t, remote)
	fixture.confirm(t)
	run, err := fixture.executor.GetRun(t.Context(), fixture.spec.Spec.Run.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	transitionRun(t, fixture.executor, run, domain.RunCancelled)
	fixture.snapshot, err = fixture.executor.ChangeJob(t.Context(), jobhost.Change{
		JobID: fixture.spec.JobID, ExpectedRevision: fixture.snapshot.Receipt.Revision,
		Status: domain.RunCancelled, At: fixture.snapshot.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.executor.MarkJobSettled(t.Context(), fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if remote {
		applyRemoteEvents(t, fixture.repository, remoteEvents(t, fixture.executor, fixture.spec))
	}
	entry, err := fixture.repository.RecycleHistory(t.Context(), historyrecycle.Target{Kind: "job", ID: fixture.spec.JobID}, repositoryEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, entry
}

func TestHistoryDeleteLocalJobAndRollbackRestoresTriggers(t *testing.T) {
	fixture, entry := settledRecycledFixture(t, false)
	removals := []historyrecycle.Removal{historyRemoval(entry)}
	var before string
	if err := fixture.db.QueryRow(`SELECT group_concat(sql,';') FROM (SELECT sql FROM sqlite_master WHERE type='trigger' ORDER BY name)`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Fail late, after the immutable revisions and their parent were deleted.
	if _, err := fixture.db.Exec(`CREATE TRIGGER fail_history_delete BEFORE DELETE ON execution_jobs BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.DeleteRecycledHistory(t.Context(), removals); err == nil {
		t.Fatal("injected failure succeeded")
	}
	if _, err := fixture.db.Exec(`DROP TRIGGER fail_history_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.GetRun(t.Context(), entry.RunIDs[0]); err != nil {
		t.Fatalf("rollback lost run: %v", err)
	}
	var after string
	if err := fixture.db.QueryRow(`SELECT group_concat(sql,';') FROM (SELECT sql FROM sqlite_master WHERE type='trigger' ORDER BY name)`).Scan(&after); err != nil || before != after {
		t.Fatalf("rollback changed triggers: %v", err)
	}
	if _, err := fixture.db.Exec(`DELETE FROM execution_run_revisions WHERE run_id=?`, entry.RunIDs[0]); err == nil {
		t.Fatal("immutable protection was lost")
	}
	if _, err := fixture.repository.DeleteRecycledHistory(t.Context(), removals); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"execution_jobs", "execution_job_runs", "execution_job_content", "execution_runs", "execution_run_revisions", "job_submissions", "history_recycle_entries", "history_recycle_members"} {
		var count int
		if err := fixture.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained %d: %v", table, count, err)
		}
	}
	if err := fixture.db.QueryRow(`SELECT group_concat(sql,';') FROM (SELECT sql FROM sqlite_master WHERE type='trigger' ORDER BY name)`).Scan(&after); err != nil || before != after {
		t.Fatalf("deletion changed triggers: %v", err)
	}
}

func TestHistoryDeleteRejectsRemoteStaleActiveAndCancelledContext(t *testing.T) {
	remote, remoteEntry := settledRecycledFixture(t, true)
	if _, err := remote.repository.DeleteRecycledHistory(t.Context(), []historyrecycle.Removal{historyRemoval(remoteEntry)}); !errors.Is(err, historyrecycle.ErrDeleteUnsupported) {
		t.Fatalf("remote deleted: %v", err)
	}
	fixture, entry := settledRecycledFixture(t, false)
	old := historyRemoval(entry)
	if _, err := fixture.repository.RestoreHistory(t.Context(), old.Target); err != nil {
		t.Fatal(err)
	}
	entry, err := fixture.repository.RecycleHistory(t.Context(), old.Target, repositoryEpoch.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.DeleteRecycledHistory(t.Context(), []historyrecycle.Removal{old}); !errors.Is(err, historyrecycle.ErrInvalid) {
		t.Fatalf("stale confirmation accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.repository.DeleteRecycledHistory(ctx, []historyrecycle.Removal{historyRemoval(entry)}); err == nil {
		t.Fatal("cancelled deletion succeeded")
	}
	if _, err := fixture.db.Exec(`UPDATE execution_jobs SET settled=0 WHERE id=?`, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.DeleteRecycledHistory(t.Context(), []historyrecycle.Removal{historyRemoval(entry)}); !errors.Is(err, historyrecycle.ErrActive) {
		t.Fatalf("unsettled deleted: %v", err)
	}
	page, err := fixture.repository.ListRecycledHistory(t.Context(), "")
	if err != nil || !reflect.DeepEqual(page.Entries, []historyrecycle.Entry{entry}) {
		t.Fatalf("rejected operation changed entries: %v", err)
	}
}

func TestHistoryDeleteComparisonRemovesWholeGroup(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	group := historyComparison(t, repository, "79000000-0000-4000-8000-000000000001")
	for _, child := range group.Runs() {
		run, err := repository.GetRun(t.Context(), child.RunID)
		if err != nil {
			t.Fatal(err)
		}
		transitionRun(t, repository, run, domain.RunCancelled)
	}
	next, err := group.Transition(domain.ComparisonCancelled, repositoryEpoch.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateComparison(t.Context(), group.Meta().Revision, next); err != nil {
		t.Fatal(err)
	}
	entry, err := repository.RecycleHistory(t.Context(), historyrecycle.Target{Kind: "comparison", ID: group.Meta().ID}, repositoryEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteRecycledHistory(t.Context(), []historyrecycle.Removal{historyRemoval(entry)}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CheckIntegrity(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, child := range group.Runs() {
		if _, err := repository.GetRun(t.Context(), child.RunID); err == nil {
			t.Fatal("deleted child run remains")
		}
	}
	if _, err := repository.GetComparison(t.Context(), group.Meta().ID); err == nil {
		t.Fatal("deleted comparison remains")
	}
}
