package workspace

import (
	"context"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestBuiltinPartialPlanSchedulesEveryCaseOnce(t *testing.T) {
	repository, err := OpenCatalog(t.TempDir(), CatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := repository.GetPlan(ctx, "65672e92-21af-4777-8bf4-72517da2746a")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Builtin || len(plan.Entries) != 1 {
		t.Fatalf("expected one-entry built-in Partial plan: %+v", plan)
	}
	entry := plan.Entries[0]
	if entry.TargetKind != domain.PlanTargetSuite || entry.Load.Mode != domain.LoadSingle {
		t.Fatalf("Partial plan must run its Suite in single mode: %+v", entry)
	}
	if entry.Load.Concurrency != 1 || entry.WarmupCount != 0 {
		t.Fatal("Partial plan unexpectedly repeats or warms up requests")
	}
	suites, err := repository.ListSuites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, suite := range suites {
		if suite.ID != entry.TargetID {
			continue
		}
		found = true
		if suite.Key != "kimi.partial" || len(suite.Cases) != 18 {
			t.Fatalf("Partial plan resolves to the wrong Suite: %+v", suite)
		}
		ids := map[string]bool{}
		for _, ref := range suite.Cases {
			if ids[ref.CaseID] {
				t.Fatalf("Partial plan repeats Case %s", ref.CaseID)
			}
			ids[ref.CaseID] = true
		}
		request := runs.ExecutionRequest{
			Entry: domain.RunEntrySnapshot{Load: entry.Load},
			Cases: make([]domain.TestCase, len(suite.Cases)),
		}
		if count := request.LoadProfile().RequestCount; count != 18 {
			t.Fatalf("Partial plan schedules %d requests, want all 18 Cases", count)
		}
	}
	if !found {
		t.Fatal("Partial plan references a missing Suite")
	}
}
