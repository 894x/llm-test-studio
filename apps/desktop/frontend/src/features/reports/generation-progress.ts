import { DesktopDataError } from "@/app/data-error"

const phases = ["loading_run", "loading_results", "loading_evidence", "building", "persisting", "ready", "failed"] as const
export type ReportGenerationPhase = typeof phases[number]
export type ReportGenerationProgress = {
  sequence: number
  run_id: string
  phase: ReportGenerationPhase
  processed: number
  total: number
  elapsed_ms: number
  report_id?: string
}
export type ReportGenerationSnapshot = { schema_version: 1; runs: ReportGenerationProgress[] }

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
function uuid(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value)
}
function count(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
}

export function parseReportGenerationProgress(value: unknown): ReportGenerationProgress {
  const keys = ["sequence", "run_id", "phase", "processed", "total", "elapsed_ms", "report_id"]
  if (!record(value) || Object.keys(value).some((key) => !keys.includes(key)) ||
    !count(value.sequence) || value.sequence < 1 || !uuid(value.run_id) ||
    !phases.includes(value.phase as ReportGenerationPhase) || !count(value.processed) ||
    !count(value.total) || value.processed > value.total || !count(value.elapsed_ms) ||
    (value.phase === "ready" ? !uuid(value.report_id) : value.report_id !== undefined)) {
    throw new DesktopDataError("Invalid report generation progress")
  }
  return { ...value } as ReportGenerationProgress
}

export function parseReportGenerationSnapshot(value: unknown): ReportGenerationSnapshot {
  if (!record(value) || value.schema_version !== 1 || !Array.isArray(value.runs) ||
    Object.keys(value).some((key) => key !== "schema_version" && key !== "runs")) {
    throw new DesktopDataError("Invalid report generation snapshot")
  }
  const runs = value.runs.map(parseReportGenerationProgress)
  if (new Set(runs.map((run) => run.run_id)).size !== runs.length) {
    throw new DesktopDataError("Duplicate report generation run")
  }
  return { schema_version: 1, runs }
}
