import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it } from "vitest"

import type { QuickPerformanceReport } from "./data"
import { QuickPerformanceRequestAnalysis } from "./quick-performance-request-analysis"

describe("QuickPerformanceRequestAnalysis streaming telemetry", () => {
  it("labels legacy TTFT as including reasoning and shows every v3 scalar in the selected request", async () => {
    const user = userEvent.setup()
    render(<QuickPerformanceRequestAnalysis report={report(3)} />)

    expect(screen.getByRole("columnheader", { name: "TTFT（含推理）" })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "查看请求 1 详情" }))
    const detail = screen.getByRole("region", { name: "请求 1 详情" })
    expect(within(detail).getByText("TTFB").nextElementSibling).toHaveTextContent("10 ms")
    expect(within(detail).getByText("TTFT（含推理）").nextElementSibling).toHaveTextContent("20 ms")
    expect(within(detail).getByText("TTFT（可见内容）").nextElementSibling).toHaveTextContent("30 ms")
    expect(within(detail).getByText("TTST（第二语义块）").nextElementSibling).toHaveTextContent("40 ms")
    expect(within(detail).getByText(/Observed ICL.*非 Token ITL/).nextElementSibling).toHaveTextContent("20 ms")
    expect(within(detail).getByText("语义块数").nextElementSibling).toHaveTextContent("3")
  })

  it("does not fabricate v3 request details for older reports", async () => {
    const user = userEvent.setup()
    render(<QuickPerformanceRequestAnalysis report={report(2)} />)
    await user.click(screen.getByRole("button", { name: "查看请求 1 详情" }))

    const detail = screen.getByRole("region", { name: "请求 1 详情" })
    expect(within(detail).queryByText("TTFB")).not.toBeInTheDocument()
    expect(within(detail).queryByText("语义块数")).not.toBeInTheDocument()
  })
})

function report(schemaVersion: 2 | 3): QuickPerformanceReport {
  const metrics: QuickPerformanceReport["metrics"] = {
    completed: 1, succeeded: 1, failed: 0, timed_out: 0, success_rate_percent: 100,
    request_qps: 1, rpm: 60, input_tpm: 1_200, output_tpm: 1_920, total_tpm: 3_120, generation_tps: 32,
    ttft_p50_ms: 20, ttft_p90_ms: 20, ttft_p95_ms: 20, ttft_p99_ms: 20, ttft_average_ms: 20,
    tpot_p50_ms: 1, tpot_p90_ms: 1, tpot_p95_ms: 1, tpot_p99_ms: 1, tpot_average_ms: 1,
    e2e_p50_ms: 60, e2e_p90_ms: 60, e2e_p95_ms: 60, e2e_p99_ms: 60, e2e_average_ms: 60,
    schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 0, schedule_lag_p95_ms: 0,
    schedule_lag_p99_ms: 0, schedule_lag_average_ms: 0,
    prompt_tokens: 20, completion_tokens: 32, cached_tokens: 0, cache_rate_percent: 0,
    ...(schemaVersion === 3 ? {
      ttft_samples: 1,
      ttfb_samples: 1, ttfb_p50_ms: 10, ttfb_p95_ms: 10, ttfb_p99_ms: 10, ttfb_average_ms: 10,
      ttft_any_samples: 1, ttft_any_p50_ms: 20, ttft_any_p95_ms: 20, ttft_any_p99_ms: 20, ttft_any_average_ms: 20,
      ttft_visible_samples: 1, ttft_visible_p50_ms: 30, ttft_visible_p95_ms: 30, ttft_visible_p99_ms: 30, ttft_visible_average_ms: 30,
      ttst_samples: 1, ttst_p50_ms: 40, ttst_p95_ms: 40, ttst_p99_ms: 40, ttst_average_ms: 40,
      observed_icl_samples: 1, observed_icl_p50_ms: 20, observed_icl_p95_ms: 20, observed_icl_p99_ms: 20, observed_icl_average_ms: 20,
      semantic_chunk_count_samples: 1, semantic_chunk_count_p50: 3, semantic_chunk_count_p95: 3,
      semantic_chunk_count_p99: 3, semantic_chunk_count_average: 3,
    } : {}),
  }
  return {
    schema_version: schemaVersion,
    archived: false,
    archive_status: "not_attempted",
    model_id: "gpt-test",
    success: true,
    address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: {
      load_mode: "fixed_concurrency", request_count: 1, duration_ms: 0, concurrency: 1,
      timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
    },
    progress: {
      phase: "completed", planned: 1, launched: 1, completed: 1, in_flight: 0,
      peak_in_flight: 1, succeeded: 1, failed: 0, rejected: 0,
      send_duration_ms: 60, drain_duration_ms: 0, total_duration_ms: 60,
    },
    metrics,
    samples: [{
      request_index: 0, scheduled_offset_ms: 0, started_offset_ms: 0, finished_offset_ms: 60,
      schedule_lag_ms: 0, e2e_ms: 60, ttft_ms: 20, tpot_ms: 1,
      ...(schemaVersion === 3 ? {
        ttfb_ms: 10, ttft_any_ms: 20, ttft_visible_ms: 30, ttst_ms: 40,
        observed_icl_ms: 20, semantic_chunk_count: 3,
      } : {}),
      http_status: 200, success: true, timed_out: false,
      prompt_tokens: 20, completion_tokens: 32, cached_tokens: 0,
    }],
    failures: [],
  }
}
