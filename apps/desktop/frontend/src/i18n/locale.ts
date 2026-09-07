export const SUPPORTED_LOCALES = ["zh-CN", "en-US"] as const

export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number]
export type LanguagePreference = "system" | SupportedLocale

export const LANGUAGE_STORAGE_KEY = "llm-studio:language-preference:v1"

export function resolveLocale(
  preference: LanguagePreference,
  systemLanguages: readonly string[],
): SupportedLocale {
  if (preference !== "system") return preference
  for (const language of systemLanguages) {
    const normalized = normalizeLocale(language)
    if (normalized) return normalized
  }
  return "zh-CN"
}

export function loadLanguagePreference(
  storage: Pick<Storage, "getItem">,
): LanguagePreference {
  try {
    const saved = JSON.parse(storage.getItem(LANGUAGE_STORAGE_KEY) ?? "null")
    return saved?.version === 1 && isLanguagePreference(saved.language)
      ? saved.language
      : "system"
  } catch {
    return "system"
  }
}

export function saveLanguagePreference(
  storage: Pick<Storage, "setItem">,
  preference: LanguagePreference,
): void {
  storage.setItem(
    LANGUAGE_STORAGE_KEY,
    JSON.stringify({ version: 1, language: preference }),
  )
}

export function applyDocumentLocale(
  root: Pick<HTMLElement, "lang" | "dir">,
  locale: SupportedLocale,
): void {
  root.lang = locale
  root.dir = "ltr"
}

function normalizeLocale(value: string): SupportedLocale | null {
  const normalized = value.trim().toLowerCase()
  if (normalized === "en" || normalized.startsWith("en-")) return "en-US"
  if (normalized === "zh" || normalized.startsWith("zh-")) return "zh-CN"
  return null
}

function isLanguagePreference(value: unknown): value is LanguagePreference {
  return value === "system" || SUPPORTED_LOCALES.includes(value as SupportedLocale)
}
