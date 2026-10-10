import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { CatalogEditor } from "./catalog-editors"
import type { CatalogActions } from "./data"

describe("per-channel model protocol editing", () => {
  it("edits the protocol subset together with the upstream model name", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.models[0].protocols = ["openai-chat", "openai-responses"]
    const item = catalog.channel_models[0]
    const save = vi.fn().mockResolvedValue(catalog)
    render(<CatalogEditor kind="mapping" item={item} catalog={catalog}
      actions={{ updateChannelModel: save } as unknown as CatalogActions} pending={false}
      mutate={async operation => { await operation() }} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "编辑映射" }))
    expect(screen.getByRole("checkbox", { name: "OpenAI Chat" })).toBeDisabled()
    expect(screen.queryByRole("checkbox", { name: "Anthropic Messages" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("checkbox", { name: "OpenAI Responses" }))
    await user.click(screen.getByRole("checkbox", { name: "OpenAI Chat" }))
    const name = screen.getByLabelText("上游模型名称")
    await user.clear(name)
    await user.type(name, "responses-upstream")
    await user.click(screen.getByRole("button", { name: "保存映射" }))
    expect(save).toHaveBeenCalledWith({ id: item.id, expected_revision: item.revision,
      upstream_model_name: "responses-upstream", protocols: ["openai-responses"] })
  })

  it("keeps protocols used by channel mappings selected in the model editor", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.models[0].protocols = ["openai-chat", "openai-responses"]
    render(<CatalogEditor kind="model" item={catalog.models[0]} catalog={catalog}
      actions={{} as CatalogActions} pending={false} mutate={vi.fn()} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "编辑模型" }))
    expect(screen.getByRole("checkbox", { name: "OpenAI Chat" })).toBeDisabled()
    expect(screen.getByRole("checkbox", { name: "OpenAI Responses" })).toBeEnabled()
  })
})
