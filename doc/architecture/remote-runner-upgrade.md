# 远程 runner 的显式升级契约

本文描述当前正式升级实现，供桌面使用者与维护者核对操作、恢复和验收边界。

## 触发条件与共享节点

- 连接、保存连接、后台监测和任务观察不会因为客户端发布版本或构建不同而覆盖已有 runner。
- 当前管理协议、任务格式与所需能力相容时，不同发布构建的客户端可以共享同一节点；不要求 client build 与 runner build 精确相等。
- 已有初装 Provision 路径继续只负责初装或复用。升级必须由独立、显式的升级动作触发。
- 升级保留服务器 NodeID、InstallationID、现有 token 和任务所有权，不重新配对，不删除节点或重置数据库。

## 桌面调用契约

| 方法 | 输入 | 行为 |
| --- | --- | --- |
| `StartRemoteNodeUpgrade` | `id, expected_revision, operation_id, expected_build_id` | 先持久保存本地 intent，再异步执行升级 |
| `GetRemoteNodeUpgrade` | 本地配置 `id` | 只读本地 intent；没有 operation 时返回 `null`，不联网、不启动任务 |
| `RetryRemoteNodeUpgrade` | `id, expected_revision, operation_id` | 查询原 operation 后，显式继续同一 operation |
| `RecoverRemoteNodeUpgrade` | `id, expected_revision, operation_id` | 按原 journal 的恢复边界恢复或确认同一 operation |

调用方在首次 Start 前生成一个 UUID，丢 ACK、网络失败和重试都保留该 `operation_id`。
Start 结果不明时先 Get；Get 明确无 intent 后，才可显式重发原 Start 输入，不能自动换 ID。
已经确认的 `completed` 与 `rolled_back` 是终态，不会因重复 Retry 而重新执行。

`RemoteNodeUpgradeView` 固定包含以下 13 个字段：

```text
configuration_id, node_id, installation_id, operation_id, stage, error_code,
expected_build_id, target_build_id, actual_runner, resume_attempted,
restorable, confirmed, changed_at
```

`configuration_id` 是本地配置 ID；`node_id` 是物理 runner ID，两者不得混用。
`actual_runner` 可以为 `null`；存在时，其节点与安装 ID 必须与 View 一致，版本和 build 来自实际 runner 的确认记录。
`GetRemoteNodes` 的每个节点包含必填的 `upgrade: View | null`。
`confirmed` 仅表示已取得并验证服务器 journal，不表示 worker 仍在运行。
关闭面板只停止前端观察；重启客户端后可显式 Retry 同一 operation。

`GetRemoteNodeUpgrade` 始终纯 local。既有显式 `CheckRemoteNode` 遇到 pending 升级时，先在单次 10 秒预算内只读同 operation 的服务器 journal 并更新观察；disabled 节点只有存在 pending 升级时才允许这次显式读取，后台 Run 仍跳过 disabled 节点。该刷新不占升级 actions 槽，保留 typed timeout 分类；不会隐式 launch、准备 artifact、Provision、Retry 或 Recover。四项升级 API 及当前 DTO 不变，显式 Check 不是第五个升级动作。

## 执行路径与持久记录

调用路径为 DesktopApp → remotenodes.Manager → SSH Client → detached systemd 升级服务 → runnerinstall。
服务器 CLI 只有当前请求格式，三个入口均从 stdin 接收请求：

```text
llm-test-runner upgrade
llm-test-runner retry-upgrade
llm-test-runner recover-upgrade
```

请求的精确字段为 `contract, operation_id, node_id, installation_id, expected_build_id, artifact_sha256`。
未知、缺失、重复字段及不支持的 contract 明确拒绝。凭据不属于请求；SSH 使用已有授权 credential lease。
SSH 密码、私钥、provider 凭据及 token 不进入升级 DTO、日志、命令参数、环境变量或客户端暂存文件。
stage 中的请求文件仅包含上述非秘密标识与摘要；服务器原有私有配置按受保护的备份边界处理。

固定记录与目录如下，路径都由已验证 UUID 派生，不接受任意用户路径：

- 本地 intent：`<nodes.json>.upgrade-<configuration_id>.json`，与现有节点配置格式独立。
- 服务器 journal：`/var/lib/llm-test-runner-install/upgrade-<operation_id>.json`。
- 共享安装 gate：同目录的 `active-upgrade.json`。
- 上传 stage：`/var/tmp/llm-test-runner-upgrade.<operation_id>`。
- 受保护备份：安装目录内的 `upgrade-data-<operation_id>`。

服务器复用安装互斥锁和 runtime 互斥锁。未终态的 active operation 阻止其他客户端或初装流程修改安装。
同 ID 的请求必须完全一致；`expected_build_id` 对当前选择的旧程序执行 CAS，不能覆盖已被其他操作更换的构建。
上传核对独占文件、目录所有权、权限、大小和 SHA；完整文件不得被未知内容覆盖。
升级服务脱离 SSH 观察连接运行。关闭客户端不会停止该服务或取消共享 Job；本地 intent 留待同 ID 恢复。

## 正向流程与 Pending 竞争

1. 核对身份、安装、旧 build、当前协议与平台，并持久保存 operation。
2. 初检 `Pending > 0` 时返回 `installation_busy`，不改变运行或维护状态，不停止服务、不备份或修改业务数据；失败 journal 可以保留。
3. 使用同一 operation 进入 maintenance pause。若初检与 Pause 之间已有新 Job 被接受，保留同 op 的暂停门并明确报 busy。
4. 此竞态下，已接受任务继续执行；不 Cancel、不 Stop、不还原数据，也不立即 Resume 后尝试重新 Pause。
5. 等待 `Pending = 0` 后，显式 Retry 沿同 op 继续；显式 Recover 可在满足条件后恢复原 runner 并结束该 op。
6. 持久记录停止意图，再 Stop；取得 runtime 锁，证明无其他运行进程占用业务状态。
7. 对当前格式数据库做包含 WAL 的一致性备份，验证精确 schema、完整性、类型化业务事实、摘要与备份内容；同时保存可验证的原程序、配置和 unit 备份。
8. 选择固定新 SHA，启动仍处于同 op maintenance 的 runner，验证当前 schema、身份、实际 build、health 与 `Pending = 0`。
9. 在 Resume RPC 前持久保存 `resume_attempted` 且撤销恢复旧 DB 的许可；同 op Resume 后验证 Ready，再写 `completed` 和实际 runner receipt。

UI 必须区分初检忙与暂停竞态：`checking + installation_busy` 表示等待任务完成后重试；
`pausing + installation_busy` 表示“节点保持维护，等待已接受任务完成，可重试或恢复”。
完整阶段为 `uploading`（本地）、`checking`、`pausing`、`stopping`、`stopped`、`backing_up`、`backed_up`、`cutover`、`verifying`、`resume_attempted`、`completed`、`rolling_back`、`rolled_back`。

## 重试、错误与恢复门槛

- 丢 ACK 后先查询原 journal；不能以 SSH exec 成功代替 receipt，也不能因查询失败再次提交新 operation。
- 已有同 op stage 可在严格验证原请求与程序 SHA 后继续使用；本地 release 被移除不授权换程序、补造文件或更换 operation。
- `installation_busy`、`installation_operation_conflict`、身份冲突、升级格式不支持和 artifact/integrity 错误均为固定安全分类，不回传原始 stderr 或凭据。
- 在尚未尝试正向 Resume 且 journal 允许恢复时，显式 Recover 可以验证备份、恢复旧状态、验证旧 runner，并用同 op Resume，最终记录 `rolled_back`。
- `resume_attempted` 后只允许确认或继续新 runner，禁止自动或显式恢复旧业务 DB；新 runner 已停止时可幂等启动并重新验证。
- 恢复旧 runner 自身也有不可逆边界：旧 runner 的 Resume 前必须持久保存 `restorable=false`。
- 旧 Resume 丢 ACK 后，Retry 不得重新正向升级或复用旧备份；Recover 只确认或继续原恢复，不再 Stop/Restore 已恢复服务的数据。
- 因而 `resume_attempted=false` 单独不足以证明可以恢复旧 DB，必须同时审阅阶段、`restorable` 与确认 journal。
- 确认过的终态、Resume 与回滚恢复事实不会被较旧的传输返回覆盖；不可逆事实不依赖服务器时间戳先后。
- 未知文件、身份变化、校验失败或不确定结果应保留 stage 与 journal 供审阅；不得手工删除 gate、改 journal、换 ID 强行重试或在在线节点覆盖 DB。

## 数据与验收边界

仅接受单一当前 SQLite schema 与当前业务格式；历史或未知格式返回升级要求并保留原数据。
本升级功能不包含历史读取器、运行时迁移、数据转换或节点数据删除；shared/foreign独立功能本轮改变了desktop/runner共用SQLite的唯一current checksum，不能把该变更当成本升级流程会自动处理的数据转换。

已验证：Windows Go 全仓测试及 vet、相关最新回归、Linux 交叉编译与 WSL 测试、Wails 2.15.0 bindings 生成，以及独立代码复核。
覆盖双锁/CAS、same-op 丢 ACK、current schema/WAL 备份、身份 token 保持、Resume 前后恢复门、旧 Resume 丢 ACK、journal 回退、关闭与脱敏边界。SSH staged-digest 修复已本地提交 `86d631692c374295c1ddf5ed6b59d17eade56cb1`，集成全仓 Go 测试 55718 exit 0（desktop 53.965s、remotenodes 3.661s、SSH 2.782s），全仓 vet exit 0；后续 clean476 客户端已使用该修复在实机执行新 operation。
维护竞争联测使用真实 `Maintenance.Apply` 和 current SQLite，并在端口注入 Pending 竞争；不是实机并发 Job 或 provider 验收。
此前 bf9106 操作的 Start、两次 Retry 和一次 Recover 已在真实 SSH/systemd 节点执行，但四次客户端原观察均 false；不能再记为未跑，也不能记为正式产品验收通过。独立运维当前 CLI recover-upgrade 随后恢复原 R4，当时服务器 journal 已 rolled_back、R4 服务健康，只读 typed 数据及既有身份/文件检查与升级前一致；该结果不替代 Desktop API Recover 验收，backup_restore_verified 仍为 false，也不声明物理 DB 或私有 executor key 字节相同。父端全文主审与 self-test 在实跑前，独立 reviewer 复核在实跑后，不改写审查先后。

新 operation346da881 仅 Start 一次，初轮六分钟客户端 false 保留；同一 Start 后台已 completed，R7 服务 ready/healthy，独立 actual backup/基线/current typed 核验、两个原免费 Mock 报告 query 恢复，以及重开 Desktop 组合的 Get/Check 纯查询均已执行通过。原报告保留原 R4 执行来源，未被当前 R7 覆盖；230 案例报告齐全不等于 K3 兼容性通过，旧 K3 和 bf 操作失败不改判。

只读观察工具 v1 误把顶层 Configuration 写入序号锁为 immutable 而 false；v2 在只排除 Observation 和顶层 writeRevision、锁定 nodeRevision 及全部 binding/pin/authrefs/default/meta 后实跑通过，完整 intent 不变。v2 fresh bound Check 命中本地终态，check_calls1/journal_reads0，不能称再次读 journal；没有升级执行动作。此纯查询结果本身不证明真实OS退出/重开或native窗口；后续单Mock真实进程验收另记。

后续 priority c2d39bb 已本地提交并通过全仓 Go/vet；真实验收 37845 以 submit/audit/reopen 三个 OS 进程验证一个免费 Mock 任务在 submit 实际退出后继续、封口并重开恢复，整轮 180 秒预算内观测 37369ms，事后独立复核无 blocking。该观测值在 summary 写盘/owner.close 前记录，不是 wrapper 总生命周期。runner 仍为原 R7 源 3ca/1b94…，本轮未重建/再次部署；当前 client 共享输入另增 2 个 priority 文件，不能声称与 3ca 全同。

本轮 sample1/success1、Launch1、paid0、daemon 进程不变；nativefalse 且使用 scoped MemoryStore，未验证 OS keyring 或真实点击关窗。完整大任务/多 client 公平性、foreign 视图/控制、文件资源、完整还原/故障回滚、native、历史库、重启、第二节点/arm64 与 CI 发布仍待交付；不从单样本推算与旧恢复耗时的提升倍数。精确证据及所有原失败见[验证记录](remote-execution-validation.md)。仅接受当前格式，未转换或删除/reset 用户数据。

本轮 shared/foreign 已提交 626ccdd，增加 per-command job_control_targets 及 outbox 复合 FK，schema version1/name0001_protocol_runtime 不变，current checksum 已由 7afb439a…0708 替换为 `adc400effb974273c9d7d18dc8cc7f2deb7623cd958c7f80d61b8010dca4be6e`。新 runtime 拒绝旧 DB 且保留原数据；现有 R7/3ca 仍运行旧 schema，尚未重建/部署。独立 TEMP 工具已通过三类 7afb fixture 及实际历史 766d1aa…一致性私有副本的直接转换验收；原用户库只读不变，真实 desktop/runner 切换与恢复仍 pending，不能写数据升级完成。工具保留至实际升级/切换/恢复验证完成，证据和验证边界见[验证记录](remote-execution-validation.md)。

新 JobView 必填 nullable submission，foreign 只从 actual execution/receipt 取来源；ControlTarget 冻结真实 node/install/spec 与已知 acceptance，历史 executor R4 与 daemon R7 合法，不比较 build 相等。GetJobControls 仅本 client 持久投递记录，foreign pending 控制通过真实 mirror anchor 保留并阻止节点移除。当前四项升级 API、Get 纯 local 和显式 Check 语义未改变；七个 Job Wails 绑定名称不变，新 Job DTO 和共用 schema 是 breaking 变更，不能用旧 reader 或自动迁移绕过。

shared/foreign 本地 Go 全仓/模块 review、FE 与浏览器 fixture 已通过，真实新 shared 控制、native 及数据切换仍 pending。现有 R7 升级、旧 query 和 c2 进程验收只证明其原 schema 阶段，不代表 adc 新 schema 已部署；当前节点数据已因后续 Mock 增长，旧 10Job 基线不是恒定现状。全部历史失败与其他交付范围保留，证据见[验证记录](remote-execution-validation.md)。

后续 r2/source519 实机共享验收 4278 exit1，观察止于 B 发现阶段，foreign 控制未执行；75284 纯 GET 仅确认原 Job d1cce52e 后来 completed、封口结果与原身份一致，原 false 不变，不是重试或升级通过。clean626 历史转换副本已用生产 DesktopApp 初始化、查询、关闭、重开两轮验证 61reports/43perf/4comparisons，母库与原用户库均未改；仍无真实切换/恢复、native 或 OS keyring 验收，TEMP 工具继续保留。quota/content 的 Linux 集成测试已通过，但内部文件基础尚无 HTTP/附件/release，未新增 schema，也未部署到当前 R7；本轮 scheduler 尚未验收。证据边界见[验证记录](remote-execution-validation.md)。

后续 scheduler/priority SQL 已提交 `a91f1d8e20cfa2c97a2b55536dd57a0518135e18`：独立两分钟 session/五秒 tick、节点四槽和 priority/history 份额、单调 tail/固定高水位 history，以及只读小 snapshot priority 查询均完成本地实现与 Windows 全仓 Go/vet、真实 WSL Linux 回归。schema/wire/FE/DTO/Wails 无变更，不新增转换要求，也不代表重跑 native；CGO0 验证不是 race。当前 R7 未部署本阶段构建，既有 adc 用户库真实切换/恢复仍 pending；后续 r3 实机27088 exit1，已冷导入并发现新 foreign，尚未执行控制/终态/重开验证，原 r2/r3 false 不追认。P1 文件上传在其他工作树实施，尚无完成或 assets 能力证据，其他交付范围保留。

r3 两个独立 client 仍连接原 R7，一次 Launch/Submit 的新 Job50620b5e 已由 B 生产 watcher 发现；R8 仅为 client label，不是 runner 升级。wrapper 随后失败，commandID 为空，cleanup/mock close 未完成、原 mock_process_after:null；只有 before 进程证据，不推断 runner after 恒定或优雅关闭。后续r4/session37618已执行，止于B13历史冷导入400.022s超时，未创建A/Mock/新Job/control；原false不改，原Job不重投、不补发控制、不重跑付费K3。离线工具复现不作为原失败唯一根因，具体证据见[验证记录](remote-execution-validation.md)。

后续 P1 内容上传由 secondary `1204270b8be90994c5cdfdb9cdb45051893e40c5` 整合为 main `45127094355757cc8ffb4d6c170271ff8ea05088`，新增既有 Features 内的可选 content_upload，无必填 RunnerInfo/DTO/FE/Wails/schema 变化或额外数据转换，也不要求client/runner同build。content_upload只证明内容PUT，assets仍false、非空Job assets仍422，未提供HTTP asset GET/任务绑定/附件/manifest/release，P2–P5闭环尚未交付，原R7未部署新构建。

上传与pause在serviceMu内原子互斥；活跃上传返回严格当前SafeError409 upload_in_progress/retryable且不确认pause，installer仅将该busy码映射ErrBusy；暂停先赢新上传423，无任意旧409 fallback。upload10分钟/bulk header2分钟和固定management/bulk池角色不扩大管理JSON30秒及原pool25秒/2分钟策略；关闭join和源释放有本地回归。初冻结Windows全仓、最终client/SSH和受角色限制的Linux包验证通过，主a91+P1全仓86996已test/vet exit0，616 Go/module输入前后逐一不变；31秒仅真实Linux进程+TCP，非跨SSH或1 GiB实测。后续r4实际失败与只读诊断分开记录，不追认旧结果，证据见[验证记录](remote-execution-validation.md)。

r4保留副本13个镜像齐全，但d1cce52e两条文档仍pending、d629停在本地running/rev3/seq400；一次GetJobs+13GetJob+13ReadJob共13.006244s全绿，原库与副本SHA均保持58c88c6f…6e93a。它证明已保留数据可用当前getter/typed读取，不是历史导入完成、升级恢复或真实服务状态的新证明。

客户端SSH response纯EOF透明修复由secondary689d58e整合main `868351809964a495d8758dbb44ae3d255304e8aa`：actual DownloadReport/PerformanceArchive六组合在exact451均ErrIntegrity RED、a91均GREEN，修复后Windows/WSL root client和SSH回归、独立review及clean868全仓Go/vet通过，617输入摘要不变。自动cancel/close/join保持，显式Close和断流不变为成功，不改hash/EOF完整性检查、期限、重试、upgrade请求/journal/schema或数据转换，也不表示原R7已部署新构建。通用直接gzip回归不同于生产wireReadAll后Multistream(false)，不能解释全部r4缺口或唯一归因d629seq400。

r5 exact868/session82376/run-vvyq5fzo已exit1，400秒cold baseline再次超时，未进入A/Mock/新Job/control；B34176父级清理后实际exit1，进程elapsed400075ms、cleanup_completed=true，BDB56012800字节。初始13个ID存在于cold_import事件，summary空数组只因未到baseline_ready；原r5 false保留，不预判其pending/getter原因，也不能由EOF修复追认全部cold/seq400已解决或共享/升级验收通过。r5终态记录时P2a仅独立提交1467e19f，后续主整合见下段。assetsfalse、native/keyring、真实用户库切换/恢复与其他待验边界保持，全部原始失败记录不覆盖。

P2a的49文件现已整合main cbef518，完成严格当前Input/SuiteInput/FileBinding及Clone、Go ValidateInputs改返回ResolvedInputs、会话有界selection和CreateLocalFileSelection/ReleaseLocalFileSelection两个绑定。主Wails CLI2.15.0核验/generate、FE757测试/tsc/lint/build与clean主树全仓Go/vet通过，既有warnings保留；完整输入与证据只列于[验证记录](remote-execution-validation.md)。这不改变schema/actualDB/转换或原R7部署，也无native/browser/physical/provider证明；fileLaunch仍禁，Stage/执行编码/capture/wire metadata/KindPerformance.native_case、P3真实admission/pins/resolver与P4 manifest/release均未交付。P3 types/ports与16表SQL基础正在别树实施，不声明新current checksum或升级已完成；原r5 false及旧升级、数据切换/恢复边界不变。
