import { useTranslation } from "react-i18next"
import FileChartColumnIcon from "lucide-react/dist/esm/icons/file-chart-column.mjs"
import FlaskConicalIcon from "lucide-react/dist/esm/icons/flask-conical.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"
import ListChecksIcon from "lucide-react/dist/esm/icons/list-checks.mjs"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import SquareIcon from "lucide-react/dist/esm/icons/square.mjs"
import SquarePenIcon from "lucide-react/dist/esm/icons/square-pen.mjs"
import ArrowRightIcon from "lucide-react/dist/esm/icons/arrow-right.mjs"
import EllipsisIcon from "lucide-react/dist/esm/icons/ellipsis.mjs"
import Trash2Icon from "lucide-react/dist/esm/icons/trash-2.mjs"
import type { QuickPerformancePhase } from "./data"
import { ModelIcon } from "./model-icon"
import { Fragment, useRef, useState, type ReactNode } from "react"

import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import type { HistoryRecycleTriggerProps } from "@/features/history-recycle/recycle-history-button"

type HistoryRecycleRenderer = (item: QuickTestHistoryItem, renderTrigger?: (props: HistoryRecycleTriggerProps) => ReactNode) => ReactNode

export interface QuickTestHistoryItem {
  id: string
  mode: "functional" | "performance" | "plan"
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
  observed?: number
  indeterminate?: number
  detail?: string
  error?: string
  reportID?: string
  runID?: string
  jobID?: string
  cancelDisabled?: boolean
  cancelHint?: string
  controlStatus?: string
}

export function QuickTestHistory({ items, selectedID, busy, onSelect, onRestore, onOpenReport, onOpenExecution, onCancel, onRefresh, renderRecycleAction }: {
  items: QuickTestHistoryItem[]
  selectedID: string
  busy: boolean
  onSelect: (item: QuickTestHistoryItem) => void
  onRestore: (id: string) => void
  onOpenReport: (id: string) => void
  onOpenExecution?: (item: QuickTestHistoryItem) => void
  onCancel: (item: QuickTestHistoryItem) => void
  onRefresh: () => void
  renderRecycleAction?: HistoryRecycleRenderer
}) {
  const { t, i18n } = useTranslation("quickTest")
  const currentYear = new Date().getFullYear()
  return (
    <TooltipProvider delayDuration={250}>
      <ol aria-label={t("workspace.history")} className="space-y-2">
        {items.map(item => {
          const statusLabel = item.statusLabel ?? (item.phase ? t(`phase.${item.phase}`) : t(`common:status.${item.status}`))
          const statusKey = item.statusLabel ? "waiting" : item.phase ?? item.status
          const compactStatusLabel = t(`historyStatus.${statusKey}`, { defaultValue: statusLabel })
          const statusColor = item.status === "passed" ? "text-outcome-pass"
            : item.status === "failed" ? "text-outcome-fail"
            : item.status === "running" || item.status === "draining" ? "text-foreground" : "text-muted-foreground"
          const controlHint = item.controlStatus || (item.active && item.cancelDisabled ? item.cancelHint : undefined)
          const status = <span className={`inline-flex min-w-0 max-w-full items-center gap-1.5 font-semibold ${statusColor}`}>
            {item.active ? <Spinner aria-hidden="true" className="size-3 shrink-0 text-primary" /> : <span aria-hidden="true" className={`size-1.5 shrink-0 rounded-full ${item.status === "cancelled" ? "border border-current" : "bg-current"}`} />}
            <span className="min-w-0 truncate">{compactStatusLabel}</span>
          </span>
          const timestamp = new Date(item.timestamp)
          const fullTimestamp = timestamp.toLocaleString(i18n.language)
          const compactDate = timestamp.toLocaleDateString(i18n.language, {
            year: timestamp.getFullYear() === currentYear ? undefined : "numeric",
            month: "2-digit", day: "2-digit",
          })
          const compactTime = timestamp.toLocaleTimeString(i18n.language, {
            hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
          })
          const percentage = item.planned && item.completed !== undefined
            ? Math.min(100, 100 * item.completed / item.planned) : undefined
          const hasOutcomeCounts = item.planned !== undefined && item.planned > 0 &&
            item.succeeded !== undefined && item.failed !== undefined
          const segments = hasOutcomeCounts ? (() => {
            const planned = item.planned!
            const outcomes = [
              { key: "succeeded", count: item.succeeded!, text: `${t(item.mode !== "performance" ? "common:status.passed" : "desktop:quick-test_succeeded")} ${item.succeeded}`, color: "bg-outcome-pass" },
              { key: "failed", count: item.failed!, text: `${t("desktop:quick-test_failed")} ${item.failed}`, color: "bg-outcome-fail" },
              ...(item.observed ? [{ key: "observed", count: item.observed, text: `${t("reports:headline.observedCases")} ${item.observed}`, color: "bg-outcome-review" }] : []),
              ...(item.indeterminate ? [{ key: "indeterminate", count: item.indeterminate, text: `${t("reports:headline.indeterminateCases")} ${item.indeterminate}`, color: "bg-outcome-review" }] : []),
              ...(item.inFlight !== undefined ? [{ key: "inFlight", count: item.inFlight, text: `${t("desktop:quick-test_in_flight")} ${item.inFlight}`, color: "bg-primary" }] : []),
            ]
            let remaining = planned
            return outcomes.map(outcome => {
              const count = Math.min(remaining, Math.max(0, outcome.count))
              remaining -= count
              return { ...outcome, width: 100 * count / planned }
            })
          })() : undefined
          return (
            <li key={item.id} data-selected={item.id === selectedID} className="group relative min-w-0 rounded-md p-3 transition-colors hover:bg-surface-hover data-[selected=true]:bg-surface-active">
              <HistoryHeading item={item} selected={item.id === selectedID} busy={busy} onSelect={() => onSelect(item)} status={
                <Tooltip>
                  <TooltipTrigger asChild><Button type="button" variant="ghost" disabled={busy} aria-label={statusLabel} aria-description={controlHint} onClick={() => onSelect(item)} className="pointer-events-auto h-auto shrink-0 justify-start rounded-sm p-0 text-left text-[11px] leading-4 hover:bg-transparent hover:text-current focus-visible:ring-inset">{status}</Button></TooltipTrigger>
                  <TooltipContent className="flex-col items-start [overflow-wrap:anywhere]"><span>{statusLabel}</span>{controlHint ? <span>{controlHint}</span> : null}</TooltipContent>
                </Tooltip>
              } />
              <div className="pointer-events-none relative">
                <div className="mt-1.5 flex min-w-0 flex-col gap-1 text-[11px] leading-4 text-muted-foreground">
                  <HistoryMetadata text={item.model} leading={<ModelIcon model={item.model} />} disabled={busy} onClick={() => onSelect(item)} className="w-full" />
                  <HistoryMetadata text={item.channel} disabled={busy} onClick={() => onSelect(item)} className="w-full" />
                </div>
                {item.active && (percentage !== undefined || segments || item.detail) ? (
                  <div role="status" aria-label={`${t("task.progress")} ${item.title}`} className="mt-2 space-y-1.5">
                    <div className="flex min-w-0 items-center gap-2">
                      {segments ? (
                        <div
                          role="progressbar"
                          aria-label={`${t("task.progress")} ${item.title}`}
                          aria-valuemin={0}
                          aria-valuemax={100}
                          aria-valuenow={percentage}
                          aria-valuetext={`${item.completed ?? 0} / ${item.planned}; ${segments.map(segment => segment.text).join(" · ")}`}
                          className="flex h-1 min-w-0 flex-1 overflow-hidden rounded-full bg-muted"
                        >
                          {segments.map(segment => (
                            <span
                              key={segment.key}
                              aria-hidden="true"
                              className={`h-full shrink-0 ${segment.color} transition-[width] duration-200`}
                              style={{ width: `${segment.width}%` }}
                            />
                          ))}
                        </div>
                      ) : (
                        <Progress value={percentage} className="h-1 min-w-0 flex-1" aria-label={`${t("performance.requestProgress")} ${item.title}`} aria-valuenow={percentage} />
                      )}
                      {item.completed !== undefined ? <span className="shrink-0 text-[11px] leading-4 tabular-nums text-muted-foreground">{item.planned ? `${item.completed} / ${item.planned}` : t("workspace.completed", { count: item.completed })}</span> : null}
                    </div>
                    {segments?.some(segment => segment.count > 0) ? (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <p tabIndex={0} className="pointer-events-auto flex min-w-0 flex-nowrap items-center gap-x-1.5 rounded-sm text-[11px] tabular-nums outline-none focus-visible:ring-2 focus-visible:ring-ring">
                            {segments.filter(segment => segment.count > 0).map((segment, index) => <Fragment key={segment.key}>
                              {index > 0 ? <span className="sr-only">{" · "}</span> : null}
                              <span className={`min-w-0 truncate ${segment.key === "succeeded" ? "text-outcome-pass" : segment.key === "failed" ? "text-outcome-fail" : segment.key === "inFlight" ? "text-muted-foreground" : "text-outcome-review"}`}>{segment.text}</span>
                            </Fragment>)}
                          </p>
                        </TooltipTrigger>
                        <TooltipContent className="[overflow-wrap:anywhere]">{segments.filter(segment => segment.count > 0).map(segment => segment.text).join(" · ")}</TooltipContent>
                      </Tooltip>
                    ) : !segments && item.detail ? <p className="text-[11px] tabular-nums text-muted-foreground [overflow-wrap:anywhere]">{item.detail}</p> : null}
                  </div>
                ) : null}
                {item.error ? <p className="mt-2 text-[11px] text-destructive [overflow-wrap:anywhere]">{item.error}</p> : null}
                {item.waitingForReport || item.reportError ? <div role="status" className="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground">
                  {item.waitingForReport ? <Spinner className="size-3 shrink-0" /> : null}
                  <span>{t(item.reportError ? "workspace.reportUnavailable" : "workspace.waitingForReport")}</span>
                </div> : null}
                <div className="mt-2 flex h-7 min-w-0 items-center justify-between gap-1">
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button type="button" variant="ghost" disabled={busy} aria-label={fullTimestamp} aria-description={t("task.restore")} onClick={() => onSelect(item)} className="pointer-events-auto relative h-full min-w-0 flex-1 justify-start rounded-sm p-0 text-left text-[11px] font-normal text-muted-foreground hover:bg-transparent hover:text-muted-foreground focus-visible:ring-inset">
                        <time className="flex min-w-0 flex-col items-start text-left leading-[13px] tabular-nums" dateTime={item.timestamp}>
                          <span className="block max-w-full truncate">{compactDate}</span>
                          <span className="block max-w-full truncate">{compactTime}</span>
                        </time>
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>{fullTimestamp}</TooltipContent>
                  </Tooltip>
                  <div className="pointer-events-auto relative flex shrink-0 items-center gap-0.5">
                    {(item.jobID || item.runID) && onOpenExecution ? <HistoryAction label={t("jobs:details")} name={item.title} onClick={() => onOpenExecution(item)}><ArrowRightIcon /></HistoryAction> : null}
                    {item.active && (item.jobID || item.runID) ? <HistoryAction variant="destructive" label={t("task.cancel")} name={item.title} aria-description={item.cancelHint ?? t("jobs:cancelDescription")} disabled={busy || item.cancelDisabled} onClick={() => onCancel(item)}><SquareIcon /></HistoryAction> : null}
                    {item.reportID && !item.active ? <HistoryAction label={t("task.report")} name={item.title} onClick={() => onOpenReport(item.reportID!)}><FileChartColumnIcon /></HistoryAction> : null}
                    {renderRecycleAction?.(item, recycle => <HistoryMoreActions item={item} busy={busy} onRestore={onRestore} onOpenReport={onOpenReport} onRefresh={onRefresh} recycle={recycle} />)
                      ?? <HistoryMoreActions item={item} busy={busy} onRestore={onRestore} onOpenReport={onOpenReport} onRefresh={onRefresh} />}
                  </div>
                </div>
              </div>
            </li>
          )
        })}
      </ol>
    </TooltipProvider>
  )
}

function HistoryHeading({ item, selected, busy, onSelect, status }: {
  item: QuickTestHistoryItem
  selected: boolean
  busy: boolean
  onSelect: () => void
  status: ReactNode
}) {
  const { t } = useTranslation("quickTest")
  const titleRef = useRef<HTMLParagraphElement>(null)
  const [open, setOpen] = useState(false)
  const Icon = item.mode === "performance" ? GaugeIcon : item.mode === "plan" ? ListChecksIcon : FlaskConicalIcon
  return <>
    <Tooltip open={open} onOpenChange={next => setOpen(next && !!titleRef.current && titleRef.current.scrollWidth > titleRef.current.clientWidth)}>
      <TooltipTrigger asChild><Button type="button" variant="ghost" disabled={busy} aria-label={item.title} aria-description={t("task.restore")} aria-current={selected ? "true" : undefined} onClick={onSelect} className="absolute inset-0 size-full cursor-pointer rounded-md p-0 hover:bg-transparent focus-visible:ring-inset" /></TooltipTrigger>
      <TooltipContent className="[overflow-wrap:anywhere]">{item.title}</TooltipContent>
    </Tooltip>
    <div className="pointer-events-none relative flex min-w-0 items-center gap-2">
      <Icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <p ref={titleRef} className="min-w-0 flex-1 truncate text-xs font-medium">{item.title}</p>
      {status}
    </div>
  </>
}

function HistoryMetadata({ text, leading, className, ...props }: { text: string; leading?: ReactNode } & React.ComponentProps<typeof Button>) {
  const { t } = useTranslation("quickTest")
  const textRef = useRef<HTMLSpanElement>(null)
  const [open, setOpen] = useState(false)
  return <Tooltip open={open} onOpenChange={next => setOpen(next && !!textRef.current && textRef.current.scrollWidth > textRef.current.clientWidth)}>
    <TooltipTrigger asChild>
      <Button type="button" variant="ghost" aria-label={text} aria-description={t("task.restore")} {...props} className={`pointer-events-auto relative h-auto min-w-0 cursor-pointer justify-start gap-1 rounded-sm p-0 text-left text-[11px] font-normal leading-4 text-muted-foreground hover:bg-transparent hover:text-muted-foreground focus-visible:ring-inset ${className ?? ""}`}>
        {leading}
        <span ref={textRef} className="block min-w-0 flex-1 truncate">{text}</span>
      </Button>
    </TooltipTrigger>
    <TooltipContent className="[overflow-wrap:anywhere]">{text}</TooltipContent>
  </Tooltip>
}

function HistoryMoreActions({ item, busy, onRestore, onOpenReport, onRefresh, recycle }: {
  item: QuickTestHistoryItem
  busy: boolean
  onRestore: (id: string) => void
  onOpenReport: (id: string) => void
  onRefresh: () => void
  recycle?: HistoryRecycleTriggerProps
}) {
  const { t } = useTranslation("quickTest")
  const recycleRequested = useRef(false)
  const canRefresh = item.active && !!(item.jobID || item.runID)
  if (!canRefresh && !item.runID && !(item.active && item.reportID) && !recycle) return null
  return <DropdownMenu onOpenChange={open => { if (open) recycleRequested.current = false }}>
    <Tooltip>
      <TooltipTrigger asChild><DropdownMenuTrigger asChild>
        <Button ref={recycle?.ref} type="button" size="icon-sm" variant="ghost" disabled={busy || recycle?.disabled} aria-label={`${t("common:actions.more")} ${item.title}`}><EllipsisIcon /></Button>
      </DropdownMenuTrigger></TooltipTrigger>
      <TooltipContent>{t("common:actions.more")}</TooltipContent>
    </Tooltip>
    <DropdownMenuContent align="end" className="min-w-44" onCloseAutoFocus={event => {
      if (!recycleRequested.current) return
      event.preventDefault()
      recycleRequested.current = false
    }}>
      {canRefresh ? <DropdownMenuItem onSelect={onRefresh}><RefreshCwIcon />{t("task.refresh")}</DropdownMenuItem> : null}
      {item.active && item.reportID ? <DropdownMenuItem onSelect={() => onOpenReport(item.reportID!)}><FileChartColumnIcon />{t("task.report")}</DropdownMenuItem> : null}
      {item.runID ? <DropdownMenuItem onSelect={() => onRestore(item.runID!)}><SquarePenIcon />{t("task.restore")}</DropdownMenuItem> : null}
      {recycle ? <DropdownMenuItem disabled={recycle.disabled} aria-label={recycle.label} variant="destructive" onSelect={() => { recycleRequested.current = true; recycle.onClick() }}><Trash2Icon />{t("historyRecycle:move")}</DropdownMenuItem> : null}
    </DropdownMenuContent>
  </DropdownMenu>
}

function HistoryAction({ label, name, children, ...props }: { label: string; name: string; children: ReactNode } & React.ComponentProps<typeof Button>) {
  return <Tooltip>
    <TooltipTrigger asChild><Button type="button" size="icon-sm" variant="ghost" aria-label={`${label} ${name}`} {...props}>{children}</Button></TooltipTrigger>
    <TooltipContent>{label}</TooltipContent>
  </Tooltip>
}
