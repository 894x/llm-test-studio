import { describe, expect, it } from "vitest"

import { parseReportDetail } from "./data"

const reportID = "10000000-0000-4000-8000-000000000001"
const runID = "10000000-0000-4000-8000-000000000002"
const suiteID = "10000000-0000-4000-8000-000000000003"
const firstEntryID = "10000000-0000-4000-8000-000000000004"
const secondEntryID = "10000000-0000-4000-8000-000000000005"
const caseID = "10000000-0000-4000-8000-000000000006"

describe("parseReportDetail run reports", () => {
  it.each([1, 2] as const)("rejects schema v%s details without required request results", (schemaVersion) => {
    expect(() => parseReportDetail({
      schema_version: schemaVersion,
      source: "run",
      report: baseReport(),
      ...(schemaVersion === 2 ? { suites: [], unassigned_request_results: [] } : {}),
    })).toThrow()
  })

  it.each([1, 2] as const)("rejects schema v%s reports without required case results", (schemaVersion) => {
    const report = baseReport() as Record<string, unknown>
    delete report.case_results
    expect(() => parseReportDetail({
      schema_version: schemaVersion,
      source: "run",
      report,
      request_results: [],
      ...(schemaVersion === 2 ? { suites: [], unassigned_request_results: [] } : {}),
    })).toThrow()
  })

  it("preserves ordered suite entries and keeps repeated suite references isolated", () => {
    const detail = parseV2Detail([
      suiteDetail(firstEntryID, "first configuration", "10000000-0000-4000-8000-000000000011"),
      suiteDetail(secondEntryID, "second configuration", "10000000-0000-4000-8000-000000000012"),
    ])

    expect(detail.source).toBe("run")
    if (detail.source !== "run") throw new Error("expected a run report")
    const suites = detail.suites
    expect(suites.map(({ suite_entry_id }) => suite_entry_id)).toEqual([firstEntryID, secondEntryID])
    expect(suites.map(({ suite_name }) => suite_name)).toEqual(["first configuration", "second configuration"])
    expect(suites[0].cases[0].request_results[0].id).toBe("10000000-0000-4000-8000-000000000011")
    expect(suites[1].cases[0].request_results[0].id).toBe("10000000-0000-4000-8000-000000000012")
    expect(suites[0].cases[0]).not.toBe(suites[1].cases[0])
  })

  it("rejects duplicate suite entry and case identities in the canonical v2 tree", () => {
    const firstSuite = suiteDetail(firstEntryID, "first configuration", "10000000-0000-4000-8000-000000000011")
    const duplicateEntry = suiteDetail(firstEntryID, "duplicate entry", "10000000-0000-4000-8000-000000000012")
    expect(() => parseV2Detail([firstSuite, duplicateEntry])).toThrow()

    const duplicateCase = suiteDetail(firstEntryID, "duplicate case", "10000000-0000-4000-8000-000000000015")
    duplicateCase.cases.push({
      ...duplicateCase.cases[0],
      key: "duplicate-case",
      name: "Duplicate case",
      summary_result: result("10000000-0000-4000-8000-000000000016", {
        suite_entry_id: firstEntryID,
        case_id: caseID,
      }),
      request_results: [{
        ...result("10000000-0000-4000-8000-000000000017", {
          suite_entry_id: firstEntryID,
          case_id: caseID,
        }),
        request_id: "request-10000000-0000-4000-8000-000000000017",
      }],
    })
    expect(() => parseV2Detail([duplicateCase])).toThrow()
  })

  it.each([
    ["summary", "suite_entry_id", undefined],
    ["summary", "case_id", secondEntryID],
    ["request", "suite_entry_id", secondEntryID],
    ["request", "case_id", undefined],
  ] as const)("rejects a nested %s result with an invalid %s", (kind, field, invalidValue) => {
    const suite = suiteDetail(firstEntryID, "invalid ownership", "10000000-0000-4000-8000-000000000018")
    const target = kind === "summary"
      ? suite.cases[0].summary_result!
      : suite.cases[0].request_results[0]
    if (invalidValue === undefined) delete target[field]
    else target[field] = invalidValue

    expect(() => parseV2Detail([suite])).toThrow()
  })

  it("rejects duplicate result IDs in the canonical v2 tree", () => {
    const suite = suiteDetail(firstEntryID, "duplicate result", "10000000-0000-4000-8000-000000000019")
    suite.cases[0].request_results.push({ ...suite.cases[0].request_results[0] })

    expect(() => parseV2Detail([suite])).toThrow()
  })

  it("rejects missing or payload-divergent canonical root results", () => {
    const suite = suiteDetail(firstEntryID, "canonical", "10000000-0000-4000-8000-000000000020")
    const requestResults = suite.cases[0].request_results
    const caseResults = [suite.cases[0].summary_result!]

    expect(() => parseReportDetail(v2Payload([suite], [], caseResults))).toThrow()
    expect(() => parseReportDetail(v2Payload(
      [suite],
      [{ ...requestResults[0], metrics: { e2e_ms: 1 } }],
      caseResults,
    ))).toThrow()
    expect(() => parseReportDetail(v2Payload([suite], requestResults, []))).toThrow()
  })

  it.each([
    ["unknown", { passed: false, verdict: "fail", issues: [] }],
    ["cancelled", { passed: false, verdict: "fail", issues: [] }],
    ["not_started", { passed: true, verdict: "pass", issues: [] }],
  ])("rejects Suite status/conclusion mismatch %s", (status, conclusion) => {
    const suite = suiteDetail(firstEntryID, "invalid status", "10000000-0000-4000-8000-000000000024")
    Object.assign(suite, { status, conclusion })

    expect(() => parseV2Detail([suite])).toThrow()
  })

  it("rejects a schema v1 flat run report", () => {
    expect(() => parseReportDetail({
      schema_version: 1,
      source: "run",
      report: baseReport(),
      request_results: [],
    })).toThrow()
  })
})

function baseReport() {
  return {
    id: reportID,
    run_id: runID,
    run_status: "completed",
    generated_at: "2026-09-08T12:00:00Z",
    model: { id: "10000000-0000-4000-8000-000000000007", name: "model" },
    channel: { id: "10000000-0000-4000-8000-000000000008", name: "channel" },
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

function suiteDetail(entryID: string, name: string, requestResultID: string) {
  return {
    suite_entry_id: entryID,
    suite_id: suiteID,
    suite_revision: 3,
    suite_key: "repeated-suite",
    suite_name: name,
    status: "completed",
    conclusion: { passed: true, verdict: "pass", issues: [] },
    sla: {},
    metrics: {},
    timeline: [],
    distributions: [],
    cases: [{
      case_id: caseID,
      revision: 2,
      key: "probe",
      name: "Response probe",
      case_type: "response.probe",
      case_type_version: 1,
      summary_result: result(entryID === firstEntryID
        ? "10000000-0000-4000-8000-000000000013"
        : "10000000-0000-4000-8000-000000000014", {
        suite_entry_id: entryID,
        case_id: caseID,
      }),
      request_results: [{
        ...result(requestResultID, { suite_entry_id: entryID, case_id: caseID }),
        request_id: `request-${requestResultID}`,
      }],
    }],
  }
}

function parseV2Detail(suites: ReturnType<typeof suiteDetail>[]) {
  const requestResults = suites.flatMap((suite) => suite.cases.flatMap((caseReport) => caseReport.request_results))
  const caseResults = suites.flatMap((suite) => suite.cases.flatMap((caseReport) =>
    caseReport.summary_result ? [caseReport.summary_result] : [],
  ))
  return parseReportDetail(v2Payload(suites, requestResults, caseResults))
}

function v2Payload(
  suites: ReturnType<typeof suiteDetail>[],
  requestResults: ReturnType<typeof result>[],
  caseResults: ReturnType<typeof result>[],
) {
  return {
    schema_version: 2,
    source: "run",
    report: { ...baseReport(), case_results: caseResults },
    request_results: requestResults,
    unassigned_request_results: [],
    suites,
  }
}

function result(
  id: string,
  extra: { suite_entry_id?: string; case_id?: string } = {},
) {
  return {
    id,
    ...extra,
    success: { transport: true, protocol: true, semantic: true, sla: true },
    metrics: {},
  }
}
