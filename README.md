<p align="right">
  <strong>English</strong> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="./assets/readme/hero.svg" width="100%" alt="LLM Test Studio turns versioned test cases into reproducible multi-model runs and evidence-backed reports">
</p>

<p align="center">
  <strong>A case-first, local-first workbench for reusable LLM evaluation.</strong><br>
  Define a test once, run it across models and channels, and keep the exact evidence behind every conclusion.
</p>

<p align="center">
  <code>Wails + React desktop</code> · <code>Go CLI</code> · <code>SQLite</code> · <code>OS credential store</code>
</p>

> [!NOTE]
> LLM Test Studio is licensed under Apache-2.0 and is being prepared for its first public open-source release.

## Product proof

<p align="center">
  <img src="./doc/images/llm-test-studio-cases.png" width="100%" alt="The real LLM Test Studio Windows desktop application showing 89 reusable test cases and revision-aware case details">
</p>

<p align="center"><sub>Real Windows desktop build · 89 built-in cases · no concept mockup or generated UI</sub></p>

The case catalog is the center of the product, not a setup screen hidden behind a run. Cases remain inspectable, revisioned assets that can be assembled into suites and plans, executed through different targets, and traced into reports.

## Why case-first testing

Most LLM tests begin as a prompt, a script, or a one-off dashboard run. That is useful for exploration, but difficult to reuse or audit later. LLM Test Studio treats the test definition as a durable asset:

- **Cases outlive runs** — requests, expected behavior, and assertions live in portable `case.json` files.
- **Intent stays separate from infrastructure** — a logical model maps to the upstream model name used by each channel, so the same case can exercise different providers or gateways.
- **Every run is reproducible** — the plan, model, channel, case revisions, load profile, SLA, and environment are pinned into an immutable snapshot.
- **Conclusions keep their evidence** — request results distinguish transport, protocol, semantic, and SLA failures before producing one sealed report model.
- **Desktop and automation agree** — the Wails application and script-friendly CLI use the same Go Application Core and domain rules.

## From case to evidence

```text
Versioned Test Case ──> Suite ──> Plan ──> Immutable Run Snapshot
                           targets + load + SLA          │
                                                          ▼
                                           Results + Evidence ──> Report
```

The reusable boundary is the exact case revision. Suites group those revisions; plans add candidate targets, load behavior, timeouts, and SLA thresholds; runs freeze the complete input before execution.

## Quick start

### Use a desktop release

Check [GitHub Releases](https://github.com/894x/llm-test-studio/releases) for available builds. Tagged releases are configured for Windows amd64 installers and archives, plus macOS universal DMG and ZIP packages. Linux does not currently have a packaged desktop release.

### Build the desktop application

Requirements: Go 1.25+ with the Go 1.26.6 toolchain, Node.js 24, pnpm 10.30.3, and Wails v2.15.0.

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
wails version

cd apps/desktop/frontend
pnpm install --frozen-lockfile
pnpm test
pnpm build

cd ..
wails build -nosyncgomod
```

Use `wails dev -nosyncgomod` instead of `wails build -nosyncgomod` for desktop development. On first start, structured application data is created below the operating system's user configuration directory.

### Build the CLI

```bash
go build -o llm-test-studio ./cmd/llm-test-studio
./llm-test-studio doctor --format human
```

On Windows PowerShell, run the binary as `.\llm-test-studio.exe`.

<details>
<summary><strong>CLI examples</strong></summary>

List the built-in Kimi K3 cases:

```bash
go run ./cmd/llm-test-studio audit list \
  --suite kimi-k3 \
  --cases-root cases \
  --format human
```

Run one compatibility case in Bash or another POSIX shell:

```bash
API_AUDIT_API_KEY='replace-me' go run ./cmd/llm-test-studio audit run \
  --suite kimi-k3 \
  --cases-root cases \
  --base-url https://gateway.example/v1 \
  --model kimi-k3 \
  --case F004 \
  --format human
```

Run a fixed-concurrency load test:

```bash
LOADTEST_API_KEY='replace-me' \
LOADTEST_URL='https://gateway.example/v1/chat/completions' \
LOADTEST_MODEL='kimi-k3' \
go run ./cmd/llm-test-studio load run \
  --requests 100 \
  --concurrency 10 \
  --stream \
  --input-tokens 100 \
  --max-tokens 10 \
  --output load-result.json
```

Use `--rate` with `--duration` for open-loop scheduling. Use `--request-file cases/<group>/<case>/case.json` to load a case request body. Plain HTTP is rejected unless `--allow-insecure-loopback` explicitly enables a localhost test server.

Avoid passing credentials directly as command arguments because shells may retain them in history. Windows PowerShell users can set the same variables with `$env:VARIABLE_NAME = 'value'` before running a command.

</details>

## What you can test

- Create and maintain built-in or user-authored test cases.
- Group exact case revisions into reusable suites and plans.
- Run single-request, fixed-concurrency, or open-loop tests with request timeouts and SLA thresholds.
- Exercise OpenAI Chat, Kimi K3, and Seedance compatibility cases with native Go execution engines.
- Compare one logical model across two or more configured channels.
- Inspect run history, request-level results, latency and token metrics, failure dimensions, SLA conclusions, and environment snapshots.
- Export the same report as JSON, standalone HTML, PNG, or PDF.
- Run compatibility audits and load tests from the CLI with stable JSON, JSONL, or human-readable output.

## Built-in case library

The repository currently includes 89 portable cases:

| Case group  | Cases | Focus                                                                       |
| ----------- | ----: | --------------------------------------------------------------------------- |
| OpenAI Chat |    43 | Chat compatibility, streaming, tools, reasoning, safety, and usage behavior |
| Kimi K3     |    40 | Kimi-specific compatibility and capability coverage                         |
| Seedance    |     6 | Text, image, video, and mixed-reference video requests                      |

Built-in cases are starting points, not a claim that every case applies to every model. Protocol compatibility is validated before a plan can run.

User cases are stored as `cases/<group>/<case>/case.json` beside the desktop executable. They can be copied, reviewed, versioned, and shared without including API keys. A user case overrides a built-in case with the same identity when the two sources are merged.

## Domain model

| Concept                 | Meaning                                                                                     |
| ----------------------- | ------------------------------------------------------------------------------------------- |
| **Model**         | The logical model being evaluated, independent of a provider-specific model name.           |
| **Channel**       | A callable service path with a Base URL, protocol, enabled state, and credential reference. |
| **Channel Model** | The upstream model name used for a logical model on a specific channel.                     |
| **Test Case**     | A versioned request, expected outcome, and assertion set.                                   |
| **Suite**         | A reusable collection of exact test case revisions.                                         |
| **Plan**          | Test intent: cases, candidate targets, load profile, and SLA thresholds.                    |
| **Run**           | One execution with an immutable input snapshot.                                             |
| **Report**        | The validated conclusion shared by JSON, HTML, PNG, and PDF exports.                        |

## Architecture

```text
Wails + React desktop ──┐
                        ├── Go Application Core ──┬── execution engines
llm-test-studio CLI ────┘                         ├── SQLite repository
                                                  ├── OS credential store
                                                  ├── filesystem case catalog
                                                  └── report renderer
```

The desktop and CLI share the same application services and domain rules. There is no Python or Streamlit runtime.

<details>
<summary><strong>Repository map</strong></summary>

- `apps/desktop` — Wails bindings and the React/Tailwind desktop interface.
- `cmd/llm-test-studio` — versioned CLI commands and JSON/JSONL output.
- `internal/application` — catalog, run, comparison, reporting, and workspace orchestration.
- `internal/execution` and `engine` — load and compatibility execution engines.
- `internal/persistence/sqlite` — schema migrations and repositories.
- `cases` — embedded, shareable cases grouped by protocol.
- `definitions` — non-secret model definition fixtures.

</details>

## Security and local data

- Test definitions reject credential-bearing headers, credential fields, and credential-like values.
- Channel keys are stored through the operating-system credential service. Updating a key creates a new credential revision.
- Persistent application data contains credential references, masked suffixes, and fingerprints, not plaintext secrets.
- Reports and results contain measurements and redacted evidence metadata, not authorization headers or credential bytes.
- SQLite stores catalog entities, pinned snapshots, runs, results, comparisons, and sealed reports. Portable cases remain filesystem assets.
- Plain HTTP endpoints are rejected except for explicitly enabled loopback testing.

## Current scope

- The dedicated comparison workflow compares one logical model across multiple channels; a full arbitrary model-by-channel matrix is not yet a first-class workflow.
- Case reuse is protocol-aware. A case cannot be attached to an incompatible model or channel.
- Windows amd64 and macOS universal are the configured packaged desktop targets. Other platforms can build from source where Wails supports them.

## Verify a source checkout

```bash
go test ./... -count=1
go vet ./...

cd apps/desktop/frontend
pnpm test
pnpm build
```

The legacy Bash benchmark remains as an independent load-engine acceptance fixture. Its mock LLM is implemented in Go:

```bash
bash scripts/test_llm_benchmark.sh
```

## License

LLM Test Studio source code, built-in case definitions, and project-owned media fixtures are available under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE), [third-party notices](THIRD_PARTY_NOTICES.md), and the [case and media provenance statement](cases/PROVENANCE.md) for attribution and scope details.

## Open-source status

Public-release preparation is in progress. Licensing, contributor guidance, security reporting, repository templates, and initial source/history secret scanning are now covered. Remaining release gates include replacing or stabilizing externally hosted media fixtures and deciding how public desktop binaries will be signed and notarized.
