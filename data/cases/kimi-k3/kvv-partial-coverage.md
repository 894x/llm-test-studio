# KVV Partial prompt-token fixtures

Scope: integrate exactly the eight records from Moonshot KVV
[`cases.partial.jsonl` at 66092cf](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/blob/66092cf444c97356c0e11c5078c67116390615d9/testdata/prompt_token_cases/cases.partial.jsonl)
for Chat Completions, SSE only. K3 and K3.1 Pro have separate opt-in Suite
entries referencing the same eight Cases. The original K3 token baselines
are retained for comparison; they are not independently verified K3.1 Pro
baselines. No non-stream, JSON continuation, or semantic reasoning-prefill
claim is added by these fixtures.

The [official Partial guide](https://platform.kimi.ai/docs/api/partial),
checked on 2026-11-11, places `partial: true` on the final assistant message
and warns against combining it with `response_format.type=json_object`.
None of these requests includes `response_format`. The optional/empty
reasoning combinations and exact token counts below are KVV observations,
not additional guarantees inferred from that guide.

## Inventory and selected scenarios

Each request retains its original two messages, roles, text, field presence,
`partial: true`, and `reasoning_effort`. The two tool requests also retain
`tools[].type`, `function.name/description/parameters`, all nested property
types, the `language` enum, and `tool_choice=required`. Only `stream=true`,
which KVV applies at execution time, is added. The Run supplies the model,
URL, and credential. No output budget, sampling setting, or `stream_options`
is inserted into the golden request.

All eight Cases are enabled, non-default, automatic, and explicitly selected
T1 token observations. They require HTTP 200, a well-formed stream ending in
`[DONE]`, and an integer `prompt_tokens` within `[expected, expected+3]` at
either top-level usage or first-choice usage. A 200 alone does not pass.
No output-text or tool-execution semantic predicate is claimed.

| Source record ID | Case directory (`openai-chat/`) | Request partition | Golden / accepted range |
|---|---|---|---|
| `k3_partial_partial_assistant` | `KVV-partial-assistant-stream` | content only | 91 / 91–94 |
| `k3_partial_partial_think` | `KVV-partial-reasoning-content-stream` | reasoning and content | 99 / 99–102 |
| `k3_partial_partial_think_empty_content` | `KVV-partial-reasoning-no-content-stream` | reasoning `你`, content absent | 90 / 90–93 |
| `k3_partial_partial_think_empty_reasoning` | `KVV-partial-empty-reasoning-stream` | empty reasoning, content present | 91 / 91–94 |
| `k3_partial_partial_think_no_response` | `KVV-partial-reasoning-only-stream` | reasoning `reasoning`, content absent | 91 / 91–94 |
| `k3_partial_tools_with_thinking_effort_partial` | `KVV-partial-tools-max-stream` | tools required, max effort | 234 / 234–237 |
| `k3_think_partial_think_no_response` | `KVV-partial-empty-content-stream` | reasoning present, empty content | 91 / 91–94 |
| `k3_think_tools_with_thinking_effort_partial` | `KVV-partial-tools-high-stream` | tools required, high effort | 235 / 235–238 |

The repository-owned [original records](kvv-partial.jsonl) preserve all source
IDs, requests, descriptions and baselines for offline regression. See
[provenance](../PROVENANCE.md) and the retained MIT notice for redistribution.

## Observation boundaries

The product reads the complete stream and asserts the final retained non-null
usage, while upstream KVV stops on its first usage. The product additionally
requires `[DONE]` and no protocol issues. Conflicting usage snapshots are not
claimed to have identical first/last interpretation. These are the same
boundaries as the existing KVV `assistant_hello` integration.

Token failures can reflect tokenizer, prompt-template, or model differences;
they alone establish neither billing errors nor lack of Partial support.
Semantic prefix continuation, JSON syntax/value preservation, and inference
about the hidden upstream model remain outside these eight token fixtures.
Other KVV token fixtures and complete API-boundary coverage remain deferred.

## Compatibility and release impact

This is an additive built-in catalog change relative to published v0.2.0:
eight new stable Case IDs and two new opt-in Suites. Existing Case IDs,
Suite memberships, Plan seeds, defaults, and user overrides are unchanged.
Case schema 3, Suite schema 1, CLI/API, desktop/runner protocols, persisted
snapshots, reports, and databases retain their existing formats. No migration,
deprecation, or removal is required; historical conclusions are not recomputed.

Users upgrading to a build containing this catalog can select either new
Partial Suite and bind the appropriate model. Older binaries lack the new
built-ins: before downgrading, remove new Suite references from separately
saved plans or retain compatible user copies of the new Cases and Suites.
No database restoration or data-format conversion is needed for this change.

## Validation

Passed T0 checks on 2026-11-11:

- `go test ./data/cases ./data/suites ./internal/casecodec ./internal/protocols/openaichat ./internal/application/suitecatalog`:
  catalog/schema loading, bundled references, and protocol assertions.
- The new Partial tests exercise all eight outgoing golden requests unchanged,
  both usage locations, inclusive bounds and their adjacent failures, invalid
  types, missing usage, malformed events, missing `[DONE]`, and HTTP rejection.
- `go test ./internal/application/plancatalog ./internal/persistence/workspace -run TestBuiltin`:
  existing built-in Plan loading and per-Case scheduling remain valid.
- Suite generator preview, write, and `--check`: two Suites, eight shared Cases
  each; its seven Python unit tests also passed.
- Direct comparison with all eight original KVV records, non-default/ID checks,
  new-file whitespace checks, and `git diff --check` passed.

No provider request or paid execution was performed by this catalog integration.

The separately added [JSON Partial catalog](json-partial-coverage.md) validates
syntax and values after prefix concatenation in both sync and SSE. It has its
own Suites and does not alter these eight token fixtures or their memberships.
The combined **Kimi · Partial 综合回归** entry (`kimi.partial`) references all
ten JSON Cases followed by these eight token Cases, once each; choose the model
at run time. The dedicated token Suites retain their original memberships.
