import { describe, expect, it } from "vitest"

import { DesktopDataError } from "@/app/data-error"
import { caseTypeLabel } from "@/features/catalog/presentation"
import { parseCatalogSnapshot } from "@/features/catalog/data"
import { performanceCapacitySummary, performanceCompletion, performanceProgressPhaseLabel } from "@/features/quick-test/performance-summary"
import { createAppI18n } from "./i18n"
import { localizeStoredMessage } from "./runtime"

describe("desktop translation integration", () => {
  it("switches retained validation errors in both directions, including range parameters", () => {
    const en = createAppI18n("en-US")
    const zh = createAppI18n("zh-CN")
    expect(localizeStoredMessage("请输入 1–256 的整数。", en.t)).toBe("Enter an integer between 1 and 256.")
    expect(localizeStoredMessage("Enter an integer between 1 and 256.", zh.t)).toBe("请输入 1–256 的整数。")
    expect(localizeStoredMessage("请输入渠道名称。", en.t)).toBe("Enter a channel name.")
    expect(localizeStoredMessage("阶梯 2 预热次数", en.t)).toBe("Step 2 warmups")
    expect(localizeStoredMessage("My custom project name", zh.t)).toBe("My custom project name")
  })

  it("localizes non-React summaries and registered case types at call time", () => {
    document.documentElement.lang = "en-US"
    createAppI18n("en-US")
    expect(performanceProgressPhaseLabel("warming_up")).toBe("Warming up")
    expect(performanceProgressPhaseLabel("ramping")).toBe("Ramping")
    expect(performanceCompletion(0, 2, 100).label).toBe("Completed (duration mode)")
    expect(performanceCapacitySummary({ status: "failed", rungs: [] }, "fixed_concurrency")).toBe("Capacity failed")
    expect(caseTypeLabel("latency.input_ladder")).toBe("Input latency ladder")
    expect(caseTypeLabel("custom.type", "User-defined name")).toBe("User-defined name")
    expect(() => parseCatalogSnapshot({})).toThrow(DesktopDataError)
    document.documentElement.lang = "zh-CN"
    expect(performanceProgressPhaseLabel("ramping")).toBe("正在爬坡")
  })
})
