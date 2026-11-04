import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"
import RotateCwIcon from "lucide-react/dist/esm/icons/rotate-cw.mjs"
import { DesktopClientError, publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { FieldGroup } from "@/components/ui/field"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { CatalogPlan, CatalogSnapshot, SaveQuickTestTargetCommand, SavedQuickTestTarget } from "@/features/catalog/data"
import { PROTOCOL_LABELS } from "@/features/catalog/protocols"
import type { HistoryChanged, HistoryVisibilityChange } from "@/features/history-recycle/data"
import { RecycleHistoryButton, type HistoryRecycleClient } from "@/features/history-recycle/recycle-history-button"
import { isJobTerminal, type ControlView, type JobView } from "@/features/jobs/data"
import { controlRun, jobControlRevision, jobControlStatus, launchAtDefault, type JobClient } from "@/features/jobs/execution"
import { useJobObservation } from "@/features/jobs/use-job-observation"
import { CaseConcurrencyField } from "@/features/runs/case-concurrency-field"
import { DEFAULT_CASE_CONCURRENCY, isCaseConcurrency, isRunActive, type StartRunTargetCommand, type PlanRunConnection } from "@/features/runs/data"
import { useRunHistory, type RunHistoryClient } from "@/features/runs/use-run-history"
import { PerformanceProfileSummary } from "./performance-profile-summary"
import { QuickTestHistory, type QuickTestHistoryItem } from "./quick-test-history"
import { PlanConnectionFields } from "./plan-connection-fields"
import { planConnectionCommand } from "./plan-connection"
import { QuickTestField } from "./quick-test-field"

type Resource = { ready: boolean; failed: boolean; retry: () => Promise<unknown> }
type Actions = JobClient & Partial<RunHistoryClient> & Partial<HistoryRecycleClient> &
  Partial<Pick<DesktopClient, "getReportGeneration" | "subscribeRemoteResults">>
type PlanJob = { job: JobView; selection: StartRunTargetCommand; title: string; model: string; channel: string }
type Observation = { job: JobView; commands: ControlView[]; controlsReady: boolean }
const emptySelection: StartRunTargetCommand = { plan_id: "", model_id: "", channel_id: "" }

export function PlanTestWorkspace({ catalog, catalogResources, loadPlans, modeControl, actions,
  connection: suppliedConnection, onConnectionChange, onSaveChannel,
  onJobLaunched, onJobChange, onOpenExecution, onOpenReport, onHistoryChanged, historyChanges = [],
}: {
  catalog: CatalogSnapshot
  catalogResources?: { connections: Resource; plans: Resource }
  loadPlans?: () => Promise<unknown>
  modeControl: ReactNode
  actions: Actions
  connection?: PlanRunConnection
  onConnectionChange?: (connection: PlanRunConnection) => void
  onSaveChannel?: (command: SaveQuickTestTargetCommand) => Promise<SavedQuickTestTarget>
  onJobLaunched?: (job: JobView) => void
  onJobChange?: (job: JobView) => void
  onOpenExecution: (job: JobView | null, runID?: string) => void
  onOpenReport: (id: string) => void | Promise<void>
  onHistoryChanged?: HistoryChanged
  historyChanges?: HistoryVisibilityChange[]
}) {
  const { t } = useTranslation("quickTest")
  const { t: tj } = useTranslation("jobs")
  const [selection, setSelection] = useState(emptySelection)
  const [jobs, setJobs] = useState<PlanJob[]>([])
  const [observations, setObservations] = useState<Record<string, Observation>>({})
  const [controls, setControls] = useState<Record<string, ControlView>>({})
  const [failedReports, setFailedReports] = useState<string[]>([])
  const [selectedID, setSelectedID] = useState("")
  const [pending, setPending] = useState(false)
  const [controlPending, setControlPending] = useState(false)
  const [error, setError] = useState("")
  const [connectionErrors, setConnectionErrors] = useState<Record<string, string>>({})
  const [localConnection, setLocalConnection] = useState<PlanRunConnection>({ protocol: "openai-chat", model: "", base_url: "", api_key: "" })
  const connection = suppliedConnection ?? localConnection
  const updateConnection = (patch: Partial<PlanRunConnection>) => {
    const next = { ...connection, ...patch }
    if (patch.protocol && patch.protocol !== connection.protocol) setSelection(current => ({ ...current, plan_id: "" }))
    setLocalConnection(next)
    onConnectionChange?.(next)
    setConnectionErrors({})
    setError("")
  }
  const inFlight = useRef(false)
  const end = useRef<HTMLDivElement>(null)
  const history = useRunHistory(actions, jobs.map(entry => entry.job))
  const { refresh, refreshIDs, applyHistoryChange, loadMore, hasMore, pending: historyPending, error: historyError } = history
  const runs = history.snapshot.runs.filter(run => run.source !== "quick_task")
  const plansReady = catalogResources?.plans.ready ?? true
  const connectionsReady = catalogResources?.connections.ready ?? true
  const availablePlans = catalog.plans.filter(item => item.protocol === connection.protocol)
  const plan = availablePlans.find(item => item.id === selection.plan_id)
  const channel = catalog.channels.find(item => item.id === connection.channel_id)
  const hasSinglePass = plan?.entries.some(entry => entry.load_mode === "single") ?? false

  useEffect(() => { void loadPlans?.().catch(() => {}) }, [loadPlans])
  const changeHistory = useCallback<HistoryChanged>((entry, action) => {
    applyHistoryChange(entry, action)
    if (action === "recycle") {
      setJobs(current => current.filter(item => !entry.job_ids.includes(item.job.job_id) &&
        !item.job.run_ids.some(id => entry.run_ids.includes(id))))
    }
  }, [applyHistoryChange])
  const appliedChanges = useRef(0)
  useEffect(() => {
    historyChanges.slice(appliedChanges.current).forEach(({ entry, action }) => changeHistory(entry, action))
    appliedChanges.current = historyChanges.length
  }, [historyChanges, changeHistory])
  useEffect(() => {
    let timer: number | undefined
    const unsubscribe = actions.subscribeRemoteResults?.(() => {
      if (timer !== undefined) return
      timer = window.setTimeout(() => { timer = undefined; void refresh() }, 100)
    })
    const interval = window.setInterval(() => void refresh(), 10_000)
    return () => { unsubscribe?.(); window.clearTimeout(timer); window.clearInterval(interval) }
  }, [actions, refresh])

  // Run completion can precede report import. Read existing summary projections;
  // do not regenerate reports or derive a verdict from the current plan.
  const waitingIDs = runs.filter(run => !isRunActive(run.status) && !run.report_id &&
    !failedReports.includes(run.id)).map(run => run.id).sort().join(",")
  useEffect(() => {
    if (!waitingIDs) return
    let active = true
    let timer: number | undefined
    const read = async () => {
      const ids = waitingIDs.split(",")
      await refreshIDs(ids)
      try {
        const generation = await actions.getReportGeneration?.()
        if (active && generation) {
          const failed = generation.runs.filter(item => ids.includes(item.run_id) && item.phase === "failed").map(item => item.run_id)
          if (failed.length) setFailedReports(current => [...new Set([...current, ...failed])])
        }
      } catch { /* Preserve the last known report state until the next read. */ }
      if (active) timer = window.setTimeout(() => void read(), 2000)
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [actions, waitingIDs, refreshIDs])

  useEffect(() => {
    const target = end.current
    if (!target || !hasMore || historyPending || historyError) return
    const observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) void loadMore()
    }, { root: target.closest('[data-slot="scroll-area-viewport"]'), rootMargin: "0px 0px 80px 0px" })
    observer.observe(target)
    return () => observer.disconnect()
  }, [hasMore, historyPending, historyError, loadMore])

  const receiveJob = useCallback((job: JobView, commands?: ControlView[]) => {
    setObservations(current => {
      const next = { job, commands: commands ?? current[job.job_id]?.commands ?? [],
        controlsReady: commands !== undefined || !!current[job.job_id]?.controlsReady }
      return JSON.stringify(current[job.job_id]) === JSON.stringify(next) ? current : { ...current, [job.job_id]: next }
    })
    setJobs(current => current.some(entry => entry.job.job_id === job.job_id && JSON.stringify(entry.job) !== JSON.stringify(job))
      ? current.map(entry => entry.job.job_id === job.job_id ? { ...entry, job } : entry) : current)
    onJobChange?.(job)
  }, [onJobChange])

  const historyItems: QuickTestHistoryItem[] = [
    ...jobs.filter(entry => !runs.some(run => run.job_id === entry.job.job_id || entry.job.run_ids.includes(run.id))).map(entry => {
      const job = observations[entry.job.job_id]?.job ?? entry.job
      const status = job.execution?.status
      const confirmation = job.submission?.confirmation
      return {
        id: job.job_id, jobID: job.job_id, mode: "plan" as const, title: entry.title, model: entry.model, channel: entry.channel,
        timestamp: job.submission?.prepared_at ?? job.execution!.accepted_at,
        status: status ?? "queued", active: !status || !isJobTerminal(status),
        statusLabel: !status ? confirmation ? tj("awaitingSync", { status: tj(`statuses.${confirmation.status}`), revision: confirmation.revision })
          : tj("awaitingConfirmation") : undefined,
      }
    }),
    ...runs.map(run => ({
      id: run.id, runID: run.id, jobID: run.job_id, mode: "plan" as const,
      title: run.plan_name, model: run.model_name, channel: run.channel_name, timestamp: run.started_at,
      status: run.conclusion === "none" ? run.status : run.conclusion,
      active: isRunActive(run.status), completed: run.completed, planned: run.planned,
      succeeded: run.passed, failed: run.failed, observed: run.observed, indeterminate: run.indeterminate,
      reportID: run.report_id, waitingForReport: !isRunActive(run.status) && !run.report_id && !failedReports.includes(run.id),
      reportError: !run.report_id && failedReports.includes(run.id),
      error: run.error_code ? t(`common:errorCode.${run.error_code}`) : undefined,
    })),
  ].map(item => {
    const observation = item.jobID ? observations[item.jobID] : undefined
    const submitted = item.jobID ? controls[item.jobID] : undefined
    const returned = observation?.commands.find(command => command.command_id === submitted?.command_id)
    const command = returned && (!submitted || returned.delivery_revision >= submitted.delivery_revision) ? returned : submitted ?? observation?.commands[0]
    const status = observation ? jobControlStatus(observation.job) : undefined
    return { ...item,
      cancelDisabled: !!item.jobID && (!observation?.controlsReady || !jobControlRevision(observation.job) || command?.state === "pending" ||
        observation.commands.some(command => command.state === "pending") || !!status && isJobTerminal(status)),
      cancelHint: observation && jobControlRevision(observation.job) ? tj("cancelDescription") : tj("awaitingControlConfirmation"),
      controlStatus: command ? `${tj(command.kind === "cancel" ? "cancel" : "stop")} · ${tj(`controlStates.${command.state}`)}${command.observation ? ` · ${tj(`observations.${command.observation}`)}` : ""}` : undefined,
    }
  }).sort((left, right) => Date.parse(right.timestamp) - Date.parse(left.timestamp) || left.id.localeCompare(right.id))
  const observerIDs = [...new Set([...jobs.map(entry => entry.job.job_id), ...runs.filter(run => isRunActive(run.status)).flatMap(run => run.job_id ? [run.job_id] : [])])]

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (inFlight.current || !plansReady || !connectionsReady || !plan) return
    const checked = planConnectionCommand(connection, catalog)
    setConnectionErrors(checked.errors)
    const invalidField = Object.keys(checked.errors)[0]
    if (invalidField) { document.getElementById(`plan-test-${invalidField}`)?.focus(); return }
    if (connection.protocol !== plan.protocol) { setError(t("plan.protocolMismatch")); return }
    inFlight.current = true
    setPending(true)
    setError("")
    const command: StartRunTargetCommand = { plan_id: plan.id, model_id: "", channel_id: "", connection: checked.command,
      ...(hasSinglePass ? { case_concurrency: selection.case_concurrency ?? DEFAULT_CASE_CONCURRENCY } : {}) }
    try {
      const job = await launchAtDefault(actions, { kind: "plan", plan: command, quick_task: null, performance: null, comparison: null })
      const { api_key: _key, ...rememberedConnection } = checked.command
      setJobs(current => [{ job, selection: { ...command, connection: rememberedConnection }, title: plan.name,
        model: checked.command.model, channel: channel?.name ?? checked.command.base_url ?? "" },
        ...current.filter(entry => entry.job.job_id !== job.job_id)])
      setSelectedID(job.job_id)
      onJobLaunched?.(job)
      void refresh()
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("plan.startFailed")))
    } finally { inFlight.current = false; setPending(false) }
  }
  const restore = (item: QuickTestHistoryItem) => {
    if (pending) return
    setSelectedID(item.id)
    setError("")
    const run = runs.find(run => run.id === item.runID)
    const submitted = jobs.find(entry => entry.job.job_id === item.jobID)
    if (submitted?.selection.connection) {
      const restored = submitted.selection.connection
      const sameConnection = connection.protocol === restored.protocol && connection.base_url === restored.base_url && connection.channel_id === restored.channel_id
      updateConnection({ ...restored, api_key: sameConnection ? connection.api_key : "" })
      setSelection(submitted.selection)
    } else if (run) {
      const savedChannel = catalog.channels.find(item => item.id === run.channel_id)
      const savedPlan = catalog.plans.find(item => item.id === run.plan_id)
      const mapping = catalog.channel_models.find(item => item.channel_id === run.channel_id && item.model_id === run.model_id &&
        item.protocols.includes(savedPlan?.protocol ?? connection.protocol))
      updateConnection({ protocol: savedPlan?.protocol ?? connection.protocol, model: mapping?.upstream_model_name ?? run.model_name,
        channel_id: savedChannel?.id ?? "", base_url: savedChannel?.base_url ?? "", api_key: "", credential_run_id: undefined })
      setSelection({ plan_id: run.plan_id, model_id: "", channel_id: "",
        case_concurrency: run.load_mode === "single" && isCaseConcurrency(run.concurrency) ? run.concurrency : DEFAULT_CASE_CONCURRENCY })
      if (!savedChannel) setError(t("plan.refillConnection"))
    }
  }
  const openExecution = async (item: QuickTestHistoryItem) => {
    try { onOpenExecution(item.jobID ? await actions.getJob(item.jobID) : null, item.runID) }
    catch (reason) { setError(publicDesktopErrorMessage(reason, tj("readFailed"))) }
  }
  const cancel = async (item: QuickTestHistoryItem) => {
    if (inFlight.current || item.cancelDisabled) return
    inFlight.current = true
    setControlPending(true)
    setError("")
    try {
      if (item.jobID) {
        const job = await actions.getJob(item.jobID)
        const revision = jobControlRevision(job)
        if (!revision) throw new DesktopClientError("job_confirmation_pending")
        const status = jobControlStatus(job)
        if (status && isJobTerminal(status)) return
        const command = await actions.controlJob({ job_id: job.job_id, kind: "cancel", expected_revision: revision })
        setControls(current => ({ ...current, [job.job_id]: command }))
      } else if (item.runID) await controlRun(actions, item.runID, "cancel")
      if (item.runID) void refreshIDs([item.runID])
    } catch (reason) { setError(publicDesktopErrorMessage(reason, t("task.failure"))) }
    finally { inFlight.current = false; setControlPending(false) }
  }

  return <main className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={t("title")}>
    {observerIDs.map(id => <PlanJobObserver key={id} client={actions} jobID={id} onChange={receiveJob} />)}
    <div className="grid min-h-0 min-w-0 flex-1 grid-cols-[240px_minmax(0,1fr)] gap-4 overflow-hidden p-4 xl:grid-cols-[256px_minmax(0,1fr)]">
      <aside className="flex min-h-0 min-w-0 flex-col" aria-label={t("workspace.history")}>
        <div className="flex shrink-0 flex-col gap-4 pb-4 pt-2">
          {modeControl}
          <div className="flex flex-wrap items-center justify-between gap-1"><h2 className="text-sm font-semibold">{t("workspace.history")}</h2>
            <span className="text-[11px] text-muted-foreground">{t("workspace.active", { count: historyItems.filter(item => item.active).length })}</span></div>
        </div>
        <ScrollArea contentWidth="viewport" className="min-h-0 flex-1">
          {historyItems.length ? <QuickTestHistory items={historyItems} selectedID={historyItems.find(item => item.id === selectedID || item.jobID === selectedID)?.id ?? selectedID} busy={pending || controlPending}
            onSelect={restore} onRestore={id => { const item = historyItems.find(item => item.runID === id); if (item) restore(item) }}
            onOpenExecution={item => void openExecution(item)} onOpenReport={id => void onOpenReport(id)}
            onCancel={item => void cancel(item)} onRefresh={() => { void refresh(); void refreshIDs(runs.map(run => run.id)); setFailedReports([]) }}
            renderRecycleAction={actions.previewHistoryRecycle && actions.recycleHistory ? (item, renderTrigger) => {
              const target = item.jobID ? { kind: "job" as const, id: item.jobID } : item.runID ? { kind: "run" as const, id: item.runID } : null
              return target ? <RecycleHistoryButton compact name={item.title} target={target} client={actions as HistoryRecycleClient}
                disabled={pending || controlPending} renderTrigger={renderTrigger}
                onRecycled={changeHistory} onHistoryChanged={onHistoryChanged} /> : null
            } : undefined}
          /> : !history.pending && !history.error ? <p className="py-4 text-xs text-muted-foreground">{t("plan.noHistory")}</p> : null}
          <div ref={end} aria-live="polite" className="py-3 text-center text-xs text-muted-foreground">
            {history.pending ? <span role="status" className="inline-flex items-center gap-2"><Spinner />{t("historyPage.loading")}</span>
              : history.error ? <div role="alert"><p>{t("historyPage.error")}</p><Button variant="ghost" size="sm" onClick={() => { void refresh(); void refreshIDs(runs.map(run => run.id)) }}>{t("historyPage.retry")}</Button></div>
              : history.hasMore ? <Button variant="ghost" size="sm" onClick={() => void history.loadMore()}>{t("historyPage.more")}</Button>
              : historyItems.length ? t("historyPage.end") : null}
          </div>
        </ScrollArea>
      </aside>
      <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1">
        <section className="mx-auto flex w-full min-w-0 max-w-[800px] flex-col gap-4 py-2" aria-label={t("plan.title")}>
          <h2 className="flex h-7 items-center text-sm font-semibold">{t("plan.title")}</h2>
          {error ? <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert> : null}
          <form onSubmit={event => void submit(event)} noValidate className="flex min-w-0 flex-col gap-4">
            <FieldGroup>
              <QuickTestField id="plan-test-plan" label={t("plan.label")}
                labelHint={<p id="plan-test-plan-hint" className="text-[11px] font-normal text-muted-foreground">{t("plan.hint")}</p>}>
                <SearchableSelect id="plan-test-plan" value={selection.plan_id} disabled={pending || !plansReady}
                  aria-describedby="plan-test-plan-hint"
                  placeholder={t("plan.choose")} className="w-full"
                  trailing={catalogResources?.plans && (!plansReady || catalogResources.plans.failed)
                    ? <ResourceStatus resource={catalogResources.plans} kind="plans" /> : undefined}
                  options={availablePlans.map(plan => ({ value: plan.id, label: `${plan.name} · ${PROTOCOL_LABELS[plan.protocol]}` }))}
                  onValueChange={plan_id => {
                    const selected = catalog.plans.find(item => item.id === plan_id)
                    if (selected) updateConnection({ protocol: selected.protocol })
                    setSelection({ plan_id, model_id: "", channel_id: "", case_concurrency: DEFAULT_CASE_CONCURRENCY }); setError("")
                  }} />
              </QuickTestField>
              {plansReady && selection.plan_id && !plan ? <Alert variant="destructive"><AlertDescription>{t("plan.missingPlan")}</AlertDescription></Alert> : null}
              {plansReady && !availablePlans.length ? <Empty><EmptyTitle>{t("plan.empty")}</EmptyTitle><EmptyDescription>{t("plan.emptyHint")}</EmptyDescription></Empty> : null}
              <PlanConnectionFields connection={connection} onChange={updateConnection} catalog={catalog}
                disabled={pending} ready={connectionsReady} errors={connectionErrors}
                status={catalogResources?.connections && (!connectionsReady || catalogResources.connections.failed)
                  ? <ResourceStatus resource={catalogResources.connections} kind="connections" /> : undefined}
                onSaveChannel={onSaveChannel} />
              {hasSinglePass ? <CaseConcurrencyField value={selection.case_concurrency ?? DEFAULT_CASE_CONCURRENCY}
                disabled={pending} onChange={case_concurrency => setSelection(current => ({ ...current, case_concurrency }))} /> : null}
            </FieldGroup>
            <div className="flex justify-end"><Button type="submit" disabled={pending || controlPending || !plansReady || !connectionsReady || !plan}>
              {pending ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}{t(pending ? "task.starting" : "plan.start")}
            </Button></div>
          </form>
          {plan ? <PlanPreview plan={plan} /> : null}
        </section>
      </ScrollArea>
    </div>
  </main>
}

function PlanJobObserver({ client, jobID, onChange }: { client: JobClient; jobID: string; onChange: (job: JobView, commands?: ControlView[]) => void }) {
  const { job, controlSummary } = useJobObservation(client, jobID, "", 0)
  useEffect(() => { if (job) onChange(job, controlSummary?.ready ? controlSummary.commands : undefined) }, [job, controlSummary, onChange])
  return null
}

function ResourceStatus({ resource, kind }: { resource?: Resource; kind: "connections" | "plans" }) {
  const { t } = useTranslation("quickTest")
  if (!resource || resource.ready && !resource.failed) return null
  const label = t(kind === "plans" ? resource.failed ? "plan.loadFailed" : "plan.loading" : `catalog.connections${resource.failed ? "Failed" : "Loading"}`)
  return <TooltipProvider><Tooltip><TooltipTrigger asChild>
    {resource.failed ? <Button type="button" variant="ghost" size="icon-sm" aria-label={t(kind === "plans" ? "plan.retry" : "catalog.connectionsRetry")}
      onClick={() => void resource.retry().catch(() => {})}><RotateCwIcon /></Button>
      : <span role="status" tabIndex={0} aria-label={label} className="flex size-7 shrink-0 items-center justify-center"><Spinner /></span>}
  </TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip></TooltipProvider>
}

function PlanPreview({ plan }: { plan: CatalogPlan }) {
  const { t } = useTranslation("catalog")
  return <section className="flex min-w-0 flex-col gap-4" aria-label={t("plans.executionOrder")}>
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <h3 className="font-medium text-foreground">{t("plans.executionOrder")}</h3>
      <span>{t("protocolDesign.entryCount", { entries: plan.entry_count, cases: plan.case_count })}</span>
      <span>{`r${plan.revision}`}</span>
    </div>
    <ol className="flex min-w-0 flex-col gap-5">
      {plan.entries.map((entry, index) => <li key={entry.entry_id} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-3">
        <span aria-hidden="true" className="pt-0.5 font-mono text-xs text-muted-foreground">{String(index + 1).padStart(2, "0")}</span>
        <div className="flex min-w-0 flex-col gap-2 [overflow-wrap:anywhere]">
          <h4 className="text-xs font-medium">{entry.name || entry.target_name}</h4>
          <p className="text-[11px] text-muted-foreground">{t(entry.performance ? "plans.performanceEntry" : "plans.protocolEntry")} · {t(entry.on_failure === "stop" ? "plans.failureStop" : "plans.failureContinue")}</p>
          {entry.performance ? <PerformanceProfileSummary profile={entry.performance} />
            : <p className="text-xs text-muted-foreground">{t(entry.target_kind === "case" ? "plans.caseTarget" : "plans.suiteTarget", { name: entry.target_name })}</p>}
        </div>
      </li>)}
    </ol>
  </section>
}
