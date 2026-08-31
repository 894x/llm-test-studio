import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { PerformanceCharts, type PerformanceChartPercentiles, type PerformanceChartSample } from "./performance-charts"

const PERCENTILES: PerformanceChartPercentiles = {
  ttft_p50_ms: 20, ttft_p95_ms: 20,
  tpot_p50_ms: 2, tpot_p95_ms: 2,
  e2e_p50_ms: 60, e2e_p95_ms: 60,
}

describe("PerformanceCharts", () => {
  it("stacks every metric chart only when the caller requests the quick-test layout", () => {
    const { rerender } = render(<PerformanceCharts samples={[sample(true, 60)]} percentiles={PERCENTILES} />)

    expect(screen.getByTestId("distribution-chart-list")).toHaveClass("sm:grid-cols-3")
    expect(screen.getByTestId("timeline-chart-list")).toHaveClass("sm:grid-cols-3")
    expect(screen.getByRole("img", { name: /TTFT 延迟直方分布/ })).toHaveClass("h-28")

    rerender(<PerformanceCharts layout="stacked" samples={[sample(true, 60)]} percentiles={PERCENTILES} />)

    expect(screen.getByTestId("distribution-chart-list")).toHaveClass("grid-cols-1")
    expect(screen.getByTestId("timeline-chart-list")).toHaveClass("grid-cols-1")
    expect(screen.getByRole("img", { name: /TTFT 延迟直方分布/ })).toHaveClass("h-36")
  })

  it("plots only successful latency samples so curves match Core percentiles", () => {
    render(<PerformanceCharts samples={[sample(true, 60), sample(false, 600)]} percentiles={PERCENTILES} />)

    expect(screen.getByRole("img", { name: "TTFT 延迟直方分布，1 个成功请求样本" })).toBeInTheDocument()
    expect(screen.getByRole("img", { name: "TPOT 随完成时间变化曲线，1 个成功请求样本" })).toBeInTheDocument()
    expect(screen.queryByRole("img", { name: /2 个成功请求样本/ })).not.toBeInTheDocument()
  })

  it("shows an explicit empty state when every request failed", () => {
    render(<PerformanceCharts samples={[sample(false, 600)]} percentiles={PERCENTILES} />)

    const ttftDistribution = screen.getByRole("figure", { name: "TTFT 分布图" })
    const e2eTimeline = screen.getByRole("figure", { name: "E2E 时间曲线" })
    expect(within(ttftDistribution).getByText("暂无成功请求样本")).toBeInTheDocument()
    expect(within(e2eTimeline).getByText("暂无成功请求样本")).toBeInTheDocument()
    expect(screen.queryByRole("img")).not.toBeInTheDocument()
  })
})

function sample(success: boolean, finishedOffsetMS: number): PerformanceChartSample {
  return {
    request_index: success ? 0 : 1,
    finished_offset_ms: finishedOffsetMS,
    e2e_ms: success ? 60 : 600,
    ttft_ms: success ? 20 : 200,
    tpot_ms: success ? 2 : 20,
    success,
  }
}
