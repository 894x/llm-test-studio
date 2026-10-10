import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { CatalogEditor } from "./catalog-editors"
import { EMPTY_CATALOG, type CatalogActions, type CatalogModel } from "./data"

function setup(item?: CatalogModel) {
  const save = vi.fn().mockResolvedValue(EMPTY_CATALOG)
  const actions = { createModel: save, updateModel: save } as unknown as CatalogActions
  render(<CatalogEditor kind="model" item={item} catalog={EMPTY_CATALOG} actions={actions} pending={false}
    mutate={async (operation) => { await operation() }} />)
  return { user: userEvent.setup(), save }
}

describe("model capability selection", () => {

  it("creates a model with multiple protocols and keeps at least one selected", async () => {
    const { user, save } = setup()
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    const chat = screen.getByRole("checkbox", { name: "OpenAI Chat" })
    expect(chat).toBeDisabled()
    await user.click(screen.getByRole("checkbox", { name: "OpenAI Responses" }))
    expect(chat).toBeEnabled()
    await user.type(screen.getByLabelText("模型名称"), "Multi API model")
    await user.click(screen.getByRole("button", { name: "保存模型" }))
    expect(save).toHaveBeenCalledWith({ name: "Multi API model", protocols: ["openai-chat", "openai-responses"], capabilities: [] })
  })
  it("saves a model with optional capabilities left empty", async () => {
    const { user, save } = setup()
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    await user.type(screen.getByLabelText("模型名称"), "First model")
    expect(screen.getByRole("combobox", { name: "模型能力（选填）" })).toHaveAccessibleDescription(/留空也可保存/)
    await user.click(screen.getByRole("button", { name: "保存模型" }))
    expect(save).toHaveBeenCalledWith({ name: "First model", protocols: ["openai-chat"], capabilities: [] })
  })

  it("searches labels and keys, adds multiple tags with the keyboard, and removes tags", async () => {
    const { user, save } = setup()
    await user.click(screen.getByRole("button", { name: "新增模型" }))
    await user.type(screen.getByLabelText("模型名称"), "Tagged model")
    const input = screen.getByRole("combobox", { name: "模型能力（选填）" })
    await user.type(input, "工具")
    await user.click(screen.getByRole("option", { name: "工具调用 (tools)" }))
    await user.type(input, "vision")
    await user.keyboard("{ArrowDown}{Enter}")
    expect(save).not.toHaveBeenCalled()
    await user.type(input, "  custom-tag  ")
    await user.click(screen.getByRole("option", { name: "添加自定义标签“custom-tag”" }))
    await user.type(input, "custom-tag")
    expect(screen.queryByRole("option", { name: "添加自定义标签“custom-tag”" })).not.toBeInTheDocument()
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "移除能力 tools" }))
    await user.click(screen.getByRole("button", { name: "保存模型" }))
    expect(save).toHaveBeenCalledWith({ name: "Tagged model", protocols: ["openai-chat"], capabilities: ["vision", "custom-tag"] })
  })

  it("preserves existing custom tags and allows removing every capability", async () => {
    const item = { id: "123e4567-e89b-42d3-a456-426614174020", revision: 2, name: "Existing", protocols: ["openai-chat"], capabilities: ["legacy-key"] } satisfies CatalogModel
    const { user, save } = setup(item)
    await user.click(screen.getByRole("button", { name: "编辑模型" }))
    await user.click(screen.getByRole("button", { name: "移除能力 legacy-key" }))
    await user.click(screen.getByRole("button", { name: "保存模型" }))
    expect(save).toHaveBeenCalledWith({ id: item.id, name: item.name, protocols: item.protocols, capabilities: [], expected_revision: 2 })
  })
})
