import { useTranslation } from "react-i18next"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import FlaskConicalIcon from "lucide-react/dist/esm/icons/flask-conical.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import SquareIcon from "lucide-react/dist/esm/icons/square.mjs"
import SquarePenIcon from "lucide-react/dist/esm/icons/square-pen.mjs"
import ArrowRightIcon from "lucide-react/dist/esm/icons/arrow-right.mjs"
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
  statusLabel?: string
  phase?: QuickPerformancePhase
  active: boolean
  waitingForReport?: boolean
  reportError?: boolean
  completed?: number
  planned?: number
  succeeded?: number
  failed?: number
  inFlight?: number
  detail?: string
  error?: string
  reportID?: string
  runID?: string
  jobID?: string
}

export function QuickTestHistory({ items, selectedID, busy, onSelect, onRestore, onOpenReport, onOpenExecution, onCancel, onRefresh }: {
  items: QuickTestHistoryItem[]
  selectedID: string
  busy: boolean
  onSelect: (item: QuickTestHistoryItem) => void
  onRestore: (id: string) => void
  onOpenReport: (id: string) => void
  onOpenExecution?: (item: QuickTestHistoryItem) => void
  onCancel: (id: string) => void
  onRefresh: () => void
}) {
  const { t, i18n } = useTranslation("quickTest")
  return (
    <TooltipProvider delayDuration={250}>
      <ol aria-label={t("workspace.history")} className="space-y-2">
        {items.map(item => {
          const statusLabel = item.statusLabel ?? (item.phase ? t(`phase.${item.phase}`) : t(`common:status.${item.status}`))
          const Icon = item.mode === "performance" ? GaugeIcon : FlaskConicalIcon
          const percentage = item.planned && item.completed !== undefined
            ? Math.min(100, 100 * item.completed / item.planned) : undefined
          const hasPerformanceCounts = item.mode === "performance" && item.planned !== undefined && item.planned > 0 &&
            item.succeeded !== undefined && item.failed !== undefined && item.inFlight !== undefined
          const segments = hasPerformanceCounts ? (() => {
            const planned = item.planned!
            const succeeded = Math.min(planned, item.succeeded!)
            const failed = Math.min(planned - succeeded, item.failed!)
            const inFlight = Math.min(planned - succeeded - failed, item.inFlight!)
            return { planned, succeeded, failed, inFlight }
          })() : undefined
          return (
            <li key={item.id} data-selected={item.id === selectedID} className="group relative min-w-0 rounded-md p-3 transition-colors hover:bg-surface-hover data-[selected=true]:bg-surface-active">
              <Button type="button" variant="ghost" disabled={busy} aria-label={item.title} aria-description={t("task.restore")} aria-current={item.id === selectedID ? "true" : undefined} onClick={() => onSelect(item)} className="absolute inset-0 size-full cursor-pointer rounded-md p-0 hover:bg-transparent focus-visible:ring-inset" />
              <div className="pointer-events-none relative">
                <div className="flex items-start gap-2">
                  <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  <p className="min-w-0 flex-1 text-xs font-medium [overflow-wrap:anywhere]">{item.title}</p>
                  {item.active ? <Spinner aria-label={statusLabel} className="mt-0.5 size-3.5 shrink-0" /> : null}
                </div>
                <p className="mt-2 text-[11px] text-muted-foreground [overflow-wrap:anywhere]">{item.model} · {item.channel}</p>
                <div className="mt-1.5 flex items-center justify-between gap-1">
                  <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1 text-[11px]">
                    <span className={item.status === "failed" ? "text-destructive" : "text-muted-foreground"}>{statusLabel}</span>
                    <time className="text-muted-foreground" dateTime={item.timestamp}>{new Date(item.timestamp).toLocaleString(i18n.language)}</time>
                  </div>
                  <div className="pointer-events-auto relative flex shrink-0 items-center gap-1">
                    {(item.jobID || item.runID) && onOpenExecution ? <HistoryAction label={t("jobs:details")} name={item.title} onClick={() => onOpenExecution(item)}><ArrowRightIcon /></HistoryAction> : null}
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
                    <div className="flex min-w-0 items-center gap-2">
                      {segments ? (
                        <div
                          role="progressbar"
                          aria-label={`${t("performance.progressAria")} ${item.title}`}
                          aria-valuemin={0}
                          aria-valuemax={100}
                          aria-valuenow={percentage}
                          aria-valuetext={`${item.completed ?? 0} / ${segments.planned}; ${t("workspace.performanceCounts", { succeeded: item.succeeded, failed: item.failed, in_flight: item.inFlight })}`}
                          className="flex h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-muted"
                        >
                          {([
                            [t("desktop:quick-test_succeeded"), segments.succeeded, "bg-outcome-pass"],
                            [t("desktop:quick-test_failed"), segments.failed, "bg-outcome-fail"],
                            [t("desktop:quick-test_in_flight"), segments.inFlight, "bg-primary"],
                          ] as const).map(([label, count, color]) => (
                            <span
                              key={label}
                              aria-hidden="true"
                              className={`h-full shrink-0 ${color} transition-[width] duration-200`}
                              style={{ width: `${100 * count / segments.planned}%` }}
                            />
                          ))}
                        </div>
                      ) : (
                        <Progress value={percentage} className="h-1 min-w-0 flex-1" aria-label={`${t("performance.requestProgress")} ${item.title}`} aria-valuenow={percentage} />
                      )}
                      {item.completed !== undefined ? <span className="max-w-[50%] shrink-0 text-right text-[11px] leading-4 tabular-nums text-muted-foreground [overflow-wrap:anywhere]">{item.planned ? `${item.completed} / ${item.planned}` : t("workspace.completed", { count: item.completed })}</span> : null}
                    </div>
                    {segments ? (
                      <p className="flex flex-wrap items-center gap-x-2 text-[11px] tabular-nums text-muted-foreground">
                        <span className="inline-flex items-center gap-1"><span aria-hidden="true" className="size-1.5 rounded-full bg-outcome-pass" />{t("desktop:quick-test_succeeded")} {item.succeeded}</span>
                        <span className="sr-only">{" · "}</span>
                        <span className="inline-flex items-center gap-1"><span aria-hidden="true" className="size-1.5 rounded-full bg-outcome-fail" />{t("desktop:quick-test_failed")} {item.failed}</span>
                        <span className="sr-only">{" · "}</span>
                        <span className="inline-flex items-center gap-1"><span aria-hidden="true" className="size-1.5 rounded-full bg-primary" />{t("desktop:quick-test_in_flight")} {item.inFlight}</span>
                      </p>
                    ) : item.detail ? <p className="text-[11px] tabular-nums text-muted-foreground [overflow-wrap:anywhere]">{item.detail}</p> : null}
                  </div>
                ) : null}
                {item.error ? <p className="mt-2 text-[11px] text-destructive [overflow-wrap:anywhere]">{item.error}</p> : null}
                {item.waitingForReport || item.reportError ? <div role="status" className="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
                  {item.waitingForReport ? <Spinner className="size-3 shrink-0" /> : null}
                  <span>{t(item.reportError ? "workspace.reportUnavailable" : "workspace.waitingForReport")}</span>
                </div> : null}
              </div>
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
