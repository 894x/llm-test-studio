# Quick task metadata in Suite definitions

A quick task uses an existing Suite identity and its pinned Cases. An optional
`quick_test` object makes the Suite eligible for the quick entry. Adding a task
must not require another task list in the frontend or a new execution switch.

This stage implements definitions, catalog editing, bundled connectivity tasks,
shared durable Suite execution, and the native desktop entry. The execution page and history
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

Starting a desktop run or invoking the CLI executes the selected tasks directly.
There is no separate billing acknowledgement or provider-specific payment policy
in protocol metadata. Target applicability, automatic-member validation, request
limits, timeouts, video concurrency constraints, and dry-run remain execution
controls.

Successful and failed observations, sealed reports, and task provenance use the
same SQLite persistence as other Runs. Native `DesktopApp.StartQuickTask` and
the frontend client's `startQuickTask` return the accepted Run ID. They do not
query the workspace after mutation: progress refresh is a separate query and
its failure cannot disguise an accepted start. Cancellation uses the ordinary
Run command. Native input and applicability errors use stable, translated codes;
underlying errors are not exposed to the frontend.

The desktop quick-entry page lists Suites carrying `quick_test` metadata and
renders their text, number, and boolean inputs. The form accepts a temporary
upstream model and connection or an existing channel without requiring an
authored Model, Channel, or Plan. It shows observed progress, shared cancellation,
local timestamps, report navigation, and the twelve most recent quick task Runs.

Drafts survive page navigation and application reload. Local storage contains
only the task reference/metadata, parameters, model, and a validated connection
address. Rejected URLs (including userinfo, query strings, and fragments) are not
persisted. The API key stays in App memory and is cleared on reload or endpoint
change; existing channels resolve their credential in Core.

After a Run is accepted, an explicit Remember action can save its temporary key
in the OS keyring. Its owner is that operational Run, scoped to the application
storage directory. No authored target or credential metadata file is created.
Only `credential_run_id` is persisted in drafts and replay snapshots. Core binds
the key to the original complete base URL and protocol; other models or Suites
at that connection can reuse it. Historical reads return an available reference,
never the key. Forget removes the key while preserving all Run history. Normal
starts never remember credentials automatically. Remember and Forget can be
retried independently without replaying a test. The performance entry uses the
same Core credential lease and pinned Suite path.

`GetQuickTask` returns allow-listed form data from the stored Run snapshot.
Restoring history includes `source_run_id` when starting again, so Core resolves
the original Suite and Case definitions even if the current catalog changed.
Edits apply to a new Run; neither the original snapshot nor authored files change.
Successful, failed, and cancelled Runs can all be restored.

OpenAI tasks expose the independent performance sheet without a preliminary
connectivity request. Its optional task reference lets Core derive the chat path
from the same pinned Suite definitions and append it using Suite URL semantics.
Performance settings retain their draft, while changing the connection or task
clears prior output and prevents late results from replacing the current view.
Workspace polling does not overlap requests and retries failed report reads even
when the Run has reached a terminal state.

## Bundled connectivity tasks

The bundle currently contains 16 quick tasks. Selection depends only on the
Suite's `quick_test` metadata. Prompt bindings reuse existing enabled automatic
Cases, preserving their other parameters, assertions, and version scope.

| Protocol | Model scope | Cases | Timeout per Case |
|---|---|---|---|
| OpenAI Chat | Generic | Synchronous response (`T001`) | 30 seconds |
| Kimi | Four existing Kimi model targets, one Suite each | Non-stream response and usage | 60 seconds |
| Seedance | Generic, as declared by `V001` | Text-to-video task through terminal output | 10 minutes |
| Wan | Nine existing model targets, one Suite each | Version-specific text-to-video success | 10 minutes |
| MiniMax | MiniMax-H3 | Text-to-video success, missing auth, invalid auth | 10 minutes |

The only editable input in these initial tasks is the prompt. Its default is
the original Case value. MiniMax's existing profile manifest owns its metadata;
the scenario generator preserves that object and checks it for drift. Other
connectivity memberships are authored semantic selections in Suite files.

The bundle test loads actual Cases and Suites through the production catalog
services, checks every protocol and authored model scope has a quick task, and
validates both default and overridden inputs without changing source Cases.
These are static catalog checks, not live provider contract verification.

## Shared validation

`domain.Suite.ValidateCases` owns Suite member, applicability, and input-binding
checks. Catalog mutations, snapshots, historical repository reads, and file
materialization resolve the relevant definitions and invoke that rule.
`internal/jsonpointer` is shared with response probe assertions to keep path
decoding and lookup consistent.
