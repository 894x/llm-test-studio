# Kimi 1h 缓存跨 5 分钟保留验证

范围：`kimi-k3` / `kimi-k3.1-pro` 的 Chat Completions、Responses、Messages，
JSON 与 SSE。只覆盖 1h 冷写 → 360 秒静默等待 → 原请求读取，不覆盖完整参数契约、
完整 1h 保留、5m 精确过期或滑动续期。文档核对日期：2026-11-12。
主来源：[Kimi Context Caching](https://platform.kimi.com/docs/guide/context-caching)。
MSH 在 Responses/Messages 上的必返及扩展 TTL 拆分一致性属于目标提供方兼容性验收。

| 契约或状态 | 前提与请求变化 | 必须满足的断言 | 用例与执行层级 | 覆盖状态 |
| --- | --- | --- | --- | --- |
| 冷写 1h | 唯一随机前缀置于最前，显式 `ttl=1h`，原有长上下文 | 读取 0，写入正整数，1h 头等于写入量，5m 头 0；usage 分项若存在则逐档相等 | `read_1h_after_6m` / `_stream`，三协议 T3 | implemented; live deferred |
| 静默等待 | 完整接收初始响应后等待 360000 ms，中间无请求 | 两次请求；第二次 `sequence_wait_ms >= 360000` | 同上 | implemented; local fake-clock passed |
| 同内容读取 | `reuse_request_from=initial`，不重新生成前缀 | 请求体相等，读取量等于首次写入量，读取正整数，无重新写入 | 同上 | implemented; local partial-hit/cold-prefix faults rejected |
| 原生返回与头对账 | 保留对应 JSON/SSE 原生请求及 usage | HTTP 200、无协议问题、usage 合法、两档头齐全并与 usage 相符；SSE 正常终态 | 同上 | implemented; local HTTP/SSE fixture passed |
| 执行隔离 | 新迭代使用新随机上下文，Case 内精确复用 | 两轮冷写请求不同，单轮两请求一致；JSON 证据往返保持断言结论 | 同上 | implemented; local passed |
| 完整一小时、准确到期、续期 | 需要独立长时间实验，且不能用中途命中刷新待测缓存 | 本次不作结论 | excluded from this bounded scenario | out of scope |

共 2 个逻辑 Case，每条含 3 个协议定义。三协议各有独立的 `kimi-cache-ttl`
Suite（2 Case / 4 HTTP 请求），不加入快测默认成员或已有准入计划。顺序执行至少
12 分钟，并发执行至少 6 分钟；存在实际供应商费用。本次只运行本地 fixture，
没有真实供应商执行证据。上游没有暴露缓存 ID，本测试不能认证物理缓存身份。
