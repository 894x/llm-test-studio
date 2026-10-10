import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronLeftIcon from "lucide-react/dist/esm/icons/chevron-left.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { ComparisonChannelResult, ComparisonSummary } from "@/features/comparisons/data"
import type { FormalReportDetail, ReportDetail, ReportSnapshot } from "./data"
import { ComparisonReportBody, type ChannelReports } from "./comparison-report"
import { exportVisualReport as defaultVisualExport, type VisualReportFormat } from "./visual-report-export"
import { blobToBase64 } from "./report-export-encoding"
import { ReportExportControls } from "./report-export-controls"
import { publicDesktopOperationErrorMessage } from "@/app/desktop-client"

export function useComparisonReport({ comparison, snapshot, getDetail, saveReportExport, copyReportPNG, exportVisualReport = defaultVisualExport, active }: {
  comparison?: ComparisonSummary
  snapshot: ReportSnapshot
  getDetail: (id: string) => Promise<ReportDetail>
  saveReportExport: (filename: string, mediaType: string, data: string, locale: string) => Promise<boolean>
  copyReportPNG: (data: string) => Promise<void>
  exportVisualReport?: typeof defaultVisualExport
  active: boolean
}) {
  const { t, i18n } = useTranslation("reports")
  const [view, setView] = useState({ comparisonID: comparison?.id, hidden: [] as string[] })
  if (view.comparisonID !== comparison?.id) setView({ comparisonID: comparison?.id, hidden: [] })
  const hidden = view.comparisonID === comparison?.id ? view.hidden : []
  const [loadState, setLoaded] = useState<ChannelReports>({})
  const [retry, setRetry] = useState(0)
  const [exporting, setExporting] = useState<VisualReportFormat | "json" | "copy" | "">("")
  const [exportError, setExportError] = useState("")
  const [watermark, setWatermark] = useState("rhzs")
  const [exportDocument, setExportDocument] = useState<{
    comparison: ComparisonSummary; channels: ComparisonChannelResult[]; loaded: ChannelReports;
    reports: Map<string, ReportSnapshot["reports"][number]>; watermark: string
  } | null>(null)
  const exportReady = useRef<((element: HTMLElement) => void) | null>(null)
  const cache = useRef({ reader: getDetail, reports: new Map<string, Promise<FormalReportDetail>>() })
  const reports = useMemo(() => new Map(snapshot.reports.flatMap(report => report.run_id ? [[report.run_id, report] as const] : [])), [snapshot.reports])
  const targets = comparison?.channels.map(channel => ({ channelID: channel.channel_id, runID: channel.run_id, reportID: reports.get(channel.run_id)?.id ?? "" })) ?? []
  const targetKey = JSON.stringify(targets)
  const modelID = comparison?.model_id
  const loaded: ChannelReports = Object.fromEntries((comparison?.channels ?? []).flatMap(channel => {
    const value = loadState[channel.channel_id]
    return value && value.reportID === reports.get(channel.run_id)?.id ? [[channel.channel_id, value]] : []
  }))

  const readReport = useCallback((target: { channelID: string; runID: string; reportID: string }): Promise<FormalReportDetail> => {
    if (cache.current.reader !== getDetail) cache.current = { reader: getDetail, reports: new Map() }
    const reportCache = cache.current.reports
    let pending = reportCache.get(target.reportID)
    if (!pending) {
      pending = Promise.resolve().then(() => getDetail(target.reportID)).then(detail => {
        if (detail.source !== "run") throw new Error("Invalid comparison report source")
        return detail
      }).catch(error => { reportCache.delete(target.reportID); throw error })
      reportCache.set(target.reportID, pending)
    }
    return pending.then(detail => {
      if (detail.report.run_id !== target.runID || detail.report.channel.id !== target.channelID ||
        detail.report.model.id !== modelID) throw new Error("Mismatched comparison report")
      return detail
    }).catch(error => {
      if (reportCache.get(target.reportID) === pending) reportCache.delete(target.reportID)
      throw error
    })
  }, [getDetail, modelID])

  useEffect(() => {
    if (!active) return
    let current = true
    // Only report identities trigger reads; progress/name refreshes keep in-flight reads alive.
    const requests: typeof targets = JSON.parse(targetKey)
    void Promise.all(requests.map(async target => {
      if (!target.reportID) return
      try {
        const detail = await readReport(target)
        if (!current) return
        setLoaded(previous => ({ ...previous, [target.channelID]: { reportID: target.reportID, detail } }))
      } catch {
        if (current) setLoaded(previous => ({ ...previous, [target.channelID]: { reportID: target.reportID, error: true } }))
      }
    }))
    return () => { current = false }
  }, [active, readReport, targetKey, retry])

  const visibleChannels = (comparison?.channels ?? []).filter(channel => !hidden.includes(channel.channel_id))
  const canExport = visibleChannels.length > 0 && visibleChannels.every(channel => reports.has(channel.run_id) && !loaded[channel.channel_id]?.error)
  const attachExportDocument = useCallback((element: HTMLElement | null) => {
    if (element && exportReady.current) {
      exportReady.current(element)
      exportReady.current = null
    }
  }, [])
  const handleExport = async (format: VisualReportFormat | "json" | "copy") => {
    if (!comparison || !canExport || exporting) return
    setExporting(format)
    setExportError("")
    try {
      const exportReports: ChannelReports = Object.fromEntries(await Promise.all(visibleChannels.map(async channel => {
        const reportID = reports.get(channel.run_id)!.id
        try {
          const detail = await readReport({ channelID: channel.channel_id, runID: channel.run_id, reportID })
          return [channel.channel_id, { reportID, detail }]
        } catch (error) {
          setLoaded(previous => ({ ...previous, [channel.channel_id]: { reportID, error: true } }))
          throw error
        }
      })))
      setLoaded(previous => ({ ...previous, ...exportReports }))
      let blob: Blob
      let filename: string
      let mediaType: string
      if (format === "json") {
        blob = new Blob([JSON.stringify({ comparison, visible_channel_ids: visibleChannels.map(channel => channel.channel_id),
          reports: visibleChannels.map(channel => exportReports[channel.channel_id].detail) }, null, 2)], { type: "application/json" })
        filename = `llm-test-studio-comparison-${comparison.id}.json`
        mediaType = "application/json"
      } else {
        const element = await new Promise<HTMLElement>(resolve => {
          exportReady.current = resolve
          setExportDocument({ comparison, channels: visibleChannels, loaded: exportReports, reports, watermark })
        })
        const result = await exportVisualReport(element, format === "copy" ? "png" : format, comparison.id)
        blob = result.blob
        filename = result.filename
        mediaType = result.mediaType
      }
      const data = await blobToBase64(blob)
      if (format === "copy") {
        try { await copyReportPNG(data) } catch (nativeError) {
          const ClipboardItemType = window.ClipboardItem
          if (!navigator.clipboard?.write || !ClipboardItemType) throw nativeError
          await navigator.clipboard.write([new ClipboardItemType({ [mediaType]: blob })])
        }
      }
      else await saveReportExport(filename, mediaType, data, i18n.resolvedLanguage ?? i18n.language)
    } catch (error) {
      setExportError(publicDesktopOperationErrorMessage(error,
        format === "copy" ? t("export.copyOperation", { name: t("comparison.title") }) :
          t("export.operation", { format: format.toUpperCase(), name: t("comparison.title") }),
        t(format === "copy" ? "export.copyError" : "export.error")))
    } finally {
      exportReady.current = null
      setExportDocument(null)
      setExporting("")
    }
  }

  const content = comparison ? <>
    <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 py-2" aria-label={t("comparison.channelControls")}>
      {comparison.channels.map((channel, index) => {
        const collapsed = hidden.includes(channel.channel_id)
        return <Button key={channel.channel_id} variant={collapsed ? "outline" : "secondary"} size="sm"
          className="h-auto min-h-8 max-w-full whitespace-normal [overflow-wrap:anywhere]"
          disabled={!collapsed && visibleChannels.length === 1} aria-expanded={!collapsed}
          aria-controls="comparison-report-results" aria-label={t(collapsed ? "comparison.expand" : "comparison.collapse", { channel: channel.channel_name })}
          onClick={() => setView({ comparisonID: comparison.id, hidden: collapsed ? hidden.filter(id => id !== channel.channel_id) : [...hidden, channel.channel_id] })}>
          {index === 0 ? <ChevronLeftIcon /> : <ChevronRightIcon />}{channel.channel_name}
          <span className="text-muted-foreground">{t(collapsed ? "comparison.expandLabel" : "comparison.collapseLabel")}</span>
        </Button>
      })}
    </div>
    <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
      <div id="comparison-report-results" className="p-4">
        <ComparisonReportBody key={`${comparison.id}:${visibleChannels.map(channel => channel.channel_id).join(",")}`} comparison={comparison} channels={visibleChannels}
          loaded={loaded} reports={reports} onRetry={() => setRetry(value => value + 1)} />
      </div>
    </ScrollArea>
  </> : null
  const exportSurface = exportDocument ? <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0 w-[1200px]">
      <article ref={attachExportDocument} data-report-export-document className="relative w-[1200px] bg-background p-4 text-foreground">
        <ComparisonReportBody comparison={exportDocument.comparison} channels={exportDocument.channels} loaded={exportDocument.loaded} reports={exportDocument.reports} paginate={false} />
        <div className="pointer-events-none absolute inset-0 grid grid-cols-2 content-around overflow-hidden" data-report-watermark>
          {Array.from({ length: 8 }, (_, index) => <span key={index} className="-rotate-12 text-center text-4xl font-semibold text-muted-foreground/15">{exportDocument.watermark.trim() || "rhzs"}</span>)}
        </div>
      </article>
    </div> : null
  return { content, exportSurface, controls: <ReportExportControls watermark={watermark} onWatermarkChange={setWatermark}
    exporting={exporting} error={exportError} disabled={!canExport} onExport={handleExport} onCopyPNG={() => handleExport("copy")} /> }
}
