import type { ComponentType } from "react"
import type { CatalogProtocol } from "@/features/catalog/data"

export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue }
export type VerificationStatus = "passed" | "failed" | "not_applicable" | "indeterminate"
export interface AssertionResult {
  id: string
  source?: string
  pointer?: string
  operator?: string
  expected?: JSONValue
  actual?: JSONValue
  status: VerificationStatus
  reason?: string
  children?: AssertionResult[]
}
export interface Verification { status: VerificationStatus; assertions: AssertionResult[] }
export interface VerificationSummary { status: VerificationStatus; passed: number; failed: number; observed: number; indeterminate: number }
export interface ProtocolObservation {
  protocol: CatalogProtocol
  http_status?: number
  response?: JSONValue
  text?: string
  stream_completed?: boolean
  metrics: Record<string, number>
  usage?: JSONValue
  task?: { id: string; status: string; terminal: boolean }
  artifacts: { kind: string; url: string }[]
  exchanges: { step: string; method: string; path: string; request_body?: JSONValue; http_status?: number; response?: JSONValue; elapsed_ms: number }[]
  issues: { stage: string; code: string }[]
}
export interface ProtocolColumn { id: string; label: string; unit?: string; metric?: string; observation?: "http_status" | "task_status" | "artifacts" }
export interface ProtocolRunSettings { poll_interval_ms?: number; task_timeout_ms?: number }
export interface ProtocolPresentation {
  protocol: CatalogProtocol
  label: string
  columns: ProtocolColumn[]
  requestExample: Record<string, JSONValue>
  operations: readonly string[]
  runSettings: readonly (keyof ProtocolRunSettings)[]
  ObservationDetails: ComponentType<{ observation: ProtocolObservation }>
}
export function isProtocolRunSettings(value: unknown): value is ProtocolRunSettings {
  return typeof value === "object" && value !== null && !Array.isArray(value) &&
    Object.entries(value).every(([key, field]) => ["poll_interval_ms", "task_timeout_ms"].includes(key) && typeof field === "number" && Number.isSafeInteger(field) && field > 0)
}
