# Built-in case and media provenance

This statement applies to the built-in case definitions, load profiles, fixtures, and media embedded in case payloads under `data/cases/`.

- Unless otherwise attributed below, the material was created by 894x for LLM Test Studio or generated for the project with AI-assisted tools.
- To the extent copyright or related rights apply, the project-owned material is distributed under Apache-2.0 with the rest of the repository.
- The material is intended to contain no customer data, confidential data, production credentials, or third-party personal data.
- URLs hosted on third-party infrastructure are delivery locations, not claims that the host owns or licenses the material. Such URLs may be replaced or become unavailable.

Contributors adding a case or media fixture must document its source and license, identify AI-assisted or synthetic material when applicable, confirm that it can be redistributed, and remove credentials or personal data before submission.

Before a public release, externally hosted fixtures should be moved to project-controlled storage or made configurable so reproducible tests do not depend on an unrelated host.

## Kimi Vendor Verifier material

The `KVV-prompt-tokens-assistant-hello-stream` request and token baseline are
adapted from the `assistant_hello` record in Moonshot AI's
[Kimi-Vendor-Verifier at 66092cf](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/blob/66092cf444c97356c0e11c5078c67116390615d9/testdata/prompt_token_cases/cases.jsonl).
The upstream MIT copyright and permission notice is preserved in
[`kimi-k3/KVV-MIT-LICENSE.txt`](kimi-k3/KVV-MIT-LICENSE.txt). The request is
unchanged except for the stream flag that upstream adds at execution time; the
assertion uses upstream's current inclusive `[99, 102]` tolerance. It is an
upstream baseline, not a guarantee for every model or deployment template.

The eight `KVV-partial-*-stream` requests and token baselines are adapted from
[`cases.partial.jsonl` at the same 66092cf revision](https://github.com/MoonshotAI/Kimi-Vendor-Verifier/blob/66092cf444c97356c0e11c5078c67116390615d9/testdata/prompt_token_cases/cases.partial.jsonl).
The original records are retained in [`kimi-k3/kvv-partial.jsonl`](kimi-k3/kvv-partial.jsonl)
under the same preserved MIT notice. Requests add only `stream=true`; absent
and empty content/reasoning fields remain distinct. Assertions retain each
upstream baseline's inclusive `expected` through `expected+3` tolerance, with
the product's additional complete-stream/protocol checks. Separate K3 and
K3.1 Pro Suites share these K3 observations, not independently calibrated
model-specific token guarantees.

The ten `JSON-partial-*` Cases use independently authored synthetic prompts
and product data. They adopt the JSON prefill workflow described in the
[official Partial guide](https://platform.kimi.ai/docs/api/partial), not copied
KVV fixtures or captured provider responses. Their scenarios and assertion
semantics are documented in [`kimi-k3/json-partial-coverage.md`](kimi-k3/json-partial-coverage.md).

Other KVV compatibility cases are project-authored synthetic cases derived
from documented behavior or KVV scenario ideas, rather than copied fixtures.
