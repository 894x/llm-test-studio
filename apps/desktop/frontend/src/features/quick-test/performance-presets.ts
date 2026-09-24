import type { QuickPerformanceProfile } from "./data"

export type QuickPerformancePresetID = "smoke" | "baseline" | "sustained" | "capacity"

export const QUICK_PERFORMANCE_PRESET_IDS: QuickPerformancePresetID[] = [
  "smoke",
  "baseline",
  "sustained",
  "capacity",
]

const PRESETS: Record<QuickPerformancePresetID, QuickPerformanceProfile> = {
  smoke: {
    load_mode: "fixed_concurrency", arrival_pattern: "constant", workload_mode: "fixed",
    request_count: 10, duration_ms: 0, concurrency: 1, rate_per_second: 1, max_in_flight: 256,
    timeout_ms: 60_000, input_tokens: 100, output_tokens: 100, random_seed: 1,
    warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 1_000, slice_duration_ms: 0,
    slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
    capacity_enabled: false, capacity_start: 1, capacity_step: 1,
  },
  baseline: {
    load_mode: "fixed_concurrency", arrival_pattern: "constant", workload_mode: "fixed",
    request_count: 100, duration_ms: 0, concurrency: 4, rate_per_second: 1, max_in_flight: 256,
    timeout_ms: 60_000, input_tokens: 256, output_tokens: 128, random_seed: 1,
    warmup_requests: 10, ramp_duration_ms: 0, ramp_request_cap: 1_000, slice_duration_ms: 5,
    slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
    capacity_enabled: false, capacity_start: 1, capacity_step: 1,
  },
  sustained: {
    load_mode: "open_loop", arrival_pattern: "constant", workload_mode: "fixed",
    request_count: 0, duration_ms: 60_000, concurrency: 1, rate_per_second: 2, max_in_flight: 64,
    timeout_ms: 60_000, input_tokens: 256, output_tokens: 128, random_seed: 1,
    warmup_requests: 10, ramp_duration_ms: 0, ramp_request_cap: 1_000, slice_duration_ms: 5,
    slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
    capacity_enabled: false, capacity_start: 1, capacity_step: 1,
  },
  capacity: {
    load_mode: "fixed_concurrency", arrival_pattern: "constant", workload_mode: "fixed",
    request_count: 20, duration_ms: 0, concurrency: 8, rate_per_second: 1, max_in_flight: 256,
    timeout_ms: 60_000, input_tokens: 256, output_tokens: 128, random_seed: 1,
    warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 1_000, slice_duration_ms: 5,
    slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 2_000, slo_target_percent: 95,
    capacity_enabled: true, capacity_start: 1, capacity_step: 1,
  },
}

export function quickPerformanceProfileForPreset(id: QuickPerformancePresetID): QuickPerformanceProfile {
  return structuredClone(PRESETS[id])
}

export function quickPerformancePresetFromProfile(profile: QuickPerformanceProfile): QuickPerformancePresetID {
  const match = QUICK_PERFORMANCE_PRESET_IDS.find((id) =>
    Object.entries(PRESETS[id]).every(([key, value]) =>
      profile[key as keyof QuickPerformanceProfile] === value,
    ),
  )
  return match ?? "smoke"
}
