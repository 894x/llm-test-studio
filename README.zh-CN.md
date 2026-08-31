# LLM Studio

[English](README.md)

LLM Studio 是一个用于测试 OpenAI 兼容 LLM 网关的本地优先桌面工作台和 CLI。产品由统一的 Go Application Core 与 Wails + React 桌面界面实现，不再需要 Python 或 Streamlit 运行时。

## 功能

- 管理逻辑模型、渠道、Base URL、API Key 和各渠道的上游模型映射。Key 只保存在操作系统凭据存储中，不会返回 React、写入 SQLite、报告或日志。
- 将内置和用户新增 Case 保存为可分享的 `cases/<group>/<case>/case.json` 文件。用户 Case 在桌面可执行文件旁创建；加载时按 group 合并两类来源，相同身份的用户 Case 覆盖内置 Case。
- 创建可复用的 Suite 与 Plan，固定 Case revision，并配置固定并发或开放环负载、请求超时和 SLA 阈值。
- 使用 Go 执行引擎运行 OpenAI Chat、Kimi K3 和 Seedance 兼容性 Case。
- 对同一个逻辑模型选择两个或更多渠道进行对比。每个渠道都会生成独立、不可变的运行快照与报告。
- 查看运行历史、请求级结果、延迟与 Token 指标、SLA 结论和执行环境快照。
- 将同一份封存报告导出为 JSON、独立 HTML、PNG 或 PDF；桌面报告页也可以复制 PNG。
- 使用 Go CLI 执行兼容性审计、通用压测和诊断。

## 架构

```text
Wails + React 桌面端 ─┐
                       ├─ Go Application Core ─┬─ 执行引擎
llm-studio CLI ────────┘                       ├─ SQLite repository
                                               ├─ 操作系统凭据存储
                                               ├─ 文件 Case 目录
                                               └─ 报告渲染器
```

主要目录：

- `apps/desktop` — Wails binding 与 React/Tailwind 桌面界面。
- `cmd/llm-studio` — 具有版本协议的 CLI 命令与 JSON/JSONL 输出。
- `internal/application` — 目录、运行、对比、报告和工作区编排。
- `internal/execution` 与 `engine` — 压测和兼容性执行引擎。
- `internal/persistence/sqlite` — migration 与 repository。
- `cases` — 按协议分组的内置可分享 Case。
- `definitions` — 不含秘密的模型定义 fixture。

## 桌面端开发

要求：Go 1.25+、Node.js、pnpm 和 Wails v2。

```bash
cd apps/desktop/frontend
pnpm install --frozen-lockfile
pnpm test
pnpm build

cd ..
wails dev
```

构建桌面可执行文件：

```bash
cd apps/desktop
wails build
```

首次启动时，结构化应用数据会创建在操作系统的用户配置目录下。用户新增 Case 保存在可执行文件旁的 `cases` 目录中，因此可以直接复制和分享整个目录。

## CLI

构建或运行 CLI：

```bash
go build -o llm-studio ./cmd/llm-studio
go run ./cmd/llm-studio --help
```

列出并运行兼容性 Case：

```bash
go run ./cmd/llm-studio audit list --suite kimi-k3 --cases-root cases --format human

API_AUDIT_API_KEY='replace-me' go run ./cmd/llm-studio audit run \
  --suite kimi-k3 \
  --cases-root cases \
  --base-url https://gateway.example/v1 \
  --model kimi-k3 \
  --case F004 \
  --format human
```

运行压测。为方便脚本调用，仍支持 `LOADTEST_API_KEY`、`LOADTEST_URL` 和 `LOADTEST_MODEL`：

```bash
LOADTEST_API_KEY='replace-me' \
LOADTEST_URL='https://gateway.example/v1/chat/completions' \
LOADTEST_MODEL='kimi-k3' \
go run ./cmd/llm-studio load run \
  --requests 100 \
  --concurrency 10 \
  --stream \
  --input-tokens 100 \
  --max-tokens 10 \
  --output load-result.json
```

使用 `--rate` 和 `--duration` 配置开放环调度。使用 `--request-file cases/<group>/<case>/case.json` 读取 Case 请求体。默认拒绝明文 HTTP；只有显式指定 `--allow-insecure-loopback` 时才允许 localhost 测试服务器。

## 验证

```bash
go test ./... -count=1
go vet ./...

cd apps/desktop/frontend
pnpm test
pnpm build
```

旧 Bash benchmark 继续作为独立的负载引擎验收 fixture，其 Mock LLM 已迁移为 Go 实现：

```bash
bash scripts/test_llm_benchmark.sh
```

## 安全与本地数据

- 不要把 API Key 写入模型定义、Case、示例、报告或会被 shell history 记录的命令参数。
- 创建或更新渠道时，渠道 Key 会写入操作系统凭据存储。更新 Key 会创建新的凭据 revision，使历史固定版本的运行仍保持原绑定。
- 报告和请求结果只包含测量数据与脱敏证据元数据，不包含 Authorization header 或凭据字节。
- SQLite 保存目录对象、固定快照、运行、结果、对比和封存报告；Case 继续使用可移植文件，而不是目录数据库行。
