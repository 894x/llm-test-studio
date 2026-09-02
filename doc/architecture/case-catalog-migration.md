# 内置测试用例目录迁移与桌面读取边界

本文记录提交 `0b6c07a` 至 `eec4e92` 交付的测试用例目录、报告只读投影和桌面接入边界。它描述的是当前已有代码，不代表兼容测试执行器、报告导出器或三平台安装包已经完成。

## 当前交付范围

| 提交 | 已交付能力 |
| --- | --- |
| `0b6c07a` | 为领域 `TestCase` 增加稳定 key、维度、启停、默认、严重度和执行模式策略 |
| `d8672f8` | 增加类型化 Catalog 查询及 Model、Channel、Mapping、Test Case、Suite、Plan 的创建/更新应用服务 |
| `c1cf044` | 将仓库内 legacy JSON 严格转换为领域 `TestCase`，以 SQLite schema v3 记录来源和导入状态 |
| `38a5391` | 增加有界、失败关闭的报告摘要投影 |
| `98de454` | Wails production 启动时迁移数据库、导入内置用例，并暴露目录和报告查询 |
| `eec4e92` | React 桌面壳接通总览、模型与渠道、用例、计划、运行和报告工作区 |

内置 bundle 当前包含 89 个可转换用例：

- OpenAI Chat：43；
- Kimi K3：40；
- Seedance：6。

导入后的策略分布为 57 个 automatic 可运行用例、26 个 disabled 用例和 6 个 manual 用例。`disabled` 优先于执行模式统计，因此这里的三类合计恰好为 89。另有 `kimi-k3/load-profile-32k.json` 作为 deferred plan template 被严格验证，但不转换成 `TestCase`，也不会由当前导入器执行。

## legacy JSON 到领域 TestCase 的映射

导入器只接受 `openai-chat/<case>/case.json`、`kimi-k3/<case>/case.json`、`seedance/<case>/case.json`，以及唯一的 deferred load-profile 路径。JSON 使用未知字段拒绝策略，目录中的符号链接、重复 source key、未知协议或 evaluator kind 都会使整次导入失败。

| legacy 字段或规则 | 领域字段 | 转换规则 |
| --- | --- | --- |
| `id` | `TestCase.Key` | 保留业务 key；持久化 UUID 不是直接复制该值 |
| `name` | `TestCase.Name` | 去除首尾空白后写入 |
| `dimension` | `TestCase.Dimension` | 去除首尾空白后写入 |
| `protocol` | `TestCase.Protocol` | 只接受领域支持的协议，并校验该协议允许的 legacy kind |
| `disabled` | `TestCase.Enabled` | 取反，即 `Enabled = !disabled` |
| `default` | `TestCase.Default` | 原值保留 |
| `severity` | `TestCase.Severity` | 缺省时补为 `normal` |
| `kind == manual_unknown` | `TestCase.ExecutionMode` | 映射为 `manual`；其余已支持 kind 映射为 `automatic` |
| `request.method` | `Definition.Request.Method` | 去空白、转大写；空值补为 `POST` |
| `request.path` | `Definition.Request.Path` | 去除首尾空白 |
| `request.body` | `Definition.Request.Body` | 字段缺失或 JSON `null` 记录为无 body；其余保留 JSON |
| 无 legacy headers 字段 | `Definition.Request.Headers` | 初始化为空 map |
| `kind` | `Expected` | 生成允许的 HTTP 状态范围，并为流式 kind 设置 required completion |
| `kind`、`options`、body 是否存在 | `Assertions[0]` | 写成 `custom` assertion，配置 driver 为 `legacy.apiaudit`、driver version 为 1 |

`legacy.apiaudit.v1` 配置保存了 legacy kind、options 和 body-presence 语义，使后续 Go evaluator 可以按版本实现一致行为。当前只完成了结构化迁移和语义留存；Go evaluator runtime 尚未实现与旧 API audit 的完整行为等价，不能把“89 个用例已导入”解释为“89 个用例已由纯 Go 执行通过”。

## 稳定身份与四类哈希

导入命名空间固定为 `builtin.cases/v1`，source key 为 `<protocol>/<legacy-id>`。实体 ID 使用固定 namespace 与 `builtin.cases/v1/<source-key>` 计算的版本 5 UUID，因此同一来源在不同数据库和重启之间得到相同 UUID。

SQLite `test_case_import_sources` 为每个来源保存四类 SHA-256：

| 字段 | 覆盖内容 | 用途 |
| --- | --- | --- |
| `source_bytes_sha256` | 单个源文件的原始字节 | 识别文件级变化，并作为来源记录 CAS 的一部分 |
| `semantic_sha256` | 经严格解码、空白规范化和默认值补全后的 legacy 语义 | 区分仅字节变化与会影响转换结果的语义变化 |
| `materialized_sha256` | 领域 `TestCase` 的 key、名称、维度、协议、策略和 definition；不含 UUID、revision、时间戳 | 比较当前实体内容与导入器目标内容，避免数据库或 revision 影响内容身份 |
| `bundle_manifest_sha256` | 所有 JSON 路径与 source bytes hash 按路径排序后的清单；包含 deferred load-profile | 标识一次导入看到的完整 bundle |

来源记录还保存 converter version、entity ID、imported revision、导入时间和可选 retired time。数据库约束校验哈希格式、来源路径唯一性、实体唯一性，以及 `(entity_id, imported_revision)` 对 `test_cases` 的外键关系。

## 幂等、CAS 与用户修改

导入服务先发现并验证完整 bundle，再读取当前来源记录和 `TestCase`，最后用一个 batch 原子应用变化；不会逐文件留下半成品。

- 首次导入：用稳定 UUID 创建 revision 1，并写入来源记录。
- 内容未变：保留实体和原始 imported revision，重复启动不会增加 revision。
- bundle 语义变化、用户未改：以领域 `NextRevision` 更新实体，并同步来源记录。
- 用户已修改：当当前 revision 大于 imported revision，且当前 materialized hash 不等于新 bundle 目标时，报告 conflict 并保留用户版本，不覆盖。
- 来源消失：以 CAS 校验旧 source hash 和 imported revision 后设置 `retired_at`，不直接删除领域实体。
- 来源恢复：重新发现相同 source key 时清除退休状态；若同时存在用户修改，仍遵守上述冲突策略。
- 并发或陈旧状态：实体 revision 和来源 expectation 任一不匹配即失败，整个事务回滚。

Wails production 只在本地日志报告冲突数量；前端目录读取仍能看到被保留的用户 revision。导入错误会使桌面初始化失败，而不是悄悄退回内置 fixture。

## SQLite v2 到 v3

schema v3 新增 `test_case_import_sources`，并遍历 v2 已有的 `test_cases` 文档。对已经符合当前 `TestCase` schema 的文档不做改写；对旧文档补齐：

- `key = migrated.<原 UUID>`；
- `dimension = legacy`；
- `enabled = true`；
- `default = false`；
- `severity = normal`；
- `execution_mode = automatic`。

回填只替换 `document_json`，保留原 ID、schema version、revision、created_at 和 updated_at；更新语句同时比较 ID、revision 和原文档字节，丢失并发条件时失败。任何无法按旧 schema 严格解码或无法生成有效新实体的记录都会阻止 migration，避免带着部分损坏数据继续启动。

## 桌面开发 fixture 与生产数据

两种入口必须明确区分：

| 运行方式 | 数据来源 | 当前用途 |
| --- | --- | --- |
| 浏览器直接运行 Vite，且没有 Wails binding | `features/runs/fixtures` 的小型、进程内样例 | 页面开发、组件测试和交互预览；不是完整目录验收 |
| Wails production | 用户配置目录下 `llm-test-studio/llm-test-studio.db` | 启动顺序为 migration → repository → 内置 bundle import → catalog/report/workspace service；完整目录为 89 个用例 |

非开发构建缺少 Wails binding 时返回公开的 unavailable 错误，不替换为 fixture。由此，Vite 页面里看到的用例数量不能用于判断 SQLite 导入结果；生产目录数量应通过 Wails `GetCatalog` 或 repository 测试确认。

## 报告只读投影边界

当前报告页面读取的是 schema v1 的安全摘要，不是完整 Report 文档或导出结果：

- 每次 snapshot 最多返回最新 100 条，以 `generated_at` 倒序、report ID 倒序稳定排序；
- 只投影报告/运行 ID、终态、计划/模型/渠道标签、结论和聚合计数等允许字段；
- report ID、run ID、UTC 时间、终态、必填标签、计数关系、唯一性和当前 run/report 所属关系任一不一致均失败关闭；
- SQLite 对完整 report document 设置 32 MiB 上限，对单个 result、evidence、baseline 或嵌套条目设置 8 MiB 上限，写入和投影读取均执行边界检查；
- 查询先限制候选 ID，再流式读取必要历史和轻量摘要，避免把所有宽 JSON 文档载入内存后排序。

这部分只完成桌面只读摘要路径。统一 JSON/HTML/PNG/PDF exporter、完整报告详情和大表虚拟化仍属于后续工作。

## 验证命令

本切片的提交前和最终交付门禁应至少包括：

```powershell
go test ./cases ./internal/application/caseimport ./internal/persistence/sqlite
go test ./internal/application/catalog ./internal/application/reporting ./apps/desktop
go test -p 1 ./...
go vet ./...
go mod verify

Push-Location apps/desktop/frontend
pnpm lint
pnpm test -- --run
pnpm build
Pop-Location

go build -trimpath -o NUL ./apps/desktop
$env:GOOS = "linux"; $env:GOARCH = "amd64"; go test -c -o NUL ./apps/desktop
$env:GOOS = "darwin"; $env:GOARCH = "amd64"; go test -c -o NUL ./apps/desktop
Remove-Item Env:GOOS, Env:GOARCH

git diff --check
git status --short
```

跨编译只能证明 Go 编译边界，不等于对应平台 Wails 安装包、系统 WebView、签名和真实运行验收。

## 后续实施顺序

1. 实现 `legacy.apiaudit.v1` 的纯 Go evaluator runtime，并用 legacy fixtures 对 HTTP、SSE、usage、tool call、Kimi 和 Seedance 行为做等价测试。
2. 将 Suite、Plan 与 89 个目录用例连接到统一 execution package，形成可保存 Run/Result/Evidence 的闭环。
3. 让 CLI 调用同一 Catalog、execution、reporting Application Core，补齐 GUI/CLI 同库同语义验收。
4. 建立完整 Report Schema 的详情读取和同源 JSON/HTML/PNG/PDF 导出，再做敏感信息与大数据量验收。
5. 用真实 Windows、macOS、Linux 环境完成 Wails 打包、系统凭据适配、签名、升级和回滚。
6. 达到行为等价和交付门槛后删除旧 UI 与脚本业务逻辑；Bash benchmark 仅保留为独立验收 fixture。
