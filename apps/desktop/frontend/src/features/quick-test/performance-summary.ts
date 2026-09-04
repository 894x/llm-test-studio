import type { QuickPerformanceLoadMode, QuickPerformancePhase, QuickPerformanceSLOStatus } from "./data"

export function performanceCompletion(
  requestCount: number,
  completed: number,
  planned: number,
): { label: string; value: string } {
  if (requestCount === 0) {
    return { label: "完成（持续时间模式）", value: String(completed) }
  }
  return { label: "完成 / 计划", value: `${completed} / ${planned}` }
}

export function performanceProgressPhaseLabel(phase: QuickPerformancePhase): string {
  switch (phase) {
    case "not_started":
      return "准备中"
    case "warming_up":
      return "正在热身"
    case "ramping":
      return "正在爬坡"
    case "sending":
      return "发送中"
    case "draining":
      return "排空中"
    case "completed":
      return "测试已完成，正在封存报告…"
    case "cancelled":
      return "已取消"
  }
}

export function performanceSLOStatusLabel(status: QuickPerformanceSLOStatus): string {
  switch (status) {
    case "passed":
      return "SLO 通过"
    case "failed":
      return "SLO 未通过"
    case "not_evaluated":
      return "SLO 未评估"
  }
}

export function performanceCapacitySummary(
  capacity: {
    status: QuickPerformanceSLOStatus
    selected_rung_index?: number
    highest_passing_rung_index?: number
    rungs: Array<{ index: number; target: number; slo_assessment: { status: QuickPerformanceSLOStatus } }>
  },
  loadMode: QuickPerformanceLoadMode,
): string {
  const unit = loadMode === "fixed_concurrency" ? "并发" : "RPS"
  const format = (value: number) => new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value)
  const selected = capacity.selected_rung_index === undefined ? undefined : capacity.rungs[capacity.selected_rung_index]
  const highest = capacity.highest_passing_rung_index === undefined ? undefined : capacity.rungs[capacity.highest_passing_rung_index]
  const firstFailed = capacity.rungs.find((rung) => rung.slo_assessment.status === "failed")
  if (capacity.status === "not_evaluated") {
    return selected ? `容量评估未完成 · 当前 ${unit} ${format(selected.target)}` : "容量评估未完成"
  }
  if (capacity.status === "failed") {
    const failed = firstFailed ? `首次未通过 ${format(firstFailed.target)}` : "容量未通过"
    return highest ? `最高通过${unit} ${format(highest.target)} · ${failed}` : failed
  }
  return highest ? `最高通过${unit} ${format(highest.target)}` : "容量评估通过"
}
