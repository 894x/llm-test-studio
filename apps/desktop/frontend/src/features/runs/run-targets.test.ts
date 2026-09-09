import { describe, expect, it } from "vitest"
import { FIXTURE_CATALOG } from "./fixtures"
import { eligibleRuntimeChannels, eligibleRuntimeModels } from "./run-targets"

describe("protocol-only runtime bindings", () => {
  it("offers protocol-matched configured channels independently of plan entry contents", () => {
    const catalog = structuredClone(FIXTURE_CATALOG); const plan = catalog.plans[0]
    expect(eligibleRuntimeModels(catalog, plan.id).map(model => model.name)).toContain("gpt-5.2")
    const model = catalog.models[0]
    expect(eligibleRuntimeChannels(catalog, plan.id, model.id).map(channel => channel.id)).toEqual([catalog.channels[0].id])
    plan.entries[0].target_id = "10000000-0000-4000-8000-000000000099"
    expect(eligibleRuntimeChannels(catalog, plan.id, model.id)).toHaveLength(1)
  })
  it("excludes disabled, uncredentialed, unmapped, and different-protocol channels", () => {
    const catalog = structuredClone(FIXTURE_CATALOG); const plan = catalog.plans[0]; const model = catalog.models[0]
    catalog.channels[0].enabled = false; expect(eligibleRuntimeChannels(catalog, plan.id, model.id)).toEqual([])
    catalog.channels[0].enabled = true; catalog.channels[0].credential_configured = false; expect(eligibleRuntimeChannels(catalog, plan.id, model.id)).toEqual([])
    catalog.channels[0].credential_configured = true; plan.protocol = "seedance"; expect(eligibleRuntimeModels(catalog, plan.id)).toEqual([])
  })
})
