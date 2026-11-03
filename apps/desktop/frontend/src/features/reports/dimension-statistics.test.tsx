import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import { protocolReportFixture, protocolReportSnapshot } from "@/test/protocol-report-fixture"
import { DimensionStatistics } from "./dimension-statistics"
import { ReportWorkspace } from "./report-workspace"

describe("dimension compatibility statistics", () => {
  it("switches all radar labels and details live while preserving custom dimensions and source data", async () => {
    const user = userEvent.setup()
    const i18n = createAppI18n("zh-CN")
    const dimensions = ["cache", "messages", "multimodal", "parameters", "protocol", "reasoning",
      "reasoning-parameters", "safety", "structured-output", "tools", "workflow", "glm53.cache", "my.custom:cache", "toString"]
    const statistics = dimensions.map(dimension => ({ dimension, case_count: 1, passed_case_count: 1,
      failed_case_count: 0, observed_case_count: 0, indeterminate_case_count: 0, not_run_case_count: 0, pass_rate_percent: 100 }))
    const before = structuredClone(statistics)
    render(<I18nextProvider i18n={i18n}><DimensionStatistics statistics={statistics} /></I18nextProvider>)
    const chart = screen.getByRole("group", { name: "维度通过率雷达图" })
    const geometry = chart.querySelector("[data-radar-area]")?.getAttribute("points")
    for (const label of ["缓存", "消息", "多模态", "参数", "协议", "推理", "推理参数", "安全", "结构化输出", "工具调用", "工作流", "GLM 5.3 缓存", "my.custom:cache", "toString"]) {
      expect(chart).toHaveTextContent(label)
    }
    await user.tab()
    expect(await screen.findByRole("tooltip")).toHaveTextContent("缓存：100%")
    await act(async () => { await i18n.changeLanguage("en-US") })
    expect(chart).toHaveAccessibleName("Dimension pass rate radar chart")
    expect(screen.getByRole("tooltip")).toHaveTextContent("Cache: 100%")
    for (const label of ["Cache", "Messages", "Multimodal", "Parameters", "Protocol", "Reasoning", "Reasoning parameters", "Safety", "Structured output", "Tools", "Workflow", "GLM 5.3 cache", "my.custom:cache", "toString"]) {
      expect(chart).toHaveTextContent(label)
    }
    expect(chart.querySelector("[data-radar-area]")?.getAttribute("points")).toBe(geometry)
    expect(statistics).toEqual(before)
  })
  it.each(["en-US", "zh-CN"] as const)("plots Core rates and leaves unverified dimensions without a value in %s", locale => {
    render(<I18nextProvider i18n={createAppI18n(locale)}><DimensionStatistics statistics={[
      { dimension: "tools", case_count: 7, passed_case_count: 3, failed_case_count: 1,
        observed_case_count: 1, indeterminate_case_count: 1, not_run_case_count: 1, pass_rate_percent: 75 },
      { dimension: "multimodal", case_count: 1, passed_case_count: 0, failed_case_count: 0,
        observed_case_count: 1, indeterminate_case_count: 0, not_run_case_count: 0, pass_rate_percent: null },
    ]} /></I18nextProvider>)
    expect(within(screen.getByRole("img", { name: /Tools:|工具调用：/ })).getByText("75%")).toBeVisible()
    const chart = screen.getByRole("group")
    expect(chart.querySelectorAll("[data-radar-point]")).toHaveLength(1)
    expect(chart.querySelector("[data-radar-area]")).toBeNull()
    expect(screen.getByRole("img", { name: /Tools:|工具调用：/ })).toHaveAccessibleName(/75%/)
    expect(screen.getByRole("img", { name: /Multimodal:|多模态：/ })).toHaveAccessibleName(/No data|无数据/)
    expect(screen.queryByRole("table")).not.toBeInTheDocument()
  })

  it("includes the summary and frozen case dimension in the report and its visual export", async () => {
    const detail = protocolReportFixture()
    detail.dimension_statistics = [{ dimension: "parameters", case_count: 1, passed_case_count: 1,
      failed_case_count: 0, observed_case_count: 0, indeterminate_case_count: 0, not_run_case_count: 0, pass_rate_percent: 100 }]
    const user = userEvent.setup()
    const i18n = createAppI18n("en-US")
    let exportedDocument: HTMLElement | undefined
    const save = vi.fn(async () => true)
    const visualExport = vi.fn(async (element: HTMLElement) => {
      exportedDocument = element.cloneNode(true) as HTMLElement
      return { filename: "report.html", mediaType: "text/html", blob: new Blob(["report"]) }
    })
    render(<I18nextProvider i18n={i18n}><ReportWorkspace
      snapshot={protocolReportSnapshot(detail)} preferredReportID={detail.report.id} getDetail={async () => detail}
      exportReport={vi.fn()} saveReportExport={save} copyReportPNG={vi.fn()}
      exportVisualReport={visualExport} /></I18nextProvider>)
    const radar = await screen.findByRole("group", { name: "Dimension pass rate radar chart" })
    expect(radar).toHaveTextContent("Parameters")
    const overview = radar.closest("[data-report-functional-overview]")!
    expect(overview.querySelector('dl[aria-label="Key case results"]')).not.toBeNull()
    expect(screen.getByRole("table", { name: "Cases in execution entry" })).toHaveTextContent("parameters")
    await act(async () => { await i18n.changeLanguage("zh-CN") })
    expect(radar).toHaveTextContent("参数")
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledOnce())
    expect(exportedDocument).toBeDefined()
    expect(within(exportedDocument!).getByRole("group", { name: "维度通过率雷达图" })).toHaveTextContent("参数")
    expect(within(exportedDocument!).getByRole("table", { name: "执行项用例结果" })).toHaveTextContent("parameters")
  })

  it("keeps older detail and performance-only reports free of invented statistics", () => {
    const { container } = render(<I18nextProvider i18n={createAppI18n("en-US")}><DimensionStatistics /></I18nextProvider>)
    expect(container).toBeEmptyDOMElement()
  })

  it("draws an area only for three or more measured axes and exposes case counts on keyboard focus", async () => {
    const user = userEvent.setup()
    const statistics = [100, 50, 0].map((rate, index) => ({ dimension: ["tools", "protocol", "multimodal"][index],
      case_count: 2, passed_case_count: rate / 50, failed_case_count: (100 - rate) / 50,
      observed_case_count: 0, indeterminate_case_count: 0, not_run_case_count: 0, pass_rate_percent: rate }))
    const view = render(<I18nextProvider i18n={createAppI18n("en-US")}><DimensionStatistics statistics={statistics} /></I18nextProvider>)
    const chart = screen.getByRole("group", { name: "Dimension pass rate radar chart" })
    expect(chart.querySelector("[data-radar-area]")).not.toBeNull()
    expect(chart.querySelectorAll("[data-radar-point]")).toHaveLength(3)
    await user.tab()
    expect(screen.getByRole("img", { name: /Tools: 100%/ })).toHaveFocus()
    expect(await screen.findByRole("tooltip")).toHaveTextContent("2 passed, 0 failed")
    await user.keyboard("{Escape}")
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument()
    view.rerender(<I18nextProvider i18n={createAppI18n("en-US")}><DimensionStatistics statistics={[
      statistics[0], { ...statistics[1], pass_rate_percent: null }, statistics[2],
    ]} /></I18nextProvider>)
    expect(chart.querySelector("[data-radar-area]")).toBeNull()
    expect(chart.querySelectorAll("[data-radar-point]")).toHaveLength(2)
  })
})
