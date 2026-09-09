// oxlint-disable react/only-export-components -- renderer modules export a descriptor for static discovery
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { desktopLocale } from "@/i18n/runtime"
import {
  CaseReportSection,
  CaseRequestResults,
} from "./generic-case.renderer"
import type {
  CaseRendererComponentProps,
  CaseRendererDefinition,
} from "./types"

interface ResponseProbeDistribution {
  case_id: string
  bucket: string
  classification: "matched" | "unknown" | "ambiguous" | "failed"
  format: string
  shape: string
  count: number
  share_percent: number
}

interface ResponseProbePayload {
  distributions: ResponseProbeDistribution[]
}

const responseProbeRenderer: CaseRendererDefinition = {
  match: { caseType: "response.probe" },
  caseTypeVersions: [1],
  dataVersions: [1],
  parse: ({ suite, caseReport }) => ({
    distributions: suite.distributions
      .filter((item) => item.kind === "response_probe" && item.case_id === caseReport.case_id)
      .map(parseProbeDistribution),
  } satisfies ResponseProbePayload),
  Component: ResponseProbeRenderer,
}

export default responseProbeRenderer

function ResponseProbeRenderer({ context, payload }: CaseRendererComponentProps) {
  const parsed = payload as ResponseProbePayload
  return (
    <CaseReportSection context={context}>
      {parsed.distributions.length ? <ResponseProbeDistributionTable distributions={parsed.distributions} /> : null}
      <CaseRequestResults
        results={context.caseReport.request_results}
        requestLimit={context.requestLimit}
        totalRequestCount={context.totalRequestCount}
        dimensionLabel={probeDimensionLabel}
      />
    </CaseReportSection>
  )
}

function parseProbeDistribution(value: Readonly<Record<string, unknown>>): ResponseProbeDistribution {
  if (
    typeof value.case_id !== "string" || !value.case_id ||
    typeof value.bucket !== "string" || !value.bucket.trim() ||
    !isProbeClassification(value.classification) ||
    typeof value.format !== "string" || typeof value.shape !== "string" ||
    !Number.isSafeInteger(value.count) || (value.count as number) <= 0 ||
    typeof value.share_percent !== "number" || !Number.isFinite(value.share_percent) ||
    value.share_percent < 0 || value.share_percent > 100 ||
    (value.classification !== "failed" && (!value.format.trim() || !value.shape.trim()))
  ) {
    throw new Error("invalid response probe distribution")
  }
  return {
    case_id: value.case_id,
    bucket: value.bucket,
    classification: value.classification,
    format: value.format,
    shape: value.shape,
    count: value.count as number,
    share_percent: value.share_percent,
  }
}

function isProbeClassification(value: unknown): value is ResponseProbeDistribution["classification"] {
  return value === "matched" || value === "unknown" || value === "ambiguous" || value === "failed"
}

function ResponseProbeDistributionTable({ distributions }: { distributions: ResponseProbeDistribution[] }) {
  const { t: tx } = useTranslation()
  return (
    <section aria-label={tx("desktop:reports_upstream_response_probe_statistics")} className="border-b px-3 py-3">
      <div className="mb-2">
        <h5 className="text-xs font-semibold">{tx("desktop:reports_upstream_response_distribution")}</h5>
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          {tx("desktop:reports_grouped_by_response_body_signatures_and_structure_fingerprints_unknown_means")}
        </p>
      </div>
      <div className="min-w-0 overflow-x-auto">
        <Table aria-label={tx("desktop:reports_upstream_response_distribution")} className="min-w-[720px]">
          <TableHeader><TableRow>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_category_label")}</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_classification")}</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_format")}</TableHead>
            <TableHead className="h-8 text-[11px]">{tx("desktop:reports_structure_fingerprint")}</TableHead>
            <TableHead className="h-8 text-right text-[11px]">{tx("desktop:catalog_request_count")}</TableHead>
            <TableHead className="h-8 pr-3 text-right text-[11px]">{tx("desktop:reports_share")}</TableHead>
          </TableRow></TableHeader>
          <TableBody>{distributions.map((distribution) => (
            <TableRow key={`${distribution.case_id}:${distribution.bucket}:${distribution.shape}`} className="h-9">
              <TableCell className="py-1 text-xs font-medium">{distribution.bucket}</TableCell>
              <TableCell className="py-1"><ProbeClassificationBadge classification={distribution.classification} /></TableCell>
              <TableCell className="py-1 font-mono text-[10px]">{distribution.format || "—"}</TableCell>
              <TableCell className="py-1 font-mono text-[10px]">{distribution.shape || "—"}</TableCell>
              <TableCell className="py-1 text-right text-xs tabular-nums">{formatNumber(distribution.count)}</TableCell>
              <TableCell className="py-1 pr-3 text-right text-xs tabular-nums">{formatNumber(distribution.share_percent)}%</TableCell>
            </TableRow>
          ))}</TableBody>
        </Table>
      </div>
    </section>
  )
}

function ProbeClassificationBadge({ classification }: { classification: ResponseProbeDistribution["classification"] }) {
  const { t: tx } = useTranslation()
  const label = classification === "matched"
    ? tx("desktop:reports_matched")
    : classification === "unknown"
      ? tx("desktop:reports_unknown_format")
      : classification === "ambiguous"
        ? tx("desktop:reports_ambiguous_rules")
        : tx("desktop:reports_request_failed")
  const tone = classification === "matched"
    ? "border-success/25 bg-success-soft text-success-strong"
    : classification === "failed"
      ? "border-destructive/25 bg-destructive/5 text-destructive"
      : "border-warning/30 bg-warning-soft text-warning-strong"
  return <Badge variant="outline" className={tone}>{label}</Badge>
}

function probeDimensionLabel(dimensions?: Record<string, string>): string {
  return dimensions?.probe_bucket
    ? `${dimensions.probe_bucket} · ${dimensions.probe_classification ?? "—"}`
    : "—"
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 2 }).format(value)
}
