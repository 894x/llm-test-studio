import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import type { ReportCaseDetail, ReportSuiteDetail } from "../data"
import { createCaseRendererRegistry } from "./registry"
import { CaseRendererSlot } from "./renderer-slot"
import type { CaseRendererDefinition } from "./types"

afterEach(() => {
  vi.restoreAllMocks()
})

describe("CaseRendererSlot", () => {
  it("retries the exact renderer when new request data arrives for the same suite and case IDs", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined)
    const exact: CaseRendererDefinition = {
      match: { caseType: "custom.deep" },
      parse: ({ caseReport }) => {
        const requestID = caseReport.request_results[0]?.request_id
        if (requestID === "broken-data") throw new Error("bad archived payload")
        return requestID
      },
      Component: ({ payload }) => <div>exact: {String(payload)}</div>,
    }
    const generic: CaseRendererDefinition = {
      match: { generic: true },
      parse: ({ caseReport }) => caseReport,
      Component: ({ payload }) => <div>generic: {(payload as ReportCaseDetail).name}</div>,
    }
    const registry = createCaseRendererRegistry([exact, generic])
    const suite = suiteDetail()
    const { rerender } = render(
      <CaseRendererSlot suite={suite} caseReport={caseWithRequest("broken-data")} registry={registry} />,
    )
    expect(screen.getByText("generic: stable identity")).toBeInTheDocument()

    rerender(
      <CaseRendererSlot suite={suite} caseReport={caseWithRequest("recovered-data")} registry={registry} />,
    )

    expect(screen.getByText("exact: recovered-data")).toBeInTheDocument()
    expect(screen.queryByText("generic: stable identity")).not.toBeInTheDocument()
  })

  it("bounds each case at 1,000 requests by default", () => {
    const generic: CaseRendererDefinition = {
      match: { generic: true },
      parse: ({ requestLimit }) => requestLimit,
      Component: ({ payload }) => <div>request limit: {String(payload)}</div>,
    }

    render(
      <CaseRendererSlot
        suite={suiteDetail()}
        caseReport={caseDetail("healthy", "custom.deep")}
        registry={createCaseRendererRegistry([generic])}
      />,
    )

    expect(screen.getByText("request limit: 1000")).toBeInTheDocument()
  })

  it("passes every renderer a bounded case projection with the full request count", () => {
    const exact: CaseRendererDefinition = {
      match: { caseType: "custom.deep" },
      parse: ({ caseReport, hasMore, requestLimit, totalRequestCount }) => ({
        hasMore,
        requestLimit,
        totalRequestCount,
        visibleRequestCount: caseReport.request_results.length,
      }),
      Component: ({ payload }) => {
        const counts = payload as {
          hasMore: boolean
          requestLimit: number
          totalRequestCount: number
          visibleRequestCount: number
        }
        return (
          <div>
            custom counts: {counts.visibleRequestCount}/{counts.totalRequestCount}
            {counts.hasMore ? " limited" : " complete"} at {counts.requestLimit}
          </div>
        )
      },
    }
    const generic: CaseRendererDefinition = {
      match: { generic: true },
      parse: ({ caseReport }) => caseReport,
      Component: ({ payload }) => <div>generic: {(payload as ReportCaseDetail).name}</div>,
    }
    const requests = Array.from({ length: 1_001 }, (_, index) => ({
      id: `result-${index}`,
      request_id: `request-${index}`,
      success: { transport: true, protocol: true, semantic: true, sla: true },
      metrics: {},
    }))

    render(
      <CaseRendererSlot
        suite={suiteDetail()}
        caseReport={{ ...caseDetail("healthy", "custom.deep"), request_results: requests }}
        registry={createCaseRendererRegistry([generic, exact])}
      />,
    )

    expect(screen.getByText("custom counts: 1000/1001 limited at 1000")).toBeInTheDocument()
  })

  it("does not expose unbounded Suite cases to extension renderers", () => {
    const exact: CaseRendererDefinition = {
      match: { caseType: "custom.deep" },
      parse: ({ suite }) => "cases" in suite,
      Component: ({ payload }) => <div>suite cases exposed: {String(payload)}</div>,
    }
    const generic: CaseRendererDefinition = {
      match: { generic: true },
      parse: ({ caseReport }) => caseReport,
      Component: ({ payload }) => <div>{(payload as ReportCaseDetail).name}</div>,
    }
    const suite = suiteDetail()
    suite.cases = [{
      ...caseDetail("healthy", "custom.deep"),
      request_results: Array.from({ length: 1_001 }, (_, index) => ({
        id: `result-${index}`,
        request_id: `request-${index}`,
        success: { transport: true, protocol: true, semantic: true, sla: true },
        metrics: {},
      })),
    }]

    render(
      <CaseRendererSlot
        suite={suite}
        caseReport={suite.cases[0]}
        registry={createCaseRendererRegistry([generic, exact])}
      />,
    )

    expect(screen.getByText("suite cases exposed: false")).toBeInTheDocument()
  })

  it.each(["parse", "render"] as const)(
    "continues from an exact renderer that throws during %s to the longest compatible family",
    (failureStage) => {
      vi.spyOn(console, "error").mockImplementation(() => undefined)
      const exact: CaseRendererDefinition = {
        match: { caseType: "custom.deep" },
        parse: ({ caseReport }) => {
          if (failureStage === "parse") throw new Error("bad exact payload")
          return caseReport.name
        },
        Component: ({ payload }) => {
          if (failureStage === "render") throw new Error("bad exact component")
          return <div>exact: {String(payload)}</div>
        },
      }
      const family: CaseRendererDefinition = {
        match: { caseTypeFamily: "custom" },
        parse: ({ caseReport }) => caseReport.name,
        Component: ({ payload }) => <div>family: {String(payload)}</div>,
      }
      const generic: CaseRendererDefinition = {
        match: { generic: true },
        parse: ({ caseReport }) => caseReport,
        Component: ({ payload }) => <div>generic: {(payload as ReportCaseDetail).name}</div>,
      }

      render(
        <CaseRendererSlot
          suite={suiteDetail()}
          caseReport={caseDetail("healthy", "custom.deep")}
          registry={createCaseRendererRegistry([generic, family, exact])}
        />,
      )

      expect(screen.getByText("family: healthy")).toBeInTheDocument()
      expect(screen.queryByText("generic: healthy")).not.toBeInTheDocument()
    },
  )

  it.each(["parse", "render"] as const)(
    "falls back only the failing case when a specialized renderer throws during %s",
    (failureStage) => {
      vi.spyOn(console, "error").mockImplementation(() => undefined)
      const specialized: CaseRendererDefinition = {
        match: { caseTypeFamily: "custom" },
        parse: ({ caseReport }) => {
          if (failureStage === "parse" && caseReport.name === "broken") throw new Error("bad payload")
          return caseReport.name
        },
        Component: ({ payload }) => {
          const name = String(payload)
          if (failureStage === "render" && name === "broken") throw new Error("bad component")
          return <div>specialized: {name}</div>
        },
      }
      const generic: CaseRendererDefinition = {
        match: { generic: true },
        parse: ({ caseReport }) => caseReport,
        Component: ({ payload }) => <div>generic: {(payload as ReportCaseDetail).name}</div>,
      }
      const registry = createCaseRendererRegistry([specialized, generic])
      const suite = suiteDetail()

      render(<>
        <CaseRendererSlot suite={suite} caseReport={caseDetail("broken", "custom.broken")} registry={registry} />
        <CaseRendererSlot suite={suite} caseReport={caseDetail("healthy", "custom.healthy")} registry={registry} />
      </>)

      expect(screen.getByText("generic: broken")).toBeInTheDocument()
      expect(screen.getByText("specialized: healthy")).toBeInTheDocument()
    },
  )

  it.each(["parse", "render"] as const)(
    "contains a failure in the final generic renderer during %s",
    (failureStage) => {
      vi.spyOn(console, "error").mockImplementation(() => undefined)
      const generic: CaseRendererDefinition = {
        match: { generic: true },
        parse: ({ caseReport }) => {
          if (failureStage === "parse") throw new Error("bad generic payload")
          return caseReport.name
        },
        Component: ({ payload }) => {
          if (failureStage === "render") throw new Error("bad generic component")
          return <div>{String(payload)}</div>
        },
      }

      render(
        <CaseRendererSlot
          suite={suiteDetail()}
          caseReport={caseDetail("terminal failure", "custom.unregistered")}
          registry={createCaseRendererRegistry([generic])}
        />,
      )

      expect(screen.getByText("terminal failure")).toBeInTheDocument()
      expect(screen.getByRole("alert")).not.toBeEmptyDOMElement()
    },
  )
})

function suiteDetail(): ReportSuiteDetail {
  return {
    suite_entry_id: "20000000-0000-4000-8000-000000000001",
    suite_id: "20000000-0000-4000-8000-000000000002",
    suite_revision: 1,
    suite_key: "renderer-test",
    suite_name: "Renderer test",
    status: "completed",
    conclusion: { passed: true, verdict: "pass", issues: [] },
    sla: {},
    metrics: {},
    timeline: [],
    distributions: [],
    cases: [],
  }
}

function caseDetail(name: string, caseType: string): ReportCaseDetail {
  return {
    case_id: name === "broken"
      ? "20000000-0000-4000-8000-000000000003"
      : "20000000-0000-4000-8000-000000000004",
    revision: 1,
    key: name,
    name,
    case_type: caseType,
    case_type_version: 1,
    request_results: [],
  }
}

function caseWithRequest(requestID: string): ReportCaseDetail {
  return {
    ...caseDetail("stable identity", "custom.deep"),
    request_results: [{
      id: "20000000-0000-4000-8000-000000000005",
      request_id: requestID,
      success: { transport: true, protocol: true, semantic: true, sla: true },
      metrics: {},
    }],
  }
}
