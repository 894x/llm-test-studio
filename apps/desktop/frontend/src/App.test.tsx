import { fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { readFileSync } from "node:fs"
import { resolve } from "node:path"

import App from "./App"
import type { DesktopClient } from "./app/desktop-client"
import { FIXTURE_WORKSPACE } from "./features/runs/fixtures"
import type { WorkspaceSnapshot } from "./features/runs/data"
import indexHtml from "../index.html?raw"

const indexCss = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8")

function desktopClient(): DesktopClient & {
  workspace: WorkspaceSnapshot
} {
  const client = {
    workspace: structuredClone(FIXTURE_WORKSPACE),
    getWorkspace: vi.fn(async () => structuredClone(client.workspace)),
    startRun: vi.fn(async () => structuredClone(client.workspace)),
    stopSending: vi.fn(async (runId: string) => {
      client.workspace = {
        ...client.workspace,
        runs: client.workspace.runs.map((run) =>
          run.id === runId ? { ...run, status: "draining" as const } : run,
        ),
      }
      return structuredClone(client.workspace)
    }),
    cancelRun: vi.fn(async (runId: string) => {
      client.workspace = {
        ...client.workspace,
        active_run_id: undefined,
        runs: client.workspace.runs.map((run) =>
          run.id === runId ? { ...run, status: "cancelled" as const } : run,
        ),
      }
      return structuredClone(client.workspace)
    }),
  }
  return client
}

describe("desktop run workspace", () => {
  beforeEach(() => {
    window.localStorage.clear()
    document.documentElement.className = ""
  })

  it("opens on the compact run workspace instead of a dashboard", async () => {
    render(<App client={desktopClient()} />)

    expect(
      await screen.findByRole("heading", { name: "运行工作区" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("navigation", { name: "主导航" })).toHaveTextContent(
      "运行",
    )
    expect(
      screen.getByRole("navigation", { name: "测试计划" }),
    ).toBeInTheDocument()
    expect(screen.getByRole("table", { name: "运行记录" })).toBeInTheDocument()
    expect(
      screen.getByRole("complementary", { name: "运行详情" }),
    ).toHaveTextContent("营销文案基准")
    expect(screen.queryByRole("button", { name: /更多操作/ })).not.toBeInTheDocument()

    const table = screen.getByRole("table", { name: "运行记录" })
    expect(table.closest('[data-slot="scroll-area-viewport"]')).not.toBeNull()
    expect(table.closest('[data-slot="table-container"]')).toBeNull()
    expect(
      document.querySelectorAll('[data-slot="scroll-area-scrollbar"]'),
    ).toHaveLength(2)
    const verticalRail = document.querySelector(
      '[data-slot="scroll-area-scrollbar"][data-orientation="vertical"]',
    )
    const horizontalRail = document.querySelector(
      '[data-slot="scroll-area-scrollbar"][data-orientation="horizontal"]',
    )
    expect(verticalRail).toHaveClass("w-[5px]")
    expect(horizontalRail).toHaveClass("h-[5px]")
    expect(verticalRail).toHaveStyle({ top: "4px", right: "4px", bottom: "4px" })
    expect(horizontalRail).toHaveStyle({ left: "4px", right: "4px", bottom: "4px" })

    const definitionRows = document.querySelectorAll(
      '[data-slot="inspector-definition-row"]',
    )
    expect(definitionRows).toHaveLength(6)
    definitionRows.forEach((row) => {
      expect(row.children).toHaveLength(2)
      expect(row.querySelector("dd")?.children).toHaveLength(0)
    })
  })

  it("keeps table selection and the inspector on the same run", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    await user.click(await screen.findByRole("button", { name: "查看 JSON 模式回归" }))

    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("JSON 模式回归")
    expect(inspector).toHaveTextContent("55555555-5555-4555-8555-555555555552")
  })

  it("opens an explicitly titled new-run sheet", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    await user.click(await screen.findByRole("button", { name: "新建运行" }))

    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    expect(dialog).toBeInTheDocument()
    expect(within(dialog).getByText("选择测试计划")).toBeInTheDocument()
    expect(
      within(dialog).getByRole("button", { name: "开始运行" }),
    ).toBeEnabled()

    const radios = within(dialog).getAllByRole("radio")
    expect(radios.filter((radio) => radio.tabIndex === 0)).toHaveLength(1)
    expect(radios[0]).toBeChecked()
    radios[0].focus()
    fireEvent.keyDown(radios[0], { key: "ArrowDown" })
    await waitFor(() => expect(radios[1]).toBeChecked())
    fireEvent.keyUp(radios[1], { key: "ArrowDown" })

    await user.click(within(dialog).getByRole("button", { name: "开始运行" }))
    expect(client.startRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.plans[1].id)
  })

  it("treats stop-sending and cancel as separate lifecycle actions", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    render(<App client={client} />)

    expect(await screen.findByText("发送中", { selector: "[data-task-state]" })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "停止发送" }))
    expect(screen.getByText("排空中", { selector: "[data-task-state]" })).toBeInTheDocument()
    expect(client.stopSending).toHaveBeenCalledWith(FIXTURE_WORKSPACE.active_run_id)
    expect(screen.getByRole("button", { name: "取消运行" })).toBeEnabled()

    await user.click(screen.getByRole("button", { name: "取消运行" }))
    expect(client.cancelRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.active_run_id)
    expect(screen.getByText("无活动运行")).toBeInTheDocument()
  })

  it("persists only the versioned theme preference", async () => {
    const user = userEvent.setup()
    render(<App client={desktopClient()} />)

    const systemTheme = await screen.findByRole("button", { name: "主题：跟随系统" })
    expect(systemTheme.querySelector(".lucide-contrast")).not.toBeNull()
    await user.click(systemTheme)
    await user.click(screen.getByRole("menuitemradio", { name: "深色" }))

    expect(document.documentElement).toHaveClass("dark")
    expect(document.documentElement).toHaveAttribute("data-theme", "dark")
    expect(document.documentElement.style.colorScheme).toBe("dark")
    expect(
      JSON.parse(
        window.localStorage.getItem("llm-test:ui-preferences:v1") ?? "null",
      ),
    ).toEqual({ version: 1, theme: "dark" })
    expect(window.localStorage).toHaveLength(1)
  })

  it("surfaces a missing production bridge instead of substituting fixtures", async () => {
    const client: DesktopClient = {
      getWorkspace: vi.fn(async () => {
        throw new Error("sk-secret from https://provider.example/v1")
      }),
      startRun: vi.fn(),
      stopSending: vi.fn(),
      cancelRun: vi.fn(),
    }

    render(<App client={client} />)

    const alert = await screen.findByRole("alert")
    expect(alert).toHaveTextContent("无法读取本地工作区")
    expect(alert).not.toHaveTextContent("sk-secret")
    expect(alert).not.toHaveTextContent("provider.example")
    expect(screen.queryByText("营销文案基准")).not.toBeInTheDocument()
  })

  it("uses the Core conclusion and never infers pass from request counts", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[2], { conclusion: "none" })

    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 多轮工具调用",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText("已完成")).toBeInTheDocument()
    expect(within(row as HTMLTableRowElement).queryByText("通过")).not.toBeInTheDocument()
  })

  it("clears the inspector and renders an explicit filtered empty state", async () => {
    const user = userEvent.setup()
    const client = desktopClient()
    client.workspace.plans.push({
      ...client.workspace.plans[0],
      id: "11111111-1111-4111-8111-111111111199",
      name: "空计划",
      run_count: 0,
    })
    render(<App client={client} />)

    await user.click(await screen.findByRole("button", { name: /空计划/ }))

    expect(screen.getByText("这个计划还没有运行记录")).toBeInTheDocument()
    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("尚未选择运行")
    expect(inspector).not.toHaveTextContent("营销文案基准")
  })

  it("presents duration-only load without a zero-request target", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[0], {
      load_mode: "open_loop",
      rate_per_second: 12,
      planned: 0,
      duration_ms: 60_000,
      conclusion: "none",
    })
    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 营销文案基准",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText(/目标 01:00/)).toBeInTheDocument()
    expect(within(row as HTMLTableRowElement).queryByText("82/0")).not.toBeInTheDocument()

    const inspector = screen.getByRole("complementary", { name: "运行详情" })
    expect(inspector).toHaveTextContent("目标时长 01:00")
    expect(inspector).toHaveTextContent("82 个请求已完成")
    expect(inspector).not.toHaveTextContent("0 个请求已固定")
    expect(inspector).not.toHaveTextContent("82/0 已完成")
  })

  it("stops indeterminate animation when a duration-only run is terminal", async () => {
    const client = desktopClient()
    Object.assign(client.workspace.runs[0], {
      load_mode: "open_loop",
      rate_per_second: 12,
      planned: 0,
      duration_ms: 60_000,
      completed: 712,
      passed: 712,
      failed: 0,
      status: "completed",
      conclusion: "passed",
    })
    client.workspace.active_run_id = undefined
    render(<App client={client} />)

    const runLink = await screen.findByRole("button", {
      name: "查看 营销文案基准",
    })
    const row = runLink.closest("tr")
    expect(row).not.toBeNull()
    expect(within(row as HTMLTableRowElement).getByText("712 个已完成")).toBeInTheDocument()
    expect(row?.querySelector('[data-slot="spinner"]')).toBeNull()
  })

  it("ships a blocking theme bootstrap before the React entrypoint", () => {
    const bootstrap = indexHtml.indexOf("llm-test:ui-preferences:v1")
    const entrypoint = indexHtml.indexOf('/src/main.tsx')

    expect(bootstrap).toBeGreaterThan(-1)
    expect(bootstrap).toBeLessThan(entrypoint)
    expect(indexHtml).toContain("data-theme")
    expect(indexHtml).toContain("colorScheme")
  })

  it("uses the exact town semantic tokens and the Contrast system icon", () => {
    for (const token of [
      "--foreground: rgba(17,24,39,.92)",
      "--card: rgba(255,255,255,.94)",
      "--muted-foreground: rgba(17,24,39,.56)",
      "--border: rgba(15,23,42,.12)",
      "--foreground: rgba(255,255,255,.88)",
      "--card: rgba(31,31,31,.92)",
      "--muted-foreground: rgba(255,255,255,.55)",
      "--border: rgba(255,255,255,.10)",
    ]) {
      expect(indexCss).toContain(token)
    }

    expect(indexCss).not.toContain("--foreground: #252a31")
    expect(indexCss).not.toContain("--foreground: #e8eaed")
  })
})
