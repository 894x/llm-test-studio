# Quick task metadata in Suite definitions

A quick task uses an existing Suite identity and its pinned Cases. An optional
`quick_test` object makes the Suite eligible for the quick entry. Adding a task
must not require another task list in the frontend or a new execution switch.

This stage implements definitions, catalog editing, and shared durable Suite
execution. The quick-test execution page, bundled task profiles, and history
integration remain tracked in [the delivery plan](task-entry-structure-plan.md).

## Definition

The following fragment assumes a Suite member named `connection.chat` whose
Case spec has a string at `/request/body/messages/0/content`:

```json
{
  "quick_test": {
    "description": "Send one message and validate the response.",
    "timeout_ms": 30000,
    "inputs": [
      {
        "key": "prompt",
        "label": "Message",
        "type": "text",
        "default": "Say hello.",
        "bindings": [
          {
            "case_key": "connection.chat",
            "pointer": "/request/body/messages/0/content"
          }
        ]
      }
    ]
  }
}
```

- `description` is authored display text. `timeout_ms` is a positive integer
  no greater than 3,600,000.
- `inputs` is required. Use `[]` for a fixed task with no editable values.
- Each input has a unique safe key, a label, a type (`text`, `number`, or
  `boolean`), a default of that type, and one or more bindings. Defaults are
  explicit task parameter values; they do not modify the authored Case.
- Bindings address existing scalar fields in the member Case's
  `definition.spec`, using JSON Pointer. Only fields below `/request/body/`
  are editable. The top-level body `model` field is reserved for target
  selection. Headers, endpoint paths, assertions, and runner options cannot
  be parameter bindings.
- Every bound field must exist in the pinned definition and match the input's
  type. A field can have only one input binding. Array indices use digits with
  no leading zero except `0`, so alternate index spellings cannot alias a field.
- Quick task members must be enabled automatic Cases. A normal Suite can
  still contain manual or disabled definitions according to existing rules.

## Applicability and revisions

Normal Suites retain their explicit `model_target`. A quick Suite may use an
empty target only when its protocol does not require versioned model targets
and all member Cases are generic. Version-scoped video protocols retain their
model constraints. Cases must match the Suite protocol and exact pinned
identities and revisions in both current catalog reads and historical reads.

The optional object is omitted from JSON when absent, preserving the canonical
shape and revision hash of existing Suite documents. Quick metadata participates
in the revision of a quick Suite and survives file saves, historical revisions,
catalog snapshots, and ordinary Suite edits. DTOs own their nested values;
editing a snapshot cannot mutate catalog state.

Case edits through the desktop repository validate every affected active Suite
before writing the Case or any Suite revision sidecar. An edit that disables a
quick member, changes its applicability, or breaks an input binding is rejected
while the current catalog remains readable. Compatible edits still archive the
previous Suite revision. Uncertain-write recovery compares all authored Suite
fields, including quick metadata, before reporting that a save succeeded.

Authored definitions remain in files. Execution records resolved inputs alongside
the source Suite and original Case revisions in operational history. It validates
the resulting Case specs before execution and runs copies with the resolved
values. Credentials stay outside these definitions and task parameters.

## Shared execution

`runs.PrepareQuickTask` accepts an exact Suite ID/revision, an upstream model
identifier, resolved/overridden task inputs, and either a saved channel ID or a
temporary endpoint/API key. Saved-channel and temporary connection fields are
mutually exclusive. `StartQuickTask` also activates the durable queued Run.

Preparation does not write model, channel, or Plan files. A transient Plan and
mapping live inside the existing v2 Run snapshot; the Plan identity is the Run
identity. Optional `quick_task` provenance includes the Suite, every effective
input including defaults, and the saved channel ID when used. Case definitions
in the snapshot retain their authored values. A temporary API key is held only
in a lease closed by the shared lifecycle; no keyring entry is created.

The router runs members sequentially in Suite order. Plan-scheduled members
receive one request each; Case-scheduled members keep their own sample schedule.
Request IDs are distinct across members and time offsets share a Run origin.
The workspace therefore labels these records `source: "quick_task"` and reports
observed request counts with `planned: 0`; the member count is not a request
budget. Original Plan progress contracts remain unchanged.

Both CLI and desktop preparation use the protocol registry's task-count billing
policy. Seedance batches require acknowledgement; Wan and MiniMax require it for
every nonempty selection. The desktop prompt uses generated metadata and keeps
unresolved historical members in its conservative acknowledgement count.

Successful and failed observations, sealed reports, and task provenance use the
same SQLite persistence as other Runs. The new task API is currently an
application-service capability. Native bindings, quick-entry UI, drafts, replay,
and explicit credential remembering remain separate delivery work.

## Shared validation

`domain.Suite.ValidateCases` owns Suite member, applicability, and input-binding
checks. Catalog mutations, snapshots, historical repository reads, and file
materialization resolve the relevant definitions and invoke that rule.
`internal/jsonpointer` is shared with response probe assertions to keep path
decoding and lookup consistent.
