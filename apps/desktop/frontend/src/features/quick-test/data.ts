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
  | "semantic_empty"
  | "response_too_large"
  | "client_closed"
  | "executor_panic"
  | "request_failed"
  | "unclassified_error"

export interface QuickTestCommand {
  address_mode: QuickTestAddressMode
  url: string
  api_key: string
  model_id: string
  prompt: string
  timeout_ms: number
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
  "semantic_empty",
  "response_too_large",
  "client_closed",
  "executor_panic",
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
  semantic_empty: "接口未返回有效模型内容",
  response_too_large: "接口响应超过安全限制",
  client_closed: "测试客户端已关闭",
  executor_panic: "测试执行器异常",
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

function isNonNegativeFinite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isInteger(value) && Number(value) >= 0
}
