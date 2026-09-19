package quicktest

import "github.com/894x/llm-test-studio/internal/execution/load"

// RecomputePerformanceDerivedFields rebuilds current-contract metrics and time
// slices from samples. Callers must still ValidateArchivedPerformanceReport.
func RecomputePerformanceDerivedFields(report *PerformanceReport) {
	if report.Profile.ArrivalPattern == "" {
		report.Profile.ArrivalPattern = load.ArrivalConstant
	}
	if report.Profile.WorkloadMode == "" {
		report.Profile.WorkloadMode = PerformanceWorkloadFixed
	}
	arrival := normalizedArrivalPattern(report.Profile.ArrivalPattern)
	report.Metrics = rebuildPerformanceMetrics(
		report.Samples,
		report.Progress,
		performanceReportEffectiveProfile(*report),
		arrival,
	)
	if report.Profile.SliceDurationMS > 0 {
		report.TimeSlices = buildPerformanceTimeSlicesFromSamples(
			report.Samples,
			report.Progress.TotalDurationMS,
			float64(report.Profile.SliceDurationMS),
		)
	}
}
