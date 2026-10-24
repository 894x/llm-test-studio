# Kimi K3.1 Pro Chat Completions 契约覆盖矩阵

## 范围

- Provider / endpoint：Moonshot Kimi，`POST /v1/chat/completions`。
- 模型：`kimi-k3.1-pro` 与 `kimi-k3.1-pro-highspeed`；运行时绑定模型，Case 请求体不硬编码模型。
- 模式：非流式、SSE、文本、图片、base64 视频、结构化输出、Partial Mode、自定义/动态工具、隐式上下文缓存。
- 文档检索日期：2026-10-25。以附件中的 K3.1 Pro 指南及当日在线官方文档为准。
- 不包含 Responses / Anthropic 协议、Formula 官方工具和文件上传生命周期；`ms://` 仅保留为依赖真实账户文件的 deferred 场景，不伪造 file ID。
- 精确 1M 上下文、缓存到期/跨组织隔离、签名响应头、认证及速率限制属于设计内场景，但受成本、执行器或测试账户条件限制，分别标记 deferred / blocked。
- 本矩阵中的“covered”表示请求与稳定断言已实现，不表示已向付费 Provider 发请求。本次执行证据统一为 T0 static / live not run。

来源简称：`CHAT` = https://platform.kimi.com/docs/api/chat；`OPENAPI` = https://platform.kimi.com/docs/openapi.json；`MODELS` = https://platform.kimi.com/docs/api/models-overview；`REASON` = https://platform.kimi.com/docs/guide/use-reasoning-effort；`VISION` = https://platform.kimi.com/docs/guide/use-kimi-vision-model；`FORMAT` = https://platform.kimi.com/docs/guide/response_format；`PARTIAL` = https://platform.kimi.com/docs/guide/use-partial-mode-feature-of-kimi-api；`TOOLS` = https://platform.kimi.com/docs/guide/use-tool-choice；`DYNAMIC` = https://platform.kimi.com/docs/guide/use-dynamic-tool-loading；`CACHE` = https://platform.kimi.com/docs/guide/context-caching；`ERRORS` = https://platform.kimi.com/docs/api/errors。均检索于 2026-10-25。

| Contract area / path | Source | Type / presence / default | Valid partitions | Invalid / boundaries / dependencies | Expected result | Model / tier | Required scenarios | Existing cases | Design status | Execution evidence | Gap/status |
|---|---|---|---|---|---|---|---|---|---|---|---|
| HTTP method/path、Bearer、JSON | CHAT, ERRORS | POST；Bearer 与 JSON body 必填 | 有效凭据 | 缺失/错误凭据为 401 | 成功 200；错误含 `error.message` | both / T1 | 正常请求；无效认证由独立账户测试 | `k31.must.usage_non_stream` | 正常路径完整；认证场景 deferred | T0 static | partial |
| `X-Msh-Request-Nonce` 与签名响应头 | OPENAPI | optional string，minLength 1 | 非空 nonce | 非法值仍执行但不签名 | 合法时返回 timestamp/signature | both / T1 | 合法、空、重复头 | 无：执行器不支持自定义请求头/响应头断言 | blocked，需执行器能力 | not run | blocked |
| `model` | OPENAPI | required enum | pro、highspeed | 未知模型 404 | 响应模型与绑定目标一致 | both / T1 | 两模型分别运行同一 Suite | 全部 Case；Run 注入 | 完整 | T0 static | covered |
| `messages` | OPENAPI | required array | 标准/动态工具消息 | missing、empty、空标准 content | 非法请求 400 + error | both / T2 | 缺字段、空数组、空 content | `k31.messages_required`、`k31.messages_empty`、`k31.message_empty_content` | 完整 | T0 static | covered |
| `messages[].role/content/name/partial` | OPENAPI, PARTIAL | role enum；content string/array；name optional；partial=false | system/user/assistant/tool；尾部 assistant partial | 非法 role/type；partial 必须在尾部 assistant | 合法 200；非法 400 | both / T1/T2 | 普通文本、partial、name、工具结果、非法形态 | `k31.must.*`、`k31.partial_mode`；完整多轮重放受执行器限制 | 基础完整；完整重放 partial | T0 static | partial |
| `reasoning_effort` | REASON, MODELS | optional enum；default max；始终思考 | low/high/max | medium、wrong type | 三档 200；非法 400 | both / T1/T2 | omitted、low、high、max、medium | `k31.must.thinking_default`、`k31.reasoning_effort_*` | 完整 | T0 static | covered |
| `temperature` | MODELS | optional fixed 1.0 | omitted、1.0 | 0.99、1.01 | 固定值 200；其他 400 | both / T1/T2 | omitted/exact/below/above | `k31.param_omit_sampling`、`k31.param_fixed_temperature`、`k31.temperature_*` | 完整 | T0 static | covered |
| `top_p` | MODELS | optional fixed 0.95 | omitted、0.95 | 0.94、0.96 | 同上 | both / T1/T2 | omitted/exact/below/above | `k31.param_fixed_top_p`、`k31.top_p_*` | 完整 | T0 static | covered |
| `n` | MODELS | optional fixed 1 | omitted、1 | 0、2 | 同上 | both / T1/T2 | omitted/exact/below/above | `k31.param_fixed_n`、`k31.n_*` | 完整 | T0 static | covered |
| `presence_penalty` | MODELS | optional fixed 0 | omitted、0 | -0.1、0.1 | 同上 | both / T1/T2 | omitted/exact/below/above | `k31.param_fixed_presence_penalty`、`k31.presence_penalty_*` | 完整 | T0 static | covered |
| `frequency_penalty` | MODELS | optional fixed 0 | omitted、0 | -0.1、0.1 | 同上 | both / T1/T2 | omitted/exact/below/above | `k31.param_fixed_frequency_penalty`、`k31.frequency_penalty_*` | 完整 | T0 static | covered |
| `max_completion_tokens` | CHAT, OPENAPI | optional integer；default 131072；max 1048576 | 小值、最大值；超总窗口自动调低 | 1048577；输入自身必须 <1M | 合法 200；超限参数 400；达到预算 finish=length | both / T1/T2/T3 | 1、1048576、1048577、运行时截断、近 1M 输入 | `k31.max_completion_*`；精确近 1M deferred | 除近 1M 外完整 | T0 static | deferred |
| deprecated `max_tokens` | OPENAPI, PARTIAL | optional deprecated integer | 兼容旧字段 | 小预算可先耗尽思考 | 成功或 length，不断言新字段等价 | both / T1 | 旧字段 smoke、截断后续写 | `k31.must.*` 旧有用例、`k31.partial_mode` | 兼容覆盖，非推荐基线 | T0 static | covered |
| `logprobs` / `top_logprobs` | OPENAPI | bool default false；integer [0,20]，依赖 logprobs=true | false/true；0、20 | -1、21、缺依赖 | 合法 200 且结构存在；非法 400 | both / T1/T2 | omitted、true、0、20、-1、21、缺依赖 | `k31.logprobs_enabled`、`k31.top_logprobs_*` | 完整 | T0 static | covered |
| `stop` | OPENAPI | string 或 array；max 5；每项 max 32 bytes | 单字符串、5 项、32 bytes | 6 项、33 bytes；匹配词不返回 | 合法 200；非法 400；运行时截断 | both / T1/T2 | 1、5、6、32B、33B、运行时 | `k31.stop_*`、`k31.stop_runtime_truncation` | 完整 | T0 static | covered |
| `stream` / `stream_options.include_usage` | CHAT, OPENAPI | bool default false；include_usage default false | false/true；SSE usage | 非流式无 SSE；最终 usage chunk 在 DONE 前 | 200；`stream.completed=true`；usage 算术 | both / T1 | omitted、explicit false、true+usage | `k31.stream_explicit_false`、`k31.must.stream_*` | 完整 | T0 static | covered |
| `response_format.type=text` | OPENAPI | optional；默认 text | omitted、explicit text | 非法 enum 400 | 文本 200 | both / T1/T2 | omitted、text、invalid | `k31.response_format_text`、`k31.response_format_invalid_type` | 完整 | T0 static | covered |
| `response_format=json_object` | CHAT, FORMAT | type enum；prompt 应说明 JSON | 合法 JSON object | 非 object/无效 JSON 不满足业务断言 | 200 + content 为 JSON object | both / T1 | 合法 JSON Mode | `k31.must.json_object` | 完整 | T0 static | covered |
| `response_format=json_schema` 及 name/strict/schema | FORMAT, OPENAPI | json_schema 条件必填 name/schema；strict default true | 简单、嵌套/数组/anyOf | 缺 name/schema、schema 非 object | 合法 200 + schema 字段；非法 400 | both / T1/T2 | 简单、复杂、strict true/false、三个非法形态 | `k31.must.json_schema`、`k31.json_schema_*` | 完整 | T0 static | covered |
| `prediction.type/content` | OPENAPI | optional object；type=content；content string/array[text object] | string、text array | 非法 type | 200；非法 400 | both / T1/T2 | 两种合法形态、非法 type | `k31.prediction_*` | 完整 | T0 static | covered |
| `tools[]` 与 function.type/name/parameters/strict | OPENAPI | optional array；type=function；name regex 1..128；parameters required；strict default true | name 1/128；strict true/false | 数字开头、129；缺字段/非法 type | 合法 200 + tool call；非法 400 | both / T1/T2 | 名称边界、strict 两档、缺字段 | `k31.tool_name_*`、`k31.tool_strict_*` | 完整 | T0 static | covered |
| `tool_choice` | TOOLS, MODELS | optional enum default auto 或 function object | omitted/auto/none/required | 指定函数与始终思考不兼容 400；unknown enum | required 至少一次调用；none 无调用 | both / T1/T2 | 四合法行为、指定函数拒绝 | `k31.must.tool_choice_*`、`k31.must.tool_call`、`k31.optional.tool_choice_function` | 完整 | T0 static | covered |
| 工具调用响应与 tool result replay | CHAT, TOOLS | assistant tool_calls；tool_call_id 对应 | required 首轮、tool 次轮 | 完整 assistant 含 reasoning/tool_calls 原样回传 | finish=tool_calls；arguments JSON；最终文本 | both / T1 | 首轮调用、完整回放、最终结果 | `k31.must.tool_call` 首轮；动态完整多步 deferred | 首轮完整；多步执行器 gap | T0 static | partial |
| 动态工具 message.tools | DYNAMIC | role=system；无 content；完整 name/description/parameters；位置起生效 | 动态单独、与全局并存 | 带 content 400；缺 description 按指南拒绝但不与 OpenAPI 冲突断言 | 200 + 调用；非法 400 | pro only / T1/T2 | 单独、并存、带 content | `k31.dynamic_tool*` | 完整；highspeed 未明确支持，不纳入其 Suite | T0 static | covered |
| 多模态 content text/image_url/video_url | CHAT, VISION | content 必须 object array；各 discriminator/条件字段必填 | 对象与字符串简写；base64；ms:// | 缺 type/text/image_url/video_url/url；公网图片 URL、SVG 400 | 图片识别 7；视频数字序列；非法 400 | both / T2/T3 | 两形式、六缺字段、公网 URL、SVG、真实 ms:// | `k31.must.image_*`、`k31.must.video_*`、`k31.image_public_url_rejected`、`k31.image_svg_rejected`；ms:// deferred | base64 完整；文件引用 deferred | T0 static | deferred |
| 图片/视频 MIME、Body 100M | VISION | 图片 jpeg/png/gif/webp/bmp/heic/heif；视频 mp4 等；Body <=100M | 每类格式 | SVG、>100M | 支持格式 200；SVG/超限 400 | both / T3 | PNG 代表、SVG；全 MIME/100M 需资产与成本 | PNG Case、SVG Case；其余 deferred | 代表覆盖 | T0 static | deferred |
| `prompt_cache_key` / `safety_identifier` | OPENAPI | optional string | 稳定 session key、哈希用户 ID | 不发送 PII | 200 | both / T1 | 两字段独立接受 | `k31.prompt_cache_key`、`k31.safety_identifier` | 完整 | T0 static | covered |
| `prompt_cache_options.mode/ttl` | CHAT, CACHE | optional object；mode=implicit；ttl=5m/1h；default implicit+5m | omitted、implicit、5m、1h | 非法 mode/ttl；两档独立 | 合法 200；非法 400 | both / T1/T2 | default、mode、两 TTL、两非法值 | `k31.prompt_cache_options_*` | 完整 | T0 static | covered |
| 缓存前缀、读写 usage、TTL 生命周期 | CHAT, CACHE | 自动按块缓存；org 隔离；命中续期；不可主动清除 | 重复稳定前缀命中 | `prompt_cache_breakpoint` 400；不足块不写入 | 第二次 cached_tokens>0；用量不超 prompt_tokens | both / T2/T3 | 5m 命中、1h 命中、breakpoint、过期/续期/隔离 | `k31.cache_prefix_hit`、`k31.cache_ttl_1h_hit`、`k31.prompt_cache_breakpoint`；时间/隔离 deferred | 即时命中完整；生命周期 deferred | T0 static | deferred |
| 非流式响应 id/object/created/model/choices/message/finish_reason | OPENAPI | response object | stop/length/tool_calls | content 可空但业务 Case 要按模式断言 | 字段类型正确、usage total=prompt+completion | both / T1 | 普通、截断、工具三类 | `k31.must.*`、`k31.max_completion_*` | 完整 | T0 static | covered |
| SSE delta/reasoning/content/tool_calls/final usage | CHAT, OPENAPI | chunk stream；reasoning 先于 content | 文本与工具增量 | DONE 后无数据；finish 后无 content | 完整终止、ID/model 一致、usage 存在 | both / T1 | 文本流、工具流 | `k31.must.stream_*`；工具流由 tool Case 非流式，stream 工具组合 deferred | 文本完整 | T0 static | partial |
| HTTP 400/401/404/429/5xx | ERRORS | `error.message` required，type/code optional | 各错误类型 | 速率与额度依赖账户 | 负向 Case 400 + 非空 message | both / T2/T3 | 参数 400 已覆盖；401/404/429/5xx 独立环境 | `k31.*` 参数拒绝；其余 deferred | 参数错误完整 | T0 static | deferred |

## 汇总与限制

- 设计采用等价类、固定值测试、数值/字节/数量边界、依赖决策表和顺序工作流。
- T0 仅做 schema、加载、Suite 引用、Plan 引用与生成器一致性校验；T1/T2/T3 均未执行。
- 旧 K3 的非官方 `enable_thinking`、`thinking`、`chat_template_kwargs` 探针不进入本目录。
- 旧 K3 的 `max_completion_tokens=1048576` 400 预期与当前 K3.1 Pro 文档冲突；新 Case 按“自动调低并继续”改为 200。
- 旧 K3 缓存工作流中的 `temperature=0` 与固定 `1.0` 冲突；新 Case 显式使用 `1.0`。
- 精确 1M、全 MIME/100M、真实 `ms://`、缓存到期/隔离、工具流式组合、完整多轮/工具结果回放、签名头、认证/限流仍需相应资产、执行器或授权，不能宣称完整边界覆盖。
