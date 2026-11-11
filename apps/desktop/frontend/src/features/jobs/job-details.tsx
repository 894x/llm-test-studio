import SquareIcon from "lucide-react/dist/esm/icons/square.mjs"
import CircleXIcon from "lucide-react/dist/esm/icons/circle-x.mjs"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { useEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import type { QuickPerformanceProgress, QuickPerformanceReport } from "@/features/quick-test/data"
import { isJobTerminal, type ControlInput, type ControlView, type JobView, type PerformanceResultView } from "./data"
import { jobControlRevision, jobControlStatus, type JobClient } from "./execution"
import { useJobObservation } from "./use-job-observation"

export function JobDetails({ client, jobID, initialJob, reportIDs, onOpenReport, onJobChange, onArchived, renderProgress, renderPerformance, showPerformanceResult = true, renderLayout, controlExtraAction }: {
  client: JobClient; jobID: string; initialJob?: JobView
  controlExtraAction?: ReactNode
  reportIDs?: string[]
  onOpenReport?: (id: string) => void | Promise<void>
  onJobChange?: (job: JobView) => void
  onArchived?: (id: string) => void | Promise<void>
  renderProgress?: (progress: QuickPerformanceProgress) => ReactNode
  renderPerformance?: (report: QuickPerformanceReport) => ReactNode
  showPerformanceResult?: boolean
  renderLayout?: (state: { job: JobView; result: PerformanceResultView | null; error: unknown; details: ReactNode; controls: ReactNode; controlButtons: ReactNode; controlFeedback: ReactNode }) => ReactNode
}) {
  const { t } = useTranslation("jobs")
  const [refresh, setRefresh] = useState(0)
  const [controlCursor, setControlCursor] = useState("")
  const [controlBack, setControlBack] = useState<string[]>([])
  const [pending, setPending] = useState(false)
  const [commandError, setCommandError] = useState<unknown>(null)
  const [submittedControl, setSubmittedControl] = useState<ControlView | null>(null)
  const { job: observed, result, controls, controlSummary, error } = useJobObservation(client, jobID, controlCursor, refresh)
  const job = observed ?? initialJob
  const archived = result?.observation?.report.archived ? result.observation.report.report_id : undefined
  const notifiedArchive = useRef("")
  useEffect(() => { if (observed) onJobChange?.(observed) }, [observed, onJobChange])
  useEffect(() => {
    if (archived && notifiedArchive.current !== archived) { notifiedArchive.current = archived; void onArchived?.(archived) }
  }, [archived, onArchived])
  const execute = async (operation: () => Promise<unknown>) => {
    if (pending) return
    setPending(true); setCommandError(null)
    try { await operation(); setRefresh(value => value + 1) }
    catch (reason) { setCommandError(reason) }
    finally { setPending(false) }
  }
  if (!job) return <div role="status" className="py-4 text-xs text-muted-foreground">{error ? publicDesktopErrorMessage(error, t("readFailed")) : <Spinner />}</div>
  const revision = jobControlRevision(job)
  const status = jobControlStatus(job)
  const terminal = status ? isJobTerminal(status) : false
  const returnedControl = controlSummary?.commands.find(command => command.command_id === submittedControl?.command_id)
  const currentControl = returnedControl && (!submittedControl || returnedControl.delivery_revision >= submittedControl.delivery_revision)
    ? returnedControl : submittedControl?.job_id === jobID ? submittedControl : controlSummary?.commands[0]
  const awaitingControl = currentControl?.state === "pending" || (controlSummary?.commands.some(command => command.state === "pending") ?? false)
  const control = (kind: ControlInput["kind"]) => {
    if (revision) void execute(async () => { setSubmittedControl(await client.controlJob({ job_id: job.job_id, kind, expected_revision: revision })) })
  }
  const cancelDisabled = pending || !revision || !controlSummary?.ready || awaitingControl
  const stopDisabled = cancelDisabled || status === "draining"
  const controlButtons = terminal ? null : <TooltipProvider><div className="flex shrink-0 items-center gap-2">
    <Tooltip>
      <TooltipTrigger asChild><span className="inline-flex rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring" tabIndex={stopDisabled ? 0 : undefined}>
        <Button size="icon-sm" aria-label={t("stop")} variant="outline" aria-description={t("stopDescription")} disabled={stopDisabled} onClick={() => control("stop_sending")}><SquareIcon aria-hidden="true" /></Button>
      </span></TooltipTrigger>
      <TooltipContent side="top" className="block max-w-64"><p className="font-medium">{t("stop")}</p><p className="mt-1">{t("stopDescription")}</p>{!revision ? <p className="mt-1">{t("awaitingControlConfirmation")}</p> : null}</TooltipContent>
    </Tooltip>
    <Tooltip>
      <TooltipTrigger asChild><span className="inline-flex rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring" tabIndex={cancelDisabled ? 0 : undefined}>
        <Button size="icon-sm" aria-label={t("cancel")} variant="outline" aria-description={t("cancelDescription")} disabled={cancelDisabled} onClick={() => control("cancel")}><CircleXIcon aria-hidden="true" /></Button>
      </span></TooltipTrigger>
      <TooltipContent side="top" className="block max-w-64"><p className="font-medium">{t("cancel")}</p><p className="mt-1">{t("cancelDescription")}</p>{!revision ? <p className="mt-1">{t("awaitingControlConfirmation")}</p> : null}</TooltipContent>
    </Tooltip>
    {controlExtraAction}
  </div></TooltipProvider>
  const controlFeedback = !terminal && (pending || currentControl || commandError) ? <div className="space-y-2 text-xs [overflow-wrap:anywhere]">
    {pending ? <p role="status" className="flex items-center gap-2 text-[11px] text-muted-foreground"><Spinner className="size-3" />{t("sendingControl")}</p> : null}
    {currentControl ? <p role="status" className="text-[11px] text-muted-foreground">{t(currentControl.kind === "cancel" ? "cancel" : "stop")} · {t(`controlStates.${currentControl.state}`)}{currentControl.observation ? ` · ${t(`observations.${currentControl.observation}`)}` : ""}</p> : null}
    {commandError ? <Alert variant="destructive"><AlertDescription>{publicDesktopErrorMessage(commandError, t("controlFailed"))}</AlertDescription></Alert> : null}
  </div> : null
  const controlActions = controlButtons ? <section aria-label={t("executionControls")} className="space-y-2">{controlButtons}{controlFeedback}</section> : null
  const p = job.progress?.progress
  const submission = job.submission
  const confirmation = submission?.confirmation
  const details = <section className="min-w-0 flex flex-col gap-4 py-3 text-xs [overflow-wrap:anywhere]" aria-label={t("details")}>
    <dl className="flex flex-col gap-3">
      <Property label={t("jobID")} value={job.job_id} />
      <Property label={t("source")} value={t(submission ? "thisDesktop" : "sharedNode")} />
      <Property label={t("location")} value={`${t(job.location.kind)} · ${job.executor.node_id}`} />
      <Property label={t("executor")} value={`${job.executor.version} · ${job.executor.os}/${job.executor.arch} · ${job.executor.installation_id} · ${job.executor.build_id}`} />
      {submission ? <Property label={t("delivery")} value={t(`deliveryStates.${submission.delivery}`)} /> : null}
      {submission?.observation ? <Property label={t("connection")} value={t(`observations.${submission.observation}`)} /> : null}
      <Property label={t("execution")} value={job.execution ? t(`statuses.${job.execution.status}`) : confirmation ? t("awaitingSync", { status: t(`statuses.${confirmation.status}`), revision: confirmation.revision }) : t("awaitingConfirmation")} />
      {job.execution?.failure ? <Property label={t("failure")} value={`${job.execution.failure.phase} · ${job.execution.failure.error_code}`} /> : null}
      {job.kind === "performance" ? <Property label={t("result")} value={t(job.performance_result_ready ? "resultReady" : "resultPending")} /> : null}
    </dl>
    {p && renderProgress ? renderProgress(p) : p ? <div role="status" className="flex flex-col gap-2">
      <p>{t(`phases.${p.phase}`)} · {t("progress", { completed: p.completed, planned: p.planned, active: p.in_flight })}</p>
      {p.planned > 0 ? <Progress value={Math.min(100, p.completed / p.planned * 100)} aria-label={t("execution")} /> : <Spinner />}
    </div> : null}
    {error ? <Alert role="status"><AlertDescription>{t("stale")} {publicDesktopErrorMessage(error, t("readFailed"))}</AlertDescription></Alert> : null}
    {!renderLayout ? controlActions : null}
    <div className="flex flex-wrap gap-2">
      {onOpenReport ? (reportIDs ?? []).map(id => <Button key={id} size="sm" variant="outline" onClick={() => void onOpenReport(id)}>{t("openReport")}</Button>) : null}
    </div>
    {controls && controls.commands.length > 0 ? <div className="flex flex-col gap-2">
      <h3 className="font-medium">{t("controls")}</h3>
      <p className="text-muted-foreground">{t("controlsDescription")}</p>
      {controls.commands.map(command => <div key={command.command_id} className="flex flex-col gap-1 py-1">
        <p>{t(command.kind === "cancel" ? "cancel" : "stop")} · {t(`controlStates.${command.state}`)} · r{command.expected_revision}</p>
        <p className="text-muted-foreground">{command.command_id}</p>
        {command.observation ? <p className="text-muted-foreground">{t(`observations.${command.observation}`)}</p> : null}
        {command.state === "pending" ? <Button variant="outline" size="sm" disabled={pending} onClick={() => void execute(async () => { setSubmittedControl(await client.retryJobControl(command.command_id)) })}>{t("retryControl")}</Button> : null}
      </div>)}
      <div className="flex gap-2">
        <Button size="sm" variant="ghost" disabled={!controlBack.length} onClick={() => { setControlCursor(controlBack.at(-1)!); setControlBack(value => value.slice(0, -1)) }}>{t("previous")}</Button>
        <Button size="sm" variant="ghost" disabled={!controls.has_more} onClick={() => { setControlBack(value => [...value, controlCursor]); setControlCursor(controls.next_cursor) }}>{t("next")}</Button>
      </div>
    </div> : null}
    {showPerformanceResult && result ? <section className="flex flex-col gap-3" aria-label={t("performanceResult")}>
      <h3 className="font-medium">{t(`outcomes.${result.observation_state}`)}</h3>
      {result.observation ? <>
        <p className="text-muted-foreground">{t(`stages.${result.observation.stage}`)} · {t(`boundaries.${result.observation.boundary}`)}</p>
        {(result.observation.stage === "measured" && result.observation.boundary !== "entered") ||
          (result.observation.stage === "capacity" && !!result.observation.report.capacity_result?.rungs.length) ? renderPerformance?.(result.observation.report) : <div className="flex flex-col gap-2 text-muted-foreground">
            <p>{t("noMeasuredTraffic")}</p>
            {result.observation.report.warmup ? <p>{t("stages.warmup")} · {t("completedTraffic", { completed: result.observation.report.warmup.completed, planned: result.observation.report.warmup.request_cap })}</p> : null}
            {result.observation.report.ramp ? <p>{t("stages.ramp")} · {t("completedTraffic", { completed: result.observation.report.ramp.traffic.completed, planned: result.observation.report.ramp.traffic.request_cap })}</p> : null}
            {result.observation.report.error_code ? <p>{t(`common:errorCode.${result.observation.report.error_code}`)}</p> : null}
          </div>}
      </> : <p className="text-muted-foreground">{t(result.execution_started ? "noCheckpoint" : "noObservation")}</p>}
    </section> : null}
  </section>
  return renderLayout ? renderLayout({ job, result, error, details, controls: controlActions, controlButtons, controlFeedback }) : details
}

function Property({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-[11px] text-muted-foreground">{label}</dt><dd className="mt-1">{value}</dd></div>
}
