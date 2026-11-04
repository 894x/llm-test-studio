import type { PerformanceOverview } from "@/features/reports/performance-overview"

/** Presentation-only example; production projections are provided by Go. */
export function performanceOverviewFixture(): PerformanceOverview {
  return { status: "failed", sample_count: 100, passed_count: 3, evaluated_count: 5, observed_count: 1, unevaluated_count: 0,
    metrics: [
      { key: "success_rate", value: 100, unit: "percent", status: "passed", target: 100, comparison: "at_least", reference: false },
      { key: "slo_rate", value: 94, unit: "percent", status: "failed", target: 95, comparison: "at_least", reference: false },
      { key: "ttft_p95", value: 9290, unit: "ms", status: "passed", target: 15000, comparison: "at_most", reference: true },
      { key: "tpot_p95", value: 42.6, unit: "ms", status: "failed", target: 40, comparison: "at_most", reference: true },
      { key: "e2e_p95", value: 23230, unit: "ms", status: "passed", target: 30000, comparison: "at_most", reference: true },
      { key: "successful_qps", value: 0.65, unit: "req/s", status: "observed", target: null, comparison: "none", reference: false },
    ] }
}
