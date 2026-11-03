import { act, fireEvent, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { getI18n } from "react-i18next"
import { describe, expect, it, vi } from "vitest"

import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { CasesWorkspace, ModelChannelWorkspace, PlansWorkspace } from "./catalog-workspaces"
import { EMPTY_CATALOG, type CatalogActions } from "./data"

const props = {
  catalog: FIXTURE_CATALOG,
  actions: {} as CatalogActions,
  mutate: async (operation: () => Promise<unknown>) => { await operation() },
  mutationPending: false,
  mutationError: "",
}

describe("Catalog table search", () => {
  it("shows case dimensions as a column and searches custom dimensions", async () => {
    const user = userEvent.setup()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.test_cases[0].dimension = "custom-dimension"
    render(<CasesWorkspace {...props} catalog={catalog} />)
    const table = screen.getByRole("table", { name: "测试用例目录" })
    expect(within(table).getByRole("columnheader", { name: "维度" })).toBeVisible()
    expect(within(table).getByRole("cell", { name: "custom-dimension" })).toBeVisible()
    await user.type(screen.getByRole("searchbox", { name: "搜索用例" }), "custom-dimension")
    expect(within(table).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(catalog.test_cases[0].name)
  })
  it("applies repeated case and suite drilldowns without clearing their search or selection", async () => {
    const user = userEvent.setup()
    const view = render(<CasesWorkspace {...props} navigationIntent={{ sequence: 1, tab: "suites" }} />)
    expect(screen.getByRole("tab", { name: /套件/ })).toHaveAttribute("aria-selected", "true")
    await user.type(screen.getByRole("searchbox", { name: "搜索套件" }), "openai")
    await user.click(screen.getByRole("button", { name: `查看套件 ${FIXTURE_CATALOG.suites[1].name}` }))
    await user.click(screen.getByRole("tab", { name: /用例 4/ }))
    await user.type(screen.getByRole("searchbox", { name: "搜索用例" }), "T037")
    view.rerender(<CasesWorkspace {...props} navigationIntent={{ sequence: 2, tab: "suites" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索套件" })).toHaveValue("openai")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(FIXTURE_CATALOG.suites[1].name)
    await user.click(screen.getByRole("tab", { name: /用例 4/ }))
    view.rerender(<CasesWorkspace {...props} navigationIntent={{ sequence: 2, tab: "suites" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索用例" })).toHaveValue("T037")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent("工具调用")
    view.rerender(<CasesWorkspace {...props} navigationIntent={{ sequence: 3, tab: "suites" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索套件" })).toHaveValue("openai")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(FIXTURE_CATALOG.suites[1].name)
  })

  it("applies model and channel drilldowns once per intent while preserving tab context", async () => {
    const user = userEvent.setup()
    const view = render(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 1, tab: "channels" }} />)
    await user.type(screen.getByRole("searchbox", { name: "搜索渠道" }), "https")
    await user.click(screen.getByRole("button", { name: `查看渠道 ${FIXTURE_CATALOG.channels[1].name}` }))
    await user.click(screen.getByRole("tab", { name: /^模型/ }))
    await user.type(screen.getByRole("searchbox", { name: "搜索模型" }), "QWEN3")
    view.rerender(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 2, tab: "channels" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索渠道" })).toHaveValue("https")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(FIXTURE_CATALOG.channels[1].name)
    await user.click(screen.getByRole("tab", { name: /^模型/ }))
    view.rerender(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 2, tab: "channels" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索模型" })).toHaveValue("QWEN3")
    view.rerender(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 3, tab: "mappings" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索映射" })).toBeInTheDocument()
    view.rerender(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 4, tab: "matrix" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索模型渠道矩阵" })).toBeInTheDocument()
    view.rerender(<ModelChannelWorkspace {...props} navigationIntent={{ sequence: 5, tab: "channels" }} />)
    expect(screen.getByRole("searchbox", { name: "搜索渠道" })).toHaveValue("https")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(FIXTURE_CATALOG.channels[1].name)
  })

  it("shows and searches case explanations with a fallback for missing descriptions", async () => {
    const user = userEvent.setup()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.test_cases[0].description = "目的：验证消息响应。\n限制：单次请求，不代表持续容量。"
    delete catalog.test_cases[1].description
    render(<CasesWorkspace {...props} catalog={catalog} />)
    const table = screen.getByRole("table", { name: "测试用例目录" })
    expect(within(table).getByText(/目的：验证消息响应。/)).toHaveClass("whitespace-pre-wrap")
    expect(within(table).getAllByText("暂无用例说明").length).toBeGreaterThan(0)
    expect(screen.getByRole("complementary", { name: "用例详情" })).toHaveTextContent("不代表持续容量")
    await user.type(screen.getByRole("searchbox", { name: "搜索用例" }), "持续容量")
    expect(within(table).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(catalog.test_cases[0].name)
    await user.click(screen.getByRole("button", { name: "清空搜索" }))
    await user.click(screen.getByRole("button", { name: `查看用例 ${catalog.test_cases[1].name}` }))
    expect(screen.getByRole("complementary", { name: "用例详情" })).toHaveTextContent("暂无用例说明")
  })

  it("finds case keys, translated types, protocols, and suite references without searching request payloads", async () => {
    const user = userEvent.setup()
    render(<CasesWorkspace {...props} />)
    const input = screen.getByRole("searchbox", { name: "搜索用例" })
    expect(input.closest('[data-slot="scroll-area-viewport"]')).toBeNull()

    await user.type(input, "  t008  ")
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("complementary", { name: "用例详情" })).toHaveTextContent("流式结束")
    expect(screen.getByRole("status")).toHaveTextContent("1 / 4 项")

    fireEvent.change(input, { target: { value: "T037" } })
    expect(within(screen.getByRole("table")).getByText("工具调用")).toBeInTheDocument()
    fireEvent.change(input, { target: { value: "OpenAI Chat" } })
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(5)
    fireEvent.change(input, { target: { value: "OPENAI-CHAT 工具" } })
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)

    fireEvent.change(input, { target: { value: "hello" } })
    expect(screen.getByText("未找到匹配项")).toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "用例详情" })).toHaveTextContent("尚未选择用例")
    expect(screen.queryByRole("button", { name: "编辑用例" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "删除用例" })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "显示全部" }))
    expect(input).toHaveValue("")
    expect(input).toHaveFocus()
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(5)
  })

  it("keeps searches independent across case and suite tabs and clears with Escape or the clear button", async () => {
    const user = userEvent.setup()
    render(<CasesWorkspace {...props} />)
    await user.type(screen.getByRole("searchbox"), "T037")
    await user.click(screen.getByRole("tab", { name: /套件/ }))
    const suites = screen.getByRole("searchbox", { name: "搜索套件" })
    expect(suites).toHaveValue("")
    await user.type(suites, "openai-regression")
    expect(within(screen.getByRole("table")).getByText("OpenAI 回归套件")).toBeInTheDocument()
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    fireEvent.change(suites, { target: { value: "openai-connectivity" } })
    expect(screen.getByRole("complementary", { name: "套件详情" })).toHaveTextContent("OpenAI Chat 连通性测试")
    await user.click(screen.getByRole("tab", { name: /用例 4/ }))
    const cases = screen.getByRole("searchbox", { name: "搜索用例" })
    expect(cases).toHaveValue("T037")
    await user.click(cases)
    await user.keyboard("{Escape}")
    expect(cases).toHaveValue("")
    expect(cases).toHaveFocus()
    await user.click(screen.getByRole("tab", { name: /套件/ }))
    expect(screen.getByRole("searchbox")).toHaveValue("openai-connectivity")
    await user.click(screen.getByRole("button", { name: "清空搜索" }))
    expect(screen.getByRole("searchbox")).toHaveFocus()
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(3)
  })

  it("shows and searches suite descriptions while keeping the selected details in sync", async () => {
    const user = userEvent.setup()
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.suites[0].description = "  "
    catalog.suites[1].description = "验证消息响应，并检查协议连通性。"
    render(<CasesWorkspace {...props} catalog={catalog} />)
    const caseDetails = screen.getByRole("complementary", { name: "用例详情" })
    expect(within(caseDetails).getByText("支持的协议")).toBeInTheDocument()
    expect(within(caseDetails).queryByText("适用模型")).not.toBeInTheDocument()

    await user.click(screen.getByRole("tab", { name: /套件/ }))
    const table = screen.getByRole("table", { name: "测试套件目录" })
    expect(within(table).getByText("验证消息响应，并检查协议连通性。")).toBeInTheDocument()
    expect(within(table).getByText("暂无套件说明")).toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "套件详情" })).toHaveTextContent("暂无套件说明")

    await user.type(screen.getByRole("searchbox", { name: "搜索套件" }), "消息响应")
    expect(within(table).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(catalog.suites[1].name)
    expect(screen.getByRole("complementary", { name: "套件详情" })).toHaveTextContent(catalog.suites[1].description)
    await user.click(screen.getByRole("button", { name: "清空搜索" }))
    const firstSuite = screen.getByRole("button", { name: `查看套件 ${catalog.suites[0].name}` })
    firstSuite.focus()
    await user.keyboard("{Enter}")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent(catalog.suites[0].name)
    expect(screen.getByRole("complementary", { name: "套件详情" })).toHaveTextContent("暂无套件说明")
  })

  it("updates visible selection when a catalog refresh removes the matching case", async () => {
    const user = userEvent.setup()
    const view = render(<CasesWorkspace {...props} />)
    await user.click(screen.getByRole("button", { name: "查看用例 流式结束" }))
    await user.type(screen.getByRole("searchbox"), "OpenAI")
    expect(screen.getByRole("row", { selected: true })).toHaveTextContent("流式结束")
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "t008" } })
    view.rerender(<CasesWorkspace {...props} catalog={{ ...FIXTURE_CATALOG, test_cases: FIXTURE_CATALOG.test_cases.filter((item) => item.key !== "T008") }} />)
    expect(screen.getByRole("searchbox")).toHaveValue("t008")
    expect(screen.getByText("未找到匹配项")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "删除用例" })).not.toBeInTheDocument()
  })

  it("searches model capabilities, channel URLs, mapping names, and matrix rows", async () => {
    const user = userEvent.setup()
    render(<ModelChannelWorkspace {...props} />)
    await user.type(screen.getByRole("searchbox", { name: "搜索模型" }), "MULTIMODAL")
    expect(within(screen.getByRole("table")).getByText("gemini-2.5-pro")).toBeInTheDocument()
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    await user.click(screen.getByRole("tab", { name: /^渠道/ }))
    await user.type(screen.getByRole("searchbox", { name: "搜索渠道" }), "dashscope.aliyuncs.com")
    expect(screen.getByRole("complementary", { name: "渠道详情" })).toHaveTextContent("阿里云备用渠道")
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    await user.click(screen.getByRole("tab", { name: /^映射/ }))
    await user.type(screen.getByRole("searchbox", { name: "搜索映射" }), "阿里云 QWEN3")
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("complementary", { name: "映射详情" })).toHaveTextContent("qwen3-max")
    await user.click(screen.getByRole("tab", { name: "矩阵" }))
    await user.type(screen.getByRole("searchbox", { name: "搜索模型渠道矩阵" }), "阿里云")
    const matrix = screen.getByRole("table", { name: "模型渠道配置矩阵" })
    expect(within(matrix).getAllByRole("rowheader")).toHaveLength(1)
    expect(within(matrix).getByRole("rowheader")).toHaveTextContent("qwen3-max")
    expect(within(matrix).getAllByRole("columnheader")).toHaveLength(FIXTURE_CATALOG.channels.length + 1)
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "missing-mapping" } })
    expect(screen.queryByRole("button", { name: "删除映射" })).not.toBeInTheDocument()
    expect(screen.getByRole("complementary", { name: "映射详情" })).toHaveTextContent("尚未选择对象")
    await user.click(screen.getByRole("tab", { name: /^模型/ }))
    expect(screen.getByRole("searchbox")).toHaveValue("MULTIMODAL")
  })

  it("runs only the visible plan selected by protocol and referenced suite fields", async () => {
    const user = userEvent.setup()
    const onStartPlan = vi.fn(async () => {})
    render(<PlansWorkspace {...props} commandPending={false} onStartPlan={onStartPlan} />)
    await user.type(screen.getByRole("searchbox", { name: "搜索计划" }), "JSON openai-regression")
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2)
    expect(screen.getByRole("complementary", { name: "计划详情" })).toHaveTextContent("JSON 模式回归")
    await user.click(screen.getByRole("button", { name: "运行这个计划" }))
    expect(onStartPlan).not.toHaveBeenCalled()
    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    await user.click(within(dialog).getByRole("button", { name: "开始运行" }))
    expect(onStartPlan).toHaveBeenCalledWith(expect.objectContaining({ plan_id: FIXTURE_CATALOG.plans[1].id }))
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "missing-plan" } })
    expect(screen.queryByRole("button", { name: "运行这个计划" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "删除计划" })).not.toBeInTheDocument()
  })

  it("keeps the original empty catalog guidance", async () => {
    const user = userEvent.setup()
    render(<CasesWorkspace {...props} catalog={EMPTY_CATALOG} />)
    await user.type(screen.getByRole("searchbox"), "T044")
    expect(screen.queryByText("未找到匹配项")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "新增用例" })).toBeEnabled()
    expect(screen.getByRole("searchbox")).toHaveValue("T044")
  })

  it("localizes search and matches the current translated case type", async () => {
    const user = userEvent.setup()
    await act(async () => { await getI18n().changeLanguage("en-US") })
    try {
      render(<CasesWorkspace {...props} />)
      await user.type(screen.getByRole("searchbox", { name: "Search cases" }), "OpenAI Chat")
      expect(screen.getByRole("status")).toHaveTextContent("4 / 4 items")
      fireEvent.change(screen.getByRole("searchbox"), { target: { value: "unmatched" } })
      expect(screen.getByText("No matches found")).toBeInTheDocument()
      expect(screen.getByRole("button", { name: "Clear search" })).toBeEnabled()
    } finally {
      await act(async () => { await getI18n().changeLanguage("zh-CN") })
    }
  })
})
