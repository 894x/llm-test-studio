# Kimi K3 Responses adaptation and contract matrix

Retrieved: 2026-10-10. Provider: Kimi China; model: `kimi-k3`; endpoint: `POST /v1/responses`.

Scope: the documented native Kimi K3 `POST /v1/responses` request, nested input/tool parameters, synchronous and SSE responses, and replay/cache/search lifecycle. Reuse existing logical Chat Cases and IDs wherever the claim has a native counterpart; add new Cases only for missing native claims. Preserve existing Chat definitions. This expansion defines local fixtures and executable requests, without authorizing new paid provider calls. Documentation retrieved 2026-10-10.

Sources, retrieved 2026-10-10: [Responses API](https://platform.kimi.com/docs/api/responses), [OpenAPI](https://platform.kimi.com/docs/openapi.json), [errors](https://platform.kimi.com/docs/api/errors). `ResponsesToolChoice` lists only `auto`; `ResponsesRequest.reasoning.effort` lists `low`, `high`, `max`; the output-budget description sets 1048576 as the maximum and explicitly separates output budget from total input plus output. Reaching the output budget produces `incomplete` with reason `max_output_tokens`.

Response assertions were audited against the OpenAPI again on **2026-10-11**: [responses-assertion-audit.md](responses-assertion-audit.md). All 254 authored Responses definitions (253 K3 Cases plus the shared base Case) now distinguish optional response properties from scenario semantics. Unsupported default echoes, fixed HTTP error types/messages, reasoning-summary presence and mandatory search results were removed. Returned usage/fixed fields and image-count caps are conditional; request definitions and historical reports are unchanged. No new provider run or unit-test execution accompanies this correction.

## Official execution evidence and correction inventory

Run `a222cb13-40ca-406b-937d-84108383e142`, 2026-10-10 22:15-22:17 Asia/Shanghai, used `https://api.moonshot.cn/v1/responses` and `kimi-k3`. The original 39 definitions produced 28 passes and 11 failures. Source evidence is the immutable local report and per-request observations; no additional provider requests were made for this correction.

| Area / source | Current claim and adjustment | Execution evidence / remaining gap |
|---|---|---|
| `tool_choice`, OpenAPI `ResponsesToolChoice` | Only `auto` is native. Capability/dynamic-tool/name-boundary requests use `auto`. `required` and `none` independently assert HTTP 400 and a required string error message; no fixed error type or wording. | Six original failures were blocked by unsupported choices. The four modified positive requests need a fresh provider run; original fixtures verified request shapes and actual function-call assertions for capability claims. |
| Function-name boundaries, `ResponsesFunctionTool.name` | Accept 1 and 128 characters with valid `auto`; verify HTTP 200 and protocol validity. Tool selection is not the primary name-boundary claim. | Original positive requests failed before their name boundary was tested. |
| Maximum output budget, `ResponsesRequest.max_output_tokens` | 1048576 is a valid output budget; do not assume input plus requested output must fit a combined window. The shared display name now describes the budget rather than a universal overflow rejection. | Original request returned HTTP 200 and completed; corrected assertion matches docs and observation. No near-1M prompt was submitted. |
| Minimum budget and terminal state, same source | HTTP 200, `incomplete`, reason `max_output_tokens`; returned output usage must be <= 1. A documented budget terminal is not a malformed protocol response. | **Documentation conflict:** official output usage was 4 at budget 1 (reasoning usage 1). The bound still applies when the counter is returned; no undocumented accounting allowance is introduced. |
| `reasoning.effort=max`, same request schema | Valid effort may complete with an answer or reach its finite output budget. The latter must have the documented reason; returned usage must stay within the requested budget. Reasoning-item/summary presence is not forced. Failed/filter/unknown terminal states do not pass. | Official output used all 2048 tokens and correctly reported budget truncation. |
| `reasoning.effort=medium`, same schema | Keep HTTP 400 for the value outside the documented enum. | **Documentation conflict:** official request returned HTTP 200. Do not add an undocumented supported enum to make the case pass. |
| Output budget 1048577, same schema | Keep rejection above the documented maximum. | **Documentation conflict:** official request returned HTTP 200. Provider rejection enforcement is not established. |
| HTTP error envelope, `ErrorResponse` | Validate an error object/message for HTTP errors without requiring successful `output`. Keep malformed success/error bodies as protocol issues. | Original rejected inputs passed assertions but were incorrectly marked `invalid_response_shape`; fixed in the decoder. |

Before the parameter expansion below, the native foundation had 39 members and admission had 16 members after adding the two unsupported-choice cases and removing the accepted 1048576-budget case. Live evidence above belongs to the original requests. Local checks and fixture replay are reported separately and do not establish a new 39-request provider pass.

Offline replay on 2026-10-10 used the saved official exchanges with the then-current decoder and assertions. JSON request comparison (excluding only the runtime-bound model) found 35 unchanged requests: 32 passed, and the three documented conflicts above failed. Four requests changed from `required` to `auto` and were excluded from replay conclusions; only their local request/assertion fixtures were verified. Temporary replay code was removed after verification.

## Reviewed existing-case mapping

| Case ID / key | Disposition / expected result | Tier / execution |
|---|---|---|
| `8e41672a-ffa2-5461-998f-96d7cd660dea` / `dynamic_tool` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `a67358ec-7ecc-5399-8660-dc4cd3d259e9` / `dynamic_and_global_tools` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `6f1ed31b-9759-5033-a360-84fb341c9cdb` / `dynamic_tool_with_content` | Responses additional_tools has no documented content exclusion; the Chat rejection cannot be reused. | not applicable; excluded from this native run |
| `6bb8af63-4d83-54d0-89f5-ff661ee93007` / `frequency_penalty_above_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `7e2327a4-9f1f-5afd-90c7-4f231dfc0c97` / `frequency_penalty_below_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `28d60522-c7f4-5e42-b954-c743322c88e1` / `json_schema_missing_name` | HTTP 200; native assertions | T1; original request in official run above |
| `50361472-cfbb-58a4-8f73-9bb3138a8ca0` / `json_schema_missing_schema` | HTTP 400; native assertions | T2; original request in official run above |
| `014dee74-a1f7-5539-abff-18522d477ca4` / `json_schema_non_object` | HTTP 400; native assertions | T2; original request in official run above |
| `b5e3ea2a-68e4-54b9-9e5f-2d69565d029f` / `max_completion_context_overflow` | HTTP 200 at native output maximum; no combined-window assertion | T1; original request in official run above |
| `510151ef-a1fc-53e3-8d15-f0b28842a33e` / `max_completion_minimal` | HTTP 200/incomplete with budget reason; strict usage bound has official documentation conflict | T1; original request in official run above |
| `7a9bf03b-d94b-5ffd-ba80-af3b626c611d` / `max_completion_over_model_limit` | HTTP 400; official documentation conflict (observed 200) | T2; original request in official run above |
| `42c593e4-21cb-5d86-80de-ce6dad5ed521` / `message_empty_content` | Responses schema defines no minItems/minLength for this empty shape; a 400 expectation is not established. | not applicable; excluded from this native run |
| `30a83b00-ad15-5200-93a6-5a4f3e24f993` / `messages_empty` | Responses schema defines no minItems/minLength for this empty shape; a 400 expectation is not established. | not applicable; excluded from this native run |
| `8ac55529-2b31-5b3a-bbd6-7c4d54ddd69c` / `messages_required` | HTTP 400; native assertions | T2; original request in official run above |
| `18ed9e4f-9572-53ca-952c-24ef738261e3` / `must.json_object` | HTTP 400; native assertions | T2; original request in official run above |
| `57d07366-b7bf-57cc-a36e-886febee83ea` / `must.json_schema` | HTTP 200; native assertions | T1; original request in official run above |
| `7df57c19-7d18-566a-998e-86342b7cee32` / `must.stream_integrity` | HTTP 200; native assertions | T1; original request in official run above |
| `68273e8a-309a-5f7c-9250-f44c5f4541ef` / `must.thinking_default` | HTTP 200; native assertions | T1; original request in official run above |
| `bd551b6e-481a-5f33-8b2e-c9b9a7eb7595` / `must.tool_call` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `1d52525c-bf36-57ab-a73e-995f3f8c7809` / `must.tool_choice_auto` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `75e1aa9b-4e08-5eff-8095-147ad690daac` / `must.tool_choice_default` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `634b8cc2-8dc6-538d-9d29-c18eb8620ad8` / `must.tool_choice_none` | HTTP 400; unsupported native enum | T2; original request in official run above |
| `e5280550-a6ca-5d9d-bb65-efce43c73a7b` / `must.tool_choice_required` | HTTP 400; unsupported native enum | T2; original request in official run above |
| `0edb2efc-f185-51e7-bf4e-7dcfeef33c96` / `must.usage_non_stream` | HTTP 200; native assertions | T1; original request in official run above |
| `c6b97ab0-a95c-53f9-ab6e-c949d5454b90` / `must.usage_stream` | HTTP 200; native assertions | T1; original request in official run above |
| `938445fb-a68e-5c2c-9ba7-201a807aac98` / `n_above_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `68dd2502-9621-5dd2-a2cd-632d4549bd95` / `n_below_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `2cef4d7e-afc0-5b7d-a5bf-203739780f6d` / `optional.tool_choice_function` | HTTP 400; native specified-function rejection | T2; original request in official run above |
| `2d2995c3-9766-52e4-be29-72a137d9b55d` / `param_omit_sampling` | HTTP 200; native assertions | T1; original request in official run above |
| `bd98f036-7086-5bfd-a416-d4cd73173a92` / `partial_mode` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `38ba86ed-9950-5e85-bd84-25f51c463084` / `prediction_invalid_type` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `8499cb35-c047-535b-82e7-a623c87a1647` / `prediction_string` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `f826a581-94d8-51b8-8404-8a152962a2e0` / `prediction_text_array` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `2f0d47ff-a16f-5d4b-bf9b-9c3320dafd15` / `presence_penalty_above_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `3ead7ae5-aef0-5f89-87e2-196941e2f448` / `presence_penalty_below_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `06d41641-33ad-57e3-a2a0-bd02889371c4` / `prompt_cache_key` | HTTP 200; native assertions | T1; original request in official run above |
| `4d87e691-dbe0-54be-b81b-c7c9669b4745` / `reasoning_effort_high` | HTTP 200; native assertions | T1; original request in official run above |
| `6a7a187d-d556-5d2c-b4b6-29f961621c49` / `reasoning_effort_invalid` | HTTP 400; official documentation conflict (observed 200) | T2; original request in official run above |
| `e7958fd9-7c92-527d-a1d4-4010ccd9e89c` / `reasoning_effort_low` | HTTP 200; native assertions | T1; original request in official run above |
| `34e241fa-9d4d-55d0-b3c2-49413480e8e9` / `reasoning_effort_max` | HTTP 200; completed or documented budget truncation | T1; original request in official run above |
| `88975a47-466b-5bc3-a362-9ec9aa2ad157` / `response_format_invalid_type` | HTTP 400; native assertions | T2; original request in official run above |
| `ea56b579-8797-527d-8fb7-bd4771ab3a02` / `safety_identifier` | HTTP 200; native assertions | T1; original request in official run above |
| `f909093d-609d-57b8-8523-944a269c373e` / `stop_five_items` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `cdd837d0-050e-547c-b20e-eb4d4e6b0953` / `stop_item_33_bytes` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `d61a5b9b-c6a6-5ff7-b829-c1e1d42d81ee` / `stop_one_item_32_bytes` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `6a8fbf9c-9c6a-59f6-bd82-04c1b103b546` / `stop_six_items` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `b3f4ad68-bba6-5b4a-8f24-fd0b69c4e843` / `temperature_above_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `6c855673-6922-5312-964f-5761179befff` / `temperature_below_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `138f994f-01a2-575a-ad34-48d66074caa8` / `tool_name_128_chars` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `f9e4de49-af2d-596b-b48c-d2814bc4a6a5` / `tool_name_129_chars` | HTTP 400; native assertions | T2; original request in official run above |
| `f353bd16-2a5c-5fc8-9244-5c4738cf54c7` / `tool_name_invalid_first_char` | HTTP 400; native assertions | T2; original request in official run above |
| `12a02b6c-8cbb-5fc3-8e69-4f8c578b2aa8` / `tool_name_one_char` | HTTP 200; native auto choice and response assertions | T1; original request in official run above |
| `48115c46-d22a-50cd-9b6a-4588acd94f45` / `top_logprobs_above_bound` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `8d016c4b-068f-597d-bed0-b1ea6e5bedb4` / `top_logprobs_below_bound` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `9e68cea7-2348-58e4-9a70-1d3ebc5cd759` / `top_logprobs_lower_bound` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `d940a071-4d95-5360-b01e-eaaed9dbfbf8` / `top_logprobs_requires_logprobs` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `a93debd0-da12-53ad-92dc-21010488fa69` / `top_logprobs_upper_bound` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `fa3dffab-2d9b-5ee7-a13e-ee1373b4e1f1` / `top_p_above_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `21aa488a-faca-55b0-b3ea-bb38e7cbfb15` / `top_p_below_fixed` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `81dbf546-625f-5fd7-ae9d-b21db4c233d6` / `must.image_url_object` | HTTP 400; native assertions | T2; original request in official run above |
| `b007bcfc-612e-5b82-afd2-8b79cff874b0` / `must.image_url_string` | HTTP 200; native assertions | T3; original request in official run above |
| `40d1a659-1525-562b-a081-904e14c598ab` / `must.video_url_object` | Native HTTP400 rejection definition added in this expansion; see added-case index. | T2; newly defined, not provider-executed |
| `35a12d23-7847-5393-80e0-14f3cb5fe1a6` / `must.video_url_string` | Native HTTP400 rejection definition added in this expansion; see added-case index. | T2; newly defined, not provider-executed |
| `e9202e63-c05b-5392-b353-148f20859064` / `must.multimodal_type_required` | HTTP 400; native assertions | T2; original request in official run above |
| `48ae6055-0345-5c82-af8f-203dc1f41cc0` / `must.multimodal_text_required` | HTTP 400; native assertions | T2; original request in official run above |
| `2f43514d-ac8d-5878-9ccb-e36ab3a93ea2` / `must.multimodal_image_url_required` | HTTP 400; native assertions | T2; original request in official run above |
| `9534cda3-dc0c-58a3-ad0e-2d724480bda1` / `must.multimodal_video_url_required` | Native HTTP400 rejection definition added in this expansion; see added-case index. | T2; newly defined, not provider-executed |
| `b0b3e328-1b42-5b50-ab68-c578a5181889` / `must.image_url_object_url_required` | Native HTTP400 rejection definition added in this expansion; see added-case index. | T2; newly defined, not provider-executed |
| `3acc5571-9386-532d-859a-8f3a411c39b1` / `must.video_url_object_url_required` | Native HTTP400 rejection definition added in this expansion; see added-case index. | T2; newly defined, not provider-executed |
| `37836b34-b6de-5046-a8d3-8d0808ead040` / `enable_thinking_true_active` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `cdefc202-0d6f-57cf-94bf-ee62fb809268` / `enable_thinking_false_effective` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `1e211369-72e6-54ec-bd51-7effd6dafc21` / `thinking_type_disabled_effective` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `ea4a2e8e-9ee1-5cad-bd44-cbaa8c946f06` / `thinking_type_enabled_active` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `f83f1965-54b4-55d1-985c-a415c2302098` / `chat_template_kwargs_enable_thinking_on` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `54b2e593-be5c-5e03-aed1-fda2e70d3f35` / `chat_template_kwargs_enable_thinking_off` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `18191522-c7c0-5111-bbb2-17c4e447e775` / `chat_template_kwargs_thinking_on` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `8bd5d716-7194-5b22-b6be-02c7b6f81ebf` / `chat_template_kwargs_thinking_off` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `e613c90c-b2ac-5fff-aebf-5299f67d06b3` / `param_fixed_temperature` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `e07aa24b-e46f-5943-bec6-82b3907e201e` / `param_fixed_top_p` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `d2f682b3-b1d4-595a-a87e-74f3e25a2d92` / `param_fixed_n` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `b9b0a69b-a588-5b1b-82e6-ade147b02b8b` / `param_fixed_presence_penalty` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `84a9c36f-74aa-5d59-b2b0-20993f121cf8` / `param_fixed_frequency_penalty` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `5e1eceec-536d-5ab1-a11c-40a1a99fccea` / `k3.stop_runtime_truncation` | No documented Responses counterpart for this Chat claim; not sent or silently dropped. | not applicable; excluded from this native run |
| `4f9e20a8-1f28-5c13-903e-8a3959f5904a` / `k3.cache_prefix_hit` | HTTP 200; native assertions | T3; original request in official run above |

## Behavioral inventory and remaining gaps

| Contract | Mandatory scenarios / expected result | Current implementation / state |
|---|---|---|
| Method, Content-Type, Bearer authentication | POST/native route/JSON; missing or invalid credentials →401 | Native route and local mocks; provider credential mutations are Run-owned: blocked |
| Signature nonce and returned signature headers | absent/valid nonce and malformed/multiple nonce behavior | Case-defined headers unavailable: blocked |
| model | bound kimi-k3; missing/null/wrong/unknown model | Run-owned model; Case boundary cannot override it: blocked |
| input | string/array; missing/null/wrong outer/item types →400 | Existing omission plus K3 input/message Cases: covered for documented union; empty rejection unpublished |
| message type/role/content/status | type omitted/message; user/assistant/developer; string/parts; required fields, wrong enum/type | Message Cases and output_text history: covered for selected documented partitions |
| instructions | omitted/string/non-string; top-level instruction used for sentinel answer | K3 instructions and instructions-type: covered; no invented precedence against contradictory same-priority instructions |
| stream and SSE lifecycle | false/true/default; start/delta/final; sequence integer and monotonic order; budget/failed terminals | Existing streaming/usage plus K3 stream-event-envelope; sequence presence/type covered; monotonicity and custom/search intermediate-event assertions remain partial |
| max_output_tokens | omission, interior,1,1048576,1048577, integer types; returned budget usage | Existing limits plus omission/fraction/string Cases; omitted-budget acceptance does not establish the 131072 behavioral default. Official conflicts at 1 and 1048577 retained; exact near-1M input deferred |
| reasoning.effort | omitted/empty object; low/high/max; wrong enum/type →400 | Existing enums plus empty-object/wrong-type Cases; no forced max echo or reasoning summary. Actual default effort remains unverified; official medium acceptance conflict retained |
| text.format | required type/schema; name omitted/explicit; strict false/true; wrong object/field types; exact JSON values | Existing format Cases plus K3 schema/text/format Cases: covered for documented fields; schema internals are free-form |
| function tools | flat fields; valid name lengths/pattern,129/empty/bad characters; optional fields/strict; native call | Existing tool name limits plus K3 function Cases: covered for selected request partitions and native call evidence |
| tool_choice | omitted/auto; required/none/specified/allowed_tools/unknown/wrong type →400 | Existing choices plus K3 unknown/type: covered; reused allowed_tools keeps its prior disabled metadata in optional profile |
| additional_tools | developer/tools required, optional id, declaration position/coexistence | Existing dynamic call plus K3 malformed declaration Cases; exact before/after-position replay remains partial |
| namespace/custom apply_patch | namespace required fields/child union; custom apply_patch grammar+lark; native call and namespace | K3 namespace/custom Cases: covered for selected field and child partitions; no Chat conversion |
| function/custom call and output replay | client-authored matching IDs, optional completed status, string/parts results; missing/type/mismatched IDs/invalid argument JSON →400 | K3 history Cases: covered for authored input contract; dynamically executing arbitrary provider tool calls remains deferred |
| reasoning replay | original provider output preserved; content over summary | Malformed nested Cases plus disabled K3 reasoning-provider-replay; exact output reference fixture verified; summary/content precedence and optional positive shapes remain partial/T3 |
| web_search history | retained history ignored by conversion; optional action/id/status malformed | K3 search-history Cases: covered for history shape and sentinel answer |
| image content | data URL string, required fields, HTTP URL rejection, four detail values/type | Existing PNG7 and required/object Cases plus K3 image URL/detail Cases; explicit detail success Cases implemented disabled T3 |
| video content | Chat video_url string/object, missing fields; native input_video outside documented closed union | F023/F024/F029/F031 reused native HTTP400; new K3 input-video-unsupported; covered as rejection, never native video capability |
| web_search | at most1, domains0/1/100/101, content text/image, max_results0/1/5/10/11, default3, caption and ignored/forbidden fields | K3 search rejection Cases enabled T2; positive Cases disabled T3; returned image caps checked conditionally without requiring search or non-empty results. Retrieval/filtering/caption behavior remains partial |
| include | two allowed values, both/omitted, unknown/wrong types, no-search ineffectiveness | K3 include/type/no-search plus three independent search include Cases; positive search Cases disabled T3; no mandatory sources/results assertion |
| cache key/options | implicit5m default,5m/1h, invalid mode/ttl/types; hit/write, expiry/refresh/org isolation | Existing hit workflow plus K3 options/default/type Cases; cache TTL/refresh/isolation remain deferred T3 without timed executable scenarios |
| safety_identifier | omitted/hashed string/non-string | Existing hashed string plus K3 wrong type: covered |
| response envelope/usage | optional fields, native output variants, returned usage arithmetic and documented fixed values | Optional counters/fixed fields checked only when returned; ID/model/output/summary presence not forced. Full optional-output/timestamp/accounting invariants remain partial |
| Errors/limits/safety | HTTP400 with required string error.message;401/403/429, overload/server/content filter | Parameter rejection and local protocol fixtures; authenticated quota/rate/safety failure environments blocked/deferred |

Unknown empty-input rules, integer lower limit, tool-count and input-array cardinality are not invented from the generic OpenAI contract.

## Structural inventory

Every official Responses request/response/SSE field is listed below and maps to the behavioral area above. Required is relative to its parent variant. Optional fields require omission, representative valid, and wrong-type partitions; enums require explicit valid values and unknown/type probes; declared ordered limits require endpoints and adjacent rejection values. Fields without primary-claim cases remain partial or missing as stated above. JSON Schema internals are free-form user schemas; no undocumented keyword limits are inferred.

| Field / variant | Type | Presence | Published constraints/default |
|---|---|---|---|
| `request` | `object` | optional | `not specified` |
| `request.model` | `string` | required | `not specified` |
| `request.input` | `union` | required | `not specified` |
| `request.input[0]` | `string` | required | `not specified` |
| `request.input[1]` | `array` | required | `not specified` |
| `request.input[1][]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem].type` | `string` | optional | `{"enum": ["message"]}` |
| `request.input[1][][ResponsesMessageItem].role` | `string` | required | `{"enum": ["user", "assistant", "developer"]}` |
| `request.input[1][][ResponsesMessageItem].content` | `union` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[0]` | `string` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1]` | `array` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入文本].type` | `string` | required | `{"enum": ["input_text"]}` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入图片]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入图片].type` | `string` | required | `{"enum": ["input_image"]}` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入图片].image_url` | `string` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输入图片].detail` | `string` | optional | `{"enum": ["auto", "low", "high", "original"]}` |
| `request.input[1][][ResponsesMessageItem].content[1][][输出文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesMessageItem].content[1][][输出文本].type` | `string` | required | `{"enum": ["output_text"]}` |
| `request.input[1][][ResponsesMessageItem].content[1][][输出文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesMessageItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesReasoningItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].type` | `string` | required | `{"enum": ["reasoning"]}` |
| `request.input[1][][ResponsesReasoningItem].id` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].summary` | `array` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].summary[]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].summary[].type` | `string` | required | `{"enum": ["summary_text"]}` |
| `request.input[1][][ResponsesReasoningItem].summary[].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesReasoningItem].content` | `array` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].content[]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesReasoningItem].content[].type` | `string` | required | `{"enum": ["reasoning_text"]}` |
| `request.input[1][][ResponsesReasoningItem].content[].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesReasoningItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesFunctionCallItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].type` | `string` | required | `{"enum": ["function_call"]}` |
| `request.input[1][][ResponsesFunctionCallItem].id` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].call_id` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].name` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].namespace` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].arguments` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].type` | `string` | required | `{"enum": ["function_call_output"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem].call_id` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output` | `union` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[0]` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1]` | `array` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入文本].type` | `string` | required | `{"enum": ["input_text"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入图片]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入图片].type` | `string` | required | `{"enum": ["input_image"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入图片].image_url` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输入图片].detail` | `string` | optional | `{"enum": ["auto", "low", "high", "original"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输出文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输出文本].type` | `string` | required | `{"enum": ["output_text"]}` |
| `request.input[1][][ResponsesFunctionCallOutputItem].output[1][][输出文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesFunctionCallOutputItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesCustomToolCallItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].type` | `string` | required | `{"enum": ["custom_tool_call"]}` |
| `request.input[1][][ResponsesCustomToolCallItem].id` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].call_id` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].name` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].namespace` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].input` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].type` | `string` | required | `{"enum": ["custom_tool_call_output"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].call_id` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output` | `union` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[0]` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1]` | `array` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入文本].type` | `string` | required | `{"enum": ["input_text"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入图片]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入图片].type` | `string` | required | `{"enum": ["input_image"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入图片].image_url` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输入图片].detail` | `string` | optional | `{"enum": ["auto", "low", "high", "original"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输出文本]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输出文本].type` | `string` | required | `{"enum": ["output_text"]}` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].output[1][][输出文本].text` | `string` | required | `not specified` |
| `request.input[1][][ResponsesCustomToolCallOutputItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesWebSearchCallItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesWebSearchCallItem].type` | `string` | required | `{"enum": ["web_search_call"]}` |
| `request.input[1][][ResponsesWebSearchCallItem].id` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesWebSearchCallItem].status` | `string` | optional | `{"enum": ["completed"]}` |
| `request.input[1][][ResponsesWebSearchCallItem].action` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesWebSearchCallItem].action.type` | `string` | optional | `{"enum": ["search"]}` |
| `request.input[1][][ResponsesWebSearchCallItem].action.query` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].type` | `string` | required | `{"enum": ["additional_tools"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].id` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].role` | `string` | required | `{"enum": ["developer"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools` | `array` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].type` | `string` | required | `{"enum": ["namespace"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].name` | `string` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].description` | `string` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools` | `array` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[]` | `union` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool]` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].type` | `string` | required | `{"enum": ["web_search"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].filters` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].filters.allowed_domains` | `array` | optional | `{"maxItems": 100}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].filters.allowed_domains[]` | `string` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].search_content_types` | `array` | optional | `{"default": ["text"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].search_content_types[]` | `string` | optional | `{"enum": ["text", "image"]}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].image_settings` | `object` | optional | `not specified` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].image_settings.max_results` | `integer` | optional | `{"default": 3, "minimum": 1, "maximum": 10}` |
| `request.input[1][][ResponsesAdditionalToolsItem].tools[][ResponsesWebSearchTool].image_settings.caption` | `boolean` | optional | `{"default": false}` |
| `request.instructions` | `string` | optional | `not specified` |
| `request.stream` | `boolean` | optional | `{"default": false}` |
| `request.max_output_tokens` | `integer` | optional | `not specified` |
| `request.reasoning` | `object` | optional | `not specified` |
| `request.reasoning.effort` | `string` | optional | `{"enum": ["low", "high", "max"], "default": "max"}` |
| `request.text` | `object` | optional | `not specified` |
| `request.text.format` | `object` | optional | `not specified` |
| `request.text.format.type` | `string` | required | `{"enum": ["json_schema"]}` |
| `request.text.format.name` | `string` | optional | `not specified` |
| `request.text.format.schema` | `object` | required | `not specified` |
| `request.text.format.strict` | `boolean` | optional | `not specified` |
| `request.tools` | `array` | optional | `not specified` |
| `request.tools[]` | `union` | optional | `not specified` |
| `request.tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `request.tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `request.tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `request.tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `request.tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `request.tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `request.tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `request.tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `request.tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `request.tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `request.tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `request.tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `request.tools[][ResponsesNamespaceTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].type` | `string` | required | `{"enum": ["namespace"]}` |
| `request.tools[][ResponsesNamespaceTool].name` | `string` | required | `not specified` |
| `request.tools[][ResponsesNamespaceTool].description` | `string` | required | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools` | `array` | required | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[]` | `union` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `request.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `request.tools[][ResponsesWebSearchTool]` | `object` | optional | `not specified` |
| `request.tools[][ResponsesWebSearchTool].type` | `string` | required | `{"enum": ["web_search"]}` |
| `request.tools[][ResponsesWebSearchTool].filters` | `object` | optional | `not specified` |
| `request.tools[][ResponsesWebSearchTool].filters.allowed_domains` | `array` | optional | `{"maxItems": 100}` |
| `request.tools[][ResponsesWebSearchTool].filters.allowed_domains[]` | `string` | optional | `not specified` |
| `request.tools[][ResponsesWebSearchTool].search_content_types` | `array` | optional | `{"default": ["text"]}` |
| `request.tools[][ResponsesWebSearchTool].search_content_types[]` | `string` | optional | `{"enum": ["text", "image"]}` |
| `request.tools[][ResponsesWebSearchTool].image_settings` | `object` | optional | `not specified` |
| `request.tools[][ResponsesWebSearchTool].image_settings.max_results` | `integer` | optional | `{"default": 3, "minimum": 1, "maximum": 10}` |
| `request.tools[][ResponsesWebSearchTool].image_settings.caption` | `boolean` | optional | `{"default": false}` |
| `request.tool_choice` | `string` | optional | `{"enum": ["auto"]}` |
| `request.include` | `array` | optional | `not specified` |
| `request.include[]` | `string` | optional | `{"enum": ["web_search_call.results", "web_search_call.action.sources"]}` |
| `request.prompt_cache_key` | `string` | optional | `not specified` |
| `request.prompt_cache_options` | `object` | optional | `not specified` |
| `request.prompt_cache_options.mode` | `string` | optional | `{"enum": ["implicit"], "default": "implicit"}` |
| `request.prompt_cache_options.ttl` | `string` | optional | `{"enum": ["5m", "1h"], "default": "5m"}` |
| `request.safety_identifier` | `string` | optional | `not specified` |
| `response` | `object` | optional | `not specified` |
| `response.id` | `string` | optional | `not specified` |
| `response.object` | `string` | optional | `{"enum": ["response"]}` |
| `response.created_at` | `integer` | optional | `not specified` |
| `response.completed_at` | `['integer', 'null']` | optional | `not specified` |
| `response.status` | `string` | optional | `{"enum": ["in_progress", "completed", "incomplete", "failed"]}` |
| `response.model` | `string` | optional | `not specified` |
| `response.output` | `array` | optional | `not specified` |
| `response.output[]` | `union` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].type` | `string` | optional | `{"enum": ["reasoning"]}` |
| `response.output[][ResponsesOutputReasoningItem].id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].summary` | `array` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].summary[]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].summary[].type` | `string` | optional | `{"enum": ["summary_text"]}` |
| `response.output[][ResponsesOutputReasoningItem].summary[].text` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].encrypted_content` | `['string', 'null']` | optional | `not specified` |
| `response.output[][ResponsesOutputReasoningItem].status` | `string` | optional | `{"enum": ["in_progress", "completed"]}` |
| `response.output[][ResponsesOutputMessageItem]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].type` | `string` | optional | `{"enum": ["message"]}` |
| `response.output[][ResponsesOutputMessageItem].id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].role` | `string` | optional | `{"enum": ["assistant"]}` |
| `response.output[][ResponsesOutputMessageItem].content` | `array` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].content[]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].content[].type` | `string` | optional | `{"enum": ["output_text"]}` |
| `response.output[][ResponsesOutputMessageItem].content[].text` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].content[].annotations` | `array` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].content[].annotations[]` | `union` | optional | `not specified` |
| `response.output[][ResponsesOutputMessageItem].status` | `string` | optional | `{"enum": ["in_progress", "completed"]}` |
| `response.output[][ResponsesOutputFunctionCallItem]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].type` | `string` | optional | `{"enum": ["function_call"]}` |
| `response.output[][ResponsesOutputFunctionCallItem].id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].call_id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].name` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].namespace` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].arguments` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputFunctionCallItem].status` | `string` | optional | `{"enum": ["in_progress", "completed"]}` |
| `response.output[][ResponsesOutputCustomToolCallItem]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].type` | `string` | optional | `{"enum": ["custom_tool_call"]}` |
| `response.output[][ResponsesOutputCustomToolCallItem].id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].call_id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].name` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].namespace` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].input` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputCustomToolCallItem].status` | `string` | optional | `{"enum": ["in_progress", "completed"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].type` | `string` | optional | `{"enum": ["web_search_call"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem].id` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].status` | `string` | optional | `{"enum": ["in_progress", "completed"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem].action` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.type` | `string` | optional | `{"enum": ["search"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.query` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.sources` | `array` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.sources[]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.sources[].type` | `string` | optional | `{"enum": ["url"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.sources[].url` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].action.sources[].title` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results` | `array` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[]` | `object` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[].type` | `string` | optional | `{"enum": ["image_result"]}` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[].image_url` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[].thumbnail_url` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[].source_website_url` | `string` | optional | `not specified` |
| `response.output[][ResponsesOutputWebSearchCallItem].results[].caption` | `string` | optional | `not specified` |
| `response.usage` | `union` | optional | `not specified` |
| `response.usage[ResponsesUsage]` | `object` | optional | `not specified` |
| `response.usage[ResponsesUsage].input_tokens` | `integer` | optional | `not specified` |
| `response.usage[ResponsesUsage].input_tokens_details` | `object` | optional | `not specified` |
| `response.usage[ResponsesUsage].input_tokens_details.cached_tokens` | `integer` | optional | `not specified` |
| `response.usage[ResponsesUsage].input_tokens_details.cache_write_tokens` | `integer` | optional | `not specified` |
| `response.usage[ResponsesUsage].output_tokens` | `integer` | optional | `not specified` |
| `response.usage[ResponsesUsage].output_tokens_details` | `object` | optional | `not specified` |
| `response.usage[ResponsesUsage].output_tokens_details.reasoning_tokens` | `integer` | optional | `not specified` |
| `response.usage[ResponsesUsage].total_tokens` | `integer` | optional | `not specified` |
| `response.usage[1]` | `null` | optional | `not specified` |
| `response.incomplete_details` | `['object', 'null']` | optional | `not specified` |
| `response.incomplete_details.reason` | `string` | optional | `{"enum": ["max_output_tokens", "content_filter"]}` |
| `response.error` | `['object', 'null']` | optional | `not specified` |
| `response.error.code` | `string` | optional | `not specified` |
| `response.error.message` | `string` | optional | `not specified` |
| `response.instructions` | `['string', 'null']` | optional | `not specified` |
| `response.reasoning` | `['object', 'null']` | optional | `not specified` |
| `response.text` | `['object', 'null']` | optional | `not specified` |
| `response.tools` | `['array', 'null']` | optional | `not specified` |
| `response.tools[]` | `union` | optional | `not specified` |
| `response.tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `response.tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `response.tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `response.tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `response.tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `response.tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `response.tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `response.tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `response.tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `response.tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `response.tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `response.tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `response.tools[][ResponsesNamespaceTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].type` | `string` | required | `{"enum": ["namespace"]}` |
| `response.tools[][ResponsesNamespaceTool].name` | `string` | required | `not specified` |
| `response.tools[][ResponsesNamespaceTool].description` | `string` | required | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools` | `array` | required | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[]` | `union` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].type` | `string` | required | `{"enum": ["function"]}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].name` | `string` | required | `{"pattern": "^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$"}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].description` | `string` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].parameters` | `object` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesFunctionTool].strict` | `boolean` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].type` | `string` | required | `{"enum": ["custom"]}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].name` | `string` | required | `{"enum": ["apply_patch"]}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].description` | `string` | optional | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format` | `object` | required | `not specified` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.type` | `string` | required | `{"enum": ["grammar"]}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.syntax` | `string` | required | `{"enum": ["lark"]}` |
| `response.tools[][ResponsesNamespaceTool].tools[][ResponsesCustomTool].format.definition` | `string` | required | `not specified` |
| `response.tools[][ResponsesWebSearchTool]` | `object` | optional | `not specified` |
| `response.tools[][ResponsesWebSearchTool].type` | `string` | required | `{"enum": ["web_search"]}` |
| `response.tools[][ResponsesWebSearchTool].filters` | `object` | optional | `not specified` |
| `response.tools[][ResponsesWebSearchTool].filters.allowed_domains` | `array` | optional | `{"maxItems": 100}` |
| `response.tools[][ResponsesWebSearchTool].filters.allowed_domains[]` | `string` | optional | `not specified` |
| `response.tools[][ResponsesWebSearchTool].search_content_types` | `array` | optional | `{"default": ["text"]}` |
| `response.tools[][ResponsesWebSearchTool].search_content_types[]` | `string` | optional | `{"enum": ["text", "image"]}` |
| `response.tools[][ResponsesWebSearchTool].image_settings` | `object` | optional | `not specified` |
| `response.tools[][ResponsesWebSearchTool].image_settings.max_results` | `integer` | optional | `{"default": 3, "minimum": 1, "maximum": 10}` |
| `response.tools[][ResponsesWebSearchTool].image_settings.caption` | `boolean` | optional | `{"default": false}` |
| `response.tool_choice` | `union` | optional | `not specified` |
| `response.tool_choice[ResponsesToolChoice]` | `string` | optional | `{"enum": ["auto"]}` |
| `response.tool_choice[1]` | `null` | optional | `not specified` |
| `response.max_output_tokens` | `['integer', 'null']` | optional | `not specified` |
| `response.temperature` | `['number', 'null']` | optional | `not specified` |
| `response.top_p` | `['number', 'null']` | optional | `not specified` |
| `response.metadata` | `['object', 'null']` | optional | `not specified` |
| `response.parallel_tool_calls` | `boolean` | optional | `not specified` |
| `response.service_tier` | `['string', 'null']` | optional | `not specified` |
| `response.store` | `boolean` | optional | `not specified` |
| `response.background` | `['boolean', 'null']` | optional | `not specified` |
| `response.previous_response_id` | `['string', 'null']` | optional | `not specified` |
| `response.conversation` | `['object', 'null']` | optional | `not specified` |
| `response.prompt_cache_options` | `object` | optional | `not specified` |
| `response.prompt_cache_options.mode` | `string` | optional | `{"enum": ["implicit"]}` |
| `response.prompt_cache_options.ttl` | `string` | optional | `{"enum": ["5m", "1h"]}` |
| `SSE` | `object` | optional | `not specified` |
| `SSE.type` | `string` | required | `{"enum": ["response.created", "response.in_progress", "response.output_item.added", "response.output_item.done", "response.content_part.added", "response.content_part.done", "response.output_text.delta", "response.output_text.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed", "response.completed", "response.incomplete", "response.failed", "error"]}` |
| `SSE.sequence_number` | `integer` | required | `not specified` |

## Execution profiles and verification

- Connectivity: 3 Cases. Foundation: **230** Cases. Expected success: **61** enabled automatic HTTP200 Cases. Admission: **169** HTTP400 Cases. Optional extended: **23** disabled Cases (22 T3 and the previously disabled allowed_tools Case).
- The expected-success profile preserves foundation order and contains its positive capabilities and valid parameter boundaries, including the image fixture/cache workflow. It excludes admission and disabled Cases; documented output-budget truncation may correctly end in `incomplete` with `max_output_tokens`. Semantic and explicit budget-terminal assertions remain; optional response/usage fields follow the 2026-10-11 assertion audit. Expected success is an authored expectation, not a claim that a new provider run passed.
- This expansion adds **214 native definitions**: 6 existing Chat IDs reused, plus 208 new native-only Cases for missing claims. There are now 253 K3 native Cases; 39 of the original 84 K3 Chat Cases remain Chat-only because their claim has no documented native counterpart.
- Reviewed pre-generation selection: [responses-boundary-plan.md](responses-boundary-plan.md). Every added definition, primary delta, source schema, stable ID, expectation, tier and enabled status is listed in [responses-added-case-index.md](responses-added-case-index.md).
- Foundation preserves its existing image fixture/cache workflow. New paid retrieval/image-detail/provider-replay Cases are disabled and excluded from foundation/admission. Adding Cases does not execute provider requests.
- The original expansion T0 checks validated every then-current Case/Suite, native error requests without normalization, HTTP200 rejection failures, exact semantic JSON/text, namespace/custom call evidence, result-count boundaries, sequence-number presence, and unchanged provider-output replay. That is historical local fixture evidence, not a new provider run or verification of the 2026-10-11 edits.
- The six reused Cases preserve their ID, global metadata and complete Chat request/assertions. Existing positive native requests continue to use auto; required/none rejection Cases remain intact.
- The Run selects openai-responses on a supporting model/channel. No authored Case owns the model, credential or URL. No internal schema version or compatibility path changed; historical reports remain unchanged.
- Full lifecycle coverage is still incomplete: nonce/auth/model mutation requires runner/environment support; exact tokenizer context boundaries, rate/content-filter conditions, cache expiry/refresh/isolation require controlled execution; stream ordering/custom/search intermediate events, dynamic declaration scope, reasoning content precedence and complete optional response invariants remain partial as detailed above. No undocumented bounds or historical live results are presented as new coverage.
