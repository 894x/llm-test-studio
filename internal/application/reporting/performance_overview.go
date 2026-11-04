package reporting

import (
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

// PerformanceOverview is a read-time presentation projection, never a replacement
// for the archived transport, per-request SLO, or capacity conclusion.
type PerformanceOverview struct {
	Status           string                      `json:"status"`
	SampleCount      uint64                      `json:"sample_count"`
	PassedCount      uint64                      `json:"passed_count"`
	EvaluatedCount   uint64                      `json:"evaluated_count"`
	ObservedCount    uint64                      `json:"observed_count"`
	UnevaluatedCount uint64                      `json:"unevaluated_count"`
	Metrics          []PerformanceOverviewMetric `json:"metrics"`
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
		Target: floatPointer(100), Comparison: "at_least",
	}
	if report.Metrics.Completed > 0 {
		success.Value = floatPointer(report.Metrics.SuccessRatePercent)
		success.Status = "not_evaluated"
		if complete {
			success.Status = "failed"
			if report.Success {
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
		key     string
		value   float64
		samples uint64
		target  float64
	}{
		{key: "ttft_p95", value: report.Metrics.TTFTP95, samples: report.Metrics.TTFTSamples},
		{key: "tpot_p95", value: report.Metrics.TPOTP95, samples: performanceTPOTSamples(report.Samples)},
		{key: "e2e_p95", value: report.Metrics.E2EP95, samples: report.Metrics.Succeeded},
	}
	if assessment != nil {
		latencies[0].target = assessment.Thresholds.TTFTMS
		latencies[1].target = assessment.Thresholds.TPOTMS
		latencies[2].target = assessment.Thresholds.E2EMS
	}
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
			metric.Comparison = "at_most"
			metric.Reference = true
			if metric.Value != nil {
				metric.Status = "not_evaluated"
				if complete && assessment.Status != quicktest.PerformanceSLONotEvaluated {
					metric.Status = compareOverviewLatency(latency.value, latency.target, assessment.LatencyPrecision)
				}
			}
		}
		overview.Metrics = append(overview.Metrics, metric)
	}
	throughput := PerformanceOverviewMetric{
		Key: "successful_qps", Unit: "req/s", Status: "unavailable", Comparison: "none",
	}
	zeroThroughput := report.Metrics.Completed > 0 && report.Metrics.Succeeded == 0 && report.Progress.TotalDurationMS > 0
	if report.Metrics.SuccessfulRequestQPS > 0 || zeroThroughput {
		throughput.Value = floatPointer(report.Metrics.SuccessfulRequestQPS)
		throughput.Status = "observed"
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
	return overview
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
