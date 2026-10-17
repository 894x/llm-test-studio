import { useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import PanelRightIcon from "lucide-react/dist/esm/icons/panel-right.mjs"
import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
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
import type { JobView } from "@/features/jobs/data"
import type { JobClient } from "@/features/jobs/execution"
import { QuickPerformanceReportPanel } from "@/features/quick-test/quick-performance-sheet"
import type { ReportSnapshot } from "@/features/reports/data"
import { formatTargetDuration, presentWorkspace, type RunRecord, type WorkspaceSnapshot } from "./data"
import { RunInspectorContent } from "./run-workspace"
import type { RunShortcutRenderer } from "./use-run-shortcuts"
import { useExecutionJobs } from "./use-execution-jobs"

type ExecutionRow = { id: string; job?: JobView; runs: RunRecord[]; title: string; time: string }
const desktopQuery = "(min-width: 1180px)"
const subscribeDesktop = (notify: () => void) => {
  const media = window.matchMedia(desktopQuery)
  media.addEventListener("change", notify)
  return () => media.removeEventListener("change", notify)
}
const isDesktop = () => window.matchMedia(desktopQuery).matches

export function RunWorkspace({ client, snapshot, reports, preferredJob, preferredRunID, commandError,
  renderRunShortcuts, onJobChange, onOpenReport, onOpenComparison, readStatus }: {
  client: JobClient; snapshot: WorkspaceSnapshot; reports: ReportSnapshot
  preferredJob: JobView | null; preferredRunID?: string; commandError: string
  renderRunShortcuts?: RunShortcutRenderer
  onJobChange?: (job: JobView) => void
  onOpenReport: (id: string) => void | Promise<void>
  onOpenComparison?: (id: string) => void | Promise<void>
  readStatus?: ReactNode
}) {
  const { t, i18n } = useTranslation("runs")
  const { t: tj } = useTranslation("jobs")
  const history = useExecutionJobs(client, preferredJob)
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
    return { executions, moreStandalone: standalone.length > standaloneLimit }
  }, [history.jobs, presentation.runs, snapshot.runs, standaloneLimit, tj])
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

  return <main className="flex min-h-0 min-w-0 flex-1 gap-4 pl-4">
    <section aria-labelledby="workspace-heading" className="flex min-h-0 min-w-0 flex-1 flex-col max-[1179px]:pr-4">
      <div className="flex shrink-0 items-center justify-between gap-3 py-3">
        <div className="min-w-0"><h1 id="workspace-heading" className="text-lg font-semibold tracking-tight">{t("title")}</h1>
          <p className="mt-1 text-[11px] text-muted-foreground">{t("description")}</p></div>
        {!desktop && selected ? <Sheet><SheetTrigger asChild><Button variant="outline" size="sm"><PanelRightIcon />{t("mobile.details")}</Button></SheetTrigger>
          <SheetContent className="data-[side=right]:w-[340px] max-w-full gap-0 p-0 pt-8">
            <SheetHeader className="sr-only"><SheetTitle>{t("inspector.details")}</SheetTitle><SheetDescription>{selected.title}</SheetDescription></SheetHeader>
            {details}
          </SheetContent></Sheet> : null}
      </div>
      {readStatus ? <div className="shrink-0">{readStatus}</div> : null}
      <div className="grid shrink-0 grid-cols-2 gap-2 pb-3 min-[1180px]:grid-cols-4">
        <Input aria-label={t("executions.search")} placeholder={t("executions.search")} value={query} onChange={event => setQuery(event.target.value)} className="col-span-2" />
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
          { value: "all", label: t("plans.all") }, ...presentation.plans.map(item => ({ value: item.id, label: item.name })),
        ]} />
        <div className="col-span-2 flex items-center justify-end gap-2 text-[11px] text-muted-foreground">
          <span>{t("executions.shown", { count: visible.length })}</span>
          {filtered ? <Button size="sm" variant="ghost" onClick={() => { setQuery(""); setKind("all"); setLocation("all"); setStatus("all"); setPlan("all") }}>{t("executions.clear")}</Button> : null}
          <Button size="sm" variant="ghost" disabled={history.pending} onClick={history.retry}>{history.pending ? <Spinner /> : null}{t("executions.refresh")}</Button>
        </div>
      </div>
      {history.error ? <Alert className="mb-2"><AlertDescription>{publicDesktopErrorMessage(history.error, tj("readFailed"))}</AlertDescription></Alert> : null}
      {commandError ? <Alert variant="destructive" className="mb-2"><AlertDescription>{commandError}</AlertDescription></Alert> : null}
      <ScrollArea className="min-h-0 min-w-0 flex-1">
        {visible.length ? <Table aria-label={t("table.aria")} className="table-fixed [&_td]:whitespace-normal [&_td]:[overflow-wrap:anywhere]">
          <TableHeader><TableRow><TableHead className="w-[88px]">{t("table.status")}</TableHead><TableHead>{t("table.runPlan")}</TableHead>
            <TableHead>{t("table.target")}</TableHead><TableHead>{t("table.progress")}</TableHead><TableHead className="w-[112px]">{t("table.started")}</TableHead></TableRow></TableHeader>
          <TableBody>{visible.map(row => <TableRow key={row.id} aria-selected={selected?.id === row.id} data-state={selected?.id === row.id ? "selected" : undefined} onClick={() => setSelectedID(row.id)}>
            <TableCell><span className="inline-flex items-center gap-1.5 text-xs"><span className="status-dot" aria-hidden="true" />
              {row.job ? row.job.execution ? tj(`statuses.${row.job.execution.status}`) : tj(`deliveryStates.${row.job.submission!.delivery}`) : t(`common:status.${row.runs[0].status}`)}</span></TableCell>
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
        {history.hasMore || rows.moreStandalone ? <Button variant="outline" size="sm" disabled={history.pending} onClick={() => { history.loadMore(); setStandaloneLimit(limit => limit + 25) }}>{t("executions.more")}</Button> : null}
      </div>
    </section>
    {desktop ? <aside aria-label={t("inspector.details")} className="flex min-h-0 min-w-0 w-[320px] shrink-0 flex-col border-l border-divider">{details}</aside> : null}
  </main>
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
  row: ExecutionRow; client: JobClient; reports: ReportSnapshot
  selectedChildID: string; onSelectChild: (id: string) => void
  detailTab: string; onDetailTabChange: (tab: string) => void
  renderRunShortcuts?: RunShortcutRenderer; onJobChange?: (job: JobView) => void
  onOpenReport: (id: string) => void | Promise<void>
  onOpenComparison?: (id: string) => void | Promise<void>
}) {
  const { t } = useTranslation("runs")
  const run = row.runs.find(item => item.id === selectedChildID) ?? row.runs[0]
  const job = row.job
  const reportIDs = job ? reports.reports.filter(report => job.report_ids.includes(report.id) ||
    (report.run_id && job.run_ids.includes(report.run_id))).map(report => report.id) : []
  const execution = job ? <JobDetails key={job.job_id} client={client} jobID={job.job_id} initialJob={job}
    reportIDs={reportIDs} onOpenReport={onOpenReport} onJobChange={onJobChange}
    renderPerformance={report => <QuickPerformanceReportPanel report={report} onOpenReport={onOpenReport} />} /> : undefined
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    {job?.kind === "comparison" ? <div className="px-4 pt-3"><p className="mb-2 text-xs font-medium">{t("executions.children", { count: job.run_ids.length })}</p>
      {onOpenComparison ? <Button variant="outline" size="sm" className="mb-2" onClick={() => void onOpenComparison(job.comparison_id)}>{t("executions.comparisonReport")}</Button> : null}
      {row.runs.length ? <SearchableSelect aria-label={t("executions.child")} value={run.id} onValueChange={onSelectChild} options={row.runs.map(child => ({ value: child.id, label: `${child.channel} · ${child.title}` }))} /> : null}</div> : null}
    {run ? <RunInspectorContent key={run.id} run={run} tab={detailTab} onTabChange={onDetailTabChange} execution={execution} shortcuts={renderRunShortcuts?.(run.id)} />
      : <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1 px-4"><h2 className="pt-4 text-sm font-semibold">{row.title}</h2>{execution}</ScrollArea>}
  </div>
}
