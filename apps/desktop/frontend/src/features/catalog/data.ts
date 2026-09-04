export type CatalogProtocol = "openai-chat" | "kimi-k3" | "seedance"
export type CatalogLoadMode = "single" | "fixed_concurrency" | "open_loop"
export type CatalogCaseSeverity = "normal" | "critical"
export type CatalogCaseExecutionMode = "automatic" | "manual"

export interface CatalogCaseTypeDescriptor {
  type: string
  type_version: number
  label: string
  category: string
  scheduling_owner: "case" | "plan"
  supported_protocols: CatalogProtocol[]
  creatable: boolean
  default_spec: Record<string, unknown>
}

export interface CatalogCaseRevision {
  case_id: string
  revision: number
}

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
  definition_schema_version: number
  type: string
  type_version: number
  spec: Record<string, unknown>
}

export interface CatalogSuite {
  id: string
  revision: number
  name: string
  case_count: number
  cases: CatalogCaseRevision[]
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
  model_ids: string[]
  channel_ids: string[]
  suite_id?: string
  suite_revision?: number
  cases: CatalogCaseRevision[]
  sla_thresholds: Record<string, number>
}

export type CreateModelCommand = Pick<CatalogModel, "name" | "protocol" | "capabilities">
export type UpdateModelCommand = CreateModelCommand & { id: string; expected_revision: number }
export type CreateChannelCommand = Pick<CatalogChannel, "name" | "base_url" | "protocol" | "enabled"> & { api_key: string }
export type UpdateChannelCommand = CreateChannelCommand & { id: string; expected_revision: number }
export type CreateChannelModelCommand = Pick<CatalogChannelModel, "channel_id" | "model_id" | "upstream_model_name">
export type UpdateChannelModelCommand = Pick<CatalogChannelModel, "upstream_model_name"> & { id: string; expected_revision: number }
export type CreateTestCaseCommand = Pick<CatalogTestCase,
  "key" | "name" | "dimension" | "protocol" | "enabled" | "default" | "severity" |
  "execution_mode" | "definition_schema_version" | "type" | "type_version" | "spec"
>
export type UpdateTestCaseCommand = CreateTestCaseCommand & { id: string; expected_revision: number }
export type CreateSuiteCommand = Pick<CatalogSuite, "name" | "cases">
export type UpdateSuiteCommand = CreateSuiteCommand & { id: string; expected_revision: number }
export type CreatePlanCommand = Pick<CatalogPlan,
  "name" | "model_ids" | "channel_ids" | "suite_id" | "suite_revision" | "cases" | "load_mode" |
  "concurrency" | "request_count" | "rate_per_second" | "duration_ms" | "request_timeout_ms" | "sla_thresholds"
>
export type UpdatePlanCommand = CreatePlanCommand & { id: string; expected_revision: number }
export interface DeleteCommand { id: string; expected_revision: number }

export interface CatalogActions {
  createModel(command: CreateModelCommand): Promise<CatalogSnapshot>
  updateModel(command: UpdateModelCommand): Promise<CatalogSnapshot>
  deleteModel(command: DeleteCommand): Promise<CatalogSnapshot>
  createChannel(command: CreateChannelCommand): Promise<CatalogSnapshot>
  updateChannel(command: UpdateChannelCommand): Promise<CatalogSnapshot>
  deleteChannel(command: DeleteCommand): Promise<CatalogSnapshot>
  createChannelModel(command: CreateChannelModelCommand): Promise<CatalogSnapshot>
  updateChannelModel(command: UpdateChannelModelCommand): Promise<CatalogSnapshot>
  deleteChannelModel(command: DeleteCommand): Promise<CatalogSnapshot>
  createTestCase(command: CreateTestCaseCommand): Promise<CatalogSnapshot>
  updateTestCase(command: UpdateTestCaseCommand): Promise<CatalogSnapshot>
  deleteTestCase(command: DeleteCommand): Promise<CatalogSnapshot>
  createSuite(command: CreateSuiteCommand): Promise<CatalogSnapshot>
  updateSuite(command: UpdateSuiteCommand): Promise<CatalogSnapshot>
  deleteSuite(command: DeleteCommand): Promise<CatalogSnapshot>
  createPlan(command: CreatePlanCommand): Promise<CatalogSnapshot>
  updatePlan(command: UpdatePlanCommand): Promise<CatalogSnapshot>
  deletePlan(command: DeleteCommand): Promise<CatalogSnapshot>
}

export interface CatalogSnapshot {
  schema_version: 2
  case_types: CatalogCaseTypeDescriptor[]
  models: CatalogModel[]
  channels: CatalogChannel[]
  channel_models: CatalogChannelModel[]
  test_cases: CatalogTestCase[]
  suites: CatalogSuite[]
  plans: CatalogPlan[]
}

export const EMPTY_CATALOG: CatalogSnapshot = {
  schema_version: 2,
  case_types: [],
  models: [],
  channels: [],
  channel_models: [],
  test_cases: [],
  suites: [],
  plans: [],
}

export function parseCatalogSnapshot(value: unknown): CatalogSnapshot {
  if (!isRecord(value) || value.schema_version !== 2) {
    throw new Error("桌面目录数据协议版本不受支持")
  }
  if (
    !Array.isArray(value.case_types) ||
    !Array.isArray(value.models) ||
    !Array.isArray(value.channels) ||
    !Array.isArray(value.channel_models) ||
    !Array.isArray(value.test_cases) ||
    !Array.isArray(value.suites) ||
    !Array.isArray(value.plans)
  ) {
    throw new Error("桌面目录数据结构无效")
  }

  const caseTypes = value.case_types.map(parseCaseTypeDescriptor)
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
  if (new Set(caseTypes.map((descriptor) => `${descriptor.type}@${descriptor.type_version}`)).size !== caseTypes.length) {
    throw new Error("桌面目录用例类型重复")
  }
  const caseTypeByKey = new Map(caseTypes.map((descriptor) => [`${descriptor.type}@${descriptor.type_version}`, descriptor]))
  for (const testCase of testCases) {
    const descriptor = caseTypeByKey.get(`${testCase.type}@${testCase.type_version}`)
    if (!descriptor || !descriptor.supported_protocols.includes(testCase.protocol)) {
      throw new Error("桌面目录测试用例类型无效")
    }
  }

  const modelByID = new Map(models.map((model) => [model.id, model]))
  const channelByID = new Map(channels.map((channel) => [channel.id, channel]))
  const suiteByID = new Map(suites.map((suite) => [suite.id, suite]))
  const mappedCounts = new Map<string, number>()
  const mappedBindings = new Set<string>()
  for (const mapping of channelModels) {
    const model = modelByID.get(mapping.model_id)
    const channel = channelByID.get(mapping.channel_id)
    if (!model || !channel || model.protocol !== channel.protocol) {
      throw new Error("桌面目录模型映射引用无效")
    }
    const binding = `${mapping.channel_id}\u0000${mapping.model_id}`
    if (mappedBindings.has(binding)) throw new Error("桌面目录模型映射重复")
    mappedBindings.add(binding)
    mappedCounts.set(mapping.channel_id, (mappedCounts.get(mapping.channel_id) ?? 0) + 1)
  }
  if (
    channels.some(
      (channel) => channel.model_count !== (mappedCounts.get(channel.id) ?? 0),
    )
  ) {
    throw new Error("桌面目录渠道模型计数无效")
  }
  for (const suite of suites) {
		if (!hasUniqueCaseIDs(suite.cases)) throw new Error("桌面目录测试套件引用无效")
  }
  for (const plan of plans) {
    if (
      plan.model_ids.some((id) => !modelByID.has(id)) ||
      plan.channel_ids.some((id) => !channelByID.has(id)) ||
			!hasUniqueCaseIDs(plan.cases) ||
      plan.model_ids.some((modelID) => plan.channel_ids.some((channelID) => !mappedBindings.has(`${channelID}\u0000${modelID}`)))
    ) {
      throw new Error("桌面目录测试计划引用无效")
    }
    if (plan.suite_id !== undefined) {
      const suite = suiteByID.get(plan.suite_id)
      if (!suite || plan.suite_revision === undefined || plan.suite_revision > suite.revision) {
        throw new Error("桌面目录测试计划套件引用无效")
      }
    }
  }

  return {
    schema_version: 2,
    case_types: caseTypes,
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
    !isPositiveInteger(value.definition_schema_version) ||
    !isSafeCaseType(value.type) ||
    !isPositiveInteger(value.type_version) ||
    !isRecord(value.spec) ||
    Object.keys(value.spec).length === 0
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
    definition_schema_version: value.definition_schema_version,
    type: value.type,
    type_version: value.type_version,
    spec: structuredClone(value.spec),
  }
}

function parseCaseTypeDescriptor(value: unknown): CatalogCaseTypeDescriptor {
  if (
    !isRecord(value) || !isSafeCaseType(value.type) || !isPositiveInteger(value.type_version) ||
    !isNonBlank(value.label) || !isNonBlank(value.category) ||
    (value.scheduling_owner !== "case" && value.scheduling_owner !== "plan") ||
    !Array.isArray(value.supported_protocols) || value.supported_protocols.length === 0 ||
    !value.supported_protocols.every(isProtocol) || new Set(value.supported_protocols).size !== value.supported_protocols.length ||
    typeof value.creatable !== "boolean" || !isRecord(value.default_spec) || Object.keys(value.default_spec).length === 0
  ) {
    throw new Error("桌面目录用例类型数据无效")
  }
  return {
    type: value.type, type_version: value.type_version, label: value.label, category: value.category,
    scheduling_owner: value.scheduling_owner, supported_protocols: [...value.supported_protocols],
    creatable: value.creatable, default_spec: structuredClone(value.default_spec),
  }
}

function parseSuite(value: unknown): CatalogSuite {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isPositiveInteger(value.case_count) ||
    !Array.isArray(value.cases)
  ) {
    throw new Error("桌面目录测试套件数据无效")
  }
  const cases = value.cases.map(parseCaseRevision)
  if (cases.length !== value.case_count || !hasUniqueCaseIDs(cases)) {
    throw new Error("桌面目录测试套件成员无效")
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    case_count: value.case_count,
    cases,
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
    || !isUUIDList(value.model_ids)
    || !isUUIDList(value.channel_ids)
    || !Array.isArray(value.cases)
    || !isFiniteNumberRecord(value.sla_thresholds)
    || !isOptionalSuiteRef(value.suite_id, value.suite_revision)
  ) {
    throw new Error("桌面目录测试计划数据无效")
  }
  const cases = value.cases.map(parseCaseRevision)
  if (
    value.model_ids.length !== value.model_count ||
    value.channel_ids.length !== value.channel_count ||
    cases.length !== value.case_count ||
    !hasUniqueCaseIDs(cases)
  ) {
    throw new Error("桌面目录测试计划成员无效")
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
    model_ids: [...value.model_ids],
    channel_ids: [...value.channel_ids],
    ...(typeof value.suite_id === "string" ? { suite_id: value.suite_id, suite_revision: value.suite_revision as number } : {}),
    cases,
    sla_thresholds: { ...value.sla_thresholds },
  }
}

function parseCaseRevision(value: unknown): CatalogCaseRevision {
  if (!isRecord(value) || !isUUID(value.case_id) || !isPositiveInteger(value.revision)) {
    throw new Error("桌面目录用例版本引用无效")
  }
  return { case_id: value.case_id, revision: value.revision }
}

function hasUniqueCaseIDs(cases: CatalogCaseRevision[]): boolean {
  return new Set(cases.map((ref) => ref.case_id)).size === cases.length
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

function isUUIDList(value: unknown): value is string[] {
  return Array.isArray(value) && value.length > 0 && value.every(isUUID) && new Set(value).size === value.length
}

function isFiniteNumberRecord(value: unknown): value is Record<string, number> {
  return isRecord(value) && Object.keys(value).length > 0 && Object.entries(value).every(([name, entry]) => isNonBlank(name) && isNonNegativeFinite(entry))
}

function isOptionalSuiteRef(id: unknown, revision: unknown): boolean {
  return (id === undefined && revision === undefined) || (isUUID(id) && isPositiveInteger(revision))
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

function isSafeCaseType(value: unknown): value is string {
  return isNonBlank(value) && /^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$/.test(value)
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
