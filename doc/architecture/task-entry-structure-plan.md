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
- [x] Express quick-test tasks through Suite metadata and provide connectivity
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
  execution limits, safe errors, and bounded redacted evidence. Starting a test
  is the execution action; do not add a separate billing acknowledgement.
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
found no runtime regression. Billing metadata initially consolidated in this
stage has since been removed following the testing-tool workflow decision below.

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

## Stage 3 execution foundation

Quick Suites now prepare temporary or saved-channel targets through the shared
Run queue, activation, cancellation, and reporting service. The operational Run
owns transient execution configuration, original source definitions, and resolved
inputs. Temporary credentials use a memory lease. The router keeps Suite order,
one invocation per member, distinct observation IDs, and a shared time origin.

Integration exposed and fixed two misleading aggregates: workspace progress
counted both request observations and Case summaries, and sequential driver
invocations restarted their report time offsets. Workspace summaries now count
observations once, preserve summary-only historical records, and validate even
uncounted summary rows. Quick tasks do not present Case cardinality as a request
budget because Case-owned schedules can produce several observations.

Controlled HTTPS integration covers mixed request/probe/latency members, input
overrides, original-file preservation, temporary targets, successful and failed
observations, report sealing, timeline ordering, workspace projections, and
SQLite close/reopen. Domain tests reject inconsistent task provenance; lease
tests verify copying, erasure, and refusal to serialize secrets.

This remains an application-service foundation. Bundled task profiles, native
entry bindings, the quick-entry UI, drafts, history replay, and explicit key
remembering are unfinished. No paid upstream verification has been performed.

Validation: `go test -p 1 ./... -count=1`, `go vet -p 1 ./...`, generated protocol
contract check, the full frontend suite (262 tests), lint, and build passed.
Subsequent focused tests also passed for mutable input isolation, sequential
timeline ordering, Suite progress rendering, and clearing billing acknowledgement
when the selected channel changes. The build retains the existing chunk-size
warning. Source review prompted the time-origin and channel-acknowledgement fixes;
the code graph was unavailable during this stage, so review used direct source.

## Bundled tasks and desktop invocation

Sixteen connectivity tasks now cover all five protocols and the existing Kimi,
Wan, and MiniMax model scopes. Thirteen new Suite files complement quick metadata
on the three existing Wan 3.0/MiniMax connectivity Suites. No Case definitions or
provider contracts changed. Task prompt defaults and bindings live in the Suite
files. MiniMax's existing profile manifest drives its generated metadata.

The native desktop binding and frontend client now expose `StartQuickTask` and
return the accepted Run ID. They do not combine the mutation with a workspace
refresh. The existing shared cancellation, persistence, and reporting lifecycle
continues to own the Run. Tests cover native lifecycle, safe error codes, strict
response parsing, and the HTTPS/SQLite integration through this desktop entry.

The execution-page replacement, drafts, history replay, and explicit credential
remembering remain unfinished. The stage 3 checkbox stays open until the new
task flow is usable from the quick-entry UI.

Validation: all Go packages passed tests (desktop rerun after updating its error
contract fixture), full Go vet and protocol generation check passed, and the
frontend passed 270 tests, lint, and build. The App tests were rerun after adding
the new mock method. The Suite generator's five tests and MiniMax drift check
passed. Wails v2.15.0 generated the native method without module changes. Direct
source review found no actionable issue; the code graph remained unavailable.
No provider calls or rendered quick-entry UI verification were performed here.

## Execution action simplification

The user clarified that a testing tool should not require a separate payment
checkbox after the user chooses to run a test. The desktop now uses the same
start action for all protocols. Runtime and compatibility commands, protocol
metadata, and both CLIs no longer carry payment-confirmation fields, errors, or
flags. Obsolete UI translations and duplicated confirmation tests are removed.

Version applicability, automatic Case requirements, credential handling, video
concurrency, and dry-run behavior remain validated. New regressions exercise
single and multiple video selections directly, while retaining rejection of
unscoped targets. This changes the product workflow; no provider calls were
needed to implement or verify it.

Verified: full Go tests and vet, protocol generation check, 262 frontend tests,
lint, and build. Wails bindings were regenerated with CLI v2.15.0 without
changing the Go module files. Direct source review found no remaining workflow
or validation regressions. README pairs share heading structure, commands, and
link targets. The existing frontend chunk-size warning remains.
