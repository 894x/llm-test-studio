import { useMemo, useState } from "react"
import PlayIcon from "lucide-react/dist/esm/icons/play.mjs"

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

const LOAD_LABELS = {
  single: "单次",
  fixed_concurrency: "固定并发",
  open_loop: "开放环",
} as const

interface CatalogWorkspaceProps {
  catalog: CatalogSnapshot
  actions: CatalogActions
  mutate: CatalogMutation
  mutationPending: boolean
  mutationError: string
}

export function ModelChannelWorkspace({ catalog, actions, mutate, mutationPending, mutationError }: CatalogWorkspaceProps) {
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
        <EmptyInspector label="尚未选择模型" />
      )
    ) : tab === "channels" && selectedChannel ? (
      <ChannelInspector channel={selectedChannel} catalog={catalog} modelNames={modelNames} />
    ) : tab === "mappings" && selectedMapping ? (
      <MappingInspector mapping={selectedMapping} channelNames={channelNames} modelNames={modelNames} />
    ) : (
      <EmptyInspector label="尚未选择对象" />
    )

  const selected = tab === "models" ? selectedModel : tab === "channels" ? selectedChannel : selectedMapping
  const deleteAction = tab === "models" ? actions.deleteModel : tab === "channels" ? actions.deleteChannel : actions.deleteChannelModel
  const kind = tab === "models" ? "model" : tab === "channels" ? "channel" : "mapping"

  return (
    <PageFrame
      title="模型与渠道"
      description="管理逻辑模型、调用渠道与上游模型映射"
      count={`${catalog.models.length} 个模型 · ${catalog.channels.length} 个渠道`}
      inspector={inspector}
      inspectorLabel={tab === "models" ? "模型详情" : tab === "channels" ? "渠道详情" : "映射详情"}
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
            模型 {catalog.models.length}
          </TabsTrigger>
          <TabsTrigger value="channels" className="text-xs">
            渠道 {catalog.channels.length}
          </TabsTrigger>
          <TabsTrigger value="mappings" className="text-xs">
            映射 {catalog.channel_models.length}
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
  if (!mappings.length) return <CatalogEmpty title="还没有模型映射" description="将逻辑模型绑定到一个兼容协议的渠道。" />
  return <ScrollArea className="min-h-0 flex-1"><Table aria-label="模型映射目录"><TableHeader><TableRow><TableHead className="pl-4">渠道</TableHead><TableHead>逻辑模型</TableHead><TableHead>上游名称</TableHead><TableHead>版本</TableHead></TableRow></TableHeader><TableBody>{mappings.map(mapping => <TableRow key={mapping.id} data-state={mapping.id === selectedID ? "selected" : undefined} onClick={() => onSelect(mapping.id)}><TableCell className="pl-4 text-xs">{channelNames.get(mapping.channel_id)}</TableCell><TableCell className="text-xs">{modelNames.get(mapping.model_id)}</TableCell><TableCell className="font-mono text-xs">{mapping.upstream_model_name}</TableCell><TableCell className="text-xs">r{mapping.revision}</TableCell></TableRow>)}</TableBody></Table></ScrollArea>
}

function MappingInspector({ mapping, channelNames, modelNames }: { mapping: CatalogChannelModel; channelNames: Map<string,string>; modelNames: Map<string,string> }) {
  return <><InspectorHeader title={mapping.upstream_model_name} subtitle={mapping.id} /><Separator /><dl className="space-y-1 px-4 py-2"><InspectorRow label="版本" value={`r${mapping.revision}`} /><InspectorRow label="渠道" value={channelNames.get(mapping.channel_id) ?? "未知渠道"} /><InspectorRow label="逻辑模型" value={modelNames.get(mapping.model_id) ?? "未知模型"} /></dl></>
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
  if (models.length === 0) {
    return <CatalogEmpty title="还没有模型" description="通过 GUI 或 CLI 添加第一个逻辑模型。" />
  }
  return (
    <ScrollArea className="min-h-0 flex-1">
      <Table aria-label="模型目录" className="min-w-[620px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 pl-4 text-[11px]">模型</TableHead>
            <TableHead className="h-8 text-[11px]">协议</TableHead>
            <TableHead className="h-8 text-[11px]">能力</TableHead>
            <TableHead className="h-8 text-right text-[11px]">版本</TableHead>
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
                  aria-label={`查看模型 ${model.name}`}
                >
                  {model.name}
                </Button>
                <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
                  {model.id}
                </div>
              </TableCell>
              <TableCell className="py-1 text-xs">{PROTOCOL_LABELS[model.protocol]}</TableCell>
              <TableCell className="py-1 text-[11px] text-muted-foreground">
                {model.capabilities.join(" · ") || "未标注"}
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
  if (channels.length === 0) {
    return <CatalogEmpty title="还没有渠道" description="添加渠道后再安全配置系统凭据。" />
  }
  return (
    <ScrollArea className="min-h-0 flex-1">
      <Table aria-label="渠道目录" className="min-w-[700px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 pl-4 text-[11px]">渠道</TableHead>
            <TableHead className="h-8 text-[11px]">状态</TableHead>
            <TableHead className="h-8 text-[11px]">服务地址</TableHead>
            <TableHead className="h-8 text-[11px]">映射</TableHead>
            <TableHead className="h-8 text-[11px]">凭据</TableHead>
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
                  aria-label={`查看渠道 ${channel.name}`}
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
                {channel.credential_configured ? "已配置" : "未配置"}
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
  const mappings = catalog.channel_models.filter((mapping) => mapping.model_id === model.id)
  return (
    <>
      <InspectorHeader title={model.name} subtitle={model.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="版本" value={`r${model.revision}`} />
        <InspectorRow label="协议" value={PROTOCOL_LABELS[model.protocol]} />
        <InspectorRow label="能力" value={model.capabilities.join(" · ") || "未标注"} />
        <InspectorRow label="渠道映射" value={`${mappings.length} 条`} />
        <InspectorRow
          label="上游名称"
          value={
            mappings
              .map((mapping) => `${channelNames.get(mapping.channel_id) ?? "未知渠道"} · ${mapping.upstream_model_name}`)
              .join("；") || "尚未绑定渠道"
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
  const mappings = catalog.channel_models.filter((mapping) => mapping.channel_id === channel.id)
  return (
    <>
      <InspectorHeader title={channel.name} subtitle={channel.id} trailing={<StateBadge enabled={channel.enabled} />} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="版本与协议" value={`r${channel.revision} · ${PROTOCOL_LABELS[channel.protocol]}`} />
        <InspectorRow label="Base URL" value={channel.base_url} />
        <InspectorRow label="凭据" value={channel.credential_configured ? "已在系统密钥环配置" : "尚未配置"} />
        <InspectorRow label="模型映射" value={`${mappings.length} 条`} />
        <InspectorRow
          label="逻辑模型"
          value={mappings.map((mapping) => modelNames.get(mapping.model_id) ?? "未知模型").join(" · ") || "尚未绑定模型"}
        />
      </dl>
    </>
  )
}

export function CasesWorkspace({ catalog, actions, mutate, mutationPending, mutationError }: CatalogWorkspaceProps) {
  const [tab, setTab] = useState<"cases" | "suites">("cases")
  const [selectedID, setSelectedID] = useState("")
  const [selectedSuiteID, setSelectedSuiteID] = useState("")
  const selected = catalog.test_cases.find((item) => item.id === selectedID) ?? catalog.test_cases[0]
  const selectedSuite = catalog.suites.find((item) => item.id === selectedSuiteID) ?? catalog.suites[0]
  const selectedEntity = tab === "cases" ? selected : selectedSuite
  const kind = tab === "cases" ? "case" : "suite"
  return (
    <PageFrame
      title="测试用例"
      description="维护请求、期望与断言"
      count={`${catalog.test_cases.length} 个用例 · ${catalog.suites.length} 个套件`}
      inspector={tab === "cases" ? (selected ? <CaseInspector testCase={selected} catalog={catalog} /> : <EmptyInspector label="尚未选择用例" />) : (selectedSuite ? <SuiteInspector suite={selectedSuite} catalog={catalog} /> : <EmptyInspector label="尚未选择套件" />)}
      inspectorLabel={tab === "cases" ? "用例详情" : "套件详情"}
      actions={<><CatalogEditor kind={kind} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} />{selectedEntity ? <CatalogEditor key={`${kind}-${selectedEntity.id}`} kind={kind} item={selectedEntity} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} /> : null}<DeleteCatalogButton kind={kind} item={selectedEntity} action={tab === "cases" ? actions.deleteTestCase : actions.deleteSuite} mutate={mutate} pending={mutationPending} /></>}
    >
      {mutationError ? <div role="alert" className="border-t px-4 py-2 text-xs text-destructive">{mutationError}</div> : null}
      <Tabs value={tab} onValueChange={(value) => setTab(value as "cases" | "suites")} className="min-h-0 flex-1 gap-0">
        <TabsList variant="line" className="mx-4 h-8"><TabsTrigger value="cases" className="text-xs">用例 {catalog.test_cases.length}</TabsTrigger><TabsTrigger value="suites" className="text-xs">套件 {catalog.suites.length}</TabsTrigger></TabsList>
        <Separator />
      {tab === "cases" && catalog.test_cases.length === 0 ? (
        <CatalogEmpty title="还没有测试用例" description="添加用例后可组合成可复用套件与计划。" />
      ) : tab === "cases" ? (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label="测试用例目录" className="min-w-[680px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-[11px]">用例</TableHead>
                <TableHead className="h-8 text-[11px]">请求</TableHead>
                <TableHead className="h-8 text-[11px]">策略</TableHead>
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
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs no-underline hover:no-underline" aria-label={`查看用例 ${testCase.name}`}>
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
  if (!suites.length) return <CatalogEmpty title="还没有测试套件" description="将多个固定版本用例组合成可复用套件。" />
  return <ScrollArea className="min-h-0 flex-1"><Table aria-label="测试套件目录"><TableHeader><TableRow><TableHead className="pl-4">套件</TableHead><TableHead>用例数</TableHead><TableHead>版本</TableHead></TableRow></TableHeader><TableBody>{suites.map(suite => <TableRow key={suite.id} data-state={suite.id === selectedID ? "selected" : undefined} onClick={() => onSelect(suite.id)}><TableCell className="pl-4 text-xs">{suite.name}</TableCell><TableCell className="text-xs">{suite.case_count}</TableCell><TableCell className="text-xs">r{suite.revision}</TableCell></TableRow>)}</TableBody></Table></ScrollArea>
}

function SuiteInspector({ suite, catalog }: { suite: CatalogSuite; catalog: CatalogSnapshot }) {
  const names = new Map(catalog.test_cases.map(testCase => [testCase.id, testCase.name]))
  return <><InspectorHeader title={suite.name} subtitle={suite.id} /><Separator /><dl className="space-y-1 px-4 py-2"><InspectorRow label="版本" value={`r${suite.revision}`} /><InspectorRow label="固定用例" value={suite.cases.map(ref => `${names.get(ref.case_id) ?? "未知用例"} · r${ref.revision}`).join("；") || "空套件"} /></dl></>
}

function CaseInspector({ testCase, catalog }: { testCase: CatalogTestCase; catalog: CatalogSnapshot }) {
  return (
    <>
      <InspectorHeader title={testCase.name} subtitle={testCase.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="来源键" value={testCase.key} />
        <InspectorRow label="协议" value={PROTOCOL_LABELS[testCase.protocol]} />
        <InspectorRow label="维度" value={testCase.dimension} />
        <InspectorRow label="执行策略" value={casePolicyLabel(testCase)} />
        <InspectorRow label="严重度" value={testCase.severity === "critical" ? "关键" : "普通"} />
        <InspectorRow label="请求" value={`${testCase.method} ${testCase.path}`} />
        <InspectorRow label="断言" value={testCase.assertion_kinds.join(" · ")} />
        <InspectorRow label="套件目录" value={`${catalog.suites.length} 个可复用套件`} />
      </dl>
    </>
  )
}

function CasePolicyBadge({ testCase }: { testCase: CatalogTestCase }) {
  const label = casePolicyLabel(testCase)
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

function casePolicyLabel(testCase: CatalogTestCase): string {
  if (!testCase.enabled) return "已停用"
  if (testCase.execution_mode === "manual") return "人工判定"
  if (testCase.default) return "默认启用"
  return "自动"
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
  const [selectedID, setSelectedID] = useState("")
  const selected = catalog.plans.find((item) => item.id === selectedID) ?? catalog.plans[0]
  return (
    <PageFrame
      title="测试计划"
      description="组合模型、渠道、用例与负载配置"
      count={`${catalog.plans.length} 个计划`}
      inspector={
        selected ? (
          <PlanInspector plan={selected} commandPending={commandPending} onStartPlan={onStartPlan} />
        ) : (
          <EmptyInspector label="尚未选择计划" />
        )
      }
      inspectorLabel="计划详情"
      actions={<><CatalogEditor kind="plan" catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} />{selected ? <CatalogEditor key={`plan-${selected.id}`} kind="plan" item={selected} catalog={catalog} actions={actions} mutate={mutate} pending={mutationPending} /> : null}<DeleteCatalogButton kind="plan" item={selected} action={actions.deletePlan} mutate={mutate} pending={mutationPending} /></>}
    >
      {mutationError ? <div role="alert" className="border-t px-4 py-2 text-xs text-destructive">{mutationError}</div> : null}
      {catalog.plans.length === 0 ? (
        <CatalogEmpty title="还没有测试计划" description="先准备模型、渠道和用例，再创建可复用计划。" />
      ) : (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label="测试计划目录" className="min-w-[720px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-[11px]">计划</TableHead>
                <TableHead className="h-8 text-[11px]">对象</TableHead>
                <TableHead className="h-8 text-[11px]">负载</TableHead>
                <TableHead className="h-8 text-[11px]">目标</TableHead>
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
                    <Button variant="link" size="sm" className="h-auto p-0 text-xs no-underline hover:no-underline" aria-label={`查看计划 ${plan.name}`}>
                      {plan.name}
                    </Button>
                    <div className="mt-0.5 text-[10px] text-muted-foreground">r{plan.revision}</div>
                  </TableCell>
                  <TableCell className="py-1 text-[11px] text-muted-foreground">
                    {plan.model_count} 模型 · {plan.channel_count} 渠道 · {plan.case_count} 用例
                  </TableCell>
                  <TableCell className="py-1 text-xs">{LOAD_LABELS[plan.load_mode]}</TableCell>
                  <TableCell className="py-1 text-xs tabular-nums">{plan.request_count > 0 ? `${plan.request_count} 请求` : `${Math.round(plan.duration_ms / 1000)} 秒`}</TableCell>
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
  return (
    <>
      <InspectorHeader title={plan.name} subtitle={plan.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="固定版本" value={`r${plan.revision}`} />
        <InspectorRow label="对象" value={`${plan.model_count} 模型 · ${plan.channel_count} 渠道 · ${plan.case_count} 用例`} />
        <InspectorRow label="负载模式" value={`${LOAD_LABELS[plan.load_mode]} · 并发 ${plan.concurrency}`} />
        <InspectorRow label="发送目标" value={plan.request_count > 0 ? `${plan.request_count} 个请求` : `${Math.round(plan.duration_ms / 1000)} 秒`} />
        <InspectorRow label="单请求超时" value={`${Math.round(plan.request_timeout_ms / 1000)} 秒`} />
      </dl>
      <div className="border-t px-4 py-3">
        <Button size="sm" disabled={commandPending} onClick={() => void onStartPlan(plan.id)}>
          <PlayIcon data-icon="inline-start" /> {commandPending ? "正在创建…" : "运行这个计划"}
        </Button>
      </div>
    </>
  )
}

function StateBadge({ enabled }: { enabled: boolean }) {
  return (
    <Badge
      variant="outline"
      className={enabled ? "border-success/25 bg-success-soft text-success-strong" : "border-border bg-muted text-muted-foreground"}
    >
      {enabled ? "启用" : "停用"}
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
