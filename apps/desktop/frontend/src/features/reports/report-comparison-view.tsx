import { Fragment, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ExecutionDetails, VerificationBadge } from "./entry-case-table"
import { QuickPerformanceRequestAnalysis } from "@/features/quick-test/quick-performance-request-analysis"
import { formatReportTimestamp } from "./report-timestamp"
import { blobToBase64 } from "./report-export-encoding"
import { copyReportPNGToClipboard } from "./report-clipboard"
import { ReportExportControls } from "./report-export-controls"
import { comparisonReportID, type CompareReports, type ReportComparison, type ReportComparisonCell } from "./report-comparison-data"
import type { ReportDetail, ReportExportFormat } from "./data"
import { exportVisualReport as createVisualReportExport } from "./visual-report-export"

export function ReportComparisonView({ reportIDs, compareReports, saveReportExport, copyReportPNG, exportVisualReport = createVisualReportExport }: {
  reportIDs: string[]
  compareReports: CompareReports
  saveReportExport: (filename: string, mediaType: string, data: string, locale: string) => Promise<boolean>
  copyReportPNG: (data: string) => Promise<void>
  exportVisualReport?: typeof createVisualReportExport
}) {
  const { t, i18n } = useTranslation("reports")
  const [baseline, setBaseline] = useState(reportIDs[0])
  const [onlyDifferences, setOnlyDifferences] = useState(false)
  const [retry, setRetry] = useState(0)
  const [state, setState] = useState<{ key: string; data?: ReportComparison; error?: boolean }>({ key: "" })
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [watermark, setWatermark] = useState("rhzs")
  const [exportError, setExportError] = useState("")
  const [exportData, setExportData] = useState<ReportComparison | null>(null)
  const exportReady = useRef<((element: HTMLElement) => void) | null>(null)
  const baselineID = reportIDs.includes(baseline) ? baseline : reportIDs[0]
  const idsKey = JSON.stringify(reportIDs)
  const key = JSON.stringify([idsKey, baselineID, retry])
  const data = state.key === key ? state.data : undefined
  useEffect(() => {
    let active = true
    const ids: string[] = JSON.parse(idsKey)
    void Promise.resolve().then(() => compareReports(ids, baselineID)).then(result => {
      if (result.baseline_id !== baselineID || JSON.stringify(result.reports.map(comparisonReportID)) !== idsKey) throw new Error("Invalid report comparison identity")
      if (active) setState({ key, data: result })
    }).catch(() => { if (active) setState({ key, error: true }) })
    return () => { active = false }
  }, [compareReports, idsKey, baselineID, retry, key])

  const handleExport = async (format: ReportExportFormat | "copy") => {
    if (!data || exporting) return
    setExporting(format); setExportError("")
    try {
      let blob: Blob
      let filename = `llm-test-studio-report-comparison-${baselineID}.json`
      let mediaType = "application/json"
      if (format === "json") {
        blob = new Blob([JSON.stringify(data, null, 2)], { type: mediaType })
      } else {
        const element = await new Promise<HTMLElement>(resolve => { exportReady.current = resolve; setExportData(data) })
        const result = await exportVisualReport(element, format === "copy" ? "png" : format, baselineID)
        blob = result.blob; filename = result.filename; mediaType = result.mediaType
      }
      if (format === "copy") await copyReportPNGToClipboard(blob, copyReportPNG)
      else await saveReportExport(filename, mediaType, await blobToBase64(blob), i18n.resolvedLanguage ?? i18n.language)
    } catch {
      setExportError(t(format === "copy" ? "export.copyError" : "export.error"))
    } finally {
      exportReady.current = null; setExportData(null); setExporting("")
    }
  }
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="flex shrink-0 flex-wrap items-center gap-3 px-4 py-2">
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <span id="report-comparison-baseline">{t("compare.baseline")}</span>
        <SearchableSelect aria-labelledby="report-comparison-baseline" className="w-72 max-w-full" value={baselineID}
          disabled={!data || Boolean(exporting)} onValueChange={setBaseline}
          options={(data?.reports ?? []).map(detail => ({ value: comparisonReportID(detail), label: reportLabel(detail, i18n.resolvedLanguage ?? i18n.language) }))} />
      </div>
      <label className="flex items-center gap-2 text-xs"><Checkbox checked={onlyDifferences} disabled={Boolean(exporting)} onCheckedChange={checked => setOnlyDifferences(checked === true)} />{t("compare.onlyDifferences")}</label>
    </div>
    <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
      {!data ? state.key === key && state.error ? <div role="alert" className="p-4 text-xs text-destructive">{t("compare.error")}<Button className="ml-2" variant="outline" size="sm" onClick={() => setRetry(value => value + 1)}>{t("comparison.retry")}</Button></div> :
        <div role="status" className="flex items-center gap-2 p-4 text-xs"><Spinner />{t("compare.loading")}</div> :
        <div className="min-w-0 space-y-3 p-4"><p className="text-xs text-muted-foreground">{t("compare.hint")}</p>
          <ReportComparisonBody key={`${idsKey}:${baselineID}`} data={data} onlyDifferences={onlyDifferences} />
          <details className="max-w-sm text-xs"><summary className="cursor-pointer py-2">{t("inspector.exportAria")}</summary>
            <ReportExportControls watermark={watermark} onWatermarkChange={setWatermark} exporting={exporting} error={exportError}
              onExport={handleExport} onCopyPNG={() => handleExport("copy")} />
          </details>
        </div>}
    </ScrollArea>
    {exportData ? <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0" style={{ width: Math.max(1200, 252 + exportData.reports.length * 240) }}>
      <article data-report-export-document ref={element => { if (element && exportReady.current) { exportReady.current(element); exportReady.current = null } }} className="relative bg-background p-4 text-foreground">
        <ReportComparisonBody data={exportData} onlyDifferences={onlyDifferences} paginate={false} />
        <div data-report-watermark className="pointer-events-none absolute inset-0 grid grid-cols-2 content-around overflow-hidden">
          {Array.from({ length: 8 }, (_, index) => <span key={index} className="-rotate-12 text-center text-4xl font-semibold text-muted-foreground/15">{watermark.trim() || "rhzs"}</span>)}
        </div>
      </article>
    </div> : null}
  </div>
}

function ReportComparisonBody({ data, onlyDifferences, paginate = true }: { data: ReportComparison; onlyDifferences: boolean; paginate?: boolean }) {
  const { t, i18n } = useTranslation("reports")
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState("")
  const rows = onlyDifferences ? data.rows.filter(row => row.different) : data.rows
  const lastPage = Math.max(0, Math.ceil(rows.length / 40) - 1)
  const currentPage = Math.min(page, lastPage)
  const visible = paginate ? rows.slice(currentPage * 40, (currentPage + 1) * 40) : rows
  return <section aria-label={t("compare.title")} className="min-w-0 space-y-3">
    <Table aria-label={t("compare.table")} className="table-fixed text-xs" style={{ minWidth: 220 + data.reports.length * 240 }}>
      <TableHeader><TableRow>
        <TableHead className="w-[220px]">{t("protocolDesign.caseName")}</TableHead>
        {data.reports.map(detail => {
          const info = reportInfo(detail)
          const id = comparisonReportID(detail)
          return <TableHead key={id} className="whitespace-normal py-3 align-top [overflow-wrap:anywhere]">
            <div className="font-medium">{info.channel}</div><div className="mt-1 font-normal text-muted-foreground">{info.model}</div>
            <time className="mt-1 block font-normal tabular-nums" dateTime={info.generated}>{formatReportTimestamp(info.generated, i18n.resolvedLanguage ?? i18n.language)}</time>
            {id === data.baseline_id ? <span className="mt-1 block">{t("compare.baseline")}</span> : null}
          </TableHead>
        })}
      </TableRow></TableHeader>
      <TableBody>{visible.map(row => <Fragment key={row.key}>
        <TableRow data-state={expanded === row.key ? "selected" : undefined}>
          <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]"><div className="font-medium">{row.name || t("system.quickPerformance")}</div><div className="mt-1 text-muted-foreground">{row.entry_name}</div></TableCell>
          {row.cells.map(cell => <TableCell key={cell.report_id} className="whitespace-normal align-top [overflow-wrap:anywhere]">
            <div className="flex flex-wrap items-center gap-2">
              {cell.status === "not_run" ? <span>{t("comparison.noResult")}</span> : <VerificationBadge status={cell.status as "passed" | "failed" | "not_applicable" | "indeterminate"} />}
              {cell.change !== "same" && cell.change !== "baseline" ? <span className={cell.change === "regressed" ? "text-destructive" : cell.change === "recovered" ? "text-success-strong" : "text-muted-foreground"}>{t(`compare.change.${cell.change}`)}</span> : null}
            </div>
            {cell.metrics.map(metric => <div key={metric.key} className="mt-2 text-[11px] tabular-nums"><span className="text-muted-foreground">{t(`performanceOverview.metrics.${metric.key}`, { defaultValue: metric.key })} </span>
              {metric.value.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: 3 })} {metric.unit}
              {cell.report_id !== data.baseline_id && metric.delta !== null ? <span className="ml-2 text-muted-foreground">Δ {metric.delta > 0 ? "+" : ""}{metric.delta.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: 3 })}</span> : null}
            </div>)}
            {cell.change !== "missing" ? <Button variant="ghost" size="xs" className="mt-2" aria-expanded={expanded === row.key} onClick={() => setExpanded(expanded === row.key ? "" : row.key)}>{t("compare.evidence")}</Button> : null}
          </TableCell>)}
        </TableRow>
        {expanded === row.key ? <TableRow><TableCell colSpan={data.reports.length + 1} className="whitespace-normal py-3">
          <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${data.reports.length}, minmax(220px, 1fr))` }}>
            {row.cells.map((cell, index) => <div key={cell.report_id} className="min-w-0"><h3 className="mb-2 font-medium">{reportLabel(data.reports[index], i18n.resolvedLanguage ?? i18n.language)}</h3><ComparisonEvidence detail={data.reports[index]} cell={cell} /></div>)}
          </div>
        </TableCell></TableRow> : null}
      </Fragment>)}</TableBody>
    </Table>
    {!rows.length ? <p className="py-4 text-xs text-muted-foreground">{t(onlyDifferences ? "compare.noDifferences" : "hierarchy.noRecordedCases")}</p> : null}
    {paginate && lastPage > 0 ? <div className="flex items-center justify-end gap-2 text-xs"><span>{currentPage + 1} / {lastPage + 1}</span>
      <Button size="xs" variant="outline" disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>{t("protocolDesign.previous")}</Button>
      <Button size="xs" variant="outline" disabled={currentPage === lastPage} onClick={() => setPage(currentPage + 1)}>{t("protocolDesign.next")}</Button>
    </div> : null}
  </section>
}

function ComparisonEvidence({ detail, cell }: { detail: ReportDetail; cell: ReportComparisonCell }) {
  if (cell.change === "missing") return null
  if (detail.source === "quick_performance") return <QuickPerformanceRequestAnalysis report={detail.performance} />
  const entry = detail.entries.find(entry => entry.entry_id === cell.entry_id)
  if (entry?.performance) return <QuickPerformanceRequestAnalysis report={entry.performance} />
  return <ExecutionDetails results={entry?.cases.find(item => item.case_id === cell.case_id)?.request_results ?? []} />
}

function reportInfo(detail: ReportDetail) {
  return detail.source === "run" ? { channel: detail.report.channel.name, model: detail.report.model.name, generated: detail.report.generated_at } :
    { channel: detail.performance.base_url, model: detail.performance.model_id, generated: detail.performance.generated_at! }
}
function reportLabel(detail: ReportDetail, locale: string) { const info = reportInfo(detail); return `${info.channel} · ${info.model} · ${formatReportTimestamp(info.generated, locale)}` }
