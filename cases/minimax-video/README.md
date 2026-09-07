# MiniMax H3 视频生成 API 完整边界用例

[English](README.en.md) | [简体中文](README.md)

本目录覆盖 MiniMax 上游原生 `POST /v2/video_generation` 的 `MiniMax-H3` 请求参数，以及创建任务后通过 `GET /v2/query/video_generation/{task_id}` 查询新建任务到终态的工作流。它不是 OpenAI 兼容接口，也不是 Seedance、Wan Video 或 `MiniMax-H3-Max` 的别名。

## 范围

- 提供方与版本：MiniMax Video V2，模型仅 `MiniMax-H3`。
- 包含模式：文生视频、首/尾帧图生视频、多模态参考生视频。
- 包含契约：Bearer 鉴权、全部顶层请求字段、`content` 联合类型及 role 决策表、媒体格式与全部文档化边界、创建响应、单任务轮询、终态结果和 usage。
- 明确排除：`MiniMax-H3-Max`、任务列表、取消/删除、H3-Context-IR、视频再生成，以及任意未文档化行为。
- 官方约束检索日期：2026-09-05。

## 用例构成

共 149 个 case，全部 `enabled=true`、`default=false` 且只绑定 `model_targets=["MiniMax-H3"]`：

- 48 个 automatic：1 个最小业务成功、45 个 HTTP 400 参数拒绝、2 个 HTTP 401 鉴权拒绝。
- 101 个 manual：65 个付费或素材依赖的正向边界、36 个需要确定性媒体才能隔离主断言的负向边界。
- manual case 使用 `{{H3_*}}` fixture 标记，并在 `options.fixture_requirements` 中记录格式、尺寸、时长、数量或字节边界。未替换为仓库控制的真实素材前不得改为 automatic。

## 场景套件

同一批 case 按执行目的组合为五个 Suite；它们不会复制或改变 case 定义：

| Suite | 数量 | 适用场景 | 执行要求 |
|---|---:|---|---|
| MiniMax H3 连通性测试套件 | 3 | 上线后快速确认路由、有效鉴权、无效鉴权、任务创建、轮询和成功终态 | 包含 1 个 4 秒付费生成；无需外部媒体 fixture |
| MiniMax H3 基本功能测试套件 | 24 | 发布验收；覆盖文生视频、图生视频、参考生视频、回调、水印和 usage | 8 个 automatic、16 个 manual；需媒体 fixture、回调端点及预算 |
| MiniMax H3 参数拒绝测试套件 | 45 | 网关校验和上游参数合同回归；只验证 HTTP 400 拒绝契约 | 全部 automatic；不创建有效生成任务，无外部媒体 fixture |
| MiniMax H3 自动化核心回归套件 | 48 | CI 或日常低素材回归；覆盖最小成功、45 个参数拒绝和 2 个鉴权拒绝 | 无外部媒体 fixture；包含 1 个 4 秒付费生成 |
| MiniMax H3 视频生成完整边界套件 | 149 | 版本升级、合同漂移审计和完整发布认证 | 48 个 automatic、101 个 manual；需要完整 fixture 集与明确预算 |

连通性套件是基本功能套件的子集，基本功能套件是完整边界套件的子集。参数拒绝套件恰好包含 45 个 automatic `minimax_video_task_rejected` case；自动化核心回归是按 `execution_mode=automatic` 独立切片，恰好包含全部 48 个 automatic case。

五个 Suite 由 [`MiniMax-H3.suite-profiles.json`](../../suites/minimax-video/MiniMax-H3.suite-profiles.json) 统一生成。新增或调整 case 后运行：

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py `
  --cases-root cases/minimax-video `
  --suites-root suites/minimax-video `
  --manifest suites/minimax-video/MiniMax-H3.suite-profiles.json `
  --write
```

提交前把 `--write` 改为 `--check`，验证所有生成文件与 case 元数据一致。

## Runner 断言

- 参数拒绝仅在 HTTP 400、`error.type=bad_request_error`、`error.http_code="400"` 且 message 非空时通过。
- 鉴权拒绝仅在 HTTP 401、`error.type=authorized_error`、`error.http_code="401"` 且 message 非空时通过。
- 业务成功必须经历创建 `task_id` 和查询终态；仅 HTTP 200 或仅创建成功不能算通过。
- 终态必须保持 task id、model、`task_type=generation`、`modality=video` 一致，并返回无 userinfo 的 HTTP(S) 视频 URL。
- case 可要求精确的 resolution、duration、ratio、usage 字段及 `total_seconds=input_seconds+output_seconds`。

## 执行安全

视频生成会计费。可先使用 `--dry-run` 查看 48 个 automatic 请求；真实运行保持 `--concurrency 1`，点击开始或运行命令后直接执行。manual 用例不会被 API Audit 自动加载，必须先准备矩阵要求的确定性 fixture、回调端点和预算，再逐项转为 automatic。

本次补齐只执行 T0 schema、模拟服务器、单元/集成测试和 dry-run 验证，不调用 MiniMax 真实接口。

## 官方资料

- [创建视频生成任务](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-create)
- [查询任务](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-query)
- [Video V2 OpenAPI](https://platform.minimaxi.com/docs/api-reference/video/generation/api/v2-video-generation.json)
