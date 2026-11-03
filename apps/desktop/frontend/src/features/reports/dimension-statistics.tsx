import { useTranslation } from "react-i18next"

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { desktopLocale } from "@/i18n/runtime"
import type { ReportDimensionStatistic } from "./data"

export function DimensionStatistics({ statistics }: { statistics?: ReportDimensionStatistic[] }) {
  const { t } = useTranslation("reports")
  if (!statistics?.length) return null
  const names = t("dimensions.names", { returnObjects: true }) as Record<string, string>
  const dimensionName = (dimension: string) => Object.hasOwn(names, dimension) ? names[dimension] : dimension
  const radius = Math.max(102, statistics.length * 6)
  const center = { x: radius + 140, y: radius + 52 }
  const point = (index: number, distance: number) => {
    const angle = -Math.PI / 2 + index * Math.PI * 2 / statistics.length
    return { x: center.x + Math.cos(angle) * distance, y: center.y + Math.sin(angle) * distance }
  }
  const coordinates = (distance: number) => statistics.map((_, index) => {
    const p = point(index, distance)
    return `${p.x},${p.y}`
  }).join(" ")
  const values = statistics.map((item, index) => item.pass_rate_percent === null ? null : point(index, radius * item.pass_rate_percent / 100))
  const complete = statistics.length >= 3 && values.every(value => value !== null)
  const rateLabel = (item: ReportDimensionStatistic) => item.pass_rate_percent === null
    ? t("dimensions.noData") : `${item.pass_rate_percent.toLocaleString(desktopLocale(), { maximumFractionDigits: 2 })}%`
  return <figure aria-label={t("dimensions.title")} className="min-w-0">
    <figcaption className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-xs font-semibold">{t("dimensions.title")}</h3>
      <span className="text-[10px] text-muted-foreground">{t("dimensions.scale")}</span>
    </figcaption>
    <TooltipProvider delayDuration={150}>
      <svg role="group" aria-label={t("dimensions.radar")} viewBox={`0 0 ${center.x * 2} ${radius * 2 + 116}`}
        className="mx-auto block max-h-[360px] w-full">
        <desc>{t("dimensions.scope")}</desc>
        {[25, 50, 75, 100].map(rate => statistics.length >= 3
          ? <polygon key={rate} points={coordinates(radius * rate / 100)} fill="none" stroke="var(--border-strong)" strokeWidth="0.7" />
          : <circle key={rate} cx={center.x} cy={center.y} r={radius * rate / 100} fill="none" stroke="var(--border-strong)" strokeWidth="0.7" />)}
        {statistics.map((item, index) => {
          const edge = point(index, radius)
          return <line key={item.dimension} x1={center.x} y1={center.y} x2={edge.x} y2={edge.y} stroke="var(--border-strong)" strokeWidth="0.7" />
        })}
        {complete ? <polygon data-radar-area points={values.map(value => `${value!.x},${value!.y}`).join(" ")}
          fill="var(--primary)" fillOpacity="0.16" stroke="var(--primary)" strokeWidth="1.8" strokeLinejoin="round" />
          : values.map((value, index) => {
            const next = values[(index + 1) % values.length]
            if (!value || !next || values.length < 2 || (values.length === 2 && index === 1)) return null
            return <line key={index} x1={value.x} y1={value.y} x2={next.x} y2={next.y} stroke="var(--primary)" strokeWidth="1.8" />
          })}
        {[25, 50, 75, 100].map(rate => <text key={rate} x={center.x + 4} y={center.y - radius * rate / 100 + 10}
          fontSize="8" fill="var(--muted-foreground)">{rate}%</text>)}
        {statistics.map((item, index) => {
          const label = point(index, radius + 30)
          const lines = dimensionLabelLines(dimensionName(item.dimension))
          const anchor = Math.abs(label.x - center.x) < 1 ? "middle" : label.x > center.x ? "start" : "end"
          const description = t("dimensions.detail", { ...item, dimension: dimensionName(item.dimension), rate: rateLabel(item) })
          const value = values[index]
          return <Tooltip key={item.dimension}>
            <TooltipTrigger asChild>
              <g tabIndex={0} role="img" aria-label={description} className="group outline-none">
                {value ? <circle data-radar-point cx={value.x} cy={value.y} r="3.5"
                  fill="var(--primary)" stroke="var(--background)" strokeWidth="1.3"
                  className="group-focus-visible:stroke-ring group-focus-visible:stroke-[3]" /> : null}
                <text x={label.x} y={label.y - (lines.length - 1) * 6} textAnchor={anchor} fontSize="12"
                  fill="var(--muted-foreground)" className="group-focus-visible:fill-foreground group-focus-visible:underline">
                  {lines.map((line, lineIndex) => <tspan key={lineIndex} x={label.x} dy={lineIndex ? 12 : 0}>{line}</tspan>)}
                  <tspan x={label.x} dy="14" fill={value ? "var(--foreground)" : "var(--muted-foreground)"} fontWeight="600">{rateLabel(item)}</tspan>
                </text>
              </g>
            </TooltipTrigger>
            <TooltipContent className="max-w-80 whitespace-normal [overflow-wrap:anywhere]">{description}</TooltipContent>
          </Tooltip>
        })}
      </svg>
    </TooltipProvider>
  </figure>
}

function dimensionLabelLines(value: string): string[] {
  const lineLength = 14
  const words = value.split(/(?<=[-_ ])/u)
  const lines: string[] = []
  for (const word of words) {
    const last = lines.length - 1
    if (last >= 0 && lines[last].length + word.length <= lineLength) lines[last] += word
    else lines.push(word)
  }
  const wrapped = lines.flatMap(line => Array.from(line).reduce<string[]>((parts, character, index) => {
    if (index % lineLength === 0) parts.push("")
    parts[parts.length - 1] += character
    return parts
  }, []))
  return wrapped.length > 3 ? [...wrapped.slice(0, 2), `${wrapped[2].slice(0, lineLength - 1)}…`] : wrapped
}
