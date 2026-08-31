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
		Reflect.deleteProperty(window, "runtime")
  })

  it("uses the typed Wails methods and forwards command identifiers", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()

    await expect(client.getWorkspace()).resolves.toEqual(FIXTURE_WORKSPACE)
    await expect(client.getCatalog()).resolves.toEqual(FIXTURE_CATALOG)
    await expect(client.getReports()).resolves.toEqual(FIXTURE_REPORTS)
    await client.startRun(FIXTURE_WORKSPACE.plans[0].id)
		const targetCommand = {
			plan_id: FIXTURE_WORKSPACE.plans[0].id,
			model_id: FIXTURE_CATALOG.models[0].id,
			channel_id: FIXTURE_CATALOG.channels[0].id,
		}
		await client.startRunTarget(targetCommand)
    await client.stopSending(FIXTURE_WORKSPACE.runs[0].id)
    await client.cancelRun(FIXTURE_WORKSPACE.runs[0].id)
    const quickCommand = {
      address_mode: "base_url" as const,
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    }
    await expect(client.runQuickTest(quickCommand)).resolves.toMatchObject({
      schema_version: 1,
      success: true,
      endpoint: "https://api.example.test/v1/chat/completions",
    })
    const performanceCommand = {
      address_mode: "base_url" as const,
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      request_count: 4,
      duration_ms: 0,
      concurrency: 2,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    }
    await expect(client.runQuickPerformanceTest(performanceCommand)).resolves.toMatchObject({
      schema_version: 1,
      success: true,
      metrics: { completed: 4, succeeded: 4 },
    })
    const saveCommand = {
      base_url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      model_name: "GPT Test",
      channel_name: "Example",
      existing_model_id: FIXTURE_CATALOG.models[0].id,
    }
    await expect(client.saveQuickTestConnection(saveCommand)).resolves.toEqual(FIXTURE_CATALOG)

    expect(binding.StartRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.plans[0].id)
		expect(binding.StartRunTarget).toHaveBeenCalledWith(targetCommand)
    expect(binding.StopSending).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)
    expect(binding.CancelRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)
    expect(binding.RunQuickTest).toHaveBeenCalledWith(quickCommand)
    expect(binding.RunQuickPerformanceTest).toHaveBeenCalledWith(performanceCommand, "")
    expect(binding.SaveQuickTestConnection).toHaveBeenCalledWith(saveCommand)
  })

  it("rejects malformed quick-test DTOs and drops unexpected payload fields", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.RunQuickTest.mockResolvedValueOnce({
      schema_version: 1,
      success: true,
      address_mode: "base_url",
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      http_status: 200,
      e2e_ms: 42,
      prompt_tokens: 8,
      completion_tokens: 1,
      cached_tokens: 0,
      response_body: "secret provider output",
      api_key: "sk-secret",
    } as never)

    const result = await createDesktopClient().runQuickTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-secret",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    })
    expect(JSON.stringify(result)).not.toContain("secret provider output")
    expect(JSON.stringify(result)).not.toContain("sk-secret")

    binding.RunQuickTest.mockResolvedValueOnce({ ...result, schema_version: 2 })
    await expect(createDesktopClient().runQuickTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-secret",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    })).rejects.toThrow("快速测试数据协议版本")

    binding.RunQuickTest.mockResolvedValueOnce({
      schema_version: 1,
      success: false,
      address_mode: "base_url",
      base_url: "",
      endpoint: "",
      http_status: 0,
      e2e_ms: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
      error_code: "insecure_endpoint",
    } as never)
    await expect(createDesktopClient().runQuickTest({
      address_mode: "base_url",
      url: "http://api.example.test/v1",
      api_key: "sk-secret",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    })).resolves.toMatchObject({ error_code: "insecure_endpoint", endpoint: "" })

    binding.RunQuickTest.mockResolvedValueOnce({
      schema_version: 1,
      success: false,
      address_mode: "base_url",
      base_url: "",
      endpoint: "",
      http_status: 0,
      e2e_ms: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
      error_code: "provider_said_sk-secret",
    } as never)
    await expect(createDesktopClient().runQuickTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-secret",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    })).rejects.toThrow("快速测试数据结构无效")
  })

  it("rejects malformed quick-performance reports and drops raw provider fields", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      profile: { ...performanceReportFixture().profile, api_key: "sk-nested" },
      progress: { ...performanceReportFixture().progress, raw_response: "provider progress" },
      metrics: { ...performanceReportFixture().metrics, provider_detail: "provider metrics" },
      samples: performanceReportFixture().samples.map((sample) => ({ ...sample, provider_response: "secret sample output" })),
      raw_results: [{ response: "secret provider output" }],
      api_key: "sk-secret",
    } as never)
    const command = {
      address_mode: "base_url" as const, url: "https://api.example.test/v1",
      api_key: "sk-secret", model_id: "gpt-test", request_count: 4,
      duration_ms: 0, concurrency: 2, timeout_ms: 30_000,
      input_tokens: 20, output_tokens: 32,
    }
    const report = await createDesktopClient().runQuickPerformanceTest(command)
    expect(JSON.stringify(report)).not.toContain("secret provider output")
    expect(JSON.stringify(report)).not.toContain("sk-secret")
    expect(JSON.stringify(report)).not.toContain("sk-nested")
    expect(JSON.stringify(report)).not.toContain("provider progress")
    expect(JSON.stringify(report)).not.toContain("provider metrics")
    expect(JSON.stringify(report)).not.toContain("secret sample output")

    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      success: false,
      base_url: "",
      endpoint: "",
      profile: { request_count: 0, duration_ms: 0, concurrency: 0, timeout_ms: 0, input_tokens: 0, output_tokens: 0 },
      progress: {
        phase: "not_started", planned: 0, launched: 0, completed: 0,
        peak_in_flight: 0, succeeded: 0, failed: 0, rejected: 0,
        send_duration_ms: 0, drain_duration_ms: 0, total_duration_ms: 0,
      },
      metrics: zeroPerformanceMetrics(),
      samples: [],
      error_code: "invalid_request",
    } as never)
    await expect(createDesktopClient().runQuickPerformanceTest(command)).resolves.toMatchObject({
      success: false,
      error_code: "invalid_request",
    })

    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      failures: [{ error_code: "provider_said_sk-secret", count: 1 }],
    } as never)
    await expect(createDesktopClient().runQuickPerformanceTest(command)).rejects.toThrow("快速性能报告数据结构无效")
  })

	it("publishes only correlated validated quick-performance progress and unsubscribes", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		let eventCallback: ((payload: unknown) => void) | undefined
		const unsubscribe = vi.fn()
		const eventsOn = vi.fn((_name: string, callback: (payload: unknown) => void) => {
			eventCallback = callback
			return unsubscribe
		})
		Object.defineProperty(window, "runtime", {
			configurable: true,
			value: { EventsOn: eventsOn },
		})
		binding.RunQuickPerformanceTest.mockImplementationOnce(async (_command, progressID) => {
			const progress = {
				phase: "sending", planned: 4, launched: 2, completed: 1,
				peak_in_flight: 2, succeeded: 1, failed: 0, rejected: 0,
				send_duration_ms: 100, drain_duration_ms: 0, total_duration_ms: 100,
			}
			eventCallback?.({ progress_id: "99999999-9999-4999-8999-999999999999", progress })
			eventCallback?.({ progress_id: progressID, progress: { ...progress, api_key: "sk-secret" }, raw_response: "secret" })
			return performanceReportFixture()
		})
		const progress = vi.fn()
		const command = {
			address_mode: "base_url" as const, url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", request_count: 4,
			duration_ms: 0, concurrency: 2, timeout_ms: 30_000,
			input_tokens: 20, output_tokens: 32,
		}

		const report = await createDesktopClient().runQuickPerformanceTest(command, progress)

		expect(eventsOn).toHaveBeenCalledWith("quick-performance-progress", expect.any(Function))
		expect(binding.RunQuickPerformanceTest).toHaveBeenCalledWith(command, expect.stringMatching(/^[0-9a-f-]{36}$/))
		expect(progress).toHaveBeenCalledTimes(1)
		expect(progress).toHaveBeenCalledWith(expect.objectContaining({ phase: "sending", completed: 1 }))
		expect(JSON.stringify(progress.mock.calls)).not.toContain("sk-secret")
		expect(JSON.stringify(progress.mock.calls)).not.toContain("raw_response")
		expect(report.metrics.schedule_lag_p90_ms).toBe(2.7)
		expect(report.metrics.schedule_lag_p99_ms).toBe(2.97)
		expect(unsubscribe).toHaveBeenCalledTimes(1)
	})

	it("accepts archived performance reports created before queue P90 and P99 were recorded", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const fixture = performanceReportFixture()
		const {
			schedule_lag_p90_ms: _legacyP90,
			schedule_lag_p99_ms: _legacyP99,
			...legacyMetrics
		} = fixture.metrics
		binding.RunQuickPerformanceTest.mockResolvedValueOnce({
			...fixture,
			metrics: legacyMetrics,
		} as never)

		const report = await createDesktopClient().runQuickPerformanceTest({
			address_mode: "base_url", url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", request_count: 4,
			duration_ms: 0, concurrency: 2, timeout_ms: 30_000,
			input_tokens: 20, output_tokens: 32,
		})

		expect(report.metrics.schedule_lag_p90_ms).toBe(0)
		expect(report.metrics.schedule_lag_p99_ms).toBe(0)
	})

	it("reads complete report details and forwards all export formats", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const client = createDesktopClient()
		const reportID = FIXTURE_REPORTS.reports[0].id
		const detail = await client.getReportDetail(reportID)
		expect(detail.source).toBe("run")
		if (detail.source !== "run") throw new Error("expected run report")
		expect(detail.report.id).toBe(reportID)
		expect(binding.GetReportDetail).toHaveBeenCalledWith(reportID)
		for (const format of ["json", "html", "png", "pdf"] as const) {
			const exported = await client.exportReport(reportID, format, "team-alpha")
			expect(exported.filename).toContain(reportID)
			expect(binding.ExportReport).toHaveBeenLastCalledWith(reportID, format, "team-alpha")
		}
	})

	it("parses an archived quick-performance detail through the closed report boundary", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const reportID = "77777777-7777-4777-8777-777777777771"
		const detailPayload: Record<string, unknown> = {
			schema_version: 1,
			source: "quick_performance",
			performance: {
				...performanceReportFixture(),
				report_id: reportID,
				generated_at: "2026-08-31T14:30:00Z",
				archived: true,
				archive_status: "archived",
				api_key: "sk-secret",
				samples: performanceReportFixture().samples.map((sample) => ({ ...sample, raw_response: "provider secret" })),
			},
		}
		binding.GetReportDetail.mockResolvedValueOnce(detailPayload as never)

		const detail = await createDesktopClient().getReportDetail(reportID)
		expect(detail.source).toBe("quick_performance")
		expect(JSON.stringify(detail)).not.toContain("sk-secret")
		expect(JSON.stringify(detail)).not.toContain("provider secret")
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
		ExportReport: vi.fn(async (reportID: string, format: string, _watermark: string) => ({
			filename: `llm-studio-report-${reportID}.${format}`,
			media_type: format === "json" ? "application/json" : "application/octet-stream",
			data_base64: "e30=",
		})),
		GetComparisons: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
    StartRun: vi.fn(async () => structuredClone(payload)),
		StartRunTarget: vi.fn(async () => structuredClone(payload)),
    StopSending: vi.fn(async () => structuredClone(payload)),
    CancelRun: vi.fn(async () => structuredClone(payload)),
		StartComparison: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
		RunQuickTest: vi.fn(async () => ({
			schema_version: 1, success: true, address_mode: "base_url",
			base_url: "https://api.example.test/v1",
			endpoint: "https://api.example.test/v1/chat/completions",
			http_status: 200, e2e_ms: 42, prompt_tokens: 8,
			completion_tokens: 1, cached_tokens: 0,
		})),
		RunQuickPerformanceTest: vi.fn(async (_command?: unknown, _progressID?: string) => performanceReportFixture()),
		SaveQuickTestConnection: vi.fn(async () => structuredClone(catalog)),
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

function performanceReportFixture() {
  return {
    schema_version: 1, archived: false, archive_status: "not_attempted", model_id: "gpt-test", success: true, address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: { request_count: 4, duration_ms: 0, concurrency: 2, timeout_ms: 30_000, input_tokens: 20, output_tokens: 32 },
    progress: {
      phase: "completed", planned: 4, launched: 4, completed: 4,
      peak_in_flight: 2, succeeded: 4, failed: 0, rejected: 0,
      send_duration_ms: 300, drain_duration_ms: 20, total_duration_ms: 320,
    },
    metrics: {
      completed: 4, succeeded: 4, failed: 0, timed_out: 0,
      success_rate_percent: 100, request_qps: 12.5, rpm: 750,
      input_tpm: 15_000, output_tpm: 24_000, total_tpm: 39_000, generation_tps: 400,
      ttft_p50_ms: 30, ttft_p90_ms: 40, ttft_p95_ms: 42, ttft_p99_ms: 44, ttft_average_ms: 32,
      tpot_p50_ms: 4, tpot_p90_ms: 5, tpot_p95_ms: 6, tpot_p99_ms: 7, tpot_average_ms: 4.5,
      e2e_p50_ms: 60, e2e_p90_ms: 75, e2e_p95_ms: 80, e2e_p99_ms: 84, e2e_average_ms: 65,
			schedule_lag_p50_ms: 1.5, schedule_lag_p90_ms: 2.7, schedule_lag_p95_ms: 2.85, schedule_lag_p99_ms: 2.97, schedule_lag_average_ms: 1.5,
      prompt_tokens: 80, completion_tokens: 128, cached_tokens: 20, cache_rate_percent: 25,
    },
    samples: [
      performanceSample(0, 60, 60, 30, 4), performanceSample(1, 75, 74, 40, 5),
      performanceSample(2, 84, 82, 44, 7), performanceSample(3, 90, 88, 42, 6),
    ],
    failures: [],
  }
}

function performanceSample(requestIndex: number, finished: number, e2e: number, ttft: number, tpot: number) {
  return { request_index: requestIndex, scheduled_offset_ms: 0, started_offset_ms: requestIndex, finished_offset_ms: finished, schedule_lag_ms: requestIndex, e2e_ms: e2e, ttft_ms: ttft, tpot_ms: tpot, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 }
}

function zeroPerformanceMetrics() {
  const report = performanceReportFixture().metrics
  return Object.fromEntries(Object.keys(report).map((key) => [key, 0]))
}

function reportDetailFixture(reportID: string) {
	const summary = FIXTURE_REPORTS.reports.find((report) => report.id === reportID) ?? FIXTURE_REPORTS.reports[0]
	return {
		schema_version: 1,
		source: "run",
		report: {
			id: summary.id, run_id: summary.run_id!, run_status: summary.run_status, generated_at: summary.generated_at,
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
