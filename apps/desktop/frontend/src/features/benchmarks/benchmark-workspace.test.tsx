import { useState } from "react"
import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { EMPTY_CATALOG } from "@/features/catalog/data"
import { EMPTY_REPORTS } from "@/features/reports/data"
import { EMPTY_WORKSPACE, type PlanRunConnection } from "@/features/runs/data"
import { ReportWorkspace } from "@/features/reports/report-workspace"
import { RunWorkspace } from "@/features/runs/execution-workspace"
import { executionClient } from "@/test/jobs-fixture"
import { benchmarkReport, benchmarkRun } from "@/test/benchmark-fixtures"
import { BenchmarkWorkspace } from "./benchmark-workspace"
import { BenchmarkReportBody } from "./benchmark-details"
import { benchmarkPreviewClient } from "./preview-client"
import type { BenchmarkClient } from "./data"

function clientFixture() {
  const preview = benchmarkPreviewClient()
  return { ...preview,
    getBenchmarkCatalog: vi.fn(async () => ({ ...await preview.getBenchmarkCatalog(), runtime: { state: "ready" as const, error_code: "" } })),
    getBenchmarks: vi.fn<BenchmarkClient["getBenchmarks"]>(async () => ({ schema_version: 1, runs: [], next_cursor: "" })),
    getBenchmark: vi.fn(async () => benchmarkRun()),
    getBenchmarkReport: vi.fn(async () => benchmarkReport()),
    startBenchmark: vi.fn<BenchmarkClient["startBenchmark"]>(async () => benchmarkRun()),
    cancelBenchmark: vi.fn(async () => undefined),
    exportBenchmarkReport: vi.fn(async () => ({ filename: "benchmark.json", media_type: "application/json", data_base64: "e30=" })),
  }
}

describe("benchmark workflow", () => {
  it("rejects 17 selected datasets and accepts the 16-dataset submission boundary", async () => {
    const client = clientFixture(), definition = await benchmarkPreviewClient().getBenchmarkCatalog()
    render(<BenchmarkWorkspace client={client} modeControl={null} catalog={EMPTY_CATALOG} connectionsReady
      connection={{ protocol: "openai-chat", model: "test", base_url: "https://api.example.test/v1", api_key: "test-key" }}
      onConnectionChange={vi.fn()} onOpenExecution={vi.fn()} onOpenReport={vi.fn()} />)
    await screen.findByRole("checkbox", { name: "ARC Easy" })
    for (const dataset of definition.datasets.slice(0, 17)) fireEvent.click(screen.getByRole("checkbox", { name: dataset.name }))
    fireEvent.click(screen.getByRole("button", { name: "开始数据测试" }))
    expect(screen.getByText("每次最多选择 16 个数据集。")).toBeVisible()
    expect(client.startBenchmark).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("checkbox", { name: definition.datasets[16].name }))
    fireEvent.click(screen.getByRole("button", { name: "开始数据测试" }))
    await waitFor(() => expect(client.startBenchmark).toHaveBeenCalledTimes(1))
    expect(client.startBenchmark.mock.calls[0][0].dataset_ids).toHaveLength(16)
  })

  it("submits selected datasets with the shared manual connection and retains independent records", async () => {
    const user = userEvent.setup(), client = clientFixture()
    let count = 0
    client.startBenchmark.mockImplementation(async () => benchmarkRun({ id: `0179406750000000000${++count}-00000000-0000-4000-8000-000000000001` }))
    function Harness() {
      const [connection, setConnection] = useState<PlanRunConnection>({ protocol: "openai-chat", model: "local-test", base_url: "https://api.example.test/v1", api_key: "private-test-key" })
      return <BenchmarkWorkspace client={client} modeControl={<div>四种测试模式</div>} catalog={EMPTY_CATALOG} connectionsReady connection={connection} onConnectionChange={setConnection} onOpenExecution={vi.fn()} onOpenReport={vi.fn()} />
    }
    render(<Harness />)
    await user.click(await screen.findByRole("checkbox", { name: "ARC Easy" }))
    await user.click(screen.getByRole("button", { name: "开始数据测试" }))
    await waitFor(() => expect(client.startBenchmark).toHaveBeenCalledTimes(1))
    expect(client.startBenchmark.mock.calls[0][0]).toMatchObject({ dataset_ids: ["arc_easy"], connection: { base_url: "https://api.example.test/v1", api_key: "private-test-key", model: "local-test", protocol: "openai-chat" } })
    await user.click(screen.getByRole("button", { name: "开始数据测试" }))
    await waitFor(() => expect(screen.getAllByRole("button", { name: "执行详情" })).toHaveLength(2))
    expect(JSON.stringify(window.localStorage)).not.toContain("private-test-key")
  })

  it("opens a dataset report in the existing report workspace without reading a functional report", async () => {
    const user = userEvent.setup(), client = clientFixture(), getDetail = vi.fn()
    client.getBenchmarks.mockResolvedValue({ schema_version: 1, runs: [benchmarkRun()], next_cursor: "" })
    render(<ReportWorkspace snapshot={EMPTY_REPORTS} benchmarkClient={client} getDetail={getDetail} exportReport={vi.fn()} saveReportExport={vi.fn()} copyReportPNG={vi.fn()} />)
    await user.click(await screen.findByRole("button", { name: "ARC Easy · 数据集报告" }))
    expect(await screen.findByText("Which planet is closest to the Sun?")).toBeInTheDocument()
    expect(getDetail).not.toHaveBeenCalled()
    expect(client.getBenchmarkReport).toHaveBeenCalledWith(benchmarkRun().id, benchmarkRun().datasets[0].report_id, "")
  })

  it("shows benchmark progress and cancellation in the existing Run workspace", async () => {
    const client = clientFixture(), run = benchmarkRun({ status: "running", finished_at: "" })
    run.datasets[0] = { ...run.datasets[0], status: "running", stage: "running", completed: 0, in_flight: 1, finished_at: "" }
    client.getBenchmark.mockResolvedValue(run)
    client.getBenchmarks.mockResolvedValue({ schema_version: 1, runs: [run], next_cursor: "" })
    render(<RunWorkspace client={{ ...executionClient(), ...client }} snapshot={EMPTY_WORKSPACE} reports={EMPTY_REPORTS} preferredJob={null} preferredRunID={run.id} commandError="" onOpenReport={vi.fn()} />)
    await screen.findByRole("button", { name: "查看 ARC Easy" })
    const details = screen.queryByRole("button", { name: "详情" })
    if (details) fireEvent.click(details)
    const cancel = await screen.findByRole("button", { name: "取消测试" })
    fireEvent.click(cancel)
    await waitFor(() => expect(client.cancelBenchmark).toHaveBeenCalledWith(run.id))
    expect(screen.getAllByText("请求中 1").length).toBeGreaterThan(0)
  })

  it("pages bounded sample evidence and exports a per-dataset JSON report", async () => {
    const user = userEvent.setup(), client = clientFixture(), save = vi.fn(async () => true)
    const first = benchmarkReport(), second = benchmarkReport()
    first.next_cursor = "a".repeat(64); second.samples[0].id = "question-2"; second.samples[0].input = "Second question"
    client.getBenchmarkReport.mockResolvedValueOnce(first).mockResolvedValue(second)
    render(<BenchmarkReportBody runID={first.run_id} result={first.result} client={client} saveExport={save} />)
    await screen.findByText("Which planet is closest to the Sun?")
    await user.click(screen.getByRole("button", { name: "加载更多" }))
    expect(await screen.findByText("Second question")).toBeInTheDocument()
    expect(screen.queryByText("Which planet is closest to the Sun?")).not.toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "导出 JSON" }))
    await waitFor(() => expect(save).toHaveBeenCalledWith("benchmark.json", "application/json", "e30=", "zh-CN"))
  })
})
