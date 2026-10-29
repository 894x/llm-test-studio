import { useEffect, useState } from "react"
import type { DesktopClient } from "./desktop-client"
import { DesktopDataError } from "./data-error"

export const STARTUP_STAGES = ["opening", "database_checking", "database_initializing", "database_backing_up", "database_migrating", "database_validating", "database_activating", "database_completed", "catalogs", "opening_repository", "recovering", "ready"] as const
export const STARTUP_FAILURE_CODES = ["workspace_busy", "permission_denied", "disk_full", "storage_unavailable", "database_unsupported", "database_corrupt", "backup_failed", "migration_failed", "unknown"] as const
export type StartupFailureCode = typeof STARTUP_FAILURE_CODES[number]
export type StartupProgress = { schema_version: 1; status: "pending" | "running" | "completed" | "failed"; stage: typeof STARTUP_STAGES[number]; from_version: number; to_version: number; elapsed_ms: number; failure_code?: StartupFailureCode; recovery_available?: boolean }
export const READY_STARTUP: StartupProgress = { schema_version: 1, status: "completed", stage: "ready", from_version: 0, to_version: 0, elapsed_ms: 0 }

export function parseStartupProgress(value: unknown): StartupProgress {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new DesktopDataError("Invalid startup progress")
  const item = value as Record<string, unknown>
  const keys = ["schema_version", "status", "stage", "from_version", "to_version", "elapsed_ms", "failure_code", "recovery_available"]
  const validFailure = item.failure_code === undefined || (item.status === "failed" && STARTUP_FAILURE_CODES.includes(item.failure_code as StartupFailureCode))
  const validRecovery = item.recovery_available === undefined || item.recovery_available === false || (item.recovery_available === true && item.status === "failed" && item.failure_code === "database_unsupported")
  if (item.schema_version !== 1 || !["pending", "running", "completed", "failed"].includes(item.status as string) || !STARTUP_STAGES.includes(item.stage as StartupProgress["stage"]) ||
    ![item.from_version, item.to_version, item.elapsed_ms].every(value => Number.isSafeInteger(value) && (value as number) >= 0) ||
    !validFailure || !validRecovery || Object.keys(item).some(key => !keys.includes(key))) throw new DesktopDataError("Invalid startup progress")
  return item as StartupProgress
}

export function useStartupProgress(client: DesktopClient): StartupProgress | null {
  const [progress, setProgress] = useState<StartupProgress | null>(null)
  useEffect(() => {
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      try {
        const next = await client.getStartupProgress()
        if (!active) return
        setProgress(next)
        if (next.status === "completed" || next.status === "failed") return
      } catch { /* Normal workspace reads own startup/transport error presentation. */ }
      if (active) timer = setTimeout(() => void poll(), 400)
    }
    void poll()
    return () => { active = false; clearTimeout(timer) }
  }, [client])
  return progress
}
