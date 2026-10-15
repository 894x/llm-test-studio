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

## 待验收

独立 runner 管理接口、SSH 自动安装、systemd 保活、全部桌面入口与默认远程节点、桌面退出后任务持续、结果补齐、历史数据升级，以及第二台节点、arm64 和多客户端共享的验证仍按实施方案 R1–R16 执行。用户追加要求：最终使用先前提供的真实服务器和渠道，执行 kimi-k3 兼容性 e2e 与最小性能测试；这项付费渠道验收单独记录实际用量，不复用前置 Python 冒烟作为完成证据。
