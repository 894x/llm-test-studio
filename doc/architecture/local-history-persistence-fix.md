# Local execution persistence during history refresh

2026-10-17 incident: Run `746a0bb4-9899-4f21-9408-ac201f619c4b` stopped with
`result_persistence_failed` / `persist_suite_result`. Only one request result
was saved; its expected HTTP 400 assertion passed. The enclosing Job remained
running and unsettled. The original Run and report are retained as evidence.

## Cause and reproduction

The desktop composition gave history queries and local execution the same
SQLite Repository, which has a single connection. Workspace projections hold
that connection through their read transaction, including history aggregation
and validation. Result persistence has a five-second storage deadline.

An isolated SQLite backup of the incident profile (251,670,528 bytes) reproduced
the failure without sending provider requests or modifying the original data:

| Arrangement | Workspace history read | Result append |
| --- | --- | --- |
| Shared repository | 6.041 seconds | 5.000 seconds; `begin result append: context deadline exceeded` |
| Existing independent read-only view | 6.112 seconds | 16.6 milliseconds; success |

These measurements establish connection starvation for the incident workload.
The initial production error detail was unavailable because the new local Core
composition did not forward the existing diagnostic callback. Consequently,
the reproduction identifies the cause without claiming to recover a missing
original log entry. The single-connection pool predates the remote refactor;
the refactor's desktop composition omitted the read isolation already used by
the runner and dropped diagnostic propagation.

## Fix and verification boundary

- Workspace, reporting, and Job UI queries use the existing WAL-backed,
  read-only Repository view. Execution, report generation, submission and
  control writes retain the writer. Query leases drain before the view and
  writer close; failed initialization also releases the view.
- Desktop diagnostics now pass through Core and RunExecutor to the Run Host,
  preserving the original persistence error and execution identity.
- A held read snapshot regression checks result persistence and terminal Run
  state while the reader remains open. A Core regression injects a persistence
  error and checks the original diagnostic after shutdown drains callbacks.
- The desktop production regression runs the authored 84-case K3 suite with a
  local mock provider while refreshing history. It checks persisted case
  summaries, report creation and terminal, settled Job state. Mock assertions
  do not constitute a new paid-provider compatibility assessment.

The incident snapshot also passed the production initializer/launcher test
with concurrent workspace, report and Job-list refreshes: 84 case summaries,
one report, completed Run/Job and settled Job, with no persistence diagnostic
(59.14 seconds, 85 mock HTTP requests). The permanent empty-profile version
passed in 15.55 seconds; it verifies integration, while the held-transaction
test provides the deterministic isolation check. The jobhost, SQLite and
desktop package tests and targeted `go vet` all passed.

The change does not alter API/data formats or database schema and requires no
historical-data conversion. Failed historical runs are not replayed or relabeled
as successful. Existing startup recovery may settle an interrupted Job while
retaining its original Run/report evidence. In the incident copy, recovery
changed the enclosing Job to cancelled/settled and preserved the failed Run's
`persist_suite_result` failure. No server upgrade is required for
this desktop connection-isolation fix.
