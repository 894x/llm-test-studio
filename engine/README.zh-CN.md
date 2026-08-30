# 兼容性引擎

[English](README.md) | [简体中文](README.zh-CN.md)

本目录包含仓库级 Go module 中的兼容性执行包和旧版 JSONL CLI adapter。它运行
`cases/` 下的 `openai-chat`、`kimi-k3` 和 `seedance` 测试套件，并生成已脱敏的
JSON 与 HTML 报告。

所有构建命令都从仓库根目录执行。

构建并查看用例：

```sh
GOWORK=off go build -o engine/bin/llm-compat-engine ./engine/cmd/llm-compat-engine
./engine/bin/llm-compat-engine list --suite openai-chat --cases-root cases --jsonl
```

不发起网络请求地 dry-run 一个测试套件：

```sh
./engine/bin/llm-compat-engine run \
  --suite openai-chat \
  --cases-root cases \
  --base-url https://gateway.example \
  --model example-model \
  --dry-run \
  --jsonl
```

实时凭据只通过 `--api-key-env` 指定的环境变量传入。可能创建多个实时
Seedance 付费任务的计划还必须提供 `--confirm-paid-suite`。

JSONL schema version 1 输出：

- `list` 输出若干 `case` 事件和一个 `final` 事件；
- `run` 输出一个 `plan`、每个具体运行一个 `progress`，以及一个 `final`。

运行的 `final` 事件包含报告路径、verdict、overall 状态和汇总计数。退出码 1
表示审计已完成但存在失败用例；退出码 2 表示配置或命令用法错误。

在 Application Core 重构期间，此可执行文件继续作为 Streamlit 和脚本的兼容
adapter。计划、执行状态、评估和报告编排将迁入共享 Core 包，由该 adapter 与
`llm-test` 共同调用。
