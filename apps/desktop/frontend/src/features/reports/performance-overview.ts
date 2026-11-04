import { DesktopDataError } from "@/app/data-error"
import { isUUID } from "@/features/catalog/data"
import { translateDesktop as tx } from "@/i18n/runtime"

export const PERFORMANCE_OVERVIEW_KEYS = ["success_rate", "slo_rate", "ttft_p95", "tpot_p95", "e2e_p95", "successful_qps"] as const
export type PerformanceOverviewStatus = "passed" | "failed" | "observed" | "not_evaluated" | "unavailable"
export interface PerformanceOverviewMetric {
  key: typeof PERFORMANCE_OVERVIEW_KEYS[number]
  value: number | null
  unit: "percent" | "ms" | "req/s"
  status: PerformanceOverviewStatus
  target: number | null
  comparison: "at_least" | "at_most" | "none"
  reference: boolean
}
export interface PerformanceOverview {
  status: Exclude<PerformanceOverviewStatus, "unavailable">
  sample_count: number
  passed_count: number
  evaluated_count: number
  observed_count: number
  unevaluated_count: number
  metrics: PerformanceOverviewMetric[]
}

export function parsePerformanceOverviews(value: unknown, owners: string[]): Record<string, PerformanceOverview> | undefined {
  if (value === undefined) return undefined
  if (!record(value)) throw invalidOverview()
  return Object.fromEntries(Object.entries(value).map(([id, overview]) => {
    if (!isUUID(id) || !owners.includes(id) || !record(overview) ||
      !["passed", "failed", "observed", "not_evaluated"].includes(String(overview.status))) throw invalidOverview()
    const countKeys = ["sample_count", "passed_count", "evaluated_count", "observed_count", "unevaluated_count"] as const
    if (!countKeys.every(key => typeof overview[key] === "number" && Number.isSafeInteger(overview[key]) && overview[key] >= 0) ||
      !Array.isArray(overview.metrics) || overview.metrics.length !== PERFORMANCE_OVERVIEW_KEYS.length) throw invalidOverview()
    const metrics = overview.metrics.map((item, index): PerformanceOverviewMetric => {
      if (!record(item) || item.key !== PERFORMANCE_OVERVIEW_KEYS[index] ||
        !["passed", "failed", "observed", "not_evaluated", "unavailable"].includes(String(item.status)) ||
        !["at_least", "at_most", "none"].includes(String(item.comparison)) || typeof item.reference !== "boolean" ||
        !nullableNumber(item.value) || !nullableNumber(item.target)) throw invalidOverview()
      const unit = index < 2 ? "percent" : index < 5 ? "ms" : "req/s"
      if (item.unit !== unit || (item.comparison === "none") !== (item.target === null) ||
        ((item.status === "passed" || item.status === "failed") && (item.value === null || item.target === null)) ||
        (item.status === "unavailable" && item.value !== null) ||
        (item.reference && (item.unit !== "ms" || item.target === null)) ||
        (unit === "percent" && ((item.value !== null && item.value > 100) || (item.target !== null && item.target > 100)))) throw invalidOverview()
      return { key: PERFORMANCE_OVERVIEW_KEYS[index], value: item.value, unit,
        status: item.status as PerformanceOverviewStatus, target: item.target,
        comparison: item.comparison as PerformanceOverviewMetric["comparison"], reference: item.reference }
    })
    const passed = metrics.filter(metric => metric.status === "passed").length
    const failed = metrics.filter(metric => metric.status === "failed").length
    const observed = metrics.filter(metric => metric.status === "observed").length
    if (overview.passed_count !== passed || overview.evaluated_count !== passed + failed || overview.observed_count !== observed ||
      overview.unevaluated_count !== metrics.length - passed - failed - observed ||
      (overview.status === "passed" && (failed > 0 || overview.unevaluated_count > 0)) ||
      (overview.status === "failed" && failed === 0)) throw invalidOverview()
    return [id, { status: overview.status as PerformanceOverview["status"], sample_count: overview.sample_count as number,
      passed_count: overview.passed_count as number, evaluated_count: overview.evaluated_count as number, observed_count: overview.observed_count as number,
      unevaluated_count: overview.unevaluated_count as number, metrics }]
  }))
}

function record(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value) }
function nullableNumber(value: unknown): value is number | null { return value === null || (typeof value === "number" && Number.isFinite(value) && value >= 0) }
function invalidOverview() { return new DesktopDataError(tx("desktop:reports_invalid_desktop_report_data_structure")) }
