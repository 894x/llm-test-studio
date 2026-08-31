export type CoreRunStatus =
  | "queued"
  | "starting"
  | "running"
  | "draining"
  | "completed"
  | "failed"
  | "cancelled"

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

export const STATUS_LABELS: Record<RunStatus, string> = {
  running: "运行中",
  draining: "排空中",
  passed: "通过",
  completed: "已完成",
  failed: "失败",
  queued: "排队中",
  cancelled: "已取消",
}

export function presentWorkspace(snapshot: WorkspaceSnapshot): {
  plans: TestPlan[]
  runs: RunRecord[]
} {
  return {
    plans: snapshot.plans.map((item) => ({
      id: item.id,
      name: item.name,
      description: describeLoad(item),
      caseCount: item.case_count,
      runCount: item.run_count,
    })),
    runs: snapshot.runs.map((item) => ({
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
      started: formatStartedAt(item.started_at),
      duration: formatDuration(item),
      loadProfile: describeRunLoad(item),
      artifactCount: item.artifact_count,
      failureSummary:
        item.error_code
          ? `${item.failure_phase ?? "run"} · ${item.error_code}`
          : item.failed > 0
          ? `${item.failed} 个请求未通过完整成功判定。`
          : item.conclusion === "failed"
            ? "运行结论未通过，请检查报告中的 SLA 与汇总。"
            : undefined,
    })),
  }
}

function describeLoad(plan: WorkspacePlan): string {
  if (plan.request_count === 0 && plan.duration_ms > 0) {
    const rate = plan.load_mode === "open_loop" ? `${plan.rate_per_second} RPS · ` : ""
    return `${rate}持续 ${formatTargetDuration(plan.duration_ms)}`
  }
  if (plan.load_mode === "open_loop") {
    return `${plan.rate_per_second} RPS · ${plan.request_count} 请求`
  }
  if (plan.load_mode === "single") return "单请求检查"
  return `${plan.concurrency} 并发 · ${plan.request_count} 请求`
}

function describeRunLoad(run: WorkspaceRun): string {
  if (run.planned === 0 && run.duration_ms > 0) {
    const rate = run.load_mode === "open_loop" ? `${run.rate_per_second} RPS · ` : ""
    return `${rate}持续 ${formatTargetDuration(run.duration_ms)}`
  }
  if (run.load_mode === "open_loop") return `开放模型 · ${run.rate_per_second} RPS`
  if (run.load_mode === "single") return "单请求检查"
  return `固定总量 · ${run.concurrency} 并发`
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

function formatStartedAt(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "—"
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(date)
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
