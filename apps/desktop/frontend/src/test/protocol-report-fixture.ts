import type { FormalReportDetail, ReportEntryDetail, ReportResult, ReportSnapshot } from "@/features/reports/data"
import type { VerificationStatus } from "@/features/protocols/types"

export function reportID(index: number) { return `10000000-0000-4000-8000-${String(index).padStart(12, "0")}` }
export function protocolReportFixture(status: VerificationStatus = "passed"): FormalReportDetail {
  const summary = { status, passed: status === "passed" ? 1 : 0, failed: status === "failed" ? 1 : 0, observed: status === "not_applicable" ? 1 : 0, indeterminate: status === "indeterminate" ? 1 : 0 }
  const request: ReportResult = {
    id: reportID(10), request_id: "opaque-request-id", entry_id: reportID(4), case_id: reportID(6), execution_status: "completed",
    verification: { status, assertions: status === "not_applicable" ? [] : [{ id: "expected-status", source: "http.status", operator: "equals", expected: 400, actual: 400, status }] },
    metrics: { e2e_ms: 32 }, observation: { protocol: "openai-chat", http_status: 400, response: { error: { code: "invalid_parameter" } }, metrics: { e2e_ms: 32 }, artifacts: [], exchanges: [], issues: [] },
  }
  const entry: ReportEntryDetail = {
    entry_id: reportID(4), target_kind: "suite", target_id: reportID(3), name: "Snapshot suite", key: "snapshot-suite", protocol: "openai-chat", seed: 12,
    warmup_count: 0, settings: {}, parameters: { content_length: 4000 }, load: { mode: "single", concurrency: 1, request_count: 1, rate_per_second: 0, duration_ms: 0, request_timeout_ms: 30000 },
    status: "completed", conclusion: { passed: status === "passed", verdict: status, issues: [] }, verification: summary, sla: {}, metrics: {}, timeline: [], distributions: [],
    cases: [{ case_id: reportID(6), revision: 2, key: "reject-parameter", name: "Reject invalid parameter", protocol: "openai-chat", verification: summary, metrics: { e2e_ms: { value: 32, samples: 1, unit: "ms" } }, summary_result: { ...request, id: reportID(11), request_id: undefined }, request_results: [request] }],
  }
  return { schema_version: 3, source: "run", report: {
    id: reportID(1), run_id: reportID(2), protocol: "openai-chat", run_status: "completed", generated_at: "2026-09-09T14:00:00Z",
    model: { id: reportID(7), name: "test-model" }, channel: { id: reportID(8), name: "test-channel" },
    environment: { os: "windows", arch: "amd64", region: "", network_egress: "", app_version: "test", engine_version: "test" },
    conclusion: { passed: status === "passed", verdict: status === "not_applicable" ? "observed" : status === "passed" ? "pass" : "fail", issues: [] },
    sla: {}, metrics: {}, case_results: [entry.cases[0].summary_result!],
  }, request_results: [request], entries: [entry], unassigned_request_results: [] }
}
export function protocolReportSnapshot(detail: FormalReportDetail): ReportSnapshot {
  return { schema_version: 1, reports: [{ id: detail.report.id, source: "run", run_id: detail.report.run_id,
    generated_at: detail.report.generated_at, run_status: detail.report.run_status, plan_name: "Protocol plan", model_name: detail.report.model.name,
    channel_name: detail.report.channel.name, passed: detail.report.conclusion.passed, verdict: detail.report.conclusion.verdict,
    issue_count: 0, case_count: 1, failed_case_count: detail.entries[0].verification.failed, passed_case_count: detail.entries[0].verification.passed,
    verified_case_count: detail.entries[0].verification.passed + detail.entries[0].verification.failed,
    observed_case_count: detail.entries[0].verification.observed, attachment_count: 0 }] }
}
