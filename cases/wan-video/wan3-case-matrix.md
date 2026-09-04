# Wan 3.0 具体测试 Case 矩阵

本表是 Wan 3.0 的实际 `case.json` 清单，不是抽象覆盖项。两个模型 `wan3.0-video`、`wan3.0-video-prime` 共用同一合同，每个 Case 都显式绑定这两个 `model_targets`。

- 总计 191 个具体 Case。
- 自动 Case 74 个：正向 35、负向 39；全部 `default=false`，真实运行仍需显式确认付费套件。
- 禁用模板 117 个：正向 66、负向 51；用于素材边界、地域/鉴权、传输原始字节和成品/语义 oracle，未提供前不会进入运行计划。
- 本次只做静态、模拟与 dry-run 验证，没有调用真实百炼接口。

自动负向 Case 的稳定通过条件是：创建阶段 HTTP 400 的 `InvalidParameter`，或创建成功后终态 `FAILED` 的 `InvalidParameter`；鉴权、欠费、限流和网关错误不能冒充参数边界通过。

为不同执行场景，两个 Wan3 模型分别提供连通性 1 Case、基本功能 6 Case、参数拒绝 39 Case、完整测试（自动可执行）74 Case、完整矩阵（含禁用模板）191 Case 的分层 Suite；详细用途见 [Wan 视频 API 边界用例](README.md#wan-30-suite-分层)。

| Case | 名称 | 维度 | 状态 | 稳定预期 / 禁用原因 |
|---|---|---|---|---|
| [wan30.t2v.min_duration](W3001-min-duration/case.json) | Wan 3.0 · 最小时长 2 秒 | compatibility | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.t2v.duration_below_min](W3002-duration-below/case.json) | Wan 3.0 · 时长低于下界 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.t2v.duration_above_max](W3003-duration-above/case.json) | Wan 3.0 · 时长高于上界 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.t2v.seed_below_min](W3004-seed-below/case.json) | Wan 3.0 · Seed 低于下界 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.t2v.seed_above_max](W3005-seed-above/case.json) | Wan 3.0 · Seed 高于上界 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.t2v.resolution_invalid](W3006-resolution-invalid/case.json) | Wan 3.0 · 非法分辨率 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.t2v.ratio_invalid](W3007-ratio-invalid/case.json) | Wan 3.0 · 非法宽高比 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.input.prompt_or_media_required](W3008-input-missing/case.json) | Wan 3.0 · 缺少 Prompt 与媒体 | required | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.model.target_valid](W3009-model-target-success/case.json) | Wan 3.0 · 目标模型名称有效 | compatibility | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.model.required](W3010-model-missing/case.json) | Wan 3.0 · 缺少 model | required | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.model.empty](W3011-model-empty/case.json) | Wan 3.0 · model 为空字符串 | parameters | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.model.unknown](W3012-model-unknown/case.json) | Wan 3.0 · model 为未知枚举 | parameters | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.model.wrong_type](W3013-model-wrong-type/case.json) | Wan 3.0 · model 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.input.null](W3014-input-null/case.json) | Wan 3.0 · input 为 null | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.input.string](W3015-input-string/case.json) | Wan 3.0 · input 为字符串 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.input.array](W3016-input-array/case.json) | Wan 3.0 · input 为数组 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.parameters.omitted](W3017-parameters-omitted/case.json) | Wan 3.0 · 省略 parameters 使用默认值 | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.parameters.empty_object](W3018-parameters-empty/case.json) | Wan 3.0 · parameters 为空对象 | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.parameters.string](W3020-parameters-string/case.json) | Wan 3.0 · parameters 为字符串 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.parameters.array](W3021-parameters-array/case.json) | Wan 3.0 · parameters 为数组 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.prompt.length_1](W3022-prompt-length-1/case.json) | Wan 3.0 · Prompt 长度 1 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt.length_20000](W3023-prompt-length-20000/case.json) | Wan 3.0 · Prompt 长度上限 20000 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt.length_20001_truncated](W3024-prompt-length-20001/case.json) | Wan 3.0 · Prompt 20001 字符可自动截断受理 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt.wrong_type](W3025-prompt-wrong-type/case.json) | Wan 3.0 · Prompt 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.duration.omitted_default_5](W3026-duration-omitted/case.json) | Wan 3.0 · 省略 duration 默认 5 秒 | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.duration.below_union](W3027-duration-minus-2/case.json) | Wan 3.0 · duration 为 -2 | boundary | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.duration.smart_minus_1](W3028-duration-smart-minus-1/case.json) | Wan 3.0 · duration 智能时长 -1 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.duration.explicit_5](W3029-duration-explicit-5/case.json) | Wan 3.0 · duration 显式 5 秒 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.duration.max_30](W3030-duration-max-30/case.json) | Wan 3.0 · duration 最大 30 秒 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.duration.wrong_type](W3031-duration-wrong-type/case.json) | Wan 3.0 · duration 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.duration.non_integer](W3032-duration-non-integer/case.json) | Wan 3.0 · duration 非整数 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.seed.omitted_random](W3033-seed-omitted/case.json) | Wan 3.0 · 省略 seed 使用随机值 | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.seed.random_minus_1](W3034-seed-minus-1/case.json) | Wan 3.0 · seed 为 -1 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.seed.interior_42](W3035-seed-interior-42/case.json) | Wan 3.0 · seed 中间值 42 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.seed.max](W3036-seed-max/case.json) | Wan 3.0 · seed 最大值 2147483647 | boundary | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.seed.wrong_type](W3037-seed-wrong-type/case.json) | Wan 3.0 · seed 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.seed.non_integer](W3038-seed-non-integer/case.json) | Wan 3.0 · seed 非整数 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.resolution.omitted_default_1080p](W3039-resolution-omitted/case.json) | Wan 3.0 · 省略 resolution 默认 1080P | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.resolution.480p](W3040-resolution-480p/case.json) | Wan 3.0 · resolution=480P | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.resolution.720p](W3041-resolution-720p/case.json) | Wan 3.0 · resolution=720P | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.resolution.1080p](W3042-resolution-1080p/case.json) | Wan 3.0 · resolution=1080P | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.resolution.wrong_type](W3043-resolution-wrong-type/case.json) | Wan 3.0 · resolution 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.ratio.omitted_default_adaptive](W3044-ratio-omitted/case.json) | Wan 3.0 · 省略 ratio 默认 adaptive | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.adaptive](W3045-ratio-adaptive/case.json) | Wan 3.0 · ratio=adaptive | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.16_9](W3046-ratio-16_9/case.json) | Wan 3.0 · ratio=16:9 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.4_3](W3047-ratio-4_3/case.json) | Wan 3.0 · ratio=4:3 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.1_1](W3048-ratio-1_1/case.json) | Wan 3.0 · ratio=1:1 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.3_4](W3049-ratio-3_4/case.json) | Wan 3.0 · ratio=3:4 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.9_16](W3050-ratio-9_16/case.json) | Wan 3.0 · ratio=9:16 | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.ratio.wrong_type](W3051-ratio-wrong-type/case.json) | Wan 3.0 · ratio 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.audio.omitted_default_true](W3052-audio-omitted/case.json) | Wan 3.0 · 省略 audio 默认 true | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.audio.true](W3053-audio-true/case.json) | Wan 3.0 · audio=true | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.audio.false](W3054-audio-false/case.json) | Wan 3.0 · audio=false | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.audio.wrong_type](W3055-audio-wrong-type/case.json) | Wan 3.0 · audio 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.prompt_extend.omitted_default_true](W3056-prompt-extend-omitted/case.json) | Wan 3.0 · 省略 prompt_extend 默认 true | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt_extend.true](W3057-prompt-extend-true/case.json) | Wan 3.0 · prompt_extend=true | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt_extend.false](W3058-prompt-extend-false/case.json) | Wan 3.0 · prompt_extend=false | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.prompt_extend.wrong_type](W3059-prompt-extend-wrong-type/case.json) | Wan 3.0 · prompt_extend 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.watermark.omitted_default_false](W3060-watermark-omitted/case.json) | Wan 3.0 · 省略 watermark 默认 false | defaults | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.watermark.false](W3061-watermark-false/case.json) | Wan 3.0 · watermark=false | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.watermark.true](W3062-watermark-true/case.json) | Wan 3.0 · watermark=true | parameters | 自动 | 创建后轮询至 `SUCCEEDED`，且返回安全的 HTTP(S) `video_url` |
| [wan30.watermark.wrong_type](W3063-watermark-wrong-type/case.json) | Wan 3.0 · watermark 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.wrong_type](W3067-media-wrong-type/case.json) | Wan 3.0 · media 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.item_wrong_type](W3068-media-item-wrong-type/case.json) | Wan 3.0 · media 元素类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.missing_type](W3069-media-missing-type/case.json) | Wan 3.0 · media 元素缺少 type | required | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.missing_url](W3070-media-missing-url/case.json) | Wan 3.0 · media 元素缺少 url | required | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.type_wrong_type](W3071-media-type-wrong-type/case.json) | Wan 3.0 · media.type 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.type_unknown](W3072-media-unknown-type/case.json) | Wan 3.0 · media.type 未知枚举 | parameters | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.url_wrong_type](W3073-media-url-wrong-type/case.json) | Wan 3.0 · media.url 类型错误 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.empty_without_prompt](W3074-media-empty-no-prompt/case.json) | Wan 3.0 · 空 media 且无 Prompt | required | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.parameters.number](W3075-parameters-number/case.json) | Wan 3.0 · parameters 为数字 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.type_case_variant](W3076-media-type-case-variant/case.json) | Wan 3.0 · media.type 大小写变体 | parameters | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.media.type_whitespace](W3077-media-type-whitespace/case.json) | Wan 3.0 · media.type 含首尾空格 | parameters | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.input.number](W3078-input-number/case.json) | Wan 3.0 · input 为数字 | type | 自动 | 创建阶段或任务终态以 `InvalidParameter` 拒绝 |
| [wan30.artifact.audio_true_has_track](W3A01-audio-true-artifact/case.json) | Wan 3.0 · audio=true 产物含音轨 | artifact | 禁用模板 | 需要下载视频并用媒体探针验证音轨 |
| [wan30.artifact.audio_false_no_track](W3A02-audio-false-artifact/case.json) | Wan 3.0 · audio=false 产物无音轨 | artifact | 禁用模板 | 需要下载视频并用媒体探针验证无音轨 |
| [wan30.artifact.watermark_true_visible](W3A03-watermark-true-artifact/case.json) | Wan 3.0 · watermark=true 产物含水印 | artifact | 禁用模板 | 需要下载视频并使用稳定视觉水印 oracle |
| [wan30.artifact.watermark_false_absent](W3A04-watermark-false-artifact/case.json) | Wan 3.0 · watermark=false 产物无水印 | artifact | 禁用模板 | 需要下载视频并使用稳定视觉水印 oracle |
| [wan30.artifact.resolution_matches](W3A05-resolution-artifact/case.json) | Wan 3.0 · 产物分辨率匹配 resolution | artifact | 禁用模板 | 需要下载视频并用媒体探针验证像素尺寸 |
| [wan30.artifact.ratio_matches](W3A06-ratio-artifact/case.json) | Wan 3.0 · 产物比例匹配 ratio | artifact | 禁用模板 | 需要下载视频并用媒体探针验证宽高比 |
| [wan30.reference_audio.duration_1](W3AD01-audio-duration-1/case.json) | Wan 3.0 · 参考音频单个时长下界 1 秒 | media-boundary | 禁用模板 | 隔离单个音频时长边界 |
| [wan30.reference_audio.duration_15](W3AD02-audio-duration-15/case.json) | Wan 3.0 · 参考音频单个时长上界 15 秒 | media-boundary | 禁用模板 | 隔离单个音频时长边界 |
| [wan30.reference_audio.duration_below_1](W3AD03-audio-duration-below-1/case.json) | Wan 3.0 · 参考音频时长低于 1 秒 | media-boundary | 禁用模板 | 隔离单个音频时长边界 |
| [wan30.reference_audio.duration_above_15](W3AD04-audio-duration-above-15/case.json) | Wan 3.0 · 参考音频时长超过 15 秒 | media-boundary | 禁用模板 | 隔离单个音频时长边界 |
| [wan30.reference_audio.total_duration_over_15](W3AD05-audio-total-duration-over-15/case.json) | Wan 3.0 · 参考音频总时长超过 15 秒 | media-boundary | 禁用模板 | 两个音频各自合法但总时长 16 秒 |
| [wan30.audio_media.format_mp3](W3AU01-audio-mp3/case.json) | Wan 3.0 · MP3 参考音频 | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.format_wav](W3AU02-audio-wav/case.json) | Wan 3.0 · WAV 参考音频 | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.size_15mb](W3AU03-audio-size-15mb/case.json) | Wan 3.0 · 音频大小 15MB | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.size_over_15mb](W3AU04-audio-size-over-15mb/case.json) | Wan 3.0 · 音频大小超过 15MB | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.unsupported_format](W3AU05-audio-unsupported-format/case.json) | Wan 3.0 · 音频格式不受支持 | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.malformed_url](W3AU06-audio-malformed-url/case.json) | Wan 3.0 · 音频 URL 非法 | media-boundary | 禁用模板 | 隔离音频格式、大小或 URL 边界 |
| [wan30.audio_media.protocol_oss](W3AU07-audio-oss/case.json) | Wan 3.0 · OSS 音频 URL | media-boundary | 禁用模板 | 覆盖 OSS 协议分区 |
| [wan30.environment.same_region](W3E01-same-region/case.json) | Wan 3.0 · Endpoint、模型与 Key 同地域 | environment | 禁用模板 | 需要可计费的同地域 Workspace、Endpoint、模型和 API Key |
| [wan30.environment.cross_region](W3E02-cross-region/case.json) | Wan 3.0 · Endpoint 与模型跨地域 | environment | 禁用模板 | 需要跨地域配置并使用独立环境错误断言，不能按参数错误误判 |
| [wan30.authorization.missing](W3E03-auth-missing/case.json) | Wan 3.0 · Authorization 缺失 | authorization | 禁用模板 | 运行器当前从渠道配置统一注入 Authorization，需支持按 Case 删除 Header |
| [wan30.authorization.non_bearer](W3E04-auth-non-bearer/case.json) | Wan 3.0 · Authorization 非 Bearer | authorization | 禁用模板 | 需要按 Case 覆盖 Authorization，且认证失败不能按参数拒绝通过 |
| [wan30.authorization.invalid_key](W3E05-auth-invalid-key/case.json) | Wan 3.0 · Bearer Key 无效 | authorization | 禁用模板 | 需要按 Case 覆盖无效 Key，且认证失败不能按参数拒绝通过 |
| [wan30.video_edit.video_only](W3ED01-edit-video-only/case.json) | Wan 3.0 · 仅视频编辑 | semantic | 禁用模板 | 需要对应素材与可复现编辑语义 oracle |
| [wan30.video_edit.video_image](W3ED02-edit-video-image/case.json) | Wan 3.0 · 视频+图像编辑 | semantic | 禁用模板 | 需要对应素材与可复现编辑语义 oracle |
| [wan30.video_edit.video_audio](W3ED03-edit-video-audio/case.json) | Wan 3.0 · 视频+音频编辑 | semantic | 禁用模板 | 需要对应素材与可复现编辑语义 oracle |
| [wan30.video_edit.video_image_audio](W3ED04-edit-video-image-audio/case.json) | Wan 3.0 · 视频+图像+音频编辑 | semantic | 禁用模板 | 需要对应素材与可复现编辑语义 oracle |
| [wan30.file.pdf_50_pages](W3F01-file-pdf-50-pages/case.json) | Wan 3.0 · PDF 页数上限 50 | media-boundary | 禁用模板 | 隔离文件页数、大小或格式边界 |
| [wan30.file.pdf_51_pages](W3F02-file-pdf-51-pages/case.json) | Wan 3.0 · PDF 页数 51 超限 | media-boundary | 禁用模板 | 隔离文件页数、大小或格式边界 |
| [wan30.file.size_100mb](W3F03-file-size-100mb/case.json) | Wan 3.0 · 文件大小 100MB | media-boundary | 禁用模板 | 隔离文件页数、大小或格式边界 |
| [wan30.file.size_over_100mb](W3F04-file-size-over-100mb/case.json) | Wan 3.0 · 文件大小超过 100MB | media-boundary | 禁用模板 | 隔离文件页数、大小或格式边界 |
| [wan30.file.unsupported_format](W3F05-file-unsupported-format/case.json) | Wan 3.0 · 文件格式不受支持 | media-boundary | 禁用模板 | 隔离文件页数、大小或格式边界 |
| [wan30.file.format_docx](W3F06-file-docx/case.json) | Wan 3.0 · DOCX 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_doc](W3F07-file-doc/case.json) | Wan 3.0 · DOC 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_xlsx](W3F08-file-xlsx/case.json) | Wan 3.0 · XLSX 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_xls](W3F09-file-xls/case.json) | Wan 3.0 · XLS 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_pptx](W3F10-file-pptx/case.json) | Wan 3.0 · PPTX 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_ppt](W3F11-file-ppt/case.json) | Wan 3.0 · PPT 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_txt](W3F12-file-txt/case.json) | Wan 3.0 · TXT 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_key](W3F13-file-key/case.json) | Wan 3.0 · KEY 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_pages](W3F14-file-pages/case.json) | Wan 3.0 · PAGES 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_numbers](W3F15-file-numbers/case.json) | Wan 3.0 · NUMBERS 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.format_md](W3F16-file-md/case.json) | Wan 3.0 · MD 文件 | media-boundary | 禁用模板 | 覆盖官方支持的文件格式枚举 |
| [wan30.file.malformed_url](W3F17-file-malformed-url/case.json) | Wan 3.0 · 文件 URL 非法 | media-boundary | 禁用模板 | 隔离 malformed URL |
| [wan30.file.protocol_oss](W3F18-file-oss/case.json) | Wan 3.0 · OSS 文件 URL | media-boundary | 禁用模板 | 覆盖 OSS 协议分区 |
| [wan30.image.side_min_240](W3I01-image-side-240/case.json) | Wan 3.0 · 图片单边最小 240 | media-boundary | 禁用模板 | 合法边界应成功 |
| [wan30.image.side_below_min_239](W3I02-image-side-239/case.json) | Wan 3.0 · 图片单边 239 低于下界 | media-boundary | 禁用模板 | 非法相邻边界应业务拒绝 |
| [wan30.image.side_max_8000](W3I03-image-side-8000/case.json) | Wan 3.0 · 图片单边最大 8000 | media-boundary | 禁用模板 | 合法边界应成功 |
| [wan30.image.side_above_max_8001](W3I04-image-side-8001/case.json) | Wan 3.0 · 图片单边 8001 超上界 | media-boundary | 禁用模板 | 非法相邻边界应业务拒绝 |
| [wan30.image.ratio_8_1](W3I05-image-ratio-8-1/case.json) | Wan 3.0 · 图片宽高比 8:1 | media-boundary | 禁用模板 | 合法比例边界应成功 |
| [wan30.image.ratio_over_8_1](W3I06-image-ratio-over-8-1/case.json) | Wan 3.0 · 图片宽高比超过 8:1 | media-boundary | 禁用模板 | 超出比例边界应业务拒绝 |
| [wan30.image.size_20mb](W3I07-image-size-20mb/case.json) | Wan 3.0 · 图片大小 20MB | media-boundary | 禁用模板 | 合法大小边界应成功 |
| [wan30.image.size_over_20mb](W3I08-image-size-over-20mb/case.json) | Wan 3.0 · 图片大小超过 20MB | media-boundary | 禁用模板 | 超出大小边界应业务拒绝 |
| [wan30.image.transparent_png](W3I09-image-transparent-png/case.json) | Wan 3.0 · 透明 PNG 不受支持 | media-boundary | 禁用模板 | 透明 PNG 应稳定失败 |
| [wan30.image.unsupported_format](W3I10-image-unsupported-format/case.json) | Wan 3.0 · 图片格式不受支持 | media-boundary | 禁用模板 | 不支持格式应稳定失败 |
| [wan30.image.format_jpg](W3I11-image-jpg/case.json) | Wan 3.0 · JPG 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.format_png_no_alpha](W3I12-image-png-no-alpha/case.json) | Wan 3.0 · 无透明通道 PNG 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.format_bmp](W3I13-image-bmp/case.json) | Wan 3.0 · BMP 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.format_webp](W3I14-image-webp/case.json) | Wan 3.0 · WEBP 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.protocol_oss](W3I15-image-oss/case.json) | Wan 3.0 · OSS 图片 URL | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.protocol_base64](W3I16-image-base64/case.json) | Wan 3.0 · Base64 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.malformed_base64](W3I17-image-malformed-base64/case.json) | Wan 3.0 · Malformed Base64 图片 | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.image.malformed_url](W3I18-image-malformed-url/case.json) | Wan 3.0 · Malformed 图片 URL | media-boundary | 禁用模板 | 隔离图片格式或 URL 协议分区 |
| [wan30.prompt.image_indexing](W3IDX01-image-indexing/case.json) | Wan 3.0 · Prompt 按顺序引用图像编号 | semantic | 禁用模板 | 需要可判定编号顺序的视觉语义 oracle |
| [wan30.prompt.video_indexing](W3IDX02-video-indexing/case.json) | Wan 3.0 · Prompt 按顺序引用视频编号 | semantic | 禁用模板 | 需要可判定编号顺序的视频语义 oracle |
| [wan30.prompt.audio_indexing](W3IDX03-audio-indexing/case.json) | Wan 3.0 · Prompt 按顺序引用音频编号 | semantic | 禁用模板 | 需要可判定编号顺序的音频语义 oracle |
| [wan30.link.non_http](W3L01-link-non-http/case.json) | Wan 3.0 · link 使用非 HTTP(S) 协议 | media-boundary | 禁用模板 | 协议错误无需真实页面 |
| [wan30.link.login_required](W3L02-link-login-required/case.json) | Wan 3.0 · link 页面需要登录 | media-boundary | 禁用模板 | 需要稳定返回登录页的测试站点 |
| [wan30.link.unreachable](W3L03-link-unreachable/case.json) | Wan 3.0 · link 页面不可访问 | media-boundary | 禁用模板 | 需要稳定不可达的测试地址 |
| [wan30.media.first_frame_only](W3M01-media-only-first-frame/case.json) | Wan 3.0 · 仅首帧图片 | dependencies | 禁用模板 | 需要稳定首帧图片素材 |
| [wan30.media.prompt_plus_first_frame](W3M02-prompt-plus-first-frame/case.json) | Wan 3.0 · Prompt + 首帧图片 | dependencies | 禁用模板 | 需要稳定首帧图片素材 |
| [wan30.media.first_last_frame](W3M03-first-last-frame/case.json) | Wan 3.0 · 首尾帧组合 | dependencies | 禁用模板 | 需要匹配的首尾帧素材 |
| [wan30.media.duplicate_first_frame](W3M04-duplicate-first-frame/case.json) | Wan 3.0 · 重复首帧 | dependencies | 禁用模板 | 需要两张合法图片以隔离数量错误 |
| [wan30.media.first_reference_conflict](W3M05-first-reference-conflict/case.json) | Wan 3.0 · 首帧与参考图互斥 | dependencies | 禁用模板 | 需要合法图片以隔离互斥错误 |
| [wan30.media.reference_image_count_10](W3M06-reference-image-count-10/case.json) | Wan 3.0 · 参考图数量上限 10 | dependencies | 禁用模板 | 需要 10 张确定性参考图 |
| [wan30.media.reference_image_count_11](W3M07-reference-image-count-11/case.json) | Wan 3.0 · 参考图数量 11 超限 | boundary | 禁用模板 | 需要 11 张合法参考图以隔离数量错误 |
| [wan30.media.reference_video_count_5_total_15](W3M08-reference-video-count-5-total-15/case.json) | Wan 3.0 · 5 个参考视频且总时长 15 秒 | dependencies | 禁用模板 | 需要 5 个各 3 秒的确定性视频 |
| [wan30.media.reference_video_count_6](W3M09-reference-video-count-6/case.json) | Wan 3.0 · 参考视频数量 6 超限 | boundary | 禁用模板 | 需要 6 个合法视频以隔离数量错误 |
| [wan30.media.reference_audio_count_5_total_15](W3M10-reference-audio-count-5-total-15/case.json) | Wan 3.0 · 5 个参考音频且总时长 15 秒 | dependencies | 禁用模板 | 需要 5 个各 3 秒的确定性音频 |
| [wan30.media.reference_audio_count_6](W3M11-reference-audio-count-6/case.json) | Wan 3.0 · 参考音频数量 6 超限 | boundary | 禁用模板 | 需要 6 个合法音频以隔离数量错误 |
| [wan30.media.file_single](W3M12-file-single/case.json) | Wan 3.0 · 单文件输入 | dependencies | 禁用模板 | 需要稳定可下载的合法文档 |
| [wan30.media.file_count_2](W3M13-file-count-2/case.json) | Wan 3.0 · 文件数量 2 超限 | boundary | 禁用模板 | 需要两个合法文件以隔离数量错误 |
| [wan30.media.link_single](W3M14-link-single/case.json) | Wan 3.0 · 单网页链接输入 | dependencies | 禁用模板 | 需要稳定且无需登录的公开页面 |
| [wan30.media.link_count_2](W3M15-link-count-2/case.json) | Wan 3.0 · 网页链接数量 2 超限 | boundary | 禁用模板 | 需要两个稳定公开页面以隔离数量错误 |
| [wan30.media.file_link_conflict](W3M16-file-link-conflict/case.json) | Wan 3.0 · file 与 link 互斥 | dependencies | 禁用模板 | 需要合法文件和公开页面以隔离互斥错误 |
| [wan30.media.reference_mix](W3M17-reference-mix/case.json) | Wan 3.0 · 图像视频音频参考组合 | dependencies | 禁用模板 | 需要三类相互匹配的确定性素材 |
| [wan30.video_extension.adaptive](W3M18-video-extension-adaptive/case.json) | Wan 3.0 · 视频延长使用 adaptive | dependencies | 禁用模板 | 需要 10 秒确定性视频及成品时长 oracle |
| [wan30.video_extension.nonadaptive_rejected](W3M19-video-extension-nonadaptive/case.json) | Wan 3.0 · 视频延长使用非 adaptive 比例 | dependencies | 禁用模板 | 需要合法视频以隔离比例依赖错误 |
| [wan30.duration.video_sum_30](W3M20-video-duration-sum-30/case.json) | Wan 3.0 · 输入输出视频总时长 30 秒 | boundary | 禁用模板 | 需要精确 10 秒的视频素材和 usage 时长证据 |
| [wan30.duration.video_sum_31](W3M21-video-duration-sum-31/case.json) | Wan 3.0 · 输入输出视频总时长 31 秒 | boundary | 禁用模板 | 需要精确 10 秒合法视频以隔离总时长错误 |
| [wan30.media.duplicate_last_frame](W3M22-duplicate-last-frame/case.json) | Wan 3.0 · 重复尾帧 | dependencies | 禁用模板 | 需要两张合法图片以隔离数量错误 |
| [wan30.media.last_frame_only_characterization](W3M23-last-frame-only/case.json) | Wan 3.0 · 仅尾帧行为刻画 | characterization | 禁用模板 | 官方只列首帧或首尾帧组合，未将仅尾帧定义为稳定错误 |
| [wan30.prompt.empty_characterization](W3P01-prompt-empty-characterization/case.json) | Wan 3.0 · 空 Prompt 行为刻画 | characterization | 禁用模板 | 官方未定义空字符串是无内容还是有效 Prompt，不预设通过/拒绝 |
| [wan30.transport.content_type_invalid](W3T01-content-type-invalid/case.json) | Wan 3.0 · Content-Type 非 JSON | transport | 禁用模板 | 运行器可发送该值，但传输错误状态/错误码需要独立 HTTP 合同断言 |
| [wan30.transport.async_header_empty](W3T02-async-header-empty/case.json) | Wan 3.0 · 异步 Header 为空 | transport | 禁用模板 | 异步 Header 缺失/空值的错误分类需要独立传输断言 |
| [wan30.transport.async_header_invalid](W3T03-async-header-invalid/case.json) | Wan 3.0 · 异步 Header 非 enable | transport | 禁用模板 | 异步 Header 错误值的错误分类需要独立传输断言 |
| [wan30.parameters.null_characterization](W3T04-parameters-null/case.json) | Wan 3.0 · parameters=null 行为刻画 | characterization | 禁用模板 | 官方仅声明 parameters 为可选 object，未定义显式 null 应等同省略还是拒绝 |
| [wan30.transport.malformed_json](W3T05-malformed-json/case.json) | Wan 3.0 · malformed JSON body | transport | 禁用模板 | 通用运行器会先序列化 map，无法发送 malformed JSON 原始字节 |
| [wan30.video.format_mp4](W3V01-video-mp4/case.json) | Wan 3.0 · MP4 参考视频 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.format_mov](W3V02-video-mov/case.json) | Wan 3.0 · MOV 参考视频 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.fps_16](W3V03-video-fps-16/case.json) | Wan 3.0 · 视频帧率下界 16fps | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.fps_15](W3V04-video-fps-15/case.json) | Wan 3.0 · 视频帧率 15fps | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.side_240](W3V05-video-side-240/case.json) | Wan 3.0 · 视频单边下界 240 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.side_239](W3V06-video-side-239/case.json) | Wan 3.0 · 视频单边 239 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.side_4096](W3V07-video-side-4096/case.json) | Wan 3.0 · 视频单边上界 4096 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.side_4097](W3V08-video-side-4097/case.json) | Wan 3.0 · 视频单边 4097 | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.size_100mb](W3V09-video-size-100mb/case.json) | Wan 3.0 · 视频大小 100MB | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.size_over_100mb](W3V10-video-size-over-100mb/case.json) | Wan 3.0 · 视频大小超过 100MB | media-boundary | 禁用模板 | 隔离视频格式或相邻边界 |
| [wan30.video.ratio_8_1](W3V11-video-ratio-8-1/case.json) | Wan 3.0 · 视频宽高比 8:1 | media-boundary | 禁用模板 | 隔离视频比例、格式或 URL 协议分区 |
| [wan30.video.ratio_over_8_1](W3V12-video-ratio-over-8-1/case.json) | Wan 3.0 · 视频宽高比超过 8:1 | media-boundary | 禁用模板 | 隔离视频比例、格式或 URL 协议分区 |
| [wan30.video.unsupported_format](W3V13-video-unsupported-format/case.json) | Wan 3.0 · 视频格式不受支持 | media-boundary | 禁用模板 | 隔离视频比例、格式或 URL 协议分区 |
| [wan30.video.protocol_oss](W3V14-video-oss/case.json) | Wan 3.0 · OSS 视频 URL | media-boundary | 禁用模板 | 隔离视频比例、格式或 URL 协议分区 |
| [wan30.reference_video.duration_1](W3VD01-video-duration-1/case.json) | Wan 3.0 · 参考视频单个时长下界 1 秒 | media-boundary | 禁用模板 | 隔离单个视频时长边界 |
| [wan30.reference_video.duration_15](W3VD02-video-duration-15/case.json) | Wan 3.0 · 参考视频单个时长上界 15 秒 | media-boundary | 禁用模板 | 隔离单个视频时长边界 |
| [wan30.reference_video.duration_below_1](W3VD03-video-duration-below-1/case.json) | Wan 3.0 · 参考视频时长低于 1 秒 | media-boundary | 禁用模板 | 隔离单个视频时长边界 |
| [wan30.reference_video.duration_above_15](W3VD04-video-duration-above-15/case.json) | Wan 3.0 · 参考视频时长超过 15 秒 | media-boundary | 禁用模板 | 隔离单个视频时长边界 |
| [wan30.reference_video.total_duration_over_15](W3VD05-video-total-duration-over-15/case.json) | Wan 3.0 · 参考视频总时长超过 15 秒 | media-boundary | 禁用模板 | 两个视频各自合法但总时长 16 秒 |
