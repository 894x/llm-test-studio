import { useEffect, useMemo, useRef, useState } from "react"
import ArrowLeftIcon from "lucide-react/dist/esm/icons/arrow-left.mjs"

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

import type { ExportedReport, ReportDetail, ReportExportFormat, ReportSnapshot, ReportSummary } from "./data"

export function ReportWorkspace({ snapshot, preferredReportID, getDetail, exportReport, exportVisualReport = createVisualReportExport }: {
  snapshot: ReportSnapshot
  preferredReportID?: string
  getDetail: (reportId: string) => Promise<ReportDetail>
  exportReport: (reportId: string, format: ReportExportFormat, watermark: string) => Promise<ExportedReport>
  exportVisualReport?: typeof createVisualReportExport
}) {
  const [selectedID, setSelectedID] = useState(preferredReportID ?? "")
  const [viewingReportID, setViewingReportID] = useState(preferredReportID ?? "")
  const [detailState, setDetailState] = useState<{ reportID: string; detail?: ReportDetail; error?: string }>({ reportID: "" })
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [exportError, setExportError] = useState("")
  const [watermark, setWatermark] = useState("rhzs")
  const exportDocumentRef = useRef<HTMLElement>(null)
  const selected = snapshot.reports.find((report) => report.id === selectedID) ?? snapshot.reports[0]
  const selectedReportID = selected?.id ?? ""
  const detail = detailState.reportID === selectedReportID ? detailState.detail ?? null : null
  const detailError = detailState.reportID === selectedReportID ? detailState.error ?? "" : ""
  const isViewingReport = viewingReportID !== "" && viewingReportID === selectedReportID

  useEffect(() => {
    if (!selectedReportID) return
    let active = true
    void getDetail(selectedReportID).then(
      (value) => { if (active) setDetailState({ reportID: selectedReportID, detail: value }) },
      () => { if (active) setDetailState({ reportID: selectedReportID, error: "无法读取报告详情，请检查本地日志" }) },
    )
    return () => { active = false }
  }, [getDetail, selectedReportID])

  const handleExport = async (format: ReportExportFormat) => {
    if (!selected) return
    setExporting(format)
    setExportError("")
    try {
      if (format === "json") {
        downloadExport(await exportReport(selected.id, format, watermark))
      } else {
        if (!exportDocumentRef.current) throw new Error("report rendering unavailable")
        downloadVisualExport(await exportVisualReport(exportDocumentRef.current, format, selected.id))
      }
    } catch (error) {
      setExportError(publicDesktopOperationErrorMessage(
        error,
        `导出 ${format.toUpperCase()} 报告（${selected.verdict}）`,
        "报告导出失败，请检查本地日志",
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
      const ClipboardItemType = window.ClipboardItem
      if (!navigator.clipboard?.write || !ClipboardItemType) throw new Error("clipboard image unsupported")
      await navigator.clipboard.write([new ClipboardItemType({ [exported.mediaType]: exported.blob })])
    } catch {
      setExportError("当前系统无法复制 PNG，可使用 PNG 下载")
    } finally {
      setExporting("")
    }
  }

  return <>
    <PageFrame
      title={isViewingReport ? "报告详情" : "测试报告"}
      description={isViewingReport ? `查看 ${selected?.verdict ?? "报告"} 的指标与请求明细` : "查看 Go Core 封存的结论、指标、请求明细与同源导出"}
      count={isViewingReport ? undefined : `${snapshot.reports.length} 份报告`}
      actions={isViewingReport ? <Button variant="outline" size="sm" onClick={() => setViewingReportID("")}><ArrowLeftIcon />返回报告列表</Button> : undefined}
      inspector={selected ? (
        <ReportInspector report={selected} detail={detail} detailError={detailError} exporting={exporting} exportError={exportError} watermark={watermark} onWatermarkChange={setWatermark} onExport={handleExport} onCopyPNG={copyPNG} />
      ) : <EmptyInspector label="尚未选择报告" />}
      inspectorLabel="报告详情"
    >
      {snapshot.reports.length === 0 ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Empty><EmptyTitle>还没有测试报告</EmptyTitle><EmptyDescription>运行完成并封存结论后，报告会出现在这里。</EmptyDescription></Empty>
        </ScrollArea>
      ) : isViewingReport ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <ReportContent detail={detail} error={detailError} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col border-t">
          <ScrollArea className="min-h-0 flex-1">
            <Table aria-label="测试报告目录" className="min-w-[840px]">
              <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm"><TableRow className="hover:bg-transparent">
                <TableHead className="h-8 w-[88px] pl-4 text-[11px]">结论</TableHead><TableHead className="h-8 text-[11px]">报告 / 计划</TableHead><TableHead className="h-8 text-[11px]">目标</TableHead><TableHead className="h-8 text-[11px]">用例</TableHead><TableHead className="h-8 text-[11px]">生成时间</TableHead><TableHead className="h-8 w-[96px] pr-4 text-right text-[11px]">查看报告</TableHead>
              </TableRow></TableHeader>
              <TableBody>{snapshot.reports.map((report) => (
                <TableRow key={report.id} data-state={report.id === selected?.id ? "selected" : undefined} aria-selected={report.id === selected?.id} onClick={() => setSelectedID(report.id)} className="dense-table-row h-11">
                  <TableCell className="py-1 pl-4"><ConclusionBadge passed={report.passed} /></TableCell>
                  <TableCell className="py-1"><div className="max-w-[240px] truncate text-xs font-medium">{report.verdict}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.plan_name}</div></TableCell>
                  <TableCell className="py-1"><div className="truncate text-xs">{report.model_name}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.channel_name}</div></TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{report.case_count - report.failed_case_count}/{report.case_count}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{formatTimestamp(report.generated_at)}</TableCell>
                  <TableCell className="py-1 pr-4 text-right"><Button variant="outline" size="xs" aria-label={`查看报告：${report.verdict}`} onClick={(event) => { event.stopPropagation(); setSelectedID(report.id); setViewingReportID(report.id) }}>查看报告</Button></TableCell>
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
  if (error) return <div role="alert" className="border-t px-4 py-3 text-xs text-destructive">{error}</div>
  if (!detail) return <div className="border-t px-4 py-3 text-xs text-muted-foreground">正在读取请求明细…</div>
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
  return <>
      {detail.request_results.length > visibleResults.length ? <div role="status" className="border-b px-4 py-2 text-[11px] text-muted-foreground">当前显示前 1,000 条请求；完整 {detail.request_results.length.toLocaleString("zh-CN")} 条可导出 JSON。</div> : null}
      <Table aria-label="请求级结果" className="min-w-[900px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95"><TableRow>
          <TableHead className="h-8 pl-4 text-[11px]">请求</TableHead><TableHead className="h-8 text-[11px]">状态</TableHead><TableHead className="h-8 text-[11px]">E2E</TableHead><TableHead className="h-8 text-[11px]">TTFT</TableHead><TableHead className="h-8 text-[11px]">TPOT</TableHead><TableHead className="h-8 text-[11px]">排队</TableHead><TableHead className="h-8 text-[11px]">Token</TableHead><TableHead className="h-8 text-[11px]">错误</TableHead>
        </TableRow></TableHeader>
        <TableBody>{visibleResults.length ? visibleResults.map((result) => (
          <TableRow key={result.id} className="h-9">
            <TableCell className="py-1 pl-4 font-mono text-[10px]">{result.request_id ?? result.id}</TableCell><TableCell className="py-1"><ConclusionBadge passed={Object.values(result.success).every(Boolean)} /></TableCell>
            <MetricCell value={result.metrics.e2e_ms} unit="ms" /><MetricCell value={result.metrics.ttft_ms} unit="ms" /><MetricCell value={result.metrics.tpot_ms} unit="ms" /><MetricCell value={result.metrics.schedule_lag_ms} unit="ms" />
            <TableCell className="py-1 text-xs tabular-nums">{metric(result.metrics.prompt_tokens)} / {metric(result.metrics.completion_tokens)}</TableCell><TableCell className="py-1 text-xs text-destructive">{result.error_code ?? "—"}</TableCell>
          </TableRow>
        )) : <TableRow><TableCell colSpan={8} className="h-24 text-center text-xs text-muted-foreground">此报告没有请求级结果</TableCell></TableRow>}</TableBody>
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
  const report = detail.performance
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned)
  return <section aria-label="归档性能报告" className="space-y-4 p-4">
    <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
      <SummaryValue label={completion.label} value={completion.value} />
      <SummaryValue label="成功" value={String(report.metrics.succeeded)} />
      <SummaryValue label="失败" value={String(report.metrics.failed)} />
      <SummaryValue label="成功率" value={`${formatMetric(report.metrics.success_rate_percent)}%`} />
      <SummaryValue label="请求速率" value={`${formatMetric(report.metrics.request_qps)} req/s`} />
      <SummaryValue label="峰值在途" value={String(report.progress.peak_in_flight)} />
      <SummaryValue label="总耗时" value={`${formatMetric(report.progress.total_duration_ms / 1_000)} s`} />
      <SummaryValue label="RPM" value={formatMetric(report.metrics.rpm)} />
      <SummaryValue label="输入 TPM" value={`${formatMetric(report.metrics.input_tpm)} TPM`} />
      <SummaryValue label="输出 TPM" value={`${formatMetric(report.metrics.output_tpm)} TPM`} />
      <SummaryValue label="总 TPM" value={`${formatMetric(report.metrics.total_tpm)} TPM`} />
      <SummaryValue label="生成速度" value={`${formatMetric(report.metrics.generation_tps)} token/s`} />
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
  const label = watermark.trim() || "rhzs"
  return <div aria-hidden="true" className="pointer-events-none fixed left-[-10000px] top-0 z-[-1] w-[1200px]">
    <article ref={ref} data-report-export-document className="relative w-[1200px] overflow-hidden bg-background text-foreground">
      <header className="px-4 py-3">
        <h1 className="text-lg font-semibold tracking-tight">报告详情</h1>
        <p className="mt-1 text-[11px] text-muted-foreground">查看 {report.verdict} 的指标与请求明细</p>
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

function MetricCell({ value, unit }: { value?: number; unit: string }) {
  return <TableCell className="py-1 text-xs tabular-nums">{metric(value)} {value === undefined ? "" : unit}</TableCell>
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
  const metrics = useMemo(() => detail?.source === "run" ? Object.entries(detail.report.metrics).slice(0, 8) : [], [detail])
  const quick = detail?.source === "quick_performance" ? detail.performance : null
  return <ScrollArea className="h-full">
    <InspectorHeader title={report.verdict} subtitle={report.id} trailing={<ConclusionBadge passed={report.passed} />} />
    <Separator />
    <dl className="space-y-1 px-4 py-2">
      <InspectorRow label="来源" value={report.source === "quick_performance" ? "快速性能测试" : "执行计划"} />{report.run_id ? <InspectorRow label="运行" value={`${report.run_status} · ${report.run_id}`} /> : null}<InspectorRow label="测试计划" value={report.plan_name} /><InspectorRow label="模型与渠道" value={`${report.model_name} · ${report.channel_name}`} /><InspectorRow label={report.source === "quick_performance" ? "请求结论" : "用例结论"} value={`${report.case_count - report.failed_case_count}/${report.case_count} 通过 · ${report.failed_case_count} 失败`} /><InspectorRow label="问题" value={`${report.issue_count} 项`} /><InspectorRow label="生成时间" value={formatTimestamp(report.generated_at)} />
    </dl>
    <Separator />
    <div className="grid grid-cols-2 gap-2 px-4 py-3" aria-label="报告导出">
      <Field className="col-span-2 block space-y-1">
        <FieldLabel htmlFor="report-watermark">导出水印</FieldLabel>
        <Input id="report-watermark" value={watermark} maxLength={64} disabled={Boolean(exporting)} onChange={(event) => onWatermarkChange(event.target.value)} placeholder="rhzs" />
      </Field>
      {(["json", "html", "png", "pdf"] as const).map((format) => <Button key={format} variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onExport(format)}>{exporting === format ? "生成中…" : format.toUpperCase()}</Button>)}
      <Button className="col-span-2" variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onCopyPNG()}>{exporting === "copy" ? "复制中…" : "复制 PNG"}</Button>
    </div>
    {exportError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{exportError}</div> : null}
    {detailError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{detailError}</div> : null}
    {detail?.source === "run" ? <><Separator /><div className="px-4 py-3"><div className="text-[11px] font-semibold">核心指标</div><dl className="mt-2 space-y-1">{metrics.map(([name, value]) => <InspectorRow key={name} label={`${name} · ${value.samples} samples`} value={`${formatMetric(value.value)} ${value.unit}`} />)}</dl><div className="mt-3 text-[10px] text-muted-foreground">{detail.report.environment.os}/{detail.report.environment.arch} · {detail.report.environment.app_version} · {detail.report.environment.engine_version}</div></div></> : quick ? <><Separator /><dl className="space-y-1 px-4 py-3"><InspectorRow label="目标" value={quick.model_id} /><InspectorRow label="请求速率" value={`${formatMetric(quick.metrics.request_qps)} req/s`} /><InspectorRow label="TTFT P50 / P95" value={`${formatMetric(quick.metrics.ttft_p50_ms)} / ${formatMetric(quick.metrics.ttft_p95_ms)} ms`} /><InspectorRow label="TPOT P50 / P95" value={`${formatMetric(quick.metrics.tpot_p50_ms)} / ${formatMetric(quick.metrics.tpot_p95_ms)} ms/token`} /><InspectorRow label="E2E P50 / P95" value={`${formatMetric(quick.metrics.e2e_p50_ms)} / ${formatMetric(quick.metrics.e2e_p95_ms)} ms`} /></dl></> : null}
  </ScrollArea>
}

function ConclusionBadge({ passed }: { passed: boolean }) {
  return <Badge variant="outline" className={passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>{passed ? "通过" : "未通过"}</Badge>
}

function exportBlob(exported: ExportedReport): Blob {
  const binary = atob(exported.data_base64)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  return new Blob([bytes], { type: exported.media_type })
}

function downloadExport(exported: ExportedReport) {
  const url = URL.createObjectURL(exportBlob(exported))
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = exported.filename
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

function downloadVisualExport(exported: { filename: string; blob: Blob }) {
  const url = URL.createObjectURL(exported.blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = exported.filename
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

function metric(value?: number): string { return value === undefined ? "—" : formatMetric(value) }
function formatMetric(value: number): string { return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value) }
function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)).replaceAll("/", "-")
}
