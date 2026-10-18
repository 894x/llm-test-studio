import { Activity, type ReactNode } from "react"
import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { FIXTURE_WORKSPACE } from "@/features/runs/fixtures"
import type { QuickHistoryPage } from "./history-data"
import { useQuickHistory } from "./use-quick-history"

function page(index: number, next = "", active = false): QuickHistoryPage {
  return { schema_version: 1, reports: [], next_cursor: next, runs: [{ ...FIXTURE_WORKSPACE.runs[0],
    id: `70000000-0000-4000-8000-${String(index).padStart(12, "0")}`, source: "quick_task", status: active ? "running" : "completed" }] }
}

afterEach(() => vi.useRealTimers())

describe("quick history pages", () => {
  it("keeps a terminal run beyond the head page fresh until its report arrives", async () => {
    const old = page(1, "next", true)
    old.runs[0].report_id = undefined
    const terminal = { ...old.runs[0], status: "completed" as const }
    const getQuickTestHistory = vi.fn().mockResolvedValueOnce(old).mockResolvedValue(page(2, "next"))
    const getRunSummaries = vi.fn().mockResolvedValue({ schema_version: 1, runs: [terminal], next_cursor: "" })
    const client = { getQuickTestHistory, getRunSummaries, subscribeRemoteResults: () => () => {} }
    vi.useFakeTimers()
    const { result } = renderHook(() => useQuickHistory(client))
    await act(async () => {})
    await act(async () => vi.advanceTimersByTimeAsync(2000))
    expect(result.current.data.runs.find(run => run.id === terminal.id)?.report_id).toBeUndefined()
    getRunSummaries.mockResolvedValue({ schema_version: 1, runs: [{ ...terminal, report_id: "ready" }], next_cursor: "" })
    await act(async () => vi.advanceTimersByTimeAsync(2000))
    expect(getRunSummaries).toHaveBeenLastCalledWith(expect.arrayContaining([terminal.id]))
    expect(result.current.data.runs.find(run => run.id === terminal.id)?.report_id).toBe("ready")
  })

  it("coalesces remote imports, discovers other clients' reports while idle and unsubscribes when hidden", async () => {
    let visible = true
    let notify = () => {}
    const detach = vi.fn()
    const initial = { schema_version: 1 as const, runs: [], reports: [], next_cursor: "" }
    const client = { getQuickTestHistory: vi.fn().mockResolvedValue(initial),
      getRunSummaries: vi.fn().mockResolvedValue(initial),
      subscribeRemoteResults: vi.fn((listener: () => void) => { notify = listener; return detach }) }
    const wrapper = ({ children }: { children: ReactNode }) => <Activity mode={visible ? "visible" : "hidden"}>{children}</Activity>
    vi.useFakeTimers()
    const { result, rerender } = renderHook(() => useQuickHistory(client), { wrapper })
    await act(async () => {})
    client.getQuickTestHistory.mockResolvedValue(page(3))
    act(() => { notify(); notify(); notify() })
    await act(async () => vi.advanceTimersByTimeAsync(100))
    expect(client.getQuickTestHistory).toHaveBeenCalledTimes(2)
    expect(result.current.data.runs).toHaveLength(1)
    visible = false
    rerender()
    expect(detach).toHaveBeenCalledTimes(1)
    act(() => notify())
    await act(async () => vi.advanceTimersByTimeAsync(20_000))
    expect(client.getQuickTestHistory).toHaveBeenCalledTimes(2)
    visible = true
    rerender()
    await act(async () => {})
    expect(client.getQuickTestHistory).toHaveBeenCalledTimes(3)
    expect(client.subscribeRemoteResults).toHaveBeenCalledTimes(2)
  })

  it("recovers a missed notification with idle polling", async () => {
    const empty = { schema_version: 1 as const, runs: [], reports: [], next_cursor: "" }
    const client = { getQuickTestHistory: vi.fn().mockResolvedValue(empty),
      getRunSummaries: vi.fn().mockResolvedValue(empty), subscribeRemoteResults: () => () => {} }
    vi.useFakeTimers()
    const { result } = renderHook(() => useQuickHistory(client))
    await act(async () => {})
    client.getQuickTestHistory.mockResolvedValue(page(7))
    await act(async () => vi.advanceTimersByTimeAsync(10_000))
    expect(result.current.data.runs).toHaveLength(1)
  })

  it("deduplicates concurrent loads, retries the failed cursor and retains all pages on refresh", async () => {
    const getQuickTestHistory = vi.fn().mockResolvedValueOnce(page(1, "next"))
    const client = { subscribeRemoteResults: () => () => {}, getQuickTestHistory, getRunSummaries: vi.fn(async () => ({ schema_version: 1 as const, runs: [], next_cursor: "" })) }
    const { result } = renderHook(() => useQuickHistory(client))
    await waitFor(() => expect(result.current.loaded).toBe(true))
    let finish!: (value: QuickHistoryPage) => void
    getQuickTestHistory.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await act(async () => { void result.current.loadMore(); void result.current.loadMore() })
    expect(getQuickTestHistory).toHaveBeenCalledTimes(2)
    await act(async () => finish(page(2, "last")))
    getQuickTestHistory.mockRejectedValueOnce(new Error("unavailable"))
    await act(() => result.current.loadMore())
    expect(result.current.error).toBe(true)
    expect(result.current.data.runs).toHaveLength(2)
    expect(result.current.data.next_cursor).toBe("last")
    getQuickTestHistory.mockResolvedValueOnce(page(3))
    await act(() => result.current.retry())
    expect(getQuickTestHistory).toHaveBeenLastCalledWith("last")
    expect(result.current.data.runs).toHaveLength(3)
    getQuickTestHistory.mockResolvedValueOnce(page(4, "new-next"))
    const beforeRefresh = getQuickTestHistory.mock.calls.length
    await act(() => result.current.refresh())
    expect(result.current.data.runs).toHaveLength(4)
    expect(result.current.data.next_cursor).toBe("new-next")
    expect(getQuickTestHistory).toHaveBeenCalledTimes(beforeRefresh + 1)
  })

  it("refreshes after an invalidation during an in-flight page and retries failed refreshes from the head", async () => {
    let finish!: (value: QuickHistoryPage) => void
    const getQuickTestHistory = vi.fn().mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const client = { subscribeRemoteResults: () => () => {}, getQuickTestHistory, getRunSummaries: vi.fn(async () => ({ schema_version: 1 as const, runs: [], next_cursor: "" })) }
    const { result } = renderHook(() => useQuickHistory(client))
    await waitFor(() => expect(getQuickTestHistory).toHaveBeenCalledTimes(1))
    act(() => result.current.invalidate())
    getQuickTestHistory.mockResolvedValueOnce(page(2, "next"))
    await act(async () => finish(page(1, "next")))
    await waitFor(() => expect(result.current.data.runs).toHaveLength(2))
    expect(getQuickTestHistory.mock.calls.map(call => call[0])).toEqual(["", ""])
    getQuickTestHistory.mockRejectedValueOnce(new Error("unavailable"))
    await act(() => result.current.refresh())
    expect(result.current.error).toBe(true)
    getQuickTestHistory.mockResolvedValueOnce(page(3, "next"))
    await act(() => result.current.retry())
    expect(getQuickTestHistory).toHaveBeenLastCalledWith("")
    expect(result.current.data.runs).toHaveLength(3)
  })

  it("pauses polling while hidden and shows retained records during the return refresh", async () => {
    let visible = true
    const getQuickTestHistory = vi.fn().mockResolvedValue(page(1, "", true))
    const wrapper = ({ children }: { children: ReactNode }) => <Activity mode={visible ? "visible" : "hidden"}>{children}</Activity>
    const client = { subscribeRemoteResults: () => () => {}, getQuickTestHistory, getRunSummaries: vi.fn(async () => ({ schema_version: 1 as const, runs: [], next_cursor: "" })) }
    const { result, rerender } = renderHook(() => useQuickHistory(client), { wrapper })
    await waitFor(() => expect(result.current.loaded).toBe(true))
    vi.useFakeTimers()
    visible = false
    rerender()
    const reads = getQuickTestHistory.mock.calls.length
    await act(async () => vi.advanceTimersByTimeAsync(10_000))
    expect(getQuickTestHistory).toHaveBeenCalledTimes(reads)
    getQuickTestHistory.mockReturnValueOnce(new Promise(() => {}))
    visible = true
    rerender()
    expect(result.current.loaded).toBe(true)
    expect(result.current.data.runs).toHaveLength(1)
  })
})
