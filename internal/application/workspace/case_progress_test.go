package workspace

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCaseProgressPreservesPartialCountOnCancellation(t *testing.T) {
	now := time.Now().UTC()
	_, run := validPlanAndRun(t, now, domain.LoadProfile{
		Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30000,
	})
	projection := RunProjection{Run: run, SuiteResults: []SuiteResultProjection{{EntryID: entryID}}}
	progress := summarizeSuiteProgress(projection)
	if len(progress) != 1 || progress[0].Status != "running" || progress[0].CaseCount != 1 || progress[0].ObservedCaseCount != 0 {
		t.Fatalf("running progress = %#v", progress)
	}
	cancelled, err := run.Transition(domain.RunCancelled, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	projection.Run = cancelled
	progress = summarizeSuiteProgress(projection)
	if progress[0].Status != "cancelled" || progress[0].ObservedCaseCount != 0 {
		t.Fatalf("cancellation must not fill progress: %#v", progress)
	}
	projection.SuiteResults[0].ObservedCases = 2
	if validateSuiteResults(projection) == nil {
		t.Fatal("accepted more observed Cases than pinned Cases")
	}
}

func TestRequestBudgetCountsLegacyCasesAndDriverGroups(t *testing.T) {
	definition := func(kind domain.CaseType) domain.TestCase {
		return domain.TestCase{Definition: domain.TestCaseDefinition{Type: kind}}
	}
	snapshot := domain.RunSnapshot{Suites: []domain.RunSuiteSnapshot{{
		Load: domain.LoadProfile{RequestCount: 1},
		CaseDefinitions: []domain.TestCase{
			definition(casetypes.TypeLegacyAPIAudit), definition(casetypes.TypeLegacyAPIAudit),
			definition(casetypes.TypeLegacyAPIAudit),
		},
	}, {
		Load: domain.LoadProfile{RequestCount: 10},
		CaseDefinitions: []domain.TestCase{
			definition(casetypes.TypeRequestSingle), definition(casetypes.TypeRequestSingle),
			definition(casetypes.TypeResponseProbe),
		},
	}}}
	if got := snapshotRequestBudget(snapshot); got != 23 {
		t.Fatalf("budget = %d, want 3 legacy Cases + 2 driver groups of 10", got)
	}
	snapshot.Suites[1].Load.RequestCount = 0
	if got := snapshotRequestBudget(snapshot); got != 0 {
		t.Fatalf("duration-based budget = %d, want unknown", got)
	}
}
