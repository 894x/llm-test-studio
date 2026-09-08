import { describe, expect, it } from "vitest"

import { FIXTURE_CATALOG } from "./fixtures"
import { eligibleRuntimeChannels, eligibleRuntimeModels } from "./run-targets"

describe("runtime plan target selection", () => {
  it("uses a plan's allowlists when they are configured", () => {
		const catalog = structuredClone(FIXTURE_CATALOG)
		const plan = catalog.plans[0]
		catalog.channels.find((channel) => channel.id === plan.channel_ids[0])!.enabled = false
		catalog.channel_models = catalog.channel_models.filter((mapping) => mapping.channel_id !== plan.channel_ids[0])
		const models = eligibleRuntimeModels(catalog, plan.id)
    expect(models.map((model) => model.id)).toEqual(plan.model_ids)
		expect(eligibleRuntimeChannels(catalog, plan.id, models[0].id).map((channel) => channel.id))
      .toEqual(plan.channel_ids)
  })

  it("offers every enabled, credentialed and mapped compatible target for a targetless plan", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    plan.suites.forEach((suite) => { suite.model_target = "" })
    plan.model_ids = []
    plan.channel_ids = []
    plan.model_count = 0
    plan.channel_count = 0

    const models = eligibleRuntimeModels(catalog, plan.id)
    expect(models.map((model) => model.name)).toEqual([
      "gpt-5.2",
      "qwen3-max",
      "claude-sonnet-4",
      "deepseek-v3.2",
      "gpt-4.1-mini",
    ])
    expect(models.map((model) => model.name)).not.toContain("gemini-2.5-pro")
  })

  it("derives the target protocol from every ordered suite entry", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    plan.suites[0].model_target = ""
    plan.suites.push({
      ...structuredClone(plan.suites[0]),
      entry_id: "99999999-9999-4999-8999-999999999991",
      parameters: { prompt: "second" },
    })
    plan.suite_count = 2
    plan.case_count += plan.suites[1].case_count
    plan.model_ids = []
    plan.channel_ids = []
    plan.model_count = 0
    plan.channel_count = 0
    expect(eligibleRuntimeModels(catalog, plan.id).length).toBeGreaterThan(0)

    plan.suites[1].protocol = "wan-video"
    expect(eligibleRuntimeModels(catalog, plan.id)).toEqual([])
  })

  it("keeps the pinned Suite protocol when the current Case revision changes protocol", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    plan.suites[0].model_target = ""
    plan.model_ids = []
    plan.channel_ids = []
    plan.model_count = 0
    plan.channel_count = 0
    const before = eligibleRuntimeModels(catalog, plan.id).map((model) => model.id)

    const pinnedCaseID = plan.suites[0].cases[0].case_id
    catalog.test_cases.find((testCase) => testCase.id === pinnedCaseID)!.protocol = "wan-video"

    expect(eligibleRuntimeModels(catalog, plan.id).map((model) => model.id)).toEqual(before)
  })

  it("requires a targetless Plan mapping to match every pinned Suite model target", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    plan.model_ids = []
    plan.channel_ids = []
    plan.model_count = 0
    plan.channel_count = 0
    plan.suites[0].model_target = "gpt-5.2"

    expect(eligibleRuntimeModels(catalog, plan.id).map((model) => model.name)).toEqual(["gpt-5.2"])
    expect(eligibleRuntimeChannels(catalog, plan.id, catalog.models[0].id).map((channel) => channel.name))
      .toEqual(["OpenAI 主渠道"])

    plan.suites[0].model_target = "missing-upstream-model"
    expect(eligibleRuntimeModels(catalog, plan.id)).toEqual([])
  })

  it("rejects a Plan whose ordered Suites require conflicting model targets", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    plan.model_ids = []
    plan.channel_ids = []
    plan.model_count = 0
    plan.channel_count = 0
    plan.suites[0].model_target = "gpt-5.2"
    plan.suites.push({
      ...structuredClone(plan.suites[0]),
      entry_id: "99999999-9999-4999-8999-999999999992",
      model_target: "qwen3-max",
    })
    plan.suite_count = 2
    plan.case_count += plan.suites[1].case_count

    expect(eligibleRuntimeModels(catalog, plan.id)).toEqual([])
    expect(eligibleRuntimeChannels(catalog, plan.id, catalog.models[0].id)).toEqual([])
  })
})
