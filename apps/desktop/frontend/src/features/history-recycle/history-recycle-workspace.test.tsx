import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, it, vi } from "vitest"

import type { DesktopClient } from "@/app/desktop-client"
import { createAppI18n } from "@/i18n/i18n"
import en from "@/i18n/resources/en-US/history-recycle.json"
import zh from "@/i18n/resources/zh-CN/history-recycle.json"

import type { RecycledHistoryEntry, RecycledHistoryPage } from "./data"
import { HistoryRecycleWorkspace } from "./history-recycle-workspace"

const FIRST: RecycledHistoryEntry = {
  kind: "job", id: "11111111-1111-4111-8111-111111111111", title: "功能测试任务",
  removed_at: "2026-10-29T10:11:12Z", remote: true,
  job_ids: ["11111111-1111-4111-8111-111111111111"],
  run_ids: ["22222222-2222-4222-8222-222222222222"],
  report_ids: ["33333333-3333-4333-8333-333333333333"], comparison_ids: [],
}
const SECOND: RecycledHistoryEntry = {
  kind: "report", id: "44444444-4444-4444-8444-444444444444", title: "性能测试报告",
  removed_at: "2026-10-28T10:11:12Z", remote: false,
  job_ids: [], run_ids: [], comparison_ids: [], report_ids: ["44444444-4444-4444-8444-444444444444"],
}

function page(entries: RecycledHistoryEntry[], next = ""): RecycledHistoryPage {
  return { schema_version: 1, entries, next_cursor: next, has_more: !!next }
}

function makeClient() {
  return {
    getRecycledHistory: vi.fn<DesktopClient["getRecycledHistory"]>().mockResolvedValue(page([])),
    restoreHistory: vi.fn<DesktopClient["restoreHistory"]>().mockResolvedValue({ ...FIRST, removed_at: "" }),
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

it("keeps loading, safe errors, retry, and the empty recycle bin in one surface", async () => {
  const user = userEvent.setup()
  const first = deferred<RecycledHistoryPage>()
  const client = makeClient()
  client.getRecycledHistory.mockReturnValueOnce(first.promise)
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={vi.fn()} />)
  expect(screen.getByRole("heading", { name: "结果回收站" })).toBeInTheDocument()
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true")
  expect(screen.getByRole("button", { name: "刷新" })).toBeDisabled()
  await act(async () => first.reject(new Error("secret=/private/database")))
  expect(screen.getByRole("alert")).toHaveTextContent("回收站读取失败，请重试。")
  expect(screen.queryByText(/secret=/)).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "重试" }))
  expect(await screen.findByRole("heading", { name: "回收站为空" })).toBeInTheDocument()
  expect(client.getRecycledHistory.mock.calls).toEqual([[""], [""]])
  expect(screen.getByText(/不会释放磁盘空间，远程节点上的数据保持不变/)).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: /彻底|清空|永久/ })).not.toBeInTheDocument()
})

it("restores the canonical owning identity and does not reinsert a restored row from a stale refresh", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  client.getRecycledHistory.mockResolvedValue(page([FIRST, SECOND]))
  const authoritative = { ...FIRST, title: "已确认的任务名称", removed_at: "" }
  client.restoreHistory.mockResolvedValue(authoritative)
  const changed = vi.fn()
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={changed} />)
  await user.click(await screen.findByRole("button", { name: "恢复 功能测试任务" }))
  await waitFor(() => expect(client.getRecycledHistory).toHaveBeenCalledTimes(2))
  expect(client.restoreHistory).toHaveBeenCalledWith({ kind: "job", id: FIRST.id })
  expect(changed).toHaveBeenCalledWith(authoritative, "restore")
  expect(screen.queryByRole("button", { name: "恢复 功能测试任务" })).not.toBeInTheDocument()
  expect(screen.getByRole("button", { name: "恢复 性能测试报告" })).toBeInTheDocument()
  expect(screen.getByRole("status")).toHaveTextContent("已恢复：已确认的任务名称")
})

it("keeps a failed restore recoverable, hides raw errors, and guards duplicate commands", async () => {
  const user = userEvent.setup()
  const pending = deferred<RecycledHistoryEntry>()
  const client = makeClient()
  client.getRecycledHistory.mockResolvedValue(page([FIRST]))
  client.restoreHistory.mockReturnValue(pending.promise)
  const changed = vi.fn()
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={changed} />)
  const restore = await screen.findByRole("button", { name: "恢复 功能测试任务" })
  await user.dblClick(restore)
  expect(client.restoreHistory).toHaveBeenCalledTimes(1)
  expect(restore).toBeDisabled()
  expect(screen.getByRole("button", { name: "刷新" })).toBeDisabled()
  await act(async () => pending.reject(new Error("provider-key=private")))
  expect(screen.getByRole("alert")).toHaveTextContent("恢复失败，记录仍保留在回收站，请重试。")
  expect(screen.queryByText(/provider-key/)).not.toBeInTheDocument()
  expect(restore).toBeEnabled()
  expect(changed).not.toHaveBeenCalled()
  expect(client.getRecycledHistory).toHaveBeenCalledTimes(1)
})

it("retains loaded rows on pagination failure and retries the same server cursor", async () => {
  const user = userEvent.setup()
  const next = deferred<RecycledHistoryPage>()
  const client = makeClient()
  client.getRecycledHistory.mockResolvedValueOnce(page([FIRST], "page-two"))
    .mockReturnValueOnce(next.promise).mockResolvedValueOnce(page([FIRST, SECOND]))
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={vi.fn()} />)
  await user.click(await screen.findByRole("button", { name: "加载更多" }))
  expect(screen.getByRole("button", { name: "恢复 功能测试任务" })).toBeDisabled()
  await act(async () => next.reject(new Error("network")))
  expect(screen.getByRole("button", { name: "恢复 功能测试任务" })).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "重试" }))
  expect(await screen.findByRole("button", { name: "恢复 性能测试报告" })).toBeInTheDocument()
  expect(screen.getAllByRole("button", { name: "恢复 功能测试任务" })).toHaveLength(1)
  expect(client.getRecycledHistory.mock.calls).toEqual([[""], ["page-two"], ["page-two"]])
  expect(screen.queryByRole("button", { name: "加载更多" })).not.toBeInTheDocument()
})

it("keeps a successful restore removed when refresh fails and allows a later new removal", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  client.getRecycledHistory.mockResolvedValueOnce(page([FIRST, SECOND]))
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValueOnce(page([FIRST, SECOND]))
    .mockResolvedValueOnce(page([{ ...FIRST, removed_at: "2026-10-30T10:11:12Z" }, SECOND]))
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={vi.fn()} />)
  await user.click(await screen.findByRole("button", { name: "恢复 功能测试任务" }))
  await screen.findByRole("alert")
  expect(screen.queryByRole("button", { name: "恢复 功能测试任务" })).not.toBeInTheDocument()
  expect(screen.getByRole("button", { name: "恢复 性能测试报告" })).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "重试" }))
  await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument())
  expect(screen.queryByRole("button", { name: "恢复 功能测试任务" })).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "刷新" }))
  expect(await screen.findByRole("button", { name: "恢复 功能测试任务" })).toBeInTheDocument()
})

it("keeps restoration successful when other pages fail to refresh and supports going back", async () => {
  const user = userEvent.setup()
  const client = makeClient()
  client.getRecycledHistory.mockResolvedValueOnce(page([FIRST]))
  const back = vi.fn()
  render(<HistoryRecycleWorkspace client={client} onHistoryChanged={vi.fn().mockRejectedValue(new Error("private callback failure"))} onBack={back} />)
  await user.click(await screen.findByRole("button", { name: "恢复 功能测试任务" }))
  expect(await screen.findByRole("alert")).toHaveTextContent("记录已恢复，其他页面暂未刷新")
  expect(screen.queryByText(/private callback/)).not.toBeInTheDocument()
  expect(screen.queryByRole("button", { name: "恢复 功能测试任务" })).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "返回" }))
  expect(back).toHaveBeenCalledOnce()
})

it("keeps recycle namespace translations and interpolation variables in parity", () => {
  const flatten = (value: object): string[] => Object.entries(value).flatMap(([key, child]) =>
    typeof child === "string" ? [`${key}:${[...child.matchAll(/{{\s*(\w+)\s*}}/g)].map(match => match[1]).sort().join(",")}`]
      : flatten(child).map(nested => `${key}.${nested}`))
  expect(flatten(en).sort()).toEqual(flatten(zh).sort())
})
