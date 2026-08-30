# 现有代码收敛实施计划

本计划按依赖顺序重构现有系统。每个阶段必须独立提交、可验证、保留迁移期兼容入口，并减少重复业务规则。

最终形态是完整 Go Core。Python/Streamlit 不是长期 adapter，也不保留为研究工具；它们只在迁移期间提供行为 fixtures、旧数据验收和人工回归，达到退出门槛后随对应业务逻辑一起删除。

## 提交原则

- 一个提交只完成一个可回滚的架构或行为切片；
- 新行为先有失败测试，再实现；
- 旧入口的兼容测试与新入口的契约测试同时通过；
- 不允许新增“临时第二套”核心；确需 adapter 时记录删除门槛；
- 每次提交正文使用分类模板并列出实际测试和文档。

## 阶段与提交切片

### M0：契约和单一 Go 模块

1. `docs(architecture)`：提交 ADR、Application Core 契约和本计划。
2. `refactor(core)`：把 `engine/go.mod` 收敛为仓库根 module，修复 EngineClient 的构建路径、UTF-8 和跨平台测试。
3. `feat(domain)`：提交 UUID/revision 元数据、领域对象、Run 状态机、四层成功和 Report v1 类型。
4. `refactor(application)`：把现有兼容 CLI 的计划、并发、取消、事件和报告编排迁入 Application Core；旧 CLI 只保留 adapter。

### M1：本地数据与凭据

5. `feat(migrations)`：用 Go migration 0001 无损接管四张旧表；覆盖 fresh、legacy、幂等、rollback 和 integrity tests。
6. `feat(persistence)`：migration 0002 创建领域、执行、报告、artifact 和 integration 表；实现 repository 与 revision conflict。
7. `feat(credentials)`：实现 Credential port、内存测试 adapter 和 Windows/macOS/Linux OS store adapter；所有持久化出口复用统一脱敏器。
8. `feat(catalog)`：把 JSON Model/Case 导入领域 repository，保留 Git JSON import/export，但 UI 不再直接写文件。

### M2：统一 CLI 与执行

9. `feat(cli)`：建立 `llm-test` 命令树和版本化 JSON/JSONL 输出，先覆盖 doctor、model、channel、audit、run、report。
10. `refactor(compatibility)`：把 `engine/apiaudit` 收敛到统一 execution package；旧 `llm-compat-engine` 调新 Core。
11. `feat(load)`：以 Bash benchmark 行为测试为基线迁移 Go 负载引擎，包括 burst、固定并发、开放环、send duration、timeout、drain 和取消。
12. `fix(streaming)`：统一 HTTP/SSE/语义成功判定和 TTFT/TPOT 定义；Python/Bash 包装层改调 `llm-test`。

### M3：统一报告

13. `feat(report-schema)`：现有性能和审计结果适配为 Report v1，不再分别聚合。
14. `feat(report-renderer)`：建立同一 HTML/CSS 到 JSON、HTML、PNG、PDF 的渲染与敏感信息扫描。
15. `refactor(history)`：Runs 页面与 CLI 读取统一 repository/report，完成旧历史 import 和基线比较。

### M4：桌面端和交付

16. `feat(desktop-poc)`：Wails v2 + React/Vite 调用 Core、订阅 Run、取消、访问同一 SQLite 和 OS store。
17. `feat(desktop)`：迁移五个 Streamlit 页面并完成 1000+ 行结果体验；达到等价后标记 Streamlit deprecated。
18. `build(release)`：三平台 CI、安装包、签名、更新和回滚；验证同一测试和报告。
19. `chore(legacy)`：删除已达到退出门槛的 Python/Bash 业务实现，只保留明确需要的导入/诊断工具。

## 兼容入口退出门槛

| 旧实现 | 迁移期用途 | 删除或降级条件 |
| --- | --- | --- |
| Streamlit | UI 原型和人工回归 | Wails 五页面、历史、报告、凭据达到功能等价 |
| Python `loadtest.py` | 迁移期行为 fixtures | Go runner 对固定 fixtures 的调度、SSE、指标和错误分类等价后删除 |
| `llm_benchmark.sh` | 开放环行为基准 | Go CLI 通过 burst/open-loop/drain 集成矩阵；脚本改为 wrapper 后再 deprecated |
| `llm-compat-engine` | Python JSONL 兼容 | Python adapter 被移除或改调主 CLI；其内部不再含业务编排 |
| Python SQLite | 旧历史只读验收 | Go migration/repository 完成旧库 fixture、恢复和数据保留测试后删除 |

## 每阶段完成定义

- 目标行为有自动化测试，且新增测试在实现前因缺失行为失败过；
- `go test ./...`、`go vet ./...` 和对应 Python/集成测试通过；
- `rg --files -g go.mod` 只返回预期 module；
- 新增 schema、事件、CLI 输出或 Report 变更有版本和迁移说明；
- 未提交生成物、秘密、数据库或无关工作区修改；
- 提交信息列出行为、测试和文档，不宣称未验证的平台。
