import type { TFunction } from "i18next"
import type { ComparisonChannelResult, ComparisonSummary } from "@/features/comparisons/data"
import type { ReportSummary } from "./data"

export type ReportListItem =
  | { id: string; kind: "report"; report: ReportSummary }
  | { id: string; kind: "comparison"; comparison: ComparisonSummary }

// Comparisons already persist their run references; group those reports for display.
export function reportListItems(reports: ReportSummary[], comparisons: ComparisonSummary[]): ReportListItem[] {
  const comparedRuns = new Set(comparisons.flatMap(item => item.channels.map(channel => channel.run_id)))
  return [
    ...comparisons.map(comparison => ({ id: comparison.id, kind: "comparison" as const, comparison })),
    ...reports.filter(report => !report.run_id || !comparedRuns.has(report.run_id))
      .map(report => ({ id: report.id, kind: "report" as const, report })),
  ]
}

export function channelVerdict(channel: ComparisonChannelResult, t: TFunction<"reports">, report?: ReportSummary) {
  if (!report && !channel.report_ready) return t(`common:status.${channel.run_status === "starting" ? "queued" : channel.run_status}`)
  const conclusion = report ?? channel
  if (conclusion.passed) return t("protocolDesign.verdict.passed")
  if (conclusion.verdict === "observed" || conclusion.verdict === "not_applicable") return t("protocolDesign.verdict.not_applicable")
  if (conclusion.verdict === "indeterminate") return t("protocolDesign.verdict.indeterminate")
  return t("protocolDesign.verdict.failed")
}
