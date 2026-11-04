import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

import { ThemeContext, type ColorTheme, type ThemePreference } from "./theme-context"

const STORAGE_KEY = "llm-test-studio:ui-preferences:v1"
const COLOR_THEME_STORAGE_KEY = "llm-test-studio:color-theme:v1"
const DARK_QUERY = "(prefers-color-scheme: dark)"

type NativeWindowThemeRuntime = Partial<{
  WindowSetSystemDefaultTheme: () => void
  WindowSetLightTheme: () => void
  WindowSetDarkTheme: () => void
}>

function isThemePreference(value: unknown): value is ThemePreference {
  return value === "system" || value === "light" || value === "dark"
}

function loadTheme(): ThemePreference {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return "system"
    const parsed = JSON.parse(raw) as { version?: unknown; theme?: unknown }
    return parsed.version === 1 && isThemePreference(parsed.theme)
      ? parsed.theme
      : "system"
  } catch {
    return "system"
  }
}

function resolveTheme(theme: ThemePreference): "light" | "dark" {
  const dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia(DARK_QUERY).matches)
  return dark ? "dark" : "light"
}

function loadColorTheme(): ColorTheme {
  try {
    const saved = JSON.parse(
      window.localStorage.getItem(COLOR_THEME_STORAGE_KEY) || "null",
    ) as { version?: unknown; colorTheme?: unknown } | null
    return saved?.version === 1 && saved.colorTheme === "high-contrast"
      ? "high-contrast"
      : "classic"
  } catch {
    return "classic"
  }
}

function applyNativeWindowTheme(theme: ThemePreference) {
  const runtime = (window as Window & { runtime?: NativeWindowThemeRuntime })
    .runtime
  const apply =
    theme === "dark"
      ? runtime?.WindowSetDarkTheme
      : theme === "light"
        ? runtime?.WindowSetLightTheme
        : runtime?.WindowSetSystemDefaultTheme
  apply?.()
}

function applyTheme(theme: ThemePreference) {
  const resolved = resolveTheme(theme)
  document.documentElement.classList.toggle("dark", resolved === "dark")
  document.documentElement.dataset.theme = resolved
  document.documentElement.dataset.themePreference = theme
  document.documentElement.style.colorScheme = resolved
  applyNativeWindowTheme(theme)
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemePreference>(loadTheme)
  const [colorTheme, setColorThemeState] = useState<ColorTheme>(loadColorTheme)

  useLayoutEffect(() => {
    document.documentElement.dataset.colorTheme = colorTheme
  }, [colorTheme])

  const setColorTheme = useCallback((next: ColorTheme) => {
    setColorThemeState(next)
    try {
      window.localStorage.setItem(
        COLOR_THEME_STORAGE_KEY,
        JSON.stringify({ version: 1, colorTheme: next }),
      )
    } catch {
      // Keep the selected colors usable when optional storage is unavailable.
    }
  }, [])

  useLayoutEffect(() => {
    applyTheme(theme)
    try {
      window.localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({ version: 1, theme }),
      )
    } catch {
      // Theme preference is optional; the desktop app remains usable without it.
    }

    if (theme !== "system") return
    const media = window.matchMedia(DARK_QUERY)
    const syncSystemTheme = () => applyTheme("system")
    media.addEventListener("change", syncSystemTheme)
    return () => media.removeEventListener("change", syncSystemTheme)
  }, [theme])

  const value = useMemo(
    () => ({ theme, setTheme: setThemeState, colorTheme, setColorTheme }),
    [theme, colorTheme, setColorTheme],
  )

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}
