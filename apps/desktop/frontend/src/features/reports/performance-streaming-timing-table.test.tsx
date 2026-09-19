import { render, screen, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { QuickPerformanceMetrics } from "@/features/quick-test/data"
import { PerformanceStreamingTimingTable } from "./performance-streaming-timing-table"

describe("PerformanceStreamingTimingTable", () => {
  it("shows the six compact v3 cohorts and renders an empty cohort entirely as dashes", () => {
    render(<PerformanceStreamingTimingTable metrics={streamingMetrics()} />)

    const table = screen.getByRole("table", { name: "流式时序统计" })
    expect(within(table).getByRole("row", { name: /TTFB/ })).toHaveTextContent(/15 ms.*15 ms.*20 ms.*20 ms.*2/)
    expect(within(table).getByRole("row", { name: /TTFT（含推理）/ })).toHaveTextContent("30 ms")
    expect(within(table).getByRole("row", { name: /TTFT（可见内容）/ })).toBeInTheDocument()
    expect(within(table).getByRole("row", { name: /TTST/ })).toHaveTextContent("50 ms")
    expect(within(table).getByRole("row", { name: /Observed ICL/ })).toHaveTextContent("语义块间隔，非 Token ITL")
    expect(within(table).getByRole("row", { name: /语义块数/ })).toHaveTextContent("2.5")

    const empty = within(table).getByRole("row", { name: /TTFT（可见内容）/ })
    expect(within(empty).getAllByText("—")).toHaveLength(5)
  })

})

function streamingMetrics(): QuickPerformanceMetrics {
  return {
    completed: 2, succeeded: 2, failed: 0, timed_out: 0, success_rate_percent: 100,
    request_qps: 2, rpm: 120, input_tpm: 0, output_tpm: 0, total_tpm: 0, generation_tps: 0,
    ttft_p50_ms: 30, ttft_p90_ms: 38, ttft_p95_ms: 39, ttft_p99_ms: 39.8, ttft_average_ms: 30,
    ttft_samples: 2,
    ttfb_samples: 2, ttfb_p50_ms: 15, ttfb_p95_ms: 19.5, ttfb_p99_ms: 19.9, ttfb_average_ms: 15,
    ttft_any_samples: 2, ttft_any_p50_ms: 30, ttft_any_p95_ms: 39, ttft_any_p99_ms: 39.8, ttft_any_average_ms: 30,
    ttft_visible_samples: 0, ttft_visible_p50_ms: 0, ttft_visible_p95_ms: 0, ttft_visible_p99_ms: 0, ttft_visible_average_ms: 0,
    ttst_samples: 2, ttst_p50_ms: 50, ttst_p95_ms: 59, ttst_p99_ms: 59.8, ttst_average_ms: 50,
    observed_icl_samples: 2, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
    semantic_chunk_count_samples: 2, semantic_chunk_count_p50: 2.5, semantic_chunk_count_p95: 2.95,
    semantic_chunk_count_p99: 2.99, semantic_chunk_count_average: 2.5,
    tpot_p50_ms: 0, tpot_p90_ms: 0, tpot_p95_ms: 0, tpot_p99_ms: 0, tpot_average_ms: 0,
    e2e_p50_ms: 0, e2e_p90_ms: 0, e2e_p95_ms: 0, e2e_p99_ms: 0, e2e_average_ms: 0,
    schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 0, schedule_lag_p95_ms: 0,
    schedule_lag_p99_ms: 0, schedule_lag_average_ms: 0,
    prompt_tokens: 0, completion_tokens: 0, cached_tokens: 0, cache_rate_percent: 0,
  }
}
