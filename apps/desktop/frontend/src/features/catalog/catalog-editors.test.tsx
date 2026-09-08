import { I18nextProvider } from "react-i18next"
import { createAppI18n } from "@/i18n/i18n"
import { act, fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { DesktopClientError } from "@/app/desktop-client"

import { CatalogEditor } from "./catalog-editors"
import { EMPTY_CATALOG, type CatalogActions, type CatalogSnapshot, type CatalogTestCase } from "./data"

describe("CatalogEditor latency ladder", () => {
  it("keeps field errors associated and translates them without resubmitting on a language change", async () => {
    const user = userEvent.setup()
    const instance = createAppI18n("zh-CN")
    const mutate = vi.fn()
    render(<I18nextProvider i18n={instance}><CatalogEditor kind="channel" catalog={EMPTY_CATALOG} actions={{} as CatalogActions} pending={false} mutate={mutate} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "新增渠道" }))
    await user.click(screen.getByRole("button", { name: "保存渠道" }))
    expect(screen.getByLabelText("渠道名称")).toHaveAttribute("aria-invalid", "true")
    await act(async () => { document.documentElement.lang = "en-US"; await instance.changeLanguage("en-US") })
    expect(screen.getByLabelText("Channel name")).toHaveAttribute("aria-invalid", "true")
    expect(screen.getByLabelText("Channel name")).toHaveAccessibleDescription("Enter a channel name.")
    expect(mutate).not.toHaveBeenCalled()
    await user.type(screen.getByLabelText("Channel name"), "New channel")
    expect(screen.getByLabelText("Channel name")).not.toHaveAttribute("aria-invalid")
  })

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
        scheduling_owner: "case", supported_protocols: ["openai-chat"], creatable: true,
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

  it("locates an invalid ladder stage and clears the inline error when edited", async () => {
    const user = userEvent.setup()
    const item: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020",
      revision: 3,
      key: "T044",
      name: "输入 Token 阶梯延迟",
      dimension: "performance",
      protocol: "openai-chat",
      model_targets: ["gpt-5.2"],
      enabled: true,
      default: false,
      severity: "normal",
      execution_mode: "automatic",
      definition_schema_version: 2,
      type: "latency.input_ladder",
      type_version: 2,
      spec: {
        request: {},
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
        scheduling_owner: "case", supported_protocols: ["openai-chat"], creatable: true,
        default_spec: item.spec,
      }],
      test_cases: [item],
    }
    const updateTestCase = vi.fn().mockResolvedValue(catalog)
    const actions = { updateTestCase } as unknown as CatalogActions

    render(<CatalogEditor kind="case" item={item} catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "编辑用例" }))
    const secondStage = screen.getByLabelText("阶梯 2 输入 Token")
    await user.clear(secondStage)
    await user.type(secondStage, "64")
    await user.click(screen.getByRole("button", { name: "保存用例" }))

    expect(screen.getByText("请输入递增且不超过 1,000,000 的整数。")).toHaveAttribute("data-slot", "field-error")
    expect(secondStage).toHaveAttribute("aria-invalid", "true")
    expect(secondStage).toHaveFocus()
    expect(updateTestCase).not.toHaveBeenCalled()

    await user.clear(secondStage)
    expect(screen.queryByText("请输入递增且不超过 1,000,000 的整数。")).not.toBeInTheDocument()
  })

  it("rejects an unsafe model target at the model-target input", async () => {
    const user = userEvent.setup()
    const item: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020",
      revision: 3,
      key: "T044",
      name: "输入 Token 阶梯延迟",
      dimension: "performance",
      protocol: "openai-chat",
      model_targets: ["gpt-5.2"],
      enabled: true,
      default: false,
      severity: "normal",
      execution_mode: "automatic",
      definition_schema_version: 2,
      type: "latency.input_ladder",
      type_version: 2,
      spec: { request: {}, stages: [{ input_tokens: 128 }], warmups_per_step: 1, samples_per_step: 3, output_tokens: 16, timeout_ms: 600000, cache_mode: "cold" },
    }
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      case_types: [{ type: "latency.input_ladder", type_version: 2, label: "输入阶梯延迟", category: "performance", scheduling_owner: "case", supported_protocols: ["openai-chat"], creatable: true, default_spec: item.spec }],
      test_cases: [item],
    }
    const updateTestCase = vi.fn().mockResolvedValue(catalog)
    const actions = { updateTestCase } as unknown as CatalogActions

    render(<CatalogEditor kind="case" item={item} catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "编辑用例" }))
    const targets = screen.getByLabelText("适用模型")
    fireEvent.change(targets, { target: { value: "模".repeat(86) } })
    await user.click(screen.getByRole("button", { name: "保存用例" }))

    expect(screen.getByText("模型 ID 的 UTF-8 编码不能超过 256 字节，且不能包含控制字符。")).toHaveAttribute("data-slot", "field-error")
    expect(targets).toHaveFocus()
    expect(updateTestCase).not.toHaveBeenCalled()
  })
})

describe("CatalogEditor filesystem suite", () => {
  it("preserves quick task parameters and offers only generic cases without a target", async () => {
    const user = userEvent.setup()
    const testCase: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020", revision: 7, key: "T001", name: "Basic chat",
      dimension: "compatibility", protocol: "openai-chat", model_targets: [], enabled: true,
      default: false, severity: "normal", execution_mode: "automatic", definition_schema_version: 2,
      type: "request.single", type_version: 1, spec: { request: {}, expected: {}, assertions: [] },
    }
    const catalog: CatalogSnapshot = { ...EMPTY_CATALOG, test_cases: [testCase, { ...testCase, id: "123e4567-e89b-42d3-a456-426614174021", name: "Scoped chat", model_targets: ["gpt-test"] }] }
    const quickTest = { description: "Connection", timeout_ms: 30000, inputs: [] }
    const item = { id: "123e4567-e89b-42d3-a456-426614174022", revision: 1, key: "connection", name: "Connection", protocol: "openai-chat" as const, model_target: "", case_count: 1, cases: [{ case_id: testCase.id, revision: testCase.revision }], quick_test: quickTest }
    const updateSuite = vi.fn().mockResolvedValue(catalog)
    render(<CatalogEditor kind="suite" item={item} catalog={catalog} actions={{ updateSuite } as unknown as CatalogActions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "编辑套件" }))
    expect(screen.queryByRole("checkbox", { name: "Scoped chat · r7" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "保存套件" }))
    expect(updateSuite).toHaveBeenCalledWith(expect.objectContaining({ model_target: "", quick_test: quickTest, cases: item.cases }))
  })

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

  it("requires a fixed case and associates the prompt with the case choices", async () => {
    const user = userEvent.setup()
    const testCase: CatalogTestCase = {
      id: "123e4567-e89b-42d3-a456-426614174020", revision: 7, key: "T001", name: "Basic chat",
      dimension: "compatibility", protocol: "openai-chat", model_targets: ["gpt-5.2"], enabled: true,
      default: false, severity: "normal", execution_mode: "automatic", definition_schema_version: 2,
      type: "request.single", type_version: 1, spec: { request: {}, expected: {}, assertions: [] },
    }
    const sameLabelCase = { ...testCase, id: "123e4567-e89b-42d3-a456-426614174099" }
    const catalog: CatalogSnapshot = { ...EMPTY_CATALOG, test_cases: [testCase, sameLabelCase] }
    const createSuite = vi.fn().mockResolvedValue(catalog)
    const actions = { createSuite } as unknown as CatalogActions

    render(<CatalogEditor kind="suite" catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增套件" }))
    await user.type(screen.getByLabelText("套件标识"), "gpt-5.2-smoke")
    await user.type(screen.getByLabelText("套件名称"), "GPT-5.2 smoke")
    await user.type(screen.getByLabelText("目标模型"), "gpt-5.2")
    const matchingChoices = screen.getAllByRole("checkbox", { name: "Basic chat · r7" })
    expect(matchingChoices[0].id).not.toBe(matchingChoices[1].id)
    const caseChoice = matchingChoices[0]
    await user.click(caseChoice)
    await user.click(screen.getByRole("button", { name: "保存套件" }))

    const prompt = screen.getByText("请至少选择一个用例。")
    expect(prompt).toHaveAttribute("data-slot", "field-error")
    expect(screen.getByRole("group", { name: "包含用例" })).toHaveAttribute("aria-describedby", prompt.id)
    expect(caseChoice).toHaveFocus()
    expect(createSuite).not.toHaveBeenCalled()
  })

  it("focuses an empty case group when there are no selectable cases", async () => {
    const user = userEvent.setup()
    const createSuite = vi.fn()
    const actions = { createSuite } as unknown as CatalogActions
    render(<CatalogEditor kind="suite" catalog={EMPTY_CATALOG} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增套件" }))
    await user.type(screen.getByLabelText("套件标识"), "empty-suite")
    await user.type(screen.getByLabelText("套件名称"), "空套件")
    await user.type(screen.getByLabelText("目标模型"), "gpt-test")
    await user.click(screen.getByRole("button", { name: "保存套件" }))

    const group = screen.getByRole("group", { name: "包含用例" })
    expect(screen.getByText("请至少选择一个用例。")).toBeInTheDocument()
    expect(group).toHaveFocus()
    expect(createSuite).not.toHaveBeenCalled()
  })
})

describe("CatalogEditor plan errors", () => {
  it("requires at least one suite", async () => {
    const user = userEvent.setup()
    const catalog: CatalogSnapshot = EMPTY_CATALOG
    const createPlan = vi.fn().mockResolvedValue(catalog)
    const actions = { createPlan } as unknown as CatalogActions

    render(<CatalogEditor kind="plan" catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划名称"), "无用例计划")
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    const suiteGroup = screen.getByRole("group", { name: "套件" })
    expect(screen.getByText("请至少添加一个套件。")).toHaveAttribute("data-slot", "field-error")
    expect(suiteGroup).toHaveFocus()
    expect(createPlan).not.toHaveBeenCalled()
  })

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
        protocol: "openai-chat",
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
      suites: [{
        id: "123e4567-e89b-42d3-a456-426614174022",
        revision: 1,
        key: "kimi-stream",
        name: "Kimi K3 流式套件",
        protocol: "openai-chat",
        model_target: "kimi-k3",
        case_count: 1,
        cases: [{ case_id: "123e4567-e89b-42d3-a456-426614174021", revision: 1 }],
      }],
    }
    const createPlan = vi.fn().mockRejectedValue(new DesktopClientError("plan_protocol_mismatch"))
    const actions = { createPlan } as unknown as CatalogActions

    render(<CatalogEditor kind="plan" catalog={catalog} actions={actions} pending={false} mutate={async (operation) => { await operation() }} />)
    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划名称"), "Kimi K3 兼容性计划")
    await user.click(screen.getByRole("button", { name: "添加套件" }))
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "新增计划保存失败：计划中的用例、模型和渠道协议不一致，请选择与用例协议一致的模型和渠道，或调整用例/套件",
    )
    expect(screen.getByLabelText("计划名称")).toHaveValue("Kimi K3 兼容性计划")
    expect(screen.getByRole("dialog", { name: "新增计划" })).toBeInTheDocument()
  })
})

describe("CatalogEditor ordered plan suites", () => {
  const suiteID = "123e4567-e89b-42d3-a456-426614174040"
  const caseID = "123e4567-e89b-42d3-a456-426614174041"
  const suite = {
    id: suiteID,
    revision: 3,
    key: "chat-smoke",
    name: "Chat smoke",
    protocol: "openai-chat" as const,
    model_target: "gpt-test",
    case_count: 1,
    cases: [{ case_id: caseID, revision: 2 }],
    quick_test: {
      description: "Smoke",
      timeout_ms: 30_000,
      inputs: [
        {
          key: "prompt",
          label: "提示词",
          type: "text" as const,
          default: "hello",
          bindings: [{ case_key: "chat.basic", pointer: "/request/body/messages/0/content" }],
        },
        {
          key: "temperature",
          label: "温度",
          type: "number" as const,
          default: 0.2,
          bindings: [{ case_key: "chat.basic", pointer: "/request/body/temperature" }],
        },
        {
          key: "stream",
          label: "流式",
          type: "boolean" as const,
          default: true,
          bindings: [{ case_key: "chat.basic", pointer: "/request/body/stream" }],
        },
      ],
    },
  }
  const catalog: CatalogSnapshot = { ...EMPTY_CATALOG, suites: [suite] }

  it("keeps repeated suite parameters independent and submits their reordered sequence", async () => {
    const user = userEvent.setup()
    const createPlan = vi.fn().mockResolvedValue(catalog)
    render(<CatalogEditor kind="plan" catalog={catalog} actions={{ createPlan } as unknown as CatalogActions} pending={false} mutate={async (operation) => { await operation() }} />)

    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划名称"), "重复套件计划")
    await user.click(screen.getByRole("button", { name: "添加套件" }))
    await user.click(screen.getByRole("button", { name: "添加套件" }))

    const prompts = screen.getAllByLabelText("提示词")
    await user.clear(prompts[0])
    await user.type(prompts[0], "first")
    await user.clear(prompts[1])
    await user.type(prompts[1], "second")
    const temperatures = screen.getAllByLabelText("温度")
    await user.clear(temperatures[0])
    await user.type(temperatures[0], "0.3")
    await user.clear(temperatures[1])
    await user.type(temperatures[1], "0.9")
    const streamInputs = screen.getAllByLabelText("流式")
    await user.click(streamInputs[1])
    const concurrency = screen.getAllByLabelText("并发数")
    expect(concurrency[0]).not.toHaveAttribute("id", concurrency[1].getAttribute("id"))
    await user.clear(concurrency[0])
    await user.type(concurrency[0], "2")
    await user.clear(concurrency[1])
    await user.type(concurrency[1], "7")
    const slas = screen.getAllByLabelText("SLA 阈值 JSON")
    fireEvent.change(slas[0], { target: { value: '{"e2e_p95_ms":2000}' } })
    fireEvent.change(slas[1], { target: { value: '{"e2e_p95_ms":7000}' } })

    await user.click(screen.getByRole("button", { name: "将第 2 个套件 Chat smoke 上移" }))
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    expect(createPlan).toHaveBeenCalledWith(expect.objectContaining({
      name: "重复套件计划",
      suites: [
        expect.objectContaining({ suite_id: suiteID, suite_revision: 3, concurrency: 7, parameters: { prompt: "second", temperature: 0.9, stream: false }, sla_thresholds: { e2e_p95_ms: 7000 } }),
        expect.objectContaining({ suite_id: suiteID, suite_revision: 3, concurrency: 2, parameters: { prompt: "first", temperature: 0.3, stream: true }, sla_thresholds: { e2e_p95_ms: 2000 } }),
      ],
    }))
    expect(createPlan.mock.calls[0][0].suites.every((entry: { entry_id?: string }) => entry.entry_id === undefined)).toBe(true)
  })

  it("preserves existing entry ids, omits ids for new entries, and removes an entry", async () => {
    const user = userEvent.setup()
    const firstID = "123e4567-e89b-42d3-a456-426614174051"
    const secondID = "123e4567-e89b-42d3-a456-426614174052"
    const plan = {
      id: "123e4567-e89b-42d3-a456-426614174050",
      revision: 4,
      name: "Editable plan",
      model_count: 0,
      channel_count: 0,
      suite_count: 2,
      case_count: 2,
      model_ids: [],
      channel_ids: [],
      suites: [firstID, secondID].map((entryID, index) => ({
        entry_id: entryID,
        suite_id: suiteID,
        suite_revision: 3,
        suite_key: suite.key,
        suite_name: suite.name,
        protocol: suite.protocol,
        model_target: suite.model_target,
        case_count: 1,
        cases: suite.cases,
        quick_test: suite.quick_test,
        parameters: { prompt: index ? "remove" : "keep", temperature: 0.2, stream: true },
        load_mode: "single" as const,
        concurrency: 1,
        request_count: 1,
        rate_per_second: 0,
        duration_ms: 0,
        request_timeout_ms: 30_000,
        sla_thresholds: { e2e_p95_ms: 3_000 },
      })),
    }
    const updatePlan = vi.fn().mockResolvedValue(catalog)
    render(<CatalogEditor kind="plan" item={plan} catalog={catalog} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async (operation) => { await operation() }} />)

    await user.click(screen.getByRole("button", { name: "编辑计划" }))
    await user.click(screen.getByRole("button", { name: "删除第 2 个套件 Chat smoke" }))
    await user.click(screen.getByRole("button", { name: "添加套件" }))
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    const entries = updatePlan.mock.calls[0][0].suites
    expect(entries).toHaveLength(2)
    expect(entries[0]).toMatchObject({ entry_id: firstID, parameters: { prompt: "keep" } })
    expect(entries[1].entry_id).toBeUndefined()
  })

  it("does not borrow quick-test inputs from a newer Suite revision", async () => {
    const user = userEvent.setup()
    const entryID = "123e4567-e89b-42d3-a456-426614174053"
    const plan = {
      id: "123e4567-e89b-42d3-a456-426614174054",
      revision: 2,
      name: "Pinned historical Suite",
      model_count: 0,
      channel_count: 0,
      suite_count: 1,
      case_count: 1,
      model_ids: [],
      channel_ids: [],
      suites: [{
        entry_id: entryID,
        suite_id: suiteID,
        suite_revision: 2,
        suite_key: "chat-smoke-v2",
        suite_name: "Chat smoke v2",
        protocol: suite.protocol,
        model_target: suite.model_target,
        case_count: 1,
        cases: suite.cases,
        parameters: {},
        load_mode: "single" as const,
        concurrency: 1,
        request_count: 1,
        rate_per_second: 0,
        duration_ms: 0,
        request_timeout_ms: 30_000,
        sla_thresholds: { e2e_p95_ms: 3_000 },
      }],
    }
    const updatePlan = vi.fn().mockResolvedValue(catalog)
    render(<CatalogEditor kind="plan" item={plan} catalog={catalog} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async (operation) => { await operation() }} />)

    await user.click(screen.getByRole("button", { name: "编辑计划" }))
    expect(screen.queryByLabelText("提示词")).not.toBeInTheDocument()
    expect(screen.getByText("此套件没有扩展参数。")).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    expect(updatePlan).toHaveBeenCalledWith(expect.objectContaining({
      suites: [expect.objectContaining({
        entry_id: entryID,
        suite_revision: 2,
        parameters: {},
      })],
    }))
  })

  it("focuses the exact repeated load field that fails validation", async () => {
    const user = userEvent.setup()
    const createPlan = vi.fn()
    render(<CatalogEditor kind="plan" catalog={catalog} actions={{ createPlan } as unknown as CatalogActions} pending={false} mutate={async (operation) => { await operation() }} />)

    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划名称"), "无效负载")
    await user.click(screen.getByRole("button", { name: "添加套件" }))
    await user.click(screen.getByRole("button", { name: "添加套件" }))
    const requestCounts = screen.getAllByLabelText("请求数")
    await user.clear(requestCounts[1])
    await user.type(requestCounts[1], "0")
    await user.click(screen.getByRole("button", { name: "保存计划" }))

    expect(requestCounts[1]).toHaveAttribute("aria-invalid", "true")
    expect(requestCounts[1]).toHaveFocus()
    expect(requestCounts[0]).not.toHaveAttribute("aria-invalid")
    expect(createPlan).not.toHaveBeenCalled()
  })
})
