package load

import (
	"math"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestComputeMetricsMatchesPercentileAndSuccessFiltering(t *testing.T) {
	observations := []Observation{
		{Success: true, ScheduleLag: 10 * time.Millisecond, E2E: 100 * time.Millisecond, TTFT: 20 * time.Millisecond, CompletionTokens: 3, PromptTokens: 100, CachedTokens: 40},
		{Success: true, ScheduleLag: 20 * time.Millisecond, E2E: 300 * time.Millisecond, TTFT: 60 * time.Millisecond, CompletionTokens: 5, PromptTokens: 300, CachedTokens: 80},
		{Success: false, TimedOut: true, ScheduleLag: 30 * time.Millisecond, E2E: 500 * time.Millisecond, TTFT: 400 * time.Millisecond, CompletionTokens: 999, PromptTokens: 999, CachedTokens: 999},
	}

	metrics := ComputeMetrics(observations, 2*time.Second)
	if metrics.Succeeded != 2 || metrics.Failed != 1 || metrics.TimedOut != 1 {
		t.Fatalf("counts = %#v", metrics)
	}
	if metrics.RequestQPS != 1.5 || metrics.SuccessfulRequestQPS != 1 || metrics.CompletedQPS != 1.5 || metrics.RPM != 90 || metrics.InputTPM != 12_000 || metrics.OutputTPM != 240 {
		t.Fatalf("throughput = %#v", metrics)
	}
	if metrics.TTFTP50 != 40 || metrics.E2EP50 != 200 || metrics.CacheRatePercent != 30 {
		t.Fatalf("latency/cache metrics = %#v", metrics)
	}
	if metrics.ScheduleLagP50 != 20 || metrics.ScheduleLagP90 != 28 || metrics.ScheduleLagP95 != 29 || math.Abs(metrics.ScheduleLagP99-29.8) > 1e-9 || metrics.ScheduleLagAverage != 20 {
		t.Fatalf("client queue metrics = %#v", metrics)
	}
	// TPOT is measured after the first generated token, so N tokens have N-1 intervals.
	if metrics.TPOTP50 != 50 {
		t.Fatalf("TPOT P50 = %v ms/token, want 50", metrics.TPOTP50)
	}
}

func TestPercentileIsInterpolatedAndMetricsStayFiniteAtZeroElapsed(t *testing.T) {
	if got := percentile([]float64{10, 20, 30, 40}, 0.9); got != 37 {
		t.Fatalf("percentile = %v, want 37", got)
	}
	metrics := ComputeMetrics(nil, 0)
	for name, value := range map[string]float64{
		"qps": metrics.RequestQPS, "rpm": metrics.RPM, "success_rate": metrics.SuccessRatePercent,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value != 0 {
			t.Fatalf("%s = %v, want finite zero", name, value)
		}
	}
}

func TestComputeMetricsWithProgressSeparatesLoadAndCompletionRates(t *testing.T) {
	observations := []Observation{
		{Success: true, CompletionTokens: 3},
		{Success: true, CompletionTokens: 5},
		{Success: false},
		{Success: false, ErrorCode: ErrorSchedulerOverload},
	}
	progress := Progress{
		Launched:      3,
		Rejected:      1,
		SendDuration:  time.Second,
		TotalDuration: 2 * time.Second,
	}

	metrics := ComputeMetricsWithProgress(observations, progress)
	if metrics.OfferedQPS != 4 || metrics.LaunchedQPS != 3 {
		t.Fatalf("load rates = %#v", metrics)
	}
	if metrics.SuccessfulRequestQPS != 1 || metrics.CompletedQPS != 1.5 || metrics.RequestQPS != 2 {
		t.Fatalf("completion rates = %#v", metrics)
	}
	if metrics.RequestQPS == metrics.CompletedQPS {
		t.Fatalf("legacy request QPS unexpectedly excluded the rejected observation: %#v", metrics)
	}
}

func TestComputeMetricsWithProfileUsesHonestOpenLoopRateWindows(t *testing.T) {
	countProfile := domain.LoadProfile{Mode: domain.LoadOpenLoop, RequestCount: 3, RatePerSecond: 500}
	countProgress := Progress{Offered: 3, Launched: 3, SendDuration: 4 * time.Millisecond, TotalDuration: 10 * time.Millisecond}
	metrics := ComputeMetricsWithProfile(make([]Observation, 3), countProgress, countProfile)
	if metrics.OfferedQPS != 500 || metrics.LaunchedQPS != 500 {
		t.Fatalf("count-limited rates = %#v", metrics)
	}
	countProgress.SendDuration = 10 * time.Millisecond
	metrics = ComputeMetricsWithProfile(make([]Observation, 3), countProgress, countProfile)
	if metrics.OfferedQPS != 300 || metrics.LaunchedQPS != 300 {
		t.Fatalf("lagged count-limited rates = %#v", metrics)
	}

	durationProfile := domain.LoadProfile{Mode: domain.LoadOpenLoop, DurationMS: 50, RatePerSecond: 10}
	durationProgress := Progress{Offered: 1, Launched: 1, SendDuration: 50 * time.Millisecond, TotalDuration: 50 * time.Millisecond}
	metrics = ComputeMetricsWithProfile(make([]Observation, 1), durationProgress, durationProfile)
	if metrics.OfferedQPS != 20 || metrics.LaunchedQPS != 20 {
		t.Fatalf("duration-limited rates = %#v", metrics)
	}
}

func TestComputeMetricsWithArrivalUsesRealizedPoissonWindow(t *testing.T) {
	profile := domain.LoadProfile{Mode: domain.LoadOpenLoop, RequestCount: 3, RatePerSecond: 10}
	progress := Progress{Offered: 3, Launched: 3, SendDuration: 200 * time.Millisecond, TotalDuration: 250 * time.Millisecond}
	constant := ComputeMetricsWithArrival(make([]Observation, 3), progress, profile, ArrivalConstant)
	poisson := ComputeMetricsWithArrival(make([]Observation, 3), progress, profile, ArrivalPoisson)
	if constant.OfferedQPS != 10 {
		t.Fatalf("constant offered QPS = %v, want configured schedule window", constant.OfferedQPS)
	}
	if poisson.OfferedQPS != 15 {
		t.Fatalf("poisson offered QPS = %v, want realized schedule window", poisson.OfferedQPS)
	}
}

func TestComputeMetricsWithArrivalUsesNominalWindowForSinglePoissonRequest(t *testing.T) {
	profile := domain.LoadProfile{Mode: domain.LoadOpenLoop, RequestCount: 1, RatePerSecond: 10}
	progress := Progress{Offered: 1, Launched: 1, SendDuration: time.Millisecond, TotalDuration: 2 * time.Millisecond}
	metrics := ComputeMetricsWithArrival([]Observation{{Success: true}}, progress, profile, ArrivalPoisson)
	if metrics.OfferedQPS != 10 || metrics.LaunchedQPS != 10 {
		t.Fatalf("single-request poisson rates = %#v, want nominal 10 QPS", metrics)
	}
}
