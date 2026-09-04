import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { EMPTY_CATALOG, type CatalogSnapshot } from "@/features/catalog/data"

import { NewRunSheet } from "./run-workspace"

describe("NewRunSheet MiniMax video confirmation", () => {
  it("requires an explicit MiniMax billing acknowledgement", async () => {
    const user = userEvent.setup()
    const caseID = "44444444-4444-4444-8444-444444444448"
    const modelID = "22222222-2222-4222-8222-222222222228"
    const channelID = "33333333-3333-4333-8333-333333333338"
    const planID = "11111111-1111-4111-8111-111111111118"
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      models: [{ id: modelID, revision: 1, name: "MiniMax H3", protocol: "minimax-video", capabilities: ["video"] }],
      channels: [{ id: channelID, revision: 1, name: "MiniMax", base_url: "https://api.minimax.cn", protocol: "minimax-video", enabled: true, credential_configured: true, model_count: 1 }],
      channel_models: [{ id: "77777777-7777-4777-8777-777777777778", revision: 1, channel_id: channelID, model_id: modelID, upstream_model_name: "MiniMax-H3" }],
      test_cases: [{
        id: caseID, revision: 1, key: "h3.t2v.min_duration", name: "MiniMax H3 最小时长", dimension: "compatibility",
        protocol: "minimax-video", model_targets: ["MiniMax-H3"], enabled: true, default: false, severity: "critical", execution_mode: "automatic",
        definition_schema_version: 2, type: "legacy.apiaudit", type_version: 1, spec: { kind: "minimax_video_task_success" },
      }],
      plans: [{
        id: planID, revision: 1, name: "MiniMax H3 边界", model_count: 1, channel_count: 1, case_count: 1,
        load_mode: "single", concurrency: 1, request_count: 1, rate_per_second: 0, duration_ms: 0, request_timeout_ms: 600000,
        model_ids: [modelID], channel_ids: [channelID], cases: [{ case_id: caseID, revision: 1 }], sla_thresholds: {},
      }],
    }
    const onStartRun = vi.fn(async () => undefined)

    render(<NewRunSheet
      plans={[{ id: planID, name: "MiniMax H3 边界", description: "版本边界", caseCount: 1, runCount: 0 }]}
      catalog={catalog}
      commandPending={false}
      onStartRun={onStartRun}
    />)

    await user.click(screen.getByRole("button", { name: "新建运行" }))
    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    expect(within(dialog).getByText(/MiniMax 视频生成会产生费用/)).toBeInTheDocument()
    expect(within(dialog).getByRole("button", { name: "开始付费运行" })).toBeDisabled()

    await user.click(within(dialog).getByRole("checkbox", { name: "我确认本次 MiniMax 视频运行会调用计费接口" }))
    await user.click(within(dialog).getByRole("button", { name: "开始付费运行" }))

    expect(onStartRun).toHaveBeenCalledWith({
      plan_id: planID,
      model_id: modelID,
      channel_id: channelID,
      confirm_paid_video: true,
    })
  })
})
