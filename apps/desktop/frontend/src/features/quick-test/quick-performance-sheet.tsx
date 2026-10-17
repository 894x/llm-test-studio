import { ExecutionOriginDetails } from "@/features/reports/execution-origin-details"
import { formatPerformanceInteger } from "@/features/reports/performance-format"
import { localizeStoredMessage, desktopLocale, translateDesktop as tx } from "@/i18n/runtime"
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import CheckCircle2Icon from "lucide-react/dist/esm/icons/check-circle-2.mjs"
import CircleAlertIcon from "lucide-react/dist/esm/icons/circle-alert.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"

import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { JobDetails } from "@/features/jobs/job-details"
import { launchAtDefault, type JobClient } from "@/features/jobs/execution"
import { isJobTerminal, type JobView } from "@/features/jobs/data"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
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
import { Spinner } from "@/components/ui/spinner"
import { PerformanceCharts } from "@/features/reports/performance-charts"
import { PerformanceLatencyTable } from "@/features/reports/performance-latency-table"
import { PerformanceStreamingTimingTable } from "@/features/reports/performance-streaming-timing-table"
import type { CatalogSnapshot } from "@/features/catalog/data"
import { eligibleRuntimeChannels, eligibleRuntimeModels } from "@/features/runs/run-targets"

import type { QuickPerformanceLoadMode, QuickPerformanceCommand, QuickPerformanceProgress, QuickPerformanceReport, QuickPerformanceProfile } from "./data"
import { performanceFormFromProfile } from "./performance-profile"
import { PerformanceProfileFields } from "./performance-profile-fields"
import { usePerformanceProfileEditor } from "./use-performance-profile-editor"
import {
  performanceCapacitySummary,
  performanceCompletion,
  performanceProgressPhaseLabel,
  performanceSLOStatusLabel,
} from "./performance-summary"
import { QuickPerformanceRequestAnalysis } from "./quick-performance-request-analysis"

export type QuickPerformanceConnection = Pick<
  QuickPerformanceCommand,
  "protocol" | "address_mode" | "url" | "api_key" | "channel_id" | "credential_run_id" | "model_id" | "task"
>

export function PlanPerformanceRunSheet({
  planID,
  catalog,
  client,
  onArchived,
  onOpenReport,
  triggerLabel,
}: {
  planID: string
  catalog: CatalogSnapshot
  client: JobClient
  onArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
  triggerLabel: string
}) {
  const { t } = useTranslation("quickTest")
  const [selectionOpen, setSelectionOpen] = useState(false)
  const [performanceOpen, setPerformanceOpen] = useState(false)
  const [selectedModel, setSelectedModel] = useState("")
  const [selectedChannel, setSelectedChannel] = useState("")
  const models = eligibleRuntimeModels(catalog, planID)
  const effectiveModel = models.some((model) => model.id === selectedModel) ? selectedModel : (models[0]?.id ?? "")
  const channels = eligibleRuntimeChannels(catalog, planID, effectiveModel)
  const effectiveChannel = channels.some((channel) => channel.id === selectedChannel) ? selectedChannel : (channels[0]?.id ?? "")
  const channel = channels.find((item) => item.id === effectiveChannel)

  return <>
    <Sheet open={selectionOpen} onOpenChange={setSelectionOpen}>
      <SheetTrigger asChild>
        <Button size="sm" disabled={!models.length}>{triggerLabel}</Button>
      </SheetTrigger>
      <SheetContent className="sm:max-w-[420px]">
        <SheetHeader>
          <SheetTitle>{t("performance.selectBinding")}</SheetTitle>
          <SheetDescription>{t("performance.selectBindingHint")}</SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4">
          <Field className="block">
            <FieldLabel>{t("performance.model")}</FieldLabel>
            <FieldContent>
              <SearchableSelect value={effectiveModel} onValueChange={(value) => { setSelectedModel(value); setSelectedChannel("") }} options={models.map((model) => ({ value: model.id, label: model.name }))} aria-label={t("performance.model")} className="w-full" />
            </FieldContent>
          </Field>
          <Field className="block">
            <FieldLabel>{t("performance.channel")}</FieldLabel>
            <FieldContent>
              <SearchableSelect value={effectiveChannel} onValueChange={setSelectedChannel} options={channels.map((item) => ({ value: item.id, label: item.name }))} aria-label={t("performance.channel")} className="w-full" />
            </FieldContent>
          </Field>
          {models.length === 0 ? <p className="text-xs text-destructive">{t("performance.noModels")}</p> : null}
          {models.length > 0 && channels.length === 0 ? <p className="text-xs text-destructive">{t("performance.noChannels")}</p> : null}
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <SheetClose asChild><Button variant="outline">{t("performance.cancel")}</Button></SheetClose>
          <Button disabled={!effectiveModel || !effectiveChannel} onClick={() => { setSelectionOpen(false); setPerformanceOpen(true) }}>{t("performance.continue")}</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
    {channel && effectiveModel ? <QuickPerformanceSheet
      open={performanceOpen}
      onOpenChange={setPerformanceOpen}
      connection={{ protocol: catalog.plans.find(plan => plan.id === planID)!.protocol, address_mode: "base_url", url: channel.base_url, api_key: "", channel_id: channel.id, model_id: effectiveModel }}
      plans={catalog.plans}
      initialPlanID={planID}
      client={client}
      onArchived={onArchived}
      onOpenReport={onOpenReport}
    /> : null}
  </>
}

type QuickPerformancePanelProps = {
  connection: QuickPerformanceConnection
  actionsContainer?: HTMLElement | null
  showPlanSelector?: boolean
  plans: CatalogSnapshot["plans"]
  initialPlanID?: string
  initialProfile?: QuickPerformanceProfile
  initialJob?: JobView | null
  onPlanChange?: (id: string) => void
  client: JobClient
  onJobLaunched?: (job: JobView, command: QuickPerformanceCommand) => void
  onJobChange?: (job: JobView) => void
  onPendingChange?: (pending: boolean) => void
  onArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
  resolveConnection?: () => QuickPerformanceConnection | undefined
}

export function QuickPerformanceSheet({
  open, onOpenChange, onCloseAutoFocus, ...props
}: QuickPerformancePanelProps & {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCloseAutoFocus?: (event: Event) => void
}) {
  const { t } = useTranslation("quickTest")
  const [job, setJob] = useState<JobView | null>(null)
  const connectionIdentity = JSON.stringify(props.connection)
  const [outputIdentity, setOutputIdentity] = useState(connectionIdentity)
  if (outputIdentity !== connectionIdentity) {
    setOutputIdentity(connectionIdentity)
    setJob(null)
  }
  const { onJobLaunched, onJobChange } = props
  const jobLaunched = useCallback((accepted: JobView, command: QuickPerformanceCommand) => {
    setJob(accepted)
    onJobLaunched?.(accepted, command)
  }, [onJobLaunched])
  const jobChanged = useCallback((observed: JobView) => {
    setJob(observed)
    onJobChange?.(observed)
  }, [onJobChange])
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent onCloseAutoFocus={onCloseAutoFocus} className="data-[side=right]:w-full data-[side=right]:sm:max-w-5xl">
        <SheetHeader>
          <SheetTitle>{t("performance.title")}</SheetTitle>
          <SheetDescription>
            {t("performance.description")}
            <span className="mt-1 block break-all">{props.connection.model_id} · {props.connection.url}</span>
          </SheetDescription>
        </SheetHeader>
        <QuickPerformancePanel {...props} initialJob={job} onJobLaunched={jobLaunched} onJobChange={jobChanged} />
      </SheetContent>
    </Sheet>
  )
}

export function QuickPerformancePanel({
  connection, plans, initialPlanID, initialProfile, initialJob, onPlanChange, showPlanSelector = true, client, onJobLaunched, onJobChange, onPendingChange, onArchived, onOpenReport, resolveConnection, actionsContainer, embedded = false, blocked = false,
}: QuickPerformancePanelProps & { embedded?: boolean; blocked?: boolean }) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("quickTest")
  const [planID, setPlanID] = useState(initialPlanID ?? "")
  const availablePlans = plans.filter(plan => plan.performance && plan.protocol === connection.protocol)
  const plan = availablePlans.find(plan => plan.id === planID)
  const editor = usePerformanceProfileEditor(initialProfile ?? plan?.performance)
  const { form } = editor
  const [pending, setPending] = useState(false)
  const [job, setJob] = useState<JobView | null>(initialJob ?? null)
  const busy = pending || !!job && (!job.execution || !isJobTerminal(job.execution.status))
  const [operationError, setOperationError] = useState("")
  const requestGeneration = useRef({ value: 0 })
  // This identity stays in memory only, including the temporary credential.
  const connectionIdentity = JSON.stringify([connection, form])
  const [outputIdentity, setOutputIdentity] = useState(connectionIdentity)
  if (outputIdentity !== connectionIdentity) {
    setOutputIdentity(connectionIdentity)
    setJob(null)
    setOperationError("")
    setPending(false)
  }
  useEffect(() => {
    const generation = requestGeneration.current
    generation.value++
    return () => {
      generation.value++
    }
  }, [connectionIdentity])
  useEffect(() => { onPendingChange?.(busy) }, [busy, onPendingChange])
  const observeJob = useCallback((observed: JobView) => { setJob(observed); onJobChange?.(observed) }, [onJobChange])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (busy || blocked) return
    const target = resolveConnection ? resolveConnection() : connection
    if (!target) return
    const profile = editor.validate()
    if (!profile) return
    setPending(true)
    setJob(null)
    setOperationError("")
    const generation = ++requestGeneration.current.value
    const command: QuickPerformanceCommand = {
        protocol: target.protocol, address_mode: target.address_mode,
        url: target.url, api_key: target.api_key, model_id: target.model_id,
        ...(target.channel_id ? { channel_id: target.channel_id } : {}),
        ...(target.credential_run_id ? { credential_run_id: target.credential_run_id } : {}),
        ...(target.task ? { task: target.task } : {}),
        ...profile,
      }
    void launchAtDefault(client, { kind: "performance", plan: null, quick_task: null, comparison: null, performance: command })
      .then((accepted) => {
        if (requestGeneration.current.value === generation) {
          setJob(accepted)
          onJobLaunched?.(accepted, command)
        }
      })
      .catch((reason: unknown) => {
        if (requestGeneration.current.value === generation)
          setOperationError(
            publicDesktopErrorMessage(
              reason,
              tx(
                "desktop:quick-test_quick_performance_testing_is_unavailable_check_the_local_logs",
              ),
            ),
          )
      })
      .finally(() => {
        if (requestGeneration.current.value === generation) setPending(false)
      })
  }

  const content = (
    <>
          <form id="quick-performance-form" onSubmit={submit} className="space-y-4 pb-4" noValidate>
            <FieldGroup>
              {showPlanSelector ? <Field className="block">
                <FieldLabel htmlFor="quick-performance-plan">{t("performance.plan")}</FieldLabel>
                <FieldContent>
                  <SearchableSelect id="quick-performance-plan" aria-label={t("performance.plan")} value={plan?.id ?? ""}
                    disabled={busy || blocked} className="w-full" placeholder={t("performance.choosePlan")}
                    options={availablePlans.map(item => ({ value: item.id, label: item.name }))}
                    onValueChange={id => {
                      const selected = availablePlans.find(item => item.id === id)
                      if (!selected?.performance) return
                      setPlanID(id); onPlanChange?.(id)
                      editor.setForm(performanceFormFromProfile(selected.performance))
                      editor.setFieldErrors({})
                    }} />
                  <FieldDescription>{t(availablePlans.length ? "performance.planHint" : "performance.noPlans")}</FieldDescription>
                </FieldContent>
              </Field> : null}
              <PerformanceProfileFields editor={editor} pending={busy || blocked} actionsContainer={actionsContainer} />
              {operationError ? (
                <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                  {localizeStoredMessage(operationError, tx)}
                </FieldError>
              ) : null}
            </FieldGroup>
          </form>
          {job ? <JobDetails key={job.job_id} client={client} jobID={job.job_id} initialJob={job}
            onJobChange={observeJob}
            onArchived={onArchived} onOpenReport={onOpenReport} reportIDs={job.report_ids}
            renderProgress={progress => <QuickPerformanceProgressPanel progress={progress} requestCount={form.requestCount} loadMode={form.loadMode} />}
            renderPerformance={report => <QuickPerformanceReportPanel report={report} onOpenReport={onOpenReport} />}
          /> : !embedded && pending ? (
            <div className="flex min-h-36 flex-col items-center justify-center rounded-lg border border-dashed text-center">
              <Spinner className="size-5" />
              <p className="mt-3 text-sm font-medium">{t("performance.running")}</p>
              <p className="mt-1 text-xs text-muted-foreground">{t("performance.runningHint")}</p>
            </div>
          ) : null}
    </>
  )
  const startButton = (
          <Button type="submit" form="quick-performance-form" disabled={busy || blocked}>
            {busy ? (
              <>
                <Spinner data-icon="inline-start" />
                {t("testing")}
              </>
            ) : (
              <>
                <GaugeIcon data-icon="inline-start" />
                {t("performance.start")}
              </>
            )}
          </Button>
  )
  return embedded ? (
    <section aria-label={t("performance.title")} className="min-w-0 space-y-4">
      {content}
      <div className="flex justify-end">{startButton}</div>
    </section>
  ) : (
    <>
      <ScrollArea className="min-h-0 flex-1 px-4">{content}</ScrollArea>
      <SheetFooter>{startButton}</SheetFooter>
    </>

  )
}

export function QuickPerformanceReportPanel({
  report,
  onOpenReport,
}: {
  report: QuickPerformanceReport
  onOpenReport?: (reportID: string) => void | Promise<void>
}) {
  const { t: tx } = useTranslation()
  const { t, i18n } = useTranslation("quickTest")
  const completion = performanceCompletion(
    report.profile.request_count,
    report.metrics.completed,
    report.progress.planned,
  )
  const hasPreparationData =
    report.request_budget !== undefined ||
    report.warmup !== undefined ||
    report.ramp !== undefined ||
    report.time_slices !== undefined
  const completedWithFailures =
    !report.success && !report.error_code && report.metrics.completed > 0
  const title = report.success
    ? t("performance.complete")
    : completedWithFailures
      ? t("performance.completeWithFailures")
      : t(`common:errorCode.${report.error_code ?? "request_failed"}`, {
          defaultValue: report.error_code ?? "request_failed",
        })
  return (
    <section
      aria-label={t("performance.reportAria")}
      className="rounded-lg border bg-surface-subtle"
    >
      <div className="flex items-start gap-3 p-4">
        {report.success ? (
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-success" />
        ) : (
          <CircleAlertIcon className="mt-0.5 size-5 shrink-0 text-warning" />
        )}
        <div className="min-w-0">
          <h3 className="text-sm font-semibold">{title}</h3>
          <div className="mt-2 flex flex-wrap gap-1.5">
            <Badge
              variant="outline"
              className={
                report.success
                  ? "border-success/30 bg-success-soft text-success"
                  : "border-warning/30 bg-warning-soft text-warning"
              }
            >
              {tx("desktop:quick-test_transport_and_protocol")}
              {report.success
                ? tx("desktop:quick-test_passed")
                : tx("desktop:quick-test_failed_301")}
            </Badge>
            {report.slo_assessment ? (
              <Badge
                variant={report.slo_assessment.status === "failed" ? "destructive" : "outline"}
                className={
                  report.slo_assessment.status === "passed"
                    ? "border-success/30 bg-success-soft text-success"
                    : report.slo_assessment.status === "not_evaluated"
                      ? "border-warning/30 bg-warning-soft text-warning"
                      : undefined
                }
              >
                {performanceSLOStatusLabel(report.slo_assessment.status)}
              </Badge>
            ) : null}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {report.success
              ? tx(
                  "desktop:quick-test_all_steady_state_requests_completed_and_passed_protocol_and_semantic",
                )
              : tx(
                  "desktop:quick-test_filter_steady_state_requests_by_failure_reason_and_inspect_redacted",
                )}
          </p>
        </div>
      </div>
      <div className="px-4 pb-4"><ExecutionOriginDetails origin={report.execution_origin} /></div>
      <Separator />
      <div className="space-y-4 p-4">
        <p className="rounded-md border bg-background/70 px-3 py-2 text-[11px] text-muted-foreground">
          {tx("desktop:quick-test_primary_metrics_cover_steady_state_only_warmup_and_ramp_traffic")}
        </p>
        {hasPreparationData ? (
          <div>
            <h4 className="mb-2 text-xs font-semibold">
              {tx("desktop:quick-test_preparation_phases")}
            </h4>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
              {report.request_budget ? (
                <ResultValue
                  label={tx("desktop:quick-test_request_budget")}
                  value={formatPerformanceBudget(report.request_budget)}
                  numeric
                  wide
                />
              ) : null}
              {report.warmup ? (
                <ResultValue
                  label={tx("desktop:quick-test_warmup")}
                  value={formatTrafficCompletion(report.warmup)}
                  numeric
                />
              ) : null}
              {report.ramp ? (
                <ResultValue
                  label={tx("desktop:quick-test_ramp_308")}
                  value={formatTrafficCompletion(report.ramp.traffic)}
                  numeric
                />
              ) : null}
              {report.time_slices ? (
                <ResultValue
                  label={tx("desktop:quick-test_time_slices")}
                  value={tx("desktop:quick-test_value_slices_value_resolution", {
                    value1: formatNumber(report.time_slices.length),
                    value2: formatDuration(report.profile.slice_duration_ms ?? 0),
                  })}
                  numeric
                  wide
                />
              ) : null}
            </dl>
            {report.ramp && !report.ramp.completed_window ? (
              <p
                role="status"
                className="mt-3 rounded-md border border-warning/25 bg-warning-soft px-3 py-2 text-[11px] text-warning"
              >
                {tx("desktop:quick-test_the_ramp_window_did_not_complete_consider_the_request_cap")}
              </p>
            ) : null}
          </div>
        ) : null}
        {report.slo_assessment ? (
          <MetricSection title={tx("desktop:quick-test_slo_and_goodput")}>
            <InlineResultValue
              label={tx("desktop:quick-test_good_requests")}
              value={`${formatNumber(report.slo_assessment.good_requests)} / ${formatNumber(report.slo_assessment.total_requests)}`}
              numeric
            />
            <ResultValue
              label={tx("desktop:quick-test_compliance_target")}
              value={`${formatNumber(report.slo_assessment.good_request_percent)}% / ${formatNumber(report.slo_assessment.target_percent)}%`}
              numeric
            />
            <InlineResultValue
              label="Goodput"
              value={`${formatPerformanceInteger(report.slo_assessment.goodput_qps)} req/s`}
              numeric
            />
            <ResultValue
              label={tx("desktop:quick-test_violations_transport_ttft_tpot_e2e")}
              value={`${report.slo_assessment.violations.transport} / ${report.slo_assessment.violations.ttft} / ${report.slo_assessment.violations.tpot} / ${report.slo_assessment.violations.e2e}`}
              numeric
              wide
            />
          </MetricSection>
        ) : null}
        {report.capacity_result ? (
          <div className="rounded-md border bg-background/70 px-3 py-2">
            <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
              {tx("desktop:quick-test_capacity_conclusion")}
            </p>
            <p className="mt-1 text-xs font-semibold tabular-nums">
              {performanceCapacitySummary(
                report.capacity_result,
                report.profile.load_mode ?? "fixed_concurrency",
              )}
            </p>
          </div>
        ) : null}
        <MetricSection title={tx("desktop:quick-test_steady_state_execution_summary")}>
          <ResultValue label={completion.label} value={completion.value} numeric />
          <ResultValue
            label={tx("desktop:quick-test_succeeded")}
            value={String(report.metrics.succeeded)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_failed")}
            value={String(report.metrics.failed)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_success_rate")}
            value={`${formatNumber(report.metrics.success_rate_percent)}%`}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_total_duration")}
            value={`${formatPerformanceInteger(report.progress.total_duration_ms)} ms`}
            numeric
          />
          {report.profile.load_mode === "open_loop" ? (
            <ResultValue
              label={tx("desktop:quick-test_peak_in_flight_limit")}
              value={`${report.progress.peak_in_flight} / ${report.profile.max_in_flight === undefined ? "—" : formatNumber(report.profile.max_in_flight)}`}
              numeric
            />
          ) : (
            <ResultValue
              label={tx("desktop:quick-test_peak_in_flight_configured_concurrency")}
              value={`${report.progress.peak_in_flight} / ${formatCapacityTarget(report.progress.capacity_target ?? report.profile.concurrency)}`}
              numeric
            />
          )}
        </MetricSection>
        <MetricSection title={tx("desktop:quick-test_workload")}>
          {report.profile.load_mode === "open_loop" ? (
            <ResultValue
              label={tx("desktop:quick-test_arrival_distribution")}
              value={performanceArrivalPattern(report)}
            />
          ) : null}
          <ResultValue
            label={tx("desktop:quick-test_token_distribution")}
            value={performanceWorkloadMode(report)}
          />
          <ResultValue
            label={tx("desktop:quick-test_random_input")}
            value={report.profile.random_input ? tx("desktop:quick-test_enabled") : tx("desktop:quick-test_disabled")}
          />
          <ResultValue
            label={tx("desktop:quick-test_random_seed")}
            value={performanceSeed(report)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_shared_prefix")}
            value={performanceSharedPrefix(report)}
            numeric
          />
          {performanceTargetRanges(report) ? (
            <ResultValue
              label={tx("desktop:quick-test_sampled_target_range_input_output")}
              value={performanceTargetRanges(report)!}
              numeric
              wide
            />
          ) : null}
        </MetricSection>
        <MetricSection title={tx("desktop:quick-test_steady_state_throughput")}>
          <ResultValue
            label={tx("desktop:quick-test_target_send_rate")}
            value={performanceTargetRate(report)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_offered_load")}
            value={formatOptionalRate(report.metrics.offered_qps)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_actual_send_rate")}
            value={formatOptionalRate(report.metrics.launched_qps)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_completed_request_throughput")}
            value={formatOptionalRate(report.metrics.completed_qps)}
            numeric
          />
          <ResultValue
            label={tx("desktop:quick-test_successful_request_throughput")}
            value={formatOptionalRate(report.metrics.successful_request_qps)}
            numeric
          />
          <ResultValue label="RPM" value={`${formatPerformanceInteger(report.metrics.rpm)} RPM`} numeric />
          <ResultValue
            label="Input TPM"
            value={`${formatPerformanceInteger(report.metrics.input_tpm)} TPM`}
            numeric
          />
          <ResultValue
            label="Output TPM"
            value={`${formatPerformanceInteger(report.metrics.output_tpm)} TPM`}
            numeric
          />
          <ResultValue
            label="Total TPM"
            value={`${formatPerformanceInteger(report.metrics.total_tpm)} TPM`}
            numeric
          />
          <ResultValue
            label={t("performance.generationRate")}
            value={`${formatPerformanceInteger(report.metrics.generation_tps)} token/s`}
            numeric
          />
        </MetricSection>
        <PerformanceLatencyTable metrics={report.metrics} />
        <PerformanceStreamingTimingTable
          metrics={report.metrics}
        />
        <MetricSection title="Token">
          <ResultValue
            label="Prompt / Completion / Cached"
            value={`${report.metrics.prompt_tokens} / ${report.metrics.completion_tokens} / ${report.metrics.cached_tokens}`}
            numeric
          />
          <ResultValue
            label={t("performance.cacheRate")}
            value={`${formatNumber(report.metrics.cache_rate_percent)}%`}
            numeric
          />
          <ResultValue
            label={t("performance.timedOut")}
            value={String(report.metrics.timed_out)}
            numeric
          />
        </MetricSection>
        {report.samples.length ? (
          <PerformanceCharts
            layout="stacked"
            samples={report.samples}
            percentiles={report.metrics}
          />
        ) : null}
        {report.failures.length ? (
          <div>
            <h4 className="text-xs font-semibold">{t("performance.failures")}</h4>
            <ul className="mt-2 space-y-1 text-xs">
              {report.failures.map((failure) => (
                <li key={failure.error_code} className="flex items-center justify-between gap-4">
                  <span className="text-muted-foreground">
                    {t(`common:errorCode.${failure.error_code}`, {
                      defaultValue: failure.error_code,
                    })}
                  </span>
                  <span className="tabular-nums">{failure.count}</span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {report.samples.length ? <QuickPerformanceRequestAnalysis report={report} /> : null}
        {report.archived && report.report_id ? (
          <div className="flex items-center justify-between gap-3 border-t pt-3">
            <p className="text-[10px] text-muted-foreground">
              {t("performance.archived", {
                date: report.generated_at
                  ? new Date(report.generated_at).toLocaleString(
                      i18n.resolvedLanguage ?? i18n.language,
                    )
                  : t("performance.completionTime"),
              })}
            </p>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void onOpenReport?.(report.report_id!)}
            >
              {t("performance.openReport")}
            </Button>
          </div>
        ) : report.archive_status === "failed" ? (
          <p role="status" className="border-t pt-3 text-[11px] text-warning">
            {t("performance.notArchived")}
          </p>
        ) : null}
      </div>
    </section>
  )
}

function QuickPerformanceProgressPanel({
  progress,
  requestCount,
  loadMode,
}: {
  progress: QuickPerformanceProgress
  requestCount: number
  loadMode: QuickPerformanceLoadMode
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("quickTest")
  const isPreparation = progress.phase === "warming_up" || progress.phase === "ramping"
  const percentage =
    (requestCount > 0 || isPreparation) && progress.planned > 0
      ? Math.min(100, (progress.completed / progress.planned) * 100)
      : undefined
  const phaseLabel = performanceProgressPhaseLabel(progress.phase)
  const completion =
    progress.phase === "warming_up"
      ? {
          label: tx("desktop:quick-test_warmup_completed_planned"),
          value: `${progress.completed} / ${progress.planned}`,
        }
      : progress.phase === "ramping"
        ? {
            label: tx("desktop:quick-test_ramp_completed_planned"),
            value: `${progress.completed} / ${progress.planned}`,
          }
        : performanceCompletion(requestCount, progress.completed, progress.planned)
  return (
    <section
      role="status"
      aria-label={t("performance.progressAria")}
      className="rounded-lg border bg-surface-subtle p-4"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Spinner className="size-4 shrink-0" />
          <span className="text-sm font-medium">{phaseLabel}</span>
          {progress.capacity_rung_number !== undefined ? (
            <Badge variant="outline" className="tabular-nums">
              {tx("desktop:quick-test_step")} {progress.capacity_rung_number} /{" "}
              {progress.capacity_rung_count}
            </Badge>
          ) : null}
        </div>
        <span className="text-xs tabular-nums text-muted-foreground">
          {percentage === undefined
            ? t("performance.durationMode")
            : `${formatNumber(percentage)}%`}
        </span>
      </div>
      <Progress
        className="mt-3 h-1.5"
        value={percentage}
        aria-label={t("performance.requestProgress")}
        aria-valuenow={percentage}
        aria-valuemin={percentage === undefined ? undefined : 0}
        aria-valuemax={percentage === undefined ? undefined : 100}
      />
      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4">
        <ResultValue label={completion.label} value={completion.value} numeric />
        {progress.offered === undefined ? null : (
          <ResultValue
            label={tx("desktop:quick-test_offered_load")}
            value={String(progress.offered)}
            numeric
          />
        )}
        <ResultValue
          label={tx("desktop:quick-test_succeeded")}
          value={String(progress.succeeded)}
          numeric
        />
        <ResultValue
          label={tx("desktop:quick-test_failed")}
          value={String(progress.failed)}
          numeric
        />
        <ResultValue
          label={tx("desktop:quick-test_in_flight")}
          value={String(progress.in_flight)}
          numeric
        />
        {progress.capacity_target === undefined ? null : (
          <InlineResultValue
            label={tx("desktop:quick-test_current_target")}
            value={`${formatCapacityTarget(progress.capacity_target)} ${loadMode === "fixed_concurrency" ? tx("desktop:quick-test_concurrency") : "RPS"}`}
            numeric
          />
        )}
      </dl>
      {progress.capped ? (
        <p className="mt-3 border-t pt-2 text-[11px] text-warning">
          {tx("desktop:quick-test_the_current_phase_reached_its_request_cap")}
        </p>
      ) : null}
    </section>
  )
}

function MetricSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h4 className="mb-2 text-xs font-semibold">{title}</h4>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-xs">{children}</dl>
    </div>
  )
}

function ResultValue({
  label,
  value,
  numeric = false,
  wide = false,
  mono = false,
}: {
  label: string
  value: string
  numeric?: boolean
  wide?: boolean
  mono?: boolean
}) {
  return (
    <div className={wide ? "col-span-2 min-w-0" : "min-w-0"}>
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd
        className={`mt-0.5 truncate ${numeric ? "tabular-nums" : ""} ${mono ? "font-mono text-[11px]" : ""}`}
        title={value}
      >
        {value}
      </dd>
    </div>
  )
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 1 }).format(value)
}

function formatCapacityTarget(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 10 }).format(value)
}

function InlineResultValue({
  label,
  value,
  numeric = false,
}: {
  label: string
  value: string
  numeric?: boolean
}) {
  return (
    <div className="min-w-0">
      <dt className="sr-only">{label}</dt>
      <dd className={`truncate ${numeric ? "tabular-nums" : ""}`} title={`${label} ${value}`}>
        {label} {value}
      </dd>
    </div>
  )
}

function formatDuration(valueMS: number): string {
  return valueMS >= 1_000 ? `${formatNumber(valueMS / 1_000)} s` : `${formatPerformanceInteger(valueMS)} ms`
}

function formatPerformanceBudget(
  budget: NonNullable<QuickPerformanceReport["request_budget"]>,
): string {
  return tx("desktop:quick-test_value_value_warmup_value_ramp_value_steady_state_value", {
    value1: formatNumber(budget.total_cap),
    value2: formatNumber(budget.limit),
    value3: formatNumber(budget.warmup_cap),
    value4: formatNumber(budget.ramp_cap),
    value5: formatNumber(budget.measured_cap),
  })
}

function formatTrafficCompletion(traffic: NonNullable<QuickPerformanceReport["warmup"]>): string {
  return tx("desktop:quick-test_value_value_succeeded_value_failed_value_rejected_value", {
    value1: formatNumber(traffic.completed),
    value2: formatNumber(traffic.request_cap),
    value3: formatNumber(traffic.succeeded),
    value4: formatNumber(traffic.failed),
    value5: formatNumber(traffic.rejected),
  })
}

function formatOptionalRate(value: number | undefined): string {
  return value === undefined ? "—" : `${formatPerformanceInteger(value)} req/s`
}

function performanceArrivalPattern(report: QuickPerformanceReport): string {
  if (report.profile.arrival_pattern === "poisson") return tx("desktop:quick-test_poisson_arrivals")
  return report.profile.arrival_pattern === "constant"
    ? tx("desktop:quick-test_constant_interval")
    : tx("desktop:quick-test_constant_interval")
}

function performanceWorkloadMode(report: QuickPerformanceReport): string {
  if (report.profile.workload_mode === "normal") {
    return tx("desktop:quick-test_normal_distribution_input_value_output_value", {
      value1: formatNumber(report.profile.input_tokens_stddev ?? 0),
      value2: formatNumber(report.profile.output_tokens_stddev ?? 0),
    })
  }
  return report.profile.workload_mode === "fixed"
    ? tx("desktop:quick-test_fixed_tokens")
    : tx("desktop:quick-test_fixed_tokens")
}

function performanceSeed(report: QuickPerformanceReport): string {
  if (report.profile.random_seed === undefined) return tx("desktop:quick-test_not_used")
  return report.profile.random_seed > 0
    ? formatNumber(report.profile.random_seed)
    : tx("desktop:quick-test_not_used")
}

function performanceSharedPrefix(report: QuickPerformanceReport): string {
  if (report.profile.shared_prefix_tokens === undefined)
    return "0 Token"
  return `${formatNumber(report.profile.shared_prefix_tokens)} Token`
}

function performanceTargetRanges(report: QuickPerformanceReport): string | undefined {
  const inputTargets = report.samples.flatMap((sample) =>
    sample.target_input_tokens === undefined ? [] : [sample.target_input_tokens],
  )
  const outputTargets = report.samples.flatMap((sample) =>
    sample.target_output_tokens === undefined ? [] : [sample.target_output_tokens],
  )
  if (inputTargets.length === 0 && outputTargets.length === 0) return undefined
  return `${formatIntegerRange(inputTargets)} / ${formatIntegerRange(outputTargets)}`
}

function formatIntegerRange(values: number[]): string {
  if (values.length === 0) return "—"
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  return minimum === maximum
    ? formatNumber(minimum)
    : `${formatNumber(minimum)}–${formatNumber(maximum)}`
}

function performanceTargetRate(report: QuickPerformanceReport): string {
  const target = report.progress.capacity_target ?? report.profile.rate_per_second
  return report.profile.load_mode === "open_loop" && target !== undefined
    ? `${formatCapacityTarget(target)} req/s`
    : tx("desktop:quick-test_fixed_concurrency")
}
