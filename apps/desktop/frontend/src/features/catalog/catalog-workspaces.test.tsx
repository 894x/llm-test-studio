import { getI18n } from "react-i18next"
import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

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
          protocols: ["openai-chat"], capabilities: ["chat"],
        },
        {
          id: "123e4567-e89b-42d3-a456-426614174002",
          revision: 1,
          name: "Kimi K3",
          protocols: ["openai-chat"], capabilities: ["chat"],
        },
      ],
      channels: [
        {
          id: "123e4567-e89b-42d3-a456-426614174011",
          revision: 1,
          name: "OpenAI 主渠道",
          base_url: "https://api.openai.example/v1",

          enabled: true,
          credential_configured: true,
          model_count: 1,
        },
        {
          id: "123e4567-e89b-42d3-a456-426614174012",
          revision: 1,
          name: "Kimi 备用渠道",
          base_url: "https://api.kimi.example/v1",

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
          upstream_model_name: "gpt-4o-2024-11-20", protocols: ["openai-chat"],
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
      id: catalog.channel_models[0].id, expected_revision: 1, upstream_model_name: "gpt-4o-updated", protocols: ["openai-chat"],
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
      channel_id: catalog.channels[1].id, model_id: catalog.models[1].id, upstream_model_name: "kimi-k3", protocols: ["openai-chat"],
    })
    expect(createButton).toHaveFocus()
  })
})
