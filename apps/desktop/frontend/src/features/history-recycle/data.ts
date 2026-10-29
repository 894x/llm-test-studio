import { DesktopDataError } from "@/app/data-error"

export type HistoryRecycleTarget = { kind: "job" | "run" | "report" | "comparison"; id: string }
export type RecycledHistoryEntry = HistoryRecycleTarget & {
  title: string
  removed_at: string
  run_ids: string[]
  report_ids: string[]
  comparison_ids: string[]
  job_ids: string[]
  remote: boolean
}
export type RecycledHistoryPage = { schema_version: 1; entries: RecycledHistoryEntry[]; next_cursor: string; has_more: boolean }
export type HistoryRecyclePreview = {
  schema_version: 1
  entry: RecycledHistoryEntry
  can_recycle: boolean
  blocked_reason: "" | "active" | "unconfirmed"
}
export type HistoryVisibilityChange = { entry: RecycledHistoryEntry; action: "recycle" | "restore" }
export type HistoryChanged = (entry: RecycledHistoryEntry, action: HistoryVisibilityChange["action"]) => void | Promise<void>

function invalid(): never { throw new DesktopDataError("Invalid history recycle data") }
function record(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid()
  const item = value as Record<string, unknown>
  if (Object.keys(item).length !== keys.length || keys.some(key => !Object.hasOwn(item, key))) return invalid()
  return item
}
function uuid(value: unknown): string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value) ? value : invalid()
}
function ids(value: unknown): string[] {
  if (!Array.isArray(value) || value.length > 10000) return invalid()
  const parsed = value.map(uuid)
  return new Set(parsed).size === parsed.length ? parsed : invalid()
}
function text(value: unknown, empty = false): string {
  return typeof value === "string" && (empty || !!value.trim()) && value.length <= 2048 && !/[\p{Cc}\p{Cs}]/u.test(value) ? value : invalid()
}
function timestamp(value: unknown): string {
  const result = text(value, true)
  if (!result) return result
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(result) || !Number.isFinite(Date.parse(result))) return invalid()
  return new Date(result).toISOString().slice(0, 19) === result.slice(0, 19) ? result : invalid()
}
export function parseHistoryRecycleTarget(value: unknown): HistoryRecycleTarget {
  const item = record(value, ["kind", "id"])
  if (!["job", "run", "report", "comparison"].includes(item.kind as string)) return invalid()
  return { kind: item.kind as HistoryRecycleTarget["kind"], id: uuid(item.id) }
}
export function parseRecycledHistoryEntry(value: unknown): RecycledHistoryEntry {
  const item = record(value, ["kind", "id", "title", "removed_at", "run_ids", "report_ids", "comparison_ids", "job_ids", "remote"])
  if (typeof item.remote !== "boolean") return invalid()
  if (typeof item.title !== "string") return invalid()
  const target = parseHistoryRecycleTarget({ kind: item.kind, id: item.id })
  const result = { ...target, title: item.title, removed_at: timestamp(item.removed_at), run_ids: ids(item.run_ids), report_ids: ids(item.report_ids), comparison_ids: ids(item.comparison_ids), job_ids: ids(item.job_ids), remote: item.remote }
  const ownerIDs = result.kind === "job" ? result.job_ids : result.kind === "comparison" ? result.comparison_ids : result.kind === "run" ? result.run_ids : result.report_ids
  return ownerIDs.includes(result.id) ? result : invalid()
}
export function parseRecycledHistoryPage(value: unknown): RecycledHistoryPage {
  const item = record(value, ["schema_version", "entries", "next_cursor", "has_more"])
  if (item.schema_version !== 1 || !Array.isArray(item.entries) || item.entries.length > 25 || typeof item.has_more !== "boolean") return invalid()
  const entries = item.entries.map(parseRecycledHistoryEntry)
  const next = text(item.next_cursor, true)
  if (entries.some(entry => !entry.removed_at) || new Set(entries.map(entry => `${entry.kind}:${entry.id}`)).size !== entries.length || item.has_more !== !!next) return invalid()
  return { schema_version: 1, entries, next_cursor: next, has_more: item.has_more }
}
export function parseHistoryRecyclePreview(value: unknown): HistoryRecyclePreview {
  const item = record(value, ["schema_version", "entry", "can_recycle", "blocked_reason"])
  if (item.schema_version !== 1 || typeof item.can_recycle !== "boolean" || !["", "active", "unconfirmed"].includes(item.blocked_reason as string) || item.can_recycle !== (item.blocked_reason === "")) return invalid()
  return { schema_version: 1, entry: parseRecycledHistoryEntry(item.entry), can_recycle: item.can_recycle, blocked_reason: item.blocked_reason as HistoryRecyclePreview["blocked_reason"] }
}
