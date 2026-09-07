# Quick task metadata in Suite definitions

A quick task uses an existing Suite identity and its pinned Cases. An optional
`quick_test` object makes the Suite eligible for the quick entry. Adding a task
must not require another task list in the frontend or a new execution switch.

This stage implements the definition, validation, catalog DTO, and editing
round trip. The quick-test execution page, bundled task profiles, and history
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

Authored definitions remain in files. Future execution must record effective
inputs alongside their source revisions in operational history and validate
the resulting Case specs before execution. It must not silently label an edited
request as an unchanged authored definition. Credentials stay outside these
definitions and task parameters.

## Shared validation

`domain.Suite.ValidateCases` owns Suite member, applicability, and input-binding
checks. Catalog mutations, snapshots, historical repository reads, and file
materialization resolve the relevant definitions and invoke that rule.
`internal/jsonpointer` is shared with response probe assertions to keep path
decoding and lookup consistent.
