# Wan 3.0 API 完整测试矩阵

## 范围

- Provider/API：阿里云百炼原生 Wan 3.0 HTTP API，创建任务 `POST /api/v1/services/aigc/video-generation/video-synthesis` 与查询任务 `GET /api/v1/tasks/{task_id}`。
- 模型：`wan3.0-video`、`wan3.0-video-prime`；官方说明 Prime 能力与标准版对齐，因此共用合同矩阵，但执行证据必须分别记录。
- 模式：文生视频、首帧/首尾帧、参考生视频、文件/网页生视频、视频编辑、视频延长，以及完整异步生命周期。
- 明确排除：Python SDK 封装行为、价格正确性、主观画质评分和未文档化的容错行为。它们不计入本矩阵完成率。
- 官方文档检索日期：2026-09-05。主来源为 [S1：Wan 3.0 API 参考][S1]，通用错误分类来源为 [S2：百炼错误码][S2]。

## 判定约定

- `success`：创建任务返回非空 `output.task_id`，随后必须轮询至 `SUCCEEDED`；仅有 HTTP 2xx 或任务受理不算业务成功。
- `parameter rejection`：稳定证据为 `InvalidParameter` 参数错误。异步 API 可能在创建阶段拒绝，也可能创建后进入 `FAILED`；矩阵标为 `blocked` 的组合场景需要执行器同时支持这两个失败阶段。
- `artifact assertion`：涉及音轨、水印、素材语义、分辨率或时长成品属性时，必须检查下载产物或对应稳定元数据，不能仅断言存在 `video_url`。
- `Design` 表示本行所需场景是否已经在矩阵中定义；`Implementation` 表示当前仓库是否已有足够的 `case.json` 与断言；`Execution` 单独记录静态、模拟或真实 provider 证据。

具体落盘清单见 [Wan 3.0 具体测试 Case 矩阵](wan3-case-matrix.md)。当前共 191 个 Wan3 Case：74 个自动 Case 覆盖无需外部素材的请求合同，117 个禁用模板记录素材、环境和产物/语义 oracle 的精确输入与启用前置条件。下文引用 `W30xx`、`W3Mxx` 等目录编号时，均可在具体 Case 矩阵中定位到对应 `case.json`。

## A. 传输、鉴权与顶层结构

| ID | Contract/path | Type / presence / default | Valid partitions | Invalid partitions / boundaries | Dependencies | Mandatory scenarios and expected result | Existing evidence | Tier | Design | Implementation | Execution |
|---|---|---|---|---|---|---|---|---|---|---|---|
| T01 | [S1] Endpoint host、`WorkspaceId`、地域 | deployment constraint；required | 北京、新加坡、东京、法兰克福、弗吉尼亚同地域配置 | Workspace/model/key 跨地域；缺失或错误 WorkspaceId | model、Endpoint、API Key 必须同地域 | 静态验证 5 类 host；至少一个同地域 success；一个跨地域配置必须失败且不可算参数通过 | W3E01-W3E02 concrete disabled templates | T0/T1 | complete | deferred: 需独立地域环境 | not run |
| T02 | [S1] `Content-Type` | string；required；`application/json` | 精确 `application/json` | missing、其他 MIME、不可解析 JSON body | POST JSON | 正确值进入任务流程；缺失/错误 MIME 或 malformed JSON 被拒绝 | header override unit test；W3T01/W3T05 templates | T0/T1 | complete | partial | simulated only |
| T03 | [S1] `Authorization` | string；required；Bearer API key | 有效同地域 Bearer key | missing、空值、非 Bearer、无效 key、跨地域 key | Endpoint/model region | 有效 key 可创建；每个无效鉴权分区必须失败，且不能被负向参数用例误判为通过 | auth-misclassification unit tests；W3E03-W3E05 templates | T0/T1 | complete | partial | simulated only |
| T04 | [S1] `X-DashScope-Async` | fixed string；required；no default | `enable` | missing、其他字符串、错误类型 | HTTP 仅支持异步 | `enable` 实际发出；missing/other 明确失败 | all automatic cases；header forwarding unit test；W3T02-W3T03 | T0/T1 | complete | partial | simulated only |
| T05 | [S1] `model` | string；required | `wan3.0-video`、`wan3.0-video-prime` | missing、空、未知模型、错误类型 | model/Endpoint/key region | 两个合法模型分别 success；其余分区参数拒绝或明确模型错误 | W3009-W3013；model_mode unit test | T0/T1/T2 | complete | covered | T0 mapping/dry-run only |
| T06 | [S1] `input` | object；required | object containing a valid prompt/media decision row | missing、null、array、string、number | `prompt`/`media` 至少一个有效 | 有效 object 进入任务；每个错误 shape 被拒绝 | W3008, W3014-W3016；有效 input 贯穿全部正向 Case | T1 | complete | covered | not provider-run |
| T07 | [S1] `parameters` | object；optional；field defaults apply when omitted | omitted、empty object、valid populated object | array/string/number；null 作为未文档化 characterization | child parameter rules | omitted 必须使用各字段默认值并 success；错误 shape 被拒绝 | W3017-W3018, W3020-W3021, W3075；W3T04 null characterization | T1/T2 | complete | covered | not provider-run |

## B. `input` 与媒体合同

| ID | Contract/path | Type / presence / default | Valid partitions | Invalid partitions / boundaries | Dependencies | Mandatory scenarios and expected result | Existing evidence | Tier | Design | Implementation | Execution |
|---|---|---|---|---|---|---|---|---|---|---|---|
| I01 | [S1] `input.prompt` / `input.media` presence | conditional union；至少一个 | prompt-only；media-only；prompt+media | neither；无有效内容 | 模式由 media 类型和 prompt 意图共同决定 | 三个有效决策行 success；neither 被参数拒绝 | W3001/W3008；W3M01-W3M02 concrete templates | T1/T2 | complete | partial | automatic prompt/neither；media fixtures not run |
| I02 | [S1] `input.prompt` | string；conditional；≤20000 chars，超出自动截断 | 1 字符、普通值、20000 字符、20001 字符截断；中英文 | wrong type；空字符串行为未文档化，单列 characterization | 全能参考模式可引用 media 顺序 | 1/20000 success；20001 success 且必须证明截断而非仅完成；wrong type 拒绝 | W3022-W3025；W3P01 characterization | T1/T2 | complete | partial: 请求边界完整，截断语义无稳定 oracle | not provider-run |
| I03 | [S1] `input.media` 与 `media[]` shape | array；conditional；item object；`type`/`url` required | 非空数组；每项同时有 string type/url | media wrong type、非 object item、缺 type、缺 url、字段 wrong type、无 prompt 的空数组 | I04-I17 | 每种 malformed shape 参数拒绝；至少一个合法 item success | W3067-W3074；W3M01 valid template | T1 | complete | partial | malformed automatic；valid fixture not run |
| I04 | [S1] `media[].type` | enum；required | `first_frame`、`last_frame`、`reference_image`、`reference_video`、`reference_audio`、`file`、`link` | unknown、wrong type、case/whitespace variant | 每种类型有不同资产与组合合同 | 每个合法行为分区至少一个 success；unknown/wrong type 拒绝 | W3071-W3072；W3M/W3I/W3V/W3AU/W3F/W3L templates | T2 | complete | deferred: Case 已落盘，需确定性素材 | not run |
| I05 | [S1] `first_frame` / `last_frame` | image media；each max 1 | first only；first+last | duplicate first、duplicate last、last-only（不在官方支持组合中）、与 reference/file/link 混用 | 只允许首帧或首尾帧模式 | 两个合法组合 success；每个互斥/数量违规必须业务失败 | W3M01-W3M05, W3M22-W3M23；runner supports terminal rejection | T2 | complete | deferred: Case 已落盘，需图片素材 | not run |
| I06 | [S1] `reference_image` cardinality | image array subset；max 10 | count 1、代表中间值、10 | 11 | 可与 reference video/audio 及 file 或 link 组合 | 1/10 success；11 业务拒绝 | W3M06-W3M07 | T2/T3 | complete | deferred: Case 已落盘，需 10+1 个素材 | not run |
| I07 | [S1] image `url` payload | JPEG/JPG/PNG(no alpha)/BMP/WEBP；HTTP(S)/OSS/Base64；≤20MB | 每个格式/协议代表；单边 240、8000；ratio 8:1；20MB | 透明 PNG、unsupported format；单边 239/8001；ratio >8:1；>20MB；malformed Base64/URL | type 为 first/last/reference_image | 对每类边界执行 n/n+1 或 min-1/min；合法产物 success，非法素材稳定失败 | W3I01-W3I10 concrete templates | T2/T3 | complete | deferred: Case 已落盘，需边界素材工厂 | not run |
| I08 | [S1] `reference_video` count/duration | video subset；max 5；single [1,15]s；total ≤15s | count 1/5；single 1/15s；total exactly 15s | count 6；single <1/>15s；total >15s | P04 输入+输出总时长 | 每个计数和时长边界分别验证；违规必须业务失败 | W3M08-W3M09, W3VD01-W3VD05 | T2/T3 | complete | deferred: Case 已落盘，需确定性视频集 | not run |
| I09 | [S1] video `url` payload | mp4/mov；HTTP(S)/OSS；≥16fps；single side [240,4096]；ratio ≤8:1；≤100MB | 两格式/协议代表；fps16；side240/4096；ratio8:1；100MB | unsupported format；fps15；side239/4097；ratio>8:1；>100MB | type=`reference_video` | 每类合法边界 success，紧邻非法值业务失败 | W3V01-W3V10 concrete templates | T2/T3 | complete | deferred: Case 已落盘，需媒体边界素材工厂 | not run |
| I10 | [S1] `reference_audio` count/duration | audio subset；max 5；single [1,15]s；total ≤15s | count1/5；single1/15s；total15s | count6；single<1/>15s；total>15s | 可与 reference image/video 组合；不能与 first/last 混用 | 每个计数/时长边界 success 或业务拒绝 | W3M10-W3M11, W3AD01-W3AD05 | T2/T3 | complete | deferred: Case 已落盘，需确定性音频集 | not run |
| I11 | [S1] audio `url` payload | wav/mp3；HTTP(S)/OSS；≤15MB | 两格式/协议代表；15MB | unsupported format；>15MB；malformed URL | type=`reference_audio` | 合法代表 success；格式/大小/URL 违规业务失败 | W3AU01-W3AU06 concrete templates | T2/T3 | complete | deferred: Case 已落盘，需媒体边界素材工厂 | not run |
| I12 | [S1] `file` media | max1；docx/doc/xlsx/xls/pptx/ppt/pdf/txt/key/pages/numbers/md；HTTP(S)/OSS；≤100MB；部分格式≤50页 | 每个解析行为分区代表；count1；100MB；50页 | count2；unsupported format；>100MB；51页；malformed URL | 与 link 互斥；可与 reference types 组合 | 格式代表与边界 success；n+1 和互斥违规业务失败 | W3M12-W3M13, W3F01-W3F05 | T2/T3 | complete | deferred: Case 已落盘，需文档边界素材工厂 | not run |
| I13 | [S1] `link` media | max1；public HTTP(S) page | count1、无需登录公开页面 | count2、非 HTTP(S)、需登录/不可访问、malformed URL | 与 file 互斥；可与 reference types 组合 | 公开页面 success；协议、数量、访问条件违规业务失败 | W3M14-W3M15, W3L01-W3L03 | T2 | complete | deferred: Case 已落盘，需稳定测试页面 | not run |
| I14 | [S1] media combination decision table | cross-field union | references 自由组合；file XOR link 可与 references 组合 | first/last 与任一 reference/file/link 混用；file+link | I05-I13；prompt 可描述媒体编号 | 覆盖每个有效组合行为分区与每条独立互斥规则；违规必须业务失败 | W3M05, W3M16-W3M17；dual-phase rejection implemented | T2/T3 | complete | deferred: Case 已落盘，需组合素材 | not run |
| I15 | [S1] prompt media indexing | ordered semantic reference | 图像、视频、音频各自按数组顺序独立编号 | 越界编号/错误指代行为未文档化，限 characterization | prompt+reference media | 每类编号顺序需通过语义可判定素材验证；未文档行为不得算合同失败 | W3IDX01-W3IDX03 | T3 | complete | deferred: Case 已落盘，缺稳定语义 oracle | not run |
| I16 | [S1] video edit mode | reference_video + edit-intent prompt | video only；video+image；video+audio；video+image+audio | 无 reference_video 或无编辑意图的行为不应臆测为固定错误 | reference combinations | 四个官方有效组合各有 success；输出需证明编辑语义 | W3ED01-W3ED04 | T3 | complete | deferred: Case 已落盘，需素材与语义 oracle | not run |
| I17 | [S1] video extension mode | reference_video + extension-intent prompt；ratio must adaptive | 官方四类 reference 组合且 ratio=`adaptive` | non-adaptive ratio；缺 reference_video；无延长意图属于 characterization | P02 ratio；P04 combined duration | 有效组合 success；non-adaptive 必须业务失败；输出证明延长 | W3M18-W3M19；dual-phase rejection implemented | T2/T3 | complete | deferred: Case 已落盘，需素材与语义 oracle | not run |

## C. `parameters` 合同

| ID | Contract/path | Type / presence / default | Valid partitions | Invalid partitions / boundaries | Dependencies | Mandatory scenarios and expected result | Existing evidence | Tier | Design | Implementation | Execution |
|---|---|---|---|---|---|---|---|---|---|---|---|
| P01 | [S1] `parameters.resolution` | string enum；optional；default `1080P` | omitted、`480P`、`720P`、`1080P` | unknown (`360P`)、wrong type | 输出 usage.SR/产物尺寸 | omitted/default 与三个显式值分别 success 并验证分辨率；unknown/wrong type 拒绝 | W3006, W3039-W3043；W3A05 artifact template | T1/T2/T3 | complete | partial: 请求合同覆盖，产物尺寸待 oracle | no provider evidence |
| P02 | [S1] `parameters.ratio` | string enum；optional；default `adaptive` | omitted、`adaptive`、`16:9`、`4:3`、`1:1`、`3:4`、`9:16` | unknown (`2:1`)、wrong type | media 可影响 adaptive；video extension requires adaptive | omitted/default 与 6 个显式值各覆盖行为分区；unknown/wrong type 拒绝；产物/usage 比例正确 | W3007, W3044-W3051；W3A06 artifact template | T1/T2/T3 | complete | partial: 请求合同覆盖，产物比例待 oracle | no provider evidence |
| P03 | [S1] `parameters.duration` without video | integer union；optional；default5；`-1` or [2,30] | omitted、-1、2、代表中间值、30 | -2、1、31、wrong type、non-integer | 直接影响费用；-1 为智能时长 | 覆盖 -2/-1/1/2/interior/30/31/wrong type 与 omitted；成功值验证 usage/output duration | W3001-W3003, W3026-W3032；expected_usage supported | T1/T2/T3 | complete | covered | no provider evidence |
| P04 | [S1] `parameters.duration` with video | integer combined budget；input video total + output ≤30 | sum<30、sum=30；-1 交互需 provider 验证 | sum>30 | I08 input video duration；usage input/output durations | 至少构造 sum29/30/31；明确 -1+video 行为；success 验证 usage，违规业务失败 | W3M20-W3M21 concrete templates | T2/T3 | complete | deferred: Case 已落盘，需精确时长素材 | not run |
| P05 | [S1] `parameters.audio` | boolean；optional；default true | omitted、true、false | wrong type | 输出音轨；价格不变不属于功能断言 | 三个合法分区 success；wrong type 拒绝；成品分别有/无音轨 | W3052-W3055；W3A01-W3A02 artifact templates | T1/T3 | complete | partial: 请求合同覆盖，音轨待 oracle | not run |
| P06 | [S1] `parameters.seed` | integer union；optional；default random；`-1` or [0,2147483647] | omitted、-1、0、interior、2147483647 | -2、2147483648、wrong type、non-integer | 同 seed 不保证完全一致，不做确定性像素断言 | 覆盖 -2/-1/0/interior/max/max+1/wrong type/omitted；只断言合同接受/拒绝 | W3004-W3005, W3033-W3038；W3001 seed=0 | T1/T2 | complete | covered | no provider evidence |
| P07 | [S1] `parameters.prompt_extend` | boolean；optional；default true | omitted、true、false | wrong type | 可能增加耗时；语义改写不可用任务完成替代 | 三个合法分区 success；wrong type 拒绝；若声称改写效果需独立 semantic oracle | W3056-W3059 | T1/T3 | complete | covered: 仅合同接受/拒绝，不声称语义效果 | not run |
| P08 | [S1] `parameters.watermark` | boolean；optional；default false | omitted、false、true | wrong type | 输出视觉水印 | 三个合法分区 success；wrong type 拒绝；成品分别无/有水印 | W3060-W3063；W3A03-W3A04 artifact templates | T1/T3 | complete | partial: 请求合同覆盖，视觉水印待 oracle | not run |

## D. 异步响应与生命周期

| ID | Contract/path | Type / presence / default | Valid partitions | Invalid partitions / boundaries | Dependencies | Mandatory scenarios and expected result | Existing evidence | Tier | Design | Implementation | Execution |
|---|---|---|---|---|---|---|---|---|---|---|---|
| W01 | [S1] create `output.task_id` / `task_status` | response object；task_id required on success | nonempty task_id；PENDING | 2xx with missing/empty task_id；malformed body | create accepted | 只有非空 task_id 才算创建成功；否则 runner fail | RunWanVideoCase unit test | T0/T1 | complete | covered | simulated T0 |
| W02 | [S1] poll state machine | enum states | PENDING→RUNNING→SUCCEEDED | FAILED、CANCELED、UNKNOWN；unknown enum；timeout | GET `/api/v1/tasks/{id}` | 中间态继续轮询；每个终态停止并正确分类；timeout/cancel fail | PENDING/RUNNING/SUCCEEDED unit path | T0/T1/T2 | complete | covered | simulated T0 |
| W03 | [S1] terminal success/failure | discriminated response | SUCCEEDED + valid video_url | SUCCEEDED missing/unsafe URL；FAILED/CANCELED/UNKNOWN | W02 | success 必须有安全 HTTP(S) URL；失败终态绝不能 pass | success URL and unsafe URL unit tests；无完整失败终态表 | T0/T2 | complete | partial | simulated only |
| W04 | [S1] `output` metadata | strings/timestamps；video_url TTL 24h | submit/scheduled/end order；orig_prompt；video_url available before expiry | malformed chronology；missing success URL；URL expired after 24h | terminal SUCCEEDED | 验证字段 shape、时间顺序和下载窗口；不得在报告中泄漏签名 URL | 仅 video_url host 检查 | T0/T2 | complete | partial | simulated only |
| W05 | [S1] `usage` | object on success | video_count=1；duration；input/output duration；fps=30；SR；ratio | missing/inconsistent fields when claim depends on them | P01-P05 and media input | 针对时长、分辨率、比例的 case 必须验证对应 usage/产物；video_count 固定1 | unit test only checks video_count | T0/T2/T3 | complete | partial | simulated only |
| W06 | [S1] task/result expiry | time lifecycle；24h | query/download within 24h | task query after expiry→UNKNOWN；result URL expired | clock and provider retention | 使用可控过期 fixture 或 provider 已过期 task；不可通过真实等待24h作为默认测试 | none | T2/T3 | complete | deferred: 需要可复用过期任务 fixture | not run |
| W07 | [S1][S2] error phase and shape | top-level create error or terminal `output.code/message` | documented parameter failure carries stable code/message | unrelated auth/billing/rate errors must not satisfy boundary case | async validation phase may vary | 同一负向断言可接受 admission InvalidParameter 或 terminal FAILED InvalidParameter，并排除欠费/鉴权/限流 | admission + terminal rejection unit tests；non-parameter safeguards | T0/T1 | complete | covered | simulated T0 |

## 审计摘要

- 合同矩阵共 39 行：`covered` 9、`partial` 13、`deferred` 17、`missing` 0、`blocked` 0、documentation conflict 0。
- 具体实现：191 个 Wan3 `case.json`，其中自动 Case 74 个（正向 35、负向 39），禁用模板 117 个（正向 66、负向 51）；两个 Wan3 模型套件都显式引用这 191 个 Case。
- 自动 Case 已覆盖 model、input/parameters shape、Prompt 长度/类型、duration、seed、resolution、ratio、audio、prompt_extend、watermark 与 media shape/type 的无需素材分区。
- 禁用模板不是“已覆盖”证据：它们把地域/鉴权、原始传输错误、图片/视频/音频/文件/网页素材边界、媒体组合、视频编辑/延长、音轨/水印/尺寸/比例 oracle 的具体输入和前置条件落盘，待 fixture 与 oracle 就绪后才能启用。
- 运行器已支持创建阶段 HTTP 400 与任务终态 `FAILED` 两阶段 `InvalidParameter` 判定、Prompt 长度生成、model 缺失/错误类型注入、Case Header 覆盖及 usage 字段预期。
- 执行层仍只有 T0 静态/模拟/dry-run 证据；没有真实 provider T1/T2/T3 证据，也没有获得付费执行授权。

[S1]: https://help.aliyun.com/zh/model-studio/wan3-video-generation-api-reference
[S2]: https://help.aliyun.com/zh/model-studio/error-code/
