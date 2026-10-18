import { useEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import type { QuickPerformanceProgress, QuickPerformanceReport } from "@/features/quick-test/data"
import { isJobTerminal, type ControlInput, type JobView, type PerformanceResultView } from "./data"
import { jobControlRevision, type JobClient } from "./execution"
import { useJobObservation } from "./use-job-observation"

export function JobDetails({ client, jobID, initialJob, reportIDs, onOpenReport, onJobChange, onArchived, renderProgress, renderPerformance, showPerformanceResult = true, renderLayout }: {
  client: JobClient; jobID: string; initialJob?: JobView
  reportIDs?: string[]
  onOpenReport?: (id: string) => void | Promise<void>
  onJobChange?: (job: JobView) => void
  onArchived?: (id: string) => void | Promise<void>
  renderProgress?: (progress: QuickPerformanceProgress) => ReactNode
  renderPerformance?: (report: QuickPerformanceReport) => ReactNode
  showPerformanceResult?: boolean
  renderLayout?: (state: { job: JobView; result: PerformanceResultView | null; error: unknown; details: ReactNode }) => ReactNode
}) {
  const { t } = useTranslation("jobs")
  const [refresh, setRefresh] = useState(0)
  const [controlCursor, setControlCursor] = useState("")
  const [controlBack, setControlBack] = useState<string[]>([])
  const [pending, setPending] = useState(false)
  const [commandError, setCommandError] = useState<unknown>(null)
  const { job: observed, result, controls, error } = useJobObservation(client, jobID, controlCursor, refresh)
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
  const terminal = !!job.execution && isJobTerminal(job.execution.status)
  const awaitingControl = controls?.commands.some(command => command.state === "pending") ?? false
  const control = (kind: ControlInput["kind"]) => {
    if (revision) void execute(() => client.controlJob({ job_id: job.job_id, kind, expected_revision: revision }))
  }
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
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" disabled={pending || terminal || !revision || awaitingControl} onClick={() => control("stop_sending")}>{t("stop")}</Button>
      <Button size="sm" variant="outline" disabled={pending || terminal || !revision || awaitingControl} onClick={() => control("cancel")}>{t("cancel")}</Button>
      {onOpenReport ? (reportIDs ?? []).map(id => <Button key={id} size="sm" variant="outline" onClick={() => void onOpenReport(id)}>{t("openReport")}</Button>) : null}
    </div>
    {commandError ? <Alert variant="destructive"><AlertDescription>{publicDesktopErrorMessage(commandError, t("controlFailed"))}</AlertDescription></Alert> : null}
    {controls && controls.commands.length > 0 ? <div className="flex flex-col gap-2">
      <h3 className="font-medium">{t("controls")}</h3>
      <p className="text-muted-foreground">{t("controlsDescription")}</p>
      {controls.commands.map(command => <div key={command.command_id} className="flex flex-col gap-1 py-1">
        <p>{t(command.kind === "cancel" ? "cancel" : "stop")} · {t(`controlStates.${command.state}`)} · r{command.expected_revision}</p>
        <p className="text-muted-foreground">{command.command_id}</p>
        {command.observation ? <p className="text-muted-foreground">{t(`observations.${command.observation}`)}</p> : null}
        {command.state === "pending" ? <Button variant="outline" size="sm" disabled={pending} onClick={() => void execute(() => client.retryJobControl(command.command_id))}>{t("retryControl")}</Button> : null}
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
  return renderLayout ? renderLayout({ job, result, error, details }) : details
}

function Property({ label, value }: { label: string; value: string }) {
  return <div><dt className="text-[11px] text-muted-foreground">{label}</dt><dd className="mt-1">{value}</dd></div>
}
