# LLM API boundary checklist

Use only the sections applicable to the provider and endpoint. Verify every limit against current primary documentation instead of copying values from this checklist.

## Request envelope

- Endpoint and compatibility facade versus provider-upstream route
- Required model and messages fields
- Missing, null, empty, wrong-type, and unknown-field behavior
- Deprecated aliases versus preferred fields
- Model-specific parameter support and defaults

## Generation controls

- Output-token minimum, maximum, default, and combined context invariant
- Fixed versus ranged sampling parameters
- `n`, penalties, seed, stop count, stop byte length, and encoding
- Log probability range and prerequisite relationships
- Reasoning/thinking enum, default, preserved-reasoning requirements, and invalid mode

## Messages and multimodal content

- Empty and maximum message/content arrays
- Supported roles, ordering, names, tool-call IDs, and multi-turn replay
- Text, image, video, file, URL, base64, MIME, and object/string variants
- Required nested fields and malformed discriminated unions
- Partial/prefill rules and incompatible response modes

## Tools and structured output

- Tool array cardinality, unique names, schema shape, strict mode, and selected-function references
- `tool_choice` default and supported enums/objects
- Tool-call response integrity, arguments JSON, parallel calls, and result replay
- Response-format default, JSON object, JSON schema, invalid schema, unsupported keywords, and complexity limits

## Streaming and usage

- Non-stream and stream equivalence
- SSE framing, ordering, final chunk, finish reason, usage placement, and `[DONE]`
- Empty deltas, tool-call deltas, reasoning deltas, disconnect, cancellation, and retry behavior
- Usage arithmetic, cached-token fields, and absence/presence rules

## Context, caching, and performance

- Context-window admission at the calculated combined boundary
- Token-estimator agreement and tokenizer/model version
- Cache key scope, stable-prefix requirements, hit/miss observability, and cross-tenant isolation
- Rate-limit dimensions, 429 response, retry metadata, concurrency, RPM/TPM, and load ramp

Treat large-context, large-array, cache, concurrency, and repeated quality tests as T3 unless a low-cost admission-only design can prove the contract.

## Security and privacy

- Authentication absence, invalid/expired credentials, and tenant isolation
- Sensitive-data redaction in errors, logs, evidence, and reports
- Prompt or tool payload size limits without embedding secrets
- Provider safety errors as distinct outcomes, not generic transport failures

## Drift checks

- Compare current official docs with repository README/comments and existing expectations.
- Flag disabled cases that current docs now support.
- Flag enabled cases that rely on deprecated fields or undocumented gateway behavior.
- Distinguish provider contract, downstream gateway policy, and observed characterization behavior.
