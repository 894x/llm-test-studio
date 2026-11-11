import { DesktopDataError } from "@/app/data-error"
import { isUUID } from "@/features/catalog/data"
import { parseReportDetail, type ReportDetail } from "./data"

export const MAX_COMPARISON_REPORTS = 16
export type ComparisonChange = "baseline" | "same" | "regressed" | "recovered" | "changed" | "conditions_changed" | "added" | "missing"
export interface ReportComparisonMetric { key: string; value: number; unit: string; samples: number; delta: number | null }
export interface ReportComparisonCell {
  report_id: string; entry_id: string; case_id: string; status: string; change: ComparisonChange; metrics: ReportComparisonMetric[]
}
export interface ReportComparisonRow {
  key: string; name: string; entry_name: string; different: boolean; cells: ReportComparisonCell[]
}
export interface ReportComparison {
  schema_version: 1; baseline_id: string; reports: ReportDetail[]; rows: ReportComparisonRow[]
}
export type CompareReports = (reportIDs: string[], baselineID: string) => Promise<ReportComparison>

export function comparisonReportID(detail: ReportDetail) {
  return detail.source === "run" ? detail.report.id : detail.performance.report_id!
}

export function parseReportComparison(value: unknown): ReportComparison {
  const invalid = () => new DesktopDataError("Invalid report comparison")
  if (!record(value) || value.schema_version !== 1 || !isUUID(value.baseline_id) || !Array.isArray(value.reports) ||
    value.reports.length < 2 || value.reports.length > MAX_COMPARISON_REPORTS || !Array.isArray(value.rows)) throw invalid()
  const reports = value.reports.map(parseReportDetail)
  const ids = reports.map(comparisonReportID)
  if (new Set(ids).size !== ids.length || !ids.includes(value.baseline_id)) throw invalid()
  const rows = value.rows.map((row): ReportComparisonRow => {
    if (!record(row) || typeof row.key !== "string" || !row.key || typeof row.name !== "string" ||
      typeof row.entry_name !== "string" || typeof row.different !== "boolean" || !Array.isArray(row.cells) || row.cells.length !== reports.length) throw invalid()
    const cells = row.cells.map((cell, index): ReportComparisonCell => {
      if (!record(cell) || cell.report_id !== ids[index] || typeof cell.entry_id !== "string" || typeof cell.case_id !== "string" ||
        typeof cell.status !== "string" || !["passed", "failed", "not_applicable", "indeterminate", "not_run"].includes(cell.status) ||
        typeof cell.change !== "string" || !["baseline", "same", "regressed", "recovered", "changed", "conditions_changed", "added", "missing"].includes(cell.change) || !Array.isArray(cell.metrics)) throw invalid()
      if ((cell.change === "baseline") !== (cell.report_id === value.baseline_id && cell.change !== "missing")) throw invalid()
      const detail = reports[index]
      if (cell.change !== "missing" && detail.source === "run") {
        const entry = detail.entries.find(entry => entry.entry_id === cell.entry_id)
        if (!entry || (cell.case_id && !entry.cases.some(item => item.case_id === cell.case_id))) throw invalid()
        if (!cell.case_id && !entry.performance) throw invalid()
      }
      if (detail.source === "quick_performance" && (cell.entry_id || cell.case_id)) throw invalid()
      const metrics = cell.metrics.map((metric): ReportComparisonMetric => {
        if (!record(metric) || typeof metric.key !== "string" || !metric.key || typeof metric.unit !== "string" || !metric.unit ||
          !finite(metric.value) || !Number.isSafeInteger(metric.samples) || Number(metric.samples) < 1 ||
          (metric.delta !== null && !finite(metric.delta))) throw invalid()
        return { key: metric.key, unit: metric.unit, value: metric.value, samples: metric.samples as number, delta: metric.delta as number | null }
      })
      if (new Set(metrics.map(metric => metric.key)).size !== metrics.length) throw invalid()
      if ((cell.change === "conditions_changed" || cell.change === "baseline" || cell.change === "added") && metrics.some(metric => metric.delta !== null)) throw invalid()
      if (cell.change === "missing" && (cell.status !== "not_run" || cell.entry_id || cell.case_id || metrics.length)) throw invalid()
      return { report_id: cell.report_id as string, entry_id: cell.entry_id, case_id: cell.case_id,
        status: cell.status as string, change: cell.change as ComparisonChange, metrics }
    })
    return { key: row.key, name: row.name, entry_name: row.entry_name, different: row.different, cells }
  })
  if (new Set(rows.map(row => row.key)).size !== rows.length) throw invalid()
  return { schema_version: 1, baseline_id: value.baseline_id, reports, rows }
}

function record(value: unknown): value is Record<string, unknown> { return typeof value === "object" && value !== null && !Array.isArray(value) }
function finite(value: unknown): value is number { return typeof value === "number" && Number.isFinite(value) }
