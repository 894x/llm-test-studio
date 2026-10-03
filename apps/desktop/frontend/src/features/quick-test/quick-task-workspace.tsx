import {
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
  type Dispatch,
  type SetStateAction,
} from "react"
import { useTranslation } from "react-i18next"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { CatalogSnapshot, CatalogSuite } from "@/features/catalog/data"
import { DEFAULT_CASE_CONCURRENCY, isRunActive, type WorkspaceSnapshot } from "@/features/runs/data"
import { CaseConcurrencyField } from "@/features/runs/case-concurrency-field"
import type { ReportSnapshot } from "@/features/reports/data"
import { QuickPerformanceSheet, type QuickPerformanceConnection } from "./quick-performance-sheet"
import { createTaskDraft, quickTaskCommand, restoreTaskDraft, type TaskDraft } from "./task-draft"
import { cn } from "@/lib/utils"

type Actions = Pick<
  DesktopClient,
  | "startQuickTask"
  | "getQuickTask"
  | "rememberQuickTaskCredential"
  | "forgetQuickTaskCredential"
  | "cancelRun"
  | "runQuickPerformanceTest"
>

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
}) {
  const { t } = useTranslation("quickTest")
  const { i18n } = useTranslation()
  const [pending, setPending] = useState<
    "start" | "history" | "cancel" | "remember" | "forget" | null
  >(null)
  const inFlight = useRef(false)
  const performanceTrigger = useRef<HTMLButtonElement>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState("")
  const [caseConcurrency, setCaseConcurrency] = useState(DEFAULT_CASE_CONCURRENCY)
  const [refreshError, setRefreshError] = useState(false)
  const [performanceCommand, setPerformanceCommand] = useState<QuickPerformanceConnection | null>(
    null,
  )
  const [performanceOpen, setPerformanceOpen] = useState(false)
  const currentTasks = catalog.suites
  const task = draft.task ?? currentTasks[0]
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
      channel.protocol === task?.protocol && channel.enabled && channel.credential_configured,
  )
  const channel = channels.find((item) => item.id === form.channel_id)
  const missingChannel = !!form.channel_id && !channel
  const models = [
    ...new Set([
      ...catalog.channel_models
        .filter((mapping) => mapping.channel_id === form.channel_id)
        .map((mapping) => mapping.upstream_model_name),
      ...catalog.models
        .filter((model) => model.protocol === task?.protocol)
        .map((model) => model.name),
    ]),
  ]
  const selectedRun = workspace.runs.find((run) => run.id === runID)
  const recent = workspace.runs.filter((run) => run.source === "quick_task").slice(0, 12)
  const selectedReport = reports.reports.find((report) => report.run_id === runID)

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
    update(next)
  }
  const refreshProgress = async () => {
    try {
      await refresh()
      setRefreshError(false)
    } catch {
      setRefreshError(true)
    }
  }
  const validCommand = (element: Element) => {
    const checked = quickTaskCommand(form, caseConcurrency)
    if (missingChannel) checked.errors.channel_id = t("task.missingChannel")
    setErrors(checked.errors)
    if (Object.keys(checked.errors).length) {
      const field = Object.keys(checked.errors)[0]
      const control = document.getElementById(`quick-task-${field}`)
      if (control instanceof HTMLElement && element.contains(control)) control.focus()
      return undefined
    }
    return checked.command
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (inFlight.current) return
    const command = validCommand(event.currentTarget)
    if (!command) return
    inFlight.current = true
    setPending("start")
    setError("")
    setRefreshError(false)
    onDraftChange(form)
    try {
      const id = await actions.startQuickTask(command)
      onRunSelected(id)
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
  const cancel = async () => {
    if (inFlight.current || !runID) return
    inFlight.current = true
    setPending("cancel")
    setError("")
    try {
      onWorkspaceUpdated(await actions.cancelRun(runID))
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
        protocol: task.protocol,
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
  const openPerformance = (element: HTMLElement) => {
    const formElement = element.closest("form")
    if (!formElement || !validCommand(formElement)) return
    setPerformanceCommand({
      task: {
        suite_id: task!.id,
        suite_revision: task!.revision,
        ...(form.source_run_id ? { source_run_id: form.source_run_id } : {}),
      },
      address_mode: "base_url",
      url: channel?.base_url ?? form.base_url.trim(),
      api_key: form.channel_id || form.credential_run_id ? "" : form.api_key.trim(),
      ...(form.credential_run_id ? { credential_run_id: form.credential_run_id } : {}),
      model_id: form.model.trim(),
      ...(form.channel_id ? { channel_id: form.channel_id } : {}),
    })
    setPerformanceOpen(true)
  }

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col" aria-labelledby="quick-test-heading">
      <header className="shrink-0 px-4 py-3">
        <h1 id="quick-test-heading" className="text-lg font-semibold tracking-tight">
          {t("title")}
        </h1>
        <p className="mt-1 text-[11px] text-muted-foreground">{t("description")}</p>
      </header>
      <div className="grid min-h-0 min-w-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(320px,0.8fr)] overflow-hidden">
        <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0">
          <section
            className="min-w-0 p-4"
            aria-label={t("connection")}
          >
            {!task ? (
              <Empty>
                <EmptyTitle>{t("task.empty")}</EmptyTitle>
                <EmptyDescription>{t("task.emptyHint")}</EmptyDescription>
              </Empty>
            ) : (
              <form onSubmit={(event) => void submit(event)} noValidate>
                <FieldGroup>
                  <TaskField id="task" label={t("task.label")} error={errors.task}>
                    <SearchableSelect value={taskKey(task)} onValueChange={changeTask} disabled={!!pending} id="quick-task-task" aria-label={t("task.label")} className="w-full" options={[...tasks.map((item) => (
                            ({value: taskKey(item), label: item.name})
                          ))]} />
                    <p className="text-[11px] text-muted-foreground">
                      {task.description}
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      {t("task.version", { revision: task.revision, count: task.case_count })} ·{" "}
                      {t("task.timeout", { seconds: form.request_timeout_ms / 1000 })}
                    </p>
                  </TaskField>
                  {form.source_run_id ? (
                    <p className="text-xs text-muted-foreground">{t("task.historyVersion")}</p>
                  ) : null}
                  <TaskField id="channel_id" label={t("fillChannel")} error={errors.channel_id}>
                    <SearchableSelect value={missingChannel ? "missing" : form.channel_id || "manual"} disabled={!!pending} onValueChange={(value) => {
                        const selected = channels.find((item) => item.id === value)
                        update({
                          ...form,
                          channel_id: selected?.id ?? "",
                          base_url: selected?.base_url ?? form.base_url,
                          api_key: "",
                          credential_run_id: undefined,
                        })
                      }} id="quick-task-channel_id" aria-label={t("fillChannel")} className="w-full" options={[({value: "manual", label: t("manual")}), ...(missingChannel ? [({value: "missing", label: t("task.missingChannel"), disabled: true})] : []), ...channels.map((item) => (
                            ({value: item.id, label: item.name})
                          ))]} />
                  </TaskField>
                  <div className="grid min-w-0 grid-cols-2 items-start gap-4">
                    <TaskField id="base_url" label={t("address")} error={errors.base_url}>
                      <Input
                        id="quick-task-base_url"
                        value={channel?.base_url ?? form.base_url}
                        disabled={!!pending || !!form.channel_id}
                        aria-invalid={!!errors.base_url || undefined}
                        aria-describedby={errors.base_url ? "quick-task-base_url-error" : undefined}
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
                      <p className="text-[11px] text-muted-foreground">{t("task.endpointHint")}</p>
                    </TaskField>
                    <TaskField id="model" label={t("model.label")} error={errors.model}>
                      <Autocomplete
                        modal={false}
                        openOnInputClick
                        items={models}
                        value={form.model}
                        onValueChange={(value) => update({ ...form, model: value ?? "" })}
                      >
                        <AutocompleteInput
                          id="quick-task-model"
                          aria-label={t("model.label")}
                          value={form.model}
                          onChange={(event) => update({ ...form, model: event.target.value })}
                          disabled={!!pending}
                          aria-invalid={!!errors.model || undefined}
                          aria-describedby={errors.model ? "quick-task-model-error" : undefined}
                          placeholder={t("model.placeholder")}
                        />
                        <AutocompleteContent>
                          <AutocompleteEmpty>{t("model.empty")}</AutocompleteEmpty>
                          <AutocompleteList>
                            {(value) => (
                              <AutocompleteItem key={value} value={value}>
                                {value}
                              </AutocompleteItem>
                            )}
                          </AutocompleteList>
                        </AutocompleteContent>
                      </Autocomplete>
                    </TaskField>
                  </div>
                  <TaskField id="api_key" label="API Key" error={errors.api_key}>
                    <Input
                      id="quick-task-api_key"
                      type="password"
                      autoComplete="off"
                      value={form.api_key}
                      disabled={!!pending || !!form.channel_id || !!form.credential_run_id}
                      aria-invalid={!!errors.api_key || undefined}
                      aria-describedby={errors.api_key ? "quick-task-api_key-error" : undefined}
                      placeholder={
                        channel
                          ? t("savedCredential", { name: channel.name })
                          : form.credential_run_id
                            ? t("task.rememberedKey")
                            : ""
                      }
                      onChange={(event) => update({ ...form, api_key: event.target.value })}
                    />
                    {!form.channel_id &&
                      (form.credential_run_id ? (
                        <div className="space-y-2">
                          <p className="text-[11px] text-muted-foreground">
                            {t("task.rememberedKey")}
                          </p>
                          <div className="flex flex-wrap gap-2">
                            <Button
                              size="sm"
                              type="button"
                              variant="outline"
                              disabled={!!pending}
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
                              disabled={!!pending}
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
                          disabled={!!pending}
                          onClick={() => void rememberCredential()}
                        >
                          {pending === "remember" ? t("task.remembering") : t("task.rememberKey")}
                        </Button>
                      ) : null)}
                  </TaskField>
                  <fieldset disabled={!!pending} className="@container/quick-task-parameters min-w-0 space-y-3">
                    <legend className="mb-3 text-sm font-semibold">{t("task.parameters")}</legend>
                    <div className="grid min-w-0 grid-cols-1 items-start gap-3 @xs/quick-task-parameters:grid-cols-2 @md/quick-task-parameters:grid-cols-3">
                      <CaseConcurrencyField
                        value={caseConcurrency}
                        onChange={setCaseConcurrency}
                        disabled={!!pending}
                        externalDescriptionId="quick-task-case-concurrency-hint"
                      />
                      <TaskField id="seed" label={t("task.seed")} error={errors.seed}>
                        <Input
                          id="quick-task-seed"
                          type="number"
                          min={0}
                          max={Number.MAX_SAFE_INTEGER}
                          value={form.seed}
                          onChange={(event) => update({ ...form, seed: Number(event.target.value) })}
                        />
                      </TaskField>
                      <TaskField id="request_timeout_ms" label={t("task.requestTimeout")} error={errors.request_timeout_ms}>
                        <Input
                          id="quick-task-request_timeout_ms"
                          type="number"
                          min={1}
                          value={form.request_timeout_ms}
                          onChange={(event) => update({ ...form, request_timeout_ms: Number(event.target.value) })}
                        />
                      </TaskField>
                      <p id="quick-task-case-concurrency-hint" className="col-span-full text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
                        {t("runs:caseConcurrency.hint")}
                      </p>
                      {task.inputs.map((input) => (
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
                  <p className="text-[11px] text-muted-foreground">{t("task.draftNote")}</p>
                  <div className="flex flex-wrap gap-2">
                    <Button type="submit" disabled={!!pending}>
                      {pending === "start" ? (
                        <Spinner data-icon="inline-start" />
                      ) : (
                        <PlayIcon data-icon="inline-start" />
                      )}
                      {pending === "start" ? t("task.starting") : t("task.start")}
                    </Button>
                    {task.protocol === "openai-chat" ? (
                      <Button
                        ref={performanceTrigger}
                        type="button"
                        variant="outline"
                        disabled={!!pending}
                        onClick={(event) => openPerformance(event.currentTarget)}
                      >
                        <GaugeIcon data-icon="inline-start" />
                        {t("task.performance")}
                      </Button>
                    ) : null}
                  </div>
                </FieldGroup>
              </form>
            )}
          </section>
        </ScrollArea>
        <ScrollArea contentWidth="viewport" className="min-h-0 min-w-0">
          <section className="min-w-0 space-y-5 p-4" aria-label={t("task.progress")}>
            {error ? (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            {refreshError ? (
              <Alert>
                <AlertDescription>{t("task.refreshFailed")}</AlertDescription>
              </Alert>
            ) : null}
            <div className="space-y-3">
              <h2 className="text-sm font-semibold">{t("task.progress")}</h2>
              {!runID ? (
                <p className="text-xs text-muted-foreground">{t("task.notStarted")}</p>
              ) : (
                <>
                  <p className="truncate font-mono text-[11px] text-muted-foreground" title={runID}>
                    {t("task.runID")}: {runID}
                  </p>
                  {selectedRun ? (
                    <>
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge
                          variant={
                            selectedRun.conclusion === "failed" || selectedRun.status === "failed"
                              ? "destructive"
                              : "outline"
                          }
                        >
                          {t(
                            `common:status.${selectedRun.conclusion === "none" ? (selectedRun.status === "starting" ? "queued" : selectedRun.status) : selectedRun.conclusion}`,
                          )}
                        </Badge>
                        <span className="min-w-0 truncate text-xs">{selectedRun.plan_name}</span>
                      </div>
                      <p className="text-xs tabular-nums">{t("task.observed", selectedRun)}</p>
                      <p className="text-[11px] text-muted-foreground">
                        {t("task.localTime")}:{" "}
                        {new Date(selectedRun.started_at).toLocaleString(i18n.language)}
                      </p>
                      {selectedRun.error_code ? (
                        <p className="text-xs text-destructive">
                          {t(`common:errorCode.${selectedRun.error_code}`, {
                            defaultValue: selectedRun.error_code,
                          })}
                        </p>
                      ) : null}
                    </>
                  ) : (
                    <p className="flex items-center gap-2 text-xs">
                      <Spinner />
                      {t("task.waiting")}
                    </p>
                  )}
                  <div className="flex flex-wrap gap-2">
                    <Button size="sm" variant="outline" onClick={() => void refreshProgress()}>
                      {t("task.refresh")}
                    </Button>
                    {!selectedRun || isRunActive(selectedRun.status) ? (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!!pending}
                        onClick={() => void cancel()}
                      >
                        {pending === "cancel" ? t("task.stopping") : t("task.cancel")}
                      </Button>
                    ) : null}
                    {selectedReport ? (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void onOpenReport(selectedReport.id)}
                      >
                        {t("task.report")}
                      </Button>
                    ) : null}
                  </div>
                </>
              )}
            </div>
            <div className="pt-4">
              <h2 className="mb-3 text-sm font-semibold">{t("task.history")}</h2>
              {!recent.length ? (
                <p className="text-xs text-muted-foreground">{t("task.noHistory")}</p>
              ) : (
                <ol className="space-y-0.5">
                  {recent.map((run) => (
                    <li key={run.id} data-selected={run.id === runID} className="min-w-0 space-y-1 rounded-md p-3 transition-colors hover:bg-surface-hover focus-within:bg-surface-hover data-[selected=true]:bg-surface-active">
                      <div className="flex items-start justify-between gap-2">
                        <p className="min-w-0 text-xs font-medium [overflow-wrap:anywhere]" title={run.plan_name}>
                          {run.plan_name}
                        </p>
                        <Badge variant="outline">
                          {t(
                            `common:status.${run.conclusion === "none" ? (run.status === "starting" ? "queued" : run.status) : run.conclusion}`,
                          )}
                        </Badge>
                      </div>
                      <p
                        className="text-[11px] text-muted-foreground [overflow-wrap:anywhere]"
                        title={`${run.model_name} · ${run.channel_name}`}
                      >
                        {run.model_name} · {run.channel_name}
                      </p>
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <time
                          className="text-[11px] text-muted-foreground"
                          dateTime={run.started_at}
                        >
                          {new Date(run.started_at).toLocaleString(i18n.language)}
                        </time>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!!pending}
                          onClick={() => void restore(run.id)}
                          aria-label={`${t("task.restore")} ${run.plan_name}`}
                        >
                          {t("task.restore")}
                        </Button>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </section>
        </ScrollArea>
      </div>
      {performanceCommand ? (
        <QuickPerformanceSheet
          open={performanceOpen}
          onOpenChange={setPerformanceOpen}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            performanceTrigger.current?.focus()
          }}
          connection={performanceCommand}
          run={actions.runQuickPerformanceTest}
          onArchived={onPerformanceArchived}
          onOpenReport={onOpenReport}
        />
      ) : null}
    </main>
  )
}

function taskKey(task: CatalogSuite) {
  return `${task.id}:${task.revision}`
}
function TaskField({
  id,
  label,
  error,
  children,
  className,
}: {
  id: string
  label: string
  error?: string
  children: ReactNode
  className?: string
}) {
  return (
    <Field className={cn("block min-w-0 space-y-2", className)} data-invalid={!!error || undefined}>
      <FieldLabel htmlFor={`quick-task-${id}`} className="[overflow-wrap:anywhere]">{label}</FieldLabel>
      {children}
      {error ? <FieldError id={`quick-task-${id}-error`}>{error}</FieldError> : null}
    </Field>
  )
}
