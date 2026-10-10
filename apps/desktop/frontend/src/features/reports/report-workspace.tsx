import { formatPerformanceInteger } from "@/features/reports/performance-format"
import { desktopLocale, translateDesktop as tx, translateExecutionError } from "@/i18n/runtime"
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import ArrowLeftIcon from "lucide-react/dist/esm/icons/arrow-left.mjs"
import { useTranslation } from "react-i18next"

import { publicDesktopOperationErrorMessage } from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { CatalogSearch, CatalogSearchEmpty } from "@/features/catalog/catalog-search"
import { useCatalogSearch } from "@/features/catalog/use-catalog-search"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { EmptyInspector, InspectorHeader, InspectorRow, PageFrame } from "@/features/shell/page-frame"
import {
  performanceCapacitySummary,
  performanceCompletion,
  performanceSLOStatusLabel,
} from "@/features/quick-test/performance-summary"
import { QuickPerformanceRequestAnalysis } from "@/features/quick-test/quick-performance-request-analysis"
import { PerformanceCharts } from "./performance-charts"
import { PerformanceLatencyTable } from "./performance-latency-table"
import { PerformanceStreamingTimingTable } from "./performance-streaming-timing-table"
import { EntryCaseTable, ExecutionDetails, VerificationCounts, VerificationBadge } from "./entry-case-table"
import { CaseOutcomeNavigator, type ReportCaseTarget } from "./case-outcome-navigator"
import { exportVisualReport as createVisualReportExport } from "./visual-report-export"
import type { RunShortcutRenderer } from "@/features/runs/use-run-shortcuts"
import { EMPTY_COMPARISONS, type ComparisonSnapshot } from "@/features/comparisons/data"
import { useComparisonReport } from "./use-comparison-report"
import { ComparisonReportInspector } from "./comparison-report-inspector"
import { ReportExportControls } from "./report-export-controls"
import { channelVerdict, reportListItems } from "./report-list-items"
import { blobToBase64 } from "./report-export-encoding"

import { reportPlanTranslationKey, reportVerdictTranslationKey, type ExportedReport, type ReportDetail, type ReportExportFormat, type ReportMetric, type ReportSnapshot, type ReportSummary, type ReportEntryDetail, type ReportEntryStatus } from "./data"

export function ReportWorkspace({ snapshot, comparisons = EMPTY_COMPARISONS, preferredReportID, reportOpenSequence = 0, getDetail, exportReport, saveReportExport, copyReportPNG, renderRunShortcuts, exportVisualReport = createVisualReportExport }: {
  snapshot: ReportSnapshot
  comparisons?: ComparisonSnapshot
  preferredReportID?: string
  reportOpenSequence?: number
  getDetail: (reportId: string) => Promise<ReportDetail>
  exportReport: (reportId: string, format: ReportExportFormat, watermark: string, locale: string) => Promise<ExportedReport>
  saveReportExport: (filename: string, mediaType: string, dataBase64: string, locale: string) => Promise<boolean>
  copyReportPNG: (dataBase64: string) => Promise<void>
  renderRunShortcuts?: RunShortcutRenderer
  exportVisualReport?: typeof createVisualReportExport
}) {
  const { t, i18n } = useTranslation("reports")
  const [selectedID, setSelectedID] = useState(preferredReportID ?? "")
  const [viewingReportID, setViewingReportID] = useState(preferredReportID ?? "")
  const reportRequest = `${reportOpenSequence}:${preferredReportID ?? ""}`
  const [lastReportRequest, setLastReportRequest] = useState(reportRequest)
  if (lastReportRequest !== reportRequest) {
    setLastReportRequest(reportRequest)
    if (preferredReportID) {
      setSelectedID(preferredReportID)
      setViewingReportID(preferredReportID)
    }
  }
  const [detailState, setDetailState] = useState<{ reportID: string; detail?: ReportDetail; error?: boolean }>({ reportID: "" })
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [exportError, setExportError] = useState("")
  const [watermark, setWatermark] = useState("rhzs")
  const [exportDocument, setExportDocument] = useState<{
    report: ReportSummary
    detail: ReportDetail
    watermark: string
  } | null>(null)
  const exportDocumentRef = useRef<HTMLElement>(null)
  const exportDocumentReadyRef = useRef<((element: HTMLElement) => void) | null>(null)
  const detailCache = useRef<{ reportID: string; reader: typeof getDetail; detail: ReportDetail } | null>(null)
  const detailRequest = useRef<{ reportID: string; reader: typeof getDetail; promise: Promise<ReportDetail> } | null>(null)
  const items = useMemo(() => reportListItems(snapshot.reports, comparisons.comparisons), [snapshot.reports, comparisons.comparisons])
  const reportsByRun = useMemo(() => new Map(snapshot.reports.flatMap(report => report.run_id ? [[report.run_id, report] as const] : [])), [snapshot.reports])
  const search = useCatalogSearch(items, (item) => item.kind === "comparison" ? [
    item.id, t("comparison.title"), item.comparison.plan_name, item.comparison.model_name,
    ...item.comparison.channels.flatMap(channel => [channel.channel_name, channelVerdict(channel, t, reportsByRun.get(channel.run_id))]),
  ] : [item.report.id, item.report.run_id ?? "", displayReportVerdict(item.report, t), displayReportPlan(item.report, t),
    item.report.model_name, item.report.channel_name])
  // A run shortcut can still open its individual report after the list groups it.
  const selectableItems = viewingReportID ? [...items, ...snapshot.reports.map(report => ({ id: report.id, kind: "report" as const, report }))] : search.rows
  const selectedItem = selectableItems.find(item => item.id === selectedID) ?? selectableItems[0]
  const selected = selectedItem?.kind === "report" ? selectedItem.report : undefined
  const selectedComparison = selectedItem?.kind === "comparison" ? selectedItem.comparison : undefined
  const selectedReportID = selected?.id ?? ""
  const detail = detailState.reportID === selectedReportID ? detailState.detail ?? null : null
  const detailError = detailState.reportID === selectedReportID && detailState.error ? t("detailError") : ""
  const isViewingReport = viewingReportID !== "" && viewingReportID === selectedItem?.id
  const selectedVerdict = selectedComparison ? t("comparison.title") : selected ? displayReportVerdict(selected, t) : t("generic")
  const comparisonReport = useComparisonReport({ comparison: selectedComparison, snapshot, getDetail,
    saveReportExport, copyReportPNG, exportVisualReport, active: isViewingReport })

  const loadDetail = useCallback((reportID: string): Promise<ReportDetail> => {
    const cached = detailCache.current
    if (cached?.reportID === reportID && cached.reader === getDetail) {
      detailRequest.current = null
      setDetailState({ reportID, detail: cached.detail })
      return Promise.resolve(cached.detail)
    }
    const pending = detailRequest.current
    if (pending?.reportID === reportID && pending.reader === getDetail) return pending.promise

    setDetailState({ reportID })

    const request = {
      reportID,
      reader: getDetail,
      promise: Promise.resolve().then(() => getDetail(reportID)),
    }
    request.promise = request.promise.then(
      (value) => {
        if (detailRequest.current === request) {
          detailCache.current = { reportID, reader: getDetail, detail: value }
          setDetailState({ reportID, detail: value })
        }
        return value
      },
      (error: unknown) => {
        if (detailRequest.current === request) setDetailState({ reportID, error: true })
        throw error
      },
    ).finally(() => {
      if (detailRequest.current === request) detailRequest.current = null
    })
    detailRequest.current = request
    return request.promise
  }, [getDetail])

  useEffect(() => {
    if (isViewingReport && selectedReportID) {
      void loadDetail(selectedReportID).catch(() => undefined)
    }
  }, [isViewingReport, loadDetail, selectedReportID])

  const attachExportDocument = useCallback((element: HTMLElement | null) => {
    exportDocumentRef.current = element
    if (!element || !exportDocumentReadyRef.current) return
    exportDocumentReadyRef.current(element)
    exportDocumentReadyRef.current = null
  }, [])

  const mountExportDocument = (
    report: ReportSummary,
    reportDetail: ReportDetail,
  ): Promise<HTMLElement> => new Promise((resolve) => {
    exportDocumentReadyRef.current = resolve
    setExportDocument({ report, detail: reportDetail, watermark })
  })

  const unmountExportDocument = () => {
    exportDocumentReadyRef.current = null
    setExportDocument(null)
  }

  const handleExport = async (format: ReportExportFormat) => {
    if (!selected) return
    setExporting(format)
    setExportError("")
    try {
      if (format === "json") {
        const exported = await exportReport(selected.id, format, watermark, i18n.resolvedLanguage ?? i18n.language)
        await saveReportExport(exported.filename, exported.media_type, exported.data_base64, i18n.resolvedLanguage ?? i18n.language)
      } else {
        const reportDetail = detail ?? await loadDetail(selected.id)
        const element = await mountExportDocument(selected, reportDetail)
        const exported = await exportVisualReport(element, format, selected.id)
        await saveReportExport(exported.filename, exported.mediaType, await blobToBase64(exported.blob), i18n.resolvedLanguage ?? i18n.language)
      }
    } catch (error) {
      setExportError(publicDesktopOperationErrorMessage(
        error,
        t("export.operation", { format: format.toUpperCase(), name: selectedVerdict }),
        t("export.error"),
      ))
    } finally {
      if (format !== "json") unmountExportDocument()
      setExporting("")
    }
  }

  const copyPNG = async () => {
    if (!selected) return
    setExporting("copy")
    setExportError("")
    try {
      const reportDetail = detail ?? await loadDetail(selected.id)
      const element = await mountExportDocument(selected, reportDetail)
      const exported = await exportVisualReport(element, "png", selected.id)
      try {
        await copyReportPNG(await blobToBase64(exported.blob))
      } catch (nativeError) {
        const ClipboardItemType = window.ClipboardItem
        if (!navigator.clipboard?.write || !ClipboardItemType) throw nativeError
        await navigator.clipboard.write([new ClipboardItemType({ [exported.mediaType]: exported.blob })])
      }
    } catch (error) {
      setExportError(publicDesktopOperationErrorMessage(
        error,
        t("export.copyOperation", { name: selectedVerdict }),
        t("export.copyError"),
      ))
    } finally {
      unmountExportDocument()
      setExporting("")
    }
  }

  return <>
    <PageFrame
      title={t(isViewingReport ? "detailTitle" : "title")}
      description={isViewingReport ? t("detailDescription", { name: selectedVerdict }) : t("description")}
      actions={isViewingReport ? <Button variant="outline" size="sm" onClick={() => setViewingReportID("")}><ArrowLeftIcon />{t("back")}</Button> : (
        <CatalogSearch search={search} label={t("search.label")} placeholder={t("search.placeholder")} totalLabel={t("count", { count: items.length })} />
      )}
      inspector={selectedComparison ? <ComparisonReportInspector comparison={selectedComparison} snapshot={snapshot}
        onOpen={!isViewingReport ? () => setViewingReportID(selectedComparison.id) : undefined} exportControls={comparisonReport.controls} /> : selected ? (
        <ReportInspector report={selected} detail={detail} detailError={detailError} exporting={exporting} exportError={exportError} watermark={watermark} onWatermarkChange={setWatermark} onExport={handleExport} onCopyPNG={copyPNG}
          shortcuts={selected.source === "run" && selected.run_id ? renderRunShortcuts?.(selected.run_id, !isViewingReport) : null} />
      ) : <EmptyInspector label={t("noneSelected")} />}
      inspectorLabel={t("detailTitle")}
    >
      {search.empty && !isViewingReport ? <CatalogSearchEmpty onClear={search.clear} /> : items.length === 0 ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Empty><EmptyTitle>{t("empty")}</EmptyTitle><EmptyDescription>{t("emptyHint")}</EmptyDescription></Empty>
        </ScrollArea>
      ) : isViewingReport ? (
        <div className="flex min-h-0 flex-1 flex-col">
          {selectedComparison ? comparisonReport.content :
            <ReportContent key={selectedReportID} detail={detail} error={detailError} />}
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <ScrollArea className="min-h-0 flex-1 px-4">
            <Table aria-label={t("catalogAria")} className="min-w-[840px]">
              <TableHeader className="sticky top-0 z-10 bg-background"><TableRow className="hover:bg-transparent">
                <TableHead className="h-8 w-[88px] pl-2 text-[11px]">{t("columns.verdict")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.reportPlan")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.target")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.cases")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.generated")}</TableHead><TableHead className="h-8 w-[96px] pr-2 text-right text-[11px]">{t("columns.view")}</TableHead>
              </TableRow></TableHeader>
              <TableBody>{search.rows.map((item) => {
                if (item.kind === "comparison") {
                  const comparison = item.comparison
                  return <TableRow key={item.id} data-state={item.id === selectedItem?.id ? "selected" : undefined} aria-selected={item.id === selectedItem?.id}
                    className="h-11" onClick={() => setSelectedID(item.id)}>
                    <TableCell className="py-1 pl-2 text-xs">{t(`common:status.${comparison.status}`)}</TableCell>
                    <TableCell className="py-1 text-xs"><div className="font-medium">{t("comparison.title")}</div><div className="mt-0.5 text-[10px] text-muted-foreground">{comparison.plan_name}</div></TableCell>
                    <TableCell className="py-1 text-xs"><div>{comparison.model_name}</div><div className="mt-0.5 text-[10px] text-muted-foreground">{comparison.channels.map(channel => channel.channel_name).join(" / ")}</div></TableCell>
                    <TableCell className="py-1 text-[11px] tabular-nums">{comparison.channels.map(channel => {
                      const report = reportsByRun.get(channel.run_id)
                      return <div key={channel.channel_id}>{channel.channel_name} · {channelVerdict(channel, t, report)}{report ? ` · ${report.passed_case_count}/${report.verified_case_count}` : ""}</div>
                    })}</TableCell>
                    <TableCell className="py-1 text-xs tabular-nums">{formatTimestamp(comparison.created_at, i18n.resolvedLanguage ?? i18n.language)}</TableCell>
                    <TableCell className="py-1 pr-2 text-right"><Button variant="outline" size="xs" aria-label={t("comparison.viewAria", { model: comparison.model_name })}
                      onClick={event => { event.stopPropagation(); setSelectedID(item.id); setViewingReportID(item.id) }}>{t("comparison.view")}</Button></TableCell>
                  </TableRow>
                }
                const report = item.report
                return (
                <TableRow key={report.id} data-state={report.id === selected?.id ? "selected" : undefined} aria-selected={report.id === selected?.id} onClick={() => setSelectedID(report.id)} className="h-11">
                  <TableCell className="py-1 pl-2"><ConclusionBadge passed={report.passed} verdict={report.verdict} status={report.run_status} /></TableCell>
                  <TableCell className="py-1"><div className="max-w-[240px] truncate text-xs font-medium">{displayReportVerdict(report, t)}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{displayReportPlan(report, t)}</div></TableCell>
                  <TableCell className="py-1"><div className="truncate text-xs">{report.model_name}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.channel_name}</div></TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{report.passed_case_count}/{report.verified_case_count}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{formatTimestamp(report.generated_at, i18n.resolvedLanguage ?? i18n.language)}</TableCell>
                  <TableCell className="py-1 pr-2 text-right"><Button variant="outline" size="xs" aria-label={t("viewAria", { name: displayReportVerdict(report, t) })} onClick={(event) => { event.stopPropagation(); setSelectedID(report.id); setViewingReportID(report.id) }}>{t("view")}</Button></TableCell>
                </TableRow>
                )
              })}</TableBody>
            </Table>
          </ScrollArea>
        </div>
      )}
    </PageFrame>
    {comparisonReport.exportSurface}
    {exportDocument ? (
      <ReportExportSurface
        ref={attachExportDocument}
        report={exportDocument.report}
        detail={exportDocument.detail}
        watermark={exportDocument.watermark}
      />
    ) : null}
  </>
}

const ReportContent = memo(function ReportContent({ detail, error }: { detail: ReportDetail | null; error: string }) {
  const { t } = useTranslation("reports")
  if (error) return <div role="alert" className="border-t px-4 py-3 text-xs text-destructive">{error}</div>
  if (!detail) {
    return (
      <div
        role="status"
        aria-live="polite"
        aria-busy="true"
        className="flex min-h-0 flex-1 items-center justify-center border-t px-4 py-12"
      >
        <div className="flex max-w-sm flex-col items-center text-center">
          <Spinner className="size-7 text-primary" />
          <p className="mt-3 text-sm font-medium text-foreground">{t("detailLoading")}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t("detailLoadingHint")}</p>
        </div>
      </div>
    )
  }
  if (detail.source === "quick_performance") return <QuickPerformanceDetail detail={detail} />
  return <RunReportDetail detail={detail} />
})

function RunReportDetail({ detail }: { detail: Extract<ReportDetail, { source: "run" }> }) {
  return (
    <ScrollArea className="min-h-[180px] flex-[3] border-t [&>[data-slot=scroll-area-viewport]>div]:block!">
      <RunReportBody detail={detail} />
    </ScrollArea>
  )
}

function RunReportBody({ detail, paginate = true }: { detail: Extract<ReportDetail, { source: "run" }>; paginate?: boolean }) {
  const { t: tx } = useTranslation()
  const [selectedCase, setSelectedCase] = useState<ReportCaseTarget | null>(null)
  const [caseNavigationRequest, setCaseNavigationRequest] = useState(0)
  const entries = detail.entries
  const unassignedResults = detail.unassigned_request_results
  const planLabel = tx("desktop:reports_plan_report")
  const unassignedLabel = tx("desktop:reports_unassigned_requests")
  return (
    <section aria-label={planLabel} className="space-y-4 p-4">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">{planLabel}</h2>
          <p className="mt-0.5 truncate text-[10px] text-muted-foreground">
            {detail.report.model.name} · {detail.report.channel.name}
          </p>
        </div>
        <ConclusionBadge passed={detail.report.conclusion.passed} verdict={detail.report.conclusion.verdict} status={detail.report.run_status} />
      </header>
      <CaseOutcomeNavigator
        entries={entries}
        selected={selectedCase}
        onSelect={(target) => {
          setSelectedCase(target)
          setCaseNavigationRequest((request) => request + 1)
        }}
      />
      {entries.map((suite) => (
        <SuiteReportSection
          key={suite.entry_id}
          suite={suite}
          selectedCaseID={selectedCase?.entryID === suite.entry_id ? selectedCase.caseID : ""}
          caseNavigationRequest={caseNavigationRequest}
          paginate={paginate}
        />
      ))}
      {unassignedResults.length ? (
        <section aria-label={unassignedLabel} className="min-w-0 overflow-hidden">
          <header className="border-b bg-muted/25 py-2">
            <h3 className="text-xs font-semibold">{unassignedLabel}</h3>
          </header>
          <div className="p-3"><ExecutionDetails results={unassignedResults} paginate={paginate} /></div>
        </section>
      ) : null}
    </section>
  )
}

function SuiteReportSection({
  suite,
  selectedCaseID,
  caseNavigationRequest,
  paginate = true,
}: {
  suite: ReportEntryDetail
  selectedCaseID: string
  caseNavigationRequest: number
  paginate?: boolean
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("reports")
  const displayName = suite.name
  return (
    <section aria-label={tx("desktop:reports_suite_aria", { value1: displayName })} className="min-w-0 overflow-hidden">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/30 py-2.5">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">{displayName}</h3>
          <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
            {suite.protocol}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <SuiteStatusBadge status={suite.status} label={suiteStatusLabel(suite.status, t)} />
          {suite.status === "completed" ? <VerificationBadge status={suite.verification.status} /> : null}
        </div>
      </header>
      {suite.conclusion.issues.length ? (
        <ul className="border-b py-2 text-[11px] text-destructive">
          {suite.conclusion.issues.map((issue) => <li key={issue}>{suiteIssueLabel(issue, t)}</li>)}
        </ul>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 border-b px-3 py-2 text-[11px] text-muted-foreground">
        <VerificationCounts summary={suite.verification} />
        <span>{t(suite.load.mode === "single" ? "protocolDesign.caseConcurrencySummary" : "protocolDesign.loadSummary", { mode: suite.load.mode, concurrency: suite.load.concurrency, count: suite.load.request_count, timeout: suite.load.request_timeout_ms })}</span>
        <span>Seed {suite.seed}</span>
        <span>{t("protocolDesign.warmup", { count: suite.warmup_count })}</span>
        {Object.entries(suite.settings).map(([key, value]) => <span key={key}>{t(`protocolDesign.${key}`)}: {value} ms</span>)}
        {Object.entries(suite.parameters).map(([key, value]) => <span key={key} className="max-w-72 truncate" title={JSON.stringify(value)}>{key}: {typeof value === "string" ? value : JSON.stringify(value)}</span>)}
      </div>
      {Object.keys(suite.metrics).length || Object.keys(suite.sla).length ? <details className="border-b px-3 py-2">
        <summary className="cursor-pointer rounded-sm text-[11px] text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">{t("inspector.coreMetrics")}</summary>
        <div className="grid gap-3 pt-2 md:grid-cols-2"><SuiteMetricSummary title={t("inspector.coreMetrics")} metrics={suite.metrics} /><SuiteMetricSummary title="SLA" metrics={suite.sla} /></div>
      </details> : null}
      <EntryCaseTable entry={suite} selectedCaseID={selectedCaseID} caseNavigationRequest={caseNavigationRequest} paginate={paginate} />
    </section>
  )
}

function SuiteMetricSummary({
  title,
  metrics,
}: {
  title: string
  metrics: Record<string, ReportMetric>
}) {
  const { t } = useTranslation("reports")
  const entries = Object.entries(metrics)
  if (!entries.length) return null
  return (
    <section className="min-w-0 py-2.5">
      <h4 className="mb-2 text-[11px] font-semibold">{title}</h4>
      <dl className="grid gap-2 sm:grid-cols-2">
        {entries.map(([name, metric]) => (
          <div key={name} className="min-w-0 py-1.5">
            <dt className="truncate font-mono text-[10px] text-muted-foreground">{name}</dt>
            <dd className="mt-0.5 text-xs font-semibold tabular-nums">
              {formatMetric(metric.value)} {metric.unit}
            </dd>
            <dd className="text-[10px] text-muted-foreground">
              {t("inspector.samples")}: {formatMetric(metric.samples)}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

function suiteStatusLabel(status: ReportEntryStatus, t: ReturnType<typeof useTranslation<"reports">>["t"]): string {
  if (status === "completed") return t("suiteStatus.completed")
  if (status === "failed") return t("suiteStatus.failed")
  if (status === "cancelled") return t("suiteStatus.cancelled")
  return t("suiteStatus.notStarted")
}

function SuiteStatusBadge({ status, label }: { status: ReportEntryStatus; label: string }) {
  const className = status === "failed"
    ? "border-destructive/25 bg-destructive/10 text-destructive"
    : status === "cancelled"
      ? "border-warning/25 bg-warning-soft text-warning-strong"
      : status === "not_started"
        ? "border-border bg-muted text-muted-foreground"
        : undefined
  return <Badge variant="outline" className={className}>{label}</Badge>
}

function suiteIssueLabel(issue: string, t: ReturnType<typeof useTranslation<"reports">>["t"]): string {
  if (issue === "suite execution failed") return t("suiteIssue.failed")
  if (issue === "suite execution was cancelled") return t("suiteIssue.cancelled")
  if (issue === "suite execution was not started") return t("suiteIssue.notStarted")
  return issue
}

function QuickPerformanceDetail({ detail }: { detail: Extract<ReportDetail, { source: "quick_performance" }> }) {
  return (
    <ScrollArea className="min-h-[260px] flex-[3] border-t [&>[data-slot=scroll-area-viewport]>div]:block!">
      <QuickPerformanceBody detail={detail} includeRequestAnalysis />
    </ScrollArea>
  )
}

function QuickPerformanceBody({ detail, includeRequestAnalysis = false }: {
  detail: Extract<ReportDetail, { source: "quick_performance" }>
  includeRequestAnalysis?: boolean
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("reports")
  const report = detail.performance
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned)
  const targetRanges = performanceTargetRanges(report)
  const hasSLOCapacityData = report.slo_assessment !== undefined || report.capacity_result !== undefined
  const hasPhaseThreeData = report.request_budget !== undefined || report.warmup !== undefined || report.ramp !== undefined || report.time_slices !== undefined ||
    report.profile.warmup_requests !== undefined || report.profile.ramp_duration_ms !== undefined || report.profile.ramp_request_cap !== undefined || report.profile.slice_duration_ms !== undefined
  return <section aria-label={tx("desktop:reports_archived_performance_report")} className="space-y-4 p-4">
    <div>
      <h4 className="mb-2 text-xs font-semibold">{t("performance.config")}</h4>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <ContextValue label={tx("desktop:catalog_protocol")} value={report.protocol} />
        <ContextValue label={tx("desktop:catalog_model")} value={report.model_id} />
        <ContextValue label={tx("desktop:quick-test_endpoint")} value={report.endpoint} mono />
        <ContextValue label={tx("desktop:catalog_load_mode")} value={performanceLoadMode(report.profile.load_mode)} />
        {report.profile.load_mode === "open_loop" ? <ContextValue label={tx("desktop:quick-test_arrival_distribution")} value={performanceArrivalPattern(report)} /> : null}
        <ContextValue label={tx("desktop:quick-test_workload")} value={performanceWorkloadMode(report)} />
        <ContextValue label={tx("desktop:quick-test_random_input")} value={report.profile.random_input ? tx("desktop:quick-test_enabled") : tx("desktop:quick-test_disabled")} />
        <ContextValue label={tx("desktop:reports_stop_condition")} value={performanceMode(report.profile.request_count, report.profile.duration_ms)} />
        <ContextValue label={report.profile.load_mode === "open_loop" ? tx("desktop:quick-test_maximum_in_flight") : tx("desktop:reports_configured_concurrency")} value={formatMetric(report.profile.load_mode === "open_loop" ? (report.profile.max_in_flight ?? 0) : report.profile.concurrency)} />
        <ContextValue label={tx("desktop:reports_request_timeout")} value={formatDuration(report.profile.timeout_ms)} />
        <ContextValue label={report.profile.workload_mode === "normal" ? tx("desktop:reports_mean_tokens_input_output") : tx("desktop:reports_target_tokens_input_output")} value={`${formatMetric(report.profile.input_tokens)} / ${formatMetric(report.profile.output_tokens)}`} />
        {report.profile.workload_mode === "normal" ? <ContextValue label={tx("desktop:reports_token_standard_deviation_input_output")} value={`${formatMetric(report.profile.input_tokens_stddev ?? 0)} / ${formatMetric(report.profile.output_tokens_stddev ?? 0)}`} /> : null}
        <ContextValue label={tx("desktop:quick-test_random_seed")} value={performanceSeed(report)} />
        <ContextValue label={tx("desktop:quick-test_shared_prefix")} value={performanceSharedPrefix(report)} />
        {targetRanges ? <ContextValue label={tx("desktop:quick-test_sampled_target_range_input_output")} value={targetRanges} /> : null}
        {hasPhaseThreeData ? <ContextValue label={tx("desktop:reports_warmup_requests")} value={performanceWarmupConfiguration(report)} /> : null}
        {hasPhaseThreeData ? <ContextValue label={tx("desktop:reports_ramp_configuration")} value={performanceRampConfiguration(report)} /> : null}
        {hasPhaseThreeData ? <ContextValue label={tx("desktop:reports_slice_resolution")} value={performanceSliceConfiguration(report)} /> : null}
      </dl>
    </div>
    {hasSLOCapacityData ? (
      <>
        <Separator />
        <div>
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <h4 className="text-xs font-semibold">{tx("desktop:quick-test_slo_and_capacity")}</h4>
            <div className="flex flex-wrap gap-1.5">
              <Badge variant="outline" className={report.success ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>
                 {tx("desktop:quick-test_transport_and_protocol")}{report.success ? tx("desktop:quick-test_passed") : tx("desktop:quick-test_failed_301")}
              </Badge>
              {report.slo_assessment ? <SLOStatusBadge status={report.slo_assessment.status} /> : null}
            </div>
          </div>
          {report.slo_assessment ? (
            <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
              <InlineSummaryValue label={tx("desktop:reports_thresholds")} value={formatSLOThresholds(report.slo_assessment.thresholds)} />
              <InlineSummaryValue label={tx("desktop:reports_target_compliance")} value={`${formatMetric(report.slo_assessment.target_percent)}%`} />
              <InlineSummaryValue label={tx("desktop:quick-test_good_requests")} value={`${formatMetric(report.slo_assessment.good_requests)} / ${formatMetric(report.slo_assessment.total_requests)}`} />
              <InlineSummaryValue label={tx("desktop:reports_actual_compliance")} value={`${formatMetric(report.slo_assessment.good_request_percent)}%`} />
              <InlineSummaryValue label="Goodput" value={`${formatPerformanceInteger(report.slo_assessment.goodput_qps)} req/s`} />
              <InlineSummaryValue label={tx("desktop:reports_violations")} value={formatSLOViolations(report.slo_assessment.violations)} />
            </div>
          ) : null}
          {report.capacity_result ? (
            <>
              <p className="mt-3 rounded-md border bg-background/70 px-3 py-2 text-xs font-semibold tabular-nums">
                {performanceCapacitySummary(report.capacity_result, report.profile.load_mode ?? "fixed_concurrency")}
              </p>
              <CapacityRungTable report={report} />
            </>
          ) : null}
        </div>
      </>
    ) : null}
    {hasPhaseThreeData ? (
      <>
        <Separator />
        <div>
          <h4 className="mb-2 text-xs font-semibold">{tx("desktop:reports_preparation_and_budget")}</h4>
          <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
            {report.request_budget ? <SummaryValue label={tx("desktop:quick-test_request_budget")} value={formatRequestBudget(report)} /> : null}
            {report.warmup ? <SummaryValue label={tx("desktop:reports_warmup_traffic")} value={formatTrafficSummary(report.warmup)} /> : null}
            {report.warmup ? <SummaryValue label={tx("desktop:reports_warmup_tokens_input_output_cached")} value={formatTrafficTokens(report.warmup)} /> : null}
            {report.ramp ? <SummaryValue label={tx("desktop:reports_ramp_traffic")} value={formatTrafficSummary(report.ramp.traffic)} /> : null}
            {report.ramp ? <SummaryValue label={tx("desktop:reports_ramp_target")} value={formatRampTarget(report)} /> : null}
            {report.ramp ? <SummaryValue label={tx("desktop:reports_ramp_duration_sending_draining_total")} value={formatTrafficDuration(report.ramp.traffic)} /> : null}
          </div>
          {report.ramp && !report.ramp.completed_window ? <p role="status" className="mt-3 rounded-md border border-warning/25 bg-warning-soft px-3 py-2 text-[11px] text-warning">{tx("desktop:reports_the_ramp_window_did_not_complete_primary_metrics_still_cover")}</p> : null}
        </div>
      </>
    ) : null}
    <Separator />
    <div>
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-xs font-semibold">{tx("desktop:reports_steady_state_results")}</h4>
        <span className="text-[10px] text-muted-foreground">{tx("desktop:reports_primary_metrics_cover_steady_state_only")}</span>
      </div>
      <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <SummaryValue label={completion.label} value={completion.value} />
        <SummaryValue label={tx("desktop:quick-test_succeeded")} value={String(report.metrics.succeeded)} />
        <SummaryValue label={tx("desktop:quick-test_failed")} value={String(report.metrics.failed)} />
        <SummaryValue label={tx("desktop:quick-test_success_rate")} value={`${formatMetric(report.metrics.success_rate_percent)}%`} />
        <SummaryValue label={tx("desktop:quick-test_target_send_rate")} value={performanceTargetRate(report)} />
        <SummaryValue label={tx("desktop:quick-test_offered_load")} value={optionalRequestRate(report.metrics.offered_qps)} />
        <SummaryValue label={tx("desktop:quick-test_actual_send_rate")} value={optionalRequestRate(report.metrics.launched_qps)} />
        <SummaryValue label={tx("desktop:quick-test_completed_request_throughput")} value={optionalRequestRate(report.metrics.completed_qps)} />
        <SummaryValue label={tx("desktop:quick-test_successful_request_throughput")} value={optionalRequestRate(report.metrics.successful_request_qps)} />
        <SummaryValue label={report.profile.load_mode === "open_loop" ? tx("desktop:quick-test_peak_in_flight_limit") : tx("desktop:quick-test_peak_in_flight_configured_concurrency")} value={`${report.progress.peak_in_flight} / ${report.profile.load_mode === "open_loop" ? (report.profile.max_in_flight ?? "—") : (report.progress.capacity_target ?? report.profile.concurrency)}`} />
        <SummaryValue label={tx("desktop:quick-test_total_duration")} value={`${formatMetric(report.progress.total_duration_ms / 1_000)} s`} />
        <SummaryValue label="RPM" value={formatPerformanceInteger(report.metrics.rpm)} />
        <SummaryValue label={tx("desktop:reports_input_tpm")} value={`${formatPerformanceInteger(report.metrics.input_tpm)} TPM`} />
        <SummaryValue label={tx("desktop:reports_output_tpm")} value={`${formatPerformanceInteger(report.metrics.output_tpm)} TPM`} />
        <SummaryValue label={tx("desktop:reports_total_tpm")} value={`${formatPerformanceInteger(report.metrics.total_tpm)} TPM`} />
        <SummaryValue label={tx("desktop:reports_aggregate_output_throughput")} value={`${formatPerformanceInteger(report.metrics.generation_tps)} token/s`} />
        <SummaryValue label={t("performance.tokenTotals")} value={`${formatPerformanceInteger(report.metrics.prompt_tokens)} / ${formatPerformanceInteger(report.metrics.completion_tokens)} / ${formatPerformanceInteger(report.metrics.cached_tokens)}`} />
        <SummaryValue label={t("performance.cacheRate")} value={report.metrics.prompt_tokens > 0 ? `${formatMetric(report.metrics.cache_rate_percent)}%` : "—"} />
      </div>
    </div>
    <PerformanceLatencyTable metrics={report.metrics} />
    <PerformanceStreamingTimingTable metrics={report.metrics} />
    {report.time_slices !== undefined ? <PerformanceTimeSliceTable slices={report.time_slices} /> : null}
    <PerformanceCharts samples={report.samples} percentiles={report.metrics} />
    {includeRequestAnalysis ? (
      <>
        <Separator />
        <QuickPerformanceRequestAnalysis report={report} />
      </>
    ) : null}
  </section>
}

function ReportExportSurface({ ref, report, detail, watermark }: {
  ref: React.Ref<HTMLElement>
  report: ReportSummary
  detail: ReportDetail
  watermark: string
}) {
  const { t } = useTranslation("reports")
  const label = watermark.trim() || "rhzs"
  return <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0 z-[-1] w-[1200px]">
    <article ref={ref} data-report-export-document className="relative w-[1200px] overflow-hidden bg-background text-foreground">
      <header className="px-4 py-3">
        <h1 className="text-lg font-semibold tracking-tight">{t("detailTitle")}</h1>
        <p className="mt-1 text-[11px] text-muted-foreground">{t("detailDescription", { name: displayReportVerdict(report, t) })}</p>
      </header>
      <div className="border-t">
        {detail.source === "quick_performance" ? <QuickPerformanceBody detail={detail} /> : <RunReportBody detail={detail} paginate={false} />}
      </div>
      <div className="absolute inset-0 z-10 grid grid-cols-2 content-around overflow-hidden" data-report-watermark>
        {Array.from({ length: 8 }, (_, index) => <span key={index} className="-rotate-12 text-center text-4xl font-semibold text-muted-foreground/15">{label}</span>)}
      </div>
    </article>
  </div>
}

function SummaryValue({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0"><div className="text-[10px] text-muted-foreground">{label}</div><div className="mt-0.5 truncate font-medium tabular-nums" title={value}>{value}</div></div>
}

function InlineSummaryValue({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0 px-3 py-2 font-medium tabular-nums" title={`${label} ${value}`}>{label} {value}</div>
}

function SLOStatusBadge({ status }: { status: NonNullable<QuickPerformanceReport["slo_assessment"]>["status"] }) {
  return <Badge
    variant={status === "failed" ? "destructive" : "outline"}
    className={status === "passed" ? "border-success/25 bg-success-soft text-success-strong" : status === "not_evaluated" ? "border-warning/25 bg-warning-soft text-warning" : undefined}
  >{performanceSLOStatusLabel(status)}</Badge>
}

function CapacityRungTable({ report }: { report: QuickPerformanceReport }) {
  const { t: tx } = useTranslation()
  const capacity = report.capacity_result
  if (!capacity) return null
  const unit = report.profile.load_mode === "open_loop" ? "RPS" : tx("desktop:quick-test_concurrency")
  return <div className="mt-3 min-w-0 overflow-x-auto">
    <Table aria-label={tx("desktop:reports_capacity_ladder_results")} className="min-w-[1120px]">
      <TableHeader><TableRow className="hover:bg-transparent">
        <TableHead className="h-8 pl-3 text-[11px]">{tx("desktop:quick-test_step")}</TableHead>
        <TableHead className="h-8 text-[11px]">{tx("desktop:reports_target")}</TableHead>
        <TableHead className="h-8 text-[11px]">{tx("desktop:reports_transport")}</TableHead>
        <TableHead className="h-8 text-[11px]">SLO</TableHead>
        <TableHead className="h-8 text-[11px]">{tx("desktop:quick-test_good_requests")}</TableHead>
        <TableHead className="h-8 text-[11px]">{tx("desktop:reports_compliance")}</TableHead>
        <TableHead className="h-8 text-[11px]">Goodput</TableHead>
        <TableHead className="h-8 text-[11px]">TTFT P95</TableHead>
        <TableHead className="h-8 text-[11px]">TPOT P95</TableHead>
        <TableHead className="h-8 text-[11px]">E2E P95</TableHead>
        <TableHead className="h-8 pr-3 text-[11px]">{tx("desktop:reports_failure_reason")}</TableHead>
      </TableRow></TableHeader>
      <TableBody>{capacity.rungs.map((rung) => (
        <TableRow key={rung.index} className="h-9">
          <TableCell className="py-1 pl-3 text-xs tabular-nums">#{rung.index + 1}</TableCell>
          <TableCell className="py-1 text-xs font-medium tabular-nums">{formatMetric(rung.target)} {unit}</TableCell>
          <TableCell className="py-1 text-xs">{rung.success ? tx("desktop:quick-test_passed") : tx("desktop:quick-test_failed_301")}</TableCell>
          <TableCell className="py-1 text-xs">{shortSLOStatus(rung.slo_assessment.status)}</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{rung.slo_assessment.good_requests} / {rung.slo_assessment.total_requests}</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.slo_assessment.good_request_percent)}%</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatPerformanceInteger(rung.slo_assessment.goodput_qps)} req/s</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatPerformanceInteger(rung.metrics.ttft_p95_ms)} ms</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatPerformanceInteger(rung.metrics.tpot_p95_ms)} ms/token</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatPerformanceInteger(rung.metrics.e2e_p95_ms)} ms</TableCell>
          <TableCell className="py-1 pr-3 text-xs text-muted-foreground">{rung.failures.length ? rung.failures.map((failure) => `${translateExecutionError(failure.error_code)} ${failure.count}`).join(" · ") : "—"}</TableCell>
        </TableRow>
      ))}</TableBody>
    </Table>
  </div>
}

function ContextValue({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0"><dt className="text-[10px] text-muted-foreground">{label}</dt><dd className={`mt-0.5 truncate font-medium ${mono ? "font-mono text-[11px]" : "tabular-nums"}`} title={value}>{value}</dd></div>
}

function ReportInspector({ report, detail, detailError, exporting, exportError, watermark, onWatermarkChange, onExport, onCopyPNG, shortcuts }: {
  report: ReportSummary
  detail: ReportDetail | null
  detailError: string
  exporting: ReportExportFormat | "copy" | ""
  exportError: string
  watermark: string
  onWatermarkChange: (value: string) => void
  onExport: (format: ReportExportFormat) => Promise<void>
  onCopyPNG: () => Promise<void>
  shortcuts?: ReactNode
}) {
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const metrics = useMemo(() => detail?.source === "run" ? Object.entries(detail.report.metrics).slice(0, 8) : [], [detail])
  const quick = detail?.source === "quick_performance" ? detail.performance : null
  return <>
    <InspectorHeader title={displayReportVerdict(report, t)} subtitle={report.id} trailing={<ConclusionBadge passed={report.passed} verdict={report.verdict} status={report.run_status} />} />
    {shortcuts}
    <Separator />
    <dl className="space-y-1 px-4 py-2">
      <InspectorRow label={t("inspector.source")} value={t(report.source === "quick_performance" ? "inspector.quickPerformance" : "inspector.planExecution")} />{report.run_id ? <InspectorRow label={t("inspector.run")} value={`${t(`common:status.${report.run_status}`)} · ${report.run_id}`} /> : null}<InspectorRow label={t("inspector.plan")} value={displayReportPlan(report, t)} /><InspectorRow label={t("inspector.modelChannel")} value={`${report.model_name} · ${report.channel_name}`} /><InspectorRow label={t(report.source === "quick_performance" ? "inspector.requestConclusion" : "inspector.caseConclusion")} value={t("inspector.conclusionValue", { passed: report.passed_case_count, total: report.verified_case_count, failed: report.failed_case_count, observed: report.observed_case_count, indeterminate: report.indeterminate_case_count })} /><InspectorRow label={t("inspector.issues")} value={t("inspector.items", { count: report.issue_count })} /><InspectorRow label={t("inspector.generated")} value={formatTimestamp(report.generated_at, locale)} />
    </dl>
    <Separator />
    <ReportExportControls watermark={watermark} onWatermarkChange={onWatermarkChange} exporting={exporting} error={exportError} onExport={onExport} onCopyPNG={onCopyPNG} />
    {detailError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{detailError}</div> : null}
    {detail?.source === "run" ? <><Separator /><div className="px-4 py-3"><div className="text-[11px] font-semibold">{tx("desktop:reports_core_metrics")}</div><dl className="mt-2 space-y-1">{metrics.map(([name, value]) => <InspectorRow key={name} label={`${name} · ${value.samples} samples`} value={`${formatMetric(value.value)} ${value.unit}`} />)}</dl><div className="mt-3 text-[10px] text-muted-foreground">{detail.report.environment.os}/{detail.report.environment.arch} · {detail.report.environment.app_version} · {detail.report.environment.engine_version}</div></div></> : quick ? <><Separator /><dl className="space-y-1 px-4 py-3"><InspectorRow label={tx("desktop:reports_target")} value={quick.model_id} /><InspectorRow label={tx("desktop:quick-test_actual_send_rate")} value={optionalRequestRate(quick.metrics.launched_qps)} /><InspectorRow label={tx("desktop:quick-test_successful_request_throughput")} value={optionalRequestRate(quick.metrics.successful_request_qps)} /><InspectorRow label="TTFT P50 / P95" value={`${formatPerformanceInteger(quick.metrics.ttft_p50_ms)} / ${formatPerformanceInteger(quick.metrics.ttft_p95_ms)} ms`} /><InspectorRow label="TPOT P50 / P95" value={`${formatPerformanceInteger(quick.metrics.tpot_p50_ms)} / ${formatPerformanceInteger(quick.metrics.tpot_p95_ms)} ms/token`} /><InspectorRow label="E2E P50 / P95" value={`${formatPerformanceInteger(quick.metrics.e2e_p50_ms)} / ${formatPerformanceInteger(quick.metrics.e2e_p95_ms)} ms`} /></dl></> : null}
  </>
}

function ConclusionBadge({ passed, status, verdict }: { passed: boolean; status?: ReportSummary["run_status"]; verdict?: string }) {
  const { t } = useTranslation("reports")
  if (verdict === "observed" || verdict === "not_applicable") return <VerificationBadge status="not_applicable" />
  if (status === "cancelled") {
    return <Badge variant="outline" className="border-warning/25 bg-warning-soft text-warning-strong">{t("system.cancelled")}</Badge>
  }
  return <Badge variant="outline" className={passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>{t(passed ? "conclusion.passed" : "conclusion.failed")}</Badge>
}

function formatMetric(value: number, locale: string = desktopLocale()): string { return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value) }
function optionalRequestRate(value?: number): string { return value === undefined ? "—" : `${formatPerformanceInteger(value)} req/s` }
function performanceLoadMode(mode?: "fixed_concurrency" | "open_loop"): string { return mode === "open_loop" ? tx("desktop:quick-test_open_arrival_rps") : mode === "fixed_concurrency" ? tx("desktop:catalog_fixed_concurrency") : tx("desktop:reports_legacy_fixed_concurrency") }
type QuickPerformanceReport = Extract<ReportDetail, { source: "quick_performance" }>["performance"]
function performanceTargetRate(report: QuickPerformanceReport): string {
  const target = report.progress.capacity_target ?? report.profile.rate_per_second
  return report.profile.load_mode === "open_loop" && target !== undefined ? `${formatMetric(target)} req/s` : tx("desktop:quick-test_fixed_concurrency")
}
function formatSLOThresholds(thresholds: NonNullable<QuickPerformanceReport["slo_assessment"]>["thresholds"]): string {
  const enabled = [
    thresholds.ttft_ms > 0 ? `TTFT ≤ ${formatMetric(thresholds.ttft_ms)} ms` : "",
    thresholds.tpot_ms > 0 ? `TPOT ≤ ${formatMetric(thresholds.tpot_ms)} ms/token` : "",
    thresholds.e2e_ms > 0 ? `E2E ≤ ${formatMetric(thresholds.e2e_ms)} ms` : "",
  ].filter(Boolean)
  return enabled.join(" · ") || tx("desktop:reports_disabled")
}
function formatSLOViolations(violations: NonNullable<QuickPerformanceReport["slo_assessment"]>["violations"]): string {
  return tx("desktop:reports_transport_value_ttft_value_tpot_value_e2e_value", { value1: violations.transport, value2: violations.ttft, value3: violations.tpot, value4: violations.e2e })
}
function shortSLOStatus(status: NonNullable<QuickPerformanceReport["slo_assessment"]>["status"]): string {
  return status === "passed" ? tx("desktop:quick-test_passed") : status === "failed" ? tx("desktop:quick-test_failed_301") : tx("desktop:reports_not_evaluated")
}
function performanceArrivalPattern(report: QuickPerformanceReport): string {
  if (report.profile.arrival_pattern === "poisson") return tx("desktop:quick-test_poisson_arrivals")
  return tx("desktop:quick-test_constant_interval")
}

function PerformanceTimeSliceTable({ slices }: { slices: NonNullable<QuickPerformanceReport["time_slices"]> }) {
  const { t: tx } = useTranslation()
  return <div>
    <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
      <h4 className="text-xs font-semibold">{tx("desktop:quick-test_time_slices")}</h4>
      <span className="text-[10px] text-muted-foreground">{tx("desktop:reports_only_steady_state_windows_recorded_by_the_backend_are_listed")}</span>
    </div>
    <div className="min-w-0 overflow-x-auto">
      <Table aria-label={tx("desktop:quick-test_time_slices")} className="min-w-[1040px]">
        <TableHeader><TableRow className="hover:bg-transparent">
          <TableHead className="h-8 pl-3 text-[11px]">{tx("desktop:reports_slices")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:reports_window")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:reports_offered")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:reports_sent")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:quick-test_succeeded")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:quick-test_failed")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:reports_rejected")}</TableHead>
          <TableHead className="h-8 text-[11px]">{tx("desktop:reports_tokens_input_output_cached")}</TableHead>
          <TableHead className="h-8 text-[11px]">TTFT P95</TableHead>
          <TableHead className="h-8 text-[11px]">TPOT P95</TableHead>
          <TableHead className="h-8 pr-3 text-[11px]">E2E P95</TableHead>
        </TableRow></TableHeader>
        <TableBody>{slices.length ? slices.map((slice) => (
          <TableRow key={slice.slice_index} className="h-9">
            <TableCell className="py-1 pl-3 text-xs tabular-nums">#{slice.slice_index}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatSliceWindow(slice.start_ms, slice.end_ms, slice.partial)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.offered)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.launched)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.succeeded)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.failed)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.rejected)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatMetric(slice.prompt_tokens)} / {formatMetric(slice.completion_tokens)} / {formatMetric(slice.cached_tokens)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatSliceP95(slice.ttft)}</TableCell>
            <TableCell className="py-1 text-xs tabular-nums">{formatSliceP95(slice.tpot)}</TableCell>
            <TableCell className="py-1 pr-3 text-xs tabular-nums">{formatSliceP95(slice.e2e)}</TableCell>
          </TableRow>
        )) : <TableRow><TableCell colSpan={11} className="h-20 text-center text-xs text-muted-foreground">{tx("desktop:reports_no_valid_time_slices_recorded")}</TableCell></TableRow>}</TableBody>
      </Table>
    </div>
  </div>
}
function performanceWorkloadMode(report: QuickPerformanceReport): string {
  if (report.profile.workload_mode === "normal") return tx("desktop:quick-test_normal_distribution")
  return tx("desktop:quick-test_fixed_tokens")
}
function performanceSeed(report: QuickPerformanceReport): string {
  if (report.profile.random_seed === undefined) return tx("desktop:quick-test_not_used")
  return report.profile.random_seed > 0 ? formatMetric(report.profile.random_seed) : tx("desktop:quick-test_not_used")
}
function performanceSharedPrefix(report: QuickPerformanceReport): string {
  if (report.profile.shared_prefix_tokens === undefined) return "0 Token"
  return `${formatMetric(report.profile.shared_prefix_tokens)} Token`
}
function performanceWarmupConfiguration(report: QuickPerformanceReport): string {
  const requests = report.profile.warmup_requests ?? 0
  return requests > 0 ? tx("desktop:reports_value_requests", { value1: formatMetric(requests) }) : tx("desktop:reports_0_disabled")
}
function performanceRampConfiguration(report: QuickPerformanceReport): string {
  const durationMS = report.profile.ramp_duration_ms ?? 0
  if (durationMS === 0) return tx("desktop:reports_0_disabled")
  if (report.profile.load_mode === "fixed_concurrency") {
    return tx("desktop:reports_value_cap_value", { value1: formatDuration(durationMS), value2: formatMetric(report.profile.ramp_request_cap ?? 0) })
  }
  return tx("desktop:reports_value_10_linear_steps", { value1: formatDuration(durationMS) })
}
function performanceSliceConfiguration(report: QuickPerformanceReport): string {
  const durationMS = report.profile.slice_duration_ms ?? 0
  return durationMS > 0 ? formatDuration(durationMS) : tx("desktop:reports_0_disabled")
}
function formatRequestBudget(report: QuickPerformanceReport): string {
  const budget = report.request_budget
  if (!budget) return "—"
  return tx("desktop:reports_value_value_warmup_value_ramp_value_steady_state_value", { value1: formatMetric(budget.total_cap), value2: formatMetric(budget.limit), value3: formatMetric(budget.warmup_cap), value4: formatMetric(budget.ramp_cap), value5: formatMetric(budget.measured_cap) })
}
function formatTrafficSummary(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  const flags = `${traffic.capped ? tx("desktop:reports_cap_reached") : ""}${traffic.stopped ? tx("desktop:reports_stopped_early") : ""}`
  return tx("desktop:reports_value_value_completed_offered_value_sent_value_succeeded_value_failed", { value1: formatMetric(traffic.completed), value2: formatMetric(traffic.request_cap), value3: formatMetric(traffic.offered), value4: formatMetric(traffic.launched), value5: formatMetric(traffic.succeeded), value6: formatMetric(traffic.failed), value7: formatMetric(traffic.rejected), value8: flags })
}
function formatTrafficTokens(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  return `${formatMetric(traffic.prompt_tokens)} / ${formatMetric(traffic.completion_tokens)} / ${formatMetric(traffic.cached_tokens)}`
}
function formatTrafficDuration(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  return `${formatDuration(traffic.send_duration_ms)} / ${formatDuration(traffic.drain_duration_ms)} / ${formatDuration(traffic.total_duration_ms)}`
}
function formatRampTarget(report: QuickPerformanceReport): string {
  const ramp = report.ramp
  if (!ramp) return "—"
  const target = ramp.target_concurrency !== undefined
    ? tx("desktop:reports_target_concurrency_value", { value1: formatMetric(ramp.target_concurrency) })
    : tx("desktop:reports_target_value_req_s", { value1: formatMetric(ramp.target_rate_per_second ?? 0) })
  return tx("desktop:reports_value_linear_steps_value_value", { value1: formatMetric(ramp.steps), value2: target, value3: ramp.completed_window ? tx("desktop:reports_complete_window") : tx("desktop:reports_incomplete_window") })
}
function formatSliceWindow(startMS: number, endMS: number, partial: boolean): string {
  return `${formatMetric(startMS / 1_000)}–${formatMetric(endMS / 1_000)} s${partial ? tx("desktop:reports_partial") : ""}`
}
function formatSliceP95(latency: NonNullable<QuickPerformanceReport["time_slices"]>[number]["ttft"]): string {
  return latency.count === 0 ? "—" : `${formatPerformanceInteger(latency.p95_ms)} ms`
}
function performanceTargetRanges(report: QuickPerformanceReport): string | undefined {
  const inputTargets = report.samples.flatMap((sample) => sample.target_input_tokens === undefined ? [] : [sample.target_input_tokens])
  const outputTargets = report.samples.flatMap((sample) => sample.target_output_tokens === undefined ? [] : [sample.target_output_tokens])
  if (inputTargets.length === 0 && outputTargets.length === 0) return undefined
  return `${formatIntegerRange(inputTargets)} / ${formatIntegerRange(outputTargets)}`
}
function formatIntegerRange(values: number[]): string {
  if (values.length === 0) return "—"
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  return minimum === maximum ? formatMetric(minimum) : `${formatMetric(minimum)}–${formatMetric(maximum)}`
}
function formatDuration(valueMS: number): string { return valueMS >= 1_000 ? `${formatMetric(valueMS / 1_000)} s` : `${formatPerformanceInteger(valueMS)} ms` }
function performanceMode(requestCount: number, durationMS: number): string {
  return requestCount > 0 ? tx("desktop:reports_fixed_count_value_requests", { value1: formatMetric(requestCount) }) : tx("desktop:reports_duration_value", { value1: formatDuration(durationMS) })
}
function formatTimestamp(value: string, locale: string = desktopLocale()): string {
  return new Intl.DateTimeFormat(locale, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)).replaceAll("/", "-")
}

function displayReportVerdict(report: ReportSummary, t: (key: string) => string): string {
  const key = reportVerdictTranslationKey(report)
  return key ? t(key) : report.verdict
}

function displayReportPlan(report: ReportSummary, t: (key: string) => string): string {
  const key = reportPlanTranslationKey(report)
  return key ? t(key) : report.plan_name
}
