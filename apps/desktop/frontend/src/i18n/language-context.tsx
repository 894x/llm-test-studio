import { useEffect, useMemo, useState, type ReactNode } from "react"
import type { i18n } from "i18next"
import { I18nextProvider } from "react-i18next"

import {
  applyDocumentLocale,
  loadLanguagePreference,
  resolveLocale,
  saveLanguagePreference,
  type LanguagePreference,
} from "./locale"
import { LanguageContext } from "./language-state"

export type LanguageRuntime = {
  storage: Pick<Storage, "getItem" | "setItem">
  systemLanguages: () => readonly string[]
  documentRoot: Pick<HTMLElement, "lang" | "dir">
  events: Pick<EventTarget, "addEventListener" | "removeEventListener">
}

export function LanguageProvider({
  children,
  instance,
  runtime = browserLanguageRuntime,
}: {
  children: ReactNode
  instance: i18n
  runtime?: LanguageRuntime
}) {
  const [preference, setPreference] = useState<LanguagePreference>(() =>
    loadLanguagePreference(runtime.storage),
  )
  const [, setSystemRevision] = useState(0)
  const locale = resolveLocale(preference, runtime.systemLanguages())

  useEffect(() => {
    applyDocumentLocale(runtime.documentRoot, locale)
    void instance.changeLanguage(locale)
  }, [instance, locale, runtime])

  useEffect(() => {
    if (preference !== "system") return
    const handleLanguageChange = () => setSystemRevision((value) => value + 1)
    runtime.events.addEventListener("languagechange", handleLanguageChange)
    return () =>
      runtime.events.removeEventListener("languagechange", handleLanguageChange)
  }, [preference, runtime])

  const value = useMemo(() => ({
    preference,
    locale,
    setPreference: (next: LanguagePreference) => {
      setPreference(next)
      try {
        saveLanguagePreference(runtime.storage, next)
      } catch {
        // Optional UI preferences must not block the desktop workspace.
      }
    },
  }), [locale, preference, runtime])

  return (
    <I18nextProvider i18n={instance}>
      <LanguageContext.Provider value={value}>
        {children}
      </LanguageContext.Provider>
    </I18nextProvider>
  )
}

const browserLanguageRuntime: LanguageRuntime = {
  storage: window.localStorage,
  systemLanguages: () => navigator.languages,
  documentRoot: document.documentElement,
  events: window,
}
