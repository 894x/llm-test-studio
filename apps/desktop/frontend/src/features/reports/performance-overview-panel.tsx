import CheckIcon from "lucide-react/dist/esm/icons/check.mjs"
import MinusIcon from "lucide-react/dist/esm/icons/minus.mjs"
import StarIcon from "lucide-react/dist/esm/icons/star.mjs"
import XIcon from "lucide-react/dist/esm/icons/x.mjs"
import { useTranslation } from "react-i18next"
import type { ReactNode } from "react"

import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { PerformanceOverview, PerformanceOverviewMetric, PerformanceOverviewStatus } from "./performance-overview"

export function PerformanceOverviewPanel({ overview, children }: { overview: PerformanceOverview; children?: ReactNode }) {
  const { t, i18n } = useTranslation("reports")
  const number = (value: number, digits = 0) => new Intl.NumberFormat(i18n.resolvedLanguage ?? i18n.language, { maximumFractionDigits: digits }).format(value)
  return <section aria-label={t("headline.performance")} className="min-w-0 space-y-3">
    <header className="flex flex-wrap items-center justify-between gap-2">
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        <h3 className="text-sm font-semibold">{t("headline.performance")}</h3>
        <PerformanceOverviewRating overview={overview} />
      </div>
      <span className="text-[11px] text-muted-foreground">{t("headline.samples", { count: overview.sample_count, value: number(overview.sample_count) })}</span>
    </header>
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs">
      <Badge variant="secondary" data-performance-status={overview.status} className={overview.status === "failed" ? "bg-destructive-soft text-destructive" : overview.status === "passed" ? "bg-success-soft text-outcome-pass" : overview.status === "not_evaluated" ? "bg-warning-soft text-warning-strong" : "bg-surface-control text-text-secondary"}>
        {t(`headline.overviewStatus.${overview.status}`)}
        <PerformanceMetricStatusIcon status={overview.status} />
      </Badge>
      {overview.evaluated_count > 0 ? <span className="tabular-nums"><strong className="font-semibold">{number(overview.passed_count)} / {number(overview.evaluated_count)}</strong> {t("headline.itemsPassed")}</span> : null}
      {overview.observed_count > 0 ? <span className="text-muted-foreground">{t("headline.observedItems", { count: overview.observed_count })}</span> : null}
      {overview.unevaluated_count > 0 ? <span className="text-muted-foreground">{t("headline.unevaluatedItems", { count: overview.unevaluated_count })}</span> : null}
      {children}
    </div>
    <dl className="grid min-w-0 grid-cols-2 gap-x-3 gap-y-5 rounded-lg bg-surface-subtle px-4 py-4 @min-[560px]:grid-cols-3 @min-[800px]:grid-cols-6">
      {overview.metrics.map(metric => <PerformanceOverviewValue key={metric.key} metric={metric} />)}
    </dl>
    <p className="text-[11px] text-muted-foreground">{t("headline.scope")}</p>
  </section>
}

function PerformanceOverviewRating({ overview }: { overview: PerformanceOverview }) {
  const { t, i18n } = useTranslation("reports")
  const rating = overview.rating
  if (!rating) return <span className="text-xs text-muted-foreground">{t("headline.rating.unavailable")}</span>
  const score = new Intl.NumberFormat(i18n.resolvedLanguage ?? i18n.language, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(rating.score)
  return <Tooltip>
    <TooltipTrigger asChild>
      <span tabIndex={0} role="img" aria-label={t("headline.rating.accessible", { score, count: rating.metric_count })}
        className="inline-flex max-w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-md text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="text-muted-foreground">{t("headline.rating.label")}</span>
        <span aria-hidden="true" className="inline-flex shrink-0 gap-0.5">
          {Array.from({ length: rating.max_score }, (_, index) => <span key={index} className="relative inline-flex size-4 text-surface-active">
            <StarIcon className="size-4 fill-current" />
            <span className="absolute inset-y-0 left-0 overflow-hidden text-warning" style={{ width: `${Math.min(1, Math.max(0, rating.score - index)) * 100}%` }}>
              <StarIcon className="size-4 fill-current" />
            </span>
          </span>)}
        </span>
        <strong aria-hidden="true" className="font-semibold tabular-nums">{score}</strong>
        <span aria-hidden="true" className="text-[11px] text-muted-foreground">{t("headline.rating.count", { count: rating.metric_count })}</span>
      </span>
    </TooltipTrigger>
    <TooltipContent className="max-w-sm text-[11px] leading-relaxed">{t("headline.rating.rule")}</TooltipContent>
  </Tooltip>
}

function PerformanceOverviewValue({ metric }: { metric: PerformanceOverviewMetric }) {
  const { t, i18n } = useTranslation("reports")
  const number = (value: number, minimumFractionDigits: number, maximumFractionDigits = minimumFractionDigits) => new Intl.NumberFormat(i18n.resolvedLanguage ?? i18n.language, { minimumFractionDigits, maximumFractionDigits }).format(value)
  const format = (value: number) => metric.unit === "ms" && value >= 1000
    ? { value: number(value / 1000, 2), unit: "s" }
    : { value: number(value, metric.unit === "percent" ? 1 : 2), unit: metric.unit === "percent" ? "%" : metric.unit }
  const value = metric.value === null ? null : format(metric.value)
  const target = metric.target === null ? null : format(metric.target)
  const color = metric.status === "passed" ? "text-outcome-pass" : metric.status === "failed" ? "text-outcome-fail" : "text-foreground"
  const statusColor = metric.status === "passed" ? "text-outcome-pass" : metric.status === "failed" ? "text-outcome-fail" : "text-muted-foreground"
  return <div className="-m-1.5 min-w-0 space-y-3 rounded-md p-1.5 transition-colors duration-200 hover:bg-surface-hover motion-reduce:transition-none [overflow-wrap:anywhere]" data-performance-metric={metric.key}>
    <dt className="text-xs text-muted-foreground">{t(`headline.metrics.${metric.key}`)}</dt>
    <dd className={`flex min-w-0 flex-wrap items-baseline gap-x-1 text-2xl font-semibold tabular-nums ${color}`}>
      {value ? <>{value.value}<span className="text-xs font-normal">{value.unit}</span></> : "—"}
    </dd>
    <dd className={`flex items-center gap-1 text-[11px] ${statusColor}`}><PerformanceMetricStatusIcon status={metric.status} />{t(`headline.metricStatus.${metric.status}`)}</dd>
    <dd className="text-[11px] tabular-nums text-muted-foreground">{target ? <>{t(metric.reference ? "headline.reference" : "headline.target")} {metric.comparison === "at_least" ? "≥" : metric.comparison === "less_than" ? "<" : "≤"} {target.value}{target.unit === "%" ? "" : " "}{target.unit}</> : t("headline.noTarget")}</dd>
  </div>
}

function PerformanceMetricStatusIcon({ status }: { status: PerformanceOverviewStatus }) {
  if (status === "passed") return <CheckIcon className="size-3.5 shrink-0" aria-hidden="true" />
  if (status === "failed") return <XIcon className="size-3.5 shrink-0" aria-hidden="true" />
  return <MinusIcon className="size-3.5 shrink-0" aria-hidden="true" />
}
