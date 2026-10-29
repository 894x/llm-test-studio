import { act, render, renderHook, screen, waitFor } from "@testing-library/react"
import { expect, it } from "vitest"
import { executionClient, jobFixture, JOB_ID } from "@/test/jobs-fixture"
import { RetainedWorkspace } from "@/features/shell/retained-workspace"
import type { JobPage } from "@/features/jobs/data"
import { useExecutionJobs } from "./use-execution-jobs"

it("hides a canonical recycled Job even when retained as the preferred observation, and restores from Core", async () => {
  const client = executionClient()
  const job = jobFixture({ execution: { status: "completed", revision: 2, latest_sequence: 2, accepted_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:01Z", failure: null }, performance_result_ready: true })
  client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], has_more: false, next_cursor: "" })
  const { result } = renderHook(() => useExecutionJobs(client, job))
  await waitFor(() => expect(result.current.pending).toBe(false))
  const entry = { kind: "job" as const, id: JOB_ID, title: "saved", removed_at: "2026-10-30T00:00:00Z", run_ids: [], report_ids: [], job_ids: [JOB_ID], comparison_ids: [], remote: true }
  act(() => { void result.current.applyHistoryChange(entry, "recycle") })
  expect(result.current.jobs).toEqual([])
  act(() => { result.current.retry() })
  await waitFor(() => expect(result.current.pending).toBe(false))
  expect(result.current.jobs).toEqual([])
  act(() => { void result.current.applyHistoryChange({ ...entry, removed_at: "" }, "restore") })
  await waitFor(() => expect(result.current.jobs.map(item => item.job_id)).toEqual([JOB_ID]))
})

it("starts a fresh page on restore and rejects a late page read while the Job was hidden", async () => {
  const client = executionClient()
  const job = jobFixture({ execution: { status: "completed", revision: 2, latest_sequence: 2, accepted_at: "2026-10-29T00:00:00Z", updated_at: "2026-10-29T00:00:01Z", failure: null }, performance_result_ready: true })
  const visible: JobPage = { schema_version: 1, jobs: [job], has_more: false, next_cursor: "" }
  client.getJobs.mockResolvedValue(visible)
  const { result } = renderHook(() => useExecutionJobs(client, null))
  await waitFor(() => expect(result.current.pending).toBe(false))
  const entry = { kind: "job" as const, id: JOB_ID, title: "saved", removed_at: "2026-10-30T00:00:00Z", run_ids: [], report_ids: [], job_ids: [JOB_ID], comparison_ids: [], remote: true }
  act(() => { void result.current.applyHistoryChange(entry, "recycle") })
  let finish!: (value: JobPage) => void
  client.getJobs.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  act(() => result.current.retry())
  await waitFor(() => expect(client.getJobs).toHaveBeenCalledTimes(2))
  act(() => { void result.current.applyHistoryChange({ ...entry, removed_at: "" }, "restore") })
  await waitFor(() => expect(client.getJobs).toHaveBeenCalledTimes(3))
  await waitFor(() => expect(result.current.jobs.map(item => item.job_id)).toEqual([JOB_ID]))
  await act(async () => finish({ ...visible, jobs: [] }))
  expect(result.current.jobs.map(item => item.job_id)).toEqual([JOB_ID])
})

it("shares an in-flight history read across a rapid hide and return", async () => {
  const client = executionClient()
  let finish!: (value: JobPage) => void
  client.getJobs.mockReturnValue(new Promise(resolve => { finish = resolve }))
  function History() {
    const history = useExecutionJobs(client, null)
    return <div><output>{history.jobs.map(job => job.job_id).join(",")}</output><button disabled={history.pending}>Refresh</button></div>
  }
  const page = (active: boolean) => <RetainedWorkspace active={active} fallback="Loading"><History /></RetainedWorkspace>
  const view = render(page(true))
  await act(async () => {})
  view.rerender(page(false))
  view.rerender(page(true))
  await act(async () => {})
  expect(client.getJobs).toHaveBeenCalledTimes(1)
  await act(async () => { finish({ schema_version: 1, jobs: [jobFixture()], has_more: false, next_cursor: "" }) })
  expect(screen.getByRole("status")).toHaveTextContent(JOB_ID)
  expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled()
})
