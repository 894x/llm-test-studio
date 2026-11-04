import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { BUILTIN_CATALOG } from "@/features/catalog/builtin-catalog.fixture"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { FIXTURE_CATALOG, FIXTURE_WORKSPACE } from "@/test/workspace-fixtures"
import { controlFixture, executionClient, jobFixture } from "@/test/jobs-fixture"
import type { JobView } from "@/features/jobs/data"
import type { WorkspaceRun } from "@/features/runs/data"
import { PlanTestWorkspace } from "./plan-test-workspace"

const ladder = BUILTIN_CATALOG.plans.find(plan => plan.protocol === "openai-chat" && plan.name.includes("输入 Token 阶梯"))!
const custom = { ...ladder, id: "12121212-1212-4212-8212-121212121212", builtin: false, name: "自建完整计划",
  entries: ladder.entries.slice(0, 2).map(entry => ({ ...entry, on_failure: "stop" as const })), entry_count: 2 }
const catalog: CatalogSnapshot = { ...FIXTURE_CATALOG, plans: [ladder, custom] }
const model = catalog.models[0]
const channel = catalog.channels[0]
const upstreamModel = catalog.channel_models.find(item => item.channel_id === channel.id && item.model_id === model.id)!.upstream_model_name
const savedRun: WorkspaceRun = { ...FIXTURE_WORKSPACE.runs[0], source: undefined,
  plan_id: ladder.id, plan_name: "历史阶梯计划", model_id: model.id, model_name: model.name,
  channel_id: channel.id, channel_name: channel.name, status: "completed", conclusion: "passed",
  report_id: "34343434-3434-4434-8434-343434343434" }

function setup(runs: WorkspaceRun[] = [], options: Partial<React.ComponentProps<typeof PlanTestWorkspace>> = {}) {
  const job = jobFixture({ kind: "run", report_ids: [], run_ids: [] })
  const actions = {
    ...executionClient(),
    getRunHistory: vi.fn(async (_after: string) => ({ schema_version: 1 as const, runs, next_cursor: "" })),
    getRunSummaries: vi.fn(async (ids: string[]) => ({ schema_version: 1 as const, runs: runs.filter(run => ids.includes(run.id)), next_cursor: "" })),
    getAvailableReports: vi.fn(async () => []),
    getReportGeneration: vi.fn(async () => ({ schema_version: 1 as const, runs: [] })),
    subscribeRemoteResults: vi.fn(() => () => {}),
  }
  actions.launchJob.mockResolvedValue(job)
  actions.getJob.mockResolvedValue(job)
  const onOpenReport = vi.fn()
  const onOpenExecution = vi.fn()
  const loadPlans = vi.fn(async () => {})
  const view = render(<I18nextProvider i18n={createAppI18n("zh-CN")}>
    <PlanTestWorkspace catalog={catalog} modeControl={<span>模式选择</span>} actions={actions}
      loadPlans={loadPlans} onOpenReport={onOpenReport} onOpenExecution={onOpenExecution} {...options} />
  </I18nextProvider>)
  return { ...actions, onOpenReport, onOpenExecution, loadPlans, ...view }
}

async function select(user: ReturnType<typeof userEvent.setup>, label: string, name: string | RegExp) {
  await user.click(screen.getByRole("combobox", { name: label }))
  await user.click(screen.getByRole("option", { name }))
}
async function configure(user: ReturnType<typeof userEvent.setup>, name = ladder.name) {
  await select(user, "测试计划", new RegExp(name))
  await select(user, "从渠道填充", channel.name)
  await user.clear(screen.getByRole("combobox", { name: "模型 ID" }))
  await user.type(screen.getByRole("combobox", { name: "模型 ID" }), upstreamModel)
  await user.keyboard("{Escape}")
}

describe("plan test workspace", () => {
  it("filters plan suggestions by protocol and clears the old plan when the protocol changes", async () => {
    const user = userEvent.setup()
    const responsePlan = BUILTIN_CATALOG.plans.find(plan => plan.protocol === "openai-responses" && plan.name.includes("输入 Token 阶梯"))!
    setup([], { catalog: { ...catalog, plans: [ladder, responsePlan] } })
    await user.click(screen.getByRole("combobox", { name: "测试计划" }))
    expect(screen.getAllByRole("option")).toHaveLength(1)
    expect(screen.getByRole("option")).toHaveTextContent("OpenAI Chat")
    await user.click(screen.getByRole("option"))
    await select(user, "协议", "OpenAI Responses")
    expect(screen.getByRole("button", { name: "开始计划测试" })).toBeDisabled()
    expect(screen.queryByRole("region", { name: "执行顺序" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("combobox", { name: "测试计划" }))
    expect(screen.getAllByRole("option")).toHaveLength(1)
    expect(screen.getByRole("option")).toHaveTextContent("OpenAI Responses")
  })

  it("keeps dropdown arrows on ready plan and channel selectors", () => {
    setup([], { catalogResources: {
      connections: { ready: true, failed: false, retry: vi.fn() },
      plans: { ready: true, failed: false, retry: vi.fn() },
    } })
    for (const name of ["测试计划", "从渠道填充"]) {
      const field = screen.getByRole("combobox", { name }).closest('[data-slot="field"]')!
      expect(within(field as HTMLElement).getByRole("button", { name: "显示候选" })).toBeInTheDocument()
    }
  })

  it("accepts a temporary address, free-form model and API key without saving a catalog target", async () => {
    const user = userEvent.setup()
    const actions = setup([], { catalog: { ...catalog, models: [], channels: [], channel_models: [] } })
    await select(user, "测试计划", new RegExp(ladder.name))
    await user.type(screen.getByRole("textbox", { name: "接口地址" }), "https://temporary.test/v1/chat/completions")
    await user.type(screen.getByRole("combobox", { name: "模型 ID" }), "temporary-upstream")
    await user.keyboard("{Escape}")
    await user.type(screen.getByLabelText("API Key"), "session-only-secret")
    await user.click(screen.getByRole("button", { name: "开始计划测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(expect.objectContaining({ kind: "plan", plan: {
      plan_id: ladder.id, model_id: "", channel_id: "", connection: {
        protocol: "openai-chat", model: "temporary-upstream", base_url: "https://temporary.test/v1/chat/completions", api_key: "session-only-secret",
      },
    } }))
    // Changing connections and restoring a receipt must not restore its API key.
    await user.clear(screen.getByRole("textbox", { name: "接口地址" }))
    await user.type(screen.getByRole("textbox", { name: "接口地址" }), "https://other.test/v1")
    await user.type(screen.getByLabelText("API Key"), "different-key")
    await user.click(screen.getByRole("button", { name: ladder.name }))
    expect(screen.getByRole("textbox", { name: "接口地址" })).toHaveValue("https://temporary.test/v1/chat/completions")
    expect(screen.getByLabelText("API Key")).toHaveValue("")
  })

  it("focuses invalid manual connection fields before attempting to launch", async () => {
    const user = userEvent.setup()
    const actions = setup()
    await select(user, "测试计划", new RegExp(ladder.name))
    await user.click(screen.getByRole("button", { name: "开始计划测试" }))
    expect(screen.getByRole("textbox", { name: "接口地址" })).toHaveAttribute("aria-invalid", "true")
    expect(screen.getByRole("textbox", { name: "接口地址" })).toHaveFocus()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("previews all ten built-in token steps and launches the entire plan at the configured executor", async () => {
    const user = userEvent.setup()
    const actions = setup()
    actions.getRemoteNodes.mockResolvedValue({ schema_version: 1, nodes: [], default_revision: 1, default: { kind: "remote", configuration_id: channel.id } })
    const before = structuredClone(ladder)
    await configure(user)
    const preview = screen.getByRole("region", { name: "执行顺序" })
    expect(within(preview).getAllByRole("listitem")).toHaveLength(10)
    expect(within(preview).getByText("512000 / 300")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "开始计划测试" }))
    await waitFor(() => expect(actions.launchJob).toHaveBeenCalledExactlyOnceWith({
      kind: "plan", plan: { plan_id: ladder.id, model_id: "", channel_id: "", connection: {
        protocol: ladder.protocol, model: upstreamModel, channel_id: channel.id,
      } },
      location: { kind: "remote", configuration_id: channel.id }, quick_task: null, performance: null, comparison: null,
    }))
    expect(ladder).toEqual(before)
    expect(screen.getByRole("button", { name: "开始计划测试" })).toBeEnabled()
    expect(within(screen.getByRole("list", { name: "测试记录" })).getByRole("button", { name: ladder.name })).toBeInTheDocument()
  })

  it("runs a custom multi-entry plan and preserves its per-entry failure policies", async () => {
    const user = userEvent.setup()
    const actions = setup()
    await configure(user, custom.name)
    const preview = screen.getByRole("region", { name: "执行顺序" })
    expect(within(preview).getAllByRole("listitem")).toHaveLength(2)
    expect(within(preview).getAllByText(/失败后停止/)).toHaveLength(2)
    await user.click(screen.getByRole("button", { name: "开始计划测试" }))
    expect(actions.launchJob).toHaveBeenCalledWith(expect.objectContaining({ kind: "plan", plan: {
      plan_id: custom.id, model_id: "", channel_id: "", connection: {
        protocol: custom.protocol, model: upstreamModel, channel_id: channel.id,
      },
    } }))
  })

  it("blocks duplicate submissions only until the receipt and retains independent accepted jobs", async () => {
    const user = userEvent.setup()
    const actions = setup()
    let resolve!: (job: JobView) => void
    actions.launchJob.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await configure(user)
    const button = screen.getByRole("button", { name: "开始计划测试" })
    fireEvent.submit(button.closest("form")!)
    fireEvent.submit(button.closest("form")!)
    await waitFor(() => expect(actions.launchJob).toHaveBeenCalledTimes(1))
    expect(screen.getByRole("button", { name: "正在启动…" })).toBeDisabled()
    await act(async () => resolve(jobFixture({ kind: "run", report_ids: [], run_ids: [] })))
    expect(button).toBeEnabled()
    const second = jobFixture({ kind: "run", job_id: "45454545-4545-4545-8545-454545454545", report_ids: [], run_ids: [] })
    actions.launchJob.mockResolvedValueOnce(second)
    await user.click(button)
    await waitFor(() => expect(within(screen.getByRole("list", { name: "测试记录" })).getAllByRole("button", { name: ladder.name })).toHaveLength(2))
  })

  it("restores existing plan selections and opens their original report and execution", async () => {
    const user = userEvent.setup()
    const actions = setup([savedRun])
    await user.click(await screen.findByRole("button", { name: savedRun.plan_name }))
    expect((screen.getByRole("combobox", { name: "测试计划" }) as HTMLInputElement).value).toContain(ladder.name)
    expect(screen.getByRole("button", { name: "开始计划测试" })).toBeEnabled()
    await user.click(screen.getByRole("button", { name: `打开报告 ${savedRun.plan_name}` }))
    expect(actions.onOpenReport).toHaveBeenCalledWith(savedRun.report_id)
    await user.click(screen.getByRole("button", { name: `执行详情 ${savedRun.plan_name}` }))
    await waitFor(() => expect(actions.onOpenExecution).toHaveBeenCalled())
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("requires replacement of deleted plans without substituting a different plan for a historical run", async () => {
    const user = userEvent.setup()
    const actions = setup([{ ...savedRun, plan_id: "56565656-5656-4656-8656-565656565656" }])
    await user.click(await screen.findByRole("button", { name: savedRun.plan_name }))
    expect(screen.getByText("原计划已不可用，请选择其他计划。")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "开始计划测试" })).toBeDisabled()
    expect(actions.launchJob).not.toHaveBeenCalled()
  })

  it("keeps loading distinct from an empty catalog and offers a failed-load retry", async () => {
    const retry = vi.fn(async () => {})
    setup([], { catalog: { ...catalog, plans: [] }, catalogResources: {
      plans: { ready: false, failed: true, retry }, connections: { ready: true, failed: false, retry },
    } })
    expect(screen.queryByText("暂无可用计划")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "开始计划测试" })).toBeDisabled()
    await userEvent.click(screen.getByRole("button", { name: "重试加载测试计划" }))
    expect(retry).toHaveBeenCalledOnce()
  })

  it("cancels an accepted plan using its confirmed revision and preserves pending-control feedback", async () => {
    const user = userEvent.setup()
    const actions = setup()
    actions.controlJob.mockResolvedValue(controlFixture({ kind: "cancel", observation: "unreachable" }))
    await configure(user)
    await user.click(screen.getByRole("button", { name: "开始计划测试" }))
    const cancel = await screen.findByRole("button", { name: `取消测试 ${ladder.name}` })
    await waitFor(() => expect(cancel).toBeEnabled())
    await user.click(cancel)
    expect(actions.controlJob).toHaveBeenCalledWith({ job_id: jobFixture().job_id, kind: "cancel", expected_revision: 1 })
    expect(cancel).toBeDisabled()
  })
})
