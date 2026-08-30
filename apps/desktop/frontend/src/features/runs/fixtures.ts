import type {
  CoreRunStatus,
  WorkspacePlan,
  WorkspaceRun,
  WorkspaceSnapshot,
} from "./data"

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

export const FIXTURE_WORKSPACE: WorkspaceSnapshot = {
  schema_version: 1,
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
  return {
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
    failed: input.failed,
    artifact_count: input.artifactCount,
    started_at: input.startedAt,
    updated_at: input.updatedAt,
  }
}
