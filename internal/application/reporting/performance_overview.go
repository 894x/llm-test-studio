package reporting

import (
	"math"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

// PerformanceOverview is a read-time presentation projection, never a replacement
// for the archived transport, per-request SLO, or capacity conclusion. The current
// reference standard applies success ≥99%, TTFT P95 <15 s, TPOT P95 <40 ms,
// E2E P95 <30 s and successful throughput ≥2 req/s. Saved E2E limits take precedence.
type PerformanceOverview struct {
	Status           string                      `json:"status"`
	SampleCount      uint64                      `json:"sample_count"`
	PassedCount      uint64                      `json:"passed_count"`
	EvaluatedCount   uint64                      `json:"evaluated_count"`
	ObservedCount    uint64                      `json:"observed_count"`
	UnevaluatedCount uint64                      `json:"unevaluated_count"`
	Rating           *PerformanceRating          `json:"rating"`
	Metrics          []PerformanceOverviewMetric `json:"metrics"`
}

// PerformanceRating describes this report's configured targets, not an absolute
// ranking across channels with different workloads or targets.
type PerformanceRating struct {
	Method      string  `json:"method"`
	Score       float64 `json:"score"`
	MaxScore    uint64  `json:"max_score"`
	MetricCount uint64  `json:"metric_count"`
}

type PerformanceOverviewMetric struct {
	Key        string   `json:"key"`
	Value      *float64 `json:"value"`
	Unit       string   `json:"unit"`
	Status     string   `json:"status"`
	Target     *float64 `json:"target"`
	Comparison string   `json:"comparison"`
	Reference  bool     `json:"reference"`
}

func performanceOverview(report *quicktest.PerformanceReport) PerformanceOverview {
	complete := report.Progress.Phase == load.PhaseCompleted && !report.Progress.Stopped
	overview := PerformanceOverview{
		Status: "observed", SampleCount: report.Metrics.Completed,
		Metrics: make([]PerformanceOverviewMetric, 0, 6),
	}
	success := PerformanceOverviewMetric{
		Key: "success_rate", Unit: "percent", Status: "unavailable",
		Target: floatPointer(99), Comparison: "at_least", Reference: true,
	}
	if report.Metrics.Completed > 0 {
		success.Value = floatPointer(report.Metrics.SuccessRatePercent)
		success.Status = "not_evaluated"
		if complete {
			success.Status = "failed"
			if report.Metrics.SuccessRatePercent >= *success.Target {
				success.Status = "passed"
			}
		}
	}
	overview.Metrics = append(overview.Metrics, success)
	slo := PerformanceOverviewMetric{
		Key: "slo_rate", Unit: "percent", Status: "observed", Comparison: "none",
	}
	assessment := report.SLOAssessment
	if assessment != nil {
		slo.Target = floatPointer(assessment.TargetPercent)
		slo.Comparison = "at_least"
		slo.Status = string(assessment.Status)
		if assessment.TotalRequests > 0 {
			slo.Value = floatPointer(assessment.GoodRequestPercent)
		} else {
			slo.Status = "unavailable"
		}
	}
	overview.Metrics = append(overview.Metrics, slo)

	latencies := []struct {
		key        string
		value      float64
		samples    uint64
		target     float64
		comparison string
	}{
		{key: "ttft_p95", value: report.Metrics.TTFTP95, samples: report.Metrics.TTFTSamples,
			target: 15_000, comparison: "less_than"},
		{key: "tpot_p95", value: report.Metrics.TPOTP95, samples: performanceTPOTSamples(report.Samples),
			target: 40, comparison: "less_than"},
		{key: "e2e_p95", value: report.Metrics.E2EP95, samples: report.Metrics.Succeeded,
			target: 30_000, comparison: "less_than"},
	}
	if assessment != nil && assessment.Thresholds.E2EMS > 0 {
		latencies[2].target = assessment.Thresholds.E2EMS
		latencies[2].comparison = "at_most"
	}
	archivedAssessmentComplete := complete && assessment != nil &&
		assessment.Status != quicktest.PerformanceSLONotEvaluated
	for _, latency := range latencies {
		metric := PerformanceOverviewMetric{
			Key: latency.key, Unit: "ms", Status: "unavailable", Comparison: "none",
		}
		if latency.samples > 0 && report.Metrics.Succeeded > 0 {
			metric.Value = floatPointer(latency.value)
			metric.Status = "observed"
		}
		if latency.target > 0 {
			metric.Target = floatPointer(latency.target)
			metric.Comparison = latency.comparison
			metric.Reference = true
			if metric.Value != nil {
				metric.Status = "not_evaluated"
				if complete && latency.comparison == "less_than" {
					metric.Status = "failed"
					if latency.value < latency.target {
						metric.Status = "passed"
					}
				}
				if archivedAssessmentComplete && latency.comparison == "at_most" {
					metric.Status = compareOverviewLatency(latency.value, latency.target, assessment.LatencyPrecision)
				}
			}
		}
		overview.Metrics = append(overview.Metrics, metric)
	}
	throughput := PerformanceOverviewMetric{
		Key: "successful_qps", Unit: "req/s", Status: "unavailable",
		Target: floatPointer(2), Comparison: "at_least", Reference: true,
	}
	zeroThroughput := report.Metrics.Completed > 0 && report.Metrics.Succeeded == 0 && report.Progress.TotalDurationMS > 0
	if report.Metrics.SuccessfulRequestQPS > 0 || zeroThroughput {
		throughput.Value = floatPointer(report.Metrics.SuccessfulRequestQPS)
		throughput.Status = "not_evaluated"
		if complete {
			throughput.Status = "failed"
			if report.Metrics.SuccessfulRequestQPS >= *throughput.Target {
				throughput.Status = "passed"
			}
		}
	}
	overview.Metrics = append(overview.Metrics, throughput)
	for _, metric := range overview.Metrics {
		switch metric.Status {
		case "passed":
			overview.PassedCount++
			overview.EvaluatedCount++
		case "failed":
			overview.EvaluatedCount++
		case "observed":
			overview.ObservedCount++
		default:
			overview.UnevaluatedCount++
		}
	}
	switch {
	case overview.PassedCount < overview.EvaluatedCount:
		overview.Status = "failed"
	case overview.UnevaluatedCount > 0:
		overview.Status = "not_evaluated"
	case overview.EvaluatedCount > 0:
		overview.Status = "passed"
	}
	overview.Rating = performanceTargetRating(overview.Metrics)
	return overview
}

func performanceTargetRating(metrics []PerformanceOverviewMetric) *PerformanceRating {
	var total float64
	var count uint64
	for _, metric := range metrics {
		if metric.Status != "passed" && metric.Status != "failed" {
			continue
		}
		if metric.Value == nil || metric.Target == nil {
			return nil
		}
		value, target := *metric.Value, *metric.Target
		if !validRatingNumber(value) || !validRatingNumber(target) {
			return nil
		}
		contribution := 1.0
		if metric.Status == "failed" {
			contribution = 0
			switch metric.Comparison {
			case "at_least":
				if target > 0 {
					contribution = value / target
				}
			case "at_most", "less_than":
				if value > 0 {
					contribution = target / value
				}
			default:
				return nil
			}
		}
		total += math.Max(0, math.Min(1, contribution))
		count++
	}
	if count == 0 {
		return nil
	}
	return &PerformanceRating{
		Method: "target_ratio_v1", Score: math.Round(total/float64(count)*500) / 100,
		MaxScore: 5, MetricCount: count,
	}
}

func validRatingNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func compareOverviewLatency(value, target float64, precision string) string {
	if precision == domain.SLALatencyTwoDecimalsV1 {
		value = domain.RoundLatencyMilliseconds(value)
		target = domain.RoundLatencyMilliseconds(target)
	}
	if value <= target {
		return "passed"
	}
	return "failed"
}

func floatPointer(value float64) *float64 { return &value }

func performanceTPOTSamples(samples []quicktest.PerformanceSample) uint64 {
	var count uint64
	for _, sample := range samples {
		observed := sample.TTFTObserved || sample.TTFTMS > 0
		zeroObserved := observed && sample.CompletionTokens > 1 && sample.E2EMS >= sample.TTFTMS
		if sample.Success && (sample.TPOTMS > 0 || zeroObserved) {
			count++
		}
	}
	return count
}
