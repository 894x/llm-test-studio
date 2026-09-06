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
import { QUICK_TEST_ERROR_MESSAGES } from "@/features/quick-test/data"
import {
  performanceCapacitySummary,
  performanceCompletion,
  performanceSLOStatusLabel,
} from "@/features/quick-test/performance-summary"
import { QuickPerformanceRequestAnalysis } from "@/features/quick-test/quick-performance-request-analysis"
import { PerformanceCharts } from "./performance-charts"
import { PerformanceLatencyTable } from "./performance-latency-table"
import { PerformanceStreamingTimingTable } from "./performance-streaming-timing-table"
import { exportVisualReport as createVisualReportExport } from "./visual-report-export"

import type { ExportedReport, ReportDetail, ReportExportFormat, ReportSnapshot, ReportSummary, ResponseProbeDistribution } from "./data"

export function ReportWorkspace({ snapshot, preferredReportID, getDetail, exportReport, saveReportExport, copyReportPNG, exportVisualReport = createVisualReportExport }: {
  snapshot: ReportSnapshot
  preferredReportID?: string
  getDetail: (reportId: string) => Promise<ReportDetail>
  exportReport: (reportId: string, format: ReportExportFormat, watermark: string) => Promise<ExportedReport>
  saveReportExport: (filename: string, mediaType: string, dataBase64: string) => Promise<boolean>
  copyReportPNG: (dataBase64: string) => Promise<void>
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
        const exported = await exportReport(selected.id, format, watermark)
        await saveReportExport(exported.filename, exported.media_type, exported.data_base64)
      } else {
        if (!exportDocumentRef.current) throw new Error("report rendering unavailable")
        const exported = await exportVisualReport(exportDocumentRef.current, format, selected.id)
        await saveReportExport(exported.filename, exported.mediaType, await blobToBase64(exported.blob))
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
        `复制 PNG 报告（${selected.verdict}）`,
        "无法写入系统剪贴板，请检查本地日志",
      ))
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
  const probeDistributions = detail.report.probe_distributions ?? []
  return <>
      {probeDistributions.length ? <ResponseProbeDistributionTable distributions={probeDistributions} /> : null}
      {detail.request_results.length > visibleResults.length ? <div role="status" className="border-b px-4 py-2 text-[11px] text-muted-foreground">当前显示前 1,000 条请求；完整 {detail.request_results.length.toLocaleString("zh-CN")} 条可导出 JSON。</div> : null}
      <Table aria-label="请求级结果" className="min-w-[900px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95"><TableRow>
          <TableHead className="h-8 pl-4 text-[11px]">请求</TableHead><TableHead className="h-8 text-[11px]">阶段</TableHead><TableHead className="h-8 text-[11px]">状态</TableHead><TableHead className="h-8 text-[11px]">E2E</TableHead><TableHead className="h-8 text-[11px]">TTFT</TableHead><TableHead className="h-8 text-[11px]">TPOT</TableHead><TableHead className="h-8 text-[11px]">排队</TableHead><TableHead className="h-8 text-[11px]">Token</TableHead><TableHead className="h-8 text-[11px]">错误</TableHead>
        </TableRow></TableHeader>
        <TableBody>{visibleResults.length ? visibleResults.map((result) => (
          <TableRow key={result.id} className="h-9">
            <TableCell className="py-1 pl-4 font-mono text-[10px]">{result.request_id ?? result.id}</TableCell><TableCell className="py-1 text-[10px] tabular-nums">{requestDimensionLabel(result.dimensions)}</TableCell><TableCell className="py-1"><ConclusionBadge passed={Object.values(result.success).every(Boolean)} /></TableCell>
            <MetricCell value={result.metrics.e2e_ms} unit="ms" /><MetricCell value={result.metrics.ttft_ms} unit="ms" /><MetricCell value={result.metrics.tpot_ms} unit="ms" /><MetricCell value={result.metrics.schedule_lag_ms} unit="ms" />
            <TableCell className="py-1 text-xs tabular-nums">{metric(result.metrics.prompt_tokens)} / {metric(result.metrics.completion_tokens)}</TableCell><TableCell className="py-1 text-xs text-destructive">{result.error_code ?? "—"}</TableCell>
          </TableRow>
        )) : <TableRow><TableCell colSpan={9} className="h-24 text-center text-xs text-muted-foreground">此报告没有请求级结果</TableCell></TableRow>}</TableBody>
      </Table>
    </>
}

function QuickPerformanceDetail({ detail }: { detail: Extract<ReportDetail, { source: "quick_performance" }> }) {
  return (
    <ScrollArea className="min-h-[260px] flex-[3] border-t">
      <QuickPerformanceBody detail={detail} includeRequestAnalysis />
    </ScrollArea>
  )
}

function ResponseProbeDistributionTable({ distributions }: { distributions: ResponseProbeDistribution[] }) {
  return <section aria-label="上游响应探测统计" className="border-b px-4 py-3">
    <div className="mb-2">
      <h4 className="text-xs font-semibold">上游响应分布</h4>
      <p className="mt-0.5 text-[10px] text-muted-foreground">按响应体签名与结构指纹聚合；unknown 表示未命中已配置规则，并不等同于请求失败。</p>
    </div>
    <Table aria-label="上游响应分布" className="min-w-[720px] rounded-md border">
      <TableHeader><TableRow>
        <TableHead className="h-8 text-[11px]">分类标签</TableHead><TableHead className="h-8 text-[11px]">判定</TableHead><TableHead className="h-8 text-[11px]">格式</TableHead><TableHead className="h-8 text-[11px]">结构指纹</TableHead><TableHead className="h-8 text-right text-[11px]">请求数</TableHead><TableHead className="h-8 pr-4 text-right text-[11px]">占比</TableHead>
      </TableRow></TableHeader>
      <TableBody>{distributions.map((distribution) => <TableRow key={`${distribution.case_id}:${distribution.bucket}:${distribution.shape}`} className="h-9">
        <TableCell className="py-1 text-xs font-medium">{distribution.bucket}</TableCell>
        <TableCell className="py-1"><ProbeClassificationBadge classification={distribution.classification} /></TableCell>
        <TableCell className="py-1 font-mono text-[10px]">{distribution.format || "—"}</TableCell>
        <TableCell className="py-1 font-mono text-[10px]">{distribution.shape || "—"}</TableCell>
        <TableCell className="py-1 text-right text-xs tabular-nums">{distribution.count.toLocaleString("zh-CN")}</TableCell>
        <TableCell className="py-1 pr-4 text-right text-xs tabular-nums">{formatMetric(distribution.share_percent)}%</TableCell>
      </TableRow>)}</TableBody>
    </Table>
  </section>
}

function ProbeClassificationBadge({ classification }: { classification: ResponseProbeDistribution["classification"] }) {
  const label = classification === "matched" ? "已匹配" : classification === "unknown" ? "未知格式" : classification === "ambiguous" ? "规则歧义" : "请求失败"
  const tone = classification === "matched" ? "border-success/25 bg-success-soft text-success-strong"
    : classification === "failed" ? "border-destructive/25 bg-destructive/5 text-destructive"
    : "border-warning/30 bg-warning-soft text-warning-strong"
  return <Badge variant="outline" className={tone}>{label}</Badge>
}

function requestDimensionLabel(dimensions?: Record<string, string>): string {
  if (dimensions?.probe_bucket) return `${dimensions.probe_bucket} · ${dimensions.probe_classification ?? "—"}`
  if (dimensions?.input_tokens_target) return `${dimensions.input_tokens_target} token · #${dimensions.sample ?? "—"}/${dimensions.stage_samples ?? "—"}`
  return "—"
}

function QuickPerformanceBody({ detail, includeRequestAnalysis = false }: {
  detail: Extract<ReportDetail, { source: "quick_performance" }>
  includeRequestAnalysis?: boolean
}) {
  const report = detail.performance
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned)
  const targetRanges = performanceTargetRanges(report)
  const hasSLOCapacityData = report.slo_assessment !== undefined || report.capacity_result !== undefined
  const hasPhaseThreeData = report.request_budget !== undefined || report.warmup !== undefined || report.ramp !== undefined || report.time_slices !== undefined ||
    report.profile.warmup_requests !== undefined || report.profile.ramp_duration_ms !== undefined || report.profile.ramp_request_cap !== undefined || report.profile.slice_duration_ms !== undefined
  return <section aria-label="归档性能报告" className="space-y-4 p-4">
    <div>
      <h4 className="mb-2 text-xs font-semibold">测试配置</h4>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <ContextValue label="模型" value={report.model_id} />
        <ContextValue label="接口地址" value={report.endpoint} mono />
        <ContextValue label="负载模式" value={performanceLoadMode(report.profile.load_mode)} />
        {report.profile.load_mode === "open_loop" ? <ContextValue label="到达分布" value={performanceArrivalPattern(report)} /> : null}
        <ContextValue label="工作负载" value={performanceWorkloadMode(report)} />
        <ContextValue label="停止条件" value={performanceMode(report.profile.request_count, report.profile.duration_ms)} />
        <ContextValue label={report.profile.load_mode === "open_loop" ? "最大在途" : "配置并发"} value={formatMetric(report.profile.load_mode === "open_loop" ? (report.profile.max_in_flight ?? 0) : report.profile.concurrency)} />
        <ContextValue label="请求超时" value={formatDuration(report.profile.timeout_ms)} />
        <ContextValue label={report.profile.workload_mode === "normal" ? "Token 均值（输入 / 输出）" : "Token 目标（输入 / 输出）"} value={`${formatMetric(report.profile.input_tokens)} / ${formatMetric(report.profile.output_tokens)}`} />
        {report.profile.workload_mode === "normal" ? <ContextValue label="Token 标准差（输入 / 输出）" value={`${formatMetric(report.profile.input_tokens_stddev ?? 0)} / ${formatMetric(report.profile.output_tokens_stddev ?? 0)}`} /> : null}
        <ContextValue label="随机种子" value={performanceSeed(report)} />
        <ContextValue label="共享前缀" value={performanceSharedPrefix(report)} />
        {targetRanges ? <ContextValue label="采样目标范围（输入 / 输出）" value={targetRanges} /> : null}
        {hasPhaseThreeData ? <ContextValue label="热身请求" value={performanceWarmupConfiguration(report)} /> : null}
        {hasPhaseThreeData ? <ContextValue label="爬坡配置" value={performanceRampConfiguration(report)} /> : null}
        {hasPhaseThreeData ? <ContextValue label="切片粒度" value={performanceSliceConfiguration(report)} /> : null}
      </dl>
    </div>
    {hasSLOCapacityData ? (
      <>
        <Separator />
        <div>
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <h4 className="text-xs font-semibold">SLO 与容量</h4>
            <div className="flex flex-wrap gap-1.5">
              <Badge variant="outline" className={report.success ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>
                传输与协议{report.success ? "通过" : "未通过"}
              </Badge>
              {report.slo_assessment ? <SLOStatusBadge status={report.slo_assessment.status} /> : null}
            </div>
          </div>
          {report.slo_assessment ? (
            <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
              <InlineSummaryValue label="阈值" value={formatSLOThresholds(report.slo_assessment.thresholds)} />
              <InlineSummaryValue label="目标达标率" value={`${formatMetric(report.slo_assessment.target_percent)}%`} />
              <InlineSummaryValue label="好请求" value={`${formatMetric(report.slo_assessment.good_requests)} / ${formatMetric(report.slo_assessment.total_requests)}`} />
              <InlineSummaryValue label="实际达标率" value={`${formatMetric(report.slo_assessment.good_request_percent)}%`} />
              <InlineSummaryValue label="Goodput" value={`${formatMetric(report.slo_assessment.goodput_qps)} req/s`} />
              <InlineSummaryValue label="违反" value={formatSLOViolations(report.slo_assessment.violations)} />
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
          <h4 className="mb-2 text-xs font-semibold">准备阶段与预算</h4>
          <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
            {report.request_budget ? <SummaryValue label="请求预算" value={formatRequestBudget(report)} /> : null}
            {report.warmup ? <SummaryValue label="热身流量" value={formatTrafficSummary(report.warmup)} /> : null}
            {report.warmup ? <SummaryValue label="热身 Token（输入 / 输出 / 缓存）" value={formatTrafficTokens(report.warmup)} /> : null}
            {report.ramp ? <SummaryValue label="爬坡流量" value={formatTrafficSummary(report.ramp.traffic)} /> : null}
            {report.ramp ? <SummaryValue label="爬坡目标" value={formatRampTarget(report)} /> : null}
            {report.ramp ? <SummaryValue label="爬坡耗时（发送 / 排空 / 总计）" value={formatTrafficDuration(report.ramp.traffic)} /> : null}
          </div>
          {report.ramp && !report.ramp.completed_window ? <p role="status" className="mt-3 rounded-md border border-warning/25 bg-warning-soft px-3 py-2 text-[11px] text-warning">爬坡窗口未完整执行；主指标仍只统计稳态阶段，请结合上限与停止状态解读爬坡数据。</p> : null}
        </div>
      </>
    ) : null}
    <Separator />
    <div>
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-xs font-semibold">稳态运行结果</h4>
        <span className="text-[10px] text-muted-foreground">主指标仅统计稳态阶段</span>
      </div>
      <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3 xl:grid-cols-6">
        <SummaryValue label={completion.label} value={completion.value} />
        <SummaryValue label="成功" value={String(report.metrics.succeeded)} />
        <SummaryValue label="失败" value={String(report.metrics.failed)} />
        <SummaryValue label="成功率" value={`${formatMetric(report.metrics.success_rate_percent)}%`} />
        <SummaryValue label="目标发送" value={performanceTargetRate(report)} />
        <SummaryValue label="调度需求" value={optionalRequestRate(report.metrics.offered_qps)} />
        <SummaryValue label="实际发送" value={optionalRequestRate(report.metrics.launched_qps)} />
        <SummaryValue label="已发送完成吞吐" value={optionalRequestRate(report.metrics.completed_qps)} />
        <SummaryValue label="成功吞吐" value={optionalRequestRate(report.metrics.successful_request_qps)} />
        {report.schema_version === 1 ? <SummaryValue label="旧版请求吞吐" value={`${formatMetric(report.metrics.request_qps)} req/s`} /> : null}
        <SummaryValue label={report.profile.load_mode === "open_loop" ? "峰值在途 / 上限" : "峰值在途 / 配置并发"} value={`${report.progress.peak_in_flight} / ${report.profile.load_mode === "open_loop" ? (report.profile.max_in_flight ?? "—") : (report.progress.capacity_target ?? report.profile.concurrency)}`} />
        <SummaryValue label="总耗时" value={`${formatMetric(report.progress.total_duration_ms / 1_000)} s`} />
        <SummaryValue label="RPM" value={formatMetric(report.metrics.rpm)} />
        <SummaryValue label="输入 TPM" value={`${formatMetric(report.metrics.input_tpm)} TPM`} />
        <SummaryValue label="输出 TPM" value={`${formatMetric(report.metrics.output_tpm)} TPM`} />
        <SummaryValue label="总 TPM" value={`${formatMetric(report.metrics.total_tpm)} TPM`} />
        <SummaryValue label="聚合输出吞吐" value={`${formatMetric(report.metrics.generation_tps)} token/s`} />
      </div>
    </div>
    <PerformanceLatencyTable metrics={report.metrics} />
    <PerformanceStreamingTimingTable schemaVersion={report.schema_version} metrics={report.metrics} />
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

function InlineSummaryValue({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0 rounded-md border bg-background/70 px-3 py-2 font-medium tabular-nums" title={`${label} ${value}`}>{label} {value}</div>
}

function SLOStatusBadge({ status }: { status: NonNullable<QuickPerformanceReport["slo_assessment"]>["status"] }) {
  return <Badge
    variant={status === "failed" ? "destructive" : "outline"}
    className={status === "passed" ? "border-success/25 bg-success-soft text-success-strong" : status === "not_evaluated" ? "border-warning/25 bg-warning-soft text-warning" : undefined}
  >{performanceSLOStatusLabel(status)}</Badge>
}

function CapacityRungTable({ report }: { report: QuickPerformanceReport }) {
  const capacity = report.capacity_result
  if (!capacity) return null
  const unit = report.profile.load_mode === "open_loop" ? "RPS" : "并发"
  return <div className="mt-3 overflow-x-auto rounded-lg border">
    <Table aria-label="容量阶梯结果" className="min-w-[1120px]">
      <TableHeader><TableRow className="hover:bg-transparent">
        <TableHead className="h-8 pl-3 text-[11px]">档位</TableHead>
        <TableHead className="h-8 text-[11px]">目标</TableHead>
        <TableHead className="h-8 text-[11px]">传输</TableHead>
        <TableHead className="h-8 text-[11px]">SLO</TableHead>
        <TableHead className="h-8 text-[11px]">好请求</TableHead>
        <TableHead className="h-8 text-[11px]">达标率</TableHead>
        <TableHead className="h-8 text-[11px]">Goodput</TableHead>
        <TableHead className="h-8 text-[11px]">TTFT P95</TableHead>
        <TableHead className="h-8 text-[11px]">TPOT P95</TableHead>
        <TableHead className="h-8 text-[11px]">E2E P95</TableHead>
        <TableHead className="h-8 pr-3 text-[11px]">失败原因</TableHead>
      </TableRow></TableHeader>
      <TableBody>{capacity.rungs.map((rung) => (
        <TableRow key={rung.index} className="h-9">
          <TableCell className="py-1 pl-3 text-xs tabular-nums">#{rung.index + 1}</TableCell>
          <TableCell className="py-1 text-xs font-medium tabular-nums">{formatMetric(rung.target)} {unit}</TableCell>
          <TableCell className="py-1 text-xs">{rung.success ? "通过" : "未通过"}</TableCell>
          <TableCell className="py-1 text-xs">{shortSLOStatus(rung.slo_assessment.status)}</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{rung.slo_assessment.good_requests} / {rung.slo_assessment.total_requests}</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.slo_assessment.good_request_percent)}%</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.slo_assessment.goodput_qps)} req/s</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.metrics.ttft_p95_ms)} ms</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.metrics.tpot_p95_ms)} ms/token</TableCell>
          <TableCell className="py-1 text-xs tabular-nums">{formatMetric(rung.metrics.e2e_p95_ms)} ms</TableCell>
          <TableCell className="py-1 pr-3 text-xs text-muted-foreground">{rung.failures.length ? rung.failures.map((failure) => `${QUICK_TEST_ERROR_MESSAGES[failure.error_code]} ${failure.count}`).join(" · ") : "—"}</TableCell>
        </TableRow>
      ))}</TableBody>
    </Table>
  </div>
}

function ContextValue({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="min-w-0"><dt className="text-[10px] text-muted-foreground">{label}</dt><dd className={`mt-0.5 truncate font-medium ${mono ? "font-mono text-[11px]" : "tabular-nums"}`} title={value}>{value}</dd></div>
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
    {detail?.source === "run" ? <><Separator /><div className="px-4 py-3"><div className="text-[11px] font-semibold">核心指标</div><dl className="mt-2 space-y-1">{metrics.map(([name, value]) => <InspectorRow key={name} label={`${name} · ${value.samples} samples`} value={`${formatMetric(value.value)} ${value.unit}`} />)}</dl><div className="mt-3 text-[10px] text-muted-foreground">{detail.report.environment.os}/{detail.report.environment.arch} · {detail.report.environment.app_version} · {detail.report.environment.engine_version}</div></div></> : quick ? <><Separator /><dl className="space-y-1 px-4 py-3"><InspectorRow label="目标" value={quick.model_id} /><InspectorRow label="实际发送" value={optionalRequestRate(quick.metrics.launched_qps)} /><InspectorRow label="成功吞吐" value={optionalRequestRate(quick.metrics.successful_request_qps)} />{quick.schema_version === 1 ? <InspectorRow label="旧版请求吞吐" value={`${formatMetric(quick.metrics.request_qps)} req/s`} /> : null}<InspectorRow label="TTFT P50 / P95" value={`${formatMetric(quick.metrics.ttft_p50_ms)} / ${formatMetric(quick.metrics.ttft_p95_ms)} ms`} /><InspectorRow label="TPOT P50 / P95" value={`${formatMetric(quick.metrics.tpot_p50_ms)} / ${formatMetric(quick.metrics.tpot_p95_ms)} ms/token`} /><InspectorRow label="E2E P50 / P95" value={`${formatMetric(quick.metrics.e2e_p50_ms)} / ${formatMetric(quick.metrics.e2e_p95_ms)} ms`} /></dl></> : null}
  </ScrollArea>
}

function ConclusionBadge({ passed }: { passed: boolean }) {
  return <Badge variant="outline" className={passed ? "border-success/25 bg-success-soft text-success-strong" : "border-destructive/25 bg-destructive-soft text-destructive"}>{passed ? "通过" : "未通过"}</Badge>
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

function metric(value?: number): string { return value === undefined ? "—" : formatMetric(value) }
function formatMetric(value: number): string { return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value) }
function optionalRequestRate(value?: number): string { return value === undefined ? "—" : `${formatMetric(value)} req/s` }
function performanceLoadMode(mode?: "fixed_concurrency" | "open_loop"): string { return mode === "open_loop" ? "开放到达（RPS）" : mode === "fixed_concurrency" ? "固定并发" : "旧版固定并发" }
type QuickPerformanceReport = Extract<ReportDetail, { source: "quick_performance" }>["performance"]
function performanceTargetRate(report: QuickPerformanceReport): string {
  const target = report.progress.capacity_target ?? report.profile.rate_per_second
  return report.profile.load_mode === "open_loop" && target !== undefined ? `${formatMetric(target)} req/s` : "—（固定并发）"
}
function formatSLOThresholds(thresholds: NonNullable<QuickPerformanceReport["slo_assessment"]>["thresholds"]): string {
  const enabled = [
    thresholds.ttft_ms > 0 ? `TTFT ≤ ${formatMetric(thresholds.ttft_ms)} ms` : "",
    thresholds.tpot_ms > 0 ? `TPOT ≤ ${formatMetric(thresholds.tpot_ms)} ms/token` : "",
    thresholds.e2e_ms > 0 ? `E2E ≤ ${formatMetric(thresholds.e2e_ms)} ms` : "",
  ].filter(Boolean)
  return enabled.join(" · ") || "未启用"
}
function formatSLOViolations(violations: NonNullable<QuickPerformanceReport["slo_assessment"]>["violations"]): string {
  return `传输 ${violations.transport} · TTFT ${violations.ttft} · TPOT ${violations.tpot} · E2E ${violations.e2e}`
}
function shortSLOStatus(status: NonNullable<QuickPerformanceReport["slo_assessment"]>["status"]): string {
  return status === "passed" ? "通过" : status === "failed" ? "未通过" : "未评估"
}
function performanceArrivalPattern(report: QuickPerformanceReport): string {
  if (report.profile.arrival_pattern === "poisson") return "Poisson 到达"
  return report.profile.arrival_pattern === "constant" ? "恒定间隔" : "恒定间隔（旧报告）"
}

function PerformanceTimeSliceTable({ slices }: { slices: NonNullable<QuickPerformanceReport["time_slices"]> }) {
  return <div>
    <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
      <h4 className="text-xs font-semibold">时间切片</h4>
      <span className="text-[10px] text-muted-foreground">仅列出后端实际记录的稳态窗口</span>
    </div>
    <div className="overflow-x-auto rounded-lg border">
      <Table aria-label="时间切片" className="min-w-[1040px]">
        <TableHeader><TableRow className="hover:bg-transparent">
          <TableHead className="h-8 pl-3 text-[11px]">段</TableHead>
          <TableHead className="h-8 text-[11px]">窗口</TableHead>
          <TableHead className="h-8 text-[11px]">调度</TableHead>
          <TableHead className="h-8 text-[11px]">发送</TableHead>
          <TableHead className="h-8 text-[11px]">成功</TableHead>
          <TableHead className="h-8 text-[11px]">失败</TableHead>
          <TableHead className="h-8 text-[11px]">拒绝</TableHead>
          <TableHead className="h-8 text-[11px]">Token（输入 / 输出 / 缓存）</TableHead>
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
        )) : <TableRow><TableCell colSpan={11} className="h-20 text-center text-xs text-muted-foreground">未记录有效时间切片</TableCell></TableRow>}</TableBody>
      </Table>
    </div>
  </div>
}
function performanceWorkloadMode(report: QuickPerformanceReport): string {
  if (report.profile.workload_mode === "normal") return "正态分布"
  return report.profile.workload_mode === "fixed" ? "固定 Token" : "固定 Token（旧报告）"
}
function performanceSeed(report: QuickPerformanceReport): string {
  if (report.profile.random_seed === undefined) return "—（旧报告）"
  return report.profile.random_seed > 0 ? formatMetric(report.profile.random_seed) : "—（未使用）"
}
function performanceSharedPrefix(report: QuickPerformanceReport): string {
  if (report.profile.shared_prefix_tokens === undefined) return "0 Token（旧报告）"
  return `${formatMetric(report.profile.shared_prefix_tokens)} Token`
}
function performanceWarmupConfiguration(report: QuickPerformanceReport): string {
  const requests = report.profile.warmup_requests ?? 0
  return requests > 0 ? `${formatMetric(requests)} 次` : "0（禁用）"
}
function performanceRampConfiguration(report: QuickPerformanceReport): string {
  const durationMS = report.profile.ramp_duration_ms ?? 0
  if (durationMS === 0) return "0（禁用）"
  if (report.profile.load_mode === "fixed_concurrency") {
    return `${formatDuration(durationMS)} · 上限 ${formatMetric(report.profile.ramp_request_cap ?? 0)}`
  }
  return `${formatDuration(durationMS)} · 10 阶线性阶梯`
}
function performanceSliceConfiguration(report: QuickPerformanceReport): string {
  const durationMS = report.profile.slice_duration_ms ?? 0
  return durationMS > 0 ? formatDuration(durationMS) : "0（禁用）"
}
function formatRequestBudget(report: QuickPerformanceReport): string {
  const budget = report.request_budget
  if (!budget) return "—"
  return `${formatMetric(budget.total_cap)} / ${formatMetric(budget.limit)} · 热身 ${formatMetric(budget.warmup_cap)} · 爬坡 ${formatMetric(budget.ramp_cap)} · 稳态 ${formatMetric(budget.measured_cap)}`
}
function formatTrafficSummary(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  const flags = `${traffic.capped ? " · 已触及上限" : ""}${traffic.stopped ? " · 提前停止" : ""}`
  return `${formatMetric(traffic.completed)} / ${formatMetric(traffic.request_cap)} 完成 · 调度 ${formatMetric(traffic.offered)} · 发送 ${formatMetric(traffic.launched)} · 成功 ${formatMetric(traffic.succeeded)} · 失败 ${formatMetric(traffic.failed)} · 拒绝 ${formatMetric(traffic.rejected)}${flags}`
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
    ? `目标并发 ${formatMetric(ramp.target_concurrency)}`
    : `目标 ${formatMetric(ramp.target_rate_per_second ?? 0)} req/s`
  return `${formatMetric(ramp.steps)} 阶线性阶梯 · ${target} · ${ramp.completed_window ? "完整窗口" : "未完整窗口"}`
}
function formatSliceWindow(startMS: number, endMS: number, partial: boolean): string {
  return `${formatMetric(startMS / 1_000)}–${formatMetric(endMS / 1_000)} s${partial ? " · 部分" : ""}`
}
function formatSliceP95(latency: NonNullable<QuickPerformanceReport["time_slices"]>[number]["ttft"]): string {
  return latency.count === 0 ? "—" : `${formatMetric(latency.p95_ms)} ms`
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
function formatDuration(valueMS: number): string { return valueMS >= 1_000 ? `${formatMetric(valueMS / 1_000)} s` : `${formatMetric(valueMS)} ms` }
function performanceMode(requestCount: number, durationMS: number): string {
  return requestCount > 0 ? `固定请求 · ${formatMetric(requestCount)} 次` : `持续时间 · ${formatDuration(durationMS)}`
}
function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)).replaceAll("/", "-")
}
