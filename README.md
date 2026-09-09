<p align="right">
  <strong>English</strong> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <img src="./assets/readme/hero.svg" width="100%" alt="LLM Test Studio is a local-first acceptance and benchmarking studio for LLM providers, gateways, and deployments">
</p>

<p align="center">
  <strong>A local-first acceptance and benchmarking studio for LLM providers, gateways, and deployments.</strong><br>
  Run the same versioned tests across every channel, measure compatibility and performance, and keep the evidence behind every release decision.
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

The case catalog is the center of the product, not a setup screen hidden behind a run. Cases remain inspectable, revisioned assets that can be assembled into suites and plans, executed against different provider or gateway endpoints, and traced into reports.

## Accept a channel before production

A successful HTTP response does not prove that an LLM endpoint is ready to carry production traffic. The same logical model can behave differently across providers, gateways, regions, or private deployments. LLM Test Studio turns that uncertainty into a repeatable acceptance decision:

- **Protocol compatibility** — verify request parameters, streaming events, tool calls, multimodal inputs, usage fields, and error behavior.
- **Capability truth** — prove that an advertised model or endpoint actually supports the behavior your application depends on.
- **Performance under load** — measure latency, token timing, throughput, queue delay, and scheduler pressure with single, fixed-concurrency, or open-loop traffic.
- **Operational reliability** — separate transport, protocol, semantic, timeout, and SLA failures instead of reducing them to one success rate.
- **Auditable evidence** — pin the exact case, target, load profile, SLA, and environment behind each conclusion.

Gateways route production traffic. LLM Test Studio validates those routes before launch and after a provider, model, gateway, or deployment changes.

## Why case-first testing

Most LLM tests begin as a prompt, a script, or a one-off dashboard run. That is useful for exploration, but difficult to reuse or audit later. LLM Test Studio treats the test definition as a durable asset:

- **Cases outlive runs** — requests, expected behavior, and assertions live in portable `case.json` files.
- **Intent stays separate from infrastructure** — a logical model maps to the upstream model name used by each channel, so the same case can exercise different providers or gateways.
- **Every run is reproducible** — the plan, model, channel, case revisions, load profile, SLA, and environment are pinned into an immutable snapshot.
- **Conclusions keep their evidence** — request results distinguish transport, protocol, semantic, and SLA failures before producing one sealed report model.
- **Desktop and automation agree** — the Wails application and script-friendly CLI use the same Go Application Core and domain rules.

## From channel acceptance to evidence

```text
Versioned Test Case ──> Suite ──> Plan ──> Immutable Run Snapshot
                           targets + load + SLA          │
                                                          ▼
                                           Results + Evidence ──> Report
```

The reusable boundary is the exact case revision. Suites group Case references; plans define their ordered execution, inputs, load, timeouts and SLA thresholds; runs resolve current revisions, bind the selected channel/model, and freeze the complete input before execution. This keeps test intent independent from the provider-specific endpoint that happens to serve the model.

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


### Current protocol runtime

Desktop and CLI audit execution share the protocol runtime for `openai-chat`, `seedance`, `wan-video`, and `minimax-video`. Cases use envelope schema 3 and explicit assertions; Suites use schema 2 with ordered Case references and input bindings. Plans retain per-entry load, warmup, settings and seed. The Run binds one model, channel and credential.

CLI `audit run --seed 42 --inputs '{"prompt":"Hello"}'` accepts only inputs declared by selected Cases. Omit `--inputs` to use their declared defaults. `--all-cases` selects enabled automatic Cases; manual/disabled selections fail explicitly. `--dry-run` prepares requests without network calls or verification claims. Waiting is authored as `workflow.mode`, replacing `--no-wait`.

The repository contains 711 current Cases, 44 Suites and 5 performance Plans. Sixteen previous model/authentication mutation Cases are disabled because those fields belong to Run binding. Empty assertions produce observation-only results and do not inflate verification pass rates. See [runtime integration and acceptance](doc/design/protocol-case-runtime-implementation.md) for format changes and historical-data impact.

<details>
<summary><strong>CLI examples</strong></summary>

List the built-in OpenAI-compatible cases (including Kimi models):

```bash
go run ./cmd/llm-test-studio audit list \
  --suite openai-chat \
  --cases-root data/cases \
  --format human
```

Run one compatibility case in Bash or another POSIX shell:

```bash
API_AUDIT_API_KEY='replace-me' go run ./cmd/llm-test-studio audit run \
  --suite openai-chat \
  --cases-root data/cases \
  --base-url https://gateway.example/v1 \
  --model kimi-k3 \
  --case must.usage_non_stream \
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

Use `--rate` with `--duration` for open-loop scheduling. Use `--request-file data/cases/<group>/<case>/case.json` to load a case request body. Plain HTTP is rejected unless `--allow-insecure-loopback` explicitly enables a localhost test server.

Avoid passing credentials directly as command arguments because shells may retain them in history. Windows PowerShell users can set the same variables with `$env:VARIABLE_NAME = 'value'` before running a command.

</details>

## What you can run

- Smoke-test a URL, model, or saved channel without first building a full catalog.
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

User cases are stored as `data/cases/<group>/<case>/case.json` beside the desktop executable. They can be copied, reviewed, versioned, and shared without including API keys. A user case overrides a built-in case with the same identity when the two sources are merged.

Authored data uses the following layout. Source paths are relative to the repository root; desktop paths are relative to the executable, including `apps/desktop/build/bin` during Wails development and local builds.

| Data | Source / desktop path |
| --- | --- |
| Cases | `data/cases/<group>/<case>/case.json` |
| Suites | `data/suites/<group>/<suite>/suite.json` |
| Model profile fixtures (source only) | `data/definitions/models/*.json` |
| User models | `data/models.json` |
| Channels and model mappings | `data/channels.json` |
| Plans | `data/plans/<id>.json` |

Built-in Cases and Suites are embedded from `data/` into the executable; a fresh release needs no external catalog copy. Desktop edits and catalog lock files are written under the executable's `data/` directory. CLI commands default `--cases-root` to `data/cases` relative to the working directory; use an explicit path when launching elsewhere.

For an existing installation, close the app and move its adjacent `cases/`, `suites/`, `plans/`, `models.json`, and `channels.json` into `data/`, including revision files and lock sidecars. Keep the executable in the same installation directory so existing keyring credentials remain accessible. The app reads only the new paths. SQLite run history, reports, and logs retain their existing user-configuration locations.

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
                                                  ├── filesystem authored catalog
                                                  └── report renderer
```

The desktop and CLI share the same application services and domain rules. There is no Python or Streamlit runtime.

<details>
<summary><strong>Repository map</strong></summary>

- `apps/desktop` — Wails bindings and the React/Tailwind desktop interface.
- `cmd/llm-test-studio` — versioned CLI commands and JSON/JSONL output.
- `internal/application` — catalog, run, comparison, reporting, and workspace orchestration.
- `internal/execution` and `engine` — load and compatibility execution engines.
- `internal/persistence/sqlite` — operational schema and runtime-evidence repositories.
- `data/cases` — embedded, shareable cases grouped by protocol.
- `data/suites` — embedded suite definitions and scenario manifests.
- `data/definitions` — non-secret model definition fixtures.

</details>

## Security and local data

- Test definitions reject credential-bearing headers, credential fields, and credential-like values.
- Channel keys are stored through the operating-system credential service. Updating a key creates a new credential revision.
- Persistent application data contains credential references, masked suffixes, and fingerprints, not plaintext secrets.
- Reports and results contain measurements and redacted evidence metadata, not authorization headers or credential bytes.
- Model, Channel, Channel Model, Test Case, Suite, and Plan definitions are file-backed. SQLite stores only operational data: immutable Run snapshots, Results, Evidence, Comparisons, performance reports, and sealed Reports.
- Plain HTTP endpoints are rejected except for explicitly enabled loopback testing.

## Current scope

- LLM Test Studio calls configured endpoints to validate them; it is not a production traffic gateway, router, or provider SDK.
- The product focuses on endpoint acceptance and comparative evidence, not on maintaining a public model leaderboard or profiling inference hardware below the API boundary.
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

LLM Test Studio source code, built-in case definitions, and project-owned media fixtures are available under the [Apache License 2.0](LICENSE). See [NOTICE](NOTICE), [third-party notices](THIRD_PARTY_NOTICES.md), and the [case and media provenance statement](data/cases/PROVENANCE.md) for attribution and scope details.

## Open-source status

Public-release preparation is in progress. Licensing, contributor guidance, security reporting, repository templates, and initial source/history secret scanning are now covered. Remaining release gates include replacing or stabilizing externally hosted media fixtures and deciding how public desktop binaries will be signed and notarized.
