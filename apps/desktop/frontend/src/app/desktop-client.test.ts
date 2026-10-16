import { protocolReportFixture } from "@/test/protocol-report-fixture"
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  FIXTURE_CATALOG,
  FIXTURE_REPORTS,
  FIXTURE_WORKSPACE,
} from "@/features/runs/fixtures"
import { EMPTY_COMPARISONS } from "@/features/comparisons/data"
import { parseQuickPerformanceReport } from "@/features/quick-test/data"

import { createDesktopClient, createFixtureClient, singleFlightRead } from "./desktop-client"
import { DesktopDataError } from "./data-error"
import type { LaunchCommand } from "@/features/jobs/data"
import { jobFixture, submissionFixture, controlFixture, performanceResultFixture, JOB_ID, JOB_TIME, COMMAND_ID, RUN_ID } from "@/test/jobs-fixture"
import { EMPTY_REMOTE_NODES } from "@/features/remote-nodes/data"
import { FILE_SELECTION } from "@/test/local-file-fixture"
import { remoteUpgradeFixture, REMOTE_CONFIGURATION_ID, UPGRADE_OPERATION_ID } from "@/test/remote-upgrade-fixture"

describe("Wails desktop client", () => {
  it("imports a bounded image document through current bindings and strictly validates its metadata", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    const command = { data_base64: "AQID", media_type: "image/png" as const }
    binding.CreateLocalFileSelection.mockResolvedValueOnce(FILE_SELECTION)
    await expect(client.createLocalFileSelection(command)).resolves.toEqual(FILE_SELECTION)
    expect(binding.CreateLocalFileSelection).toHaveBeenCalledExactlyOnceWith(JSON.stringify(command))
    await client.releaseLocalFileSelection(FILE_SELECTION.selection_id)
    expect(binding.ReleaseLocalFileSelection).toHaveBeenCalledExactlyOnceWith(FILE_SELECTION.selection_id)
    for (const response of [
      { ...FILE_SELECTION, path: "private" }, { ...FILE_SELECTION, data_base64: "AQID" },
      { ...FILE_SELECTION, selection_id: "invalid" }, { ...FILE_SELECTION, sha256: "A".repeat(64) },
      { ...FILE_SELECTION, size: 0 }, { ...FILE_SELECTION, size: 1048577 }, { ...FILE_SELECTION, size: 1.5 },
      { ...FILE_SELECTION, media_type: "image/gif" }, { ...FILE_SELECTION, media_type: "image/jpeg" },
    ]) {
      binding.CreateLocalFileSelection.mockResolvedValueOnce(response)
      await expect(client.createLocalFileSelection(command)).rejects.toBeInstanceOf(DesktopDataError)
    }
    for (const code of ["file_input_invalid", "file_input_too_large", "file_input_limit", "file_input_busy", "file_input_not_found", "file_input_unavailable"]) {
      binding.CreateLocalFileSelection.mockRejectedValueOnce(code)
      await expect(client.createLocalFileSelection(command)).rejects.toMatchObject({ code })
    }
    const count = binding.CreateLocalFileSelection.mock.calls.length
    await expect(client.createLocalFileSelection({ ...command, path: "private" } as never)).rejects.toMatchObject({ code: "file_input_invalid" })
    await expect(client.createLocalFileSelection({ ...command, data_base64: "!bad" })).rejects.toMatchObject({ code: "file_input_invalid" })
    await expect(client.createLocalFileSelection({ ...command, data_base64: "A".repeat(1398108) })).rejects.toMatchObject({ code: "file_input_too_large" })
    await expect(client.createLocalFileSelection({ ...command, data_base64: "A".repeat(1398104) })).rejects.toMatchObject({ code: "file_input_too_large" })
    expect(binding.CreateLocalFileSelection).toHaveBeenCalledTimes(count)
    const maximum = { ...command, data_base64: "A".repeat(1398102) + "==" }
    binding.CreateLocalFileSelection.mockResolvedValueOnce({ ...FILE_SELECTION, size: 1048576 })
    await expect(client.createLocalFileSelection(maximum)).resolves.toMatchObject({ size: 1048576 })
    expect(new TextEncoder().encode(binding.CreateLocalFileSelection.mock.lastCall![0]).byteLength).toBeLessThanOrEqual(1400000)
    await expect(client.releaseLocalFileSelection("private-path")).rejects.toMatchObject({ code: "file_input_invalid" })
    binding.ReleaseLocalFileSelection.mockRejectedValueOnce("file_input_not_found")
    await expect(client.releaseLocalFileSelection(FILE_SELECTION.selection_id)).rejects.toMatchObject({ code: "file_input_not_found" })
  })

  it("keeps local file selection explicitly unavailable in fixture previews", async () => {
    const client = createFixtureClient(FIXTURE_WORKSPACE)
    await expect(client.createLocalFileSelection({ data_base64: "AQID", media_type: "image/png" })).rejects.toMatchObject({ code: "file_input_unavailable" })
    await expect(client.releaseLocalFileSelection(FILE_SELECTION.selection_id)).rejects.toMatchObject({ code: "file_input_unavailable" })
  })
  it("uses current explicit upgrade bindings and checks operation/configuration identity", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    const command = { id: REMOTE_CONFIGURATION_ID, expected_revision: 3, operation_id: UPGRADE_OPERATION_ID, expected_build_id: "a".repeat(64) }
    const action = { id: command.id, expected_revision: command.expected_revision, operation_id: command.operation_id }
    const view = remoteUpgradeFixture()
    binding.StartRemoteNodeUpgrade.mockResolvedValueOnce(view)
    await expect(client.startRemoteNodeUpgrade(command)).resolves.toEqual(view)
    expect(binding.StartRemoteNodeUpgrade).toHaveBeenCalledExactlyOnceWith(command)
    await expect(client.getRemoteNodeUpgrade(command.id)).resolves.toBeNull()
    binding.GetRemoteNodeUpgrade.mockResolvedValueOnce(view)
    await expect(client.getRemoteNodeUpgrade(command.id)).resolves.toEqual(view)
    expect(binding.GetRemoteNodeUpgrade).toHaveBeenLastCalledWith(command.id)
    binding.RetryRemoteNodeUpgrade.mockResolvedValueOnce(view)
    await expect(client.retryRemoteNodeUpgrade(action)).resolves.toEqual(view)
    expect(binding.RetryRemoteNodeUpgrade).toHaveBeenCalledExactlyOnceWith(action)
    binding.RecoverRemoteNodeUpgrade.mockResolvedValueOnce(view)
    await expect(client.recoverRemoteNodeUpgrade(action)).resolves.toEqual(view)
    expect(binding.RecoverRemoteNodeUpgrade).toHaveBeenCalledExactlyOnceWith(action)
    binding.StartRemoteNodeUpgrade.mockResolvedValueOnce({ ...view, operation_id: REMOTE_CONFIGURATION_ID })
    await expect(client.startRemoteNodeUpgrade(command)).rejects.toBeInstanceOf(DesktopDataError)
    binding.GetRemoteNodeUpgrade.mockResolvedValueOnce({ ...view, configuration_id: UPGRADE_OPERATION_ID })
    await expect(client.getRemoteNodeUpgrade(command.id)).rejects.toBeInstanceOf(DesktopDataError)
    binding.RetryRemoteNodeUpgrade.mockResolvedValueOnce({ ...view, operation_id: REMOTE_CONFIGURATION_ID })
    await expect(client.retryRemoteNodeUpgrade(action)).rejects.toBeInstanceOf(DesktopDataError)
    binding.RecoverRemoteNodeUpgrade.mockRejectedValueOnce("remote_node_identity_conflict")
    await expect(client.recoverRemoteNodeUpgrade(action)).rejects.toMatchObject({ code: "remote_node_identity_conflict" })
    binding.GetRemoteNodeUpgrade.mockResolvedValueOnce(undefined)
    await expect(client.getRemoteNodeUpgrade(command.id)).rejects.toBeInstanceOf(DesktopDataError)
  })
  afterEach(() => {
    Reflect.deleteProperty(window, "go")
		Reflect.deleteProperty(window, "runtime")
		vi.unstubAllEnvs()
		vi.useRealTimers()
  })

  it("uses the current remote node bindings and rejects malformed snapshots", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    await expect(client.getRemoteNodes()).resolves.toEqual(EMPTY_REMOTE_NODES)
    binding.GetRemoteNodes.mockResolvedValueOnce({ ...EMPTY_REMOTE_NODES, token_ref: "private" } as never)
    await expect(client.getRemoteNodes()).rejects.toBeInstanceOf(DesktopDataError)
    const command = { expected_revision: 1, location: { kind: "local" as const, configuration_id: "" } }
    await client.selectDefaultExecution(command)
    expect(binding.SelectDefaultExecution).toHaveBeenCalledExactlyOnceWith(command)
    binding.SelectDefaultExecution.mockRejectedValueOnce("remote_node_revision_conflict")
    await expect(client.selectDefaultExecution(command)).rejects.toMatchObject({ code: "remote_node_revision_conflict" })
  })

  it("classifies malformed English payloads by type instead of translated text", async () => {
    document.documentElement.lang = "en-US"
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetWorkspace.mockResolvedValueOnce({})
    await expect(createDesktopClient().getWorkspace()).rejects.toThrow("Unsupported desktop data protocol version")
    expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith(expect.objectContaining({ operation: "load_workspace", error_code: "frontend_data_invalid" }))
  })

  it("reads only the current report generation snapshot", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const progress = { sequence: 1, run_id: FIXTURE_WORKSPACE.runs[0].id, phase: "building", processed: 2, total: 4, elapsed_ms: 20 }
    binding.GetReportGeneration.mockResolvedValueOnce({ schema_version: 1, runs: [progress] } as never)
    const client = createDesktopClient()
    await expect(client.getReportGeneration()).resolves.toEqual({ schema_version: 1, runs: [progress] })
    binding.GetReportGeneration.mockResolvedValueOnce({ schema_version: 0, runs: [] } as never)
    await expect(client.getReportGeneration()).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("rejects malformed generation events and releases the runtime subscription", () => {
    installBinding(FIXTURE_WORKSPACE)
    let event!: (payload: unknown) => void
    const detach = vi.fn()
    const eventsOn = vi.fn((_name: string, receive: (payload: unknown) => void) => { event = receive; return detach })
    Object.assign(window, { runtime: { EventsOn: eventsOn } })
    const receive = vi.fn()
    const unsubscribe = createDesktopClient().subscribeReportGeneration(receive)
    expect(eventsOn).toHaveBeenCalledWith("report-generation-progress", expect.any(Function))
    const progress = { sequence: 1, run_id: FIXTURE_WORKSPACE.runs[0].id, phase: "building", processed: 2, total: 4, elapsed_ms: 20 }
    event({ ...progress, total: 1 })
    event({ ...progress, api_key: "private" })
    expect(receive).not.toHaveBeenCalled()
    event(progress)
    expect(receive).toHaveBeenCalledExactlyOnceWith(progress)
    unsubscribe()
    expect(detach).toHaveBeenCalledTimes(1)
  })

  it.each([
    ["getWorkspace", "GetWorkspace", FIXTURE_WORKSPACE],
    ["getCatalog", "GetCatalog", FIXTURE_CATALOG],
    ["getReports", "GetReports", FIXTURE_REPORTS],
    ["getComparisons", "GetComparisons", EMPTY_COMPARISONS],
  ] as const)("shares concurrent %s reads and performs a fresh read after settlement", async (method, bindingMethod, payload) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    let resolveRead!: (value: unknown) => void
    binding[bindingMethod].mockReturnValueOnce(new Promise((resolve) => { resolveRead = resolve }) as never)
    const client = createDesktopClient()
    const first = client[method]()
    const second = client[method]()
    expect(binding[bindingMethod]).toHaveBeenCalledTimes(1)
    expect(first).toBe(second)
    resolveRead(payload)
    await expect(first).resolves.toEqual(payload)
    await expect(second).resolves.toEqual(payload)
    await client[method]()
    expect(binding[bindingMethod]).toHaveBeenCalledTimes(2)
  })

  it("queues one fresh read after invalidation without overlapping an older request", async () => {
    let resolveOld!: (value: string) => void
    const read = vi.fn().mockReturnValueOnce(new Promise<string>((resolve) => { resolveOld = resolve })).mockResolvedValue("after command")
    const get = singleFlightRead<string>(read)
    const old = get()
    get.invalidate()
    const refreshed = get()
    const concurrent = get()
    expect(read).toHaveBeenCalledTimes(1)
    expect(refreshed).toBe(concurrent)
    resolveOld("before command")
    await expect(old).resolves.toBe("before command")
    await expect(refreshed).resolves.toBe("after command")
    await expect(concurrent).resolves.toBe("after command")
    expect(read).toHaveBeenCalledTimes(2)
  })

  it.each([
    ["getWorkspace", "GetWorkspace", FIXTURE_WORKSPACE],
    ["getCatalog", "GetCatalog", FIXTURE_CATALOG],
    ["getReports", "GetReports", FIXTURE_REPORTS],
    ["getComparisons", "GetComparisons", EMPTY_COMPARISONS],
  ] as const)("allows retry after a shared %s read rejects", async (method, bindingMethod, payload) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding[bindingMethod].mockRejectedValueOnce(new Error("temporary read failure"))
    const client = createDesktopClient()
    const first = client[method]()
    const second = client[method]()
    const results = await Promise.allSettled([first, second])
    expect(results.map((result) => result.status)).toEqual(["rejected", "rejected"])
    expect(binding[bindingMethod]).toHaveBeenCalledTimes(1)
    await expect(client[method]()).resolves.toEqual(payload)
    expect(binding[bindingMethod]).toHaveBeenCalledTimes(2)
    expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledTimes(1)
  })

  it("returns the accepted Job identity without a query and preserves pending delivery", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const pending = jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [],
      submission: submissionFixture({ delivery: "pending", observation: "unreachable", confirmation: null }), execution: null })
    binding.LaunchJob.mockResolvedValueOnce(pending)
    binding.GetJob.mockRejectedValueOnce(new Error("read unavailable"))
    const command = quickTaskCommand()
    await expect(createDesktopClient().launchJob(command)).resolves.toEqual(pending)
    expect(binding.LaunchJob).toHaveBeenCalledExactlyOnceWith(command)
    expect(binding.GetJob).not.toHaveBeenCalled()
    expect(binding.GetWorkspace).not.toHaveBeenCalled()
  })

  it.each(["", "not-a-job", null, { job_id: JOB_ID }, { ...jobFixture(), schema_version: 0 }, { ...jobFixture(), location: undefined }])("rejects malformed launch acceptance %j without retrying", async (payload) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.LaunchJob.mockResolvedValueOnce(payload as never)
    await expect(createDesktopClient().launchJob(quickTaskCommand())).rejects.toBeInstanceOf(DesktopDataError)
    expect(binding.LaunchJob).toHaveBeenCalledTimes(1)
    expect(binding.GetJob).not.toHaveBeenCalled()
  })

  it("loads only safe task history and rejects a different Run identity", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const runID = FIXTURE_WORKSPACE.runs[0].id
    const detail = { schema_version: 1, seed: 1, request_timeout_ms: 60000, case_concurrency: 4, run_id: runID, suite: FIXTURE_CATALOG.suites[1], model: "model", base_url: "https://example.test", inputs: { prompt: "edited" } }
    binding.GetQuickTask.mockResolvedValueOnce({ ...detail, api_key: "private-key", case_definitions: [{ raw: "hidden" }] })
    await expect(createDesktopClient().getQuickTask(runID)).resolves.toEqual(detail)
    expect(binding.GetQuickTask).toHaveBeenCalledExactlyOnceWith(runID)
    binding.GetQuickTask.mockResolvedValueOnce({ ...detail, run_id: "123e4567-e89b-42d3-a456-426614174099" })
    await expect(createDesktopClient().getQuickTask(runID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("forwards all seven current Job methods with stable identifiers and revisions", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    const command = quickTaskCommand()
    const control = { job_id: JOB_ID, kind: "stop_sending" as const, expected_revision: 1 }
    await expect(client.launchJob(command)).resolves.toEqual(jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [], execution: null }))
    await expect(client.getJobs("", 25)).resolves.toEqual({ schema_version: 1, jobs: [], next_cursor: "", has_more: false })
    await expect(client.getJob(JOB_ID)).resolves.toEqual(jobFixture())
    await expect(client.getJobPerformanceResult(JOB_ID)).resolves.toMatchObject({ job_id: JOB_ID, observation_state: "available" })
    await expect(client.controlJob(control)).resolves.toEqual(controlFixture())
    await expect(client.getJobControls(JOB_ID, "", 25)).resolves.toEqual({ schema_version: 1, job_id: JOB_ID, commands: [], next_cursor: "", has_more: false })
    await expect(client.retryJobControl(COMMAND_ID)).resolves.toEqual(controlFixture())
    expect(binding.LaunchJob).toHaveBeenCalledExactlyOnceWith(command)
    expect(binding.GetJobs).toHaveBeenCalledExactlyOnceWith("", 25)
    expect(binding.GetJob).toHaveBeenCalledExactlyOnceWith(JOB_ID)
    expect(binding.GetJobPerformanceResult).toHaveBeenCalledExactlyOnceWith(JOB_ID)
    expect(binding.ControlJob).toHaveBeenCalledExactlyOnceWith(control)
    expect(binding.GetJobControls).toHaveBeenCalledExactlyOnceWith(JOB_ID, "", 25)
    expect(binding.RetryJobControl).toHaveBeenCalledExactlyOnceWith(COMMAND_ID)
  })

  it.each(["local", "remote"] as const)("preserves explicit %s launch routing", async (kind) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const command = { ...quickTaskCommand(), location: { kind, configuration_id: kind === "local" ? "" : RUN_ID } }
    const result = await createDesktopClient().launchJob(command)
    expect(result.kind).toBe("run")
    expect(result.location).toEqual(command.location)
    expect(binding.LaunchJob).toHaveBeenCalledExactlyOnceWith(command)
  })

  it.each([
    jobFixture(),
    jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [], location: { kind: "remote", configuration_id: RUN_ID } }),
  ])("rejects a launch response for another kind or location without retrying", async (response) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.LaunchJob.mockResolvedValueOnce(response)
    await expect(createDesktopClient().launchJob(quickTaskCommand())).rejects.toBeInstanceOf(DesktopDataError)
    expect(binding.LaunchJob).toHaveBeenCalledTimes(1)
  })

  it("loads quick task history without an authored plan", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.plans = []
    payload.runs = [{ ...payload.runs[0], source: "quick_task", plan_id: payload.runs[0].id, load_mode: "single", planned: payload.runs[0].case_count, duration_ms: 0, completed: 0, passed: 0, failed: 0, observed: 0, indeterminate: 0 }]
    payload.active_run_id = payload.runs[0].id
    installBinding(payload)
    await expect(createDesktopClient().getWorkspace()).resolves.toEqual(payload)
  })

  it("loads snapshot-backed authored Run history after the current plan is deleted", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.plans = []
    payload.runs = [{ ...payload.runs[0], plan_name: "Deleted authored plan" }]
    payload.active_run_id = payload.runs[0].id
    const binding = installBinding(payload)
    await expect(createDesktopClient().getWorkspace()).resolves.toEqual(payload)

    binding.GetWorkspace.mockResolvedValueOnce({
      ...payload,
      runs: [{ ...payload.runs[0], plan_id: "invalid" }],
    })
    await expect(createDesktopClient().getWorkspace()).rejects.toBeInstanceOf(DesktopDataError)

    binding.GetWorkspace.mockResolvedValueOnce({
      ...payload,
      runs: [payload.runs[0], payload.runs[0]],
    })
    await expect(createDesktopClient().getWorkspace()).rejects.toBeInstanceOf(DesktopDataError)

    binding.GetWorkspace.mockResolvedValueOnce({ ...payload, active_run_id: payload.runs[0].plan_id })
    await expect(createDesktopClient().getWorkspace()).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("refuses fixture execution mutations instead of simulating a remote connection", async () => {
    const client = createFixtureClient(FIXTURE_WORKSPACE)
    const before = await client.getWorkspace()
    const remote = { ...quickTaskCommand(), location: { kind: "remote" as const, configuration_id: RUN_ID } }
    for (const command of [quickTaskCommand(), remote]) {
      await expect(client.launchJob(command)).rejects.toMatchObject({ code: "job_commands_unavailable" })
    }
    await expect(client.controlJob({ job_id: JOB_ID, kind: "cancel", expected_revision: 1 })).rejects.toMatchObject({ code: "job_commands_unavailable" })
    await expect(client.retryJobControl(COMMAND_ID)).rejects.toMatchObject({ code: "job_commands_unavailable" })
    expect(await client.getWorkspace()).toEqual(before)
  })

  it("materializes ordered plan suites in the browser fixture with generated entry ids", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.plans = []
    const suite = catalog.suites[0]
    const entry = {
      warmup_count: 0, settings: {}, target_kind: "suite" as const, target_id: suite.id,
      parameters: { prompt: "first" },
      load_mode: "fixed_concurrency" as const,
      concurrency: 2,
      request_count: 10,
      rate_per_second: 0,
      duration_ms: 0,
      request_timeout_ms: 30_000,
      sla_thresholds: { e2e_p95_ms: 2_000 },
    }
    const client = createFixtureClient(FIXTURE_WORKSPACE, catalog)

    const result = await client.createPlan({
      name: "Repeated suite fixture",
      protocol: "openai-chat", seed: 1,
      entries: [{ ...entry }, { ...entry, parameters: { prompt: "second" }, concurrency: 7 }],
    })

    const plan = result.plans[0]
    expect(plan).toMatchObject({ entry_count: 2, case_count: suite.case_count * 2 })
    expect(plan.entries[0]).toMatchObject({ parameters: { prompt: "first" }, concurrency: 2 })
    expect(plan.entries[1]).toMatchObject({ parameters: { prompt: "second" }, concurrency: 7 })
    expect(plan.entries[0].entry_id).not.toBe(plan.entries[1].entry_id)
  })

  it("resolves current target names while keeping only references in fixture plan commands", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.suites[0].name = "Updated suite"
    const client = createFixtureClient(FIXTURE_WORKSPACE, catalog)
    const plan = catalog.plans[0]
    const updated = await client.updatePlan({ id: plan.id, expected_revision: plan.revision, name: plan.name, protocol: plan.protocol, seed: plan.seed,
      entries: plan.entries.map(({ target_name: _name, target_key: _key, case_count: _count, ...entry }) => entry) })
    expect(updated.plans[0].entries[0]).toMatchObject({ target_id: catalog.suites[0].id, target_name: "Updated suite" })
    expect(updated.plans[0].entries[0]).not.toHaveProperty("suite_revision")
    expect(updated.plans[0].entries[0]).not.toHaveProperty("cases")
  })

  it("forwards explicit credential actions without restarting or refreshing the task", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    const command = { run_id: FIXTURE_WORKSPACE.runs[0].id, base_url: "https://example.test", protocol: "seedance" as const, api_key: "private-test-key" }
    await client.rememberQuickTaskCredential(command)
    await client.forgetQuickTaskCredential(command.run_id)
    expect(binding.RememberQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(command)
    expect(binding.ForgetQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(command.run_id)
    expect(binding.LaunchJob).not.toHaveBeenCalled()
    expect(binding.GetWorkspace).not.toHaveBeenCalled()
  })

  it("keeps credential history and explicit remember/forget operations independent of launches", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const runID = FIXTURE_WORKSPACE.runs[0].id
    const detail = { schema_version: 1, seed: 1, request_timeout_ms: 60000, case_concurrency: 4,
      run_id: runID, suite: FIXTURE_CATALOG.suites[1], model: "model", base_url: "https://example.test",
      credential_run_id: runID, inputs: { prompt: "hello" } }
    binding.GetQuickTask.mockResolvedValueOnce({ ...detail, api_key: "synthetic-secret" })
    const client = createDesktopClient()
    await expect(client.getQuickTask(runID)).resolves.toEqual(detail)
    await client.forgetQuickTaskCredential(runID)
    expect(binding.ForgetQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(runID)
    expect(binding.LaunchJob).not.toHaveBeenCalled()
  })

  it.each([
    { api_key: "synthetic-secret" }, { run_id: RUN_ID }, { delivery: "failed" },
    { location: { kind: "local", configuration_id: RUN_ID } },
    { executor: { ...jobFixture().executor, token: "synthetic-secret" } },
    { submission: submissionFixture({ delivery: "pending" }) },
    { submission: { ...submissionFixture(), confirmation: { ...submissionFixture().confirmation, revision: 0 } } },
    { execution: { ...jobFixture().execution, revision: 0 } },
    { run_ids: [RUN_ID, RUN_ID] }, { report_ids: null }, { prepared_at: "yesterday" },
    { performance_result_ready: true },
    { progress: { sequence: 2, recorded_at: "2026-10-16T00:00:00Z", progress: {} } },
  ])("rejects noncurrent Job data without retaining secrets %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetJob.mockResolvedValueOnce({ ...jobFixture(), ...invalid } as never)
    await expect(createDesktopClient().getJob(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("preserves a receipt ahead of journal synchronization without inventing execution", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const confirmation = jobFixture({ execution: null, progress: null })
    binding.GetJob.mockResolvedValueOnce(confirmation)
    await expect(createDesktopClient().getJob(JOB_ID)).resolves.toEqual(confirmation)
  })

  it("reads shared remote jobs without requiring a local submission or matching runner release", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const job = jobFixture({ submission: null, location: { kind: "remote", configuration_id: RUN_ID },
      executor: { ...jobFixture().executor, version: "runner-r4", build_id: "b".repeat(64), os: "linux" } })
    binding.GetJob.mockResolvedValueOnce(job)
    binding.GetJobs.mockResolvedValueOnce({ schema_version: 1, jobs: [job], next_cursor: JOB_ID, has_more: false } as never)
    const client = createDesktopClient()
    await expect(client.getJob(JOB_ID)).resolves.toEqual(job)
    await expect(client.getJobs("", 25)).resolves.toMatchObject({ jobs: [job] })
    expect(binding.LaunchJob).not.toHaveBeenCalled()
  })

  it("rejects a shared job as the result of this client's launch", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const command = { ...quickTaskCommand(), location: { kind: "remote" as const, configuration_id: RUN_ID } }
    binding.LaunchJob.mockResolvedValueOnce(jobFixture({ kind: "run", run_ids: [RUN_ID], report_ids: [], submission: null, location: command.location }))
    await expect(createDesktopClient().launchJob(command)).rejects.toBeInstanceOf(DesktopDataError)
    expect(binding.LaunchJob).toHaveBeenCalledTimes(1)
    expect(binding.GetJob).not.toHaveBeenCalled()
  })

  it("observes current Job progress through a query and rejects unexpected progress fields", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const job = jobFixture({ execution: { ...jobFixture().execution!, latest_sequence: 3 },
      progress: { sequence: 2, recorded_at: JOB_TIME, progress: parseQuickPerformanceReport(performanceReportFixture()).progress } })
    binding.GetJob.mockResolvedValueOnce(job)
    await expect(createDesktopClient().getJob(JOB_ID)).resolves.toEqual(job)
    binding.GetJob.mockResolvedValueOnce({ ...job, progress: { ...job.progress,
      progress: { ...job.progress!.progress, api_key: "synthetic-secret" } } } as never)
    await expect(createDesktopClient().getJob(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("forwards the current pagination cursor unchanged", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const page = { schema_version: 1, jobs: [jobFixture()], next_cursor: JOB_ID, has_more: true }
    binding.GetJobs.mockResolvedValueOnce(page as never)
    const client = createDesktopClient()
    await expect(client.getJobs(RUN_ID, 1)).resolves.toEqual(page)
    expect(binding.GetJobs).toHaveBeenCalledExactlyOnceWith(RUN_ID, 1)
  })

  it.each([false, true])("accepts the current GetJobs final SQL page (empty tail: %s)", async empty => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const after = empty ? JOB_ID : ""
    const page = { schema_version: 1, jobs: empty ? [] : [jobFixture()], next_cursor: JOB_ID, has_more: false }
    binding.GetJobs.mockResolvedValueOnce(page as never)
    await expect(createDesktopClient().getJobs(after, 25)).resolves.toEqual(page)
    expect(binding.GetJobs).toHaveBeenCalledExactlyOnceWith(after, 25)
  })

  it.each([
    { jobs: null }, { jobs: [jobFixture(), jobFixture()] }, { jobs: Array(26).fill(jobFixture()) },
    { jobs: [], has_more: true }, { jobs: [], next_cursor: "", has_more: true }, { jobs: [], schema_version: 0 },
    { jobs: [], raw_payload: "synthetic-secret" },
  ])("rejects invalid Job pages %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetJobs.mockResolvedValueOnce(Object.assign({ schema_version: 1, jobs: [], next_cursor: "", has_more: false }, invalid) as never)
    await expect(createDesktopClient().getJobs("", 25)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("keeps pending and rejected controls distinct from authoritative Job execution", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const command = { job_id: JOB_ID, kind: "stop_sending" as const, expected_revision: 1 }
    const client = createDesktopClient()
    await expect(client.controlJob(command)).resolves.toMatchObject({ state: "pending", execution: null })
    binding.RetryJobControl.mockResolvedValueOnce(controlFixture({ state: "rejected", observation: "rejected", delivery_revision: 2 }))
    await expect(client.retryJobControl(COMMAND_ID)).resolves.toMatchObject({ state: "rejected", execution: null })
    await expect(client.getJob(JOB_ID)).resolves.toMatchObject({ execution: { status: "running", revision: 1 } })
    expect(binding.ControlJob).toHaveBeenCalledTimes(1)
    expect(binding.RetryJobControl).toHaveBeenCalledExactlyOnceWith(COMMAND_ID)
  })

  it("keeps a confirmed command snapshot separate from the independently observed Job", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const confirmed = controlFixture({ state: "confirmed", delivery_revision: 2, confirmed_at: JOB_TIME,
      execution: { ...jobFixture().execution!, status: "draining", revision: 2, latest_sequence: 2 } })
    binding.ControlJob.mockResolvedValueOnce(confirmed)
    const client = createDesktopClient()
    await expect(client.controlJob({ job_id: JOB_ID, kind: "stop_sending", expected_revision: 1 })).resolves.toEqual(confirmed)
    await expect(client.getJob(JOB_ID)).resolves.toEqual(jobFixture())
    expect(binding.GetJob).toHaveBeenCalledExactlyOnceWith(JOB_ID)
  })

  it.each([
    { command_id: RUN_ID }, { job_id: RUN_ID }, { kind: "cancel" }, { expected_revision: 2 },
    { raw_response: "synthetic-secret" }, { state: "confirmed" }, { state: "rejected" },
    { confirmed_at: "2026-10-16T00:00:00Z" }, { execution: jobFixture().execution },
  ])("rejects malformed or miscorrelated controls %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.ControlJob.mockResolvedValueOnce({ ...controlFixture(), ...invalid } as never)
    const client = createDesktopClient()
    const request = { job_id: JOB_ID, kind: "stop_sending" as const, expected_revision: 1 }
    if (Object.hasOwn(invalid, "command_id")) {
      binding.RetryJobControl.mockResolvedValueOnce({ ...controlFixture(), ...invalid } as never)
      await expect(client.retryJobControl(COMMAND_ID)).rejects.toBeInstanceOf(DesktopDataError)
    } else await expect(client.controlJob(request)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it.each([
    { job_id: RUN_ID }, { commands: null }, { commands: [controlFixture({ job_id: RUN_ID })] },
    { commands: [controlFixture(), controlFixture()] }, { commands: [], next_cursor: "", has_more: true },
    { commands: [], has_more: true }, { commands: [], secret: "synthetic-secret" },
  ])("rejects malformed control history %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetJobControls.mockResolvedValueOnce({ schema_version: 1, job_id: JOB_ID, commands: [], next_cursor: "", has_more: false, ...invalid } as never)
    await expect(createDesktopClient().getJobControls(JOB_ID, "", 25)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it.each([false, true])("accepts the current GetJobControls final SQL page (empty tail: %s)", async empty => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const after = empty ? COMMAND_ID : ""
    const page = { schema_version: 1, job_id: JOB_ID, commands: empty ? [] : [controlFixture()], next_cursor: COMMAND_ID, has_more: false }
    binding.GetJobControls.mockResolvedValueOnce(page as never)
    await expect(createDesktopClient().getJobControls(JOB_ID, after, 25)).resolves.toEqual(page)
    expect(binding.GetJobControls).toHaveBeenCalledExactlyOnceWith(JOB_ID, after, 25)
  })

  it.each(["not_started", "interrupted", "available"] as const)("preserves the sealed %s performance outcome without an archive query", async (state) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    result.observation_state = state
    if (state === "not_started") { result.execution_started = false; result.observation = null; result.terminal.status = "cancelled" }
    if (state === "interrupted") { result.terminal.status = "cancelled"; result.observation!.stage = "warmup"; result.observation!.boundary = "entered" }
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).resolves.toEqual(result)
    expect(binding.GetReports).not.toHaveBeenCalled()
    expect(binding.GetReportDetail).not.toHaveBeenCalled()
  })

  it("preserves an interrupted execution with no actual observation instead of inventing zero metrics", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    result.observation_state = "interrupted"
    result.terminal.status = "cancelled"
    result.observation = null
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    const actual = await createDesktopClient().getJobPerformanceResult(JOB_ID)
    expect(actual).toEqual(result)
    expect(actual.execution_started).toBe(true)
    expect(actual.observation).toBeNull()
    expect(binding.GetReports).not.toHaveBeenCalled()
    expect(binding.GetReportDetail).not.toHaveBeenCalled()
  })

  it("keeps the observation sequence independent from the execution revision", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    result.observation!.revision = 75
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).resolves.toEqual(result)
  })

  it("preserves an explicitly absent checkpoint execution origin", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    result.observation!.report.execution_origin = null
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    const actual = await createDesktopClient().getJobPerformanceResult(JOB_ID)
    expect(actual.observation!.report.execution_origin).toBeNull()
    expect(actual.executor).toEqual(result.executor)
  })

  it.each([
    { kind: "remote" }, { node_id: RUN_ID }, { installation_id: RUN_ID },
    { engine_build_id: "b".repeat(64) }, { runner_version: "different-runner" },
    { os: "linux" }, { arch: "arm64" },
  ])("rejects checkpoint origin that differs from the receipt executor %j", async (mismatch) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    result.observation!.report.execution_origin = { ...result.observation!.report.execution_origin!, ...mismatch } as never
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it.each([
    { boundary: "completed" }, { boundary: "entered" }, { stage: "capacity" },
    { rung_index: 0 }, { revision: 0 }, { recorded_at: "2026-10-15T23:00:00Z" },
    { report: { ...parseQuickPerformanceReport(performanceReportFixture()), report_id: RUN_ID } },
  ])("rejects a mismatched sealed observation without mutating the rejected data %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    const payload = { ...result, observation: { ...result.observation, ...invalid } }
    const before = structuredClone(payload)
    binding.GetJobPerformanceResult.mockResolvedValueOnce(payload as never)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
    expect(payload).toEqual(before)
  })

  it("accepts bounded redacted response evidence in the actual sealed observation", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const report = parseQuickPerformanceReport(performanceReportFixture())
    report.success = false
    Object.assign(report.progress, { succeeded: 3, failed: 1 })
    Object.assign(report.metrics, { succeeded: 3, failed: 1, success_rate_percent: 75,
      ttft_samples: 3, ttfb_samples: 3, ttft_any_samples: 3, ttft_visible_samples: 3, semantic_chunk_count_samples: 3 })
    report.failures = [{ error_code: "authentication_failed", count: 1 }]
    Object.assign(report.samples[0], { success: false, http_status: 401, error_code: "authentication_failed",
      response_evidence: { capture_status: "captured", content_type: "application/json", request_id: "req-synthetic",
        body: '{"error":{"api_key":"[REDACTED]"}}', body_bytes: 36, truncated: false, redacted: true } })
    const result = performanceResultFixture(report)
    binding.GetJobPerformanceResult.mockResolvedValueOnce(result)
    const actual = await createDesktopClient().getJobPerformanceResult(JOB_ID)
    expect(actual.observation!.report.samples[0].response_evidence).toEqual(report.samples[0].response_evidence)
    const unsafe = structuredClone(result)
    Object.assign(unsafe.observation!.report.samples[0].response_evidence!, { redacted: false })
    binding.GetJobPerformanceResult.mockResolvedValueOnce(unsafe)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it.each([
    { job_id: RUN_ID }, { schema_version: 0 }, { raw_response: "synthetic-secret" },
    { terminal: jobFixture().execution }, { observation_state: "unknown" }, { observation: null },
    { execution_started: false }, { location: undefined },
  ])("rejects malformed sealed performance envelopes %j", async (invalid) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetJobPerformanceResult.mockResolvedValueOnce({ ...performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture())), ...invalid } as never)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("rejects unknown secret fields inside a sealed performance observation", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const result = performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))
    binding.GetJobPerformanceResult.mockResolvedValueOnce({ ...result, observation: { ...result.observation,
      report: { ...result.observation!.report, api_key: "synthetic-secret" } } } as never)
    await expect(createDesktopClient().getJobPerformanceResult(JOB_ID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it.each(["job_invalid", "job_not_found", "job_conflict", "job_unavailable", "job_confirmation_pending", "job_commit_unconfirmed", "job_query_unavailable", "job_commands_unavailable", "remote_node_upgrade_required"] as const)("keeps stable %s failures without automatic retry or fallback", async (code) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.LaunchJob.mockRejectedValueOnce(new Error(code))
    await expect(createDesktopClient().launchJob({ ...quickTaskCommand(), location: { kind: "remote", configuration_id: RUN_ID } })).rejects.toMatchObject({ code })
    expect(binding.LaunchJob).toHaveBeenCalledTimes(1)
    expect(binding.GetJob).not.toHaveBeenCalled()
  })

	it("reads complete report details and forwards all export formats", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const client = createDesktopClient()
		const reportID = FIXTURE_REPORTS.reports[0].id
		const detail = await client.getReportDetail(reportID)
		expect(detail.source).toBe("run")
		if (detail.source !== "run") throw new Error("expected run report")
		expect(detail.report.id).toBe(reportID)
		expect(binding.GetReportDetail).toHaveBeenCalledWith(reportID)
		for (const format of ["json", "html", "png", "pdf"] as const) {
			const exported = await client.exportReport(reportID, format, "team-alpha", "en-US")
			expect(exported.filename).toContain(reportID)
			expect(binding.ExportReport).toHaveBeenLastCalledWith(reportID, format, "team-alpha", "en-US")
		}
		await expect(client.saveReportExport("report.png", "image/png", "iVBORw0KGgo=", "en-US")).resolves.toBe(true)
		expect(binding.SaveReportExport).toHaveBeenCalledWith("report.png", "image/png", "iVBORw0KGgo=", "en-US")
		await client.copyReportPNG("iVBORw0KGgo=")
		expect(binding.CopyReportPNG).toHaveBeenCalledWith("iVBORw0KGgo=")
	})

	it("parses an archived quick-performance detail through the closed report boundary", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const reportID = "77777777-7777-4777-8777-777777777771"
		const detailPayload: Record<string, unknown> = {
			schema_version: 1,
			source: "quick_performance",
			performance: {
				...performanceReportFixture(),
				report_id: reportID,
				generated_at: "2026-08-31T14:30:00Z",
				archived: true,
				archive_status: "archived",
				api_key: "sk-secret",
				samples: performanceReportFixture().samples.map((sample) => ({ ...sample, raw_response: "provider secret" })),
			},
		}
		binding.GetReportDetail.mockResolvedValueOnce(detailPayload as never)

		const detail = await createDesktopClient().getReportDetail(reportID)
		expect(detail.source).toBe("quick_performance")
		expect(JSON.stringify(detail)).not.toContain("sk-secret")
		expect(JSON.stringify(detail)).not.toContain("provider secret")
	})

  it("forwards all catalog create update and delete commands", async () => {
	const binding = installBinding(FIXTURE_WORKSPACE)
	const client = createDesktopClient()
	const model = FIXTURE_CATALOG.models[0]
	const channel = FIXTURE_CATALOG.channels[0]
	const mapping = FIXTURE_CATALOG.channel_models[0]
	const testCase = FIXTURE_CATALOG.test_cases[0]
	const suite = FIXTURE_CATALOG.suites[0]
	const plan = FIXTURE_CATALOG.plans[0]
	const planInput = {
		name: plan.name,
		protocol: plan.protocol, seed: plan.seed,
		entries: plan.entries.map((entry) => ({
			entry_id: entry.entry_id,
			target_id: entry.target_id, target_kind: entry.target_kind,
			parameters: entry.parameters,
			load_mode: entry.load_mode,
			concurrency: entry.concurrency,
			request_count: entry.request_count,
			rate_per_second: entry.rate_per_second,
			duration_ms: entry.duration_ms,
			request_timeout_ms: entry.request_timeout_ms,
			sla_thresholds: entry.sla_thresholds,
		})),
	}
	const deletion = (id: string, expected_revision: number) => ({ id, expected_revision })

	const commands = [
		["CreateModel", "createModel", { name: "new model", protocols: ["openai-chat"], capabilities: ["chat"] }],
		["UpdateModel", "updateModel", { id: model.id, expected_revision: model.revision, name: model.name, protocols: model.protocols, capabilities: model.capabilities }],
		["DeleteModel", "deleteModel", deletion(model.id, model.revision)],
		["CreateChannel", "createChannel", { name: "new channel", base_url: "https://example.test/v1", api_key: "test-key-1234",  enabled: true }],
		["UpdateChannel", "updateChannel", { id: channel.id, expected_revision: channel.revision, name: channel.name, base_url: channel.base_url, api_key: "test-key-5678",  enabled: channel.enabled }],
		["DeleteChannel", "deleteChannel", deletion(channel.id, channel.revision)],
		["CreateChannelModel", "createChannelModel", { channel_id: mapping.channel_id, model_id: mapping.model_id, upstream_model_name: "new-upstream", protocols: ["openai-chat"], }],
		["UpdateChannelModel", "updateChannelModel", { id: mapping.id, expected_revision: mapping.revision, upstream_model_name: mapping.upstream_model_name, protocols: ["openai-chat"], }],
		["DeleteChannelModel", "deleteChannelModel", deletion(mapping.id, mapping.revision)],
		["CreateTestCase", "createTestCase", withoutIdentity(testCase)],
		["UpdateTestCase", "updateTestCase", { ...withoutIdentity(testCase), id: testCase.id, expected_revision: testCase.revision }],
		["DeleteTestCase", "deleteTestCase", deletion(testCase.id, testCase.revision)],
		["CreateSuite", "createSuite", { key: "new-suite", name: "new suite", protocol: suite.protocol, description: suite.description, inputs: suite.inputs, cases: suite.cases }],
		["UpdateSuite", "updateSuite", { id: suite.id, expected_revision: suite.revision, key: suite.key, name: suite.name, protocol: suite.protocol, description: suite.description, inputs: suite.inputs, cases: suite.cases }],
		["DeleteSuite", "deleteSuite", deletion(suite.id, suite.revision)],
		["CreatePlan", "createPlan", planInput],
		["UpdatePlan", "updatePlan", { ...planInput, id: plan.id, expected_revision: plan.revision }],
		["DeletePlan", "deletePlan", deletion(plan.id, plan.revision)],
	] as const

	for (const [bindingMethod, clientMethod, command] of commands) {
		await expect(client[clientMethod](command as never)).resolves.toEqual(FIXTURE_CATALOG)
		expect(binding[bindingMethod]).toHaveBeenLastCalledWith(command)
	}
  })

	it("treats an invalid catalog mutation response as committed and reports the parse cause", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		binding.CreateChannel.mockResolvedValueOnce({
			...structuredClone(FIXTURE_CATALOG),
			schema_version: 3,
		} as never)
		const client = createDesktopClient()

		const result = client.createChannel({
			name: "new channel",
			base_url: "https://example.test/v1",
			api_key: "test-key-1234",

			enabled: true,
		})

		await expect(result).rejects.toMatchObject({
			code: "catalog_saved_refresh_failed",
		})
		expect(binding.CreateChannel).toHaveBeenCalledOnce()
		expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith({
			operation: "load_catalog",
			error_code: "frontend_data_invalid",
			detail: "桌面目录数据协议版本不受支持",
		})
	})

	it("keeps a rejected catalog mutation in the unsaved failure path", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		binding.CreateChannel.mockRejectedValueOnce(new Error("catalog_invalid"))
		const client = createDesktopClient()

		const result = client.createChannel({
			name: "new channel",
			base_url: "https://example.test/v1",
			api_key: "test-key-1234",

			enabled: true,
		})

		await expect(result).rejects.toMatchObject({ code: "catalog_invalid" })
		expect(binding.CreateChannel).toHaveBeenCalledOnce()
		expect(binding.ReportFrontendDiagnostic).not.toHaveBeenCalled()
	})

  it.each([
    [{ ...FIXTURE_WORKSPACE, schema_version: 99 }, "协议版本"],
    [{ ...FIXTURE_WORKSPACE, plans: [{ id: "broken" }] }, "测试计划"],
    [
      {
        ...FIXTURE_WORKSPACE,
        runs: [{ ...FIXTURE_WORKSPACE.runs[0], status: "surprise" }],
      },
      "运行记录",
    ],
    [
      {
        ...FIXTURE_WORKSPACE,
        runs: [{ ...FIXTURE_WORKSPACE.runs[0], completed: Number.NaN }],
      },
      "运行记录",
    ],
  ])("rejects malformed binding payloads %#o", async (payload, message) => {
    installBinding(payload)

    await expect(createDesktopClient().getWorkspace()).rejects.toThrow(message)
  })

  it("rebuilds an allow-listed snapshot instead of retaining unexpected fields", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE) as unknown as Record<
      string,
      unknown
    >
    payload.credential = "sk-should-never-enter-react"
    const plans = payload.plans as Array<Record<string, unknown>>
    plans[0].network_egress = "private-egress-secret"
    const runs = payload.runs as Array<Record<string, unknown>>
    runs[0].base_url = "https://secret-provider.example/v1"
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()
    const encoded = JSON.stringify(snapshot)

    expect(encoded).not.toContain("sk-should-never-enter-react")
    expect(encoded).not.toContain("private-egress-secret")
    expect(encoded).not.toContain("secret-provider.example")
    expect(snapshot).toEqual(FIXTURE_WORKSPACE)
  })

  it("allow-lists diagnostics status and opens the owned directory without exposing its path", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()

    const status = await client.getDiagnostics()
    await client.openDiagnosticsDirectory()

    expect(status).toEqual({
      schema_version: 1,
      available: true,
      format: "jsonl",
      max_file_bytes: 10 * 1024 * 1024,
      backup_files: 5,
      run_correlation: true,
      request_correlation: true,
    })
    expect(JSON.stringify(status)).not.toContain("secret-log-path")
    expect(binding.OpenDiagnosticsDirectory).toHaveBeenCalledOnce()
  })

  it("preserves stable failure phase and error code for failed runs", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], {
      status: "failed",
      failure_phase: "execute",
      error_code: "run_execution_failed",
    })
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()

    expect(snapshot.runs[0]).toMatchObject({
      status: "failed",
      failure_phase: "execute",
      error_code: "run_execution_failed",
    })
  })

  it.each([
    ["partial metadata", { status: "failed", failure_phase: "execute" }],
    ["metadata on a non-failed run", { status: "completed", failure_phase: "execute", error_code: "run_execution_failed" }],
    ["invalid stable code", { status: "failed", failure_phase: "execute!", error_code: "run_execution_failed" }],
  ])("rejects %s", async (_name, failure) => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], failure)
    installBinding(payload)

    await expect(createDesktopClient().getWorkspace()).rejects.toThrow("运行记录")
  })

  it("accepts historical failed runs without failure metadata", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.active_run_id = undefined
    Object.assign(payload.runs[0], { status: "failed" })
    installBinding(payload)

    const snapshot = await createDesktopClient().getWorkspace()

    expect(snapshot.runs[0].status).toBe("failed")
    expect(snapshot.runs[0]).not.toHaveProperty("failure_phase")
    expect(snapshot.runs[0]).not.toHaveProperty("error_code")
  })

  it("counts observed and indeterminate outcomes in completed executions", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    Object.assign(payload.runs[0], { completed: 6, passed: 2, failed: 1, observed: 2, indeterminate: 1 })
    installBinding(payload)
    expect((await createDesktopClient().getWorkspace()).runs[0]).toMatchObject({ completed: 6, observed: 2, indeterminate: 1 })
  })

  it("uses the resolved Case count as a quick task request budget", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    Object.assign(payload.runs[0], { source: "quick_task", load_mode: "single", planned: payload.runs[0].case_count, duration_ms: 0, completed: 0, passed: 0, failed: 0, observed: 0, indeterminate: 0 })
    installBinding(payload)
    expect((await createDesktopClient().getWorkspace()).runs[0]).toMatchObject({ source: "quick_task", planned: payload.runs[0].case_count })
  })

  it.each([
    { source: "quick_task", planned: 0, duration_ms: 0 },
    { source: "quick_task", planned: 0, duration_ms: 1000 },
    { source: "unknown", planned: 0, duration_ms: 0 },
    { planned: 0, duration_ms: 0, case_count: 0 },
    { case_count: 3 },
    { observed_case_count: 5 },
    { entry_progress: [] },
  ])("rejects inconsistent task progress metadata %j", async (progress) => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    Object.assign(payload.runs[0], progress)
    installBinding(payload)
    await expect(createDesktopClient().getWorkspace()).rejects.toThrow("运行记录")
  })

  it("drops unexpected secret-bearing fields from catalog and report payloads", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    catalog.api_key = "opaque-catalog-secret"
    ;(catalog.channels as Array<Record<string, unknown>>)[0].credential_id =
      "99999999-9999-4999-8999-999999999999"
    ;(catalog.models as Array<Record<string, unknown>>)[0].provider_token =
      "opaque-model-secret"

    const reports = structuredClone(FIXTURE_REPORTS) as unknown as Record<string, unknown>
    reports.network_egress = "private-egress-secret"
    ;(reports.reports as Array<Record<string, unknown>>)[0].evidence = {
      authorization: "opaque-report-secret",
    }
    installBinding(FIXTURE_WORKSPACE, catalog, reports)

    const client = createDesktopClient()
    const encoded = JSON.stringify([
      await client.getCatalog(),
      await client.getReports(),
    ])

    expect(encoded).not.toContain("opaque-catalog-secret")
    expect(encoded).not.toContain("opaque-model-secret")
    expect(encoded).not.toContain("private-egress-secret")
    expect(encoded).not.toContain("opaque-report-secret")
    expect(encoded).not.toContain("credential_id")
  })

  it("preserves only the public imported-case policy fields", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    const testCase = (catalog.test_cases as Array<Record<string, unknown>>)[0]
    Object.assign(testCase, {
      key: "T001",
      dimension: "must",
      enabled: false,
      default: false,
      severity: "critical",
      execution_mode: "manual",
      source_path: "cases/private/should-not-cross-boundary.json",
      source_bytes_sha256: "secret-provenance-value",
    })
    installBinding(FIXTURE_WORKSPACE, catalog)

    const snapshot = await createDesktopClient().getCatalog()
    expect(snapshot.test_cases[0]).toMatchObject({
      key: "T001",
      dimension: "must",
      enabled: false,
      default: false,
      severity: "critical",
      execution_mode: "manual",
    })
    expect(JSON.stringify(snapshot)).not.toContain("source_path")
    expect(JSON.stringify(snapshot)).not.toContain("secret-provenance-value")
  })

  it("accepts every TestCase shape allowed by the Go catalog contract", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG) as unknown as Record<string, unknown>
    const testCase = (catalog.test_cases as Array<Record<string, unknown>>)[0]
    const longKey = `case.${"a".repeat(160)}`
    const longDimension = "compatibility ".repeat(14).trim()
    Object.assign(testCase, {
      key: longKey,
      dimension: longDimension,
      definitions: { "openai-chat": { inputs: {}, request: { body: {} }, assertions: [] }, "openai-responses": { inputs: {}, request: { body: { input: "hello" } }, assertions: [] } },
    })
    installBinding(FIXTURE_WORKSPACE, catalog)

    const snapshot = await createDesktopClient().getCatalog()
    expect(snapshot.test_cases[0]).toMatchObject({
      key: longKey,
      dimension: longDimension,
      definitions: { "openai-chat": { inputs: {} }, "openai-responses": { request: { body: { input: "hello" } } } },
    })
  })

  it.each([
    ["catalog_unavailable", "无法读取模型、渠道与用例目录", "GetCatalog", "getCatalog"],
    ["reports_unavailable", "无法读取测试报告", "GetReports", "getReports"],
    ["plan_protocol_mismatch", "计划中的用例、模型和渠道协议不一致", "CreatePlan", "createPlan"],
    ["run_invalid", "测试输入无效", "LaunchJob", "launchJob"],
    ["run_not_runnable", "所选任务或目标无法执行", "LaunchJob", "launchJob"],
    ["run_not_active", "当前运行已结束，无法取消或停止发送", "ControlJob", "controlJob"],
    ["catalog_invalid", "目录内容无效", "CreateModel", "createModel"],
    ["catalog_revision_conflict", "对象版本已变化或仍被引用", "UpdateModel", "updateModel"],
    ["catalog_not_found", "对象已删除或不存在", "DeleteModel", "deleteModel"],
    ["catalog_saved_refresh_failed", "已保存，但目录刷新失败", "CreateModel", "createModel"],
    ["comparison_invalid", "渠道对比请求无效", "LaunchJob", "launchJob"],
    ["comparison_not_ready", "所选渠道无法用同一个模型版本公平对比", "LaunchJob", "launchJob"],
    ["comparison_target_unmapped", "所选模型尚未绑定到其中一个渠道", "LaunchJob", "launchJob"],
    ["comparison_channel_not_ready", "其中一个渠道未启用或未配置密钥", "LaunchJob", "launchJob"],
  ] as const)("maps the public %s binding error", async (code, message, bindingMethod, clientMethod) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding[bindingMethod].mockRejectedValueOnce(new Error(code))

    await expect((createDesktopClient()[clientMethod] as (command?: never) => Promise<unknown>)()).rejects.toThrow(message)
  })

  it.each([
    ["GetWorkspace", "getWorkspace", "无法读取运行工作区", "load_workspace"],
    ["GetCatalog", "getCatalog", "无法读取模型、渠道与用例目录", "load_catalog"],
    ["GetReports", "getReports", "无法读取测试报告", "load_reports"],
    ["GetComparisons", "getComparisons", "无法读取渠道对比", "load_comparisons"],
  ] as const)(
    "identifies %s when Wails only returns operation_failed",
    async (bindingMethod, clientMethod, message, operation) => {
      const binding = installBinding(FIXTURE_WORKSPACE)
      binding[bindingMethod].mockRejectedValueOnce(new Error("operation_failed"))

      await expect(
        (createDesktopClient()[clientMethod] as () => Promise<unknown>)(),
      ).rejects.toThrow(message)
      expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith(
        expect.objectContaining({ operation }),
      )
    },
  )

  it("rejects corrupt catalog references and contradictory report conclusions", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.channel_models[0].model_id =
      "99999999-9999-4999-8999-999999999999"
    const reports = structuredClone(FIXTURE_REPORTS)
    reports.reports[0].failed_case_count = 1
    installBinding(FIXTURE_WORKSPACE, catalog, reports)

    const client = createDesktopClient()
    await expect(client.getCatalog()).rejects.toThrow("模型映射引用")
    await expect(client.getReports()).rejects.toThrow("报告摘要")
  })

	it("reports a clear diagnostic when catalog data cannot be consumed", async () => {
		const catalog = structuredClone(FIXTURE_CATALOG) as unknown as {
			test_cases: Array<Record<string, unknown>>
		}
		catalog.test_cases[0].protocol = "unknown"
		const binding = installBinding(FIXTURE_WORKSPACE, catalog)

		await expect(createDesktopClient().getCatalog()).rejects.toThrow("桌面目录测试用例数据无效")
		expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith({
			operation: "load_catalog",
			error_code: "frontend_data_invalid",
			detail: "桌面目录测试用例数据无效",
		})
	})

	it("falls back to the Wails runtime when the diagnostic binding rejects", async () => {
		const catalog = structuredClone(FIXTURE_CATALOG) as unknown as {
			test_cases: Array<Record<string, unknown>>
		}
		catalog.test_cases[0].protocol = "unknown"
		const binding = installBinding(FIXTURE_WORKSPACE, catalog)
		binding.ReportFrontendDiagnostic.mockRejectedValueOnce(new Error("diagnostics unavailable"))
		const logError = vi.fn()
		Object.defineProperty(window, "runtime", {
			configurable: true,
			value: { LogError: logError },
		})

		await expect(createDesktopClient().getCatalog()).rejects.toThrow("桌面目录测试用例数据无效")
		expect(logError).toHaveBeenCalledOnce()
		expect(logError).toHaveBeenCalledWith(expect.stringContaining("load_catalog"))
		expect(logError).toHaveBeenCalledWith(expect.stringContaining("frontend_data_invalid"))
		expect(logError).toHaveBeenCalledWith(expect.stringContaining("桌面目录测试用例数据无效"))
	})

	it("reports an incomplete Wails binding before rejecting initial workspace access", async () => {
		vi.useFakeTimers()
		const binding = installBinding(FIXTURE_WORKSPACE)
		Reflect.deleteProperty(binding, "GetCatalog")
		const pendingWorkspace = createDesktopClient().getWorkspace()
		const rejection = expect(pendingWorkspace).rejects.toThrow("无法读取运行工作区")

		await vi.advanceTimersByTimeAsync(2_000)
		await rejection
		expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith({
			operation: "load_workspace",
			error_code: "frontend_operation_failed",
			detail: "Wails desktop binding is incomplete; missing methods: GetCatalog",
		})
	})

	it("waits for a late Wails binding instead of permanently failing initial workspace access", async () => {
		vi.stubEnv("DEV", false)
		const client = createDesktopClient()
		const pendingWorkspace = client.getWorkspace()

		queueMicrotask(() => installBinding(FIXTURE_WORKSPACE))

		await expect(pendingWorkspace).resolves.toEqual(FIXTURE_WORKSPACE)
	})

	it("reports a binding startup timeout after the Wails runtime becomes available", async () => {
		vi.useFakeTimers()
		vi.stubEnv("DEV", false)
		const logError = vi.fn()
		const pendingWorkspace = createDesktopClient().getWorkspace()
		const rejection = expect(pendingWorkspace).rejects.toThrow("无法读取运行工作区")

		queueMicrotask(() => {
			Object.defineProperty(window, "runtime", {
				configurable: true,
				value: { LogError: logError },
			})
		})
		await vi.advanceTimersByTimeAsync(2_000)
		await rejection

		expect(logError).toHaveBeenCalledWith(expect.stringContaining("load_workspace"))
		expect(logError).toHaveBeenCalledWith(expect.stringContaining("frontend_operation_failed"))
	})
})

function installBinding(
  payload: unknown,
  catalog: unknown = FIXTURE_CATALOG,
  reports: unknown = FIXTURE_REPORTS,
) {
  const binding = {
    CreateLocalFileSelection: vi.fn<(document: string) => Promise<unknown>>(),
    ReleaseLocalFileSelection: vi.fn(async (_id?: string) => undefined),
    GetRemoteNodes: vi.fn(async () => structuredClone(EMPTY_REMOTE_NODES)),
    StartRemoteNodeUpgrade: vi.fn(), GetRemoteNodeUpgrade: vi.fn<(id: string) => Promise<unknown>>(async () => null),
    RetryRemoteNodeUpgrade: vi.fn(), RecoverRemoteNodeUpgrade: vi.fn(),
    SaveRemoteNode: vi.fn(async (_command?: unknown) => undefined),
    RefreshRemoteNode: vi.fn(async (_command?: unknown) => undefined),
    ConfirmRemoteNodeHostKey: vi.fn(async (_command?: unknown) => undefined),
    SelectDefaultExecution: vi.fn(async (_command?: unknown) => undefined),
    RetryRemoteInstallation: vi.fn(async (_command?: unknown) => undefined),
    RemoveRemoteNode: vi.fn(async (_command?: unknown) => undefined),
    GetWorkspace: vi.fn(async () => structuredClone(payload)),
    GetCatalog: vi.fn(async () => structuredClone(catalog)),
    GetReports: vi.fn(async () => structuredClone(reports)),
    GetReportGeneration: vi.fn(async () => ({ schema_version: 1, runs: [] })),
		GetDiagnostics: vi.fn(async () => ({
			schema_version: 1, available: true, format: "jsonl",
			max_file_bytes: 10 * 1024 * 1024, backup_files: 5,
			run_correlation: true, request_correlation: true,
			path: "C:\\secret-log-path",
		})),
		OpenDiagnosticsDirectory: vi.fn(async () => undefined),
		ReportFrontendDiagnostic: vi.fn(async () => undefined),
		GetReportDetail: vi.fn(async (reportID: string) => structuredClone(reportDetailFixture(reportID))),
			ExportReport: vi.fn(async (reportID: string, format: string, _watermark: string) => ({
			filename: `llm-test-studio-report-${reportID}.${format}`,
			media_type: format === "json" ? "application/json" : "application/octet-stream",
				data_base64: "e30=",
			})),
			SaveReportExport: vi.fn(async () => true),
			CopyReportPNG: vi.fn(async () => undefined),
			GetComparisons: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
    LaunchJob: vi.fn(async (value?: unknown) => {
      const command = value as LaunchCommand
      switch (command.kind) {
        case "plan":
        case "quick_task":
          return jobFixture({ kind: "run", location: command.location, run_ids: [RUN_ID], report_ids: [], execution: null })
        case "performance":
          return jobFixture({ location: command.location, execution: null })
        case "comparison":
          return jobFixture({ kind: "comparison", location: command.location, comparison_id: COMMAND_ID, run_ids: [RUN_ID], report_ids: [], execution: null })
      }
    }),
    GetJobs: vi.fn(async (_after?: string, _limit?: number) => ({ schema_version: 1, jobs: [], next_cursor: "", has_more: false })),
    GetJob: vi.fn(async (_jobID?: string) => jobFixture()),
    GetJobPerformanceResult: vi.fn(async (_jobID?: string) => performanceResultFixture(parseQuickPerformanceReport(performanceReportFixture()))),
    ControlJob: vi.fn(async (_command?: unknown) => controlFixture()),
    GetJobControls: vi.fn(async (_jobID?: string, _after?: string, _limit?: number) => ({ schema_version: 1, job_id: JOB_ID, commands: [], next_cursor: "", has_more: false })),
    RetryJobControl: vi.fn(async (_commandID?: string) => controlFixture()),
    GetQuickTask: vi.fn(),
    RememberQuickTaskCredential: vi.fn(async () => undefined), ForgetQuickTaskCredential: vi.fn(async () => undefined),

	CreateModel: vi.fn(async () => structuredClone(catalog)),
	UpdateModel: vi.fn(async () => structuredClone(catalog)),
	DeleteModel: vi.fn(async () => structuredClone(catalog)),
	SaveQuickTestTarget: vi.fn(async () => structuredClone(catalog)),
	CreateChannel: vi.fn(async () => structuredClone(catalog)),
	UpdateChannel: vi.fn(async () => structuredClone(catalog)),
	DeleteChannel: vi.fn(async () => structuredClone(catalog)),
	CreateChannelModel: vi.fn(async () => structuredClone(catalog)),
	UpdateChannelModel: vi.fn(async () => structuredClone(catalog)),
	DeleteChannelModel: vi.fn(async () => structuredClone(catalog)),
	CreateTestCase: vi.fn(async () => structuredClone(catalog)),
	UpdateTestCase: vi.fn(async () => structuredClone(catalog)),
	DeleteTestCase: vi.fn(async () => structuredClone(catalog)),
	CreateSuite: vi.fn(async () => structuredClone(catalog)),
	UpdateSuite: vi.fn(async () => structuredClone(catalog)),
	DeleteSuite: vi.fn(async () => structuredClone(catalog)),
	CreatePlan: vi.fn(async () => structuredClone(catalog)),
	UpdatePlan: vi.fn(async () => structuredClone(catalog)),
	DeletePlan: vi.fn(async () => structuredClone(catalog)),
  }
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { main: { DesktopApp: binding } },
  })
  return binding
}

function performanceReportFixture() {
  const fine = {
    ttfb_ms: 10, ttft_any_ms: 30, ttft_visible_ms: 30, ttst_ms: 0, observed_icl_ms: 0, semantic_chunk_count: 1,
  }
  const fineMetrics = {
    ttft_samples: 4, ttft_p50_ms: 30, ttft_p90_ms: 30, ttft_p95_ms: 30, ttft_p99_ms: 30, ttft_average_ms: 30,
    ttfb_samples: 4, ttfb_p50_ms: 10, ttfb_p95_ms: 10, ttfb_p99_ms: 10, ttfb_average_ms: 10,
    ttft_any_samples: 4, ttft_any_p50_ms: 30, ttft_any_p95_ms: 30, ttft_any_p99_ms: 30, ttft_any_average_ms: 30,
    ttft_visible_samples: 4, ttft_visible_p50_ms: 30, ttft_visible_p95_ms: 30, ttft_visible_p99_ms: 30, ttft_visible_average_ms: 30,
    ttst_samples: 0, ttst_p50_ms: 0, ttst_p95_ms: 0, ttst_p99_ms: 0, ttst_average_ms: 0,
    observed_icl_samples: 0, observed_icl_p50_ms: 0, observed_icl_p95_ms: 0, observed_icl_p99_ms: 0, observed_icl_average_ms: 0,
    semantic_chunk_count_samples: 4, semantic_chunk_count_p50: 1, semantic_chunk_count_p95: 1,
    semantic_chunk_count_p99: 1, semantic_chunk_count_average: 1,
  }
  return {
    schema_version: 2,
    execution_origin: null,
    protocol: "openai-chat" as const, archived: false, archive_status: "not_attempted", model_id: "gpt-test", success: true, address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: { load_mode: "fixed_concurrency", request_count: 4, duration_ms: 0, concurrency: 2, timeout_ms: 30_000, input_tokens: 20, output_tokens: 32 },
    progress: {
      phase: "completed", planned: 4, offered: 4, launched: 4, completed: 4,
      peak_in_flight: 2, succeeded: 4, failed: 0, rejected: 0,
      send_duration_ms: 300, drain_duration_ms: 20, total_duration_ms: 320,
    },
    metrics: {
      completed: 4, succeeded: 4, failed: 0, timed_out: 0,
      success_rate_percent: 100, request_qps: 12.5, rpm: 750,
      input_tpm: 15_000, output_tpm: 24_000, total_tpm: 39_000, generation_tps: 400,
      ...fineMetrics,
      tpot_p50_ms: 4, tpot_p90_ms: 5, tpot_p95_ms: 6, tpot_p99_ms: 7, tpot_average_ms: 4.5,
      e2e_p50_ms: 60, e2e_p90_ms: 75, e2e_p95_ms: 80, e2e_p99_ms: 84, e2e_average_ms: 65,
			schedule_lag_p50_ms: 1.5, schedule_lag_p90_ms: 2.7, schedule_lag_p95_ms: 2.85, schedule_lag_p99_ms: 2.97, schedule_lag_average_ms: 1.5,
      prompt_tokens: 80, completion_tokens: 128, cached_tokens: 20, cache_rate_percent: 25,
    },
    samples: [
      performanceSample(0, 60, 60, 30, 4, fine), performanceSample(1, 75, 74, 30, 5, fine),
      performanceSample(2, 84, 82, 30, 7, fine), performanceSample(3, 90, 88, 30, 6, fine),
    ],
    failures: [],
  }
}

function performanceSample(requestIndex: number, finished: number, e2e: number, ttft: number, tpot: number, fine?: Record<string, number>) {
  return {
    request_index: requestIndex, scheduled_offset_ms: 0, started_offset_ms: requestIndex, finished_offset_ms: finished,
    schedule_lag_ms: requestIndex, e2e_ms: e2e, ttft_ms: ttft, tpot_ms: tpot, http_status: 200, success: true, timed_out: false,
    prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5,
    ...(fine ?? {}),
  }
}

function reportDetailFixture(reportID: string) {
  const summary = FIXTURE_REPORTS.reports.find(report => report.id === reportID) ?? FIXTURE_REPORTS.reports[0]
  const detail = protocolReportFixture()
  detail.report.id = summary.id; detail.report.run_id = summary.run_id!; detail.report.run_status = summary.run_status
  return detail
}

function withoutIdentity<T extends { id: string; revision: number }>(value: T): Omit<T, "id" | "revision"> {
  const { id: _id, revision: _revision, ...rest } = value
  return rest
}

function quickTaskCommand(): LaunchCommand {
  return { location: { kind: "local", configuration_id: "" }, kind: "quick_task", plan: null,
    quick_task: { suite_id: FIXTURE_CATALOG.suites[1].id, seed: 1,
      request_timeout_ms: 60000, model: "synthetic-model", base_url: "https://example.test", inputs: {} },
    performance: null, comparison: null }
}
