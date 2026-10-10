import { localizeStoredMessage } from "@/i18n/runtime"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"

import {
  createDesktopClient,
  isCatalogSavedRefreshFailure,
  DesktopClientError,
  publicDesktopErrorMessage,
  publicDesktopOperationErrorMessage,
  singleFlightRead,
  type DesktopClient,
} from "@/app/desktop-client"
import { ThemeProvider } from "@/app/theme"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { createAppI18n } from "@/i18n/i18n"
import { LanguageProvider } from "@/i18n/language-context"
import { loadLanguagePreference, resolveLocale } from "@/i18n/locale"
import { TooltipProvider } from "@/components/ui/tooltip"
import {
  CasesWorkspace,
  ModelChannelWorkspace,
  PlansWorkspace,
} from "@/features/catalog/catalog-workspaces"
import type { CatalogSnapshot, SavedQuickTestTarget } from "@/features/catalog/data"
import { OverviewWorkspace } from "@/features/overview/overview-workspace"
import { QuickTaskWorkspace } from "@/features/quick-test/quick-task-workspace"
import { createTaskDraft, decodeTaskDraft, encodeTaskDraft, TASK_DRAFT_KEY, type TaskDraft } from "@/features/quick-test/task-draft"
import { ReportWorkspace } from "@/features/reports/report-workspace"
import type { ReportSnapshot } from "@/features/reports/data"
import type { ReportGenerationProgress } from "@/features/reports/generation-progress"
import { ReportGenerationStatus } from "@/features/reports/report-generation-status"
import { NewComparisonSheet } from "@/features/comparisons/comparison-workspace"
import type { ComparisonSnapshot, StartComparisonCommand } from "@/features/comparisons/data"
import { isRunActive, presentPlans, type StartRunTargetCommand, type WorkspaceSnapshot } from "@/features/runs/data"
import {
  NewRunSheet,
  RunWorkspace,
} from "@/features/runs/run-workspace"
import { useRunShortcuts } from "@/features/runs/use-run-shortcuts"
import {
  DesktopShell,
} from "@/features/shell/desktop-shell"
import { DiagnosticsSheet } from "@/features/shell/diagnostics-sheet"
import {
  desktopPageFromHash,
  type DesktopPage,
} from "@/features/shell/navigation"

const INITIAL_LOAD_STEPS = ["workspace", "catalog", "reports", "comparisons"] as const
type InitialLoadStep = typeof INITIAL_LOAD_STEPS[number]

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
  onRecreateClient: () => void
}) {
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation(["app", "runs"])
  const [page, setPage] = useState<DesktopPage>(() =>
    desktopPageFromHash(window.location.hash),
  )
  const [snapshot, setSnapshot] = useState<WorkspaceSnapshot | null>(null)
  const [catalog, setCatalog] = useState<CatalogSnapshot | null>(null)
  const [reports, setReports] = useState<ReportSnapshot | null>(null)
  const [reportGeneration, setReportGeneration] = useState<Record<string, ReportGenerationProgress>>({})
  const reportReadGeneration = useRef(0)
  const [preferredReportID, setPreferredReportID] = useState("")
  const [reportOpenSequence, setReportOpenSequence] = useState(0)
  const [preferredRunID, setPreferredRunID] = useState("")
  const [comparisons, setComparisons] = useState<ComparisonSnapshot | null>(null)
  const [loadError, setLoadError] = useState<unknown>(null)
  const [openingLogs, setOpeningLogs] = useState(false)
  const [openLogsError, setOpenLogsError] = useState<unknown>(null)
  const [commandError, setCommandError] = useState("")
  const [commandPending, setCommandPending] = useState(false)
  const [catalogMutationPending, setCatalogMutationPending] = useState(false)
  const [catalogMutationError, setCatalogMutationError] = useState("")
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [initialLoadSteps, setInitialLoadSteps] = useState<Record<InitialLoadStep, boolean>>({
    workspace: false,
    catalog: false,
    reports: false,
    comparisons: false,
  })
  const [quickRunID, setQuickRunID] = useState("")
  const [startingRunID, setStartingRunID] = useState("")
  const [quickDraft, setQuickDraft] = useState<TaskDraft>(() => {
    try { return decodeTaskDraft(localStorage.getItem(TASK_DRAFT_KEY)) ?? createTaskDraft(null) }
    catch { return createTaskDraft(null) }
  })
  const reads = useMemo(() => ({
    getWorkspace: singleFlightRead(() => client.getWorkspace()),
    getCatalog: singleFlightRead(() => client.getCatalog()),
    getReports: singleFlightRead(() => client.getReports()),
    getComparisons: singleFlightRead(() => client.getComparisons()),
  }), [client])
  const latestDraft = useRef(quickDraft)
  const workspaceGeneration = useRef(0)
  const comparisonGeneration = useRef(0)
  const catalogGeneration = useRef(0)
  const invalidateWorkspace = useCallback(() => {
    workspaceGeneration.current += 1
    reads.getWorkspace.invalidate()
  }, [reads])
  const refreshWorkspace = useCallback(async () => {
    const generation = workspaceGeneration.current
    const nextWorkspace = await reads.getWorkspace()
    if (generation === workspaceGeneration.current) setSnapshot(nextWorkspace)
  }, [reads])
  const updateWorkspace = useCallback((nextWorkspace: WorkspaceSnapshot) => {
    invalidateWorkspace()
    setSnapshot(nextWorkspace)
  }, [invalidateWorkspace])

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
    let active = true
    const reportGenerationAtRead = reportReadGeneration.current
    const trackLoad = <T,>(step: InitialLoadStep, promise: Promise<T>): Promise<T> => Promise.resolve(promise).then((value) => {
      if (active) setInitialLoadSteps((completed) => ({ ...completed, [step]: true }))
      return value
    })
    void Promise.all([
      trackLoad("workspace", reads.getWorkspace()),
      trackLoad("catalog", reads.getCatalog()),
      trackLoad("reports", reads.getReports()),
      trackLoad("comparisons", reads.getComparisons()),
    ])
      .then(([nextWorkspace, nextCatalog, nextReports, nextComparisons]) => {
        if (active) {
          setLoadError(null)
          setSnapshot(nextWorkspace)
          setCatalog(nextCatalog)
          if (reportGenerationAtRead === reportReadGeneration.current) setReports(nextReports)
					setComparisons(nextComparisons)
        }
      })
      .catch((error: unknown) => {
        if (active) {
          setLoadError(error)
        }
      })
    return () => {
      active = false
    }
  }, [reads, loadAttempt])

  const retryInitialLoad = useCallback(() => {
    setLoadError("")
    setOpenLogsError(null)
    setInitialLoadSteps({ workspace: false, catalog: false, reports: false, comparisons: false })
    onRecreateClient()
    setLoadAttempt((attempt) => attempt + 1)
  }, [onRecreateClient])

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
  const shouldPollWorkspace = !!(retryWorkspacePoll || startingRunID || snapshot?.runs.some((run) => isRunActive(run.status)) || comparisonRunning || (quickRunID && !snapshot?.runs.some((run) => run.id === quickRunID)))
  useEffect(() => {
		if (!shouldPollWorkspace) return
		let active = true
		let timer: number | undefined
		const refresh = async () => {
			const generation = workspaceGeneration.current
			try {
				const nextWorkspace = await reads.getWorkspace()
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
		return () => { active = false; window.clearTimeout(timer) }
	}, [reads, shouldPollWorkspace])

  const shouldPollComparisons = comparisonRunning || retryComparisonPoll
  useEffect(() => {
    if (!shouldPollComparisons) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      const generation = comparisonGeneration.current
      try {
        const nextComparisons = await reads.getComparisons()
        if (active && generation === comparisonGeneration.current) {
          setRetryComparisonPoll(false)
          setComparisons(nextComparisons)
        }
      } catch {
        if (active && generation === comparisonGeneration.current) setRetryComparisonPoll(true)
      } finally {
        if (active) timer = window.setTimeout(() => void refresh(), 1_000)
      }
    }
    timer = window.setTimeout(() => void refresh(), 1_000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [reads, shouldPollComparisons])

  useEffect(() => {
    if (startingRunID && snapshot?.runs.some((run) => run.id === startingRunID)) {
      setStartingRunID("")
    }
  }, [snapshot, startingRunID])

  const [reportRetryUntil, setReportRetryUntil] = useState(0)
  const updateReports = useCallback((nextReports: ReportSnapshot) => {
    setReports(nextReports)
    setReportRetryUntil(0)
  }, [])
  const retryReports = useCallback(() => {
    setReportRetryUntil((until) => until || Date.now() + 60_000)
  }, [])
  const refreshReports = useCallback(async () => {
    const generation = reportReadGeneration.current
    try {
      const nextReports = await reads.getReports()
      if (generation === reportReadGeneration.current) updateReports(nextReports)
      return nextReports
    } catch (error) {
      if (generation === reportReadGeneration.current) retryReports()
      throw error
    }
  }, [reads, updateReports, retryReports])

  useEffect(() => {
    let active = true
    const latest = new Map<string, number>()
    const receive = (progress: ReportGenerationProgress) => {
      if (!active || progress.sequence <= (latest.get(progress.run_id) ?? 0)) return
      latest.set(progress.run_id, progress.sequence)
      setReportGeneration((previous) => ({ ...previous, [progress.run_id]: progress }))
      if (progress.phase === "ready") {
        reportReadGeneration.current += 1
        reads.getReports.invalidate()
        void Promise.all([refreshReports(), refreshWorkspace()]).catch(() => {
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
  }, [client, reads, refreshReports, refreshWorkspace, t])

  useEffect(() => {
    if (page === "reports") void refreshReports().catch(() => {})
  }, [refreshReports, page])

  useEffect(() => {
    if (!reportRetryUntil) return
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
  }, [refreshReports, reportRetryUntil])

  const refreshQuickTask = useCallback(async () => {
    await Promise.all([refreshWorkspace(), refreshReports()])
  }, [refreshWorkspace, refreshReports])

  const navigate = useCallback((next: DesktopPage) => {
    if (desktopPageFromHash(window.location.hash) === next) {
      setPage(next)
      return
    }
    window.location.hash = next
  }, [])

  const refreshArchivedPerformanceReport = useCallback(async (reportID: string): Promise<void> => {
    reads.getReports.invalidate()
    try {
      await refreshReports()
      setPreferredReportID(reportID)
    } catch {
      // The quick-test result remains available in its sheet; the reports
      // workspace can be refreshed again through normal app polling/reload.
    }
  }, [reads, refreshReports])

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
    client, workspace: snapshot, catalog, reports, generation: reportGeneration,
    draft: quickDraft, commandPending, onStarted: rerunStarted,
    onOpenReport: openReport, refreshReports,
  })

  const runCommand = useCallback(
    async (operation: () => Promise<WorkspaceSnapshot>): Promise<void> => {
      setCommandPending(true)
      setCommandError("")
      try {
        const nextWorkspace = await operation()
        invalidateWorkspace()
        setSnapshot(nextWorkspace)
      } catch (error) {
        setCommandError(
          publicDesktopErrorMessage(error, t("app:commandError")),
        )
        throw error
      } finally {
        setCommandPending(false)
      }
    },
    [invalidateWorkspace, t],
  )

  const startRun = useCallback(async (command: StartRunTargetCommand): Promise<void> => {
    setCommandPending(true)
    setCommandError("")
    try {
      const runID = await client.startRunTarget(command)
      invalidateWorkspace()
      setStartingRunID(runID)
    } catch (error) {
      setCommandError(publicDesktopErrorMessage(error, t("app:commandError")))
      throw error
    } finally {
      setCommandPending(false)
    }
  }, [client, invalidateWorkspace, t])

  const mutateCatalog = useCallback(
    async (operation: () => Promise<CatalogSnapshot>, operationLabel: string): Promise<void> => {
      setCatalogMutationPending(true)
      setCatalogMutationError("")
      try {
        const nextCatalog = await operation()
        catalogGeneration.current += 1
        setCatalog(nextCatalog)
        reads.getCatalog.invalidate()
        invalidateWorkspace()
        void refreshWorkspace().catch(() => {
          setCatalogMutationError(t("app:catalogRefreshError", { operation: operationLabel }))
        })
      } catch (error) {
        if (isCatalogSavedRefreshFailure(error)) {
          const generation = ++catalogGeneration.current
          reads.getCatalog.invalidate()
          invalidateWorkspace()
          void reads.getCatalog().then((nextCatalog) => {
            if (generation === catalogGeneration.current) setCatalog(nextCatalog)
          }, () => { /* The mutation is already committed. */ })
          void refreshWorkspace().catch(() => { /* A later reload can recover the workspace. */ })
          setCatalogMutationError(publicDesktopErrorMessage(error, tx("desktop:app_saved_but_the_catalog_could_not_refresh_refresh_or_reopen")))
          return
        }
        setCatalogMutationError(publicDesktopOperationErrorMessage(error, operationLabel, tx("desktop:app_catalog_operation_failed_check_whether_the_item_is_still_referenced")))
        throw error
      } finally {
        setCatalogMutationPending(false)
      }
    },
    [reads, invalidateWorkspace, refreshWorkspace, t, tx],
  )

  const startComparison = useCallback(async (command: StartComparisonCommand): Promise<void> => {
		setCommandPending(true)
		setCommandError("")
		try {
			const nextComparisons = await client.startComparison(command)
			comparisonGeneration.current += 1
			reads.getComparisons.invalidate()
			setComparisons(nextComparisons)
			invalidateWorkspace()
			void refreshWorkspace().catch(() => setRetryWorkspacePoll(true))
		} catch (error) {
			setCommandError(publicDesktopErrorMessage(error, t("app:comparisonError")))
			throw error
		} finally {
			setCommandPending(false)
		}
	}, [client, reads, invalidateWorkspace, refreshWorkspace, t])

  const workspacePlans = snapshot?.plans
  const plans = useMemo(
    () => (workspacePlans ? presentPlans(workspacePlans, {
      locale: i18n.resolvedLanguage ?? i18n.language,
      t: (key, values) => t(`runs:${key}`, values),
    }) : []),
    [i18n.language, i18n.resolvedLanguage, workspacePlans, t],
  )

  if (loadError) {
    return (
      <div className="flex h-svh min-h-[640px] items-center justify-center bg-background p-6 text-foreground">
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
    )
  }
  if (!snapshot || !catalog || !reports || !comparisons) {
    const loadTotal = 4
    const loadCompleted = INITIAL_LOAD_STEPS.filter((step) => initialLoadSteps[step]).length
    const loadPercent = Math.round((loadCompleted / loadTotal) * 100)
    return (
      <div className="flex h-svh min-h-[640px] items-center justify-center bg-background p-6 text-foreground">
        <div
          role="status"
          aria-live="polite"
          aria-busy="true"
          className="flex w-full max-w-sm flex-col items-center text-center"
        >
          <Spinner className="size-8 text-primary" />
          <h1 className="mt-4 text-sm font-semibold">{t("app:loadingTitle")}</h1>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("app:loadingProgress", { completed: loadCompleted, total: loadTotal })}
          </p>
          <Progress
            value={loadPercent}
            aria-label={t("app:loadingProgress", { completed: loadCompleted, total: loadTotal })}
            className="mt-4"
          />
          <ul className="mt-4 w-full space-y-2 text-left text-xs text-muted-foreground">
            {INITIAL_LOAD_STEPS.map((step) => (
              <li key={step} className={initialLoadSteps[step] ? "text-foreground" : undefined}>
                <span aria-hidden="true" className="mr-2 inline-block w-3 text-center">
                  {initialLoadSteps[step] ? "✓" : "·"}
                </span>
                {t(`app:loadingSteps.${step}`)}
              </li>
            ))}
          </ul>
        </div>
      </div>
    )
  }

  return (
    <DesktopShell
      activePage={page}
      onNavigate={navigate}
      actions={
				<div className="flex items-center gap-2">
					<DiagnosticsSheet client={client} />
					<NewComparisonSheet catalog={catalog} pending={commandPending} onStart={async (command) => { await startComparison(command); navigate("runs") }} />
					<NewRunSheet
						plans={plans}
						catalog={catalog}
						commandPending={commandPending}
						onStartRun={async (command) => {
							await startRun(command)
							navigate("runs")
						}}
					/>
				</div>
      }
    >
      {page === "overview" ? (
        <OverviewWorkspace workspace={snapshot} catalog={catalog} reports={reports} onOpenReport={openReport} />
      ) : page === "quick-test" ? (
        <QuickTaskWorkspace
          catalog={catalog}
          workspace={snapshot}
          reports={reports}
          draft={quickDraft}
          onDraftChange={setQuickDraft}
          runID={quickRunID}
          onRunSelected={setQuickRunID}
          actions={client}
          refresh={refreshQuickTask}
          onWorkspaceUpdated={updateWorkspace}
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
        <ModelChannelWorkspace catalog={catalog} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={localizeStoredMessage(catalogMutationError, tx)} />
      ) : page === "cases" ? (
        <CasesWorkspace catalog={catalog} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={localizeStoredMessage(catalogMutationError, tx)} />
      ) : page === "plans" ? (
        <PlansWorkspace
          catalog={catalog}
          actions={client}
          mutate={mutateCatalog}
          mutationPending={catalogMutationPending}
          mutationError={localizeStoredMessage(catalogMutationError, tx)}
          commandPending={commandPending}
          onRunPerformance={client.runQuickPerformanceTest}
          onStartPlan={async (command) => {
            await startRun(command)
            navigate("runs")
          }}
        />
      ) : page === "reports" ? (
        <ReportWorkspace
          snapshot={reports}
          comparisons={comparisons}
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
          snapshot={snapshot}
          comparisons={comparisons}
          commandPending={commandPending}
          commandError={commandError}
          preferredRunID={preferredRunID}
          renderRunShortcuts={renderRunShortcuts}
          onStopSending={(runId) =>
            runCommand(() => client.stopSending(runId))
          }
          onCancelRun={(runId) => runCommand(() => client.cancelRun(runId))}
        />
      )}
      <ReportGenerationStatus
        progress={Object.values(reportGeneration).filter((progress) =>
          progress.phase !== "ready" || !reports.reports.some((report) => report.id === progress.report_id),
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
          onRecreateClient={() => setClientGeneration((generation) => generation + 1)}
        />
        </TooltipProvider>
      </ThemeProvider>
    </LanguageProvider>
  )
}

export default App
