import type { QuickPerformancePhase } from "./data"
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
  const group = zhQuickTest[section as "completion" | "phase"]
  return group?.[name as keyof typeof group] ?? key
}
