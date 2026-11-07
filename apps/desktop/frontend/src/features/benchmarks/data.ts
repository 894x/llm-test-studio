import { DesktopDataError } from "@/app/data-error"

export type BenchmarkProtocol = "openai-chat" | "openai-responses" | "anthropic-messages"
export type BenchmarkStatus = "queued" | "running" | "cancelling" | "completed" | "failed" | "cancelled" | "interrupted"
export type BenchmarkDataset = { id: string; name: string; description: string; category: string; source_url: string; default_max_tokens: number }
export type BenchmarkOptions = { sample_limit: number; epochs: number; max_tokens: number; concurrency: number; timeout_seconds: number; temperature: number | null; seed: number; stream: boolean; reasoning_effort: string }
export type BenchmarkTarget = { protocol: BenchmarkProtocol; model: string; channel_id: string; channel_name: string; base_url: string }
export type BenchmarkCommand = { dataset_ids: string[]; connection: { protocol: BenchmarkProtocol; model: string; channel_id: string; base_url: string; api_key: string }; options: BenchmarkOptions }
export type BenchmarkMetric = { scorer: string; name: string; value: number }
export type BenchmarkProvenance = { inspect_ai: string; inspect_evals: string; task: string; task_version: string; dataset_name: string; dataset_location: string; input_sha256: string }
export type BenchmarkDatasetRun = { report_id: string; dataset: BenchmarkDataset; status: BenchmarkStatus; stage: string; total: number; completed: number; failed: number; in_flight: number; metrics: BenchmarkMetric[]; provenance: BenchmarkProvenance | null; error_code: string; started_at: string; finished_at: string }
export type BenchmarkRun = { schema_version: 1; id: string; target: BenchmarkTarget; options: BenchmarkOptions; status: BenchmarkStatus; created_at: string; finished_at: string; datasets: BenchmarkDatasetRun[] }
export type BenchmarkPage = { schema_version: 1; runs: BenchmarkRun[]; next_cursor: string }
export type BenchmarkCatalog = { schema_version: 1; datasets: BenchmarkDataset[]; runtime: { state: "missing" | "preparing" | "ready" | "failed"; error_code: string } }
export type BenchmarkScore = { name: string; value: string; answer: string; explanation: string }
export type BenchmarkSample = { id: string; epoch: number; status: "completed" | "error"; scores: BenchmarkScore[]; input: string; target: string; output: string; input_tokens: number; output_tokens: number; duration_ms: number; error_code: string }
export type BenchmarkReport = { schema_version: 1; run_id: string; target: BenchmarkTarget; options: BenchmarkOptions; result: BenchmarkDatasetRun; samples: BenchmarkSample[]; next_cursor: string }
export type BenchmarkExport = { filename: string; media_type: string; data_base64: string }
export interface BenchmarkClient {
  getBenchmarkDatasets(): Promise<BenchmarkDatasetInventory>
  manageBenchmarkDataset(id: string, action: "download" | "cancel" | "verify" | "remove"): Promise<BenchmarkDatasetDownload>
  getBenchmarkCatalog(): Promise<BenchmarkCatalog>
  prepareBenchmarkRuntime(): Promise<void>
  startBenchmark(command: BenchmarkCommand): Promise<BenchmarkRun>
  getBenchmarks(after: string): Promise<BenchmarkPage>
  getBenchmark(id: string): Promise<BenchmarkRun>
  getBenchmarkReportOwner(reportID: string): Promise<BenchmarkRun>
  cancelBenchmark(id: string): Promise<void>
  getBenchmarkReport(runID: string, reportID: string, after: string): Promise<BenchmarkReport>
  exportBenchmarkReport(runID: string, reportID: string): Promise<BenchmarkExport>
}

export type BenchmarkDatasetDownload = { state: "missing" | "downloading" | "verifying" | "removing" | "ready" | "failed" | "cancelled" | "invalid" | "interrupted" | "outdated"; bytes: number; files: number; samples: number; input_sha256: string; cache_sha256: string; task_version: string; updated_at: string; verified_at: string; error_code: string }
export type BenchmarkDatasetInventory = { schema_version: 1; datasets: { dataset: BenchmarkDataset; download: BenchmarkDatasetDownload }[]; runtime: BenchmarkCatalog["runtime"] }
export const EMPTY_DATASET_DOWNLOAD: BenchmarkDatasetDownload = { state: "missing", bytes: 0, files: 0, samples: 0, input_sha256: "", cache_sha256: "", task_version: "", updated_at: "", verified_at: "", error_code: "" }
export const isDatasetBusy = (state: BenchmarkDatasetDownload["state"]) => ["downloading", "verifying", "removing"].includes(state)

export const DEFAULT_BENCHMARK_OPTIONS: BenchmarkOptions = { sample_limit: 20, epochs: 1, max_tokens: 4096, concurrency: 4, timeout_seconds: 300, temperature: null, seed: 1, stream: false, reasoning_effort: "" }
export const EMPTY_BENCHMARK_PAGE: BenchmarkPage = { schema_version: 1, runs: [], next_cursor: "" }
export const isBenchmarkActive = (status: BenchmarkStatus) => ["queued", "running", "cancelling"].includes(status)
const invalid = (): never => { throw new DesktopDataError("Invalid benchmark data") }
function record(value: unknown, keys: string[]): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return invalid()
  const item = value as Record<string, unknown>
  if (Object.keys(item).length !== keys.length || keys.some(key => !Object.hasOwn(item, key))) return invalid()
  return item
}
function text(value: unknown, max = 2048, empty = false): string {
  return typeof value === "string" && value.length <= max && (empty || value.trim() !== "") && !Array.from(value).some(char => char < " " && char !== "\n" && char !== "\t") ? value : invalid()
}
function integer(value: unknown, min: number, max: number): number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= min && value <= max ? value : invalid()
}
function number(value: unknown): number { return typeof value === "number" && Number.isFinite(value) ? value : invalid() }
function choice<T extends string>(value: unknown, values: readonly T[]): T { return values.includes(value as T) ? value as T : invalid() }
function list<T>(value: unknown, parse: (item: unknown) => T, max: number): T[] {
  return Array.isArray(value) && value.length <= max ? value.map(parse) : invalid()
}
function version(value: unknown): 1 { return value === 1 ? 1 : invalid() }
function uuid(value: unknown): string { const id = text(value, 36); return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id) ? id : invalid() }
function runID(value: unknown): string { const id = text(value, 57); if (!/^\d{20}-/.test(id)) return invalid(); uuid(id.slice(21)); return id }
function cursor(value: unknown): string { const id = text(value, 64, true); return id === "" || /^[a-f0-9]{64}$/.test(id) ? id : invalid() }
function timestamp(value: unknown, empty = false): string { const date = text(value, 64, empty); return (empty && !date) || Number.isFinite(Date.parse(date)) ? date : invalid() }
const status = (value: unknown) => choice(value, ["queued", "running", "cancelling", "completed", "failed", "cancelled", "interrupted"] as const)
function protocol(value: unknown) { return choice(value, ["openai-chat", "openai-responses", "anthropic-messages"] as const) }
function url(value: unknown): string {
  const string = text(value)
  try { const parsed = new URL(string); if (!["https:", "http:"].includes(parsed.protocol) || parsed.username || parsed.password || /[?#\\]/.test(string)) return invalid(); return string } catch { return invalid() }
}
export function parseBenchmarkDataset(value: unknown): BenchmarkDataset {
  const o = record(value, ["id", "name", "description", "category", "source_url", "default_max_tokens"])
  return { id: text(o.id, 128), name: text(o.name, 256), description: text(o.description, 2048), category: text(o.category, 64), source_url: url(o.source_url), default_max_tokens: integer(o.default_max_tokens, 1, 1048576) }
}
function options(value: unknown): BenchmarkOptions {
  const o = record(value, ["sample_limit", "epochs", "max_tokens", "concurrency", "timeout_seconds", "temperature", "seed", "stream", "reasoning_effort"])
  if (typeof o.stream !== "boolean" || (o.temperature !== null && (number(o.temperature) < 0 || number(o.temperature) > 2))) return invalid()
  return { sample_limit: integer(o.sample_limit, 0, 10000), epochs: integer(o.epochs, 1, 32), max_tokens: integer(o.max_tokens, 1, 1048576), concurrency: integer(o.concurrency, 1, 64), timeout_seconds: integer(o.timeout_seconds, 1, 86400), temperature: o.temperature === null ? null : number(o.temperature), seed: integer(o.seed, 0, 2147483647), stream: o.stream, reasoning_effort: choice(o.reasoning_effort, ["", "minimal", "low", "medium", "high", "max"]) }
}
function target(value: unknown): BenchmarkTarget {
  const o = record(value, ["protocol", "model", "channel_id", "channel_name", "base_url"])
  return { protocol: protocol(o.protocol), model: text(o.model, 256), channel_id: o.channel_id === "" ? "" : uuid(o.channel_id), channel_name: text(o.channel_name, 256), base_url: url(o.base_url) }
}
function metric(value: unknown): BenchmarkMetric {
  const o = record(value, ["scorer", "name", "value"])
  return { scorer: text(o.scorer, 256), name: text(o.name, 256), value: number(o.value) }
}
function provenance(value: unknown): BenchmarkProvenance {
  const o = record(value, ["inspect_ai", "inspect_evals", "task", "task_version", "dataset_name", "dataset_location", "input_sha256"])
  const hash = cursor(o.input_sha256); if (!hash) return invalid()
  return { inspect_ai: text(o.inspect_ai, 64), inspect_evals: text(o.inspect_evals, 64), task: text(o.task, 256), task_version: text(o.task_version, 256, true), dataset_name: text(o.dataset_name, 512, true), dataset_location: text(o.dataset_location, 2048, true), input_sha256: hash }
}
function datasetRun(value: unknown): BenchmarkDatasetRun {
  const o = record(value, ["report_id", "dataset", "status", "stage", "total", "completed", "failed", "in_flight", "metrics", "provenance", "error_code", "started_at", "finished_at"])
  const total = integer(o.total, 0, 100000), completed = integer(o.completed, 0, total)
  return { report_id: uuid(o.report_id), dataset: parseBenchmarkDataset(o.dataset), status: status(o.status), stage: choice(o.stage, ["queued", "loading", "running", "completed", "failed", "cancelled", "interrupted"]), total, completed, failed: integer(o.failed, 0, completed), in_flight: integer(o.in_flight, 0, 64), metrics: list(o.metrics, metric, 128), provenance: o.provenance === null ? null : provenance(o.provenance), error_code: choice(o.error_code, ["", "evaluation_failed"]), started_at: timestamp(o.started_at, true), finished_at: timestamp(o.finished_at, true) }
}
export function parseBenchmarkRun(value: unknown): BenchmarkRun {
  const o = record(value, ["schema_version", "id", "target", "options", "status", "created_at", "finished_at", "datasets"])
  const datasets = list(o.datasets, datasetRun, 16)
  if (!datasets.length || new Set(datasets.map(d => d.report_id)).size !== datasets.length) return invalid()
  return { schema_version: version(o.schema_version), id: runID(o.id), target: target(o.target), options: options(o.options), status: status(o.status), created_at: timestamp(o.created_at), finished_at: timestamp(o.finished_at, true), datasets }
}
export function parseBenchmarkPage(value: unknown): BenchmarkPage {
  const o = record(value, ["schema_version", "runs", "next_cursor"])
  const runs = list(o.runs, parseBenchmarkRun, 25)
  if (new Set(runs.map(r => r.id)).size !== runs.length) return invalid()
  return { schema_version: version(o.schema_version), runs, next_cursor: o.next_cursor === "" ? "" : runID(o.next_cursor) }
}
export function parseBenchmarkCatalog(value: unknown): BenchmarkCatalog {
  const o = record(value, ["schema_version", "datasets", "runtime"]), runtime = record(o.runtime, ["state", "error_code"])
  return { schema_version: version(o.schema_version), datasets: list(o.datasets, parseBenchmarkDataset, 100), runtime: { state: choice(runtime.state, ["missing", "preparing", "ready", "failed"]), error_code: choice(runtime.error_code, ["", "runtime_prepare_failed"]) } }
}
function score(value: unknown): BenchmarkScore {
  const o = record(value, ["name", "value", "answer", "explanation"])
  return { name: text(o.name, 256), value: text(o.value, 2048, true), answer: text(o.answer, 24000, true), explanation: text(o.explanation, 24000, true) }
}
function sample(value: unknown): BenchmarkSample {
  const o = record(value, ["id", "epoch", "status", "scores", "input", "target", "output", "input_tokens", "output_tokens", "duration_ms", "error_code"])
  return { id: text(o.id, 512), epoch: integer(o.epoch, 1, 32), status: choice(o.status, ["completed", "error"]), scores: list(o.scores, score, 32), input: text(o.input, 24000, true), target: text(o.target, 24000, true), output: text(o.output, 24000, true), input_tokens: integer(o.input_tokens, 0, Number.MAX_SAFE_INTEGER), output_tokens: integer(o.output_tokens, 0, Number.MAX_SAFE_INTEGER), duration_ms: integer(o.duration_ms, 0, Number.MAX_SAFE_INTEGER), error_code: choice(o.error_code, ["", "sample_failed"]) }
}
export function parseBenchmarkDatasetDownload(value: unknown): BenchmarkDatasetDownload {
  const o = record(value, ["state", "bytes", "files", "samples", "input_sha256", "cache_sha256", "task_version", "updated_at", "verified_at", "error_code"])
  return { state: choice(o.state, ["missing", "downloading", "verifying", "removing", "ready", "failed", "cancelled", "invalid", "interrupted", "outdated"]), bytes: integer(o.bytes, 0, 8 * 1024 ** 3), files: integer(o.files, 0, 100000), samples: integer(o.samples, 0, 100000), input_sha256: cursor(o.input_sha256), cache_sha256: cursor(o.cache_sha256), task_version: text(o.task_version, 256, true), updated_at: timestamp(o.updated_at, true), verified_at: timestamp(o.verified_at, true), error_code: choice(o.error_code, ["", "dataset_download_failed", "dataset_cache_invalid"]) }
}
export function parseBenchmarkDatasetInventory(value: unknown): BenchmarkDatasetInventory {
  const o = record(value, ["schema_version", "datasets", "runtime"])
  const runtime = record(o.runtime, ["state", "error_code"])
  const datasets = list(o.datasets, value => { const item = record(value, ["dataset", "download"]); return { dataset: parseBenchmarkDataset(item.dataset), download: parseBenchmarkDatasetDownload(item.download) } }, 100)
  if (new Set(datasets.map(item => item.dataset.id)).size !== datasets.length) return invalid()
  return { schema_version: version(o.schema_version), datasets, runtime: { state: choice(runtime.state, ["missing", "preparing", "ready", "failed"]), error_code: choice(runtime.error_code, ["", "runtime_prepare_failed"]) } }
}
export function parseBenchmarkReport(value: unknown): BenchmarkReport {
  const o = record(value, ["schema_version", "run_id", "target", "options", "result", "samples", "next_cursor"])
  return { schema_version: version(o.schema_version), run_id: runID(o.run_id), target: target(o.target), options: options(o.options), result: datasetRun(o.result), samples: list(o.samples, sample, 10), next_cursor: cursor(o.next_cursor) }
}
