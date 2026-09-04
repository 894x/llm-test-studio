import { useMemo, useState } from "react"
import CircleStopIcon from "lucide-react/dist/esm/icons/circle-stop.mjs"
import CircleXIcon from "lucide-react/dist/esm/icons/circle-x.mjs"
import PanelLeftIcon from "lucide-react/dist/esm/icons/panel-left.mjs"
import PanelRightIcon from "lucide-react/dist/esm/icons/panel-right.mjs"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"

import {
  publicDesktopOperationErrorMessage,
} from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@/components/ui/field"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"
import { ComparisonPanel } from "@/features/comparisons/comparison-workspace"
import type { ComparisonSnapshot } from "@/features/comparisons/data"
import type { CatalogSnapshot } from "@/features/catalog/data"

import {
  STATUS_LABELS,
  formatTargetDuration,
  presentWorkspace,
  type RunRecord,
  type RunStatus,
  type StartRunTargetCommand,
  type TestPlan,
  type WorkspaceSnapshot,
} from "./data"
import { eligibleRuntimeChannels, eligibleRuntimeModels } from "./run-targets"

type ActiveTaskState = "queued" | "starting" | "running" | "draining"

const STATUS_CLASS: Record<RunStatus, string> = {
  running: "border-info/25 bg-info-soft text-info-strong",
  draining: "border-warning/25 bg-warning-soft text-warning-strong",
  passed: "border-success/25 bg-success-soft text-success-strong",
  completed: "border-border bg-muted text-foreground",
  failed: "border-destructive/25 bg-destructive-soft text-destructive",
  queued: "border-warning/25 bg-warning-soft text-warning-strong",
  cancelled: "border-border bg-muted text-muted-foreground",
}

const TASK_LABEL: Record<ActiveTaskState, string> = {
  queued: "排队中",
  starting: "启动中",
  running: "发送中",
  draining: "排空中",
}

function PlanNavigation({
  activePlanId,
  plans,
  totalRuns,
  onSelect,
}: {
  activePlanId: string
  plans: TestPlan[]
  totalRuns: number
  onSelect: (planId: string) => void
}) {
  return (
    <nav aria-label="测试计划" className="flex min-h-0 flex-1 flex-col">
      <div className="px-3 pb-2 pt-4">
        <div className="text-xs font-semibold">测试计划</div>
        <div className="mt-1 text-[11px] text-muted-foreground">
          按已固定的计划版本查看运行
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-2 pb-3">
        <button
          type="button"
          data-active={activePlanId === "all"}
          onClick={() => onSelect("all")}
          className="plan-nav-item"
        >
          <span>
            <span className="block text-xs font-medium">全部运行</span>
            <span className="mt-0.5 block text-[11px] text-muted-foreground">
              跨计划追踪与对比
            </span>
          </span>
          <span className="text-[11px] tabular-nums text-muted-foreground">
            {totalRuns}
          </span>
        </button>
        {plans.map((plan) => (
          <button
            key={plan.id}
            type="button"
            data-active={activePlanId === plan.id}
            onClick={() => onSelect(plan.id)}
            className="plan-nav-item"
          >
            <span className="min-w-0">
              <span className="block truncate text-xs font-medium">
                {plan.name}
              </span>
              <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">
                {plan.description}
              </span>
            </span>
            <span className="text-[11px] tabular-nums text-muted-foreground">
              {plan.runCount}
            </span>
          </button>
        ))}
      </div>
    </nav>
  )
}

function StatusBadge({ status }: { status: RunStatus }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        "h-5 rounded-md px-1.5 text-[11px] font-medium",
        STATUS_CLASS[status],
      )}
    >
      <span className="status-dot" aria-hidden="true" />
      {STATUS_LABELS[status]}
    </Badge>
  )
}

function RunTable({
  runs,
  selectedId,
  onSelect,
  filtered,
}: {
  runs: RunRecord[]
  selectedId: string
  onSelect: (run: RunRecord) => void
  filtered: boolean
}) {
  if (runs.length === 0) {
    return (
      <ScrollArea className="min-h-0 flex-1 border-t">
        <Empty>
          <EmptyTitle>
            {filtered ? "这个计划还没有运行记录" : "还没有运行记录"}
          </EmptyTitle>
          <EmptyDescription>
            {filtered
              ? "选择其他计划，或从当前计划新建一次运行。"
              : "从右上角新建运行，结果会出现在这里。"}
          </EmptyDescription>
        </Empty>
      </ScrollArea>
    )
  }

  return (
    <ScrollArea className="min-h-0 flex-1 border-t">
      <Table aria-label="运行记录" className="min-w-[780px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 w-[96px] pl-4 text-[11px] text-muted-foreground">
              状态
            </TableHead>
            <TableHead className="h-8 min-w-[190px] text-[11px] text-muted-foreground">
              运行 / 计划
            </TableHead>
            <TableHead className="h-8 min-w-[180px] text-[11px] text-muted-foreground">
              目标
            </TableHead>
            <TableHead className="h-8 min-w-[150px] text-[11px] text-muted-foreground">
              进度
            </TableHead>
            <TableHead className="h-8 text-[11px] text-muted-foreground">
              P95
            </TableHead>
            <TableHead className="h-8 text-[11px] text-muted-foreground">
              开始时间
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((run) => {
            const selected = run.id === selectedId
            const percent =
              run.total === 0
                ? 0
                : Math.round((run.completed / run.total) * 100)
            return (
              <TableRow
                key={run.id}
                data-state={selected ? "selected" : undefined}
                aria-selected={selected}
                onClick={() => onSelect(run)}
                className="dense-table-row h-11"
              >
                <TableCell className="py-1 pl-4">
                  <StatusBadge status={run.status} />
                </TableCell>
                <TableCell className="py-1">
                  <Button
                    variant="link"
                    size="sm"
                    aria-label={`查看 ${run.title}`}
                    onClick={() => onSelect(run)}
                    className="h-auto max-w-[220px] justify-start p-0 text-xs font-medium no-underline hover:no-underline"
                  >
                    <span className="truncate">{run.title}</span>
                  </Button>
                  <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                    {run.id}
                  </div>
                </TableCell>
                <TableCell className="py-1">
                  <div className="truncate text-xs">{run.model}</div>
                  <div className="mt-0.5 truncate text-[10px] text-muted-foreground">
                    {run.channel}
                  </div>
                </TableCell>
                <TableCell className="py-1">
                  {run.total > 0 ? (
                    <>
                      <div className="flex items-center gap-2">
                        <Progress
                          value={percent}
                          className="w-20"
                          aria-label={`${run.title} 进度 ${percent}%`}
                        />
                        <span className="text-[11px] tabular-nums text-muted-foreground">
                          {run.completed}/{run.total}
                        </span>
                      </div>
                      <div className="mt-1 text-[10px] text-muted-foreground">
                        通过 {run.passed}
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                        {isActiveTaskState(run.coreStatus) ? <Spinner /> : null}
                        <span className="tabular-nums">{run.completed} 个已完成</span>
                      </div>
                      <div className="mt-1 text-[10px] text-muted-foreground">
                        目标 {formatTargetDuration(run.targetDurationMS)}
                      </div>
                    </>
                  )}
                </TableCell>
                <TableCell className="py-1 text-xs tabular-nums">
                  {run.p95}
                </TableCell>
                <TableCell className="py-1">
                  <div className="text-xs">{run.started}</div>
                  <div className="mt-0.5 text-[10px] tabular-nums text-muted-foreground">
                    {run.duration}
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </ScrollArea>
  )
}

function DefinitionRow({
  label,
  value,
}: {
  label: string
  value: string
}) {
  return (
    <div data-slot="inspector-definition-row" className="py-2">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 truncate text-xs font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

function RunInspectorContent({ run }: { run: RunRecord }) {
  const durationOnly = run.total === 0 && run.targetDurationMS > 0
  const targetDetail = durationOnly
    ? `目标时长 ${formatTargetDuration(run.targetDurationMS)}`
    : `${run.total} 个请求已固定`
  const progressDetail = durationOnly
    ? `${run.completed} 个请求已完成`
    : `${run.completed}/${run.total} 已完成`

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="px-4 pb-3 pt-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">{run.title}</div>
            <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
              {run.id}
            </div>
          </div>
          <StatusBadge status={run.status} />
        </div>
      </div>
      <Tabs defaultValue="summary" className="min-h-0 flex-1 gap-0">
        <TabsList variant="line" className="mx-4 h-8">
          <TabsTrigger value="summary" className="text-xs">
            摘要
          </TabsTrigger>
          <TabsTrigger value="failures" className="text-xs">
            失败
          </TabsTrigger>
          <TabsTrigger value="artifacts" className="text-xs">
            产物
          </TabsTrigger>
        </TabsList>
        <Separator />
        <TabsContent
          value="summary"
          className="min-h-0 overflow-y-auto px-4 py-2"
        >
          <dl data-slot="inspector-description-list" className="space-y-1">
            <DefinitionRow
              label="计划版本"
              value={`${run.planRevision} · ${targetDetail}`}
            />
            <DefinitionRow
              label="模型"
              value={`${run.model} · ${run.modelRevision}`}
            />
            <DefinitionRow
              label="渠道"
              value={`${run.channel} · ${run.channelRevision}`}
            />
            <DefinitionRow
              label="负载"
              value={`${run.loadProfile} · ${progressDetail}`}
            />
            <DefinitionRow
              label="耗时"
              value={`${run.duration} · P95 ${run.p95}`}
            />
            <DefinitionRow
              label="产物"
              value={`${run.artifactCount} 个已保存 · 报告、证据与原始事件`}
            />
          </dl>
        </TabsContent>
        <TabsContent
          value="failures"
          className="min-h-0 overflow-y-auto px-4 py-3 text-xs"
        >
          {run.failureSummary ?? "当前运行没有已记录的失败。"}
        </TabsContent>
        <TabsContent
          value="artifacts"
          className="min-h-0 overflow-y-auto px-4 py-3 text-xs"
        >
          {run.artifactCount > 0
            ? `${run.artifactCount} 个产物等待 Go Core 提供本地路径。`
            : "尚未生成产物。"}
        </TabsContent>
      </Tabs>
    </div>
  )
}

export function NewRunSheet({
  plans,
	catalog,
  commandPending,
  onStartRun,
}: {
  plans: TestPlan[]
	catalog: CatalogSnapshot
  commandPending: boolean
  onStartRun: (command: StartRunTargetCommand) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [selectedPlan, setSelectedPlan] = useState(plans[0]?.id ?? "")
	const [selectedModel, setSelectedModel] = useState("")
	const [selectedChannel, setSelectedChannel] = useState("")
  const [paidVideoConfirmed, setPaidVideoConfirmed] = useState(false)
  const [startError, setStartError] = useState("")
  const effectiveSelectedPlan = plans.some((plan) => plan.id === selectedPlan)
    ? selectedPlan
    : (plans[0]?.id ?? "")
	const models = useMemo(() => eligibleRuntimeModels(catalog, effectiveSelectedPlan), [catalog, effectiveSelectedPlan])
	const effectiveSelectedModel = models.some((model) => model.id === selectedModel) ? selectedModel : (models[0]?.id ?? "")
	const channels = useMemo(
		() => eligibleRuntimeChannels(catalog, effectiveSelectedPlan, effectiveSelectedModel),
		[catalog, effectiveSelectedPlan, effectiveSelectedModel],
	)
	const effectiveSelectedChannel = channels.some((channel) => channel.id === selectedChannel) ? selectedChannel : (channels[0]?.id ?? "")
  const selectedModelDefinition = models.find((model) => model.id === effectiveSelectedModel)
  const requiresPaidVideoConfirmation = selectedModelDefinition?.protocol === "wan-video" || selectedModelDefinition?.protocol === "minimax-video"
  const paidVideoProvider = selectedModelDefinition?.protocol === "minimax-video" ? "MiniMax" : "Wan"

  const start = async () => {
    if (!effectiveSelectedPlan || !effectiveSelectedModel || !effectiveSelectedChannel) return
    setStartError("")
    try {
      await onStartRun({
        plan_id: effectiveSelectedPlan,
        model_id: effectiveSelectedModel,
        channel_id: effectiveSelectedChannel,
        confirm_paid_video: requiresPaidVideoConfirmation && paidVideoConfirmed,
      })
      setOpen(false)
    } catch (error) {
      setStartError(
        publicDesktopOperationErrorMessage(
          error,
          `创建运行（计划：${plans.find((plan) => plan.id === effectiveSelectedPlan)?.name ?? effectiveSelectedPlan}）`,
          "无法创建运行，请检查本地日志",
        ),
      )
    }
  }

  return (
    <Sheet open={open} onOpenChange={(nextOpen) => { setOpen(nextOpen); if (!nextOpen) setPaidVideoConfirmed(false) }}>
      <SheetTrigger asChild>
        <Button size="sm" className="ml-1">
          <PlusIcon data-icon="inline-start" />
          新建运行
        </Button>
      </SheetTrigger>
      <SheetContent className="sm:max-w-[420px]">
        <SheetHeader>
          <SheetTitle>新建运行</SheetTitle>
          <SheetDescription>
            从固定版本的测试计划创建一次可追溯运行。
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4">
          <div className="text-xs font-semibold">选择测试计划</div>
          <RadioGroup
            aria-label="测试计划"
            value={effectiveSelectedPlan}
            onValueChange={(value) => { setSelectedPlan(value); setSelectedModel(""); setSelectedChannel(""); setPaidVideoConfirmed(false) }}
          >
            {plans.map((plan) => {
              const selected = effectiveSelectedPlan === plan.id
              return (
                <Field
                  key={plan.id}
                  className={cn(
                    "run-plan-option",
                    selected && "border-primary bg-info-soft",
                  )}
                >
                  <RadioGroupItem id={`run-plan-${plan.id}`} value={plan.id} />
                  <FieldContent>
                    <FieldLabel htmlFor={`run-plan-${plan.id}`}>
                      {plan.name}
                    </FieldLabel>
                    <FieldDescription>
                      {plan.description}
                    </FieldDescription>
                  </FieldContent>
                  <span className="text-[11px] tabular-nums text-muted-foreground">
                    {plan.caseCount} 用例
                  </span>
                </Field>
              )
            })}
          </RadioGroup>
					<div className="grid gap-3 border-t pt-3">
						<RuntimeTargetSelect
							label="逻辑模型"
							value={effectiveSelectedModel}
							options={models.map((model) => [model.id, model.name])}
							onChange={(value) => { setSelectedModel(value); setSelectedChannel(""); setPaidVideoConfirmed(false) }}
						/>
						<RuntimeTargetSelect
							label="执行渠道"
							value={effectiveSelectedChannel}
							options={channels.map((channel) => [channel.id, channel.name])}
							onChange={setSelectedChannel}
						/>
						{models.length === 0 ? <p className="text-xs text-destructive">当前计划没有协议兼容且已映射的可用模型。</p> : null}
						{models.length > 0 && channels.length === 0 ? <p className="text-xs text-destructive">当前模型没有已启用、已配置密钥且已映射的可用渠道。</p> : null}
					</div>
          {requiresPaidVideoConfirmation ? (
            <div role="alert" className="rounded-md border border-warning/30 bg-warning-soft p-3 text-xs text-warning-strong">
              <p className="font-medium">{paidVideoProvider} 视频生成会产生费用。</p>
              <p className="mt-1 leading-5">运行将向上游提交真实视频任务，费用受模型、分辨率和时长影响。</p>
              <Field className="mt-3">
                <Checkbox
                  id="confirm-paid-video"
                  checked={paidVideoConfirmed}
                  onCheckedChange={(value) => setPaidVideoConfirmed(value === true)}
                />
                <FieldContent>
                  <FieldLabel htmlFor="confirm-paid-video">我确认本次 {paidVideoProvider} 视频运行会调用计费接口</FieldLabel>
                </FieldContent>
              </Field>
            </div>
          ) : null}
          <div className="border-t pt-3 text-[11px] leading-5 text-muted-foreground">
            凭据将由 Go Core 从系统密钥环按需租用，不会进入前端状态或本地存储。
          </div>
          {startError ? (
            <p role="alert" className="text-xs text-destructive">
              {startError}
            </p>
          ) : null}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <SheetClose asChild>
            <Button variant="outline">取消</Button>
          </SheetClose>
          <Button
            disabled={!effectiveSelectedPlan || !effectiveSelectedModel || !effectiveSelectedChannel || commandPending || (requiresPaidVideoConfirmation && !paidVideoConfirmed)}
            onClick={() => void start()}
          >
            {commandPending ? "正在创建…" : requiresPaidVideoConfirmation ? "开始付费运行" : "开始运行"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function RuntimeTargetSelect({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
	return (
		<Field className="block">
			<FieldLabel>{label}</FieldLabel>
			<Select value={value} onValueChange={onChange} disabled={options.length === 0}>
				<SelectTrigger aria-label={label} className="w-full"><SelectValue placeholder={`无可用${label}`} /></SelectTrigger>
				<SelectContent><SelectGroup>{options.map(([id, name]) => <SelectItem key={id} value={id}>{name}</SelectItem>)}</SelectGroup></SelectContent>
			</Select>
		</Field>
	)
}

function MobilePlanSheet({
  activePlanId,
  plans,
  totalRuns,
  onSelect,
}: {
  activePlanId: string
  plans: TestPlan[]
  totalRuns: number
  onSelect: (planId: string) => void
}) {
  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="min-[1180px]:hidden"
        >
          <PanelLeftIcon data-icon="inline-start" /> 计划
        </Button>
      </SheetTrigger>
      <SheetContent side="left" className="w-[300px] p-0">
        <SheetHeader className="sr-only">
          <SheetTitle>测试计划</SheetTitle>
          <SheetDescription>筛选运行记录</SheetDescription>
        </SheetHeader>
        <PlanNavigation
          activePlanId={activePlanId}
          plans={plans}
          totalRuns={totalRuns}
          onSelect={onSelect}
        />
      </SheetContent>
    </Sheet>
  )
}

function MobileInspectorSheet({ run }: { run: RunRecord }) {
  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="min-[1180px]:hidden"
        >
          <PanelRightIcon data-icon="inline-start" /> 详情
        </Button>
      </SheetTrigger>
      <SheetContent className="w-[340px] p-0">
        <SheetHeader className="sr-only">
          <SheetTitle>运行详情</SheetTitle>
          <SheetDescription>
            {run.title} 的版本、负载与产物信息
          </SheetDescription>
        </SheetHeader>
        <RunInspectorContent run={run} />
      </SheetContent>
    </Sheet>
  )
}

function ActiveTaskBar({
  run,
  state,
  commandPending,
  onStop,
  onCancel,
}: {
  run: RunRecord
  state: ActiveTaskState
  commandPending: boolean
  onStop: () => void
  onCancel: () => void
}) {
  const progress =
    run.total === 0 ? 0 : Math.round((run.completed / run.total) * 100)

  return (
    <div className="flex min-h-14 shrink-0 items-center gap-3 border-t bg-background px-4 py-2">
      <div
        className={cn(
          "size-2 shrink-0 rounded-full",
          state === "running" && "animate-pulse bg-info",
          state === "draining" && "bg-warning",
          (state === "queued" || state === "starting") && "bg-muted-foreground",
        )}
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-xs">
          <span data-task-state className="font-semibold">
            {TASK_LABEL[state]}
          </span>
          <span className="truncate text-muted-foreground">{run.title}</span>
          <span className="hidden tabular-nums text-muted-foreground sm:inline">
            {run.total > 0
              ? `${run.completed}/${run.total}`
              : `${run.completed} 已完成 · 目标 ${formatTargetDuration(run.targetDurationMS)}`}
          </span>
        </div>
        {run.total > 0 ? (
          <Progress
            value={progress}
            className="mt-2 max-w-sm"
            aria-label={`活动运行进度 ${progress}%`}
          />
        ) : (
          <div className="mt-2 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <Spinner /> 定时运行，进度取决于经过时间
          </div>
        )}
      </div>
      <Button
        variant="outline"
        size="sm"
        disabled={state !== "running" || commandPending}
        onClick={onStop}
      >
        <CircleStopIcon data-icon="inline-start" /> 停止发送
      </Button>
      <Button
        variant="destructive"
        size="sm"
        disabled={commandPending}
        onClick={onCancel}
      >
        <CircleXIcon data-icon="inline-start" /> 取消运行
      </Button>
    </div>
  )
}

function IdleTaskBar() {
  return (
    <div className="flex min-h-14 shrink-0 items-center border-t bg-background px-4 py-2">
      <span className="size-2 rounded-full bg-muted-foreground" aria-hidden="true" />
      <span className="ml-3 text-xs font-semibold">无活动运行</span>
      <span className="ml-2 text-[11px] text-muted-foreground">
        新建运行后，任务生命周期会固定显示在这里
      </span>
    </div>
  )
}

export function RunWorkspace({
  snapshot,
  comparisons,
  commandPending,
  commandError,
  onStopSending,
  onCancelRun,
}: {
  snapshot: WorkspaceSnapshot
  comparisons: ComparisonSnapshot
  commandPending: boolean
  commandError: string
  onStopSending: (runId: string) => Promise<void>
  onCancelRun: (runId: string) => Promise<void>
}) {
  const [activePlanId, setActivePlanId] = useState("all")
  const [selectedRunId, setSelectedRunId] = useState("")

  const presentation = useMemo(() => presentWorkspace(snapshot), [snapshot])
  const { plans, runs } = presentation
  const visibleRuns = useMemo(
    () =>
      activePlanId === "all"
        ? runs
        : runs.filter((run) => run.planId === activePlanId),
    [activePlanId, runs],
  )
  const selectedRun =
    visibleRuns.find((run) => run.id === selectedRunId) ?? visibleRuns[0]
  const activeRun = snapshot.active_run_id
    ? runs.find((run) => run.id === snapshot.active_run_id)
    : undefined

  const selectPlan = (planId: string) => {
    setActivePlanId(planId)
    const firstRun =
      planId === "all"
        ? runs[0]
        : runs.find((run) => run.planId === planId)
    setSelectedRunId(firstRun?.id ?? "")
  }

  return (
    <main className="flex min-h-0 flex-1">
        <aside className="hidden w-56 shrink-0 border-r bg-sidebar min-[1180px]:flex">
          <PlanNavigation
            activePlanId={activePlanId}
            plans={plans}
            totalRuns={runs.length}
            onSelect={selectPlan}
          />
        </aside>

        <section
          aria-labelledby="workspace-heading"
          className="flex min-w-0 flex-1 flex-col"
        >
          <div className="flex shrink-0 items-end justify-between gap-3 px-4 py-3">
            <div className="min-w-0">
              <h1
                id="workspace-heading"
                className="text-lg font-semibold tracking-tight"
              >
                运行工作区
              </h1>
              <p className="mt-1 truncate text-[11px] text-muted-foreground">
                比较固定版本、追踪执行状态并检查可复现证据
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <MobilePlanSheet
                activePlanId={activePlanId}
                plans={plans}
                totalRuns={runs.length}
                onSelect={selectPlan}
              />
              {selectedRun ? <MobileInspectorSheet run={selectedRun} /> : null}
              <span className="hidden text-[11px] text-muted-foreground sm:inline">
                {visibleRuns.length} 次运行
              </span>
            </div>
          </div>

          <ComparisonPanel snapshot={comparisons} />

          <RunTable
            runs={visibleRuns}
            selectedId={selectedRun?.id ?? ""}
            onSelect={(run) => setSelectedRunId(run.id)}
            filtered={activePlanId !== "all"}
          />

          {commandError ? (
            <div role="alert" className="border-t border-destructive/30 px-4 py-2 text-xs text-destructive">
              {commandError}
            </div>
          ) : null}
          {activeRun && isActiveTaskState(activeRun.coreStatus) ? (
            <ActiveTaskBar
              run={activeRun}
              state={activeRun.coreStatus}
              commandPending={commandPending}
              onStop={() => {
                void onStopSending(activeRun.id).catch(() => undefined)
              }}
              onCancel={() => {
                void onCancelRun(activeRun.id).catch(() => undefined)
              }}
            />
          ) : (
            <IdleTaskBar />
          )}
        </section>

        <aside
          aria-label="运行详情"
          className="hidden w-[320px] shrink-0 border-l bg-background min-[1180px]:flex"
        >
          {selectedRun ? (
            <RunInspectorContent run={selectedRun} />
          ) : (
            <div className="p-4 text-xs text-muted-foreground">尚未选择运行</div>
          )}
        </aside>
    </main>
  )
}

function isActiveTaskState(value: string): value is ActiveTaskState {
  return value === "queued" || value === "starting" || value === "running" || value === "draining"
}
