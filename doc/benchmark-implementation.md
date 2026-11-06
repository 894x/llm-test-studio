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
requests. Dataset downloads occur only after an explicit benchmark submission.

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
  packages. Full bootstrap tests in the original checkout currently include
  unrelated authored Kimi files: two pre-existing fixed-cardinality assertions
  expect 1085 Cases / 78 Suites but observe 1089 / 81. Verify the committed tree
  separately before final acceptance; do not rewrite user-owned definitions.
- Product UI and full rendered/end-to-end acceptance remain pending.
