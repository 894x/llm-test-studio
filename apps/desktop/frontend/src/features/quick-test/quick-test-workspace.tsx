import { useState, type ComponentProps, type FormEvent } from "react"
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
import { Checkbox } from "@/components/ui/checkbox"
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
  QUICK_TEST_ERROR_MESSAGES,
  estimateQuickPerformanceOpenLoopRequestCap,
  updateQuickTestForm,
  type QuickPerformanceArrivalPattern,
  type QuickPerformanceCommand,
  type QuickPerformanceLoadMode,
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickPerformanceWorkloadMode,
  type QuickTestAddressMode,
  type QuickTestCommand,
  type QuickTestResult,
  type SaveQuickTestConnectionCommand,
} from "./data"
import {
  performanceCapacitySummary,
  performanceCompletion,
  performanceProgressPhaseLabel,
  performanceSLOStatusLabel,
} from "./performance-summary"
import { QuickPerformanceRequestAnalysis } from "./quick-performance-request-analysis"

type QuickTestActions = Pick<
  DesktopClient,
  "runQuickTest" | "runQuickPerformanceTest" | "saveQuickTestConnection"
>

const DEFAULT_PROMPT = "Reply with OK only."
const DEFAULT_TIMEOUT_MS = 30_000
const MAX_PERFORMANCE_REQUESTS = 10_000

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

type ConnectionField = "url" | "apiKey" | "modelID" | "prompt" | "timeout"
type ConnectionFieldErrors = Partial<Record<ConnectionField, string>>

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
  const [fieldErrors, setFieldErrors] = useState<ConnectionFieldErrors>({})
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
    const field = connectionFieldForCommandKey(key)
    if (field) setFieldErrors((current) => omitFieldError(current, field))
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
    setFieldErrors({})
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
    const nextFieldErrors = validateConnectionForm(command, Boolean(selectedChannel))
    if (Object.keys(nextFieldErrors).length > 0) {
      setFieldErrors(nextFieldErrors)
      setRequestError("")
      focusFormField(event.currentTarget, firstConnectionErrorField(nextFieldErrors))
      return
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
          publicDesktopErrorMessage(error, "快速测试暂不可用，请检查本地日志"),
        )
      })
      .finally(() => setPending(false))
  }

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col" aria-labelledby="quick-test-heading">
      <header className="flex shrink-0 items-start justify-between gap-4 border-b px-4 py-3">
        <div className="min-w-0">
          <h1 id="quick-test-heading" className="text-lg font-semibold tracking-tight">
            快速测试
          </h1>
          <p className="mt-1 text-[11px] text-muted-foreground">
            无需预先创建模型、渠道或计划，直接验证 OpenAI Chat 兼容接口。
          </p>
        </div>
        <Badge variant="outline" className="shrink-0">
          临时连接 · 不自动保存
        </Badge>
      </header>

      <ScrollArea className="min-h-0 flex-1">
        <div className="grid min-h-full min-w-0 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(340px,0.8fr)]">
          <section aria-labelledby="quick-test-form-heading" className="min-w-0 border-b p-4 lg:border-r lg:border-b-0">
            <div className="mb-4">
              <h2 id="quick-test-form-heading" className="text-sm font-semibold">
                连接信息
              </h2>
              <p className="mt-1 text-[11px] text-muted-foreground">
                API Key 仅用于本次测试；只有确认保存后才会交给 Core 凭据链路。
              </p>
            </div>
            <form onSubmit={submit} noValidate>
              <FieldGroup>
                <Field className="block">
                  <FieldLabel>从渠道填充</FieldLabel>
                  <FieldContent>
                    <Select value={form.channel_id ?? "manual"} onValueChange={selectChannel}>
                      <SelectTrigger aria-label="从渠道填充" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="manual">手动输入地址与 API Key</SelectItem>
                          {channelCandidates.map((channel) => (
                            <SelectItem key={channel.id} value={channel.id}>{channel.name}</SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {selectedChannel
                        ? `已填入 ${selectedChannel.name} 的地址；保存的 API Key 由 Core 安全读取。`
                        : channelCandidates.length
                          ? "可选择已启用渠道自动填充，也可以继续手动粘贴。"
                          : "当前没有可用于快速测试的已启用渠道，请手动输入。"}
                    </FieldDescription>
                  </FieldContent>
                </Field>
                <fieldset>
                  <legend className="mb-2 text-xs font-medium">地址模式</legend>
                  <RadioGroup
                    value={form.address_mode}
                    onValueChange={(value) => update("address_mode", value as QuickTestAddressMode)}
                    className="grid grid-cols-2 gap-2"
                  >
                    <ModeOption
                      id="quick-test-base-url"
                      value="base_url"
                      label="Base URL"
                      description="自动拼接 /chat/completions"
                    />
                    <ModeOption
                      id="quick-test-full-url"
                      value="full_url"
                      label="完整 URL"
                      description="按填写地址原样请求"
                    />
                  </RadioGroup>
                </fieldset>

                <TextField
                  id="quick-test-url"
                  label="接口地址"
                  value={form.url}
                  onChange={(value) => update("url", value)}
                  error={fieldErrors.url}
                  placeholder={form.address_mode === "base_url" ? "https://api.example.com/v1" : "https://api.example.com/v1/chat/completions"}
                  required
                />
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <TextField
                    id="quick-test-api-key"
                    label="API Key"
                    type="password"
                    autoComplete="new-password"
                    value={form.api_key}
                    onChange={(value) => update("api_key", value)}
                    error={fieldErrors.apiKey}
                    disabled={!!selectedChannel}
                    placeholder={selectedChannel ? `已使用 ${selectedChannel.name} 的保存凭据` : undefined}
                    required={!selectedChannel}
                  />
                  <ModelIDField
                    value={form.model_id}
                    onChange={(value) => update("model_id", value)}
                    optionNames={modelOptionNames}
                    existingModel={existingModel}
                    ambiguous={ambiguousModelName}
                    error={fieldErrors.modelID}
                  />
                </div>
                <Field className="block" data-invalid={fieldErrors.prompt ? true : undefined}>
                  <FieldLabel htmlFor="quick-test-prompt">测试消息</FieldLabel>
                  <FieldContent>
                    <Textarea
                      id="quick-test-prompt"
                      aria-label="测试消息"
                      aria-invalid={fieldErrors.prompt ? true : undefined}
                      aria-describedby={fieldErrors.prompt ? "quick-test-prompt-error" : undefined}
                      className="min-h-20 resize-y"
                      value={form.prompt}
                      onChange={(event) => update("prompt", event.target.value)}
                      required
                    />
                    {fieldErrors.prompt ? <FieldError id="quick-test-prompt-error">{fieldErrors.prompt}</FieldError> : null}
                  </FieldContent>
                </Field>
                <Field className="block max-w-52" data-invalid={fieldErrors.timeout ? true : undefined}>
                  <FieldLabel htmlFor="quick-test-timeout">超时（毫秒）</FieldLabel>
                  <FieldContent>
                    <Input
                      id="quick-test-timeout"
                      aria-label="超时（毫秒）"
                      aria-invalid={fieldErrors.timeout ? true : undefined}
                      aria-describedby={fieldErrors.timeout ? "quick-test-timeout-error" : undefined}
                      type="number"
                      min={1_000}
                      max={120_000}
                      step={1_000}
                      value={form.timeout_ms}
                      onChange={(event) => update("timeout_ms", Number(event.target.value))}
                      required
                    />
                    <FieldDescription>默认 30 秒，端到端计时由 Core 返回。</FieldDescription>
                    {fieldErrors.timeout ? <FieldError id="quick-test-timeout-error">{fieldErrors.timeout}</FieldError> : null}
                  </FieldContent>
                </Field>
                {requestError ? (
                  <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                    {requestError}
                  </FieldError>
                ) : null}
                <Button type="submit" className="w-fit min-w-24" disabled={pending}>
                  {pending ? (
                    <><Spinner data-icon="inline-start" />正在测试…</>
                  ) : (
                    <><PlugZapIcon data-icon="inline-start" />发送测试</>
                  )}
                </Button>
              </FieldGroup>
            </form>
          </section>

          <section aria-labelledby="quick-test-result-heading" className="min-w-0 p-4">
            <h2 id="quick-test-result-heading" className="text-sm font-semibold">测试结果</h2>
            <p className="mt-1 text-[11px] text-muted-foreground">
              仅展示稳定诊断、计时和计数，不展示响应正文或内部错误。
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
                  <p className="mt-3 text-sm font-medium">等待测试</p>
                  <p className="mt-1 max-w-xs text-xs text-muted-foreground">
                    填写连接信息并发送后，这里会显示连通性、HTTP 状态和耗时。
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

function TextField({ label, value, onChange, type = "text", error, ...props }: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: "text" | "password"
  error?: string
} & Omit<ComponentProps<typeof Input>, "value" | "onChange" | "type">) {
  const id = props.id ?? `quick-test-${label}`
  const errorID = `${id}-error`
  return (
    <Field className="block" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <FieldContent>
        <Input
          {...props}
          id={id}
          aria-label={label}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorID : props["aria-describedby"]}
          type={type}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
        {error ? <FieldError id={errorID}>{error}</FieldError> : null}
      </FieldContent>
    </Field>
  )
}

function ModelIDField({ value, onChange, optionNames, existingModel, ambiguous, error }: {
  value: string
  onChange: (value: string) => void
  optionNames: readonly string[]
  existingModel?: QuickTestModelCandidate
  ambiguous: boolean
  error?: string
}) {
  return (
    <Field className="block" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor="quick-test-model-id">模型 ID</FieldLabel>
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
            aria-label="模型 ID"
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? "quick-test-model-id-error" : undefined}
            placeholder="选择目录模型或手动输入"
            required
            triggerLabel="显示模型候选"
            triggerDisabled={optionNames.length === 0}
          />
          {optionNames.length > 0 ? (
            <AutocompleteContent>
              <AutocompleteEmpty>无匹配模型，可继续使用当前输入</AutocompleteEmpty>
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
            ? `已匹配目录模型 ${existingModel.name}，保存连接时将直接复用。`
            : ambiguous
              ? "目录中存在多个同名模型，无法自动复用；请手动输入唯一的上游模型 ID。"
              : optionNames.length
              ? "可选择已有 OpenAI 模型，也可以继续手动输入上游模型 ID。"
              : "当前目录没有 OpenAI 模型，可直接手动输入上游模型 ID。"}
        </FieldDescription>
        {error ? <FieldError id="quick-test-model-id-error">{error}</FieldError> : null}
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
  const title = result.success
    ? "连接成功"
    : QUICK_TEST_ERROR_MESSAGES[result.error_code ?? "request_failed"]
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
              {result.success ? "可用" : "未通过"}
            </Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {result.success ? "接口已完成一次有效模型请求。" : "请根据稳定分类检查地址、凭据或上游服务。"}
          </p>
        </div>
      </div>
      <Separator />
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 p-4 text-xs">
        <ResultValue label="HTTP 状态" value={String(result.http_status)} numeric />
        <ResultValue label="端到端耗时" value={`${formatNumber(result.e2e_ms)} ms`} numeric />
        <ResultValue label="Prompt / Completion / Cached" value={`${result.prompt_tokens} / ${result.completion_tokens} / ${result.cached_tokens}`} numeric />
        <ResultValue label="地址模式" value={result.address_mode === "base_url" ? "Base URL" : "完整 URL"} />
        <ResultValue label="实际端点" value={result.endpoint} wide mono />
      </dl>
      {result.success ? (
        <div className="flex flex-wrap items-center gap-2 border-t p-4">
          {catalogChannelSelected ? (
            <>
              <span className="text-xs text-success">当前连接来自已保存渠道。</span>
              <Button size="sm" variant="outline" onClick={onOpenCatalog}>
                打开模型与渠道<ArrowRightIcon data-icon="inline-end" />
              </Button>
            </>
          ) : saved ? (
            <>
              <span className="text-xs text-success">模型、渠道与映射已保存。</span>
              <Button size="sm" variant="outline" onClick={onOpenCatalog}>
                打开模型与渠道<ArrowRightIcon data-icon="inline-end" />
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={onSave}>保存为模型与渠道</Button>
          )}
          <Button size="sm" variant="outline" onClick={onPerformance}>
            <GaugeIcon data-icon="inline-start" />快速性能测试
          </Button>
        </div>
      ) : null}
    </div>
  )
}

interface PerformanceForm {
  loadMode: QuickPerformanceLoadMode
  arrivalPattern: QuickPerformanceArrivalPattern
  workloadMode: QuickPerformanceWorkloadMode
  requestCount: number
  durationSeconds: number
  concurrency: number
  ratePerSecond: number
  maxInFlight: number
  timeoutSeconds: number
  inputTokens: number
  outputTokens: number
  inputTokensStdDev: number
  outputTokensStdDev: number
  sharedPrefixTokens: number
  randomSeed: number
  warmupRequests: number
  rampDurationSeconds: number
  rampRequestCap: number
  sliceDurationSeconds: number
  sloTTFTMS: number
  sloTPOTMS: number
  sloE2EMS: number
  sloTargetPercent: number
  capacityEnabled: boolean
  fixedCapacityStart: number
  fixedCapacityStep: number
  openCapacityStart: number
  openCapacityStep: number
}

type PerformanceNumberFieldName = Exclude<keyof PerformanceForm, "loadMode" | "arrivalPattern" | "workloadMode" | "capacityEnabled">
type PerformanceFieldErrors = Partial<Record<PerformanceNumberFieldName, string>>

const PERFORMANCE_TARGET_ERRORS = {
  requestCount: "请输入大于 0 的请求数，或填写持续时间。",
  durationSeconds: "请输入大于 0 的持续时间，或填写请求数。",
} as const

const DEFAULT_PERFORMANCE_FORM: PerformanceForm = {
  loadMode: "fixed_concurrency",
  arrivalPattern: "constant",
  workloadMode: "fixed",
  requestCount: 10,
  durationSeconds: 0,
  concurrency: 1,
  ratePerSecond: 1,
  maxInFlight: 256,
  timeoutSeconds: 60,
  inputTokens: 100,
  outputTokens: 100,
  inputTokensStdDev: 10,
  outputTokensStdDev: 10,
  sharedPrefixTokens: 0,
  randomSeed: 1,
  warmupRequests: 0,
  rampDurationSeconds: 0,
  rampRequestCap: 1_000,
  sliceDurationSeconds: 0,
  sloTTFTMS: 0,
  sloTPOTMS: 0,
  sloE2EMS: 0,
  sloTargetPercent: 0,
  capacityEnabled: false,
  fixedCapacityStart: 1,
  fixedCapacityStep: 1,
  openCapacityStart: 1,
  openCapacityStep: 1,
}

function QuickPerformanceSheet({ open, onOpenChange, testedCommand, run, onArchived, onOpenReport }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  testedCommand: QuickTestCommand
  run: (command: QuickPerformanceCommand, onProgress?: (progress: QuickPerformanceProgress) => void) => Promise<QuickPerformanceReport>
  onArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
}) {
  const [form, setForm] = useState<PerformanceForm>(DEFAULT_PERFORMANCE_FORM)
  const [pending, setPending] = useState(false)
  const [report, setReport] = useState<QuickPerformanceReport | null>(null)
  const [progress, setProgress] = useState<QuickPerformanceProgress | null>(null)
  const [fieldErrors, setFieldErrors] = useState<PerformanceFieldErrors>({})
  const [operationError, setOperationError] = useState("")
  const budgetPreview = performanceRequestBudget(form)
  const capacityTargets = performanceCapacityTargets(form)

  const resetOutput = () => {
    setReport(null)
    setProgress(null)
    setOperationError("")
  }

  const update = (key: PerformanceNumberFieldName, value: number) => {
    setForm((current) => ({ ...current, [key]: value }))
    setFieldErrors((current) => {
      let next = omitFieldError(current, key)
      if ([
        "requestCount", "durationSeconds", "concurrency", "ratePerSecond", "warmupRequests",
        "rampDurationSeconds", "rampRequestCap", "sloTTFTMS", "sloTPOTMS", "sloE2EMS",
        "sloTargetPercent", "fixedCapacityStart", "fixedCapacityStep", "openCapacityStart", "openCapacityStep",
      ].includes(key)) {
        for (const field of [
          "requestCount", "durationSeconds", "concurrency", "ratePerSecond", "warmupRequests",
          "rampDurationSeconds", "rampRequestCap", "sloTTFTMS", "sloTPOTMS", "sloE2EMS",
          "sloTargetPercent", "fixedCapacityStart", "fixedCapacityStep", "openCapacityStart", "openCapacityStep",
        ] as const) {
          next = omitFieldError(next, field)
        }
      }
      return next
    })
    resetOutput()
  }

  const updateCapacityEnabled = (capacityEnabled: boolean) => {
    setForm((current) => ({ ...current, capacityEnabled }))
    setFieldErrors({})
    resetOutput()
  }

  const updateLoadMode = (loadMode: QuickPerformanceLoadMode) => {
    setForm((current) => ({ ...current, loadMode }))
    setFieldErrors((current) => {
      const next = loadMode === "fixed_concurrency"
        ? omitFieldError(omitFieldError(current, "ratePerSecond"), "maxInFlight")
        : omitFieldError(omitFieldError(current, "concurrency"), "rampRequestCap")
      const withoutBudgetErrors = omitFieldError(omitFieldError(next, "requestCount"), "warmupRequests")
      return loadMode === "fixed_concurrency" && form.workloadMode === "fixed"
        ? omitFieldError(withoutBudgetErrors, "randomSeed")
        : withoutBudgetErrors
    })
    resetOutput()
  }

  const updateArrivalPattern = (arrivalPattern: QuickPerformanceArrivalPattern) => {
    setForm((current) => ({ ...current, arrivalPattern }))
    setFieldErrors((current) => {
      const next = omitFieldError(current, "ratePerSecond")
      return arrivalPattern === "constant" && form.workloadMode === "fixed"
        ? omitFieldError(next, "randomSeed")
        : next
    })
    resetOutput()
  }

  const updateWorkloadMode = (workloadMode: QuickPerformanceWorkloadMode) => {
    setForm((current) => ({ ...current, workloadMode }))
    if (workloadMode === "fixed") {
      setFieldErrors((current) => {
        let next = omitFieldError(omitFieldError(omitFieldError(current, "inputTokensStdDev"), "outputTokensStdDev"), "sharedPrefixTokens")
        if (form.loadMode !== "open_loop" || form.arrivalPattern !== "poisson") next = omitFieldError(next, "randomSeed")
        return next
      })
    }
    resetOutput()
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    const nextFieldErrors = validatePerformanceForm(form)
    if (Object.keys(nextFieldErrors).length > 0) {
      setFieldErrors(nextFieldErrors)
      setOperationError("")
      focusFormField(event.currentTarget, firstPerformanceErrorField(nextFieldErrors))
      return
    }
    setPending(true)
    setReport(null)
    setProgress(null)
    setFieldErrors({})
    setOperationError("")
    void run({
      address_mode: testedCommand.address_mode,
      url: testedCommand.url,
      api_key: testedCommand.api_key,
      ...(testedCommand.channel_id ? { channel_id: testedCommand.channel_id } : {}),
      model_id: testedCommand.model_id,
      load_mode: form.loadMode,
      request_count: form.requestCount,
      duration_ms: form.durationSeconds * 1_000,
      concurrency: form.loadMode === "fixed_concurrency" ? form.concurrency : 0,
      rate_per_second: form.loadMode === "open_loop" ? form.ratePerSecond : 0,
      max_in_flight: form.loadMode === "open_loop" ? form.maxInFlight : 0,
      arrival_pattern: form.loadMode === "open_loop" ? form.arrivalPattern : "constant",
      workload_mode: form.workloadMode,
      random_seed: performanceNeedsSeed(form) ? form.randomSeed : 0,
      input_tokens_stddev: form.workloadMode === "normal" ? form.inputTokensStdDev : 0,
      output_tokens_stddev: form.workloadMode === "normal" ? form.outputTokensStdDev : 0,
      shared_prefix_tokens: form.workloadMode === "normal" ? form.sharedPrefixTokens : 0,
      warmup_requests: form.warmupRequests,
      ramp_duration_ms: form.capacityEnabled ? 0 : form.rampDurationSeconds * 1_000,
      ramp_request_cap: !form.capacityEnabled && form.loadMode === "fixed_concurrency" && form.rampDurationSeconds > 0 ? form.rampRequestCap : 0,
      slice_duration_ms: form.sliceDurationSeconds * 1_000,
      slo_ttft_ms: form.sloTTFTMS,
      slo_tpot_ms: form.sloTPOTMS,
      slo_e2e_ms: form.sloE2EMS,
      slo_target_percent: form.sloTargetPercent,
      capacity_enabled: form.capacityEnabled,
      capacity_start: form.capacityEnabled
        ? form.loadMode === "fixed_concurrency" ? form.fixedCapacityStart : form.openCapacityStart
        : 0,
      capacity_step: form.capacityEnabled
        ? form.loadMode === "fixed_concurrency" ? form.fixedCapacityStep : form.openCapacityStep
        : 0,
      timeout_ms: form.timeoutSeconds * 1_000,
      input_tokens: form.inputTokens,
      output_tokens: form.outputTokens,
    }, setProgress)
      .then((nextReport) => {
        setReport(nextReport)
        if (nextReport.archived && nextReport.report_id) void onArchived?.(nextReport.report_id)
      })
      .catch((reason: unknown) => {
        setOperationError(publicDesktopErrorMessage(reason, "快速性能测试暂不可用，请检查本地日志"))
      })
      .finally(() => setPending(false))
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-5xl">
        <SheetHeader>
          <SheetTitle>快速性能测试</SheetTitle>
          <SheetDescription>
            使用刚刚验证成功的临时连接运行流式负载；实际发起请求后会自动封存报告，但不会创建模型、渠道、用例或计划。
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4">
          <form id="quick-performance-form" onSubmit={submit} className="space-y-4 pb-4" noValidate>
            <FieldGroup>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <Field className="block min-w-0">
                  <FieldLabel htmlFor="quick-performance-loadMode">负载模式</FieldLabel>
                  <FieldContent>
                    <Select value={form.loadMode} disabled={pending} onValueChange={(value) => updateLoadMode(value as QuickPerformanceLoadMode)}>
                      <SelectTrigger id="quick-performance-loadMode" aria-label="负载模式" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="fixed_concurrency">固定并发</SelectItem>
                          <SelectItem value="open_loop">开放到达（RPS）</SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                {form.loadMode === "open_loop" ? (
                  <Field className="block min-w-0">
                    <FieldLabel htmlFor="quick-performance-arrivalPattern">到达分布</FieldLabel>
                    <FieldContent>
                      <Select value={form.arrivalPattern} disabled={pending} onValueChange={(value) => updateArrivalPattern(value as QuickPerformanceArrivalPattern)}>
                        <SelectTrigger id="quick-performance-arrivalPattern" aria-label="到达分布" className="w-full">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value="constant">恒定间隔</SelectItem>
                            <SelectItem value="poisson">Poisson 到达</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </FieldContent>
                  </Field>
                ) : null}
                <Field className="block min-w-0">
                  <FieldLabel htmlFor="quick-performance-workloadMode">工作负载</FieldLabel>
                  <FieldContent>
                    <Select value={form.workloadMode} disabled={pending} onValueChange={(value) => updateWorkloadMode(value as QuickPerformanceWorkloadMode)}>
                      <SelectTrigger id="quick-performance-workloadMode" aria-label="工作负载" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="fixed">固定 Token</SelectItem>
                          <SelectItem value="normal">正态分布</SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                <PerformanceNumberField field="requestCount" label="请求数" value={form.requestCount} min={0} max={10_000} disabled={pending} error={fieldErrors.requestCount} onChange={(value) => update("requestCount", value)} />
                <PerformanceNumberField field="durationSeconds" label="持续时间（秒）" value={form.durationSeconds} min={0} max={3_600} disabled={pending} error={fieldErrors.durationSeconds} onChange={(value) => update("durationSeconds", value)} />
                {form.loadMode === "fixed_concurrency" ? (
                  <PerformanceNumberField field="concurrency" label={form.capacityEnabled ? "终止并发" : "并发数"} value={form.concurrency} min={1} max={256} disabled={pending} error={fieldErrors.concurrency} onChange={(value) => update("concurrency", value)} />
                ) : (
                  <>
                    <PerformanceNumberField field="ratePerSecond" label={form.capacityEnabled ? "终止 RPS" : "目标发送 RPS"} value={form.ratePerSecond} min={0.01} max={100_000} step={0.01} disabled={pending} error={fieldErrors.ratePerSecond} onChange={(value) => update("ratePerSecond", value)} />
                    <PerformanceNumberField field="maxInFlight" label="最大在途" value={form.maxInFlight} min={1} max={2_000} disabled={pending} error={fieldErrors.maxInFlight} onChange={(value) => update("maxInFlight", value)} />
                  </>
                )}
                <PerformanceNumberField field="timeoutSeconds" label="单请求超时（秒）" value={form.timeoutSeconds} min={1} max={600} disabled={pending} error={fieldErrors.timeoutSeconds} onChange={(value) => update("timeoutSeconds", value)} />
                <PerformanceNumberField field="inputTokens" label={form.workloadMode === "normal" ? "近似输入 Token 均值" : "近似输入 Token"} value={form.inputTokens} min={1} max={1_000_000} disabled={pending} error={fieldErrors.inputTokens} onChange={(value) => update("inputTokens", value)} />
                <PerformanceNumberField field="outputTokens" label={form.workloadMode === "normal" ? "最大输出 Token 均值" : "最大输出 Token"} value={form.outputTokens} min={1} max={65_536} disabled={pending} error={fieldErrors.outputTokens} onChange={(value) => update("outputTokens", value)} />
                {form.workloadMode === "normal" ? (
                  <>
                    <PerformanceNumberField field="inputTokensStdDev" label="输入 Token 标准差" value={form.inputTokensStdDev} min={0} max={1_000_000} disabled={pending} error={fieldErrors.inputTokensStdDev} onChange={(value) => update("inputTokensStdDev", value)} />
                    <PerformanceNumberField field="outputTokensStdDev" label="输出 Token 标准差" value={form.outputTokensStdDev} min={0} max={65_536} disabled={pending} error={fieldErrors.outputTokensStdDev} onChange={(value) => update("outputTokensStdDev", value)} />
                    <PerformanceNumberField field="sharedPrefixTokens" label="共享前缀 Token" value={form.sharedPrefixTokens} min={0} max={999_999} disabled={pending} error={fieldErrors.sharedPrefixTokens} onChange={(value) => update("sharedPrefixTokens", value)} />
                  </>
                ) : null}
                {performanceNeedsSeed(form) ? (
                  <PerformanceNumberField field="randomSeed" label="随机种子" value={form.randomSeed} min={1} max={4_294_967_295} disabled={pending} error={fieldErrors.randomSeed} onChange={(value) => update("randomSeed", value)} />
                ) : null}
              </div>
              <div className="rounded-lg border bg-surface-subtle p-3">
                <div className="mb-3">
                  <h3 className="text-xs font-semibold">SLO 与容量</h3>
                  <p className="mt-1 text-[10px] text-muted-foreground">0 表示不启用；达标请求必须同时通过传输校验和所有已启用的延迟阈值。</p>
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <PerformanceNumberField field="sloTTFTMS" label="SLO TTFT（ms）" value={form.sloTTFTMS} min={0} max={Number.MAX_VALUE} step={0.01} disabled={pending} error={fieldErrors.sloTTFTMS} onChange={(value) => update("sloTTFTMS", value)} />
                  <PerformanceNumberField field="sloTPOTMS" label="SLO TPOT（ms/token）" value={form.sloTPOTMS} min={0} max={Number.MAX_VALUE} step={0.01} disabled={pending} error={fieldErrors.sloTPOTMS} onChange={(value) => update("sloTPOTMS", value)} />
                  <PerformanceNumberField field="sloE2EMS" label="SLO E2E（ms）" value={form.sloE2EMS} min={0} max={Number.MAX_VALUE} step={0.01} disabled={pending} error={fieldErrors.sloE2EMS} onChange={(value) => update("sloE2EMS", value)} />
                  <PerformanceNumberField field="sloTargetPercent" label="SLO 目标达标率（%）" value={form.sloTargetPercent} min={0} max={100} step={0.01} disabled={pending} error={fieldErrors.sloTargetPercent} onChange={(value) => update("sloTargetPercent", value)} />
                </div>
                <Field className="mt-3 gap-2">
                  <FieldLabel htmlFor="quick-performance-capacityEnabled" className="flex min-h-8 cursor-pointer items-center gap-2 rounded-md border bg-background px-3 py-1.5">
                    <Checkbox
                      id="quick-performance-capacityEnabled"
                      aria-label="容量阶梯"
                      checked={form.capacityEnabled}
                      disabled={pending}
                      onCheckedChange={(checked) => updateCapacityEnabled(checked === true)}
                    />
                    <span>容量阶梯</span>
                    <span className="ml-auto text-[10px] font-normal text-muted-foreground">逐档执行，首个未达标后停止</span>
                  </FieldLabel>
                </Field>
                {form.capacityEnabled ? (
                  <div className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_2fr]">
                    {form.loadMode === "fixed_concurrency" ? (
                      <>
                        <PerformanceNumberField field="fixedCapacityStart" label="起始并发" value={form.fixedCapacityStart} min={1} max={256} disabled={pending} error={fieldErrors.fixedCapacityStart} onChange={(value) => update("fixedCapacityStart", value)} />
                        <PerformanceNumberField field="fixedCapacityStep" label="并发步长" value={form.fixedCapacityStep} min={1} max={Number.MAX_SAFE_INTEGER} disabled={pending} error={fieldErrors.fixedCapacityStep} onChange={(value) => update("fixedCapacityStep", value)} />
                      </>
                    ) : (
                      <>
                        <PerformanceNumberField field="openCapacityStart" label="起始 RPS" value={form.openCapacityStart} min={0.01} max={100_000} step={0.01} disabled={pending} error={fieldErrors.openCapacityStart} onChange={(value) => update("openCapacityStart", value)} />
                        <PerformanceNumberField field="openCapacityStep" label="RPS 步长" value={form.openCapacityStep} min={0.01} max={Number.MAX_VALUE} step={0.01} disabled={pending} error={fieldErrors.openCapacityStep} onChange={(value) => update("openCapacityStep", value)} />
                      </>
                    )}
                    <div className="col-span-2 flex min-h-8 min-w-0 items-center rounded-md border bg-background px-3 text-[11px] tabular-nums text-muted-foreground sm:col-span-1">
                      <span className="truncate">
                        {capacityTargets?.length
                          ? `${capacityTargets.map(formatCapacityTarget).join(" → ")} · ${formatNumber(capacityTargets.length)} 档`
                          : "请填写有效的起始值、步长与终止目标"}
                      </span>
                    </div>
                  </div>
                ) : null}
              </div>
              <div className="rounded-lg border bg-surface-subtle p-3">
                <div className="mb-2 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <h3 className="text-xs font-semibold">阶段与采样</h3>
                  <p className="text-[10px] tabular-nums text-muted-foreground">
                    {form.capacityEnabled
                      ? <>预算：热身 {formatNumber(budgetPreview.warmupCap)} · 容量 {formatNumber(budgetPreview.measuredCap)} · 合计 {formatNumber(budgetPreview.totalCap)} / {formatNumber(MAX_PERFORMANCE_REQUESTS)}</>
                      : <>预算：热身 {formatNumber(budgetPreview.warmupCap)} · 爬坡 {formatNumber(budgetPreview.rampCap)} · 稳态 {formatNumber(budgetPreview.measuredCap)} · 合计 {formatNumber(budgetPreview.totalCap)} / {formatNumber(MAX_PERFORMANCE_REQUESTS)}</>}
                  </p>
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <PerformanceNumberField field="warmupRequests" label="热身请求数" value={form.warmupRequests} min={0} max={10_000} disabled={pending} error={fieldErrors.warmupRequests} onChange={(value) => update("warmupRequests", value)} />
                  <PerformanceNumberField field="rampDurationSeconds" label="爬坡时间（秒）" value={form.rampDurationSeconds} min={0} max={3_600} disabled={pending || form.capacityEnabled} error={fieldErrors.rampDurationSeconds} onChange={(value) => update("rampDurationSeconds", value)} />
                  {!form.capacityEnabled && form.loadMode === "fixed_concurrency" && form.rampDurationSeconds > 0 ? (
                    <PerformanceNumberField field="rampRequestCap" label="爬坡请求上限" value={form.rampRequestCap} min={1} max={10_000} disabled={pending} error={fieldErrors.rampRequestCap} onChange={(value) => update("rampRequestCap", value)} />
                  ) : null}
                  <PerformanceNumberField field="sliceDurationSeconds" label="时间切片（秒）" value={form.sliceDurationSeconds} min={0} max={3_600} disabled={pending} error={fieldErrors.sliceDurationSeconds} onChange={(value) => update("sliceDurationSeconds", value)} />
                </div>
                <p className="mt-2 text-[10px] text-muted-foreground">0 表示跳过对应阶段或不生成时间切片；{form.capacityEnabled ? "容量模式跳过爬坡，每档指标独立统计。" : "爬坡使用 10 阶线性阶梯，稳态指标不包含热身与爬坡。"}</p>
              </div>
              <FieldDescription>
                {form.loadMode === "open_loop"
                  ? form.arrivalPattern === "poisson"
                    ? "按可复现的 Poisson 到达过程调度请求；达到最大在途后会记录本地拒绝。"
                    : "按恒定间隔的目标 RPS 独立调度请求；达到最大在途后会记录本地拒绝。"
                  : "固定并发会在请求完成后补发，维持配置的在途请求数。"}
                {form.workloadMode === "normal" ? " 正态工作负载会用种子复现每个请求的 Token 目标与唯一后缀。" : ""}
                同时填写请求数和持续时间时，任一目标先达到即停止发送；输出 Token 是请求上限，不保证模型实际生成到该数值。
              </FieldDescription>
              {operationError ? <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">{operationError}</FieldError> : null}
            </FieldGroup>
          </form>
          {pending && progress ? (
            <QuickPerformanceProgressPanel progress={progress} requestCount={form.requestCount} loadMode={form.loadMode} />
          ) : pending ? (
            <div className="flex min-h-36 flex-col items-center justify-center rounded-lg border border-dashed text-center">
              <Spinner className="size-5" />
              <p className="mt-3 text-sm font-medium">性能测试运行中</p>
              <p className="mt-1 text-xs text-muted-foreground">请求完成后由 Core 生成聚合报告。</p>
            </div>
          ) : report ? <QuickPerformanceReportPanel report={report} onOpenReport={onOpenReport} /> : null}
        </ScrollArea>
        <SheetFooter>
          <Button type="submit" form="quick-performance-form" disabled={pending}>
            {pending ? <><Spinner data-icon="inline-start" />正在测试…</> : <><GaugeIcon data-icon="inline-start" />开始性能测试</>}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function PerformanceNumberField({ field, label, value, min, max, step = 1, disabled, error, onChange }: {
  field: PerformanceNumberFieldName
  label: string
  value: number
  min: number
  max: number
  step?: number
  disabled: boolean
  error?: string
  onChange: (value: number) => void
}) {
  const id = `quick-performance-${field}`
  const errorID = `${id}-error`
  return (
    <Field className="block min-w-0" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <FieldContent>
        <Input
          id={id}
          aria-label={label}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorID : undefined}
          type="number"
          min={min}
          max={max}
          step={step}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(Number(event.target.value))}
          required
        />
        {error ? <FieldError id={errorID}>{error}</FieldError> : null}
      </FieldContent>
    </Field>
  )
}

function QuickPerformanceReportPanel({ report, onOpenReport }: { report: QuickPerformanceReport; onOpenReport?: (reportID: string) => void | Promise<void> }) {
  const completion = performanceCompletion(report.profile.request_count, report.metrics.completed, report.progress.planned)
  const hasPreparationData = report.request_budget !== undefined || report.warmup !== undefined || report.ramp !== undefined || report.time_slices !== undefined
  const completedWithFailures = !report.success && !report.error_code && report.metrics.completed > 0
  const title = report.success
    ? "性能测试完成"
    : completedWithFailures
      ? "性能测试完成，存在失败请求"
      : QUICK_TEST_ERROR_MESSAGES[report.error_code ?? "request_failed"]
  return (
    <section aria-label="性能报告" className="rounded-lg border bg-surface-subtle">
      <div className="flex items-start gap-3 p-4">
        {report.success ? (
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-success" />
        ) : (
          <CircleAlertIcon className="mt-0.5 size-5 shrink-0 text-warning" />
        )}
        <div className="min-w-0">
          <h3 className="text-sm font-semibold">{title}</h3>
          <div className="mt-2 flex flex-wrap gap-1.5">
            <Badge variant="outline" className={report.success ? "border-success/30 bg-success-soft text-success" : "border-warning/30 bg-warning-soft text-warning"}>
              传输与协议{report.success ? "通过" : "未通过"}
            </Badge>
            {report.slo_assessment ? (
              <Badge
                variant={report.slo_assessment.status === "failed" ? "destructive" : "outline"}
                className={report.slo_assessment.status === "passed" ? "border-success/30 bg-success-soft text-success" : report.slo_assessment.status === "not_evaluated" ? "border-warning/30 bg-warning-soft text-warning" : undefined}
              >
                {performanceSLOStatusLabel(report.slo_assessment.status)}
              </Badge>
            ) : null}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {report.success ? "稳态请求全部完成且通过协议与语义校验。" : "可按失败原因筛选稳态请求，并查看经过脱敏和限长处理的响应详情。"}
          </p>
        </div>
      </div>
      <Separator />
      <div className="space-y-4 p-4">
        <p className="rounded-md border bg-background/70 px-3 py-2 text-[11px] text-muted-foreground">主指标仅统计稳态阶段；热身与爬坡流量单独汇总。</p>
        {hasPreparationData ? (
          <div>
            <h4 className="mb-2 text-xs font-semibold">准备阶段</h4>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
              {report.request_budget ? <ResultValue label="请求预算" value={formatPerformanceBudget(report.request_budget)} numeric wide /> : null}
              {report.warmup ? <ResultValue label="热身" value={formatTrafficCompletion(report.warmup)} numeric /> : null}
              {report.ramp ? <ResultValue label="爬坡" value={formatTrafficCompletion(report.ramp.traffic)} numeric /> : null}
              {report.time_slices ? <ResultValue label="时间切片" value={`${formatNumber(report.time_slices.length)} 段 · ${formatDuration(report.profile.slice_duration_ms ?? 0)} 粒度`} numeric wide /> : null}
            </dl>
            {report.ramp && !report.ramp.completed_window ? <p role="status" className="mt-3 rounded-md border border-warning/25 bg-warning-soft px-3 py-2 text-[11px] text-warning">爬坡窗口未完整执行；请结合请求上限或提前停止状态解读准备阶段。</p> : null}
          </div>
        ) : null}
        {report.slo_assessment ? (
          <MetricSection title="SLO 与 Goodput">
            <InlineResultValue label="好请求" value={`${formatNumber(report.slo_assessment.good_requests)} / ${formatNumber(report.slo_assessment.total_requests)}`} numeric />
            <ResultValue label="达标率 / 目标" value={`${formatNumber(report.slo_assessment.good_request_percent)}% / ${formatNumber(report.slo_assessment.target_percent)}%`} numeric />
            <InlineResultValue label="Goodput" value={`${formatNumber(report.slo_assessment.goodput_qps)} req/s`} numeric />
            <ResultValue label="违反（传输 / TTFT / TPOT / E2E）" value={`${report.slo_assessment.violations.transport} / ${report.slo_assessment.violations.ttft} / ${report.slo_assessment.violations.tpot} / ${report.slo_assessment.violations.e2e}`} numeric wide />
          </MetricSection>
        ) : null}
        {report.capacity_result ? (
          <div className="rounded-md border bg-background/70 px-3 py-2">
            <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">容量结论</p>
            <p className="mt-1 text-xs font-semibold tabular-nums">{performanceCapacitySummary(report.capacity_result, report.profile.load_mode ?? "fixed_concurrency")}</p>
          </div>
        ) : null}
        <MetricSection title="稳态执行摘要">
          <ResultValue label={completion.label} value={completion.value} numeric />
          <ResultValue label="成功" value={String(report.metrics.succeeded)} numeric />
          <ResultValue label="失败" value={String(report.metrics.failed)} numeric />
          <ResultValue label="成功率" value={`${formatNumber(report.metrics.success_rate_percent)}%`} numeric />
          <ResultValue label="总耗时" value={`${formatNumber(report.progress.total_duration_ms)} ms`} numeric />
          {report.profile.load_mode === "open_loop" ? (
            <ResultValue label="峰值在途 / 上限" value={`${report.progress.peak_in_flight} / ${report.profile.max_in_flight === undefined ? "—" : formatNumber(report.profile.max_in_flight)}`} numeric />
          ) : (
            <ResultValue label="峰值在途 / 配置并发" value={`${report.progress.peak_in_flight} / ${formatCapacityTarget(report.progress.capacity_target ?? report.profile.concurrency)}`} numeric />
          )}
        </MetricSection>
        <MetricSection title="工作负载">
          {report.profile.load_mode === "open_loop" ? <ResultValue label="到达分布" value={performanceArrivalPattern(report)} /> : null}
          <ResultValue label="Token 分布" value={performanceWorkloadMode(report)} />
          <ResultValue label="随机种子" value={performanceSeed(report)} numeric />
          <ResultValue label="共享前缀" value={performanceSharedPrefix(report)} numeric />
          {performanceTargetRanges(report) ? <ResultValue label="采样目标范围（输入 / 输出）" value={performanceTargetRanges(report)!} numeric wide /> : null}
        </MetricSection>
        <MetricSection title="稳态吞吐">
          <ResultValue label="目标发送" value={performanceTargetRate(report)} numeric />
          <ResultValue label="调度需求" value={formatOptionalRate(report.metrics.offered_qps)} numeric />
          <ResultValue label="实际发送" value={formatOptionalRate(report.metrics.launched_qps)} numeric />
          <ResultValue label="已发送完成吞吐" value={formatOptionalRate(report.metrics.completed_qps)} numeric />
          <ResultValue label="成功吞吐" value={formatOptionalRate(report.metrics.successful_request_qps)} numeric />
          {report.schema_version === 1 ? <ResultValue label="旧版请求吞吐" value={`${formatNumber(report.metrics.request_qps)} req/s`} numeric /> : null}
          <ResultValue label="RPM" value={`${formatNumber(report.metrics.rpm)} RPM`} numeric />
          <ResultValue label="输入 TPM" value={`${formatNumber(report.metrics.input_tpm)} TPM`} numeric />
          <ResultValue label="输出 TPM" value={`${formatNumber(report.metrics.output_tpm)} TPM`} numeric />
          <ResultValue label="总 TPM" value={`${formatNumber(report.metrics.total_tpm)} TPM`} numeric />
          <ResultValue label="生成速度" value={`${formatNumber(report.metrics.generation_tps)} token/s`} numeric />
        </MetricSection>
        <PerformanceLatencyTable metrics={report.metrics} />
        <MetricSection title="Token">
          <ResultValue label="Prompt / Completion / Cached" value={`${report.metrics.prompt_tokens} / ${report.metrics.completion_tokens} / ${report.metrics.cached_tokens}`} numeric />
          <ResultValue label="KV 缓存命中率" value={`${formatNumber(report.metrics.cache_rate_percent)}%`} numeric />
          <ResultValue label="超时请求" value={String(report.metrics.timed_out)} numeric />
        </MetricSection>
        {report.samples.length ? <PerformanceCharts layout="stacked" samples={report.samples} percentiles={report.metrics} /> : null}
        {report.failures.length ? (
          <div>
            <h4 className="text-xs font-semibold">失败分类</h4>
            <ul className="mt-2 space-y-1 text-xs">
              {report.failures.map((failure) => (
                <li key={failure.error_code} className="flex items-center justify-between gap-4">
                  <span className="text-muted-foreground">{QUICK_TEST_ERROR_MESSAGES[failure.error_code]}</span>
                  <span className="tabular-nums">{failure.count}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {report.samples.length ? <QuickPerformanceRequestAnalysis report={report} /> : null}
        {report.archived && report.report_id ? (
          <div className="flex items-center justify-between gap-3 border-t pt-3">
            <p className="text-[10px] text-muted-foreground">已于 {report.generated_at ? new Date(report.generated_at).toLocaleString("zh-CN") : "测试完成时"} 封存到报告。</p>
            <Button type="button" size="sm" variant="outline" onClick={() => void onOpenReport?.(report.report_id!)}>查看正式报告</Button>
          </div>
        ) : report.archive_status === "failed" ? (
          <p role="status" className="border-t pt-3 text-[11px] text-warning">性能结果已生成，但未能写入报告目录。</p>
        ) : null}
      </div>
    </section>
  )
}

function QuickPerformanceProgressPanel({ progress, requestCount, loadMode }: { progress: QuickPerformanceProgress; requestCount: number; loadMode: QuickPerformanceLoadMode }) {
  const isPreparation = progress.phase === "warming_up" || progress.phase === "ramping"
  const percentage = (requestCount > 0 || isPreparation) && progress.planned > 0
    ? Math.min(100, progress.completed / progress.planned * 100)
    : undefined
  const phaseLabel = performanceProgressPhaseLabel(progress.phase)
  const completion = progress.phase === "warming_up"
    ? { label: "热身完成 / 计划", value: `${progress.completed} / ${progress.planned}` }
    : progress.phase === "ramping"
      ? { label: "爬坡完成 / 计划", value: `${progress.completed} / ${progress.planned}` }
      : performanceCompletion(requestCount, progress.completed, progress.planned)
  return (
    <section role="status" aria-label="性能测试进度" className="rounded-lg border bg-surface-subtle p-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Spinner className="size-4 shrink-0" />
          <span className="text-sm font-medium">{phaseLabel}</span>
          {progress.capacity_rung_number !== undefined ? (
            <Badge variant="outline" className="tabular-nums">档位 {progress.capacity_rung_number} / {progress.capacity_rung_count}</Badge>
          ) : null}
        </div>
        <span className="text-xs tabular-nums text-muted-foreground">
          {percentage === undefined ? "持续时间模式" : `${formatNumber(percentage)}%`}
        </span>
      </div>
      <Progress
        className="mt-3 h-1.5"
        value={percentage}
        aria-label="请求完成进度"
        aria-valuenow={percentage}
        aria-valuemin={percentage === undefined ? undefined : 0}
        aria-valuemax={percentage === undefined ? undefined : 100}
      />
      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4">
        <ResultValue label={completion.label} value={completion.value} numeric />
        {progress.offered === undefined ? null : <ResultValue label="调度需求" value={String(progress.offered)} numeric />}
        <ResultValue label="成功" value={String(progress.succeeded)} numeric />
        <ResultValue label="失败" value={String(progress.failed)} numeric />
        <ResultValue label="在途" value={String(progress.in_flight)} numeric />
        {progress.capacity_target === undefined ? null : <InlineResultValue label="当前目标" value={`${formatCapacityTarget(progress.capacity_target)} ${loadMode === "fixed_concurrency" ? "并发" : "RPS"}`} numeric />}
      </dl>
      {progress.capped ? <p className="mt-3 border-t pt-2 text-[11px] text-warning">当前阶段已达到请求上限。</p> : null}
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

function validatePerformanceForm(form: PerformanceForm): PerformanceFieldErrors {
  const errors: PerformanceFieldErrors = {}
  if (!integerInRange(form.requestCount, 0, 10_000)) errors.requestCount = "请求数需为 0–10,000 的整数。"
  if (!integerInRange(form.durationSeconds, 0, 3_600)) errors.durationSeconds = "持续时间需为 0–3,600 秒的整数。"
  if (!integerInRange(form.warmupRequests, 0, 10_000)) errors.warmupRequests = "热身请求数需为 0–10,000 的整数。"
  if (!form.capacityEnabled && !integerInRange(form.rampDurationSeconds, 0, 3_600)) errors.rampDurationSeconds = "爬坡时间需为 0–3,600 秒的整数。"
  if (!integerInRange(form.sliceDurationSeconds, 0, 3_600)) errors.sliceDurationSeconds = "时间切片需为 0–3,600 秒的整数。"
  if (!Number.isFinite(form.sloTTFTMS) || form.sloTTFTMS < 0) errors.sloTTFTMS = "SLO TTFT 阈值需为大于或等于 0 的有限数字。"
  if (!Number.isFinite(form.sloTPOTMS) || form.sloTPOTMS < 0) errors.sloTPOTMS = "SLO TPOT 阈值需为大于或等于 0 的有限数字。"
  if (!Number.isFinite(form.sloE2EMS) || form.sloE2EMS < 0) errors.sloE2EMS = "SLO E2E 阈值需为大于或等于 0 的有限数字。"
  if (!finiteInRange(form.sloTargetPercent, 0, 100)) errors.sloTargetPercent = "SLO 目标达标率需为 0–100。"
  const hasSLOThreshold = form.sloTTFTMS > 0 || form.sloTPOTMS > 0 || form.sloE2EMS > 0
  if (!errors.sloTargetPercent && form.sloTargetPercent > 0 && !hasSLOThreshold) {
    errors.sloTargetPercent = "设置目标达标率时，至少启用一个延迟阈值。"
  } else if (!errors.sloTargetPercent && hasSLOThreshold && form.sloTargetPercent <= 0) {
    errors.sloTargetPercent = "启用延迟阈值时，目标达标率必须大于 0。"
  }
  if (!errors.requestCount && !errors.durationSeconds && form.requestCount === 0 && form.durationSeconds === 0) {
    errors.requestCount = PERFORMANCE_TARGET_ERRORS.requestCount
    errors.durationSeconds = PERFORMANCE_TARGET_ERRORS.durationSeconds
  }
  if (form.loadMode === "fixed_concurrency") {
    if (!integerInRange(form.concurrency, 1, 256)) errors.concurrency = "并发数需为 1–256 的整数。"
    if (!form.capacityEnabled && form.rampDurationSeconds > 0 && !integerInRange(form.rampRequestCap, 1, 10_000)) errors.rampRequestCap = "爬坡请求上限需为 1–10,000 的整数。"
  } else {
    if (!finiteInRange(form.ratePerSecond, 0.01, 100_000)) errors.ratePerSecond = "目标发送 RPS 需为 0.01–100,000。"
    if (!integerInRange(form.maxInFlight, 1, 2_000)) errors.maxInFlight = "最大在途需为 1–2,000 的整数。"
  }
  if (!integerInRange(form.timeoutSeconds, 1, 600)) errors.timeoutSeconds = "单请求超时需为 1–600 秒的整数。"
  if (!integerInRange(form.inputTokens, 1, 1_000_000)) errors.inputTokens = "近似输入 Token 需为 1–1,000,000 的整数。"
  if (!integerInRange(form.outputTokens, 1, 65_536)) errors.outputTokens = "最大输出 Token 需为 1–65,536 的整数。"
  if (form.workloadMode === "normal") {
    if (!integerInRange(form.inputTokensStdDev, 0, 1_000_000)) errors.inputTokensStdDev = "输入 Token 标准差需为 0–1,000,000 的整数。"
    else if (!errors.inputTokens && form.inputTokensStdDev > form.inputTokens) errors.inputTokensStdDev = "输入 Token 标准差不能大于输入均值。"
    if (!integerInRange(form.outputTokensStdDev, 0, 65_536)) errors.outputTokensStdDev = "输出 Token 标准差需为 0–65,536 的整数。"
    else if (!errors.outputTokens && form.outputTokensStdDev > form.outputTokens) errors.outputTokensStdDev = "输出 Token 标准差不能大于输出均值。"
    if (!integerInRange(form.sharedPrefixTokens, 0, 999_999)) errors.sharedPrefixTokens = "共享前缀 Token 需为 0–999,999 的整数。"
    else if (!errors.inputTokens && form.sharedPrefixTokens >= form.inputTokens) errors.sharedPrefixTokens = "共享前缀 Token 必须小于输入均值。"
  }
  if (performanceNeedsSeed(form) && !integerInRange(form.randomSeed, 1, 4_294_967_295)) {
    errors.randomSeed = "随机种子需为 1–4,294,967,295 的整数。"
  }
  if (form.capacityEnabled) {
    if (!hasSLOThreshold || form.sloTargetPercent <= 0) {
      if (!errors.sloTargetPercent) errors.sloTargetPercent = "容量阶梯需要先配置有效的 SLO。"
    }
    if (form.requestCount <= 0 && !errors.requestCount) errors.requestCount = "容量阶梯需要大于 0 的请求数。"
    if (form.loadMode === "fixed_concurrency") {
      if (!integerInRange(form.fixedCapacityStart, 1, form.concurrency)) errors.fixedCapacityStart = "起始并发需为不超过终止并发的正整数。"
      if (!Number.isSafeInteger(form.fixedCapacityStep) || form.fixedCapacityStep <= 0) errors.fixedCapacityStep = "并发步长需为正整数。"
    } else {
      if (!finiteInRange(form.openCapacityStart, 0.01, form.ratePerSecond)) errors.openCapacityStart = "起始 RPS 需为 0.01 到终止 RPS。"
      if (!Number.isFinite(form.openCapacityStep) || form.openCapacityStep <= 0) errors.openCapacityStep = "RPS 步长需为大于 0 的有限数字。"
    }
    const targets = performanceCapacityTargets(form)
    if (targets && targets.length > 20) {
      errors[form.loadMode === "fixed_concurrency" ? "fixedCapacityStep" : "openCapacityStep"] = "容量阶梯最多支持 20 档；请增大步长或减小终止目标。"
    }
  }
  const canCalculateBudget = !errors.requestCount && !errors.durationSeconds && !errors.warmupRequests &&
    (!form.capacityEnabled || (
      !errors.fixedCapacityStart && !errors.fixedCapacityStep && !errors.openCapacityStart && !errors.openCapacityStep &&
      (performanceCapacityTargets(form)?.length ?? 21) <= 20
    )) &&
    (form.capacityEnabled || (!errors.rampDurationSeconds && !errors.rampRequestCap)) &&
    (form.loadMode === "fixed_concurrency" || !errors.ratePerSecond) &&
    (form.requestCount > 0 || form.durationSeconds > 0)
  if (canCalculateBudget) {
    const budget = performanceRequestBudget(form)
    if (!form.capacityEnabled && form.loadMode === "fixed_concurrency" && form.requestCount === 0 && budget.measuredCap < 1) {
      const field = form.rampDurationSeconds > 0 ? "rampRequestCap" : "warmupRequests"
      errors[field] = "热身与爬坡已用完 10,000 请求预算；持续时间稳态至少需要保留 1 个请求。"
    } else if (budget.totalCap > MAX_PERFORMANCE_REQUESTS) {
      if (form.capacityEnabled) {
        errors.requestCount = `热身 ${formatNumber(budget.warmupCap)} + 容量 ${formatNumber(budget.measuredCap)} = ${formatNumber(budget.totalCap)}，超过总请求预算 10,000。`
      } else if (form.loadMode === "open_loop" && form.requestCount === 0 && budget.warmupCap === 0 && budget.rampCap === 0) {
        errors.ratePerSecond = form.arrivalPattern === "poisson"
          ? `Poisson 到达需预留两倍调度余量；当前预计上限 ${formatNumber(budget.measuredCap)} 个请求，超过 10,000 个上限。`
          : `当前持续时间与 RPS 预计调度 ${formatNumber(budget.measuredCap)} 个请求，超过 10,000 个上限。`
      } else {
        const message = `热身 ${formatNumber(budget.warmupCap)} + 爬坡 ${formatNumber(budget.rampCap)} + 稳态 ${formatNumber(budget.measuredCap)} = ${formatNumber(budget.totalCap)}，超过总请求预算 10,000。`
        if (form.requestCount > 0) errors.requestCount = message
        else if (form.loadMode === "open_loop") errors.ratePerSecond = message
        else if (form.rampDurationSeconds > 0) errors.rampRequestCap = message
        else errors.warmupRequests = message
      }
    }
  }
  return errors
}

interface PerformanceRequestBudgetPreview {
  warmupCap: number
  rampCap: number
  measuredCap: number
  totalCap: number
}

function performanceRequestBudget(form: PerformanceForm): PerformanceRequestBudgetPreview {
  const warmupCap = nonNegativeFiniteOrZero(form.warmupRequests)
  const rampDurationSeconds = form.capacityEnabled ? 0 : nonNegativeFiniteOrZero(form.rampDurationSeconds)
  const rampCap = rampDurationSeconds === 0
    ? 0
    : form.loadMode === "fixed_concurrency"
      ? nonNegativeFiniteOrZero(form.rampRequestCap)
      : performanceOpenLoopRequestCap(rampDurationSeconds * nonNegativeFiniteOrZero(form.ratePerSecond) * 0.55, form.arrivalPattern)
  const capacityTargets = performanceCapacityTargets(form)
  const measuredCap = form.capacityEnabled && capacityTargets
    ? nonNegativeFiniteOrZero(form.requestCount) * capacityTargets.length
    : form.requestCount > 0
      ? nonNegativeFiniteOrZero(form.requestCount)
    : form.durationSeconds <= 0
      ? 0
      : form.loadMode === "fixed_concurrency"
        ? Math.max(0, MAX_PERFORMANCE_REQUESTS - warmupCap - rampCap)
        : estimateQuickPerformanceOpenLoopRequestCap(
          nonNegativeFiniteOrZero(form.durationSeconds) * 1_000,
          nonNegativeFiniteOrZero(form.ratePerSecond),
          form.arrivalPattern,
        )
  return { warmupCap, rampCap, measuredCap, totalCap: warmupCap + rampCap + measuredCap }
}

function performanceCapacityTargets(form: PerformanceForm): number[] | undefined {
  if (!form.capacityEnabled) return undefined
  const maximum = form.loadMode === "fixed_concurrency" ? form.concurrency : form.ratePerSecond
  const start = form.loadMode === "fixed_concurrency" ? form.fixedCapacityStart : form.openCapacityStart
  const step = form.loadMode === "fixed_concurrency" ? form.fixedCapacityStep : form.openCapacityStep
  if (!Number.isFinite(maximum) || !Number.isFinite(start) || !Number.isFinite(step) || maximum <= 0 || start <= 0 || step <= 0 || start > maximum) return undefined
  if (form.loadMode === "fixed_concurrency" && (!Number.isInteger(start) || !Number.isInteger(step))) return undefined
  if (form.loadMode === "open_loop" && start < 0.01) return undefined
  const targets: number[] = []
  for (let index = 0; index <= 20; index += 1) {
    const candidate = start + index * step
    if (!Number.isFinite(candidate) || candidate <= 0) return undefined
    if (candidate >= maximum) {
      targets.push(maximum)
      break
    }
    if (targets.length > 0 && candidate <= targets[targets.length - 1]) return undefined
    targets.push(candidate)
  }
  if (targets.length === 0 || targets[targets.length - 1] !== maximum) {
    // Preserve one extra sentinel rung so field validation can distinguish a
    // valid ladder that exceeds the supported twenty-rung UI/Core contract.
    if (targets.length === 21) return targets
    return undefined
  }
  return targets
}

function performanceOpenLoopRequestCap(intensity: number, arrivalPattern: QuickPerformanceArrivalPattern): number {
  return arrivalPattern === "poisson" ? Math.ceil(2 * intensity) + 1 : Math.ceil(intensity)
}

function nonNegativeFiniteOrZero(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0
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
  const [modelName, setModelName] = useState(modelID)
  const [channelName, setChannelName] = useState(() => defaultChannelName(result.base_url))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<"modelName" | "channelName", string>>>({})
  const [partialSave, setPartialSave] = useState(false)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    const nextFieldErrors = {
      ...(!modelName.trim() ? { modelName: "请输入模型名称。" } : {}),
      ...(!channelName.trim() ? { channelName: "请输入渠道名称。" } : {}),
    }
    if (Object.keys(nextFieldErrors).length > 0) {
      setFieldErrors(nextFieldErrors)
      setError("")
      focusFormField(event.currentTarget, nextFieldErrors.modelName ? "modelName" : "channelName")
      return
    }
    setPending(true)
    setError("")
    setFieldErrors({})
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
        setError(publicDesktopErrorMessage(reason, "连接保存失败，请检查本地日志"))
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
          <SheetTitle>保存连接</SheetTitle>
          <SheetDescription>
            {existingModel
              ? `将复用目录模型 ${existingModel.name}，并创建渠道与映射。`
              : "Core 将创建模型、渠道与映射；若名称或连接已存在，会拒绝保存以避免覆盖。"}
          </SheetDescription>
        </SheetHeader>
        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col px-4" noValidate>
          <FieldGroup>
            <TextField id="quick-save-modelName" label="模型名称" value={modelName} onChange={(value) => { setFieldErrors((current) => omitFieldError(current, "modelName")); setModelName(value) }} error={fieldErrors.modelName} disabled={!!existingModel} />
            <TextField id="quick-save-channelName" label="渠道名称" value={channelName} onChange={(value) => { setFieldErrors((current) => omitFieldError(current, "channelName")); setChannelName(value) }} error={fieldErrors.channelName} />
            <div className="rounded-lg border bg-surface-subtle p-3 text-xs">
              <dl className="space-y-2">
                <ResultValue label="Base URL" value={result.base_url} mono />
                <ResultValue label="上游模型 ID" value={modelID} mono />
              </dl>
              <p className="mt-2 text-[11px] text-muted-foreground">API Key 不会回显到此预览。</p>
            </div>
            {error ? (
              <div className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                <FieldError>{error}</FieldError>
                {partialSave ? (
                  <Button type="button" size="sm" variant="outline" className="mt-3" onClick={onOpenCatalog}>
                    打开模型与渠道
                  </Button>
                ) : null}
              </div>
            ) : null}
          </FieldGroup>
          <SheetFooter className="px-0">
            <Button type="submit" disabled={pending}>
              {pending ? <><Spinner data-icon="inline-start" />正在保存…</> : "确认保存"}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}

function defaultChannelName(baseURL: string): string {
  try {
    return new URL(baseURL).host
  } catch {
    return "快速测试渠道"
  }
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value)
}

function formatCapacityTarget(value: number): string {
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 10 }).format(value)
}

function InlineResultValue({ label, value, numeric = false }: { label: string; value: string; numeric?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="sr-only">{label}</dt>
      <dd className={`truncate ${numeric ? "tabular-nums" : ""}`} title={`${label} ${value}`}>
        {label} {value}
      </dd>
    </div>
  )
}

function formatDuration(valueMS: number): string {
  return valueMS >= 1_000 ? `${formatNumber(valueMS / 1_000)} s` : `${formatNumber(valueMS)} ms`
}

function formatPerformanceBudget(budget: NonNullable<QuickPerformanceReport["request_budget"]>): string {
  return `${formatNumber(budget.total_cap)} / ${formatNumber(budget.limit)}（热身 ${formatNumber(budget.warmup_cap)} · 爬坡 ${formatNumber(budget.ramp_cap)} · 稳态 ${formatNumber(budget.measured_cap)}）`
}

function formatTrafficCompletion(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  return `${formatNumber(traffic.completed)} / ${formatNumber(traffic.request_cap)} · 成功 ${formatNumber(traffic.succeeded)} · 失败 ${formatNumber(traffic.failed)} · 拒绝 ${formatNumber(traffic.rejected)}`
}

function formatOptionalRate(value: number | undefined): string {
  return value === undefined ? "—" : `${formatNumber(value)} req/s`
}

function performanceNeedsSeed(form: PerformanceForm): boolean {
  return form.workloadMode === "normal" || (form.loadMode === "open_loop" && form.arrivalPattern === "poisson")
}

function performanceArrivalPattern(report: QuickPerformanceReport): string {
  if (report.profile.arrival_pattern === "poisson") return "Poisson 到达"
  return report.profile.arrival_pattern === "constant" ? "恒定间隔" : "恒定间隔（旧报告）"
}

function performanceWorkloadMode(report: QuickPerformanceReport): string {
  if (report.profile.workload_mode === "normal") {
    return `正态分布（输入 σ ${formatNumber(report.profile.input_tokens_stddev ?? 0)} / 输出 σ ${formatNumber(report.profile.output_tokens_stddev ?? 0)}）`
  }
  return report.profile.workload_mode === "fixed" ? "固定 Token" : "固定 Token（旧报告）"
}

function performanceSeed(report: QuickPerformanceReport): string {
  if (report.profile.random_seed === undefined) return "—（旧报告）"
  return report.profile.random_seed > 0 ? formatNumber(report.profile.random_seed) : "—（未使用）"
}

function performanceSharedPrefix(report: QuickPerformanceReport): string {
  if (report.profile.shared_prefix_tokens === undefined) return "0 Token（旧报告）"
  return `${formatNumber(report.profile.shared_prefix_tokens)} Token`
}

function performanceTargetRanges(report: QuickPerformanceReport): string | undefined {
  const inputTargets = report.samples.flatMap((sample) => sample.target_input_tokens === undefined ? [] : [sample.target_input_tokens])
  const outputTargets = report.samples.flatMap((sample) => sample.target_output_tokens === undefined ? [] : [sample.target_output_tokens])
  if (inputTargets.length === 0 && outputTargets.length === 0) return undefined
  return `${formatIntegerRange(inputTargets)} / ${formatIntegerRange(outputTargets)}`
}

function formatIntegerRange(values: number[]): string {
  if (values.length === 0) return "—"
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  return minimum === maximum ? formatNumber(minimum) : `${formatNumber(minimum)}–${formatNumber(maximum)}`
}

function performanceTargetRate(report: QuickPerformanceReport): string {
  const target = report.progress.capacity_target ?? report.profile.rate_per_second
  return report.profile.load_mode === "open_loop" && target !== undefined
    ? `${formatCapacityTarget(target)} req/s`
    : "—（固定并发）"
}

function connectionFieldForCommandKey(key: keyof QuickTestCommand): ConnectionField | undefined {
  return ({
    url: "url",
    api_key: "apiKey",
    model_id: "modelID",
    prompt: "prompt",
    timeout_ms: "timeout",
  } as Partial<Record<keyof QuickTestCommand, ConnectionField>>)[key]
}

function validateConnectionForm(command: QuickTestCommand, usesStoredCredential: boolean): ConnectionFieldErrors {
  const errors: ConnectionFieldErrors = {}
  if (!command.url) {
    errors.url = "请输入接口地址。"
  } else {
    const urlHint = connectionURLHint(command.url, command.address_mode)
    if (urlHint) errors.url = urlHint
  }
  if (!usesStoredCredential && !command.api_key) errors.apiKey = "请输入 API Key，或选择已保存凭据的渠道。"
  if (!command.model_id) errors.modelID = "请输入模型 ID。"
  if (!command.prompt.trim()) errors.prompt = "请输入测试消息。"
  if (!integerInRange(command.timeout_ms, 1_000, 120_000)) errors.timeout = "超时需为 1,000–120,000 毫秒的整数。"
  return errors
}

function connectionURLHint(value: string, mode: QuickTestCommand["address_mode"]): string | undefined {
  if (value.trim() !== value) return "接口地址前后不能有空格。"
  if (value.includes("\\")) return "接口地址不能包含反斜杠。"
  try {
    const parsed = new URL(value)
    if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname) {
      return "请输入以 http:// 或 https:// 开头的有效地址。"
    }
    if (parsed.username || parsed.password || parsed.search || parsed.hash) {
      return "接口地址不能包含账号、密码、查询参数或片段。"
    }
    if (parsed.protocol === "http:" && !isLoopbackHost(parsed.hostname)) {
      return "远程接口必须使用 https://；http:// 仅适用于本机回环地址。"
    }
    if (mode === "full_url" && !value.replace(/\/+$/, "").endsWith("/chat/completions")) {
      return "完整 URL 必须以 /chat/completions 结尾。"
    }
    return undefined
  } catch {
    return "请输入以 http:// 或 https:// 开头的有效地址。"
  }
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/\.$/, "")
  return normalized === "localhost" || normalized === "::1" || normalized === "[::1]" || /^127(?:\.\d{1,3}){3}$/.test(normalized)
}

function integerInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isInteger(value) && value >= minimum && value <= maximum
}

function finiteInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isFinite(value) && value >= minimum && value <= maximum
}

function firstConnectionErrorField(errors: ConnectionFieldErrors): ConnectionField | undefined {
  return (["url", "apiKey", "modelID", "prompt", "timeout"] as const).find((field) => errors[field])
}

function firstPerformanceErrorField(errors: PerformanceFieldErrors): PerformanceNumberFieldName | undefined {
  return ([
    "requestCount", "durationSeconds", "concurrency", "ratePerSecond", "maxInFlight", "timeoutSeconds",
    "inputTokens", "outputTokens", "inputTokensStdDev", "outputTokensStdDev", "sharedPrefixTokens", "randomSeed",
    "sloTTFTMS", "sloTPOTMS", "sloE2EMS", "sloTargetPercent",
    "fixedCapacityStart", "fixedCapacityStep", "openCapacityStart", "openCapacityStep",
    "warmupRequests", "rampDurationSeconds", "rampRequestCap", "sliceDurationSeconds",
  ] as const)
    .find((field) => errors[field])
}

function omitFieldError<T extends object>(errors: T, field: PropertyKey): T {
  if (!(field in errors)) return errors
  const next = { ...errors }
  delete (next as Record<PropertyKey, unknown>)[field]
  return next
}

function focusFormField(form: Element, field: string | undefined) {
  if (!field) return
  const id = ({
    url: "quick-test-url",
    apiKey: "quick-test-api-key",
    modelID: "quick-test-model-id",
    prompt: "quick-test-prompt",
    timeout: "quick-test-timeout",
    modelName: "quick-save-modelName",
    channelName: "quick-save-channelName",
  } as Record<string, string>)[field] ?? `quick-performance-${field}`
  const control = document.getElementById(id)
  if (control instanceof HTMLElement && form.contains(control)) control.focus()
}
