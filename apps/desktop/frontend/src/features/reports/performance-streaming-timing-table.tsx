import { ScrollArea } from "@/components/ui/scroll-area"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { QuickPerformanceMetrics, QuickPerformanceSchemaVersion } from "@/features/quick-test/data"

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
  if (schemaVersion !== 3 || metrics.ttfb_samples === undefined) return null
  const rows: StreamingTimingRow[] = [
    latencyRow("TTFB", metrics.ttfb_samples, metrics.ttfb_average_ms, metrics.ttfb_p50_ms, metrics.ttfb_p95_ms, metrics.ttfb_p99_ms),
    latencyRow("TTFT（含推理）", metrics.ttft_any_samples, metrics.ttft_any_average_ms, metrics.ttft_any_p50_ms, metrics.ttft_any_p95_ms, metrics.ttft_any_p99_ms),
    latencyRow("TTFT（可见内容）", metrics.ttft_visible_samples, metrics.ttft_visible_average_ms, metrics.ttft_visible_p50_ms, metrics.ttft_visible_p95_ms, metrics.ttft_visible_p99_ms),
    latencyRow("TTST（第二语义块）", metrics.ttst_samples, metrics.ttst_average_ms, metrics.ttst_p50_ms, metrics.ttst_p95_ms, metrics.ttst_p99_ms),
    latencyRow("Observed ICL（语义块间隔，非 Token ITL）", metrics.observed_icl_samples, metrics.observed_icl_average_ms, metrics.observed_icl_p50_ms, metrics.observed_icl_p95_ms, metrics.observed_icl_p99_ms),
    {
      label: "语义块数",
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
        <h4 className="text-xs font-semibold">流式时序</h4>
        <span className="text-[10px] text-muted-foreground">Observed ICL 是语义块间隔，不是 Token ITL</span>
      </div>
      <ScrollArea className="w-full rounded-md border">
        <Table aria-label="流式时序统计" className="min-w-[680px] text-xs">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="h-8 pl-3 text-[11px]">指标</TableHead>
              {["平均", "P50", "P95", "P99", "Samples"].map((label) => <TableHead key={label} className="h-8 text-right text-[11px]">{label}</TableHead>)}
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
  return `${formatNumber(value)}${unit ? ` ${unit}` : ""}`
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value)
}
