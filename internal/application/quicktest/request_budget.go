package quicktest

import (
	"errors"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func phaseThreeConfigured(profile PerformanceProfile) bool {
	return profile.WarmupRequests > 0 || profile.RampDurationMS > 0 || profile.RampRequestCap > 0 || profile.SliceDurationMS > 0
}

func buildPerformanceRequestBudget(profile PerformanceProfile) (*PerformanceRequestBudget, uint64, error) {
	if profile.WarmupRequests > MaxPerformanceRequests || profile.RampRequestCap > MaxPerformanceRequests ||
		profile.RampDurationMS > MaxPerformanceDurationMS || profile.SliceDurationMS > MaxPerformanceDurationMS {
		return nil, 0, errors.New("performance phase exceeds configured limit")
	}
	if profile.RampDurationMS == 0 {
		if profile.RampRequestCap != 0 {
			return nil, 0, errors.New("ramp request cap requires a ramp duration")
		}
	} else {
		switch profile.LoadMode {
		case domain.LoadFixedConcurrency:
			if profile.RampRequestCap == 0 {
				return nil, 0, errors.New("fixed-concurrency ramp requires a request cap")
			}
		case domain.LoadOpenLoop:
			if profile.RampRequestCap != 0 {
				return nil, 0, errors.New("open-loop ramp request cap is derived")
			}
		default:
			return nil, 0, errors.New("ramp load mode is invalid")
		}
	}

	rampCap := profile.RampRequestCap
	if profile.RampDurationMS > 0 && profile.LoadMode == domain.LoadOpenLoop {
		var err error
		rampCap, err = load.EstimateLinearRampRequestCap(
			profile.RatePerSecond,
			time.Duration(profile.RampDurationMS)*time.Millisecond,
			normalizedArrivalPattern(profile.ArrivalPattern),
		)
		if err != nil {
			return nil, 0, err
		}
	}
	if rampCap > MaxPerformanceRequests || profile.WarmupRequests > MaxPerformanceRequests-rampCap {
		return nil, 0, errors.New("performance preparation exceeds request budget")
	}
	prepCap := profile.WarmupRequests + rampCap

	var measuredCap uint64
	switch {
	case profile.RequestCount > 0:
		measuredCap = profile.RequestCount
	case profile.LoadMode == domain.LoadFixedConcurrency:
		measuredCap = MaxPerformanceRequests - prepCap
	case profile.LoadMode == domain.LoadOpenLoop:
		var err error
		measuredCap, err = estimateOpenLoopRequestCap(profile.RatePerSecond, profile.DurationMS, normalizedArrivalPattern(profile.ArrivalPattern))
		if err != nil {
			return nil, 0, err
		}
	}
	if measuredCap == 0 || measuredCap > MaxPerformanceRequests || prepCap > MaxPerformanceRequests-measuredCap {
		return nil, 0, errors.New("performance request budget exceeds hard limit")
	}
	if !phaseThreeConfigured(profile) {
		return nil, measuredCap, nil
	}
	budget := &PerformanceRequestBudget{
		Limit:       MaxPerformanceRequests,
		WarmupCap:   profile.WarmupRequests,
		RampCap:     rampCap,
		MeasuredCap: measuredCap,
		TotalCap:    prepCap + measuredCap,
	}
	return budget, measuredCap, nil
}

func estimateOpenLoopRequestCap(rate float64, durationMS uint64, arrival load.ArrivalPattern) (uint64, error) {
	if arrival != load.ArrivalPoisson {
		planned, capped, err := load.EstimateOpenLoopDurationSchedule(
			rate,
			time.Duration(durationMS)*time.Millisecond,
			arrival,
			0,
			MaxPerformanceRequests,
		)
		if err == nil && capped {
			return 0, errors.New("performance request estimate exceeds hard limit")
		}
		return planned, err
	}
	intensity := float64(durationMS) * rate / 1_000
	if math.IsNaN(intensity) || math.IsInf(intensity, 0) || intensity < 0 {
		return 0, errors.New("performance request estimate overflow")
	}
	if arrival == load.ArrivalPoisson {
		intensity = math.Ceil(intensity*2) + 1
	} else {
		intensity = math.Ceil(intensity)
	}
	if math.IsNaN(intensity) || math.IsInf(intensity, 0) || intensity >= math.Ldexp(1, 64) {
		return 0, errors.New("performance request estimate overflow")
	}
	return uint64(intensity), nil
}
