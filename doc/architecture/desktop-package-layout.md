# 桌面端包组织

`apps/desktop/main.go` 保留 Wails 入口、前端资源嵌入和发布脚本使用的
`main.desktopApplicationVersion`。桌面业务继续调用仓库根目录已有的
`internal/application` 服务。

| 路径 | 职责 |
| --- | --- |
| `apps/desktop/internal/bridge` | `DesktopApp`、前端 DTO、错误码、事件和生命周期；按目录、任务、报告、远程节点等功能分文件 |
| `apps/desktop/internal/bootstrap` | 生产依赖装配、凭据作用域、本地执行器、远程连接、共享操作锁和资源清理 |
| `apps/desktop/internal/native` | 文件对话框、导出内容校验、打开目录和桌面存储路径 |
| `apps/desktop/internal/native/clipboard` | 各操作系统的 PNG 剪贴板实现，包括 macOS C 桥接文件 |
| `internal/persistence/workspace` | 文件配置仓储，以及文件配置与 SQLite 运行数据的组合仓储 |
| `apps/desktop/tests/integration` | 跨桥接、生产装配和仓储边界的本地集成测试 |
| `apps/desktop/tests/live` | 显式启用的远程实测及其输入、回执验证；默认不连接远程环境 |

依赖方向：入口连接 `bootstrap`、`bridge` 和 `native`；`bootstrap` 构造应用服务和
仓储，并返回 `bridge.Dependencies`；`bridge` 调用应用服务与原生适配。
仓储不依赖桌面包，`bridge` 不依赖 `bootstrap`。单元测试与实现同包。

## 保持的行为

- 一个 `DesktopApp` 统一处理启动、调用租约、取消、等待进行中的调用及资源关闭。
  宿主生命周期和配置钩子使用包级函数，不增加前端可调用的方法。
- `bootstrap` 创建一个共享 gate，保留跨文件目录与 SQLite 的串行协调边界、
  历史只读视图和原有资源关闭顺序。
- 启动时的存储检查、文件位置、凭据作用域、持久化结构和远程协议均保持不变。
  此次包移动不引入数据迁移，也不改变既有迁移支持范围。
- 前后端同包发布。前端统一网关从 `window.go.bridge.DesktopApp` 读取绑定，
  同步更新测试及 Wails 生成物；功能组件仍使用同一个 desktop client。
- Wails 模块和 CLI 固定为 `v2.15.0`；发布的 `-X main.desktopApplicationVersion=...`
  参数继续有效。

## 验证

在仓库根目录执行：

```text
go test ./...
go vet ./...
```

只验证本次拆分范围时，使用递归路径。仅 `go test ./apps/desktop` 现在只覆盖入口测试：

```text
go test ./apps/desktop/... ./internal/persistence/workspace/...
```

前端在 `apps/desktop/frontend` 执行 `pnpm test`、`pnpm lint`、`pnpm build`。
桌面构建在 `apps/desktop` 先执行 `wails version`，确认 `v2.15.0` 后使用
`wails build -m -nosyncgomod`，由 Wails 生成绑定。不要手工修改 `frontend/wailsjs`。

远程测试仍使用各自已有的环境变量和标准输入凭据约束。测试包变为
`./apps/desktop/tests/live`，例如先用 `go test -c` 编译该包，再按目标测试原有的
显式启用方式运行；普通测试不会据此自动启用远程或付费提供商请求。

构建及本地测试不能代替原生剪贴板、各操作系统窗口行为或真实远程环境验收。
