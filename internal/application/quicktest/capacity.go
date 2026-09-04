package quicktest

import (
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const MaxPerformanceCapacityRungs = 20

func validPerformanceCapacityConfiguration(profile PerformanceProfile) bool {
	if !profile.CapacityEnabled {
		return profile.CapacityStart == 0 && profile.CapacityStep == 0
	}
	if !performanceSLOConfigured(profile) || profile.RequestCount == 0 || profile.RampDurationMS != 0 || profile.RampRequestCap != 0 {
		return false
	}
	_, err := buildPerformanceCapacityTargets(profile)
	return err == nil
}

func buildPerformanceCapacityTargets(profile PerformanceProfile) ([]float64, error) {
	if !profile.CapacityEnabled {
		return nil, errors.New("performance capacity ladder is disabled")
	}
	if math.IsNaN(profile.CapacityStart) || math.IsInf(profile.CapacityStart, 0) ||
		math.IsNaN(profile.CapacityStep) || math.IsInf(profile.CapacityStep, 0) {
		return nil, errors.New("performance capacity target is not finite")
	}

	var maximum float64
	switch profile.LoadMode {
	case domain.LoadFixedConcurrency:
		maximum = float64(profile.Concurrency)
		if profile.CapacityStart <= 0 || profile.CapacityStep <= 0 ||
			math.Trunc(profile.CapacityStart) != profile.CapacityStart || math.Trunc(profile.CapacityStep) != profile.CapacityStep {
			return nil, errors.New("fixed-concurrency capacity targets must be positive integers")
		}
	case domain.LoadOpenLoop:
		maximum = profile.RatePerSecond
		if profile.CapacityStart < MinPerformanceRatePerSecond || profile.CapacityStep <= 0 {
			return nil, errors.New("open-loop capacity targets must be positive")
		}
	default:
		return nil, errors.New("performance capacity load mode is invalid")
	}
	if maximum <= 0 || profile.CapacityStart > maximum {
		return nil, errors.New("performance capacity start exceeds its maximum")
	}

	targets := make([]float64, 0, MaxPerformanceCapacityRungs)
	for index := 0; index <= MaxPerformanceCapacityRungs; index++ {
		candidate := profile.CapacityStart + float64(index)*profile.CapacityStep
		if math.IsNaN(candidate) || math.IsInf(candidate, 0) || candidate <= 0 {
			return nil, errors.New("performance capacity target overflow")
		}
		if candidate >= maximum {
			targets = append(targets, maximum)
			break
		}
		if len(targets) > 0 && candidate <= targets[len(targets)-1] {
			return nil, errors.New("performance capacity targets are not increasing")
		}
		targets = append(targets, candidate)
	}
	if len(targets) == 0 || len(targets) > MaxPerformanceCapacityRungs || targets[len(targets)-1] != maximum {
		return nil, errors.New("performance capacity ladder exceeds the rung limit")
	}
	return targets, nil
}

func performanceCapacityProfile(profile PerformanceProfile, target float64) PerformanceProfile {
	result := profile
	result.CapacityEnabled = false
	result.CapacityStart = 0
	result.CapacityStep = 0
	if profile.LoadMode == domain.LoadFixedConcurrency {
		result.Concurrency = uint32(target)
	} else {
		result.RatePerSecond = target
	}
	return result
}

func performanceReportEffectiveProfile(report PerformanceReport) PerformanceProfile {
	if !report.Profile.CapacityEnabled || report.CapacityResult == nil || report.CapacityResult.SelectedRungIndex == nil {
		return report.Profile
	}
	index := *report.CapacityResult.SelectedRungIndex
	if uint64(index) >= uint64(len(report.CapacityResult.Rungs)) {
		return report.Profile
	}
	return performanceCapacityProfile(report.Profile, report.CapacityResult.Rungs[index].Target)
}

func validatePerformanceCapacity(report PerformanceReport) error {
	if !report.Profile.CapacityEnabled {
		if report.CapacityResult != nil || report.Progress.CapacityRungNumber != 0 ||
			report.Progress.CapacityRungCount != 0 || report.Progress.CapacityTarget != 0 {
			return errors.New("quick performance report has unconfigured capacity data")
		}
		return nil
	}
	targets, err := buildPerformanceCapacityTargets(report.Profile)
	if err != nil || report.CapacityResult == nil {
		return errors.New("quick performance report capacity plan is invalid")
	}
	capacity := report.CapacityResult
	if len(capacity.Rungs) == 0 || len(capacity.Rungs) > len(targets) || len(capacity.Rungs) > MaxPerformanceCapacityRungs {
		return errors.New("quick performance report capacity rungs are invalid")
	}

	var highestPassing *uint32
	for index, rung := range capacity.Rungs {
		rungIndex := uint32(index)
		if rung.Index != rungIndex || !approximatelyEqual(rung.Target, targets[index]) ||
			rung.Progress.CapacityRungNumber != rungIndex+1 || rung.Progress.CapacityRungCount != uint32(len(targets)) ||
			!approximatelyEqual(rung.Progress.CapacityTarget, rung.Target) {
			return errors.New("quick performance report capacity rung plan is inconsistent")
		}
		if err := validatePerformanceCapacityRung(report.Profile, rung); err != nil {
			return err
		}
		if index < len(capacity.Rungs)-1 && rung.SLOAssessment.Status != PerformanceSLOPassed {
			return errors.New("quick performance report continues after a failed capacity rung")
		}
		if rung.SLOAssessment.Status == PerformanceSLOPassed {
			copy := rungIndex
			highestPassing = &copy
		}
	}

	last := capacity.Rungs[len(capacity.Rungs)-1]
	var expectedStatus PerformanceSLOStatus
	var expectedSelected uint32
	switch last.SLOAssessment.Status {
	case PerformanceSLOPassed:
		if len(capacity.Rungs) != len(targets) {
			return errors.New("quick performance report capacity plan ends before its maximum")
		}
		expectedStatus = PerformanceSLOPassed
		expectedSelected = last.Index
	case PerformanceSLOFailed:
		expectedStatus = PerformanceSLOFailed
		if highestPassing != nil {
			expectedSelected = *highestPassing
		} else {
			expectedSelected = last.Index
		}
	default:
		return errors.New("archived quick performance capacity result is not evaluated")
	}
	if capacity.Status != expectedStatus || capacity.SelectedRungIndex == nil || *capacity.SelectedRungIndex != expectedSelected {
		return errors.New("quick performance report capacity selection is inconsistent")
	}
	if (highestPassing == nil) != (capacity.HighestPassingRungIndex == nil) ||
		(highestPassing != nil && *highestPassing != *capacity.HighestPassingRungIndex) {
		return errors.New("quick performance report highest passing capacity rung is inconsistent")
	}
	selected := capacity.Rungs[expectedSelected]
	if report.Success != selected.Success || report.Progress != selected.Progress || report.Metrics != selected.Metrics ||
		!reflect.DeepEqual(report.Failures, selected.Failures) || report.SLOAssessment == nil ||
		!equalPerformanceSLOAssessment(*report.SLOAssessment, selected.SLOAssessment) {
		return errors.New("quick performance report capacity projection is inconsistent")
	}
	return nil
}

func validatePerformanceCapacityRung(profile PerformanceProfile, rung PerformanceCapacityRung) error {
	progress := rung.Progress
	metrics := rung.Metrics
	if progress.Phase != load.PhaseCompleted || progress.Stopped || progress.Capped || progress.InFlight != 0 ||
		progress.Planned != profile.RequestCount || progress.Offered == 0 || progress.Offered > profile.RequestCount ||
		progress.Offered != progress.Launched+progress.Rejected || progress.Completed != progress.Offered ||
		progress.Succeeded+progress.Failed != progress.Completed || progress.PeakInFlight > progress.Launched ||
		(progress.Launched > 0 && progress.PeakInFlight == 0) || progress.Rejected > progress.Failed ||
		(profile.DurationMS == 0 && progress.Offered != profile.RequestCount) ||
		!finiteNonNegative(progress.SendDurationMS) || !finiteNonNegative(progress.DrainDurationMS) ||
		!finiteNonNegative(progress.TotalDurationMS) || progress.SendDurationMS < durationMilliseconds(time.Nanosecond) ||
		progress.TotalDurationMS < durationMilliseconds(time.Nanosecond) ||
		!approximatelyEqual(progress.TotalDurationMS, progress.SendDurationMS+progress.DrainDurationMS) {
		return errors.New("quick performance capacity rung progress is invalid")
	}
	if metrics.Completed != progress.Completed || metrics.Succeeded != progress.Succeeded || metrics.Failed != progress.Failed ||
		metrics.TimedOut > metrics.Failed || metrics.CachedTokens > metrics.PromptTokens ||
		!validPerformanceMetricScalars(metrics) {
		return errors.New("quick performance capacity rung metrics are invalid")
	}
	expectedSuccessRate := float64(progress.Succeeded) / float64(progress.Completed) * 100
	seconds := progress.TotalDurationMS / 1_000
	if !approximatelyEqual(metrics.SuccessRatePercent, expectedSuccessRate) ||
		!approximatelyEqual(metrics.CompletedQPS, float64(progress.Launched)/seconds) ||
		!approximatelyEqual(metrics.SuccessfulRequestQPS, float64(progress.Succeeded)/seconds) ||
		!approximatelyEqual(metrics.RequestQPS, float64(progress.Completed)/seconds) ||
		!approximatelyEqual(metrics.RPM, metrics.RequestQPS*60) ||
		!approximatelyEqual(metrics.InputTPM, float64(metrics.PromptTokens)/seconds*60) ||
		!approximatelyEqual(metrics.OutputTPM, float64(metrics.CompletionTokens)/seconds*60) ||
		!approximatelyEqual(metrics.TotalTPM, metrics.InputTPM+metrics.OutputTPM) ||
		!approximatelyEqual(metrics.GenerationTPS, float64(metrics.CompletionTokens)/seconds) {
		return errors.New("quick performance capacity rung throughput is inconsistent")
	}
	effectiveProfile := performanceCapacityProfile(profile, rung.Target)
	if effectiveProfile.LoadMode == domain.LoadFixedConcurrency {
		if progress.Rejected != 0 || !approximatelyEqual(metrics.OfferedQPS, float64(progress.Offered)/(progress.SendDurationMS/1_000)) ||
			!approximatelyEqual(metrics.LaunchedQPS, metrics.OfferedQPS) {
			return errors.New("quick performance fixed capacity rung load rate is inconsistent")
		}
	} else {
		rateWindowSeconds := progress.SendDurationMS / 1_000
		countLimitReached := progress.Offered >= profile.RequestCount
		useNominalCountWindow := normalizedArrivalPattern(profile.ArrivalPattern) != load.ArrivalPoisson || progress.Offered == 1
		if countLimitReached && useNominalCountWindow {
			rateWindowSeconds = math.Max(rateWindowSeconds, float64(progress.Offered)/effectiveProfile.RatePerSecond)
		}
		if !approximatelyEqual(metrics.OfferedQPS, float64(progress.Offered)/rateWindowSeconds) ||
			!approximatelyEqual(metrics.LaunchedQPS, float64(progress.Launched)/rateWindowSeconds) {
			return errors.New("quick performance open capacity rung load rate is inconsistent")
		}
	}
	if metrics.PromptTokens == 0 {
		if metrics.CacheRatePercent != 0 {
			return errors.New("quick performance capacity rung cache rate is inconsistent")
		}
	} else if !approximatelyEqual(metrics.CacheRatePercent, float64(metrics.CachedTokens)/float64(metrics.PromptTokens)*100) {
		return errors.New("quick performance capacity rung cache rate is inconsistent")
	}
	if rung.Success != (progress.Phase == load.PhaseCompleted && metrics.Completed > 0 && metrics.Failed == 0) {
		return errors.New("quick performance capacity rung transport success is inconsistent")
	}
	if err := validatePerformanceCapacityFailures(rung.Failures, progress, metrics); err != nil {
		return err
	}
	if err := validatePerformanceCapacitySLO(profile, rung.SLOAssessment, progress); err != nil {
		return err
	}
	return nil
}

func validatePerformanceCapacitySLO(profile PerformanceProfile, assessment PerformanceSLOAssessment, progress PerformanceProgress) error {
	if !approximatelyEqual(assessment.Thresholds.TTFTMS, profile.SLOTTFTMS) ||
		!approximatelyEqual(assessment.Thresholds.TPOTMS, profile.SLOTPOTMS) ||
		!approximatelyEqual(assessment.Thresholds.E2EMS, profile.SLOE2EMS) ||
		!approximatelyEqual(assessment.TargetPercent, profile.SLOTargetPercent) ||
		assessment.TotalRequests != progress.Completed || assessment.GoodRequests > assessment.TotalRequests ||
		assessment.BadRequests != assessment.TotalRequests-assessment.GoodRequests ||
		assessment.Violations.Transport != progress.Failed {
		return errors.New("quick performance capacity rung SLO counts are inconsistent")
	}
	expectedPercent := float64(assessment.GoodRequests) / float64(assessment.TotalRequests) * 100
	expectedGoodput := float64(assessment.GoodRequests) / (progress.TotalDurationMS / 1_000)
	if !approximatelyEqual(assessment.GoodRequestPercent, expectedPercent) || !approximatelyEqual(assessment.GoodputQPS, expectedGoodput) {
		return errors.New("quick performance capacity rung SLO rates are inconsistent")
	}
	expectedStatus := PerformanceSLOFailed
	if expectedPercent >= profile.SLOTargetPercent {
		expectedStatus = PerformanceSLOPassed
	}
	if assessment.Status != expectedStatus {
		return errors.New("quick performance capacity rung SLO status is inconsistent")
	}
	latencyViolations := []struct {
		enabled bool
		count   uint64
	}{
		{enabled: profile.SLOTTFTMS > 0, count: assessment.Violations.TTFT},
		{enabled: profile.SLOTPOTMS > 0, count: assessment.Violations.TPOT},
		{enabled: profile.SLOE2EMS > 0, count: assessment.Violations.E2E},
	}
	var maximum, sum uint64
	for _, violation := range latencyViolations {
		if (!violation.enabled && violation.count != 0) || violation.count > progress.Succeeded {
			return errors.New("quick performance capacity rung SLO violations are invalid")
		}
		maximum = max(maximum, violation.count)
		sum += violation.count
	}
	if assessment.BadRequests < assessment.Violations.Transport {
		return errors.New("quick performance capacity rung SLO violations are inconsistent")
	}
	latencyBad := assessment.BadRequests - assessment.Violations.Transport
	if latencyBad > progress.Succeeded || latencyBad < maximum || latencyBad > sum {
		return errors.New("quick performance capacity rung SLO violations are inconsistent")
	}
	return nil
}

func validatePerformanceCapacityFailures(failures []PerformanceFailure, progress PerformanceProgress, metrics load.Metrics) error {
	if failures == nil {
		return errors.New("quick performance capacity rung failures are missing")
	}
	var failed, rejected, timedOut uint64
	var previous domain.ErrorCode
	for index, failure := range failures {
		if failure.Count == 0 || !safePerformanceErrorCode(failure.ErrorCode) || (index > 0 && failure.ErrorCode <= previous) ||
			failed > math.MaxUint64-failure.Count {
			return errors.New("quick performance capacity rung failures are invalid")
		}
		failed += failure.Count
		if failure.ErrorCode == load.ErrorSchedulerOverload {
			rejected += failure.Count
		}
		if failure.ErrorCode == load.ErrorTimeout {
			timedOut += failure.Count
		}
		previous = failure.ErrorCode
	}
	if failed != progress.Failed || rejected != progress.Rejected || timedOut != metrics.TimedOut {
		return errors.New("quick performance capacity rung failures are inconsistent")
	}
	return nil
}

func validPerformanceMetricScalars(metrics load.Metrics) bool {
	values := []float64{
		metrics.SuccessRatePercent, metrics.OfferedQPS, metrics.LaunchedQPS, metrics.CompletedQPS,
		metrics.SuccessfulRequestQPS, metrics.RequestQPS, metrics.RPM, metrics.InputTPM, metrics.OutputTPM,
		metrics.TotalTPM, metrics.GenerationTPS, metrics.TTFTP50, metrics.TTFTP90, metrics.TTFTP95,
		metrics.TTFTP99, metrics.TTFTAverage, metrics.TPOTP50, metrics.TPOTP90, metrics.TPOTP95,
		metrics.TPOTP99, metrics.TPOTAverage, metrics.E2EP50, metrics.E2EP90, metrics.E2EP95,
		metrics.E2EP99, metrics.E2EAverage, metrics.ScheduleLagP50, metrics.ScheduleLagP90,
		metrics.ScheduleLagP95, metrics.ScheduleLagP99, metrics.ScheduleLagAverage, metrics.CacheRatePercent,
	}
	for _, value := range values {
		if !finiteNonNegative(value) {
			return false
		}
	}
	return metrics.SuccessRatePercent <= 100 && metrics.CacheRatePercent <= 100 &&
		validPerformanceMetricDistributions(metrics)
}

func validPerformanceMetricDistributions(metrics load.Metrics) bool {
	distributions := [][5]float64{
		{metrics.TTFTP50, metrics.TTFTP90, metrics.TTFTP95, metrics.TTFTP99, metrics.TTFTAverage},
		{metrics.TPOTP50, metrics.TPOTP90, metrics.TPOTP95, metrics.TPOTP99, metrics.TPOTAverage},
		{metrics.E2EP50, metrics.E2EP90, metrics.E2EP95, metrics.E2EP99, metrics.E2EAverage},
		{metrics.ScheduleLagP50, metrics.ScheduleLagP90, metrics.ScheduleLagP95, metrics.ScheduleLagP99, metrics.ScheduleLagAverage},
	}
	for _, distribution := range distributions {
		if distribution[0] > distribution[1] || distribution[1] > distribution[2] || distribution[2] > distribution[3] ||
			(distribution[4] == 0 && (distribution[0] != 0 || distribution[1] != 0 || distribution[2] != 0 || distribution[3] != 0)) {
			return false
		}
	}
	if metrics.Succeeded == 0 {
		for _, distribution := range distributions[:3] {
			for _, value := range distribution {
				if value != 0 {
					return false
				}
			}
		}
	}
	if metrics.Completed == 0 {
		for _, value := range distributions[3] {
			if value != 0 {
				return false
			}
		}
	}
	return true
}
