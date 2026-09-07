import { formatPerformanceInteger } from "@/features/reports/performance-format"
import { localizeStoredMessage, desktopLocale, translateDesktop as tx } from "@/i18n/runtime"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import CheckCircle2Icon from "lucide-react/dist/esm/icons/check-circle-2.mjs"
import CircleAlertIcon from "lucide-react/dist/esm/icons/circle-alert.mjs"
import GaugeIcon from "lucide-react/dist/esm/icons/gauge.mjs"

import { publicDesktopErrorMessage } from "@/app/desktop-client"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { PerformanceCharts } from "@/features/reports/performance-charts"
import { PerformanceLatencyTable } from "@/features/reports/performance-latency-table"
import { PerformanceStreamingTimingTable } from "@/features/reports/performance-streaming-timing-table"

import {
  estimateQuickPerformanceOpenLoopRequestCap,
  type QuickPerformanceArrivalPattern,
  type QuickPerformanceCommand,
  type QuickPerformanceLoadMode,
  type QuickPerformanceProgress,
  type QuickPerformanceReport,
  type QuickPerformanceWorkloadMode,
} from "./data"
import {
  performanceCapacitySummary,
  performanceCompletion,
  performanceProgressPhaseLabel,
  performanceSLOStatusLabel,
} from "./performance-summary"
import { QuickPerformanceRequestAnalysis } from "./quick-performance-request-analysis"

const MAX_PERFORMANCE_REQUESTS = 10_000

interface PerformanceForm {
  loadMode: QuickPerformanceLoadMode
  arrivalPattern: QuickPerformanceArrivalPattern
  workloadMode: QuickPerformanceWorkloadMode
  requestCount: number
  durationSeconds: number
  concurrency: number
  ratePerSecond: number
  maxInFlight: number
  timeoutSeconds: number
  inputTokens: number
  outputTokens: number
  inputTokensStdDev: number
  outputTokensStdDev: number
  sharedPrefixTokens: number
  randomSeed: number
  warmupRequests: number
  rampDurationSeconds: number
  rampRequestCap: number
  sliceDurationSeconds: number
  sloTTFTMS: number
  sloTPOTMS: number
  sloE2EMS: number
  sloTargetPercent: number
  capacityEnabled: boolean
  fixedCapacityStart: number
  fixedCapacityStep: number
  openCapacityStart: number
  openCapacityStep: number
}

type PerformanceNumberFieldName = Exclude<
  keyof PerformanceForm,
  "loadMode" | "arrivalPattern" | "workloadMode" | "capacityEnabled"
>
type PerformanceFieldErrors = Partial<Record<PerformanceNumberFieldName, string>>

function performanceTargetErrors() {
  return {
    requestCount: tx("desktop:quick-test_enter_a_request_count_greater_than_0_or_set_a"),
    durationSeconds: tx("desktop:quick-test_enter_a_duration_greater_than_0_or_set_a_request"),
  } as const
}

const DEFAULT_PERFORMANCE_FORM: PerformanceForm = {
  loadMode: "fixed_concurrency",
  arrivalPattern: "constant",
  workloadMode: "fixed",
  requestCount: 10,
  durationSeconds: 0,
  concurrency: 1,
  ratePerSecond: 1,
  maxInFlight: 256,
  timeoutSeconds: 60,
  inputTokens: 100,
  outputTokens: 100,
  inputTokensStdDev: 10,
  outputTokensStdDev: 10,
  sharedPrefixTokens: 0,
  randomSeed: 1,
  warmupRequests: 0,
  rampDurationSeconds: 0,
  rampRequestCap: 1_000,
  sliceDurationSeconds: 0,
  sloTTFTMS: 0,
  sloTPOTMS: 0,
  sloE2EMS: 0,
  sloTargetPercent: 0,
  capacityEnabled: false,
  fixedCapacityStart: 1,
  fixedCapacityStep: 1,
  openCapacityStart: 1,
  openCapacityStep: 1,
}

export type QuickPerformanceConnection = Pick<
  QuickPerformanceCommand,
  "address_mode" | "url" | "api_key" | "channel_id" | "credential_run_id" | "model_id" | "task"
>

export function QuickPerformanceSheet({
  open,
  onOpenChange,
  onCloseAutoFocus,
  connection,
  run,
  onArchived,
  onOpenReport,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCloseAutoFocus?: (event: Event) => void
  connection: QuickPerformanceConnection
  run: (
    command: QuickPerformanceCommand,
    onProgress?: (progress: QuickPerformanceProgress) => void,
  ) => Promise<QuickPerformanceReport>
  onArchived?: (reportID: string) => void | Promise<void>
  onOpenReport?: (reportID: string) => void | Promise<void>
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("quickTest")
  const [form, setForm] = useState<PerformanceForm>(DEFAULT_PERFORMANCE_FORM)
  const [pending, setPending] = useState(false)
  const [report, setReport] = useState<QuickPerformanceReport | null>(null)
  const [progress, setProgress] = useState<QuickPerformanceProgress | null>(null)
  const [fieldErrors, setFieldErrors] = useState<PerformanceFieldErrors>({})
  const [operationError, setOperationError] = useState("")
  const requestGeneration = useRef({ value: 0 })
  // This identity stays in memory only, including the temporary credential.
  const connectionIdentity = JSON.stringify(connection)
  const [outputIdentity, setOutputIdentity] = useState(connectionIdentity)
  if (outputIdentity !== connectionIdentity) {
    setOutputIdentity(connectionIdentity)
    setReport(null)
    setProgress(null)
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
  const budgetPreview = performanceRequestBudget(form)
  const capacityTargets = performanceCapacityTargets(form)

  const resetOutput = () => {
    setReport(null)
    setProgress(null)
    setOperationError("")
  }

  const update = (key: PerformanceNumberFieldName, value: number) => {
    setForm((current) => ({ ...current, [key]: value }))
    setFieldErrors((current) => {
      let next = omitFieldError(current, key)
      if (
        [
          "requestCount",
          "durationSeconds",
          "concurrency",
          "ratePerSecond",
          "warmupRequests",
          "rampDurationSeconds",
          "rampRequestCap",
          "sloTTFTMS",
          "sloTPOTMS",
          "sloE2EMS",
          "sloTargetPercent",
          "fixedCapacityStart",
          "fixedCapacityStep",
          "openCapacityStart",
          "openCapacityStep",
        ].includes(key)
      ) {
        for (const field of [
          "requestCount",
          "durationSeconds",
          "concurrency",
          "ratePerSecond",
          "warmupRequests",
          "rampDurationSeconds",
          "rampRequestCap",
          "sloTTFTMS",
          "sloTPOTMS",
          "sloE2EMS",
          "sloTargetPercent",
          "fixedCapacityStart",
          "fixedCapacityStep",
          "openCapacityStart",
          "openCapacityStep",
        ] as const) {
          next = omitFieldError(next, field)
        }
      }
      return next
    })
    resetOutput()
  }

  const updateCapacityEnabled = (capacityEnabled: boolean) => {
    setForm((current) => ({ ...current, capacityEnabled }))
    setFieldErrors({})
    resetOutput()
  }

  const updateLoadMode = (loadMode: QuickPerformanceLoadMode) => {
    setForm((current) => ({ ...current, loadMode }))
    setFieldErrors((current) => {
      const next =
        loadMode === "fixed_concurrency"
          ? omitFieldError(omitFieldError(current, "ratePerSecond"), "maxInFlight")
          : omitFieldError(omitFieldError(current, "concurrency"), "rampRequestCap")
      const withoutBudgetErrors = omitFieldError(
        omitFieldError(next, "requestCount"),
        "warmupRequests",
      )
      return loadMode === "fixed_concurrency" && form.workloadMode === "fixed"
        ? omitFieldError(withoutBudgetErrors, "randomSeed")
        : withoutBudgetErrors
    })
    resetOutput()
  }

  const updateArrivalPattern = (arrivalPattern: QuickPerformanceArrivalPattern) => {
    setForm((current) => ({ ...current, arrivalPattern }))
    setFieldErrors((current) => {
      const next = omitFieldError(current, "ratePerSecond")
      return arrivalPattern === "constant" && form.workloadMode === "fixed"
        ? omitFieldError(next, "randomSeed")
        : next
    })
    resetOutput()
  }

  const updateWorkloadMode = (workloadMode: QuickPerformanceWorkloadMode) => {
    setForm((current) => ({ ...current, workloadMode }))
    if (workloadMode === "fixed") {
      setFieldErrors((current) => {
        let next = omitFieldError(
          omitFieldError(omitFieldError(current, "inputTokensStdDev"), "outputTokensStdDev"),
          "sharedPrefixTokens",
        )
        if (form.loadMode !== "open_loop" || form.arrivalPattern !== "poisson")
          next = omitFieldError(next, "randomSeed")
        return next
      })
    }
    resetOutput()
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (pending) return
    const nextFieldErrors = validatePerformanceForm(form)
    if (Object.keys(nextFieldErrors).length > 0) {
      setFieldErrors(nextFieldErrors)
      setOperationError("")
      focusFormField(event.currentTarget, firstPerformanceErrorField(nextFieldErrors))
      return
    }
    setPending(true)
    setReport(null)
    setProgress(null)
    setFieldErrors({})
    setOperationError("")
    const generation = ++requestGeneration.current.value
    void run(
      {
        address_mode: connection.address_mode,
        ...(connection.task ? { task: connection.task } : {}),
        url: connection.url,
        api_key: connection.api_key,
        ...(connection.channel_id ? { channel_id: connection.channel_id } : {}),
        ...(connection.credential_run_id
          ? { credential_run_id: connection.credential_run_id }
          : {}),
        model_id: connection.model_id,
        load_mode: form.loadMode,
        request_count: form.requestCount,
        duration_ms: form.durationSeconds * 1_000,
        concurrency: form.loadMode === "fixed_concurrency" ? form.concurrency : 0,
        rate_per_second: form.loadMode === "open_loop" ? form.ratePerSecond : 0,
        max_in_flight: form.loadMode === "open_loop" ? form.maxInFlight : 0,
        arrival_pattern: form.loadMode === "open_loop" ? form.arrivalPattern : "constant",
        workload_mode: form.workloadMode,
        random_seed: performanceNeedsSeed(form) ? form.randomSeed : 0,
        input_tokens_stddev: form.workloadMode === "normal" ? form.inputTokensStdDev : 0,
        output_tokens_stddev: form.workloadMode === "normal" ? form.outputTokensStdDev : 0,
        shared_prefix_tokens: form.workloadMode === "normal" ? form.sharedPrefixTokens : 0,
        warmup_requests: form.warmupRequests,
        ramp_duration_ms: form.capacityEnabled ? 0 : form.rampDurationSeconds * 1_000,
        ramp_request_cap:
          !form.capacityEnabled &&
          form.loadMode === "fixed_concurrency" &&
          form.rampDurationSeconds > 0
            ? form.rampRequestCap
            : 0,
        slice_duration_ms: form.sliceDurationSeconds * 1_000,
        slo_ttft_ms: form.sloTTFTMS,
        slo_tpot_ms: form.sloTPOTMS,
        slo_e2e_ms: form.sloE2EMS,
        slo_target_percent: form.sloTargetPercent,
        capacity_enabled: form.capacityEnabled,
        capacity_start: form.capacityEnabled
          ? form.loadMode === "fixed_concurrency"
            ? form.fixedCapacityStart
            : form.openCapacityStart
          : 0,
        capacity_step: form.capacityEnabled
          ? form.loadMode === "fixed_concurrency"
            ? form.fixedCapacityStep
            : form.openCapacityStep
          : 0,
        timeout_ms: form.timeoutSeconds * 1_000,
        input_tokens: form.inputTokens,
        output_tokens: form.outputTokens,
      },
      (nextProgress) => {
        if (requestGeneration.current.value === generation) setProgress(nextProgress)
      },
    )
      .then((nextReport) => {
        if (requestGeneration.current.value === generation) setReport(nextReport)
        if (nextReport.archived && nextReport.report_id) void onArchived?.(nextReport.report_id)
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

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        onCloseAutoFocus={onCloseAutoFocus}
        className="data-[side=right]:w-full data-[side=right]:sm:max-w-5xl"
      >
        <SheetHeader>
          <SheetTitle>{t("performance.title")}</SheetTitle>
          <SheetDescription>
            {t("performance.description")}
            <span className="mt-1 block break-all">
              {connection.model_id} · {connection.url}
            </span>
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className="min-h-0 flex-1 px-4">
          <form id="quick-performance-form" onSubmit={submit} className="space-y-4 pb-4" noValidate>
            <FieldGroup>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <Field className="block min-w-0">
                  <FieldLabel htmlFor="quick-performance-loadMode">
                    {tx("desktop:catalog_load_mode")}
                  </FieldLabel>
                  <FieldContent>
                    <Select
                      value={form.loadMode}
                      disabled={pending}
                      onValueChange={(value) => updateLoadMode(value as QuickPerformanceLoadMode)}
                    >
                      <SelectTrigger
                        id="quick-performance-loadMode"
                        aria-label={tx("desktop:catalog_load_mode")}
                        className="w-full"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="fixed_concurrency">
                            {tx("desktop:catalog_fixed_concurrency")}
                          </SelectItem>
                          <SelectItem value="open_loop">
                            {tx("desktop:quick-test_open_arrival_rps")}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                {form.loadMode === "open_loop" ? (
                  <Field className="block min-w-0">
                    <FieldLabel htmlFor="quick-performance-arrivalPattern">
                      {tx("desktop:quick-test_arrival_distribution")}
                    </FieldLabel>
                    <FieldContent>
                      <Select
                        value={form.arrivalPattern}
                        disabled={pending}
                        onValueChange={(value) =>
                          updateArrivalPattern(value as QuickPerformanceArrivalPattern)
                        }
                      >
                        <SelectTrigger
                          id="quick-performance-arrivalPattern"
                          aria-label={tx("desktop:quick-test_arrival_distribution")}
                          className="w-full"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value="constant">
                              {tx("desktop:quick-test_constant_interval")}
                            </SelectItem>
                            <SelectItem value="poisson">
                              {tx("desktop:quick-test_poisson_arrivals")}
                            </SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </FieldContent>
                  </Field>
                ) : null}
                <Field className="block min-w-0">
                  <FieldLabel htmlFor="quick-performance-workloadMode">
                    {tx("desktop:quick-test_workload")}
                  </FieldLabel>
                  <FieldContent>
                    <Select
                      value={form.workloadMode}
                      disabled={pending}
                      onValueChange={(value) =>
                        updateWorkloadMode(value as QuickPerformanceWorkloadMode)
                      }
                    >
                      <SelectTrigger
                        id="quick-performance-workloadMode"
                        aria-label={tx("desktop:quick-test_workload")}
                        className="w-full"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="fixed">
                            {tx("desktop:quick-test_fixed_tokens")}
                          </SelectItem>
                          <SelectItem value="normal">
                            {tx("desktop:quick-test_normal_distribution")}
                          </SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldContent>
                </Field>
                <PerformanceNumberField
                  field="requestCount"
                  label={tx("desktop:catalog_request_count")}
                  value={form.requestCount}
                  min={0}
                  max={10_000}
                  disabled={pending}
                  error={localizeStoredMessage(fieldErrors.requestCount ?? "", tx)}
                  onChange={(value) => update("requestCount", value)}
                />
                <PerformanceNumberField
                  field="durationSeconds"
                  label={tx("desktop:quick-test_duration_seconds")}
                  value={form.durationSeconds}
                  min={0}
                  max={3_600}
                  disabled={pending}
                  error={localizeStoredMessage(fieldErrors.durationSeconds ?? "", tx)}
                  onChange={(value) => update("durationSeconds", value)}
                />
                {form.loadMode === "fixed_concurrency" ? (
                  <PerformanceNumberField
                    field="concurrency"
                    label={
                      form.capacityEnabled
                        ? tx("desktop:quick-test_final_concurrency")
                        : tx("desktop:catalog_concurrency")
                    }
                    value={form.concurrency}
                    min={1}
                    max={256}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.concurrency ?? "", tx)}
                    onChange={(value) => update("concurrency", value)}
                  />
                ) : (
                  <>
                    <PerformanceNumberField
                      field="ratePerSecond"
                      label={
                        form.capacityEnabled
                          ? tx("desktop:quick-test_final_rps")
                          : tx("desktop:quick-test_target_send_rps")
                      }
                      value={form.ratePerSecond}
                      min={0.01}
                      max={100_000}
                      step={0.01}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.ratePerSecond ?? "", tx)}
                      onChange={(value) => update("ratePerSecond", value)}
                    />
                    <PerformanceNumberField
                      field="maxInFlight"
                      label={tx("desktop:quick-test_maximum_in_flight")}
                      value={form.maxInFlight}
                      min={1}
                      max={2_000}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.maxInFlight ?? "", tx)}
                      onChange={(value) => update("maxInFlight", value)}
                    />
                  </>
                )}
                <PerformanceNumberField
                  field="timeoutSeconds"
                  label={tx("desktop:quick-test_request_timeout_seconds")}
                  value={form.timeoutSeconds}
                  min={1}
                  max={600}
                  disabled={pending}
                  error={localizeStoredMessage(fieldErrors.timeoutSeconds ?? "", tx)}
                  onChange={(value) => update("timeoutSeconds", value)}
                />
                <PerformanceNumberField
                  field="inputTokens"
                  label={
                    form.workloadMode === "normal"
                      ? tx("desktop:quick-test_approximate_mean_input_tokens")
                      : tx("desktop:quick-test_approximate_input_tokens")
                  }
                  value={form.inputTokens}
                  min={1}
                  max={1_000_000}
                  disabled={pending}
                  error={localizeStoredMessage(fieldErrors.inputTokens ?? "", tx)}
                  onChange={(value) => update("inputTokens", value)}
                />
                <PerformanceNumberField
                  field="outputTokens"
                  label={
                    form.workloadMode === "normal"
                      ? tx("desktop:quick-test_mean_maximum_output_tokens")
                      : tx("desktop:quick-test_maximum_output_tokens")
                  }
                  value={form.outputTokens}
                  min={1}
                  max={65_536}
                  disabled={pending}
                  error={localizeStoredMessage(fieldErrors.outputTokens ?? "", tx)}
                  onChange={(value) => update("outputTokens", value)}
                />
                {form.workloadMode === "normal" ? (
                  <>
                    <PerformanceNumberField
                      field="inputTokensStdDev"
                      label={tx("desktop:quick-test_input_token_standard_deviation")}
                      value={form.inputTokensStdDev}
                      min={0}
                      max={1_000_000}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.inputTokensStdDev ?? "", tx)}
                      onChange={(value) => update("inputTokensStdDev", value)}
                    />
                    <PerformanceNumberField
                      field="outputTokensStdDev"
                      label={tx("desktop:quick-test_output_token_standard_deviation")}
                      value={form.outputTokensStdDev}
                      min={0}
                      max={65_536}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.outputTokensStdDev ?? "", tx)}
                      onChange={(value) => update("outputTokensStdDev", value)}
                    />
                    <PerformanceNumberField
                      field="sharedPrefixTokens"
                      label={tx("desktop:quick-test_shared_prefix_tokens")}
                      value={form.sharedPrefixTokens}
                      min={0}
                      max={999_999}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.sharedPrefixTokens ?? "", tx)}
                      onChange={(value) => update("sharedPrefixTokens", value)}
                    />
                  </>
                ) : null}
                {performanceNeedsSeed(form) ? (
                  <PerformanceNumberField
                    field="randomSeed"
                    label={tx("desktop:quick-test_random_seed")}
                    value={form.randomSeed}
                    min={1}
                    max={4_294_967_295}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.randomSeed ?? "", tx)}
                    onChange={(value) => update("randomSeed", value)}
                  />
                ) : null}
              </div>
              <div className="rounded-lg border bg-surface-subtle p-3">
                <div className="mb-3">
                  <h3 className="text-xs font-semibold">
                    {tx("desktop:quick-test_slo_and_capacity")}
                  </h3>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {tx(
                      "desktop:quick-test_0_disables_a_threshold_good_requests_must_pass_transport_validation",
                    )}
                  </p>
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <PerformanceNumberField
                    field="sloTTFTMS"
                    label="SLO TTFT（ms）"
                    value={form.sloTTFTMS}
                    min={0}
                    max={Number.MAX_VALUE}
                    step={0.01}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.sloTTFTMS ?? "", tx)}
                    onChange={(value) => update("sloTTFTMS", value)}
                  />
                  <PerformanceNumberField
                    field="sloTPOTMS"
                    label="SLO TPOT（ms/token）"
                    value={form.sloTPOTMS}
                    min={0}
                    max={Number.MAX_VALUE}
                    step={0.01}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.sloTPOTMS ?? "", tx)}
                    onChange={(value) => update("sloTPOTMS", value)}
                  />
                  <PerformanceNumberField
                    field="sloE2EMS"
                    label="SLO E2E（ms）"
                    value={form.sloE2EMS}
                    min={0}
                    max={Number.MAX_VALUE}
                    step={0.01}
                    disabled={pending}
                    error={fieldErrors.sloE2EMS}
                    onChange={(value) => update("sloE2EMS", value)}
                  />
                  <PerformanceNumberField
                    field="sloTargetPercent"
                    label={tx("desktop:quick-test_slo_target_compliance")}
                    value={form.sloTargetPercent}
                    min={0}
                    max={100}
                    step={0.01}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.sloTargetPercent ?? "", tx)}
                    onChange={(value) => update("sloTargetPercent", value)}
                  />
                </div>
                <Field className="mt-3 gap-2">
                  <FieldLabel
                    htmlFor="quick-performance-capacityEnabled"
                    className="flex min-h-8 cursor-pointer items-center gap-2 rounded-md border bg-background px-3 py-1.5"
                  >
                    <Checkbox
                      id="quick-performance-capacityEnabled"
                      aria-label={tx("desktop:quick-test_capacity_ladder")}
                      checked={form.capacityEnabled}
                      disabled={pending}
                      onCheckedChange={(checked) => updateCapacityEnabled(checked === true)}
                    />
                    <span>{tx("desktop:quick-test_capacity_ladder")}</span>
                    <span className="ml-auto text-[10px] font-normal text-muted-foreground">
                      {tx("desktop:quick-test_run_each_step_and_stop_after_the_first_failure")}
                    </span>
                  </FieldLabel>
                </Field>
                {form.capacityEnabled ? (
                  <div className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_2fr]">
                    {form.loadMode === "fixed_concurrency" ? (
                      <>
                        <PerformanceNumberField
                          field="fixedCapacityStart"
                          label={tx("desktop:quick-test_starting_concurrency")}
                          value={form.fixedCapacityStart}
                          min={1}
                          max={256}
                          disabled={pending}
                          error={localizeStoredMessage(fieldErrors.fixedCapacityStart ?? "", tx)}
                          onChange={(value) => update("fixedCapacityStart", value)}
                        />
                        <PerformanceNumberField
                          field="fixedCapacityStep"
                          label={tx("desktop:quick-test_concurrency_step")}
                          value={form.fixedCapacityStep}
                          min={1}
                          max={Number.MAX_SAFE_INTEGER}
                          disabled={pending}
                          error={localizeStoredMessage(fieldErrors.fixedCapacityStep ?? "", tx)}
                          onChange={(value) => update("fixedCapacityStep", value)}
                        />
                      </>
                    ) : (
                      <>
                        <PerformanceNumberField
                          field="openCapacityStart"
                          label={tx("desktop:quick-test_starting_rps")}
                          value={form.openCapacityStart}
                          min={0.01}
                          max={100_000}
                          step={0.01}
                          disabled={pending}
                          error={localizeStoredMessage(fieldErrors.openCapacityStart ?? "", tx)}
                          onChange={(value) => update("openCapacityStart", value)}
                        />
                        <PerformanceNumberField
                          field="openCapacityStep"
                          label={tx("desktop:quick-test_rps_step")}
                          value={form.openCapacityStep}
                          min={0.01}
                          max={Number.MAX_VALUE}
                          step={0.01}
                          disabled={pending}
                          error={localizeStoredMessage(fieldErrors.openCapacityStep ?? "", tx)}
                          onChange={(value) => update("openCapacityStep", value)}
                        />
                      </>
                    )}
                    <div className="col-span-2 flex min-h-8 min-w-0 items-center rounded-md border bg-background px-3 text-[11px] tabular-nums text-muted-foreground sm:col-span-1">
                      <span className="truncate">
                        {capacityTargets?.length
                          ? tx("desktop:quick-test_value_value_steps", {
                              value1: capacityTargets.map(formatCapacityTarget).join(" → "),
                              value2: formatNumber(capacityTargets.length),
                            })
                          : tx("desktop:quick-test_enter_a_valid_start_step_and_final_target")}
                      </span>
                    </div>
                  </div>
                ) : null}
              </div>
              <div className="rounded-lg border bg-surface-subtle p-3">
                <div className="mb-2 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                  <h3 className="text-xs font-semibold">
                    {tx("desktop:quick-test_phases_and_sampling")}
                  </h3>
                  <p className="text-[10px] tabular-nums text-muted-foreground">
                    {form.capacityEnabled ? (
                      <>
                        {tx("desktop:quick-test_budget_warmup")}{" "}
                        {formatNumber(budgetPreview.warmupCap)} {tx("desktop:quick-test_capacity")}{" "}
                        {formatNumber(budgetPreview.measuredCap)} {tx("desktop:quick-test_total")}{" "}
                        {formatNumber(budgetPreview.totalCap)} /{" "}
                        {formatNumber(MAX_PERFORMANCE_REQUESTS)}
                      </>
                    ) : (
                      <>
                        {tx("desktop:quick-test_budget_warmup")}{" "}
                        {formatNumber(budgetPreview.warmupCap)} {tx("desktop:quick-test_ramp")}{" "}
                        {formatNumber(budgetPreview.rampCap)}{" "}
                        {tx("desktop:quick-test_steady_state")}{" "}
                        {formatNumber(budgetPreview.measuredCap)} {tx("desktop:quick-test_total")}{" "}
                        {formatNumber(budgetPreview.totalCap)} /{" "}
                        {formatNumber(MAX_PERFORMANCE_REQUESTS)}
                      </>
                    )}
                  </p>
                </div>
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                  <PerformanceNumberField
                    field="warmupRequests"
                    label={tx("desktop:quick-test_warmup_request_count")}
                    value={form.warmupRequests}
                    min={0}
                    max={10_000}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.warmupRequests ?? "", tx)}
                    onChange={(value) => update("warmupRequests", value)}
                  />
                  <PerformanceNumberField
                    field="rampDurationSeconds"
                    label={tx("desktop:quick-test_ramp_duration_seconds")}
                    value={form.rampDurationSeconds}
                    min={0}
                    max={3_600}
                    disabled={pending || form.capacityEnabled}
                    error={localizeStoredMessage(fieldErrors.rampDurationSeconds ?? "", tx)}
                    onChange={(value) => update("rampDurationSeconds", value)}
                  />
                  {!form.capacityEnabled &&
                  form.loadMode === "fixed_concurrency" &&
                  form.rampDurationSeconds > 0 ? (
                    <PerformanceNumberField
                      field="rampRequestCap"
                      label={tx("desktop:quick-test_ramp_request_cap")}
                      value={form.rampRequestCap}
                      min={1}
                      max={10_000}
                      disabled={pending}
                      error={localizeStoredMessage(fieldErrors.rampRequestCap ?? "", tx)}
                      onChange={(value) => update("rampRequestCap", value)}
                    />
                  ) : null}
                  <PerformanceNumberField
                    field="sliceDurationSeconds"
                    label={tx("desktop:quick-test_time_slice_seconds")}
                    value={form.sliceDurationSeconds}
                    min={0}
                    max={3_600}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.sliceDurationSeconds ?? "", tx)}
                    onChange={(value) => update("sliceDurationSeconds", value)}
                  />
                </div>
                <p className="mt-2 text-[10px] text-muted-foreground">
                  {tx("desktop:quick-test_0_skips_a_phase_or_disables_time_slices")}
                  {form.capacityEnabled
                    ? tx(
                        "desktop:quick-test_capacity_mode_skips_ramping_and_measures_each_step_independently",
                      )
                    : tx(
                        "desktop:quick-test_ramping_uses_10_linear_steps_steady_state_metrics_exclude_warmup",
                      )}
                </p>
              </div>
              <FieldDescription>
                {form.loadMode === "open_loop"
                  ? form.arrivalPattern === "poisson"
                    ? tx(
                        "desktop:quick-test_requests_follow_a_reproducible_poisson_arrival_process_reaching_the_in",
                      )
                    : tx(
                        "desktop:quick-test_requests_are_scheduled_independently_at_constant_intervals_for_the_target",
                      )
                  : tx(
                      "desktop:quick-test_fixed_concurrency_starts_a_replacement_when_a_request_completes_to",
                    )}
                {form.workloadMode === "normal"
                  ? tx(
                      "desktop:quick-test_a_normal_workload_uses_the_seed_to_reproduce_each_request",
                    )
                  : ""}
                {tx(
                  "desktop:quick-test_when_both_request_count_and_duration_are_set_sending_stops",
                )}{" "}
              </FieldDescription>
              {operationError ? (
                <FieldError className="rounded-md border border-destructive/25 bg-destructive-soft p-3">
                  {localizeStoredMessage(operationError, tx)}
                </FieldError>
              ) : null}
            </FieldGroup>
          </form>
          {pending && progress ? (
            <QuickPerformanceProgressPanel
              progress={progress}
              requestCount={form.requestCount}
              loadMode={form.loadMode}
            />
          ) : pending ? (
            <div className="flex min-h-36 flex-col items-center justify-center rounded-lg border border-dashed text-center">
              <Spinner className="size-5" />
              <p className="mt-3 text-sm font-medium">{t("performance.running")}</p>
              <p className="mt-1 text-xs text-muted-foreground">{t("performance.runningHint")}</p>
            </div>
          ) : report ? (
            <QuickPerformanceReportPanel report={report} onOpenReport={onOpenReport} />
          ) : null}
        </ScrollArea>
        <SheetFooter>
          <Button type="submit" form="quick-performance-form" disabled={pending}>
            {pending ? (
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
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function PerformanceNumberField({
  field,
  label,
  value,
  min,
  max,
  step = 1,
  disabled,
  error,
  onChange,
}: {
  field: PerformanceNumberFieldName
  label: string
  value: number
  min: number
  max: number
  step?: number
  disabled: boolean
  error?: string
  onChange: (value: number) => void
}) {
  const id = `quick-performance-${field}`
  const errorID = `${id}-error`
  return (
    <Field className="block min-w-0" data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <FieldContent>
        <Input
          id={id}
          aria-label={label}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorID : undefined}
          type="number"
          min={min}
          max={max}
          step={step}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(Number(event.target.value))}
          required
        />
        {error ? <FieldError id={errorID}>{localizeStoredMessage(error, tx)}</FieldError> : null}
      </FieldContent>
    </Field>
  )
}

function QuickPerformanceReportPanel({
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
      : t(`errorCode.${report.error_code ?? "request_failed"}`)
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
          {report.schema_version === 1 ? (
            <ResultValue
              label={tx("desktop:quick-test_legacy_request_throughput")}
              value={`${formatPerformanceInteger(report.metrics.request_qps)} req/s`}
              numeric
            />
          ) : null}
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
          schemaVersion={report.schema_version}
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
                    {t(`errorCode.${failure.error_code}`)}
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

function validatePerformanceForm(form: PerformanceForm): PerformanceFieldErrors {
  const errors: PerformanceFieldErrors = {}
  if (!integerInRange(form.requestCount, 0, 10_000))
    errors.requestCount = tx("desktop:quick-test_request_count_must_be_an_integer_from_0_to_10")
  if (!integerInRange(form.durationSeconds, 0, 3_600))
    errors.durationSeconds = tx("desktop:quick-test_duration_must_be_an_integer_from_0_to_3_600")
  if (!integerInRange(form.warmupRequests, 0, 10_000))
    errors.warmupRequests = tx(
      "desktop:quick-test_warmup_request_count_must_be_an_integer_from_0_to",
    )
  if (!form.capacityEnabled && !integerInRange(form.rampDurationSeconds, 0, 3_600))
    errors.rampDurationSeconds = tx(
      "desktop:quick-test_ramp_duration_must_be_an_integer_from_0_to_3",
    )
  if (!integerInRange(form.sliceDurationSeconds, 0, 3_600))
    errors.sliceDurationSeconds = tx("desktop:quick-test_time_slice_must_be_an_integer_from_0_to_3")
  if (!Number.isFinite(form.sloTTFTMS) || form.sloTTFTMS < 0)
    errors.sloTTFTMS = tx(
      "desktop:quick-test_the_slo_ttft_threshold_must_be_a_finite_number_greater",
    )
  if (!Number.isFinite(form.sloTPOTMS) || form.sloTPOTMS < 0)
    errors.sloTPOTMS = tx(
      "desktop:quick-test_the_slo_tpot_threshold_must_be_a_finite_number_greater",
    )
  if (!Number.isFinite(form.sloE2EMS) || form.sloE2EMS < 0)
    errors.sloE2EMS = tx("desktop:quick-test_the_slo_e2e_threshold_must_be_a_finite_number_greater")
  if (!finiteInRange(form.sloTargetPercent, 0, 100))
    errors.sloTargetPercent = tx("desktop:quick-test_slo_target_compliance_must_be_from_0_to_100")
  const hasSLOThreshold = form.sloTTFTMS > 0 || form.sloTPOTMS > 0 || form.sloE2EMS > 0
  if (!errors.sloTargetPercent && form.sloTargetPercent > 0 && !hasSLOThreshold) {
    errors.sloTargetPercent = tx(
      "desktop:quick-test_enable_at_least_one_latency_threshold_when_setting_target_compliance",
    )
  } else if (!errors.sloTargetPercent && hasSLOThreshold && form.sloTargetPercent <= 0) {
    errors.sloTargetPercent = tx(
      "desktop:quick-test_target_compliance_must_be_greater_than_0_when_a_latency",
    )
  }
  if (
    !errors.requestCount &&
    !errors.durationSeconds &&
    form.requestCount === 0 &&
    form.durationSeconds === 0
  ) {
    errors.requestCount = performanceTargetErrors().requestCount
    errors.durationSeconds = performanceTargetErrors().durationSeconds
  }
  if (form.loadMode === "fixed_concurrency") {
    if (!integerInRange(form.concurrency, 1, 256))
      errors.concurrency = tx("desktop:quick-test_concurrency_must_be_an_integer_from_1_to_256")
    if (
      !form.capacityEnabled &&
      form.rampDurationSeconds > 0 &&
      !integerInRange(form.rampRequestCap, 1, 10_000)
    )
      errors.rampRequestCap = tx("desktop:quick-test_ramp_request_cap_must_be_an_integer_from_1_to")
  } else {
    if (!finiteInRange(form.ratePerSecond, 0.01, 100_000))
      errors.ratePerSecond = tx("desktop:quick-test_target_send_rps_must_be_from_0_01_to_100")
    if (!integerInRange(form.maxInFlight, 1, 2_000))
      errors.maxInFlight = tx("desktop:quick-test_maximum_in_flight_must_be_an_integer_from_1_to")
  }
  if (!integerInRange(form.timeoutSeconds, 1, 600))
    errors.timeoutSeconds = tx(
      "desktop:quick-test_request_timeout_must_be_an_integer_from_1_to_600",
    )
  if (!integerInRange(form.inputTokens, 1, 1_000_000))
    errors.inputTokens = tx(
      "desktop:quick-test_approximate_input_tokens_must_be_an_integer_from_1_to",
    )
  if (!integerInRange(form.outputTokens, 1, 65_536))
    errors.outputTokens = tx(
      "desktop:quick-test_maximum_output_tokens_must_be_an_integer_from_1_to",
    )
  if (form.workloadMode === "normal") {
    if (!integerInRange(form.inputTokensStdDev, 0, 1_000_000))
      errors.inputTokensStdDev = tx(
        "desktop:quick-test_input_token_standard_deviation_must_be_an_integer_from_0",
      )
    else if (!errors.inputTokens && form.inputTokensStdDev > form.inputTokens)
      errors.inputTokensStdDev = tx(
        "desktop:quick-test_input_token_standard_deviation_cannot_exceed_the_input_mean",
      )
    if (!integerInRange(form.outputTokensStdDev, 0, 65_536))
      errors.outputTokensStdDev = tx(
        "desktop:quick-test_output_token_standard_deviation_must_be_an_integer_from_0",
      )
    else if (!errors.outputTokens && form.outputTokensStdDev > form.outputTokens)
      errors.outputTokensStdDev = tx(
        "desktop:quick-test_output_token_standard_deviation_cannot_exceed_the_output_mean",
      )
    if (!integerInRange(form.sharedPrefixTokens, 0, 999_999))
      errors.sharedPrefixTokens = tx(
        "desktop:quick-test_shared_prefix_tokens_must_be_an_integer_from_0_to",
      )
    else if (!errors.inputTokens && form.sharedPrefixTokens >= form.inputTokens)
      errors.sharedPrefixTokens = tx(
        "desktop:quick-test_shared_prefix_tokens_must_be_less_than_the_input_mean",
      )
  }
  if (performanceNeedsSeed(form) && !integerInRange(form.randomSeed, 1, 4_294_967_295)) {
    errors.randomSeed = tx("desktop:quick-test_random_seed_must_be_an_integer_from_1_to_4")
  }
  if (form.capacityEnabled) {
    if (!hasSLOThreshold || form.sloTargetPercent <= 0) {
      if (!errors.sloTargetPercent)
        errors.sloTargetPercent = tx(
          "desktop:quick-test_configure_a_valid_slo_before_enabling_a_capacity_ladder",
        )
    }
    if (form.requestCount <= 0 && !errors.requestCount)
      errors.requestCount = tx(
        "desktop:quick-test_a_capacity_ladder_requires_a_request_count_greater_than_0",
      )
    if (form.loadMode === "fixed_concurrency") {
      if (!integerInRange(form.fixedCapacityStart, 1, form.concurrency))
        errors.fixedCapacityStart = tx(
          "desktop:quick-test_starting_concurrency_must_be_a_positive_integer_no_greater_than",
        )
      if (!Number.isSafeInteger(form.fixedCapacityStep) || form.fixedCapacityStep <= 0)
        errors.fixedCapacityStep = tx(
          "desktop:quick-test_concurrency_step_must_be_a_positive_integer",
        )
    } else {
      if (!finiteInRange(form.openCapacityStart, 0.01, form.ratePerSecond))
        errors.openCapacityStart = tx(
          "desktop:quick-test_starting_rps_must_be_from_0_01_to_final_rps",
        )
      if (!Number.isFinite(form.openCapacityStep) || form.openCapacityStep <= 0)
        errors.openCapacityStep = tx(
          "desktop:quick-test_rps_step_must_be_a_finite_number_greater_than_0",
        )
    }
    const targets = performanceCapacityTargets(form)
    if (targets && targets.length > 20) {
      errors[form.loadMode === "fixed_concurrency" ? "fixedCapacityStep" : "openCapacityStep"] = tx(
        "desktop:quick-test_capacity_ladders_support_at_most_20_steps_increase_the_step",
      )
    }
  }
  const canCalculateBudget =
    !errors.requestCount &&
    !errors.durationSeconds &&
    !errors.warmupRequests &&
    (!form.capacityEnabled ||
      (!errors.fixedCapacityStart &&
        !errors.fixedCapacityStep &&
        !errors.openCapacityStart &&
        !errors.openCapacityStep &&
        (performanceCapacityTargets(form)?.length ?? 21) <= 20)) &&
    (form.capacityEnabled || (!errors.rampDurationSeconds && !errors.rampRequestCap)) &&
    (form.loadMode === "fixed_concurrency" || !errors.ratePerSecond) &&
    (form.requestCount > 0 || form.durationSeconds > 0)
  if (canCalculateBudget) {
    const budget = performanceRequestBudget(form)
    if (
      !form.capacityEnabled &&
      form.loadMode === "fixed_concurrency" &&
      form.requestCount === 0 &&
      budget.measuredCap < 1
    ) {
      const field = form.rampDurationSeconds > 0 ? "rampRequestCap" : "warmupRequests"
      errors[field] = tx("desktop:quick-test_warmup_and_ramp_use_the_entire_10_000_request_budget")
    } else if (budget.totalCap > MAX_PERFORMANCE_REQUESTS) {
      if (form.capacityEnabled) {
        errors.requestCount = tx(
          "desktop:quick-test_warmup_value_capacity_value_value_exceeding_the_total_request_budget",
          {
            value1: formatNumber(budget.warmupCap),
            value2: formatNumber(budget.measuredCap),
            value3: formatNumber(budget.totalCap),
          },
        )
      } else if (
        form.loadMode === "open_loop" &&
        form.requestCount === 0 &&
        budget.warmupCap === 0 &&
        budget.rampCap === 0
      ) {
        errors.ratePerSecond =
          form.arrivalPattern === "poisson"
            ? tx(
                "desktop:quick-test_poisson_arrivals_require_double_scheduling_headroom_the_estimated_cap_of",
                { value1: formatNumber(budget.measuredCap) },
              )
            : tx(
                "desktop:quick-test_the_current_duration_and_rps_schedule_an_estimated_value_requests",
                { value1: formatNumber(budget.measuredCap) },
              )
      } else {
        const message = tx(
          "desktop:quick-test_warmup_value_ramp_value_steady_state_value_value_exceeding_the",
          {
            value1: formatNumber(budget.warmupCap),
            value2: formatNumber(budget.rampCap),
            value3: formatNumber(budget.measuredCap),
            value4: formatNumber(budget.totalCap),
          },
        )
        if (form.requestCount > 0) errors.requestCount = message
        else if (form.loadMode === "open_loop") errors.ratePerSecond = message
        else if (form.rampDurationSeconds > 0) errors.rampRequestCap = message
        else errors.warmupRequests = message
      }
    }
  }
  return errors
}

interface PerformanceRequestBudgetPreview {
  warmupCap: number
  rampCap: number
  measuredCap: number
  totalCap: number
}

function performanceRequestBudget(form: PerformanceForm): PerformanceRequestBudgetPreview {
  const warmupCap = nonNegativeFiniteOrZero(form.warmupRequests)
  const rampDurationSeconds = form.capacityEnabled
    ? 0
    : nonNegativeFiniteOrZero(form.rampDurationSeconds)
  const rampCap =
    rampDurationSeconds === 0
      ? 0
      : form.loadMode === "fixed_concurrency"
        ? nonNegativeFiniteOrZero(form.rampRequestCap)
        : performanceOpenLoopRequestCap(
            rampDurationSeconds * nonNegativeFiniteOrZero(form.ratePerSecond) * 0.55,
            form.arrivalPattern,
          )
  const capacityTargets = performanceCapacityTargets(form)
  const measuredCap =
    form.capacityEnabled && capacityTargets
      ? nonNegativeFiniteOrZero(form.requestCount) * capacityTargets.length
      : form.requestCount > 0
        ? nonNegativeFiniteOrZero(form.requestCount)
        : form.durationSeconds <= 0
          ? 0
          : form.loadMode === "fixed_concurrency"
            ? Math.max(0, MAX_PERFORMANCE_REQUESTS - warmupCap - rampCap)
            : estimateQuickPerformanceOpenLoopRequestCap(
                nonNegativeFiniteOrZero(form.durationSeconds) * 1_000,
                nonNegativeFiniteOrZero(form.ratePerSecond),
                form.arrivalPattern,
              )
  return { warmupCap, rampCap, measuredCap, totalCap: warmupCap + rampCap + measuredCap }
}

function performanceCapacityTargets(form: PerformanceForm): number[] | undefined {
  if (!form.capacityEnabled) return undefined
  const maximum = form.loadMode === "fixed_concurrency" ? form.concurrency : form.ratePerSecond
  const start =
    form.loadMode === "fixed_concurrency" ? form.fixedCapacityStart : form.openCapacityStart
  const step =
    form.loadMode === "fixed_concurrency" ? form.fixedCapacityStep : form.openCapacityStep
  if (
    !Number.isFinite(maximum) ||
    !Number.isFinite(start) ||
    !Number.isFinite(step) ||
    maximum <= 0 ||
    start <= 0 ||
    step <= 0 ||
    start > maximum
  )
    return undefined
  if (
    form.loadMode === "fixed_concurrency" &&
    (!Number.isInteger(start) || !Number.isInteger(step))
  )
    return undefined
  if (form.loadMode === "open_loop" && start < 0.01) return undefined
  const targets: number[] = []
  for (let index = 0; index <= 20; index += 1) {
    const candidate = start + index * step
    if (!Number.isFinite(candidate) || candidate <= 0) return undefined
    if (candidate >= maximum) {
      targets.push(maximum)
      break
    }
    if (targets.length > 0 && candidate <= targets[targets.length - 1]) return undefined
    targets.push(candidate)
  }
  if (targets.length === 0 || targets[targets.length - 1] !== maximum) {
    // Preserve one extra sentinel rung so field validation can distinguish a
    // valid ladder that exceeds the supported twenty-rung UI/Core contract.
    if (targets.length === 21) return targets
    return undefined
  }
  return targets
}

function performanceOpenLoopRequestCap(
  intensity: number,
  arrivalPattern: QuickPerformanceArrivalPattern,
): number {
  return arrivalPattern === "poisson" ? Math.ceil(2 * intensity) + 1 : Math.ceil(intensity)
}

function nonNegativeFiniteOrZero(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0
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

function performanceNeedsSeed(form: PerformanceForm): boolean {
  return (
    form.workloadMode === "normal" ||
    (form.loadMode === "open_loop" && form.arrivalPattern === "poisson")
  )
}

function performanceArrivalPattern(report: QuickPerformanceReport): string {
  if (report.profile.arrival_pattern === "poisson") return tx("desktop:quick-test_poisson_arrivals")
  return report.profile.arrival_pattern === "constant"
    ? tx("desktop:quick-test_constant_interval")
    : tx("desktop:quick-test_constant_interval_legacy_report")
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
    : tx("desktop:quick-test_fixed_tokens_legacy_report")
}

function performanceSeed(report: QuickPerformanceReport): string {
  if (report.profile.random_seed === undefined) return tx("desktop:quick-test_legacy_report")
  return report.profile.random_seed > 0
    ? formatNumber(report.profile.random_seed)
    : tx("desktop:quick-test_not_used")
}

function performanceSharedPrefix(report: QuickPerformanceReport): string {
  if (report.profile.shared_prefix_tokens === undefined)
    return tx("desktop:quick-test_0_tokens_legacy_report")
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

function integerInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isInteger(value) && value >= minimum && value <= maximum
}

function finiteInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isFinite(value) && value >= minimum && value <= maximum
}

function firstPerformanceErrorField(
  errors: PerformanceFieldErrors,
): PerformanceNumberFieldName | undefined {
  return (
    [
      "requestCount",
      "durationSeconds",
      "concurrency",
      "ratePerSecond",
      "maxInFlight",
      "timeoutSeconds",
      "inputTokens",
      "outputTokens",
      "inputTokensStdDev",
      "outputTokensStdDev",
      "sharedPrefixTokens",
      "randomSeed",
      "sloTTFTMS",
      "sloTPOTMS",
      "sloE2EMS",
      "sloTargetPercent",
      "fixedCapacityStart",
      "fixedCapacityStep",
      "openCapacityStart",
      "openCapacityStep",
      "warmupRequests",
      "rampDurationSeconds",
      "rampRequestCap",
      "sliceDurationSeconds",
    ] as const
  ).find((field) => errors[field])
}

function omitFieldError<T extends object>(errors: T, field: PropertyKey): T {
  if (!(field in errors)) return errors
  const next = { ...errors }
  delete (next as Record<PropertyKey, unknown>)[field]
  return next
}

function focusFormField(form: Element, field: string | undefined) {
  if (!field) return
  const control = document.getElementById(`quick-performance-${field}`)
  if (control instanceof HTMLElement && form.contains(control)) control.focus()
}
