import { fireEvent, render, screen } from "@testing-library/react"
import { lazy, useState } from "react"
import { afterEach, expect, it, vi } from "vitest"

import { RetainedWorkspace } from "./retained-workspace"

afterEach(() => {
  delete (window as Window & { runtime?: unknown }).runtime
  vi.restoreAllMocks()
})

it("contains a rejected page chunk, preserves navigation and drafts, and records a bounded diagnostic", async () => {
  vi.spyOn(console, "error").mockImplementation(() => undefined)
  const logError = vi.fn()
  Object.defineProperty(window, "runtime", { configurable: true, value: { LogError: logError } })
  const error = new TypeError("Failed to fetch dynamically imported module: http://wails.localhost/assets/report-missing.js")
  const BrokenReports = lazy(() => Promise.reject(error))
  function Workspace() {
    const [reports, setReports] = useState(false)
    return <>
      <nav><button onClick={() => setReports(false)}>运行</button><button onClick={() => setReports(true)}>报告</button></nav>
      <RetainedWorkspace active={!reports} fallback="Loading"><input aria-label="运行筛选" defaultValue="" /></RetainedWorkspace>
      <RetainedWorkspace active={reports} fallback="Loading"><BrokenReports /></RetainedWorkspace>
    </>
  }
  render(<Workspace />)
  const draft = screen.getByRole("textbox", { name: "运行筛选" })
  fireEvent.change(draft, { target: { value: "keep my filter" } })
  fireEvent.click(screen.getByRole("button", { name: "报告" }))

  expect(await screen.findByRole("alert")).toHaveTextContent("页面加载失败")
  expect(screen.getByRole("button", { name: "重新加载界面" })).toBeInTheDocument()
  expect(screen.queryByText(error.message)).not.toBeInTheDocument()
  expect(logError).toHaveBeenCalledWith(JSON.stringify({
    component: "frontend", operation: "load_workspace", error_code: "frontend_operation_failed",
    detail: `render workspace: ${error.message}`,
  }))

  fireEvent.click(screen.getByRole("button", { name: "运行" }))
  expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  expect(screen.getByRole("textbox", { name: "运行筛选" })).toBe(draft)
  expect(draft).toHaveValue("keep my filter")
})

it("contains a synchronous page render exception without requiring the native runtime", async () => {
  vi.spyOn(console, "error").mockImplementation(() => undefined)
  function BrokenPage(): never { throw new Error("internal render failure") }
  render(<RetainedWorkspace active fallback="Loading"><BrokenPage /></RetainedWorkspace>)
  expect(await screen.findByRole("alert")).toHaveTextContent("页面加载失败")
  expect(screen.queryByText("internal render failure")).not.toBeInTheDocument()
})
