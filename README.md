# LLM Studio

[简体中文](README.zh-CN.md)

LLM Studio is a local-first desktop workspace and CLI for testing OpenAI-compatible LLM gateways. The product is implemented with one Go Application Core and a Wails + React desktop interface; there is no Python or Streamlit runtime.

## Capabilities

- Manage logical models, channels, Base URLs, API keys, and per-channel upstream model mappings. Keys are stored in the operating-system credential store and are never returned to React, SQLite, reports, or logs.
- Keep built-in and user-authored cases as shareable `cases/<group>/<case>/case.json` files. User cases are created beside the desktop executable and override built-in cases with the same identity when the two sources are merged by group.
- Build reusable suites and plans with pinned case revisions, fixed concurrency or open-loop load, request timeouts, and SLA thresholds.
- Run OpenAI Chat, Kimi K3, and Seedance compatibility cases with the Go execution engines.
- Compare one logical model across two or more selected channels. Every channel gets an independent immutable run snapshot and report.
- Inspect run history, request-level results, latency and token metrics, SLA conclusions, and environment snapshots.
- Export the same sealed report document as JSON, standalone HTML, PNG, or PDF; PNG can also be copied from the desktop report page.
- Use the Go CLI for compatibility audits, generic load runs, and diagnostics.

## Architecture

```text
Wails + React desktop ─┐
                       ├─ Go Application Core ─┬─ execution engines
llm-studio CLI ────────┘                       ├─ SQLite repository
                                               ├─ OS credential store
                                               ├─ filesystem case catalog
                                               └─ report renderer
```

Important directories:

- `apps/desktop` — Wails bindings and the React/Tailwind desktop UI.
- `cmd/llm-studio` — versioned CLI commands and JSON/JSONL output.
- `internal/application` — catalog, run, comparison, reporting, and workspace orchestration.
- `internal/execution` and `engine` — load and compatibility execution engines.
- `internal/persistence/sqlite` — migrations and repositories.
- `cases` — embedded shareable cases grouped by protocol.
- `definitions` — non-secret model definition fixtures.

## Desktop development

Requirements: Go 1.25+, Node.js, pnpm, and Wails v2.

```bash
cd apps/desktop/frontend
pnpm install --frozen-lockfile
pnpm test
pnpm build

cd ..
wails dev
```

Build the desktop executable:

```bash
cd apps/desktop
wails build
```

On first start, structured application data is created below the operating system's user configuration directory. User-authored cases are stored in a `cases` directory beside the executable so the directory can be copied and shared directly.

## CLI

Build or run the CLI:

```bash
go build -o llm-studio ./cmd/llm-studio
go run ./cmd/llm-studio --help
```

List and run compatibility cases:

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

Run a load test. `LOADTEST_API_KEY`, `LOADTEST_URL`, and `LOADTEST_MODEL` remain supported for script-friendly configuration:

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

Use `--rate` with `--duration` for open-loop scheduling. Use `--request-file cases/<group>/<case>/case.json` to load a case request body. Plain HTTP is rejected except when `--allow-insecure-loopback` explicitly enables a localhost test server.

## Verification

```bash
go test ./... -count=1
go vet ./...

cd apps/desktop/frontend
pnpm test
pnpm build
```

The legacy Bash benchmark remains as an independent load-engine acceptance fixture. Its mock LLM is now implemented in Go:

```bash
bash scripts/test_llm_benchmark.sh
```

## Security and local data

- Never add API keys to model definitions, cases, examples, reports, or command arguments recorded by shell history.
- Channel keys are written to the OS credential store when a channel is created or updated. Updating a key creates a new credential revision so historical pinned runs keep their original binding.
- Reports and request results contain measurements and redacted evidence metadata, not authorization headers or credential bytes.
- The SQLite database stores catalogs, pinned snapshots, runs, results, comparisons, and sealed report documents. Cases remain portable files rather than catalog rows.
