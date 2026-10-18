import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { useState } from "react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { FIXTURE_CATALOG, FIXTURE_WORKSPACE, FIXTURE_REPORTS } from "@/features/runs/fixtures"
import { createTaskDraft, type TaskDraft } from "./task-draft"
import { QuickTaskWorkspace, type QuickTaskJobEntry } from "./quick-task-workspace"
import { executionClient, jobFixture } from "@/test/jobs-fixture"
import type { CatalogSuite } from "@/features/catalog/data"
import { FILE_CATALOG, FILE_SELECTION, FILE_SUITE } from "@/test/local-file-fixture"
import type { QuickPerformanceCommand } from "./data"

const task = {
  ...FIXTURE_CATALOG.suites[0],
  name: "Video connectivity",
  protocol: "seedance" as const,
    description: "Run video",
    inputs: [
      {
        key: "prompt",
        label: "视频提示词",
        type: "string" as const,
        default: "cat",
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "prompt" }],
      },
      {
        key: "duration",
        label: "时长",
        type: "number" as const,
        default: 4,
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "duration" }],
      },
      {
        key: "audio",
        label: "音频",
        type: "boolean" as const,
        default: false,
        bindings: [{ case_id: FIXTURE_CATALOG.test_cases[0].id, input: "audio" }],
      },
    ],
}

const archivedPerformanceID = "77777777-7777-4777-8777-777777777771"
async function archivedPerformance() {
  const command: QuickPerformanceCommand = {
    protocol: "openai-chat", address_mode: "base_url", url: "https://previous.test/v1", api_key: "", model_id: "past-performance-model",
    load_mode: "fixed_concurrency", arrival_pattern: "constant", workload_mode: "fixed",
    request_count: 23, duration_ms: 0, concurrency: 3, rate_per_second: 1, max_in_flight: 64,
    timeout_ms: 9000, input_tokens: 32, output_tokens: 43, random_input: true, random_seed: 5,
    input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
    warmup_requests: 2, ramp_duration_ms: 0, ramp_request_cap: 1000, slice_duration_ms: 0,
    slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
    capacity_enabled: false, capacity_start: 1, capacity_step: 1,
  }
  const { protocol, address_mode, url, api_key: _apiKey, model_id, ...profile } = command
  return { protocol, address_mode, base_url: url, endpoint: `${url}/chat/completions`, model_id,
    profile, archived: true, archive_status: "archived", report_id: archivedPerformanceID }
}
const archivedPerformanceSummary = {
  ...FIXTURE_REPORTS.reports[0], id: archivedPerformanceID, run_id: undefined,
  source: "quick_performance" as const, plan_name: "Saved performance test",
}

function setup(
  initial = createTaskDraft(task),
  refresh = vi.fn(async () => {}),
  workspace = FIXTURE_WORKSPACE,
  catalog = FIXTURE_CATALOG,
  reports = FIXTURE_REPORTS,
  selectedTask: CatalogSuite = task,
) {
  const actions = {
    ...executionClient(),
    getAvailableReports: vi.fn(async (_ids: string[]): Promise<string[]> => []),
    getQuickTask: vi.fn(),
    getReportDetail: vi.fn(),
    rememberQuickTaskCredential: vi.fn(async () => undefined),
    forgetQuickTaskCredential: vi.fn(async () => undefined),
    createLocalFileSelection: vi.fn(async () => FILE_SELECTION),
    releaseLocalFileSelection: vi.fn(async () => undefined),
  }
  const observed = jobFixture({ kind: "run", run_ids: [FIXTURE_WORKSPACE.runs[0].id], report_ids: [] })
  actions.launchJob.mockResolvedValue({ ...observed, execution: null })
  actions.getJob.mockResolvedValue(observed)
  const onSaveChannel = vi.fn(async () => ({ catalog, channel_id: catalog.channels[0].id }))
  const onOpenReport = vi.fn()
  const onJobChange = vi.fn()
  const onOpenExecution = vi.fn()
  function Harness({ currentReports = reports, currentWorkspace = workspace, visible = true }: {
    currentReports?: typeof reports; currentWorkspace?: typeof workspace; visible?: boolean
  }) {
    const [draft, setDraft] = useState<TaskDraft>(initial)
    const [runID, setRunID] = useState("")
    const [functionalJobs, setFunctionalJobs] = useState<QuickTaskJobEntry[]>([])
    if (!visible) return null
    return (
      <QuickTaskWorkspace
        catalog={{
          ...catalog,
          suites: catalog.suites.length ? [selectedTask, catalog.suites[1]] : [],
        }}
        workspace={currentWorkspace}
        reports={currentReports}
        draft={draft}
        onDraftChange={setDraft}
        runID={runID}
        onRunSelected={setRunID}
        actions={actions}
        functionalJobs={functionalJobs}
        onFunctionalJobsChange={setFunctionalJobs}
        onJobChange={onJobChange}
        onOpenExecution={onOpenExecution}
        refresh={refresh}
        onOpenReport={onOpenReport}
        onPerformanceArchived={vi.fn()}
        onSaveChannel={onSaveChannel}
      />
    )
  }
  const view = render(
    <I18nextProvider i18n={createAppI18n("zh-CN")}>
      <Harness />
    </I18nextProvider>,
  )
  const updateReports = (currentReports: typeof reports) => view.rerender(
    <I18nextProvider i18n={createAppI18n("zh-CN")}><Harness currentReports={currentReports} /></I18nextProvider>,
  )
  const updateWorkspace = (currentWorkspace: typeof workspace) => view.rerender(
    <I18nextProvider i18n={createAppI18n("zh-CN")}><Harness currentWorkspace={currentWorkspace} /></I18nextProvider>,
  )
  const setVisible = (visible: boolean) => view.rerender(
    <I18nextProvider i18n={createAppI18n("zh-CN")}><Harness visible={visible} /></I18nextProvider>,
  )
  return { ...actions, onSaveChannel, onOpenReport, onJobChange, onOpenExecution, updateReports, updateWorkspace, setVisible }
}

describe("Suite quick task workspace", () => {
  it("unlocks on acceptance and keeps consecutive jobs while refresh is still pending", async () => {
    const user = userEvent.setup()
    let finishRefresh!: () => void
    const refreshing = new Promise<void>(resolve => { finishRefresh = resolve })
    const refresh = vi.fn(() => refreshing)
    const actions = setup({ ...createTaskDraft(task), base_url: "https://example.test", api_key: "private-key", model: "model" },
      refresh, { ...FIXTURE_WORKSPACE, runs: [] }, undefined, { schema_version: 1, reports: [] })
    const jobs = [1, 2, 3].map(index => jobFixture({ kind: "run", job_id: `a1111111-1111-4111-8111-11111111111${index}`, run_ids: [], report_ids: [] }))
    actions.getJob.mockImplementation(async id => jobs.find(job => job.job_id === id)!)
    let accept!: (job: typeof jobs[number]) => void
    actions.launchJob.mockReturnValueOnce(new Promise(resolve => { accept = resolve }))
    const form = screen.getByRole("button", { name: "开启功能测试" }).closest("form")!
    fireEvent.submit(form)
    fireEvent.submit(form)
    await waitFor(() => expect(actions.launchJob).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText("模型 ID")).toBeDisabled()
    await act(async () => accept(jobs[0]))
    for (const job of jobs.slice(1)) {
      actions.launchJob.mockResolvedValueOnce(job)
      await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    }
    expect(actions.launchJob).toHaveBeenCalledTimes(3)
    expect(refresh).toHaveBeenCalledTimes(3)
    expect(screen.getByRole("button", { name: "开启功能测试" })).toBeEnabled()
    expect(screen.getByText("3 项运行中")).toBeVisible()
    const history = screen.getByRole("list", { name: "测试记录" })
    expect(within(history).getAllByRole("listitem")).toHaveLength(3)
    for (const job of jobs) expect(actions.getJob).toHaveBeenCalledWith(job.job_id)
    await act(async () => finishRefresh())
    expect(actions.launchJob).toHaveBeenCalledTimes(3)
  })

  it("observes every performance job independently after new submissions and form remounts", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(FIXTURE_CATALOG.suites[1]), base_url: "https://example.test", api_key: "private-key", model: "model-1" },
      undefined, { ...FIXTURE_WORKSPACE, runs: [] }, undefined, { schema_version: 1, reports: [] })
    const jobs = [1, 2, 3].map(index => jobFixture({ job_id: `a1111111-1111-4111-8111-11111111111${index}`, report_ids: [] }))
    actions.getJob.mockImplementation(async id => jobs.find(job => job.job_id === id)!)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    for (let index = 0; index < jobs.length; index++) {
      actions.launchJob.mockResolvedValueOnce(jobs[index])
      await user.clear(screen.getByLabelText("模型 ID"))
      await user.type(screen.getByLabelText("模型 ID"), `model-${index + 1}`)
      await user.keyboard("{Escape}")
      await user.click(screen.getByRole("button", { name: "开始性能测试" }))
      expect(screen.getByRole("button", { name: "开始性能测试" })).toBeEnabled()
    }
    const history = screen.getByRole("list", { name: "测试记录" })
    expect(within(history).getAllByRole("listitem")).toHaveLength(3)
    expect(screen.getByText("3 项运行中")).toBeVisible()
    // Restoring an active record remounts the parameter editor, not its observers.
    await user.click(within(history).getAllByRole("button", { name: "快速性能测试" })[2])
    expect(screen.getByLabelText("模型 ID")).toHaveValue("model-1")
    expect(actions.launchJob).toHaveBeenCalledTimes(3)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    expect(screen.getByRole("button", { name: "开启功能测试" })).toBeEnabled()
    jobs[0] = { ...jobs[0], execution: { ...jobs[0].execution!, status: "failed", revision: 2,
      failure: { phase: "execution", error_code: "request_failed" } } }
    await waitFor(() => expect(screen.getByText("2 项运行中")).toBeVisible(), { timeout: 4000 })
    const rows = within(history).getAllByRole("listitem")
    const failed = rows.find(row => row.textContent?.includes("model-1"))!
    expect(failed).toHaveTextContent("失败")
    expect(rows.find(row => row.textContent?.includes("model-2"))).toHaveTextContent("运行中")
    await user.click(within(failed).getByRole("button", { name: "执行详情 快速性能测试" }))
    await waitFor(() => expect(actions.onOpenExecution).toHaveBeenCalledWith(jobs[0]))
  }, 10_000)

  it("lists a remote functional submission before a local run exists and follows its execution", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(task), base_url: "https://example.test", api_key: "private-key", model: "remote-model" },
      undefined, { ...FIXTURE_WORKSPACE, runs: [] }, undefined, { schema_version: 1, reports: [] })
    const remote = jobFixture({ kind: "run", location: { kind: "remote", configuration_id: "remote-node" }, run_ids: [], report_ids: [] })
    const accepted = { ...remote, execution: null }
    actions.launchJob.mockResolvedValue(accepted)
    actions.getJob.mockResolvedValue(accepted)
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    const history = await screen.findByRole("list", { name: "测试记录" })
    expect(within(history).getByRole("button", { name: task.name })).toHaveAttribute("aria-current", "true")
    expect(within(history).getByText(/执行日志待同步/)).toBeInTheDocument()
    expect(screen.getByText("1 项运行中")).toBeInTheDocument()
    expect(screen.queryByText("执行编号")).not.toBeInTheDocument()
    const details = within(history).getByRole("button", { name: `执行详情 ${task.name}` })
    expect(details).toHaveTextContent("")
    await user.hover(details)
    expect(await screen.findByRole("tooltip", { name: "执行详情" })).toBeInTheDocument()
    await user.click(details)
    expect(actions.onOpenExecution).toHaveBeenCalledWith(accepted)
    expect(within(history).queryByRole("button", { name: `填入并编辑 ${task.name}` })).not.toBeInTheDocument()
    await user.click(within(history).getByRole("button", { name: task.name }))
    expect(actions.getQuickTask).not.toHaveBeenCalled()
    actions.getJob.mockResolvedValue(remote)
    await waitFor(() => expect(within(history).getByText("运行中")).toBeInTheDocument(), { timeout: 4000 })
    expect(actions.onJobChange).toHaveBeenCalledWith(remote)
    actions.getJob.mockResolvedValue({ ...remote, execution: { ...remote.execution!, status: "failed", revision: 2, latest_sequence: 2,
      failure: { phase: "execution", error_code: "request_failed" } } })
    await waitFor(() => expect(within(history).getByText("失败")).toBeInTheDocument(), { timeout: 4000 })
    expect(screen.getByText("0 项运行中")).toBeInTheDocument()
    expect(actions.launchJob).toHaveBeenCalledTimes(1)
  })

  it("retains submitted jobs across page remounts and observes them in performance mode", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(task), base_url: "https://example.test", api_key: "private-key", model: "remote-model" },
      undefined, { ...FIXTURE_WORKSPACE, runs: [] }, undefined, { schema_version: 1, reports: [] })
    const remote = jobFixture({ kind: "run", run_ids: [], report_ids: [] })
    actions.launchJob.mockResolvedValue(remote)
    actions.getJob.mockResolvedValue(remote)
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(await screen.findByText("1 项运行中")).toBeInTheDocument()
    actions.setVisible(false)
    actions.setVisible(true)
    const history = screen.getByRole("list", { name: "测试记录" })
    expect(within(history).getByText(task.name)).toBeInTheDocument()
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    actions.getJob.mockRejectedValue(new Error("private transport error"))
    await new Promise(resolve => setTimeout(resolve, 1600))
    expect(within(history).getByText("运行中")).toBeInTheDocument()
    expect(screen.getByText("1 项运行中")).toBeInTheDocument()
    expect(screen.queryByText("private transport error")).not.toBeInTheDocument()
    actions.getJob.mockResolvedValue({ ...remote, execution: { ...remote.execution!, status: "cancelled", revision: 2, latest_sequence: 2 } })
    await waitFor(() => expect(within(history).getByText("已取消")).toBeInTheDocument(), { timeout: 4000 })
    expect(screen.getByText("0 项运行中")).toBeInTheDocument()
    expect(actions.launchJob).toHaveBeenCalledTimes(1)
  })

  it("merges a synchronized run into its submitted job without duplicating history", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(task), base_url: "https://example.test", api_key: "private-key", model: "remote-model" },
      undefined, { ...FIXTURE_WORKSPACE, runs: [] }, undefined, { schema_version: 1, reports: [] })
    const run = { ...FIXTURE_WORKSPACE.runs[0], source: "quick_task" as const, plan_name: task.name, status: "running" as const, conclusion: "none" as const }
    const remote = jobFixture({ kind: "run", run_ids: [run.id], report_ids: [] })
    actions.launchJob.mockResolvedValue(remote)
    actions.getJob.mockResolvedValue(remote)
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    const history = await screen.findByRole("list", { name: "测试记录" })
    actions.updateWorkspace({ ...FIXTURE_WORKSPACE, runs: [run] })
    expect(within(history).getAllByRole("listitem")).toHaveLength(1)
    expect(within(history).getByRole("button", { name: `填入并编辑 ${task.name}` })).toBeEnabled()
    expect(screen.getByText("1 项运行中")).toBeInTheDocument()
  })

  it("opens execution details for historical runs without restoring or launching a test", async () => {
    const user = userEvent.setup()
    const run = { ...FIXTURE_WORKSPACE.runs[0], source: "quick_task" as const, plan_name: "Historical functional test" }
    const actions = setup(undefined, undefined, { ...FIXTURE_WORKSPACE, runs: [run] })
    await user.click(screen.getByRole("button", { name: `执行详情 ${run.plan_name}` }))
    expect(actions.onOpenExecution).toHaveBeenCalledWith(null, run.id)
    expect(actions.getQuickTask).not.toHaveBeenCalled()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("renders an explicit file declaration and blocks launch before and after image selection", async () => {
    const actions = setup({ ...createTaskDraft(FILE_SUITE), base_url: "https://example.test", api_key: "key", model: "model" },
      undefined, undefined, FILE_CATALOG, undefined, FILE_SUITE)
    const start = screen.getByRole("button", { name: "开启功能测试" })
    expect(start).toBeDisabled()
    fireEvent.submit(start.closest("form")!)
    expect(actions.launchJob).not.toHaveBeenCalled()
    expect(screen.getAllByText(/包含文件输入的任务暂不可运行/).length).toBeGreaterThan(0)
    const picker = screen.getByLabelText("测试图片")
    await userEvent.setup().upload(picker, new File([new Uint8Array([1, 2, 3])], "sample.png", { type: "image/png" }))
    await screen.findByText("已选择：image/png · 3 字节")
    expect(start).toBeDisabled()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("uses performance plans as tasks and keeps temporary parameter edits local", async () => {
    const user = userEvent.setup()
    const plan = FIXTURE_CATALOG.plans.find(item => item.protocol === "openai-chat" && item.performance?.random_input)!
    const original = structuredClone(plan.performance)
    const actions = setup({ ...createTaskDraft(FIXTURE_CATALOG.suites[1]), base_url: "https://example.test", api_key: "temporary-key", model: "model" })
    actions.launchJob.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    expect(screen.queryByRole("combobox", { name: "性能计划" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("combobox", { name: "测试任务" }))
    expect(screen.queryByRole("option", { name: /连通性测试/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole("option", { name: plan.name }))
    expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(500)
    expect(screen.getByLabelText("并发数", { exact: true })).toHaveValue(40)
    expect(screen.getByLabelText("时间切片（秒）")).toHaveValue(5)
    expect(screen.getByRole("checkbox", { name: "随机输入" })).toBeChecked()
    await user.clear(screen.getByLabelText("请求数", { exact: true }))
    await user.type(screen.getByLabelText("请求数", { exact: true }), "37")
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(performanceLaunch(expect.objectContaining({ request_count: 37, concurrency: 40, random_input: true })))
    expect(actions.launchJob.mock.calls[0][0].performance).not.toHaveProperty("task")
    expect(plan.performance).toEqual(original)
    await screen.findAllByText(/快速性能测试暂不可用/)
  })

  it("can configure a performance test when no functional suites exist", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(null), base_url: "https://example.test", api_key: "temporary-key", model: "model" }, undefined, undefined, { ...FIXTURE_CATALOG, suites: [] })
    actions.launchJob.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(10)
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(performanceLaunch(expect.objectContaining({ protocol: "openai-chat", request_count: 10 })))
    await screen.findAllByText(/快速性能测试暂不可用/)
  })
  it.each(["completed", "failed", "cancelled"] as const)(
    "opens the matching archived report from %s history without restoring the draft",
    async (status) => {
      const user = userEvent.setup()
      const report = FIXTURE_REPORTS.reports[1]
      const run = { ...FIXTURE_WORKSPACE.runs[0], id: report.run_id!, source: "quick_task" as const, status, plan_name: "Past quick test" }
      const actions = setup(undefined, undefined, { ...FIXTURE_WORKSPACE, runs: [run] })

      const open = screen.getByRole("button", { name: "打开报告 Past quick test" })
      open.focus()
      await user.keyboard("{Enter}")

      expect(actions.onOpenReport).toHaveBeenCalledExactlyOnceWith(report.id)
      expect(actions.getQuickTask).not.toHaveBeenCalled()
      expect(actions.launchJob).not.toHaveBeenCalled()
      expect(screen.getByRole("button", { name: "填入并编辑 Past quick test" })).toBeEnabled()
    },
  )

  it.each(["running", "completed"] as const)(
    "keeps %s history editable without offering an unavailable report",
    (status) => {
      const run = { ...FIXTURE_WORKSPACE.runs[0], source: "quick_task" as const, status, plan_name: "Unarchived quick test" }
      setup(undefined, undefined, { ...FIXTURE_WORKSPACE, runs: [run] }, undefined, { schema_version: 1, reports: [] })

      expect(screen.queryByRole("button", { name: "打开报告 Unarchived quick test" })).not.toBeInTheDocument()
      expect(screen.getByRole("button", { name: "填入并编辑 Unarchived quick test" })).toBeEnabled()
    },
  )

  it("rejects a protocol excluded by the saved model mapping", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.models[0].protocols = ["openai-chat", "openai-responses"]
    const mapping = catalog.channel_models[0]
    mapping.protocols = ["openai-responses"]
    const actions = setup({ ...createTaskDraft(catalog.suites[1]),
      channel_id: mapping.channel_id, model: mapping.upstream_model_name }, undefined, undefined, catalog)
    await userEvent.setup().click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.launchJob).not.toHaveBeenCalled()
    expect(screen.getByRole("combobox", { name: "模型 ID" })).toHaveAttribute("aria-invalid", "true")
  })

  it("accepts a mapped protocol supported by another alias for the selected protocol", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.models[0].protocols = ["openai-chat", "openai-responses"]
    const mapping = catalog.channel_models[0]
    mapping.protocols = ["openai-responses"]
    const alias = catalog.channel_models.find(value => value.channel_id === mapping.channel_id && value.id !== mapping.id)!
    alias.upstream_model_name = mapping.upstream_model_name
    alias.protocols = ["openai-chat"]

    const actions = setup({ ...createTaskDraft(catalog.suites[1]),
      channel_id: mapping.channel_id, model: mapping.upstream_model_name }, undefined, undefined, catalog)
    await userEvent.setup().click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(quickTaskLaunch(expect.objectContaining({
      channel_id: mapping.channel_id, model: mapping.upstream_model_name,
    })))
  })

  it("switches protocols before selecting tasks and updates the endpoint preview", async () => {
    const user = userEvent.setup()
    setup({ ...createTaskDraft(task), base_url: "https://api.example.test", model: "custom-model", api_key: "temporary-key" })
    await user.click(screen.getByRole("combobox", { name: "协议" }))
    await user.click(screen.getByRole("option", { name: "OpenAI Chat" }))
    expect(screen.getByRole("combobox", { name: "测试任务" })).toHaveValue(`${FIXTURE_CATALOG.suites[1].name} · ${FIXTURE_CATALOG.suites[1].case_count} 个用例`)
    expect(screen.getByLabelText("模型 ID")).toHaveValue("custom-model")
    expect(screen.getByLabelText("API Key")).toHaveValue("temporary-key")
    expect(document.querySelector('[data-slot="connection-url-suffix"]')).toHaveTextContent("/v1/chat/completions")
    await user.click(screen.getByRole("combobox", { name: "协议" }))
    await user.click(screen.getByRole("option", { name: "OpenAI Responses" }))
    expect(screen.getByText("暂无快速测试任务")).toBeInTheDocument()
    expect(screen.getByRole("combobox", { name: "协议" })).toHaveValue("OpenAI Responses")
    await user.click(screen.getByRole("combobox", { name: "协议" }))
    await user.click(screen.getByRole("option", { name: "Seedance" }))
    expect(screen.getByRole("combobox", { name: "测试任务" })).toHaveValue(`${task.name} · ${task.case_count} 个用例`)
  })
  it("keeps the gray completion outside the editable value and submits a full endpoint intact", async () => {
    const user = userEvent.setup()
    const actions = setup()
    const address = screen.getByLabelText("接口地址")
    const suffix = document.querySelector('[data-slot="connection-url-suffix"]')!
    await user.type(address, "https://example.test/api/v3/")
    expect(address).toHaveValue("https://example.test/api/v3/")
    expect(suffix).toHaveTextContent("contents/generations/tasks")
    expect(suffix.closest('[aria-hidden="true"]')).not.toBeNull()
    expect(address).toHaveAccessibleDescription(expect.stringContaining("灰色路径自动补全"))
    await user.keyboard("{End}{ArrowRight}{Delete}")
    expect(address).toHaveValue("https://example.test/api/v3/")
    expect(suffix).toHaveTextContent("contents/generations/tasks")
    await user.type(address, "contents/generations/tasks")
    expect(suffix).toHaveTextContent("")
    await user.type(screen.getByLabelText("API Key"), "test-key")
    await user.type(screen.getByLabelText("模型 ID"), "model")
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(quickTaskLaunch(expect.objectContaining({
      base_url: "https://example.test/api/v3/contents/generations/tasks",
    })))
  })

  it("validates the connection on save and opens the channel sheet without starting a test", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(task), seed: -1, request_timeout_seconds: 0 })
    const save = screen.getByRole("button", { name: "保存为渠道" })
    expect(save).toBeEnabled()

    await user.click(save)
    expect(screen.getByLabelText("接口地址")).toHaveFocus()
    expect(screen.getByLabelText("接口地址")).toHaveAttribute("aria-invalid", "true")
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()

    await user.type(screen.getByLabelText("接口地址"), "https://example.test")
    await user.click(save)
    expect(screen.getByLabelText("API Key")).toHaveFocus()
    expect(screen.getByLabelText("API Key")).toHaveAttribute("aria-invalid", "true")

    await user.type(screen.getByLabelText("API Key"), "temporary-key")
    await user.click(save)
    expect(screen.getByLabelText("模型 ID")).toHaveFocus()

    await user.type(screen.getByLabelText("模型 ID"), "custom-model")
    await user.keyboard("{Escape}")
    await user.click(save)
    expect(await screen.findByRole("dialog", { name: "保存为渠道" })).toBeInTheDocument()
    expect(actions.launchJob).not.toHaveBeenCalled()
    expect(actions.onSaveChannel).not.toHaveBeenCalled()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    expect(save).toHaveFocus()
  })

  it("groups short parameters and associates the concise concurrency hint", () => {
    setup()
    const concurrency = screen.getByRole("combobox", { name: "用例并发数" })
    const grid = concurrency.closest('[data-slot="field"]')!.parentElement!
    for (const control of [screen.getByLabelText("根随机种子"), screen.getByLabelText("用例超时（s）"), screen.getByLabelText("时长"), screen.getByRole("checkbox", { name: "音频" })]) {
      expect(control.closest('[data-slot="field"]')!.parentElement).toBe(grid)
    }
    expect(screen.getByLabelText("视频提示词").closest('[data-slot="field"]')).toHaveClass("col-span-full")
    const hint = document.getElementById(concurrency.getAttribute("aria-describedby")!)!
    expect(hint).toHaveTextContent("默认 4")
    expect(concurrency).toHaveAccessibleDescription(hint.textContent!)
  })

  it("renders typed Suite inputs, focuses missing fields, and sends a temporary video task", async () => {
    const user = userEvent.setup()
    const actions = setup()
    expect(screen.getByLabelText("时长")).toHaveValue(4)
    expect(screen.getByRole("checkbox", { name: "音频" })).not.toBeChecked()
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(screen.getByLabelText("接口地址")).toHaveFocus()
    expect(actions.launchJob).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText("接口地址"), "https://example.test")
    await user.type(screen.getByLabelText("API Key"), "private-key")
    await user.type(screen.getByLabelText("模型 ID"), "video-model")
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("checkbox", { name: "音频" }))
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    await waitFor(() =>
      expect(actions.launchJob).toHaveBeenCalledExactlyOnceWith(quickTaskLaunch({
        suite_id: task.id,
        seed: 1, request_timeout_seconds: 60, case_concurrency: 4,
        model: "video-model",
        base_url: "https://example.test",
        api_key: "private-key",
        inputs: { prompt: "cat", duration: 4, audio: true },
      })),
    )
    expect(screen.queryByRole("button", { name: "开始性能测试" })).not.toBeInTheDocument()
  })

  it("keeps acceptance and input values after a refresh failure without repeating the mutation", async () => {
    const user = userEvent.setup()
    const refresh = vi.fn(async () => {
      throw new Error("private refresh error")
    })
    const actions = setup(
      {
        ...createTaskDraft(task),
        base_url: "https://example.test",
        api_key: "private-key",
        model: "video-model",
      },
      refresh,
    )
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(await screen.findByText(/测试已启动，进度暂时无法刷新/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "刷新进度" }))
    expect(actions.launchJob).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText("视频提示词")).toHaveValue("cat")
    expect(document.body).not.toHaveTextContent("private refresh error")
  })
  it.each([["passed", "row"], ["failed", "keyboard"], ["passed", "edit"]] as const)(
    "restores %s history through %s activation and uses the current suite with edited inputs",
    async (conclusion, activation) => {
      const user = userEvent.setup()
      const run = {
        ...FIXTURE_WORKSPACE.runs[0],
        source: "quick_task" as const,
        plan_name: "Past video task",
        status: "completed" as const,
        conclusion,
      }
      const initial = {
        ...createTaskDraft(task),
        model: "model",
        base_url: "https://example.test",
        api_key: "private-key",
      }
      const actions = setup(
        initial,
        vi.fn(async () => {}),
        { ...FIXTURE_WORKSPACE, runs: [run] },
      )
      const historicalTask = { ...task, revision: 7 }
      actions.getQuickTask.mockResolvedValue({
        schema_version: 1, seed: 1, request_timeout_seconds: 60, case_concurrency: 2,
        run_id: run.id,
        suite: historicalTask,
        model: "past-model",
        base_url: "https://example.test",
        inputs: { prompt: "past prompt", duration: 8, audio: true },
      })
      const row = screen.getByRole("button", { name: "Past video task" })
      if (activation === "keyboard") { row.focus(); await user.keyboard("{Enter}") }
      else await user.click(activation === "row" ? row : screen.getByRole("button", { name: "填入并编辑 Past video task" }))
      expect(actions.getQuickTask).toHaveBeenCalledExactlyOnceWith(run.id)
      await waitFor(() => expect(screen.getByLabelText("时长")).toHaveValue(8))
      expect(screen.getByRole("combobox", { name: "测试任务" })).toHaveValue(`${task.name} · ${task.case_count} 个用例`)
      expect(screen.getByLabelText("时长")).toHaveValue(8)
      expect(screen.getByLabelText("API Key")).toHaveValue("private-key")
      await user.type(screen.getByLabelText("视频提示词"), " edited")
      await user.click(screen.getByRole("button", { name: "开启功能测试" }))
      expect(actions.launchJob).toHaveBeenCalledExactlyOnceWith(quickTaskLaunch(expect.objectContaining({
          seed: 1, request_timeout_seconds: 60, case_concurrency: 2,
          source_run_id: run.id,
          inputs: { prompt: "past prompt edited", duration: 8, audio: true },
        })))
    },
  )

  it("restores archived performance connection and load parameters from the row without running a test", async () => {
    const user = userEvent.setup()
    const performance = await archivedPerformance()
    const actions = setup({ ...createTaskDraft(task), base_url: "https://other.test", model: "other-model", api_key: "other-private-key" },
      undefined, undefined, undefined, { schema_version: 1, reports: [archivedPerformanceSummary] })
    actions.getReportDetail.mockResolvedValue({ schema_version: 1, source: "quick_performance", performance })
    const row = screen.getByRole("button", { name: "Saved performance test" })
    await user.click(row)
    await waitFor(() => expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(23))
    expect(actions.getReportDetail).toHaveBeenCalledExactlyOnceWith(archivedPerformanceID)
    expect(screen.getByRole("switch", { name: "切换测试模式" })).toBeChecked()
    expect(screen.getByLabelText("接口地址")).toHaveValue("https://previous.test/v1")
    expect(screen.getByLabelText("模型 ID")).toHaveValue("past-performance-model")
    expect(screen.getByLabelText("API Key")).toHaveValue("")
    expect(screen.getByLabelText("并发数", { exact: true })).toHaveValue(3)
    expect(screen.getByLabelText("单请求超时（秒）")).toHaveValue(9)
    expect(screen.getByRole("checkbox", { name: "随机输入" })).toBeChecked()
    await user.clear(screen.getByLabelText("请求数", { exact: true }))
    await user.type(screen.getByLabelText("请求数", { exact: true }), "99")
    await user.click(row)
    await waitFor(() => expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(23))
    await user.click(screen.getByRole("button", { name: "打开报告 Saved performance test" }))
    expect(actions.onOpenReport).toHaveBeenCalledExactlyOnceWith(archivedPerformanceID)
    expect(actions.getReportDetail).toHaveBeenCalledTimes(2)
    expect(actions.launchJob).not.toHaveBeenCalled()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("keeps the current draft when the requested performance report identity does not match", async () => {
    const user = userEvent.setup()
    const performance = await archivedPerformance()
    const actions = setup({ ...createTaskDraft(task), base_url: "https://unchanged.test", model: "unchanged-model", api_key: "private-key" },
      undefined, undefined, undefined, { schema_version: 1, reports: [archivedPerformanceSummary] })
    actions.getReportDetail.mockResolvedValue({ schema_version: 1, source: "quick_performance", performance: { ...performance, report_id: FIXTURE_REPORTS.reports[0].id } })
    await user.click(screen.getByRole("button", { name: "Saved performance test" }))
    expect(await screen.findByText("快速测试历史记录无效，请刷新后重试。")).toBeInTheDocument()
    expect(screen.getByLabelText("接口地址")).toHaveValue("https://unchanged.test")
    expect(screen.getByLabelText("模型 ID")).toHaveValue("unchanged-model")
    expect(screen.getByLabelText("API Key")).toHaveValue("private-key")
    expect(screen.getByRole("switch", { name: "切换测试模式" })).not.toBeChecked()
  })

  it("restores a local failed performance attempt without retaining its temporary key", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const actions = setup({ ...createTaskDraft(selected), base_url: "https://local-past.test", api_key: "private-original-key", model: "local-past-model" })
    const failed = jobFixture({ execution: { ...jobFixture().execution!, status: "failed", failure: { phase: "execution", error_code: "request_failed" } } })
    actions.launchJob.mockResolvedValue({ ...failed, execution: null })
    actions.getJob.mockResolvedValue(failed)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    await user.clear(screen.getByLabelText("请求数", { exact: true }))
    await user.type(screen.getByLabelText("请求数", { exact: true }), "37")
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    await waitFor(() => expect(screen.getByLabelText("API Key")).toBeEnabled())
    await user.clear(screen.getByLabelText("请求数", { exact: true }))
    await user.type(screen.getByLabelText("请求数", { exact: true }), "5")
    await user.type(screen.getByLabelText("接口地址"), "/other")
    await user.type(screen.getByLabelText("API Key"), "new-private-key")
    await user.click(screen.getByRole("button", { name: "快速性能测试" }))
    expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(37)
    expect(screen.getByLabelText("接口地址")).toHaveValue("https://local-past.test")
    expect(screen.getByLabelText("API Key")).toHaveValue("")
    expect(actions.getReportDetail).not.toHaveBeenCalled()
    expect(actions.launchJob).toHaveBeenCalledTimes(1)
  })

  it("uses a saved channel without sending an exposed credential", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const channel = FIXTURE_CATALOG.channels.find(
      (channel) => channel.credential_configured && FIXTURE_CATALOG.channel_models.some(mapping => mapping.channel_id === channel.id && mapping.protocols.includes(selected.protocol)),
    )!
    const actions = setup({
      ...createTaskDraft(selected),
      channel_id: channel.id,
      model: "upstream",
      api_key: "stale-key",
    })
    expect(screen.getByLabelText("API Key")).toBeDisabled()
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.launchJob).toHaveBeenCalledExactlyOnceWith(quickTaskLaunch({
      suite_id: selected.id,
      seed: 1, request_timeout_seconds: 60, case_concurrency: 4,
      channel_id: channel.id,
      model: "upstream",
      inputs: { prompt: "hello" },
    }))
  })

  it("runs performance independently from the functional Suite without a preliminary request", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const actions = setup({
      ...createTaskDraft(selected),
      base_url: "https://example.test",
      api_key: "private-key",
      model: "model",
    })
    actions.launchJob.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(performanceLaunch(expect.objectContaining({
        url: "https://example.test",
        api_key: "private-key",
      })))
    await screen.findAllByText(/快速性能测试暂不可用/)
  })

  it.each([
    { status: "completed" as const, passed: false, expected: "失败" },
    { status: "completed" as const, passed: true, expected: "通过" },
    { status: "failed" as const, passed: true, expected: "失败" },
    { status: "cancelled" as const, passed: true, expected: "已取消" },
  ])("shows the archived performance verdict after a $status Job (passed=$passed)", async ({ status, passed, expected }) => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(FIXTURE_CATALOG.suites[1]), base_url: "https://example.test", api_key: "private-key", model: "model" },
      undefined, undefined, undefined, { schema_version: 1, reports: [] })
    const completed = jobFixture({ performance_result_ready: true, report_ids: [archivedPerformanceID],
      execution: { ...jobFixture().execution!, status, revision: 2,
        failure: status === "failed" ? { phase: "execution", error_code: "request_failed" } : null } })
    actions.launchJob.mockResolvedValue({ ...completed, execution: null, performance_result_ready: false })
    actions.getJob.mockResolvedValue(completed)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    const list = screen.getByRole("list", { name: "测试记录" })
    const row = within(list).getByRole("button", { name: "快速性能测试" }).closest("li")!
    await waitFor(() => expect(row).toHaveTextContent("等待报告同步"))
    expect(within(row).queryByRole("button", { name: "打开报告 快速性能测试" })).not.toBeInTheDocument()
    actions.updateReports({ schema_version: 1, reports: [{ ...archivedPerformanceSummary,
      run_status: "completed", passed, failed_case_count: passed ? 0 : 2,
    }] })
    expect(within(row).getByRole("button", { name: "打开报告 快速性能测试" })).toBeInTheDocument()
    expect(row).not.toHaveTextContent("等待报告同步")
    expect(within(list).getByRole("button", { name: "快速性能测试" }).closest("li")).toBe(row)
    expect(within(row).getByText(expected, { exact: true })).toBeInTheDocument()
    expect(within(list).queryByRole("button", { name: "Saved performance test" })).not.toBeInTheDocument()
  })

  it("finds an imported performance archive outside the loaded history page", async () => {
    const user = userEvent.setup()
    const actions = setup({ ...createTaskDraft(FIXTURE_CATALOG.suites[1]), base_url: "https://example.test", api_key: "private-key", model: "model" },
      undefined, undefined, undefined, { schema_version: 1, reports: [] })
    const job = jobFixture({ performance_result_ready: true, report_ids: [archivedPerformanceID],
      execution: { ...jobFixture().execution!, status: "completed" } })
    actions.launchJob.mockResolvedValue(job)
    actions.getJob.mockResolvedValue(job)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    const list = screen.getByRole("list", { name: "测试记录" })
    await waitFor(() => expect(actions.getAvailableReports).toHaveBeenCalledWith([archivedPerformanceID]))
    expect(list).toHaveTextContent("等待报告同步")
    actions.getAvailableReports.mockResolvedValue([archivedPerformanceID])
    expect(await within(list).findByRole("button", { name: "打开报告 快速性能测试" }, { timeout: 4000 })).toBeVisible()
    expect(list).not.toHaveTextContent("等待报告同步")
  })

  it.each(["openai-chat", "seedance"])("keeps performance progress and failures when switching back to %s", async protocol => {
    const user = userEvent.setup()
    const selected = protocol === "seedance" ? task : FIXTURE_CATALOG.suites[1]
    const actions = setup({ ...createTaskDraft(selected), base_url: "https://example.test", api_key: "private-key", model: "model" })
    const running = jobFixture({ progress: { sequence: 2, recorded_at: "2026-10-16T00:00:00Z", progress: {
      phase: "sending", planned: 10, launched: 4, completed: 3, in_flight: 1,
      peak_in_flight: 2, succeeded: 2, failed: 1, rejected: 0,
      send_duration_ms: 10, drain_duration_ms: 0, total_duration_ms: 10,
    } } })
    actions.launchJob.mockResolvedValue({ ...running, execution: null })
    actions.getJob.mockResolvedValue(running)
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    if (protocol === "seedance") {
      await user.click(screen.getByRole("combobox", { name: "协议" }))
      await user.click(screen.getByRole("option", { name: "OpenAI Chat" }))
    }
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    const list = screen.getByRole("list", { name: "测试记录" })
    const row = within(list).getByRole("button", { name: "快速性能测试" }).closest("li")!
    await waitFor(() => expect(row).toHaveTextContent("3 / 10"))
    expect(row).toHaveTextContent("发送中")
    expect(row).toHaveTextContent("成功 2 · 失败 1 · 在途 1")
    expect(screen.getByLabelText("API Key")).toBeEnabled()
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    expect(screen.getByRole("button", { name: "开启功能测试" })).toBeEnabled()
    expect(within(row).getByRole("status")).toBeInTheDocument()
    actions.getJob.mockResolvedValue({ ...running, execution: { ...running.execution!, status: "failed", revision: 2,
      failure: { phase: "execution", error_code: "request_failed" } } })
    await waitFor(() => expect(row).toHaveTextContent("等待报告同步"), { timeout: 3500 })
    expect(within(row).queryByRole("progressbar")).not.toBeInTheDocument()
    expect(within(list).getByRole("button", { name: "快速性能测试" }).closest("li")).toBe(row)
    expect(row).toHaveTextContent("失败")
    expect(screen.getByLabelText("API Key")).toHaveValue("private-key")
    expect(screen.getByLabelText("API Key")).toBeEnabled()
    expect(actions.launchJob).toHaveBeenCalledTimes(1)
  })

  it("keeps both mode drafts and validates the shared connection before performance execution", async () => {
    const user = userEvent.setup()
    const actions = setup(createTaskDraft(FIXTURE_CATALOG.suites[1]))
    const mode = screen.getByRole("switch", { name: "切换测试模式" })
    mode.focus()
    await user.keyboard(" ")
    expect(mode).toBeChecked()
    await user.clear(screen.getByLabelText("请求数", { exact: true }))
    await user.type(screen.getByLabelText("请求数", { exact: true }), "17")
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.launchJob).not.toHaveBeenCalled()
    expect(screen.getByLabelText("接口地址")).toHaveAttribute("aria-invalid", "true")
    expect(screen.getByLabelText("接口地址")).toHaveFocus()
    await user.click(mode)
    await user.clear(screen.getByLabelText("用例超时（s）"))
    await user.type(screen.getByLabelText("用例超时（s）"), "25")
    await user.click(mode)
    expect(screen.getByLabelText("请求数", { exact: true })).toHaveValue(17)
    await user.click(mode)
    expect(screen.getByLabelText("用例超时（s）")).toHaveValue(25)
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("blocks a deleted saved channel and focuses its replacement control", async () => {
    const user = userEvent.setup()
    const actions = setup({
      ...createTaskDraft(task),
      channel_id: "123e4567-e89b-42d3-a456-426614174099",
      model: "model",
    })
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(screen.getByRole("combobox", { name: "从渠道填充" })).toHaveFocus()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })
  it("remembers a tested temporary connection explicitly, reuses its reference, and forgets it", async () => {
    const user = userEvent.setup()
    const actions = setup({
      ...createTaskDraft(task),
      base_url: "https://example.test",
      api_key: "temporary-key",
      model: "video-model",
    })
    expect(screen.queryByRole("button", { name: "记住此连接的密钥" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.rememberQuickTaskCredential).not.toHaveBeenCalled()
    await user.click(await screen.findByRole("button", { name: "记住此连接的密钥" }))
    expect(actions.rememberQuickTaskCredential).toHaveBeenCalledExactlyOnceWith({
      run_id: FIXTURE_WORKSPACE.runs[0].id,
      base_url: "https://example.test",
      protocol: "seedance",
      api_key: "temporary-key",
    })
    expect(screen.getByLabelText("API Key")).toHaveValue("")
    expect(screen.getByLabelText("API Key")).toBeDisabled()
    await user.click(screen.getByRole("button", { name: "开启功能测试" }))
    expect(actions.launchJob).toHaveBeenLastCalledWith(quickTaskLaunch(expect.objectContaining({ credential_run_id: FIXTURE_WORKSPACE.runs[0].id })))
    expect(actions.launchJob).not.toHaveBeenLastCalledWith(quickTaskLaunch(expect.objectContaining({ api_key: expect.any(String) })))
    await user.click(screen.getByRole("button", { name: "忘记已保存的密钥" }))
    expect(actions.forgetQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(
      FIXTURE_WORKSPACE.runs[0].id,
    )
    expect(screen.getByLabelText("API Key")).toBeEnabled()
  })

  it("keeps the entered key when remembering fails and never replays the Run", async () => {
    const user = userEvent.setup()
    const actions = setup({
      ...createTaskDraft(task),
      source_run_id: FIXTURE_WORKSPACE.runs[0].id,
      base_url: "https://example.test",
      api_key: "temporary-key",
      model: "model",
    })
    actions.rememberQuickTaskCredential.mockRejectedValueOnce(new Error("private OS error"))
    await user.click(screen.getByRole("button", { name: "记住此连接的密钥" }))
    expect(await screen.findByRole("alert")).not.toHaveTextContent("private OS error")
    expect(screen.getByLabelText("API Key")).toHaveValue("temporary-key")
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("reuses a remembered key for performance and drops it when the endpoint changes", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const actions = setup({
      ...createTaskDraft(selected),
      base_url: "https://example.test",
      credential_run_id: FIXTURE_WORKSPACE.runs[0].id,
      model: "model",
    })
    actions.launchJob.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("switch", { name: "切换测试模式" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(performanceLaunch(expect.objectContaining({ credential_run_id: FIXTURE_WORKSPACE.runs[0].id, api_key: "" })))
    await screen.findAllByText(/快速性能测试暂不可用/)
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    await user.type(screen.getByLabelText("接口地址"), "/other")
    expect(screen.getByLabelText("API Key")).toBeEnabled()
    expect(screen.queryByRole("button", { name: "忘记已保存的密钥" })).not.toBeInTheDocument()
  })
})

function quickTaskLaunch(quick_task: unknown) {
  return { location: { kind: "local", configuration_id: "" }, kind: "quick_task",
    plan: null, quick_task, performance: null, comparison: null }
}
function performanceLaunch(performance: unknown) {
  return { location: { kind: "local", configuration_id: "" }, kind: "performance",
    plan: null, quick_task: null, performance, comparison: null }
}
