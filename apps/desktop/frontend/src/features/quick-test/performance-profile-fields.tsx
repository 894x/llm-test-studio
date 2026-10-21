import { useTranslation } from "react-i18next"
import { localizeStoredMessage, translateDesktop as tx } from "@/i18n/runtime"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldContent, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { SearchableSelect } from "@/components/ui/searchable-select"
import type { QuickPerformanceLoadMode, QuickPerformanceArrivalPattern, QuickPerformanceWorkloadMode } from "./data"
import type { usePerformanceProfileEditor } from "./use-performance-profile-editor"
import { PerformanceParameterActions } from "./performance-parameter-actions"
import { MAX_PERFORMANCE_REQUESTS, PERFORMANCE_BUDGET_FIELDS,
  omitFieldError, performanceRequestBudget, performanceCapacityTargets, performanceNeedsSeed,
  formatNumber, formatCapacityTarget, type PerformanceNumberFieldName } from "./performance-profile"

export function PerformanceProfileFields({ editor, pending, actionsContainer }: {
  editor: ReturnType<typeof usePerformanceProfileEditor>; pending: boolean; actionsContainer?: HTMLElement | null
}) {
  const { t: tx } = useTranslation()
  const { t } = useTranslation("quickTest")
  const { form, setForm, fieldErrors, setFieldErrors } = editor
  const budgetPreview = performanceRequestBudget(form)
  const capacityTargets = performanceCapacityTargets(form)
  const update = (key: PerformanceNumberFieldName, value: number) => {
    setForm((current) => ({ ...current, [key]: value }))
    setFieldErrors((current) => {
      let next = omitFieldError(current, key)
      if (PERFORMANCE_BUDGET_FIELDS.includes(key)) {
        for (const field of PERFORMANCE_BUDGET_FIELDS) {
          next = omitFieldError(next, field)
        }
      }
      return next
    })
  }

  const updateCapacityEnabled = (capacityEnabled: boolean) => {
    setForm((current) => ({ ...current, capacityEnabled }))
    setFieldErrors({})
  }

  const updateRandomInput = (randomInput: boolean) => {
    setForm((current) => ({ ...current, randomInput }))
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
  }

  const updateArrivalPattern = (arrivalPattern: QuickPerformanceArrivalPattern) => {
    setForm((current) => ({ ...current, arrivalPattern }))
    setFieldErrors((current) => {
      const next = omitFieldError(current, "ratePerSecond")
      return arrivalPattern === "constant" && form.workloadMode === "fixed"
        ? omitFieldError(next, "randomSeed")
        : next
    })
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
  }

  return <div className="min-w-0 space-y-4">
    <PerformanceParameterActions editor={editor} disabled={pending} container={actionsContainer} />
              <section aria-labelledby="quick-performance-load-heading" className="grid grid-cols-2 gap-3 rounded-lg border bg-surface-subtle p-3">
                <h3 id="quick-performance-load-heading" className="col-span-full text-xs font-semibold">{t("performance.loadAndRouting")}</h3>
                <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                    <FieldLabel htmlFor="quick-performance-loadMode">{tx("desktop:catalog_load_mode")}</FieldLabel>
                    <FieldDescription id="quick-performance-loadMode-hint" className="mt-0 text-[11px]">{t(form.loadMode === "open_loop" ? "workspace.openLoopHint" : "workspace.concurrencyHint")}</FieldDescription>
                  </div>
                  <FieldContent>
                    <SearchableSelect value={form.loadMode} disabled={pending} onValueChange={(value) => updateLoadMode(value as QuickPerformanceLoadMode)} id="quick-performance-loadMode" aria-describedby="quick-performance-loadMode-hint" aria-label={tx("desktop:catalog_load_mode")} className="w-full" options={[({value: "fixed_concurrency", label: tx("desktop:catalog_fixed_concurrency")}), ({value: "open_loop", label: tx("desktop:quick-test_open_arrival_rps")})]} />
                  </FieldContent>
                </Field>
                {form.loadMode === "open_loop" ? (
                  <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                    <FieldLabel htmlFor="quick-performance-arrivalPattern">
                      {tx("desktop:quick-test_arrival_distribution")}
                    </FieldLabel>
                    <FieldContent>
                      <SearchableSelect value={form.arrivalPattern} disabled={pending} onValueChange={(value) =>
                          updateArrivalPattern(value as QuickPerformanceArrivalPattern)
                        } id="quick-performance-arrivalPattern" aria-label={tx("desktop:quick-test_arrival_distribution")} className="w-full" options={[({value: "constant", label: tx("desktop:quick-test_constant_interval")}), ({value: "poisson", label: tx("desktop:quick-test_poisson_arrivals")})]} />
                    </FieldContent>
                  </Field>
                ) : null}
                <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                  <FieldLabel htmlFor="quick-performance-workloadMode">
                    {tx("desktop:quick-test_workload")}
                  </FieldLabel>
                  <FieldContent>
                    <SearchableSelect value={form.workloadMode} disabled={pending} onValueChange={(value) =>
                        updateWorkloadMode(value as QuickPerformanceWorkloadMode)
                      } id="quick-performance-workloadMode" aria-label={tx("desktop:quick-test_workload")} className="w-full" options={[({value: "fixed", label: tx("desktop:quick-test_fixed_tokens")}), ({value: "normal", label: tx("desktop:quick-test_normal_distribution")})]} />
                  </FieldContent>
                </Field>
                <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                    <FieldLabel htmlFor="quick-performance-randomInput">{tx("desktop:quick-test_random_input")}</FieldLabel>
                    <FieldDescription id="quick-performance-randomInput-hint" className="mt-0 text-[11px]">{t("workspace.randomInputHint")}</FieldDescription>
                  </div>
                  <FieldLabel htmlFor="quick-performance-randomInput" className="flex h-8 cursor-pointer items-center gap-2 rounded-md border border-input bg-surface-control px-2.5">
                    <Checkbox id="quick-performance-randomInput" aria-label={tx("desktop:quick-test_random_input")} aria-describedby="quick-performance-randomInput-hint" checked={form.randomInput} disabled={pending} onCheckedChange={checked => updateRandomInput(checked === true)} />
                    <span aria-hidden="true">{t("workspace.enable")}</span>
                  </FieldLabel>
                </Field>
                <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                    <FieldLabel htmlFor="quick-performance-affinityEnabled">{t("performance.affinity")}</FieldLabel>
                    <FieldDescription id="quick-performance-affinityEnabled-hint" className="mt-0 text-[11px]">{t("performance.affinityHint")}</FieldDescription>
                  </div>
                  <FieldLabel htmlFor="quick-performance-affinityEnabled" className="flex h-8 cursor-pointer items-center gap-2 rounded-md border border-input bg-surface-control px-2.5">
                    <Checkbox id="quick-performance-affinityEnabled" aria-label={t("performance.affinity")} aria-describedby="quick-performance-affinityEnabled-hint" checked={form.affinityEnabled} disabled={pending} onCheckedChange={checked => setForm(current => ({ ...current, affinityEnabled: checked === true }))} />
                    <span aria-hidden="true">{t("workspace.enable")}</span>
                  </FieldLabel>
                </Field>
              </section>
              <section aria-labelledby="quick-performance-request-heading" className="grid grid-cols-2 gap-3 rounded-lg border bg-surface-subtle p-3 sm:grid-cols-3">
                <h3 id="quick-performance-request-heading" className="col-span-full text-xs font-semibold">{t("performance.requestParameters")}</h3>
                <PerformanceNumberField
                  field="requestCount"
                  hint={t("workspace.targetHint")}
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
                  hint={t("workspace.zeroHint")}
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
                  hint={t("workspace.outputHint")}
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
              </section>
              <div className="rounded-lg border bg-surface-subtle p-3">
                <div className="mb-3 flex flex-wrap items-baseline gap-x-2 gap-y-1">
                  <h3 className="text-xs font-semibold">
                    {tx("desktop:quick-test_slo_and_capacity")}
                  </h3>
                  <p className="text-[11px] text-muted-foreground">{t("workspace.sloHint")}</p>
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
                    <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2">
                      <FieldLabel htmlFor="quick-performance-capacityPreview">{t("workspace.capacityPreview")}</FieldLabel>
                      <Input id="quick-performance-capacityPreview" readOnly value={capacityTargets?.length
                        ? tx("desktop:quick-test_value_value_steps", { value1: capacityTargets.map(formatCapacityTarget).join(" → "), value2: formatNumber(capacityTargets.length) })
                        : tx("desktop:quick-test_enter_a_valid_start_step_and_final_target")} />
                    </Field>
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
                <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                  <PerformanceNumberField
                    field="warmupRequests"
                  hint={t("workspace.warmupHint")}
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
                  hint={t("workspace.rampHint")}
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
                  hint={t("workspace.sliceHint")}
                    label={tx("desktop:quick-test_time_slice_seconds")}
                    value={form.sliceDurationSeconds}
                    min={0}
                    max={3_600}
                    disabled={pending}
                    error={localizeStoredMessage(fieldErrors.sliceDurationSeconds ?? "", tx)}
                    onChange={(value) => update("sliceDurationSeconds", value)}
                  />
                </div>
              </div>
  </div>
}

function PerformanceNumberField({
  field,
  label,
  hint,
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
  hint?: string
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
    <Field className="row-span-2 grid min-w-0 grid-rows-subgrid gap-2" data-invalid={error ? true : undefined}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {hint ? <FieldDescription id={`${id}-hint`} className="mt-0 text-[11px]">{hint}</FieldDescription> : null}
        {error ? <FieldError id={errorID} className="min-w-0 text-[11px] font-normal">{localizeStoredMessage(error, tx)}</FieldError> : null}
      </div>
      <FieldContent>
        <Input
          id={id}
          aria-label={label}
          aria-invalid={error ? true : undefined}
          aria-describedby={[...(hint ? [`${id}-hint`] : []), ...(error ? [errorID] : [])].join(" ") || undefined}
          type="number"
          min={min}
          max={max}
          step={step}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(Number(event.target.value))}
          required
        />
      </FieldContent>
    </Field>
  )
}
