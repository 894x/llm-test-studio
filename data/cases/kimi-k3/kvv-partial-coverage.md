# KVV-derived Partial continuation format checks

Scope: the eight KVV Partial request partitions for Chat Completions SSE,
explicitly selected with `kimi-k3` or `kimi-k3.1-pro`. These Cases now check
continuation **format**, as requested on 2026-11-11. They do not compare token
counts or require a particular generated answer. No new live execution is
implied by this catalog update.

The [official Partial guide](https://platform.kimi.com/docs/api/partial),
retrieved on 2026-11-11, puts `partial: true` on the final assistant message,
returns the suffix without the input prefix, and warns against mixing Partial
with `response_format.type=json_object`. The reasoning-field combinations are
KVV-derived exploratory partitions, not separately documented guarantees.

## Contract inventory and selected partitions

Every request remains equal to its original record in
[`cases.partial.jsonl` at 66092cf](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/blob/66092cf444c97356c0e11c5078c67116390615d9/testdata/prompt_token_cases/cases.partial.jsonl),
with only `stream=true` added. Roles, text, absent/empty `content` and
`reasoning_content`, `reasoning_effort`, tools, tool schema and
`tool_choice=required` are preserved. Model, URL and credentials are bound by
the Run. No sampling, output-budget or usage-request parameter is added.

All Cases require HTTP 200, no protocol issues, `[DONE]`, and a normal finish
(`stop` for text, `tool_calls` for tools). Text Cases require at least one
non-whitespace character in the returned content. When the input has a
non-empty content prefix, the returned content must not contain that literal
prefix again. Empty or absent content prefixes do not produce an always-failing
empty-string exclusion. Tool Cases permit empty text when at least one valid
CodeRunner function call is returned, with arguments decoding to a JSON object.

| KVV source record | Case directory (`openai-chat/`) | Preserved input partition | Observable format assertion |
|---|---|---|---|
| `k3_partial_partial_assistant` | `KVV-partial-assistant-stream` | content only | non-blank text; no repeated `incomplete` |
| `k3_partial_partial_think` | `KVV-partial-reasoning-content-stream` | reasoning and content | non-blank text; no repeated `incomplete` |
| `k3_partial_partial_think_empty_content` | `KVV-partial-reasoning-no-content-stream` | reasoning; content absent | non-blank subsequent text |
| `k3_partial_partial_think_empty_reasoning` | `KVV-partial-empty-reasoning-stream` | empty reasoning; content present | non-blank text; no repeated `incomplete` |
| `k3_partial_partial_think_no_response` | `KVV-partial-reasoning-only-stream` | reasoning; content absent | non-blank subsequent text |
| `k3_partial_tools_with_thinking_effort_partial` | `KVV-partial-tools-max-stream` | tools required; max effort | valid tool call; no repeated content prefix |
| `k3_think_partial_think_no_response` | `KVV-partial-empty-content-stream` | reasoning; empty content | non-blank subsequent text |
| `k3_think_tools_with_thinking_effort_partial` | `KVV-partial-tools-high-stream` | tools required; high effort | valid tool call; no repeated content prefix |

All eight are enabled, non-default, automatic T1 Cases. Implemented format
coverage has T0 fixture execution evidence. Exact answers, semantic relevance,
visible or hidden reasoning-prefix preservation and tool-code execution are
outside this scope. In particular, a non-blank answer without a content prefix
does not prove that the reasoning prefix was honored. General API parameter
boundaries and SSE/non-stream parity for these eight requests remain outside
this bounded catalog.

## Token data and attribution

The [original records](kvv-partial.jsonl), including upstream token baselines,
remain unchanged for source attribution under the retained
[MIT notice](KVV-MIT-LICENSE.txt). They are not runtime verdict conditions.
The separate `kvv.prompt_tokens_assistant_hello.stream` tokenizer/template
comparison is outside the Partial catalog and remains unchanged.

## Suites, Plan and compatibility

The K3 and K3.1 Pro Partial Suites share the eight stable Case IDs. The combined
`kimi.partial` Suite and its existing Plan still resolve the same 18 unique
Cases: ten JSON format/control Cases and these eight continuation format Cases.
Two of those ten are no-Partial controls; the overall count is not a Partial
semantic-support rate.

Published v0.2.0 formats and consumers are unchanged: Case schema 2/3, Suite
schema 1, Plan, CLI/API, desktop/runner protocols and persisted data keep their
contracts. This revises the unreleased Partial catalog's assertion policy,
not the provider requests or stable Case/Suite/Plan identities. Existing frozen
snapshots and historical verdicts are not rewritten or regraded. Compare newly
created runs under the new policy separately from earlier token/value runs.
No migration or new runner feature is needed; these eight use existing
operators. The combined Suite still needs `json_prefix_assertions` for its JSON
members. Downgrading should use the older catalog; historical snapshots still
need an execution/reporting build that understands their original operators.

## Validation scope

Fixture tests verify all eight outgoing requests against the retained source,
fragmented SSE text/tool arguments, acceptance with absent usage or arbitrary
integer token counts, rejection of blank or repeated text, invalid tool calls,
wrong HTTP/finish status, malformed events and missing `[DONE]`. A catalog guard
checks all 18 Partial Cases for token and fixed-JSON-answer assertions.
Suite generation checks and built-in Plan tests verify unchanged identities,
ordered memberships and one execution per Case. Provider-backed execution has
not been repeated for this policy change.
