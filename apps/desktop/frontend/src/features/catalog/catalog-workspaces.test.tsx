import { getI18n } from "react-i18next"
import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"

import { ModelChannelWorkspace } from "./catalog-workspaces"
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
  })
})
