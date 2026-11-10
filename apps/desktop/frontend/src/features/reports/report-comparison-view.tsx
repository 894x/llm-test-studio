import { Fragment, useEffect, useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronLeftIcon from "lucide-react/dist/esm/icons/chevron-left.mjs"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import DownloadIcon from "lucide-react/dist/esm/icons/download.mjs"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Checkbox } from "@/components/ui/checkbox"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { TooltipProvider } from "@/components/ui/tooltip"
import { ExecutionDetails, VerificationBadge } from "./entry-case-table"
import { QuickPerformanceRequestAnalysis } from "@/features/quick-test/quick-performance-request-analysis"
import { blobToBase64 } from "./report-export-encoding"
import { copyReportPNGToClipboard } from "./report-clipboard"
import { ReportExportControls } from "./report-export-controls"
import { CaseSeverityBadge } from "./case-severity-badge"
import { ComparisonChannelHeader, ComparisonTableColumns } from "./comparison-channel-header"
import { comparisonReportID, type CompareReports, type ReportComparison, type ReportComparisonCell, type ReportComparisonRow } from "./report-comparison-data"
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
  const [hiddenReports, setHiddenReports] = useState<string[]>([])
  const [expandedRow, setExpandedRow] = useState({ comparison: "", row: "" })
  const [state, setState] = useState<{ key: string; data?: ReportComparison; error?: boolean }>({ key: "" })
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [watermark, setWatermark] = useState("rhzs")
  const [exportError, setExportError] = useState("")
  const [exportData, setExportData] = useState<{ comparison: ReportComparison; width: number } | null>(null)
  const workspace = useRef<HTMLDivElement>(null)
  const resultsID = useId()
  const exportReady = useRef<((element: HTMLElement) => void) | null>(null)
  const baselineID = reportIDs.includes(baseline) ? baseline : reportIDs[0]
  const idsKey = JSON.stringify(reportIDs)
  const comparisonKey = `${idsKey}:${baselineID}`
  const expanded = expandedRow.comparison === comparisonKey ? expandedRow.row : ""
  const key = JSON.stringify([idsKey, baselineID, retry])
  const data = state.key === key ? state.data : undefined
  const visibleReports = (data?.reports ?? []).filter(detail => !hiddenReports.includes(comparisonReportID(detail)))
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
        const element = await new Promise<HTMLElement>(resolve => {
          exportReady.current = resolve
          setExportData({ comparison: data, width: workspace.current?.clientWidth || 1200 })
        })
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
  return <div ref={workspace} className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="flex shrink-0 flex-wrap items-center gap-3 px-4 py-2">
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <span id="report-comparison-baseline">{t("compare.baseline")}</span>
        <SearchableSelect aria-labelledby="report-comparison-baseline" className="w-72 max-w-full" value={baselineID}
          disabled={!data || Boolean(exporting)} onValueChange={setBaseline}
          options={(data?.reports ?? []).map(detail => ({ value: comparisonReportID(detail), label: reportLabel(detail) }))} />
      </div>
      <label className="flex items-center gap-2 text-xs"><Checkbox checked={onlyDifferences} disabled={Boolean(exporting)} onCheckedChange={checked => setOnlyDifferences(checked === true)} />{t("compare.onlyDifferences")}</label>
      <div className="ml-auto">
        <Popover>
          <PopoverTrigger asChild><Button variant="secondary" size="sm" disabled={!data}><DownloadIcon data-icon="inline-start" />{t("inspector.exportAria")}</Button></PopoverTrigger>
          <PopoverContent align="end" aria-label={t("inspector.exportAria")} className="w-80 max-w-[calc(100vw/var(--interface-zoom,1)-32px)]">
            <h2 className="px-4 pt-3 text-sm font-semibold">{t("inspector.exportAria")}</h2>
            <ReportExportControls watermark={watermark} onWatermarkChange={setWatermark} exporting={exporting} error={exportError}
              onExport={handleExport} onCopyPNG={() => handleExport("copy")} />
          </PopoverContent>
        </Popover>
      </div>
    </div>
    {data ? <div className="flex shrink-0 flex-wrap items-center gap-2 px-4 py-2" aria-label={t("comparison.channelControls")}>
      {data.reports.map((detail, index) => {
        const id = comparisonReportID(detail)
        const channel = reportInfo(detail).channel
        const collapsed = hiddenReports.includes(id)
        return <Button key={id} variant={collapsed ? "outline" : "secondary"} size="sm"
          className="h-auto min-h-8 max-w-full whitespace-normal [overflow-wrap:anywhere]"
          disabled={Boolean(exporting) || (!collapsed && visibleReports.length === 1)} aria-expanded={!collapsed}
          aria-controls={resultsID} aria-label={t(collapsed ? "comparison.expand" : "comparison.collapse", { channel })}
          onClick={() => setHiddenReports(current => collapsed ? current.filter(value => value !== id) : [...current, id])}>
          {index === 0 ? <ChevronLeftIcon /> : <ChevronRightIcon />}{channel}
          <span className="text-muted-foreground">{t(collapsed ? "comparison.expandLabel" : "comparison.collapseLabel")}</span>
        </Button>
      })}
    </div> : null}
    <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
      {!data ? state.key === key && state.error ? <div role="alert" className="p-4 text-xs text-destructive">{t("compare.error")}<Button className="ml-2" variant="outline" size="sm" onClick={() => setRetry(value => value + 1)}>{t("comparison.retry")}</Button></div> :
        <div role="status" className="flex items-center gap-2 p-4 text-xs"><Spinner />{t("compare.loading")}</div> :
        <div id={resultsID} className="min-w-0 space-y-3 p-4">
          <ReportComparisonBody key={comparisonKey} data={data} hiddenReports={hiddenReports} onlyDifferences={onlyDifferences} expanded={expanded}
            onExpandedChange={row => setExpandedRow({ comparison: comparisonKey, row })} disabled={Boolean(exporting)} />
        </div>}
    </ScrollArea>
    {exportData ? <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0" style={{ width: exportData.width }}>
      <article data-report-export-document ref={element => { if (element && exportReady.current) { exportReady.current(element); exportReady.current = null } }} className="relative bg-background p-4 text-foreground" style={{ width: exportData.width }}>
        <ReportComparisonBody data={exportData.comparison} hiddenReports={hiddenReports} onlyDifferences={onlyDifferences} expanded={expanded} exporting />
        <div data-report-watermark className="pointer-events-none absolute inset-0 grid grid-cols-2 content-around overflow-hidden">
          {Array.from({ length: 8 }, (_, index) => <span key={index} className="-rotate-12 text-center text-4xl font-semibold text-muted-foreground/15">{watermark.trim() || "rhzs"}</span>)}
        </div>
      </article>
    </div> : null}
  </div>
}

function ReportComparisonBody({ data, hiddenReports, onlyDifferences, expanded, onExpandedChange, exporting = false, disabled = false }: {
  data: ReportComparison
  hiddenReports: string[]
  onlyDifferences: boolean
  expanded: string
  onExpandedChange?: (row: string) => void
  exporting?: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation("reports")
  const reports = data.reports.filter(detail => !hiddenReports.includes(comparisonReportID(detail)))
  const sections = comparisonSections(data.rows).map(section => ({ ...section,
    rows: onlyDifferences ? section.rows.filter(row => row.different) : section.rows,
  })).filter(section => section.rows.length > 0)
  return <TooltipProvider delayDuration={250}><section aria-label={t("compare.title")} className="min-w-0 space-y-6">
    {sections.map(section => <section key={section.key} aria-label={section.name || t("system.quickPerformance")} className="min-w-0 space-y-2">
      <h2 className="text-xs font-semibold [overflow-wrap:anywhere]">{section.name || t("system.quickPerformance")}</h2>
      <ComparisonSuiteTable data={data} reports={reports} rows={section.rows} expanded={expanded} onExpandedChange={onExpandedChange}
        exporting={exporting} disabled={disabled} />
    </section>)}
    {!sections.length ? <p className="py-4 text-xs text-muted-foreground">{t(onlyDifferences ? "compare.noDifferences" : "hierarchy.noRecordedCases")}</p> : null}
  </section></TooltipProvider>
}

function ComparisonSuiteTable({ data, reports, rows, expanded, onExpandedChange, exporting, disabled }: {
  data: ReportComparison
  reports: ReportDetail[]
  rows: ReportComparisonRow[]
  expanded: string
  onExpandedChange?: (row: string) => void
  exporting: boolean
  disabled: boolean
}) {
  const { t } = useTranslation("reports")
  const detailsID = useId()
  const [page, setPage] = useState(0)
  const lastPage = Math.max(0, Math.ceil(rows.length / 40) - 1)
  const currentPage = Math.min(page, lastPage)
  const visible = exporting ? rows : rows.slice(currentPage * 40, (currentPage + 1) * 40)
  return <div className="min-w-0 space-y-3">
    <Table aria-label={t("compare.table")} className="table-fixed text-xs">
      <ComparisonTableColumns channels={reports.length} />
      <TableHeader><TableRow>
        <TableHead>{t("protocolDesign.caseName")}</TableHead>
        {reports.map(detail => {
          const info = reportInfo(detail)
          const id = comparisonReportID(detail)
          return <ComparisonChannelHeader key={id} channel={info.channel} model={info.model} baseline={id === data.baseline_id} exporting={exporting} />
        })}
      </TableRow></TableHeader>
      <TableBody>{visible.map(row => <Fragment key={row.key}>
        <TableRow data-state={expanded === row.key ? "selected" : undefined}>
          <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
            {exporting || row.cells.every(cell => cell.change === "missing") ? <div className="font-medium">{row.name || t("system.quickPerformance")}</div> :
              <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full min-w-0 max-w-full justify-start whitespace-normal px-0 py-1 text-left" aria-expanded={expanded === row.key}
                disabled={disabled} aria-controls={`${detailsID}-${encodeURIComponent(row.key)}`} onClick={() => onExpandedChange?.(expanded === row.key ? "" : row.key)}>
                {expanded === row.key ? <ChevronDownIcon aria-hidden="true" /> : <ChevronRightIcon aria-hidden="true" />}
                <span className="min-w-0 [overflow-wrap:anywhere]">{row.name || t("system.quickPerformance")}</span>
              </Button>}
          </TableCell>
          {row.cells.flatMap(cell => {
            const detail = reports.find(detail => comparisonReportID(detail) === cell.report_id)
            if (!detail) return []
            const caseItem = detail.source === "run" ? detail.entries.find(entry => entry.entry_id === cell.entry_id)?.cases.find(item => item.case_id === cell.case_id) : undefined
            return <TableCell key={cell.report_id} className="whitespace-normal align-top [overflow-wrap:anywhere]">
              <div className="flex min-h-7 flex-wrap items-center gap-2 py-1">
                {caseItem ? <CaseSeverityBadge severity={caseItem.severity} /> : null}
                {cell.change === "missing" || cell.status === "not_run" ? <Badge variant="outline" className="h-auto min-h-5 max-w-full whitespace-normal text-center text-muted-foreground [overflow-wrap:anywhere]">{t(cell.change === "missing" ? "compare.change.missing" : "comparison.noResult")}</Badge> :
                  <VerificationBadge status={cell.status as "passed" | "failed" | "not_applicable" | "indeterminate"} />}
              </div>
            </TableCell>
          })}
        </TableRow>
        {expanded === row.key ? <TableRow id={`${detailsID}-${encodeURIComponent(row.key)}`}><TableCell colSpan={reports.length + 1} className="whitespace-normal py-3">
          <div className="grid min-w-0 gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(100%, 240px), 1fr))" }}>
            {row.cells.flatMap(cell => {
              const detail = reports.find(detail => comparisonReportID(detail) === cell.report_id)
              return detail ? <div key={cell.report_id} className="min-w-0 space-y-3"><h3 className="font-medium">{reportLabel(detail)}</h3>
                <ComparisonMetrics cell={cell} baselineID={data.baseline_id} />
                <ComparisonEvidence detail={detail} cell={cell} />
              </div> : []
            })}
          </div>
        </TableCell></TableRow> : null}
      </Fragment>)}</TableBody>
    </Table>
    {!exporting && lastPage > 0 ? <div className="flex items-center justify-end gap-2 text-xs"><span>{currentPage + 1} / {lastPage + 1}</span>
      <Button size="xs" variant="outline" disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>{t("protocolDesign.previous")}</Button>
      <Button size="xs" variant="outline" disabled={currentPage === lastPage} onClick={() => setPage(currentPage + 1)}>{t("protocolDesign.next")}</Button>
    </div> : null}
  </div>
}

function comparisonSections(rows: ReportComparisonRow[]) {
  // Use Core-aligned entry identities so repeated names and missing cases keep their own sections.
  const parents = new Map<string, string>()
  const root = (key: string): string => {
    const parent = parents.get(key)
    if (!parent) return key
    const result = root(parent)
    parents.set(key, result)
    return result
  }
  const identities = rows.map(row => row.cells.filter(cell => cell.change !== "missing")
    .map(cell => JSON.stringify([cell.report_id, cell.entry_id])))
  for (const keys of identities) {
    if (!keys.length) continue
    for (const key of keys.slice(1)) {
      const left = root(keys[0])
      const right = root(key)
      if (left !== right) parents.set(right, left)
    }
  }
  const sections = new Map<string, { key: string; name: string; rows: ReportComparisonRow[] }>()
  rows.forEach((row, index) => {
    const key = root(identities[index][0] ?? JSON.stringify(["row", row.key]))
    let section = sections.get(key)
    if (!section) { section = { key, name: row.entry_name, rows: [] }; sections.set(key, section) }
    section.rows.push(row)
  })
  return [...sections.values()]
}

function ComparisonMetrics({ cell, baselineID }: { cell: ReportComparisonCell; baselineID: string }) {
  const { t, i18n } = useTranslation("reports")
  return <div className="space-y-2 text-[11px] tabular-nums">{cell.metrics.map(metric => <div key={metric.key}>
    <span className="text-muted-foreground">{t(`performanceOverview.metrics.${metric.key}`, { defaultValue: metric.key })} </span>
    {metric.value.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: 3 })} {metric.unit}
    {cell.report_id !== baselineID && metric.delta !== null ? <span className="ml-2 text-muted-foreground">Δ {metric.delta > 0 ? "+" : ""}{metric.delta.toLocaleString(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: 3 })}</span> : null}
  </div>)}</div>
}

function ComparisonEvidence({ detail, cell }: { detail: ReportDetail; cell: ReportComparisonCell }) {
  if (cell.change === "missing") return null
  if (detail.source === "quick_performance") return <QuickPerformanceRequestAnalysis report={detail.performance} />
  const entry = detail.entries.find(entry => entry.entry_id === cell.entry_id)
  if (entry?.performance) return <QuickPerformanceRequestAnalysis report={entry.performance} />
  return <ExecutionDetails results={entry?.cases.find(item => item.case_id === cell.case_id)?.request_results ?? []} />
}

function reportInfo(detail: ReportDetail) {
  return detail.source === "run" ? { channel: detail.report.channel.name, model: detail.report.model.name } :
    { channel: detail.performance.base_url, model: detail.performance.model_id }
}
function reportLabel(detail: ReportDetail) { const info = reportInfo(detail); return `${info.channel} · ${info.model}` }
