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
