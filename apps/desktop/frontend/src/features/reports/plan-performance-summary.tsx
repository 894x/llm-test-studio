import { useTranslation } from "react-i18next"
import type { ReportPerformanceSummary } from "./data"
import { formatPerformanceInteger } from "./performance-format"

export function PlanPerformanceSummary({ summary }: { summary: ReportPerformanceSummary }) {
  const { t } = useTranslation("reports")
  const formatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 })
  const number = (value: number | null | undefined, suffix = "") => value == null ? "—" : `${formatter.format(value)}${suffix}`
  const metrics = [
    [t("performanceSummary.requests"), formatPerformanceInteger(summary.completed)],
    [t("performance.success"), formatPerformanceInteger(summary.succeeded)],
    [t("performance.failed"), formatPerformanceInteger(summary.failed)],
    [t("performance.successRate"), number(summary.success_rate_percent, "%")],
    [t("performanceSummary.input"), formatPerformanceInteger(summary.prompt_tokens)],
    [t("performanceSummary.output"), formatPerformanceInteger(summary.completion_tokens)],
    [t("performanceSummary.cached"), formatPerformanceInteger(summary.cached_tokens)],
    [t("performance.cacheRate"), number(summary.cache_rate_percent, "%")],
    [t("performanceSummary.duration"), number(summary.total_duration_ms / 1_000, " s")],
    [t("performanceSummary.throughput"), number(summary.successful_request_qps, " req/s")],
    ["TTFT P95", number(summary.ttft?.p95_ms, " ms")],
    ["E2E P95", number(summary.e2e?.p95_ms, " ms")],
  ]
  return <section aria-label={t("performanceSummary.title")} className="space-y-3 py-2">
    <div className="flex flex-wrap items-baseline justify-between gap-2">
      <h3 className="text-sm font-semibold">{t("performanceSummary.title")}</h3>
      <span className="text-[11px] text-muted-foreground">{t("performanceSummary.items", { measured: summary.measured_item_count, count: summary.item_count, completed: summary.completed_item_count })}</span>
    </div>
    <p className="text-[11px] text-muted-foreground">{t("performanceSummary.scope")}</p>
    {summary.completed_item_count < summary.item_count ? <p role="status" className="text-[11px] text-warning">{t("performanceSummary.partial")}</p> : null}
    <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-3 xl:grid-cols-4">
      {metrics.map(([label, value]) => <div key={label} className="min-w-0">
        <dt className="text-[11px] text-muted-foreground">{label}</dt>
        <dd className="mt-1 break-words font-semibold tabular-nums">{value}</dd>
      </div>)}
    </dl>
    {!summary.samples_complete ? <p className="text-[11px] text-muted-foreground">{t("performanceSummary.missingSamples")}</p> : null}
  </section>
}
