import { createContext, useContext } from "react"

export type ThemePreference = "system" | "light" | "dark"
export type ColorTheme = "classic" | "high-contrast"

type ThemeContextValue = {
  theme: ThemePreference
  setTheme: (theme: ThemePreference) => void
  colorTheme: ColorTheme
  setColorTheme: (theme: ColorTheme) => void
}

export const ThemeContext = createContext<ThemeContextValue | null>(null)

export function useTheme() {
  const context = useContext(ThemeContext)
  if (!context) throw new Error("useTheme must be used inside ThemeProvider")
  return context
}
