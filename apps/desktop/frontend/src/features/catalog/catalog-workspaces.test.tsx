import { getI18n } from "react-i18next"
import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { CasesWorkspace, ModelChannelWorkspace, PlansWorkspace } from "./catalog-workspaces"
import { EMPTY_CATALOG, type CatalogActions, type CatalogSnapshot } from "./data"

describe("ModelChannelWorkspace", () => {
  it("shows model-to-channel configuration as a two-dimensional matrix", async () => {
    const user = userEvent.setup()
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      models: [
        {
          id: "123e4567-e89b-42d3-a456-426614174001",
          revision: 1,
          name: "GPT-4o",
          protocol: "openai-chat",
          capabilities: ["chat"],
        },
        {
          id: "123e4567-e89b-42d3-a456-426614174002",
          revision: 1,
          name: "Kimi K3",
          protocol: "openai-chat",
          capabilities: ["chat"],
        },
      ],
      channels: [
        {
          id: "123e4567-e89b-42d3-a456-426614174011",
          revision: 1,
          name: "OpenAI 主渠道",
          base_url: "https://api.openai.example/v1",
          protocol: "openai-chat",
          enabled: true,
          credential_configured: true,
          model_count: 1,
        },
        {
          id: "123e4567-e89b-42d3-a456-426614174012",
          revision: 1,
          name: "Kimi 备用渠道",
          base_url: "https://api.kimi.example/v1",
          protocol: "openai-chat",
          enabled: true,
          credential_configured: true,
          model_count: 0,
        },
      ],
      channel_models: [
        {
          id: "123e4567-e89b-42d3-a456-426614174021",
          revision: 1,
          channel_id: "123e4567-e89b-42d3-a456-426614174011",
          model_id: "123e4567-e89b-42d3-a456-426614174001",
          upstream_model_name: "gpt-4o-2024-11-20",
        },
      ],
    }

    const createChannelModel = vi.fn().mockResolvedValue(catalog)
    const updateChannelModel = vi.fn().mockResolvedValue(catalog)
    render(
      <ModelChannelWorkspace
        catalog={catalog}
        actions={{ createChannelModel, updateChannelModel } as unknown as CatalogActions}
        mutate={async (operation) => { await operation() }}
        mutationPending={false}
        mutationError=""
      />,
    )

    await user.click(screen.getByRole("tab", { name: /矩阵/ }))

    await act(async () => { document.documentElement.lang = "en-US"; await getI18n().changeLanguage("en-US") })
    const englishMatrix = screen.getByRole("table", { name: "Model and channel configuration matrix" })
    expect(within(englishMatrix).getByRole("columnheader", { name: "OpenAI 主渠道" })).toBeInTheDocument()
    expect(within(englishMatrix).getByRole("button", { name: "View GPT-4o mapping on OpenAI 主渠道: gpt-4o-2024-11-20" })).toHaveTextContent("Configured")
    await act(async () => { document.documentElement.lang = "zh-CN"; await getI18n().changeLanguage("zh-CN") })

    const matrix = screen.getByRole("table", { name: "模型渠道配置矩阵" })
    expect(matrix.closest('[data-slot="scroll-area-viewport"]')).not.toBeNull()
    expect(within(matrix).getByRole("columnheader", { name: "OpenAI 主渠道" })).toBeInTheDocument()
    expect(within(matrix).getByRole("columnheader", { name: "Kimi 备用渠道" })).toBeInTheDocument()
    expect(within(matrix).getByRole("rowheader", { name: /GPT-4o/ })).toBeInTheDocument()
    expect(within(matrix).getByRole("rowheader", { name: /Kimi K3/ })).toBeInTheDocument()

    const configured = within(matrix).getByRole("button", {
      name: "查看 GPT-4o 在 OpenAI 主渠道的映射：gpt-4o-2024-11-20",
    })
    expect(configured).toHaveTextContent("已配置")
    expect(configured).toHaveTextContent("GPT-4o")
    expect(configured).toHaveTextContent("gpt-4o-2024-11-20")

    const unconfigured = within(matrix).getByRole("cell", {
      name: "Kimi K3 在 Kimi 备用渠道未配置",
    })
    expect(unconfigured).toHaveTextContent("未配置")
    await user.hover(unconfigured)
    expect(unconfigured).toHaveAttribute("data-intersection", "true")
    expect(within(matrix).getByRole("columnheader", { name: "Kimi 备用渠道" })).toHaveAttribute("data-crosshair", "true")
    expect(within(matrix).getByRole("rowheader", { name: /Kimi K3/ })).toHaveAttribute("data-crosshair", "true")
    expect(configured.closest("td")).not.toHaveAttribute("data-crosshair")
    await user.unhover(unconfigured)
    expect(unconfigured).not.toHaveAttribute("data-intersection")
    await user.hover(configured)
    expect(configured.closest("td")).toHaveAttribute("data-intersection", "true")
    expect(within(matrix).getByRole("columnheader", { name: "OpenAI 主渠道" })).toHaveAttribute("data-crosshair", "true")

    await user.click(configured)
    const editDialog = screen.getByRole("dialog", { name: "编辑映射" })
    const upstream = within(editDialog).getByRole("textbox", { name: /上游模型/ })
    expect(upstream).toHaveValue("gpt-4o-2024-11-20")
    await user.clear(upstream)
    await user.type(upstream, "gpt-4o-updated")
    await user.click(within(editDialog).getByRole("button", { name: "保存映射" }))
    expect(updateChannelModel).toHaveBeenCalledWith({
      id: catalog.channel_models[0].id, expected_revision: 1, upstream_model_name: "gpt-4o-updated",
    })
    expect(configured).toHaveFocus()

    const createButton = within(unconfigured).getByRole("button")
    createButton.focus()
    await user.keyboard("{Enter}")
    const createDialog = screen.getByRole("dialog", { name: "新增映射" })
    expect(within(createDialog).getByRole("combobox", { name: "渠道" })).toHaveValue("Kimi 备用渠道")
    expect(within(createDialog).getByRole("combobox", { name: "逻辑模型" })).toHaveValue("Kimi K3")
    await user.type(within(createDialog).getByRole("textbox", { name: /上游模型/ }), "kimi-k3")
    await user.click(within(createDialog).getByRole("button", { name: "保存映射" }))
    expect(createChannelModel).toHaveBeenCalledWith({
      channel_id: catalog.channels[1].id, model_id: catalog.models[1].id, upstream_model_name: "kimi-k3",
    })
    expect(createButton).toHaveFocus()
  })
})

describe("CasesWorkspace", () => {
  it("renders human-readable case type labels with the interface font", () => {
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      case_types: [{
        type: "legacy.apiaudit",
        type_version: 1,
        label: "内置兼容性审计",
        category: "compatibility",
        scheduling_owner: "case",
        supported_protocols: ["openai-chat"],
        creatable: false,
        default_spec: { kind: "chat_sync" },
      }],
      test_cases: [{
        id: "123e4567-e89b-42d3-a456-426614174020",
        revision: 1,
        key: "F001",
        name: "输入 usage 增长检测",
        dimension: "compatibility",
        protocol: "openai-chat",
        model_targets: [],
        enabled: true,
        default: true,
        severity: "normal",
        execution_mode: "automatic",
        definition_schema_version: 2,
        type: "legacy.apiaudit",
        type_version: 1,
        spec: { kind: "chat_sync" },
      }],
    }

    render(
      <CasesWorkspace
        catalog={catalog}
        actions={{} as CatalogActions}
        mutate={async (operation) => { await operation() }}
        mutationPending={false}
        mutationError=""
      />,
    )

    expect(screen.getByRole("cell", { name: "内置兼容性审计" })).not.toHaveClass("font-mono")
  })

  it("renders the Wan video protocol label", () => {
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      case_types: [{
        type: "legacy.apiaudit",
        type_version: 1,
        label: "内置兼容性审计",
        category: "compatibility",
        scheduling_owner: "case",
        supported_protocols: ["wan-video"],
        creatable: false,
        default_spec: { kind: "wan_task_success" },
      }],
      test_cases: [{
        id: "123e4567-e89b-42d3-a456-426614174021",
        revision: 1,
        key: "wan30.t2v_smoke",
        name: "Wan 3.0 文生视频冒烟",
        dimension: "compatibility",
        protocol: "wan-video",
        model_targets: ["wan3.0-video"],
        enabled: true,
        default: false,
        severity: "critical",
        execution_mode: "automatic",
        definition_schema_version: 2,
        type: "legacy.apiaudit",
        type_version: 1,
        spec: { kind: "wan_task_success" },
      }],
    }

    render(
      <CasesWorkspace
        catalog={catalog}
        actions={{} as CatalogActions}
        mutate={async (operation) => { await operation() }}
        mutationPending={false}
        mutationError=""
      />,
    )

    expect(screen.getAllByText("Wan Video").length).toBeGreaterThan(0)
  })
})

describe("PlansWorkspace", () => {
  it("searches every ordered suite and shows the sequence in the inspector", async () => {
    const user = userEvent.setup()
    const suiteA = { id: "123e4567-e89b-42d3-a456-426614174061", revision: 1, key: "alpha", name: "Alpha suite", protocol: "openai-chat" as const, model_target: "gpt-test", case_count: 1, cases: [{ case_id: "123e4567-e89b-42d3-a456-426614174071", revision: 1 }] }
    const suiteB = { id: "123e4567-e89b-42d3-a456-426614174062", revision: 2, key: "beta", name: "Beta searchable suite", protocol: "openai-chat" as const, model_target: "gpt-test", case_count: 1, cases: [{ case_id: "123e4567-e89b-42d3-a456-426614174072", revision: 1 }] }
    const entry = (entryID: string, suite: typeof suiteA, concurrency: number) => ({
      entry_id: entryID,
      suite_id: suite.id,
      suite_revision: suite.revision,
      suite_key: suite.key,
      suite_name: suite.name,
      protocol: suite.protocol,
      model_target: suite.model_target,
      case_count: suite.case_count,
      cases: suite.cases,
      parameters: {},
      load_mode: "fixed_concurrency" as const,
      concurrency,
      request_count: 10,
      rate_per_second: 0,
      duration_ms: 0,
      request_timeout_ms: 30_000,
      sla_thresholds: { e2e_p95_ms: 3_000 },
    })
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      suites: [suiteA, suiteB],
      plans: [{
        id: "123e4567-e89b-42d3-a456-426614174060",
        revision: 1,
        name: "Ordered plan",
        model_count: 0,
        channel_count: 0,
        suite_count: 2,
        case_count: 2,
        model_ids: [],
        channel_ids: [],
        suites: [entry("123e4567-e89b-42d3-a456-426614174081", suiteA, 2), entry("123e4567-e89b-42d3-a456-426614174082", suiteB, 4)],
      }],
    }

    render(<PlansWorkspace catalog={catalog} actions={{} as CatalogActions} mutate={async (operation) => { await operation() }} mutationPending={false} mutationError="" commandPending={false} onStartPlan={async () => {}} />)

    await user.type(screen.getByRole("searchbox", { name: "搜索计划" }), "Beta searchable")
    expect(screen.getByRole("row", { name: /Ordered plan/ })).toBeInTheDocument()
    expect(screen.getByText("1. Alpha suite")).toBeInTheDocument()
    expect(screen.getByText("2. Beta searchable suite")).toBeInTheDocument()
    expect(screen.getByText("2 个套件 · 2 个用例")).toBeInTheDocument()
  })
})
