import { describe, expect, it } from "vitest"
import { parseReportGenerationProgress, parseReportGenerationSnapshot } from "./generation-progress"

const progress = {
  sequence: 1, run_id: "88888888-8888-4888-8888-888888888888", phase: "building",
  processed: 64, total: 169, elapsed_ms: 120,
}

describe("report generation boundary", () => {
  it("accepts measured work and a ready report identity", () => {
    expect(parseReportGenerationProgress(progress).total).toBe(169)
    expect(parseReportGenerationProgress({ ...progress, phase: "ready", report_id: progress.run_id }).phase).toBe("ready")
  })
  it.each([
    { phase: "unknown" }, { sequence: 0 }, { run_id: "invalid" }, { processed: 170 },
    { elapsed_ms: -1 }, { total: NaN }, { total: 1.5 }, { private_error: "secret" },
    { phase: "ready" }, { phase: "failed", report_id: progress.run_id },
  ])("rejects malformed or unrestricted fields %j", (change) => {
    expect(() => parseReportGenerationProgress({ ...progress, ...change })).toThrow()
  })
  it("rejects unsupported snapshot formats and duplicate runs", () => {
    expect(() => parseReportGenerationSnapshot({ schema_version: 0, runs: [] })).toThrow()
    expect(() => parseReportGenerationSnapshot({ schema_version: 1, runs: [progress, progress] })).toThrow()
  })
})
