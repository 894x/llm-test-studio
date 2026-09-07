import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"

import App from "./App"
import {
  DesktopClientError,
  type DesktopClient,
  type DesktopDiagnosticsSnapshot,
} from "./app/desktop-client"
import {
  FIXTURE_CATALOG,
  FIXTURE_REPORTS,
  FIXTURE_WORKSPACE,
} from "./features/runs/fixtures"
import type { WorkspaceSnapshot } from "./features/runs/data"
import { EMPTY_COMPARISONS } from "./features/comparisons/data"
import type { QuickPerformanceReport } from "./features/quick-test/data"
import indexHtml from "../index.html?raw"

const indexCss = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8")

function deferred<T>() {
  let resolvePromise!: (value: T) => void
  const promise = new Promise<T>((resolve) => {
    resolvePromise = resolve
  })
  return { promise, resolve: resolvePromise }
}

function desktopClient(): DesktopClient & {
  workspace: WorkspaceSnapshot
} {
  const client = {
    workspace: structuredClone(FIXTURE_WORKSPACE),
    getWorkspace: vi.fn(async () => structuredClone(client.workspace)),
    getCatalog: vi.fn(async () => structuredClone(FIXTURE_CATALOG)),
    getReports: vi.fn(async () => structuredClone(FIXTURE_REPORTS)),
		getDiagnostics: vi.fn(async () => ({
			schema_version: 1 as const, available: true, format: "jsonl" as const,
			max_file_bytes: 10 * 1024 * 1024, backup_files: 5,
			run_correlation: true, request_correlation: true,
		})),
		openDiagnosticsDirectory: vi.fn(async () => undefined),
		getReportDetail: vi.fn(async () => { throw new Error("report detail unavailable in shell fixture") }),
		exportReport: vi.fn(async () => { throw new Error("report export unavailable in shell fixture") }),
		saveReportExport: vi.fn(async () => true),
		copyReportPNG: vi.fn(async () => undefined),
		getComparisons: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
    startRun: vi.fn(async () => structuredClone(client.workspace)),
		startRunTarget: vi.fn(async () => structuredClone(client.workspace)),
		startQuickTask: vi.fn(async () => client.workspace.runs[0].id),
		getQuickTask: vi.fn(),
    rememberQuickTaskCredential: vi.fn(async () => undefined), forgetQuickTaskCredential: vi.fn(async () => undefined),
    stopSending: vi.fn(async (runId: string) => {
      client.workspace = {
        ...client.workspace,
        runs: client.workspace.runs.map((run) =>
          run.id === runId ? { ...run, status: "draining" as const } : run,
        ),
      }
      return structuredClone(client.workspace)
    }),
    cancelRun: vi.fn(async (runId: string) => {
      client.workspace = {
        ...client.workspace,
        active_run_id: undefined,
        runs: client.workspace.runs.map((run) =>
          run.id === runId ? { ...run, status: "cancelled" as const } : run,
        ),
      }
      return structuredClone(client.workspace)
    }),
		startComparison: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
		runQuickPerformanceTest: vi.fn(),

    ...catalogMutationMocks(),
  }
  return client
}

function catalogMutationMocks() {
  const reply = async () => structuredClone(FIXTURE_CATALOG)
  return {
    createModel: vi.fn(reply), updateModel: vi.fn(reply), deleteModel: vi.fn(reply),
    createChannel: vi.fn(reply), updateChannel: vi.fn(reply), deleteChannel: vi.fn(reply),
    createChannelModel: vi.fn(reply), updateChannelModel: vi.fn(reply), deleteChannelModel: vi.fn(reply),
    createTestCase: vi.fn(reply), updateTestCase: vi.fn(reply), deleteTestCase: vi.fn(reply),
    createSuite: vi.fn(reply), updateSuite: vi.fn(reply), deleteSuite: vi.fn(reply),
    createPlan: vi.fn(reply), updatePlan: vi.fn(reply), deletePlan: vi.fn(reply),
  }
}

function archivedPerformanceReport(reportID: string): QuickPerformanceReport {
  return {
    schema_version: 1,
    report_id: reportID,
    generated_at: "2026-08-31T14:30:00Z",
    archived: true,
    archive_status: "archived",
    model_id: "gpt-new",
    success: true,
    address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: { request_count: 1, duration_ms: 0, concurrency: 1, timeout_ms: 60_000, input_tokens: 100, output_tokens: 100 },
    progress: { phase: "completed", planned: 1, launched: 1, completed: 1, in_flight: 0, peak_in_flight: 1, succeeded: 1, failed: 0, rejected: 0, send_duration_ms: 40, drain_duration_ms: 20, total_duration_ms: 60 },
    metrics: {
      completed: 1, succeeded: 1, failed: 0, timed_out: 0, success_rate_percent: 100, request_qps: 16.7, rpm: 1_000,
      input_tpm: 100_000, output_tpm: 100_000, total_tpm: 200_000, generation_tps: 1_666.7,
      ttft_p50_ms: 20, ttft_p90_ms: 20, ttft_p95_ms: 20, ttft_p99_ms: 20, ttft_average_ms: 20,
      tpot_p50_ms: 2, tpot_p90_ms: 2, tpot_p95_ms: 2, tpot_p99_ms: 2, tpot_average_ms: 2,
      e2e_p50_ms: 60, e2e_p90_ms: 60, e2e_p95_ms: 60, e2e_p99_ms: 60, e2e_average_ms: 60,
      schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 0, schedule_lag_p95_ms: 0, schedule_lag_p99_ms: 0, schedule_lag_average_ms: 0,
      prompt_tokens: 100, completion_tokens: 100, cached_tokens: 0, cache_rate_percent: 0,
    },
    samples: [{ request_index: 0, scheduled_offset_ms: 0, started_offset_ms: 0, finished_offset_ms: 60, schedule_lag_ms: 0, e2e_ms: 60, ttft_ms: 20, tpot_ms: 2, http_status: 200, success: true, timed_out: false, prompt_tokens: 100, completion_tokens: 100, cached_tokens: 0 }],
    failures: [],
  }
}

describe("desktop run workspace", () => {
  beforeEach(() => {
    window.localStorage.clear()
    document.documentElement.className = ""
    Object.defineProperty(window.navigator, "languages", {
      configurable: true,
      value: ["zh-CN"],
    })
    window.history.replaceState(null, "", "#runs")
    delete (window as Window & { runtime?: unknown }).runtime
  })

  it("opens on the compact run workspace instead of a dashboard", async () => {
    render(<App client={desktopClient()} />)

    expect(
      await screen.findByRole("heading", { name: "运行工作区" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("navigation", { name: "主导航" })).toHaveTextContent(
      "运行",
    )
    expect(
      screen.getByRole("navigation", { name: "测试计划" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("table", { name: "运行记录" })).toBeInTheDocument()
    expect(
      screen.getByRole("complementary", { name: "运行详情" }),
    ).toHaveTextContent("营销文案基准")
    expect(screen.queryByRole("button", { name: /更多操作/ })).not.toBeInTheDocument()

    const table = screen.getByRole("table", { name: "运行记录" })
    expect(table.closest('[data-slot="scroll-area-viewport"]')).not.toBeNull()
    expect(table.closest('[data-slot="table-container"]')).toBeNull()
    expect(
      document.querySelectorAll('[data-slot="scroll-area-scrollbar"]'),
    ).toHaveLength(2)
    const verticalRail = document.querySelector(
      '[data-slot="scroll-area-scrollbar"][data-orientation="vertical"]',
    )
    const horizontalRail = document.querySelector(
      '[data-slot="scroll-area-scrollbar"][data-orientation="horizontal"]',
    )
    expect(verticalRail).toHaveClass("w-[5px]")
    expect(horizontalRail).toHaveClass("h-[5px]")
    expect(verticalRail).toHaveStyle({ top: "4px", right: "4px", bottom: "4px" })
    expect(horizontalRail).toHaveStyle({ left: "4px", right: "4px", bottom: "4px" })

    const definitionRows = document.querySelectorAll(
      '[data-slot="inspector-definition-row"]',
    )
    expect(definitionRows).toHaveLength(6)
    definitionRows.forEach((row) => {
      expect(row.children).toHaveLength(2)
      expect(row.querySelector("dd")?.children).toHaveLength(0)
    })
  })

	it("starts a same-model comparison across selected channels", async () => {
		const user = userEvent.setup()
		const client = desktopClient()
		const catalog = structuredClone(FIXTURE_CATALOG)
		const plan = catalog.plans[0]
		const model = catalog.models[0]
		const secondChannel = catalog.channels[1]
		plan.channel_ids.push(secondChannel.id)
		plan.channel_count = 2
		secondChannel.model_count += 1
		catalog.channel_models.push({
			id: "77777777-7777-4777-8777-777777777799", revision: 1,
			channel_id: secondChannel.id, model_id: model.id, upstream_model_name: model.name,
		})
		vi.mocked(client.getCatalog).mockResolvedValue(catalog)

		render(<App client={client} />)
		await user.click(await screen.findByRole("button", { name: "渠道对比" }))
		await user.click(screen.getByRole("checkbox", { name: /OpenAI 主渠道/ }))
		await user.click(screen.getByRole("checkbox", { name: /阿里云备用渠道/ }))
		await user.click(screen.getByRole("button", { name: "对比 2 个渠道" }))

		await waitFor(() => expect(client.startComparison).toHaveBeenCalledWith({
			plan_id: plan.id, model_id: model.id, channel_ids: [catalog.channels[0].id, secondChannel.id],
		}))
	})

	it("identifies the comparison selection when starting it fails", async () => {
		const user = userEvent.setup()
		const client = desktopClient()
		const catalog = structuredClone(FIXTURE_CATALOG)
		const plan = catalog.plans[0]
		const model = catalog.models[0]
		const secondChannel = catalog.channels[1]
		plan.channel_ids.push(secondChannel.id)
		plan.channel_count = 2
		secondChannel.model_count += 1
		catalog.channel_models.push({
			id: "77777777-7777-4777-8777-777777777798", revision: 1,
			channel_id: secondChannel.id, model_id: model.id, upstream_model_name: model.name,
		})
		vi.mocked(client.getCatalog).mockResolvedValue(catalog)
		vi.mocked(client.startComparison).mockRejectedValueOnce(
			new DesktopClientError("comparison_unavailable"),
		)

		render(<App client={client} />)
		await user.click(await screen.findByRole("button", { name: "渠道对比" }))
		const dialog = screen.getByRole("dialog", { name: "同模型渠道对比" })
		await user.click(within(dialog).getByRole("checkbox", { name: /OpenAI 主渠道/ }))
		await user.click(within(dialog).getByRole("checkbox", { name: /阿里云备用渠道/ }))
		await user.click(within(dialog).getByRole("button", { name: "对比 2 个渠道" }))

		expect(await within(dialog).findByRole("alert")).toHaveTextContent(
			"启动渠道对比（计划：营销文案基准，模型：gpt-5.2，渠道：2 个）失败：无法读取渠道对比，请重试；日志操作名：load_comparisons",
		)
	})

  it("opens every primary workspace from the main navigation", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "运行工作区" })
    for (const [label, heading, evidence] of [
      ["总览", "工作台总览", "6 个模型"],
      ["快速测试", "快速测试", "选择一个测试任务，填写连接与参数即可运行；无需预先创建模型、渠道或计划。"],
      ["模型与渠道", "模型与渠道", "gpt-5.2"],
      ["用例", "测试用例", "基础对话"],
      ["计划", "测试计划", "营销文案基准"],
      ["报告", "测试报告", "兼容性门禁通过"],
      ["运行", "运行工作区", "JSON 模式回归"],
    ] as const) {
      await user.click(screen.getByRole("button", { name: label }))
      expect(
        await screen.findByRole("heading", { name: heading }),
      ).toBeInTheDocument()
      expect(screen.getAllByText(evidence).length).toBeGreaterThan(0)
    }
    expect(client.getWorkspace).toHaveBeenCalledTimes(1)
    expect(client.getCatalog).toHaveBeenCalledTimes(1)
    expect(client.getReports).toHaveBeenCalledTimes(1)
  })

  it("opens diagnostics from compact header chrome without exposing a filesystem path", async () => {
		const user = userEvent.setup()
		const client = desktopClient()
		render(<App client={client} />)
		await screen.findByRole("heading", { name: "运行工作区" })

		await user.click(screen.getByRole("button", { name: "诊断信息" }))
		const dialog = await screen.findByRole("dialog", { name: "诊断信息" })
		expect(dialog).toHaveTextContent("结构化日志已启用")
		expect(dialog).toHaveTextContent("10 MB")
		expect(dialog).toHaveTextContent("5 个历史文件")
		expect(dialog).toHaveTextContent("Run 与 Request")
		expect(dialog).not.toHaveTextContent("secret-log-path")
		expect(dialog.querySelectorAll("dl")).toHaveLength(1)
		expect(dialog.querySelectorAll("dt")).toHaveLength(6)
		expect(dialog.querySelectorAll("dd")).toHaveLength(6)

		await user.click(within(dialog).getByRole("button", { name: "打开日志目录" }))
		expect(client.openDiagnosticsDirectory).toHaveBeenCalledOnce()
	})

	it("ignores a stale diagnostics load after the sheet is reopened", async () => {
		const user = userEvent.setup()
		const client = desktopClient()
		const first = deferred<DesktopDiagnosticsSnapshot>()
		const second = deferred<DesktopDiagnosticsSnapshot>()
		vi.mocked(client.getDiagnostics)
			.mockReturnValueOnce(first.promise)
			.mockReturnValueOnce(second.promise)

		render(<App client={client} />)
		await screen.findByRole("heading", { name: "运行工作区" })
		await user.click(screen.getByRole("button", { name: "诊断信息" }))
		expect(await screen.findByRole("dialog", { name: "诊断信息" })).toHaveTextContent("正在读取诊断状态")
		await user.keyboard("{Escape}")
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "诊断信息" })).not.toBeInTheDocument())
		await user.click(screen.getByRole("button", { name: "诊断信息" }))

		second.resolve({
			schema_version: 1, available: false, format: "jsonl",
			max_file_bytes: 2 * 1024 * 1024, backup_files: 2,
			run_correlation: true, request_correlation: false,
		})
		const dialog = await screen.findByRole("dialog", { name: "诊断信息" })
		expect(dialog).toHaveTextContent("诊断日志暂不可用")

		first.resolve({
			schema_version: 1, available: true, format: "jsonl",
			max_file_bytes: 99 * 1024 * 1024, backup_files: 99,
			run_correlation: true, request_correlation: true,
		})
		await waitFor(() => {
			expect(dialog).toHaveTextContent("诊断日志暂不可用")
			expect(dialog).not.toHaveTextContent("99 MB")
		})
	})

  it("offers only current OpenAI catalog models to quick test while keeping the field editable", async () => {
    window.history.replaceState(null, "", "#quick-test")
    const user = userEvent.setup()
    const client = desktopClient()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.models.push({
      id: "22222222-2222-4222-8222-222222222299",
      revision: 1,
      name: "seedance-video-model",
      protocol: "seedance",
      capabilities: ["video"],
    })
    vi.mocked(client.getCatalog).mockResolvedValue(catalog)

    render(<App client={client} />)

    const modelID = await screen.findByLabelText("模型 ID")
    expect(modelID).toHaveAttribute("aria-autocomplete", "list")
    await user.click(modelID)
    const options = await screen.findByRole("listbox")
    expect(within(options).getByRole("option", { name: "gpt-5.2" })).toBeInTheDocument()
    expect(within(options).queryByRole("option", { name: "seedance-video-model" })).not.toBeInTheDocument()
  })

	it("shows request-level report detail and all Go export actions", async () => {
		window.history.replaceState(null, "", "#reports")
		const client = desktopClient()
		const summary = FIXTURE_REPORTS.reports[0]
		vi.mocked(client.getReportDetail).mockResolvedValue({
			schema_version: 1,
			source: "run",
			report: {
				id: summary.id, run_id: summary.run_id!, run_status: summary.run_status, generated_at: summary.generated_at,
				model: { id: "22222222-2222-4222-8222-222222222221", name: summary.model_name },
				channel: { id: "33333333-3333-4333-8333-333333333331", name: summary.channel_name },
				environment: { os: "windows", arch: "amd64", region: "local", network_egress: "direct", app_version: "test", engine_version: "go-core-v1" },
				conclusion: { passed: true, verdict: summary.verdict, issues: [] },
				sla: {}, metrics: { e2e_p95_ms: { value: 123, unit: "ms", samples: 1 } }, case_results: [],
			},
			request_results: [{
				id: "99999999-9999-4999-8999-999999999991", request_id: "request-1",
				success: { transport: true, protocol: true, semantic: true, sla: true },
				metrics: { e2e_ms: 123, ttft_ms: 40, tpot_ms: 10, schedule_lag_ms: 1, prompt_tokens: 10, completion_tokens: 3 },
			}],
		})

		render(<App client={client} />)
		await userEvent.click(await screen.findByRole("button", { name: `查看报告：${summary.verdict}` }))
		expect(await screen.findByRole("table", { name: "请求级结果" })).toHaveTextContent("request-1")
		for (const label of ["JSON", "HTML", "PNG", "PDF", "复制 PNG"]) {
			expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
		}
		expect(screen.getByText(/e2e_p95_ms/)).toBeInTheDocument()
	})

	it("refreshes archived quick-performance reports and opens the selected report charts", async () => {
		window.history.replaceState(null, "", "#quick-test")
		const user = userEvent.setup()
		const client = desktopClient()
		client.workspace.active_run_id = undefined
		client.workspace.runs = client.workspace.runs.map((run) => ["queued", "starting", "running", "draining"].includes(run.status) ? { ...run, status: "completed" as const } : run)
		const reportID = "77777777-7777-4777-8777-777777777771"
		const performance = archivedPerformanceReport(reportID)
		const updatedReports = structuredClone(FIXTURE_REPORTS)
		updatedReports.reports.push({
			id: reportID,
			source: "quick_performance",
			generated_at: performance.generated_at!,
			run_status: "completed",
			plan_name: "快速性能测试",
			model_name: "gpt-new",
			channel_name: "api.example.test",
			passed: true,
			verdict: "快速性能测试通过",
			issue_count: 0,
			case_count: 1,
			failed_case_count: 0,
			attachment_count: 0,
		})
		vi.mocked(client.getReports)
			.mockResolvedValueOnce(structuredClone(FIXTURE_REPORTS))
			.mockResolvedValue(structuredClone(updatedReports))
		vi.mocked(client.runQuickPerformanceTest).mockResolvedValue(performance)
		vi.mocked(client.getReportDetail).mockResolvedValue({ schema_version: 1, source: "quick_performance", performance })

		render(<App client={client} />)
		await user.type(await screen.findByLabelText("接口地址"), "https://api.example.test/v1")
		await user.type(screen.getByLabelText("API Key"), "sk-private-value")
		await user.type(screen.getByLabelText("模型 ID"), "gpt-new")
		await user.keyboard("{Escape}")
		await user.click(await screen.findByRole("button", { name: "快速性能测试" }))
		const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
		await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

		await waitFor(() => expect(client.getReports).toHaveBeenCalledTimes(2))
		await user.click(await within(dialog).findByRole("button", { name: "查看正式报告" }))
		expect(window.location.hash).toBe("#reports")
		expect(await screen.findByRole("region", { name: "归档性能报告" })).toHaveTextContent("1 / 1")
		expect(client.getReportDetail).toHaveBeenCalledWith(reportID)
	})

  it("creates, updates, and confirms deletion for catalog models", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    expect(screen.getByRole("heading", { name: "新增模型" })).toBeInTheDocument()
    await user.type(screen.getByLabelText("模型名称"), "gpt-next")
    await user.type(screen.getByLabelText("模型能力"), "chat, tools")
    await user.click(screen.getByRole("button", { name: "保存模型" }))

    expect(client.createModel).toHaveBeenCalledWith({
      name: "gpt-next",
      protocol: "openai-chat",
      capabilities: ["chat", "tools"],
    })

    await user.click(screen.getByRole("button", { name: "编辑模型" }))
    const nameInput = screen.getByLabelText("模型名称")
    await user.clear(nameInput)
    await user.type(nameInput, "gpt-5.2 edited")
    await user.click(screen.getByRole("button", { name: "保存模型" }))
    expect(client.updateModel).toHaveBeenCalledWith(
      expect.objectContaining({
        id: FIXTURE_CATALOG.models[0].id,
        expected_revision: FIXTURE_CATALOG.models[0].revision,
        name: "gpt-5.2 edited",
      }),
    )

    await user.click(screen.getByRole("button", { name: "删除模型" }))
    expect(screen.getByRole("alertdialog")).toHaveTextContent("历史版本与已停用的 ID 会保留")
    await user.click(screen.getByRole("button", { name: "确认删除模型" }))
    expect(client.deleteModel).toHaveBeenCalledWith({
      id: FIXTURE_CATALOG.models[0].id,
      expected_revision: FIXTURE_CATALOG.models[0].revision,
    })
    expect(vi.mocked(client.getWorkspace).mock.calls.length).toBeGreaterThanOrEqual(4)
  })

  it("identifies the catalog form and field when local validation blocks saving", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    await user.click(screen.getByRole("button", { name: "保存模型" }))

    const dialog = screen.getByRole("dialog", { name: "新增模型" })
    const name = within(dialog).getByLabelText("模型名称")
    expect(await within(dialog).findByText("请输入模型名称。")).toHaveAttribute("data-slot", "field-error")
    expect(name).toHaveAttribute("aria-invalid", "true")
    expect(name.closest('[data-slot="field"]')).toHaveAttribute("data-invalid", "true")
    expect(within(dialog).getByText("模型名称", { selector: "label" })).toHaveAttribute("for", name.id)
    expect(name).toHaveFocus()

    await user.type(name, "gpt-friendly-errors")
    expect(name).not.toHaveAttribute("aria-invalid")
    expect(within(dialog).queryByText("请输入模型名称。")).not.toBeInTheDocument()
  })

  it("identifies the catalog form when its backend save is rejected", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    const client = desktopClient()
    vi.mocked(client.createModel).mockRejectedValueOnce(
      new DesktopClientError("catalog_revision_conflict"),
    )
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    await user.type(screen.getByLabelText("模型名称"), "gpt-conflict")
    await user.click(screen.getByRole("button", { name: "保存模型" }))

    const dialog = screen.getByRole("dialog", { name: "新增模型" })
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      "新增模型保存失败：对象版本已变化或仍被引用，请刷新并解除引用后重试",
    )
    expect(dialog.querySelector('[data-slot="field-group"]')).not.toHaveAttribute("aria-invalid")
  })

  it("identifies an invalid channel service address before saving", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("tab", { name: /渠道/ }))
    await user.click(screen.getByRole("button", { name: "新增渠道" }))
    const dialog = screen.getByRole("dialog", { name: "新增渠道" })
    await user.type(within(dialog).getByLabelText("渠道名称"), "缺少主机名的渠道")
    await user.type(within(dialog).getByLabelText("API Key"), "sk-private")
    await user.click(within(dialog).getByRole("button", { name: "保存渠道" }))

    const address = within(dialog).getByLabelText("服务地址")
    expect(await within(dialog).findByText("请输入包含主机名的 http:// 或 https:// 服务地址。")).toHaveAttribute("data-slot", "field-error")
    expect(address).toHaveAttribute("aria-invalid", "true")
    expect(address).toHaveFocus()
    expect(client.createChannel).not.toHaveBeenCalled()
  })

  it("identifies both missing plan stop conditions", async () => {
    window.history.replaceState(null, "", "#plans")
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "测试计划" })
    await user.click(screen.getByRole("button", { name: "新增计划" }))
    const dialog = screen.getByRole("dialog", { name: "新增计划" })
    await user.type(within(dialog).getByLabelText("计划名称"), "缺少停止条件")
    const requestCount = within(dialog).getByLabelText("请求数")
    await user.clear(requestCount)
    await user.type(requestCount, "0")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))

    expect(await within(dialog).findByText("请求数和持续时间不能同时为 0。")).toHaveAttribute("data-slot", "field-error")
    expect(requestCount).toHaveAttribute("aria-invalid", "true")
    expect(requestCount).toHaveFocus()
    expect(client.createPlan).not.toHaveBeenCalled()

    await user.type(within(dialog).getByLabelText("持续时间毫秒"), "100")
    expect(within(dialog).queryByText("请求数和持续时间不能同时为 0。")).not.toBeInTheDocument()
  })

  it("treats a committed catalog refresh failure as saved and closes the editor", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    await user.type(screen.getByLabelText("模型名称"), "gpt-committed")
    vi.mocked(client.createModel).mockRejectedValueOnce(
      new DesktopClientError("catalog_saved_refresh_failed"),
    )
    vi.mocked(client.getCatalog).mockRejectedValueOnce(new Error("refresh still unavailable"))

    await user.click(screen.getByRole("button", { name: "保存模型" }))

    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "新增模型" })).not.toBeInTheDocument()
    })
    expect(client.createModel).toHaveBeenCalledTimes(1)
    expect(client.getCatalog).toHaveBeenCalledTimes(2)
    expect(screen.getByRole("alert")).toHaveTextContent(
      "已保存，但目录刷新失败，请刷新或重新打开应用",
    )
    expect(screen.getAllByText(FIXTURE_CATALOG.models[0].name).length).toBeGreaterThan(0)
    expect(screen.queryByText(/保存未完成/)).not.toBeInTheDocument()
  })

  it("exposes CRUD entry points for mappings, cases, suites, and plans", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "运行工作区" })
    await user.click(screen.getByRole("button", { name: "模型与渠道" }))
    await user.click(screen.getByRole("tab", { name: /映射/ }))
    expect(screen.getByRole("button", { name: "新增映射" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "编辑映射" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "删除映射" })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "用例" }))
    expect(screen.getByRole("button", { name: "新增用例" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "编辑用例" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "删除用例" })).toBeInTheDocument()
    await user.click(screen.getByRole("tab", { name: /套件/ }))
    expect(screen.getByRole("button", { name: "新增套件" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "编辑套件" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "删除套件" })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "计划" }))
    expect(screen.getByRole("button", { name: "新增计划" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "编辑计划" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "删除计划" })).toBeInTheDocument()
  })

  it("keeps a rejected catalog deletion visible as a workspace error", async () => {
    window.history.replaceState(null, "", "#catalog")
    const user = userEvent.setup()
    const client = desktopClient()
    vi.mocked(client.deleteModel).mockRejectedValueOnce(new Error("foreign key constraint"))
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "删除模型" }))
    await user.click(screen.getByRole("button", { name: "确认删除模型" }))
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "删除模型失败：目录操作失败，请检查对象是否仍被引用",
    )
  })

  it("preserves pinned case revisions during a suite name edit", async () => {
    window.history.replaceState(null, "", "#cases")
    const user = userEvent.setup()
    const client = desktopClient()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.test_cases[0].revision = catalog.suites[0].cases[0].revision + 5
    vi.mocked(client.getCatalog).mockResolvedValue(catalog)
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "测试用例" })
    await user.click(screen.getByRole("tab", { name: /套件/ }))
    await user.click(screen.getByRole("button", { name: "编辑套件" }))
    const name = screen.getByLabelText("套件名称")
    await user.clear(name)
    await user.type(name, "仅重命名套件")
    await user.click(screen.getByRole("button", { name: "保存套件" }))
    expect(client.updateSuite).toHaveBeenCalledWith(expect.objectContaining({
      name: "仅重命名套件",
      cases: catalog.suites[0].cases,
    }))
  })

  it("renders a suite's fixed cases as a scannable list", async () => {
    window.history.replaceState(null, "", "#cases")
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await screen.findByRole("heading", { name: "测试用例" })
    await user.click(screen.getByRole("tab", { name: /套件/ }))

    const inspector = screen.getByRole("complementary", { name: "套件详情" })
    const fixedCases = within(inspector).getByRole("list", { name: "固定用例" })
    expect(within(fixedCases).getAllByRole("listitem")).toHaveLength(4)
    expect(fixedCases).toHaveTextContent("基础对话")
    expect(fixedCases).toHaveTextContent("JSON 模式")
    expect(fixedCases).toHaveTextContent("工具调用")
    expect(fixedCases).toHaveTextContent("流式结束")
  })

  it("keeps internal case revisions out of the suite's fixed-case list", async () => {
    window.history.replaceState(null, "", "#cases")
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await screen.findByRole("heading", { name: "测试用例" })
    await user.click(screen.getByRole("tab", { name: /套件/ }))

    const fixedCases = screen.getByRole("list", { name: "固定用例" })
    expect(fixedCases).not.toHaveTextContent(/r\d+/)
  })

  it("keeps the suite's fixed-case list inside a bounded scroll region", async () => {
    window.history.replaceState(null, "", "#cases")
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await screen.findByRole("heading", { name: "测试用例" })
    await user.click(screen.getByRole("tab", { name: /套件/ }))

    const fixedCases = screen.getByRole("list", { name: "固定用例" })
    expect(fixedCases.closest('[data-slot="scroll-area-viewport"]')).not.toBeNull()
  })

  it("shows a clear empty state when a suite has no fixed cases", async () => {
    window.history.replaceState(null, "", "#cases")
    const user = userEvent.setup()
    const client = desktopClient()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.suites[0].cases = []
    catalog.suites[0].case_count = 0
    vi.mocked(client.getCatalog).mockResolvedValue(catalog)
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "测试用例" })
    await user.click(screen.getByRole("tab", { name: /套件/ }))

    const inspector = screen.getByRole("complementary", { name: "套件详情" })
    expect(within(inspector).getByText("暂无固定用例")).toBeInTheDocument()
    expect(within(inspector).queryByRole("list", { name: "固定用例" })).not.toBeInTheDocument()
  })

  it("distinguishes imported automatic, disabled, and manual cases", async () => {
    window.history.replaceState(null, "", "#cases")
    const client = desktopClient()
    const catalog = structuredClone(FIXTURE_CATALOG)
    Object.assign(catalog.test_cases[0], {
      key: "T001",
      dimension: "must",
      enabled: true,
      default: true,
      severity: "critical",
      execution_mode: "automatic",
    })
    Object.assign(catalog.test_cases[1], {
      key: "disabled.case",
      dimension: "compatibility",
      enabled: false,
      default: false,
      severity: "critical",
      execution_mode: "automatic",
    })
    Object.assign(catalog.test_cases[2], {
      key: "T010",
      dimension: "manual",
      model_targets: ["kimi-k3", "kimi-k2.6"],
      enabled: true,
      default: false,
      severity: "normal",
      execution_mode: "manual",
    })
    vi.mocked(client.getCatalog).mockResolvedValue(catalog)

    render(<App client={client} />)

    const table = await screen.findByRole("table", { name: "测试用例目录" })
    expect(within(table).getByText("默认启用")).toBeInTheDocument()
    expect(within(table).getByText("已停用")).toBeInTheDocument()
    expect(within(table).getByText("人工判定")).toBeInTheDocument()
    expect(within(table).getByText("kimi-k3 · kimi-k2.6")).toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "查看用例 工具调用" }))
    const inspector = screen.getByRole("complementary", { name: "用例详情" })
    expect(inspector).toHaveTextContent("T010")
    expect(inspector).toHaveTextContent("人工判定")
  })

  it("keeps internal case revisions out of the test case workspace", async () => {
    window.history.replaceState(null, "", "#cases")
    const client = desktopClient()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.test_cases[0].revision = 137
    vi.mocked(client.getCatalog).mockResolvedValue(catalog)

    render(<App client={client} />)

    expect(await screen.findByText("维护请求、期望与断言")).toBeInTheDocument()
    const table = screen.getByRole("table", { name: "测试用例目录" })
    expect(within(table).queryByRole("columnheader", { name: "版本" })).not.toBeInTheDocument()
    expect(within(table).queryByText("r137")).not.toBeInTheDocument()

    const inspector = screen.getByRole("complementary", { name: "用例详情" })
    expect(within(inspector).getByText("协议")).toBeInTheDocument()
    expect(inspector).not.toHaveTextContent("r137")
  })

  it("restores the selected workspace from hash navigation", async () => {
    window.history.replaceState(null, "", "#reports")
    render(<App client={desktopClient()} />)

    expect(
      await screen.findByRole("heading", { name: "测试报告" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "报告" })).toHaveAttribute(
      "aria-current",
      "page",
    )
  })

  it("keeps table selection and the inspector on the same run", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await user.click(await screen.findByRole("button", { name: "查看 JSON 模式回归" }))

    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("JSON 模式回归")
    expect(inspector).toHaveTextContent("55555555-5555-4555-8555-555555555552")
  })

  it("opens an explicitly titled new-run sheet", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await user.click(await screen.findByRole("button", { name: "新建运行" }))

    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    expect(dialog).toBeInTheDocument()
    expect(within(dialog).getByText("选择测试计划")).toBeInTheDocument()
    expect(
      within(dialog).getByRole("button", { name: "开始运行" }),
    ).toBeEnabled()

    const radios = within(dialog).getAllByRole("radio")
    expect(radios.filter((radio) => radio.tabIndex === 0)).toHaveLength(1)
    expect(radios[0]).toBeChecked()
    radios[0].focus()
    fireEvent.keyDown(radios[0], { key: "ArrowDown" })
    await waitFor(() => expect(radios[1]).toBeChecked())
    fireEvent.keyUp(radios[1], { key: "ArrowDown" })

    await user.click(within(dialog).getByRole("button", { name: "开始运行" }))
		expect(client.startRunTarget).toHaveBeenCalledWith({
			plan_id: FIXTURE_WORKSPACE.plans[1].id,
			model_id: FIXTURE_CATALOG.plans[1].model_ids[0],
			channel_id: FIXTURE_CATALOG.plans[1].channel_ids[0],
		})
  })

  it("identifies the selected plan when creating a run fails", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
		vi.mocked(client.startRunTarget).mockRejectedValueOnce(
      new DesktopClientError("run_commands_unavailable"),
    )
    render(<App client={client} />)

    await user.click(await screen.findByRole("button", { name: "新建运行" }))
    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    await user.click(within(dialog).getByRole("button", { name: "开始运行" }))

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "创建运行（计划：营销文案基准）失败：运行命令暂不可用",
    )
  })

  it("identifies the report and format when an export fails", async () => {
    window.history.replaceState(null, "", "#reports")
    const user = userEvent.setup()
    const client = desktopClient()
    vi.mocked(client.exportReport).mockRejectedValueOnce(
      new DesktopClientError("reports_unavailable"),
    )
    render(<App client={client} />)

    await screen.findByRole("heading", { name: "测试报告" })
    await user.click(screen.getByRole("button", { name: "JSON" }))

    expect(await screen.findByText(
      "导出 JSON 报告（兼容性门禁通过）失败：无法读取测试报告，请重试；日志操作名：load_reports",
    )).toBeInTheDocument()
  })

  it("treats stop-sending and cancel as separate lifecycle actions", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    expect(await screen.findByText("发送中", { selector: "[data-task-state]" })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "停止发送" }))
    expect(screen.getByText("排空中", { selector: "[data-task-state]" })).toBeInTheDocument()
    expect(client.stopSending).toHaveBeenCalledWith(FIXTURE_WORKSPACE.active_run_id)
    expect(screen.getByRole("button", { name: "取消运行" })).toBeEnabled()

    await user.click(screen.getByRole("button", { name: "取消运行" }))
    expect(client.cancelRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.active_run_id)
    expect(screen.getByText("无活动运行")).toBeInTheDocument()
  })

  it("persists only the versioned theme preference", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    const systemTheme = await screen.findByRole("button", {
      name: "界面设置，主题：跟随系统",
    })
    await user.click(systemTheme)
    await user.click(screen.getByRole("menuitemradio", { name: "深色" }))

    expect(document.documentElement).toHaveClass("dark")
    expect(document.documentElement).toHaveAttribute("data-theme", "dark")
    expect(document.documentElement.style.colorScheme).toBe("dark")
    expect(
      JSON.parse(
        window.localStorage.getItem("llm-test-studio:ui-preferences:v1") ?? "null",
      ),
    ).toEqual({ version: 1, theme: "dark" })
    expect(window.localStorage).toHaveLength(1)
  })

  it("switches the complete shell language and persists the preference", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    const settings = await screen.findByRole("button", {
      name: "界面设置，主题：跟随系统",
    })
    await user.click(settings)
    await user.click(screen.getByRole("menuitemradio", { name: "English" }))

    expect(
      await screen.findByRole("navigation", { name: "Main navigation" }),
    ).toHaveTextContent("Overview")
    expect(screen.getByRole("heading", { name: "Run workspace" })).toBeInTheDocument()
    expect(screen.getByRole("table", { name: "Run records" })).toBeInTheDocument()
    expect(document.documentElement).toHaveAttribute("lang", "en-US")
    expect(
      JSON.parse(
        window.localStorage.getItem("llm-studio:language-preference:v1") ??
          "null",
      ),
    ).toEqual({ version: 1, language: "en-US" })
    expect(
      screen.getByRole("button", {
        name: "Interface settings, theme: System",
      }),
    ).toBeInTheDocument()
  })

  it("switches overview and diagnostics content with the selected language", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await user.click(
      await screen.findByRole("button", {
        name: "界面设置，主题：跟随系统",
      }),
    )
    await user.click(screen.getByRole("menuitemradio", { name: "English" }))
    await user.click(screen.getByRole("button", { name: "Overview" }))

    expect(
      await screen.findByRole("heading", { name: "Workspace overview" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("region", { name: "Local object summary" })).toHaveTextContent(
      "6 models",
    )

    await user.click(screen.getByRole("button", { name: "Diagnostics" }))
    const dialog = await screen.findByRole("dialog", { name: "Diagnostics" })
    expect(dialog).toHaveTextContent("Structured logging enabled")
    expect(dialog).toHaveTextContent("5 retained files")
    expect(within(dialog).getByRole("button", { name: "Open log folder" })).toBeInTheDocument()

    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "Models & Channels" }))
    expect(await screen.findByRole("heading", { name: "Models & Channels" })).toBeInTheDocument()
    expect(screen.getByRole("table", { name: "Model catalog" })).toBeInTheDocument()

    for (const [navigation, heading, table] of [
      ["Quick Test", "Quick Test", null],
      ["Cases", "Test Cases", "Test case catalog"],
      ["Plans", "Test Plans", "Test plan catalog"],
      ["Reports", "Test Reports", "Test report catalog"],
      ["Runs", "Run workspace", "Run records"],
    ] as const) {
      await user.click(screen.getByRole("button", { name: navigation }))
      expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument()
      if (table) expect(screen.getByRole("table", { name: table })).toBeInTheDocument()
      if (navigation === "Reports") expect(screen.getByRole("main")).toHaveClass("min-w-0")
    }
  })

  it("keeps the native window chrome aligned with the theme preference", async () => {
    const user = userEvent.setup()
    const runtime = {
      WindowSetSystemDefaultTheme: vi.fn(),
      WindowSetLightTheme: vi.fn(),
      WindowSetDarkTheme: vi.fn(),
    }
    Object.defineProperty(window, "runtime", {
      configurable: true,
      value: runtime,
    })

    render(<App client={desktopClient()} />)

    const themeTrigger = await screen.findByRole("button", {
      name: "界面设置，主题：跟随系统",
    })
    expect(runtime.WindowSetSystemDefaultTheme).toHaveBeenCalledTimes(1)

    await user.click(themeTrigger)
    await user.click(screen.getByRole("menuitemradio", { name: "深色" }))
    expect(runtime.WindowSetDarkTheme).toHaveBeenCalledTimes(1)

    await user.click(
      screen.getByRole("button", { name: "界面设置，主题：深色" }),
    )
    await user.click(screen.getByRole("menuitemradio", { name: "浅色" }))
    expect(runtime.WindowSetSystemDefaultTheme).toHaveBeenCalledTimes(1)
    expect(runtime.WindowSetDarkTheme).toHaveBeenCalledTimes(1)
    expect(runtime.WindowSetLightTheme).toHaveBeenCalledTimes(1)
  })

  it("surfaces a missing production bridge instead of substituting fixtures", async () => {
    const client: DesktopClient = {
      getDiagnostics: vi.fn(),
      openDiagnosticsDirectory: vi.fn(),
      getWorkspace: vi.fn(async () => {
        throw new Error("sk-secret from https://provider.example/v1")
      }),
      getCatalog: vi.fn(),
      getReports: vi.fn(),
			getReportDetail: vi.fn(),
			exportReport: vi.fn(),
			saveReportExport: vi.fn(),
			copyReportPNG: vi.fn(),
			getComparisons: vi.fn(),
      startRun: vi.fn(),
			startRunTarget: vi.fn(),
			startQuickTask: vi.fn(),
			getQuickTask: vi.fn(),
    rememberQuickTaskCredential: vi.fn(async () => undefined), forgetQuickTaskCredential: vi.fn(async () => undefined),
      stopSending: vi.fn(),
      cancelRun: vi.fn(),
			startComparison: vi.fn(),

			runQuickPerformanceTest: vi.fn(),

      ...catalogMutationMocks(),
    }

    render(<App client={client} />)

    const alert = await screen.findByRole("alert")
    expect(alert).toHaveTextContent("无法读取本地工作区")
    expect(alert).not.toHaveTextContent("sk-secret")
    expect(alert).not.toHaveTextContent("provider.example")
    expect(screen.queryByText("营销文案基准")).not.toBeInTheDocument()
  })

	it("identifies the failed workspace load and lets the user retry it", async () => {
		const user = userEvent.setup()
		const client = desktopClient()
		vi.mocked(client.getWorkspace)
			.mockRejectedValueOnce(new DesktopClientError("workspace_unavailable"))
			.mockResolvedValue(structuredClone(client.workspace))

		render(<App client={client} />)

		const alert = await screen.findByRole("alert")
		expect(alert).toHaveTextContent(
			"无法读取运行工作区，请重试；若仍失败，请查看日志中的 load_workspace 记录",
		)
		await user.click(within(alert).getByRole("button", { name: "重试打开" }))

		expect(
			await screen.findByRole("heading", { name: "运行工作区" }),
		).toBeInTheDocument()
		expect(client.getWorkspace).toHaveBeenCalledTimes(2)
	})

  it("retries a failed report query after the last running task reaches its terminal state", async () => {
    const client = desktopClient()
    const terminal = { ...client.workspace, active_run_id: undefined, runs: client.workspace.runs.map((run) => ({ ...run, status: "completed" as const })) }
    vi.mocked(client.getWorkspace).mockResolvedValueOnce(client.workspace).mockResolvedValue(terminal)
    vi.mocked(client.getReports).mockResolvedValueOnce(FIXTURE_REPORTS).mockRejectedValueOnce(new Error("temporary report error")).mockResolvedValue(FIXTURE_REPORTS)
    render(<App client={client} />)
    await waitFor(() => expect(vi.mocked(client.getReports).mock.calls.length).toBeGreaterThanOrEqual(3), { timeout: 3500 })
  })

  it("preserves quick task drafts across navigation and reload while keeping credentials in memory only", async () => {
    window.history.replaceState(null, "", "#quick-test")
    const user = userEvent.setup()
    const client = desktopClient()
    const rendered = render(<App client={client} />)
    await user.type(await screen.findByLabelText("接口地址"), "https://example.test")
    await user.type(screen.getByLabelText("API Key"), "test-private-key")
    await user.type(screen.getByLabelText("模型 ID"), "draft-model")
    await user.keyboard("{Escape}")
    await user.clear(screen.getByLabelText("测试消息"))
    await user.type(screen.getByLabelText("测试消息"), "draft prompt")
    await user.click(screen.getByRole("button", { name: "模型与渠道" }))
    await screen.findByRole("heading", { name: "模型与渠道" })
    await user.click(screen.getByRole("button", { name: "快速测试" }))
    expect(await screen.findByLabelText("测试消息")).toHaveValue("draft prompt")
    expect(screen.getByLabelText("API Key")).toHaveValue("test-private-key")
    expect(JSON.stringify(localStorage)).not.toContain("test-private-key")
    rendered.unmount()
    render(<App client={client} />)
    expect(await screen.findByLabelText("测试消息")).toHaveValue("draft prompt")
    expect(screen.getByLabelText("API Key")).toHaveValue("")
    expect(client.startQuickTask).not.toHaveBeenCalled()
  })

  it("uses the Core conclusion and never infers pass from request counts", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[2], { conclusion: "none" })

    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 多轮工具调用",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText("已完成")).toBeInTheDocument()
    expect(within(row as HTMLTableRowElement).queryByText("通过")).not.toBeInTheDocument()
  })

  it("clears the inspector and renders an explicit filtered empty state", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    client.workspace.plans.push({
      ...client.workspace.plans[0],
      id: "11111111-1111-4111-8111-111111111199",
      name: "空计划",
      run_count: 0,
    })
    render(<App client={client} />)

    await user.click(await screen.findByRole("button", { name: /空计划/ }))

    expect(screen.getByText("这个计划还没有运行记录")).toBeInTheDocument()
    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("尚未选择运行")
    expect(inspector).not.toHaveTextContent("营销文案基准")
  })

  it("presents duration-only load without a zero-request target", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[0], {
      load_mode: "open_loop",
      rate_per_second: 12,
      planned: 0,
      duration_ms: 60_000,
      conclusion: "none",
    })
    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 营销文案基准",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText(/目标 01:00/)).toBeInTheDocument()
    expect(within(row as HTMLTableRowElement).queryByText("82/0")).not.toBeInTheDocument()

    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("目标时长 01:00")
    expect(inspector).toHaveTextContent("82 个请求已完成")
    expect(inspector).not.toHaveTextContent("0 个请求已固定")
    expect(inspector).not.toHaveTextContent("82/0 已完成")
  })

  it("stops indeterminate animation when a duration-only run is terminal", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[0], {
      load_mode: "open_loop",
      rate_per_second: 12,
      planned: 0,
      duration_ms: 60_000,
      completed: 712,
      passed: 712,
      failed: 0,
      status: "completed",
      conclusion: "passed",
    })
    client.workspace.active_run_id = undefined
    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 营销文案基准",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText("712 个已完成")).toBeInTheDocument()
    expect(row?.querySelector('[data-slot="spinner"]')).toBeNull()
  })

  it("ships a blocking theme bootstrap before the React entrypoint", () => {
    const bootstrap = indexHtml.indexOf("llm-test-studio:ui-preferences:v1")
    const entrypoint = indexHtml.indexOf('/src/main.tsx')

    expect(bootstrap).toBeGreaterThan(-1)
    expect(bootstrap).toBeLessThan(entrypoint)
    expect(indexHtml).toContain("data-theme")
    expect(indexHtml).toContain("colorScheme")
  })

  it("ships a blocking language bootstrap before the React entrypoint", () => {
    const bootstrap = indexHtml.indexOf("llm-studio:language-preference:v1")
    const entrypoint = indexHtml.indexOf('/src/main.tsx')

    expect(bootstrap).toBeGreaterThan(-1)
    expect(bootstrap).toBeLessThan(entrypoint)
    expect(indexHtml).toContain("navigator.languages")
    expect(indexHtml).toContain('root.setAttribute("lang", locale)')
  })

  it("uses the exact town semantic tokens and the Contrast system icon", () => {
    for (const token of [
      "--foreground: rgba(17,24,39,.92)",
      "--card: rgba(255,255,255,.94)",
      "--muted-foreground: rgba(17,24,39,.56)",
      "--border: rgba(15,23,42,.12)",
      "--foreground: rgba(255,255,255,.88)",
      "--card: rgba(31,31,31,.92)",
      "--muted-foreground: rgba(255,255,255,.55)",
      "--border: rgba(255,255,255,.10)",
    ]) {
      expect(indexCss).toContain(token)
    }

    expect(indexCss).not.toContain("--foreground: #252a31")
    expect(indexCss).not.toContain("--foreground: #e8eaed")
  })
})
