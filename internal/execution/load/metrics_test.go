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

func TestComputeMetricsUsesSuccessfulPhaseFiveCohorts(t *testing.T) {
	observations := []Observation{
		{Success: true, Streaming: true, TTFB: 10 * time.Millisecond, TTFTAny: 20 * time.Millisecond, TTFTVisible: 20 * time.Millisecond, TTST: 999 * time.Millisecond, SemanticChunkCount: 1},
		{Success: true, Streaming: true, TTFB: 20 * time.Millisecond, TTFTAny: 40 * time.Millisecond, TTST: 60 * time.Millisecond, ObservedICL: 20 * time.Millisecond, SemanticChunkCount: 2},
		{Success: true, Streaming: true, TTFTAny: 60 * time.Millisecond, TTFTVisible: 90 * time.Millisecond, TTST: 70 * time.Millisecond, ObservedICL: 0, SemanticChunkCount: 3},
		{Success: true, Streaming: true, TTFB: 40 * time.Millisecond, SemanticChunkCount: 0},
		{Success: true, TTFB: 50 * time.Millisecond, SemanticChunkCount: 7},
		{Success: false, Streaming: true, TTFB: 999 * time.Millisecond, TTFTAny: 999 * time.Millisecond, TTFTVisible: 999 * time.Millisecond, TTST: 999 * time.Millisecond, ObservedICL: 999 * time.Millisecond, SemanticChunkCount: 99},
	}

	metrics := ComputeMetrics(observations, time.Second)
	if metrics.TTFBSamples != 4 || metrics.TTFBP50 != 30 || metrics.TTFBP95 != 48.5 ||
		math.Abs(metrics.TTFBP99-49.7) > 1e-9 || metrics.TTFBAverage != 30 {
		t.Fatalf("TTFB metrics = %#v", metrics)
	}
	if metrics.TTFTSamples != 3 || metrics.TTFTAnySamples != 3 || metrics.TTFTAnyP50 != 40 ||
		metrics.TTFTAnyP95 != 58 || math.Abs(metrics.TTFTAnyP99-59.6) > 1e-9 || metrics.TTFTAnyAverage != 40 ||
		metrics.TTFTP50 != metrics.TTFTAnyP50 || metrics.TTFTP90 != 56 || metrics.TTFTP95 != metrics.TTFTAnyP95 ||
		metrics.TTFTP99 != metrics.TTFTAnyP99 || metrics.TTFTAverage != metrics.TTFTAnyAverage {
		t.Fatalf("TTFT metrics = %#v", metrics)
	}
	if metrics.TTFTVisibleSamples != 2 || metrics.TTFTVisibleP50 != 55 || metrics.TTFTVisibleP95 != 86.5 ||
		math.Abs(metrics.TTFTVisibleP99-89.3) > 1e-9 || metrics.TTFTVisibleAverage != 55 {
		t.Fatalf("visible TTFT metrics = %#v", metrics)
	}
	if metrics.TTSTSamples != 2 || metrics.TTSTP50 != 65 || metrics.TTSTP95 != 69.5 ||
		math.Abs(metrics.TTSTP99-69.9) > 1e-9 || metrics.TTSTAverage != 65 {
		t.Fatalf("TTST metrics = %#v", metrics)
	}
	if metrics.ObservedICLSamples != 2 || metrics.ObservedICLP50 != 10 || metrics.ObservedICLP95 != 19 ||
		math.Abs(metrics.ObservedICLP99-19.8) > 1e-9 || metrics.ObservedICLAverage != 10 {
		t.Fatalf("observed ICL metrics = %#v", metrics)
	}
	if metrics.SemanticChunkCountSamples != 4 || metrics.SemanticChunkCountP50 != 1.5 || math.Abs(metrics.SemanticChunkCountP95-2.85) > 1e-9 ||
		math.Abs(metrics.SemanticChunkCountP99-2.97) > 1e-9 || metrics.SemanticChunkCountAverage != 1.5 {
		t.Fatalf("semantic chunk metrics = %#v", metrics)
	}
}
