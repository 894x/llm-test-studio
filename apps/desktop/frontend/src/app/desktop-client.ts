import { DesktopDataError } from "@/app/data-error"
import { parseQuickTaskDetail, type QuickTaskDetail, type RememberQuickTaskCredentialCommand } from "@/features/quick-test/task-draft"
import { translateDesktop as tx } from "@/i18n/runtime"
import type { StartQuickTaskCommand, StartRunTargetCommand, WorkspaceSnapshot } from "@/features/runs/data"
import {
  EMPTY_COMPARISONS,
  parseComparisonSnapshot,
  type ComparisonSnapshot,
  type StartComparisonCommand,
} from "@/features/comparisons/data"
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
  parseExportedReport,
  parseReportDetail,
  parseReportSnapshot,
  type ExportedReport,
  type ReportDetail,
  type ReportExportFormat,
  type ReportSnapshot,
} from "@/features/reports/data"
import {
  estimateQuickPerformanceOpenLoopRequestCap,
  parseQuickPerformanceProgress,
  parseQuickPerformanceReport,
  parseQuickTestResult,
  type QuickPerformanceCommand,
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickPerformanceSliceCount,
  type QuickPerformanceSliceLatency,
  type QuickTestCommand,
  type QuickTestResult,
  type SaveQuickTestConnectionCommand,
} from "@/features/quick-test/data"
import enCommon from "@/i18n/resources/en-US/common.json"
import zhCommon from "@/i18n/resources/zh-CN/common.json"

export type DesktopErrorCode =
  | "desktop_not_started"
  | "desktop_startup_failed"
  | "desktop_stopped"
  | "workspace_unavailable"
  | "catalog_unavailable"
  | "reports_unavailable"
  | "run_commands_unavailable"
  | "comparison_unavailable"
  | "diagnostics_unavailable"
  | "quick_test_unavailable"
  | "quick_test_save_partial"
  | "invalid_identifier"
  | "operation_cancelled"
  | "operation_failed"
  | "run_invalid"
  | "run_not_runnable"
  | "quick_task_credential_unavailable"
  | "plan_protocol_mismatch"
  | "catalog_invalid"
  | "catalog_revision_conflict"
  | "catalog_not_found"
  | "catalog_saved_refresh_failed"

type PublicErrorMessages = Record<DesktopErrorCode, string> & {
  operationFailed: string
}

const PUBLIC_ERROR_MESSAGES = {
  "zh-CN": zhCommon.error as PublicErrorMessages,
  "en-US": enCommon.error as PublicErrorMessages,
} as const

export class DesktopClientError extends Error {
  readonly code: DesktopErrorCode

  constructor(code: DesktopErrorCode) {
    super(currentPublicErrorMessages()[code])
    this.name = "DesktopClientError"
    this.code = code
  }
}

export function publicDesktopErrorMessage(
  error: unknown,
  fallback: string,
): string {
  return error instanceof DesktopClientError
    ? currentPublicErrorMessages()[error.code]
    : fallback
}

export function publicDesktopOperationErrorMessage(
  error: unknown,
  operation: string,
  fallback: string,
): string {
  return currentPublicErrorMessages().operationFailed
    .replace("{{operation}}", operation)
    .replace("{{message}}", publicDesktopErrorMessage(error, fallback))
}

function currentPublicErrorMessages(): PublicErrorMessages {
  return typeof document !== "undefined" && document.documentElement.lang === "en-US"
    ? PUBLIC_ERROR_MESSAGES["en-US"]
    : PUBLIC_ERROR_MESSAGES["zh-CN"]
}

export function isCatalogSavedRefreshFailure(error: unknown): error is DesktopClientError {
  return error instanceof DesktopClientError && error.code === "catalog_saved_refresh_failed"
}

export interface DesktopClient extends CatalogActions {
  getDiagnostics(): Promise<DesktopDiagnosticsSnapshot>
  openDiagnosticsDirectory(): Promise<void>
  getWorkspace(): Promise<WorkspaceSnapshot>
  getCatalog(): Promise<CatalogSnapshot>
  getReports(): Promise<ReportSnapshot>
  getReportDetail(reportId: string): Promise<ReportDetail>
  exportReport(reportId: string, format: ReportExportFormat, watermark: string, locale: string): Promise<ExportedReport>
  saveReportExport(filename: string, mediaType: string, dataBase64: string, locale: string): Promise<boolean>
  copyReportPNG(dataBase64: string): Promise<void>
  getComparisons(): Promise<ComparisonSnapshot>
  startRun(planId: string): Promise<WorkspaceSnapshot>
  startRunTarget(command: StartRunTargetCommand): Promise<WorkspaceSnapshot>
  startQuickTask(command: StartQuickTaskCommand): Promise<string>
  getQuickTask(runId: string): Promise<QuickTaskDetail>
  rememberQuickTaskCredential(command: RememberQuickTaskCredentialCommand): Promise<void>
  forgetQuickTaskCredential(runId: string): Promise<void>
  stopSending(runId: string): Promise<WorkspaceSnapshot>
  cancelRun(runId: string): Promise<WorkspaceSnapshot>
  startComparison(command: StartComparisonCommand): Promise<ComparisonSnapshot>
  runQuickTest(command: QuickTestCommand): Promise<QuickTestResult>
  runQuickPerformanceTest(command: QuickPerformanceCommand, onProgress?: (progress: QuickPerformanceProgress) => void): Promise<QuickPerformanceReport>
  saveQuickTestConnection(command: SaveQuickTestConnectionCommand): Promise<CatalogSnapshot>
}

export type DesktopDiagnosticsSnapshot = {
  schema_version: 1
  available: boolean
  format: "jsonl"
  max_file_bytes: number
  backup_files: number
  run_correlation: boolean
  request_correlation: boolean
}

type WailsDesktopBinding = {
  GetDiagnostics(): Promise<unknown>
  OpenDiagnosticsDirectory(): Promise<unknown>
  ReportFrontendDiagnostic(diagnostic: FrontendDiagnostic): Promise<unknown>
  GetWorkspace(): Promise<unknown>
  GetCatalog(): Promise<unknown>
  GetReports(): Promise<unknown>
  GetReportDetail(reportId: string): Promise<unknown>
  ExportReport(reportId: string, format: ReportExportFormat, watermark: string, locale: string): Promise<unknown>
  SaveReportExport(filename: string, mediaType: string, dataBase64: string, locale: string): Promise<unknown>
  CopyReportPNG(dataBase64: string): Promise<unknown>
  GetComparisons(): Promise<unknown>
  StartRun(planId: string): Promise<unknown>
  StartRunTarget(command: StartRunTargetCommand): Promise<unknown>
  StartQuickTask(command: StartQuickTaskCommand): Promise<unknown>
  GetQuickTask(runId: string): Promise<unknown>
  RememberQuickTaskCredential(command: RememberQuickTaskCredentialCommand): Promise<unknown>
  ForgetQuickTaskCredential(runId: string): Promise<unknown>
  StopSending(runId: string): Promise<unknown>
  CancelRun(runId: string): Promise<unknown>
  StartComparison(command: StartComparisonCommand): Promise<unknown>
  RunQuickTest(command: QuickTestCommand): Promise<unknown>
  RunQuickPerformanceTest(command: QuickPerformanceCommand, progressId: string): Promise<unknown>
  SaveQuickTestConnection(command: SaveQuickTestConnectionCommand): Promise<unknown>
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

type FrontendDiagnosticBinding = Pick<WailsDesktopBinding, "ReportFrontendDiagnostic">

type FrontendDiagnostic = {
  operation: "load_workspace" | "load_catalog" | "load_reports" | "load_comparisons"
  error_code: "frontend_data_invalid" | "frontend_operation_failed"
  detail: string
}

const REQUIRED_WAILS_BINDING_METHODS = [
  "GetDiagnostics", "OpenDiagnosticsDirectory", "ReportFrontendDiagnostic",
  "GetWorkspace", "GetCatalog", "GetReports", "GetReportDetail", "ExportReport",
  "SaveReportExport", "CopyReportPNG", "GetComparisons", "StartRun", "StartRunTarget", "StartQuickTask", "GetQuickTask", "RememberQuickTaskCredential", "ForgetQuickTaskCredential",
  "StopSending", "CancelRun", "StartComparison", "RunQuickTest", "RunQuickPerformanceTest",
  "SaveQuickTestConnection", "CreateModel", "UpdateModel", "DeleteModel", "CreateChannel",
  "UpdateChannel", "DeleteChannel", "CreateChannelModel", "UpdateChannelModel",
  "DeleteChannelModel", "CreateTestCase", "UpdateTestCase", "DeleteTestCase", "CreateSuite",
  "UpdateSuite", "DeleteSuite", "CreatePlan", "UpdatePlan", "DeletePlan",
] as const satisfies ReadonlyArray<keyof WailsDesktopBinding>

const WAILS_BINDING_WAIT_TIMEOUT_MS = 1_500
const WAILS_BINDING_POLL_INTERVAL_MS = 25

class WailsBindingUnavailableError extends DesktopClientError {
  readonly diagnosticDetail: string

  constructor(diagnosticDetail: string) {
    super("workspace_unavailable")
    this.name = "WailsBindingUnavailableError"
    this.diagnosticDetail = diagnosticDetail
  }
}

export function createDesktopClient(): DesktopClient {
  const candidate = readWailsBindingCandidate()
  const binding = readWailsBinding(candidate)
  if (binding) return wailsClient(binding)
  if (import.meta.env.DEV && !candidate) return createLazyFixtureClient()
  return wailsClient(deferredWailsBinding(candidate))
}

export function createFixtureClient(
  initial: WorkspaceSnapshot,
  catalog: CatalogSnapshot = EMPTY_CATALOG,
  reports: ReportSnapshot = EMPTY_REPORTS,
	comparisons: ComparisonSnapshot = EMPTY_COMPARISONS,
): DesktopClient {
  let workspace = cloneSnapshot(initial)
  let catalogState = structuredClone(catalog)
	let comparisonState = structuredClone(comparisons)
  const nextID = () => crypto.randomUUID()
  const quickTasks = new Map<string, QuickTaskDetail>()
  const rememberedTaskCredentials = new Set<string>()
  const refreshChannelCounts = () => {
    catalogState.channels = catalogState.channels.map((channel) => ({
      ...channel,
      model_count: catalogState.channel_models.filter((mapping) => mapping.channel_id === channel.id).length,
    }))
  }
  return {
    async getDiagnostics() {
      return fixtureDiagnosticsSnapshot()
    },
    async openDiagnosticsDirectory() {},
    async getWorkspace() {
      return cloneSnapshot(workspace)
    },
    async getCatalog() {
      return structuredClone(catalogState)
    },
    async getReports() {
      return structuredClone(reports)
    },
		async getReportDetail(reportId) {
			const summary = reports.reports.find((report) => report.id === reportId)
			if (!summary) throw new DesktopClientError("invalid_identifier")
			return fixtureReportDetail(summary)
		},
		async exportReport(reportId, format, watermark) {
			const summary = reports.reports.find((report) => report.id === reportId)
			if (!summary) throw new DesktopClientError("invalid_identifier")
			const detail = fixtureReportDetail(summary)
			const mediaTypes: Record<ReportExportFormat, string> = { json: "application/json", html: "text/html; charset=utf-8", png: "image/png", pdf: "application/pdf" }
			const payload = format === "json" ? JSON.stringify({ watermark: watermark.trim() || "rhzs", ...detail }, null, 2) : `LLM Test Studio ${format.toUpperCase()} report ${reportId} watermark ${watermark.trim() || "rhzs"}`
			return { filename: `llm-test-studio-report-${reportId}.${format}`, media_type: mediaTypes[format], data_base64: bytesToBase64(new TextEncoder().encode(payload)) }
		},
		async saveReportExport() {
			return true
		},
		async copyReportPNG() {},
		async getComparisons() {
			return structuredClone(comparisonState)
		},
    async startRun(planId) {
      if (!workspace.plans.some((plan) => plan.id === planId)) {
        throw new DesktopClientError("invalid_identifier")
      }
      return cloneSnapshot(workspace)
    },
		async startRunTarget(command) {
			const plan = catalogState.plans.find((item) => item.id === command.plan_id)
			const model = catalogState.models.find((item) => item.id === command.model_id)
			const channel = catalogState.channels.find((item) => item.id === command.channel_id)
			const mapped = catalogState.channel_models.some((item) => item.model_id === command.model_id && item.channel_id === command.channel_id)
			if (!plan || !model || !channel || !mapped) throw new DesktopClientError("invalid_identifier")
			return cloneSnapshot(workspace)
		},
    async startQuickTask(command) {
      const suite = command.source_run_id ? quickTasks.get(command.source_run_id)?.suite : catalogState.suites.find((item) => item.id === command.suite_id && item.revision === command.suite_revision)
      if (!suite?.quick_test || (suite.model_target && suite.model_target !== command.model)) throw new DesktopClientError("run_not_runnable")
      const channel = catalogState.channels.find((item) => item.id === command.channel_id)
      if (command.channel_id && (!channel?.enabled || channel.protocol !== suite.protocol || !channel.credential_configured)) throw new DesktopClientError("run_not_runnable")
      if (!command.model || (!channel && (!command.base_url || (!command.api_key && !command.credential_run_id))) || (channel && (command.base_url || command.api_key))) throw new DesktopClientError("run_invalid")
      if (command.credential_run_id) {
        const source = quickTasks.get(command.credential_run_id)
        if (channel || command.api_key || !source || !rememberedTaskCredentials.has(command.credential_run_id) || source.base_url !== command.base_url || source.suite.protocol !== suite.protocol) throw new DesktopClientError("quick_task_credential_unavailable")
      }
      const id = nextID()
      const now = new Date().toISOString()
      workspace.runs.unshift({
        id, revision: 1, source: "quick_task", plan_id: id, plan_revision: 1, plan_name: suite.name,
        model_id: nextID(), model_revision: 1, model_name: command.model,
        channel_id: channel?.id ?? nextID(), channel_revision: channel?.revision ?? 1, channel_name: channel?.name ?? new URL(command.base_url!).host,
        status: "queued", conclusion: "none", load_mode: "fixed_concurrency", concurrency: 1,
        rate_per_second: 0, planned: 0, duration_ms: 0, completed: 0, passed: 0, failed: 0,
        artifact_count: 0, started_at: now, updated_at: now,
      })
      workspace.active_run_id = id
      quickTasks.set(id, { schema_version: 1, run_id: id, suite: structuredClone(suite), model: command.model, base_url: channel?.base_url ?? command.base_url!, ...(channel ? { channel_id: channel.id } : {}), inputs: { ...Object.fromEntries(suite.quick_test.inputs.map((input) => [input.key, input.default])), ...structuredClone(command.inputs) }, ...(command.credential_run_id ? { credential_run_id: command.credential_run_id } : {}) })
      return id
    },
    async getQuickTask(runId) {
      const detail = quickTasks.get(runId)
      if (!detail) throw new DesktopClientError("run_not_runnable")
      const credentialRunID = rememberedTaskCredentials.has(runId) ? runId : detail.credential_run_id && rememberedTaskCredentials.has(detail.credential_run_id) ? detail.credential_run_id : undefined
      return { ...structuredClone(detail), credential_run_id: credentialRunID }
    },
    async rememberQuickTaskCredential(command) {
      const source = quickTasks.get(command.run_id)
      if (!source || source.channel_id || !command.api_key || source.base_url !== command.base_url || source.suite.protocol !== command.protocol) throw new DesktopClientError("run_not_runnable")
      rememberedTaskCredentials.add(command.run_id)
    },
    async forgetQuickTaskCredential(runId) { rememberedTaskCredentials.delete(runId) },
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
		async startComparison(command) {
			const plan = catalogState.plans.find((item) => item.id === command.plan_id)
			const model = catalogState.models.find((item) => item.id === command.model_id)
			const channels = command.channel_ids.map((id) => catalogState.channels.find((item) => item.id === id))
			if (!plan || !model || channels.length < 2 || channels.some((item) => !item)) {
				throw new DesktopClientError("invalid_identifier")
			}
			comparisonState.comparisons.unshift({
				id: nextID(), created_at: new Date().toISOString(), status: "running",
				plan_id: plan.id, plan_name: plan.name, model_id: model.id, model_name: model.name,
				channels: channels.map((channel) => ({
					channel_id: channel!.id, channel_name: channel!.name, run_id: nextID(),
					run_status: "queued", report_ready: false, passed: false, verdict: "", metrics: {},
				})),
			})
			return structuredClone(comparisonState)
		},
    async runQuickTest(command) {
      const normalizedURL = command.url.replace(/\/+$/, "")
      const endpoint = command.address_mode === "base_url"
        ? `${normalizedURL}/chat/completions`
        : command.url
      return {
        schema_version: 1,
        success: true,
        address_mode: command.address_mode,
        base_url: command.address_mode === "base_url"
          ? normalizedURL
          : normalizedURL.replace(/\/chat\/completions$/, ""),
        endpoint,
        http_status: 200,
        e2e_ms: 42,
        prompt_tokens: 8,
        completion_tokens: 1,
        cached_tokens: 0,
      }
    },
    async runQuickPerformanceTest(command, onProgress) {
      onProgress?.(fixtureQuickPerformanceProgress(command, "sending", 0))
      onProgress?.(fixtureQuickPerformanceProgress(command, "completed", command.request_count || Math.max(1, command.concurrency * 2)))
      return fixtureQuickPerformanceReport(command)
    },
    async saveQuickTestConnection(command) {
      let modelID = command.existing_model_id
      if (modelID) {
        const existing = catalogState.models.find((model) => model.id === modelID)
        if (!existing || existing.protocol !== "openai-chat") {
          throw new DesktopClientError("catalog_not_found")
        }
      } else {
        modelID = nextID()
        catalogState.models.push({
          id: modelID, revision: 1, name: command.model_name,
          protocol: "openai-chat", capabilities: ["chat"],
        })
      }
      const channelID = nextID()
      catalogState.channels.push({
        id: channelID, revision: 1, name: command.channel_name,
        base_url: command.base_url, protocol: "openai-chat", enabled: true,
        credential_configured: true, model_count: 1,
      })
      catalogState.channel_models.push({
        id: nextID(), revision: 1, channel_id: channelID, model_id: modelID,
        upstream_model_name: command.model_id,
      })
      return structuredClone(catalogState)
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
      catalogState.channels.push({ id: nextID(), revision: 1, ...structuredClone(command), credential_configured: true, model_count: 0 })
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
        credential_configured: true,
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
      catalogState.test_cases.push({ id: nextID(), revision: 1, ...structuredClone(command) })
      return structuredClone(catalogState)
    },
    async updateTestCase(command) {
      const { id, expected_revision, ...editable } = command
      catalogState.test_cases = replaceByID(catalogState.test_cases, command.id, {
        id,
        revision: expected_revision + 1,
        ...structuredClone(editable),
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
      const { id, expected_revision, ...editable } = command
      catalogState.suites = replaceByID(catalogState.suites, command.id, {
        id,
        revision: expected_revision + 1,
        ...structuredClone(editable),
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
    getDiagnostics: async () => (await client).getDiagnostics(),
    openDiagnosticsDirectory: async () =>
      (await client).openDiagnosticsDirectory(),
    getWorkspace: async () => (await client).getWorkspace(),
    getCatalog: async () => (await client).getCatalog(),
    getReports: async () => (await client).getReports(),
		getReportDetail: async (reportId) => (await client).getReportDetail(reportId),
		exportReport: async (reportId, format, watermark, locale) => (await client).exportReport(reportId, format, watermark, locale),
		saveReportExport: async (filename, mediaType, dataBase64, locale) => (await client).saveReportExport(filename, mediaType, dataBase64, locale),
		copyReportPNG: async (dataBase64) => (await client).copyReportPNG(dataBase64),
		getComparisons: async () => (await client).getComparisons(),
    startRun: async (planId) => (await client).startRun(planId),
		startRunTarget: async (command) => (await client).startRunTarget(command),
		startQuickTask: async (command) => (await client).startQuickTask(command),
		getQuickTask: async (runId) => (await client).getQuickTask(runId),
    rememberQuickTaskCredential: async (command) => (await client).rememberQuickTaskCredential(command),
    forgetQuickTaskCredential: async (runId) => (await client).forgetQuickTaskCredential(runId),
    stopSending: async (runId) => (await client).stopSending(runId),
    cancelRun: async (runId) => (await client).cancelRun(runId),
		startComparison: async (command) => (await client).startComparison(command),
    runQuickTest: async (command) => (await client).runQuickTest(command),
    runQuickPerformanceTest: async (command, onProgress) => (await client).runQuickPerformanceTest(command, onProgress),
    saveQuickTestConnection: async (command) => (await client).saveQuickTestConnection(command),
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
    getDiagnostics: async () =>
      callBinding(() => binding.GetDiagnostics(), parseDiagnosticsSnapshot),
    openDiagnosticsDirectory: async () =>
      callBinding(() => binding.OpenDiagnosticsDirectory(), parseVoid),
    getWorkspace: async () =>
      callBinding(
        () => binding.GetWorkspace(),
        parseSnapshot,
        reportFrontendFailure(binding, "load_workspace"),
        "workspace_unavailable",
      ),
    getCatalog: async () =>
      callBinding(
        () => binding.GetCatalog(),
        parseCatalogSnapshot,
        reportFrontendFailure(binding, "load_catalog"),
        "catalog_unavailable",
      ),
    getReports: async () =>
      callBinding(
        () => binding.GetReports(),
        parseReportSnapshot,
        reportFrontendFailure(binding, "load_reports"),
        "reports_unavailable",
      ),
		getReportDetail: async (reportId) =>
			callBinding(() => binding.GetReportDetail(reportId), parseReportDetail),
		exportReport: async (reportId, format, watermark, locale) =>
			callBinding(() => binding.ExportReport(reportId, format, watermark, locale), parseExportedReport),
		saveReportExport: async (filename, mediaType, dataBase64, locale) =>
			callBinding(() => binding.SaveReportExport(filename, mediaType, dataBase64, locale), parseBoolean),
		copyReportPNG: async (dataBase64) =>
			callBinding(() => binding.CopyReportPNG(dataBase64), parseVoid),
		getComparisons: async () =>
			callBinding(
				() => binding.GetComparisons(),
				parseComparisonSnapshot,
				reportFrontendFailure(binding, "load_comparisons"),
				"comparison_unavailable",
			),
    startRun: async (planId) =>
      callBinding(() => binding.StartRun(planId), parseSnapshot),
		startRunTarget: async (command) =>
			callBinding(() => binding.StartRunTarget(command), parseSnapshot),
		startQuickTask: async (command) =>
			callBinding(() => binding.StartQuickTask(command), (value) => {
        if (!isUUID(value)) throw new DesktopDataError(tx("desktop:quick_task_invalid_run_identity"))
        return value
      }),
		getQuickTask: async (runId) => callBinding(() => binding.GetQuickTask(runId), (value) => {
      const detail = parseQuickTaskDetail(value)
      if (detail.run_id !== runId) throw new DesktopDataError(tx("quickTest:task.invalidHistory"))
      return detail
    }),
    stopSending: async (runId) =>
      callBinding(() => binding.StopSending(runId), parseSnapshot),
    cancelRun: async (runId) =>
      callBinding(() => binding.CancelRun(runId), parseSnapshot),
		startComparison: async (command) =>
			callBinding(() => binding.StartComparison(command), parseComparisonSnapshot),
    rememberQuickTaskCredential: async (command) => callBinding(() => binding.RememberQuickTaskCredential(command), () => undefined),
    forgetQuickTaskCredential: async (runId) => callBinding(() => binding.ForgetQuickTaskCredential(runId), () => undefined),
    runQuickTest: async (command) =>
      callBinding(() => binding.RunQuickTest(command), parseQuickTestResult),
    runQuickPerformanceTest: async (command, onProgress) => {
      const subscription = subscribeQuickPerformanceProgress(onProgress)
      try {
        return await callBinding(
          () => binding.RunQuickPerformanceTest(command, subscription.progressID),
          parseQuickPerformanceReport,
        )
      } finally {
        subscription.unsubscribe()
      }
    },
    saveQuickTestConnection: async (command) =>
      callCatalogMutation(binding, () => binding.SaveQuickTestConnection(command)),
    createModel: async (command) => callCatalogMutation(binding, () => binding.CreateModel(command)),
    updateModel: async (command) => callCatalogMutation(binding, () => binding.UpdateModel(command)),
    deleteModel: async (command) => callCatalogMutation(binding, () => binding.DeleteModel(command)),
    createChannel: async (command) => callCatalogMutation(binding, () => binding.CreateChannel(command)),
    updateChannel: async (command) => callCatalogMutation(binding, () => binding.UpdateChannel(command)),
    deleteChannel: async (command) => callCatalogMutation(binding, () => binding.DeleteChannel(command)),
    createChannelModel: async (command) => callCatalogMutation(binding, () => binding.CreateChannelModel(command)),
    updateChannelModel: async (command) => callCatalogMutation(binding, () => binding.UpdateChannelModel(command)),
    deleteChannelModel: async (command) => callCatalogMutation(binding, () => binding.DeleteChannelModel(command)),
    createTestCase: async (command) => callCatalogMutation(binding, () => binding.CreateTestCase(command)),
    updateTestCase: async (command) => callCatalogMutation(binding, () => binding.UpdateTestCase(command)),
    deleteTestCase: async (command) => callCatalogMutation(binding, () => binding.DeleteTestCase(command)),
    createSuite: async (command) => callCatalogMutation(binding, () => binding.CreateSuite(command)),
    updateSuite: async (command) => callCatalogMutation(binding, () => binding.UpdateSuite(command)),
    deleteSuite: async (command) => callCatalogMutation(binding, () => binding.DeleteSuite(command)),
    createPlan: async (command) => callCatalogMutation(binding, () => binding.CreatePlan(command)),
    updatePlan: async (command) => callCatalogMutation(binding, () => binding.UpdatePlan(command)),
    deletePlan: async (command) => callCatalogMutation(binding, () => binding.DeletePlan(command)),
  }
}

function deferredWailsBinding(
	initialCandidate?: Partial<WailsDesktopBinding>,
): WailsDesktopBinding {
	let resolution: Promise<WailsDesktopBinding> | undefined
	const resolve = () => {
		resolution ??= waitForWailsBinding(initialCandidate)
		return resolution
	}
	return new Proxy({} as WailsDesktopBinding, {
		get(_target, property) {
			if (property === "ReportFrontendDiagnostic") {
				return async (diagnostic: FrontendDiagnostic) => {
					const candidate = readWailsBindingCandidate() ?? initialCandidate
					const reporter = candidate && readFrontendDiagnosticBinding(candidate)
					if (reporter) return reporter.ReportFrontendDiagnostic(diagnostic)
					return (await resolve()).ReportFrontendDiagnostic(diagnostic)
				}
			}
			return async (...args: unknown[]) => {
				const binding = await resolve()
				const method = Reflect.get(binding, property)
				if (typeof method !== "function") {
					throw new WailsBindingUnavailableError(bindingAvailabilityDetail(binding))
				}
				return Reflect.apply(method, binding, args)
			}
		},
	})
}

async function waitForWailsBinding(
	initialCandidate?: Partial<WailsDesktopBinding>,
): Promise<WailsDesktopBinding> {
	const deadline = Date.now() + WAILS_BINDING_WAIT_TIMEOUT_MS
	for (;;) {
		const candidate = readWailsBindingCandidate() ?? initialCandidate
		const binding = readWailsBinding(candidate)
		if (binding) return binding
		if (Date.now() >= deadline) {
			throw new WailsBindingUnavailableError(bindingAvailabilityDetail(candidate))
		}
		await new Promise((resolve) => window.setTimeout(resolve, WAILS_BINDING_POLL_INTERVAL_MS))
	}
}

function bindingAvailabilityDetail(candidate?: Partial<WailsDesktopBinding>): string {
	if (!candidate) {
		return "Wails desktop binding did not become ready before startup timeout"
	}
	const missing = REQUIRED_WAILS_BINDING_METHODS.filter(
		(method) => typeof candidate[method] !== "function",
	)
	return missing.length > 0
		? `Wails desktop binding is incomplete; missing methods: ${missing.join(", ")}`
		: "Wails desktop binding did not become callable before startup timeout"
}

function readWailsBindingCandidate(): Partial<WailsDesktopBinding> | undefined {
  const root = window as typeof window & {
    go?: { main?: { DesktopApp?: Partial<WailsDesktopBinding> } }
  }
  return root.go?.main?.DesktopApp
}

function readFrontendDiagnosticBinding(
	candidate: Partial<WailsDesktopBinding>,
): FrontendDiagnosticBinding | undefined {
	return typeof candidate.ReportFrontendDiagnostic === "function"
		? candidate as FrontendDiagnosticBinding
		: undefined
}

function readWailsBinding(
	candidate: Partial<WailsDesktopBinding> | undefined = readWailsBindingCandidate(),
): WailsDesktopBinding | undefined {
	if (!candidate || REQUIRED_WAILS_BINDING_METHODS.some(
		(method) => typeof candidate[method] !== "function",
	)) {
    return undefined
  }
  return candidate as WailsDesktopBinding
}

function fixtureDiagnosticsSnapshot(): DesktopDiagnosticsSnapshot {
  return {
    schema_version: 1,
    available: true,
    format: "jsonl",
    max_file_bytes: 10 * 1024 * 1024,
    backup_files: 5,
    run_correlation: true,
    request_correlation: true,
  }
}

const quickPerformanceProgressEventName = "quick-performance-progress"

function subscribeQuickPerformanceProgress(onProgress?: (progress: QuickPerformanceProgress) => void): {
  progressID: string
  unsubscribe: () => void
} {
  if (!onProgress) return { progressID: "", unsubscribe: () => {} }
  const runtime = (window as typeof window & {
    runtime?: { EventsOn?: (eventName: string, callback: (payload: unknown) => void) => () => void }
  }).runtime
  if (typeof runtime?.EventsOn !== "function") return { progressID: "", unsubscribe: () => {} }
  const progressID = crypto.randomUUID()
  const unsubscribe = runtime.EventsOn(quickPerformanceProgressEventName, (payload) => {
    if (!isRecord(payload) || payload.progress_id !== progressID) return
    try {
      onProgress(parseQuickPerformanceProgress(payload.progress))
    } catch {
      // Ignore malformed or stale event payloads at the desktop boundary.
    }
  })
  return { progressID, unsubscribe }
}

function fixtureQuickPerformanceReport(command: QuickPerformanceCommand): QuickPerformanceReport {
  const configuredInFlight = command.load_mode === "open_loop" ? command.max_in_flight : command.concurrency
  const requestBudget = fixtureQuickPerformanceBudget(command)
  const completed = command.request_count || Math.min(requestBudget.measured_cap, Math.max(1, configuredInFlight * 2))
  const totalDurationMS = Math.max(320, command.duration_ms)
  const seconds = totalDurationMS / 1_000
  const promptTokens = completed * command.input_tokens
  const completionTokens = completed * command.output_tokens
  const samples: QuickPerformanceReport["samples"] = Array.from({ length: completed }, (_, index) => ({
    request_index: index,
    scheduled_offset_ms: 0,
    started_offset_ms: index,
    finished_offset_ms: 120 + index,
    schedule_lag_ms: index,
    e2e_ms: 120,
    ttfb_ms: 15,
    ttft_any_ms: 35,
    ttft_visible_ms: 45,
    ttft_ms: 35,
    ttst_ms: 60,
    observed_icl_ms: 25,
    semantic_chunk_count: 2,
    tpot_ms: 5,
    http_status: 200,
    success: true,
    timed_out: false,
    prompt_tokens: command.input_tokens,
    completion_tokens: command.output_tokens,
    cached_tokens: 0,
    ...(command.workload_mode === "normal" ? {
      target_input_tokens: command.input_tokens,
      target_output_tokens: command.output_tokens,
    } : {}),
  }))
  const baseProgress: QuickPerformanceProgress = {
    phase: "completed",
    planned: command.capacity_enabled ? command.request_count : requestBudget.measured_cap,
    offered: completed,
    launched: completed,
    completed,
    in_flight: 0,
    peak_in_flight: Math.min(configuredInFlight, completed),
    succeeded: completed,
    failed: 0,
    rejected: 0,
    capped: false,
    send_duration_ms: totalDurationMS,
    drain_duration_ms: 0,
    total_duration_ms: totalDurationMS,
  }
  const ttft = fixturePerformanceSummary(samples.map((sample) => sample.ttft_ms))
  const ttfb = fixturePerformanceSummary(samples.map((sample) => sample.ttfb_ms!))
  const ttftVisible = fixturePerformanceSummary(samples.map((sample) => sample.ttft_visible_ms!))
  const ttst = fixturePerformanceSummary(samples.map((sample) => sample.ttst_ms!))
  const observedICL = fixturePerformanceSummary(samples.map((sample) => sample.observed_icl_ms!))
  const semanticChunkCount = fixturePerformanceSummary(samples.map((sample) => sample.semantic_chunk_count!))
  const tpot = fixturePerformanceSummary(samples.map((sample) => sample.tpot_ms))
  const e2e = fixturePerformanceSummary(samples.map((sample) => sample.e2e_ms))
  const scheduleLag = fixturePerformanceSummary(samples.map((sample) => sample.schedule_lag_ms))
  const metrics: QuickPerformanceReport["metrics"] = {
    completed, succeeded: completed, failed: 0, timed_out: 0,
    success_rate_percent: 100, offered_qps: completed / seconds,
    launched_qps: completed / seconds, completed_qps: completed / seconds,
    successful_request_qps: completed / seconds, request_qps: completed / seconds,
    rpm: completed / seconds * 60, input_tpm: promptTokens / seconds * 60,
    output_tpm: completionTokens / seconds * 60,
    total_tpm: (promptTokens + completionTokens) / seconds * 60,
    generation_tps: completionTokens / seconds,
    ttft_samples: completed,
    ttft_p50_ms: ttft.p50, ttft_p90_ms: ttft.p90, ttft_p95_ms: ttft.p95,
    ttft_p99_ms: ttft.p99, ttft_average_ms: ttft.average,
    ttfb_samples: completed, ttfb_p50_ms: ttfb.p50, ttfb_p95_ms: ttfb.p95,
    ttfb_p99_ms: ttfb.p99, ttfb_average_ms: ttfb.average,
    ttft_any_samples: completed, ttft_any_p50_ms: ttft.p50, ttft_any_p95_ms: ttft.p95,
    ttft_any_p99_ms: ttft.p99, ttft_any_average_ms: ttft.average,
    ttft_visible_samples: completed, ttft_visible_p50_ms: ttftVisible.p50,
    ttft_visible_p95_ms: ttftVisible.p95, ttft_visible_p99_ms: ttftVisible.p99,
    ttft_visible_average_ms: ttftVisible.average,
    ttst_samples: completed, ttst_p50_ms: ttst.p50, ttst_p95_ms: ttst.p95,
    ttst_p99_ms: ttst.p99, ttst_average_ms: ttst.average,
    observed_icl_samples: completed, observed_icl_p50_ms: observedICL.p50,
    observed_icl_p95_ms: observedICL.p95, observed_icl_p99_ms: observedICL.p99,
    observed_icl_average_ms: observedICL.average,
    semantic_chunk_count_samples: completed, semantic_chunk_count_p50: semanticChunkCount.p50,
    semantic_chunk_count_p95: semanticChunkCount.p95, semantic_chunk_count_p99: semanticChunkCount.p99,
    semantic_chunk_count_average: semanticChunkCount.average,
    tpot_p50_ms: tpot.p50, tpot_p90_ms: tpot.p90, tpot_p95_ms: tpot.p95,
    tpot_p99_ms: tpot.p99, tpot_average_ms: tpot.average,
    e2e_p50_ms: e2e.p50, e2e_p90_ms: e2e.p90, e2e_p95_ms: e2e.p95,
    e2e_p99_ms: e2e.p99, e2e_average_ms: e2e.average,
    schedule_lag_p50_ms: scheduleLag.p50, schedule_lag_p90_ms: scheduleLag.p90,
    schedule_lag_p95_ms: scheduleLag.p95, schedule_lag_p99_ms: scheduleLag.p99,
    schedule_lag_average_ms: scheduleLag.average,
    prompt_tokens: promptTokens, completion_tokens: completionTokens,
    cached_tokens: 0, cache_rate_percent: 0,
  }
  const sloAssessment = command.slo_target_percent > 0
    ? fixtureQuickPerformanceSLOAssessment(command, samples, totalDurationMS)
    : undefined
  const capacityTargets = fixtureQuickPerformanceCapacityTargets(command)
  const executedTargets = command.capacity_enabled && sloAssessment
    ? sloAssessment.status === "passed" ? capacityTargets : capacityTargets.slice(0, 1)
    : []
  const capacityRungs: NonNullable<QuickPerformanceReport["capacity_result"]>["rungs"] = executedTargets.map((target, index) => ({
    index,
    target,
    success: true,
    progress: {
      ...baseProgress,
      peak_in_flight: Math.min(command.load_mode === "fixed_concurrency" ? target : command.max_in_flight, completed),
      capacity_rung_number: index + 1,
      capacity_rung_count: capacityTargets.length,
      capacity_target: target,
    },
    metrics,
    failures: [],
    slo_assessment: sloAssessment!,
  }))
  const capacityResult: QuickPerformanceReport["capacity_result"] = capacityRungs.length > 0 && sloAssessment
    ? sloAssessment.status === "passed"
      ? {
        status: "passed",
        selected_rung_index: capacityRungs.length - 1,
        highest_passing_rung_index: capacityRungs.length - 1,
        rungs: capacityRungs,
      }
      : { status: "failed", selected_rung_index: 0, rungs: capacityRungs }
    : undefined
  const selectedRung = capacityResult?.selected_rung_index === undefined
    ? undefined
    : capacityResult.rungs[capacityResult.selected_rung_index]
  return {
    schema_version: 3,
    archived: false,
    archive_status: "not_attempted",
    model_id: command.model_id,
    success: true,
    address_mode: command.address_mode,
    base_url: command.url.replace(/\/chat\/completions\/?$/, "").replace(/\/+$/, ""),
    endpoint: command.address_mode === "base_url"
      ? `${command.url.replace(/\/+$/, "")}/chat/completions`
      : command.url,
    profile: {
      load_mode: command.load_mode, request_count: command.request_count, duration_ms: command.duration_ms,
      concurrency: command.concurrency,
      ...(command.load_mode === "open_loop" ? { rate_per_second: command.rate_per_second, max_in_flight: command.max_in_flight } : {}),
      arrival_pattern: command.arrival_pattern,
      workload_mode: command.workload_mode,
      random_seed: command.random_seed,
      input_tokens_stddev: command.input_tokens_stddev,
      output_tokens_stddev: command.output_tokens_stddev,
      shared_prefix_tokens: command.shared_prefix_tokens,
      ...(command.warmup_requests > 0 ? { warmup_requests: command.warmup_requests } : {}),
      ...(command.ramp_duration_ms > 0 ? { ramp_duration_ms: command.ramp_duration_ms } : {}),
      ...(command.ramp_request_cap > 0 ? { ramp_request_cap: command.ramp_request_cap } : {}),
      ...(command.slice_duration_ms > 0 ? { slice_duration_ms: command.slice_duration_ms } : {}),
      ...(command.slo_ttft_ms > 0 ? { slo_ttft_ms: command.slo_ttft_ms } : {}),
      ...(command.slo_tpot_ms > 0 ? { slo_tpot_ms: command.slo_tpot_ms } : {}),
      ...(command.slo_e2e_ms > 0 ? { slo_e2e_ms: command.slo_e2e_ms } : {}),
      ...(command.slo_target_percent > 0 ? { slo_target_percent: command.slo_target_percent } : {}),
      ...(command.capacity_enabled ? {
        capacity_enabled: true,
        capacity_start: command.capacity_start,
        capacity_step: command.capacity_step,
      } : {}),
      timeout_ms: command.timeout_ms,
      input_tokens: command.input_tokens, output_tokens: command.output_tokens,
    },
    progress: selectedRung?.progress ?? baseProgress,
    metrics: selectedRung?.metrics ?? metrics,
    samples,
    failures: [],
    ...(sloAssessment ? { slo_assessment: selectedRung?.slo_assessment ?? sloAssessment } : {}),
    ...(capacityResult ? { capacity_result: capacityResult } : {}),
    ...(fixtureHasPhaseThreeConfiguration(command) ? { request_budget: requestBudget } : {}),
    ...(command.warmup_requests > 0 ? { warmup: fixtureQuickPerformanceTraffic(command, command.warmup_requests, command.warmup_requests) } : {}),
    ...(command.ramp_duration_ms > 0 ? {
      ramp: fixtureQuickPerformanceRamp(command, requestBudget.ramp_cap),
    } : {}),
    ...(command.slice_duration_ms > 0 ? {
      time_slices: fixtureQuickPerformanceTimeSlices(command, samples, totalDurationMS),
    } : {}),
  }
}

function fixtureHasPhaseThreeConfiguration(command: QuickPerformanceCommand): boolean {
  return command.warmup_requests > 0 || command.ramp_duration_ms > 0 || command.slice_duration_ms > 0 || command.capacity_enabled
}

function fixtureQuickPerformanceBudget(command: QuickPerformanceCommand): NonNullable<QuickPerformanceReport["request_budget"]> {
  const rampIntensity = command.ramp_duration_ms / 1_000 * command.rate_per_second * 0.55
  const rampCap = command.ramp_duration_ms === 0
    ? 0
    : command.load_mode === "fixed_concurrency"
      ? command.ramp_request_cap
      : fixtureOpenLoopRequestCap(rampIntensity, command.arrival_pattern)
  const measuredCap = command.capacity_enabled
    ? command.request_count * fixtureQuickPerformanceCapacityTargets(command).length
    : command.request_count > 0
      ? command.request_count
    : command.load_mode === "fixed_concurrency"
      ? Math.max(1, 10_000 - command.warmup_requests - rampCap)
      : estimateQuickPerformanceOpenLoopRequestCap(command.duration_ms, command.rate_per_second, command.arrival_pattern)
  return {
    limit: 10_000,
    warmup_cap: command.warmup_requests,
    ramp_cap: rampCap,
    measured_cap: measuredCap,
    total_cap: command.warmup_requests + rampCap + measuredCap,
  }
}

function fixtureQuickPerformanceCapacityTargets(command: QuickPerformanceCommand): number[] {
  if (!command.capacity_enabled) return []
  const maximum = command.load_mode === "fixed_concurrency" ? command.concurrency : command.rate_per_second
  const targets: number[] = []
  for (let index = 0; index <= 20; index += 1) {
    const candidate = command.capacity_start + index * command.capacity_step
    if (candidate >= maximum) {
      targets.push(maximum)
      break
    }
    targets.push(candidate)
  }
  return targets
}

function fixtureQuickPerformanceSLOAssessment(
  command: QuickPerformanceCommand,
  samples: QuickPerformanceReport["samples"],
  totalDurationMS: number,
): NonNullable<QuickPerformanceReport["slo_assessment"]> {
  const violations = { transport: 0, ttft: 0, tpot: 0, e2e: 0 }
  let goodRequests = 0
  for (const sample of samples) {
    const transportViolation = !sample.success
    const ttftViolation = command.slo_ttft_ms > 0 && (sample.ttft_ms <= 0 || sample.ttft_ms > command.slo_ttft_ms)
    const tpotViolation = command.slo_tpot_ms > 0 && (sample.tpot_ms <= 0 || sample.tpot_ms > command.slo_tpot_ms)
    const e2eViolation = command.slo_e2e_ms > 0 && (sample.e2e_ms <= 0 || sample.e2e_ms > command.slo_e2e_ms)
    if (transportViolation) violations.transport += 1
    if (ttftViolation) violations.ttft += 1
    if (tpotViolation) violations.tpot += 1
    if (e2eViolation) violations.e2e += 1
    if (!transportViolation && !ttftViolation && !tpotViolation && !e2eViolation) goodRequests += 1
  }
  const totalRequests = samples.length
  const goodRequestPercent = totalRequests > 0 ? goodRequests / totalRequests * 100 : 0
  return {
    status: goodRequestPercent >= command.slo_target_percent ? "passed" : "failed",
    thresholds: { ttft_ms: command.slo_ttft_ms, tpot_ms: command.slo_tpot_ms, e2e_ms: command.slo_e2e_ms },
    target_percent: command.slo_target_percent,
    total_requests: totalRequests,
    good_requests: goodRequests,
    bad_requests: totalRequests - goodRequests,
    good_request_percent: goodRequestPercent,
    goodput_qps: totalDurationMS > 0 ? goodRequests / (totalDurationMS / 1_000) : 0,
    violations,
  }
}

function fixtureOpenLoopRequestCap(intensity: number, pattern: QuickPerformanceCommand["arrival_pattern"]): number {
  return pattern === "poisson" ? Math.ceil(2 * intensity) + 1 : Math.ceil(intensity)
}

function fixtureQuickPerformanceTraffic(
  command: QuickPerformanceCommand,
  requestCap: number,
  completed: number,
): NonNullable<QuickPerformanceReport["warmup"]> {
  const configuredInFlight = command.load_mode === "open_loop" ? command.max_in_flight : command.concurrency
  return {
    request_cap: requestCap,
    offered: completed,
    launched: completed,
    completed,
    succeeded: completed,
    failed: 0,
    timed_out: 0,
    rejected: 0,
    peak_in_flight: Math.min(configuredInFlight, completed),
    prompt_tokens: completed * command.input_tokens,
    completion_tokens: completed * command.output_tokens,
    cached_tokens: 0,
    send_duration_ms: 100,
    drain_duration_ms: 0,
    total_duration_ms: 100,
    failures: [],
    stopped: false,
    capped: false,
  }
}

function fixtureQuickPerformanceRamp(
  command: QuickPerformanceCommand,
  requestCap: number,
): NonNullable<QuickPerformanceReport["ramp"]> {
  return {
    shape: "linear_staircase",
    duration_ms: command.ramp_duration_ms,
    steps: command.load_mode === "fixed_concurrency" ? Math.min(command.concurrency, 10) : 10,
    ...(command.load_mode === "fixed_concurrency"
      ? { target_concurrency: command.concurrency }
      : { target_rate_per_second: command.rate_per_second }),
    completed_window: true,
    traffic: {
      ...fixtureQuickPerformanceTraffic(command, requestCap, requestCap),
      send_duration_ms: command.ramp_duration_ms,
      total_duration_ms: command.ramp_duration_ms,
    },
  }
}

function fixtureQuickPerformanceTimeSlices(
  command: QuickPerformanceCommand,
  samples: QuickPerformanceReport["samples"],
  totalDurationMS: number,
): NonNullable<QuickPerformanceReport["time_slices"]> {
  type Slice = NonNullable<QuickPerformanceReport["time_slices"]>[number]
  type Accumulator = {
    slice: Slice
    ttfb: number[]
    ttftAny: number[]
    ttftVisible: number[]
    ttst: number[]
    observedICL: number[]
    semanticChunkCount: number[]
    tpot: number[]
    e2e: number[]
  }
  const byIndex = new Map<number, Accumulator>()
  const get = (offsetMS: number) => {
    const finalOffsetMS = totalDurationMS - Math.max(Number.EPSILON, Math.abs(totalDurationMS) * Number.EPSILON)
    const boundedOffset = Math.max(0, Math.min(offsetMS, finalOffsetMS))
    const sliceIndex = Math.floor(boundedOffset / command.slice_duration_ms)
    const startMS = sliceIndex * command.slice_duration_ms
    const nominalEndMS = startMS + command.slice_duration_ms
    const endMS = Math.min(nominalEndMS, totalDurationMS)
    let accumulator = byIndex.get(sliceIndex)
    if (!accumulator) {
      const emptyLatency = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 }
      const emptyCount = { count: 0, p50: 0, p95: 0, p99: 0, average: 0 }
      accumulator = {
        slice: {
          slice_index: sliceIndex,
          start_ms: startMS,
          end_ms: endMS,
          partial: endMS < nominalEndMS,
          offered: 0,
          launched: 0,
          completed: 0,
          succeeded: 0,
          failed: 0,
          rejected: 0,
          prompt_tokens: 0,
          completion_tokens: 0,
          cached_tokens: 0,
          ttfb: emptyLatency,
          ttft_any: emptyLatency,
          ttft_visible: emptyLatency,
          ttft: emptyLatency,
          ttst: emptyLatency,
          observed_icl: emptyLatency,
          semantic_chunk_count: emptyCount,
          tpot: emptyLatency,
          e2e: emptyLatency,
        },
        ttfb: [],
        ttftAny: [],
        ttftVisible: [],
        ttst: [],
        observedICL: [],
        semanticChunkCount: [],
        tpot: [],
        e2e: [],
      }
      byIndex.set(sliceIndex, accumulator)
    }
    return accumulator
  }
  for (const sample of samples) {
    get(sample.scheduled_offset_ms).slice.offered += 1
    const launched = get(sample.started_offset_ms)
    launched.slice.launched += 1
    if (sample.success) {
      if (sample.ttfb_ms! > 0) launched.ttfb.push(sample.ttfb_ms!)
      if (sample.ttft_any_ms! > 0) launched.ttftAny.push(sample.ttft_any_ms!)
      if (sample.ttft_visible_ms! > 0) launched.ttftVisible.push(sample.ttft_visible_ms!)
      if (sample.semantic_chunk_count! >= 2) {
        if (sample.ttst_ms! > 0) launched.ttst.push(sample.ttst_ms!)
        launched.observedICL.push(sample.observed_icl_ms!)
      }
      launched.semanticChunkCount.push(sample.semantic_chunk_count!)
      if (sample.tpot_ms > 0) launched.tpot.push(sample.tpot_ms)
      if (sample.e2e_ms > 0) launched.e2e.push(sample.e2e_ms)
    }

    const completed = get(sample.finished_offset_ms)
    completed.slice.completed += 1
    if (sample.success) {
      completed.slice.succeeded += 1
      completed.slice.prompt_tokens += sample.prompt_tokens
      completed.slice.completion_tokens += sample.completion_tokens
      completed.slice.cached_tokens += sample.cached_tokens
    } else {
      completed.slice.failed += 1
      if (sample.error_code === "scheduler_overload") completed.slice.rejected += 1
    }
  }
  if (totalDurationMS % command.slice_duration_ms !== 0) get(totalDurationMS)
  return [...byIndex.values()]
    .sort((left, right) => left.slice.slice_index - right.slice.slice_index)
    .map((accumulator) => ({
      ...accumulator.slice,
      ttfb: fixturePerformanceLatencySlice(accumulator.ttfb),
      ttft_any: fixturePerformanceLatencySlice(accumulator.ttftAny),
      ttft_visible: fixturePerformanceLatencySlice(accumulator.ttftVisible),
      ttft: fixturePerformanceLatencySlice(accumulator.ttftAny),
      ttst: fixturePerformanceLatencySlice(accumulator.ttst),
      observed_icl: fixturePerformanceLatencySlice(accumulator.observedICL),
      semantic_chunk_count: fixturePerformanceCountSlice(accumulator.semanticChunkCount),
      tpot: fixturePerformanceLatencySlice(accumulator.tpot),
      e2e: fixturePerformanceLatencySlice(accumulator.e2e),
    }))
}

function fixturePerformanceLatencySlice(values: number[]): QuickPerformanceSliceLatency {
  if (values.length === 0) return { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 }
  const summary = fixturePerformanceSummary(values)
  return { count: values.length, p50_ms: summary.p50, p95_ms: summary.p95, p99_ms: summary.p99, average_ms: summary.average }
}

function fixturePerformanceCountSlice(values: number[]): QuickPerformanceSliceCount {
  if (values.length === 0) return { count: 0, p50: 0, p95: 0, p99: 0, average: 0 }
  const summary = fixturePerformanceSummary(values)
  return { count: values.length, p50: summary.p50, p95: summary.p95, p99: summary.p99, average: summary.average }
}

function fixturePerformanceSummary(values: number[]): { p50: number; p90: number; p95: number; p99: number; average: number } {
  const sorted = [...values].sort((left, right) => left - right)
  const percentile = (quantile: number) => {
    const position = (sorted.length - 1) * quantile
    const lower = Math.floor(position)
    const upper = Math.ceil(position)
    if (lower === upper) return sorted[lower]
    return sorted[lower] * (upper - position) + sorted[upper] * (position - lower)
  }
  return {
    p50: percentile(0.5),
    p90: percentile(0.9),
    p95: percentile(0.95),
    p99: percentile(0.99),
    average: sorted.reduce((sum, value) => sum + value, 0) / sorted.length,
  }
}

function fixtureQuickPerformanceProgress(command: QuickPerformanceCommand, phase: "sending" | "completed", completed: number): QuickPerformanceProgress {
  const capacityTargets = fixtureQuickPerformanceCapacityTargets(command)
  const configuredInFlight = command.load_mode === "open_loop"
    ? command.max_in_flight
    : command.capacity_enabled ? capacityTargets[0] : command.concurrency
  const planned = command.capacity_enabled ? command.request_count : fixtureQuickPerformanceBudget(command).measured_cap
  const totalDurationMS = phase === "completed" ? Math.max(320, command.duration_ms) : 0
  return {
    phase,
    planned,
    offered: phase === "completed" ? planned : Math.min(configuredInFlight, planned),
    launched: phase === "completed" ? planned : Math.min(configuredInFlight, planned),
    completed,
    in_flight: phase === "completed" ? 0 : Math.min(configuredInFlight, planned),
    peak_in_flight: Math.min(configuredInFlight, planned),
    succeeded: completed,
    failed: 0,
    rejected: 0,
    capped: false,
    send_duration_ms: totalDurationMS,
    drain_duration_ms: 0,
    total_duration_ms: totalDurationMS,
    ...(command.capacity_enabled ? {
      capacity_rung_number: 1,
      capacity_rung_count: capacityTargets.length,
      capacity_target: capacityTargets[0],
    } : {}),
  }
}

function fixtureReportDetail(summary: ReportSnapshot["reports"][number]): ReportDetail {
	return {
		schema_version: 1,
		source: "run",
		report: {
			id: summary.id, run_id: summary.run_id!, run_status: summary.run_status, generated_at: summary.generated_at,
			model: { id: "10000000-0000-4000-8000-000000000001", name: summary.model_name },
			channel: { id: "10000000-0000-4000-8000-000000000002", name: summary.channel_name },
			environment: { os: "windows", arch: "amd64", region: "local", network_egress: "direct", app_version: "fixture", engine_version: "go-core-v1" },
			conclusion: { passed: summary.passed, verdict: summary.verdict, issues: summary.issue_count ? ["fixture issue"] : [] },
			sla: {}, metrics: {}, case_results: [],
		},
		request_results: [],
	}
}

function bytesToBase64(bytes: Uint8Array): string {
	let binary = ""
	for (const byte of bytes) binary += String.fromCharCode(byte)
	return btoa(binary)
}

function parseDiagnosticsSnapshot(value: unknown): DesktopDiagnosticsSnapshot {
  if (
    !isRecord(value) ||
    value.schema_version !== 1 ||
    typeof value.available !== "boolean" ||
    value.format !== "jsonl" ||
    !isPositiveSafeInteger(value.max_file_bytes) ||
    (value.max_file_bytes as number) > 1024 * 1024 * 1024 ||
    !isNonNegativeSafeInteger(value.backup_files) ||
    (value.backup_files as number) > 100 ||
    typeof value.run_correlation !== "boolean" ||
    typeof value.request_correlation !== "boolean"
  ) {
    throw new DesktopDataError(tx("desktop:app_invalid_desktop_diagnostics"))
  }
  return {
    schema_version: 1,
    available: value.available,
    format: "jsonl",
    max_file_bytes: value.max_file_bytes as number,
    backup_files: value.backup_files as number,
    run_correlation: value.run_correlation,
    request_correlation: value.request_correlation,
  }
}

function parseVoid(value: unknown): void {
  if (value !== undefined && value !== null) {
		throw new DesktopDataError(tx("desktop:app_invalid_desktop_command_response"))
  }
}

function parseBoolean(value: unknown): boolean {
	if (typeof value !== "boolean") {
		throw new DesktopDataError(tx("desktop:app_invalid_desktop_command_response"))
	}
	return value
}

function parseSnapshot(value: unknown): WorkspaceSnapshot {
  if (!isRecord(value) || value.schema_version !== 1) {
    throw new DesktopDataError(tx("desktop:app_unsupported_desktop_data_protocol_version"))
  }
  if (!Array.isArray(value.plans) || !Array.isArray(value.runs)) {
    throw new DesktopDataError(tx("desktop:app_invalid_desktop_data_structure"))
  }
  const plans = value.plans.map(parsePlan)
  const runs = value.runs.map(parseRun)
  const planIDs = new Set(plans.map((plan) => plan.id))
  const runIDs = new Set(runs.map((run) => run.id))
  if (planIDs.size !== plans.length) throw new DesktopDataError(tx("desktop:app_invalid_desktop_test_plan_data"))
  if (
    runIDs.size !== runs.length ||
    runs.some((run) => !planIDs.has(run.plan_id))
  ) {
    throw new DesktopDataError(tx("desktop:app_invalid_desktop_run_data"))
  }
  if (
    value.active_run_id !== undefined &&
    (!isUUID(value.active_run_id) ||
      !runIDs.has(value.active_run_id))
  ) {
    throw new DesktopDataError(tx("desktop:app_invalid_active_desktop_run_reference"))
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
  reportFailure?: (error: unknown) => Promise<void>,
  operationFallbackCode?: DesktopErrorCode,
): Promise<T> {
  try {
    return parse(await invoke())
  } catch (error) {
    if (reportFailure) await reportFailure(error)
    if (isProtocolError(error)) throw error
    const normalized = normalizeBindingError(error)
    if (normalized.code === "operation_failed" && operationFallbackCode) {
      throw new DesktopClientError(operationFallbackCode)
    }
    throw normalized
  }
}

async function callCatalogMutation(
  binding: FrontendDiagnosticBinding,
  invoke: () => Promise<unknown>,
): Promise<CatalogSnapshot> {
  const payload = await callBinding(invoke, (value) => value)
  try {
    return parseCatalogSnapshot(payload)
  } catch (error) {
    await reportFrontendFailure(binding, "load_catalog")(error)
    throw new DesktopClientError("catalog_saved_refresh_failed")
  }
}

function reportFrontendFailure(
  binding: FrontendDiagnosticBinding | undefined,
  operation: FrontendDiagnostic["operation"],
): (error: unknown) => Promise<void> {
  return async (error) => {
    const diagnostic: FrontendDiagnostic = {
      operation,
      error_code: isProtocolError(error) ? "frontend_data_invalid" : "frontend_operation_failed",
      detail: frontendDiagnosticDetail(error),
    }
    try {
		if (!binding) throw new Error("frontend diagnostic binding unavailable")
      await binding.ReportFrontendDiagnostic(diagnostic)
    } catch {
		reportWailsRuntimeFailure(diagnostic)
    }
  }
}

function reportWailsRuntimeFailure(diagnostic: FrontendDiagnostic): void {
	const runtime = (window as typeof window & {
		runtime?: { LogError?: (message: string) => void }
	}).runtime
	if (typeof runtime?.LogError !== "function") return
	try {
		runtime.LogError(JSON.stringify({
			component: "frontend",
			operation: diagnostic.operation,
			error_code: diagnostic.error_code,
			detail: diagnostic.detail,
		}))
	} catch {
		// Preserve the original desktop failure if the Wails runtime is unavailable.
	}
}

function frontendDiagnosticDetail(error: unknown): string {
  const detail = error instanceof WailsBindingUnavailableError
    ? error.diagnosticDetail
    : error instanceof Error
    ? error.message.trim()
    : typeof error === "string"
      ? error.trim()
      : isRecord(error) && typeof error.code === "string"
        ? `desktop binding error: ${error.code.trim()}`
        : "unknown frontend desktop error"
  return (detail || "unknown frontend desktop error").slice(0, 2048)
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
  return error instanceof DesktopDataError
}

function isDesktopErrorCode(value: string): value is DesktopErrorCode {
  return Object.hasOwn(PUBLIC_ERROR_MESSAGES["zh-CN"], value)
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
  if (!found) throw new DesktopDataError(tx("desktop:app_run_not_found"))
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
  if (!isWorkspacePlan(value)) throw new DesktopDataError(tx("desktop:app_invalid_desktop_test_plan_data"))
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
  const hasFailure =
    isRecord(value) &&
    (value.failure_phase !== undefined || value.error_code !== undefined)
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
    (value.source === undefined || value.source === "quick_task") &&
    (value.source === "quick_task"
      ? value.planned === 0 && value.duration_ms === 0
      : (value.planned as number) > 0 || (value.duration_ms as number) > 0) &&
    isConclusion(value.conclusion) &&
    (!hasFailure ||
      (value.status === "failed" &&
        isStableErrorCode(value.failure_phase) &&
        isStableErrorCode(value.error_code))) &&
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
  if (!isWorkspaceRun(value)) throw new DesktopDataError(tx("desktop:app_invalid_desktop_run_data"))
  const record = value as Record<string, unknown>
  return {
    ...(record.source === "quick_task" ? { source: "quick_task" as const } : {}),
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
    ...(record.failure_phase === undefined
      ? {}
      : {
          failure_phase: record.failure_phase as string,
          error_code: record.error_code as string,
        }),
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

function isStableErrorCode(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length <= 64 &&
    /^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$/.test(value)
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
