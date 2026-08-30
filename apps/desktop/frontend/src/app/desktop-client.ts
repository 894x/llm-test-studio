import type { WorkspaceSnapshot } from "@/features/runs/data"
import {
  EMPTY_CATALOG,
  parseCatalogSnapshot,
  type CatalogActions,
  type CatalogSnapshot,
  type CreateChannelCommand,
  type CreateChannelModelCommand,
  type CreateModelCommand,
  type CreatePlanCommand,
  type CreateSuiteCommand,
  type CreateTestCaseCommand,
  type DeleteCommand,
  type UpdateChannelCommand,
  type UpdateChannelModelCommand,
  type UpdateModelCommand,
  type UpdatePlanCommand,
  type UpdateSuiteCommand,
  type UpdateTestCaseCommand,
} from "@/features/catalog/data"
import {
  EMPTY_REPORTS,
  parseReportSnapshot,
  type ReportSnapshot,
} from "@/features/reports/data"

export type DesktopErrorCode =
  | "desktop_not_started"
  | "desktop_startup_failed"
  | "desktop_stopped"
  | "workspace_unavailable"
  | "catalog_unavailable"
  | "reports_unavailable"
  | "run_commands_unavailable"
  | "invalid_identifier"
  | "operation_cancelled"
  | "operation_failed"
  | "catalog_invalid"
  | "catalog_revision_conflict"
  | "catalog_not_found"

const PUBLIC_ERROR_MESSAGES: Record<DesktopErrorCode, string> = {
  desktop_not_started: "桌面应用尚未启动",
  desktop_startup_failed: "桌面应用初始化失败",
  desktop_stopped: "桌面应用已停止",
  workspace_unavailable: "本地工作区暂不可用",
  catalog_unavailable: "测试目录暂不可用",
  reports_unavailable: "测试报告暂不可用",
  run_commands_unavailable: "运行命令暂不可用",
  invalid_identifier: "操作对象无效",
  operation_cancelled: "操作已取消",
  operation_failed: "桌面操作失败，请检查本地日志",
  catalog_invalid: "目录内容无效，请检查表单字段",
  catalog_revision_conflict: "对象版本已变化或仍被引用，请刷新并解除引用后重试",
  catalog_not_found: "对象已删除或不存在，请刷新目录",
}

export class DesktopClientError extends Error {
  readonly code: DesktopErrorCode

  constructor(code: DesktopErrorCode) {
    super(PUBLIC_ERROR_MESSAGES[code])
    this.name = "DesktopClientError"
    this.code = code
  }
}

export function publicDesktopErrorMessage(
  error: unknown,
  fallback: string,
): string {
  return error instanceof DesktopClientError ? error.message : fallback
}

export interface DesktopClient extends CatalogActions {
  getWorkspace(): Promise<WorkspaceSnapshot>
  getCatalog(): Promise<CatalogSnapshot>
  getReports(): Promise<ReportSnapshot>
  startRun(planId: string): Promise<WorkspaceSnapshot>
  stopSending(runId: string): Promise<WorkspaceSnapshot>
  cancelRun(runId: string): Promise<WorkspaceSnapshot>
}

type WailsDesktopBinding = {
  GetWorkspace(): Promise<unknown>
  GetCatalog(): Promise<unknown>
  GetReports(): Promise<unknown>
  StartRun(planId: string): Promise<unknown>
  StopSending(runId: string): Promise<unknown>
  CancelRun(runId: string): Promise<unknown>
  CreateModel(command: CreateModelCommand): Promise<unknown>
  UpdateModel(command: UpdateModelCommand): Promise<unknown>
  DeleteModel(command: DeleteCommand): Promise<unknown>
  CreateChannel(command: CreateChannelCommand): Promise<unknown>
  UpdateChannel(command: UpdateChannelCommand): Promise<unknown>
  DeleteChannel(command: DeleteCommand): Promise<unknown>
  CreateChannelModel(command: CreateChannelModelCommand): Promise<unknown>
  UpdateChannelModel(command: UpdateChannelModelCommand): Promise<unknown>
  DeleteChannelModel(command: DeleteCommand): Promise<unknown>
  CreateTestCase(command: CreateTestCaseCommand): Promise<unknown>
  UpdateTestCase(command: UpdateTestCaseCommand): Promise<unknown>
  DeleteTestCase(command: DeleteCommand): Promise<unknown>
  CreateSuite(command: CreateSuiteCommand): Promise<unknown>
  UpdateSuite(command: UpdateSuiteCommand): Promise<unknown>
  DeleteSuite(command: DeleteCommand): Promise<unknown>
  CreatePlan(command: CreatePlanCommand): Promise<unknown>
  UpdatePlan(command: UpdatePlanCommand): Promise<unknown>
  DeletePlan(command: DeleteCommand): Promise<unknown>
}

export function createDesktopClient(): DesktopClient {
  const binding = readWailsBinding()
  if (binding) return wailsClient(binding)
  if (import.meta.env.DEV) return createLazyFixtureClient()
  return unavailableClient()
}

export function createFixtureClient(
  initial: WorkspaceSnapshot,
  catalog: CatalogSnapshot = EMPTY_CATALOG,
  reports: ReportSnapshot = EMPTY_REPORTS,
): DesktopClient {
  let workspace = cloneSnapshot(initial)
  let catalogState = structuredClone(catalog)
  const nextID = () => crypto.randomUUID()
  const refreshChannelCounts = () => {
    catalogState.channels = catalogState.channels.map((channel) => ({
      ...channel,
      model_count: catalogState.channel_models.filter((mapping) => mapping.channel_id === channel.id).length,
    }))
  }
  return {
    async getWorkspace() {
      return cloneSnapshot(workspace)
    },
    async getCatalog() {
      return structuredClone(catalogState)
    },
    async getReports() {
      return structuredClone(reports)
    },
    async startRun(planId) {
      if (!workspace.plans.some((plan) => plan.id === planId)) {
        throw new DesktopClientError("invalid_identifier")
      }
      return cloneSnapshot(workspace)
    },
    async stopSending(runId) {
      workspace = updateRun(workspace, runId, "draining")
      return cloneSnapshot(workspace)
    },
    async cancelRun(runId) {
      workspace = {
        ...updateRun(workspace, runId, "cancelled"),
        active_run_id:
          workspace.active_run_id === runId ? undefined : workspace.active_run_id,
      }
      return cloneSnapshot(workspace)
    },
    async createModel(command) {
      catalogState.models.push({ id: nextID(), revision: 1, ...structuredClone(command) })
      return structuredClone(catalogState)
    },
    async updateModel(command) {
      catalogState.models = replaceByID(catalogState.models, command.id, {
        id: command.id,
        revision: command.expected_revision + 1,
        name: command.name,
        protocol: command.protocol,
        capabilities: [...command.capabilities],
      })
      return structuredClone(catalogState)
    },
    async deleteModel(command) {
      catalogState.models = removeByID(catalogState.models, command.id)
      return structuredClone(catalogState)
    },
    async createChannel(command) {
      catalogState.channels.push({ id: nextID(), revision: 1, ...structuredClone(command), credential_configured: false, model_count: 0 })
      return structuredClone(catalogState)
    },
    async updateChannel(command) {
      const current = catalogState.channels.find((channel) => channel.id === command.id)
      if (!current) throw new DesktopClientError("invalid_identifier")
      catalogState.channels = replaceByID(catalogState.channels, command.id, {
        id: command.id,
        revision: command.expected_revision + 1,
        name: command.name,
        base_url: command.base_url,
        protocol: command.protocol,
        enabled: command.enabled,
        credential_configured: current.credential_configured,
        model_count: current.model_count,
      })
      return structuredClone(catalogState)
    },
    async deleteChannel(command) {
      catalogState.channels = removeByID(catalogState.channels, command.id)
      return structuredClone(catalogState)
    },
    async createChannelModel(command) {
      catalogState.channel_models.push({ id: nextID(), revision: 1, ...structuredClone(command) })
      refreshChannelCounts()
      return structuredClone(catalogState)
    },
    async updateChannelModel(command) {
      const current = catalogState.channel_models.find((mapping) => mapping.id === command.id)
      if (!current) throw new DesktopClientError("invalid_identifier")
      catalogState.channel_models = replaceByID(catalogState.channel_models, command.id, {
        ...current,
        revision: command.expected_revision + 1,
        upstream_model_name: command.upstream_model_name,
      })
      return structuredClone(catalogState)
    },
    async deleteChannelModel(command) {
      catalogState.channel_models = removeByID(catalogState.channel_models, command.id)
      refreshChannelCounts()
      return structuredClone(catalogState)
    },
    async createTestCase(command) {
      catalogState.test_cases.push({ id: nextID(), revision: 1, ...structuredClone(command), assertion_kinds: command.assertions.map((assertion) => assertion.kind) })
      return structuredClone(catalogState)
    },
    async updateTestCase(command) {
      const { id, expected_revision, ...editable } = command
      catalogState.test_cases = replaceByID(catalogState.test_cases, command.id, {
        id,
        revision: expected_revision + 1,
        ...structuredClone(editable),
        assertion_kinds: command.assertions.map((assertion) => assertion.kind),
      })
      return structuredClone(catalogState)
    },
    async deleteTestCase(command) {
      catalogState.test_cases = removeByID(catalogState.test_cases, command.id)
      return structuredClone(catalogState)
    },
    async createSuite(command) {
      catalogState.suites.push({ id: nextID(), revision: 1, ...structuredClone(command), case_count: command.cases.length })
      return structuredClone(catalogState)
    },
    async updateSuite(command) {
      catalogState.suites = replaceByID(catalogState.suites, command.id, {
        id: command.id,
        revision: command.expected_revision + 1,
        name: command.name,
        cases: structuredClone(command.cases),
        case_count: command.cases.length,
      })
      return structuredClone(catalogState)
    },
    async deleteSuite(command) {
      catalogState.suites = removeByID(catalogState.suites, command.id)
      return structuredClone(catalogState)
    },
    async createPlan(command) {
      catalogState.plans.push(planFromCommand(nextID(), 1, command))
      return structuredClone(catalogState)
    },
    async updatePlan(command) {
      catalogState.plans = replaceByID(catalogState.plans, command.id, planFromCommand(command.id, command.expected_revision + 1, command))
      return structuredClone(catalogState)
    },
    async deletePlan(command) {
      catalogState.plans = removeByID(catalogState.plans, command.id)
      return structuredClone(catalogState)
    },
  }
}

function createLazyFixtureClient(): DesktopClient {
  const client = import("@/features/runs/fixtures").then(
    ({ FIXTURE_CATALOG, FIXTURE_REPORTS, FIXTURE_WORKSPACE }) =>
      createFixtureClient(FIXTURE_WORKSPACE, FIXTURE_CATALOG, FIXTURE_REPORTS),
  )
  return {
    getWorkspace: async () => (await client).getWorkspace(),
    getCatalog: async () => (await client).getCatalog(),
    getReports: async () => (await client).getReports(),
    startRun: async (planId) => (await client).startRun(planId),
    stopSending: async (runId) => (await client).stopSending(runId),
    cancelRun: async (runId) => (await client).cancelRun(runId),
    createModel: async (command) => (await client).createModel(command),
    updateModel: async (command) => (await client).updateModel(command),
    deleteModel: async (command) => (await client).deleteModel(command),
    createChannel: async (command) => (await client).createChannel(command),
    updateChannel: async (command) => (await client).updateChannel(command),
    deleteChannel: async (command) => (await client).deleteChannel(command),
    createChannelModel: async (command) => (await client).createChannelModel(command),
    updateChannelModel: async (command) => (await client).updateChannelModel(command),
    deleteChannelModel: async (command) => (await client).deleteChannelModel(command),
    createTestCase: async (command) => (await client).createTestCase(command),
    updateTestCase: async (command) => (await client).updateTestCase(command),
    deleteTestCase: async (command) => (await client).deleteTestCase(command),
    createSuite: async (command) => (await client).createSuite(command),
    updateSuite: async (command) => (await client).updateSuite(command),
    deleteSuite: async (command) => (await client).deleteSuite(command),
    createPlan: async (command) => (await client).createPlan(command),
    updatePlan: async (command) => (await client).updatePlan(command),
    deletePlan: async (command) => (await client).deletePlan(command),
  }
}

function wailsClient(binding: WailsDesktopBinding): DesktopClient {
  return {
    getWorkspace: async () =>
      callBinding(() => binding.GetWorkspace(), parseSnapshot),
    getCatalog: async () =>
      callBinding(() => binding.GetCatalog(), parseCatalogSnapshot),
    getReports: async () =>
      callBinding(() => binding.GetReports(), parseReportSnapshot),
    startRun: async (planId) =>
      callBinding(() => binding.StartRun(planId), parseSnapshot),
    stopSending: async (runId) =>
      callBinding(() => binding.StopSending(runId), parseSnapshot),
    cancelRun: async (runId) =>
      callBinding(() => binding.CancelRun(runId), parseSnapshot),
    createModel: async (command) => callBinding(() => binding.CreateModel(command), parseCatalogSnapshot),
    updateModel: async (command) => callBinding(() => binding.UpdateModel(command), parseCatalogSnapshot),
    deleteModel: async (command) => callBinding(() => binding.DeleteModel(command), parseCatalogSnapshot),
    createChannel: async (command) => callBinding(() => binding.CreateChannel(command), parseCatalogSnapshot),
    updateChannel: async (command) => callBinding(() => binding.UpdateChannel(command), parseCatalogSnapshot),
    deleteChannel: async (command) => callBinding(() => binding.DeleteChannel(command), parseCatalogSnapshot),
    createChannelModel: async (command) => callBinding(() => binding.CreateChannelModel(command), parseCatalogSnapshot),
    updateChannelModel: async (command) => callBinding(() => binding.UpdateChannelModel(command), parseCatalogSnapshot),
    deleteChannelModel: async (command) => callBinding(() => binding.DeleteChannelModel(command), parseCatalogSnapshot),
    createTestCase: async (command) => callBinding(() => binding.CreateTestCase(command), parseCatalogSnapshot),
    updateTestCase: async (command) => callBinding(() => binding.UpdateTestCase(command), parseCatalogSnapshot),
    deleteTestCase: async (command) => callBinding(() => binding.DeleteTestCase(command), parseCatalogSnapshot),
    createSuite: async (command) => callBinding(() => binding.CreateSuite(command), parseCatalogSnapshot),
    updateSuite: async (command) => callBinding(() => binding.UpdateSuite(command), parseCatalogSnapshot),
    deleteSuite: async (command) => callBinding(() => binding.DeleteSuite(command), parseCatalogSnapshot),
    createPlan: async (command) => callBinding(() => binding.CreatePlan(command), parseCatalogSnapshot),
    updatePlan: async (command) => callBinding(() => binding.UpdatePlan(command), parseCatalogSnapshot),
    deletePlan: async (command) => callBinding(() => binding.DeletePlan(command), parseCatalogSnapshot),
  }
}

function unavailableClient(): DesktopClient {
  const reject = async <T>(): Promise<T> => {
    throw new DesktopClientError("workspace_unavailable")
  }
  return {
    getWorkspace: () => reject(),
    getCatalog: () => reject(),
    getReports: () => reject(),
    startRun: () => reject(),
    stopSending: () => reject(),
    cancelRun: () => reject(),
    createModel: () => reject(),
    updateModel: () => reject(),
    deleteModel: () => reject(),
    createChannel: () => reject(),
    updateChannel: () => reject(),
    deleteChannel: () => reject(),
    createChannelModel: () => reject(),
    updateChannelModel: () => reject(),
    deleteChannelModel: () => reject(),
    createTestCase: () => reject(),
    updateTestCase: () => reject(),
    deleteTestCase: () => reject(),
    createSuite: () => reject(),
    updateSuite: () => reject(),
    deleteSuite: () => reject(),
    createPlan: () => reject(),
    updatePlan: () => reject(),
    deletePlan: () => reject(),
  }
}

function readWailsBinding(): WailsDesktopBinding | undefined {
  const root = window as typeof window & {
    go?: { main?: { DesktopApp?: Partial<WailsDesktopBinding> } }
  }
  const candidate = root.go?.main?.DesktopApp
  const catalogMethods = [
    "CreateModel", "UpdateModel", "DeleteModel",
    "CreateChannel", "UpdateChannel", "DeleteChannel",
    "CreateChannelModel", "UpdateChannelModel", "DeleteChannelModel",
    "CreateTestCase", "UpdateTestCase", "DeleteTestCase",
    "CreateSuite", "UpdateSuite", "DeleteSuite",
    "CreatePlan", "UpdatePlan", "DeletePlan",
  ] as const satisfies ReadonlyArray<keyof WailsDesktopBinding>
  if (
    typeof candidate?.GetWorkspace !== "function" ||
    typeof candidate.GetCatalog !== "function" ||
    typeof candidate.GetReports !== "function" ||
    typeof candidate.StartRun !== "function" ||
    typeof candidate.StopSending !== "function" ||
    typeof candidate.CancelRun !== "function" ||
    catalogMethods.some((method) => typeof candidate[method] !== "function")
  ) {
    return undefined
  }
  return candidate as WailsDesktopBinding
}

function parseSnapshot(value: unknown): WorkspaceSnapshot {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new Error("桌面数据协议版本不受支持")
  }
  if (!Array.isArray(value.plans) || !Array.isArray(value.runs)) {
    throw new Error("桌面数据结构无效")
  }
  const plans = value.plans.map(parsePlan)
  const runs = value.runs.map(parseRun)
  const planIDs = new Set(plans.map((plan) => plan.id))
  const runIDs = new Set(runs.map((run) => run.id))
  if (planIDs.size !== plans.length) throw new Error("桌面测试计划数据无效")
  if (
    runIDs.size !== runs.length ||
    runs.some((run) => !planIDs.has(run.plan_id))
  ) {
    throw new Error("桌面运行记录数据无效")
  }
  if (
    value.active_run_id !== undefined &&
    (!isUUID(value.active_run_id) ||
      !runIDs.has(value.active_run_id))
  ) {
    throw new Error("桌面活动运行引用无效")
  }
  return {
    schema_version: 1,
    plans,
    runs,
    ...(value.active_run_id === undefined
      ? {}
      : { active_run_id: value.active_run_id }),
  }
}

async function callBinding<T>(
  invoke: () => Promise<unknown>,
  parse: (value: unknown) => T,
): Promise<T> {
  try {
    return parse(await invoke())
  } catch (error) {
    if (isProtocolError(error)) throw error
    throw normalizeBindingError(error)
  }
}

function normalizeBindingError(error: unknown): DesktopClientError {
  if (error instanceof DesktopClientError) return error
  const candidate =
    isRecord(error) && typeof error.code === "string"
      ? error.code
      : error instanceof Error
        ? error.message.trim()
        : typeof error === "string"
          ? error.trim()
          : ""
  return new DesktopClientError(
    isDesktopErrorCode(candidate) ? candidate : "operation_failed",
  )
}

function isProtocolError(error: unknown): boolean {
  return (
    error instanceof Error &&
    (error.message.startsWith("桌面数据") ||
      error.message.startsWith("桌面测试计划数据") ||
      error.message.startsWith("桌面运行记录数据") ||
      error.message.startsWith("桌面活动运行引用") ||
      error.message.startsWith("桌面目录") ||
      error.message.startsWith("桌面报告"))
  )
}

function isDesktopErrorCode(value: string): value is DesktopErrorCode {
  return Object.hasOwn(PUBLIC_ERROR_MESSAGES, value)
}

function updateRun(
  snapshot: WorkspaceSnapshot,
  runId: string,
  status: "draining" | "cancelled",
): WorkspaceSnapshot {
  let found = false
  const runs = snapshot.runs.map((run) => {
    if (run.id !== runId) return run
    found = true
    return { ...run, status }
  })
  if (!found) throw new Error("运行不存在")
  return { ...snapshot, runs }
}

function cloneSnapshot(snapshot: WorkspaceSnapshot): WorkspaceSnapshot {
  return structuredClone(snapshot)
}

function replaceByID<T extends { id: string }>(items: T[], id: string, replacement: T): T[] {
  if (!items.some((item) => item.id === id)) throw new DesktopClientError("invalid_identifier")
  return items.map((item) => (item.id === id ? replacement : item))
}

function removeByID<T extends { id: string }>(items: T[], id: string): T[] {
  if (!items.some((item) => item.id === id)) throw new DesktopClientError("invalid_identifier")
  return items.filter((item) => item.id !== id)
}

function planFromCommand(id: string, revision: number, command: CreatePlanCommand): CatalogSnapshot["plans"][number] {
  return {
    id,
    revision,
    name: command.name,
    model_count: command.model_ids.length,
    channel_count: command.channel_ids.length,
    case_count: command.cases.length,
    load_mode: command.load_mode,
    concurrency: command.concurrency,
    request_count: command.request_count,
    rate_per_second: command.rate_per_second,
    duration_ms: command.duration_ms,
    request_timeout_ms: command.request_timeout_ms,
    model_ids: [...command.model_ids],
    channel_ids: [...command.channel_ids],
    ...(command.suite_id === undefined ? {} : { suite_id: command.suite_id, suite_revision: command.suite_revision }),
    cases: structuredClone(command.cases),
    sla_thresholds: { ...command.sla_thresholds },
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

function isWorkspacePlan(value: unknown): boolean {
  return (
    isRecord(value) &&
    isUUID(value.id) &&
    isPositiveSafeInteger(value.revision) &&
    isNonBlankString(value.name) &&
    isNonNegativeSafeInteger(value.case_count) &&
    isNonNegativeSafeInteger(value.run_count) &&
    isLoadMode(value.load_mode) &&
    isPositiveSafeInteger(value.concurrency) &&
    isNonNegativeSafeInteger(value.request_count) &&
    isNonNegativeFinite(value.rate_per_second) &&
    isNonNegativeSafeInteger(value.duration_ms) &&
    ((value.request_count as number) > 0 || (value.duration_ms as number) > 0) &&
    isPositiveSafeInteger(value.request_timeout_ms)
  )
}

function parsePlan(value: unknown) {
  if (!isWorkspacePlan(value)) throw new Error("桌面测试计划数据无效")
  const record = value as Record<string, unknown>
  return {
    id: record.id as string,
    revision: record.revision as number,
    name: record.name as string,
    case_count: record.case_count as number,
    run_count: record.run_count as number,
    load_mode: record.load_mode as "single" | "fixed_concurrency" | "open_loop",
    concurrency: record.concurrency as number,
    request_count: record.request_count as number,
    rate_per_second: record.rate_per_second as number,
    duration_ms: record.duration_ms as number,
    request_timeout_ms: record.request_timeout_ms as number,
  }
}

function isWorkspaceRun(value: unknown): boolean {
  return (
    isRecord(value) &&
    isUUID(value.id) &&
    isPositiveSafeInteger(value.revision) &&
    isUUID(value.plan_id) &&
    isPositiveSafeInteger(value.plan_revision) &&
    isNonBlankString(value.plan_name) &&
    isRunStatus(value.status) &&
    isUUID(value.model_id) &&
    isPositiveSafeInteger(value.model_revision) &&
    isNonBlankString(value.model_name) &&
    isUUID(value.channel_id) &&
    isPositiveSafeInteger(value.channel_revision) &&
    isNonBlankString(value.channel_name) &&
    isLoadMode(value.load_mode) &&
    isPositiveSafeInteger(value.concurrency) &&
    isNonNegativeFinite(value.rate_per_second) &&
    isNonNegativeSafeInteger(value.planned) &&
    isNonNegativeSafeInteger(value.duration_ms) &&
    ((value.planned as number) > 0 || (value.duration_ms as number) > 0) &&
    isConclusion(value.conclusion) &&
    isNonNegativeSafeInteger(value.completed) &&
    isNonNegativeSafeInteger(value.passed) &&
    isNonNegativeSafeInteger(value.failed) &&
    value.completed === value.passed + value.failed &&
    (value.planned === 0 || value.completed <= value.planned) &&
    isNonNegativeSafeInteger(value.artifact_count) &&
    isTimestamp(value.started_at) &&
    isTimestamp(value.updated_at)
  )
}

function parseRun(value: unknown) {
  if (!isWorkspaceRun(value)) throw new Error("桌面运行记录数据无效")
  const record = value as Record<string, unknown>
  return {
    id: record.id as string,
    revision: record.revision as number,
    plan_id: record.plan_id as string,
    plan_revision: record.plan_revision as number,
    plan_name: record.plan_name as string,
    status: record.status as
      | "queued"
      | "starting"
      | "running"
      | "draining"
      | "completed"
      | "failed"
      | "cancelled",
    conclusion: record.conclusion as "none" | "passed" | "failed",
    model_id: record.model_id as string,
    model_revision: record.model_revision as number,
    model_name: record.model_name as string,
    channel_id: record.channel_id as string,
    channel_revision: record.channel_revision as number,
    channel_name: record.channel_name as string,
    load_mode: record.load_mode as "single" | "fixed_concurrency" | "open_loop",
    concurrency: record.concurrency as number,
    rate_per_second: record.rate_per_second as number,
    planned: record.planned as number,
    duration_ms: record.duration_ms as number,
    completed: record.completed as number,
    passed: record.passed as number,
    failed: record.failed as number,
    artifact_count: record.artifact_count as number,
    started_at: record.started_at as string,
    updated_at: record.updated_at as string,
  }
}

function isConclusion(value: unknown): boolean {
  return value === "none" || value === "passed" || value === "failed"
}

function isRunStatus(value: unknown): boolean {
  return (
    value === "queued" ||
    value === "starting" ||
    value === "running" ||
    value === "draining" ||
    value === "completed" ||
    value === "failed" ||
    value === "cancelled"
  )
}

function isLoadMode(value: unknown): boolean {
  return value === "single" || value === "fixed_concurrency" || value === "open_loop"
}

function isUUID(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      value,
    ) &&
    value !== "00000000-0000-0000-0000-000000000000"
  )
}

function isNonBlankString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0
}

function isPositiveSafeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) > 0
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0
}

function isNonNegativeFinite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
}

function isTimestamp(value: unknown): value is string {
  return (
    typeof value === "string" &&
    (value.endsWith("Z") || /[+-]00:00$/.test(value)) &&
    Number.isFinite(Date.parse(value))
  )
}
