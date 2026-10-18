import { createComparisonHistoryResource } from "@/features/comparisons/history-resource"
import { RetainedWorkspace } from "@/features/shell/retained-workspace"
import { useStartupProgress } from "@/app/startup-progress"
import { RemoteNodesMenu } from "@/features/remote-nodes/remote-nodes-menu"
import { launchAtDefault } from "@/features/jobs/execution"
import type { JobView } from "@/features/jobs/data"
import { localizeStoredMessage } from "@/i18n/runtime"
import { lazy, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"

import {
  createDesktopClient,
  isCatalogSavedRefreshFailure,
  DesktopClientError,
  publicDesktopErrorMessage,
  publicDesktopOperationErrorMessage,
  type DesktopClient,
} from "@/app/desktop-client"
import { createLazyResource, useResource } from "@/app/lazy-resource"
import { EMPTY_COMPARISONS } from "@/features/comparisons/data"
import { EMPTY_CATALOG, selectCatalog, type CatalogScope } from "@/features/catalog/data"
import { ThemeProvider } from "@/app/theme"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { createAppI18n } from "@/i18n/i18n"
import { LanguageProvider } from "@/i18n/language-context"
import { loadLanguagePreference, resolveLocale } from "@/i18n/locale"
import { TooltipProvider } from "@/components/ui/tooltip"
import { TransientOverlayProvider } from "@/components/ui/transient-overlays"
import { useDismissTransientOverlays } from "@/components/ui/use-transient-overlay"
import type { CatalogSnapshot, SavedQuickTestTarget } from "@/features/catalog/data"
import { createTaskDraft, decodeTaskDraft, encodeTaskDraft, TASK_DRAFT_KEY, type TaskDraft } from "@/features/quick-test/task-draft"
import type { QuickTaskJobEntry } from "@/features/quick-test/quick-task-workspace"
import { EMPTY_REPORTS, type ReportSnapshot } from "@/features/reports/data"
import type { ReportGenerationProgress } from "@/features/reports/generation-progress"
import { ReportGenerationStatus } from "@/features/reports/report-generation-status"
import { NewComparisonSheet } from "@/features/comparisons/comparison-workspace"
import type { ComparisonSnapshot, StartComparisonCommand } from "@/features/comparisons/data"
import { EMPTY_WORKSPACE, isRunActive, presentPlans, type StartRunTargetCommand } from "@/features/runs/data"
import {
  NewRunSheet,
} from "@/features/runs/run-workspace"
import { RunWorkspace } from "@/features/runs/execution-workspace"
import { useRunShortcuts } from "@/features/runs/use-run-shortcuts"
import {
  DesktopShell,
} from "@/features/shell/desktop-shell"
import { DiagnosticsSheet } from "@/features/shell/diagnostics-sheet"
import {
  DESKTOP_PAGES,
  desktopPageFromHash,
  type DesktopPage,
} from "@/features/shell/navigation"

const OverviewWorkspace = lazy(() => import("@/features/overview/overview-workspace").then(module => ({ default: module.OverviewWorkspace })))
const QuickTaskPage = lazy(() => import("@/features/quick-test/quick-task-page").then(module => ({ default: module.QuickTaskPage })))
const ReportWorkspace = lazy(() => import("@/features/reports/report-workspace").then(module => ({ default: module.ReportWorkspace })))
const ModelChannelWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.ModelChannelWorkspace })))
const CasesWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.CasesWorkspace })))
const PlansWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.PlansWorkspace })))

const PAGE_READS = {
  overview: ["overview"],
  "quick-test": ["quickCatalog"],
  catalog: ["connections"], cases: ["definitions"], plans: ["catalog"],
  runs: [], reports: ["reports"],
} as const
type ResourceKey = "overview" | "workspace" | "catalog" | "reports" | "comparisons" | "connections" | "definitions" | "quickCatalog"

function persistDraft(draft: TaskDraft): void {
  if (!draft.task && !draft.base_url && !draft.model) return
  try { localStorage.setItem(TASK_DRAFT_KEY, encodeTaskDraft(draft)) }
  catch { /* The in-memory draft remains usable when storage is unavailable. */ }
}

function AppWorkspace({
  client,
  onRecreateClient,
}: {
  client: DesktopClient
  onRecreateClient: () => boolean
}) {
  const startup = useStartupProgress(client)
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation(["app", "runs"])
  const [page, setPage] = useState<DesktopPage>(() =>
    desktopPageFromHash(window.location.hash),
  )
  const pageRef = useRef(page)
  const dismissOverlays = useDismissTransientOverlays()
  const changePage = useCallback((next: DesktopPage) => {
    if (pageRef.current === next) return
    dismissOverlays?.()
    pageRef.current = next
    setPage(next)
  }, [dismissOverlays])
  useEffect(() => { pageRef.current = page }, [page])
  const resources = useMemo(() => {
    const catalog = createLazyResource(() => client.getCatalog("all"))
    const scopedCatalog = (scope: CatalogScope) => createLazyResource(() => {
      const full = catalog.getSnapshot()
      return full.data && !full.stale ? Promise.resolve(selectCatalog(full.data, scope)) : client.getCatalog(scope)
    })
    const comparisonHistory = createComparisonHistoryResource((after, ids) => client.getComparisons(after, ids))
    return {
      overview: createLazyResource(() => client.getOverview()),
      workspace: createLazyResource(() => client.getWorkspace()),
      catalog, connections: scopedCatalog("connections"), definitions: scopedCatalog("definitions"), quickCatalog: scopedCatalog("quick_test"),
      reports: createLazyResource(() => client.getReports()),
      comparisons: comparisonHistory.resource,
      queueComparisonRun: comparisonHistory.queueRun,
    }
  }, [client])
  const overviewState = useResource(resources.overview)
  const workspaceState = useResource(resources.workspace)
  const catalogState = useResource(resources.catalog)
  const connectionState = useResource(resources.connections)
  const definitionState = useResource(resources.definitions)
  const quickCatalogState = useResource(resources.quickCatalog)
  const reportState = useResource(resources.reports)
  const comparisonState = useResource(resources.comparisons)
  const snapshot = workspaceState.data
  const reports = reportState.data
  const comparisons = comparisonState.data
  const setSnapshot = resources.workspace.set
  const setCatalog = useCallback((next: CatalogSnapshot) => {
    resources.overview.invalidate()
    resources.catalog.set(next)
    resources.connections.set(selectCatalog(next, "connections"))
    resources.definitions.set(selectCatalog(next, "definitions"))
    resources.quickCatalog.set(selectCatalog(next, "quick_test"))
  }, [resources])
  const setReports = resources.reports.set
  const setComparisons = resources.comparisons.set
  const comparisonAppend = useRef(false)
  const [comparisonAppending, setComparisonAppending] = useState(false)
  const [comparisonAppendError, setComparisonAppendError] = useState(false)
  const loadMoreComparisons = async () => {
    if (!comparisons?.next_cursor || comparisonState.pending || comparisonAppend.current) return
    comparisonAppend.current = true
    setComparisonAppending(true)
    setComparisonAppendError(false)
    try {
      const next = await client.getComparisons(comparisons.next_cursor)
      const merged = new Map((resources.comparisons.getSnapshot().data ?? EMPTY_COMPARISONS).comparisons.map(item => [item.id, item]))
      next.comparisons.forEach(item => merged.set(item.id, item))
      const combined = { ...next, comparisons: [...merged.values()] }
      setComparisons(combined)
    } catch { setComparisonAppendError(true) }
    finally { comparisonAppend.current = false; setComparisonAppending(false) }
  }

  const [showComparisons, setShowComparisons] = useState(false)
  const states = { overview: overviewState, workspace: workspaceState, catalog: catalogState, connections: connectionState, definitions: definitionState, quickCatalog: quickCatalogState, reports: reportState, comparisons: comparisonState }
  const [reportGeneration, setReportGeneration] = useState<Record<string, ReportGenerationProgress>>({})
  const reportReadGeneration = useRef(0)
  const [preferredReportID, setPreferredReportID] = useState("")
  const [reportOpenSequence, setReportOpenSequence] = useState(0)
  const [preferredRunID, setPreferredRunID] = useState("")
  const [preferredJob, setPreferredJob] = useState<JobView | null>(null)
  const [pendingComparisonIDs, setPendingComparisonIDs] = useState<string[]>([])
  const receiveComparisons = useCallback((next: ComparisonSnapshot) => {
    setComparisons(next)
    setPendingComparisonIDs(ids => ids.filter(id => !next.comparisons.some(comparison => comparison.id === id)))
  }, [setComparisons, setPendingComparisonIDs])
  const [openingLogs, setOpeningLogs] = useState(false)
  const [openLogsError, setOpenLogsError] = useState<unknown>(null)
  const [commandError, setCommandError] = useState("")
  const [commandPending, setCommandPending] = useState(false)
  const [catalogMutationPending, setCatalogMutationPending] = useState(false)
  const [catalogMutationError, setCatalogMutationError] = useState("")
  const [quickRunID, setQuickRunID] = useState("")
  const [quickFunctionalJobs, setQuickFunctionalJobs] = useState<QuickTaskJobEntry[]>([])
  const [startingRunID, setStartingRunID] = useState("")
  const [quickDraft, setQuickDraft] = useState<TaskDraft>(() => {
    try { return decodeTaskDraft(localStorage.getItem(TASK_DRAFT_KEY)) ?? createTaskDraft(null) }
    catch { return createTaskDraft(null) }
  })
  const latestDraft = useRef(quickDraft)
  const workspaceGeneration = useRef(0)
  const comparisonGeneration = useRef(0)
  const catalogGeneration = useRef(0)
  const invalidateWorkspace = useCallback(() => {
    workspaceGeneration.current += 1
    resources.workspace.invalidate()
    resources.overview.invalidate()
  }, [resources])
  const refreshWorkspace = useCallback(async () => {
    const generation = workspaceGeneration.current
    const nextWorkspace = await resources.workspace.refresh()
    if (generation === workspaceGeneration.current) setSnapshot(nextWorkspace)
  }, [resources, setSnapshot])

  useEffect(() => {
    latestDraft.current = quickDraft
    const timer = window.setTimeout(() => persistDraft(quickDraft), 250)
    return () => window.clearTimeout(timer)
  }, [quickDraft])

  useEffect(() => {
    const flush = () => persistDraft(latestDraft.current)
    window.addEventListener("pagehide", flush)
    return () => {
      window.removeEventListener("pagehide", flush)
      flush()
    }
  }, [])

  useEffect(() => {
    const syncPage = () => changePage(desktopPageFromHash(window.location.hash))
    window.addEventListener("hashchange", syncPage)
    return () => window.removeEventListener("hashchange", syncPage)
  }, [changePage])

  useEffect(() => {
    PAGE_READS[page].forEach(key => { void resources[key].load().catch(() => {}) })
  }, [page, resources])

  const retryInitialLoad = useCallback(() => {
    setOpenLogsError(null)
    if (onRecreateClient()) return
    PAGE_READS[page].forEach(key => { void resources[key].load().catch(() => {}) })
  }, [onRecreateClient, page, resources])

  const openLogs = async () => {
    setOpeningLogs(true)
    setOpenLogsError(null)
    try {
      await client.openDiagnosticsDirectory()
    } catch (error) {
      setOpenLogsError(error)
    } finally {
      setOpeningLogs(false)
    }
  }

  const [retryWorkspacePoll, setRetryWorkspacePoll] = useState(false)
  const [retryComparisonPoll, setRetryComparisonPoll] = useState(false)
  const comparisonRunning = !!comparisons?.comparisons.some((comparison) => comparison.status === "running")
  const startingRunPending = !!startingRunID && !snapshot?.runs.some(run => run.id === startingRunID)
  const workspaceVisible = (PAGE_READS[page] as readonly string[]).includes("workspace")
  const shouldPollWorkspace = workspaceVisible && !!(retryWorkspacePoll || startingRunPending || snapshot?.runs.some((run) => isRunActive(run.status)) || comparisonRunning || (quickRunID && !snapshot?.runs.some((run) => run.id === quickRunID)))
  useEffect(() => {
		if (!shouldPollWorkspace) return
		let active = true
		let timer: number | undefined
		const refresh = async () => {
			const generation = workspaceGeneration.current
			try {
				const nextWorkspace = await resources.workspace.refresh()
				if (active && generation === workspaceGeneration.current) {
					setRetryWorkspacePoll(false)
					setSnapshot(nextWorkspace)
				}
			} catch {
				if (active && generation === workspaceGeneration.current) setRetryWorkspacePoll(true)
			} finally {
				if (active) timer = window.setTimeout(() => void refresh(), 1_000)
			}
		}
		timer = window.setTimeout(() => void refresh(), 1_000)
		return () => { active = false; window.clearTimeout(timer); invalidateWorkspace() }
	}, [resources, shouldPollWorkspace, setSnapshot, invalidateWorkspace])

  const overviewActive = overviewState.data?.workspace.runs.some(run => isRunActive(run.status))
  useEffect(() => {
    if (page !== "overview" || !overviewActive) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      try {
        const current = resources.overview.getSnapshot().data
        if (!current) return
        const next = await client.getRunSummaries(current.workspace.runs.map(run => run.id))
        if (!active) return
        if (next.runs.some(run => isRunActive(run.status))) {
          resources.overview.set({ ...current, workspace: { ...current.workspace, runs: next.runs } })
        } else { await resources.overview.refresh() }
      } catch { /* Keep the retained overview and retry while visible. */ }
      finally { if (active) timer = window.setTimeout(() => void refresh(), 2000) }
    }
    timer = window.setTimeout(() => void refresh(), 2000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [page, overviewActive, client, resources])

  const shouldPollComparisons = (page === "runs" || page === "reports") && showComparisons && (comparisonRunning || pendingComparisonIDs.length > 0 || retryComparisonPoll)
  useEffect(() => {
    if (!shouldPollComparisons) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      const generation = comparisonGeneration.current
      try {
        const nextComparisons = await resources.comparisons.refresh()
        if (active && generation === comparisonGeneration.current) {
          setRetryComparisonPoll(false)
          receiveComparisons(nextComparisons)
          if (workspaceVisible) void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
        }
      } catch {
        if (active && generation === comparisonGeneration.current) setRetryComparisonPoll(true)
      } finally {
        if (active) timer = window.setTimeout(() => void refresh(), 1_000)
      }
    }
    timer = window.setTimeout(() => void refresh(), 1_000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [resources, shouldPollComparisons, receiveComparisons, refreshWorkspace, workspaceVisible])

  const [reportRetryUntil, setReportRetryUntil] = useState(0)
  const updateReports = useCallback((nextReports: ReportSnapshot) => {
    setReports(nextReports)
    setReportRetryUntil(0)
  }, [setReports])
  const retryReports = useCallback(() => {
    setReportRetryUntil((until) => until || Date.now() + 60_000)
  }, [])
  const refreshReports = useCallback(async () => {
    const generation = reportReadGeneration.current
    try {
      const nextReports = await resources.reports.refresh()
      if (generation === reportReadGeneration.current) updateReports(nextReports)
      return nextReports
    } catch (error) {
      if (generation === reportReadGeneration.current) retryReports()
      throw error
    }
  }, [resources, updateReports, retryReports])

  useEffect(() => {
    let active = true
    const latest = new Map<string, number>()
    const receive = (progress: ReportGenerationProgress) => {
      if (!active || progress.sequence <= (latest.get(progress.run_id) ?? 0)) return
      latest.set(progress.run_id, progress.sequence)
      setReportGeneration((previous) => ({ ...previous, [progress.run_id]: progress }))
      if (progress.phase === "ready") {
        const affected = resources.queueComparisonRun(progress.run_id)
        if (affected.length) setPendingComparisonIDs(ids => [...new Set([...ids, ...affected])])
        reportReadGeneration.current += 1
        const visibleReads: readonly string[] = PAGE_READS[pageRef.current]
        resources.reports.invalidate()
        invalidateWorkspace()
        void Promise.all([
          ...(visibleReads.includes("reports") ? [refreshReports()] : []),
          ...(visibleReads.includes("overview") ? [resources.overview.refresh()] : []),
          ...(visibleReads.includes("workspace") ? [refreshWorkspace()] : []),
        ]).catch(() => {
          // A transient read failure is retried without restarting generation.
        })
      }
    }
    const unsubscribe = client.subscribeReportGeneration(receive)
    void client.getReportGeneration().then((next) => {
      if (active) next.runs.forEach(receive)
    }).catch((error: unknown) => {
      if (active) setCommandError(publicDesktopErrorMessage(error, t("app:loadError")))
    })
    return () => { active = false; unsubscribe() }
  }, [client, resources, invalidateWorkspace, refreshReports, refreshWorkspace, t])

  useEffect(() => {
    if (!reportRetryUntil || !PAGE_READS[page].some(key => key === "reports")) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      if (Date.now() >= reportRetryUntil) {
        setReportRetryUntil(0)
        return
      }
      try { await refreshReports() } catch { /* Retry a failed list read. */ }
      if (active) timer = window.setTimeout(() => void refresh(), 2_000)
    }
    timer = window.setTimeout(() => void refresh(), 2_000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [refreshReports, reportRetryUntil, page])


  const lastObservedJob = useRef("")
  const receiveJobObservation = useCallback((job: JobView) => {
    const marker = `${job.job_id}:${job.submission?.delivery_revision}:${job.submission?.confirmation?.revision}:${job.execution?.revision}:${job.execution?.latest_sequence}:${job.performance_result_ready}`
    if (lastObservedJob.current === marker) return
    lastObservedJob.current = marker
    if (job.kind === "comparison" && job.submission?.delivery !== "prepared" && job.comparison_id) {
      setPendingComparisonIDs(ids => ids.includes(job.comparison_id) ? ids : [...ids, job.comparison_id])
    }
    invalidateWorkspace()
    resources.comparisons.invalidate()
    resources.reports.invalidate()
    const visibleReads: readonly string[] = PAGE_READS[pageRef.current]
    if (visibleReads.includes("workspace")) void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
    if (showComparisons && (pageRef.current === "runs" || pageRef.current === "reports")) {
      void resources.comparisons.refresh().then(receiveComparisons).catch(() => setRetryComparisonPoll(true))
    }
    if (visibleReads.includes("reports")) void refreshReports().catch(() => { /* The reports read owns its retry state. */ })
  }, [invalidateWorkspace, resources, receiveComparisons, refreshWorkspace, refreshReports, showComparisons, setPendingComparisonIDs, setRetryComparisonPoll])

  const navigate = useCallback((next: DesktopPage) => {
    if (desktopPageFromHash(window.location.hash) === next) {
      changePage(next)
      return
    }
    window.location.hash = next
  }, [changePage])

  const refreshArchivedPerformanceReport = useCallback(async (reportID: string): Promise<void> => {
    resources.reports.invalidate()
    setPreferredReportID(reportID)
  }, [resources])

  const openReport = useCallback(async (reportID: string): Promise<void> => {
    setPreferredReportID(reportID)
    setReportOpenSequence(sequence => sequence + 1)
    navigate("reports")
    try {
      await refreshReports()
    } catch {
      // Keep the reports workspace open with the last authoritative snapshot.
    }
  }, [refreshReports, navigate])

  const rerunStarted = useCallback((runID: string) => {
    invalidateWorkspace()
    setStartingRunID(runID)
    setPreferredRunID(runID)
    navigate("runs")
  }, [invalidateWorkspace, navigate])

  const renderRunShortcuts = useRunShortcuts({
    active: page === "runs" || page === "reports",
    client, workspace: snapshot, reports, generation: reportGeneration,
    draft: quickDraft, commandPending, onJobLaunched: setPreferredJob, onStarted: rerunStarted,
    onOpenReport: openReport, refreshReports,
    loadWorkspace: resources.workspace.load, loadCatalog: resources.quickCatalog.load,
  })

  const startRun = useCallback(async (command: StartRunTargetCommand): Promise<void> => {
    setCommandPending(true)
    setCommandError("")
    try {
      const job = await launchAtDefault(client, { kind: "plan", plan: command, quick_task: null, performance: null, comparison: null })
      setPreferredJob(job)
      invalidateWorkspace()
      if (job.run_ids[0]) setStartingRunID(job.run_ids[0])
    } catch (error) {
      setCommandError(publicDesktopErrorMessage(error, t("app:commandError")))
      throw error
    } finally {
      setCommandPending(false)
    }
  }, [client, invalidateWorkspace, setPreferredJob, t])

  const mutateCatalog = useCallback(
    async (operation: () => Promise<CatalogSnapshot>, operationLabel: string): Promise<void> => {
      setCatalogMutationPending(true)
      setCatalogMutationError("")
      try {
        const nextCatalog = await operation()
        catalogGeneration.current += 1
        setCatalog(nextCatalog)
        invalidateWorkspace()
      } catch (error) {
        if (isCatalogSavedRefreshFailure(error)) {
          const generation = ++catalogGeneration.current
          resources.catalog.invalidate()
          resources.connections.invalidate()
          resources.definitions.invalidate()
          resources.quickCatalog.invalidate()
          invalidateWorkspace()
          void resources.catalog.refresh().then((nextCatalog) => {
            if (generation === catalogGeneration.current) setCatalog(nextCatalog)
          }, () => { /* The mutation is already committed. */ })
          setCatalogMutationError(publicDesktopErrorMessage(error, tx("desktop:app_saved_but_the_catalog_could_not_refresh_refresh_or_reopen")))
          return
        }
        setCatalogMutationError(publicDesktopOperationErrorMessage(error, operationLabel, tx("desktop:app_catalog_operation_failed_check_whether_the_item_is_still_referenced")))
        throw error
      } finally {
        setCatalogMutationPending(false)
      }
    },
    [resources, setCatalog, invalidateWorkspace, tx],
  )

  const startComparison = useCallback(async (command: StartComparisonCommand): Promise<void> => {
		setCommandPending(true)
		setCommandError("")
		try {
      const job = await launchAtDefault(client, { kind: "comparison", comparison: command, plan: null, quick_task: null, performance: null })
      setPreferredJob(job)
      if (job.comparison_id) setPendingComparisonIDs(ids => [...new Set([...ids, job.comparison_id])])
      comparisonGeneration.current += 1
      resources.comparisons.invalidate()
      void resources.comparisons.refresh().then(receiveComparisons).catch(() => setRetryComparisonPoll(true))
      setShowComparisons(true)
			invalidateWorkspace()
			if ((PAGE_READS[pageRef.current] as readonly string[]).includes("workspace")) void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
		} catch (error) {
			setCommandError(publicDesktopErrorMessage(error, t("app:comparisonError")))
			throw error
		} finally {
			setCommandPending(false)
		}
	}, [client, resources, invalidateWorkspace, receiveComparisons, refreshWorkspace, setPreferredJob, setPendingComparisonIDs, setRetryComparisonPoll, t])

  const workspacePlans = snapshot?.plans
  const plans = useMemo(
    () => (workspacePlans ? presentPlans(workspacePlans, {
      locale: i18n.resolvedLanguage ?? i18n.language,
      t: (key, values) => t(`runs:${key}`, values),
    }) : (catalogState.data?.plans.map(plan => ({ id: plan.id, name: plan.name, caseCount: plan.case_count, runCount: 0, description: "" })) ?? [])),
    [i18n.language, i18n.resolvedLanguage, workspacePlans, catalogState.data, t],
  )

  const renderWorkspace = (page: DesktopPage) => {
    const requiredReads: readonly ResourceKey[] = PAGE_READS[page]
    const failedRead = requiredReads.find(key => states[key].error !== null && states[key].data === null)
    const loadError = startup?.status === "failed" ? new DesktopClientError("desktop_startup_failed") : failedRead ? states[failedRead].error : null
    const pageReady = startup?.status !== "running" && requiredReads.every(key => states[key].data !== null)
    const catalog = (page === "catalog" ? connectionState : page === "cases" ? definitionState : page === "quick-test" ? quickCatalogState : catalogState).data

    const pageStatus = loadError ? (
        <div className="flex min-h-0 flex-1 items-center justify-center bg-background p-6 text-foreground">
          <div role="alert" className="max-w-md border-l-2 border-destructive pl-4">
            <div className="text-sm font-semibold">{tx("desktop:app_unable_to_open_the_local_workspace")}</div>
            <p className="mt-1 text-xs text-muted-foreground">{publicDesktopErrorMessage(loadError, t("app:loadError"))}</p>
            {startup?.status === "failed" ? <p className="mt-2 text-xs text-muted-foreground">{t("app:startup.failedStage", { stage: t(`app:startup.stages.${startup.stage}`) })}</p> : null}
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" onClick={retryInitialLoad}>
                {tx("desktop:app_retry_opening")}
              </Button>
              <Button size="sm" variant="outline" disabled={openingLogs} aria-busy={openingLogs} onClick={() => void openLogs()}>
                {openingLogs ? <Spinner /> : <FolderOpenIcon />}
                {t(openingLogs ? "app:openingLogs" : "app:openLogs")}
              </Button>
            </div>
            {openLogsError !== null && (
              <p role="status" className="mt-2 text-xs text-destructive">
                {publicDesktopOperationErrorMessage(openLogsError, t("app:openLogs"), tx("shell:diagnostics.openError"))}
              </p>
            )}
          </div>
        </div>
    ) : !pageReady ? (
      <div role="status" aria-live="polite" aria-busy="true" className="flex min-h-0 flex-1 items-center justify-center gap-2 text-xs text-muted-foreground">
        <Spinner /><div className="space-y-1">
          <div>{startup && startup.status !== "completed" ? t(`app:startup.stages.${startup.stage}`) : t("app:loadingPage")}</div>
          {startup?.status === "running" ? <p>{t("app:startup.elapsed", { seconds: Math.floor(startup.elapsed_ms / 1000) })}{startup.from_version > 0 && startup.to_version > startup.from_version ? ` · ${t("app:startup.version", { from: startup.from_version, to: startup.to_version })}` : ""}</p> : null}
          {startup?.stage === "database_backing_up" || startup?.stage === "database_migrating" || startup?.stage === "database_validating" ? <p>{t("app:startup.keepOpen")}</p> : null}
        </div>
      </div>
    ) : null

      return (page === "runs" && startup?.status !== "running" && startup?.status !== "failed" ? null : pageStatus) ?? (page === "overview" ? (
        <OverviewWorkspace workspace={overviewState.data!.workspace} catalog={EMPTY_CATALOG} reports={overviewState.data!.reports} counts={overviewState.data!.catalog} runCount={overviewState.data!.run_count} reportCount={overviewState.data!.report_count} onOpenReport={openReport} />
      ) : page === "quick-test" ? (
        <QuickTaskPage
          catalog={catalog!}
          historyRevision={reportGeneration}
          draft={quickDraft}
          onDraftChange={setQuickDraft}
          runID={quickRunID}
          onRunSelected={setQuickRunID}
          actions={client}
          onJobLaunched={setPreferredJob}
          functionalJobs={quickFunctionalJobs}
          onFunctionalJobsChange={setQuickFunctionalJobs}
          onJobChange={receiveJobObservation}
          onOpenExecution={(job, runID) => { setPreferredJob(job); setPreferredRunID(runID ?? ""); navigate("runs") }}
          onSaveChannel={async command => {
            let saved: SavedQuickTestTarget | undefined
            await mutateCatalog(async () => { saved = await client.saveQuickTestTarget(command); return saved.catalog }, t("quickTest:saveChannel.title"))
            if (!saved) throw new DesktopClientError("catalog_saved_refresh_failed")
            return saved
          }}
          onPerformanceArchived={refreshArchivedPerformanceReport}
          onOpenReport={openReport}
        />
      ) : page === "catalog" ? (
        <ModelChannelWorkspace catalog={catalog!} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={localizeStoredMessage(catalogMutationError, tx)} />
      ) : page === "cases" ? (
        <CasesWorkspace catalog={catalog!} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={localizeStoredMessage(catalogMutationError, tx)} />
      ) : page === "plans" ? (
        <PlansWorkspace
          catalog={catalog!}
          actions={client}
          mutate={mutateCatalog}
          mutationPending={catalogMutationPending}
          mutationError={localizeStoredMessage(catalogMutationError, tx)}
          commandPending={commandPending}
          executionClient={client}
          onStartPlan={async (command) => {
            await startRun(command)
            navigate("runs")
          }}
        />
      ) : page === "reports" ? (
        <ReportWorkspace
          snapshot={reports!}
          comparisonControl={!showComparisons || comparisonState.error || (!comparisonState.data && !comparisonState.pending) ? <Button size="sm" variant="ghost" onClick={() => {
            setShowComparisons(true)
            void resources.comparisons.refresh().catch(() => {})
          }}>{t(comparisonState.error ? "runs:comparisons.retry" : "runs:comparisons.load")}</Button> : comparisonState.pending ? <span role="status" className="text-xs text-muted-foreground">{t("runs:comparisons.loading")}</span> : comparisons?.comparisons.length === 0 ? <span role="status" className="text-xs text-muted-foreground">{t("runs:comparisons.empty")}</span> : comparisons?.next_cursor ? <Button size="sm" variant="ghost" disabled={comparisonAppending} onClick={() => void loadMoreComparisons()}>{t(comparisonAppendError ? "runs:comparisons.retry" : "runs:executions.more")}</Button> : undefined}
          comparisons={showComparisons ? comparisons ?? EMPTY_COMPARISONS : EMPTY_COMPARISONS}
          preferredReportID={preferredReportID}
          reportOpenSequence={reportOpenSequence}
          getDetail={client.getReportDetail}
          exportReport={client.exportReport}
          saveReportExport={client.saveReportExport}
          copyReportPNG={client.copyReportPNG}
          renderRunShortcuts={renderRunShortcuts}
        />
      ) : (
        <RunWorkspace
          historyRevision={reportGeneration}
          client={client}
          snapshot={snapshot ?? EMPTY_WORKSPACE}
          readStatus={pageStatus}
          reports={reports ?? EMPTY_REPORTS}
          preferredJob={preferredJob}
          preferredRunID={preferredRunID}
          commandError={commandError}
          renderRunShortcuts={renderRunShortcuts}
          onJobChange={receiveJobObservation}
          onOpenReport={openReport}
          onOpenComparison={async id => {
            setShowComparisons(true)
            setPendingComparisonIDs(ids => ids.includes(id) ? ids : [...ids, id])
            void resources.comparisons.refresh().then(receiveComparisons).catch(() => setRetryComparisonPoll(true))
            await openReport(id)
          }}
        />
      ))
  }

  return (
    <DesktopShell
      activePage={page}
      onNavigate={navigate}
      actions={
				<div className="flex items-center gap-1">
					<DiagnosticsSheet client={client} />
          <RemoteNodesMenu client={client} disabled={commandPending} />
					<NewComparisonSheet catalog={catalogState.data ?? EMPTY_CATALOG} onOpen={() => resources.catalog.load()} pending={commandPending} onStart={async (command) => { await startComparison(command); navigate("runs") }} />
					<NewRunSheet
						iconOnly
						plans={plans}
						catalog={catalogState.data ?? EMPTY_CATALOG}
            onOpen={() => resources.catalog.load()}
						commandPending={commandPending}
						onStartRun={async (command) => {
							await startRun(command)
							navigate("runs")
						}}
					/>
				</div>
      }
    >
      {DESKTOP_PAGES.map(({ id }) => (
        <RetainedWorkspace key={id} active={page === id}
          fallback={<div role="status" className="flex min-h-0 flex-1 items-center justify-center gap-2 text-xs text-muted-foreground"><Spinner />{t("app:loadingPage")}</div>}>
          {renderWorkspace(id)}
        </RetainedWorkspace>
      ))}
      <ReportGenerationStatus
        progress={Object.values(reportGeneration).filter((progress) =>
          progress.phase !== "ready" || !reports?.reports.some((report) => report.id === progress.report_id),
        )}
        onOpenLogs={() => { void openLogs() }}
      />
    </DesktopShell>
  )
}

function App({ client }: { client?: DesktopClient }) {
  const [clientGeneration, setClientGeneration] = useState(0)
  const desktopClient = useMemo(
    () => {
      void clientGeneration
      return client ?? createDesktopClient()
    },
    [client, clientGeneration],
  )

  const i18n = useMemo(() => {
    const preference = loadLanguagePreference(window.localStorage)
    return createAppI18n(resolveLocale(preference, navigator.languages))
  }, [])

  return (
    <LanguageProvider instance={i18n}>
      <ThemeProvider>
        <TooltipProvider delayDuration={250}>
        <TransientOverlayProvider>
        <AppWorkspace
          client={desktopClient}
          onRecreateClient={() => {
            if (client) return false
            setClientGeneration((generation) => generation + 1)
            return true
          }}
        />
        </TransientOverlayProvider>
        </TooltipProvider>
      </ThemeProvider>
    </LanguageProvider>
  )
}

export default App
