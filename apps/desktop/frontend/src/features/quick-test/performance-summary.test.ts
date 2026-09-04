import { describe, expect, it } from "vitest"

import {
  performanceCapacitySummary,
  performanceProgressPhaseLabel,
  performanceSLOStatusLabel,
} from "./performance-summary"

describe("performanceProgressPhaseLabel", () => {
  it("names warmup and ramp phases without presenting either as steady-state sending", () => {
    expect(performanceProgressPhaseLabel("warming_up")).toBe("正在热身")
    expect(performanceProgressPhaseLabel("ramping")).toBe("正在爬坡")
  })

  it("keeps transport success separate from every SLO assessment state", () => {
    expect(performanceSLOStatusLabel("passed")).toBe("SLO 通过")
    expect(performanceSLOStatusLabel("failed")).toBe("SLO 未通过")
    expect(performanceSLOStatusLabel("not_evaluated")).toBe("SLO 未评估")
  })

  it("summarizes the highest passing and first failing capacity targets", () => {
    expect(performanceCapacitySummary({
      status: "failed",
      selected_rung_index: 1,
      highest_passing_rung_index: 1,
      rungs: [
        { index: 0, target: 1, slo_assessment: { status: "passed" } },
        { index: 1, target: 2, slo_assessment: { status: "passed" } },
        { index: 2, target: 3, slo_assessment: { status: "failed" } },
      ],
    }, "fixed_concurrency")).toBe("最高通过并发 2 · 首次未通过 3")
    expect(performanceCapacitySummary({
      status: "not_evaluated",
      selected_rung_index: 0,
      rungs: [{ index: 0, target: 0.5, slo_assessment: { status: "not_evaluated" } }],
    }, "open_loop")).toBe("容量评估未完成 · 当前 RPS 0.5")
  })
})
