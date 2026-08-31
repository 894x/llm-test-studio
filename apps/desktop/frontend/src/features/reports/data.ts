export interface ReportSummary {
  id: string
  run_id: string
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
  case_id?: string
  success: {
    transport: boolean
    protocol: boolean
    semantic: boolean
    sla: boolean
  }
  failure_kind?: string
  error_code?: string
  metrics: Record<string, number>
}

export interface ReportDetail {
  schema_version: 1
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
    case_results: ReportResult[]
  }
  request_results: ReportResult[]
}

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

export function parseReportSnapshot(value: unknown): ReportSnapshot {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new Error("桌面报告数据协议版本不受支持")
  }
  if (!Array.isArray(value.reports)) {
    throw new Error("桌面报告数据结构无效")
  }
  const reports = value.reports.map(parseReport)
  if (new Set(reports.map((report) => report.id)).size !== reports.length) {
    throw new Error("桌面报告数据包含重复标识")
  }
  return { schema_version: 1, reports }
}

export function parseReportDetail(value: unknown): ReportDetail {
  if (!isRecord(value) || value.schema_version !== 1 || !isRecord(value.report) || !Array.isArray(value.request_results)) {
    throw new Error("桌面报告详情数据无效")
  }
  const report = value.report
  if (
    !isUUID(report.id) || !isUUID(report.run_id) || !isRunStatus(report.run_status) ||
    !isUTCTimestamp(report.generated_at) || !isSubject(report.model) || !isSubject(report.channel) ||
    !isEnvironment(report.environment) || !isConclusion(report.conclusion) ||
    !isRecord(report.sla) || !isRecord(report.metrics) || !Array.isArray(report.case_results)
  ) {
    throw new Error("桌面报告详情数据无效")
  }
  return {
    schema_version: 1,
    report: {
      id: report.id, run_id: report.run_id, run_status: report.run_status,
      generated_at: report.generated_at, model: report.model, channel: report.channel,
      environment: report.environment, conclusion: report.conclusion,
      sla: parseMetricMap(report.sla), metrics: parseMetricMap(report.metrics),
      case_results: report.case_results.map(parseResult),
    },
    request_results: value.request_results.map(parseResult),
  }
}

export function parseExportedReport(value: unknown): ExportedReport {
  if (
    !isRecord(value) || !isNonBlank(value.filename) || !isNonBlank(value.media_type) ||
    typeof value.data_base64 !== "string" || !/^[A-Za-z0-9+/]*={0,2}$/.test(value.data_base64)
  ) {
    throw new Error("桌面报告导出数据无效")
  }
  return { filename: value.filename, media_type: value.media_type, data_base64: value.data_base64 }
}

function parseMetricMap(value: Record<string, unknown>): Record<string, ReportMetric> {
  const parsed: Record<string, ReportMetric> = {}
  for (const [name, item] of Object.entries(value)) {
    if (!isNonBlank(name) || !isRecord(item) || typeof item.value !== "number" || !Number.isFinite(item.value) || !isNonBlank(item.unit) || !isNonNegativeInteger(item.samples)) {
      throw new Error("桌面报告指标数据无效")
    }
    parsed[name] = { value: item.value, unit: item.unit, samples: item.samples }
  }
  return parsed
}

function parseResult(value: unknown): ReportResult {
  if (!isRecord(value) || !isUUID(value.id) || !isRecord(value.success) || (value.metrics !== undefined && !isRecord(value.metrics))) {
    throw new Error("桌面报告结果数据无效")
  }
  const success = value.success
  if ([success.transport, success.protocol, success.semantic, success.sla].some((item) => typeof item !== "boolean")) {
    throw new Error("桌面报告结果状态无效")
  }
  const metrics: Record<string, number> = {}
  for (const [name, metric] of Object.entries((value.metrics ?? {}) as Record<string, unknown>)) {
    if (!isNonBlank(name) || typeof metric !== "number" || !Number.isFinite(metric)) throw new Error("桌面报告请求指标无效")
    metrics[name] = metric
  }
  return {
    id: value.id,
    ...(typeof value.request_id === "string" && value.request_id ? { request_id: value.request_id } : {}),
    ...(typeof value.case_id === "string" && value.case_id ? { case_id: value.case_id } : {}),
    success: { transport: success.transport as boolean, protocol: success.protocol as boolean, semantic: success.semantic as boolean, sla: success.sla as boolean },
    ...(typeof value.failure_kind === "string" && value.failure_kind ? { failure_kind: value.failure_kind } : {}),
    ...(typeof value.error_code === "string" && value.error_code ? { error_code: value.error_code } : {}),
    metrics,
  }
}

function isSubject(value: unknown): value is { id: string; name: string } {
  return isRecord(value) && isUUID(value.id) && isNonBlank(value.name)
}

function isEnvironment(value: unknown): value is ReportDetail["report"]["environment"] {
  return isRecord(value) && [value.os, value.arch, value.region, value.network_egress, value.app_version, value.engine_version].every((item) => typeof item === "string")
}

function isConclusion(value: unknown): value is ReportDetail["report"]["conclusion"] {
  return isRecord(value) && typeof value.passed === "boolean" && isNonBlank(value.verdict) && Array.isArray(value.issues) && value.issues.every(isNonBlank)
}

function parseReport(value: unknown): ReportSummary {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isUUID(value.run_id) ||
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
    throw new Error("桌面报告摘要数据无效")
  }
  return {
    id: value.id,
    run_id: value.run_id,
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

function isNonBlank(value: unknown): value is string {
  return typeof value === "string" && value.trim() === value && value.length > 0
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
}
