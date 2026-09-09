import { isProtocolRunSettings, type ProtocolRunSettings } from "@/features/protocols/types"
import { isJSONValue, isUUID, type CatalogProtocol, type CatalogPlanParameterValue } from "@/features/catalog/data"
import { isProtocol } from "@/features/catalog/protocols"
import type { AssertionResult, ProtocolObservation, Verification, VerificationSummary } from "@/features/protocols/types"
import { DesktopDataError } from "@/app/data-error"
import { translateDesktop as tx } from "@/i18n/runtime"
import { parseQuickPerformanceReport, type QuickPerformanceReport } from "@/features/quick-test/data"

export type ReportSource = "run" | "quick_performance"

export interface ReportSummary {
  id: string
  source: ReportSource
  run_id?: string
  generated_at: string
  run_status: "completed" | "failed" | "cancelled"
  plan_name: string
  model_name: string
  channel_name: string
  passed: boolean
  verdict: string
  issue_count: number
  case_count: number
  failed_case_count: number
  passed_case_count: number
  verified_case_count: number
  observed_case_count: number
  attachment_count: number
}

export interface ReportSnapshot {
  schema_version: 1
  reports: ReportSummary[]
}

export interface ReportMetric {
  value: number
  unit: string
  samples: number
}

export interface ReportResult {
  id: string; request_id?: string; entry_id?: string; case_id?: string
  execution_status: "completed" | "failed" | "cancelled"
  verification: Verification
  observation?: ProtocolObservation
  failure_kind?: string; error_code?: string
  dimensions?: Record<string, string>; metrics: Record<string, number>
}
export type ReportDistribution = Readonly<Record<string, unknown>>
export type ReportEntryStatus = "completed" | "failed" | "cancelled" | "not_started"
export interface ReportCaseDetail {
  case_id: string; revision: number; key: string; name: string; protocol: CatalogProtocol
  verification: VerificationSummary; metrics: Record<string, ReportMetric>
  summary_result?: ReportResult; request_results: ReportResult[]
}
export interface ReportEntryDetail {
  entry_id: string; target_kind: "case" | "suite"; target_id: string
  name: string; key: string; protocol: CatalogProtocol; seed: number
  warmup_count: number; settings: ProtocolRunSettings
  parameters: Record<string, CatalogPlanParameterValue>
  load: { mode: "single" | "fixed_concurrency" | "open_loop"; concurrency: number; request_count: number; rate_per_second: number; duration_ms: number; request_timeout_ms: number }
  status: ReportEntryStatus; conclusion: { passed: boolean; verdict: string; issues: string[] }
  verification: VerificationSummary; sla: Record<string, ReportMetric>; metrics: Record<string, ReportMetric>
  timeline: ReportDistribution[]; distributions: ReportDistribution[]; cases: ReportCaseDetail[]
}
export interface FormalReportDetail {
  schema_version: 3; source: "run"
  report: {
    id: string; run_id: string; protocol: CatalogProtocol; run_status: ReportSummary["run_status"]; generated_at: string
    model: { id: string; name: string }; channel: { id: string; name: string }
    environment: { os: string; arch: string; region: string; network_egress: string; app_version: string; engine_version: string }
    conclusion: { passed: boolean; verdict: string; issues: string[] }
    sla: Record<string, ReportMetric>; metrics: Record<string, ReportMetric>
    distributions?: ReportDistribution[]; case_results: ReportResult[]
  }
  request_results: ReportResult[]; entries: ReportEntryDetail[]; unassigned_request_results: ReportResult[]
}

export interface QuickPerformanceReportDetail {
  schema_version: 1
  source: "quick_performance"
  performance: QuickPerformanceReport
}

export type ReportDetail = FormalReportDetail | QuickPerformanceReportDetail

export type ReportExportFormat = "json" | "html" | "png" | "pdf"

export interface ExportedReport {
  filename: string
  media_type: string
  data_base64: string
}

export const EMPTY_REPORTS: ReportSnapshot = {
  schema_version: 1,
  reports: [],
}

export function reportVerdictTranslationKey(report: ReportSummary): "system.quickPassed" | "system.quickFailed" | "system.passed" | "system.failed" | "system.cancelled" | null {
  if (report.source === "quick_performance") {
    if (["全部请求成功", "快速性能测试通过"].includes(report.verdict)) return "system.quickPassed"
    if (["性能测试未通过", "快速性能测试未通过"].includes(report.verdict)) return "system.quickFailed"
  }
  if (report.verdict === "pass") return "system.passed"
  if (report.verdict === "fail") return "system.failed"
  if (report.verdict === "cancelled") return "system.cancelled"
  return null
}

export function reportPlanTranslationKey(report: ReportSummary): "system.quickPerformance" | null {
  return report.source === "quick_performance" ? "system.quickPerformance" : null
}

export function parseReportSnapshot(value: unknown): ReportSnapshot {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new DesktopDataError(tx("desktop:reports_unsupported_desktop_report_protocol_version"))
  }
  if (!Array.isArray(value.reports)) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_data_structure"))
  }
  const reports = value.reports.map(parseReport)
  if (new Set(reports.map((report) => report.id)).size !== reports.length) {
    throw new DesktopDataError(tx("desktop:reports_duplicate_desktop_report_identifiers"))
  }
  return { schema_version: 1, reports }
}

export function parseReportDetail(value: unknown): ReportDetail {
  if (!isRecord(value) || !isReportSource(value.source)) throw invalidReport()
  if (value.source === "quick_performance") {
    if (value.schema_version !== 1) throw invalidReport()
    const performance = parseQuickPerformanceReport(value.performance)
    if (!performance.archived || !performance.report_id) throw invalidReport()
    return { schema_version: 1, source: "quick_performance", performance }
  }
  if (value.schema_version !== 3 || !isRecord(value.report) || !Array.isArray(value.entries) ||
    !Array.isArray(value.request_results) || !Array.isArray(value.unassigned_request_results) || "suites" in value) throw invalidReport()
  const report = value.report
  if (!isUUID(report.id) || !isUUID(report.run_id) || !isProtocol(report.protocol) || !isRunStatus(report.run_status) ||
    !isUTCTimestamp(report.generated_at) || !isSubject(report.model) || !isSubject(report.channel) ||
    !isEnvironment(report.environment) || !isConclusion(report.conclusion) || !isRecord(report.sla) ||
    !isRecord(report.metrics) || !Array.isArray(report.case_results)) throw invalidReport()
  const entries = value.entries.map(parseEntry)
  if (new Set(entries.map(entry => entry.entry_id)).size !== entries.length || entries.some(entry => entry.protocol !== report.protocol)) throw invalidReport()
  const requestResults = value.request_results.map(parseResult)
  const caseResults = report.case_results.map(parseResult)
  const unassigned = value.unassigned_request_results.map(parseResult)
  const owned = entries.flatMap(entry => entry.cases.flatMap(caseItem => {
    if (caseItem.protocol !== entry.protocol || caseItem.request_results.some(result => result.entry_id !== entry.entry_id || result.case_id !== caseItem.case_id)) throw invalidReport()
    return caseItem.request_results
  }))
  const ids = [...owned, ...unassigned].map(result => result.id)
  if (new Set(ids).size !== ids.length || ids.length !== requestResults.length || requestResults.some(result => !ids.includes(result.id))) throw invalidReport()
  return { schema_version: 3, source: "run", report: {
    id: report.id, run_id: report.run_id, protocol: report.protocol, run_status: report.run_status,
    generated_at: report.generated_at, model: { id: report.model.id, name: report.model.name },
    channel: { id: report.channel.id, name: report.channel.name }, environment: { ...report.environment },
    conclusion: copyConclusion(report.conclusion), sla: parseMetricMap(report.sla), metrics: parseMetricMap(report.metrics),
    distributions: parseRecordArray(report.distributions ?? []), case_results: caseResults,
  }, entries, request_results: requestResults, unassigned_request_results: unassigned }
}
function parseEntry(value: unknown): ReportEntryDetail {
  if (!isRecord(value) || !isUUID(value.entry_id) || !isUUID(value.target_id) ||
    (value.target_kind !== "case" && value.target_kind !== "suite") || !isNonBlank(value.name) ||
    !isNonBlank(value.key) || !isProtocol(value.protocol) || !isNonNegativeInteger(value.seed) ||
    !isNonNegativeInteger(value.warmup_count) || !isProtocolRunSettings(value.settings) ||
    !isRecord(value.parameters) || !Object.values(value.parameters).every(isJSONValue) || !isRecord(value.load) ||
    !["single", "fixed_concurrency", "open_loop"].includes(value.load.mode as string) ||
    !isPositiveInteger(value.load.concurrency) || !isNonNegativeInteger(value.load.request_count) ||
    !isNonNegativeInteger(value.load.duration_ms) || !isPositiveInteger(value.load.request_timeout_ms) ||
    typeof value.load.rate_per_second !== "number" || !Number.isFinite(value.load.rate_per_second) ||
    !isSuiteStatus(value.status) || !isConclusion(value.conclusion) || !isRecord(value.sla) ||
    !isRecord(value.metrics) || !Array.isArray(value.cases)) throw invalidReport()
  const cases = value.cases.map(parseCase)
  if (new Set(cases.map(item => item.case_id)).size !== cases.length) throw invalidReport()
  return { entry_id: value.entry_id, target_kind: value.target_kind, target_id: value.target_id,
    name: value.name, key: value.key, protocol: value.protocol, seed: value.seed,
    warmup_count: value.warmup_count, settings: { ...value.settings },
    parameters: structuredClone(value.parameters) as ReportEntryDetail["parameters"],
    load: { mode: value.load.mode as ReportEntryDetail["load"]["mode"], concurrency: value.load.concurrency,
      request_count: value.load.request_count, rate_per_second: value.load.rate_per_second,
      duration_ms: value.load.duration_ms, request_timeout_ms: value.load.request_timeout_ms },
    status: value.status, conclusion: copyConclusion(value.conclusion), verification: parseSummary(value.verification),
    sla: parseMetricMap(value.sla), metrics: parseMetricMap(value.metrics),
    timeline: parseRecordArray(value.timeline), distributions: parseRecordArray(value.distributions), cases }
}
function parseCase(value: unknown): ReportCaseDetail {
  if (!isRecord(value) || !isUUID(value.case_id) || !isPositiveInteger(value.revision) ||
    !isNonBlank(value.key) || !isNonBlank(value.name) || !isProtocol(value.protocol) ||
    !isRecord(value.metrics) || !Array.isArray(value.request_results) || "case_type" in value) throw invalidReport()
  return { case_id: value.case_id, revision: value.revision, key: value.key, name: value.name,
    protocol: value.protocol, verification: parseSummary(value.verification), metrics: parseMetricMap(value.metrics),
    ...(value.summary_result === undefined ? {} : { summary_result: parseResult(value.summary_result) }),
    request_results: value.request_results.map(parseResult) }
}
function parseSummary(value: unknown): VerificationSummary {
  if (!isRecord(value) || !isVerificationStatus(value.status) ||
    ![value.passed, value.failed, value.observed, value.indeterminate].every(isNonNegativeInteger)) throw invalidReport()
  return { status: value.status, passed: value.passed as number, failed: value.failed as number,
    observed: value.observed as number, indeterminate: value.indeterminate as number }
}
function parseAssertion(value: unknown, depth = 0): AssertionResult {
  if (depth > 32 || !isRecord(value) || !isNonBlank(value.id) || !isVerificationStatus(value.status) ||
    [value.source, value.pointer, value.operator, value.reason].some(item => item !== undefined && typeof item !== "string") ||
    (value.expected !== undefined && !isJSONValue(value.expected)) || (value.actual !== undefined && !isJSONValue(value.actual)) ||
    (value.children !== undefined && !Array.isArray(value.children))) throw invalidReport()
  return { id: value.id, status: value.status,
    ...(typeof value.source === "string" ? { source: value.source } : {}),
    ...(typeof value.pointer === "string" ? { pointer: value.pointer } : {}),
    ...(typeof value.operator === "string" ? { operator: value.operator } : {}),
    ...(typeof value.reason === "string" ? { reason: value.reason } : {}),
    ...(value.expected !== undefined ? { expected: structuredClone(value.expected) } : {}),
    ...(value.actual !== undefined ? { actual: structuredClone(value.actual) } : {}),
    ...(Array.isArray(value.children) ? { children: value.children.map(item => parseAssertion(item, depth + 1)) } : {}) }
}
function parseObservation(value: unknown): ProtocolObservation {
  if (!isRecord(value) || !isProtocol(value.protocol) || !isRecord(value.metrics) ||
    !Object.values(value.metrics).every(item => typeof item === "number" && Number.isFinite(item)) ||
    (value.http_status !== undefined && !isPositiveInteger(value.http_status)) ||
    (value.text !== undefined && typeof value.text !== "string") || (value.stream_completed !== undefined && typeof value.stream_completed !== "boolean") ||
    (value.response !== undefined && !isJSONValue(value.response)) || (value.usage !== undefined && !isJSONValue(value.usage)) ||
    !Array.isArray(value.artifacts) || !Array.isArray(value.exchanges) || !Array.isArray(value.issues)) throw invalidReport()
  if (value.task !== undefined && (!isRecord(value.task) || typeof value.task.id !== "string" || typeof value.task.status !== "string" || typeof value.task.terminal !== "boolean")) throw invalidReport()
  return { protocol: value.protocol, metrics: { ...value.metrics } as Record<string, number>,
    ...(value.http_status !== undefined ? { http_status: value.http_status as number } : {}),
    ...(value.response !== undefined ? { response: structuredClone(value.response) } : {}),
    ...(value.usage !== undefined ? { usage: structuredClone(value.usage) } : {}),
    ...(value.text !== undefined ? { text: value.text as string } : {}),
    ...(value.stream_completed !== undefined ? { stream_completed: value.stream_completed as boolean } : {}),
    ...(value.task !== undefined ? { task: { id: (value.task as NonNullable<ProtocolObservation["task"]>).id, status: (value.task as NonNullable<ProtocolObservation["task"]>).status, terminal: (value.task as NonNullable<ProtocolObservation["task"]>).terminal } } : {}),
    artifacts: value.artifacts.map(item => {
      if (!isRecord(item) || typeof item.kind !== "string" || typeof item.url !== "string") throw invalidReport()
      return { kind: item.kind, url: item.url }
    }),
    exchanges: value.exchanges.map(item => {
      if (!isRecord(item) || typeof item.step !== "string" || typeof item.method !== "string" || typeof item.path !== "string" ||
        typeof item.elapsed_ms !== "number" || !Number.isFinite(item.elapsed_ms) ||
        (item.http_status !== undefined && !isPositiveInteger(item.http_status)) ||
        (item.request_body !== undefined && !isJSONValue(item.request_body)) || (item.response !== undefined && !isJSONValue(item.response))) throw invalidReport()
      return { step: item.step, method: item.method, path: item.path, elapsed_ms: item.elapsed_ms,
        ...(item.http_status !== undefined ? { http_status: item.http_status as number } : {}),
        ...(item.request_body !== undefined ? { request_body: structuredClone(item.request_body) } : {}),
        ...(item.response !== undefined ? { response: structuredClone(item.response) } : {}) }
    }),
    issues: value.issues.map(item => {
      if (!isRecord(item) || typeof item.stage !== "string" || typeof item.code !== "string") throw invalidReport()
      return { stage: item.stage, code: item.code }
    }) }
}
function isVerificationStatus(value: unknown): value is Verification["status"] {
  return value === "passed" || value === "failed" || value === "not_applicable" || value === "indeterminate"
}
function copyConclusion(value: FormalReportDetail["report"]["conclusion"]) { return { passed: value.passed, verdict: value.verdict, issues: [...value.issues] } }
function invalidReport() { return new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details")) }
function parseRecordArray(value: unknown): ReportDistribution[] {
  if (!Array.isArray(value) || !value.every(isRecord)) throw invalidReport()
  return value.map(item => structuredClone(item))
}

export function parseExportedReport(value: unknown): ExportedReport {
  if (
    !isRecord(value) || !isNonBlank(value.filename) || !isNonBlank(value.media_type) ||
    typeof value.data_base64 !== "string" || !/^[A-Za-z0-9+/]*={0,2}$/.test(value.data_base64)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_export_data"))
  }
  return { filename: value.filename, media_type: value.media_type, data_base64: value.data_base64 }
}

function parseMetricMap(value: Record<string, unknown>): Record<string, ReportMetric> {
  const parsed: Record<string, ReportMetric> = {}
  for (const [name, item] of Object.entries(value)) {
    if (!isNonBlank(name) || !isRecord(item) || typeof item.value !== "number" || !Number.isFinite(item.value) || !isNonBlank(item.unit) || !isNonNegativeInteger(item.samples)) {
      throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_metrics"))
    }
    parsed[name] = { value: item.value, unit: item.unit, samples: item.samples }
  }
  return parsed
}

function parseResult(value: unknown): ReportResult {
  if (!isRecord(value) || !isUUID(value.id) || !["completed", "failed", "cancelled"].includes(value.execution_status as string) ||
    !isRecord(value.verification) || !isVerificationStatus(value.verification.status) || !Array.isArray(value.verification.assertions) ||
    (value.request_id !== undefined && !isNonBlank(value.request_id)) ||
    (value.entry_id !== undefined && !isUUID(value.entry_id)) || (value.case_id !== undefined && !isUUID(value.case_id)) ||
    (value.error_code !== undefined && !isNonBlank(value.error_code)) || (value.failure_kind !== undefined && !isNonBlank(value.failure_kind)) ||
    (value.dimensions !== undefined && !isStringRecord(value.dimensions)) ||
    (value.metrics !== undefined && (!isRecord(value.metrics) || !Object.values(value.metrics).every(item => typeof item === "number" && Number.isFinite(item)))) ||
    "success" in value || "suite_entry_id" in value) throw invalidReport()
  return { id: value.id, execution_status: value.execution_status as ReportResult["execution_status"],
    verification: { status: value.verification.status, assertions: value.verification.assertions.map(item => parseAssertion(item)) },
    metrics: { ...(value.metrics as Record<string, number> ?? {}) },
    ...(value.observation !== undefined ? { observation: parseObservation(value.observation) } : {}),
    ...(typeof value.request_id === "string" ? { request_id: value.request_id } : {}),
    ...(typeof value.entry_id === "string" ? { entry_id: value.entry_id } : {}),
    ...(typeof value.case_id === "string" ? { case_id: value.case_id } : {}),
    ...(typeof value.error_code === "string" ? { error_code: value.error_code } : {}),
    ...(typeof value.failure_kind === "string" ? { failure_kind: value.failure_kind } : {}),
    ...(value.dimensions ? { dimensions: { ...value.dimensions as Record<string, string> } } : {}) }
}

function isSubject(value: unknown): value is { id: string; name: string } {
  return isRecord(value) && isUUID(value.id) && isNonBlank(value.name)
}

function isEnvironment(value: unknown): value is FormalReportDetail["report"]["environment"] {
  return isRecord(value) && [value.os, value.arch, value.region, value.network_egress, value.app_version, value.engine_version].every((item) => typeof item === "string")
}

function isConclusion(value: unknown): value is FormalReportDetail["report"]["conclusion"] {
  return isRecord(value) && typeof value.passed === "boolean" && isNonBlank(value.verdict) && Array.isArray(value.issues) && value.issues.every(isNonBlank)
}

function parseReport(value: unknown): ReportSummary {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isReportSource(value.source) ||
    (value.source === "run" ? !isUUID(value.run_id) : value.run_id !== undefined) ||
    !isUTCTimestamp(value.generated_at) ||
    !isRunStatus(value.run_status) ||
    !isNonBlank(value.plan_name) ||
    !isNonBlank(value.model_name) ||
    !isNonBlank(value.channel_name) ||
    typeof value.passed !== "boolean" ||
    !isNonBlank(value.verdict) ||
    !isNonNegativeInteger(value.issue_count) ||
    !isNonNegativeInteger(value.case_count) ||
    !isNonNegativeInteger(value.failed_case_count) ||
    value.failed_case_count > value.case_count ||
    ![value.passed_case_count, value.verified_case_count, value.observed_case_count].every(isNonNegativeInteger) ||
    !isNonNegativeInteger(value.attachment_count) ||
    (value.passed && (value.run_status !== "completed" || value.failed_case_count !== 0))
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_summary"))
  }
  return {
    id: value.id,
    source: value.source,
    ...(value.source === "run" ? { run_id: value.run_id as string } : {}),
    generated_at: value.generated_at,
    run_status: value.run_status,
    plan_name: value.plan_name,
    model_name: value.model_name,
    channel_name: value.channel_name,
    passed: value.passed,
    verdict: value.verdict,
    issue_count: value.issue_count,
    case_count: value.case_count,
    failed_case_count: value.failed_case_count,
    passed_case_count: value.passed_case_count as number, verified_case_count: value.verified_case_count as number, observed_case_count: value.observed_case_count as number,
    attachment_count: value.attachment_count,
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isUTCTimestamp(value: unknown): value is string {
  return (
    typeof value === "string" &&
    (value.endsWith("Z") || /[+-]00:00$/.test(value)) &&
    Number.isFinite(Date.parse(value))
  )
}

function isRunStatus(value: unknown): value is ReportSummary["run_status"] {
  return value === "completed" || value === "failed" || value === "cancelled"
}

function isSuiteStatus(value: unknown): value is ReportEntryStatus {
  return value === "completed" || value === "failed" || value === "cancelled" || value === "not_started"
}

function isReportSource(value: unknown): value is ReportSource {
  return value === "run" || value === "quick_performance"
}

function isNonBlank(value: unknown): value is string {
  return typeof value === "string" && value.trim() === value && value.length > 0
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.entries(value).every(([name, entry]) => isNonBlank(name) && isNonBlank(entry))
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) > 0
}
