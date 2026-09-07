import { describe, expect, it } from "vitest"

import { createAppI18n } from "./i18n"

describe("application translations", () => {
  it("renders shell text in the selected language and supports live switching", async () => {
    const instance = await createAppI18n("en-US")
    expect(instance.t("navigation.overview", { ns: "shell" })).toBe("Overview")

    await instance.changeLanguage("zh-CN")
    expect(instance.t("navigation.overview", { ns: "shell" })).toBe("总览")
  })

  it("falls back to Simplified Chinese for an unsupported lookup locale", async () => {
    const instance = await createAppI18n("en-US")
    expect(instance.t("product.subtitle", { ns: "shell", lng: "fr-FR" })).toBe(
      "本地测试工作台",
    )
  })
})
