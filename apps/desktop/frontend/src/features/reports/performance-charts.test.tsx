import { render as testingRender, screen, within } from "@testing-library/react"
import type { ReactElement } from "react"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import { PerformanceCharts, type PerformanceChartPercentiles, type PerformanceChartSample } from "./performance-charts"

const PERCENTILES: PerformanceChartPercentiles = {
  ttft_p50_ms: 20, ttft_p95_ms: 20,
  tpot_p50_ms: 2, tpot_p95_ms: 2,
  e2e_p50_ms: 60, e2e_p95_ms: 60,
}

function render(ui: ReactElement) {
  const instance = createAppI18n("zh-CN")
  const result = testingRender(<I18nextProvider i18n={instance}>{ui}</I18nextProvider>)
  return {
    ...result,
    rerender: (next: ReactElement) => result.rerender(<I18nextProvider i18n={instance}>{next}</I18nextProvider>),
  }
}

describe("PerformanceCharts", () => {
  it("weights cache hits by input tokens in the same completion windows as throughput", () => {
    render(<PerformanceCharts samples={[
      { ...sample(true, 1_100), prompt_tokens: 100, cached_tokens: 100 },
      { ...sample(true, 1_200), prompt_tokens: 900, cached_tokens: 0 },
      { ...sample(false, 1_300), prompt_tokens: 10_000, cached_tokens: 10_000 },
      { ...sample(true, 8_000), prompt_tokens: 200, cached_tokens: 0 },
    ]} percentiles={PERCENTILES} />)

    const chart = screen.getByRole("img", { name: "缓存命中率时间线，2 个有效时间窗口" })
    expect(within(chart).getByText("1 s–2 s：10%（缓存 100 / 输入 1,000 Token）")).toBeInTheDocument()
    expect(within(chart).getByText("7 s–8 s：0%（缓存 0 / 输入 200 Token）")).toBeInTheDocument()
    expect(chart.querySelectorAll("circle")).toHaveLength(2)
    expect(chart.querySelector("path")?.getAttribute("d")).not.toContain("L")
    expect(screen.getByRole("img", { name: "吞吐与并发时间线，4 个完成请求" })).toBeInTheDocument()
  })

  it("leaves missing, invalid, zero-input and failed token observations unavailable", () => {
    render(<PerformanceCharts samples={[
      sample(true, 1_000),
      { ...sample(true, 2_000), prompt_tokens: 0, cached_tokens: 0 },
      { ...sample(true, 3_000), prompt_tokens: 10, cached_tokens: 11 },
      { ...sample(true, 4_000), prompt_tokens: 10, cached_tokens: Number.NaN },
      { ...sample(false, 5_000), prompt_tokens: 100, cached_tokens: 50 },
    ]} percentiles={PERCENTILES} />)

    const chart = screen.getByRole("figure", { name: "缓存命中率时间线" })
    expect(within(chart).getByText("暂无可计算缓存命中率的 Token 数据")).toBeInTheDocument()
    expect(within(chart).queryByRole("img")).not.toBeInTheDocument()
  })

  it("stacks every metric chart only when the caller requests the quick-test layout", () => {
    const { rerender } = render(<PerformanceCharts samples={[sample(true, 60)]} percentiles={PERCENTILES} />)

    expect(screen.getByTestId("distribution-chart-list")).toHaveClass("sm:grid-cols-3")
    expect(screen.getByTestId("timeline-chart-list")).toHaveClass("sm:grid-cols-3")
    expect(screen.getByRole("img", { name: /TTFT（含推理） 延迟直方分布/ })).toHaveClass("h-28")

    rerender(<PerformanceCharts layout="stacked" samples={[sample(true, 60)]} percentiles={PERCENTILES} />)

    expect(screen.getByTestId("distribution-chart-list")).toHaveClass("grid-cols-1")
    expect(screen.getByTestId("timeline-chart-list")).toHaveClass("grid-cols-1")
    expect(screen.getByRole("img", { name: /TTFT（含推理） 延迟直方分布/ })).toHaveClass("h-36")
  })

  it("plots only successful latency samples so curves match Core percentiles", () => {
    render(<PerformanceCharts samples={[sample(true, 60), sample(false, 600)]} percentiles={PERCENTILES} />)

    expect(screen.getByRole("img", { name: "TTFT（含推理） 延迟直方分布，1 个成功请求样本" })).toBeInTheDocument()
    expect(screen.getByRole("img", { name: "TPOT 随完成时间变化曲线，1 个成功请求样本" })).toBeInTheDocument()
    expect(screen.queryByRole("img", { name: /2 个成功请求样本/ })).not.toBeInTheDocument()
    expect(screen.getByRole("img", { name: "吞吐与并发时间线，2 个完成请求" })).toBeInTheDocument()
  })

  it("shows an explicit empty state when every request failed", () => {
    render(<PerformanceCharts samples={[sample(false, 600)]} percentiles={PERCENTILES} />)

    const ttftDistribution = screen.getByRole("figure", { name: "TTFT（含推理） 分布图" })
    const e2eTimeline = screen.getByRole("figure", { name: "E2E 时间曲线" })
    expect(within(ttftDistribution).getByText("暂无成功请求样本")).toBeInTheDocument()
    expect(within(e2eTimeline).getByText("暂无成功请求样本")).toBeInTheDocument()
    expect(screen.getByRole("img", { name: "吞吐与并发时间线，1 个完成请求" })).toBeInTheDocument()
  })
})

function sample(success: boolean, finishedOffsetMS: number): PerformanceChartSample {
  return {
    request_index: success ? 0 : 1,
    started_offset_ms: finishedOffsetMS - 50,
    finished_offset_ms: finishedOffsetMS,
    e2e_ms: success ? 60 : 600,
    ttft_ms: success ? 20 : 200,
    tpot_ms: success ? 2 : 20,
    success,
  }
}
