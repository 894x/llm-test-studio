# KVV API compatibility integration

Scope: Moonshot Chat Completions (`POST /v1/chat/completions`), `kimi-k3` and
`kimi-k3.1-pro`, sync and SSE. Dynamic tools are not included for highspeed:
the dynamic-tools guide lists only these two models. Provider documentation
was checked on 2026-11-01. This is a bounded KVV comparison, not a complete
inventory of the Chat API. The broader K3 and K3.1 matrices remain applicable.

Sources: [KVV at 66092cf](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/tree/66092cf444c97356c0e11c5078c67116390615d9),
[Chat](https://platform.kimi.com/docs/api/chat),
[dynamic tools](https://platform.kimi.com/docs/guide/use-dynamic-tool-loading),
[tool choice](https://platform.kimi.com/docs/guide/use-tool-choice),
[response format](https://platform.kimi.com/docs/guide/response_format),
[models](https://platform.kimi.com/docs/api/models-overview), and
[OpenAPI](https://platform.kimi.com/docs/openapi.json).

Each new Case is non-default and runs against the model bound by the operator.
Except for the attributed token fixtures, inputs are project-authored and synthetic. `core` suites contain documented
claims; `observed` suites contain KVV expectations that are not established as
documented contracts. A 400 in those suites means KVV-compatible rejection,
not necessarily an independently documented provider requirement. T1 denotes
short successful requests, T2 admission-rejection probes. No provider execution
is implied by membership.

The `parameters` suites reuse the existing model-specific parameter, reasoning
and JSON cases. `schema` isolates the strict flat-schema samples, while `token`
is a separate K3 upstream-baseline observation. The token fixtures are attributed
in `data/cases/PROVENANCE.md`; unlike the other new cases they are adapted from
upstream test data. The `partial` suites retain exactly eight original KVV Partial
SSE requests with project-owned continuation-format assertions, shared by K3
and K3.1 Pro entries; see the
[bounded Partial matrix and compatibility notes](kvv-partial-coverage.md).

| KVV area | Required partitions and assertions | Existing or planned cases | Tier / status |
|---|---|---|---|
| Dynamic messages | system declaration after user; callable tool; sync and SSE with assembled name, arguments, finish and DONE | existing `dynamic_tool`, `k31.dynamic_tool`; new `kvv.dynamic_system_*` | T1 partial; basic call implemented, full position/schema matrix remains |
| Dynamic message roles | user and assistant carry tools, reject separately | `kvv.dynamic_role_*` | T2 KVV-observed |
| Dynamic tool shape | missing type/function/name/parameters; bad type; non-array; null item; invalid name | `kvv.dynamic_*` | T2 KVV-observed; description conflict not asserted |
| Duplicate names | same message, across dynamic messages, global plus dynamic | `kvv.dynamic_duplicate_*` | T2 KVV-observed |
| `tool_choice` | required with absent or empty tools; invalid enum | `kvv.tool_choice_*`; existing auto/none/required | T2 KVV-observed; named form remains documented conflict / upstream skipped |
| `response_format` | missing schema envelope, non-boolean strict; valid strict schema including required/type/no extra fields | `kvv.format_*`; existing invalid type and missing name/schema | T1/T2; simple strict shape only |
| Tool argument Schema | callable strict tool, JSON object with exactly required property and correct type; sync/SSE | `kvv.tool_schema_*` | T1 partial; selected flat schema only; general MFJS corpus blocked on complete JSON Schema evaluator and fixture rights |
| Stream handling | same claims in sync and SSE; reject malformed input as HTTP 400 even with stream requested | paired `*.sync` / `*.stream` | T1/T2; no byte-for-byte generation equivalence claimed |
| Parameters | fixed K3 settings and boundary rejection | existing K3/K3.1 parameter cases | T1/T2 existing; no duplicated copies |
| Thinking | current top-level `reasoning_effort` valid and invalid values | existing K3/K3.1 reasoning cases | T1/T2 partial; legacy `thinking.effort` and model self-report are observations, not current documented contract |
| Preserved thinking | carry complete returned assistant message across calls | existing matrix runner gap | blocked pending lossless replay |
| Prompt token groundtruth | source request and tokenizer/template version must match; stream placement and permitted deviation must be defined | `kvv.prompt_tokens_assistant_hello.stream` | T1 KVV-observed: one upstream fixture, inclusive 99–102, checks top-level or choice-level usage; template/model drift remains possible. Other non-Partial fixtures deferred; KVV's separate 539 test is skipped upstream |
| Partial continuation format | eight original content/reasoning, absent/empty-field and tool/effort partitions; SSE, non-blank text without a repeated non-empty content prefix, or valid tool call with JSON object arguments | `kvv.partial_*.stream`, K3 / K3.1 Pro opt-in Partial Suites | Eight format Cases implemented; T0 fixture evidence. No token comparison, fixed-answer guarantee, hidden-reasoning proof or non-stream parity claim |
| Benchmark, image fixtures, load and long context | scoring / datasets or expensive execution | outside this integration | deferred / separate workflow |

`response_format.strict=false` promises valid JSON object, not compliance with
every schema field. Generic nested/recursive Schema validation and complete
upstream KVV parity are not claimed. Provider-backed execution and expenditure
require a separate explicit run request.

The token cases assert the final retained non-null top-level or first-choice
usage and require `[DONE]`; KVV stops reading on the first usage. Conflicting
usage values across chunks therefore need separate investigation and are not
claimed to be evaluated identically.

Compatibility: existing Case IDs, Suite memberships and stored reports are
unchanged. The streaming parser now retains a previously observed non-null
usage object when a subsequent chunk carries `usage: null`, both at the top
level and within a choice. This affects observations of new runs only; it does
not recompute historical reports. The change adds no database migration.
