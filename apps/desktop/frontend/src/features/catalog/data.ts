import { isProtocolRunSettings, type ProtocolRunSettings } from "@/features/protocols/types"
import { DesktopDataError } from "@/app/data-error"
import { translateDesktop as tx } from "@/i18n/runtime"
import { isProtocol, type ProtocolID } from "./protocols"
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

export interface CatalogCaseReference {
  case_id: string
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

export type CatalogPlanParameterValue = string | number | boolean | null | CatalogPlanParameterValue[] | { [key: string]: CatalogPlanParameterValue }
export interface SuiteInput {
  key: string
  label: string
  type: "string" | "integer" | "number" | "boolean" | "object" | "array"
  description?: string
  unit?: string
  default?: CatalogPlanParameterValue
  required?: boolean
  minimum?: number
  maximum?: number
  enum?: CatalogPlanParameterValue[]
  bindings: { case_id: string; input: string }[]
}
export interface CatalogSuite {
  id: string; revision: number; key: string; name: string; protocol: CatalogProtocol
  case_count: number; cases: CatalogCaseReference[]; description: string; inputs: SuiteInput[]
}
export interface CatalogPlanEntry {
  entry_id: string; target_kind: "case" | "suite"; target_id: string
  target_name: string; target_key: string; case_count: number
  parameters: Record<string, CatalogPlanParameterValue>
  load_mode: CatalogLoadMode; concurrency: number; request_count: number
  rate_per_second: number; duration_ms: number; request_timeout_ms: number
  warmup_count: number; settings: ProtocolRunSettings
  sla_thresholds: Record<string, number>
}
export interface CatalogPlan {
  id: string; revision: number; name: string; protocol: CatalogProtocol; seed: number
  entry_count: number; case_count: number; entries: CatalogPlanEntry[]
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
export type CreateSuiteCommand = Pick<CatalogSuite, "key" | "name" | "protocol" | "cases" | "description" | "inputs">
export type UpdateSuiteCommand = CreateSuiteCommand & { id: string; expected_revision: number }
export type PlanEntryCommand = Pick<CatalogPlanEntry,
  "target_kind" | "target_id" | "parameters" | "load_mode" | "concurrency" | "request_count" |
  "rate_per_second" | "duration_ms" | "request_timeout_ms" | "sla_thresholds" | "warmup_count" | "settings"
> & { entry_id?: string }
export type CreatePlanCommand = Pick<CatalogPlan, "name" | "protocol" | "seed"> & { entries: PlanEntryCommand[] }
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
  schema_version: 4
  case_types: CatalogCaseTypeDescriptor[]
  models: CatalogModel[]
  channels: CatalogChannel[]
  channel_models: CatalogChannelModel[]
  test_cases: CatalogTestCase[]
  suites: CatalogSuite[]
  plans: CatalogPlan[]
}

export const EMPTY_CATALOG: CatalogSnapshot = {
  schema_version: 4,
  case_types: [],
  models: [],
  channels: [],
  channel_models: [],
  test_cases: [],
  suites: [],
  plans: [],
}

export function parseCatalogSnapshot(value: unknown): CatalogSnapshot {
  if (!isRecord(value) || value.schema_version !== 4) {
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

  return {
    schema_version: 4,
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
  if (!isRecord(value) || !isUUID(value.id) || !isPositiveInteger(value.revision) ||
    !isSafeCaseKey(value.key) || !isNonBlank(value.name) || !isProtocol(value.protocol) ||
    !isNonNegativeInteger(value.case_count) || !Array.isArray(value.cases) ||
    typeof value.description !== "string" || !Array.isArray(value.inputs) ||
    "quick_test" in value || "model_target" in value) throw invalidCatalog()
  const cases = value.cases.map((ref): CatalogCaseReference => {
    if (!isRecord(ref) || !isUUID(ref.case_id) || "revision" in ref) throw invalidCatalog()
    return { case_id: ref.case_id }
  })
  if (cases.length !== value.case_count || !hasUniqueCaseIDs(cases)) throw invalidCatalog()
  const inputs = value.inputs.map(parseSuiteInput)
  if (new Set(inputs.map(input => input.key)).size !== inputs.length) throw invalidCatalog()
  return { id: value.id, revision: value.revision, key: value.key, name: value.name,
    protocol: value.protocol, case_count: value.case_count, cases, description: value.description, inputs }
}
export function parseSuiteInput(value: unknown): SuiteInput {
  if (!isRecord(value) || !isSafeCaseKey(value.key) || !isNonBlank(value.label) ||
    !["string", "integer", "number", "boolean", "object", "array"].includes(value.type as string) ||
    !Array.isArray(value.bindings) ||
    (value.default !== undefined && !isJSONValue(value.default)) ||
    (value.required !== undefined && typeof value.required !== "boolean") ||
    (value.minimum !== undefined && (typeof value.minimum !== "number" || !Number.isFinite(value.minimum))) ||
    (value.maximum !== undefined && (typeof value.maximum !== "number" || !Number.isFinite(value.maximum))) ||
    (value.description !== undefined && typeof value.description !== "string") ||
    (value.unit !== undefined && typeof value.unit !== "string") ||
    (value.enum !== undefined && (!Array.isArray(value.enum) || !value.enum.every(isJSONValue)))) throw invalidCatalog()
  return { key: value.key, label: value.label, type: value.type as SuiteInput["type"],
    ...(value.default !== undefined ? { default: structuredClone(value.default) as CatalogPlanParameterValue } : {}),
    ...(value.required !== undefined ? { required: value.required as boolean } : {}),
    ...(value.minimum !== undefined ? { minimum: value.minimum as number } : {}),
    ...(value.maximum !== undefined ? { maximum: value.maximum as number } : {}),
    ...(value.description !== undefined ? { description: value.description as string } : {}),
    ...(value.unit !== undefined ? { unit: value.unit as string } : {}),
    ...(value.enum !== undefined ? { enum: structuredClone(value.enum) as CatalogPlanParameterValue[] } : {}),
    bindings: value.bindings.map(binding => {
      if (!isRecord(binding) || !isUUID(binding.case_id) || !isNonBlank(binding.input) || "pointer" in binding || "case_key" in binding) throw invalidCatalog()
      return { case_id: binding.case_id, input: binding.input }
    }) }
}
function parsePlan(value: unknown): CatalogPlan {
  if (!isRecord(value) || !isUUID(value.id) || !isPositiveInteger(value.revision) ||
    !isNonBlank(value.name) || !isProtocol(value.protocol) || !isNonNegativeInteger(value.seed) ||
    !isNonNegativeInteger(value.entry_count) || !isNonNegativeInteger(value.case_count) ||
    !Array.isArray(value.entries) || ["suites", "model_ids", "channel_ids"].some(key => key in value)) throw invalidCatalog()
  const entries = value.entries.map(parsePlanEntry)
  if (entries.length !== value.entry_count || new Set(entries.map(entry => entry.entry_id)).size !== entries.length) throw invalidCatalog()
  return { id: value.id, revision: value.revision, name: value.name, protocol: value.protocol,
    seed: value.seed, entry_count: value.entry_count, case_count: value.case_count, entries }
}
function parsePlanEntry(value: unknown): CatalogPlanEntry {
  if (!isRecord(value) || !isUUID(value.entry_id) || !isUUID(value.target_id) ||
    (value.target_kind !== "case" && value.target_kind !== "suite") ||
    typeof value.target_name !== "string" || typeof value.target_key !== "string" || !isNonNegativeInteger(value.case_count) ||
    !isNonNegativeInteger(value.warmup_count) || !isProtocolRunSettings(value.settings) ||
    !isRecord(value.parameters) || !Object.values(value.parameters).every(isJSONValue) ||
    !isLoadMode(value.load_mode) || !isPositiveInteger(value.concurrency) ||
    !isNonNegativeInteger(value.request_count) || !isNonNegativeFinite(value.rate_per_second) ||
    !isNonNegativeInteger(value.duration_ms) || !isPositiveInteger(value.request_timeout_ms) ||
    !isRecord(value.sla_thresholds) || !Object.values(value.sla_thresholds).every(isNonNegativeFinite) ||
    ["suite_revision", "suite_id", "cases"].some(key => key in value)) throw invalidCatalog()
  return { entry_id: value.entry_id, target_kind: value.target_kind, target_id: value.target_id,
    target_name: value.target_name, target_key: value.target_key, case_count: value.case_count,
    warmup_count: value.warmup_count, settings: { ...value.settings },
    parameters: structuredClone(value.parameters) as CatalogPlanEntry["parameters"], load_mode: value.load_mode,
    concurrency: value.concurrency, request_count: value.request_count, rate_per_second: value.rate_per_second,
    duration_ms: value.duration_ms, request_timeout_ms: value.request_timeout_ms,
    sla_thresholds: { ...value.sla_thresholds } as Record<string, number> }
}
function hasUniqueCaseIDs(cases: CatalogCaseReference[]): boolean { return new Set(cases.map(ref => ref.case_id)).size === cases.length }
function invalidCatalog() { return new DesktopDataError(tx("desktop:catalog_invalid_desktop_catalog_structure")) }
export function isJSONValue(value: unknown): value is CatalogPlanParameterValue {
  if (value === null || typeof value === "string" || typeof value === "boolean") return true
  if (typeof value === "number") return Number.isFinite(value)
  if (Array.isArray(value)) return value.every(isJSONValue)
  return isRecord(value) && Object.values(value).every(isJSONValue)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
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
  return isProtocol(value)
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
