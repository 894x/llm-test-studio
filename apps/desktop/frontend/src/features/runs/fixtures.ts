import type {
  CoreRunStatus,
  WorkspacePlan,
  WorkspaceRun,
  WorkspaceSnapshot,
} from "./data"
import type { CatalogPlanEntry, CatalogSnapshot, CatalogTestCase } from "@/features/catalog/data"
import type { ReportSnapshot } from "@/features/reports/data"

const PLAN_IDS = {
  copy: "11111111-1111-4111-8111-111111111111",
  json: "11111111-1111-4111-8111-111111111112",
  tools: "11111111-1111-4111-8111-111111111113",
  stream: "11111111-1111-4111-8111-111111111114",
}

const MODEL_IDS = {
  openai: "22222222-2222-4222-8222-222222222221",
  qwen: "22222222-2222-4222-8222-222222222222",
  claude: "22222222-2222-4222-8222-222222222223",
  gemini: "22222222-2222-4222-8222-222222222224",
  deepseek: "22222222-2222-4222-8222-222222222225",
  mini: "22222222-2222-4222-8222-222222222226",
}

const CHANNEL_IDS = {
  openai: "33333333-3333-4333-8333-333333333331",
  aliyun: "33333333-3333-4333-8333-333333333332",
  anthropic: "33333333-3333-4333-8333-333333333333",
  vertex: "33333333-3333-4333-8333-333333333334",
  compatible: "33333333-3333-4333-8333-333333333335",
}

const CASE_IDS = {
  chat: "44444444-4444-4444-8444-444444444441",
  json: "44444444-4444-4444-8444-444444444442",
  tools: "44444444-4444-4444-8444-444444444443",
  stream: "44444444-4444-4444-8444-444444444444",
}

const SUITE_ID = "88888888-8888-4888-8888-888888888881"

function caseEditor(_assertionKinds: string[]): Pick<CatalogTestCase, "definition_schema_version" | "type" | "type_version" | "spec"> {
  return { definition_schema_version: 2, type: "openai-chat", type_version: 1,
    spec: { inputs: { prompt: { type: "string", default: "hello" } }, request: { body: { messages: [{ role: "user", content: { $input: "prompt" } }] } }, assertions: [{ id: "http-status", source: "http.status", operator: "equals", value: 200 }] } }
}

const ALL_CASE_REVISIONS = [
  { case_id: CASE_IDS.chat },
  { case_id: CASE_IDS.json },
  { case_id: CASE_IDS.tools },
  { case_id: CASE_IDS.stream },
]

function fixturePlanEntry(
  entryID: string,
  loadMode: CatalogPlanEntry["load_mode"],
  concurrency: number,
  requestCount: number,
  ratePerSecond: number,
  slaP95: number,
): CatalogPlanEntry {
  return {
    entry_id: entryID,
    target_id: SUITE_ID, target_kind: "suite", target_key: "openai-regression", target_name: "OpenAI 回归套件",
    case_count: ALL_CASE_REVISIONS.length,
    parameters: {},
    load_mode: loadMode,
    concurrency,
    request_count: requestCount,
    rate_per_second: ratePerSecond,
    duration_ms: 0,
    request_timeout_ms: 30_000,
    warmup_count: 0, settings: {},
    sla_thresholds: { p95_ms: slaP95 },
  }
}

export const FIXTURE_CATALOG: CatalogSnapshot = {
  schema_version: 4,
  case_types: [
    { type: "openai-chat", type_version: 1, label: "OpenAI Chat", category: "protocol", scheduling_owner: "plan", supported_protocols: ["openai-chat"], creatable: true, default_spec: caseEditor([]).spec },
    { type: "seedance", type_version: 1, label: "Seedance", category: "protocol", scheduling_owner: "plan", supported_protocols: ["seedance"], creatable: true, default_spec: { inputs: {}, request: { body: { content: [] } }, assertions: [] } },
    { type: "wan-video", type_version: 1, label: "Wan Video", category: "protocol", scheduling_owner: "plan", supported_protocols: ["wan-video"], creatable: true, default_spec: { inputs: {}, request: { body: { input: {} } }, assertions: [] } },
    { type: "minimax-video", type_version: 1, label: "MiniMax Video", category: "protocol", scheduling_owner: "plan", supported_protocols: ["minimax-video"], creatable: true, default_spec: { inputs: {}, request: { body: { prompt: "hello" } }, assertions: [] } },
  ],
  models: [
    { id: MODEL_IDS.openai, revision: 2, name: "gpt-5.2", protocol: "openai-chat", capabilities: ["text", "json", "tools"] },
    { id: MODEL_IDS.qwen, revision: 1, name: "qwen3-max", protocol: "openai-chat", capabilities: ["text", "json"] },
    { id: MODEL_IDS.claude, revision: 1, name: "claude-sonnet-4", protocol: "openai-chat", capabilities: ["text", "tools"] },
    { id: MODEL_IDS.gemini, revision: 1, name: "gemini-2.5-pro", protocol: "openai-chat", capabilities: ["text", "multimodal"] },
    { id: MODEL_IDS.deepseek, revision: 1, name: "deepseek-v3.2", protocol: "openai-chat", capabilities: ["text", "stream"] },
    { id: MODEL_IDS.mini, revision: 1, name: "gpt-4.1-mini", protocol: "openai-chat", capabilities: ["text"] },
  ],
  channels: [
    { id: CHANNEL_IDS.openai, revision: 2, name: "OpenAI 主渠道", base_url: "https://api.openai.com/v1", protocol: "openai-chat", enabled: true, credential_configured: true, model_count: 2 },
    { id: CHANNEL_IDS.aliyun, revision: 1, name: "阿里云备用渠道", base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1", protocol: "openai-chat", enabled: true, credential_configured: true, model_count: 1 },
    { id: CHANNEL_IDS.anthropic, revision: 1, name: "Anthropic 主渠道", base_url: "https://gateway.example.test/anthropic/v1", protocol: "openai-chat", enabled: true, credential_configured: true, model_count: 1 },
    { id: CHANNEL_IDS.vertex, revision: 1, name: "Vertex 测试渠道", base_url: "https://gateway.example.test/vertex/v1", protocol: "openai-chat", enabled: false, credential_configured: false, model_count: 1 },
    { id: CHANNEL_IDS.compatible, revision: 1, name: "兼容协议渠道", base_url: "https://gateway.example.test/compatible/v1", protocol: "openai-chat", enabled: true, credential_configured: true, model_count: 1 },
  ],
  channel_models: [
    { id: "77777777-7777-4777-8777-777777777771", revision: 1, channel_id: CHANNEL_IDS.openai, model_id: MODEL_IDS.openai, upstream_model_name: "gpt-5.2" },
    { id: "77777777-7777-4777-8777-777777777772", revision: 1, channel_id: CHANNEL_IDS.openai, model_id: MODEL_IDS.mini, upstream_model_name: "gpt-4.1-mini" },
    { id: "77777777-7777-4777-8777-777777777773", revision: 1, channel_id: CHANNEL_IDS.aliyun, model_id: MODEL_IDS.qwen, upstream_model_name: "qwen3-max" },
    { id: "77777777-7777-4777-8777-777777777774", revision: 1, channel_id: CHANNEL_IDS.anthropic, model_id: MODEL_IDS.claude, upstream_model_name: "claude-sonnet-4" },
    { id: "77777777-7777-4777-8777-777777777775", revision: 1, channel_id: CHANNEL_IDS.vertex, model_id: MODEL_IDS.gemini, upstream_model_name: "gemini-2.5-pro" },
    { id: "77777777-7777-4777-8777-777777777776", revision: 1, channel_id: CHANNEL_IDS.compatible, model_id: MODEL_IDS.deepseek, upstream_model_name: "deepseek-v3.2" },
  ],
  test_cases: [
    { id: CASE_IDS.chat, revision: 3, key: "T001", name: "基础对话", dimension: "must", protocol: "openai-chat", enabled: true, default: true, severity: "critical", execution_mode: "automatic", ...caseEditor(["response_schema", "text"]) },
    { id: CASE_IDS.json, revision: 2, key: "T016", name: "JSON 模式", dimension: "response", protocol: "openai-chat", enabled: true, default: false, severity: "critical", execution_mode: "automatic", ...caseEditor(["response_schema", "json"]) },
    { id: CASE_IDS.tools, revision: 1, key: "T037", name: "工具调用", dimension: "tools", protocol: "openai-chat", enabled: true, default: false, severity: "normal", execution_mode: "automatic", ...caseEditor(["response_schema", "tool_call"]) },
    { id: CASE_IDS.stream, revision: 2, key: "T008", name: "流式结束", dimension: "streaming", protocol: "openai-chat", enabled: true, default: false, severity: "normal", execution_mode: "automatic", ...caseEditor(["stream_end", "text"]) },
  ],
  suites: [
    { id: SUITE_ID, revision: 2, key: "openai-regression", name: "OpenAI 回归套件", protocol: "openai-chat", description: "OpenAI protocol regression", inputs: [], case_count: 4, cases: ALL_CASE_REVISIONS },
    { id: "88888888-8888-4888-8888-888888888882", revision: 1, key: "openai-connectivity", name: "OpenAI Chat 连通性测试", protocol: "openai-chat", case_count: 1, cases: [ALL_CASE_REVISIONS[0]], description: "发送一条消息并检查响应。", inputs: [{ key: "prompt", label: "测试消息", type: "string", default: "hello", bindings: [{ case_id: CASE_IDS.chat, input: "prompt" }] }] },
  ],
  plans: [
    { id: PLAN_IDS.copy, revision: 1, name: "营销文案基准", protocol: "openai-chat", seed: 1, entry_count: 1, case_count: 4, entries: [fixturePlanEntry(PLAN_IDS.copy, "fixed_concurrency", 4, 120, 0, 2000)] },
    { id: PLAN_IDS.json, revision: 1, name: "JSON 模式回归", protocol: "openai-chat", seed: 1, entry_count: 1, case_count: 4, entries: [fixturePlanEntry(PLAN_IDS.json, "fixed_concurrency", 2, 24, 0, 2500)] },
    { id: PLAN_IDS.tools, revision: 1, name: "工具调用兼容性", protocol: "openai-chat", seed: 1, entry_count: 1, case_count: 4, entries: [fixturePlanEntry(PLAN_IDS.tools, "fixed_concurrency", 8, 72, 0, 3000)] },
    { id: PLAN_IDS.stream, revision: 1, name: "流式性能门禁", protocol: "openai-chat", seed: 1, entry_count: 1, case_count: 4, entries: [fixturePlanEntry(PLAN_IDS.stream, "open_loop", 1, 180, 12, 1800)] },
  ],
}

export const FIXTURE_REPORTS: ReportSnapshot = {
  schema_version: 1,
  reports: [
    { id: "66666666-6666-4666-8666-666666666661", source: "run", run_id: "55555555-5555-4555-8555-555555555553", generated_at: "2026-08-30T07:34:00Z", run_status: "completed", plan_name: "多轮工具调用", model_name: "claude-sonnet-4", channel_name: "Anthropic 主渠道", passed: true, verdict: "兼容性门禁通过", issue_count: 0, case_count: 18, failed_case_count: 0, passed_case_count: 18, verified_case_count: 18, observed_case_count: 0, attachment_count: 2 },
    { id: "66666666-6666-4666-8666-666666666662", source: "run", run_id: "55555555-5555-4555-8555-555555555554", generated_at: "2026-08-30T05:18:00Z", run_status: "completed", plan_name: "长上下文边界", model_name: "gemini-2.5-pro", channel_name: "Vertex 测试渠道", passed: false, verdict: "存在一项语义回归", issue_count: 1, case_count: 36, failed_case_count: 1, passed_case_count: 35, verified_case_count: 36, observed_case_count: 0, attachment_count: 2 },
    { id: "66666666-6666-4666-8666-666666666663", source: "run", run_id: "55555555-5555-4555-8555-555555555552", generated_at: "2026-08-30T08:54:00Z", run_status: "failed", plan_name: "JSON 模式回归", model_name: "qwen3-max", channel_name: "阿里云备用渠道", passed: false, verdict: "协议错误导致运行失败", issue_count: 2, case_count: 24, failed_case_count: 3, passed_case_count: 21, verified_case_count: 24, observed_case_count: 0, attachment_count: 3 },
  ],
}

export const FIXTURE_WORKSPACE: WorkspaceSnapshot = {
  schema_version: 2,
  active_run_id: "55555555-5555-4555-8555-555555555551",
  plans: [
    plan(PLAN_IDS.copy, "营销文案基准", 36, 2, "fixed_concurrency", 4, 120),
    plan(PLAN_IDS.json, "JSON 模式回归", 24, 2, "fixed_concurrency", 2, 24),
    plan(PLAN_IDS.tools, "工具调用兼容性", 18, 1, "fixed_concurrency", 8, 72),
    plan(PLAN_IDS.stream, "流式性能门禁", 12, 1, "open_loop", 1, 180, 12),
  ],
  runs: [
    run({
      id: "55555555-5555-4555-8555-555555555551",
      planId: PLAN_IDS.copy,
      planName: "营销文案基准",
      modelId: MODEL_IDS.openai,
      modelName: "gpt-5.2",
      channelId: CHANNEL_IDS.openai,
      channelName: "OpenAI 主渠道",
      status: "running",
      completed: 82,
      planned: 120,
      passed: 78,
      failed: 4,
      concurrency: 4,
      artifactCount: 6,
      startedAt: "2026-08-30T09:18:00Z",
      updatedAt: "2026-08-30T09:24:42Z",
    }),
    run({
      id: "55555555-5555-4555-8555-555555555552",
      planId: PLAN_IDS.json,
      planName: "JSON 模式回归",
      modelId: MODEL_IDS.qwen,
      modelName: "qwen3-max",
      channelId: CHANNEL_IDS.aliyun,
      channelName: "阿里云备用渠道",
      status: "failed",
      completed: 24,
      planned: 24,
      passed: 21,
      failed: 3,
      concurrency: 2,
      artifactCount: 4,
      startedAt: "2026-08-30T08:51:00Z",
      updatedAt: "2026-08-30T08:53:18Z",
    }),
    run({
      id: "55555555-5555-4555-8555-555555555553",
      planId: PLAN_IDS.tools,
      planName: "多轮工具调用",
      modelId: MODEL_IDS.claude,
      modelName: "claude-sonnet-4",
      channelId: CHANNEL_IDS.anthropic,
      channelName: "Anthropic 主渠道",
      status: "completed",
      completed: 72,
      planned: 72,
      passed: 72,
      failed: 0,
      concurrency: 8,
      artifactCount: 8,
      startedAt: "2026-08-30T07:24:00Z",
      updatedAt: "2026-08-30T07:33:04Z",
    }),
    run({
      id: "55555555-5555-4555-8555-555555555554",
      planId: PLAN_IDS.copy,
      planName: "长上下文边界",
      modelId: MODEL_IDS.gemini,
      modelName: "gemini-2.5-pro",
      channelId: CHANNEL_IDS.vertex,
      channelName: "Vertex 测试渠道",
      status: "completed",
      completed: 36,
      planned: 36,
      passed: 35,
      failed: 1,
      concurrency: 2,
      artifactCount: 5,
      startedAt: "2026-08-30T05:06:00Z",
      updatedAt: "2026-08-30T05:17:38Z",
    }),
    run({
      id: "55555555-5555-4555-8555-555555555555",
      planId: PLAN_IDS.stream,
      planName: "流式首字延迟",
      modelId: MODEL_IDS.deepseek,
      modelName: "deepseek-v3.2",
      channelId: CHANNEL_IDS.compatible,
      channelName: "兼容协议渠道",
      status: "queued",
      completed: 0,
      planned: 180,
      passed: 0,
      failed: 0,
      concurrency: 1,
      ratePerSecond: 12,
      artifactCount: 0,
      startedAt: "2026-08-30T04:58:00Z",
      updatedAt: "2026-08-30T04:58:00Z",
    }),
    run({
      id: "55555555-5555-4555-8555-555555555556",
      planId: PLAN_IDS.json,
      planName: "安全拒答抽查",
      modelId: MODEL_IDS.mini,
      modelName: "gpt-4.1-mini",
      channelId: CHANNEL_IDS.openai,
      channelName: "OpenAI 主渠道",
      status: "cancelled",
      completed: 7,
      planned: 24,
      passed: 7,
      failed: 0,
      concurrency: 2,
      artifactCount: 2,
      startedAt: "2026-08-29T14:48:00Z",
      updatedAt: "2026-08-29T14:48:51Z",
    }),
  ],
}

function plan(
  id: string,
  name: string,
  caseCount: number,
  runCount: number,
  loadMode: WorkspacePlan["load_mode"],
  concurrency: number,
  requestCount: number,
  ratePerSecond = 0,
): WorkspacePlan {
  return { protocol: "openai-chat",
    id,
    revision: 1,
    name,
    case_count: caseCount,
    run_count: runCount,
    load_mode: loadMode,
    concurrency,
    request_count: requestCount,
    rate_per_second: ratePerSecond,
    duration_ms: 0,
    request_timeout_ms: 30_000,
  }
}

function run(input: {
  id: string
  planId: string
  planName: string
  modelId: string
  modelName: string
  channelId: string
  channelName: string
  status: CoreRunStatus
  completed: number
  planned: number
  passed: number
  failed: number
  concurrency: number
  ratePerSecond?: number
  artifactCount: number
  startedAt: string
  updatedAt: string
}): WorkspaceRun {
  const conclusion =
    input.status === "completed"
      ? input.failed > 0
        ? "failed"
        : "passed"
      : input.status === "failed"
        ? "failed"
        : "none"
  return {
    case_count: 4,
    observed_case_count: input.status === "completed" ? 4 : input.completed > 0 ? 2 : 0,
    entry_progress: [{ entry_id: input.id, name: input.planName, case_count: 4,
      observed_case_count: input.status === "completed" ? 4 : input.completed > 0 ? 2 : 0,
      status: input.status === "starting" || input.status === "queued" ? "queued" : input.status === "draining" ? "running" : input.status }],
    id: input.id,
    revision: 1,
    plan_id: input.planId,
    plan_revision: 1,
    plan_name: input.planName,
    status: input.status,
    conclusion,
    model_id: input.modelId,
    model_revision: 1,
    model_name: input.modelName,
    channel_id: input.channelId,
    channel_revision: 1,
    channel_name: input.channelName,
    load_mode: input.ratePerSecond ? "open_loop" : "fixed_concurrency",
    concurrency: input.concurrency,
    rate_per_second: input.ratePerSecond ?? 0,
    planned: input.planned,
    duration_ms: 0,
    completed: input.completed,
    passed: input.passed,
    failed: input.failed, observed: 0, indeterminate: 0,
    artifact_count: input.artifactCount,
    started_at: input.startedAt,
    updated_at: input.updatedAt,
  }
}
