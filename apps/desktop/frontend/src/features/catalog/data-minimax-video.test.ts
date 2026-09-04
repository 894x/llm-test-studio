import { describe, expect, it } from "vitest"
import { parseCatalogSnapshot } from "./data"

describe("MiniMax video catalog protocol", () => {
  it("accepts MiniMax H3 models in catalog snapshots", () => {
    const parsed = parseCatalogSnapshot({
      schema_version: 2,
      case_types: [],
      models: [{
        id: "11111111-1111-4111-8111-111111111111",
        revision: 1,
        name: "MiniMax-H3",
        protocol: "minimax-video",
        capabilities: ["video"],
      }],
      channels: [],
      channel_models: [],
      test_cases: [],
      suites: [],
      plans: [],
    })

    expect(parsed.models[0]?.protocol).toBe("minimax-video")
  })
})
