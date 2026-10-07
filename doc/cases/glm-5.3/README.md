# GLM-5.3 cases 与 suites

[English](README.en.md)

基于 2026-09-08 检索的[智谱 GLM-5.3 官方文档](https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3)，为普通 Model API 的 Chat Completions 构建。精确模型 ID 为 `glm-5.3`，沿用 `openai-chat` 协议。

共 212 个 case：165 个启用的自动用例、47 个禁用模板。完整的 54 行契约清单、来源和缺口见 [覆盖矩阵](coverage-matrix.md) 与 [机器可读清单](contract-inventory.json)。仍有待验证和阻塞项，因此这不是已实现完整覆盖或已通过实测的边界套件。

| Suite | Case 数量 | 用途 |
|---|---:|---|
| GLM 5.3 连通性套件 | 3 | 最终回答、缺失与无效鉴权 |
| GLM 5.3 基本功能套件 | 12 | 思考、SSE、JSON、工具、历史思考、缓存计数 |
| GLM 5.3 参数拒绝套件 | 101 | 参数错误的 HTTP 状态与业务错误码 |
| GLM 5.3 自动回归套件 | 165 | 全部已启用自动用例 |
| GLM 5.3 完整设计（含禁用模板）套件 | 212 | 所有设计及明确保留的缺口 |

所有用例都设置 `default=false`，适用模型仅为 `glm-5.3`。机械套件用 `glm53.*` 维度筛选，避免混入全模型通用用例。配置在 `data/suites/openai-chat/glm-5.3.suite-profiles.json`，产物由项目技能的生成器维护。

## 在 Studio 中使用

1. 渠道协议选择 `openai-chat`，直连普通 API 的 Base URL 填 `https://open.bigmodel.cn/api/paas/v4`，凭据通过渠道配置保存。
2. 将逻辑模型 `glm-5.3` 映射到上游 `glm-5.3`。case 的路径是 `/chat/completions`，与 Base URL 拼接后得到完整接口地址。
3. 在测试目录查看 `GLM 5.3` 命名的 case 和上述五个 suite，创建 Plan 时先选择连通性或基本功能套件。普通回归建议并发 1，单 case 超时至少 120 秒；历史/工具回传 case 包含两次请求。
4. T3 大输出、上下文、缓存负载、大工具数组，以及外部服务或文档不明确的模板保持禁用。补齐各模板的前置条件和断言后，再单独启用和执行。

连通性套件也提供快速测试入口，允许修改提示词，并检查 HTTP 成功和非空最终答案。凭据缺失/无效两项仍使用固定的最小请求。

普通 API 只支持 `reasoning_effort=low/high/max`。Coding Plan 的别名映射和默认历史思考行为不同；Responses 与 Anthropic 协议也需要独立契约。本套件不能直接证明这些端点的兼容性。经过网关时，错误码被归一化也会导致本套件失败，应作为兼容性差异记录。

## 断言与报告

- 普通成功用例保留 HTTP 状态、最终文本和终止原因；专用响应契约用例只检查 assistant 角色、`reasoning_content` 与 usage 算术。身份/时间戳及 `choices` 数量等 envelope 字段不再重复断言，覆盖矩阵因此标为 partial。`max_tokens=1` 单独验证 `length` 截断和用量上限。
- 参数拒绝要求 HTTP 400 和该 case 允许的 `1210/1213/1214` 等业务码。鉴权、模型不存在、安全拦截分别检查；不会把任意错误算成参数通过。
- 流式断言分开累积最终文本、思考与函数参数；检查唯一终止、`[DONE]`、用量以及函数参数 JSON。只有 reasoning 而无最终结果会失败。
- 工具边界用例只确认请求被接受并返回有效文本或工具调用；专用 roundtrip/stream 用例检查具体工具名和参数，不再单独断言名称/ID 非空或 `type=function`。单工具直接读取第一个调用，不使用 `each` 嵌套；ID 是否可用通过 roundtrip 回传结果验证。roundtrip 只提供合成的本地常量 fixture，不执行模型返回的任意函数；它回传原始 assistant 消息、reasoning 与匹配的 tool call ID，并合计两次请求的 token 用量。
- 从历史任务填入并重新测试时，沿用历史连接与输入，读取当前套件和用例定义。历史报告仍保留原断言；当前套件或用例已不存在时拒绝启动，不从历史快照恢复执行定义。
- 使用现有 `legacy.apiaudit` 报告通路，展示结果、简短失败原因、请求数及用量；无需新增配置表单或报告 DTO。模板不会伪装为已验证的成功。

样例提示词、标识和工具结果均为本项目 AI 辅助生成的合成测试素材，按仓库 Apache-2.0 许可分发。官方文档用于提取接口事实；没有复制文档全文、生产数据、凭据或第三方媒体。

## 静态验证

在仓库根目录运行：

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases/openai-chat --suites-root data/suites/openai-chat --manifest data/suites/openai-chat/glm-5.3.suite-profiles.json --check
python .agents/skills/api-boundary-test-case-design/scripts/audit_case_coverage.py data/cases/openai-chat --model glm-5.3
go test -p 1 ./... -count=1
```

审计脚本会同时包含适用于所有模型的既有通用 case；212 是 `GLM53-*` 文件和五个专属 suite 的数量，不能将脚本的总数误认为专属 case 数。

本次仅执行 T0 加载、生成一致性与模拟 HTTP/SSE 测试，没有发起 T1/T2/T3 供应商调用。

具体检查结果、环境处理和全部未完成覆盖行见 [验证记录](validation.md)。
