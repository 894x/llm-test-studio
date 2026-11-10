import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, it, vi } from "vitest"
import { benchmarkPreviewClient } from "./preview-client"
import { DatasetWorkspace } from "./dataset-workspace"

it("shows the expanded catalog, starts downloads, and opens the selected dataset for testing", async () => {
  const user = userEvent.setup(), preview = benchmarkPreviewClient()
  const inventory = await preview.getBenchmarkDatasets()
  inventory.runtime.state = "ready"
  const getBenchmarkDatasets = vi.fn(async () => structuredClone(inventory))
  const manageBenchmarkDataset = vi.fn(async () => {
    inventory.datasets[0].download = { ...inventory.datasets[0].download, state: "ready", bytes: 4096, files: 2, samples: 2376 }
    return inventory.datasets[0].download
  })
  const onTest = vi.fn()
  render(<DatasetWorkspace client={{ ...preview, getBenchmarkDatasets, manageBenchmarkDataset }} onTest={onTest} />)
  await user.click(await screen.findByRole("button", { name: "ARC Easy" }))
  expect(screen.getByText("共 24 个评测数据集")).toBeVisible()
  await user.click(screen.getByRole("button", { name: "自动下载" }))
  await waitFor(() => expect(manageBenchmarkDataset).toHaveBeenCalledWith("arc_easy", "download"))
  await waitFor(() => expect(screen.getAllByText("已下载").length).toBeGreaterThan(0))
  await user.type(screen.getByRole("textbox", { name: "搜索数据集" }), "HellaSwag")
  await user.click(screen.getByRole("button", { name: "用于数据测试" }))
  expect(onTest).toHaveBeenCalledWith("hellaswag")
})

it("requires a clear-cache decision and preserves the cache when dismissed", async () => {
  const user = userEvent.setup(), preview = benchmarkPreviewClient()
  const inventory = await preview.getBenchmarkDatasets()
  inventory.runtime.state = "ready"
  inventory.datasets[0].download = { ...inventory.datasets[0].download, state: "ready", bytes: 4096, files: 2, samples: 2376 }
  const manageBenchmarkDataset = vi.fn(async () => inventory.datasets[0].download)
  render(<DatasetWorkspace client={{ ...preview, getBenchmarkDatasets: async () => inventory, manageBenchmarkDataset }} onTest={vi.fn()} />)
  await user.click(await screen.findByRole("button", { name: "ARC Easy" }))
  await user.click(screen.getByRole("button", { name: "清理本地缓存" }))
  expect(screen.getByRole("alertdialog")).toHaveTextContent("历史运行和报告会保留")
  await user.click(screen.getByRole("button", { name: "保留缓存" }))
  expect(manageBenchmarkDataset).not.toHaveBeenCalled()
  await user.click(screen.getByRole("button", { name: "校验缓存" }))
  await waitFor(() => expect(manageBenchmarkDataset).toHaveBeenCalledWith("arc_easy", "verify"))
})
