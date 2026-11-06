# Kimi K3 / K3.1 响应协议检查

## 范围与来源

检索日期：2026-11-07。适用运行时绑定的 `kimi-k3`、`kimi-k3.1-pro`，分别检查 Chat Completions、Responses、Messages 的普通文本 JSON / SSE 响应。包含响应信封、文本容器、已返回字段的类型、流结束和思考用量明细；不扩展为全部请求参数、工具、多模态、错误码或计费金额审计，不承诺字段无损转发的逐字节一致性。

来源：[Chat](https://platform.kimi.com/docs/api/chat)、[Responses](https://platform.kimi.com/docs/api/responses)、[Messages](https://platform.kimi.com/docs/api/messages)、[OpenAPI](https://platform.kimi.com/docs/openapi.json)、[推理强度](https://platform.kimi.com/docs/guide/use-reasoning-effort)。Messages 思考计数的输出子集语义另见 [Anthropic 文档](https://platform.claude.com/docs/en/build-with-claude/thinking-steering-and-cost)。以上链接均检索于上述日期。

OpenAPI 的许多响应属性没有列入 `required`；Chat 的思考计数还只出现在官方响应示例中。因此分成两类 Case：结构检查允许文档可选字段缺失，返回时必须符合类型；思考用量检查是明确选择的完整性探针，要求供应商/网关返回文档展示的原生明细，缺失即失败。后者用于发现类似 new-api `6be41ef2ca482aa82dbef888664fe1306847fc5c` 的字段丢失，不把失败直接解释为所有兼容 API 均违反强制标准，也不把缺失补为零。

## 契约清单与矩阵

以下字段按协议原生路径检查，不从归一化 metrics 推断原始字段存在。全部 Case 为 T1、enabled、automatic、非默认；请求只要求一个短文本回答，最多生成 2048 token。执行证据初始为未运行，完成后的实际结果记录在文末。

| 契约项 / 路径 | 来源 | 类型、存在性、分区与依赖 | 必要场景与断言 | Case 后缀 `kimi.protocol.` | 状态 |
| --- | --- | --- | --- | --- | --- |
| HTTP 与响应根 | 三协议 / OpenAPI | 200、object；成功文本场景 | 正常文本成功；非 object、缺文本、错误状态拒绝 | 全部 | implemented / live not run |
| id、model、created / created_at、object / type、role | 三协议 / OpenAPI | 可选字段返回时按原生类型/枚举；不要求模型别名回显一致 | 缺省允许；错误类型、错误协议常量拒绝 | `response_shape`、`response_shape_stream` | implemented / live not run |
| Chat choices、message、content、reasoning_content、finish_reason | Chat / OpenAPI | 本文本场景要求 choices 与可见文本；可空字符串字段按文档允许 null；index 为整数 | 数组/对象/字符串类型、assistant、有效结束原因；不检查固定 ID 前缀 | 两个 response_shape | implemented / live not run |
| Messages content 块、stop_reason、stop_sequence | Messages / OpenAPI | 本文本场景要求 content 与可见文本；块类型及文字/思考/签名返回时检查；stop_sequence 可空 | 错容器/字段类型拒绝；不要求 thinking 位于固定下标 | 两个 response_shape | implemented / live not run |
| Responses output 消息及文本块、status | Responses / OpenAPI | 文本场景要求输出文本并 completed；其余可选字段返回时检查 | 错容器/字段类型、非 completed 拒绝；不把所有响应输出强制为 message | 两个 response_shape | implemented / live not run |
| 原生 usage 及顶层计数 | 三协议 / OpenAPI | 结构 Case 可省略；返回时 object、非负整数；Responses usage 可 null | 错类型、负数、小数拒绝；用量 Case 则要求全部总数存在 | 全部 | implemented / live not run |
| Chat completion_tokens_details.reasoning_tokens | Chat 响应示例 | 完整性探针要求 object + 非负整数；允许显式 0 | 缺对象/计数、null、字符串、负数、小数、计数超过 completion_tokens 拒绝 | `thinking_usage`、`thinking_usage_stream` | implemented / live not run |
| Responses output_tokens_details.reasoning_tokens | Responses / OpenAPI | 完整性探针要求 object + 非负整数；允许显式 0 | 同上；计数不得超过 output_tokens | 两个 thinking_usage | implemented / live not run |
| Messages output_tokens_details.thinking_tokens | Messages / OpenAPI；Anthropic 语义 | 完整性探针要求 object + 非负整数；允许显式 0 | 同上；计数不得超过 output_tokens；捕获被 sanitizer 删除的明细 | 两个 thinking_usage | implemented / live not run |
| 用量算术 | Chat / Responses | input + output = total；思考已包含于输出，不重复相加 | 正确、总数不符、思考大于输出分别验证；Messages 不虚构 total_tokens | 两个 thinking_usage | implemented / live not run |
| SSE 终态、事件形状 | 三协议；既有解析器 | 必须完成且 issues 为空；保存的事件按协议检查类型；Responses sequence_number 为整数 | 正常流、断流、错误事件字段拒绝；Chat 显式 include_usage，要求恰好一次 finish，拒绝 finish 后内容及 DONE 后数据；严格用量 Case 要求恰好一个最终 usage 块 | 两个 `_stream` | implemented / live not run |
| 全部 SSE 事件的无损性及字段出现时刻 | 既有 evidence 保存上限 | 原始 events 仅保存前 256 条；合并后的 response 保留最终 usage | 本补充检查保留事件及合并结果，不声称检查所有事件或严格证明最终事件才含明细 | 无 | deferred：需要完整事件验证能力 |
| 网关转发计数精确保真 | 上游与下游对照 | 单侧响应只能证明字段存在、类型与内部关系 | 验证 255 未被改成 0、总输出未同步错误扩大，需可控上游或双侧证据 | 无 | deferred：专项上游夹具，非在线探针可独立证明 |

## 套件与兼容性

每种协议各有一个 `Kimi K3 / K3.1 协议完整性` 专项 Suite，包含上述四个逻辑 Case；同一 Case ID 跨三个协议复用。运行时选择 K3 或 K3.1 Pro 及对应渠道。专项套件独立于原日常、基础和参数拒绝套件，避免改变已有任务的通过条件；可单独选择结构 Case，或运行包含严格用量要求的完整套件。

仅添加当前 schema 3 Case、schema 1 Suite 与本地回归夹具；不改变运行器、公开接口、数据格式、历史 Case ID、快照或已保存结论，不需要数据迁移，没有弃用/移除计划。Chat 严格流式用量探针使用既有 `stream_final_usage_count` 指标，沿用 `cache_write_headers` 能力门禁；不具备该能力的远程 runner 在提交前拒绝执行，需选择已有该能力的版本。其他定义不增加能力要求。旧版本不会自动获得新增内置 Case；更新并重启应用后加载。降级时新增目录可能不可见，原有数据与历史报告含义不变。

## 验证证据

验证完成：4 个逻辑 Case、12 份协议定义、3 个各含 4 Case 的专项 Suite，目录总数更新为 1,089 Case / 81 Suite。两个模型、三个协议及各类正常/损坏响应共 360 个本地 HTTP/SSE 组合通过；覆盖字段缺失、类型错误、负数、小数、计数上界和加和、HTTP 错误、断流、错误事件、Chat 缺 finish / DONE 后数据 / 仅早期 usage，以及超过 256 事件后的合并响应。Messages 另验证末尾明细、显式零修正、后续部分 usage 保留先前明细；有效证据经过 JSON 往返后断言仍通过。

以下验证通过：`go test` 的 `data/cases`、`data/suites`、Case/Suite catalog、casecodec、testspec、全部协议包、jobcontract、CLI、application/catalog 和 persistence/workspace；新增测试包与 Suite catalog 的 `go vet`；三份新 manifest 的生成器 `--check`；技能的 7 项 Python 回归；`git diff --check`。没有 UI、Wails 绑定或构建契约变化，未运行桌面构建。未调用付费供应商 API，不把本地模拟结果称为真实模型执行通过；矩阵中两个 deferred 范围仍未完成。
