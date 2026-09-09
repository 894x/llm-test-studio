# MiniMax H3 视频生成 API 边界用例

[English](README.en.md) | [简体中文](README.md)

此目录面向 MiniMax-H3 原生视频创建与轮询。配套矩阵中的服务商约束采集于 2026-09-05；本次运行时接入没有重新验证这些外部约束。

## 当前目录

149 个 Case 均使用外层 schema 3 和显式协议断言：43 个已启用自动用例、101 个需要确定性媒体或付费生成的手动用例，以及 5 个已停用的模型／认证修改用例。模型和凭据由 Run 统一绑定；停用定义保留未覆盖的测试主张，不能在当前 Suite 中执行。

手动用例中的 `{{H3_*}}` 标记描述素材前提；启用执行前应通过编写输入替换为受控媒体，不能伪造服务商资产。

## 场景 Suite

| 场景 | 数量 | 执行要求 |
|---|---:|---|
| 连通性 | 1 | 创建、轮询和成功终态；包含一次付费生成 |
| 基础功能 | 21 | 5 个自动、16 个手动用例；需要素材和预算 |
| 参数拒绝 | 42 | 显式拒绝断言；不修改模型或认证 |
| 自动核心回归 | 43 | 最小成功与参数拒绝 |
| 完整视频边界 | 144 | 43 个自动、101 个手动用例 |

Suite schema 2 保存有序 Case ID 与显式输入映射。已提交的[场景清单](../../suites/minimax-video/MiniMax-H3.suite-profiles.json) 是生成成员列表的来源：

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases/minimax-video --suites-root data/suites/minimax-video --manifest data/suites/minimax-video/MiniMax-H3.suite-profiles.json --check
```

审查预期清单改动后使用 `--write`。生成器拒绝旧 kind 选择器和模型路由字段。

## 断言与执行

断言显式检查 HTTP 拒绝详情，或任务终态、身份和输出 URL。HTTP 200 或任务受理本身不代表业务成功；空断言仅观察。等待行为属于 Case workflow，准确字段要求以各个当前定义为准。

CLI `--dry-run` 仅准备已启用自动用例的请求，不联网，不能证明服务商行为。实时执行会产生生成费用。本次接入使用本地 HTTPS fixture、当前格式检查及单元／E2E 测试，没有调用真实 MiniMax 服务。

## 官方资料

- [创建视频生成任务](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-create)
- [查询任务](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-query)
- [Video V2 OpenAPI](https://platform.minimaxi.com/docs/api-reference/video/generation/api/v2-video-generation.json)
