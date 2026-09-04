import type { StartRunTargetCommand, WorkspaceSnapshot } from "@/features/runs/data"
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
  parseQuickPerformanceProgress,
  parseQuickPerformanceReport,
  parseQuickTestResult,
  type QuickPerformanceCommand,
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickTestCommand,
  type QuickTestResult,
  type SaveQuickTestConnectionCommand,
} from "@/features/quick-test/data"

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
  | "plan_protocol_mismatch"
  | "catalog_invalid"
  | "catalog_revision_conflict"
  | "catalog_not_found"

const PUBLIC_ERROR_MESSAGES: Record<DesktopErrorCode, string> = {
  desktop_not_started: "桌面服务仍在启动，请稍候重试；日志操作名：startup",
  desktop_startup_failed: "桌面服务初始化失败，请查看日志中的 startup 记录",
  desktop_stopped: "桌面应用已停止",
  workspace_unavailable: "无法读取运行工作区，请重试；若仍失败，请查看日志中的 load_workspace 记录",
  catalog_unavailable: "无法读取模型、渠道与用例目录，请重试；日志操作名：load_catalog",
  reports_unavailable: "无法读取测试报告，请重试；日志操作名：load_reports",
  run_commands_unavailable: "运行命令暂不可用",
  comparison_unavailable: "无法读取渠道对比，请重试；日志操作名：load_comparisons",
  diagnostics_unavailable: "诊断日志暂不可用",
  quick_test_unavailable: "快速测试暂不可用",
  quick_test_save_partial: "连接已部分保存，请前往模型与渠道检查并完成配置",
  invalid_identifier: "操作对象无效",
  operation_cancelled: "操作已取消",
  operation_failed: "桌面操作失败，请检查本地日志",
  plan_protocol_mismatch: "计划中的用例、模型和渠道协议不一致，请选择与用例协议一致的模型和渠道，或调整用例/套件",
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

export function publicDesktopOperationErrorMessage(
  error: unknown,
  operation: string,
  fallback: string,
): string {
  return `${operation}失败：${publicDesktopErrorMessage(error, fallback)}`
}

export interface DesktopClient extends CatalogActions {
  getDiagnostics(): Promise<DesktopDiagnosticsSnapshot>
  openDiagnosticsDirectory(): Promise<void>
  getWorkspace(): Promise<WorkspaceSnapshot>
  getCatalog(): Promise<CatalogSnapshot>
  getReports(): Promise<ReportSnapshot>
  getReportDetail(reportId: string): Promise<ReportDetail>
  exportReport(reportId: string, format: ReportExportFormat, watermark: string): Promise<ExportedReport>
  saveReportExport(filename: string, mediaType: string, dataBase64: string): Promise<boolean>
  copyReportPNG(dataBase64: string): Promise<void>
  getComparisons(): Promise<ComparisonSnapshot>
  startRun(planId: string): Promise<WorkspaceSnapshot>
  startRunTarget(command: StartRunTargetCommand): Promise<WorkspaceSnapshot>
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
  ExportReport(reportId: string, format: ReportExportFormat, watermark: string): Promise<unknown>
  SaveReportExport(filename: string, mediaType: string, dataBase64: string): Promise<unknown>
  CopyReportPNG(dataBase64: string): Promise<unknown>
  GetComparisons(): Promise<unknown>
  StartRun(planId: string): Promise<unknown>
  StartRunTarget(command: StartRunTargetCommand): Promise<unknown>
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
  "SaveReportExport", "CopyReportPNG", "GetComparisons", "StartRun", "StartRunTarget",
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
		exportReport: async (reportId, format, watermark) => (await client).exportReport(reportId, format, watermark),
		saveReportExport: async (filename, mediaType, dataBase64) => (await client).saveReportExport(filename, mediaType, dataBase64),
		copyReportPNG: async (dataBase64) => (await client).copyReportPNG(dataBase64),
		getComparisons: async () => (await client).getComparisons(),
    startRun: async (planId) => (await client).startRun(planId),
		startRunTarget: async (command) => (await client).startRunTarget(command),
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
		exportReport: async (reportId, format, watermark) =>
			callBinding(() => binding.ExportReport(reportId, format, watermark), parseExportedReport),
		saveReportExport: async (filename, mediaType, dataBase64) =>
			callBinding(() => binding.SaveReportExport(filename, mediaType, dataBase64), parseBoolean),
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
    stopSending: async (runId) =>
      callBinding(() => binding.StopSending(runId), parseSnapshot),
    cancelRun: async (runId) =>
      callBinding(() => binding.CancelRun(runId), parseSnapshot),
		startComparison: async (command) =>
			callBinding(() => binding.StartComparison(command), parseComparisonSnapshot),
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
      callBinding(() => binding.SaveQuickTestConnection(command), parseCatalogSnapshot),
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
    ttft_ms: 35,
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
  return {
    schema_version: 2,
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
      timeout_ms: command.timeout_ms,
      input_tokens: command.input_tokens, output_tokens: command.output_tokens,
    },
    progress: {
      phase: "completed", planned: requestBudget.measured_cap, offered: completed, launched: completed, completed,
      in_flight: 0,
      peak_in_flight: Math.min(configuredInFlight, completed), succeeded: completed,
      failed: 0, rejected: 0, capped: false, send_duration_ms: totalDurationMS,
      drain_duration_ms: 0, total_duration_ms: totalDurationMS,
    },
    metrics: {
      completed, succeeded: completed, failed: 0, timed_out: 0,
      success_rate_percent: 100, offered_qps: completed / seconds,
      launched_qps: completed / seconds, completed_qps: completed / seconds,
      successful_request_qps: completed / seconds, request_qps: completed / seconds,
      rpm: completed / seconds * 60, input_tpm: promptTokens / seconds * 60,
      output_tpm: completionTokens / seconds * 60,
      total_tpm: (promptTokens + completionTokens) / seconds * 60,
      generation_tps: completionTokens / seconds,
      ttft_p50_ms: 35, ttft_p90_ms: 45, ttft_p95_ms: 48, ttft_p99_ms: 50, ttft_average_ms: 38,
      tpot_p50_ms: 5, tpot_p90_ms: 6, tpot_p95_ms: 6.5, tpot_p99_ms: 7, tpot_average_ms: 5.2,
      e2e_p50_ms: 120, e2e_p90_ms: 150, e2e_p95_ms: 160, e2e_p99_ms: 170, e2e_average_ms: 128,
      schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 1.8, schedule_lag_p95_ms: 2,
      schedule_lag_p99_ms: 2.8, schedule_lag_average_ms: 0.5,
      prompt_tokens: promptTokens, completion_tokens: completionTokens,
      cached_tokens: 0, cache_rate_percent: 0,
    },
    samples,
    failures: [],
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
  return command.warmup_requests > 0 || command.ramp_duration_ms > 0 || command.slice_duration_ms > 0
}

function fixtureQuickPerformanceBudget(command: QuickPerformanceCommand): NonNullable<QuickPerformanceReport["request_budget"]> {
  const rampIntensity = command.ramp_duration_ms / 1_000 * command.rate_per_second * 0.55
  const rampCap = command.ramp_duration_ms === 0
    ? 0
    : command.load_mode === "fixed_concurrency"
      ? command.ramp_request_cap
      : fixtureOpenLoopRequestCap(rampIntensity, command.arrival_pattern)
  const measuredCap = command.request_count > 0
    ? command.request_count
    : command.load_mode === "fixed_concurrency"
      ? Math.max(1, 10_000 - command.warmup_requests - rampCap)
      : fixtureOpenLoopRequestCap(command.duration_ms / 1_000 * command.rate_per_second, command.arrival_pattern)
  return {
    limit: 10_000,
    warmup_cap: command.warmup_requests,
    ramp_cap: rampCap,
    measured_cap: measuredCap,
    total_cap: command.warmup_requests + rampCap + measuredCap,
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
  type Accumulator = { slice: Slice; ttft: number[]; tpot: number[]; e2e: number[] }
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
      const emptyLatency = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 }
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
          ttft: emptyLatency,
          tpot: emptyLatency,
          e2e: emptyLatency,
        },
        ttft: [],
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
    if (sample.success && sample.ttft_ms > 0) launched.ttft.push(sample.ttft_ms)
    if (sample.success && sample.tpot_ms > 0) launched.tpot.push(sample.tpot_ms)
    if (sample.success && sample.e2e_ms > 0) launched.e2e.push(sample.e2e_ms)

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
      ttft: fixturePerformanceLatencySlice(accumulator.ttft),
      tpot: fixturePerformanceLatencySlice(accumulator.tpot),
      e2e: fixturePerformanceLatencySlice(accumulator.e2e),
    }))
}

function fixturePerformanceLatencySlice(values: number[]): NonNullable<QuickPerformanceReport["time_slices"]>[number]["ttft"] {
  if (values.length === 0) return { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 }
  const sorted = [...values].sort((left, right) => left - right)
  const percentile = (quantile: number) => {
    const position = (sorted.length - 1) * quantile
    const lower = Math.floor(position)
    const upper = Math.ceil(position)
    if (lower === upper) return sorted[lower]
    return sorted[lower] * (upper - position) + sorted[upper] * (position - lower)
  }
  return { count: sorted.length, p50_ms: percentile(0.5), p95_ms: percentile(0.95), p99_ms: percentile(0.99) }
}

function fixtureQuickPerformanceProgress(command: QuickPerformanceCommand, phase: "sending" | "completed", completed: number): QuickPerformanceProgress {
  const configuredInFlight = command.load_mode === "open_loop" ? command.max_in_flight : command.concurrency
  const planned = fixtureQuickPerformanceBudget(command).measured_cap
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
    throw new Error("桌面诊断数据无效")
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
		throw new Error("桌面命令响应无效")
  }
}

function parseBoolean(value: unknown): boolean {
	if (typeof value !== "boolean") {
		throw new Error("桌面命令响应无效")
	}
	return value
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
  return (
    error instanceof Error &&
    (error.message.startsWith("桌面数据") ||
      error.message.startsWith("桌面测试计划数据") ||
      error.message.startsWith("桌面运行记录数据") ||
      error.message.startsWith("桌面活动运行引用") ||
      error.message.startsWith("桌面目录") ||
      error.message.startsWith("桌面报告") ||
		error.message.startsWith("渠道对比") ||
		error.message.startsWith("桌面诊断") ||
		error.message.startsWith("快速测试") ||
		error.message.startsWith("快速性能"))
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
    ((value.planned as number) > 0 || (value.duration_ms as number) > 0) &&
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
