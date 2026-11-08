# Kimi 缓存写入与用量对账

## 2026-11-09 拆分范围与契约清单

本次范围是现有 `kimi-k3` / `kimi-k3.1-pro` 缓存专项：Chat Completions、
Responses、Messages，默认/5m/1h 冷写入及 5m/1h 重复读取，均覆盖 JSON 和 SSE。
只验证上报用量与响应头，不覆盖 TTL 实际过期、续期、跨组织隔离或其他模型参数。
依据为 2026-11-09 检索的[官方缓存文档](https://platform.kimi.com/docs/guide/context-caching)。
Chat/Responses 嵌套 `cache_creation` 是兼容提供方的已观察扩展，区别于官方明确的总读写计数；
MSH 响应头在官方文档以 Chat 为例说明，Responses/Messages 的必返要求仅属于可选兼容性验收。

实现前的结构和行为清单如下；每项分别对应 usage 或 headers case，不能相互替代。

| 边界 | 字段/形状及前提 | 必要场景和预期 | 所属 case | 验证范围 |
| --- | --- | --- | --- | --- |
| 请求 TTL | Chat/Responses 顶层 `prompt_cache_options`，Messages 顶层 `cache_control` | 省略默认5m、显式5m、显式1h；保持既有请求参数 | 既有 usage 及对应 headers | T0/本地 HTTP/SSE，T3 live 不执行 |
| 原生读写计数 | Chat/Responses details 下 `cached_tokens/cache_write_tokens`；Messages 独立读写量 | 必须存在、非负整数、冷写入为正；重复请求读取为正且不重写；保留已有上界 | usage | 三协议、流式和非流式 |
| 输出与总量 | 输出为非负整数；Chat/Responses `total_tokens` | 总量严格等于输入加输出；不能以 HTTP200 替代用量判断 | usage | 三协议输出，两个 OpenAI 协议总量 |
| 原生 TTL | Messages `usage.cache_creation` 两个计数 | 必须存在且非负整数、和等于写入量；冷写入非选中档为零 | usage | 保留原断言 |
| 扩展 TTL | Chat/Responses 顶层或各自 details 下 `cache_creation` | 可缺失；一旦返回必须为 object，所选档等于写入量，另档为零或省略；零写入允许省略零档；错误类型/负数/错档/错和均失败 | usage | 观测型兼容性断言，不伪造官方必返契约 |
| 两个 MSH 头 | 非负十进制整数，两个字段均必须存在 | 缺失、负数、小数、重复及歧义值失败；两档和等于该请求原生写入总量；冷写入只在请求档位增加 | headers | 三协议可选专项，区别缺失和显式0 |
| 分离结果 | usage 不依赖 MSH 头及其解析指标 | usage 正确而头缺失/格式错误/错和时，usage 通过、headers 失败；其他协议错误仍失败 | 两组本地 fixture | 不以另一组兜底通过 |
| 流终态 | SSE 正常终止，Chat finish 后单独最终 usage | 缺最终 usage、提前中断、DONE 后数据及 finish 后内容失败 | 两组 case | 沿用既有流指标与 runner 能力门禁 |
| 读序列 | 同次执行两次请求完全相同 | 不能因第二步缺头继承第一步指标；不能改前缀证明命中 | 两组 case | 显式本地故障场景 |

兼容性：既有 10 个 usage Case 的 ID、key、schema 3 和三个 usage Suite 的 key/成员
引用保持。当前内置定义把头断言迁到新 Case，并加强 body 对账；用户覆盖、已冻结的运行
快照及历史结论不改写或重算。新增 10 个 headers Case 与三个独立 headers Suite，全部为
T3、非默认，不加入日常套件。Case/Suite/runner/报告/数据库格式不变，无迁移或弃用计划。
旧版本仍可读取同格式资产，但其内置目录保留旧预期；降级不转换用户数据。

Responses/Messages 的 sequence 仍保留所有协议诊断；缓存响应头格式错误不再提前
中止后续已声明步骤，正文解析失败仍中止。usage case 仅允许 `headers` 阶段的
`invalid_cache_write_header` 诊断，其余问题仍失败；headers case 保持零问题要求。
因此格式错误和缺失可以独立判定，也能保留重复读取第二次请求的证据；没有额外自动重试。

当前共 20 个逻辑 Case、60 份协议定义；三个原 usage Suite 各含原 10 个 Case，
三个新 headers Suite 各含对应的 10 个 Case。两组分开运行会分别产生请求费用，
所有用例继续 `default=false`，本次仅做本地验证。

2026-11-09 本地验证已通过：

- `go test ./data/... ./internal/casecodec ./internal/testspec ./internal/protocols/... ./internal/jobcontract ./internal/application/casecatalog ./internal/application/suitecatalog ./cmd/llm-test-studio ./internal/application/workspace -count=1`
- 补齐单头缺失及顶层/details 明细冲突 fixture 后，`go test ./data/cases -run '^TestKimiCache' -count=1`。
  三协议均验证缺失、负数、小数、重复头与 usage 结论独立，读序列不被头诊断截断；
  Chat/Responses 顶层和 details 的扩展明细分别验证，不能用另一处正确值覆盖矛盾值。
- 三协议专项 manifest 的生成器 `--check`，以及用例设计技能的 7 项 Python 回归。
- 原 10 个 Case 的 ID/key、请求/工作流及原三个 Suite 的成员引用一致性检查，`git diff --check`。

以上 HTTP/SSE 验证均使用本地模拟服务，未运行付费 Provider 测试，不代表线上验收通过。

此节作为当前拆分的范围清单；下文记录拆分前的 2026-11-03 基线。

## 契约与范围

核验日期：2026-11-03。主要来源：[上下文缓存最佳实践](https://platform.kimi.com/docs/guide/context-caching)；Chat 最终用量块的 `choices=[]`、finish 后顺序及 `include_usage` 契约另见 [Chat Completions API](https://platform.kimi.com/docs/api/chat)。

本次补充 `kimi-k3`、`kimi-k3.1-pro` 的 Chat Completions 缓存 usage 与 MSH 用量头核验，以及 Responses / Messages 的原生 usage 核验。Case 不绑定模型；运行时选择对应模型和协议。暂不据此承诺 Highspeed 的 Cache Write 支持。新探针属于 T3、非默认 Case，独立套件按需运行。

结构清单：`prompt_cache_options.mode/ttl`；Chat `usage.prompt_tokens`、`prompt_tokens_details.cached_tokens/cache_write_tokens`；Responses `usage.input_tokens`、`input_tokens_details.cached_tokens/cache_write_tokens`；Messages 顶层 `cache_control.type/ttl`、`usage.input_tokens/cache_read_input_tokens/cache_creation_input_tokens`、`cache_creation.ephemeral_5m_input_tokens/ephemeral_1h_input_tokens`；Chat `Msh-Usage-Cache-Write-Tokens-5m/1h`；流式最终 usage。

行为清单：省略 TTL 默认 5m；显式 5m/1h；新前缀写入；相同前缀读取；非负整数计数；读写量分别不超过总输入；两档头之和等于 usage 写入量；新前缀只写入所选档位；流式/非流式均可对账。首次写入使用每次执行独立的随机长前缀，避免固定前缀被预先缓存。读取探针连续发送完全相同的请求，第二次要求读取大于零且无新增写入。

以下字段均以同一官方页面和 2026-11-03 检索结果为来源；未包含的模型参数沿用现有目录，本补充不声称覆盖整个 API 契约。

| 字段/协议 | 类型、存在性与默认 | 有效分区 | 无效/边界及依赖 | 预期与断言 |
| --- | --- | --- | --- | --- |
| Chat/Responses `prompt_cache_options.mode/ttl` | 可选 object；mode=implicit；TTL string，默认5m | omitted、显式5m、显式1h | 非法请求值沿用既有参数拒绝 Case；冷写入必须有独立新前缀 | HTTP200、正写入；Chat 非选中档为0 |
| Messages `cache_control.type/ttl` | 可选顶层object；type=ephemeral；TTL默认5m | omitted、显式5m、显式1h | 消息内标记不生效，此补充只使用顶层；非法值拒绝未新增 | 正写入且仅选中档增加 |
| Chat/Responses 原生输入/读/写计数 | usage 子字段，整数 | 0与正数分开 | 缺失、负数、小数在本地故障响应中必须失败；读写各自≤输入 | 类型、非负、上界；冷写入>0；第二次读取>0、写入=0 |
| Chat MSH 两档响应头 | 两个十进制非负整数字符串；本补充要求均存在，无写入时为0 | selected>0/other=0；重复读取两者=0 | 缺失不补0；重复、非法字符、小数、负数、超2^53−1明确失败 | 解析指标后 `sum_equals` 严格对账 usage；通过 `cache_write_headers` 能力门禁 |
| Messages 创建量及两档拆分 | `cache_creation_input_tokens` 和两个 `cache_creation` 子计数，非负整数 | 0、正数与两档独立 | 原生input不含读写，不能直接用其作写入上界 | 两档之和=创建量；非选中档=0；读取不重写 |
| `stream` 与 Chat `stream_options.include_usage` | 可选bool；新流式定义均显式true | 流式/非流式分别定义 | Chat必须请求include_usage；缺最终usage、未终止流在模拟中失败 | 所有stream完成；Chat最终独立usage chunk恰好一次；非流式原生JSON |

所有新 Case 绑定 K3 / K3.1 Pro，执行层级 T3；矩阵中的故障响应是本地模拟验证断言强度，不是向 Provider 发送非法请求。

| 契约项 | 必要场景 / 预期 | Case 后缀（`kimi.cache.`） | 成本 / 证据 | 状态 |
| --- | --- | --- | --- | --- |
| 默认 TTL | 不传缓存选项，写入大于零；Chat 1h 头为零 | `write_default`、`write_default_stream` | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| 显式 5m/1h | 两档独立新前缀写入；Chat 非选中头为零 | `write_5m`、`write_1h`，各含 `_stream` | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| 写入 usage | 原生计数为非负整数，写入大于零且不超输入；保留原生 JSON | 六个写入 Case | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| MSH 写入头 | Chat 两档均必须提供合法非负整数；合计严格等于 usage 写入量 | 全部 Chat 定义 | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| 缓存读取 | 同一请求重复；读取大于零、无新增写入；Chat 两档头均为零 | `read_5m`、`read_1h`，各含 `_stream` | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| Responses MSH 头 | 官方页面用 Chat 举例；暂不把相同头在 Responses 必返作为硬契约 | 无 | 未确认端点契约 | deferred |
| TTL 过期、续期、锁定与切换 | 独立前缀跨时间观察，避免探测自身续期；不能用即时命中证明保留时长 | 无 | 需定时执行能力与获授权的 T3 时间窗口 | deferred |
| 组织隔离、块阈值 | 分组织账户、官方块阈值/可验证 tokenizer | 无 | 缺账户/明确阈值 | deferred |
| Messages TTL 拆分 usage | 两档非负整数之和等于创建量；冷写入非选中档为零；读取不重写；流式/非流式 | 全部 Messages 定义 | T3 / 静态与本地 HTTP/SSE 模拟 | implemented, live not run |
| 输入守恒 | 读与写分别不超总输入；尚未独立核验未缓存量及三者之和 | 全部定义的单项上界 | 无供应商未缓存量字段断言 | partial |

## 执行证据与兼容性

两个 MSH 数值头保存到现有 `metrics.msh_usage_cache_write_tokens_5m/1h`；各请求保存在 `exchanges[].metrics`，原始 usage 保持原状。零值与缺失分开：缺失不补零，重复、非整数、负数或超出精确数值范围的头产生协议问题，不持久化原始头值或其他响应头。沿用 `sum_equals` 和 `expected_from` 进行对账。

Chat SSE 另记录 `stream_final_usage_count`：只计 finish 之后、`choices=[]` 且含 usage 对象的独立 chunk。流式探针要求恰好一次最终用量，并拒绝 DONE 后数据和 finish 后新增内容；不能用早期 chunk 中的 usage 冒充最终 usage。该计数独立于原始事件证据的 256 条保存上限。

依赖上述指标的任务要求 runner 声明 `cache_write_headers` 能力；旧 runner 在提交前拒绝此类任务，普通旧任务不增加要求。Case 使用已有 schema 3，Suite 使用 schema 1；没有数据库字段、迁移链或报告结构变化。新增指标使用既有开放数值映射。旧 schema 2 Case、无这些头的旧证据和历史报告保持原义，无新增弃用或移除时间。旧程序不支持新用例的执行语义；降级时恢复新用例加入前的目录和匹配工作区备份，备份后的写入不在恢复范围。

共 10 个逻辑 Case、30 份协议定义，流式和非流式各 5 个 Case。三个独立套件名为 `Kimi 缓存 usage 对账 · openai-chat / openai-responses / anthropic-messages`，每套 10 个 Case；分别绑定 K3、K3.1 Pro 运行，不加入既有日常/基础套件。新写入探针验证 header 与 usage 的缺失、负数、小数、不一致、错 TTL、零写入；读取探针验证未命中、重复写入；流式还验证最终 usage 缺失和流未结束会失败。

验证已通过：缓存协议、runner 能力、Case/Suite 目录、初始化与 CLI 的定向 Go 回归；三份专项 manifest 的生成器 `--check`；用例设计技能的 7 项 Python 回归；`git diff --check`。Go 回归包含本地 HTTP/SSE、原生协议执行、断言、JSON 证据往返、旧 runner 能力拒绝与目录加载；同步了当前目录/套件数量夹具。本次不调用付费 Provider，不把模拟通过称为线上通过；TTL 生命周期仍不完整。
