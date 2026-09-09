import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { QuickPerformanceMetrics } from "@/features/quick-test/data"
import { useTranslation } from "react-i18next"
import { formatPerformanceInteger } from "./performance-format"

export function PerformanceLatencyTable({ metrics }: { metrics: QuickPerformanceMetrics }) {
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const rows = [
    { label: tx("desktop:quick-test_ttft_includes_reasoning"), unit: "ms", values: [metrics.ttft_average_ms, metrics.ttft_p50_ms, metrics.ttft_p90_ms, metrics.ttft_p95_ms, metrics.ttft_p99_ms] },
    { label: "TPOT", unit: "ms/token", values: [metrics.tpot_average_ms, metrics.tpot_p50_ms, metrics.tpot_p90_ms, metrics.tpot_p95_ms, metrics.tpot_p99_ms] },
    { label: "E2E", unit: "ms", values: [metrics.e2e_average_ms, metrics.e2e_p50_ms, metrics.e2e_p90_ms, metrics.e2e_p95_ms, metrics.e2e_p99_ms] },
    { label: t("latency.clientQueue"), unit: "ms", values: [metrics.schedule_lag_average_ms, metrics.schedule_lag_p50_ms, metrics.schedule_lag_p90_ms, metrics.schedule_lag_p95_ms, metrics.schedule_lag_p99_ms] },
  ]
  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold">{t("latency.title")}</h4>
      <ScrollArea className="w-full">
        <Table aria-label={t("latency.aria")} className="min-w-[560px] text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="h-8 pl-3 text-[11px]">{t("latency.metric")}</TableHead>
              {[t("latency.average"), "P50", "P90", "P95", "P99"].map((label) => <TableHead key={label} className="h-8 text-right text-[11px]">{label}</TableHead>)}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.label}>
                <TableCell className="py-2 pl-3 font-medium">{row.label} <span className="font-normal text-muted-foreground">({row.unit})</span></TableCell>
                {row.values.map((value, index) => <TableCell key={index} className="py-2 text-right tabular-nums">{formatPerformanceInteger(value, locale)}</TableCell>)}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ScrollArea>
    </div>
  )
}
