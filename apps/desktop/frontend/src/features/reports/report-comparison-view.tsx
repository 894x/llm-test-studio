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
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { CatalogCaseSeverity } from "@/features/catalog/data"
import { ExecutionDetails, VerificationBadge } from "./entry-case-table"
import { QuickPerformanceRequestAnalysis } from "@/features/quick-test/quick-performance-request-analysis"
import { blobToBase64 } from "./report-export-encoding"
import { copyReportPNGToClipboard } from "./report-clipboard"
import { ReportExportControls } from "./report-export-controls"
import { CaseSeverityBadge } from "./case-severity-badge"
import { COMPARISON_RESULT_COLUMN_WIDTH, ComparisonChannelHeader, ComparisonTableColumns } from "./comparison-channel-header"
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
  const [detailReportID, setDetailReportID] = useState("")
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
        const shown = data.reports.filter(detail => !hiddenReports.includes(comparisonReportID(detail))).length
        const minWidth = shown > 8 ? 284 + shown * COMPARISON_RESULT_COLUMN_WIDTH + (data.reports.length - shown) * 32 : 0
        const element = await new Promise<HTMLElement>(resolve => {
          exportReady.current = resolve
          setExportData({ comparison: data, width: Math.max(workspace.current?.clientWidth || 1200, minWidth) })
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
          <TooltipProvider><Tooltip><TooltipTrigger asChild><PopoverTrigger asChild><Button variant="secondary" size="icon-sm" aria-label={t("inspector.exportAria")} disabled={!data}><DownloadIcon aria-hidden="true" /></Button></PopoverTrigger></TooltipTrigger><TooltipContent>{t("inspector.exportAria")}</TooltipContent></Tooltip></TooltipProvider>
          <PopoverContent align="end" aria-label={t("inspector.exportAria")} className="w-80 max-w-[calc(100vw/var(--interface-zoom,1)-32px)]">
            <h2 className="px-4 pt-3 text-sm font-semibold">{t("inspector.exportAria")}</h2>
            <ReportExportControls watermark={watermark} onWatermarkChange={setWatermark} exporting={exporting} error={exportError}
              onExport={handleExport} onCopyPNG={() => handleExport("copy")} />
          </PopoverContent>
        </Popover>
      </div>
    </div>
    <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
      {!data ? state.key === key && state.error ? <div role="alert" className="p-4 text-xs text-destructive">{t("compare.error")}<Button className="ml-2" variant="outline" size="sm" onClick={() => setRetry(value => value + 1)}>{t("comparison.retry")}</Button></div> :
        <div role="status" className="flex items-center gap-2 p-4 text-xs"><Spinner />{t("compare.loading")}</div> :
        <div id={resultsID} className="min-w-0 space-y-3 p-4">
          <ReportComparisonBody key={comparisonKey} data={data} hiddenReports={hiddenReports} onlyDifferences={onlyDifferences} expanded={expanded}
            onExpandedChange={row => setExpandedRow({ comparison: comparisonKey, row })} detailReportID={detailReportID}
            onDetailReportChange={setDetailReportID} onToggleReport={id => setHiddenReports(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])} disabled={Boolean(exporting)} />
        </div>}
    </ScrollArea>
    {exportData ? <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0" style={{ width: exportData.width }}>
      <article data-report-export-document ref={element => { if (element && exportReady.current) { exportReady.current(element); exportReady.current = null } }} className="relative bg-background p-4 text-foreground" style={{ width: exportData.width }}>
        <ReportComparisonBody data={exportData.comparison} hiddenReports={hiddenReports} onlyDifferences={onlyDifferences} expanded={expanded} detailReportID={detailReportID} exporting />
        <div data-report-watermark className="pointer-events-none absolute inset-0 grid grid-cols-2 content-around overflow-hidden">
          {Array.from({ length: 8 }, (_, index) => <span key={index} className="-rotate-12 text-center text-4xl font-semibold text-muted-foreground/15">{watermark.trim() || "rhzs"}</span>)}
        </div>
      </article>
    </div> : null}
  </div>
}

function ReportComparisonBody({ data, hiddenReports, onlyDifferences, expanded, onExpandedChange, detailReportID, onDetailReportChange, onToggleReport, exporting = false, disabled = false }: {
  data: ReportComparison
  hiddenReports: string[]
  onlyDifferences: boolean
  expanded: string
  onExpandedChange?: (row: string) => void
  detailReportID: string
  onDetailReportChange?: (id: string) => void
  onToggleReport?: (id: string) => void
  exporting?: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation("reports")
  const reports = data.reports
  const sections = comparisonSections(data.rows).map(section => ({ ...section,
    rows: onlyDifferences ? section.rows.filter(row => row.different) : section.rows,
  })).filter(section => section.rows.length > 0)
  return <TooltipProvider delayDuration={250}><section aria-label={t("compare.title")} className="min-w-0 space-y-6">
    {sections.map(section => <section key={section.key} aria-label={section.name || t("system.quickPerformance")} className="min-w-0 space-y-2">
      <h2 className="text-xs font-semibold [overflow-wrap:anywhere]">{section.name || t("system.quickPerformance")}</h2>
      <ComparisonSuiteTable data={data} reports={reports} rows={section.rows} expanded={expanded} onExpandedChange={onExpandedChange}
        detailReportID={detailReportID} onDetailReportChange={onDetailReportChange} hiddenReports={hiddenReports} onToggleReport={onToggleReport} exporting={exporting} disabled={disabled} />
    </section>)}
    {!sections.length ? <p className="py-4 text-xs text-muted-foreground">{t(onlyDifferences ? "compare.noDifferences" : "hierarchy.noRecordedCases")}</p> : null}
  </section></TooltipProvider>
}

function ComparisonSuiteTable({ data, reports, rows, expanded, onExpandedChange, detailReportID, onDetailReportChange, hiddenReports, onToggleReport, exporting, disabled }: {
  data: ReportComparison
  reports: ReportDetail[]
  rows: ReportComparisonRow[]
  expanded: string
  onExpandedChange?: (row: string) => void
  detailReportID: string
  onDetailReportChange?: (id: string) => void
  hiddenReports: string[]
  onToggleReport?: (id: string) => void
  exporting: boolean
  disabled: boolean
}) {
  const { t } = useTranslation("reports")
  const detailsID = useId()
  const [page, setPage] = useState(0)
  const lastPage = Math.max(0, Math.ceil(rows.length / 40) - 1)
  const currentPage = Math.min(page, lastPage)
  const visible = exporting ? rows : rows.slice(currentPage * 40, (currentPage + 1) * 40)
  const visibleReports = reports.filter(detail => !hiddenReports.includes(comparisonReportID(detail)))
  const sharedSeverity = visibleReports.length > 8
  const reportIDs = visibleReports.map(comparisonReportID)
  const activeReportID = reportIDs.includes(detailReportID) ? detailReportID : reportIDs.includes(data.baseline_id) ? data.baseline_id : reportIDs[0]
  const table = <Table aria-label={t("compare.table")} className="table-fixed text-xs"
    style={sharedSeverity ? { minWidth: 284 + reports.reduce((width, detail) => width + (hiddenReports.includes(comparisonReportID(detail)) ? 32 : COMPARISON_RESULT_COLUMN_WIDTH), 0) } : undefined}>
      <ComparisonTableColumns channels={reports.length} collapsed={reports.map(detail => hiddenReports.includes(comparisonReportID(detail)))} sharedSeverity={sharedSeverity} />
      <TableHeader><TableRow>
        <TableHead>{t("protocolDesign.caseName")}</TableHead>
        {sharedSeverity ? <TableHead>{t("protocolDesign.severity")}</TableHead> : null}
        {reports.map(detail => {
          const info = reportInfo(detail)
          const id = comparisonReportID(detail)
          const collapsed = hiddenReports.includes(id)
          return <ComparisonChannelHeader key={id} channel={info.channel} baseline={id === data.baseline_id} exporting={exporting}
            collapsed={collapsed} onToggle={() => onToggleReport?.(id)} disabled={disabled || (!collapsed && visibleReports.length < 2)} />
        })}
      </TableRow></TableHeader>
      <TableBody>{visible.map(row => <Fragment key={row.key}>
        <TableRow>
          <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
            {exporting || row.cells.every(cell => cell.change === "missing") ? <div className="font-medium">{row.name || t("system.quickPerformance")}</div> :
              <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full min-w-0 max-w-full justify-start whitespace-normal px-0 py-1 text-left" aria-expanded={expanded === row.key}
                disabled={disabled} aria-controls={`${detailsID}-${encodeURIComponent(row.key)}`} onClick={() => onExpandedChange?.(expanded === row.key ? "" : row.key)}>
                {expanded === row.key ? <ChevronDownIcon aria-hidden="true" /> : <ChevronRightIcon aria-hidden="true" />}
                <span className="min-w-0 [overflow-wrap:anywhere]">{row.name || t("system.quickPerformance")}</span>
              </Button>}
          </TableCell>
          {sharedSeverity ? <TableCell className="align-top"><ComparisonSeverity row={row} reports={visibleReports} exporting={exporting} /></TableCell> : null}
          {row.cells.flatMap(cell => {
            const detail = reports.find(detail => comparisonReportID(detail) === cell.report_id)
            if (!detail) return []
            if (hiddenReports.includes(cell.report_id)) return <TableCell key={cell.report_id} />
            const caseItem = detail.source === "run" ? detail.entries.find(entry => entry.entry_id === cell.entry_id)?.cases.find(item => item.case_id === cell.case_id) : undefined
            const selected = expanded === row.key && cell.report_id === activeReportID
            const result = <>
              {caseItem && !sharedSeverity ? <CaseSeverityBadge severity={caseItem.severity} /> : null}
              {cell.change === "missing" || cell.status === "not_run" ? <Badge variant="outline" className="h-auto min-h-5 max-w-full whitespace-normal text-center text-muted-foreground [overflow-wrap:anywhere]">{t(cell.change === "missing" ? "compare.change.missing" : "comparison.noResult")}</Badge> :
                <VerificationBadge status={cell.status as "passed" | "failed" | "not_applicable" | "indeterminate"} />}
            </>
            return <TableCell key={cell.report_id} className="whitespace-normal align-top [overflow-wrap:anywhere]">
              {exporting ? <div className={`flex min-h-7 flex-wrap items-center gap-2 rounded-md px-1 py-1 ${selected ? "bg-surface-active" : ""}`}>{result}</div> :
                <Button variant="ghost" size="sm" className="h-auto min-h-7 w-full min-w-0 justify-start gap-2 whitespace-normal px-1 py-1 flex-wrap aria-pressed:bg-surface-active aria-pressed:hover:bg-surface-active"
                  disabled={disabled || cell.change === "missing"} aria-pressed={selected} aria-controls={`${detailsID}-${encodeURIComponent(row.key)}`}
                  aria-label={t("comparison.caseDetails", { channel: reportInfo(detail).channel, name: row.name || t("system.quickPerformance") })}
                  onClick={() => { onDetailReportChange?.(cell.report_id); onExpandedChange?.(row.key) }}>{result}</Button>}
            </TableCell>
          })}
        </TableRow>
        {expanded === row.key ? <TableRow id={`${detailsID}-${encodeURIComponent(row.key)}`}><TableCell colSpan={reports.length + (sharedSeverity ? 2 : 1)} className="whitespace-normal py-3">
          <ComparisonChannelDetails reports={visibleReports} row={row} baselineID={data.baseline_id} selectedReportID={detailReportID}
            onSelectedReportChange={onDetailReportChange} exporting={exporting} disabled={disabled} />
        </TableCell></TableRow> : null}
      </Fragment>)}</TableBody>
    </Table>
  return <div className="min-w-0 space-y-3">
    {sharedSeverity && !exporting ? <ScrollArea className="max-w-full [&>[data-slot=scroll-area-viewport]]:h-auto">{table}</ScrollArea> : table}
    {!exporting && lastPage > 0 ? <div className="flex items-center justify-end gap-2 text-xs"><span>{currentPage + 1} / {lastPage + 1}</span>
      <Button size="xs" variant="outline" disabled={!currentPage} onClick={() => setPage(currentPage - 1)}>{t("protocolDesign.previous")}</Button>
      <Button size="xs" variant="outline" disabled={currentPage === lastPage} onClick={() => setPage(currentPage + 1)}>{t("protocolDesign.next")}</Button>
    </div> : null}
  </div>
}

function ComparisonSeverity({ row, reports, exporting }: { row: ReportComparisonRow; reports: ReportDetail[]; exporting: boolean }) {
  const groups = new Map<CatalogCaseSeverity, string[]>()
  for (const detail of reports) {
    if (detail.source !== "run") continue
    const cell = row.cells.find(cell => cell.report_id === comparisonReportID(detail))
    if (!cell || cell.change === "missing") continue
    const item = detail.entries.find(entry => entry.entry_id === cell.entry_id)?.cases.find(item => item.case_id === cell.case_id)
    if (!item) continue
    const channels = groups.get(item.severity) ?? []
    channels.push(reportInfo(detail).channel)
    groups.set(item.severity, channels)
  }
  return <div className="flex min-h-7 flex-wrap items-center gap-2 py-1">{[...groups].map(([severity, channels]) =>
    <Tooltip key={severity}><TooltipTrigger asChild><span tabIndex={exporting ? undefined : 0} className="rounded-sm outline-none focus-visible:outline-2 focus-visible:outline-ring">
      <CaseSeverityBadge severity={severity} />
    </span></TooltipTrigger><TooltipContent className="max-w-80 [overflow-wrap:anywhere]">{channels.join(" · ")}</TooltipContent></Tooltip>
  )}</div>
}

function ComparisonChannelDetails({ reports, row, baselineID, selectedReportID, onSelectedReportChange, exporting, disabled }: {
  reports: ReportDetail[]
  row: ReportComparisonRow
  baselineID: string
  selectedReportID: string
  onSelectedReportChange?: (id: string) => void
  exporting: boolean
  disabled: boolean
}) {
  const { t } = useTranslation("reports")
  const viewportID = useId()
  const items = reports.flatMap(detail => {
    const cell = row.cells.find(cell => cell.report_id === comparisonReportID(detail))
    return cell ? [{ detail, cell }] : []
  })
  const selectedIndex = items.findIndex(item => item.cell.report_id === selectedReportID)
  const baselineIndex = items.findIndex(item => item.cell.report_id === baselineID)
  const index = selectedIndex >= 0 ? selectedIndex : Math.max(0, baselineIndex)
  const active = items[index]
  if (!active) return null
  if (exporting) return <section className="min-w-0 space-y-3">
    <h3 className="font-medium [overflow-wrap:anywhere]">{reportLabel(active.detail)}</h3>
    <ComparisonEvidence detail={active.detail} cell={active.cell} />
  </section>
  return <section aria-label={t("comparison.details")} className="grid min-w-0 grid-cols-[28px_minmax(0,1fr)_28px] items-center gap-2">
    <Button variant="secondary" size="icon-sm" disabled={disabled || items.length < 2} aria-label={t("comparison.previousChannel")}
      title={t("comparison.previousChannel")} aria-controls={viewportID} onClick={() => onSelectedReportChange?.(items[(index - 1 + items.length) % items.length].cell.report_id)}>
      <ChevronLeftIcon aria-hidden="true" />
    </Button>
    <div className="min-w-0 space-y-3">
      <div className="flex min-w-0 items-start justify-between gap-3" aria-live="polite" aria-atomic="true">
        <h3 className="min-w-0 font-medium [overflow-wrap:anywhere]">{reportLabel(active.detail)}</h3>
        <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">{t("comparison.channelPosition", { current: index + 1, total: items.length })}</span>
      </div>
      <div id={viewportID} className="min-w-0 overflow-hidden">
        <div className="flex min-w-0 transition-transform duration-200 ease-out motion-reduce:transition-none" style={{ transform: `translateX(-${index * 100}%)` }}>
          {items.map(({ detail, cell }, itemIndex) => <div key={cell.report_id} role="group" aria-label={reportLabel(detail)}
            aria-hidden={itemIndex !== index} inert={itemIndex !== index} className="w-full min-w-0 shrink-0">
            <ScrollArea className="h-[min(52vh,420px)]" contentWidth="viewport">
              <div className="min-w-0 space-y-3 pr-3 pb-2">
                <ComparisonEvidence detail={detail} cell={cell} />
              </div>
            </ScrollArea>
          </div>)}
        </div>
      </div>
    </div>
    <Button variant="secondary" size="icon-sm" disabled={disabled || items.length < 2} aria-label={t("comparison.nextChannel")}
      title={t("comparison.nextChannel")} aria-controls={viewportID} onClick={() => onSelectedReportChange?.(items[(index + 1) % items.length].cell.report_id)}>
      <ChevronRightIcon aria-hidden="true" />
    </Button>
  </section>
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
