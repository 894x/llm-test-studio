# Task entry and structural consolidation

## Objective

Reduce duplicated sources of truth and fix the observed quick-test limitations.
Quick test is a lightweight entry point for a testing task represented by a
Suite; connectivity is the initial task. It must accept a temporary target
without requiring authored models, channels, or Plans.

## Delivery stages

- [x] Consolidate protocol metadata, legacy kind validation, and execution
  dispatch; discover bundled resources by layout; derive frontend protocol
  options and validation from the same source.
- [ ] Express quick-test tasks through Suite metadata and provide connectivity
  tasks for text and the existing version-scoped video adapters. Keep task
  selection, protocol applicability, and editable inputs driven by definitions.
- [ ] Prepare quick Suite runs with temporary or saved targets and use shared
  execution, cancellation, progress, snapshots, and result handling. Preserve
  the existing performance entry without treating performance as a text-only
  prerequisite for every task.
- [ ] Retain form drafts and persist run history. Derive recent task/target
  combinations from history; restore parameters for repeat and edited runs.
  Keep credentials in the keyring or temporary memory, with references only in
  history. Credential remembering must be explicit for temporary targets.
- [ ] Verify the complete user flow, review changes, and commit coherent stages.

## Boundaries and acceptance

- Authored Case/Suite/Plan definitions remain files. Operational snapshots and
  history remain SQLite. Running a temporary task must not create authored
  catalog records as an implementation shortcut.
- Preserve provider/version applicability, asynchronous task completion checks,
  paid-run confirmation behavior, safe errors, and bounded redacted evidence.
- A new task over an existing executor must not require another frontend list
  or another quick-test executor branch.
- Both successful and failed executions must be recoverable in history.
- Verify protocol dispatch with controlled HTTP fixtures, bundle completeness
  against repository files, targeted and full Go/frontend checks as appropriate,
  and rendered UI checks for the eventual workflow.
- Keep Wails and its CLI at v2.15.0. Do not push unless requested.

## Current evidence

The initial source audit found independent protocol lists in Doctor, CLI
validation/help, domain validation, and frontend controls; duplicated legacy
kind sets and desktop/CLI dispatch; explicit Case bundle group patterns; and a
quick-test service tied to one synchronous Chat Completions request. Desktop
Case/Suite discovery already uses filesystem traversal and should be reused.

This document tracks unfinished scope across staged commits. A stage does not
establish completion of the overall objective.

## Stage 1 outcome

Protocol metadata lives in `internal/protocol`. Legacy drivers own accepted
kinds and dispatch in `engine/apiaudit/registry.go`. Case catalog descriptors,
CLI validation/help, Doctor, and desktop execution consume those definitions.
The Case bundle includes every `*/*/case.json`; its test checks the embedded
bytes against source files. Frontend protocol types/options/validation are
generated from Go; regenerate with `go run ./scripts/generate-protocols` and
verify with the same command plus `-check` (also enforced by CI).

Verified: `go test -p 1 ./... -count=1`, `go vet -p 1 ./...`, frontend
`bun run test --maxWorkers=2` (251 tests), lint, build, and the generated-contract
check. The build retains the existing large-chunk warning. A read-only reviewer
found no runtime regression; the unconditional paid-confirmation flag was
renamed `AlwaysConfirmPaid` to distinguish it from Seedance batch policy.

Remaining follow-through: during shared task preparation, replace the CLI's
Seedance batch and Wan/MiniMax paid-task branches with an explicit task-count
policy, and derive the UI acknowledgement from that policy. Do not treat
`AlwaysConfirmPaid: false` as permission to execute an unconfirmed batch.

## Video output correctness

Fixed Seedance accepting `succeeded` without a usable output video URL. Six
invalid-output fixtures reproduced the false pass before the fix. Seedance,
Wan, and MiniMax now share the existing HTTP(S), host, and no-userinfo URL
validation; signed HTTPS output remains accepted and evidence includes only
the host. Provider-specific task identity and output contract checks remain
in their adapters.

Verified the API audit engine, compatibility service, run service, both CLI
entry points, and relevant vet checks. Read-only review found no issues. These
are controlled HTTP fixture tests, not paid upstream verification.

## Stage 2 definition foundation

Optional `quick_test` metadata now carries task descriptions, timeouts, typed
editable inputs, and bindings to existing Case request-body fields. Generic
tasks can omit a model target only when their protocol and Cases permit it.
The shared `Suite.ValidateCases` rule checks exact Case revisions, model
applicability, enabled automatic task members, and input bindings across file
loading, catalog mutations, snapshots, and historical reads.

Metadata survives file saves, revision history, DTO cloning, and normal Suite
edits. Fixed tasks preserve an empty input array. The shared JSON Pointer
resolver also prevents alternate numeric spellings from aliasing an array
field. See [the definition contract](suite-quick-tasks.md).

This is a foundation commit: bundled task profiles, task execution, form drafts,
and history remain unfinished. The stage checkbox above stays open until the
connectivity task definitions are included.

Validation: full Go tests and vet, 254 frontend tests, lint, build, and generated
protocol-contract check passed. Review found and prompted fixes for metadata-only
write recovery and Case edits invalidating dependent quick Suites; focused
repository regressions reproduce those failures and cover compatible edits with
historical references. The final repository/catalog checks passed after the
fixes; read-only review has no remaining findings. No provider calls were made.
