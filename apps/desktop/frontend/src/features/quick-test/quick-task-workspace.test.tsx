import { render, screen, waitFor } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { useState } from "react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { FIXTURE_CATALOG, FIXTURE_WORKSPACE, FIXTURE_REPORTS } from "@/features/runs/fixtures"
import { createTaskDraft, type TaskDraft } from "./task-draft"
import { QuickTaskWorkspace } from "./quick-task-workspace"

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

function setup(
  initial = createTaskDraft(task),
  refresh = vi.fn(async () => {}),
  workspace = FIXTURE_WORKSPACE,
) {
  const actions = {
    startQuickTask: vi.fn(async () => FIXTURE_WORKSPACE.runs[0].id),
    getQuickTask: vi.fn(),
    rememberQuickTaskCredential: vi.fn(async () => undefined),
    forgetQuickTaskCredential: vi.fn(async () => undefined),
    cancelRun: vi.fn(async () => FIXTURE_WORKSPACE),
    runQuickPerformanceTest: vi.fn(),
  }
  function Harness() {
    const [draft, setDraft] = useState<TaskDraft>(initial)
    const [runID, setRunID] = useState("")
    return (
      <QuickTaskWorkspace
        catalog={{
          ...FIXTURE_CATALOG,
          suites: [task, FIXTURE_CATALOG.suites[1]],
        }}
        workspace={workspace}
        reports={FIXTURE_REPORTS}
        draft={draft}
        onDraftChange={setDraft}
        runID={runID}
        onRunSelected={setRunID}
        actions={actions}
        refresh={refresh}
        onWorkspaceUpdated={vi.fn()}
        onOpenReport={vi.fn()}
        onPerformanceArchived={vi.fn()}
      />
    )
  }
  render(
    <I18nextProvider i18n={createAppI18n("zh-CN")}>
      <Harness />
    </I18nextProvider>,
  )
  return actions
}

describe("Suite quick task workspace", () => {
  it("renders typed Suite inputs, focuses missing fields, and sends a temporary video task", async () => {
    const user = userEvent.setup()
    const actions = setup()
    expect(screen.getByLabelText("时长")).toHaveValue(4)
    expect(screen.getByRole("checkbox", { name: "音频" })).not.toBeChecked()
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    expect(screen.getByLabelText("接口地址")).toHaveFocus()
    expect(actions.startQuickTask).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText("接口地址"), "https://example.test")
    await user.type(screen.getByLabelText("API Key"), "private-key")
    await user.type(screen.getByLabelText("模型 ID"), "video-model")
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("checkbox", { name: "音频" }))
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    await waitFor(() =>
      expect(actions.startQuickTask).toHaveBeenCalledExactlyOnceWith({
        suite_id: task.id,
        seed: 1, request_timeout_ms: 60000,
        model: "video-model",
        base_url: "https://example.test",
        api_key: "private-key",
        inputs: { prompt: "cat", duration: 4, audio: true },
      }),
    )
    expect(screen.queryByRole("button", { name: "快速性能测试" })).not.toBeInTheDocument()
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
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    expect(await screen.findByText(/测试已启动，进度暂时无法刷新/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "刷新进度" }))
    expect(actions.startQuickTask).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText("视频提示词")).toHaveValue("cat")
    expect(document.body).not.toHaveTextContent("private refresh error")
  })
  it.each(["passed", "failed"] as const)(
    "restores %s history and resubmits its pinned definition with edits",
    async (conclusion) => {
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
        schema_version: 2, seed: 1, request_timeout_ms: 60000,
        run_id: run.id,
        suite: historicalTask,
        model: "past-model",
        base_url: "https://example.test",
        inputs: { prompt: "past prompt", duration: 8, audio: true },
      })
      await user.click(screen.getByRole("button", { name: "填入并编辑 Past video task" }))
      expect(await screen.findByText(/已恢复历史任务版本/)).toBeInTheDocument()
      expect(screen.getByLabelText("时长")).toHaveValue(8)
      expect(screen.getByLabelText("API Key")).toHaveValue("private-key")
      await user.type(screen.getByLabelText("视频提示词"), " edited")
      await user.click(screen.getByRole("button", { name: "开始测试" }))
      expect(actions.startQuickTask).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          seed: 1, request_timeout_ms: 60000,
          source_run_id: run.id,
          inputs: { prompt: "past prompt edited", duration: 8, audio: true },
        }),
      )
    },
  )

  it("uses a saved channel without sending an exposed credential", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const channel = FIXTURE_CATALOG.channels.find(
      (channel) => channel.protocol === selected.protocol && channel.credential_configured,
    )!
    const actions = setup({
      ...createTaskDraft(selected),
      channel_id: channel.id,
      model: "upstream",
      api_key: "stale-key",
    })
    expect(screen.getByLabelText("API Key")).toBeDisabled()
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    expect(actions.startQuickTask).toHaveBeenCalledExactlyOnceWith({
      suite_id: selected.id,
      seed: 1, request_timeout_ms: 60000,
      channel_id: channel.id,
      model: "upstream",
      inputs: { prompt: "hello" },
    })
  })

  it("keeps Suite identity when opening performance testing without a preliminary request", async () => {
    const user = userEvent.setup()
    const selected = FIXTURE_CATALOG.suites[1]
    const actions = setup({
      ...createTaskDraft(selected),
      base_url: "https://example.test",
      api_key: "private-key",
      model: "model",
    })
    actions.runQuickPerformanceTest.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("button", { name: "快速性能测试" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.startQuickTask).not.toHaveBeenCalled()
    expect(actions.runQuickPerformanceTest).toHaveBeenCalledWith(
      expect.objectContaining({
        task: { suite_id: selected.id, suite_revision: selected.revision },
        url: "https://example.test",
        api_key: "private-key",
      }),
      expect.any(Function),
    )
    await screen.findByText(/快速性能测试暂不可用/)
  })

  it("blocks a deleted saved channel and focuses its replacement control", async () => {
    const user = userEvent.setup()
    const actions = setup({
      ...createTaskDraft(task),
      channel_id: "123e4567-e89b-42d3-a456-426614174099",
      model: "model",
    })
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    expect(screen.getByRole("combobox", { name: "从渠道填充" })).toHaveFocus()
    expect(actions.startQuickTask).not.toHaveBeenCalled()
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
    await user.click(screen.getByRole("button", { name: "开始测试" }))
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
    await user.click(screen.getByRole("button", { name: "开始测试" }))
    expect(actions.startQuickTask).toHaveBeenLastCalledWith(
      expect.objectContaining({ credential_run_id: FIXTURE_WORKSPACE.runs[0].id }),
    )
    expect(actions.startQuickTask).not.toHaveBeenLastCalledWith(
      expect.objectContaining({ api_key: expect.any(String) }),
    )
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
    expect(actions.startQuickTask).not.toHaveBeenCalled()
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
    actions.runQuickPerformanceTest.mockRejectedValue(new Error("fixture only"))
    await user.click(screen.getByRole("button", { name: "快速性能测试" }))
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    expect(actions.runQuickPerformanceTest).toHaveBeenCalledWith(
      expect.objectContaining({ credential_run_id: FIXTURE_WORKSPACE.runs[0].id, api_key: "" }),
      expect.any(Function),
    )
    await screen.findByText(/快速性能测试暂不可用/)
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())
    await user.type(screen.getByLabelText("接口地址"), "/other")
    expect(screen.getByLabelText("API Key")).toBeEnabled()
    expect(screen.queryByRole("button", { name: "忘记已保存的密钥" })).not.toBeInTheDocument()
  })
})
