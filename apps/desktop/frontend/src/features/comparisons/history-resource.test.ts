import { expect, it, vi } from "vitest"
import { createComparisonHistoryResource } from "./history-resource"
import type { ComparisonSummary } from "./data"

const summary = (id: string, status: ComparisonSummary["status"] = "completed"): ComparisonSummary => ({
  id, status, created_at: "2026-01-01T00:00:00Z", plan_id: "plan", plan_name: "Plan", model_id: "model", model_name: "Model",
  channels: [{ run_id: `run-${id}`, channel_id: "channel", channel_name: "Channel", run_status: "completed", report_ready: false, passed: false, verdict: "", metrics: {} }],
})

it("refreshes only affected older groups while retaining loaded pages and cursor", async () => {
  const old = summary("old")
  const head = { schema_version: 1 as const, comparisons: [summary("new")], next_cursor: "head-cursor" }
  const read = vi.fn(async (_after?: string, ids?: string[]) => ids?.length
    ? { ...head, comparisons: [{ ...old, channels: old.channels.map(channel => ({ ...channel, report_ready: true })) }] }
    : head)
  const { resource, queueRun } = createComparisonHistoryResource(read)
  resource.set({ schema_version: 1, comparisons: [head.comparisons[0], old], next_cursor: "loaded-cursor" })
  expect(queueRun("run-old")).toEqual(["old"])
  const next = await resource.refresh()
  expect(read.mock.calls).toEqual([[""], ["", ["old"]]])
  expect(next.comparisons.find(item => item.id === "old")?.channels[0].report_ready).toBe(true)
  expect(next.next_cursor).toBe("loaded-cursor")
  read.mockClear()
  await resource.refresh()
  expect(read.mock.calls).toEqual([[""]])
})

it("keeps queued refreshes after a failed read", async () => {
  const old = summary("old")
  const head = { schema_version: 1 as const, comparisons: [summary("new")], next_cursor: "next" }
  const read = vi.fn(async (_after?: string, ids?: string[]) => ids?.length ? { ...head, comparisons: [old] } : head)
  const { resource, queueRun } = createComparisonHistoryResource(read)
  resource.set({ ...head, comparisons: [head.comparisons[0], old] })
  queueRun("run-old")
  read.mockRejectedValueOnce(new Error("temporary"))
  await expect(resource.refresh()).rejects.toThrow("temporary")
  expect(resource.getSnapshot().data?.comparisons).toHaveLength(2)
  await resource.refresh()
  expect(read).toHaveBeenLastCalledWith("", ["old"])
})
