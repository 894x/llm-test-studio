import { useState, type ComponentProps, type FormEvent } from "react"
import ArrowRightIcon from "lucide-react/dist/esm/icons/arrow-right.mjs"
import CheckCircle2Icon from "lucide-react/dist/esm/icons/check-circle-2.mjs"
import CircleAlertIcon from "lucide-react/dist/esm/icons/circle-alert.mjs"
import PlugZapIcon from "lucide-react/dist/esm/icons/plug-zap.mjs"

import {
  DesktopClientError,
  publicDesktopErrorMessage,
  type DesktopClient,
} from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
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
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { ScrollArea } from "@/components/ui/scroll-area"
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

import {
  QUICK_TEST_ERROR_MESSAGES,
  type QuickTestAddressMode,
  type QuickTestCommand,
  type QuickTestResult,
  type SaveQuickTestConnectionCommand,
} from "./data"

type QuickTestActions = Pick<
  DesktopClient,
  "runQuickTest" | "saveQuickTestConnection"
>

const DEFAULT_PROMPT = "Reply with OK only."
const DEFAULT_TIMEOUT_MS = 30_000

export interface QuickTestModelCandidate {
  id: string
  name: string
}

interface TestedQuickTest {
  command: QuickTestCommand
  result: QuickTestResult
  existingModel?: QuickTestModelCandidate
}

export function QuickTestWorkspace({
  modelCandidates,
  runQuickTest,
  saveQuickTestConnection,
  refreshCatalog,
  onCatalogUpdated,
  onOpenCatalog,
}: QuickTestActions & {
  modelCandidates: readonly QuickTestModelCandidate[]
  refreshCatalog: () => Promise<CatalogSnapshot>
  onCatalogUpdated: (catalog: CatalogSnapshot) => void
  onOpenCatalog: () => void
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
  const [saveOpen, setSaveOpen] = useState(false)
  const [saved, setSaved] = useState(false)
  const matchingModels = modelCandidates.filter(
    (candidate) => candidate.name === form.model_id.trim(),
  )
  const existingModel = matchingModels.length === 1 ? matchingModels[0] : undefined
  const ambiguousModelName = matchingModels.length > 1
  const modelOptionNames = [...new Set(modelCandidates.map((candidate) => candidate.name))]

  const update = <K extends keyof QuickTestCommand>(
    key: K,
    value: QuickTestCommand[K],
  ) => {
    setForm((current) => ({ ...current, [key]: value }))
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
            <form onSubmit={submit}>
              <FieldGroup>
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
                  label="接口地址"
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
                    required
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
                  <FieldLabel htmlFor="quick-test-prompt">测试消息</FieldLabel>
                  <FieldContent>
                    <Textarea
                      id="quick-test-prompt"
                      aria-label="测试消息"
                      className="min-h-20 resize-y"
                      value={form.prompt}
                      onChange={(event) => update("prompt", event.target.value)}
                      required
                    />
                  </FieldContent>
                </Field>
                <Field className="block max-w-52">
                  <FieldLabel htmlFor="quick-test-timeout">超时（毫秒）</FieldLabel>
                  <FieldContent>
                    <Input
                      id="quick-test-timeout"
                      aria-label="超时（毫秒）"
                      type="number"
                      min={1_000}
                      max={120_000}
                      step={1_000}
                      value={form.timeout_ms}
                      onChange={(event) => update("timeout_ms", Number(event.target.value))}
                      required
                    />
                    <FieldDescription>默认 30 秒，端到端计时由 Core 返回。</FieldDescription>
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
                  onSave={() => setSaveOpen(true)}
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
  return (
    <Field className="block">
      <FieldLabel htmlFor="quick-test-model-id">模型 ID</FieldLabel>
      <FieldContent>
        <Input
          id="quick-test-model-id"
          aria-label="模型 ID"
          list="quick-test-model-options"
          value={value}
          placeholder="选择目录模型或手动输入"
          required
          onChange={(event) => onChange(event.target.value)}
        />
        <datalist id="quick-test-model-options">
          {optionNames.map((name) => <option key={name} value={name} />)}
        </datalist>
        <FieldDescription>
          {existingModel
            ? `已匹配目录模型 ${existingModel.name}，保存连接时将直接复用。`
            : ambiguous
              ? "目录中存在多个同名模型，无法自动复用；请手动输入唯一的上游模型 ID。"
              : optionNames.length
              ? "可选择已有 OpenAI 模型，也可以继续手动输入上游模型 ID。"
              : "当前目录没有 OpenAI 模型，可直接手动输入上游模型 ID。"}
        </FieldDescription>
      </FieldContent>
    </Field>
  )
}

function ResultPanel({ result, saved, onSave, onOpenCatalog }: {
  result: QuickTestResult
  saved: boolean
  onSave: () => void
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
          {saved ? (
            <>
              <span className="text-xs text-success">模型、渠道与映射已保存。</span>
              <Button size="sm" variant="outline" onClick={onOpenCatalog}>
                打开模型与渠道<ArrowRightIcon data-icon="inline-end" />
              </Button>
            </>
          ) : (
            <Button size="sm" onClick={onSave}>保存为模型与渠道</Button>
          )}
        </div>
      ) : null}
    </div>
  )
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
        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col px-4">
          <FieldGroup>
            <TextField label="模型名称" value={modelName} onChange={setModelName} disabled={!!existingModel} required />
            <TextField label="渠道名称" value={channelName} onChange={setChannelName} required />
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
            <Button type="submit" disabled={pending || !modelName.trim() || !channelName.trim()}>
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
