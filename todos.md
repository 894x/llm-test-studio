# LLM Test Lab Roadmap & TODO

本文记录项目的产品边界、阶段架构与实施清单。目标不是把现有脚本简单包一层界面，而是统一模型、渠道、测试用例、执行、结果和报告，使 GUI、CLI、本地执行和未来的服务端执行共享同一套语义。

本次 V1 是完整的 Go 迁移，不长期保留 Python 业务层。Python/Streamlit 只作为迁移期行为基准和旧数据验收工具；对应 Go 能力通过等价测试后即删除，不形成第二套运行时。

## 产品定义

项目定位为面向 LLM 接口的本地优先测试工作台，覆盖：

- 模型管理：维护逻辑模型、能力、协议和模型映射。
- 渠道与 Key 管理：维护服务地址、鉴权信息、可用状态和模型绑定。
- 测试用例：维护请求输入、期望结果、校验规则和版本。
- LLM 压测：支持并发、到达率、持续时间、流式指标和错误分析。
- LLM 兼容测试：验证 OpenAI 兼容协议、流式语义、模型能力和厂商差异。
- 运行与对比：保留执行快照、结果、证据、基线和历史趋势。
- 报告导出：统一支持 JSON、HTML、PNG、PDF。
- 双入口：GUI 和 CLI 提供等价能力，共用一个 Application Core。

## 统一领域语言

| 对象 | 定义 | 关键约束 |
| --- | --- | --- |
| Model | 用户认知中的逻辑模型及其能力 | 不等同于某个渠道里的模型名称 |
| Channel | 一条可调用的服务通道 | 包含 Base URL、协议、状态和模型映射，仅引用凭据 |
| Credential | API Key、管理员令牌等秘密 | 不以明文写入 SQLite、日志、报告或导出文件 |
| Test Case | 可版本化的输入、参数、期望和校验规则 | 修改后不能改变历史 Run 的语义 |
| Suite | 一组可复用的测试用例 | 用于兼容测试和回归测试 |
| Plan | 可复用的执行配置 | 指定渠道、模型、用例、负载和判定阈值 |
| Run | 一次实际执行及其不可变快照 | 本地和远程执行使用相同状态机 |
| Result | 请求级或用例级结果 | 区分传输成功、协议成功、语义成功和 SLA 成功 |
| Evidence | 支撑结论的脱敏原始证据 | 可追溯且不得泄露完整凭据 |
| Report | 从 Run 和 Result 生成的统一报告模型 | 各导出格式内容口径一致 |
| Integration | new-api 等外部系统连接 | 与普通模型渠道凭据分开管理 |

## 总体架构原则

- GUI 和 CLI 只负责交互，业务规则、校验、执行、持久化和报告必须进入共享核心。
- V1 本地优先、离线可用，不依赖账号或远程服务。
- V1 即为主要对象分配稳定 UUID，并保存 schema version、revision 和时间戳，为 V2 同步预留条件。
- 本地结构化数据使用 SQLite；大体积请求证据和报告附件使用本地文件存储，SQLite 只保存索引、哈希和相对路径。
- Credential 使用操作系统安全存储，SQLite 只保存凭据引用和脱敏元数据。
- HTML 是可视化报告的标准渲染结果；PNG 和 PDF 从同一 HTML/CSS 渲染链路导出，JSON 是结构化数据源。
- 本地和远程执行共享 Plan、Run、Result、Evidence、Report 协议，避免 V2 重写产品。
- 数据库迁移、CLI 输出协议和 Report Schema 均需版本化并保持可迁移。

---

## V1：本地跨平台版本

### 目标架构

```text
React + Tailwind + shadcn/ui ─┐
                              ├─ Local Application Core ─┬─ Execution Engines
llm-test CLI ─────────────────┘                          ├─ SQLite Repository
                                                         ├─ OS Credential Store
                                                         └─ Report / Artifact Renderer
```

目标平台：Windows、macOS、Linux。

### 桌面技术选型

- [x] 完成桌面壳技术决策并记录 ADR；Wails 进入 PoC 后仍须通过三平台退出门槛。
- [x] 选择 `Wails + React/Vite + Tailwind CSS + shadcn/ui + Go Core`：GUI 与 CLI 直接调用同一 Go 核心，不启动本地 HTTP 服务。
- [ ] 将 `Tauri + React/Vite + Go sidecar` 作为备选，验证 sidecar 生命周期、签名和打包复杂度。
- [ ] 仅在生态能力确有必要时选择 `Electron + React/Vite + Go sidecar`，同时评估体积和内存成本。
- [ ] 技术验证至少覆盖：GUI 调用 Go、CLI 调用同一核心、访问同一 SQLite、操作系统凭据存储、三平台打包。
- [x] 明确现有 Streamlit Dashboard 的去留：迁移期用于人工回归，Wails 功能等价后删除。

建议代码边界：

```text
apps/desktop             Web UI 与桌面壳
apps/cli                 llm-test 命令行入口
internal/domain          领域对象与纯业务规则
internal/application     用例编排、事务和权限无关的应用服务
internal/execution       压测、兼容测试和执行状态机
internal/persistence     SQLite repository 与 migrations
internal/credentials     系统凭据存储适配
internal/reporting       Report Schema、HTML、PNG、PDF 导出
internal/integrations    new-api 等外部系统适配器
```

### GUI 与 CLI 统一

- [ ] 定义稳定的 Application Core API，GUI 和 CLI 不直接操作数据库。
- [ ] GUI 与 CLI 共用数据校验、错误码、执行状态、结果聚合、凭据读取和报告生成。
- [ ] 将 `scripts/` 中的现有实现逐步迁移到共享核心。
- [ ] 迁移期间保留 Bash/PowerShell/Python 脚本作为兼容包装层，内部调用统一的 `llm-test` CLI。
- [ ] 迁移完成后标记旧脚本为 deprecated，不再维护第二套业务逻辑。
- [ ] 将现有脚本中的流式完成判定、开放环发送、TTFT/TPOT 和实时指标纳入统一引擎。
- [ ] 合并当前 Python Dashboard 与 Bash benchmark 的执行语义，消除指标口径差异。

目标 CLI：

```text
llm-test model list|show|add|edit
llm-test channel list|show|add|edit|enable|disable|test
llm-test credential set|replace|remove
llm-test case list|show|add|edit|validate
llm-test suite list|show
llm-test plan list|show|create|clone|run
llm-test load run
llm-test audit run
llm-test run list|show|cancel
llm-test report show|export
llm-test doctor
```

### SQLite 与本地数据

- [ ] 建立数据库 migration 机制，不允许仅依赖启动时临时建表。
- [ ] 建立核心表：models、channels、channel_models、test_cases、test_suites、test_plans、plan_cases。
- [ ] 建立执行表：runs、request_results、case_results、report_records、artifact_records。
- [ ] 建立 integrations 表，但只保存外部连接的非秘密配置与 Credential 引用。
- [ ] Run 必须保存模型、渠道、用例、负载参数和环境的不可变快照。
- [ ] 大体积响应、SSE 原文、截图和报告文件放入 artifact 目录，并保存 SHA-256。
- [ ] 支持 SQLite 与 artifact 目录的一致性备份、恢复和迁移。
- [ ] 明确数据保留策略：请求明细、原始证据、聚合结果和报告可分别设置保留期。

### Key 与凭据安全

- [ ] Windows 使用 Credential Manager，macOS 使用 Keychain，Linux 使用 Secret Service 或等价安全存储。
- [ ] SQLite 仅保存 credential_id、store_ref、类型、脱敏尾号、指纹和更新时间。
- [ ] UI 和 CLI 只允许设置、替换、删除和测试凭据，默认不提供明文回显。
- [ ] 日志、错误、证据、报告和诊断包统一经过脱敏器。
- [ ] 普通渠道 API Key 与 new-api 管理员凭据使用不同用途和权限标识。

### 模型、渠道与测试用例

- [ ] 支持模型 CRUD、能力标签、协议类型和渠道模型名映射。
- [ ] 支持渠道 CRUD、启停、连通性测试、健康状态和多个模型绑定。
- [ ] 支持同一逻辑模型跨渠道对比，以及同一渠道跨模型对比。
- [ ] 支持 Test Case CRUD、复制、导入、导出和版本历史。
- [ ] 用例校验至少覆盖：HTTP、响应结构、流式结束、文本/JSON 内容、工具调用、多模态和自定义断言。
- [ ] Suite 和 Plan 可复用，并允许通过 GUI 与 CLI 运行。

### LLM 压测

- [ ] 支持突发并发、固定并发、固定到达率、开放环、阶梯增压和定时运行。
- [ ] 分离发送持续时间、单请求超时和 drain 等待时间。
- [ ] 流式请求必须校验协议结束标记或等价语义，不能只以 HTTP 2xx 判成功。
- [ ] 支持取消、停止发送和 drain，明确各状态的统计口径。
- [ ] 实时展示发送速率、完成吞吐、成功率、并发数、TTFT P50/P90/P99、TPOT P50/P90/P99 和端到端时延。
- [ ] 区分网络错误、HTTP 错误、协议错误、语义错误、限流、超时和取消。
- [ ] 支持 SLA 阈值、基线对比、回归判定和不同模型/渠道的横向比较。
- [ ] 保存执行机、网络出口、应用版本和引擎版本，避免错误解释跨环境数据。

### LLM 兼容测试

- [ ] 复用并收敛现有 Go 兼容测试引擎，不在 GUI 中重写协议逻辑。
- [ ] 覆盖 OpenAI 兼容接口、流式响应、Kimi 差异和 Seedance 等已有能力。
- [ ] 显示测试计划、实时进度、用例详情、最终结论和脱敏证据。
- [ ] 支持自定义 evaluator，并为 evaluator 输入输出定义版本化接口。
- [ ] 支持跨模型、跨渠道、跨版本的兼容性矩阵与差异报告。

### 报告与可视化

报告是核心产品能力，不是执行结果的简单附件。

- [ ] 定义统一 Report Schema，至少包含：执行计划、模型、渠道、环境、结论、SLA、核心指标、时间线、分布、用例维度、错误聚类、证据、基线和附件。
- [ ] JSON 作为完整的机器可读数据源，并包含 schema version。
- [ ] 建立统一视觉规范：色板、字体、间距、卡片、表格、图表、状态和打印样式。
- [ ] HTML 作为标准可视化报告，支持独立打开、响应式布局和证据折叠。
- [ ] PNG 从同一 HTML 页面生成，支持整页和指定图表导出。
- [ ] PDF 使用同一 HTML 与专用 print CSS 生成，处理分页、页眉页脚、宽表和孤行。
- [ ] 四种格式的指标、结论和筛选上下文必须一致。
- [ ] 首屏必须回答：测了什么、在哪测、是否通过、主要问题、相对基线如何。
- [ ] 图表必须注明单位、样本数、时间范围、分位数和异常点，不能仅给出漂亮但不可解释的图形。
- [ ] 兼容性结果除红绿状态外，还要展示失败阶段、错误簇、影响范围和代表性证据。
- [ ] 压测报告至少包含吞吐/到达率时间线、TTFT/TPOT/总时延分布、成功率、错误分布、并发变化和基线对比。
- [ ] 对 1000 条以上用例结果验证表格虚拟化、筛选、搜索和导出体验。
- [ ] 所有格式通过敏感信息扫描；PDF 不出现空白页，PNG 不截断内容。

### V1 验收标准

- [ ] Windows、macOS、Linux 均可安装并完成一次本地测试。
- [ ] 无账号、无网络服务依赖时，除被测 LLM 外全部功能可用。
- [ ] GUI 和 CLI 能操作同一数据库，并对同一 Plan 产生同口径结果。
- [ ] SQLite、日志、报告和导出包中不存在明文 Key。
- [ ] 模型、渠道、Key、用例、Suite、Plan、Run 和 Report 形成完整闭环。
- [ ] 压测和兼容测试均可导出 JSON、HTML、PNG、PDF。
- [ ] 旧数据可通过 migration 升级，失败时能够回滚或恢复备份。

---

## V2：账号、同步与服务端压测

### 目标架构

```text
Desktop / CLI
  ├─ Local Core + SQLite + Local Runner
  └─ Sync Client
          │
          ▼
Independent Backend / Control Plane
  ├─ Account, Workspace, Device and Sync
  ├─ Run Scheduler and Realtime Events
  ├─ Report and Artifact Metadata
  └─ new-api Integration
          │
          ▼
Remote Execution Plane
  ├─ Queue / Scheduler
  ├─ Isolated Load Workers
  └─ Object Storage for Evidence and Reports
```

### 账号与控制面

- [ ] 增加注册、登录、会话续期、密码重置、设备列表和设备撤销。
- [ ] 初期可先提供个人 Workspace，但数据模型需允许后续团队成员和角色权限。
- [ ] 独立后端负责账号、同步、运行调度、实时事件、审计和报告元数据。
- [ ] 服务端结构化数据优先使用 PostgreSQL；本地 SQLite 继续作为离线主存或缓存。
- [ ] 报告、原始证据和大文件使用对象存储，不直接塞入关系数据库。

### 跨账号与跨机器同步

- [ ] 同步 Model、Channel 非秘密元数据、Test Case、Suite、Plan、Run 摘要、Report 和 artifact 元数据。
- [ ] 默认不上传普通渠道 Key；用户需明确选择本地专用、可同步或允许远程执行。
- [ ] 使用 UUID、revision/ETag 和 tombstone 支持增量同步与删除传播。
- [ ] Test Case 和 Plan 保留版本，发生冲突时提供明确的保留本地、保留远端或复制为新版本。
- [ ] Run 和 Result 视为不可变事件，避免多设备合并时改写历史。
- [ ] 支持设备级同步状态、最后同步时间、冲突列表和手动重试。

### 服务端压测

- [ ] 用户可为同一 Plan 选择本地执行或远程执行。
- [ ] 远程 Run 状态统一为 queued、starting、running、draining、completed、failed、cancelled。
- [ ] 客户端关闭后任务继续运行，其他设备可查看进度和结果。
- [ ] 服务端实时推送发送速率、完成吞吐、成功率、TTFT、TPOT、错误和队列状态。
- [ ] Worker 使用与本地一致的执行包和事件协议，报告继续使用同一 Report Schema。
- [ ] 记录 Worker 区域、网络出口、规格、引擎版本和时间同步状态。
- [ ] 支持配额、最大并发、最长时间、取消、超时、重试和 drain，防止资源耗尽。
- [ ] 隔离不同账号的执行环境、凭据和证据文件。

### 一键上架到 new-api

这里的“上架”初步定义为：把本项目中验证通过的渠道配置发布或更新到指定 new-api 实例。

- [ ] 配置 new-api 地址和管理员连接，优先支持管理员 Access Token。
- [ ] 仅在目标版本不支持 Token 时考虑管理员账号密码，并使用服务端加密存储。
- [ ] 连接时检测 new-api 版本、API 能力和管理员权限。
- [ ] 提供 dry-run，展示 create、update、skip、conflict，不显示完整 Key。
- [ ] 使用稳定外部标识或映射表实现幂等 upsert，重复点击不创建重复渠道。
- [ ] 首期发布字段：渠道类型、Base URL、API Key 引用、模型列表、模型映射和启用状态。
- [ ] 明确后续是否包含分组、权重、优先级、价格、代理和自动禁用策略。
- [ ] 发布后读取 new-api 实际配置进行校验，并展示逐项结果。
- [ ] 保存操作者、时间、目标实例、变更摘要和结果的审计记录。
- [ ] 为失败的部分更新提供可恢复方案；涉及删除或覆盖时必须单独确认。

### 服务端安全

- [ ] 服务端凭据使用独立密钥加密，密钥与业务数据库分离。
- [ ] new-api 管理员凭据和普通渠道 Key 分用途、分权限、分审计。
- [ ] API 永不返回完整秘密；日志、事件、报告和支持包统一脱敏。
- [ ] 对用户提供的 Base URL 做 SSRF 防护、协议限制和目标校验。
- [ ] Worker 使用短期身份和最小权限，只能取得当前 Run 所需凭据。
- [ ] 报告、证据和 artifact 下载必须校验 owner/workspace 权限，并使用短期链接。
- [ ] 账号、同步、远程任务和 new-api 发布均需要限流和审计。

### V2 验收标准

- [ ] 同一账号在两台机器上同步模型元数据、用例、计划和报告。
- [ ] 用户可以关闭同步并继续完整使用本地功能。
- [ ] 远程压测不依赖客户端持续在线，并可从另一台设备接管查看。
- [ ] 本地和远程对相同 Plan 生成同结构、同指标口径的报告。
- [ ] 普通渠道 Key 默认不上传，所有上传和远程使用都有明确授权。
- [ ] new-api 支持 dry-run、一键发布、幂等重试、回读校验和审计。

---

## 里程碑

### M0：领域与契约

- [ ] 固化领域对象、Run 状态机、错误分类和成功判定。
- [ ] 定义 Application Core API、CLI 输出协议、Report Schema 和数据库迁移规范。
- [ ] 为 V1/V2 共用对象确定 UUID、revision 和版本策略。

### M1：V1 本地核心

- [ ] 完成桌面技术验证和 ADR。
- [ ] 完成 SQLite repository、migration 和系统凭据存储。
- [ ] 完成 Model、Channel、Credential、Test Case、Suite 和 Plan。

### M2：统一执行引擎

- [ ] 迁移并统一 `scripts/` 中的压测逻辑。
- [ ] 收敛现有 Go 兼容测试引擎。
- [ ] GUI、CLI 共用 Run 状态机、实时事件和结果存储。

### M3：报告系统

- [ ] 完成 Report Schema 与报告设计规范。
- [ ] 完成 HTML/JSON，再从同一链路输出 PNG/PDF。
- [ ] 完成压测、兼容性、对比和趋势报告的可视化验收。

### M4：跨平台交付

- [ ] 完成三平台打包、签名、自动更新和升级回滚。
- [ ] 完成旧脚本兼容包装与迁移文档。

### M5：V2 控制面与同步

- [ ] 完成账号、设备、Workspace、同步协议和冲突处理。
- [ ] 完成 PostgreSQL、对象存储和审计。

### M6：V2 远程执行

- [ ] 完成调度、队列、Worker 隔离、实时事件和配额。
- [ ] 验证本地/远程执行与报告口径一致。

### M7：new-api 集成

- [ ] 完成管理员连接、dry-run、幂等发布、回读校验和审计。

## 待讨论决策

- [x] 桌面壳选择 Wails；Tauri 和 Electron 仅在 PoC 未通过退出门槛时重新评估。
- [x] React 样式与基础组件选择 Tailwind CSS + shadcn/ui；业务状态与校验不进入前端组件。
- [x] Python 压测实现完整迁移到 Go；等价测试通过后删除 Python 业务实现。
- [x] Streamlit Dashboard 在新桌面端达到功能等价后停止维护并删除。
- [ ] V2 是否允许同步渠道 Key？若允许，需要哪些逐级授权和撤销机制？
- [ ] V2 首期只支持个人账号，还是直接引入团队 Workspace？
- [ ] 远程 Worker 是共享池、账号独享，还是两者都支持？
- [ ] 是否需要按地域选择远程出口，以便比较不同网络路径？
- [ ] new-api 的首期上架范围是否只包含渠道和模型，还是同时管理分组、权重与价格？
- [ ] new-api 是否强制使用管理员 Access Token，避免保存管理员密码？

## 当前优先级

- **P0**：领域对象与协议、桌面技术验证、Application Core、SQLite/凭据边界、Report Schema 和视觉原型。
- **P1**：统一压测与兼容引擎、GUI/CLI 功能闭环、四格式报告、三平台打包。
- **P2**：账号同步、远程执行、new-api 一键上架。
