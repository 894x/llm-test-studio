import { describe, expect, it } from "vitest"
import { protocolReportFixture, reportID } from "@/test/protocol-report-fixture"
import { parseReportDetail, parseReportSnapshot } from "./data"
import type { ExecutionOrigin } from "./execution-origin"

describe("current protocol report contract", () => {
  it.each(["local", "remote", null] as const)("preserves the current %s execution origin independently of preparation environment", kind => {
    const source = protocolReportFixture()
    const origin: ExecutionOrigin | null = kind === null ? null : { kind, node_id: reportID(20), installation_id: reportID(21),
      engine_build_id: "b".repeat(64), runner_version: "v-remote", os: "linux", arch: "arm64" }
    source.report.execution_origin = origin
    const detail = parseReportDetail(source)
    if (detail.source !== "run") throw new Error("run expected")
    expect(detail.report.execution_origin).toEqual(origin)
    expect(detail.report.environment.os).toBe("windows")
    if (origin) {
      expect(detail.report.execution_origin).not.toBe(origin)
      origin.os = "changed"
      expect(detail.report.execution_origin?.os).toBe("linux")
    }
  })
  it("rejects missing or mis-cased execution_origin without mutating an old report", () => {
    const source = protocolReportFixture() as unknown as { report: Record<string, unknown> }
    delete source.report.execution_origin
    const before = structuredClone(source)
    expect(() => parseReportDetail(source)).toThrow(/execution_origin/)
    expect(source).toEqual(before)
    source.report.ExecutionOrigin = null
    expect(() => parseReportDetail(source)).toThrow(/execution_origin/)
  })
  it("keeps observed execution separate from a pass and preserves snapshot parameters", () => {
    const source = protocolReportFixture("not_applicable")
    source.entries[0].cases[0].severity = "normal"
    const detail = parseReportDetail(source)
    if (detail.source !== "run") throw new Error("run expected")
    expect(detail.entries[0].verification).toEqual({ status: "not_applicable", passed: 0, failed: 0, observed: 1, indeterminate: 0 })
    expect(detail.entries[0].parameters).toEqual({ content_length: 4000 })
    expect(detail.entries[0].cases[0].severity).toBe("normal")
    expect(detail.entries[0].cases[0].request_results[0].observation?.http_status).toBe(400)
  })
  it.each([undefined, "urgent"])("rejects a missing or unknown case severity %s", severity => {
    const source = protocolReportFixture() as unknown as { entries: { cases: Record<string, unknown>[] }[] }
    if (severity === undefined) delete source.entries[0].cases[0].severity
    else source.entries[0].cases[0].severity = severity
    expect(() => parseReportDetail(source)).toThrow()
  })
  it.each([2, 3, 4])("rejects unsupported report schema %s without mutating it", schema_version => {
    const source = { ...protocolReportFixture(), schema_version }; const before = structuredClone(source)
    expect(() => parseReportDetail(source)).toThrow(); expect(source).toEqual(before)
  })
  it("rejects duplicate entries, mixed protocols, mismatched ownership, and malformed assertion values", () => {
    const duplicate = protocolReportFixture(); duplicate.entries.push(structuredClone(duplicate.entries[0])); expect(() => parseReportDetail(duplicate)).toThrow()
    const mixed = protocolReportFixture(); mixed.entries[0].protocol = "seedance"; expect(() => parseReportDetail(mixed)).toThrow()
    const wrong = protocolReportFixture(); wrong.entries[0].cases[0].request_results[0].entry_id = reportID(99); expect(() => parseReportDetail(wrong)).toThrow()
    const malformed = protocolReportFixture(); malformed.request_results[0].verification.assertions[0].actual = Number.NaN; expect(() => parseReportDetail(malformed)).toThrow()
  })
  it("accepts HTTP 400 with a passed assertion without deriving success from status", () => {
    const detail = parseReportDetail(protocolReportFixture())
    if (detail.source !== "run") throw new Error("run expected")
    expect(detail.request_results[0].verification.status).toBe("passed")
    expect(detail.request_results[0].observation?.http_status).toBe(400)
  })
  it("strips unknown credential fields from the allowed observation DTO", () => {
    const value = protocolReportFixture(); Object.assign(value.request_results[0].observation!, { api_key: "hidden" })
    const detail = parseReportDetail(value)
    expect(JSON.stringify(detail)).not.toContain("hidden")
  })
})

describe("cancelled report summaries", () => {
  const cancelledSummary = {
    id: reportID(1), source: "run", run_id: reportID(2),
    generated_at: "2026-10-29T03:00:00Z", run_status: "cancelled",
    plan_name: "Regression", model_name: "gpt-test", channel_name: "primary",
    passed: false, verdict: "cancelled", issue_count: 0,
    case_count: 0, failed_case_count: 64,
    passed_case_count: 0, verified_case_count: 64, observed_case_count: 0, indeterminate_case_count: 0,
    attachment_count: 0,
  }

  it("keeps a cancelled run whose request verdicts exceed the summarized case count", () => {
    const snapshot = parseReportSnapshot({ schema_version: 1, reports: [cancelledSummary] })
    expect(snapshot.reports[0].case_count).toBe(0)
    expect(snapshot.reports[0].failed_case_count).toBe(64)
  })

  it("still rejects a passing summary that contains failed cases", () => {
    const passing = { ...cancelledSummary, passed: true, run_status: "completed", verdict: "pass" }
    expect(() => parseReportSnapshot({ schema_version: 1, reports: [passing] })).toThrow()
  })
})
