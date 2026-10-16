import { describe, expect, it } from "vitest"
import { controlFixture, jobFixture, JOB_ID, JOB_TIME, REPORT_ID } from "@/test/jobs-fixture"
import { parseControlPage, parseControlView, parseJobPage, parseJobView, parsePerformanceResult } from "./data"

describe("current durable job boundary", () => {
  it("keeps delivery, confirmation and synchronized execution independent", () => {
    const confirmed = jobFixture({ execution: null })
    expect(parseJobView(confirmed)).toEqual(confirmed)
    const pending = jobFixture({ delivery: "pending", confirmation: null, execution: null, observation: "unreachable" })
    expect(parseJobView(pending)).toEqual(pending)
    expect(parseJobView(jobFixture({ kind: "run", report_ids: [] })).kind).toBe("run")
  })

  it.each([
    { extra: "unknown" }, { schema_version: 0 }, { job_id: "invalid" }, { kind: "plan" },
    { kind: "quick_task" }, { delivery_revision: "2" }, { delivery_revision: 0 },
    { delivery: "failed" }, { confirmation: null }, { observation: "timeout" },
    { prepared_at: "2026-10-16" }, { run_ids: null }, { report_ids: [REPORT_ID, REPORT_ID] },
    { location: { kind: "remote", configuration_id: "" } },
    { executor: { ...jobFixture().executor, build_id: "unverified" } },
    { executor: { ...jobFixture().executor, secret: "rejected" } },
    { execution: { ...jobFixture().execution!, revision: 1.5 } },
    { performance_result_ready: true },
  ])("rejects malformed current job data %j", change => {
    expect(() => parseJobView({ ...jobFixture(), ...change })).toThrow()
  })

  it("validates bounded, non-null pages and current UUID cursors", () => {
    const page = { schema_version: 1, jobs: [jobFixture()], next_cursor: "", has_more: false }
    expect(parseJobPage(page)).toEqual(page)
    for (const change of [{ jobs: null }, { jobs: Array(26).fill(jobFixture()) }, { jobs: [jobFixture(), jobFixture()] }, { has_more: true }, { next_cursor: "offset:25" }, { previous_cursor: "" }]) {
      expect(() => parseJobPage({ ...page, ...change })).toThrow()
    }
  })

  it("preserves pending and rejected control intent without task state", () => {
    expect(parseControlView(controlFixture())).toEqual(controlFixture())
    expect(parseControlView(controlFixture({ state: "rejected", observation: "rejected", delivery_revision: 2 })).state).toBe("rejected")
    const confirmed = controlFixture({ state: "confirmed", delivery_revision: 2, confirmed_at: JOB_TIME, execution: jobFixture().execution })
    expect(parseControlView(confirmed)).toEqual(confirmed)
    const page = { schema_version: 1, job_id: JOB_ID, commands: [controlFixture()], next_cursor: "", has_more: false }
    expect(parseControlPage(page)).toEqual(page)
    expect(() => parseControlPage({ ...page, job_id: REPORT_ID })).toThrow()
    expect(() => parseControlView({ ...confirmed, arbitrary: "state" })).toThrow()
    expect(() => parseControlView(controlFixture({ state: "confirmed" }))).toThrow()
    expect(() => parseControlView(controlFixture({ state: "rejected", observation: "" }))).toThrow()
  })

  it("keeps not-started and interrupted-without-checkpoint distinct", () => {
    const job = jobFixture()
    const envelope = {
      schema_version: 1, job_id: JOB_ID, report_id: REPORT_ID, location: job.location, executor: job.executor,
      terminal: { ...job.execution!, status: "cancelled" }, sealed_at: JOB_TIME,
      execution_started: false, observation_state: "not_started", observation: null,
    }
    expect(parsePerformanceResult(envelope).observation).toBeNull()
    expect(parsePerformanceResult({ ...envelope, execution_started: true, observation_state: "interrupted" }).observation_state).toBe("interrupted")
    expect(() => parsePerformanceResult({ ...envelope, execution_started: true })).toThrow()
    expect(() => parsePerformanceResult({ ...envelope, observation_state: "available" })).toThrow()
    expect(() => parsePerformanceResult({ ...envelope, extra: "no" })).toThrow()
    expect(() => parsePerformanceResult({ ...envelope, terminal: job.execution })).toThrow()
  })
})
