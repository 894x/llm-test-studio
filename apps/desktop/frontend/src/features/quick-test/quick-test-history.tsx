import { useTranslation } from "react-i18next"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import FlaskConicalIcon from "lucide-react/dist/esm/icons/flask-conical.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import SquareIcon from "lucide-react/dist/esm/icons/square.mjs"
import SquarePenIcon from "lucide-react/dist/esm/icons/square-pen.mjs"
import type { QuickPerformancePhase } from "./data"
import type { ReactNode } from "react"

import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"

export interface QuickTestHistoryItem {
  id: string
  mode: "functional" | "performance"
  title: string
  model: string
  channel: string
  timestamp: string
  status: string
  phase?: QuickPerformancePhase
  active: boolean
  completed?: number
  planned?: number
  detail?: string
  error?: string
  reportID?: string
  runID?: string
}

export function QuickTestHistory({ items, selectedID, busy, onSelect, onRestore, onOpenReport, onCancel, onRefresh }: {
  items: QuickTestHistoryItem[]
  selectedID: string
  busy: boolean
  onSelect: (item: QuickTestHistoryItem) => void
  onRestore: (id: string) => void
  onOpenReport: (id: string) => void
  onCancel: (id: string) => void
  onRefresh: () => void
}) {
  const { t, i18n } = useTranslation("quickTest")
  return (
    <TooltipProvider delayDuration={250}>
      <ol aria-label={t("workspace.history")} className="space-y-2">
        {items.map(item => {
          const statusLabel = item.phase ? t(`phase.${item.phase}`) : t(`common:status.${item.status}`)
          const Icon = item.mode === "performance" ? GaugeIcon : FlaskConicalIcon
          const percentage = item.planned && item.completed !== undefined
            ? Math.min(100, 100 * item.completed / item.planned) : undefined
          return (
            <li key={item.id} data-selected={item.id === selectedID} className="group min-w-0 rounded-md p-3 transition-colors hover:bg-surface-hover data-[selected=true]:bg-surface-active">
              <div className="flex items-start gap-2">
                <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <Button type="button" variant="ghost" aria-current={item.id === selectedID ? "true" : undefined} onClick={() => onSelect(item)} className="h-auto min-w-0 shrink flex-1 justify-start rounded-none p-0 text-left whitespace-normal hover:bg-transparent">
                  <span className="min-w-0 text-xs font-medium [overflow-wrap:anywhere]">{item.title}</span>
                </Button>
                {item.active ? <Spinner aria-label={statusLabel} className="mt-0.5 size-3.5 shrink-0" /> : null}
              </div>
              <p className="mt-2 text-[11px] text-muted-foreground [overflow-wrap:anywhere]">{item.model} · {item.channel}</p>
              <div className="mt-1.5 flex items-center justify-between gap-1">
              <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1 text-[11px]">
                <span className={item.status === "failed" ? "text-destructive" : "text-muted-foreground"}>{statusLabel}</span>
                <time className="text-muted-foreground" dateTime={item.timestamp}>{new Date(item.timestamp).toLocaleString(i18n.language)}</time>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                {item.active && item.runID ? <>
                  <HistoryAction label={t("task.refresh")} name={item.title} onClick={onRefresh}><RefreshCwIcon /></HistoryAction>
                  <HistoryAction label={t("task.cancel")} name={item.title} disabled={busy} onClick={() => onCancel(item.runID!)}><SquareIcon /></HistoryAction>
                </> : null}
                {item.reportID ? <HistoryAction label={t("task.report")} name={item.title} onClick={() => onOpenReport(item.reportID!)}><FileChartColumnIcon /></HistoryAction> : null}
                {item.runID ? <HistoryAction label={t("task.restore")} name={item.title} disabled={busy} onClick={() => onRestore(item.runID!)}><SquarePenIcon /></HistoryAction> : null}
              </div>
              </div>
              {item.active ? (
                <div role="status" aria-label={`${t("task.progress")} ${item.title}`} className="mt-2 space-y-2">
                  <Progress value={percentage} className="h-1" aria-label={`${t("performance.requestProgress")} ${item.title}`} aria-valuenow={percentage} />
                  {item.completed !== undefined ? <p className="text-[11px] tabular-nums text-muted-foreground">{item.planned ? `${item.completed} / ${item.planned}` : t("workspace.completed", { count: item.completed })}</p> : null}
                  {item.detail ? <p className="text-[11px] tabular-nums text-muted-foreground [overflow-wrap:anywhere]">{item.detail}</p> : null}
                </div>
              ) : null}
              {item.error ? <p className="mt-2 text-[11px] text-destructive [overflow-wrap:anywhere]">{item.error}</p> : null}

            </li>
          )
        })}
      </ol>
    </TooltipProvider>
  )
}

function HistoryAction({ label, name, children, ...props }: { label: string; name: string; children: ReactNode } & React.ComponentProps<typeof Button>) {
  return <Tooltip>
    <TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={`${label} ${name}`} {...props}>{children}</Button></TooltipTrigger>
    <TooltipContent>{label}</TooltipContent>
  </Tooltip>
}
