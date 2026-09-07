import { desktopLocale } from "@/i18n/runtime"
import { translateDesktop as tx } from "@/i18n/runtime"
import type { QuickPerformanceLoadMode, QuickPerformancePhase, QuickPerformanceSLOStatus } from "./data"
import enQuickTest from "@/i18n/resources/en-US/quick-test.json"
import zhQuickTest from "@/i18n/resources/zh-CN/quick-test.json"

type Translate = (key: string) => string

export function performanceCompletion(
  requestCount: number,
  completed: number,
  planned: number,
  translate: Translate = defaultTranslate,
): { label: string; value: string } {
  if (requestCount === 0) {
    return { label: translate("completion.duration"), value: String(completed) }
  }
  return { label: translate("completion.planned"), value: `${completed} / ${planned}` }
}

export function performanceProgressPhaseLabel(
  phase: QuickPerformancePhase,
  translate: Translate = defaultTranslate,
): string {
  return translate(`phase.${phase}`)
}

function defaultTranslate(key: string): string {
  const [section, name] = key.split(".")
  const group = (desktopLocale() === "en-US" ? enQuickTest : zhQuickTest)[section as "completion" | "phase"]
  return group?.[name as keyof typeof group] ?? key
}

export function performanceSLOStatusLabel(status: QuickPerformanceSLOStatus): string {
  switch (status) {
    case "passed":
      return tx("desktop:quick-test_slo_passed")
    case "failed":
      return tx("desktop:quick-test_slo_failed")
    case "not_evaluated":
      return tx("desktop:quick-test_slo_not_evaluated")
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
  const unit = loadMode === "fixed_concurrency" ? tx("desktop:quick-test_concurrency") : "RPS"
  const format = (value: number) => new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 2 }).format(value)
  const selected = capacity.selected_rung_index === undefined ? undefined : capacity.rungs[capacity.selected_rung_index]
  const highest = capacity.highest_passing_rung_index === undefined ? undefined : capacity.rungs[capacity.highest_passing_rung_index]
  const firstFailed = capacity.rungs.find((rung) => rung.slo_assessment.status === "failed")
  if (capacity.status === "not_evaluated") {
    return selected ? tx("desktop:quick-test_capacity_evaluation_incomplete_current_value_value", { value1: unit, value2: format(selected.target) }) : tx("desktop:quick-test_capacity_evaluation_incomplete")
  }
  if (capacity.status === "failed") {
    const failed = firstFailed ? tx("desktop:quick-test_first_failed_target_value", { value1: format(firstFailed.target) }) : tx("desktop:quick-test_capacity_failed")
    return highest ? tx("desktop:quick-test_highest_passing_value_value_value", { value1: unit, value2: format(highest.target), value3: failed }) : failed
  }
  return highest ? tx("desktop:quick-test_highest_passing_value_value", { value1: unit, value2: format(highest.target) }) : tx("desktop:quick-test_capacity_evaluation_passed")
}
