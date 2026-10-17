import { DesktopDataError } from "@/app/data-error"
import { isUUID } from "@/features/catalog/data"

export type ExecutionOrigin = {
  kind: "local" | "remote"
  node_id: string
  installation_id: string
  engine_build_id: string
  runner_version: string
  os: string
  arch: string
}

const ORIGIN_FIELDS = ["kind", "node_id", "installation_id", "engine_build_id", "runner_version", "os", "arch"]

function invalidOrigin(): never {
  throw new DesktopDataError("Invalid execution_origin. Recreate the report with the current application; an unrecorded origin must be explicit null.")
}

function label(value: unknown, maxBytes: number): string {
  if (typeof value !== "string" || !value || value.trim() !== value ||
    new TextEncoder().encode(value).length > maxBytes || /[\p{Cc}\p{Cs}]/u.test(value)) return invalidOrigin()
  return value
}

// Null records an absent provenance record. Missing data never implies local execution.
export function parseExecutionOrigin(value: unknown): ExecutionOrigin | null {
  if (value === null) return null
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalidOrigin()
  const item = value as Record<string, unknown>
  if (Object.keys(item).length !== ORIGIN_FIELDS.length || ORIGIN_FIELDS.some(key => !Object.hasOwn(item, key))) return invalidOrigin()
  if ((item.kind !== "local" && item.kind !== "remote") || !isUUID(item.node_id) || !isUUID(item.installation_id) ||
    typeof item.engine_build_id !== "string" || !/^[0-9a-f]{64}$/.test(item.engine_build_id)) return invalidOrigin()
  return { kind: item.kind, node_id: item.node_id, installation_id: item.installation_id,
    engine_build_id: item.engine_build_id, runner_version: label(item.runner_version, 128),
    os: label(item.os, 32), arch: label(item.arch, 32) }
}
