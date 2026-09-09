import { describe, expect, it } from "vitest"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { parseCatalogSnapshot } from "./data"

describe("reference-only protocol catalog", () => {
  it("parses the current contract with unpinned references and root seed", () => {
    const parsed = parseCatalogSnapshot(FIXTURE_CATALOG)
    expect(parsed.plans[0]).toMatchObject({ protocol: "openai-chat", seed: 1 })
    expect(parsed.plans[0]).not.toHaveProperty("model_ids")
    expect(parsed.suites[0].cases[0]).toEqual({ case_id: FIXTURE_CATALOG.test_cases[0].id })
  })
  it("preserves a dangling reference for run-time diagnostics rather than rejecting the whole catalog", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.suites[0].cases[0].case_id = "10000000-0000-4000-8000-000000000099"
    catalog.plans[0].entries[0].target_id = "10000000-0000-4000-8000-000000000098"
    expect(parseCatalogSnapshot(catalog).plans[0].entries[0].target_id).toBe(catalog.plans[0].entries[0].target_id)
  })
  it("rejects old pinned members and model-bound plans without modifying input", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    Object.assign(catalog.suites[0].cases[0], { revision: 1 })
    const before = structuredClone(catalog); expect(() => parseCatalogSnapshot(catalog)).toThrow(); expect(catalog).toEqual(before)
    const plan = structuredClone(FIXTURE_CATALOG); Object.assign(plan.plans[0], { model_ids: [] }); expect(() => parseCatalogSnapshot(plan)).toThrow()
    expect(() => parseCatalogSnapshot({ ...FIXTURE_CATALOG, schema_version: 3 })).toThrow()
  })
  it("keeps repeated references distinct while rejecting duplicate entry identity", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const entry = { ...catalog.plans[0].entries[0], entry_id: "10000000-0000-4000-8000-000000000099" }
    catalog.plans[0].entries.push(entry); catalog.plans[0].entry_count = 2
    expect(parseCatalogSnapshot(catalog).plans[0].entries).toHaveLength(2)
    entry.entry_id = catalog.plans[0].entries[0].entry_id; expect(() => parseCatalogSnapshot(catalog)).toThrow()
  })
  it("rejects unsafe channel addresses and strips unrecognized credential fields", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.channels[0].base_url = "https://user:password@example.test"; expect(() => parseCatalogSnapshot(catalog)).toThrow()
    catalog.channels[0].base_url = "https://example.test"; Object.assign(catalog.channels[0], { api_key: "hidden" })
    expect(JSON.stringify(parseCatalogSnapshot(catalog))).not.toContain("hidden")
  })
})
