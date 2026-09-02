# LLM Test Studio 前端

[English](README.md)

本目录是本地 LLM Test Studio 桌面应用的 React 展示层，负责呈现紧凑的运行工作区、收集用户意图，并调用 Wails 生成的绑定。领域规则、持久化、凭证、执行过程和权威运行状态均由 Go Core 负责。

## 本地开发

安装锁定版本的依赖并启动 Vite：

```bash
pnpm install --frozen-lockfile
pnpm dev
```

当 Wails 运行时不存在时，Vite 开发构建可以加载本地夹具。夹具仅用于开发，不会进入生产构建产物。

## 验证

提交变更前运行前端检查：

```bash
pnpm test
pnpm lint
pnpm build
```

## 生产边界

生产构建要求 Wails 壳提供 `GetWorkspace`、`StartRun`、`StopSending` 和 `CancelRun`。绑定缺失或响应无效时会明确报错；界面不会用夹具替代真实数据，也不会伪造成功状态。
