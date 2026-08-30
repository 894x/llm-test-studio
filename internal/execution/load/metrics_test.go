package load

import (
	"math"
	"testing"
	"time"
)

func TestComputeMetricsMatchesLegacyPercentileAndSuccessFiltering(t *testing.T) {
	observations := []Observation{
		{Success: true, E2E: 100 * time.Millisecond, TTFT: 20 * time.Millisecond, CompletionTokens: 3, PromptTokens: 100, CachedTokens: 40},
		{Success: true, E2E: 300 * time.Millisecond, TTFT: 60 * time.Millisecond, CompletionTokens: 5, PromptTokens: 300, CachedTokens: 80},
		{Success: false, TimedOut: true, E2E: 500 * time.Millisecond, TTFT: 400 * time.Millisecond, CompletionTokens: 999, PromptTokens: 999, CachedTokens: 999},
	}

	metrics := ComputeMetrics(observations, 2*time.Second)
	if metrics.Succeeded != 2 || metrics.Failed != 1 || metrics.TimedOut != 1 {
		t.Fatalf("counts = %#v", metrics)
	}
	if metrics.RequestQPS != 1.5 || metrics.RPM != 90 || metrics.InputTPM != 12_000 || metrics.OutputTPM != 240 {
		t.Fatalf("throughput = %#v", metrics)
	}
	if metrics.TTFTP50 != 40 || metrics.E2EP50 != 200 || metrics.CacheRatePercent != 30 {
		t.Fatalf("latency/cache metrics = %#v", metrics)
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
