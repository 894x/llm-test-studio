import { useEffect, useMemo, useRef, useState } from "react"
import ArrowLeftIcon from "lucide-react/dist/esm/icons/arrow-left.mjs"
import { useTranslation } from "react-i18next"

import { publicDesktopOperationErrorMessage } from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { EmptyInspector, InspectorHeader, InspectorRow, PageFrame } from "@/features/shell/page-frame"
import { performanceCompletion } from "@/features/quick-test/performance-summary"
import { PerformanceCharts } from "./performance-charts"
import { PerformanceLatencyTable } from "./performance-latency-table"
import { exportVisualReport as createVisualReportExport } from "./visual-report-export"

import { reportPlanTranslationKey, reportVerdictTranslationKey, type ExportedReport, type ReportDetail, type ReportExportFormat, type ReportSnapshot, type ReportSummary } from "./data"

export function ReportWorkspace({ snapshot, preferredReportID, getDetail, exportReport, saveReportExport, copyReportPNG, exportVisualReport = createVisualReportExport }: {
  snapshot: ReportSnapshot
  preferredReportID?: string
  getDetail: (reportId: string) => Promise<ReportDetail>
  exportReport: (reportId: string, format: ReportExportFormat, watermark: string, locale: string) => Promise<ExportedReport>
  saveReportExport: (filename: string, mediaType: string, dataBase64: string, locale: string) => Promise<boolean>
  copyReportPNG: (dataBase64: string) => Promise<void>
  exportVisualReport?: typeof createVisualReportExport
}) {
  const { t, i18n } = useTranslation("reports")
  const [selectedID, setSelectedID] = useState(preferredReportID ?? "")
  const [viewingReportID, setViewingReportID] = useState(preferredReportID ?? "")
  const [detailState, setDetailState] = useState<{ reportID: string; detail?: ReportDetail; error?: boolean }>({ reportID: "" })
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [exportError, setExportError] = useState("")
  const [watermark, setWatermark] = useState("rhzs")
  const exportDocumentRef = useRef<HTMLElement>(null)
  const selected = snapshot.reports.find((report) => report.id === selectedID) ?? snapshot.reports[0]
  const selectedReportID = selected?.id ?? ""
  const detail = detailState.reportID === selectedReportID ? detailState.detail ?? null : null
  const detailError = detailState.reportID === selectedReportID && detailState.error ? t("detailError") : ""
  const isViewingReport = viewingReportID !== "" && viewingReportID === selectedReportID
  const selectedVerdict = selected ? displayReportVerdict(selected, t) : t("generic")

  useEffect(() => {
    if (!selectedReportID) return
    let active = true
    void getDetail(selectedReportID).then(
      (value) => { if (active) setDetailState({ reportID: selectedReportID, detail: value }) },
      () => { if (active) setDetailState({ reportID: selectedReportID, error: true }) },
    )
    return () => { active = false }
  }, [getDetail, selectedReportID])

  const handleExport = async (format: ReportExportFormat) => {
    if (!selected) return
    setExporting(format)
    setExportError("")
    try {
      if (format === "json") {
        const exported = await exportReport(selected.id, format, watermark, i18n.resolvedLanguage ?? i18n.language)
        await saveReportExport(exported.filename, exported.media_type, exported.data_base64, i18n.resolvedLanguage ?? i18n.language)
      } else {
        if (!exportDocumentRef.current) throw new Error("report rendering unavailable")
        const exported = await exportVisualReport(exportDocumentRef.current, format, selected.id)
        await saveReportExport(exported.filename, exported.mediaType, await blobToBase64(exported.blob), i18n.resolvedLanguage ?? i18n.language)
      }
    } catch (error) {
      setExportError(publicDesktopOperationErrorMessage(
        error,
        t("export.operation", { format: format.toUpperCase(), name: selectedVerdict }),
        t("export.error"),
      ))
    } finally {
      setExporting("")
    }
  }

  const copyPNG = async () => {
    if (!selected) return
    setExporting("copy")
    setExportError("")
    try {
      if (!exportDocumentRef.current) throw new Error("report rendering unavailable")
      const exported = await exportVisualReport(exportDocumentRef.current, "png", selected.id)
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
      setExporting("")
    }
  }

  return <>
    <PageFrame
      title={t(isViewingReport ? "detailTitle" : "title")}
      description={isViewingReport ? t("detailDescription", { name: selectedVerdict }) : t("description")}
      count={isViewingReport ? undefined : t("count", { count: snapshot.reports.length })}
      actions={isViewingReport ? <Button variant="outline" size="sm" onClick={() => setViewingReportID("")}><ArrowLeftIcon />{t("back")}</Button> : undefined}
      inspector={selected ? (
        <ReportInspector report={selected} detail={detail} detailError={detailError} exporting={exporting} exportError={exportError} watermark={watermark} onWatermarkChange={setWatermark} onExport={handleExport} onCopyPNG={copyPNG} />
      ) : <EmptyInspector label={t("noneSelected")} />}
      inspectorLabel={t("detailTitle")}
    >
      {snapshot.reports.length === 0 ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Empty><EmptyTitle>{t("empty")}</EmptyTitle><EmptyDescription>{t("emptyHint")}</EmptyDescription></Empty>
        </ScrollArea>
      ) : isViewingReport ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <ReportContent detail={detail} error={detailError} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col border-t">
          <ScrollArea className="min-h-0 flex-1">
            <Table aria-label={t("catalogAria")} className="min-w-[840px]">
              <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm"><TableRow className="hover:bg-transparent">
                <TableHead className="h-8 w-[88px] pl-4 text-[11px]">{t("columns.verdict")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.reportPlan")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.target")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.cases")}</TableHead><TableHead className="h-8 text-[11px]">{t("columns.generated")}</TableHead><TableHead className="h-8 w-[96px] pr-4 text-right text-[11px]">{t("columns.view")}</TableHead>
              </TableRow></TableHeader>
              <TableBody>{snapshot.reports.map((report) => (
                <TableRow key={report.id} data-state={report.id === selected?.id ? "selected" : undefined} aria-selected={report.id === selected?.id} onClick={() => setSelectedID(report.id)} className="dense-table-row h-11">
                  <TableCell className="py-1 pl-4"><ConclusionBadge passed={report.passed} /></TableCell>
                  <TableCell className="py-1"><div className="max-w-[240px] truncate text-xs font-medium">{displayReportVerdict(report, t)}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{displayReportPlan(report, t)}</div></TableCell>
                  <TableCell className="py-1"><div className="truncate text-xs">{report.model_name}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.channel_name}</div></TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{report.case_count - report.failed_case_count}/{report.case_count}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{formatTimestamp(report.generated_at, i18n.resolvedLanguage ?? i18n.language)}</TableCell>
                  <TableCell className="py-1 pr-4 text-right"><Button variant="outline" size="xs" aria-label={t("viewAria", { name: displayReportVerdict(report, t) })} onClick={(event) => { event.stopPropagation(); setSelectedID(report.id); setViewingReportID(report.id) }}>{t("view")}</Button></TableCell>
                </TableRow>
              ))}</TableBody>
            </Table>
          </ScrollArea>
        </div>
      )}
    </PageFrame>
    {selected && detail ? <ReportExportSurface ref={exportDocumentRef} report={selected} detail={detail} watermark={watermark} /> : null}
  </>
}

function ReportContent({ detail, error }: { detail: ReportDetail | null; error: string }) {
  const { t } = useTranslation("reports")
  if (error) return <div role="alert" className="border-t px-4 py-3 text-xs text-destructive">{error}</div>
  if (!detail) return <div className="border-t px-4 py-3 text-xs text-muted-foreground">{t("detailLoading")}</div>
  if (detail.source === "quick_performance") return <QuickPerformanceDetail detail={detail} />
  return <RunReportDetail detail={detail} />
}

function RunReportDetail({ detail }: { detail: Extract<ReportDetail, { source: "run" }> }) {
  const visibleResults = detail.request_results.slice(0, 1000)
  return (
    <ScrollArea className="min-h-[180px] flex-[3] border-t">
      <RunReportBody detail={detail} visibleResults={visibleResults} />
    </ScrollArea>
  )
}

function RunReportBody({ detail, visibleResults = detail.request_results.slice(0, 1000) }: {
  detail: Extract<ReportDetail, { source: "run" }>
  visibleResults?: Extract<ReportDetail, { source: "run" }>["request_results"]
}) {
  const { t, i18n } = useTranslation("reports")
  return <>
      {detail.request_results.length > visibleResults.length ? <div role="status" className="border-b px-4 py-2 text-[11px] text-muted-foreground">{t("request.truncated", { count: detail.request_results.length.toLocaleString(i18n.resolvedLanguage ?? i18n.language) })}</div> : null}
      <Table aria-label={t("request.aria")} className="min-w-[900px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95"><TableRow>
          <TableHead className="h-8 pl-4 text-[11px]">{t("request.request")}</TableHead><TableHead className="h-8 text-[11px]">{t("request.status")}</TableHead><TableHead className="h-8 text-[11px]">E2E</TableHead><TableHead className="h-8 text-[11px]">TTFT</TableHead><TableHead className="h-8 text-[11px]">TPOT</TableHead><TableHead className="h-8 text-[11px]">{t("request.queue")}</TableHead><TableHead className="h-8 text-[11px]">Token</TableHead><TableHead className="h-8 text-[11px]">{t("request.error")}</TableHead>
        </TableRow></TableHeader>
        <TableBody>{visibleResults.length ? visibleResults.map((result) => (
          <TableRow key={result.id} className="h-9">
            <TableCell className="py-1 pl-4 font-mono text-[10px]">{result.request_id ?? result.id}</TableCell><TableCell className="py-1"><ConclusionBadge passed={Object.values(result.success).every(Boolean)} /></TableCell>
            <MetricCell value={result.metrics.e2e_ms} unit="ms" /><MetricCell value={result.metrics.ttft_ms} unit="ms" /><MetricCell value={result.metrics.tpot_ms} unit="ms" /><MetricCell value={result.metrics.schedule_lag_ms} unit="ms" />
            <TableCell className="py-1 text-xs tabular-nums">{metric(result.metrics.prompt_tokens)} / {metric(result.metrics.completion_tokens)}</TableCell><TableCell className="py-1 text-xs text-destructive">{result.error_code ?? "—"}</TableCell>
          </TableRow>
        )) : <TableRow><TableCell colSpan={8} className="h-24 text-center text-xs text-muted-foreground">{t("request.empty")}</TableCell></TableRow>}</TableBody>
      </Table>
    </>
}

function QuickPerformanceDetail({ detail }: { detail: Extract<ReportDetail, { source: "quick_performance" }> }) {
  return (
    <ScrollArea className="min-h-[260px] flex-[3] border-t">
      <QuickPerformanceBody detail={detail} />
    </ScrollArea>
  )
}

function QuickPerformanceBody({ detail }: { detail: Extract<ReportDetail, { source: "quick_performance" }> }) {
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const format = (value: number) => formatMetric(value, locale)
  const report = detail.performance
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned, (key) => i18n.t(`quickTest:${key}`))
  return <section aria-label={t("performance.aria")} className="space-y-4 p-4">
    <div>
      <h4 className="mb-2 text-xs font-semibold">{t("performance.config")}</h4>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <ContextValue label={t("performance.model")} value={report.model_id} />
        <ContextValue label={t("performance.endpoint")} value={report.endpoint} mono />
        <ContextValue label={t("performance.mode")} value={performanceMode(report.profile.request_count, report.profile.duration_ms, t, locale)} />
        <ContextValue label={t("performance.concurrency")} value={format(report.profile.concurrency)} />
        <ContextValue label={t("performance.timeout")} value={formatDuration(report.profile.timeout_ms, locale)} />
        <ContextValue label={t("performance.tokens")} value={`${format(report.profile.input_tokens)} / ${format(report.profile.output_tokens)}`} />
      </dl>
    </div>
    <Separator />
    <div>
      <h4 className="mb-2 text-xs font-semibold">{t("performance.results")}</h4>
      <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <SummaryValue label={completion.label} value={completion.value} />
        <SummaryValue label={t("performance.success")} value={String(report.metrics.succeeded)} />
        <SummaryValue label={t("performance.failed")} value={String(report.metrics.failed)} />
        <SummaryValue label={t("performance.successRate")} value={`${format(report.metrics.success_rate_percent)}%`} />
        <SummaryValue label={t("performance.requestRate")} value={`${format(report.metrics.request_qps)} req/s`} />
        <SummaryValue label={t("performance.peak")} value={`${report.progress.peak_in_flight} / ${report.profile.concurrency}`} />
        <SummaryValue label={t("performance.totalDuration")} value={`${format(report.progress.total_duration_ms / 1_000)} s`} />
        <SummaryValue label="RPM" value={format(report.metrics.rpm)} />
        <SummaryValue label={t("performance.inputTpm")} value={`${format(report.metrics.input_tpm)} TPM`} />
        <SummaryValue label={t("performance.outputTpm")} value={`${format(report.metrics.output_tpm)} TPM`} />
        <SummaryValue label={t("performance.totalTpm")} value={`${format(report.metrics.total_tpm)} TPM`} />
        <SummaryValue label={t("performance.outputThroughput")} value={`${format(report.metrics.generation_tps)} token/s`} />
      </div>
    </div>
    <PerformanceLatencyTable metrics={report.metrics} />
    <PerformanceCharts samples={report.samples} percentiles={report.metrics} />
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
        {detail.source === "quick_performance" ? <QuickPerformanceBody detail={detail} /> : <RunReportBody detail={detail} />}
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

function ContextValue({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0"><dt className="text-[10px] text-muted-foreground">{label}</dt><dd className={`mt-0.5 truncate font-medium ${mono ? "font-mono text-[11px]" : "tabular-nums"}`} title={value}>{value}</dd></div>
}

function MetricCell({ value, unit }: { value?: number; unit: string }) {
  const { i18n } = useTranslation()
  return <TableCell className="py-1 text-xs tabular-nums">{metric(value, i18n.resolvedLanguage ?? i18n.language)} {value === undefined ? "" : unit}</TableCell>
}

function ReportInspector({ report, detail, detailError, exporting, exportError, watermark, onWatermarkChange, onExport, onCopyPNG }: {
  report: ReportSummary
  detail: ReportDetail | null
  detailError: string
  exporting: ReportExportFormat | "copy" | ""
  exportError: string
  watermark: string
  onWatermarkChange: (value: string) => void
  onExport: (format: ReportExportFormat) => Promise<void>
  onCopyPNG: () => Promise<void>
}) {
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const metrics = useMemo(() => detail?.source === "run" ? Object.entries(detail.report.metrics).slice(0, 8) : [], [detail])
  const quick = detail?.source === "quick_performance" ? detail.performance : null
  return <ScrollArea className="h-full">
    <InspectorHeader title={displayReportVerdict(report, t)} subtitle={report.id} trailing={<ConclusionBadge passed={report.passed} />} />
    <Separator />
    <dl className="space-y-1 px-4 py-2">
      <InspectorRow label={t("inspector.source")} value={t(report.source === "quick_performance" ? "inspector.quickPerformance" : "inspector.planExecution")} />{report.run_id ? <InspectorRow label={t("inspector.run")} value={`${t(`common:status.${report.run_status}`)} · ${report.run_id}`} /> : null}<InspectorRow label={t("inspector.plan")} value={displayReportPlan(report, t)} /><InspectorRow label={t("inspector.modelChannel")} value={`${report.model_name} · ${report.channel_name}`} /><InspectorRow label={t(report.source === "quick_performance" ? "inspector.requestConclusion" : "inspector.caseConclusion")} value={t("inspector.conclusionValue", { passed: report.case_count - report.failed_case_count, total: report.case_count, failed: report.failed_case_count })} /><InspectorRow label={t("inspector.issues")} value={t("inspector.items", { count: report.issue_count })} /><InspectorRow label={t("inspector.generated")} value={formatTimestamp(report.generated_at, locale)} />
    </dl>
    <Separator />
    <div className="grid grid-cols-2 gap-2 px-4 py-3" aria-label={t("inspector.exportAria")}>
      <Field className="col-span-2 block space-y-1">
        <FieldLabel htmlFor="report-watermark">{t("inspector.watermark")}</FieldLabel>
        <Input id="report-watermark" value={watermark} maxLength={64} disabled={Boolean(exporting)} onChange={(event) => onWatermarkChange(event.target.value)} placeholder="rhzs" />
      </Field>
      {(["json", "html", "png", "pdf"] as const).map((format) => <Button key={format} variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onExport(format)}>{exporting === format ? t("inspector.generating") : format.toUpperCase()}</Button>)}
      <Button className="col-span-2" variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onCopyPNG()}>{t(exporting === "copy" ? "inspector.copying" : "inspector.copyPng")}</Button>
    </div>
    {exportError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{exportError}</div> : null}
    {detailError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{detailError}</div> : null}
    {detail?.source === "run" ? <><Separator /><div className="px-4 py-3"><div className="text-[11px] font-semibold">{t("inspector.coreMetrics")}</div><dl className="mt-2 space-y-1">{metrics.map(([name, value]) => <InspectorRow key={name} label={`${name} · ${value.samples} ${t("inspector.samples")}`} value={`${formatMetric(value.value, locale)} ${value.unit}`} />)}</dl><div className="mt-3 text-[10px] text-muted-foreground">{detail.report.environment.os}/{detail.report.environment.arch} · {detail.report.environment.app_version} · {detail.report.environment.engine_version}</div></div></> : quick ? <><Separator /><dl className="space-y-1 px-4 py-3"><InspectorRow label={t("inspector.target")} value={quick.model_id} /><InspectorRow label={t("performance.requestRate")} value={`${formatMetric(quick.metrics.request_qps, locale)} req/s`} /><InspectorRow label="TTFT P50 / P95" value={`${formatMetric(quick.metrics.ttft_p50_ms, locale)} / ${formatMetric(quick.metrics.ttft_p95_ms, locale)} ms`} /><InspectorRow label="TPOT P50 / P95" value={`${formatMetric(quick.metrics.tpot_p50_ms, locale)} / ${formatMetric(quick.metrics.tpot_p95_ms, locale)} ms/token`} /><InspectorRow label="E2E P50 / P95" value={`${formatMetric(quick.metrics.e2e_p50_ms, locale)} / ${formatMetric(quick.metrics.e2e_p95_ms, locale)} ms`} /></dl></> : null}
  </ScrollArea>
}

function ConclusionBadge({ passed }: { passed: boolean }) {
  const { t } = useTranslation("reports")
  return <Badge variant="outline" className={passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>{t(passed ? "conclusion.passed" : "conclusion.failed")}</Badge>
}

function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error("report export encoding failed"))
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("report export encoding failed"))
        return
      }
      const separator = reader.result.indexOf(",")
      if (separator < 0) {
        reject(new Error("report export encoding failed"))
        return
      }
      resolve(reader.result.slice(separator + 1))
    }
    reader.readAsDataURL(blob)
  })
}

function metric(value?: number, locale = "zh-CN"): string { return value === undefined ? "—" : formatMetric(value, locale) }
function formatMetric(value: number, locale = "zh-CN"): string { return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value) }
function formatDuration(valueMS: number, locale = "zh-CN"): string { return valueMS >= 1_000 ? `${formatMetric(valueMS / 1_000, locale)} s` : `${formatMetric(valueMS, locale)} ms` }
function performanceMode(requestCount: number, durationMS: number, t: (key: string, values?: Record<string, unknown>) => string, locale: string): string {
  return requestCount > 0 ? t("performance.fixed", { count: formatMetric(requestCount, locale) }) : t("performance.duration", { duration: formatDuration(durationMS, locale) })
}
function formatTimestamp(value: string, locale = "zh-CN"): string {
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
