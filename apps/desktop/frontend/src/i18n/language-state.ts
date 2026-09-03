import { createContext, useContext } from "react"

import type { LanguagePreference, SupportedLocale } from "./locale"

export type LanguageContextValue = {
  preference: LanguagePreference
  locale: SupportedLocale
  setPreference: (preference: LanguagePreference) => void
}

export const LanguageContext = createContext<LanguageContextValue>({
  preference: "system",
  locale: "zh-CN",
  setPreference: () => undefined,
})

export function useLanguage(): LanguageContextValue {
  return useContext(LanguageContext)
}
