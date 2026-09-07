export interface PerformanceChartSample {
  request_index: number
  started_offset_ms: number
  finished_offset_ms: number
  e2e_ms: number
  ttft_ms: number
  tpot_ms: number
  success: boolean
  prompt_tokens?: number
  cached_tokens?: number
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
  label: string
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
  const { t } = useTranslation("reports")
  const metrics = METRICS.map((metric) => ({ ...metric, label: metric.key === "ttft_ms" ? t("desktop:quick-test_ttft_includes_reasoning") : metric.label }))
  const stacked = layout === "stacked"
  const timeline = throughputTimeline(samples)
  return (
    <section aria-label={t("charts.aria")} className="@container space-y-3">
      <div>
        <h4 className="text-xs font-semibold">{t("charts.distribution")}</h4>
        <p className="mt-0.5 text-[10px] text-muted-foreground">{t("charts.distributionHint")}</p>
        <div data-testid="distribution-chart-list" className={stacked ? "mt-2 grid grid-cols-1 gap-2" : "mt-2 grid gap-2 sm:grid-cols-3"}>
          {metrics.map((metric) => (
            <DistributionChart key={metric.key} metric={metric} samples={samples} percentiles={percentiles} expanded={stacked} />
          ))}
        </div>
      </div>
      <div>
        <h4 className="text-xs font-semibold">{t("charts.timeline")}</h4>
        <p className="mt-0.5 text-[10px] text-muted-foreground">{t("charts.timelineHint")}</p>
        <div data-testid="timeline-chart-list" className={stacked ? "mt-2 grid grid-cols-1 gap-2" : "mt-2 grid gap-2 sm:grid-cols-3"}>
          {metrics.map((metric) => (
            <TimelineChart key={metric.key} metric={metric} samples={samples} percentiles={percentiles} expanded={stacked} />
          ))}
        </div>
      </div>
      <div data-testid="throughput-cache-chart-list" className="grid grid-cols-1 gap-3 @min-[720px]:grid-cols-2">
        <div className="flex min-w-0 flex-col">
          <h4 className="text-xs font-semibold">{t("charts.throughput")}</h4>
          <p className="mt-0.5 mb-2 flex-1 text-[10px] text-muted-foreground">{t("charts.throughputHint")}</p>
          <ThroughputConcurrencyChart samples={samples} timeline={timeline} />
        </div>
        <div className="flex min-w-0 flex-col">
          <h4 className="text-xs font-semibold">{t("charts.cacheTimeline")}</h4>
          <p className="mt-0.5 mb-2 flex-1 text-[10px] text-muted-foreground">{t("charts.cacheTimelineHint")}</p>
          <CacheRateTimelineChart timeline={timeline} />
        </div>
      </div>
    </section>
  )
}

function ThroughputConcurrencyChart({ samples, timeline }: {
  samples: readonly PerformanceChartSample[]
  timeline: ReturnType<typeof throughputTimeline>
}) {
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  if (!timeline) {
    return <figure aria-label={t("charts.throughput")} className="rounded-md border bg-surface-control p-2"><EmptyChart expanded /></figure>
  }
  const { buckets, durationMS, maxThroughput, maxConcurrency } = timeline
  const width = 480
  const height = 176
  const left = 42
  const right = 42
  const top = 18
  const bottom = 26
  const plotWidth = width - left - right
  const plotHeight = height - top - bottom
  const slotWidth = plotWidth / buckets.length
  const points = buckets.map((bucket, index) => {
    const x = left + (index + 0.5) * slotWidth
    const y = top + plotHeight - bucket.peakInFlight / Math.max(1, maxConcurrency) * plotHeight
    return `${x},${y}`
  }).join(" ")
  return (
    <figure aria-label={t("charts.throughput")} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-[10px] text-muted-foreground">
        <span className="flex items-center gap-3">
          <span className="inline-flex items-center gap-1.5"><span className="size-2 rounded-sm bg-primary/50" />{t("charts.completedThroughput")}</span>
          <span className="inline-flex items-center gap-1.5"><span className="w-3 border-t-2 border-text-secondary" />{t("charts.peakInFlight")}</span>
        </span>
        <span className="tabular-nums">{t("charts.peakSummary", { throughput: number(maxThroughput, locale), concurrency: number(maxConcurrency, locale) })}</span>
      </figcaption>
      <svg role="img" aria-label={t("charts.completedRequests", { count: samples.length })} viewBox={`0 0 ${width} ${height}`} className="mt-1 h-44 w-full overflow-visible">
        <line x1={left} y1={top + plotHeight} x2={left + plotWidth} y2={top + plotHeight} stroke="var(--border-strong)" />
        {buckets.map((bucket, index) => {
          const barHeight = bucket.throughput / Math.max(1, maxThroughput) * plotHeight
          return <rect key={index} x={left + index * slotWidth + 1} y={top + plotHeight - barHeight} width={Math.max(1, slotWidth - 2)} height={barHeight} rx="1" fill="var(--primary)" opacity="0.5" />
        })}
        <polyline points={points} fill="none" stroke="var(--text-secondary)" strokeWidth="1.8" strokeLinejoin="round" />
        {buckets.map((bucket, index) => <circle key={index} cx={left + (index + 0.5) * slotWidth} cy={top + plotHeight - bucket.peakInFlight / Math.max(1, maxConcurrency) * plotHeight} r="1.8" fill="var(--surface-elevated)" stroke="var(--text-secondary)" strokeWidth="1.1" />)}
        <g fill="var(--muted-foreground)" fontSize="8">
          <text x={left - 3} y={top + 7} textAnchor="end">{number(maxThroughput, locale)}</text>
          <text x={left - 3} y={top + plotHeight} textAnchor="end">0</text>
          <text x={left + plotWidth + 3} y={top + 7}>{number(maxConcurrency, locale)}</text>
          <text x={left + plotWidth + 3} y={top + plotHeight}>0</text>
          <text x={left} y={top - 6}>req/s</text>
          <text x={left + plotWidth} y={top - 6} textAnchor="end">{t("charts.inFlight")}</text>
          <text x={left} y={top + plotHeight + 14}>{t("charts.completionOffset")}</text>
          <text x={left + plotWidth} y={top + plotHeight + 14} textAnchor="end">{time(durationMS, locale)}</text>
        </g>
      </svg>
    </figure>
  )
}

interface TimelineBucket {
  throughput: number
  peakInFlight: number
  promptTokens: number
  cachedTokens: number
}

function throughputTimeline(samples: readonly PerformanceChartSample[]): {
  buckets: TimelineBucket[]
  durationMS: number
  maxThroughput: number
  maxConcurrency: number
} | null {
  const valid = samples.filter((sample) => Number.isFinite(sample.started_offset_ms) && Number.isFinite(sample.finished_offset_ms) && sample.started_offset_ms >= 0 && sample.finished_offset_ms >= sample.started_offset_ms)
  const durationMS = Math.max(0, ...valid.map((sample) => sample.finished_offset_ms))
  if (!valid.length || durationMS <= 0) return null
  const bucketCount = Math.min(48, Math.max(8, Math.ceil(durationMS / 1_000)))
  const bucketMS = durationMS / bucketCount
  const completionCounts = Array<number>(bucketCount).fill(0)
  const cacheCounts = Array.from({ length: bucketCount }, () => ({ promptTokens: 0, cachedTokens: 0 }))
  for (const sample of valid) {
    const index = Math.min(bucketCount - 1, Math.floor(sample.finished_offset_ms / bucketMS))
    completionCounts[index] += 1
    const prompt = sample.prompt_tokens
    const cached = sample.cached_tokens
    if (sample.success && prompt !== undefined && cached !== undefined &&
      Number.isSafeInteger(prompt) && prompt > 0 && Number.isSafeInteger(cached) && cached >= 0 && cached <= prompt) {
      cacheCounts[index].promptTokens += prompt
      cacheCounts[index].cachedTokens += cached
    }
  }
  const events = valid.filter((sample) => sample.finished_offset_ms > sample.started_offset_ms).flatMap((sample) => [
    { offset: sample.started_offset_ms, delta: 1 },
    { offset: sample.finished_offset_ms, delta: -1 },
  ]).sort((left, right) => left.offset - right.offset || left.delta - right.delta)
  const buckets: TimelineBucket[] = []
  let active = 0
  let eventIndex = 0
  for (let bucketIndex = 0; bucketIndex < bucketCount; bucketIndex += 1) {
    const bucketEnd = (bucketIndex + 1) * bucketMS
    let peakInFlight = active
    while (eventIndex < events.length && events[eventIndex].offset <= bucketEnd) {
      active = Math.max(0, active + events[eventIndex].delta)
      peakInFlight = Math.max(peakInFlight, active)
      eventIndex += 1
    }
    buckets.push({ throughput: completionCounts[bucketIndex] / (bucketMS / 1_000), peakInFlight, ...cacheCounts[bucketIndex] })
  }
  return {
    buckets,
    durationMS,
    maxThroughput: Math.max(0, ...buckets.map((bucket) => bucket.throughput)),
    maxConcurrency: Math.max(0, ...buckets.map((bucket) => bucket.peakInFlight)),
  }
}

function CacheRateTimelineChart({ timeline }: { timeline: ReturnType<typeof throughputTimeline> }) {
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
  const width = 480
  const height = 176
  const left = 42
  const right = 42
  const top = 18
  const bottom = 26
  const plotWidth = width - left - right
  const plotHeight = height - top - bottom
  const points = timeline?.buckets.map((bucket, index) => bucket.promptTokens > 0 ? {
    ...bucket,
    index,
    rate: bucket.cachedTokens / bucket.promptTokens * 100,
    x: left + (index + 0.5) / timeline.buckets.length * plotWidth,
    y: top + plotHeight * (1 - bucket.cachedTokens / bucket.promptTokens),
  } : null) ?? []
  // Empty windows break the curve; they are neither zero hits nor interpolated data.
  const path = points.map((point, index) => point ? `${index > 0 && points[index - 1] ? "L" : "M"} ${point.x},${point.y}` : "").join(" ")
  const available = points.filter((point) => point !== null)
  return (
    <figure aria-label={t("charts.cacheTimeline")} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="text-[10px] text-muted-foreground">{t("charts.cacheRatio")}</figcaption>
      {timeline && available.length ? (
        <svg role="img" aria-label={t("charts.cacheWindows", { count: available.length })} viewBox={`0 0 ${width} ${height}`} className="mt-1 h-44 w-full overflow-visible">
          {[0, 50, 100].map((rate) => {
            const y = top + plotHeight * (1 - rate / 100)
            return <g key={rate}>
              <line x1={left} y1={y} x2={left + plotWidth} y2={y} stroke="var(--border-strong)" strokeDasharray={rate === 0 ? undefined : "3 3"} />
              <text x={left - 3} y={y + 3} textAnchor="end" fill="var(--muted-foreground)" fontSize="8">{rate}%</text>
            </g>
          })}
          <path d={path} fill="none" stroke="var(--primary)" strokeWidth="1.8" strokeLinejoin="round" />
          {available.map((point) => <circle key={point.index} cx={point.x} cy={point.y} r="2.5" fill="var(--surface-elevated)" stroke="var(--primary)" strokeWidth="1.2">
            <title>{t("charts.cacheWindow", {
              start: time(point.index / timeline.buckets.length * timeline.durationMS, locale),
              end: time((point.index + 1) / timeline.buckets.length * timeline.durationMS, locale),
              rate: number(point.rate, locale, 2), cached: number(point.cachedTokens, locale), prompt: number(point.promptTokens, locale),
            })}</title>
          </circle>)}
          <ChartXAxis left={left} top={top} width={plotWidth} height={plotHeight} end={time(timeline.durationMS, locale)} label={t("charts.completionOffset")} />
        </svg>
      ) : <div className="mt-1 flex h-44 items-center justify-center text-[10px] text-muted-foreground">{t("charts.cacheEmpty")}</div>}
    </figure>
  )
}

function DistributionChart({ metric, samples, percentiles, expanded }: {
  metric: MetricDefinition
  samples: readonly PerformanceChartSample[]
  percentiles: PerformanceChartPercentiles
  expanded: boolean
}) {
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
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
    <figure aria-label={t("charts.distributionAria", { metric: metric.label })} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="flex items-baseline justify-between gap-2 text-[11px] font-medium">
        <span>{metric.label}</span>
        <span className="truncate text-[9px] font-normal tabular-nums text-muted-foreground">P50 {number(p50, locale)} · P95 {number(p95, locale)} {metric.unit}</span>
      </figcaption>
      {values.length ? (
        <svg role="img" aria-label={t("charts.histogramAria", { metric: metric.label, count: values.length })} viewBox={`0 0 ${width} ${height}`} className={expanded ? "mt-1 h-36 w-full overflow-visible" : "mt-1 h-28 w-full overflow-visible"}>
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
          <ChartXAxis left={left} top={top} width={plotWidth} height={plotHeight} end={number(max, locale)} label={metric.unit} />
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
  const { t, i18n } = useTranslation("reports")
  const locale = i18n.resolvedLanguage ?? i18n.language
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
    <figure aria-label={t("charts.curveAria", { metric: metric.label })} className="min-w-0 rounded-md border bg-surface-control p-2">
      <figcaption className="flex items-baseline justify-between gap-2 text-[11px] font-medium">
        <span>{metric.label}</span>
        <span className="truncate text-[9px] font-normal tabular-nums text-muted-foreground">{t("charts.completionTime", { unit: metric.unit })}</span>
      </figcaption>
      {points.length ? (
        <svg role="img" aria-label={t("charts.curveSamples", { metric: metric.label, count: points.length })} viewBox={`0 0 ${width} ${height}`} className={expanded ? "mt-1 h-36 w-full overflow-visible" : "mt-1 h-28 w-full overflow-visible"}>
          <line x1={left} y1={top + plotHeight} x2={left + plotWidth} y2={top + plotHeight} stroke="var(--border-strong)" />
          <HorizontalMarker value={p50} max={maxY} left={left} top={top} width={plotWidth} height={plotHeight} label="P50" />
          <HorizontalMarker value={p95} max={maxY} left={left} top={top} width={plotWidth} height={plotHeight} label="P95" strong />
          <polyline points={path} fill="none" stroke="var(--primary)" strokeWidth="1.5" strokeLinejoin="round" />
          {points.map((point, index) => <circle key={`${point.x}-${index}`} cx={left + point.x / maxX * plotWidth} cy={top + plotHeight - point.y / maxY * plotHeight} r="2" fill="var(--surface-elevated)" stroke="var(--primary)" strokeWidth="1.2" />)}
          <ChartYAxis max={maxY} left={left} top={top} height={plotHeight} />
          <ChartXAxis left={left} top={top} width={plotWidth} height={plotHeight} end={time(maxX, locale)} label={t("charts.completionOffset")} />
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
  const { i18n } = useTranslation()
  return <g fill="var(--muted-foreground)" fontSize="8"><text x={left - 3} y={top + 7} textAnchor="end">{number(max, i18n.resolvedLanguage ?? i18n.language)}</text><text x={left - 3} y={top + height} textAnchor="end">0</text></g>
}

function ChartXAxis({ left, top, width, height, end, label }: { left: number; top: number; width: number; height: number; end: string; label: string }) {
  return <g fill="var(--muted-foreground)" fontSize="8"><text x={left} y={top + height + 12}>{label}</text><text x={left + width} y={top + height + 12} textAnchor="end">{end}</text></g>
}

function EmptyChart({ expanded }: { expanded: boolean }) {
  const { t } = useTranslation("reports")
  return <div className={`flex ${expanded ? "h-36" : "h-28"} items-center justify-center text-[10px] text-muted-foreground`}>{t("charts.empty")}</div>
}

function metricValues(samples: readonly PerformanceChartSample[], key: MetricDefinition["key"]): number[] {
  return samples.filter((sample) => sample.success).map((sample) => sample[key]).filter((value) => Number.isFinite(value) && value > 0)
}

function histogram(values: readonly number[], max: number, binCount: number): number[] {
  const bins = Array<number>(binCount).fill(0)
  for (const value of values) bins[Math.min(binCount - 1, Math.floor(value / max * binCount))] += 1
  return bins
}

function number(value: number, locale: string, maximumFractionDigits = 0): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits }).format(value)
}

function time(value: number, locale: string): string {
  return value >= 1_000 ? `${number(value / 1_000, locale, 2)} s` : `${number(value, locale)} ms`
}
import { useTranslation } from "react-i18next"
