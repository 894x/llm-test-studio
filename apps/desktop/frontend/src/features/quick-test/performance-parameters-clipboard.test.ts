import { afterEach, describe, expect, it, vi } from "vitest"
import { FIXTURE_PERFORMANCE_PLANS } from "@/features/catalog/builtin-performance-plans.fixture"
import { performanceFormFromProfile, performanceProfileFromForm } from "./performance-profile"
import { parsePerformanceParameters, readPerformanceParameters, writePerformanceParameters } from "./performance-parameters-clipboard"

afterEach(() => vi.unstubAllGlobals())

const profile = performanceProfileFromForm(performanceFormFromProfile())

describe("performance parameter clipboard format", () => {
  it("accepts existing copied parameters without affinity and round trips enabled affinity", () => {
    const legacy = { ...profile, affinity_enabled: undefined }
    expect(parsePerformanceParameters(JSON.stringify(legacy)).affinityEnabled).toBe(false)
    const enabled = { ...profile, affinity_enabled: true }
    expect(performanceProfileFromForm(parsePerformanceParameters(JSON.stringify(enabled)))).toEqual(enabled)
    expect(() => parsePerformanceParameters(JSON.stringify({ ...profile, affinity_enabled: "yes" }))).toThrow()
  })
  it.each(FIXTURE_PERFORMANCE_PLANS)("round trips $name ($protocol)", plan => {
    const parameters = performanceProfileFromForm(performanceFormFromProfile(plan.performance))
    const imported = parsePerformanceParameters(JSON.stringify(parameters, null, 2))
    expect(performanceProfileFromForm(imported)).toEqual(parameters)
  })

  it.each([
    "", "not JSON", "null", "[]", "{}",
    JSON.stringify({ ...profile, api_key: "do-not-import" }),
    JSON.stringify({ ...profile, version: 0 }),
    JSON.stringify({ ...profile, load_mode: "unknown" }),
    JSON.stringify({ ...profile, request_count: 0, duration_ms: 0 }),
    JSON.stringify({ ...profile, request_count: 10_001 }),
    JSON.stringify({ ...profile, request_count: 10_000, warmup_requests: 1 }),
    JSON.stringify({ ...profile, slo_e2e_ms: 100, slo_target_percent: 0 }),
    JSON.stringify({ ...profile, concurrency: "4" }),
    JSON.stringify({ ...profile, slice_duration_ms: 5 }),
    JSON.stringify({ ...profile, random_input: undefined }),
    " ".repeat(32_769),
  ])("rejects malformed, incomplete or invalid parameter data %#", text => {
    expect(() => parsePerformanceParameters(text)).toThrow()
  })
})

describe("performance parameter clipboard access", () => {
  it("prefers Wails for native clipboard reads and writes", async () => {
    const ClipboardSetText = vi.fn(async () => true)
    const ClipboardGetText = vi.fn(async () => "parameters")
    const writeText = vi.fn(), readText = vi.fn()
    vi.stubGlobal("runtime", { ClipboardSetText, ClipboardGetText })
    vi.stubGlobal("navigator", { clipboard: { writeText, readText } })
    await writePerformanceParameters("parameters")
    expect(await readPerformanceParameters()).toBe("parameters")
    expect(ClipboardSetText).toHaveBeenCalledExactlyOnceWith("parameters")
    expect(ClipboardGetText).toHaveBeenCalledOnce()
    expect(writeText).not.toHaveBeenCalled()
    expect(readText).not.toHaveBeenCalled()
  })

  it("uses the browser clipboard in preview", async () => {
    const writeText = vi.fn(async () => undefined), readText = vi.fn(async () => "preview parameters")
    vi.stubGlobal("runtime", undefined)
    vi.stubGlobal("navigator", { clipboard: { writeText, readText } })
    await writePerformanceParameters("preview parameters")
    expect(await readPerformanceParameters()).toBe("preview parameters")
    expect(writeText).toHaveBeenCalledExactlyOnceWith("preview parameters")
    expect(readText).toHaveBeenCalledOnce()
  })

  it("surfaces native failures without retrying through the browser", async () => {
    const writeText = vi.fn(), readText = vi.fn()
    vi.stubGlobal("runtime", {
      ClipboardSetText: vi.fn(async () => false),
      ClipboardGetText: vi.fn(async () => { throw new Error("denied") }),
    })
    vi.stubGlobal("navigator", { clipboard: { writeText, readText } })
    await expect(writePerformanceParameters("parameters")).rejects.toThrow()
    await expect(readPerformanceParameters()).rejects.toThrow()
    expect(writeText).not.toHaveBeenCalled()
    expect(readText).not.toHaveBeenCalled()
  })

  it("reports unavailable browser clipboard methods", async () => {
    vi.stubGlobal("runtime", undefined)
    vi.stubGlobal("navigator", {})
    await expect(writePerformanceParameters("parameters")).rejects.toThrow()
    await expect(readPerformanceParameters()).rejects.toThrow()
  })
})
