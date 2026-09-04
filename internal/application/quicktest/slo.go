package quicktest

import (
	"errors"
	"math"

	"github.com/894x/llm-test-studio/internal/execution/load"
)

func performanceSLOConfigured(profile PerformanceProfile) bool {
	return profile.SLOTTFTMS > 0 || profile.SLOTPOTMS > 0 || profile.SLOE2EMS > 0 || profile.SLOTargetPercent > 0
}

func validatePerformanceSLO(report PerformanceReport) error {
	if !performanceSLOConfigured(report.Profile) {
		if report.SLOAssessment != nil {
			return errors.New("quick performance report has an unconfigured SLO assessment")
		}
		return nil
	}
	expected := buildPerformanceSLOAssessment(report.Profile, report.Samples, report.Progress)
	if report.SLOAssessment == nil || expected == nil || !equalPerformanceSLOAssessment(*report.SLOAssessment, *expected) {
		return errors.New("quick performance report SLO assessment is inconsistent")
	}
	return nil
}

func equalPerformanceSLOAssessment(left, right PerformanceSLOAssessment) bool {
	return left.Status == right.Status &&
		approximatelyEqual(left.Thresholds.TTFTMS, right.Thresholds.TTFTMS) &&
		approximatelyEqual(left.Thresholds.TPOTMS, right.Thresholds.TPOTMS) &&
		approximatelyEqual(left.Thresholds.E2EMS, right.Thresholds.E2EMS) &&
		approximatelyEqual(left.TargetPercent, right.TargetPercent) &&
		left.TotalRequests == right.TotalRequests && left.GoodRequests == right.GoodRequests && left.BadRequests == right.BadRequests &&
		approximatelyEqual(left.GoodRequestPercent, right.GoodRequestPercent) &&
		approximatelyEqual(left.GoodputQPS, right.GoodputQPS) && left.Violations == right.Violations
}

func validPerformanceSLOConfiguration(profile PerformanceProfile) bool {
	values := []float64{profile.SLOTTFTMS, profile.SLOTPOTMS, profile.SLOE2EMS, profile.SLOTargetPercent}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	if !performanceSLOConfigured(profile) {
		return true
	}
	return profile.SLOTargetPercent > 0 && profile.SLOTargetPercent <= 100 &&
		(profile.SLOTTFTMS > 0 || profile.SLOTPOTMS > 0 || profile.SLOE2EMS > 0)
}

func buildPerformanceSLOAssessment(
	profile PerformanceProfile,
	samples []PerformanceSample,
	progress PerformanceProgress,
) *PerformanceSLOAssessment {
	if !performanceSLOConfigured(profile) {
		return nil
	}
	assessment := &PerformanceSLOAssessment{
		Status: PerformanceSLONotEvaluated,
		Thresholds: PerformanceSLOThresholds{
			TTFTMS: profile.SLOTTFTMS,
			TPOTMS: profile.SLOTPOTMS,
			E2EMS:  profile.SLOE2EMS,
		},
		TargetPercent: profile.SLOTargetPercent,
		TotalRequests: uint64(len(samples)),
	}
	for _, sample := range samples {
		good := sample.Success
		if !sample.Success {
			assessment.Violations.Transport++
		} else {
			if profile.SLOTTFTMS > 0 && (sample.TTFTMS <= 0 || sample.TTFTMS > profile.SLOTTFTMS) {
				assessment.Violations.TTFT++
				good = false
			}
			if profile.SLOTPOTMS > 0 && (sample.TPOTMS <= 0 || sample.TPOTMS > profile.SLOTPOTMS) {
				assessment.Violations.TPOT++
				good = false
			}
			if profile.SLOE2EMS > 0 && (sample.E2EMS <= 0 || sample.E2EMS > profile.SLOE2EMS) {
				assessment.Violations.E2E++
				good = false
			}
		}
		if good {
			assessment.GoodRequests++
		}
	}
	assessment.BadRequests = assessment.TotalRequests - assessment.GoodRequests
	if assessment.TotalRequests > 0 {
		assessment.GoodRequestPercent = float64(assessment.GoodRequests) / float64(assessment.TotalRequests) * 100
	}
	seconds := progress.TotalDurationMS / 1_000
	if seconds > 0 {
		assessment.GoodputQPS = float64(assessment.GoodRequests) / seconds
	}
	if progress.Phase == load.PhaseCompleted && !progress.Stopped && assessment.TotalRequests > 0 {
		if assessment.GoodRequestPercent >= profile.SLOTargetPercent {
			assessment.Status = PerformanceSLOPassed
		} else {
			assessment.Status = PerformanceSLOFailed
		}
	}
	return assessment
}
