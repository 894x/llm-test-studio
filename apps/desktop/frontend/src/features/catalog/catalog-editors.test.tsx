import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { DesktopClientError } from "@/app/desktop-client"

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
      model_targets: ["gpt-5.2", "gpt-4.1-mini"],
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
    expect(screen.getByLabelText("适用模型")).toHaveValue("gpt-5.2, gpt-4.1-mini")
    expect(screen.getByLabelText("阶梯 2 采样次数")).toHaveValue(null)
    await user.type(screen.getByLabelText("阶梯 2 采样次数"), "7")
    await user.click(screen.getByRole("button", { name: "保存用例" }))

    expect(updateTestCase).toHaveBeenCalledWith(expect.objectContaining({
      type_version: 2,
      model_targets: ["gpt-5.2", "gpt-4.1-mini"],
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

describe("CatalogEditor filesystem suite", () => {
  it("submits a per-model shareable suite identity", async () => {
    const user = userEvent.setup()
    const testCase: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020",
      revision: 7,
      key: "T001",
      name: "Basic chat",
      dimension: "compatibility",
      protocol: "openai-chat",
      model_targets: ["gpt-5.2"],
      enabled: true,
      default: false,
      severity: "normal",
      execution_mode: "automatic",
      definition_schema_version: 2,
      type: "request.single",
      type_version: 1,
      spec: { request: {}, expected: {}, assertions: [] },
    }
    const catalog: CatalogSnapshot = { ...EMPTY_CATALOG, test_cases: [testCase] }
    const createSuite = vi.fn().mockResolvedValue(catalog)
    const actions = { createSuite } as unknown as CatalogActions

    render(<CatalogEditor kind="suite" catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增套件" }))
    await user.type(screen.getByLabelText("套件标识"), "gpt-5.2-smoke")
    await user.type(screen.getByLabelText("套件名称"), "GPT-5.2 smoke")
    await user.type(screen.getByLabelText("目标模型"), "gpt-5.2")
    await user.click(screen.getByRole("button", { name: "保存套件" }))

    expect(createSuite).toHaveBeenCalledWith({
      key: "gpt-5.2-smoke",
      name: "GPT-5.2 smoke",
      protocol: "openai-chat",
      model_target: "gpt-5.2",
      cases: [{ case_id: testCase.id, revision: testCase.revision }],
    })
  })
})

describe("CatalogEditor plan errors", () => {
  it("keeps the plan form open and explains how to resolve a protocol mismatch", async () => {
    const user = userEvent.setup()
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      test_cases: [{
        id: "123e4567-e89b-42d3-a456-426614174021",
        revision: 1,
        key: "must.usage_stream",
        name: "协议 · Usage 流式",
        dimension: "compatibility",
        protocol: "kimi-k3",
        model_targets: ["kimi-k3"],
        enabled: true,
        default: true,
        severity: "normal",
        execution_mode: "automatic",
        definition_schema_version: 2,
        type: "legacy.apiaudit",
        type_version: 1,
        spec: { kind: "chat_stream" },
      }],
    }
    const createPlan = vi.fn().mockRejectedValue(new DesktopClientError("plan_protocol_mismatch"))
    const actions = { createPlan } as unknown as CatalogActions

    render(<CatalogEditor kind="plan" catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划名称"), "Kimi K3 兼容性计划")
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "新增计划保存失败：计划中的用例、模型和渠道协议不一致，请选择与用例协议一致的模型和渠道，或调整用例/套件",
    )
    expect(screen.getByLabelText("计划名称")).toHaveValue("Kimi K3 兼容性计划")
    expect(screen.getByRole("dialog", { name: "新增计划" })).toBeInTheDocument()
  })
})
