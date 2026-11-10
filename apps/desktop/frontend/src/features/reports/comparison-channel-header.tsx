import { useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { TableHead } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import "./report-comparison-view.css"

export function ComparisonTableColumns({ channels }: { channels: number }) {
  const columns = useRef<HTMLTableColElement>(null)
  const [caseWidth, setCaseWidth] = useState(220)
  useLayoutEffect(() => {
    const table = columns.current?.parentElement
    if (!table) return
    const measure = () => setCaseWidth(Math.max(220, table.clientWidth - channels * 104))
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(table)
    return () => observer.disconnect()
  }, [channels])
  return <colgroup ref={columns}><col style={{ width: caseWidth }} /><col span={channels} /></colgroup>
}

export function ComparisonChannelHeader({ channel, model, baseline = false, exporting = false }: {
  channel: string
  model?: string
  baseline?: boolean
  exporting?: boolean
}) {
  const { t } = useTranslation("reports")
  const container = useRef<HTMLDivElement>(null)
  const channelText = useRef<HTMLSpanElement>(null)
  const modelText = useRef<HTMLSpanElement>(null)
  const [tilted, setTilted] = useState(false)
  useLayoutEffect(() => {
    const element = container.current
    if (!element) return
    const measure = () => setTilted(Math.max(channelText.current?.offsetWidth ?? 0, modelText.current?.offsetWidth ?? 0) > element.clientWidth)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    if (channelText.current) observer.observe(channelText.current)
    if (modelText.current) observer.observe(modelText.current)
    return () => observer.disconnect()
  }, [channel, model])
  return <TableHead className="whitespace-normal py-3 align-top [overflow-wrap:anywhere]">
    <div ref={container} className="report-comparison-channel" data-tilted={tilted}><div className="report-comparison-channel-heading">
      <Tooltip><TooltipTrigger asChild><div className="report-comparison-channel-label rounded-sm outline-none focus-visible:outline-2 focus-visible:outline-ring" tabIndex={exporting ? undefined : 0}>
        <div className="report-comparison-channel-name font-medium"><span ref={channelText}>{channel}</span></div>
        {model ? <div className="report-comparison-channel-model font-normal text-muted-foreground"><span ref={modelText}>{model}</span></div> : null}
      </div></TooltipTrigger><TooltipContent className="whitespace-normal [overflow-wrap:anywhere]">{model ? `${channel} · ${model}` : channel}</TooltipContent></Tooltip>
      {baseline ? <span className="report-comparison-channel-baseline mt-1 block">{t("compare.baseline")}</span> : null}
    </div></div>
  </TableHead>
}
