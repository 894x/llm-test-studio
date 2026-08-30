export type CatalogProtocol = "openai-chat" | "kimi-k3" | "seedance"
export type CatalogLoadMode = "single" | "fixed_concurrency" | "open_loop"
export type CatalogCaseSeverity = "normal" | "critical"
export type CatalogCaseExecutionMode = "automatic" | "manual"

export interface CatalogModel {
  id: string
  revision: number
  name: string
  protocol: CatalogProtocol
  capabilities: string[]
}

export interface CatalogChannel {
  id: string
  revision: number
  name: string
  base_url: string
  protocol: CatalogProtocol
  enabled: boolean
  credential_configured: boolean
  model_count: number
}

export interface CatalogChannelModel {
  id: string
  revision: number
  channel_id: string
  model_id: string
  upstream_model_name: string
}

export interface CatalogTestCase {
  id: string
  revision: number
  key: string
  name: string
  dimension: string
  protocol: CatalogProtocol
  enabled: boolean
  default: boolean
  severity: CatalogCaseSeverity
  execution_mode: CatalogCaseExecutionMode
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  path: string
  assertion_kinds: string[]
}

export interface CatalogSuite {
  id: string
  revision: number
  name: string
  case_count: number
}

export interface CatalogPlan {
  id: string
  revision: number
  name: string
  model_count: number
  channel_count: number
  case_count: number
  load_mode: CatalogLoadMode
  concurrency: number
  request_count: number
  rate_per_second: number
  duration_ms: number
  request_timeout_ms: number
}

export interface CatalogSnapshot {
  schema_version: 1
  models: CatalogModel[]
  channels: CatalogChannel[]
  channel_models: CatalogChannelModel[]
  test_cases: CatalogTestCase[]
  suites: CatalogSuite[]
  plans: CatalogPlan[]
}

export const EMPTY_CATALOG: CatalogSnapshot = {
  schema_version: 1,
  models: [],
  channels: [],
  channel_models: [],
  test_cases: [],
  suites: [],
  plans: [],
}

export function parseCatalogSnapshot(value: unknown): CatalogSnapshot {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new Error("桌面目录数据协议版本不受支持")
  }
  if (
    !Array.isArray(value.models) ||
    !Array.isArray(value.channels) ||
    !Array.isArray(value.channel_models) ||
    !Array.isArray(value.test_cases) ||
    !Array.isArray(value.suites) ||
    !Array.isArray(value.plans)
  ) {
    throw new Error("桌面目录数据结构无效")
  }

  const models = value.models.map(parseModel)
  const channels = value.channels.map(parseChannel)
  const channelModels = value.channel_models.map(parseChannelModel)
  const testCases = value.test_cases.map(parseTestCase)
  const suites = value.suites.map(parseSuite)
  const plans = value.plans.map(parsePlan)
  const groups = [models, channels, channelModels, testCases, suites, plans]
  if (groups.some((items) => new Set(items.map((item) => item.id)).size !== items.length)) {
    throw new Error("桌面目录数据包含重复标识")
  }

  const modelByID = new Map(models.map((model) => [model.id, model]))
  const channelByID = new Map(channels.map((channel) => [channel.id, channel]))
  const mappedCounts = new Map<string, number>()
  for (const mapping of channelModels) {
    const model = modelByID.get(mapping.model_id)
    const channel = channelByID.get(mapping.channel_id)
    if (!model || !channel || model.protocol !== channel.protocol) {
      throw new Error("桌面目录模型映射引用无效")
    }
    mappedCounts.set(mapping.channel_id, (mappedCounts.get(mapping.channel_id) ?? 0) + 1)
  }
  if (
    channels.some(
      (channel) => channel.model_count !== (mappedCounts.get(channel.id) ?? 0),
    )
  ) {
    throw new Error("桌面目录渠道模型计数无效")
  }

  return {
    schema_version: 1,
    models,
    channels,
    channel_models: channelModels,
    test_cases: testCases,
    suites,
    plans,
  }
}

function parseModel(value: unknown): CatalogModel {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isProtocol(value.protocol) ||
    !isUniqueStrings(value.capabilities)
  ) {
    throw new Error("桌面目录模型数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    protocol: value.protocol,
    capabilities: [...value.capabilities],
  }
}

function parseChannel(value: unknown): CatalogChannel {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isSafeServiceURL(value.base_url) ||
    !isProtocol(value.protocol) ||
    typeof value.enabled !== "boolean" ||
    typeof value.credential_configured !== "boolean" ||
    !isNonNegativeInteger(value.model_count)
  ) {
    throw new Error("桌面目录渠道数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    base_url: value.base_url,
    protocol: value.protocol,
    enabled: value.enabled,
    credential_configured: value.credential_configured,
    model_count: value.model_count,
  }
}

function parseChannelModel(value: unknown): CatalogChannelModel {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isUUID(value.channel_id) ||
    !isUUID(value.model_id) ||
    !isNonBlank(value.upstream_model_name)
  ) {
    throw new Error("桌面目录模型映射数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    channel_id: value.channel_id,
    model_id: value.model_id,
    upstream_model_name: value.upstream_model_name,
  }
}

function parseTestCase(value: unknown): CatalogTestCase {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isSafeCaseKey(value.key) ||
    !isNonBlank(value.name) ||
    !isSafeDimension(value.dimension) ||
    !isProtocol(value.protocol) ||
    typeof value.enabled !== "boolean" ||
    typeof value.default !== "boolean" ||
    (value.default && !value.enabled) ||
    !isCaseSeverity(value.severity) ||
    !isCaseExecutionMode(value.execution_mode) ||
    !isMethod(value.method) ||
    !isRequestPath(value.path) ||
    !isStringList(value.assertion_kinds) ||
    value.assertion_kinds.length === 0
  ) {
    throw new Error("桌面目录测试用例数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    key: value.key,
    name: value.name,
    dimension: value.dimension,
    protocol: value.protocol,
    enabled: value.enabled,
    default: value.default,
    severity: value.severity,
    execution_mode: value.execution_mode,
    method: value.method,
    path: value.path,
    assertion_kinds: [...value.assertion_kinds],
  }
}

function parseSuite(value: unknown): CatalogSuite {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isPositiveInteger(value.case_count)
  ) {
    throw new Error("桌面目录测试套件数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    case_count: value.case_count,
  }
}

function parsePlan(value: unknown): CatalogPlan {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isPositiveInteger(value.model_count) ||
    !isPositiveInteger(value.channel_count) ||
    !isPositiveInteger(value.case_count) ||
    !isLoadMode(value.load_mode) ||
    !isPositiveInteger(value.concurrency) ||
    !isNonNegativeInteger(value.request_count) ||
    !isNonNegativeFinite(value.rate_per_second) ||
    !isNonNegativeInteger(value.duration_ms) ||
    (value.request_count === 0 && value.duration_ms === 0) ||
    !isPositiveInteger(value.request_timeout_ms)
  ) {
    throw new Error("桌面目录测试计划数据无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    model_count: value.model_count,
    channel_count: value.channel_count,
    case_count: value.case_count,
    load_mode: value.load_mode,
    concurrency: value.concurrency,
    request_count: value.request_count,
    rate_per_second: value.rate_per_second,
    duration_ms: value.duration_ms,
    request_timeout_ms: value.request_timeout_ms,
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

function isPositiveInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) > 0
}

function isNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
}

function isNonNegativeFinite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
}

function isNonBlank(value: unknown): value is string {
  return typeof value === "string" && value.trim() === value && value.length > 0
}

function isUniqueStrings(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.every(isNonBlank) &&
    new Set(value).size === value.length
  )
}

function isStringList(value: unknown): value is string[] {
  return Array.isArray(value) && value.every(isNonBlank)
}

function isProtocol(value: unknown): value is CatalogProtocol {
  return value === "openai-chat" || value === "kimi-k3" || value === "seedance"
}

function isLoadMode(value: unknown): value is CatalogLoadMode {
  return value === "single" || value === "fixed_concurrency" || value === "open_loop"
}

function isCaseSeverity(value: unknown): value is CatalogCaseSeverity {
  return value === "normal" || value === "critical"
}

function isCaseExecutionMode(value: unknown): value is CatalogCaseExecutionMode {
  return value === "automatic" || value === "manual"
}

function isSafeCaseKey(value: unknown): value is string {
  return (
    isNonBlank(value) &&
    /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value)
  )
}

function isSafeDimension(value: unknown): value is string {
  return isNonBlank(value)
}

function isMethod(value: unknown): value is CatalogTestCase["method"] {
  return value === "GET" || value === "POST" || value === "PUT" || value === "PATCH" || value === "DELETE"
}

function isRequestPath(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.startsWith("/") &&
    !value.startsWith("//") &&
    !/[?#\\]/.test(value) &&
    !value.split("/").some((part) => part === "." || part === "..")
  )
}

function isSafeServiceURL(value: unknown): value is string {
  if (typeof value !== "string" || value.trim() !== value) return false
  try {
    const parsed = new URL(value)
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.username === "" &&
      parsed.password === "" &&
      parsed.search === "" &&
      parsed.hash === ""
    )
  } catch {
    return false
  }
}
