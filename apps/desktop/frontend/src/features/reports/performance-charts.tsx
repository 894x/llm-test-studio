export interface PerformanceChartSample {
  request_index: number
  finished_offset_ms: number
  e2e_ms: number
  ttft_ms: number
  tpot_ms: number
  success: boolean
}

export interface PerformanceChartPercentiles {
  ttft_p50_ms: number
  ttft_p95_ms: number
  tpot_p50_ms: number
  tpot_p95_ms: number
  e2e_p50_ms: number
  e2e_p95_ms: number
}

interface MetricDefinition {
  key: "ttft_ms" | "tpot_ms" | "e2e_ms"
  label: "TTFT" | "TPOT" | "E2E"
  unit: "ms" | "ms/token"
  p50: keyof PerformanceChartPercentiles
  p95: keyof PerformanceChartPercentiles
}

const METRICS: MetricDefinition[] = [
  { key: "ttft_ms", label: "TTFT", unit: "ms", p50: "ttft_p50_ms", p95: "ttft_p95_ms" },
  { key: "tpot_ms", label: "TPOT", unit: "ms/token", p50: "tpot_p50_ms", p95: "tpot_p95_ms" },
  { key: "e2e_ms", label: "E2E", unit: "ms", p50: "e2e_p50_ms", p95: "e2e_p95_ms" },
]

export function PerformanceCharts({ samples, percentiles, layout = "grid" }: {
  samples: readonly PerformanceChartSample[]
  percentiles: PerformanceChartPercentiles
  layout?: "grid" | "stacked"
}) {
  const stacked = layout === "stacked"
  return (
    <section aria-label="性能图表" className="space-y-3">
      <div>
        <h4 className="text-xs font-semibold">延迟分布</h4>
        <p className="mt-0.5 text-[10px] text-muted-foreground">仅统计成功请求；每项指标使用独立纵轴，TPOT 单位为 ms/token。</p>
        <div data-testid="distribution-chart-list" className={stacked ? "mt-2 grid grid-cols-1 gap-2" : "mt-2 grid gap-2 sm:grid-cols-3"}>
          {METRICS.map((metric) => (
            <DistributionChart key={metric.key} metric={metric} samples={samples} percentiles={percentiles} expanded={stacked} />
          ))}
        </div>
      </div>
      <div>
        <h4 className="text-xs font-semibold">延迟时间曲线</h4>
        <p className="mt-0.5 text-[10px] text-muted-foreground">仅展示成功请求；横轴为完成偏移，虚线标记整体 P50 / P95。</p>
        <div data-testid="timeline-chart-list" className={stacked ? "mt-2 grid grid-cols-1 gap-2" : "mt-2 grid gap-2 sm:grid-cols-3"}>
          {METRICS.map((metric) => (
            <TimelineChart key={metric.key} metric={metric} samples={samples} percentiles={percentiles} expanded={stacked} />
          ))}
        </div>
      </div>
    </section>
  )
}

function DistributionChart({ metric, samples, percentiles, expanded }: {
  metric: MetricDefinition
  samples: readonly PerformanceChartSample[]
  percentiles: PerformanceChartPercentiles
  expanded: boolean
}) {
  const values = metricValues(samples, metric.key)
  const p50 = percentiles[metric.p50]
  const p95 = percentiles[metric.p95]
  const max = Math.max(1, ...values, p95)
  const bins = histogram(values, max, 10)
  const peak = Math.max(1, ...bins)
  const width = expanded ? 520 : 240
  const height = expanded ? 144 : 112
  const left = expanded ? 36 : 30
  const right = expanded ? 12 : 8
  const top = 10
  const bottom = expanded ? 24 : 22
  const plotWidth = width - left - right
  const plotHeight = height - top - bottom
  return (
    <figure aria-label={`${metric.label} 分布图`} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="flex items-baseline justify-between gap-2 text-[11px] font-medium">
        <span>{metric.label}</span>
        <span className="truncate text-[9px] font-normal tabular-nums text-muted-foreground">P50 {number(p50)} · P95 {number(p95)} {metric.unit}</span>
      </figcaption>
      {values.length ? (
        <svg role="img" aria-label={`${metric.label} 延迟直方分布，${values.length} 个成功请求样本`} viewBox={`0 0 ${width} ${height}`} className={expanded ? "mt-1 h-36 w-full overflow-visible" : "mt-1 h-28 w-full overflow-visible"}>
          <line x1={left} y1={top + plotHeight} x2={left + plotWidth} y2={top + plotHeight} stroke="var(--border-strong)" />
          {bins.map((count, index) => {
            const gap = 2
            const barWidth = plotWidth / bins.length - gap
            const barHeight = count / peak * plotHeight
            return <rect key={index} x={left + index * plotWidth / bins.length + gap / 2} y={top + plotHeight - barHeight} width={Math.max(1, barWidth)} height={barHeight} rx="1" fill="var(--primary)" opacity="0.58" />
          })}
          <PercentileMarker value={p50} max={max} left={left} top={top} width={plotWidth} height={plotHeight} label="P50" />
          <PercentileMarker value={p95} max={max} left={left} top={top} width={plotWidth} height={plotHeight} label="P95" strong />
          <ChartYAxis max={peak} left={left} top={top} height={plotHeight} />
          <ChartXAxis left={left} top={top} width={plotWidth} height={plotHeight} end={number(max)} label={metric.unit} />
        </svg>
      ) : <EmptyChart expanded={expanded} />}
    </figure>
  )
}

function TimelineChart({ metric, samples, percentiles, expanded }: {
  metric: MetricDefinition
  samples: readonly PerformanceChartSample[]
  percentiles: PerformanceChartPercentiles
  expanded: boolean
}) {
  const points = samples
    .filter((sample) => sample.success && Number.isFinite(sample[metric.key]) && sample[metric.key] > 0)
    .map((sample) => ({ x: sample.finished_offset_ms, y: sample[metric.key] }))
    .sort((a, b) => a.x - b.x)
  const p50 = percentiles[metric.p50]
  const p95 = percentiles[metric.p95]
  const maxX = Math.max(1, ...points.map((point) => point.x))
  const maxY = Math.max(1, p95, ...points.map((point) => point.y))
  const width = expanded ? 520 : 240
  const height = expanded ? 144 : 112
  const left = expanded ? 36 : 30
  const right = expanded ? 12 : 8
  const top = 10
  const bottom = expanded ? 24 : 22
  const plotWidth = width - left - right
  const plotHeight = height - top - bottom
  const path = points.map((point) => `${left + point.x / maxX * plotWidth},${top + plotHeight - point.y / maxY * plotHeight}`).join(" ")
  return (
    <figure aria-label={`${metric.label} 时间曲线`} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="flex items-baseline justify-between gap-2 text-[11px] font-medium">
        <span>{metric.label}</span>
        <span className="truncate text-[9px] font-normal tabular-nums text-muted-foreground">完成时间 · {metric.unit}</span>
      </figcaption>
      {points.length ? (
        <svg role="img" aria-label={`${metric.label} 随完成时间变化曲线，${points.length} 个成功请求样本`} viewBox={`0 0 ${width} ${height}`} className={expanded ? "mt-1 h-36 w-full overflow-visible" : "mt-1 h-28 w-full overflow-visible"}>
          <line x1={left} y1={top + plotHeight} x2={left + plotWidth} y2={top + plotHeight} stroke="var(--border-strong)" />
          <HorizontalMarker value={p50} max={maxY} left={left} top={top} width={plotWidth} height={plotHeight} label="P50" />
          <HorizontalMarker value={p95} max={maxY} left={left} top={top} width={plotWidth} height={plotHeight} label="P95" strong />
          <polyline points={path} fill="none" stroke="var(--primary)" strokeWidth="1.5" strokeLinejoin="round" />
          {points.map((point, index) => <circle key={`${point.x}-${index}`} cx={left + point.x / maxX * plotWidth} cy={top + plotHeight - point.y / maxY * plotHeight} r="2" fill="var(--surface-elevated)" stroke="var(--primary)" strokeWidth="1.2" />)}
          <ChartYAxis max={maxY} left={left} top={top} height={plotHeight} />
          <ChartXAxis left={left} top={top} width={plotWidth} height={plotHeight} end={time(maxX)} label="完成偏移" />
        </svg>
      ) : <EmptyChart expanded={expanded} />}
    </figure>
  )
}

function PercentileMarker({ value, max, left, top, width, height, label, strong = false }: {
  value: number; max: number; left: number; top: number; width: number; height: number; label: string; strong?: boolean
}) {
  const x = left + Math.min(1, value / max) * width
  return <g><line x1={x} y1={top} x2={x} y2={top + height} stroke={strong ? "var(--warning)" : "var(--text-secondary)"} strokeDasharray="3 3" /><text x={x + 2} y={top + 8} fill={strong ? "var(--warning-strong)" : "var(--muted-foreground)"} fontSize="7">{label}</text></g>
}

function HorizontalMarker({ value, max, left, top, width, height, label, strong = false }: {
  value: number; max: number; left: number; top: number; width: number; height: number; label: string; strong?: boolean
}) {
  const y = top + height - Math.min(1, value / max) * height
  return <g><line x1={left} y1={y} x2={left + width} y2={y} stroke={strong ? "var(--warning)" : "var(--text-secondary)"} strokeDasharray="3 3" /><text x={left + 2} y={Math.max(top + 7, y - 2)} fill={strong ? "var(--warning-strong)" : "var(--muted-foreground)"} fontSize="7">{label}</text></g>
}

function ChartYAxis({ max, left, top, height }: { max: number; left: number; top: number; height: number }) {
  return <g fill="var(--muted-foreground)" fontSize="8"><text x={left - 3} y={top + 7} textAnchor="end">{number(max)}</text><text x={left - 3} y={top + height} textAnchor="end">0</text></g>
}

function ChartXAxis({ left, top, width, height, end, label }: { left: number; top: number; width: number; height: number; end: string; label: string }) {
  return <g fill="var(--muted-foreground)" fontSize="8"><text x={left} y={top + height + 12}>{label}</text><text x={left + width} y={top + height + 12} textAnchor="end">{end}</text></g>
}

function EmptyChart({ expanded }: { expanded: boolean }) {
  return <div className={`flex ${expanded ? "h-36" : "h-28"} items-center justify-center text-[10px] text-muted-foreground`}>暂无成功请求样本</div>
}

function metricValues(samples: readonly PerformanceChartSample[], key: MetricDefinition["key"]): number[] {
  return samples.filter((sample) => sample.success).map((sample) => sample[key]).filter((value) => Number.isFinite(value) && value > 0)
}

function histogram(values: readonly number[], max: number, binCount: number): number[] {
  const bins = Array<number>(binCount).fill(0)
  for (const value of values) bins[Math.min(binCount - 1, Math.floor(value / max * binCount))] += 1
  return bins
}

function number(value: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value)
}

function time(value: number): string {
  return value >= 1_000 ? `${number(value / 1_000)} s` : `${number(value)} ms`
}
