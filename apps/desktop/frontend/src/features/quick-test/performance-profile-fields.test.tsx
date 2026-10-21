import { render as testingRender, screen, waitFor, within } from "@testing-library/react"
import type { ReactElement } from "react"
import { I18nextProvider } from "react-i18next"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from "@/components/ui/sheet"
import { Button } from "@/components/ui/button"
import { PerformanceProfileFields } from "./performance-profile-fields"
import { usePerformanceProfileEditor } from "./use-performance-profile-editor"
import type { QuickPerformanceProfile } from "./data"
function render(ui: ReactElement) {
  return testingRender(<I18nextProvider i18n={createAppI18n("zh-CN")}>{ui}</I18nextProvider>)
}
function ProfileEditor({ onSave }: { onSave: (profile: QuickPerformanceProfile) => unknown }) {
  const editor = usePerformanceProfileEditor()
  return <Sheet open><SheetContent><SheetHeader><SheetTitle>编辑性能计划</SheetTitle><SheetDescription>配置并保存计划参数</SheetDescription></SheetHeader>
    <form noValidate onSubmit={event => { event.preventDefault(); const profile = editor.validate(); if (profile) onSave(profile) }}>
      <PerformanceProfileFields editor={editor} pending={false} /><Button type="submit">保存计划</Button>
    </form></SheetContent></Sheet>
}
describe("performance plan parameter authoring", () => {
  it("defaults affinity off and saves a keyboard-enabled affinity setting", async () => {
    const user = userEvent.setup()
    const save = vi.fn()
    render(<ProfileEditor onSave={save} />)
    const affinity = screen.getByRole("checkbox", { name: "启用亲和性" })
    expect(affinity).not.toBeChecked()
    affinity.focus()
    await user.keyboard(" ")
    expect(affinity).toBeChecked()
    await user.click(screen.getByRole("button", { name: "保存计划" }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ affinity_enabled: true }))
  })
  it("saves the random-input setting", async () => {
    const user = userEvent.setup()
    const run = vi.fn(async () => undefined)
    render(<ProfileEditor onSave={run} />)
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    const randomInput = within(dialog).getByRole("checkbox", { name: "随机输入" })
    expect(randomInput).not.toBeChecked()
    await user.click(randomInput)
    expect(randomInput).toBeChecked()
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() => expect(run).toHaveBeenCalledWith(expect.objectContaining({ random_input: true })))
  })

  it("keeps mode-specific drafts and saves an open-loop RPS profile", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })

    const mode = within(dialog).getByRole("combobox", { name: "负载模式" })
    expect(mode).toHaveValue("固定并发")
    await replaceNumber(user, within(dialog).getByLabelText("并发数"), "3")
    await user.click(mode)
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))

    expect(within(dialog).queryByLabelText("并发数")).not.toBeInTheDocument()
    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "12.5")
    await replaceNumber(user, within(dialog).getByLabelText("最大在途"), "37")

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).getByLabelText("并发数")).toHaveValue(3)
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).getByLabelText("目标发送 RPS")).toHaveValue(12.5)
    expect(within(dialog).getByLabelText("最大在途")).toHaveValue(37)

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        {
          load_mode: "open_loop",
          request_count: 10,
          duration_ms: 0,
          concurrency: 0,
          rate_per_second: 12.5,
          max_in_flight: 37,
          arrival_pattern: "constant",
          workload_mode: "fixed",
          random_input: false, affinity_enabled: false,
          random_seed: 0,
          input_tokens_stddev: 0,
          output_tokens_stddev: 0,
          shared_prefix_tokens: 0,
          warmup_requests: 0,
          ramp_duration_ms: 0,
          ramp_request_cap: 0,
          slice_duration_ms: 0,
          slo_ttft_ms: 0,
          slo_tpot_ms: 0,
          slo_e2e_ms: 0,
          slo_target_percent: 0,
          capacity_enabled: false,
          capacity_start: 0,
          capacity_step: 0,
          timeout_ms: 60_000,
          input_tokens: 100,
          output_tokens: 100,
        },
      ),
    )
  })

  it("saves seven explicit disabled SLO and capacity fields by default and focuses the first SLO error", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })

    expect(within(dialog).getByLabelText("SLO TTFT（ms）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO TPOT（ms/token）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO E2E（ms）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO 目标达标率（%）")).toHaveValue(0)
    expect(within(dialog).getByRole("checkbox", { name: "容量阶梯" })).not.toBeChecked()

    const target = within(dialog).getByLabelText("SLO 目标达标率（%）")
    await replaceNumber(user, target, "95")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    expect(
      await within(dialog).findByText("设置目标达标率时，至少启用一个延迟阈值。"),
    ).toHaveAttribute("data-slot", "field-error")
    expect(target).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          slo_ttft_ms: 50,
          slo_tpot_ms: 0,
          slo_e2e_ms: 0,
          slo_target_percent: 95,
          capacity_enabled: false,
          capacity_start: 0,
          capacity_step: 0,
        }),
      ),
    )
  })

  it("keeps fixed and open-loop capacity drafts while disabling ramp and sending only the active mode", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await replaceNumber(user, within(dialog).getByLabelText("SLO 目标达标率（%）"), "90")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "3601")
    await user.click(within(dialog).getByRole("checkbox", { name: "容量阶梯" }))

    expect(within(dialog).queryByLabelText("并发数")).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText("终止并发")).toHaveValue(1)
    expect(within(dialog).getByLabelText("爬坡时间（秒）")).toBeDisabled()
    expect(within(dialog).getByLabelText("爬坡时间（秒）")).toHaveValue(3601)
    await replaceNumber(user, within(dialog).getByLabelText("终止并发"), "4")
    await replaceNumber(user, within(dialog).getByLabelText("起始并发"), "2")
    await replaceNumber(user, within(dialog).getByLabelText("并发步长"), "1")
    expect(within(dialog).getByLabelText("阶梯预览")).toHaveValue("2 → 3 → 4 · 3 档")

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await replaceNumber(user, within(dialog).getByLabelText("终止 RPS"), "3.5")
    await replaceNumber(user, within(dialog).getByLabelText("起始 RPS"), "0.01")
    await replaceNumber(user, within(dialog).getByLabelText("RPS 步长"), "1")
    expect(within(dialog).getByLabelText("阶梯预览")).toHaveValue("0.01 → 1.01 → 2.01 → 3.01 → 3.5 · 5 档")

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).getByLabelText("起始并发")).toHaveValue(2)
    expect(within(dialog).getByLabelText("并发步长")).toHaveValue(1)
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).getByLabelText("起始 RPS")).toHaveValue(0.01)
    expect(within(dialog).getByLabelText("RPS 步长")).toHaveValue(1)

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          load_mode: "open_loop",
          rate_per_second: 3.5,
          ramp_duration_ms: 0,
          ramp_request_cap: 0,
          capacity_enabled: true,
          capacity_start: 0.01,
          capacity_step: 1,
        }),
      ),
    )
  })

  it("accepts a twenty-rung 10,000-request capacity budget and blocks a twenty-first rung", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await replaceNumber(user, within(dialog).getByLabelText("SLO 目标达标率（%）"), "90")
    await user.click(within(dialog).getByRole("checkbox", { name: "容量阶梯" }))
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "500")
    await replaceNumber(user, within(dialog).getByLabelText("终止并发"), "21")

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    expect(
      await within(dialog).findByText("容量阶梯最多支持 20 档；请增大步长或减小终止目标。"),
    ).toHaveAttribute("data-slot", "field-error")
    expect(within(dialog).getByLabelText("并发步长")).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("终止并发"), "20")
    expect(within(dialog).getByText(/容量 10,000 · 合计 10,000 \/ 10,000/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 500,
          concurrency: 20,
          capacity_enabled: true,
          capacity_start: 1,
          capacity_step: 1,
        }),
      ),
    )
  })

  it("keeps a near-terminal open-loop start as a separate rung even when the step exceeds the maximum", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await replaceNumber(user, within(dialog).getByLabelText("SLO 目标达标率（%）"), "90")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "2")
    await user.click(within(dialog).getByRole("checkbox", { name: "容量阶梯" }))
    await replaceNumber(user, within(dialog).getByLabelText("起始 RPS"), "1.9999999999")
    await replaceNumber(user, within(dialog).getByLabelText("RPS 步长"), "3")

    expect(within(dialog).getByLabelText("阶梯预览")).toHaveValue("1.9999999999 → 2 · 2 档")
    expect(within(dialog).getByText(/容量 20 · 合计 20 \/ 10,000/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          load_mode: "open_loop",
          rate_per_second: 2,
          capacity_enabled: true,
          capacity_start: 1.9999999999,
          capacity_step: 3,
        }),
      ),
    )
  })

  it("keeps workload drafts and saves a reproducible Poisson normal workload", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })

    expect(within(dialog).queryByRole("combobox", { name: "到达分布" })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await user.click(within(dialog).getByRole("combobox", { name: "到达分布" }))
    await user.click(screen.getByRole("option", { name: "Poisson 到达" }))

    await user.click(within(dialog).getByRole("combobox", { name: "工作负载" }))
    await user.click(screen.getByRole("option", { name: "正态分布" }))
    await replaceNumber(user, within(dialog).getByLabelText("近似输入 Token 均值"), "120")
    await replaceNumber(user, within(dialog).getByLabelText("最大输出 Token 均值"), "40")
    await replaceNumber(user, within(dialog).getByLabelText("输入 Token 标准差"), "20")
    await replaceNumber(user, within(dialog).getByLabelText("输出 Token 标准差"), "8")
    await replaceNumber(user, within(dialog).getByLabelText("共享前缀 Token"), "60")
    await replaceNumber(user, within(dialog).getByLabelText("随机种子"), "424242")

    await user.click(within(dialog).getByRole("combobox", { name: "工作负载" }))
    await user.click(screen.getByRole("option", { name: "固定 Token" }))
    expect(within(dialog).queryByLabelText("输入 Token 标准差")).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText("随机种子")).toHaveValue(424242)
    await user.click(within(dialog).getByRole("combobox", { name: "工作负载" }))
    await user.click(screen.getByRole("option", { name: "正态分布" }))
    expect(within(dialog).getByLabelText("输入 Token 标准差")).toHaveValue(20)
    expect(within(dialog).getByLabelText("输出 Token 标准差")).toHaveValue(8)
    expect(within(dialog).getByLabelText("共享前缀 Token")).toHaveValue(60)

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        {
          load_mode: "open_loop",
          request_count: 10,
          duration_ms: 0,
          concurrency: 0,
          rate_per_second: 1,
          max_in_flight: 256,
          arrival_pattern: "poisson",
          workload_mode: "normal",
          random_input: false, affinity_enabled: false,
          random_seed: 424242,
          input_tokens_stddev: 20,
          output_tokens_stddev: 8,
          shared_prefix_tokens: 60,
          warmup_requests: 0,
          ramp_duration_ms: 0,
          ramp_request_cap: 0,
          slice_duration_ms: 0,
          slo_ttft_ms: 0,
          slo_tpot_ms: 0,
          slo_e2e_ms: 0,
          slo_target_percent: 0,
          capacity_enabled: false,
          capacity_start: 0,
          capacity_step: 0,
          timeout_ms: 60_000,
          input_tokens: 120,
          output_tokens: 40,
        },
      ),
    )
  })

  it("keeps the fixed-ramp cap draft while normalizing open-loop preparation fields", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })

    expect(within(dialog).getByLabelText("热身请求数")).toHaveValue(0)
    expect(within(dialog).getByLabelText("爬坡时间（秒）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("时间切片（秒）")).toHaveValue(0)
    expect(within(dialog).queryByLabelText("爬坡请求上限")).not.toBeInTheDocument()

    await replaceNumber(user, within(dialog).getByLabelText("热身请求数"), "3")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "10")
    expect(within(dialog).getByLabelText("爬坡请求上限")).toHaveValue(1000)
    await replaceNumber(user, within(dialog).getByLabelText("爬坡请求上限"), "321")
    await replaceNumber(user, within(dialog).getByLabelText("时间切片（秒）"), "2")

    const loadMode = within(dialog).getByRole("combobox", { name: "负载模式" })
    await user.click(loadMode)
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).queryByLabelText("爬坡请求上限")).not.toBeInTheDocument()
    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "10")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).getByLabelText("爬坡请求上限")).toHaveValue(321)
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          warmup_requests: 3,
          ramp_duration_ms: 10_000,
          ramp_request_cap: 0,
          slice_duration_ms: 2_000,
        }),
      ),
    )
  })

  it("accepts exactly 10,000 total fixed requests and blocks 10,001", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("热身请求数"), "1000")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "8001")

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    expect(
      await within(dialog).findByText(
        "热身 1,000 + 爬坡 1,000 + 稳态 8,001 = 10,001，超过总请求预算 10,000。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "8000")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 8_000,
          warmup_requests: 1_000,
          ramp_duration_ms: 1_000,
          ramp_request_cap: 1_000,
        }),
      ),
    )
  })

  it("reserves one measured request for fixed duration-only runs", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("热身请求数"), "9000")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "1")

    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    expect(
      await within(dialog).findByText(
        "热身与爬坡已用完 10,000 请求预算；持续时间稳态至少需要保留 1 个请求。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("爬坡请求上限"), "999")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          warmup_requests: 9_000,
          ramp_request_cap: 999,
        }),
      ),
    )
  })

  it.each([
    { arrival: "恒定间隔", rejectedRate: "6451", acceptedRate: "6450", total: "10,001" },
    { arrival: "Poisson 到达", rejectedRate: "3225.1", acceptedRate: "3225", total: "10,001" },
  ])(
    "uses ramp intensity headroom at the $arrival 10,000-request boundary",
    async ({ arrival, rejectedRate, acceptedRate, total }) => {
      const user = userEvent.setup()
      const saveProfile = vi.fn(async () => undefined)
      render(
        <ProfileEditor
          onSave={saveProfile}
        />,
      )
      const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
      await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
      await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
      await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "1")
      await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
      await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
      if (arrival === "Poisson 到达") {
        await user.click(within(dialog).getByRole("combobox", { name: "到达分布" }))
        await user.click(screen.getByRole("option", { name: arrival }))
      }
      const rate = within(dialog).getByLabelText("目标发送 RPS")
      await replaceNumber(user, rate, rejectedRate)

      await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
      expect(
        await within(dialog).findByText(new RegExp(`= ${total}，超过总请求预算 10,000`)),
      ).toHaveAttribute("data-slot", "field-error")
      expect(saveProfile).not.toHaveBeenCalled()

      await replaceNumber(user, rate, acceptedRate)
      await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
      await waitFor(() =>
        expect(saveProfile).toHaveBeenCalledWith(
          expect.objectContaining({
            request_count: 0,
            ramp_duration_ms: 1_000,
            ramp_request_cap: 0,
          }),
        ),
      )
    },
  )

  it("validates normal workload bounds before saving a performance plan", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn()
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await user.click(within(dialog).getByRole("combobox", { name: "工作负载" }))
    await user.click(screen.getByRole("option", { name: "正态分布" }))
    await replaceNumber(user, within(dialog).getByLabelText("输入 Token 标准差"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("输出 Token 标准差"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("共享前缀 Token"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("随机种子"), "0")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))

    expect(await within(dialog).findByText("输入 Token 标准差不能大于输入均值。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(within(dialog).getByText("输出 Token 标准差不能大于输出均值。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(within(dialog).getByText("共享前缀 Token 必须小于输入均值。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(within(dialog).getByText("随机种子需为 1–4,294,967,295 的整数。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(within(dialog).getByLabelText("输入 Token 标准差")).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()
  })

  it("allows the constant duration-only schedule cap boundary and rejects the first request above it", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    const rate = within(dialog).getByLabelText("目标发送 RPS")

    await replaceNumber(user, rate, "10000.01")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    expect(
      await within(dialog).findByText(
        "当前持续时间与 RPS 预计调度 10,001 个请求，超过 10,000 个上限。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(rate).toHaveAttribute("aria-invalid", "true")
    expect(rate).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, rate, "10000")
    expect(rate).not.toHaveAttribute("aria-invalid")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          duration_ms: 1_000,
          rate_per_second: 10_000,
          arrival_pattern: "constant",
        }),
      ),
    )
  })

  it("uses Poisson headroom for duration-only loads and preserves the hidden RPS draft across modes", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn(async () => undefined)
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await user.click(within(dialog).getByRole("combobox", { name: "到达分布" }))
    await user.click(screen.getByRole("option", { name: "Poisson 到达" }))
    const rate = within(dialog).getByLabelText("目标发送 RPS")
    await replaceNumber(user, rate, "5000")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))

    expect(
      await within(dialog).findByText(
        "Poisson 到达需预留两倍调度余量；当前预计上限 10,001 个请求，超过 10,000 个上限。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(rate).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).queryByLabelText("目标发送 RPS")).not.toBeInTheDocument()
    expect(within(dialog).queryByText(/当前预计上限/)).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).getByLabelText("目标发送 RPS")).toHaveValue(5000)
    expect(within(dialog).getByRole("combobox", { name: "到达分布" })).toHaveValue(
      "Poisson 到达",
    )

    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "4999.5")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))
    await waitFor(() =>
      expect(saveProfile).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          duration_ms: 1_000,
          rate_per_second: 4_999.5,
          arrival_pattern: "poisson",
        }),
      ),
    )
  })

  it("requires a request-count or duration target before saving a performance plan", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn()
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))

    const requestCount = within(dialog).getByLabelText("请求数")
    const duration = within(dialog).getByLabelText("持续时间（秒）")
    expect(
      await within(dialog).findByText("请输入大于 0 的请求数，或填写持续时间。"),
    ).toHaveAttribute("data-slot", "field-error")
    expect(within(dialog).getByText("请输入大于 0 的持续时间，或填写请求数。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(requestCount).toHaveAttribute("aria-invalid", "true")
    expect(duration).toHaveAttribute("aria-invalid", "true")
    expect(requestCount).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()

    await replaceNumber(user, duration, "1")
    expect(requestCount).not.toHaveAttribute("aria-invalid")
    expect(duration).not.toHaveAttribute("aria-invalid")
    expect(
      within(dialog).queryByText("请输入大于 0 的请求数，或填写持续时间。"),
    ).not.toBeInTheDocument()
    expect(
      within(dialog).queryByText("请输入大于 0 的持续时间，或填写请求数。"),
    ).not.toBeInTheDocument()
  })

  it("identifies an out-of-range performance input", async () => {
    const user = userEvent.setup()
    const saveProfile = vi.fn()
    render(
      <ProfileEditor
        onSave={saveProfile}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "编辑性能计划" })
    const concurrency = within(dialog).getByLabelText("并发数")
    await replaceNumber(user, concurrency, "0")
    await user.click(within(dialog).getByRole("button", { name: "保存计划" }))

    expect(await within(dialog).findByText("并发数需为 1–256 的整数。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(concurrency).toHaveAttribute("aria-invalid", "true")
    expect(concurrency).toHaveFocus()
    expect(saveProfile).not.toHaveBeenCalled()
  })
})
async function replaceNumber(
  user: ReturnType<typeof userEvent.setup>,
  input: HTMLElement,
  value: string,
) {
  await user.clear(input)
  await user.type(input, value)
}
