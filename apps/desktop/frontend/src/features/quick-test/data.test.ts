import { describe, expect, it } from "vitest"

import {
  estimateQuickPerformanceOpenLoopRequestCap,
  parseQuickPerformanceReport,
  type QuickPerformanceCapacityResult,
  type QuickPerformanceCapacityRung,
  type QuickPerformanceSLOAssessment,
} from "./data"

type PhaseFourFixture = ReturnType<typeof phaseThreeReport> & {
  profile: ReturnType<typeof phaseThreeReport>["profile"] & {
    slo_ttft_ms: number
    slo_tpot_ms: number
    slo_e2e_ms: number
    slo_target_percent: number
    capacity_enabled: boolean
    capacity_start: number
    capacity_step: number
  }
  progress: ReturnType<typeof phaseThreeReport>["progress"] & {
    stopped?: boolean
    capacity_rung_number?: number
    capacity_rung_count?: number
    capacity_target?: number
  }
  slo_assessment: QuickPerformanceSLOAssessment & { provider_internal?: string }
  capacity_result: Omit<QuickPerformanceCapacityResult, "rungs"> & {
    rungs: Array<Omit<QuickPerformanceCapacityRung, "progress"> & {
      progress: ReturnType<typeof phaseThreeReport>["progress"] & {
        stopped?: boolean
        capacity_rung_number?: number
        capacity_rung_count?: number
        capacity_target?: number
      }
      provider_internal?: string
    }>
    provider_internal?: string
  }
}

describe("parseQuickPerformanceReport phase-three fields", () => {
  it("keeps a validated random-input setting in report profiles", () => {
    const raw = phaseThreeReport()
    expect(parseQuickPerformanceReport({ ...raw, profile: { ...raw.profile, random_input: true } }).profile.random_input).toBe(true)
    expect(() => parseQuickPerformanceReport({ ...raw, profile: { ...raw.profile, random_input: "yes" } })).toThrow("快速性能报告数据结构无效")
  })

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

  it("accepts the backend ramp budget at a floating-point ceiling boundary", () => {
    const raw = phaseThreeReport()
    const report = parseQuickPerformanceReport({
      ...raw,
      profile: {
        ...raw.profile,
        load_mode: "open_loop",
        concurrency: 0,
        rate_per_second: 20,
        max_in_flight: 100,
        ramp_duration_ms: 5_000,
        ramp_request_cap: 0,
      },
      request_budget: {
        ...raw.request_budget,
        ramp_cap: 55,
        total_cap: 58,
      },
      ramp: {
        ...raw.ramp,
        duration_ms: 5_000,
        steps: 10,
        target_concurrency: undefined,
        target_rate_per_second: 20,
        completed_window: true,
        traffic: {
          ...raw.ramp.traffic,
          request_cap: 55,
          capped: false,
          send_duration_ms: 5_000,
          total_duration_ms: 5_020,
        },
      },
    })

    expect(report.request_budget?.ramp_cap).toBe(55)
  })

  it("does not invent phase-three data when those windows are omitted", () => {
    const raw = phaseFiveReport()
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
          time_slices: raw.time_slices.map((slice: (typeof raw.time_slices)[number], index: number) => index === 0
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

describe("parseQuickPerformanceReport phase-four fields", () => {
  it("matches the scheduler's interval-first constant duration estimate at a floating-point boundary", () => {
    expect(estimateQuickPerformanceOpenLoopRequestCap(1, 29_000, "constant")).toBe(30)
    expect(estimateQuickPerformanceOpenLoopRequestCap(1, 29_000, "poisson")).toBe(59)
  })

  it("keeps an allow-listed SLO assessment and a capacity prefix through the selected rung", () => {
    const report = parseQuickPerformanceReport(phaseFiveCapacityReport())

    expect(report.profile).toMatchObject({
      slo_ttft_ms: 50,
      slo_tpot_ms: 7,
      slo_e2e_ms: 100,
      slo_target_percent: 90,
      capacity_enabled: true,
      capacity_start: 1,
      capacity_step: 1,
    })
    expect(report.slo_assessment).toEqual({
      status: "passed",
      thresholds: { ttft_ms: 50, tpot_ms: 7, e2e_ms: 100 },
      target_percent: 90,
      total_requests: 2,
      good_requests: 2,
      bad_requests: 0,
      good_request_percent: 100,
      goodput_qps: 0.8,
      violations: { transport: 0, ttft: 0, tpot: 0, e2e: 0 },
    })
    expect(report.capacity_result).toMatchObject({
      status: "passed",
      selected_rung_index: 1,
      highest_passing_rung_index: 1,
    })
    expect(report.capacity_result?.rungs.map(({ index, target }) => ({ index, target }))).toEqual([
      { index: 0, target: 1 },
      { index: 1, target: 2 },
    ])
    expect(report.samples[0]).not.toHaveProperty("capacity_result")
    expect(JSON.stringify(report)).not.toContain("provider_internal")
  })

  it("accepts a capacity step larger than the terminal target and still appends the exact maximum", () => {
    const raw = phaseFourReport()
    raw.profile.capacity_step = 5
    expect(parseQuickPerformanceReport(raw).capacity_result?.rungs.map((rung) => rung.target)).toEqual([1, 2])
  })

  it("preserves a near-maximum open-loop start as its own rung before the exact maximum", () => {
    const raw = phaseFourReport()
    const nearMaximum = 2 - 1e-10
    Object.assign(raw.profile, {
      load_mode: "open_loop",
      concurrency: 0,
      rate_per_second: 2,
      max_in_flight: 2,
      capacity_start: nearMaximum,
      capacity_step: 3,
    })
    raw.capacity_result.rungs = [capacityRung(raw, 0, nearMaximum), capacityRung(raw, 1, 2)]
    for (const rung of raw.capacity_result.rungs) rung.progress.capacity_rung_count = 2
    raw.progress = structuredClone(raw.capacity_result.rungs[1].progress)

    expect(parseQuickPerformanceReport(raw).capacity_result?.rungs.map((rung) => rung.target)).toEqual([nearMaximum, 2])
  })

  it("rejects an ordinary SLO report whose positive timing window is below one nanosecond", () => {
    const raw = phaseFourSLOOnlyReport()
    raw.profile.warmup_requests = 0
    raw.profile.slice_duration_ms = 0
    raw.request_budget = undefined as never
    raw.warmup = undefined as never
    raw.time_slices = undefined as never
    raw.progress.send_duration_ms = Number.MIN_VALUE
    raw.progress.drain_duration_ms = 0
    raw.progress.total_duration_ms = Number.MIN_VALUE
    raw.slo_assessment.goodput_qps = Number.MAX_VALUE

    expect(() => parseQuickPerformanceReport(raw)).toThrow("快速性能报告数据结构无效")
  })

  it("rejects a capacity rung whose positive timing window is below one nanosecond", () => {
    const raw = phaseFourReport()
    const rung = raw.capacity_result.rungs[0]
    rung.progress.send_duration_ms = Number.MIN_VALUE
    rung.progress.drain_duration_ms = 0
    rung.progress.total_duration_ms = Number.MIN_VALUE
    rung.slo_assessment.goodput_qps = Number.MAX_VALUE

    expect(() => parseQuickPerformanceReport(raw)).toThrow("快速性能报告数据结构无效")
  })

  it.each([
    ["more good requests than transport successes", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.slo_assessment.good_requests = 3
      raw.slo_assessment.bad_requests = -1
    }],
    ["a fabricated good-request percentage", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.slo_assessment.good_request_percent = 99
    }],
    ["a fabricated goodput", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.slo_assessment.goodput_qps = 80
    }],
    ["a verdict that disagrees with target attainment", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.slo_assessment.status = "failed"
    }],
    ["a violation counter that disagrees with samples", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.slo_assessment.violations.ttft = 1
    }],
  ])("rejects %s", (_name, mutate) => {
    const raw = phaseFourReport()
    mutate(raw)
    expect(() => parseQuickPerformanceReport(raw)).toThrow("快速性能报告数据结构无效")
  })

  it("treats missing configured TTFT and TPOT as SLO violations", () => {
    const raw = phaseFourSLOOnlyReport()
    Object.assign(raw.samples[0], {
      ttft_ms: 0,
      tpot_ms: 0,
      ttfb_ms: 10,
      ttft_any_ms: 0,
      ttft_visible_ms: 0,
      ttst_ms: 0,
      observed_icl_ms: 0,
      semantic_chunk_count: 0,
    })
    Object.assign(raw.metrics, {
      ttft_samples: 1, ttft_p50_ms: 40, ttft_p90_ms: 40, ttft_p95_ms: 40, ttft_p99_ms: 40, ttft_average_ms: 40,
      ttft_any_samples: 1, ttft_any_p50_ms: 40, ttft_any_p95_ms: 40, ttft_any_p99_ms: 40, ttft_any_average_ms: 40,
      ttft_visible_samples: 0, ttft_visible_p50_ms: 0, ttft_visible_p95_ms: 0, ttft_visible_p99_ms: 0, ttft_visible_average_ms: 0,
      ttst_samples: 1, ttst_p50_ms: 60, ttst_p95_ms: 60, ttst_p99_ms: 60, ttst_average_ms: 60,
      observed_icl_samples: 1, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
      semantic_chunk_count_samples: 2, semantic_chunk_count_p50: 1, semantic_chunk_count_p95: 1.9,
      semantic_chunk_count_p99: 1.98, semantic_chunk_count_average: 1,
    })
    Object.assign(raw.time_slices[0], {
      ttft_any: { count: 1, p50_ms: 40, p95_ms: 40, p99_ms: 40, average_ms: 40 },
      ttft: { count: 1, p50_ms: 40, p95_ms: 40, p99_ms: 40, average_ms: 40 },
      ttft_visible: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 },
      ttst: { count: 1, p50_ms: 60, p95_ms: 60, p99_ms: 60, average_ms: 60 },
      observed_icl: { count: 1, p50_ms: 20, p95_ms: 20, p99_ms: 20, average_ms: 20 },
      semantic_chunk_count: { count: 2, p50: 1, p95: 1.9, p99: 1.98, average: 1 },
      tpot: { count: 1, p50_ms: 4, p95_ms: 4, p99_ms: 4, average_ms: 4 },
    })
    raw.slo_assessment = {
      ...raw.slo_assessment,
      status: "failed",
      good_requests: 1,
      bad_requests: 1,
      good_request_percent: 50,
      goodput_qps: 0.4,
      violations: { transport: 0, ttft: 1, tpot: 1, e2e: 0 },
    }
    expect(parseQuickPerformanceReport(raw).slo_assessment).toMatchObject({
      status: "failed",
      good_requests: 1,
      violations: { ttft: 1, tpot: 1 },
    })
  })

  it("keeps a stopped run explicitly not evaluated even when observed good requests meet the target", () => {
    const raw = phaseFourSLOOnlyReport()
    raw.progress.stopped = true
    raw.slo_assessment.status = "not_evaluated"

    const report = parseQuickPerformanceReport(raw)
    expect(report.progress.stopped).toBe(true)
    expect(report.slo_assessment?.status).toBe("not_evaluated")
  })

  it.each([
    ["twenty-one planned rungs", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.profile.concurrency = 21
      raw.profile.capacity_step = 1
      raw.capacity_result.rungs = Array.from({ length: 21 }, (_, index) => capacityRung(raw, index, index + 1))
      raw.capacity_result.selected_rung_index = 20
      raw.capacity_result.highest_passing_rung_index = 20
    }],
    ["a skipped planned target", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.capacity_result.rungs[1].target = 1.5
    }],
    ["a rung after the first failure", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.capacity_result.rungs[0].slo_assessment.status = "failed"
    }],
    ["a selected index that is not the highest passing rung", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.capacity_result.selected_rung_index = 0
    }],
    ["top-level metrics that are not the selected rung projection", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.capacity_result.rungs[1].metrics.rpm = 999
    }],
    ["a capacity rung missing its progress context", (raw: ReturnType<typeof phaseFourReport>) => {
      delete raw.capacity_result.rungs[0].progress.capacity_rung_count
    }],
    ["top-level progress missing its capacity context", (raw: ReturnType<typeof phaseFourReport>) => {
      delete raw.progress.capacity_rung_count
    }],
    ["good requests that are not explained by any enabled latency violation", (raw: ReturnType<typeof phaseFourReport>) => {
      raw.profile.slo_target_percent = 50
      raw.slo_assessment.target_percent = 50
      for (const rung of raw.capacity_result.rungs) rung.slo_assessment.target_percent = 50
      Object.assign(raw.capacity_result.rungs[0].slo_assessment, {
        good_requests: 1,
        bad_requests: 1,
        good_request_percent: 50,
        goodput_qps: 0.4,
        violations: { transport: 0, ttft: 0, tpot: 0, e2e: 0 },
      })
    }],
  ])("rejects capacity data with %s", (_name, mutate) => {
    const raw = phaseFourReport()
    mutate(raw)
    expect(() => parseQuickPerformanceReport(raw)).toThrow("快速性能报告数据结构无效")
  })

  it("accepts twenty fixed-concurrency rungs but rejects a configuration that plans twenty-one", () => {
    const twenty = phaseFourReport()
    twenty.profile.concurrency = 20
    twenty.profile.capacity_step = 1
    twenty.request_budget.measured_cap = 40
    twenty.request_budget.total_cap = 41
    twenty.capacity_result.rungs = Array.from({ length: 20 }, (_, index) => capacityRung(twenty, index, index + 1))
    twenty.capacity_result.selected_rung_index = 19
    twenty.capacity_result.highest_passing_rung_index = 19
    twenty.progress = { ...twenty.progress, capacity_rung_number: 20, capacity_rung_count: 20, capacity_target: 20 }
    expect(parseQuickPerformanceReport(twenty).capacity_result?.rungs).toHaveLength(20)

    const twentyOne = structuredClone(twenty)
    twentyOne.profile.concurrency = 21
    twentyOne.request_budget.measured_cap = 42
    twentyOne.request_budget.total_cap = 43
    expect(() => parseQuickPerformanceReport(twentyOne)).toThrow("快速性能报告数据结构无效")
  })

  it("keeps reports without SLO or capacity objects free of empty placeholders", () => {
    const report = parseQuickPerformanceReport(phaseFiveReport())
    expect(report.slo_assessment).toBeUndefined()
    expect(report.capacity_result).toBeUndefined()
  })
})

describe("parseQuickPerformanceReport phase-five streaming telemetry", () => {
  it("keeps a schema-v3 report whose fine streaming metrics reconstruct from successful samples", () => {
    const report = parseQuickPerformanceReport(phaseFiveReport())

    expect(report.schema_version).toBe(1)
    expect(report.samples[0]).toMatchObject({
      ttfb_ms: 10,
      ttft_any_ms: 20,
      ttft_visible_ms: 30,
      ttft_ms: 20,
      ttst_ms: 40,
      observed_icl_ms: 20,
      semantic_chunk_count: 3,
    })
    expect(report.metrics).toMatchObject({
      ttft_samples: 2,
      ttfb_samples: 2,
      ttfb_p50_ms: 15,
      ttfb_p95_ms: 19.5,
      ttfb_p99_ms: 19.9,
      ttfb_average_ms: 15,
      ttft_any_samples: 2,
      ttft_any_p50_ms: 30,
      ttft_any_p95_ms: 39,
      ttft_any_p99_ms: 39.8,
      ttft_any_average_ms: 30,
      ttft_visible_samples: 1,
      ttst_samples: 2,
      observed_icl_samples: 2,
      semantic_chunk_count_samples: 2,
      semantic_chunk_count_p50: 2.5,
    })
    expect(report.time_slices?.[0]).toMatchObject({
      ttfb: { count: 2, average_ms: 15 },
      ttft_any: { count: 2, average_ms: 30 },
      ttft: { count: 2, average_ms: 30 },
      ttft_visible: { count: 1, average_ms: 30 },
      semantic_chunk_count: { count: 2, average: 2.5 },
    })
  })

  it.each([
    ["a missing mandatory sample scalar", (raw: any) => { delete raw.samples[0].ttfb_ms }],
    ["a null mandatory sample scalar", (raw: any) => { raw.samples[0].ttfb_ms = null }],
    ["a numeric-string Any TTFT", (raw: any) => { raw.samples[0].ttft_any_ms = "20" }],
    ["a boolean visible TTFT", (raw: any) => { raw.samples[0].ttft_visible_ms = false }],
    ["a null second-token milestone", (raw: any) => { raw.samples[0].ttst_ms = null }],
    ["a boolean mandatory sample scalar", (raw: any) => { raw.samples[0].observed_icl_ms = false }],
    ["a numeric-string mandatory sample scalar", (raw: any) => { raw.samples[0].semantic_chunk_count = "3" }],
    ["a request-level TTFT alias mismatch", (raw: any) => { raw.samples[0].ttft_ms = 21 }],
    ["a visible milestone before the first semantic chunk", (raw: any) => { raw.samples[0].ttft_visible_ms = 19 }],
    ["a second semantic milestone without two chunks", (raw: any) => { raw.samples[0].semantic_chunk_count = 1 }],
    ["one semantic chunk without an Any TTFT", (raw: any) => {
      Object.assign(raw.samples[0], { semantic_chunk_count: 1, ttft_any_ms: 0, ttft_ms: 0, ttft_visible_ms: 0, ttst_ms: 0, observed_icl_ms: 0 })
    }],
    ["one semantic event with distinct Any and Visible timestamps", (raw: any) => {
      Object.assign(raw.samples[0], { semantic_chunk_count: 1, ttst_ms: 0, observed_icl_ms: 0 })
    }],
    ["multiple semantic chunks without an Any TTFT", (raw: any) => {
      Object.assign(raw.samples[0], { ttft_any_ms: 0, ttft_ms: 0, ttft_visible_ms: 0, ttst_ms: 0 })
    }],
    ["multiple semantic chunks without a second-token milestone", (raw: any) => { raw.samples[0].ttst_ms = 0 }],
    ["a fabricated cohort size", (raw: any) => { raw.metrics.ttfb_samples = 1 }],
    ["a fabricated percentile", (raw: any) => { raw.metrics.ttft_any_p95_ms = 38 }],
    ["a monotonic but fabricated legacy TTFT P90", (raw: any) => { raw.metrics.ttft_p90_ms = 37 }],
    ["a fabricated average", (raw: any) => { raw.metrics.observed_icl_average_ms = 19 }],
    ["a legacy TTFT metric alias mismatch", (raw: any) => { raw.metrics.ttft_p50_ms = 29 }],
    ["a time-slice TTFT alias mismatch", (raw: any) => { raw.time_slices[0].ttft.p50_ms = 29 }],
    ["a reconstructed time-slice percentile mismatch", (raw: any) => { raw.time_slices[0].ttst.p99_ms = 58 }],
  ])("rejects schema-v3 telemetry with %s", (_name, mutate) => {
    const raw = phaseFiveReport()
    mutate(raw)
    expect(() => parseQuickPerformanceReport(raw)).toThrow("快速性能报告数据结构无效")
  })

  it("excludes failed requests with partial milestones from every aggregate cohort", () => {
    const report = parseQuickPerformanceReport(phaseFiveMixedReport())

    expect(report.metrics).toMatchObject({
      succeeded: 1,
      failed: 1,
      ttfb_samples: 1,
      ttfb_average_ms: 10,
      ttft_any_samples: 1,
      ttft_any_average_ms: 20,
      semantic_chunk_count_samples: 1,
      semantic_chunk_count_average: 3,
    })
    expect(report.samples[1]).toMatchObject({ success: false, ttfb_ms: 20, ttft_any_ms: 40 })
  })

  it("attributes streaming latency to the start slice even when completion lands in a later slice", () => {
    const raw = phaseFiveReport()
    raw.samples[1].finished_offset_ms = 1_100
    raw.samples[1].e2e_ms = 1_090
    Object.assign(raw.time_slices[0], {
      completed: 1, succeeded: 1, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5,
      e2e: { count: 2, p50_ms: 575, p95_ms: 1_038.5, p99_ms: 1_079.7, average_ms: 575 },
    })
    const emptyLatency = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 }
    raw.time_slices.splice(1, 0, {
      slice_index: 1, start_ms: 1_000, end_ms: 2_000, partial: false,
      offered: 0, launched: 0, completed: 1, succeeded: 1, failed: 0, rejected: 0,
      prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5,
      ttfb: emptyLatency, ttft_any: emptyLatency, ttft_visible: emptyLatency, ttft: emptyLatency,
      ttst: emptyLatency, observed_icl: emptyLatency,
      semantic_chunk_count: { count: 0, p50: 0, p95: 0, p99: 0, average: 0 },
      tpot: emptyLatency, e2e: emptyLatency,
    })

    expect(parseQuickPerformanceReport(raw).time_slices?.[0]).toMatchObject({
      succeeded: 1,
      semantic_chunk_count: { count: 2 },
    })
  })

  it("validates selected capacity metrics from samples and nonselected rungs by cohort algebra", () => {
    const selected = phaseFiveCapacityReport()
    selected.metrics.ttfb_average_ms = 16
    selected.capacity_result.rungs[1].metrics.ttfb_average_ms = 16
    expect(() => parseQuickPerformanceReport(selected)).toThrow("快速性能报告数据结构无效")

    const nonselected = phaseFiveCapacityReport()
    nonselected.capacity_result.rungs[0].metrics.semantic_chunk_count_samples = 1
    expect(() => parseQuickPerformanceReport(nonselected)).toThrow("快速性能报告数据结构无效")

    const impossibleP90 = phaseFiveCapacityReport()
    impossibleP90.capacity_result.rungs[0].metrics.ttft_p90_ms = 40
    impossibleP90.capacity_result.rungs[0].metrics.ttft_p95_ms = 39
    expect(() => parseQuickPerformanceReport(impossibleP90)).toThrow("快速性能报告数据结构无效")
  })

  it("rejects unsupported schema versions and documents missing current fine telemetry", () => {
    const raw = phaseThreeReport() as any
    expect(() => parseQuickPerformanceReport({ ...raw, schema_version: 2 })).toThrow("快速性能报告数据协议版本不受支持")
    expect(() => parseQuickPerformanceReport({ ...raw, schema_version: 99 })).toThrow("快速性能报告数据协议版本不受支持")
    const withoutFineTelemetry = {
      ...raw,
      samples: raw.samples.map(({ ttfb_ms: _ttfb, ttft_any_ms: _any, ttft_visible_ms: _visible, ttst_ms: _ttst, observed_icl_ms: _icl, semantic_chunk_count: _chunks, ...sample }: any) => sample),
    }
    expect(() => parseQuickPerformanceReport(withoutFineTelemetry)).toThrow("快速性能报告数据结构无效")
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
  const raw = {
    schema_version: 1 as const,
    archived: false,
    archive_status: "not_attempted",
    model_id: "gpt-test",
    success: true,
    address_mode: "base_url",
    base_url: "http://api.example.test/v1",
    endpoint: "http://api.example.test/v1/chat/completions",
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
  return withCurrentStreamingTelemetry(raw)
}

function phaseFourReport(): PhaseFourFixture {
  const raw = phaseThreeReport()
  raw.profile.ramp_duration_ms = 0
  raw.profile.ramp_request_cap = 0
  raw.ramp = undefined as never
  raw.request_budget.measured_cap = 4
  raw.request_budget.ramp_cap = 0
  raw.request_budget.total_cap = 5
  const slo: PhaseFourFixture["slo_assessment"] = {
    status: "passed",
    thresholds: { ttft_ms: 50, tpot_ms: 7, e2e_ms: 100 },
    target_percent: 90,
    total_requests: 2,
    good_requests: 2,
    bad_requests: 0,
    good_request_percent: 100,
    goodput_qps: 0.8,
    violations: { transport: 0, ttft: 0, tpot: 0, e2e: 0 },
    provider_internal: "drop me",
  }
  const result: PhaseFourFixture = {
    ...raw,
    progress: {
      ...raw.progress,
      capacity_rung_number: 2,
      capacity_rung_count: 2,
      capacity_target: 2,
    },
    profile: {
      ...raw.profile,
      slo_ttft_ms: 50,
      slo_tpot_ms: 7,
      slo_e2e_ms: 100,
      slo_target_percent: 90,
      capacity_enabled: true,
      capacity_start: 1,
      capacity_step: 1,
    },
    slo_assessment: slo,
    capacity_result: {
      status: "passed",
      selected_rung_index: 1,
      highest_passing_rung_index: 1,
      rungs: [],
      provider_internal: "drop me",
    },
  }
  result.capacity_result.rungs = [capacityRung(result, 0, 1), capacityRung(result, 1, 2)]
  return result
}

function phaseFourSLOOnlyReport() {
  const raw = phaseFourReport()
  const {
    capacity_enabled: _enabled,
    capacity_start: _start,
    capacity_step: _step,
    ...profile
  } = raw.profile
  const { capacity_result: _capacity, ...withoutCapacity } = raw
  const {
    capacity_rung_number: _rungNumber,
    capacity_rung_count: _rungCount,
    capacity_target: _capacityTarget,
    ...progress
  } = raw.progress
  return {
    ...withoutCapacity,
    profile,
    progress,
    request_budget: { ...raw.request_budget, measured_cap: 2, total_cap: 3 },
  }
}

function capacityRung(raw: PhaseFourFixture, index: number, target: number): PhaseFourFixture["capacity_result"]["rungs"][number] {
  const rungCount = raw.profile.concurrency
  return {
    index,
    target,
    success: raw.success,
    progress: {
      ...structuredClone(raw.progress),
      capacity_rung_number: index + 1,
      capacity_rung_count: rungCount,
      capacity_target: target,
    },
    metrics: structuredClone(raw.metrics),
    failures: structuredClone(raw.failures),
    slo_assessment: structuredClone(raw.slo_assessment),
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

function phaseFiveReport(): any {
  return phaseThreeReport()
}

function withCurrentStreamingTelemetry(raw: any) {
  raw.schema_version = 1
  Object.assign(raw.samples[0], {
    ttfb_ms: 10, ttft_any_ms: 20, ttft_visible_ms: 30, ttft_ms: 20,
    ttst_ms: 40, observed_icl_ms: 20, semantic_chunk_count: 3,
  })
  Object.assign(raw.samples[1], {
    ttfb_ms: 20, ttft_any_ms: 40, ttft_visible_ms: 0, ttft_ms: 40,
    ttst_ms: 60, observed_icl_ms: 20, semantic_chunk_count: 2,
  })
  Object.assign(raw.metrics, phaseFiveMetrics())
  const emptyLatency = { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, average_ms: 0 }
  const emptyCount = { count: 0, p50: 0, p95: 0, p99: 0, average: 0 }
  if (raw.time_slices) {
    Object.assign(raw.time_slices[0], {
      ttfb: { count: 2, p50_ms: 15, p95_ms: 19.5, p99_ms: 19.9, average_ms: 15 },
      ttft_any: { count: 2, p50_ms: 30, p95_ms: 39, p99_ms: 39.8, average_ms: 30 },
      ttft_visible: { count: 1, p50_ms: 30, p95_ms: 30, p99_ms: 30, average_ms: 30 },
      ttft: { count: 2, p50_ms: 30, p95_ms: 39, p99_ms: 39.8, average_ms: 30 },
      ttst: { count: 2, p50_ms: 50, p95_ms: 59, p99_ms: 59.8, average_ms: 50 },
      observed_icl: { count: 2, p50_ms: 20, p95_ms: 20, p99_ms: 20, average_ms: 20 },
      semantic_chunk_count: { count: 2, p50: 2.5, p95: 2.95, p99: 2.99, average: 2.5 },
      tpot: { count: 2, p50_ms: 4, p95_ms: 4, p99_ms: 4, average_ms: 4 },
      e2e: { count: 2, p50_ms: 70, p95_ms: 79, p99_ms: 79.8, average_ms: 70 },
    })
    Object.assign(raw.time_slices[1], {
      ttfb: emptyLatency, ttft_any: emptyLatency, ttft_visible: emptyLatency, ttft: emptyLatency,
      ttst: emptyLatency, observed_icl: emptyLatency, semantic_chunk_count: emptyCount,
      tpot: emptyLatency, e2e: emptyLatency,
    })
  }
  return raw
}

function phaseFiveMetrics() {
  return {
    ttft_samples: 2,
    ttft_p50_ms: 30, ttft_p90_ms: 38, ttft_p95_ms: 39, ttft_p99_ms: 39.8, ttft_average_ms: 30,
    ttfb_samples: 2, ttfb_p50_ms: 15, ttfb_p95_ms: 19.5, ttfb_p99_ms: 19.9, ttfb_average_ms: 15,
    ttft_any_samples: 2, ttft_any_p50_ms: 30, ttft_any_p95_ms: 39, ttft_any_p99_ms: 39.8, ttft_any_average_ms: 30,
    ttft_visible_samples: 1, ttft_visible_p50_ms: 30, ttft_visible_p95_ms: 30, ttft_visible_p99_ms: 30, ttft_visible_average_ms: 30,
    ttst_samples: 2, ttst_p50_ms: 50, ttst_p95_ms: 59, ttst_p99_ms: 59.8, ttst_average_ms: 50,
    observed_icl_samples: 2, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
    semantic_chunk_count_samples: 2, semantic_chunk_count_p50: 2.5, semantic_chunk_count_p95: 2.95,
    semantic_chunk_count_p99: 2.99, semantic_chunk_count_average: 2.5,
  }
}

function phaseFiveMixedReport(): any {
  const raw = phaseFiveReport()
  raw.profile.warmup_requests = 0
  raw.profile.ramp_duration_ms = 0
  raw.profile.ramp_request_cap = 0
  raw.profile.slice_duration_ms = 0
  delete raw.request_budget
  delete raw.warmup
  delete raw.ramp
  delete raw.time_slices
  Object.assign(raw.samples[1], { success: false, timed_out: true, error_code: "timeout" })
  Object.assign(raw.progress, { succeeded: 1, failed: 1 })
  Object.assign(raw.metrics, {
    succeeded: 1, failed: 1, timed_out: 1, success_rate_percent: 50,
    prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5, cache_rate_percent: 25,
    ttft_samples: 1,
    ttft_p50_ms: 20, ttft_p90_ms: 20, ttft_p95_ms: 20, ttft_p99_ms: 20, ttft_average_ms: 20,
    ttfb_samples: 1, ttfb_p50_ms: 10, ttfb_p95_ms: 10, ttfb_p99_ms: 10, ttfb_average_ms: 10,
    ttft_any_samples: 1, ttft_any_p50_ms: 20, ttft_any_p95_ms: 20, ttft_any_p99_ms: 20, ttft_any_average_ms: 20,
    ttft_visible_samples: 1, ttft_visible_p50_ms: 30, ttft_visible_p95_ms: 30, ttft_visible_p99_ms: 30, ttft_visible_average_ms: 30,
    ttst_samples: 1, ttst_p50_ms: 40, ttst_p95_ms: 40, ttst_p99_ms: 40, ttst_average_ms: 40,
    observed_icl_samples: 1, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
    semantic_chunk_count_samples: 1, semantic_chunk_count_p50: 3, semantic_chunk_count_p95: 3,
    semantic_chunk_count_p99: 3, semantic_chunk_count_average: 3,
  })
  raw.failures = [{ error_code: "timeout", count: 1 }]
  raw.success = false
  return raw
}

function phaseFiveCapacityReport(): any {
  const raw: any = phaseFourReport()
  raw.schema_version = 1
  raw.profile.warmup_requests = 0
  raw.profile.slice_duration_ms = 0
  delete raw.warmup
  delete raw.time_slices
  raw.request_budget.warmup_cap = 0
  raw.request_budget.total_cap = raw.request_budget.measured_cap
  Object.assign(raw.samples[0], {
    ttfb_ms: 10, ttft_any_ms: 20, ttft_visible_ms: 30, ttft_ms: 20,
    ttst_ms: 40, observed_icl_ms: 20, semantic_chunk_count: 3,
  })
  Object.assign(raw.samples[1], {
    ttfb_ms: 20, ttft_any_ms: 40, ttft_visible_ms: 0, ttft_ms: 40,
    ttst_ms: 60, observed_icl_ms: 20, semantic_chunk_count: 2,
  })
  Object.assign(raw.metrics, phaseFiveMetrics())
  for (const rung of raw.capacity_result.rungs) Object.assign(rung.metrics, phaseFiveMetrics())
  return raw
}
