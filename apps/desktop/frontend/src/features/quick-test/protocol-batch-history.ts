import type { ComparisonSummary } from "@/features/comparisons/data"
import type { QuickTestHistoryItem } from "./quick-test-history"

const EMPTY_BATCHES: ComparisonSummary[] = []

export function protocolBatchItems(
  comparisons: ComparisonSummary[] = EMPTY_BATCHES,
  mode: "functional" | "plan",
): QuickTestHistoryItem[] {
  return comparisons.flatMap(batch => {
    if (!batch.channels[0]?.protocol || Boolean(batch.channels[0].suite) !== (mode === "functional")) return []
    const active = batch.status === "running"
    const ready = batch.channels.some(channel => channel.report_ready)
    return [{
      id: batch.id, comparisonID: batch.id, jobID: batch.job_id, mode,
      title: batch.plan_name, model: batch.model_name, channel: batch.channels[0].channel_name,
      timestamp: batch.created_at, status: batch.status, active, waitingForReport: !active && !ready,
      reportID: ready ? batch.id : undefined,
      protocols: batch.channels.flatMap(channel => channel.protocol
        ? [{ protocol: channel.protocol, status: channel.run_status, progress: channel.case_progress }] : []),
    }]
  })
}
