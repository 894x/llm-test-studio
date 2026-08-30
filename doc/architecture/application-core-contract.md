# Application Core 契约

本文是 M0 的规范性设计。代码、CLI、桌面 binding、数据库 migration、事件和报告必须遵循这些语义；兼容入口只能做格式转换。

## 分层与依赖方向

| 层 | 职责 | 禁止事项 |
| --- | --- | --- |
| `internal/domain` | 领域对象、状态机、成功判定和纯规则 | 导入 UI、数据库、网络或操作系统实现 |
| `internal/application` | 用例编排、事务边界、事件和端口 | 读取环境变量、直接输出终端、拼 SQL |
| `internal/execution` | 兼容测试、负载调度、协议观察和 evaluator | 持久化明文凭据、决定 UI 展示 |
| `internal/persistence` | SQLite repository、migration、备份和 artifact 索引 | 保存秘密、重算业务结论 |
| `internal/credentials` | OS 安全存储 adapter 和脱敏 | 向查询 API 返回明文秘密 |
| `internal/reporting` | Report Model 到 JSON/HTML/PNG/PDF | 从运行时对象另算一套指标 |
| adapters | CLI、Wails、旧 JSONL/Python 兼容 | 绕过 Application Service 调数据库或执行器 |

依赖只能从 adapter 指向 application，再指向 domain 和端口；具体 adapter 实现反向注入。

## 通用标识与版本

主要对象使用规范 UUID，禁止以数据库自增 ID 作为公开身份。每个可变对象包含：

- `schema_version`：序列化契约版本；
- `revision`：每次成功写入递增；
- `created_at`、`updated_at`：UTC 时间；
- 删除同步使用 tombstone，不复用 ID。

Test Case 和 Plan 的修改产生新 revision。Run、Result、Evidence 和已生成 Report 视为不可变事实；修正通过新记录或 supersedes 关系表达。

## 领域对象

| 对象 | 必要内容 | 所有权约束 |
| --- | --- | --- |
| Model | 逻辑名称、协议、能力 | 不等于渠道中的 upstream model name |
| Channel | Base URL、协议、状态、Credential 引用 | 不包含秘密 |
| ChannelModel | Model、Channel、upstream model name | 唯一表达模型映射 |
| CredentialRef | store ref、用途、尾号、指纹、更新时间 | 秘密只在 OS store |
| TestCase | 协议、请求、期望、断言、revision | 历史 Run 引用快照 |
| Suite | 可复用 Case revision 集合 | 不隐式读取“最新版本”改变旧 Run |
| Plan | Model、Channel、Suite/Case、负载和 SLA | 可 clone；执行前生成不可变快照 |
| Run | Plan snapshot、环境、状态和版本 | 本地与远程共用状态机 |
| Result | 四层成功、错误分类、指标和证据引用 | HTTP 2xx 不等于总体成功 |
| Evidence | 相对路径、SHA-256、media type、脱敏标记 | 大内容不写 SQLite |
| Report | 统一 schema、结论、指标、结果和附件 | 四种格式消费同一对象 |
| Integration | 外部系统非秘密配置和专用 Credential | 与普通 Channel 凭据分用途 |

## Run 状态机

```text
queued ─► starting ─► running ─► draining ─► completed
  │           │           │          │
  ├───────────┴───────────┴──────────┴──► failed
  └───────────┴───────────┴──────────┴──► cancelled
```

- `queued`：已持久化快照，尚未取得执行资源；
- `starting`：解析凭据、准备 artifact 和 runner；
- `running`：仍允许发送新请求；
- `draining`：停止发送，等待在途请求结束；
- terminal 状态不可再迁移；
- 每次转换必须以 optimistic revision 校验持久化并发布状态事件；
- 兼容测试无需 drain 时允许 `running -> completed`。

## 成功与错误

每条 Result 分别记录：

1. `transport`：请求完成且没有网络/超时/取消错误；
2. `protocol`：HTTP、JSON/SSE schema、finish 和 `[DONE]` 等协议完整；
3. `semantic`：内容、工具、多模态或 evaluator 断言通过；
4. `sla`：时延、吞吐、成功率等阈值通过。

只有四项均为真才是总体成功。错误分类固定为 `network`、`http`、`protocol`、`semantic`、`rate_limit`、`timeout`、`cancelled`、`sla`；provider 原文只能作为已脱敏详情，不能代替稳定错误码。

## Application API

Application Service 使用有类型 command/query，不暴露数据库 row：

```text
ModelService       List/Get/Create/Update
ChannelService     List/Get/Create/Update/Enable/Disable/Test
CredentialService  Set/Replace/Remove/TestMetadata
CaseService        List/Get/CreateRevision/Import/Export/Validate
SuiteService       List/Get/CreateRevision
PlanService        List/Get/Create/Clone/Run
RunService         List/Get/Cancel/Subscribe
ReportService      Get/Export
DoctorService      Inspect
```

写入方法接收 expected revision；冲突返回稳定错误码。运行方法返回 Run，不阻塞到完成；进度通过订阅事件或 CLI JSONL 输出。

## 版本化事件

事件 envelope 至少包含：

```json
{
  "schema_version": 1,
  "event_id": "uuid",
  "run_id": "uuid",
  "sequence": 1,
  "occurred_at": "UTC timestamp",
  "type": "plan|state|progress|metric|result|final",
  "payload": {}
}
```

同一 Run 的 sequence 单调递增。adapter 不得重新解释 payload。旧 Go JSONL 的 `plan/progress/final` 在迁移期映射到该协议。

## Report Schema

Report v1 必须包含 schema version、ID、Run ID、生成时间、Plan snapshot、Model、Channel、环境、结论、SLA、指标、时间线、分布、结果、错误簇、Evidence、Baseline 和附件。Metric 必须携带单位和样本数。

JSON 是权威数据；HTML 是权威视觉渲染；PNG 与 PDF 从同一 HTML/CSS 和筛选上下文生成。禁止每个格式独立聚合指标。

## 持久化与 migration

- `schema_migrations` 是权威版本记录，`PRAGMA user_version` 仅作快速探测；
- migration 使用事务、固定 checksum，未知旧 schema 或 checksum 不匹配时停止；
- 迁移前创建一致性备份，迁移后执行 foreign key 和 quick check；
- 首次 migration 只接管现有 `runs`、`run_results`、`audit_runs`、`audit_case_results`，不改写历史 ID；
- 新领域表在后续 migration 创建，并通过显式 import/cutover 迁移旧数据；
- SQLite 只保存 artifact 相对路径、SHA-256 和索引。

## 凭据边界

Credential Service 是唯一能够读取秘密的应用边界。读取只允许返回短生命周期 lease 给当前 Run；GUI、CLI query、SQLite、事件、日志、报告和诊断包永不返回完整秘密。Channel API Key 与 Integration 管理员凭据使用不同 purpose，不能互换。
