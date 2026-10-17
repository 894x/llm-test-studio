import { act, render, screen } from "@testing-library/react"
import { expect, it } from "vitest"
import { executionClient, jobFixture, JOB_ID } from "@/test/jobs-fixture"
import { RetainedWorkspace } from "@/features/shell/retained-workspace"
import type { JobPage } from "@/features/jobs/data"
import { useExecutionJobs } from "./use-execution-jobs"

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
