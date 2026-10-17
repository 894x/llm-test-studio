import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { afterEach, describe, expect, it, vi } from "vitest"

import { DesktopClientError } from "@/app/desktop-client"
import { TooltipProvider } from "@/components/ui/tooltip"
import { createAppI18n } from "@/i18n/i18n"
import { remoteNodeFixture, remoteRunnerFixture, remoteUpgradeFixture, REMOTE_CONFIGURATION_ID, UPGRADE_OPERATION_ID } from "@/test/remote-upgrade-fixture"
import type { RemoteNode, RemoteNodesSnapshot, RemoteNodeUpgradeView } from "./data"
import { RemoteNodesMenu, type RemoteNodesClient } from "./remote-nodes-menu"

function fixture(nodes: RemoteNode[] = [remoteNodeFixture()]) {
  const snapshot: RemoteNodesSnapshot = { schema_version: 1, default_revision: 5,
    default: { kind: "remote", configuration_id: REMOTE_CONFIGURATION_ID }, nodes }
  const client = {
    getRemoteNodes: vi.fn(async () => structuredClone(snapshot)),
    saveRemoteNode: vi.fn<RemoteNodesClient["saveRemoteNode"]>(),
    refreshRemoteNode: vi.fn<RemoteNodesClient["refreshRemoteNode"]>(),
    confirmRemoteNodeHostKey: vi.fn<RemoteNodesClient["confirmRemoteNodeHostKey"]>(),
    selectDefaultExecution: vi.fn<RemoteNodesClient["selectDefaultExecution"]>(async command => { snapshot.default = command.location }),
    retryRemoteInstallation: vi.fn<RemoteNodesClient["retryRemoteInstallation"]>(),
    removeRemoteNode: vi.fn<RemoteNodesClient["removeRemoteNode"]>(),
    startRemoteNodeUpgrade: vi.fn<RemoteNodesClient["startRemoteNodeUpgrade"]>(async command => {
      const view = remoteUpgradeFixture({ operation_id: command.operation_id, confirmed: false })
      snapshot.nodes.find(node => node.id === command.id)!.upgrade = view
      return structuredClone(view)
    }),
    getRemoteNodeUpgrade: vi.fn<RemoteNodesClient["getRemoteNodeUpgrade"]>(async id => structuredClone(snapshot.nodes.find(node => node.id === id)?.upgrade ?? null)),
    retryRemoteNodeUpgrade: vi.fn<RemoteNodesClient["retryRemoteNodeUpgrade"]>(async () => remoteUpgradeFixture()),
    recoverRemoteNodeUpgrade: vi.fn<RemoteNodesClient["recoverRemoteNodeUpgrade"]>(async () => remoteUpgradeFixture()),
  } satisfies RemoteNodesClient
  return { client, snapshot }
}

function show(client: RemoteNodesClient, locale: "zh-CN" | "en-US" = "zh-CN") {
  return render(<I18nextProvider i18n={createAppI18n(locale)}><TooltipProvider><RemoteNodesMenu client={client} /></TooltipProvider></I18nextProvider>)
}
async function open(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "远程节点" }))
  await waitFor(() => expect(screen.getByRole("button", { name: "添加节点" })).toBeEnabled())
}
const nodeRegion = () => screen.getByRole("region", { name: "Upgrade test node" })
afterEach(() => vi.useRealTimers())

describe("explicit Runner upgrades", () => {
  it("accepts the actual compatible build and starts exactly one operation only after a click", async () => {
    const user = userEvent.setup()
    const f = fixture()
    let acknowledge!: (value: RemoteNodeUpgradeView) => void
    f.client.startRemoteNodeUpgrade.mockImplementation(() => new Promise(resolve => { acknowledge = resolve }))
    show(f.client)
    await open(user)
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
    expect(within(nodeRegion()).getByText("runner-old-compatible")).toBeVisible()
    const start = within(nodeRegion()).getByRole("button", { name: "升级 Runner" })
    expect(start).toBeEnabled()
    await user.click(start)
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
    expect(screen.getByRole("alertdialog")).toHaveTextContent("显式暂停任务接入")
    await user.click(screen.getByRole("button", { name: "取消" }))
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
    await user.click(start)
    const confirm = screen.getByRole("button", { name: "确认升级" })
    await act(async () => { fireEvent.click(confirm); fireEvent.click(confirm) })
    expect(f.client.startRemoteNodeUpgrade).toHaveBeenCalledTimes(1)
    const command = f.client.startRemoteNodeUpgrade.mock.calls[0][0]
    expect(command).toMatchObject({ id: REMOTE_CONFIGURATION_ID, expected_revision: 3, expected_build_id: "a".repeat(64) })
    expect(command.operation_id).toMatch(/^[0-9a-f-]{36}$/)
    expect(within(nodeRegion()).getByRole("button", { name: "编辑" })).toBeDisabled()
    expect(within(nodeRegion()).getByRole("button", { name: "移除" })).toBeDisabled()
    expect(within(nodeRegion()).getByRole("button", { name: "重试安装" })).toBeDisabled()
    const view = remoteUpgradeFixture({ operation_id: command.operation_id })
    f.snapshot.nodes[0].upgrade = view
    await act(async () => acknowledge(view))
  })

  it("retains an uncertain operation across close/reopen and resends the same Start only when local intent is absent", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.startRemoteNodeUpgrade.mockRejectedValueOnce(new Error("secret=/private/upgrade"))
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" }))
    await user.click(screen.getByRole("button", { name: "确认升级" }))
    await screen.findByText("无法确认此次命令的结果。已保留同一操作，请先读取状态再重试。")
    const command = f.client.startRemoteNodeUpgrade.mock.calls[0][0]
    expect(screen.queryByText(/secret=|private\//)).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "关闭" }))
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "重试此次升级" }))
    expect(f.client.startRemoteNodeUpgrade).toHaveBeenCalledTimes(2)
    expect(f.client.startRemoteNodeUpgrade.mock.calls[1][0]).toEqual(command)
    expect(f.client.retryRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("polls the same durable operation while closed and observes completion without cancelling or retrying it", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.startRemoteNodeUpgrade.mockImplementation(async command => {
      f.snapshot.nodes[0].upgrade = remoteUpgradeFixture({ operation_id: command.operation_id, confirmed: false, error_code: "remote_connection_failed" })
      throw new DesktopClientError("remote_node_commit_unconfirmed")
    })
    show(f.client)
    await open(user)
    vi.useFakeTimers()
    await act(async () => fireEvent.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" })))
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "确认升级" })))
    const command = f.client.startRemoteNodeUpgrade.mock.calls[0][0]
    const before = f.client.getRemoteNodeUpgrade.mock.calls.length
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "关闭" })))
    f.snapshot.nodes[0].upgrade = remoteUpgradeFixture({ operation_id: command.operation_id, stage: "completed", resume_attempted: true,
      actual_runner: remoteRunnerFixture({ version: "runner-upgraded", build_id: "b".repeat(64) }), changed_at: "2026-10-16T12:01:00Z" })
    await act(async () => vi.advanceTimersByTimeAsync(1500))
    expect(f.client.getRemoteNodeUpgrade).toHaveBeenCalledTimes(before + 1)
    expect(f.client.getRemoteNodeUpgrade).toHaveBeenLastCalledWith(REMOTE_CONFIGURATION_ID)
    expect(f.client.retryRemoteNodeUpgrade).not.toHaveBeenCalled()
    expect(f.client.recoverRemoteNodeUpgrade).not.toHaveBeenCalled()
    vi.useRealTimers()
    await open(user)
    expect(await screen.findByText("升级已完成")).toBeVisible()
    expect(within(nodeRegion()).getByText("runner-upgraded")).toBeVisible()
    expect(within(nodeRegion()).getByRole("button", { name: "编辑" })).toBeEnabled()
  })

  it("reopens a durable intent without creating another operation and retries the existing operation", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ upgrade: remoteUpgradeFixture({ confirmed: false, error_code: "remote_connection_failed" }) })])
    const first = show(f.client)
    await open(user)
    first.unmount()
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "重试此次升级" }))
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
    expect(f.client.retryRemoteNodeUpgrade).toHaveBeenCalledWith({ id: REMOTE_CONFIGURATION_ID, expected_revision: 3, operation_id: UPGRADE_OPERATION_ID })
  })

  it("allows the same operation to be retried after restart even when its journal was confirmed", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ upgrade: remoteUpgradeFixture({ stage: "backed_up", confirmed: true }) })])
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "重试此次升级" }))
    expect(f.client.retryRemoteNodeUpgrade).toHaveBeenCalledWith({ id: REMOTE_CONFIGURATION_ID, expected_revision: 3, operation_id: UPGRADE_OPERATION_ID })
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("uses a newer node observation instead of a terminal upgrade's old task counts", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ checked_at: "2026-10-16T12:01:00Z",
      runner: remoteRunnerFixture({ version: "fresh-observation", build_id: "c".repeat(64), active: 2 }),
      upgrade: remoteUpgradeFixture({ stage: "completed", resume_attempted: true }) })])
    show(f.client)
    await open(user)
    expect(within(nodeRegion()).getByText("fresh-observation")).toBeVisible()
    expect(within(nodeRegion()).getByRole("button", { name: "升级 Runner" })).toBeDisabled()
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("uses the fresh actual build for the next explicit upgrade", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ checked_at: "2026-10-16T12:01:00Z",
      runner: remoteRunnerFixture({ build_id: "c".repeat(64) }),
      upgrade: remoteUpgradeFixture({ stage: "rolled_back" }) })])
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" }))
    await user.click(screen.getByRole("button", { name: "确认升级" }))
    expect(f.client.startRemoteNodeUpgrade.mock.calls[0][0].expected_build_id).toBe("c".repeat(64))
  })

  it("does not reopen recovery when an older acknowledgement arrives after a resume observation", async () => {
    const user = userEvent.setup()
    const f = fixture()
    let acknowledge!: (value: RemoteNodeUpgradeView) => void
    f.client.startRemoteNodeUpgrade.mockImplementation(() => new Promise(resolve => { acknowledge = resolve }))
    show(f.client)
    await open(user)
    vi.useFakeTimers()
    await act(async () => fireEvent.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" })))
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "确认升级" })))
    const operation_id = f.client.startRemoteNodeUpgrade.mock.calls[0][0].operation_id
    f.client.getRemoteNodeUpgrade.mockResolvedValueOnce(remoteUpgradeFixture({ operation_id, stage: "resume_attempted",
      resume_attempted: true, changed_at: "2026-10-16T12:02:00Z" }))
    await act(async () => vi.advanceTimersByTimeAsync(1500))
    expect(within(nodeRegion()).getByText(/已禁止还原旧数据库/)).toBeVisible()
    f.client.getRemoteNodeUpgrade.mockRejectedValue(new Error("read failed"))
    await act(async () => acknowledge(remoteUpgradeFixture({ operation_id, stage: "backed_up", restorable: true,
      changed_at: "2026-10-16T12:01:00Z" })))
    expect(within(nodeRegion()).queryByRole("button", { name: "恢复升级前状态" })).not.toBeInTheDocument()
    expect(within(nodeRegion()).getByText(/已禁止还原旧数据库/)).toBeVisible()
    expect(f.client.recoverRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("releases a definitely rejected Start only after the durable read confirms there is no intent", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.startRemoteNodeUpgrade.mockRejectedValue(new DesktopClientError("remote_node_busy"))
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" }))
    await user.click(screen.getByRole("button", { name: "确认升级" }))
    await waitFor(() => expect(within(nodeRegion()).getByRole("button", { name: "编辑" })).toBeEnabled())
    expect(f.client.getRemoteNodeUpgrade).toHaveBeenCalledWith(REMOTE_CONFIGURATION_ID)
    expect(within(nodeRegion()).queryByRole("button", { name: "重试此次升级" })).not.toBeInTheDocument()
  })

  it.each(["active", "queued", "pending"] as const)("shows existing %s tasks and blocks a new upgrade without changing task controls", async field => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ runner: remoteRunnerFixture({ [field]: 2 }) })])
    show(f.client)
    await open(user)
    expect(within(nodeRegion()).getByRole("button", { name: "升级 Runner" })).toBeDisabled()
    expect(within(nodeRegion()).getByText(/升级不会取消它们/)).toBeVisible()
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("only offers recovery before resume and uses the same operation", async () => {
    const user = userEvent.setup()
    const view = remoteUpgradeFixture({ stage: "backed_up", error_code: "remote_connection_failed", restorable: true })
    const f = fixture([remoteNodeFixture({ upgrade: view })])
    f.client.recoverRemoteNodeUpgrade.mockImplementation(async () => {
      const completed = remoteUpgradeFixture({ stage: "rolled_back", changed_at: "2026-10-16T12:02:00Z" })
      f.snapshot.nodes[0].upgrade = completed
      return completed
    })
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "恢复升级前状态" }))
    expect(f.client.recoverRemoteNodeUpgrade).toHaveBeenCalledWith({ id: REMOTE_CONFIGURATION_ID, expected_revision: 3, operation_id: UPGRADE_OPERATION_ID })
    await screen.findByText("已恢复升级前状态")
    expect(within(nodeRegion()).queryByRole("button", { name: "恢复升级前状态" })).not.toBeInTheDocument()
  })

  it("blocks database restoration once admission resume has been attempted", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ upgrade: remoteUpgradeFixture({ stage: "resume_attempted", resume_attempted: true, restorable: true,
      error_code: "remote_connection_failed" }) })])
    show(f.client)
    await open(user)
    expect(within(nodeRegion()).getByText(/已禁止还原旧数据库/)).toBeVisible()
    expect(within(nodeRegion()).queryByRole("button", { name: "恢复升级前状态" })).not.toBeInTheDocument()
    expect(f.client.recoverRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("keeps defaults and other nodes usable during an upgrade", async () => {
    const user = userEvent.setup()
    const otherID = "55555555-5555-4555-8555-555555555555"
    const f = fixture([remoteNodeFixture({ upgrade: remoteUpgradeFixture() }), remoteNodeFixture({ id: otherID, name: "Other node" })])
    show(f.client)
    await open(user)
    expect(within(nodeRegion()).getByRole("button", { name: "编辑" })).toBeDisabled()
    expect(within(screen.getByRole("region", { name: "Other node" })).getByRole("button", { name: "编辑" })).toBeEnabled()
    const selector = screen.getByRole("combobox", { name: "默认执行位置" })
    expect(selector).toBeEnabled()
    await user.click(selector)
    await user.click(screen.getByRole("option", { name: "本地" }))
    expect(f.client.selectDefaultExecution).toHaveBeenCalledWith({ expected_revision: 5, location: { kind: "local", configuration_id: "" } })
  })

  it("distinguishes busy before maintenance from busy after admission was paused", async () => {
    const user = userEvent.setup()
    const f = fixture([remoteNodeFixture({ upgrade: remoteUpgradeFixture({ stage: "pausing", error_code: "installation_busy", restorable: true }) })])
    show(f.client)
    await open(user)
    expect(within(nodeRegion()).getByText(/节点保持维护，已接受的任务继续运行/)).toBeVisible()
    expect(within(nodeRegion()).getByRole("button", { name: "重试此次升级" })).toBeEnabled()
    expect(within(nodeRegion()).getByRole("button", { name: "恢复升级前状态" })).toBeEnabled()
  })

  it("safely renders unknown error codes in English without exposing server text", async () => {
    const user = userEvent.setup()
    const unsafe = remoteUpgradeFixture({ error_code: "secret=/private/path" as never })
    const f = fixture([remoteNodeFixture({ upgrade: unsafe })])
    show(f.client, "en-US")
    await user.click(screen.getByRole("button", { name: "Remote nodes" }))
    expect(await screen.findByText("The upgrade reported an unrecognised error. Read its state and check desktop logs before proceeding.")).toBeVisible()
    expect(screen.queryByText(/secret=|private\//)).not.toBeInTheDocument()
  })

  it("locks a mismatched operation instead of replacing the retained intent", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.startRemoteNodeUpgrade.mockRejectedValueOnce(new Error("lost acknowledgement"))
    f.client.getRemoteNodeUpgrade.mockResolvedValue(remoteUpgradeFixture())
    show(f.client)
    await open(user)
    await user.click(within(nodeRegion()).getByRole("button", { name: "升级 Runner" }))
    await user.click(screen.getByRole("button", { name: "确认升级" }))
    await screen.findByText("升级操作或 Runner 身份与预期不一致。已锁住此节点，请先读取状态再继续。")
    expect(within(nodeRegion()).getByRole("button", { name: "重试此次升级" })).toBeDisabled()
    expect(within(nodeRegion()).getByRole("button", { name: "编辑" })).toBeDisabled()
  })
})
