import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import type { ComparisonSnapshot } from "@/features/comparisons/data"
import { protocolReportFixture, protocolReportSnapshot, reportID } from "@/test/protocol-report-fixture"
import type { ReportSnapshot } from "./data"
import { ReportWorkspace } from "./report-workspace"

function fixture() {
  const left = protocolReportFixture()
  left.report.channel.name = "渠道 A"
  const right = protocolReportFixture("failed")
  right.report.id = reportID(20)
  right.report.run_id = reportID(21)
  right.report.channel = { id: reportID(22), name: "渠道 B" }
  const snapshot: ReportSnapshot = { schema_version: 1, reports: [
    ...protocolReportSnapshot(left).reports, ...protocolReportSnapshot(right).reports,
  ] }
  const comparisons: ComparisonSnapshot = { schema_version: 1, comparisons: [{
    id: reportID(30), created_at: "2026-10-11T00:00:00Z", status: "completed",
    plan_id: reportID(31), plan_name: "渠道一致性", model_id: left.report.model.id, model_name: left.report.model.name,
    channels: [left, right].map(detail => ({ channel_id: detail.report.channel.id, channel_name: detail.report.channel.name,
      run_id: detail.report.run_id, run_status: detail.report.run_status, report_ready: true,
      passed: detail.report.conclusion.passed, verdict: detail.report.conclusion.verdict, metrics: {} })),
  }] }
  const getDetail = vi.fn(async (id: string) => id === left.report.id ? left : right)
  return { left, right, snapshot, comparisons, getDetail }
}

function mount(data: ReturnType<typeof fixture>, extra: Partial<React.ComponentProps<typeof ReportWorkspace>> = {}) {
  return render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace snapshot={data.snapshot}
    comparisons={data.comparisons} getDetail={data.getDetail} exportReport={vi.fn()} saveReportExport={vi.fn(async () => true)}
    copyReportPNG={vi.fn()} {...extra} /></I18nextProvider>)
}

async function openComparison(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "查看渠道对比报告：test-model" }))
  return (await screen.findAllByRole("table", { name: "渠道对比用例结果" }))[0]
}

describe("channel comparison reports", () => {
  it("lists one comparison, searches both channels, and opens details on demand", async () => {
    const data = fixture(); const user = userEvent.setup()
    mount(data)
    const list = screen.getByRole("table", { name: "测试报告目录" })
    expect(within(list).getAllByRole("row")).toHaveLength(2)
    expect(list).toHaveTextContent("渠道 A · 通过 · 1/1")
    expect(list).toHaveTextContent("渠道 B · 未通过 · 0/1")
    expect(data.getDetail).not.toHaveBeenCalled()
    await user.type(screen.getByRole("searchbox"), "渠道 B")
    const table = await openComparison(user)
    expect(within(table).getAllByRole("columnheader")).toHaveLength(3)
    expect(within(table).getAllByText("Reject invalid parameter")).toHaveLength(1)
    expect(within(table).queryByRole("columnheader", { name: "E2E" })).not.toBeInTheDocument()
    await user.click(within(table).getByRole("button", { name: "查看渠道 B的用例详情：Reject invalid parameter" }))
    expect(within(table).getAllByRole("table", { name: "逐条断言结果" })).toHaveLength(2)
    await user.click(screen.getByRole("button", { name: "返回报告列表" }))
    expect(screen.getByRole("searchbox")).toHaveValue("渠道 B")
  })

  it("folds either side into all columns and keeps a channel visible", async () => {
    const data = fixture(); const user = userEvent.setup(); mount(data)
    await openComparison(user)
    await user.click(screen.getByRole("button", { name: "折叠渠道：渠道 B" }))
    let table = screen.getByRole("table", { name: "执行项用例结果" })
    for (const name of ["E2E", "TTFT", "TPOT", "输出 Token", "执行汇总"]) {
      expect(within(table).getByRole("columnheader", { name })).toBeInTheDocument()
    }
    expect(screen.getByRole("button", { name: "折叠渠道：渠道 A" })).toBeDisabled()
    expect(screen.getByRole("button", { name: "展开渠道：渠道 B" })).toHaveAttribute("aria-expanded", "false")
    await user.click(screen.getByRole("button", { name: "展开渠道：渠道 B" }))
    await user.click(screen.getByRole("button", { name: "折叠渠道：渠道 A" }))
    table = screen.getByRole("table", { name: "执行项用例结果" })
    expect(within(table).getByText("未通过")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "折叠渠道：渠道 B" })).toBeDisabled()
    await user.click(screen.getByRole("button", { name: "展开渠道：渠道 A" }))
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toBeInTheDocument()
  })

  it("aligns by entry and case IDs even when names collide and order changes", async () => {
    const data = fixture(); const user = userEvent.setup()
    const secondLeft = { ...data.left.entries[0].cases[0], case_id: reportID(40) }
    const secondRight = { ...data.right.entries[0].cases[0], case_id: reportID(40), verification: secondLeft.verification }
    data.left.entries[0].cases.push(secondLeft)
    data.right.entries[0].cases.unshift(secondRight)
    const repeated = structuredClone(data.left.entries[0]); repeated.entry_id = reportID(41); repeated.name = "重复套件"
    data.left.entries.push(repeated)
    mount(data)
    await openComparison(user)
    const first = screen.getAllByRole("table", { name: "渠道对比用例结果" })[0]
    const rows = within(first).getAllByRole("row").slice(1)
    expect(within(rows[0]).getAllByText("通过")).toHaveLength(1)
    expect(within(rows[0]).getByText("未通过")).toBeInTheDocument()
    expect(within(rows[1]).getAllByText("通过")).toHaveLength(2)
    const repeatedTable = screen.getAllByRole("table", { name: "渠道对比用例结果" })[1]
    expect(repeatedTable).toHaveTextContent("未执行")
  })

  it("preserves one side while the other report is pending or fails, and retries", async () => {
    const data = fixture(); const user = userEvent.setup()
    data.getDetail.mockRejectedValueOnce(new Error("private provider error"))
    mount(data)
    await openComparison(user)
    expect(screen.getByRole("alert")).not.toHaveTextContent("private provider error")
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("无法读取")
    await user.click(screen.getByRole("button", { name: "重新读取" }))
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("通过")
  })

  it("updates when a pending channel report arrives without reopening", async () => {
    const data = fixture(); const user = userEvent.setup()
    const rightReport = data.snapshot.reports.pop()!
    const rendered = mount(data)
    await openComparison(user)
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("等待报告生成")
    expect(screen.getByRole("button", { name: "HTML" })).toBeDisabled()
    data.snapshot = { ...data.snapshot, reports: [...data.snapshot.reports, rightReport] }
    rendered.rerender(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace snapshot={data.snapshot} comparisons={data.comparisons}
      getDetail={data.getDetail} exportReport={vi.fn()} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    await waitFor(() => expect(screen.getByRole("button", { name: "HTML" })).toBeEnabled())
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("未通过")
  })

  it("exports every case and reflects the folded view", async () => {
    const data = fixture(); const user = userEvent.setup()
    for (const detail of [data.left, data.right]) {
      const seed = detail.entries[0].cases[0]
      detail.entries[0].cases = Array.from({ length: 60 }, (_, i) => ({ ...seed, case_id: reportID(100 + i), name: `Case ${i}` }))
    }
    const visualExport = vi.fn(async (element: HTMLElement) => {
      expect(element).toHaveTextContent("Case 59")
      expect(element.querySelectorAll("thead th")).toHaveLength(3)
      expect(element).toHaveTextContent("rhzs")
      return { filename: "comparison.html", mediaType: "text/html", blob: new Blob(["html"]) }
    })
    const save = vi.fn(async () => true)
    mount(data, { exportVisualReport: visualExport, saveReportExport: save })
    await openComparison(user)
    expect(screen.queryByText("Case 59")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(save).toHaveBeenCalledOnce())
    await user.click(screen.getByRole("button", { name: "折叠渠道：渠道 B" }))
    visualExport.mockImplementationOnce(async element => {
      expect(element.querySelectorAll("thead th")).toHaveLength(8)
      expect(element).toHaveTextContent("Case 59")
      return { filename: "comparison.png", mediaType: "image/png", blob: new Blob(["png"]) }
    })
    await user.click(screen.getByRole("button", { name: "PNG" }))
    await waitFor(() => expect(save).toHaveBeenCalledTimes(2))
  })

  it("still opens an individual channel report from a run shortcut", async () => {
    const data = fixture()
    mount(data, { preferredReportID: data.left.report.id })
    expect(await screen.findByRole("table", { name: "执行项用例结果" })).toBeInTheDocument()
    expect(data.getDetail).toHaveBeenCalledExactlyOnceWith(data.left.report.id)
  })

  it("exports from the list inspector on demand and shares the ordinary report controls", async () => {
    const data = fixture(); const user = userEvent.setup()
    let finishRight!: (value: typeof data.right) => void
    const reader = vi.fn((id: string) => id === data.left.report.id ? Promise.resolve(data.left) :
      new Promise<typeof data.right>(resolve => { finishRight = resolve }))
    const save = vi.fn(async () => true)
    mount(data, { getDetail: reader, saveReportExport: save })
    const inspector = screen.getByRole("complementary", { name: "报告详情" })
    const watermark = within(inspector).getByRole("textbox", { name: "导出水印" })
    expect(watermark).toHaveValue("rhzs")
    expect(reader).not.toHaveBeenCalled()
    await user.click(within(inspector).getByRole("button", { name: "JSON" }))
    await waitFor(() => expect(reader).toHaveBeenCalledTimes(2))
    expect(within(inspector).getByRole("button", { name: "生成中…" })).toBeDisabled()
    expect(watermark).toBeDisabled()
    for (const name of ["HTML", "PNG", "PDF", "复制 PNG"]) {
      expect(within(inspector).getByRole("button", { name })).toBeDisabled()
    }
    finishRight(data.right)
    await waitFor(() => expect(save).toHaveBeenCalledOnce())
    const [filename, mediaType, encoded] = save.mock.calls[0] as unknown as string[]
    expect(filename).toBe(`llm-test-studio-comparison-${data.comparisons.comparisons[0].id}.json`)
    expect(mediaType).toBe("application/json")
    const exported = JSON.parse(atob(encoded))
    expect(exported.reports.map((report: typeof data.left) => report.report.id)).toEqual([data.left.report.id, data.right.report.id])
    expect(screen.getByRole("table", { name: "测试报告目录" })).toBeInTheDocument()
    expect(watermark).toBeEnabled()
  })

  it("shows the shared copy loading state and public export error in the inspector", async () => {
    const data = fixture(); const user = userEvent.setup()
    let fail!: (error: Error) => void
    const visualExport = vi.fn(() => new Promise<never>((_, reject) => { fail = reject }))
    mount(data, { exportVisualReport: visualExport })
    await openComparison(user)
    const inspector = screen.getByRole("complementary", { name: "报告详情" })
    expect(within(inspector).getByRole("button", { name: "HTML" })).toBeInTheDocument()
    await user.click(within(inspector).getByRole("button", { name: "复制 PNG" }))
    await waitFor(() => expect(visualExport).toHaveBeenCalledOnce())
    expect(within(inspector).getByRole("button", { name: "复制中…" })).toBeDisabled()
    fail(new Error("private renderer details"))
    const alert = await within(inspector).findByRole("alert")
    expect(alert).not.toHaveTextContent("private renderer details")
    expect(within(inspector).getByRole("button", { name: "复制 PNG" })).toBeEnabled()
    expect(document.querySelector("[data-report-export-document]")).not.toBeInTheDocument()
  })

  it("keeps slow report reads alive through comparison progress refreshes", async () => {
    const data = fixture(); const user = userEvent.setup()
    let finishRight!: (value: typeof data.right) => void
    const reader = vi.fn((id: string) => id === data.left.report.id ? Promise.resolve(data.left) :
      new Promise<typeof data.right>(resolve => { finishRight = resolve }))
    const rendered = mount(data, { getDetail: reader })
    await openComparison(user)
    await waitFor(() => expect(reader).toHaveBeenCalledTimes(2))
    const refreshed = structuredClone(data.comparisons)
    refreshed.comparisons[0].channels[1].report_ready = false
    rendered.rerender(<I18nextProvider i18n={createAppI18n("zh-CN")}><ReportWorkspace snapshot={data.snapshot} comparisons={refreshed}
      getDetail={reader} exportReport={vi.fn()} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} /></I18nextProvider>)
    finishRight(data.right)
    await waitFor(() => expect(screen.getByRole("button", { name: "HTML" })).toBeEnabled())
    expect(reader).toHaveBeenCalledTimes(2)
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("未通过")
  })

  it("rejects a report from the wrong channel without displaying its result", async () => {
    const data = fixture(); const user = userEvent.setup()
    const reader = vi.fn(async () => data.left)
    mount(data, { getDetail: reader })
    const table = await openComparison(user)
    await screen.findByRole("alert")
    expect(table).toHaveTextContent("无法读取")
    expect(within(table).queryByRole("button", { name: "查看渠道 B的用例详情：Reject invalid parameter" })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "HTML" })).toBeDisabled()
    reader.mockResolvedValueOnce(data.right)
    await user.click(screen.getByRole("button", { name: "重新读取" }))
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
    expect(screen.getByRole("button", { name: "HTML" })).toBeEnabled()
    expect(screen.getByRole("table", { name: "渠道对比用例结果" })).toHaveTextContent("未通过")
  })
})
