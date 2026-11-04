import type { PerformanceOverview } from "@/features/reports/performance-overview"

/** Presentation-only example; production projections are provided by Go. */
export function performanceOverviewFixture(): PerformanceOverview {
  return { status: "failed", sample_count: 100, passed_count: 3, evaluated_count: 6, observed_count: 0, unevaluated_count: 0,
    rating: { method: "target_ratio_v1", score: 4.38, max_score: 5, metric_count: 6 },
    metrics: [
      { key: "success_rate", value: 100, unit: "percent", status: "passed", target: 99, comparison: "at_least", reference: true },
      { key: "slo_rate", value: 94, unit: "percent", status: "failed", target: 95, comparison: "at_least", reference: false },
      { key: "ttft_p95", value: 9290, unit: "ms", status: "passed", target: 15000, comparison: "less_than", reference: true },
      { key: "tpot_p95", value: 42.6, unit: "ms", status: "failed", target: 40, comparison: "less_than", reference: true },
      { key: "e2e_p95", value: 23230, unit: "ms", status: "passed", target: 30000, comparison: "less_than", reference: true },
      { key: "successful_qps", value: 0.65, unit: "req/s", status: "failed", target: 2, comparison: "at_least", reference: true },
    ] }
}
