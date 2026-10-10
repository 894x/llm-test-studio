# 多协议 Case 与原生文本接口

一个 Case 保留一个 ID、key、名称、维度和启用状态，在 `definitions` 中声明支持的协议。每个 value 是完整的 Spec；请求体、输入、断言和工作流独立维护，不继承其他协议，不在运行时转换请求。

```json
{
  "schema_version": 2,
  "id": "0edb2efc-f185-51e7-bf4e-7dcfeef33c96",
  "key": "must.usage_non_stream",
  "name": "非流式用量",
  "dimension": "usage",
  "enabled": true,
  "default": true,
  "severity": "normal",
  "execution_mode": "automatic",
  "definitions": {
    "openai-chat": {
      "inputs": {},
      "request": {"body": {"messages": [{"role": "user", "content": "Hello"}], "max_tokens": 2048}},
      "assertions": []
    },
    "openai-responses": {
      "inputs": {},
      "request": {"body": {"input": "Hello", "max_output_tokens": 2048}},
      "assertions": []
    },
    "anthropic-messages": {
      "inputs": {},
      "request": {"body": {"messages": [{"role": "user", "content": "Hello"}], "max_tokens": 2048}},
      "assertions": []
    }
  }
}
```

示例中的空断言表示仅观察。实际内置配置包含 HTTP、原生终态、usage、文本或工具调用断言。模型、URL、认证由 Run 绑定；Case 不保存这些字段。

Suite、Plan、Channel、Model 和一次 Run 各选择一个协议。执行时读取 `definitions[protocol]`，缺少定义会在准备阶段报错。快照独立复制每份定义；报告标明实际运行协议。Case 的显式 ID 不随增加协议或更换文件夹变化。

新增原生协议：

- `openai-responses`：`/v1/responses`，保留 output 数组、工具项和原生 usage；流必须收到 completed/failed/incomplete 等原生终态，失败或不完整不能因 HTTP 200 而通过。
- `anthropic-messages`：`/v1/messages`，发送必需的 `anthropic-version` 请求头；保留 content blocks、thinking/signature、工具输入和累计 usage，流以 message_stop 完成。

两个协议复用有大小限制的 HTTP/SSE 执行边界，各自解析原生事件。普通测试支持显式 sequence 工作流和响应引用。性能测试使用对应原生请求体与终态，报告 schema 2 必须包含 protocol；保留缓存、文本/语义首 token 和失败分类。

全部 717 个 `data/cases/**/case.json` 已由单协议文件转换为当前格式，ID 按转换前的规则保存，已有 Suite 引用保持原值。5 个通用文本 Case 增加两个协议的定义；4 个新 Suite 复用这些 Case：每个协议各有 connectivity 和 text-contract。其他 Case 保留适用的协议，Case 数量没有增加。

K3 后续按官方 Responses 文档为现有 84 个 Case 中的 39 个配置原生定义，并新增连通性、基础与参数拒绝 Suite；当前目录为 717 个 Case、51 个 Suite。协议差异、剩余 45 个 Chat 场景与原生契约缺口见 [K3 Responses 覆盖矩阵](../../data/cases/kimi-k3/responses-coverage-matrix.md)。

这是项目内部契约的替换。旧的 protocol/definition 包装和旧性能报告格式不被运行时读取。历史数据升级使用单独调用的临时脚本，先备份、生成候选、用当前代码校验，再替换；启动不执行迁移。

本机升级于 2026-10-09 执行：37 次运行、148 个修订、35 份正式报告、35 份性能报告通过当前 Repository 读取校验。2538 份 JSON 文档逐一反向比较，除约定的格式字段外内容保持一致，数据库触发器保持一致。升级前数据库保存在本机应用数据目录的 `backups/20261009-multi-protocol-before-upgrade.db`；原始 Case 文件备份在 `.tmp/multi-protocol/authored-cases-schema1.zip`。历史备份保持原格式。其他安装需要另行离线升级。

协议测试使用本地 mock 服务，不代表 K3 或其他供应商已接受请求，也没有执行付费上游请求。

原生契约来源：[OpenAI Responses](https://developers.openai.com/api/reference/python/resources/responses/methods/create)、[OpenAI streaming](https://developers.openai.com/api/docs/guides/streaming-responses)、[Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create)、[Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)。
