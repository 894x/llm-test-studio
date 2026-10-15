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

nodes.json 的当前 Installation 增加必填 temporary_directory 与 cleanup_pending；所有当前生产者、校验器和测试同步更新，缺字段旧格式仍明确拒绝且不修改数据，不新增历史读取路径。此前未创建实际用户 nodes.json，本阶段只使用临时配置，没有历史数据转换，没有改变 SQLite 或报告格式。桌面 bootstrap 的持久目录、OS Store 装配、UI、任务入口和结果同步仍待实现；进程强杀期间尚未回传的上传暂存目录追踪和显式升级继续后续验证，不能把本阶段视为最终远程 K3 e2e。

## 2026-10-16：桌面节点服务生产装配

生产 initializer 已装配 Manager、`remote-nodes/nodes.json`、按该根目录计算的独立 scoped Store，以及 `runner-releases` 校验缓存。发布默认为桌面 appVersion 对应的明确 GitHub release，测试和开发装配可显式注入来源与发布；创建服务不会下载产物。启动先验证当前节点配置，再清理孤立凭据和启动监测；未知格式在任何凭据清理前拒绝且保留原字节。退出关闭并等待所有检查与监测循环，Closer 可重复调用；关闭先于 Run goroutine 调度时返回正常取消，重复启动监测仍拒绝。

新增私有目录准备，避免在凭据 intent 写入前隐式建立未确认目录链。新目录先在父目录内暂存，再按平台发布；Unix 确认目标及全部祖先目录，即使前次失败留下可见目录，重试也不跳过祖先同步。Windows 使用现有 MoveFileEx WRITE_THROUGH 发布原语，不声称支持目录 fsync。已有祖先权限不修改，目标目录保持私有；文件与最终 symlink 拒绝，已有数据保留。

Windows desktop、remotenodes 与 fileconfig 测试及相关 vet 通过，生产重开/立即关闭相关用例重复 10 次通过。目录用例在 Ubuntu 24.04 WSL 通过，包含并发创建、保留已有内容、取消、symlink 拒绝，以及发布父目录后注入同步失败、重试重新确认祖先。Windows symlink 子项因系统权限不可用跳过。生产装配测试使用临时用户根、内存凭据与拒绝连接的 stub，验证保存的远程默认在离线重开后保留、凭据只能通过正确 scope 取得且不进入 nodes.json、没有产物下载。

这一提交没有公开 Remote binding/UI 或改变启动任务契约，也未验证原生 Wails、实际 OS Store 或真实 provider；节点删除使用检查继续缺省拒绝，待任务 Coordinator 装配。SQLite schema 和报告格式不变，实际用户配置与数据库未打开、未转换。当前 Wails 模块和用于版本核验的 CLI 均为 v2.15.0，没有执行绑定生成或更改依赖。

## 2026-10-16：K3 静态测试契约复核

再次读取 [Kimi 官方 OpenAPI](https://platform.kimi.com/docs/openapi.json)，确认 Responses 的 HTTP 400 使用 ErrorResponse，要求 error.message 字符串；type/code 可选且没有枚举。此前全仓失败来自测试仍要求固定 type，与既有 Case 审计后的当前断言矛盾。现仅同步测试：允许省略或其他字符串 type，仍拒绝 HTTP 200、缺失/非字符串 message 和 401/429/500/502；继续比较发往 /v1/responses 的 JSON 内容，未增加固定错误类型断言。没有改变 Case 请求、断言、Suite、稳定身份或历史结果，也不把错误 envelope 当成指定参数被拒绝的独立证明。

Windows `go test ./...` 已完整通过，包含 data/cases、data/suites、桌面、JobHost、管理 HTTP、SQLite 与 SSH。这是静态和 fixture 回归，解决前述已记录的测试基线失败；没有调用付费渠道，最终远程 K3 与最小性能验收仍未执行。

## 待验收

完整管理接口、发布产物分发、全部桌面入口与默认远程节点、桌面退出后任务持续、结果补齐、显式升级与历史数据升级，以及第二台节点、arm64 和多客户端并发竞争的验证仍按实施方案 R1–R16 执行。单个 amd64 节点的 SSH 安装、共享复用与 systemd 自动恢复已按上节验证。用户追加要求：最终使用先前提供的真实服务器和渠道，执行 kimi-k3 兼容性 e2e 与最小性能测试；这项付费渠道验收单独记录实际用量，不复用前置 Python 冒烟作为完成证据。
