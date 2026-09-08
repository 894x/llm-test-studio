# Kimi 官方多模型基础用例

所有用例使用 `openai-chat` 传输协议；Kimi 名称标识模型，不是协议。

桌面应用将 `data/cases/openai-chat` 下可运行的 Kimi 用例组织为四个内置模型套件。每个可运行用例声明明确的 `model_targets`；共用用例由适用套件引用，无需复制。

- 当前目标模型为 `kimi-k3`、`kimi-k2.7-code`、`kimi-k2.7-code-highspeed` 和 `kimi-k2.6`。
- 目录包含跨 11 个维度的 70 个已启用 K3 用例。`Kimi K3 官方基础套件` 引用 60 个常规 T1/T2 用例；10 个媒体密集型 T3 用例可显式选择，但不进入基础套件。
- `Kimi K2.7 Code 官方基础套件` 和 `Kimi K2.7 Code Highspeed 官方基础套件` 各包含 10 个用例。
- `Kimi K2.6 官方基础套件` 包含 11 个用例。
- 四个套件引用 73 个唯一的已启用 HTTP/SSE 用例。桌面执行器在准备目标运行时仍检查模型适用性，约束用户创建的套件。
- 通用非 Kimi 用例的空 `model_targets` 表示适用所有模型；这些 Kimi 套件的可运行用例始终使用明确的目标列表。
- K3 专属用例覆盖始终开启的推理及 `tool_choice=required`；K2.6 用例覆盖 `thinking.type=enabled|disabled`；K2.7 Code 与 Highspeed 共用保留思考的基线。
- 共用协议、用量、auto/none 工具选择、结构化输出和固定参数基线只维护一份，适用于四个模型。
- 指定函数的工具选择作为负向用例启用，因为它与开启思考的 K3 不兼容。历史 `allowed_tools` 兼容扩展仍禁用，因为它不是当前官方基线。
- K3 多模态对象/字符串输入及畸形输入用例作为非默认 T3 用例启用，确定性测试素材保留在用例定义中。
- 思考开关与兼容别名用例禁用，因为 Kimi-K3 官方要求始终开启思考。仅不传开关的默认开启基线可执行。
- 确定性素材由 `tools/api-audit/fixtures/kimi-k3/generate_number_media.py` 使用 OpenCV/Numpy 帧缓冲生成：PNG 包含 `7`，MP4 按顺序显示 `3`、`7`、`4`。
- File-ID 输入需要目标供应商账户创建的 ID；自包含套件覆盖 base64 分支，不伪造会产生确定性误报的文件 ID。
- Kimi-K3 固定采样契约接受官方精确参数组合，并要求略低或略高的代表值返回 HTTP 400。不传参数仍是推荐基线。
- 报告中的 `must.tool_choice`、`must.thinking_switch` 和 `must.structured_output` 是派生汇总，不重复创建无操作用例。
- 报告中的 `note.content_filter` 需要供应商人工确认，不作为自动通过项。
- `load-profile-32k.json` 记录报告的 32K 负载场景，但常规 `api-audit` 运行不会自动执行这个需付费的 160 请求负载。

K3 套件使用等价类、数值/数量边界、依赖表和工作流用例覆盖当前官方请求契约。具体覆盖、执行等级、文档冲突，以及保留思考重放和签名请求头的剩余执行器缺口，见 [coverage-matrix.md](coverage-matrix.md)。

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
