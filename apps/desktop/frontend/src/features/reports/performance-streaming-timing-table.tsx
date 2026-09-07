import { desktopLocale } from "@/i18n/runtime"
import { useTranslation } from "react-i18next"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { QuickPerformanceMetrics, QuickPerformanceSchemaVersion } from "@/features/quick-test/data"
import { formatPerformanceInteger } from "./performance-format"

interface StreamingTimingRow {
  label: string
  unit: "ms" | ""
  samples: number
  average: number
  p50: number
  p95: number
  p99: number
}

export function PerformanceStreamingTimingTable({ schemaVersion, metrics }: {
  schemaVersion: QuickPerformanceSchemaVersion
  metrics: QuickPerformanceMetrics
}) {
  const { t: tx } = useTranslation()
  if (schemaVersion !== 3 || metrics.ttfb_samples === undefined) return null
  const rows: StreamingTimingRow[] = [
    latencyRow("TTFB", metrics.ttfb_samples, metrics.ttfb_average_ms, metrics.ttfb_p50_ms, metrics.ttfb_p95_ms, metrics.ttfb_p99_ms),
    latencyRow(tx("desktop:quick-test_ttft_includes_reasoning"), metrics.ttft_any_samples, metrics.ttft_any_average_ms, metrics.ttft_any_p50_ms, metrics.ttft_any_p95_ms, metrics.ttft_any_p99_ms),
    latencyRow(tx("desktop:quick-test_ttft_visible_content"), metrics.ttft_visible_samples, metrics.ttft_visible_average_ms, metrics.ttft_visible_p50_ms, metrics.ttft_visible_p95_ms, metrics.ttft_visible_p99_ms),
    latencyRow(tx("desktop:quick-test_ttst_second_semantic_chunk"), metrics.ttst_samples, metrics.ttst_average_ms, metrics.ttst_p50_ms, metrics.ttst_p95_ms, metrics.ttst_p99_ms),
    latencyRow(tx("desktop:quick-test_observed_icl_semantic_chunk_interval_not_token_itl"), metrics.observed_icl_samples, metrics.observed_icl_average_ms, metrics.observed_icl_p50_ms, metrics.observed_icl_p95_ms, metrics.observed_icl_p99_ms),
    {
      label: tx("desktop:quick-test_semantic_chunks"),
      unit: "",
      samples: metrics.semantic_chunk_count_samples ?? 0,
      average: metrics.semantic_chunk_count_average ?? 0,
      p50: metrics.semantic_chunk_count_p50 ?? 0,
      p95: metrics.semantic_chunk_count_p95 ?? 0,
      p99: metrics.semantic_chunk_count_p99 ?? 0,
    },
  ]

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <h4 className="text-xs font-semibold">{tx("desktop:reports_streaming_timing")}</h4>
        <span className="text-[10px] text-muted-foreground">{tx("desktop:reports_observed_icl_measures_semantic_chunk_intervals_not_token_itl")}</span>
      </div>
      <ScrollArea className="w-full rounded-md border">
        <Table aria-label={tx("desktop:reports_streaming_timing_statistics")} className="min-w-[680px] text-xs">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="h-8 pl-3 text-[11px]">{tx("desktop:reports_metric")}</TableHead>
              {[tx("desktop:reports_average"), "P50", "P95", "P99", tx("reports:inspector.samples")].map((label) => <TableHead key={label} className="h-8 text-right text-[11px]">{label}</TableHead>)}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.label}>
                <TableCell className="py-2 pl-3 font-medium">{row.label}</TableCell>
                {[row.average, row.p50, row.p95, row.p99].map((value, index) => (
                  <TableCell key={index} className="py-2 text-right tabular-nums">{formatStreamingValue(value, row.samples, row.unit)}</TableCell>
                ))}
                <TableCell className="py-2 text-right tabular-nums">{row.samples === 0 ? "—" : formatNumber(row.samples)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ScrollArea>
    </div>
  )
}

function latencyRow(
  label: string,
  samples: number | undefined,
  average: number | undefined,
  p50: number | undefined,
  p95: number | undefined,
  p99: number | undefined,
): StreamingTimingRow {
  return { label, unit: "ms", samples: samples ?? 0, average: average ?? 0, p50: p50 ?? 0, p95: p95 ?? 0, p99: p99 ?? 0 }
}

function formatStreamingValue(value: number, samples: number, unit: StreamingTimingRow["unit"]): string {
  if (samples === 0) return "—"
  return unit === "ms" ? `${formatPerformanceInteger(value)} ms` : formatNumber(value)
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 2 }).format(value)
}
