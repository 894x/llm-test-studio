package comparisons_test

import (
	"context"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/domain"
)

// This node-side port exposes no authored catalog, credential or start method.
type comparisonFacts struct {
	comparison domain.Comparison
	runs       map[string]domain.Run
	writes     int
}

func (facts *comparisonFacts) GetComparison(context.Context, string) (domain.Comparison, error) {
	return facts.comparison, nil
}

func (facts *comparisonFacts) UpdateComparison(_ context.Context, _ uint64, comparison domain.Comparison) error {
	facts.comparison = comparison
	facts.writes++
	return nil
}

func (facts *comparisonFacts) GetRun(_ context.Context, id string) (domain.Run, error) {
	return facts.runs[id], nil
}

func TestRefresherAdvancesCompleteGroupWithoutDesktopOrCatalogs(t *testing.T) {
	for _, test := range []struct {
		name     string
		children []domain.RunStatus
		want     domain.ComparisonStatus
	}{
		{name: "all complete", children: []domain.RunStatus{domain.RunCompleted, domain.RunCompleted},
			want: domain.ComparisonCompleted},
		{name: "failure wins", children: []domain.RunStatus{domain.RunCancelled, domain.RunFailed},
			want: domain.ComparisonFailed},
		{name: "cancelled", children: []domain.RunStatus{domain.RunCompleted, domain.RunCancelled},
			want: domain.ComparisonCancelled},
		{name: "active group", children: []domain.RunStatus{domain.RunFailed, domain.RunQueued},
			want: domain.ComparisonRunning},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newComparisonFixture(t)
			refs := make([]domain.ComparisonRunRef, 0, len(fixture.completedRuns))
			facts := &comparisonFacts{runs: map[string]domain.Run{}}
			for index, completed := range fixture.completedRuns {
				meta := completed.Meta()
				meta.Revision, meta.UpdatedAt = 1, meta.CreatedAt
				run, err := domain.NewRun(meta, completed.PlanID(), completed.Snapshot())
				if err != nil {
					t.Fatal(err)
				}
				switch test.children[index] {
				case domain.RunCompleted:
					run = completed
				case domain.RunFailed:
					run, err = run.Fail(domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}, fixture.now)
				case domain.RunCancelled:
					run, err = run.Transition(domain.RunCancelled, fixture.now)
				}
				if err != nil {
					t.Fatal(err)
				}
				facts.runs[run.Meta().ID] = run
				refs = append(refs, domain.ComparisonRunRef{
					Channel: run.Snapshot().Channel.EntityRevisionRef, RunID: run.Meta().ID,
				})
			}
			meta, err := domain.NewEntityMeta(fixture.now)
			if err != nil {
				t.Fatal(err)
			}
			first := fixture.completedRuns[0].Snapshot()
			facts.comparison, err = domain.NewComparison(
				meta,
				first.Plan,
				first.Model.EntityRevisionRef,
				refs,
			)
			if err != nil {
				t.Fatal(err)
			}
			refresher, err := comparisons.NewRefresher(comparisons.RefresherDependencies{
				Repository: facts, Runs: facts, Clock: &comparisonClock{now: fixture.now.Add(time.Hour)},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				comparison, err := refresher.Refresh(t.Context(), meta.ID)
				if err != nil || comparison.Status() != test.want {
					t.Fatalf("group state = %s, %v", comparison.Status(), err)
				}
			}
			writes := 1
			if test.want == domain.ComparisonRunning {
				writes = 0
			}
			if facts.writes != writes {
				t.Fatalf("terminal transition count = %d, want %d", facts.writes, writes)
			}
		})
	}
}
