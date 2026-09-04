# Wan 视频 API 边界用例

本目录按 Wan 模型版本保存可执行的文生视频契约用例。它不是 Seedance 用例的别名：Wan 使用 DashScope 异步任务协议，创建任务后从 `output.task_id` 轮询 `/api/v1/tasks/{task_id}`，成功必须得到 `output.video_url`。

## 版本范围

- Wan 3.0：`wan3.0-video`、`wan3.0-video-prime`
- Wan 2.7：`wan2.7-t2v`、`wan2.7-t2v-2026-06-12`
- Wan 2.6：`wan2.6-t2v`
- Wan 2.5：`wan2.5-t2v-preview`
- Wan 2.2：`wan2.2-t2v-plus`
- Wan 2.1：`wanx2.1-t2v-turbo`、`wanx2.1-t2v-plus`

所有内置用例都不是默认用例。Wan 3.0 已落盘 191 个具体 Case，完整清单见 [Wan 3.0 具体测试 Case 矩阵](wan3-case-matrix.md)：74 个自动 Case 覆盖无需外部素材的字段/类型/枚举/默认值/数值边界，117 个禁用模板精确记录素材、环境与产物 oracle 的前置条件。

自动负向用例可在创建阶段 HTTP 400，或创建成功后任务进入 `FAILED` 时通过；两个阶段都必须同时含稳定的 provider `code` 与 `message`，且错误码属于官方 `InvalidParameter` 参数错误族。欠费、鉴权、限流、网关故障或仅有任务受理的 2xx 都不能冒充“边界被正确拒绝”。

## 执行安全

视频生成会计费。先使用 `--dry-run` 检查选中的版本、请求体和模型注入；真实运行必须显式传入 `--confirm-paid-suite`，并保持 `--concurrency 1`。本次构建只做静态、模拟服务器和 dry-run 验证，没有调用真实百炼接口。

素材输入、音频输入、最长时长成功生成等依赖外部资产或支出较高的 Case 以 `enabled=false` 的具体模板落盘，使用 `fixture://wan3/...` 描述所需确定性素材，并在 `options.reason` / `precondition` 中记录启用门槛；没有伪造 provider 文件 ID，也不会进入运行计划。

## Wan 3.0 Suite 分层

`wan3.0-video` 与 `wan3.0-video-prime` 各自提供相同的五类场景 Suite，避免把低成本烟测、自动合同验证和待素材完整矩阵混为一谈：

| 场景 | Case 数 | 组成 | 用途 |
|---|---:|---|---|
| 连通性测试 | 1 | 1 个 2 秒/480P 正向 Case | 最小成本证明鉴权、路由、任务轮询和结果 URL |
| 基本功能测试 | 6 | 5 个短正向 + 1 个必填负向 | 覆盖基础生成、比例、音频、Prompt 扩写、水印与输入校验 |
| 参数拒绝测试 | 39 | 39 个自动负向 | 验证类型、枚举、必填与数值边界在创建或任务终态稳定拒绝 |
| 完整测试（自动可执行） | 74 | 35 个自动正向 + 39 个自动负向 | 不依赖外部 fixture 的完整自动合同集合；包含高时长 Case，成本高于基本功能测试 |
| 完整矩阵（含禁用模板） | 191 | 74 个自动 + 117 个禁用模板 | 仅用于审阅完整合同与后续启用；当前不能直接作为可运行 Plan |

这些 Suite 全部保持 `default=false`。创建 Suite 不代表授权真实调用或消费百炼额度；执行视频 Suite 仍需要显式付费确认。真实执行应选择前四类自动 Suite；191 Case 的完整矩阵含禁用模板，只有逐项满足 fixture/oracle 前置条件并启用后才能转为可运行 Plan。

## 资料

- [Wan 3.0 视频生成 API](https://help.aliyun.com/zh/model-studio/wan3-video-generation-api-reference)
- [Wan 2.7 文生视频 API](https://help.aliyun.com/zh/model-studio/text-to-video-api-reference)
- [Wan 2.1～2.6 文生视频 API](https://help.aliyun.com/zh/model-studio/legacy-wan-text-to-video-api-reference)
- [百炼 API 错误码](https://help.aliyun.com/zh/model-studio/error-code/)
- [用户提供的百炼控制台模型页](https://bailian.console.aliyun.com/cn-beijing?tab=api#/api/?type=model&url=3049634)

约束提取日期：2026-09-05。控制台页需要登录态并由前端渲染，本矩阵的可验证依据使用公开的阿里云官方 API 文档。
