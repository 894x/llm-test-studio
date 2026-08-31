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
    const exportReport = vi.fn(async () => { throw new Error("stop after export request") })
    const exportVisualReport = vi.fn(async (element: HTMLElement, _format: "html" | "png" | "pdf", reportID: string) => {
      expect(reportID).toBe(quickID)
      expect(element).toHaveTextContent("team-alpha")
      expect(element.querySelectorAll("figure")).toHaveLength(6)
      expect(element.querySelector('[aria-label="TTFT 分布图"]')).not.toBeNull()
      expect(element.querySelector('[aria-label="E2E 时间曲线"]')).not.toBeNull()
      throw new Error("stop after visual export request")
    })

    render(<ReportWorkspace snapshot={snapshot} getDetail={getDetail} exportReport={exportReport} exportVisualReport={exportVisualReport} />)

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
    expect(archivedReport).toHaveTextContent("3 / 3")
    expect(archivedReport).toHaveTextContent("完成 / 计划")
    expect(archivedReport).toHaveTextContent("失败")
    expect(archivedReport).toHaveTextContent("104,000 TPM")
    expect(within(archivedReport).getByRole("table", { name: "延迟分布统计" })).toHaveTextContent("客户端排队（本地调度延迟）")
    expect(screen.queryByRole("table", { name: "测试报告目录" })).not.toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    const charts = screen.getByRole("region", { name: "性能图表" })
    expect(within(charts).getByRole("figure", { name: "TTFT 分布图" })).toBeInTheDocument()
    expect(within(charts).getByRole("figure", { name: "TPOT 时间曲线" })).toBeInTheDocument()
    expect(within(charts).getByRole("figure", { name: "E2E 时间曲线" })).toBeInTheDocument()
    expect(getDetail).toHaveBeenCalledWith(quickID)
    expect(screen.queryByRole("table", { name: "请求级结果" })).not.toBeInTheDocument()

    const watermark = screen.getByRole("textbox", { name: "导出水印" })
    expect(watermark).toHaveValue("rhzs")
    await user.click(screen.getByRole("button", { name: "JSON" }))
    expect(exportReport).toHaveBeenCalledWith(quickID, "json", "rhzs")
    await user.clear(watermark)
    await user.type(watermark, "team-alpha")
    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(exportVisualReport).toHaveBeenCalledTimes(1))
    expect(exportReport).not.toHaveBeenCalledWith(quickID, "html", "team-alpha")
    await user.click(screen.getByRole("button", { name: "PNG" }))
    await user.click(screen.getByRole("button", { name: "PDF" }))
    await waitFor(() => expect(exportVisualReport).toHaveBeenCalledTimes(3))
    expect(exportVisualReport.mock.calls.map(([, format]) => format)).toEqual(["html", "png", "pdf"])

    await user.click(screen.getByRole("button", { name: "返回报告列表" }))
    expect(screen.getByRole("table", { name: "测试报告目录" })).toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "报告详情" })).toHaveTextContent(quickID)
    expect(screen.queryByRole("region", { name: "归档性能报告" })).not.toBeInTheDocument()
  })
})

function quickDetail(reportID: string) {
  return {
    schema_version: 1,
    source: "quick_performance",
    performance: {
      schema_version: 1,
      report_id: reportID,
      generated_at: "2026-08-31T14:30:00Z",
      archived: true,
      archive_status: "archived",
      model_id: "gpt-fast",
      success: true,
      address_mode: "base_url",
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      profile: { request_count: 3, duration_ms: 0, concurrency: 2, timeout_ms: 30_000, input_tokens: 20, output_tokens: 32 },
      progress: { phase: "completed", planned: 3, launched: 3, completed: 3, in_flight: 0, peak_in_flight: 2, succeeded: 3, failed: 0, rejected: 0, send_duration_ms: 60, drain_duration_ms: 30, total_duration_ms: 90 },
      metrics: {
        completed: 3, succeeded: 3, failed: 0, timed_out: 0, success_rate_percent: 100, request_qps: 33.3, rpm: 2_000,
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
