package sqlite_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestHistoryRecycleRejectsActiveUnknownAndUnconfirmedWithoutMutation(t *testing.T) {
	fixture := newSubmittedReadFixture(t, true)
	target := historyrecycle.Target{Kind: "job", ID: fixture.spec.JobID}
	preview, err := fixture.repository.PreviewHistoryRecycle(t.Context(), target)
	if err != nil || preview.CanRecycle || preview.BlockedReason != "active" {
		t.Fatalf("active preview: %+v %v", preview, err)
	}
	if _, err := fixture.repository.RecycleHistory(t.Context(), target, repositoryEpoch); !errors.Is(err, historyrecycle.ErrActive) {
		t.Fatalf("active accepted: %v", err)
	}
	for _, kind := range []string{"job", "run", "report", "comparison"} {
		unknown := historyrecycle.Target{Kind: kind, ID: "79000000-0000-4000-8000-000000000099"}
		if _, err := fixture.repository.RecycleHistory(t.Context(), unknown, repositoryEpoch); !errors.Is(err, historyrecycle.ErrNotFound) {
			t.Fatalf("unknown %s: %v", kind, err)
		}
	}
	page, err := fixture.repository.ListRecycledHistory(t.Context(), "")
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("rejected mutation changed recycle bin: %+v %v", page, err)
	}
	prepared := fixture.record
	prepared.Prepared.Spec.JobID = "79000000-0000-4000-8000-000000000001"
	prepared.Prepared.Spec.Spec.Run.Meta.ID = "79000000-0000-4000-8000-000000000002"
	prepared = submissionFixture(t, prepared.Prepared.Spec)
	if _, err := fixture.repository.CreateSubmission(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	preview, err = fixture.repository.PreviewHistoryRecycle(t.Context(), historyrecycle.Target{Kind: "job", ID: prepared.Prepared.Spec.JobID})
	if err != nil || preview.CanRecycle || preview.BlockedReason != "unconfirmed" {
		t.Fatalf("unknown execution classified terminal: %+v %v", preview, err)
	}
}

func TestHistoryRecycleLocalJobRequiresSettlementAndPreservesRawHistory(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	fixture.confirm(t)
	run, err := fixture.repository.GetRun(t.Context(), fixture.spec.Spec.Run.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	transitionRun(t, fixture.repository, run, domain.RunCancelled)
	terminal, err := fixture.repository.ChangeJob(t.Context(), jobhost.Change{JobID: fixture.spec.JobID,
		ExpectedRevision: fixture.snapshot.Receipt.Revision, Status: domain.RunCancelled, At: fixture.snapshot.UpdatedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	target := historyrecycle.Target{Kind: "run", ID: run.Meta().ID}
	if _, err := fixture.repository.RecycleHistory(t.Context(), target, repositoryEpoch); !errors.Is(err, historyrecycle.ErrActive) {
		t.Fatalf("unsettled job removed: %v", err)
	}
	if err := fixture.repository.MarkJobSettled(t.Context(), fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	entry, err := fixture.repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Hour))
	if err != nil || entry.Kind != "job" || entry.ID != fixture.spec.JobID || len(entry.RunIDs) != 1 || entry.Remote {
		t.Fatalf("canonical job: %+v %v", entry, err)
	}
	page, err := view.Jobs(t.Context(), "", 25)
	if err != nil || len(page.Jobs) != 0 || page.HasMore {
		t.Fatalf("desktop job stayed visible: %+v %v", page, err)
	}
	raw, err := fixture.repository.Jobs(t.Context(), "", 25)
	if err != nil || len(raw.Jobs) != 1 {
		t.Fatalf("raw job lost: %+v %v", raw, err)
	}
	stored, err := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if err != nil || !reflect.DeepEqual(stored, terminal) {
		t.Fatalf("immutable execution changed: %v", err)
	}
	if _, err := fixture.repository.GetRun(t.Context(), run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		restored, err := fixture.repository.RestoreHistory(t.Context(), target)
		if err != nil || restored.RemovedAt != "" {
			t.Fatalf("restore: %+v %v", restored, err)
		}
	}
	page, err = view.Jobs(t.Context(), "", 25)
	if err != nil || len(page.Jobs) != 1 {
		t.Fatalf("job not restored: %+v %v", page, err)
	}
}

func TestHistoryRecycleComparisonChildHidesWholeLegacyGroup(t *testing.T) {
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
	groupNext, err := group.Transition(domain.ComparisonCancelled, repositoryEpoch.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateComparison(t.Context(), group.Meta().Revision, groupNext); err != nil {
		t.Fatal(err)
	}
	entry, err := repository.RecycleHistory(t.Context(), historyrecycle.Target{Kind: "run", ID: group.Runs()[0].RunID}, repositoryEpoch.Add(2*time.Hour))
	if err != nil || entry.Kind != "comparison" || entry.ID != group.Meta().ID || len(entry.RunIDs) != 2 {
		t.Fatalf("group: %+v %v", entry, err)
	}
	view, err := repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	page, err := view.ComparisonHistory(t.Context(), "", []string{group.Meta().ID})
	if err != nil || len(page.Comparisons) != 0 {
		t.Fatalf("hidden group refreshed: %+v %v", page, err)
	}
	if _, err := repository.GetComparison(t.Context(), group.Meta().ID); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryRecycleRemoteResyncKeepsLocalRemovalAndSharedResult(t *testing.T) {
	fixture := newSubmittedReadFixture(t, true)
	fixture.confirm(t)
	run, err := fixture.executor.GetRun(t.Context(), fixture.spec.Spec.Run.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	transitionRun(t, fixture.executor, run, domain.RunCancelled)
	next, err := fixture.executor.ChangeJob(t.Context(), jobhost.Change{JobID: fixture.spec.JobID,
		ExpectedRevision: fixture.snapshot.Receipt.Revision, Status: domain.RunCancelled, At: fixture.snapshot.UpdatedAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	fixture.snapshot = next
	applyRemoteEvents(t, fixture.repository, remoteEvents(t, fixture.executor, fixture.spec))
	target := historyrecycle.Target{Kind: "job", ID: fixture.spec.JobID}
	entry, err := fixture.repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Hour))
	if err != nil || !entry.Remote {
		t.Fatalf("remote: %+v %v", entry, err)
	}
	applyRemoteEvents(t, fixture.repository, remoteEvents(t, fixture.executor, fixture.spec))
	view, err := fixture.repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	page, err := view.Jobs(t.Context(), "", 25)
	if err != nil || len(page.Jobs) != 0 {
		t.Fatalf("sync resurrected locally hidden remote: %+v %v", page, err)
	}
	shared, err := fixture.executor.GetJob(t.Context(), fixture.spec.JobID)
	if err != nil || !reflect.DeepEqual(shared, fixture.snapshot) {
		t.Fatalf("remote shared execution changed: %v", err)
	}
	if _, err := fixture.repository.RestoreHistory(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	page, err = view.Jobs(t.Context(), "", 25)
	if err != nil || len(page.Jobs) != 1 {
		t.Fatalf("remote restore: %+v %v", page, err)
	}
}

func TestHistoryRecycleConcurrentIdempotenceAndKeysetPagination(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	const reportID = "79000000-0000-4000-8000-000000000001"
	if err := repository.SaveQuickPerformanceReport(t.Context(), validQuickPerformanceReport(reportID, repositoryEpoch.Format(time.RFC3339Nano))); err != nil {
		t.Fatal(err)
	}
	target := historyrecycle.Target{Kind: "report", ID: reportID}
	var wg sync.WaitGroup
	for index := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Duration(index)*time.Nanosecond)); err != nil {
				t.Errorf("concurrent recycle: %v", err)
			}
		}()
	}
	wg.Wait()
	for index := 2; index <= 28; index++ {
		id := fmt.Sprintf("79000000-0000-4000-8000-%012d", index)
		if err := repository.SaveQuickPerformanceReport(t.Context(), validQuickPerformanceReport(id, repositoryEpoch.Format(time.RFC3339Nano))); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.RecycleHistory(t.Context(), historyrecycle.Target{Kind: "report", ID: id}, repositoryEpoch.Add(time.Duration(index)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repository.ListRecycledHistory(t.Context(), "")
	if err != nil || len(page.Entries) != 25 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first: %+v %v", page, err)
	}
	last, err := repository.ListRecycledHistory(t.Context(), page.NextCursor)
	if err != nil || len(last.Entries) != 3 || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last: %+v %v", last, err)
	}
	if _, err := repository.ListRecycledHistory(t.Context(), "invalid"); !errors.Is(err, historyrecycle.ErrInvalid) {
		t.Fatalf("cursor: %v", err)
	}
}

func TestHistoryRecyclePersistsAcrossReopenAndRestoresOriginalArchive(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	const id = "79000000-0000-4000-8000-000000000001"
	report := validQuickPerformanceReport(id, repositoryEpoch.Format(time.RFC3339Nano))
	if err := repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	target := historyrecycle.Target{Kind: "report", ID: id}
	entry, err := repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	page, err := repository.ListRecycledHistory(t.Context(), "")
	if err != nil || len(page.Entries) != 1 || !reflect.DeepEqual(page.Entries[0], entry) {
		t.Fatalf("reopen lost removal: %+v %v", page, err)
	}
	view, err := repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	summaries, err := view.ListRecentQuickPerformanceSummaries(t.Context(), 25)
	if err != nil || len(summaries) != 0 {
		t.Fatalf("reopen archive still visible: %v %v", summaries, err)
	}
	if _, err := repository.RestoreHistory(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	stored, err := view.GetQuickPerformanceReport(t.Context(), id)
	if err != nil || !reflect.DeepEqual(stored, report) {
		t.Fatalf("restore changed original archive: %v", err)
	}
	if _, err := view.GetRun(t.Context(), id); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("performance archive invented a run: %v", err)
	}
}
