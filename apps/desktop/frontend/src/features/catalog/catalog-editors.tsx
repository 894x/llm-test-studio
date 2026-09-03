import { useState, type FormEvent, type ReactNode } from "react"
import type { TFunction } from "i18next"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"
import { useTranslation } from "react-i18next"

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
  CatalogPlan, CatalogProtocol, CatalogSnapshot, CatalogStreamCompletion, CatalogSuite,
  CatalogTestCase, DeleteCommand,
} from "./data"

export type CatalogEntityKind = "model" | "channel" | "mapping" | "case" | "suite" | "plan"
export type CatalogEntity = CatalogModel | CatalogChannel | CatalogChannelModel | CatalogTestCase | CatalogSuite | CatalogPlan
export type CatalogMutation = (
  operation: () => Promise<CatalogSnapshot>,
  operationLabel: string,
) => Promise<void>

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
  const { t } = useTranslation("catalog")
  const [open, setOpen] = useState(false)
  const noun = t(`editor.noun.${kind}`)
  const title = t(item ? "editor.editTitle" : "editor.newTitle", { noun })
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
          <SheetDescription>{t("editor.description")}</SheetDescription>
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
  const { t } = useTranslation("catalog")
  if (!item) return null
  const noun = t(`editor.noun.${kind}`)
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild><Button size="sm" variant="destructive" disabled={pending}>{t("editor.delete", { noun })}</Button></AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("editor.deleteTitle", { noun })}</AlertDialogTitle>
          <AlertDialogDescription>{t("editor.deleteDescription")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("editor.cancel")}</AlertDialogCancel>
          <AlertDialogAction onClick={() => void mutate(() => action({ id: item.id, expected_revision: item.revision }), t("editor.delete", { noun })).catch(() => undefined)}>{t("editor.confirmDelete", { noun })}</AlertDialogAction>
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
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [capabilities, setCapabilities] = useState(item?.capabilities.join(", ") ?? "")
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.model") })} formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, t("editor.fields.modelName"), t), protocol, capabilities: list(capabilities) }
    await mutate(() => item ? actions.updateModel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createModel(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.modelName")} value={name} onChange={setName} />
    <SelectField label={t("common.protocol")} value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <TextField label={t("editor.fields.modelCapabilities")} value={capabilities} onChange={setCapabilities} description={t("editor.fields.capabilityHint")} />
  </FormShell>
}

function ChannelForm({ item, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannel>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [baseURL, setBaseURL] = useState(item?.base_url ?? "https://")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [enabled, setEnabled] = useState(item?.enabled ?? true)
  const [apiKey, setAPIKey] = useState("")
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.channel") })} formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, t("editor.fields.channelName"), t), base_url: required(baseURL, t("editor.fields.serviceUrl"), t), api_key: required(apiKey, t("editor.fields.apiKey"), t), protocol, enabled }
    await mutate(() => item ? actions.updateChannel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createChannel(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.channelName")} value={name} onChange={setName} />
    <TextField label={t("editor.fields.serviceUrl")} value={baseURL} onChange={setBaseURL} />
    <TextField label={t("editor.fields.apiKey")} type="password" value={apiKey} onChange={setAPIKey} description={t(item ? "editor.fields.apiKeyEditHint" : "editor.fields.apiKeyNewHint")} />
    <SelectField label={t("common.protocol")} value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <CheckField label={t("editor.fields.enabledChannel")} checked={enabled} onChange={setEnabled} />
    <FieldDescription>{t("editor.fields.channelSaveHint")}</FieldDescription>
  </FormShell>
}

function MappingForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannelModel>) {
  const { t } = useTranslation("catalog")
  const [channelID, setChannelID] = useState(item?.channel_id ?? catalog.channels[0]?.id ?? "")
  const compatibleModels = catalog.models.filter((model) => model.protocol === catalog.channels.find((channel) => channel.id === channelID)?.protocol)
  const [modelID, setModelID] = useState(item?.model_id ?? compatibleModels[0]?.id ?? "")
  const [upstreamName, setUpstreamName] = useState(item?.upstream_model_name ?? "")
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.mapping") })} formTitle={formTitle} onSubmit={async () => {
    if (!channelID || !modelID) throw new FormValidationError(t("editor.fields.selectChannelModel"))
    await mutate(() => item
      ? actions.updateChannelModel({ id: item.id, expected_revision: item.revision, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel"), t) })
      : actions.createChannelModel({ channel_id: channelID, model_id: modelID, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel"), t) }), t("editor.savedOperation", { title: formTitle }))
    onSaved()
  }}>
    <SelectField label={t("common.channel")} value={channelID} disabled={!!item} options={catalog.channels.map((value) => [value.id, value.name])} onChange={(value) => { setChannelID(value); const protocol = catalog.channels.find((channel) => channel.id === value)?.protocol; setModelID(catalog.models.find((model) => model.protocol === protocol)?.id ?? "") }} />
    <SelectField label={t("editor.fields.logicalModel")} value={modelID} disabled={!!item} options={compatibleModels.map((value) => [value.id, value.name])} onChange={setModelID} />
    <TextField label={t("editor.fields.upstreamModel")} value={upstreamName} onChange={setUpstreamName} />
  </FormShell>
}

function CaseForm({ item, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogTestCase>) {
  const { t } = useTranslation("catalog")
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
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.case") })} formTitle={formTitle} onSubmit={async () => {
    const command = {
      key: required(value.key, t("editor.fields.caseKey"), t), name: required(value.name, t("editor.fields.caseName"), t), dimension: required(value.dimension, t("editor.fields.dimension"), t),
      protocol: value.protocol, enabled: value.enabled, default: value.default, severity: value.severity as "normal" | "critical",
      execution_mode: value.execution_mode as "automatic" | "manual", definition_schema_version: value.definition_schema_version,
      method: value.method as CatalogTestCase["method"], path: required(value.path, t("editor.fields.requestPath"), t),
      headers: recordJSON<string>(value.headers, t("editor.fields.headers"), t), body: nullableRecordJSON(value.body, t("editor.fields.body"), t),
      allowed_http_statuses: numberList(value.statuses, t), stream_completion: value.stream_completion,
      assertions: arrayJSON<{ kind: string; config: Record<string, unknown> }>(value.assertions, t("editor.fields.assertions"), t),
    }
    await mutate(() => item ? actions.updateTestCase({ ...command, id: item.id, expected_revision: item.revision }) : actions.createTestCase(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <div className="grid grid-cols-2 gap-3"><TextField label={t("editor.fields.caseKey")} value={value.key} disabled={!!item} onChange={(v) => set("key", v)} /><TextField label={t("editor.fields.caseName")} value={value.name} onChange={(v) => set("name", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><TextField label={t("editor.fields.dimension")} value={value.dimension} onChange={(v) => set("dimension", v)} /><SelectField label={t("common.protocol")} value={value.protocol} disabled={!!item} options={protocolOptions} onChange={(v) => set("protocol", v as CatalogProtocol)} /></div>
    <div className="grid grid-cols-2 gap-3"><SelectField label={t("editor.fields.requestMethod")} value={value.method} options={["GET","POST","PUT","PATCH","DELETE"].map(v => [v,v])} onChange={(v) => set("method", v as CatalogTestCase["method"])} /><TextField label={t("editor.fields.requestPath")} value={value.path} onChange={(v) => set("path", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><SelectField label={t("editor.fields.severity")} value={value.severity} options={[["normal",t("editor.options.normal")],["critical",t("editor.options.critical")]]} onChange={(v) => set("severity", v as "normal" | "critical")} /><SelectField label={t("editor.fields.executionMode")} value={value.execution_mode} options={[["automatic",t("editor.options.automatic")],["manual",t("editor.options.manual")]]} onChange={(v) => set("execution_mode", v as "automatic" | "manual")} /></div>
    <div className="grid grid-cols-2 gap-3"><CheckField label={t("editor.fields.enabled")} checked={value.enabled} onChange={(v) => set("enabled", v)} /><CheckField label={t("editor.fields.enabledDefault")} checked={value.default} onChange={(v) => set("default", v)} /></div>
    <TextAreaField label={t("editor.fields.headersJson")} value={value.headers} onChange={(v) => set("headers", v)} />
    <TextAreaField label={t("editor.fields.bodyJson")} value={value.body} onChange={(v) => set("body", v)} description={t("editor.fields.bodyHint")} />
    <TextField label={t("editor.fields.statuses")} value={value.statuses} onChange={(v) => set("statuses", v)} />
    <SelectField label={t("editor.fields.streamCompletion")} value={value.stream_completion} options={[["not_applicable",t("editor.options.notApplicable")],["required",t("editor.options.required")],["forbidden",t("editor.options.forbidden")]]} onChange={(v) => set("stream_completion", v as CatalogStreamCompletion)} />
    <TextAreaField label={t("editor.fields.assertionsJson")} value={value.assertions} onChange={(v) => set("assertions", v)} />
  </FormShell>
}

function SuiteForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogSuite>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [selected, setSelected] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.suite") })} formTitle={formTitle} onSubmit={async () => {
    const pinned = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const command = { name: required(name, t("editor.fields.suiteName"), t), cases: catalog.test_cases.filter((testCase) => selected.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinned.get(testCase.id) ?? testCase.revision })) }
    await mutate(() => item ? actions.updateSuite({ ...command, id: item.id, expected_revision: item.revision }) : actions.createSuite(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.suiteName")} value={name} onChange={setName} />
    <ChoiceList label={t("editor.fields.includedCases")} values={catalog.test_cases.map((value) => ({ id: value.id, label: `${value.name} · r${value.revision}` }))} selected={selected} onChange={setSelected} />
  </FormShell>
}

function PlanForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogPlan>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [models, setModels] = useState(() => new Set(item?.model_ids ?? []))
  const [channels, setChannels] = useState(() => new Set(item?.channel_ids ?? []))
  const [cases, setCases] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  const [suiteID, setSuiteID] = useState(item?.suite_id ?? "none")
  const [loadMode, setLoadMode] = useState<CatalogLoadMode>(item?.load_mode ?? "single")
  const [numbers, setNumbers] = useState({ concurrency: item?.concurrency ?? 1, request_count: item?.request_count ?? 1, rate_per_second: item?.rate_per_second ?? 0, duration_ms: item?.duration_ms ?? 0, request_timeout_ms: item?.request_timeout_ms ?? 60000 })
  const [sla, setSla] = useState(json(item?.sla_thresholds ?? { e2e_p95_ms: 3000 }))
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.plan") })} formTitle={formTitle} onSubmit={async () => {
		if ((models.size === 0) !== (channels.size === 0)) throw new FormValidationError(t("editor.fields.targetPair"))
    const suite = catalog.suites.find((value) => value.id === suiteID)
    const pinnedCases = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const command = {
      name: required(name, t("editor.fields.planName"), t), model_ids: [...models], channel_ids: [...channels],
      suite_id: suite?.id, suite_revision: suite?.id === item?.suite_id ? item?.suite_revision : suite?.revision,
      cases: catalog.test_cases.filter((testCase) => cases.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinnedCases.get(testCase.id) ?? testCase.revision })),
      load_mode: loadMode, ...numbers, sla_thresholds: recordJSON<number>(sla, t("editor.fields.sla"), t),
    }
    await mutate(() => item ? actions.updatePlan({ ...command, id: item.id, expected_revision: item.revision }) : actions.createPlan(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.planName")} value={name} onChange={setName} />
    <ChoiceList label={t("editor.fields.models")} values={catalog.models.map(v => ({ id: v.id, label: v.name }))} selected={models} onChange={setModels} />
    <ChoiceList label={t("editor.fields.channels")} values={catalog.channels.map(v => ({ id: v.id, label: v.name }))} selected={channels} onChange={setChannels} />
		<FieldDescription>{t("editor.fields.targetHint")}</FieldDescription>
    <ChoiceList label={t("editor.fields.directCases")} values={catalog.test_cases.map(v => ({ id: v.id, label: `${v.name} · r${v.revision}` }))} selected={cases} onChange={setCases} />
    <SelectField label={t("editor.fields.suite")} value={suiteID} options={[["none",t("editor.fields.noSuite")], ...catalog.suites.map(v => [v.id, `${v.name} · r${v.revision}`] as [string,string])]} onChange={setSuiteID} />
    <SelectField label={t("editor.fields.loadMode")} value={loadMode} options={[["single",t("plans.loadSingle")],["fixed_concurrency",t("plans.loadFixed")],["open_loop",t("plans.loadOpen")]]} onChange={(v) => setLoadMode(v as CatalogLoadMode)} />
    <div className="grid grid-cols-2 gap-3">
      {Object.entries({ concurrency: t("editor.fields.concurrency"), request_count: t("editor.fields.requestCount"), rate_per_second: t("editor.fields.rate"), duration_ms: t("editor.fields.durationMs"), request_timeout_ms: t("editor.fields.timeoutMs") }).map(([key, label]) => <NumberField key={key} label={label} value={numbers[key as keyof typeof numbers]} onChange={(v) => setNumbers(current => ({ ...current, [key]: v }))} />)}
    </div>
    <TextAreaField label={t("editor.fields.slaJson")} value={sla} onChange={setSla} />
  </FormShell>
}

function FormShell({ children, label, pending, formTitle, onSubmit }: { children: ReactNode; label: string; pending: boolean; formTitle: string; onSubmit: () => Promise<void> }) {
  const { t } = useTranslation("catalog")
  const [error, setError] = useState<{ message: string; validation: boolean } | null>(null)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    setError(null)
    void onSubmit().catch((reason: unknown) => {
      const validation = reason instanceof FormValidationError
      setError({
        validation,
        message: validation
          ? t("editor.validationError", { title: formTitle, message: reason.message })
          : publicDesktopOperationErrorMessage(reason, t("editor.savedOperation", { title: formTitle }), t("editor.saveError")),
      })
    })
  }
  return <form className="pb-4" onSubmit={submit}>
    <FieldGroup data-invalid={error?.validation || undefined} aria-invalid={error?.validation || undefined}>
      {children}
      {error ? <FieldError className={error.validation ? undefined : "rounded-md border border-destructive/25 bg-destructive-soft p-3"}>{error.message}</FieldError> : null}
    </FieldGroup>
    <SheetFooter className="px-0"><Button type="submit" className="min-w-24" disabled={pending}>{pending ? <><Spinner data-icon="inline-start" />{t("editor.saving")}</> : label}</Button></SheetFooter>
  </form>
}

function TextField({ label, value, onChange, description, disabled = false, type = "text" }: { label: string; value: string; onChange: (value: string) => void; description?: string; disabled?: boolean; type?: "text" | "password" }) {
  return <Field className="block"><FieldLabel>{label}</FieldLabel><FieldContent><Input aria-label={label} type={type} autoComplete={type === "password" ? "new-password" : undefined} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} />{description ? <FieldDescription>{description}</FieldDescription> : null}</FieldContent></Field>
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
  const { t } = useTranslation("catalog")
  return <fieldset className="space-y-2 rounded-lg border p-3"><legend className="px-1 text-xs font-medium">{label}</legend>{values.length ? values.map(value => <CheckField key={value.id} label={value.label} checked={selected.has(value.id)} onChange={(checked) => { const next = new Set(selected); if (checked) next.add(value.id); else next.delete(value.id); onChange(next) }} />) : <FieldDescription>{t("editor.noChoices")}</FieldDescription>}</fieldset>
}

const protocolOptions = [["openai-chat","OpenAI Chat"],["kimi-k3","Kimi K3"],["seedance","Seedance"]] as const
class FormValidationError extends Error {}
function required(value: string, label: string, t: TFunction<"catalog">) { const result = value.trim(); if (!result) throw new FormValidationError(t("editor.required", { label })); return result }
function list(value: string) { return [...new Set(value.split(",").map(v => v.trim()).filter(Boolean))] }
function numberList(value: string, t: TFunction<"catalog">) { const result = list(value).map(Number); if (!result.length || result.some(v => !Number.isInteger(v))) throw new FormValidationError(t("editor.invalidStatuses")); return result }
function json(value: unknown) { return JSON.stringify(value, null, 2) }
function parseJSON(value: string, label: string, t: TFunction<"catalog">): unknown { try { return JSON.parse(value) } catch { throw new FormValidationError(t("editor.invalidJson", { label })) } }
function recordJSON<T>(value: string, label: string, t: TFunction<"catalog">): Record<string,T> { const parsed = parseJSON(value, label, t); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new FormValidationError(t("editor.jsonObject", { label })); return parsed as Record<string,T> }
function nullableRecordJSON(value: string, label: string, t: TFunction<"catalog">): Record<string,unknown> | null { const parsed = parseJSON(value, label, t); if (parsed === null) return null; if (Array.isArray(parsed) || typeof parsed !== "object") throw new FormValidationError(t("editor.nullableJsonObject", { label })); return parsed as Record<string,unknown> }
function arrayJSON<T>(value: string, label: string, t: TFunction<"catalog">): T[] { const parsed = parseJSON(value, label, t); if (!Array.isArray(parsed)) throw new FormValidationError(t("editor.jsonArray", { label })); return parsed as T[] }
