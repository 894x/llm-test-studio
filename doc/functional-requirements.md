# LLM Test Studio 功能需求

## 1. 文档定位

本文档描述 LLM Test Studio 当前版本已经实现、并能从代码与测试中验证的功能需求。它是现状需求说明（as-is specification），不是未来路线图。

- 产品形态：本地运行的单用户模型验证工作台
- 核心目标：统一管理模型、测试用例、执行计划、结果证据和报告
- 支持协议：OpenAI Chat、Kimi K3、Seedance
- 执行方式：Wails + React 桌面端、Go 压测/兼容性执行引擎、统一 `llm-test-studio` CLI
- 数据位置：版本化 JSON 文件、本地 SQLite、本地报告目录

## 2. 用户与核心场景

主要用户是需要验证模型网关的开发、测试或运维人员。核心场景包括：

1. 维护不含凭据的模型档案。
2. 维护可代码审查的协议测试用例。
3. 对 OpenAI 兼容接口执行定时分发的并发性能测试。
4. 对 OpenAI Chat、Kimi K3 和 Seedance 执行可重复的兼容性审计。
5. 保存请求级指标和审计证据，并在多次运行之间比较。
6. 导出可分享、可离线查看的结果报告。

## 3. 领域对象

| 对象 | 说明 | 持久化位置 |
| --- | --- | --- |
| Model Profile | 模型 ID、协议、端点、能力和可用测试套件，不含凭据 | `data/definitions/models/*.json` |
| Test Case | 协议、维度、runner kind、请求定义和判定选项 | `data/cases/<protocol>/<case>/case.json` |
| Performance Plan | 请求数、分发窗口、连接上限、超时、token 与多模态配置 | 运行时对象；摘要保存到 SQLite |
| Audit Plan | suite、模型、用例选择、dry-run、并发和 Seedance 安全选项 | Go 引擎 JSONL `plan` 事件；摘要保存到 SQLite |
| Request Result | 单请求状态、延迟、token、流式 chunk、错误与采样响应 | SQLite `run_results` |
| Audit Result | 单用例状态、证据、HTTP 状态、指标和 artifact 路径 | SQLite `audit_case_results` |
| Report | 性能报告或兼容性审计报告 | 下载内容或 `data/artifacts/` |

## 4. 功能需求

### FR-100 应用导航与总览

- FR-101：系统必须提供 Overview、Models、Test Cases、Test Plans、Runs 五个一级页面。
- FR-102：Overview 必须展示模型数量、用例数量、性能与审计运行总数、SQLite 文件大小。
- FR-103：Overview 必须展示最近的性能运行；当至少存在两次性能运行时，展示 TTFT P50 与 E2E P50 趋势。
- FR-104：Overview 必须按 `openai-chat`、`seedance`、`wan-video`、`minimax-video` 展示用例总数、默认用例数和覆盖维度数。
- FR-105：无历史数据或趋势样本不足时，界面必须给出可理解的空状态提示。

### FR-200 模型档案管理

- FR-201：系统必须能够列出、按协议筛选并查看模型档案。
- FR-202：用户必须能够创建和编辑模型档案；已存在档案的 ID 不可在编辑时修改。
- FR-203：档案必须包含 `schema_version`、`id`、`display_name`、`model`、`protocol`、`endpoint`、`suites`、`capabilities`、`enabled`。
- FR-204：`protocol` 只允许 `openai-chat`、`seedance`、`wan-video`、`minimax-video`。
- FR-205：档案 ID 必须可安全映射到文件名，只允许字母、数字、`.`、`_`、`-`，且必须与 JSON 文件名一致。
- FR-206：系统必须原子写入档案，避免部分写入损坏文件。
- FR-207：系统必须递归拒绝任何 API key、Authorization 或等价凭据字段进入模型档案。
- FR-208：只有 `enabled=true` 的档案可以进入新建性能或兼容性计划的可选列表。

验收标准：创建或编辑后，生成的 JSON 能被 Catalog 重新读取；包含凭据字段、非法协议、非法 ID 或缺失必填字段时必须拒绝保存。

### FR-300 测试用例管理

- FR-301：系统必须递归读取 `data/cases/` 下所有 `case.json` 并验证其目录协议与内容协议一致。
- FR-302：Catalog 必须支持按协议、维度、runner kind、启用状态、默认状态和关键词筛选用例；Dashboard 必须提供关键词、协议、维度和严重度筛选。
- FR-303：用户必须能够创建和编辑用例；已存在用例的 ID 与协议不可在编辑时修改。
- FR-304：用例必须包含非空的 `id`、`name`、`dimension`、`protocol`、`kind` 和 JSON 对象 `request`。
- FR-305：新用例必须显式提供 `data/cases/` 下的相对路径，并保持 `<protocol>/<case-directory>/case.json` 结构。
- FR-306：系统必须拒绝逃逸 `data/cases/` 目录、错误文件名、错误协议目录和非 JSON 兼容值。
- FR-307：用例可通过 `default` 控制默认选择、通过 `disabled` 禁用、通过 `severity` 标记严重度、通过 `options` 提供 runner 专用判定参数。
- FR-308：系统必须原子写入用例文件。

验收标准：现有 OpenAI Chat、Kimi K3、Seedance 用例必须全部通过 Catalog 验证；新用例保存后仍位于指定 suite 目录。

### FR-400 性能测试计划

- FR-401：用户必须先选择一个已启用的模型档案，并输入请求端点和本次运行的 API key。
- FR-402：计划必须支持配置请求总数、分发窗口、近似输入 token、最大输出 token、连接上限和单请求超时。
- FR-403：连接上限必须在 1 到 2000 之间；单请求超时必须为正数。
- FR-404：计划必须支持流式或非流式请求，以及固定间隔或带小幅随机抖动的分发。
- FR-405：计划必须支持可选图片和视频 fixture；每种媒体拥有独立提示词，启用媒体时对应提示词不得为空。
- FR-406：文本请求必须保持字符串 `content`；多模态请求必须使用带 `text`、`image_url`、`video_url` part 的数组内容。
- FR-407：系统必须基于统一起点把请求分布到分发窗口中；连接槽不足时，请求必须等待，并单独记录 queue time。
- FR-408：系统必须在运行期间展示完成数、成功数、失败数、在途数、QPS 和总 TPM；失败时展示最近失败信息。
- FR-409：每个请求必须使用独立超时；一个请求失败不得阻止其余请求完成和记录。
- FR-410：HTTP 200 视为成功；超时和客户端异常必须保留为独立错误状态。

验收标准：计划结束后 `done` 等于配置请求数，`running=false`，每个请求有请求级结果，并生成一个持久化 run ID。

### FR-500 性能指标与证据保存

- FR-501：系统必须保存请求状态、E2E、TTFT、TPOT、queue time、相对开始/结束时间、prompt/completion/cached token 和 stream chunk 数。
- FR-502：流式 TTFT 必须取首个非空 `content` 或 `reasoning_content` delta 到达时间。
- FR-503：存在有效 TTFT 且 completion token 至少为 2 时，TPOT 按 `(E2E - TTFT) / (completion_tokens - 1)` 计算；首个 token 已包含在 TTFT 中，不重复计入生成间隔。
- FR-504：系统必须计算成功率、失败数、超时数、峰值在途、QPS、RPM、输入/输出/总 TPM、生成 TPS 和缓存率。
- FR-505：TTFT、TPOT、E2E 必须提供 P50、P90、P95、P99 和平均值；queue time 至少提供 P50、P95 和平均值。
- FR-506：每次运行只保存一份完整 request template；单请求记录只保存相对该模板的顶层差异。
- FR-507：原始响应保存策略必须支持 `errors_and_sample`、`errors_only`、`none`、`all`；请求级指标不受响应采样策略影响。
- FR-508：记录多模态请求时，Base64 数据必须替换为长度占位符，但必须保留原 JSON 结构。
- FR-509：写入前必须递归脱敏 API key、Authorization、X-API-Key 及密钥字符串本身。
- FR-510：系统必须能够读取旧版仅保存完整请求行的历史数据，并重建当前请求结果。

验收标准：保存再读取后，summary、request template、request delta、采样响应和请求级指标能重建为等价的运行结果，且数据库中不包含本次 API key。

### FR-600 兼容性审计计划

- FR-601：系统必须支持 `openai-chat` suite（包括 Kimi 模型），并只展示声明支持该 suite 的已启用模型档案。
- FR-602：用户必须能够选择默认用例、显式用例或全部用例。
- FR-603：兼容性计划必须默认启用 dry-run；dry-run 不得发出目标网络请求，但必须生成具体 plan、progress 和 report。
- FR-604：live run 必须要求 API key；API key 只能通过子进程环境变量传递，不得进入命令行参数。
- FR-605：同一计划选择多个模型时，Dashboard 必须按模型顺序逐个运行；单个模型内部可使用 1 到 32 个 case worker。
- FR-606：Go 引擎必须校验 suite、绝对 HTTPS base URL、正数 timeout/poll interval、合法并发和用例选择。
- FR-607：Go 引擎必须输出机器可读 JSONL：一个 `plan`、每个具体 run 一个 `progress`、一个 `final`。
- FR-608：单用例结果必须包含 ID、名称、维度、协议、模型、状态、严重度、耗时、证据和 HTTP 状态等可审计信息。
- FR-609：最终报告必须提供 pass、warning、fail、unknown 汇总，以及 overall、verdict、JSON 和 HTML 路径。
- FR-610：存在失败用例时，审计仍必须产出有效 `final` 与报告；进程退出码 1 表示审计完成但有失败，退出码 2 表示配置或协议错误。

验收标准：桌面端和 CLI 直接调用 Go Application Core，消费完整运行状态，并把有效结论与逐用例结果写入统一 SQLite repository。

### FR-700 Seedance 审计计划

- FR-701：系统必须支持 Seedance 默认模型、显式模型和引擎配置的全部模型模式。
- FR-702：Seedance 必须支持默认用例、显式用例和全部用例选择。
- FR-703：Seedance live run 必须保持单 worker；不得接受大于 1 的 case concurrency。
- FR-704：用户可选择 `no-wait`，使任务创建后不继续轮询到终态。
- FR-705：用户点击开始或运行 CLI 命令后直接执行选中的任务，不设置额外付费确认；执行前必须明确目标与任务计划，并提供 dry-run 预览。
- FR-706：运行结果必须沿用统一的 plan/progress/final 事件和审计持久化流程。

验收标准：未满足目标、用例或并发约束时必须在请求发出前拒绝执行；满足约束后可直接开始，dry-run 可预览具体任务计划而不请求上游。

### FR-800 历史、详情与导出

- FR-801：Runs 页面必须分别展示 Performance 与 Compatibility & Seedance 历史。
- FR-802：性能历史必须展示配置、成功/失败、吞吐和关键延迟；选择一次运行后必须能重建请求级详情。
- FR-803：存在至少两次性能运行时，必须支持 TTFT P50、E2E P50、TPOT P50 趋势比较。
- FR-804：性能报告必须支持 JSON、独立 HTML、PDF 和 PNG；PNG 必须可通过浏览器剪贴板复制。
- FR-805：审计历史必须展示 suite、模型、overall 和四类状态汇总，并列出逐用例证据。
- FR-806：审计报告文件存在时，必须提供 JSON 和 HTML 下载。
- FR-807：报告路径保存到 SQLite 时应优先转换为项目相对路径，确保项目目录可迁移。

### FR-900 CLI 与辅助工具

- FR-901：Go 引擎必须提供 `list` 和 `run` 命令，并支持人类可读输出与 `--jsonl` 输出。
- FR-902：Go CLI 必须通过 `LOADTEST_API_KEY`、`LOADTEST_URL`、`LOADTEST_MODEL` 等环境变量读取运行配置；不得要求把密钥写入仓库。
- FR-903：独立 Bash benchmark 必须支持 burst 与 open-loop timed dispatch、流式/非流式、单请求超时、请求级 artifact、汇总 JSON 和实时 TTFT/TPOT 指标。
- FR-904：Bash benchmark 的 HTTP 2xx 流式请求只有在收到 SSE `[DONE]` 后才能判定为成功。
- FR-905：benchmark 集成测试必须使用本地 OpenAI-compatible mock，不依赖真实供应商或真实密钥。
- FR-906：Tencent TokenHub 定价导入脚本必须默认只校验；只有显式 `-Apply` 才允许更新 `ModelPrice`，更新后必须重新读取并逐模型验证。

## 5. 非功能需求

### NFR-100 安全与隐私

- API key 仅存在于当前进程内存或受控子进程环境中。
- 任何持久化边界都必须脱敏或拒绝凭据字段。
- URL 持久化时必须移除 userinfo、query 和 fragment；审计证据写出前必须执行引擎脱敏。
- 本地 artifact 即使已脱敏也可能包含业务输入和模型输出，必须按敏感测试数据管理。

### NFR-200 可审查与可复现

- 模型和用例必须使用格式化 JSON，能够进入 Git code review。
- fixture 必须是确定性项目输入；运行产物不得进入 Git。
- dry-run 必须能在不访问目标网关的情况下生成可检查的具体计划与报告。

### NFR-300 独立性与兼容性

- 项目不得依赖原始 `new-api` checkout 才能运行 Dashboard 或 Go 引擎。
- Go Core 要求 Go 1.25+；React 桌面前端要求 Node.js 与 pnpm。
- SQLite schema 初始化必须兼容旧版性能表，并可按需补充新列。

当前 Windows 可移植性仍有一个已知缺口：跨盘临时报告目录不能直接相对化。Go 进程边界已显式使用 UTF-8，集成测试也不再依赖 POSIX executable bit 或 `true` 命令；但在 artifact 路径支持外部位置前，仍不能宣称完成跨平台保证。

### NFR-400 可观测性与失败语义

- 长时间运行必须持续提供进度，而不是只在结束时给出结果。
- 单请求、单用例失败不得丢失已完成结果。
- 配置错误、执行失败、审计判定失败必须具有不同的错误语义。

## 6. 当前非目标与边界

以下能力当前代码未实现，不应在产品说明中表述为现有功能：

- 多用户登录、角色权限和远程共享工作区；
- API key vault、长期密钥托管或自动轮换；
- 删除模型档案或用例的 Dashboard 操作；
- 分布式压测、多机 worker 协调或远程任务队列；
- 定时/周期性自动执行测试计划；
- 远程数据库、对象存储或云端报告发布；
- 在 Dashboard 中直接运行独立 Bash benchmark 或 TokenHub 定价导入脚本；
- 用价格配置自动计算测试成本。

## 7. 回归验收

| 范围 | 最低验收命令/检查 |
| --- | --- |
| Go 领域、应用、持久化、CLI 与桌面 binding | `go test ./... -count=1` |
| React UI 与 binding contract | `cd apps/desktop/frontend && pnpm test && pnpm build` |
| Go Core 与兼容性引擎 | `GOWORK=off go test ./...` |
| Go 可执行文件 | `GOWORK=off go build ./engine/cmd/llm-compat-engine` |
| Bash benchmark | `bash scripts/test_llm_benchmark.sh` |
| 文档 | README 链接有效，示例路径和环境变量与代码一致，未包含真实凭据 |

需求或代码发生变化时，应同步更新本文件，尤其是协议范围、执行安全开关、持久化结构和报告格式。
