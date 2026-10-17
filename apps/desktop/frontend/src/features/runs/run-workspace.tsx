import { useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"
import { useTranslation } from "react-i18next"

import {
  publicDesktopOperationErrorMessage,
} from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@/components/ui/field"
import { Progress } from "@/components/ui/progress"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"
import type { CatalogSnapshot } from "@/features/catalog/data"

import {
  DEFAULT_CASE_CONCURRENCY,
  formatTargetDuration,
  type RunRecord,
  type RunStatus,
  type StartRunTargetCommand,
  type TestPlan,
} from "./data"
import { CaseConcurrencyField } from "./case-concurrency-field"
import { eligibleRuntimeChannels, eligibleRuntimeModels } from "./run-targets"

const STATUS_CLASS: Record<RunStatus, string> = {
  running: "text-info-strong",
  draining: "text-warning-strong",
  passed: "text-success-strong",
  completed: "text-foreground",
  failed: "text-destructive",
  queued: "text-warning-strong",
  cancelled: "text-muted-foreground",
}

function StatusBadge({ status }: { status: RunStatus }) {
  const { t } = useTranslation("common")

  return (
    <Badge
      variant="outline"
      className={cn(
        "h-5 rounded-none border-0 px-0 text-[11px] font-medium",
        STATUS_CLASS[status],
      )}
    >
      <span className="status-dot" aria-hidden="true" />
      {t(`status.${status}`)}
    </Badge>
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
    <div data-slot="inspector-definition-row" className="min-w-0 py-2 [overflow-wrap:anywhere]">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 whitespace-normal text-xs font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

export function RunInspectorContent({ run, shortcuts, execution, tab, onTabChange }: {
  run: RunRecord; shortcuts?: ReactNode; execution?: ReactNode
  tab: string; onTabChange: (tab: string) => void
}) {
  const { t } = useTranslation("runs")
  const durationOnly = run.total === 0 && run.targetDurationMS > 0
  const targetDetail = run.quickTask ? t("presentation.quickSuite") : durationOnly
    ? t("inspector.targetDuration", { duration: formatTargetDuration(run.targetDurationMS) })
    : t("inspector.fixedRequests", { count: run.total })
  const progressDetail = run.total === 0
    ? t("inspector.completedRequests", { count: run.completed })
    : t("inspector.completedFraction", { completed: run.completed, total: run.total })

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="px-4 pb-3 pt-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-sm font-semibold [overflow-wrap:anywhere]">{run.title}</div>
            <div className="mt-1 [overflow-wrap:anywhere] font-mono text-[10px] text-muted-foreground">
              {run.id}
            </div>
          </div>
          <StatusBadge status={run.status} />
        </div>
      </div>
      <Tabs value={!execution && tab === "execution" ? "summary" : tab} onValueChange={onTabChange} className="min-h-0 min-w-0 flex-1 gap-0">
        <TabsList variant="line" className="mx-4 h-8">
          {execution ? <TabsTrigger value="execution" className="text-xs">{t("inspector.execution")}</TabsTrigger> : null}
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
        {execution ? <TabsContent value="execution" className="min-h-0 min-w-0 flex-1"><ScrollArea contentWidth="viewport" className="min-h-0 h-full px-4">{execution}</ScrollArea></TabsContent> : null}
        <TabsContent
          value="summary"
          className="min-h-0 min-w-0 flex-1"
        >
          <ScrollArea contentWidth="viewport" className="min-h-0 h-full [overflow-wrap:anywhere] px-4 py-2">
          <div className="space-y-3 py-2" aria-label={t("caseProgress.suites")}>
            {run.entryProgress.map((suite, index) => (
              <div key={suite.entry_id} className="min-w-0">
                <div className="flex items-start justify-between gap-2 text-xs">
                  <span className="min-w-0 [overflow-wrap:anywhere]" title={suite.name}>{index + 1}. {suite.name}</span>
                  <span className="shrink-0 text-muted-foreground">{t(`caseProgress.status.${suite.status}`)}</span>
                </div>
                <div className="mt-1 text-[11px] tabular-nums text-muted-foreground">
                  {t("caseProgress.count", { completed: suite.observed_case_count, total: suite.case_count })}
                </div>
                <Progress value={Math.round(suite.observed_case_count / suite.case_count * 100)} className="mt-1.5"
                  aria-label={t("caseProgress.suiteAria", { name: suite.name, completed: suite.observed_case_count, total: suite.case_count })} />
              </div>
            ))}
          </div>
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
          </ScrollArea>
        </TabsContent>
        <TabsContent
          value="failures"
          className="min-h-0 min-w-0 flex-1"
        >
          <ScrollArea contentWidth="viewport" className="min-h-0 h-full [overflow-wrap:anywhere] px-4 py-3 text-xs">{run.failureSummary ?? t("inspector.noFailures")}</ScrollArea>
        </TabsContent>
        <TabsContent
          value="artifacts"
          className="min-h-0 min-w-0 flex-1"
        >
          <ScrollArea contentWidth="viewport" className="min-h-0 h-full [overflow-wrap:anywhere] px-4 py-3 text-xs">
          {run.artifactCount > 0
            ? t("inspector.artifactsPending", { count: run.artifactCount })
            : t("inspector.noArtifacts")}
          </ScrollArea>
        </TabsContent>
      </Tabs>
      {shortcuts}
    </div>
  )
}

export function NewRunSheet({
  plans,
	catalog,
  commandPending,
  onStartRun,
  triggerLabel,
  onOpen,
}: {
  plans: TestPlan[]
	catalog: CatalogSnapshot
  commandPending: boolean
  onStartRun: (command: StartRunTargetCommand) => Promise<void>
  triggerLabel?: string
  onOpen?: () => Promise<unknown>
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("runs")
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState("")
  const planGroup = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (open && !loading && !loadError) {
      planGroup.current?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]')?.focus()
    }
  }, [open, loading, loadError])
  const loadCatalog = () => {
    if (!onOpen) return
    setLoading(true)
    setLoadError("")
    void onOpen().catch(error => setLoadError(publicDesktopOperationErrorMessage(error, t("newRun.title"), t("newRun.error"))))
      .finally(() => setLoading(false))
  }
  const [selectedPlan, setSelectedPlan] = useState(plans[0]?.id ?? "")
	const [selectedModel, setSelectedModel] = useState("")
	const [selectedChannel, setSelectedChannel] = useState("")
  const [startError, setStartError] = useState("")
  const [caseConcurrency, setCaseConcurrency] = useState(DEFAULT_CASE_CONCURRENCY)
  const effectiveSelectedPlan = plans.some((plan) => plan.id === selectedPlan)
    ? selectedPlan
    : (plans[0]?.id ?? "")
  const hasSinglePassEntries = catalog.plans
    .find((plan) => plan.id === effectiveSelectedPlan)?.entries
    .some((entry) => entry.load_mode === "single") ?? false
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
      await onStartRun({
        plan_id: effectiveSelectedPlan,
        model_id: effectiveSelectedModel,
        channel_id: effectiveSelectedChannel,
        case_concurrency: caseConcurrency,
      })
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
    <Sheet open={open} onOpenChange={next => { setOpen(next); if (next) loadCatalog() }}>
      <SheetTrigger asChild>
        <Button size="sm" className="ml-1" disabled={commandPending}>
          <PlusIcon data-icon="inline-start" />
          {triggerLabel ?? t("newRun.title")}
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
          {loading ? <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Spinner />{tx("app:loadingPage")}</p> : loadError ? <div role="alert" className="space-y-2 text-xs text-destructive">{loadError}<Button variant="outline" size="sm" onClick={loadCatalog}>{tx("desktop:app_retry_opening")}</Button></div> : <>
          <div className="text-xs font-semibold">{t("newRun.selectPlan")}</div>
          <RadioGroup
            ref={planGroup}
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
          {hasSinglePassEntries ? <CaseConcurrencyField value={caseConcurrency} onChange={setCaseConcurrency} disabled={commandPending} /> : null}
          {startError ? (
            <p role="alert" className="text-xs text-destructive">
              {startError}
            </p>
          ) : null}
          </>}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <SheetClose asChild>
            <Button variant="outline">{t("newRun.cancel")}</Button>
          </SheetClose>
          <Button
            disabled={loading || !!loadError || !effectiveSelectedPlan || !effectiveSelectedModel || !effectiveSelectedChannel || commandPending}
            onClick={() => void start()}
          >
            {commandPending ? tx("desktop:runs_creating") : tx("desktop:runs_start_run")}
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
			<SearchableSelect value={value} onValueChange={onChange} disabled={options.length === 0} aria-label={label} className="w-full" placeholder={t("newRun.noOptions", { label })} options={[...options.map(([id, name]) => ({value: id, label: name}))]} />
		</Field>
	)
}
