# 远程执行验证记录

## 2026-10-16：首台真实节点前置检查

用户授权在其提供的服务器和渠道验证完整流程。节点在本文标记为 `test-amd64-1`；密码和渠道密钥未写入仓库、远程文件或报告。服务器地址由会话保管，不作为产品内置默认值。

| 项目 | 观测 |
| --- | --- |
| SSH | 用户指定端口连接及认证成功，后续连接核对首次取得的 Ed25519 主机公钥摘要 |
| 环境 | Ubuntu 22.04 LTS，x86_64，Linux 5.15，PID 1 为 systemd |
| 资源 | 约 64 GiB 内存，根目录约 191 GB 可用；有 Python、curl、tar、sha256sum；未安装 Go |
| 现有服务 | 已有 HTTP、HTTPS 及其他监听；没有匹配的 llm-test runner unit |
| 既有系统状态 | systemd 为 degraded；唯一失败项为 fwupd-refresh.service，未修改或重启 |
| 渠道发现 | 从该节点发起模型列表请求，HTTP 200，模型列表包含 kimi-k3 |
| 单次渠道冒烟 | 从节点发起 OpenAI Chat 流式请求，限制 max_tokens=64；HTTP 200，收到内容、stop 和 [DONE] |
| 冒烟用量与时延 | 提供商返回 input=90、output=2、total=92 tokens；从开始到首帧约 2986 ms，流结束约 2987 ms |

此处生成请求由临时 Python 进程通过 SSH stdin 发起，用于确认节点到渠道的真实网络链路；不是 runner 的任务接收、重连或桌面闭环验收。没有安装服务、修改现有监听、执行服务器重启或迁移用户数据库。amd64 前置检查也不能证明 arm64、多节点或自动部署完成。

## 2026-10-16：真实 amd64 节点共享核心验证

在 `test-amd64-1` 上传并运行 Linux 测试二进制，源码提交为 `6a098f4`，二进制 SHA-256 为 `2fbc338ca32cbb63ec760a2beba065e50e2f01eecc35b31471c1c2043647db9d`。上传后重新核对摘要，再执行以下测试选择：

```text
TestPerformanceExecutor|TestSlowProgress|TestCoreShares
```

第三轮退出码为 0，用时 85521 ms，全部选中测试通过。测试在节点上使用真实 Core、节点临时 SQLite 和可控 HTTP 服务；没有消耗提供商额度。上传的二进制和其临时目录已移除。

| 选中测试 | 证据范围 |
| --- | --- |
| CoreSharesOneHostQueueAcrossAllJobKinds | Run、Comparison 和 Performance 共用队列，等待活动槽，实际请求与各自报告齐全 |
| PerformanceExecutorUsesRealCoreForEveryLoadPhaseWithoutSubmissionContext | 测量、预热、爬坡、容量、open-loop 和随机输入，提交上下文结束后仍完成 |
| PerformanceExecutorStopAndCancelPreserveActualPhaseObservations | 四流量阶段的停止与取消保留实际观察，不进入后续阶段 |
| PerformanceExecutorConfirmsLostWritesWithoutRepeatingTraffic | 观察、完整结果和统计归档确认丢失后回读，未重复请求或事件 |
| PerformanceExecutorCheckpointFaultIsolatesBeforeSettlementAndRecoversWithoutTraffic | 四阶段落盘故障立即隔离，保留部分容量，无请求恢复与稳定封口 |
| PerformanceExecutorRejectsChangedArchiveResultAndFrozenInputWithoutMutation | 拒绝错配归档、typed 结果和冻结输入，不修改权威事实 |
| PerformanceExecutorArchivesFailedProviderMeasurementsWithoutLeakingCredential | Mock 错误样本保留，回显的测试凭据被脱敏 |
| PerformanceExecutorRemoteShutdownAndDeadlinePreserveOwnerFailure | 宿主 Close 与执行时限保留各自原因，实际请求被中止 |
| PerformanceExecutorQueuedCancellationSealsWithoutTrafficOrStatisticalArchive | 排队取消没有请求或伪造的统计归档 |
| SlowProgressStorageDoesNotBlockMeasuredTrafficAndIsJoinedBeforeSettlement | 展示进度存储端口阻塞时调度继续；终态等待写入退出，失败后隔离 |

前两轮失败记录已保留在本地验收产物：首次包含过短的测试时限，第二轮暴露同步进度落盘阻塞发送和普通测试执行预算不足。修复后第三轮通过；分位数相同样本的浮点顺序问题另由固定样本回归及完整负载重复测试验证。测试预算调整只影响 fixture，生产节点默认执行时限仍为 60 分钟。

这是共享核心在真实 Linux 上的验证；测试二进制没有常驻管理接口，不能当作独立 runner、systemd 保活、SSH 自动安装或真实 Wails 验收。客户端版本差异、共享授权和安装互斥也仍需 R15 的独立进程验证。

## 2026-10-16：管理接口本地与 Linux 验证

节点级 token、当前协议头与能力检查已通过真实 JobHost、Core、临时 SQLite 和 Mock HTTP 验证。两个独立 HTTP transport 使用不同客户端构建和相同节点授权，共用同一队列；接收后连接结束和响应写失败都不撤销执行，同一提交重传不重复发请求。控制命令重传返回原确认；非法 path/body 与超限请求不写命令记录。

任务清单按稳定接收序列分页，不受相同接收时间或其他 owner 的序列空洞影响；末页后新增仍可找到。Run、Comparison 子报告与 Performance 完整结果下载核对原始字节、摘要及所属任务。Run 报告还在同一读事务核对冻结输入、实际存储事实和唯一日志引用；损坏摘要会明确拒绝，读取不修复数据。

相关 Go package 测试与 go vet 通过；管理接口 TestHTTP 另由 Ubuntu 24.04 WSL 执行 Linux 测试二进制通过。这一步没有改变 SQLite schema 或既有报告格式，没有升级实际用户数据。WSL 与本地 Mock 验证不证明物理服务器出口、独立常驻进程、systemd、SSH 安装或 Wails 交互；真实渠道最终验收仍待 R16。

## 2026-10-16：独立 Linux runner 进程

新增 Linux runner 的 init/serve/version 入口。init 通过 stdin 接收节点级 token，配置保存其摘要和独立的任务凭据承诺密钥；配置使用私有文件，不覆盖已有安装身份。运行时持有独立 flock，使用节点 SQLite、共享实际 Core 和 loopback HTTP，不依赖 Wails 或客户端进程。运行 inode 的 SHA-256 作为实际构建身份。

Ubuntu 24.04 WSL 的独立进程测试验证：两个进程不能同时持有状态目录，强杀后锁自动释放；已发出一条 Mock 请求后强杀，重新启动会保存 runner_interrupted 及部分 Performance 观察，下载结果可用且没有第二条上游请求。CLI 重复初始化不改配置；未知配置 contract 明确提示升级、保留文件且不创建数据库。

实际子进程向测试 Unix datagram socket 发送 READY 和 WATCHDOG。周期磁盘检查发现超预算后进程以失败退出；只读 SQLite 的写探针失败且不改任务与日志。写探针只验证 writer admission，空 UPDATE 不证明脏页已落盘；磁盘空间与状态文件预算独立检查。预算测试使用 sparse 文件，不是物理磁盘写满试验。

私有文件测试发现 os.Root.OpenFile 会在 root 内解析 symlink；现使用固定目录 descriptor 上的 Openat/O_NOFOLLOW 强制拒绝私有文件 symlink，回归通过。测试目录原有宽权限也按实际状态纠正为独立私有子目录，生产权限检查保持严格。

Windows 的配置、SQLite、Host、管理 HTTP 与桌面 Go 回归，以及针对改动的 go vet 通过。runnerstate、runnerprocess、runner CLI 与 writer 探针的 Linux 测试二进制在 WSL 通过；其中包含实际子进程。两种 Linux 架构构建均成功：

| 构建 | 本轮二进制 SHA-256 | 验证范围 |
| --- | --- | --- |
| linux/amd64 | af235918cc417e8f4ce26dc21154ab3fca7bd1b4d13b44d0410b376f02049bd2 | WSL 执行 version，返回的实际 inode 摘要与构建文件摘要一致 |
| linux/arm64 | 814b93ff9c0851b11f08d064232af0b5e2c906b545ccb6344460bd128cd5b34c | 仅交叉构建，尚未在 arm64 节点运行 |

二进制为本轮默认 dev 构建，验证后临时文件已移除，不当作已分发的发布产物。unit 模板经 WSL systemd-analyze verify 接受；为检查模板语法将临时副本的 ExecStart 替换成 /bin/true，输出还包含当地既有 netplan-ovs-cleanup.service 的权限警告。没有安装或启动本产品 unit，物理服务器验证仍待继续。

这一阶段未改变 SQLite schema 或既有报告格式，没有接触实际用户数据，没有执行历史数据升级。systemd unit 当前为模板；SSH 自动安装、安装升级锁、维护接收门、asset 文件处理和桌面入口仍待实施。当前证据不表示已完成物理服务器部署、systemd 保活或 R16 真实渠道验收。

## 2026-10-16：升级维护门

管理接口新增当前 installation 的 pause/resume，暂停确认与 Submit 在同一 Host 锁边界内生效；已接收重传仍能获得原确认，后续新任务返回 423。Pending 包括报告与资源收尾，不能以 Active=Queued=0 代替安全停机条件。暂停保持 Healthy 和 watchdog，只有 Ready 关闭。

维护记录原子替换并同步文件和目录。审查发现 rename 后目录 fsync 失败仍可读到新值，因此写错误不能仅回读就确认；当前实现会补做文件及目录同步，仍失败则隔离 Host。已验证可见但不可持久确认的记录不获成功确认，避免重启意外开放接收。

本地当前契约、Host、管理接口、配置、SQLite 和桌面 Go 回归及相关 go vet 通过。WSL 实际子进程验证 pause 后正常退出、重启仍保留 owner 并拒绝新任务，READY/WATCHDOG 继续，原操作可恢复。未知维护格式在数据库访问前拒绝，保留维护文件和哨兵数据库原字节。私有可变文件拒绝 symlink 替换且保留目标。Linux arm64 交叉构建通过，仍未在 arm64 节点执行。

RunnerInfo/Host Status 当前字段增加 Healthy、Pending 和 MaintenanceOperationID，生产者与本仓库消费者同步更新；没有旧元数据解码分支。本阶段未改变 SQLite schema 或报告格式，未接触实际用户数据。维护记录只保存最近操作，不能声称历史安装操作全局幂等；安装器仍须持有节点安装锁并维护操作日志。SSH 安装、物理节点 systemd 与最终 R16 尚未完成。

## 2026-10-16：SSH 与 SFTP 传输

SSH 传输支持密码、未加密及带口令私钥的内存认证，先确认主机公钥再认证；固定指纹同时记录 key type，只协商相应安全签名算法。实际接入时核对了同一服务器可提供不同类型公钥的情况，明确使用先前核验的 Ed25519 类型与同一指纹，未跳过主机校验。固定只读 probe 回读 uid、Linux 架构、PID1 与 systemd 版本以及安装所需基础命令。

HTTP 使用 direct-tcpip，不开放客户端监听端口、不使用代理或跟随重定向，只能访问指定的 runner loopback 端口。审查发现 x/crypto 的 DialContext 取消不会终止底层等待通道确认的 goroutine；现由调用者在建立超时或取消时关闭父 SSH 并等待返回。同时将原始 HTTP 请求取消穿过 net/http 的独立 dial context，定向回归由失败转通过。已建立的连接和 runner 已接收的任务没有依赖握手 context。

使用用户提供的真实 amd64 节点完成固定主机验证、密码认证和只读探测：uid=0、Linux amd64、systemd=249 且 PID1=systemd，具备 sha256sum/runuser。通过 SFTP 在专用临时目录上传 184320 字节非秘密 fixture，SHA-256 为 `1658ace3431830c281602d3a66e3c6aa04c47710431a3acf648f56d2ff3a279b`；文件 fsync、独立回读、错摘要拒绝和拒绝覆盖全部通过，实际测试耗时 20.82 秒。精确临时文件与目录已清理，没有安装或重启服务，没有发起提供商请求。密码仅通过隐藏输入与子进程 stdin 传入，未写入文件、参数或测试输出。

本地测试另验证秘密格式化与序列化拒绝、成功私钥认证、握手和命令取消、keepalive failure reply 保持在线与无响应关闭、固定命令输出限额。Linux SFTP fixture 检查私有权限和 symlink 拒绝，以及服务器不支持 fsync 时明确拒绝。上传只保证新文件与内容验证，父目录落盘、发布激活和安装操作持久性仍由后续安装器实现。

SSH package 的 Windows 和 WSL Linux 测试、相关 go vet、Linux arm64 runner 构建通过。Linux 握手 deadline 验证发现 socket 超时可能先于 context timer 被观察到，现把 net.Error timeout 统一归为 DeadlineExceeded；定向回归连续 10 次通过。没有运行 race detector。

`go test -p 1 ./...` 的其他 package 通过，data/cases 的 TestKimiResponsesBoundaryRejectionsReachProviderUnchanged 失败：现有负向 Case 只检查 HTTP400、无协议 issue 和 message 为字符串，不能拒绝 fixture 的 server_error。使用本次 SSH 变更前 HEAD 的 go.mod/go.sum 重跑 cache_mode_enum 同样失败，确认不是此次依赖更新引入。没有为通过测试而弱化断言；最终 R16 前仍须核对这些 Case 的提供商契约和判定，单独修复并验收。

新增 pkg/sftp v1.13.11，其依赖要求同步 x/crypto v0.54.0、x/sys v0.47.0 与 x/text v0.40.0；Wails 保持 v2.15.0。本阶段没有改变 SQLite 或报告格式、没有迁移实际用户数据。方案路径统一到已有 unit 的 `/opt/llm-test-runner` 与 `/var/lib/llm-test-runner`；root 专有配对授权与安装日志的设计已补充，尚未安装落地。桌面节点管理、OS Store 专用用途、实际自动安装与 R16 仍待完成。

## 2026-10-16：服务端首次安装与兼容复用

独立 runner 新增 provision 子命令，输入只有当前契约、操作 UUID、产物摘要和端口。首次配置生成节点级授权，root 配对目录与 runner 配置分别持有原令牌和摘要，其他客户端接入不轮换授权。服务端安装锁独立于运行进程锁，操作阶段和完成确认持久保存；同一操作重试返回原确认，客户端须另做实时健康探测。

安装器拒绝未知程序、状态或安装目录，保留拒绝的数据；已有兼容 runner 直接复用，客户端带来的不同产物不切换 current。程序目录先在私有暂存目录内持久化归属标记，再整体原子发布；程序、配置、unit、配对和操作记录均核对文件及目录同步。配置初始化确认丢失通过回读既有身份解决，不重置状态。systemd 检查 Restart、watchdog、启动限速和已加载配置，回读实际进程 UID/GID、附加组及正在执行的 inode 摘要。

Windows 当前安装输入与操作记录测试、Linux installer/CLI go vet、Linux amd64 测试在 WSL 通过；包含首次安装、不同构建复用、共享安装锁、部分启动重试、未知路径与配对丢失拒绝、进程和服务策略检查。Linux arm64 交叉构建通过，尚无 arm64 实机执行。本轮 systemd 操作由 fixture 驱动，不能替代物理节点安装与保活验收；SSH 安装编排、显式升级备份切换及 R16 尚待完成。

本阶段没有改变 SQLite schema 或报告格式，没有执行实际用户数据升级，没有在用户服务器安装服务或调用付费渠道。

## 2026-10-16：SSH 自动部署与真实共享节点

SSH 客户端现可上传 Linux 产物并调用固定 provision 子命令；请求仅通过 stdin 传入，进度和完成确认均核对操作 ID 与当前字段。连接已有节点只读取 root 专有配对与身份文件并实时 GET /runner，不要求客户端构建等于 runner。连接关闭释放配对 lease 和 HTTP 池，重复连接不会耗尽池额度。当前 RunnerInfo 的所有字段和嵌套字段都必须显式存在，拒绝 null、大小写变体、重复及未知字段；这不增加历史元数据解码路径。

用户 amd64 服务器已从无安装状态配置为专用 uid/gid 998 的 systemd 服务。实机发现并修复：首次创建账户时局部 err 遮蔽了成功的再次查询；systemd 249 在从未启动前的有效 WatchdogUSec 为 infinity，必须先核验已加载 unit 的 WatchdogSec，启动后再核验有效 30s。活动服务无论是否 enable 都执行严格运行时检查；拒绝 drop-in 覆盖，并只读确认全局 service-watchdogs 已启用，不改变全局设置。

第一次 19570550 字节上传因串行包往返超过 2 分钟，尚未执行安装器；改为 512 KiB 缓冲、每文件最多 16 个请求后完整产物上传/独立回读通过。超时留下的准确暂存目录已重新连接核验所有者、权限和全部内容后清理。后续失败保留专用账户、程序与配置，以新操作记录继续，未重置数据。未把这些重试描述为修复后的全新机器一次成功安装；第二台干净机器与 arm64 仍待验证。

最终 TestLiveProvisionSharedRunner 通过，耗时 82.94 秒。两个独立 SSH 客户端回读同一 node_id `55e50d9b-b900-4b5e-9fb6-c4fee2bc17be` 和 installation_id `2ec0bac4-20bc-4765-b256-cad839f1c5a1`。另一产物摘要 `2f02df586dd69801b68d94e80b08ed154b843821a01956444223436e1fb94f3c` 通过 installer 兼容复用，连接前后 MainPID 和 NRestarts 不变。实际保留的 runner 为验证构建 `remote-install-3fdc7b4-fixed`，执行 inode SHA-256 为 `9f59901c370b279208269d3a4ec14e3efca528d1feddb992ba4f046aba44ce38`；不以最新上传产物或客户端构建冒充实际执行构建。

维护 pause 确认且 Pending=0 后，仅对产品主进程发送 SIGKILL。systemd 自动恢复为新 PID 173242，NRestarts=1；installation、配对和暂停 owner 保留，WATCHDOG 时间戳继续推进、有效时限为 30s。原维护操作恢复后 Ready=true、Healthy=true、Pending=0。该试验没有重启服务器，没有验证宿主重启后的自启动。最初强杀验证用了该 systemd 不支持的参数，恢复接收后改为其支持的 --kill-who 再执行上述完整成功验证。

Windows jobcontract、SSH、installer、credentials 测试及相关 vet 通过；完整 SSH 测试和 installer 测试在 WSL 通过。root WSL 的 BootstrapCleanup 用例验证未知文件保留、公开权限文件拒绝，以及全部校验后才清理准确私有路径。Linux arm64 交叉构建通过，尚无 arm64 实机验证。全仓 Go 的既有 K3 负向 Case 判定问题仍按上一节记录，未用这些定向通过结果宣称全仓通过。

本阶段没有改变 SQLite schema 或报告格式，也未升级桌面实际用户数据库；服务器运行时创建新的当前格式 runner 数据库。SSH 密码只经隐藏输入和进程 stdin，未保存于文件、参数或日志。未使用用户渠道 key、未发起提供商请求；桌面节点配置、默认远程、全部入口与离线报告、显式升级和最终 R16 仍待接通。

## 2026-10-16：桌面节点配置与凭据生命周期

新增独立 nodes.json 当前格式，节点、默认位置和文档分别维护 revision；跨进程文件锁与比较 revision 防止多个桌面覆盖编辑。检查结果只更新观察信息，不使用户正在编辑的节点或默认位置失效。远程默认在离线、观察过期、禁用时保留，快照明确 Eligible=false；删除默认节点或未确认任务使用情况时拒绝删除。此阶段尚未装配桌面入口，删除使用检查端口缺省为拒绝。

SSH 密码、私钥、口令和节点 token 分别使用专用 CredentialPurpose，由 scoped Store 保存。nodes.json 仅保存非秘密引用；公开快照不暴露这些引用、token 摘要或凭据尾号。保存先持久化非秘密 intent，再写 keyring，最后提交节点引用；独立 GC 在锁内确认完整引用集已持久化后才删除未引用凭据。已取得的 SSH lease 不随配置轮换或 GC 改变。安装身份唯一，绑定后禁止静默换主机、公钥或安装身份；同一安装操作重试不能换产物请求。

Windows 和 Ubuntu 24.04 WSL 的 remotenodes 全部 16 项测试通过，另有未知格式子项；credentials/domain 回归与 remotenodes vet 通过。并发 CAS 定向重复 10 次通过。注入测试验证 keyring 写确认丢失、最终引用写失败、文件可见但持久确认失败、GC 删除后确认丢失都保留可恢复状态，不误删仍引用的凭据。未知、缺失、重复、大小写变化、null 和超大文档均拒绝且保留原字节。

Windows 根目录替换测试发现 Lstat 的文件身份可能延迟到 SameFile 时才读取；现改用打开句柄后的 File.Stat 固定身份，并在取锁前后复核。FileStore 要求配置根已经存在且持久化，拒绝自行创建未确认的目录链；生产 bootstrap 尚须落实该前提。本阶段没有创建用户节点配置、访问 OS keyring、改变 SQLite schema 或报告格式，也未执行历史数据升级。节点检查与自动部署编排、桌面按钮和全入口路由仍待继续；上述单元测试不表示最终远程 K3 e2e 已完成。

## 2026-10-16：双架构发布清单与下载缓存

新增 runner-manifest 工具，读取已经构建的 linux/amd64 与 linux/arm64 产物，记录单一当前格式、管理契约、明确发布标签、源码 commit/dirty 声明、文件名、大小和 SHA-256。release workflow 新增独立 runner job，两种架构使用 CGO_ENABLED=0、trimpath 和发布版本参数构建；检查源码目录干净，构建输出位于 RUNNER_TEMP，发布 job 等待桌面与 runner 产物后统一上传。manifest 的源码字段来自构建调用方声明；实际运行构建身份仍以执行 inode 摘要为准。

客户端下载指定 GitHub 发布或明确选择的本地开发目录，按发布标签完整摘要确定缓存目录与锁名，避免 Windows 大小写、尾随点和设备名别名。下载按声明字节数加一限制、流式计算摘要，并在原子发布前检查固定 64 字节 ELF 目标头；不会解析节表或解压可能超出预算的内容。缓存读取重新校验字节与目标架构，损坏或不支持格式明确拒绝并保留原文件，不自动换来源或覆盖；完整缓存可在源站离线后继续使用。

Windows 和 Ubuntu 24.04 WSL 的缓存、当前 manifest、来源与工具测试通过；覆盖并发只下载一次、离线复用、截断/超长/错摘要、架构错配、非 ELF、Windows 标签别名、异常节表声明、HTTP 取消、HTTPS 降级与重定向循环、未知当前字段拒绝。WSL 验证目录来源拒绝最终 symlink；Windows 因缺少创建 symlink 的权限跳过这一子项。相关 go vet 与桌面 Go 回归通过。workflow 经 YAML 解析和提取脚本的 bash -n 检查，未运行 GitHub Actions，也未推送或创建发布。

本地从当前工作树交叉构建真实双架构开发包 artifact-c1d-dev，manifest 明确 source_dirty=true，基线 commit 为 e34158d。Windows 与 WSL 都通过真实大文件缓存验证：amd64 为 13463714 字节、SHA-256 `f1ae809a1632f7cec2f0344530058f6fd0f5d80b95bc58ddac8d26c709461645`；arm64 为 12517538 字节、SHA-256 `d4f30c5dc5bd696c94ec8e9577b1ad48c0c0fd315e141b29633726bdfc27aaa4`。amd64 在 WSL 执行 version，实际摘要与清单一致；arm64 只有构建和头部/摘要验证，未执行。开发包仅保留于本机临时目录供后续编排验证，未上传服务器或发布。

没有调用用户渠道，没有改变 SQLite schema、报告格式或实际用户数据。节点编排和桌面选择仍未接入；发布和缓存验证不能代替远程执行 e2e。全仓既有 K3 Case 判定失败仍待最终 R16 前单独处理。

## 2026-10-16：节点检查与安装恢复编排

新增应用拥有的 Manager：保存即排入检查、启动扫描并每 15 秒独立检查启用节点，慢节点不会阻塞其他节点；观察者取消只停止等待，Close 取消并等待客户端活动退出。已配对节点使用本地保存的节点 token 与明确安装身份连接，不重新读取配对文件；兼容的不同 runner 发布和构建直接复用，不下载产物或重启服务。首次主机指纹确认必须精确匹配最近 45 秒内的探测，已确认密钥变化不能静默覆盖。

首次部署在远程写入前保存稳定安装请求，持久化进度、错误和临时目录清理责任。确认丢失时先清理准确目录并查询服务端操作日志；确认完成后才绑定授权。失败重试保留原 release、架构、操作 ID 与产物摘要，已有响应的 daemon 不能绕过本客户端未完成的安装确认。每节点的跨进程操作锁独立于短配置锁，覆盖安装、恢复、重试、编辑、移除与绑定；恢复取得锁后重新读取节点，避免旧快照覆盖另一 Manager 的新清理记录。不同节点可以继续检查。

Windows 与 Ubuntu 24.04 WSL 的 remotenodes 共 27 项普通测试通过，实机测试默认跳过；fileconfig、SSH 定向测试和相关 vet 通过。安装恢复、并发编辑、延迟重试与监测关闭测试重复 10 次通过。确定性双 Manager 测试验证恢复清理期间另一方 Retry 返回 Busy，释放锁后重试留下的新目录仍被保留，旧 Node 再恢复会读取新目录。测试使用独立 FileStore/Service/Manager 和真实文件锁；不是两个 Wails 进程或 race detector 验证。

显式 TestLiveNodeManagerPairsIndependentClientsAndReusesSavedBinding 在用户 amd64 节点通过，耗时 21.75 秒。两套独立临时配置与内存凭据分别完成共享配对、保存远程默认和使用保存授权重连；均回读 node_id `55e50d9b-b900-4b5e-9fb6-c4fee2bc17be`、installation_id `2ec0bac4-20bc-4765-b256-cad839f1c5a1`、版本 `remote-install-3fdc7b4-fixed` 与实际构建 `9f59901c370b279208269d3a4ec14e3efca528d1feddb992ba4f046aba44ce38`。该测试没有产物来源，未安装、升级或调用提供商，也未验证实际 OS Store 和桌面 UI。

nodes.json 的当前 Installation 增加必填 temporary_directory 与 cleanup_pending；所有当前生产者、校验器和测试同步更新，缺字段旧格式仍明确拒绝且不修改数据，不新增历史读取路径。此前未创建实际用户 nodes.json，本阶段只使用临时配置，没有历史数据转换，没有改变 SQLite 或报告格式。后续已补齐进程强杀前的暂存目录追踪，见下方独立记录；显式升级与最终远程 K3 e2e 仍需分别验证。

## 2026-10-16：桌面节点服务生产装配

生产 initializer 已装配 Manager、`remote-nodes/nodes.json`、按该根目录计算的独立 scoped Store，以及 `runner-releases` 校验缓存。发布默认为桌面 appVersion 对应的明确 GitHub release，测试和开发装配可显式注入来源与发布；创建服务不会下载产物。启动先验证当前节点配置，再清理孤立凭据和启动监测；未知格式在任何凭据清理前拒绝且保留原字节。退出关闭并等待所有检查与监测循环，Closer 可重复调用；关闭先于 Run goroutine 调度时返回正常取消，重复启动监测仍拒绝。

新增私有目录准备，避免在凭据 intent 写入前隐式建立未确认目录链。新目录先在父目录内暂存，再按平台发布；Unix 确认目标及全部祖先目录，即使前次失败留下可见目录，重试也不跳过祖先同步。Windows 使用现有 MoveFileEx WRITE_THROUGH 发布原语，不声称支持目录 fsync。已有祖先权限不修改，目标目录保持私有；文件与最终 symlink 拒绝，已有数据保留。

Windows desktop、remotenodes 与 fileconfig 测试及相关 vet 通过，生产重开/立即关闭相关用例重复 10 次通过。目录用例在 Ubuntu 24.04 WSL 通过，包含并发创建、保留已有内容、取消、symlink 拒绝，以及发布父目录后注入同步失败、重试重新确认祖先。Windows symlink 子项因系统权限不可用跳过。生产装配测试使用临时用户根、内存凭据与拒绝连接的 stub，验证保存的远程默认在离线重开后保留、凭据只能通过正确 scope 取得且不进入 nodes.json、没有产物下载。

这一提交没有公开 Remote binding/UI 或改变启动任务契约，也未验证原生 Wails、实际 OS Store 或真实 provider；节点删除使用检查继续缺省拒绝，待任务 Coordinator 装配。SQLite schema 和报告格式不变，实际用户配置与数据库未打开、未转换。当前 Wails 模块和用于版本核验的 CLI 均为 v2.15.0，没有执行绑定生成或更改依赖。

## 2026-10-16：K3 静态测试契约复核

再次读取 [Kimi 官方 OpenAPI](https://platform.kimi.com/docs/openapi.json)，确认 Responses 的 HTTP 400 使用 ErrorResponse，要求 error.message 字符串；type/code 可选且没有枚举。此前全仓失败来自测试仍要求固定 type，与既有 Case 审计后的当前断言矛盾。现仅同步测试：允许省略或其他字符串 type，仍拒绝 HTTP 200、缺失/非字符串 message 和 401/429/500/502；继续比较发往 /v1/responses 的 JSON 内容，未增加固定错误类型断言。没有改变 Case 请求、断言、Suite、稳定身份或历史结果，也不把错误 envelope 当成指定参数被拒绝的独立证明。

Windows `go test ./...` 已完整通过，包含 data/cases、data/suites、桌面、JobHost、管理 HTTP、SQLite 与 SSH。这是静态和 fixture 回归，解决前述已记录的测试基线失败；没有调用付费渠道，最终远程 K3 与最小性能验收仍未执行。

## 2026-10-16：任务管理客户端与报告续传

新增远程 typed client，通过受限 SSH Request 调用当前 runner 的 Probe、Submit、GetJob、ListJobs、ReadEvents 和 Command。提交响应核对任务、安装、实际构建及全部子对象身份；查询与分页核对安装归属，事件必须是请求游标之后的连续同任务序列。JSON 状态、回执、事件及命令确认只接受唯一当前字段，缺失、null、大小写变体、重复及未知字段拒绝；分页数组在 typed 解码前限制条数和字节，空页保留 keyset 游标。错误只保留固定公共码与重试提示，不把服务端 message、凭据或 response body 写入错误。

客户端不自动重新提交、不生成新任务身份，也不拥有原 Submission 的凭据或已接收任务。序列化提交副本的读取与清零通过互斥保护，HTTP 提前拒绝或仍在关闭请求体时不会并发改写正在读取的字节。调用方取消在建连及读取响应体阶段保留原语义，不被解释为远程任务取消。

报告下载只使用明确的不可变 ID、类型、大小和摘要引用。私有 partial file 从准确偏移续传，先核对单值 ETag、SHA、类型、Content-Length 与 Content-Range，再追加剩余字节；完成后重算整个文件摘要。损坏前缀、超长文件或异常响应明确拒绝，不重置用户数据。完整已校验文件可直接复用，不访问服务器；下载时间不改写报告指标。调用方仍须在成功后执行私有文件同步与原子发布，本客户端尚未装配桌面缓存或离线报告。

实际 JobHost、三类 Core、SQLite 和 runner HTTP 的集成测试通过 Run、Comparison、Performance 提交、同身份重传、观察者取消后继续、另一个 client 查询同任务、任务/事件分页、queued 停止、活动取消与命令确认重放；全部子 Run 报告及 Performance 完整结果均经 Range 续传并逐字节对比服务端原存储文档。上游仍是本地 Mock HTTP，没有调用用户渠道。客户端故障测试另覆盖错误身份、字段和契约、游标缺口、重复 Job、集合超限、错误摘要/头部/Range、断流后续传、损坏前缀、完整缓存与提交正文清零。

SSH Node.Do 的当前 Go API 替换为 Request 值，所有仓库调用方同步更新；Range 只允许无 body 的 GET 且偏移非负，不保留旧包装。底层 HTTP 总预算改为 2 分钟，普通 typed 请求为 30 秒，Node.Info 单独保持 30 秒。显式 31 秒慢响应测试经过真实 SSH testPeer/direct-tcpip 和 loopback HTTP，在首字节后继续读取完整报告，避免原 30 秒总预算提前截断；这是本地隧道验证，不能冒充物理远程服务器验收。

Windows `go test ./...` 全部通过，相关 vet 通过；客户端负向测试、三类型 HTTP 集成、当前状态解码及 SSH Range/31 秒慢响应在 Ubuntu 24.04 WSL 验证通过。没有新增格式版本、历史解析器或 schema 变化，没有接触实际用户数据库，也没有修改当前物理节点 runner。完整远程投影、assets/证据、Coordinator、桌面 Remote 面板和最终 K3 e2e 仍待接通。

## 2026-10-16：原子结果镜像与 Run 报告导入

新增独立 remoteresults 导入端口。镜像固定本地节点配置 ID、实际 node_id、installation_id、job_id 与规范摘要，按节点当前日志逐页保存完整 Run revision、Result、Evidence 索引、Comparison、任务状态和报告引用；领域事实、原日志与已应用游标在同一 SQLite 事务内提交。相同页或重叠页必须内容完全一致，来源变化、缺口、改变的不可变对象和本地 ID 碰撞明确拒绝；终态决定之后到达的已提交结果仍可同步。镜像不进入 execution_jobs，不成为本地执行或恢复对象。

本地 UpdateRun、输出写入、CreateReport、Comparison 更新及性能归档都检查来源。启动恢复改用明确的 ListRecoveryRuns，普通列表仍包含镜像。Comparison Refresher 只推进本地对象；远程子 Run 终态页与 Comparison 终态页之间保留节点已同步状态，避免桌面读列表时错误聚合或因写保护使列表不可读。Run、Comparison 的合法状态变化与报告写入复用唯一的事务内实现，没有第二套状态机或历史格式路径。

Run 报告先保留 pending 引用，再校验下载文档的当前格式、所属运行、原始大小和 SHA-256；报告、附件索引、运行封口和 imported 标记原子提交。重复导入核对相同文档和完整关系。该标记只代表报告 JSON 已校验导入，不声称证据及附件文件已经下载；私有文件同步、原子发布、同步循环和桌面消费仍待后续装配。Performance 统计归档与完整 PerformanceResult 使用独立引用类型，即使 report_id 相同也不会混淆原始字节；本阶段二者仍 pending，未实现性能文档导入或重算指标。

远程文档在 typed 解码前扫描项目自有集合，限制每事件 32768 个、每 Run 报告 131072 个集合成员及 128 层嵌套；超限明确拒绝，不前移游标。case_inputs 两层 map 计数，inputs 等数据键不当作 DTO 字段；definitions 内部当前 TestSpec 继续扫描，原生 request.body、response、断言值等真正 RawMessage 叶子保持 opaque。测试用超限元素后的损坏 JSON 尾部验证提前拒绝，另验证 provider 大数组仍按原语义保留。

Windows 全仓 Go 回归与相关 vet 通过。八项镜像集成测试和文档预算测试在 Windows、Ubuntu 24.04 WSL 通过，覆盖重新打开后重叠页续传、重复事实、终态后结果补齐、八个并发调用经两套独立仓库只写一份日志、日志写入故障的整页回滚、报告标记失败回滚封口与附件、来源/摘要/当前格式拒绝、恢复隔离和对比页面间隔。Performance 使用真实 Quick Host 的 Mock HTTP 测量数据，并单独验证预热、爬坡回调经节点日志进入镜像；没有使用真实渠道。审查发现并关闭了合法预热/爬坡 phase 遗漏及集合提前分配问题。

新增镜像表改变唯一当前 SQLite schema checksum；此前数据库由现有版本校验明确拒绝并保留，正常启动不转换。没有打开或转换实际用户数据，没有修改物理节点 runner；E 阶段独立转换工具及执行验证仍待完成。没有改变领域报告格式或添加版本协商。本阶段不是最终远程闭环验收，Remote UI、Coordinator、文件证据、性能文档和用户指定的 K3 e2e/最小性能测试仍待完成。

## 2026-10-16：性能完整结果与统计归档镜像

Performance 完整结果和统计归档使用独立下载路径与不可变引用。新增 archives 接口返回数据库保存的原始统计报告，验证任务归属、冻结配置、完整结果的唯一统计投影及报告日志；reports 对性能任务继续返回完整执行结果。两条路径共用 Range、ETag、SHA-256 和全文件校验，不互相回退。生产 runner 声明 performance_archives 能力；连接仍不比较客户端构建或自动覆盖 runner，后续 Coordinator 须在需要此能力的任务提交前核对。

镜像先验证原始摘要、大小和当前格式，再将完整结果关联到真实 starting 记录、结果事件的前序序号、封存时间和最后 checkpoint。归档必须等于完整结果的统计投影，复用现有列表和详情存储；文档、归档和 imported 标记在同一事务内提交。重复导入不改变原字节或测量时间，排队取消只导入完整结果，不制造空统计报告。性能扫描预算允许 10000 个样本及其稀疏时间片，仍分别限制字节、集合总数、数组长度和深度。

Windows 全仓 Go 测试及 vet 通过；新归档接口的跨两个实际 Job、未知身份、错误 UUID、额外查询和四类文档/引用损坏拒绝测试另通过 Windows 与 Ubuntu 24.04 WSL。真实 Core 的普通测量与预热/爬坡后测量经 typed client 续传两份原始文档，导入后关闭 HTTP 和数据库，再重新打开第二数据库离线读取，原指标和列表保持一致。注入归档标记失败验证整笔事务回滚；同步改写摘要和引用的启动/封存时间/checkpoint 伪造仍拒绝，原文档不变。大型 DTO 预算用例验证 10000 samples、20000 sparse time_slices 在 32 MiB 内可扫描，不把资源压力数据称为有效测量报告。

上述性能集成、预算测试与全部独立 runnerprocess 测试在 WSL 通过，后者包括实际构建摘要、loopback、强杀恢复无重发、维护暂停与 watchdog。测试上游均为本地 Mock，没有调用真实付费渠道或更新物理节点。当前 SQLite checksum 因新增完整结果镜像表改变；正常打开仍拒绝旧格式且不转换，实际用户数据库未打开或修改，E 阶段独立升级工具及执行仍待完成。文件缓存、自动同步、桌面任务入口与 UI 尚未接通；本阶段不能替代最终远程 K3 和最小性能 e2e。

## 2026-10-16：私有报告缓存与有界同步

新增按 SHA-256 命名和加锁的文档缓存。未完成内容只保留私有 partial，下载写入不能超出引用大小；完成后校验整份字节、同步文件并原子发布，再确认可读取的原字节。Unix 同步目录，Windows 使用 MoveFileEx WRITE_THROUGH，重读完整文件时再次 Flush；未声称 Windows 支持目录 fsync。发布前失败重试复用完整 partial，发布后确认错误重试复核完整文件，均不再下载。已损坏文档或前缀明确拒绝并保留，不自动重置；正常发布不保留重复 partial。

目录身份由打开的目录句柄 Stat 固定。测试发现 Windows 单独保存 Lstat 可能在同路径新目录出现后延迟解析身份，已改为句柄取身份并通过目录替换回归。缓存检查当前可观察到的链接、目录/文件身份和 Unix 权限；Windows 隐私依赖调用方创建的用户目录 ACL，不将 chmod 位当成 ACL 验证，也不声称绝对防御同用户恶意 TOCTOU。

新增应用级 Synchronizer.Pass：先核对本地固定来源，再通过 GET Probe 核对实际节点和安装，随后从数据库已提交游标补齐最多 20 页，每页 200 事件，总 context 为 5 分钟。Performance 完整结果先导入，统计归档后导入；两者均在私有缓存持久返回后才写数据库。它没有 Submit 或 Command 端口，网络错误和观察者取消只停止本轮同步。MoreEvents 只表示观察到的页面尾，不能据此认定报告收尾或最终 manifest 已完成。

Windows 全仓 Go 测试及 vet 通过；缓存、分页资源边界和三类型同步集成在 Ubuntu 24.04 WSL 通过。缓存测试使用两套独立对象与真实文件锁，验证排队等待取消、八个并发重读只下载一次、发布前后错误重试、字节超限、坏前缀和离线复用；Windows 三项 symlink 子用例因权限跳过，WSL 全部通过。集成测试使用实际 Run/Comparison/Performance Core、HTTP 与 SQLite；归档响应在 17 字节后截断时，完整结果已导入而归档仍 pending。关闭数据库后创建新的仓库、缓存和同步器，实际 GET Range 从 17 续传，已完整缓存的结果不再下载，关闭 HTTP 后仍可读取原报告，节点断开不会改写已知 completed。另有单次资源端口 fixture 验证每轮页数限制，不把该 fixture 当成领域日志验证。

本阶段没有改变 SQLite schema、领域报告或持久配置格式，没有打开实际用户数据库、更新物理 runner 或调用真实渠道。当前组件尚未装配桌面后台发现/调度和任务入口，媒体证据/附件与最终 manifest、release 仍待实现，不能将单次同步器视为完整桌面自动同步或最终远程验收。

## 2026-10-16：后台任务发现与桌面同步装配

新增独立节点观察循环并装配桌面启动与退出。来源仅包含已启用、主机密钥已固定、保存绑定有效且安装操作已解决的节点；从同一配置 revision 租用 SSH 和节点级 token，实时核对实际节点与安装身份后建立 typed client。观察路径不调用安装、升级、任务提交或控制。退出先取消并等待全部同步连接，再关闭节点管理和数据库。

观察者最多 64 个，配置变化取消旧连接，旧观察者退出前仍计入数量上限。每页发现最多 25 个任务，每次连接处理至多四项，完整同步全局并发最多四份；单次连接两分钟，默认五秒轮询、十秒刷新节点配置。保留未处理的页内任务，单个任务失败不会卡住同页后续项，尾部循环重扫以发现其他客户端的新任务、活动任务后续状态和迟到报告。相同事件序号的根快照必须一致，文档仍 pending 时继续同步；已导入 JSON 的跳过判断不表示另行管理的附件、证据或外部删除的文件可用。

配置读取超时和无效配置不再永久结束后台循环：同步公布固定错误，暂停旧配置下的观察，下一轮成功读到当前有效配置后恢复。节点健康监测同样保留循环；不支持格式仍经 Snapshot 明确拒绝，测试证明拒绝的文件原字节不变。没有历史格式回退、数据转换或隐式修复。

Windows 全仓 go test ./... 与 go vet ./... 通过。Ubuntu 24.04 WSL 的同步器全包及节点监测恢复测试各重复 20 次通过；桌面生产装配四项测试通过。两项实际 Core/HTTP/SQLite 后台集成分别验证三类共享任务在观察者和桌面数据库关闭后继续执行、重开导入、新增任务发现，以及同一观察者补齐既有任务的迟到性能文档；节点端 Mock 请求没有因重连重放。生产装配测试使用临时目录、MemoryStore 和连接端口，验证保存授权重开、禁用节点过滤、配置身份先拒绝及退出等待，未声称真实 SSH、OS Store 或原生桌面进程已验收。

本阶段没有改变 schema、持久配置或报告格式，没有打开实际用户数据库、替换物理节点 runner 或调用真实渠道。Coordinator/outbox、公开 bindings、全部启动入口和 Remote UI、证据附件与最终 manifest/release、显式升级、历史数据独立转换及最终真实 K3/最小性能验收仍待完成。

## 2026-10-16：提交 outbox 与固定确认记录

新增桌面提交记录，冻结完整 JobSpec、规范摘要、所选配置身份及实际 runner 元数据，不接收或持久化 Submission.Secrets。该记录与 execution_jobs、远端已接受镜像分开；写入 outbox 不创建执行事实。prepared 只表示尚未发送，pending 表示发送意图已持久化但接收仍待确认，accepted 保存与原任务、子 ID、实际构建和安装匹配的确认。接收后不能回退或更换原准备值。

pending 的 unreachable、not_found、identity_changed 和 rejected 只是有限观察，不改变权威执行状态，也不使记录重新可发送。特别是 GET 暂时 not_found 无法排除原请求仍在服务端完成接收；该观察不会生成新 ID 或把目标改为本地。相同准备的重复登记返回原记录；不同目标或 runner 信息冲突，两个仓库竞争相同 revision 的发送意图只有一个胜者。

待确认列表使用 job_id keyset，数量最多 25、记录文档合计最多 16 MiB，保留尚未返回的尾项。SQL CASE 在 Get/List 的 driver Scan 之前限制单个文档大小。解码精确检查必填外层及 Prepared 字段、重复、大小写与 null，先分别验证有界 Spec/Runner/Receipt，再由这些值构造记录，避免未检查的重复值提前分配 typed 集合；行中独立身份和摘要必须匹配文档。损坏数据明确拒绝，读取不修改原字节。

原结果集合扫描器提取为一个当前 owned JSON 扫描实现，继续保留事件、Run 报告与 Performance 的各自预算和错误分类。Spec、Submission 及 Freeze 在 typed 解码前也使用它，限制 32768 个 owned 集合成员和 128 层，原有 assets/secrets/comparison.runs 上限继续适用，native RawMessage body 保持 opaque。超限嵌套 entries、case_definitions、authored assertions 与 case_inputs 的损坏尾部在 typed 分配前拒绝，合法的原生大数组通过。

验证覆盖三类记录保存与数据库重开、跨两个仓库的幂等和 CAS、错误确认、固定准备值变化、UPDATE 故障不推进记录，以及六份含 3 MiB 原生 body 的有效记录按字节分页。另覆盖重复/别名/null、9 MiB 文档、独立 owner 列损坏及拒绝字节不变；这些是应用契约与真实 SQLite 验证，不代表 Backend 提交、SSH 或提供商执行。

Windows 全仓 Go 测试、针对性回归与 vet 通过。Ubuntu 24.04 WSL 的提交记录、字节分页、损坏解码和旧 schema 原字节保留测试通过，当前 jobcontract 全包与原结果资源预算全包也通过。审查发现的重复 envelope 绕过预算、SQL Scan 先分配大文档及嵌套领域集合无总预算三项均已修正并复核；原精确 envelope 集合限制的固定字段错误分类继续保留，所有当前检查均须成功才能 typed 解码。

本阶段新增 job_submissions 及其 pending 索引，替换唯一当前 SQLite schema checksum；旧数据库仍明确拒绝且原样保留，独立升级工具未创建或执行，未打开实际用户数据库、更新物理 runner 或调用真实渠道。Coordinator、发送前 gate 的实际使用、确认丢失重查与全部桌面入口仍待实施。

## 2026-10-16：任务提交与确认重查协调器

新增 Coordinator 的准备、首次提交与只读确认重查。Location 必须明确区分本地与保存的远程配置；没有默认到本地的缺字段行为。每次准备和新提交均 fresh probe 当前管理契约、真实节点/安装/构建、ready 和任务能力。HTTP 与 Coordinator 共用当前能力判定，删除 HTTP 中被替换的私有实现；Performance 的桌面闭环另要求 performance_archives。该判定没有客户端构建或发布号输入，兼容连接不会安装、覆盖或重启 runner。

新提交复制精确 secret slots 并验证传输字节后，只有持久 prepared → pending CAS 明确成功才发出 POST。写失败或数据库提交确认丢失均停止发送；仅凭回查到 pending 不能认为本进程取得发送权。所有传入 lease 和传输副本在成功、拒绝、取消与确认不明时清理。任务一旦接收，管理连接和桌面调用结束不控制执行宿主的生命周期。

Reconcile 仅 GET 原节点上的原 job_id，不使用临时渠道秘密。404、身份变化和不可达仍保留 pending；不支持当前管理契约时明确返回升级要求。Prepare/Submit 明确使用 SubmissionAccess，Reconcile 使用 ObservationAccess，连接端口可分别限制新任务和已有任务观察；关闭新任务入口的测试仍能找回原确认。只读查找允许当前兼容 runner 的构建与 ready 状态变化，但必须核对原节点/安装以及返回确认中的原构建、任务与子 ID。已通过另一路持久确认的记录不会被晚到提交错误改为待确认。

真实 Run/Comparison/Performance Core、管理 HTTP、typed client 与第二 SQLite 的集成验证：服务端已接收后截断响应，桌面保持 pending；关闭桌面数据库后节点继续完成，重开并重新创建 Coordinator，通过 GET 找回同一任务的确认，再由同步器导入全部预期报告文档。每个任务只有一次 POST，重查和同步不增加 Mock 渠道请求。这里关闭的是组件和数据库，不是实际 Wails 进程，HTTP 连接也不等于物理 SSH 验收。

两个协调器和两个仓库同时停在同一 prepared revision 后竞争，只有一个持久 CAS 胜者发出 HTTP 提交；原输入、接收者和原执行构建保持固定。另验证构建/节点/安装变化、归档能力缺失、错/多/已关闭凭据 slot、调用取消，以及发送意图写前失败和提交后确认丢失均不发送。构建变化和维护期间查找原确认的策略使用实际任务与 GET，并经 Probe 端口替换元数据验证，不声称已执行物理节点升级。

Windows 全仓 Go 测试与 vet 通过；最终端口调整后，runnerhttp、jobs、jobcontract 完整包回归和全仓 vet 再次通过。Ubuntu 24.04 WSL 中，最终源码的 Coordinator 与独立协调器竞争测试重复三轮通过。此前同组压力回归在 Windows 重复二十轮、WSL 重复十轮通过。

本阶段没有改变 schema、持久格式或领域报告，没有打开实际用户数据库、更新物理 runner 或调用真实渠道。生产 Backend 装配、启动后的自动待确认重查、本地共享 JobHost、显式同 ID 重交、停止/取消的待送达操作、所有公开入口与 Remote UI 仍待继续实施；最终 K3/最小性能及其余 R1–R16 验收未被这些组件测试替代。

## 2026-10-16：生产远程 Backend 与后台确认恢复

Coordinator 与 ConfirmationWatcher 已进入桌面生产装配和关闭顺序。提交和结果同步复用一个保存绑定连接器：在同一配置 revision 下取得 SSH lease 与节点级令牌，固定主机密钥，并通过 ConnectBoundNode 连接已保存的安装；不重配对、安装、升级或因连接失败调用本地执行器。当前 404 保留为任务未找到观察，当前契约/能力错误明确要求升级。Local JobBackend 尚待下一阶段，当前内部协调器收到显式 local 时明确不可用，既有公开运行入口尚未切换。

节点 Enabled 现只阻止新提交，已配对节点的结果观察和 pending 确认重查继续运行。生产同步装配测试改为同时观察 enabled/disabled 两个已配对节点，重开配置后仍可读取各自保存的授权，并在关闭返回前释放连接；未访问物理 SSH 或 OS Store。

确认观察器启动立即扫描未确认 outbox，之后每五秒推进一页、最多四项并行只读重查，数据库读取预算五秒、查询预算三十秒。失败项保留原目的地与 pending，游标仍到达后页；尾部回绕发现新项。现有分页同时包含 prepared 和 pending，prepared 只推进游标、不发 GET、不取得渠道秘密、不修改记录；不能因未发送的准备项阻塞已发送项。关闭取消并 join 全部查询，先于关闭共享数据库。

三类实际 Core/HTTP/第二 SQLite 的确认丢失重开测试已改由后台观察器自动找回原确认，再同步全部报告，仍只有一次 POST。另以首项有效 prepared、七项 pending、第一页三个不可达项验证后页实际 GET 404、每项只访问一次且准备项保持 revision 1；四项阻塞查询的关闭测试验证取消 join 和拒绝第二个 Run。没有渠道付费请求，也未把组件关闭当作实际桌面退出验收。

Windows 全仓 Go 测试和 vet 通过；修正混合状态分页后，jobs、完整 runnerhttp、desktop 测试及相关 vet 再次通过。最终源码的三类任务自动恢复、混合页公平性与有界关闭测试在 Ubuntu 24.04 WSL 重复三轮通过；只读复核无剩余 blocker。本阶段未改变 schema、持久文档或公开绑定，没有执行历史数据升级，没有修改物理 runner；公开运行入口、Remote UI、本地 JobHost、命令送达和最终真实 K3/最小性能验收仍待完成。

## 2026-10-16：真实 K3 验收前的完整输入预检

新增 TestRemoteJobEnvelopeFitsCompleteAuthoredK3Suites，通过生产目录、真实 Preparer 与当前 runner 边界做 T0 检查。Chat 官方基础 Suite 的 84 个和 Responses 官方基础 Suite 的 230 个 Case 均完整进入准备值，没有只取 default 或连通性子集；保留原有协议定义和断言，不改变案例或减弱结论。两份提交分别为 1,256,705 和 1,481,853 字节，Freeze、DecodeSubmission 及 AcceptedEvent 导入集合预算均通过；接收事件分别为 1,257,052 和 1,482,200 字节。

同时准备最小性能输入：openai-chat、kimi-k3、固定并发 1、3 个请求、输入目标 32、输出上限 128、单请求超时 120 秒，不加预热、爬坡或容量探索；895 字节的完整任务通过当前提交解码。真实执行时复用这些限制并替换为用户先前提供的连接，记录实际返回的 tokens、完成/失败数、耗时和可取得的流式指标；它只用于验证最小性能闭环，不能当作容量或稳定吞吐结论。

本预检使用临时目录、临时数据库、内存凭据和占位连接，未提交或执行任务，未访问用户服务器或真实渠道；零 Run 写入。Windows 针对性测试与 desktop vet 通过。它证明完整 authored 任务可以打包和解析，不代表官方参数覆盖完整、远程执行成功或报告已通过真实验收。

## 2026-10-16：本地 Backend 与共享 Core 能力来源

新增 LocalBackend 借用应用所有的 JobHost，经同一个 Coordinator 提交完整任务。每个 handle 的 Close 只使该 handle 不可再用，不关闭执行宿主；真正应用 owner 关闭仍由 Host 执行 local_shutdown 取消和收尾。Probe 动态读取宿主活动/排队/维护状态，平台来自当前 Go 进程，预算来自注入 Policy。查询同时核对固定 target、安装及完整原 spec/确认，可在 owner 构建改变后读取原执行构建；不能把远程 Host 或其他目标重标为本地。

三类实际 Core、SQLite 和 Mock 渠道通过 Coordinator 准备及接收，在提交调用取消和 handle 关闭后仍完成任务与报告；重复已确认查询不再连接或执行。另验证接收确认丢失、原 owner 关闭后用同一安装的新构建找回原确认，保留原构建且不增加执行次数；显式注入两个活动槽可同时执行两个本地 Job，不套用远程单任务策略。目标错误、未知当前格式和关闭 handle 拒绝时均清理传输秘密。

CoreCapabilities 现在提供本地和远程共同的执行协议与阶段能力，runner 只在此基础追加自身管理维护能力；删除被替换的重复协议与执行能力枚举，没有历史能力分支。Windows 本地边界回归十轮、jobs/jobhost/runnerhttp/desktop 完整包测试和相关 vet 通过；Ubuntu 24.04 WSL 本地边界及共享队列测试五轮通过，独立 runner 实际平台/构建元数据与维护跨重启测试通过。只读复核无新增 blocker。

本阶段没有变更 schema 或持久格式，没有接入桌面本地 Host 的长期身份与所有权装配，也未切换公开运行入口；现有生产 local factory 仍明确不可用。未访问物理测试节点或付费渠道，Remote UI、完整入口和最终 K3/最小性能验收仍待继续。

## 2026-10-16：生产本地 Host 的身份与所有权

桌面生产初始化现在先取得私有配置目录的生命周期锁，再迁移和打开数据库。锁由应用 owner 显式释放，调用 context 结束不会提前释放，sidecar 保留供下次复用。失败与正常关闭均等待本地 Host/Core 收尾后关闭数据库、释放锁，避免第二个桌面进程先恢复仍在运行的任务。LocalBackend 工厂已使用真实 Core；本地策略为 128 个活动 Job、1024 个排队 Job、总并发 65536、排队和执行各 24 小时，不套用远程默认串行策略。

本地节点、安装与 32 字节承诺密钥存入私有数据库，由 immediate 事务首次创建；重开不变。已有任务或任何提交记录旁身份丢失、无效类型、超限、错误 UUID 和全零密钥均拒绝，保留原数据，不推断历史格式或创建替代身份。实际构建摘要统一从当前进程读取，Linux 保留 /proc/self/exe 的运行 inode 语义，删除 runnerprocess 原 BuildID 路径。普通中断恢复通过关联 NOT EXISTS 排除节点镜像与 JobHost 拥有的 Run；损坏的 NULL owner 不会让其他任务被静默跳过。

Windows 全仓 Go 测试及 vet 通过；审查后修复 NULL owner 的最终 SQLite 定向回归重复三轮及 vet 通过。生产装配以真实 Core/SQLite/Mock 渠道验证重复 owner 在恢复前拒绝、关闭收尾、失败释放锁与重开稳定身份；Ubuntu 24.04 WSL 的生产测试、身份/恢复和文件锁测试重复三轮通过，独立 runnerprocess 与 CLI 测试通过。两项只读复核已关闭全部发现。所有请求均为 Mock，未访问真实渠道或更新物理节点。

新增唯一当前 local_executor_identity 表，SQLite checksum 改变；旧格式正常打开时明确拒绝且不转换，实际用户数据库未修改，E 阶段转换工具及执行仍未完成。此提交接通生产 local factory，但没有替换旧的桌面公开运行入口、生成绑定或实现 Remote UI；这些仍是下一阶段必要工作。真实 K3 和最小性能 e2e 尚未执行。

## 2026-10-16：四种准备的统一启动组合

新增 Launcher 将 Plan、QuickTask、Performance、Comparison 的现有只读准备端口组合到同一个 Coordinator。命令要求显式 Location、当前种类与唯一对应 payload；Comparison 在准备前限制渠道数量。整体一分钟 context 内先取得独立 JobID，准备完整冻结输入，再持久准备并提交；它不拥有执行 goroutine。所有出口清理准备 lease，性能准备只传递规范错误码，调用取消优先保留。接收确认丢失直接返回原 pending 记录与 ID，由既有只读确认器恢复，不在启动层另行发送。

生产初始化已组合真实 Run/Comparison/Performance Preparer 与本地或远程 Coordinator。四种路由测试使用准备端口 fixture 选择已冻结输入，实际 Core、SQLite 与 Mock 渠道验证全部原报告/结果；另覆盖非法选择、准备失败、部分组凭据、性能代码与取消、不可达远程零回退、确认丢失后一次提交。生产测试则使用真实目录和 Preparer，完整 84-case K3 Chat Suite 经 Mock 执行并封存原报告，最小性能保持并发一、三次测量请求与持久完整结果；用例判定没有弱化，Mock 响应不代表 K3 兼容性结论。

Windows Launcher 回归三轮、jobs/jobhost/desktop 完整包测试及相关 vet 通过；Ubuntu 24.04 WSL 的 Launcher 三轮与生产完整准备/执行两轮通过。源代码复核无阻塞问题。本阶段没有变更持久格式或 schema，没有执行历史转换、部署物理 runner 或调用真实付费渠道。公开启动绑定、执行位置选择、长任务展示与原公开路径删除仍待同一边界切换，不把此内部组合当作桌面远程功能已验收。

## 2026-10-16：Remote 的桌面展示与命令边界

新增七个 Wails 方法用于节点列表、保存、异步刷新、主机指纹确认、默认执行位置、安装重试及移除，全部受桌面启动/退出 lease 保护。保存只接收连接和输入凭据，从 Go-owned 状态保留原 pin，不能通过普通编辑接受新的主机密钥；密码、私钥和口令分别限制 8192、1 MiB、8192 字节，临时 lease 在所有出口清理。展示投影只含节点连接摘要、观察状态、实际 runner 身份/构建/版本与预算，不包含凭据引用、配对材料或安装路径；时间在 Go 边界格式化为 RFC3339Nano 字符串。

前端 typed client 与当前严格解析器一同更新；根、位置、节点、指纹和 runner 均拒绝未知字段，集合限制 64 个节点，默认远程必须引用配置节点 ID。离线或禁用默认节点继续保留该选择，不改为本地。Vite fixture 只提供空本地列表，远程变更明确 unavailable，不伪造配置、SSH、安装或运行成功。公开错误码及中英文映射和对应 fixture 已同步更新。

Windows desktop 完整测试、vet 和构建通过；前端全套 421 项测试及构建通过，补充 typed client 测试与 Unicode 控制字符检查后，最终相关 103 项和构建再次通过；lint 仅剩已有 App/report 的两项 set-state-in-effect 警告。CLI v2.15.0 生成并核对新增七个绑定，新 checked_at 字段为 string；模块版本及 go.mod/go.sum 未改变。Ubuntu 24.04 WSL 的绑定/当前错误码契约测试重复三轮通过。两项只读复核无阻塞发现。

上述 Go 测试使用真实 Manager、私有 FileStore 和内存凭据库，SSH 连接器为可控端口 fixture；没有连接实际服务器、调用付费渠道或证明 OS Store/原生 Wails/可见 UI。移除方法仍通过现有 fail-closed guard，生产使用情况 guard 尚未接入；Remote 面板与默认选择控件、全部启动入口和最终真实 K3/最小性能验收仍待继续。本阶段不改变持久格式/schema，没有执行历史转换。

## 待验收

完整管理接口、发布产物分发、全部桌面入口与默认远程节点、桌面退出后任务持续、结果补齐、显式升级与历史数据升级，以及第二台节点、arm64 和多客户端并发竞争的验证仍按实施方案 R1–R16 执行。单个 amd64 节点的 SSH 安装、共享复用与 systemd 自动恢复已按上节验证。用户追加要求：最终使用先前提供的真实服务器和渠道，执行 kimi-k3 兼容性 e2e 与最小性能测试；这项付费渠道验收单独记录实际用量，不复用前置 Python 冒烟作为完成证据。

## 2026-10-16：持久任务与完整性能结果的只读查询

新增按原 JobID 查询的展示投影，将发送状态、已确认回执、已同步执行快照、性能进度和结果下载分别保存其含义。查询只读取本地数据库：固定 outbox 的配置/node/install、实际 runner 版本/构建与 spec 摘要，不查询当前节点、不重交或控制执行。accepted 但尚无远程镜像时保留确认且执行投影为空；本地已接收任务缺失 owner 则拒绝为损坏。GET 确认领先日志同步时保留真实回执并暂不发布旧执行投影，镜像追平后再显示，不把正常时序差当数据损坏。

SQLite 在同一个只读事务内使用私有 helper，避免单连接池嵌套查询。执行文档和 64 KiB 进度在 typed 解码前由 SQL 限制字节；进度 envelope、owner、序号、规范时间、计数及阶段均验证。性能就绪标记检查封口结果或远程导入的存在性，完整 getter 仍验证原始字节、SHA、来源和日志；引用到达但未下载返回未就绪。未开始、阶段中断和最终返回均保留完整原观察，不使用统计归档回退或重算指标。

Windows jobs/jobhost/runnerhttp/sqlite 完整测试及相关 vet 通过；最终新增查询回归通过。Ubuntu 24.04 WSL 对 SQLite 查询、本地四种启动与远程 HTTP 丢确认/重开/离线读取各重复三轮通过。两项只读审查发现并修复确认领先镜像的问题，最终复核无阻塞项；同长度、类型合法的结果篡改通过轻量存在性检查，但完整 getter 因摘要不符明确拒绝。全部请求均为 Mock，没有真实渠道流量。本阶段无持久格式或 schema 变更、未执行历史升级；公开启动/查询/控制绑定、默认位置及可见 Remote UI 仍待接入，真实 K3 与最小性能 e2e 尚未执行。

## 2026-10-16：持久停止/取消指令与后台送达

Controller 先将确切 command_id、kind、job_id 和 expected_revision 写入当前 job_control_outbox，再连接原不可变 Submission 的配置、node 与 installation。尚未发送的 prepared 任务不能创建控制；pending 提交可能已在节点执行，允许保存指令。离线、404、身份变化和不支持契约只保存有限观察，不能生成执行终态或修改原 revision。节点 Command 的明确冲突成为 rejected；连接前冲突不能代替节点拒绝。兼容构建变化与维护暂停不阻止控制已有任务，不比较客户端版本，也不改变原任务的执行构建。

控制没有临时渠道秘密，可以按节点安装范围内的原 command_id 安全重传。HTTP 接收端在核对当前状态前返回已持久的原命令确认，因此完成后也能找回丢失的停止/取消回执。客户端确认采用 CAS；已存确认回包丢失先只读核对，人工与后台同时送达只保留一个原确认。数据库提交不明时本次调用不发送，已持久的 pending 仍可由后台发现。任务权威状态继续由执行日志提供，不以命令 pending、rejected 或网络错误改写。

后台每五秒最多处理四项，读取五秒、送达三十秒，坏节点不阻止游标走向后页，页尾才回绕；UUID 分页不承诺不同指令的操作顺序，各命令保留原 revision，冲突需重新读取权威状态。生产装配组合两类 worker；关闭先取消并等待控制/确认循环，再关闭同步、节点、Core 和数据库。当前桌面控制面先针对已持久准备的本桌面 Job，导入共享任务的控制展示、公开绑定和 UI 仍待完成。

SQLite Create 同 ID 同内容返回原 confirmed/rejected，改变命令或终态回退明确拒绝。Get 使用同一只读事务核对原 Submission，List 最多 25 项，每份约 70 KiB，在 SQL 中先限字节再解码；单页含前瞻项最多约 1.8 MiB。新测试覆盖两仓库 Create/CAS、重开、final 稳定性、超限/重复/未知/缺失/null/大小写/旧格式、错列与确认 owner，拒绝字节不变，取消和存储错误不变成缺失。

Windows 全仓 go test ./...、go vet ./... 通过，最终相关五包控制回归与 vet 通过。Ubuntu 24.04 WSL 对控制存储/旧格式保留、三种实际 Core 的 HTTP 控制、后台调度、本地 Backend 和生产关闭各重复三轮通过；新增两个 HTTP 客户端、两个 SQLite 连接同时重传的测试在 Windows/Linux 各三轮通过，只有一个节点决策。三种任务分别验证离线保存、两次重开、控制 ACK 截断、节点完成后同 ID 重传，不增加 Mock 渠道请求。维护为实际 Host 暂停，构建变化仍通过 Probe 端口模拟，不能当作物理升级证据。两项只读复核无阻塞项。

新增当前表与索引改变共享 SQLite schema checksum，桌面和 runner 的历史数据库都需显式转换；正常打开拒绝旧格式且不修改拒绝文件。实际用户数据库、物理节点未变更，历史转换工具未创建或执行；真实 K3 兼容性与最小性能 e2e 尚未执行。

## 2026-10-16：安装暂存目录在远端创建前持久化

安装暂存目录改为唯一当前的 `/var/tmp/llm-test-runner-bootstrap.<operation_id>`。Manager 在创建远端目录或上传前持久化原操作、确切路径及待清理责任；确认丢失和进程强杀后，重开配置先恢复同一路径，再查询原安装日志。成功清理保留路径事实并清除待清理标记。重试不能更换请求或路径，已有目录不能直接复用上传。

恢复仅清理已记录的确切私有命名空间，先校验 root 所有权、0700 目录、普通私有文件、文件数量及大小，未知内容、符号链接和特殊权限明确拒绝。旧十位临时目录、缺少路径及路径与操作不符的配置均拒绝且保留原字节；没有双格式读取或自动转换。

Windows SSH/remotenodes 两包完整测试通过；交叉编译后 Ubuntu 24.04 WSL root 两包完整测试通过。新增验证使用真实子进程 Process.Kill、重开 FileStore、丢失 mkdir 回执、实际 SFTP partial 写入后断开 SSH、重连清理和同操作重试，持久化失败时没有远端 Provision。测试 SFTP fixture 仅在中断场景广告 fsync 能力以进入写入阶段，没有执行或声称 fsync 成功。未使用物理服务器或用户凭据，未改变 SQLite schema，实际节点升级与最终渠道测试另行记录。

## 2026-10-16：桌面异步任务、Remote 面板与实际来源集成

当前七个 Job Wails 方法替换旧六个启动/控制方法和同步性能事件，CLI 固定 v2.15.0 生成绑定。Plan、Suite、Performance 和 Comparison 在每次启动前读取 Go 保存的最新默认位置；缺配置、远程不可用和能力不足明确阻断，不切回本地。任务面板从持久分页恢复原 JobID，投递确认、执行进度、固定控制指令和结果分别展示，关闭观察不取消任务。控制 pending 只能重传原 command_id；尚未导入的统计归档不会因为完整 outcome 已封口而提前开放。

来源由执行 Host 在接受时冻结，包含节点/安装、实际版本/构建及 OS/arch。Receipt、Accepted 日志、镜像、完整性能结果、统计归档和 Run 报告均核对同一来源，准备 Environment 保留原值。CurrentContract 仍为唯一的 1，要求 execution_origin 能力；不兼容节点明确升级，连接不替换程序。managed 报告不接受空来源；当前合法 unmanaged 报告明确序列化 null，缺字段旧文档拒绝且不修改。历史转换未由正常运行路径触发。

全仓首轮集成暴露一项实际性能故障：1,257,285 字节的 accepted 文档在 84 条结果写入中逐条重复解码，AppendResults 5.32s 超过既有 5s 存储期限，导致 Host 隔离。已改为全部文档原字节 SHA256 的有界纯校验缓存，只保存最多 128 份小回执及固定摘要；每次仍核对当前 owner/安装/构建/digest/时间，修改任何字节会重新严格验证。没有改变超时或 DDL。完整 84 Case 后接三请求性能任务两次独立回归通过，约 13s；桌面全包 37.462s、SQLite 全包 71.156s 和相关 vet 通过。另验证确认读/写失败及确认 ACK 丢失保留原 pending ID，不再次 POST；独立复核发现的 Accepted 内层 Spec 绕过预算问题已改为复用 DecodeSpec。

前端最新全量 46 文件、613 测试通过，生产构建与 lint 退出 0，仅两条既有 set-state-in-effect 提示。独立复核的三项 P2 已关闭：pending Comparison 持续观察、QuickTask 控制只走原持久指令、sealed outcome 不冒充统计归档。

独立 APPDATA 下启动真实 Wails dev 进程，核对正确 worktree EXE、LLM Test Studio 窗口标题、34115 端口归属、真实 Wails/Go runtime 和四组工作区读取；没有使用用户数据库。Windows 截图工具与 CUA runtime 均报 failed to write kernel assets，改用系统已安装 Chrome 和现有 Playwright 对真实 Wails 开发服务验证 1440×900 light、1280×800 dark、960×640 dark：Remote 按钮、真实空节点列表/本地默认、添加表单、密码遮蔽、Escape 关闭和任务面板可用，表单无横向溢出。应用代码没有页面运行错误；每次加载会遇到当前 Wails v2.15.0 开发覆盖层 /wails/ipc.js 的 Svelte h.nodes 异常，已核对其库源码，未修改 Wails 或放宽应用解析。原生窗口截图、非空节点交互及真实模型结果另待实机验收。

## 2026-10-16 当前桌面闭环与节点升级补充

当前阶段的 `go test ./...` 和 `go vet ./...` 均通过；桌面包 30.086 秒、SQLite 包 66.985 秒。前端完整 46 文件、613 测试、生产构建与 lint 通过，lint 保留两项既有警告。节点移除 guard 已装配到生产，提交和新控制意图与移除串行，持久控制的读取及原 ID 重传沿原路径执行。相关并发及持久化回归和独立代码复核通过。

测试服务器 amd64 上已显式执行 operation `620443b3-c7a4-4723-bd6b-31d37237d976`：暂停接收、等待 Pending=0、停止服务并取得运行锁、一致性备份、独立数据转换、原子选择新程序、暂停状态验证、恢复同一 operation。备份和操作日志保留在服务器私有目录 `/var/backups/llm-test-runner/620443b3-c7a4-4723-bd6b-31d37237d976/`。转换仅接受此次验证的空业务表源结构；没有转换用户本地数据库，也没有向运行时增加旧格式读取。

源 schema checksum 为 `59469710be911b477c828d6947e625631a3de9feeb755609efeebd66aa1c7c69`，当前为 `7afb439aaccf28d19e7e7381e2ef5611e013f062676e3706d44b3313d52c0708`。转换工具的六个本地验证场景包括旧 WAL 未清理的源库、一致性源备份，以及错误 marker、未知对象、非空业务数据和 SQL 字面量篡改的拒绝且不修改。

节点实际 `/proc/<MainPID>/exe` SHA-256 为 `89a9db5fb6319c74110c8620a13636193743464e9994eb5f1cf630448dd48eae`，版本 `remote-execution-20261016`，平台 linux/amd64。升级后 node/install 身份保持，systemd active/running，Restart=on-failure、Watchdog=30s，runner healthy/ready，维护 operation 已清空，策略仍为 active=1、queue=16、total concurrency=2000。该产物来自 `cef4288` 基础上的 dirty 工作区，不能称为已发布的干净构建。

首次 operator 尝试因维护确认 envelope 读取错误在转换和切换前停止；同一 operation 恢复接收，原程序与数据库保持。修正为当前 `{record,status}` 契约后执行上面的成功操作。回滚边界经独立审查：恢复接收可能已发生后禁止回滚旧数据库；此前仅恢复已经校验的一致性 SQLite 备份。此次实机没有在转换后注入失败来验证恢复，不把本地恢复验证描述为实机故障注入。

实际 K3 测试已由独立生产装配工作区开始，第一个远程任务为 `07428b56-66a3-4eae-9b16-26abe5c7333a`。计划执行现有 authored Chat 84、Responses 230 及三请求最小性能，另验证观察端关闭后的继续运行与同 ID 重开同步；结果尚未完成，不能据此声称提供商兼容性通过。凭据只经隐藏输入和匿名管道传递，不写入本文、仓库或汇总。
