import { describe, expect, it } from "vitest"
import { protocolReportFixture, reportID } from "@/test/protocol-report-fixture"
import { parseReportDetail } from "./data"
import type { ExecutionOrigin } from "./execution-origin"

describe("current protocol report contract", () => {
  it("preserves dimension statistics from Core and accepts earlier details without the additive fields", () => {
    const source = protocolReportFixture()
    const statistic = { dimension: "parameters", case_count: 5, passed_case_count: 1, failed_case_count: 1,
      observed_case_count: 1, indeterminate_case_count: 1, not_run_case_count: 1, pass_rate_percent: 50 }
    const parsed = parseReportDetail({ ...source, dimension_statistics: [statistic] })
    if (parsed.source !== "run") throw new Error("run expected")
    expect(parsed.dimension_statistics).toEqual([statistic])
    expect(parsed.dimension_statistics?.[0]).not.toBe(statistic)
    expect(parsed.entries[0].cases[0].dimension).toBe("parameters")
    delete source.entries[0].cases[0].dimension
    const before = structuredClone(source)
    const legacy = parseReportDetail(source)
    expect(legacy.source === "run" && legacy.dimension_statistics).toBeUndefined()
    expect(legacy.source === "run" && legacy.entries[0].cases[0]).not.toHaveProperty("dimension")
    expect(source).toEqual(before)
  })
  it("rejects malformed dimension statistics without modifying the source", () => {
    const source = protocolReportFixture()
    const statistic = { dimension: "tools", case_count: 2, passed_case_count: 1, failed_case_count: 1,
      observed_case_count: 0, indeterminate_case_count: 0, not_run_case_count: 0, pass_rate_percent: 50 }
    for (const patch of [{ dimension: " " }, { dimension: 1 }, { case_count: 3 }, { failed_case_count: -1 },
      { passed_case_count: 0.5 }, { pass_rate_percent: null }, { pass_rate_percent: 101 }, { pass_rate_percent: NaN }]) {
      const value = { ...source, dimension_statistics: [{ ...statistic, ...patch }] }
      const before = structuredClone(value)
      expect(() => parseReportDetail(value)).toThrow()
      expect(value).toEqual(before)
    }
    expect(() => parseReportDetail({ ...source, dimension_statistics: [statistic, statistic] })).toThrow()
    const observed = { ...statistic, passed_case_count: 0, failed_case_count: 0, observed_case_count: 2, pass_rate_percent: null }
    expect(() => parseReportDetail({ ...source, dimension_statistics: [observed] })).not.toThrow()
    expect(() => parseReportDetail({ ...source, dimension_statistics: [{ ...observed, pass_rate_percent: 0 }] })).toThrow()
    Object.assign(source.entries[0].cases[0], { dimension: " tools " })
    expect(() => parseReportDetail(source)).toThrow()
  })
  it("accepts an optional Go-owned performance summary and rejects malformed values without changing input", () => {
    const source = protocolReportFixture()
    const entry = source.entries[0]
    source.entries = [0, 1].map(index => ({ ...entry, entry_id: reportID(30 + index), target_kind: "performance", cases: [] }))
    source.request_results = []; source.report.case_results = []
    const summary = {
      item_count: 2, measured_item_count: 1, completed_item_count: 0, completed: 2, succeeded: 1, failed: 1, timed_out: 1,
      prompt_tokens: 100, completion_tokens: 10, cached_tokens: 80, success_rate_percent: 50, cache_rate_percent: 80,
      total_duration_ms: 1_000, successful_request_qps: 1, samples_complete: false, ttft: null, e2e: null,
    }
    const value = { ...source, performance_summary: summary }
    const parsed = parseReportDetail(value)
    if (parsed.source !== "run") throw new Error("run expected")
    expect(parsed.performance_summary).toEqual(summary)
    expect(parsed.performance_summary).not.toBe(summary)
    expect(parseReportDetail(source)).not.toHaveProperty("performance_summary", summary)
    for (const patch of [{ cached_tokens: -1 }, { cache_rate_percent: NaN }, { success_rate_percent: 101 }, { item_count: 3 }, { succeeded: 3 }, { ttft: { count: 1 } }]) {
      const invalid = { ...value, performance_summary: { ...summary, ...patch } }
      const before = structuredClone(invalid)
      expect(() => parseReportDetail(invalid)).toThrow()
      expect(invalid).toEqual(before)
    }
  })
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
