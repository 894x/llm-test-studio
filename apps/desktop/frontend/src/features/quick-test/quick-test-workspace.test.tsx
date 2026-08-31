import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { FIXTURE_CATALOG } from "@/features/runs/fixtures"
import { DesktopClientError } from "@/app/desktop-client"

import { QuickTestWorkspace } from "./quick-test-workspace"
import { updateQuickTestForm, type QuickPerformanceProgress, type QuickPerformanceReport, type QuickTestResult } from "./data"

describe("QuickTestWorkspace", () => {
  it("uses a selected catalog channel without exposing its stored API key", async () => {
    const user = userEvent.setup()
    const runQuickTest = vi.fn(async () => ({
      schema_version: 1 as const,
      success: true,
      address_mode: "base_url" as const,
      base_url: "https://api.example.test/v1",
      endpoint: "https://api.example.test/v1/chat/completions",
      http_status: 200,
      e2e_ms: 100,
      prompt_tokens: 1,
      completion_tokens: 1,
      cached_tokens: 0,
    }))

    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        channelCandidates={[{
          id: "10000000-0000-4000-8000-000000000001",
          name: "OpenAI 主渠道",
          baseUrl: "https://api.example.test/v1",
        }]}
        runQuickTest={runQuickTest}
        runQuickPerformanceTest={vi.fn()}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    await user.click(screen.getByRole("combobox", { name: "从渠道填充" }))
    await user.click(screen.getByRole("option", { name: "OpenAI 主渠道" }))

    expect(screen.getByLabelText("接口地址")).toHaveValue("https://api.example.test/v1")
    expect(screen.getByLabelText("API Key")).toBeDisabled()
    expect(screen.getByLabelText("API Key")).toHaveAttribute("placeholder", "已使用 OpenAI 主渠道 的保存凭据")

    await user.type(screen.getByLabelText("模型 ID"), "gpt-test")
    await user.click(screen.getByRole("button", { name: "发送测试" }))

    await waitFor(() => expect(runQuickTest).toHaveBeenCalledWith({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "",
      channel_id: "10000000-0000-4000-8000-000000000001",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    }))
    const result = screen.getByRole("region", { name: "测试结果" })
    expect(result).toHaveTextContent("当前连接来自已保存渠道")
    expect(within(result).queryByRole("button", { name: "保存为模型与渠道" })).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent("sk-")
  })

  it("removes the stored channel credential source when its copied address is edited", () => {
    expect(updateQuickTestForm({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "",
      channel_id: "10000000-0000-4000-8000-000000000001",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    }, "url", "https://manual.example.test/v1")).toEqual({
      address_mode: "base_url",
      url: "https://manual.example.test/v1",
      api_key: "",
      model_id: "gpt-test",
      prompt: "Reply with OK only.",
      timeout_ms: 30_000,
    })
  })

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
        runQuickPerformanceTest={vi.fn()}
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
    expect(within(result).getByRole("button", { name: "快速性能测试" })).toBeInTheDocument()
  })

  it("runs a configurable performance test from the immutable successful connection and renders its report", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn(async () => successfulPerformanceReport())
    const onPerformanceArchived = vi.fn(async () => {})
    const onOpenReport = vi.fn()
    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={successfulQuickTest}
        runQuickPerformanceTest={runQuickPerformanceTest}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
        onPerformanceArchived={onPerformanceArchived}
        onOpenReport={onOpenReport}
      />,
    )
    await fillAndRun(user)
    const result = await screen.findByRole("region", { name: "测试结果" })
    await user.click(within(result).getByRole("button", { name: "快速性能测试" }))

    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    expect(within(dialog).getByLabelText("请求数")).toHaveValue(10)
    expect(within(dialog).getByLabelText("持续时间（秒）")).toHaveValue(0)
    expect(within(dialog).getByLabelText("并发数")).toHaveValue(1)
    expect(within(dialog).getByLabelText("单请求超时（秒）")).toHaveValue(60)
    expect(within(dialog).getByLabelText("近似输入 Token")).toHaveValue(100)
    expect(within(dialog).getByLabelText("最大输出 Token")).toHaveValue(100)
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "4")
    await replaceNumber(user, within(dialog).getByLabelText("持续时间（秒）"), "1")
    await replaceNumber(user, within(dialog).getByLabelText("并发数"), "2")
    await replaceNumber(user, within(dialog).getByLabelText("单请求超时（秒）"), "30")
    await replaceNumber(user, within(dialog).getByLabelText("近似输入 Token"), "20")
    await replaceNumber(user, within(dialog).getByLabelText("最大输出 Token"), "32")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    await waitFor(() => expect(runQuickPerformanceTest).toHaveBeenCalledWith({
      address_mode: "base_url",
      url: "https://api.example.test/v1",
      api_key: "sk-private-value",
      model_id: "gpt-new",
      request_count: 4,
      duration_ms: 1_000,
      concurrency: 2,
      timeout_ms: 30_000,
      input_tokens: 20,
      output_tokens: 32,
    }, expect.any(Function)))
    const report = await within(dialog).findByRole("region", { name: "性能报告" })
    expect(report).toHaveTextContent("完成 / 计划")
    expect(report).toHaveTextContent("失败")
    expect(report).toHaveTextContent("100%")
    expect(report).toHaveTextContent("12.5 req/s")
    expect(report).toHaveTextContent("750 RPM")
    expect(report).toHaveTextContent("39,000 TPM")
    const latency = within(report).getByRole("table", { name: "延迟分布统计" })
    expect(within(latency).getByRole("row", { name: /TTFT/ })).toHaveTextContent(/32\s*30\s*40\s*42\s*44/)
    expect(within(latency).getByRole("row", { name: /TPOT/ })).toHaveTextContent(/4\.5\s*4\s*5\s*6\s*7/)
    expect(within(latency).getByRole("row", { name: /E2E/ })).toHaveTextContent(/65\s*60\s*75\s*80\s*84/)
    expect(within(latency).getByRole("row", { name: /客户端排队（本地调度延迟）/ })).toHaveTextContent(/0\.5\s*0\s*1\.8\s*2\s*2\.8/)
    expect(within(report).getByRole("figure", { name: "TTFT 分布图" })).toBeInTheDocument()
    expect(within(report).getByRole("figure", { name: "TPOT 时间曲线" })).toBeInTheDocument()
    expect(within(report).getByRole("figure", { name: "E2E 时间曲线" })).toBeInTheDocument()
    expect(onPerformanceArchived).toHaveBeenCalledWith("77777777-7777-4777-8777-777777777771")
    await user.click(within(report).getByRole("button", { name: "查看正式报告" }))
    expect(onOpenReport).toHaveBeenCalledWith("77777777-7777-4777-8777-777777777771")
    expect(report).not.toHaveTextContent("sk-private-value")
  })

  it("shows authoritative progress while a performance test is running", async () => {
    const user = userEvent.setup()
    let resolvePerformance!: (report: QuickPerformanceReport) => void
    const runQuickPerformanceTest = vi.fn((_command, onProgress?: (progress: QuickPerformanceProgress) => void) => {
      onProgress?.({
        phase: "sending", planned: 10, launched: 5, completed: 3, in_flight: 2,
        peak_in_flight: 2, succeeded: 2, failed: 1, rejected: 0,
        send_duration_ms: 120, drain_duration_ms: 0, total_duration_ms: 120,
      })
      return new Promise<QuickPerformanceReport>((resolve) => { resolvePerformance = resolve })
    })
    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={successfulQuickTest}
        runQuickPerformanceTest={runQuickPerformanceTest}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )
    await fillAndRun(user)
    await user.click(await screen.findByRole("button", { name: "快速性能测试" }))
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    const status = await within(dialog).findByRole("status", { name: "性能测试进度" })
    expect(status).toHaveTextContent("发送中")
    expect(status).toHaveTextContent(/完成\s*3 \/ 10/)
    expect(status).toHaveTextContent(/成功\s*2/)
    expect(status).toHaveTextContent(/失败\s*1/)
    expect(status).toHaveTextContent(/在途\s*2/)
    expect(within(status).getByRole("progressbar", { name: "请求完成进度" })).toHaveAttribute("aria-valuenow", "30")

    resolvePerformance(successfulPerformanceReport())
    await within(dialog).findByRole("region", { name: "性能报告" })
  })

  it("requires a request-count or duration target before starting performance testing", async () => {
    const user = userEvent.setup()
    const runQuickPerformanceTest = vi.fn()
    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={successfulQuickTest}
        runQuickPerformanceTest={runQuickPerformanceTest}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )
    await fillAndRun(user)
    await user.click(await screen.findByRole("button", { name: "快速性能测试" }))
    const dialog = screen.getByRole("dialog", { name: "快速性能测试" })
    await replaceNumber(user, within(dialog).getByLabelText("请求数"), "0")
    await user.click(within(dialog).getByRole("button", { name: "开始性能测试" }))

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("请求数和持续时间至少填写一项")
    expect(runQuickPerformanceTest).not.toHaveBeenCalled()
  })

  it("blocks duplicate submissions and maps a stable failure classification", async () => {
    const user = userEvent.setup()
    let resolveTest!: (value: QuickTestResult) => void
    const runQuickTest = vi.fn(() => new Promise<QuickTestResult>((resolve) => { resolveTest = resolve }))

    render(
      <QuickTestWorkspace
        modelCandidates={[]}
        runQuickTest={runQuickTest}
        runQuickPerformanceTest={vi.fn()}
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
        runQuickPerformanceTest={vi.fn()}
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
        runQuickPerformanceTest={vi.fn()}
        saveQuickTestConnection={vi.fn()}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    const modelID = screen.getByLabelText("模型 ID")
    expect(modelID).not.toHaveAttribute("list")
    expect(screen.getByRole("button", { name: "显示模型候选" })).toBeInTheDocument()
    await user.click(modelID)
    expect(screen.getByRole("listbox")).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "gpt-5.2" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "gpt-4.1-mini" })).toBeInTheDocument()

    await user.click(screen.getByRole("option", { name: "gpt-5.2" }))
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
        runQuickPerformanceTest={vi.fn()}
        saveQuickTestConnection={saveQuickTestConnection}
        refreshCatalog={vi.fn()}
        onCatalogUpdated={vi.fn()}
        onOpenCatalog={vi.fn()}
      />,
    )

    await user.type(screen.getByLabelText("接口地址"), "https://api.example.test/v1")
    await user.type(screen.getByLabelText("API Key"), "sk-private-value")
    await user.type(screen.getByLabelText("模型 ID"), existingModel.name)
    await user.keyboard("{Escape}")
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
        runQuickPerformanceTest={vi.fn()}
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
        runQuickPerformanceTest={vi.fn()}
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
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "发送测试" }))
    await user.clear(url)
    await user.type(url, "https://untested.example/v1")
    await user.clear(apiKey)
    await user.type(apiKey, "sk-untested")
    await user.clear(modelID)
    await user.type(modelID, "untested-model")
    await user.keyboard("{Escape}")
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
        runQuickPerformanceTest={vi.fn()}
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
        runQuickPerformanceTest={vi.fn()}
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
  await user.keyboard("{Escape}")
  await user.click(screen.getByRole("button", { name: "发送测试" }))
}

async function replaceNumber(user: ReturnType<typeof userEvent.setup>, input: HTMLElement, value: string) {
  await user.clear(input)
  await user.type(input, value)
}

function successfulPerformanceReport(): QuickPerformanceReport {
  return {
    schema_version: 1,
    report_id: "77777777-7777-4777-8777-777777777771",
    generated_at: "2026-08-31T14:30:00Z",
    archived: true,
    archive_status: "archived",
    model_id: "gpt-new",
    success: true,
    address_mode: "base_url",
    base_url: "https://api.example.test/v1",
    endpoint: "https://api.example.test/v1/chat/completions",
    profile: {
      request_count: 4, duration_ms: 1_000, concurrency: 2,
      timeout_ms: 30_000, input_tokens: 20, output_tokens: 32,
    },
    progress: {
      phase: "completed", planned: 4, launched: 4, completed: 4,
      in_flight: 0, peak_in_flight: 2, succeeded: 4, failed: 0, rejected: 0,
      send_duration_ms: 300, drain_duration_ms: 20, total_duration_ms: 320,
    },
    metrics: {
      completed: 4, succeeded: 4, failed: 0, timed_out: 0,
      success_rate_percent: 100, request_qps: 12.5, rpm: 750,
      input_tpm: 15_000, output_tpm: 24_000, total_tpm: 39_000,
      generation_tps: 400,
      ttft_p50_ms: 30, ttft_p90_ms: 40, ttft_p95_ms: 42, ttft_p99_ms: 44, ttft_average_ms: 32,
      tpot_p50_ms: 4, tpot_p90_ms: 5, tpot_p95_ms: 6, tpot_p99_ms: 7, tpot_average_ms: 4.5,
      e2e_p50_ms: 60, e2e_p90_ms: 75, e2e_p95_ms: 80, e2e_p99_ms: 84, e2e_average_ms: 65,
      schedule_lag_p50_ms: 0, schedule_lag_p90_ms: 1.8, schedule_lag_p95_ms: 2,
      schedule_lag_p99_ms: 2.8, schedule_lag_average_ms: 0.5,
      prompt_tokens: 80, completion_tokens: 128, cached_tokens: 20, cache_rate_percent: 25,
    },
    samples: [
      { request_index: 0, scheduled_offset_ms: 0, started_offset_ms: 0, finished_offset_ms: 60, schedule_lag_ms: 0, e2e_ms: 60, ttft_ms: 30, tpot_ms: 4, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 },
      { request_index: 1, scheduled_offset_ms: 0, started_offset_ms: 1, finished_offset_ms: 75, schedule_lag_ms: 1, e2e_ms: 74, ttft_ms: 40, tpot_ms: 5, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 },
      { request_index: 2, scheduled_offset_ms: 0, started_offset_ms: 2, finished_offset_ms: 84, schedule_lag_ms: 2, e2e_ms: 82, ttft_ms: 44, tpot_ms: 7, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 },
      { request_index: 3, scheduled_offset_ms: 0, started_offset_ms: 2, finished_offset_ms: 90, schedule_lag_ms: 2, e2e_ms: 88, ttft_ms: 42, tpot_ms: 6, http_status: 200, success: true, timed_out: false, prompt_tokens: 20, completion_tokens: 32, cached_tokens: 5 },
    ],
    failures: [],
  }
}
