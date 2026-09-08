import { afterEach, describe, expect, it, vi } from "vitest"

import {
  FIXTURE_CATALOG,
  FIXTURE_REPORTS,
  FIXTURE_WORKSPACE,
} from "@/features/runs/fixtures"
import { EMPTY_COMPARISONS } from "@/features/comparisons/data"
import { parseQuickPerformanceReport } from "@/features/quick-test/data"

import { createDesktopClient, createFixtureClient } from "./desktop-client"
import { DesktopDataError } from "./data-error"

describe("Wails desktop client", () => {
  afterEach(() => {
    Reflect.deleteProperty(window, "go")
		Reflect.deleteProperty(window, "runtime")
		vi.unstubAllEnvs()
		vi.useRealTimers()
  })

  it("classifies malformed English payloads by type instead of translated text", async () => {
    document.documentElement.lang = "en-US"
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetWorkspace.mockResolvedValueOnce({})
    await expect(createDesktopClient().getWorkspace()).rejects.toThrow("Unsupported desktop data protocol version")
    expect(binding.ReportFrontendDiagnostic).toHaveBeenCalledWith(expect.objectContaining({ operation: "load_workspace", error_code: "frontend_data_invalid" }))
  })

  it("starts a Suite task once and returns its Run identity without refreshing", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.GetWorkspace.mockRejectedValueOnce(new Error("refresh failed"))
    const command = {
      suite_id: "11111111-1111-4111-8111-111111111111", suite_revision: 2,
      model: "temporary-model", base_url: "https://example.test", api_key: "private-test-key",
      inputs: { prompt: "hello", duration: 4, audio: false },
    }
    await expect(createDesktopClient().startQuickTask(command)).resolves.toBe(FIXTURE_WORKSPACE.runs[0].id)
    expect(binding.StartQuickTask).toHaveBeenCalledExactlyOnceWith(command)
    expect(binding.GetWorkspace).not.toHaveBeenCalled()
  })

  it.each(["", "not-a-run", null, { run_id: "private-test-key" }])("rejects malformed quick task acceptance %j", async (payload) => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.StartQuickTask.mockResolvedValueOnce(payload as never)
    await expect(createDesktopClient().startQuickTask({ suite_id: "11111111-1111-4111-8111-111111111111", suite_revision: 1, model: "model", inputs: {} })).rejects.toBeInstanceOf(DesktopDataError)
    expect(binding.StartQuickTask).toHaveBeenCalledTimes(1)
  })

  it("loads only safe task history and rejects a different Run identity", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const runID = FIXTURE_WORKSPACE.runs[0].id
    const detail = { schema_version: 1, run_id: runID, suite: FIXTURE_CATALOG.suites.find((suite) => suite.quick_test)!, model: "model", base_url: "https://example.test", inputs: { prompt: "edited" } }
    binding.GetQuickTask.mockResolvedValueOnce({ ...detail, api_key: "private-key", case_definitions: [{ raw: "hidden" }] })
    await expect(createDesktopClient().getQuickTask(runID)).resolves.toEqual(detail)
    expect(binding.GetQuickTask).toHaveBeenCalledExactlyOnceWith(runID)
    binding.GetQuickTask.mockResolvedValueOnce({ ...detail, run_id: "123e4567-e89b-42d3-a456-426614174099" })
    await expect(createDesktopClient().getQuickTask(runID)).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("uses the typed Wails methods and forwards command identifiers", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()

    await expect(client.getWorkspace()).resolves.toEqual(FIXTURE_WORKSPACE)
    await expect(client.getCatalog()).resolves.toEqual(FIXTURE_CATALOG)
    await expect(client.getReports()).resolves.toEqual(FIXTURE_REPORTS)
    await client.startRun(FIXTURE_WORKSPACE.plans[0].id)
		const targetCommand = {
			plan_id: FIXTURE_WORKSPACE.plans[0].id,
			model_id: FIXTURE_CATALOG.models[0].id,
			channel_id: FIXTURE_CATALOG.channels[0].id,
		}
		await client.startRunTarget(targetCommand)
    await client.stopSending(FIXTURE_WORKSPACE.runs[0].id)
    await client.cancelRun(FIXTURE_WORKSPACE.runs[0].id)
    const performanceCommand = {
      address_mode: "base_url" as const,
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      load_mode: "fixed_concurrency" as const,
      request_count: 4,
      duration_ms: 0,
      concurrency: 2,
      rate_per_second: 0,
      max_in_flight: 0,
      arrival_pattern: "constant" as const,
      workload_mode: "fixed" as const,
      random_seed: 0,
      input_tokens_stddev: 0,
      output_tokens_stddev: 0,
      shared_prefix_tokens: 0,
      warmup_requests: 0,
      ramp_duration_ms: 0,
      ramp_request_cap: 0,
      slice_duration_ms: 0,
      slo_ttft_ms: 0,
      slo_tpot_ms: 0,
      slo_e2e_ms: 0,
      slo_target_percent: 0,
      capacity_enabled: false,
      capacity_start: 0,
      capacity_step: 0,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    }
    await expect(client.runQuickPerformanceTest(performanceCommand)).resolves.toMatchObject({
      schema_version: 1,
      success: true,
      metrics: { completed: 4, succeeded: 4 },
    })
    expect(binding.StartRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.plans[0].id)
		expect(binding.StartRunTarget).toHaveBeenCalledWith(targetCommand)
    expect(binding.StopSending).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)
    expect(binding.CancelRun).toHaveBeenCalledWith(FIXTURE_WORKSPACE.runs[0].id)

    expect(binding.RunQuickPerformanceTest).toHaveBeenCalledWith(performanceCommand, "")

  })

  it("loads quick task history without an authored plan while retaining formal Run reference checks", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    payload.plans = []
    payload.runs = [{ ...payload.runs[0], source: "quick_task", plan_id: payload.runs[0].id, planned: 0, duration_ms: 0 }]
    payload.active_run_id = payload.runs[0].id
    const binding = installBinding(payload)
    await expect(createDesktopClient().getWorkspace()).resolves.toEqual(payload)

    const formalRun = { ...payload.runs[0], source: undefined, planned: 1 }
    binding.GetWorkspace.mockResolvedValueOnce({ ...payload, runs: [formalRun] })
    await expect(createDesktopClient().getWorkspace()).rejects.toBeInstanceOf(DesktopDataError)
  })

  it("keeps fixture quick Runs separate from authored targets and supports cancellation", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const suite = catalog.suites[0]
    suite.quick_test = { description: "Connectivity", timeout_ms: 30000, inputs: [] }
    const client = createFixtureClient(FIXTURE_WORKSPACE, catalog)
    const command = { suite_id: suite.id, suite_revision: suite.revision, model: suite.model_target || "temporary-model", base_url: "https://example.test", api_key: "private-test-key", inputs: {} }
    const firstID = await client.startQuickTask(command)
    const secondID = await client.startQuickTask(command)
    expect(firstID).not.toBe(secondID)
    const snapshot = await client.getWorkspace()
    expect(snapshot.runs.find((run) => run.id === firstID)).toMatchObject({ source: "quick_task", status: "queued", plan_id: firstID, planned: 0 })
    expect(JSON.stringify(snapshot)).not.toContain("private-test-key")
    expect(await client.getCatalog()).toEqual(catalog)
    expect((await client.cancelRun(secondID)).runs.find((run) => run.id === secondID)?.status).toBe("cancelled")
  })

  it("materializes ordered plan suites in the browser fixture with generated entry ids", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    catalog.plans = []
    const suite = catalog.suites[0]
    const entry = {
      suite_id: suite.id,
      suite_revision: suite.revision,
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
      model_ids: [],
      channel_ids: [],
      suites: [{ ...entry }, { ...entry, parameters: { prompt: "second" }, concurrency: 7 }],
    })

    const plan = result.plans[0]
    expect(plan).toMatchObject({ suite_count: 2, case_count: suite.case_count * 2 })
    expect(plan.suites[0]).toMatchObject({ parameters: { prompt: "first" }, concurrency: 2 })
    expect(plan.suites[1]).toMatchObject({ parameters: { prompt: "second" }, concurrency: 7 })
    expect(plan.suites[0].entry_id).not.toBe(plan.suites[1].entry_id)
  })

  it("preserves pinned Suite metadata when a browser fixture Plan is edited", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const plan = catalog.plans[0]
    const entry = plan.suites[0]
    const currentSuite = catalog.suites.find((suite) => suite.id === entry.suite_id)!
    entry.suite_revision = currentSuite.revision - 1
    entry.suite_key = "historical-suite"
    entry.suite_name = "Historical Suite"
    delete entry.quick_test
    entry.parameters = {}
    currentSuite.quick_test = {
      description: "New metadata",
      timeout_ms: 30_000,
      inputs: [{
        key: "prompt",
        label: "Prompt",
        type: "text",
        default: "new default",
        bindings: [{ case_key: "fixture", pointer: "/request/body/prompt" }],
      }],
    }
    const client = createFixtureClient(FIXTURE_WORKSPACE, catalog)

    const updated = await client.updatePlan({
      id: plan.id,
      expected_revision: plan.revision,
      name: plan.name,
      model_ids: plan.model_ids,
      channel_ids: plan.channel_ids,
      suites: [{
        entry_id: entry.entry_id,
        suite_id: entry.suite_id,
        suite_revision: entry.suite_revision,
        parameters: {},
        load_mode: entry.load_mode,
        concurrency: entry.concurrency,
        request_count: entry.request_count,
        rate_per_second: entry.rate_per_second,
        duration_ms: entry.duration_ms,
        request_timeout_ms: entry.request_timeout_ms,
        sla_thresholds: entry.sla_thresholds,
      }],
    })

    expect(updated.plans.find((candidate) => candidate.id === plan.id)?.suites[0]).toMatchObject({
      suite_revision: entry.suite_revision,
      suite_key: "historical-suite",
      suite_name: "Historical Suite",
      parameters: {},
    })
    expect(updated.plans.find((candidate) => candidate.id === plan.id)?.suites[0].quick_test).toBeUndefined()
  })

  it("forwards explicit credential actions without restarting or refreshing the task", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const client = createDesktopClient()
    const command = { run_id: FIXTURE_WORKSPACE.runs[0].id, base_url: "https://example.test", protocol: "seedance" as const, api_key: "private-test-key" }
    await client.rememberQuickTaskCredential(command)
    await client.forgetQuickTaskCredential(command.run_id)
    expect(binding.RememberQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(command)
    expect(binding.ForgetQuickTaskCredential).toHaveBeenCalledExactlyOnceWith(command.run_id)
    expect(binding.StartQuickTask).not.toHaveBeenCalled()
    expect(binding.GetWorkspace).not.toHaveBeenCalled()
  })

  it("replays remembered fixture targets without storing secret values and forgets all references", async () => {
    const catalog = structuredClone(FIXTURE_CATALOG)
    const suite = catalog.suites.find((item) => item.quick_test)!
    const client = createFixtureClient(FIXTURE_WORKSPACE, catalog)
    const command = { suite_id: suite.id, suite_revision: suite.revision, model: suite.model_target || "model", base_url: "https://example.test", api_key: "private-test-key", inputs: {} }
    const runID = await client.startQuickTask(command)
    expect((await client.getQuickTask(runID)).credential_run_id).toBeUndefined()
    await client.rememberQuickTaskCredential({ run_id: runID, base_url: command.base_url, protocol: suite.protocol, api_key: command.api_key })
    const history = await client.getQuickTask(runID)
    expect(history.credential_run_id).toBe(runID)
    expect(JSON.stringify(history)).not.toContain(command.api_key)
    const replay = { ...command, api_key: undefined, credential_run_id: runID }
    await expect(client.startQuickTask({ ...replay, base_url: "https://different.test" })).rejects.toThrow()
    const secondID = await client.startQuickTask(replay)
    expect((await client.getQuickTask(secondID)).credential_run_id).toBe(runID)
    await client.forgetQuickTaskCredential(runID)
    expect((await client.getQuickTask(secondID)).credential_run_id).toBeUndefined()
    await expect(client.startQuickTask(replay)).rejects.toThrow()
  })

  it("keeps idle fixture windows sparse while retaining an empty final partial slice", async () => {
    const report = await createFixtureClient(FIXTURE_WORKSPACE).runQuickPerformanceTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      load_mode: "fixed_concurrency",
      request_count: 4,
      duration_ms: 2_500,
      concurrency: 2,
      rate_per_second: 0,
      max_in_flight: 0,
      arrival_pattern: "constant",
      workload_mode: "fixed",
      random_seed: 0,
      input_tokens_stddev: 0,
      output_tokens_stddev: 0,
      shared_prefix_tokens: 0,
      warmup_requests: 0,
      ramp_duration_ms: 0,
      ramp_request_cap: 0,
      slice_duration_ms: 1_000,
      slo_ttft_ms: 0,
      slo_tpot_ms: 0,
      slo_e2e_ms: 0,
      slo_target_percent: 0,
      capacity_enabled: false,
      capacity_start: 0,
      capacity_step: 0,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    })

    expect(report.schema_version).toBe(3)
    expect(report.samples[0]).toMatchObject({
      ttfb_ms: 15, ttft_any_ms: 35, ttft_ms: 35, ttft_visible_ms: 45,
      ttst_ms: 60, observed_icl_ms: 25, semantic_chunk_count: 2,
    })
    expect(report.metrics).toMatchObject({
      ttft_samples: 4, ttft_any_samples: 4, ttft_p50_ms: 35, ttft_any_p50_ms: 35,
      ttfb_samples: 4, ttft_visible_samples: 4, ttst_samples: 4,
      observed_icl_samples: 4, semantic_chunk_count_samples: 4,
    })
    expect(report.time_slices?.map((slice) => slice.slice_index)).toEqual([0, 2])
    expect(report.time_slices?.[0]).toMatchObject({
      ttfb: { count: 4, average_ms: 15 },
      ttft_any: { count: 4, average_ms: 35 },
      ttft: { count: 4, average_ms: 35 },
      semantic_chunk_count: { count: 4, average: 2 },
    })
    expect(parseQuickPerformanceReport(structuredClone(report))).toMatchObject({ schema_version: 3 })
    expect(report.time_slices?.[1]).toMatchObject({
      start_ms: 2_000,
      end_ms: 2_500,
      partial: true,
      offered: 0,
      launched: 0,
      completed: 0,
      ttft: { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0 },
    })
  })

  it("builds a coherent SLO capacity ladder in the desktop fixture", async () => {
    const report = await createFixtureClient(FIXTURE_WORKSPACE).runQuickPerformanceTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      load_mode: "fixed_concurrency",
      request_count: 2,
      duration_ms: 0,
      concurrency: 3,
      rate_per_second: 0,
      max_in_flight: 0,
      arrival_pattern: "constant",
      workload_mode: "fixed",
      random_seed: 0,
      input_tokens_stddev: 0,
      output_tokens_stddev: 0,
      shared_prefix_tokens: 0,
      warmup_requests: 0,
      ramp_duration_ms: 0,
      ramp_request_cap: 0,
      slice_duration_ms: 0,
      slo_ttft_ms: 50,
      slo_tpot_ms: 0,
      slo_e2e_ms: 0,
      slo_target_percent: 90,
      capacity_enabled: true,
      capacity_start: 1,
      capacity_step: 1,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    })

    expect(report.request_budget).toMatchObject({ measured_cap: 6, total_cap: 6 })
    expect(report.slo_assessment).toMatchObject({ status: "passed", total_requests: 2, good_requests: 2 })
    expect(report.capacity_result).toMatchObject({
      status: "passed",
      selected_rung_index: 2,
      highest_passing_rung_index: 2,
      rungs: [
        { index: 0, target: 1 },
        { index: 1, target: 2 },
        { index: 2, target: 3 },
      ],
    })
    expect(report.progress).toMatchObject({ capacity_rung_number: 3, capacity_rung_count: 3, capacity_target: 3 })
  })

  it("keeps a near-terminal fixture target before the exact open-loop maximum", async () => {
    const report = await createFixtureClient(FIXTURE_WORKSPACE).runQuickPerformanceTest({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      load_mode: "open_loop",
      request_count: 2,
      duration_ms: 0,
      concurrency: 0,
      rate_per_second: 2,
      max_in_flight: 4,
      arrival_pattern: "constant",
      workload_mode: "fixed",
      random_seed: 0,
      input_tokens_stddev: 0,
      output_tokens_stddev: 0,
      shared_prefix_tokens: 0,
      warmup_requests: 0,
      ramp_duration_ms: 0,
      ramp_request_cap: 0,
      slice_duration_ms: 0,
      slo_ttft_ms: 50,
      slo_tpot_ms: 0,
      slo_e2e_ms: 0,
      slo_target_percent: 90,
      capacity_enabled: true,
      capacity_start: 1.9999999999,
      capacity_step: 3,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    })

    expect(report.capacity_result?.rungs.map((rung) => rung.target)).toEqual([1.9999999999, 2])
    expect(report.progress).toMatchObject({ capacity_rung_number: 2, capacity_rung_count: 2, capacity_target: 2 })
  })

  it("rejects malformed quick-performance reports and drops raw provider fields", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      profile: { ...performanceReportFixture().profile, api_key: "sk-nested" },
      progress: { ...performanceReportFixture().progress, raw_response: "provider progress" },
      metrics: { ...performanceReportFixture().metrics, provider_detail: "provider metrics" },
      samples: performanceReportFixture().samples.map((sample) => ({ ...sample, provider_response: "secret sample output" })),
      raw_results: [{ response: "secret provider output" }],
      api_key: "sk-secret",
    } as never)
    const command = {
      address_mode: "base_url" as const, url: "https://api.example.test/v1",
      api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency" as const, request_count: 4,
      duration_ms: 0, concurrency: 2, rate_per_second: 0, max_in_flight: 0,
      arrival_pattern: "constant" as const, workload_mode: "fixed" as const, random_seed: 0,
      input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
      warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
      slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
      capacity_enabled: false, capacity_start: 0, capacity_step: 0, timeout_ms: 30_000,
      input_tokens: 20, output_tokens: 32,
    }
    const report = await createDesktopClient().runQuickPerformanceTest(command)
    expect(JSON.stringify(report)).not.toContain("secret provider output")
    expect(JSON.stringify(report)).not.toContain("sk-secret")
    expect(JSON.stringify(report)).not.toContain("sk-nested")
    expect(JSON.stringify(report)).not.toContain("provider progress")
    expect(JSON.stringify(report)).not.toContain("provider metrics")
    expect(JSON.stringify(report)).not.toContain("secret sample output")

    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      success: false,
      base_url: "",
      endpoint: "",
      profile: { request_count: 0, duration_ms: 0, concurrency: 0, timeout_ms: 0, input_tokens: 0, output_tokens: 0 },
      progress: {
        phase: "not_started", planned: 0, launched: 0, completed: 0,
        peak_in_flight: 0, succeeded: 0, failed: 0, rejected: 0,
        send_duration_ms: 0, drain_duration_ms: 0, total_duration_ms: 0,
      },
      metrics: zeroPerformanceMetrics(),
      samples: [],
      error_code: "invalid_request",
    } as never)
    await expect(createDesktopClient().runQuickPerformanceTest(command)).resolves.toMatchObject({
      success: false,
      error_code: "invalid_request",
    })

    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...performanceReportFixture(),
      failures: [{ error_code: "provider_said_sk-secret", count: 1 }],
    } as never)
    await expect(createDesktopClient().runQuickPerformanceTest(command)).rejects.toThrow("快速性能报告数据结构无效")
  })

  it("accepts only bounded redacted failure response evidence", async () => {
    const binding = installBinding(FIXTURE_WORKSPACE)
    const fixture = performanceReportFixture()
    binding.RunQuickPerformanceTest.mockResolvedValueOnce({
      ...fixture,
      success: false,
      progress: { ...fixture.progress, succeeded: 3, failed: 1 },
      metrics: { ...fixture.metrics, succeeded: 3, failed: 1, success_rate_percent: 75 },
      failures: [{ error_code: "authentication_failed", count: 1 }],
      samples: fixture.samples.map((sample, index) => index === 0 ? {
        ...sample,
        success: false,
        http_status: 401,
        error_code: "authentication_failed",
        response_evidence: {
          capture_status: "captured",
          content_type: "application/json",
          request_id: "req-safe-123",
          body: "{\"error\":{\"message\":\"quota exhausted\",\"api_key\":\"[REDACTED]\"}}",
          body_bytes: 96,
          truncated: false,
          redacted: true,
          provider_secret: "must be dropped",
        },
      } : sample),
    } as never)

    const report = await createDesktopClient().runQuickPerformanceTest({
      address_mode: "base_url", url: "https://api.example.test/v1",
      api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency", request_count: 4,
      duration_ms: 0, concurrency: 2, rate_per_second: 0, max_in_flight: 0,
      arrival_pattern: "constant", workload_mode: "fixed", random_seed: 0,
      input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
      warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
      slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
      capacity_enabled: false, capacity_start: 0, capacity_step: 0, timeout_ms: 30_000,
      input_tokens: 20, output_tokens: 32,
    })

    expect(report.samples[0].response_evidence).toEqual({
      capture_status: "captured",
      content_type: "application/json",
      request_id: "req-safe-123",
      body: "{\"error\":{\"message\":\"quota exhausted\",\"api_key\":\"[REDACTED]\"}}",
      body_bytes: 96,
      truncated: false,
      redacted: true,
    })
    expect(JSON.stringify(report)).not.toContain("must be dropped")
  })

	it("publishes only correlated validated quick-performance progress and unsubscribes", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		let eventCallback: ((payload: unknown) => void) | undefined
		const unsubscribe = vi.fn()
		const eventsOn = vi.fn((_name: string, callback: (payload: unknown) => void) => {
			eventCallback = callback
			return unsubscribe
		})
		Object.defineProperty(window, "runtime", {
			configurable: true,
			value: { EventsOn: eventsOn },
		})
		binding.RunQuickPerformanceTest.mockImplementationOnce(async (_command, progressID) => {
			const progress = {
				phase: "ramping", planned: 4, launched: 2, completed: 1, capped: true,
				peak_in_flight: 2, succeeded: 1, failed: 0, rejected: 0,
				send_duration_ms: 100, drain_duration_ms: 0, total_duration_ms: 100,
			}
			eventCallback?.({ progress_id: "99999999-9999-4999-8999-999999999999", progress })
			eventCallback?.({ progress_id: progressID, progress: { ...progress, api_key: "sk-secret" }, raw_response: "secret" })
			return performanceReportFixture()
		})
		const progress = vi.fn()
		const command = {
			address_mode: "base_url" as const, url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency" as const, request_count: 4,
			duration_ms: 0, concurrency: 2, rate_per_second: 0, max_in_flight: 0,
			arrival_pattern: "constant" as const, workload_mode: "fixed" as const, random_seed: 0,
			input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
			warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
			slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
			capacity_enabled: false, capacity_start: 0, capacity_step: 0, timeout_ms: 30_000,
			input_tokens: 20, output_tokens: 32,
		}

		const report = await createDesktopClient().runQuickPerformanceTest(command, progress)

		expect(eventsOn).toHaveBeenCalledWith("quick-performance-progress", expect.any(Function))
		expect(binding.RunQuickPerformanceTest).toHaveBeenCalledWith(command, expect.stringMatching(/^[0-9a-f-]{36}$/))
		expect(progress).toHaveBeenCalledTimes(1)
		expect(progress).toHaveBeenCalledWith(expect.objectContaining({ phase: "ramping", completed: 1, capped: true }))
		expect(JSON.stringify(progress.mock.calls)).not.toContain("sk-secret")
		expect(JSON.stringify(progress.mock.calls)).not.toContain("raw_response")
		expect(report.metrics.schedule_lag_p90_ms).toBe(2.7)
		expect(report.metrics.schedule_lag_p99_ms).toBe(2.97)
		expect(unsubscribe).toHaveBeenCalledTimes(1)
	})

	it("accepts archived performance reports created before queue P90 and P99 were recorded", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const fixture = performanceReportFixture()
		const {
			schedule_lag_p90_ms: _legacyP90,
			schedule_lag_p99_ms: _legacyP99,
			...legacyMetrics
		} = fixture.metrics
		binding.RunQuickPerformanceTest.mockResolvedValueOnce({
			...fixture,
			metrics: legacyMetrics,
		} as never)

		const report = await createDesktopClient().runQuickPerformanceTest({
			address_mode: "base_url", url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency", request_count: 4,
			duration_ms: 0, concurrency: 2, rate_per_second: 0, max_in_flight: 0,
			arrival_pattern: "constant", workload_mode: "fixed", random_seed: 0,
			input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
			warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
			slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
			capacity_enabled: false, capacity_start: 0, capacity_step: 0, timeout_ms: 30_000,
			input_tokens: 20, output_tokens: 32,
		})

		expect(report.metrics.schedule_lag_p90_ms).toBe(0)
		expect(report.metrics.schedule_lag_p99_ms).toBe(0)
	})

	it("parses schema v2 load semantics without fabricating them for legacy reports", async () => {
		const binding = installBinding(FIXTURE_WORKSPACE)
		const fixture = performanceReportFixture()
		binding.RunQuickPerformanceTest.mockResolvedValueOnce({
			...fixture,
			schema_version: 2,
			profile: {
				...fixture.profile,
				load_mode: "open_loop",
				concurrency: 0,
				rate_per_second: 12.5,
				max_in_flight: 37,
				arrival_pattern: "poisson",
				workload_mode: "normal",
				random_seed: 424242,
				input_tokens_stddev: 4,
				output_tokens_stddev: 8,
				shared_prefix_tokens: 10,
				warmup_requests: 1,
				ramp_duration_ms: 1_000,
				ramp_request_cap: 0,
				slice_duration_ms: 1_000,
			},
			progress: { ...fixture.progress, offered: 4, capped: false },
			metrics: {
				...fixture.metrics,
				offered_qps: 15,
				launched_qps: 12.5,
				completed_qps: 11,
				successful_request_qps: 10,
				request_qps: 10,
			},
			samples: fixture.samples.map((sample, index) => ({
				...sample,
				target_input_tokens: 18 + index,
				target_output_tokens: 28 + index,
				provider_target: "must be dropped",
			})),
			request_budget: { limit: 10_000, warmup_cap: 1, ramp_cap: 15, measured_cap: 4, total_cap: 20, provider_internal: "drop" },
			warmup: phaseThreeTrafficSummary(1, 1),
			ramp: {
				shape: "linear_staircase", duration_ms: 1_000, steps: 10, target_rate_per_second: 12.5,
				completed_window: true,
				traffic: { ...phaseThreeTrafficSummary(15, 15), send_duration_ms: 1_000, total_duration_ms: 1_020 },
				provider_internal: "drop",
			},
			time_slices: [{
				slice_index: 0, start_ms: 0, end_ms: 320, partial: true,
				offered: 4, launched: 4, completed: 4, succeeded: 4, failed: 0, rejected: 0,
				prompt_tokens: 80, completion_tokens: 128, cached_tokens: 20,
				ttft: { count: 4, p50_ms: 30, p95_ms: 42, p99_ms: 44 },
				tpot: { count: 4, p50_ms: 4, p95_ms: 6, p99_ms: 7 },
				e2e: { count: 4, p50_ms: 60, p95_ms: 80, p99_ms: 84 },
				provider_internal: "drop",
			}],
		} as never)

		const v2 = await createDesktopClient().runQuickPerformanceTest({
			address_mode: "base_url", url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", load_mode: "open_loop",
			request_count: 4, duration_ms: 0, concurrency: 0, rate_per_second: 12.5,
			max_in_flight: 37, arrival_pattern: "poisson", workload_mode: "normal", random_seed: 424242,
			input_tokens_stddev: 4, output_tokens_stddev: 8, shared_prefix_tokens: 10,
			warmup_requests: 1, ramp_duration_ms: 1_000, ramp_request_cap: 0, slice_duration_ms: 1_000,
			slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
			capacity_enabled: false, capacity_start: 0, capacity_step: 0,
			timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
		})
		expect(v2.schema_version).toBe(2)
			expect(v2.profile).toMatchObject({
			load_mode: "open_loop", rate_per_second: 12.5, max_in_flight: 37,
			arrival_pattern: "poisson", workload_mode: "normal", random_seed: 424242,
			input_tokens_stddev: 4, output_tokens_stddev: 8, shared_prefix_tokens: 10,
			warmup_requests: 1, ramp_duration_ms: 1_000, ramp_request_cap: 0, slice_duration_ms: 1_000,
		})
		expect(v2.progress.offered).toBe(4)
		expect(v2.progress.capped).toBe(false)
		expect(v2.request_budget).toEqual({ limit: 10_000, warmup_cap: 1, ramp_cap: 15, measured_cap: 4, total_cap: 20 })
		expect(v2.ramp).toMatchObject({ shape: "linear_staircase", steps: 10, target_rate_per_second: 12.5 })
		expect(v2.time_slices?.[0]).toMatchObject({ slice_index: 0, offered: 4, completed: 4 })
		expect(v2.metrics).toMatchObject({ offered_qps: 15, launched_qps: 12.5, completed_qps: 11, successful_request_qps: 10 })
		expect(v2.samples[0]).toMatchObject({ target_input_tokens: 18, target_output_tokens: 28 })
		expect(JSON.stringify(v2.samples)).not.toContain("must be dropped")

		binding.RunQuickPerformanceTest.mockResolvedValueOnce({
			...fixture,
			schema_version: 2,
			profile: { ...fixture.profile, load_mode: "fixed_concurrency" },
			progress: { ...fixture.progress, offered: 4 },
			metrics: {
				...fixture.metrics,
				offered_qps: 13.3,
				launched_qps: 13.3,
				completed_qps: 12.5,
				successful_request_qps: 12.5,
			},
		} as never)
		const phaseOneV2 = await createDesktopClient().runQuickPerformanceTest({
			address_mode: "base_url", url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency",
			request_count: 4, duration_ms: 0, concurrency: 2, rate_per_second: 0,
			max_in_flight: 0, arrival_pattern: "constant", workload_mode: "fixed", random_seed: 0,
			input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
			warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
			slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
			capacity_enabled: false, capacity_start: 0, capacity_step: 0,
			timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
		} as never)
		expect(phaseOneV2.profile.arrival_pattern).toBeUndefined()
		expect(phaseOneV2.profile.workload_mode).toBeUndefined()
		expect(phaseOneV2.samples[0].target_input_tokens).toBeUndefined()

		binding.RunQuickPerformanceTest.mockResolvedValueOnce(fixture as never)
		const legacy = await createDesktopClient().runQuickPerformanceTest({
			address_mode: "base_url", url: "https://api.example.test/v1",
			api_key: "sk-secret", model_id: "gpt-test", load_mode: "fixed_concurrency",
			request_count: 4, duration_ms: 0, concurrency: 2, rate_per_second: 0,
			max_in_flight: 0, arrival_pattern: "constant", workload_mode: "fixed", random_seed: 0,
			input_tokens_stddev: 0, output_tokens_stddev: 0, shared_prefix_tokens: 0,
			warmup_requests: 0, ramp_duration_ms: 0, ramp_request_cap: 0, slice_duration_ms: 0,
			slo_ttft_ms: 0, slo_tpot_ms: 0, slo_e2e_ms: 0, slo_target_percent: 0,
			capacity_enabled: false, capacity_start: 0, capacity_step: 0,
			timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
		})
		expect(legacy.schema_version).toBe(1)
		expect(legacy.profile.load_mode).toBeUndefined()
		expect(legacy.progress.offered).toBeUndefined()
		expect(legacy.metrics.offered_qps).toBeUndefined()
		expect(legacy.metrics.successful_request_qps).toBeUndefined()
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
		model_ids: plan.model_ids,
		channel_ids: plan.channel_ids,
		suites: plan.suites.map((entry) => ({
			entry_id: entry.entry_id,
			suite_id: entry.suite_id,
			suite_revision: entry.suite_revision,
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
		["CreateModel", "createModel", { name: "new model", protocol: "openai-chat", capabilities: ["chat"] }],
		["UpdateModel", "updateModel", { id: model.id, expected_revision: model.revision, name: model.name, protocol: model.protocol, capabilities: model.capabilities }],
		["DeleteModel", "deleteModel", deletion(model.id, model.revision)],
		["CreateChannel", "createChannel", { name: "new channel", base_url: "https://example.test/v1", api_key: "test-key-1234", protocol: "openai-chat", enabled: true }],
		["UpdateChannel", "updateChannel", { id: channel.id, expected_revision: channel.revision, name: channel.name, base_url: channel.base_url, api_key: "test-key-5678", protocol: channel.protocol, enabled: channel.enabled }],
		["DeleteChannel", "deleteChannel", deletion(channel.id, channel.revision)],
		["CreateChannelModel", "createChannelModel", { channel_id: mapping.channel_id, model_id: mapping.model_id, upstream_model_name: "new-upstream" }],
		["UpdateChannelModel", "updateChannelModel", { id: mapping.id, expected_revision: mapping.revision, upstream_model_name: mapping.upstream_model_name }],
		["DeleteChannelModel", "deleteChannelModel", deletion(mapping.id, mapping.revision)],
		["CreateTestCase", "createTestCase", withoutIdentity(testCase)],
		["UpdateTestCase", "updateTestCase", { ...withoutIdentity(testCase), id: testCase.id, expected_revision: testCase.revision }],
		["DeleteTestCase", "deleteTestCase", deletion(testCase.id, testCase.revision)],
		["CreateSuite", "createSuite", { key: "new-suite", name: "new suite", protocol: suite.protocol, model_target: suite.model_target, cases: suite.cases }],
		["UpdateSuite", "updateSuite", { id: suite.id, expected_revision: suite.revision, key: suite.key, name: suite.name, protocol: suite.protocol, model_target: suite.model_target, cases: suite.cases }],
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
			schema_version: 4,
		} as never)
		const client = createDesktopClient()

		const result = client.createChannel({
			name: "new channel",
			base_url: "https://example.test/v1",
			api_key: "test-key-1234",
			protocol: "openai-chat",
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
			protocol: "openai-chat",
			enabled: true,
		})

		await expect(result).rejects.toMatchObject({ code: "catalog_invalid" })
		expect(binding.CreateChannel).toHaveBeenCalledOnce()
		expect(binding.ReportFrontendDiagnostic).not.toHaveBeenCalled()
	})

  it.each([
    [{ ...FIXTURE_WORKSPACE, schema_version: 2 }, "协议版本"],
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

  it("accepts Suite history whose Case members own a variable request schedule", async () => {
    const payload = structuredClone(FIXTURE_WORKSPACE)
    Object.assign(payload.runs[0], { source: "quick_task", planned: 0, duration_ms: 0, completed: 6, passed: 5, failed: 1 })
    installBinding(payload)
    expect((await createDesktopClient().getWorkspace()).runs[0]).toMatchObject({ source: "quick_task", planned: 0, completed: 6 })
  })

  it.each([
    { source: "quick_task", planned: 1, duration_ms: 0 },
    { source: "quick_task", planned: 0, duration_ms: 1000 },
    { source: "unknown", planned: 0, duration_ms: 0 },
    { planned: 0, duration_ms: 0 },
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
      type: "request.single",
      type_version: 1,
      spec: { request: { method: "POST", path: "/v1/chat/completions", headers: {}, body: {} }, expected: { allowed_http_statuses: [200], stream_completion: "not_applicable" }, assertions: [{ kind: "custom", config: { mode: "manual" } }] },
    })
    installBinding(FIXTURE_WORKSPACE, catalog)

    const snapshot = await createDesktopClient().getCatalog()
    expect(snapshot.test_cases[0]).toMatchObject({
      key: longKey,
      dimension: longDimension,
      type: "request.single",
      type_version: 1,
    })
  })

  it.each([
    ["catalog_unavailable", "无法读取模型、渠道与用例目录", "GetCatalog", "getCatalog"],
    ["reports_unavailable", "无法读取测试报告", "GetReports", "getReports"],
    ["plan_protocol_mismatch", "计划中的用例、模型和渠道协议不一致", "CreatePlan", "createPlan"],
    ["run_invalid", "测试输入无效", "StartQuickTask", "startQuickTask"],
    ["run_not_runnable", "所选任务或目标无法执行", "StartQuickTask", "startQuickTask"],
    ["catalog_invalid", "目录内容无效", "CreateModel", "createModel"],
    ["catalog_revision_conflict", "对象版本已变化或仍被引用", "UpdateModel", "updateModel"],
    ["catalog_not_found", "对象已删除或不存在", "DeleteModel", "deleteModel"],
    ["catalog_saved_refresh_failed", "已保存，但目录刷新失败", "CreateModel", "createModel"],
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
		catalog.test_cases[0].model_targets = [42]
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
		catalog.test_cases[0].model_targets = [42]
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
    GetWorkspace: vi.fn(async () => structuredClone(payload)),
    GetCatalog: vi.fn(async () => structuredClone(catalog)),
    GetReports: vi.fn(async () => structuredClone(reports)),
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
    StartRun: vi.fn(async () => structuredClone(payload)),
		StartRunTarget: vi.fn(async () => structuredClone(payload)),
		StartQuickTask: vi.fn(async () => FIXTURE_WORKSPACE.runs[0].id),
		GetQuickTask: vi.fn(),
    RememberQuickTaskCredential: vi.fn(async () => undefined), ForgetQuickTaskCredential: vi.fn(async () => undefined),
    StopSending: vi.fn(async () => structuredClone(payload)),
    CancelRun: vi.fn(async () => structuredClone(payload)),
		StartComparison: vi.fn(async () => structuredClone(EMPTY_COMPARISONS)),
		RunQuickPerformanceTest: vi.fn(async (_command?: unknown, _progressID?: string) => performanceReportFixture()),

	CreateModel: vi.fn(async () => structuredClone(catalog)),
	UpdateModel: vi.fn(async () => structuredClone(catalog)),
	DeleteModel: vi.fn(async () => structuredClone(catalog)),
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
  return {
    schema_version: 1, archived: false, archive_status: "not_attempted", model_id: "gpt-test", success: true, address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: { request_count: 4, duration_ms: 0, concurrency: 2, timeout_ms: 30_000, input_tokens: 20, output_tokens: 32 },
    progress: {
      phase: "completed", planned: 4, launched: 4, completed: 4,
      peak_in_flight: 2, succeeded: 4, failed: 0, rejected: 0,
      send_duration_ms: 300, drain_duration_ms: 20, total_duration_ms: 320,
    },
    metrics: {
      completed: 4, succeeded: 4, failed: 0, timed_out: 0,
      success_rate_percent: 100, request_qps: 12.5, rpm: 750,
      input_tpm: 15_000, output_tpm: 24_000, total_tpm: 39_000, generation_tps: 400,
      ttft_p50_ms: 30, ttft_p90_ms: 40, ttft_p95_ms: 42, ttft_p99_ms: 44, ttft_average_ms: 32,
      tpot_p50_ms: 4, tpot_p90_ms: 5, tpot_p95_ms: 6, tpot_p99_ms: 7, tpot_average_ms: 4.5,
      e2e_p50_ms: 60, e2e_p90_ms: 75, e2e_p95_ms: 80, e2e_p99_ms: 84, e2e_average_ms: 65,
			schedule_lag_p50_ms: 1.5, schedule_lag_p90_ms: 2.7, schedule_lag_p95_ms: 2.85, schedule_lag_p99_ms: 2.97, schedule_lag_average_ms: 1.5,
      prompt_tokens: 80, completion_tokens: 128, cached_tokens: 20, cache_rate_percent: 25,
    },
    samples: [
      performanceSample(0, 60, 60, 30, 4), performanceSample(1, 75, 74, 40, 5),
      performanceSample(2, 84, 82, 44, 7), performanceSample(3, 90, 88, 42, 6),
    ],
    failures: [],
  }
}

function performanceSample(requestIndex: number, finished: number, e2e: number, ttft: number, tpot: number) {
  return { request_index: requestIndex, scheduled_offset_ms: 0, started_offset_ms: requestIndex, finished_offset_ms: finished, schedule_lag_ms: requestIndex, e2e_ms: e2e, ttft_ms: ttft, tpot_ms: tpot, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 }
}

function phaseThreeTrafficSummary(requestCap: number, completed: number) {
  return {
    request_cap: requestCap, offered: completed, launched: completed, completed, succeeded: completed,
    failed: 0, timed_out: 0, rejected: 0, peak_in_flight: Math.min(2, completed),
    prompt_tokens: completed * 20, completion_tokens: completed * 32, cached_tokens: completed * 5,
    send_duration_ms: 100, drain_duration_ms: 20, total_duration_ms: 120,
    failures: [], stopped: false, capped: false, provider_internal: "drop",
  }
}

function zeroPerformanceMetrics() {
  const report = performanceReportFixture().metrics
  return Object.fromEntries(Object.keys(report).map((key) => [key, 0]))
}

function reportDetailFixture(reportID: string) {
	const summary = FIXTURE_REPORTS.reports.find((report) => report.id === reportID) ?? FIXTURE_REPORTS.reports[0]
	return {
		schema_version: 2,
		source: "run",
		report: {
			id: summary.id, run_id: summary.run_id!, run_status: summary.run_status, generated_at: summary.generated_at,
			model: { id: "22222222-2222-4222-8222-222222222221", name: summary.model_name },
			channel: { id: "33333333-3333-4333-8333-333333333331", name: summary.channel_name },
			environment: { os: "windows", arch: "amd64", region: "local", network_egress: "direct", app_version: "test", engine_version: "go-core-v1" },
			conclusion: { passed: summary.passed, verdict: summary.verdict, issues: [] },
			sla: {}, metrics: {}, case_results: [],
		},
		request_results: [],
		suites: [{
			suite_entry_id: "88888888-8888-4888-8888-888888888881",
			suite_id: "88888888-8888-4888-8888-888888888882",
			suite_revision: 1,
			suite_key: "fixture-suite",
			suite_name: "Fixture suite",
			status: summary.run_status,
			conclusion: { passed: summary.passed, verdict: summary.passed ? "pass" : "fail", issues: [] },
			sla: {}, metrics: {}, timeline: [], distributions: [], cases: [],
		}],
		unassigned_request_results: [],
	}
}

function withoutIdentity<T extends { id: string; revision: number }>(value: T): Omit<T, "id" | "revision"> {
  const { id: _id, revision: _revision, ...rest } = value
  return rest
}
