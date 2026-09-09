import { PROTOCOLS, protocolOptions } from "./protocols"
import { localizeStoredMessage, desktopLocale, translateDesktop as tx } from "@/i18n/runtime"
import { caseTypeLabel } from "./presentation"
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

import type {
  CatalogActions, CatalogChannel, CatalogChannelModel, CatalogLoadMode, CatalogModel,
  CatalogPlan, CatalogPlanParameterValue, CatalogPlanSuite, CatalogProtocol, CatalogSnapshot, CatalogSuite,
  CatalogTestCase, DeleteCommand, PlanSuiteCommand, SuiteQuickTest,
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
      <SheetContent className={kind === "case" || kind === "suite" ? "data-[side=right]:w-full data-[side=right]:sm:max-w-3xl" : kind === "plan" ? "data-[side=right]:w-full data-[side=right]:sm:max-w-2xl" : "sm:max-w-lg"} onEscapeKeyDown={(event) => {
        if (event.target instanceof HTMLElement && event.target.matches('[role="combobox"][aria-expanded="true"]')) event.preventDefault()
      }}>
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

function ModelForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogModel>) {
  const { t } = useTranslation("catalog")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [capabilities, setCapabilities] = useState<string[]>(item?.capabilities ?? [])
  const suggestedCapabilities = ["chat", "tools", "vision"] as const
  const capabilityOptions = [
    ...suggestedCapabilities.map((value) => ({ value, label: `${t(`capabilities.${value}`)} (${value})` })),
    ...[...new Set([...catalog.models.flatMap((model) => model.capabilities), ...capabilities])]
      .filter((value) => !suggestedCapabilities.some((suggestion) => suggestion === value))
      .map((value) => ({ value, label: value })),
  ]
  return <FormShell pending={pending} label={t("editor.save", { noun: t("editor.noun.model") })} formTitle={formTitle} onSubmit={async () => {
    const command = { name: required(name, t("editor.fields.modelName")), protocol, capabilities }
    await mutate(() => item ? actions.updateModel({ ...command, id: item.id, expected_revision: item.revision }) : actions.createModel(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField label={t("editor.fields.modelName")} value={name} onChange={setName} />
    <SelectField label={t("common.protocol")} value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
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

function MappingForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogChannelModel>) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const [channelID, setChannelID] = useState(item?.channel_id ?? catalog.channels[0]?.id ?? "")
  const compatibleModels = catalog.models.filter((model) => model.protocol === catalog.channels.find((channel) => channel.id === channelID)?.protocol)
  const [modelID, setModelID] = useState(item?.model_id ?? compatibleModels[0]?.id ?? "")
  const [upstreamName, setUpstreamName] = useState(item?.upstream_model_name ?? "")
  return <FormShell pending={pending} label={tx("desktop:catalog_save_mapping")} formTitle={formTitle} onSubmit={async () => {
    if (!channelID) throw new FormValidationError(tx("desktop:catalog_channel"), tx("desktop:catalog_select_a_channel"))
    if (!modelID) throw new FormValidationError(tx("desktop:catalog_logical_model"), tx("desktop:catalog_select_a_logical_model"))
    await mutate(() => item
      ? actions.updateChannelModel({ id: item.id, expected_revision: item.revision, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel")) })
      : actions.createChannelModel({ channel_id: channelID, model_id: modelID, upstream_model_name: required(upstreamName, t("editor.fields.upstreamModel")) }), t("editor.savedOperation", { title: formTitle }))
    onSaved()
  }}>
    <SelectField label={t("common.channel")} value={channelID} disabled={!!item} options={catalog.channels.map((value) => [value.id, value.name])} onChange={(value) => { setChannelID(value); const protocol = catalog.channels.find((channel) => channel.id === value)?.protocol; setModelID(catalog.models.find((model) => model.protocol === protocol)?.id ?? "") }} />
    <SelectField label={t("editor.fields.logicalModel")} value={modelID} disabled={!!item} options={compatibleModels.map((value) => [value.id, value.name])} onChange={setModelID} />
    <TextField label={t("editor.fields.upstreamModel")} value={upstreamName} onChange={setUpstreamName} />
  </FormShell>
}

function CaseForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogTestCase>) {
  const { t: tx } = useTranslation()
  const availableTypes = catalog.case_types.filter((descriptor) => descriptor.creatable || descriptor.type === item?.type)
  const initialDescriptor = catalog.case_types.find((descriptor) => descriptor.type === item?.type && descriptor.type_version === item.type_version)
    ?? availableTypes.find((descriptor) => descriptor.supported_protocols.includes(item?.protocol ?? "openai-chat"))
  const initialSpec = item?.spec ?? initialDescriptor?.default_spec ?? {}
  const { t } = useTranslation("catalog")
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
  const typeOptions = availableTypes.filter((candidate) => candidate.supported_protocols.includes(value.protocol)).map((candidate) => [`${candidate.type}@${candidate.type_version}`, `${caseTypeLabel(candidate.type, candidate.label)} · v${candidate.type_version}`] as [string, string])
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
  return <FormShell pending={pending} label={tx("desktop:catalog_save_case")} formTitle={formTitle} onSubmit={async () => {
    if (!descriptor) throw new FormValidationError(tx("desktop:catalog_case_type"), tx("desktop:catalog_select_an_available_case_type"))
    if (value.type === "latency.input_ladder") {
      integerField(value.warmups_per_step, tx("desktop:catalog_default_warmups_per_step"), 0, 10)
      integerField(value.samples_per_step, tx("desktop:catalog_default_samples_per_step"), 1, 100)
      integerField(value.output_tokens, tx("desktop:catalog_output_token_limit"), 1, 65_536)
      integerField(value.timeout_ms, tx("desktop:catalog_request_timeout_ms"), 1, 600_000)
    }
    const spec = value.type === "latency.input_ladder"
      ? {
          ...recordJSON<unknown>(value.spec, tx("desktop:catalog_case_configuration")),
          stages: latencyStageSpecs(value.stages), warmups_per_step: value.warmups_per_step,
          samples_per_step: value.samples_per_step, output_tokens: value.output_tokens,
          timeout_ms: value.timeout_ms, cache_mode: value.cache_mode,
        }
      : recordJSON<unknown>(value.spec, tx("desktop:catalog_case_configuration"))
    const modelTargets = list(value.model_targets)
    if (modelTargets.length > 32) throw new FormValidationError(tx("desktop:catalog_applicable_models"), tx("desktop:catalog_enter_at_most_32_model_ids"))
    modelTargets.forEach((target) => safeModelTarget(target, tx("desktop:catalog_applicable_models")))
    if (value.default && !value.enabled) throw new FormValidationError(tx("desktop:catalog_enabled_by_default"), tx("desktop:catalog_default_cases_must_also_be_enabled"))
    const command = {
      key: safeCatalogKey(value.key, tx("desktop:catalog_case_key")), name: required(value.name, tx("desktop:catalog_case_name")), dimension: required(value.dimension, tx("desktop:catalog_dimension")),
      protocol: value.protocol, enabled: value.enabled, default: value.default, severity: value.severity as "normal" | "critical",
      model_targets: modelTargets,
      execution_mode: value.execution_mode as "automatic" | "manual", definition_schema_version: 2,
      type: value.type, type_version: value.type_version, spec,
    }
    await mutate(() => item ? actions.updateTestCase({ ...command, id: item.id, expected_revision: item.revision }) : actions.createTestCase(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <div className="grid grid-cols-2 gap-3"><TextField label={tx("desktop:catalog_case_key")} value={value.key} disabled={!!item} onChange={(v) => set("key", v)} /><TextField label={tx("desktop:catalog_case_name")} value={value.name} onChange={(v) => set("name", v)} /></div>
    <div className="grid grid-cols-2 gap-3"><TextField label={tx("desktop:catalog_dimension")} value={value.dimension} onChange={(v) => set("dimension", v)} /><SelectField label={tx("desktop:catalog_protocol")} value={value.protocol} disabled={!!item} options={protocolOptions} onChange={(v) => set("protocol", v as CatalogProtocol)} /></div>
    <TextField label={tx("desktop:catalog_applicable_models")} value={value.model_targets} onChange={(v) => set("model_targets", v)} description={tx("desktop:catalog_enter_exact_upstream_model_ids_separated_by_commas_leave_empty")} />
    <SelectField label={tx("desktop:catalog_case_type")} value={`${value.type}@${value.type_version}`} options={typeOptions} onChange={selectType} />
    {descriptor ? <FieldDescription>{descriptor.category}  {tx("desktop:catalog_scheduled_by")}{descriptor.scheduling_owner === "case" ? tx("desktop:catalog_case") : tx("desktop:catalog_plan")}{tx("desktop:catalog_separator")} {descriptor.type}@{descriptor.type_version}</FieldDescription> : null}
    <div className="grid grid-cols-2 gap-3"><SelectField label={tx("desktop:catalog_severity")} value={value.severity} options={[["normal",tx("desktop:catalog_normal")],["critical",tx("desktop:catalog_critical")]]} onChange={(v) => set("severity", v as "normal" | "critical")} /><SelectField label={tx("desktop:catalog_execution_mode")} value={value.execution_mode} options={[["automatic",tx("desktop:catalog_automatic")],["manual",tx("desktop:catalog_manual")]]} onChange={(v) => set("execution_mode", v as "automatic" | "manual")} /></div>
    <div className="grid grid-cols-2 gap-3"><CheckField label={tx("desktop:catalog_enabled")} checked={value.enabled} clearFields={[tx("desktop:catalog_enabled_by_default")]} onChange={(v) => set("enabled", v)} /><CheckField label={tx("desktop:catalog_enabled_by_default")} checked={value.default} clearFields={[tx("desktop:catalog_enabled")]} onChange={(v) => set("default", v)} /></div>
    {value.type === "latency.input_ladder" ? <>
      <div className="grid grid-cols-2 gap-3"><NumberField label={tx("desktop:catalog_default_warmups_per_step")} value={value.warmups_per_step} maximum={10} onChange={(v) => set("warmups_per_step", v)} /><NumberField label={tx("desktop:catalog_default_samples_per_step")} value={value.samples_per_step} minimum={1} maximum={100} onChange={(v) => set("samples_per_step", v)} /></div>
      <fieldset className="space-y-2 rounded-lg border p-3">
        <legend className="px-1 text-xs font-medium">{tx("desktop:catalog_input_token_ladder")}</legend>
        <FieldDescription>{tx("desktop:catalog_leave_warmups_or_samples_empty_to_inherit_the_defaults_above")}</FieldDescription>
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] gap-2 px-1 text-[10px] text-muted-foreground" aria-hidden="true">
          <span>{tx("desktop:catalog_input_tokens")}</span><span>{tx("desktop:catalog_warmup_override")}</span><span>{tx("desktop:catalog_sample_override")}</span><span className="w-12" />
        </div>
        {value.stages.map((stage, index) => <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] items-start gap-2">
          <StageNumberField label={tx("desktop:catalog_step_value_input_tokens", { value1: index + 1 })} minimum={1} maximum={1_000_000} value={stage.input_tokens} onChange={(next) => updateStage(index, { input_tokens: Number(next) })} />
          <StageNumberField label={tx("desktop:catalog_step_value_warmups", { value1: index + 1 })} minimum={0} maximum={10} value={stage.warmups} placeholder={tx("desktop:catalog_inherit_value", { value1: value.warmups_per_step })} onChange={(next) => updateStage(index, { warmups: next })} />
          <StageNumberField label={tx("desktop:catalog_step_value_samples", { value1: index + 1 })} minimum={1} maximum={100} value={stage.samples} placeholder={tx("desktop:catalog_inherit_value", { value1: value.samples_per_step })} onChange={(next) => updateStage(index, { samples: next })} />
          <Button type="button" size="sm" variant="ghost" className="w-12" disabled={value.stages.length === 1} onClick={() => removeStage(index)} aria-label={tx("desktop:catalog_delete_step_value", { value1: index + 1 })}>{tx("desktop:catalog_delete")}</Button>
        </div>)}
        <Button type="button" size="sm" variant="outline" disabled={value.stages.length >= 32 || (value.stages.at(-1)?.input_tokens ?? 0) >= 1_000_000} onClick={addStage}><PlusIcon data-icon="inline-start" />{tx("desktop:catalog_add_step")}</Button>
      </fieldset>
      <div className="grid grid-cols-2 gap-3"><NumberField label={tx("desktop:catalog_output_token_limit")} value={value.output_tokens} minimum={1} maximum={65_536} onChange={(v) => set("output_tokens", v)} /><NumberField label={tx("desktop:catalog_request_timeout_ms")} value={value.timeout_ms} minimum={1} maximum={600_000} onChange={(v) => set("timeout_ms", v)} /></div>
      <SelectField label={tx("desktop:catalog_cache_mode")} value={value.cache_mode} options={[["cold",tx("desktop:catalog_cold_cache_vary_the_probe")],["warm",tx("desktop:catalog_warm_cache_reuse_the_probe")]]} onChange={(v) => set("cache_mode", v as "cold" | "warm")} />
      <TextAreaField label={tx("desktop:catalog_advanced_configuration_json")} value={value.spec} onChange={(v) => set("spec", v)} description={tx("desktop:catalog_the_request_template_is_stored_here_saving_replaces_matching_fields")} />
    </> : <TextAreaField label={tx("desktop:catalog_case_configuration_json")} value={value.spec} onChange={(v) => set("spec", v)} description={value.type === "response.probe"
      ? tx("desktop:catalog_signatures_match_responses_using_json_pointer_probe_count_and_concurrency")
      : tx("desktop:catalog_the_selected_type_version_defines_the_configuration_structure_which_the")} />}
  </FormShell>
}

function SuiteForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogSuite>) {
  const { t: tx } = useTranslation()
  const [key, setKey] = useState(item?.key ?? "")
  const [name, setName] = useState(item?.name ?? "")
  const [protocol, setProtocol] = useState<CatalogProtocol>(item?.protocol ?? "openai-chat")
  const [modelTarget, setModelTarget] = useState(item?.model_target ?? "")
  const genericQuickTask = !!item?.quick_test && !modelTarget.trim() && !PROTOCOLS.find(({ id }) => id === protocol)?.requiresModelTargets
  const [selected, setSelected] = useState(() => new Set(item?.cases.map((ref) => ref.case_id) ?? catalog.test_cases.slice(0, 1).map(testCase => testCase.id)))
  const availableCases = catalog.test_cases.filter((testCase) =>
    testCase.protocol === protocol && (!item?.quick_test || (testCase.enabled && testCase.execution_mode === "automatic")) &&
    (genericQuickTask ? testCase.model_targets.length === 0 : !modelTarget.trim() || testCase.model_targets.length === 0 || testCase.model_targets.includes(modelTarget.trim())),
  )
  return <FormShell pending={pending} label={tx("desktop:catalog_save_suite")} formTitle={formTitle} onSubmit={async () => {
    const pinned = new Map(item?.cases.map(ref => [ref.case_id, ref.revision]) ?? [])
    const validatedKey = safeCatalogKey(key, tx("desktop:catalog_suite_key"))
    const validatedName = required(name, tx("desktop:catalog_suite_name"))
    const validatedModelTarget = genericQuickTask ? "" : safeModelTarget(modelTarget, tx("desktop:catalog_target_model"))
    const cases = availableCases.filter((testCase) => selected.has(testCase.id)).map((testCase) => ({ case_id: testCase.id, revision: pinned.get(testCase.id) ?? testCase.revision }))
    if (cases.length === 0) throw new FormValidationError(tx("desktop:catalog_included_cases"), tx("desktop:catalog_select_at_least_one_case"))
    const command = {
      key: validatedKey, name: validatedName, protocol,
      model_target: validatedModelTarget,
      cases,
      ...(item?.quick_test ? { quick_test: item.quick_test } : {}),
    }
    await mutate(() => item ? actions.updateSuite({ ...command, id: item.id, expected_revision: item.revision }) : actions.createSuite(command), tx("desktop:catalog_save_value", { value1: formTitle })); onSaved()
  }}>
    <TextField label={tx("desktop:catalog_suite_key")} value={key} onChange={setKey} disabled={!!item} description={tx("desktop:catalog_a_stable_identifier_for_suite_json_such_as_gpt_5")} />
    <TextField label={tx("desktop:catalog_suite_name")} value={name} onChange={setName} />
    <SelectField label={tx("desktop:catalog_protocol")} value={protocol} disabled={!!item} options={protocolOptions} onChange={(value) => setProtocol(value as CatalogProtocol)} />
    <TextField label={tx("desktop:catalog_target_model")} value={modelTarget} onChange={setModelTarget} description={tx("desktop:catalog_enter_the_model_identifier_used_by_the_channel_each_suite")} />
    <ChoiceList multiColumn label={tx("desktop:catalog_included_cases")} values={availableCases.map((value) => ({ id: value.id, label: value.name }))} selected={selected} onChange={setSelected} />
  </FormShell>
}

type PlanSuiteNumberDrafts = {
  concurrency: string
  request_count: string
  rate_per_second: string
  duration_ms: string
  request_timeout_ms: string
}

type PlanSuiteDraft = {
  draftKey: string
  entryID?: string
  suiteID: string
  suiteRevision: number
  suiteKey: string
  suiteName: string
  quickTest?: SuiteQuickTest
  parameters: Record<string, string | boolean>
  parametersJSON: string
  loadMode: CatalogLoadMode
  numbers: PlanSuiteNumberDrafts
  sla: string
}

function PlanForm({ item, catalog, actions, mutate, pending, formTitle, onSaved }: FormProps<CatalogPlan>) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("catalog")
  const nextDraftKey = useRef(0)
  const [name, setName] = useState(item?.name ?? "")
  const [models, setModels] = useState(() => new Set(item?.model_ids ?? []))
  const [channels, setChannels] = useState(() => new Set(item?.channel_ids ?? []))
  const [suiteID, setSuiteID] = useState(catalog.suites[0]?.id ?? "")
  const [suiteDrafts, setSuiteDrafts] = useState<PlanSuiteDraft[]>(() =>
    (item?.suites ?? []).map((entry) => planSuiteDraft(entry, catalog.suites.find((suite) => suite.id === entry.suite_id), entry.entry_id)),
  )
  const updateSuite = (draftKey: string, update: Partial<PlanSuiteDraft>) => {
    setSuiteDrafts((current) => current.map((entry) => entry.draftKey === draftKey ? { ...entry, ...update } : entry))
  }
  const addSuite = () => {
    const suite = catalog.suites.find((candidate) => candidate.id === suiteID)
    if (!suite) return
    nextDraftKey.current += 1
    setSuiteDrafts((current) => [...current, planSuiteDraft(undefined, suite, `new-${nextDraftKey.current}`)])
  }
  const moveSuite = (index: number, offset: -1 | 1) => {
    setSuiteDrafts((current) => {
      const target = index + offset
      if (target < 0 || target >= current.length) return current
      const next = [...current]
      const [moved] = next.splice(index, 1)
      next.splice(target, 0, moved)
      return next
    })
  }
  return <FormShell pending={pending} label={tx("desktop:catalog_save_plan")} formTitle={formTitle} onSubmit={async () => {
    const validatedName = required(name, tx("desktop:catalog_plan_name"), "plan.name")
    if ((models.size === 0) !== (channels.size === 0)) {
      const missingModels = models.size === 0
      throw new FormValidationError(missingModels ? "plan.models" : "plan.channels", tx("desktop:catalog_configure_both_model_and_channel_restrictions_or_leave_both_empty"), missingModels ? tx("desktop:catalog_model") : tx("desktop:catalog_channel"))
    }
    if (suiteDrafts.length === 0) {
      throw new FormValidationError("plan.suites", tx("desktop:catalog_select_at_least_one_suite"), tx("desktop:catalog_plan_suites"))
    }
    const command = {
      name: validatedName,
      model_ids: [...models],
      channel_ids: [...channels],
      suites: suiteDrafts.map(planSuiteCommand),
    }
    await mutate(() => item ? actions.updatePlan({ ...command, id: item.id, expected_revision: item.revision }) : actions.createPlan(command), t("editor.savedOperation", { title: formTitle })); onSaved()
  }}>
    <TextField fieldKey="plan.name" label={tx("desktop:catalog_plan_name")} value={name} onChange={setName} />
    <div className="grid grid-cols-2 gap-3">
      <ChoiceList fieldKey="plan.models" label={tx("desktop:catalog_model")} values={catalog.models.map(v => ({ id: v.id, label: v.name }))} selected={models} clearFields={["plan.channels"]} onChange={setModels} />
      <ChoiceList fieldKey="plan.channels" label={tx("desktop:catalog_channel")} values={catalog.channels.map(v => ({ id: v.id, label: v.name }))} selected={channels} clearFields={["plan.models"]} onChange={setChannels} />
    </div>
    <FieldDescription>{tx("desktop:catalog_when_model_and_channel_are_empty_choose_a_mapped_target")}</FieldDescription>
    <PlanSuiteList
      entries={suiteDrafts}
      suites={catalog.suites}
      suiteID={suiteID}
      pending={pending}
      onSuiteIDChange={setSuiteID}
      onAdd={addSuite}
      onUpdate={updateSuite}
      onMove={moveSuite}
      onRemove={(draftKey) => setSuiteDrafts((current) => current.filter((entry) => entry.draftKey !== draftKey))}
    />
  </FormShell>
}

function PlanSuiteList({ entries, suites, suiteID, pending, onSuiteIDChange, onAdd, onUpdate, onMove, onRemove }: {
  entries: PlanSuiteDraft[]
  suites: CatalogSuite[]
  suiteID: string
  pending: boolean
  onSuiteIDChange: (value: string) => void
  onAdd: () => void
  onUpdate: (draftKey: string, update: Partial<PlanSuiteDraft>) => void
  onMove: (index: number, offset: -1 | 1) => void
  onRemove: (draftKey: string) => void
}) {
  const { t: tx } = useTranslation()
  const validation = useCatalogValidation("plan.suites")
  const errorID = "catalog-plan-suites-error"
  return <fieldset className="space-y-3 rounded-lg border p-3" tabIndex={-1} data-invalid={validation.invalid || undefined} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} data-field-key="plan.suites">
    <legend className="px-1 text-xs font-medium">{tx("desktop:catalog_plan_suites")}</legend>
    <FieldDescription>{tx("desktop:catalog_plan_suites_run_in_order_and_continue_after_failure")}</FieldDescription>
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2">
      <SelectField fieldKey="plan.suite-picker" label={tx("desktop:catalog_suite")} value={suiteID} disabled={!suites.length || pending} options={suites.map((suite) => [suite.id, `${suite.name} · r${suite.revision}`])} onChange={(value) => { validation.clear(); onSuiteIDChange(value) }} />
      <Button type="button" size="sm" variant="outline" disabled={!suiteID || pending} onClick={() => { validation.clear(); onAdd() }}>
        <PlusIcon data-icon="inline-start" />{tx("desktop:catalog_add_suite")}
      </Button>
    </div>
    {!suites.length ? <FieldDescription>{tx("desktop:catalog_no_available_options")}</FieldDescription> : null}
    <div className="space-y-3">
      {entries.map((entry, index) => <PlanSuiteEntryEditor key={entry.draftKey} entry={entry} index={index} count={entries.length} pending={pending} onUpdate={(update) => onUpdate(entry.draftKey, update)} onMove={onMove} onRemove={() => onRemove(entry.draftKey)} />)}
    </div>
    {validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}
  </fieldset>
}

function PlanSuiteEntryEditor({ entry, index, count, pending, onUpdate, onMove, onRemove }: {
  entry: PlanSuiteDraft
  index: number
  count: number
  pending: boolean
  onUpdate: (update: Partial<PlanSuiteDraft>) => void
  onMove: (index: number, offset: -1 | 1) => void
  onRemove: () => void
}) {
  const { t: tx } = useTranslation()
  const prefix = `plan.suites.${entry.draftKey}`
  const updateNumber = (name: keyof PlanSuiteNumberDrafts, value: string) => onUpdate({ numbers: { ...entry.numbers, [name]: value } })
  return <fieldset className="min-w-0 space-y-3 rounded-lg border bg-muted/20 p-3" disabled={pending}>
    <legend className="sr-only">{tx("desktop:catalog_suite_position_name", { value1: index + 1, value2: entry.suiteName })}</legend>
    <div className="flex min-w-0 items-center gap-2">
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-medium">{index + 1}. {entry.suiteName}</div>
        <div className="truncate text-[11px] text-muted-foreground">{entry.suiteKey} · r{entry.suiteRevision}</div>
      </div>
      <Button type="button" size="icon-sm" variant="ghost" disabled={index === 0 || pending} aria-label={tx("desktop:catalog_move_suite_up", { value1: index + 1, value2: entry.suiteName })} title={tx("desktop:catalog_move_suite_up", { value1: index + 1, value2: entry.suiteName })} onClick={() => onMove(index, -1)}><ArrowUpIcon /></Button>
      <Button type="button" size="icon-sm" variant="ghost" disabled={index === count - 1 || pending} aria-label={tx("desktop:catalog_move_suite_down", { value1: index + 1, value2: entry.suiteName })} title={tx("desktop:catalog_move_suite_down", { value1: index + 1, value2: entry.suiteName })} onClick={() => onMove(index, 1)}><ArrowDownIcon /></Button>
      <Button type="button" size="icon-sm" variant="ghost" disabled={pending} aria-label={tx("desktop:catalog_remove_suite", { value1: index + 1, value2: entry.suiteName })} title={tx("desktop:catalog_remove_suite", { value1: index + 1, value2: entry.suiteName })} onClick={onRemove}><Trash2Icon /></Button>
    </div>
    <SelectField fieldKey={`${prefix}.load_mode`} label={tx("desktop:catalog_load_mode")} value={entry.loadMode} options={[["single",tx("desktop:catalog_single_request")],["fixed_concurrency",tx("desktop:catalog_fixed_concurrency")],["open_loop",tx("desktop:catalog_open_loop")]]} onChange={(value) => onUpdate({ loadMode: value as CatalogLoadMode })} />
    <div className="grid grid-cols-2 gap-3">
      <DraftNumberField fieldKey={`${prefix}.concurrency`} label={tx("desktop:catalog_concurrency")} value={entry.numbers.concurrency} minimum={1} onChange={(value) => updateNumber("concurrency", value)} />
      <DraftNumberField fieldKey={`${prefix}.request_count`} label={tx("desktop:catalog_request_count")} value={entry.numbers.request_count} minimum={0} clearFields={[`${prefix}.duration_ms`]} onChange={(value) => updateNumber("request_count", value)} />
      <DraftNumberField fieldKey={`${prefix}.rate_per_second`} label={tx("desktop:catalog_requests_per_second")} value={entry.numbers.rate_per_second} minimum={0} step="any" onChange={(value) => updateNumber("rate_per_second", value)} />
      <DraftNumberField fieldKey={`${prefix}.duration_ms`} label={tx("desktop:catalog_duration_ms")} value={entry.numbers.duration_ms} minimum={0} clearFields={[`${prefix}.request_count`]} onChange={(value) => updateNumber("duration_ms", value)} />
      <DraftNumberField fieldKey={`${prefix}.request_timeout_ms`} label={tx("desktop:catalog_request_timeout_ms")} value={entry.numbers.request_timeout_ms} minimum={1} onChange={(value) => updateNumber("request_timeout_ms", value)} />
    </div>
    <TextAreaField fieldKey={`${prefix}.sla_thresholds`} label={tx("desktop:catalog_sla_thresholds_json")} value={entry.sla} onChange={(value) => onUpdate({ sla: value })} />
    <PlanSuiteParameterFields entry={entry} fieldPrefix={prefix} onUpdate={onUpdate} />
  </fieldset>
}

function PlanSuiteParameterFields({ entry, fieldPrefix, onUpdate }: { entry: PlanSuiteDraft; fieldPrefix: string; onUpdate: (update: Partial<PlanSuiteDraft>) => void }) {
  const { t: tx } = useTranslation()
  const inputs = entry.quickTest?.inputs ?? []
  if (!inputs.length) {
    if (entry.parametersJSON !== "{}") return <TextAreaField fieldKey={`${fieldPrefix}.parameters`} label={tx("desktop:catalog_extension_parameters_json")} value={entry.parametersJSON} onChange={(parametersJSON) => onUpdate({ parametersJSON })} />
    return <FieldDescription>{tx("desktop:catalog_suite_has_no_extension_parameters")}</FieldDescription>
  }
  return <fieldset className="space-y-3">
    <legend className="text-xs font-medium">{tx("desktop:catalog_suite_parameters")}</legend>
    {inputs.map((input) => {
      const fieldKey = `${fieldPrefix}.parameters.${input.key}`
      const value = entry.parameters[input.key]
      if (input.type === "boolean") return <CheckField key={input.key} fieldKey={fieldKey} label={input.label} checked={value === true} onChange={(checked) => onUpdate({ parameters: { ...entry.parameters, [input.key]: checked } })} />
      if (input.type === "number") return <DraftNumberField key={input.key} fieldKey={fieldKey} label={input.label} value={typeof value === "string" ? value : ""} step="any" onChange={(next) => onUpdate({ parameters: { ...entry.parameters, [input.key]: next } })} />
      return <TextField key={input.key} fieldKey={fieldKey} label={input.label} value={typeof value === "string" ? value : ""} onChange={(next) => onUpdate({ parameters: { ...entry.parameters, [input.key]: next } })} />
    })}
  </fieldset>
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
function DraftNumberField({ fieldKey, label, value, onChange, minimum, maximum, step, clearFields = [] }: { fieldKey: string; label: string; value: string; onChange: (value: string) => void; minimum?: number; maximum?: number; step?: string; clearFields?: string[] }) {
  const validation = useCatalogValidation(fieldKey, clearFields, label)
  const id = catalogFieldID(fieldKey)
  const errorID = `${id}-error`
  return <Field className="block" data-invalid={validation.invalid || undefined} data-field-key={fieldKey} data-field-name={label}><FieldLabel htmlFor={id}>{label}</FieldLabel><FieldContent><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type="number" min={minimum} max={maximum} step={step} value={value} onChange={(event) => { validation.clear(); onChange(event.target.value) }} />{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
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
function CheckField({ fieldKey, label, checked, onChange, clearFields = [], controlID }: { fieldKey?: string; label: string; checked: boolean; onChange: (value: boolean) => void; clearFields?: string[]; controlID?: string }) {
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, clearFields, label)
  const id = controlID ?? `${catalogFieldID(key)}-check`
  const errorID = `${id}-error`
  return <Field data-invalid={validation.invalid || undefined} data-field-key={key} data-field-name={label}><Checkbox id={id} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} checked={checked} onCheckedChange={(value) => { validation.clear(); onChange(value === true) }} /><FieldContent><FieldLabel htmlFor={id}>{label}</FieldLabel>{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</FieldContent></Field>
}
function ChoiceList({ fieldKey, label, values, selected, onChange, clearFields = [], multiColumn = false }: { multiColumn?: boolean; fieldKey?: string; label: string; values: {id:string;label:string}[]; selected: Set<string>; onChange: (value: Set<string>) => void; clearFields?: string[] }) {
  const { t: tx } = useTranslation()
  const key = fieldKey ?? label
  const validation = useCatalogValidation(key, clearFields, label)
  const errorID = `${catalogFieldID(key)}-error`
  return <fieldset className="space-y-2 rounded-lg border p-3" tabIndex={-1} data-invalid={validation.invalid || undefined} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} data-field-key={key} data-field-name={label}><legend className="px-1 text-xs font-medium">{label}</legend>{values.length ? <div className={multiColumn ? "grid grid-cols-[repeat(auto-fit,minmax(min(100%,16rem),1fr))] gap-x-4 gap-y-2 [&>[data-slot=field]]:min-w-0 [&_[data-slot=field-label]]:[overflow-wrap:anywhere]" : "space-y-2"}>{values.map(value => <CheckField key={value.id} fieldKey={`${key}.${value.id}`} controlID={`${catalogFieldID(key)}-${encodeURIComponent(value.id)}-check`} label={value.label} checked={selected.has(value.id)} onChange={(checked) => { validation.clear(); const next = new Set(selected); if (checked) next.add(value.id); else next.delete(value.id); onChange(next) }} />)}</div> : <FieldDescription>{tx("desktop:catalog_no_available_options")}</FieldDescription>}{validation.message ? <FieldError id={errorID}>{validation.message}</FieldError> : null}</fieldset>
}

function StageNumberField({ label, value, minimum, maximum, placeholder, onChange }: { label: string; value: string | number; minimum: number; maximum: number; placeholder?: string; onChange: (value: string) => void }) {
  const validation = useCatalogValidation(label)
  const id = catalogFieldID(label)
  const errorID = `${id}-error`
  return <div data-invalid={validation.invalid || undefined} data-field-name={label}><Input id={id} aria-label={label} aria-invalid={validation.invalid || undefined} aria-describedby={validation.invalid ? errorID : undefined} type="number" min={minimum} max={maximum} value={value} placeholder={placeholder} onChange={(event) => { validation.clear(); onChange(event.target.value) }} />{validation.message ? <FieldError id={errorID} className="mt-1">{validation.message}</FieldError> : null}</div>
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
function safeModelTarget(value: string, label: string) {
  const result = required(value, label)
  if (new TextEncoder().encode(result).length > 256 || /\p{Cc}/u.test(result)) throw new FormValidationError(label, tx("desktop:catalog_model_ids_must_be_at_most_256_utf_8_bytes"))
  return result
}
function list(value: string) { return [...new Set(value.split(",").map(v => v.trim()).filter(Boolean))] }
function json(value: unknown) { return JSON.stringify(value, null, 2) }
function parseJSON(value: string, label: string): unknown { try { return JSON.parse(value) } catch { throw new FormValidationError(label, tx("desktop:catalog_enter_valid_json")) } }
function recordJSON<T>(value: string, label: string): Record<string,T> { const parsed = parseJSON(value, label); if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new FormValidationError(label, tx("desktop:catalog_enter_a_json_object")); return parsed as Record<string,T> }
function finiteNumber(value: unknown, fallback: number): number { return typeof value === "number" && Number.isFinite(value) ? value : fallback }

function planSuiteDraft(entry: CatalogPlanSuite | undefined, suite: CatalogSuite | undefined, draftKey: string): PlanSuiteDraft {
  if (!entry && !suite) throw new Error("a plan suite draft requires a catalog suite")
  const quickTest = entry ? entry.quick_test : suite?.quick_test
  const parameterValues = {
    ...Object.fromEntries((quickTest?.inputs ?? []).map((input) => [input.key, input.default])),
    ...(entry?.parameters ?? {}),
  }
  const parameters = Object.fromEntries(Object.entries(parameterValues).map(([key, value]) => [
    key,
    typeof value === "boolean" ? value : String(value),
  ])) as Record<string, string | boolean>
  return {
    draftKey,
    ...(entry?.entry_id ? { entryID: entry.entry_id } : {}),
    suiteID: entry?.suite_id ?? suite!.id,
    suiteRevision: entry?.suite_revision ?? suite!.revision,
    suiteKey: entry?.suite_key ?? suite!.key,
    suiteName: entry?.suite_name ?? suite!.name,
    ...(quickTest ? { quickTest } : {}),
    parameters,
    parametersJSON: json(entry?.parameters ?? {}),
    loadMode: entry?.load_mode ?? "single",
    numbers: {
      concurrency: String(entry?.concurrency ?? 1),
      request_count: String(entry?.request_count ?? 1),
      rate_per_second: String(entry?.rate_per_second ?? 0),
      duration_ms: String(entry?.duration_ms ?? 0),
      request_timeout_ms: String(entry?.request_timeout_ms ?? quickTest?.timeout_ms ?? 60_000),
    },
    sla: json(entry?.sla_thresholds ?? { e2e_p95_ms: 3_000 }),
  }
}

function planSuiteCommand(draft: PlanSuiteDraft): PlanSuiteCommand {
  const prefix = `plan.suites.${draft.draftKey}`
  const numbers = Object.fromEntries(Object.entries(draft.numbers).map(([key, value]) => [key, value.trim() ? Number(value) : Number.NaN])) as {
    concurrency: number
    request_count: number
    rate_per_second: number
    duration_ms: number
    request_timeout_ms: number
  }
  validatePlanLoad(draft.loadMode, numbers, prefix)
  const inputs = draft.quickTest?.inputs ?? []
  let parameters: Record<string, CatalogPlanParameterValue>
  if (inputs.length) {
    parameters = Object.fromEntries(inputs.map((input) => {
      const fieldKey = `${prefix}.parameters.${input.key}`
      const value = draft.parameters[input.key]
      if (input.type === "boolean") return [input.key, value === true]
      if (input.type === "number") {
        const parsed = typeof value === "string" && value.trim() ? Number(value) : Number.NaN
        if (!Number.isFinite(parsed)) throw new FormValidationError(fieldKey, tx("desktop:catalog_enter_a_finite_number"), input.label)
        return [input.key, parsed]
      }
      return [input.key, typeof value === "string" ? value : String(value ?? "")]
    }))
  } else {
    parameters = primitiveParameterRecord(draft.parametersJSON, `${prefix}.parameters`, tx("desktop:catalog_extension_parameters_json"))
  }
  return {
    ...(draft.entryID ? { entry_id: draft.entryID } : {}),
    suite_id: draft.suiteID,
    suite_revision: draft.suiteRevision,
    parameters,
    load_mode: draft.loadMode,
    ...numbers,
    sla_thresholds: nonNegativeNumberRecord(draft.sla, tx("desktop:catalog_sla_thresholds_json"), `${prefix}.sla_thresholds`),
  }
}

function primitiveParameterRecord(value: string, field: string, label: string): Record<string, CatalogPlanParameterValue> {
  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    throw new FormValidationError(field, tx("desktop:catalog_enter_valid_json"), label)
  }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new FormValidationError(field, tx("desktop:catalog_enter_a_json_object"), label)
  }
  if (Object.values(parsed).some((parameter) => !(
    typeof parameter === "string" || typeof parameter === "boolean" ||
    (typeof parameter === "number" && Number.isFinite(parameter))
  ))) {
    throw new FormValidationError(field, tx("desktop:catalog_extension_parameters_must_be_primitive_values"), label)
  }
  return parsed as Record<string, CatalogPlanParameterValue>
}

function integerField(value: number, label: string, minimum: number, maximum: number, field = label) {
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new FormValidationError(field, tx("desktop:catalog_enter_an_integer_between_value_and_value", { value1: minimum.toLocaleString(desktopLocale()), value2: maximum.toLocaleString(desktopLocale()) }), field === label ? undefined : label)
  }
}

function validatePlanLoad(loadMode: CatalogLoadMode, numbers: { concurrency: number; request_count: number; rate_per_second: number; duration_ms: number; request_timeout_ms: number }, prefix: string) {
  integerField(numbers.concurrency, tx("desktop:catalog_concurrency"), 1, Number.MAX_SAFE_INTEGER, `${prefix}.concurrency`)
  integerField(numbers.request_count, tx("desktop:catalog_request_count"), 0, Number.MAX_SAFE_INTEGER, `${prefix}.request_count`)
  integerField(numbers.duration_ms, tx("desktop:catalog_duration_ms"), 0, Number.MAX_SAFE_INTEGER, `${prefix}.duration_ms`)
  if (numbers.request_count === 0 && numbers.duration_ms === 0) {
    throw new FormValidationError(`${prefix}.request_count`, tx("desktop:catalog_request_count_and_duration_cannot_both_be_0"), tx("desktop:catalog_request_count"))
  }
  if (!Number.isFinite(numbers.rate_per_second) || numbers.rate_per_second < 0) {
    throw new FormValidationError(`${prefix}.rate_per_second`, tx("desktop:catalog_enter_a_finite_number_greater_than_or_equal_to_0"), tx("desktop:catalog_requests_per_second"))
  }
  if (loadMode === "open_loop" && numbers.rate_per_second <= 0) {
    throw new FormValidationError(`${prefix}.rate_per_second`, tx("desktop:catalog_requests_per_second_must_be_greater_than_0_for_open"), tx("desktop:catalog_requests_per_second"))
  }
  integerField(numbers.request_timeout_ms, tx("desktop:catalog_request_timeout_ms"), 1, Number.MAX_SAFE_INTEGER, `${prefix}.request_timeout_ms`)
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
  if (entries.length === 0) throw new FormValidationError(field, tx("desktop:catalog_enter_at_least_one_sla_threshold"), label)
  for (const [name, threshold] of entries) {
    if (!name.trim() || typeof threshold !== "number" || !Number.isFinite(threshold) || threshold < 0) {
      throw new FormValidationError(field, tx("desktop:catalog_threshold_value_must_be_a_finite_number_greater_than_or", { value1: name || tx("desktop:catalog_unnamed") }), label)
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
  if (!stages.length) throw new FormValidationError(tx("desktop:catalog_input_token_ladder"), tx("desktop:catalog_add_at_least_one_input_token_step"))
  let previous = 0
  return stages.map((stage, index) => {
    if (!Number.isInteger(stage.input_tokens) || stage.input_tokens <= previous || stage.input_tokens > 1_000_000) {
      throw new FormValidationError(tx("desktop:catalog_step_value_input_tokens", { value1: index + 1 }), tx("desktop:catalog_enter_increasing_integers_no_greater_than_1_000_000"))
    }
    previous = stage.input_tokens
    const warmups = optionalInteger(stage.warmups, tx("desktop:catalog_step_value_warmups", { value1: index + 1 }), 0, 10)
    const samples = optionalInteger(stage.samples, tx("desktop:catalog_step_value_samples", { value1: index + 1 }), 1, 100)
    return { input_tokens: stage.input_tokens, ...(warmups === undefined ? {} : { warmups }), ...(samples === undefined ? {} : { samples }) }
  })
}

function optionalInteger(value: string, label: string, minimum: number, maximum: number): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < minimum || parsed > maximum) throw new FormValidationError(label, tx("desktop:catalog_enter_an_integer_between_value_and_value", { value1: minimum, value2: maximum }))
  return parsed
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
