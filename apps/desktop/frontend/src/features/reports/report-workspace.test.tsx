import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { ReportWorkspace } from "./report-workspace"
import type { ReportDetail, ReportSnapshot } from "./data"

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
    const getDetail = vi.fn(async () => quickDetail(quickID) as unknown as ReportDetail)
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
      expect(element.querySelector('[aria-label="TTFT 分布图"]')).not.toBeNull()
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
    expect(archivedReport).toHaveTextContent("https://api.example.test/v1/chat/completions")
    expect(archivedReport).toHaveTextContent("峰值在途 / 配置并发")
    expect(archivedReport).toHaveTextContent("2 / 2")
    expect(archivedReport).toHaveTextContent("实际发送")
    expect(archivedReport).toHaveTextContent("成功吞吐")
    expect(archivedReport).not.toHaveTextContent("请求速率")
    expect(archivedReport).toHaveTextContent("失败")
    expect(archivedReport).toHaveTextContent("104,000 TPM")
    expect(within(archivedReport).getByRole("table", { name: "延迟分布统计" })).toHaveTextContent("客户端排队（本地调度延迟）")
    expect(screen.queryByRole("table", { name: "测试报告目录" })).not.toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    const charts = screen.getByRole("region", { name: "性能图表" })
    expect(within(charts).getByRole("figure", { name: "TTFT 分布图" })).toBeInTheDocument()
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
})

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
      profile: { load_mode: "fixed_concurrency", request_count: 0, duration_ms: 1_000, concurrency: 2, timeout_ms: 30_000, input_tokens: 20, output_tokens: 32 },
      progress: { phase: "completed", planned: 10_000, offered: 3, launched: 3, completed: 3, in_flight: 0, peak_in_flight: 2, succeeded: 3, failed: 0, rejected: 0, send_duration_ms: 60, drain_duration_ms: 30, total_duration_ms: 90 },
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
    },
  }
}

function sample(requestIndex: number, finished: number, e2e: number, ttft: number, tpot: number) {
  return { request_index: requestIndex, scheduled_offset_ms: 0, started_offset_ms: requestIndex, finished_offset_ms: finished, schedule_lag_ms: requestIndex, e2e_ms: e2e, ttft_ms: ttft, tpot_ms: tpot, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 0 }
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
