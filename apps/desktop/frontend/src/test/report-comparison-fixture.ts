import type { FormalReportDetail } from "@/features/reports/data"
import type { ReportComparison } from "@/features/reports/report-comparison-data"
import { protocolReportFixture, reportID } from "./protocol-report-fixture"

export function reportComparisonFixture(left = protocolReportFixture(), right = protocolReportFixture("failed")): ReportComparison {
  if (left.report.id === right.report.id) {
    right.report.id = reportID(20)
    right.report.run_id = reportID(21)
    right.report.generated_at = "2026-09-10T14:00:00Z"
  }
  const cell = (detail: FormalReportDetail, baseline: boolean) => ({ report_id: detail.report.id,
    entry_id: detail.entries[0].entry_id, case_id: detail.entries[0].cases[0].case_id,
    status: detail.entries[0].cases[0].verification.status, change: baseline ? "baseline" as const : "regressed" as const,
    metrics: [{ key: "e2e_ms", value: 32, unit: "ms", samples: 1, delta: baseline ? null : 0 }] })
  return { schema_version: 1, baseline_id: left.report.id, reports: [left, right], rows: [
    { key: "first", name: "Reject invalid parameter", entry_name: "Snapshot suite", different: true, cells: [cell(left, true), cell(right, false)] },
    { key: "unchanged", name: "Unchanged case", entry_name: "Snapshot suite", different: false, cells: [cell(left, true), { ...cell(right, false), status: "passed", change: "same" }] },
  ] }
}
