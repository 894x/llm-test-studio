import { useRef, useState, type ReactNode } from "react"
import RotateCcwIcon from "lucide-react/dist/esm/icons/rotate-ccw.mjs"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { useTranslation } from "react-i18next"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { InspectorHeader, InspectorRow } from "@/features/shell/page-frame"
import type { ComparisonSummary } from "@/features/comparisons/data"
import type { ReportSnapshot } from "./data"
import { channelVerdict } from "./report-list-items"

export function ComparisonReportInspector({ comparison, snapshot, onOpen, onRerun, disabled = false, exportControls, recycleAction }: {
  comparison: ComparisonSummary
  snapshot: ReportSnapshot
  onOpen?: () => void
  onRerun?: () => Promise<void>
  disabled?: boolean
  exportControls: ReactNode
  recycleAction?: ReactNode
}) {
  const { t } = useTranslation("reports")
  const { t: tr } = useTranslation("runs")
  const [pending, setPending] = useState(false)
  const [failed, setFailed] = useState(false)
  const inFlight = useRef(false)
  const rerun = async () => {
    if (!onRerun || inFlight.current) return
    inFlight.current = true
    setPending(true)
    setFailed(false)
    try { await onRerun() } catch { setFailed(true) }
    finally { inFlight.current = false; setPending(false) }
  }
  const rerunLabel = tr(pending ? "shortcuts.starting" : "shortcuts.rerun")
  return <>
    <InspectorHeader title={t("comparison.title")} subtitle={comparison.model_name}
      trailing={<Badge variant="secondary">{t(`status.${comparison.status}`)}</Badge>} />
    {onRerun || onOpen || recycleAction ? <TooltipProvider><div className="space-y-2 border-y border-divider px-4 py-3">
      <div className="flex items-center gap-2">
        {onRerun ? <Tooltip><TooltipTrigger asChild><span className="inline-flex">
          <Button size="icon-sm" aria-label={rerunLabel} aria-busy={pending} disabled={disabled || pending || comparison.status === "running"} onClick={() => void rerun()}>{pending ? <Spinner /> : <RotateCcwIcon aria-hidden="true" />}</Button>
        </span></TooltipTrigger><TooltipContent>{rerunLabel}</TooltipContent></Tooltip> : null}
        {onOpen ? <Tooltip><TooltipTrigger asChild><Button variant="outline" size="icon-sm" aria-label={t("comparison.view")} onClick={onOpen}><FileChartColumnIcon aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("comparison.view")}</TooltipContent></Tooltip> : null}
        {recycleAction}
      </div>
      {failed ? <p role="alert" className="text-[11px] text-destructive">{tr("shortcuts.failed")}</p> : null}
    </div></TooltipProvider> : null}
    <dl className="space-y-3 px-4 py-2">
      <InspectorRow label={t("inspector.plan")} value={comparison.plan_name} />
      {comparison.channels.map(channel => <InspectorRow key={channel.channel_id} label={channel.channel_name}
        value={channelVerdict(channel, t, snapshot.reports.find(report => report.run_id === channel.run_id))} />)}
    </dl>
    <Separator />
    {exportControls}
  </>
}
