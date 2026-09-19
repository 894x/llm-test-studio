import { describe, expect, it } from "vitest"
import { protocolReportFixture, reportID } from "@/test/protocol-report-fixture"
import { parseReportDetail } from "./data"

describe("current protocol report contract", () => {
  it("keeps observed execution separate from a pass and preserves snapshot parameters", () => {
    const detail = parseReportDetail(protocolReportFixture("not_applicable"))
    if (detail.source !== "run") throw new Error("run expected")
    expect(detail.entries[0].verification).toEqual({ status: "not_applicable", passed: 0, failed: 0, observed: 1, indeterminate: 0 })
    expect(detail.entries[0].parameters).toEqual({ content_length: 4000 })
    expect(detail.entries[0].cases[0].request_results[0].observation?.http_status).toBe(400)
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
