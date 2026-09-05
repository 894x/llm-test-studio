import { describe, expect, it } from "vitest"

import { FIXTURE_CATALOG } from "@/features/runs/fixtures"

import { parseCatalogSnapshot } from "./data"

const PLAN_ID = "123e4567-e89b-42d3-a456-426614174001"
const CASE_ID = "123e4567-e89b-42d3-a456-426614174002"

function targetlessCatalog(modelCount: number, channelCount: number): unknown {
  return {
    schema_version: 2,
    case_types: [],
    models: [],
    channels: [],
    channel_models: [],
    test_cases: [],
    suites: [],
    plans: [
      {
        id: PLAN_ID,
        revision: 1,
        name: "Targetless plan",
        model_count: modelCount,
        channel_count: channelCount,
        case_count: 1,
        load_mode: "single",
        concurrency: 1,
        request_count: 1,
        rate_per_second: 0,
        duration_ms: 0,
        request_timeout_ms: 30_000,
        model_ids: [],
        channel_ids: [],
        cases: [{ case_id: CASE_ID, revision: 1 }],
        sla_thresholds: { error_rate: 0 },
      },
    ],
  }
}

describe("parseCatalogSnapshot targetless plans", () => {
  it("accepts zero model and channel counts when both target lists are empty", () => {
    const catalog = parseCatalogSnapshot(targetlessCatalog(0, 0))

    expect(catalog.plans[0]).toMatchObject({
      model_count: 0,
      channel_count: 0,
      model_ids: [],
      channel_ids: [],
    })
  })

  it.each([
    ["negative model count", -1, 0],
    ["fractional model count", 0.5, 0],
    ["negative channel count", 0, -1],
    ["fractional channel count", 0, 0.5],
  ])("rejects %s", (_name, modelCount, channelCount) => {
    expect(() => parseCatalogSnapshot(targetlessCatalog(modelCount, channelCount))).toThrow(
      "桌面目录测试计划数据无效",
    )
  })
})

describe("parseCatalogSnapshot historical suite references", () => {
  it("accepts an exact historical suite revision independent of the current suite hash", () => {
    const payload = structuredClone(FIXTURE_CATALOG)
    payload.suites[0].revision = 1

    const catalog = parseCatalogSnapshot(payload)

    expect(catalog.plans[0]).toMatchObject({
      suite_id: payload.suites[0].id,
      suite_revision: 2,
    })
  })
})
