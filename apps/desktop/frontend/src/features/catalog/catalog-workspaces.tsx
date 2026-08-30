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
  CatalogChannel,
  CatalogModel,
  CatalogPlan,
  CatalogSnapshot,
  CatalogTestCase,
} from "./data"

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

export function ModelChannelWorkspace({ catalog }: { catalog: CatalogSnapshot }) {
  const [tab, setTab] = useState<"models" | "channels">("models")
  const [selectedModelID, setSelectedModelID] = useState("")
  const [selectedChannelID, setSelectedChannelID] = useState("")
  const selectedModel =
    catalog.models.find((model) => model.id === selectedModelID) ?? catalog.models[0]
  const selectedChannel =
    catalog.channels.find((channel) => channel.id === selectedChannelID) ?? catalog.channels[0]
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
    ) : selectedChannel ? (
      <ChannelInspector channel={selectedChannel} catalog={catalog} modelNames={modelNames} />
    ) : (
      <EmptyInspector label="尚未选择渠道" />
    )

  return (
    <PageFrame
      title="模型与渠道"
      description="管理逻辑模型、调用渠道与上游模型映射"
      count={`${catalog.models.length} 个模型 · ${catalog.channels.length} 个渠道`}
      inspector={inspector}
      inspectorLabel={tab === "models" ? "模型详情" : "渠道详情"}
    >
      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value as "models" | "channels")}
        className="min-h-0 flex-1 gap-0"
      >
        <TabsList variant="line" className="mx-4 h-8">
          <TabsTrigger value="models" className="text-xs">
            模型 {catalog.models.length}
          </TabsTrigger>
          <TabsTrigger value="channels" className="text-xs">
            渠道 {catalog.channels.length}
          </TabsTrigger>
        </TabsList>
        <Separator />
        {tab === "models" ? (
          <ModelTable
            models={catalog.models}
            selectedID={selectedModel?.id ?? ""}
            onSelect={setSelectedModelID}
          />
        ) : (
          <ChannelTable
            channels={catalog.channels}
            selectedID={selectedChannel?.id ?? ""}
            onSelect={setSelectedChannelID}
          />
        )}
      </Tabs>
    </PageFrame>
  )
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

export function CasesWorkspace({ catalog }: { catalog: CatalogSnapshot }) {
  const [selectedID, setSelectedID] = useState("")
  const selected = catalog.test_cases.find((item) => item.id === selectedID) ?? catalog.test_cases[0]
  return (
    <PageFrame
      title="测试用例"
      description="维护版本化请求、期望与断言"
      count={`${catalog.test_cases.length} 个用例 · ${catalog.suites.length} 个套件`}
      inspector={selected ? <CaseInspector testCase={selected} catalog={catalog} /> : <EmptyInspector label="尚未选择用例" />}
      inspectorLabel="用例详情"
    >
      {catalog.test_cases.length === 0 ? (
        <CatalogEmpty title="还没有测试用例" description="添加用例后可组合成可复用套件与计划。" />
      ) : (
        <ScrollArea className="min-h-0 flex-1 border-t">
          <Table aria-label="测试用例目录" className="min-w-[680px]">
            <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 pl-4 text-[11px]">用例</TableHead>
                <TableHead className="h-8 text-[11px]">请求</TableHead>
                <TableHead className="h-8 text-[11px]">策略</TableHead>
                <TableHead className="h-8 text-right text-[11px]">版本</TableHead>
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
                  <TableCell className="py-1 text-right text-xs tabular-nums">r{testCase.revision}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </ScrollArea>
      )}
    </PageFrame>
  )
}

function CaseInspector({ testCase, catalog }: { testCase: CatalogTestCase; catalog: CatalogSnapshot }) {
  return (
    <>
      <InspectorHeader title={testCase.name} subtitle={testCase.id} />
      <Separator />
      <dl className="space-y-1 px-4 py-2">
        <InspectorRow label="来源键" value={testCase.key} />
        <InspectorRow label="版本与协议" value={`r${testCase.revision} · ${PROTOCOL_LABELS[testCase.protocol]}`} />
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
  commandPending,
  onStartPlan,
}: {
  catalog: CatalogSnapshot
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
    >
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
