package quicktest

import (
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

// ValidateArchivedPerformanceReport protects every persistence and reporting
// adapter from malformed or unsafe quick-report documents.
func ValidateArchivedPerformanceReport(report PerformanceReport) (time.Time, error) {
	if (report.SchemaVersion != LegacyPerformanceSchemaVersion && report.SchemaVersion != PerformanceSchemaVersion) || !domain.IsUUID(report.ReportID) {
		return time.Time{}, errors.New("quick performance report identity is invalid")
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, report.GeneratedAt)
	if err != nil || generatedAt.IsZero() || generatedAt.Location() != time.UTC || generatedAt.Format(time.RFC3339Nano) != report.GeneratedAt {
		return time.Time{}, errors.New("quick performance report timestamp is invalid")
	}
	if !report.Archived || report.ArchiveStatus != PerformanceArchiveArchived {
		return time.Time{}, errors.New("quick performance report is not archived")
	}
	if strings.TrimSpace(report.ModelID) == "" || report.ModelID != strings.TrimSpace(report.ModelID) || strings.TrimSpace(report.BaseURL) == "" {
		return time.Time{}, errors.New("quick performance report connection summary is invalid")
	}
	if !validArchivedBaseURL(report.BaseURL) || strings.TrimRight(report.BaseURL, "/")+"/chat/completions" != report.Endpoint {
		return time.Time{}, errors.New("quick performance report address is invalid")
	}
	if len(report.Samples) > int(MaxPerformanceRequests) {
		return time.Time{}, errors.New("quick performance report exceeds the sample limit")
	}
	arrival := normalizedArrivalPattern(report.Profile.ArrivalPattern)
	workloadMode := normalizedWorkloadMode(report.Profile.WorkloadMode)
	if report.SchemaVersion == PerformanceSchemaVersion {
		if !validArchivedPerformanceProfile(report.Profile) || report.Progress.Offered != report.Progress.Launched+report.Progress.Rejected ||
			report.Progress.Completed != report.Progress.Offered {
			return time.Time{}, errors.New("quick performance report load profile is invalid")
		}
		if !finiteNonNegative(report.Progress.SendDurationMS) || !finiteNonNegative(report.Progress.DrainDurationMS) ||
			!approximatelyEqual(report.Progress.TotalDurationMS, report.Progress.SendDurationMS+report.Progress.DrainDurationMS) {
			return time.Time{}, errors.New("quick performance report timing windows are invalid")
		}
		if !finiteNonNegative(report.Metrics.OfferedQPS) || !finiteNonNegative(report.Metrics.LaunchedQPS) ||
			!finiteNonNegative(report.Metrics.CompletedQPS) || !finiteNonNegative(report.Metrics.SuccessfulRequestQPS) ||
			report.Progress.TotalDurationMS <= 0 || report.Metrics.CompletedQPS <= 0 || report.Metrics.LaunchedQPS > report.Metrics.OfferedQPS ||
			report.Metrics.SuccessfulRequestQPS > report.Metrics.CompletedQPS {
			return time.Time{}, errors.New("quick performance report throughput is invalid")
		}
		seconds := report.Progress.TotalDurationMS / 1_000
		if !approximatelyEqual(report.Metrics.CompletedQPS, float64(report.Progress.Launched)/seconds) ||
			!approximatelyEqual(report.Metrics.SuccessfulRequestQPS, float64(report.Progress.Succeeded)/seconds) ||
			!approximatelyEqual(report.Metrics.RequestQPS, float64(report.Progress.Completed)/seconds) ||
			!approximatelyEqual(report.Metrics.RPM, report.Metrics.RequestQPS*60) {
			return time.Time{}, errors.New("quick performance report throughput is inconsistent")
		}
		if report.Progress.SendDurationMS == 0 {
			if report.Profile.LoadMode != domain.LoadFixedConcurrency || report.Metrics.OfferedQPS != 0 || report.Metrics.LaunchedQPS != 0 {
				return time.Time{}, errors.New("quick performance report zero send window is inconsistent")
			}
		} else if report.Profile.LoadMode == domain.LoadFixedConcurrency {
			sendSeconds := report.Progress.SendDurationMS / 1_000
			if !approximatelyEqual(report.Metrics.OfferedQPS, float64(report.Progress.Offered)/sendSeconds) ||
				!approximatelyEqual(report.Metrics.LaunchedQPS, report.Metrics.OfferedQPS) {
				return time.Time{}, errors.New("quick performance report fixed-concurrency throughput is inconsistent")
			}
		} else {
			rateWindowSeconds := report.Progress.SendDurationMS / 1_000
			countLimitReached := !report.Progress.Stopped && report.Profile.RequestCount > 0 && report.Progress.Offered >= report.Profile.RequestCount
			useNominalCountWindow := arrival != load.ArrivalPoisson || report.Progress.Offered == 1
			if countLimitReached && useNominalCountWindow {
				minimumScheduleWindow := float64(report.Progress.Offered) / report.Profile.RatePerSecond
				if rateWindowSeconds < minimumScheduleWindow {
					rateWindowSeconds = minimumScheduleWindow
				}
			}
			if !approximatelyEqual(report.Metrics.OfferedQPS, float64(report.Progress.Offered)/rateWindowSeconds) ||
				!approximatelyEqual(report.Metrics.LaunchedQPS, float64(report.Progress.Launched)/rateWindowSeconds) {
				return time.Time{}, errors.New("quick performance report open-loop throughput is inconsistent")
			}
		}
		if report.Progress.SendDurationMS > 0 &&
			!approximatelyEqual(report.Metrics.LaunchedQPS/report.Metrics.OfferedQPS, float64(report.Progress.Launched)/float64(report.Progress.Offered)) {
			return time.Time{}, errors.New("quick performance report load rate ratio is inconsistent")
		}
	}
	if report.Progress.Launched == 0 || report.Progress.Completed != uint64(len(report.Samples)) || report.Metrics.Completed != report.Progress.Completed ||
		report.Progress.Succeeded+report.Progress.Failed != report.Progress.Completed || report.Metrics.Succeeded != report.Progress.Succeeded || report.Metrics.Failed != report.Progress.Failed {
		return time.Time{}, errors.New("quick performance report counts are inconsistent")
	}
	if report.Success != (report.Progress.Phase == "completed" && report.Metrics.Completed > 0 && report.Metrics.Failed == 0) {
		return time.Time{}, errors.New("quick performance report success is inconsistent")
	}
	if !finiteNonNegative(report.Progress.TotalDurationMS) || !finiteNonNegative(report.Metrics.SuccessRatePercent) || report.Metrics.SuccessRatePercent > 100 {
		return time.Time{}, errors.New("quick performance report aggregate is invalid")
	}
	if report.ErrorCode != "" && !safePerformanceErrorCode(report.ErrorCode) {
		return time.Time{}, errors.New("quick performance report error code is unsafe")
	}
	seen := make(map[uint64]struct{}, len(report.Samples))
	var workload *performanceWorkload
	if report.SchemaVersion == PerformanceSchemaVersion && workloadMode == PerformanceWorkloadNormal {
		workload, err = newPerformanceWorkload(report.Profile)
		if err != nil {
			return time.Time{}, errors.New("quick performance report workload is invalid")
		}
	}
	var succeeded, failed, timedOut, promptTokens, completionTokens, cachedTokens uint64
	evidenceBytes := 0
	failureCounts := make(map[domain.ErrorCode]uint64)
	for _, sample := range report.Samples {
		if _, duplicate := seen[sample.RequestIndex]; duplicate {
			return time.Time{}, errors.New("quick performance report sample index is duplicated")
		}
		seen[sample.RequestIndex] = struct{}{}
		if workload == nil {
			if sample.TargetInputTokens != 0 || sample.TargetOutputTokens != 0 {
				return time.Time{}, errors.New("fixed quick performance sample has workload targets")
			}
		} else {
			target := workload.target(sample.RequestIndex)
			if sample.TargetInputTokens != target.InputTokens || sample.TargetOutputTokens != target.OutputTokens {
				return time.Time{}, errors.New("quick performance sample workload target is inconsistent")
			}
		}
		if sample.HTTPStatus < 0 || sample.HTTPStatus > 999 || !finiteNonNegative(sample.ScheduledOffsetMS) || !finiteNonNegative(sample.StartedOffsetMS) ||
			!finiteNonNegative(sample.FinishedOffsetMS) || !finiteNonNegative(sample.ScheduleLagMS) || !finiteNonNegative(sample.E2EMS) ||
			!finiteNonNegative(sample.TTFTMS) || !finiteNonNegative(sample.TPOTMS) || sample.FinishedOffsetMS < sample.StartedOffsetMS {
			return time.Time{}, errors.New("quick performance report sample measurement is invalid")
		}
		if sample.Success {
			if sample.ErrorCode != "" || sample.ResponseEvidence != nil {
				return time.Time{}, errors.New("successful quick performance sample has an error code")
			}
			succeeded++
			promptTokens += sample.PromptTokens
			completionTokens += sample.CompletionTokens
			cachedTokens += sample.CachedTokens
		} else {
			if strings.TrimSpace(string(sample.ErrorCode)) == "" || !safePerformanceErrorCode(sample.ErrorCode) {
				return time.Time{}, errors.New("failed quick performance sample lacks an error code")
			}
			failed++
			failureCounts[sample.ErrorCode]++
			if sample.ResponseEvidence != nil {
				if err := validatePerformanceResponseEvidence(*sample.ResponseEvidence); err != nil {
					return time.Time{}, err
				}
				evidenceBytes += len(sample.ResponseEvidence.Body)
				if evidenceBytes > MaxPerformanceEvidenceTotalBytes {
					return time.Time{}, errors.New("quick performance response evidence exceeds the total limit")
				}
			}
		}
		if sample.TimedOut {
			timedOut++
		}
	}
	if succeeded != report.Metrics.Succeeded || failed != report.Metrics.Failed || timedOut != report.Metrics.TimedOut ||
		promptTokens != report.Metrics.PromptTokens || completionTokens != report.Metrics.CompletionTokens || cachedTokens != report.Metrics.CachedTokens {
		return time.Time{}, errors.New("quick performance report sample totals are inconsistent")
	}
	if !sort.SliceIsSorted(report.Failures, func(left, right int) bool { return report.Failures[left].ErrorCode < report.Failures[right].ErrorCode }) {
		return time.Time{}, errors.New("quick performance report failures are not stable")
	}
	for _, failure := range report.Failures {
		if failure.Count == 0 || !safePerformanceErrorCode(failure.ErrorCode) || failureCounts[failure.ErrorCode] != failure.Count {
			return time.Time{}, errors.New("quick performance report failures are inconsistent")
		}
		delete(failureCounts, failure.ErrorCode)
	}
	if len(failureCounts) != 0 {
		return time.Time{}, errors.New("quick performance report failures are incomplete")
	}
	if report.SchemaVersion == PerformanceSchemaVersion {
		if err := validatePerformancePhaseThree(report); err != nil {
			return time.Time{}, err
		}
	}
	return generatedAt, nil
}

func validatePerformancePhaseThree(report PerformanceReport) error {
	expectedBudget, _, err := buildPerformanceRequestBudget(report.Profile)
	if err != nil {
		return errors.New("quick performance report request budget is invalid")
	}
	configured := phaseThreeConfigured(report.Profile)
	if !configured {
		if report.RequestBudget != nil || report.Warmup != nil || report.Ramp != nil || report.TimeSlices != nil {
			return errors.New("quick performance report has unconfigured phase-three data")
		}
		return nil
	}
	if report.RequestBudget == nil || expectedBudget == nil || *report.RequestBudget != *expectedBudget {
		return errors.New("quick performance report request budget is inconsistent")
	}
	if !validPerformancePhaseThreeSamples(report.Samples, report.Progress.TotalDurationMS) {
		return errors.New("quick performance report phase-three sample timing is inconsistent")
	}
	if report.Progress.Planned == 0 || report.Progress.Planned > expectedBudget.MeasuredCap ||
		report.Progress.Offered > expectedBudget.MeasuredCap || report.Progress.Launched > expectedBudget.MeasuredCap ||
		report.Progress.Completed > expectedBudget.MeasuredCap {
		return errors.New("quick performance report measured traffic exceeds its request budget")
	}
	if report.Progress.Phase == load.PhaseCompleted && !report.Progress.Stopped && report.Profile.DurationMS == 0 &&
		(report.Progress.Planned != report.Profile.RequestCount || report.Progress.Offered != report.Profile.RequestCount) {
		return errors.New("quick performance report measured count is incomplete")
	}
	if report.Profile.RequestCount > 0 {
		if report.Progress.Capped {
			return errors.New("quick performance report count-bounded run is marked capped")
		}
	} else if report.Profile.LoadMode == domain.LoadFixedConcurrency && report.Profile.DurationMS > 0 {
		if report.Progress.Capped != performanceMeasuredBudgetCapped(
			report.Profile,
			expectedBudget.MeasuredCap,
			report.Progress.Offered,
			report.Progress.SendDurationMS,
		) {
			return errors.New("quick performance report measured cap is inconsistent")
		}
	} else if report.Profile.LoadMode == domain.LoadOpenLoop && report.Profile.DurationMS > 0 {
		planned, capped, planErr := load.EstimateOpenLoopDurationSchedule(
			report.Profile.RatePerSecond,
			time.Duration(report.Profile.DurationMS)*time.Millisecond,
			normalizedArrivalPattern(report.Profile.ArrivalPattern),
			report.Profile.RandomSeed,
			expectedBudget.MeasuredCap,
		)
		if planErr != nil || report.Progress.Planned != planned || report.Progress.Capped != capped {
			return errors.New("quick performance report open-loop measured cap is inconsistent")
		}
	}

	if report.Profile.WarmupRequests == 0 {
		if report.Warmup != nil {
			return errors.New("quick performance report has an unconfigured warmup")
		}
	} else {
		if report.Warmup == nil || validatePerformanceTrafficSummary(*report.Warmup, expectedBudget.WarmupCap) != nil ||
			report.Warmup.Stopped || report.Warmup.Capped || report.Warmup.Offered != expectedBudget.WarmupCap ||
			report.Warmup.Launched != expectedBudget.WarmupCap || report.Warmup.Rejected != 0 {
			return errors.New("quick performance report warmup is inconsistent")
		}
	}

	if report.Profile.RampDurationMS == 0 {
		if report.Ramp != nil {
			return errors.New("quick performance report has an unconfigured ramp")
		}
	} else {
		if report.Ramp == nil || report.Ramp.Shape != "linear_staircase" || report.Ramp.DurationMS != report.Profile.RampDurationMS ||
			validatePerformanceTrafficSummary(report.Ramp.Traffic, expectedBudget.RampCap) != nil || report.Ramp.Traffic.Stopped ||
			report.Ramp.CompletedWindow != !report.Ramp.Traffic.Capped {
			return errors.New("quick performance report ramp is inconsistent")
		}
		rampProfile := performanceRampProfile(report.Profile, expectedBudget.RampCap)
		if report.Ramp.Steps != load.LinearRampStepCount(rampProfile) {
			return errors.New("quick performance report ramp steps are inconsistent")
		}
		if report.Profile.LoadMode == domain.LoadFixedConcurrency {
			if report.Ramp.TargetConcurrency != report.Profile.Concurrency || report.Ramp.TargetRatePerSecond != 0 {
				return errors.New("quick performance report fixed ramp target is inconsistent")
			}
			expectedCapped := report.Ramp.Traffic.Offered >= expectedBudget.RampCap
			if report.Ramp.Traffic.Capped != expectedCapped {
				return errors.New("quick performance report fixed ramp cap is inconsistent")
			}
		} else {
			if report.Ramp.TargetConcurrency != 0 || !approximatelyEqual(report.Ramp.TargetRatePerSecond, report.Profile.RatePerSecond) {
				return errors.New("quick performance report open ramp target is inconsistent")
			}
			planned, capped, planErr := load.EstimateOpenLoopRampSchedule(
				report.Profile.RatePerSecond,
				time.Duration(report.Profile.RampDurationMS)*time.Millisecond,
				normalizedArrivalPattern(report.Profile.ArrivalPattern),
				report.Profile.RandomSeed,
				expectedBudget.RampCap,
			)
			if planErr != nil || report.Ramp.Traffic.Offered != planned || report.Ramp.Traffic.Capped != capped {
				return errors.New("quick performance report open ramp cap is inconsistent")
			}
		}
		if report.Ramp.Traffic.Capped && report.Ramp.Traffic.Offered != expectedBudget.RampCap {
			return errors.New("quick performance report ramp cap is inconsistent")
		}
		if report.Ramp.CompletedWindow && report.Ramp.Traffic.SendDurationMS < float64(report.Profile.RampDurationMS) {
			return errors.New("quick performance report ramp window is incomplete")
		}
	}

	if report.Profile.SliceDurationMS == 0 {
		if report.TimeSlices != nil {
			return errors.New("quick performance report has unconfigured time slices")
		}
		return nil
	}
	expectedSlices := buildPerformanceTimeSlicesFromSamples(
		report.Samples,
		report.Progress.TotalDurationMS,
		float64(report.Profile.SliceDurationMS),
	)
	if len(expectedSlices) == 0 || len(report.TimeSlices) != len(expectedSlices) {
		return errors.New("quick performance report time slices are incomplete")
	}
	for index := range expectedSlices {
		if !equalPerformanceTimeSlice(report.TimeSlices[index], expectedSlices[index]) {
			return errors.New("quick performance report time slices are inconsistent")
		}
	}
	if !performanceTimeSliceTotalsMatch(report.TimeSlices, report.Progress, report.Metrics) {
		return errors.New("quick performance report time slice totals are inconsistent")
	}
	return nil
}

func validatePerformanceTrafficSummary(summary PerformanceTrafficSummary, expectedCap uint64) error {
	if expectedCap == 0 || summary.RequestCap != expectedCap || summary.Offered == 0 || summary.Offered > summary.RequestCap ||
		summary.Offered != summary.Launched+summary.Rejected || summary.Completed != summary.Offered ||
		summary.Succeeded+summary.Failed != summary.Completed || summary.TimedOut > summary.Failed || summary.Rejected > summary.Failed ||
		summary.PeakInFlight > summary.Launched || (summary.Launched > 0 && summary.PeakInFlight == 0) ||
		summary.CachedTokens > summary.PromptTokens || summary.Failures == nil ||
		!finiteNonNegative(summary.SendDurationMS) || !finiteNonNegative(summary.DrainDurationMS) || !finiteNonNegative(summary.TotalDurationMS) ||
		summary.SendDurationMS <= 0 || summary.TotalDurationMS <= 0 ||
		!approximatelyEqual(summary.TotalDurationMS, summary.SendDurationMS+summary.DrainDurationMS) {
		return errors.New("quick performance traffic summary is invalid")
	}
	var failed, rejected, timedOut uint64
	var previous domain.ErrorCode
	for index, failure := range summary.Failures {
		if failure.Count == 0 || !safePerformanceErrorCode(failure.ErrorCode) ||
			(index > 0 && failure.ErrorCode <= previous) || failed > math.MaxUint64-failure.Count {
			return errors.New("quick performance traffic failures are invalid")
		}
		failed += failure.Count
		if failure.ErrorCode == load.ErrorSchedulerOverload {
			rejected = failure.Count
		}
		if failure.ErrorCode == load.ErrorTimeout {
			timedOut = failure.Count
		}
		previous = failure.ErrorCode
	}
	if failed != summary.Failed || rejected != summary.Rejected || timedOut != summary.TimedOut {
		return errors.New("quick performance traffic failures are inconsistent")
	}
	return nil
}

func validPerformancePhaseThreeSamples(samples []PerformanceSample, totalDurationMS float64) bool {
	for _, sample := range samples {
		if sample.ScheduledOffsetMS > totalDurationMS || sample.StartedOffsetMS > totalDurationMS || sample.FinishedOffsetMS > totalDurationMS {
			return false
		}
		expectedLag := math.Max(0, sample.StartedOffsetMS-sample.ScheduledOffsetMS)
		elapsed := sample.FinishedOffsetMS - sample.StartedOffsetMS
		coarseClockMinimum := elapsed == 0 && approximatelyEqual(sample.E2EMS, durationMilliseconds(time.Nanosecond))
		if !approximatelyEqual(sample.ScheduleLagMS, expectedLag) ||
			(sample.E2EMS > elapsed && !approximatelyEqual(sample.E2EMS, elapsed) && !coarseClockMinimum) ||
			sample.TTFTMS > sample.E2EMS ||
			!approximatelyEqual(sample.TPOTMS, performanceTPOTMilliseconds(sample.TTFTMS, sample.E2EMS, sample.CompletionTokens)) {
			return false
		}
	}
	return true
}

func equalPerformanceTimeSlice(left, right PerformanceTimeSlice) bool {
	return left.SliceIndex == right.SliceIndex && approximatelyEqual(left.StartMS, right.StartMS) && approximatelyEqual(left.EndMS, right.EndMS) &&
		left.Partial == right.Partial && left.Offered == right.Offered && left.Launched == right.Launched && left.Completed == right.Completed &&
		left.Succeeded == right.Succeeded && left.Failed == right.Failed && left.Rejected == right.Rejected &&
		left.PromptTokens == right.PromptTokens && left.CompletionTokens == right.CompletionTokens && left.CachedTokens == right.CachedTokens &&
		equalPerformanceLatencySlice(left.TTFT, right.TTFT) && equalPerformanceLatencySlice(left.TPOT, right.TPOT) &&
		equalPerformanceLatencySlice(left.E2E, right.E2E)
}

func performanceTimeSliceTotalsMatch(slices []PerformanceTimeSlice, progress PerformanceProgress, metrics load.Metrics) bool {
	var offered, launched, completed, succeeded, failed, rejected uint64
	var promptTokens, completionTokens, cachedTokens uint64
	for _, slice := range slices {
		offered += slice.Offered
		launched += slice.Launched
		completed += slice.Completed
		succeeded += slice.Succeeded
		failed += slice.Failed
		rejected += slice.Rejected
		promptTokens += slice.PromptTokens
		completionTokens += slice.CompletionTokens
		cachedTokens += slice.CachedTokens
	}
	return offered == progress.Offered && launched == progress.Launched && completed == progress.Completed &&
		succeeded == progress.Succeeded && failed == progress.Failed && rejected == progress.Rejected &&
		promptTokens == metrics.PromptTokens && completionTokens == metrics.CompletionTokens && cachedTokens == metrics.CachedTokens
}

func equalPerformanceLatencySlice(left, right PerformanceLatencySlice) bool {
	return left.Count == right.Count && approximatelyEqual(left.P50MS, right.P50MS) &&
		approximatelyEqual(left.P95MS, right.P95MS) && approximatelyEqual(left.P99MS, right.P99MS)
}

func validArchivedPerformanceProfile(profile PerformanceProfile) bool {
	return validPerformanceProfileValues(profile)
}

func validatePerformanceResponseEvidence(evidence PerformanceResponseEvidence) error {
	if !evidence.Redacted || len(evidence.ContentType) > 128 || len(evidence.RequestID) > 256 ||
		!utf8.ValidString(evidence.Body) || len(evidence.Body) > MaxPerformanceEvidenceBodyBytes {
		return errors.New("quick performance response evidence is unsafe")
	}
	switch evidence.CaptureStatus {
	case PerformanceEvidenceCaptured:
		if strings.TrimSpace(evidence.Body) == "" {
			return errors.New("captured quick performance response evidence is empty")
		}
	case PerformanceEvidenceEmpty:
		if evidence.Body != "" || evidence.Truncated {
			return errors.New("empty quick performance response evidence is inconsistent")
		}
	case PerformanceEvidenceOmitted:
		if evidence.Body != "" || !evidence.Truncated {
			return errors.New("omitted quick performance response evidence is inconsistent")
		}
	default:
		return errors.New("quick performance response evidence status is invalid")
	}
	return nil
}

// ValidatePerformanceArchiveSummary protects the list boundary without
// loading or decoding the potentially large request-sample document.
func ValidatePerformanceArchiveSummary(summary PerformanceArchiveSummary) (time.Time, error) {
	if !domain.IsUUID(summary.ReportID) {
		return time.Time{}, errors.New("quick performance summary identity is invalid")
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, summary.GeneratedAt)
	if err != nil || generatedAt.IsZero() || generatedAt.Location() != time.UTC || generatedAt.Format(time.RFC3339Nano) != summary.GeneratedAt {
		return time.Time{}, errors.New("quick performance summary timestamp is invalid")
	}
	if strings.TrimSpace(summary.ModelID) == "" || summary.ModelID != strings.TrimSpace(summary.ModelID) || !validArchivedBaseURL(summary.BaseURL) {
		return time.Time{}, errors.New("quick performance summary connection is invalid")
	}
	if summary.Completed == 0 || summary.Completed > MaxPerformanceRequests || summary.Failed > summary.Completed {
		return time.Time{}, errors.New("quick performance summary counts are invalid")
	}
	if summary.Phase != load.PhaseCompleted && summary.Phase != load.PhaseCancelled {
		return time.Time{}, errors.New("quick performance summary phase is invalid")
	}
	if summary.Success != (summary.Phase == load.PhaseCompleted && summary.Failed == 0) {
		return time.Time{}, errors.New("quick performance summary success is inconsistent")
	}
	return generatedAt, nil
}

func validArchivedBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == "" && strings.TrimRight(value, "/") == value
}

func safePerformanceErrorCode(code domain.ErrorCode) bool {
	switch code {
	case ErrorInvalidRequest, ErrorInsecureEndpoint, ErrorCredentialRequired, ErrorAuthenticationFailed,
		load.ErrorNetwork, load.ErrorTimeout, load.ErrorCancelled, load.ErrorHTTP, load.ErrorRateLimited,
		load.ErrorProtocol, load.ErrorIncompleteStream, load.ErrorSemanticEmpty, load.ErrorResponseTooLarge,
		load.ErrorClientClosed, load.ErrorSchedulerOverload, load.ErrorExecutorPanic, load.ErrorRequestFailed,
		load.ErrorUnclassified:
		return true
	default:
		return false
	}
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func approximatelyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= scale*1e-9
}
