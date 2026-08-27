# LLM Test Lab

A standalone model validation platform for compatibility audits, Seedance task checks, and OpenAI-compatible performance testing.

## Features

- Managed model profiles and filesystem-backed case editing
- OpenAI Chat, Kimi K3, and Seedance compatibility suites
- Standalone Go audit engine with redacted JSONL progress and reports
- Scheduled concurrent load tests with streaming support
- Text, image, and video request fixtures
- Request, token, latency, queue, and concurrency metrics
- SQLite-backed performance and compatibility history
- Per-run request templates with sampled response capture
- JSON, standalone HTML, PDF, and clipboard PNG report exports

## Setup

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -r requirements.txt
```

The compatibility engine is built from `engine/` when needed and requires Go. It is a separate module and does not depend on the original `new-api` checkout.

## Run the dashboard

```bash
streamlit run app.py
```

Open [http://127.0.0.1:8501/](http://127.0.0.1:8501/) after startup. API keys are entered for an individual run, passed to the compatibility engine only through its process environment, and never persisted.

## Application structure

```text
app.py                       Streamlit entry point
llm_test/app_shell.py        Navigation and application shell
llm_test/pages/              Overview, models, cases, plans, and runs
llm_test/catalog.py          Model and case filesystem catalog
llm_test/storage.py          Performance-run persistence
llm_test/audit_storage.py    Compatibility and Seedance persistence
llm_test/metrics.py          Shared performance calculations
llm_test/engine_client.py    Python boundary for the Go engine
engine/                      Standalone compatibility engine
definitions/models/         Versioned model profiles
cases/                       Versioned test definitions
data/artifacts/              Ignored, redacted local run artifacts
```

Model profiles intentionally contain no credentials. Cases and profiles can be maintained in the dashboard while remaining reviewable JSON files.

## Performance capture policy

Each performance run stores one complete request template. Individual request rows store timing/token metrics and only a top-level request delta. Raw responses support four policies:

- `errors_and_sample` — the default; failures plus the first N successful samples
- `errors_only`
- `none`
- `all`

Multimodal Base64 payloads are compacted in recorded artifacts.

## Run the tests

```bash
python -m unittest discover -v
```

To test the standalone engine when Go is installed:

```bash
cd engine
GOWORK=off go test ./...
GOWORK=off go build ./cmd/llm-compat-engine
```

## CLI test scripts

The CLI scripts read credentials and endpoint settings from environment variables:

```bash
export LOADTEST_API_KEY="..."
export LOADTEST_URL="https://example.com/v1/chat/completions"
export LOADTEST_MODEL="your-model"

python run_100_test.py
python run_multimodal.py
python run_video_test.py
```

`KIMI_K3_API_KEY` remains supported by `run_100_test.py` for compatibility with its existing workflow.

## Local data

`loadtest_history.db`, `data/`, and generated result JSON files are intentionally ignored by Git. Deterministic media under `fixtures/`, model profiles under `definitions/`, and protocol suites under `cases/` are project-owned inputs.
