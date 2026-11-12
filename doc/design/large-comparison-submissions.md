# Large comparison submissions

The desktop and runner keep the current complete-job contract (version 1). A comparison
still freezes each channel independently, preserving its plan, case definitions and
execution identity. This change expands acceptance bounds without rewriting historical
snapshots, authored plans, reports or database schemas.

## Capacity

The previous 8 MiB document and 32,768 owned-item budgets were aggregate limits across
all channels. Complete multi-item plans could exhaust either bound before a job was
created. Complete submissions now accept up to 128 MiB and 524,288 owned items, providing
sixteen times the previous capacity. Per-case validation, collection bounds, secret
limits, depth limits and the UI selection limit of sixteen channels remain enforced.
These are finite resource bounds, not an unlimited allocation policy.

The submission reconciliation page retains its 16 MiB aggregate budget, but allows one
valid larger record as the first item. This prevents an empty, nonadvancing page without
packing multiple large documents into one read. Local acceptance, HTTP transport, SQLite readers and event envelopes
consume the same shared document limit.

Oversized documents or owned collections produce the public desktop error
`job_submission_too_large`. They must not be reported as an unavailable execution location.
Secret-bearing envelope overflow also preserves the size error rather than converting it
to an invalid submission error.

## Compatibility

Previously supported contract-1 inputs remain supported. There is no migration, deprecated
alias or removal schedule. Older runner binaries retain their old capacity, so update the
execution node before sending a larger job. This change updates the local desktop only;
it does not deploy remote nodes. No new wire fields or protocol version are introduced.

An older binary may reject a newly created oversized record on read. Use a matching new
binary for workspaces containing such records. If an older binary is required, restore a
verified pre-change workspace backup and matching binary; post-backup writes will not be
present. Do not delete job records to force a downgrade.

## Verification

A regression fixture freezes and decodes sixteen channels with five plan items and 121
cases each (about 17 MiB), checking that every item and case survives and the digest is
unchanged. The fixture exceeds both old aggregate budgets. Existing byte-boundary,
collection-boundary, persistence paging and HTTP submission tests continue to enforce
rejection above the expanded limits. Desktop boundary and frontend tests cover the new
public error code. These tests do not send paid provider requests.

## Submission processing

Successful protocol-definition checks are memoized by protocol and the exact current
content fingerprint (1,024 keys). Parsed case specs use a bounded cache (1,024 entries,
32 MiB charged budget); returned inputs, assertions and workflows are deeply cloned.
Rejected input is never cached, and edited bytes cannot inherit old validity.

Job specs carry process-local validation proofs tied to a structural fingerprint of all
exported current content, including native JSON bytes and metadata. This bookkeeping
fingerprint is not persisted and does not replace the published canonical job digest.
It detects edits without re-encoding the full plan. Unchanged internal transfers reuse
private frozen domain Run entities and canonical encoding. Any changed content falls
back to complete validation. Public mutable results still receive owned copies.

Parsed specs and verified submission documents each use separate bounded caches (32
entries, 128 MiB charged budget). Cache hits never establish database existence: row
identity/state columns are still checked, and returned payloads are independently owned.
Canonical encoding proofs retain at most 256 content fingerprints and no payloads.

Digests retain the original UseNumber canonical algorithm and integer precision. Existing
storage formats, schema versions and wire contracts are unchanged. Fixed submission and
acceptance envelopes are assembled from canonical children. Acceptance reuses canonical
run snapshots from the same validated job instead of reconstructing them through generic
JSON maps. Immutable Run metadata is read without cloning its complete snapshot.

Submission successors validate metadata and the complete immutable preparation before
acquiring the write transaction. The final compare-and-swap checks the observed revision,
canonical document and every denormalized identity/state column. State transitions retain
the previous canonical prepared payload and update only delivery metadata. Concurrent
updates, malformed receipts and changed preparations are still rejected.

## Local diagnostic measurements

An isolated replay of the real three-channel preparation originally took about 9.3s to
prepare and 28.6s to submit. A sixteen-channel expansion was reduced from approximately
18.9s submission time during early optimization to around 4.3s in a later run. This is not
a one-second guarantee: a memory-constrained diagnostic run took about 6.4s, with roughly
4.1s in executor admission and 0.9s per outbox state update. Provider execution was replaced
with a fake and no upstream requests were sent.

The sixteen-channel input is about 18.3 MiB. Its job input, delivery document, independent
run snapshots/documents and accepted event occupy about 91 MiB logically in the database,
before state-transition write amplification. Remaining latency is dominated by storing
these complete historical artifacts. Reducing those storage copies would be a separate
persisted-format design and is deliberately outside this optimization.

The running client was left intact throughout profiling. Loading the candidate requires
an explicitly authorized local client restart; no production service or remote node is
restarted by this work.
