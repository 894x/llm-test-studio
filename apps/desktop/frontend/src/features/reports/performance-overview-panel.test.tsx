import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { TooltipProvider } from "@/components/ui/tooltip"
import { performanceOverviewFixture } from "@/test/performance-overview-fixture"
import { PerformanceOverviewPanel } from "./performance-overview-panel"

describe("PerformanceOverviewPanel", () => {
  it("presents Core values, counts, individual statuses and reference limits without recomputing them", () => {
    const overview = performanceOverviewFixture()
    const before = structuredClone(overview)
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><PerformanceOverviewPanel overview={overview} /></I18nextProvider>, { wrapper: TooltipProvider })
    const panel = screen.getByRole("region", { name: "关键性能指标" })
    expect(panel).toHaveTextContent("3 / 6 项达标")
    expect(panel).toHaveTextContent("100 次请求")
    const tpot = panel.querySelector('[data-performance-metric="tpot_p95"]') as HTMLElement
    expect(tpot).toHaveTextContent("42.60ms")
    expect(tpot).toHaveTextContent("参考目标 < 40.00 ms")
    expect(panel.querySelector('[data-performance-metric="success_rate"]')).toHaveTextContent("参考目标 ≥ 99.0%")
    expect(panel.querySelector('[data-performance-metric="ttft_p95"]')).toHaveTextContent("参考目标 < 15.00 s")
    expect(panel.querySelector('[data-performance-metric="e2e_p95"]')).toHaveTextContent("参考目标 < 30.00 s")
    expect(panel.querySelector('[data-performance-metric="successful_qps"]')).toHaveTextContent("参考目标 ≥ 2.00 req/s")
    expect(within(tpot).getByText("未达标")).toHaveClass("text-outcome-fail")
    expect(within(panel).getByText("0.65")).toBeInTheDocument()
    expect(screen.getByRole("img", { name: "渠道评分 4.38 / 5，6 项参与" })).toBeInTheDocument()
    const status = within(panel).getByText("部分指标未达标")
    expect(status.lastElementChild).toHaveClass("lucide-x")
    expect(overview).toEqual(before)
  })
  it("renders missing observations and unassessed runs without a false zero or pass", () => {
    const overview = performanceOverviewFixture()
    overview.status = "not_evaluated"
    overview.rating = null
    overview.passed_count = 0; overview.evaluated_count = 0; overview.observed_count = 0; overview.unevaluated_count = 6
    overview.metrics.forEach(metric => { metric.status = "unavailable"; metric.value = null })
    render(<I18nextProvider i18n={createAppI18n("en-US")}><PerformanceOverviewPanel overview={overview} /></I18nextProvider>)
    expect(screen.getAllByText("—")).toHaveLength(6)
    expect(screen.getByText("6 unevaluated metrics")).toBeInTheDocument()
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument()
    expect(screen.queryByText("0.00")).not.toBeInTheDocument()
    expect(screen.getByText("Channel rating unavailable")).toBeInTheDocument()
    expect(screen.queryByRole("img")).not.toBeInTheDocument()
  })
  it("shows a right-side pass icon and exposes the rating rule through keyboard focus", async () => {
    const overview = performanceOverviewFixture()
    overview.status = "passed"; overview.passed_count = 6
    overview.metrics.forEach(metric => { if (metric.status === "failed") metric.status = "passed" })
    overview.rating = { method: "target_ratio_v1", score: 5, max_score: 5, metric_count: 6 }
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><PerformanceOverviewPanel overview={overview} /></I18nextProvider>, { wrapper: TooltipProvider })
    expect(screen.getByText("已评估指标达标").lastElementChild).toHaveClass("lucide-check")
    await userEvent.tab()
    expect(screen.getByRole("img", { name: "渠道评分 5.00 / 5，6 项参与" })).toHaveFocus()
    expect(await screen.findByRole("tooltip")).toHaveTextContent("评分不替代达标结论")
  })
})
