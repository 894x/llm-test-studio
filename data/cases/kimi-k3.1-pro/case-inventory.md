# Case coverage inventory

- Root: `/Users/snowflake/GITHUB/llm-test/data/cases/openai-chat`
- Case key prefix: `k31.`
- Case files: 442
- Selected cases: 93
- Cases asserting 4xx HTTP rejection: 41

## Dimensions

- `cache`: 9
- `messages`: 3
- `multimodal`: 12
- `parameters`: 30
- `protocol`: 4
- `reasoning`: 1
- `reasoning-parameters`: 4
- `safety`: 1
- `structured-output`: 9
- `tools`: 16
- `workflow`: 4

## Request parameters

- `frequency_penalty`: 3
- `logprobs`: 5
- `max_completion_tokens`: 70
- `max_tokens`: 23
- `messages`: 92
- `n`: 3
- `prediction`: 3
- `presence_penalty`: 3
- `prompt_cache_key`: 3
- `prompt_cache_options`: 6
- `reasoning_effort`: 25
- `response_format`: 9
- `safety_identifier`: 1
- `stop`: 5
- `stream`: 3
- `stream_options`: 2
- `temperature`: 4
- `tool_choice`: 10
- `tools`: 14
- `top_logprobs`: 6
- `top_p`: 3

## Assertion operators

- `contains`: 5
- `equals`: 115
- `exists`: 1
- `gt`: 2
- `gte`: 4
- `json_valid`: 4
- `length_equals`: 14
- `lte`: 2
- `non_empty`: 110
- `not_contains`: 1
- `not_exists`: 1
- `type`: 7

## Selected cases

| Path | Key | Dimension | Operators | Parameters | HTTP rejection? |
|---|---|---|---|---|---|
| `K31-F001-stream-integrity/case.json` | `k31.must.stream_integrity` | `protocol` | `equals, gte, length_equals, non_empty` | `max_tokens, messages, stream, stream_options` | no |
| `K31-F002-usage-non-stream/case.json` | `k31.must.usage_non_stream` | `protocol` | `equals, gte, non_empty, type` | `max_tokens, messages` | no |
| `K31-F003-usage-stream/case.json` | `k31.must.usage_stream` | `protocol` | `equals, gte, length_equals, non_empty, type` | `max_tokens, messages, stream, stream_options` | no |
| `K31-F004-image-url-object/case.json` | `k31.must.image_url_object` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | no |
| `K31-F005-image-url-string/case.json` | `k31.must.image_url_string` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | no |
| `K31-F006-tool-call/case.json` | `k31.must.tool_call` | `tools` | `equals, non_empty` | `max_tokens, messages, tool_choice, tools` | no |
| `K31-F007-tool-choice-default/case.json` | `k31.must.tool_choice_default` | `tools` | `equals, non_empty` | `max_tokens, messages, tools` | no |
| `K31-F008-tool-choice-auto/case.json` | `k31.must.tool_choice_auto` | `tools` | `equals, non_empty` | `max_tokens, messages, tool_choice, tools` | no |
| `K31-F009-tool-choice-required/case.json` | `k31.must.tool_choice_required` | `tools` | `equals, non_empty` | `max_tokens, messages, tool_choice, tools` | no |
| `K31-F010-tool-choice-none/case.json` | `k31.must.tool_choice_none` | `tools` | `equals, length_equals, non_empty, not_exists` | `max_tokens, messages, tool_choice, tools` | no |
| `K31-F011-tool-choice-function/case.json` | `k31.optional.tool_choice_function` | `tools` | `equals, non_empty` | `max_tokens, messages, tool_choice, tools` | yes |
| `K31-F020-thinking-default/case.json` | `k31.must.thinking_default` | `reasoning` | `equals` | `max_tokens, messages` | no |
| `K31-F021-json-object/case.json` | `k31.must.json_object` | `structured-output` | `contains, equals, non_empty, type` | `max_tokens, messages, response_format` | no |
| `K31-F022-json-schema/case.json` | `k31.must.json_schema` | `structured-output` | `contains, equals, non_empty, type` | `max_tokens, messages, response_format` | no |
| `K31-F023-video-url-object/case.json` | `k31.must.video_url_object` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | no |
| `K31-F024-video-url-string/case.json` | `k31.must.video_url_string` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | no |
| `K31-F025-multimodal-type-required/case.json` | `k31.must.multimodal_type_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F027-multimodal-text-required/case.json` | `k31.must.multimodal_text_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F028-multimodal-image-url-required/case.json` | `k31.must.multimodal_image_url_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F029-multimodal-video-url-required/case.json` | `k31.must.multimodal_video_url_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F030-image-url-object-url-required/case.json` | `k31.must.image_url_object_url_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F031-video-url-object-url-required/case.json` | `k31.must.video_url_object_url_required` | `multimodal` | `equals, non_empty` | `max_tokens, messages` | yes |
| `K31-F032-json-schema-missing-name/case.json` | `k31.json_schema_missing_name` | `structured-output` | `equals, non_empty` | `max_completion_tokens, messages, response_format` | yes |
| `K31-F033-json-schema-missing-schema/case.json` | `k31.json_schema_missing_schema` | `structured-output` | `equals, non_empty` | `max_completion_tokens, messages, response_format` | yes |
| `K31-F034-json-schema-non-object/case.json` | `k31.json_schema_non_object` | `structured-output` | `equals, non_empty` | `max_completion_tokens, messages, response_format` | yes |
| `K31-F035-response-format-invalid-type/case.json` | `k31.response_format_invalid_type` | `structured-output` | `equals, non_empty` | `max_completion_tokens, messages, response_format` | yes |
| `K31-F040-dynamic-tool/case.json` | `k31.dynamic_tool` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, tool_choice` | no |
| `K31-F041-dynamic-tool-with-content/case.json` | `k31.dynamic_tool_with_content` | `tools` | `equals, non_empty` | `max_completion_tokens, messages` | yes |
| `K31-F042-dynamic-and-global-tools/case.json` | `k31.dynamic_and_global_tools` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, tools` | no |
| `K31-F043-tool-name-one-char/case.json` | `k31.tool_name_one_char` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, tool_choice, tools` | no |
| `K31-F044-tool-name-128-chars/case.json` | `k31.tool_name_128_chars` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, tool_choice, tools` | no |
| `K31-F045-tool-name-invalid-first-char/case.json` | `k31.tool_name_invalid_first_char` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, tools` | yes |
| `K31-F046-tool-name-129-chars/case.json` | `k31.tool_name_129_chars` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, tools` | yes |
| `K31-F050-partial-mode/case.json` | `k31.partial_mode` | `workflow` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort` | no |
| `K31-F052-prediction-string/case.json` | `k31.prediction_string` | `workflow` | `equals, non_empty` | `max_completion_tokens, messages, prediction` | no |
| `K31-F053-prediction-text-array/case.json` | `k31.prediction_text_array` | `workflow` | `equals, non_empty` | `max_completion_tokens, messages, prediction` | no |
| `K31-F054-prediction-invalid-type/case.json` | `k31.prediction_invalid_type` | `workflow` | `equals, non_empty` | `max_completion_tokens, messages, prediction` | yes |
| `K31-F055-messages-required/case.json` | `k31.messages_required` | `messages` | `equals, non_empty` | `max_completion_tokens` | yes |
| `K31-F056-messages-empty/case.json` | `k31.messages_empty` | `messages` | `equals, non_empty` | `max_completion_tokens, messages` | yes |
| `K31-F057-message-empty-content/case.json` | `k31.message_empty_content` | `messages` | `equals, non_empty` | `max_completion_tokens, messages` | yes |
| `K31-F058-prompt-cache-key/case.json` | `k31.prompt_cache_key` | `cache` | `equals, non_empty` | `max_completion_tokens, messages, prompt_cache_key` | no |
| `K31-F059-safety-identifier/case.json` | `k31.safety_identifier` | `safety` | `equals, non_empty` | `max_completion_tokens, messages, safety_identifier` | no |
| `K31-K3-cache-prefix-hit/case.json` | `k31.cache_prefix_hit` | `cache` | `equals, gt, gte, lte, non_empty, type` | `max_completion_tokens, messages, prompt_cache_key, temperature` | no |
| `K31-K3-stop-runtime-truncation/case.json` | `k31.stop_runtime_truncation` | `parameters` | `contains, equals, non_empty, not_contains` | `max_completion_tokens, messages, reasoning_effort, stop` | no |
| `K31-N-cache-ttl-1h-hit/case.json` | `k31.cache_ttl_1h_hit` | `cache` | `equals, gt, lte` | `max_completion_tokens, messages, prompt_cache_key, prompt_cache_options, reasoning_effort` | no |
| `K31-N-image_public_url_rejected/case.json` | `k31.image_public_url_rejected` | `multimodal` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort` | yes |
| `K31-N-image_svg_rejected/case.json` | `k31.image_svg_rejected` | `multimodal` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort` | yes |
| `K31-N-json_schema_complex/case.json` | `k31.json_schema_complex` | `structured-output` | `equals, json_valid, length_equals, non_empty, type` | `max_completion_tokens, messages, reasoning_effort, response_format` | no |
| `K31-N-json_schema_strict_false/case.json` | `k31.json_schema_strict_false` | `structured-output` | `equals, json_valid, length_equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, response_format` | no |
| `K31-N-logprobs_enabled/case.json` | `k31.logprobs_enabled` | `parameters` | `equals, exists, length_equals, non_empty` | `logprobs, max_completion_tokens, messages, reasoning_effort, top_logprobs` | no |
| `K31-N-prompt_cache_breakpoint/case.json` | `k31.prompt_cache_breakpoint` | `cache` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort` | yes |
| `K31-N-prompt_cache_options_mode/case.json` | `k31.prompt_cache_options_mode` | `cache` | `equals, length_equals, non_empty` | `max_completion_tokens, messages, prompt_cache_options, reasoning_effort` | no |
| `K31-N-prompt_cache_options_mode_invalid/case.json` | `k31.prompt_cache_options_mode_invalid` | `cache` | `equals, non_empty` | `max_completion_tokens, messages, prompt_cache_options, reasoning_effort` | yes |
| `K31-N-prompt_cache_options_ttl_1h/case.json` | `k31.prompt_cache_options_ttl_1h` | `cache` | `equals, length_equals, non_empty` | `max_completion_tokens, messages, prompt_cache_options, reasoning_effort` | no |
| `K31-N-prompt_cache_options_ttl_5m/case.json` | `k31.prompt_cache_options_ttl_5m` | `cache` | `equals, length_equals, non_empty` | `max_completion_tokens, messages, prompt_cache_options, reasoning_effort` | no |
| `K31-N-prompt_cache_options_ttl_invalid/case.json` | `k31.prompt_cache_options_ttl_invalid` | `cache` | `equals, non_empty` | `max_completion_tokens, messages, prompt_cache_options, reasoning_effort` | yes |
| `K31-N-response_format_text/case.json` | `k31.response_format_text` | `structured-output` | `equals, length_equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, response_format` | no |
| `K31-N-stream_explicit_false/case.json` | `k31.stream_explicit_false` | `protocol` | `equals, length_equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, stream` | no |
| `K31-N-tool_parameters_required/case.json` | `k31.tool_parameters_required` | `tools` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, tools` | yes |
| `K31-N-tool_strict_false/case.json` | `k31.tool_strict_false` | `tools` | `equals, json_valid, length_equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, tool_choice, tools` | no |
| `K31-N-tool_strict_true/case.json` | `k31.tool_strict_true` | `tools` | `equals, json_valid, length_equals, non_empty` | `max_completion_tokens, messages, reasoning_effort, tool_choice, tools` | no |
| `K31-P010-param-omit-sampling/case.json` | `k31.param_omit_sampling` | `parameters` | `equals, non_empty` | `max_tokens, messages` | no |
| `K31-P012-temperature-below-fixed/case.json` | `k31.temperature_below_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, temperature` | yes |
| `K31-P013-temperature-above-fixed/case.json` | `k31.temperature_above_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, temperature` | yes |
| `K31-P014-top-p-below-fixed/case.json` | `k31.top_p_below_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, top_p` | yes |
| `K31-P015-top-p-above-fixed/case.json` | `k31.top_p_above_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, top_p` | yes |
| `K31-P016-n-below-fixed/case.json` | `k31.n_below_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, n` | yes |
| `K31-P017-n-above-fixed/case.json` | `k31.n_above_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, n` | yes |
| `K31-P018-presence-penalty-below-fixed/case.json` | `k31.presence_penalty_below_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, presence_penalty` | yes |
| `K31-P019-presence-penalty-above-fixed/case.json` | `k31.presence_penalty_above_fixed` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, presence_penalty` | yes |
| `K31-P020-frequency-penalty-below-fixed/case.json` | `k31.frequency_penalty_below_fixed` | `parameters` | `equals, non_empty` | `frequency_penalty, max_completion_tokens, messages` | yes |
| `K31-P021-frequency-penalty-above-fixed/case.json` | `k31.frequency_penalty_above_fixed` | `parameters` | `equals, non_empty` | `frequency_penalty, max_completion_tokens, messages` | yes |
| `K31-P022-param-fixed-temperature/case.json` | `k31.param_fixed_temperature` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, temperature` | no |
| `K31-P023-param-fixed-top-p/case.json` | `k31.param_fixed_top_p` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, top_p` | no |
| `K31-P024-param-fixed-n/case.json` | `k31.param_fixed_n` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, n` | no |
| `K31-P025-param-fixed-presence-penalty/case.json` | `k31.param_fixed_presence_penalty` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, presence_penalty` | no |
| `K31-P026-param-fixed-frequency-penalty/case.json` | `k31.param_fixed_frequency_penalty` | `parameters` | `equals, non_empty` | `frequency_penalty, max_completion_tokens, messages` | no |
| `K31-P030-top-logprobs-lower-bound/case.json` | `k31.top_logprobs_lower_bound` | `parameters` | `equals, non_empty` | `logprobs, max_completion_tokens, messages, top_logprobs` | no |
| `K31-P031-top-logprobs-upper-bound/case.json` | `k31.top_logprobs_upper_bound` | `parameters` | `equals, non_empty` | `logprobs, max_completion_tokens, messages, top_logprobs` | no |
| `K31-P032-top-logprobs-below-bound/case.json` | `k31.top_logprobs_below_bound` | `parameters` | `equals, non_empty` | `logprobs, max_completion_tokens, messages, top_logprobs` | yes |
| `K31-P033-top-logprobs-above-bound/case.json` | `k31.top_logprobs_above_bound` | `parameters` | `equals, non_empty` | `logprobs, max_completion_tokens, messages, top_logprobs` | yes |
| `K31-P034-top-logprobs-requires-logprobs/case.json` | `k31.top_logprobs_requires_logprobs` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, top_logprobs` | yes |
| `K31-P040-stop-one-item-32-bytes/case.json` | `k31.stop_one_item_32_bytes` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, stop` | no |
| `K31-P041-stop-five-items/case.json` | `k31.stop_five_items` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, stop` | no |
| `K31-P042-stop-six-items/case.json` | `k31.stop_six_items` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, stop` | yes |
| `K31-P043-stop-item-33-bytes/case.json` | `k31.stop_item_33_bytes` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages, stop` | yes |
| `K31-P050-max-completion-minimal/case.json` | `k31.max_completion_minimal` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages` | no |
| `K31-P051-max-completion-context-overflow/case.json` | `k31.max_completion_context_overflow` | `parameters` | `equals, length_equals, non_empty` | `max_completion_tokens, messages` | no |
| `K31-P052-max-completion-over-model-limit/case.json` | `k31.max_completion_over_model_limit` | `parameters` | `equals, non_empty` | `max_completion_tokens, messages` | yes |
| `K31-R001-reasoning-effort-low/case.json` | `k31.reasoning_effort_low` | `reasoning-parameters` | `equals` | `max_completion_tokens, messages, reasoning_effort` | no |
| `K31-R002-reasoning-effort-high/case.json` | `k31.reasoning_effort_high` | `reasoning-parameters` | `equals` | `max_completion_tokens, messages, reasoning_effort` | no |
| `K31-R003-reasoning-effort-max/case.json` | `k31.reasoning_effort_max` | `reasoning-parameters` | `equals` | `max_completion_tokens, messages, reasoning_effort` | no |
| `K31-R004-reasoning-effort-invalid/case.json` | `k31.reasoning_effort_invalid` | `reasoning-parameters` | `equals, non_empty` | `max_completion_tokens, messages, reasoning_effort` | yes |

> This is a structural inventory, not proof of provider-contract coverage.
