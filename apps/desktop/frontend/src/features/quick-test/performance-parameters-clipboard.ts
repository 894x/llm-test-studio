import { parseQuickPerformanceProfile } from "./data"
import { performanceFormFromProfile, performanceProfileFromForm, validatePerformanceForm, type PerformanceForm } from "./performance-profile"

const PARAMETER_KEYS = new Set(Object.keys(performanceProfileFromForm(performanceFormFromProfile())))
const MAX_PARAMETER_TEXT_LENGTH = 32_768

export function parsePerformanceParameters(text: string): PerformanceForm {
  if (text.length > MAX_PARAMETER_TEXT_LENGTH) throw new Error("invalid_performance_parameters")
  const value: unknown = JSON.parse(text)
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid_performance_parameters")
  const keys = Object.keys(value)
  if (keys.length !== PARAMETER_KEYS.size || keys.some(key => !PARAMETER_KEYS.has(key)))
    throw new Error("invalid_performance_parameters")
  const form = performanceFormFromProfile(parseQuickPerformanceProfile(value))
  if (Object.keys(validatePerformanceForm(form)).length) throw new Error("invalid_performance_parameters")
  return form
}

type ClipboardRuntime = {
  ClipboardSetText?: (text: string) => Promise<boolean>
  ClipboardGetText?: () => Promise<string>
}

function clipboardRuntime(): ClipboardRuntime | undefined {
  return (window as typeof window & { runtime?: ClipboardRuntime }).runtime
}

export async function writePerformanceParameters(text: string): Promise<void> {
  const runtime = clipboardRuntime()
  if (typeof runtime?.ClipboardSetText === "function") {
    if (!await runtime.ClipboardSetText(text)) throw new Error("clipboard_write_failed")
    return
  }
  if (!navigator.clipboard?.writeText) throw new Error("clipboard_unavailable")
  await navigator.clipboard.writeText(text)
}

export async function readPerformanceParameters(): Promise<string> {
  const runtime = clipboardRuntime()
  if (typeof runtime?.ClipboardGetText === "function") return runtime.ClipboardGetText()
  if (!navigator.clipboard?.readText) throw new Error("clipboard_unavailable")
  return navigator.clipboard.readText()
}
