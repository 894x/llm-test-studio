import { render as testingRender, screen, waitFor, within } from "@testing-library/react"
import type { ReactElement } from "react"
import { I18nextProvider } from "react-i18next"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { createAppI18n } from "@/i18n/i18n"

import { QuickPerformanceSheet } from "./quick-performance-sheet"
import {
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickPerformanceSLOAssessment,
} from "./data"

function render(ui: ReactElement) {
  return testingRender(<I18nextProvider i18n={createAppI18n("zh-CN")}>{ui}</I18nextProvider>)
}

const connection = {
  address_mode: "base_url" as const,
  url: "https://api.example.test/v1",
  api_key: "sk-private-value",
  model_id: "gpt-new",
  prompt: "hello",
  timeout_ms: 30000,
}

describe("QuickPerformanceSheet", () => {
  it("rounds result throughput and latency presentation while keeping percentage precision", async () => {
    const user = userEvent.setup()
    const result = successfulPerformanceReport()
    Object.assign(result.metrics, {
      input_tpm: 11_813_376.88, output_tpm: 50_029.88, total_tpm: 11_863_406.75,
      generation_tps: 833.83, successful_request_qps: 6.15, tpot_p95_ms: 21.82,
      cache_rate_percent: 25.25,
    })
    const original = structuredClone(result)
    render(<QuickPerformanceSheet open onOpenChange={vi.fn()} connection={connection} run={async () => result} />)
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    const report = await screen.findByRole("region", { name: "性能报告" })
    for (const value of ["11,813,377 TPM", "50,030 TPM", "11,863,407 TPM", "834 token/s", "6 req/s", "25.3%"]) {
      expect(report).toHaveTextContent(value)
    }
    const latency = within(report).getByRole("table", { name: "延迟分布统计" })
    expect(within(latency).getByRole("row", { name: /TPOT/ })).toHaveTextContent("22")
    expect(latency).not.toHaveTextContent("21.8")
    expect(result).toEqual(original)
  })

  it("clears a previous target report and ignores late results from an earlier target", async () => {
    const user = userEvent.setup()
    let finishOld!: (report: QuickPerformanceReport) => void
    const run = vi.fn(
      () =>
        new Promise<QuickPerformanceReport>((resolve) => {
          finishOld = resolve
        }),
    )
    const view = render(
      <QuickPerformanceSheet open onOpenChange={vi.fn()} connection={connection} run={run} />,
    )
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    const next = { ...connection, model_id: "other-model" }
    view.rerender(
      <I18nextProvider i18n={createAppI18n("zh-CN")}>
        <QuickPerformanceSheet open onOpenChange={vi.fn()} connection={next} run={run} />
      </I18nextProvider>,
    )
    finishOld(successfulPerformanceReport())
    await waitFor(() => expect(screen.getByRole("button", { name: "开始性能测试" })).toBeEnabled())
    expect(screen.queryByRole("region", { name: "性能报告" })).not.toBeInTheDocument()
    run.mockResolvedValue(successfulPerformanceReport())
    await user.click(screen.getByRole("button", { name: "开始性能测试" }))
    await screen.findByRole("region", { name: "性能报告" })
    view.rerender(
      <I18nextProvider i18n={createAppI18n("zh-CN")}>
        <QuickPerformanceSheet open onOpenChange={vi.fn()} connection={connection} run={run} />
      </I18nextProvider>,
    )
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "性能报告" })).not.toBeInTheDocument(),
    )
  })

  it("runs a configurable performance test from the immutable connection and renders its report", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    const onPerformanceArchived = vi.fn(async () => {})
    const onOpenReport = vi.fn()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
        onArchived={onPerformanceArchived}
        onOpenReport={onOpenReport}
      />,
    )

    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    expect(within(dialog).getByLabelText("请求数")).toHaveValue(10)
    expect(within(dialog).getByLabelText("持续时间（秒）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("并发数")).toHaveValue(1)
    expect(within(dialog).getByLabelText("单请求超时（秒）")).toHaveValue(60)
    expect(within(dialog).getByLabelText("近似输入 Token")).toHaveValue(100)
    expect(within(dialog).getByLabelText("近似输入 Token")).toHaveAttribute("max", "1000000")
    expect(within(dialog).getByLabelText("最大输出 Token")).toHaveValue(100)
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "4")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("并发数"), "2")
    await replaceNumber(user, within(dialog).getByLabelText("单请求超时（秒）"), "30")
    await replaceNumber(user, within(dialog).getByLabelText("近似输入 Token"), "1000000")
    await replaceNumber(user, within(dialog).getByLabelText("最大输出 Token"), "32")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        {
          address_mode: "base_url",
          url: "https://api.example.test/v1",
          api_key: "sk-private-value",
          model_id: "gpt-new",
          load_mode: "fixed_concurrency",
          request_count: 4,
          duration_ms: 1_000,
          concurrency: 2,
          rate_per_second: 0,
          max_in_flight: 0,
          arrival_pattern: "constant",
          workload_mode: "fixed",
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
          timeout_ms: 30_000,
          input_tokens: 1_000_000,
          output_tokens: 32,
        },
        expect.any(Function),
      ),
    )
    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent("完成 / 计划")
    expect(report).toHaveTextContent("失败")
    expect(report).toHaveTextContent("100%")
    expect(report).toHaveTextContent("13 req/s")
    expect(report).toHaveTextContent("目标发送")
    expect(report).toHaveTextContent("实际发送")
    expect(report).toHaveTextContent("成功吞吐")
    expect(report).not.toHaveTextContent("旧版请求吞吐")
    expect(report).toHaveTextContent("750 RPM")
    expect(report).toHaveTextContent("39,000 TPM")
    const latency = within(report).getByRole("table", { name: "延迟分布统计" })
    expect(within(latency).getByRole("row", { name: /TTFT/ })).toHaveTextContent(
      /32\s*30\s*40\s*42\s*44/,
    )
    expect(within(latency).getByRole("row", { name: /TPOT/ })).toHaveTextContent(
      /5\s*4\s*5\s*6\s*7/,
    )
    expect(within(latency).getByRole("row", { name: /E2E/ })).toHaveTextContent(
      /65\s*60\s*75\s*80\s*84/,
    )
    expect(
      within(latency).getByRole("row", { name: /客户端排队（本地调度延迟）/ }),
    ).toHaveTextContent(/1\s*0\s*2\s*2\s*3/)
    expect(
      within(report).getByRole("figure", { name: "TTFT（含推理） 分布图" }),
    ).toBeInTheDocument()
    expect(within(report).getByRole("figure", { name: "TPOT 时间曲线" })).toBeInTheDocument()
    expect(within(report).getByRole("figure", { name: "E2E 时间曲线" })).toBeInTheDocument()
    expect(onPerformanceArchived).toHaveBeenCalledWith("77777777-7777-4777-8777-777777777771")
    await user.click(within(report).getByRole("button", { name: "查看正式报告" }))
    expect(onOpenReport).toHaveBeenCalledWith("77777777-7777-4777-8777-777777777771")
    expect(report).not.toHaveTextContent("sk-private-value")
    expect(within(report).queryByRole("table", { name: "流式时序统计" })).not.toBeInTheDocument()
  })

  it("renders the shared streaming timing table for a live schema-v3 report", async () => {
    const user = userEvent.setup()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={vi.fn(async () => phaseFivePerformanceReport())}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    const streaming = within(report).getByRole("table", { name: "流式时序统计" })
    expect(within(streaming).getByRole("row", { name: /TTFT（含推理）/ })).toHaveTextContent(
      /20 ms.*4/,
    )
    expect(within(streaming).getByRole("row", { name: /Observed ICL/ })).toHaveTextContent(
      "非 Token ITL",
    )
  })

  it("keeps mode-specific drafts and sends an open-loop RPS profile", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })

    const mode = within(dialog).getByRole("combobox", { name: "负载模式" })
    expect(mode).toHaveTextContent("固定并发")
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

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        {
          address_mode: "base_url",
          url: "https://api.example.test/v1",
          api_key: "sk-private-value",
          model_id: "gpt-new",
          load_mode: "open_loop",
          request_count: 10,
          duration_ms: 0,
          concurrency: 0,
          rate_per_second: 12.5,
          max_in_flight: 37,
          arrival_pattern: "constant",
          workload_mode: "fixed",
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
        expect.any(Function),
      ),
    )
  })

  it("sends seven explicit disabled SLO and capacity fields by default and focuses the first SLO error", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })

    expect(within(dialog).getByLabelText("SLO TTFT（ms）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO TPOT（ms/token）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO E2E（ms）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("SLO 目标达标率（%）")).toHaveValue(0)
    expect(within(dialog).getByRole("checkbox", { name: "容量阶梯" })).not.toBeChecked()

    const target = within(dialog).getByLabelText("SLO 目标达标率（%）")
    await replaceNumber(user, target, "95")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    expect(
      await within(dialog).findByText("设置目标达标率时，至少启用一个延迟阈值。"),
    ).toHaveAttribute("data-slot", "field-error")
    expect(target).toHaveFocus()
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          slo_ttft_ms: 50,
          slo_tpot_ms: 0,
          slo_e2e_ms: 0,
          slo_target_percent: 95,
          capacity_enabled: false,
          capacity_start: 0,
          capacity_step: 0,
        }),
        expect.any(Function),
      ),
    )
  })

  it("keeps fixed and open-loop capacity drafts while disabling ramp and sending only the active mode", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
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
    expect(within(dialog).getByText("2 → 3 → 4 · 3 档")).toBeInTheDocument()

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await replaceNumber(user, within(dialog).getByLabelText("终止 RPS"), "3.5")
    await replaceNumber(user, within(dialog).getByLabelText("起始 RPS"), "0.01")
    await replaceNumber(user, within(dialog).getByLabelText("RPS 步长"), "1")
    expect(within(dialog).getByText("0.01 → 1.01 → 2.01 → 3.01 → 3.5 · 5 档")).toBeInTheDocument()

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).getByLabelText("起始并发")).toHaveValue(2)
    expect(within(dialog).getByLabelText("并发步长")).toHaveValue(1)
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).getByLabelText("起始 RPS")).toHaveValue(0.01)
    expect(within(dialog).getByLabelText("RPS 步长")).toHaveValue(1)

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          load_mode: "open_loop",
          rate_per_second: 3.5,
          ramp_duration_ms: 0,
          ramp_request_cap: 0,
          capacity_enabled: true,
          capacity_start: 0.01,
          capacity_step: 1,
        }),
        expect.any(Function),
      ),
    )
  })

  it("accepts a twenty-rung 10,000-request capacity budget and blocks a twenty-first rung", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await replaceNumber(user, within(dialog).getByLabelText("SLO 目标达标率（%）"), "90")
    await user.click(within(dialog).getByRole("checkbox", { name: "容量阶梯" }))
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "500")
    await replaceNumber(user, within(dialog).getByLabelText("终止并发"), "21")

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    expect(
      await within(dialog).findByText("容量阶梯最多支持 20 档；请增大步长或减小终止目标。"),
    ).toHaveAttribute("data-slot", "field-error")
    expect(within(dialog).getByLabelText("并发步长")).toHaveFocus()
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("终止并发"), "20")
    expect(within(dialog).getByText(/容量 10,000 · 合计 10,000 \/ 10,000/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 500,
          concurrency: 20,
          capacity_enabled: true,
          capacity_start: 1,
          capacity_step: 1,
        }),
        expect.any(Function),
      ),
    )
  })

  it("keeps a near-terminal open-loop start as a separate rung even when the step exceeds the maximum", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("SLO TTFT（ms）"), "50")
    await replaceNumber(user, within(dialog).getByLabelText("SLO 目标达标率（%）"), "90")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "2")
    await user.click(within(dialog).getByRole("checkbox", { name: "容量阶梯" }))
    await replaceNumber(user, within(dialog).getByLabelText("起始 RPS"), "1.9999999999")
    await replaceNumber(user, within(dialog).getByLabelText("RPS 步长"), "3")

    expect(within(dialog).getByText("1.9999999999 → 2 · 2 档")).toBeInTheDocument()
    expect(within(dialog).getByText(/容量 20 · 合计 20 \/ 10,000/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          load_mode: "open_loop",
          rate_per_second: 2,
          capacity_enabled: true,
          capacity_start: 1.9999999999,
          capacity_step: 3,
        }),
        expect.any(Function),
      ),
    )
  })

  it("keeps workload drafts and sends a reproducible Poisson normal workload", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })

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

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        {
          address_mode: "base_url",
          url: "https://api.example.test/v1",
          api_key: "sk-private-value",
          model_id: "gpt-new",
          load_mode: "open_loop",
          request_count: 10,
          duration_ms: 0,
          concurrency: 0,
          rate_per_second: 1,
          max_in_flight: 256,
          arrival_pattern: "poisson",
          workload_mode: "normal",
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
        expect.any(Function),
      ),
    )
  })

  it("keeps the fixed-ramp cap draft while normalizing open-loop preparation fields", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })

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

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          warmup_requests: 3,
          ramp_duration_ms: 10_000,
          ramp_request_cap: 0,
          slice_duration_ms: 2_000,
        }),
        expect.any(Function),
      ),
    )
  })

  it("accepts exactly 10,000 total fixed requests and blocks 10,001", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("热身请求数"), "1000")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "8001")

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    expect(
      await within(dialog).findByText(
        "热身 1,000 + 爬坡 1,000 + 稳态 8,001 = 10,001，超过总请求预算 10,000。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "8000")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 8_000,
          warmup_requests: 1_000,
          ramp_duration_ms: 1_000,
          ramp_request_cap: 1_000,
        }),
        expect.any(Function),
      ),
    )
  })

  it("reserves one measured request for fixed duration-only runs", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("热身请求数"), "9000")
    await replaceNumber(user, within(dialog).getByLabelText("爬坡时间（秒）"), "1")

    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    expect(
      await within(dialog).findByText(
        "热身与爬坡已用完 10,000 请求预算；持续时间稳态至少需要保留 1 个请求。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await replaceNumber(user, within(dialog).getByLabelText("爬坡请求上限"), "999")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          warmup_requests: 9_000,
          ramp_request_cap: 999,
        }),
        expect.any(Function),
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
      const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
      render(
        <QuickPerformanceSheet
          open
          onOpenChange={vi.fn()}
          connection={connection}
          run={runQuickPerformanceTest}
        />,
      )
      const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
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

      await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
      expect(
        await within(dialog).findByText(new RegExp(`= ${total}，超过总请求预算 10,000`)),
      ).toHaveAttribute("data-slot", "field-error")
      expect(runQuickPerformanceTest).not.toHaveBeenCalled()

      await replaceNumber(user, rate, acceptedRate)
      await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
      await waitFor(() =>
        expect(runQuickPerformanceTest).toHaveBeenCalledWith(
          expect.objectContaining({
            request_count: 0,
            ramp_duration_ms: 1_000,
            ramp_request_cap: 0,
          }),
          expect.any(Function),
        ),
      )
    },
  )

  it("validates normal workload bounds before starting performance testing", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("combobox", { name: "工作负载" }))
    await user.click(screen.getByRole("option", { name: "正态分布" }))
    await replaceNumber(user, within(dialog).getByLabelText("输入 Token 标准差"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("输出 Token 标准差"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("共享前缀 Token"), "101")
    await replaceNumber(user, within(dialog).getByLabelText("随机种子"), "0")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

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
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()
  })

  it("allows the constant duration-only schedule cap boundary and rejects the first request above it", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    const rate = within(dialog).getByLabelText("目标发送 RPS")

    await replaceNumber(user, rate, "10000.01")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    expect(
      await within(dialog).findByText(
        "当前持续时间与 RPS 预计调度 10,001 个请求，超过 10,000 个上限。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(rate).toHaveAttribute("aria-invalid", "true")
    expect(rate).toHaveFocus()
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await replaceNumber(user, rate, "10000")
    expect(rate).not.toHaveAttribute("aria-invalid")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          duration_ms: 1_000,
          rate_per_second: 10_000,
          arrival_pattern: "constant",
        }),
        expect.any(Function),
      ),
    )
  })

  it("uses Poisson headroom for duration-only loads and preserves the hidden RPS draft across modes", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await user.click(within(dialog).getByRole("combobox", { name: "到达分布" }))
    await user.click(screen.getByRole("option", { name: "Poisson 到达" }))
    const rate = within(dialog).getByLabelText("目标发送 RPS")
    await replaceNumber(user, rate, "5000")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    expect(
      await within(dialog).findByText(
        "Poisson 到达需预留两倍调度余量；当前预计上限 10,001 个请求，超过 10,000 个上限。",
      ),
    ).toHaveAttribute("data-slot", "field-error")
    expect(rate).toHaveFocus()
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "固定并发" }))
    expect(within(dialog).queryByLabelText("目标发送 RPS")).not.toBeInTheDocument()
    expect(within(dialog).queryByText(/当前预计上限/)).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    expect(within(dialog).getByLabelText("目标发送 RPS")).toHaveValue(5000)
    expect(within(dialog).getByRole("combobox", { name: "到达分布" })).toHaveTextContent(
      "Poisson 到达",
    )

    await replaceNumber(user, within(dialog).getByLabelText("目标发送 RPS"), "4999.5")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))
    await waitFor(() =>
      expect(runQuickPerformanceTest).toHaveBeenCalledWith(
        expect.objectContaining({
          request_count: 0,
          duration_ms: 1_000,
          rate_per_second: 4_999.5,
          arrival_pattern: "poisson",
        }),
        expect.any(Function),
      ),
    )
  })

  it("shows authoritative progress while a performance test is running", async () => {
    const user = userEvent.setup()
    let resolvePerformance!: (report: QuickPerformanceReport) => void
    const runQuickPerformanceTest = vi.fn(
      (_command, onProgress?: (progress: QuickPerformanceProgress) => void) => {
        onProgress?.({
          phase: "sending",
          planned: 10,
          launched: 5,
          completed: 3,
          in_flight: 2,
          peak_in_flight: 2,
          succeeded: 2,
          failed: 1,
          rejected: 0,
          send_duration_ms: 120,
          drain_duration_ms: 0,
          total_duration_ms: 120,
        })
        return new Promise<QuickPerformanceReport>((resolve) => {
          resolvePerformance = resolve
        })
      },
    )
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const status = await within(dialog).findByRole("status", { name: "性能测试进度" })
    expect(status).toHaveTextContent("发送中")
    expect(status).toHaveTextContent(/完成 \/ 计划\s*3 \/ 10/)
    expect(status).toHaveTextContent(/成功\s*2/)
    expect(status).toHaveTextContent(/失败\s*1/)
    expect(status).toHaveTextContent(/在途\s*2/)
    expect(within(status).getByRole("progressbar", { name: "请求完成进度" })).toHaveAttribute(
      "aria-valuenow",
      "30",
    )

    resolvePerformance(successfulPerformanceReport())
    await within(dialog).findByRole("region", { name: "性能报告" })
  })

  it("adds authoritative capacity rung context to the existing progress phase", async () => {
    const user = userEvent.setup()
    let resolvePerformance!: (report: QuickPerformanceReport) => void
    const runQuickPerformanceTest = vi.fn(
      (_command, onProgress?: (progress: QuickPerformanceProgress) => void) => {
        onProgress?.({
          phase: "sending",
          planned: 10,
          offered: 6,
          launched: 5,
          completed: 3,
          in_flight: 2,
          peak_in_flight: 2,
          succeeded: 3,
          failed: 0,
          rejected: 1,
          capacity_rung_number: 2,
          capacity_rung_count: 4,
          capacity_target: 0.01,
          send_duration_ms: 120,
          drain_duration_ms: 0,
          total_duration_ms: 120,
        })
        return new Promise<QuickPerformanceReport>((resolve) => {
          resolvePerformance = resolve
        })
      },
    )
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("combobox", { name: "负载模式" }))
    await user.click(screen.getByRole("option", { name: "开放到达（RPS）" }))
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const status = await within(dialog).findByRole("status", { name: "性能测试进度" })
    expect(status).toHaveTextContent("档位 2 / 4")
    expect(status).toHaveTextContent("当前目标 0.01 RPS")
    expect(status).toHaveTextContent("发送中")

    resolvePerformance(successfulPerformanceReport())
    await within(dialog).findByRole("region", { name: "性能报告" })
  })

  it("shows transport, selected-rung SLO, and capacity conclusions independently", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => capacityPerformanceReport())
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent("传输与协议通过")
    expect(report).toHaveTextContent("SLO 通过")
    expect(report).toHaveTextContent("最高通过并发 2")
    expect(report).toHaveTextContent("首次未通过 3")
    expect(report).toHaveTextContent("好请求 4 / 4")
    expect(report).toHaveTextContent("Goodput 13 req/s")
    expect(report).toHaveTextContent(/峰值在途 \/ 配置并发\s*2 \/ 2/)
  })

  it("uses the selected open-loop capacity rung as the steady-state send target", async () => {
    const user = userEvent.setup()
    const selectedReport = capacityPerformanceReport()
    selectedReport.profile.load_mode = "open_loop"
    selectedReport.profile.rate_per_second = 3
    selectedReport.profile.max_in_flight = 4
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={vi.fn(async () => selectedReport)}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent(/目标发送\s*2 req\/s/)
  })

  it("identifies warmup progress and an authoritative request-cap stop", async () => {
    const user = userEvent.setup()
    let resolvePerformance!: (report: QuickPerformanceReport) => void
    const runQuickPerformanceTest = vi.fn(
      (_command, onProgress?: (progress: QuickPerformanceProgress) => void) => {
        onProgress?.({
          phase: "warming_up",
          planned: 4,
          offered: 4,
          launched: 3,
          completed: 2,
          in_flight: 1,
          peak_in_flight: 2,
          succeeded: 2,
          failed: 0,
          rejected: 1,
          capped: true,
          send_duration_ms: 120,
          drain_duration_ms: 0,
          total_duration_ms: 120,
        })
        return new Promise<QuickPerformanceReport>((resolve) => {
          resolvePerformance = resolve
        })
      },
    )
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const status = await within(dialog).findByRole("status", { name: "性能测试进度" })
    expect(status).toHaveTextContent("正在热身")
    expect(status).toHaveTextContent(/热身完成 \/ 计划\s*2 \/ 4/)
    expect(status).toHaveTextContent("当前阶段已达到请求上限")
    expect(within(status).getByRole("progressbar", { name: "请求完成进度" })).toHaveAttribute(
      "aria-valuenow",
      "50",
    )

    resolvePerformance(successfulPerformanceReport())
    await within(dialog).findByRole("region", { name: "性能报告" })
  })

  it("shows report finalization after Core has completed the load", async () => {
    const user = userEvent.setup()
    let resolvePerformance!: (report: QuickPerformanceReport) => void
    const runQuickPerformanceTest = vi.fn(
      (_command, onProgress?: (progress: QuickPerformanceProgress) => void) => {
        onProgress?.({
          phase: "completed",
          planned: 10,
          launched: 10,
          completed: 10,
          in_flight: 0,
          peak_in_flight: 2,
          succeeded: 10,
          failed: 0,
          rejected: 0,
          send_duration_ms: 300,
          drain_duration_ms: 20,
          total_duration_ms: 320,
        })
        return new Promise<QuickPerformanceReport>((resolve) => {
          resolvePerformance = resolve
        })
      },
    )
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const status = await within(dialog).findByRole("status", { name: "性能测试进度" })
    expect(status).toHaveTextContent("测试已完成，正在封存报告")
    expect(status).not.toHaveTextContent("发送中")

    resolvePerformance(successfulPerformanceReport())
    await within(dialog).findByRole("region", { name: "性能报告" })
  })

  it("does not present the duration-mode safety cap as a planned request count", async () => {
    const user = userEvent.setup()
    const durationReport = successfulPerformanceReport()
    durationReport.profile.request_count = 0
    durationReport.profile.duration_ms = 1_000
    durationReport.progress.planned = 10_000
    const runQuickPerformanceTest = vi.fn(async () => durationReport)
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent("完成（持续时间模式）")
    expect(report).not.toHaveTextContent("4 / 10,000")
  })

  it("marks steady-state metrics as primary and summarizes authoritative preparation data", async () => {
    const user = userEvent.setup()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={vi.fn(async () => phaseThreePerformanceReport())}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent("主指标仅统计稳态阶段")
    expect(report).toHaveTextContent("请求预算")
    expect(report).toHaveTextContent("7 / 10,000")
    expect(report).toHaveTextContent("热身")
    expect(report).toHaveTextContent("1 / 1")
    expect(report).toHaveTextContent("爬坡")
    expect(report).toHaveTextContent("2 / 2")
    expect(report).toHaveTextContent("爬坡窗口未完整执行")
    expect(report).toHaveTextContent("2 段 · 1 s 粒度")
    expect(within(report).queryByRole("table", { name: "时间切片" })).not.toBeInTheDocument()
  })

  it("requires a request-count or duration target before starting performance testing", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

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
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()

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

  it("filters request results and reveals redacted failure response evidence", async () => {
    const user = userEvent.setup()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={vi.fn(async () => mixedPerformanceReport())}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(
      within(report).getByRole("figure", { name: "请求结果分布：成功 3，失败 1" }),
    ).toBeInTheDocument()
    expect(within(report).getByRole("table", { name: "逐请求结果" })).toBeInTheDocument()
    await user.click(within(report).getByRole("button", { name: "失败 1" }))
    expect(within(report).getByRole("row", { name: /请求 1/ })).toBeInTheDocument()
    expect(within(report).queryByRole("row", { name: /请求 2/ })).not.toBeInTheDocument()

    await user.click(within(report).getByRole("button", { name: "查看请求 1 详情" }))
    const detail = within(report).getByRole("region", { name: "请求 1 详情" })
    expect(detail).toHaveTextContent("鉴权失败")
    expect(detail).toHaveTextContent("req-safe-123")
    expect(detail).toHaveTextContent("quota exhausted")
    expect(detail).toHaveTextContent("已截断")
    expect(detail).toHaveTextContent("响应已经过安全脱敏")
    expect(detail).not.toHaveTextContent("sk-private-value")
  })

  it("identifies an out-of-range performance input", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn()
    render(
      <QuickPerformanceSheet
        open
        onOpenChange={vi.fn()}
        connection={connection}
        run={runQuickPerformanceTest}
      />,
    )
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    const concurrency = within(dialog).getByLabelText("并发数")
    await replaceNumber(user, concurrency, "0")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    expect(await within(dialog).findByText("并发数需为 1–256 的整数。")).toHaveAttribute(
      "data-slot",
      "field-error",
    )
    expect(concurrency).toHaveAttribute("aria-invalid", "true")
    expect(concurrency).toHaveFocus()
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()
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

function successfulPerformanceReport(): QuickPerformanceReport {
  return {
    schema_version: 2,
    report_id: "77777777-7777-4777-8777-777777777771",
    generated_at: "2026-08-31T14:30:00Z",
    archived: true,
    archive_status: "archived",
    model_id: "gpt-new",
    success: true,
    address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: {
      load_mode: "fixed_concurrency",
      request_count: 4,
      duration_ms: 1_000,
      concurrency: 2,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    },
    progress: {
      phase: "completed",
      planned: 4,
      offered: 4,
      launched: 4,
      completed: 4,
      in_flight: 0,
      peak_in_flight: 2,
      succeeded: 4,
      failed: 0,
      rejected: 0,
      send_duration_ms: 300,
      drain_duration_ms: 20,
      total_duration_ms: 320,
    },
    metrics: {
      completed: 4,
      succeeded: 4,
      failed: 0,
      timed_out: 0,
      success_rate_percent: 100,
      offered_qps: 13.3,
      launched_qps: 13.3,
      completed_qps: 12.5,
      successful_request_qps: 12.5,
      request_qps: 12.5,
      rpm: 750,
      input_tpm: 15_000,
      output_tpm: 24_000,
      total_tpm: 39_000,
      generation_tps: 400,
      ttft_p50_ms: 30,
      ttft_p90_ms: 40,
      ttft_p95_ms: 42,
      ttft_p99_ms: 44,
      ttft_average_ms: 32,
      tpot_p50_ms: 4,
      tpot_p90_ms: 5,
      tpot_p95_ms: 6,
      tpot_p99_ms: 7,
      tpot_average_ms: 4.5,
      e2e_p50_ms: 60,
      e2e_p90_ms: 75,
      e2e_p95_ms: 80,
      e2e_p99_ms: 84,
      e2e_average_ms: 65,
      schedule_lag_p50_ms: 0,
      schedule_lag_p90_ms: 1.8,
      schedule_lag_p95_ms: 2,
      schedule_lag_p99_ms: 2.8,
      schedule_lag_average_ms: 0.5,
      prompt_tokens: 80,
      completion_tokens: 128,
      cached_tokens: 20,
      cache_rate_percent: 25,
    },
    samples: [
      {
        request_index: 0,
        scheduled_offset_ms: 0,
        started_offset_ms: 0,
        finished_offset_ms: 60,
        schedule_lag_ms: 0,
        e2e_ms: 60,
        ttft_ms: 30,
        tpot_ms: 4,
        http_status: 200,
        success: true,
        timed_out: false,
        prompt_tokens: 20,
        completion_tokens: 32,
        cached_tokens: 5,
      },
      {
        request_index: 1,
        scheduled_offset_ms: 0,
        started_offset_ms: 1,
        finished_offset_ms: 75,
        schedule_lag_ms: 1,
        e2e_ms: 74,
        ttft_ms: 40,
        tpot_ms: 5,
        http_status: 200,
        success: true,
        timed_out: false,
        prompt_tokens: 20,
        completion_tokens: 32,
        cached_tokens: 5,
      },
      {
        request_index: 2,
        scheduled_offset_ms: 0,
        started_offset_ms: 2,
        finished_offset_ms: 84,
        schedule_lag_ms: 2,
        e2e_ms: 82,
        ttft_ms: 44,
        tpot_ms: 7,
        http_status: 200,
        success: true,
        timed_out: false,
        prompt_tokens: 20,
        completion_tokens: 32,
        cached_tokens: 5,
      },
      {
        request_index: 3,
        scheduled_offset_ms: 0,
        started_offset_ms: 2,
        finished_offset_ms: 90,
        schedule_lag_ms: 2,
        e2e_ms: 88,
        ttft_ms: 42,
        tpot_ms: 6,
        http_status: 200,
        success: true,
        timed_out: false,
        prompt_tokens: 20,
        completion_tokens: 32,
        cached_tokens: 5,
      },
    ],
    failures: [],
  }
}

function phaseFivePerformanceReport(): QuickPerformanceReport {
  const report = successfulPerformanceReport()
  return {
    ...report,
    schema_version: 3,
    metrics: {
      ...report.metrics,
      ttft_samples: 4,
      ttft_p50_ms: 20,
      ttft_p90_ms: 20,
      ttft_p95_ms: 20,
      ttft_p99_ms: 20,
      ttft_average_ms: 20,
      ttfb_samples: 4,
      ttfb_p50_ms: 10,
      ttfb_p95_ms: 10,
      ttfb_p99_ms: 10,
      ttfb_average_ms: 10,
      ttft_any_samples: 4,
      ttft_any_p50_ms: 20,
      ttft_any_p95_ms: 20,
      ttft_any_p99_ms: 20,
      ttft_any_average_ms: 20,
      ttft_visible_samples: 4,
      ttft_visible_p50_ms: 30,
      ttft_visible_p95_ms: 30,
      ttft_visible_p99_ms: 30,
      ttft_visible_average_ms: 30,
      ttst_samples: 4,
      ttst_p50_ms: 40,
      ttst_p95_ms: 40,
      ttst_p99_ms: 40,
      ttst_average_ms: 40,
      observed_icl_samples: 4,
      observed_icl_p50_ms: 20,
      observed_icl_p95_ms: 20,
      observed_icl_p99_ms: 20,
      observed_icl_average_ms: 20,
      semantic_chunk_count_samples: 4,
      semantic_chunk_count_p50: 3,
      semantic_chunk_count_p95: 3,
      semantic_chunk_count_p99: 3,
      semantic_chunk_count_average: 3,
    },
    samples: report.samples.map((sample) => ({
      ...sample,
      ttft_ms: 20,
      ttfb_ms: 10,
      ttft_any_ms: 20,
      ttft_visible_ms: 30,
      ttst_ms: 40,
      observed_icl_ms: 20,
      semantic_chunk_count: 3,
    })),
  }
}

function capacityPerformanceReport(): QuickPerformanceReport {
  const report = successfulPerformanceReport()
  const passed = {
    status: "passed" as const,
    thresholds: { ttft_ms: 50, tpot_ms: 0, e2e_ms: 0 },
    target_percent: 90,
    total_requests: 4,
    good_requests: 4,
    bad_requests: 0,
    good_request_percent: 100,
    goodput_qps: 12.5,
    violations: { transport: 0, ttft: 0, tpot: 0, e2e: 0 },
  }
  const failed = {
    ...passed,
    status: "failed" as const,
    good_requests: 2,
    bad_requests: 2,
    good_request_percent: 50,
    goodput_qps: 6.25,
    violations: { transport: 0, ttft: 2, tpot: 0, e2e: 0 },
  }
  const rung = (index: number, target: number, slo: QuickPerformanceSLOAssessment = passed) => ({
    index,
    target,
    success: true,
    progress: {
      ...report.progress,
      capacity_rung_number: index + 1,
      capacity_rung_count: 3,
      capacity_target: target,
    },
    metrics: report.metrics,
    failures: report.failures,
    slo_assessment: slo,
  })
  return {
    ...report,
    profile: {
      ...report.profile,
      concurrency: 3,
      slo_ttft_ms: 50,
      slo_target_percent: 90,
      capacity_enabled: true,
      capacity_start: 1,
      capacity_step: 1,
    },
    progress: {
      ...report.progress,
      capacity_rung_number: 2,
      capacity_rung_count: 3,
      capacity_target: 2,
    },
    request_budget: { limit: 10_000, warmup_cap: 0, ramp_cap: 0, measured_cap: 12, total_cap: 12 },
    slo_assessment: passed,
    capacity_result: {
      status: "failed",
      selected_rung_index: 1,
      highest_passing_rung_index: 1,
      rungs: [rung(0, 1), rung(1, 2), rung(2, 3, failed)],
    },
  }
}

function phaseThreePerformanceReport(): QuickPerformanceReport {
  const report = successfulPerformanceReport()
  const traffic = {
    request_cap: 1,
    offered: 1,
    launched: 1,
    completed: 1,
    succeeded: 1,
    failed: 0,
    timed_out: 0,
    rejected: 0,
    peak_in_flight: 1,
    prompt_tokens: 20,
    completion_tokens: 32,
    cached_tokens: 5,
    send_duration_ms: 50,
    drain_duration_ms: 10,
    total_duration_ms: 60,
    failures: [],
    stopped: false,
    capped: false,
  }
  return {
    ...report,
    profile: {
      ...report.profile,
      warmup_requests: 1,
      ramp_duration_ms: 1_000,
      ramp_request_cap: 2,
      slice_duration_ms: 1_000,
    },
    progress: { ...report.progress, capped: false },
    request_budget: { limit: 10_000, warmup_cap: 1, ramp_cap: 2, measured_cap: 4, total_cap: 7 },
    warmup: traffic,
    ramp: {
      shape: "linear_staircase",
      duration_ms: 1_000,
      steps: 2,
      target_concurrency: 2,
      completed_window: false,
      traffic: {
        ...traffic,
        request_cap: 2,
        offered: 2,
        launched: 2,
        completed: 2,
        succeeded: 2,
        capped: true,
      },
    },
    time_slices: [
      performanceTimeSlice(0, 0, 1_000, false),
      performanceTimeSlice(2, 2_000, 2_500, true),
    ],
  }
}

function performanceTimeSlice(
  sliceIndex: number,
  startMS: number,
  endMS: number,
  partial: boolean,
) {
  return {
    slice_index: sliceIndex,
    start_ms: startMS,
    end_ms: endMS,
    partial,
    offered: 1,
    launched: 1,
    completed: 1,
    succeeded: 1,
    failed: 0,
    rejected: 0,
    prompt_tokens: 20,
    completion_tokens: 32,
    cached_tokens: 5,
    ttft: { count: 1, p50_ms: 30, p95_ms: 40, p99_ms: 45 },
    tpot: { count: 1, p50_ms: 4, p95_ms: 6, p99_ms: 7 },
    e2e: { count: 1, p50_ms: 60, p95_ms: 80, p99_ms: 90 },
  }
}

function mixedPerformanceReport(): QuickPerformanceReport {
  const report = successfulPerformanceReport()
  return {
    ...report,
    success: false,
    progress: { ...report.progress, succeeded: 3, failed: 1 },
    metrics: { ...report.metrics, succeeded: 3, failed: 1, success_rate_percent: 75 },
    failures: [{ error_code: "authentication_failed", count: 1 }],
    samples: report.samples.map((sample, index) =>
      index === 0
        ? {
            ...sample,
            success: false,
            http_status: 401,
            error_code: "authentication_failed",
            response_evidence: {
              capture_status: "captured",
              content_type: "application/json",
              request_id: "req-safe-123",
              body: '{"error":{"message":"quota exhausted","api_key":"[REDACTED]"}}',
              body_bytes: 96,
              truncated: true,
              redacted: true,
            },
          }
        : sample,
    ),
  }
}
