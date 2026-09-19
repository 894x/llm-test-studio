import { Fragment, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import ChevronDownIcon from "lucide-react/dist/esm/icons/chevron-down.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { protocolPresentation } from "@/features/protocols/registry"
import type { ProtocolObservation, AssertionResult, ProtocolColumn, VerificationStatus, VerificationSummary } from "@/features/protocols/types"
import { desktopLocale } from "@/i18n/runtime"
import type { ReportCaseDetail, ReportEntryDetail, ReportResult } from "./data"

const PAGE_SIZE = 50
const DETAIL_PAGE_SIZE = 20

export function EntryCaseTable({
  entry,
  selectedCaseID = "",
  caseNavigationRequest = 0,
}: {
  entry: ReportEntryDetail
  selectedCaseID?: string
  caseNavigationRequest?: number
}) {
  const { t } = useTranslation("reports")
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState<string | null>(null)
  const columns = protocolPresentation(entry.protocol).columns
  const visible = entry.cases.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE)

  useEffect(() => {
    if (!selectedCaseID) return
    const caseIndex = entry.cases.findIndex((caseItem) => caseItem.case_id === selectedCaseID)
    if (caseIndex < 0) return
    const selectedPage = Math.floor(caseIndex / PAGE_SIZE)
    if (selectedPage !== page) {
      setPage(selectedPage)
      return
    }
    const frame = window.requestAnimationFrame(() => {
      const row = document.getElementById(reportCaseRowID(entry.entry_id, selectedCaseID))
      if (!row) return
      const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches
      row.scrollIntoView({ behavior: reducedMotion ? "auto" : "smooth", block: "center", inline: "nearest" })
      row.focus({ preventScroll: true })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [caseNavigationRequest, entry.cases, entry.entry_id, page, selectedCaseID])

  return <>
    <div className="overflow-x-auto">
      <Table aria-label={t("protocolDesign.caseTable")} className="min-w-[660px]">
        <TableHeader><TableRow className="hover:bg-transparent">
          <TableHead className="h-8 w-[30%] pl-3 text-[11px]">{t("protocolDesign.caseName")}</TableHead>
          <TableHead className="h-8 text-[11px]">{t("protocolDesign.verification")}</TableHead>
          {columns.map(column => <TableHead key={column.id} className="h-8 text-[11px]">{columnLabel(column, t)}</TableHead>)}
          <TableHead className="h-8 pr-3 text-[11px]">{t("protocolDesign.summary")}</TableHead>
        </TableRow></TableHeader>
        <TableBody>{visible.length ? visible.map(caseItem => {
          const open = expanded === caseItem.case_id
          const detailID = `case-${entry.entry_id}-${caseItem.case_id}`
          return <Fragment key={caseItem.case_id}>
            <TableRow
              id={reportCaseRowID(entry.entry_id, caseItem.case_id)}
              className="h-9 cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
              data-state={open || selectedCaseID === caseItem.case_id ? "selected" : undefined}
              aria-selected={selectedCaseID === caseItem.case_id}
              tabIndex={-1}
              onClick={() => setExpanded(open ? null : caseItem.case_id)}
              onKeyDown={(event) => {
                if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return
                event.preventDefault()
                setExpanded(open ? null : caseItem.case_id)
              }}
            >
              <TableCell className="py-1 pl-2"><Button type="button" variant="ghost" size="sm" className="h-auto min-h-7 max-w-full justify-start whitespace-normal py-1 text-left" aria-expanded={open} aria-controls={detailID}>
                {open ? <ChevronDownIcon /> : <ChevronRightIcon />}<span className="line-clamp-2 break-words">{caseItem.name}</span>
              </Button></TableCell>
              <TableCell className="py-1"><VerificationBadge status={caseItem.verification.status} /></TableCell>
              {columns.map(column => <TableCell key={column.id} className="py-1 text-xs tabular-nums">{caseColumn(caseItem, column)}</TableCell>)}
              <TableCell className="py-1 pr-3 text-[11px]"><VerificationCounts summary={caseItem.verification} /></TableCell>
            </TableRow>
            {open ? <TableRow id={detailID} className="hover:bg-transparent"><TableCell colSpan={columns.length + 3} className="bg-muted/20 p-3">
              <div className="mb-2 break-all font-mono text-[10px] text-muted-foreground">{caseItem.key} · {caseItem.case_id}</div>
              <ExecutionDetails results={caseItem.request_results} />
            </TableCell></TableRow> : null}
          </Fragment>
        }) : <TableRow><TableCell colSpan={columns.length + 3} className="py-6 text-center text-xs text-muted-foreground">{t("hierarchy.noRecordedCases")}</TableCell></TableRow>}</TableBody>
      </Table>
    </div>
    <ResultPagination page={page} count={entry.cases.length} size={PAGE_SIZE} onPage={setPage} />
  </>
}

function reportCaseRowID(entryID: string, caseID: string): string {
  return `report-case-${entryID}-${caseID}`
}

export function VerificationBadge({ status }: { status: VerificationStatus }) {
  const { t } = useTranslation("reports")
  const style = status === "failed" ? "border-destructive/25 bg-destructive/5 text-destructive"
    : status === "passed" ? "border-success/25 bg-success-soft text-success-strong"
    : status === "indeterminate" ? "border-warning/25 bg-warning-soft text-warning-strong" : "text-muted-foreground"
  return <Badge variant="outline" className={style}>{t(`protocolDesign.verdict.${status}`)}</Badge>
}

export function VerificationCounts({ summary }: { summary: VerificationSummary }) {
  const { t } = useTranslation("reports")
  return <span className="tabular-nums">{t("protocolDesign.counts", { ...summary })}</span>
}

export function ExecutionDetails({ results }: { results: ReportResult[] }) {
  const { t } = useTranslation("reports")
  const [page, setPage] = useState(0)
  const visible = results.slice(page * DETAIL_PAGE_SIZE, (page + 1) * DETAIL_PAGE_SIZE)
  return <div className="min-w-0 space-y-2">
    {visible.map((result, index) => <details key={result.id} className="min-w-0 border-b pb-2" open={results.length === 1}>
      <summary className="flex cursor-pointer flex-wrap items-center gap-2 rounded-sm py-1 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span>{t("protocolDesign.executionNumber", { count: page * DETAIL_PAGE_SIZE + index + 1 })}</span>
        <span className="text-muted-foreground">{t(`protocolDesign.execution.${result.execution_status}`)}</span>
        <VerificationBadge status={result.verification.status} />
        {result.dimensions?.phase === "warmup" ? <span className="text-muted-foreground">{t("protocolDesign.warmupExecution")}</span> : null}
        {result.observation?.http_status !== undefined ? <span>HTTP {result.observation.http_status}</span> : null}
        {result.error_code ? <span className="break-all text-destructive">{t(`common:errorCode.${result.error_code}`, { defaultValue: result.error_code })}</span> : null}
      </summary>
      <div className="min-w-0 space-y-3 pt-2">
        <div className="break-all font-mono text-[10px] text-muted-foreground">{result.request_id ?? result.id}</div>
        <ExecutionMetrics result={result} />
        {result.verification.assertions.length ? <AssertionTable assertions={result.verification.assertions} /> : <p className="text-xs text-muted-foreground">{t("protocolDesign.observedHint")}</p>}
        {result.observation ? <>
          <ObservationDetails observation={result.observation} />
          {result.observation.exchanges.map((exchange, i) => <details key={i} className="min-w-0"><summary className="cursor-pointer rounded-sm py-1 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring">{exchange.step} · {exchange.method} · {exchange.http_status ?? "—"} · {number(exchange.elapsed_ms)} ms</summary>
            <div className="mt-2 grid min-w-0 gap-3 lg:grid-cols-2"><JSONEvidence label={t("protocolDesign.request")} value={exchange.request_body} /><JSONEvidence label={t("protocolDesign.response")} value={exchange.response} /></div>
          </details>)}
          <JSONEvidence label={t("protocolDesign.observation")} value={{ ...result.observation, exchanges: undefined }} />
        </> : null}
      </div>
    </details>)}
    {!results.length ? <p className="py-2 text-xs text-muted-foreground">{t("protocolDesign.noExecutions")}</p> : null}
    <ResultPagination page={page} count={results.length} size={DETAIL_PAGE_SIZE} onPage={setPage} />
  </div>
}

function ExecutionMetrics({ result }: { result: ReportResult }) {
  const { t } = useTranslation("reports")
  const metrics = [
    ["e2e_ms", "E2E", "ms"], ["ttft_ms", "TTFT", "ms"],
    ["tpot_ms", "TPOT", "ms/token"], ["schedule_lag_ms", t("protocolDesign.queue"), "ms"],
    ["prompt_tokens", t("protocolDesign.inputTokens"), ""],
    ["completion_tokens", t("protocolDesign.outputTokens"), ""],
    ["cached_tokens", t("protocolDesign.cachedTokens"), ""],
  ]
  return <dl aria-label={t("protocolDesign.executionMetrics")} className="grid min-w-0 grid-cols-2 gap-3 sm:grid-cols-4">
    {metrics.map(([key, label, unit]) => <div key={key} className="min-w-0">
      <dt className="text-[10px] text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-xs tabular-nums">{result.metrics[key] === undefined ? "—" : `${number(result.metrics[key])}${unit ? ` ${unit}` : ""}`}</dd>
    </div>)}
  </dl>
}

function ObservationDetails({ observation }: { observation: ProtocolObservation }) {
  const Component = protocolPresentation(observation.protocol).ObservationDetails
  return <Component observation={observation} />
}

function AssertionTable({ assertions }: { assertions: AssertionResult[] }) {
  const { t } = useTranslation("reports")
  return <div className="overflow-x-auto"><Table aria-label={t("protocolDesign.assertions")}>
    <TableHeader><TableRow><TableHead>{t("protocolDesign.rule")}</TableHead><TableHead>{t("protocolDesign.expected")}</TableHead><TableHead>{t("protocolDesign.actual")}</TableHead><TableHead>{t("protocolDesign.verification")}</TableHead></TableRow></TableHeader>
    <TableBody>{assertions.map(assertion => <Fragment key={assertion.id}><TableRow>
      <TableCell className="max-w-60 whitespace-normal text-xs"><div>{assertion.id}</div><div className="break-all font-mono text-[10px] text-muted-foreground">{assertion.source}{assertion.pointer} {assertion.operator}</div></TableCell>
      <TableCell className="max-w-60 whitespace-normal break-all font-mono text-[11px]">{boundedJSON(assertion.expected, 512)}</TableCell>
      <TableCell className="max-w-60 whitespace-normal break-all font-mono text-[11px]">{boundedJSON(assertion.actual, 512)}{assertion.reason ? <div className="text-muted-foreground">{assertion.reason}</div> : null}</TableCell>
      <TableCell><VerificationBadge status={assertion.status} /></TableCell>
    </TableRow>{assertion.children?.length ? <TableRow><TableCell colSpan={4} className="pl-4"><AssertionTable assertions={assertion.children} /></TableCell></TableRow> : null}</Fragment>)}</TableBody>
  </Table></div>
}
function JSONEvidence({ label, value }: { label: string; value: unknown }) {
  if (value === undefined) return null
  return <details className="min-w-0"><summary className="cursor-pointer rounded-sm text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring">{label}</summary><pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 font-mono text-[11px]">{boundedJSON(value, 16000)}</pre></details>
}
function ResultPagination({ page, count, size, onPage }: { page: number; count: number; size: number; onPage: (page: number) => void }) {
  const { t } = useTranslation("reports")
  if (count <= size) return null
  return <div className="flex items-center justify-end gap-2 border-t px-3 py-2 text-xs"><span className="tabular-nums">{page + 1} / {Math.ceil(count / size)}</span><Button variant="outline" size="xs" disabled={!page} onClick={() => onPage(page - 1)}>{t("protocolDesign.previous")}</Button><Button variant="outline" size="xs" disabled={(page + 1) * size >= count} onClick={() => onPage(page + 1)}>{t("protocolDesign.next")}</Button></div>
}
function caseColumn(caseItem: ReportCaseDetail, column: ProtocolColumn): string {
  if (column.metric) {
    const metric = caseItem.metrics[column.metric]
    return metric ? `${number(metric.value)}${metric.unit ? ` ${metric.unit}` : ""}` : "—"
  }
  const observations = caseItem.request_results
    .filter(result => result.dimensions?.phase !== "warmup")
    .flatMap(result => result.observation ? [result.observation] : [])
  const values = observations.flatMap(observation => {
    if (column.observation === "http_status") return observation.http_status === undefined ? [] : [String(observation.http_status)]
    if (column.observation === "task_status") return observation.task?.status ? [observation.task.status] : []
    if (column.observation === "artifacts") return [String(observation.artifacts.length)]
    return []
  })
  if (values.length) return [...new Set(values)].join(" / ")
  return "—"
}
function columnLabel(column: ProtocolColumn, t: ReturnType<typeof useTranslation<"reports">>["t"]) {
  if (column.observation === "task_status") return t("protocolDesign.taskStatus")
  if (column.observation === "artifacts") return t("protocolDesign.artifacts")
  if (column.metric === "completion_tokens") return t("protocolDesign.outputTokens")
  return column.label
}
function number(value: number) { return value.toLocaleString(desktopLocale(), { maximumFractionDigits: 2 }) }
function boundedJSON(value: unknown, limit: number) { const text = value === undefined ? "—" : JSON.stringify(value, null, 2); return text.length > limit ? `${text.slice(0, limit)}…` : text }
