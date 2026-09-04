export type QuickTestAddressMode = "base_url" | "full_url"

export type QuickTestErrorCode =
  | "invalid_request"
  | "insecure_endpoint"
  | "credential_required"
  | "authentication_failed"
  | "network_error"
  | "timeout"
  | "cancelled"
  | "http_error"
  | "rate_limited"
  | "protocol_error"
  | "incomplete_stream"
  | "semantic_empty"
  | "response_too_large"
  | "client_closed"
  | "executor_panic"
  | "scheduler_overload"
  | "request_failed"
  | "unclassified_error"

export interface QuickTestCommand {
  address_mode: QuickTestAddressMode
  url: string
  api_key: string
  channel_id?: string
  model_id: string
  prompt: string
  timeout_ms: number
}

export function updateQuickTestForm<K extends keyof QuickTestCommand>(
  current: QuickTestCommand,
  key: K,
  value: QuickTestCommand[K],
): QuickTestCommand {
  const next = { ...current, [key]: value }
  if (current.channel_id && (key === "url" || key === "address_mode")) {
    const { channel_id: _channelID, ...manual } = next
    return manual
  }
  return next
}

export interface QuickTestResult {
  schema_version: 1
  success: boolean
  address_mode: QuickTestAddressMode
  base_url: string
  endpoint: string
  http_status: number
  e2e_ms: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  error_code?: QuickTestErrorCode
}

export interface SaveQuickTestConnectionCommand {
  base_url: string
  api_key: string
  model_id: string
  model_name: string
  channel_name: string
  existing_model_id?: string
}

export interface QuickPerformanceCommand {
  address_mode: QuickTestAddressMode
  url: string
  api_key: string
  channel_id?: string
  model_id: string
  request_count: number
  duration_ms: number
  concurrency: number
  timeout_ms: number
  input_tokens: number
  output_tokens: number
}

export interface QuickPerformanceProfile {
  request_count: number
  duration_ms: number
  concurrency: number
  timeout_ms: number
  input_tokens: number
  output_tokens: number
}

export type QuickPerformancePhase = "not_started" | "sending" | "draining" | "completed" | "cancelled"

export interface QuickPerformanceProgress {
  phase: QuickPerformancePhase
  planned: number
  launched: number
  completed: number
  in_flight: number
  peak_in_flight: number
  succeeded: number
  failed: number
  rejected: number
  send_duration_ms: number
  drain_duration_ms: number
  total_duration_ms: number
}

export interface QuickPerformanceMetrics {
  completed: number
  succeeded: number
  failed: number
  timed_out: number
  success_rate_percent: number
  request_qps: number
  rpm: number
  input_tpm: number
  output_tpm: number
  total_tpm: number
  generation_tps: number
  ttft_p50_ms: number
  ttft_p90_ms: number
  ttft_p95_ms: number
  ttft_p99_ms: number
  ttft_average_ms: number
  tpot_p50_ms: number
  tpot_p90_ms: number
  tpot_p95_ms: number
  tpot_p99_ms: number
  tpot_average_ms: number
  e2e_p50_ms: number
  e2e_p90_ms: number
  e2e_p95_ms: number
  e2e_p99_ms: number
  e2e_average_ms: number
  schedule_lag_p50_ms: number
  schedule_lag_p90_ms: number
  schedule_lag_p95_ms: number
  schedule_lag_p99_ms: number
  schedule_lag_average_ms: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  cache_rate_percent: number
}

export type QuickPerformanceArchiveStatus = "not_attempted" | "archived" | "failed"

export type QuickPerformanceEvidenceCaptureStatus = "captured" | "empty" | "omitted"

export interface QuickPerformanceResponseEvidence {
  capture_status: QuickPerformanceEvidenceCaptureStatus
  content_type?: string
  request_id?: string
  body: string
  body_bytes: number
  truncated: boolean
  redacted: true
}

export interface QuickPerformanceSample {
  request_index: number
  scheduled_offset_ms: number
  started_offset_ms: number
  finished_offset_ms: number
  schedule_lag_ms: number
  e2e_ms: number
  ttft_ms: number
  tpot_ms: number
  http_status: number
  success: boolean
  timed_out: boolean
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  error_code?: QuickTestErrorCode
  response_evidence?: QuickPerformanceResponseEvidence
}

export interface QuickPerformanceReport {
  schema_version: 1
  report_id?: string
  generated_at?: string
  archived: boolean
  archive_status: QuickPerformanceArchiveStatus
  model_id: string
  success: boolean
  address_mode: QuickTestAddressMode
  base_url: string
  endpoint: string
  profile: QuickPerformanceProfile
  progress: QuickPerformanceProgress
  metrics: QuickPerformanceMetrics
  samples: QuickPerformanceSample[]
  failures: Array<{ error_code: QuickTestErrorCode; count: number }>
  error_code?: QuickTestErrorCode
}

export function parseQuickPerformanceProgress(value: unknown): QuickPerformanceProgress {
  if (!isPerformanceProgress(value)) throw new Error("快速性能进度数据结构无效")
  return pickPerformanceProgress(value)
}

const ERROR_CODES = new Set<QuickTestErrorCode>([
  "invalid_request",
  "insecure_endpoint",
  "credential_required",
  "authentication_failed",
  "network_error",
  "timeout",
  "cancelled",
  "http_error",
  "rate_limited",
  "protocol_error",
  "incomplete_stream",
  "semantic_empty",
  "response_too_large",
  "client_closed",
  "executor_panic",
  "scheduler_overload",
  "request_failed",
  "unclassified_error",
])

export const QUICK_TEST_ERROR_MESSAGES: Record<QuickTestErrorCode, string> = {
  invalid_request: "测试参数无效",
  insecure_endpoint: "仅支持 HTTPS 接口地址",
  credential_required: "API Key 不能为空",
  authentication_failed: "鉴权失败",
  network_error: "无法连接接口",
  timeout: "请求超时",
  cancelled: "测试已取消",
  http_error: "接口返回失败状态",
  rate_limited: "接口触发限流",
  protocol_error: "接口响应协议无效",
  incomplete_stream: "流式响应未正常结束",
  semantic_empty: "接口未返回有效模型内容",
  response_too_large: "接口响应超过安全限制",
  client_closed: "测试客户端已关闭",
  executor_panic: "测试执行器异常",
  scheduler_overload: "本地调度容量不足",
  request_failed: "接口请求失败",
  unclassified_error: "接口返回未分类错误",
}

export function parseQuickTestResult(value: unknown): QuickTestResult {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new Error("快速测试数据协议版本不受支持")
  }
  if (
    typeof value.success !== "boolean" ||
    !isAddressMode(value.address_mode) ||
    !isOptionalSafeURL(value.base_url) ||
    !isOptionalSafeURL(value.endpoint) ||
    !isStatus(value.http_status) ||
    !isNonNegativeFinite(value.e2e_ms) ||
    !isNonNegativeInteger(value.prompt_tokens) ||
    !isNonNegativeInteger(value.completion_tokens) ||
    !isNonNegativeInteger(value.cached_tokens) ||
    (value.error_code !== undefined && !isErrorCode(value.error_code)) ||
    (value.success && value.error_code !== undefined) ||
    (!value.success && value.error_code === undefined)
  ) {
    throw new Error("快速测试数据结构无效")
  }
  if (value.success && (!value.base_url || !value.endpoint)) {
    throw new Error("快速测试数据结构无效")
  }
  return {
    schema_version: 1,
    success: value.success,
    address_mode: value.address_mode,
    base_url: value.base_url,
    endpoint: value.endpoint,
    http_status: value.http_status,
    e2e_ms: value.e2e_ms,
    prompt_tokens: value.prompt_tokens,
    completion_tokens: value.completion_tokens,
    cached_tokens: value.cached_tokens,
    ...(value.error_code === undefined ? {} : { error_code: value.error_code }),
  }
}

function parsePerformanceResponseEvidence(value: unknown): QuickPerformanceResponseEvidence {
  if (!isRecord(value)) throw new Error("快速性能报告响应证据无效")
  const body = value.body === undefined ? "" : value.body
  const contentType = value.content_type
  const requestID = value.request_id
  if (
    !isEvidenceCaptureStatus(value.capture_status) ||
    typeof body !== "string" || new TextEncoder().encode(body).length > 16 * 1024 ||
    (contentType !== undefined && (typeof contentType !== "string" || contentType.length > 128)) ||
    (requestID !== undefined && (typeof requestID !== "string" || requestID.length > 256)) ||
    !isNonNegativeInteger(value.body_bytes) ||
    typeof value.truncated !== "boolean" ||
    value.redacted !== true ||
    (value.capture_status === "captured" && body.trim() === "") ||
    (value.capture_status === "empty" && (body !== "" || value.truncated)) ||
    (value.capture_status === "omitted" && (body !== "" || !value.truncated))
  ) throw new Error("快速性能报告响应证据无效")
  return {
    capture_status: value.capture_status,
    ...(contentType === undefined ? {} : { content_type: contentType }),
    ...(requestID === undefined ? {} : { request_id: requestID }),
    body,
    body_bytes: value.body_bytes,
    truncated: value.truncated,
    redacted: true,
  }
}

export function parseQuickPerformanceReport(value: unknown): QuickPerformanceReport {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new Error("快速性能报告数据协议版本不受支持")
  }
  if (
    typeof value.success !== "boolean" ||
    typeof value.archived !== "boolean" ||
    !isArchiveStatus(value.archive_status) ||
    typeof value.model_id !== "string" ||
    (value.report_id !== undefined && !isUUID(value.report_id)) ||
    (value.generated_at !== undefined && !isUTCTimestamp(value.generated_at)) ||
    !isAddressMode(value.address_mode) ||
    !isOptionalSafeURL(value.base_url) ||
    !isOptionalSafeURL(value.endpoint) ||
    !isPerformanceProfile(value.profile) ||
    !isPerformanceProgress(value.progress) ||
    !isPerformanceMetrics(value.metrics) ||
    !Array.isArray(value.failures) ||
    !Array.isArray(value.samples) ||
    (value.error_code !== undefined && !isErrorCode(value.error_code)) ||
    (value.success && value.error_code !== undefined)
  ) {
    throw new Error("快速性能报告数据结构无效")
  }
  const failures: QuickPerformanceReport["failures"] = []
  const seen = new Set<QuickTestErrorCode>()
  for (const failure of value.failures) {
    if (!isRecord(failure) || !isErrorCode(failure.error_code) || !isPositiveInteger(failure.count) || seen.has(failure.error_code)) {
      throw new Error("快速性能报告数据结构无效")
    }
    seen.add(failure.error_code)
    failures.push({ error_code: failure.error_code, count: failure.count })
  }
  const progress = value.progress
  const metrics = value.metrics
  const samples = value.samples.map(parsePerformanceSample)
  const evidenceBytes = samples.reduce((sum, sample) => sum + new TextEncoder().encode(sample.response_evidence?.body ?? "").length, 0)
  const failureCount = failures.reduce((sum, failure) => sum + failure.count, 0)
  if (
    progress.completed !== metrics.completed ||
    progress.succeeded !== metrics.succeeded ||
    progress.failed !== metrics.failed ||
    metrics.succeeded + metrics.failed !== metrics.completed ||
    failureCount !== metrics.failed ||
    samples.length !== metrics.completed ||
    evidenceBytes > 2 * 1024 * 1024 ||
    new Set(samples.map((sample) => sample.request_index)).size !== samples.length ||
    (value.archived !== (value.archive_status === "archived")) ||
    (value.archived && (value.report_id === undefined || value.generated_at === undefined)) ||
    (!value.archived && value.archive_status === "not_attempted" && (value.report_id !== undefined || value.generated_at !== undefined)) ||
    (value.success && (progress.phase !== "completed" || metrics.completed === 0 || metrics.failed !== 0)) ||
    (!value.success && value.error_code === undefined && metrics.failed === 0)
  ) {
    throw new Error("快速性能报告数据结构无效")
  }
  if (value.error_code === undefined && !isRunnablePerformanceProfile(value.profile)) {
    throw new Error("快速性能报告数据结构无效")
  }
  return {
    schema_version: 1,
    ...(value.report_id === undefined ? {} : { report_id: value.report_id }),
    ...(value.generated_at === undefined ? {} : { generated_at: value.generated_at }),
    archived: value.archived,
    archive_status: value.archive_status,
    model_id: value.model_id,
    success: value.success,
    address_mode: value.address_mode,
    base_url: value.base_url,
    endpoint: value.endpoint,
    profile: pickPerformanceProfile(value.profile),
    progress: pickPerformanceProgress(progress),
    metrics: pickPerformanceMetrics(metrics),
    samples,
    failures,
    ...(value.error_code === undefined ? {} : { error_code: value.error_code }),
  }
}

function parsePerformanceSample(value: unknown): QuickPerformanceSample {
  if (!isRecord(value)) throw new Error("快速性能报告样本数据无效")
  const offsets = [value.scheduled_offset_ms, value.started_offset_ms, value.finished_offset_ms, value.schedule_lag_ms, value.e2e_ms, value.ttft_ms, value.tpot_ms]
  if (
    !isNonNegativeInteger(value.request_index) ||
    !offsets.every(isNonNegativeFinite) ||
    !isStatus(value.http_status) ||
    typeof value.success !== "boolean" ||
    typeof value.timed_out !== "boolean" ||
    !isNonNegativeInteger(value.prompt_tokens) ||
    !isNonNegativeInteger(value.completion_tokens) ||
    !isNonNegativeInteger(value.cached_tokens) ||
    (value.error_code !== undefined && !isErrorCode(value.error_code)) ||
    (value.success && (value.error_code !== undefined || value.response_evidence !== undefined)) ||
    Number(value.started_offset_ms) < Number(value.scheduled_offset_ms) ||
    Number(value.finished_offset_ms) < Number(value.started_offset_ms)
  ) throw new Error("快速性能报告样本数据无效")
  return {
    request_index: value.request_index,
    scheduled_offset_ms: Number(value.scheduled_offset_ms),
    started_offset_ms: Number(value.started_offset_ms),
    finished_offset_ms: Number(value.finished_offset_ms),
    schedule_lag_ms: Number(value.schedule_lag_ms),
    e2e_ms: Number(value.e2e_ms),
    ttft_ms: Number(value.ttft_ms),
    tpot_ms: Number(value.tpot_ms),
    http_status: value.http_status,
    success: value.success,
    timed_out: value.timed_out,
    prompt_tokens: value.prompt_tokens,
    completion_tokens: value.completion_tokens,
    cached_tokens: value.cached_tokens,
    ...(value.error_code === undefined ? {} : { error_code: value.error_code }),
    ...(value.response_evidence === undefined ? {} : { response_evidence: parsePerformanceResponseEvidence(value.response_evidence) }),
  }
}

function isPerformanceProfile(value: unknown): value is QuickPerformanceProfile {
  return isRecord(value) &&
    isNonNegativeInteger(value.request_count) &&
    isNonNegativeInteger(value.duration_ms) &&
    isNonNegativeInteger(value.concurrency) &&
    isNonNegativeInteger(value.timeout_ms) &&
    isNonNegativeInteger(value.input_tokens) &&
    isNonNegativeInteger(value.output_tokens)
}

function isRunnablePerformanceProfile(value: QuickPerformanceProfile): boolean {
  return (value.request_count > 0 || value.duration_ms > 0) &&
    value.concurrency > 0 && value.timeout_ms > 0 && value.input_tokens > 0 && value.output_tokens > 0
}

function pickPerformanceProfile(value: QuickPerformanceProfile): QuickPerformanceProfile {
  return {
    request_count: value.request_count,
    duration_ms: value.duration_ms,
    concurrency: value.concurrency,
    timeout_ms: value.timeout_ms,
    input_tokens: value.input_tokens,
    output_tokens: value.output_tokens,
  }
}

function pickPerformanceProgress(value: QuickPerformanceProgress): QuickPerformanceProgress {
  return {
    phase: value.phase,
    planned: value.planned,
    launched: value.launched,
    completed: value.completed,
    in_flight: value.in_flight ?? 0,
    peak_in_flight: value.peak_in_flight,
    succeeded: value.succeeded,
    failed: value.failed,
    rejected: value.rejected,
    send_duration_ms: value.send_duration_ms,
    drain_duration_ms: value.drain_duration_ms,
    total_duration_ms: value.total_duration_ms,
  }
}

function pickPerformanceMetrics(value: QuickPerformanceMetrics): QuickPerformanceMetrics {
  return {
    completed: value.completed,
    succeeded: value.succeeded,
    failed: value.failed,
    timed_out: value.timed_out,
    success_rate_percent: value.success_rate_percent,
    request_qps: value.request_qps,
    rpm: value.rpm,
    input_tpm: value.input_tpm,
    output_tpm: value.output_tpm,
    total_tpm: value.total_tpm,
    generation_tps: value.generation_tps,
    ttft_p50_ms: value.ttft_p50_ms,
    ttft_p90_ms: value.ttft_p90_ms,
    ttft_p95_ms: value.ttft_p95_ms,
    ttft_p99_ms: value.ttft_p99_ms,
    ttft_average_ms: value.ttft_average_ms,
    tpot_p50_ms: value.tpot_p50_ms,
    tpot_p90_ms: value.tpot_p90_ms,
    tpot_p95_ms: value.tpot_p95_ms,
    tpot_p99_ms: value.tpot_p99_ms,
    tpot_average_ms: value.tpot_average_ms,
    e2e_p50_ms: value.e2e_p50_ms,
    e2e_p90_ms: value.e2e_p90_ms,
    e2e_p95_ms: value.e2e_p95_ms,
    e2e_p99_ms: value.e2e_p99_ms,
    e2e_average_ms: value.e2e_average_ms,
    schedule_lag_p50_ms: value.schedule_lag_p50_ms,
    schedule_lag_p90_ms: value.schedule_lag_p90_ms ?? 0,
    schedule_lag_p95_ms: value.schedule_lag_p95_ms,
    schedule_lag_p99_ms: value.schedule_lag_p99_ms ?? 0,
    schedule_lag_average_ms: value.schedule_lag_average_ms,
    prompt_tokens: value.prompt_tokens,
    completion_tokens: value.completion_tokens,
    cached_tokens: value.cached_tokens,
    cache_rate_percent: value.cache_rate_percent,
  }
}

function isPerformanceProgress(value: unknown): value is QuickPerformanceProgress {
  return isRecord(value) &&
    isPerformancePhase(value.phase) &&
    isNonNegativeInteger(value.planned) &&
    isNonNegativeInteger(value.launched) &&
    isNonNegativeInteger(value.completed) &&
    (value.in_flight === undefined || isNonNegativeInteger(value.in_flight)) &&
    isNonNegativeInteger(value.peak_in_flight) &&
    isNonNegativeInteger(value.succeeded) &&
    isNonNegativeInteger(value.failed) &&
    isNonNegativeInteger(value.rejected) &&
    isNonNegativeFinite(value.send_duration_ms) &&
    isNonNegativeFinite(value.drain_duration_ms) &&
    isNonNegativeFinite(value.total_duration_ms)
}

function isPerformanceMetrics(value: unknown): value is QuickPerformanceMetrics {
  if (!isRecord(value)) return false
  const integerFields = ["completed", "succeeded", "failed", "timed_out", "prompt_tokens", "completion_tokens", "cached_tokens"] as const
  const numberFields = [
    "success_rate_percent", "request_qps", "rpm", "input_tpm", "output_tpm", "total_tpm", "generation_tps",
    "ttft_p50_ms", "ttft_p90_ms", "ttft_p95_ms", "ttft_p99_ms", "ttft_average_ms",
    "tpot_p50_ms", "tpot_p90_ms", "tpot_p95_ms", "tpot_p99_ms", "tpot_average_ms",
    "e2e_p50_ms", "e2e_p90_ms", "e2e_p95_ms", "e2e_p99_ms", "e2e_average_ms",
    "schedule_lag_p50_ms", "schedule_lag_p95_ms", "schedule_lag_average_ms", "cache_rate_percent",
  ] as const
  return integerFields.every((field) => isNonNegativeInteger(value[field])) &&
    numberFields.every((field) => isNonNegativeFinite(value[field])) &&
    (value.schedule_lag_p90_ms === undefined || isNonNegativeFinite(value.schedule_lag_p90_ms)) &&
    (value.schedule_lag_p99_ms === undefined || isNonNegativeFinite(value.schedule_lag_p99_ms)) &&
    Number(value.success_rate_percent) <= 100 && Number(value.cache_rate_percent) <= 100
}

function isPerformancePhase(value: unknown): value is QuickPerformancePhase {
  return value === "not_started" || value === "sending" || value === "draining" || value === "completed" || value === "cancelled"
}

function isArchiveStatus(value: unknown): value is QuickPerformanceArchiveStatus {
  return value === "not_attempted" || value === "archived" || value === "failed"
}

function isEvidenceCaptureStatus(value: unknown): value is QuickPerformanceEvidenceCaptureStatus {
  return value === "captured" || value === "empty" || value === "omitted"
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isAddressMode(value: unknown): value is QuickTestAddressMode {
  return value === "base_url" || value === "full_url"
}

function isErrorCode(value: unknown): value is QuickTestErrorCode {
  return typeof value === "string" && ERROR_CODES.has(value as QuickTestErrorCode)
}

function isOptionalSafeURL(value: unknown): value is string {
  if (value === "") return true
  if (typeof value !== "string" || value !== value.trim()) return false
  try {
    return new URL(value).protocol === "https:"
  } catch {
    return false
  }
}

function isStatus(value: unknown): value is number {
  return Number.isInteger(value) && Number(value) >= 0 && Number(value) <= 599
}

function isUUID(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)
}

function isUTCTimestamp(value: unknown): value is string {
  return typeof value === "string" && (value.endsWith("Z") || /[+-]00:00$/.test(value)) && Number.isFinite(Date.parse(value))
}

function isNonNegativeFinite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) > 0
}
