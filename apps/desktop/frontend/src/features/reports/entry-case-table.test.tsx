import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { afterEach, describe, expect, it, vi } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { openAIChatPresentation } from "@/features/protocols/openai-chat"
import { EntryCaseTable, ExecutionDetails } from "./entry-case-table"
import type { ReportEntryDetail, ReportResult } from "./data"

afterEach(() => vi.unstubAllGlobals())

function renderBodyEvidence(requestBody: string) {
  const result: ReportResult = {
    id: "request-body", execution_status: "completed", metrics: {},
    verification: { status: "not_applicable", assertions: [] },
    observation: {
      protocol: "openai-chat", metrics: {}, artifacts: [], issues: [],
      exchanges: [{ step: "chat", method: "POST", path: "/chat/completions", elapsed_ms: 10,
        request_body: { content: requestBody }, response: { choices: [{ message: { content: "你好" } }] } }],
    },
  }
  render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ExecutionDetails results={[result]} /></I18nextProvider>)
  fireEvent.click(screen.getByText(/chat · POST/))
  fireEvent.click(screen.getByText("请求体"))
  fireEvent.click(screen.getByText("响应"))
  return result.observation!.exchanges[0]
}

describe("protocol execution details", () => {
  it("copies complete request and response bodies while keeping details open", async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal("navigator", { clipboard: { writeText } })
    const exchange = renderBodyEvidence(`${"长".repeat(17000)}结尾`)
    const requestPanel = screen.getByText("请求体").closest("details")!
    expect(requestPanel.querySelector("pre")).not.toHaveTextContent("结尾")

    fireEvent.click(screen.getByRole("button", { name: "复制请求体" }))
    await waitFor(() => expect(within(requestPanel).getByRole("status")).toHaveTextContent("已复制"))
    expect(writeText).toHaveBeenLastCalledWith(JSON.stringify(exchange.request_body, null, 2))
    expect(requestPanel).toHaveAttribute("open")

    fireEvent.click(screen.getByRole("button", { name: "复制响应" }))
    await waitFor(() => expect(writeText).toHaveBeenLastCalledWith(JSON.stringify(exchange.response, null, 2)))
    expect(screen.getByText("响应").closest("details")).toHaveAttribute("open")
    expect(screen.getAllByRole("button", { name: /^复制/ })).toHaveLength(2)
  })

  it("prevents duplicate writes and lets a failed copy be retried", async () => {
    let rejectWrite!: (reason: Error) => void
    const writeText = vi.fn(() => new Promise<void>((_resolve, reject) => { rejectWrite = reject }))
    vi.stubGlobal("navigator", { clipboard: { writeText } })
    renderBodyEvidence("test")
    const button = screen.getByRole("button", { name: "复制请求体" })
    const requestPanel = button.closest("details")!

    fireEvent.click(button)
    expect(button).toBeDisabled()
    fireEvent.click(button)
    expect(writeText).toHaveBeenCalledOnce()
    rejectWrite(new Error("permission denied"))
    await waitFor(() => expect(within(requestPanel).getByRole("status")).toHaveTextContent("复制失败，请重试"))
    expect(button).toBeEnabled()
    expect(requestPanel).not.toHaveTextContent("permission denied")

    writeText.mockResolvedValueOnce()
    fireEvent.click(button)
    await waitFor(() => expect(within(requestPanel).getByRole("status")).toHaveTextContent("已复制"))
  })

  it("preserves latency, token and warmup evidence without inventing missing metrics", () => {
    const result: ReportResult = {
      id: "request-one", request_id: "warmup-1", execution_status: "completed",
      verification: { status: "not_applicable", assertions: [] },
      dimensions: { phase: "warmup" },
      metrics: { e2e_ms: 123.25, ttft_ms: 15, tpot_ms: 4.5, completion_tokens: 8, prompt_tokens: 20, cached_tokens: 3 },
    }
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ExecutionDetails results={[result]} /></I18nextProvider>)
    const metrics = screen.getAllByRole("definition")[0].closest("dl")!
    expect(within(metrics).getByText("123.25 ms")).toBeInTheDocument()
    expect(within(metrics).getByText("4.5 ms/token")).toBeInTheDocument()
    expect(within(metrics).getByText("8")).toBeInTheDocument()
    expect(within(metrics).getByText("20")).toBeInTheDocument()
    expect(within(metrics).getByText("3")).toBeInTheDocument()
    expect(within(metrics).getByText("—")).toBeInTheDocument()
    expect(screen.getByText(/预热/)).toBeInTheDocument()
    expect(openAIChatPresentation.columns.find(column => column.id === "tokens")?.metric).toBe("completion_tokens")
  })

  it("translates known execution error codes and keeps unknown codes readable", () => {
    const results: ReportResult[] = [
      {
        id: "known", request_id: "req-auth", execution_status: "failed",
        verification: { status: "failed", assertions: [] },
        error_code: "authentication_failed",
        metrics: {},
      },
      {
        id: "unknown", request_id: "req-raw", execution_status: "failed",
        verification: { status: "failed", assertions: [] },
        error_code: "provider_internal_timeout",
        metrics: {},
      },
    ]
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><ExecutionDetails results={results} /></I18nextProvider>)
    expect(screen.getByText("鉴权失败")).toBeInTheDocument()
    expect(screen.getByText("provider_internal_timeout")).toBeInTheDocument()
    expect(screen.queryByText("authentication_failed")).not.toBeInTheDocument()
    expect(screen.queryByText("common:errorCode.provider_internal_timeout")).not.toBeInTheDocument()
  })
})

it("shows distinct measured HTTP observations without counting warmup", () => {
  const observation = (status: number) => ({ protocol: "openai-chat" as const, model: "test", http_status: status, metrics: {}, exchanges: [], artifacts: [], issues: [] })
  const results: ReportResult[] = [503, 400, 200, 200].map((status, index) => ({
    id: String(index), execution_status: "completed", verification: { status: "not_applicable", assertions: [] },
    metrics: {}, dimensions: { phase: index === 0 ? "warmup" : "measured" }, observation: observation(status),
  }))
  const verification = { status: "not_applicable" as const, passed: 0, failed: 0, observed: 3, indeterminate: 0 }
  const entry: ReportEntryDetail = {
    entry_id: "entry", target_id: "case", target_kind: "case", name: "entry", key: "entry", protocol: "openai-chat", seed: 0,
    warmup_count: 1, settings: {}, parameters: {}, status: "completed", verification,
    load: { mode: "single", concurrency: 1, request_count: 3, rate_per_second: 0, duration_ms: 0, request_timeout_ms: 1000 },
    conclusion: { passed: false, verdict: "observed", issues: [] }, sla: {}, metrics: {}, timeline: [], distributions: [],
    cases: [{ case_id: "case", revision: 1, key: "case", name: "Observed statuses", protocol: "openai-chat", severity: "normal", verification, metrics: {}, request_results: results }],
  }
  render(<I18nextProvider i18n={createAppI18n("zh-CN")}><EntryCaseTable entry={entry} /></I18nextProvider>)
  expect(screen.getByText("400 / 200")).toBeInTheDocument()
  expect(screen.queryByText("503")).not.toBeInTheDocument()
  expect(screen.getByRole("columnheader", { name: "输出 Token" })).toBeInTheDocument()
  expect(screen.getByRole("columnheader", { name: "等级" })).toBeInTheDocument()
  expect(screen.getByText("容忍项")).toHaveClass("bg-info-soft", "text-info-strong")
})
