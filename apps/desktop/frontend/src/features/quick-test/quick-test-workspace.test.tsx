import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { DesktopClientError } from "@/app/desktop-client"

import { QuickTestWorkspace } from "./quick-test-workspace"
import type { QuickTestResult } from "./data"

describe("QuickTestWorkspace", () => {
  it("runs a zero-configuration connectivity test without exposing secrets or payloads", async () => {
    const user = userEvent.setup()
    const runQuickTest = vi.fn(async () => ({
      schema_version: 1 as const,
      success: true,
      address_mode: "base_url" as const,
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      http_status: 200,
      e2e_ms: 318,
      prompt_tokens: 8,
      completion_tokens: 1,
      cached_tokens: 0,
    }))

    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={runQuickTest}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    expect(screen.getByRole("heading", { name: "快速测试" })).toBeInTheDocument()
    expect(screen.getByLabelText("测试消息")).toHaveValue("Reply with OK only.")
    expect(screen.getByLabelText("API Key")).toHaveAttribute("type", "password")

    await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1")
    await user.type(screen.getByLabelText("API Key"), "sk-private-value")
    await user.type(screen.getByLabelText("模型 ID"), "gpt-test")
    await user.click(screen.getByRole("button", { name: "发送测试" }))

    await waitFor(() => expect(runQuickTest).toHaveBeenCalledWith({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    }))

    const result = screen.getByRole("region", { name: "测试结果" })
    expect(result).toHaveTextContent("连接成功")
    expect(result).toHaveTextContent("200")
    expect(result).toHaveTextContent("318 ms")
    expect(result).toHaveTextContent("8 / 1 / 0")
    expect(result).toHaveTextContent("https://api.example.test/v1/chat/completions")
    expect(result).not.toHaveTextContent("sk-private-value")
    expect(result).not.toHaveTextContent("Reply with OK only.")
    expect(within(result).getByRole("button", { name: "保存为模型与渠道" })).toBeInTheDocument()
  })

  it("blocks duplicate submissions and maps a stable failure classification", async () => {
    const user = userEvent.setup()
    let resolveTest!: (value: QuickTestResult) => void
    const runQuickTest = vi.fn(() => new Promise<QuickTestResult>((resolve) => { resolveTest = resolve }))

    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={runQuickTest}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )
    await user.click(screen.getByRole("radio", { name: "完整 URL" }))
    await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1/chat/completions")
    await user.type(screen.getByLabelText("API Key"), "invalid-key")
    await user.type(screen.getByLabelText("模型 ID"), "gpt-test")
    const submit = screen.getByRole("button", { name: "发送测试" })
    await user.click(submit)

    expect(submit).toBeDisabled()
    expect(runQuickTest).toHaveBeenCalledTimes(1)
    resolveTest({
      schema_version: 1,
      success: false,
      address_mode: "full_url",
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      http_status: 401,
      e2e_ms: 120,
      prompt_tokens: 0,
      completion_tokens: 0,
      cached_tokens: 0,
      error_code: "authentication_failed",
    })

    const result = await screen.findByRole("region", { name: "测试结果" })
    expect(result).toHaveTextContent("鉴权失败")
    expect(result).not.toHaveTextContent("invalid-key")
  })

  it("saves an explicitly named connection from the successful test result", async () => {
    const user = userEvent.setup()
    const saveQuickTestConnection = vi.fn(async () => structuredClone(FIXTURE_CATALOG))
    const onCatalogUpdated = vi.fn()
    const onOpenCatalog = vi.fn()

    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={vi.fn(async () => ({
          schema_version: 1 as const,
          success: true,
          address_mode: "base_url" as const,
          base_url: "https://api.example.test/v1",
          endpoint: "https://api.example.test/v1/chat/completions",
          http_status: 200,
          e2e_ms: 88,
          prompt_tokens: 8,
          completion_tokens: 1,
          cached_tokens: 0,
        }))}
        saveQuickTestConnection={saveQuickTestConnection}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={onCatalogUpdated}
        onOpenCatalog={onOpenCatalog}
      />,
    )
    await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1")
    await user.type(screen.getByLabelText("API Key"), "sk-private-value")
    await user.type(screen.getByLabelText("模型 ID"), "gpt-test")
    await user.click(screen.getByRole("button", { name: "发送测试" }))
    await user.click(await screen.findByRole("button", { name: "保存为模型与渠道" }))

    expect(screen.getByRole("dialog", { name: "保存连接" })).toBeInTheDocument()
    const modelName = screen.getByLabelText("模型名称")
    const channelName = screen.getByLabelText("渠道名称")
    expect(modelName).toHaveValue("gpt-test")
    await user.clear(channelName)
    await user.type(channelName, "测试渠道")
    await user.click(screen.getByRole("button", { name: "确认保存" }))

    await waitFor(() => expect(saveQuickTestConnection).toHaveBeenCalledWith({
      base_url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-test",
      model_name: "gpt-test",
      channel_name: "测试渠道",
    }))
    expect(onCatalogUpdated).toHaveBeenCalledWith(FIXTURE_CATALOG)
    expect(await screen.findByRole("button", { name: "打开模型与渠道" })).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "打开模型与渠道" }))
    expect(onOpenCatalog).toHaveBeenCalledTimes(1)
  })

  it("offers configured OpenAI model names while retaining manual model IDs", async () => {
    const user = userEvent.setup()
    render(
      <QuickTestWorkspace
        modelCandidates={[
          { id: "22222222-2222-4222-8222-222222222221", name: "gpt-5.2" },
          { id: "22222222-2222-4222-8222-222222222222", name: "gpt-4.1-mini" },
        ]}
        runQuickTest={vi.fn()}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    const modelID = screen.getByLabelText("模型 ID")
    expect(modelID).toHaveAttribute("list", "quick-test-model-options")
    expect(document.querySelector('option[value="gpt-5.2"]')).not.toBeNull()
    expect(document.querySelector('option[value="gpt-4.1-mini"]')).not.toBeNull()

    await user.type(modelID, "gpt-5.2")
    expect(modelID).toHaveValue("gpt-5.2")
    await user.clear(modelID)
    await user.type(modelID, "my-private-model-id")
    expect(modelID).toHaveValue("my-private-model-id")
  })

  it("reuses a uniquely matched catalog model when saving the tested connection", async () => {
    const user = userEvent.setup()
    const existingModel = FIXTURE_CATALOG.models[0]
    const saveQuickTestConnection = vi.fn(async () => structuredClone(FIXTURE_CATALOG))
    render(
      <QuickTestWorkspace
        modelCandidates={[{ id: existingModel.id, name: existingModel.name }]}
        runQuickTest={vi.fn(async () => ({
          schema_version: 1 as const,
          success: true,
          address_mode: "base_url" as const,
          base_url: "https://api.example.test/v1",
          endpoint: "https://api.example.test/v1/chat/completions",
          http_status: 200,
          e2e_ms: 88,
          prompt_tokens: 8,
          completion_tokens: 1,
          cached_tokens: 0,
        }))}
        saveQuickTestConnection={saveQuickTestConnection}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1")
    await user.type(screen.getByLabelText("API Key"), "sk-private-value")
    await user.type(screen.getByLabelText("模型 ID"), existingModel.name)
    await user.click(screen.getByRole("button", { name: "发送测试" }))
    await user.click(await screen.findByRole("button", { name: "保存为模型与渠道" }))

    const dialog = screen.getByRole("dialog", { name: "保存连接" })
    expect(dialog).toHaveTextContent(`将复用目录模型 ${existingModel.name}`)
    expect(within(dialog).getByLabelText("模型名称")).toBeDisabled()
    const channelName = within(dialog).getByLabelText("渠道名称")
    await user.clear(channelName)
    await user.type(channelName, "复用模型渠道")
    await user.click(within(dialog).getByRole("button", { name: "确认保存" }))

    await waitFor(() => expect(saveQuickTestConnection).toHaveBeenCalledWith({
      base_url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: existingModel.name,
      model_name: existingModel.name,
      channel_name: "复用模型渠道",
      existing_model_id: existingModel.id,
    }))
  })

  it("does not infer model identity from an ambiguous duplicate name", async () => {
    const user = userEvent.setup()
    render(
      <QuickTestWorkspace
        modelCandidates={[
          { id: "22222222-2222-4222-8222-222222222231", name: "shared-name" },
          { id: "22222222-2222-4222-8222-222222222232", name: "shared-name" },
        ]}
        runQuickTest={vi.fn()}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    await user.type(screen.getByLabelText("模型 ID"), "shared-name")
    expect(screen.getByText("目录中存在多个同名模型，无法自动复用；请手动输入唯一的上游模型 ID。")).toBeInTheDocument()
  })

  it("saves only the immutable command and model identity that produced the result", async () => {
    const user = userEvent.setup()
    const existingModel = FIXTURE_CATALOG.models[0]
    let resolveTest!: (result: QuickTestResult) => void
    const saveQuickTestConnection = vi.fn(async () => structuredClone(FIXTURE_CATALOG))
    render(
      <QuickTestWorkspace
        modelCandidates={[{ id: existingModel.id, name: existingModel.name }]}
        runQuickTest={vi.fn(() => new Promise<QuickTestResult>((resolve) => { resolveTest = resolve }))}
        saveQuickTestConnection={saveQuickTestConnection}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    const url = screen.getByLabelText("接口地址")
    const apiKey = screen.getByLabelText("API Key")
    const modelID = screen.getByLabelText("模型 ID")
    await user.type(url, "https://tested.example/v1")
    await user.type(apiKey, "sk-tested")
    await user.type(modelID, existingModel.name)
    await user.click(screen.getByRole("button", { name: "发送测试" }))
    await user.clear(url)
    await user.type(url, "https://untested.example/v1")
    await user.clear(apiKey)
    await user.type(apiKey, "sk-untested")
    await user.clear(modelID)
    await user.type(modelID, "untested-model")
    resolveTest({
      schema_version: 1, success: true, address_mode: "base_url",
      base_url: "https://tested.example/v1",
      endpoint: "https://tested.example/v1/chat/completions",
      http_status: 200, e2e_ms: 80, prompt_tokens: 8,
      completion_tokens: 1, cached_tokens: 0,
    })

    await user.click(await screen.findByRole("button", { name: "保存为模型与渠道" }))
    const dialog = screen.getByRole("dialog", { name: "保存连接" })
    expect(dialog).toHaveTextContent(`将复用目录模型 ${existingModel.name}`)
    const channelName = within(dialog).getByLabelText("渠道名称")
    await user.clear(channelName)
    await user.type(channelName, "冻结快照渠道")
    await user.click(within(dialog).getByRole("button", { name: "确认保存" }))

    await waitFor(() => expect(saveQuickTestConnection).toHaveBeenCalledWith({
      base_url: "https://tested.example/v1",
      api_key: "sk-tested",
      model_id: existingModel.name,
      model_name: existingModel.name,
      channel_name: "冻结快照渠道",
      existing_model_id: existingModel.id,
    }))
  })

  it("refreshes authoritative catalog only after a partial save", async () => {
    const user = userEvent.setup()
    const refreshedCatalog = structuredClone(FIXTURE_CATALOG)
    refreshedCatalog.channels[0].name = "部分保存后的权威渠道"
    const refreshCatalog = vi.fn(async () => refreshedCatalog)
    const onCatalogUpdated = vi.fn()
    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={successfulQuickTest}
        saveQuickTestConnection={vi.fn(async () => { throw new DesktopClientError("quick_test_save_partial") })}
        refreshCatalog={refreshCatalog}
        onCatalogUpdated={onCatalogUpdated}
        onOpenCatalog={vi.fn()}
      />,
    )
    await fillAndRun(user)
    await user.click(await screen.findByRole("button", { name: "保存为模型与渠道" }))
    await user.click(screen.getByRole("button", { name: "确认保存" }))

    await waitFor(() => expect(refreshCatalog).toHaveBeenCalledTimes(1))
    expect(onCatalogUpdated).toHaveBeenCalledWith(refreshedCatalog)
    expect(screen.getByRole("button", { name: "打开模型与渠道" })).toBeInTheDocument()
  })

  it("does not refresh catalog or show partial guidance for an ordinary save failure", async () => {
    const user = userEvent.setup()
    const refreshCatalog = vi.fn()
    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={successfulQuickTest}
        saveQuickTestConnection={vi.fn(async () => { throw new DesktopClientError("catalog_revision_conflict") })}
        refreshCatalog={refreshCatalog}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )
    await fillAndRun(user)
    await user.click(await screen.findByRole("button", { name: "保存为模型与渠道" }))
    await user.click(screen.getByRole("button", { name: "确认保存" }))

    expect(await screen.findByRole("alert")).toHaveTextContent("对象版本已变化或仍被引用")
    expect(refreshCatalog).not.toHaveBeenCalled()
    expect(screen.queryByRole("button", { name: "打开模型与渠道" })).not.toBeInTheDocument()
  })
})

const successfulQuickTest = vi.fn(async () => ({
  schema_version: 1 as const,
  success: true,
  address_mode: "base_url" as const,
  base_url: "https://api.example.test/v1",
  endpoint: "https://api.example.test/v1/chat/completions",
  http_status: 200,
  e2e_ms: 88,
  prompt_tokens: 8,
  completion_tokens: 1,
  cached_tokens: 0,
}))

async function fillAndRun(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1")
  await user.type(screen.getByLabelText("API Key"), "sk-private-value")
  await user.type(screen.getByLabelText("模型 ID"), "gpt-new")
  await user.click(screen.getByRole("button", { name: "发送测试" }))
}
