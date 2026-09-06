import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { ReportWorkspace } from "./report-workspace"
import { parseReportDetail, type ReportDetail, type ReportSnapshot } from "./data"
import type { QuickPerformanceSLOAssessment } from "@/features/quick-test/data"

describe("ReportWorkspace", () => {
  it("keeps row selection in the inspector and opens report content only from the action column", async () => {
    const user = userEvent.setup()
    const quickID = "77777777-7777-4777-8777-777777777771"
    const snapshot = {
      schema_version: 1,
      reports: [
        {
          id: "66666666-6666-4666-8666-666666666661", source: "run", run_id: "55555555-5555-4555-8555-555555555551",
          generated_at: "2026-08-31T14:00:00Z", run_status: "completed", plan_name: "正式计划", model_name: "gpt-formal", channel_name: "正式渠道",
          passed: true, verdict: "兼容性门禁通过", issue_count: 0, case_count: 1, failed_case_count: 0, attachment_count: 0,
        },
        {
          id: quickID, source: "quick_performance", generated_at: "2026-08-31T14:30:00Z", run_status: "completed", plan_name: "快速性能测试",
          model_name: "gpt-fast", channel_name: "api.example.test", passed: true, verdict: "快速性能测试通过", issue_count: 0, case_count: 3, failed_case_count: 0, attachment_count: 0,
        },
      ],
    } as unknown as ReportSnapshot
    const getDetail = vi.fn(async () => phaseFiveQuickDetail(quickID))
    const exportReport = vi.fn(async (_reportID: string, format: "json" | "html" | "png" | "pdf") => ({
      filename: `llm-test-studio-report-${quickID}.${format}`,
      media_type: "application/json",
      data_base64: "e30=",
    }))
    const saveReportExport = vi.fn(async (_filename: string, _mediaType: string, _dataBase64: string) => true)
    const copyReportPNG = vi.fn(async (_dataBase64: string) => undefined)
    const exportVisualReport = vi.fn(async (element: HTMLElement, format: "html" | "png" | "pdf", reportID: string) => {
      expect(reportID).toBe(quickID)
      expect(element).toHaveTextContent("team-alpha")
      expect(element.querySelectorAll("figure")).toHaveLength(7)
      expect(element.querySelector('[aria-label="TTFT（含推理） 分布图"]')).not.toBeNull()
      expect(element.querySelector('[aria-label="流式时序统计"]')).not.toBeNull()
      expect(element.querySelector('[aria-label="E2E 时间曲线"]')).not.toBeNull()
      const mediaType = format === "html" ? "text/html; charset=utf-8" : format === "png" ? "image/png" : "application/pdf"
      return { filename: `llm-test-studio-report-${quickID}.${format}`, mediaType, blob: new Blob([format], { type: mediaType }) }
    })

    render(<ReportWorkspace snapshot={snapshot} getDetail={getDetail} exportReport={exportReport} saveReportExport={saveReportExport} copyReportPNG={copyReportPNG} exportVisualReport={exportVisualReport} />)

    const table = screen.getByRole("table", { name: "测试报告目录" })
    expect(within(table).getByRole("columnheader", { name: "查看报告" })).toBeInTheDocument()
    expect(screen.queryByRole("region", { name: "归档性能报告" })).not.toBeInTheDocument()

    const quickRow = within(table).getByText("gpt-fast").closest("tr")
    expect(quickRow).not.toBeNull()
    await user.click(within(quickRow as HTMLTableRowElement).getByText("gpt-fast"))
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    expect(screen.getByRole("table", { name: "测试报告目录" })).toBeInTheDocument()
    expect(screen.queryByRole("region", { name: "归档性能报告" })).not.toBeInTheDocument()

    await user.click(within(quickRow as HTMLTableRowElement).getByRole("button", { name: "查看报告：快速性能测试通过" }))

    const archivedReport = await screen.findByRole("region", { name: "归档性能报告" })
    expect(archivedReport).toHaveTextContent("完成（持续时间模式）")
    expect(archivedReport).not.toHaveTextContent("3 / 10,000")
    expect(archivedReport).toHaveTextContent("测试配置")
    expect(archivedReport).toHaveTextContent("持续时间 · 1 s")
    expect(archivedReport).toHaveTextContent("正态分布")
    expect(archivedReport).toHaveTextContent("随机种子")
    expect(archivedReport).toHaveTextContent("424,242")
    expect(archivedReport).toHaveTextContent("共享前缀")
    expect(archivedReport).toHaveTextContent("8 Token")
    expect(archivedReport).toHaveTextContent("采样目标范围（输入 / 输出）")
    expect(archivedReport).toHaveTextContent("19–22 / 22–35")
    expect(archivedReport).toHaveTextContent("热身请求")
    expect(archivedReport).toHaveTextContent("爬坡配置")
    expect(archivedReport).toHaveTextContent("1 s · 上限 2")
    expect(archivedReport).toHaveTextContent("切片粒度")
    expect(archivedReport).toHaveTextContent("准备阶段与预算")
    expect(archivedReport).toHaveTextContent("10,000 / 10,000")
    expect(archivedReport).toHaveTextContent("爬坡窗口未完整执行")
    expect(archivedReport).toHaveTextContent("https://api.example.test/v1/chat/completions")
    expect(archivedReport).toHaveTextContent("峰值在途 / 配置并发")
    expect(archivedReport).toHaveTextContent("2 / 2")
    expect(archivedReport).toHaveTextContent("实际发送")
    expect(archivedReport).toHaveTextContent("成功吞吐")
    expect(archivedReport).not.toHaveTextContent("请求速率")
    expect(archivedReport).toHaveTextContent("失败")
    expect(archivedReport).toHaveTextContent("3,744 TPM")
    expect(within(archivedReport).getByRole("table", { name: "延迟分布统计" })).toHaveTextContent("客户端排队（本地调度延迟）")
    expect(within(archivedReport).getByRole("table", { name: "流式时序统计" })).toHaveTextContent("语义块间隔，非 Token ITL")
    const timeSlices = within(archivedReport).getByRole("table", { name: "时间切片" })
    expect(timeSlices.parentElement).toHaveClass("overflow-x-auto")
    expect(within(timeSlices).getByRole("columnheader", { name: "TTFT P95" })).toBeInTheDocument()
    expect(within(timeSlices).getByRole("columnheader", { name: "TPOT P95" })).toBeInTheDocument()
    expect(within(timeSlices).getByRole("columnheader", { name: "E2E P95" })).toBeInTheDocument()
    expect(within(timeSlices).getByRole("row", { name: /#0/ })).toHaveTextContent(/0–1 s.*3.*3.*3.*0.*0.*60 \/ 96 \/ 0.*20 ms.*2\.15 ms.*86\.6 ms/)
    expect(within(timeSlices).getByRole("row", { name: /#2/ })).toHaveTextContent(/2–2\.5 s · 部分.*0.*0.*0.*0.*0.*0 \/ 0 \/ 0.*—.*—.*—/)
    expect(within(timeSlices).queryByRole("row", { name: /#1/ })).not.toBeInTheDocument()
    expect(screen.queryByRole("table", { name: "测试报告目录" })).not.toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    const charts = screen.getByRole("region", { name: "性能图表" })
    expect(within(charts).getByRole("figure", { name: "TTFT（含推理） 分布图" })).toBeInTheDocument()
    expect(within(charts).getByRole("figure", { name: "TPOT 时间曲线" })).toBeInTheDocument()
    expect(within(charts).getByRole("figure", { name: "E2E 时间曲线" })).toBeInTheDocument()
    expect(within(charts).getByRole("figure", { name: "吞吐与并发时间线" })).toBeInTheDocument()
    expect(within(charts).getByRole("img", { name: "吞吐与并发时间线，3 个完成请求" })).toBeInTheDocument()
    expect(getDetail).toHaveBeenCalledWith(quickID)
    expect(screen.queryByRole("table", { name: "请求级结果" })).not.toBeInTheDocument()

    const watermark = screen.getByRole("textbox", { name: "导出水印" })
    expect(watermark).toHaveValue("rhzs")
    await user.click(screen.getByRole("button", { name: "JSON" }))
    expect(exportReport).toHaveBeenCalledWith(quickID, "json", "rhzs")
    await waitFor(() => expect(saveReportExport).toHaveBeenCalledWith(`llm-test-studio-report-${quickID}.json`, "application/json", "e30="))
    await user.clear(watermark)
    await user.type(watermark, "team-alpha")
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(saveReportExport).toHaveBeenCalledTimes(2))
    expect(exportReport).not.toHaveBeenCalledWith(quickID, "html", "team-alpha")
    await user.click(screen.getByRole("button", { name: "PNG" }))
    await waitFor(() => expect(saveReportExport).toHaveBeenCalledTimes(3))
    await user.click(screen.getByRole("button", { name: "PDF" }))
    await waitFor(() => expect(saveReportExport).toHaveBeenCalledTimes(4))
    await waitFor(() => expect(exportVisualReport).toHaveBeenCalledTimes(3))
    expect(exportVisualReport.mock.calls.map(([, format]) => format)).toEqual(["html", "png", "pdf"])
    expect(saveReportExport.mock.calls.map(([filename]) => filename)).toEqual([
      `llm-test-studio-report-${quickID}.json`,
      `llm-test-studio-report-${quickID}.html`,
      `llm-test-studio-report-${quickID}.png`,
      `llm-test-studio-report-${quickID}.pdf`,
    ])
    await user.click(screen.getByRole("button", { name: "复制 PNG" }))
    await waitFor(() => expect(copyReportPNG).toHaveBeenCalledWith("cG5n"))

    await user.click(screen.getByRole("button", { name: "返回报告列表" }))
    expect(screen.getByRole("table", { name: "测试报告目录" })).toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    expect(screen.queryByRole("region", { name: "归档性能报告" })).not.toBeInTheDocument()
  })

  it("filters archived request results and reveals redacted failure response evidence", async () => {
    const user = userEvent.setup()
    const quickID = "77777777-7777-4777-8777-777777777772"
    const snapshot = {
      schema_version: 1,
      reports: [{
        id: quickID, source: "quick_performance", generated_at: "2026-08-31T14:30:00Z", run_status: "completed", plan_name: "快速性能测试",
        model_name: "gpt-fast", channel_name: "api.example.test", passed: false, verdict: "快速性能测试未通过", issue_count: 1, case_count: 3, failed_case_count: 1, attachment_count: 0,
      }],
    } as unknown as ReportSnapshot
    const getDetail = vi.fn(async () => failedQuickDetail(quickID) as unknown as ReportDetail)
    const exportReport = vi.fn(async () => ({ filename: `${quickID}.json`, media_type: "application/json", data_base64: "e30=" }))

    render(
      <ReportWorkspace
        snapshot={snapshot}
        getDetail={getDetail}
        exportReport={exportReport}
        saveReportExport={vi.fn(async () => true)}
        copyReportPNG={vi.fn(async () => undefined)}
      />,
    )

    await user.click(screen.getByRole("button", { name: "查看报告：快速性能测试未通过" }))
    const archivedReport = await screen.findByRole("region", { name: "归档性能报告" })
    expect(within(archivedReport).getByRole("figure", { name: "请求结果分布：成功 2，失败 1" })).toBeInTheDocument()
    expect(within(archivedReport).getByRole("table", { name: "逐请求结果" })).toBeInTheDocument()

    await user.click(within(archivedReport).getByRole("button", { name: "失败 1" }))
    expect(within(archivedReport).getByRole("row", { name: /请求 1/ })).toBeInTheDocument()
    expect(within(archivedReport).queryByRole("row", { name: /请求 2/ })).not.toBeInTheDocument()
    await user.click(within(archivedReport).getByRole("button", { name: "查看请求 1 详情" }))

    const requestDetail = within(archivedReport).getByRole("region", { name: "请求 1 详情" })
    expect(requestDetail).toHaveTextContent("鉴权失败")
    expect(requestDetail).toHaveTextContent("req-report-safe")
    expect(requestDetail).toHaveTextContent("quota exhausted")
    expect(requestDetail).not.toHaveTextContent("sk-report-private")
  })

  it("shows Go-aggregated upstream response probe distributions", async () => {
		const user = userEvent.setup()
		const reportID = "66666666-6666-4666-8666-666666666662"
		const snapshot = {
			schema_version: 1,
			reports: [{
				id: reportID, source: "run", run_id: "55555555-5555-4555-8555-555555555552",
				generated_at: "2026-09-05T08:00:00Z", run_status: "completed", plan_name: "渠道探测", model_name: "gpt-probe", channel_name: "聚合上游",
				passed: true, verdict: "pass", issue_count: 0, case_count: 1, failed_case_count: 0, attachment_count: 0,
			}],
		} as unknown as ReportSnapshot
		const detail = parseReportDetail(formalProbeDetail(reportID))

		render(
			<ReportWorkspace
				snapshot={snapshot}
				getDetail={vi.fn(async () => detail)}
				exportReport={vi.fn(async () => ({ filename: `${reportID}.json`, media_type: "application/json", data_base64: "e30=" }))}
				saveReportExport={vi.fn(async () => true)}
				copyReportPNG={vi.fn(async () => undefined)}
			/>,
		)

		await user.click(screen.getByRole("button", { name: "查看报告：pass" }))
		const distribution = await screen.findByRole("table", { name: "上游响应分布" })
		expect(within(distribution).getByRole("row", { name: /provider-a.*已匹配.*sha256:known.*2.*66.67%/ })).toBeInTheDocument()
		expect(within(distribution).getByRole("row", { name: /unknown.*未知格式.*sha256:mystery.*1.*33.33%/ })).toBeInTheDocument()
	})

  it("renders SLO goodput, violation counters, and the capacity rung table in the shared report DOM", async () => {
    const user = userEvent.setup()
    const quickID = "77777777-7777-4777-8777-777777777774"
    render(
      <ReportWorkspace
        snapshot={quickSnapshot(quickID, "容量评估未通过")}
        getDetail={vi.fn(async () => phaseFourQuickDetail(quickID, "failed") as unknown as ReportDetail)}
        exportReport={vi.fn()}
        saveReportExport={vi.fn()}
        copyReportPNG={vi.fn()}
      />,
    )
    await user.click(screen.getByRole("button", { name: "查看报告：容量评估未通过" }))

    const report = await screen.findByRole("region", { name: "归档性能报告" })
    expect(report).toHaveTextContent("SLO 与容量")
    expect(report).toHaveTextContent("TTFT ≤ 50 ms")
    expect(report).toHaveTextContent("目标达标率 90%")
    expect(report).toHaveTextContent("传输与协议通过")
    expect(report).toHaveTextContent("SLO 通过")
    expect(report).toHaveTextContent("好请求 4 / 4")
    expect(report).toHaveTextContent("Goodput 12.5 req/s")
    expect(report).toHaveTextContent("传输 0 · TTFT 0 · TPOT 0 · E2E 0")
    expect(report).toHaveTextContent("最高通过并发 2")
    expect(report).toHaveTextContent("首次未通过 3")
    expect(report).toHaveTextContent(/峰值在途 \/ 配置并发\s*2 \/ 2/)
    const rungs = within(report).getByRole("table", { name: "容量阶梯结果" })
    expect(rungs.parentElement).toHaveClass("overflow-x-auto")
    expect(within(rungs).getByRole("columnheader", { name: "目标" })).toBeInTheDocument()
    expect(within(rungs).getByRole("columnheader", { name: "传输" })).toBeInTheDocument()
    expect(within(rungs).getByRole("columnheader", { name: "SLO" })).toBeInTheDocument()
    expect(within(rungs).getByRole("columnheader", { name: "Goodput" })).toBeInTheDocument()
    expect(within(rungs).getByRole("row", { name: /#3.*3 并发.*未通过/ })).toBeInTheDocument()

    const exportSurface = document.querySelector<HTMLElement>("[data-report-export-document]")
    expect(exportSurface).not.toBeNull()
    expect(within(exportSurface!).getByRole("table", { name: "容量阶梯结果", hidden: true })).toBeInTheDocument()
  })

  it("uses the selected open-loop capacity rung as the archived steady-state send target", async () => {
    const user = userEvent.setup()
    const quickID = "77777777-7777-4777-8777-777777777778"
    const detail = phaseFourQuickDetail(quickID, "failed") as unknown as Extract<ReportDetail, { source: "quick_performance" }>
    detail.performance.profile = {
      ...detail.performance.profile,
      load_mode: "open_loop",
      rate_per_second: 3,
      max_in_flight: 4,
    }
    render(
      <ReportWorkspace
        snapshot={quickSnapshot(quickID, "开放到达容量评估")}
        getDetail={vi.fn(async () => detail)}
        exportReport={vi.fn()}
        saveReportExport={vi.fn()}
        copyReportPNG={vi.fn()}
      />,
    )
    await user.click(screen.getByRole("button", { name: "查看报告：开放到达容量评估" }))

    const report = await screen.findByRole("region", { name: "归档性能报告" })
    expect(report).toHaveTextContent(/目标发送\s*2 req\/s/)
  })

  it.each([
    ["passed", "SLO 通过"],
    ["failed", "SLO 未通过"],
    ["not_evaluated", "SLO 未评估"],
  ] as const)("renders the %s SLO conclusion independently from transport", async (status, label) => {
    const user = userEvent.setup()
    const quickID = `77777777-7777-4777-8777-77777777777${status === "passed" ? "5" : status === "failed" ? "6" : "7"}`
    render(
      <ReportWorkspace
        snapshot={quickSnapshot(quickID, `${label}报告`)}
        getDetail={vi.fn(async () => phaseFourQuickDetail(quickID, status, false) as unknown as ReportDetail)}
        exportReport={vi.fn()}
        saveReportExport={vi.fn()}
        copyReportPNG={vi.fn()}
      />,
    )
    await user.click(screen.getByRole("button", { name: `查看报告：${label}报告` }))
    const report = await screen.findByRole("region", { name: "归档性能报告" })
    expect(report).toHaveTextContent("传输与协议通过")
    expect(report).toHaveTextContent(label)
  })

  it("renders earlier schema-v2 reports with legacy constant and fixed defaults without phase-three sections", async () => {
    const user = userEvent.setup()
    const quickID = "77777777-7777-4777-8777-777777777773"
    const snapshot = {
      schema_version: 1,
      reports: [{
        id: quickID, source: "quick_performance", generated_at: "2026-08-31T14:30:00Z", run_status: "completed",
        plan_name: "旧版快速性能测试", model_name: "gpt-fast", channel_name: "api.example.test", passed: true,
        verdict: "旧版快速性能测试通过", issue_count: 0, case_count: 3, failed_case_count: 0, attachment_count: 0,
      }],
    } as unknown as ReportSnapshot

    render(
      <ReportWorkspace
        snapshot={snapshot}
        getDetail={vi.fn(async () => legacyQuickDetail(quickID) as unknown as ReportDetail)}
        exportReport={vi.fn()}
        saveReportExport={vi.fn()}
        copyReportPNG={vi.fn()}
      />,
    )
    await user.click(screen.getByRole("button", { name: "查看报告：旧版快速性能测试通过" }))

    const archivedReport = await screen.findByRole("region", { name: "归档性能报告" })
    expect(archivedReport).toHaveTextContent("恒定间隔（旧报告）")
    expect(archivedReport).toHaveTextContent("固定 Token（旧报告）")
    expect(archivedReport).not.toHaveTextContent("准备阶段与预算")
    expect(archivedReport).not.toHaveTextContent("SLO 与容量")
    expect(within(archivedReport).queryByRole("table", { name: "时间切片" })).not.toBeInTheDocument()
  })
})

function quickSnapshot(reportID: string, verdict: string): ReportSnapshot {
  return {
    schema_version: 1,
    reports: [{
      id: reportID,
      source: "quick_performance",
      generated_at: "2026-08-31T14:30:00Z",
      run_status: "completed",
      plan_name: "快速性能测试",
      model_name: "gpt-fast",
      channel_name: "api.example.test",
      passed: true,
      verdict,
      issue_count: 0,
      case_count: 4,
      failed_case_count: 0,
      attachment_count: 0,
    }],
  }
}

function quickDetail(reportID: string) {
  return {
    schema_version: 1,
    source: "quick_performance",
    performance: {
      schema_version: 2,
      report_id: reportID,
      generated_at: "2026-08-31T14:30:00Z",
      archived: true,
      archive_status: "archived",
      model_id: "gpt-fast",
      success: true,
      address_mode: "base_url",
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      profile: {
        load_mode: "fixed_concurrency", request_count: 0, duration_ms: 1_000, concurrency: 2,
        arrival_pattern: "constant", workload_mode: "normal", random_seed: 424242,
        input_tokens_stddev: 2, output_tokens_stddev: 4, shared_prefix_tokens: 8,
        warmup_requests: 1, ramp_duration_ms: 1_000, ramp_request_cap: 2, slice_duration_ms: 1_000,
        timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
      },
      progress: { phase: "completed", planned: 9_997, offered: 3, launched: 3, completed: 3, in_flight: 0, peak_in_flight: 2, succeeded: 3, failed: 0, rejected: 0, capped: false, send_duration_ms: 2_400, drain_duration_ms: 100, total_duration_ms: 2_500 },
      metrics: {
        completed: 3, succeeded: 3, failed: 0, timed_out: 0, success_rate_percent: 100,
        offered_qps: 50, launched_qps: 50, completed_qps: 33.3, successful_request_qps: 33.3, request_qps: 33.3, rpm: 2_000,
        input_tpm: 40_000, output_tpm: 64_000, total_tpm: 104_000, generation_tps: 1_066.7,
        ttft_p50_ms: 30, ttft_p90_ms: 40, ttft_p95_ms: 42, ttft_p99_ms: 44, ttft_average_ms: 32,
        tpot_p50_ms: 4, tpot_p90_ms: 5, tpot_p95_ms: 6, tpot_p99_ms: 7, tpot_average_ms: 4.5,
        e2e_p50_ms: 60, e2e_p90_ms: 75, e2e_p95_ms: 80, e2e_p99_ms: 84, e2e_average_ms: 65,
        schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 1.8, schedule_lag_p95_ms: 2, schedule_lag_p99_ms: 2.8, schedule_lag_average_ms: 0.5,
        prompt_tokens: 60, completion_tokens: 96, cached_tokens: 0, cache_rate_percent: 0,
      },
      samples: [
        sample(0, 60, 60, 30, 4), sample(1, 75, 74, 40, 5), sample(2, 90, 88, 44, 7),
      ],
      failures: [],
      request_budget: { limit: 10_000, warmup_cap: 1, ramp_cap: 2, measured_cap: 9_997, total_cap: 10_000 },
      warmup: trafficSummary(1, 1),
      ramp: {
        shape: "linear_staircase", duration_ms: 1_000, steps: 2, target_concurrency: 2,
        completed_window: false, traffic: { ...trafficSummary(2, 2), capped: true },
      },
      time_slices: [
        { ...timeSlice(0, 0, 1_000, false, true), offered: 3, launched: 3, completed: 3, succeeded: 3, prompt_tokens: 60, completion_tokens: 96 },
        timeSlice(2, 2_000, 2_500, true, false, 0),
      ],
    },
  }
}

function phaseFiveQuickDetail(reportID: string) {
  const detail = quickDetail(reportID)
  const emptyLatency = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 }
  const emptyCount = { count: 0, p50: 0, p95: 0, p99: 0, average: 0 }
  return parseReportDetail({
    ...detail,
    performance: {
      ...detail.performance,
      schema_version: 3,
      metrics: {
        ...detail.performance.metrics,
        offered_qps: 1.25, launched_qps: 1.25, completed_qps: 1.2, successful_request_qps: 1.2,
        request_qps: 1.2, rpm: 72, input_tpm: 1_440, output_tpm: 2_304, total_tpm: 3_744, generation_tps: 38.4,
        ttft_samples: 3,
        ttft_p50_ms: 20, ttft_p90_ms: 20, ttft_p95_ms: 20, ttft_p99_ms: 20, ttft_average_ms: 20,
        tpot_p50_ms: 54 / 31, tpot_p90_ms: 65.2 / 31, tpot_p95_ms: 66.6 / 31,
        tpot_p99_ms: 67.72 / 31, tpot_average_ms: 54 / 31,
        e2e_p50_ms: 74, e2e_p90_ms: 85.2, e2e_p95_ms: 86.6, e2e_p99_ms: 87.72, e2e_average_ms: 74,
        schedule_lag_p50_ms: 1, schedule_lag_p90_ms: 1.8, schedule_lag_p95_ms: 1.9,
        schedule_lag_p99_ms: 1.98, schedule_lag_average_ms: 1,
        ttfb_samples: 3, ttfb_p50_ms: 10, ttfb_p95_ms: 10, ttfb_p99_ms: 10, ttfb_average_ms: 10,
        ttft_any_samples: 3, ttft_any_p50_ms: 20, ttft_any_p95_ms: 20, ttft_any_p99_ms: 20, ttft_any_average_ms: 20,
        ttft_visible_samples: 3, ttft_visible_p50_ms: 30, ttft_visible_p95_ms: 30, ttft_visible_p99_ms: 30, ttft_visible_average_ms: 30,
        ttst_samples: 3, ttst_p50_ms: 40, ttst_p95_ms: 40, ttst_p99_ms: 40, ttst_average_ms: 40,
        observed_icl_samples: 3, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
        semantic_chunk_count_samples: 3, semantic_chunk_count_p50: 3, semantic_chunk_count_p95: 3,
        semantic_chunk_count_p99: 3, semantic_chunk_count_average: 3,
      },
      samples: detail.performance.samples.map((item) => ({
        ...item,
        target_input_tokens: [22, 19, 19][item.request_index],
        target_output_tokens: [35, 22, 28][item.request_index],
        ttft_ms: 20,
        ttfb_ms: 10,
        ttft_any_ms: 20,
        ttft_visible_ms: 30,
        ttst_ms: 40,
        observed_icl_ms: 20,
        semantic_chunk_count: 3,
        tpot_ms: (item.e2e_ms - 20) / (item.completion_tokens - 1),
      })),
      time_slices: [
        {
          ...detail.performance.time_slices[0],
          ttfb: { count: 3, p50_ms: 10, p95_ms: 10, p99_ms: 10, average_ms: 10 },
          ttft_any: { count: 3, p50_ms: 20, p95_ms: 20, p99_ms: 20, average_ms: 20 },
          ttft_visible: { count: 3, p50_ms: 30, p95_ms: 30, p99_ms: 30, average_ms: 30 },
          ttft: { count: 3, p50_ms: 20, p95_ms: 20, p99_ms: 20, average_ms: 20 },
          ttst: { count: 3, p50_ms: 40, p95_ms: 40, p99_ms: 40, average_ms: 40 },
          observed_icl: { count: 3, p50_ms: 20, p95_ms: 20, p99_ms: 20, average_ms: 20 },
          semantic_chunk_count: { count: 3, p50: 3, p95: 3, p99: 3, average: 3 },
          tpot: { count: 3, p50_ms: 54 / 31, p95_ms: 66.6 / 31, p99_ms: 67.72 / 31, average_ms: 54 / 31 },
          e2e: { count: 3, p50_ms: 74, p95_ms: 86.6, p99_ms: 87.72, average_ms: 74 },
        },
        {
          ...detail.performance.time_slices[1],
          ttfb: emptyLatency,
          ttft_any: emptyLatency,
          ttft_visible: emptyLatency,
          ttft: emptyLatency,
          ttst: emptyLatency,
          observed_icl: emptyLatency,
          semantic_chunk_count: emptyCount,
          tpot: emptyLatency,
          e2e: emptyLatency,
        },
      ],
    },
  })
}

function phaseFourQuickDetail(
  reportID: string,
  status: "passed" | "failed" | "not_evaluated",
  withCapacity = true,
) {
  const detail = quickDetail(reportID)
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
  const assessment = status === "passed" ? passed : status === "not_evaluated" ? {
    ...passed,
    status: "not_evaluated" as const,
  } : {
    ...passed,
    status: "failed" as const,
    good_requests: 2,
    bad_requests: 2,
    good_request_percent: 50,
    goodput_qps: 6.25,
    violations: { transport: 0, ttft: 2, tpot: 0, e2e: 0 },
  }
  const failed = { ...assessment, status: "failed" as const }
  const rung = (index: number, target: number, slo: QuickPerformanceSLOAssessment = passed) => ({
    index,
    target,
    success: true,
    progress: {
      ...detail.performance.progress,
      planned: 4,
      offered: 4,
      launched: 4,
      completed: 4,
      succeeded: 4,
      capacity_rung_number: index + 1,
      capacity_rung_count: 3,
      capacity_target: target,
    },
    metrics: { ...detail.performance.metrics, completed: 4, succeeded: 4 },
    failures: [],
    slo_assessment: slo,
  })
  const selectedRung = rung(1, 2)
  return {
    ...detail,
    performance: {
      ...detail.performance,
      profile: {
        ...detail.performance.profile,
        request_count: 4,
        duration_ms: 0,
        concurrency: 3,
        slo_ttft_ms: 50,
        slo_target_percent: 90,
        ...(withCapacity ? { capacity_enabled: true, capacity_start: 1, capacity_step: 1 } : {}),
      },
      progress: withCapacity ? selectedRung.progress : detail.performance.progress,
      metrics: withCapacity ? selectedRung.metrics : detail.performance.metrics,
      slo_assessment: withCapacity ? passed : assessment,
      ...(withCapacity ? {
        request_budget: { limit: 10_000, warmup_cap: 0, ramp_cap: 0, measured_cap: 12, total_cap: 12 },
        capacity_result: {
          status: "failed" as const,
          selected_rung_index: 1,
          highest_passing_rung_index: 1,
          rungs: [rung(0, 1), selectedRung, rung(2, 3, failed)],
        },
      } : {}),
    },
  }
}

function sample(requestIndex: number, finished: number, e2e: number, ttft: number, tpot: number) {
  const targetInput = [18, 20, 22][requestIndex]
  const targetOutput = [28, 32, 36][requestIndex]
  return { request_index: requestIndex, scheduled_offset_ms: 0, started_offset_ms: requestIndex, finished_offset_ms: finished, schedule_lag_ms: requestIndex, e2e_ms: e2e, ttft_ms: ttft, tpot_ms: tpot, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 0, target_input_tokens: targetInput, target_output_tokens: targetOutput }
}

function trafficSummary(requestCap: number, completed: number) {
  return {
    request_cap: requestCap, offered: completed, launched: completed, completed, succeeded: completed,
    failed: 0, timed_out: 0, rejected: 0, peak_in_flight: Math.min(2, completed),
    prompt_tokens: completed * 20, completion_tokens: completed * 32, cached_tokens: 0,
    send_duration_ms: 60, drain_duration_ms: 10, total_duration_ms: 70,
    failures: [], stopped: false, capped: false,
  }
}

function timeSlice(sliceIndex: number, startMS: number, endMS: number, partial: boolean, withLatency: boolean, completed = 1) {
  const empty = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 }
  return {
    slice_index: sliceIndex, start_ms: startMS, end_ms: endMS, partial,
    offered: completed, launched: completed, completed, succeeded: completed, failed: 0, rejected: 0,
    prompt_tokens: completed * 20, completion_tokens: completed * 32, cached_tokens: 0,
    ttft: withLatency ? { count: completed, p50_ms: 30, p95_ms: 40, p99_ms: 45 } : empty,
    tpot: withLatency ? { count: completed, p50_ms: 4, p95_ms: 6, p99_ms: 7 } : empty,
    e2e: withLatency ? { count: completed, p50_ms: 60, p95_ms: 80, p99_ms: 90 } : empty,
  }
}

function legacyQuickDetail(reportID: string) {
  const detail = quickDetail(reportID)
  const {
    request_budget: _budget,
    warmup: _warmup,
    ramp: _ramp,
    time_slices: _slices,
    ...legacyPerformance
  } = detail.performance
  const {
    arrival_pattern: _arrival,
    workload_mode: _workload,
    random_seed: _seed,
    input_tokens_stddev: _inputDeviation,
    output_tokens_stddev: _outputDeviation,
    shared_prefix_tokens: _sharedPrefix,
    warmup_requests: _warmupRequests,
    ramp_duration_ms: _rampDuration,
    ramp_request_cap: _rampCap,
    slice_duration_ms: _sliceDuration,
    ...legacyProfile
  } = legacyPerformance.profile
  const { capped: _capped, ...legacyProgress } = legacyPerformance.progress
  return {
    ...detail,
    performance: {
      ...legacyPerformance,
      profile: {
        ...legacyProfile,
        load_mode: "open_loop",
        concurrency: 0,
        rate_per_second: 10,
        max_in_flight: 2,
      },
      progress: legacyProgress,
    },
  }
}

function failedQuickDetail(reportID: string) {
  const detail = quickDetail(reportID)
  return {
    ...detail,
    performance: {
      ...detail.performance,
      success: false,
      progress: { ...detail.performance.progress, succeeded: 2, failed: 1 },
      metrics: {
        ...detail.performance.metrics,
        succeeded: 2,
        failed: 1,
        success_rate_percent: 66.67,
        prompt_tokens: 40,
        completion_tokens: 64,
      },
      samples: detail.performance.samples.map((entry, index) => index === 0 ? {
        ...entry,
        http_status: 401,
        success: false,
        prompt_tokens: 0,
        completion_tokens: 0,
        error_code: "authentication_failed",
        response_evidence: {
          capture_status: "captured",
          content_type: "application/json",
          request_id: "req-report-safe",
          body: "{\"error\":{\"message\":\"quota exhausted\",\"api_key\":\"[REDACTED]\"}}",
          body_bytes: 96,
          truncated: false,
          redacted: true,
        },
      } : entry),
      failures: [{ error_code: "authentication_failed", count: 1 }],
    },
  }
}

function formalProbeDetail(reportID: string) {
	const runID = "55555555-5555-4555-8555-555555555552"
	const caseID = "44444444-4444-4444-8444-444444444442"
	return {
		schema_version: 1,
		source: "run",
		report: {
			id: reportID,
			run_id: runID,
			run_status: "completed",
			generated_at: "2026-09-05T08:00:00Z",
			model: { id: "33333333-3333-4333-8333-333333333332", name: "gpt-probe" },
			channel: { id: "22222222-2222-4222-8222-222222222222", name: "聚合上游" },
			environment: { os: "windows", arch: "amd64", region: "local", network_egress: "direct", app_version: "test", engine_version: "test" },
			conclusion: { passed: true, verdict: "pass", issues: [] },
			sla: {},
			metrics: {},
			distributions: [
				{ kind: "response_probe", case_id: caseID, bucket: "provider-a", classification: "matched", format: "json", shape: "sha256:known", count: 2, share_percent: 200 / 3 },
				{ kind: "response_probe", case_id: caseID, bucket: "unknown", classification: "unknown", format: "json", shape: "sha256:mystery", count: 1, share_percent: 100 / 3 },
			],
			case_results: [{ id: "11111111-1111-4111-8111-111111111111", case_id: caseID, success: { transport: true, protocol: true, semantic: true, sla: true }, metrics: {} }],
		},
		request_results: [],
	}
}
