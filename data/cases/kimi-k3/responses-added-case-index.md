# Kimi K3 Responses added-case index

Source: [https://platform.kimi.com/docs/openapi.json](https://platform.kimi.com/docs/openapi.json), retrieved 2026-10-10. Model `kimi-k3`; POST `/v1/responses`.

Response assertions corrected 2026-10-11: [responses-assertion-audit.md](responses-assertion-audit.md). Current rejection outcomes require only HTTP400 and the required string `error.message`; optional response metadata and search results are not forced. The original T0 evidence below predates this correction.

Pre-generation selection: [responses-boundary-plan.md](responses-boundary-plan.md). All below have T0 loading/rendering verification; no live provider execution. T3 Cases are disabled and excluded from the foundation. Existing IDs and Chat definitions are preserved.

| Case key / stable ID | Official schema / primary delta | Expected outcome | Tier / implemented status |
|---|---|---|---|
| `must.video_url_object` / `40d1a659-1525-562b-a081-904e14c598ab` | `ResponsesInputContentPart`; video_url discriminator (Chat payload retained) | HTTP400 + required string error.message | T2 enabled; reused Chat ID |
| `must.video_url_string` / `35a12d23-7847-5393-80e0-14f3cb5fe1a6` | `ResponsesInputContentPart`; video_url discriminator (Chat payload retained) | HTTP400 + required string error.message | T2 enabled; reused Chat ID |
| `must.multimodal_video_url_required` / `9534cda3-dc0c-58a3-ad0e-2d724480bda1` | `ResponsesInputContentPart`; video_url discriminator (Chat payload retained) | HTTP400 + required string error.message | T2 enabled; reused Chat ID |
| `must.video_url_object_url_required` / `3acc5571-9386-532d-859a-8f3a411c39b1` | `ResponsesInputContentPart`; video_url discriminator (Chat payload retained) | HTTP400 + required string error.message | T2 enabled; reused Chat ID |
| `must.image_url_object_url_required` / `b0b3e328-1b42-5b50-ab68-c578a5181889` | `ResponsesInputContentPart`; F030-image-url-object-url-required | HTTP400 + required string error.message | T2 enabled; reused Chat ID |
| `must.tool_choice_allowed_tools` / `8e47eb42-de53-574f-803f-b16238289bb6` | `ResponsesToolChoice`; F012-tool-choice-allowed-tools | HTTP400 + required string error.message | T2 disabled; reused Chat ID |
| `k3.responses.input_string` / `bc670eff-958d-4fb3-b851-1fb5087017ea` | `ResponsesRequest`; input=string | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.message_type_omitted` / `1a90d24b-93a0-4971-bf1b-5dc04cc27990` | `ResponsesMessageItem`; message-type-omitted | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.message_completed` / `d18197a1-c895-4f38-a6b6-96da0b3242ec` | `ResponsesMessageItem`; message-completed | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.input_text_parts` / `2e6fae18-081a-4074-82ef-94effc0533b2` | `ResponsesInputContentPart`; input-text-parts | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.message_role_assistant` / `408d7726-759e-4396-9b0e-c3e5d83e3679` | `ResponsesMessageItem`; message-role-assistant | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.message_role_developer` / `ffce8d07-99b3-47d1-884c-5aaaeee99bc2` | `ResponsesMessageItem`; message-role-developer | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.output_text_history` / `61017d9d-8378-40ea-85ba-5fadf4be7e59` | `ResponsesInputContentPart`; output-text-history | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.input_null` / `37e51d24-0042-4e27-9f49-dd6cefbb188c` | `ResponsesRequest`; /input=null | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.input_number` / `89e90343-0d5f-4f58-a27c-b6c5a3bc9050` | `ResponsesRequest`; /input=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.input_object` / `44e5d75b-3f81-4374-9454-d89453ef79e1` | `ResponsesRequest`; /input={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.input_item_number` / `abe07387-e663-4a50-9658-d3a08094cb85` | `ResponsesRequest`; /input=[7] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_type_invalid` / `2581d61c-248f-4c34-a5f4-62d9d311e00b` | `ResponsesMessageItem`; /input/0/type="unknown" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_type_type` / `f5673958-16e4-4c8f-a376-aa67e3b03704` | `ResponsesMessageItem`; /input/0/type=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_role_missing` / `569817f3-3166-4c41-952a-be54bf173a57` | `ResponsesMessageItem`; /input/0/role omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_role_invalid` / `18410479-c50e-42f1-be13-5755dc2b645c` | `ResponsesMessageItem`; /input/0/role="system" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_role_type` / `0df5c2e3-e513-47fd-89c8-fd3386f98924` | `ResponsesMessageItem`; /input/0/role=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_content_missing` / `2de7fd51-cdd8-435c-a03c-2d46a18a29c9` | `ResponsesMessageItem`; /input/0/content omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_content_type` / `53945b9e-4c07-422d-942a-2239c8846b8d` | `ResponsesMessageItem`; /input/0/content=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_content_null` / `688967b8-7aa9-410c-9904-15d78ce1b567` | `ResponsesMessageItem`; /input/0/content=null | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_status_invalid` / `6bc7a27b-a352-48bf-846b-b884e1955428` | `ResponsesMessageItem`; /input/0/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.message_status_type` / `119065b9-9a17-4edf-aaf1-ea6c1847d144` | `ResponsesMessageItem`; /input/0/status=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.content_part_invalid` / `cc040446-bf0f-450f-b41a-769657fe30bc` | `ResponsesInputContentPart`; /input/0/content/0/type="audio" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.input_text_type` / `9a06a760-6535-4052-8db2-26db5641a963` | `ResponsesInputContentPart`; /input/0/content/0/text=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.instructions_type` / `7003a5f0-eec5-492e-a23a-c36e741ad171` | `ResponsesRequest`; /instructions=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.stream_type` / `35e85ccd-2242-4484-a37d-8b4aa3a87a83` | `ResponsesRequest`; /stream="true" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.stream_null` / `28d887a1-72a7-4ca9-a510-be61b490f4af` | `ResponsesRequest`; /stream=null | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.max_output_tokens_fraction` / `ff9eca31-3f9d-478e-9248-e07c7d89dee7` | `ResponsesRequest`; /max_output_tokens=1.5 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.max_output_tokens_type` / `9fcbaa26-8da6-46ff-aa5a-4a036519f00e` | `ResponsesRequest`; /max_output_tokens="2048" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_type` / `db884471-4df6-4127-8faa-40b350114065` | `ResponsesRequest`; /reasoning="low" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.safety_identifier_type` / `5128e99a-d9f9-441a-9e0c-5fb441b79506` | `ResponsesRequest`; /safety_identifier=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.prompt_cache_key_type` / `2b84b5ab-5bad-49d2-ab92-77797e2333bb` | `ResponsesRequest`; /prompt_cache_key=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tools_type` / `c4ec8537-6480-479f-99a9-83aff5ab1323` | `ResponsesRequest`; /tools={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.include_type` / `9d714744-b684-40c5-9528-5bad41a7e559` | `ResponsesRequest`; /include="web_search_call.results" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.instructions` / `dd384f19-1bc5-487b-968b-c4b95cf88670` | `ResponsesRequest`; instructions | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.stream_false` / `0f46ae67-4577-477a-a6dc-1552402b31c9` | `ResponsesRequest`; stream-false | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.output_budget_default` / `5d5e75ac-9c36-45cd-88f6-1b1bb610b15f` | `ResponsesRequest`; output-budget-default | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.reasoning_effort_type` / `d17563f1-c97a-46ce-a9c9-52bfa624cd89` | `ResponsesRequest`; /reasoning/effort=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_effort_null` / `4b1eadfe-305a-4aa6-8981-c6f254b40840` | `ResponsesRequest`; /reasoning/effort=null | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_empty_default` / `aa2842db-7cb4-4856-91cc-3921f7259a20` | `ResponsesRequest`; reasoning-empty-default | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.tool_choice_unknown` / `a9e35385-d4ae-494f-9993-27d453f2826f` | `ResponsesToolChoice`; /tool_choice="sometimes" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tool_choice_type` / `d7494f13-ce5d-42f2-885e-cad53b346008` | `ResponsesToolChoice`; /tool_choice=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.include_enum` / `24065ae3-a1b4-4bf9-b870-2f1cd92141dc` | `ResponsesRequest`; /include/0="unknown" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.include_item_type` / `53847665-43af-43cb-9955-5b542c938d4c` | `ResponsesRequest`; /include/0=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.include_without_search` / `bb929103-2dc9-4549-a874-3ee3fa621d08` | `ResponsesRequest`; include-without-search | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.schema_strict_false` / `7ade974d-61a4-4aa7-a429-ff4f91184e96` | `ResponsesRequest`; schema-strict-false | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.schema_strict_true` / `51efc2bd-9239-481c-b92c-c7983a72f118` | `ResponsesRequest`; schema-strict-true | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.text_empty` / `d51f8257-1574-48bc-8749-3c6f34ecf543` | `ResponsesRequest`; text-empty | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.text_type` / `610e2f18-3580-4023-bdfd-770076d5c4eb` | `ResponsesRequest`; /text=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.format_type` / `a3e2da58-dc1c-4de1-89e5-b5fe937eaed3` | `ResponsesRequest`; /text/format=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.format_missing_type` / `e3c08255-67ef-4b8a-8b9d-0af8972fd7e4` | `ResponsesRequest`; /text/format/type omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.format_discriminator_type` / `3f2aa420-7c4f-4f64-94a6-3d1e95ba6e00` | `ResponsesRequest`; /text/format/type=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.format_name_type` / `0c39545a-12fb-442a-a445-8e644f27719b` | `ResponsesRequest`; /text/format/name=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.format_strict_type` / `6d5797e9-acd9-4a71-a1d7-96c21aac184a` | `ResponsesRequest`; /text/format/strict="true" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tools_item_type` / `57915545-7ee3-4a27-b55c-fb353a0661a9` | `ResponsesFunctionTool`; /tools/0=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tool_type_missing` / `4c6a3a68-b3f3-4e3f-93c9-607b83a4a2d1` | `ResponsesFunctionTool`; /tools/0/type omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tool_type_unknown` / `41a4d199-02c5-4628-8905-ba776409a170` | `ResponsesFunctionTool`; /tools/0/type="computer" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.tool_type_wrong` / `2dde6eda-017c-4535-adbe-4e10a29cff27` | `ResponsesFunctionTool`; /tools/0/type=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_name_missing` / `7b5623b3-0a63-49c1-b33c-d15aa0d0a969` | `ResponsesFunctionTool`; /tools/0/name omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_name_empty` / `e911bb2f-1d8a-4c3e-be79-74922fa4de48` | `ResponsesFunctionTool`; /tools/0/name="" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_name_type` / `09577e69-c902-4515-a20b-b71b99d5c068` | `ResponsesFunctionTool`; /tools/0/name=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_name_character` / `cf1bee39-d9e8-4138-802b-e673f862ecfa` | `ResponsesFunctionTool`; /tools/0/name="bad.name" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_description_type` / `a426eaa1-1101-435c-a90a-d6f00c1a5cd6` | `ResponsesFunctionTool`; /tools/0/description=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_parameters_type` / `7f04229f-902d-4de6-8302-64cf58511dc5` | `ResponsesFunctionTool`; /tools/0/parameters=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_strict_type` / `0f869ff5-ac5d-4cfb-bdb4-27b3263926b5` | `ResponsesFunctionTool`; /tools/0/strict="true" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.function_name_pattern` / `80acf8d2-d18e-4efa-b800-9f03ea2a4755` | `ResponsesFunctionTool`; function-name-pattern | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.function_name_interior` / `84b139e0-c903-4e2f-89ee-8a7e6712e00d` | `ResponsesFunctionTool`; function-name-interior | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.function_strict_false` / `57de471b-a51d-41db-8cce-f8547b08c585` | `ResponsesFunctionTool`; function-strict-false | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.function_strict_true` / `679e0fed-90df-4835-b5ec-d629cfff5c2d` | `ResponsesFunctionTool`; function-strict-true | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.custom_apply_patch` / `5db07ada-898b-463d-9705-6b66df30bad0` | `ResponsesCustomTool`; custom-apply-patch | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.custom_name_missing` / `b87922b2-0c7c-4d68-a42c-b1994a532f29` | `ResponsesCustomTool`; /tools/0/name omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_missing` / `cef3fea0-73ca-477b-a152-05032fdd5824` | `ResponsesCustomTool`; /tools/0/format omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_name_enum` / `628a3368-a462-4c5c-b8e5-e248a9d63ce3` | `ResponsesCustomTool`; /tools/0/name="shell" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_description_type` / `b09d97a7-012d-4525-b4bb-ce7b9298feec` | `ResponsesCustomTool`; /tools/0/description=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_type` / `bdbb0302-deb5-4fdf-8e89-4abdde154390` | `ResponsesCustomTool`; /tools/0/format=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_kind` / `229e56ba-54f3-46cd-acb2-6bfc0aa2b29c` | `ResponsesCustomTool`; /tools/0/format/type="text" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_syntax` / `bee33214-e4c1-476c-8ec4-330f9c9e613a` | `ResponsesCustomTool`; /tools/0/format/syntax="regex" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_definition_type` / `7f2601a1-a938-47aa-8887-69acfebf3fe4` | `ResponsesCustomTool`; /tools/0/format/definition=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_type_missing` / `1f626010-873e-4d51-b451-41c48818b647` | `ResponsesCustomTool`; /tools/0/format/type omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_syntax_missing` / `9c1c8c08-483e-4908-9337-c4ba4d37d6d6` | `ResponsesCustomTool`; /tools/0/format/syntax omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.custom_format_definition_missing` / `daf671fe-8129-4e0a-a33e-b2883386b161` | `ResponsesCustomTool`; /tools/0/format/definition omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_function` / `9c0399a4-02e9-493b-b962-3941b15e8fa1` | `ResponsesNamespaceTool`; namespace-function | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.namespace_custom` / `742d8c65-0f49-4828-9b2f-038733c8b171` | `ResponsesNamespaceTool`; namespace-custom | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.namespace_name_missing` / `1b70d8e4-fbf3-49bc-af08-cc6883a3c93e` | `ResponsesNamespaceTool`; /tools/0/name omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_description_missing` / `bde5c342-5e59-4ab9-b96f-725897c02fd3` | `ResponsesNamespaceTool`; /tools/0/description omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_tools_missing` / `1a476e41-cc05-41d8-a29a-278cb4660585` | `ResponsesNamespaceTool`; /tools/0/tools omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_name_type` / `ba0de555-0f53-429b-95e9-341f34e96d37` | `ResponsesNamespaceTool`; /tools/0/name=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_description_type` / `7f9458bb-6ba1-4351-aff7-3806504ebebd` | `ResponsesNamespaceTool`; /tools/0/description=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_tools_type` / `bd1c32b1-11b3-4359-b47e-69c97e4eda10` | `ResponsesNamespaceTool`; /tools/0/tools={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_nested` / `51d33786-fe47-4978-840a-fe78f12ece83` | `ResponsesNamespaceTool`; /tools/0/tools/0={"type": "namespace", "name": "math_tools", "description": "数值工具", "tools": [{"type": "function", "name": "get_number", "description": "返回约定的代码。", "parameters": {"type": "object", "properties": {}, "addi (full literal in Case) | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.namespace_search` / `cb50c3fb-a099-4db0-9dc5-10f607cfe627` | `ResponsesNamespaceTool`; /tools/0/tools/0={"type": "web_search"} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.dynamic_role_missing` / `38269017-7bdb-4443-a58c-0c787ee2ef65` | `ResponsesAdditionalToolsItem`; /input/0/role omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.dynamic_tools_missing` / `ba445e36-5603-4052-b227-1d1b12730f9f` | `ResponsesAdditionalToolsItem`; /input/0/tools omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.dynamic_role_invalid` / `4d4d96a3-40fd-4579-9fd5-43252e23c961` | `ResponsesAdditionalToolsItem`; /input/0/role="user" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.dynamic_tools_invalid` / `68c0b681-1753-4fa6-ac4d-23c8dfe1a5a3` | `ResponsesAdditionalToolsItem`; /input/0/tools={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.dynamic_id_invalid` / `844b327d-0a65-450a-ad2c-63c85c3a0ee1` | `ResponsesAdditionalToolsItem`; /input/0/id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_string` / `253c5beb-b5a4-4789-97fc-4852382d7c66` | `ResponsesFunctionCallItem`; history-function-string | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_function_parts` / `c9b60bec-0f01-44af-b6f6-5ac9277f5cb3` | `ResponsesFunctionCallItem`; history-function-parts | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_function_call_id_missing` / `4b869926-81c3-4038-82b3-292c2b4029ed` | `ResponsesFunctionCallItem`; /input/1/call_id omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_call_id_type` / `0fb7cb6b-7017-4d4e-9fe9-a1f0c3aafc91` | `ResponsesFunctionCallItem`; /input/1/call_id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_name_missing` / `1fe0acbb-c5ea-4275-8fa3-b71784599164` | `ResponsesFunctionCallItem`; /input/1/name omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_name_type` / `d61d6641-a5a6-4cd6-8b70-c9abcf82f51a` | `ResponsesFunctionCallItem`; /input/1/name=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_arguments_missing` / `fef2a322-b1ef-4b06-8ae6-92ad838b69b7` | `ResponsesFunctionCallItem`; /input/1/arguments omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_arguments_type` / `9aa2fe60-50c7-44c4-982e-831ee844fcf3` | `ResponsesFunctionCallItem`; /input/1/arguments=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_id_invalid` / `5093b4ea-547f-42e8-bc8f-e4211f7e9b54` | `ResponsesFunctionCallItem`; /input/1/id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_namespace_invalid` / `c24ac591-f57c-4ce2-b73c-8f1630a05e86` | `ResponsesFunctionCallItem`; /input/1/namespace=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_status_invalid` / `78453153-96cb-4e69-abc1-838a72239f1c` | `ResponsesFunctionCallItem`; /input/1/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_output_call_id_missing` / `3518a3fa-aebd-4328-9035-3806dd9e3c64` | `ResponsesFunctionCallOutputItem`; /input/2/call_id omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_output_call_id_type` / `26585856-97a1-4f43-bf4b-27ed838a0a7b` | `ResponsesFunctionCallOutputItem`; /input/2/call_id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_output_output_missing` / `243bf204-c242-4703-adf4-161f3c405bc7` | `ResponsesFunctionCallOutputItem`; /input/2/output omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_output_output_type` / `a3d5fbd7-0712-40cd-93c3-7d77e6f1be33` | `ResponsesFunctionCallOutputItem`; /input/2/output=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_output_status_invalid` / `f30b7275-9571-40a2-8fb9-8c1757891365` | `ResponsesFunctionCallOutputItem`; /input/2/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_string` / `47044095-4f71-4e10-9c53-f9595f94efdd` | `ResponsesCustomToolCallItem`; history-custom-tool-string | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_custom_tool_parts` / `23deee8f-2531-4e0f-bb80-6dad5cd8a319` | `ResponsesCustomToolCallItem`; history-custom-tool-parts | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_custom_tool_call_id_missing` / `7ea3f127-3241-48f6-a031-7b2386698b0f` | `ResponsesCustomToolCallItem`; /input/1/call_id omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_call_id_type` / `15b91f4f-3827-4a25-9a3a-776408eba694` | `ResponsesCustomToolCallItem`; /input/1/call_id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_name_missing` / `b971fc92-f680-4bce-a8d1-1cf3c26f35f7` | `ResponsesCustomToolCallItem`; /input/1/name omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_name_type` / `fa285b48-5743-454c-bf0a-73a0d009451d` | `ResponsesCustomToolCallItem`; /input/1/name=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_input_missing` / `f29c997f-5fed-4082-bd54-9401248c654e` | `ResponsesCustomToolCallItem`; /input/1/input omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_input_type` / `68459999-13fc-4f61-8d2a-f455cfa56ac1` | `ResponsesCustomToolCallItem`; /input/1/input=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_id_invalid` / `74fbcb4d-1032-44f1-b3f7-c84e97b1a1f1` | `ResponsesCustomToolCallItem`; /input/1/id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_namespace_invalid` / `15b432b9-4062-4188-bf0b-f42017055f08` | `ResponsesCustomToolCallItem`; /input/1/namespace=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_status_invalid` / `1a4631a9-4fe3-4725-8593-7d0978d1d057` | `ResponsesCustomToolCallItem`; /input/1/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_output_call_id_missing` / `49542af3-be68-4a47-8048-c701d9d196ef` | `ResponsesCustomToolCallOutputItem`; /input/2/call_id omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_output_call_id_type` / `4fcac6fa-5d31-43bc-9c97-6b0b0ba6efc5` | `ResponsesCustomToolCallOutputItem`; /input/2/call_id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_output_output_missing` / `b8f50811-2d21-4cd3-849e-3c20f5fd9f8a` | `ResponsesCustomToolCallOutputItem`; /input/2/output omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_output_output_type` / `67f7d03a-c6f1-4f99-b3be-b2b997616ba9` | `ResponsesCustomToolCallOutputItem`; /input/2/output=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_output_status_invalid` / `42926ab4-aa8e-4b35-a5be-c28587870105` | `ResponsesCustomToolCallOutputItem`; /input/2/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_item_id_invalid` / `167987b4-157f-4dfb-9402-8635b66b349d` | `ResponsesReasoningItem`; /input/0/id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_item_status_invalid` / `e2ac07ce-9c20-4c44-8af6-8b82fbff4b10` | `ResponsesReasoningItem`; /input/0/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_item_summary_invalid` / `6d5e643a-ca14-4723-8a31-1aa9f7cc2191` | `ResponsesReasoningItem`; /input/0/summary={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_item_content_invalid` / `af5cbfca-30e6-4110-85c0-64e73f34d7aa` | `ResponsesReasoningItem`; /input/0/content={} | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_summary_type_missing` / `341d844e-b05c-462e-9704-0cd9243c5b5e` | `ResponsesReasoningItem`; /input/0/summary/0/type omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_summary_text_missing` / `8d2669ec-1c7b-42c3-b432-7b1f97a89a2c` | `ResponsesReasoningItem`; /input/0/summary/0/text omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_summary_type_invalid` / `6e388bb6-c731-477e-a89c-5ef587557314` | `ResponsesReasoningItem`; /input/0/summary/0/type="output_text" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_summary_text_invalid` / `030dce18-35b3-4e12-aeb1-02e57c28f475` | `ResponsesReasoningItem`; /input/0/summary/0/text=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_content_type_missing` / `0f9caa0c-8c3c-43f6-8127-af0483accf0f` | `ResponsesReasoningItem`; /input/0/content/0/type omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_content_text_missing` / `24b12f48-ff80-4686-8a83-32a6e932c0fe` | `ResponsesReasoningItem`; /input/0/content/0/text omitted | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_content_type_invalid` / `edb48d86-10f6-4446-8661-476139d6781c` | `ResponsesReasoningItem`; /input/0/content/0/type="output_text" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_content_text_invalid` / `98550a08-edcb-4169-995f-7f4c10e7185a` | `ResponsesReasoningItem`; /input/0/content/0/text=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.reasoning_provider_replay` / `6816bf84-7dce-4cd8-a0cf-56ef6dec003a` | `ResponsesReasoningItem`; reasoning-provider-replay | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_history` / `3f787b88-b85b-465e-b6cc-41d7d7e2f231` | `ResponsesWebSearchCallItem`; search-history | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.search_history_id_invalid` / `3fc85005-21d2-4b35-8b84-e62e533da858` | `ResponsesWebSearchCallItem`; /input/0/id=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_history_status_invalid` / `0408e8a6-d76b-4225-9373-17d23d5197d9` | `ResponsesWebSearchCallItem`; /input/0/status="in_progress" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_history_action_invalid` / `8a2b81b3-af8c-43c0-8aa4-96b4f70fb04f` | `ResponsesWebSearchCallItem`; /input/0/action=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_history_action_type_invalid` / `549072ce-505c-4f52-b411-2c068a05bc8c` | `ResponsesWebSearchCallItem`; /input/0/action/type="unknown" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_history_action_query_invalid` / `6f0f609b-1c74-46c9-a9d8-e95aa2cf9772` | `ResponsesWebSearchCallItem`; /input/0/action/query=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.cache_options_empty` / `22d2df37-cdf4-4585-b381-a9cbabe5d019` | `ResponsesRequest`; cache-options-empty | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.cache_options_mode_default_ttl` / `3730adbf-eac6-479c-bd16-0b4579bcd9e7` | `ResponsesRequest`; cache-options-mode-default-ttl | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.cache_options_ttl_5m` / `e384033d-b04f-46f9-962e-d2062e3a4c4d` | `ResponsesRequest`; cache-options-ttl-5m | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.cache_options_ttl_1h` / `b82df9cb-b8a8-4b85-8d65-d52be327da20` | `ResponsesRequest`; cache-options-ttl-1h | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.cache_options_omitted` / `4685e7de-4525-44df-b22c-8826477f693d` | `ResponsesRequest`; cache-options-omitted | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.cache_outer_type` / `26ad5236-4b1d-4564-b710-6cdaa40ad0d3` | `ResponsesRequest`; /prompt_cache_options=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.cache_mode_enum` / `d4c7347c-652a-49f8-9299-0500068b6539` | `ResponsesRequest`; /prompt_cache_options/mode="explicit" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.cache_mode_type` / `aab7dbe8-a0b1-4854-b935-993e7db53273` | `ResponsesRequest`; /prompt_cache_options/mode=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.cache_ttl_enum` / `d0af0b2a-2e90-4515-9f72-f9490b5f5899` | `ResponsesRequest`; /prompt_cache_options/ttl="10m" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.cache_ttl_type` / `53b2cc1f-edc1-474f-959b-810130385651` | `ResponsesRequest`; /prompt_cache_options/ttl=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.image_detail_auto` / `a979781f-b102-4300-ae7f-000a4507b4ab` | `ResponsesInputContentPart`; image-detail-auto | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.image_detail_low` / `5bcd6d73-5bed-49eb-97b7-f7dd8fbe9554` | `ResponsesInputContentPart`; image-detail-low | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.image_detail_high` / `57a72665-6119-4d63-8f76-a39bcd9bbbcf` | `ResponsesInputContentPart`; image-detail-high | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.image_detail_original` / `0e0ab778-de82-4566-9e07-ab97f2de1479` | `ResponsesInputContentPart`; image-detail-original | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.image_url_number` / `a6341dd5-7207-4714-b92c-e141da699a49` | `ResponsesInputContentPart`; /input/0/content/1/image_url=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.image_url_http` / `3a7dba4d-719e-445b-a60f-19f414ae3e8e` | `ResponsesInputContentPart`; /input/0/content/1/image_url="https://example.com/digit.png" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.image_detail_enum` / `4dfe7f66-d1ac-4191-b648-aaa5dfde33da` | `ResponsesInputContentPart`; /input/0/content/1/detail="medium" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.image_detail_type` / `11b6ac3a-e314-481b-befe-d63825d5e8bf` | `ResponsesInputContentPart`; /input/0/content/1/detail=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.input_video_unsupported` / `d2554a83-7855-40f0-98df-a69c097c6598` | `ResponsesInputContentPart`; input-video-unsupported | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_filters_type` / `0b6bf8bd-50b8-49a5-9817-14e0ec503c7c` | `ResponsesWebSearchTool`; /tools/0/filters=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_domains_type` / `62c4fbb6-4242-467e-8ae7-6e54fd3ee878` | `ResponsesWebSearchTool`; /tools/0/filters/allowed_domains="platform.kimi.com" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_domain_item_type` / `aeda383d-7f10-4e1c-bb09-6e381e630b15` | `ResponsesWebSearchTool`; /tools/0/filters/allowed_domains/0=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_domains_101` / `ef6027d5-ef25-4edd-9219-cdb5c2c0baa5` | `ResponsesWebSearchTool`; /tools/0/filters/allowed_domains=["site0.example.com", "site1.example.com", "site2.example.com", "site3.example.com", "site4.example.com", "site5.example.com", "site6.example.com", "site7.example.com", "site8.example.com (full literal in Case) | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_content_types_type` / `156cef28-25e4-44a8-8dd2-3b0725d88ffe` | `ResponsesWebSearchTool`; /tools/0/search_content_types="text" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_content_enum` / `2cbb823f-e697-44d7-92b4-18ed89bf9284` | `ResponsesWebSearchTool`; /tools/0/search_content_types/0="video" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_content_item_type` / `fe615287-f8a3-4796-9a69-0d79c0ff60a9` | `ResponsesWebSearchTool`; /tools/0/search_content_types/0=7 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_settings_type` / `a2ec7c23-3f78-473e-a874-b1923700e672` | `ResponsesWebSearchTool`; /tools/0/image_settings=[] | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_max_zero` / `10423597-9e52-45e0-891e-3ed4398f0398` | `ResponsesWebSearchTool`; /tools/0/image_settings/max_results=0 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_max_eleven` / `f4ffa60d-c443-419f-b795-76b1b7c2796d` | `ResponsesWebSearchTool`; /tools/0/image_settings/max_results=11 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_max_fraction` / `80bcb4ad-3c17-4128-b169-d060f13ae053` | `ResponsesWebSearchTool`; /tools/0/image_settings/max_results=1.5 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_max_type` / `54f6a409-0d3b-47d0-aeda-be644f3af977` | `ResponsesWebSearchTool`; /tools/0/image_settings/max_results="3" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_image_caption_type` / `9d82020c-581e-4a46-b72b-d742c8ee6d41` | `ResponsesWebSearchTool`; /tools/0/image_settings/caption="true" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_two_tools` / `75839fd8-e22c-450a-aea5-bfde8b7ade1c` | `ResponsesWebSearchTool`; search-two-tools | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_forbidden_search_context_size` / `84967dbc-179d-4dcf-80ad-59816157d180` | `ResponsesWebSearchTool`; search-forbidden-search-context-size | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_forbidden_blocked_domains` / `ec729c0c-243a-4c62-8b04-8164932cdaa9` | `ResponsesWebSearchTool`; search-forbidden-blocked-domains | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_forbidden_filters_blocked_domains` / `68891755-4d35-4b14-a2fb-d0b1a068b05d` | `ResponsesWebSearchTool`; search-forbidden-filters-blocked-domains | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_domains_0` / `ad2273fe-263a-4859-8356-58280c4a86c4` | `ResponsesWebSearchTool`; search-domains-0 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_domains_1` / `01399bc7-583c-45c9-83e1-f18799b6cb4f` | `ResponsesWebSearchTool`; search-domains-1 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_domains_100` / `bd0b2a9b-19a5-4748-a80d-405a528bc437` | `ResponsesWebSearchTool`; search-domains-100 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_content_text` / `40746f7e-7cc5-479d-961d-64ccbf866ab4` | `ResponsesWebSearchTool`; search-content-text | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_content_image` / `e08d9fc9-2c83-4397-bf1a-bcf8c2a09634` | `ResponsesWebSearchTool`; search-content-image | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_content_text_image` / `c69b5f69-e53e-4b2a-95f8-d17e37fffca0` | `ResponsesWebSearchTool`; search-content-text-image | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_image_max_1` / `2aa555c1-d7e4-4c0f-96ca-11028fe69905` | `ResponsesWebSearchTool`; search-image-max-1 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_image_max_5` / `97620e94-6234-4160-b0c8-dc390505845b` | `ResponsesWebSearchTool`; search-image-max-5 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_image_max_10` / `3941ffb6-bb4d-4111-a845-cf630c8a1248` | `ResponsesWebSearchTool`; search-image-max-10 | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_image_default` / `c88c169b-150b-4120-8abf-212c88897440` | `ResponsesWebSearchTool`; search-image-default | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_image_caption` / `33869d94-c769-4a5d-94ad-2eee95b5f6bd` | `ResponsesWebSearchTool`; search-image-caption | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_ignored_user_location` / `82f6f297-a15b-4b64-b53c-5710d9894e6a` | `ResponsesWebSearchTool`; search-ignored-user-location | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_ignored_external_web_access` / `31daedf8-31ed-449a-bf3b-e72f036039d2` | `ResponsesWebSearchTool`; search-ignored-external-web-access | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_ignored_indexed_web_access` / `2bb1e2f2-f7d4-4385-a52e-57f0238b9abb` | `ResponsesWebSearchTool`; search-ignored-indexed-web-access | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.response_envelope` / `af2342f4-e23b-4197-9452-d7de783d86f5` | `ResponsesResponse`; response-envelope | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.stream_event_envelope` / `2ca7f1b0-09fd-4c83-a465-60f02b508710` | `ResponsesStreamEvent`; stream-event-envelope | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.function_optional_omitted` / `9d9ab1ca-3fce-4d02-82a3-4af3ab6e194b` | `ResponsesFunctionTool`; tools[0]={type:function,name:get_number}; optional fields omitted | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.custom_description_omitted` / `9d1f6d96-b026-42de-97fb-01155b6c6f32` | `ResponsesCustomTool`; /tools/0/description omitted | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_function_completed` / `c4416b29-a6fd-46b6-aaf9-755236d54368` | `ResponsesFunctionCallItem`; client-authored call id + completed call/output | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_function_mismatched_id` / `f3470dd9-f01c-44a1-a1c2-43d7a1cf0b33` | `ResponsesFunctionCallOutputItem`; /input/2/call_id=client-call-2 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_custom_tool_completed` / `fd142658-0f83-43b3-9468-2a649160c3e1` | `ResponsesCustomToolCallItem`; client-authored call id + completed call/output | HTTP200 + semantic/native-item assertions | T1 enabled |
| `k3.responses.history_custom_tool_mismatched_id` / `137f5c81-0174-43c2-9fe9-e1135dbadc61` | `ResponsesCustomToolCallOutputItem`; /input/2/call_id=client-call-2 | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.history_function_invalid_json` / `400b2297-875f-4960-9cc7-375d31a6a7c3` | `ResponsesFunctionCallItem`; /input/1/arguments="{" | HTTP400 + required string error.message | T2 enabled |
| `k3.responses.search_include_results` / `8a71cc27-1df0-4ed7-8815-c30120c95461` | `ResponsesRequest`; /include=[web_search_call.results] | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_include_sources` / `4e1c5a18-6460-4ab4-b09c-cc30677bcc86` | `ResponsesRequest`; /include=[web_search_call.action.sources] | HTTP200 + semantic/native-item assertions | T3 disabled |
| `k3.responses.search_include_omitted` / `9791b92f-822e-4366-a915-9d5577419c4f` | `ResponsesRequest`; /include omitted | HTTP200 + semantic/native-item assertions | T3 disabled |
