import { useLayoutEffect, useRef, useState } from "react"
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
  const [tilted, setTilted] = useState(false)
  useLayoutEffect(() => {
    const element = container.current
    if (!element) return
    const measure = () => setTilted((channelText.current?.offsetWidth ?? 0) > element.clientWidth)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    if (channelText.current) observer.observe(channelText.current)
    return () => observer.disconnect()
  }, [channel])
  const label = <span className="report-comparison-channel-label block rounded-sm outline-none focus-visible:outline-2 focus-visible:outline-ring" tabIndex={exporting || onToggle ? undefined : 0}>
    <span className="report-comparison-channel-name block font-medium"><span ref={channelText}>{channel}</span></span>
  </span>
  return <TableHead data-comparison-baseline={baseline} className={collapsed ? "whitespace-normal px-0 py-3 align-top" : "whitespace-normal py-3 align-top [overflow-wrap:anywhere]"}>
    <div ref={container} className="report-comparison-channel" data-tilted={tilted || collapsed} data-collapsed={collapsed}><div className="report-comparison-channel-heading">
      <Tooltip><TooltipTrigger asChild>{onToggle && !exporting ? <Button variant="ghost" className="absolute inset-0 h-full w-full whitespace-normal p-0 text-xs" disabled={disabled}
        aria-expanded={!collapsed} aria-label={t(collapsed ? "comparison.expand" : "comparison.collapse", { channel })} onClick={onToggle}>{label}</Button> : label}</TooltipTrigger><TooltipContent className="whitespace-normal [overflow-wrap:anywhere]">{channel}{baseline ? ` · ${t("compare.baseline")}` : ""}</TooltipContent></Tooltip>
    </div></div>
  </TableHead>
}
