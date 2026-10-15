import { afterEach, describe, expect, it, vi } from "vitest"
import { copyReportPNGToClipboard, copyReportTextToClipboard } from "./report-clipboard"

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

describe("report text clipboard", () => {
  it("uses the Wails clipboard when available", async () => {
    const ClipboardSetText = vi.fn(async () => true)
    const writeText = vi.fn()
    vi.stubGlobal("runtime", { ClipboardSetText })
    vi.stubGlobal("navigator", { clipboard: { writeText } })

    await copyReportTextToClipboard("完整请求体\n{\"model\":\"test\"}")

    expect(ClipboardSetText).toHaveBeenCalledExactlyOnceWith("完整请求体\n{\"model\":\"test\"}")
    expect(writeText).not.toHaveBeenCalled()
  })

  it("uses the browser clipboard in browser preview", async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal("runtime", undefined)
    vi.stubGlobal("navigator", { clipboard: { writeText } })

    await copyReportTextToClipboard("response")

    expect(writeText).toHaveBeenCalledExactlyOnceWith("response")
  })

  it.each([false, "rejected"])("reports a native write failure (%s)", async result => {
    const ClipboardSetText = vi.fn(async () => {
      if (result === "rejected") throw new Error("clipboard_denied")
      return false
    })
    vi.stubGlobal("runtime", { ClipboardSetText })

    await expect(copyReportTextToClipboard("response")).rejects.toThrow()
  })

  it("reports an unavailable or denied browser clipboard", async () => {
    vi.stubGlobal("runtime", undefined)
    vi.stubGlobal("navigator", {})
    await expect(copyReportTextToClipboard("response")).rejects.toThrow("clipboard_unavailable")

    vi.stubGlobal("navigator", { clipboard: { writeText: vi.fn(async () => { throw new Error("clipboard_denied") }) } })
    await expect(copyReportTextToClipboard("response")).rejects.toThrow("clipboard_denied")
  })
})

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
