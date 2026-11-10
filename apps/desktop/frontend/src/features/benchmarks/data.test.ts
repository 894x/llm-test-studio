import { describe, expect, it } from "vitest"
import { benchmarkReport, benchmarkRun } from "@/test/benchmark-fixtures"
import { parseBenchmarkReport, parseBenchmarkRun } from "./data"

describe("benchmark desktop boundary", () => {
  it("accepts frozen Inspect scores and bounded sample evidence", () => {
    expect(parseBenchmarkRun(benchmarkRun()).datasets[0].metrics[0].value).toBe(1)
    expect(parseBenchmarkReport(benchmarkReport()).samples[0].scores[0].value).toBe("C")
  })
  it("rejects newer versions, secret fields, invalid counts, and unknown lifecycle states", () => {
    const newer = { ...benchmarkRun(), schema_version: 2 }
    expect(() => parseBenchmarkRun(newer)).toThrow()
    expect(() => parseBenchmarkRun({ ...benchmarkRun(), api_key: "secret" })).toThrow()
    const run = benchmarkRun(); run.datasets[0].completed = 2
    expect(() => parseBenchmarkRun(run)).toThrow()
    expect(() => parseBenchmarkRun({ ...benchmarkRun(), status: "success" })).toThrow()
    const report = benchmarkReport(); report.samples[0].output = "x".repeat(24001)
    expect(() => parseBenchmarkReport(report)).toThrow()
  })
})
