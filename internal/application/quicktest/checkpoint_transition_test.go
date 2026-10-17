package quicktest

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
)

func TestPerformanceCheckpointTransitionsFollowActualCoreAndRejectRewrites(t *testing.T) {
	prepared, lease, code := New(Dependencies{}).BuildPerformance(t.Context(), checkpointTestCommand())
	if code != "" {
		t.Fatal(code)
	}
	checkpoints := []PerformanceCheckpoint{}
	var requests atomic.Int64
	_, err := checkpointTestHost(&requests).ExecutePrepared(
		t.Context(),
		prepared,
		lease,
		ExecutionOptions{
			OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
				checkpoints = append(checkpoints, checkpoint)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, next := range checkpoints {
		var previous *PerformanceCheckpoint
		if index > 0 {
			previous = &checkpoints[index-1]
		}
		if err := ValidatePerformanceCheckpointTransition(previous, next); err != nil {
			t.Fatalf("actual Core transition %d rejected: %v", index, err)
		}
	}
	if ValidatePerformanceCheckpointTransition(nil, checkpoints[1]) == nil {
		t.Fatal("first checkpoint skipped phase entry")
	}
	if ValidatePerformanceCheckpointTransition(&checkpoints[0], checkpoints[3]) == nil {
		t.Fatal("checkpoint skipped a phase boundary")
	}
	if ValidatePerformanceCheckpointTransition(&checkpoints[8], checkpoints[8]) == nil {
		t.Fatal("a returned observation advanced again")
	}
	var rewrite PerformanceCheckpoint
	document, err := json.Marshal(checkpoints[8])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(document, &rewrite); err != nil {
		t.Fatal(err)
	}
	// Both standalone measurements remain internally valid. A completed set of
	// observations cannot change its per-request identity at the return boundary.
	last := len(rewrite.Report.Samples) - 1
	rewrite.Report.Samples[last].RequestIndex += 100
	if rewrite.Validate() != nil {
		t.Fatal("rewrite fixture is not a standalone valid observation")
	}
	if ValidatePerformanceCheckpointTransition(&checkpoints[7], rewrite) == nil {
		t.Fatal("return boundary rewrote a completed measurement")
	}
}

func TestCapacityCheckpointTransitionsPreserveRungsAndFinalCancellation(t *testing.T) {
	for _, cancelAfter := range []int{-1, 0, 2} {
		command := hostTestCommand("https://example.test")
		command.Concurrency = 3
		command.CapacityEnabled = true
		command.CapacityStart, command.CapacityStep = 1, 1
		command.SLOE2EMS, command.SLOTargetPercent = 10000, 100
		ctx, cancel := context.WithCancel(t.Context())
		prepared, lease, code := New(Dependencies{}).BuildPerformance(ctx, command)
		if code != "" {
			cancel()
			t.Fatal(code)
		}
		var requests atomic.Int64
		var previous *PerformanceCheckpoint
		report, err := checkpointTestHost(&requests).ExecutePrepared(
			ctx,
			prepared,
			lease,
			ExecutionOptions{
				OnCheckpoint: func(ctx context.Context, checkpoint PerformanceCheckpoint) error {
					if err := ValidatePerformanceCheckpointTransition(previous, checkpoint); err != nil {
						t.Fatalf("actual capacity transition %s rejected: %v", checkpointLabel(checkpoint), err)
					}
					previous = &checkpoint
					if checkpoint.Stage == PerformanceStageCapacity && checkpoint.Boundary == PerformanceBoundaryCompleted {
						if int(*checkpoint.RungIndex) == cancelAfter {
							cancel()
						}
					}
					return nil
				},
			},
		)
		cancel()
		if err != nil || previous.Boundary != PerformanceBoundaryReturned {
			t.Fatalf("capacity final observation was lost after rung %d: %v", cancelAfter, err)
		}
		if cancelAfter >= 0 && report.CapacityResult.Status != PerformanceSLONotEvaluated {
			t.Fatal("cancellation after a completed rung remained evaluated")
		}
	}
}
