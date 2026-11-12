# Kimi 缓存写入与用量对账

## 分项结论与缺少 TTL 证据

现有 12 个 Case 的三协议 JSON/SSE 定义分别输出 `cache_io`（缓存读写）和
`cache_ttl`（TTL 对账）两个断言组。首轮写入、后续命中、禁止重写、等待及同请求
属于读写组；分项来源、数值、请求档位和来源一致性属于 TTL 组。

只有 TTL 证据不完整时，`ttl_*_evidence_required` 通过显式
`failure_status: "indeterminate"` 记录未验证，报告以黄色“TTL 证据不足”显示；
读写组单独显示其原生结论。不能因此把整条 Case 算为通过，也不计入断言失败数。
写入错误、未命中、部分命中、错档、负数、非法类型及来源冲突仍失败。缺少或空的
body 分项可由完整 header 佐证；没有任何完整来源才是证据不足。所有已返回的值
仍独立校验，不能选择另一来源掩盖错误。

报告列表、套件/Case 汇总、渠道对比和导出沿用 `indeterminate`，不以 `passed=false`
替换为失败。旧报告没有这两个 authored 组时不推导分项；本次用户指定的现有
对比在备份后按新规则重评，其他历史不自动改变。

每一步的证据详情依次展示请求头、响应头、请求体、响应体。新功能任务在执行器
声明 `http_header_evidence` 能力时冻结同名标记，JSON/SSE 在 Go 传输边界采集，
写入现有 Exchange 的可选 `request_headers/response_headers`，保存 `values` 和
可选 `truncated`。鉴权、密钥、cookie 等头脱敏，已知密钥回显和签名 URL 查询串
清除；限制头数量和长度。未采集与已采集空映射保持区别，旧记录显示“未记录”，
不会从 TTL 指标补造完整头。新断言策略需要 `assertion_failure_status` 能力，
升级及降级限制见 [兼容性说明](compatibility-policy.md)。

## 当前规则：header 与 usage 两种 TTL 证据

范围为现有综合对账 10 Case 与延迟验证 2 Case，三协议 JSON/SSE，目标为
`kimi-k3` / `kimi-k3.1-pro`；来源于 2026-11-12 核对的
[官方缓存文档](https://platform.kimi.com/docs/guide/context-caching)。下文早期章节的
MSH 必返、首轮零读取必返及仅 SSE 可省略零写入限制由本节替代。

| 协议 | 原生写入总量 | TTL 证据 |
| --- | --- | --- |
| Chat | `usage.prompt_tokens_details.cache_write_tokens` | 两档 MSH 头，或顶层/details 的 `cache_creation` |
| Responses | `usage.input_tokens_details.cache_write_tokens` | 两档 MSH 头，或顶层/details 的 `cache_creation` |
| Messages | `usage.cache_creation_input_tokens` | `usage.cache_creation`，或兼容提供方的两档 MSH 头 |

body 扩展、Messages 的 header 回退及零字段省略属于兼容验收，不是所有提供方的
官方必返约定。请求、Case/Suite/Plan 身份、12 Case / 18 请求预算、流终态、
输入/输出/读写上界、360 秒等待、同请求及延迟命中量等于首次写入的要求保持。

每次响应须有一套可核对的分项：完整头含两档非负整数；body 为 object，正写入
选中档明确等于总量，非选中零档可省略。显式零写入时至少返回一个零档，另一档
可省略；空 object 不是独立证据。有完整 body 时允许头部分缺失，返回的头仍校验。
每个 body 位置和所有已返回头独立校验，任何类型错误、负数、小数、重复头、
错档、错和或来源冲突均失败。只有总量时 `ttl_*_evidence_required` 记录未验证，
表示 TTL 证据不足，不从请求参数推算。

Chat/Responses 首轮读取可确实缺失，但须有显式正写入，原始 usage/指标不补零；
字段存在时仍校验整数、非负和上界，延迟首轮明确非零读取仍失败。后续读取必须
明确为正。命中轮零写入可省略，但须有两档零头或 body 两档显式零佐证；不能仅凭
命中或单个零档推断。Messages 原生总量和读取仍必返，null/字符串不视为省略。

本次按用户明确要求，对指定的已完成 Kimi 对比以保存响应重评，先做一致备份。
这是限定任务 ID 的一次性数据修复，其他历史、用户覆盖及在途任务不自动迁移。
原始执行输入、请求、响应、时间、指标及 ID 保留；重评结果标记规则版本，不发送
新供应商请求。Case/runner/数据库格式不变，无格式迁移或弃用。

## 2026-11-12 延迟流式读取省略零写入字段

`kimi.cache.ttl.read_1h_after_6m_stream` 的 Chat/Responses 第二次读取，沿用
即时读取用例的条件兼容规则：`cache_write_tokens` 确实缺失、`cached_tokens > 0`、
两档 MSH 写入头齐全且均为 0 时，允许按零写入验收。只影响断言，不给原始 usage
补字段。字段存在时继续校验整数、非负、上界、header 对账与零重写。

首次冷写仍必须返回显式正写入且读取为 0；360 秒静默等待、相同请求体、延迟读取量
等于首次写入量、TTL 分项一致及流终态检查全部保留。Messages 与非流式规则保持。
缺头、写入头非零、未命中、部分命中、TTL 矛盾，以及 null、字符串、负数或小数
不能通过此例外。此规则属于目标提供方观测兼容性验收，不扩大为通用官方契约。

来源是 2026-11-12 22:41–22:47 的真实 Chat 运行
`a4e0c09f-397d-4f93-9c91-b4c8a3328195`：两种模式均在约 360 秒后读取 3584
token，等于首次写入；SSE 的第二次 usage 省略写入字段，两档头及 TTL 写入分项
均为 0。新规则的本地 HTTP/SSE 故障回归通过，记录响应的只读离线复核也通过。
未重放付费请求，旧报告仍保留原失败结论；新规则用于后续新运行。

## 2026-11-12 缓存与 TTL 验证计划

新增内置「Kimi 缓存与 TTL 验证计划」，ID 为
`c6c6b79c-1f26-4b4e-9716-fb121b3110b9`，协议为 OpenAI Chat。
运行时选择 K3 / K3.1 Pro 对应模型、渠道和凭据，计划不保存连接绑定。

| 顺序 | 套件 | Case 数 | HTTP 请求数 |
| --- | --- | ---: | ---: |
| 1 | 缓存与 TTL 综合对账 | 10 | 14 |
| 2 | 1h 缓存延迟验证 | 2 | 4 |

两项均为 `single`，每条 Case 一次；共 12 条互不重复的 Case、18 次 HTTP 请求。
不预热，无耗时 SLA，第一项失败后继续第二项。单次 HTTP 超时为 600000 ms，
延迟工作流的 360 秒等待另计；Case 并发由启动时的配置决定。延迟项在并发 1 时
至少等待 12 分钟，并发至少 2 时至少等待 6 分钟，另加请求耗时。

计划沿用固定种子 894 的可复现生成规则；同种子、同计划项重跑会复用随机前缀，
需要独立冷写样本时应更换种子或等待旧缓存过期。延迟用例仍要求首次读取为 0。
本计划仅提供按需入口，不自动执行；只复用已存在的 Case/Suite，不增加供应商
契约覆盖，不证明完整 1h 保留。本次验证限于本地计划加载、引用、请求预算和调度。

## 2026-11-12 精简可选套件入口

移除内置列表中的「缓存 usage 对账」和「缓存响应头对账」三协议入口。
当前只提供以下两类，共六个协议入口：

| 套件 | 每协议 Case 数 | 每轮请求数 | 用途 |
| --- | ---: | ---: | --- |
| `<protocol>.kimi.cache` | 10 | 14 | 同次响应核对 usage/header，并检查即时重复读取 |
| `<protocol>.kimi.cache-ttl` | 2 | 4 | 冷写 1h 后静默等待 6 分钟，原样读取并与首次写入量比较 |

三份生成 manifest 只保留这两类，旧入口不会被重新生成。Case 文件和现有准入
计划不删除或改写。六份旧 schema 1 Suite 转为 `data/suites/compatibility/`
中的只读兼容文档，列表和计数不包含它们；旧计划仍按发布过的原 ID 解析原成员
和输入绑定，usage 旧引用不会被悄悄切换为更严格的 header 检查。
用户自己保存的 Suite 覆盖继续生效，不自动删除；新建任务选择上表入口。

本节替代下文关于旧套件继续作为可选入口的历史说明。支持范围、退出条件和降级
见[兼容性说明](compatibility-policy.md)。验证仅含本地生成、目录、旧 ID 解析、
用户覆盖及桌面初始化回归，没有供应商调用。

## 2026-11-12 流式读取的零写入字段省略

仅调整 `kimi.cache.headers.read_5m_stream` 和
`kimi.cache.headers.read_1h_stream` 两个逻辑 Case 的 Chat/Responses 定义。
首轮或重复轮的同次响应在 `cached_tokens > 0`、两档 MSH 写入头均存在且为
0 时，允许 `cache_write_tokens` 缺失，按 0 验收。此规则是用户指定的目标渠道
兼容性验收，依据本次报告观测，不扩大为所有供应商的官方字段约定。

| 边界 | 验收规则 | 本地回归 |
| --- | --- | --- |
| 命中且缺失写入字段 | 读取量为正、两档头均为 0，才允许按零写入通过 | 重复轮省略、首轮已命中且两轮省略 |
| 未命中或存在新写入 | 不能用字段省略替代写入计数或掩盖重写 | 未命中、写入头为正时省略均失败 |
| 字段已返回 | 保留整数、非负、上界、usage/header 对账和重复轮写入为 0 的校验 | null、字符串、负数、小数、重写继续失败 |
| 其他证据 | 保留输入/输出、缓存读取、TTL 分项、两档头完整性及流终态校验 | 缺头、TTL 矛盾及既有故障继续失败 |

Messages 原生 `cache_creation_input_tokens`、其他 Case 和非流式定义保持原规则。
保留已发布 v0.2.0 的 Case 身份、请求、工作流及 Suite/Plan 引用；只影响新运行
使用的内置定义，用户覆盖、冻结快照和历史报告不改写或重算。文件、runner 和
数据库格式不变，无迁移、弃用或移除安排；降级使用旧版内置断言，无需数据转换。
验证使用本地 HTTP/SSE fixture 和保存后重判，不发送真实供应商请求。
下文各历史章节的字段必返规则以本节限定的例外为准。

## 2026-11-12 多步骤延迟验证

新增三协议独立入口 `<protocol>.kimi.cache-ttl`，名称为「Kimi 1h 缓存延迟验证」。
每个入口有 JSON/SSE 两条非默认 Case，共 4 次请求；顺序至少 12 分钟，并发至少
6 分钟。已有综合套件和准入计划不自动加入这些长耗时用例。
如手动加入带耗时 SLA 的计划，需要把 6 分钟等待计入 E2E 预算。

每条先以唯一随机前缀冷写 1h，完整接收响应后静默等待 360 秒，再用
`reuse_request_from: "initial"` 原样重发。要求首次读取 0、写入为正且落在 1h 档，
延迟读取量**等于首次写入量**且不重写；同时检查请求体相等、实际等待量、usage
与 header 逐档一致及 JSON/SSE 协议完整性。等待期间没有命中探测，避免刷新 TTL。

本地使用虚拟时钟和三协议 HTTP/SSE fixture 验证执行与故障断言，没有发送供应商
请求。这些用例只证明超过 5 分钟仍可复用，不证明完整 1 小时。上游未返回缓存 ID，
不能仅凭相同前缀和相同 token 数认证物理缓存身份。

新字段、超时、取消与 runner 要求见[多步骤 Case](multi-step-cases.md)；范围见
[覆盖矩阵](../data/cases/kimi-cache-ttl/coverage-matrix.md)。以下历史章节所述
「未验证跨时间保留」适用于当时的即时重复用例，长耗时入口提供了独立执行能力。

## 2026-11-12 同次响应合并 usage 与 header 验收

`<protocol>.kimi.cache` 的 Chat、Responses、Messages 三个综合入口由 20 条缩为
10 条 Case：默认/5m/1h 写入及 5m/1h 重复读取，各含 JSON/SSE。每条在同次响应
上同时校验 usage 和 MSH header，不再为两组检查重复发请求；一轮从 28 次降为
14 次请求。Chat 准入计划的第五项仍引用该入口，计划本身无需改版。

合并复用原 `kimi.cache.headers.*` 的稳定 ID/key，补齐对应 usage 的输入、输出、
缓存读写计数、上界和 Messages 原生 TTL 拆分断言，并保留严格的 header 与流终态
校验。`usage.total_tokens` 继续不参与验收。

| 同次响应的验收项 | 当前规则 | 本地验证 |
| --- | --- | --- |
| 两档 MSH header | 均存在、合法非负整数；两档之和等于该请求 usage 缓存写入总量 | 缺失、负数、小数、重复、错和失败 |
| TTL 分项一致 | usage 返回的 5m/1h 分项分别等于对应 header；Chat/Responses 同时返回顶层及 details 时均校验 | 分项错值、错档、错类型失败；Messages 原生拆分不得缺失 |
| 冷写入及读序列 | 写入为正，选中档等于 usage 写入量、另一档为 0；读序列首次请求也核验档位；第二次读取为正且写入及两档 header 均为 0 | 首次错 TTL、第二次 header 或 usage 分项不一致失败 |
| 原生 usage | 输入、输出、缓存读写为非负整数；保留原有适用上界 | 缺输入、缺输出、输出小数、负读取、读写超过输入失败 |
| 流式及证据 | 使用最终 usage，正常终止，保存后可重判同一断言 | 既有 HTTP/SSE 故障及 JSON 往返回归 |

范围仍为 `kimi-k3` / `kimi-k3.1-pro`，按需 T3。2026-11-12 复核的
[官方缓存文档](https://platform.kimi.com/docs/guide/context-caching)明确了三协议
usage 字段及 Messages TTL 拆分，并以 Chat 为例说明 MSH 两档响应头。
Responses/Messages 必返相同响应头及 Chat/Responses 扩展 TTL 分项属于用户
要求的目标提供方兼容性验收，不扩大为通用供应商契约。
Chat/Responses 扩展 TTL 对象可省略；返回时必须逐档与 header 相等，省略零档
只在对应 header 为 0 时接受。实际 TTL 过期、跨时间保留和续期仍未验证。

v0.2.0 两类独立 Suite 的路径、key、身份和成员继续保留；usage 独立入口及其
Case 定义不变，响应头独立入口复用增强后的合并 Case，因此新运行也执行补齐的
usage 检查。用户覆盖、冻结快照、历史报告及其结论不改写；Case/Suite/Plan、
runner、CLI/API 与数据库格式不变，无数据迁移、弃用或移除安排。
降级使用对应旧版内置定义；对综合入口的旧版引用限制沿用下文说明。

验证为本地 HTTP/SSE、目录加载、三协议各 10 条唯一成员与 14 次请求、manifest
生成一致性和差异检查，未执行真实付费供应商请求。下文保留此前分组及串行运行的
历史记录，其中 20 条/28 次和分离验收的综合入口描述由本节替代。

## 2026-11-12 移除总 token 数验收

本次只调整 `kimi-k3` / `kimi-k3.1-pro` 缓存专项的 Chat Completions 和 Responses
定义，覆盖默认/5m/1h 写入及 5m/1h 重复读取、JSON/SSE。移除 `usage.total_tokens`
的整数、非负和输入加输出相等断言；缺失、格式错误或数值不一致均不影响缓存用例结论。
输入、输出、缓存读写计数、TTL 拆分、MSH 头和流终态的既有验收继续适用，写入
Case 的缓存创建字段仍须存在且大于 0；本次不放宽重复读取不重写等其他条件。

| 验收项 | 当前规则 | 范围及验证 |
| --- | --- | --- |
| `usage.total_tokens` | 不参与验收，不校验存在、类型、非负或输入加输出关系 | 10 个 usage Case 的 Chat/Responses 定义；本地 HTTP/SSE 验证缺失、负数、错误类型及错和可通过 |
| 缓存创建量 | 沿用存在、非负整数及写入 Case 大于 0 等既有规则 | 缺失、负数、小数、零写入故障继续失败 |
| 其他缓存及协议规则 | 沿用下文原规则 | 原有故障回归继续执行；未发送真实供应商请求 |

字段来源仍为[官方缓存文档](https://platform.kimi.com/docs/guide/context-caching)
（2026-11-12 核对）；移除总量验收是本项目的测试范围选择。下文日期章节保留
调整前的验收记录；其中 `total_tokens` 的严格要求已由本节替代。

保留已发布 v0.2.0 Case/Suite 的身份、引用、请求、工作流和文件格式，只影响
新运行使用的内置定义。用户覆盖、冻结快照和历史报告不改写或重算；无数据迁移、
弃用或移除安排。降级使用旧版内置断言，既有用户数据无需转换。

## 2026-11-11 统一运行入口

新增「Kimi 缓存与 TTL 综合对账」，Chat、Responses、Messages 各一个协议入口，
key 为 `<protocol>.kimi.cache`。每套先运行原 usage 对账的 10 条 Case，再运行
原响应头对账的 10 条 Case，共 20 条不同的 Case；沿用已有 schema 1、显式稳定
Case ID 和空输入绑定，由各协议的 `kimi-cache-usage.suite-profiles.json` 生成。

范围仍为 `kimi-k3` / `kimi-k3.1-pro`，默认/5m/1h 写入、5m/1h 重复读取，覆盖
JSON 与 SSE。本次只合并已有成员，不改变请求、工作流、断言或模型适用性；
沿用 2026-11-09 的契约清单和覆盖缺口，不新增供应商契约主张或实测证据。
两组结果继续独立：usage 对账不依赖 MSH 头，响应头对账仍要求两档 MSH 头
完整、合法且与同次 usage 一致。Responses/Messages 的必返头属于目标提供方
兼容性扩展验收，不因此成为通用缓存要求。

统一运行仍为按需 T3；全部执行会产生原两组各自的请求费用。20 条 Case 的
读取序列各含两次请求，因此一轮共 28 次协议请求；不会合并请求或复用另一组
的响应。TTL 实际过期、跨时间保留与续期仍未覆盖。Chat 入口已作为第五项加入
「Kimi K3.1 Pro 完整准入计划」（原基础与参数回归计划，r5），其他四项身份、
顺序和配置保持，各 Case 执行一次。该名称不代表完整供应商契约覆盖。

v0.2.0 已发布的两类独立 Suite（每类三协议）保留原路径、key、身份及成员，
已有计划、用户覆盖和直接 Suite 引用继续有效；需要合并运行时选择新入口。
没有弃用或移除期限，不改用户文件、冻结快照或历史报告，不重算历史结论。
Case/Suite/Plan、CLI/API、runner 和数据库格式不变，无数据迁移。
降级使用旧版自带目录；另存计划若引用新入口，旧版需移除该引用或提供新 Suite
及已有 Case，且执行端仍须支持原有能力门禁，无需数据库恢复或格式转换。

验证限定为 T0：三份 manifest 的生成一致性、20 条按序且唯一的引用、旧 Suite
及 Case/Plan 文件保持、目录及输入绑定、桌面初始化与重复打开、已有缓存
HTTP/SSE 本地故障 fixture。未发送真实供应商请求；下文保留此前拆分的证据。

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
