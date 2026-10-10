import { isQuickPerformanceProtocol } from "@/features/quick-test/data"
import { protocolOptions } from "./protocols"
import { localizeStoredMessage, desktopLocale, translateDesktop as tx } from "@/i18n/runtime"
import { protocolPresentation } from "@/features/protocols/registry"
import { parseSuiteInput } from "./data"
import { createContext, useContext, useRef, useState, type FormEvent, type ReactNode } from "react"
import ArrowDownIcon from "lucide-react/dist/esm/icons/arrow-down.mjs"
import ArrowUpIcon from "lucide-react/dist/esm/icons/arrow-up.mjs"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"
import Trash2Icon from "lucide-react/dist/esm/icons/trash-2.mjs"
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
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Textarea } from "@/components/ui/textarea"
import { Spinner } from "@/components/ui/spinner"
import { TagAutocomplete } from "@/components/ui/tag-autocomplete"
import {
  QUICK_PERFORMANCE_PRESET_IDS,
  quickPerformancePresetFromProfile,
  quickPerformanceProfileForPreset,
  type QuickPerformancePresetID,
} from "@/features/quick-test/performance-presets"

import type {
  CatalogActions, CatalogChannel, CatalogChannelModel, CatalogLoadMode, CatalogModel,
  CatalogPlan, CatalogPlanParameterValue, CatalogProtocol, CatalogSnapshot, CatalogSuite,
  CatalogTestCase, DeleteCommand, PlanEntryCommand,
} from "./data"

export type CatalogEntityKind = "model" | "channel" | "mapping" | "case" | "suite" | "plan"
export type CatalogEntity = CatalogModel | CatalogChannel | CatalogChannelModel | CatalogTestCase | CatalogSuite | CatalogPlan
export type CatalogMutation = (
  operation: () => Promise<CatalogSnapshot>,
  operationLabel: string,
) => Promise<void>

export function CatalogEditor({
  kind, item, catalog, actions, mutate, pending, trigger, initialMapping,
}: {
  trigger?: ReactNode
  initialMapping?: Pick<CatalogChannelModel, "model_id" | "channel_id">
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
        {trigger ?? <Button size="sm" variant={item ? "outline" : "default"} disabled={pending || (kind === "mapping" && (!catalog.models.length || !catalog.channels.length))}>
          {!item ? <PlusIcon data-icon="inline-start" /> : null}{title}
        </Button>}
      </SheetTrigger>
      <SheetContent className={kind === "case" || kind === "suite" ? "data-[side=right]:w-full data-[side=right]:sm:max-w-3xl" : kind === "plan" ? "data-[side=right]:w-full data-[side=right]:sm:max-w-2xl" : "sm:max-w-lg"} onEscapeKeyDown={(event) => {
        if (event.target instanceof HTMLElement && event.target.matches('[role="combobox"][aria-expanded="true"]')) event.preventDefault()
      }}>
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>{t("editor.description")}</SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4">
          <EditorForm initialMapping={initialMapping} kind={kind} item={item} catalog={catalog} actions={actions} mutate={mutate} pending={pending} formTitle={title} onSaved={() => setOpen(false)} />
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
  initialMapping?: Pick<CatalogChannelModel, "model_id" | "channel_id">
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

function ModelForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogModel>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [protocols, setProtocols] = useState<CatalogProtocol[]>(item?.protocols ?? ["openai-chat"])
  const usedProtocols = new Set(catalog.channel_models.filter(mapping => mapping.model_id === item?.id).flatMap(mapping => mapping.protocols))
  const [capabilities, setCapabilities] = useState<string[]>(item?.capabilities ?? [])
  const suggestedCapabilities = ["chat", "tools", "vision"] as const
  const capabilityOptions = [
    ...suggestedCapabilities.map((value) => ({ value, label: `${t(`capabilities.${value}`)} (${value})` })),
    ...[...new Set([...catalog.models.flatMap((model) => model.capabilities), ...capabilities])]
      .filter((value) => !suggestedCapabilities.some((suggestion) => suggestion === value))
      .map((value) => ({ value, label: value })),
  ]
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.model") })} formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, t("editor.fields.modelName")), protocols, capabilities }
    await mutate(() => item ? actions.updateModel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createModel(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.modelName")} value={name} onChange={setName} />
    <Field className="block space-y-2"><FieldLabel>{t("protocolDesign.supportedProtocols")}</FieldLabel><FieldDescription>{t("editor.fields.modelProtocolsHint")}</FieldDescription>
      <div className="grid grid-cols-2 gap-2">{protocolOptions.map(([value, label]) => <CheckField key={value} label={label}
        checked={protocols.includes(value)} disabled={pending || usedProtocols.has(value) || (protocols.length === 1 && protocols.includes(value))}
        onChange={checked => setProtocols(current => checked ? [...current, value] : current.filter(item => item !== value))} />)}</div>
    </Field>
    <TagAutocomplete label={t("editor.fields.modelCapabilities")} value={capabilities} onChange={setCapabilities}
      options={capabilityOptions} description={t("editor.fields.capabilityHint")} disabled={pending}
      placeholder={t("capabilities.placeholder")} emptyText={t("capabilities.empty")}
      addLabel={(value) => t("capabilities.add", { value })} removeLabel={(value) => t("capabilities.remove", { value })} />
  </FormShell>
}

function ChannelForm({ item, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannel>) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [baseURL, setBaseURL] = useState(item?.base_url ?? "https://")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [enabled, setEnabled] = useState(item?.enabled ?? true)
  const [apiKey, setAPIKey] = useState("")
  return <FormShell pending={pending} label={tx("desktop:catalog_save_channel")} formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, tx("desktop:catalog_channel_name")), base_url: serviceURL(baseURL), api_key: required(apiKey, "API Key"), protocol, enabled }
    await mutate(() => item ? actions.updateChannel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createChannel(command), tx("desktop:catalog_save_value", { value1: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.channelName")} value={name} onChange={setName} />
    <TextField label={t("editor.fields.serviceUrl")} value={baseURL} onChange={setBaseURL} />
    <TextField label={t("editor.fields.apiKey")} type="password" value={apiKey} onChange={setAPIKey} description={t(item ? "editor.fields.apiKeyEditHint" : "editor.fields.apiKeyNewHint")} />
    <SelectField label={t("common.protocol")} value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <CheckField label={t("editor.fields.enabledChannel")} checked={enabled} onChange={setEnabled} />
    <FieldDescription>{t("editor.fields.channelSaveHint")}</FieldDescription>
  </FormShell>
}

function MappingForm({ item, catalog, actions, mutate, pending, formTitle, onSaved, initialMapping }: FormProps<CatalogChannelModel> & { initialMapping?: Pick<CatalogChannelModel, "model_id" | "channel_id"> }) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const [channelID, setChannelID] = useState(item?.channel_id ?? initialMapping?.channel_id ?? catalog.channels[0]?.id ?? "")
  const compatibleModels = catalog.models
  const [modelID, setModelID] = useState(item?.model_id ?? initialMapping?.model_id ?? compatibleModels[0]?.id ?? "")
  const model = catalog.models.find(value => value.id === modelID)
  const [protocols, setProtocols] = useState<CatalogProtocol[]>(item?.protocols ?? model?.protocols ?? [])
  const [upstreamName, setUpstreamName] = useState(item?.upstream_model_name ?? "")
  return <FormShell pending={pending} label={tx("desktop:catalog_save_mapping")} formTitle={formTitle} onSubmit={async () => {
    if (!channelID) throw new FormValidationError(tx("desktop:catalog_channel"), tx("desktop:catalog_select_a_channel"))
    if (!modelID) throw new FormValidationError(tx("desktop:catalog_logical_model"), tx("desktop:catalog_select_a_logical_model"))
    await mutate(() => item
      ? actions.updateChannelModel({ id: item.id, expected_revision: item.revision, protocols, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel")) })
      : actions.createChannelModel({ channel_id: channelID, model_id: modelID, protocols, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel")) }), t("editor.savedOperation", { title: formTitle }))
    onSaved()
  }}>
    <SelectField label={t("common.channel")} value={channelID} disabled={!!item} options={catalog.channels.map((value) => [value.id, value.name])} onChange={setChannelID} />
    <SelectField label={t("editor.fields.logicalModel")} value={modelID} disabled={!!item} options={compatibleModels.map((value) => [value.id, value.name])} onChange={value => { setModelID(value); setProtocols(catalog.models.find(model => model.id === value)!.protocols) }} />
    <Field className="block space-y-2"><FieldLabel>{t("protocolDesign.supportedProtocols")}</FieldLabel>
      <div className="grid grid-cols-2 gap-2">{protocolOptions.filter(([value]) => model?.protocols.includes(value)).map(([value, label]) => <CheckField key={value} label={label}
        checked={protocols.includes(value)} disabled={pending || (protocols.length === 1 && protocols.includes(value))}
        onChange={checked => setProtocols(current => checked ? [...current, value] : current.filter(item => item !== value))} />)}</div>
      <FieldDescription>{t("editor.fields.mappingProtocolHint")}</FieldDescription>
    </Field>
    <TextField label={t("editor.fields.upstreamModel")} value={upstreamName} onChange={setUpstreamName} />
  </FormShell>
}

interface CaseProtocolDraft {
  inputs: string; body: string; assertions: string; operation: string; workflow: string
}

function caseProtocolDraft(spec: Record<string, unknown>): CaseProtocolDraft {
  return { inputs: json(spec.inputs ?? {}), body: json((spec.request as { body?: unknown })?.body ?? {}),
    assertions: json(spec.assertions ?? []), operation: typeof spec.operation === "string" ? spec.operation : "default",
    workflow: json(spec.workflow ?? null) }
}

function CaseForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogTestCase>) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const initialProtocol = Object.keys(item?.definitions ?? {})[0] as CatalogProtocol | undefined
  const [protocol, setProtocol] = useState<CatalogProtocol>(initialProtocol ?? "openai-chat")
  const defaultDraft = (value: CatalogProtocol) => caseProtocolDraft(catalog.case_types.find(type => type.type === value)?.default_spec ?? {})
  const [drafts, setDrafts] = useState<Partial<Record<CatalogProtocol, CaseProtocolDraft>>>(() => item
    ? Object.fromEntries(Object.entries(item.definitions).map(([value, spec]) => [value, caseProtocolDraft(spec)]))
    : { "openai-chat": defaultDraft("openai-chat") })
  const draft = drafts[protocol] ?? defaultDraft(protocol)
  const updateDraft = (field: keyof CaseProtocolDraft, value: string) => setDrafts(current => ({ ...current, [protocol]: { ...draft, [field]: value } }))
  const toggleProtocol = (value: CatalogProtocol, checked: boolean) => {
    const next = { ...drafts }
    if (checked) { next[value] = next[value] ?? defaultDraft(value); setProtocol(value) }
    else {
      if (Object.keys(next).length === 1) return
      delete next[value]
      if (protocol === value) setProtocol(Object.keys(next)[0] as CatalogProtocol)
    }
    setDrafts(next)
  }
  const [key, setKey] = useState(item?.key ?? "")
  const [name, setName] = useState(item?.name ?? "")
  const [dimension, setDimension] = useState(item?.dimension ?? "compatibility")
  const [enabled, setEnabled] = useState(item?.enabled ?? true)
  return <FormShell pending={pending} label={tx("desktop:catalog_save_case")} formTitle={formTitle} onSubmit={async () => {
    const definitions: CatalogTestCase["definitions"] = {}
    for (const [value, current] of Object.entries(drafts)) {
      const parsedAssertions = parseJSON(current.assertions, `${value} · ${t("protocolDesign.assertions")}`)
      if (!Array.isArray(parsedAssertions)) throw new FormValidationError(t("protocolDesign.assertions"), t("protocolDesign.arrayRequired"))
      const parsedWorkflow = parseJSON(current.workflow, `${value} · ${t("protocolDesign.workflow")}`)
      definitions[value as CatalogProtocol] = {
        inputs: recordJSON<unknown>(current.inputs, `${value} · ${t("protocolDesign.inputs")}`),
        request: { body: parseJSON(current.body, `${value} · ${t("protocolDesign.body")}`) }, assertions: parsedAssertions,
        ...(current.operation === "default" ? {} : { operation: current.operation }),
        ...(parsedWorkflow === null ? {} : { workflow: parsedWorkflow }),
      }
    }
    const command = {
      key: safeCatalogKey(key, tx("desktop:catalog_case_key")), name: required(name, tx("desktop:catalog_case_name")),
      dimension: required(dimension, tx("desktop:catalog_dimension")), enabled,
      default: item?.default ?? false, severity: item?.severity ?? "normal" as const,
      execution_mode: item?.execution_mode ?? "automatic" as const, definitions,
    }
    await mutate(() => item ? actions.updateTestCase({ ...command, id: item.id, expected_revision: item.revision }) : actions.createTestCase(command), formTitle)
    onSaved()
  }}>
    <div className="grid min-w-0 grid-cols-2 gap-3"><TextField label={tx("desktop:catalog_case_key")} value={key} onChange={setKey} disabled={!!item} /><TextField label={tx("desktop:catalog_case_name")} value={name} onChange={setName} /></div>
    <Field><FieldLabel>{t("protocolDesign.supportedProtocols")}</FieldLabel><FieldDescription>{t("protocolDesign.supportedProtocolsHint")}</FieldDescription>
      <div className="grid grid-cols-2 gap-2">{protocolOptions.map(([value, label]) => <CheckField key={value} label={label} checked={!!drafts[value as CatalogProtocol]} onChange={checked => toggleProtocol(value as CatalogProtocol, checked)} />)}</div>
    </Field>
    <SelectField label={t("protocolDesign.editProtocol")} value={protocol} options={protocolOptions.filter(([value]) => !!drafts[value as CatalogProtocol])} onChange={value => setProtocol(value as CatalogProtocol)} />
    {protocolPresentation(protocol).operations.length ? <SelectField label={t("protocolDesign.operation")} value={draft.operation} options={[["default", t("protocolDesign.defaultOperation")], ...protocolPresentation(protocol).operations.map(value => [value, value] as [string,string])]} onChange={value => updateDraft("operation", value)} /> : null}
    <TextField label={tx("desktop:catalog_dimension")} value={dimension} onChange={setDimension} />
    <TextAreaField label={t("protocolDesign.inputs")} value={draft.inputs} onChange={value => updateDraft("inputs", value)} description={t("protocolDesign.inputsHint")} />
    <TextAreaField label={t("protocolDesign.body")} value={draft.body} onChange={value => updateDraft("body", value)} description={t("protocolDesign.bodyHint")} />
    <TextAreaField label={t("protocolDesign.assertions")} value={draft.assertions} onChange={value => updateDraft("assertions", value)} description={t("protocolDesign.assertionsHint")} />
    <TextAreaField label={t("protocolDesign.workflow")} value={draft.workflow} onChange={value => updateDraft("workflow", value)} description={t("protocolDesign.workflowHint")} />
    <CheckField label={tx("desktop:catalog_enabled")} checked={enabled} onChange={setEnabled} />
  </FormShell>
}

function SuiteForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogSuite>) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const [key, setKey] = useState(item?.key ?? "")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [description, setDescription] = useState(item?.description ?? "")
  const [inputs, setInputs] = useState(json(item?.inputs ?? []))
  const [selected, setSelected] = useState(() => new Set(item?.cases.map(ref => ref.case_id) ?? []))
  const availableCases = catalog.test_cases.filter(testCase => !!testCase.definitions[protocol])
  return <FormShell pending={pending} label={tx("desktop:catalog_save_suite")} formTitle={formTitle} onSubmit={async () => {
    const parsedInputs = parseJSON(inputs, t("protocolDesign.mapping"))
    if (!Array.isArray(parsedInputs)) throw new FormValidationError(t("protocolDesign.mapping"), t("protocolDesign.arrayRequired"))
    const command = { key: safeCatalogKey(key, tx("desktop:catalog_suite_key")), name: required(name, tx("desktop:catalog_suite_name")),
      protocol, description, cases: [...selected].map(case_id => ({ case_id })), inputs: parsedInputs.map(parseSuiteInput) }
    await mutate(() => item ? actions.updateSuite({ ...command, id: item.id, expected_revision: item.revision }) : actions.createSuite(command), formTitle)
    onSaved()
  }}>
    <TextField label={tx("desktop:catalog_suite_key")} value={key} onChange={setKey} disabled={!!item} />
    <TextField label={tx("desktop:catalog_suite_name")} value={name} onChange={setName} />
    <SelectField label={t("common.protocol")} value={protocol} options={protocolOptions} disabled={!!item} onChange={value => { setProtocol(value as CatalogProtocol); setSelected(new Set()) }} />
    <TextField label={t("protocolDesign.description")} value={description} onChange={setDescription} />
    <ChoiceList multiColumn label={tx("desktop:catalog_included_cases")} values={availableCases.map(value => ({ id: value.id, label: value.name }))} selected={selected} onChange={setSelected} />
    <TextAreaField label={t("protocolDesign.mapping")} value={inputs} onChange={setInputs} description={t("protocolDesign.mappingHint")} />
  </FormShell>
}

type EntryDraft = PlanEntryCommand & { draftKey: string; parametersJSON: string; slaJSON: string }
function PlanForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogPlan>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [seed, setSeed] = useState(item?.seed ?? 1)
  const [planMode, setPlanMode] = useState<"protocol" | "performance">(item?.performance ? "performance" : "protocol")
  const [performancePreset, setPerformancePreset] = useState<QuickPerformancePresetID>(
    item?.performance ? quickPerformancePresetFromProfile(item.performance) : "smoke",
  )
  const [target, setTarget] = useState("")
  const [entries, setEntries] = useState<EntryDraft[]>(() => item?.entries.map(entry => ({ ...entry,
    draftKey: entry.entry_id, parametersJSON: json(entry.parameters), slaJSON: json(entry.sla_thresholds) })) ?? [])
  const targets = [
    ...catalog.test_cases.filter(value => !!value.definitions[protocol]).map(value => [`case:${value.id}`, `${t("editor.noun.case")} · ${value.name}`] as [string, string]),
    ...catalog.suites.filter(value => value.protocol === protocol).map(value => [`suite:${value.id}`, `${t("editor.noun.suite")} · ${value.name}`] as [string, string]),
  ]
  const update = (index: number, patch: Partial<EntryDraft>) => setEntries(current => current.map((entry, i) => i === index ? { ...entry, ...patch } : entry))
  const move = (index: number, offset: number) => setEntries(current => { const next = [...current]; [next[index], next[index + offset]] = [next[index + offset], next[index]]; return next })
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.plan") })} formTitle={formTitle} onSubmit={async () => {
    integerField(seed, t("protocolDesign.seed"), 0, Number.MAX_SAFE_INTEGER)
    const planName = required(name, t("common.plan"))
    if (planMode === "protocol" && entries.length === 0) {
      throw new FormValidationError("planEntries", t("protocolDesign.entryRequired"), t("protocolDesign.target"))
    }
    const command = planMode === "performance"
      ? { name: planName, protocol, seed, entries: [], performance: quickPerformanceProfileForPreset(performancePreset) }
      : { name: planName, protocol, seed,
        entries: entries.map(({ draftKey: _key, parametersJSON, slaJSON, ...entry }) => ({ ...entry,
          parameters: recordJSON<CatalogPlanParameterValue>(parametersJSON, t("protocolDesign.parameters")),
          sla_thresholds: nonNegativeNumberRecord(slaJSON, t("protocolDesign.thresholds")) })) }
    await mutate(() => item ? actions.updatePlan({ ...command, id: item.id, expected_revision: item.revision }) : actions.createPlan(command), formTitle)
    onSaved()
  }}>
    <TextField label={t("common.plan")} value={name} onChange={setName} />
    <SelectField label={t("plans.executionPolicy")} value={planMode} options={[["protocol", t("plans.protocolPlan")], ["performance", t("plans.performancePlan")]]} onChange={value => { const next = value as "protocol" | "performance"; setPlanMode(next); if (next === "performance" && !isQuickPerformanceProtocol(protocol)) setProtocol("openai-chat"); setEntries([]); setTarget("") }} />
    <SelectField label={t("common.protocol")} value={protocol} options={planMode === "performance" ? protocolOptions.filter(([id]) => isQuickPerformanceProtocol(id)) : protocolOptions} onChange={value => { setProtocol(value as CatalogProtocol); setEntries([]); setTarget("") }} />
    <NumberField label={t("protocolDesign.seed")} value={seed} maximum={Number.MAX_SAFE_INTEGER} onChange={setSeed} />
    {planMode === "performance" ? <SelectField label={t("plans.performancePreset")} value={performancePreset} options={
      QUICK_PERFORMANCE_PRESET_IDS.map(id => [id, t(`quickTest:performance.presets.${id}`)] as [string, string])
    } onChange={value => setPerformancePreset(value as QuickPerformancePresetID)} /> : null}
    {planMode === "protocol" ? <>
      <FieldDescription>{t("protocolDesign.runBindingHint")}</FieldDescription>
      <PlanEntryTargetField target={target} targets={targets} onTargetChange={setTarget} onAdd={() => {
        const [kind, id] = target.split(":")
        setEntries(current => [...current, { draftKey: crypto.randomUUID(), target_kind: kind as "case" | "suite", target_id: id,
          warmup_count: 0, settings: {}, parameters: {}, parametersJSON: "{}", sla_thresholds: {}, slaJSON: "{}", load_mode: "single", concurrency: 1,
          request_count: 1, rate_per_second: 1, duration_ms: 0, request_timeout_ms: 60000 }])
      }} />
    </> : null}
    {planMode === "protocol" ? <div className="divide-y">
      {entries.map((entry, index) => <section key={entry.draftKey} className="space-y-3 py-3">
        <header className="flex min-w-0 items-center gap-2"><h3 className="min-w-0 flex-1 truncate text-xs font-semibold">{index + 1}. {targets.find(([id]) => id === `${entry.target_kind}:${entry.target_id}`)?.[1] ?? entry.target_id}</h3>
          <Button type="button" variant="ghost" size="icon-xs" disabled={index === 0} aria-label={t("protocolDesign.moveUp")} onClick={() => move(index, -1)}><ArrowUpIcon /></Button>
          <Button type="button" variant="ghost" size="icon-xs" disabled={index === entries.length - 1} aria-label={t("protocolDesign.moveDown")} onClick={() => move(index, 1)}><ArrowDownIcon /></Button>
          <Button type="button" variant="ghost" size="icon-xs" aria-label={t("protocolDesign.remove")} onClick={() => setEntries(current => current.filter((_, i) => i !== index))}><Trash2Icon /></Button>
        </header>
        <SelectField label={`${index + 1}. ${t("protocolDesign.strategy")}`} value={entry.load_mode} options={[["single",t("plans.loadSingle")],["fixed_concurrency",t("plans.loadFixed")],["open_loop",t("plans.loadOpen")]]} onChange={value => update(index, { load_mode: value as CatalogLoadMode })} />
        <div className="grid min-w-0 grid-cols-2 gap-3">
          <NumberField label={`${index + 1}. ${t("protocolDesign.count")}`} value={entry.request_count} onChange={value => update(index, { request_count: value })} />
          <NumberField label={`${index + 1}. ${t("protocolDesign.timeout")}`} value={entry.request_timeout_ms} minimum={1} onChange={value => update(index, { request_timeout_ms: value })} />
          {entry.load_mode !== "single" ? <><NumberField label={`${index + 1}. ${t("protocolDesign.concurrency")}`} value={entry.concurrency} minimum={1} onChange={value => update(index, { concurrency: value })} /><NumberField label={`${index + 1}. ${t("protocolDesign.duration")}`} value={entry.duration_ms} onChange={value => update(index, { duration_ms: value })} /></> : null}
          {entry.load_mode === "open_loop" ? <NumberField label={`${index + 1}. ${t("protocolDesign.rate")}`} value={entry.rate_per_second} minimum={0} onChange={value => update(index, { rate_per_second: value })} /> : null}
        </div>
        <div className="grid min-w-0 grid-cols-2 gap-3">
          <NumberField label={`${index + 1}. ${t("protocolDesign.warmup")}`} value={entry.warmup_count} onChange={value => update(index, { warmup_count: value })} />
          {protocolPresentation(protocol).runSettings.map(key => <OptionalNumberField key={key} label={`${index + 1}. ${t(`protocolDesign.${key}`)}`} value={entry.settings[key]} onChange={value => { const settings = { ...entry.settings }; if (value === undefined) delete settings[key]; else settings[key] = value; update(index, { settings }) }} />)}
        </div>
        <TextAreaField label={`${index + 1}. ${t("protocolDesign.parameters")}`} value={entry.parametersJSON} onChange={value => update(index, { parametersJSON: value })} description={t("protocolDesign.parametersHint")} />
        <TextAreaField label={`${index + 1}. ${t("protocolDesign.thresholds")}`} value={entry.slaJSON} onChange={value => update(index, { slaJSON: value })} />
      </section>)}
    </div> : null}
  </FormShell>
}

function PlanEntryTargetField({ target, targets, onTargetChange, onAdd }: {
  target: string
  targets: readonly (readonly [string, string])[]
  onTargetChange: (value: string) => void
  onAdd: () => void
}) {
  const { t } = useTranslation("catalog")
  const fieldKey = "planEntries"
  const label = t("protocolDesign.target")
  const validation = useCatalogValidation(fieldKey, [], label)
  const id = catalogFieldID(fieldKey)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={fieldKey} data-field-name={label}>
    <FieldLabel htmlFor={id}>{label}</FieldLabel>
    <FieldContent>
      <div className="flex min-w-0 items-center gap-2">
        <SearchableSelect value={target} onValueChange={onTargetChange} id={id} aria-label={label}
          aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined}
          className="min-w-0 flex-1" options={targets.map(([value, optionLabel]) => ({ value, label: optionLabel }))} />
        <Button type="button" variant="outline" disabled={!target} onClick={() => { validation.clear(); onAdd() }}>
          <PlusIcon />{t("protocolDesign.add")}
        </Button>
      </div>
      {validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}
    </FieldContent>
  </Field>
}

type CatalogValidationContextValue = {
  error: FormValidationError | null
  clear: (...fields: string[]) => void
}

const CatalogValidationContext = createContext<CatalogValidationContextValue | null>(null)

function FormShell({ children, label, pending, formTitle, onSubmit }: { children: ReactNode; label: string; pending: boolean; formTitle: string; onSubmit: () => Promise<void> }) {
  const { t: tx } = useTranslation()
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
      setOperationError(publicDesktopOperationErrorMessage(reason, tx("desktop:catalog_save_value", { value1: formTitle }), tx("desktop:catalog_save_did_not_complete_check_the_local_logs")))
    })
  }
  const clear = (...fields: string[]) => setValidationError((current) => current && (
    fields.includes(current.field) || fields.includes(localizeStoredMessage(current.field, tx))
  ) ? null : current)
  return <CatalogValidationContext.Provider value={{ error: validationError, clear }}>
    <form ref={formRef} className="pb-4" onSubmit={submit} noValidate>
      <FieldGroup>
        {children}
        {validationError ? (
          <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
            {tx("desktop:catalog_validation_summary", { title: formTitle, field: localizeStoredMessage(validationError.label ?? validationError.field, tx), message: localizeStoredMessage(validationError.message, tx) })}
          </FieldError>
        ) : null}
        {operationError ? <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">{localizeStoredMessage(operationError, tx)}</FieldError> : null}
      </FieldGroup>
      <SheetFooter className="px-0"><Button type="submit" className="min-w-24" disabled={pending}>{pending ? <><Spinner data-icon="inline-start" />{tx("desktop:catalog_saving")}</> : label}</Button></SheetFooter>
    </form>
  </CatalogValidationContext.Provider>
}

function TextField({ fieldKey, label, value, onChange, description, disabled = false, type = "text" }: { fieldKey?: string; label: string; value: string; onChange: (value: string) => void; description?: string; disabled?: boolean; type?: "text" | "password" }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, [], label)
  const id = catalogFieldID(key)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type={type} autoComplete={type === "password" ? "new-password" : undefined} value={value} disabled={disabled} onChange={(e) => { validation.clear(); onChange(e.target.value) }} />{description ? <FieldDescription>{description}</FieldDescription> : null}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function NumberField({ fieldKey, label, value, onChange, minimum = 0, maximum, clearFields = [] }: { fieldKey?: string; label: string; value: number; onChange: (value: number) => void; minimum?: number; maximum?: number; clearFields?: string[] }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, clearFields, label)
  const id = catalogFieldID(key)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type="number" min={minimum} max={maximum} value={value} onChange={(e) => { validation.clear(); onChange(Number(e.target.value)) }} />{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function OptionalNumberField({ label, value, onChange }: { label: string; value?: number; onChange: (value?: number) => void }) {
  const id = catalogFieldID(label)
  return <Field className="block"><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} type="number" min={1} value={value ?? ""} onChange={event => onChange(event.target.value === "" ? undefined : Number(event.target.value))} /></FieldContent></Field>
}

function TextAreaField({ fieldKey, label, value, onChange, description }: { fieldKey?: string; label: string; value: string; onChange: (value: string) => void; description?: string }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, [], label)
  const id = catalogFieldID(key)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Textarea id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} className="min-h-24 font-mono text-xs" value={value} onChange={(e) => { validation.clear(); onChange(e.target.value) }} />{description ? <FieldDescription>{description}</FieldDescription> : null}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function SelectField({ fieldKey, label, value, options, onChange, disabled = false }: { fieldKey?: string; label: string; value: string; options: readonly (readonly [string,string])[]; onChange: (value: string) => void; disabled?: boolean }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, [], label)
  const id = catalogFieldID(key)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><SearchableSelect value={value} onValueChange={(next) => { validation.clear(); onChange(next) }} disabled={disabled} id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} className="w-full" options={[...options.map(([optionID, text]) => ({value: optionID, label: text}))]} />{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function CheckField({ fieldKey, label, checked, onChange, clearFields = [], controlID, disabled }: { fieldKey?: string; label: string; checked: boolean; onChange: (value: boolean) => void; clearFields?: string[]; controlID?: string; disabled?: boolean }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, clearFields, label)
  const id = controlID ?? `${catalogFieldID(key)}-check`
  const errorID = `${id}-error`
  return <Field data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><Checkbox id={id} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} checked={checked} disabled={disabled} onCheckedChange={(value) => { validation.clear(); onChange(value === true) }} /><FieldContent><FieldLabel htmlFor={id}>{label}</FieldLabel>{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function ChoiceList({ fieldKey, label, values, selected, onChange, clearFields = [], multiColumn = false }: { multiColumn?: boolean; fieldKey?: string; label: string; values: {id:string;label:string}[]; selected: Set<string>; onChange: (value: Set<string>) => void; clearFields?: string[] }) {
  const { t: tx } = useTranslation()
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, clearFields, label)
  const errorID = `${catalogFieldID(key)}-error`
  return <fieldset className="space-y-2 rounded-lg border p-3 [&>legend]:mb-0" tabIndex={-1} data-invalid={validation.invalid || undefined} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} data-field-key={key} data-field-name={label}><legend className="px-1 text-xs font-medium">{label}</legend>{values.length ? <div className={multiColumn ? "grid grid-cols-[repeat(auto-fit,minmax(min(100%,16rem),1fr))] gap-x-4 gap-y-2 [&>[data-slot=field]]:min-w-0 [&_[data-slot=field-label]]:[overflow-wrap:anywhere]" : "space-y-2"}>{values.map(value => <CheckField key={value.id} fieldKey={`${key}.${value.id}`} controlID={`${catalogFieldID(key)}-${encodeURIComponent(value.id)}-check`} label={value.label} checked={selected.has(value.id)} onChange={(checked) => { validation.clear(); const next = new Set(selected); if (checked) next.add(value.id); else next.delete(value.id); onChange(next) }} />)}</div> : <FieldDescription>{tx("desktop:catalog_no_available_options")}</FieldDescription>}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</fieldset>
}

class FormValidationError extends Error {
  readonly field: string
  readonly label?: string

  constructor(field: string, message: string, label?: string) {
    super(message)
    this.field = field
    this.label = label
  }
}
function required(value: string, label: string, field = label) { const result = value.trim(); if (!result) throw new FormValidationError(field, tx("desktop:catalog_enter_value", { value1: label }), field === label ? undefined : label); return result }
function serviceURL(value: string) {
  const result = required(value, tx("desktop:catalog_service_url"))
  let parsed: URL
  try {
    parsed = new URL(result)
  } catch {
    throw new FormValidationError(tx("desktop:catalog_service_url"), tx("desktop:catalog_enter_an_http_or_https_service_url_with_a_hostname"))
  }
  if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname) {
    throw new FormValidationError(tx("desktop:catalog_service_url"), tx("desktop:catalog_enter_an_http_or_https_service_url_with_a_hostname"))
  }
  if (parsed.username || parsed.password) throw new FormValidationError(tx("desktop:catalog_service_url"), tx("desktop:catalog_the_service_url_must_not_contain_a_username_or_password"))
  if (parsed.search || parsed.hash) throw new FormValidationError(tx("desktop:catalog_service_url"), tx("desktop:catalog_the_service_url_must_not_contain_a_query_string_or"))
  return result
}
function safeCatalogKey(value: string, label: string) {
  const result = required(value, label)
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(result)) {
    throw new FormValidationError(label, tx("desktop:catalog_use_only_letters_digits_dots_underscores_or_hyphens_starting_with"))
  }
  return result
}
function json(value: unknown) { return JSON.stringify(value, null, 2) }
function parseJSON(value: string, label: string): unknown { try { return JSON.parse(value) } catch { throw new FormValidationError(label, tx("desktop:catalog_enter_valid_json")) } }
function recordJSON<T>(value: string, label: string): Record<string,T> { const parsed = parseJSON(value, label); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new FormValidationError(label, tx("desktop:catalog_enter_a_json_object")); return parsed as Record<string,T> }
function integerField(value: number, label: string, minimum: number, maximum: number, field = label) {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new FormValidationError(field, tx("desktop:catalog_enter_an_integer_between_value_and_value", { value1: minimum.toLocaleString(desktopLocale()), value2: maximum.toLocaleString(desktopLocale()) }), field === label ? undefined : label)
  }
}

function nonNegativeNumberRecord(value: string, label: string, field = label): Record<string, number> {
  let record: Record<string, unknown>
  try {
    record = recordJSON<unknown>(value, label)
  } catch (reason) {
    if (reason instanceof FormValidationError) throw new FormValidationError(field, reason.message, label)
    throw reason
  }
  const entries = Object.entries(record)
  for (const [name, threshold] of entries) {
    if (!name.trim() || typeof threshold !== "number" || !Number.isFinite(threshold) || threshold < 0) {
      throw new FormValidationError(field, tx("desktop:catalog_threshold_value_must_be_a_finite_number_greater_than_or", { value1: name || tx("desktop:catalog_unnamed") }), label)
    }
  }
  return record as Record<string, number>
}

function useCatalogValidation(field: string, clearFields: string[] = [], label = field) {
  const { t } = useTranslation()
  const context = useContext(CatalogValidationContext)
  const matches = context?.error && (
    context.error.field === field ||
    localizeStoredMessage(context.error.field, t) === label
  )
  const message = matches && context?.error ? localizeStoredMessage(context.error.message, t) : ""
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
  const keyedContainer = Array.from(form?.querySelectorAll<HTMLElement>("[data-field-key]") ?? [])
    .find((candidate) => candidate.dataset.fieldKey === field)
  const namedContainer = Array.from(form?.querySelectorAll<HTMLElement>("[data-field-name]") ?? [])
    .find((candidate) => candidate.dataset.fieldName === field)
  const container = keyedContainer ?? namedContainer
  const control = container?.querySelector<HTMLElement>("input:not(:disabled), textarea:not(:disabled), button:not(:disabled), [tabindex]:not([tabindex='-1'])")
    ?? (container?.matches("[tabindex]") ? container : undefined)
    ?? Array.from(form?.querySelectorAll<HTMLElement>("[aria-label]") ?? []).find((candidate) => candidate.getAttribute("aria-label") === field)
  control?.focus()
}
