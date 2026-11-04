import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { FIXTURE_CATALOG, FIXTURE_REPORTS, FIXTURE_WORKSPACE } from "@/test/workspace-fixtures"
import { EMPTY_WORKSPACE } from "@/features/runs/data"
import { OverviewWorkspace, type OverviewDestination } from "./overview-workspace"

const snapshots = { workspace: FIXTURE_WORKSPACE, catalog: FIXTURE_CATALOG, reports: FIXTURE_REPORTS }

describe("OverviewWorkspace drilldown", () => {
  it("opens each summary destination with independent case and suite controls", async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    render(<OverviewWorkspace {...snapshots} onOpenReport={vi.fn()} onNavigate={onNavigate} />)

    const summary = screen.getByRole("region", { name: "本地对象摘要" })
    const buttons = within(summary).getAllByRole("button")
    const destinations: OverviewDestination[] = ["models", "channels", "cases", "suites", "plans", "runs", "reports"]
    expect(buttons).toHaveLength(destinations.length)
    for (const [index, destination] of destinations.entries()) {
      await user.click(buttons[index])
      expect(onNavigate).toHaveBeenLastCalledWith(destination)
    }
    expect(summary.querySelector("button button")).toBeNull()
  })

  it("supports keyboard activation through the shared button primitive", async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    render(<OverviewWorkspace {...snapshots} onOpenReport={vi.fn()} onNavigate={onNavigate} />)

    const buttons = within(screen.getByRole("region", { name: "本地对象摘要" })).getAllByRole("button")
    await user.tab()
    expect(buttons[0]).toHaveFocus()
    await user.keyboard("{Enter}")
    expect(onNavigate).toHaveBeenLastCalledWith("models")
    await user.tab()
    expect(buttons[1]).toHaveFocus()
    await user.keyboard(" ")
    expect(onNavigate).toHaveBeenLastCalledWith("channels")
  })

  it("opens the catalog tab appropriate to each readiness value", async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    render(<OverviewWorkspace {...snapshots} onOpenReport={vi.fn()} onNavigate={onNavigate} />)

    const readiness = screen.getByRole("region", { name: "目录就绪度" })
    for (const [name, destination] of [
      [/^启用渠道/, "channels"],
      [/^已配置凭据/, "channels"],
      [/^模型映射/, "mappings"],
      [/^可复用套件/, "suites"],
    ] as const) {
      await user.click(within(readiness).getByRole("button", { name }))
      expect(onNavigate).toHaveBeenLastCalledWith(destination)
    }
  })

  it("opens the authoritative active run ID rather than the first displayed run", async () => {
    const user = userEvent.setup()
    const onOpenRun = vi.fn()
    render(<OverviewWorkspace {...snapshots} workspace={{ ...FIXTURE_WORKSPACE, runs: [...FIXTURE_WORKSPACE.runs].reverse() }} onOpenReport={vi.fn()} onOpenRun={onOpenRun} />)

    await user.click(screen.getByRole("button", { name: "查看运行" }))
    expect(onOpenRun).toHaveBeenCalledExactlyOnceWith(FIXTURE_WORKSPACE.active_run_id)
  })

  it("renders plain values when navigation callbacks are absent", () => {
    render(<OverviewWorkspace {...snapshots} onOpenReport={vi.fn()} />)

    expect(within(screen.getByRole("region", { name: "本地对象摘要" })).queryByRole("button")).not.toBeInTheDocument()
    expect(within(screen.getByRole("region", { name: "目录就绪度" })).queryByRole("button")).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "查看运行" })).not.toBeInTheDocument()
  })

  it("does not offer an active run action when no run is active", () => {
    render(<OverviewWorkspace {...snapshots} workspace={EMPTY_WORKSPACE} onOpenReport={vi.fn()} onOpenRun={vi.fn()} />)

    expect(screen.getByText("当前没有活动运行")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "查看运行" })).not.toBeInTheDocument()
  })
})
