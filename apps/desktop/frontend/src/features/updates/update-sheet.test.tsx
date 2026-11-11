import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { StrictMode, useRef, useState } from "react"
import { createAppI18n } from "@/i18n/i18n"
import { I18nextProvider } from "react-i18next"
import { TooltipProvider } from "@/components/ui/tooltip"
import { DesktopDataError } from "@/app/data-error"
import { parseDesktopUpdate, UNAVAILABLE_UPDATE, UNAVAILABLE_UPDATE_CLIENT, type DesktopUpdate, type DesktopUpdateClient } from "./data"
import { useDesktopUpdates } from "./use-desktop-updates"
import { UpdateSheet } from "./update-sheet"

const available: DesktopUpdate = { ...UNAVAILABLE_UPDATE, current_version: "v0.2.0", latest_version: "v0.3.0", status: "available", auto_check: true, install_kind: "restart", notes: "Changes", total: 2048 }
afterEach(() => vi.useRealTimers())

async function show(client: DesktopUpdateClient) {
  const i18n = await createAppI18n("en-US")
  function Surface() {
    const updates = useDesktopUpdates(client)
    const [open, setOpen] = useState(true)
    const trigger = useRef<HTMLButtonElement>(null)
    return <><button ref={trigger} onClick={() => setOpen(true)}>Open</button><UpdateSheet updates={updates} open={open} onOpenChange={setOpen} returnFocus={trigger} /></>
  }
  render(<StrictMode><I18nextProvider i18n={i18n}><TooltipProvider><Surface /></TooltipProvider></I18nextProvider></StrictMode>)
  await screen.findByText("v0.2.0")
}

describe("desktop update boundary and flow", () => {
  it("restores keyboard focus to the persistent entry after Escape", async () => {
    await show({ ...UNAVAILABLE_UPDATE_CLIENT, getDesktopUpdate: async () => ({ ...available }) })
    fireEvent.keyDown(screen.getByRole("dialog", { name: "Software updates" }), { key: "Escape" })
    await waitFor(() => expect(screen.getByRole("button", { name: "Open" })).toHaveFocus())
  })
  it("rejects unknown fields, versions, states, payloads, and unsafe counters", () => {
    expect(parseDesktopUpdate(available)).toEqual(available)
    for (const data of [null, { ...available, secret: "path" }, { ...available, schema_version: 2 }, { ...available, status: "done" },
      { ...available, downloaded: -1 }, { ...available, total: 2 ** 40 }, { ...available, auto_check: "yes" }, { ...available, notes: "x".repeat(32769) }]) {
      expect(() => parseDesktopUpdate(data)).toThrow(DesktopDataError)
    }
  })

  it("checks automatically once in StrictMode, downloads, requires confirmation, and shows deferred-install reason", async () => {
    const client = {
      ...UNAVAILABLE_UPDATE_CLIENT,
      getDesktopUpdate: vi.fn(async () => ({ ...available, status: "idle" as const, latest_version: "" })),
      checkDesktopUpdate: vi.fn(async () => ({ ...available })),
      downloadDesktopUpdate: vi.fn(async () => ({ ...available, status: "ready" as const, downloaded: 2048 })),
      installDesktopUpdate: vi.fn(async () => ({ ...available, status: "ready" as const, error_code: "tasks_active" as const })),
    }
    await show(client)
    expect(client.checkDesktopUpdate).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole("button", { name: "Download update" }))
    const install = await screen.findByRole("button", { name: "Restart and update" })
    expect(install).toBeDisabled()
    fireEvent.click(screen.getByRole("checkbox", { name: "I have saved my edits and agree to quit and install." }))
    expect(install).toBeEnabled()
    fireEvent.click(install)
    await screen.findByText("Tests, operations, or background downloads are still active. Wait before installing.")
    expect(client.installDesktopUpdate).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole("button", { name: "Close" }))
    fireEvent.click(screen.getByRole("button", { name: "Open" }))
    expect(screen.getByRole("button", { name: "Restart and update" })).toBeDisabled()
  })

  it("respects disabled automatic checks and saves preferences and skip through Core", async () => {
    const client = {
      ...UNAVAILABLE_UPDATE_CLIENT,
      getDesktopUpdate: vi.fn(async () => ({ ...available, auto_check: false })),
      checkDesktopUpdate: vi.fn(async () => ({ ...available })),
      setDesktopUpdatePreferences: vi.fn(async (auto: boolean, skip: boolean) => ({ ...available, auto_check: auto, status: skip ? "skipped" as const : "available" as const })),
    }
    await show(client)
    expect(client.checkDesktopUpdate).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("checkbox", { name: "Check for updates at startup" }))
    await waitFor(() => expect(client.setDesktopUpdatePreferences).toHaveBeenCalledWith(true, false))
    await waitFor(() => expect(screen.getByRole("button", { name: "Skip this version" })).toBeEnabled())
    fireEvent.click(screen.getByRole("button", { name: "Skip this version" }))
    await screen.findByText("Version skipped; manual download is still available")
    expect(client.setDesktopUpdatePreferences).toHaveBeenCalledWith(true, true)
  })

  it("renders download progress and cancellation, and keeps raw errors private", async () => {
    const client = {
      ...UNAVAILABLE_UPDATE_CLIENT,
      getDesktopUpdate: vi.fn(async () => ({ ...available, status: "downloading" as const, downloaded: 1024 })),
      cancelDesktopUpdate: vi.fn(async () => ({ ...available })),
      downloadDesktopUpdate: vi.fn(async () => { throw new Error("secret filesystem path") }),
    }
    await show(client)
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50")
    fireEvent.click(screen.getByRole("button", { name: "Cancel download" }))
    await screen.findByText("A new version is available")
    fireEvent.click(screen.getByRole("button", { name: "Download update" }))
    await screen.findByText("Unable to read update status. Check again to retry.")
    expect(screen.queryByText("secret filesystem path")).not.toBeInTheDocument()
  })

  it("uses the macOS installation handoff and release-page fallback", async () => {
    const client = {
      ...UNAVAILABLE_UPDATE_CLIENT,
      getDesktopUpdate: vi.fn(async () => ({ ...available, status: "ready" as const, install_kind: "manual" as const })),
      installDesktopUpdate: vi.fn(async () => ({ ...available, status: "ready" as const, install_kind: "manual" as const })),
      openDesktopUpdateRelease: vi.fn(async () => {}),
    }
    await show(client)
    expect(screen.queryByRole("button", { name: "Restart and update" })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "Open installer" }))
    await act(async () => {})
    expect(client.installDesktopUpdate).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole("button", { name: "Release page" }))
    expect(client.openDesktopUpdateRelease).toHaveBeenCalledTimes(1)
  })
})
