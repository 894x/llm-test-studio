import { DesktopDataError } from "@/app/data-error"
import { translateDesktop as tx } from "@/i18n/runtime"
import { isProtocol, PROTOCOLS, type ProtocolID } from "./protocols"
export type CatalogProtocol = ProtocolID
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
  model_targets: string[]
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
  key: string
  name: string
  protocol: CatalogProtocol
  model_target: string
  case_count: number
  cases: CatalogCaseRevision[]
  quick_test?: SuiteQuickTest
}

export interface SuiteQuickTest {
  description: string
  timeout_ms: number
  inputs: SuiteInput[]
}

export interface SuiteInput {
  key: string
  label: string
  type: "text" | "number" | "boolean"
  default: string | number | boolean
  bindings: { case_key: string; pointer: string }[]
}

export type CatalogPlanParameterValue = string | number | boolean

export interface CatalogPlanSuite {
  entry_id: string
  suite_id: string
  suite_revision: number
  suite_key: string
  suite_name: string
  protocol: CatalogProtocol
  model_target: string
  case_count: number
  cases: CatalogCaseRevision[]
  quick_test?: SuiteQuickTest
  parameters: Record<string, CatalogPlanParameterValue>
  load_mode: CatalogLoadMode
  concurrency: number
  request_count: number
  rate_per_second: number
  duration_ms: number
  request_timeout_ms: number
  sla_thresholds: Record<string, number>
}

export interface CatalogPlan {
  id: string
  revision: number
  name: string
  model_count: number
  channel_count: number
  suite_count: number
  case_count: number
  model_ids: string[]
  channel_ids: string[]
  suites: CatalogPlanSuite[]
}

export type CreateModelCommand = Pick<CatalogModel, "name" | "protocol" | "capabilities">
export type UpdateModelCommand = CreateModelCommand & { id: string; expected_revision: number }
export type CreateChannelCommand = Pick<CatalogChannel, "name" | "base_url" | "protocol" | "enabled"> & { api_key: string }
export type UpdateChannelCommand = CreateChannelCommand & { id: string; expected_revision: number }
export type CreateChannelModelCommand = Pick<CatalogChannelModel, "channel_id" | "model_id" | "upstream_model_name">
export type UpdateChannelModelCommand = Pick<CatalogChannelModel, "upstream_model_name"> & { id: string; expected_revision: number }
export type CreateTestCaseCommand = Pick<CatalogTestCase,
  "key" | "name" | "dimension" | "protocol" | "enabled" | "default" | "severity" |
  "model_targets" | "execution_mode" | "definition_schema_version" | "type" | "type_version" | "spec"
>
export type UpdateTestCaseCommand = CreateTestCaseCommand & { id: string; expected_revision: number }
export type CreateSuiteCommand = Pick<CatalogSuite, "key" | "name" | "protocol" | "model_target" | "cases" | "quick_test">
export type UpdateSuiteCommand = CreateSuiteCommand & { id: string; expected_revision: number }
export type PlanSuiteCommand = Pick<CatalogPlanSuite,
  "suite_id" | "suite_revision" | "parameters" | "load_mode" | "concurrency" | "request_count" |
  "rate_per_second" | "duration_ms" | "request_timeout_ms" | "sla_thresholds"
> & { entry_id?: string }
export type CreatePlanCommand = Pick<CatalogPlan, "name" | "model_ids" | "channel_ids"> & { suites: PlanSuiteCommand[] }
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
  schema_version: 3
  case_types: CatalogCaseTypeDescriptor[]
  models: CatalogModel[]
  channels: CatalogChannel[]
  channel_models: CatalogChannelModel[]
  test_cases: CatalogTestCase[]
  suites: CatalogSuite[]
  plans: CatalogPlan[]
}

export const EMPTY_CATALOG: CatalogSnapshot = {
  schema_version: 3,
  case_types: [],
  models: [],
  channels: [],
  channel_models: [],
  test_cases: [],
  suites: [],
  plans: [],
}

export function parseCatalogSnapshot(value: unknown): CatalogSnapshot {
  if (!isRecord(value) || value.schema_version !== 3) {
    throw new DesktopDataError(tx("desktop:catalog_unsupported_desktop_catalog_protocol_version"))
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_structure"))
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
    throw new DesktopDataError(tx("desktop:catalog_duplicate_desktop_catalog_identifiers"))
  }
  if (new Set(caseTypes.map((descriptor) => `${descriptor.type}@${descriptor.type_version}`)).size !== caseTypes.length) {
    throw new DesktopDataError(tx("desktop:catalog_duplicate_desktop_catalog_case_types"))
  }
  const caseTypeByKey = new Map(caseTypes.map((descriptor) => [`${descriptor.type}@${descriptor.type_version}`, descriptor]))
  for (const testCase of testCases) {
    const descriptor = caseTypeByKey.get(`${testCase.type}@${testCase.type_version}`)
    if (!descriptor || !descriptor.supported_protocols.includes(testCase.protocol)) {
      throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_test_case_type"))
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
      throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_model_mapping_reference"))
    }
    const binding = `${mapping.channel_id}\u0000${mapping.model_id}`
    if (mappedBindings.has(binding)) throw new DesktopDataError(tx("desktop:catalog_duplicate_desktop_catalog_model_mapping"))
    mappedBindings.add(binding)
    mappedCounts.set(mapping.channel_id, (mappedCounts.get(mapping.channel_id) ?? 0) + 1)
  }
  if (
    channels.some(
      (channel) => channel.model_count !== (mappedCounts.get(channel.id) ?? 0),
    )
  ) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_channel_model_count"))
  }
  for (const suite of suites) {
		if (!hasUniqueCaseIDs(suite.cases)) throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_suite_reference"))
  }
  for (const plan of plans) {
    if (
      plan.model_ids.some((id) => !modelByID.has(id)) ||
      plan.channel_ids.some((id) => !channelByID.has(id))
    ) {
      throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_reference"))
    }
    for (const entry of plan.suites) {
      const suite = suiteByID.get(entry.suite_id)
      if (!suite) {
        throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_suite_reference"))
      }
    }
  }

  return {
    schema_version: 3,
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_model_data"))
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_channel_data"))
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_model_mapping_data"))
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
    !Array.isArray(value.model_targets) ||
    value.model_targets.length > 32 ||
    !value.model_targets.every((target) => isNonBlank(target) && target.trim() === target && target.length <= 256) ||
    new Set(value.model_targets).size !== value.model_targets.length ||
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_test_case_data"))
  }
  return {
    id: value.id,
    revision: value.revision,
    key: value.key,
    name: value.name,
    dimension: value.dimension,
    protocol: value.protocol,
    model_targets: [...value.model_targets],
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
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_case_type_data"))
  }
  return {
    type: value.type, type_version: value.type_version, label: value.label, category: value.category,
    scheduling_owner: value.scheduling_owner, supported_protocols: [...value.supported_protocols],
    creatable: value.creatable, default_spec: structuredClone(value.default_spec),
  }
}

export function parseSuite(value: unknown): CatalogSuite {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isSafeCaseKey(value.key) ||
    !isNonBlank(value.name) ||
    !isProtocol(value.protocol) ||
    typeof value.model_target !== "string" || value.model_target.trim() !== value.model_target || value.model_target.length > 256 ||
    !isPositiveInteger(value.case_count) ||
    !Array.isArray(value.cases)
  ) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_suite_data"))
  }
  const cases = value.cases.map(parseCaseRevision)
  const quickTest = value.quick_test === undefined ? undefined : parseSuiteQuickTest(value.quick_test)
  if (!value.model_target && (!quickTest || PROTOCOLS.find(({ id }) => id === value.protocol)?.requiresModelTargets)) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_suite_data"))
  }
  if (cases.length !== value.case_count || !hasUniqueCaseIDs(cases)) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_suite_member"))
  }
  return {
    id: value.id,
    revision: value.revision,
    key: value.key,
    name: value.name,
    protocol: value.protocol,
    model_target: value.model_target,
    case_count: value.case_count,
    cases,
    ...(quickTest ? { quick_test: quickTest } : {}),
  }
}

function parseSuiteQuickTest(value: unknown): SuiteQuickTest {
  const invalid = () => new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_suite_data"))
  if (!isRecord(value) || !isNonBlank(value.description) || !isPositiveInteger(value.timeout_ms) || value.timeout_ms > 3_600_000 || !Array.isArray(value.inputs)) throw invalid()
  const keys = new Set<string>()
  const bindings = new Set<string>()
  const inputs = value.inputs.map((input): SuiteInput => {
    if (!isRecord(input) || !isSafeCaseKey(input.key) || keys.has(input.key) || !isNonBlank(input.label) || !Array.isArray(input.bindings) || !input.bindings.length) throw invalid()
    keys.add(input.key)
    const type = input.type
    const defaultValue = input.default
    if (!(type === "text" && typeof defaultValue === "string") && !(type === "number" && typeof defaultValue === "number" && Number.isFinite(defaultValue)) && !(type === "boolean" && typeof defaultValue === "boolean")) throw invalid()
    return {
      key: input.key, label: input.label, type: type as SuiteInput["type"], default: defaultValue as SuiteInput["default"],
      bindings: input.bindings.map((binding) => {
        if (!isRecord(binding) || !isSafeCaseKey(binding.case_key) || typeof binding.pointer !== "string" || !binding.pointer.startsWith("/request/body/") || /^\/request\/body\/model(?:\/|$)/.test(binding.pointer) || /~(?:[^01]|$)/.test(binding.pointer)) throw invalid()
        const identity = `${binding.case_key}\u0000${binding.pointer}`
        if (bindings.has(identity)) throw invalid()
        bindings.add(identity)
        return { case_key: binding.case_key, pointer: binding.pointer }
      }),
    }
  })
  return { description: value.description, timeout_ms: value.timeout_ms, inputs }
}

function parsePlan(value: unknown): CatalogPlan {
  if (
    !isRecord(value) ||
    !isUUID(value.id) ||
    !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) ||
    !isNonNegativeInteger(value.model_count) ||
    !isNonNegativeInteger(value.channel_count) ||
    !isPositiveInteger(value.suite_count) ||
    !isPositiveInteger(value.case_count) ||
    !isUUIDList(value.model_ids) ||
    !isUUIDList(value.channel_ids) ||
    (value.model_ids.length === 0) !== (value.channel_ids.length === 0) ||
    !Array.isArray(value.suites) ||
    hasLegacyPlanFields(value)
  ) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_data"))
  }
  const suites = value.suites.map(parsePlanSuite)
  if (
    value.model_ids.length !== value.model_count ||
    value.channel_ids.length !== value.channel_count ||
    suites.length !== value.suite_count ||
    new Set(suites.map((entry) => entry.entry_id)).size !== suites.length ||
    suites.reduce((total, entry) => total + entry.case_count, 0) !== value.case_count
  ) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_member"))
  }
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    model_count: value.model_count,
    channel_count: value.channel_count,
    suite_count: value.suite_count,
    case_count: value.case_count,
    model_ids: [...value.model_ids],
    channel_ids: [...value.channel_ids],
    suites,
  }
}

function hasLegacyPlanFields(value: Record<string, unknown>): boolean {
  return [
    "suite_id", "suite_revision", "cases", "load_mode", "concurrency", "request_count",
    "rate_per_second", "duration_ms", "request_timeout_ms", "sla_thresholds",
  ].some((key) => Object.hasOwn(value, key))
}

function parsePlanSuite(value: unknown): CatalogPlanSuite {
  if (
    !isRecord(value) ||
    !isUUID(value.entry_id) ||
    !isUUID(value.suite_id) ||
    !isPositiveInteger(value.suite_revision) ||
    !isNonBlank(value.suite_key) ||
    !isNonBlank(value.suite_name) ||
    !isProtocol(value.protocol) ||
    typeof value.model_target !== "string" ||
    !isPositiveInteger(value.case_count) ||
    !Array.isArray(value.cases) ||
    !isPlanParameters(value.parameters) ||
    !isLoadMode(value.load_mode) ||
    !isPositiveInteger(value.concurrency) ||
    !isNonNegativeInteger(value.request_count) ||
    !isNonNegativeFinite(value.rate_per_second) ||
    !isNonNegativeInteger(value.duration_ms) ||
    (value.request_count === 0 && value.duration_ms === 0) ||
    !isPositiveInteger(value.request_timeout_ms) ||
    !isFiniteNumberRecord(value.sla_thresholds)
  ) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_data"))
  }
  const cases = value.cases.map(parseCaseRevision)
  if (cases.length !== value.case_count || !hasUniqueCaseIDs(cases)) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_plan_member"))
  }
  const quickTest = value.quick_test === undefined ? undefined : parseSuiteQuickTest(value.quick_test)
  return {
    entry_id: value.entry_id,
    suite_id: value.suite_id,
    suite_revision: value.suite_revision,
    suite_key: value.suite_key,
    suite_name: value.suite_name,
    protocol: value.protocol,
    model_target: value.model_target,
    case_count: value.case_count,
    cases,
    ...(quickTest ? { quick_test: quickTest } : {}),
    parameters: { ...value.parameters },
    load_mode: value.load_mode,
    concurrency: value.concurrency,
    request_count: value.request_count,
    rate_per_second: value.rate_per_second,
    duration_ms: value.duration_ms,
    request_timeout_ms: value.request_timeout_ms,
    sla_thresholds: { ...value.sla_thresholds },
  }
}

function parseCaseRevision(value: unknown): CatalogCaseRevision {
  if (!isRecord(value) || !isUUID(value.case_id) || !isPositiveInteger(value.revision)) {
    throw new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_case_version_reference"))
  }
  return { case_id: value.case_id, revision: value.revision }
}

function hasUniqueCaseIDs(cases: CatalogCaseRevision[]): boolean {
  return new Set(cases.map((ref) => ref.case_id)).size === cases.length
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

export function isUUID(value: unknown): value is string {
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
  return Array.isArray(value) && value.every(isUUID) && new Set(value).size === value.length
}

function isFiniteNumberRecord(value: unknown): value is Record<string, number> {
  return isRecord(value) && Object.keys(value).length > 0 && Object.entries(value).every(([name, entry]) => isNonBlank(name) && isNonNegativeFinite(entry))
}

function isPlanParameters(value: unknown): value is Record<string, CatalogPlanParameterValue> {
  return !Array.isArray(value) && isRecord(value) && Object.values(value).every((item) =>
    typeof item === "string" || typeof item === "boolean" || (typeof item === "number" && Number.isFinite(item)),
  )
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
