import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useTranslation } from "react-i18next"
import { describe, expect, it } from "vitest"

import { createAppI18n } from "./i18n"
import {
  LanguageProvider,
  type LanguageRuntime,
} from "./language-context"
import { useLanguage } from "./language-state"
import { LANGUAGE_STORAGE_KEY } from "./locale"

function LanguageProbe() {
  const { preference, locale, setPreference } = useLanguage()
  const { t } = useTranslation("shell")
  return (
    <div>
      <span>preference:{preference}</span>
      <span>locale:{locale}</span>
      <span>{t("navigation.overview")}</span>
      <button onClick={() => setPreference("zh-CN")}>switch</button>
    </div>
  )
}

describe("LanguageProvider", () => {
  it("loads, applies, persists, and live-switches the selected language", async () => {
    const values = new Map([
      [
        LANGUAGE_STORAGE_KEY,
        JSON.stringify({ version: 1, language: "en-US" }),
      ],
    ])
    const root = document.createElement("html")
    const runtime: LanguageRuntime = {
      storage: {
        getItem: (key) => values.get(key) ?? null,
        setItem: (key, value) => values.set(key, value),
      },
      systemLanguages: () => ["en-GB"],
      documentRoot: root,
      events: new EventTarget(),
    }
    const instance = await createAppI18n("zh-CN")

    render(
      <LanguageProvider instance={instance} runtime={runtime}>
        <LanguageProbe />
      </LanguageProvider>,
    )

    expect(await screen.findByText("Overview")).toBeInTheDocument()
    expect(screen.getByText("preference:en-US")).toBeInTheDocument()
    expect(screen.getByText("locale:en-US")).toBeInTheDocument()
    expect(root.lang).toBe("en-US")

    await userEvent.click(screen.getByRole("button", { name: "switch" }))

    expect(await screen.findByText("总览")).toBeInTheDocument()
    expect(root.lang).toBe("zh-CN")
    expect(values.get(LANGUAGE_STORAGE_KEY)).toBe(
      JSON.stringify({ version: 1, language: "zh-CN" }),
    )
  })
})
