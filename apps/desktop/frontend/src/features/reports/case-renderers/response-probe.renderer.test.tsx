import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { ReportCaseDetail, ReportSuiteDetail } from "../data"
import responseProbeRenderer from "./response-probe.renderer"
import type { CaseRendererContext } from "./types"

describe("response probe case renderer", () => {
  it("parses and renders only the current case distributions", () => {
    const context = rendererContext([
      probeDistribution(currentCase.case_id, "provider-a"),
      probeDistribution("40000000-0000-4000-8000-000000000099", "other-case"),
    ])

    const payload = responseProbeRenderer.parse(context)
    render(<responseProbeRenderer.Component context={context} payload={payload} />)

    const table = screen.getByRole("table", { name: "上游响应分布" })
    expect(within(table).getByText("provider-a")).toBeInTheDocument()
    expect(within(table).queryByText("other-case")).not.toBeInTheDocument()
  })

  it("rejects a malformed current-case payload so the slot can fall back", () => {
    const malformed = {
      ...probeDistribution(currentCase.case_id, "provider-a"),
      count: 0,
    }
    expect(() => responseProbeRenderer.parse(rendererContext([malformed]))).toThrow()
  })
})

const currentCase: ReportCaseDetail = {
  case_id: "40000000-0000-4000-8000-000000000003",
  revision: 1,
  key: "probe",
  name: "Response probe",
  case_type: "response.probe",
  case_type_version: 1,
  request_results: [],
}

function rendererContext(distributions: Record<string, unknown>[]): CaseRendererContext {
  const suite: ReportSuiteDetail = {
    suite_entry_id: "40000000-0000-4000-8000-000000000001",
    suite_id: "40000000-0000-4000-8000-000000000002",
    suite_revision: 1,
    suite_key: "probe-suite",
    suite_name: "Probe suite",
    status: "completed",
    conclusion: { passed: true, verdict: "pass", issues: [] },
    sla: {},
    metrics: {},
    timeline: [],
    distributions,
    cases: [currentCase],
  }
  return {
    suite,
    caseReport: currentCase,
    dataVersion: 1,
    requestLimit: 1_000,
    totalRequestCount: 0,
    hasMore: false,
  }
}

function probeDistribution(caseID: string, bucket: string) {
  return {
    kind: "response_probe",
    schema_version: 1,
    case_id: caseID,
    bucket,
    classification: "matched",
    format: "json",
    shape: `sha256:${bucket}`,
    count: 1,
    share_percent: 100,
  }
}
