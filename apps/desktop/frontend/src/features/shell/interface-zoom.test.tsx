import { act, fireEvent, render, renderHook, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, test, vi } from "vitest"
import { ThemeProvider } from "@/app/theme"
import { TooltipProvider } from "@/components/ui/tooltip"
import { DesktopShell } from "./desktop-shell"
import { useInterfaceZoom } from "./use-interface-zoom"

beforeEach(() => localStorage.clear())
afterEach(() => { localStorage.clear(); vi.useRealTimers() })

test("keyboard and wheel zoom keep an explicit app scale and reset without using display DPI", () => {
  const { result, unmount } = renderHook(useInterfaceZoom)
  expect(result.current.percent).toBe(100)
  fireEvent.keyDown(window, { key: "+", ctrlKey: true })
  expect(result.current.percent).toBe(110)
  expect(document.documentElement.style.zoom).toBe("1.1")
  fireEvent.wheel(window, { deltaY: -120, ctrlKey: true })
  expect(result.current.percent).toBe(125)
  fireEvent.keyDown(window, { key: "+", ctrlKey: true })
  expect(result.current.percent).toBe(125)
  fireEvent.keyDown(window, { key: "0", ctrlKey: true })
  expect(result.current.percent).toBe(100)
  fireEvent.keyDown(window, { key: "-" })
  fireEvent.wheel(window, { deltaY: -120 })
  expect(result.current.percent).toBe(100)
  unmount()
  expect(document.documentElement.style.zoom).toBe("")
})

test("zoom survives reopening and rejects malformed or unsupported saved preferences", () => {
  localStorage.setItem("llm-test-studio:interface-zoom:v1", JSON.stringify({ version: 1, percent: 90 }))
  const first = renderHook(useInterfaceZoom)
  expect(first.result.current.percent).toBe(90)
  act(() => first.result.current.change(-1))
  first.unmount()
  const second = renderHook(useInterfaceZoom)
  expect(second.result.current.percent).toBe(80)
  expect(second.result.current.canDecrease).toBe(false)
  second.unmount()
  localStorage.setItem("llm-test-studio:interface-zoom:v1", JSON.stringify({ version: 2, percent: 999 }))
  const third = renderHook(useInterfaceZoom)
  expect(third.result.current.percent).toBe(100)
  third.unmount()
})

test("changed zoom is announced briefly and keeps a persistent keyboard-accessible reset", async () => {
  const user = userEvent.setup()
  render(<ThemeProvider><TooltipProvider><DesktopShell activePage="reports" onNavigate={() => undefined}>Report</DesktopShell></TooltipProvider></ThemeProvider>)
  fireEvent.keyDown(window, { key: "=", ctrlKey: true })
  expect(screen.getByRole("status")).toHaveTextContent("界面缩放 110%")
  const reset = screen.getByRole("button", { name: "当前缩放 110%，恢复 100%" })
  await user.click(reset)
  expect(screen.getByRole("status")).toHaveTextContent("界面缩放 100%")
  expect(screen.queryByRole("button", { name: /当前缩放/ })).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: /界面设置，主题/ }))
  expect(screen.getByRole("menuitem", { name: "放大" })).toBeInTheDocument()
  expect(screen.getByText(/Ctrl.*滚轮/)).toBeInTheDocument()
  await user.click(screen.getByRole("menuitem", { name: "缩小" }))
  expect(screen.getByRole("button", { name: "当前缩放 90%，恢复 100%" })).toBeInTheDocument()
})
