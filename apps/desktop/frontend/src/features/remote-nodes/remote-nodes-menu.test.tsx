import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { I18nextProvider } from "react-i18next"
import { afterEach, describe, expect, it, vi } from "vitest"

import { DesktopClientError } from "@/app/desktop-client"
import { TooltipProvider } from "@/components/ui/tooltip"
import { createAppI18n } from "@/i18n/i18n"
import en from "@/i18n/resources/en-US/remote-nodes.json"
import zh from "@/i18n/resources/zh-CN/remote-nodes.json"

import type { RemoteNode, RemoteNodesSnapshot } from "./data"
import { RemoteNodesMenu, type RemoteNodesClient } from "./remote-nodes-menu"

vi.mock("@/app/desktop-client", () => ({
  DesktopClientError: class extends Error {
    code: string
    constructor(code: string) { super(code); this.code = code }
  },
}))

const NODE_ID = "11111111-1111-4111-8111-111111111111"
const fingerprint = { type: "ssh-ed25519", fingerprint: `SHA256:${"A".repeat(43)}` }

function node(changes: Partial<RemoteNode> = {}): RemoteNode {
  return { id: NODE_ID, revision: 3, name: "Tokyo runner", region: "Tokyo", host: "runner.example", port: 22,
    user: "runner", enabled: true, authentication_kind: "password", has_authentication: true, has_passphrase: false,
    host_key: fingerprint, proposed_host_key: null, status: "ready", eligible: true, checking: false,
    checked_at: "2026-10-16T00:00:00Z", runner: { node_id: NODE_ID, installation_id: NODE_ID,
      version: "v-current", build_id: "a".repeat(64), os: "linux", arch: "amd64", ready: true, healthy: true,
      active: 2, queued: 1, pending: 0, max_active_jobs: 4, max_concurrency: 16 },
    installation_stage: "completed", installation_error: "", upgrade: null, ...changes }
}

function fixture(nodes: RemoteNode[] = [node()]) {
  let snapshot: RemoteNodesSnapshot = { schema_version: 1, default_revision: 4,
    default: { kind: "remote", configuration_id: nodes[0]?.id ?? NODE_ID }, nodes }
  if (!nodes.length) snapshot.default = { kind: "local", configuration_id: "" }
  const client = {
    getRemoteNodes: vi.fn(async () => structuredClone(snapshot)),
    saveRemoteNode: vi.fn<RemoteNodesClient["saveRemoteNode"]>(async () => undefined),
    refreshRemoteNode: vi.fn<RemoteNodesClient["refreshRemoteNode"]>(async () => undefined),
    confirmRemoteNodeHostKey: vi.fn<RemoteNodesClient["confirmRemoteNodeHostKey"]>(async () => undefined),
    selectDefaultExecution: vi.fn<RemoteNodesClient["selectDefaultExecution"]>(async () => undefined),
    retryRemoteInstallation: vi.fn<RemoteNodesClient["retryRemoteInstallation"]>(async () => undefined),
    removeRemoteNode: vi.fn<RemoteNodesClient["removeRemoteNode"]>(async () => undefined),
    startRemoteNodeUpgrade: vi.fn<RemoteNodesClient["startRemoteNodeUpgrade"]>(),
    getRemoteNodeUpgrade: vi.fn<RemoteNodesClient["getRemoteNodeUpgrade"]>(async () => null),
    retryRemoteNodeUpgrade: vi.fn<RemoteNodesClient["retryRemoteNodeUpgrade"]>(),
    recoverRemoteNodeUpgrade: vi.fn<RemoteNodesClient["recoverRemoteNodeUpgrade"]>(),
  } satisfies RemoteNodesClient
  return { client, get: () => snapshot, set: (next: RemoteNodesSnapshot) => { snapshot = next } }
}

function show(client: RemoteNodesClient, locale: "zh-CN" | "en-US" = "zh-CN") {
  const i18n = createAppI18n(locale)
  i18n.addResourceBundle("zh-CN", "remoteNodes", zh)
  i18n.addResourceBundle("en-US", "remoteNodes", en)
  return render(<I18nextProvider i18n={i18n}><TooltipProvider><RemoteNodesMenu client={client} /></TooltipProvider></I18nextProvider>)
}

async function open(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "远程节点" }))
  await screen.findByRole("button", { name: "添加节点" })
  await waitFor(() => expect(screen.getByRole("button", { name: "添加节点" })).toBeEnabled())
}

afterEach(() => vi.useRealTimers())

describe("RemoteNodesMenu", () => {
  it.each(["offline", "disabled"] as const)("keeps a %s default and only changes it by explicit selection", async status => {
    const user = userEvent.setup()
    const f = fixture([node({ status, eligible: false, enabled: status !== "disabled" })])
    f.client.selectDefaultExecution.mockImplementation(async command => { f.set({ ...f.get(), default: command.location, default_revision: 5 }) })
    show(f.client)
    await open(user)
    expect(screen.getByRole("combobox", { name: "默认执行位置" })).toHaveValue(`Tokyo runner · Tokyo · ${status === "offline" ? "离线" : "已停用"}`)
    expect(f.client.selectDefaultExecution).not.toHaveBeenCalled()
    await user.click(screen.getByRole("combobox", { name: "默认执行位置" }))
    await user.click(screen.getByRole("option", { name: "本地" }))
    await waitFor(() => expect(f.client.selectDefaultExecution).toHaveBeenCalledWith({ expected_revision: 4, location: { kind: "local", configuration_id: "" } }))
    await waitFor(() => expect(screen.getByRole("combobox", { name: "默认执行位置" })).toHaveValue("本地"))
  })

  it("only trusts the exact observed host key after an explicit action", async () => {
    const user = userEvent.setup()
    const previous = { ...fingerprint, fingerprint: `SHA256:${"B".repeat(43)}` }
    const f = fixture([node({ status: "host_changed", eligible: false, host_key: previous, proposed_host_key: fingerprint })])
    show(f.client)
    await open(user)
    expect(screen.getByText(fingerprint.fingerprint)).toBeVisible()
    expect(f.client.confirmRemoteNodeHostKey).not.toHaveBeenCalled()
    expect(screen.queryByRole("textbox", { name: /指纹/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "信任此指纹" }))
    expect(f.client.confirmRemoteNodeHostKey).toHaveBeenCalledWith({ id: NODE_ID, expected_revision: 3, key: fingerprint })
  })

  it("reconciles an uncertain new save before another command, retaining its ID and clearing credentials", async () => {
    const user = userEvent.setup()
    const f = fixture([])
    let resolveRead!: (value: RemoteNodesSnapshot) => void
    let savedID = ""
    f.client.saveRemoteNode.mockImplementationOnce(async draft => {
      savedID = draft.id
      f.client.getRemoteNodes.mockImplementationOnce(() => new Promise(resolve => { resolveRead = resolve }))
      throw new DesktopClientError("remote_node_commit_unconfirmed")
    })
    show(f.client)
    await open(user)
    await user.click(screen.getByRole("button", { name: "添加节点" }))
    await user.type(screen.getByLabelText("名称"), "New runner")
    await user.type(screen.getByLabelText("SSH 主机"), "ssh.example")
    await user.type(screen.getByLabelText("SSH 用户"), "tester")
    await user.type(screen.getByLabelText("SSH 密码"), "sensitive-password")
    await user.click(screen.getByRole("button", { name: "保存节点" }))
    await waitFor(() => expect(f.client.saveRemoteNode).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText("SSH 密码")).toHaveValue("")
    expect(screen.getByRole("button", { name: "保存节点" })).toBeDisabled()
    expect(screen.queryByText("sensitive-password")).not.toBeInTheDocument()
    const saved = node({ id: savedID, name: "New runner", host: "ssh.example", user: "tester", revision: 1 })
    await act(async () => resolveRead({ ...f.get(), nodes: [saved] }))
    await waitFor(() => expect(screen.getByRole("button", { name: "保存节点" })).toBeEnabled())
    expect(screen.getByText("保存结果尚不确定，请检查重新读取的配置后再保存。")).toBeVisible()
    expect(f.client.saveRemoteNode).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole("button", { name: "保存节点" }))
    expect(f.client.saveRemoteNode).toHaveBeenLastCalledWith(expect.objectContaining({ id: savedID, expected_revision: 1, password: "" }))
  })

  it("preserves existing empty credentials and supports an explicit passphrase removal", async () => {
    const user = userEvent.setup()
    const f = fixture([node({ authentication_kind: "private_key", has_passphrase: true })])
    show(f.client)
    await open(user)
    await user.click(screen.getByRole("button", { name: "编辑" }))
    expect(screen.getByLabelText("SSH 私钥")).toHaveValue("")
    expect(screen.getByLabelText("私钥口令（可选）")).toHaveValue("")
    await user.click(screen.getByRole("checkbox", { name: "移除已保存的私钥口令" }))
    await user.click(screen.getByRole("button", { name: "保存节点" }))
    expect(f.client.saveRemoteNode).toHaveBeenCalledWith(expect.objectContaining({ id: NODE_ID, expected_revision: 3,
      authentication: "private_key", private_key: "", passphrase: "", remove_passphrase: true }))
  })

  it("blocks changes until a failed post-save read is explicitly recovered", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.saveRemoteNode.mockImplementationOnce(async () => {
      f.set({ ...f.get(), nodes: [node({ revision: 4 })] })
      f.client.getRemoteNodes.mockRejectedValueOnce(new Error("/private/read-failure"))
      throw new Error("/private/save-failure")
    })
    show(f.client)
    await open(user)
    await user.click(screen.getByRole("button", { name: "编辑" }))
    await user.click(screen.getByRole("button", { name: "保存节点" }))
    await screen.findByText("无法读取当前配置。请重新读取节点后再进行更改。")
    expect(screen.getByRole("button", { name: "保存节点" })).toBeDisabled()
    expect(screen.queryByText(/private\//)).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "重新读取节点" }))
    await waitFor(() => expect(screen.getByRole("button", { name: "保存节点" })).toBeEnabled())
    expect(f.client.saveRemoteNode).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole("button", { name: "保存节点" }))
    expect(f.client.saveRemoteNode).toHaveBeenLastCalledWith(expect.objectContaining({ id: NODE_ID, expected_revision: 4 }))
  })

  it("keeps a rejected removal and hides raw internal installation errors", async () => {
    const user = userEvent.setup()
    const f = fixture([node({ installation_error: "secret=/private/credential-reference", installation_stage: "C:/private/path" })])
    f.client.removeRemoteNode.mockRejectedValueOnce(new DesktopClientError("remote_node_still_referenced"))
    show(f.client)
    await open(user)
    await user.click(screen.getByRole("button", { name: "移除" }))
    await screen.findByText("此节点仍被任务引用或仍是默认节点。请先更改默认位置并处理任务引用，再移除节点。")
    expect(screen.getByRole("heading", { name: "Tokyo runner" })).toBeVisible()
    expect(screen.queryByText(/secret=|private\/|credential-reference/)).not.toBeInTheDocument()
    expect(screen.getByText("安装未能完成，请检查连接后重试安装。")).toBeVisible()
  })

  it("stops polling when the sheet closes", async () => {
    const user = userEvent.setup()
    const f = fixture([node({ checking: true, status: "checking" })])
    const view = show(f.client)
    await open(user)
    await waitFor(() => expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(3), { timeout: 2200 })
    await user.click(screen.getByRole("button", { name: "关闭" }))
    const afterClose = f.client.getRemoteNodes.mock.calls.length
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 1700)) })
    expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(afterClose)
    view.unmount()
  })

  it("ignores a read that resolves after unmount", async () => {
    const f = fixture()
    let resolveRead!: (value: RemoteNodesSnapshot) => void
    f.client.getRemoteNodes.mockImplementationOnce(() => new Promise(resolve => { resolveRead = resolve }))
    const view = show(f.client)
    view.unmount()
    await act(async () => resolveRead(f.get()))
    expect(screen.queryByRole("button", { name: "远程节点" })).not.toBeInTheDocument()
    expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(1)
  })

  it("bounds installation polling and lets the user explicitly resume it", async () => {
    const user = userEvent.setup()
    const f = fixture()
    f.client.refreshRemoteNode.mockImplementation(async () => { f.set({ ...f.get(), nodes: [node({ checking: true, status: "checking" })] }) })
    show(f.client)
    await open(user)
    vi.useFakeTimers()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "检查连接" })))
    const readsBeforePolling = f.client.getRemoteNodes.mock.calls.length
    for (let count = 0; count < 40; count++) await act(async () => vi.advanceTimersByTimeAsync(1500))
    expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(readsBeforePolling + 40)
    await act(async () => vi.advanceTimersByTimeAsync(10000))
    expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(readsBeforePolling + 40)
    expect(screen.getByText(/自动刷新已在一分钟后暂停/)).toBeVisible()
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "重新读取节点" })))
    expect(f.client.getRemoteNodes).toHaveBeenCalledTimes(readsBeforePolling + 41)
  })

  it("renders English labels and authoritative runner metrics", async () => {
    const user = userEvent.setup()
    const f = fixture()
    show(f.client, "en-US")
    await user.click(screen.getByRole("button", { name: "Remote nodes" }))
    const sheet = await screen.findByRole("dialog", { name: "Remote nodes" })
    expect(await within(sheet).findByText("2 active · 1 queued · 0 pending")).toBeVisible()
    expect(within(sheet).getByText("v-current")).toBeVisible()
    expect(within(sheet).getByRole("button", { name: "Retry installation" })).toBeVisible()
  })

  it("keeps the build collapsed and copies it only on request", async () => {
    const user = userEvent.setup()
    const f = fixture()
    show(f.client)
    await open(user)
    expect(screen.queryByText("a".repeat(64))).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "执行器详情" }))
    expect(screen.getByText("a".repeat(64))).toBeVisible()
    await user.click(screen.getByRole("button", { name: "复制构建标识" }))
    expect(await navigator.clipboard.readText()).toBe("a".repeat(64))
    expect(screen.getByText("构建标识已复制")).toBeVisible()
    await user.click(screen.getByRole("button", { name: "执行器详情" }))
    expect(screen.queryByText("a".repeat(64))).not.toBeInTheDocument()
    expect(f.client.startRemoteNodeUpgrade).not.toHaveBeenCalled()
  })

  it("keeps remote translation keys and interpolation variables in parity", () => {
    const flatten = (value: unknown, prefix = ""): Record<string, string> => typeof value === "string" ? { [prefix]: value }
      : Object.fromEntries(Object.entries(value as Record<string, unknown>).flatMap(([key, child]) => Object.entries(flatten(child, `${prefix}.${key}`))))
    const left = flatten(zh)
    const right = flatten(en)
    expect(Object.keys(left).sort()).toEqual(Object.keys(right).sort())
    for (const key of Object.keys(left)) {
      const variables = (text: string) => [...text.matchAll(/{{\s*([^},\s]+).*?}}/g)].map(match => match[1]).sort()
      expect(variables(left[key])).toEqual(variables(right[key]))
    }
  })
})
