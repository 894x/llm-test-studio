# Compatibility engine

[English](README.md) | [简体中文](README.zh-CN.md)

This directory contains the compatibility execution packages and legacy JSONL
CLI adapter in the repository-wide Go module. It runs the protocol suites under
`data/cases/` and produces redacted JSON and HTML reports.

Run build commands from the repository root.

Build and inspect cases:

```sh
GOWORK=off go build -o engine/bin/llm-compat-engine ./engine/cmd/llm-compat-engine
./engine/bin/llm-compat-engine list --suite openai-chat --cases-root data/cases --jsonl
```

Dry-run a suite without making network requests:

```sh
./engine/bin/llm-compat-engine run \
  --suite openai-chat \
  --cases-root data/cases \
  --base-url https://gateway.example \
  --model example-model \
  --dry-run \
  --jsonl
```

Live credentials are accepted only through the environment variable named by
`--api-key-env`. Running the command starts the selected tasks directly. Use
`--dry-run` to preview the plan without making provider requests; video runs
retain their single-worker concurrency constraint.

JSONL schema version 1 emits:

- `case` events and a `final` event for `list`;
- one `plan`, one `progress` per planned run, and one `final` for `run`.

The run's final event contains report paths, verdict, overall status, and
summary counts. Exit code 1 means the audit completed with failed cases; exit
code 2 means configuration or usage failed.

This executable is the legacy JSONL compatibility adapter. Planning, execution
state, evaluation, and report orchestration live in shared Go Core packages
used by both this adapter and the primary `llm-test-studio` CLI. New workflows
should prefer `llm-test-studio audit list|run`.
