import type { WorkspaceRun } from "@/features/runs/data"
import type { ReportSnapshot } from "@/features/reports/data"

export type QuickHistoryPage = {
  schema_version: 1
  runs: WorkspaceRun[]
  reports: ReportSnapshot["reports"]
  next_cursor: string
}
