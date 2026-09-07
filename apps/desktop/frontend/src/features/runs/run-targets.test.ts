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
