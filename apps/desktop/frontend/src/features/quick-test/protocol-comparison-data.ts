import { DesktopDataError } from "@/app/data-error"
import type { ProtocolID } from "@/features/catalog/protocols"

export const COMPARISON_PROTOCOLS = ["openai-chat", "openai-responses", "anthropic-messages"] as const
export type ComparisonProtocol = typeof COMPARISON_PROTOCOLS[number]

export function isComparisonProtocol(value: unknown): value is ComparisonProtocol {
  return COMPARISON_PROTOCOLS.some(protocol => protocol === value)
}

export function canCompareProtocols(protocol: ProtocolID) {
  return isComparisonProtocol(protocol)
}

export interface ProtocolPreviewTarget {
  protocol: ComparisonProtocol
  cases: number
  requests: number | null
  available: boolean
  missing_item?: string
}

export interface ProtocolComparisonPreview {
  targets: ProtocolPreviewTarget[]
}

export function parseProtocolComparisonPreview(value: unknown): ProtocolComparisonPreview {
  const invalid = () => new DesktopDataError("Invalid protocol comparison preview")
  if (!isRecord(value) || !Array.isArray(value.targets) || value.targets.length !== COMPARISON_PROTOCOLS.length) throw invalid()
  const targets = value.targets.map((target, index): ProtocolPreviewTarget => {
    if (!isRecord(target) || target.protocol !== COMPARISON_PROTOCOLS[index] ||
      !isCount(target.cases) || (target.requests !== null && !isCount(target.requests)) ||
      typeof target.available !== "boolean" || (target.missing_item !== undefined && typeof target.missing_item !== "string")) throw invalid()
    return { protocol: COMPARISON_PROTOCOLS[index], cases: target.cases, requests: target.requests, available: target.available,
      ...(target.missing_item ? { missing_item: target.missing_item } : {}) }
  })
  return { targets }
}

export function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
