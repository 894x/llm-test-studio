import { desktopLocale, translateDesktop as tx } from "@/i18n/runtime"
import { estimateQuickPerformanceOpenLoopRampRequestCap, estimateQuickPerformanceOpenLoopRequestCap,
  type QuickPerformanceProfile, type QuickPerformanceLoadMode, type QuickPerformanceArrivalPattern,
  type QuickPerformanceWorkloadMode } from "./data"

export const DEFAULT_PERFORMANCE_PROFILE: QuickPerformanceProfile = {
  load_mode: "fixed_concurrency", arrival_pattern: "constant", workload_mode: "fixed",
  request_count: 10, duration_ms: 0, concurrency: 1, timeout_ms: 60_000,
  input_tokens: 100, output_tokens: 100,
}

export const MAX_PERFORMANCE_REQUESTS = 10_000

export interface PerformanceForm {
  loadMode: QuickPerformanceLoadMode
  arrivalPattern: QuickPerformanceArrivalPattern
  workloadMode: QuickPerformanceWorkloadMode
  randomInput: boolean
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

export type PerformanceNumberFieldName = Exclude<
  keyof PerformanceForm,
  "loadMode" | "arrivalPattern" | "workloadMode" | "randomInput" | "capacityEnabled"
>
export type PerformanceFieldErrors = Partial<Record<PerformanceNumberFieldName, string>>

export const PERFORMANCE_BUDGET_FIELDS: readonly PerformanceNumberFieldName[] = [
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
]

function performanceTargetErrors() {
  return {
    requestCount: tx("desktop:quick-test_enter_a_request_count_greater_than_0_or_set_a"),
    durationSeconds: tx("desktop:quick-test_enter_a_duration_greater_than_0_or_set_a_request"),
  } as const
}

export function performanceFormFromProfile(profile?: QuickPerformanceProfile): PerformanceForm {
  const source = profile ?? DEFAULT_PERFORMANCE_PROFILE
  return {
    loadMode: source.load_mode ?? "fixed_concurrency",
    arrivalPattern: source.arrival_pattern ?? "constant",
    workloadMode: source.workload_mode ?? "fixed",
    randomInput: source.random_input ?? false,
    requestCount: source.request_count,
    durationSeconds: source.duration_ms / 1_000,
    concurrency: source.concurrency,
    ratePerSecond: source.rate_per_second ?? 1,
    maxInFlight: source.max_in_flight ?? 256,
    timeoutSeconds: source.timeout_ms / 1_000,
    inputTokens: source.input_tokens,
    outputTokens: source.output_tokens,
    inputTokensStdDev: source.input_tokens_stddev ?? 0,
    outputTokensStdDev: source.output_tokens_stddev ?? 0,
    sharedPrefixTokens: source.shared_prefix_tokens ?? 0,
    randomSeed: source.random_seed ?? 1,
    warmupRequests: source.warmup_requests ?? 0,
    rampDurationSeconds: (source.ramp_duration_ms ?? 0) / 1_000,
    rampRequestCap: source.ramp_request_cap ?? 1_000,
    sliceDurationSeconds: (source.slice_duration_ms ?? 0) / 1_000,
    sloTTFTMS: source.slo_ttft_ms ?? 0,
    sloTPOTMS: source.slo_tpot_ms ?? 0,
    sloE2EMS: source.slo_e2e_ms ?? 0,
    sloTargetPercent: source.slo_target_percent ?? 0,
    capacityEnabled: source.capacity_enabled ?? false,
    fixedCapacityStart: source.load_mode === "fixed_concurrency" ? source.capacity_start ?? 1 : 1,
    fixedCapacityStep: source.load_mode === "fixed_concurrency" ? source.capacity_step ?? 1 : 1,
    openCapacityStart: source.load_mode === "open_loop" ? source.capacity_start ?? 1 : 1,
    openCapacityStep: source.load_mode === "open_loop" ? source.capacity_step ?? 1 : 1,
  }
}

export function performanceProfileFromForm(form: PerformanceForm): Required<QuickPerformanceProfile> {
  const fixedConcurrency = form.loadMode === "fixed_concurrency"
  const openLoop = form.loadMode === "open_loop"
  const normalWorkload = form.workloadMode === "normal"
  const capacityStart = fixedConcurrency ? form.fixedCapacityStart : form.openCapacityStart
  const capacityStep = fixedConcurrency ? form.fixedCapacityStep : form.openCapacityStep
  return {
    load_mode: form.loadMode,
    request_count: form.requestCount,
    duration_ms: form.durationSeconds * 1_000,
    concurrency: fixedConcurrency ? form.concurrency : 0,
    rate_per_second: openLoop ? form.ratePerSecond : 0,
    max_in_flight: openLoop ? form.maxInFlight : 0,
    arrival_pattern: openLoop ? form.arrivalPattern : "constant",
    workload_mode: form.workloadMode,
    random_input: form.randomInput,
    random_seed: performanceNeedsSeed(form) ? form.randomSeed : 0,
    input_tokens_stddev: normalWorkload ? form.inputTokensStdDev : 0,
    output_tokens_stddev: normalWorkload ? form.outputTokensStdDev : 0,
    shared_prefix_tokens: normalWorkload ? form.sharedPrefixTokens : 0,
    warmup_requests: form.warmupRequests,
    ramp_duration_ms: form.capacityEnabled ? 0 : form.rampDurationSeconds * 1_000,
    ramp_request_cap:
      !form.capacityEnabled && fixedConcurrency && form.rampDurationSeconds > 0
        ? form.rampRequestCap
        : 0,
    slice_duration_ms: form.sliceDurationSeconds * 1_000,
    slo_ttft_ms: form.sloTTFTMS,
    slo_tpot_ms: form.sloTPOTMS,
    slo_e2e_ms: form.sloE2EMS,
    slo_target_percent: form.sloTargetPercent,
    capacity_enabled: form.capacityEnabled,
    capacity_start: form.capacityEnabled ? capacityStart : 0,
    capacity_step: form.capacityEnabled ? capacityStep : 0,
    timeout_ms: form.timeoutSeconds * 1_000,
    input_tokens: form.inputTokens,
    output_tokens: form.outputTokens,
  }
}

export function validatePerformanceForm(form: PerformanceForm): PerformanceFieldErrors {
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

export function performanceRequestBudget(form: PerformanceForm): PerformanceRequestBudgetPreview {
  const warmupCap = nonNegativeFiniteOrZero(form.warmupRequests)
  const rampDurationSeconds = form.capacityEnabled
    ? 0
    : nonNegativeFiniteOrZero(form.rampDurationSeconds)
  const rampCap =
    rampDurationSeconds === 0
      ? 0
      : form.loadMode === "fixed_concurrency"
        ? nonNegativeFiniteOrZero(form.rampRequestCap)
        : estimateQuickPerformanceOpenLoopRampRequestCap(
            rampDurationSeconds * 1_000,
            nonNegativeFiniteOrZero(form.ratePerSecond),
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

export function performanceCapacityTargets(form: PerformanceForm): number[] | undefined {
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

function nonNegativeFiniteOrZero(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0
}

export function formatNumber(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 1 }).format(value)
}

export function formatCapacityTarget(value: number): string {
  return new Intl.NumberFormat(desktopLocale(), { maximumFractionDigits: 10 }).format(value)
}

export function performanceNeedsSeed(form: PerformanceForm): boolean {
  return (
    form.workloadMode === "normal" ||
    (form.loadMode === "open_loop" && form.arrivalPattern === "poisson")
  )
}

function integerInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isInteger(value) && value >= minimum && value <= maximum
}

function finiteInRange(value: number, minimum: number, maximum: number): boolean {
  return Number.isFinite(value) && value >= minimum && value <= maximum
}

export function firstPerformanceErrorField(
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

export function omitFieldError<T extends object>(errors: T, field: PropertyKey): T {
  if (!(field in errors)) return errors
  const next = { ...errors }
  delete (next as Record<PropertyKey, unknown>)[field]
  return next
}

export function performanceProfilesEqual(left: QuickPerformanceProfile, right: QuickPerformanceProfile): boolean {
  const a = performanceProfileFromForm(performanceFormFromProfile(left))
  const b = performanceProfileFromForm(performanceFormFromProfile(right))
  return Object.entries(a).every(([key, value]) => b[key as keyof QuickPerformanceProfile] === value)
}
