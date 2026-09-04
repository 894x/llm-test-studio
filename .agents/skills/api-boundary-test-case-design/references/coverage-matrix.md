# Contract coverage matrix

Create this matrix before naming or implementing cases. One row may expand into several cases, but every case should map back to one primary row and scenario.

| Field | Meaning |
|---|---|
| Contract area | Endpoint, header, request field, response field, or workflow |
| Parameter/path | Exact JSON path or protocol element |
| Source | Direct primary-document URL and retrieval date |
| Type/shape | Scalar, enum, object, array, union, message sequence, or stream |
| Presence | Required, optional, conditionally required, forbidden, or deprecated |
| Default | Documented behavior when omitted |
| Valid partitions | Semantically distinct accepted classes |
| Invalid partitions | Wrong type, unknown enum, malformed shape, forbidden state, or unsupported combination |
| Boundaries | Min/max, fixed value, length/cardinality, token/context budget, or rate limit |
| Dependencies | Requires, excludes, implies, or changes another parameter |
| Expected result | Status/error type plus stable business assertion |
| Model/version scope | Exact provider model, API version, or feature gate |
| Execution tier | T0, T1, T2, or T3 |
| Existing cases | Case IDs that already prove this row |
| Gap/status | Covered, partial, missing, deferred, blocked, or documentation conflict |

## Case derivation rules

### Ordered ranges

For inclusive `[a,b]`, start with `a-1`, `a`, one ordinary interior value, `b`, and `b+1`. Use a domain-aware predecessor/successor: one byte for byte limits, one token for token budgets, the smallest supported unit for time, and a representable epsilon only when the API defines floating-point precision.

### Fixed values

For a value that is accepted only at `v`, derive omission/default, explicit `v`, one type-valid non-`v` value, and a wrong type when the boundary layer promises validation. Do not generate `v-1` and `v+1` mechanically if the domain is not ordered or the provider only promises that any other value is rejected.

### Enumerations

Cover all values only when each has distinct behavior or the set is small. Otherwise select one representative per behavior partition. Always separate omitted/default from an explicit default value when serialization or gateway transformation could differ.

### Length and cardinality

For maximum `n`, prefer `0`, `1`, `n`, and `n+1`. Add `n-1` when off-by-one risk is material. If the contract is byte-based, include multibyte Unicode that has a different character count.

### Dependencies and combinations

Write a decision table first. Mark impossible combinations, expected accepted rows, and each distinct rejection rule. Apply pairwise reduction only after mandatory rows are preserved. Pairwise coverage must not remove a documented boundary or dependency row.

### Stateful flows

Model states, valid transitions, invalid transitions, and reset/retry behavior. For streaming, tool calls, uploads, or asynchronous jobs, assert intermediate and terminal protocol states separately.

## Assertions

Prefer stable contract assertions: transport status or SSE termination; provider error type/code without brittle full-message matching; required response shape and semantic predicate; documented finish reason or state transition; usage fields when the claim concerns accounting; and absence of forbidden behavior.

Avoid accepting any 2xx, merely parseable JSON, or task completion when the case's business claim is more specific.

## Audit summary

Report totals by covered/partial/missing/deferred/conflict matrix rows; positive, negative, dependency, state, and robustness cases; T0/T1/T2/T3 tier; enabled/default/disabled status; and model/API-version scope.

Coverage is scoped to the documented matrix. Unknown or undocumented provider behavior remains an explicit limitation.
