import { afterEach, describe, expect, it, vi } from "vitest"
import { copyReportPNGToClipboard } from "./report-clipboard"

afterEach(() => vi.unstubAllGlobals())

function browserClipboard() {
  class ClipboardItemStub {
    readonly data: Record<string, Blob>
    constructor(data: Record<string, Blob>) { this.data = data }
  }
  const write = vi.fn(async (_items: ClipboardItemStub[]) => undefined)
  vi.stubGlobal("ClipboardItem", ClipboardItemStub)
  vi.stubGlobal("navigator", { clipboard: { write } })
  return write
}

describe("report PNG clipboard", () => {
  it("copies the encoded PNG natively without also writing through the browser", async () => {
    const write = browserClipboard()
    const nativeCopy = vi.fn(async () => undefined)

    await copyReportPNGToClipboard(new Blob(["png"], { type: "image/png" }), nativeCopy)

    expect(nativeCopy).toHaveBeenCalledExactlyOnceWith("cG5n")
    expect(write).not.toHaveBeenCalled()
  })

  it("writes the same PNG through the browser when native copying fails", async () => {
    const write = browserClipboard()
    const blob = new Blob(["png"], { type: "image/png" })
    const nativeCopy = vi.fn(async () => { throw new Error("native clipboard unavailable") })

    await copyReportPNGToClipboard(blob, nativeCopy)

    expect(nativeCopy).toHaveBeenCalledExactlyOnceWith("cG5n")
    expect(write).toHaveBeenCalledOnce()
    expect(write.mock.calls[0][0][0].data).toEqual({ "image/png": blob })
  })

  it.each(["clipboard", "ClipboardItem"])("preserves the native error when %s is unavailable", async unavailable => {
    const write = browserClipboard()
    if (unavailable === "clipboard") vi.stubGlobal("navigator", {})
    else vi.stubGlobal("ClipboardItem", undefined)
    const error = new Error("native clipboard unavailable")

    await expect(copyReportPNGToClipboard(new Blob(["png"]), async () => { throw error })).rejects.toBe(error)

    expect(write).not.toHaveBeenCalled()
  })

  it("propagates a browser clipboard failure instead of reporting success", async () => {
    const write = browserClipboard()
    const error = new Error("browser clipboard denied")
    write.mockRejectedValueOnce(error)

    await expect(copyReportPNGToClipboard(new Blob(["png"]), async () => {
      throw new Error("native clipboard unavailable")
    })).rejects.toBe(error)
  })
})
