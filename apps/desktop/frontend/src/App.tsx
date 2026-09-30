import { localizeStoredMessage } from "@/i18n/runtime"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"

import {
  createDesktopClient,
  isCatalogSavedRefreshFailure,
  publicDesktopErrorMessage,
  publicDesktopOperationErrorMessage,
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
import type { CatalogSnapshot } from "@/features/catalog/data"
import { OverviewWorkspace } from "@/features/overview/overview-workspace"
import { QuickTaskWorkspace } from "@/features/quick-test/quick-task-workspace"
import { createTaskDraft, decodeTaskDraft, encodeTaskDraft, TASK_DRAFT_KEY, type TaskDraft } from "@/features/quick-test/task-draft"
import { ReportWorkspace } from "@/features/reports/report-workspace"
import type { ReportSnapshot } from "@/features/reports/data"
import { NewComparisonSheet } from "@/features/comparisons/comparison-workspace"
import type { ComparisonSnapshot, StartComparisonCommand } from "@/features/comparisons/data"
import { isRunActive, presentWorkspace, type StartRunTargetCommand, type WorkspaceRun, type WorkspaceSnapshot } from "@/features/runs/data"
import {
  NewRunSheet,
  RunWorkspace,
} from "@/features/runs/run-workspace"
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

function recentlyFinished(run: WorkspaceRun): boolean {
  const age = Date.now() - Date.parse(run.updated_at)
  return !isRunActive(run.status) && age >= 0 && age < 60_000
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
  const [preferredReportID, setPreferredReportID] = useState("")
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

  useEffect(() => {
    if (!quickDraft.task && !quickDraft.base_url && !quickDraft.model) return
    try { localStorage.setItem(TASK_DRAFT_KEY, encodeTaskDraft(quickDraft)) }
    catch { /* The in-memory draft remains usable when storage is unavailable. */ }
  }, [quickDraft])

  useEffect(() => {
    const syncPage = () => setPage(desktopPageFromHash(window.location.hash))
    window.addEventListener("hashchange", syncPage)
    return () => window.removeEventListener("hashchange", syncPage)
  }, [])

  useEffect(() => {
    let active = true
    const trackLoad = <T,>(step: InitialLoadStep, promise: Promise<T>): Promise<T> => Promise.resolve(promise).then((value) => {
      if (active) setInitialLoadSteps((completed) => ({ ...completed, [step]: true }))
      return value
    })
    void Promise.all([
      trackLoad("workspace", client.getWorkspace()),
      trackLoad("catalog", client.getCatalog()),
      trackLoad("reports", client.getReports()),
      trackLoad("comparisons", client.getComparisons()),
    ])
      .then(([nextWorkspace, nextCatalog, nextReports, nextComparisons]) => {
        if (active) {
          setLoadError(null)
          setSnapshot(nextWorkspace)
          setCatalog(nextCatalog)
          setReports(nextReports)
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
  }, [client, loadAttempt])

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

  const [retryPoll, setRetryPoll] = useState(false)
  const shouldPoll = !!(retryPoll || startingRunID || snapshot?.runs.some((run) => isRunActive(run.status)) || comparisons?.comparisons.some((comparison) => comparison.status === "running") || (quickRunID && !snapshot?.runs.some((run) => run.id === quickRunID)))
  useEffect(() => {
		if (!shouldPoll) return
		let active = true
		let timer: number | undefined
		const refresh = async () => {
			try {
				const [nextWorkspace, nextReports, nextComparisons] = await Promise.allSettled([
					client.getWorkspace(), client.getReports(), client.getComparisons(),
				])
				if (active) {
					setRetryPoll([nextWorkspace, nextReports, nextComparisons].some((result) => result.status === "rejected"))
					if (nextWorkspace.status === "fulfilled") setSnapshot(nextWorkspace.value)
					if (nextReports.status === "fulfilled") setReports(nextReports.value)
					if (nextComparisons.status === "fulfilled") setComparisons(nextComparisons.value)
				}
			} finally {
				if (active) timer = window.setTimeout(() => void refresh(), 1_000)
			}
		}
		timer = window.setTimeout(() => void refresh(), 1_000)
		return () => { active = false; window.clearTimeout(timer) }
	}, [client, shouldPoll])

  useEffect(() => {
    if (startingRunID && snapshot?.runs.some((run) => run.id === startingRunID)) {
      setStartingRunID("")
    }
  }, [snapshot, startingRunID])

  useEffect(() => {
    if (page !== "reports") return
    let active = true
    void client.getReports().then(
      (nextReports) => { if (active) setReports(nextReports) },
      () => { /* Keep the last successfully loaded report list. */ },
    )
    return () => { active = false }
  }, [client, page])

  const recentRunAwaitingReport = page === "reports" && !!snapshot?.runs.some((run) =>
    recentlyFinished(run) &&
    !reports?.reports.some((report) => report.run_id === run.id),
  )
  useEffect(() => {
    if (!recentRunAwaitingReport) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      try {
        const nextReports = await client.getReports()
        if (active) setReports(nextReports)
      } catch {
        // A later read can recover a transient report generation or read error.
      }
      if (active && snapshot?.runs.some(recentlyFinished)) {
        timer = window.setTimeout(() => void refresh(), 2_000)
      }
    }
    timer = window.setTimeout(() => void refresh(), 2_000)
    return () => { active = false; window.clearTimeout(timer) }
  }, [client, recentRunAwaitingReport, snapshot])

  const refreshQuickTask = useCallback(async () => {
    setSnapshot(await client.getWorkspace())
    setReports(await client.getReports())
  }, [client])

  const navigate = useCallback((next: DesktopPage) => {
    if (desktopPageFromHash(window.location.hash) === next) {
      setPage(next)
      return
    }
    window.location.hash = next
  }, [])

  const refreshArchivedPerformanceReport = useCallback(async (reportID: string): Promise<void> => {
    try {
      setReports(await client.getReports())
      setPreferredReportID(reportID)
    } catch {
      // The quick-test result remains available in its sheet; the reports
      // workspace can be refreshed again through normal app polling/reload.
    }
  }, [client])

  const openReport = useCallback(async (reportID: string): Promise<void> => {
    setPreferredReportID(reportID)
    navigate("reports")
    try {
      setReports(await client.getReports())
    } catch {
      // Keep the reports workspace open with the last authoritative snapshot.
    }
  }, [client, navigate])

  const runCommand = useCallback(
    async (operation: () => Promise<WorkspaceSnapshot>): Promise<void> => {
      setCommandPending(true)
      setCommandError("")
      try {
        setSnapshot(await operation())
      } catch (error) {
        setCommandError(
          publicDesktopErrorMessage(error, t("app:commandError")),
        )
        throw error
      } finally {
        setCommandPending(false)
      }
    },
    [t],
  )

  const startRun = useCallback(async (command: StartRunTargetCommand): Promise<void> => {
    setCommandPending(true)
    setCommandError("")
    try {
      setStartingRunID(await client.startRunTarget(command))
    } catch (error) {
      setCommandError(publicDesktopErrorMessage(error, t("app:commandError")))
      throw error
    } finally {
      setCommandPending(false)
    }
  }, [client, t])

  const mutateCatalog = useCallback(
    async (operation: () => Promise<CatalogSnapshot>, operationLabel: string): Promise<void> => {
      setCatalogMutationPending(true)
      setCatalogMutationError("")
      try {
        setCatalog(await operation())
        try {
          setSnapshot(await client.getWorkspace())
        } catch {
          setCatalogMutationError(t("app:catalogRefreshError", { operation: operationLabel }))
        }
      } catch (error) {
        if (isCatalogSavedRefreshFailure(error)) {
          try {
            setCatalog(await client.getCatalog())
          } catch {
            // Keep the last authoritative catalog; the mutation is already committed.
          }
          try {
            setSnapshot(await client.getWorkspace())
          } catch {
            // Keep the last workspace snapshot and let a later reload recover it.
          }
          setCatalogMutationError(publicDesktopErrorMessage(error, tx("desktop:app_saved_but_the_catalog_could_not_refresh_refresh_or_reopen")))
          return
        }
        setCatalogMutationError(publicDesktopOperationErrorMessage(error, operationLabel, tx("desktop:app_catalog_operation_failed_check_whether_the_item_is_still_referenced")))
        throw error
      } finally {
        setCatalogMutationPending(false)
      }
    },
    [client, t, tx],
  )

  const startComparison = useCallback(async (command: StartComparisonCommand): Promise<void> => {
		setCommandPending(true)
		setCommandError("")
		try {
			setComparisons(await client.startComparison(command))
			setSnapshot(await client.getWorkspace())
		} catch (error) {
			setCommandError(publicDesktopErrorMessage(error, t("app:comparisonError")))
			throw error
		} finally {
			setCommandPending(false)
		}
	}, [client, t])

  const plans = useMemo(
    () => (snapshot ? presentWorkspace(snapshot, {
      locale: i18n.resolvedLanguage ?? i18n.language,
      t: (key, values) => t(`runs:${key}`, values),
    }).plans : []),
    [i18n.language, i18n.resolvedLanguage, snapshot, t],
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
          onWorkspaceUpdated={setSnapshot}
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
          preferredReportID={preferredReportID}
          getDetail={client.getReportDetail}
          exportReport={client.exportReport}
          saveReportExport={client.saveReportExport}
          copyReportPNG={client.copyReportPNG}
        />
      ) : (
        <RunWorkspace
          snapshot={snapshot}
          comparisons={comparisons}
          commandPending={commandPending}
          commandError={commandError}
          onStopSending={(runId) =>
            runCommand(() => client.stopSending(runId))
          }
          onCancelRun={(runId) => runCommand(() => client.cancelRun(runId))}
        />
      )}
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
