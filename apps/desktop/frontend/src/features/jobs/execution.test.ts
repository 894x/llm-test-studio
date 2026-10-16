import { describe, expect, it, vi } from "vitest"
import { DesktopClientError } from "@/app/desktop-client"
import { executionClient, jobFixture, RUN_ID } from "@/test/jobs-fixture"
import { controlRun, jobControlRevision, launchAtDefault } from "./execution"

describe("durable job submission and controls", () => {
  const payload = { kind: "plan" as const, plan: { plan_id: RUN_ID, model_id: RUN_ID, channel_id: RUN_ID }, quick_task: null, performance: null, comparison: null }

  it("reads the persisted default on every explicit launch", async () => {
    const client = executionClient()
    const settings = await client.getRemoteNodes()
    client.getRemoteNodes.mockResolvedValueOnce({ ...settings, default: { kind: "remote", configuration_id: RUN_ID } })
    await launchAtDefault(client, payload)
    expect(client.launchJob).toHaveBeenLastCalledWith({ ...payload, location: { kind: "remote", configuration_id: RUN_ID } })
    await launchAtDefault(client, payload)
    expect(client.launchJob).toHaveBeenLastCalledWith({ ...payload, location: { kind: "local", configuration_id: "" } })
  })

  it("never replaces a failed remote launch with a local launch or a new ID", async () => {
    const client = executionClient()
    client.launchJob.mockRejectedValueOnce(new DesktopClientError("job_unavailable"))
    await expect(launchAtDefault(client, payload)).rejects.toMatchObject({ code: "job_unavailable" })
    expect(client.launchJob).toHaveBeenCalledTimes(1)
    client.getRemoteNodes.mockRejectedValueOnce(new DesktopClientError("remote_nodes_unavailable"))
    await expect(launchAtDefault(client, payload)).rejects.toMatchObject({ code: "remote_nodes_unavailable" })
    expect(client.launchJob).toHaveBeenCalledTimes(1)
  })

  it("uses only the authoritative job receipt or synchronized revision", () => {
    const job = jobFixture({ confirmation: { ...jobFixture().confirmation!, revision: 4 } })
    expect(jobControlRevision(job)).toBe(4)
    expect(jobControlRevision({ ...job, execution: null })).toBe(4)
    expect(jobControlRevision({ ...job, execution: null, confirmation: null })).toBeNull()
  })

  it("maps a run through persisted submissions and refreshes before control", async () => {
    const client = executionClient()
    const job = jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [] })
    client.getJobs.mockResolvedValueOnce({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
    client.getJob.mockResolvedValueOnce({ ...job, execution: { ...job.execution!, revision: 6 } })
    await controlRun(client, RUN_ID, "stop_sending")
    expect(client.controlJob).toHaveBeenCalledExactlyOnceWith({ job_id: job.job_id, kind: "stop_sending", expected_revision: 6 })
  })

  it("refuses unmanaged historical runs and does not send local controls", async () => {
    const client = executionClient()
    await expect(controlRun(client, RUN_ID, "cancel")).rejects.toMatchObject({ code: "job_not_found" })
    expect(client.controlJob).not.toHaveBeenCalled()
  })

  it("does not rebase or resubmit a refused revision", async () => {
    const client = executionClient()
    const job = jobFixture({ run_ids: [RUN_ID] })
    client.getJobs.mockResolvedValueOnce({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
    client.controlJob.mockRejectedValueOnce(new DesktopClientError("job_conflict"))
    await expect(controlRun(client, RUN_ID, "cancel")).rejects.toMatchObject({ code: "job_conflict" })
    expect(client.controlJob).toHaveBeenCalledTimes(1)
    expect(client.getJob).toHaveBeenCalledTimes(1)
  })

  it("bounds mapping queries even when a backend repeats a continuation", async () => {
    const client = executionClient()
    client.getJobs = vi.fn(async () => ({ schema_version: 1 as const, jobs: [], next_cursor: RUN_ID, has_more: true }))
    await expect(controlRun(client, RUN_ID, "cancel")).rejects.toMatchObject({ code: "job_query_unavailable" })
    expect(client.getJobs).toHaveBeenCalledTimes(2)
  })

  it("can control an older active job past the tenth bounded history page", async () => {
    const client = executionClient()
    const job = jobFixture({ run_ids: [RUN_ID] })
    for (let index = 0; index < 10; index++) {
      client.getJobs.mockResolvedValueOnce({ schema_version: 1, jobs: [], next_cursor: `a1111111-1111-4111-8111-${String(index).padStart(12, "0")}`, has_more: true })
    }
    client.getJobs.mockResolvedValueOnce({ schema_version: 1, jobs: [job], next_cursor: "", has_more: false })
    await controlRun(client, RUN_ID, "cancel")
    expect(client.getJobs).toHaveBeenCalledTimes(11)
    expect(client.controlJob).toHaveBeenCalledExactlyOnceWith({ job_id: job.job_id, kind: "cancel", expected_revision: 1 })
  })
})
