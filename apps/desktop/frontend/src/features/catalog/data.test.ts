import { describe, expect, it } from "vitest"

import { FIXTURE_CATALOG } from "@/features/runs/fixtures"

import { parseCatalogSnapshot } from "./data"

const PLAN_ID = "123e4567-e89b-42d3-a456-426614174001"

describe("quick Suite metadata", () => {
  const profile = { description: "Connectivity", timeout_ms: 30000, inputs: [{ key: "prompt", label: "Message", type: "text", default: "hello", bindings: [{ case_key: "T001", pointer: "/request/body/messages/0/content" }] }] }
  it("preserves a generic quick task and its editable parameters", () => {
    const payload = structuredClone(FIXTURE_CATALOG)
    Object.assign(payload.suites[0], { model_target: "", quick_test: profile })
    expect(parseCatalogSnapshot(payload).suites[0]).toMatchObject({ model_target: "", quick_test: profile })
  })
  it("rejects malformed parameter metadata and unscoped version-specific tasks", () => {
    for (const quick_test of [false, {}, { ...profile, timeout_ms: 0 }, { ...profile, inputs: [{ ...profile.inputs[0], default: 7 }] }]) {
      const payload = structuredClone(FIXTURE_CATALOG)
      Object.assign(payload.suites[0], { quick_test })
      expect(() => parseCatalogSnapshot(payload)).toThrow()
    }
    const payload = structuredClone(FIXTURE_CATALOG)
    Object.assign(payload.suites[0], { protocol: "wan-video", model_target: "", quick_test: profile })
    expect(() => parseCatalogSnapshot(payload)).toThrow()
  })
})

function targetlessCatalog(modelCount: number, channelCount: number): unknown {
  const payload = structuredClone(FIXTURE_CATALOG)
  Object.assign(payload.plans[0], {
    name: "Targetless plan",
    model_count: modelCount,
    channel_count: channelCount,
    model_ids: [],
    channel_ids: [],
  })
  return payload
}

describe("parseCatalogSnapshot targetless plans", () => {
  it("accepts zero model and channel counts when both target lists are empty", () => {
    const catalog = parseCatalogSnapshot(targetlessCatalog(0, 0))

    expect(catalog.schema_version).toBe(3)
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
  it("retains an exact historical suite revision independent of the current suite hash", () => {
    const typed = structuredClone(FIXTURE_CATALOG) as unknown as {
      suites: Array<Record<string, unknown>>
      plans: Array<{ suites: Array<Record<string, unknown>> }>
    }
    typed.suites[0].revision = 1

    const catalog = parseCatalogSnapshot(typed)

    expect(catalog.plans[0].suites[0]).toMatchObject({
      suite_id: typed.suites[0].id,
      suite_revision: 2,
      suite_key: typed.suites[0].key,
      suite_name: typed.suites[0].name,
    })
  })
})

describe("parseCatalogSnapshot legacy Plan rejection", () => {
  it("rejects the previous catalog protocol instead of projecting it", () => {
    const payload = structuredClone(FIXTURE_CATALOG) as unknown as { schema_version: number }
    payload.schema_version = 2

    expect(() => parseCatalogSnapshot(payload)).toThrow("桌面目录数据协议版本不受支持")
  })

  it("rejects a flat Plan inside the current catalog protocol", () => {
    const payload = structuredClone(FIXTURE_CATALOG) as unknown as {
      plans: Array<Record<string, unknown>>
    }
    const plan = payload.plans[0]
    const suite = (plan.suites as Array<Record<string, unknown>>)[0]
    delete plan.suites
    delete plan.suite_count
    Object.assign(plan, {
      case_count: suite.case_count,
      load_mode: suite.load_mode,
      concurrency: suite.concurrency,
      request_count: suite.request_count,
      rate_per_second: suite.rate_per_second,
      duration_ms: suite.duration_ms,
      request_timeout_ms: suite.request_timeout_ms,
      suite_id: suite.suite_id,
      suite_revision: suite.suite_revision,
      cases: suite.cases,
      sla_thresholds: suite.sla_thresholds,
    })

    expect(() => parseCatalogSnapshot(payload)).toThrow("桌面目录测试计划数据无效")
  })
})

describe("parseCatalogSnapshot ordered plan suites", () => {
  it("preserves repeated suites, their order, and independent execution settings", () => {
    const payload = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    const catalogPayload = payload as {
      suites: Array<Record<string, unknown>>
      plans: Array<Record<string, unknown>>
    }
    const suite = catalogPayload.suites[0]
    const suiteID = suite.id as string
    const suiteRevision = suite.revision as number
    const cases = structuredClone(suite.cases)
    catalogPayload.plans = [{
      id: PLAN_ID,
      revision: 1,
      name: "Repeated suite plan",
      model_count: 0,
      channel_count: 0,
      suite_count: 2,
      case_count: 8,
      model_ids: [],
      channel_ids: [],
      suites: [
        {
          entry_id: "123e4567-e89b-42d3-a456-426614174031",
          suite_id: suiteID,
          suite_revision: suiteRevision,
          suite_key: suite.key,
          suite_name: suite.name,
          protocol: suite.protocol,
          model_target: suite.model_target,
          case_count: 4,
          cases,
          quick_test: suite.quick_test,
          parameters: { prompt: "first" },
          load_mode: "fixed_concurrency",
          concurrency: 2,
          request_count: 10,
          rate_per_second: 0,
          duration_ms: 0,
          request_timeout_ms: 30_000,
          sla_thresholds: { e2e_p95_ms: 2_000 },
        },
        {
          entry_id: "123e4567-e89b-42d3-a456-426614174032",
          suite_id: suiteID,
          suite_revision: suiteRevision,
          suite_key: suite.key,
          suite_name: suite.name,
          protocol: suite.protocol,
          model_target: suite.model_target,
          case_count: 4,
          cases,
          quick_test: suite.quick_test,
          parameters: { prompt: "second" },
          load_mode: "open_loop",
          concurrency: 1,
          request_count: 20,
          rate_per_second: 5,
          duration_ms: 0,
          request_timeout_ms: 60_000,
          sla_thresholds: { e2e_p95_ms: 4_000 },
        },
      ],
    }]

    const plan = parseCatalogSnapshot(catalogPayload).plans[0]
    expect(plan.suites.map((entry) => entry.entry_id)).toEqual([
      "123e4567-e89b-42d3-a456-426614174031",
      "123e4567-e89b-42d3-a456-426614174032",
    ])
    expect(plan.suites.map((entry) => entry.parameters.prompt)).toEqual(["first", "second"])
    expect(plan.suites.map((entry) => entry.concurrency)).toEqual([2, 1])
  })

  it("rejects duplicate suite entry identities without rejecting a repeated suite", () => {
    const payload = structuredClone(FIXTURE_CATALOG) as unknown as {
      suites: Array<Record<string, unknown>>
      plans: Array<Record<string, unknown>>
    }
    const suite = payload.suites[0]
    const entry = {
      entry_id: "123e4567-e89b-42d3-a456-426614174031",
      suite_id: suite.id,
      suite_revision: suite.revision,
      suite_key: suite.key,
      suite_name: suite.name,
      protocol: suite.protocol,
      model_target: suite.model_target,
      case_count: 4,
      cases: structuredClone(suite.cases),
      parameters: {},
      load_mode: "single",
      concurrency: 1,
      request_count: 1,
      rate_per_second: 0,
      duration_ms: 0,
      request_timeout_ms: 30_000,
      sla_thresholds: { e2e_p95_ms: 3_000 },
    }
    payload.plans = [{
      id: PLAN_ID,
      revision: 1,
      name: "Duplicate entry",
      model_count: 0,
      channel_count: 0,
      suite_count: 2,
      case_count: 8,
      model_ids: [],
      channel_ids: [],
      suites: [entry, structuredClone(entry)],
    }]

    expect(() => parseCatalogSnapshot(payload)).toThrow("桌面目录测试计划成员无效")
  })
})

it("accepts saved Plan references before channel mappings are configured", () => {
  const payload = structuredClone(FIXTURE_CATALOG)
  payload.channel_models = []
  payload.channels.forEach((channel) => { channel.model_count = 0 })
  expect(parseCatalogSnapshot(payload).plans).toEqual(payload.plans)
})
