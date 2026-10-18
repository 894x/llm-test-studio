import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, expect, it, vi } from "vitest"
import { DesktopClientError } from "@/app/desktop-client"
import { EMPTY_REPORTS } from "@/features/reports/data"
import { controlFixture, executionClient, jobFixture, JOB_ID, REPORT_ID, submissionFixture } from "@/test/jobs-fixture"
import type { QuickPerformanceReport } from "@/features/quick-test/data"
import { RunWorkspace } from "./execution-workspace"
import { FIXTURE_WORKSPACE, FIXTURE_REPORTS } from "./fixtures"
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
  expect(await screen.findByRole("tab", { name: "摘要" })).toHaveAttribute("aria-selected", "true")
  expect(screen.getByRole("button", { name: "查看报告" })).toBeDisabled()
  fireEvent.mouseDown(screen.getByRole("tab", { name: "执行" }), { button: 0, ctrlKey: false })
  expect(await screen.findByRole("region", { name: "执行详情" })).toHaveTextContent("任务状态尚不确定")
  expect(screen.queryByRole("button", { name: "任务" })).not.toBeInTheDocument()
  view.unmount()
  render(<RunWorkspace {...props} />)
  fireEvent.mouseDown(await screen.findByRole("tab", { name: "执行" }), { button: 0, ctrlKey: false })
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
  expect(screen.getByRole("tabpanel", { name: "摘要" })).toHaveTextContent("当前展示最后一次确认的数据")
  fireEvent.mouseDown(screen.getByRole("tab", { name: "执行" }), { button: 0, ctrlKey: false })
  const details = screen.getByRole("region", { name: "执行详情" })
  expect(details).toHaveTextContent("当前展示最后一次确认的数据")
  expect(within(details).getByRole("button", { name: "取消运行" })).toBeDisabled()
  fireEvent.click(within(details).getByRole("button", { name: "重试原命令" }))
  await act(async () => {})
  expect(client.retryJobControl).toHaveBeenCalledExactlyOnceWith(command.command_id)
  expect(client.controlJob).not.toHaveBeenCalled()
})

it("shows performance metadata by default and keeps one observer across tabs and report navigation", async () => {
  const user = userEvent.setup()
  const report: QuickPerformanceReport = {
    schema_version: 2, model_id: "performance-model", base_url: "https://channel.test/v1", endpoint: "",
    execution_origin: null, protocol: "openai-chat", address_mode: "base_url", success: false, archived: true, archive_status: "archived",
    profile: { load_mode: "fixed_concurrency", concurrency: 2, request_count: 10, duration_ms: 0, timeout_ms: 30000, input_tokens: 10, output_tokens: 10 },
    progress: { phase: "completed", planned: 10, launched: 10, completed: 10, in_flight: 0, total_duration_ms: 12000,
      peak_in_flight: 2, succeeded: 9, failed: 1, rejected: 0, send_duration_ms: 11000, drain_duration_ms: 1000 },
    metrics: {
      completed: 10, succeeded: 9, failed: 1, timed_out: 0, success_rate_percent: 90,
      request_qps: 1, rpm: 60, input_tpm: 600, output_tpm: 600, total_tpm: 1200, generation_tps: 10,
      ttft_p50_ms: 10, ttft_p90_ms: 20, ttft_p95_ms: 30, ttft_p99_ms: 40, ttft_average_ms: 20,
      tpot_p50_ms: 1, tpot_p90_ms: 2, tpot_p95_ms: 3, tpot_p99_ms: 4, tpot_average_ms: 2,
      e2e_p50_ms: 100, e2e_p90_ms: 200, e2e_p95_ms: 300, e2e_p99_ms: 400, e2e_average_ms: 200,
      schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 0, schedule_lag_p95_ms: 0, schedule_lag_p99_ms: 0, schedule_lag_average_ms: 0,
      prompt_tokens: 100, completion_tokens: 90, cached_tokens: 0, cache_rate_percent: 0,
    },
    failures: [{ error_code: "request_failed", count: 1 }], samples: [],
  }
  const client = executionClient(report)
  const job = await client.getJob(JOB_ID)
  client.getJob.mockClear()
  client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
  const onOpenReport = vi.fn()
  render(<RunWorkspace client={client} snapshot={empty} preferredJob={null} commandError="" onOpenReport={onOpenReport}
    reports={{ schema_version: 1, reports: [{ ...FIXTURE_REPORTS.reports[0], id: REPORT_ID, source: "quick_performance", run_id: undefined }] }} />)
  const summary = await screen.findByRole("tabpanel", { name: "摘要" })
  await waitFor(() => expect(summary).toHaveTextContent("performance-model"))
  expect(summary).toHaveTextContent("https://channel.test/v1")
  expect(summary).toHaveTextContent("并发 2")
  expect(summary).toHaveTextContent("10/10 已完成")
  expect(summary).toHaveTextContent("00:12")
  expect(screen.queryByText("固定执行器")).not.toBeInTheDocument()
  expect(screen.queryByRole("region", { name: "性能报告" })).not.toBeInTheDocument()
  for (const name of ["执行", "失败", "产物", "摘要"]) {
    await user.click(screen.getByRole("tab", { name }))
    expect(screen.getByRole("tabpanel", { name })).toBeVisible()
    if (name === "执行") {
      expect(screen.getByText("固定执行器")).toBeVisible()
      expect(screen.queryByRole("region", { name: "真实性能结果" })).not.toBeInTheDocument()
    }
    expect(screen.getByRole("button", { name: "查看报告" })).toBeEnabled()
  }
  expect(client.getJobPerformanceResult).toHaveBeenCalledTimes(1)
  expect(client.getJobControls).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole("button", { name: "查看报告" }))
  expect(onOpenReport).toHaveBeenCalledExactlyOnceWith(REPORT_ID)
  expect(client.launchJob).not.toHaveBeenCalled()
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
