import { useMemo, useState } from "react"
import CircleStopIcon from "lucide-react/dist/esm/icons/circle-stop.mjs"
import CircleXIcon from "lucide-react/dist/esm/icons/circle-x.mjs"
import PanelLeftIcon from "lucide-react/dist/esm/icons/panel-left.mjs"
import PanelRightIcon from "lucide-react/dist/esm/icons/panel-right.mjs"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"
import { useTranslation } from "react-i18next"

import {
  publicDesktopOperationErrorMessage,
} from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
  const { t } = useTranslation("runs")

  return (
    <nav aria-label={t("plans.aria")} className="flex min-h-0 flex-1 flex-col">
      <div className="px-3 pb-2 pt-4">
        <div className="text-xs font-semibold">{t("plans.title")}</div>
        <div className="mt-1 text-[11px] text-muted-foreground">
          {t("plans.description")}
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
            <span className="block text-xs font-medium">{t("plans.all")}</span>
            <span className="mt-0.5 block text-[11px] text-muted-foreground">
              {t("plans.allDescription")}
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
  const { t } = useTranslation("common")

  return (
    <Badge
      variant="outline"
      className={cn(
        "h-5 rounded-md px-1.5 text-[11px] font-medium",
        STATUS_CLASS[status],
      )}
    >
      <span className="status-dot" aria-hidden="true" />
      {t(`status.${status}`)}
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
  const { t } = useTranslation("runs")

  if (runs.length === 0) {
    return (
      <ScrollArea className="min-h-0 flex-1 border-t">
        <Empty>
          <EmptyTitle>
            {t(filtered ? "table.emptyFiltered" : "table.empty")}
          </EmptyTitle>
          <EmptyDescription>
            {filtered
              ? t("table.emptyFilteredDescription")
              : t("table.emptyDescription")}
          </EmptyDescription>
        </Empty>
      </ScrollArea>
    )
  }

  return (
    <ScrollArea className="min-h-0 flex-1 border-t">
      <Table aria-label={t("table.aria")} className="min-w-[780px]">
        <TableHeader className="sticky top-0 z-10 bg-background/95 backdrop-blur-sm">
          <TableRow className="hover:bg-transparent">
            <TableHead className="h-8 w-[96px] pl-4 text-[11px] text-muted-foreground">
              {t("table.status")}
            </TableHead>
            <TableHead className="h-8 min-w-[190px] text-[11px] text-muted-foreground">
              {t("table.runPlan")}
            </TableHead>
            <TableHead className="h-8 min-w-[180px] text-[11px] text-muted-foreground">
              {t("table.target")}
            </TableHead>
            <TableHead className="h-8 min-w-[150px] text-[11px] text-muted-foreground">
              {t("table.progress")}
            </TableHead>
            <TableHead className="h-8 text-[11px] text-muted-foreground">
              P95
            </TableHead>
            <TableHead className="h-8 text-[11px] text-muted-foreground">
              {t("table.started")}
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
                    aria-label={t("table.view", { title: run.title })}
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
                          aria-label={t("table.progressAria", { title: run.title, percent })}
                        />
                        <span className="text-[11px] tabular-nums text-muted-foreground">
                          {run.completed}/{run.total}
                        </span>
                      </div>
                      <div className="mt-1 text-[10px] text-muted-foreground">
                        {t("table.passed", { count: run.passed })}
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                        {isActiveTaskState(run.coreStatus) ? <Spinner /> : null}
                        <span className="tabular-nums">{t("table.completedCount", { count: run.completed })}</span>
                      </div>
                      <div className="mt-1 text-[10px] text-muted-foreground">
                        {t("table.targetDuration", { duration: formatTargetDuration(run.targetDurationMS) })}
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
  const { t } = useTranslation("runs")
  const durationOnly = run.total === 0 && run.targetDurationMS > 0
  const targetDetail = durationOnly
    ? t("inspector.targetDuration", { duration: formatTargetDuration(run.targetDurationMS) })
    : t("inspector.fixedRequests", { count: run.total })
  const progressDetail = durationOnly
    ? t("inspector.completedRequests", { count: run.completed })
    : t("inspector.completedFraction", { completed: run.completed, total: run.total })

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
            {t("inspector.summary")}
          </TabsTrigger>
          <TabsTrigger value="failures" className="text-xs">
            {t("inspector.failures")}
          </TabsTrigger>
          <TabsTrigger value="artifacts" className="text-xs">
            {t("inspector.artifacts")}
          </TabsTrigger>
        </TabsList>
        <Separator />
        <TabsContent
          value="summary"
          className="min-h-0 overflow-y-auto px-4 py-2"
        >
          <dl data-slot="inspector-description-list" className="space-y-1">
            <DefinitionRow
              label={t("inspector.planRevision")}
              value={`${run.planRevision} · ${targetDetail}`}
            />
            <DefinitionRow
              label={t("inspector.model")}
              value={`${run.model} · ${run.modelRevision}`}
            />
            <DefinitionRow
              label={t("inspector.channel")}
              value={`${run.channel} · ${run.channelRevision}`}
            />
            <DefinitionRow
              label={t("inspector.load")}
              value={`${run.loadProfile} · ${progressDetail}`}
            />
            <DefinitionRow
              label={t("inspector.duration")}
              value={`${run.duration} · P95 ${run.p95}`}
            />
            <DefinitionRow
              label={t("inspector.artifacts")}
              value={t("inspector.savedArtifacts", { count: run.artifactCount })}
            />
          </dl>
        </TabsContent>
        <TabsContent
          value="failures"
          className="min-h-0 overflow-y-auto px-4 py-3 text-xs"
        >
          {run.failureSummary ?? t("inspector.noFailures")}
        </TabsContent>
        <TabsContent
          value="artifacts"
          className="min-h-0 overflow-y-auto px-4 py-3 text-xs"
        >
          {run.artifactCount > 0
            ? t("inspector.artifactsPending", { count: run.artifactCount })
            : t("inspector.noArtifacts")}
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
  const { t } = useTranslation("runs")
  const [open, setOpen] = useState(false)
  const [selectedPlan, setSelectedPlan] = useState(plans[0]?.id ?? "")
	const [selectedModel, setSelectedModel] = useState("")
	const [selectedChannel, setSelectedChannel] = useState("")
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

  const start = async () => {
    if (!effectiveSelectedPlan || !effectiveSelectedModel || !effectiveSelectedChannel) return
    setStartError("")
    try {
      await onStartRun({ plan_id: effectiveSelectedPlan, model_id: effectiveSelectedModel, channel_id: effectiveSelectedChannel })
      setOpen(false)
    } catch (error) {
      setStartError(
        publicDesktopOperationErrorMessage(
          error,
          t("newRun.operation", { plan: plans.find((plan) => plan.id === effectiveSelectedPlan)?.name ?? effectiveSelectedPlan }),
          t("newRun.error"),
        ),
      )
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button size="sm" className="ml-1">
          <PlusIcon data-icon="inline-start" />
          {t("newRun.title")}
        </Button>
      </SheetTrigger>
      <SheetContent className="sm:max-w-[420px]">
        <SheetHeader>
          <SheetTitle>{t("newRun.title")}</SheetTitle>
          <SheetDescription>
            {t("newRun.description")}
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4">
          <div className="text-xs font-semibold">{t("newRun.selectPlan")}</div>
          <RadioGroup
            aria-label={t("plans.aria")}
            value={effectiveSelectedPlan}
            onValueChange={(value) => { setSelectedPlan(value); setSelectedModel(""); setSelectedChannel("") }}
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
                    {t("newRun.caseCount", { count: plan.caseCount })}
                  </span>
                </Field>
              )
            })}
          </RadioGroup>
					<div className="grid gap-3 border-t pt-3">
						<RuntimeTargetSelect
                            label={t("newRun.model")}
							value={effectiveSelectedModel}
							options={models.map((model) => [model.id, model.name])}
							onChange={(value) => { setSelectedModel(value); setSelectedChannel("") }}
						/>
						<RuntimeTargetSelect
                            label={t("newRun.channel")}
							value={effectiveSelectedChannel}
							options={channels.map((channel) => [channel.id, channel.name])}
							onChange={setSelectedChannel}
						/>
                        {models.length === 0 ? <p className="text-xs text-destructive">{t("newRun.noModels")}</p> : null}
                        {models.length > 0 && channels.length === 0 ? <p className="text-xs text-destructive">{t("newRun.noChannels")}</p> : null}
					</div>
          <div className="border-t pt-3 text-[11px] leading-5 text-muted-foreground">
            {t("newRun.credentialNote")}
          </div>
          {startError ? (
            <p role="alert" className="text-xs text-destructive">
              {startError}
            </p>
          ) : null}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <SheetClose asChild>
            <Button variant="outline">{t("newRun.cancel")}</Button>
          </SheetClose>
          <Button
            disabled={!effectiveSelectedPlan || !effectiveSelectedModel || !effectiveSelectedChannel || commandPending}
            onClick={() => void start()}
          >
            {t(commandPending ? "newRun.creating" : "newRun.start")}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function RuntimeTargetSelect({ label, value, options, onChange }: { label: string; value: string; options: [string, string][]; onChange: (value: string) => void }) {
	const { t } = useTranslation("runs")
	return (
		<Field className="block">
			<FieldLabel>{label}</FieldLabel>
			<Select value={value} onValueChange={onChange} disabled={options.length === 0}>
				<SelectTrigger aria-label={label} className="w-full"><SelectValue placeholder={t("newRun.noOptions", { label })} /></SelectTrigger>
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
  const { t } = useTranslation("runs")

  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="min-[1180px]:hidden"
        >
          <PanelLeftIcon data-icon="inline-start" /> {t("mobile.plans")}
        </Button>
      </SheetTrigger>
      <SheetContent side="left" className="w-[300px] p-0">
        <SheetHeader className="sr-only">
          <SheetTitle>{t("plans.title")}</SheetTitle>
          <SheetDescription>{t("plans.filter")}</SheetDescription>
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
  const { t } = useTranslation("runs")

  return (
    <Sheet>
      <SheetTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="min-[1180px]:hidden"
        >
          <PanelRightIcon data-icon="inline-start" /> {t("mobile.details")}
        </Button>
      </SheetTrigger>
      <SheetContent className="w-[340px] p-0">
        <SheetHeader className="sr-only">
          <SheetTitle>{t("inspector.details")}</SheetTitle>
          <SheetDescription>
            {t("inspector.description", { title: run.title })}
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
  const { t } = useTranslation("runs")
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
            {t(`task.${state}`)}
          </span>
          <span className="truncate text-muted-foreground">{run.title}</span>
          <span className="hidden tabular-nums text-muted-foreground sm:inline">
            {run.total > 0
              ? `${run.completed}/${run.total}`
              : t("task.completedTarget", { count: run.completed, duration: formatTargetDuration(run.targetDurationMS) })}
          </span>
        </div>
        {run.total > 0 ? (
          <Progress
            value={progress}
            className="mt-2 max-w-sm"
            aria-label={t("task.progressAria", { percent: progress })}
          />
        ) : (
          <div className="mt-2 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <Spinner /> {t("task.timedHint")}
          </div>
        )}
      </div>
      <Button
        variant="outline"
        size="sm"
        disabled={state !== "running" || commandPending}
        onClick={onStop}
      >
        <CircleStopIcon data-icon="inline-start" /> {t("task.stop")}
      </Button>
      <Button
        variant="destructive"
        size="sm"
        disabled={commandPending}
        onClick={onCancel}
      >
        <CircleXIcon data-icon="inline-start" /> {t("task.cancel")}
      </Button>
    </div>
  )
}

function IdleTaskBar() {
  const { t } = useTranslation("runs")

  return (
    <div className="flex min-h-14 shrink-0 items-center border-t bg-background px-4 py-2">
      <span className="size-2 rounded-full bg-muted-foreground" aria-hidden="true" />
      <span className="ml-3 text-xs font-semibold">{t("task.idle")}</span>
      <span className="ml-2 text-[11px] text-muted-foreground">
        {t("task.idleHint")}
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
  const { t, i18n } = useTranslation("runs")
  const [activePlanId, setActivePlanId] = useState("all")
  const [selectedRunId, setSelectedRunId] = useState("")

  const presentation = useMemo(
    () => presentWorkspace(snapshot, {
      locale: i18n.resolvedLanguage ?? i18n.language,
      t: (key, values) => t(key, values),
    }),
    [i18n.language, i18n.resolvedLanguage, snapshot, t],
  )
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
                {t("title")}
              </h1>
              <p className="mt-1 truncate text-[11px] text-muted-foreground">
                {t("description")}
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
                {t("count", { count: visibleRuns.length })}
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
          aria-label={t("inspector.details")}
          className="hidden w-[320px] shrink-0 border-l bg-background min-[1180px]:flex"
        >
          {selectedRun ? (
            <RunInspectorContent run={selectedRun} />
          ) : (
            <div className="p-4 text-xs text-muted-foreground">{t("inspector.noneSelected")}</div>
          )}
        </aside>
    </main>
  )
}

function isActiveTaskState(value: string): value is ActiveTaskState {
  return value === "queued" || value === "starting" || value === "running" || value === "draining"
}
