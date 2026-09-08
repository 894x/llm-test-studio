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
  id: string
  request_id?: string
  suite_entry_id?: string
  case_id?: string
  success: {
    transport: boolean
    protocol: boolean
    semantic: boolean
    sla: boolean
  }
  failure_kind?: string
  error_code?: string
  dimensions?: Record<string, string>
  metrics: Record<string, number>
}

export type ReportDistribution = Readonly<Record<string, unknown>>
export type ReportSuiteStatus = "completed" | "failed" | "cancelled" | "not_started"

export interface ReportCaseDetail {
  case_id: string
  revision: number
  key: string
  name: string
  case_type: string
  case_type_version: number
  summary_result?: ReportResult
  request_results: ReportResult[]
}

export interface ReportSuiteDetail {
  suite_entry_id: string
  suite_id: string
  suite_revision: number
  suite_key: string
  suite_name: string
  status: ReportSuiteStatus
  conclusion: { passed: boolean; verdict: string; issues: string[] }
  sla: Record<string, ReportMetric>
  metrics: Record<string, ReportMetric>
  timeline: ReportDistribution[]
  distributions: ReportDistribution[]
  cases: ReportCaseDetail[]
}

export interface FormalReportDetail {
  schema_version: 2
  source: "run"
  report: {
    id: string
    run_id: string
    run_status: ReportSummary["run_status"]
    generated_at: string
    model: { id: string; name: string }
    channel: { id: string; name: string }
    environment: {
      os: string
      arch: string
      region: string
      network_egress: string
      app_version: string
      engine_version: string
    }
    conclusion: { passed: boolean; verdict: string; issues: string[] }
    sla: Record<string, ReportMetric>
    metrics: Record<string, ReportMetric>
    distributions?: ReportDistribution[]
    case_results: ReportResult[]
  }
  request_results: ReportResult[]
  suites: ReportSuiteDetail[]
  unassigned_request_results: ReportResult[]
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
  if (!isRecord(value) || !isReportSource(value.source)) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  if (value.source === "quick_performance") {
    if (value.schema_version !== 1) throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    const performance = parseQuickPerformanceReport(value.performance)
    if (!performance.archived || !performance.report_id) throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    return { schema_version: 1, source: "quick_performance", performance }
  }
  if (
    value.schema_version !== 2 || !isRecord(value.report) ||
    !Array.isArray(value.request_results) || !Array.isArray(value.suites) ||
    !Array.isArray(value.unassigned_request_results)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  const report = value.report
  if (
    !isUUID(report.id) || !isUUID(report.run_id) || !isRunStatus(report.run_status) ||
    !isUTCTimestamp(report.generated_at) || !isSubject(report.model) || !isSubject(report.channel) ||
    !isEnvironment(report.environment) || !isConclusion(report.conclusion) ||
    !isRecord(report.sla) || !isRecord(report.metrics) ||
    (report.distributions !== undefined && !Array.isArray(report.distributions)) ||
    !Array.isArray(report.case_results)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  const requestResults = value.request_results.map(parseResult)
  const parsedReport = {
    id: report.id, run_id: report.run_id, run_status: report.run_status,
    generated_at: report.generated_at, model: report.model, channel: report.channel,
    environment: report.environment, conclusion: report.conclusion,
    sla: parseMetricMap(report.sla), metrics: parseMetricMap(report.metrics),
    distributions: parseRecordArray(report.distributions ?? []),
    case_results: report.case_results.map(parseResult),
  }

  const suites = value.suites.map(parseSuiteDetail)
  const unassignedRequestResults = value.unassigned_request_results.map(parseResult)
  validateCanonicalRunTree(
    suites,
    unassignedRequestResults,
    requestResults,
    parsedReport.case_results,
  )
  validateRunConclusion(parsedReport.run_status, parsedReport.conclusion, suites)
  return {
    schema_version: 2,
    source: "run",
    report: parsedReport,
    request_results: requestResults,
    suites,
    unassigned_request_results: unassignedRequestResults,
  }
}

function parseSuiteDetail(value: unknown): ReportSuiteDetail {
  if (
    !isRecord(value) || !isUUID(value.suite_entry_id) || !isUUID(value.suite_id) ||
    !isPositiveInteger(value.suite_revision) || !isNonBlank(value.suite_key) ||
    !isNonBlank(value.suite_name) || !isSuiteStatus(value.status) || !isConclusion(value.conclusion) ||
    !isRecord(value.sla) || !isRecord(value.metrics) || !Array.isArray(value.timeline) ||
    !Array.isArray(value.distributions) || !Array.isArray(value.cases)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  if (!suiteConclusionMatchesStatus(value.status, value.conclusion)) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  return {
    suite_entry_id: value.suite_entry_id,
    suite_id: value.suite_id,
    suite_revision: value.suite_revision,
    suite_key: value.suite_key,
    suite_name: value.suite_name,
    status: value.status,
    conclusion: value.conclusion,
    sla: parseMetricMap(value.sla),
    metrics: parseMetricMap(value.metrics),
    timeline: parseRecordArray(value.timeline),
    distributions: parseRecordArray(value.distributions),
    cases: value.cases.map(parseCaseDetail),
  }
}

function parseCaseDetail(value: unknown): ReportCaseDetail {
  if (
    !isRecord(value) || !isUUID(value.case_id) || !isPositiveInteger(value.revision) ||
    !isNonBlank(value.key) || !isNonBlank(value.name) || !isNonBlank(value.case_type) ||
    !isPositiveInteger(value.case_type_version) || !Array.isArray(value.request_results)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  return {
    case_id: value.case_id,
    revision: value.revision,
    key: value.key,
    name: value.name,
    case_type: value.case_type,
    case_type_version: value.case_type_version,
    ...(value.summary_result !== undefined ? { summary_result: parseResult(value.summary_result) } : {}),
    request_results: value.request_results.map(parseResult),
  }
}

function validateCanonicalRunTree(
  suites: ReportSuiteDetail[],
  unassignedRequestResults: ReportResult[],
  requestResults: ReportResult[],
  caseResults: ReportResult[],
): void {
  const suiteEntryIDs = new Set<string>()
  const resultIDs = new Set<string>()
  const projectedRequests = new Map<string, ReportResult>()
  const projectedCaseResults = new Map<string, ReportResult>()
  for (const suite of suites) {
    const suiteEntryID = suite.suite_entry_id
    if (suiteEntryIDs.has(suiteEntryID)) {
      throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    }
    suiteEntryIDs.add(suiteEntryID)

    const caseIDs = new Set<string>()
    for (const caseReport of suite.cases) {
      if (caseIDs.has(caseReport.case_id)) {
        throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
      }
      caseIDs.add(caseReport.case_id)
      if (caseReport.summary_result) {
        validateOwnedResult(caseReport.summary_result, suiteEntryID, caseReport.case_id, false, resultIDs)
        projectedCaseResults.set(caseReport.summary_result.id, caseReport.summary_result)
      }
      for (const result of caseReport.request_results) {
        validateOwnedResult(result, suiteEntryID, caseReport.case_id, true, resultIDs)
        projectedRequests.set(result.id, result)
      }
    }
  }
  for (const result of unassignedRequestResults) {
    if (
      !result.request_id || result.suite_entry_id !== undefined || result.case_id !== undefined ||
      resultIDs.has(result.id)
    ) {
      throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    }
    resultIDs.add(result.id)
    projectedRequests.set(result.id, result)
  }
  validateResultProjection(requestResults, projectedRequests)
  validateResultProjection(caseResults, projectedCaseResults)
}

function validateOwnedResult(
  result: ReportResult,
  suiteEntryID: string,
  caseID: string,
  request: boolean,
  resultIDs: Set<string>,
): void {
  if (
    result.suite_entry_id !== suiteEntryID || result.case_id !== caseID ||
    (request ? !result.request_id : result.request_id !== undefined) ||
    resultIDs.has(result.id)
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  resultIDs.add(result.id)
}

function validateResultProjection(
  canonical: ReportResult[],
  projection: Map<string, ReportResult>,
): void {
  if (canonical.length !== projection.size) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  const canonicalIDs = new Set<string>()
  for (const result of canonical) {
    const projected = projection.get(result.id)
    if (canonicalIDs.has(result.id) || !projected || !sameReportResult(result, projected)) {
      throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    }
    canonicalIDs.add(result.id)
  }
}

function sameReportResult(left: ReportResult, right: ReportResult): boolean {
  return (
    left.id === right.id && left.request_id === right.request_id &&
    left.suite_entry_id === right.suite_entry_id && left.case_id === right.case_id &&
    left.failure_kind === right.failure_kind && left.error_code === right.error_code &&
    left.success.transport === right.success.transport &&
    left.success.protocol === right.success.protocol &&
    left.success.semantic === right.success.semantic && left.success.sla === right.success.sla &&
    sameRecord(left.dimensions, right.dimensions) && sameRecord(left.metrics, right.metrics)
  )
}

function sameRecord<T extends string | number>(
  left: Record<string, T> | undefined,
  right: Record<string, T> | undefined,
): boolean {
  if (left === undefined || right === undefined) return left === right
  const leftKeys = Object.keys(left).sort()
  const rightKeys = Object.keys(right).sort()
  return leftKeys.length === rightKeys.length &&
    leftKeys.every((key, index) => key === rightKeys[index] && left[key] === right[key])
}

function suiteConclusionMatchesStatus(
  status: ReportSuiteStatus,
  conclusion: FormalReportDetail["report"]["conclusion"],
): boolean {
  switch (status) {
    case "completed":
      return conclusion.verdict === (conclusion.passed ? "pass" : "fail")
    case "failed":
      return !conclusion.passed && conclusion.verdict === "fail"
    case "cancelled":
      return !conclusion.passed && conclusion.verdict === "cancelled"
    case "not_started":
      return !conclusion.passed && conclusion.verdict === "not_started"
  }
}

function validateRunConclusion(
  status: FormalReportDetail["report"]["run_status"],
  conclusion: FormalReportDetail["report"]["conclusion"],
  suites: ReportSuiteDetail[],
): void {
  let executionStopped = false
  for (const suite of suites) {
    if (
      (executionStopped && suite.status !== "not_started") ||
      (status !== "cancelled" && suite.status === "cancelled")
    ) {
      throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
    }
    if (suite.status === "cancelled" || suite.status === "not_started") executionStopped = true
  }
  if (
    (status !== "completed" && conclusion.passed) ||
    (status === "completed" && suites.some((suite) => suite.status !== "completed")) ||
    (conclusion.passed && suites.some((suite) => !suite.conclusion.passed))
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
}

function parseRecordArray(value: unknown): ReportDistribution[] {
  if (!Array.isArray(value) || !value.every(isRecord)) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_details"))
  }
  return value.map((item) => ({ ...item }))
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
  if (
    !isRecord(value) || !isUUID(value.id) || !isRecord(value.success) ||
    (value.request_id !== undefined && !isNonBlank(value.request_id)) ||
    (value.suite_entry_id !== undefined && !isUUID(value.suite_entry_id)) ||
    (value.case_id !== undefined && !isUUID(value.case_id)) ||
    (value.failure_kind !== undefined && !isNonBlank(value.failure_kind)) ||
    (value.error_code !== undefined && !isNonBlank(value.error_code)) ||
    (value.metrics !== undefined && !isRecord(value.metrics)) ||
    (value.dimensions !== undefined && !isStringRecord(value.dimensions))
  ) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_result_data"))
  }
  const success = value.success
  if ([success.transport, success.protocol, success.semantic, success.sla].some((item) => typeof item !== "boolean")) {
    throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_result_status"))
  }
  const metrics: Record<string, number> = {}
  for (const [name, metric] of Object.entries((value.metrics ?? {}) as Record<string, unknown>)) {
    if (!isNonBlank(name) || typeof metric !== "number" || !Number.isFinite(metric)) throw new DesktopDataError(tx("desktop:reports_invalid_desktop_report_request_metrics"))
    metrics[name] = metric
  }
  return {
    id: value.id,
    ...(typeof value.request_id === "string" && value.request_id ? { request_id: value.request_id } : {}),
    ...(typeof value.suite_entry_id === "string" && value.suite_entry_id ? { suite_entry_id: value.suite_entry_id } : {}),
    ...(typeof value.case_id === "string" && value.case_id ? { case_id: value.case_id } : {}),
    success: { transport: success.transport as boolean, protocol: success.protocol as boolean, semantic: success.semantic as boolean, sla: success.sla as boolean },
    ...(typeof value.failure_kind === "string" && value.failure_kind ? { failure_kind: value.failure_kind } : {}),
    ...(typeof value.error_code === "string" && value.error_code ? { error_code: value.error_code } : {}),
    ...(value.dimensions !== undefined ? { dimensions: { ...value.dimensions } as Record<string, string> } : {}),
    metrics,
  }
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
    attachment_count: value.attachment_count,
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

function isUUID(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)
  )
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

function isSuiteStatus(value: unknown): value is ReportSuiteStatus {
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
