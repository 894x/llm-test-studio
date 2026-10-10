# Kimi K3 contract coverage matrix

Last verified against the first-party Kimi documentation on 2026-09-30.

This matrix distinguishes runnable contract cases from deferred cost-heavy or runner-limited work. A case being present means the request and assertion are defined; it does not mean the paid provider call has been executed.

This document describes the Chat Completions contract. The native K3 Responses adaptation is recorded separately in [responses-coverage-matrix.md](responses-coverage-matrix.md): 39 existing Case IDs are reused, 16 have native rejection expectations, and 45 remain Chat-only. Responses choices now follow the official native enum (`auto`), with `required`/`none` rejection confirmed by the official run on 2026-10-10. Native output maximum and budget truncation are separate from Chat expectations; the remaining three documentation conflicts are recorded there. Optional Responses Schema `name` retains its native success expectation.

## Coverage summary

| Contract area | Technique | Runnable coverage | Tier / status |
|---|---|---|---|
| Base HTTP, SSE, usage | equivalence classes | sync usage, stream usage, SSE completion | T1 runnable |
| `reasoning_effort` | enum partition | omitted default plus `low`, `high`, `max`, invalid `medium` | T1/T2 runnable |
| Thinking parameter probes | behavior/alias partition | default-on baseline plus `enable_thinking`, `thinking.type`, and `chat_template_kwargs` on/off probes | T1 runnable; official baseline plus provider-behavior probes |
| Fixed sampling values | singleton boundary values | omitted parameters, five independent exact official values, below/above representatives for all five fixed parameters | T1/T2 runnable |
| `top_logprobs` | numeric BVA + dependency table | `0`, `20`, `-1`, `21`, and missing `logprobs=true` | T1/T2 runnable |
| `stop` | length/cardinality BVA + runtime behavior | one 32-byte item, five items, six items, one 33-byte item, and a response that must terminate at `BBB` without returning it | T1/T2 runnable |
| `max_completion_tokens` | numeric + dependent boundary | small accepted value, model maximum plus input overflow, maximum + 1 | T2 runnable; exact near-1M acceptance is T3 |
| Structured Output | enum/required-field partition | `json_object`, valid `json_schema`, missing `name`, missing `schema`, non-object schema, invalid format type | T1/T2 runnable |
| Tool choice | enum + decision table | omitted/auto/none/required, specified-function incompatibility while K3 thinking is on | T1/T2 runnable |
| Tool definitions | string BVA/pattern partition | 1-char and 128-char names, digit prefix, 129-char name | T1/T2 runnable |
| Dynamic tools | dependent-field/state partition | valid system tool declaration, declaration with `content`, global and dynamic tools coexisting | T1/T2 runnable |
| Messages | required/empty partition | missing list, empty list, empty standard-message content | T2 runnable |
| Vision/video shapes | polymorphic partition | Base64 image/video object and string inputs, plus missing discriminators/conditional fields | T3, in foundation but non-default selectable |
| Partial Mode | workflow state | final assistant message with `partial=true` | T1 runnable |
| Predicted Output | polymorphic/enum partition | string content, text-object array, invalid type | T1/T2 runnable |
| Cache/safety identifiers | optional-field partition | `prompt_cache_key`, hashed `safety_identifier` accepted | T1 runnable |
| 1M context admission | dependent token boundary | near-window profile requirements documented below | T3 deferred; no paid execution |
| Prefix-cache behavior | state/repetition | two sequential K3 requests share a deterministic >256-token system prefix and assert a positive second-request `cached_tokens` value bounded by `prompt_tokens` | T3 runnable, non-default provider-observation probe |
| Preserved Thinking replay | state transition | contract documented; exact replay needs a multi-request evaluator that carries the provider-returned assistant object unchanged | runner gap |
| Request signature nonce | header contract | documented, but current Case execution does not forward case-defined request headers | runner gap |
| Rate limits/concurrency | load/state | 32K load profile retained separately | T3 deferred |

## Execution policy

- T0: repository bundle, current-format decoding, Suite reference validation, and evaluator unit tests.
- T1: small successful requests. These are enabled but non-default unless they are an existing smoke baseline.
- T2: admission-boundary and malformed-request cases expecting an OpenAI-style HTTP 400.
- T3: media, long-context, caching, concurrency, or repeated-call scenarios. They are never part of the default case selection and require explicit operator selection or a separate profile.
- No live Kimi request was made while creating this matrix.

The deferred 1M profile should use a tokenizer-generated prompt near 999,000 tokens, `max_completion_tokens=512`, one request, and concurrency 1. It must first prove that input plus requested output stays within 1,048,576 tokens. It remains a design note rather than an executable case; normal Case discovery reads `case.json` documents, while `load-profile-32k.json` is retained separately as a reference workload.

## Primary sources

- [Chat Completions API](https://platform.kimi.com/docs/api/chat)
- [OpenAPI document](https://platform.kimi.com/docs/openapi.json)
- [Kimi K3 quickstart](https://platform.kimi.com/docs/guide/kimi-k3-quickstart)
- [Model parameter reference](https://platform.kimi.com/docs/api/models-overview)
- [Reasoning effort](https://platform.kimi.com/docs/guide/use-reasoning-effort)
- [Tool choice constraints](https://platform.kimi.com/docs/guide/use-tool-choice)
- [Dynamic tool loading](https://platform.kimi.com/docs/guide/use-dynamic-tool-loading)
- [Structured Output](https://platform.kimi.com/docs/guide/response_format)
- [Partial Mode](https://platform.kimi.com/docs/guide/use-partial-mode-feature-of-kimi-api)
- [Context caching](https://platform.kimi.com/docs/guide/use-context-caching-feature-of-kimi-api)
- [Error responses](https://platform.kimi.com/docs/api/errors)

## Documentation conflicts and limits

- The dynamic-tool guide says a complete dynamic tool includes `name`, `description`, and `parameters`, while the current OpenAPI `ToolDefinition` marks only `name` and `parameters` as required. The suite does not assert that missing `description` must fail.
- The OpenAPI schema documents the function-name regex and 128-character maximum. It does not currently declare a maximum number of tools, so this suite does not invent a 128-tool cardinality boundary.
- `max_completion_tokens=1048576` cannot be a successful non-empty request at a 1M total context window because input plus requested output would exceed the window. The runnable boundary therefore asserts that dependent overflow is rejected; near-window success remains a T3 token-estimation scenario.
- Error cases assert HTTP 400 plus a non-empty OpenAI-style `error.message`. They intentionally avoid matching unstable provider wording.
