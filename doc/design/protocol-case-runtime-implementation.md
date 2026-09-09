# Protocol Case Runtime Integration

Integrated on 2026-09-10 using a two-parent merge from `codex/protocol-case-runtime` (`a6773dd08e0f75e8bd64d68a911209e0535ba705`) into local `main` (`84ab8df458b1bb06d554a881a1282aae04538439`). Work was verified in an isolated worktree; main's unrelated `doc/experiments/` content and source-worktree private conversion artifacts were preserved. Git reported seven conflicting paths: five textual conflicts (13 regions) and two obsolete renderer deletion conflicts.

## Current contracts

- Case envelope schema 3, definition schema 2, protocol type version 1. The supported types are `openai-chat`, `seedance`, `wan-video`, and `minimax-video`. Cases declare inputs, request templates/generators, workflows and explicit assertions.
- Suite schema 2 contains ordered same-protocol Case IDs and explicit mappings to Case inputs. It does not route models or merge arbitrary bodies into members.
- Plans contain ordered Case or Suite entries, parameters, load, warmup, protocol settings, SLA and a root seed. Run start resolves current references, validates the whole execution, binds one model/channel/credential and freezes snapshot schema 4. Saved references do not pin revisions prematurely.
- Scheduling operates on complete Case workflows. Warmup uses separate request/random identities, preserves measured-seed reproducibility, and is excluded from measured progress and verification denominators. Cancellation and stop-sending prevent later measured work. Suite failure does not stop later entries unless cancelled.
- Protocol execution collects observations; assertions produce passed, failed, not_applicable or indeterminate verdicts. Expected HTTP 400 may pass; unexpected HTTP 200 may fail. Empty assertions remain observation only, including an unsuccessful transport observation. Execution failure remains recorded independently.
- Formal report schema 3 groups by Plan entry and Case. The renderer retains timing, tokens, cache usage, queue delay, request/response evidence, assertions and video task/artifact details. Tables summarize distinct measured observations and retain separate warmup records. TPOT uses ms/token.
- CLI audit list/run uses the same protocol client and assertion evaluator. The old kind executors were removed. `--seed` and declared `--inputs` are supported; waiting belongs in authored workflows, replacing `--no-wait`. Dry-run stops at the shared HTTP boundary and prepares the initial request without network calls or invented verification.
- The independent Quick Performance feature keeps its existing product flow; this merge replaces protocol Case execution and formal Run reports. It does not convert standalone performance archives.

## Authored-data conversion

A temporary offline script validated the prior Case schema before writing the current schema. It converted all 711 repository-owned Case documents, checked all 44 Suite reference/input mappings, and extracted five performance Plans. The converted artifacts passed the current Go codec, protocol validation, bundle tests and CLI preparation checks. The integration-worktree script copies and conversion audit were removed after verification; no converter runs at application startup or is shipped as a runtime dependency.

| Protocol | Definitions | Enabled automatic | Manual | Disabled |
|---|---:|---:|---:|---:|
| OpenAI Chat | 343 | 269 | 6 | 68 |
| Seedance | 6 | 6 | 0 | 0 |
| Wan Video | 213 | 92 | 0 | 121 |
| MiniMax Video | 149 | 43 | 101 | 5 |
| Total | 711 | 410 | 107 | 194 |

Sixteen previous model/authentication mutation Cases are disabled and removed from 16 affected Suites: five H3, seven GLM 5.3, and four Wan 3.0 definitions. Their claims are retained as unavailable coverage because model and credential fields belong to Run binding. They are not silently translated into unrelated assertions. Manual and other disabled Cases still require their documented assets, oracle or budget; conversion is not live provider coverage.

The Suite authoring generator, both checked-in profile manifests and the inventory helper now consume only current protocol Cases and reference-only Suites. They reject old kind selectors/model routing fields and do not infer negative coverage from names. Runtime readers reject unsupported old authored/snapshot/report formats without rewriting them.

## Verification

- Complete `go test ./...` and `go vet ./...` passed.
- Frontend: 32 test files / 303 tests passed; `pnpm lint` and `pnpm build` passed. The existing large-bundle advisory remains.
- Wails CLI/module remain 2.15.0. Bindings were regenerated and the generated protocol contract check passed. Production packaging uses `wails build -s -m -nosyncgomod`.
- Current authoring tools: five Python tests and both Suite generator `--check` commands passed.
- Built CLI previewed all 410 enabled automatic Cases across four protocols. It included bodyless models-list GET and produced no assertion-pass claims or upstream calls.
- `TestProtocolDesktopEndToEnd` exercises production initialization, real file catalogs, scheduler, scoped local HTTPS fixtures, credential leases, SQLite, report list/detail and JSON/HTML/PNG/PDF exports across all four protocols. OpenAI Chat covers streaming, expected rejection, deliberate assertion failure, observation-only results, two warmups and continuation into the next entry. Each video protocol covers submit/poll/terminal output and settings propagation. Reopening the database verifies durable workspace/catalog/report reads.
- `TestProtocolDesktopTransportFailureEndToEnd` covers all four protocols when transport fails. It preserves execution failures and indeterminate/observed counts, reads/exports reports and reopens the database. This caught and fixed disagreement between SQL and Go report-count validation; existing current-format failure reports became readable without data edits.
- Edge browser acceptance against the actual Wails Go backend verified all protocol reports, expanded request metrics/assertions, report search, keyboard Escape, 1440x900 / 1024x768 / 960x640 layouts and light/dark/system themes. UI export generated JSON/HTML/PNG/PDF; only the native save-dialog boundary was captured into temporary files. A run started through the real UI against a closed loopback port persisted seven execution records, displayed four indeterminate Case summaries plus one observation-only summary, and reopened its report correctly. Temporary test channels were removed.
- Screenshots and UI-generated exports were inspected. Wails dev's bundled overlay emits `Cannot read properties of null (reading 'nodes')` and `runtime:ready` diagnostics in the browser bridge; these were recorded separately from application behavior and the pinned Wails version was retained.

Verification used local fixtures and loopback failures, not paid/live provider calls. Native desktop visual automation was unavailable; visual interaction used Edge with the real Wails backend. The packaged executable was separately checked for startup errors.

## Historical installation impact

No user AppData database was reset, deleted or converted. The changed operational baseline and current snapshot/report formats explicitly reject previous installations. Historical operational-data conversion remains outstanding and requires a separately planned, recoverable offline upgrade before those records can be used with this version. The successful authored-file conversion above applies to the tracked repository corpus only. No permanent compatibility reader, migration chain or old executor was added to hide this requirement.
