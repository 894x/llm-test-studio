import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { useRef, useState } from "react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { I18nextProvider } from "react-i18next"
import { createAppI18n } from "@/i18n/i18n"
import { FILE_INPUT, FILE_SELECTION } from "@/test/local-file-fixture"
import type { LocalFileSelection } from "@/app/desktop-client"
import { LocalFileInput, type LocalFileActions } from "./local-file-input"

function setup(actions: LocalFileActions = {
  createLocalFileSelection: vi.fn(async () => FILE_SELECTION),
  releaseLocalFileSelection: vi.fn(async () => undefined),
}, count = 1) {
  function Harness() {
    const busy = useRef(false)
    const [importing, setImporting] = useState(false)
    const coordinator = {
      begin: () => { if (busy.current) return false; busy.current = true; setImporting(true); return true },
      end: () => { busy.current = false; setImporting(false) },
    }
    return Array.from({ length: count }, (_, index) => <div key={index}>
      <label htmlFor={`image-${index}`}>{`Image ${index}`}</label>
      <LocalFileInput id={`image-${index}`} definition={FILE_INPUT.file} actions={actions} importing={importing} coordinator={coordinator} />
    </div>)
  }
  const result = render(<I18nextProvider i18n={createAppI18n("zh-CN")}><Harness /></I18nextProvider>)
  return { ...result, actions, picker: screen.getByLabelText("Image 0") as HTMLInputElement }
}

const image = (type = "image/png") => new File([new Uint8Array([1, 2, 3])], "private-image-name.png", { type })
const deferred = () => {
  let resolve!: (value: LocalFileSelection) => void
  const promise = new Promise<LocalFileSelection>(done => { resolve = done })
  return { promise, resolve }
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe("explicit local file input", () => {
  it("imports one image, shows bounded metadata, and releases an explicit removal", async () => {
    const { picker, actions } = setup()
    expect(picker).toHaveAttribute("type", "file")
    expect(picker).toHaveAttribute("accept", "image/png,image/jpeg")
    expect(picker).not.toHaveAttribute("multiple")
    expect(picker).toHaveAccessibleDescription(expect.stringContaining("1048576"))
    expect(screen.getByRole("status")).toHaveTextContent("尚未选择图片")
    await userEvent.setup().tab()
    expect(picker).toHaveFocus()
    await userEvent.setup().upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    expect(actions.createLocalFileSelection).toHaveBeenCalledExactlyOnceWith({ data_base64: "AQID", media_type: "image/png" })
    expect(document.body).not.toHaveTextContent(FILE_SELECTION.selection_id)
    expect(document.body).not.toHaveTextContent(FILE_SELECTION.sha256)
    expect(document.body).not.toHaveTextContent("private-image-name")
    expect(picker.value).toBe("")
    await userEvent.setup().click(screen.getByRole("button", { name: "移除图片" }))
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("尚未选择图片"))
    expect(actions.releaseLocalFileSelection).toHaveBeenCalledExactlyOnceWith(FILE_SELECTION.selection_id)
    expect(picker).toHaveFocus()
  })

  it("checks size and MIME before reading and preserves the previous valid selection on invalid replacement", async () => {
    const { picker, actions } = setup()
    const user = userEvent.setup({ applyAccept: false })
    await user.upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    const read = vi.spyOn(FileReader.prototype, "readAsArrayBuffer")
    for (const invalid of [new File([], "empty.png", { type: "image/png" }),
      new File([new Uint8Array(1048577)], "large.png", { type: "image/png" }), image("image/gif")]) {
      await user.upload(picker, invalid)
      expect(screen.getByText("已选择：image/png · 3 字节")).toBeInTheDocument()
      expect(picker).toHaveAttribute("aria-invalid", "true")
    }
    expect(read).not.toHaveBeenCalled()
    expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(1)
    expect(actions.releaseLocalFileSelection).not.toHaveBeenCalled()
  })

  it("releases a replaced selection and a selection still held at unmount", async () => {
    const next = { ...FILE_SELECTION, selection_id: "a1000000-0000-4000-8000-000000000002", media_type: "image/jpeg" as const }
    const actions = { createLocalFileSelection: vi.fn().mockResolvedValueOnce(FILE_SELECTION).mockResolvedValueOnce(next),
      releaseLocalFileSelection: vi.fn(async () => undefined) }
    const { picker, unmount } = setup(actions)
    const user = userEvent.setup()
    await user.upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    fireEvent(picker, new Event("cancel", { bubbles: true }))
    expect(screen.getByText("已选择：image/png · 3 字节")).toBeInTheDocument()
    await user.upload(picker, image("image/jpeg"))
    await screen.findByText("已选择：image/jpeg · 3 字节")
    expect(actions.releaseLocalFileSelection).toHaveBeenCalledWith(FILE_SELECTION.selection_id)
    unmount()
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledWith(next.selection_id))
  })

  it("retains a failed replacement release and blocks new imports until explicit cleanup succeeds", async () => {
    const next = { ...FILE_SELECTION, selection_id: "a1000000-0000-4000-8000-000000000002", media_type: "image/jpeg" as const }
    const allocated = new Set<string>()
    const actions = {
      createLocalFileSelection: vi.fn().mockImplementationOnce(async () => { allocated.add(FILE_SELECTION.selection_id); return FILE_SELECTION })
        .mockImplementationOnce(async () => { allocated.add(next.selection_id); return next }),
      releaseLocalFileSelection: vi.fn<(id: string) => Promise<void>>()
        .mockRejectedValueOnce(new Error("private release failure"))
        .mockRejectedValueOnce(new Error("private release failure"))
        .mockImplementation(async id => { allocated.delete(id) }),
    }
    const { picker } = setup(actions)
    const user = userEvent.setup()
    await user.upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    await user.upload(picker, image("image/jpeg"))
    await screen.findByText("已选择：image/jpeg · 3 字节")
    await screen.findByText(/本地图片选择不可用/)
    expect(allocated.size).toBe(2)
    expect(document.body).not.toHaveTextContent("private release failure")
    await user.upload(picker, image())
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledTimes(2))
    expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(2)
    expect(screen.getByText("已选择：image/jpeg · 3 字节")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "移除图片" }))
    await waitFor(() => expect(allocated.size).toBe(0))
    expect(actions.releaseLocalFileSelection.mock.calls.map(([id]) => id)).toEqual([
      FILE_SELECTION.selection_id, FILE_SELECTION.selection_id, FILE_SELECTION.selection_id, next.selection_id,
    ])
    expect(screen.getByRole("status")).toHaveTextContent("尚未选择图片")
  })

  it("retries an in-flight removal rejected after unmount without losing its handle", async () => {
    let rejectRelease!: (reason: Error) => void
    const pendingRelease = new Promise<void>((_resolve, reject) => { rejectRelease = reject })
    const allocated = new Set([FILE_SELECTION.selection_id])
    const actions = {
      createLocalFileSelection: vi.fn(async () => FILE_SELECTION),
      releaseLocalFileSelection: vi.fn<(id: string) => Promise<void>>().mockImplementationOnce(() => pendingRelease)
        .mockImplementation(async id => { allocated.delete(id) }),
    }
    const { picker, unmount } = setup(actions)
    const user = userEvent.setup()
    await user.upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    await user.click(screen.getByRole("button", { name: "移除图片" }))
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledTimes(1))
    unmount()
    expect(actions.releaseLocalFileSelection).toHaveBeenCalledTimes(1)
    await act(async () => { rejectRelease(new Error("private removal failure")); await pendingRelease.catch(() => undefined) })
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledTimes(2))
    expect(actions.releaseLocalFileSelection.mock.calls.map(([id]) => id)).toEqual([FILE_SELECTION.selection_id, FILE_SELECTION.selection_id])
    expect(allocated.size).toBe(0)
  })

  it.each(["late", "mismatch"])("retains a failed %s-result release for the next explicit interaction", async mode => {
    const pending = deferred()
    const next = { ...FILE_SELECTION, selection_id: "a1000000-0000-4000-8000-000000000002" }
    const actions = {
      createLocalFileSelection: vi.fn().mockImplementationOnce(() => mode === "late" ? pending.promise : Promise.resolve({ ...FILE_SELECTION, size: 4 }))
        .mockResolvedValue(next),
      releaseLocalFileSelection: vi.fn<(id: string) => Promise<void>>().mockRejectedValueOnce(new Error("private cleanup failure"))
        .mockResolvedValue(undefined),
    }
    const first = setup(actions)
    const user = userEvent.setup()
    await user.upload(first.picker, image())
    await waitFor(() => expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(1))
    if (mode === "late") {
      first.unmount()
      await act(async () => { pending.resolve(FILE_SELECTION); await pending.promise })
    } else await screen.findByText(/本地图片选择不可用/)
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledTimes(1))
    const picker = mode === "late" ? setup(actions).picker : first.picker
    await user.upload(picker, image())
    await screen.findByText("已选择：image/png · 3 字节")
    expect(actions.releaseLocalFileSelection.mock.calls.map(([id]) => id)).toEqual([FILE_SELECTION.selection_id, FILE_SELECTION.selection_id])
    expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(2)
    expect(document.body).not.toHaveTextContent("private cleanup failure")
  })

  it.each(["cancel", "unmount"])("releases a late Core result after %s and permits only one import", async mode => {
    const pending = deferred()
    const actions = { createLocalFileSelection: vi.fn(() => pending.promise), releaseLocalFileSelection: vi.fn(async () => undefined) }
    const { picker, unmount } = setup(actions, 2)
    await userEvent.setup().upload(picker, image())
    await waitFor(() => expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText("Image 1")).toBeDisabled()
    if (mode === "cancel") await userEvent.setup().click(screen.getByRole("button", { name: "取消图片导入" }))
    else unmount()
    await act(async () => { pending.resolve(FILE_SELECTION); await pending.promise })
    await waitFor(() => expect(actions.releaseLocalFileSelection).toHaveBeenCalledExactlyOnceWith(FILE_SELECTION.selection_id))
    expect(screen.queryByText("已选择：image/png · 3 字节")).not.toBeInTheDocument()
    expect(actions.createLocalFileSelection).toHaveBeenCalledTimes(1)
  })

  it.each(["cancel", "picker-cancel", "unmount", "replace"])("aborts an active FileReader on %s", async mode => {
    class ControlledReader {
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      onabort: (() => void) | null = null
      result: ArrayBuffer | null = null
      readAsArrayBuffer = vi.fn()
      abort = vi.fn(() => this.onabort?.())
      constructor() { readers.push(this) }
    }
    const readers: ControlledReader[] = []
    vi.stubGlobal("FileReader", ControlledReader)
    const { picker, actions, unmount } = setup()
    await userEvent.setup().upload(picker, image())
    expect(readers).toHaveLength(1)
    if (mode === "cancel") await userEvent.setup().click(screen.getByRole("button", { name: "取消图片导入" }))
    else if (mode === "picker-cancel") fireEvent(picker, new Event("cancel", { bubbles: true }))
    else if (mode === "unmount") unmount()
    else fireEvent.change(picker, { target: { files: [image("image/jpeg")] } })
    await waitFor(() => expect(readers[0].abort).toHaveBeenCalled())
    expect(actions.createLocalFileSelection).not.toHaveBeenCalled()
    if (mode === "replace") {
      await waitFor(() => expect(readers).toHaveLength(2))
      unmount()
    }
  })
})
