import { createContext, useContext, useRef, useState, type FormEvent, type ReactNode } from "react"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"

import { publicDesktopOperationErrorMessage } from "@/app/desktop-client"
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Textarea } from "@/components/ui/textarea"
import { Spinner } from "@/components/ui/spinner"

import type {
  CatalogActions, CatalogChannel, CatalogChannelModel, CatalogLoadMode, CatalogModel,
  CatalogPlan, CatalogProtocol, CatalogSnapshot, CatalogSuite,
  CatalogTestCase, DeleteCommand,
} from "./data"

export type CatalogEntityKind = "model" | "channel" | "mapping" | "case" | "suite" | "plan"
export type CatalogEntity = CatalogModel | CatalogChannel | CatalogChannelModel | CatalogTestCase | CatalogSuite | CatalogPlan
export type CatalogMutation = (
  operation: () => Promise<CatalogSnapshot>,
  operationLabel: string,
) => Promise<void>

const TITLES: Record<CatalogEntityKind, string> = {
  model: "模型", channel: "渠道", mapping: "映射", case: "用例", suite: "套件", plan: "计划",
}

export function CatalogEditor({
  kind, item, catalog, actions, mutate, pending,
}: {
  kind: CatalogEntityKind
  item?: CatalogEntity
  catalog: CatalogSnapshot
  actions: CatalogActions
  mutate: CatalogMutation
  pending: boolean
}) {
  const [open, setOpen] = useState(false)
  const title = `${item ? "编辑" : "新增"}${TITLES[kind]}`
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm" variant={item ? "outline" : "default"} disabled={pending || (kind === "mapping" && (!catalog.models.length || !catalog.channels.length))}>
          {!item ? <PlusIcon data-icon="inline-start" /> : null}{title}
        </Button>
      </SheetTrigger>
      <SheetContent className="sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>保存后将刷新整个本地目录；编辑会检查当前版本，避免覆盖其他修改。</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4">
          <EditorForm kind={kind} item={item} catalog={catalog} actions={actions} mutate={mutate} pending={pending} formTitle={title} onSaved={() => setOpen(false)} />
        </ScrollArea>
      </SheetContent>
    </Sheet>
  )
}

export function DeleteCatalogButton({
  kind, item, action, mutate, pending,
}: {
  kind: CatalogEntityKind
  item?: { id: string; revision: number }
  action: (command: DeleteCommand) => Promise<CatalogSnapshot>
  mutate: CatalogMutation
  pending: boolean
}) {
  if (!item) return null
  const noun = TITLES[kind]
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild><Button size="sm" variant="destructive" disabled={pending}>删除{noun}</Button></AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>删除{noun}？</AlertDialogTitle>
          <AlertDialogDescription>此对象会从当前目录隐藏，历史版本与已停用的 ID 会保留。对象可能被渠道映射或测试计划引用，存在当前引用时后端会拒绝删除。</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction onClick={() => void mutate(() => action({ id: item.id, expected_revision: item.revision }), `删除${noun}`).catch(() => undefined)}>确认删除{noun}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function EditorForm(props: {
  kind: CatalogEntityKind; item?: CatalogEntity; catalog: CatalogSnapshot; actions: CatalogActions;
  mutate: CatalogMutation; pending: boolean; formTitle: string; onSaved: () => void
}) {
  if (props.kind === "model") return <ModelForm {...props} item={props.item as CatalogModel | undefined} />
  if (props.kind === "channel") return <ChannelForm {...props} item={props.item as CatalogChannel | undefined} />
  if (props.kind === "mapping") return <MappingForm {...props} item={props.item as CatalogChannelModel | undefined} />
  if (props.kind === "case") return <CaseForm {...props} item={props.item as CatalogTestCase | undefined} />
  if (props.kind === "suite") return <SuiteForm {...props} item={props.item as CatalogSuite | undefined} />
  return <PlanForm {...props} item={props.item as CatalogPlan | undefined} />
}

type FormProps<T> = {
  item?: T; catalog: CatalogSnapshot; actions: CatalogActions; mutate: CatalogMutation;
  pending: boolean; formTitle: string; onSaved: () => void
}

function ModelForm({ item, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogModel>) {
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [capabilities, setCapabilities] = useState(item?.capabilities.join(", ") ?? "")
  return <FormShell pending={pending} label="保存模型" formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, "模型名称"), protocol, capabilities: list(capabilities) }
    await mutate(() => item ? actions.updateModel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createModel(command), `${formTitle}保存`); onSaved()
  }}>
    <TextField label="模型名称" value={name} onChange={setName} />
    <SelectField label="协议" value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <TextField label="模型能力" value={capabilities} onChange={setCapabilities} description="使用逗号分隔，例如 chat, tools, vision。" />
  </FormShell>
}

function ChannelForm({ item, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannel>) {
  const [name, setName] = useState(item?.name ?? "")
  const [baseURL, setBaseURL] = useState(item?.base_url ?? "https://")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [enabled, setEnabled] = useState(item?.enabled ?? true)
  const [apiKey, setAPIKey] = useState("")
  return <FormShell pending={pending} label="保存渠道" formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, "渠道名称"), base_url: serviceURL(baseURL), api_key: required(apiKey, "API Key"), protocol, enabled }
    await mutate(() => item ? actions.updateChannel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createChannel(command), `${formTitle}保存`); onSaved()
  }}>
    <TextField label="渠道名称" value={name} onChange={setName} />
    <TextField label="服务地址" value={baseURL} onChange={setBaseURL} />
    <TextField label="API Key" type="password" value={apiKey} onChange={setAPIKey} description={item ? "保存为渠道新版本的独立凭据；历史计划继续使用旧版本。" : "仅写入系统密钥环，不会保存到数据库或前端快照。"} />
    <SelectField label="协议" value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <CheckField label="启用渠道" checked={enabled} onChange={setEnabled} />
    <FieldDescription>服务地址和 API Key 会作为同一个渠道配置一起保存。</FieldDescription>
  </FormShell>
}

function MappingForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannelModel>) {
  const [channelID, setChannelID] = useState(item?.channel_id ?? catalog.channels[0]?.id ?? "")
  const compatibleModels = catalog.models.filter((model) => model.protocol === catalog.channels.find((channel) => channel.id === channelID)?.protocol)
  const [modelID, setModelID] = useState(item?.model_id ?? compatibleModels[0]?.id ?? "")
  const [upstreamName, setUpstreamName] = useState(item?.upstream_model_name ?? "")
  return <FormShell pending={pending} label="保存映射" formTitle={formTitle} onSubmit={async () => {
    if (!channelID) throw new FormValidationError("渠道", "请选择渠道。")
    if (!modelID) throw new FormValidationError("逻辑模型", "请选择逻辑模型。")
    await mutate(() => item
      ? actions.updateChannelModel({ id: item.id, expected_revision: item.revision, upstream_model_name: required(upstreamName, "上游模型名称") })
      : actions.createChannelModel({ channel_id: channelID, model_id: modelID, upstream_model_name: required(upstreamName, "上游模型名称") }), `${formTitle}保存`)
    onSaved()
  }}>
    <SelectField label="渠道" value={channelID} disabled={!!item} options={catalog.channels.map((value) => [value.id, value.name])} onChange={(value) => { setChannelID(value); const protocol = catalog.channels.find((channel) => channel.id === value)?.protocol; setModelID(catalog.models.find((model) => model.protocol === protocol)?.id ?? "") }} />
    <SelectField label="逻辑模型" value={modelID} disabled={!!item} options={compatibleModels.map((value) => [value.id, value.name])} onChange={setModelID} />
    <TextField label="上游模型名称" value={upstreamName} onChange={setUpstreamName} />
  </FormShell>
}

function CaseForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogTestCase>) {
  const availableTypes = catalog.case_types.filter((descriptor) => descriptor.creatable || descriptor.type === item?.type)
  const initialDescriptor = catalog.case_types.find((descriptor) => descriptor.type === item?.type && descriptor.type_version === item.type_version)
    ?? availableTypes.find((descriptor) => descriptor.supported_protocols.includes(item?.protocol ?? "openai-chat"))
  const initialSpec = item?.spec ?? initialDescriptor?.default_spec ?? {}
  const [value, setValue] = useState(() => ({
    key: item?.key ?? "", name: item?.name ?? "", dimension: item?.dimension ?? "compatibility",
    protocol: item?.protocol ?? "openai-chat" as CatalogProtocol, enabled: item?.enabled ?? true,
    model_targets: item?.model_targets.join(", ") ?? "",
    default: item?.default ?? false, severity: item?.severity ?? "normal", execution_mode: item?.execution_mode ?? "automatic",
    type: item?.type ?? initialDescriptor?.type ?? "", type_version: item?.type_version ?? initialDescriptor?.type_version ?? 1,
    spec: json(initialSpec),
    stages: latencyStages(initialSpec.stages),
    warmups_per_step: finiteNumber(initialSpec.warmups_per_step, 1),
    samples_per_step: finiteNumber(initialSpec.samples_per_step, 3),
    output_tokens: finiteNumber(initialSpec.output_tokens, 16),
    timeout_ms: finiteNumber(initialSpec.timeout_ms, 600000),
    cache_mode: initialSpec.cache_mode === "warm" ? "warm" : "cold",
  }))
  const set = <K extends keyof typeof value>(key: K, next: (typeof value)[K]) => setValue((current) => ({ ...current, [key]: next }))
  const descriptor = catalog.case_types.find((candidate) => candidate.type === value.type && candidate.type_version === value.type_version)
  const typeOptions = availableTypes.filter((candidate) => candidate.supported_protocols.includes(value.protocol)).map((candidate) => [`${candidate.type}@${candidate.type_version}`, `${candidate.label} · v${candidate.type_version}`] as [string, string])
  const selectType = (key: string) => {
    const next = catalog.case_types.find((candidate) => `${candidate.type}@${candidate.type_version}` === key)
    if (!next) return
    const spec = next.default_spec
    setValue((current) => ({
      ...current, type: next.type, type_version: next.type_version, dimension: next.category, spec: json(spec),
      stages: latencyStages(spec.stages), warmups_per_step: finiteNumber(spec.warmups_per_step, 1),
      samples_per_step: finiteNumber(spec.samples_per_step, 3), output_tokens: finiteNumber(spec.output_tokens, 16),
      timeout_ms: finiteNumber(spec.timeout_ms, 600000), cache_mode: spec.cache_mode === "warm" ? "warm" : "cold",
    }))
  }
  const updateStage = (index: number, patch: Partial<LatencyStageDraft>) => setValue((current) => ({
    ...current,
    stages: current.stages.map((stage, stageIndex) => stageIndex === index ? { ...stage, ...patch } : stage),
  }))
  const addStage = () => setValue((current) => {
    const previous = current.stages.at(-1)?.input_tokens ?? 64
    return { ...current, stages: [...current.stages, { input_tokens: Math.min(previous * 2, 1_000_000), warmups: "", samples: "" }] }
  })
  const removeStage = (index: number) => setValue((current) => ({ ...current, stages: current.stages.filter((_, stageIndex) => stageIndex !== index) }))
  return <FormShell pending={pending} label="保存用例" formTitle={formTitle} onSubmit={async () => {
    if (!descriptor) throw new FormValidationError("用例类型", "请选择可用的用例类型。")
    if (value.type === "latency.input_ladder") {
      integerField(value.warmups_per_step, "默认每档预热次数", 0, 10)
      integerField(value.samples_per_step, "默认每档采样次数", 1, 100)
      integerField(value.output_tokens, "输出 Token 上限", 1, 65_536)
      integerField(value.timeout_ms, "单请求超时毫秒", 1, 600_000)
    }
    const spec = value.type === "latency.input_ladder"
      ? {
          ...recordJSON<unknown>(value.spec, "用例配置"),
          stages: latencyStageSpecs(value.stages), warmups_per_step: value.warmups_per_step,
          samples_per_step: value.samples_per_step, output_tokens: value.output_tokens,
          timeout_ms: value.timeout_ms, cache_mode: value.cache_mode,
        }
      : recordJSON<unknown>(value.spec, "用例配置")
    const modelTargets = list(value.model_targets)
    if (modelTargets.length > 32) throw new FormValidationError("适用模型", "最多填写 32 个模型 ID。")
    modelTargets.forEach((target) => safeModelTarget(target, "适用模型"))
    if (value.default && !value.enabled) throw new FormValidationError("默认启用", "默认用例必须同时启用。")
    const command = {
      key: safeCatalogKey(value.key, "用例键"), name: required(value.name, "用例名称"), dimension: required(value.dimension, "维度"),
      protocol: value.protocol, enabled: value.enabled, default: value.default, severity: value.severity as "normal" | "critical",
      model_targets: modelTargets,
      execution_mode: value.execution_mode as "automatic" | "manual", definition_schema_version: 2,
      type: value.type, type_version: value.type_version, spec,
    }
    await mutate(() => item ? actions.updateTestCase({ ...command, id: item.id, expected_revision: item.revision }) : actions.createTestCase(command), `${formTitle}保存`); onSaved()
  }}>
    <div className="grid grid-cols-2 gap-3"><TextField label="用例键" value={value.key} disabled={!!item} onChange={(v) => set("key", v)} /><TextField label="用例名称" value={value.name} onChange={(v) => set("name", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><TextField label="维度" value={value.dimension} onChange={(v) => set("dimension", v)} /><SelectField label="协议" value={value.protocol} disabled={!!item} options={protocolOptions} onChange={(v) => set("protocol", v as CatalogProtocol)} /></div>
    <TextField label="适用模型" value={value.model_targets} onChange={(v) => set("model_targets", v)} description="填写精确的上游模型 ID，多个用逗号分隔；留空表示适用于该协议下全部模型。" />
    <SelectField label="用例类型" value={`${value.type}@${value.type_version}`} options={typeOptions} onChange={selectType} />
    {descriptor ? <FieldDescription>{descriptor.category} · 调度由{descriptor.scheduling_owner === "case" ? "用例" : "计划"}负责 · {descriptor.type}@{descriptor.type_version}</FieldDescription> : null}
    <div className="grid grid-cols-2 gap-3"><SelectField label="严重度" value={value.severity} options={[["normal","普通"],["critical","关键"]]} onChange={(v) => set("severity", v as "normal" | "critical")} /><SelectField label="执行方式" value={value.execution_mode} options={[["automatic","自动"],["manual","人工"]]} onChange={(v) => set("execution_mode", v as "automatic" | "manual")} /></div>
    <div className="grid grid-cols-2 gap-3"><CheckField label="启用" checked={value.enabled} clearFields={["默认启用"]} onChange={(v) => set("enabled", v)} /><CheckField label="默认启用" checked={value.default} clearFields={["启用"]} onChange={(v) => set("default", v)} /></div>
    {value.type === "latency.input_ladder" ? <>
      <div className="grid grid-cols-2 gap-3"><NumberField label="默认每档预热次数" value={value.warmups_per_step} maximum={10} onChange={(v) => set("warmups_per_step", v)} /><NumberField label="默认每档采样次数" value={value.samples_per_step} minimum={1} maximum={100} onChange={(v) => set("samples_per_step", v)} /></div>
      <fieldset className="space-y-2 rounded-lg border p-3">
        <legend className="px-1 text-xs font-medium">输入 Token 阶梯</legend>
        <FieldDescription>预热或采样留空时继承上方默认值；填写后仅覆盖当前阶梯。</FieldDescription>
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] gap-2 px-1 text-[10px] text-muted-foreground" aria-hidden="true">
          <span>输入 Token</span><span>预热覆盖</span><span>采样覆盖</span><span className="w-12" />
        </div>
        {value.stages.map((stage, index) => <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] items-start gap-2">
          <StageNumberField label={`阶梯 ${index + 1} 输入 Token`} minimum={1} maximum={1_000_000} value={stage.input_tokens} onChange={(next) => updateStage(index, { input_tokens: Number(next) })} />
          <StageNumberField label={`阶梯 ${index + 1} 预热次数`} minimum={0} maximum={10} value={stage.warmups} placeholder={`继承 ${value.warmups_per_step}`} onChange={(next) => updateStage(index, { warmups: next })} />
          <StageNumberField label={`阶梯 ${index + 1} 采样次数`} minimum={1} maximum={100} value={stage.samples} placeholder={`继承 ${value.samples_per_step}`} onChange={(next) => updateStage(index, { samples: next })} />
          <Button type="button" size="sm" variant="ghost" className="w-12" disabled={value.stages.length === 1} onClick={() => removeStage(index)} aria-label={`删除阶梯 ${index + 1}`}>删除</Button>
        </div>)}
        <Button type="button" size="sm" variant="outline" disabled={value.stages.length >= 32 || (value.stages.at(-1)?.input_tokens ?? 0) >= 1_000_000} onClick={addStage}><PlusIcon data-icon="inline-start" />新增阶梯</Button>
      </fieldset>
      <div className="grid grid-cols-2 gap-3"><NumberField label="输出 Token 上限" value={value.output_tokens} minimum={1} maximum={65_536} onChange={(v) => set("output_tokens", v)} /><NumberField label="单请求超时毫秒" value={value.timeout_ms} minimum={1} maximum={600_000} onChange={(v) => set("timeout_ms", v)} /></div>
      <SelectField label="缓存模式" value={value.cache_mode} options={[["cold","冷缓存（每次变化探针）"],["warm","热缓存（复用探针）"]]} onChange={(v) => set("cache_mode", v as "cold" | "warm")} />
      <TextAreaField label="高级配置 JSON" value={value.spec} onChange={(v) => set("spec", v)} description="请求模板保存在这里；上方阶梯参数保存时会覆盖同名字段。" />
    </> : <TextAreaField label="用例配置 JSON" value={value.spec} onChange={(v) => set("spec", v)} description="配置结构由所选 type@version 定义并由后端校验。" />}
  </FormShell>
}

function SuiteForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogSuite>) {
  const [key, setKey] = useState(item?.key ?? "")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [modelTarget, setModelTarget] = useState(item?.model_target ?? "")
  const [selected, setSelected] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  const availableCases = catalog.test_cases.filter((testCase) =>
    testCase.protocol === protocol && (!modelTarget.trim() || testCase.model_targets.length === 0 || testCase.model_targets.includes(modelTarget.trim())),
  )
  return <FormShell pending={pending} label="保存套件" formTitle={formTitle} onSubmit={async () => {
    const pinned = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const validatedKey = safeCatalogKey(key, "套件标识")
    const validatedName = required(name, "套件名称")
    const validatedModelTarget = safeModelTarget(modelTarget, "目标模型")
    const cases = availableCases.filter((testCase) => selected.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinned.get(testCase.id) ?? testCase.revision }))
    if (cases.length === 0) throw new FormValidationError("包含用例", "请至少选择一个用例。")
    const command = {
      key: validatedKey, name: validatedName, protocol,
      model_target: validatedModelTarget,
      cases,
    }
    await mutate(() => item ? actions.updateSuite({ ...command, id: item.id, expected_revision: item.revision }) : actions.createSuite(command), `${formTitle}保存`); onSaved()
  }}>
    <TextField label="套件标识" value={key} onChange={setKey} disabled={!!item} description="用于 suite.json 的稳定标识，例如 gpt-5.2-smoke。" />
    <TextField label="套件名称" value={name} onChange={setName} />
    <SelectField label="协议" value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <TextField label="目标模型" value={modelTarget} onChange={setModelTarget} description="填写渠道实际调用的模型标识；每个套件只对应一个模型。" />
    <ChoiceList label="包含用例" values={availableCases.map((value) => ({ id: value.id, label: `${value.name} · r${value.revision}` }))} selected={selected} onChange={setSelected} />
  </FormShell>
}

function PlanForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogPlan>) {
  const [name, setName] = useState(item?.name ?? "")
  const [models, setModels] = useState(() => new Set(item?.model_ids ?? []))
  const [channels, setChannels] = useState(() => new Set(item?.channel_ids ?? []))
  const [cases, setCases] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  const [suiteID, setSuiteID] = useState(item?.suite_id ?? "none")
  const [loadMode, setLoadMode] = useState<CatalogLoadMode>(item?.load_mode ?? "single")
  const [numbers, setNumbers] = useState({ concurrency: item?.concurrency ?? 1, request_count: item?.request_count ?? 1, rate_per_second: item?.rate_per_second ?? 0, duration_ms: item?.duration_ms ?? 0, request_timeout_ms: item?.request_timeout_ms ?? 60000 })
  const [sla, setSla] = useState(json(item?.sla_thresholds ?? { e2e_p95_ms: 3000 }))
  return <FormShell pending={pending} label="保存计划" formTitle={formTitle} onSubmit={async () => {
		const validatedName = required(name, "计划名称")
		if ((models.size === 0) !== (channels.size === 0)) {
      throw new FormValidationError(models.size === 0 ? "模型" : "渠道", "模型和渠道限制必须同时留空或同时配置。")
    }
    const suite = catalog.suites.find((value) => value.id === suiteID)
    const pinnedCases = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const caseRefs = catalog.test_cases.filter((testCase) => cases.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinnedCases.get(testCase.id) ?? testCase.revision }))
    if (caseRefs.length === 0) throw new FormValidationError("直接用例", "请至少选择一个直接用例。")
    validatePlanLoad(loadMode, numbers)
    const command = {
      name: validatedName, model_ids: [...models], channel_ids: [...channels],
      suite_id: suite?.id, suite_revision: suite?.id === item?.suite_id ? item?.suite_revision : suite?.revision,
      cases: caseRefs,
      load_mode: loadMode, ...numbers, sla_thresholds: nonNegativeNumberRecord(sla, "SLA 阈值 JSON"),
    }
    await mutate(() => item ? actions.updatePlan({ ...command, id: item.id, expected_revision: item.revision }) : actions.createPlan(command), `${formTitle}保存`); onSaved()
  }}>
    <TextField label="计划名称" value={name} onChange={setName} />
    <ChoiceList label="模型" values={catalog.models.map(v => ({ id: v.id, label: v.name }))} selected={models} clearFields={["渠道"]} onChange={setModels} />
    <ChoiceList label="渠道" values={catalog.channels.map(v => ({ id: v.id, label: v.name }))} selected={channels} clearFields={["模型"]} onChange={setChannels} />
		<FieldDescription>模型和渠道均留空时，在每次运行开始前选择一个协议兼容、已映射的目标；一旦启动，具体修订会固定到运行快照。</FieldDescription>
    <ChoiceList label="直接用例" values={catalog.test_cases.map(v => ({ id: v.id, label: `${v.name} · r${v.revision}` }))} selected={cases} onChange={setCases} />
    <SelectField label="套件" value={suiteID} options={[["none","不使用套件"], ...catalog.suites.map(v => [v.id, `${v.name} · r${v.revision}`] as [string,string])]} onChange={setSuiteID} />
    <SelectField label="负载模式" value={loadMode} options={[["single","单次"],["fixed_concurrency","固定并发"],["open_loop","开放环"]]} onChange={(v) => setLoadMode(v as CatalogLoadMode)} />
    <div className="grid grid-cols-2 gap-3">
      {Object.entries({ concurrency: "并发数", request_count: "请求数", rate_per_second: "每秒请求数", duration_ms: "持续时间毫秒", request_timeout_ms: "单请求超时毫秒" }).map(([key, label]) => <NumberField key={key} label={label} value={numbers[key as keyof typeof numbers]} clearFields={label === "请求数" ? ["持续时间毫秒"] : label === "持续时间毫秒" ? ["请求数"] : undefined} onChange={(v) => setNumbers(current => ({ ...current, [key]: v }))} />)}
    </div>
    <TextAreaField label="SLA 阈值 JSON" value={sla} onChange={setSla} />
  </FormShell>
}

type CatalogValidationContextValue = {
  error: FormValidationError | null
  clear: (...fields: string[]) => void
}

const CatalogValidationContext = createContext<CatalogValidationContextValue | null>(null)

function FormShell({ children, label, pending, formTitle, onSubmit }: { children: ReactNode; label: string; pending: boolean; formTitle: string; onSubmit: () => Promise<void> }) {
  const formRef = useRef<HTMLFormElement>(null)
  const [validationError, setValidationError] = useState<FormValidationError | null>(null)
  const [operationError, setOperationError] = useState("")
  const submit = (event: FormEvent) => {
    event.preventDefault()
    setValidationError(null)
    setOperationError("")
    void onSubmit().catch((reason: unknown) => {
      if (reason instanceof FormValidationError) {
        setValidationError(reason)
        focusCatalogField(formRef.current, reason.field)
        return
      }
      setOperationError(publicDesktopOperationErrorMessage(reason, `${formTitle}保存`, "保存未完成，请检查本地日志"))
    })
  }
  const clear = (...fields: string[]) => setValidationError((current) => current && fields.includes(current.field) ? null : current)
  return <CatalogValidationContext.Provider value={{ error: validationError, clear }}>
    <form ref={formRef} className="pb-4" onSubmit={submit} noValidate>
      <FieldGroup>
        {children}
        {validationError ? (
          <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
            {formTitle}中“{validationError.field}”需要修改：{validationError.message}
          </FieldError>
        ) : null}
        {operationError ? <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">{operationError}</FieldError> : null}
      </FieldGroup>
      <SheetFooter className="px-0"><Button type="submit" className="min-w-24" disabled={pending}>{pending ? <><Spinner data-icon="inline-start" />正在保存…</> : label}</Button></SheetFooter>
    </form>
  </CatalogValidationContext.Provider>
}

function TextField({ label, value, onChange, description, disabled = false, type = "text" }: { label: string; value: string; onChange: (value: string) => void; description?: string; disabled?: boolean; type?: "text" | "password" }) {
  const validation = useCatalogValidation(label)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type={type} autoComplete={type === "password" ? "new-password" : undefined} value={value} disabled={disabled} onChange={(e) => { validation.clear(); onChange(e.target.value) }} />{description ? <FieldDescription>{description}</FieldDescription> : null}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function NumberField({ label, value, onChange, minimum = 0, maximum, clearFields = [] }: { label: string; value: number; onChange: (value: number) => void; minimum?: number; maximum?: number; clearFields?: string[] }) {
  const validation = useCatalogValidation(label, clearFields)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type="number" min={minimum} max={maximum} value={value} onChange={(e) => { validation.clear(); onChange(Number(e.target.value)) }} />{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function TextAreaField({ label, value, onChange, description }: { label: string; value: string; onChange: (value: string) => void; description?: string }) {
  const validation = useCatalogValidation(label)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Textarea id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} className="min-h-24 font-mono text-xs" value={value} onChange={(e) => { validation.clear(); onChange(e.target.value) }} />{description ? <FieldDescription>{description}</FieldDescription> : null}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function SelectField({ label, value, options, onChange, disabled = false }: { label: string; value: string; options: readonly (readonly [string,string])[]; onChange: (value: string) => void; disabled?: boolean }) {
  const validation = useCatalogValidation(label)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Select value={value} onValueChange={(next) => { validation.clear(); onChange(next) }} disabled={disabled}><SelectTrigger id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{options.map(([optionID, text]) => <SelectItem key={optionID} value={optionID}>{text}</SelectItem>)}</SelectGroup></SelectContent></Select>{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function CheckField({ label, checked, onChange, clearFields = [], controlID }: { label: string; checked: boolean; onChange: (value: boolean) => void; clearFields?: string[]; controlID?: string }) {
  const validation = useCatalogValidation(label, clearFields)
  const id = controlID ?? `${catalogFieldID(label)}-check`
  const errorID = `${id}-error`
  return <Field data-invalid={validation.invalid || undefined} data-field-name={label}><Checkbox id={id} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} checked={checked} onCheckedChange={(value) => { validation.clear(); onChange(value === true) }} /><FieldContent><FieldLabel htmlFor={id}>{label}</FieldLabel>{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function ChoiceList({ label, values, selected, onChange, clearFields = [] }: { label: string; values: {id:string;label:string}[]; selected: Set<string>; onChange: (value: Set<string>) => void; clearFields?: string[] }) {
  const validation = useCatalogValidation(label, clearFields)
  const errorID = `${catalogFieldID(label)}-error`
  return <fieldset className="space-y-2 rounded-lg border p-3" tabIndex={-1} data-invalid={validation.invalid || undefined} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} data-field-name={label}><legend className="px-1 text-xs font-medium">{label}</legend>{values.length ? values.map(value => <CheckField key={value.id} controlID={`${catalogFieldID(label)}-${encodeURIComponent(value.id)}-check`} label={value.label} checked={selected.has(value.id)} onChange={(checked) => { validation.clear(); const next = new Set(selected); if (checked) next.add(value.id); else next.delete(value.id); onChange(next) }} />) : <FieldDescription>暂无可选项</FieldDescription>}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</fieldset>
}

function StageNumberField({ label, value, minimum, maximum, placeholder, onChange }: { label: string; value: string | number; minimum: number; maximum: number; placeholder?: string; onChange: (value: string) => void }) {
  const validation = useCatalogValidation(label)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <div data-invalid={validation.invalid || undefined} data-field-name={label}><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type="number" min={minimum} max={maximum} value={value} placeholder={placeholder} onChange={(event) => { validation.clear(); onChange(event.target.value) }} />{validation.message ? <FieldError id={errorID} className="mt-1">{validation.message}</FieldError> : null}</div>
}

const protocolOptions = [["openai-chat","OpenAI Chat"],["kimi-k3","Kimi K3"],["seedance","Seedance"]] as const
class FormValidationError extends Error {
  readonly field: string

  constructor(field: string, message: string) {
    super(message)
    this.field = field
  }
}
function required(value: string, label: string) { const result = value.trim(); if (!result) throw new FormValidationError(label, `请输入${label}。`); return result }
function serviceURL(value: string) {
  const result = required(value, "服务地址")
  let parsed: URL
  try {
    parsed = new URL(result)
  } catch {
    throw new FormValidationError("服务地址", "请输入包含主机名的 http:// 或 https:// 服务地址。")
  }
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname) {
    throw new FormValidationError("服务地址", "请输入包含主机名的 http:// 或 https:// 服务地址。")
  }
  if (parsed.username || parsed.password) throw new FormValidationError("服务地址", "服务地址不能包含账号或密码。")
  if (parsed.search || parsed.hash) throw new FormValidationError("服务地址", "服务地址不能包含查询参数或片段。")
  return result
}
function safeCatalogKey(value: string, label: string) {
  const result = required(value, label)
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(result)) {
    throw new FormValidationError(label, "只能使用字母、数字、点、下划线或连字符，并以字母或数字开头。")
  }
  return result
}
function safeModelTarget(value: string, label: string) {
  const result = required(value, label)
  if (new TextEncoder().encode(result).length > 256 || /\p{Cc}/u.test(result)) throw new FormValidationError(label, "模型 ID 的 UTF-8 编码不能超过 256 字节，且不能包含控制字符。")
  return result
}
function list(value: string) { return [...new Set(value.split(",").map(v => v.trim()).filter(Boolean))] }
function json(value: unknown) { return JSON.stringify(value, null, 2) }
function parseJSON(value: string, label: string): unknown { try { return JSON.parse(value) } catch { throw new FormValidationError(label, "请输入有效的 JSON。") } }
function recordJSON<T>(value: string, label: string): Record<string,T> { const parsed = parseJSON(value, label); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new FormValidationError(label, "请输入 JSON 对象。"); return parsed as Record<string,T> }
function finiteNumber(value: unknown, fallback: number): number { return typeof value === "number" && Number.isFinite(value) ? value : fallback }

function integerField(value: number, label: string, minimum: number, maximum: number) {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new FormValidationError(label, `请输入 ${minimum.toLocaleString("zh-CN")}–${maximum.toLocaleString("zh-CN")} 的整数。`)
  }
}

function validatePlanLoad(loadMode: CatalogLoadMode, numbers: { concurrency: number; request_count: number; rate_per_second: number; duration_ms: number; request_timeout_ms: number }) {
  integerField(numbers.concurrency, "并发数", 1, Number.MAX_SAFE_INTEGER)
  integerField(numbers.request_count, "请求数", 0, Number.MAX_SAFE_INTEGER)
  integerField(numbers.duration_ms, "持续时间毫秒", 0, Number.MAX_SAFE_INTEGER)
  if (numbers.request_count === 0 && numbers.duration_ms === 0) {
    throw new FormValidationError("请求数", "请求数和持续时间不能同时为 0。")
  }
  if (!Number.isFinite(numbers.rate_per_second) || numbers.rate_per_second < 0) {
    throw new FormValidationError("每秒请求数", "请输入大于或等于 0 的有限数值。")
  }
  if (loadMode === "open_loop" && numbers.rate_per_second <= 0) {
    throw new FormValidationError("每秒请求数", "开放环负载的每秒请求数必须大于 0。")
  }
  integerField(numbers.request_timeout_ms, "单请求超时毫秒", 1, Number.MAX_SAFE_INTEGER)
}

function nonNegativeNumberRecord(value: string, label: string): Record<string, number> {
  const record = recordJSON<unknown>(value, label)
  const entries = Object.entries(record)
  if (entries.length === 0) throw new FormValidationError(label, "请至少填写一个 SLA 阈值。")
  for (const [name, threshold] of entries) {
    if (!name.trim() || typeof threshold !== "number" || !Number.isFinite(threshold) || threshold < 0) {
      throw new FormValidationError(label, `阈值“${name || "未命名"}”必须是大于或等于 0 的有限数值。`)
    }
  }
  return record as Record<string, number>
}

type LatencyStageDraft = { input_tokens: number; warmups: string; samples: string }

function latencyStages(value: unknown): LatencyStageDraft[] {
  if (!Array.isArray(value)) return [{ input_tokens: 128, warmups: "", samples: "" }]
  const stages = value.flatMap((entry) => {
    if (!entry || Array.isArray(entry) || typeof entry !== "object") return []
    const stage = entry as Record<string, unknown>
    if (typeof stage.input_tokens !== "number" || !Number.isFinite(stage.input_tokens)) return []
    return [{
      input_tokens: stage.input_tokens,
      warmups: typeof stage.warmups === "number" && Number.isFinite(stage.warmups) ? String(stage.warmups) : "",
      samples: typeof stage.samples === "number" && Number.isFinite(stage.samples) ? String(stage.samples) : "",
    }]
  })
  return stages.length ? stages : [{ input_tokens: 128, warmups: "", samples: "" }]
}

function latencyStageSpecs(stages: LatencyStageDraft[]) {
  if (!stages.length) throw new FormValidationError("输入 Token 阶梯", "请至少添加一个输入 Token 阶梯。")
  let previous = 0
  return stages.map((stage, index) => {
    if (!Number.isInteger(stage.input_tokens) || stage.input_tokens <= previous || stage.input_tokens > 1_000_000) {
      throw new FormValidationError(`阶梯 ${index + 1} 输入 Token`, "请输入递增且不超过 1,000,000 的整数。")
    }
    previous = stage.input_tokens
    const warmups = optionalInteger(stage.warmups, `阶梯 ${index + 1} 预热次数`, 0, 10)
    const samples = optionalInteger(stage.samples, `阶梯 ${index + 1} 采样次数`, 1, 100)
    return { input_tokens: stage.input_tokens, ...(warmups === undefined ? {} : { warmups }), ...(samples === undefined ? {} : { samples }) }
  })
}

function optionalInteger(value: string, label: string, minimum: number, maximum: number): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < minimum || parsed > maximum) throw new FormValidationError(label, `请输入 ${minimum}–${maximum} 的整数。`)
  return parsed
}

function useCatalogValidation(field: string, clearFields: string[] = []) {
  const context = useContext(CatalogValidationContext)
  const message = context?.error?.field === field ? context.error.message : ""
  return {
    invalid: Boolean(message),
    message,
    clear: () => context?.clear(field, ...clearFields),
  }
}

function catalogFieldID(label: string) {
  return `catalog-${encodeURIComponent(label)}`
}

function focusCatalogField(form: HTMLFormElement | null, field: string) {
  const container = Array.from(form?.querySelectorAll<HTMLElement>("[data-field-name]") ?? [])
    .find((candidate) => candidate.dataset.fieldName === field)
  const control = container?.querySelector<HTMLElement>("input, textarea, button, [tabindex]")
    ?? (container?.matches("[tabindex]") ? container : undefined)
    ?? Array.from(form?.querySelectorAll<HTMLElement>("[aria-label]") ?? []).find((candidate) => candidate.getAttribute("aria-label") === field)
  control?.focus()
}
