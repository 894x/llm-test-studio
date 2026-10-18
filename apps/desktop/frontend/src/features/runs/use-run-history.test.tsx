import { Activity, type ReactNode } from "react"
import { act, renderHook, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { FIXTURE_WORKSPACE } from "./fixtures"
import { useRunHistory } from "./use-run-history"
import type { RunHistoryPage } from "@/app/desktop-client"

afterEach(() => vi.useRealTimers())
const run = (index: number, active = false) => ({ ...FIXTURE_WORKSPACE.runs[0],
  id: `70000000-0000-4000-8000-${String(index).padStart(12, "0")}`, status: active ? "running" as const : "completed" as const })

it("keeps loaded pages and polls only active IDs, then stops at terminal state", async () => {
  let visible = true
  const first: RunHistoryPage = { schema_version: 1, runs: Array.from({ length: 25 }, (_, i) => run(i, i === 0)), next_cursor: "older" }
  const second: RunHistoryPage = { schema_version: 1, runs: Array.from({ length: 25 }, (_, i) => run(i + 25)), next_cursor: "" }
  const client = { getRunHistory: vi.fn(async (cursor: string) => cursor ? second : first),
    getRunSummaries: vi.fn(async (_ids: string[]) => ({ schema_version: 1 as const, runs: [run(0, true)], next_cursor: "" })) }
  const wrapper = ({ children }: { children: ReactNode }) => <Activity mode={visible ? "visible" : "hidden"}>{children}</Activity>
  const { result, rerender } = renderHook(() => useRunHistory(client, []), { wrapper })
  await waitFor(() => expect(result.current.snapshot.runs).toHaveLength(25))
  await act(() => result.current.loadMore())
  expect(result.current.snapshot.runs).toHaveLength(50)
  const historyReads = client.getRunHistory.mock.calls.length
  vi.useFakeTimers()
  await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
  expect(client.getRunHistory).toHaveBeenCalledTimes(historyReads)
  expect(client.getRunSummaries.mock.calls.every(([ids]) => JSON.stringify(ids) === JSON.stringify([run(0).id]))).toBe(true)
  visible = false
  rerender()
  const activeReads = client.getRunSummaries.mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(client.getRunSummaries).toHaveBeenCalledTimes(activeReads)
  client.getRunHistory.mockReturnValueOnce(new Promise(() => {}))
  client.getRunSummaries.mockResolvedValue({ schema_version: 1, runs: [run(0)], next_cursor: "" })
  visible = true
  rerender()
  expect(result.current.snapshot.runs).toHaveLength(50)
  await act(async () => {})
  const terminalReads = client.getRunSummaries.mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(10000) })
  expect(client.getRunSummaries).toHaveBeenCalledTimes(terminalReads)
  expect(result.current.snapshot.runs).toHaveLength(50)
})

it("refreshes a loaded terminal run after report generation without rereading its page", async () => {
  const original = run(0)
  const client = { getRunHistory: vi.fn(async () => ({ schema_version: 1 as const, runs: [original], next_cursor: "" })),
    getRunSummaries: vi.fn(async (_ids: string[]) => ({ schema_version: 1 as const, runs: [{ ...original, report_id: run(1).id }], next_cursor: "" })) }
  const { result } = renderHook(() => useRunHistory(client, []))
  await waitFor(() => expect(result.current.snapshot.runs).toHaveLength(1))
  await act(() => result.current.refreshIDs([original.id]))
  expect(result.current.snapshot.runs[0].report_id).toBe(run(1).id)
  expect(client.getRunHistory).toHaveBeenCalledTimes(1)
})

it("retries a failed terminal report refresh without losing retained records", async () => {
  const original = run(0)
  const client = { getRunHistory: vi.fn(async () => ({ schema_version: 1 as const, runs: [original], next_cursor: "" })),
    getRunSummaries: vi.fn(async (_ids: string[]) => ({ schema_version: 1 as const, runs: [{ ...original, report_id: run(1).id }], next_cursor: "" })) }
  const { result } = renderHook(() => useRunHistory(client, []))
  await waitFor(() => expect(result.current.snapshot.runs).toHaveLength(1))
  client.getRunSummaries.mockRejectedValueOnce(new Error("temporary"))
  vi.useFakeTimers()
  await act(() => result.current.refreshIDs([original.id]))
  expect(result.current.snapshot.runs).toHaveLength(1)
  expect(result.current.error).toBeTruthy()
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(result.current.snapshot.runs[0].report_id).toBe(run(1).id)
  expect(result.current.error).toBeFalsy()
  expect(client.getRunHistory).toHaveBeenCalledTimes(1)
})
