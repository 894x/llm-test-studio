# Compatibility engine

This directory is an independent Go module. It runs the `openai-chat`,
`kimi-k3`, and `seedance` compatibility suites stored under the repository's
`cases/` directory and produces redacted JSON and HTML reports.

Build and inspect cases:

```sh
GOWORK=off go build -o bin/llm-compat-engine ./cmd/llm-compat-engine
./bin/llm-compat-engine list --suite openai-chat --cases-root ../cases --jsonl
```

Dry-run a suite without making network requests:

```sh
./bin/llm-compat-engine run \
  --suite openai-chat \
  --cases-root ../cases \
  --base-url https://gateway.example \
  --model example-model \
  --dry-run \
  --jsonl
```

Live credentials are accepted only through the environment variable named by
`--api-key-env`. Multi-task live Seedance plans additionally require
`--confirm-paid-suite`.

JSONL schema version 1 emits:

- `case` events and a `final` event for `list`;
- one `plan`, one `progress` per planned run, and one `final` for `run`.

The run's final event contains report paths, verdict, overall status, and
summary counts. Exit code 1 means the audit completed with failed cases; exit
code 2 means configuration or usage failed.
