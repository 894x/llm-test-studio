# JSON Partial continuation

Scope: Chat Completions JSON prefix continuation for `kimi-k3` and
`kimi-k3.1-pro`, with paired non-stream and SSE Cases. This is the JSON
continuation portion of the local Partial investigation, distinct from the
eight KVV-derived continuation-format Cases. Inputs are project-authored
synthetic data; no provider credentials or captured reasoning are shipped.

For one combined entry, select **Kimi · Partial 综合回归**
([`kimi.partial`](../../suites/openai-chat/kimi-partial/suite.json)). It contains
the ten JSON Cases first and the eight KVV-derived format Cases second, with no
repeated Case ID. Its model is supplied at run time, so the same Suite can be run
with K3 or K3.1 Pro. Two JSON members are no-Partial controls; overall counts are
not a semantic Partial-support rate. No Partial member compares token counts.
The existing dedicated Suites remain available. The combined Suite reuses the
same Cases without request overrides or changes to Case defaults; its opt-in
Plan is described below.
It requires the same JSON-prefix runner capability.

The [official Partial guide](https://platform.kimi.ai/docs/api/partial),
retrieved on 2026-11-11, enables prefill with a final assistant message carrying
`partial: true`, demonstrates JSON continuation from `{`, and requires callers
to concatenate prefix and returned content. It warns against mixing Partial
with `response_format.type=json_object`; every Case here omits that field.
String/field boundaries are exploratory continuation scenarios, not separately
documented tokenizer-boundary guarantees. The extraction fixture follows the
guide's workflow with independently authored product data and explicit types.

## Inventory and scenario matrix

All requests use POST Chat Completions with explicit `messages` and `stream`,
and a 1024-token output budget. The Run binds model/URL/credential. Every pair
differs only in `stream`; no temperature or token baseline is assumed.
Core scenarios set `thinking.type=disabled`; extraction omits thinking controls
as in the guide. No request combines thinking and reasoning-effort controls.

| Scenario | Final assistant prefix | Requested example (not a value assertion) | Modes / count |
|---|---|---|---|
| string boundary | `{"answer":"PA` with `partial=true` | `{"answer":"PARIS"}` | sync + stream / 2 |
| field boundary | `{"answer":` with `partial=true` | `{"answer":"PARIS"}` | sync + stream / 2 |
| object boundary | `{` with `partial=true` | `{"answer":"PARIS"}` | sync + stream / 2 |
| no-Partial control | `{"answer":` without `partial` | complete response `{"answer":"PARIS"}` | sync + stream / 2 |
| documented extraction workflow | `{` with `partial=true` | product object with explicit string/number/array values | sync + stream / 2 |

Each Case requires HTTP 200, no protocol issues, `finish_reason=stop`, and
non-whitespace content. SSE also requires `[DONE]`. The assertion parses the
literal input prefix plus returned suffix as exactly one complete JSON value.
It rejects empty/truncated responses, repeated full objects, Markdown fences,
invalid syntax and trailing values. It does not enforce answer values, field
types, missing/extra fields, product accuracy or token counts. For example,
`PA` plus `IRS` is a valid string value and passes this format-only policy.
Whitespace inside a JSON string remains data; no trimming is performed. The
no-Partial control uses an empty assertion prefix and validates the complete
returned JSON; it does not assert continuation.

## Assertion contract

The runner retains two operators using the existing Assertion `value` field:

- `json_prefix_valid`: `value` is the literal prefix string. The selected
  actual string is concatenated without trimming, then parsed as exactly one
  complete JSON value.
- `json_prefix_equals`: `value` is exactly `{"prefix":"...","expected":...}`.
  Parse `prefix + actual`, then compare the complete JSON value semantically.
  Object order and equivalent numeric notation follow existing JSON equality.

The Partial Cases now use only `json_prefix_valid`; `json_prefix_equals` remains
available for authored Cases and historical snapshots requiring value checks.
Both reject non-string actual values, malformed/trailing JSON and concatenated
documents exceeding the existing 4 MiB authored-value decoding limit. Invalid
operator configuration is rejected by Spec validation. No new Assertion fields
or format decoder are introduced. Existing operators keep their behavior.

## Compatibility and release impact

The catalog contains ten non-default, enabled, automatic T1 Cases and two
opt-in JSON Partial Suites. The eight KVV-derived Cases now use continuation
format checks as documented in [their coverage notes](kvv-partial-coverage.md).
Case/Suite IDs, memberships and Plan references remain unchanged. Supported
Case schema 2/3, Suite schema 1, CLI/API and stored database/report formats are
unchanged. No data migration, deprecation or
removal is required; historical results are not recomputed. New runs use the
format-only policy adopted on 2026-11-11, so their conclusions must not be
compared as though earlier token/value assertions were identical.

JSON-prefix assertions require the runner feature `json_prefix_assertions`.
Updated local/remote hosts advertise it; clients check it recursively through
`all`, `any` and `each` before scheduling the new assertions. Existing jobs do
not gain this requirement. Upgrade both client and selected runner to a build
with this capability for the new Suites. Older binaries do not understand the
new operators and reject user-authored copies rather than reinterpret them.
Before downgrading, use the older bundled catalog and remove new Suite/Case
references from separately saved plans. No database restoration is needed.

## Verification

Validation on 2026-11-11 separates generic operator coverage from the current
Partial format policy:

- `go test ./data/cases ./data/suites ./internal/casecodec ./internal/testspec ./internal/protocols/openaichat ./internal/jobcontract ./internal/application/suitecatalog`:
  bundled decoding and references, all five paired requests, fragmented SSE,
  format-only JSON validation, acceptance of changed values/types/fields,
  rejection of malformed/repeated JSON and wrong finish/status, generic
  operator regression tests and runner capability checks.
- `go test ./internal/application/plancatalog ./internal/persistence/workspace -run TestBuiltin -count=1`:
  retained built-in Plan loading and per-Case scheduling.
- All three Partial manifests pass the generator's `--check`;
  seven generator unit tests pass. Direct checks confirm ten unique Case IDs,
  matching literal prefixes, no-flag controls, pairwise request equivalence,
  and no credentials/model bindings in authored request bodies.
- `go vet ./internal/protocols/openaichat` and `git diff --check` pass.

This integration made no live provider requests and does not transfer prior
live-test conclusions onto the new authored Cases. Sampling variants and full
provider parameter-boundary coverage remain outside this ten-Case catalog.

The combined `kimi.partial` entry provides one 18-member Suite,
flattening the existing JSON and KVV-derived format memberships without
duplicate IDs. Its generator check, bundled reference/catalog tests, and
retained built-in Plan scheduling test passed locally. The combined Suite
does not override its members' request bodies or assertions.

The corresponding **Kimi Partial 综合回归计划**
([`65672e92-21af-4777-8bf4-72517da2746a`](../../plans/65672e92-21af-4777-8bf4-72517da2746a.json))
has one Suite entry in `single` mode, concurrency one and no warmup. The
existing `single` semantics expand its nominal `request_count: 1` to one
execution per resolved Suite Case: 18 requests, each Case once. Bind model and
channel when running the plan. Existing plans and user overrides are unchanged.
Before downgrading, use the older bundled catalog and remove new Plan/Suite
references from user-authored plans; copying Case files does not add JSON-prefix
assertion support to an older binary. No stored data is migrated or recomputed.

`TestBuiltinPartialPlanSchedulesEveryCaseOnce` verifies built-in Plan loading,
the `kimi.partial` target, 18 unique Case references, concurrency one, no warmup,
and the expanded 18-request budget through the existing execution profile.
