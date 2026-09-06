import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"

import { CasesWorkspace, ModelChannelWorkspace } from "./catalog-workspaces"
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
          protocol: "kimi-k3",
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
          protocol: "kimi-k3",
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

    render(
      <ModelChannelWorkspace
        catalog={catalog}
        actions={{} as CatalogActions}
        mutate={async (operation) => { await operation() }}
        mutationPending={false}
        mutationError=""
      />,
    )

    await user.click(screen.getByRole("tab", { name: /矩阵/ }))

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
