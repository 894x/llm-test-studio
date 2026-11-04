import { describe, expect, it } from "vitest"
import { reportID } from "@/test/protocol-report-fixture"
import { performanceOverviewFixture } from "@/test/performance-overview-fixture"
import { parsePerformanceOverviews } from "./performance-overview"

describe("Go-owned performance overview contract", () => {
  it("preserves the projection, accepts absent additive data, and does not mutate input", () => {
    const source = { [reportID(1)]: performanceOverviewFixture() }
    const before = structuredClone(source)
    expect(parsePerformanceOverviews(source, [reportID(1)])).toEqual(source)
    expect(parsePerformanceOverviews(undefined, [])).toBeUndefined()
    expect(source).toEqual(before)
  })
  it.each(["status", "count", "value", "target", "ownership", "order"])("rejects malformed %s data", kind => {
    const overview = performanceOverviewFixture()
    if (kind === "status") Object.assign(overview.metrics[0], { status: "invented" })
    if (kind === "count") overview.passed_count = 5
    if (kind === "value") overview.metrics[2].value = Number.NaN
    if (kind === "target") overview.metrics[3].target = null
    if (kind === "order") overview.metrics.reverse()
    expect(() => parsePerformanceOverviews({ [reportID(1)]: overview }, kind === "ownership" ? [] : [reportID(1)])).toThrow()
  })
})
