import { performanceProfilesEqual } from "./performance-profile"
import { isQuickPerformanceProtocol, type QuickPerformanceCommand, type QuickPerformanceProfile, type QuickPerformanceReport } from "./data"
import {
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
  type Dispatch,
  type SetStateAction,
} from "react"
import { useTranslation } from "react-i18next"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"
import SaveIcon from "lucide-react/dist/esm/icons/save.mjs"
import { publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import { Alert, AlertDescription } from "@/components/ui/alert"
import {
  Autocomplete,
  AutocompleteContent,
  AutocompleteEmpty,
  AutocompleteInput,
  AutocompleteItem,
  AutocompleteList,
} from "@/components/ui/autocomplete"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { CatalogProtocol, CatalogSnapshot, CatalogSuite, SaveQuickTestTargetCommand, SavedQuickTestTarget } from "@/features/catalog/data"
import { DEFAULT_CASE_CONCURRENCY, isRunActive, type WorkspaceSnapshot } from "@/features/runs/data"
import { CaseConcurrencyField } from "@/features/runs/case-concurrency-field"
import type { ReportSnapshot } from "@/features/reports/data"
import { QuickPerformancePanel, type QuickPerformanceConnection } from "./quick-performance-sheet"
import { createTaskDraft, quickTaskCommand, restoreTaskDraft, type TaskDraft } from "./task-draft"
import { cn } from "@/lib/utils"
import { PROTOCOLS } from "@/features/catalog/protocols"
import { resolveConnectionURL, taskConnectionURLs } from "./connection-url"
import { ConnectionURLInput } from "./connection-url-input"
import { protocolOptions } from "@/features/catalog/protocols"
import { SaveChannelSheet } from "./save-channel-sheet"
import { QuickTestModeSwitch } from "./quick-test-mode-switch"
import { QuickTestHistory, type QuickTestHistoryItem } from "./quick-test-history"
import { ModelIcon, ProtocolIcon } from "./model-icon"

type Actions = Pick<
  DesktopClient,
  | "startQuickTask"
  | "getQuickTask"
  | "getReportDetail"
  | "rememberQuickTaskCredential"
  | "forgetQuickTaskCredential"
  | "cancelRun"
  | "runQuickPerformanceTest"
>

type PerformanceHistorySettings = Pick<QuickPerformanceReport,
  "protocol" | "address_mode" | "base_url" | "endpoint" | "model_id" | "profile"
> & { channel_id?: string; credential_run_id?: string; suite_id?: string }

// Omit connection details and the temporary key from the in-memory parameter snapshot.
function performanceProfile({
  protocol: _protocol, address_mode: _addressMode, url: _url, api_key: _apiKey,
  channel_id: _channelID, credential_run_id: _credentialRunID, model_id: _modelID, task: _task,
  ...profile
}: QuickPerformanceCommand): QuickPerformanceProfile {
  return profile
}

export function QuickTaskWorkspace({
  catalog,
  workspace,
  reports,
  draft,
  onDraftChange,
  runID,
  onRunSelected,
  actions,
  refresh,
  onWorkspaceUpdated,
  onOpenReport,
  onPerformanceArchived,
  onSaveChannel,
}: {
  catalog: CatalogSnapshot
  workspace: WorkspaceSnapshot
  reports: ReportSnapshot
  draft: TaskDraft
  onDraftChange: Dispatch<SetStateAction<TaskDraft>>
  runID: string
  onRunSelected: (id: string) => void
  actions: Actions
  refresh: () => Promise<void>
  onWorkspaceUpdated: (snapshot: WorkspaceSnapshot) => void
  onOpenReport: (id: string) => void | Promise<void>
  onPerformanceArchived: (id: string) => void | Promise<void>
  onSaveChannel: (command: SaveQuickTestTargetCommand) => Promise<SavedQuickTestTarget>
}) {
  const { t } = useTranslation("quickTest")
  const [pending, setPending] = useState<
    "start" | "history" | "cancel" | "remember" | "forget" | "save" | null
  >(null)
  const inFlight = useRef(false)
  const saveChannelTrigger = useRef<HTMLButtonElement>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState("")
  const [caseConcurrency, setCaseConcurrency] = useState(DEFAULT_CASE_CONCURRENCY)
  const [refreshError, setRefreshError] = useState(false)
  const [mode, setMode] = useState<"functional" | "performance">("functional")
  const [performancePending, setPerformancePending] = useState(false)
  const [performanceHistory, setPerformanceHistory] = useState<QuickTestHistoryItem[]>([])
  const [performanceSettings, setPerformanceSettings] = useState<Record<string, PerformanceHistorySettings>>({})
  const [restoredPerformanceProfile, setRestoredPerformanceProfile] = useState<QuickPerformanceProfile>()
  const [selectedPerformancePlanID, setSelectedPerformancePlanID] = useState("")
  const [performanceFormRevision, setPerformanceFormRevision] = useState(0)
  const [performanceActionsContainer, setPerformanceActionsContainer] = useState<HTMLDivElement | null>(null)
  const [selectedHistoryID, setSelectedHistoryID] = useState("")
  const busy = !!pending || performancePending
  const [saveChannelOpen, setSaveChannelOpen] = useState(false)
  const [selectedProtocol, setSelectedProtocol] = useState<CatalogProtocol>(draft.task?.protocol ?? "openai-chat")
  const protocol = mode === "performance" ? selectedProtocol : draft.task?.protocol ?? selectedProtocol
  const performancePlans = catalog.plans.filter(plan => plan.performance && plan.protocol === protocol)
  const currentTasks = catalog.suites.filter(item => item.protocol === protocol)
  const task = currentTasks.find((item) => item.id === draft.task?.id) ?? draft.task ?? currentTasks[0]
  const form =
    draft.task || !task
      ? draft
      : {
          ...createTaskDraft(task),
          base_url: draft.base_url,
          api_key: draft.api_key,
          model: draft.model,
        }
  const tasks =
    task && !currentTasks.some((item) => taskKey(item) === taskKey(task))
      ? [task, ...currentTasks]
      : currentTasks
  const channels = catalog.channels.filter(
    (channel) =>
      channel.enabled && channel.credential_configured,
  )
  const channel = channels.find((item) => item.id === form.channel_id)
  const missingChannel = !!form.channel_id && !channel
  const performanceDescriptor = PROTOCOLS.find(item => item.id === protocol)!
  const performanceURL = resolveConnectionURL(channel?.base_url ?? form.base_url, performanceDescriptor.request_paths[0].path, performanceDescriptor.request_paths)
  const connectionURLs = mode === "performance" ? (performanceURL ? [performanceURL] : []) : taskConnectionURLs(channel?.base_url ?? form.base_url, task, catalog)
  const models = [
    ...new Set([
      ...catalog.channel_models
        .filter((mapping) => mapping.channel_id === form.channel_id && mapping.protocols.includes(protocol))
        .map((mapping) => mapping.upstream_model_name),
      ...catalog.models
        .filter((model) => !form.channel_id && model.protocols.includes(protocol))
        .map((model) => model.name),
    ]),
  ]
  const performanceConnection: QuickPerformanceConnection = {
    protocol,
    address_mode: "base_url",
    url: channel?.base_url ?? form.base_url.trim(),
    api_key: form.channel_id || form.credential_run_id ? "" : form.api_key.trim(),
    ...(form.credential_run_id ? { credential_run_id: form.credential_run_id } : {}),
    model_id: form.model.trim(),
    ...(form.channel_id ? { channel_id: form.channel_id } : {}),
  }
  const history: QuickTestHistoryItem[] = [
    ...workspace.runs.filter(run => run.source === "quick_task").map(run => ({
      id: run.id, runID: run.id, mode: "functional" as const,
      title: run.plan_name, model: run.model_name, channel: run.channel_name,
      timestamp: run.started_at,
      status: run.conclusion === "none" ? (run.status === "starting" ? "queued" : run.status) : run.conclusion,
      active: isRunActive(run.status), completed: run.completed, planned: run.planned,
      detail: t("task.observed", run),
      error: run.error_code ? t(`common:errorCode.${run.error_code}`, { defaultValue: run.error_code }) : undefined,
      reportID: reports.reports.find(report => report.run_id === run.id)?.id,
    })),
    ...reports.reports.filter(report => report.source === "quick_performance" && !performanceHistory.some(item => item.reportID === report.id)).map(report => ({
      id: report.id, reportID: report.id, mode: "performance" as const,
      title: report.plan_name, model: report.model_name, channel: report.channel_name,
      timestamp: report.generated_at, active: false,
      status: report.run_status === "cancelled" ? "cancelled" : report.passed ? "passed" : "failed",
    })),
    ...performanceHistory,
  ].sort((a, b) => Date.parse(b.timestamp) - Date.parse(a.timestamp))
  const visibleHistory = history.filter((item, index) => item.active || index < 50)
  const selectedID = selectedHistoryID || runID
  const runPerformance: Actions["runQuickPerformanceTest"] = async (command, onProgress) => {
    const id = crypto.randomUUID()
    const item: QuickTestHistoryItem = {
      id, mode: "performance", title: catalog.plans.find(plan => plan.id === selectedPerformancePlanID)?.name ?? t("performance.title"),
      model: command.model_id, channel: channel?.name ?? command.url,
      timestamp: new Date().toISOString(), active: true, status: "queued", phase: "not_started",
    }
    setPerformanceSettings(current => ({ ...current, [id]: {
      protocol: command.protocol, address_mode: command.address_mode,
      base_url: command.url, endpoint: command.url, model_id: command.model_id,
      profile: performanceProfile(command), channel_id: command.channel_id,
      credential_run_id: command.credential_run_id, suite_id: command.task?.suite_id,
    } }))
    setPerformancePending(true)
    setSelectedHistoryID(id)
    setPerformanceHistory(current => [item, ...current])
    const updateItem = (patch: Partial<QuickTestHistoryItem>) => setPerformanceHistory(current => current.map(entry => entry.id === id ? { ...entry, ...patch } : entry))
    try {
      const report = await actions.runQuickPerformanceTest(command, progress => {
        updateItem({
          phase: progress.phase, completed: progress.completed,
          planned: command.request_count > 0 || progress.phase === "warming_up" || progress.phase === "ramping" ? progress.planned : undefined,
          detail: t("workspace.performanceCounts", { ...progress }),
        })
        onProgress?.(progress)
      })
      updateItem({ active: false, reportID: report.report_id, phase: undefined, status: report.progress.phase === "cancelled" ? "cancelled" : report.success ? "passed" : "failed" })
      return report
    } catch (reason) {
      updateItem({ active: false, phase: undefined, status: "failed", error: publicDesktopErrorMessage(reason, t("performance.error")) })
      throw reason
    } finally {
      setPerformancePending(false)
    }
  }

  const update = (next: TaskDraft) => {
    onDraftChange(next)
    setErrors({})
    setError("")
  }
  const changeTask = (value: string) => {
    const selected = tasks.find((item) => taskKey(item) === value)
    if (!selected) return
    const next = createTaskDraft(selected)
    if (selected.protocol === task?.protocol)
      Object.assign(next, {
        base_url: form.base_url,
        api_key: form.api_key,
        channel_id: form.channel_id,
        credential_run_id: form.credential_run_id,
        model: form.model,
      })
    setSelectedProtocol(selected.protocol)
    update(next)
  }
  const changeProtocol = (value: string) => {
    const nextProtocol = value as CatalogProtocol
    setSelectedProtocol(nextProtocol)
    if (mode === "performance") {
      setSelectedPerformancePlanID(""); setRestoredPerformanceProfile(undefined)
      setPerformanceFormRevision(current => current + 1)
      setErrors({}); setError("")
      return
    }
    const next = createTaskDraft(catalog.suites.find(item => item.protocol === nextProtocol) ?? null)
    update({ ...next, base_url: form.base_url, channel_id: form.channel_id, api_key: form.api_key, model: form.model })
  }
  const refreshProgress = async () => {
    try {
      await refresh()
      setRefreshError(false)
    } catch {
      setRefreshError(true)
    }
  }
  const validCommand = () => {
    const checked = quickTaskCommand(
      { ...form, task: currentTasks.find((item) => item.id === task?.id) ?? null },
      caseConcurrency,
    )
    if (missingChannel) checked.errors.channel_id = t("task.missingChannel")
    const configuredMappings = catalog.channel_models.filter(mapping => mapping.channel_id === form.channel_id && mapping.upstream_model_name === form.model.trim())
    if (configuredMappings.length > 0 && !configuredMappings.some(mapping => mapping.protocols.includes(protocol))) checked.errors.model = t("task.unsupportedModelProtocol")
    setErrors(checked.errors)
    if (Object.keys(checked.errors).length) {
      const field = Object.keys(checked.errors)[0]
      const control = document.getElementById(`quick-task-${field}`)
      if (control instanceof HTMLElement) control.focus()
      return undefined
    }
    return checked.command
  }
  const validPerformanceConnection = () => {
    const connectionFields = ["base_url", "api_key", "model", "channel_id"]
    const nextErrors = Object.fromEntries(Object.entries(quickTaskCommand(form).errors).filter(([field]) => connectionFields.includes(field)))
    if (missingChannel) nextErrors.channel_id = t("task.missingChannel")
    const mappings = catalog.channel_models.filter(mapping => mapping.channel_id === form.channel_id && mapping.upstream_model_name === form.model.trim())
    if (mappings.length && !mappings.some(mapping => mapping.protocols.includes(protocol))) nextErrors.model = t("task.unsupportedModelProtocol")
    setErrors(nextErrors)
    const first = Object.keys(nextErrors)[0]
    if (first) { document.getElementById(`quick-task-${first}`)?.focus(); return undefined }
    return performanceConnection
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (inFlight.current) return
    const command = validCommand()
    if (!command) return
    inFlight.current = true
    setPending("start")
    setError("")
    setRefreshError(false)
    onDraftChange(form)
    try {
      const id = await actions.startQuickTask(command)
      onRunSelected(id)
      setSelectedHistoryID(id)
      onDraftChange((current) => (current === form ? { ...form, source_run_id: id } : current))
      await refreshProgress()
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.failure")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const restore = async (id: string) => {
    if (inFlight.current) return
    inFlight.current = true
    setPending("history")
    setError("")
    try {
      const detail = await actions.getQuickTask(id)
      update(restoreTaskDraft(detail, form))
      setCaseConcurrency(detail.case_concurrency)
      onRunSelected(id)
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.failure")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const restoreHistory = async (item: QuickTestHistoryItem) => {
    if (inFlight.current || performancePending) return
    if (item.runID) {
      setMode("functional")
      setSelectedHistoryID(item.id)
      await restore(item.runID)
      return
    }
    if (item.active) return
    inFlight.current = true
    setPending("history")
    setError("")
    try {
      let settings = performanceSettings[item.id]
      if (!settings) {
        if (!item.reportID) throw new Error(t("task.invalidHistory"))
        const detail = await actions.getReportDetail(item.reportID)
        if (detail.source !== "quick_performance" || detail.performance.report_id !== item.reportID)
          throw new Error(t("task.invalidHistory"))
        settings = detail.performance
      }
      const suite = settings.suite_id
        ? catalog.suites.find(entry => entry.id === settings.suite_id && entry.protocol === settings.protocol)
        : catalog.suites.find(entry => entry.id === form.task?.id && entry.protocol === settings.protocol)
          ?? catalog.suites.find(entry => entry.protocol === settings.protocol)
      const url = settings.address_mode === "full_url" ? settings.endpoint : settings.base_url
      const sameConnection = protocol === settings.protocol && performanceConnection.url === url
      const channelID = settings.channel_id ?? (sameConnection ? form.channel_id : "")
      const credentialRunID = settings.credential_run_id ?? (sameConnection ? form.credential_run_id : undefined)
      update({
        ...createTaskDraft(suite ?? null), model: settings.model_id, base_url: url,
        channel_id: channelID, credential_run_id: credentialRunID,
        api_key: sameConnection && !channelID && !credentialRunID ? form.api_key : "",
      })
      setSelectedProtocol(settings.protocol)
      const matchingPlan = catalog.plans.find(plan => plan.protocol === settings.protocol && plan.performance && performanceProfilesEqual(plan.performance, settings.profile))
      setSelectedPerformancePlanID(matchingPlan?.id ?? "")
      setRestoredPerformanceProfile(settings.profile)
      setPerformanceFormRevision(current => current + 1)
      setMode("performance")
      setSelectedHistoryID(item.id)
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.invalidHistory")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const cancel = async (id: string) => {
    if (inFlight.current || !id) return
    inFlight.current = true
    setPending("cancel")
    setError("")
    try {
      onWorkspaceUpdated(await actions.cancelRun(id))
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.failure")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const rememberCredential = async () => {
    if (inFlight.current || !form.source_run_id || !task || form.channel_id) return
    inFlight.current = true
    setPending("remember")
    setError("")
    try {
      await actions.rememberQuickTaskCredential({
        run_id: form.source_run_id,
        base_url: form.base_url.trim(),
        protocol,
        api_key: form.api_key.trim(),
      })
      onDraftChange((current) =>
        current === form
          ? { ...form, api_key: "", credential_run_id: form.source_run_id }
          : current,
      )
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.rememberFailed")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const forgetCredential = async () => {
    if (inFlight.current || !form.credential_run_id) return
    inFlight.current = true
    setPending("forget")
    setError("")
    try {
      await actions.forgetQuickTaskCredential(form.credential_run_id)
      onDraftChange((current) =>
        current.credential_run_id === form.credential_run_id
          ? { ...current, api_key: "", credential_run_id: undefined }
          : current,
      )
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("task.forgetFailed")))
    } finally {
      inFlight.current = false
      setPending(null)
    }
  }
  const openSaveChannel = () => {
    const connectionErrors = Object.fromEntries(
      Object.entries(quickTaskCommand(form).errors).filter(([field]) =>
        ["base_url", "api_key", "model"].includes(field),
      ),
    )
    setErrors(connectionErrors)
    setError("")
    const firstError = Object.keys(connectionErrors)[0]
    if (firstError) {
      document.getElementById(`quick-task-${firstError}`)?.focus()
      return
    }
    setSaveChannelOpen(true)
  }

  const protocolField = (
    <TaskField id="protocol" label={t("protocol")}>
      <SearchableSelect
        id="quick-task-protocol"
        aria-label={t("protocol")}
        value={protocol}
        disabled={busy}
        onValueChange={changeProtocol}
        className="w-full"
        options={protocolOptions.map(([value, label]) => ({ value, label, icon: <ProtocolIcon protocol={value} /> }))}
      />
    </TaskField>
  )

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={t("title")}>
      <div className="grid min-h-0 min-w-0 flex-1 grid-cols-[240px_minmax(0,1fr)] gap-4 p-4 xl:grid-cols-[256px_minmax(0,1fr)] overflow-hidden">
        <aside className="flex min-h-0 min-w-0 flex-col" aria-label={t("workspace.history")}>
          <div className="shrink-0 space-y-4 pb-4 pt-2">
            <QuickTestModeSwitch performance={mode === "performance"} onChange={checked => setMode(checked ? "performance" : "functional")} />
            <div className="flex items-center justify-between"><h2 className="text-sm font-semibold">{t("workspace.history")}</h2><span className="text-[11px] text-muted-foreground">{t("workspace.active", { count: history.filter(item => item.active).length })}</span></div>
          </div>
          <ScrollArea contentWidth="viewport" className="min-h-0 flex-1">
            {visibleHistory.length ? <QuickTestHistory items={visibleHistory} selectedID={selectedID} busy={busy}
              onSelect={item => void restoreHistory(item)}
              onRestore={id => { setMode("functional"); setSelectedHistoryID(id); void restore(id) }}
              onOpenReport={id => void onOpenReport(id)} onCancel={id => void cancel(id)} onRefresh={() => void refreshProgress()} />
              : <p className="py-4 text-xs leading-relaxed text-muted-foreground">{t("task.noHistory")}</p>}
            {runID && !history.some(item => item.runID === runID) ? <p role="status" className="flex items-center gap-2 py-4 text-xs text-muted-foreground"><Spinner />{t("task.waiting")}</p> : null}
          </ScrollArea>
        </aside>
        <div className="flex min-h-0 min-w-0 flex-col">
        <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0 flex-1">
          <section
            className="mx-auto w-full min-w-0 max-w-[800px] py-2"
            aria-label={t("connection")}
          >
            <div className="mb-4 flex h-7 min-w-0 items-center justify-between gap-2">
              <h2 className="text-sm font-semibold">{mode === "performance" ? t("performance.title") : t("workspace.functional")}</h2>
              <div ref={setPerformanceActionsContainer} hidden={mode !== "performance"} />
            </div>
            {error ? <Alert variant="destructive" className="mb-4"><AlertDescription>{error}</AlertDescription></Alert> : null}
            {refreshError ? <Alert className="mb-4"><AlertDescription>{t("task.refreshFailed")}</AlertDescription><Button size="sm" variant="outline" onClick={() => void refreshProgress()}>{t("task.refresh")}</Button></Alert> : null}
            {!task && mode === "functional" ? (
              <>
                <div className="mb-4">{protocolField}</div>
                <Empty>
                  <EmptyTitle>{t("task.empty")}</EmptyTitle>
                  <EmptyDescription>{t("task.emptyHint")}</EmptyDescription>
                </Empty>
              </>
            ) : (
              <div>
                <FieldGroup>
                  <TaskField id="task" label={t("task.label")} error={errors.task}
                    labelHint={<p id="quick-task-draft-hint" className="text-[11px] font-normal text-muted-foreground">{t(mode === "performance" ? "performance.planHint" : "task.draftNote")}</p>}
                  >
                    <SearchableSelect
                      value={mode === "performance" ? selectedPerformancePlanID : task ? taskKey(task) : ""}
                      onValueChange={value => {
                        if (mode === "functional") { changeTask(value); return }
                        const plan = performancePlans.find(item => item.id === value)
                        if (!plan?.performance) return
                        setSelectedPerformancePlanID(value); setRestoredPerformanceProfile(undefined)
                        setPerformanceFormRevision(current => current + 1)
                      }}
                      disabled={busy}
                      id="quick-task-task"
                      aria-label={t("task.label")}
                      aria-invalid={!!errors.task || undefined}
                      aria-describedby={["quick-task-draft-hint", ...(errors.task ? ["quick-task-task-error"] : [])].join(" ")}
                      className="w-full"
                      placeholder={mode === "performance" ? t("performance.choosePlan") : t("task.choose")}
                      options={mode === "performance" ? performancePlans.map(plan => ({ value: plan.id, label: plan.name })) : tasks.map((item) => ({
                        value: taskKey(item),
                        label: `${item.name} · ${t("task.caseCount", { count: item.case_count })}`,
                      }))}
                    />
                  </TaskField>
                  <div className="grid min-w-0 grid-cols-4 items-start gap-x-4 gap-y-2">
                    <TaskField id="channel_id" label={t("fillChannel")} error={errors.channel_id} className="row-span-2 grid grid-rows-subgrid">
                      <SearchableSelect value={missingChannel ? "missing" : form.channel_id || "manual"} disabled={busy} onValueChange={(value) => {
                        const selected = channels.find((item) => item.id === value)
                        update({
                          ...form,
                          channel_id: selected?.id ?? "",
                          base_url: selected?.base_url ?? form.base_url,
                          api_key: "",
                          credential_run_id: undefined,
                        })
                      }} id="quick-task-channel_id" aria-label={t("fillChannel")} aria-invalid={!!errors.channel_id || undefined} aria-describedby={errors.channel_id ? "quick-task-channel_id-error" : undefined} className="w-full" options={[({value: "manual", label: t("manual")}), ...(missingChannel ? [({value: "missing", label: t("task.missingChannel"), disabled: true})] : []), ...channels.map((item) => (
                            ({value: item.id, label: item.name})
                          ))]} />
                    </TaskField>
                    <TaskField id="base_url" label={t("address")} error={errors.base_url} className="col-span-3 row-span-2 grid grid-rows-subgrid"
                      labelHint={<p id="quick-task-base_url-hint" className="text-[11px] font-normal text-muted-foreground">{t("task.endpointHint")}</p>}
                    >
                      <ConnectionURLInput
                        id="quick-task-base_url"
                        value={channel?.base_url ?? form.base_url}
                        suffix={connectionURLs[0]?.suffix ?? ""}
                        disabled={busy || !!form.channel_id}
                        aria-invalid={!!errors.base_url || undefined}
                        aria-describedby={[
                          "quick-task-base_url-hint",
                          ...(errors.base_url ? ["quick-task-base_url-error"] : []),
                        ].join(" ")}
                        onChange={(event) =>
                          update({
                            ...form,
                            base_url: event.target.value,
                            api_key: "",
                            credential_run_id: undefined,
                          })
                        }
                        placeholder="https://api.example.com"
                      />
                    </TaskField>
                  </div>
                  <div className="grid min-w-0 grid-cols-4 items-start gap-4">
                    <TaskField id="model" label={t("model.label")} error={errors.model}>
                      <Autocomplete
                        modal={false}
                        openOnInputClick
                        items={models}
                        value={form.model}
                        onValueChange={(value) => update({ ...form, model: value ?? "" })}
                      >
                        <AutocompleteInput
                          leading={<ModelIcon model={form.model} />}
                          id="quick-task-model"
                          aria-label={t("model.label")}
                          value={form.model}
                          onChange={(event) => update({ ...form, model: event.target.value })}
                          disabled={busy}
                          aria-invalid={!!errors.model || undefined}
                          aria-describedby={errors.model ? "quick-task-model-error" : undefined}
                          placeholder={t("model.placeholder")}
                        />
                        <AutocompleteContent>
                          <AutocompleteEmpty>{t("model.empty")}</AutocompleteEmpty>
                          <AutocompleteList>
                            {(value) => (
                              <AutocompleteItem key={value} value={value} className="gap-2">
                                <ModelIcon model={value} />
                                <span className="min-w-0 [overflow-wrap:anywhere]">{value}</span>
                              </AutocompleteItem>
                            )}
                          </AutocompleteList>
                        </AutocompleteContent>
                      </Autocomplete>
                    </TaskField>
                    {protocolField}
                  <TaskField id="api_key" label="API Key" error={errors.api_key} className="col-span-2"
                    labelHint={form.credential_run_id ? <p id="quick-task-key-hint" className="text-[11px] font-normal text-muted-foreground">{t("task.rememberedKey")}</p> : undefined}
                  >
                    <InputGroup className="has-[input[aria-invalid=true]]:border-destructive">
                      <InputGroupInput
                        id="quick-task-api_key"
                        type="password"
                        autoComplete="off"
                        value={form.api_key}
                        disabled={busy || !!form.channel_id || !!form.credential_run_id}
                        aria-invalid={!!errors.api_key || undefined}
                        aria-describedby={[
                          ...(form.credential_run_id ? ["quick-task-key-hint"] : []),
                          ...(errors.api_key ? ["quick-task-api_key-error"] : []),
                        ].join(" ") || undefined}
                        placeholder={
                          channel
                            ? t("savedCredential", { name: channel.name })
                            : form.credential_run_id
                              ? t("task.rememberedKey")
                              : ""
                        }
                        onChange={(event) => update({ ...form, api_key: event.target.value })}
                      />
                      {!form.channel_id && (
                        <InputGroupAddon>
                          <TooltipProvider delayDuration={250}><Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                ref={saveChannelTrigger}
                                type="button"
                                size="icon-sm"
                                variant="ghost"
                                aria-label={t("saveChannel.title")}
                                disabled={busy}
                                onClick={openSaveChannel}
                              >
                                <SaveIcon aria-hidden="true" className="size-4" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>{t("saveChannel.title")}</TooltipContent>
                          </Tooltip></TooltipProvider>
                        </InputGroupAddon>
                      )}
                    </InputGroup>
                    {!form.channel_id &&
                      (form.credential_run_id ? (
                        <div className="space-y-2">
                          <div className="flex flex-wrap gap-2">
                            <Button
                              size="sm"
                              type="button"
                              variant="outline"
                              disabled={busy}
                              onClick={() => {
                                update({ ...form, credential_run_id: undefined, api_key: "" })
                                requestAnimationFrame(() =>
                                  document.getElementById("quick-task-api_key")?.focus(),
                                )
                              }}
                            >
                              {t("task.useOtherKey")}
                            </Button>
                            <Button
                              size="sm"
                              type="button"
                              variant="ghost"
                              disabled={busy}
                              onClick={() => void forgetCredential()}
                            >
                              {pending === "forget" ? t("task.forgetting") : t("task.forgetKey")}
                            </Button>
                          </div>
                        </div>
                      ) : form.source_run_id && form.api_key.trim() ? (
                        <Button
                          size="sm"
                          type="button"
                          variant="outline"
                          disabled={busy}
                          onClick={() => void rememberCredential()}
                        >
                          {pending === "remember" ? t("task.remembering") : t("task.rememberKey")}
                        </Button>
                      ) : null)}
                  </TaskField>
                  </div>
                  <form hidden={mode !== "functional"} onSubmit={(event) => void submit(event)} noValidate className="space-y-4">
                  <fieldset disabled={busy} className="@container/quick-task-parameters min-w-0 space-y-3">
                    <legend className="mb-3 text-xs font-medium">{t("task.parameters")}</legend>
                    <div className="grid min-w-0 grid-cols-1 items-start gap-3 @xs/quick-task-parameters:grid-cols-2 @md/quick-task-parameters:grid-cols-3">
                      <CaseConcurrencyField
                        value={caseConcurrency}
                        onChange={setCaseConcurrency}
                        disabled={busy}
                        labelHint={t("task.caseConcurrencyHint")}
                      />
                      <TaskField id="seed" label={t("task.seed")} error={errors.seed}>
                        <Input
                          id="quick-task-seed"
                          type="number"
                          min={0}
                          max={Number.MAX_SAFE_INTEGER}
                          value={form.seed}
                          aria-invalid={!!errors.seed || undefined}
                          aria-describedby={errors.seed ? "quick-task-seed-error" : undefined}
                          onChange={(event) => update({ ...form, seed: Number(event.target.value) })}
                        />
                      </TaskField>
                      <TaskField id="request_timeout_seconds" label={t("task.requestTimeout")} error={errors.request_timeout_seconds}>
                        <Input
                          id="quick-task-request_timeout_seconds"
                          type="number"
                          min={0.001}
                          step={0.001}
                          value={form.request_timeout_seconds}
                          aria-invalid={!!errors.request_timeout_seconds || undefined}
                          aria-describedby={errors.request_timeout_seconds ? "quick-task-request_timeout_seconds-error" : undefined}
                          onChange={(event) => update({ ...form, request_timeout_seconds: Number(event.target.value) })}
                        />
                      </TaskField>
                      {(task?.inputs ?? []).map((input) => (
                        <TaskField
                          key={input.key}
                          id={`input.${input.key}`}
                          label={input.label}
                          error={errors[`input.${input.key}`]}
                          className={input.type === "boolean" || input.type === "number" || input.type === "integer" ? undefined : "col-span-full"}
                        >
                          {input.type === "boolean" ? (
                            <Checkbox
                              id={`quick-task-input.${input.key}`}
                              checked={form.inputs[input.key] === true}
                              aria-invalid={!!errors[`input.${input.key}`] || undefined}
                              aria-describedby={errors[`input.${input.key}`] ? `quick-task-input.${input.key}-error` : undefined}
                              onCheckedChange={(checked) =>
                                update({
                                  ...form,
                                  inputs: { ...form.inputs, [input.key]: checked === true },
                                })
                              }
                            />
                          ) : (input.type === "number" || input.type === "integer") ? (
                            <Input
                              id={`quick-task-input.${input.key}`}
                              type="number"
                              step="any"
                              value={String(form.inputs[input.key] ?? "")}
                              aria-invalid={!!errors[`input.${input.key}`] || undefined}
                              aria-describedby={errors[`input.${input.key}`] ? `quick-task-input.${input.key}-error` : undefined}
                              onChange={(event) =>
                                update({
                                  ...form,
                                  inputs: { ...form.inputs, [input.key]: event.target.value },
                                })
                              }
                            />
                          ) : (
                            <Textarea
                              id={`quick-task-input.${input.key}`}
                              value={String(form.inputs[input.key] ?? "")}
                              className="min-h-24"
                              aria-invalid={!!errors[`input.${input.key}`] || undefined}
                              aria-describedby={errors[`input.${input.key}`] ? `quick-task-input.${input.key}-error` : undefined}
                              onChange={(event) =>
                                update({
                                  ...form,
                                  inputs: { ...form.inputs, [input.key]: event.target.value },
                                })
                              }
                            />
                          )}
                        </TaskField>
                      ))}
                    </div>
                  </fieldset>
                  <div className="flex flex-wrap justify-end gap-2">
                    <Button type="submit" disabled={busy}>
                      {pending === "start" ? (
                        <Spinner data-icon="inline-start" />
                      ) : (
                        <PlayIcon data-icon="inline-start" />
                      )}
                      {pending === "start" ? t("task.starting") : t("task.start")}
                    </Button>
                  </div>
                  </form>
                  <div hidden={mode !== "performance"}>
                    {isQuickPerformanceProtocol(protocol) ? <QuickPerformancePanel
                      key={performanceFormRevision} plans={catalog.plans}
                      initialProfile={restoredPerformanceProfile} initialPlanID={selectedPerformancePlanID} onPlanChange={setSelectedPerformancePlanID}
                      embedded showPlanSelector={false} blocked={!!pending} connection={performanceConnection} actionsContainer={performanceActionsContainer}
                      resolveConnection={validPerformanceConnection}
                      run={runPerformance} onArchived={onPerformanceArchived} onOpenReport={onOpenReport}
                    /> : <p className="text-xs text-muted-foreground">{t("task.performanceUnavailable")}</p>}
                  </div>
                </FieldGroup>
              </div>
            )}
          </section>
        </ScrollArea>
        </div>
      </div>
      {saveChannelOpen && <SaveChannelSheet
        open={saveChannelOpen}
        onOpenChange={setSaveChannelOpen}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          const trigger = saveChannelTrigger.current ?? document.getElementById("quick-task-channel_id")
          trigger?.focus()
        }}
        connection={{ base_url: form.base_url, api_key: form.api_key.trim(), credential_run_id: form.credential_run_id, model_name: form.model.trim(), protocol }}
        onSave={async command => {
          if (inFlight.current) throw new Error(t("saveChannel.failed"))
          inFlight.current = true
          setPending("save")
          try { return await onSaveChannel(command) }
          finally { inFlight.current = false; setPending(null) }
        }}
        onSaved={saved => update({ ...form, channel_id: saved.channel_id, api_key: "", credential_run_id: undefined })}
      />}
    </main>
  )
}

function taskKey(task: CatalogSuite) {
  return `${task.id}:${task.revision}`
}
function TaskField({
  id,
  label,
  labelHint,
  error,
  children,
  className,
}: {
  id: string
  label: string
  labelHint?: ReactNode
  error?: string
  children: ReactNode
  className?: string
}) {
  return (
    <Field className={cn("min-w-0 flex-col items-stretch gap-2", className)} data-invalid={!!error || undefined}>
      <div className="flex min-w-0 items-baseline gap-2">
        <FieldLabel htmlFor={`quick-task-${id}`} className="shrink-0 [overflow-wrap:anywhere]">{label}</FieldLabel>
        {labelHint || error ? (
          <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-1 [overflow-wrap:anywhere]">
            {labelHint}
            {error ? <FieldError id={`quick-task-${id}-error`} className="min-w-0 text-[11px] font-normal">{error}</FieldError> : null}
          </div>
        ) : null}
      </div>
      {children}
    </Field>
  )
}
