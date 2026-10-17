import { RemoteNodesMenu } from "@/features/remote-nodes/remote-nodes-menu"
import { launchAtDefault } from "@/features/jobs/execution"
import type { JobView } from "@/features/jobs/data"
import { localizeStoredMessage } from "@/i18n/runtime"
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react"
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
import type { CatalogSnapshot, SavedQuickTestTarget } from "@/features/catalog/data"
import { createTaskDraft, decodeTaskDraft, encodeTaskDraft, TASK_DRAFT_KEY, type TaskDraft } from "@/features/quick-test/task-draft"
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
  desktopPageFromHash,
  type DesktopPage,
} from "@/features/shell/navigation"

const OverviewWorkspace = lazy(() => import("@/features/overview/overview-workspace").then(module => ({ default: module.OverviewWorkspace })))
const QuickTaskWorkspace = lazy(() => import("@/features/quick-test/quick-task-workspace").then(module => ({ default: module.QuickTaskWorkspace })))
const ReportWorkspace = lazy(() => import("@/features/reports/report-workspace").then(module => ({ default: module.ReportWorkspace })))
const ModelChannelWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.ModelChannelWorkspace })))
const CasesWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.CasesWorkspace })))
const PlansWorkspace = lazy(() => import("@/features/catalog/catalog-workspaces").then(module => ({ default: module.PlansWorkspace })))

const PAGE_READS = {
  overview: ["workspace", "catalog", "reports"],
  "quick-test": ["workspace", "quickCatalog", "reports"],
  catalog: ["connections"], cases: ["definitions"], plans: ["catalog"],
  runs: ["workspace"], reports: ["reports"],
} as const
type ResourceKey = "workspace" | "catalog" | "reports" | "comparisons" | "connections" | "definitions" | "quickCatalog"

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
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation(["app", "runs"])
  const [page, setPage] = useState<DesktopPage>(() =>
    desktopPageFromHash(window.location.hash),
  )
  const pageRef = useRef(page)
  useEffect(() => { pageRef.current = page }, [page])
  const resources = useMemo(() => {
    const catalog = createLazyResource(() => client.getCatalog("all"))
    const scopedCatalog = (scope: CatalogScope) => createLazyResource(() => {
      const full = catalog.getSnapshot()
      return full.data && !full.stale ? Promise.resolve(selectCatalog(full.data, scope)) : client.getCatalog(scope)
    })
    return {
      workspace: createLazyResource(() => client.getWorkspace()),
      catalog, connections: scopedCatalog("connections"), definitions: scopedCatalog("definitions"), quickCatalog: scopedCatalog("quick_test"),
      reports: createLazyResource(() => client.getReports()),
      comparisons: createLazyResource(() => client.getComparisons()),
    }
  }, [client])
  const workspaceState = useResource(resources.workspace)
  const catalogState = useResource(resources.catalog)
  const connectionState = useResource(resources.connections)
  const definitionState = useResource(resources.definitions)
  const quickCatalogState = useResource(resources.quickCatalog)
  const reportState = useResource(resources.reports)
  const comparisonState = useResource(resources.comparisons)
  const snapshot = workspaceState.data
  const catalog = (page === "catalog" ? connectionState : page === "cases" ? definitionState : page === "quick-test" ? quickCatalogState : catalogState).data
  const reports = reportState.data
  const comparisons = comparisonState.data
  const setSnapshot = resources.workspace.set
  const setCatalog = useCallback((next: CatalogSnapshot) => {
    resources.catalog.set(next)
    resources.connections.set(selectCatalog(next, "connections"))
    resources.definitions.set(selectCatalog(next, "definitions"))
    resources.quickCatalog.set(selectCatalog(next, "quick_test"))
  }, [resources])
  const setReports = resources.reports.set
  const setComparisons = resources.comparisons.set
  const [showComparisons, setShowComparisons] = useState(false)
  const requiredReads: readonly ResourceKey[] = PAGE_READS[page]
  const states = { workspace: workspaceState, catalog: catalogState, connections: connectionState, definitions: definitionState, quickCatalog: quickCatalogState, reports: reportState, comparisons: comparisonState }
  const failedRead = requiredReads.find(key => states[key].error !== null && states[key].data === null)
  const loadError = failedRead ? states[failedRead].error : null
  const pageReady = requiredReads.every(key => states[key].data !== null)
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
    const syncPage = () => setPage(desktopPageFromHash(window.location.hash))
    window.addEventListener("hashchange", syncPage)
    return () => window.removeEventListener("hashchange", syncPage)
  }, [])

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
  const workspaceVisible = PAGE_READS[page].some(key => key === "workspace")
  const shouldPollWorkspace = (workspaceVisible || startingRunPending) && !!(retryWorkspacePoll || startingRunPending || snapshot?.runs.some((run) => isRunActive(run.status)) || comparisonRunning || (quickRunID && !snapshot?.runs.some((run) => run.id === quickRunID)))
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
          void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
        }
      } catch {
        if (active && generation === comparisonGeneration.current) setRetryComparisonPoll(true)
      } finally {
        if (active) timer = window.setTimeout(() => void refresh(), 1_000)
      }
    }
    timer = window.setTimeout(() => void refresh(), 1_000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [resources, shouldPollComparisons, receiveComparisons, refreshWorkspace])

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
        reportReadGeneration.current += 1
        const visibleReads: readonly string[] = PAGE_READS[pageRef.current]
        resources.reports.invalidate()
        invalidateWorkspace()
        void Promise.all([
          ...(visibleReads.includes("reports") ? [refreshReports()] : []),
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

  const refreshQuickTask = useCallback(async () => {
    await Promise.all([refreshWorkspace(), refreshReports()])
  }, [refreshWorkspace, refreshReports])

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
    if (pageRef.current === "runs" || visibleReads.includes("reports")) void refreshReports().catch(() => { /* The reports read owns its retry state. */ })
  }, [invalidateWorkspace, resources, receiveComparisons, refreshWorkspace, refreshReports, showComparisons, setPendingComparisonIDs, setRetryComparisonPoll])

  const navigate = useCallback((next: DesktopPage) => {
    if (desktopPageFromHash(window.location.hash) === next) {
      setPage(next)
      return
    }
    window.location.hash = next
  }, [])

  const refreshArchivedPerformanceReport = useCallback(async (reportID: string): Promise<void> => {
    resources.reports.invalidate()
    try {
      await refreshReports()
      setPreferredReportID(reportID)
    } catch {
      // The quick-test result remains available in its sheet; the reports
      // workspace can be refreshed again through normal app polling/reload.
    }
  }, [resources, refreshReports])

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
			void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
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

  const pageStatus = loadError ? (
      <div className="flex min-h-0 flex-1 items-center justify-center bg-background p-6 text-foreground">
        <div role="alert" className="max-w-md border-l-2 border-destructive pl-4">
          <div className="text-sm font-semibold">{tx("desktop:app_unable_to_open_the_local_workspace")}</div>
          <p className="mt-1 text-xs text-muted-foreground">{publicDesktopErrorMessage(loadError, t("app:loadError"))}</p>
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
      <Spinner /> {t("app:loadingPage")}
    </div>
  ) : null

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
      <Suspense fallback={<div role="status" className="flex min-h-0 flex-1 items-center justify-center gap-2 text-xs text-muted-foreground"><Spinner />{t("app:loadingPage")}</div>}>
      {(page === "runs" ? null : pageStatus) ?? (page === "overview" ? (
        <OverviewWorkspace workspace={snapshot!} catalog={catalog!} reports={reports!} onOpenReport={openReport} />
      ) : page === "quick-test" ? (
        <QuickTaskWorkspace
          catalog={catalog!}
          workspace={snapshot!}
          reports={reports!}
          draft={quickDraft}
          onDraftChange={setQuickDraft}
          runID={quickRunID}
          onRunSelected={setQuickRunID}
          actions={client}
          refresh={refreshQuickTask}
          onJobLaunched={setPreferredJob}
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
          }}>{t(comparisonState.error ? "runs:comparisons.retry" : "runs:comparisons.load")}</Button> : comparisonState.pending ? <span role="status" className="text-xs text-muted-foreground">{t("runs:comparisons.loading")}</span> : comparisons?.comparisons.length === 0 ? <span role="status" className="text-xs text-muted-foreground">{t("runs:comparisons.empty")}</span> : undefined}
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
      ))}
      </Suspense>
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
        <AppWorkspace
          client={desktopClient}
          onRecreateClient={() => {
            if (client) return false
            setClientGeneration((generation) => generation + 1)
            return true
          }}
        />
        </TooltipProvider>
      </ThemeProvider>
    </LanguageProvider>
  )
}

export default App
