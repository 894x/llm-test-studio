import { desktopLocale } from "@/i18n/runtime"
import { useTranslation } from "react-i18next"
import { useMemo, useState, type ReactNode } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

import {
  type QuickPerformanceReport,
  type QuickPerformanceSample,
  type QuickTestErrorCode,
} from "./data"

const PAGE_SIZE = 50

type RequestFilter = "all" | "failed" | "succeeded" | QuickTestErrorCode

export function QuickPerformanceRequestAnalysis({ report }: { report: QuickPerformanceReport }) {
  const { t: tx } = useTranslation()
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
    <section aria-label={tx("desktop:quick-test_request_result_analysis")} className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[220px_minmax(0,1fr)]">
        <OutcomeDonut succeeded={report.metrics.succeeded} failed={report.metrics.failed} />
        <div className="min-w-0">
          <h4 className="text-xs font-semibold">{tx("desktop:quick-test_failure_reasons")}</h4>
          {report.failures.length ? (
            <div className="mt-2 flex flex-wrap gap-2">
              {report.failures.map((failure) => (
                <Button
                  key={failure.error_code}
                  type="button"
                  size="sm"
                  variant={filter === failure.error_code ? "secondary" : "outline"}
                  aria-pressed={filter === failure.error_code}
                  aria-label={tx("desktop:quick-test_filter_by_value_value_requests", { value1: tx(`common:errorCode.${failure.error_code}`, { defaultValue: failure.error_code }), value2: failure.count })}
                  onClick={() => changeFilter(failure.error_code)}
                >
                  {tx(`common:errorCode.${failure.error_code}`, { defaultValue: failure.error_code })} <span className="tabular-nums">{failure.count}</span>
                </Button>
              ))}
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">{tx("desktop:quick-test_no_requests_failed_in_this_test")}</p>
          )}
          <p className="mt-2 text-[11px] text-muted-foreground">{tx("desktop:quick-test_failure_responses_show_only_evidence_redacted_and_size_limited_by")}</p>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap gap-1" aria-label={tx("desktop:quick-test_filter_request_results")}>
          <FilterButton active={filter === "all"} onClick={() => changeFilter("all")}>{tx("desktop:quick-test_all")} {report.metrics.completed}</FilterButton>
          <FilterButton active={filter === "failed"} onClick={() => changeFilter("failed")}>{tx("desktop:quick-test_failed")} {report.metrics.failed}</FilterButton>
          <FilterButton active={filter === "succeeded"} onClick={() => changeFilter("succeeded")}>{tx("desktop:quick-test_succeeded")} {report.metrics.succeeded}</FilterButton>
        </div>
        <p className="text-[11px] tabular-nums text-muted-foreground">{tx("desktop:quick-test_showing")} {filtered.length}  {tx("desktop:quick-test_requests")}</p>
      </div>

      <ScrollArea className="h-96">
        <Table aria-label={tx("desktop:quick-test_individual_request_results")} className="min-w-[840px] text-xs">
          <TableHeader className="sticky top-0 z-10 bg-background">
            <TableRow>
              <TableHead className="h-8">{tx("desktop:quick-test_request")}</TableHead>
              <TableHead className="h-8">{tx("desktop:quick-test_result")}</TableHead>
              <TableHead className="h-8 text-right">HTTP</TableHead>
              <TableHead className="h-8">{tx("desktop:quick-test_error_reason")}</TableHead>
              <TableHead className="h-8 text-right">E2E</TableHead>
              <TableHead className="h-8 text-right">{tx("desktop:quick-test_ttft_includes_reasoning")}</TableHead>
              <TableHead className="h-8 text-right">Token</TableHead>
              <TableHead className="h-8 text-right">{tx("desktop:quick-test_actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.map((sample) => (
              <TableRow key={sample.request_index} data-state={selectedIndex === sample.request_index ? "selected" : undefined}>
                <TableCell className="font-mono">{tx("desktop:quick-test_request")} {sample.request_index + 1}</TableCell>
                <TableCell><Badge variant={sample.success ? "secondary" : "destructive"}>{sample.success ? tx("desktop:quick-test_succeeded") : tx("desktop:quick-test_failed")}</Badge></TableCell>
                <TableCell className="text-right tabular-nums">{sample.http_status || "—"}</TableCell>
                <TableCell>{sample.error_code ? tx(`common:errorCode.${sample.error_code}`, { defaultValue: sample.error_code }) : "—"}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMS(sample.e2e_ms)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMS(sample.ttft_ms)}</TableCell>
                <TableCell className="text-right tabular-nums">{sample.prompt_tokens} / {sample.completion_tokens}</TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    aria-label={tx("desktop:quick-test_view_request_value_details", { value1: sample.request_index + 1 })}
                    onClick={() => setSelectedIndex(sample.request_index)}
                  >
                     {tx("desktop:quick-test_view")} </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {visible.length === 0 ? <p className="p-8 text-center text-xs text-muted-foreground">{tx("desktop:quick-test_no_requests_match_the_current_filter")}</p> : null}
      </ScrollArea>

      {pages > 1 ? (
        <div className="flex items-center justify-end gap-2">
          <Button type="button" size="sm" variant="outline" disabled={safePage === 0} onClick={() => setPage((value) => Math.max(0, value - 1))}>{tx("desktop:quick-test_previous_page")}</Button>
          <span className="text-[11px] tabular-nums text-muted-foreground">{safePage + 1} / {pages}</span>
          <Button type="button" size="sm" variant="outline" disabled={safePage + 1 >= pages} onClick={() => setPage((value) => Math.min(pages - 1, value + 1))}>{tx("desktop:quick-test_next_page")}</Button>
        </div>
      ) : null}

      {selected ? <RequestDetail sample={selected} /> : null}
    </section>
  )
}

function OutcomeDonut({ succeeded, failed }: { succeeded: number; failed: number }) {
  const { t: tx } = useTranslation()
  const total = Math.max(1, succeeded + failed)
  const radius = 34
  const circumference = 2 * Math.PI * radius
  const successLength = circumference * succeeded / total
  const failureLength = circumference * failed / total
  return (
    <figure aria-label={tx("desktop:quick-test_request_results_value_succeeded_value_failed", { value1: succeeded, value2: failed })} className="flex items-center gap-3 rounded-md border bg-surface-control p-3">
      <svg aria-hidden="true" viewBox="0 0 88 88" className="size-20 shrink-0 -rotate-90">
        <circle cx="44" cy="44" r={radius} fill="none" className="stroke-border" strokeWidth="10" />
        {succeeded ? <circle cx="44" cy="44" r={radius} fill="none" className="stroke-success" strokeWidth="10" strokeDasharray={`${successLength} ${circumference - successLength}`} /> : null}
        {failed ? <circle cx="44" cy="44" r={radius} fill="none" className="stroke-destructive" strokeWidth="10" strokeDasharray={`${failureLength} ${circumference - failureLength}`} strokeDashoffset={-successLength} /> : null}
      </svg>
      <figcaption className="min-w-0 text-xs">
        <p className="font-semibold">{tx("desktop:quick-test_request_results")}</p>
        <p className="mt-1 text-success">{tx("desktop:quick-test_succeeded")} <span className="tabular-nums">{succeeded}</span></p>
        <p className="mt-0.5 text-destructive">{tx("desktop:quick-test_failed")} <span className="tabular-nums">{failed}</span></p>
      </figcaption>
    </figure>
  )
}

function FilterButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return <Button type="button" size="sm" variant={active ? "secondary" : "ghost"} aria-pressed={active} onClick={onClick}>{children}</Button>
}

function RequestDetail({ sample }: { sample: QuickPerformanceSample }) {
  const { t: tx } = useTranslation()
  const evidence = sample.response_evidence
  return (
    <section aria-label={tx("desktop:quick-test_request_value_details", { value1: sample.request_index + 1 })} className="space-y-3 border-t pt-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h4 className="text-xs font-semibold">{tx("desktop:quick-test_request")} {sample.request_index + 1}  {tx("desktop:quick-test_details")}</h4>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{tx("desktop:quick-test_the_response_is_redacted_credentials_cookies_and_sensitive_fields_are")}</p>
        </div>
        <Badge variant={sample.success ? "secondary" : "destructive"}>{sample.success ? tx("desktop:quick-test_succeeded") : tx("desktop:quick-test_failed")}</Badge>
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3">
        <DetailValue label={tx("desktop:quick-test_error_reason")} value={sample.error_code ? tx(`common:errorCode.${sample.error_code}`, { defaultValue: sample.error_code }) : tx("desktop:quick-test_none")} />
        <DetailValue label={tx("desktop:quick-test_http_status")} value={sample.http_status ? String(sample.http_status) : tx("desktop:quick-test_no_response_received")} numeric />
        <DetailValue label={tx("desktop:quick-test_request_id")} value={evidence?.request_id || tx("desktop:quick-test_not_provided")} mono />
        <DetailValue label="Content-Type" value={evidence?.content_type || tx("desktop:quick-test_not_provided")} mono />
        <DetailValue label="E2E / TPOT" value={`${formatMS(sample.e2e_ms)} / ${formatMS(sample.tpot_ms)}`} numeric />
        <DetailValue label="Prompt / Completion / Cached" value={`${sample.prompt_tokens} / ${sample.completion_tokens} / ${sample.cached_tokens}`} numeric />
        {sample.ttfb_ms !== undefined ? <>
          <DetailValue label="TTFB" value={formatMilestone(sample.ttfb_ms)} numeric />
          <DetailValue label={tx("desktop:quick-test_ttft_includes_reasoning")} value={formatMilestone(sample.ttft_any_ms ?? 0)} numeric />
          <DetailValue label={tx("desktop:quick-test_ttft_visible_content")} value={formatMilestone(sample.ttft_visible_ms ?? 0)} numeric />
          <DetailValue label={tx("desktop:quick-test_ttst_second_semantic_chunk")} value={formatMilestone(sample.ttst_ms ?? 0)} numeric />
          <DetailValue label={tx("desktop:quick-test_observed_icl_semantic_chunk_interval_not_token_itl")} value={(sample.semantic_chunk_count ?? 0) >= 2 ? formatMS(sample.observed_icl_ms ?? 0) : "—"} numeric />
          <DetailValue label={tx("desktop:quick-test_semantic_chunks")} value={String(sample.semantic_chunk_count ?? 0)} numeric />
        </> : null}
      </dl>
      <ResponseBody sample={sample} />
    </section>
  )
}

function ResponseBody({ sample }: { sample: QuickPerformanceSample }) {
  const { t: tx } = useTranslation()
  const evidence = sample.response_evidence
  if (!evidence) {
    return <p className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">{sample.http_status === 0 ? tx("desktop:quick-test_no_http_response_was_received_so_no_response_body_is") : tx("desktop:quick-test_no_response_evidence_is_available_for_this_request")}</p>
  }
  if (evidence.capture_status === "omitted") {
    return <p className="rounded-md border border-dashed p-3 text-xs text-warning">{tx("desktop:quick-test_the_response_body_was_not_saved_because_the_test_reached")}</p>
  }
  if (evidence.capture_status === "empty") {
    return <p className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">{tx("desktop:quick-test_the_upstream_response_body_is_empty")}</p>
  }
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
        <span>{tx("desktop:quick-test_redacted_response_body")}</span>
        <span className="tabular-nums">{tx("desktop:quick-test_bytes_read")} {evidence.body_bytes} B{evidence.truncated ? tx("desktop:quick-test_truncated") : ""}</span>
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
  return `${new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 0 }).format(value)} ms`
}

function formatMilestone(value: number): string {
  return value > 0 ? formatMS(value) : "—"
}
