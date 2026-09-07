import { describe, expect, it } from "vitest"

import { FIXTURE_CATALOG } from "./fixtures"
import { eligibleRuntimeChannels, eligibleRuntimeModels, paidRuntimeProtocol } from "./run-targets"

describe("runtime plan target selection", () => {
  it("uses protocol metadata and applicable pinned members for Seedance billing acknowledgement", () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    const model = catalog.models.find((item) => item.id === plan.model_ids[0])!
    const channelID = plan.channel_ids[0]
    const mapping = catalog.channel_models.find((item) => item.model_id === model.id && item.channel_id === channelID)!
    model.protocol = "seedance"
    plan.cases = plan.cases.slice(0, 2)
    for (const ref of plan.cases) {
      const testCase = catalog.test_cases.find((item) => item.id === ref.case_id)!
      testCase.model_targets = []
    }
    expect(paidRuntimeProtocol(catalog, plan.id, model.id, channelID)?.id).toBe("seedance")
    const second = catalog.test_cases.find((item) => item.id === plan.cases[1].case_id)!
    second.model_targets = [`${mapping.upstream_model_name}-other`]
    expect(paidRuntimeProtocol(catalog, plan.id, model.id, channelID)).toBeUndefined()
    second.revision++
    expect(paidRuntimeProtocol(catalog, plan.id, model.id, channelID)?.id).toBe("seedance")
  })
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
})
