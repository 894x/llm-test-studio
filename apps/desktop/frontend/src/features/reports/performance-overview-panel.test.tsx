import { render, screen, within } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { performanceOverviewFixture } from "@/test/performance-overview-fixture"
import { PerformanceOverviewPanel } from "./performance-overview-panel"

describe("PerformanceOverviewPanel", () => {
  it("presents Core values, counts, individual statuses and reference limits without recomputing them", () => {
    const overview = performanceOverviewFixture()
    const before = structuredClone(overview)
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><PerformanceOverviewPanel overview={overview} /></I18nextProvider>)
    const panel = screen.getByRole("region", { name: "关键性能指标" })
    expect(panel).toHaveTextContent("3 / 5 项达标")
    expect(panel).toHaveTextContent("另有 1 项仅观测")
    expect(panel).toHaveTextContent("100 次请求")
    const tpot = panel.querySelector('[data-performance-metric="tpot_p95"]') as HTMLElement
    expect(tpot).toHaveTextContent("42.60ms")
    expect(tpot).toHaveTextContent("参考上限 ≤ 40.00 ms")
    expect(within(tpot).getByText("未达标")).toHaveClass("text-outcome-fail")
    expect(within(panel).getByText("0.65")).toBeInTheDocument()
    expect(overview).toEqual(before)
  })
  it("renders missing observations and unassessed runs without a false zero or pass", () => {
    const overview = performanceOverviewFixture()
    overview.status = "not_evaluated"
    overview.passed_count = 0; overview.evaluated_count = 0; overview.observed_count = 0; overview.unevaluated_count = 6
    overview.metrics.forEach(metric => { metric.status = "unavailable"; metric.value = null })
    render(<I18nextProvider i18n={createAppI18n("en-US")}><PerformanceOverviewPanel overview={overview} /></I18nextProvider>)
    expect(screen.getAllByText("—")).toHaveLength(6)
    expect(screen.getByText("6 unevaluated metrics")).toBeInTheDocument()
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument()
    expect(screen.queryByText("0.00")).not.toBeInTheDocument()
  })
})
