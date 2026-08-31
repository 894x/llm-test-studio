import type { QuickPerformancePhase } from "./data"

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
