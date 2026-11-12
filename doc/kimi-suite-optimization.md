# Kimi K3 / K3.1 套件选择与兼容性

本次整理覆盖仓库内的 Kimi K3 Chat、K3 Responses，以及 K3.1 Pro / Highspeed Chat 套件；K3.1 Responses 和其他供应商协议不在本次范围。模型、渠道和凭据由运行时选择，套件本身不会限制或自动选择模型。

## 推荐入口

| 目的 | 入口 | 范围与注意事项 |
| --- | --- | --- |
| 连通性 | 对应模型和协议的连通性套件 | 包含 1 或 3 个 Case；日常套件已包含这些 Case。 |
| 日常检查 | Kimi K3 Chat 日常检查 | 7 个短请求，覆盖基础响应、流式、用量、JSON、工具及 `reasoning_effort=low`。 |
| 日常检查 | Kimi K3.1 Chat 日常检查（Pro / Highspeed） | 同一组 7 个 Case；分别选择 Pro 或 Highspeed 运行，不含动态工具、多模态和缓存。 |
| 日常检查 | Kimi K3 Responses 日常检查 | 6 个 Case；部分请求的最大输出预算为 2048 tokens。使用支持 Responses 的模型和渠道。 |
| 图片与视频 | Kimi K3 Chat 多模态套件、Kimi K3.1 Chat 多模态套件（Pro / Highspeed） | 分别包含 10 / 12 个既有 Case，含识别结果和异常输入检查；不含缓存及大输出边界。 |
| 完整回归 | K3 Chat 官方基础、K3 Responses 官方基础、K3.1 对应模型完整设计 | 包含日常或专项场景，且可能包含图片、视频、缓存及大输出预算，运行前检查用例与费用；名称不表示官方契约全部覆盖。 |
| 按需专项 | 参数拒绝、扩展、KVV | 用于定位特定能力；与完整套件有交集，连续运行会重复请求。KVV 观察项包含未独立确认的行为，不能直接视作官方契约。 |

Highspeed 的既有完整套件排除三个仅适用于 Pro 的动态工具场景，共用日常套件也不包含这些场景。Pro 和 Highspeed 共用 Case 定义不代表一次运行能够验证两个模型：需要分别选择模型执行。不同协议继续使用各自的请求和断言。

## 多模态专项套件与 Pro 回归计划（2026-11-11）

本次范围为 Kimi `POST /v1/chat/completions` 的 `kimi-k3`、`kimi-k3.1-pro` 和 `kimi-k3.1-pro-highspeed` 既有非流式图片、视频用例的独立套件入口，以及 Pro 基础与参数回归计划引用。只复用现有固定素材、请求和断言，不新增 Case，不扩展为多模态全参数验收；流式、多图、多视频、文件上传／`ms://`、格式／大小上限、Responses 和 Messages 不在此次目录整理范围。2026-11-11 核对的[官方 Chat 文档](https://platform.kimi.com/docs/api/chat)说明 `content` 数组通过 `type` 区分文本、图片、视频，图片／视频地址支持对象 `url` 与字符串形式。

成员按以下既有主张选择；`must.*` 用于 K3，`k31.must.*` 用于 K3.1，后两项拒绝检查仅用于 K3.1：

| 请求区域／形状 | 既有 Case key | 请求与通过条件 | 成员数（K3 / K3.1） | 层级与限制 |
| --- | --- | --- | ---: | --- |
| `content[].image_url` 对象／字符串 | `must.image_url_object`、`must.image_url_string` | 内置 PNG；HTTP 200 且提取数字等于 `7` | 2 / 2 | T3 按需；仅证明固定素材识别和这两种输入形状 |
| `content[].video_url` 对象／字符串 | `must.video_url_object`、`must.video_url_string` | 内置 MP4；HTTP 200 且提取数字顺序等于 `374` | 2 / 2 | T3 按需；仅证明固定视频数字顺序识别 |
| `content[].type`、`text`、`image_url`、`video_url` 及两种对象的 `url` 必填 | `must.multimodal_type_required`、`must.multimodal_text_required`、`must.multimodal_image_url_required`、`must.multimodal_video_url_required`、`must.image_url_object_url_required`、`must.video_url_object_url_required` | 每项独立缺少一个必填字段；HTTP 400 且 `error.message` 非空 | 6 / 6 | T2；不代表全部错误类型或消息顺序边界 |
| 图片地址／MIME 的既有拒绝预期 | `k31.image_public_url_rejected`、`k31.image_svg_rejected` | 公网 URL 或 SVG data URL；HTTP 400 且 `error.message` 非空 | 0 / 2 | T2；保留已有预期，不增加供应商实测证据 |

共 10 / 12 项全部为已启用的自动用例，单独 Case 的非默认状态保持。完整基础／扩展套件仍保留这些成员，连续运行会重复请求。它们是固定样本与形状检查，不是通用视觉能力评分，也不是图片／视频契约的完整边界覆盖；文件引用、其他素材格式、大小／数量边界和流式组合仍未由本专项验证。

`Kimi K3.1 Pro 基础与参数回归计划` 保留名称和身份，由 r2 更新为 r3，在原基础、参数拒绝两项之后追加 `kimi-k3.1.multimodal`。三项均使用 `single` 模式，各 Case 执行一次；新增 12 个不重复 Case，不引入扩展套件中的缓存和大输出边界，但新运行的图片／视频请求会增加费用。Highspeed 的现有计划保持原成员，可单独选择共用多模态套件并绑定 Highspeed 模型。

随后 r4 在这三个套件项之后追加既有 `kimi.partial`（「Kimi · Partial 综合回归」），当时共四项。新增项含 10 条 JSON 格式检查（8 条 Partial、2 条对照）及 8 条 KVV 续写格式检查，共 18 条，沿用 `single` 模式逐条执行一次。请求、断言及前三项身份和顺序保持；后续新运行会增加对应调用费用，需要支持 `json_prefix_assertions` 的执行端。格式通过不代表语义或隐藏推理续写正确，本次仅做本地目录、调度和生成一致性验证，未增加供应商实测证据。

2026-11-12 的 r5 保持原计划 ID，改名为「Kimi K3.1 Pro 完整准入计划」，再追加 `openai-chat.kimi.cache`（「Kimi 缓存与 TTL 综合对账」）。当前顺序为基础、参数拒绝、多模态、Partial、缓存与 TTL，共五项，均以 `single` 逐用例执行一次。缓存新增 20 条 Case，含读取序列共 28 次请求；原四项及其他计划保持，新增调用费用随新运行产生。用户覆盖不自动改名或追加，历史名称与报告不回写；降级引用限制见[兼容性说明](compatibility-policy.md)。名称不代表供应商全部契约均已覆盖，Partial 语义和 TTL 实际过期等限制仍见各专项说明。验证仅含本地目录、调度和资产保持检查，未执行供应商请求。

两个新套件由 `data/suites/openai-chat/kimi-multimodal.suite-profiles.json` 保存显式、按序的稳定 Case ID 引用，经场景生成器输出。所有既有 Case 请求、断言、ID、旧套件成员及计划前两项身份保持；用户同 ID 计划覆盖继续优先，已保存的覆盖不会自动追加本次内置项。历史快照和报告不改写，无数据迁移、弃用或移除安排。降级到缺少新套件的版本前，用户另存计划应先移除新增引用或提供对应 Suite 文件；旧二进制自带的 Pro 计划仍是原成员。

验证范围为 T0：生成器预览／写入／一致性检查、目录加载和 Suite 引用、计划逐用例调度及现有协议／断言回归。供应商请求未执行，现有覆盖矩阵的缺口和文档冲突继续有效。

## 工具场景输出预算（2026-11-11）

本次复核限定为 K3 Chat、K3 Responses、K3.1 Pro / Highspeed Chat 及已有 KVV 工具专项中的已启用、预期 HTTP 200 的工具相关场景，包含工具选择、动态工具、名称与 Schema、工具历史回放和请求接受检查。42 个 Case 文件中的 49 份协议定义统一使用 4096 tokens 的输出预算：45 份从原有 200、2048 或 2560 提高至 4096，另 4 份保持原有 4096：

| 定义范围 | 数量 | 调整字段 |
| --- | ---: | --- |
| K3 Chat（含保留的独立工具调用 Case） | 9 | 原有 `max_tokens` 或 `max_completion_tokens` |
| K3.1 Pro / Highspeed 共用 Chat | 12 | 原有 `max_tokens` 或 `max_completion_tokens` |
| KVV 同步 / 流式工具成功路径 | 6 | `max_completion_tokens` |
| K3 Responses 工具成功路径 | 22 | `max_output_tokens` |

2026-11-11 核对的[模型参数说明](https://platform.kimi.com/docs/api/models-overview)指出，三款 K3 模型始终思考，默认 `reasoning_effort=max`；[Chat API](https://platform.kimi.com/docs/api/chat)说明生成达到上限时会以 `length` 结束，并推荐以 `max_completion_tokens` 替代已弃用的 `max_tokens`。4096 是本项目为工具场景预留的生成预算，不是供应商推荐的固定值，也不保证所有请求都不会截断；实际输出可提前结束。此次保留每个 Case 原有字段及推理档位，继续覆盖旧字段兼容行为，避免同时发送两个预算字段。

参数拒绝、故意截断、输出上限边界和禁用场景保留各自预算与断言。Case schema 2/3、Suite schema 1、稳定 ID、key、套件成员和断言均保持，已有公开 CLI、API、runner 和存储格式无需改变，不新增弃用或移除期限，也无需数据迁移。新运行会使用统一后的内置预算，上调的场景最坏输出量和费用上限相应增加；其他模型复用这些 Case 时也会使用新预算。同 ID 用户覆盖继续优先，历史冻结快照、报告及结论不回写或重算。降级使用旧二进制自带的预算，数据不需要转换。

验证已通过：`data/cases`、`data/suites`、`internal/casecodec`、Chat / Responses 协议测试，4 份 manifest 对应的 27 个 Suite 生成一致性检查，以及逐字段差异检查和 `git diff --check`。1057 个未选中 Case 文件与修改前一致；对照复核前基线，40 个 Case 文件中的 45 份定义仅改变预算值，其余 4 份目标定义保持原值。证据限定为 T0 本地验证，本次没有发送供应商请求。既有覆盖矩阵中的 partial、deferred、blocked 和文档冲突继续有效，此调整不增加契约覆盖或供应商实测证据。

## 成员变化和已有引用

- K3 Chat 官方基础套件删除与 `must.tool_choice_required` 的 Chat 请求和断言完全一致的 `must.tool_call` 引用，成员由 86 调整为 85。`must.tool_call` 及其 Responses 定义仍然保留。
- K3.1 Pro / Highspeed 的基本功能及完整设计套件，删除与 `k31.must.tool_choice_required` 请求和断言完全一致的 `k31.must.tool_call` 引用；前者 Case 文件与 ID 保留，已有直接引用仍可使用。相应基本套件由 41 / 39 调整为 40 / 38，完整套件由 93 / 90 调整为 92 / 89。
- K3 Responses 基础及预期成功套件，删除与 `k3.responses.input_string` 请求和断言完全一致的 `k3.responses.cache_options_omitted` 引用；该 Case 文件与 ID 保留，基础 / 预期成功套件由 230 / 61 调整为 229 / 60，参数拒绝与禁用扩展仍为 169 / 23。省略缓存选项的成功请求无法证明默认 TTL 为 5m；其名称已明确这一限制，真实默认行为仍需单独验证。
- 原有套件的协议、key 和稳定 ID 均保留，历史 Case、Suite、Plan、快照与报告无需数据迁移。已保存的历史报告保持原结论；再次运行引用上述内置套件的计划时，套件将使用更新后的成员，因此新报告的用例数会减少 1。套件描述变更也会产生新的内容修订。
- 保留 `k3.stop_runtime_truncation` 和 `k31.stop_runtime_truncation` 的独立身份，避免改变既有模型专用套件中的展示及引用。KVV 的相同 Case 继续共用；两个模型的独立套件入口保留，以便分别绑定模型。
- 未弃用或计划移除旧 Case / Suite，现有用户编写文件不被改写。不存在新的格式版本或数据库迁移；降级到旧二进制会恢复旧内置成员，依赖新增日常套件的计划需要先改回旧版本支持的套件。现有已归档报告不受影响。

这些目录层面的调整通过本地静态检查验证，不代表新增供应商实测。已有能力矩阵中的缺口、禁用 Case、供应商文档冲突和高成本前置条件仍然有效。
