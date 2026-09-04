import { useMemo, useState, type ReactNode } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

import {
  QUICK_TEST_ERROR_MESSAGES,
  type QuickPerformanceReport,
  type QuickPerformanceSample,
  type QuickTestErrorCode,
} from "./data"

const PAGE_SIZE = 50

type RequestFilter = "all" | "failed" | "succeeded" | QuickTestErrorCode

export function QuickPerformanceRequestAnalysis({ report }: { report: QuickPerformanceReport }) {
  const [filter, setFilter] = useState<RequestFilter>("all")
  const [page, setPage] = useState(0)
  const [selectedIndex, setSelectedIndex] = useState<number | null>(null)
  const filtered = useMemo(() => report.samples.filter((sample) => matchesFilter(sample, filter)), [report.samples, filter])
  const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const safePage = Math.min(page, pages - 1)
  const visible = filtered.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE)
  const selected = selectedIndex === null ? null : report.samples.find((sample) => sample.request_index === selectedIndex) ?? null

  const changeFilter = (next: RequestFilter) => {
    setFilter(next)
    setPage(0)
    setSelectedIndex(null)
  }

  return (
    <section aria-label="请求结果分析" className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[220px_minmax(0,1fr)]">
        <OutcomeDonut succeeded={report.metrics.succeeded} failed={report.metrics.failed} />
        <div className="min-w-0">
          <h4 className="text-xs font-semibold">失败原因分布</h4>
          {report.failures.length ? (
            <div className="mt-2 flex flex-wrap gap-2">
              {report.failures.map((failure) => (
                <Button
                  key={failure.error_code}
                  type="button"
                  size="sm"
                  variant={filter === failure.error_code ? "secondary" : "outline"}
                  aria-pressed={filter === failure.error_code}
                  aria-label={`按${QUICK_TEST_ERROR_MESSAGES[failure.error_code]}筛选，${failure.count} 个请求`}
                  onClick={() => changeFilter(failure.error_code)}
                >
                  {QUICK_TEST_ERROR_MESSAGES[failure.error_code]} <span className="tabular-nums">{failure.count}</span>
                </Button>
              ))}
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">本次测试没有失败请求。</p>
          )}
          <p className="mt-2 text-[11px] text-muted-foreground">失败响应仅展示 Core 已脱敏并限制大小的安全证据。</p>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap gap-1" aria-label="请求结果筛选">
          <FilterButton active={filter === "all"} onClick={() => changeFilter("all")}>全部 {report.metrics.completed}</FilterButton>
          <FilterButton active={filter === "failed"} onClick={() => changeFilter("failed")}>失败 {report.metrics.failed}</FilterButton>
          <FilterButton active={filter === "succeeded"} onClick={() => changeFilter("succeeded")}>成功 {report.metrics.succeeded}</FilterButton>
        </div>
        <p className="text-[11px] tabular-nums text-muted-foreground">显示 {filtered.length} 条请求</p>
      </div>

      <ScrollArea className="h-96 rounded-md border bg-background">
        <Table aria-label="逐请求结果" className="min-w-[780px] text-xs">
          <TableHeader className="sticky top-0 z-10 bg-background">
            <TableRow>
              <TableHead className="h-8">请求</TableHead>
              <TableHead className="h-8">结果</TableHead>
              <TableHead className="h-8 text-right">HTTP</TableHead>
              <TableHead className="h-8">错误原因</TableHead>
              <TableHead className="h-8 text-right">E2E</TableHead>
              <TableHead className="h-8 text-right">TTFT</TableHead>
              <TableHead className="h-8 text-right">Token</TableHead>
              <TableHead className="h-8 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.map((sample) => (
              <TableRow key={sample.request_index} data-state={selectedIndex === sample.request_index ? "selected" : undefined}>
                <TableCell className="font-mono">请求 {sample.request_index + 1}</TableCell>
                <TableCell><Badge variant={sample.success ? "secondary" : "destructive"}>{sample.success ? "成功" : "失败"}</Badge></TableCell>
                <TableCell className="text-right tabular-nums">{sample.http_status || "—"}</TableCell>
                <TableCell>{sample.error_code ? QUICK_TEST_ERROR_MESSAGES[sample.error_code] : "—"}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMS(sample.e2e_ms)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMS(sample.ttft_ms)}</TableCell>
                <TableCell className="text-right tabular-nums">{sample.prompt_tokens} / {sample.completion_tokens}</TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    aria-label={`查看请求 ${sample.request_index + 1} 详情`}
                    onClick={() => setSelectedIndex(sample.request_index)}
                  >
                    查看
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {visible.length === 0 ? <p className="p-8 text-center text-xs text-muted-foreground">当前筛选下没有请求。</p> : null}
      </ScrollArea>

      {pages > 1 ? (
        <div className="flex items-center justify-end gap-2">
          <Button type="button" size="sm" variant="outline" disabled={safePage === 0} onClick={() => setPage((value) => Math.max(0, value - 1))}>上一页</Button>
          <span className="text-[11px] tabular-nums text-muted-foreground">{safePage + 1} / {pages}</span>
          <Button type="button" size="sm" variant="outline" disabled={safePage + 1 >= pages} onClick={() => setPage((value) => Math.min(pages - 1, value + 1))}>下一页</Button>
        </div>
      ) : null}

      {selected ? <RequestDetail sample={selected} /> : null}
    </section>
  )
}

function OutcomeDonut({ succeeded, failed }: { succeeded: number; failed: number }) {
  const total = Math.max(1, succeeded + failed)
  const radius = 34
  const circumference = 2 * Math.PI * radius
  const successLength = circumference * succeeded / total
  const failureLength = circumference * failed / total
  return (
    <figure aria-label={`请求结果分布：成功 ${succeeded}，失败 ${failed}`} className="flex items-center gap-3 rounded-md border bg-surface-control p-3">
      <svg aria-hidden="true" viewBox="0 0 88 88" className="size-20 shrink-0 -rotate-90">
        <circle cx="44" cy="44" r={radius} fill="none" className="stroke-border" strokeWidth="10" />
        {succeeded ? <circle cx="44" cy="44" r={radius} fill="none" className="stroke-success" strokeWidth="10" strokeDasharray={`${successLength} ${circumference - successLength}`} /> : null}
        {failed ? <circle cx="44" cy="44" r={radius} fill="none" className="stroke-destructive" strokeWidth="10" strokeDasharray={`${failureLength} ${circumference - failureLength}`} strokeDashoffset={-successLength} /> : null}
      </svg>
      <figcaption className="min-w-0 text-xs">
        <p className="font-semibold">请求结果</p>
        <p className="mt-1 text-success">成功 <span className="tabular-nums">{succeeded}</span></p>
        <p className="mt-0.5 text-destructive">失败 <span className="tabular-nums">{failed}</span></p>
      </figcaption>
    </figure>
  )
}

function FilterButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return <Button type="button" size="sm" variant={active ? "secondary" : "ghost"} aria-pressed={active} onClick={onClick}>{children}</Button>
}

function RequestDetail({ sample }: { sample: QuickPerformanceSample }) {
  const evidence = sample.response_evidence
  return (
    <section aria-label={`请求 ${sample.request_index + 1} 详情`} className="space-y-3 border-t pt-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h4 className="text-xs font-semibold">请求 {sample.request_index + 1} 详情</h4>
          <p className="mt-0.5 text-[11px] text-muted-foreground">响应已经过安全脱敏；凭据、Cookie 与敏感字段不会展示。</p>
        </div>
        <Badge variant={sample.success ? "secondary" : "destructive"}>{sample.success ? "成功" : "失败"}</Badge>
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3">
        <DetailValue label="错误原因" value={sample.error_code ? QUICK_TEST_ERROR_MESSAGES[sample.error_code] : "无"} />
        <DetailValue label="HTTP 状态" value={sample.http_status ? String(sample.http_status) : "未收到响应"} numeric />
        <DetailValue label="请求 ID" value={evidence?.request_id || "未提供"} mono />
        <DetailValue label="Content-Type" value={evidence?.content_type || "未提供"} mono />
        <DetailValue label="E2E / TTFT / TPOT" value={`${formatMS(sample.e2e_ms)} / ${formatMS(sample.ttft_ms)} / ${formatMS(sample.tpot_ms)}`} numeric />
        <DetailValue label="Prompt / Completion / Cached" value={`${sample.prompt_tokens} / ${sample.completion_tokens} / ${sample.cached_tokens}`} numeric />
      </dl>
      <ResponseBody sample={sample} />
    </section>
  )
}

function ResponseBody({ sample }: { sample: QuickPerformanceSample }) {
  const evidence = sample.response_evidence
  if (!evidence) {
    return <p className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">{sample.http_status === 0 ? "请求未收到 HTTP 响应，因此没有响应正文。" : "该请求没有可用的响应证据。"}</p>
  }
  if (evidence.capture_status === "omitted") {
    return <p className="rounded-md border border-dashed p-3 text-xs text-warning">响应正文因本次测试的证据总量达到安全上限而未保存。</p>
  }
  if (evidence.capture_status === "empty") {
    return <p className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">上游响应正文为空。</p>
  }
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
        <span>脱敏响应正文</span>
        <span className="tabular-nums">原始读取 {evidence.body_bytes} B{evidence.truncated ? " · 已截断" : ""}</span>
      </div>
      <ScrollArea className="h-48 rounded-md border bg-surface-control">
        <pre className="min-w-full whitespace-pre-wrap break-all p-3 font-mono text-xs leading-5">{evidence.body}</pre>
      </ScrollArea>
    </div>
  )
}

function DetailValue({ label, value, numeric = false, mono = false }: { label: string; value: string; numeric?: boolean; mono?: boolean }) {
  return <div className="min-w-0"><dt className="text-[11px] text-muted-foreground">{label}</dt><dd className={`mt-0.5 break-all ${numeric ? "tabular-nums" : ""} ${mono ? "font-mono" : ""}`}>{value}</dd></div>
}

function matchesFilter(sample: QuickPerformanceSample, filter: RequestFilter): boolean {
  if (filter === "all") return true
  if (filter === "failed") return !sample.success
  if (filter === "succeeded") return sample.success
  return sample.error_code === filter
}

function formatMS(value: number): string {
  return `${new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value)} ms`
}
