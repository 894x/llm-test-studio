import { afterEach, describe, expect, it, vi } from "vitest"

import {
  FIXTURE_CATALOG,
  FIXTURE_REPORTS,
  FIXTURE_WORKSPACE,
} from "@/features/runs/fixtures"
import { EMPTY_COMPARISONS } from "@/features/comparisons/data"

import { createDesktopClient } from "./desktop-client"

describe("Wails desktop client", () => {
  afterEach(() => {
    Reflect.deleteProperty(window, "go")
  })

  it("uses the typed Wails methods and forwards command identifiers", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()

    await expect(client.getWorkspace()).resolves.toEqual(FIXTURE_WORKSPACE)
    await expect(client.getCatalog()).resolves.toEqual(FIXTURE_CATALOG)
    await expect(client.getReports()).resolves.toEqual(FIXTURE_REPORTS)
    await client.startRun(FIXTURE_WORKSPACE.plans[0].id)
    await client.stopSending(FIXTURE_WORKSPACE.runs[0].id)
    await client.cancelRun(FIXTURE_WORKSPACE.runs[0].id)

    expect(binding.StartRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.plans[0].id)
    expect(binding.StopSending).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)
    expect(binding.CancelRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)
  })

	it("reads complete report details and forwards all export formats", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const client = createDesktopClient()
		const reportID = FIXTURE_REPORTS.reports[0].id
		const detail = await client.getReportDetail(reportID)
		expect(detail.report.id).toBe(reportID)
		expect(binding.GetReportDetail).toHaveBeenCalledWith(reportID)
		for (const format of ["json", "html", "png", "pdf"] as const) {
			const exported = await client.exportReport(reportID, format)
			expect(exported.filename).toContain(reportID)
			expect(binding.ExportReport).toHaveBeenLastCalledWith(reportID, format)
		}
	})

  it("forwards all catalog create update and delete commands", async () => {
	const binding = installBinding(FIXTURE_WORKSPACE)
	const client = createDesktopClient()
	const model = FIXTURE_CATALOG.models[0]
	const channel = FIXTURE_CATALOG.channels[0]
	const mapping = FIXTURE_CATALOG.channel_models[0]
	const testCase = FIXTURE_CATALOG.test_cases[0]
	const suite = FIXTURE_CATALOG.suites[0]
	const plan = FIXTURE_CATALOG.plans[0]
	const deletion = (id: string, expected_revision: number) => ({ id, expected_revision })

	const commands = [
		["CreateModel", "createModel", { name: "new model", protocol: "openai-chat", capabilities: ["chat"] }],
		["UpdateModel", "updateModel", { id: model.id, expected_revision: model.revision, name: model.name, protocol: model.protocol, capabilities: model.capabilities }],
		["DeleteModel", "deleteModel", deletion(model.id, model.revision)],
		["CreateChannel", "createChannel", { name: "new channel", base_url: "https://example.test/v1", api_key: "test-key-1234", protocol: "openai-chat", enabled: true }],
		["UpdateChannel", "updateChannel", { id: channel.id, expected_revision: channel.revision, name: channel.name, base_url: channel.base_url, api_key: "test-key-5678", protocol: channel.protocol, enabled: channel.enabled }],
		["DeleteChannel", "deleteChannel", deletion(channel.id, channel.revision)],
		["CreateChannelModel", "createChannelModel", { channel_id: mapping.channel_id, model_id: mapping.model_id, upstream_model_name: "new-upstream" }],
		["UpdateChannelModel", "updateChannelModel", { id: mapping.id, expected_revision: mapping.revision, upstream_model_name: mapping.upstream_model_name }],
		["DeleteChannelModel", "deleteChannelModel", deletion(mapping.id, mapping.revision)],
		["CreateTestCase", "createTestCase", withoutIdentity(testCase)],
		["UpdateTestCase", "updateTestCase", { ...withoutIdentity(testCase), id: testCase.id, expected_revision: testCase.revision }],
		["DeleteTestCase", "deleteTestCase", deletion(testCase.id, testCase.revision)],
		["CreateSuite", "createSuite", { name: "new suite", cases: suite.cases }],
		["UpdateSuite", "updateSuite", { id: suite.id, expected_revision: suite.revision, name: suite.name, cases: suite.cases }],
		["DeleteSuite", "deleteSuite", deletion(suite.id, suite.revision)],
		["CreatePlan", "createPlan", withoutIdentity(plan)],
		["UpdatePlan", "updatePlan", { ...withoutIdentity(plan), id: plan.id, expected_revision: plan.revision }],
		["DeletePlan", "deletePlan", deletion(plan.id, plan.revision)],
	] as const

	for (const [bindingMethod, clientMethod, command] of commands) {
		await expect(client[clientMethod](command as never)).resolves.toEqual(FIXTURE_CATALOG)
		expect(binding[bindingMethod]).toHaveBeenLastCalledWith(command)
	}
  })

  it.each([
    [{ ...FIXTURE_WORKSPACE, schema_version: 2 }, "协议版本"],
    [{ ...FIXTURE_WORKSPACE, plans: [{ id: "broken" }] }, "测试计划"],
    [
      {
        ...FIXTURE_WORKSPACE,
        runs: [{ ...FIXTURE_WORKSPACE.runs[0], status: "surprise" }],
      },
      "运行记录",
    ],
    [
      {
        ...FIXTURE_WORKSPACE,
        runs: [{ ...FIXTURE_WORKSPACE.runs[0], completed: Number.NaN }],
      },
      "运行记录",
    ],
  ])("rejects malformed binding payloads %#o", async (payload, message) => {
    installBinding(payload)

    await expect(createDesktopClient().getWorkspace()).rejects.toThrow(message)
  })

  it("rebuilds an allow-listed snapshot instead of retaining unexpected fields", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE) as unknown as Record<
      string,
      unknown
    >
    payload.credential = "sk-should-never-enter-react"
    const plans = payload.plans as Array<Record<string, unknown>>
    plans[0].network_egress = "private-egress-secret"
    const runs = payload.runs as Array<Record<string, unknown>>
    runs[0].base_url = "https://secret-provider.example/v1"
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()
    const encoded = JSON.stringify(snapshot)

    expect(encoded).not.toContain("sk-should-never-enter-react")
    expect(encoded).not.toContain("private-egress-secret")
    expect(encoded).not.toContain("secret-provider.example")
    expect(snapshot).toEqual(FIXTURE_WORKSPACE)
  })

  it("preserves stable failure phase and error code for failed runs", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], {
      status: "failed",
      failure_phase: "execute",
      error_code: "run_execution_failed",
    })
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()

    expect(snapshot.runs[0]).toMatchObject({
      status: "failed",
      failure_phase: "execute",
      error_code: "run_execution_failed",
    })
  })

  it.each([
    ["partial metadata", { status: "failed", failure_phase: "execute" }],
    ["metadata on a non-failed run", { status: "completed", failure_phase: "execute", error_code: "run_execution_failed" }],
    ["invalid stable code", { status: "failed", failure_phase: "execute!", error_code: "run_execution_failed" }],
  ])("rejects %s", async (_name, failure) => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], failure)
    installBinding(payload)

    await expect(createDesktopClient().getWorkspace()).rejects.toThrow("运行记录")
  })

  it("accepts historical failed runs without failure metadata", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], { status: "failed" })
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()

    expect(snapshot.runs[0].status).toBe("failed")
    expect(snapshot.runs[0]).not.toHaveProperty("failure_phase")
    expect(snapshot.runs[0]).not.toHaveProperty("error_code")
  })

  it("drops unexpected secret-bearing fields from catalog and report payloads", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    catalog.api_key = "opaque-catalog-secret"
    ;(catalog.channels as Array<Record<string, unknown>>)[0].credential_id =
      "99999999-9999-4999-8999-999999999999"
    ;(catalog.models as Array<Record<string, unknown>>)[0].provider_token =
      "opaque-model-secret"

    const reports = structuredClone(FIXTURE_REPORTS) as unknown as Record<string, unknown>
    reports.network_egress = "private-egress-secret"
    ;(reports.reports as Array<Record<string, unknown>>)[0].evidence = {
      authorization: "opaque-report-secret",
    }
    installBinding(FIXTURE_WORKSPACE, catalog, reports)

    const client = createDesktopClient()
    const encoded = JSON.stringify([
      await client.getCatalog(),
      await client.getReports(),
    ])

    expect(encoded).not.toContain("opaque-catalog-secret")
    expect(encoded).not.toContain("opaque-model-secret")
    expect(encoded).not.toContain("private-egress-secret")
    expect(encoded).not.toContain("opaque-report-secret")
    expect(encoded).not.toContain("credential_id")
  })

  it("preserves only the public imported-case policy fields", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    const testCase = (catalog.test_cases as Array<Record<string, unknown>>)[0]
    Object.assign(testCase, {
      key: "T001",
      dimension: "must",
      enabled: false,
      default: false,
      severity: "critical",
      execution_mode: "manual",
      source_path: "cases/private/should-not-cross-boundary.json",
      source_bytes_sha256: "secret-provenance-value",
    })
    installBinding(FIXTURE_WORKSPACE, catalog)

    const snapshot = await createDesktopClient().getCatalog()
    expect(snapshot.test_cases[0]).toMatchObject({
      key: "T001",
      dimension: "must",
      enabled: false,
      default: false,
      severity: "critical",
      execution_mode: "manual",
    })
    expect(JSON.stringify(snapshot)).not.toContain("source_path")
    expect(JSON.stringify(snapshot)).not.toContain("secret-provenance-value")
  })

  it("accepts every TestCase shape allowed by the Go catalog contract", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    const testCase = (catalog.test_cases as Array<Record<string, unknown>>)[0]
    const longKey = `case.${"a".repeat(160)}`
    const longDimension = "compatibility ".repeat(14).trim()
    Object.assign(testCase, {
      key: longKey,
      dimension: longDimension,
      assertion_kinds: ["custom", "custom"],
      assertions: [
        { kind: "custom", config: {} },
        { kind: "custom", config: { mode: "manual" } },
      ],
    })
    installBinding(FIXTURE_WORKSPACE, catalog)

    const snapshot = await createDesktopClient().getCatalog()
    expect(snapshot.test_cases[0]).toMatchObject({
      key: longKey,
      dimension: longDimension,
      assertion_kinds: ["custom", "custom"],
    })
  })

  it.each([
    ["catalog_unavailable", "测试目录暂不可用", "GetCatalog", "getCatalog"],
    ["reports_unavailable", "测试报告暂不可用", "GetReports", "getReports"],
    ["catalog_invalid", "目录内容无效", "CreateModel", "createModel"],
    ["catalog_revision_conflict", "对象版本已变化或仍被引用", "UpdateModel", "updateModel"],
    ["catalog_not_found", "对象已删除或不存在", "DeleteModel", "deleteModel"],
  ] as const)("maps the public %s binding error", async (code, message, bindingMethod, clientMethod) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding[bindingMethod].mockRejectedValueOnce({ code })

    await expect((createDesktopClient()[clientMethod] as (command?: never) => Promise<unknown>)()).rejects.toThrow(message)
  })

  it("rejects corrupt catalog references and contradictory report conclusions", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.channel_models[0].model_id =
      "99999999-9999-4999-8999-999999999999"
    const reports = structuredClone(FIXTURE_REPORTS)
    reports.reports[0].failed_case_count = 1
    installBinding(FIXTURE_WORKSPACE, catalog, reports)

    const client = createDesktopClient()
    await expect(client.getCatalog()).rejects.toThrow("模型映射引用")
    await expect(client.getReports()).rejects.toThrow("报告摘要")
  })
})

function installBinding(
  payload: unknown,
  catalog: unknown = FIXTURE_CATALOG,
  reports: unknown = FIXTURE_REPORTS,
) {
  const binding = {
    GetWorkspace: vi.fn(async () => structuredClone(payload)),
    GetCatalog: vi.fn(async () => structuredClone(catalog)),
    GetReports: vi.fn(async () => structuredClone(reports)),
		GetReportDetail: vi.fn(async (reportID: string) => structuredClone(reportDetailFixture(reportID))),
		ExportReport: vi.fn(async (reportID: string, format: string) => ({
			filename: `llm-studio-report-${reportID}.${format}`,
			media_type: format === "json" ? "application/json" : "application/octet-stream",
			data_base64: "e30=",
		})),
		GetComparisons: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
    StartRun: vi.fn(async () => structuredClone(payload)),
    StopSending: vi.fn(async () => structuredClone(payload)),
    CancelRun: vi.fn(async () => structuredClone(payload)),
		StartComparison: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
	CreateModel: vi.fn(async () => structuredClone(catalog)),
	UpdateModel: vi.fn(async () => structuredClone(catalog)),
	DeleteModel: vi.fn(async () => structuredClone(catalog)),
	CreateChannel: vi.fn(async () => structuredClone(catalog)),
	UpdateChannel: vi.fn(async () => structuredClone(catalog)),
	DeleteChannel: vi.fn(async () => structuredClone(catalog)),
	CreateChannelModel: vi.fn(async () => structuredClone(catalog)),
	UpdateChannelModel: vi.fn(async () => structuredClone(catalog)),
	DeleteChannelModel: vi.fn(async () => structuredClone(catalog)),
	CreateTestCase: vi.fn(async () => structuredClone(catalog)),
	UpdateTestCase: vi.fn(async () => structuredClone(catalog)),
	DeleteTestCase: vi.fn(async () => structuredClone(catalog)),
	CreateSuite: vi.fn(async () => structuredClone(catalog)),
	UpdateSuite: vi.fn(async () => structuredClone(catalog)),
	DeleteSuite: vi.fn(async () => structuredClone(catalog)),
	CreatePlan: vi.fn(async () => structuredClone(catalog)),
	UpdatePlan: vi.fn(async () => structuredClone(catalog)),
	DeletePlan: vi.fn(async () => structuredClone(catalog)),
  }
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { main: { DesktopApp: binding } },
  })
  return binding
}

function reportDetailFixture(reportID: string) {
	const summary = FIXTURE_REPORTS.reports.find((report) => report.id === reportID) ?? FIXTURE_REPORTS.reports[0]
	return {
		schema_version: 1,
		report: {
			id: summary.id, run_id: summary.run_id, run_status: summary.run_status, generated_at: summary.generated_at,
			model: { id: "22222222-2222-4222-8222-222222222221", name: summary.model_name },
			channel: { id: "33333333-3333-4333-8333-333333333331", name: summary.channel_name },
			environment: { os: "windows", arch: "amd64", region: "local", network_egress: "direct", app_version: "test", engine_version: "go-core-v1" },
			conclusion: { passed: summary.passed, verdict: summary.verdict, issues: [] },
			sla: {}, metrics: {}, case_results: [],
		},
		request_results: [],
	}
}

function withoutIdentity<T extends { id: string; revision: number }>(value: T): Omit<T, "id" | "revision"> {
  const { id: _id, revision: _revision, ...rest } = value
  return rest
}
