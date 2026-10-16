package quicktest

import "reflect"

// ValidatePerformanceCheckpointTransition rejects out-of-order observations and
// rewrites of facts already recorded at an earlier execution boundary.
func ValidatePerformanceCheckpointTransition(previous *PerformanceCheckpoint, next PerformanceCheckpoint) error {
	if next.Validate() != nil {
		return ErrPerformanceCheckpoint
	}
	if previous == nil {
		initial := next.Stage == PerformanceStagePreparing && next.Boundary == PerformanceBoundaryEntered &&
			next.Report.ErrorCode == ""
		if !initial {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	if previous.Validate() != nil || previous.Boundary == PerformanceBoundaryReturned {
		return ErrPerformanceCheckpoint
	}
	before, after := previous.Report, next.Report
	if !reflect.DeepEqual(before.ExecutionOrigin, after.ExecutionOrigin) {
		return ErrPerformanceCheckpoint
	}
	if !reflect.DeepEqual(preparedPerformance(before, before.ReportID), preparedPerformance(after, after.ReportID)) {
		return ErrPerformanceCheckpoint
	}
	warmupChanged := before.Warmup != nil && !reflect.DeepEqual(before.Warmup, after.Warmup)
	rampChanged := before.Ramp != nil && !reflect.DeepEqual(before.Ramp, after.Ramp)
	budgetChanged := before.RequestBudget != nil && !reflect.DeepEqual(before.RequestBudget, after.RequestBudget)
	if warmupChanged || rampChanged || budgetChanged {
		return ErrPerformanceCheckpoint
	}
	if before.CapacityResult != nil {
		if after.CapacityResult == nil || len(after.CapacityResult.Rungs) < len(before.CapacityResult.Rungs) {
			return ErrPerformanceCheckpoint
		}
		for index, rung := range before.CapacityResult.Rungs {
			if !reflect.DeepEqual(rung, after.CapacityResult.Rungs[index]) {
				return ErrPerformanceCheckpoint
			}
		}
	}
	samePhase := previous.Stage == next.Stage && reflect.DeepEqual(previous.RungIndex, next.RungIndex)
	if samePhase && previous.Boundary == PerformanceBoundaryEntered {
		if next.Boundary == PerformanceBoundaryCompleted {
			return nil
		}
		preparationFailure := next.Stage == PerformanceStagePreparing && next.Boundary == PerformanceBoundaryReturned &&
			next.Report.ErrorCode != ""
		if preparationFailure {
			return nil
		}
		return ErrPerformanceCheckpoint
	}
	if previous.Boundary != PerformanceBoundaryCompleted {
		return ErrPerformanceCheckpoint
	}
	if samePhase && next.Boundary == PerformanceBoundaryReturned {
		measured := next.Stage == PerformanceStageMeasured || next.Stage == PerformanceStageCapacity
		if measured && !samePerformanceMeasurements(before, after) {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	if next.Boundary != PerformanceBoundaryEntered || before.ErrorCode != "" || before.Progress.Stopped {
		return ErrPerformanceCheckpoint
	}
	if previous.Stage == PerformanceStageCapacity {
		if next.Stage != PerformanceStageCapacity || previous.RungIndex == nil || next.RungIndex == nil {
			return ErrPerformanceCheckpoint
		}
		capacity := before.CapacityResult
		last := capacity.Rungs[len(capacity.Rungs)-1]
		if *next.RungIndex != *previous.RungIndex+1 || last.SLOAssessment.Status != PerformanceSLOPassed {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	want := PerformanceStageMeasured
	if before.Profile.CapacityEnabled {
		want = PerformanceStageCapacity
	}
	if previous.Stage == PerformanceStagePreparing {
		switch {
		case before.Profile.WarmupRequests > 0:
			want = PerformanceStageWarmup
		case before.Profile.RampDurationMS > 0:
			want = PerformanceStageRamp
		}
	}
	if previous.Stage == PerformanceStageWarmup && before.Profile.RampDurationMS > 0 {
		want = PerformanceStageRamp
	}
	if next.Stage != want || previous.Stage == PerformanceStageMeasured {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func samePerformanceMeasurements(before, after PerformanceReport) bool {
	return before.Progress == after.Progress && before.Metrics == after.Metrics && before.Success == after.Success &&
		reflect.DeepEqual(before.Samples, after.Samples) && reflect.DeepEqual(before.Failures, after.Failures) &&
		reflect.DeepEqual(before.TimeSlices, after.TimeSlices) && reflect.DeepEqual(before.SLOAssessment, after.SLOAssessment)
}
