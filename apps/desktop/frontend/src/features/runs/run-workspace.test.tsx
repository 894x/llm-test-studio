import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { EMPTY_CATALOG, type CatalogSnapshot } from "@/features/catalog/data"
import { EMPTY_COMPARISONS } from "@/features/comparisons/data"

import { NewRunSheet, RunWorkspace } from "./run-workspace"
import { FIXTURE_WORKSPACE } from "./fixtures"

it("shows separate Suite bars and aggregate Case progress", () => {
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  const run = snapshot.runs[0]
  run.case_count = 8
  run.observed_case_count = 3
  run.suite_progress = [
    { entry_id: run.id, name: "第一套件", case_count: 4, observed_case_count: 3, status: "running" },
    { entry_id: snapshot.runs[1].id, name: "第二套件", case_count: 4, observed_case_count: 0, status: "not_started" },
  ]
  snapshot.runs = [run]
  render(<RunWorkspace snapshot={snapshot} comparisons={EMPTY_COMPARISONS} commandPending={false} commandError="" onStopSending={vi.fn()} onCancelRun={vi.fn()} />)
  expect(screen.getByRole("progressbar", { name: "第一套件：3 / 4 个 Case 已有结果" })).toHaveAttribute("aria-valuenow", "75")
  expect(screen.getByRole("progressbar", { name: "第二套件：0 / 4 个 Case 已有结果" })).toHaveAttribute("aria-valuenow", "0")
  expect(screen.getByRole("progressbar", { name: "Case 进度：3 / 8" })).toHaveAttribute("aria-valuenow", "38")
})

it("shows task observations without a fictitious request total or timed target", () => {
  const snapshot = structuredClone(FIXTURE_WORKSPACE)
  snapshot.plans = []
  snapshot.runs = [snapshot.runs[0]]
  Object.assign(snapshot.runs[0], { source: "quick_task", status: "running", planned: 0, duration_ms: 0, completed: 6, passed: 6, failed: 0 })
  snapshot.active_run_id = snapshot.runs[0].id
  render(<RunWorkspace snapshot={snapshot} comparisons={EMPTY_COMPARISONS} commandPending={false} commandError="" onStopSending={vi.fn()} onCancelRun={vi.fn()} />)
  expect(screen.getAllByText("Case 已有结果 2 / 4").length).toBeGreaterThan(0)
  expect(screen.getByRole("progressbar", { name: "Case 进度：2 / 4" })).toHaveAttribute("aria-valuenow", "50")
  expect(screen.getAllByText(/6 个请求已完成/).length).toBeGreaterThan(0)
  expect(screen.queryByText(/6\/0|目标时长 0|定时运行/)).not.toBeInTheDocument()
})

describe("NewRunSheet video execution", () => {
  it.each(["wan-video", "seedance", "minimax-video"] as const)("starts %s through the common run action", async (protocol) => {
    const user = userEvent.setup()
    const caseID = "44444444-4444-4444-8444-444444444449"
    const modelID = "22222222-2222-4222-8222-222222222229"
    const channelID = "33333333-3333-4333-8333-333333333339"
    const planID = "11111111-1111-4111-8111-111111111119"
    const suiteID = "88888888-8888-4888-8888-888888888889"
    const catalog: CatalogSnapshot = {
      ...EMPTY_CATALOG,
      models: [{ id: modelID, revision: 1, name: "Wan 3.0", protocol: "wan-video", capabilities: ["video"] }],
      channels: [{ id: channelID, revision: 1, name: "百炼", base_url: "https://workspace.example", protocol: "wan-video", enabled: true, credential_configured: true, model_count: 1 }],
      channel_models: [{ id: "77777777-7777-4777-8777-777777777779", revision: 1, channel_id: channelID, model_id: modelID, upstream_model_name: "wan3.0-video" }],
      test_cases: [{
        id: caseID, revision: 1, key: "wan30.t2v.min_duration", name: "Wan 3.0 最小时长", dimension: "compatibility",
        protocol: "wan-video", model_targets: ["wan3.0-video"], enabled: true, default: false, severity: "critical", execution_mode: "automatic",
        definition_schema_version: 2, type: "legacy.apiaudit", type_version: 1, spec: { kind: "wan_task_success" },
      }],
      suites: [{
        id: suiteID, revision: 1, key: "wan30-boundary", name: "Wan 3.0 边界套件", protocol: "wan-video",
        model_target: "wan3.0-video", case_count: 1, cases: [{ case_id: caseID, revision: 1 }],
      }],
      plans: [{
        id: planID, revision: 1, name: "Wan 3.0 边界", model_count: 1, channel_count: 1, suite_count: 1, case_count: 1,
        model_ids: [modelID], channel_ids: [channelID], suites: [{
          entry_id: "99999999-9999-4999-8999-999999999999", suite_id: suiteID, suite_revision: 1,
          suite_key: "wan30-boundary", suite_name: "Wan 3.0 边界套件", protocol: "wan-video", model_target: "wan3.0-video", case_count: 1, cases: [{ case_id: caseID, revision: 1 }],
          parameters: {}, load_mode: "single", concurrency: 1, request_count: 1, rate_per_second: 0,
          duration_ms: 0, request_timeout_ms: 600000, sla_thresholds: { e2e_p95_ms: 600000 },
        }],
      }],
    }
    const onStartRun = vi.fn(async () => undefined)
	const secondChannelID = "33333333-3333-4333-8333-333333333338"
	catalog.channels.push({ ...catalog.channels[0], id: secondChannelID, name: "第二渠道" })
	catalog.channel_models.push({ ...catalog.channel_models[0], id: "77777777-7777-4777-8777-777777777778", channel_id: secondChannelID })
	catalog.plans[0].channel_ids.push(secondChannelID)
	catalog.models[0].protocol = protocol
	for (const channel of catalog.channels) channel.protocol = protocol
	catalog.test_cases[0].protocol = protocol
	catalog.suites[0].protocol = protocol
	catalog.plans[0].suites[0].protocol = protocol
	if (protocol === "seedance") {
		const second = { ...catalog.test_cases[0], id: "44444444-4444-4444-8444-444444444448", key: "seedance.second" }
		catalog.test_cases.push(second)
		catalog.suites[0].cases.push({ case_id: second.id, revision: second.revision })
		catalog.suites[0].case_count += 1
		catalog.plans[0].suites[0].cases.push({ case_id: second.id, revision: second.revision })
		catalog.plans[0].suites[0].case_count += 1
		catalog.plans[0].case_count += 1
	}

    render(<NewRunSheet
      plans={[{ id: planID, name: "Wan 3.0 边界", description: "版本边界", caseCount: 1, runCount: 0 }]}
      catalog={catalog}
      commandPending={false}
      onStartRun={onStartRun}
    />)

    await user.click(screen.getByRole("button", { name: "新建运行" }))
    const dialog = screen.getByRole("dialog", { name: "新建运行" })
    expect(within(dialog).queryByRole("checkbox")).not.toBeInTheDocument()
    expect(within(dialog).getByRole("button", { name: "开始运行" })).toBeEnabled()
	await user.click(within(dialog).getByRole("combobox", { name: "执行渠道" }))
	await user.click(screen.getByRole("option", { name: "第二渠道" }))
	expect(within(dialog).getByRole("button", { name: "开始运行" })).toBeEnabled()
    await user.click(within(dialog).getByRole("button", { name: "开始运行" }))

    expect(onStartRun).toHaveBeenCalledWith({
      plan_id: planID,
      model_id: modelID,
      channel_id: secondChannelID,
    })
  })
})
