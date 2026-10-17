import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { I18nextProvider } from "react-i18next"
import { createAppI18n } from "@/i18n/i18n"
import { FIXTURE_PERFORMANCE_PLANS } from "@/features/catalog/builtin-performance-plans.fixture"
import { PerformanceProfileFields } from "./performance-profile-fields"
import { usePerformanceProfileEditor } from "./use-performance-profile-editor"
import { performanceFormFromProfile, performanceProfileFromForm } from "./performance-profile"
import { QuickPerformancePanel } from "./quick-performance-sheet"
import { executionClient } from "@/test/jobs-fixture"

afterEach(() => vi.unstubAllGlobals())

function setup(pending = false) {
  const user = userEvent.setup()
  const writeText = vi.fn(async (_text: string) => undefined)
  const readText = vi.fn(async () => "")
  vi.stubGlobal("runtime", undefined)
  vi.stubGlobal("navigator", { clipboard: { writeText, readText } })
  function Form() {
    const editor = usePerformanceProfileEditor()
    return <form onSubmit={event => { event.preventDefault(); throw new Error("Unexpected submission") }}>
      <PerformanceProfileFields editor={editor} pending={pending} />
    </form>
  }
  render(<I18nextProvider i18n={createAppI18n("zh-CN")}><Form /></I18nextProvider>)
  return { user, writeText, readText }
}

describe("performance parameter sharing actions", () => {
  it("copies the current edited parameters and exposes icon tooltips", async () => {
    const { user, writeText } = setup()
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "37" } })
    const copy = screen.getByRole("button", { name: "拷贝参数" })
    expect(copy).toHaveAttribute("type", "button")
    expect(copy).toHaveTextContent("")
    await user.hover(copy)
    expect(await screen.findByRole("tooltip")).toHaveTextContent("拷贝参数")
    await user.click(copy)
    await screen.findByText("参数已拷贝")
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({ ...performanceProfileFromForm(performanceFormFromProfile()), request_count: 37 })
  })

  it("only shares load parameters when a connection contains credentials", async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async (_text: string) => undefined)
    vi.stubGlobal("runtime", undefined)
    vi.stubGlobal("navigator", { clipboard: { writeText } })
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><QuickPerformancePanel plans={[]} embedded client={executionClient()} connection={{
      protocol: "openai-chat", address_mode: "base_url", url: "https://private.test/v1", api_key: "private-key", model_id: "private-model", channel_id: "private-channel",
    }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "拷贝参数" }))
    await waitFor(() => expect(writeText).toHaveBeenCalledOnce())
    expect(writeText.mock.calls[0][0]).not.toMatch(/private|api_key|url|model_id|channel_id/)
  })

  it("imports the complete open-loop workload and clears prior validation errors", async () => {
    const { user, readText } = setup()
    fireEvent.change(screen.getByLabelText("并发数"), { target: { value: "0" } })
    await user.click(screen.getByRole("button", { name: "拷贝参数" }))
    expect(screen.getByLabelText("并发数")).toHaveAttribute("aria-invalid", "true")
    const profile = { ...performanceProfileFromForm(performanceFormFromProfile()),
      load_mode: "open_loop", arrival_pattern: "poisson", workload_mode: "normal", concurrency: 0,
      rate_per_second: 3.5, max_in_flight: 32, random_seed: 42, input_tokens: 256, output_tokens: 128,
      input_tokens_stddev: 10, output_tokens_stddev: 4, shared_prefix_tokens: 64,
      warmup_requests: 2, ramp_duration_ms: 1000, slice_duration_ms: 5000, slo_e2e_ms: 2000, slo_target_percent: 95,
    }
    readText.mockResolvedValue(JSON.stringify(profile))
    await user.click(screen.getByRole("button", { name: "从剪贴板导入参数" }))
    await screen.findByText("参数已导入，仅用于当前表单")
    expect(screen.getByLabelText("目标发送 RPS")).toHaveValue(3.5)
    expect(screen.getByLabelText("最大在途")).toHaveValue(32)
    expect(screen.getByLabelText("共享前缀 Token")).toHaveValue(64)
    expect(screen.getByLabelText("随机种子")).toHaveValue(42)
    expect(screen.getByLabelText("时间切片（秒）")).toHaveValue(5)
    expect(screen.queryByText("请先修正参数错误后再拷贝。")).not.toBeInTheDocument()
    expect(document.querySelector('[aria-invalid="true"]')).toBeNull()
  })

  it("keeps all current values when clipboard parameters are invalid", async () => {
    const { user, readText } = setup()
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "73" } })
    readText.mockResolvedValue('{"request_count":999}')
    await user.click(screen.getByRole("button", { name: "从剪贴板导入参数" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("当前参数未更改")
    expect(screen.getByLabelText("请求数")).toHaveValue(73)
    expect(screen.getByLabelText("并发数")).toHaveValue(1)
  })

  it("handles clipboard denial and allows retry without exposing raw errors", async () => {
    const { user, readText } = setup()
    readText.mockRejectedValueOnce(new Error("private clipboard error"))
    await user.click(screen.getByRole("button", { name: "从剪贴板导入参数" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("无法读取剪贴板")
    expect(screen.getByRole("alert")).not.toHaveTextContent("private clipboard error")
    const plan = FIXTURE_PERFORMANCE_PLANS.find(item => item.performance?.random_input)!
    readText.mockResolvedValue(JSON.stringify(performanceProfileFromForm(performanceFormFromProfile(plan.performance))))
    await user.click(screen.getByRole("button", { name: "从剪贴板导入参数" }))
    await screen.findByText("参数已导入，仅用于当前表单")
    expect(screen.getByLabelText("请求数")).toHaveValue(500)
    expect(screen.getByRole("checkbox", { name: "随机输入" })).toBeChecked()
  })

  it("blocks duplicate operations and ignores a stale import after editing", async () => {
    const { user, readText } = setup()
    let finish!: (text: string) => void
    readText.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const paste = screen.getByRole("button", { name: "从剪贴板导入参数" })
    await user.dblClick(paste)
    expect(readText).toHaveBeenCalledOnce()
    expect(paste).toBeDisabled()
    expect(screen.getByRole("button", { name: "拷贝参数" })).toBeDisabled()
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "73" } })
    await act(async () => finish(JSON.stringify(performanceProfileFromForm(performanceFormFromProfile()))))
    expect(await screen.findByRole("alert")).toHaveTextContent("请重新导入")
    expect(screen.getByLabelText("请求数")).toHaveValue(73)
    expect(paste).toBeEnabled()
  })

  it("disables both actions during a running test", () => {
    setup(true)
    expect(screen.getByRole("button", { name: "拷贝参数" })).toBeDisabled()
    expect(screen.getByRole("button", { name: "从剪贴板导入参数" })).toBeDisabled()
  })
})
