import { render, screen, within } from "@testing-library/react"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it } from "vitest"
import { createAppI18n } from "@/i18n/i18n"
import { openAIChatPresentation } from "@/features/protocols/openai-chat"
import { EntryCaseTable, ExecutionDetails } from "./entry-case-table"
import type { ReportEntryDetail, ReportResult } from "./data"

describe("protocol execution details", () => {
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
    cases: [{ case_id: "case", revision: 1, key: "case", name: "Observed statuses", protocol: "openai-chat", verification, metrics: {}, request_results: results }],
  }
  render(<I18nextProvider i18n={createAppI18n("zh-CN")}><EntryCaseTable entry={entry} /></I18nextProvider>)
  expect(screen.getByText("400 / 200")).toBeInTheDocument()
  expect(screen.queryByText("503")).not.toBeInTheDocument()
  expect(screen.getByRole("columnheader", { name: "输出 Token" })).toBeInTheDocument()
})
