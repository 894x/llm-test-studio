import { DesktopDataError } from "@/app/data-error"
import { EMPTY_REMOTE_NODES, parseRemoteNodes, parseRemoteNodeUpgrade, parseNullableRemoteNodeUpgrade,
  type StartRemoteNodeUpgradeCommand, type RemoteNodeUpgradeAction, type RemoteNodeUpgradeView, type RemoteNodesSnapshot, type RemoteNodeDraft,
  type RemoteNodeAction, type HostKeyConfirmation, type DefaultExecutionCommand } from "@/features/remote-nodes/data"
import { parseQuickTaskDetail, type QuickTaskDetail, type RememberQuickTaskCredentialCommand } from "@/features/quick-test/task-draft"
import { translateDesktop as tx } from "@/i18n/runtime"
import { type WorkspaceSnapshot } from "@/features/runs/data"
import {
  EMPTY_COMPARISONS,
  parseComparisonSnapshot,
  type ComparisonSnapshot,
} from "@/features/comparisons/data"
import {
  EMPTY_CATALOG,
  parseCatalogSnapshot,
  type CatalogActions,
  type CatalogSnapshot,
  type CreateChannelCommand,
  type SaveQuickTestTargetCommand,
  type CreateChannelModelCommand,
  type CreateModelCommand,
  type CreatePlanCommand,
  type CreateSuiteCommand,
  type CreateTestCaseCommand,
  type DeleteCommand,
  type FileMediaType,
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
import { parseJobView, parseJobPage, parsePerformanceResult, parseControlView, parseControlPage,
  type LaunchCommand, type JobView, type JobPage, type PerformanceResultView,
  type ControlInput, type ControlView, type ControlPageView } from "@/features/jobs/data"
import enCommon from "@/i18n/resources/en-US/common.json"
import zhCommon from "@/i18n/resources/zh-CN/common.json"
import {
  parseReportGenerationProgress, parseReportGenerationSnapshot,
  type ReportGenerationProgress, type ReportGenerationSnapshot,
} from "@/features/reports/generation-progress"

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
  | "invalid_identifier"
  | "operation_cancelled"
  | "operation_failed"
  | "run_invalid"
  | "run_not_runnable"
  | "run_not_active"
  | "quick_task_credential_unavailable"
  | "plan_protocol_mismatch"
  | "catalog_invalid"
  | "catalog_revision_conflict"
  | "catalog_not_found"
  | "catalog_saved_refresh_failed"
  | "comparison_invalid"
  | "comparison_not_ready"
  | "comparison_target_unmapped"
  | "comparison_channel_not_ready"
  | "remote_nodes_unavailable"
  | "remote_node_invalid"
  | "remote_node_revision_conflict"
  | "remote_node_not_found"
  | "remote_node_re_pair_required"
  | "remote_node_still_referenced"
  | "remote_node_usage_unavailable"
  | "remote_node_credential_unavailable"
  | "remote_node_busy"
  | "remote_node_commit_unconfirmed"
  | "remote_node_upgrade_required"
  | "remote_node_identity_conflict"
  | "job_invalid"
  | "job_not_found"
  | "job_conflict"
  | "job_unavailable"
  | "job_confirmation_pending"
  | "job_commit_unconfirmed"
  | "job_query_unavailable"
  | "job_commands_unavailable"
  | "file_input_invalid"
  | "file_input_too_large"
  | "file_input_limit"
  | "file_input_busy"
  | "file_input_not_found"
  | "file_input_unavailable"

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
  createLocalFileSelection(command: CreateLocalFileSelectionCommand): Promise<LocalFileSelection>
  releaseLocalFileSelection(selectionID: string): Promise<void>
  getRemoteNodes(): Promise<RemoteNodesSnapshot>
  saveRemoteNode(command: RemoteNodeDraft): Promise<void>
  refreshRemoteNode(command: RemoteNodeAction): Promise<void>
  confirmRemoteNodeHostKey(command: HostKeyConfirmation): Promise<void>
  selectDefaultExecution(command: DefaultExecutionCommand): Promise<void>
  retryRemoteInstallation(command: RemoteNodeAction): Promise<void>
  removeRemoteNode(command: RemoteNodeAction): Promise<void>
  startRemoteNodeUpgrade(command: StartRemoteNodeUpgradeCommand): Promise<RemoteNodeUpgradeView>
  getRemoteNodeUpgrade(id: string): Promise<RemoteNodeUpgradeView | null>
  retryRemoteNodeUpgrade(command: RemoteNodeUpgradeAction): Promise<RemoteNodeUpgradeView>
  recoverRemoteNodeUpgrade(command: RemoteNodeUpgradeAction): Promise<RemoteNodeUpgradeView>
  getDiagnostics(): Promise<DesktopDiagnosticsSnapshot>
  openDiagnosticsDirectory(): Promise<void>
  getWorkspace(): Promise<WorkspaceSnapshot>
  getCatalog(): Promise<CatalogSnapshot>
  getReports(): Promise<ReportSnapshot>
  getReportGeneration(): Promise<ReportGenerationSnapshot>
  subscribeReportGeneration(onProgress: (progress: ReportGenerationProgress) => void): () => void
  getReportDetail(reportId: string): Promise<ReportDetail>
  exportReport(reportId: string, format: ReportExportFormat, watermark: string, locale: string): Promise<ExportedReport>
  saveReportExport(filename: string, mediaType: string, dataBase64: string, locale: string): Promise<boolean>
  copyReportPNG(dataBase64: string): Promise<void>
  getComparisons(): Promise<ComparisonSnapshot>
  launchJob(command: LaunchCommand): Promise<JobView>
  getJobs(after: string, limit: number): Promise<JobPage>
  getJob(jobID: string): Promise<JobView>
  getJobPerformanceResult(jobID: string): Promise<PerformanceResultView>
  controlJob(command: ControlInput): Promise<ControlView>
  getJobControls(jobID: string, after: string, limit: number): Promise<ControlPageView>
  retryJobControl(commandID: string): Promise<ControlView>
  getQuickTask(runId: string): Promise<QuickTaskDetail>
  rememberQuickTaskCredential(command: RememberQuickTaskCredentialCommand): Promise<void>
  forgetQuickTaskCredential(runId: string): Promise<void>
}

export const MAX_LOCAL_FILE_BYTES = 1048576
const MAX_LOCAL_FILE_BASE64_LENGTH = 1398104
const MAX_LOCAL_FILE_DOCUMENT_BYTES = 1400000
export type CreateLocalFileSelectionCommand = { data_base64: string; media_type: FileMediaType }
export type LocalFileSelection = { selection_id: string; sha256: string; size: number; media_type: FileMediaType }

function localFileSelectionDocument(command: CreateLocalFileSelectionCommand): string {
  if (!isRecord(command) || Object.keys(command).length !== 2 ||
    (command.media_type !== "image/png" && command.media_type !== "image/jpeg") ||
    typeof command.data_base64 !== "string" || !command.data_base64.length ||
    command.data_base64.length % 4 !== 0) throw new DesktopClientError("file_input_invalid")
  if (command.data_base64.length > MAX_LOCAL_FILE_BASE64_LENGTH) throw new DesktopClientError("file_input_too_large")
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(command.data_base64)) throw new DesktopClientError("file_input_invalid")
  const size = command.data_base64.length / 4 * 3 - (command.data_base64.endsWith("==") ? 2 : command.data_base64.endsWith("=") ? 1 : 0)
  if (size > MAX_LOCAL_FILE_BYTES) throw new DesktopClientError("file_input_too_large")
  const document = JSON.stringify(command)
  if (new TextEncoder().encode(document).byteLength > MAX_LOCAL_FILE_DOCUMENT_BYTES) throw new DesktopClientError("file_input_too_large")
  return document
}

function parseLocalFileSelection(value: unknown): LocalFileSelection {
  if (!isRecord(value) || Object.keys(value).length !== 4 || !isUUID(value.selection_id) ||
    typeof value.sha256 !== "string" || !/^[a-f0-9]{64}$/.test(value.sha256) ||
    !Number.isSafeInteger(value.size) || (value.size as number) < 1 || (value.size as number) > MAX_LOCAL_FILE_BYTES ||
    (value.media_type !== "image/png" && value.media_type !== "image/jpeg")) throw new DesktopDataError("Invalid local file selection")
  return { selection_id: value.selection_id, sha256: value.sha256, size: value.size as number, media_type: value.media_type }
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
  CreateLocalFileSelection(document: string): Promise<unknown>
  ReleaseLocalFileSelection(selectionID: string): Promise<unknown>
  GetRemoteNodes(): Promise<unknown>
  StartRemoteNodeUpgrade(command: StartRemoteNodeUpgradeCommand): Promise<unknown>
  GetRemoteNodeUpgrade(id: string): Promise<unknown>
  RetryRemoteNodeUpgrade(command: RemoteNodeUpgradeAction): Promise<unknown>
  RecoverRemoteNodeUpgrade(command: RemoteNodeUpgradeAction): Promise<unknown>
  SaveRemoteNode(command: RemoteNodeDraft): Promise<unknown>
  RefreshRemoteNode(command: RemoteNodeAction): Promise<unknown>
  ConfirmRemoteNodeHostKey(command: HostKeyConfirmation): Promise<unknown>
  SelectDefaultExecution(command: DefaultExecutionCommand): Promise<unknown>
  RetryRemoteInstallation(command: RemoteNodeAction): Promise<unknown>
  RemoveRemoteNode(command: RemoteNodeAction): Promise<unknown>
  GetDiagnostics(): Promise<unknown>
  OpenDiagnosticsDirectory(): Promise<unknown>
  ReportFrontendDiagnostic(diagnostic: FrontendDiagnostic): Promise<unknown>
  GetWorkspace(): Promise<unknown>
  GetCatalog(): Promise<unknown>
  GetReports(): Promise<unknown>
  GetReportGeneration(): Promise<unknown>
  GetReportDetail(reportId: string): Promise<unknown>
  ExportReport(reportId: string, format: ReportExportFormat, watermark: string, locale: string): Promise<unknown>
  SaveReportExport(filename: string, mediaType: string, dataBase64: string, locale: string): Promise<unknown>
  CopyReportPNG(dataBase64: string): Promise<unknown>
  GetComparisons(): Promise<unknown>
  LaunchJob(command: LaunchCommand): Promise<unknown>
  GetJobs(after: string, limit: number): Promise<unknown>
  GetJob(jobID: string): Promise<unknown>
  GetJobPerformanceResult(jobID: string): Promise<unknown>
  ControlJob(command: ControlInput): Promise<unknown>
  GetJobControls(jobID: string, after: string, limit: number): Promise<unknown>
  RetryJobControl(commandID: string): Promise<unknown>
  GetQuickTask(runId: string): Promise<unknown>
  RememberQuickTaskCredential(command: RememberQuickTaskCredentialCommand): Promise<unknown>
  ForgetQuickTaskCredential(runId: string): Promise<unknown>
  CreateModel(command: CreateModelCommand): Promise<unknown>
  UpdateModel(command: UpdateModelCommand): Promise<unknown>
  DeleteModel(command: DeleteCommand): Promise<unknown>
  SaveQuickTestTarget(command: SaveQuickTestTargetCommand): Promise<unknown>
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
  "CreateLocalFileSelection", "ReleaseLocalFileSelection",
  "GetRemoteNodes", "SaveRemoteNode", "RefreshRemoteNode", "ConfirmRemoteNodeHostKey", "SelectDefaultExecution",
  "RetryRemoteInstallation", "RemoveRemoteNode",
  "StartRemoteNodeUpgrade", "GetRemoteNodeUpgrade", "RetryRemoteNodeUpgrade", "RecoverRemoteNodeUpgrade",
  "GetDiagnostics", "OpenDiagnosticsDirectory", "ReportFrontendDiagnostic",
  "GetWorkspace", "GetCatalog", "GetReports", "GetReportGeneration", "GetReportDetail", "ExportReport",
  "SaveReportExport", "CopyReportPNG", "GetComparisons", "LaunchJob", "GetJobs", "GetJob", "GetJobPerformanceResult", "ControlJob", "GetJobControls", "RetryJobControl", "GetQuickTask", "RememberQuickTaskCredential", "ForgetQuickTaskCredential",
  "CreateModel", "UpdateModel", "DeleteModel", "CreateChannel", "SaveQuickTestTarget",
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
  const workspace = cloneSnapshot(initial)
  let catalogState = structuredClone(catalog)
	const comparisonState = structuredClone(comparisons)
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
    async createLocalFileSelection() { throw new DesktopClientError("file_input_unavailable") },
    async releaseLocalFileSelection() { throw new DesktopClientError("file_input_unavailable") },
    async getDiagnostics() {
      return fixtureDiagnosticsSnapshot()
    },
    async getRemoteNodes() { return structuredClone(EMPTY_REMOTE_NODES) },
    async saveRemoteNode() { throw new DesktopClientError("remote_nodes_unavailable") },
    async refreshRemoteNode() { throw new DesktopClientError("remote_nodes_unavailable") },
    async confirmRemoteNodeHostKey() { throw new DesktopClientError("remote_nodes_unavailable") },
    async selectDefaultExecution() { throw new DesktopClientError("remote_nodes_unavailable") },
    async retryRemoteInstallation() { throw new DesktopClientError("remote_nodes_unavailable") },
    async removeRemoteNode() { throw new DesktopClientError("remote_nodes_unavailable") },
    async startRemoteNodeUpgrade() { throw new DesktopClientError("remote_nodes_unavailable") },
    async getRemoteNodeUpgrade() { return null },
    async retryRemoteNodeUpgrade() { throw new DesktopClientError("remote_nodes_unavailable") },
    async recoverRemoteNodeUpgrade() { throw new DesktopClientError("remote_nodes_unavailable") },
    async openDiagnosticsDirectory() {},
    async getReportGeneration() { return { schema_version: 1, runs: [] } },
    subscribeReportGeneration() { return () => {} },
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

    async launchJob() { throw new DesktopClientError("job_commands_unavailable") },
    async getJobs() { return { schema_version: 1, jobs: [], next_cursor: "", has_more: false } },
    async getJob() { throw new DesktopClientError("job_not_found") },
    async getJobPerformanceResult() { throw new DesktopClientError("job_not_found") },
    async controlJob() { throw new DesktopClientError("job_commands_unavailable") },
    async getJobControls(jobID) { return { schema_version: 1, job_id: jobID, commands: [], next_cursor: "", has_more: false } },
    async retryJobControl() { throw new DesktopClientError("job_commands_unavailable") },
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
    async createModel(command) {
      catalogState.models.push({ id: nextID(), revision: 1, ...structuredClone(command) })
      return structuredClone(catalogState)
    },
    async updateModel(command) {
      catalogState.models = replaceByID(catalogState.models, command.id, {
        id: command.id,
        revision: command.expected_revision + 1,
        name: command.name,
        protocols: [...command.protocols],
        capabilities: [...command.capabilities],
      })
      return structuredClone(catalogState)
    },
    async deleteModel(command) {
      catalogState.models = removeByID(catalogState.models, command.id)
      return structuredClone(catalogState)
    },
    async saveQuickTestTarget(command) {
      const matches = catalogState.models.filter(model => model.name === command.model_name)
      if (matches.length > 1) throw new DesktopClientError("catalog_revision_conflict")
      let model = matches[0]
      if (!model) {
        model = { id: nextID(), revision: 1, name: command.model_name, protocols: [command.protocol], capabilities: [] }
        catalogState.models.push(model)
      } else if (!model.protocols.includes(command.protocol)) {
        model.protocols.push(command.protocol)
        model.revision += 1
      }
      const channelID = nextID()
      catalogState.channels.push({ id: channelID, revision: 1, name: command.name, base_url: command.base_url, enabled: true, credential_configured: true, model_count: 1 })
      catalogState.channel_models.push({ id: nextID(), revision: 1, channel_id: channelID, model_id: model.id, upstream_model_name: command.model_name, protocols: [command.protocol] })
      return { catalog: structuredClone(catalogState), channel_id: channelID }
    },
    async createChannel(command) {
      catalogState.channels.push({ id: nextID(), revision: 1, name: command.name, base_url: command.base_url, enabled: command.enabled, credential_configured: true, model_count: 0 })
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
        protocols: [...command.protocols],
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
      catalogState.plans.push(planFromCommand(nextID(), 1, command, catalogState))
      return structuredClone(catalogState)
    },
    async updatePlan(command) {
      catalogState.plans = replaceByID(catalogState.plans, command.id, planFromCommand(command.id, command.expected_revision + 1, command, catalogState))
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
    createLocalFileSelection: async command => (await client).createLocalFileSelection(command),
    releaseLocalFileSelection: async id => (await client).releaseLocalFileSelection(id),
    getDiagnostics: async () => (await client).getDiagnostics(),
    getRemoteNodes: async () => (await client).getRemoteNodes(),
    saveRemoteNode: async command => (await client).saveRemoteNode(command),
    refreshRemoteNode: async command => (await client).refreshRemoteNode(command),
    confirmRemoteNodeHostKey: async command => (await client).confirmRemoteNodeHostKey(command),
    selectDefaultExecution: async command => (await client).selectDefaultExecution(command),
    retryRemoteInstallation: async command => (await client).retryRemoteInstallation(command),
    removeRemoteNode: async command => (await client).removeRemoteNode(command),
    startRemoteNodeUpgrade: async command => (await client).startRemoteNodeUpgrade(command),
    getRemoteNodeUpgrade: async id => (await client).getRemoteNodeUpgrade(id),
    retryRemoteNodeUpgrade: async command => (await client).retryRemoteNodeUpgrade(command),
    recoverRemoteNodeUpgrade: async command => (await client).recoverRemoteNodeUpgrade(command),
    openDiagnosticsDirectory: async () =>
      (await client).openDiagnosticsDirectory(),
    getWorkspace: async () => (await client).getWorkspace(),
    getCatalog: async () => (await client).getCatalog(),
    getReports: async () => (await client).getReports(),
    getReportGeneration: async () => (await client).getReportGeneration(),
    subscribeReportGeneration: (onProgress) => {
      let active = true
      let unsubscribe = () => {}
      void client.then((ready) => {
        if (active) unsubscribe = ready.subscribeReportGeneration(onProgress)
      })
      return () => { active = false; unsubscribe() }
    },
		getReportDetail: async (reportId) => (await client).getReportDetail(reportId),
		exportReport: async (reportId, format, watermark, locale) => (await client).exportReport(reportId, format, watermark, locale),
		saveReportExport: async (filename, mediaType, dataBase64, locale) => (await client).saveReportExport(filename, mediaType, dataBase64, locale),
		copyReportPNG: async (dataBase64) => (await client).copyReportPNG(dataBase64),
		getComparisons: async () => (await client).getComparisons(),
    launchJob: async command => (await client).launchJob(command),
    getJobs: async (after, limit) => (await client).getJobs(after, limit),
    getJob: async jobID => (await client).getJob(jobID),
    getJobPerformanceResult: async jobID => (await client).getJobPerformanceResult(jobID),
    controlJob: async command => (await client).controlJob(command),
    getJobControls: async (jobID, after, limit) => (await client).getJobControls(jobID, after, limit),
    retryJobControl: async commandID => (await client).retryJobControl(commandID),
		getQuickTask: async (runId) => (await client).getQuickTask(runId),
    rememberQuickTaskCredential: async (command) => (await client).rememberQuickTaskCredential(command),
    forgetQuickTaskCredential: async (runId) => (await client).forgetQuickTaskCredential(runId),
    createModel: async (command) => (await client).createModel(command),
    updateModel: async (command) => (await client).updateModel(command),
    deleteModel: async (command) => (await client).deleteModel(command),
    saveQuickTestTarget: async (command) => (await client).saveQuickTestTarget(command),
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

export function singleFlightRead<T>(read: () => Promise<T>): (() => Promise<T>) & { invalidate(): void } {
  let pending: Promise<T> | undefined
  let queued: Promise<T> | undefined
  let invalidated = false
  const get = (): Promise<T> => {
    if (pending && invalidated) {
      queued ??= pending.then(() => undefined, () => undefined).then(() => {
        queued = undefined
        return get()
      })
      return queued
    }
    if (!pending) invalidated = false
    pending ??= Promise.resolve(read()).finally(() => { pending = undefined })
    return pending
  }
  get.invalidate = () => { if (pending) invalidated = true }
  return get
}

function wailsClient(binding: WailsDesktopBinding): DesktopClient {
  return {
    createLocalFileSelection: async command => {
      const document = localFileSelectionDocument(command)
      return callBinding(() => binding.CreateLocalFileSelection(document), value => {
        const selection = parseLocalFileSelection(value)
        if (selection.media_type !== command.media_type) throw new DesktopDataError("Invalid local file selection media type")
        return selection
      })
    },
    releaseLocalFileSelection: async id => {
      if (!isUUID(id)) throw new DesktopClientError("file_input_invalid")
      return callBinding(() => binding.ReleaseLocalFileSelection(id), parseVoid)
    },
    getRemoteNodes: singleFlightRead(() => callBinding(() => binding.GetRemoteNodes(), parseRemoteNodes)),
    saveRemoteNode: async command => callBinding(() => binding.SaveRemoteNode(command), parseVoid),
    refreshRemoteNode: async command => callBinding(() => binding.RefreshRemoteNode(command), parseVoid),
    confirmRemoteNodeHostKey: async command => callBinding(() => binding.ConfirmRemoteNodeHostKey(command), parseVoid),
    selectDefaultExecution: async command => callBinding(() => binding.SelectDefaultExecution(command), parseVoid),
    retryRemoteInstallation: async command => callBinding(() => binding.RetryRemoteInstallation(command), parseVoid),
    removeRemoteNode: async command => callBinding(() => binding.RemoveRemoteNode(command), parseVoid),
    startRemoteNodeUpgrade: async command => callBinding(() => binding.StartRemoteNodeUpgrade(command), value => {
      const view = parseRemoteNodeUpgrade(value)
      if (view.configuration_id !== command.id || view.operation_id !== command.operation_id || view.expected_build_id !== command.expected_build_id) throw new DesktopDataError("Remote upgrade identity mismatch")
      return view
    }),
    getRemoteNodeUpgrade: async id => callBinding(() => binding.GetRemoteNodeUpgrade(id), value => {
      const view = parseNullableRemoteNodeUpgrade(value)
      if (view && view.configuration_id !== id) throw new DesktopDataError("Remote upgrade identity mismatch")
      return view
    }),
    retryRemoteNodeUpgrade: async command => callBinding(() => binding.RetryRemoteNodeUpgrade(command), value => {
      const view = parseRemoteNodeUpgrade(value)
      if (view.configuration_id !== command.id || view.operation_id !== command.operation_id) throw new DesktopDataError("Remote upgrade identity mismatch")
      return view
    }),
    recoverRemoteNodeUpgrade: async command => callBinding(() => binding.RecoverRemoteNodeUpgrade(command), value => {
      const view = parseRemoteNodeUpgrade(value)
      if (view.configuration_id !== command.id || view.operation_id !== command.operation_id) throw new DesktopDataError("Remote upgrade identity mismatch")
      return view
    }),
    getReportGeneration: async () => callBinding(() => binding.GetReportGeneration(), parseReportGenerationSnapshot),
    subscribeReportGeneration: (onProgress) => {
      const runtime = (window as typeof window & {
        runtime?: { EventsOn?: (name: string, callback: (payload: unknown) => void) => () => void }
      }).runtime
      return runtime?.EventsOn?.("report-generation-progress", (payload) => {
        try { onProgress(parseReportGenerationProgress(payload)) }
        catch { /* Reject malformed progress at the boundary. */ }
      }) ?? (() => {})
    },
    getDiagnostics: async () =>
      callBinding(() => binding.GetDiagnostics(), parseDiagnosticsSnapshot),
    openDiagnosticsDirectory: async () =>
      callBinding(() => binding.OpenDiagnosticsDirectory(), parseVoid),
    getWorkspace: singleFlightRead(() =>
      callBinding(
        () => binding.GetWorkspace(),
        parseSnapshot,
        reportFrontendFailure(binding, "load_workspace"),
        "workspace_unavailable",
      )),
    getCatalog: singleFlightRead(() =>
      callBinding(
        () => binding.GetCatalog(),
        parseCatalogSnapshot,
        reportFrontendFailure(binding, "load_catalog"),
        "catalog_unavailable",
      )),
    getReports: singleFlightRead(() =>
      callBinding(
        () => binding.GetReports(),
        parseReportSnapshot,
        reportFrontendFailure(binding, "load_reports"),
        "reports_unavailable",
      )),
		getReportDetail: async (reportId) =>
			callBinding(() => binding.GetReportDetail(reportId), parseReportDetail),
		exportReport: async (reportId, format, watermark, locale) =>
			callBinding(() => binding.ExportReport(reportId, format, watermark, locale), parseExportedReport),
		saveReportExport: async (filename, mediaType, dataBase64, locale) =>
			callBinding(() => binding.SaveReportExport(filename, mediaType, dataBase64, locale), parseBoolean),
		copyReportPNG: async (dataBase64) =>
			callBinding(() => binding.CopyReportPNG(dataBase64), parseVoid),
		getComparisons: singleFlightRead(() =>
			callBinding(
				() => binding.GetComparisons(),
				parseComparisonSnapshot,
				reportFrontendFailure(binding, "load_comparisons"),
				"comparison_unavailable",
			)),
    launchJob: async command => callBinding(() => binding.LaunchJob(command), value => {
      const job = parseJobView(value)
      const kind = command.kind === "plan" || command.kind === "quick_task" ? "run" : command.kind
      if (!job.submission || job.kind !== kind || job.location.kind !== command.location.kind || job.location.configuration_id !== command.location.configuration_id) throw new DesktopDataError("Invalid launch identity")
      return job
    }),
    getJobs: async (after, limit) => callBinding(() => binding.GetJobs(after, limit), parseJobPage),
    getJob: async jobID => callBinding(() => binding.GetJob(jobID), value => {
      const job = parseJobView(value)
      if (job.job_id !== jobID) throw new DesktopDataError("Invalid job identity")
      return job
    }),
    getJobPerformanceResult: async jobID => callBinding(() => binding.GetJobPerformanceResult(jobID), value => {
      const result = parsePerformanceResult(value)
      if (result.job_id !== jobID) throw new DesktopDataError("Invalid job identity")
      return result
    }),
    controlJob: async command => callBinding(() => binding.ControlJob(command), value => {
      const result = parseControlView(value)
      if (result.job_id !== command.job_id || result.kind !== command.kind || result.expected_revision !== command.expected_revision) throw new DesktopDataError("Invalid control identity")
      return result
    }),
    getJobControls: async (jobID, after, limit) => callBinding(() => binding.GetJobControls(jobID, after, limit), value => {
      const page = parseControlPage(value)
      if (page.job_id !== jobID) throw new DesktopDataError("Invalid job identity")
      return page
    }),
    retryJobControl: async commandID => callBinding(() => binding.RetryJobControl(commandID), value => {
      const command = parseControlView(value)
      if (command.command_id !== commandID) throw new DesktopDataError("Invalid control identity")
      return command
    }),
		getQuickTask: async (runId) => callBinding(() => binding.GetQuickTask(runId), (value) => {
      const detail = parseQuickTaskDetail(value)
      if (detail.run_id !== runId) throw new DesktopDataError(tx("quickTest:task.invalidHistory"))
      return detail
    }),
    rememberQuickTaskCredential: async (command) => callBinding(() => binding.RememberQuickTaskCredential(command), () => undefined),
    forgetQuickTaskCredential: async (runId) => callBinding(() => binding.ForgetQuickTaskCredential(runId), () => undefined),
    createModel: async (command) => callCatalogMutation(binding, () => binding.CreateModel(command)),
    updateModel: async (command) => callCatalogMutation(binding, () => binding.UpdateModel(command)),
    deleteModel: async (command) => callCatalogMutation(binding, () => binding.DeleteModel(command)),
    saveQuickTestTarget: async (command) => {
      const payload = await callBinding(() => binding.SaveQuickTestTarget(command), value => value)
      try {
        if (!isRecord(payload) || typeof payload.channel_id !== "string") throw new Error("invalid saved channel")
        const catalog = parseCatalogSnapshot(payload.catalog)
        if (!catalog.channels.some(channel => channel.id === payload.channel_id)) throw new Error("saved channel missing")
        return { catalog, channel_id: payload.channel_id }
      } catch (error) {
        await reportFrontendFailure(binding, "load_catalog")(error)
        throw new DesktopClientError("catalog_saved_refresh_failed")
      }
    },
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

function fixtureReportDetail(summary: ReportSnapshot["reports"][number]): ReportDetail {
 const verification = {status:summary.passed ? "passed" as const : "failed" as const,passed:summary.passed_case_count,failed:summary.failed_case_count,observed:summary.observed_case_count,indeterminate:0}
 return {
  schema_version:1,source:"run",
  report:{id:summary.id,run_id:summary.run_id!,run_status:summary.run_status,generated_at:summary.generated_at,protocol:"openai-chat",
   model:{id:"10000000-0000-4000-8000-000000000001",name:summary.model_name},channel:{id:"10000000-0000-4000-8000-000000000002",name:summary.channel_name},
   execution_origin:null,environment:{os:"windows",arch:"amd64",region:"local",network_egress:"direct",app_version:"fixture",engine_version:"go-core"},
   conclusion:{passed:summary.passed,verdict:summary.verdict,issues:[]},sla:{},metrics:{},case_results:[]},
  request_results:[],entries:[{entry_id:"10000000-0000-4000-8000-000000000003",target_kind:"suite",target_id:"10000000-0000-4000-8000-000000000004",key:"fixture-suite",name:"Fixture suite",protocol:"openai-chat",parameters:{},seed:1,warmup_count:0,settings:{},
   load:{mode:"single",concurrency:1,request_count:1,rate_per_second:0,duration_ms:0,request_timeout_ms:60000},status:summary.run_status,
   conclusion:{passed:summary.passed,verdict:summary.verdict,issues:[]},verification,sla:{},metrics:{},timeline:[],distributions:[],cases:[]}],unassigned_request_results:[]}
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
  if (runIDs.size !== runs.length) {
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

function planFromCommand(id: string, revision: number, command: CreatePlanCommand, catalog: CatalogSnapshot): CatalogSnapshot["plans"][number] {
 const entries=command.entries.map(entry=>{
  const target=entry.target_kind==="suite"?catalog.suites.find(item=>item.id===entry.target_id):catalog.test_cases.find(item=>item.id===entry.target_id)
  return {...structuredClone(entry),entry_id:entry.entry_id??crypto.randomUUID(),target_name:target?.name??entry.target_id,target_key:target?.key??entry.target_id,
   case_count:target?("case_count" in target?target.case_count:1):0}
 })
 return {id,revision,name:command.name,protocol:command.protocol,seed:command.seed,entry_count:entries.length,case_count:entries.reduce((sum,entry)=>sum+entry.case_count,0),entries}
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
    protocol:record.protocol as CatalogSnapshot["plans"][number]["protocol"],
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
    isCaseProgress(value) &&
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
    (value.source !== "quick_task" || (value.load_mode === "single" && value.planned === value.case_count && value.duration_ms === 0)) &&
    ((value.planned as number) > 0 || (value.duration_ms as number) > 0 || (value.case_count as number) > 0) &&
    isConclusion(value.conclusion) &&
    (!hasFailure ||
      (value.status === "failed" &&
        isStableErrorCode(value.failure_phase) &&
        isStableErrorCode(value.error_code))) &&
    isNonNegativeSafeInteger(value.completed) &&
    isNonNegativeSafeInteger(value.passed) &&
    isNonNegativeSafeInteger(value.failed) && isNonNegativeSafeInteger(value.observed) && isNonNegativeSafeInteger(value.indeterminate) &&
    value.completed === value.passed + value.failed + value.observed + value.indeterminate &&
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
    case_count: record.case_count as number,
    observed_case_count: record.observed_case_count as number,
    entry_progress: (record.entry_progress as Record<string, unknown>[]).map((suite) => ({
      entry_id: suite.entry_id as string,
      name: suite.name as string,
      case_count: suite.case_count as number,
      observed_case_count: suite.observed_case_count as number,
      status: suite.status as "not_started" | "queued" | "running" | "completed" | "failed" | "cancelled",
    })),
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
    failed: record.failed as number,observed:record.observed as number,indeterminate:record.indeterminate as number,
    artifact_count: record.artifact_count as number,
    started_at: record.started_at as string,
    updated_at: record.updated_at as string,
  }
}

function isCaseProgress(value: Record<string, unknown>): boolean {
  if (!isNonNegativeSafeInteger(value.case_count) || !isNonNegativeSafeInteger(value.observed_case_count) ||
      !Array.isArray(value.entry_progress) || value.entry_progress.length === 0) return false
  const ids = new Set<string>()
  let total = 0
  let observed = 0
  for (const suite of value.entry_progress) {
    if (!isRecord(suite) || !isUUID(suite.entry_id) || ids.has(suite.entry_id) || !isNonBlankString(suite.name) ||
        !isPositiveSafeInteger(suite.case_count) || !isNonNegativeSafeInteger(suite.observed_case_count) ||
        (suite.observed_case_count as number) > (suite.case_count as number) ||
        !["not_started", "queued", "running", "completed", "failed", "cancelled"].includes(suite.status as string)) return false
    ids.add(suite.entry_id)
    total += suite.case_count as number
    observed += suite.observed_case_count as number
  }
  return total === value.case_count && observed === value.observed_case_count
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
