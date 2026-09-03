import { useMemo, useState } from "react"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  EmptyInspector,
  InspectorHeader,
  InspectorRow,
  PageFrame,
} from "@/features/shell/page-frame"

import type {
  CatalogActions,
  CatalogChannel,
  CatalogChannelModel,
  CatalogModel,
  CatalogPlan,
  CatalogSnapshot,
  CatalogSuite,
  CatalogTestCase,
} from "./data"
import { CatalogEditor, DeleteCatalogButton, type CatalogMutation } from "./catalog-editors"

const PROTOCOL_LABELS = {
  "openai-chat": "OpenAI Chat",
  "kimi-k3": "Kimi K3",
  seedance: "Seedance",
} as const

interface CatalogWorkspaceProps {
  catalog: CatalogSnapshot
  actions: CatalogActions
  mutate: CatalogMutation
  mutationPending: boolean
  mutationError: string
}

export function ModelChannelWorkspace({ catalog, actions, mutate, mutationPending, mutationError }: CatalogWorkspaceProps) {
  const { t } = useTranslation("catalog")
  const [tab, setTab] = useState<"models" | "channels" | "mappings">("models")
  const [selectedModelID, setSelectedModelID] = useState("")
  const [selectedChannelID, setSelectedChannelID] = useState("")
  const [selectedMappingID, setSelectedMappingID] = useState("")
  const selectedModel =
    catalog.models.find((model) => model.id === selectedModelID) ?? catalog.models[0]
  const selectedChannel =
    catalog.channels.find((channel) => channel.id === selectedChannelID) ?? catalog.channels[0]
  const selectedMapping = catalog.channel_models.find((mapping) => mapping.id === selectedMappingID) ?? catalog.channel_models[0]
  const channelNames = useMemo(
    () => new Map(catalog.channels.map((channel) => [channel.id, channel.name])),
    [catalog.channels],
  )
  const modelNames = useMemo(
    () => new Map(catalog.models.map((model) => [model.id, model.name])),
    [catalog.models],
  )

  const inspector =
    tab === "models" ? (
      selectedModel ? (
        <ModelInspector model={selectedModel} catalog={catalog} channelNames={channelNames} />
      ) : (
        <EmptyInspector label={t("models.noneSelectedModel")} />
      )
    ) : tab === "channels" && selectedChannel ? (
      <ChannelInspector channel={selectedChannel} catalog={catalog} modelNames={modelNames} />
    ) : tab === "mappings" && selectedMapping ? (
      <MappingInspector mapping={selectedMapping} channelNames={channelNames} modelNames={modelNames} />
    ) : (
      <EmptyInspector label={t("models.noneSelected")} />
    )

  const selected = tab === "models" ? selectedModel : tab === "channels" ? selectedChannel : selectedMapping
  const deleteAction = tab === "models" ? actions.deleteModel : tab === "channels" ? actions.deleteChannel : actions.deleteChannelModel
  const kind = tab === "models" ? "model" : tab === "channels" ? "channel" : "mapping"

  return (
    <PageFrame
      title={t("models.title")}
      description={t("models.description")}
      count={t("models.count", { models: catalog.models.length, channels: catalog.channels.length })}
      inspector={inspector}
      inspectorLabel={t(tab === "models" ? "models.modelDetails" : tab === "channels" ? "models.channelDetails" : "models.mappingDetails")}
      actions={<><CatalogEditor kind={kind} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} />{selected ? <CatalogEditor key={`${kind}-${selected.id}`} kind={kind} item={selected} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} /> : null}<DeleteCatalogButton kind={kind} item={selected} action={deleteAction} mutate={mutate} pending={mutationPending} /></>}
    >
      {mutationError ? <div role="alert" className="border-t px-4 py-2 text-xs text-destructive">{mutationError}</div> : null}
      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value as "models" | "channels" | "mappings")}
        className="min-h-0 flex-1 gap-0"
      >
        <TabsList variant="line" className="mx-4 h-8">
          <TabsTrigger value="models" className="text-xs">
            {t("models.tabs.models", { count: catalog.models.length })}
          </TabsTrigger>
          <TabsTrigger value="channels" className="text-xs">
            {t("models.tabs.channels", { count: catalog.channels.length })}
          </TabsTrigger>
          <TabsTrigger value="mappings" className="text-xs">
            {t("models.tabs.mappings", { count: catalog.channel_models.length })}
          </TabsTrigger>
        </TabsList>
        <Separator />
        {tab === "models" ? (
          <ModelTable
            models={catalog.models}
            selectedID={selectedModel?.id ?? ""}
            onSelect={setSelectedModelID}
          />
        ) : tab === "channels" ? (
          <ChannelTable
            channels={catalog.channels}
            selectedID={selectedChannel?.id ?? ""}
            onSelect={setSelectedChannelID}
          />
        ) : (
          <MappingTable mappings={catalog.channel_models} selectedID={selectedMapping?.id ?? ""} onSelect={setSelectedMappingID} channelNames={channelNames} modelNames={modelNames} />
        )}
      </Tabs>
    </PageFrame>
  )
}

function MappingTable({ mappings, selectedID, onSelect, channelNames, modelNames }: { mappings: CatalogChannelModel[]; selectedID: string; onSelect: (id: string) => void; channelNames: Map<string,string>; modelNames: Map<string,string> }) {
  const { t } = useTranslation("catalog")
  if (!mappings.length) return <CatalogEmpty title={t("models.mappingEmpty")} description={t("models.mappingEmptyDescription")} />
  return <ScrollArea className="min-h-0 flex-1"><Table aria-label={t("models.mappingAria")}><TableHeader><TableRow><TableHead className="pl-4">{t("common.channel")}</TableHead><TableHead>{t("models.logicalModel")}</TableHead><TableHead>{t("models.upstreamName")}</TableHead><TableHead>{t("common.version")}</TableHead></TableRow></TableHeader><TableBody>{mappings.map(mapping => <TableRow key={mapping.id} data-state={mapping.id === selectedID ? "selected" : undefined} onClick={() => onSelect(mapping.id)}><TableCell className="pl-4 text-xs">{channelNames.get(mapping.channel_id)}</TableCell><TableCell className="text-xs">{modelNames.get(mapping.model_id)}</TableCell><TableCell className="font-mono text-xs">{mapping.upstream_model_name}</TableCell><TableCell className="text-xs">r{mapping.revision}</TableCell></TableRow>)}</TableBody></Table></ScrollArea>
}

function MappingInspector({ mapping, channelNames, modelNames }: { mapping: CatalogChannelModel; channelNames: Map<string,string>; modelNames: Map<string,string> }) {
  const { t } = useTranslation("catalog")
  return <><InspectorHeader title={mapping.upstream_model_name} subtitle={mapping.id} /><Separator /><dl className="space-y-1 px-4 py-2"><InspectorRow label={t("common.version")} value={`r${mapping.revision}`} /><InspectorRow label={t("common.channel")} value={channelNames.get(mapping.channel_id) ?? t("common.unknownChannel")} /><InspectorRow label={t("models.logicalModel")} value={modelNames.get(mapping.model_id) ?? t("common.unknownModel")} /></dl></>
}

function ModelTable({
  models,
  selectedID,
  onSelect,
}: {
  models: CatalogModel[]
  selectedID: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation("catalog")
  if (models.length === 0) {
    return <CatalogEmpty title={t("models.modelEmpty")} description={t("models.modelEmptyDescription")} />
  }
  return (
    <ScrollArea className="min-h-0 flex-1">
      <Table aria-label={t("models.modelAria")} className="min-w-[620px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 pl-4 text-[11px]">{t("common.model")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("common.protocol")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("common.capabilities")}</TableHead>
            <TableHead className="h-8 text-right text-[11px]">{t("common.version")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {models.map((model) => (
            <TableRow
              key={model.id}
              data-state={model.id === selectedID ? "selected" : undefined}
              aria-selected={model.id === selectedID}
              onClick={() => onSelect(model.id)}
              className="dense-table-row h-11"
            >
              <TableCell className="py-1 pl-4">
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto justify-start p-0 text-xs no-underline hover:no-underline"
                  aria-label={t("models.viewModel", { name: model.name })}
                >
                  {model.name}
                </Button>
                <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                  {model.id}
                </div>
              </TableCell>
              <TableCell className="py-1 text-xs">{PROTOCOL_LABELS[model.protocol]}</TableCell>
              <TableCell className="py-1 text-[11px] text-muted-foreground">
                {model.capabilities.join(" · ") || t("common.notSpecified")}
              </TableCell>
              <TableCell className="py-1 text-right text-xs tabular-nums">r{model.revision}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </ScrollArea>
  )
}

function ChannelTable({
  channels,
  selectedID,
  onSelect,
}: {
  channels: CatalogChannel[]
  selectedID: string
  onSelect: (id: string) => void
}) {
  const { t } = useTranslation("catalog")
  if (channels.length === 0) {
    return <CatalogEmpty title={t("models.channelEmpty")} description={t("models.channelEmptyDescription")} />
  }
  return (
    <ScrollArea className="min-h-0 flex-1">
      <Table aria-label={t("models.channelAria")} className="min-w-[700px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 pl-4 text-[11px]">{t("common.channel")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("common.status")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("models.serviceUrl")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("common.mapping")}</TableHead>
            <TableHead className="h-8 text-[11px]">{t("models.credential")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {channels.map((channel) => (
            <TableRow
              key={channel.id}
              data-state={channel.id === selectedID ? "selected" : undefined}
              aria-selected={channel.id === selectedID}
              onClick={() => onSelect(channel.id)}
              className="dense-table-row h-11"
            >
              <TableCell className="py-1 pl-4">
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto justify-start p-0 text-xs no-underline hover:no-underline"
                  aria-label={t("models.viewChannel", { name: channel.name })}
                >
                  {channel.name}
                </Button>
                <div className="mt-0.5 text-[10px] text-muted-foreground">
                  {PROTOCOL_LABELS[channel.protocol]} · r{channel.revision}
                </div>
              </TableCell>
              <TableCell className="py-1">
                <StateBadge enabled={channel.enabled} />
              </TableCell>
              <TableCell className="max-w-[260px] truncate py-1 font-mono text-[10px] text-muted-foreground">
                {channel.base_url}
              </TableCell>
              <TableCell className="py-1 text-xs tabular-nums">{channel.model_count}</TableCell>
              <TableCell className="py-1 text-xs">
                {t(channel.credential_configured ? "models.configured" : "models.notConfigured")}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </ScrollArea>
  )
}

function ModelInspector({
  model,
  catalog,
  channelNames,
}: {
  model: CatalogModel
  catalog: CatalogSnapshot
  channelNames: Map<string, string>
}) {
  const { t } = useTranslation("catalog")
  const mappings = catalog.channel_models.filter((mapping) => mapping.model_id === model.id)
  return (
    <>
      <InspectorHeader title={model.name} subtitle={model.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label={t("common.version")} value={`r${model.revision}`} />
        <InspectorRow label={t("common.protocol")} value={PROTOCOL_LABELS[model.protocol]} />
        <InspectorRow label={t("common.capabilities")} value={model.capabilities.join(" · ") || t("common.notSpecified")} />
        <InspectorRow label={t("models.channelMappings")} value={t("common.countMappings", { count: mappings.length })} />
        <InspectorRow
          label={t("models.upstreamName")}
          value={
            mappings
              .map((mapping) => `${channelNames.get(mapping.channel_id) ?? t("common.unknownChannel")} · ${mapping.upstream_model_name}`)
              .join("; ") || t("models.unboundChannels")
          }
        />
      </dl>
    </>
  )
}

function ChannelInspector({
  channel,
  catalog,
  modelNames,
}: {
  channel: CatalogChannel
  catalog: CatalogSnapshot
  modelNames: Map<string, string>
}) {
  const { t } = useTranslation("catalog")
  const mappings = catalog.channel_models.filter((mapping) => mapping.channel_id === channel.id)
  return (
    <>
      <InspectorHeader title={channel.name} subtitle={channel.id} trailing={<StateBadge enabled={channel.enabled} />} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label={t("models.versionProtocol")} value={`r${channel.revision} · ${PROTOCOL_LABELS[channel.protocol]}`} />
        <InspectorRow label="Base URL" value={channel.base_url} />
        <InspectorRow label={t("models.credential")} value={t(channel.credential_configured ? "models.credentialConfigured" : "models.credentialNotConfigured")} />
        <InspectorRow label={t("models.modelMappings")} value={t("common.countMappings", { count: mappings.length })} />
        <InspectorRow
          label={t("models.logicalModel")}
          value={mappings.map((mapping) => modelNames.get(mapping.model_id) ?? t("common.unknownModel")).join(" · ") || t("models.unboundModels")}
        />
      </dl>
    </>
  )
}

export function CasesWorkspace({ catalog, actions, mutate, mutationPending, mutationError }: CatalogWorkspaceProps) {
  const { t } = useTranslation("catalog")
  const [tab, setTab] = useState<"cases" | "suites">("cases")
  const [selectedID, setSelectedID] = useState("")
  const [selectedSuiteID, setSelectedSuiteID] = useState("")
  const selected = catalog.test_cases.find((item) => item.id === selectedID) ?? catalog.test_cases[0]
  const selectedSuite = catalog.suites.find((item) => item.id === selectedSuiteID) ?? catalog.suites[0]
  const selectedEntity = tab === "cases" ? selected : selectedSuite
  const kind = tab === "cases" ? "case" : "suite"
  return (
    <PageFrame
      title={t("cases.title")}
      description={t("cases.description")}
      count={t("cases.count", { cases: catalog.test_cases.length, suites: catalog.suites.length })}
      inspector={tab === "cases" ? (selected ? <CaseInspector testCase={selected} catalog={catalog} /> : <EmptyInspector label={t("cases.noneSelectedCase")} />) : (selectedSuite ? <SuiteInspector suite={selectedSuite} catalog={catalog} /> : <EmptyInspector label={t("cases.noneSelectedSuite")} />)}
      inspectorLabel={t(tab === "cases" ? "cases.caseDetails" : "cases.suiteDetails")}
      actions={<><CatalogEditor kind={kind} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} />{selectedEntity ? <CatalogEditor key={`${kind}-${selectedEntity.id}`} kind={kind} item={selectedEntity} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} /> : null}<DeleteCatalogButton kind={kind} item={selectedEntity} action={tab === "cases" ? actions.deleteTestCase : actions.deleteSuite} mutate={mutate} pending={mutationPending} /></>}
    >
      {mutationError ? <div role="alert" className="border-t px-4 py-2 text-xs text-destructive">{mutationError}</div> : null}
      <Tabs value={tab} onValueChange={(value) => setTab(value as "cases" | "suites")} className="min-h-0 flex-1 gap-0">
        <TabsList variant="line" className="mx-4 h-8"><TabsTrigger value="cases" className="text-xs">{t("cases.tabs.cases", { count: catalog.test_cases.length })}</TabsTrigger><TabsTrigger value="suites" className="text-xs">{t("cases.tabs.suites", { count: catalog.suites.length })}</TabsTrigger></TabsList>
        <Separator />
      {tab === "cases" && catalog.test_cases.length === 0 ? (
        <CatalogEmpty title={t("cases.empty")} description={t("cases.emptyDescription")} />
      ) : tab === "cases" ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label={t("cases.aria")} className="min-w-[680px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-[11px]">{t("common.case")}</TableHead>
                <TableHead className="h-8 text-[11px]">{t("cases.request")}</TableHead>
                <TableHead className="h-8 text-[11px]">{t("cases.policy")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {catalog.test_cases.map((testCase) => (
                <TableRow
                  key={testCase.id}
                  data-state={testCase.id === selected?.id ? "selected" : undefined}
                  aria-selected={testCase.id === selected?.id}
                  onClick={() => setSelectedID(testCase.id)}
                  className="dense-table-row h-11"
                >
                  <TableCell className="py-1 pl-4">
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs no-underline hover:no-underline" aria-label={t("cases.view", { name: testCase.name })}>
                      {testCase.name}
                    </Button>
                    <div className="mt-0.5 text-[10px] text-muted-foreground">{PROTOCOL_LABELS[testCase.protocol]}</div>
                  </TableCell>
                  <TableCell className="py-1 font-mono text-[11px]">{testCase.method} {testCase.path}</TableCell>
                  <TableCell className="py-1">
                    <CasePolicyBadge testCase={testCase} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ScrollArea>
      ) : <SuiteTable suites={catalog.suites} selectedID={selectedSuite?.id ?? ""} onSelect={setSelectedSuiteID} />}
      </Tabs>
    </PageFrame>
  )
}

function SuiteTable({ suites, selectedID, onSelect }: { suites: CatalogSuite[]; selectedID: string; onSelect: (id:string) => void }) {
  const { t } = useTranslation("catalog")
  if (!suites.length) return <CatalogEmpty title={t("cases.suiteEmpty")} description={t("cases.suiteEmptyDescription")} />
  return <ScrollArea className="min-h-0 flex-1"><Table aria-label={t("cases.suiteAria")}><TableHeader><TableRow><TableHead className="pl-4">{t("common.suite")}</TableHead><TableHead>{t("cases.caseCount")}</TableHead><TableHead>{t("common.version")}</TableHead></TableRow></TableHeader><TableBody>{suites.map(suite => <TableRow key={suite.id} data-state={suite.id === selectedID ? "selected" : undefined} onClick={() => onSelect(suite.id)}><TableCell className="pl-4 text-xs">{suite.name}</TableCell><TableCell className="text-xs">{suite.case_count}</TableCell><TableCell className="text-xs">r{suite.revision}</TableCell></TableRow>)}</TableBody></Table></ScrollArea>
}

function SuiteInspector({ suite, catalog }: { suite: CatalogSuite; catalog: CatalogSnapshot }) {
  const { t } = useTranslation("catalog")
  const names = new Map(catalog.test_cases.map(testCase => [testCase.id, testCase.name]))
  return <div className="flex h-full min-h-0 flex-col">
    <InspectorHeader title={suite.name} subtitle={suite.id} />
    <Separator />
    <ScrollArea className="min-h-0 flex-1">
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label={t("common.version")} value={`r${suite.revision}`} />
        <div data-slot="inspector-definition-row" className="py-2">
          <dt className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
            <span>{t("cases.pinnedCases")}</span>
            <span className="tabular-nums">{t("common.countItems", { count: suite.cases.length })}</span>
          </dt>
          <dd className="mt-1 min-w-0">
            {suite.cases.length ? (
              <ul aria-label={t("cases.pinnedCasesAria")}>
                {suite.cases.map(ref => (
                  <li key={ref.case_id} className="flex min-w-0 items-start gap-2 py-1.5">
                    <span aria-hidden="true" className="mt-1.5 size-1 shrink-0 rounded-full bg-muted-foreground" />
                    <span className="min-w-0 break-words text-xs font-medium leading-4">
                      {names.get(ref.case_id) ?? t("common.unknownCase")}
                    </span>
                  </li>
                ))}
              </ul>
            ) : <span className="text-xs text-muted-foreground">{t("cases.noPinnedCases")}</span>}
          </dd>
        </div>
      </dl>
    </ScrollArea>
  </div>
}

function CaseInspector({ testCase, catalog }: { testCase: CatalogTestCase; catalog: CatalogSnapshot }) {
  const { t } = useTranslation("catalog")
  return (
    <>
      <InspectorHeader title={testCase.name} subtitle={testCase.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label={t("cases.sourceKey")} value={testCase.key} />
        <InspectorRow label={t("common.protocol")} value={PROTOCOL_LABELS[testCase.protocol]} />
        <InspectorRow label={t("cases.dimension")} value={testCase.dimension} />
        <InspectorRow label={t("cases.executionPolicy")} value={t(casePolicyKey(testCase))} />
        <InspectorRow label={t("cases.severity")} value={t(testCase.severity === "critical" ? "cases.critical" : "cases.normal")} />
        <InspectorRow label={t("cases.request")} value={`${testCase.method} ${testCase.path}`} />
        <InspectorRow label={t("cases.assertions")} value={testCase.assertion_kinds.join(" · ")} />
        <InspectorRow label={t("cases.suiteCatalog")} value={t("cases.reusableSuites", { count: catalog.suites.length })} />
      </dl>
    </>
  )
}

function CasePolicyBadge({ testCase }: { testCase: CatalogTestCase }) {
  const { t } = useTranslation("catalog")
  const label = t(casePolicyKey(testCase))
  return (
    <Badge
      variant="outline"
      className={
        !testCase.enabled
          ? "border-border bg-muted text-muted-foreground"
          : testCase.execution_mode === "manual"
            ? "border-warning/25 bg-warning-soft text-warning-strong"
            : "border-success/25 bg-success-soft text-success-strong"
      }
    >
      {label}
    </Badge>
  )
}

function casePolicyKey(testCase: CatalogTestCase): string {
  if (!testCase.enabled) return "cases.policyDisabled"
  if (testCase.execution_mode === "manual") return "cases.policyManual"
  if (testCase.default) return "cases.policyDefault"
  return "cases.policyAutomatic"
}

export function PlansWorkspace({
  catalog,
  actions,
  mutate,
  mutationPending,
  mutationError,
  commandPending,
  onStartPlan,
}: {
  catalog: CatalogSnapshot
  actions: CatalogActions
  mutate: CatalogMutation
  mutationPending: boolean
  mutationError: string
  commandPending: boolean
  onStartPlan: (planID: string) => Promise<void>
}) {
  const { t } = useTranslation("catalog")
  const [selectedID, setSelectedID] = useState("")
  const selected = catalog.plans.find((item) => item.id === selectedID) ?? catalog.plans[0]
  return (
    <PageFrame
      title={t("plans.title")}
      description={t("plans.description")}
      count={t("plans.count", { count: catalog.plans.length })}
      inspector={
        selected ? (
          <PlanInspector plan={selected} commandPending={commandPending} onStartPlan={onStartPlan} />
        ) : (
          <EmptyInspector label={t("plans.noneSelected")} />
        )
      }
      inspectorLabel={t("plans.details")}
      actions={<><CatalogEditor kind="plan" catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} />{selected ? <CatalogEditor key={`plan-${selected.id}`} kind="plan" item={selected} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} /> : null}<DeleteCatalogButton kind="plan" item={selected} action={actions.deletePlan} mutate={mutate} pending={mutationPending} /></>}
    >
      {mutationError ? <div role="alert" className="border-t px-4 py-2 text-xs text-destructive">{mutationError}</div> : null}
      {catalog.plans.length === 0 ? (
        <CatalogEmpty title={t("plans.empty")} description={t("plans.emptyDescription")} />
      ) : (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label={t("plans.aria")} className="min-w-[720px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-[11px]">{t("common.plan")}</TableHead>
                <TableHead className="h-8 text-[11px]">{t("plans.objects")}</TableHead>
                <TableHead className="h-8 text-[11px]">{t("common.load")}</TableHead>
                <TableHead className="h-8 text-[11px]">{t("common.target")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {catalog.plans.map((plan) => (
                <TableRow
                  key={plan.id}
                  data-state={plan.id === selected?.id ? "selected" : undefined}
                  aria-selected={plan.id === selected?.id}
                  onClick={() => setSelectedID(plan.id)}
                  className="dense-table-row h-11"
                >
                  <TableCell className="py-1 pl-4">
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs no-underline hover:no-underline" aria-label={t("plans.view", { name: plan.name })}>
                      {plan.name}
                    </Button>
                    <div className="mt-0.5 text-[10px] text-muted-foreground">r{plan.revision}</div>
                  </TableCell>
                  <TableCell className="py-1 text-[11px] text-muted-foreground">
                    {t("plans.objectCount", { models: plan.model_count, channels: plan.channel_count, cases: plan.case_count })}
                  </TableCell>
                  <TableCell className="py-1 text-xs">{t(`plans.load${plan.load_mode === "single" ? "Single" : plan.load_mode === "fixed_concurrency" ? "Fixed" : "Open"}`)}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{t(plan.request_count > 0 ? "common.requests" : "common.seconds", { count: plan.request_count > 0 ? plan.request_count : Math.round(plan.duration_ms / 1000) })}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ScrollArea>
      )}
    </PageFrame>
  )
}

function PlanInspector({
  plan,
  commandPending,
  onStartPlan,
}: {
  plan: CatalogPlan
  commandPending: boolean
  onStartPlan: (planID: string) => Promise<void>
}) {
  const { t } = useTranslation("catalog")
  const loadLabel = t(`plans.load${plan.load_mode === "single" ? "Single" : plan.load_mode === "fixed_concurrency" ? "Fixed" : "Open"}`)
  return (
    <>
      <InspectorHeader title={plan.name} subtitle={plan.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label={t("plans.fixedVersion")} value={`r${plan.revision}`} />
        <InspectorRow label={t("plans.objects")} value={t("plans.objectCount", { models: plan.model_count, channels: plan.channel_count, cases: plan.case_count })} />
        <InspectorRow label={t("plans.loadMode")} value={`${loadLabel} · ${t("plans.concurrency", { count: plan.concurrency })}`} />
        <InspectorRow label={t("plans.sendTarget")} value={t(plan.request_count > 0 ? "common.requestCount" : "common.seconds", { count: plan.request_count > 0 ? plan.request_count : Math.round(plan.duration_ms / 1000) })} />
        <InspectorRow label={t("plans.requestTimeout")} value={t("common.seconds", { count: Math.round(plan.request_timeout_ms / 1000) })} />
      </dl>
      <div className="border-t px-4 py-3">
        <Button size="sm" disabled={commandPending} onClick={() => void onStartPlan(plan.id)}>
          <PlayIcon data-icon="inline-start" /> {t(commandPending ? "plans.creating" : "plans.run")}
        </Button>
      </div>
    </>
  )
}

function StateBadge({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation("catalog")
  return (
    <Badge
      variant="outline"
      className={enabled ? "border-success/25 bg-success-soft text-success-strong" : "border-border bg-muted text-muted-foreground"}
    >
      {t(enabled ? "common.enabled" : "common.disabled")}
    </Badge>
  )
}

function CatalogEmpty({ title, description }: { title: string; description: string }) {
  return (
    <ScrollArea className="min-h-0 flex-1 border-t">
      <Empty>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </Empty>
    </ScrollArea>
  )
}
