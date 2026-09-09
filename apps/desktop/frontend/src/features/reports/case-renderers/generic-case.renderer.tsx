// oxlint-disable react/only-export-components -- renderer modules export a descriptor for static discovery
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { desktopLocale } from "@/i18n/runtime"
import type { ReportCaseDetail, ReportResult } from "../data"
import type {
  CaseRendererComponentProps,
  CaseRendererContext,
  CaseRendererDefinition,
} from "./types"

const genericCaseRenderer: CaseRendererDefinition = {
  match: { generic: true },
  parse: ({ caseReport }) => caseReport,
  Component: GenericCaseRenderer,
}

export default genericCaseRenderer

function GenericCaseRenderer({ context, payload }: CaseRendererComponentProps) {
  const caseReport = payload as ReportCaseDetail
  return (
    <CaseReportSection context={context}>
      <CaseRequestResults
        results={caseReport.request_results}
        requestLimit={context.requestLimit}
        totalRequestCount={context.totalRequestCount}
      />
    </CaseReportSection>
  )
}

export function CaseReportSection({
  context,
  children,
}: {
  context: CaseRendererContext
  children: ReactNode
}) {
  const { t } = useTranslation("reports")
  const { t: tx } = useTranslation()
  const { caseReport } = context
  const passed = caseReport.summary_result
    ? Object.values(caseReport.summary_result.success).every(Boolean)
    : undefined
  return (
    <section
      aria-label={tx("desktop:reports_case_aria", { value1: caseReport.name })}
      className="min-w-0 overflow-hidden"
    >
      <header className="flex flex-wrap items-center justify-between gap-2 py-2">
        <div className="min-w-0">
          <h4 className="truncate text-xs font-semibold">{caseReport.name}</h4>
          <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
            {caseReport.key}{caseReport.case_type
              ? ` · ${caseReport.case_type}@${caseReport.case_type_version ?? "?"}`
              : ` · ${t("hierarchy.genericRenderer")}`}
          </p>
        </div>
        {passed === undefined ? null : (
          <Badge
            variant="outline"
            className={passed
              ? "border-success/25 bg-success-soft text-success-strong"
              : "border-destructive/25 bg-destructive/5 text-destructive"}
          >
            {t(passed ? "conclusion.passed" : "conclusion.failed")}
          </Badge>
        )}
      </header>
      {children}
    </section>
  )
}

export function CaseRequestResults({
  results,
  requestLimit,
  totalRequestCount = results.length,
  dimensionLabel = genericDimensionLabel,
}: {
  results: ReportResult[]
  requestLimit: number
  totalRequestCount?: number
  dimensionLabel?: (dimensions?: Record<string, string>) => string
}) {
  const { t: tx } = useTranslation()
  const visibleResults = results.slice(0, requestLimit)
  return (
    <div>
      {totalRequestCount > visibleResults.length ? (
        <div role="status" className="px-3 py-2 text-[11px] text-muted-foreground">
          {tx("desktop:reports_showing_the_first_1_000_requests_all")} {formatNumber(totalRequestCount)} {tx("desktop:reports_requests_can_be_exported_as_json")}
        </div>
      ) : null}
      <div className="overflow-x-auto">
        <Table aria-label={tx("desktop:reports_request_level_results")} className="min-w-[900px]">
          <TableHeader><TableRow>
            <TableHead className="h-8 pl-3 text-[11px]">{tx("desktop:quick-test_request")}</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_phase")}</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_status")}</TableHead>
            <TableHead className="h-8 text-[11px]">E2E</TableHead>
            <TableHead className="h-8 text-[11px]">TTFT</TableHead>
            <TableHead className="h-8 text-[11px]">TPOT</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_queue")}</TableHead>
            <TableHead className="h-8 text-[11px]">Token</TableHead>
            <TableHead className="h-8 pr-3 text-[11px]">{tx("desktop:reports_error")}</TableHead>
          </TableRow></TableHeader>
          <TableBody>{visibleResults.length ? visibleResults.map((result) => (
            <TableRow key={result.id} className="h-9">
              <TableCell className="py-1 pl-3 font-mono text-[10px]">{result.request_id ?? result.id}</TableCell>
              <TableCell className="py-1 text-[10px] tabular-nums">{dimensionLabel(result.dimensions)}</TableCell>
              <TableCell className="py-1"><RequestConclusionBadge result={result} /></TableCell>
              <RequestMetricCell value={result.metrics.e2e_ms} unit="ms" />
              <RequestMetricCell value={result.metrics.ttft_ms} unit="ms" />
              <RequestMetricCell value={result.metrics.tpot_ms} unit="ms" />
              <RequestMetricCell value={result.metrics.schedule_lag_ms} unit="ms" />
              <TableCell className="py-1 text-xs tabular-nums">{metric(result.metrics.prompt_tokens)} / {metric(result.metrics.completion_tokens)}</TableCell>
              <TableCell className="py-1 pr-3 text-xs text-destructive">{result.error_code ?? "—"}</TableCell>
            </TableRow>
          )) : (
            <TableRow><TableCell colSpan={9} className="h-20 text-center text-xs text-muted-foreground">
              {tx("desktop:reports_this_report_has_no_request_level_results")}
            </TableCell></TableRow>
          )}</TableBody>
        </Table>
      </div>
    </div>
  )
}

function RequestConclusionBadge({ result }: { result: ReportResult }) {
  const { t } = useTranslation("reports")
  const passed = Object.values(result.success).every(Boolean)
  return (
    <Badge
      variant="outline"
      className={passed
        ? "border-success/25 bg-success-soft text-success-strong"
        : "border-destructive/25 bg-destructive/5 text-destructive"}
    >
      {t(passed ? "conclusion.passed" : "conclusion.failed")}
    </Badge>
  )
}

function RequestMetricCell({ value, unit }: { value?: number; unit: string }) {
  return <TableCell className="py-1 text-xs tabular-nums">{metric(value)} {value === undefined ? "" : unit}</TableCell>
}

function genericDimensionLabel(dimensions?: Record<string, string>): string {
  if (dimensions?.input_tokens_target) {
    return `${dimensions.input_tokens_target} token · #${dimensions.sample ?? "—"}/${dimensions.stage_samples ?? "—"}`
  }
  return "—"
}

function metric(value?: number): string {
  return value === undefined ? "—" : formatNumber(value)
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 2 }).format(value)
}
