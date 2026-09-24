import { describe, expect, it } from "vitest"

import { QUICK_PERFORMANCE_PRESET_IDS, quickPerformancePresetFromProfile, quickPerformanceProfileForPreset } from "./performance-presets"

describe("quick performance presets", () => {
  it.each(QUICK_PERFORMANCE_PRESET_IDS)("recognizes %s after Go JSON serialization", (id) => {
    const profile = quickPerformanceProfileForPreset(id)
    const reordered = Object.fromEntries(Object.entries(profile).reverse()) as typeof profile

    expect(quickPerformancePresetFromProfile(reordered)).toBe(id)
  })
})
