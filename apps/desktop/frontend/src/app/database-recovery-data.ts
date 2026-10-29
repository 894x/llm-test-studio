import { DesktopDataError } from "./data-error"
import { STARTUP_FAILURE_CODES, type StartupFailureCode } from "./startup-progress"

export type DatabaseRecoveryResult = { schema_version: 1; status: "completed" | "failed"; workspace_ready: boolean; failure_code?: StartupFailureCode; reopen_required?: boolean }

export function parseDatabaseRecoveryResult(value: unknown): DatabaseRecoveryResult {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new DesktopDataError("Invalid database recovery result")
  const item = value as Record<string, unknown>
  const completed = item.status === "completed" && item.failure_code === undefined
  const failed = item.status === "failed" && item.workspace_ready === false && STARTUP_FAILURE_CODES.includes(item.failure_code as StartupFailureCode)
  const validReopen = item.reopen_required === undefined || item.reopen_required === false || (item.reopen_required === true && failed)
  const keys = ["schema_version", "status", "workspace_ready", "failure_code", "reopen_required"]
  if (item.schema_version !== 1 || typeof item.workspace_ready !== "boolean" || (!completed && !failed) || !validReopen || Object.keys(item).some(key => !keys.includes(key))) {
    throw new DesktopDataError("Invalid database recovery result")
  }
  return item as DatabaseRecoveryResult
}
