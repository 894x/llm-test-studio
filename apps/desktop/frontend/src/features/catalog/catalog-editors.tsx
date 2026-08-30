import { useState, type FormEvent, type ReactNode } from "react"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"

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
  CatalogPlan, CatalogProtocol, CatalogSnapshot, CatalogStreamCompletion, CatalogSuite,
  CatalogTestCase, DeleteCommand,
} from "./data"

export type CatalogEntityKind = "model" | "channel" | "mapping" | "case" | "suite" | "plan"
export type CatalogEntity = CatalogModel | CatalogChannel | CatalogChannelModel | CatalogTestCase | CatalogSuite | CatalogPlan
export type CatalogMutation = (operation: () => Promise<CatalogSnapshot>) => Promise<void>

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
          <EditorForm kind={kind} item={item} catalog={catalog} actions={actions} mutate={mutate} pending={pending} onSaved={() => setOpen(false)} />
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
          <AlertDialogAction onClick={() => void mutate(() => action({ id: item.id, expected_revision: item.revision })).catch(() => undefined)}>确认删除{noun}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function EditorForm(props: {
  kind: CatalogEntityKind; item?: CatalogEntity; catalog: CatalogSnapshot; actions: CatalogActions;
  mutate: CatalogMutation; pending: boolean; onSaved: () => void
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
  pending: boolean; onSaved: () => void
}

function ModelForm({ item, actions, mutate, pending, onSaved }: FormProps<CatalogModel>) {
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [capabilities, setCapabilities] = useState(item?.capabilities.join(", ") ?? "")
  return <FormShell pending={pending} label="保存模型" onSubmit={async () => {
    const command = { name: required(name, "模型名称"), protocol, capabilities: list(capabilities) }
    await mutate(() => item ? actions.updateModel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createModel(command)); onSaved()
  }}>
    <TextField label="模型名称" value={name} onChange={setName} />
    <SelectField label="协议" value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <TextField label="模型能力" value={capabilities} onChange={setCapabilities} description="使用逗号分隔，例如 chat, tools, vision。" />
  </FormShell>
}

function ChannelForm({ item, actions, mutate, pending, onSaved }: FormProps<CatalogChannel>) {
  const [name, setName] = useState(item?.name ?? "")
  const [baseURL, setBaseURL] = useState(item?.base_url ?? "https://")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [enabled, setEnabled] = useState(item?.enabled ?? true)
  return <FormShell pending={pending} label="保存渠道" onSubmit={async () => {
    const command = { name: required(name, "渠道名称"), base_url: required(baseURL, "服务地址"), protocol, enabled }
    await mutate(() => item ? actions.updateChannel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createChannel(command)); onSaved()
  }}>
    <TextField label="渠道名称" value={name} onChange={setName} />
    <TextField label="服务地址" value={baseURL} onChange={setBaseURL} />
    <SelectField label="协议" value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <CheckField label="启用渠道" checked={enabled} onChange={setEnabled} />
    <FieldDescription>凭据仍由操作系统密钥环管理，不会进入目录快照。</FieldDescription>
  </FormShell>
}

function MappingForm({ item, catalog, actions, mutate, pending, onSaved }: FormProps<CatalogChannelModel>) {
  const [channelID, setChannelID] = useState(item?.channel_id ?? catalog.channels[0]?.id ?? "")
  const compatibleModels = catalog.models.filter((model) => model.protocol === catalog.channels.find((channel) => channel.id === channelID)?.protocol)
  const [modelID, setModelID] = useState(item?.model_id ?? compatibleModels[0]?.id ?? "")
  const [upstreamName, setUpstreamName] = useState(item?.upstream_model_name ?? "")
  return <FormShell pending={pending} label="保存映射" onSubmit={async () => {
    if (!channelID || !modelID) throw new Error("请选择渠道和模型")
    await mutate(() => item
      ? actions.updateChannelModel({ id: item.id, expected_revision: item.revision, upstream_model_name: required(upstreamName, "上游模型名称") })
      : actions.createChannelModel({ channel_id: channelID, model_id: modelID, upstream_model_name: required(upstreamName, "上游模型名称") }))
    onSaved()
  }}>
    <SelectField label="渠道" value={channelID} disabled={!!item} options={catalog.channels.map((value) => [value.id, value.name])} onChange={(value) => { setChannelID(value); const protocol = catalog.channels.find((channel) => channel.id === value)?.protocol; setModelID(catalog.models.find((model) => model.protocol === protocol)?.id ?? "") }} />
    <SelectField label="逻辑模型" value={modelID} disabled={!!item} options={compatibleModels.map((value) => [value.id, value.name])} onChange={setModelID} />
    <TextField label="上游模型名称" value={upstreamName} onChange={setUpstreamName} />
  </FormShell>
}

function CaseForm({ item, actions, mutate, pending, onSaved }: FormProps<CatalogTestCase>) {
  const [value, setValue] = useState(() => ({
    key: item?.key ?? "", name: item?.name ?? "", dimension: item?.dimension ?? "compatibility",
    protocol: item?.protocol ?? "openai-chat" as CatalogProtocol, enabled: item?.enabled ?? true,
    default: item?.default ?? false, severity: item?.severity ?? "normal", execution_mode: item?.execution_mode ?? "automatic",
    definition_schema_version: item?.definition_schema_version ?? 1, method: item?.method ?? "POST",
    path: item?.path ?? "/v1/chat/completions", headers: json(item?.headers ?? {}), body: json(item?.body),
    statuses: item?.allowed_http_statuses.join(", ") ?? "200", stream_completion: item?.stream_completion ?? "not_applicable" as CatalogStreamCompletion,
    assertions: json(item?.assertions ?? [{ kind: "custom", config: { name: "custom-check" } }]),
  }))
  const set = <K extends keyof typeof value>(key: K, next: (typeof value)[K]) => setValue((current) => ({ ...current, [key]: next }))
  return <FormShell pending={pending} label="保存用例" onSubmit={async () => {
    const command = {
      key: required(value.key, "用例键"), name: required(value.name, "用例名称"), dimension: required(value.dimension, "维度"),
      protocol: value.protocol, enabled: value.enabled, default: value.default, severity: value.severity as "normal" | "critical",
      execution_mode: value.execution_mode as "automatic" | "manual", definition_schema_version: value.definition_schema_version,
      method: value.method as CatalogTestCase["method"], path: required(value.path, "请求路径"),
      headers: recordJSON<string>(value.headers, "请求头"), body: nullableRecordJSON(value.body, "请求体"),
      allowed_http_statuses: numberList(value.statuses), stream_completion: value.stream_completion,
      assertions: arrayJSON<{ kind: string; config: Record<string, unknown> }>(value.assertions, "断言"),
    }
    await mutate(() => item ? actions.updateTestCase({ ...command, id: item.id, expected_revision: item.revision }) : actions.createTestCase(command)); onSaved()
  }}>
    <div className="grid grid-cols-2 gap-3"><TextField label="用例键" value={value.key} disabled={!!item} onChange={(v) => set("key", v)} /><TextField label="用例名称" value={value.name} onChange={(v) => set("name", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><TextField label="维度" value={value.dimension} onChange={(v) => set("dimension", v)} /><SelectField label="协议" value={value.protocol} disabled={!!item} options={protocolOptions} onChange={(v) => set("protocol", v as CatalogProtocol)} /></div>
    <div className="grid grid-cols-2 gap-3"><SelectField label="请求方法" value={value.method} options={["GET","POST","PUT","PATCH","DELETE"].map(v => [v,v])} onChange={(v) => set("method", v as CatalogTestCase["method"])} /><TextField label="请求路径" value={value.path} onChange={(v) => set("path", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><SelectField label="严重度" value={value.severity} options={[["normal","普通"],["critical","关键"]]} onChange={(v) => set("severity", v as "normal" | "critical")} /><SelectField label="执行方式" value={value.execution_mode} options={[["automatic","自动"],["manual","人工"]]} onChange={(v) => set("execution_mode", v as "automatic" | "manual")} /></div>
    <div className="grid grid-cols-2 gap-3"><CheckField label="启用" checked={value.enabled} onChange={(v) => set("enabled", v)} /><CheckField label="默认启用" checked={value.default} onChange={(v) => set("default", v)} /></div>
    <TextAreaField label="请求头 JSON" value={value.headers} onChange={(v) => set("headers", v)} />
    <TextAreaField label="请求体 JSON" value={value.body} onChange={(v) => set("body", v)} description="无请求体请填写 null。" />
    <TextField label="允许的 HTTP 状态码" value={value.statuses} onChange={(v) => set("statuses", v)} />
    <SelectField label="流结束约束" value={value.stream_completion} options={[["not_applicable","不适用"],["required","必须完成"],["forbidden","禁止流式"]]} onChange={(v) => set("stream_completion", v as CatalogStreamCompletion)} />
    <TextAreaField label="断言 JSON" value={value.assertions} onChange={(v) => set("assertions", v)} />
  </FormShell>
}

function SuiteForm({ item, catalog, actions, mutate, pending, onSaved }: FormProps<CatalogSuite>) {
  const [name, setName] = useState(item?.name ?? "")
  const [selected, setSelected] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  return <FormShell pending={pending} label="保存套件" onSubmit={async () => {
    const pinned = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const command = { name: required(name, "套件名称"), cases: catalog.test_cases.filter((testCase) => selected.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinned.get(testCase.id) ?? testCase.revision })) }
    await mutate(() => item ? actions.updateSuite({ ...command, id: item.id, expected_revision: item.revision }) : actions.createSuite(command)); onSaved()
  }}>
    <TextField label="套件名称" value={name} onChange={setName} />
    <ChoiceList label="包含用例" values={catalog.test_cases.map((value) => ({ id: value.id, label: `${value.name} · r${value.revision}` }))} selected={selected} onChange={setSelected} />
  </FormShell>
}

function PlanForm({ item, catalog, actions, mutate, pending, onSaved }: FormProps<CatalogPlan>) {
  const [name, setName] = useState(item?.name ?? "")
  const [models, setModels] = useState(() => new Set(item?.model_ids ?? catalog.models.slice(0, 1).map(model => model.id)))
  const [channels, setChannels] = useState(() => new Set(item?.channel_ids ?? catalog.channels.slice(0, 1).map(channel => channel.id)))
  const [cases, setCases] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  const [suiteID, setSuiteID] = useState(item?.suite_id ?? "none")
  const [loadMode, setLoadMode] = useState<CatalogLoadMode>(item?.load_mode ?? "single")
  const [numbers, setNumbers] = useState({ concurrency: item?.concurrency ?? 1, request_count: item?.request_count ?? 1, rate_per_second: item?.rate_per_second ?? 0, duration_ms: item?.duration_ms ?? 0, request_timeout_ms: item?.request_timeout_ms ?? 60000 })
  const [sla, setSla] = useState(json(item?.sla_thresholds ?? { p95_ms: 3000 }))
  return <FormShell pending={pending} label="保存计划" onSubmit={async () => {
    const suite = catalog.suites.find((value) => value.id === suiteID)
    const pinnedCases = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const command = {
      name: required(name, "计划名称"), model_ids: [...models], channel_ids: [...channels],
      suite_id: suite?.id, suite_revision: suite?.id === item?.suite_id ? item?.suite_revision : suite?.revision,
      cases: catalog.test_cases.filter((testCase) => cases.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinnedCases.get(testCase.id) ?? testCase.revision })),
      load_mode: loadMode, ...numbers, sla_thresholds: recordJSON<number>(sla, "SLA 阈值"),
    }
    await mutate(() => item ? actions.updatePlan({ ...command, id: item.id, expected_revision: item.revision }) : actions.createPlan(command)); onSaved()
  }}>
    <TextField label="计划名称" value={name} onChange={setName} />
    <ChoiceList label="模型" values={catalog.models.map(v => ({ id: v.id, label: v.name }))} selected={models} onChange={setModels} />
    <ChoiceList label="渠道" values={catalog.channels.map(v => ({ id: v.id, label: v.name }))} selected={channels} onChange={setChannels} />
    <ChoiceList label="直接用例" values={catalog.test_cases.map(v => ({ id: v.id, label: `${v.name} · r${v.revision}` }))} selected={cases} onChange={setCases} />
    <SelectField label="套件" value={suiteID} options={[["none","不使用套件"], ...catalog.suites.map(v => [v.id, `${v.name} · r${v.revision}`] as [string,string])]} onChange={setSuiteID} />
    <SelectField label="负载模式" value={loadMode} options={[["single","单次"],["fixed_concurrency","固定并发"],["open_loop","开放环"]]} onChange={(v) => setLoadMode(v as CatalogLoadMode)} />
    <div className="grid grid-cols-2 gap-3">
      {Object.entries({ concurrency: "并发数", request_count: "请求数", rate_per_second: "每秒请求数", duration_ms: "持续时间毫秒", request_timeout_ms: "单请求超时毫秒" }).map(([key, label]) => <NumberField key={key} label={label} value={numbers[key as keyof typeof numbers]} onChange={(v) => setNumbers(current => ({ ...current, [key]: v }))} />)}
    </div>
    <TextAreaField label="SLA 阈值 JSON" value={sla} onChange={setSla} />
  </FormShell>
}

function FormShell({ children, label, pending, onSubmit }: { children: ReactNode; label: string; pending: boolean; onSubmit: () => Promise<void> }) {
  const [error, setError] = useState("")
  return <form className="pb-4" onSubmit={(event: FormEvent) => { event.preventDefault(); setError(""); void onSubmit().catch((reason: unknown) => setError(reason instanceof Error ? reason.message : "保存失败")) }}>
    <FieldGroup data-invalid={error ? true : undefined} aria-invalid={error ? true : undefined}>{children}{error ? <FieldError>{error}</FieldError> : null}</FieldGroup>
    <SheetFooter className="px-0"><Button type="submit" className="min-w-24" disabled={pending}>{pending ? <><Spinner data-icon="inline-start" />正在保存…</> : label}</Button></SheetFooter>
  </form>
}

function TextField({ label, value, onChange, description, disabled = false }: { label: string; value: string; onChange: (value: string) => void; description?: string; disabled?: boolean }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><FieldContent><Input aria-label={label} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} />{description ? <FieldDescription>{description}</FieldDescription> : null}</FieldContent></Field>
}
function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><Input aria-label={label} type="number" min={0} value={value} onChange={(e) => onChange(Number(e.target.value))} /></Field>
}
function TextAreaField({ label, value, onChange, description }: { label: string; value: string; onChange: (value: string) => void; description?: string }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><Textarea aria-label={label} className="min-h-24 font-mono text-xs" value={value} onChange={(e) => onChange(e.target.value)} />{description ? <FieldDescription>{description}</FieldDescription> : null}</Field>
}
function SelectField({ label, value, options, onChange, disabled = false }: { label: string; value: string; options: readonly (readonly [string,string])[]; onChange: (value: string) => void; disabled?: boolean }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><Select value={value} onValueChange={onChange} disabled={disabled}><SelectTrigger aria-label={label} className="w-full"><SelectValue /></SelectTrigger><SelectContent><SelectGroup>{options.map(([id, text]) => <SelectItem key={id} value={id}>{text}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>
}
function CheckField({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) {
  return <Field><Checkbox id={`check-${label}`} checked={checked} onCheckedChange={(value) => onChange(value === true)} /><FieldLabel htmlFor={`check-${label}`}>{label}</FieldLabel></Field>
}
function ChoiceList({ label, values, selected, onChange }: { label: string; values: {id:string;label:string}[]; selected: Set<string>; onChange: (value: Set<string>) => void }) {
  return <fieldset className="space-y-2 rounded-lg border p-3"><legend className="px-1 text-xs font-medium">{label}</legend>{values.length ? values.map(value => <CheckField key={value.id} label={value.label} checked={selected.has(value.id)} onChange={(checked) => { const next = new Set(selected); if (checked) next.add(value.id); else next.delete(value.id); onChange(next) }} />) : <FieldDescription>暂无可选项</FieldDescription>}</fieldset>
}

const protocolOptions = [["openai-chat","OpenAI Chat"],["kimi-k3","Kimi K3"],["seedance","Seedance"]] as const
function required(value: string, label: string) { const result = value.trim(); if (!result) throw new Error(`${label}不能为空`); return result }
function list(value: string) { return [...new Set(value.split(",").map(v => v.trim()).filter(Boolean))] }
function numberList(value: string) { const result = list(value).map(Number); if (!result.length || result.some(v => !Number.isInteger(v))) throw new Error("HTTP 状态码格式无效"); return result }
function json(value: unknown) { return JSON.stringify(value, null, 2) }
function parseJSON(value: string, label: string): unknown { try { return JSON.parse(value) } catch { throw new Error(`${label}不是有效 JSON`) } }
function recordJSON<T>(value: string, label: string): Record<string,T> { const parsed = parseJSON(value, label); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error(`${label}必须是 JSON 对象`); return parsed as Record<string,T> }
function nullableRecordJSON(value: string, label: string): Record<string,unknown> | null { const parsed = parseJSON(value, label); if (parsed === null) return null; if (Array.isArray(parsed) || typeof parsed !== "object") throw new Error(`${label}必须是 JSON 对象或 null`); return parsed as Record<string,unknown> }
function arrayJSON<T>(value: string, label: string): T[] { const parsed = parseJSON(value, label); if (!Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 数组`); return parsed as T[] }
