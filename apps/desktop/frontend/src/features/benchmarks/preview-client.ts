import manifest from "../../../../../../internal/benchmarkruntime/datasets.json"
import { EMPTY_DATASET_DOWNLOAD, type BenchmarkClient } from "./data"

// Vite preview reads the shipped catalog but never simulates provider execution.
export function benchmarkPreviewClient(): BenchmarkClient {
  const unavailable = async (): Promise<never> => { throw new Error("Benchmark execution requires the desktop application") }
  return {
    getBenchmarkDatasets: async () => ({ schema_version: 1, runtime: { state: "missing", error_code: "" }, datasets: manifest.datasets.map(({ id, name, description, category, source_url, default_max_tokens }) => ({ dataset: { id, name, description, category, source_url, default_max_tokens }, download: { ...EMPTY_DATASET_DOWNLOAD } })) }),
    manageBenchmarkDataset: unavailable,
    getBenchmarkCatalog: async () => ({ schema_version: 1, runtime: { state: "missing", error_code: "" },
      datasets: manifest.datasets.map(({ id, name, description, category, source_url, default_max_tokens }) =>
        ({ id, name, description, category, source_url, default_max_tokens })) }),
    getBenchmarks: async () => ({ schema_version: 1, runs: [], next_cursor: "" }),
    prepareBenchmarkRuntime: unavailable, startBenchmark: unavailable, getBenchmark: unavailable, getBenchmarkReportOwner: unavailable,
    cancelBenchmark: unavailable, getBenchmarkReport: unavailable, exportBenchmarkReport: unavailable,
  }
}
