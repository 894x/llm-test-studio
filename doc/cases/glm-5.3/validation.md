# GLM-5.3 验证记录

日期：2026-09-08。供应商调用：0；仅 T0 与模拟传输。

## 已通过

- `go test -p 1 ./... -count=1`：全仓库通过，包括桌面启动、文件目录、模型适用范围、自动/禁用策略、Suite 重启稳定性、Go 执行器和 SQLite。
- `go vet ./engine/apiaudit ./internal/casecodec ./suites`：通过。
- `build_scenario_suites.py --check`：五个 suite 的成员和 quick_test 元数据均一致。
- 212/212 专属 case 与覆盖矩阵键一一对应；schema、来源、日期、精确模型范围、非默认策略及 T3 禁用策略已检查。
- 现有跟踪文件 `git diff --check` 与新增文件空白检查通过。

初次运行因目录数量断言和新增模型快速测试入口缺失失败，已修正。期间 C 盘临时空间耗尽；最终通过的全量检查将 TEMP、TMP、GOTMPDIR 和 GOCACHE 指向工作区 `.tmp/glm53-runtime`，未改全局配置。

## 审计解释

通用审计脚本的 `--include-disabled` 输出含 256 个适用 case，即 212 个专属加 44 个原有通用 case。唯一启发式提示是使用 `max_tokens` 而未使用 `max_completion_tokens`；已用 [官方参数文档](https://docs.bigmodel.cn/cn/guide/start/concept-param) 核对，GLM 本契约使用 `max_tokens`。

## 未完成的契约覆盖

54 行中 covered=14、partial=10、deferred=26、blocked=3、documentation conflict=1。covered 是实现层面的场景覆盖，不是供应商实测通过。所有有缺口行如下；细节和前置条件见覆盖矩阵。

- partial: `transport`, `message_fields`, `sampling_dependency`, `max_tokens`, `clear_thinking`, `tools`, `function_schema`, `tool_lifecycle`, `tool_stream`, `cache`
- deferred: `retrieval_knowledge_id`, `retrieval_prompt_template`, `web_search_enable`, `web_search_search_engine`, `web_search_search_query`, `web_search_search_intent`, `web_search_count`, `web_search_search_domain_filter`, `web_search_search_recency_filter`, `web_search_content_size`, `web_search_result_sequence`, `web_search_search_result`, `web_search_require_search`, `web_search_search_prompt`, `mcp_server_label`, `mcp_server_url`, `mcp_transport_type`, `mcp_allowed_tools`, `mcp_headers`, `builtin_tool_envelope`, `builtin_tool_response`, `safety`, `rate_limit`, `disconnect`, `auth_expiry`, `other_protocols`
- blocked: `context`, `modality`, `language_header`
- documentation conflict: `stop`

T3 上下文、长输出、大工具数组、缓存和负载，以及检索/MCP 外部环境均未执行；文档冲突与未定义行为没有被当作自动通过。
