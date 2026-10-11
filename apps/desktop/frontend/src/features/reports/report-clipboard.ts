import { blobToBase64 } from "./report-export-encoding"

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
