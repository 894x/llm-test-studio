import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { DesktopClientError } from "@/app/desktop-client"
import { EMPTY_REPORTS } from "@/features/reports/data"
import { controlFixture, executionClient, jobFixture, JOB_ID, submissionFixture } from "@/test/jobs-fixture"
import { RunWorkspace } from "./execution-workspace"
import { FIXTURE_WORKSPACE } from "./fixtures"
import type { WorkspaceSnapshot } from "./data"

const empty: WorkspaceSnapshot = { schema_version: 1, plans: [], runs: [] }
beforeEach(() => {
  const media = window.matchMedia("(min-width: 1180px)")
  vi.spyOn(window, "matchMedia").mockImplementation(query => ({ ...media, matches: query === "(min-width: 1180px)" }))
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

it("lists a pending remote submission before a Run exists and recovers it after remount", async () => {
  const client = executionClient()
  const job = jobFixture({ location: { kind: "remote", configuration_id: JOB_ID }, execution: null,
    submission: submissionFixture({ delivery: "pending", confirmation: null, observation: "unreachable" }) })
  client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
  client.getJob.mockResolvedValue(job)
  const props = { client, snapshot: empty, reports: EMPTY_REPORTS, preferredJob: null, commandError: "", onOpenReport: vi.fn() }
  const view = render(<RunWorkspace {...props} />)
  expect(await screen.findByRole("region", { name: "执行详情" })).toHaveTextContent("任务状态尚不确定")
  expect(screen.queryByRole("button", { name: "任务" })).not.toBeInTheDocument()
  view.unmount()
  render(<RunWorkspace {...props} />)
  expect(await screen.findByRole("region", { name: "执行详情" })).toHaveTextContent(JOB_ID)
  expect(client.getJobs).toHaveBeenCalledWith("", 25)
  expect(client.launchJob).not.toHaveBeenCalled()
  expect(client.controlJob).not.toHaveBeenCalled()
})

it("shows a comparison once, selects its child runs, and controls its owning Job", async () => {
  const user = userEvent.setup()
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  snapshot.runs = snapshot.runs.slice(0, 2).map(run => ({ ...run, job_id: JOB_ID }))
  const job = jobFixture({ kind: "comparison", run_ids: snapshot.runs.map(run => run.id), comparison_id: JOB_ID,
    location: { kind: "remote", configuration_id: JOB_ID }, submission: null })
  const client = executionClient()
  client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
  client.getJob.mockResolvedValue(job)
  render(<RunWorkspace client={client} snapshot={snapshot} reports={EMPTY_REPORTS} preferredJob={null} commandError="" onOpenReport={vi.fn()} />)
  const table = await screen.findByRole("table", { name: "运行记录" })
  await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(2))
  expect(within(table).getByText("2 个子运行")).toBeVisible()
  await user.click(screen.getByRole("combobox", { name: "查看子运行" }))
  await user.click(screen.getByRole("option", { name: `${snapshot.runs[1].channel_name} · ${snapshot.runs[1].plan_name}` }))
  await user.click(screen.getByRole("tab", { name: "执行" }))
  const details = await screen.findByRole("region", { name: "执行详情" })
  expect(details).toHaveTextContent("共享节点任务")
  await user.click(within(details).getByRole("button", { name: "取消运行" }))
  expect(client.controlJob).toHaveBeenCalledWith({ job_id: JOB_ID, kind: "cancel", expected_revision: 1 })
  expect(client.launchJob).not.toHaveBeenCalled()
})

it("keeps owned runs out of standalone history when their Job is on a later page", async () => {
  const user = userEvent.setup()
  const client = executionClient()
  const first = jobFixture()
  const nextID = "a2222222-2222-4222-8222-222222222222"
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  snapshot.runs = [{ ...snapshot.runs[0], job_id: nextID }]
  const second = jobFixture({ job_id: nextID, kind: "run", run_ids: [snapshot.runs[0].id] })
  client.getJobs.mockImplementation(async cursor => ({ schema_version: 1, jobs: [cursor ? second : first], next_cursor: cursor ? nextID : JOB_ID, has_more: !cursor }))
  client.getJob.mockImplementation(async id => id === nextID ? second : first)
  render(<RunWorkspace client={client} snapshot={snapshot} reports={EMPTY_REPORTS} preferredJob={null} commandError="" onOpenReport={vi.fn()} />)
  const table = await screen.findByRole("table", { name: "运行记录" })
  expect(within(table).getAllByRole("row")).toHaveLength(2)
  expect(within(table).queryByText(snapshot.runs[0].plan_name)).not.toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "加载更多" }))
  await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(3))
  expect(within(table).getAllByText(snapshot.runs[0].plan_name)).toHaveLength(1)
  expect(client.getJobs).toHaveBeenCalledWith(JOB_ID, 25)
})

it("retains the last remote state on read failure and retries the original control command", async () => {
  vi.useFakeTimers()
  const client = executionClient()
  const job = jobFixture()
  client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
  client.getJob.mockResolvedValueOnce(job).mockRejectedValue(new DesktopClientError("job_query_unavailable"))
  const command = controlFixture()
  client.getJobControls.mockResolvedValue({ schema_version: 1, job_id: JOB_ID, commands: [command], next_cursor: "", has_more: false })
  render(<RunWorkspace client={client} snapshot={empty} reports={EMPTY_REPORTS} preferredJob={null} commandError="" onOpenReport={vi.fn()} />)
  await act(async () => {})
  await act(async () => { await vi.advanceTimersByTimeAsync(1_500) })
  const details = screen.getByRole("region", { name: "执行详情" })
  expect(details).toHaveTextContent("当前展示最后一次确认的数据")
  expect(within(details).getByRole("button", { name: "取消运行" })).toBeDisabled()
  fireEvent.click(within(details).getByRole("button", { name: "重试原命令" }))
  await act(async () => {})
  expect(client.retryJobControl).toHaveBeenCalledExactlyOnceWith(command.command_id)
  expect(client.controlJob).not.toHaveBeenCalled()
})

it("does not replace a confirmed submission with an older copy from an overlapping history page", async () => {
  const user = userEvent.setup()
  const client = executionClient()
  const accepted = jobFixture({ execution: null, submission: submissionFixture({ delivery_revision: 3 }) })
  const pending = jobFixture({ execution: null, submission: submissionFixture({
    delivery_revision: 2, delivery: "pending", confirmation: null,
  }) })
  client.getJobs.mockImplementation(async cursor => ({ schema_version: 1,
    jobs: [cursor ? pending : accepted], next_cursor: JOB_ID, has_more: !cursor }))
  client.getJob.mockResolvedValue(accepted)
  render(<RunWorkspace client={client} snapshot={empty} reports={EMPTY_REPORTS} preferredJob={null} commandError="" onOpenReport={vi.fn()} />)
  await user.click(await screen.findByRole("button", { name: "加载更多" }))
  await waitFor(() => expect(screen.queryByRole("button", { name: "加载更多" })).not.toBeInTheDocument())
  const table = screen.getByRole("table", { name: "运行记录" })
  expect(table).toHaveTextContent("执行器已确认")
  expect(table).not.toHaveTextContent("已投递，等待确认")
  expect(within(table).getAllByRole("row")).toHaveLength(2)
})
