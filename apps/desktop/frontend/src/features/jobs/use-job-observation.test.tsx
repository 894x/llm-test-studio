import { act, renderHook } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { controlFixture, executionClient, jobFixture, JOB_ID, RUN_ID } from "@/test/jobs-fixture"
import { useJobObservation } from "./use-job-observation"

afterEach(() => vi.useRealTimers())

it("finds and refreshes controls beyond the first UUID-ordered page with bounded reads", async () => {
  vi.useFakeTimers()
  const client = executionClient()
  const older = Array.from({ length: 25 }, (_, index) => controlFixture({
    command_id: `11111111-1111-4111-8111-${index.toString(16).padStart(12, "0")}`,
    state: "rejected", observation: "rejected",
    created_at: new Date(Date.UTC(2026, 9, 29, 0, 0, index)).toISOString(),
  }))
  let target = controlFixture({ command_id: "ffffffff-ffff-4fff-8fff-ffffffffffff", created_at: "2026-10-29T01:00:00Z" })
  const cursor = older.at(-1)!.command_id
  client.getJobControls.mockImplementation(async (id, after) => ({ schema_version: 1, job_id: id,
    commands: after ? [target] : older, next_cursor: after ? target.command_id : cursor, has_more: !after }))
  const { result } = renderHook(() => useJobObservation(client, JOB_ID, "", 0))
  await act(async () => {})
  expect(result.current.controlSummary?.ready).toBe(false)
  expect(client.getJobControls).toHaveBeenCalledTimes(1)
  await act(async () => vi.advanceTimersByTimeAsync(1500))
  expect(result.current.controlSummary?.ready).toBe(true)
  expect(result.current.controlSummary?.commands[0].command_id).toBe(target.command_id)
  expect(result.current.controlSummary?.commands[0].state).toBe("pending")
  target = { ...target, state: "rejected", observation: "rejected", delivery_revision: 2 }
  await act(async () => vi.advanceTimersByTimeAsync(3000))
  expect(result.current.controlSummary?.commands[0].state).toBe("rejected")
  expect(result.current.controlSummary?.commands.some(command => command.state === "pending")).toBe(false)
  expect(client.getJobControls).toHaveBeenCalledWith(JOB_ID, cursor, 25)
})

it("keeps current pending control visible while browsing an older history page", async () => {
  const client = executionClient()
  const pending = controlFixture()
  client.getJobControls.mockImplementation(async (id, after) => ({ schema_version: 1, job_id: id,
    commands: after ? [] : [pending], next_cursor: after, has_more: false }))
  const { result } = renderHook(() => useJobObservation(client, JOB_ID, "old-page", 0))
  await act(async () => {})
  expect(result.current.controls?.commands).toEqual([])
  expect(result.current.controlSummary?.commands).toEqual([pending])
  expect(client.getJobControls).toHaveBeenCalledWith(JOB_ID, "", 25)
})

it("observes a terminal functional job until the caller confirms local report availability", async () => {
  vi.useFakeTimers()
  const client = executionClient()
  const job = jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [] })
  job.execution!.status = "completed"
  client.getJob.mockResolvedValue(job)
  const { result, rerender } = renderHook(({ waiting }) => useJobObservation(client, JOB_ID, "", 0, waiting), {
    initialProps: { waiting: true },
  })
  await act(async () => {})
  expect(result.current.job?.execution?.status).toBe("completed")
  await act(async () => vi.advanceTimersByTimeAsync(3000))
  expect(client.getJob.mock.calls.length).toBeGreaterThan(1)
  rerender({ waiting: false })
  await act(async () => {})
  const reads = client.getJob.mock.calls.length
  await act(async () => vi.advanceTimersByTimeAsync(6000))
  expect(client.getJob).toHaveBeenCalledTimes(reads)
})
