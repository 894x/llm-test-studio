# GLM-5.3 cases and suites

[简体中文](README.md)

Built for Chat Completions on the standard Model API, based on the [official GLM-5.3 documentation](https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3) consulted on 2026-09-08. The exact model ID is `glm-5.3`, using the `openai-chat` protocol.

There are 212 cases: 165 enabled automatic cases and 47 disabled templates. See the [coverage matrix](coverage-matrix.md) and [machine-readable inventory](contract-inventory.json) for the full 54-row contract inventory, sources, and gaps. Pending and blocked items remain; this suite does not claim complete coverage or successful live verification.

| Suite | Cases | Purpose |
|---|---:|---|
| GLM 5.3 connectivity | 3 | Final answer, missing and invalid authentication |
| GLM 5.3 basic functionality | 12 | Thinking, SSE, JSON, tools, historical reasoning, cache counters |
| GLM 5.3 parameter rejection | 101 | HTTP status and business error codes for invalid parameters |
| GLM 5.3 automatic regression | 165 | All enabled automatic cases |
| GLM 5.3 full design (including disabled templates) | 212 | All designs and explicitly retained gaps |

All cases set `default=false` and apply only to `glm-5.3`. Generated suites select the `glm53.*` dimensions to exclude generic cases that apply to all models. Configuration is in `data/suites/openai-chat/glm-5.3.suite-profiles.json`; the repository skill's generator maintains the output.

## Use in Studio

1. For the direct standard API, use Base URL `https://open.bigmodel.cn/api/paas/v4` and save credentials through channel configuration.
2. Map logical model `glm-5.3` to upstream model `glm-5.3`, with `openai-chat` included in both the model and mapping protocols. Cases use `/chat/completions`, which is appended to the Base URL.
3. Find the `GLM 5.3` cases and the five suites in the catalog. Start a Plan with connectivity or basic functionality. For ordinary regression, use concurrency 1 and a per-case timeout of at least 120 seconds; history/tool roundtrip cases make two requests.
4. Keep T3 large-output, context, cache-load, large-tool-array, external-service, and documentation-dependent templates disabled. Complete each template's prerequisites and assertions before enabling and running it separately.

The connectivity suite also exposes a Quick Test entry with an editable prompt; it checks HTTP success and non-empty final text. Missing/invalid credential cases retain fixed minimal requests.

The standard API supports only `reasoning_effort=low/high/max`. Coding Plan alias mapping and default historical-reasoning behavior differ; Responses and Anthropic endpoints need separate contracts. This suite cannot directly establish their compatibility. Gateways that normalize error codes can also fail these assertions; record that as a compatibility difference.

## Assertions and reports

- Ordinary success cases retain HTTP status, final text, and termination checks. The dedicated response-contract case checks only assistant role, `reasoning_content`, and usage arithmetic. Identity/timestamp metadata and envelope details such as `choices` count are no longer repeated assertions, so the coverage matrix marks this row partial. `max_tokens=1` separately checks `length` truncation and usage limits.
- Parameter rejection requires HTTP 400 and allowed business codes such as `1210/1213/1214`. Authentication, missing-model, and safety errors are checked separately; an arbitrary error is not a passing parameter test.
- Streaming assertions accumulate final text, reasoning, and function arguments separately; they verify a unique termination, `[DONE]`, usage, and valid function-argument JSON. Reasoning without a final result fails.
- Tool-boundary cases only confirm that the request is accepted and returns valid text or a tool call. Dedicated roundtrip/stream cases check the exact tool name and arguments, without separate non-empty name/ID or `type=function` assertions. Single-tool cases read the first call directly instead of nesting `each`; the roundtrip result verifies whether the ID is usable. Roundtrips use synthetic local constant fixtures and never execute arbitrary model-returned functions; they return the original assistant message, reasoning, and matching tool call ID, and sum token usage across both requests.
- Restoring a historical task and testing again reuses its connection and inputs while loading the current suite and case definitions. Historical reports retain their original assertions. A missing current suite or case prevents the run from starting; execution definitions are never restored from a historical snapshot.
- The existing `legacy.apiaudit` reporting path displays results, brief failure reasons, request counts, and usage. No new configuration form or report DTO is required. Templates are not presented as verified successes.

Example prompts, identifiers, and tool outputs are synthetic test materials created with AI assistance for this project and distributed under the repository's Apache-2.0 license. Official documentation supplies API facts; full documentation, production data, credentials, and third-party media were not copied.

## Static verification

Run from the repository root:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases/openai-chat --suites-root data/suites/openai-chat --manifest data/suites/openai-chat/glm-5.3.suite-profiles.json --check
python .agents/skills/api-boundary-test-case-design/scripts/audit_case_coverage.py data/cases/openai-chat --model glm-5.3

go test -p 1 ./... -count=1
```

The audit script also includes existing generic cases applicable to all models. The count 212 refers to `GLM53-*` files and the five dedicated suites; the script's total is not the dedicated-case count.

The original suite validation ran only T0 loading, generation consistency, and simulated HTTP/SSE tests. It made no T1/T2/T3 provider calls.

See the [validation record](validation.md) for exact check results, environment handling, and all incomplete coverage rows.
