import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, it, vi } from "vitest"

import type { DesktopClient } from "@/app/desktop-client"
import { createAppI18n } from "@/i18n/i18n"
import en from "@/i18n/resources/en-US/history-recycle.json"
import zh from "@/i18n/resources/zh-CN/history-recycle.json"

import type { HistoryRecyclePreview, RecycledHistoryEntry } from "./data"
import { RecycleHistoryButton } from "./recycle-history-button"

const OWNER: RecycledHistoryEntry = {
  kind: "job", id: "11111111-1111-4111-8111-111111111111", title: "整次功能测试",
  removed_at: "", remote: true,
  job_ids: ["11111111-1111-4111-8111-111111111111"],
  run_ids: ["22222222-2222-4222-8222-222222222222"],
  report_ids: ["33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444"],
  comparison_ids: [],
}
const TARGET = { kind: "report" as const, id: OWNER.report_ids[0] }

function preview(blocked: HistoryRecyclePreview["blocked_reason"] = ""): HistoryRecyclePreview {
  return { schema_version: 1, entry: OWNER, can_recycle: !blocked, blocked_reason: blocked }
}

function makeClient() {
  return {
    previewHistoryRecycle: vi.fn<DesktopClient["previewHistoryRecycle"]>().mockResolvedValue(preview()),
    recycleHistory: vi.fn<DesktopClient["recycleHistory"]>().mockResolvedValue({ ...OWNER, removed_at: "2026-10-29T10:11:12Z" }),
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

beforeEach(() => {
  const i18n = createAppI18n("zh-CN")
  i18n.addResourceBundle("zh-CN", "historyRecycle", zh)
  i18n.addResourceBundle("en-US", "historyRecycle", en)
})

it.each([
  ["active", "测试仍在执行，请先停止或取消，等待结束后再移除。"],
  ["unconfirmed", "任务状态尚未确认，请等待状态确认后再移除。"],
] as const)("honors an authoritative %s block without mutating history", async (reason, message) => {
  const user = userEvent.setup()
  const client = makeClient()
  client.previewHistoryRecycle.mockResolvedValue(preview(reason))
  const changed = vi.fn()
  render(<RecycleHistoryButton client={client} target={TARGET} onHistoryChanged={changed} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  const dialog = screen.getByRole("alertdialog", { name: "移入结果回收站" })
  expect(await within(dialog).findByRole("status")).toHaveTextContent(message)
  const confirm = within(dialog).getByRole("button", { name: "移入回收站" })
  expect(confirm).toBeDisabled()
  await user.click(confirm)
  expect(client.previewHistoryRecycle).toHaveBeenCalledWith(TARGET)
  expect(client.recycleHistory).not.toHaveBeenCalled()
  expect(changed).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole("button", { name: "取消" }))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})

it("confirms the entire canonical group and publishes only the authoritative returned membership", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  const returned = { ...OWNER, title: "服务端确认的测试名称", removed_at: "2026-10-29T10:11:12Z", report_ids: [OWNER.report_ids[0]] }
  const pending = deferred<RecycledHistoryEntry>()
  client.recycleHistory.mockReturnValue(pending.promise)
  const changed = vi.fn()
  const recycled = vi.fn()
  render(<RecycleHistoryButton client={client} target={TARGET} onRecycled={recycled} onHistoryChanged={changed} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  const dialog = screen.getByRole("alertdialog", { name: "移入结果回收站" })
  expect(await within(dialog).findByText("整次功能测试")).toBeInTheDocument()
  expect(within(dialog).getByText("1 次运行 / 2 份报告")).toBeInTheDocument()
  expect(within(dialog).getByText(/整次测试及关联报告将一起/)).toBeInTheDocument()
  expect(within(dialog).getByText("此操作可以恢复，不会释放磁盘空间。")).toBeInTheDocument()
  expect(within(dialog).getByText("远程节点上的数据保持不变。")).toBeInTheDocument()
  await user.dblClick(within(dialog).getByRole("button", { name: "移入回收站" }))
  expect(client.recycleHistory).toHaveBeenCalledTimes(1)
  expect(client.recycleHistory).toHaveBeenCalledWith({ kind: "job", id: OWNER.id })
  expect(within(dialog).getByRole("button", { name: "取消" })).toBeDisabled()
  expect(changed).not.toHaveBeenCalled()
  await act(async () => pending.resolve(returned))
  expect(recycled).toHaveBeenCalledWith(returned, "recycle")
  expect(changed).toHaveBeenCalledWith(returned, "recycle")
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
})

it("still invalidates other pages if local refresh fails after a successful move", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  const changed = vi.fn()
  render(<RecycleHistoryButton client={client} target={TARGET} onHistoryChanged={changed} onRecycled={() => { throw new Error("private refresh error") }} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  const dialog = screen.getByRole("alertdialog")
  const confirm = within(dialog).getByRole("button", { name: "移入回收站" })
  await waitFor(() => expect(confirm).toBeEnabled())
  await user.click(confirm)
  await waitFor(() => expect(changed).toHaveBeenCalledOnce())
  expect(client.recycleHistory).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  expect(await screen.findByRole("status")).toHaveTextContent("记录已移入回收站，部分页面暂未刷新")
  expect(screen.queryByText(/private refresh error/)).not.toBeInTheDocument()
})

it("keeps preview failures safe and allows a fresh inspection", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  client.previewHistoryRecycle.mockRejectedValueOnce(new Error("private-path=/workspace/database"))
  render(<RecycleHistoryButton client={client} target={TARGET} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  let dialog = screen.getByRole("alertdialog")
  expect(await within(dialog).findByRole("alert")).toHaveTextContent("移入回收站失败，请重试。")
  expect(screen.queryByText(/private-path/)).not.toBeInTheDocument()
  expect(within(dialog).getByRole("button", { name: "移入回收站" })).toBeDisabled()
  expect(client.recycleHistory).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole("button", { name: "取消" }))
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  dialog = screen.getByRole("alertdialog")
  expect(await within(dialog).findByText("整次功能测试")).toBeInTheDocument()
  expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument()
  expect(client.previewHistoryRecycle).toHaveBeenCalledTimes(2)
})

it("keeps a failed command visible and retryable without invoking success callbacks", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  client.recycleHistory.mockRejectedValueOnce(new Error("credential=private-token"))
  const changed = vi.fn()
  render(<RecycleHistoryButton client={client} target={TARGET} onHistoryChanged={changed} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  const dialog = screen.getByRole("alertdialog")
  const confirm = within(dialog).getByRole("button", { name: "移入回收站" })
  await waitFor(() => expect(confirm).toBeEnabled())
  await user.click(confirm)
  expect(await within(dialog).findByRole("alert")).toHaveTextContent("移入回收站失败，请重试。")
  expect(screen.queryByText(/private-token/)).not.toBeInTheDocument()
  expect(confirm).toBeEnabled()
  expect(changed).not.toHaveBeenCalled()
  await user.click(confirm)
  expect(changed).toHaveBeenCalledTimes(1)
  expect(client.recycleHistory).toHaveBeenCalledTimes(2)
})

it("ignores a cancelled preview that resolves after a newer blocked inspection", async () => {
  const user = userEvent.setup()
  const oldPreview = deferred<HistoryRecyclePreview>()
  const client = makeClient()
  client.previewHistoryRecycle.mockReturnValueOnce(oldPreview.promise).mockResolvedValueOnce(preview("active"))
  render(<RecycleHistoryButton client={client} target={TARGET} />)
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  let dialog = screen.getByRole("alertdialog")
  expect(within(dialog).getByRole("status")).toHaveTextContent("正在检查关联记录…")
  await user.click(within(dialog).getByRole("button", { name: "取消" }))
  expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "移入回收站" }))
  dialog = screen.getByRole("alertdialog")
  await within(dialog).findByText("测试仍在执行，请先停止或取消，等待结束后再移除。")
  await act(async () => oldPreview.resolve(preview()))
  expect(within(dialog).getByRole("button", { name: "移入回收站" })).toBeDisabled()
  expect(within(dialog).getByRole("status")).toHaveTextContent("测试仍在执行")
  expect(client.recycleHistory).not.toHaveBeenCalled()
})
