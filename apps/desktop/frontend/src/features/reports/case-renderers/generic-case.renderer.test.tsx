import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { ReportCaseDetail, ReportResult, ReportSuiteDetail } from "../data"
import genericCaseRenderer from "./generic-case.renderer"
import type { CaseRendererContext } from "./types"

describe("generic case renderer", () => {
  it("bounds request rows per case and reports the complete case count", () => {
    const requests = Array.from({ length: 3 }, (_, index) => requestResult(index + 1))
    const caseReport: ReportCaseDetail = {
      case_id: "50000000-0000-4000-8000-000000000003",
      revision: 1,
      key: "generic",
      name: "Generic contract",
      case_type: "custom.contract",
      case_type_version: 1,
      request_results: requests.slice(0, 2),
    }
    const context: CaseRendererContext = {
      suite: suiteDetail(),
      caseReport,
      dataVersion: 1,
      requestLimit: 2,
      totalRequestCount: requests.length,
      hasMore: true,
    }

    const payload = genericCaseRenderer.parse(context)
    render(<genericCaseRenderer.Component context={context} payload={payload} />)

    const caseRegion = screen.getByRole("region", { name: "用例：Generic contract" })
    const table = within(caseRegion).getByRole("table", { name: "请求级结果" })
    expect(within(table).getByText("request-1")).toBeInTheDocument()
    expect(within(table).getByText("request-2")).toBeInTheDocument()
    expect(within(table).queryByText("request-3")).not.toBeInTheDocument()
    expect(within(caseRegion).getByRole("status")).toHaveTextContent("3")
  })
})

function suiteDetail(): ReportSuiteDetail {
  return {
    suite_entry_id: "50000000-0000-4000-8000-000000000001",
    suite_id: "50000000-0000-4000-8000-000000000002",
    suite_revision: 1,
    suite_key: "generic-suite",
    suite_name: "Generic suite",
    status: "completed",
    conclusion: { passed: true, verdict: "pass", issues: [] },
    sla: {},
    metrics: {},
    timeline: [],
    distributions: [],
    cases: [],
  }
}

function requestResult(index: number): ReportResult {
  return {
    id: `50000000-0000-4000-8000-${String(index).padStart(12, "0")}`,
    request_id: `request-${index}`,
    success: { transport: true, protocol: true, semantic: true, sla: true },
    metrics: {},
  }
}
