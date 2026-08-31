import { useCallback, useEffect, useMemo, useState } from "react"

import {
  createDesktopClient,
  publicDesktopErrorMessage,
  publicDesktopOperationErrorMessage,
  type DesktopClient,
} from "@/app/desktop-client"
import { ThemeProvider } from "@/app/theme"
import { TooltipProvider } from "@/components/ui/tooltip"
import {
  CasesWorkspace,
  ModelChannelWorkspace,
  PlansWorkspace,
} from "@/features/catalog/catalog-workspaces"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { OverviewWorkspace } from "@/features/overview/overview-workspace"
import { QuickTestWorkspace } from "@/features/quick-test/quick-test-workspace"
import { ReportWorkspace } from "@/features/reports/report-workspace"
import type { ReportSnapshot } from "@/features/reports/data"
import { NewComparisonSheet } from "@/features/comparisons/comparison-workspace"
import type { ComparisonSnapshot, StartComparisonCommand } from "@/features/comparisons/data"
import { presentWorkspace, type WorkspaceSnapshot } from "@/features/runs/data"
import {
  NewRunSheet,
  RunWorkspace,
} from "@/features/runs/run-workspace"
import {
  DesktopShell,
} from "@/features/shell/desktop-shell"
import {
  desktopPageFromHash,
  type DesktopPage,
} from "@/features/shell/navigation"

function AppWorkspace({ client }: { client: DesktopClient }) {
  const [page, setPage] = useState<DesktopPage>(() =>
    desktopPageFromHash(window.location.hash),
  )
  const [snapshot, setSnapshot] = useState<WorkspaceSnapshot | null>(null)
  const [catalog, setCatalog] = useState<CatalogSnapshot | null>(null)
  const [reports, setReports] = useState<ReportSnapshot | null>(null)
  const [preferredReportID, setPreferredReportID] = useState("")
  const [comparisons, setComparisons] = useState<ComparisonSnapshot | null>(null)
  const [loadError, setLoadError] = useState("")
  const [commandError, setCommandError] = useState("")
  const [commandPending, setCommandPending] = useState(false)
  const [catalogMutationPending, setCatalogMutationPending] = useState(false)
  const [catalogMutationError, setCatalogMutationError] = useState("")

  useEffect(() => {
    const syncPage = () => setPage(desktopPageFromHash(window.location.hash))
    window.addEventListener("hashchange", syncPage)
    return () => window.removeEventListener("hashchange", syncPage)
  }, [])

  useEffect(() => {
    let active = true
    void Promise.all([
      client.getWorkspace(),
      client.getCatalog(),
      client.getReports(),
			client.getComparisons(),
    ])
      .then(([nextWorkspace, nextCatalog, nextReports, nextComparisons]) => {
        if (active) {
          setLoadError("")
          setSnapshot(nextWorkspace)
          setCatalog(nextCatalog)
          setReports(nextReports)
					setComparisons(nextComparisons)
        }
      })
      .catch((error: unknown) => {
        if (active) {
          setLoadError(publicDesktopErrorMessage(error, "无法读取本地工作区"))
        }
      })
    return () => {
      active = false
    }
  }, [client])

  useEffect(() => {
		const activeRun = snapshot?.runs.some((run) => ["queued", "starting", "running", "draining"].includes(run.status))
		const activeComparison = comparisons?.comparisons.some((comparison) => comparison.status === "running")
		if (!activeRun && !activeComparison) return
		let active = true
		const refresh = async () => {
			try {
				const [nextWorkspace, nextReports, nextComparisons] = await Promise.all([
					client.getWorkspace(), client.getReports(), client.getComparisons(),
				])
				if (active) {
					setSnapshot(nextWorkspace)
					setReports(nextReports)
					setComparisons(nextComparisons)
				}
			} catch {
				// Keep the last authoritative snapshot; command errors remain explicit.
			}
		}
		const timer = window.setInterval(() => void refresh(), 1_000)
		return () => { active = false; window.clearInterval(timer) }
	}, [client, snapshot, comparisons])

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

  const openArchivedPerformanceReport = useCallback(async (reportID: string): Promise<void> => {
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
          publicDesktopErrorMessage(error, "桌面操作失败，请检查本地日志"),
        )
        throw error
      } finally {
        setCommandPending(false)
      }
    },
    [],
  )

  const mutateCatalog = useCallback(
    async (operation: () => Promise<CatalogSnapshot>, operationLabel: string): Promise<void> => {
      setCatalogMutationPending(true)
      setCatalogMutationError("")
      try {
        setCatalog(await operation())
        try {
          setSnapshot(await client.getWorkspace())
        } catch {
          setCatalogMutationError(`${operationLabel}已完成，但运行计划列表刷新失败，请重新打开应用`)
        }
      } catch (error) {
        setCatalogMutationError(publicDesktopOperationErrorMessage(error, operationLabel, "目录操作失败，请检查对象是否仍被引用"))
        throw error
      } finally {
        setCatalogMutationPending(false)
      }
    },
    [client],
  )

  const startComparison = useCallback(async (command: StartComparisonCommand): Promise<void> => {
		setCommandPending(true)
		setCommandError("")
		try {
			setComparisons(await client.startComparison(command))
			setSnapshot(await client.getWorkspace())
		} catch (error) {
			setCommandError(publicDesktopErrorMessage(error, "无法启动渠道对比，请检查本地日志"))
			throw error
		} finally {
			setCommandPending(false)
		}
	}, [client])

  const plans = useMemo(
    () => (snapshot ? presentWorkspace(snapshot).plans : []),
    [snapshot],
  )

  if (loadError) {
    return (
      <div className="flex h-svh min-h-[640px] items-center justify-center bg-background p-6 text-foreground">
        <div role="alert" className="max-w-md border-l-2 border-destructive pl-4">
          <div className="text-sm font-semibold">无法打开本地工作台</div>
          <p className="mt-1 text-xs text-muted-foreground">{loadError}</p>
        </div>
      </div>
    )
  }
  if (!snapshot || !catalog || !reports || !comparisons) {
    return (
      <div className="flex h-svh min-h-[640px] items-center justify-center bg-background text-xs text-muted-foreground">
        正在读取本地工作区…
      </div>
    )
  }

  return (
    <DesktopShell
      activePage={page}
      onNavigate={navigate}
      actions={
				<div className="flex items-center gap-2">
					<NewComparisonSheet catalog={catalog} pending={commandPending} onStart={async (command) => { await startComparison(command); navigate("runs") }} />
					<NewRunSheet
						plans={plans}
						commandPending={commandPending}
						onStartRun={async (planId) => {
							await runCommand(() => client.startRun(planId))
							navigate("runs")
						}}
					/>
				</div>
      }
    >
      {page === "overview" ? (
        <OverviewWorkspace workspace={snapshot} catalog={catalog} reports={reports} />
      ) : page === "quick-test" ? (
        <QuickTestWorkspace
          modelCandidates={catalog.models
            .filter((model) => model.protocol === "openai-chat")
            .map((model) => ({ id: model.id, name: model.name }))}
          runQuickTest={client.runQuickTest}
          runQuickPerformanceTest={client.runQuickPerformanceTest}
          saveQuickTestConnection={client.saveQuickTestConnection}
          refreshCatalog={client.getCatalog}
          onCatalogUpdated={setCatalog}
          onOpenCatalog={() => navigate("catalog")}
          onPerformanceArchived={refreshArchivedPerformanceReport}
          onOpenReport={openArchivedPerformanceReport}
        />
      ) : page === "catalog" ? (
        <ModelChannelWorkspace catalog={catalog} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={catalogMutationError} />
      ) : page === "cases" ? (
        <CasesWorkspace catalog={catalog} actions={client} mutate={mutateCatalog} mutationPending={catalogMutationPending} mutationError={catalogMutationError} />
      ) : page === "plans" ? (
        <PlansWorkspace
          catalog={catalog}
          actions={client}
          mutate={mutateCatalog}
          mutationPending={catalogMutationPending}
          mutationError={catalogMutationError}
          commandPending={commandPending}
          onStartPlan={async (planID) => {
            await runCommand(() => client.startRun(planID))
            navigate("runs")
          }}
        />
      ) : page === "reports" ? (
        <ReportWorkspace snapshot={reports} preferredReportID={preferredReportID} getDetail={client.getReportDetail} exportReport={client.exportReport} />
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
  const desktopClient = useMemo(() => client ?? createDesktopClient(), [client])

  return (
    <ThemeProvider>
      <TooltipProvider delayDuration={250}>
        <AppWorkspace client={desktopClient} />
      </TooltipProvider>
    </ThemeProvider>
  )
}

export default App
