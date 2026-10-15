package quicktest

import (
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

// Validate checks current execution observations, including partial preparation
// and capacity phases. Statistical archive validation remains a separate gate.
func (checkpoint PerformanceCheckpoint) Validate() error {
	if err := checkpoint.ValidateEnvelope(); err != nil {
		return err
	}
	report := checkpoint.Report
	if report.Samples == nil || report.Failures == nil || !safeCheckpointError(report.ErrorCode) {
		return ErrPerformanceCheckpoint
	}
	if err := checkpoint.validateArchive(); err != nil {
		return err
	}
	budget, _, err := preparePerformanceProfile(report.Profile)
	if err != nil {
		return ErrPerformanceCheckpoint
	}
	initial := checkpoint.Stage == PerformanceStagePreparing && checkpoint.Boundary == PerformanceBoundaryEntered
	if initial {
		if report.RequestBudget != nil {
			return ErrPerformanceCheckpoint
		}
	} else if !reflect.DeepEqual(report.RequestBudget, budget) {
		return ErrPerformanceCheckpoint
	}
	if err := checkpoint.validatePreparation(); err != nil {
		return err
	}
	if checkpoint.Stage == PerformanceStageCapacity {
		return checkpoint.validateCapacity()
	}
	if report.CapacityResult != nil {
		return ErrPerformanceCheckpoint
	}
	if !report.Profile.CapacityEnabled && validatePerformanceCapacity(report) != nil {
		return ErrPerformanceCheckpoint
	}
	observed := checkpoint.Stage == PerformanceStageMeasured && checkpoint.Boundary != PerformanceBoundaryEntered
	if !observed {
		return checkpoint.validateUnmeasured()
	}
	if err := validateObservedPerformanceMeasurements(report); err != nil {
		return err
	}
	if err := validatePerformancePhaseThree(report, budget); err != nil {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func safeCheckpointError(code domain.ErrorCode) bool {
	return code == "" || safePerformanceErrorCode(code)
}

func (checkpoint PerformanceCheckpoint) validateArchive() error {
	report := checkpoint.Report
	canArchive := checkpoint.Boundary == PerformanceBoundaryReturned &&
		(checkpoint.Stage == PerformanceStageMeasured || checkpoint.Stage == PerformanceStageCapacity)
	switch report.ArchiveStatus {
	case PerformanceArchiveNotAttempted:
		if report.Archived || report.GeneratedAt != "" {
			return ErrPerformanceCheckpoint
		}
	case PerformanceArchiveArchived:
		if !canArchive {
			return ErrPerformanceCheckpoint
		}
		if _, err := ValidateArchivedPerformanceReport(report); err != nil {
			return ErrPerformanceCheckpoint
		}
	case PerformanceArchiveFailed:
		if !canArchive || report.Archived || report.Progress.Launched == 0 {
			return ErrPerformanceCheckpoint
		}
		if report.CapacityResult != nil && report.CapacityResult.Status == PerformanceSLONotEvaluated {
			return ErrPerformanceCheckpoint
		}
		if report.GeneratedAt != "" {
			generated, err := time.Parse(time.RFC3339Nano, report.GeneratedAt)
			valid := err == nil && !generated.IsZero() && generated.Location() == time.UTC
			if !valid || generated.Format(time.RFC3339Nano) != report.GeneratedAt {
				return ErrPerformanceCheckpoint
			}
		}
	default:
		return ErrPerformanceCheckpoint
	}
	return nil
}

func (checkpoint PerformanceCheckpoint) validatePreparation() error {
	report := checkpoint.Report
	warmupObserved := checkpoint.Stage == PerformanceStageWarmup && checkpoint.Boundary != PerformanceBoundaryEntered
	warmupPassed := checkpoint.Stage == PerformanceStageRamp || checkpoint.Stage == PerformanceStageMeasured ||
		checkpoint.Stage == PerformanceStageCapacity
	wantWarmup := report.Profile.WarmupRequests > 0 && (warmupObserved || warmupPassed)
	if (report.Warmup != nil) != wantWarmup {
		return ErrPerformanceCheckpoint
	}
	if report.Warmup != nil {
		if err := validateObservedPerformanceTraffic(*report.Warmup, report.Profile.WarmupRequests); err != nil {
			return err
		}
		if report.Warmup.Rejected != 0 || report.Warmup.Capped {
			return ErrPerformanceCheckpoint
		}
		if warmupPassed && (report.Warmup.Stopped || report.Warmup.Completed != report.Profile.WarmupRequests) {
			return ErrPerformanceCheckpoint
		}
	}
	rampObserved := checkpoint.Stage == PerformanceStageRamp && checkpoint.Boundary != PerformanceBoundaryEntered
	rampPassed := checkpoint.Stage == PerformanceStageMeasured || checkpoint.Stage == PerformanceStageCapacity
	wantRamp := report.Profile.RampDurationMS > 0 && (rampObserved || rampPassed)
	if (report.Ramp != nil) != wantRamp {
		return ErrPerformanceCheckpoint
	}
	if report.Ramp == nil {
		return nil
	}
	ramp := report.Ramp
	budget, _, err := preparePerformanceProfile(report.Profile)
	if err != nil || budget == nil {
		return ErrPerformanceCheckpoint
	}
	profile := performanceRampProfile(report.Profile, budget.RampCap)
	validPlan := ramp.Shape == "linear_staircase" && ramp.DurationMS == report.Profile.RampDurationMS &&
		ramp.Steps == load.LinearRampStepCount(profile)
	if !validPlan || validateObservedPerformanceTraffic(ramp.Traffic, budget.RampCap) != nil {
		return ErrPerformanceCheckpoint
	}
	if report.Profile.LoadMode == domain.LoadFixedConcurrency {
		if ramp.TargetConcurrency != report.Profile.Concurrency || ramp.TargetRatePerSecond != 0 || ramp.Traffic.Rejected != 0 {
			return ErrPerformanceCheckpoint
		}
	} else if ramp.TargetConcurrency != 0 || ramp.TargetRatePerSecond != report.Profile.RatePerSecond {
		return ErrPerformanceCheckpoint
	}
	if ramp.CompletedWindow {
		incomplete := ramp.Traffic.Stopped || ramp.Traffic.Capped ||
			ramp.Traffic.SendDurationMS < float64(ramp.DurationMS)
		if incomplete {
			return ErrPerformanceCheckpoint
		}
	}
	if rampPassed && (ramp.Traffic.Stopped || ramp.CompletedWindow != !ramp.Traffic.Capped) {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func validateObservedPerformanceTraffic(summary PerformanceTrafficSummary, cap uint64) error {
	validCounts := summary.Launched <= summary.Offered && summary.Rejected <= summary.Offered &&
		summary.Succeeded <= summary.Completed && summary.Failed <= summary.Completed
	if !validCounts {
		return ErrPerformanceCheckpoint
	}
	if summary.Offered > 0 {
		if err := validatePerformanceTrafficSummary(summary, cap); err != nil {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	expected := PerformanceTrafficSummary{
		RequestCap: cap, Failures: []PerformanceFailure{}, Stopped: summary.Stopped,
		SendDurationMS: summary.SendDurationMS, DrainDurationMS: summary.DrainDurationMS,
		TotalDurationMS: summary.TotalDurationMS,
	}
	validTimes := finiteNonNegative(summary.SendDurationMS) && finiteNonNegative(summary.DrainDurationMS) &&
		finiteNonNegative(summary.TotalDurationMS) &&
		approximatelyEqual(summary.TotalDurationMS, summary.SendDurationMS+summary.DrainDurationMS)
	if cap == 0 || !validTimes || !summary.Stopped || !reflect.DeepEqual(summary, expected) {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func (checkpoint PerformanceCheckpoint) validateUnmeasured() error {
	report := checkpoint.Report
	noMeasurement := report.Metrics == (load.Metrics{}) && len(report.Samples) == 0 && len(report.Failures) == 0
	if !noMeasurement || report.Success || report.SLOAssessment != nil || report.TimeSlices != nil {
		return ErrPerformanceCheckpoint
	}
	expectedProgress := PerformanceProgress{Phase: PerformancePhaseNotStarted}
	var traffic *PerformanceTrafficSummary
	switch checkpoint.Stage {
	case PerformanceStageWarmup:
		traffic = report.Warmup
	case PerformanceStageRamp:
		if report.Ramp != nil {
			traffic = &report.Ramp.Traffic
		}
	}
	if traffic == nil {
		if report.Progress != expectedProgress {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	if report.ErrorCode != "" {
		expectedProgress = PerformanceProgress{Phase: load.PhaseCancelled, Stopped: true}
	} else if traffic.Stopped || report.Progress.Stopped {
		expectedProgress = PerformanceProgress{
			Phase: load.PhaseCompleted, Stopped: true, Planned: report.Progress.Planned,
			Offered: traffic.Offered, Launched: traffic.Launched, Completed: traffic.Completed,
			Succeeded: traffic.Succeeded, Failed: traffic.Failed, Rejected: traffic.Rejected,
			PeakInFlight: traffic.PeakInFlight, Capped: traffic.Capped,
			SendDurationMS: traffic.SendDurationMS, DrainDurationMS: traffic.DrainDurationMS,
			TotalDurationMS: traffic.TotalDurationMS,
		}
		if report.Progress.Planned == 0 || report.Progress.Planned > traffic.RequestCap {
			return ErrPerformanceCheckpoint
		}
	}
	if report.Progress != expectedProgress {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func validateObservedPerformanceMeasurements(report PerformanceReport) error {
	progress := report.Progress
	validPhase := progress.Phase == load.PhaseCompleted || progress.Phase == load.PhaseCancelled
	_, maximum, err := preparePerformanceProfile(report.Profile)
	if report.Profile.CapacityEnabled {
		maximum = report.Profile.RequestCount
	}
	validCounts := progress.Planned > 0 && progress.Planned <= maximum && progress.Offered <= progress.Planned &&
		progress.Launched <= progress.Offered && progress.Rejected <= progress.Offered &&
		progress.Succeeded <= progress.Completed && progress.Failed <= progress.Completed &&
		progress.PeakInFlight <= progress.Launched && progress.InFlight == 0
	if !validPhase || !validCounts || err != nil {
		return ErrPerformanceCheckpoint
	}
	if report.Profile.RequestCount > 0 && progress.Planned != report.Profile.RequestCount {
		return ErrPerformanceCheckpoint
	}
	if progress.Phase == load.PhaseCancelled && (!progress.Stopped || report.ErrorCode == "") {
		return ErrPerformanceCheckpoint
	}
	if progress.Launched > 0 && progress.PeakInFlight == 0 {
		return ErrPerformanceCheckpoint
	}
	if !validObservedPerformancePeak(performanceReportEffectiveProfile(report), progress) {
		return ErrPerformanceCheckpoint
	}
	if len(report.Samples) > 0 {
		if err := validatePerformanceMeasurements(report); err != nil {
			return ErrPerformanceCheckpoint
		}
		return nil
	}
	if !progress.Stopped {
		return ErrPerformanceCheckpoint
	}
	expected := PerformanceProgress{
		Phase: progress.Phase, Planned: progress.Planned, Stopped: true, Capped: progress.Capped,
		CapacityRungNumber: progress.CapacityRungNumber, CapacityRungCount: progress.CapacityRungCount,
		CapacityTarget: progress.CapacityTarget, SendDurationMS: progress.SendDurationMS,
		DrainDurationMS: progress.DrainDurationMS, TotalDurationMS: progress.TotalDurationMS,
	}
	validTimes := finiteNonNegative(progress.SendDurationMS) && finiteNonNegative(progress.DrainDurationMS) &&
		finiteNonNegative(progress.TotalDurationMS) &&
		approximatelyEqual(progress.TotalDurationMS, progress.SendDurationMS+progress.DrainDurationMS)
	noMeasurement := report.Metrics == (load.Metrics{}) && len(report.Failures) == 0 && !report.Success
	if !validTimes || !noMeasurement || progress != expected || validatePerformanceSLO(report) != nil {
		return ErrPerformanceCheckpoint
	}
	return nil
}

func validObservedPerformancePeak(profile PerformanceProfile, progress PerformanceProgress) bool {
	limit := uint64(profile.Concurrency)
	if profile.LoadMode == domain.LoadOpenLoop {
		limit = uint64(profile.MaxInFlight)
	}
	return progress.PeakInFlight <= limit
}

func (checkpoint PerformanceCheckpoint) validateCapacity() error {
	report := checkpoint.Report
	capacity := report.CapacityResult
	if capacity == nil || capacity.Rungs == nil {
		return ErrPerformanceCheckpoint
	}
	count := uint64(*checkpoint.RungIndex)
	if checkpoint.Boundary != PerformanceBoundaryEntered {
		count++
	}
	if uint64(len(capacity.Rungs)) != count {
		return ErrPerformanceCheckpoint
	}
	if count == 0 {
		valid := capacity.Status == PerformanceSLONotEvaluated && capacity.SelectedRungIndex == nil &&
			capacity.HighestPassingRungIndex == nil
		if !valid {
			return ErrPerformanceCheckpoint
		}
		return checkpoint.validateUnmeasured()
	}
	targets, err := buildPerformanceCapacityTargets(report.Profile)
	if err != nil {
		return ErrPerformanceCheckpoint
	}
	var highest *uint32
	for index, rung := range capacity.Rungs {
		planMatches := rung.Index == uint32(index) && rung.Target == targets[index] &&
			rung.Progress.CapacityRungNumber == uint32(index)+1 &&
			rung.Progress.CapacityRungCount == uint32(len(targets)) && rung.Progress.CapacityTarget == rung.Target
		if !planMatches {
			return ErrPerformanceCheckpoint
		}
		if !validObservedPerformancePeak(performanceCapacityProfile(report.Profile, rung.Target), rung.Progress) {
			return ErrPerformanceCheckpoint
		}
		if rung.SLOAssessment.Status == PerformanceSLONotEvaluated {
			if index != len(capacity.Rungs)-1 || checkpoint.Boundary == PerformanceBoundaryEntered {
				return ErrPerformanceCheckpoint
			}
		} else if validatePerformanceCapacityRung(report.Profile, rung, true) != nil {
			return ErrPerformanceCheckpoint
		}
		if index < len(capacity.Rungs)-1 && rung.SLOAssessment.Status != PerformanceSLOPassed {
			return ErrPerformanceCheckpoint
		}
		if rung.SLOAssessment.Status == PerformanceSLOPassed {
			rungIndex := uint32(index)
			highest = &rungIndex
		}
	}
	last := capacity.Rungs[len(capacity.Rungs)-1]
	if checkpoint.Boundary == PerformanceBoundaryEntered && last.SLOAssessment.Status != PerformanceSLOPassed {
		return ErrPerformanceCheckpoint
	}
	selected := last.Index
	expectedStatus := last.SLOAssessment.Status
	switch last.SLOAssessment.Status {
	case PerformanceSLOPassed:
		if count != uint64(len(targets)) || report.ErrorCode == load.ErrorCancelled || report.ErrorCode == load.ErrorTimeout {
			expectedStatus = PerformanceSLONotEvaluated
		}
	case PerformanceSLOFailed:
		if highest != nil {
			selected = *highest
		}
	case PerformanceSLONotEvaluated:
	default:
		return ErrPerformanceCheckpoint
	}
	if capacity.Status != expectedStatus || capacity.SelectedRungIndex == nil || *capacity.SelectedRungIndex != selected {
		return ErrPerformanceCheckpoint
	}
	if !reflect.DeepEqual(highest, capacity.HighestPassingRungIndex) {
		return ErrPerformanceCheckpoint
	}
	projection := capacity.Rungs[selected]
	matching := report.Success == projection.Success && report.Progress == projection.Progress &&
		report.Metrics == projection.Metrics && reflect.DeepEqual(report.Failures, projection.Failures) &&
		report.SLOAssessment != nil && equalPerformanceSLOAssessment(*report.SLOAssessment, projection.SLOAssessment)
	if !matching || validateObservedPerformanceMeasurements(report) != nil {
		return ErrPerformanceCheckpoint
	}
	budget, _, err := preparePerformanceProfile(report.Profile)
	if err != nil || validatePerformancePhaseThree(report, budget) != nil {
		return ErrPerformanceCheckpoint
	}
	return nil
}
