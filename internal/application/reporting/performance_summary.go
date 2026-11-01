package reporting

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

// PerformanceSummary is a read-time view of sealed steady-state observations.
// It does not replace an item's historical metrics, SLO or capacity verdict.
type PerformanceSummary struct {
	ItemCount            uint64                             `json:"item_count"`
	MeasuredItemCount    uint64                             `json:"measured_item_count"`
	CompletedItemCount   uint64                             `json:"completed_item_count"`
	Completed            uint64                             `json:"completed"`
	Succeeded            uint64                             `json:"succeeded"`
	Failed               uint64                             `json:"failed"`
	TimedOut             uint64                             `json:"timed_out"`
	PromptTokens         uint64                             `json:"prompt_tokens"`
	CompletionTokens     uint64                             `json:"completion_tokens"`
	CachedTokens         uint64                             `json:"cached_tokens"`
	SuccessRatePercent   *float64                           `json:"success_rate_percent"`
	CacheRatePercent     *float64                           `json:"cache_rate_percent"`
	TotalDurationMS      float64                            `json:"total_duration_ms"`
	SuccessfulRequestQPS *float64                           `json:"successful_request_qps"`
	SamplesComplete      bool                               `json:"samples_complete"`
	TTFT                 *quicktest.PerformanceLatencySlice `json:"ttft"`
	E2E                  *quicktest.PerformanceLatencySlice `json:"e2e"`
}

func summarizePerformance(entries []EntryDetail) (*PerformanceSummary, error) {
	summary := &PerformanceSummary{SamplesComplete: true}
	ttfts, e2es := []float64{}, []float64{}
	for _, entry := range entries {
		if entry.TargetKind != domain.PlanTargetPerformance {
			continue
		}
		summary.ItemCount++
		if entry.Status == domain.EntryReportCompleted {
			summary.CompletedItemCount++
		}
		if len(entry.Performance) == 0 {
			continue
		}
		var report quicktest.PerformanceReport
		if err := json.Unmarshal(entry.Performance, &report); err != nil {
			return nil, fmt.Errorf("decode performance summary item: %w", err)
		}
		metrics := report.Metrics
		if metrics.Completed == 0 {
			continue
		}
		summary.MeasuredItemCount++
		summary.Completed += metrics.Completed
		summary.Succeeded += metrics.Succeeded
		summary.Failed += metrics.Failed
		summary.TimedOut += metrics.TimedOut
		summary.PromptTokens += metrics.PromptTokens
		summary.CompletionTokens += metrics.CompletionTokens
		summary.CachedTokens += metrics.CachedTokens
		summary.TotalDurationMS += report.Progress.TotalDurationMS
		if uint64(len(report.Samples)) != metrics.Completed {
			summary.SamplesComplete = false
		}
		for _, sample := range report.Samples {
			if !sample.Success {
				continue
			}
			e2es = append(e2es, sample.E2EMS)
			if sample.TTFTObserved || sample.TTFTMS > 0 {
				ttfts = append(ttfts, sample.TTFTMS)
			}
		}
	}
	if summary.ItemCount < 2 {
		return nil, nil
	}
	if summary.Completed > 0 {
		rate := float64(summary.Succeeded) / float64(summary.Completed) * 100
		summary.SuccessRatePercent = &rate
	}
	if summary.PromptTokens > 0 {
		rate := float64(summary.CachedTokens) / float64(summary.PromptTokens) * 100
		summary.CacheRatePercent = &rate
	}
	if summary.TotalDurationMS > 0 {
		rate := float64(summary.Succeeded) / (summary.TotalDurationMS / 1_000)
		summary.SuccessfulRequestQPS = &rate
	}
	// Never average item percentiles or publish a percentile of truncated samples.
	if summary.SamplesComplete {
		summary.TTFT = summaryLatency(ttfts)
		summary.E2E = summaryLatency(e2es)
	}
	return summary, nil
}

func summaryLatency(values []float64) *quicktest.PerformanceLatencySlice {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	var total float64
	for _, value := range values {
		total += value
	}
	return &quicktest.PerformanceLatencySlice{
		Count: uint64(len(values)), AverageMS: total / float64(len(values)),
		P50MS: interpolatedPercentile(values, 0.5),
		P95MS: interpolatedPercentile(values, 0.95),
		P99MS: interpolatedPercentile(values, 0.99),
	}
}

func performanceSummaryMetrics(summary *PerformanceSummary) map[string]domain.MetricValue {
	metrics := map[string]domain.MetricValue{}
	if summary == nil {
		return metrics
	}
	for name, value := range map[string]uint64{
		"performance_items": summary.ItemCount, "measured_items": summary.MeasuredItemCount,
		"completed_requests": summary.Completed, "succeeded_requests": summary.Succeeded,
		"failed_requests": summary.Failed, "timed_out_requests": summary.TimedOut,
		"prompt_tokens": summary.PromptTokens, "completion_tokens": summary.CompletionTokens,
		"cached_tokens": summary.CachedTokens,
	} {
		unit := "count"
		tokenMetric := name == "prompt_tokens" || name == "completion_tokens" || name == "cached_tokens"
		if tokenMetric {
			unit = "tokens"
		}
		metrics[name] = domain.MetricValue{Value: float64(value), Unit: unit, Samples: int(summary.Completed)}
	}
	metrics["steady_duration_ms"] = domain.MetricValue{Value: summary.TotalDurationMS, Unit: "ms"}
	for name, value := range map[string]*float64{
		"success_rate_percent": summary.SuccessRatePercent, "cache_rate_percent": summary.CacheRatePercent,
		"successful_request_qps": summary.SuccessfulRequestQPS,
	} {
		if value != nil {
			unit := "percent"
			if name == "successful_request_qps" {
				unit = "requests/second"
			}
			metrics[name] = domain.MetricValue{Value: *value, Unit: unit, Samples: int(summary.Completed)}
		}
	}
	for name, latency := range map[string]*quicktest.PerformanceLatencySlice{
		"ttft_p95_ms": summary.TTFT, "e2e_p95_ms": summary.E2E,
	} {
		if latency != nil {
			metrics[name] = domain.MetricValue{Value: latency.P95MS, Unit: "ms", Samples: int(latency.Count)}
		}
	}
	return metrics
}
