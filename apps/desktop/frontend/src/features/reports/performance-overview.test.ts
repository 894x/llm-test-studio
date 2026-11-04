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
  it("accepts missing and unavailable additive ratings without inventing a score", () => {
    const overview = performanceOverviewFixture()
    delete overview.rating
    expect(parsePerformanceOverviews({ [reportID(1)]: overview }, [reportID(1)])?.[reportID(1)]).toEqual(overview)
    overview.rating = null
    expect(parsePerformanceOverviews({ [reportID(1)]: overview }, [reportID(1)])?.[reportID(1)].rating).toBeNull()
  })
  it.each([
    { score: -1 }, { score: 5.01 }, { score: Number.NaN }, { max_score: 10 },
    { method: "unpublished" }, { metric_count: 7 },
  ])("rejects malformed rating %j", invalid => {
    const overview = performanceOverviewFixture()
    Object.assign(overview.rating!, invalid)
    expect(() => parsePerformanceOverviews({ [reportID(1)]: overview }, [reportID(1)])).toThrow()
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
