import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, it, vi } from "vitest"

import { ThemeProvider } from "@/app/theme"
import { TooltipProvider } from "@/components/ui/tooltip"
import { DesktopShell } from "./desktop-shell"

function renderShell() {
  return render(<ThemeProvider><TooltipProvider>
    <DesktopShell activePage="runs" onNavigate={() => undefined}>Workspace</DesktopShell>
  </TooltipProvider></ThemeProvider>)
}

function installRuntime() {
  const runtime = {
    WindowMinimise: vi.fn(),
    WindowToggleMaximise: vi.fn(),
    WindowIsMaximised: vi.fn(() => Promise.resolve(false)),
    Quit: vi.fn(),
  }
  Object.defineProperty(window, "runtime", { configurable: true, value: runtime })
  return runtime
}

afterEach(() => {
  delete (window as Window & { runtime?: unknown }).runtime
  window.localStorage.clear()
  vi.restoreAllMocks()
})

it("uses the native window commands and refreshes the restore action from native resize state", async () => {
  const runtime = installRuntime()
  const user = userEvent.setup()
  renderShell()
  await waitFor(() => expect(runtime.WindowIsMaximised).toHaveBeenCalledOnce())

  await user.click(screen.getByRole("button", { name: "最小化" }))
  expect(runtime.WindowMinimise).toHaveBeenCalledOnce()
  await user.click(screen.getByRole("button", { name: "最大化" }))
  expect(runtime.WindowToggleMaximise).toHaveBeenCalledOnce()
  expect(screen.queryByRole("button", { name: "还原窗口" })).not.toBeInTheDocument()

  runtime.WindowIsMaximised.mockResolvedValue(true)
  fireEvent(window, new Event("resize"))
  const restore = await screen.findByRole("button", { name: "还原窗口" })
  await user.click(restore)
  expect(runtime.WindowToggleMaximise).toHaveBeenCalledTimes(2)

  runtime.WindowIsMaximised.mockResolvedValue(false)
  fireEvent(window, new Event("focus"))
  await screen.findByRole("button", { name: "最大化" })

  const close = screen.getByRole("button", { name: "关闭窗口" })
  close.focus()
  await user.keyboard("{Enter}")
  expect(runtime.Quit).toHaveBeenCalledOnce()
})

it("ignores outdated native state queries and removes observation on unmount", async () => {
  const runtime = installRuntime()
  let resolveInitial!: (value: boolean) => void
  runtime.WindowIsMaximised.mockImplementationOnce(() => new Promise(resolve => { resolveInitial = resolve }))
  const view = renderShell()

  runtime.WindowIsMaximised.mockResolvedValue(true)
  fireEvent(window, new Event("resize"))
  await screen.findByRole("button", { name: "还原窗口" })
  await act(async () => { resolveInitial(false) })
  expect(screen.getByRole("button", { name: "还原窗口" })).toBeInTheDocument()

  view.unmount()
  const calls = runtime.WindowIsMaximised.mock.calls.length
  fireEvent(window, new Event("resize"))
  fireEvent(window, new Event("focus"))
  expect(runtime.WindowIsMaximised).toHaveBeenCalledTimes(calls)
})

it("maximises on title-bar double click and leaves interactive areas independent", () => {
  const runtime = installRuntime()
  renderShell()
  fireEvent.doubleClick(screen.getByRole("banner"))
  expect(runtime.WindowToggleMaximise).toHaveBeenCalledOnce()

  fireEvent.doubleClick(screen.getByRole("button", { name: "总览" }))
  fireEvent.doubleClick(screen.getByRole("button", { name: "界面设置，主题：跟随系统" }))
  fireEvent.doubleClick(screen.getByRole("button", { name: "最大化" }))
  expect(runtime.WindowToggleMaximise).toHaveBeenCalledOnce()
})

it("disables native-only controls when opened in a regular browser", () => {
  renderShell()
  for (const name of ["最小化", "最大化", "关闭窗口"]) {
    expect(screen.getByRole("button", { name })).toBeDisabled()
  }
})
