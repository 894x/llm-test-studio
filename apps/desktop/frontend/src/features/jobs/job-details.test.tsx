import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { DesktopClientError } from "@/app/desktop-client"
import { controlFixture, executionClient, jobFixture, JOB_ID, JOB_TIME, REPORT_ID } from "@/test/jobs-fixture"
import { JobDetails } from "./job-details"

afterEach(() => { vi.useRealTimers() })

describe("durable job observation", () => {
  it("shows shared remote execution and controls it without local submission fields", async () => {
    const client = executionClient()
    const job = jobFixture({ submission: null, location: { kind: "remote", configuration_id: JOB_ID },
      executor: { ...jobFixture().executor, version: "runner-r4", build_id: "b".repeat(64), os: "linux" },
      execution: { ...jobFixture().execution!, revision: 7 } })
    client.getJobs.mockResolvedValue({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
    client.getJob.mockResolvedValue(job)
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={job} />)
    const details = await screen.findByRole("region", { name: "执行详情" })
    expect(within(details).getByText("共享节点任务")).toBeVisible()
    expect(within(details).getByText("运行中")).toBeVisible()
    expect(within(details).getByText(/runner-r4/)).toHaveTextContent("b".repeat(64))
    expect(within(details).queryByText("投递状态")).not.toBeInTheDocument()
    expect(within(details).queryByText("等待执行器确认")).not.toBeInTheDocument()
    expect(within(details).queryByText("本客户端控制命令")).not.toBeInTheDocument()
    const cancel = within(details).getByRole("button", { name: "取消运行" })
    expect(cancel).toBeEnabled()
    fireEvent.click(cancel)
    await waitFor(() => expect(client.controlJob).toHaveBeenCalledExactlyOnceWith({ job_id: JOB_ID, kind: "cancel", expected_revision: 7 }))
    expect(client.launchJob).not.toHaveBeenCalled()
  })

  it("keeps an executor confirmation visible while journal synchronization is missing", async () => {
    const client = executionClient()
    const job = jobFixture({ execution: null })
    client.getJob.mockResolvedValue(job)
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={job} />)
    expect(await screen.findByText(/已确认：运行中.*执行日志待同步/)).toBeVisible()
    expect(screen.getByText(/test-current/)).toBeVisible()
    expect(client.controlJob).not.toHaveBeenCalled()
  })

  it("preserves the last actual status after a polling network failure", async () => {
    vi.useFakeTimers()
    const client = executionClient()
    const job = jobFixture()
    client.getJob.mockResolvedValueOnce(job).mockRejectedValue(new DesktopClientError("job_query_unavailable"))
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={job} />)
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(1_500) })
    expect(screen.getByText("运行中")).toBeVisible()
    expect(screen.getByText(/当前展示最后一次确认的数据/)).toBeVisible()
    expect(screen.queryByText("失败")).not.toBeInTheDocument()
  })

  it("retries the original pending command ID and never creates a replacement", async () => {
    const client = executionClient()
    const pending = controlFixture()
    client.getJobControls.mockResolvedValue({ schema_version: 1, job_id: JOB_ID, commands: [pending], next_cursor: "", has_more: false })
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={jobFixture()} />)
    const retry = await screen.findByRole("button", { name: "重试原命令" })
    expect(screen.getByRole("button", { name: "取消运行" })).toBeDisabled()
    fireEvent.click(retry)
    await waitFor(() => expect(client.retryJobControl).toHaveBeenCalledExactlyOnceWith(pending.command_id))
    expect(client.controlJob).not.toHaveBeenCalled()
  })

  it("clears old local control records when a successful query returns an empty page", async () => {
    vi.useFakeTimers()
    const client = executionClient()
    client.getJobControls.mockResolvedValueOnce({ schema_version: 1, job_id: JOB_ID, commands: [controlFixture()], next_cursor: "", has_more: false })
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={jobFixture()} />)
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole("button", { name: "重试原命令" })).toBeVisible()
    expect(screen.getByText("本客户端控制命令")).toBeVisible()
    expect(screen.getByRole("button", { name: "取消运行" })).toBeDisabled()
    await act(async () => { await vi.advanceTimersByTimeAsync(1_500) })
    expect(client.getJobControls).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole("button", { name: "重试原命令" })).not.toBeInTheDocument()
    expect(screen.queryByText("本客户端控制命令")).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "取消运行" })).toBeEnabled()
  })

  it("retains the last authoritative local commands when their next read fails", async () => {
    vi.useFakeTimers()
    const client = executionClient()
    client.getJobControls.mockResolvedValueOnce({ schema_version: 1, job_id: JOB_ID, commands: [controlFixture()], next_cursor: "", has_more: false })
      .mockRejectedValue(new DesktopClientError("job_query_unavailable"))
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={jobFixture()} />)
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(1_500) })
    expect(screen.getByRole("button", { name: "重试原命令" })).toBeVisible()
    expect(screen.getByRole("button", { name: "取消运行" })).toBeDisabled()
    expect(screen.getByText(/当前展示最后一次确认的数据/)).toBeVisible()
  })

  it("does not send a cancellation when the observer closes", async () => {
    const client = executionClient()
    const view = render(<JobDetails client={client} jobID={JOB_ID} initialJob={jobFixture()} />)
    await waitFor(() => expect(client.getJob).toHaveBeenCalled())
    view.unmount()
    expect(client.controlJob).not.toHaveBeenCalled()
    expect(client.retryJobControl).not.toHaveBeenCalled()
  })

  it("keeps successful execution reads even when control history fails", async () => {
    const client = executionClient()
    client.getJob.mockResolvedValue(jobFixture({ kind: "run", execution: { ...jobFixture().execution!, status: "completed" } }))
    client.getJobControls.mockRejectedValue(new DesktopClientError("job_query_unavailable"))
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={jobFixture()} />)
    expect(await screen.findByText("已完成")).toBeVisible()
    expect(screen.getByText(/当前展示最后一次确认的数据/)).toBeVisible()
  })

  it("stops repeated full reads after an authoritative terminal outcome", async () => {
    vi.useFakeTimers()
    const client = executionClient()
    const job = jobFixture({ kind: "run", execution: { ...jobFixture().execution!, status: "completed" } })
    client.getJob.mockResolvedValue(job)
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={job} />)
    await act(async () => { await Promise.resolve() })
    await act(async () => { await vi.advanceTimersByTimeAsync(4_500) })
    expect(client.getJob).toHaveBeenCalledTimes(1)
  })

  it("shows not-started and interrupted-without-observation without zero metrics", async () => {
    const client = executionClient()
    const job = jobFixture({ execution: { ...jobFixture().execution!, status: "cancelled" }, performance_result_ready: true })
    client.getJob.mockResolvedValue(job)
    client.getJobPerformanceResult.mockResolvedValue({ schema_version: 1, job_id: JOB_ID, report_id: REPORT_ID,
      location: job.location, executor: job.executor, terminal: job.execution!, sealed_at: JOB_TIME,
      execution_started: true, observation_state: "interrupted", observation: null })
    const measured = vi.fn()
    render(<JobDetails client={client} jobID={JOB_ID} initialJob={job} onOpenReport={vi.fn()} renderPerformance={measured} />)
    expect(await screen.findByText("执行被中断，展示最后一次真实观测")).toBeVisible()
    expect(screen.getByText(/执行已开始，但未留下可恢复的观测/)).toBeVisible()
    expect(measured).not.toHaveBeenCalled()
    expect(screen.queryByRole("button", { name: "打开已同步报告" })).not.toBeInTheDocument()
  })

})
