package domain

import (
	"testing"
	"time"
)

func TestComparisonRequiresOneModelTwoDistinctChannelsAndRunPerChannel(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	comparison, err := NewComparison(
		EntityMeta{ID: "50000000-0000-4000-8000-000000000001", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		EntityRevisionRef{ID: "50000000-0000-4000-8000-000000000002", Revision: 3},
		EntityRevisionRef{ID: "50000000-0000-4000-8000-000000000003", Revision: 2},
		[]ComparisonRunRef{
			{Channel: EntityRevisionRef{ID: "50000000-0000-4000-8000-000000000004", Revision: 1}, RunID: "50000000-0000-4000-8000-000000000006"},
			{Channel: EntityRevisionRef{ID: "50000000-0000-4000-8000-000000000005", Revision: 4}, RunID: "50000000-0000-4000-8000-000000000007"},
		},
	)
	if err != nil {
		t.Fatalf("NewComparison() error = %v", err)
	}
	if comparison.Status() != ComparisonRunning {
		t.Fatalf("status = %q", comparison.Status())
	}
	completed, err := comparison.Transition(ComparisonCompleted, now.Add(time.Second))
	if err != nil || completed.Status() != ComparisonCompleted || completed.Meta().Revision != 2 {
		t.Fatalf("Transition() = %#v, %v", completed, err)
	}

	if _, err := NewComparison(comparison.Meta(), comparison.Plan(), comparison.Model(), comparison.Runs()[:1]); err == nil {
		t.Fatal("comparison accepted fewer than two channels")
	}
	duplicate := comparison.Runs()
	duplicate[1].Channel = duplicate[0].Channel
	if _, err := NewComparison(comparison.Meta(), comparison.Plan(), comparison.Model(), duplicate); err == nil {
		t.Fatal("comparison accepted duplicate channels")
	}
}
