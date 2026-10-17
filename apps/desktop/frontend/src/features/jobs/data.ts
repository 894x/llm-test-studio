import { DesktopDataError } from "@/app/data-error"
import { parseExecutionLocation, type ExecutionLocation } from "@/features/remote-nodes/data"
import { parseQuickPerformanceProgress, parseQuickPerformanceObservationReport, type QuickPerformanceCommand, type QuickPerformanceProgress, type QuickPerformanceReport } from "@/features/quick-test/data"
import type { StartQuickTaskCommand, StartRunTargetCommand } from "@/features/runs/data"
import type { StartComparisonCommand } from "@/features/comparisons/data"

export type LaunchPayload =
  | { kind: "plan"; plan: StartRunTargetCommand; quick_task: null; performance: null; comparison: null }
  | { kind: "quick_task"; plan: null; quick_task: StartQuickTaskCommand; performance: null; comparison: null }
  | { kind: "performance"; plan: null; quick_task: null; performance: QuickPerformanceCommand; comparison: null }
  | { kind: "comparison"; plan: null; quick_task: null; performance: null; comparison: StartComparisonCommand }
export type LaunchCommand = LaunchPayload & { location: ExecutionLocation }
export type JobKind = "run" | "performance" | "comparison"
export type JobStatus = "queued" | "starting" | "running" | "draining" | "completed" | "failed" | "cancelled"
export type Observation = "" | "unreachable" | "not_found" | "identity_changed" | "rejected"
export type ExecutorView = { node_id: string; installation_id: string; version: string; build_id: string; os: string; arch: string }
export type ExecutionView = { status: JobStatus; revision: number; latest_sequence: number; accepted_at: string; updated_at: string; failure: { phase: string; error_code: string } | null }
export type ConfirmationView = { status: JobStatus; revision: number; accepted_at: string }
export type SubmissionView = {
  delivery_revision: number; delivery: "prepared" | "pending" | "accepted"; observation: Observation
  prepared_at: string; delivery_updated_at: string; confirmation: ConfirmationView | null
}
export type JobView = {
  schema_version: 1; job_id: string; kind: JobKind; location: ExecutionLocation; executor: ExecutorView
  submission: SubmissionView | null; run_ids: string[]; report_ids: string[]; comparison_id: string
  execution: ExecutionView | null
  progress: { sequence: number; recorded_at: string; progress: QuickPerformanceProgress } | null
  performance_result_ready: boolean
}
export type JobPage = { schema_version: 1; jobs: JobView[]; next_cursor: string; has_more: boolean }
export type ControlInput = { job_id: string; kind: "stop_sending" | "cancel"; expected_revision: number }
export type ControlView = {
  schema_version: 1; command_id: string; job_id: string; kind: ControlInput["kind"]
  expected_revision: number; delivery_revision: number; state: "pending" | "confirmed" | "rejected"; observation: Observation
  created_at: string; updated_at: string; confirmed_at: string | null; execution: ExecutionView | null
}
export type ControlPageView = { schema_version: 1; job_id: string; commands: ControlView[]; next_cursor: string; has_more: boolean }
export type PerformanceResultView = {
  schema_version: 1; job_id: string; report_id: string; location: ExecutionLocation; executor: ExecutorView
  terminal: ExecutionView; sealed_at: string; execution_started: boolean
  observation_state: "not_started" | "interrupted" | "available"
  observation: { revision: number; recorded_at: string; stage: "preparing" | "warmup" | "ramp" | "measured" | "capacity"; boundary: "entered" | "completed" | "returned"; rung_index: number | null; report: QuickPerformanceReport } | null
}

function invalid(): never { throw new DesktopDataError("Invalid current job data") }
function record(value: unknown, required: string[], optional: string[] = []): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid()
  const item = value as Record<string, unknown>
  if (required.some(key => !Object.hasOwn(item, key)) || Object.keys(item).some(key => !required.includes(key) && !optional.includes(key))) return invalid()
  return item
}
function text(value: unknown, empty = false, max = 256): string {
  if (typeof value !== "string" || (!empty && !value) || value.trim() !== value || new TextEncoder().encode(value).length > max || /[\p{Cc}\p{Cs}]/u.test(value)) return invalid()
  return value
}
function uuid(value: unknown): string { const result = text(value); return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(result) ? result : invalid() }
function integer(value: unknown, min = 0): number { return typeof value === "number" && Number.isSafeInteger(value) && value >= min ? value : invalid() }
function bool(value: unknown): boolean { return typeof value === "boolean" ? value : invalid() }
function choice<T extends string>(value: unknown, values: readonly T[]): T { return values.includes(value as T) ? value as T : invalid() }
function time(value: unknown): string {
  const result = text(value)
  if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(result) || !Number.isFinite(Date.parse(result))) return invalid()
  return new Date(result).toISOString().slice(0, 19) === result.slice(0, 19) ? result : invalid()
}
function nullable<T>(value: unknown, parse: (value: unknown) => T): T | null { return value === null ? null : parse(value) }
function ids(value: unknown): string[] { if (!Array.isArray(value) || value.length > 128) return invalid(); const result = value.map(uuid); return new Set(result).size === result.length ? result : invalid() }
function schema(item: Record<string, unknown>): void { if (item.schema_version !== 1) invalid() }
const statuses = ["queued", "starting", "running", "draining", "completed", "failed", "cancelled"] as const
const observations = ["", "unreachable", "not_found", "identity_changed", "rejected"] as const
function executor(value: unknown): ExecutorView {
  const item = record(value, ["node_id", "installation_id", "version", "build_id", "os", "arch"])
  const buildID = text(item.build_id)
  if (!/^[0-9a-f]{64}$/.test(buildID)) return invalid()
  return { node_id: uuid(item.node_id), installation_id: uuid(item.installation_id), version: text(item.version, false, 128), build_id: buildID, os: text(item.os, false, 32), arch: text(item.arch, false, 32) }
}
function execution(value: unknown): ExecutionView {
  const item = record(value, ["status", "revision", "latest_sequence", "accepted_at", "updated_at", "failure"])
  const failure = nullable(item.failure, value => { const f = record(value, ["phase", "error_code"]); return { phase: safeCode(f.phase), error_code: safeCode(f.error_code) } })
  const result = { status: choice(item.status, statuses), revision: integer(item.revision, 1), latest_sequence: integer(item.latest_sequence, 1), accepted_at: time(item.accepted_at), updated_at: time(item.updated_at), failure }
  if (Date.parse(result.updated_at) < Date.parse(result.accepted_at) || (failure !== null && result.status !== "failed")) return invalid()
  return result
}
function safeCode(value: unknown): string { const result = text(value, false, 128); return /^[a-z][a-z0-9_]*$/.test(result) ? result : invalid() }
function confirmation(value: unknown): ConfirmationView {
  const item = record(value, ["status", "revision", "accepted_at"])
  return { status: choice(item.status, statuses), revision: integer(item.revision, 1), accepted_at: time(item.accepted_at) }
}
function submission(value: unknown): SubmissionView {
  const item = record(value, ["delivery_revision", "delivery", "observation", "prepared_at", "delivery_updated_at", "confirmation"])
  const result: SubmissionView = {
    delivery_revision: integer(item.delivery_revision, 1), delivery: choice(item.delivery, ["prepared", "pending", "accepted"]),
    observation: choice(item.observation, observations), prepared_at: time(item.prepared_at),
    delivery_updated_at: time(item.delivery_updated_at), confirmation: nullable(item.confirmation, confirmation),
  }
  if ((result.delivery === "accepted") !== (result.confirmation !== null)) return invalid()
  return result
}
const progressKeys = ["phase", "planned", "offered", "launched", "completed", "in_flight", "peak_in_flight", "succeeded", "failed", "rejected", "stopped", "capped", "capacity_rung_number", "capacity_rung_count", "capacity_target", "send_duration_ms", "drain_duration_ms", "total_duration_ms"]
function progress(value: unknown): QuickPerformanceProgress {
  record(value, ["phase", "planned", "launched", "completed", "peak_in_flight", "succeeded", "failed", "rejected", "send_duration_ms", "drain_duration_ms", "total_duration_ms"], progressKeys)
  return parseQuickPerformanceProgress(value)
}
export function parseJobView(value: unknown): JobView {
  const item = record(value, ["schema_version", "job_id", "kind", "location", "executor", "submission", "run_ids", "report_ids", "comparison_id", "execution", "progress", "performance_result_ready"])
  schema(item)
  const result: JobView = { schema_version: 1, job_id: uuid(item.job_id), kind: choice(item.kind, ["run", "performance", "comparison"]), location: parseExecutionLocation(item.location), executor: executor(item.executor), submission: nullable(item.submission, submission), run_ids: ids(item.run_ids), report_ids: ids(item.report_ids), comparison_id: item.comparison_id === "" ? "" : uuid(item.comparison_id), execution: nullable(item.execution, execution), progress: nullable(item.progress, value => { const p = record(value, ["sequence", "recorded_at", "progress"]); return { sequence: integer(p.sequence, 2), recorded_at: time(p.recorded_at), progress: progress(p.progress) } }), performance_result_ready: bool(item.performance_result_ready) }
  if ((!result.submission && (!result.execution || result.location.kind !== "remote")) ||
      (result.submission?.delivery === "prepared" && result.execution !== null) ||
      (result.performance_result_ready && (result.kind !== "performance" || !result.execution || !isJobTerminal(result.execution.status))) ||
      (result.progress && (!result.execution || result.kind !== "performance" || result.progress.sequence > result.execution.latest_sequence))) return invalid()
  return result
}
function pagination(item: Record<string, unknown>): { next_cursor: string; has_more: boolean } {
  const cursor = item.next_cursor === "" ? "" : uuid(item.next_cursor)
  const hasMore = bool(item.has_more)
  // Final and exhausted keyset pages retain their continuation UUID.
  if (hasMore && cursor === "") return invalid()
  return { next_cursor: cursor, has_more: hasMore }
}
export function parseJobPage(value: unknown): JobPage {
  const item = record(value, ["schema_version", "jobs", "next_cursor", "has_more"]); schema(item)
  if (!Array.isArray(item.jobs) || item.jobs.length > 25) return invalid()
  const jobs = item.jobs.map(parseJobView)
  if (new Set(jobs.map(job => job.job_id)).size !== jobs.length) return invalid()
  return { schema_version: 1, jobs, ...pagination(item) }
}
export function parseControlView(value: unknown): ControlView {
  const item = record(value, ["schema_version", "command_id", "job_id", "kind", "expected_revision", "delivery_revision", "state", "observation", "created_at", "updated_at", "confirmed_at", "execution"]); schema(item)
  const result: ControlView = { schema_version: 1, command_id: uuid(item.command_id), job_id: uuid(item.job_id), kind: choice(item.kind, ["stop_sending", "cancel"]), expected_revision: integer(item.expected_revision, 1), delivery_revision: integer(item.delivery_revision, 1), state: choice(item.state, ["pending", "confirmed", "rejected"]), observation: choice(item.observation, observations), created_at: time(item.created_at), updated_at: time(item.updated_at), confirmed_at: nullable(item.confirmed_at, time), execution: nullable(item.execution, execution) }
  if ((result.state === "confirmed") !== (result.confirmed_at !== null && result.execution !== null) || (result.state !== "confirmed" && (result.confirmed_at !== null || result.execution !== null)) || (result.state === "rejected" && result.observation !== "rejected")) return invalid()
  return result
}
export function parseControlPage(value: unknown): ControlPageView {
  const item = record(value, ["schema_version", "job_id", "commands", "next_cursor", "has_more"]); schema(item)
  const jobID = uuid(item.job_id)
  if (!Array.isArray(item.commands) || item.commands.length > 25) return invalid()
  const commands = item.commands.map(parseControlView)
  if (commands.some(command => command.job_id !== jobID) || new Set(commands.map(command => command.command_id)).size !== commands.length) return invalid()
  return { schema_version: 1, job_id: jobID, commands, ...pagination(item) }
}
export function parsePerformanceResult(value: unknown): PerformanceResultView {
  const item = record(value, ["schema_version", "job_id", "report_id", "location", "executor", "terminal", "sealed_at", "execution_started", "observation_state", "observation"]); schema(item)
  const observation = nullable(item.observation, value => {
    const o = record(value, ["revision", "recorded_at", "stage", "boundary", "rung_index", "report"])
    return { revision: integer(o.revision, 1), recorded_at: time(o.recorded_at), stage: choice(o.stage, ["preparing", "warmup", "ramp", "measured", "capacity"] as const), boundary: choice(o.boundary, ["entered", "completed", "returned"] as const), rung_index: nullable(o.rung_index, integer), report: parseQuickPerformanceObservationReport(o.report) }
  })
  const result: PerformanceResultView = { schema_version: 1, job_id: uuid(item.job_id), report_id: uuid(item.report_id), location: parseExecutionLocation(item.location), executor: executor(item.executor), terminal: execution(item.terminal), sealed_at: time(item.sealed_at), execution_started: bool(item.execution_started), observation_state: choice(item.observation_state, ["not_started", "interrupted", "available"]), observation }
  const returned = observation?.boundary === "returned"
  const origin = observation?.report.execution_origin
  if (origin && (origin.kind !== result.location.kind || origin.node_id !== result.executor.node_id ||
      origin.installation_id !== result.executor.installation_id || origin.engine_build_id !== result.executor.build_id ||
      origin.runner_version !== result.executor.version || origin.os !== result.executor.os || origin.arch !== result.executor.arch)) return invalid()
  if (!isJobTerminal(result.terminal.status) || Date.parse(result.sealed_at) < Date.parse(result.terminal.updated_at) ||
      (result.observation_state === "not_started" && (result.execution_started || observation !== null)) ||
      (result.observation_state === "available" && (!result.execution_started || !returned)) ||
      (result.observation_state === "interrupted" && (!result.execution_started || returned)) ||
      (result.terminal.status === "completed" && !returned) ||
      (observation && ((observation.stage === "capacity") !== (observation.rung_index !== null) ||
        observation.report.report_id !== result.report_id || Date.parse(observation.recorded_at) < Date.parse(result.terminal.accepted_at) ||
        Date.parse(observation.recorded_at) > Date.parse(result.sealed_at)))) return invalid()
  return result
}
export function isJobTerminal(status: JobStatus): boolean { return status === "completed" || status === "failed" || status === "cancelled" }
