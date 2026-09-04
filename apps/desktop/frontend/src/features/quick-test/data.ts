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
  load_mode: QuickPerformanceLoadMode
  request_count: number
  duration_ms: number
  concurrency: number
  rate_per_second: number
  max_in_flight: number
  arrival_pattern: QuickPerformanceArrivalPattern
  workload_mode: QuickPerformanceWorkloadMode
  random_seed: number
  input_tokens_stddev: number
  output_tokens_stddev: number
  shared_prefix_tokens: number
  warmup_requests: number
  ramp_duration_ms: number
  ramp_request_cap: number
  slice_duration_ms: number
  slo_ttft_ms: number
  slo_tpot_ms: number
  slo_e2e_ms: number
  slo_target_percent: number
  capacity_enabled: boolean
  capacity_start: number
  capacity_step: number
  timeout_ms: number
  input_tokens: number
  output_tokens: number
}

export type QuickPerformanceLoadMode = "fixed_concurrency" | "open_loop"
export type QuickPerformanceArrivalPattern = "constant" | "poisson"
export type QuickPerformanceWorkloadMode = "fixed" | "normal"

export interface QuickPerformanceProfile {
  load_mode?: QuickPerformanceLoadMode
  request_count: number
  duration_ms: number
  concurrency: number
  rate_per_second?: number
  max_in_flight?: number
  arrival_pattern?: QuickPerformanceArrivalPattern
  workload_mode?: QuickPerformanceWorkloadMode
  random_seed?: number
  input_tokens_stddev?: number
  output_tokens_stddev?: number
  shared_prefix_tokens?: number
  warmup_requests?: number
  ramp_duration_ms?: number
  ramp_request_cap?: number
  slice_duration_ms?: number
  slo_ttft_ms?: number
  slo_tpot_ms?: number
  slo_e2e_ms?: number
  slo_target_percent?: number
  capacity_enabled?: boolean
  capacity_start?: number
  capacity_step?: number
  timeout_ms: number
  input_tokens: number
  output_tokens: number
}

export type QuickPerformancePhase = "not_started" | "warming_up" | "ramping" | "sending" | "draining" | "completed" | "cancelled"

export interface QuickPerformanceProgress {
  phase: QuickPerformancePhase
  planned: number
  offered?: number
  launched: number
  completed: number
  in_flight: number
  peak_in_flight: number
  succeeded: number
  failed: number
  rejected: number
  stopped?: boolean
  capped?: boolean
  capacity_rung_number?: number
  capacity_rung_count?: number
  capacity_target?: number
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
  offered_qps?: number
  launched_qps?: number
  completed_qps?: number
  successful_request_qps?: number
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
  target_input_tokens?: number
  target_output_tokens?: number
  error_code?: QuickTestErrorCode
  response_evidence?: QuickPerformanceResponseEvidence
}

export interface QuickPerformanceRequestBudget {
  limit: number
  warmup_cap: number
  ramp_cap: number
  measured_cap: number
  total_cap: number
}

export interface QuickPerformanceTrafficSummary {
  request_cap: number
  offered: number
  launched: number
  completed: number
  succeeded: number
  failed: number
  timed_out: number
  rejected: number
  peak_in_flight: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  send_duration_ms: number
  drain_duration_ms: number
  total_duration_ms: number
  failures: Array<{ error_code: QuickTestErrorCode; count: number }>
  stopped: boolean
  capped: boolean
}

export interface QuickPerformanceRamp {
  shape: "linear_staircase"
  duration_ms: number
  steps: number
  target_concurrency?: number
  target_rate_per_second?: number
  completed_window: boolean
  traffic: QuickPerformanceTrafficSummary
}

export interface QuickPerformanceSliceLatency {
  count: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

export interface QuickPerformanceTimeSlice {
  slice_index: number
  start_ms: number
  end_ms: number
  partial: boolean
  offered: number
  launched: number
  completed: number
  succeeded: number
  failed: number
  rejected: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  ttft: QuickPerformanceSliceLatency
  tpot: QuickPerformanceSliceLatency
  e2e: QuickPerformanceSliceLatency
}

export type QuickPerformanceSLOStatus = "not_evaluated" | "passed" | "failed"

export interface QuickPerformanceSLOAssessment {
  status: QuickPerformanceSLOStatus
  thresholds: {
    ttft_ms: number
    tpot_ms: number
    e2e_ms: number
  }
  target_percent: number
  total_requests: number
  good_requests: number
  bad_requests: number
  good_request_percent: number
  goodput_qps: number
  violations: {
    transport: number
    ttft: number
    tpot: number
    e2e: number
  }
}

export interface QuickPerformanceCapacityRung {
  index: number
  target: number
  success: boolean
  progress: QuickPerformanceProgress
  metrics: QuickPerformanceMetrics
  failures: Array<{ error_code: QuickTestErrorCode; count: number }>
  slo_assessment: QuickPerformanceSLOAssessment
}

export interface QuickPerformanceCapacityResult {
  status: QuickPerformanceSLOStatus
  selected_rung_index?: number
  highest_passing_rung_index?: number
  rungs: QuickPerformanceCapacityRung[]
}

export interface QuickPerformanceReport {
  schema_version: 1 | 2
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
  request_budget?: QuickPerformanceRequestBudget
  warmup?: QuickPerformanceTrafficSummary
  ramp?: QuickPerformanceRamp
  time_slices?: QuickPerformanceTimeSlice[]
  slo_assessment?: QuickPerformanceSLOAssessment
  capacity_result?: QuickPerformanceCapacityResult
  error_code?: QuickTestErrorCode
}

export function parseQuickPerformanceProgress(value: unknown): QuickPerformanceProgress {
  if (!isPerformanceProgress(value)) throw new Error("快速性能进度数据结构无效")
  return pickPerformanceProgress(value, true)
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
  if (!isRecord(value) || (value.schema_version !== 1 && value.schema_version !== 2)) {
    throw new Error("快速性能报告数据协议版本不受支持")
  }
  const schemaVersion = value.schema_version
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
    !isPerformanceProfile(value.profile, schemaVersion) ||
    !isPerformanceProgress(value.progress) ||
    !isPerformanceMetrics(value.metrics, schemaVersion) ||
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
  const samples = value.samples.map((sample) => parsePerformanceSample(sample, schemaVersion === 2))
  const requestBudget = schemaVersion === 2 && value.request_budget !== undefined
    ? parsePerformanceRequestBudget(value.request_budget)
    : undefined
  const warmup = schemaVersion === 2 && value.warmup !== undefined
    ? parsePerformanceTrafficSummary(value.warmup)
    : undefined
  const ramp = schemaVersion === 2 && value.ramp !== undefined
    ? parsePerformanceRamp(value.ramp)
    : undefined
  const timeSlices = schemaVersion === 2 && value.time_slices !== undefined
    ? parsePerformanceTimeSlices(value.time_slices, value.profile, progress.total_duration_ms)
    : undefined
  const sloAssessment = schemaVersion === 2 && value.slo_assessment !== undefined
    ? parsePerformanceSLOAssessment(value.slo_assessment, value.profile, progress, metrics, samples)
    : undefined
  const capacityResult = schemaVersion === 2 && value.capacity_result !== undefined
    ? parsePerformanceCapacityResult(value.capacity_result, value.profile)
    : undefined
  const evidenceBytes = samples.reduce((sum, sample) => sum + new TextEncoder().encode(sample.response_evidence?.body ?? "").length, 0)
  const failureCount = failures.reduce((sum, failure) => sum + failure.count, 0)
  if (
    progress.completed !== metrics.completed ||
    progress.succeeded !== metrics.succeeded ||
    progress.failed !== metrics.failed ||
    metrics.succeeded + metrics.failed !== metrics.completed ||
    failureCount !== metrics.failed ||
    samples.length !== metrics.completed ||
    (schemaVersion === 2 && !performanceSampleTargetsMatchWorkload(value.profile, samples)) ||
    (schemaVersion === 2 && !performancePhaseThreeReportMatches(
      value.profile,
      progress,
      metrics,
      requestBudget,
      warmup,
      ramp,
      timeSlices,
      value.error_code !== undefined,
    )) ||
    (schemaVersion === 2 && !performancePhaseFourReportMatches(
      value.profile,
      value.success,
      progress,
      metrics,
      failures,
      sloAssessment,
      capacityResult,
      value.error_code !== undefined,
    )) ||
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
  if (value.error_code === undefined && !isRunnablePerformanceProfile(value.profile, schemaVersion)) {
    throw new Error("快速性能报告数据结构无效")
  }
  return {
    schema_version: schemaVersion,
    ...(value.report_id === undefined ? {} : { report_id: value.report_id }),
    ...(value.generated_at === undefined ? {} : { generated_at: value.generated_at }),
    archived: value.archived,
    archive_status: value.archive_status,
    model_id: value.model_id,
    success: value.success,
    address_mode: value.address_mode,
    base_url: value.base_url,
    endpoint: value.endpoint,
    profile: pickPerformanceProfile(value.profile, schemaVersion),
    progress: pickPerformanceProgress(progress, schemaVersion === 2),
    metrics: pickPerformanceMetrics(metrics, schemaVersion),
    samples,
    failures,
    ...(requestBudget === undefined ? {} : { request_budget: requestBudget }),
    ...(warmup === undefined ? {} : { warmup }),
    ...(ramp === undefined ? {} : { ramp }),
    ...(timeSlices === undefined ? {} : { time_slices: timeSlices }),
    ...(sloAssessment === undefined ? {} : { slo_assessment: sloAssessment }),
    ...(capacityResult === undefined ? {} : { capacity_result: capacityResult }),
    ...(value.error_code === undefined ? {} : { error_code: value.error_code }),
  }
}

function parsePerformanceSample(value: unknown, includeTargets: boolean): QuickPerformanceSample {
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
    (includeTargets && value.target_input_tokens !== undefined && !isPositiveUint32(value.target_input_tokens)) ||
    (includeTargets && value.target_output_tokens !== undefined && !isPositiveUint32(value.target_output_tokens)) ||
    (includeTargets && ((value.target_input_tokens === undefined) !== (value.target_output_tokens === undefined))) ||
    (value.error_code !== undefined && !isErrorCode(value.error_code)) ||
    (value.success && (value.error_code !== undefined || value.response_evidence !== undefined)) ||
    Number(value.started_offset_ms) < Number(value.scheduled_offset_ms) ||
    Number(value.finished_offset_ms) < Number(value.started_offset_ms)
  ) throw new Error("快速性能报告样本数据无效")
  const targetInputTokens = includeTargets && value.target_input_tokens !== undefined ? Number(value.target_input_tokens) : undefined
  const targetOutputTokens = includeTargets && value.target_output_tokens !== undefined ? Number(value.target_output_tokens) : undefined
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
    ...(targetInputTokens === undefined ? {} : { target_input_tokens: targetInputTokens }),
    ...(targetOutputTokens === undefined ? {} : { target_output_tokens: targetOutputTokens }),
    ...(value.error_code === undefined ? {} : { error_code: value.error_code }),
    ...(value.response_evidence === undefined ? {} : { response_evidence: parsePerformanceResponseEvidence(value.response_evidence) }),
  }
}

function parsePerformanceSLOAssessment(
  value: unknown,
  profile: QuickPerformanceProfile,
  progress: QuickPerformanceProgress,
  metrics: QuickPerformanceMetrics,
  samples?: QuickPerformanceSample[],
): QuickPerformanceSLOAssessment {
  if (!isRecord(value) || !isPerformanceSLOStatus(value.status) || !isRecord(value.thresholds) || !isRecord(value.violations)) {
    throw new Error("快速性能报告数据结构无效")
  }
  const thresholdFields = ["ttft_ms", "tpot_ms", "e2e_ms"] as const
  const countFields = ["total_requests", "good_requests", "bad_requests"] as const
  const violationFields = ["transport", "ttft", "tpot", "e2e"] as const
  if (
    !thresholdFields.every((field) => isNonNegativeFinite((value.thresholds as Record<string, unknown>)[field])) ||
    !countFields.every((field) => isNonNegativeInteger(value[field])) ||
    !violationFields.every((field) => isNonNegativeInteger((value.violations as Record<string, unknown>)[field])) ||
    !isPositiveFinite(value.target_percent) || Number(value.target_percent) > 100 ||
    !isNonNegativeFinite(value.good_request_percent) || Number(value.good_request_percent) > 100 ||
    !isNonNegativeFinite(value.goodput_qps)
  ) throw new Error("快速性能报告数据结构无效")

  const assessment: QuickPerformanceSLOAssessment = {
    status: value.status,
    thresholds: {
      ttft_ms: Number(value.thresholds.ttft_ms),
      tpot_ms: Number(value.thresholds.tpot_ms),
      e2e_ms: Number(value.thresholds.e2e_ms),
    },
    target_percent: Number(value.target_percent),
    total_requests: Number(value.total_requests),
    good_requests: Number(value.good_requests),
    bad_requests: Number(value.bad_requests),
    good_request_percent: Number(value.good_request_percent),
    goodput_qps: Number(value.goodput_qps),
    violations: {
      transport: Number(value.violations.transport),
      ttft: Number(value.violations.ttft),
      tpot: Number(value.violations.tpot),
      e2e: Number(value.violations.e2e),
    },
  }
  const expectedPercent = assessment.total_requests === 0 ? 0 : assessment.good_requests / assessment.total_requests * 100
  const expectedGoodput = progress.total_duration_ms === 0 ? 0 : assessment.good_requests / (progress.total_duration_ms / 1_000)
  const expectedStatus: QuickPerformanceSLOStatus = progress.phase !== "completed" || progress.stopped === true || assessment.total_requests === 0
    ? "not_evaluated"
    : expectedPercent >= assessment.target_percent
      ? "passed"
      : "failed"
  const latencyViolationCounts = [assessment.violations.ttft, assessment.violations.tpot, assessment.violations.e2e]
  const latencyBad = assessment.bad_requests - assessment.violations.transport
  if (
    !performanceSLOEnabled(profile) ||
    !approximatelyEqual(assessment.thresholds.ttft_ms, profile.slo_ttft_ms ?? 0) ||
    !approximatelyEqual(assessment.thresholds.tpot_ms, profile.slo_tpot_ms ?? 0) ||
    !approximatelyEqual(assessment.thresholds.e2e_ms, profile.slo_e2e_ms ?? 0) ||
    !approximatelyEqual(assessment.target_percent, profile.slo_target_percent ?? 0) ||
    assessment.total_requests !== metrics.completed ||
    assessment.good_requests > metrics.succeeded ||
    assessment.bad_requests !== assessment.total_requests - assessment.good_requests ||
    !approximatelyEqual(assessment.good_request_percent, expectedPercent) ||
    !approximatelyEqual(assessment.goodput_qps, expectedGoodput) ||
    assessment.status !== expectedStatus ||
    assessment.violations.transport !== metrics.failed ||
    assessment.bad_requests < assessment.violations.transport ||
    latencyBad > metrics.succeeded ||
    latencyBad < Math.max(...latencyViolationCounts) ||
    latencyBad > latencyViolationCounts.reduce((sum, count) => sum + count, 0) ||
    assessment.violations.ttft > metrics.succeeded ||
    assessment.violations.tpot > metrics.succeeded ||
    assessment.violations.e2e > metrics.succeeded ||
    (assessment.thresholds.ttft_ms === 0 && assessment.violations.ttft !== 0) ||
    (assessment.thresholds.tpot_ms === 0 && assessment.violations.tpot !== 0) ||
    (assessment.thresholds.e2e_ms === 0 && assessment.violations.e2e !== 0)
  ) throw new Error("快速性能报告数据结构无效")

  if (samples !== undefined) {
    const observed = samples.reduce((summary, sample) => {
      const transport = !sample.success
      const ttft = sample.success && assessment.thresholds.ttft_ms > 0 && (sample.ttft_ms <= 0 || sample.ttft_ms > assessment.thresholds.ttft_ms)
      const tpot = sample.success && assessment.thresholds.tpot_ms > 0 && (sample.tpot_ms <= 0 || sample.tpot_ms > assessment.thresholds.tpot_ms)
      const e2e = sample.success && assessment.thresholds.e2e_ms > 0 && (sample.e2e_ms <= 0 || sample.e2e_ms > assessment.thresholds.e2e_ms)
      if (sample.success && !ttft && !tpot && !e2e) summary.good += 1
      if (transport) summary.transport += 1
      if (ttft) summary.ttft += 1
      if (tpot) summary.tpot += 1
      if (e2e) summary.e2e += 1
      return summary
    }, { good: 0, transport: 0, ttft: 0, tpot: 0, e2e: 0 })
    if (
      assessment.total_requests !== samples.length ||
      assessment.good_requests !== observed.good ||
      assessment.violations.transport !== observed.transport ||
      assessment.violations.ttft !== observed.ttft ||
      assessment.violations.tpot !== observed.tpot ||
      assessment.violations.e2e !== observed.e2e
    ) throw new Error("快速性能报告数据结构无效")
  }
  return assessment
}

function parsePerformanceCapacityResult(value: unknown, profile: QuickPerformanceProfile): QuickPerformanceCapacityResult {
  if (!isRecord(value) || !isPerformanceSLOStatus(value.status) || !Array.isArray(value.rungs)) {
    throw new Error("快速性能报告数据结构无效")
  }
  const targets = performanceCapacityTargets(profile)
  if (!targets || value.rungs.length === 0 || value.rungs.length > targets.length || value.rungs.length > 20) {
    throw new Error("快速性能报告数据结构无效")
  }
  const selected = value.selected_rung_index
  const highest = value.highest_passing_rung_index
  if (!isNonNegativeInteger(selected) || Number(selected) >= value.rungs.length ||
    (highest !== undefined && (!isNonNegativeInteger(highest) || Number(highest) >= value.rungs.length))) {
    throw new Error("快速性能报告数据结构无效")
  }
  const rungs = value.rungs.map((rung, index) => parsePerformanceCapacityRung(rung, profile, index, targets[index], targets.length))
  const passing = rungs.filter((rung) => rung.slo_assessment.status === "passed")
  const highestPassing = passing.length === 0 ? undefined : passing[passing.length - 1].index
  const last = rungs[rungs.length - 1]
  const firstNonPassing = rungs.findIndex((rung) => rung.slo_assessment.status !== "passed")
  if (firstNonPassing >= 0 && firstNonPassing !== rungs.length - 1) throw new Error("快速性能报告数据结构无效")

  let expectedSelected: number
  if (last.slo_assessment.status === "failed") {
    if (value.status !== "failed") throw new Error("快速性能报告数据结构无效")
    expectedSelected = highestPassing ?? last.index
  } else if (last.slo_assessment.status === "not_evaluated") {
    if (value.status !== "not_evaluated") throw new Error("快速性能报告数据结构无效")
    expectedSelected = last.index
  } else {
    if (value.status !== "passed" || rungs.length !== targets.length) throw new Error("快速性能报告数据结构无效")
    expectedSelected = last.index
  }
  if (Number(selected) !== expectedSelected || highest !== highestPassing) throw new Error("快速性能报告数据结构无效")
  return {
    status: value.status,
    selected_rung_index: Number(selected),
    ...(highestPassing === undefined ? {} : { highest_passing_rung_index: highestPassing }),
    rungs,
  }
}

function parsePerformanceCapacityRung(
  value: unknown,
  profile: QuickPerformanceProfile,
  expectedIndex: number,
  expectedTarget: number,
  rungCount: number,
): QuickPerformanceCapacityRung {
  if (!isRecord(value) || value.index !== expectedIndex || !isPositiveFinite(value.target) ||
    !approximatelyEqual(Number(value.target), expectedTarget) || typeof value.success !== "boolean" ||
    !isPerformanceProgress(value.progress) || !isPerformanceMetrics(value.metrics, 2) || !Array.isArray(value.failures)) {
    throw new Error("快速性能报告数据结构无效")
  }
  const progress = pickPerformanceProgress(value.progress, true)
  const metrics = pickPerformanceMetrics(value.metrics, 2)
  const failures = parsePerformanceFailures(value.failures)
  const assessment = parsePerformanceSLOAssessment(value.slo_assessment, profile, progress, metrics)
  const failureCount = failures.reduce((sum, failure) => sum + failure.count, 0)
  const expectedSuccess = progress.phase === "completed" && metrics.completed > 0 && metrics.failed === 0
  if (
    progress.completed !== metrics.completed || progress.succeeded !== metrics.succeeded || progress.failed !== metrics.failed ||
    metrics.succeeded + metrics.failed !== metrics.completed || failureCount !== metrics.failed || value.success !== expectedSuccess ||
    progress.capacity_rung_number !== expectedIndex + 1 ||
    progress.capacity_rung_count !== rungCount ||
    !approximatelyEqual(progress.capacity_target ?? -1, expectedTarget)
  ) throw new Error("快速性能报告数据结构无效")
  return { index: expectedIndex, target: Number(value.target), success: value.success, progress, metrics, failures, slo_assessment: assessment }
}

function parsePerformanceFailures(value: unknown[]): Array<{ error_code: QuickTestErrorCode; count: number }> {
  const failures: Array<{ error_code: QuickTestErrorCode; count: number }> = []
  const seen = new Set<QuickTestErrorCode>()
  for (const failure of value) {
    if (!isRecord(failure) || !isErrorCode(failure.error_code) || !isPositiveInteger(failure.count) || seen.has(failure.error_code)) {
      throw new Error("快速性能报告数据结构无效")
    }
    seen.add(failure.error_code)
    failures.push({ error_code: failure.error_code, count: failure.count })
  }
  return failures
}

function parsePerformanceRequestBudget(value: unknown): QuickPerformanceRequestBudget {
  if (!isRecord(value)) throw new Error("快速性能报告数据结构无效")
  const fields = ["limit", "warmup_cap", "ramp_cap", "measured_cap", "total_cap"] as const
  if (!fields.every((field) => isNonNegativeInteger(value[field])) || value.limit !== 10_000) {
    throw new Error("快速性能报告数据结构无效")
  }
  const budget = {
    limit: Number(value.limit),
    warmup_cap: Number(value.warmup_cap),
    ramp_cap: Number(value.ramp_cap),
    measured_cap: Number(value.measured_cap),
    total_cap: Number(value.total_cap),
  }
  if (budget.total_cap !== budget.warmup_cap + budget.ramp_cap + budget.measured_cap || budget.total_cap > budget.limit) {
    throw new Error("快速性能报告数据结构无效")
  }
  return budget
}

function parsePerformanceTrafficSummary(value: unknown): QuickPerformanceTrafficSummary {
  if (!isRecord(value)) throw new Error("快速性能报告数据结构无效")
  const integerFields = [
    "request_cap", "offered", "launched", "completed", "succeeded", "failed", "timed_out", "rejected",
    "peak_in_flight", "prompt_tokens", "completion_tokens", "cached_tokens",
  ] as const
  const durationFields = ["send_duration_ms", "drain_duration_ms", "total_duration_ms"] as const
  if (
    !integerFields.every((field) => isNonNegativeInteger(value[field])) ||
    !durationFields.every((field) => isRepresentableDurationMS(value[field])) ||
    !Array.isArray(value.failures) ||
    typeof value.stopped !== "boolean" ||
    typeof value.capped !== "boolean"
  ) throw new Error("快速性能报告数据结构无效")
  const failures: QuickPerformanceTrafficSummary["failures"] = []
  const seen = new Set<QuickTestErrorCode>()
  let previousErrorCode: QuickTestErrorCode | undefined
  for (const failure of value.failures) {
    if (!isRecord(failure) || !isErrorCode(failure.error_code) || !isPositiveInteger(failure.count) ||
      seen.has(failure.error_code) || (previousErrorCode !== undefined && failure.error_code <= previousErrorCode)) {
      throw new Error("快速性能报告数据结构无效")
    }
    seen.add(failure.error_code)
    previousErrorCode = failure.error_code
    failures.push({ error_code: failure.error_code, count: failure.count })
  }
  const summary: QuickPerformanceTrafficSummary = {
    request_cap: Number(value.request_cap),
    offered: Number(value.offered),
    launched: Number(value.launched),
    completed: Number(value.completed),
    succeeded: Number(value.succeeded),
    failed: Number(value.failed),
    timed_out: Number(value.timed_out),
    rejected: Number(value.rejected),
    peak_in_flight: Number(value.peak_in_flight),
    prompt_tokens: Number(value.prompt_tokens),
    completion_tokens: Number(value.completion_tokens),
    cached_tokens: Number(value.cached_tokens),
    send_duration_ms: Number(value.send_duration_ms),
    drain_duration_ms: Number(value.drain_duration_ms),
    total_duration_ms: Number(value.total_duration_ms),
    failures,
    stopped: value.stopped,
    capped: value.capped,
  }
  const failureCount = failures.reduce((sum, failure) => sum + failure.count, 0)
  if (
    summary.request_cap === 0 ||
    summary.offered > summary.request_cap ||
    summary.launched + summary.rejected !== summary.offered ||
    summary.completed !== summary.offered ||
    summary.succeeded + summary.failed !== summary.completed ||
    summary.timed_out > summary.failed ||
    summary.rejected > summary.failed ||
    summary.peak_in_flight > summary.launched ||
    (summary.launched > 0 && summary.peak_in_flight === 0) ||
    summary.cached_tokens > summary.prompt_tokens ||
    failureCount !== summary.failed ||
    !approximatelyEqual(summary.total_duration_ms, summary.send_duration_ms + summary.drain_duration_ms)
  ) throw new Error("快速性能报告数据结构无效")
  return summary
}

function parsePerformanceRamp(value: unknown): QuickPerformanceRamp {
  if (!isRecord(value) || value.shape !== "linear_staircase" || !isPositiveInteger(value.duration_ms) ||
    !isPositiveInteger(value.steps) || value.steps > 10 || typeof value.completed_window !== "boolean") {
    throw new Error("快速性能报告数据结构无效")
  }
  const targetConcurrency = value.target_concurrency
  const targetRate = value.target_rate_per_second
  if ((targetConcurrency !== undefined && !isPositiveInteger(targetConcurrency)) ||
    (targetRate !== undefined && !isPositiveFinite(targetRate)) ||
    ((targetConcurrency === undefined) === (targetRate === undefined))) {
    throw new Error("快速性能报告数据结构无效")
  }
  const traffic = parsePerformanceTrafficSummary(value.traffic)
  if (value.completed_window !== (!traffic.stopped && !traffic.capped)) throw new Error("快速性能报告数据结构无效")
  return {
    shape: "linear_staircase",
    duration_ms: value.duration_ms,
    steps: value.steps,
    ...(targetConcurrency === undefined ? {} : { target_concurrency: targetConcurrency }),
    ...(targetRate === undefined ? {} : { target_rate_per_second: targetRate }),
    completed_window: value.completed_window,
    traffic,
  }
}

function parsePerformanceTimeSlices(
  value: unknown,
  profile: QuickPerformanceProfile,
  totalDurationMS: number,
): QuickPerformanceTimeSlice[] {
  if (!Array.isArray(value) || value.length === 0 || (profile.slice_duration_ms ?? 0) <= 0 || totalDurationMS <= 0) {
    throw new Error("快速性能报告数据结构无效")
  }
  const durationMS = profile.slice_duration_ms ?? 0
  const slices = value.map(parsePerformanceTimeSlice)
  for (let index = 0; index < slices.length; index += 1) {
    const slice = slices[index]
    const previous = slices[index - 1]
    const expectedStartMS = slice.slice_index * durationMS
    const expectedEndMS = Math.min(expectedStartMS + durationMS, totalDurationMS)
    const expectedPartial = expectedEndMS < expectedStartMS + durationMS
    const hasRecordedEvent = slice.offered > 0 || slice.launched > 0 || slice.completed > 0
    if (
      !approximatelyEqual(slice.start_ms, expectedStartMS) ||
      !approximatelyEqual(slice.end_ms, expectedEndMS) ||
      slice.partial !== expectedPartial ||
      (slice.partial && index !== slices.length - 1) ||
      (!hasRecordedEvent && !(slice.partial && index === slices.length - 1)) ||
      (previous !== undefined && (slice.slice_index <= previous.slice_index || slice.start_ms < previous.end_ms))
    ) throw new Error("快速性能报告数据结构无效")
  }
  if (totalDurationMS % durationMS !== 0) {
    const finalSlice = slices[slices.length - 1]
    if (finalSlice.slice_index !== Math.ceil(totalDurationMS / durationMS) - 1 || !finalSlice.partial) {
      throw new Error("快速性能报告数据结构无效")
    }
  }
  return slices
}

function parsePerformanceTimeSlice(value: unknown): QuickPerformanceTimeSlice {
  if (!isRecord(value)) throw new Error("快速性能报告数据结构无效")
  const integerFields = [
    "slice_index", "offered", "launched", "completed", "succeeded", "failed", "rejected",
    "prompt_tokens", "completion_tokens", "cached_tokens",
  ] as const
  if (
    !integerFields.every((field) => isNonNegativeInteger(value[field])) ||
    !isNonNegativeFinite(value.start_ms) ||
    !isNonNegativeFinite(value.end_ms) ||
    value.end_ms <= value.start_ms ||
    typeof value.partial !== "boolean" ||
    Number(value.succeeded) + Number(value.failed) !== Number(value.completed) ||
    Number(value.rejected) > Number(value.failed) ||
    Number(value.cached_tokens) > Number(value.prompt_tokens)
  ) throw new Error("快速性能报告数据结构无效")
  const ttft = parsePerformanceSliceLatency(value.ttft)
  const tpot = parsePerformanceSliceLatency(value.tpot)
  const e2e = parsePerformanceSliceLatency(value.e2e)
  if (ttft.count > Number(value.launched) || tpot.count > ttft.count || e2e.count > Number(value.launched)) {
    throw new Error("快速性能报告数据结构无效")
  }
  return {
    slice_index: Number(value.slice_index),
    start_ms: Number(value.start_ms),
    end_ms: Number(value.end_ms),
    partial: value.partial,
    offered: Number(value.offered),
    launched: Number(value.launched),
    completed: Number(value.completed),
    succeeded: Number(value.succeeded),
    failed: Number(value.failed),
    rejected: Number(value.rejected),
    prompt_tokens: Number(value.prompt_tokens),
    completion_tokens: Number(value.completion_tokens),
    cached_tokens: Number(value.cached_tokens),
    ttft,
    tpot,
    e2e,
  }
}

function parsePerformanceSliceLatency(value: unknown): QuickPerformanceSliceLatency {
  if (!isRecord(value) || !isNonNegativeInteger(value.count) || !isNonNegativeFinite(value.p50_ms) ||
    !isNonNegativeFinite(value.p95_ms) || !isNonNegativeFinite(value.p99_ms) ||
    value.p50_ms > value.p95_ms || value.p95_ms > value.p99_ms ||
    (value.count === 0 && (value.p50_ms !== 0 || value.p95_ms !== 0 || value.p99_ms !== 0))) {
    throw new Error("快速性能报告数据结构无效")
  }
  return { count: value.count, p50_ms: value.p50_ms, p95_ms: value.p95_ms, p99_ms: value.p99_ms }
}

function performancePhaseThreeReportMatches(
  profile: QuickPerformanceProfile,
  progress: QuickPerformanceProgress,
  metrics: QuickPerformanceMetrics,
  budget: QuickPerformanceRequestBudget | undefined,
  warmup: QuickPerformanceTrafficSummary | undefined,
  ramp: QuickPerformanceRamp | undefined,
  slices: QuickPerformanceTimeSlice[] | undefined,
  hasReportError: boolean,
): boolean {
  const configured = (profile.warmup_requests ?? 0) > 0 || (profile.ramp_duration_ms ?? 0) > 0 ||
    (profile.ramp_request_cap ?? 0) > 0 || (profile.slice_duration_ms ?? 0) > 0 || profile.capacity_enabled === true
  const hasPhaseThreeData = budget !== undefined || warmup !== undefined || ramp !== undefined || slices !== undefined
  if (!configured) return !hasPhaseThreeData
  if (budget === undefined) return hasReportError && !hasPhaseThreeData
  if (budget !== undefined && !performanceRequestBudgetMatchesProfile(profile, budget)) return false
  if (
    progress.planned > budget.measured_cap ||
    (progress.offered ?? 0) > budget.measured_cap ||
    progress.launched > budget.measured_cap ||
    progress.completed > budget.measured_cap ||
    (!hasReportError && (progress.planned === 0 || progress.offered === undefined))
  ) return false
  if (warmup !== undefined && (
    (profile.warmup_requests ?? 0) === 0 ||
    warmup.request_cap !== budget.warmup_cap ||
    (!hasReportError && (warmup.stopped || warmup.capped || warmup.offered !== budget.warmup_cap ||
      warmup.launched !== budget.warmup_cap || warmup.rejected !== 0))
  )) return false
  if (!hasReportError && (profile.warmup_requests ?? 0) > 0 && warmup === undefined) return false
  if (ramp !== undefined && (
    (profile.ramp_duration_ms ?? 0) === 0 ||
    ramp.duration_ms !== profile.ramp_duration_ms ||
    ramp.traffic.request_cap !== budget.ramp_cap ||
    ramp.steps !== (profile.load_mode === "fixed_concurrency" ? Math.min(profile.concurrency, 10) : 10) ||
    (!hasReportError && (ramp.traffic.stopped ||
      (ramp.traffic.capped && ramp.traffic.offered !== budget.ramp_cap) ||
      (ramp.completed_window && ramp.traffic.send_duration_ms < ramp.duration_ms))) ||
    (profile.load_mode === "fixed_concurrency"
      ? ramp.target_concurrency !== profile.concurrency || ramp.target_rate_per_second !== undefined
      : ramp.target_rate_per_second !== profile.rate_per_second || ramp.target_concurrency !== undefined)
  )) return false
  if (!hasReportError && (profile.ramp_duration_ms ?? 0) > 0 && ramp === undefined) return false
  if ((profile.slice_duration_ms ?? 0) === 0 && slices !== undefined) return false
  if (!hasReportError && (profile.slice_duration_ms ?? 0) > 0 && slices === undefined) return false
  if (slices !== undefined) {
    const totals = slices.reduce((sum, slice) => ({
      offered: sum.offered + slice.offered,
      launched: sum.launched + slice.launched,
      completed: sum.completed + slice.completed,
      succeeded: sum.succeeded + slice.succeeded,
      failed: sum.failed + slice.failed,
      rejected: sum.rejected + slice.rejected,
      prompt_tokens: sum.prompt_tokens + slice.prompt_tokens,
      completion_tokens: sum.completion_tokens + slice.completion_tokens,
      cached_tokens: sum.cached_tokens + slice.cached_tokens,
    }), { offered: 0, launched: 0, completed: 0, succeeded: 0, failed: 0, rejected: 0, prompt_tokens: 0, completion_tokens: 0, cached_tokens: 0 })
    if (
      (progress.offered !== undefined && totals.offered !== progress.offered) ||
      totals.launched !== progress.launched ||
      totals.completed !== metrics.completed ||
      totals.succeeded !== metrics.succeeded ||
      totals.failed !== metrics.failed ||
      totals.rejected !== progress.rejected ||
      totals.prompt_tokens !== metrics.prompt_tokens ||
      totals.completion_tokens !== metrics.completion_tokens ||
      totals.cached_tokens !== metrics.cached_tokens
    ) return false
  }
  return true
}

function performancePhaseFourReportMatches(
  profile: QuickPerformanceProfile,
  success: boolean,
  progress: QuickPerformanceProgress,
  metrics: QuickPerformanceMetrics,
  failures: Array<{ error_code: QuickTestErrorCode; count: number }>,
  assessment: QuickPerformanceSLOAssessment | undefined,
  capacity: QuickPerformanceCapacityResult | undefined,
  hasReportError: boolean,
): boolean {
  const sloEnabled = performanceSLOEnabled(profile)
  const capacityEnabled = profile.capacity_enabled === true
  if (!sloEnabled) return assessment === undefined && capacity === undefined && !capacityEnabled
  if (assessment === undefined) return hasReportError && capacity === undefined
  if (!capacityEnabled) return capacity === undefined
  if (capacity === undefined) return hasReportError
  const selectedIndex = capacity.selected_rung_index
  if (selectedIndex === undefined) return false
  const selected = capacity.rungs[selectedIndex]
  return selected !== undefined && success === selected.success &&
    performanceProgressEqual(progress, selected.progress) &&
    performanceMetricsEqual(metrics, selected.metrics) &&
    performanceFailuresEqual(failures, selected.failures) &&
    performanceSLOAssessmentEqual(assessment, selected.slo_assessment)
}

function performanceSLOEnabled(profile: QuickPerformanceProfile): boolean {
  return (profile.slo_target_percent ?? 0) > 0
}

function performanceCapacityTargets(profile: QuickPerformanceProfile): number[] | undefined {
  if (profile.capacity_enabled !== true) return undefined
  const maximum = profile.load_mode === "fixed_concurrency" ? profile.concurrency : profile.rate_per_second ?? 0
  const start = profile.capacity_start ?? 0
  const step = profile.capacity_step ?? 0
  if (!(start > 0 && step > 0 && maximum > 0 && start <= maximum)) return undefined
  const targets: number[] = []
  for (let index = 0; index < 20; index += 1) {
    const target = start + index * step
    if (target >= maximum) break
    targets.push(target)
  }
  if (targets.length >= 20) return undefined
  targets.push(maximum)
  return targets
}

function performanceProgressEqual(left: QuickPerformanceProgress, right: QuickPerformanceProgress): boolean {
  return JSON.stringify(pickPerformanceProgress(left, true)) === JSON.stringify(pickPerformanceProgress(right, true))
}

function performanceMetricsEqual(left: QuickPerformanceMetrics, right: QuickPerformanceMetrics): boolean {
  return JSON.stringify(pickPerformanceMetrics(left, 2)) === JSON.stringify(pickPerformanceMetrics(right, 2))
}

function performanceFailuresEqual(
  left: Array<{ error_code: QuickTestErrorCode; count: number }>,
  right: Array<{ error_code: QuickTestErrorCode; count: number }>,
): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

function performanceSLOAssessmentEqual(left: QuickPerformanceSLOAssessment, right: QuickPerformanceSLOAssessment): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}

function performanceRequestBudgetMatchesProfile(profile: QuickPerformanceProfile, budget: QuickPerformanceRequestBudget): boolean {
  const warmupCap = profile.warmup_requests ?? 0
  const rampDurationSeconds = (profile.ramp_duration_ms ?? 0) / 1_000
  const rampCap = rampDurationSeconds === 0
    ? 0
    : profile.load_mode === "fixed_concurrency"
      ? profile.ramp_request_cap ?? 0
      : estimateOpenLoopRequestCap(rampDurationSeconds * (profile.rate_per_second ?? 0) * 0.55, profile.arrival_pattern ?? "constant")
  const capacityTargets = performanceCapacityTargets(profile)
  const measuredCap = profile.request_count > 0
    ? profile.request_count * (capacityTargets?.length ?? 1)
    : profile.load_mode === "fixed_concurrency"
      ? budget.limit - warmupCap - rampCap
      : estimateQuickPerformanceOpenLoopRequestCap(profile.duration_ms, profile.rate_per_second ?? 0, profile.arrival_pattern ?? "constant")
  return measuredCap > 0 && budget.warmup_cap === warmupCap && budget.ramp_cap === rampCap &&
    budget.measured_cap === measuredCap && budget.total_cap === warmupCap + rampCap + measuredCap
}

function estimateOpenLoopRequestCap(intensity: number, pattern: QuickPerformanceArrivalPattern): number {
  return pattern === "poisson" ? Math.ceil(2 * intensity) + 1 : Math.ceil(intensity)
}

export function estimateQuickPerformanceOpenLoopRequestCap(
  durationMS: number,
  ratePerSecond: number,
  pattern: QuickPerformanceArrivalPattern,
): number {
  const intensity = durationMS * ratePerSecond / 1_000
  if (pattern === "poisson") return Math.ceil(2 * intensity) + 1
  const intervalNanoseconds = 1_000_000_000 / ratePerSecond
  return Math.ceil(durationMS * 1_000_000 / intervalNanoseconds)
}

function isPerformanceProfile(value: unknown, schemaVersion: 1 | 2): value is QuickPerformanceProfile {
  if (!(isRecord(value) &&
    isNonNegativeInteger(value.request_count) &&
    isNonNegativeInteger(value.duration_ms) &&
    isNonNegativeInteger(value.concurrency) &&
    isNonNegativeInteger(value.timeout_ms) &&
    isNonNegativeInteger(value.input_tokens) &&
    isNonNegativeInteger(value.output_tokens))) return false
  if (schemaVersion === 1) return true
  return isPerformanceLoadMode(value.load_mode) &&
    (value.rate_per_second === undefined || isNonNegativeFinite(value.rate_per_second)) &&
    (value.max_in_flight === undefined || isNonNegativeInteger(value.max_in_flight)) &&
    (value.arrival_pattern === undefined || isPerformanceArrivalPattern(value.arrival_pattern)) &&
    (value.workload_mode === undefined || isPerformanceWorkloadMode(value.workload_mode)) &&
    (value.random_seed === undefined || isUint32(value.random_seed)) &&
    (value.input_tokens_stddev === undefined || isUint32(value.input_tokens_stddev)) &&
    (value.output_tokens_stddev === undefined || isUint32(value.output_tokens_stddev)) &&
    (value.shared_prefix_tokens === undefined || isUint32(value.shared_prefix_tokens)) &&
    (value.warmup_requests === undefined || (isNonNegativeInteger(value.warmup_requests) && value.warmup_requests <= 10_000)) &&
    (value.ramp_duration_ms === undefined || (isNonNegativeInteger(value.ramp_duration_ms) && value.ramp_duration_ms <= 3_600_000)) &&
    (value.ramp_request_cap === undefined || (isNonNegativeInteger(value.ramp_request_cap) && value.ramp_request_cap <= 10_000)) &&
    (value.slice_duration_ms === undefined || (isNonNegativeInteger(value.slice_duration_ms) && value.slice_duration_ms <= 3_600_000)) &&
    (value.slo_ttft_ms === undefined || isNonNegativeFinite(value.slo_ttft_ms)) &&
    (value.slo_tpot_ms === undefined || isNonNegativeFinite(value.slo_tpot_ms)) &&
    (value.slo_e2e_ms === undefined || isNonNegativeFinite(value.slo_e2e_ms)) &&
    (value.slo_target_percent === undefined || (isNonNegativeFinite(value.slo_target_percent) && value.slo_target_percent <= 100)) &&
    (value.capacity_enabled === undefined || typeof value.capacity_enabled === "boolean") &&
    (value.capacity_start === undefined || isNonNegativeFinite(value.capacity_start)) &&
    (value.capacity_step === undefined || isNonNegativeFinite(value.capacity_step))
}

function isRunnablePerformanceProfile(value: QuickPerformanceProfile, schemaVersion: 1 | 2): boolean {
  if (!((value.request_count > 0 || value.duration_ms > 0) &&
    value.timeout_ms > 0 && value.input_tokens > 0 && value.output_tokens > 0)) return false
  if (schemaVersion === 1) return value.concurrency > 0
  if (value.load_mode === "fixed_concurrency") {
    if (!(value.concurrency > 0 && (value.rate_per_second ?? 0) === 0 && (value.max_in_flight ?? 0) === 0)) return false
  } else if (!(value.load_mode === "open_loop" && value.concurrency === 0 &&
    (value.rate_per_second ?? 0) > 0 && (value.max_in_flight ?? 0) > 0)) {
    return false
  }
  const arrivalPattern = value.arrival_pattern ?? "constant"
  const workloadMode = value.workload_mode ?? "fixed"
  const randomSeed = value.random_seed ?? 0
  const inputStdDev = value.input_tokens_stddev ?? 0
  const outputStdDev = value.output_tokens_stddev ?? 0
  const sharedPrefix = value.shared_prefix_tokens ?? 0
  const rampDurationMS = value.ramp_duration_ms ?? 0
  const rampRequestCap = value.ramp_request_cap ?? 0
  const sloTargetPercent = value.slo_target_percent ?? 0
  const hasSLOThreshold = (value.slo_ttft_ms ?? 0) > 0 || (value.slo_tpot_ms ?? 0) > 0 || (value.slo_e2e_ms ?? 0) > 0
  if (arrivalPattern === "poisson" && value.load_mode !== "open_loop") return false
  if ((arrivalPattern === "poisson" || workloadMode === "normal") !== (randomSeed > 0)) return false
  if (value.load_mode === "fixed_concurrency") {
    if ((rampDurationMS > 0) !== (rampRequestCap > 0)) return false
  } else if (rampRequestCap !== 0) return false
  if ((sloTargetPercent > 0) !== hasSLOThreshold) return false
  if (value.capacity_enabled === true) {
    const start = value.capacity_start ?? 0
    const step = value.capacity_step ?? 0
    if (!hasSLOThreshold || value.request_count === 0 || rampDurationMS !== 0 || performanceCapacityTargets(value) === undefined) return false
    if (value.load_mode === "fixed_concurrency") {
      if (!Number.isInteger(start) || !Number.isInteger(step)) return false
    } else if (start < 0.01) return false
  } else if ((value.capacity_start ?? 0) !== 0 || (value.capacity_step ?? 0) !== 0) return false
  if (workloadMode === "fixed") return inputStdDev === 0 && outputStdDev === 0 && sharedPrefix === 0
  return inputStdDev <= value.input_tokens && outputStdDev <= value.output_tokens && sharedPrefix < value.input_tokens
}

function pickPerformanceProfile(value: QuickPerformanceProfile, schemaVersion: 1 | 2): QuickPerformanceProfile {
  return {
    ...(schemaVersion === 2 ? { load_mode: value.load_mode } : {}),
    request_count: value.request_count,
    duration_ms: value.duration_ms,
    concurrency: value.concurrency,
    ...(schemaVersion === 2 && value.rate_per_second !== undefined ? { rate_per_second: value.rate_per_second } : {}),
    ...(schemaVersion === 2 && value.max_in_flight !== undefined ? { max_in_flight: value.max_in_flight } : {}),
    ...(schemaVersion === 2 && value.arrival_pattern !== undefined ? { arrival_pattern: value.arrival_pattern } : {}),
    ...(schemaVersion === 2 && value.workload_mode !== undefined ? { workload_mode: value.workload_mode } : {}),
    ...(schemaVersion === 2 && value.random_seed !== undefined ? { random_seed: value.random_seed } : {}),
    ...(schemaVersion === 2 && value.input_tokens_stddev !== undefined ? { input_tokens_stddev: value.input_tokens_stddev } : {}),
    ...(schemaVersion === 2 && value.output_tokens_stddev !== undefined ? { output_tokens_stddev: value.output_tokens_stddev } : {}),
    ...(schemaVersion === 2 && value.shared_prefix_tokens !== undefined ? { shared_prefix_tokens: value.shared_prefix_tokens } : {}),
    ...(schemaVersion === 2 && value.warmup_requests !== undefined ? { warmup_requests: value.warmup_requests } : {}),
    ...(schemaVersion === 2 && value.ramp_duration_ms !== undefined ? { ramp_duration_ms: value.ramp_duration_ms } : {}),
    ...(schemaVersion === 2 && value.ramp_request_cap !== undefined ? { ramp_request_cap: value.ramp_request_cap } : {}),
    ...(schemaVersion === 2 && value.slice_duration_ms !== undefined ? { slice_duration_ms: value.slice_duration_ms } : {}),
    ...(schemaVersion === 2 && value.slo_ttft_ms !== undefined ? { slo_ttft_ms: value.slo_ttft_ms } : {}),
    ...(schemaVersion === 2 && value.slo_tpot_ms !== undefined ? { slo_tpot_ms: value.slo_tpot_ms } : {}),
    ...(schemaVersion === 2 && value.slo_e2e_ms !== undefined ? { slo_e2e_ms: value.slo_e2e_ms } : {}),
    ...(schemaVersion === 2 && value.slo_target_percent !== undefined ? { slo_target_percent: value.slo_target_percent } : {}),
    ...(schemaVersion === 2 && value.capacity_enabled !== undefined ? { capacity_enabled: value.capacity_enabled } : {}),
    ...(schemaVersion === 2 && value.capacity_start !== undefined ? { capacity_start: value.capacity_start } : {}),
    ...(schemaVersion === 2 && value.capacity_step !== undefined ? { capacity_step: value.capacity_step } : {}),
    timeout_ms: value.timeout_ms,
    input_tokens: value.input_tokens,
    output_tokens: value.output_tokens,
  }
}

function pickPerformanceProgress(value: QuickPerformanceProgress, includeOffered: boolean): QuickPerformanceProgress {
  return {
    phase: value.phase,
    planned: value.planned,
    ...(!includeOffered || value.offered === undefined ? {} : { offered: value.offered }),
    launched: value.launched,
    completed: value.completed,
    in_flight: value.in_flight ?? 0,
    peak_in_flight: value.peak_in_flight,
    succeeded: value.succeeded,
    failed: value.failed,
    rejected: value.rejected,
    ...(!includeOffered || value.stopped === undefined ? {} : { stopped: value.stopped }),
    ...(!includeOffered || value.capped === undefined ? {} : { capped: value.capped }),
    ...(!includeOffered || value.capacity_rung_number === undefined ? {} : {
      capacity_rung_number: value.capacity_rung_number,
      capacity_rung_count: value.capacity_rung_count,
      capacity_target: value.capacity_target,
    }),
    send_duration_ms: value.send_duration_ms,
    drain_duration_ms: value.drain_duration_ms,
    total_duration_ms: value.total_duration_ms,
  }
}

function pickPerformanceMetrics(value: QuickPerformanceMetrics, schemaVersion: 1 | 2): QuickPerformanceMetrics {
  return {
    completed: value.completed,
    succeeded: value.succeeded,
    failed: value.failed,
    timed_out: value.timed_out,
    success_rate_percent: value.success_rate_percent,
    ...(schemaVersion === 2 && value.offered_qps !== undefined ? { offered_qps: value.offered_qps } : {}),
    ...(schemaVersion === 2 && value.launched_qps !== undefined ? { launched_qps: value.launched_qps } : {}),
    ...(schemaVersion === 2 && value.completed_qps !== undefined ? { completed_qps: value.completed_qps } : {}),
    ...(schemaVersion === 2 && value.successful_request_qps !== undefined ? { successful_request_qps: value.successful_request_qps } : {}),
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
  if (!isRecord(value)) return false
  const hasCapacityRung = value.capacity_rung_number !== undefined || value.capacity_rung_count !== undefined || value.capacity_target !== undefined
  if (hasCapacityRung && (!isPositiveInteger(value.capacity_rung_number) || !isPositiveInteger(value.capacity_rung_count) ||
    Number(value.capacity_rung_number) > Number(value.capacity_rung_count) || !isPositiveFinite(value.capacity_target))) return false
  return isPerformancePhase(value.phase) &&
    isNonNegativeInteger(value.planned) &&
    (value.offered === undefined || isNonNegativeInteger(value.offered)) &&
    isNonNegativeInteger(value.launched) &&
    isNonNegativeInteger(value.completed) &&
    (value.in_flight === undefined || isNonNegativeInteger(value.in_flight)) &&
    isNonNegativeInteger(value.peak_in_flight) &&
    isNonNegativeInteger(value.succeeded) &&
    isNonNegativeInteger(value.failed) &&
    isNonNegativeInteger(value.rejected) &&
    (value.stopped === undefined || typeof value.stopped === "boolean") &&
    (value.capped === undefined || typeof value.capped === "boolean") &&
    isRepresentableDurationMS(value.send_duration_ms) &&
    isRepresentableDurationMS(value.drain_duration_ms) &&
    isRepresentableDurationMS(value.total_duration_ms)
}

function isPerformanceMetrics(value: unknown, schemaVersion: 1 | 2 = 1): value is QuickPerformanceMetrics {
  if (!isRecord(value)) return false
  const integerFields = ["completed", "succeeded", "failed", "timed_out", "prompt_tokens", "completion_tokens", "cached_tokens"] as const
  const numberFields = [
    "success_rate_percent", "request_qps", "rpm", "input_tpm", "output_tpm", "total_tpm", "generation_tps",
    "ttft_p50_ms", "ttft_p90_ms", "ttft_p95_ms", "ttft_p99_ms", "ttft_average_ms",
    "tpot_p50_ms", "tpot_p90_ms", "tpot_p95_ms", "tpot_p99_ms", "tpot_average_ms",
    "e2e_p50_ms", "e2e_p90_ms", "e2e_p95_ms", "e2e_p99_ms", "e2e_average_ms",
    "schedule_lag_p50_ms", "schedule_lag_p95_ms", "schedule_lag_average_ms", "cache_rate_percent",
  ] as const
  const v2Fields = ["offered_qps", "launched_qps", "completed_qps", "successful_request_qps"] as const
  return integerFields.every((field) => isNonNegativeInteger(value[field])) &&
    numberFields.every((field) => isNonNegativeFinite(value[field])) &&
    (schemaVersion === 1 || v2Fields.every((field) => value[field] === undefined || isNonNegativeFinite(value[field]))) &&
    (value.schedule_lag_p90_ms === undefined || isNonNegativeFinite(value.schedule_lag_p90_ms)) &&
    (value.schedule_lag_p99_ms === undefined || isNonNegativeFinite(value.schedule_lag_p99_ms)) &&
    Number(value.success_rate_percent) <= 100 && Number(value.cache_rate_percent) <= 100
}

function isPerformancePhase(value: unknown): value is QuickPerformancePhase {
  return value === "not_started" || value === "warming_up" || value === "ramping" || value === "sending" || value === "draining" || value === "completed" || value === "cancelled"
}

function isPerformanceLoadMode(value: unknown): value is QuickPerformanceLoadMode {
  return value === "fixed_concurrency" || value === "open_loop"
}

function isPerformanceArrivalPattern(value: unknown): value is QuickPerformanceArrivalPattern {
  return value === "constant" || value === "poisson"
}

function isPerformanceWorkloadMode(value: unknown): value is QuickPerformanceWorkloadMode {
  return value === "fixed" || value === "normal"
}

function isPerformanceSLOStatus(value: unknown): value is QuickPerformanceSLOStatus {
  return value === "not_evaluated" || value === "passed" || value === "failed"
}

function performanceSampleTargetsMatchWorkload(profile: QuickPerformanceProfile, samples: QuickPerformanceSample[]): boolean {
  const hasTargets = (sample: QuickPerformanceSample) => sample.target_input_tokens !== undefined && sample.target_output_tokens !== undefined
  return (profile.workload_mode ?? "fixed") === "normal"
    ? samples.every(hasTargets)
    : samples.every((sample) => !hasTargets(sample))
}

function isArchiveStatus(value: unknown): value is QuickPerformanceArchiveStatus {
  return value === "not_attempted" || value === "archived" || value === "failed"
}

function isEvidenceCaptureStatus(value: unknown): value is QuickPerformanceEvidenceCaptureStatus {
  return value === "captured" || value === "empty" || value === "omitted"
}

function isUint32(value: unknown): value is number {
  return isNonNegativeInteger(value) && value <= 4_294_967_295
}

function isPositiveUint32(value: unknown): value is number {
  return isPositiveInteger(value) && value <= 4_294_967_295
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

function isPositiveFinite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0
}

function approximatelyEqual(left: number, right: number): boolean {
  if (!Number.isFinite(left) || !Number.isFinite(right)) return false
  const scale = Math.max(1, Math.abs(left), Math.abs(right))
  return Math.abs(left - right) <= scale * 1e-9
}

function isRepresentableDurationMS(value: unknown): value is number {
  return isNonNegativeFinite(value) && (value === 0 || value >= 1e-6)
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) > 0
}
