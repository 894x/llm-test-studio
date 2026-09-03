export type VisualReportFormat = "html" | "png" | "pdf"

export interface VisualReportExport {
  filename: string
  mediaType: string
  blob: Blob
}

export async function exportVisualReport(
  element: HTMLElement,
  format: VisualReportFormat,
  reportID: string,
): Promise<VisualReportExport> {
  const filename = `llm-studio-report-${reportID}.${format}`
  if (format === "html") {
    return {
      filename,
      mediaType: "text/html; charset=utf-8",
      blob: new Blob([standaloneReportHTML(element, reportID)], { type: "text/html; charset=utf-8" }),
    }
  }

  const { toCanvas } = await import("html-to-image")
  const canvas = await toCanvas(element, {
    backgroundColor: getComputedStyle(element).backgroundColor,
    cacheBust: true,
    pixelRatio: 2,
    width: element.scrollWidth,
    height: element.scrollHeight,
  })
  if (format === "png") {
    return { filename, mediaType: "image/png", blob: await canvasBlob(canvas) }
  }

  const width = canvas.width / 2
  const height = canvas.height / 2
  const { jsPDF } = await import("jspdf")
  const pdf = new jsPDF({
    orientation: width >= height ? "landscape" : "portrait",
    unit: "px",
    format: [width, height],
    compress: true,
    hotfixes: ["px_scaling"],
  })
  pdf.addImage(canvas, "PNG", 0, 0, width, height)
  return { filename, mediaType: "application/pdf", blob: pdf.output("blob") }
}

export function standaloneReportHTML(element: HTMLElement, reportID: string): string {
  const root = document.documentElement
  const language = root.lang || "zh-CN"
  const direction = root.dir || "ltr"
  const theme = root.dataset.theme ? ` data-theme="${escapeAttribute(root.dataset.theme)}"` : ""
  const styles = Array.from(document.styleSheets).flatMap(readStyleSheet).join("\n")
  return `<!doctype html>
<html lang="${escapeAttribute(language)}" dir="${escapeAttribute(direction)}" class="${escapeAttribute(root.className)}"${theme}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>LLM Studio Report ${escapeHTML(reportID)}</title>
<style>${styles}</style>
</head>
<body>${element.outerHTML}</body>
</html>`
}

function readStyleSheet(sheet: CSSStyleSheet): string[] {
  try {
    return Array.from(sheet.cssRules, (rule) => rule.cssText)
  } catch {
    return []
  }
}

function canvasBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob)
      else reject(new Error("PNG encoding failed"))
    }, "image/png")
  })
}

function escapeAttribute(value: string): string {
  return escapeHTML(value).replaceAll("`", "&#96;")
}

function escapeHTML(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;")
}
