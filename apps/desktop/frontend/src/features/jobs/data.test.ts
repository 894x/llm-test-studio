import { describe, expect, it } from "vitest"
import { controlFixture, jobFixture, submissionFixture, COMMAND_ID, JOB_ID, JOB_TIME, REPORT_ID } from "@/test/jobs-fixture"
import { parseControlPage, parseControlView, parseJobPage, parseJobView, parsePerformanceResult } from "./data"

describe("current durable job boundary", () => {
  it("keeps delivery, confirmation and synchronized execution independent", () => {
    const confirmed = jobFixture({ execution: null })
    expect(parseJobView(confirmed)).toEqual(confirmed)
    const pending = jobFixture({ submission: submissionFixture({ delivery: "pending", confirmation: null, observation: "unreachable" }), execution: null })
    expect(parseJobView(pending)).toEqual(pending)
    expect(parseJobView(jobFixture({ kind: "run", report_ids: [] })).kind).toBe("run")
  })

  it.each([
    { extra: "unknown" }, { schema_version: 0 }, { job_id: "invalid" }, { kind: "plan" },
    { kind: "quick_task" }, { submission: submissionFixture({ delivery_revision: 0 }) },
    { submission: { ...submissionFixture(), delivery_revision: "2" } },
    { submission: { ...submissionFixture(), delivery: "failed" } },
    { submission: submissionFixture({ confirmation: null }) },
    { submission: { ...submissionFixture(), observation: "timeout" } },
    { submission: submissionFixture({ prepared_at: "2026-10-16" }) },
    { submission: { ...submissionFixture(), unexpected: true } },
    { submission: undefined }, { submission: null },
    { run_ids: null }, { report_ids: [REPORT_ID, REPORT_ID] },
    { location: { kind: "remote", configuration_id: "" } },
    { executor: { ...jobFixture().executor, build_id: "unverified" } },
    { executor: { ...jobFixture().executor, secret: "rejected" } },
    { execution: { ...jobFixture().execution!, revision: 1.5 } },
    { performance_result_ready: true },
  ])("rejects malformed current job data %j", change => {
    expect(() => parseJobView({ ...jobFixture(), ...change })).toThrow()
  })

  it("accepts shared remote execution without inventing local submission state", () => {
    const shared = jobFixture({ submission: null, location: { kind: "remote", configuration_id: JOB_ID },
      executor: { ...jobFixture().executor, version: "runner-r4", build_id: "b".repeat(64), os: "linux" } })
    expect(parseJobView(shared)).toEqual(shared)
    expect(() => parseJobView({ ...shared, execution: null })).toThrow()
    expect(() => parseJobView({ ...shared, location: { kind: "local", configuration_id: "" } })).toThrow()
  })

  it("rejects each replaced top-level field and the previous whole DTO without mutating it", () => {
    const current = jobFixture()
    const localSubmission = submissionFixture()
    for (const [key, value] of Object.entries(localSubmission)) {
      const raw = { ...current, [key]: value }
      const before = structuredClone(raw)
      expect(() => parseJobView(raw)).toThrow()
      expect(raw).toEqual(before)
    }
    const { submission: _submission, ...sharedFields } = current
    const previous = { ...sharedFields, ...localSubmission }
    const before = structuredClone(previous)
    expect(() => parseJobView(previous)).toThrow()
    expect(previous).toEqual(before)
  })

  it("keeps current local delivery constraints inside the submission object", () => {
    const pendingWithExecution = jobFixture({ submission: submissionFixture({ delivery: "pending", confirmation: null }) })
    expect(parseJobView(pendingWithExecution)).toEqual(pendingWithExecution)
    const prepared = jobFixture({ submission: submissionFixture({ delivery: "prepared", confirmation: null }), execution: null })
    expect(parseJobView(prepared)).toEqual(prepared)
    expect(() => parseJobView({ ...prepared, execution: jobFixture().execution })).toThrow()
    for (const key of Object.keys(submissionFixture())) {
      const incomplete: Record<string, unknown> = { ...submissionFixture() }
      delete incomplete[key]
      expect(() => parseJobView({ ...jobFixture(), submission: incomplete })).toThrow()
    }
  })

  it("validates bounded, non-null pages and current UUID cursors", () => {
    const page = { schema_version: 1, jobs: [jobFixture()], next_cursor: JOB_ID, has_more: false }
    expect(parseJobPage(page)).toEqual(page)
    for (const change of [{ jobs: null }, { jobs: Array(26).fill(jobFixture()) }, { jobs: [jobFixture(), jobFixture()] }, { has_more: true, next_cursor: "" }, { next_cursor: "offset:25" }, { previous_cursor: "" }]) {
      expect(() => parseJobPage({ ...page, ...change })).toThrow()
    }
  })

  it("preserves pending and rejected control intent without task state", () => {
    expect(parseControlView(controlFixture())).toEqual(controlFixture())
    expect(parseControlView(controlFixture({ state: "rejected", observation: "rejected", delivery_revision: 2 })).state).toBe("rejected")
    const confirmed = controlFixture({ state: "confirmed", delivery_revision: 2, confirmed_at: JOB_TIME, execution: jobFixture().execution })
    expect(parseControlView(confirmed)).toEqual(confirmed)
    const page = { schema_version: 1, job_id: JOB_ID, commands: [controlFixture()], next_cursor: COMMAND_ID, has_more: false }
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

describe.each(["GetJobs", "GetJobControls"] as const)("%s current keyset pagination", method => {
  const parse = method === "GetJobs" ? parseJobPage : parseControlPage
  const cursor = method === "GetJobs" ? JOB_ID : COMMAND_ID
  const page = (empty: boolean, nextCursor: unknown, hasMore: unknown) => ({
    schema_version: 1,
    ...(method === "GetJobs" ? { jobs: empty ? [] : [jobFixture()] } : { job_id: JOB_ID, commands: empty ? [] : [controlFixture()] }),
    next_cursor: nextCursor,
    has_more: hasMore,
  })

  it("accepts the last nonempty SQL page with its last ID and has_more false", () => {
    const raw = page(false, cursor, false)
    expect(parse(raw)).toEqual(raw)
  })

  it("accepts an exhausted empty SQL tail retaining the incoming cursor", () => {
    const raw = page(true, cursor, false)
    expect(parse(raw)).toEqual(raw)
  })

  it("accepts an empty initial SQL page without a cursor", () => {
    const raw = page(true, "", false)
    expect(parse(raw)).toEqual(raw)
  })

  it("requires a nonempty UUID only when another page is available", () => {
    const raw = page(false, cursor, true)
    expect(parse(raw)).toEqual(raw)
    expect(() => parse(page(false, "", true))).toThrow()
  })

  it.each([null, undefined, 0, true, "offset:25", "not-a-uuid"])("rejects invalid cursor %j without mutating the response", invalidCursor => {
    for (const hasMore of [false, true]) {
      const raw = page(false, invalidCursor, hasMore)
      const before = structuredClone(raw)
      expect(() => parse(raw)).toThrow()
      expect(raw).toEqual(before)
    }
  })

  it("keeps has_more strictly boolean", () => {
    expect(() => parse(page(false, cursor, "false"))).toThrow()
  })
})
