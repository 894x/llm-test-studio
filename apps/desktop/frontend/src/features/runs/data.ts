import { translateDesktop as tx } from "@/i18n/runtime"
export type CoreRunStatus =
  | "queued"
  | "starting"
  | "running"
  | "draining"
  | "completed"
  | "failed"
  | "cancelled"

export function isRunActive(status: CoreRunStatus): boolean {
  return status === "queued" || status === "starting" || status === "running" || status === "draining"
}

export interface StartRunTargetCommand {
  plan_id: string
  model_id: string
  channel_id: string
}

export interface StartQuickTaskCommand {
  source_run_id?: string
  credential_run_id?: string
  suite_id: string
  suite_revision: number
  model: string
  channel_id?: string
  base_url?: string
  api_key?: string
  inputs: Record<string, string | number | boolean>
}

export type WorkspacePlan = {
  id: string
  revision: number
  name: string
  case_count: number
  run_count: number
  load_mode: "single" | "fixed_concurrency" | "open_loop"
  concurrency: number
  request_count: number
  rate_per_second: number
  duration_ms: number
  request_timeout_ms: number
}

export type WorkspaceRun = {
  source?: "quick_task"
  id: string
  revision: number
  plan_id: string
  plan_revision: number
  plan_name: string
  status: CoreRunStatus
  conclusion: "none" | "passed" | "failed"
  failure_phase?: string
  error_code?: string
  model_id: string
  model_revision: number
  model_name: string
  channel_id: string
  channel_revision: number
  channel_name: string
  load_mode: WorkspacePlan["load_mode"]
  concurrency: number
  rate_per_second: number
  planned: number
  duration_ms: number
  completed: number
  passed: number
  failed: number
  artifact_count: number
  started_at: string
  updated_at: string
}

export type WorkspaceSnapshot = {
  schema_version: 1
  plans: WorkspacePlan[]
  runs: WorkspaceRun[]
  active_run_id?: string
}

export type RunStatus =
  | "running"
  | "draining"
  | "passed"
  | "completed"
  | "failed"
  | "queued"
  | "cancelled"

export type TestPlan = {
  id: string
  name: string
  description: string
  caseCount: number
  runCount: number
}

export type RunRecord = {
  quickTask?: boolean
  id: string
  title: string
  planId: string
  planRevision: string
  model: string
  modelRevision: string
  channel: string
  channelRevision: string
  status: RunStatus
  coreStatus: CoreRunStatus
  completed: number
  total: number
  targetDurationMS: number
  passed: number
  p95: string
  started: string
  duration: string
  loadProfile: string
  artifactCount: number
  failureSummary?: string
}

export type RunTranslator = (
  key: string,
  values?: Record<string, string | number>,
) => string

export type RunPresentationOptions = {
  locale?: string
  t?: RunTranslator
}

export function presentWorkspace(
  snapshot: WorkspaceSnapshot,
  options: RunPresentationOptions = {},
): {
  plans: TestPlan[]
  runs: RunRecord[]
} {
  return {
    plans: snapshot.plans.map((item) => ({
      id: item.id,
      name: item.name,
      description: describeLoad(item, options.t ?? defaultRunTranslator),
      caseCount: item.case_count,
      runCount: item.run_count,
    })),
    runs: snapshot.runs.map((item) => ({
      quickTask: item.source === "quick_task",
      id: item.id,
      title: item.plan_name,
      planId: item.plan_id,
      planRevision: `r${item.plan_revision}`,
      model: item.model_name,
      modelRevision: `r${item.model_revision}`,
      channel: item.channel_name,
      channelRevision: `r${item.channel_revision}`,
      status: presentStatus(item),
      coreStatus: item.status,
      completed: item.completed,
      total: item.planned,
      targetDurationMS: item.duration_ms,
      passed: item.passed,
      p95: "—",
      started: formatStartedAt(item.started_at, options.locale ?? "zh-CN"),
      duration: formatDuration(item),
      loadProfile: describeRunLoad(item, options.t ?? defaultRunTranslator),
      artifactCount: item.artifact_count,
      failureSummary:
        item.error_code
          ? `${item.failure_phase ?? "run"} · ${item.error_code}`
          : item.failed > 0
          ? (options.t ?? defaultRunTranslator)("presentation.failedRequests", { count: item.failed })
          : item.conclusion === "failed"
            ? (options.t ?? defaultRunTranslator)("presentation.failedConclusion")
            : undefined,
    })),
  }
}

function describeLoad(plan: WorkspacePlan, t: RunTranslator): string {
  if (plan.request_count === 0 && plan.duration_ms > 0) {
    const rate = plan.load_mode === "open_loop" ? `${plan.rate_per_second} RPS · ` : ""
    return t("presentation.continuous", { rate, duration: formatTargetDuration(plan.duration_ms) })
  }
  if (plan.load_mode === "open_loop") {
    return t("presentation.openLoopRequests", { rate: plan.rate_per_second, count: plan.request_count })
  }
  if (plan.load_mode === "single") return t("presentation.single")
  return t("presentation.fixedConcurrency", { concurrency: plan.concurrency, count: plan.request_count })
}

function describeRunLoad(run: WorkspaceRun, t: RunTranslator): string {
  if (run.source === "quick_task") return t("presentation.quickSuite")
  if (run.planned === 0 && run.duration_ms > 0) {
    const rate = run.load_mode === "open_loop" ? `${run.rate_per_second} RPS · ` : ""
    return t("presentation.continuous", { rate, duration: formatTargetDuration(run.duration_ms) })
  }
  if (run.load_mode === "open_loop") return t("presentation.openLoop", { rate: run.rate_per_second })
  if (run.load_mode === "single") return t("presentation.single")
  return t("presentation.fixedTotal", { concurrency: run.concurrency })
}

function presentStatus(run: WorkspaceRun): RunStatus {
  if (run.status === "running") return "running"
  if (run.status === "draining") return "draining"
  if (run.status === "queued" || run.status === "starting") return "queued"
  if (run.status === "cancelled") return "cancelled"
  if (run.status === "failed") return "failed"
  if (run.status === "completed") {
    if (run.conclusion === "passed") return "passed"
    if (run.conclusion === "failed") return "failed"
    return "completed"
  }
  return "cancelled"
}

export function formatTargetDuration(durationMS: number): string {
  const totalSeconds = Math.max(0, Math.ceil(durationMS / 1_000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
}

function formatStartedAt(value: string, locale: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "—"
  return new Intl.DateTimeFormat(locale, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date)
}

const DEFAULT_RUN_MESSAGES: Record<string, string> = {
  "presentation.quickSuite": "runs:presentation.quickSuite",
  "presentation.failedRequests": "desktop:runs_value_requests_failed_the_full_success_criteria",
  "presentation.failedConclusion": "desktop:runs_the_run_did_not_pass_check_the_report_sla_and",
  "presentation.continuous": "desktop:runs_value_for_value",
  "presentation.openLoopRequests": "desktop:runs_value_rps_value_requests",
  "presentation.single": "desktop:runs_single_request_check",
  "presentation.fixedConcurrency": "desktop:runs_value_concurrency_value_requests",
  "presentation.openLoop": "desktop:runs_open_model_value_rps",
  "presentation.fixedTotal": "desktop:runs_fixed_count_value_concurrency",
}

function defaultRunTranslator(
  key: string,
  values: Record<string, string | number> = {},
): string {
  return Object.entries(values).reduce(
    (message, [name, value]) => message.replaceAll(`{{${name}}}`, String(value)),
    tx(DEFAULT_RUN_MESSAGES[key] ?? key, values),
  )
}

function formatDuration(run: WorkspaceRun): string {
  if (["queued", "starting", "running", "draining"].includes(run.status)) return "—"
  const elapsed = new Date(run.updated_at).getTime() - new Date(run.started_at).getTime()
  if (!Number.isFinite(elapsed) || elapsed < 0) return "—"
  const totalSeconds = Math.floor(elapsed / 1_000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
}
