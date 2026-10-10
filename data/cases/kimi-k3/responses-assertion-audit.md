# Responses response assertion audit

Source: [Kimi OpenAPI](https://platform.kimi.com/docs/openapi.json), retrieved 2026-10-11. Scope: all 254 authored `openai-responses` definitions, including the 46 definitions in shared Chat Cases and 208 native-only Cases. Request bodies, workflows and stable identities are outside this response-assertion correction.

The response schemas have no `required` list. A property type, enum or description constrains a returned value; it does not require every optional property to be present. Request defaults do not imply response echoes. Case-specific answer, tool-use and cache-hit assertions describe the scenario's requested behavior, rather than mandatory fields in every Responses response.

| Assertion family | Schema evidence | Correction |
|---|---|---|
| `default_budget`, `default_effort` | Request default only; response budget is nullable and response reasoning has no effort property contract | Remove exact response echoes; describe these Cases as omission/empty-object acceptance |
| Common object/status/output checks | Optional properties; `output` has no `minItems` | Remove redundant unconditional presence/completed/non-empty checks; retain explicit budget terminal scenarios and semantic answers |
| Response ID/model/timestamps/fixed fields | Optional; no minimum string length; fixed-value descriptions exist for selected properties | Remove non-empty ID/model checks; check timestamp types and documented fixed values only when present, allowing published null alternatives |
| HTTP error envelope | `ErrorResponse` requires `error.message` as a string; type/code are optional, with no enum or message pattern | Require a string message; remove fixed error type, non-empty and keyword assumptions |
| Usage | Nullable and all nested counters optional; no positive minimum | Remove unconditional presence and positive-count checks; validate returned counter types, arithmetic and documented output budget only when their inputs exist |
| Reasoning output | Optional reasoning item/summary; no minimum cardinality | Remove forced reasoning-item and summary presence |
| Web search output | Search happens only when the server decides it is needed; sources/results/caption optional; URL fields are strings without format constraints | Remove forced search, non-empty sources/images/captions and URL-format checks; validate the documented image count cap if a search item contains results |
| Cache option echo | Response reports applied values; defaults are specified on the request | Remove forced mode/TTL echoes |
| Function strict=false | Output argument string describes JSON, not an exact empty object | Retain JSON validity, remove the exact `{}` argument expectation |
| SSE | Event `type` and `sequence_number` are required | Retain event and completion assertions |
| Scenario semantics | Sentinel answers, declared function/custom calls and paired IDs/namespaces, cache-hit workflow, explicit budget truncation | Retain these functional outcomes; do not present them as unconditional schema requirements |

No provider requests are part of this correction. Existing run snapshots and reports retain their original assertions and verdicts. Removing an echo does not verify the actual default budget or reasoning effort; those behavioral defaults still require separate controlled evidence.

Static checks: all 254 JSON definitions retain their request/workflow and stable IDs; shared Chat definitions are identical to the original. Assertion nodes decreased from 1394 to 964. All five Suite manifests pass the generator's `--check` with unchanged membership (3/230/61/169/23 Cases). `git diff --check` and formatting inspection pass. The existing search-image fixture expectation now permits zero returned results; unit tests were not run.

Cases are embedded in the application binary. A rebuilt desktop process is required to load the revised built-in definitions; historical Run snapshots are not rewritten.
