import { describe, expect, it } from "vitest"

import { performanceProgressPhaseLabel } from "./performance-summary"

describe("performanceProgressPhaseLabel", () => {
  it("names warmup and ramp phases without presenting either as steady-state sending", () => {
    expect(performanceProgressPhaseLabel("warming_up")).toBe("正在热身")
    expect(performanceProgressPhaseLabel("ramping")).toBe("正在爬坡")
  })
})
