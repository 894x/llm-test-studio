import { blobToBase64 } from "./report-export-encoding"

export async function copyReportTextToClipboard(text: string): Promise<void> {
  const runtime = (window as typeof window & {
    runtime?: { ClipboardSetText?: (text: string) => Promise<boolean> }
  }).runtime
  if (typeof runtime?.ClipboardSetText === "function") {
    if (!await runtime.ClipboardSetText(text)) throw new Error("clipboard_write_failed")
    return
  }
  if (!navigator.clipboard?.writeText) throw new Error("clipboard_unavailable")
  await navigator.clipboard.writeText(text)
}

export async function copyReportPNGToClipboard(
  blob: Blob,
  copyNativePNG: (dataBase64: string) => Promise<void>,
): Promise<void> {
  const data = await blobToBase64(blob)
  try {
    await copyNativePNG(data)
  } catch (nativeError) {
    const ClipboardItemType = window.ClipboardItem
    if (!navigator.clipboard?.write || !ClipboardItemType) throw nativeError
    await navigator.clipboard.write([new ClipboardItemType({ "image/png": blob })])
  }
}
