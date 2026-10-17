import { vi } from "vitest"
import type { DesktopClient } from "@/app/desktop-client"
import type { JobView, SubmissionView, ControlView, PerformanceResultView } from "@/features/jobs/data"
import { EMPTY_REMOTE_NODES } from "@/features/remote-nodes/data"
import type { QuickPerformanceReport } from "@/features/quick-test/data"

export const JOB_ID = "a1111111-1111-4111-8111-111111111111"
export const COMMAND_ID = "b1111111-1111-4111-8111-111111111111"
export const REPORT_ID = "c1111111-1111-4111-8111-111111111111"
export const RUN_ID = "d1111111-1111-4111-8111-111111111111"
export const JOB_TIME = "2026-10-16T00:00:00Z"

export function submissionFixture(overrides: Partial<SubmissionView> = {}): SubmissionView {
  return { delivery_revision: 2, delivery: "accepted", observation: "",
    prepared_at: JOB_TIME, delivery_updated_at: JOB_TIME,
    confirmation: { status: "running", revision: 1, accepted_at: JOB_TIME }, ...overrides }
}

export function jobFixture(overrides: Partial<JobView> = {}): JobView {
  return {
    schema_version: 1, job_id: JOB_ID, kind: "performance",
    location: { kind: "local", configuration_id: "" },
    executor: { node_id: "e1111111-1111-4111-8111-111111111111",
      installation_id: "f1111111-1111-4111-8111-111111111111",
      version: "test-current", build_id: "a".repeat(64), os: "windows", arch: "amd64" },
    submission: submissionFixture(),
    run_ids: [], report_ids: [REPORT_ID], comparison_id: "",
    execution: { status: "running", revision: 1, latest_sequence: 1,
      accepted_at: JOB_TIME, updated_at: JOB_TIME, failure: null },
    progress: null, performance_result_ready: false,
    ...overrides,
  }
}

export function controlFixture(overrides: Partial<ControlView> = {}): ControlView {
  return { schema_version: 1, command_id: COMMAND_ID, job_id: JOB_ID,
    kind: "stop_sending", expected_revision: 1, delivery_revision: 1,
    state: "pending", observation: "", created_at: JOB_TIME, updated_at: JOB_TIME,
    confirmed_at: null, execution: null, ...overrides }
}

export function performanceResultFixture(report: QuickPerformanceReport): PerformanceResultView {
  const job = jobFixture()
  const reportID = report.report_id ?? REPORT_ID
  return { schema_version: 1, job_id: job.job_id, report_id: reportID,
    location: job.location, executor: job.executor,
    terminal: { status: "completed", revision: 2, latest_sequence: 3,
      accepted_at: JOB_TIME, updated_at: JOB_TIME, failure: null },
    sealed_at: JOB_TIME, execution_started: true, observation_state: "available",
    observation: { revision: 2, recorded_at: JOB_TIME, stage: "measured", boundary: "returned",
      rung_index: null, report: { ...report, report_id: reportID,
        execution_origin: { kind: job.location.kind, node_id: job.executor.node_id,
          installation_id: job.executor.installation_id, engine_build_id: job.executor.build_id,
          runner_version: job.executor.version, os: job.executor.os, arch: job.executor.arch } } } }
}

export type ExecutionClient = Pick<DesktopClient, "getRemoteNodes" | "launchJob" | "getJob" | "getJobs" |
  "getJobPerformanceResult" | "controlJob" | "getJobControls" | "retryJobControl">

export function executionClient(report?: QuickPerformanceReport) {
  const active = jobFixture()
  const accepted = jobFixture({ execution: null })
  const completed = jobFixture({
    submission: submissionFixture({ confirmation: { status: "completed", revision: 2, accepted_at: JOB_TIME } }),
    execution: { ...active.execution!, status: "completed", revision: 2, latest_sequence: 3 },
    performance_result_ready: true,
  })
  return {
    getRemoteNodes: vi.fn(async () => structuredClone(EMPTY_REMOTE_NODES)),
    launchJob: vi.fn<ExecutionClient["launchJob"]>(async () => accepted),
    getJob: vi.fn<ExecutionClient["getJob"]>(async () => report ? completed : active),
    getJobs: vi.fn<ExecutionClient["getJobs"]>(async after => ({ schema_version: 1, jobs: [], next_cursor: after, has_more: false })),
    getJobPerformanceResult: vi.fn<ExecutionClient["getJobPerformanceResult"]>(async () => {
      if (!report) throw new Error("Test outcome is not available")
      return performanceResultFixture(report)
    }),
    controlJob: vi.fn<ExecutionClient["controlJob"]>(async () => controlFixture()),
    getJobControls: vi.fn<ExecutionClient["getJobControls"]>(async (jobID, after) => ({
      schema_version: 1, job_id: jobID, commands: [], next_cursor: after, has_more: false,
    })),
    retryJobControl: vi.fn<ExecutionClient["retryJobControl"]>(async () => controlFixture()),
  } satisfies ExecutionClient
}
