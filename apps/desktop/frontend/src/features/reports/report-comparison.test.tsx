import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { reportComparisonFixture } from "@/test/report-comparison-fixture"
import { protocolReportSnapshot, reportID } from "@/test/protocol-report-fixture"
import { parseReportComparison, comparisonReportID } from "./report-comparison-data"
import { ReportWorkspace } from "./report-workspace"
import { ReportComparisonView } from "./report-comparison-view"

function fixture() {
  const comparison = reportComparisonFixture()
  const ids = comparison.reports.map(comparisonReportID)
  const snapshot = { schema_version: 1 as const, reports: comparison.reports.flatMap(detail => detail.source === "run" ? protocolReportSnapshot(detail).reports : []) }
  const compareReports = vi.fn(async () => comparison)
  return { comparison, ids, snapshot, compareReports }
}

describe("report comparison", () => {
  it("renders separate suite tables, preserves repeated names and missing cases, and exports the filtered sections", async () => {
    const data = fixture(); const user = userEvent.setup()
    for (const detail of data.comparison.reports) if (detail.source === "run") {
      for (const [entryID, name] of [[reportID(40), "Snapshot suite"], [reportID(60), "Another suite"]]) {
        detail.entries.push({ ...structuredClone(detail.entries[0]), entry_id: entryID, name })
      }
    }
    const first = data.comparison.rows[0]
    first.cells[1].status = "not_run"
    data.comparison.rows.push(
      { ...structuredClone(first), key: "right-only", name: "Only on right", cells: [
        { report_id: data.ids[0], entry_id: "", case_id: "", status: "not_run", change: "missing", metrics: [] },
        { ...first.cells[1], status: "failed" },
      ] },
      { ...structuredClone(first), key: "repeated-suite", name: "Repeated suite case", cells: first.cells.map(cell => ({ ...cell, entry_id: reportID(40) })) },
      { ...structuredClone(first), key: "another-suite", name: "Unchanged suite case", entry_name: "Another suite", different: false,
        cells: first.cells.map(cell => ({ ...cell, entry_id: reportID(60) })) },
    )
    const original = structuredClone(data.comparison)
    const documents: HTMLElement[] = []
    const save = vi.fn(async () => true)
    const visual = vi.fn<NonNullable<React.ComponentProps<typeof ReportComparisonView>["exportVisualReport"]>>(async element => {
      documents.push(element.cloneNode(true) as HTMLElement)
      return { blob: new Blob(["html"]), filename: "comparison.html", mediaType: "text/html" }
    })
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportComparisonView reportIDs={data.ids} compareReports={data.compareReports}
      saveReportExport={save} copyReportPNG={vi.fn()} exportVisualReport={visual} /></I18nextProvider>)
    await waitFor(() => expect(screen.getAllByRole("table", { name: "报告对比结果" })).toHaveLength(3))
    const tables = screen.getAllByRole("table", { name: "报告对比结果" })
    expect(screen.getAllByRole("heading", { name: "Snapshot suite" })).toHaveLength(2)
    expect(screen.getByRole("heading", { name: "Another suite" })).toBeInTheDocument()
    expect(tables[0]).toHaveTextContent("Only on right")
    expect(tables[0]).not.toHaveTextContent("Repeated suite case")
    expect(tables[1]).toHaveTextContent("Repeated suite case")
    expect(within(tables[0]).getByText("未测")).toHaveAttribute("data-slot", "badge")
    expect(within(tables[0]).getByText("本报告未覆盖")).toHaveAttribute("data-slot", "badge")
    for (const table of tables) expect(within(table).queryByText("Snapshot suite")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledOnce())
    expect(documents[0].querySelectorAll('table[aria-label="报告对比结果"]')).toHaveLength(3)
    await user.click(screen.getByRole("checkbox", { name: "仅看差异" }))
    expect(screen.getAllByRole("table", { name: "报告对比结果" })).toHaveLength(2)
    expect(screen.queryByRole("heading", { name: "Another suite" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2))
    expect(documents[1].querySelectorAll('table[aria-label="报告对比结果"]')).toHaveLength(2)
    expect(data.comparison).toEqual(original)
  })

  it("opens export modes in a popover, preserves the watermark, and restores keyboard focus", async () => {
    const data = fixture(); const user = userEvent.setup()
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportComparisonView reportIDs={data.ids} compareReports={data.compareReports}
      saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    await screen.findByRole("table", { name: "报告对比结果" })
    const trigger = screen.getByRole("button", { name: "报告导出" })
    expect(screen.queryByRole("dialog", { name: "报告导出" })).not.toBeInTheDocument()
    await user.click(trigger)
    const panel = screen.getByRole("dialog", { name: "报告导出" })
    for (const name of ["HTML", "PNG", "PDF", "JSON", "复制 PNG"]) expect(within(panel).getByRole("button", { name })).toBeEnabled()
    const watermark = within(panel).getByRole("textbox", { name: "导出水印" })
    await user.clear(watermark); await user.type(watermark, "kept watermark")
    await user.keyboard("{Escape}")
    expect(screen.queryByRole("dialog", { name: "报告导出" })).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
    await user.click(trigger)
    expect(screen.getByRole("textbox", { name: "导出水印" })).toHaveValue("kept watermark")
    await user.click(screen.getByRole("checkbox", { name: "仅看差异" }))
    expect(screen.queryByRole("dialog", { name: "报告导出" })).not.toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: "仅看差异" })).toBeChecked()
  })

  it("selects independent reports from the same channel and preserves selection across empty search and back navigation", async () => {
    const data = fixture(); const user = userEvent.setup()
    const getDetail = vi.fn()
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace snapshot={data.snapshot} compareReports={data.compareReports}
      getDetail={getDetail} exportReport={vi.fn()} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    expect(screen.getByRole("button", { name: "对比报告（0）" })).toBeDisabled()
    const checkboxes = screen.getAllByRole("checkbox", { name: /^选择报告：/ })
    await user.click(checkboxes[1]); expect(screen.getByRole("button", { name: "对比报告（1）" })).toBeDisabled()
    await user.click(checkboxes[0])
    expect(data.compareReports).not.toHaveBeenCalled(); expect(getDetail).not.toHaveBeenCalled()
    await user.type(screen.getByRole("searchbox"), "no matching report")
    await user.click(screen.getByRole("button", { name: "对比报告（2）" }))
    const table = await screen.findByRole("table", { name: "报告对比结果" })
    expect(within(table).getAllByRole("columnheader")).toHaveLength(3)
    expect(table.querySelectorAll("time")).toHaveLength(0)
    expect((screen.getByRole("combobox", { name: "基准报告" }) as HTMLInputElement).value).not.toMatch(/2026|GMT/)
    expect(table).not.toHaveTextContent("新增失败")
    expect(table).toHaveTextContent("必过")
    await user.click(screen.getByRole("checkbox", { name: "仅看差异" }))
    expect(screen.queryByText("Unchanged case")).not.toBeInTheDocument()
    expect(within(table).queryByText("E2E")).not.toBeInTheDocument()
    expect(screen.queryByRole("table", { name: "逐条断言结果" })).not.toBeInTheDocument()
    const details = screen.getByRole("button", { name: "Reject invalid parameter" })
    const cells = details.closest("tr")!.querySelectorAll(":scope > td")
    expect(within(cells[0] as HTMLElement).getAllByRole("button")).toHaveLength(1)
    expect(within(cells[1] as HTMLElement).queryByRole("button")).not.toBeInTheDocument()
    expect(within(cells[2] as HTMLElement).queryByRole("button")).not.toBeInTheDocument()
    expect(cells[1]).toHaveTextContent("必过通过")
    expect(cells[2]).toHaveTextContent("必过失败")
    const collapseButtons = screen.getAllByRole("button", { name: "折叠渠道：test-channel" })
    await user.click(collapseButtons[1])
    expect(within(table).getAllByRole("columnheader")).toHaveLength(2)
    expect(collapseButtons[0]).toBeDisabled()
    expect(table).not.toHaveTextContent("失败")
    await user.click(screen.getByRole("button", { name: "展开渠道：test-channel" }))
    expect(within(table).getAllByRole("columnheader")).toHaveLength(3)
    expect(details).toHaveAttribute("aria-expanded", "false")
    await user.click(details)
    expect(details).toHaveAttribute("aria-expanded", "true")
    expect(within(table).getAllByText("E2E").length).toBeGreaterThan(0)
    expect(screen.getAllByRole("table", { name: "逐条断言结果" }).length).toBeGreaterThan(0)
    await user.click(details)
    expect(details).toHaveAttribute("aria-expanded", "false")
    expect(within(table).queryByText("E2E")).not.toBeInTheDocument()
    expect(screen.queryByRole("table", { name: "逐条断言结果" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "返回报告列表" }))
    expect(screen.getByRole("searchbox")).toHaveValue("no matching report")
    expect(screen.getByRole("button", { name: "对比报告（2）" })).toBeEnabled()
    await user.click(screen.getByRole("button", { name: "清除选择" }))
    expect(screen.getByRole("button", { name: "对比报告（0）" })).toBeDisabled()
  })

  it("changes the baseline through Core and ignores an outdated response", async () => {
    const data = fixture(); const user = userEvent.setup()
    let finish!: (value: typeof data.comparison) => void
    const compare = vi.fn().mockResolvedValueOnce(data.comparison).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const rendered = render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportComparisonView reportIDs={data.ids} compareReports={compare} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    await screen.findByRole("table", { name: "报告对比结果" })
    const baseline = screen.getByRole("combobox", { name: "基准报告" })
    const label = (baseline as HTMLInputElement).value
    await user.clear(baseline)
    await user.type(baseline, label)
    const options = await screen.findAllByRole("option")
    await user.click(options[1])
    await waitFor(() => expect(compare).toHaveBeenLastCalledWith(data.ids, data.ids[1]))
    expect(screen.getByRole("status")).toHaveTextContent("正在读取并对齐报告")
    rendered.unmount()
    await act(async () => finish(data.comparison))
  })

  it("shows a safe read failure, retries, and exports complete comparison JSON", async () => {
    const data = fixture(); const user = userEvent.setup(); const save = vi.fn<React.ComponentProps<typeof ReportComparisonView>["saveReportExport"]>(async () => true)
    data.compareReports.mockRejectedValueOnce(new Error("private provider payload"))
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportComparisonView reportIDs={data.ids} compareReports={data.compareReports} saveReportExport={save} copyReportPNG={vi.fn()} /></I18nextProvider>)
    expect(await screen.findByRole("alert")).not.toHaveTextContent("private provider payload")
    await user.click(screen.getByRole("button", { name: "重新读取" }))
    await screen.findByRole("table", { name: "报告对比结果" })
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    await user.click(screen.getByRole("button", { name: "JSON" }))
    await waitFor(() => expect(save).toHaveBeenCalledOnce())
    const exported = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(save.mock.calls[0][2]), char => char.charCodeAt(0))))
    expect(exported.baseline_id).toBe(data.ids[0]); expect(exported.reports).toHaveLength(2)
  })

  it("uses the same compact results and expanded details for visual exports and copying", async () => {
    const data = fixture(); const user = userEvent.setup()
    if (data.comparison.reports[1].source === "run") data.comparison.reports[1].entries[0].cases[0].severity = "normal"
    const save = vi.fn(async () => true); const copy = vi.fn(async () => {})
    const documents: HTMLElement[] = []
    const visual = vi.fn<NonNullable<React.ComponentProps<typeof ReportComparisonView>["exportVisualReport"]>>(async (element, format) => {
      documents.push(element.cloneNode(true) as HTMLElement)
      return { blob: new Blob(["report"], { type: "image/png" }), filename: `comparison.${format}`, mediaType: "image/png" }
    })
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportComparisonView reportIDs={data.ids} compareReports={data.compareReports}
      saveReportExport={save} copyReportPNG={copy} exportVisualReport={visual} /></I18nextProvider>)
    const table = await screen.findByRole("table", { name: "报告对比结果" })
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    for (const [index, label] of ["HTML", "PNG", "PDF"].entries()) {
      await user.click(screen.getByRole("button", { name: label }))
      await waitFor(() => expect(save).toHaveBeenCalledTimes(index + 1))
    }
    await user.click(screen.getByRole("button", { name: "复制 PNG" }))
    await waitFor(() => expect(copy).toHaveBeenCalledOnce())
    expect(visual.mock.calls.map(call => call[1])).toEqual(["html", "png", "pdf", "png"])
    for (const document of documents) {
      expect(document).not.toHaveTextContent("新增失败")
      expect(document).toHaveTextContent("必过通过")
      expect(document).toHaveTextContent("容忍失败")
      expect(document).toHaveTextContent("Unchanged case")
      expect(document.querySelector("thead")).not.toHaveTextContent(/2026|GMT/)
      expect(document.querySelector("time")).toBeNull()
      expect(document.querySelector("button")).toBeNull()
      expect(document).not.toHaveTextContent("E2E")
    }
    await user.click(within(table).getByRole("button", { name: "Reject invalid parameter" }))
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledTimes(4))
    expect(documents[4]).toHaveTextContent("E2E")
    expect(within(documents[4]).getAllByRole("table", { name: "逐条断言结果" }).length).toBeGreaterThan(0)
    expect(documents[4].querySelector("thead")).not.toHaveTextContent(/2026|GMT/)
    await user.click(screen.getAllByRole("button", { name: "折叠渠道：test-channel" })[1])
    await user.click(screen.getByRole("button", { name: "报告导出" }))
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledTimes(5))
    expect(documents[5].querySelector("table")?.querySelectorAll(":scope > thead > tr > th")).toHaveLength(2)
    expect(documents[5]).toHaveTextContent("必过通过")
    expect(documents[5]).not.toHaveTextContent("容忍失败")
  })

  it("accepts the closed DTO and rejects substituted identities or invalid changes", () => {
    const data = fixture()
    expect(parseReportComparison(data.comparison).reports).toHaveLength(2)
    for (const mutate of [
      (value: typeof data.comparison) => { value.baseline_id = reportID(99) },
      (value: typeof data.comparison) => { value.rows[0].cells[1].report_id = reportID(99) },
      (value: typeof data.comparison) => { value.rows[0].cells[1].entry_id = reportID(99) },
      (value: typeof data.comparison) => { value.rows[0].cells[1].change = "conditions_changed" },
    ]) {
      const invalid = structuredClone(data.comparison); mutate(invalid)
      expect(() => parseReportComparison(invalid)).toThrow()
    }
  })

  it("bounds multiselection and removes unavailable reports from the active selection", async () => {
    const data = fixture(); const user = userEvent.setup()
    data.snapshot.reports = Array.from({ length: 9 }, (_, index) => ({ ...data.snapshot.reports[0], id: reportID(100 + index), run_id: reportID(200 + index), channel_name: `Channel ${index}` }))
    const props = { snapshot: data.snapshot, compareReports: data.compareReports, getDetail: vi.fn(), exportReport: vi.fn(), saveReportExport: vi.fn(), copyReportPNG: vi.fn() }
    const rendered = render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace {...props} /></I18nextProvider>)
    expect(screen.getByRole("checkbox", { name: "选择当前显示的报告" })).toBeDisabled()
    const checkboxes = screen.getAllByRole("checkbox", { name: /^选择报告：/ })
    for (const checkbox of checkboxes.slice(0, 8)) await user.click(checkbox)
    expect(checkboxes[8]).toBeDisabled()
    expect(screen.getByRole("button", { name: "对比报告（8）" })).toBeEnabled()
    rendered.rerender(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace {...props} snapshot={{ ...data.snapshot, reports: [] }} /></I18nextProvider>)
    expect(screen.getByRole("button", { name: "对比报告（0）" })).toBeDisabled()
    expect(data.compareReports).not.toHaveBeenCalled()
  })
})
