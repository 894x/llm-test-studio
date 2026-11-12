# 多步骤 Case

一个 Case 的 `workflow.mode = "sequence"` 按顺序执行初始请求和后续请求。
初始请求的固定 ID 为 `initial`；每一步完整读完 JSON 或 SSE 响应后，才开始下一步。
Chat Completions、Responses 和 Messages 共用以下控制字段：

```json
{
  "mode": "sequence",
  "steps": [
    {"id": "after_6m", "delay_ms": 360000, "reuse_request_from": "initial"}
  ]
}
```

在桌面 Case 编辑器的「工作流」JSON 中填写以上内容；请求体仍在初始请求中填写。
也可以直接编写 Case 文件各协议定义下的 `workflow`。

| 字段 | 规则 |
| --- | --- |
| `id` | 唯一标识，不能使用保留名 `initial` |
| `delay_ms` | 可选，非负整数毫秒，默认 0；所有步骤的等待合计最多 86,400,000 ms（24 小时） |
| `reuse_request_from` | 复用本 Case 内 `initial` 或更早步骤已渲染的请求体；不能引用自己、未来步骤或其他 Case |
| `request.body` | 不复用时提供请求模板，仍可用 `$response` 读取前一步响应；与 `reuse_request_from` 互斥 |

仍最多允许 32 个后续步骤。复用会保留已生成的随机内容和请求参数，不会重新调用生成器。
复用只保存本次执行的请求体，不共享其他 Case、迭代、运行或 runner 的状态。
步骤可复用已经复用过的更早步骤。文件输入不支持 sequence，沿用原有限制。

延迟发生在上一响应接收完毕之后、当前请求发送之前。等待使用可取消的计时器，
取消任务会中断等待且不发送后续请求；「停止发送」会阻止调度新 Case，已开始的
Case 仍会完成其步骤。应用退出或进程中断后不恢复计时器，也不自动续跑到中间步骤。

启用任一新控制字段后，配置的请求超时分别约束每一次 HTTP 请求（包括读取响应）；
Case 的总预算为「请求数量 × 单次请求超时 + 等待合计」。外部任务截止时间仍生效，
默认 job host 执行上限为一小时，长流程需要适当设置 host 策略。等待不占单次 HTTP
超时，但占用一个 Case 并发槽，且计入 Case E2E 与任务总耗时。每次成功等完后，
对应 exchange 的 `metrics.sequence_wait_ms` 保存实际等待毫秒数；HTTP 耗时不含等待。
没有新控制字段的旧 sequence 保持原来的整个 Case 共用一个超时预算。

断言在 Case 执行结束后统一评价。`requests` 数组按照实际请求顺序保存请求体、响应、
状态、流事件及指标；引用缺失步骤不会自动补 0。以 Chat 缓存为例：

```json
{
  "id": "read_matches_first_write",
  "source": "requests",
  "pointer": "/1/response/usage/prompt_tokens_details/cached_tokens",
  "operator": "equals",
  "expected_from": {
    "source": "requests",
    "pointer": "/0/response/usage/prompt_tokens_details/cache_write_tokens"
  }
}
```

还需同时断言首次写入为正、首次读取为 0、后续未重写、两次请求体相同、实际等待
满足下限、两次 HTTP/流终态有效，以及没有协议 issues。只比较两个可能为 0 的值
不足以证明缓存命中。完整例子见 `data/cases/kimi-cache-ttl/` 和
[Kimi 缓存专项](kimi-cache-usage.md)。

新控制字段需要 runner 能力 `sequence_steps`，调度时会拒绝不支持的 runner。
旧定义和旧报告保持原来的编码与含义；使用本功能前应更新桌面端及对应 runner。
格式与降级限制见[兼容性说明](compatibility-policy.md)。
