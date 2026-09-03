import { describe, expect, it } from "vitest"

import {
  LANGUAGE_STORAGE_KEY,
  applyDocumentLocale,
  loadLanguagePreference,
  resolveLocale,
  saveLanguagePreference,
} from "./locale"

describe("locale preference", () => {
  it("resolves explicit and system language preferences to supported locales", () => {
    expect(resolveLocale("en-US", ["zh-CN"])).toBe("en-US")
    expect(resolveLocale("system", ["fr-FR", "en-GB"])).toBe("en-US")
    expect(resolveLocale("system", ["zh-Hant-TW"])).toBe("zh-CN")
    expect(resolveLocale("system", ["fr-FR"])).toBe("zh-CN")
  })

  it("round-trips a valid preference and ignores corrupt optional storage", () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    }

    saveLanguagePreference(storage, "en-US")
    expect(values.get(LANGUAGE_STORAGE_KEY)).toBe(
      JSON.stringify({ version: 1, language: "en-US" }),
    )
    expect(loadLanguagePreference(storage)).toBe("en-US")

    values.set(LANGUAGE_STORAGE_KEY, "not-json")
    expect(loadLanguagePreference(storage)).toBe("system")
    values.set(
      LANGUAGE_STORAGE_KEY,
      JSON.stringify({ version: 1, language: "de-DE" }),
    )
    expect(loadLanguagePreference(storage)).toBe("system")
  })

  it("applies the resolved locale to document language and direction", () => {
    const root = document.documentElement
    applyDocumentLocale(root, "en-US")
    expect(root.lang).toBe("en-US")
    expect(root.dir).toBe("ltr")
  })
})
