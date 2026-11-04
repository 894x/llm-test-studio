import { describe, expect, it } from "vitest"
import { createDesktopClient } from "@/app/desktop-client"
import { BUILTIN_CATALOG } from "./builtin-catalog.fixture"
import { parseCatalogSnapshot } from "./data"

describe("file-discovered built-in catalog", () => {
  it("uses the Core projection with closed suite and plan references", () => {
    expect(parseCatalogSnapshot(BUILTIN_CATALOG)).toEqual(BUILTIN_CATALOG)
    for (const rows of [BUILTIN_CATALOG.models, BUILTIN_CATALOG.test_cases, BUILTIN_CATALOG.suites, BUILTIN_CATALOG.plans]) {
      expect(rows.length).toBeGreaterThan(0)
      expect(new Set(rows.map(row => row.id)).size).toBe(rows.length)
    }
    const cases = new Set(BUILTIN_CATALOG.test_cases.map(row => row.id))
    const suites = new Set(BUILTIN_CATALOG.suites.map(row => row.id))
    for (const suite of BUILTIN_CATALOG.suites) {
      expect(suite.cases.every(row => cases.has(row.case_id))).toBe(true)
    }
    for (const plan of BUILTIN_CATALOG.plans) {
      for (const entry of plan.entries) {
        if (entry.target_kind === "suite") expect(suites.has(entry.target_id)).toBe(true)
        if (entry.target_kind === "case") expect(cases.has(entry.target_id)).toBe(true)
      }
    }
  })

  it("loads the same data in browser preview without fabricated user history or channels", async () => {
    const preview = createDesktopClient()
    expect(await preview.getCatalog("all")).toEqual(BUILTIN_CATALOG)
    expect((await preview.getWorkspace()).runs).toEqual([])
    expect(BUILTIN_CATALOG.channels).toEqual([])
    expect(BUILTIN_CATALOG.channel_models).toEqual([])
  })
})
