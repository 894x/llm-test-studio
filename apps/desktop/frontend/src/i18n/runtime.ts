import { getI18n } from "react-i18next"

import enCommon from "./resources/en-US/common.json"
import zhCommon from "./resources/zh-CN/common.json"
import enDesktop from "./resources/en-US/desktop.json"
import zhDesktop from "./resources/zh-CN/desktop.json"
import { resources } from "./i18n"

// Formatting and validation helpers run outside React. Components use the
// provider's useTranslation hook so language changes also trigger a render.
export function desktopLocale(): "zh-CN" | "en-US" {
  return typeof document !== "undefined" && document.documentElement.lang === "en-US" ? "en-US" : "zh-CN"
}

export function translateExecutionError(code: string, locale?: string): string {
  return lookupCommonMessage("errorCode", code, locale)
}

export function translateFailurePhase(phase: string, locale?: string): string {
  return lookupCommonMessage("failurePhase", phase, locale)
}

export function translateRunFailureSummary(phase: string | undefined, code: string, locale?: string): string {
  return `${translateFailurePhase(phase || "run", locale)} · ${translateExecutionError(code, locale)}`
}

function lookupCommonMessage(group: "errorCode" | "failurePhase", code: string, locale?: string): string {
  const lng = locale === "en-US" || locale === "zh-CN" ? locale : desktopLocale()
  const key = `common:${group}.${code}`
  const instance = getI18n()
  if (instance?.exists(key, { lng })) return String(instance.t(key, { lng }))
  const bundle = lng === "en-US" ? enCommon : zhCommon
  const table = bundle[group] as Record<string, string>
  return table[code] ?? code
}

export function translateDesktop(key: string, values: Record<string, unknown> = {}): string {
  const locale = desktopLocale()
  const instance = getI18n()
  if (instance?.exists(key, { lng: locale })) return String(instance.t(key, { ...values, lng: locale }))
  const resource = locale === "en-US" ? enDesktop : zhDesktop
  const message = resource[key.replace(/^desktop:/, "") as keyof typeof resource] ?? key
  return message.replace(/{{\s*([^}\s]+)\s*}}/g, (placeholder, name: string) => name in values ? String(values[name]) : placeholder)
}

type Translate = (key: string, values?: Record<string, unknown>) => string

// Only use this for UI messages retained in state, never for authored names,
// model output or report evidence. Match known resource templates so errors
// remain readable after a language change without repeating the operation.
const storedMessages = Object.values(resources).flatMap((namespaces) =>
  Object.entries(namespaces).flatMap(([namespace, bundle]) => messageTemplates(bundle, `${namespace}:`)),
).sort((left, right) =>
  right.message.replace(/{{.*?}}/g, "").length - left.message.replace(/{{.*?}}/g, "").length,
)

function messageTemplates(value: unknown, key: string): { key: string; message: string; names: string[]; pattern: RegExp }[] {
  if (typeof value !== "string") return Object.entries(value as Record<string, unknown>)
    .flatMap(([name, child]) => messageTemplates(child, `${key}${key.endsWith(":") ? "" : "."}${name}`))
  const names: string[] = []
  const parts = value.split(/({{\s*[^}\s]+\s*}})/g).map((part) => {
    const placeholder = /^{{\s*([^}\s]+)\s*}}$/.exec(part)
    if (placeholder) { names.push(placeholder[1]); return "([\\s\\S]*?)" }
    return part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
  })
  return [{ key, message: value, names, pattern: new RegExp(`^${parts.join("")}$`) }]
}

export function localizeStoredMessage(message: string, translate: Translate = translateDesktop, depth = 0): string {
  const exact = storedMessages.find((entry) => entry.names.length === 0 && entry.message === message)
  if (exact) return translate(exact.key)
  if (depth >= 2) return message
  for (const entry of storedMessages) {
    if (!entry.names.length) continue
    const match = entry.pattern.exec(message)
    if (!match) continue
    const values = Object.fromEntries(entry.names.map((name, index) => [name, localizeStoredMessage(match[index + 1], translate, depth + 1)]))
    return translate(entry.key, values)
  }
  return message
}
