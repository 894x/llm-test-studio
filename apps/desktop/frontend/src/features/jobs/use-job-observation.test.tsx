import { act, renderHook } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { executionClient, jobFixture, JOB_ID, RUN_ID } from "@/test/jobs-fixture"
import { useJobObservation } from "./use-job-observation"

afterEach(() => vi.useRealTimers())

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
