import { useEffect, useMemo, useState } from "react"

import { publicDesktopOperationErrorMessage } from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { EmptyInspector, InspectorHeader, InspectorRow, PageFrame } from "@/features/shell/page-frame"

import type { ExportedReport, ReportDetail, ReportExportFormat, ReportSnapshot, ReportSummary } from "./data"

export function ReportWorkspace({ snapshot, getDetail, exportReport }: {
  snapshot: ReportSnapshot
  getDetail: (reportId: string) => Promise<ReportDetail>
  exportReport: (reportId: string, format: ReportExportFormat) => Promise<ExportedReport>
}) {
  const [selectedID, setSelectedID] = useState("")
  const [detail, setDetail] = useState<ReportDetail | null>(null)
  const [detailError, setDetailError] = useState("")
  const [exporting, setExporting] = useState<ReportExportFormat | "copy" | "">("")
  const [exportError, setExportError] = useState("")
  const selected = snapshot.reports.find((report) => report.id === selectedID) ?? snapshot.reports[0]

  useEffect(() => {
    if (!selected) {
      setDetail(null)
      return
    }
    let active = true
    setDetail(null)
    setDetailError("")
    void getDetail(selected.id).then(
      (value) => { if (active) setDetail(value) },
      () => { if (active) setDetailError("无法读取报告详情，请检查本地日志") },
    )
    return () => { active = false }
  }, [getDetail, selected?.id])

  const handleExport = async (format: ReportExportFormat) => {
    if (!selected) return
    setExporting(format)
    setExportError("")
    try {
      downloadExport(await exportReport(selected.id, format))
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
      const exported = await exportReport(selected.id, "png")
      const ClipboardItemType = window.ClipboardItem
      if (!navigator.clipboard?.write || !ClipboardItemType) throw new Error("clipboard image unsupported")
      await navigator.clipboard.write([new ClipboardItemType({ [exported.media_type]: exportBlob(exported) })])
    } catch {
      setExportError("当前系统无法复制 PNG，可使用 PNG 下载")
    } finally {
      setExporting("")
    }
  }

  return (
    <PageFrame
      title="测试报告"
      description="查看 Go Core 封存的结论、指标、请求明细与同源导出"
      count={`${snapshot.reports.length} 份报告`}
      inspector={selected ? (
        <ReportInspector report={selected} detail={detail} detailError={detailError} exporting={exporting} exportError={exportError} onExport={handleExport} onCopyPNG={copyPNG} />
      ) : <EmptyInspector label="尚未选择报告" />}
      inspectorLabel="报告详情"
    >
      {snapshot.reports.length === 0 ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Empty><EmptyTitle>还没有测试报告</EmptyTitle><EmptyDescription>运行完成并封存结论后，报告会出现在这里。</EmptyDescription></Empty>
        </ScrollArea>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col border-t">
          <ScrollArea className="min-h-[180px] flex-[2]">
            <Table aria-label="测试报告目录" className="min-w-[760px]">
              <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm"><TableRow className="hover:bg-transparent">
                <TableHead className="h-8 w-[88px] pl-4 text-[11px]">结论</TableHead><TableHead className="h-8 text-[11px]">报告 / 计划</TableHead><TableHead className="h-8 text-[11px]">目标</TableHead><TableHead className="h-8 text-[11px]">用例</TableHead><TableHead className="h-8 text-[11px]">生成时间</TableHead>
              </TableRow></TableHeader>
              <TableBody>{snapshot.reports.map((report) => (
                <TableRow key={report.id} data-state={report.id === selected?.id ? "selected" : undefined} aria-selected={report.id === selected?.id} onClick={() => setSelectedID(report.id)} className="dense-table-row h-11">
                  <TableCell className="py-1 pl-4"><ConclusionBadge passed={report.passed} /></TableCell>
                  <TableCell className="py-1"><Button variant="link" size="sm" className="h-auto max-w-[240px] justify-start p-0 text-xs no-underline hover:no-underline" aria-label={`查看报告 ${report.verdict}`}><span className="truncate">{report.verdict}</span></Button><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.plan_name}</div></TableCell>
                  <TableCell className="py-1"><div className="truncate text-xs">{report.model_name}</div><div className="mt-0.5 truncate text-[10px] text-muted-foreground">{report.channel_name}</div></TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{report.case_count - report.failed_case_count}/{report.case_count}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{formatTimestamp(report.generated_at)}</TableCell>
                </TableRow>
              ))}</TableBody>
            </Table>
          </ScrollArea>
          <RequestResultTable detail={detail} error={detailError} />
        </div>
      )}
    </PageFrame>
  )
}

function RequestResultTable({ detail, error }: { detail: ReportDetail | null; error: string }) {
  if (error) return <div role="alert" className="border-t px-4 py-3 text-xs text-destructive">{error}</div>
  if (!detail) return <div className="border-t px-4 py-3 text-xs text-muted-foreground">正在读取请求明细…</div>
  const visibleResults = detail.request_results.slice(0, 1000)
  return (
    <ScrollArea className="min-h-[180px] flex-[3] border-t">
      {detail.request_results.length > visibleResults.length ? <div role="status" className="border-b px-4 py-2 text-[11px] text-muted-foreground">当前显示前 1,000 条请求；完整 {detail.request_results.length.toLocaleString("zh-CN")} 条可导出 JSON 或 HTML。</div> : null}
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
    </ScrollArea>
  )
}

function MetricCell({ value, unit }: { value?: number; unit: string }) {
  return <TableCell className="py-1 text-xs tabular-nums">{metric(value)} {value === undefined ? "" : unit}</TableCell>
}

function ReportInspector({ report, detail, detailError, exporting, exportError, onExport, onCopyPNG }: {
  report: ReportSummary
  detail: ReportDetail | null
  detailError: string
  exporting: ReportExportFormat | "copy" | ""
  exportError: string
  onExport: (format: ReportExportFormat) => Promise<void>
  onCopyPNG: () => Promise<void>
}) {
  const metrics = useMemo(() => detail ? Object.entries(detail.report.metrics).slice(0, 8) : [], [detail])
  return <ScrollArea className="h-full">
    <InspectorHeader title={report.verdict} subtitle={report.id} trailing={<ConclusionBadge passed={report.passed} />} />
    <Separator />
    <dl className="space-y-1 px-4 py-2">
      <InspectorRow label="运行" value={`${report.run_status} · ${report.run_id}`} /><InspectorRow label="测试计划" value={report.plan_name} /><InspectorRow label="模型与渠道" value={`${report.model_name} · ${report.channel_name}`} /><InspectorRow label="用例结论" value={`${report.case_count - report.failed_case_count}/${report.case_count} 通过 · ${report.failed_case_count} 失败`} /><InspectorRow label="问题" value={`${report.issue_count} 项`} /><InspectorRow label="生成时间" value={formatTimestamp(report.generated_at)} />
    </dl>
    <Separator />
    <div className="grid grid-cols-2 gap-2 px-4 py-3" aria-label="报告导出">
      {(["json", "html", "png", "pdf"] as const).map((format) => <Button key={format} variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onExport(format)}>{exporting === format ? "生成中…" : format.toUpperCase()}</Button>)}
      <Button className="col-span-2" variant="outline" size="sm" disabled={Boolean(exporting)} onClick={() => void onCopyPNG()}>{exporting === "copy" ? "复制中…" : "复制 PNG"}</Button>
    </div>
    {exportError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{exportError}</div> : null}
    {detailError ? <div role="alert" className="px-4 pb-3 text-[11px] text-destructive">{detailError}</div> : null}
    {detail ? <><Separator /><div className="px-4 py-3"><div className="text-[11px] font-semibold">核心指标</div><dl className="mt-2 space-y-1">{metrics.map(([name, value]) => <InspectorRow key={name} label={`${name} · ${value.samples} samples`} value={`${formatMetric(value.value)} ${value.unit}`} />)}</dl><div className="mt-3 text-[10px] text-muted-foreground">{detail.report.environment.os}/{detail.report.environment.arch} · {detail.report.environment.app_version} · {detail.report.environment.engine_version}</div></div></> : null}
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

function metric(value?: number): string { return value === undefined ? "—" : formatMetric(value) }
function formatMetric(value: number): string { return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value) }
function formatTimestamp(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)).replaceAll("/", "-")
}
