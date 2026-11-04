import CheckIcon from "lucide-react/dist/esm/icons/check.mjs"
import CircleAlertIcon from "lucide-react/dist/esm/icons/circle-alert.mjs"
import MinusIcon from "lucide-react/dist/esm/icons/minus.mjs"
import XIcon from "lucide-react/dist/esm/icons/x.mjs"
import { useTranslation } from "react-i18next"
import type { ReactNode } from "react"

import { Badge } from "@/components/ui/badge"
import type { PerformanceOverview, PerformanceOverviewMetric, PerformanceOverviewStatus } from "./performance-overview"

export function PerformanceOverviewPanel({ overview, children }: { overview: PerformanceOverview; children?: ReactNode }) {
  const { t, i18n } = useTranslation("reports")
  const number = (value: number, digits = 0) => new Intl.NumberFormat(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: digits }).format(value)
  return <section aria-label={t("headline.performance")} className="min-w-0 space-y-3">
    <header className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-semibold">{t("headline.performance")}</h3>
      <span className="text-[11px] text-muted-foreground">{t("headline.samples", { count: overview.sample_count, value: number(overview.sample_count) })}</span>
    </header>
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs">
      <Badge variant="secondary" className={overview.status === "failed" ? "bg-destructive-soft text-destructive" : overview.status === "not_evaluated" ? "bg-warning-soft text-warning-strong" : "bg-surface-control text-text-secondary"}>
        {overview.status === "failed" ? <CircleAlertIcon aria-hidden="true" className="size-3.5" /> : null}
        {t(`headline.overviewStatus.${overview.status}`)}
      </Badge>
      {overview.evaluated_count > 0 ? <span className="tabular-nums"><strong className="font-semibold">{number(overview.passed_count)} / {number(overview.evaluated_count)}</strong> {t("headline.itemsPassed")}</span> : null}
      {overview.observed_count > 0 ? <span className="text-muted-foreground">{t("headline.observedItems", { count: overview.observed_count })}</span> : null}
      {overview.unevaluated_count > 0 ? <span className="text-muted-foreground">{t("headline.unevaluatedItems", { count: overview.unevaluated_count })}</span> : null}
      {children}
    </div>
    <dl className="grid min-w-0 grid-cols-2 gap-x-5 gap-y-5 rounded-lg bg-surface-subtle px-4 py-4 @min-[560px]:grid-cols-3 @min-[1000px]:grid-cols-6">
      {overview.metrics.map(metric => <PerformanceOverviewValue key={metric.key} metric={metric} />)}
    </dl>
    <p className="text-[11px] text-muted-foreground">{t("headline.scope")}</p>
  </section>
}

function PerformanceOverviewValue({ metric }: { metric: PerformanceOverviewMetric }) {
  const { t, i18n } = useTranslation("reports")
  const number = (value: number, minimumFractionDigits: number, maximumFractionDigits = minimumFractionDigits) => new Intl.NumberFormat(i18n.resolvedLanguage ?? i18n.language, { minimumFractionDigits, maximumFractionDigits }).format(value)
  const format = (value: number) => metric.unit === "ms" && value >= 1000
    ? { value: number(value / 1000, 2), unit: "s" }
    : { value: number(value, metric.unit === "percent" ? 1 : 2), unit: metric.unit === "percent" ? "%" : metric.unit }
  const value = metric.value === null ? null : format(metric.value)
  const target = metric.target === null ? null : format(metric.target)
  const color = metric.status === "failed" ? "text-outcome-fail" : "text-foreground"
  const statusColor = metric.status === "passed" ? "text-outcome-pass" : metric.status === "failed" ? "text-outcome-fail" : "text-muted-foreground"
  return <div className="min-w-0 space-y-3 [overflow-wrap:anywhere]" data-performance-metric={metric.key}>
    <dt className="text-xs text-muted-foreground">{t(`headline.metrics.${metric.key}`)}</dt>
    <dd className={`flex min-w-0 flex-wrap items-baseline gap-x-1 text-2xl font-semibold tabular-nums ${color}`}>
      {value ? <>{value.value}<span className="text-xs font-normal">{value.unit}</span></> : "—"}
    </dd>
    <dd className={`flex items-center gap-1 text-[11px] ${statusColor}`}><PerformanceMetricStatusIcon status={metric.status} />{t(`headline.metricStatus.${metric.status}`)}</dd>
    <dd className="text-[11px] tabular-nums text-muted-foreground">{target ? <>{t(metric.reference ? "headline.reference" : "headline.target")} {metric.comparison === "at_least" ? "≥" : "≤"} {target.value}{target.unit === "%" ? "" : " "}{target.unit}</> : t("headline.noTarget")}</dd>
  </div>
}

function PerformanceMetricStatusIcon({ status }: { status: PerformanceOverviewStatus }) {
  if (status === "passed") return <CheckIcon className="size-3.5 shrink-0" aria-hidden="true" />
  if (status === "failed") return <XIcon className="size-3.5 shrink-0" aria-hidden="true" />
  return <MinusIcon className="size-3.5 shrink-0" aria-hidden="true" />
}
