# Protocol Case Runtime Implementation

This work replaces the authored Case, Suite, Plan, Run and report contracts with one current protocol-centered implementation.

## Agreed boundaries

- A Case type identifies a supported protocol. Cases declare inputs, request body templates/generators and explicit assertions. Connection URL, headers, credentials and bound model belong to execution binding.
- Suites contain only references to same-protocol Cases and explicit input mappings. Plans contain ordered Case or Suite references for one protocol, parameters, load policy, aggregate acceptance and a root random seed.
- Saving definitions never pins dependent content or cascades invalidation. Run start resolves current references, validates the entire execution, binds one channel/model/credential and freezes the same resolved definitions before provider requests.
- Protocol executors collect observations; assertions determine verification. HTTP 400 can pass and HTTP 200 can fail. Empty assertions mean observation only, excluded from verification pass ratios.
- Scheduling acts on complete Case executions, not individual polling HTTP requests. Seed derivation uses stable entry/member/iteration/generator identities.
- Reports group by Plan entry. Suite parameters appear once in its header; Cases are compact named rows with protocol-specific metrics and expandable execution/assertion details.
- Backend and frontend capabilities are organized by protocol with explicit registration and shared scheduling, storage and reporting infrastructure.
- No historical runtime format readers, aliases or dual execution paths. Authored-file conversion is separate from normal runtime. User AppData is not reset or silently upgraded.

## Implementation stages

1. Shared input/generator/assertion contracts and protocol execution modules, with deterministic and negative-response tests.
2. Reference-only catalogs, Plan entries, startup resolution, immutable Run snapshots and operational result persistence.
3. Protocol-aware editors and compact report presentation; generated Wails bindings and end-to-end local verification.
4. Authored catalog conversion verification, obsolete path removal, documentation and final review.

All work is on `codex/protocol-case-runtime` in `E:/GITHUB/llm-test-worktrees/protocol-case-runtime`, based on `313eb23655be7ba7bf73ae5f13cd98409ff38af8`. The original checkout's uncommitted UI and experiment work is not included.

## Ownership during implementation

- Protocol worker: `internal/testspec`, `internal/protocols`, `internal/casetypes`, `internal/domain/test_case.go`.
- Catalog worker: Suite/Plan domain, authored codecs and catalogs, filesystem catalog adapter, Suite/Plan authored data.
- Frontend worker: protocol editors/renderers and frontend contracts, excluding generated bindings and desktop client integration.
- Integrator: operational Run/Result/Report domain and services, SQLite, desktop composition and bridge, Case corpus conversion, build/bindings, verification and staged commits.

Completion and validation evidence will be added after implementation, not inferred from this plan.

## Checkpoint: 2026-09-10

This is an implementation checkpoint requested during development, not a completed release or migration.

Verified at this checkpoint:

- `go build ./...` passes.
- Domain, testspec, protocols, casetypes, Case/Suite/Plan catalogs, runs, comparisons, workspace, SQLite and reporting package tests pass.
- Frontend TypeScript project build passes.

Outstanding before acceptance:

- Finish and verify the authored Case corpus conversion and performance Plan extraction. The converted Suite references are already checked in, while old Case documents are deliberately rejected by the current codec. The bundled catalog is not yet ready to run.
- Finish desktop and bundle integration test updates, then run the complete Go and frontend test suites.
- Finish warmup scheduling and protocol settings propagation; the definition and snapshot fields are present, but this checkpoint does not claim end-to-end execution coverage for them.
- Generate and verify Wails bindings using CLI 2.15.0; perform desktop/UI acceptance.
- Complete report export review, historical performance-format removal within the affected report scope, and final architecture/user documentation.
- Review memory usage during large runs: per-case summary collection still retains result drafts until the entry completes.

Pending conversion work remains locally in `_upgrade_cases.py` and `_suite-input-upgrade.json`; these incomplete temporary artifacts and diagnostic test logs are excluded from commits. No live AppData database has been converted or reset. Existing operational databases are rejected by the new single-current schema and require an explicitly planned upgrade before use.
