package workspace

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCaseProgressPreservesPartialCountOnCancellation(t *testing.T) {
	now := time.Now().UTC()
	_, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30000,
	})
	projection := RunProjection{Run: run, EntryResults: []EntryResultProjection{{EntryID: entryID}}}
	progress := summarizeEntryProgress(projection)
	if len(progress) != 1 || progress[0].Status != "running" || progress[0].CaseCount != 1 || progress[0].ObservedCaseCount != 0 {
		t.Fatalf("running progress = %#v", progress)
	}
	cancelled, err := run.Transition(domain.RunCancelled, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	projection.Run = cancelled
	progress = summarizeEntryProgress(projection)
	if progress[0].Status != "cancelled" || progress[0].ObservedCaseCount != 0 {
		t.Fatalf("cancellation must not fill progress: %#v", progress)
	}
	projection.EntryResults[0].ObservedCases = 2
	if validateEntryResults(projection) == nil {
		t.Fatal("accepted more observed Cases than pinned Cases")
	}
}

func TestRequestBudgetCountsCaseExecutionsAndLoadEntries(t *testing.T) {
	snapshot := domain.RunSnapshot{Entries: []domain.RunEntrySnapshot{{
		Load: domain.LoadProfile{Mode: domain.LoadSingle, RequestCount: 1}, Cases: make([]domain.CaseRevisionRef, 3),
	}, {Load: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, RequestCount: 10}}}}
	if got := snapshotRequestBudget(snapshot); got != 13 {
		t.Fatalf("budget=%d want13", got)
	}
	snapshot.Entries[1].Load.RequestCount = 0
	if got := snapshotRequestBudget(snapshot); got != 0 {
		t.Fatalf("duration budget=%d", got)
	}
}
