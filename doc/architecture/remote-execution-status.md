# Remote 当前交付状态

更新：2026-10-17。核心实现、分阶段提交及本地R9试用包已完成，代码在 `codex/remote-execution` 独立 worktree，未推送。用户已明确确认“本轮一并暂缓，先交付”：旧节点404、原生界面补验及额外平台故障演练本轮暂缓，按现有R9包与已验证结果交付。本轮交付完成不等于R1–R16全部通过；下列失败、未验收边界和恢复材料继续保留。文件、媒体及native_case扩展继续暂缓。

已实现多节点配置、默认本地或远程选择、自动安装和 systemd 保活、共享节点授权、后台执行和报告恢复。不同客户端构建可共用同一 runner；按唯一当前合同与所需能力校验，连接不会覆盖 runner，安装和升级互斥，报告记录实际执行来源。

| 验证范围 | 结果 |
| --- | --- |
| 两台实际 amd64 节点 | 安装或复用、默认位置切换、共享任务执行通过；新节点 SSH 吞吐实测约 8–11 MiB/s。 |
| ARM64 | 完整 ARM64 系统模拟环境中的安装、启动与任务执行通过；当前 CLI 的忙拒绝、卸载、保留及幂等也已实跑通过。不属于物理 ARM64 机器验收。 |
| 保活与故障 | 启动失败限速及恢复通过；在途强杀后原任务封存为 `runner_interrupted`，请求数保持 1→1；自建 WSL 节点的真实 watchdog 超时及自动恢复已由只读补证确认。 |
| 全入口 | 两 Suite 的 Plan、Comparison、预热＋爬坡、预热＋容量阶梯在客户端实际退出后继续；报告由独立进程和重开客户端读回。 |
| 共享与重连 | 两个实际客户端构建双向提交通过；离线原命令持久化，恢复后由原 worker 投递一次并确认。 |
| 历史与凭据 | 原 13 个任务已完整续传；原生窗口显示 63 条历史运行；真实 Windows OSStore 跨进程保存、读取及测试凭据清理通过。 |
| 数据恢复 | 固定源独立转换、实际 APPDATA 路径临时切换、当前 Core 读取及原文件精确恢复通过。 |
| 历史积压调度 | 350ddc8分离状态同步与报告下载；真实HTTP/SQLite/cache中旧报告held时，own完整报告与未知foreign跨32历史的running/child/terminal事实均通过。五包回归与vet通过；本期实SSH完整积压公平性仍待。 |

## 用户指定渠道最终实测

实际使用 `122.51.2.21` 的 R8 runner、指定渠道和 `kimi-k3`，完整运行原有 Case，兼容性并发 4；最小性能仍为并发 1、3 请求。三任务均为 `completed`，只有 3 次 Launch/Submit，无自动重投；报告已下载、离线重开核对，客户端实际退出 0。

| 项目 | 最终结果 |
| --- | --- |
| Chat | 84 项：65 通过、19 失败、0 未判定。 |
| Responses | 230 项：103 通过、127 失败、0 未判定。 |
| 最小性能 | 3/3 成功；平均 TTFT 1555.51 ms，平均端到端 2294.23 ms，记录的 generation TPS 为 49.97。 |

远程运行链路通过，兼容性整体未通过；146 项真实断言失败全部保留，未修改预期。性能数值只属于本次三请求最小测试。报告：Chat `e0c92267-cb95-4a00-90d5-439869a9cf05`，Responses `e8eee1db-a67f-4352-8c16-de7ec6a20cb4`，性能 `df3ec54e-aeba-49b7-8455-35d0aadbb4d4`。最终安全汇总位于 `E:/TEMP/llm-final-r16-k3-c4-bae3eb2590a64e69ba9860f2bc5238fb/run-current-r8/acceptance-final.safe.json`，SHA-256 `56fa564c9bdb6e9d0f52f3846d41ba0fa28b4bb983346beb2e8b5523e0a1c97e`。

## 保留的验收边界

- R4整体三方对照仍未通过；用户已明确暂缓旧节点404，停止排查，不执行准备的R9服务器升级。原任务、失败回执和清理证据全部保留。本地与新节点的业务、归档及实际出口已证；旧节点任务实际WAL为completed/12事件/归属一致，但管理GetJob/Events仍404。ListJobs200之后重读也仍404，并非预算耗尽。未改库、checkpoint、重启或升级，详见[验证记录](remote-execution-validation.md)。
- 原SQLite驱动取消后的native Rows泄漏已独立复现并升级到首次修复版1.40.1；真实读视图回归由8/0 WAL checkpoint转为8/8，54包Go、全仓vet及Linux定向通过，修复包含在下述R9本地包。旧驱动的新pooled连接仍可读新数据，不能据此宣布R7稳定404已定位或修复；两台服务器均未因此自动升级。
- 阶段 C 的管理员 CLI `uninstall` / `recover-uninstall` 已实现并通过 Win/Linux 合同、故障及 SQLite 测试和独立审查。自建 WSL amd64 与新建 ARM64 完整 VM 的实际 busy 拒绝、settled 后卸载、数据保留及幂等均通过；ARM64 单 Provision1/Mock1、实际 exit0/EOF，VM 已安全停止并保留证据。卸载保留业务数据、身份、配对、内容、备份及服务账户，只删除明确归属的受管程序和 unit；桌面移除配置不代替该操作。
- R11 本轮选定的未知节点、字段类型、任务能力、观察生命周期及文档边界已补齐 T0 验证；文件声明随既定文件范围暂缓。最大文档的有效实体、预留预算的损坏拒绝和 HTTP 编码层证据分别记录，平台与运维实跑仍保留在矩阵 G07，详见[契约矩阵](remote-execution-contract-matrix.md)。
- 已配置节点的原生点击切换、三种窗口尺寸、主题和键盘交互补验，用户已明确同意本轮暂缓；已有组件和空节点面板验证不代替这些交互，新R9包没有启动GUI。
- G07额外平台与故障演练也经用户明确同意本轮暂缓，包括完整实SSH积压公平性、首次安装SSH中断、amd64整机重启及受权限限制的Windows链接场景。保留未验收或SKIP标记，已有WSL/ARM64模拟环境证据不提升为物理机器或全部平台通过。
- 云端 CI 和发布工作流未运行，未推送或发布。
- 用户实际 APPDATA 最终保持原格式数据库（SHA 以 `cbc5337e` 开头）；转换后的当前格式副本及恢复材料均保留。正常运行仍明确拒绝旧格式，启用新版本时需显式切换，尚未执行永久切换。本次没有运行时迁移或历史格式 fallback。

## 本地测试产物

最新代码 `22bf930` 的R9 Windows正式构建已通过，包含同步分离及SQLite取消泄漏修复，Wails CLI/模块固定2.15.0。EXE：`E:/TEMP/llm-native-final-22bf930-a625e5b18e8c4812be42be62bd8920ba/aligned-r9/llm-test-studio.exe`，SHA-256 `51c2cf0ac9ac09956e28b01da65b5a852ab232629862348b26c4a111c5a3a5cf`。54包Go和全仓vet通过，正式构建15.764秒；新包没有启动GUI，此前63条历史的原生观察仍对应旧b03e350包。

同目录ZIP：`llm-test-studio-22bf930-r9-windows-amd64.zip`，SHA-256 `9077d6e98545b84d7ad10122a778c039824b2cfbd74dea8195a706e252d97916`；唯一entry流式SHA与新EXE相同。EXE-only ZIP不包含数据库或凭据，也未推送或发布；此前全部包保留。

已验证的隔离历史 profile 仍保留在旧目录 `E:/TEMP/llm-native-b03e350-cdf42c78e4af456ea8157b98be64080a/` 的 `appdata` 与 `localappdata`。直接使用默认用户目录启动仍会遇到已明确列出的旧格式限制，测试包不代表用户库已永久升级。

新包目录另备 `Launch-verified-test-profile.cmd`，仅为新EXE子进程设置上述两个隔离路径，便于本机打开已验证历史副本；没有复制profile或凭据。桌面AppVersion、配套runner版本及manifest Release均为 `remote-execution-20261017-r9`，其release资源标签一致仅为离线试用资源配置，不改变共享节点允许不同client/runner构建的规则。脚本只准备、未执行，不能当作新包原生验收。

已通过当前Cache.Open在上述隔离profile的公有runner缓存中预热R9两架构资源。随后以禁止Fetch的Source重新打开两架构均成功，Fetch0；因此该本机试用配置无需从尚未发布的GitHub release下载配套runner。仅写公有ELF/manifest及当前API必要锁文件，未读取或修改数据库、凭据、其他profile内容或真实APPDATA。此预热不等同GUI启动或服务器安装；安全回执见[验证记录](remote-execution-validation.md)。

完整证据、转换工具保留位置和早期失败记录见[验证记录](remote-execution-validation.md)，原验收矩阵见[实施计划](remote-execution-plan.md)。
