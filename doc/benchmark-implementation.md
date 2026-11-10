# Inspect benchmark implementation

## Product acceptance

- Quick Test has four peer modes: functional, performance, plan, and data testing.
- Data testing selects one or more registered Inspect Evals datasets and uses a
  saved channel or an explicit base URL, model ID, protocol, and temporary key.
- Each submission retains its identity, frozen dataset/configuration, progress,
  sample outcomes, and per-dataset reports. A later submission never replaces it.
- The existing Run workspace lists benchmark executions alongside other tests;
  details show loading, sample progress, failures, cancellation, and report links.
- The existing Report workspace lists each dataset report with its dataset,
  channel/model, observed scores, sample outcomes, and reproducibility metadata.
- Reports preserve partial/error outcomes and support inspecting individual
  samples and exporting the frozen report. Execution success is not a capability
  pass/fail threshold, and a benchmark score does not prove model identity.
- UI follows the repository desktop design skill, including all three desktop
  size anchors, light/dark/system themes, keyboard operation, and retained state.

## Architecture and compatibility

Go Application Core owns admission, channel credential leases, execution state,
bounded process I/O, persistence, controls, and presentation projections. A shipped
Python worker uses pinned Inspect AI and Inspect Evals packages to run registered
tasks, emits a versioned structured event stream, and retains native Inspect logs.
Temporary keys travel over process stdin and are never arguments, authored
configuration, history, or frontend report data.

Use the current application/service/adapter structure. Benchmark storage is a new,
explicitly versioned, atomic file boundary under the private desktop data root;
existing SQLite, Case/Suite/Plan, reports, public CLI, and runner protocols retain
their supported formats. The desktop's existing exclusive workspace lock protects
the new local store. Older releases ignore the new directory; downgrading hides
benchmarks without converting or deleting them. Unknown/newer/corrupt benchmark
formats fail without mutation. No migration of existing user data is required.

Benchmark execution is local, explicitly identified in the interface. It does not
silently submit unsupported work to the default remote runner. Runtime preparation
is explicit and observable; installing Python dependencies does not send model
requests. Dataset downloads occur after an explicit benchmark submission or a
download action in the Dataset workspace.

## Staged delivery

1. **Design and acceptance**: record scope, compatibility, and verification gates.
2. **Inspect adapter**: pinned Python environment, dataset manifest, structured
   progress/result protocol, safe scoring projection, and offline adapter tests.
3. **Application core**: durable runs and dataset reports, credential integration,
   runtime preparation, process lifecycle/cancellation, desktop bindings and tests.
4. **Product UI**: fourth quick-test mode, connection/dataset forms, Run workspace
   integration, per-dataset report list/detail/export, localization and tests.
5. **Acceptance**: actual Inspect against a local deterministic API, persistence
   reopen/failure verification, frontend/Go checks, rendered desktop acceptance,
   and update the validation record below.

Each stage is committed separately after relevant verification. Only task-owned
changes are staged; unrelated work already present in the checkout is preserved.

## Validation record

- Design committed as `2fde95b6`; pinned Inspect worker committed as `ffd862c7`.
- Worker tests exercise real Inspect against a local deterministic API, including
  repeated epochs, sample progress, native metrics, redaction, and all six factory
  imports. Model inference in these tests is local and incurs no provider charges.
- Application tests cover per-dataset report identities, persistence/reopen,
  cancellation of queued/active work, retained partial samples, pagination,
  secret-free exports, interruption reads, malformed worker events, and rejection
  of unknown/newer or incomplete documents without modifying them.
- Desktop bridge tests and Go vet pass for the new application/runtime/bridge
  packages. Full bootstrap tests also fail in a detached clean checkout at
  `9df5d432`: two fixed-cardinality assertions expect 1085 Cases / 78 Suites but
  observe 1089 / 81 after the baseline Kimi additions in `9edea2a2`. This is
  independent of the unrelated dirty authored files in the original checkout.
- Product UI provides the fourth quick-test mode, dataset selection, retained
  options, paginated history, Run progress/cancellation, and per-dataset reports
  with sample evidence and JSON export in both interface languages.
- Frontend type checking, lint, build, and all 1145 tests across 78 files pass
  (`pnpm test -- --run --maxWorkers=2`). Browser checks at 1440x900, 1024x768,
  and 960x640 show no page overflow or duplicate field IDs; searching datasets
  and switching modes preserve selection. Explicit light/dark selections and
  system-theme resolution are checked without relevant console errors.
- Real Inspect worker tests cover OpenAI Chat, OpenAI Responses, and Anthropic
  Messages against local deterministic APIs, including explicit credentials in
  the presence of an ambient Anthropic token and the streaming-off setting.
- The opt-in registered-dataset integration passes through both the process
  adapter and application service. Native logs use a shorter report-ID path to
  avoid the reproduced Windows filename-length failure.
- A production Wails build with an isolated application-data root runs ARC Easy
  and ARC Challenge with two real dataset samples each against a local mock
  channel. Run displays loading/in-flight/completed progress, both dataset
  reports retain their identities, and the native JSON save dialog produces a
  report with two samples, native accuracy/stderr, and no temporary API key.
- Reopening the same isolated native workspace preserves the completed 2/2
  outcomes for both datasets alongside the earlier failed run. The user guide
  in `doc/benchmark-testing.md` records preparation, scoring, local execution,
  bounded evidence, versioning, and downgrade behavior.

## Dataset management follow-up

- Add a dedicated Dataset navigation entry with a searchable catalog of 24 tasks,
  actual download states, cache size, question/file counts, provenance, and a
  selected-dataset handoff into Quick Test. Variants and years are separate tasks,
  not 24 unrelated benchmark families. The curated tasks use native scoring
  without a separate model judge; MATH tasks requiring extra judge/platform
  dependencies are not included.
- An explicit download prepares all task data without receiving a model key.
  The first evaluation automatically joins or starts the same preparation, then
  loads its cached Hugging Face data offline. Downloads support cancellation,
  retry, checksum verification, and confirmed cache-only cleanup. Active or
  queued evaluations prevent cache verification/removal.
- Version-1 metadata lives beside Benchmark records; regenerable datasets use
  short, isolated workspace/dataset directories in the OS user cache. Existing
  global Hugging Face caches, reports, SQLite data, and public runner/CLI formats
  are unchanged. Unknown metadata is rejected without modification; interrupted
  operations remain observable and retryable after reopening.
- Go tests and vet pass for the application, runtime, bridge, and desktop entry
  point. Tests cover automatic preparation/reuse, cancellation, same-size cache
  corruption, cache removal preserving reports, interrupted reads, and rejected
  newer metadata. Six Python tests pass, including imports and no-argument binding
  of all 24 factories and download without model calls.
- The registered ARC integration passes after a real download, offline execution
  against a deterministic local API, and subsequent cache verification. Inspect's
  trace/viewer files are excluded from dataset checksums; a regression fixture
  covers viewer state written by evaluation. Relative HF dataset paths avoid the
  reproduced Windows lock-filename length failure.
- All 1,148 frontend tests across 79 files pass with two workers and a 20-second
  per-test allowance for the existing large-history interaction. Type checking,
  lint, and production build pass. A subsequent focused test also verifies the
  16/17-dataset submission boundary. Presentation checks cover 1440x900, 1024x768,
  and 960x640 without page-level horizontal overflow; dark/system changes, the
  compact inspector's Escape/focus restoration, search, and test handoff pass.
- An isolated production Wails workspace downloads ARC Easy through the actual
  Dataset UI: 2,376 questions, 2,875,515 bytes, and 15 cached files. Native cache
  verification completes successfully. No paid model endpoint is used. Other
  registered tasks have factory validation, not a claim of 24 completed downloads.
