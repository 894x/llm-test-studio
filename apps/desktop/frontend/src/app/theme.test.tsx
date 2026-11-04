import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"

import { TooltipProvider } from "@/components/ui/tooltip"
import { DesktopShell } from "@/features/shell/desktop-shell"
import { ThemeProvider } from "./theme"
import indexHtml from "../../index.html?raw"

const colorKey = "llm-test-studio:color-theme:v1"
const modeKey = "llm-test-studio:ui-preferences:v1"
const bootstrap = indexHtml.match(/<script>([\s\S]*?)<\/script>/)![1]

function renderShell() {
  return render(<ThemeProvider><TooltipProvider>
    <DesktopShell activePage="runs" onNavigate={() => undefined}>Workspace</DesktopShell>
  </TooltipProvider></ThemeProvider>)
}

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear()
  document.documentElement.className = ""
  delete document.documentElement.dataset.colorTheme
  delete document.documentElement.dataset.theme
  delete document.documentElement.dataset.themePreference
  document.documentElement.style.colorScheme = ""
})

it("switches color themes independently, restores selection, and retains legacy mode storage", async () => {
  window.localStorage.setItem(modeKey, JSON.stringify({ version: 1, theme: "dark" }))
  const user = userEvent.setup()
  const shell = renderShell()
  expect(document.documentElement).toHaveAttribute("data-color-theme", "classic")

  const trigger = screen.getByRole("button", { name: "界面设置，主题：深色" })
  await user.click(trigger)
  expect(screen.getByRole("menuitemradio", { name: "经典" })).toHaveAttribute("aria-checked", "true")
  await user.click(screen.getByRole("menuitemradio", { name: "高对比" }))
  expect(trigger).toHaveFocus()
  expect(document.documentElement).toHaveAttribute("data-color-theme", "high-contrast")
  expect(document.documentElement).toHaveAttribute("data-theme", "dark")
  expect(JSON.parse(window.localStorage.getItem(modeKey)!)).toEqual({ version: 1, theme: "dark" })
  expect(JSON.parse(window.localStorage.getItem(colorKey)!)).toEqual({ version: 1, colorTheme: "high-contrast" })

  shell.unmount()
  renderShell()
  await user.click(screen.getByRole("button", { name: "界面设置，主题：深色" }))
  expect(screen.getByRole("menuitemradio", { name: "高对比" })).toHaveAttribute("aria-checked", "true")
  await user.click(screen.getByRole("menuitemradio", { name: "浅色" }))
  expect(document.documentElement).toHaveAttribute("data-color-theme", "high-contrast")
  expect(document.documentElement).toHaveAttribute("data-theme", "light")

  await user.click(screen.getByRole("button", { name: "界面设置，主题：浅色" }))
  await user.click(screen.getByRole("menuitemradio", { name: "经典" }))
  expect(document.documentElement).toHaveAttribute("data-color-theme", "classic")
  expect(JSON.parse(window.localStorage.getItem(colorKey)!)).toEqual({ version: 1, colorTheme: "classic" })
})

it("retains high contrast when the system color scheme changes", () => {
  window.localStorage.setItem(colorKey, JSON.stringify({ version: 1, colorTheme: "high-contrast" }))
  const media = new EventTarget() as EventTarget & { matches: boolean }
  media.matches = false
  vi.spyOn(window, "matchMedia").mockImplementation(() => media as MediaQueryList)
  const shell = renderShell()
  expect(document.documentElement).toHaveAttribute("data-theme", "light")
  act(() => { media.matches = true; media.dispatchEvent(new Event("change")) })
  expect(document.documentElement).toHaveClass("dark")
  expect(document.documentElement).toHaveAttribute("data-color-theme", "high-contrast")
  shell.unmount()
  media.matches = false
  media.dispatchEvent(new Event("change"))
  expect(document.documentElement).toHaveClass("dark")
})

it.each(["light", "dark", "system"])("applies the saved colors before React for %s mode", (theme) => {
  window.localStorage.setItem(modeKey, JSON.stringify({ version: 1, theme }))
  window.localStorage.setItem(colorKey, JSON.stringify({ version: 1, colorTheme: "high-contrast" }))
  const runBootstrap = new Function("document", "localStorage", "navigator", "matchMedia", bootstrap)
  runBootstrap(document, window.localStorage, navigator, () => ({ matches: true }))
  expect(document.documentElement).toHaveAttribute("data-color-theme", "high-contrast")
  expect(document.documentElement).toHaveAttribute("data-theme", theme === "light" ? "light" : "dark")
})

it.each(['{"version":2,"colorTheme":"high-contrast"}', '{"version":1,"colorTheme":"unknown"}', "{broken"])("keeps unrecognized optional colors untouched: %s", (saved) => {
  window.localStorage.setItem(colorKey, saved)
  const runBootstrap = new Function("document", "localStorage", "navigator", "matchMedia", bootstrap)
  runBootstrap(document, window.localStorage, navigator, window.matchMedia)
  expect(document.documentElement).toHaveAttribute("data-color-theme", "classic")
  renderShell()
  expect(document.documentElement).toHaveAttribute("data-color-theme", "classic")
  expect(window.localStorage.getItem(colorKey)).toBe(saved)
})

it("can change colors with unavailable optional storage", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked") })
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked") })
  const user = userEvent.setup()
  renderShell()
  await user.click(screen.getByRole("button", { name: "界面设置，主题：跟随系统" }))
  await user.click(screen.getByRole("menuitemradio", { name: "高对比" }))
  expect(document.documentElement).toHaveAttribute("data-color-theme", "high-contrast")
})
