import { I18nextProvider } from "react-i18next"
import { createAppI18n } from "@/i18n/i18n"
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { CatalogEditor } from "./catalog-editors"
import { EMPTY_CATALOG, type CatalogActions } from "./data"

describe("current protocol catalog editors", () => {
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


  it("saves complete case inputs, body generators and empty assertions without connection fields", async () => {
    const user = userEvent.setup(); const item = FIXTURE_CATALOG.test_cases[0]
    const updateTestCase = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="case" item={item} catalog={FIXTURE_CATALOG} actions={{ updateTestCase } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit case" }))
    fireEvent.change(screen.getByLabelText("Assertions (JSON)"), { target: { value: "[]" } })
    expect(screen.queryByLabelText(/API key|Service URL|Applicable models/)).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Save case" }))
    await waitFor(() => expect(updateTestCase).toHaveBeenCalled())
    expect(updateTestCase.mock.calls[0][0]).toMatchObject({ type: "openai-chat", spec: { inputs: { prompt: { type: "string", default: "hello" } }, assertions: [] } })
    expect(updateTestCase.mock.calls[0][0]).not.toHaveProperty("model_targets")
  })
  it("keeps plan entry ids and parameters independent without authoring model/channel bindings", async () => {
    const user = userEvent.setup(); const updatePlan = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    const item = structuredClone(FIXTURE_CATALOG.plans[0]); item.entries.push({ ...item.entries[0], entry_id: crypto.randomUUID() }); item.entry_count = 2
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="plan" item={item} catalog={FIXTURE_CATALOG} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit plan" }))
    expect(screen.queryByLabelText(/Model bindings|Channel bindings/)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText("2. Input values (JSON)"), { target: { value: '{"prompt":"second"}' } })
    await user.click(screen.getAllByRole("button", { name: "Move entry up" })[1])
    await user.click(screen.getByRole("button", { name: "Save plan" }))
    await waitFor(() => expect(updatePlan).toHaveBeenCalled())
    const command = updatePlan.mock.calls[0][0]
    expect(command.entries[0]).toMatchObject({ entry_id: item.entries[1].entry_id, parameters: { prompt: "second" } })
    expect(command.entries[1].parameters).toEqual({}); expect(command).not.toHaveProperty("model_ids")
  })
})
