# ADR-0001：桌面壳与共享 Application Core

- 状态：Accepted
- 日期：2026-08-30
- 决策范围：V1 本地跨平台版本

## 背景

当前产品由 Streamlit 页面、Python 压测实现、Bash benchmark 和独立 Go 兼容引擎组成。它们已经可以完成测试，但 UI 直接编排执行器、SQLite 和 Go 子进程，负载测试又存在多套成功判定与指标口径。继续在现有页面上叠加功能，会长期形成两套业务规则。

本次工作是基于目标架构重构现有代码，不是在旧系统旁新建一套产品。迁移期间允许兼容入口共存，但每个阶段都必须减少重复业务逻辑，并为旧入口定义退出条件。

## 决策

1. 仓库使用单一根 Go module。领域、应用、执行、持久化、凭据和报告能力进入共享 Go Core；现有 `engine/` 代码逐步被吸收，而不是长期作为独立 module。
2. 桌面端采用 React、TypeScript、Vite 与 Wails。Wails v2 稳定版是首个生产 PoC 基线；Wails v3 在正式稳定、工具链和三平台验证通过后重新评估。
3. V1 在架构上分离 UI 与 Core，但部署为本地单体。React 通过有类型的 Wails binding 调用 Application Service，不启动 localhost HTTP 服务。
4. `llm-test` CLI 与桌面 adapter 调用同一 Application Core。Core 不依赖 Wails、终端、Streamlit 或具体数据库驱动。
5. Python、Streamlit 和 Bash 仅在迁移期分别作为兼容 adapter、交互原型和行为基准。V1 不保留 Python 业务运行时；对应能力迁入 Go Core 且通过等价测试后，直接删除重复业务实现。
6. 长任务统一使用稳定 Run ID、版本化事件和 `context.Context` 取消。GUI 和 CLI 不直接访问 SQLite、系统凭据存储或执行器。

## 目标边界

```text
apps/desktop (Wails + React) ─┐
                             ├─ internal/application
cmd/llm-test ────────────────┘          │
                 ┌──────────────────────┼──────────────────────┐
                 ▼                      ▼                      ▼
       internal/execution     internal/persistence   internal/reporting
                                      │
                             internal/credentials
```

`engine/cmd/llm-compat-engine` 在迁移期保留为 JSONL 兼容 adapter。它不得继续拥有计划编排、状态机、判定或报告业务规则。

## 备选方案

### 原生 Go UI（Fyne/Gio）

不采用。测试工作台需要密集表格、筛选、虚拟滚动、图表、证据折叠和 HTML 报告预览。原生 widget 与 HTML 报告会形成两套展示体系，增加视觉和交互重复实现。

### Tauri + Go sidecar

保留为备选。它会引入 Rust 壳、逐 target sidecar、权限配置和额外签名/生命周期管理；在 Go 已是核心语言时收益不足。

### Electron + Go sidecar

仅在 Wails 无法稳定完成同源 HTML 到 PNG/PDF 的三平台渲染时重新评估。Electron 的内置 Chromium 有利于一致渲染，但进程、包体和内存成本更高。

### 本地 HTTP 前后端

V1 不采用。它会提前引入端口、鉴权、生命周期和本地攻击面，却不能增加离线桌面能力。V2 服务端通过独立 adapter 暴露相同契约。

## PoC 退出门槛

只有全部满足时，Wails 才能进入产品迁移：

- Windows、macOS、Linux 均能构建、安装并运行；
- GUI 与 CLI 调用同一服务并读取同一 SQLite；
- 系统凭据写入、替换、删除和测试均不回显明文；
- 运行事件、停止发送、drain 和取消在长任务中可靠；
- 1000 条以上结果可以筛选、搜索和虚拟滚动；
- 同一 Report Model 生成 JSON、独立 HTML、完整 PNG 和无空白页 PDF；
- 打包、签名、升级和失败回滚路径有自动化验证入口。

## 参考

- Wails v2：https://wails.io/docs/introduction/
- Wails v3 状态：https://v3.wails.io/faq/
- Tauri sidecar：https://v2.tauri.app/develop/sidecar/
