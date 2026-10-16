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

全部交付仍按实施方案 R1–R16 核验，后续各节分别追加实现和实测证据。当前 K3 完整 Chat、Responses 与最小性能已执行，真实断言失败和原 summary 保留；离线读取复核通过，不补认原关闭观察窗口。产品内完整升级编排、foreign Job 全视图与控制、媒体与外置附件、release/回收、第二台节点、arm64 实机、宿主重启及实机故障回滚仍未完成；R15 已证明双独立客户端共用队列，初次 attempt 因桌面镜像导入超时失败，后续 query-only 原 ID 补同步通过。R2 的完整 230 Case 节点 Mock settlement 因 runner exit 1 未通过；关闭观察端的原尝试因 helper 等待本地 mirror 超时未通过，修正 helper 后尚未重验。单个 amd64 节点的安装、共享复用、systemd 强杀恢复与两次显式操作不代替其余边界，付费渠道结果也不由前置 Python 冒烟替代。

## 2026-10-16：持久任务与完整性能结果的只读查询

新增按原 JobID 查询的展示投影，将发送状态、已确认回执、已同步执行快照、性能进度和结果下载分别保存其含义。查询只读取本地数据库：固定 outbox 的配置/node/install、实际 runner 版本/构建与 spec 摘要，不查询当前节点、不重交或控制执行。accepted 但尚无远程镜像时保留确认且执行投影为空；本地已接收任务缺失 owner 则拒绝为损坏。GET 确认领先日志同步时保留真实回执并暂不发布旧执行投影，镜像追平后再显示，不把正常时序差当数据损坏。

SQLite 在同一个只读事务内使用私有 helper，避免单连接池嵌套查询。执行文档和 64 KiB 进度在 typed 解码前由 SQL 限制字节；进度 envelope、owner、序号、规范时间、计数及阶段均验证。性能就绪标记检查封口结果或远程导入的存在性，完整 getter 仍验证原始字节、SHA、来源和日志；引用到达但未下载返回未就绪。未开始、阶段中断和最终返回均保留完整原观察，不使用统计归档回退或重算指标。

Windows jobs/jobhost/runnerhttp/sqlite 完整测试及相关 vet 通过；最终新增查询回归通过。Ubuntu 24.04 WSL 对 SQLite 查询、本地四种启动与远程 HTTP 丢确认/重开/离线读取各重复三轮通过。两项只读审查发现并修复确认领先镜像的问题，最终复核无阻塞项；同长度、类型合法的结果篡改通过轻量存在性检查，但完整 getter 因摘要不符明确拒绝。全部请求均为 Mock，没有真实渠道流量。本阶段无持久格式或 schema 变更、未执行历史升级；公开启动/查询/控制绑定、默认位置及可见 Remote UI 仍待接入，真实 K3 与最小性能 e2e 尚未执行。

## 2026-10-16：持久停止/取消指令与后台送达

本阶段实现时，Controller 先将确切 command_id、kind、job_id 和 expected_revision 写入当时的 job_control_outbox，再连接原不可变 Submission 的配置、node 与 installation；这是该阶段事实，后续共享控制目标契约见文末。尚未发送的 prepared 任务不能创建控制；pending 提交可能已在节点执行，允许保存指令。离线、404、身份变化和不支持契约只保存有限观察，不能生成执行终态或修改原 revision。节点 Command 的明确冲突成为 rejected；连接前冲突不能代替节点拒绝。兼容构建变化与维护暂停不阻止控制已有任务，不比较客户端版本，也不改变原任务的执行构建。

控制没有临时渠道秘密，可以按节点安装范围内的原 command_id 安全重传。HTTP 接收端在核对当前状态前返回已持久的原命令确认，因此完成后也能找回丢失的停止/取消回执。客户端确认采用 CAS；已存确认回包丢失先只读核对，人工与后台同时送达只保留一个原确认。数据库提交不明时本次调用不发送，已持久的 pending 仍可由后台发现。任务权威状态继续由执行日志提供，不以命令 pending、rejected 或网络错误改写。

后台每五秒最多处理四项，读取五秒、送达三十秒，坏节点不阻止游标走向后页，页尾才回绕；UUID 分页不承诺不同指令的操作顺序，各命令保留原 revision，冲突需重新读取权威状态。生产装配组合两类 worker；关闭先取消并等待控制/确认循环，再关闭同步、节点、Core 和数据库。该阶段桌面控制面先针对已持久准备的本桌面 Job，导入共享任务的控制展示、公开绑定和 UI 当时仍待完成。

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

实际 K3 测试由独立生产装配工作区提交，第一个远程任务为 `07428b56-66a3-4eae-9b16-26abe5c7333a`。现有 authored Chat 84、Responses 230 及三请求最小性能均已完成，具体判定与观察窗口的证据边界见下一节；本轮没有通过完整提供商兼容性验收。凭据只经隐藏输入和匿名管道传递，不写入本文、仓库或汇总。

## 2026-10-16：K3 实测结果与收尾成本修复

上述三项真实任务均已完成，没有重新提交或更换 JobID。现有 authored Chat 套件完成 84/84，67 通过、17 失败；Responses 套件 Job `b2856f8f-fb78-43f7-9109-ee56fde233b0` 完成 230/230，104 通过、126 失败。两份报告均记录同一实际 linux/amd64 runner 来源。失败与任务执行终态分开：断言失败不能解释为远程任务没有完成，也不能改变断言使其通过。

安全诊断显示 Chat 的 13 项失败为预期 HTTP 400、实得 200，3 项为预期 200、实得 400，1 项为工具调用字段缺失。其中四项名称包含 thinking-off 的冻结定义实际只验证应拒绝请求，没有验证有效关闭，不据名称推断关闭能力。Responses 的失败包括 111 项预期 400、实得 200，3 项预期 200、实得 400，以及 12 项工具、历史或流式输出断言。三项流式失败收到非空文本 delta，但完成事件 output 和最终输出为空，不能把它描述为没有收到文本；提供商事件形状与客户端组装责任仍需另外分析。

最小性能 Job `500a3a97-d41a-4272-bf9d-34222869fb37` 实际发送 3 次请求、并发 1、输入目标 32 token、输出上限 128、单请求时限 120s，未启用额外负载阶段。3/3 成功、0 超时，总时间 10.789s，TTFT P50 2994.382ms、E2E P50 3679.773ms；提供商报告输入 402、输出 321 token。仅据此验证最小性能链路，不推断节点最大吞吐。

原验收脚本有两项观测错误，原汇总的 `passed=false` 保持不改：checkpoint 的 execution_origin 可以为当前合法 null，脚本误把它当成来源丢失；权威 terminal、镜像和独立统计归档的来源实际七字段一致，文档引用已导入且缓存 hash 正确。另将约 38s 的跨机时钟估计区间用于 15s 关闭窗口，无法证明窗口内推进。后续脚本改用桌面已关闭后两个独立只读 GET 的序号增长，不追认本次旧窗口。第一次 SQLite 一致性私有克隆在禁止管理连接的生产装配上复核三项原 Job、报告、完整性能与归档，8.81s 通过。

第二轮离线复核在私有克隆 `%TEMP%/llm-remote-k3-offline-review-166257786/` 实际通过，用时 17.04s；安全证据为该目录的 `acceptance-offline-review.safe.json`。`GetReports` 实际返回三个指定报告，调用用时 4367ms；三项原 Job、两份完整 Run 报告、完整性能结果与独立统计归档均可从禁止管理连接的生产装配读取。原数据库的任务/报告源表摘要与原 summary 均未修改，没有重新请求提供商。这是离线读取通过，不能据此改写原提供商断言或补认旧的关闭观察窗口。

实机在两份大 Run 进入终态后分别 exit 1，systemd 恢复后补齐原报告。退出位于终态和报告生成之间，不能把此自动恢复描述为无故障验收。当前 Settle 的完整工作流共享既有 5s 存储期限；本地完整 Responses 230 的终态至收尾为 3.694s。只读量测确认重复来源校验成本：RunOrigin 667ms、报告读取 1116ms、生成 dryrun 2118ms。

现已用小型不可变身份和快照摘要复用纯校验，当前 row 与原始字节仍逐次核对；报告写入和读回复用本事务已验证的 Run。相同量测降至约 247ms、489ms、1015ms，写报告来源部分从 677ms 降至 44ms。常驻真实 Core/SQLite/Mock 的完整 230 Case 后接三请求性能回归通过，收尾降至 2.116s，Host 仍健康并就绪；该次 SQLite 全包 62.035s、相关 vet 和独立审查通过。收尾优化提交 `dcfa4a3` 后，全仓 `go test ./...` 退出 0，其中 desktop 66.810s、SQLite 99.506s；`go vet ./...` 退出 0。没有修改 DDL、超时、提供商行为或统计口径。

## 2026-10-16：保留完整业务数据的 R2 程序升级

测试 amd64 节点的当前格式程序升级 operation `30656a4a-fc7f-4958-813b-a461762d3aa3` 已实际完成。R1 的三个完整 Job 及其业务数据共 1312 行先做一致性备份，fresh 只读备份核验 `quick_check=ok`、foreign key 错误 0；备份与操作记录保留于 `/var/backups/llm-test-runner/30656a4a-fc7f-4958-813b-a461762d3aa3/`。本次没有数据转换，SQLite checksum 仍为 `7afb439aaccf28d19e7e7381e2ef5611e013f062676e3706d44b3313d52c0708`，没有打开或修改本地用户数据库。

升级后实际 `/proc/200404/exe` SHA-256 为 `3d3435b5000e6c1dc1f0b5e281af0bd17d9fb9bd9995bfc2fcb19f21fed9890a`，版本 `remote-execution-20261016-r2`。node_id `55e50d9b-b900-4b5e-9fb6-c4fee2bc17be`、installation_id `2ec0bac4-20bc-4765-b256-cad839f1c5a1` 保持；systemd active/running，runner ready/healthy，NRestarts=0、Pending=0，共享策略仍为活动任务 1、队列 16、总并发 2000。manifest 的 source_commit 为 `e4786a359de43af96cda8fc517ca2b3cdf5e3786`、source_dirty=true；不能把该验证产物描述为 clean 发布构建。

R15 双独立客户端复验的初次 attempt 与后续原 ID 恢复分别记录于后续两节，不将初次 attempt 记为通过；升级后完整 230 Case 的真实节点 Mock 收尾角色在该次失败时未启动，后续进展独立记录。产品内完整升级编排、其他客户端发现任务的全视图和控制、本地媒体 asset 与外置证据附件、明确 release/回收、arm64 实机、第二台服务器、宿主重启后的自启动及实机升级故障回滚仍待对应实施或验收。既有方案的范围和验收条件保持，不以本次单节点程序升级代替全部交付。

## 2026-10-16：R15 共享队列通过，桌面镜像导入超时

两个 child 使用同一验收测试程序，在 production options 中分别设置 `r15-client-a`、`r15-client-b` 应用版本；两个独立进程和工作区共用同一节点授权及预算，证明不同客户端版本值不必匹配实际 R2 runner 版本。没有运行两个历史客户端代码构建，也没有遍历历史版本组合；项目仍只有单一当前管理契约，没有版本兼容层。

会话 `90867` 在 `role_stage_deadline` 自然失败，没有重新提交任务。`r15-client-a`、`r15-client-b` 是两个独立进程，均回读实际 R2 runner 来源；A 仅 Launch 一次 Job `38e7fbaf-2908-47ba-a655-7854c17398e1`，B 仅 Launch 一次 Job `72f9ea6b-e858-4638-8739-7874043c5c59`。独立只读 audit 观察 A running、B queued，全局 active=1、queued=1，Mock 请求数 1、在途 1；释放 Mock 后，同一 audit 观察两项原 Job 均为 completed。这证明两客户端共用远端队列和活动预算，不表示桌面同步已完成。

随后桌面 Mirror/报告导入超过阶段期限，未产生 `stage_completed`，因此 R15 整体未通过，后续 230 Case settlement 角色根本未启动。只读本地数据库核查：A 仅有旧 Chat cursor 177 及已导入报告、旧 Responses cursor 200 且镜像仍 running、旧性能 cursor 16 且完整结果/归档已导入；B 仅有旧 Chat cursor 177 和旧 Responses cursor 200。两份本地数据库均没有上述两个新 JobID 的 mirror，不能把本地导入滞后写成远端执行失败。两个 child 约 110 秒处于高 CPU，历史大任务导入成本正在诊断和修复，修复结果另行验证。

原工作区保留于 `%TEMP%/llm-remote-r15-a-enpo155j/` 与 `%TEMP%/llm-remote-r15-b-yxyozpq0/`。后续仅重开并查询这两个原 Job 补齐同步证据，禁止再次 Launch；阶段失败与原远端终态保持。

后续 fresh 只读节点检查仍为 PID 200404、NRestarts=0、原 R2 执行 SHA，ready/healthy=true、active/queued/pending 均为 0。节点 SQL 中原三个 Job 与新 A/B 共五个任务均 completed、revision=4、settled=1，证明 A/B 已在服务端完成收尾，本轮未触发 runner 重启。安全证据为 `%TEMP%/llm-remote-r15-shared-20261016.safe.json`，原样保留且不会覆盖成通过结果。R15 整体仍因 local mirror 超时为 false；该小任务收尾证据不能替代尚未启动的 230 Case settlement 角色，也不证明大任务优化已在实机验证。

预览构建使用 Wails CLI v2.15.0，退出码 0；`remote-acceptance` EXE 为 26532864 字节，SHA-256 `44866e9f6b9b8ca269252359d7f7ba985f31048a283300b996b55721e406ea9c`。该产物尚未启动，不能作为原生窗口或 UI 验收证据。

## 2026-10-16：导入成本修复与原 R15 任务只读恢复

提交 `b3355e7` 在 SQLite 导入事务的一页内复用已验证的 Run，逐事件继续核对 owner、position、revision、seal、reportCount 和 Receipt 静态身份；Run 事件清除该复用值，每页重新验证。没有改变 DDL、超时或当前 contract，也没有增加旧格式读取。全仓 `go test ./...` 通过，desktop 48.299s、SQLite 73.248s；全仓 vet 通过。独立复核 `TestRemoteBatch` 1.802s 通过，没有 blocking finding。

本地回放完整真实 Responses 的 469 个事件、461 条结果、原约 2 MB 报告，随后回放小性能任务的 16 个事件、完整结果与统计归档，9.46s 通过。24 事件量测从 6.592s 降至 2.626s，但两次资源条件不同，不能将全部差值归因于缓存。CPU profile 中 `ApplyRemoteBatch` 为 6.32s → 2.53s、`queryCurrentRun` 为 3.90s → 0.60s，支持重复解析是明确成本来源；本地回放不代替实际节点大任务收尾验收。

实机 query-only 恢复会话 `23834` 退出 0。A 原 Job `38e7fbaf-2908-47ba-a655-7854c17398e1` 的 mirror 在 24.400s 可读，完整性能结果、归档及来源在 26.406s 确认，ReportID 为 `012d66de-7069-4413-bd04-09b84572a713`；B 原 Job `72f9ea6b-e858-4638-8739-7874043c5c59` 分别为 31.709s、33.715s，ReportID 为 `86805873-4ad0-4803-80ce-6588b47cd243`。该阶段 Launch、Submit、Command 和提供商调用均为 0；固定 SSH 主机身份后恢复 MemoryStore 中原凭据引用，仅发起 GET，没有重提原任务。

恢复前后 runner 仍为 PID 200404、start tick 23033236、NRestarts=0，实际执行 SHA 不变。新安全证据为 `%TEMP%/llm-remote-r15-resume-20261016.safe.json`；原 shared 失败 JSON 与原 K3 `passed=false` 均原样保留。此结果证明新客户端代码可补齐原 ID 的镜像与报告，不追认会话 `90867` 为通过，也不将其描述为两个历史客户端代码构建的验证。

含本次补丁的新版预览构建由 Wails CLI v2.15.0 完成，退出 0；EXE 为 26547200 字节，SHA-256 `6a00f731ed6e583c51c1adaa3701bbbd2a5ad6bfe2f42ed4cb2ac4fd785672d8`，仍未启动。统一 live test EXE SHA-256 为 `2456560c913b1be27b7561e35ccbcc4d4438fd5b7dff46e70809a242fac03160`，来源工作区为 dirty，不称 clean 发布或原生窗口验收完成。

完整 230 Case 的实际节点免费 Mock settlement 角色会话 `84720` 及单免费 Job 的关闭观察验证均已得到失败结果，具体原因见下一节；不再记为 pending，也不以原 ID 补同步通过替代。产品内完整升级编排、foreign Job 全视图与控制、媒体与外置附件、release/回收、arm64 实机、第二服务器、宿主重启和升级故障回滚等既有剩余范围保持。

## 2026-10-16：R2 大任务收尾与关闭观察验证失败

完整 Responses 230 Case 的实际节点免费 Mock Job `e00123b8-ad42-4753-9a1f-ac5e50e442a2` 未通过无故障收尾验收。runner 从原 PID 200404 变为 209188，NRestarts 从 0 增至 1，journal 记录 `runner_start_failed` 和 exit 1；systemd 重启后补齐原报告。安全证据 `%TEMP%/llm-remote-r15-settlement-r2-20261016.safe.json` 保留 false，原 JobID 禁止重提。报告最终可读不能改写这次退出和重启，也不能把先前本地回放或小任务通过推广为大任务实机通过。

关闭观察端的原尝试也未通过：helper 错误地等待本地 mirror，在 70s 截止前没有进入预期观测流程。只读 outbox 已证明原 Job `06dfa643-3886-4c39-9f36-9177d513bab5` 为 accepted；原安全 JSON 的 `launch_calls=0` 是未及时更新的默认值，不能据此断言任务没有提交。原 JSON 保留 false，继续查询原 ID，不重新 Launch。helper 修正已提交为 `13392f6`，尚未重验，不能补认原关闭窗口。

该失败后的诊断使用隔离的服务器数据库 clone 确认超时步骤，修复方向是将每次单独存储 I/O 的既有 5s 限制与整个收尾 workflow 分开。以下分别记录基线诊断与 R3 升级事实；最终大任务和关闭观察实机验收仍未完成，不增加历史契约兼容路径。

## 2026-10-16：R2 原 Job 的隔离收尾基线诊断

会话 `72591` 退出 0，使用 `b3355e7` 的旧 R2 基线在实服务器私有数据库 clone 上执行 Settle-only，原 Job `e00123b8-ad42-4753-9a1f-ac5e50e442a2` 没有重提。源库包含 7 个 Job，目标任务为 461 条结果、469 个事件。共享 5s workflow 的实际总耗时为 5334ms；首个失败步骤为最后一次 `ListReportsForRuns`，开始于 4819ms、剩余期限 180ms，该调用实际耗时 515ms。此前两次 `GetRun` 为 666ms/642ms，`RunExecutionOrigin` 748ms、`ListResults` 686ms、`CreateReport` 1560ms，各次均小于 5s，证据指向多个合法存储步骤累计耗尽共享期限。

诊断进程另用 60s workflow 作对照，总耗时 7193ms，最后 Mark 157ms 并成功；这不是把产品 `storageTimeout` 调为 60s。两轮 Execute 和提供商请求均为 0。原业务 typed hash、PID 209188、start tick 23203834、NRestarts=1、实际 R2 构建 SHA `3d3435b5000e6c1dc1f0b5e281af0bd17d9fb9bd9995bfc2fcb19f21fed9890a` 全部保持。

本地安全证据为 `%TEMP%/llm-remote-settlement-baseline-20261016.safe.json`，SHA-256 `8699ca5edd3fe01540d1f8c7bb6a88bdbae192ac365aa63b2d2f617b7ed05a7c`；远端证据为 `/var/tmp/llm-test-settle-diagnostic-a7c21f619ab05a0308222501568c5d3b/diagnostic.safe.jsonl`。同目录的 source.db 与诊断 binary 保留，内部临时 work clone 已正常删除。本节仅证明旧基线的根因；诊断当时 R3 尚未升级，后续升级证据独立记录于下一节，不补认大任务复验。

## 2026-10-16：R3 收尾修复发布到测试节点

修复提交 `2f52b3284408d8d4dbbae4ca0b9f93c3c218ca41` 后，全仓 Go 测试与 vet 通过，desktop 48.845s、jobhost 24.206s、SQLite 71.387s。该提交修改 10 个文件，859 行增加、66 行删除。它将单次存储 I/O 的既有 5s 预算与整体收尾 workflow 分开；本地验证与隔离基线的诊断证据不能代替后续大任务实测，其失败事实另记于下一节。

R3 升级 operation `650cdb6c-7312-4846-bc83-8d62d4a8a52d` 的会话 `80581` 退出 0，实际执行 pause、等待 Pending=0、停止服务、一致性备份、切换 current、暂停状态核验、同 operation 恢复接收，最终 completed。备份保留于 `/var/backups/llm-test-runner/650cdb6c-7312-4846-bc83-8d62d4a8a52d/`，当前 schema checksum 仍为 `7afb439aaccf28d19e7e7381e2ef5611e013f062676e3706d44b3313d52c0708`；备份 `quick_check=ok`、foreign key 错误 0、业务 2300 行。没有执行数据格式转换或触碰本地用户数据库。

fresh 独立 probe 会话 `65509` 退出 0：实际 PID 211250、NRestarts=0，执行 inode SHA-256 `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a`，版本 `remote-execution-20261016-r3`；原 node/install 身份保持，ready/healthy=true、active/queued/pending=0、maintenance 为空，共享预算仍为 1/16/2000。原七个 Job 均 completed、revision=4、settled=1；其中包括旧关闭观察 Job `06dfa643-3886-4c39-9f36-9177d513bab5`，其终态不能证明旧性能结果或旧关闭窗口验收成功。

R3 产物保留于 `%TEMP%/llm-runner-remote-e2e-20261016-r3/`：linux/amd64 为 13639842 字节、SHA-256 `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a`；linux/arm64 为 12714146 字节、SHA-256 `efc509ceb44165f8f7f51fd535470c9096ce0cb11f1bc061e9d851ffb7944535`。manifest source_commit 为 `2f52b3284408d8d4dbbae4ca0b9f93c3c218ca41`、source_dirty=true，仍是准确记录来源的验证产物，不是 clean 发布；arm64 仍只有构建证据。

原生预览 `apps/desktop/build/bin/llm-test-studio-remote-acceptance-r3.exe` 使用 Wails CLI v2.15.0 构建通过，26608640 字节、SHA-256 `295b8b98d8a2a3f379bccde8ff651be9cb31d17ed9130a84adf0b9c8ae814cae`，尚未启动。升级和独立健康检查通过不代表大任务验收通过；R3 免费 230 Case 的后续失败事实见下一节，关闭观察仍未运行。既有失败证据与其余未完成范围均保持。

## 2026-10-16：R3 大任务实测失败，执行阶段故障待定位

单独 settlement 会话 `89151` 仅接收一次 Job `f24d35f8-2370-4578-ac45-0fc288161b61`，工作区保留于 `%TEMP%/llm-remote-r15-settlement-dqea80zt/`。原任务在 30635ms 确认 accepted，35244ms 观察到 running、healthy=true，随后验收以 `role_incomplete` 失败；该版 harness 未保存首个错误的阶段证据，不能据角色结果推断具体失败调用。

fresh 只读检查会话 `73567` 确认 runner 从 PID 211250 变为 212013、NRestarts 从 0 增至 1，实际执行 SHA 仍为 `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a`。journal 记录 `runner_start_failed`、exit 1，时间戳为 `1792119868596750us`，CPU 为 36.693s。恢复后原 `f24d35f8-2370-4578-ac45-0fc288161b61` 为 failed、revision=4、settled=1，其余七个任务仍为 completed；当前 runner ready/healthy=true、Pending=0。恢复后的健康状态不能改写这次任务失败和进程退出。

原失败安全证据以 O_EXCL 创建并保留于 `%TEMP%/llm-remote-r15-settlement-r3-20261016.safe.json`，SHA-256 `a7216a514b2f31bc13ea58e06486eead273654ae6d98b25a183ecea4bb1c1b13`，不覆盖为通过结果。原 f24 Job 和此前付费 JobID 均禁止重提；下一步只读取得原失败事实、核对执行路径，并在私有 clone 端口做成本 profile，由测试侧补齐安全阶段证据及 query-only 原 Run 恢复。

R3 的收尾期限源码修复与隔离 clone 根因验证仍成立，但执行阶段另一项存储故障尚未解决，真实大任务验收明确失败。关闭观察验证尚未运行，不记为通过；不得以本地测试、升级成功、健康重启或报告恢复替代这两项实际边界。

## 2026-10-16：原 f24 失败事实与只读恢复未通过

源节点只读 probe 会话 `62506` 已读得业务事实，但最后可选文件的 key allow-list 检查报错，进程退出 1；不能称整次 probe 成功。原 f24 的持久时间顺序为 root accepted `03:04:08.75972606`、root starting `03:04:12.625073801`、root running `03:04:12.812903159`、child failed `03:04:19.473155006`、root failed `03:04:22.01015323`、report `03:04:25.213264543`。child 失败为 revision=2、phase=`runner`、code=`job_storage_unavailable`；事件总数仅 6，请求结果、Case summary 和 entry marker 均为 0。

本地只读核查同样证明 child 从 revision=1 queued 直接到 revision=2 failed。ReportID `61323546-5bb1-4b2e-9252-20b508cce96f`（RunID 前缀 `6ceb6140`）的引用已 imported=1，本地文档为 1483134 字节；这些行存在不等于公开报告 API 已通过验证。节点 available bytes 为 191221383168，配置仍为 MaxStorage 20 GiB、MinFree 512 MiB、Reservation 128 MiB；本次未量测 UsedBytes，不能用宿主空闲空间排除状态目录配额因素。

原失败事实的安全证据为 `%TEMP%/llm-remote-f24-failure-facts-20261016.safe.json`，SHA-256 `6e712a833e12d8e002d3238d5a00aa9156584ad116522ff3477ca9337b4bbb2b`。原始失败结论保留，不重提任务、不覆盖既有证据。

query-only 恢复会话 `68469` 自然退出 1。原 receipt 在 2586ms 可读，服务器仍为 failed、sequence=6、revision=4、healthy=true、Pending=0；本地 mirror 在 118566ms 到达 terminal failed。报告阶段到 600666ms 仍未通过 `GetReports`/`GetReportDetail`，尽管引用和文档已存在。该阶段 Launch、blocked writes 和提供商调用计数均为 0，original outcome=failed，不把这次恢复转为通过。安全证据 `%TEMP%/llm-remote-f24-query-recovery-20261016.safe.json` 的 SHA-256 为 `5f787836313b11eb0c0a27fc9591771cef45d77c128d0038cd63da6253ff05bd`。

具体失败存储端口当时仍未证明。现有顺序最符合 Adopt 之后 starting 的 `UpdateRun` 未提交，或监控取消这两条候选路径，不能据此选定根因。该阶段制定了服务器私有 clone 的三个端口并发读取 profile 和本地 clone 单次公开报告 API 诊断计划，当时尚无节点 profile 执行证据；后续 16 端口的实际结果单独记录如下，没有新建真实 Job。

## 2026-10-16：报告查询修复与写探针取消竞态的独立证据

本地提交 `76258fb` 修正 `CheckWritable` 的取消竞态：只有 Commit 返回精确的 `sql.ErrTxDone`，且同一次调用的 `ctx.Err()!=nil` 时，保留 context 取消分类。这是已经证明的独立竞态，尚未证明它是原 f24 失败的唯一根因；该提交尚未部署到节点，不能把本地修复写成实机问题已解决。

报告列表投影发现另一项独立错误：合法、带 Failure 的 `run.Fail` 历史被用 `Transition` 重放，导致错误返回 Corrupt。`report_projection.go` 与对应测试已按唯一当前契约修正，未改变 DDL 或持久格式；独立 review、两个相关 Go 包完整测试和 vet 均通过。本次投影修复已本地提交为 `f0f43c5`（`fix(sqlite): validate failed run histories in report projections`），仅包含这两个 Go 文件，仍未部署。随后全仓 `go test ./...` 通过，desktop 59.848s、jobhost 24.606s、SQLite 96.028s；全仓 `go vet ./...` 通过。这些本地检查不补认原实机失败验收。

在原数据库的一致性 clone 上，单次 `GetReports` 用时 6962ms，返回 8 份报告，目标失败报告存在且保留 failed；单次 `GetReportDetail` 用时 509ms，仍为 failed、0 results。源库 SHA-256 `7c5be8f7a5d7c656c537e4471f91eadcc96dce8d393c60ba25cb3f593aacc8c9` 前后保持。安全证据为 `%TEMP%/llm-report-query-diagnostic-_9c1flu7/projection-fix-api-proof.safe.json`，SHA-256 `f96dbfb3a0b71d2df08de34b51e415a62bc0609eb598ef9e24b3a3b9ba416f8f`。这证明当前修复后的公开查询能读取 clone 中原失败报告，不改变该任务的失败事实。

截至该次投影修复记录时，弱节点 16 端口 profiler 尚在 reader barrier 校准和重建 pin 的准备阶段，没有建立 SSH；这一历史准备状态不构成已执行的 profile。后续真实执行结果见下一节，仍不将诊断机制写成原 f24 启动失败的唯一根因。

原 K3 付费判定、R3 原 failed 以及 600s query-only 失败结果和证据全部保留，不覆盖或改成通过。整体仍有执行稳定性、关闭观察与恢复、产品内正式升级编排和干净产物交付、共享 foreign Job 全视图与控制、文件资源及附件/回收、最终原生窗口验收、第二节点与 arm64 实机等工作；已有单节点升级和 clone 查询证据不缩减这些范围。

## 2026-10-16：弱节点隔离诊断完成，单连接竞争机制成立

会话 `83277` 在真实弱节点的私有 clone 完成 16 个端口场景，223945ms、返回 0，生成 18 条记录。原库 typed business SHA `4844878864421964db5126084b11aa83cd6286348b9c52d2d6368660206d0632` 前后相同；PID 212013、start tick 23384984、NRestarts=1、原 R3 可执行文件 SHA `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a` 保持。Activate、Execute 与提供商调用均为 0，诊断没有创建或重提真实任务。

全部 8 条四 reader 样本实际记录 queued=3、in-use=1、reader_errors=0，证明 reader 并发已建立且共享全局 max-connection=1 的连接池；诊断程序返回 0 表示场景执行完整，不表示所有端口操作都在其期限内成功。

| 端口 | 无并发 reader | 四 reader、常规预算 | 四 reader、60s 外层诊断对照 |
| --- | --- | --- | --- |
| Starting Update | 1817ms | 5s 预算下共 5021ms，BeginTx 4424ms，deadline | 8478ms，BeginTx 6532ms，成功 |
| GetRun | 620ms | 5s 预算下 5000ms timeout | 7450ms，成功 |
| AdoptQueued | 798ms | 5s 预算下 4428ms 成功，接近预算 | 内部 GetRun 仍因其独立 5s deadline 失败，外层延长不能解决 |
| CheckWritable | 0ms | 4s 预算下 3013ms 成功 | 4526ms 成功，未复现 TxDone |

该 profile 证明大历史读取持有单连接，会让执行关键读写在等待连接或执行期间超时；尚未唯一证明它就是原 f24 的失败根因。60s 只是诊断 control，没有更改生产预算。写探针未复现 TxDone 不否定先前独立竞态测试，也不能把两项证据合成唯一因果链。

远端 stage 为 `/var/tmp/llm-test-settle-diagnostic-5ff093e197f9f0a5b4c0c58407e02ad5/`。原始内层日志经会话 `20577` 成功取回到 `%TEMP%/runports-4b21bce229d0febdb96974b4.safe.jsonl`，12107 字节、SHA-256 `cbce692a38e5fddb9af1f53b4be5853b7e7e47e8237c65c2f9b5903f3a22e5b4`。外层独立观察摘要为 `%TEMP%/llm-remote-run-ports-baseline-20261016.safe.json`，SHA-256 `4a67d25e155018a75fe5fbf7a56e0004042bb97709aa3fe04a46aa3153f382f5`。后者是 `parent_observed` 摘要，不是原始 raw JSON；raw JSONL 只包含 inner clone 和端口记录，不能混称原始日志已经含全部源库与进程观察。

已安装 modernc SQLite v1.35.0 的 ReadOnly transaction 使用 plain BEGIN/deferred；不能仅凭 DSN 的 immediate 选项把实际只读事务描述成 immediate。本轮明确瓶颈是全局 max-connection=1。HTTP 独立只读 view 与 WAL 下的读写连接隔离已提交为 `81d7b93bcddb946e12d8ab42a8da6b76652643fa`，提交时尚未部署或执行同源实机对照；确定性测试、构建和后来完成的隔离对照分别见后续记录。执行稳定性和最终实机验收仍未完成，原 K3/R3/query 失败历史及整体剩余范围保持。

## 2026-10-16：HTTP 只读隔离已提交，R4 构建完成但未部署

提交 `81d7b93bcddb946e12d8ab42a8da6b76652643fa`（`fix(runner): isolate history reads from execution storage`）包含 6 个文件、666 行增加/8 行删除。HTTP 全部 7 项 ReadRepository 端口，包括 `ownJob` 的 owner 核对，使用独立 mode=ro、query_only、deferred、单连接 WAL view；Core、Host 和 health 保留原 RW 单连接。两者共用 openRepository 的唯一当前 schema 和 integrity 校验，没有通过历史格式或降低校验换取并发。

HTTP 关闭准入与 handler 的 Add 登记使用同一锁，取消 API 后执行既有 10s Shutdown、Close，等待 Serve 与实际 handler 退出，再关闭 Host/Core/read view/RW。10s 是 Shutdown 阶段的既有期限，不是整个关闭 workflow 的硬上限。

确定性测试在持有只读 snapshot 的门闩期间验证 RW GetRun、Starting Update 和 health 成功；已持有的读 snapshot 保留原 revision，后续新读见到新 revision。另覆盖只读路径 replacement、FK、busy、WAL 拒绝时不改数据、未知 DDL 拒绝和取消关闭。review 发现的测试 fixture 路径问题已按平台纠正：Windows 使用合法的 `#`、`%` 字符，Linux 使用真实 `?` 路径验证 URI。最新 Windows focused 独立运行 1.551s、实现者运行 1.618s 通过，WSL focused 9 个测试和 runnerprocess 全部 8 个测试通过，相关 vet 通过。

最终全仓 `go test ./...` 通过，desktop 59.472s、jobhost 24.483s、SQLite 96.799s；全仓 vet 通过。独立 review 无 blocking，聚合结果摘要为 `f706ed09011c91c69662d0baa1750d05d6fe6e721f43fdd08d075c44a98ec777`。这些本地/WSL 证据证明隔离边界，不代替弱节点同矩阵实测。

R4 构建前后源码均为 clean `81d7b93bcddb946e12d8ab42a8da6b76652643fa`，产物目录 `%TEMP%/llm-runner-remote-e2e-20261016-r4/`。runner 使用真实入口 `cmd/llm-test-runner`、CGO=0、trimpath、`-s -w` 和 `main.version=remote-execution-20261016-r4`；manifest source_commit 为该提交、source_dirty=false，manifest SHA-256 `bcb766e51b25cb423a6d6ae16ea55c017e25b75c5182e88261a5b2ed9a08c345`。

| 产物 | 字节数 | SHA-256 | 已执行边界 |
| --- | ---: | --- | --- |
| linux/amd64 runner | 13656226 | `1c4c2bab329e2349dee6c0f87002662f9b20acfc1dcb64518eff546fcffc1d62` | 仅 WSL version 返回 r4 及相同摘要 |
| linux/arm64 runner | 12714146 | `bba03cecd185bb752a35d4763deccea53b346bec252dc79b800e4d29b2a83e10` | 仅 ELF/摘要核对，未实机运行 |
| 原生 R4 preview EXE | 26621440 | `0e20b12f51c21311bd15f113193143c67a9cb053b3ee08a9a66a0153396ccaf1` | Wails v2.15.0、pnpm build 和指定构建通过，未启动 |
| 统一 acceptance test EXE | 36766208 | `558f6bc6141669fbf2216de30e4ff84745fda21d27ffac870f5833c9d4940374` | 构建产物，不作为新实机对照证据 |

原生 EXE 位于上述产物目录的副本及 `apps/desktop/build/bin/llm-test-studio-remote-acceptance-r4.exe`；test EXE 为 `%TEMP%/llm-remote-81d7b93-acceptance.test.exe`。完整构建证据为产物目录内 `r4-build-proof.safe.json`，SHA-256 `441d025f444c4f37bc890bf6bbc23c8ba2cd7ace8313b75bd0fa0c27a8312c9d`。go.mod、go.sum、锁文件与 bindings 合计 9 个输入文件原字节未变。

截至 R4 构建记录时，真实服务器仍为 R3、PID 212013、原 `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a` 可执行文件；新增分池 readerStats 的同一 16 场景对照当时仅在 TEMP 准备、未 SSH，后续实际对照结果独立记录于下一节。正式升级功能当时在独立 managed worktree `C:/Users/894/.codex/worktrees/remote-upgrade-flow/llm-test`、分支 `codex/remote-upgrade-flow`（基线 `51a218d`）按 API、core、snapshot、SSH、UI 五路实现，尚未合入或验收；后续整合状态另记。旧失败证据不清理、不改判，整体剩余范围保持。

## 2026-10-16：同源 read view 实机隔离对照通过，R4 升级进行中

会话 `55904` 使用精确源码 `81d7b93bcddb946e12d8ab42a8da6b76652643fa` archive 在同一服务器完成 read view 对照，return=0、260639ms，18 条记录中的 16 个端口场景 error_class 全部为 ok，没有 deadline 或 TxDone。Activate、Execute 和提供商调用均为 0；这次运行是私有 clone 诊断工具，不是正式 runner 替换。

全部 8 条四 reader 场景均实际记录 readerQueued=3、readerInUse=1、reader_errors=0；执行关键 writer 的 queued、in-use、pool WaitCount 和 WaitTotal 全部为 0。父端程序逐项校验了 16 个端口场景及双池条件，确认读并发存在且执行连接没有被历史 reader 排队占用。

| 端口 | 无 reader、常规预算 | 四 reader、常规预算 | 四 reader、60s 外层诊断对照 |
| --- | --- | --- | --- |
| Starting Update | 2398ms，成功，BeginTx 0ms | 1654ms，成功，BeginTx 0ms | 1810ms，成功 |
| CheckWritable | 0ms，成功 | 0ms，成功 | 0ms，成功 |
| GetRun | 655ms，成功 | 700ms，成功 | 665ms，成功 |
| AdoptQueued | 756ms，成功 | 774ms，成功 | 784ms，成功，内部 GetRun 的 5s 预算也通过 |

原单池基线四 reader 下 Starting Update 5021ms/BeginTx 4424ms 失败、GetRun 5000ms 失败，AdoptQueued 即使外层 60s 仍遇到内部 GetRun 5s 失败。新对照证明该单池竞争机制已被隔离修复，不改写原 f24 失败，也不唯一归因原失败。60s 仍只是 control，生产单次 5s/health 4s 期限没有改变。

源库 typed business SHA `4844878864421964db5126084b11aa83cd6286348b9c52d2d6368660206d0632`、原 R3 PID 212013、start tick 23384984、NRestarts=1 和 exe SHA `7cc292b7f2c9ffb6728d0035d68deb8d72fc58243b3905566d558b6b06e9d25a` 前后均不变。远端 stage 为 `/var/tmp/llm-test-settle-diagnostic-664f5f00eddc3f11ba49884f665121ac/`，诊断 binary SHA-256 `26985a7fd430e0b667d3e798546e616955e9ea384220699bbcd3e14eb3ca8a6e`，dispatcher SHA-256 `dce6169118c8f43acaaaf79404c7e627cf4525a4b831eb3a321760825b955e8b`；不能将其摘要当成正在运行的 runner 摘要。

会话 `42559` 成功取回原字节日志 `%TEMP%/readview-runports-07df66aa528ef895f4646c47.safe.jsonl`，14039 字节、SHA-256 `591a7279c952fef0026d7a4614244f795738836425bc1e4294007bc0882f1f75`。父端外层 observed 摘要为 `%TEMP%/llm-remote-run-ports-readview-20261016.safe.json`，SHA-256 `559bb41ff7837508eceff08bbdc49c53088f9cfaa6cb84255a12cdb43bc15d3c`；原始 inner clone/ports 日志与外层源库/进程观察继续区分保存。

operator 会话 `79091` 在上述对照完成时已开始 R3 → R4，当时尚未取得完成确认、新 PID 或 operation ID；后续完成事实见下一节。实际升级后的免费 Mock 大任务与关闭观察仍须验收；不重复付费 K3，不覆盖原 paid/failed/query 证据，也不缩减正式升级功能、共享 foreign 管理、文件资源、最终 native、第二节点与 arm64 等未完成范围。

## 2026-10-16：R4 运维升级完成，正式产品升级流程已整合

运维会话 `79091` 退出 0，operation `0e6927a1-e600-4a1c-903b-a91ff12d89a3` 为 completed，备份保留于 `/var/backups/llm-test-runner/0e6927a1-e600-4a1c-903b-a91ff12d89a3/`。独立只读 probe `31788` 退出 0：PID 220541、NRestarts=0、systemd active/running，实际 exe SHA `1c4c2bab329e2349dee6c0f87002662f9b20acfc1dcb64518eff546fcffc1d62`，ready/healthy=true、active/queued/pending=0、maintenance 为空，node/install 身份保持。journal 为 completed；备份 quick_check=ok、FK 错误 0、当前 7afb schema marker、业务 2312 行。后续独立 verifier 的 typed 比对通过，具体证据如下；新免费 settlement 会话 `10334` 在该时点刚启动，后续同步失败结果见下一节，不补认为通过。

正式产品升级功能由独立 worktree 的 `d18e44e` 整合为当前主 worktree 提交 `0d4dd840090590b3970a7528e67f940414ec62ff`，47 个文件、7045 行增加/39 行删除。整合前独立 worktree 的最新全仓 Go 测试和 vet 通过，前端 726 测试、tsc、build、6 个浏览器 fixture 及 review 通过；fixture 不等于 native 验收。整合后主 worktree 的全仓 Go 测试完整通过，desktop 59.420s、jobhost 24.756s、SQLite 95.873s，全仓 vet 通过。该正式流程尚未完成真实 SSH/systemd 验收，运维 R4 升级不能替代它。schema、Job 和节点配置格式未变；必填 `node.upgrade` 对象或显式 null 是唯一当前 DTO 的 breaking 变化，四项升级 API 的当前契约见[远程 runner 升级设计](remote-runner-upgrade.md)。既有失败历史和其他未完成验收范围保持。

只读 verifier 会话 `73600` 退出 0，current → backup → current 的真正 typed business SHA 均为 `4844878864421964db5126084b11aa83cd6286348b9c52d2d6368660206d0632`；该次比对时八个 Job 全部 settled，计数为 case 1091、event 1175、rev 14、runs 4、reports 4、perf 4。schema marker 为 7afb，56 个有 SQL 对象的 DDL SHA-256 为 `496a3b23dcdea695dd3ab90b54d081cc2470aa7e7975d53c97ca698164e73ce2`，current 使用 WAL、backup 使用 DELETE，二者 quick_check=ok、FK0。进程前后 PID 220541、start tick 23877733、NRestarts=0、实际 R4 exe SHA 和 UID 998 保持，control/Launch/提供商调用均为 0。原字节证据 `%TEMP%/r4-readonly-verification-606718e14d565f04f071b3f0.safe.jsonl` 为 2438 字节、SHA-256 `c5e20ba3cda0b4786fdb212b9afd7bc42f8bd0a09cbd3a9e8393772831fb3e7e`，verifier 工具 SHA-256 `80fc4c68630670ce682c9b795797ea974d5829e7b51be5379201347bdcc53c4c`。这证明本次程序切换保留业务与身份，不将免费任务或产品升级流程的验收转为通过。

## 2026-10-16：R4 业务收尾完成但同步失败，R5 仅构建完成

会话 `10334` 最终 exit 1。免费 Job `d62994b6-f054-4a7d-a14e-0e167dc1023e` 仅 Launch 一次，工作区 `%TEMP%/llm-remote-r15-settlement-4xkjh3sv/` 保留；accepted 33487ms、running 35013ms、completed 66119ms、settled 71094ms，终态至收尾 4974ms，healthy=true、Pending=0。随后 180s mirror 窗口在 role 252082ms 达到 phase_deadline，实际 Case/报告同步未校验，不能记为零请求或整体验收通过。保留客户端 SQLite 的只读核查为 outbox 1 条、仅旧 foreign Job `72f9…` mirror sequence 12，当前 Job 尚无 mirror。resubmission=false，未调用付费提供商；父端观察摘要 `%TEMP%/llm-remote-r4-settlement-20261016.safe.json` 为 1447 字节、SHA-256 `be7032a70aa70a1da5de57143af8803391439e6cfaa412b32da9690ccdf5cd2e`，它不是原始 raw 输出。

只读 HTTP 会话 `51503` exit 1 时，进程仍为 PID 220541/NRestarts=0/原 R4 exe SHA；GET runner HTTP 200、562ms、healthy/ready、Pending=0。ListJobs after=0/limit=25 返回 HTTP 503，111 字节、6385ms；目标 Job GET HTTP 200、1815ms、completed/sequence 469；目标 events 已返回 HTTP 200，但正文仅收到 1966080 字节后在 30007ms 超时，partial body hash 不能作为完整报告或页面摘要。旧 `72f9…` Job HTTP 200、4511ms、completed/sequence 12，events HTTP 200、6312 字节、568ms。业务完成、元数据查询 503 与大 events 传输 30s 截止属于不同失败层，不唯一归因先前失败。原安全输出 `%TEMP%/r4-mirror-http-bab232c7a8e64ab71b36569d.safe.jsonl` 为 1659 字节、SHA-256 `ceefaaa4c26c7da725e4f00a842e623c537e3235d3e97d4e63e4b40775f075ae2`；实际 loaded script SHA 为 `2710497220e26c44b1c25fbeb3bc047e20db9ce20794d1a75c9b4c256492d0ef`，磁盘后改的 e4cc 版本没有执行，不能混入本次证据。实际 loaded 271 版已由原修改记录逆向重建，核对原 SHA 相同后以 O_EXCL 存档为 `%TEMP%/llm_remote_r4_mirror_http_probe.executed-2710497220.py`（10649 字节、139 行）；这是事后可核验重建，不冒充事前自动备份。

基于 clean `53b8e0ad00ffcf371a0be90ad0670d8519773f83` 的 R5 已构建，尚未部署，native 尚未启动。产物目录 `%TEMP%/llm-runner-remote-e2e-20261016-r5/`：amd64 13877410 字节/SHA-256 `ba1ac137f80ad0538140231e297abe67ac087e3cb016160686ea429af22cf044`；arm64 12910754 字节/`c3ef992e2d794f467dcd7ebc9fcdc0f1f21121774e7cc206ec8817ffa8d56fe9`；native 26772992 字节/`3ce48410086289f1dc0d74d962de82b9ace0e79817e7f20ea2fbe785e51b44f4`。完整 manifest/helper 摘要见该目录 `r5-build-proof.safe.json`（14416 字节、SHA-256 `b66dd73af6ab203dce1cb30258c5f053faf7dc009d1d5daf7f4167da82010c71`）。Wails 自动 tidy 的首轮 attempt 证据保留但不作为最终构建证明；恢复 go.mod/go.sum 原字节后以 `-m` 重建，9 个输入摘要相同。buildinfo 没有 vcs.modified 字段，clean 来源依据 HEAD/status、9 个输入 hash 与 manifest，不能虚构该字段。

`remote-sync-fairness` managed worktree 从 53 基线、分支 `codex/remote-sync-fairness` 分三路修复 ListJobs 输入字节前缀分页、有界 JSON gzip 协商和已导入文档重复，当时仍未提交、部署或验收；调度公平性及优先处理来源缺口仍在范围内，不宣称本轮修完。主 worktree 的 opt-in `apps/desktop/remote_node_upgrade_live_test.go` 已独立提交为 `80d435276c964ebfd469220f236c01da697d0b5a`（527 行），安全测试、默认 live SKIP 和 vet 通过，正式产品 R4 → 下一修复构建当时尚未实际执行；R5 仍是 53 clean 的历史构建，不能把该测试提交当成其 source，R5 有产物不等于产品流程验收。其余旧失败和未完成范围全部保持。

关闭观察会话 `1713` exit 1，是新的测试目的而非重投 d629。唯一新 Job `e700cb98-6eec-4f7a-8d32-703e99eb114b`、accepted ReportID `a5c0247d-5696-4e00-ae08-1e493243629e` 的工作区为 `%TEMP%/llm-remote-closed-workspace-9nndrqgd/`：accepted 17.622s，关闭前 running/sequence 7 为 19.776s，DesktopApp.shutdown 同在 19.776s 返回；独立 closed baseline 21.933s 仍 running/sequence 7，其后才释放 Mock gate，23.866s 观察到 completed/sequence 13。这证明 shutdown 返回后执行继续并完成，但处于同一 OS 测试进程/MemoryStore 边界，不是 native OS 进程退出。随后 reopened 阶段等待 80s 超时，整体 passed=false；同 Job 重开、封口样本、gallery 和 origin 均未获验证，样本/请求数量未知，不能写为 0 或关闭恢复整体通过。原安全记录 `%TEMP%/llm-remote-closed-review-x6vzmm2q/acceptance-closed-observer.safe.json` 为 481 字节、SHA-256 `c35acdae785d2cc3a5cf841bdc442202cafc0aedf78445f09fffe2a760ddff51`，保持 false；父端 observed 摘要 `%TEMP%/llm-remote-r4-closed-progress-20261016.safe.json` 为 1887 字节、SHA-256 `49abe748873ccda2162ead47bd8fff44f62ee4adcde61ebf7554a58d6eac857f`，不是 raw。使用前述 558f6b… helper，service_before 为 PID 220541/start tick 23877733/NRestarts=0，没有 process_after，不能补造后验进程事实。不付费、不重提；在该次记录时，完整重开恢复及新 worktree 的 sync/gzip/ListJobs 修复仍待后续提交、部署和验收；后续提交及产品升级首轮结果见下一节。

## 2026-10-16：同步修复和 R6 clean 构建完成，正式产品升级首轮未取得完成确认

`01a30b30e33e9af4b8bfa754ea3cc441eb10ed24` 已提交 ListJobs 按冻结 spec 原字节预算选择分页前缀、当前管理 JSON 的有界 gzip 协商，以及同步跳过已导入文档的修复。入页项仍完整校验，游标只推进到实际返回项；gzip 分别限制 wire 与解压后字节，不改变 canonical 文档、报告原字节或 Range 续传。独立报告失败后继续处理其他独立文档，保留首错、取消边界及 sealed performance result 先于 archive 的依赖。唯一当前 contract、5 秒 HTTP 存储期限和 30 秒请求期限保持；不是历史格式兼容。具体规则见[当前契约矩阵](remote-execution-contract-matrix.md)。此前 `remote-sync-fairness` 阶段“未提交”的记录对应当时状态，本次提交并不证明长历史下新任务的及时性或本机 outbox 优先调度已完成。

父端最终 Windows `go test ./...` 和 `go vet ./...` 均通过，定向 WSL 测试、真实 HTTP 同步检查和独立 review 通过。跨平台 SSH 的既有 durableUpload/provision fixture 失败在替换回旧代码 overlay 后同样出现；该对照不能支持 Linux 全套通过，也不能单凭它把失败归因于本次改动。真实 SSH/systemd 产品流程及完整桌面恢复仍单独验收。

R6 构建前后 HEAD 均为上述 01a30b3、status 为空、source_dirty=false；9 个输入文件与 frontend dist 原字节保持。Wails CLI/module 均为 v2.15.0，native 构建通过；前端复用与 R5 相同的 tracked tree 和 dist，没有重复执行 pnpm build 或重新生成 bindings。产物位于 `%TEMP%/llm-runner-remote-e2e-20261016-r6/`：

| 产物 | 字节 | SHA-256 | 已验证边界 |
| --- | --- | --- | --- |
| linux/amd64 runner | 13930658 | `9c2ca1e639dc02c14b8a533ba07ece8a23d9af4a8fd1db48641b710839aa2d20` | WSL version 自报 R6 且 build ID 同摘要 |
| linux/arm64 runner | 12910754 | `b349e593e334ba46b5f55431add60a172f8b1ae69c63ff3fa436c2a3f0fd25d6` | 构建与摘要，不是 arm64 实机运行 |
| native R6 preview EXE | 26779136 | `6ff6946cd38e6e2a347757a91d42cb8c7a3b27a0a2b3149e199d5307512c7893` | 构建通过，未启动 native 应用 |
| acceptance test EXE | 37046272 | `742face53f96f361eda05b9de2ad75465dead38e70004fffdbacd26597f98916` | 正式升级测试与 dispatch 已链接；构建记录本身未执行验收 |

manifest SHA-256 为 `852279281807feefa228cc524e750091cd90ddd650b604de26ac037b6a50cc11`，release 为 `remote-execution-20261016-r6`，source_commit 为上述完整 01a30b3，source_dirty=false。完整 `r6-build-proof.safe.json` 为 10039 字节、SHA-256 `2059d38203d65c959d7d55b86976a829b09d74fa1c6c88dec18de4fda36ecc27`；构建证据记录 SSH、upgrade commands、provider requests 均未执行，native 未启动。schema checksum 仍为 `7afb439aaccf28d19e7e7381e2ef5611e013f062676e3706d44b3313d52c0708`。这些是构建边界，不能代替后续真实产品升级与回归。

正式产品 R4 → R6 已仅调用一次 Start，operation 为 `bf9106b7-a913-4bb5-b309-4e295ecd8b1f`，证明目录 `%TEMP%/llm-product-upgrade-proof-d1dudsl2/`。会话 `8090` 最终 exit 1，6 分钟 role 未取得 completed；原 summary 的 passed=false、failure=`original_operation_requires_review`。start_calls=1、retry/provision/provider/recover=0，未发起第二次 Start。before_start 的 eligible/idle 和 check_preserved_old_build 均为 true；客户端记录 uploading 自 05:16:44.595 至 05:18:58.299，随后 error=`runner_installation_failed`、confirmed=false、resume_attempted=false、actual_runner=null。客户端记录未确认服务器升级操作的最终失败、回滚或传输失败所在层，也不能单凭错误码确定真实 deadline。原 `%TEMP%/llm-product-upgrade-proof-d1dudsl2/acceptance.safe.json` 为 1422 字节、SHA-256 `d23ed80c51c9051239e1c8c480e638caa79ad237a2c1eb4b5033a86f9e424858`；`events.safe.jsonl` 为 4065 字节、SHA-256 `4cd9d377ef14664eaa6fe5222195dd76f4ef66e4a6479578f2abfe24b003e674`，两者保持原结果。

升级前只读 capture `%TEMP%/product-upgrade-capture_baseline-fda799c9fd178a3726753754.safe.json` 为 7089 字节、SHA-256 `eb165d42edab7e6bc014d93baaf1b9ab25cf35b34efe3b7de7bb8e7ba37da99b`：10 个 Job 全部 settled、Pending=0，case_results 1552、events 1657、product_rows 3269；typed business SHA 为 `9ca036a90b8dd6a93e37b239d2842594c4801e694bba38c4d73725764b56e4a5`，quick_check=ok、FK0。capture 的 control/Launch/provider 调用均为 0。原 8090 结束后，父端仅只读再次 capture，会话 `49172` exit 0；新 O_EXCL 文件 `%TEMP%/product-upgrade-capture_baseline-a10f42b28397e5ff6e87f58f.safe.json` 同为 7089 字节、同一 SHA，且与原基线逐字节相同。到该次 capture 时实际仍为 R4 PID 220541、原 start tick、NRestarts=0，ready/healthy、Pending=0，现有业务、程序/配置/unit/identity 保持；该读证据不判断 upgrade stage/temp 是否写入或远端 operation journal 是否存在，后续同 UUID 的只读 peek 结果如下。

父端只读 peek 会话 `6125` exit 0，原字节 `%TEMP%/llm-upgrade-bf9106-readonly-proof-ojxocn5v/facts.safe.json` 为 1269 字节、SHA-256 `17db69cb1b13736862437c5dc248d0790fb9e4083badbbce9eae1f28dd966862`：该 operation 的 journal absent，固定 upgrade unit 为 not-found/inactive/dead、MainPID=0；runner 仍为 R4 PID 220541、start tick 23877733、NRestarts=0、UID 998。stage 中只有 `request.json`（346 字节、SHA-256 `b924ce37ee080e9491d7d19d80bda943d987a86a261b311dd67b9e278e0e9c67`）和 root 所有、mode 0700 的 partial `.upload-W3IPOEXEVE4RBJYSNQXAMUNLHM`（stat 大小 13930658 字节），unknown_entries=0，没有正式 `llm-test-runner`。该次 partial 仅 stat、未 hash，可能是稀疏文件，不能据 full-size 判断内容完整正确或上传已完成；后续独立 hash 与读回验证另记。peek 的 Start/retry/recover/upload/cleanup/provider 均为 0。当时结论限于升级未获完成回执、R6 未部署、原 R4 业务与现有程序保持；后续同 UUID 的诊断、两次 Retry、Recover 与运维恢复如下，不将旧观察当成后续状态。

query-only 验收工具另提交为 `d38e61a3237ee9e3a8588618c78f8e9853d104cb`，将 execution 与 daemon release 的等待分开；工具验证不等于原 Job 的 query-only 恢复，当时该项恢复/回归尚未运行或验收，后续结果另记，也不改变 R6 产物的 01a30b3 source。既有执行稳定性、完整关闭/重开恢复、正式升级、共享 foreign 管理、文件资源、最终 native、第二节点和 arm64 实机验收范围保持。

## 2026-10-16：四次产品观察仍失败，运维恢复 R4 与 typed 数据核验通过

同一 operation `bf9106b7-a913-4bb5-b309-4e295ecd8b1f` 的原 Start 和两次显式 Retry、一次显式 Recover 均保留客户端 exit 1/passed=false，聚合 Start 1 / Retry 2 / Recover 1，无 Provision 或付费请求。后来的运维恢复与原结果分开，不追认这些尝试通过。

| 会话 | 本次产品调用 | 已观察事实 |
| --- | --- | --- |
| 8090 | Start 1 | uploading 错误、未取得完成回执；原证据见上一节 |
| 13525 | Retry 1 | 新 5c client 仍未取得终态回执，uploading/runner_installation_failed；之后只读 capture 与原基线相同 |
| 54151 | Retry 1 | 4a client 完成上传，06:00:56Z confirmed/backing_up；06:02:50Z 本地仍 backing_up/runner_installation_failed，未正向 Resume |
| 20011 | Recover 1 | 六分钟观察仍失败、backup_restore_verified=false；fresh peek 未见本次服务器进度 |

三个新增原始 summary 分别位于 `%TEMP%/llm-product-upgrade-proof-hlht2jvt/acceptance.safe.json`（SHA-256 `1c517095af101653e362273862aa897301fb74bf4791227b2c7a76fac5785128`）、`llm-product-upgrade-proof-twidowfe/acceptance.safe.json`（`f5682f0b0f49b9f7243c8b4debfb35f78e5084cecea5b158b86fb11d0f0eae6b`）和 `llm-product-recover-proof-_qfzi99x/acceptance.safe.json`（`89ae836849d1124f9aee21b69cb0e1221e52ce4bf94c327cb6c29167078fb981`）；各目录 events 原样保留。前两次尝试前后的三份 7089 字节 capture 均为原 `eb165d…`，只证明那两个观察窗口的数据保持，不能沿用到第三次停机后。

上传诊断区分了内容与传输：65166 的服务器本地 partial SHA 与原 R6 匹配/stable，生产 SFTP 单独读回却在 120000ms deadline 结束；72558 在 bulk 五分钟预算下 172540ms 完整验证 13930658 字节、error flags 全 false、无 KA close 事件。未完成读回的 matches_expected=false 不等于内容不符，wire 未观测不等于零传输。`5c1af74` 将实际 8 处调用拆成 bulk 3 / metadata 5，`4a78a9c` 修正活跃入站但 ACK 延迟的 keepalive 边界；它们的 clean client/appVersion R6 与 target 原 01a30b3/R6 分开，原 target/helper 产物保留。Windows 全仓 test/vet 通过；单端口读回和本地 fixture 不唯一还原 8090/13525 的失败微步骤，也不证明整次升级成功。

92238 只读服务器 journal 为 current-contract/backing_up/error 空，runner inactive，upgrader PID 234213 active；这与本地安装错误属于不同观察层。96852 两样本相隔 1045ms，CPU 增加 139 ticks、rchar 4515451063→4518420009，105074688 字节源 DB 存在而 backup DB/manifest absent，证明仍计算/读取，支持重复 decode 的归因方向但不唯一定位执行栈。28576 在两次身份和阶段守卫一致后，仅通过 pidfd 发送一次 TERM，197ms 观察退出。38120 和正式 Recover 后的 39585 facts 逐字节相同（SHA-256 `6450c796ef2456c1c8d98e80f3836856a3adbed655f0a94782b284342890f997`）：upgrader failed/exit 1/MainPID 0、runner inactive，服务器 backing_up/runner_upgrade_required，resume=false/restorable=false，时间仍为 06:28:06Z。工具零直接 DB/file 写入不表示退出中的进程没有更新 journal。CPU 原证据为 `%TEMP%/llm-bf9106-backup-process-readonly-proof-eqaw_ggm/facts.safe.json`（SHA `5e5e5775fe4b8d7459530470dc0d471b2e497b226fe20ffebca1663f3ec22475`），TERM outcome 为 `llm-bf9106-terminate-upgrader-proof-ero83e6r/outcome.safe.json`（`ae820546611237368184f2db6a445e8405690a624d47349ea14f0d40bdd9affc`）。

父端在全文主审与 self-test 后仅执行一次当前 CLI `recover-upgrade` 同 operation，86792 exit 0 / 3847ms；独立 reviewer 的无 blocking 复核发生在实跑之后。两次 guard 均核对原 journal、R4 selected、runner inactive/upgrader failed，持有 stage R6 文件描述符并验证服务器本地 SHA 及原 request b924… 后，只调用当前 CLI。工具没有直接写文件/DB、KILL 或付费请求；CLI 自身可以按契约写状态和控制服务。`%TEMP%/llm-bf9106-cli-recover-proof-7t5ghvbz/outcome.safe.json`（1544 字节，SHA-256 `8ff31162358e8bea834fac874f0b8bd560e550f53f50fba076331372f86f487e`）明确 desktop_api_verified=false、backup_restore_verified=false。

独立只读 80927 确认 journal rolled_back/error 空，changed_at=2026-10-16T06:45:47.231941566Z，resume=false/restorable=false、实际回执为 R4；runner active/running、PID 238933 / start tick 24712086 / UID 998 / NRestarts 0，实际 exe 仍为 R4 `1c4c2bab…`，ready/healthy、active/queued/pending=0、预算 1/16/2000，旧 upgrader failed/MainPID 0 记录保留。证据 `%TEMP%/llm-upgrade-bf9106-readonly-proof-j_y9f64v/facts.safe.json` SHA-256 为 `13b6729570f7acf8d178082c26a3b218eabf024919b80e1b29cd678cfb697d83`。这是原程序恢复运行，不是 R6 升级完成或 Desktop API Recover 通过。

独立 HTTP 及当前 typed 数据 capture 50837 退出 0：`%TEMP%/product-upgrade-capture_baseline-61d4b5be006fb05427428426.safe.json` 为 7089 字节/SHA-256 `847349412685748465dc52d38632f278ea35be71fbf1f773d2fb983c6eef3ea8`。与原 eb165d…基线比较的 database facts、origin、runner info、既有 files、build、version 全部一致，PID 220541 → 238933 为正常重启；10 Job 全 settled、1552 case_results、1657 events、3269 product rows，typed business SHA 仍为 9ca036…、quick_check=ok/FK0。比较 proof `%TEMP%/llm-bf9106-recovery-data-comparison-3d030f4b42fa4ca2823682ce38fe703a.safe.json` SHA-256 `51beedc4529b1db4b5ec45384ed2e1f81e0d931daa94427f7936ab0e0fb4b055`。结论仅限当前只读 typed 算法与既有节点/配置/令牌 identity 摘要等检查一致，不声称 physical DB SHA 或私有 executor key 字节相同；未做 DB 格式转换，未升级桌面旧库，也未删除或 reset 数据。

本地后续提交为备份分组`3ca83d5`（测试 owner 读取 258→2，短 ID 页逐文档校验）、显式 Check 同 op 只读刷新`1ecad87`、same-op Recover harness `9697620`；main 全仓 Go 测试 54005 通过（desktop 94.413s / SQLite 150.994s / remotenodes 6.813s / jobhost 62.345s），全仓 vet 通过。SSH staged-digest 修复仅把已有完整 artifact 复验改为服务器 sha256sum；HasSHA preflight、私有目录及文件 UID/GID/mode/size/mtime 前后核验、当前程序 self-digest 守卫、严格单行 4KiB 结果和固定 UUID 路径保留。新上传 UploadNew 的独立 SFTP 回读、写 hash/fsync/rename 保持，期限不增、无格式/schema 变化；本地 SSH tests/vet/review 通过，RED/GREEN fixture 的旧路径下载 2621440 字节、新路径 0 字节。该修复已本地提交 `86d631692c374295c1ddf5ed6b59d17eade56cb1`；集成全仓 Go 测试会话 55718 exit 0（desktop 53.965s、remotenodes 3.661s、SSH 2.782s），全仓 vet exit 0，该轮记录时尚未进入实机。后续 476 client 使用此修复执行新 operation，但不能将它定为先前真实 API 失败的唯一微步骤。

R7 为 clean `3ca83d5121e902febbeb6ed95176d40cef2abee1` 构建，source_dirty=false，构建记录时尚未部署，后续实机结果见下一节；amd64 13934754 字节/SHA `1b94dfe7ec7d7e8fa80227e60151cee415a15573cc71f7ca5ad27a17b070d7c7`，arm64 12910754 字节/`1084c649cc288addf60f1885dcca3cb52e86d1a5687281a3ed425cfb5371e1c9`，manifest 615 字节/`9bb0318c51b4c6e27b5ae36adce07cc46dd80576e7394944f5b229e98ee0ee96`。目录 `%TEMP%/llm-runner-remote-e2e-20261016-r7/` 的 `r7-runner-build-proof.safe.json` SHA 为 `2961c6ce6fd4cfe5904bc7d191740028a0560326b24b7f4199953d80d3a2b922`，`r7-clean-source-verification.safe.json` 为 `04a408d1be7b7f5e7a8ba973a3c3fad47399c8922ac6e312c6fc5efbbcc799eb`；该 clean 源码全 test/vet 另通过（155.529s，SQLite 147.771s）。该构建阶段 amd64 仅 WSL version/build ID 运行核对，arm64 仅 cross-build；该轮 runner 构建未生成 native/helper，后续 client 另记；client SSH 修复不在 runner 生产依赖内，不能把后续 client 源码当成 R7 source。

K3 保存证据离线分类为：拒绝预期 400 而实际 200 共 124 例、预期成功实际 400 共 6 例、HTTP200 字段/语义失败 13 例，总 143 例（Chat17/Responses126），均 completed/issues0；这不证明适配器必然正确，也不能统一归咎于 provider 或改预期变绿。三个 Responses 流的离线复现测试通过，原案例仍失败；保存事件保留 JSON payload，没有原始 SSE wire framing：终结前已有 31/30/5 字符，但保存终态 response.output 均为空且与 stored/decoder/pipeline 一致；能排除非空终态在同步入库时被抹空，仍不能完全排除当前 decoder 的终态优先策略，不能将责任统一归于 provider，也不能伪造 delta 回填原始终态。原 DB/summary 摘要未变、network/provider=0；中文分类文件 `%TEMP%/llm-k3-failure-review-f1df34518aa04ebd9f6b33180a6b1e93.safe.zh.md` SHA 为 `a1cba7fef8b443970153b0613b0ee2d7d7d2e88c51424bf18a6366a669d3bac4`，流重放 `llm-k3-stream-replay-e70fb6a2d02643c3adbe3ca2fb54949e.safe.json` 为 `af4b95eb17caa47c6ae440dde891842cf5347fe770298b781ebf4be97cd1a5e7`。原 K3 判定、性能 3 次成功与旧来源验收 false 的区分保持。

在本节阶段 R4 已恢复健康、typed 数据核验通过，原四次产品观察仍未通过；后续 R7 新 operation 的结果另记。报告同步、原 Job query-only 恢复、真实桌面进程退出后的重开、共享 foreign Job 全视图与控制、最终 native、文件资源/附件/release 与回收、历史用户库转换、宿主重启、第二节点和 arm64 实机等既有范围继续保留。

## 2026-10-16：R7 后台升级、独立数据核验与原报告恢复通过

新 operation `346da881-372e-42a6-88b2-b2b8b61fdcfa`（配置 `1b5723bc-51ed-45e7-910d-af15c3292df2`）已由同一次 Start 在后台完成，实际 R7 ready/healthy；独立 backup/current typed 核验、两个原免费 Mock Job 的报告 query-only 恢复及重开 Desktop Check 纯查询均已执行通过。原 bf 的 rolled_back 与四个客户端 false、此次首轮六分钟 false 均原样保留，后续查询不追认旧观察通过。

本轮 client 为 clean `476a4b7`，包含 `86d6316` 的 SSH digest 修复；target 仍是 clean3ca 的原 R7 `1b94dfe7…`，没有重建 runner。新 helper SHA `68009cbfe0830cc26c7fca338e3d6616e4d1746a1d1d87f9928e6a4ef3f0a879`，native SHA `b5658f9383232e12dada89b31cc8de944a922ff304957df8fba2cf58ddd8e730` 仅构建未启动。28669 仅 Start1，Retry/Recover/Provision/provider=0，六分钟后 exit1/false；客户端先后 observing backing_up、remote_connection_failed、Check 清错及 backed_up，没有在窗口内取得 completed。原 `%TEMP%/llm-product-current-upgrade-proof-cx2ctnk1/acceptance.safe.json` SHA 为 `5511618bcee5242d12da883d219b04169b6274b73b8a5c3b30b5006262d4dd58`，events 为 `7cf74a1db249d3e8d0551f06942ecebf07fb82894d5f8ba952dd302855b5aa0c`。

正确 configuration-bound 的独立 59949/86840/98639 依次确认 backing_up→verifying→completed；准备阶段残留旧配置 f153… 的 peek 被审查拦住、未 SSH，不能混入实跑证据。98639 的 current-contract journal changed_at=2026-10-16T07:11:02.337999717Z、error 空、resume_attempted=true/restorable=false；runner PID244293/start24859722/UID998/NRestarts0、实际 R7 exe、active/running、ready/healthy 及 active/queued/pending=0，upgrader 正常结束。证据 `%TEMP%/llm-upgrade-346da881-configuration-bound-readonly-proof-wngbv1_w/facts.safe.json` SHA `cae0598fe7c3389ef0e14b6703f0517f941d83aca36856d91ce0f65fba564b47`。这是同一 Start 后台完成，未再提交或重试。

独立 10427 `verify_upgrade` exit0，`%TEMP%/product-upgrade-verify_upgrade-d0cbb1386164054a8ec6c99c.safe.json`（7880 字节，SHA `be798e1c5d86fb6f7ee87118bc71762988d995bd30260acd85081611cefb601b`）确认节点 identity 保持、R7 进程健康闲置；实际 backup 为 105074688 字节/SHA `a3829a87175eeffd0c01ff6a14028116aa92c97e882eab38597f786894036247`，backup typed=R4 基线 847349…=current typed，10 Job 全 settled、1552 case_results、1657 events、3269 product rows、quick_check=ok/FK0。备份 program/config/unit 对应旧 baseline，journal/manifest 已校核，但 `product_manifest_business_sha256_recomputed=false`。这不声明 live 物理 DB 与 backup 字节相同或私有 key 字节相同，也没有实际 DB restore/故障 rollback；旧格式桌面库未转换、未删除或 reset 数据。

原 ID 查询 27151 exit0，两个 Job 均 query_verified，来源仍为冻结的 R4，未被当前 R7 覆盖。全过程 daemon PID244293/start24859722 不变，Launch/blockedWrites/provider 均 0。

| 原免费任务 | 已恢复的报告事实 | query 耗时 |
| --- | --- | --- |
| d62994b6-f054-4a7d-a14e-0e167dc1023e，R15 Mock settlement | reported/observed/expected 均 230，case identities 匹配，report `f92dc857-b5f3-48d8-9c14-2b62b99dcee5`，origin verified | 265054ms |
| e700cb98-6eec-4f7a-8d32-703e99eb114b，原 close-observer 性能任务 | 原 report `a5c0247d-5696-4e00-ae08-1e493243629e`，sealed/archived/origin verified 均 true | 261269ms |

观测日志 `%TEMP%/llm-r7-original-query-d629-e700-20261016-0719.observed.safe.jsonl`（7531 字节，SHA `c6d76a3c852660674ba89c563245ebc10032d698c5c0b97fa21cd8fe0c848bc2`）是 PowerShell 捕获的 safe events，不是原始 SSE wire。首次 25829 在 SSH 前因缺测试 guard 目录拒绝；父端补 4 个空目录、24 个既有 config/DB 逐 hash 不变后才运行 27151，未重投 Job。d629 的 230 是免费 Mock 报告齐全，不是 K3 兼容性 230 全通过；e700 恢复也不证明真正 OS 进程关闭验收。K3 仍为 Chat67/84、Responses104/230、performance3/3，原 summary false 不改。

重开 Desktop 查询观察 29264 的 v1 原 false 保留（`%TEMP%/llm-product-upgrade-observe-proof-seqscz9q/acceptance.safe.json` SHA `a4a70c37213ab6b7785351ef5eda6c9e1fa925aefd4ba1692eff242a59f7615a`）：它已 Get completed R7、freshCheck/Get 稳定、intent 保持，journal_reads1；失败来自工具将顶层 Configuration.Revision 写入序号 10→11 错误锁为 immutable。精确诊断只发现该字段变化，nodeRevision3 与 binding/pin/authrefs/default/meta 不变，Observation 不参与该比较。修正工具的独立实跑 68012 exit0/PASS：`%TEMP%/llm-product-upgrade-observe-v2-proof-pc40p1qn/acceptance.safe.json`（2206 字节，SHA `728eff6939b168a7fc1e3e4156c0ee45831e66eb611f1a1c9de12ac411051be7`），events SHA `053bd63a650af4587b5af893a6db750ca893567fe8a3b57aa7e3869a7671b0bd`。完整 intent hash 不变；配置语义比较仅排除 Observation 和顶层 writeRevision，write11→12 而 nodeRevision 及全部身份/授权引用不变。check_calls1、journal_reads0：本地已有终态，本轮 fresh bound Check 不能称又读 journal；重复 Get/Check 与 R7 健康闲置通过，升级 mutation/Shared/artifact/provider 均 0，1 次任务连接被 guard 阻断。

在本节阶段已验证服务器升级、实际 backup 与 typed 业务保持、指定原报告恢复及重开 Desktop 纯查询；当时真实 OS 退出/重开与优先同步调度仍待验证，后续结果见下一节。其余既有范围按各项实际验收推进，不因单一查询通过而全部完成。

## 2026-10-16：优先同步已提交，单 Mock 真实 OS 退出与重开恢复通过

优先同步调度已提交 `c2d39bb351af8bc2b44f69107c324397a8f86166`（10 文件）。父端全仓 `go test ./... -timeout=15m` 会话 99319 exit0（desktop61.336s、SQLite106.737s、runnerhttp29.175s、remotesync0.679s），全仓 vet exit0，10 个源码 SHA 前后相同；`%TEMP%/llm-priority-verification-3a7520383767414dbf1a24ba7696ccfc/verification.safe.json` SHA 为 `16caf1edbe04310aad1cd2ce22db0da2c53886d4669ae40ccf5b238675090446`。本轮变更内部 Watcher/Repository 端口与调度，未改变对外 DTO、数据库 schema 或持久文档格式，无历史数据转换。本地实现/测试完成，本轮增加了既有共享历史下单个新任务完成及重开恢复的实机证据；当前客户端的完整大任务、多 client 公平性仍待实机验收。

验收 client 来自 clean c2d39bb 及冻结 TEMP overlay/显式 map，helper 为 37265408 字节/SHA `bff20b08d76378a35ad72ade4cf8b5e56ffa927c5f597e52e04a9d903423743a`；build proof `%TEMP%/llm-client-priority-r7-c2d39bb/process-boundary-build-proof.safe.json` SHA `354a0c2686242e8176d26c95dc3c3c97040bab329fcccbab6a01c39806771de8`。native 为 26819072 字节/SHA `769c9b325bae5bf858917bf4a36576c1d324b4413237846ab0116a624bec1e1c`，同目录 `client-native-build-proof.safe.json` SHA `faf7442224200a82fd8ab081fa79719ed3be5ef4f5291a6e4a75f603cee269fb`，仅构建未启动。实际 runner 仍是原 R7/3ca 源/1b94…，本轮未重建或再次部署；当前 client 的 runner 共享依赖保留 210 个既有输入不变，但新增 `internal/application/remoteresults/priority.go` 和 `internal/persistence/sqlite/repository_submission_priority.go`，不能声称当前源码与 3ca 完全相同。

父端唯一真实进程验收 wrapper `37845` exit0/PASS：workspace `%TEMP%/llm-remote-closed-process-workspace-j5chp8tr/`，config `419240b0-f223-453f-8b3b-d2629adf7d06`，Job `d42161cf-f6c7-4960-b25a-285f35beecb3`，report `58da40ad-66ab-4c2a-9cb0-2d6fd6cb5839`，spec SHA `a190009db22bfe86a692c4336a05c74361616f19c1808ba47b30a4175c6c9cae`。整轮观察上限 180000ms，`observed_ms=37369`包含 Mock setup 和三 role，在 summary 写盘与 owner.close 前采集，不是 wrapper 总生命周期精确 37.369 秒。

| OS 测试进程 | 证据时间与结果 |
| --- | --- |
| submit PID15372 | 实际进程 exit0 在 26960ms 确认，随后才创建 audit |
| audit PID43596 | submit 退出后的 baseline 为 29467ms/sequence7；30088ms 推进至 sequence8，30524ms 到 completed/sequence13/revision4，进程 exit0 |
| reopen PID61536 | 36900ms 恢复原 default、Job/report/spec/receipt/executor；sealed/archived/origin verified 均 true，sample1/success1，进程 exit0 |

本轮 Launch1、Mock perf_count1，paid/provider0、automatic retries0、control0、query blocked writes0；runner PID244293/start24859722/R7 exe 前后保持。原产物 `%TEMP%/llm-remote-closed-process-proof-akxh_79a/acceptance.safe.json`（5717 字节，SHA `7a91a3b70d7299012ef1172a286cbc6cb336b181d90e5d22ef24c4b6d04ae872`）和 events（9475 字节，SHA `21a7a0394a151d2d347e491b16e8408919ea4e34975c927fd81d46893ab7fc6c`）保持。事后独立证据复核无 blocking，核过 11 个 build proof 引用、实际 proof 和冻结脚本，确认 coordinator 在真实 Popen.wait 取得 submit exit0 并 fsync 后才创建 audit，三个 PID 均 exit0、身份/序号与零额外执行动作成立；不改写审查先后。

该 PASS 覆盖单个免费 Mock 任务的真实 OS 进程退出后继续与重开恢复，native_window_verified=false；使用 scoped MemoryStore，不构成 OS keyring 或真实点击关窗验收。37.369 秒与旧 265/261 秒的原报告恢复不具严格同条件，不计算提升倍数或唯一归因。付费 K3 executor 仍为 R1、两个旧 query Job 为 R4、本轮新 Mock 为 R7；K3 仍 67/84、104/230、performance3/3 且未重复付费，所有原 false 保留。

在该次进程验收阶段仍需完整大任务/多 client 公平性、foreign Job 全视图与控制（当时尚未实现）、文件资源/附件/release 与回收、native 窗口与真实关窗/OS keyring、历史用户库转换、宿主重启、完整还原/故障回滚、第二节点/arm64 实机、CI 及发布交付等验收。本轮单样本与本地 priority 测试不替代这些范围。

## 2026-10-16：shared/foreign 当前契约已实现，存储转换与新实机验收待完成

本轮基于 a4ea4dc 冻结 51 个源码文件，实现本 client 提交任务与发现的 foreign remote 任务的统一查询/控制。JobView 删除旧六个 delivery 顶层字段，改为必填 nullable `submission`，对象精确含 delivery_revision、delivery、observation、prepared_at、delivery_updated_at、confirmation；foreign 必须 submission:null 及 actual execution，不伪造发送或接收时间。Query 改为 Job/Jobs/PerformanceResult/JobControls，七个 Wails 绑定名称和 ControlInput 三字段不变，Go/FE/fixtures/bindings 同时替换；旧 shape 明确拒绝，只有唯一当前路径。progress/result 使用 common Spec；同只读 TX 核验来源并对 Submission/Mirror 做 UNION UUID 去重及完整原文预算分页。完整规则与负例见[契约矩阵](remote-execution-contract-matrix.md)。

ControlRecord 新增必填 Target，冻结 Job/location/node/install/specSHA 和 nullable acceptance(origin+accepted_at)；foreign 必须来自 actual receipt，own pending 尚不知接收时可 null，保存后的完整 Target 连同 Command/创建时间永久冻结，后来获知接收也不重写旧 null。每个 command 以真实 Submission 或 Mirror 恰一 FK 建立 anchor，Create 同 write TX 重读 source 并核 proposal，outbox 复合 FK 绑定 command_id/job_id；重放、CAS 和所有读再核 anchor/Target/current source。已知 executor R4 而当前 daemon R7 合法，不比较 build 相等。GetJobControls 仅表示本 client 持久投递记录，不包含他人未投递意图；他人 confirmed 控制由 remote events 提供。foreign pending control 也阻止移除节点。

这是 breaking 存储变更：version1/name0001_protocol_runtime 不变，7afb439a…0708 被唯一 current checksum `adc400effb974273c9d7d18dc8cc7f2deb7623cd958c7f80d61b8010dca4be6e`替换；仅新增 job_control_targets 和 outbox FK，schemaObjects55→56（migration metadata 另算）。共用 schema 的旧 desktop/runner DB 被新 runtime 明确拒绝且保留原数据，没有运行时旧 reader、自动转换或 reset。TEMP 独立工具在 `%TEMP%/llm-shared-jobs-offline-conversion-20261016/`准备，尚未完成转换验收或切换。

实际历史用户库更早，为固定源 `766d1aa38961d13165383a5e3ed57207d558d5593c9f401e7d0f60213846faa8`，不能误按 7afb 处理；需 766→adc 直接 converter。源 187662336 字节，只读预检前后 SHA 相同（cbc5337e…4eb5）；execution_runs63/run revisions252/case results5482/comparisons4/comparison revisions8/reports61/perf43 均须保留。缺 execution_origin 的 reports/perf 仅拟在独立工具中补合法 unmanaged 显式 null，不能由 runtime 缺字段回退或当前 Probe 伪造历史来源。工具未完成验收、未切换，源用户库、服务器与 K3 旧 proof 均未改。

App 13files、FE 15files、SQLite 14files 及其余 9 个当前契约适配测试已冻结，模块全测/vet/独立 review 通过；App owner desktop61.073s、SQLite152.454s。FE 原完整 738 测试、lint/build 通过后追加一个测试，随后 App comparison 定向 60 测试、当前 bindings 下 tsc 及独立 review 通过；没有将最新全套 739 声称为已实跑。五个旧 live 入口均 SKIP，没有新 K3 或 SSH。最终集成 78581 全仓 `go test ./...` exit0（desktop68.802s、runnerhttp28.235s、jobhost24.317s、jobs0.415s、SQLite167.962s），全仓 vet exit0，51files 源码前后相同；`%TEMP%/llm-shared-jobs-integration-k48jzi04/verification.safe.json` SHA `294a69442d0824dd2a6309e87de4f765bd9010695882465ec03d12a0179e1d53`，go-test.log SHA `50977daf73557546ab4cb899e063487c4c6f229f724be20e78fbd2bab00f56b4`。

浏览器 fixture 实跑通过：1440×900 light、1024×768 dark、960×640 system-dark 均 identity 正确/非空/无 overlay/console0/外部请求 0/无横向滚动；foreign 不显示 delivery、按 revision3 模拟 cancel 后显示本 client 控制记录，own 显示 delivery，Escape 焦点恢复/Enter 重开/system change→light 通过。`%TEMP%/llm-shared-jobs-ui-ryulrs8_/browser-check.safe.json`为 2373 字节、SHA `5c54f496526a62fc9df2475c79427fd81da0e8a455e3bbe14bf46b384ab7be8c`。CUA 因 kernel assets 路径错误不可用，使用已有 bundled Playwright1.62.1/缓存 Chromium140.0.7339.16 完成，无安装依赖；fixture 不是真实共享控制或 native 窗口。Vite 自有 PID33652 核对后停止，exit1 是清理，不是验收失败。

新 600 秒单 Mock 共享控制工具在 `%TEMP%/llm-shared-foreign-acceptance-1e9984b8-833d-4673-8161-013d4a2d7dd9/`准备，尚未 compile/SSH/Launch；新 schema 转换/切换、Go 实机共享控制、native 都未验收。实际服务器仍为原 R7/3ca/1b94…，未重建/部署；前轮新 Mock 已增加业务，旧 10Job 基线只属于当时记录，不是当前恒定计数。旧 c2/180 秒单 Mock 退出重开 PASS、K3 及所有旧 false 保留，不能替代新 shared 契约验收。完整公平性/大任务、文件资源、native、历史库转换、重启/故障还原、第二节点/arm64、CI 发布等范围不缩减。

## 2026-10-16：626ccdd 离线转换副本验收通过，真实切换/恢复待完成

shared/foreign 已提交 `626ccdd1e14c8fcb154329459c5161cc66e2a043`。独立 TEMP 工具基于该 clean 源的 597 输入 archive 验收：三类 7afb pending/local/remote fixture 共 9 controls，保留 pending 未知 acceptance:null、confirmed/rejected 及原控制值；实际 766 历史源的一致性私有副本直接转为 adc，63runs/252revisions/5482results/4comparisons/8comparison revisions/22comparison runs 完整，61reports/43perf 根字段显式补 `execution_origin:null`，原 blob/text 类型及其他 JSON 数字/字段原值保全。当前 getter、typed 关系、完整 journal 逐事件 replay、imported 原字节验证、FK 与 quick_check 均通过；不宣称每条未报告 projection 与 replay 另做全表字节等价验证。

转换目标 `converted-766-history-clean626.db` 为 190853120 字节，SHA `42edad760773d78732f737af3931758ebf9cd92f7cd996a5491a5247368b7133`；其 conversion proof SHA `92f2b84d78d6f7555c600a522b86dce5dec7eba36af71bf84ad394428e2f96dd`。最终 `%TEMP%/llm-shared-jobs-offline-conversion-20261016/conversion-acceptance-final-clean626.safe.json` 为 11481 字节，SHA `4645a3224fb144414354cd3bb851ecd8faeedf111f4036b0bfbb01644f58bd24`。父端独立复核 14 tools、4 clones、负例及 review 证据的哈希，原 APPDATA 源库前后 SHA 均为 `cbc5337e54c00bd581d00ad7db8a097d6400f1f9b12338f950b1900860ce4eb5`。

旧 r1 转换被不可变 trigger 拒绝，incomplete 产物保留；r2/r3 仅在 fresh clone 的同一 TX 中核验固定 DDL、暂移 trigger、重写 104 份归档并恢复原 trigger。10 个拒绝负例、有效前缀/无效后缀整 TX 回滚、130 ID/128 项分页、空 ID、取消及单连接释放验证、Go vet 与独立 backup_cursor_audit 均通过且无 blocking。原 187662336 字节用户库未写入，runtime/SSH/provider/source_cutover 均为 0；没有完成真实用户库升级、desktop/runner 切换或恢复验收。临时工具保留至实际升级/切换/恢复完成后再清理，不进入生产启动、构建或运行时读取；共享控制实机及其余既有待办范围不变。

同一 clean626 源的 native 构建已完成：git archive 的 1924 个 raw blob 前后相同，frontend tree `8b8d20fea16f8c3a46c8e987854a3f59e1daa29f` 生成新 dist，Wails CLI/module2.15.0 下 bindings、FE、native 三阶段均 exit0。`%TEMP%/llm-shared-native-626ccdd-a16c58b152bf4d2f8a9499be8ffecf41/llm-test-studio-shared-626ccdd.exe` 为 26679808 字节，SHA `84d0b996574434d7c98374467a17056867e51eb248b2bcef60a8d4fad9541226`；同目录 `artifact-proof.safe.json` SHA `542809357cb0b34f36757746f27723df646a42946bb2d25ad6b70519f8c275d9`，父端已核实际哈希。嵌入版本字符串为 remote-execution-20261016-shared-626ccdd，Windows version resource 未填，不称 file version。产物尚未启动，没有 native 窗口、OS keyring 或用户 DB 运行验收；binding helper 仅使用 TEMP APPDATA，真实 APPDATA 未写。共享控制 E2E r1 预检发现三处 Desktop 分页 32>25，尚未 SSH/Mock/Launch，原产物及证据保留，r2 工具正在修正并补真实 getter 回归，尚无新 live 通过结论。

## 2026-10-16：共享 r2 观察失败、原 Job 只读核验及历史副本启动验证

r2/source519 实机 session4278 exit1，证据目录 `%TEMP%/llm-shared-foreign-acceptance-1e9984b8-833d-4673-8161-013d4a2d7dd9/r2/run-xv3awutc/`；acceptance.safe.json 为 7013 字节，SHA `2d4e617cbb59fc285fc7c717b57cae7542c5e89e83dba977045b04ad88a28c60`，events.safe.jsonl SHA `eafea53e42641ce60f4f46b8d8f33f4285c18fcb4907e104e78150cf897e565b`。A 唯一 Launch1 接收 Job `d1cce52e-c390-4c1c-98e8-587a695cceee`、report `8d438085-461b-4677-bf5f-c62b282c2820`，Spec SHA `aaa3308b2b9f962cb53cd47532fecd0eaf975667657b8094980f68ee78601e8d`，accepted_at2026-10-16T09:21:51.043032742Z，观察时 running/seq7/rev3。B baseline_ready 包含 11 个旧 mirror、17 docs，未观察到新 Job，未进入 foreign_visible/cancel/control/C 查询，commandID 为空；原 false 与原 ID 保留，不重投。约 25 秒超时只是时序推断，generic wrapper 没保存精确错误，不能唯一归因。

离线 B clone 的实际 Desktop GetJobs 返回 11 项约 5.183118s，profile 主要为 JSON 协议校验，只证明成本，未证明失败唯一原因。后续父端纯 GET 原 Job 的 session75284 exit0，`%TEMP%/llm-shared-foreign-acceptance-1e9984b8-833d-4673-8161-013d4a2d7dd9/postfailure-d1cce52e/readonly-vr964it5/observation.safe.json` 为 5963 字节，SHA `d80b0a761c5676a6834269e3e9c065acfe6e6ee9914111565d36db6354b46a88`。固定原 Job/report/Spec/receipt 与 R7 actual probe 一致，原任务 completed/rev4/seq13，封口 PerformanceResult 的摘要/canonical 下载在内存核验通过。Submit/Command/provider 均为 0，仅是新诊断本地计数，不能代替旧 Mock 终端请求计数；原 B 库未启动 watcher、未导入或控制，live_controls_queried=false。diagnostic_complete 不改 r2 false；该次 API audit 本身不含 service after PID/hash 证据。

随后独立只读 service 观察 session89225 exit0，固定 systemctl show 与 /proc 元数据确认 PID244293/starttick24859722/NRestarts0/execstart248597223642/UID998/exe SHA1b94…d7c7 与 before 一致，服务 active/running，没有 unit 操作。`postfailure-d1cce52e/parent-service-readonly-vahoxow1/service.safe.json` SHA `d395a4c9df613cde95e96676aab5555aa5ccc4f90e0b644b7bac749b0cc349fc`；同一 postfailure 根的 `parent-postfailure-verification.safe.json` SHA `dbbc441363cfd7b03caa5a86a42dd46363f15e4d6c6fd63a4c0cf69b7faa3243` 再核原 summary/events/A 与 B 原 DB 四份 SHA 不变。它是后续独立时点的读证，原 r2 mock_process_after:null 不回写，原 false 和未执行的共享控制仍保留。

历史转换副本以 clean626 的真实 DesktopApp production initializer/startup/getters/shutdown/reopen 完成两轮，主验收 92.283s；61 Run 报告、43 性能报告、4 comparison details 均按原 ID 可读，104 份 archive body 与母本 typed JSON 等值。公开列表 cap100 正常，最旧 4 份仍可 detail 读取；补充 GetComparisons 核对 unique4 comparisons/11 channel sets 通过。所有原历史行不变，仅应用副本新增 local identity1；不宣称每个派生 Detail 字段均独立与母本逐字段比较。`%TEMP%/llm-shared-jobs-offline-conversion-20261016/desktop-initializer-acceptance-clean626/desktop-history-final.safe.json` 为 8057 字节，SHA `9fe08ab83d1ee7263250a7a2a55b57c71624825871c5b2732c49954d82a49739`；597 个输入前后不变，独立 review 无 blocking，MemoryStore、0 HTTP/SSH/provider，native/OS keyring=false。原 CBC 用户源库与 42ed 转换母库不变，真实 cutover/recovery 未执行，工具继续保留。

quota8f7c828/contentff657e7 已本地提交，Linux WSL integrated runnerstate 与 runnerprocess 完整测试、vet 均 exit0；`%TEMP%/llm-content-integration-6573c837b1554a7aaa6365da9dc4a191/verification.safe.json` SHA `babacacf57899520b2764d8c655991201779517cb07f8935b9d125c10369da45`。仅 ErrCapacity 软拒新提交，实际 DB fault 仍隔离；content 为内部流式原子存储基础，HTTP/Job assets/附件/release 尚无，assets 能力仍 false。两项未部署到 R7、没有新增 schema；本轮 scheduler 实施刚开始，未宣称实现或通过，完整共享控制/公平性、大任务、文件资源、native、真实历史库切换、重启/故障还原、第二节点/arm64、CI 发布及原 K3 失败范围均保留。

## 2026-10-16：a91f1d8 同步 session 与 priority SQL 已提交，本地集成通过

源码由 secondary `2f3afdc649c3b956567aff0e45c97d4a50b9476c` 整合为 main `a91f1d8e20cfa2c97a2b55536dd57a0518135e18`，Go/application/module Git blobs 完全相同，仅四份旧 docs 不同。父端 session15905 的全仓 `go test ./... -count=1 -timeout=15m` 和 `go vet ./...` exit0，602 个 Go/module 输入前后不变；desktop79.133s、runnerhttp68.908s、SQLite193.715s、remotesync0.921s。`%TEMP%/llm-shared-sync-integration-90caf5e283fc1/verification.safe.json` SHA `389eb2ce320e9d314b2154088841362b3e8546391607f28e403985dfadd91e3b`，同目录 commit-binding.safe.json SHA `ad9fc2ce1dfca62cf7d746fded9e811c67c4eae10bdced289e2a3e76d95df68a` 将测试输入绑定到 main 提交；前者保留测试时尚未提交的原记录。没有 FE/DTO/Wails 变化或 FE/native 重跑，Windows CGO0 不是 race 验证。

当前每节点同一两分钟 session/五秒 tick，metadata 与 job 独立推进，不等整批完成；节点 inflight≤4（含 gate 等待）、tick 新启动≤4、priority page/inflight≤2、history inflight≤1、tail/history pending 各≤25，全局完整 GET/needsSync/Pass gate4 和 priority SQL gate/五秒期限保持。tail admission cursor 单调、末页不归零，冷 tail 首次 terminal 仍导入；history 独立回绕且每轮固定 tail 高水位，ListJobs 页边界最多一页 25 项有界越界。正常期限轮换立即重连保留 scan，不报 remote error；取消 join 全部 metadata/job 再 close 一次，配置 generation 变化则 join 后 reset，旧结果不污染新 session。AppliedAt 仅真实 callback 更新，session 首个真实错误不被 metadata 成功抹掉；四槽实际忙时不承诺绝对 25 秒。

六个 watcher 文件经独立 review、Windows 验证、真实 WSL Linux sync10轮及 HTTP 全包通过，最终 style 后源码重新编译/vet/定向运行通过。`%TEMP%/llm-shared-sync-6dd15d1bb73c4cf0b470e49a24f86fce/verification.safe.json` SHA `004b4f783892da126e9e33eb9b7f2de0f3b6ec6b1e568ef5cbc729b1ff378ee4` 分别记录 WSL 较早全包与最终 style 后定向运行，最终整合 Windows 全仓结果见上段。新增测试覆盖 session/游标/取消 join/AppliedAt 与真实错误保留，旧 late report、partial document 及三类共享导入验证仍保留。

唯一 `ListRemoteSyncPriorityIDs` 替换旧方法且无 alias，候选为 own pending/accepted 无 mirror 或 own/foreign 非 terminal、known refs 未 imported；own prepared 排除，终态全部 known refs imported 后退出但不称 settled，late report 由 history 找回。单 statement 严格核顶层 UUID 顺序、当前 Snapshot/Receipt、小身份与双源冲突，Go 至 limit+1 eligible 停止；完整 spec 不传 Go，实际同步完整校验不弱化。SQL 可先 scan/sort 节点 scope 的小 header/snapshot，工作不是 O(limit)，也不宣称全仓 constant memory。SQL 定向/desktop/vet/review 与 WAL 双仓库 statement snapshot 一致性回归通过；128 terminal 后 1 active 的样本约 22.3073ms，真实 B 私有 clone 的 11 terminal/17 imported 返回零 priority 为 69.3992ms，母 snapshot/clone 前后 SHA 均 `7355b479d7c3d87807ffcfdde3e946e50f7dab012380f3f3c18317a43e703ef6`。`%TEMP%/llm-shared-sync-priority-9c2c82bed6f1462d8436f481e6f820a0/verification.safe.json` SHA `95b3adaec4a56f3bfe47c80123f3af7ba225ac964d2e30497e01a316c5f6352a`；仅为该样本，不承诺全历史时延，原 B watcher 未启动，不是 B 实机恢复。

本阶段 schema/wire 不变，无新增转换需求，既有 adc 用户库切换/恢复仍未执行；原 R7 未部署本阶段构建。r3 工具尚属静态草案，未 final compile/SSH/Launch/control，原 r2 false/ID 及 K3 旧结果保持。P1 文件上传在不同工作树实施，尚无完成或 assets 能力证据；完整公平性/大任务、共享控制实机、文件资源、native、历史库真实切换、重启/故障还原、第二节点/arm64、CI 发布范围均未缩减。

## 2026-10-16：r3 实机冷导入及新 foreign 发现成立，整轮仍失败

父端以 exact r3 binary `16ef99e35d853598ee2c785af76e7d34a1162cd5891e408af02d903d30d53bbd`、coordinator01565047（5 pins 预检/selftest0）执行 session27088，exit1。原目录 `%TEMP%/llm-shared-foreign-acceptance-1e9984b8-833d-4673-8161-013d4a2d7dd9/r3/run-kvlj2zam/` 的 acceptance.safe.json 为16263字节、SHA `c70676c803e9b709083b4d232d780855d118401c0c3b3e382c525f91d749af10`，phases.safe.jsonl SHA `207b78b1196b7d958ab26504ad2f6efc18ec42a01fe4f6c7c5977f28c0880100`。原 passed=false/wrapper_operation_failed 保留，不重投原ID或补发控制。

B 的空 workspace 经产品 watcher 导入初始12Job及全部 pending 文档，public/typed/SQL foreign 均无 own submission；冷准备总239654ms，其中 cold_import phase214840733us。A/B 为独立 OS 进程 PID49384/25004、不同配置，R8 client labels 对应的实际 daemon 仍是 R7。唯一 Launch1/Submit1/paid0 接收 Job `50620b5e-ec5b-4ccb-b7f1-3f456a2844bc`、report `d43a41cf-30a4-4df4-a080-8521f727cf7c`、Spec SHA `d5b540c66b9924fcf3eb02d61f9ae5563d3f7dc38fed3cce09346407c0114930`，accepted_at2026-10-16T10:37:59.173390439Z。Mock first status 已实际入站 active1/count1/max1，gate_released=false、requests_expired=false、settle0/rejected0；mock-request-started.safe.json SHA `1590beeda31cc2bb511ea1e5481dbe8c8730784d614671eca768d7011e15c1b5`。B foreign_visible phase4162382us、父整体观察4187ms，running/rev3/seq7、submission:null/publicforeign，真实 GetJobControls 为空；这是本轮共享发现证据，不是通用25秒 SLA，也不与旧样本计算提升倍数。

随后 before-cancel 文件尚不存在即 wrapper 失败，commandID 空、cancel/control0，无 queryC，terminal/sealed/reopen 均未验证。清理前 A/B poll 均 null，父记录 terminate_requested，实际两进程 exit1、readerEOFtrue；cleanup_completed=false、mock_close_completed=false，原 mock_process_after:null。before-cleanup.safe.json SHA `d0518a2f374381f0d4fbc454180d24603ac33a1555ed57d7d20b360adae89db0`，after-cleanup.safe.json SHA `f794ba1df87c8ef91387468c1bb3af542a1ddff8daf7e3c92efd38b05a410165`。仅有 before 的原 R7 PID244293/starttick24859722/NRestarts0，不能称优雅退出或 runner after 状态保持已证。

离线工具另复现一条相符缺陷：status cap2 的 channel.settimeout 影响同 channel 后台 reader，闲置2秒触发 socket.timeout/reader_failed；第二次 status 抛另一模块 AcceptanceError 后被 generic wrapper 接住，死 reader 又不能消费 cleanup ACK。原 raw error 未保存，不能追认为 r3 唯一根因，也不修改产品来适配坏验收。r4 仅 TEMP 准备中、未实跑；P1 仍在 secondary 未整合/部署，assetsfalse，Linux 一条旧 provision 测试仍在核 baseline，不宣称本轮全 Linux 通过。共享控制、终态/重开、完整公平性/大任务、文件资源、native/OS keyring、真实历史库切换/恢复、故障重启、第二节点/arm64、CI 发布及全部旧失败边界继续保留。

## 2026-10-16：P1 内容上传已提交，Job assets 仍关闭

P1 的28文件最终raw aggregate为 `e30981adca0fd1dcea8f8b1ea9200ff78089ff00e61e251b591959a007fb9b9f`，secondary `1204270b8be90994c5cdfdb9cdb45051893e40c5` 已整合 main `45127094355757cc8ffb4d6c170271ff8ea05088`。`%TEMP%/llm-content-http-p1-c962-20261016/verification-final.safe.json` 为6060字节、SHA `95f46c6f237a611204f0f682888c85cca8b1a589bef61412dba9e7160cfc534a`，source-final.safe.json 为4422字节、SHA `9aa74f32aaa145fd3dca98084e17fa9ddbf8df27d524c93b0dde74f90cd200c1`；这两份保留freeze时未提交的原状态。handoff.md SHA `d0d44ef369238c9faa95fc21623669a1edf7d75037013a258f3181d2d26080f3`、p1-boundary-matrix.safe.json SHA `bcd77697dff1ac9725de8d62fec4671251588289ecef2ef889b70b7974bc6344` 已亲读，矩阵U01–U13只覆盖有界P1，不表示P2–P5完成。

当前PUT /assets/{lowerSHA64}在Bearer/当前契约/规范路径与identity octetstream、已知CL1..1 GiB校验后接收原字节，完整长度/SHA/持久发布校验后返回严格typed receipt201或200；错误输入与真实存储故障分开。唯一jobcontract.ContentIdentity替换原runnerstate类型，无alias；content_upload是既有Features中的可选feature，无新必填RunnerInfo、同build要求或schema/DTO/FE/Wails变化。assets仍false、非空Job assets仍422/0Execute；HTTPassetGET、Job binding/refDB、附件、manifest、release及P2–P5尚无，不触发历史数据升级或原R7部署。

20 GiB/minfree512 MiB共享reservation、4upload槽、16384 retained完整文件/partial上限保守计费，无GC/delete；upload10分钟/bulk header2分钟，普通JSON30秒及原pool25秒/2分钟未扩大，SSH最多4 endpoint owner×两固定management/bulk role≤8transports。serviceMu令upload与maintenance原子互斥：活跃上传pause409 upload_in_progress/retryable/noApply，暂停先赢新upload423；installer严格当前SafeError仅busy码映射ErrBusy，无旧409 fallback。取消/关闭join并释放源、连续100次空读上限、receipt身份/坏2xx、丢确认显式完整重传及并发预算等回归与独立review通过；最后closed-owner测试先RED Read0/Close0，修复后Read0/Close1且WG/request集合为空。

Windows全仓test/vet在初冻结ba0789通过（177698ms）；最后仅SSH http.go/http_test.go的closed-owner body-close补丁，完整client/SSH/vet通过，其余26路径不变。Linux七个affected包初次SSH nonroot失败、其余通过；exact c962旧archive在UID1000同样失败，command_count2/cleanup_pendingtrue，fake probe UID0与实际fixture UID1000不符。最终同版SSH在root UID0完整包通过，最终Linux vet0；未放宽fixture或生产权限，旧nonroot失败证据保留，不宣称任意Linux环境全通过。后续父端主a91+P1全仓Go/vet session86996已真实exit0，整合验证通过。

父级对main4512709执行 `go test ./... -count=1 -timeout=15m` 与 `go vet ./...` 均exit0，总177.900s，Go1.26.6 windows/amd64、CGO0；616个Go/module输入前后逐一SHA未变，inputs-before/after清单均为 `b551c70cabc2723eac1d8bb1c375804808de63212f8f2b6bd67d545360ca0967`。desktop60.900s、runnerhttp45.511s、jobhost28.250s、remotesync1.010s、SQLite168.229s、SSH3.055s。`%TEMP%/llm-content-shared-integration-4512709-c0324b8e2be54b2098a3a8e622eac7d8/verification.safe.json` SHA `99dcad229c5418bd736e6e76fc5746c1ae84ab8960e79198933fb6be01a5d686`，go-test.log SHA `1c9c8394d04a032f3cc3ef8bce6975f824ec9809a623ac4e4a1f4db5d37ec920`，vet日志为空。精确P1的28paths及modules Git blobs在secondary1204270与main4512709完全一致；整合源码包含预期a91 watcher差异，不能按整个目录等同P1清单。该PASS仍是Windows整合检查，不改变r4未实跑、native/外部SSH/provider/部署未验收边界。

真实独立Linux进程PID722/exe SHA `69ed288de7f2242834411fc6a4f7d4f804191e7346f4d6ebb8aebcb6a7341c67` 经TCP上传131072字节，31.041710264s越过旧30秒，同时普通JSON正常；重复/重启、坏body保持健康、disk fault隔离、阻塞upload SIGTERM join及维护互斥通过。没有实际1 GiB传输、31秒跨SSH全链、race（CGO不可用）、外部SSH/provider、nativeUI、真实用户DB或部署验收；既有opt-in31秒report/SSH测试未启用。r4工具将基于451 exact archive覆盖新SSH ownership，尚未实跑；原r3 false和全部既有未完成范围保留。
