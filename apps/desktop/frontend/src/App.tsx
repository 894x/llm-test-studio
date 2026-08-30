import { useCallback, useEffect, useMemo, useState } from "react"

import {
  createDesktopClient,
  publicDesktopErrorMessage,
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
import { ReportWorkspace } from "@/features/reports/report-workspace"
import type { ReportSnapshot } from "@/features/reports/data"
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
    ])
      .then(([nextWorkspace, nextCatalog, nextReports]) => {
        if (active) {
          setLoadError("")
          setSnapshot(nextWorkspace)
          setCatalog(nextCatalog)
          setReports(nextReports)
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

  const navigate = useCallback((next: DesktopPage) => {
    if (desktopPageFromHash(window.location.hash) === next) {
      setPage(next)
      return
    }
    window.location.hash = next
  }, [])

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
    async (operation: () => Promise<CatalogSnapshot>): Promise<void> => {
      setCatalogMutationPending(true)
      setCatalogMutationError("")
      try {
        setCatalog(await operation())
        try {
          setSnapshot(await client.getWorkspace())
        } catch {
          setCatalogMutationError("目录已保存，但运行计划列表刷新失败，请重新打开应用")
        }
      } catch (error) {
        setCatalogMutationError(publicDesktopErrorMessage(error, "目录操作失败，请检查对象是否仍被引用"))
        throw error
      } finally {
        setCatalogMutationPending(false)
      }
    },
    [client],
  )

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
  if (!snapshot || !catalog || !reports) {
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
        <NewRunSheet
          plans={plans}
          commandPending={commandPending}
          onStartRun={async (planId) => {
            await runCommand(() => client.startRun(planId))
            navigate("runs")
          }}
        />
      }
    >
      {page === "overview" ? (
        <OverviewWorkspace workspace={snapshot} catalog={catalog} reports={reports} />
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
        <ReportWorkspace snapshot={reports} />
      ) : (
        <RunWorkspace
          snapshot={snapshot}
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
