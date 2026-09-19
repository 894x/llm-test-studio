import { describe, expect, it } from "vitest"

import { DesktopDataError } from "@/app/data-error"
import { caseTypeLabel } from "@/features/catalog/presentation"
import { parseCatalogSnapshot } from "@/features/catalog/data"
import { performanceCapacitySummary, performanceCompletion, performanceProgressPhaseLabel } from "@/features/quick-test/performance-summary"
import { createAppI18n } from "./i18n"
import { localizeStoredMessage, translateExecutionError, translateRunFailureSummary } from "./runtime"

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
    expect(caseTypeLabel("openai-chat")).toBe("OpenAI Chat")
    expect(caseTypeLabel("custom.type", "User-defined name")).toBe("User-defined name")
    expect(() => parseCatalogSnapshot({})).toThrow(DesktopDataError)
    document.documentElement.lang = "zh-CN"
    expect(performanceProgressPhaseLabel("ramping")).toBe("正在爬坡")
  })

  it("translates shared execution error codes without leaking unknown values as resource keys", () => {
    document.documentElement.lang = "zh-CN"
    createAppI18n("zh-CN")
    expect(translateExecutionError("authentication_failed")).toBe("鉴权失败")
    expect(translateRunFailureSummary("execute", "run_execution_failed")).toBe("执行 · 运行执行失败")
    expect(translateExecutionError("provider_said_sk-secret")).toBe("provider_said_sk-secret")
    document.documentElement.lang = "en-US"
    createAppI18n("en-US")
    expect(translateExecutionError("authentication_failed", "en-US")).toBe("Authentication failed")
    expect(translateRunFailureSummary("persist_suite_result", "result_persistence_failed", "en-US")).toBe(
      "Saving suite results · The run could not save results",
    )
    document.documentElement.lang = "zh-CN"
  })
})
