import { I18nextProvider } from "react-i18next"
import { createAppI18n } from "@/i18n/i18n"
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { FIXTURE_CATALOG } from "@/test/workspace-fixtures"
import { CatalogEditor } from "./catalog-editors"
import { EMPTY_CATALOG, type CatalogActions } from "./data"
import { performanceFormFromProfile, performanceProfileFromForm } from "@/features/quick-test/performance-profile"

describe("current protocol catalog editors", () => {
  it("explains selected cases and suites and updates the context when changing targets", async () => {
    const user = userEvent.setup()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.suites[1].description = ""
    catalog.test_cases[0].description = "Checks message responses with a configured model."
    delete catalog.test_cases[1].description
    const item = catalog.plans[0]
    const updatePlan = vi.fn(async (_command: Record<string, any>) => catalog)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="plan" item={item} catalog={catalog} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit plan" }))
    const target = screen.getByRole("combobox", { name: "Case or suite" })
    expect(target).toHaveAccessibleDescription(`Suite description: ${catalog.suites[0].description}`)
    await user.click(target)
    await user.click(screen.getByRole("option", { name: `suite · ${catalog.suites[1].name}` }))
    expect(target).toHaveAccessibleDescription("Suite description: No suite description provided")
    expect(screen.getByRole("dialog", { name: "Edit plan" })).toBeInTheDocument()

    await user.click(target)
    await user.click(screen.getByRole("option", { name: `case · ${catalog.test_cases[0].name}` }))
    expect(target).toHaveAccessibleDescription(`Case description: ${catalog.test_cases[0].description}`)
    expect(screen.queryByText("Suite description: No suite description provided")).not.toBeInTheDocument()
    await user.click(target)
    await user.click(screen.getByRole("option", { name: `case · ${catalog.test_cases[1].name}` }))
    expect(target).toHaveAccessibleDescription("Case description: No case description provided")
    await user.click(screen.getByRole("button", { name: "Save plan" }))
    await waitFor(() => expect(updatePlan).toHaveBeenCalledWith(expect.objectContaining({ entries: [expect.objectContaining({ target_kind: "case", target_id: catalog.test_cases[1].id })] })))
  })

  it("edits multiline case descriptions, preserves them after failure, and omits cleared text", async () => {
    const user = userEvent.setup()
    const item = { ...FIXTURE_CATALOG.test_cases[0], description: "Original explanation" }
    const updateTestCase = vi.fn<(command: Record<string, unknown>) => Promise<typeof FIXTURE_CATALOG>>()
      .mockRejectedValueOnce(new Error("save failed"))
      .mockResolvedValue(FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="case" item={item} catalog={FIXTURE_CATALOG} actions={{ updateTestCase } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit case" }))
    const explanation = screen.getByRole("textbox", { name: "Case description (optional)" })
    expect(explanation.tagName).toBe("TEXTAREA")
    expect(explanation).not.toHaveClass("font-mono")
    expect(explanation).toHaveValue(item.description)
    expect(explanation).toHaveAccessibleDescription(/purpose, prerequisites, pass criteria, and coverage limits/)
    const description = "Purpose: Check message responses.\nPrerequisites: A configured model.\nPass criteria: All assertions pass.\nCoverage limits: One request."
    fireEvent.change(explanation, { target: { value: `  ${description}  ` } })
    await user.click(screen.getByRole("button", { name: "Save case" }))
    await waitFor(() => expect(updateTestCase).toHaveBeenCalledOnce())
    expect(updateTestCase.mock.calls[0][0]).toMatchObject({ description })
    await waitFor(() => expect(screen.getByRole("dialog", { name: "Edit case" })).toHaveTextContent("Save did not complete"))
    expect(explanation).toHaveValue(`  ${description}  `)
    fireEvent.change(explanation, { target: { value: " \n " } })
    await user.click(screen.getByRole("button", { name: "Save case" }))
    await waitFor(() => expect(updateTestCase).toHaveBeenCalledTimes(2))
    expect(updateTestCase.mock.calls[1][0]).not.toHaveProperty("description")
  })

  it("creates a case with a description while preserving protocol drafts", async () => {
    const user = userEvent.setup()
    const createTestCase = vi.fn(async (_command: Record<string, unknown>) => FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="case" catalog={FIXTURE_CATALOG} actions={{ createTestCase } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Add case" }))
    fireEvent.change(screen.getByLabelText("Case key"), { target: { value: "custom-case" } })
    fireEvent.change(screen.getByLabelText("Case name"), { target: { value: "Custom case" } })
    fireEvent.change(screen.getByLabelText("Case description (optional)"), { target: { value: "  Check the configured response assertions.  " } })
    const bodyLabel = "Request body / Generator (JSON)"
    fireEvent.change(screen.getByLabelText(bodyLabel), { target: { value: '{"messages":[{"role":"user","content":"draft"}]}' } })
    await user.click(screen.getByRole("checkbox", { name: "OpenAI Responses" }))
    fireEvent.change(screen.getByLabelText(bodyLabel), { target: { value: '{"input":"response draft"}' } })
    expect(screen.getByLabelText("Case description (optional)")).toHaveValue("  Check the configured response assertions.  ")
    await user.click(screen.getByRole("button", { name: "Save case" }))
    await waitFor(() => expect(createTestCase).toHaveBeenCalledOnce())
    expect(createTestCase.mock.calls[0][0]).toMatchObject({ description: "Check the configured response assertions.", definitions: {
      "openai-chat": { request: { body: { messages: [{ role: "user", content: "draft" }] } } },
      "openai-responses": { request: { body: { input: "response draft" } } },
    } })
  })

  it("shows case explanations when choosing suite members and edits a multiline suite explanation", async () => {
    const user = userEvent.setup()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.test_cases[0].description = "Purpose: Check a message response.\nCoverage limits: One response."
    delete catalog.test_cases[1].description
    const item = catalog.suites[0]
    const updateSuite = vi.fn(async (_command: Record<string, unknown>) => catalog)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="suite" item={item} catalog={catalog} actions={{ updateSuite } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit suite" }))
    expect(screen.getByRole("checkbox", { name: catalog.test_cases[0].name })).toHaveAccessibleDescription(catalog.test_cases[0].description)
    expect(screen.getByRole("checkbox", { name: catalog.test_cases[1].name })).toHaveAccessibleDescription("No case description provided")
    const explanation = screen.getByRole("textbox", { name: "Description" })
    expect(explanation.tagName).toBe("TEXTAREA")
    expect(explanation).not.toHaveClass("font-mono")
    const description = "Purpose: Check message behavior.\nCoverage limits: Configured suite members."
    fireEvent.change(explanation, { target: { value: description } })
    await user.click(screen.getByRole("button", { name: "Save suite" }))
    await waitFor(() => expect(updateSuite).toHaveBeenCalledWith(expect.objectContaining({ description, cases: item.cases })))
  })

  it("preserves mixed items, failure policies and independent duplicated pressure profiles", async () => {
    const user = userEvent.setup()
    const updatePlan = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    const item = structuredClone(FIXTURE_CATALOG.plans[0])
    item.entries[0].name = "K3 basic"
    item.entries[0].on_failure = "stop"
    const pressure = structuredClone(FIXTURE_CATALOG.plans.find(plan => plan.builtin)!.entries[0])
    pressure.entry_id = crypto.randomUUID(); pressure.target_id = pressure.entry_id
    pressure.name = "Pressure A"; pressure.performance!.request_count = 20
    item.entries.push(pressure); item.entry_count = 2
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="plan" item={item} catalog={FIXTURE_CATALOG} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit plan" }))
    await user.click(screen.getByRole("button", { name: /^2\./ }))
    await user.click(screen.getAllByRole("button", { name: "Duplicate item" })[1])
    fireEvent.change(screen.getByLabelText("Item name"), { target: { value: "Pressure B" } })
    fireEvent.change(screen.getByLabelText("Request count"), { target: { value: "73" } })
    await user.click(screen.getByRole("button", { name: "Save plan" }))
    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    const entries = updatePlan.mock.calls[0][0].entries
    expect(entries).toHaveLength(3)
    expect(entries[0]).toMatchObject({ entry_id: item.entries[0].entry_id, on_failure: "stop" })
    expect(entries[1]).toMatchObject({ entry_id: pressure.entry_id, performance: { request_count: 20 } })
    expect(entries[2]).toMatchObject({ name: "Pressure B", on_failure: "continue", performance: { request_count: 73 } })
    expect(entries[2]).not.toHaveProperty("entry_id")
  })
  it("preserves custom performance parameters when editing a plan", async () => {
    const user = userEvent.setup()
    const performance = { ...performanceProfileFromForm(performanceFormFromProfile()), request_count: 742, concurrency: 17, input_tokens: 2048, random_input: true, warmup_requests: 3, slo_e2e_ms: 500, slo_target_percent: 95 }
    const item = { ...FIXTURE_CATALOG.plans[0], entries: [], entry_count: 0, case_count: 0, performance }
    const updatePlan = vi.fn(async (_command: Record<string, unknown>) => FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CatalogEditor kind="plan" item={item} catalog={FIXTURE_CATALOG} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "编辑计划" }))
    expect(screen.getByLabelText("请求数")).toHaveValue(742)
    expect(screen.getByLabelText("并发数")).toHaveValue(17)
    expect(screen.queryByLabelText("性能预设")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "保存计划" }))
    await waitFor(() => expect(updatePlan).toHaveBeenCalledWith(expect.objectContaining({ entries: [expect.objectContaining({ performance })] })))
  })

  it("creates a performance plan with the full parameter form and rejects an empty target", async () => {
    const user = userEvent.setup()
    const createPlan = vi.fn(async (_command: Record<string, unknown>) => FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CatalogEditor kind="plan" catalog={FIXTURE_CATALOG} actions={{ createPlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "新增计划" }))
    fireEvent.change(screen.getByLabelText("计划"), { target: { value: "Custom performance" } })
    await user.click(screen.getByRole("button", {name:"添加计划项"}))
    fireEvent.change(screen.getByLabelText("计划项名称"), {target:{value:"压力一"}})
    await user.click(screen.getByRole("combobox", { name: "类别" }))
    await user.click(screen.getByRole("option", { name: "快速性能测试" }))
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "0" } })
    await user.click(screen.getByRole("button", { name: "保存计划" }))
    expect(screen.getByLabelText("请求数")).toHaveAttribute("aria-invalid", "true")
    expect(createPlan).not.toHaveBeenCalled()
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "73" } })
    fireEvent.change(screen.getByLabelText("并发数"), { target: { value: "11" } })
    await user.click(screen.getByRole("button", { name: "保存计划" }))
    await waitFor(() => expect(createPlan).toHaveBeenCalledWith(expect.objectContaining({ entries: [expect.objectContaining({performance: expect.objectContaining({ request_count: 73, concurrency: 11 })})] })))
  })

  it("saves an adjusted built-in plan as a new plan", async () => {
    const user = userEvent.setup()
    const item = FIXTURE_CATALOG.plans.find(plan => plan.builtin)!
    const createPlan = vi.fn(async (_command: Record<string, unknown>) => FIXTURE_CATALOG)
    const updatePlan = vi.fn()
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CatalogEditor kind="plan" item={item} catalog={FIXTURE_CATALOG} actions={{ createPlan, updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "另存为计划" }))
    fireEvent.change(screen.getByLabelText("请求数"), { target: { value: "31" } })
    await user.click(screen.getByRole("button", { name: "另存为计划" }))
    await waitFor(() => expect(createPlan).toHaveBeenCalledWith(expect.objectContaining({ entries: [expect.objectContaining({performance: expect.objectContaining({ request_count: 31 })})] })))
    expect(updatePlan).not.toHaveBeenCalled()
  })
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
    expect(updateTestCase.mock.calls[0][0]).toMatchObject({ definitions: { "openai-chat": { inputs: { prompt: { type: "string", default: "hello" } }, assertions: [] } } })
    expect(updateTestCase.mock.calls[0][0]).not.toHaveProperty("model_targets")
  })
  it("keeps independent protocol drafts while saving one Case", async () => {
    const user = userEvent.setup()
    const item = structuredClone(FIXTURE_CATALOG.test_cases[0])
    const updateTestCase = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="case" item={item} catalog={FIXTURE_CATALOG} actions={{ updateTestCase } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit case" }))
    const bodyLabel = "Request body / Generator (JSON)"
    fireEvent.change(screen.getByLabelText(bodyLabel), { target: { value: '{"messages":[' } })
    await user.click(screen.getByRole("checkbox", { name: "OpenAI Responses" }))
    fireEvent.change(screen.getByLabelText(bodyLabel), { target: { value: '{"input":"native response"}' } })
    await user.click(screen.getByRole("combobox", { name: "Edit protocol" }))
    await user.click(screen.getByRole("option", { name: "OpenAI Chat" }))
    expect(screen.getByLabelText(bodyLabel)).toHaveValue('{"messages":[')
    fireEvent.change(screen.getByLabelText(bodyLabel), { target: { value: '{"messages":[{"role":"user","content":"chat draft"}]}' } })
    await user.click(screen.getByRole("button", { name: "Save case" }))
    await waitFor(() => expect(updateTestCase).toHaveBeenCalledOnce())
    expect(updateTestCase.mock.calls[0][0]).toMatchObject({ id: item.id, definitions: {
      "openai-chat": { request: { body: { messages: [{ role: "user", content: "chat draft" }] } } },
      "openai-responses": { request: { body: { input: "native response" } } },
    } })
  })

  it("keeps plan entry ids and parameters independent without authoring model/channel bindings", async () => {
    const user = userEvent.setup(); const updatePlan = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    const item = structuredClone(FIXTURE_CATALOG.plans[0]); item.entries.push({ ...item.entries[0], entry_id: crypto.randomUUID() }); item.entry_count = 2
    render(<I18nextProvider i18n={createAppI18n("en-US")}><CatalogEditor kind="plan" item={item} catalog={FIXTURE_CATALOG} actions={{ updatePlan } as unknown as CatalogActions} pending={false} mutate={async operation => { await operation() }} /></I18nextProvider>)
    await user.click(screen.getByRole("button", { name: "Edit plan" }))
    expect(screen.queryByLabelText(/Model bindings|Channel bindings/)).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", {name:/^2\./}))
    fireEvent.change(screen.getByLabelText("2. Input values (JSON)"), { target: { value: '{"prompt":"second"}' } })
    await user.click(screen.getAllByRole("button", { name: "Move entry up" })[1])
    await user.click(screen.getByRole("button", { name: "Save plan" }))
    await waitFor(() => expect(updatePlan).toHaveBeenCalled())
    const command = updatePlan.mock.calls[0][0]
    expect(command.entries[0]).toMatchObject({ entry_id: item.entries[1].entry_id, parameters: { prompt: "second" } })
    expect(command.entries[1].parameters).toEqual({}); expect(command).not.toHaveProperty("model_ids")
  })

  it("explains how to add a required plan entry before submitting", async () => {
    const user = userEvent.setup()
    const createPlan = vi.fn(async (_command: Record<string, any>) => FIXTURE_CATALOG)
    const mutate = vi.fn(async (operation: () => Promise<unknown>) => { await operation() })
    render(<I18nextProvider i18n={createAppI18n("zh-CN")}><CatalogEditor kind="plan" catalog={FIXTURE_CATALOG} actions={{ createPlan } as unknown as CatalogActions} pending={false} mutate={mutate} /></I18nextProvider>)

    await user.click(screen.getByRole("button", { name: "新增计划" }))
    await user.type(screen.getByLabelText("计划"), "缺少执行项的计划")
    await user.click(screen.getByRole("button", { name: "保存计划" }))
    expect(mutate).not.toHaveBeenCalled()
    expect(createPlan).not.toHaveBeenCalled()
    await user.click(screen.getByRole("button", { name: "添加计划项" }))
    expect(screen.getByRole("combobox", {name:"类别"})).toBeVisible()
    expect(screen.getByRole("combobox", {name:"失败后"})).toBeVisible()

  })
})
