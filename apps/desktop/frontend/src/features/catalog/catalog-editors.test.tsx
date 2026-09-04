import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { CatalogEditor } from "./catalog-editors"
import { EMPTY_CATALOG, type CatalogActions, type CatalogSnapshot, type CatalogTestCase } from "./data"

describe("CatalogEditor latency ladder", () => {
  it("inherits uniform request counts and submits per-stage overrides", async () => {
    const user = userEvent.setup()
    const item: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020",
      revision: 3,
      key: "T044",
      name: "输入 Token 阶梯延迟",
      dimension: "performance",
      protocol: "openai-chat",
      enabled: true,
      default: false,
      severity: "normal",
      execution_mode: "automatic",
      definition_schema_version: 2,
      type: "latency.input_ladder",
      type_version: 2,
      spec: {
        request: { method: "POST", path: "/v1/chat/completions", headers: {}, body: { messages: [{ role: "user", content: "placeholder" }] } },
        stages: [{ input_tokens: 128 }, { input_tokens: 512 }],
        warmups_per_step: 1,
        samples_per_step: 3,
        output_tokens: 16,
        timeout_ms: 600000,
        cache_mode: "cold",
      },
    }
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      case_types: [{
        type: "latency.input_ladder", type_version: 2, label: "输入阶梯延迟", category: "performance",
        scheduling_owner: "case", supported_protocols: ["openai-chat", "kimi-k3"], creatable: true,
        default_spec: item.spec,
      }],
      test_cases: [item],
    }
    const updateTestCase = vi.fn().mockResolvedValue(catalog)
    const actions = { updateTestCase } as unknown as CatalogActions

    render(<CatalogEditor kind="case" item={item} catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "编辑用例" }))

    expect(screen.getByLabelText("阶梯 1 输入 Token")).toHaveValue(128)
    expect(screen.getByLabelText("阶梯 2 采样次数")).toHaveValue(null)
    await user.type(screen.getByLabelText("阶梯 2 采样次数"), "7")
    await user.click(screen.getByRole("button", { name: "保存用例" }))

    expect(updateTestCase).toHaveBeenCalledWith(expect.objectContaining({
      type_version: 2,
      spec: expect.objectContaining({
        warmups_per_step: 1,
        samples_per_step: 3,
        stages: [
          { input_tokens: 128 },
          { input_tokens: 512, samples: 7 },
        ],
      }),
    }))
  })
})
