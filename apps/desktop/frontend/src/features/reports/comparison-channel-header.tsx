import { useLayoutEffect, useRef, useState, type CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { TableHead } from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import "./report-comparison-view.css"

export const COMPARISON_RESULT_COLUMN_WIDTH = 68

export function ComparisonTableColumns({ channels, collapsed = [], sharedSeverity = false }: { channels: number; collapsed?: boolean[]; sharedSeverity?: boolean }) {
  const columns = useRef<HTMLTableColElement>(null)
  const [caseWidth, setCaseWidth] = useState(220)
  const channelWidth = Array.from({ length: channels }, (_, index) => collapsed[index] ? 32 : sharedSeverity ? COMPARISON_RESULT_COLUMN_WIDTH : 104).reduce((sum, width) => sum + width, sharedSeverity ? 64 : 0)
  useLayoutEffect(() => {
    const table = columns.current?.parentElement
    if (!table) return
    const measure = () => setCaseWidth(Math.max(220, table.clientWidth - channelWidth))
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(table)
    return () => observer.disconnect()
  }, [channelWidth])
  // Image export copies computed widths; a spanning col reports all of its columns combined.
  return <colgroup ref={columns}><col style={{ width: caseWidth }} />{sharedSeverity ? <col style={{ width: 64 }} /> : null}{Array.from({ length: channels }, (_, index) => <col key={index} style={collapsed[index] ? { width: 32 } : undefined} />)}</colgroup>
}

export function ComparisonChannelHeader({ channel, baseline = false, exporting = false, collapsed = false, onToggle, disabled = false }: {
  channel: string
  baseline?: boolean
  exporting?: boolean
  collapsed?: boolean
  onToggle?: () => void
  disabled?: boolean
}) {
  const { t } = useTranslation("reports")
  const container = useRef<HTMLDivElement>(null)
  const channelText = useRef<HTMLSpanElement>(null)
  const [measurement, setMeasurement] = useState({ tilted: false, width: 0, height: 14, availableWidth: 0 })
  useLayoutEffect(() => {
    const element = container.current
    if (!element) return
    const measure = () => {
      const width = channelText.current?.offsetWidth ?? 0
      const height = channelText.current?.offsetHeight ?? 14
      const availableWidth = element.clientWidth
      const tilted = width > availableWidth
      setMeasurement(previous => previous.tilted === tilted && previous.width === width && previous.height === height && previous.availableWidth === availableWidth
        ? previous : { tilted, width, height, availableWidth })
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    if (channelText.current) observer.observe(channelText.current)
    return () => observer.disconnect()
  }, [channel])
  const angle = Math.PI / 3
  const rotatedLineWidth = measurement.height * Math.sin(angle)
  const availableTextWidth = Math.max(1, (measurement.availableWidth - 8 - rotatedLineWidth) / Math.cos(angle))
  const labelWidth = Math.min(measurement.width, 124, collapsed ? 124 : availableTextWidth)
  const headingStyle = {
    "--comparison-label-width": `${labelWidth}px`,
    "--comparison-label-left": `calc(50% + ${(rotatedLineWidth - labelWidth * Math.cos(angle)) / 2}px)`,
    "--comparison-label-bottom": onToggle && !exporting ? "4px" : "8px",
    "--comparison-heading-height": `${Math.ceil(collapsed ? labelWidth
      : labelWidth * Math.sin(angle) + measurement.height * Math.cos(angle))}px`,
  } as CSSProperties
  const label = <span className="report-comparison-channel-label block rounded-sm outline-none focus-visible:outline-2 focus-visible:outline-ring" tabIndex={exporting || onToggle ? undefined : 0}>
    <span className="report-comparison-channel-name block font-medium"><span ref={channelText}>{channel}</span></span>
  </span>
  return <TableHead data-comparison-baseline={baseline} className={collapsed ? "relative whitespace-normal px-0 py-1 align-bottom" : "relative whitespace-normal py-1 align-bottom [overflow-wrap:anywhere]"}>
    <div ref={container} className="report-comparison-channel" style={headingStyle} data-tilted={measurement.tilted || collapsed} data-collapsed={collapsed}>
      {baseline ? <span aria-hidden="true" className={`pointer-events-none absolute inset-y-1 rounded-md bg-surface-active ${collapsed ? "inset-x-0" : "inset-x-2"}`} /> : null}
      <div className="report-comparison-channel-heading">
        <Tooltip><TooltipTrigger asChild>{onToggle && !exporting ? <Button variant="ghost" className={`absolute inset-y-1 h-auto w-auto whitespace-normal p-0 text-xs ${collapsed ? "inset-x-0" : "inset-x-2"}`} disabled={disabled}
          aria-expanded={!collapsed} aria-label={t(collapsed ? "comparison.expand" : "comparison.collapse", { channel })} onClick={onToggle}>{label}</Button> : label}</TooltipTrigger><TooltipContent className="whitespace-normal [overflow-wrap:anywhere]">{channel}{baseline ? ` · ${t("compare.baseline")}` : ""}</TooltipContent></Tooltip>
      </div>
    </div>
  </TableHead>
}
