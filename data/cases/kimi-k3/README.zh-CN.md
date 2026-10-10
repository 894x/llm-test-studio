# Kimi 官方多模型基础用例

四个 Kimi Chat 基础套件选择 `openai-chat` 协议；Kimi 名称标识模型。K3 另有原生 `openai-responses` 连通性、基础与参数拒绝 Suite。Responses 基础套件复用现有 84 个 K3 Case 中的 39 个 ID；其余 45 个 Chat 场景没有文档对应项，或缺少稳定的原生预期。5 个共用文本 Case 还配置了 `anthropic-messages`。运行时需绑定支持所选接口的模型和渠道；系统不自动转换协议。

Responses 配置依据 2026-10-09 查阅的 [Kimi Responses API](https://platform.kimi.com/docs/api/responses) 和官方 OpenAPI。按用户要求，`tool_choice` 预期参考现有 K3 Chat Case，以及 2026-10-10 查阅的[工具调用约束](https://platform.kimi.com/docs/guide/use-tool-choice)：省略或 `auto` 允许模型自行选择，`none` 要求文本回复且不调用工具，`required` 要求至少一次工具调用。指定函数选择保留 Chat 在思考开启时的拒绝预期。Responses Schema 仍只声明 `auto`；这些参考 Chat 的预期尚未经过 Responses 上游实测。推理使用 `reasoning.effort`，结构化输出使用 `text.format`，省略 Schema 的 `name` 会被接受。图片字段为 data URL 字符串，缓存工作流校验原生输入、缓存读取及缓存写入用量。全部 84 个场景的映射、完整字段清单和剩余缺口见 [responses-coverage-matrix.md](responses-coverage-matrix.md)。这不表示 Responses 边界契约已完整覆盖。

- Responses 连通性套件引用 3 个 Case，基础套件引用 39 个，参数拒绝套件引用 15 个预期 HTTP 400 的 Case。
- 运行完整基础套件会包含 T3 图片素材和两次请求的缓存工作流，需要显式选择；本次改动没有执行付费 Kimi 请求。
- 逻辑目录仍为 717 个 Case。各协议的请求和断言保存在现有 Case ID 下；Chat 定义保持原样。

不发送上游请求即可校验编写的套件成员：

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases --suites-root data/suites/openai-responses --manifest data/suites/openai-responses/kimi-k3.suite-profiles.json --check
```

桌面应用将 `data/cases/openai-chat` 下可运行的 Kimi 相关用例组织为四个按模型命名的基础套件，另有更小的连通性套件。共用用例由适用套件引用，无需复制；模型与渠道由运行时的 Run 选择。

- 当前目标模型为 `kimi-k3`、`kimi-k2.7-code`、`kimi-k2.7-code-highspeed` 和 `kimi-k2.6`。
- 目录包含跨 11 个维度的 84 个已启用 K3 用例。`Kimi K3 官方基础套件` 引用全部 84 个用例，包括 10 个媒体密集型 T3 用例及 stop 运行时/cache 工作流探针。它们的 `default=false` 使其不进入单独 Case 的默认选择，但运行整个套件时仍会执行。
- `Kimi K2.7 Code 官方基础套件` 和 `Kimi K2.7 Code Highspeed 官方基础套件` 各包含 10 个用例。
- `Kimi K2.6 官方基础套件` 包含 11 个用例。
- 四个基础套件引用 87 个唯一的已启用 HTTP/SSE 用例。套件成员决定执行哪些 Case；模型与渠道由 Run 绑定。
- Case 不编码目标模型。套件名称表达预期覆盖范围，运行时需要由操作者绑定兼容的模型。
- K3 专属用例覆盖始终开启的推理及 `tool_choice=required`；K2.6 用例覆盖 `thinking.type=enabled|disabled`；K2.7 Code 与 Highspeed 共用保留思考的基线。
- 共用协议、用量、auto/none 工具选择、结构化输出和固定参数基线只维护一份，适用于四个模型。
- 指定函数的工具选择作为负向用例启用，因为它与开启思考的 K3 不兼容。历史 `allowed_tools` 兼容扩展仍禁用，因为它不是当前官方基线。
- K3 多模态对象/字符串输入及畸形输入用例作为单独选择时非默认的 T3 用例纳入基础套件，确定性测试素材保留在用例定义中。
- 不传开关的默认开启用例仍是 K3 官方基线。`enable_thinking`、`thinking.type` 和 `chat_template_kwargs` 探针也已启用，用于观察供应商行为；其结果可能与官方 K3 契约不同。
- 确定性素材由 `tools/api-audit/fixtures/kimi-k3/generate_number_media.py` 使用 OpenCV/Numpy 帧缓冲生成：PNG 包含 `7`，MP4 按顺序显示 `3`、`7`、`4`。
- File-ID 输入需要目标供应商账户创建的 ID；自包含套件覆盖 base64 分支，不伪造会产生确定性误报的文件 ID。
- Kimi-K3 固定采样契约分别用五个用例检查官方精确值，并要求略低或略高的代表值返回 HTTP 400。不传参数仍是推荐基线。
- 报告中的 `must.tool_choice`、`must.thinking_switch` 和 `must.structured_output` 是派生汇总，不重复创建无操作用例。
- 报告中的 `note.content_filter` 需要供应商人工确认，不作为自动通过项。
- K3 前缀缓存工作流使用两次顺序请求，检查第二次响应的 `usage.prompt_tokens_details.cached_tokens`；它属于非默认的供应商观察探针，不保证所有兼容网关的计费结果。
- `load-profile-32k.json` 记录报告的 32K 负载场景，但常规 `api-audit` 运行不会自动执行这个需付费的 160 请求负载。

K3 套件使用等价类、数值/数量边界、依赖表和工作流用例覆盖部分有文档依据的请求边界，不代表已完整覆盖官方契约。具体覆盖、执行等级、文档冲突，以及保留思考重放和签名请求头的剩余执行器缺口，见 [coverage-matrix.md](coverage-matrix.md)。

当前模型矩阵依据官方[模型概览](https://platform.kimi.com/docs/api/models-overview)、[思考模型指南](https://platform.kimi.com/docs/guide/use-thinking-models)和[工具选择指南](https://platform.kimi.com/docs/guide/use-tool-choice)。

```powershell
go build -o .\bin\api-audit.exe .\cmd\llm-test-studio
.\bin\api-audit.exe audit list --suite openai-chat --cases-root data/cases

# 为 Kimi K3 运行非流式用量冒烟用例。
$env:API_AUDIT_API_KEY = "<secret>"
.\bin\api-audit.exe audit run --suite openai-chat --cases-root data/cases --base-url "https://gateway.example" --model kimi-k3 --case must.usage_non_stream

# 查看适用的 OpenAI 用例，不发送请求。
.\bin\api-audit.exe audit run --suite openai-chat --cases-root data/cases --base-url "https://gateway.example" --model kimi-k3 --all-cases --dry-run
```
