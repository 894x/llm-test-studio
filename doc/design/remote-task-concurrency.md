# 远程节点的同时运行任务数

远程节点面板的“同时运行任务数”控制节点共享的 Job 槽位，取值为 1–128。
新安装仍默认 1。所有桌面客户端共享同一个 runner 和这项设置；任务内部的
请求并发继续受各任务配置及节点 `budget.max_concurrency` 的合计预算约束。
增加槽位后，已有 FIFO 队列立即按两个预算调度。减少槽位时，如果新值小于
当前活动任务数则拒绝保存，等待任务结束后可以再改；不会取消任务。

设置由 runner 原子写回现有私有 `runner.json` 的 `budget.max_active_jobs`，
确认持久化后才修改内存策略，无需重启。节点身份、密钥、其他预算与任务数据
保持原值；重启继续使用保存的值。保存回应丢失时桌面只读回当前状态，不自动
重复写入；多个客户端使用此前观测值进行比较更新，过期值返回冲突。

## 接口和兼容

新增节点令牌授权的 `POST /runner/task-concurrency`，沿用当前 contract 头，
请求字段为 `contract`、`node_id`、`node_installation_id`、
`expected_max_active_jobs`、`max_active_jobs`。全部必填，两个数量为 1–128
的整数，未知或重复字段、错误类型、超大请求与不同节点身份均拒绝。
成功返回原有 `RunnerInfo` 结构和实际预算；设置值已等于目标时可安全确认重试。
校验失败不写配置、不调整任务。

runner 仅在装配了持久化控制器时声明 `task_concurrency` 能力。新桌面连接未声明
能力的现有 runner 时，原有查询和任务提交继续可用，修改此设置则提示升级，
不会发送配置写请求。旧桌面可继续查询新版 runner：`RunnerInfo` 字段没有增加，
能力列表原本允许新名称。公开 CLI、退出码、数据库、节点配置格式及现有迁移链
均不变，没有删除或弃用原有契约。此功能不涉及旧公开标签 `v0.0.01` 的数据库
升级支持范围。

部署本功能需要更新桌面端与要修改设置的 runner；旧 runner 不需要为了继续执行
原有任务而升级。升级 runner 应沿用已有升级流程，等待在途任务结束，不直接
重启生产中的任务。降级时保存值仍是旧 runner 已支持的配置字段，不需要配置迁移；
数据格式的降级限制仍遵循兼容政策。

## 本地验证矩阵

范围是本仓库自己的当前 runner 管理接口，不涉及模型提供商，也不发送付费请求。
契约依据为本次实现的 `jobcontract.TaskConcurrencyCommand`、`jobhost.Scheduling`
和 `runnerhttp`；以下场景由本地模拟及 Linux 进程测试验证。

| 契约 | 场景 | 断言来源 |
| --- | --- | --- |
| 数量范围、身份及封闭字段 | 0、1、4、128、129、错误类型、重复/未知字段、尾随 JSON、超大请求、异版本 | jobcontract/task_concurrency_test.go |
| 任务执行与持久化 | 保存失败保留原槽位，增加后排队任务开始，活动任务不取消 | application/jobhost/scheduling_test.go |
| 两个共享预算与多客户端 | 增加槽位仍受请求预算限制，竞争修改仅一方写入，同值重试不重复写 | application/jobhost/scheduling_test.go |
| HTTP 与能力协商 | 未支持的 runner 拒绝修改，新能力及实际预算可读，过期更新和非法范围不覆盖 | adapters/runnerhttp/scheduling_test.go |
| 桌面与节点状态 | 使用绑定身份，回应丢失只读回一次，过期连接不写入，旧会话明确拒绝 | application/remotenodes/scheduling_test.go |
| 用户输入 | 当前值、成功回读、0/129/小数、低于运行数、升级提示及保留草稿 | remote-nodes-menu.test.tsx |
| 重启与数据保全 | 真实 runner 进程修改后退出重开，身份和其他字段保留，源配置变化时拒绝且逐字节保留 | runnerprocess/scheduling_linux_test.go |

实际生产节点应用配置及原生桌面操作需要单独验收，本地测试不代表已经调整了
用户正在使用的 runner。

### 2026-10-24 验证记录

- 新增功能的契约、调度、HTTP/客户端、节点管理及 Wails 绑定检查通过；节点管理
  回归另覆盖并修复了较早的后台健康检查覆盖新设置的问题。
- `jobcontract`、`jobhost`、`remote/client`、`application/remotenodes`、桌面 bridge
  与桌面入口的包测试通过。`jobhost` 超时/关闭用例首轮在较大并行验证负载下失败，
  隔离复测及之后整包重跑均通过。
- 前端定向测试 8 个文件、232 项通过；TypeScript、生产构建及 lint 通过。lint
  保留已有的 `entry-case-table.tsx:57` effect 警告。
- Linux amd64 的真实 runner 进程重启、私有配置变化拒绝，以及 rename 后同步失败
  的重试恢复，共 3 项测试通过；使用 Windows 交叉编译的测试程序在 WSL 实际执行。
- 浏览器测试数据页面验证中文暗色和英文浅色的显示、保存回读、上限错误、关闭后
  焦点返回及重新打开；1280×720 无页面横向溢出，控制台无错误。没有据此宣称原生
  Wails 或生产节点验收通过。
- `runnerhttp` 整包仍有 `TestHTTPRunReportCompleteEntityAtByteLimitAndAbove/33554432`
  失败。相同用例在独立解出的原始提交
  `1ab8743acf98fa380a5893d137fc8273d9ba2ce6` 中同样失败，新增任务配置专项通过；
  不将该 HTTP 整包记为全部通过，也未修改这项报告下载用例。

### 2026-10-24 实际更新与配置回读

- 已使用 Wails CLI v2.15.0 重新生成绑定、构建并启动当前桌面开发程序；通过该
  程序的真实绑定保存设置，再重新检查节点，回读同时运行任务数为 2。
- 存量节点的主线构建升级在备份校验返回 `runner_upgrade_required`，同一操作已
  显式恢复原服务。补齐了已确认、尚未完成备份阶段的桌面恢复入口；恢复仍由 Go
  后端判断，尝试正向 Resume 后继续禁止还原旧数据。
- 此次实际部署使用 `remote-task-concurrency-r8-20261024`：基于既有 R8 源码
  `9a0991b5f00da3ebeeccc985c5923700d6c6ceec` 回移本次任务配置功能，共 15 个源码
  文件变化，数据库实现及模块文件保持该基线，清单明确记录 `source_dirty: true`。
  这不是当前主线完整发布或存量数据库迁移的验收。
- Linux amd64 实际运行文件 SHA-256 为
  `9ad1290df9e4ff1d74bcb315c69e18db5f46c113088d781d035578717d2153d1`；
  升级 journal 已确认 `completed`，节点健康、就绪，原节点与安装身份保持。
  arm64 仅完成构建。大库备份的完整校验耗时较长，客户端观察超时后继续查询同一
  操作，未重复 Start，也未跳过数据校验。
- 服务器私有配置独立回读确认 `max_active_jobs = 2`、`max_concurrency = 2000`，
  其余配置与升级前备份一致。升级前后 48 张表的数量全部相同，包括 84 个任务、
  37 份报告、49 份性能报告、5,095 条用例结果；没有运行付费供应商测试。
- 本轮相关 Go 包与 vet、前端 3 个文件共 212 项回归、lint 和构建通过；R8 回移
  源码的契约、调度与客户端包及 HTTP 专项通过，WSL 真实进程与持久化 3 项通过。
  原有 32 MiB HTTP 报告边界测试失败仍按上一节保留。
