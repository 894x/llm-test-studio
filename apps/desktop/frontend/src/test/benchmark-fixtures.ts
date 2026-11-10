import manifest from "../../../../../internal/benchmarkruntime/datasets.json"
import { DEFAULT_BENCHMARK_OPTIONS, type BenchmarkReport, type BenchmarkRun } from "@/features/benchmarks/data"

const source = manifest.datasets[0]
const dataset = { id: source.id, name: source.name, description: source.description, category: source.category, source_url: source.source_url, default_max_tokens: source.default_max_tokens }
export const BENCHMARK_RUN_ID = "01794067500000000000-00000000-0000-4000-8000-000000000001"
export const BENCHMARK_REPORT_ID = "00000000-0000-4000-8000-000000000002"
export function benchmarkRun(overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return structuredClone({ schema_version: 1, id: BENCHMARK_RUN_ID,
    target: { protocol: "openai-chat", model: "local-test", channel_id: "", channel_name: "api.example.test", base_url: "https://api.example.test/v1" },
    options: { ...DEFAULT_BENCHMARK_OPTIONS }, status: "completed", created_at: "2026-11-07T01:00:00Z", finished_at: "2026-11-07T01:00:05Z",
    datasets: [{ report_id: BENCHMARK_REPORT_ID, dataset, status: "completed", stage: "completed", total: 1, completed: 1, failed: 0, in_flight: 0,
      metrics: [{ scorer: "choice", name: "accuracy", value: 1 }],
      provenance: { inspect_ai: "0.3.287", inspect_evals: "0.27.0", task: "arc_easy", task_version: "1", dataset_name: "ARC-Easy", dataset_location: "allenai/ai2_arc", input_sha256: "a".repeat(64) },
      error_code: "", started_at: "2026-11-07T01:00:01Z", finished_at: "2026-11-07T01:00:05Z" }], ...overrides } as BenchmarkRun)
}
export function benchmarkReport(): BenchmarkReport {
  const run = benchmarkRun()
  return { schema_version: 1, run_id: run.id, target: run.target, options: run.options, result: run.datasets[0], next_cursor: "",
    samples: [{ id: "question-1", epoch: 1, status: "completed", scores: [{ name: "choice", value: "C", answer: "A", explanation: "The extracted answer matches the reference." }],
      input: "Which planet is closest to the Sun?", target: "A", output: "ANSWER: A", input_tokens: 24, output_tokens: 3, duration_ms: 50, error_code: "" }] }
}
