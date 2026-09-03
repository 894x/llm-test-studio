import { useState, type ComponentProps, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import ArrowRightIcon from "lucide-react/dist/esm/icons/arrow-right.mjs"
import CheckCircle2Icon from "lucide-react/dist/esm/icons/check-circle-2.mjs"
import CircleAlertIcon from "lucide-react/dist/esm/icons/circle-alert.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"
import PlugZapIcon from "lucide-react/dist/esm/icons/plug-zap.mjs"

import {
  DesktopClientError,
  publicDesktopErrorMessage,
  type DesktopClient,
} from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import {
  Autocomplete,
  AutocompleteContent,
  AutocompleteEmpty,
  AutocompleteInput,
  AutocompleteItem,
  AutocompleteList,
} from "@/components/ui/autocomplete"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { PerformanceCharts } from "@/features/reports/performance-charts"
import { PerformanceLatencyTable } from "@/features/reports/performance-latency-table"

import {
  updateQuickTestForm,
  type QuickPerformanceCommand,
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickTestAddressMode,
  type QuickTestCommand,
  type QuickTestResult,
  type SaveQuickTestConnectionCommand,
} from "./data"
import { performanceCompletion, performanceProgressPhaseLabel } from "./performance-summary"

type QuickTestActions = Pick<
  DesktopClient,
  "runQuickTest" | "runQuickPerformanceTest" | "saveQuickTestConnection"
>

const DEFAULT_PROMPT = "Reply with OK only."
const DEFAULT_TIMEOUT_MS = 30_000

export interface QuickTestModelCandidate {
  id: string
  name: string
}

export interface QuickTestChannelCandidate {
  id: string
  name: string
  baseUrl: string
}

interface TestedQuickTest {
  command: QuickTestCommand
  result: QuickTestResult
  existingModel?: QuickTestModelCandidate
}

export function QuickTestWorkspace({
  modelCandidates,
  channelCandidates = [],
  runQuickTest,
  runQuickPerformanceTest,
  saveQuickTestConnection,
  refreshCatalog,
  onCatalogUpdated,
  onOpenCatalog,
  onPerformanceArchived,
  onOpenReport,
}: QuickTestActions & {
  modelCandidates: readonly QuickTestModelCandidate[]
  channelCandidates?: readonly QuickTestChannelCandidate[]
  refreshCatalog: () => Promise<CatalogSnapshot>
  onCatalogUpdated: (catalog: CatalogSnapshot) => void
  onOpenCatalog: () => void
  onPerformanceArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
}) {
  const { t } = useTranslation("quickTest")
  const [form, setForm] = useState<QuickTestCommand>({
    address_mode: "base_url",
    url: "",
    api_key: "",
    model_id: "",
    prompt: DEFAULT_PROMPT,
    timeout_ms: DEFAULT_TIMEOUT_MS,
  })
  const [pending, setPending] = useState(false)
  const [tested, setTested] = useState<TestedQuickTest | null>(null)
  const [requestError, setRequestError] = useState("")
  const [saveOpen, setSaveOpen] = useState(false)
  const [performanceOpen, setPerformanceOpen] = useState(false)
  const [saved, setSaved] = useState(false)
  const matchingModels = modelCandidates.filter(
    (candidate) => candidate.name === form.model_id.trim(),
  )
  const existingModel = matchingModels.length === 1 ? matchingModels[0] : undefined
  const ambiguousModelName = matchingModels.length > 1
  const modelOptionNames = [...new Set(modelCandidates.map((candidate) => candidate.name))]
  const selectedChannel = channelCandidates.find((candidate) => candidate.id === form.channel_id)

  const update = <K extends keyof QuickTestCommand>(
    key: K,
    value: QuickTestCommand[K],
  ) => {
    setForm((current) => updateQuickTestForm(current, key, value))
    setTested(null)
    setRequestError("")
    setSaved(false)
  }

  const selectChannel = (channelID: string) => {
    if (channelID === "manual") {
      setForm((current) => {
        const { channel_id: _channelID, ...manual } = current
        return manual
      })
    } else {
      const channel = channelCandidates.find((candidate) => candidate.id === channelID)
      if (!channel) return
      setForm((current) => ({
        ...current,
        address_mode: "base_url",
        url: channel.baseUrl,
        api_key: "",
        channel_id: channel.id,
      }))
    }
    setTested(null)
    setRequestError("")
    setSaved(false)
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    const command = {
      ...form,
      url: form.url.trim(),
      api_key: form.api_key.trim(),
      model_id: form.model_id.trim(),
      prompt: form.prompt,
    }
    const testedExistingModel = existingModel
    setPending(true)
    setTested(null)
    setRequestError("")
    setSaved(false)
    void runQuickTest(command)
      .then((result) => setTested({
        command,
        result,
        ...(testedExistingModel ? { existingModel: testedExistingModel } : {}),
      }))
      .catch((error: unknown) => {
        setRequestError(
          publicDesktopErrorMessage(error, t("error")),
        )
      })
      .finally(() => setPending(false))
  }

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col" aria-labelledby="quick-test-heading">
      <header className="flex shrink-0 items-start justify-between gap-4 border-b px-4 py-3">
        <div className="min-w-0">
          <h1 id="quick-test-heading" className="text-lg font-semibold tracking-tight">
            {t("title")}
          </h1>
          <p className="mt-1 text-[11px] text-muted-foreground">
            {t("description")}
          </p>
        </div>
        <Badge variant="outline" className="shrink-0">
          {t("temporary")}
        </Badge>
      </header>

      <ScrollArea className="min-h-0 flex-1">
        <div className="grid min-h-full min-w-0 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(340px,0.8fr)]">
          <section aria-labelledby="quick-test-form-heading" className="min-w-0 border-b p-4 lg:border-r lg:border-b-0">
            <div className="mb-4">
              <h2 id="quick-test-form-heading" className="text-sm font-semibold">
                {t("connection")}
              </h2>
              <p className="mt-1 text-[11px] text-muted-foreground">
                {t("keyNote")}
              </p>
            </div>
            <form onSubmit={submit}>
              <FieldGroup>
                <Field className="block">
                  <FieldLabel>{t("fillChannel")}</FieldLabel>
                  <FieldContent>
                    <Select value={form.channel_id ?? "manual"} onValueChange={selectChannel}>
                      <SelectTrigger aria-label={t("fillChannel")} className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="manual">{t("manual")}</SelectItem>
                          {channelCandidates.map((channel) => (
                            <SelectItem key={channel.id} value={channel.id}>{channel.name}</SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {selectedChannel
                        ? t("filled", { name: selectedChannel.name })
                        : channelCandidates.length
                          ? t("chooseChannel")
                          : t("noChannel")}
                    </FieldDescription>
                  </FieldContent>
                </Field>
                <fieldset>
                  <legend className="mb-2 text-xs font-medium">{t("addressMode")}</legend>
                  <RadioGroup
                    value={form.address_mode}
                    onValueChange={(value) => update("address_mode", value as QuickTestAddressMode)}
                    className="grid grid-cols-2 gap-2"
                  >
                    <ModeOption
                      id="quick-test-base-url"
                      value="base_url"
                      label="Base URL"
                      description={t("baseUrlHint")}
                    />
                    <ModeOption
                      id="quick-test-full-url"
                      value="full_url"
                      label={t("fullUrl")}
                      description={t("fullUrlHint")}
                    />
                  </RadioGroup>
                </fieldset>

                <TextField
                  label={t("address")}
                  value={form.url}
                  onChange={(value) => update("url", value)}
                  placeholder={form.address_mode === "base_url" ? "https://api.example.com/v1" : "https://api.example.com/v1/chat/completions"}
                  required
                />
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <TextField
                    label="API Key"
                    type="password"
                    autoComplete="new-password"
                    value={form.api_key}
                    onChange={(value) => update("api_key", value)}
                    disabled={!!selectedChannel}
                    placeholder={selectedChannel ? t("savedCredential", { name: selectedChannel.name }) : undefined}
                    required={!selectedChannel}
                  />
                  <ModelIDField
                    value={form.model_id}
                    onChange={(value) => update("model_id", value)}
                    optionNames={modelOptionNames}
                    existingModel={existingModel}
                    ambiguous={ambiguousModelName}
                  />
                </div>
                <Field className="block">
                  <FieldLabel htmlFor="quick-test-prompt">{t("message")}</FieldLabel>
                  <FieldContent>
                    <Textarea
                      id="quick-test-prompt"
                      aria-label={t("message")}
                      className="min-h-20 resize-y"
                      value={form.prompt}
                      onChange={(event) => update("prompt", event.target.value)}
                      required
                    />
                  </FieldContent>
                </Field>
                <Field className="block max-w-52">
                  <FieldLabel htmlFor="quick-test-timeout">{t("timeout")}</FieldLabel>
                  <FieldContent>
                    <Input
                      id="quick-test-timeout"
                      aria-label={t("timeout")}
                      type="number"
                      min={1_000}
                      max={120_000}
                      step={1_000}
                      value={form.timeout_ms}
                      onChange={(event) => update("timeout_ms", Number(event.target.value))}
                      required
                    />
                    <FieldDescription>{t("timeoutHint")}</FieldDescription>
                  </FieldContent>
                </Field>
                {requestError ? (
                  <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                    {requestError}
                  </FieldError>
                ) : null}
                <Button type="submit" className="w-fit min-w-24" disabled={pending}>
                  {pending ? (
                    <><Spinner data-icon="inline-start" />{t("testing")}</>
                  ) : (
                    <><PlugZapIcon data-icon="inline-start" />{t("send")}</>
                  )}
                </Button>
              </FieldGroup>
            </form>
          </section>

          <section aria-labelledby="quick-test-result-heading" className="min-w-0 p-4">
            <h2 id="quick-test-result-heading" className="text-sm font-semibold">{t("result")}</h2>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {t("resultHint")}
            </p>
            <div className="mt-4">
              {tested ? (
                <ResultPanel
                  result={tested.result}
                  saved={saved}
                  catalogChannelSelected={!!tested.command.channel_id}
                  onSave={() => setSaveOpen(true)}
                  onPerformance={() => setPerformanceOpen(true)}
                  onOpenCatalog={onOpenCatalog}
                />
              ) : (
                <div className="flex min-h-52 flex-col items-center justify-center rounded-lg border border-dashed px-6 text-center">
                  <PlugZapIcon className="size-5 text-muted-foreground" />
                  <p className="mt-3 text-sm font-medium">{t("waiting")}</p>
                  <p className="mt-1 max-w-xs text-xs text-muted-foreground">
                    {t("waitingHint")}
                  </p>
                </div>
              )}
            </div>
          </section>
        </div>
      </ScrollArea>

      {tested?.result.success ? (
        <>
          <SaveConnectionSheet
            open={saveOpen}
            onOpenChange={setSaveOpen}
            result={tested.result}
            apiKey={tested.command.api_key}
            modelID={tested.command.model_id}
            existingModel={tested.existingModel}
            save={saveQuickTestConnection}
            refreshCatalog={refreshCatalog}
            onCatalogUpdated={onCatalogUpdated}
            onSaved={(catalog) => {
              onCatalogUpdated(catalog)
              setSaved(true)
              setSaveOpen(false)
            }}
            onOpenCatalog={onOpenCatalog}
          />
          <QuickPerformanceSheet
            open={performanceOpen}
            onOpenChange={setPerformanceOpen}
            testedCommand={tested.command}
            run={runQuickPerformanceTest}
            onArchived={onPerformanceArchived}
            onOpenReport={onOpenReport}
          />
        </>
      ) : null}
    </main>
  )
}

function ModeOption({ id, value, label, description }: {
  id: string
  value: QuickTestAddressMode
  label: string
  description: string
}) {
  return (
    <label htmlFor={id} className="flex min-w-0 cursor-pointer gap-2 rounded-lg border bg-surface-control p-3 transition-colors hover:bg-surface-hover has-data-[state=checked]:border-border-strong has-data-[state=checked]:bg-surface-active">
      <RadioGroupItem id={id} value={value} aria-label={label} />
      <span className="min-w-0">
        <span className="block text-xs font-medium">{label}</span>
        <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{description}</span>
      </span>
    </label>
  )
}

function TextField({ label, value, onChange, type = "text", ...props }: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: "text" | "password"
} & Omit<ComponentProps<typeof Input>, "value" | "onChange" | "type">) {
  const id = `quick-test-${label}`
  return (
    <Field className="block">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <FieldContent>
        <Input
          {...props}
          id={id}
          aria-label={label}
          type={type}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
      </FieldContent>
    </Field>
  )
}

function ModelIDField({ value, onChange, optionNames, existingModel, ambiguous }: {
  value: string
  onChange: (value: string) => void
  optionNames: readonly string[]
  existingModel?: QuickTestModelCandidate
  ambiguous: boolean
}) {
  const { t } = useTranslation("quickTest")
  return (
    <Field className="block">
      <FieldLabel htmlFor="quick-test-model-id">{t("model.label")}</FieldLabel>
      <FieldContent>
        <Autocomplete
          items={optionNames}
          value={value}
          onValueChange={onChange}
          modal={false}
          openOnInputClick
        >
          <AutocompleteInput
            id="quick-test-model-id"
            aria-label={t("model.label")}
            placeholder={t("model.placeholder")}
            required
            triggerLabel={t("model.trigger")}
            triggerDisabled={optionNames.length === 0}
          />
          {optionNames.length > 0 ? (
            <AutocompleteContent>
              <AutocompleteEmpty>{t("model.empty")}</AutocompleteEmpty>
              <AutocompleteList>
                {(name) => (
                  <AutocompleteItem key={name} value={name}>
                    {name}
                  </AutocompleteItem>
                )}
              </AutocompleteList>
            </AutocompleteContent>
          ) : null}
        </Autocomplete>
        <FieldDescription>
          {existingModel
            ? t("model.matched", { name: existingModel.name })
            : ambiguous
              ? t("model.ambiguous")
              : optionNames.length
              ? t("model.choose")
              : t("model.none")}
        </FieldDescription>
      </FieldContent>
    </Field>
  )
}

function ResultPanel({ result, saved, catalogChannelSelected = false, onSave, onPerformance, onOpenCatalog }: {
  result: QuickTestResult
  saved: boolean
  catalogChannelSelected?: boolean
  onSave: () => void
  onPerformance: () => void
  onOpenCatalog: () => void
}) {
  const { t } = useTranslation("quickTest")
  const title = result.success
    ? t("resultPanel.success")
    : t(`errorCode.${result.error_code ?? "request_failed"}`)
  return (
    <div className="rounded-lg border bg-surface-subtle">
      <div className="flex items-start gap-3 p-4">
        {result.success ? (
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-success" />
        ) : (
          <CircleAlertIcon className="mt-0.5 size-5 shrink-0 text-destructive" />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold">{title}</h3>
            <Badge variant={result.success ? "secondary" : "destructive"}>
              {t(result.success ? "resultPanel.available" : "resultPanel.failed")}
            </Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {t(result.success ? "resultPanel.successHint" : "resultPanel.failedHint")}
          </p>
        </div>
      </div>
      <Separator />
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 p-4 text-xs">
        <ResultValue label={t("resultPanel.http")} value={`${result.http_status}`} numeric />
        <ResultValue label={t("resultPanel.e2e")} value={`${formatNumber(result.e2e_ms)} ms`} numeric />
        <ResultValue label="Prompt / Completion / Cached" value={`${result.prompt_tokens} / ${result.completion_tokens} / ${result.cached_tokens}`} numeric />
        <ResultValue label={t("resultPanel.mode")} value={result.address_mode === "base_url" ? "Base URL" : t("fullUrl")} />
        <ResultValue label={t("resultPanel.endpoint")} value={result.endpoint} wide mono />
      </dl>
      {result.success ? (
        <div className="flex flex-wrap items-center gap-2 border-t p-4">
          {catalogChannelSelected ? (
            <>
              <span className="text-xs text-success">{t("resultPanel.savedChannel")}</span>
              <Button size="sm" variant="outline" onClick={onOpenCatalog}>
                {t("resultPanel.openCatalog")}<ArrowRightIcon data-icon="inline-end" />
              </Button>
            </>
          ) : saved ? (
            <>
              <span className="text-xs text-success">{t("resultPanel.saved")}</span>
              <Button size="sm" variant="outline" onClick={onOpenCatalog}>
                {t("resultPanel.openCatalog")}<ArrowRightIcon data-icon="inline-end" />
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={onSave}>{t("resultPanel.save")}</Button>
          )}
          <Button size="sm" variant="outline" onClick={onPerformance}>
            <GaugeIcon data-icon="inline-start" />{t("resultPanel.performance")}
          </Button>
        </div>
      ) : null}
    </div>
  )
}

interface PerformanceForm {
  requestCount: number
  durationSeconds: number
  concurrency: number
  timeoutSeconds: number
  inputTokens: number
  outputTokens: number
}

const DEFAULT_PERFORMANCE_FORM: PerformanceForm = {
  requestCount: 10,
  durationSeconds: 0,
  concurrency: 1,
  timeoutSeconds: 60,
  inputTokens: 100,
  outputTokens: 100,
}

function QuickPerformanceSheet({ open, onOpenChange, testedCommand, run, onArchived, onOpenReport }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  testedCommand: QuickTestCommand
  run: (command: QuickPerformanceCommand, onProgress?: (progress: QuickPerformanceProgress) => void) => Promise<QuickPerformanceReport>
  onArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
}) {
  const { t } = useTranslation("quickTest")
  const [form, setForm] = useState<PerformanceForm>(DEFAULT_PERFORMANCE_FORM)
  const [pending, setPending] = useState(false)
  const [report, setReport] = useState<QuickPerformanceReport | null>(null)
  const [progress, setProgress] = useState<QuickPerformanceProgress | null>(null)
  const [error, setError] = useState("")

  const update = (key: keyof PerformanceForm, value: number) => {
    setForm((current) => ({ ...current, [key]: value }))
    setReport(null)
    setProgress(null)
    setError("")
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    if (!validPerformanceForm(form)) {
      setError(form.requestCount === 0 && form.durationSeconds === 0
        ? t("performance.needTarget")
        : t("performance.invalid"))
      return
    }
    setPending(true)
    setReport(null)
    setProgress(null)
    setError("")
    void run({
      address_mode: testedCommand.address_mode,
      url: testedCommand.url,
      api_key: testedCommand.api_key,
      ...(testedCommand.channel_id ? { channel_id: testedCommand.channel_id } : {}),
      model_id: testedCommand.model_id,
      request_count: form.requestCount,
      duration_ms: form.durationSeconds * 1_000,
      concurrency: form.concurrency,
      timeout_ms: form.timeoutSeconds * 1_000,
      input_tokens: form.inputTokens,
      output_tokens: form.outputTokens,
    }, setProgress)
      .then((nextReport) => {
        setReport(nextReport)
        if (nextReport.archived && nextReport.report_id) void onArchived?.(nextReport.report_id)
      })
      .catch((reason: unknown) => {
        setError(publicDesktopErrorMessage(reason, t("performance.error")))
      })
      .finally(() => setPending(false))
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle>{t("performance.title")}</SheetTitle>
          <SheetDescription>
            {t("performance.description")}
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4">
          <form id="quick-performance-form" onSubmit={submit} className="space-y-4 pb-4">
            <FieldGroup>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <PerformanceNumberField label={t("performance.requestCount")} value={form.requestCount} min={0} max={10_000} disabled={pending} onChange={(value) => update("requestCount", value)} />
                <PerformanceNumberField label={t("performance.duration")} value={form.durationSeconds} min={0} max={3_600} disabled={pending} onChange={(value) => update("durationSeconds", value)} />
                <PerformanceNumberField label={t("performance.concurrency")} value={form.concurrency} min={1} max={256} disabled={pending} onChange={(value) => update("concurrency", value)} />
                <PerformanceNumberField label={t("performance.timeout")} value={form.timeoutSeconds} min={1} max={600} disabled={pending} onChange={(value) => update("timeoutSeconds", value)} />
                <PerformanceNumberField label={t("performance.inputTokens")} value={form.inputTokens} min={1} max={200_000} disabled={pending} onChange={(value) => update("inputTokens", value)} />
                <PerformanceNumberField label={t("performance.outputTokens")} value={form.outputTokens} min={1} max={65_536} disabled={pending} onChange={(value) => update("outputTokens", value)} />
              </div>
              <FieldDescription>
                {t("performance.formHint")}
              </FieldDescription>
              {error ? <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">{error}</FieldError> : null}
            </FieldGroup>
          </form>
          {pending && progress ? (
            <QuickPerformanceProgressPanel progress={progress} requestCount={form.requestCount} />
          ) : pending ? (
            <div className="flex min-h-36 flex-col items-center justify-center rounded-lg border border-dashed text-center">
              <Spinner className="size-5" />
              <p className="mt-3 text-sm font-medium">{t("performance.running")}</p>
              <p className="mt-1 text-xs text-muted-foreground">{t("performance.runningHint")}</p>
            </div>
          ) : report ? <QuickPerformanceReportPanel report={report} onOpenReport={onOpenReport} /> : null}
        </ScrollArea>
        <SheetFooter>
          <Button type="submit" form="quick-performance-form" disabled={pending}>
            {pending ? <><Spinner data-icon="inline-start" />{t("testing")}</> : <><GaugeIcon data-icon="inline-start" />{t("performance.start")}</>}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function PerformanceNumberField({ label, value, min, max, disabled, onChange }: {
  label: string
  value: number
  min: number
  max: number
  disabled: boolean
  onChange: (value: number) => void
}) {
  const id = `quick-performance-${label}`
  return (
    <Field className="block min-w-0">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <FieldContent>
        <Input
          id={id}
          aria-label={label}
          type="number"
          min={min}
          max={max}
          step={1}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(Number(event.target.value))}
          required
        />
      </FieldContent>
    </Field>
  )
}

function QuickPerformanceReportPanel({ report, onOpenReport }: { report: QuickPerformanceReport; onOpenReport?: (reportID: string) => void | Promise<void> }) {
  const { t, i18n } = useTranslation("quickTest")
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned, (key) => t(key))
  const completedWithFailures = !report.success && !report.error_code && report.metrics.completed > 0
  const title = report.success
    ? t("performance.complete")
    : completedWithFailures
      ? t("performance.completeWithFailures")
      : t(`errorCode.${report.error_code ?? "request_failed"}`)
  return (
    <section aria-label={t("performance.reportAria")} className="rounded-lg border bg-surface-subtle">
      <div className="flex items-start gap-3 p-4">
        {report.success ? (
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-success" />
        ) : (
          <CircleAlertIcon className="mt-0.5 size-5 shrink-0 text-warning" />
        )}
        <div className="min-w-0">
          <h3 className="text-sm font-semibold">{title}</h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {t(report.success ? "performance.passedHint" : "performance.failedHint")}
          </p>
        </div>
      </div>
      <Separator />
      <div className="space-y-4 p-4">
        <MetricSection title={t("performance.summary")}>
          <ResultValue label={completion.label} value={completion.value} numeric />
          <ResultValue label={t("performance.success")} value={String(report.metrics.succeeded)} numeric />
          <ResultValue label={t("performance.failed")} value={String(report.metrics.failed)} numeric />
          <ResultValue label={t("performance.successRate")} value={`${formatNumber(report.metrics.success_rate_percent)}%`} numeric />
          <ResultValue label={t("performance.totalDuration")} value={`${formatNumber(report.progress.total_duration_ms)} ms`} numeric />
          <ResultValue label={t("performance.peak")} value={String(report.progress.peak_in_flight)} numeric />
        </MetricSection>
        <MetricSection title={t("performance.throughput")}>
          <ResultValue label={t("performance.requestRate")} value={`${formatNumber(report.metrics.request_qps)} req/s`} numeric />
          <ResultValue label="RPM" value={`${formatNumber(report.metrics.rpm)} RPM`} numeric />
          <ResultValue label="Input TPM" value={`${formatNumber(report.metrics.input_tpm)} TPM`} numeric />
          <ResultValue label="Output TPM" value={`${formatNumber(report.metrics.output_tpm)} TPM`} numeric />
          <ResultValue label="Total TPM" value={`${formatNumber(report.metrics.total_tpm)} TPM`} numeric />
          <ResultValue label={t("performance.generationRate")} value={`${formatNumber(report.metrics.generation_tps)} token/s`} numeric />
        </MetricSection>
        <PerformanceLatencyTable metrics={report.metrics} />
        <MetricSection title="Token">
          <ResultValue label="Prompt / Completion / Cached" value={`${report.metrics.prompt_tokens} / ${report.metrics.completion_tokens} / ${report.metrics.cached_tokens}`} numeric />
          <ResultValue label={t("performance.cacheRate")} value={`${formatNumber(report.metrics.cache_rate_percent)}%`} numeric />
          <ResultValue label={t("performance.timedOut")} value={String(report.metrics.timed_out)} numeric />
        </MetricSection>
        {report.samples.length ? <PerformanceCharts layout="stacked" samples={report.samples} percentiles={report.metrics} /> : null}
        {report.failures.length ? (
          <div>
            <h4 className="text-xs font-semibold">{t("performance.failures")}</h4>
            <ul className="mt-2 space-y-1 text-xs">
              {report.failures.map((failure) => (
                <li key={failure.error_code} className="flex items-center justify-between gap-4">
                  <span className="text-muted-foreground">{t(`errorCode.${failure.error_code}`)}</span>
                  <span className="tabular-nums">{failure.count}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {report.archived && report.report_id ? (
          <div className="flex items-center justify-between gap-3 border-t pt-3">
            <p className="text-[10px] text-muted-foreground">{t("performance.archived", { date: report.generated_at ? new Date(report.generated_at).toLocaleString(i18n.resolvedLanguage ?? i18n.language) : t("performance.completionTime") })}</p>
            <Button type="button" size="sm" variant="outline" onClick={() => void onOpenReport?.(report.report_id!)}>{t("performance.openReport")}</Button>
          </div>
        ) : report.archive_status === "failed" ? (
          <p role="status" className="border-t pt-3 text-[11px] text-warning">{t("performance.notArchived")}</p>
        ) : null}
      </div>
    </section>
  )
}

function QuickPerformanceProgressPanel({ progress, requestCount }: { progress: QuickPerformanceProgress; requestCount: number }) {
  const { t } = useTranslation("quickTest")
  const percentage = requestCount > 0 && progress.planned > 0
    ? Math.min(100, progress.completed / progress.planned * 100)
    : undefined
  const phaseLabel = performanceProgressPhaseLabel(progress.phase, (key) => t(key))
  const completion = performanceCompletion(requestCount, progress.completed, progress.planned, (key) => t(key))
  return (
    <section role="status" aria-label={t("performance.progressAria")} className="rounded-lg border bg-surface-subtle p-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Spinner className="size-4 shrink-0" />
          <span className="text-sm font-medium">{phaseLabel}</span>
        </div>
        <span className="text-xs tabular-nums text-muted-foreground">
          {percentage === undefined ? t("performance.durationMode") : `${formatNumber(percentage)}%`}
        </span>
      </div>
      <Progress
        className="mt-3 h-1.5"
        value={percentage}
        aria-label={t("performance.requestProgress")}
        aria-valuenow={percentage}
        aria-valuemin={percentage === undefined ? undefined : 0}
        aria-valuemax={percentage === undefined ? undefined : 100}
      />
      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4">
        <ResultValue label={completion.label} value={completion.value} numeric />
        <ResultValue label={t("performance.success")} value={String(progress.succeeded)} numeric />
        <ResultValue label={t("performance.failed")} value={String(progress.failed)} numeric />
        <ResultValue label={t("performance.inFlight")} value={String(progress.in_flight)} numeric />
      </dl>
    </section>
  )
}

function MetricSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold">{title}</h4>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs">{children}</dl>
    </div>
  )
}

function validPerformanceForm(form: PerformanceForm): boolean {
  return Number.isInteger(form.requestCount) && form.requestCount >= 0 && form.requestCount <= 10_000 &&
    Number.isInteger(form.durationSeconds) && form.durationSeconds >= 0 && form.durationSeconds <= 3_600 &&
    (form.requestCount > 0 || form.durationSeconds > 0) &&
    Number.isInteger(form.concurrency) && form.concurrency >= 1 && form.concurrency <= 256 &&
    Number.isInteger(form.timeoutSeconds) && form.timeoutSeconds >= 1 && form.timeoutSeconds <= 600 &&
    Number.isInteger(form.inputTokens) && form.inputTokens >= 1 && form.inputTokens <= 200_000 &&
    Number.isInteger(form.outputTokens) && form.outputTokens >= 1 && form.outputTokens <= 65_536
}

function ResultValue({ label, value, numeric = false, wide = false, mono = false }: {
  label: string
  value: string
  numeric?: boolean
  wide?: boolean
  mono?: boolean
}) {
  return (
    <div className={wide ? "col-span-2 min-w-0" : "min-w-0"}>
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className={`mt-0.5 truncate ${numeric ? "tabular-nums" : ""} ${mono ? "font-mono text-[11px]" : ""}`} title={value}>
        {value}
      </dd>
    </div>
  )
}

function SaveConnectionSheet({
  open,
  onOpenChange,
  result,
  apiKey,
  modelID,
  existingModel,
  save,
  refreshCatalog,
  onCatalogUpdated,
  onSaved,
  onOpenCatalog,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  result: QuickTestResult
  apiKey: string
  modelID: string
  existingModel?: QuickTestModelCandidate
  save: (command: SaveQuickTestConnectionCommand) => Promise<CatalogSnapshot>
  refreshCatalog: () => Promise<CatalogSnapshot>
  onCatalogUpdated: (catalog: CatalogSnapshot) => void
  onSaved: (catalog: CatalogSnapshot) => void
  onOpenCatalog: () => void
}) {
  const { t } = useTranslation("quickTest")
  const [modelName, setModelName] = useState(modelID)
  const [channelName, setChannelName] = useState(() => defaultChannelName(result.base_url, t("save.defaultChannel")))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [partialSave, setPartialSave] = useState(false)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    setPartialSave(false)
    void save({
      base_url: result.base_url,
      api_key: apiKey.trim(),
      model_id: modelID,
      model_name: modelName.trim(),
      channel_name: channelName.trim(),
      ...(existingModel ? { existing_model_id: existingModel.id } : {}),
    })
      .then(onSaved)
      .catch(async (reason: unknown) => {
        const partial = reason instanceof DesktopClientError &&
          reason.code === "quick_test_save_partial"
        setPartialSave(partial)
        setError(publicDesktopErrorMessage(reason, t("save.error")))
        if (partial) {
          try {
            onCatalogUpdated(await refreshCatalog())
          } catch {
            // Keep the partial-save guidance visible; navigation can retry the
            // authoritative catalog query without exposing internal details.
          }
        }
      })
      .finally(() => setPending(false))
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="sm:max-w-md">
        <SheetHeader>
          <SheetTitle>{t("save.title")}</SheetTitle>
          <SheetDescription>
            {existingModel
              ? t("save.reuse", { name: existingModel.name })
              : t("save.create")}
          </SheetDescription>
        </SheetHeader>
        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col px-4">
          <FieldGroup>
            <TextField label={t("save.modelName")} value={modelName} onChange={setModelName} disabled={!!existingModel} required />
            <TextField label={t("save.channelName")} value={channelName} onChange={setChannelName} required />
            <div className="rounded-lg border bg-surface-subtle p-3 text-xs">
              <dl className="space-y-2">
                <ResultValue label="Base URL" value={result.base_url} mono />
                <ResultValue label={t("save.upstreamId")} value={modelID} mono />
              </dl>
              <p className="mt-2 text-[11px] text-muted-foreground">{t("save.keyHidden")}</p>
            </div>
            {error ? (
              <div className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                <FieldError>{error}</FieldError>
                {partialSave ? (
                  <Button type="button" size="sm" variant="outline" className="mt-3" onClick={onOpenCatalog}>
                    {t("save.openCatalog")}
                  </Button>
                ) : null}
              </div>
            ) : null}
          </FieldGroup>
          <SheetFooter className="px-0">
            <Button type="submit" disabled={pending || !modelName.trim() || !channelName.trim()}>
              {pending ? <><Spinner data-icon="inline-start" />{t("save.saving")}</> : t("save.confirm")}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}

function defaultChannelName(baseURL: string, fallback = "Quick Test channel"): string {
  try {
    return new URL(baseURL).host
  } catch {
    return fallback
  }
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value)
}
