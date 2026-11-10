import type { TFunction } from "i18next"
import type { ComparisonChannelResult, ComparisonSummary } from "@/features/comparisons/data"
import type { ReportSummary } from "./data"
import { isBenchmarkActive, type BenchmarkRun, type BenchmarkDatasetRun } from "@/features/benchmarks/data"

export type ReportListItem =
  | { id: string; kind: "report"; report: ReportSummary }
  | { id: string; kind: "comparison"; comparison: ComparisonSummary }
  | { id: string; kind: "benchmark"; run: BenchmarkRun; result: BenchmarkDatasetRun }

// Batch records retain their history identity; every report remains selectable.
export function reportListItems(reports: ReportSummary[], comparisons: ComparisonSummary[], benchmarks: BenchmarkRun[] = []): ReportListItem[] {
  const items: ReportListItem[] = [
    ...benchmarks.flatMap(run => run.datasets.filter(result => !isBenchmarkActive(result.status)).map(result => ({ id: result.report_id, kind: "benchmark" as const, run, result }))),
    ...comparisons.map(comparison => ({ id: comparison.id, kind: "comparison" as const, comparison })),
    ...reports.map(report => ({ id: report.id, kind: "report" as const, report })),
  ]
  return items.sort((left, right) => {
    const leftTime = left.kind === "benchmark" ? left.result.finished_at || left.run.created_at : left.kind === "comparison" ? left.comparison.created_at : left.report.generated_at
    const rightTime = right.kind === "benchmark" ? right.result.finished_at || right.run.created_at : right.kind === "comparison" ? right.comparison.created_at : right.report.generated_at
    return Date.parse(rightTime) - Date.parse(leftTime) || right.kind.localeCompare(left.kind) || right.id.localeCompare(left.id)
  })
}

export function channelVerdict(channel: ComparisonChannelResult, t: TFunction<"reports">, report?: ReportSummary) {
  if (!report && !channel.report_ready) return t(`status.${channel.run_status === "starting" ? "queued" : channel.run_status}`)
  const conclusion = report ?? channel
  if (conclusion.passed) return t("protocolDesign.verdict.passed")
  if (conclusion.verdict === "observed" || conclusion.verdict === "not_applicable") return t("protocolDesign.verdict.not_applicable")
  if (conclusion.verdict === "indeterminate") return t("protocolDesign.verdict.indeterminate")
  return t("protocolDesign.verdict.failed")
}
