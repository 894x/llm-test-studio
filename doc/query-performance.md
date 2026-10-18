# Desktop query performance and retained navigation

The desktop now reads bounded history windows and retains visited workspaces. Run and Quick Test history load 25 records per cursor page and append at the end of the scroll viewport. Run filters apply to loaded records, as stated in the UI. Navigating away preserves the loaded records, cursor, filters, selection, inspector tab and scroll position. Hidden workspaces stop their polling effects.

| Surface | Query behavior |
| --- | --- |
| Runs | Pages standalone runs separately from Jobs; fetches child summaries only for loaded Jobs. Polling refreshes unsettled Jobs and active run IDs, in batches of at most 100. |
| Jobs | Limits each local/remote candidate source before merging. Budget checks inspect the event documents actually needed by the summary rather than every historical event. |
| Comparisons | Reads 25 summaries plus explicitly requested IDs (at most 100). Loads scalar report fields instead of complete run/report documents. Report-ready events also refresh affected older loaded groups. |
| Quick Test | Refreshes the first page and affected/active IDs, preserving older pages. It no longer walks every loaded page on each refresh. |
| Reports | Keeps the existing bounded report contract and uses the history index instead of sorting the entire report table. |
| Authored catalogs | Reuses validated Case/Suite/Plan parses by content hash. Returned full definitions remain independent copies. Metadata counts avoid cloning complete definitions. |
| Overview | Returns catalog counts, run/report counts, at most one active run and four recent reports. Active polling reads that run's summary rather than rebuilding the catalog and report list. |

Refresh failures retain displayed data and offer retry. Targeted run/report refresh failures retry while visible. Content hashing still reads authored files to detect external replacements, including edits that retain timestamps and size; this is not an elimination of all catalog filesystem I/O. Exact global database counts also retain a cost proportional to index/table size. Loaded frontend rows remain in memory to satisfy navigation retention.

## Compatibility

Existing full query methods remain available; new bounded desktop methods ship with their frontend consumers. CLI and remote-runner protocols are unchanged. Database schema version 3 appends indexes without rewriting released migration identities or changing stored report conclusions. See [Compatibility and data upgrades](compatibility-policy.md) for backups, supported source versions, recovery and downgrade requirements.

Runtime upgrades create and verify a recoverable backup, migrate transactionally and validate facts before normal writes. Historical fixtures cover the released unversioned formats, schema versions 1 and 2, skipped-release upgrades, repeated opening and rejected/corrupt/newer data. Do not open an upgraded database with an older binary; restore the matching pre-upgrade backup to downgrade.

## Verification on 2026-10-18

- The original mixed-plan `sla_stop` test intermittently failed at the unchanged `e72dc1fc` baseline (`requests = 5, want 1`). A diagnostic rerun recorded `e2e_p95_ms = 0 ms`, which correctly passes its old `0.000001 ms` upper limit. The test now supplies deterministic measured E2E values after real protocol execution, retaining the HTTP, run-host, SQLite and report paths. It covers above-limit stop/continue policies and equal/below-limit success, and checks persisted metrics and conclusions separately from functional assertions. This changes only test fixtures, not runtime SLA rules, stored data or database schema.
- After that fix, `go test ./...` passed without exclusions, including SQLite (245.917 s), and `go vet ./internal/persistence/sqlite` passed. All ten mixed-plan scenarios passed 50 consecutive rounds on the final source (76.554 s). Temporary build mutations that treated equality as failure or bypassed the host's SLA stop check were both detected. An earlier repetition run alongside the full suite had one performance-checkpoint report validation failure in `sla_continue`; it did not recur in 50 diagnostic rounds or the final 50 rounds. Its cause remains unconfirmed, so these results do not establish that every performance-checkpoint timing issue is resolved.
- Focused tests cover cursor boundaries and duplicates, selected versus off-page corruption, frozen comparison child identity, content cache invalidation/isolation, metadata counts, binding validation and lifecycle cancellation. Query-plan assertions verify history index range seeks and the quick-run predicate index.
- All 893 frontend tests passed (`vitest run --testTimeout=15000`; the cross-page language test can exceed the default 5-second limit on this host). Tests cover retained filters/selection/scroll, active-ID polling, hidden-page suspension, report-ready updates and transient refresh failures. TypeScript build and lint were run; the existing `entry-case-table.tsx` effect warning remains outside this change.
- Wails CLI/module version 2.15.0 was verified, bindings regenerated, desktop built, and desktop/query packages vetted.
- A separate Wails process opened an isolated schema-v2 SQLite database with 44 runs and 88 performance reports. Startup produced a verified upgrade backup. Via its real Wails bridge, Runs loaded 25 then 44 records; returning retained search `Fixture` and scrollTop 1013. Quick Test loaded 25 then 50 and retained scrollTop 2280 on return. Overview displayed the preserved 44/88 counts and only four recent reports.
- Rendered browser checks used 1440 x 900, 1024 x 768 and 960 x 640 with light, dark and system appearance. No horizontal page overflow was observed. The Wails development browser inspector emits a `wails/ipc.js` null `nodes` error; the tested application queries and interactions still work. Native window visuals were not independently automated.

These checks establish bounded reads and navigation behavior, not a production latency percentage or a paid-provider execution benchmark.
