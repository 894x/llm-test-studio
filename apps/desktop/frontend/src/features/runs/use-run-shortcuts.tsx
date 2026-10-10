import { useEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { quickTaskCommand, restoreTaskDraft, type TaskDraft } from "@/features/quick-test/task-draft"
import type { ReportSnapshot } from "@/features/reports/data"
import type { ReportGenerationProgress } from "@/features/reports/generation-progress"
import { isCaseConcurrency, isRunActive, type StartQuickTaskCommand, type WorkspaceSnapshot } from "./data"
import { RunShortcutActions, type RerunCredentialRequest } from "./run-shortcut-actions"

export type RunShortcutRenderer = (runID: string, showCurrentReport?: boolean) => ReactNode

type RerunAttempt = { sourceID: string; runID?: string; waiting: boolean; error?: string }

export function useRunShortcuts({ client, workspace, catalog, reports, generation, draft, commandPending, onStarted, onOpenReport, refreshReports }: {
  client: DesktopClient
  workspace: WorkspaceSnapshot | null
  catalog: CatalogSnapshot | null
  reports: ReportSnapshot | null
  generation: Record<string, ReportGenerationProgress>
  draft: TaskDraft
  commandPending: boolean
  onStarted: (runID: string) => void
  onOpenReport: (reportID: string) => Promise<void>
  refreshReports: () => Promise<ReportSnapshot>
}): RunShortcutRenderer {
  const { t } = useTranslation("runs")
  const [pendingID, setPendingID] = useState("")
  const [attempt, setAttempt] = useState<RerunAttempt | null>(null)
  const [credential, setCredential] = useState<RerunCredentialRequest | null>(null)
  const inFlight = useRef(false)
  const openedRunID = useRef("")
  const newRunStatus = workspace?.runs.find(run => run.id === attempt?.runID)?.status
  const newReport = attempt?.runID ? reports?.reports.find(report => report.source === "run" && report.run_id === attempt.runID) : undefined
  const generationFailed = !!attempt?.runID && generation[attempt.runID]?.phase === "failed"
  const waiting = !!attempt?.waiting && !newReport && !generationFailed

  useEffect(() => {
    if (!attempt?.waiting || !attempt.runID || !newReport || openedRunID.current === attempt.runID) return
    openedRunID.current = attempt.runID
    void onOpenReport(newReport.id)
  }, [attempt, newReport, onOpenReport])

  // Read the report list after terminal status as well as on Core's ready event.
  useEffect(() => {
    if (!waiting || !newRunStatus || isRunActive(newRunStatus)) return
    let active = true
    let timer: number | undefined
    const refresh = async () => {
      try { await refreshReports() } catch { /* The app retains its report read retry state. */ }
      if (active) timer = window.setTimeout(() => void refresh(), 2_000)
    }
    void refresh()
    return () => { active = false; window.clearTimeout(timer) }
  }, [waiting, newRunStatus, refreshReports])

  const started = (sourceID: string, runID: string) => {
    setCredential(null)
    setAttempt({ sourceID, runID, waiting: true })
    onStarted(runID)
  }

  const startQuick = async (sourceID: string, command: StartQuickTaskCommand) => {
    started(sourceID, await client.startQuickTask(command))
  }

  const rerun = async (sourceID: string) => {
    if (inFlight.current || commandPending || waiting) return
    const run = workspace?.runs.find(item => item.id === sourceID)
    if (!run || isRunActive(run.status)) return
    inFlight.current = true
    setPendingID(sourceID)
    setCredential(null)
    setAttempt({ sourceID, waiting: false })
    try {
      if (run.source === "quick_task") {
        const detail = await client.getQuickTask(sourceID)
        const suite = catalog?.suites.find(item => item.id === detail.suite.id && item.protocol === detail.suite.protocol)
        if (!suite) {
          setAttempt({ sourceID, waiting: false, error: t("shortcuts.missingSuite") })
          return
        }
        const restored = restoreTaskDraft({ ...detail, suite }, draft)
        const checked = quickTaskCommand(restored, detail.case_concurrency)
        if (checked.errors.api_key && Object.keys(checked.errors).length === 1) {
          setCredential({ sourceID, draft: { ...restored, api_key: "" }, concurrency: detail.case_concurrency })
          return
        }
        if (!checked.command) {
          setAttempt({ sourceID, waiting: false, error: t("shortcuts.changedInputs") })
          return
        }
        await startQuick(sourceID, checked.command)
      } else {
        if (!workspace?.plans.some(plan => plan.id === run.plan_id)) {
          setAttempt({ sourceID, waiting: false, error: t("shortcuts.missingPlan") })
          return
        }
        const runID = await client.startRunTarget({
          plan_id: run.plan_id, model_id: run.model_id, channel_id: run.channel_id,
          ...(run.load_mode === "single" && isCaseConcurrency(run.concurrency) ? { case_concurrency: run.concurrency } : {}),
        })
        started(sourceID, runID)
      }
    } catch (error) {
      setAttempt({ sourceID, waiting: false, error: publicDesktopErrorMessage(error, t("shortcuts.failed")) })
    } finally {
      inFlight.current = false
      setPendingID("")
    }
  }

  const submitCredential = async (request: RerunCredentialRequest, apiKey: string): Promise<boolean> => {
    if (inFlight.current || commandPending || credential !== request) return false
    const checked = quickTaskCommand({ ...request.draft, api_key: apiKey }, request.concurrency)
    if (!checked.command) {
      setAttempt({ sourceID: request.sourceID, waiting: false, error: t("shortcuts.invalidKey") })
      return false
    }
    inFlight.current = true
    setPendingID(request.sourceID)
    setAttempt({ sourceID: request.sourceID, waiting: false })
    try {
      await startQuick(request.sourceID, checked.command)
      return true
    }
    catch (error) {
      setAttempt({ sourceID: request.sourceID, waiting: false, error: publicDesktopErrorMessage(error, t("shortcuts.failed")) })
      return false
    } finally {
      inFlight.current = false
      setPendingID("")
    }
  }

  return (runID, showCurrentReport = true) => {
    const run = workspace?.runs.find(item => item.id === runID)
    const related = attempt?.sourceID === runID || attempt?.runID === runID
    const report = related && newReport ? newReport : showCurrentReport ? reports?.reports.find(item => item.source === "run" && item.run_id === runID) : undefined
    return <RunShortcutActions
      key={runID}
      disabled={!run || isRunActive(run.status) || commandPending || !!pendingID || waiting}
      pending={pendingID === runID}
      waiting={!!related && waiting}
      error={related ? generationFailed ? t("shortcuts.reportFailed") : attempt?.error : undefined}
      reportID={report?.id}
      newReport={!!related && !!newReport}
      credential={credential?.sourceID === runID ? credential : null}
      onRerun={() => void rerun(runID)}
      onOpenReport={onOpenReport}
      onCredential={submitCredential}
      onCancelCredential={() => setCredential(null)}
    />
  }
}
