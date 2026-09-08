import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { describe, expect, it, vi } from "vitest"

import { createAppI18n } from "@/i18n/i18n"
import { parseReportDetail, type ReportSnapshot } from "./data"
import { ReportWorkspace } from "./report-workspace"

const reportID = "30000000-0000-4000-8000-000000000001"
const runID = "30000000-0000-4000-8000-000000000002"
const suiteID = "30000000-0000-4000-8000-000000000003"
const firstEntryID = "30000000-0000-4000-8000-000000000004"
const secondEntryID = "30000000-0000-4000-8000-000000000005"
const sharedCaseID = "30000000-0000-4000-8000-000000000006"

describe("multi-suite run reports", () => {
  it("renders every ordered suite independently after a failure and exports the same full tree", async () => {
    const user = userEvent.setup()
    const firstRequests = Array.from({ length: 1_001 }, (_, index) => result(
      `30000000-0000-4000-8000-${String(index + 100).padStart(12, "0")}`,
      firstEntryID,
      `first-${index + 1}`,
    ))
    const secondRequest = result(
      "30000000-0000-4000-8000-999999999999",
      secondEntryID,
      "second-entry-request",
    )
    const detail = parseReportDetail({
      schema_version: 2,
      source: "run",
      report: {
        ...baseReport(),
        run_status: "failed",
        conclusion: { passed: false, verdict: "fail", issues: ["suite failed"] },
      },
      request_results: [...firstRequests, secondRequest],
      unassigned_request_results: [],
      suites: [
        suiteEntry({
          entryID: firstEntryID,
          name: "first configuration",
          status: "failed",
          passed: false,
          caseType: "text.contract",
          requests: firstRequests,
          distributions: [],
          metrics: { executed_total: { value: 91.5, unit: "score", samples: 1_001 } },
          sla: { latency_p95: { value: 500, unit: "ms", samples: 1_001 } },
        }),
        suiteEntry({
          entryID: secondEntryID,
          name: "second configuration",
          status: "not_started",
          passed: false,
          caseType: "response.probe",
          requests: [secondRequest],
          distributions: [{
            kind: "response_probe",
            schema_version: 1,
            case_id: sharedCaseID,
            bucket: "provider-a",
            classification: "matched",
            format: "json",
            shape: "sha256:known",
            count: 1,
            share_percent: 100,
          }],
          metrics: { executed_total: { value: 37.25, unit: "score", samples: 1 } },
          sla: { latency_p95: { value: 250, unit: "ms", samples: 1 } },
        }),
      ],
    })
    const exportVisualReport = vi.fn(async (element: HTMLElement, format: "html" | "png" | "pdf") => {
      expect(format).toBe("html")
      expect(element).toBe(document.querySelector("[data-report-export-document]"))
      expect(element.querySelector('[aria-label="Suite first configuration"]')).not.toBeNull()
      expect(element.querySelector('[aria-label="Suite second configuration"]')).not.toBeNull()
      expect(element).toHaveTextContent("second-entry-request")
      expect(element).toHaveTextContent("provider-a")
      return { filename: `${reportID}.html`, mediaType: "text/html", blob: new Blob(["report"]) }
    })

    renderWorkspace(detail, exportVisualReport)
    await user.click(screen.getByRole("button", { name: "View report: Passed" }))

    const plan = await screen.findByRole("region", { name: "Plan report" })
    expect(document.querySelector("[data-report-export-document]")).toBeNull()
    const suites = within(plan).getAllByRole("region", { name: /^Suite / })
    expect(suites.map((suite) => suite.getAttribute("aria-label"))).toEqual([
      "Suite first configuration",
      "Suite second configuration",
    ])
    expect(suites[0]).toHaveTextContent("Failed")
    expect(suites[0]).toHaveTextContent(/executed_total\s*91.5 score/)
    expect(suites[0]).toHaveTextContent(/latency_p95\s*500 ms/)
    expect(suites[1]).toHaveTextContent("Not started")
    expect(within(suites[1]).queryByText("Failed")).not.toBeInTheDocument()
    expect(suites[1]).toHaveTextContent(/executed_total\s*37.25 score/)
    expect(suites[1]).toHaveTextContent(/latency_p95\s*250 ms/)
    expect(suites[1]).not.toHaveTextContent("500 ms")
    expect(suites[1]).toHaveTextContent("second-entry-request")
    expect(within(suites[0]).queryByText("second-entry-request")).not.toBeInTheDocument()
    expect(within(suites[1]).getByRole("table", { name: "Upstream response distribution" })).toHaveTextContent("provider-a")
    expect(within(suites[0]).getByRole("status")).toHaveTextContent("1,001")

    await user.click(screen.getByRole("button", { name: "HTML" }))
    await waitFor(() => expect(exportVisualReport).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(document.querySelector("[data-report-export-document]")).toBeNull())
  })

  it("uses the generic renderer for an unregistered case type", async () => {
    const user = userEvent.setup()
    const customRequest = result(
      "30000000-0000-4000-8000-000000000021",
      firstEntryID,
      "custom-request",
    )
    const detail = parseReportDetail({
      schema_version: 2,
      source: "run",
      report: baseReport(),
      request_results: [customRequest],
      unassigned_request_results: [],
      suites: [suiteEntry({
        entryID: firstEntryID,
        name: "custom configuration",
        status: "completed",
        passed: true,
        caseType: "custom.new-case",
        requests: [customRequest],
        distributions: [],
      })],
    })

    renderWorkspace(detail)
    await user.click(screen.getByRole("button", { name: "View report: Passed" }))

    const customSuite = await screen.findByRole("region", { name: "Suite custom configuration" })
    expect(within(customSuite).getByText("custom-request")).toBeInTheDocument()
    expect(within(customSuite).queryByRole("table", { name: "Upstream response distribution" })).not.toBeInTheDocument()
  })

  it("labels a cancelled report as cancelled instead of failed", async () => {
    const user = userEvent.setup()
    const detail = parseReportDetail({
      schema_version: 2,
      source: "run",
      report: {
        ...baseReport(),
        run_status: "cancelled",
        conclusion: { passed: false, verdict: "cancelled", issues: ["run cancelled"] },
      },
      request_results: [],
      unassigned_request_results: [],
      suites: [suiteEntry({
        entryID: firstEntryID,
        name: "cancelled configuration",
        status: "cancelled",
        passed: false,
        caseType: "custom.cancelled",
        requests: [],
        distributions: [],
      })],
    })

    renderWorkspace(detail, vi.fn(), snapshot({
      run_status: "cancelled",
      passed: false,
      verdict: "cancelled",
      failed_case_count: 0,
    }))

    expect(screen.getAllByText("Cancelled").length).toBeGreaterThan(0)
    expect(screen.queryByText("Failed")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "View report: Cancelled" }))
    expect((await screen.findAllByText("Cancelled")).length).toBeGreaterThan(1)
    expect(screen.queryByText("Failed")).not.toBeInTheDocument()
  })
})

function renderWorkspace(
  detail: ReturnType<typeof parseReportDetail>,
  exportVisualReport = vi.fn(),
  reportSnapshot = snapshot(),
) {
  render(
    <I18nextProvider i18n={createAppI18n("en-US")}>
      <ReportWorkspace
        snapshot={reportSnapshot}
        getDetail={vi.fn(async () => detail)}
        exportReport={vi.fn(async () => ({ filename: `${reportID}.json`, media_type: "application/json", data_base64: "e30=" }))}
        saveReportExport={vi.fn(async () => true)}
        copyReportPNG={vi.fn(async () => undefined)}
        exportVisualReport={exportVisualReport}
      />
    </I18nextProvider>,
  )
}

function snapshot(overrides: Partial<ReportSnapshot["reports"][number]> = {}): ReportSnapshot {
  return {
    schema_version: 1,
    reports: [{
      id: reportID,
      source: "run",
      run_id: runID,
      generated_at: "2026-09-08T12:00:00Z",
      run_status: "completed",
      plan_name: "multi-suite plan",
      model_name: "model",
      channel_name: "channel",
      passed: true,
      verdict: "pass",
      issue_count: 1,
      case_count: 2,
      failed_case_count: 1,
      attachment_count: 0,
      ...overrides,
    }],
  }
}

function baseReport() {
  return {
    id: reportID,
    run_id: runID,
    run_status: "completed",
    generated_at: "2026-09-08T12:00:00Z",
    model: { id: "30000000-0000-4000-8000-000000000007", name: "model" },
    channel: { id: "30000000-0000-4000-8000-000000000008", name: "channel" },
    environment: {
      os: "windows", arch: "amd64", region: "local", network_egress: "direct",
      app_version: "test", engine_version: "test",
    },
    conclusion: { passed: true, verdict: "pass", issues: [] },
    sla: {},
    metrics: {},
    distributions: [],
    case_results: [],
  }
}

function suiteEntry({
  entryID,
  name,
  status,
  passed,
  caseType,
  requests,
  distributions,
  metrics = {},
  sla = {},
}: {
  entryID: string
  name: string
  status: string
  passed: boolean
  caseType: string
  requests: ReturnType<typeof result>[]
  distributions: Record<string, unknown>[]
  metrics?: Record<string, { value: number; unit: string; samples: number }>
  sla?: Record<string, { value: number; unit: string; samples: number }>
}) {
  return {
    suite_entry_id: entryID,
    suite_id: suiteID,
    suite_revision: 1,
    suite_key: "same-suite",
    suite_name: name,
    status,
    conclusion: {
      passed,
      verdict: status === "cancelled" ? "cancelled" : status === "not_started" ? "not_started" : passed ? "pass" : "fail",
      issues: passed ? [] : ["suite failed"],
    },
    sla,
    metrics,
    timeline: [],
    distributions,
    cases: [{
      case_id: sharedCaseID,
      revision: 1,
      key: "same-case",
      name: `${name} case`,
      case_type: caseType,
      case_type_version: 1,
      request_results: requests,
    }],
  }
}

function result(id: string, entryID: string, requestID: string) {
  return {
    id,
    request_id: requestID,
    suite_entry_id: entryID,
    case_id: sharedCaseID,
    success: { transport: true, protocol: true, semantic: true, sla: true },
    dimensions: {},
    metrics: {},
  }
}
