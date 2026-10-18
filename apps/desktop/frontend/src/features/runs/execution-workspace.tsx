import type { ReportGenerationProgress } from "@/features/reports/generation-progress"
import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import PanelRightIcon from "lucide-react/dist/esm/icons/panel-right.mjs"
import FileTextIcon from "lucide-react/dist/esm/icons/file-text.mjs"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { JobDetails } from "@/features/jobs/job-details"
import type { JobStatus, JobView, SubmissionView } from "@/features/jobs/data"
import type { JobClient } from "@/features/jobs/execution"
import type { QuickPerformanceReport } from "@/features/quick-test/data"
import type { ReportSnapshot } from "@/features/reports/data"
import { cn } from "@/lib/utils"
import { formatTargetDuration, presentWorkspace, type RunRecord, type RunStatus, type WorkspaceSnapshot } from "./data"
import { RunInspectorContent } from "./run-workspace"
import { RunInspectorFrame, InspectorDefinitionRow } from "./run-inspector-frame"
import type { RunShortcutRenderer } from "./use-run-shortcuts"
import { useRunHistory, type RunHistoryClient } from "./use-run-history"
import { useExecutionJobs } from "./use-execution-jobs"

type ExecutionRow = { id: string; job?: JobView; runs: RunRecord[]; title: string; time: string }
const STATUS_CLASS: Record<JobStatus | SubmissionView["delivery"] | RunStatus, string> = {
  prepared: "text-muted-foreground",
  pending: "text-warning-strong",
  accepted: "text-info-strong",
  queued: "text-warning-strong",
  starting: "text-info-strong",
  running: "text-info-strong",
  draining: "text-warning-strong",
  completed: "text-success-strong",
  passed: "text-success-strong",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
}
const desktopQuery = "(min-width: 1180px)"
const subscribeDesktop = (notify: () => void) => {
  const media = window.matchMedia(desktopQuery)
  media.addEventListener("change", notify)
  return () => media.removeEventListener("change", notify)
}
const isDesktop = () => window.matchMedia(desktopQuery).matches

export function RunWorkspace({ client, snapshot: suppliedSnapshot, reports, preferredJob, preferredRunID, commandError,
  renderRunShortcuts, onJobChange, onOpenReport, onOpenComparison, readStatus, historyRevision }: {
  client: JobClient & Partial<RunHistoryClient>; snapshot: WorkspaceSnapshot; reports: ReportSnapshot
  preferredJob: JobView | null; preferredRunID?: string; commandError: string
  renderRunShortcuts?: RunShortcutRenderer
  onJobChange?: (job: JobView) => void
  onOpenReport: (id: string) => void | Promise<void>
  onOpenComparison?: (id: string) => void | Promise<void>
  historyRevision?: Record<string, ReportGenerationProgress>
  readStatus?: ReactNode
}) {
  const { t, i18n } = useTranslation("runs")
  const { t: tj } = useTranslation("jobs")
  const history = useExecutionJobs(client, preferredJob)
  const runHistory = useRunHistory(client, history.jobs, preferredRunID)
  const readyHistory = JSON.stringify(Object.values(historyRevision ?? {}).filter(item => item.phase === "ready").map(item => item.run_id).sort())
  const observedReady = useRef(new Set<string>())
  const refreshRunIDs = runHistory.refreshIDs
  useEffect(() => {
    const ready = JSON.parse(readyHistory) as string[]
    const changed = ready.filter(id => !observedReady.current.has(id))
    if (!changed.length) return
    changed.forEach(id => observedReady.current.add(id))
    void refreshRunIDs(changed)
  }, [readyHistory, refreshRunIDs])
  const snapshot = client.getRunHistory ? runHistory.snapshot : suppliedSnapshot
  const desktop = useSyncExternalStore(subscribeDesktop, isDesktop)
  const [selectedID, setSelectedID] = useState("")
  const [selectedChildID, setSelectedChildID] = useState(preferredRunID ?? "")
  const [detailTab, setDetailTab] = useState("summary")
  const [query, setQuery] = useState("")
  const [kind, setKind] = useState("all")
  const [location, setLocation] = useState("all")
  const [status, setStatus] = useState("all")
  const [plan, setPlan] = useState("all")
  const [standaloneLimit, setStandaloneLimit] = useState(25)
  const [shownPreferred, setShownPreferred] = useState("")
  const preferred = preferredJob?.job_id ?? (preferredRunID ? snapshot.runs.find(run => run.id === preferredRunID)?.job_id ?? preferredRunID : "")
  if (preferred && preferred !== shownPreferred) {
    setShownPreferred(preferred); setSelectedID(preferred)
    setQuery(""); setKind("all"); setLocation("all"); setStatus("all"); setPlan("all")
  }
  const presentation = useMemo(() => presentWorkspace(snapshot, {
    locale: i18n.resolvedLanguage ?? i18n.language, t: (key, values) => t(key, values),
  }), [snapshot, i18n.resolvedLanguage, i18n.language, t])
  const rows = useMemo(() => {
    const runs = new Map(presentation.runs.map(run => [run.id, run]))
    const sources = new Map(snapshot.runs.map(run => [run.id, run]))
    const owned = new Set(history.jobs.flatMap(job => job.run_ids))
    const executions: ExecutionRow[] = history.jobs.map(job => {
      const children = job.run_ids.flatMap(id => runs.get(id) ? [runs.get(id)!] : [])
      return { id: job.job_id, job, runs: children,
        title: job.kind === "run" && children[0] ? children[0].title : tj(`kinds.${job.kind}`),
        time: job.execution?.accepted_at ?? job.submission?.prepared_at ?? "" }
    })
    const standalone = presentation.runs.filter(run => !sources.get(run.id)?.job_id && !owned.has(run.id))
    executions.push(...standalone.slice(0, standaloneLimit).map(run => ({ id: run.id, runs: [run], title: run.title,
      time: sources.get(run.id)!.started_at })))
    executions.sort((a, b) => b.time.localeCompare(a.time) || b.id.localeCompare(a.id))
    return { executions, moreStandalone: standalone.length > standaloneLimit || runHistory.hasMore }
  }, [history.jobs, presentation.runs, snapshot.runs, standaloneLimit, runHistory.hasMore, tj])
  const needle = query.trim().toLocaleLowerCase()
  const visible = rows.executions.filter(row =>
    (kind === "all" || (row.job?.kind ?? "run") === kind) &&
    (location === "all" || row.job?.location.kind === location) &&
    (status === "all" || (row.job ? row.job.execution?.status ?? `delivery_${row.job.submission?.delivery}` : row.runs[0]?.coreStatus) === status) &&
    (plan === "all" || row.runs.some(run => run.planId === plan)) &&
    (!needle || [row.title, row.id, row.job?.executor.node_id, ...row.runs.flatMap(run => [run.id, run.model, run.channel])]
      .some(value => value?.toLocaleLowerCase().includes(needle))))
  const selected = visible.find(row => row.id === selectedID) ?? visible[0]
  const selectedJob = selected?.job
  useEffect(() => { if (selectedJob) onJobChange?.(selectedJob) }, [selectedJob, onJobChange])
  const filtered = !!needle || [kind, location, status, plan].some(value => value !== "all")
  const details = selected ? <ExecutionInspector key={selected.id} row={selected} client={client} reports={reports}
    selectedChildID={selectedChildID} onSelectChild={setSelectedChildID} detailTab={detailTab} onDetailTabChange={setDetailTab}
    renderRunShortcuts={renderRunShortcuts} onJobChange={onJobChange} onOpenReport={onOpenReport} onOpenComparison={onOpenComparison} />
    : <p className="p-4 text-xs text-muted-foreground">{t("inspector.noneSelected")}</p>

  const loadMore = () => {
    if (history.pending || runHistory.pending) return
    history.loadMore()
    void runHistory.loadMore()
    setStandaloneLimit(limit => limit + 25)
  }
  return <main className="flex min-h-0 min-w-0 flex-1 gap-4 pl-4">
    <section aria-labelledby="workspace-heading" className="flex min-h-0 min-w-0 flex-1 flex-col max-[1179px]:pr-4">
      <div className="flex shrink-0 items-center justify-between gap-3 py-3">
        <div className="min-w-0"><h1 id="workspace-heading" className="text-lg font-semibold tracking-tight">{t("title")}</h1>
          <p className="mt-1 text-[11px] text-muted-foreground">{t("description")}</p></div>
        <div className="flex shrink-0 items-center gap-2 whitespace-nowrap text-[11px] text-muted-foreground">
          <span>{t("executions.shown", { count: visible.length })}</span>
          {filtered ? <Button size="sm" variant="ghost" onClick={() => { setQuery(""); setKind("all"); setLocation("all"); setStatus("all"); setPlan("all") }}>{t("executions.clear")}</Button> : null}
          <Button size="sm" variant="ghost" disabled={history.pending} onClick={() => { history.retry(); void runHistory.refresh() }}>{history.pending ? <Spinner /> : null}{t("executions.refresh")}</Button>
          {!desktop && selected ? <Sheet><SheetTrigger asChild><Button variant="outline" size="sm"><PanelRightIcon />{t("mobile.details")}</Button></SheetTrigger>
          <SheetContent className="data-[side=right]:w-[340px] max-w-full gap-0 p-0 pt-8">
            <SheetHeader className="sr-only"><SheetTitle>{t("inspector.details")}</SheetTitle><SheetDescription>{selected.title}</SheetDescription></SheetHeader>
            {details}
          </SheetContent></Sheet> : null}
        </div>
      </div>
      {readStatus ? <div className="shrink-0">{readStatus}</div> : null}
      <div className="grid shrink-0 grid-cols-[minmax(180px,1.6fr)_104px_104px_116px_minmax(124px,1fr)] gap-2 pb-3">
        <Input aria-label={t("executions.search")} placeholder={t("executions.search")} value={query} onChange={event => setQuery(event.target.value)} />
        <SearchableSelect aria-label={t("executions.kind")} value={kind} onValueChange={setKind} options={[
          { value: "all", label: t("executions.allKinds") }, ...["run", "performance", "comparison"].map(value => ({ value, label: tj(`kinds.${value}`) })),
        ]} />
        <SearchableSelect aria-label={t("executions.location")} value={location} onValueChange={setLocation} options={[
          { value: "all", label: t("executions.allLocations") }, ...["local", "remote"].map(value => ({ value, label: tj(value) })),
        ]} />
        <SearchableSelect aria-label={t("table.status")} value={status} onValueChange={setStatus} options={[
          { value: "all", label: t("executions.allStatuses") }, ...["queued", "starting", "running", "draining", "completed", "failed", "cancelled"].map(value => ({ value, label: tj(`statuses.${value}`) })),
          ...["prepared", "pending", "accepted"].map(value => ({ value: `delivery_${value}`, label: tj(`deliveryStates.${value}`) })),
        ]} />
        <SearchableSelect aria-label={t("plans.aria")} value={plan} onValueChange={setPlan} options={[
          { value: "all", label: t("plans.all") }, ...Array.from(new Map([...presentation.plans.map(item => [item.id, item.name] as const), ...snapshot.runs.map(run => [run.plan_id, run.plan_name] as const)])).map(([value, label]) => ({ value, label })),
        ]} />
      </div>
      {(history.error || runHistory.error) ? <Alert className="mb-2"><AlertDescription>{publicDesktopErrorMessage(history.error || runHistory.error, tj("readFailed"))}</AlertDescription></Alert> : null}
      {commandError ? <Alert variant="destructive" className="mb-2"><AlertDescription>{commandError}</AlertDescription></Alert> : null}
      <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1" onScrollCapture={event => {
        const viewport = event.target as HTMLElement
        if (viewport.dataset.slot === "scroll-area-viewport" && viewport.scrollHeight > viewport.clientHeight &&
          viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop < 120) loadMore()
      }}>
        {visible.length ? <Table aria-label={t("table.aria")} className="table-fixed [&_td]:whitespace-normal [&_td]:[overflow-wrap:anywhere]">
          <colgroup><col className="w-[88px]" /><col /><col /><col /><col className="w-[144px]" /></colgroup>
          <TableHeader><TableRow><TableHead>{t("table.status")}</TableHead><TableHead>{t("table.runPlan")}</TableHead>
            <TableHead>{t("table.target")}</TableHead><TableHead>{t("table.progress")}</TableHead><TableHead>{t("table.started")}</TableHead></TableRow></TableHeader>
          <TableBody>{visible.map(row => <TableRow key={row.id} aria-selected={selected?.id === row.id} data-state={selected?.id === row.id ? "selected" : undefined} onClick={() => setSelectedID(row.id)}>
            <TableCell><ExecutionStatus row={row} /></TableCell>
            <TableCell><Button variant="link" size="sm" className="h-auto w-full justify-start whitespace-normal p-0 text-left text-xs text-foreground" aria-label={t("table.view", { title: row.title })} onClick={() => setSelectedID(row.id)}>{row.title}</Button>
              <p className="mt-1 text-[10px] text-muted-foreground">{tj(`kinds.${row.job?.kind ?? "run"}`)}</p></TableCell>
            <TableCell><p className="text-xs">{row.runs[0]?.model ?? "—"}</p><p className="mt-1 text-[10px] text-muted-foreground">{row.runs[0]?.channel}</p>
              {row.job ? <p className="mt-1 text-[11px]">{tj(row.job.location.kind)}{!row.job.submission ? ` · ${tj("sharedNode")}` : ""}</p> : null}</TableCell>
            <TableCell><ExecutionProgress row={row} /></TableCell>
            <TableCell className="text-[11px] tabular-nums">{row.time ? new Date(row.time).toLocaleString(i18n.resolvedLanguage ?? i18n.language) : "—"}</TableCell>
          </TableRow>)}</TableBody>
        </Table> : <Empty><EmptyTitle>{history.pending ? t("executions.loading") : t(filtered ? plan !== "all" ? "table.emptyFiltered" : "executions.emptyFiltered" : "table.empty")}</EmptyTitle>
          <EmptyDescription>{filtered ? t("executions.noMatches") : t("table.emptyDescription")}</EmptyDescription></Empty>}
      </ScrollArea>
      <div className="flex shrink-0 items-center justify-between gap-2 py-2">
        <span className="text-[11px] text-muted-foreground">{t("executions.historyHint")}</span>
        {history.hasMore || rows.moreStandalone ? <Button variant="outline" size="sm" disabled={history.pending || runHistory.pending} onClick={loadMore}>{t("executions.more")}</Button> : null}
      </div>
    </section>
    {desktop ? <aside aria-label={t("inspector.details")} className="flex min-h-0 min-w-0 w-[320px] shrink-0 flex-col border-l border-divider">{details}</aside> : null}
  </main>
}

function ExecutionStatus({ row }: { row: ExecutionRow }) {
  const { t } = useTranslation("common")
  const { t: tj } = useTranslation("jobs")
  const status = row.job ? row.job.execution?.status ?? row.job.submission!.delivery : row.runs[0].status
  const label = row.job ? row.job.execution ? tj(`statuses.${row.job.execution.status}`)
    : tj(`deliveryStates.${row.job.submission!.delivery}`) : t(`status.${row.runs[0].status}`)
  return <Badge variant="outline" className={cn("h-auto min-h-5 max-w-full gap-1.5 rounded-none border-0 px-0 text-left text-[11px] whitespace-normal", STATUS_CLASS[status])}>
    <span className="status-dot shrink-0" aria-hidden="true" /><span className="min-w-0">{label}</span>
  </Badge>
}

function ExecutionProgress({ row }: { row: ExecutionRow }) {
  const { t } = useTranslation("runs")
  const { t: tj } = useTranslation("jobs")
  const progress = row.job?.progress?.progress
  const run = row.runs[0]
  if (row.job?.kind === "comparison") return <p className="text-[11px] text-muted-foreground">{t("executions.children", { count: row.job.run_ids.length })}</p>
  if (progress) return <p className="text-[11px] tabular-nums">{tj(`phases.${progress.phase}`)}<br />{tj("progress", { completed: progress.completed, planned: progress.planned, active: progress.in_flight })}</p>
  if (!run) return <span className="text-[11px] text-muted-foreground">{t("executions.awaitingData")}</span>
  const percent = run.caseCount ? Math.round(run.observedCaseCount / run.caseCount * 100) : 0
  return <div className="space-y-1 text-[11px] text-muted-foreground">
    {run.caseCount ? <><Progress value={percent} aria-label={t("table.progressAria", { title: run.title, percent })} /><p>{t("caseProgress.count", { completed: run.observedCaseCount, total: run.caseCount })}</p></> : null}
    <p><span>{t("table.completedCount", { count: run.completed })}</span> · {t("table.passed", { count: run.passed })}</p>
    {run.targetDurationMS > 0 ? <p>{t("table.targetDuration", { duration: formatTargetDuration(run.targetDurationMS) })}</p> : null}
  </div>
}

function ExecutionInspector({ row, client, reports, selectedChildID, onSelectChild, detailTab, onDetailTabChange,
  renderRunShortcuts, onJobChange, onOpenReport, onOpenComparison }: {
  row: ExecutionRow; client: JobClient & Partial<RunHistoryClient>; reports: ReportSnapshot
  selectedChildID: string; onSelectChild: (id: string) => void
  detailTab: string; onDetailTabChange: (tab: string) => void
  renderRunShortcuts?: RunShortcutRenderer; onJobChange?: (job: JobView) => void
  onOpenReport: (id: string) => void | Promise<void>
  onOpenComparison?: (id: string) => void | Promise<void>
}) {
  const { t } = useTranslation("runs")
  const run = row.runs.find(item => item.id === selectedChildID) ?? row.runs[0]
  const job = row.job
  const reportIDs = [...new Set([...row.runs.flatMap(run => run.reportID ? [run.reportID] : []), ...(job ? reports.reports.filter(report => job.report_ids.includes(report.id) ||
    (report.run_id && job.run_ids.includes(report.run_id))).map(report => report.id) : [])])]
  if (job?.kind === "performance") return <PerformanceInspector row={row} client={client} reportIDs={reportIDs}
    onJobChange={onJobChange} onOpenReport={onOpenReport} />
  const execution = job ? <JobDetails key={job.job_id} client={client} jobID={job.job_id} initialJob={job}
    reportIDs={reportIDs} onOpenReport={onOpenReport} onJobChange={onJobChange} /> : undefined
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    {job?.kind === "comparison" ? <div className="px-4 pt-3"><p className="mb-2 text-xs font-medium">{t("executions.children", { count: job.run_ids.length })}</p>
      {onOpenComparison ? <Button variant="outline" size="sm" className="mb-2" onClick={() => void onOpenComparison(job.comparison_id)}>{t("executions.comparisonReport")}</Button> : null}
      {row.runs.length ? <SearchableSelect aria-label={t("executions.child")} value={run.id} onValueChange={onSelectChild} options={row.runs.map(child => ({ value: child.id, label: `${child.channel} · ${child.title}` }))} /> : null}</div> : null}
    {run ? <RunInspectorContent key={run.id} run={run} tab={detailTab} onTabChange={onDetailTabChange} execution={execution} shortcuts={renderRunShortcuts?.(run.id)} />
      : <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1 px-4"><h2 className="pt-4 text-sm font-semibold">{row.title}</h2>{execution}</ScrollArea>}
  </div>
}

function PerformanceInspector({ row, client, reportIDs, onJobChange, onOpenReport }: {
  row: ExecutionRow; client: JobClient & Partial<RunHistoryClient>; reportIDs: string[]
  onJobChange?: (job: JobView) => void
  onOpenReport: (id: string) => void | Promise<void>
}) {
  const { t } = useTranslation("runs")
  const { t: tj } = useTranslation("jobs")
  const [tab, setTab] = useState("summary")
  const [archivedID, setArchivedID] = useState("")
  const [availableID, setAvailableID] = useState("")
  useEffect(() => {
    if (!archivedID || !client.getAvailableReports) return
    let active = true
    let timer: number | undefined
    const read = async () => {
      try {
        const ids = await client.getAvailableReports!([archivedID])
        if (!active) return
        if (ids.includes(archivedID)) { setAvailableID(archivedID); return }
      } catch { /* Retry only the selected archive identity while visible. */ }
      if (active) timer = window.setTimeout(() => void read(), 2000)
    }
    void read()
    return () => { active = false; window.clearTimeout(timer) }
  }, [archivedID, client])
  return <JobDetails client={client} jobID={row.job!.job_id} initialJob={row.job}
    onJobChange={onJobChange} onArchived={setArchivedID} showPerformanceResult={false}
    renderLayout={({ job, result, error, details }) => {
      const report = result?.observation?.report
      const failure = job.execution?.failure
      const reportID = availableID || reportIDs[0]
      const outcome = result ? tj(`outcomes.${result.observation_state}`) : tj("resultPending")
      return <RunInspectorFrame title={row.title} id={job.job_id} status={<ExecutionStatus row={{ ...row, job }} />}
        tab={tab} onTabChange={setTab} execution={details}
        summary={<>
          {error ? <Alert role="status"><AlertDescription>{tj("stale")} {publicDesktopErrorMessage(error, tj("readFailed"))}</AlertDescription></Alert> : null}
          <div className="space-y-2 py-2">
            <p className="text-muted-foreground">{outcome}</p>
            <ExecutionProgress row={{ ...row, job }} />
          </div>
          {report ? <PerformanceExecutionSummary report={report} /> : <p className="py-2 text-muted-foreground">
            {result ? tj(result.execution_started ? "noCheckpoint" : "noObservation") : t("executions.awaitingData")}
          </p>}
        </>}
        failures={<div className="space-y-3 py-1">
          {failure ? <p>{failure.phase} · {tj(`common:errorCode.${failure.error_code}`, { defaultValue: failure.error_code })}</p> : null}
          {report?.error_code ? <p>{tj(`common:errorCode.${report.error_code}`)}</p> : null}
          {report?.failures.map(item => <p key={item.error_code}>{tj(`common:errorCode.${item.error_code}`)} · {item.count}</p>)}
          {!failure && !report?.error_code && !report?.failures.length ? <p>{t("inspector.noFailures")}</p> : null}
        </div>}
        artifacts={<p className="py-1">{reportID ? t("shortcuts.viewReport") : t("inspector.noArtifacts")}</p>}
        footer={<div className="shrink-0 border-t border-divider px-4 py-3">
          <Button size="sm" variant="outline" disabled={!reportID} onClick={() => { if (reportID) void onOpenReport(reportID) }}>
            <FileTextIcon />{t("shortcuts.viewReport")}
          </Button>
        </div>}
      />
    }} />
}

function PerformanceExecutionSummary({ report }: { report: QuickPerformanceReport }) {
  const { t } = useTranslation("runs")
  const { profile, progress } = report
  const load = profile.load_mode === "open_loop"
    ? t("presentation.openLoop", { rate: profile.rate_per_second ?? "—" })
    : t("quickTest:performance.concurrencySummary", { count: profile.concurrency })
  const fields = [
    [t("inspector.model"), report.model_id || "—"],
    [t("inspector.channel"), report.base_url || report.endpoint || "—"],
    [t("inspector.load"), load],
    [t("table.progress"), progress.planned > 0
      ? t("inspector.completedFraction", { completed: progress.completed, total: progress.planned })
      : t("inspector.completedRequests", { count: progress.completed })],
    [t("inspector.duration"), formatTargetDuration(progress.total_duration_ms)],
  ]
  return <dl data-slot="inspector-description-list" className="space-y-1">
    {fields.map(([label, value]) => <InspectorDefinitionRow key={label} label={label} value={value} />)}
  </dl>
}
