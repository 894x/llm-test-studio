import { describe, expect, it } from "vitest"

import { parseQuickPerformanceReport } from "./data"

describe("parseQuickPerformanceReport phase-three fields", () => {
  it("keeps validated preparation, budget, and sparse time-slice data while dropping unknown fields", () => {
    const raw = phaseThreeReport()
    const report = parseQuickPerformanceReport(raw)

    expect(report.profile).toMatchObject({
      warmup_requests: 1,
      ramp_duration_ms: 1_000,
      ramp_request_cap: 2,
      slice_duration_ms: 1_000,
    })
    expect(report.progress.capped).toBe(false)
    expect(report.request_budget).toEqual({ limit: 10_000, warmup_cap: 1, ramp_cap: 2, measured_cap: 2, total_cap: 5 })
    expect(report.warmup).toMatchObject({ request_cap: 1, completed: 1, capped: false })
    expect(report.ramp).toMatchObject({
      shape: "linear_staircase",
      duration_ms: 1_000,
      steps: 2,
      target_concurrency: 2,
      completed_window: false,
    })
    expect(report.time_slices?.map((slice) => slice.slice_index)).toEqual([0, 2])
    expect(report.time_slices?.[1]).toMatchObject({
      slice_index: 2,
      partial: true,
      offered: 0,
      launched: 0,
      completed: 0,
      ttft: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 },
    })
    expect(JSON.stringify(report)).not.toContain("provider_internal")
  })

  it("does not invent phase-three data for an earlier schema-v2 report", () => {
    const raw = phaseThreeReport()
    const { warmup_requests: _warmup, ramp_duration_ms: _ramp, ramp_request_cap: _cap, slice_duration_ms: _slice, ...legacyProfile } = raw.profile
    const { capped: _capped, ...legacyProgress } = raw.progress
    const legacy = parseQuickPerformanceReport({
      ...raw,
      profile: legacyProfile,
      progress: legacyProgress,
      request_budget: undefined,
      warmup: undefined,
      ramp: undefined,
      time_slices: undefined,
    })

    expect(legacy.profile.warmup_requests).toBeUndefined()
    expect(legacy.progress.capped).toBeUndefined()
    expect(legacy.request_budget).toBeUndefined()
    expect(legacy.warmup).toBeUndefined()
    expect(legacy.ramp).toBeUndefined()
    expect(legacy.time_slices).toBeUndefined()
  })

  it("rejects inconsistent budgets, traffic totals, unsorted slices, and invalid latency summaries", () => {
    const cases = [
      () => ({ ...phaseThreeReport(), request_budget: { ...phaseThreeReport().request_budget, total_cap: 6 } }),
      () => ({ ...phaseThreeReport(), warmup: { ...phaseThreeReport().warmup, completed: 2 } }),
      () => ({ ...phaseThreeReport(), time_slices: [...phaseThreeReport().time_slices].reverse() }),
      () => ({ ...phaseThreeReport(), time_slices: [phaseThreeReport().time_slices[0]] }),
      () => {
        const raw = phaseThreeReport()
        const fabricatedIdleSlice = {
          ...raw.time_slices[1],
          slice_index: 1,
          start_ms: 1_000,
          end_ms: 2_000,
          partial: false,
        }
        return { ...raw, time_slices: [raw.time_slices[0], fabricatedIdleSlice, raw.time_slices[1]] }
      },
      () => {
        const raw = phaseThreeReport()
        return { ...raw, time_slices: [raw.time_slices[0], { ...raw.time_slices[1], end_ms: 2_400 }] }
      },
      () => {
        const raw = phaseThreeReport()
        return {
          ...raw,
          time_slices: raw.time_slices.map((slice, index) => index === 0
            ? { ...slice, ttft: { count: 1, p50_ms: 30, p95_ms: 20, p99_ms: 40 } }
            : slice),
        }
      },
    ]

    for (const build of cases) {
      expect(() => parseQuickPerformanceReport(build())).toThrow("快速性能报告数据结构无效")
    }
  })

  it("rejects phase-three objects without matching configuration or a complete successful-run budget", () => {
    const raw = phaseThreeReport()
    const {
      warmup_requests: _warmup,
      ramp_duration_ms: _ramp,
      ramp_request_cap: _cap,
      slice_duration_ms: _slice,
      ...legacyProfile
    } = raw.profile

    expect(() => parseQuickPerformanceReport({
      ...raw,
      profile: legacyProfile,
      request_budget: { limit: 10_000, warmup_cap: 0, ramp_cap: 0, measured_cap: 2, total_cap: 2 },
      warmup: undefined,
      ramp: undefined,
      time_slices: undefined,
    })).toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({ ...raw, request_budget: undefined })).toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({ ...raw, warmup: undefined })).toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({ ...raw, ramp: undefined })).toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({ ...raw, time_slices: undefined })).toThrow("快速性能报告数据结构无效")
  })

  it("rejects unstable traffic failures, impossible peaks, and inconsistent timing windows", () => {
    const raw = phaseThreeReport()
    expect(() => parseQuickPerformanceReport({ ...raw, warmup: { ...raw.warmup, peak_in_flight: 0 } }))
      .toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({ ...raw, warmup: { ...raw.warmup, total_duration_ms: 99 } }))
      .toThrow("快速性能报告数据结构无效")
    expect(() => parseQuickPerformanceReport({
      ...raw,
      ramp: {
        ...raw.ramp,
        traffic: {
          ...raw.ramp.traffic,
          succeeded: 0,
          failed: 2,
          failures: [
            { error_code: "timeout", count: 1 },
            { error_code: "http_error", count: 1 },
          ],
        },
      },
    })).toThrow("快速性能报告数据结构无效")
  })

  it("rejects out-of-range phase-three profile values even on an early error report", () => {
    const raw = phaseThreeReport()
    const earlyErrorReport = (profile: Partial<Record<
      "warmup_requests" | "ramp_duration_ms" | "ramp_request_cap" | "slice_duration_ms",
      number
    >>) => ({
      ...raw,
      success: false,
      profile: { ...raw.profile, ...profile },
      progress: {
        phase: "not_started",
        planned: 0,
        launched: 0,
        completed: 0,
        peak_in_flight: 0,
        succeeded: 0,
        failed: 0,
        rejected: 0,
        send_duration_ms: 0,
        drain_duration_ms: 0,
        total_duration_ms: 0,
      },
      metrics: Object.fromEntries(Object.keys(raw.metrics).map((field) => [field, 0])),
      samples: [],
      failures: [],
      request_budget: undefined,
      warmup: undefined,
      ramp: undefined,
      time_slices: undefined,
      error_code: "invalid_request",
    })

    for (const profile of [
      { warmup_requests: 10_001 },
      { ramp_duration_ms: 3_600_001 },
      { ramp_request_cap: 10_001 },
      { slice_duration_ms: 3_600_001 },
    ]) {
      expect(() => parseQuickPerformanceReport(earlyErrorReport(profile))).toThrow("快速性能报告数据结构无效")
    }
  })
})

function phaseThreeReport() {
  const traffic = {
    request_cap: 1,
    offered: 1,
    launched: 1,
    completed: 1,
    succeeded: 1,
    failed: 0,
    timed_out: 0,
    rejected: 0,
    peak_in_flight: 1,
    prompt_tokens: 20,
    completion_tokens: 32,
    cached_tokens: 5,
    send_duration_ms: 80,
    drain_duration_ms: 20,
    total_duration_ms: 100,
    failures: [],
    stopped: false,
    capped: false,
    provider_internal: "drop me",
  }
  return {
    schema_version: 2 as const,
    archived: false,
    archive_status: "not_attempted",
    model_id: "gpt-test",
    success: true,
    address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: {
      load_mode: "fixed_concurrency",
      request_count: 2,
      duration_ms: 0,
      concurrency: 2,
      arrival_pattern: "constant",
      workload_mode: "fixed",
      random_seed: 0,
      input_tokens_stddev: 0,
      output_tokens_stddev: 0,
      shared_prefix_tokens: 0,
      warmup_requests: 1,
      ramp_duration_ms: 1_000,
      ramp_request_cap: 2,
      slice_duration_ms: 1_000,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
      provider_internal: "drop me",
    },
    progress: {
      phase: "completed",
      planned: 2,
      offered: 2,
      launched: 2,
      completed: 2,
      in_flight: 0,
      peak_in_flight: 2,
      succeeded: 2,
      failed: 0,
      rejected: 0,
      capped: false,
      send_duration_ms: 2_400,
      drain_duration_ms: 100,
      total_duration_ms: 2_500,
      provider_internal: "drop me",
    },
    metrics: {
      completed: 2,
      succeeded: 2,
      failed: 0,
      timed_out: 0,
      success_rate_percent: 100,
      offered_qps: 20,
      launched_qps: 20,
      completed_qps: 16.67,
      successful_request_qps: 16.67,
      request_qps: 16.67,
      rpm: 1_000,
      input_tpm: 20_000,
      output_tpm: 32_000,
      total_tpm: 52_000,
      generation_tps: 533.33,
      ttft_p50_ms: 30,
      ttft_p90_ms: 35,
      ttft_p95_ms: 40,
      ttft_p99_ms: 45,
      ttft_average_ms: 32,
      tpot_p50_ms: 4,
      tpot_p90_ms: 5,
      tpot_p95_ms: 6,
      tpot_p99_ms: 7,
      tpot_average_ms: 4.5,
      e2e_p50_ms: 60,
      e2e_p90_ms: 70,
      e2e_p95_ms: 80,
      e2e_p99_ms: 90,
      e2e_average_ms: 65,
      schedule_lag_p50_ms: 0,
      schedule_lag_p90_ms: 1,
      schedule_lag_p95_ms: 2,
      schedule_lag_p99_ms: 3,
      schedule_lag_average_ms: 0.5,
      prompt_tokens: 40,
      completion_tokens: 64,
      cached_tokens: 10,
      cache_rate_percent: 25,
    },
    samples: [performanceSample(0, 60), performanceSample(1, 90)],
    failures: [],
    request_budget: { limit: 10_000, warmup_cap: 1, ramp_cap: 2, measured_cap: 2, total_cap: 5, provider_internal: "drop me" },
    warmup: traffic,
    ramp: {
      shape: "linear_staircase",
      duration_ms: 1_000,
      steps: 2,
      target_concurrency: 2,
      completed_window: false,
      traffic: { ...traffic, request_cap: 2, offered: 2, launched: 2, completed: 2, succeeded: 2, peak_in_flight: 2, prompt_tokens: 40, completion_tokens: 64, cached_tokens: 10, capped: true },
      provider_internal: "drop me",
    },
    time_slices: [
      { ...timeSlice(0, 0, 1_000, false, 0), offered: 2, launched: 2, completed: 2, succeeded: 2, prompt_tokens: 40, completion_tokens: 64, cached_tokens: 10 },
      {
        ...timeSlice(2, 2_000, 2_500, true, 1),
        offered: 0, launched: 0, completed: 0, succeeded: 0, failed: 0, rejected: 0,
        prompt_tokens: 0, completion_tokens: 0, cached_tokens: 0,
        ttft: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 },
        tpot: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 },
        e2e: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 },
      },
    ],
    provider_internal: "drop me",
  }
}

function performanceSample(requestIndex: number, finishedOffsetMS: number) {
  return {
    request_index: requestIndex,
    scheduled_offset_ms: requestIndex * 10,
    started_offset_ms: requestIndex * 10,
    finished_offset_ms: finishedOffsetMS,
    schedule_lag_ms: 0,
    e2e_ms: finishedOffsetMS - requestIndex * 10,
    ttft_ms: 30,
    tpot_ms: 4,
    http_status: 200,
    success: true,
    timed_out: false,
    prompt_tokens: 20,
    completion_tokens: 32,
    cached_tokens: 5,
  }
}

function timeSlice(sliceIndex: number, startMS: number, endMS: number, partial: boolean, requestIndex: number) {
  const latency = { count: 1, p50_ms: 30 + requestIndex, p95_ms: 40 + requestIndex, p99_ms: 45 + requestIndex, provider_internal: "drop me" }
  return {
    slice_index: sliceIndex,
    start_ms: startMS,
    end_ms: endMS,
    partial,
    offered: 1,
    launched: 1,
    completed: 1,
    succeeded: 1,
    failed: 0,
    rejected: 0,
    prompt_tokens: 20,
    completion_tokens: 32,
    cached_tokens: 5,
    ttft: latency,
    tpot: { ...latency, p50_ms: 4, p95_ms: 6, p99_ms: 7 },
    e2e: { ...latency, p50_ms: 60, p95_ms: 80, p99_ms: 90 },
    provider_internal: "drop me",
  }
}
