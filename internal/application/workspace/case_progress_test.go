package workspace

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCaseProgressPreservesPartialCountOnCancellation(t *testing.T) {
	now := time.Now().UTC()
	plan, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30000,
	})
	projection := projectionFixture(run, plan, RunProjection{EntryResults: []EntryResultProjection{{EntryID: entryID}}})
	progress := summarizeEntryProgress(projection)
	if len(progress) != 1 || progress[0].Status != "running" || progress[0].CaseCount != 1 || progress[0].ObservedCaseCount != 0 {
		t.Fatalf("running progress = %#v", progress)
	}
	cancelled, err := run.Transition(domain.RunCancelled, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	projection.Status = cancelled.Status()
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
	entries := []RunEntryProjection{
		{Load: domain.LoadProfile{Mode: domain.LoadSingle, RequestCount: 1}, CaseCount: 3},
		{Load: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, RequestCount: 10}},
	}
	if got := entryRequestBudget(entries); got != 13 {
		t.Fatalf("budget=%d want13", got)
	}
	entries[1].Load.RequestCount = 0
	if got := entryRequestBudget(entries); got != 0 {
		t.Fatalf("duration budget=%d", got)
	}
}

func TestFailurePolicyStopLeavesLaterItemsNotStarted(t *testing.T) {
	projection := RunProjection{
		Status:       domain.RunFailed,
		Failure:      &domain.RunFailure{Phase: "execute_suites", ErrorCode: "plan_item_failed"},
		Entries:      []RunEntryProjection{{EntryID: "basic"}, {EntryID: "pressure"}},
		EntryResults: []EntryResultProjection{{EntryID: "basic", Status: domain.EntryExecutionCompleted}},
	}
	progress := summarizeEntryProgress(projection)
	if progress[0].Status != "completed" || progress[1].Status != "not_started" {
		t.Fatalf("failure-policy progress = %#v", progress)
	}
}
