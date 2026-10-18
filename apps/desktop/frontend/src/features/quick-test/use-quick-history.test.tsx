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
  it("deduplicates concurrent loads, retries the failed cursor and retains all pages on refresh", async () => {
    const getQuickTestHistory = vi.fn().mockResolvedValueOnce(page(1, "next"))
    const client = { getQuickTestHistory }
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
    getQuickTestHistory.mockResolvedValueOnce(page(4, "new-next")).mockResolvedValueOnce(page(1, "next")).mockResolvedValueOnce(page(2, "last"))
    await act(() => result.current.refresh())
    expect(result.current.data.runs).toHaveLength(4)
    expect(result.current.data.next_cursor).toBe("last")
  })

  it("refreshes after an invalidation during an in-flight page and retries failed refreshes from the head", async () => {
    let finish!: (value: QuickHistoryPage) => void
    const getQuickTestHistory = vi.fn().mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const client = { getQuickTestHistory }
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
    const client = { getQuickTestHistory }
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
