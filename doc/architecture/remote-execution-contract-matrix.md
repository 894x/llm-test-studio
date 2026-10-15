# 远程执行当前提交契约与验证矩阵

范围：自有 runner 管理协议的 JobSpec、ExecutionTarget、Submission、asset manifest、事件和控制命令。规则来源为[远程执行方案](remote-execution-plan.md)，记录日期 2026-10-15；这是管理协议，不定义提供商参数。全部用例为 T0 本地验证，不消耗提供商额度。当前事件与操作确认已有存储测试；HTTP 路由、身份鉴权、队列、运行控制与结果投影的执行证据仍缺失。

结构清单：Submission 只有 job 与 secrets；job 包含 contract、job_id、node_installation_id、engine_build_id、target、kind、spec、assets；target 包含 kind、node_id；spec 恰有 run、performance、comparison 之一。秘密条目只有 slot 与 value，value 为 JSON base64 字节。asset 条目只有 ref、sha256、size、media_type。run 与 performance 直接复用 A1 至 A3 的准备值；comparison 包含 meta、plan、model、runs，复用 A4a 的整组准备值。

新字段没有历史默认或旧格式回退。assets 必须为数组，没有文件时显式传 []。本地和远程都携带执行宿主的安装身份与实际构建；不要求客户端自身构建与 runner 相同。本地 target 禁止 node_id，remote target 必须携带节点回读的共享 UUID。资产 ref 是 1 至 80 字节的小写字母数字及点、下划线、连字符，首字符须是字母数字；禁止任意文件路径。media_type 是无参数的规范小写 MIME 类型，最多 127 字节。

| 行 | 字段 / 类型 / 存在性 | 默认、有效与无效分区 | 必需场景与边界 | 依赖与结果 | 设计 / 当前执行 |
| --- | --- | --- | --- | --- | --- |
| C01 | job.contract / int / 必填 | 只接受当前 1；缺失、0、2、字符串拒绝 | 缺失、1、非当前、类型错误 | admission 拒绝 unsupported_contract；不解析历史版本 | 已设计 / 见下方证据 |
| C02 | job_id、node_installation_id / string / 必填 | 规范小写 UUID；空、错误长度、非十六进制、大小写变体拒绝 | 每字段有效、缺失、错误 UUID | admission 拒绝 invalid_spec | 已设计 / 见下方证据 |
| C03 | engine_build_id / string / 必填 | 探测到的 runner 实际构建，64 字节小写 SHA-256；空、63、64、65、非十六进制、大小写变体 | 长度和字符边界；核对提交期间 runner 未被替换，不比较客户端构建 | admission 拒绝 invalid_spec 或 build_mismatch | 已设计 / 见下方证据 |
| C04 | target.kind / enum / 必填 | local、remote；缺失、未知拒绝 | local 无节点、remote 有节点；其余组合拒绝 | node_id 的交叉约束；未知配置节点由 Coordinator 拒绝 | 已设计 / 见下方证据 |
| C05 | target.node_id / string / 条件必填 | remote 规范 UUID；local 必须为空 | 缺失、有效、错误、交叉组合 | admission 拒绝 invalid_target | 已设计 / 见下方证据 |
| C06 | kind、spec / enum 与联合 / 必填 | run、performance、comparison 各对应恰一 payload | 0、1、2、3 payload；类型不符；未知 kind | admission 拒绝 invalid_spec | 已设计 / 见下方证据 |
| C07 | spec.run / PreparedRun | 当前领域快照；初始 revision=1 | 有效、坏快照、非初始 revision | 复用 domain.NewRun；拒绝不创建 Run | 已设计 / 见下方证据 |
| C08 | spec.performance / PreparedPerformance | A3 当前协议、地址、模型、profile 和 ReportID | 有效、端点不一致、错误 ID、非法 profile | 复用 PreparedPerformance.Validate；不回查桌面连接 | 已设计 / 见下方证据 |
| C09 | spec.comparison / PreparedComparison | 2 至 32 子 Run；ID 唯一、相同冻结 Plan/Model；初始 revision=1 | 0、1、2、32、33；重复、不同定义、不同修订 | 复用整组 validator；全组接收前拒绝 | 已设计 / 见下方证据 |
| C10 | assets / array / 必填 | [] 或 manifest；null、缺失拒绝 | 0、1、128、129 | 总量约束与引用检查；实际文件存在性在宿主验证 | 已设计 / 见下方证据 |
| C11 | asset.ref / string / 必填 | 受限逻辑名；禁止路径、控制字符、重复 | 0、1、80、81 字节；../、斜杠、重复 | admission 拒绝 invalid_asset | 已设计 / 见下方证据 |
| C12 | asset.sha256 / string / 必填 | 64 字节小写 SHA-256；同 manifest 不可重复 | 63、64、65；错误字符、重复 | 内容摘要实测在上传与宿主接收验证 | 已设计 / 见下方证据 |
| C13 | asset.size / int64 / 必填 | 1 至 1 GiB；每 Job 总和至 4 GiB | 0、1、1 GiB、1 GiB+1；4 GiB、4 GiB+1 | admission 拒绝；上传长度与摘要独立验证 | 已设计 / 见下方证据 |
| C14 | asset.media_type / string / 必填 | 规范小写无参数 MIME；最长 127 字节 | 空、有效、参数、大小写、非法、127/128 | admission 拒绝 invalid_asset | 已设计 / 见下方证据 |
| C15 | secrets / array / 必填 | slot 与所需 Run/Report ID 恰好一一对应 | 缺失、额外、重复、错误、32、33 条 | admission 拒绝 invalid_secrets；不持久化秘密 | 已设计 / 见下方证据 |
| C16 | secret.value / bytes / 必填 | 1 至 8192 字节；秘密不能全为空白 | 0、1、8192、8193、空白、非 base64 | admission 拒绝 invalid_secrets；错误消息无秘密 | 已设计 / 见下方证据 |
| C17 | JSON / bytes | 最多 8 MiB；拒绝未知自有 envelope 字段、尾随文档和错误类型 | 上限、上限+1、尾随、未知字段、错类型 | 有界解码；嵌套领域对象沿用当前领域 decoder | 已设计 / 见下方证据 |
| C18 | digest / canonical JSON | 对象 key 顺序与 asset 排序无关；业务数据变化必须改变摘要 | JSON roundtrip、map/asset 顺序变化、payload变化 | 不含 secrets；凭据承诺由宿主 HMAC 单独计算 | 已设计 / 见下方证据 |
| C19 | receipt、snapshot / 当前状态 | queued revision=1；后续 revision>=2；sequence>=revision，计数不超过 int64；UTC 非零时间不倒退；显式唯一子身份 | 零、最大值、溢出、错误状态及失败详情组合 | 状态 CAS 与事件同事务；终态不可再转换 | 已设计 / 见下方证据 |
| C20 | event、event page / 有界序列 | 完整事件最多 8 MiB+64 KiB；完整页面再加 4 KiB；limit=1..200；after 不超过权威最新序号 | 极限完整事件、超限和转义膨胀；连续前缀续页；缺记录、额外记录和损坏记录 | 只返回连续持久序号；字节截页保留可续游标 | 已设计 / 见下方证据 |
| C21 | command / 当前固定对象 | contract=1、规范 command_id/job_id、kind=stop_sending/cancel、expected_revision=1..int64最大值-1；完整 JSON 最多 4 KiB | 缺失、未知、错误类型、零、最大与溢出；精确大小写、重复与多余字段；上限与上限+1 | 全 installation 唯一命令身份；更改任一业务字段冲突 | 已设计 / 见下方证据 |
| C22 | command confirmation / 状态决策 | queued stop→cancelled；starting/running stop→draining；draining stop 不改 revision；活动 cancel→cancelled；终态拒绝新操作 | 7 状态×2 操作；确认丢失后重传；完成与取消竞争；状态/事件/确认写故障 | 确认保存原决策快照，不把旧确认当作当前状态；重传不再转换 | 已设计 / 见下方证据 |
| C23 | host policy / 显式资源 | 活动槽 1..128；排队槽 0..1024；总并发 1..65536；排队与执行时限 >0 且 <=24h | 缺失、下限、上限、超限；顺序 Entry 峰值、warmup、open-loop 实际 cap、Comparison 求和 | 超预算接收前拒绝，不削减工作量；排队超时不调用执行器 | 已设计 / 见下方证据 |
| C24 | host ownership / 生命周期 | 接收后拥有上下文、秘密和 reservation；同 ID 不重启；存储故障停止新调度；终态收尾有持久确认 | 接收/命令确认丢失、请求取消、队列与执行超时、退出、重启、慢收尾、释放失败 | 重启仅 Settle、不得 Execute；foreign owner 保留；progress 限频且共事务 | 已设计 / 见下方证据 |
| C25 | Performance checkpoint / 阶段事实 | stage=preparing/warmup/ramp/measured/capacity；boundary=entered/completed/returned；capacity 有合法 rung_index，其余禁止；完整 checkpoint 最多 32 MiB | 全阶段顺序；0/1/N 容量阶梯；回调修改隔离；阶段开始/结束/返回写失败；取消后独立存储时限；提前停止不进入下一阶段 | 阶段进入先观察再发请求；阶段完成先观察再推进；回调失败停止并释放秘密，不重试上游；returned 不能把未知阶段流量解释为零 | 已设计 / 见下方证据 |
| C26 | Performance observation/result / 持久收尾 | 最新 checkpoint 使用 CAS 与小型事件共事务；结果明确 not_started/available/interrupted；只允许唯一终态与封口引用，报告身份不变 | 写入/事件回滚；确认丢失；重复与冲突；已终态未封口恢复；无观察记录和阶段中崩溃；部分容量观察保留 | 统计归档仍采用当前 measured 验证规则；完整 Job 结果另存真实阶段观察；Settle 与恢复零上游请求 | 已设计 / 见下方证据 |
| C27 | observation.contract/job_id/revision/recorded_at/checkpoint | 当前 contract；规范 Job UUID 与已接收 Performance 输入一致；revision=1..int64 最大值；UTC 非零且不早于接收和前一观察；完整快照有 32 MiB 限制，wrapper 另留 4 KiB | 缺失/其他 contract；错误 owner/模型/报告；0/1/最大/溢出 revision；无效/非 UTC/倒退时间；边界乱序、已记录事实改写 | CAS 与 Job revision 分开；同 expected_revision 与相同 checkpoint 重试返回原时间；旧文档损坏拒绝且不修复 | 已设计 / 见下方证据 |
| C28 | result.contract/job_id/report_id/terminal/sealed_at/execution_started/observation_state/observation | 仅当前 contract；稳定 owner 与 report；权威唯一终态；UTC 封口时间不早于终态或观察；未启动必须无观察且有未启动证据；available 必须 returned；其余已启动但未 returned 为 interrupted | 三种观察状态及错误组合；完整/部分/无观察；缺失开始日志不得当作未启动；completed 必须存在最终返回观察 | 只读恢复并封口一次，保留原时间和 canonical 文档；迟到观察允许到封口前；封口后拒绝新增观察 | 已设计 / 见下方证据 |
| C29 | result reference.job_id/report_id/sha256/size | 规范 UUID、64 字节小写 SHA-256；size=1..完整结果预算；预算为 checkpoint、观察 wrapper、最大终态事件与结果头之和 | 0/1/上限/上限+1，错误摘要和身份；原始下载字节与摘要相同；重传及并发不新增封口事件 | 结果引用与统计 ReportReference 分开；checkpoint 事件只通知当前观察更新，不承诺下载历史大文档 | 已设计 / 见下方证据 |

RunSnapshot、Plan、Case、输入与 PerformanceProfile 的子字段仍以当前领域 validator 及对应已有测试为规则来源。本矩阵不据此宣称这些既有合同已穷尽所有组合。每行只有全部必需场景有断言和执行记录后才可标为 covered；有缺项保持 partial。

## 当前执行证据

以下为 T0 package 测试，已通过 `go test -p 1 ./internal/jobcontract` 与 `go vet ./internal/jobcontract`。测试名均位于 `internal/jobcontract/`；没有 HTTP 或真实节点证据。

| 行 | 已执行的主要断言 | 状态与剩余边界 |
| --- | --- | --- |
| C01 | TestSpecValidatesIdentityAndExclusivePayload、TestSubmissionDecodeLimitsAndRejectsUnknownFields | covered：缺失、当前、非当前与类型错误 |
| C02 | TestIdentityFieldsRequireCanonicalUUIDsAndAssetHashes | covered：两个身份字段分别验证空、错长、非十六进制和非规范大小写 |
| C03 | TestSpecValidatesIdentityAndExclusivePayload | partial：格式分区通过；实际构建相等由宿主阶段验证 |
| C04、C05 | TestSpecValidatesIdentityAndExclusivePayload | partial：显式位置与交叉组合通过；配置节点存在性待 Coordinator |
| C06 | TestSpecValidatesIdentityAndExclusivePayload、TestRunAndWholeComparisonPayloadValidation | partial：0/1/2/3 payload、三种类型与未知 kind 通过；JSON 联合对象类型矩阵待 HTTP 层补齐 |
| C07 | TestRunAndWholeComparisonPayloadValidation | partial：有效输入与非初始 revision；坏快照全部格式分区待补齐 |
| C08 | TestSpecValidatesIdentityAndExclusivePayload | covered：当前 Performance validator、错误身份、端点不一致和 profile |
| C09 | TestRunAndWholeComparisonPayloadValidation；comparisons/preparation_test.go 的冻结定义变更测试 | partial：数量、重复 ID、不同 Case 定义已验证；其余引用／修订组合待补齐 |
| C10 | TestAssetManifestBoundaries、TestDecoderRejectsOversizedCollectionsBeforeParsingTheirRemainingElements | partial：数组存在性、0/1/128/129 与解码前数量限制通过；实际文件存在性待宿主 |
| C11 | TestAssetManifestBoundaries | partial：长度、路径穿越、重复已验证；控制字符与 Unicode 专项待补齐 |
| C12 | TestAssetManifestBoundaries、TestIdentityFieldsRequireCanonicalUUIDsAndAssetHashes | covered：合法、长度、字符、大小写与重复 |
| C13 | TestAssetManifestBoundaries | partial：描述中的单文件及总字节边界通过；上传真实字节核验待实现 |
| C14 | TestAssetManifestBoundaries | covered：空、有效、非法、参数、大小写与长度边界 |
| C15 | TestSubmissionRequiresExactBoundedSecretSlots、TestRunAndWholeComparisonPayloadValidation、TestDecoderRejectsOversizedCollectionsBeforeParsingTheirRemainingElements | covered：缺失、多余、错误、重复、32 与解码前超限拒绝 |
| C16 | TestSubmissionRequiresExactBoundedSecretSlots、TestSubmissionDecodeLimitsAndRejectsUnknownFields | covered：0/1/8192/8193、空白、非法 base64；仅接受规范 base64 字符串，拒绝数字数组 |
| C17 | TestSubmissionDecodeLimitsAndRejectsUnknownFields、decode_test.go | partial：8 MiB、超限、尾随、未知字段、非规范大小写及三个自有集合的分配前拒绝通过；嵌套领域集合资源审计和 HTTP 错误映射待补齐 |
| C18 | TestSpecFreezeAndDigestOwnMutableInputAndNormalizeAssetOrder、TestSpecDigestCanonicalizesNativeObjectsWithoutRoundingLargeIntegers | covered：对象 key 与资产排序不影响摘要，业务变更与 2^53 附近不同整数影响摘要，冻结副本隔离 |
| C19 | TestSnapshotStateValidationBoundaries；SQLite repository_jobs_test.go 的 CAS、终态和事务故障测试 | partial：当前状态、计数、时间、子身份及事务边界已验证；HTTP 控制与竞争调度待宿主 |
| C20 | TestEventPayloadAndCompleteEnvelopeByteBoundaries；SQLite TestJobEvent* | partial：完整序列化字节、转义、条数、权威序号、字节续页与损坏拒绝通过；HTTP 传输与桌面投影待实现 |
| C21 | TestCommandValidationBoundaries、TestCommandDecoderStrictFieldsAndByteLimits；SQLite 命令重传和内容冲突断言 | partial：主要身份、枚举、计数、JSON 与字节边界通过；HTTP body/path 身份核对及完整字段类型矩阵待补齐 |
| C22 | TestCommandConfirmationStateDecisionTable、TestJobCommandStateDecisionTableAndStableConfirmations、TestJobCommandRollsBackStateEventsAndConfirmationTogether、TestCancelCommandAndCompletionHaveOneDurableWinner；jobhost 控制、RunExecutor 与 PerformanceExecutor Stop/Cancel 测试 | partial：存储决策、原确认、回滚、两个连接竞争及三类真实 Core 的 Mock HTTP 控制通过，Performance 包含四流量阶段和 queued 取消；管理 HTTP 和离线待送达尚待实现 |
| C23 | TestPolicyExplicitResourceBoundaries、TestConcurrencyBudgetUsesCorePhasePeaksAndWholeComparison、TestSharedConcurrencyBudgetQueuesAcrossActiveJobSlots、TestHostQueueCapacityExpiryAndRetryDoNotSendExtraRequests | partial：策略边界、各类型峰值和队列实测通过；真实 Core/Guard 集成与完整类型组合仍待验证 |
| C24 | jobhost/host_test.go、fault_test.go、run_executor_test.go、run_executor_settlement_test.go、run_executor_fault_test.go、performance_executor_test.go、performance_executor_fault_test.go；runs/host_lifecycle_test.go；SQLite TestRecoveryJobs*、TestLookupJob*、TestProgressEvent* | partial：真实 SQLite 与 Mock HTTP 验证三类 Core 的请求、结果、报告、提前停止、可靠故障通知、存储阻塞下隔离、确认丢失及无请求恢复；asset/disk Guard、独立进程与真实节点闭环仍待完成 |
| C25 | quicktest/checkpoint_test.go、checkpoint_validation_test.go 与 TestPerformanceHostStopSendingDoesNotAdvanceToLaterPhases | partial：阶段和容量边界顺序、回调失败后的 HTTP 计数、快照隔离、32 MiB 与超限、身份/阶段/配置/阶梯索引及取消后存储时限已通过；完整事实校验覆盖四种流量阶段的停止/取消、零请求/在途观察、open-loop 归档、失败容量阶梯的最高通过投影及篡改拒绝；全部阶段的持久闭环仍见 C26 |
| C26 | SQLite repository_performance_jobs_test.go；quicktest/checkpoint_transition_test.go；jobhost/performance_executor_test.go、performance_executor_fault_test.go | partial：真实 Core 的阶段观察经过 SQLite 保存、CAS、小型事件、两仓库重复/冲突、六组写入/事件故障回滚、原始结果字节摘要、迟到观察及封口后拒绝均通过；完整 JobHost Performance 全阶段、爬坡和部分容量故障恢复及无重发已验证；HTTP/桌面下载待完成 |
| C27 | TestPerformanceObservationMetadataAndFrozenOwnerBoundaries、TestPerformanceObservationsPersistRealCoreFactsWithIdempotentCAS、TestPerformanceObservationCorruptTimeIsRejectedWithoutMutationOnEveryPath；quicktest transition tests | partial：当前 metadata 与主要身份/计数/时间分区、冻结输入、完整快照拷贝和阶段事实不可改写通过；最大完整 wrapper 的实际字节测试与 HTTP decoder 待补齐 |
| C28 | TestPerformanceResultObservationStateAndTerminalBoundaries、TestPerformanceResultRecoveryPreservesActualObservationAndDoesNotExecute、TestPerformanceLateObservationRespectsTerminalWinnerAndResultSeal、TestPerformanceResultDoesNotClaimUnstartedWhenStartJournalIsMissing；PerformanceExecutor control/fault tests | partial：无启动、开始后无观察、四阶段中断、已完成未返回和最终返回、权威取消、重复封口、错误组合及丢失开始日志已通过；实际 Core 的 owner Close 与 deadline 保留权威原因；独立进程 kill/重启待验收 |
| C29 | TestPerformanceResultReferenceByteAndIdentityBounds、TestPerformanceObservationAndResultReadsRejectCorruption、SQLite recovery tests | partial：reference 尺寸边界、错误摘要/身份、原始 canonical bytes 与 SHA 一致、同 ID 重试不改时间或事件已通过；最大完整结果实体、传输中断与客户端校验待完成 |

自有 envelope 字段采用精确大小写并拒绝重复字段，避免 Go 默认大小写不敏感匹配绕过集合限制。领域快照及原生请求 JSON 继续使用其当前 decoder；不能据此声称 R11/R12 的所有边界已经验收。

## HTTP 管理边界实施清单

规则来源为本仓库当前实施方案，2026-10-16 定义；这是项目自有管理接口，不是提供商 API。以下行在对应实现和断言完成前保持 missing 或 partial，Mock HTTP 执行不代替真实独立进程验收。

| 行 | 结构、默认与约束 | 必需分区及状态断言 | 当前证据 |
| --- | --- | --- | --- |
| H01 | 每请求一个 Authorization: Bearer 节点 token；先鉴权再解码；不接受 query token | 缺失、错误、重复、错 scheme、超长拒绝 401；正确 token 允许多个客户端；拒绝不得触发执行或泄露输入 | partial：TestHTTPNodeAuthorizationAndContractAreCheckedBeforeSubmission、TestHTTPClientsWithDifferentBuildsShareOneNodeQueueAndAuthorization；两客户端使用独立 HTTP transport，共享授权与资源；独立进程验收待 R15 |
| H02 | X-LLM-Test-Contract 必填且恰为当前 1；客户端发布版本和构建不参与匹配 | 缺失、重复、非当前、非规范值返回 426；同契约不同客户端版本可访问；不探测历史 decoder | covered：上述鉴权测试与 TestHTTPSharedNodeMetadataDoesNotRequireMatchingClientVersions；只校验唯一当前合同 |
| H03 | GET /runner 返回安装/节点身份、实际构建、runner 版本、平台、当前能力、预算与状态 | 身份来自宿主，不能由客户端覆盖；元数据副本隔离；缺任务能力拒收；重连不修改版本、token 或策略 | partial：TestRunnerInfoValidatesIdentityCapabilitiesAndExactBudgetBoundaries、TestHTTPSharedNodeMetadataDoesNotRequireMatchingClientVersions、TestHTTPRejectsMissingTaskCapabilitiesBeforeAdmission、TestRunnerInfoDecodeRequiresEveryCurrentFieldWithoutDefaults；已遍历必填字段及嵌套对象的缺失/null/大小写，重复/未知/字节边界亦拒绝；全部字段类型和各 Run 能力组合仍待补齐 |
| H04 | POST /jobs 使用当前 Submission，Content-Type 为 application/json，可带 charset=utf-8 | 有效首次 202、同内容 200；错类型 415；坏 JSON/组合拒绝；超过 8 MiB 返回 413；客户端退出不撤销已接收 Job | partial：TestHTTPSubmissionContentAndByteBoundariesDoNotExecuteRejectedRequests、TestHTTPSubmitIsIdempotentAndIndependentOfSubmissionConnection、TestHTTPFailedReceiptWriteDoesNotAbandonOrDuplicateAcceptedExecution；精确上限、响应写失败、秘密冲突均验证；三类任务全部 HTTP 输入分区仍待补齐 |
| H05 | GET /jobs/{id} 只读取当前节点 installation 与共享 node_id 的事实 | 有效、未知、错误 UUID、其他 owner；节点升级后的同当前格式历史构建仍可读，不以当前 build 拒绝旧执行事实 | partial：TestHTTPOwnerChecksAllowPreviousRunnerBuildButExcludeOtherInstallations；当前只返回根状态，完整进度、子事实与报告引用投影仍待实现 |
| H06 | events 必填 after_sequence 与 limit，limit 1..200，wait_ms 可省略为 0，范围 0..20000 | 0/1/200/201、负数、重复/未知参数、未来序号；空页长轮询到期仍 200，取消只停止观察，不取消执行 | partial：TestHTTPEventsRejectInvalidQueriesAndAllowEmptyLongPoll、TestHTTPEventObservationCancellationDoesNotChangeSettledJob；上限等待和活动任务观察中断尚待验证 |
| H07 | stop-sending/cancel 的 body 为当前 Command，path job_id 与 kind 必须对应 | path/body 不符不写命令；4 KiB 边界；首次确认与原命令重传；终态、revision 竞争仍由同一 Host 决策 | partial：TestHTTPControlsConfirmDurableCommandsAndDoNotRepeatRequests、TestHTTPCommandPathAndByteRejectionsLeaveJobAndJournalUnchanged；不匹配与 4 KiB+1 不改状态/命令记录，精确 4 KiB 接受；HTTP 并发 revision 竞争仍待补齐 |
| H08 | Performance 完整结果下载使用存储的 canonical bytes，报告 ID 必须属于该 Job | 404 未就绪/未知，跨 Job 不泄露；SHA/size 与事件引用一致；支持完整和 Range 下载；不重编码或重跑请求 | partial：TestHTTPPerformanceDownloadUsesOriginalCanonicalBytesAndRanges、TestHTTPRejectsCorruptReportBytesWithoutRepairOrProviderTraffic；原始字节、摘要、四类合法/非法 Range 与损坏拒绝通过；跨两个实际 Job、未就绪与传输中断续传仍待补齐 |
| H09 | GET /jobs 按 accepted_sequence 升序分页；limit 必填 1..200；after 可省略为 0，有值时须为规范非负 int64；next_cursor 为十进制字符串 | 空页、1/200/201、错误类型与重复参数；limit+1 不推进未返回项；相同时间和其他 owner 造成的序列空洞不乱序；末页后新接收仍可发现 | partial：TestHTTPJobListingUsesStableCursorAndRejectsMalformedPageQueries、TestJobListKeysetKeepsAcceptanceOrderAcrossOwnerGapsAndNewAdmissions；201 条本 owner 与两个其他 owner、上限、非法查询和末页后新增通过；取消、损坏及两个客户端同时追加的分页仍待补齐 |
| H10 | Run/Comparison 子报告必须属于同一 Job；唯一当前格式，原始字节最多 32 MiB；同事务校验冻结输入、存储事实、seal 与唯一报告日志 | 完整下载字节和摘要匹配事件；其他实际 Job 拒绝；报告日志损坏明确失败且不修复；不再次执行上游请求 | partial：TestHTTPRunAndComparisonReportsMatchJournalAndOriginalStoredBytes、TestOwnedRunFactsAndReportsCannotCommitWithoutTheirJournal；完整 Run/Comparison 报告与跨实际 Run Job 拒绝、损坏摘要保留通过；Run 专项 Range/中断续传及大报告上限仍待补齐 |
| H11 | POST /runner/maintenance 为当前 contract、UUID operation_id 和 pause/resume；4 KiB 上限；暂停与接收使用同一 Host 门，已接收重试仍能确认；仅同一当前操作可恢复 | 当前动作、非法形状/未知字段/大小写/重复键/错误版本/UUID、精确上限与超限；不同操作争用；接收竞争、取消后报告收尾；持久性不明时隔离 | partial：MaintenanceCommandCurrentFieldsAndByteLimits、MaintenanceOwnerAndLostConfirmationDoNotRepeatWrites、MaintenancePauseIncludesAnAdmissionAlreadyHoldingTheHostGate、MaintenancePendingIncludesCancelledQueueSettlement、VisibleMaintenanceWriteWithoutDurabilityIsNotAcknowledged、HTTPMaintenanceBlocksNewWorkAndAllowsAlreadyAcceptedWorkToFinish 通过；真实安装器持锁升级与历史操作日志仍待 C/R15 |

### 性能文档传输与镜像边界

| 行 | 当前规则与分区 | 验证与剩余边界 |
| --- | --- | --- |
| H12 | `GET /jobs/{job_id}/archives/{report_id}` 仅返回对应 Performance 的统计归档原字节；无查询参数，UUID 与节点归属必须成立；未启动、仅预热/爬坡无统计归档时返回 404；`/reports/{report_id}` 对 Performance 继续返回独立的完整执行结果，二者不互相回退 | 正常测量、预热/爬坡后测量与原字节 Range 续传由 TestRemotePerformanceDocumentsDownloadImportAndReadOffline 验证；queued 取消由 TestQueuedPerformanceResultHasNoStatisticalArchive 验证；跨两个实际任务、错误身份/UUID/query、文档/摘要/类型/模型错配由 TestHTTPPerformanceArchiveRejectsWrongOwnerQueriesAndCorruption 验证；尚无物理 SSH 全链路 |
| H13 | 统计归档资格复用唯一 `PerformanceResult.StatisticalArchive`，必须有有效 measured 或 capacity 样本；允许失败/取消的有效测量，不能以 completed 代替已测量证明；只使用封存时刻和归档标记，不重算指标 | 真实 Core 的基本测量、预热/爬坡和 queued 取消经管理 HTTP 验证；既有 JobHost 中断、取消、容量与收尾故障回归继续验证共同投影，实机提供商待 R16 |
| H14 | 完整结果与归档采用独立引用和导入标记。先校验持久引用的原始摘要/大小，再解码当前格式；完整结果绑定真实 starting 事实、紧邻结果事件的前序快照、封存时间和最后 checkpoint；归档必须等于该结果的统计投影 | 结构合法且摘要同步改写的启动/封存时间/checkpoint 伪造仍拒绝；报告标记写入故障回滚归档与列表；重复导入、修改字节、关闭 HTTP 后重新打开第二个数据库离线读取通过；未启动不生成空归档 |
| H15 | 大型性能文档在 typed 解码前限制 4194304 个自有集合成员、每数组 131072 项和 128 层自有嵌套，继续应用 32 MiB 统计归档及完整结果字节预算；provider RawMessage 内容保持 opaque | 实际 DTO 宽度的 10000 samples 与 20000 sparse time_slices JSON 在 32 MiB 内通过扫描，超大稀疏数组在损坏尾部前拒绝；该压力用例验证资源预算，不声称它是完整有效的性能测量报告 |
| H16 | 文件缓存只接受规范内容摘要、当前报告身份/种类及字节预算；Unix 根目录 0700、文件 0600，Windows 依赖用户目录 ACL；保存目录句柄取得的身份，拒绝可观察到的目录替换及文件/锁 symlink | TestCacheBoundsWriterAndRejectsChangedRootAndSymlinks 验证目录替换、io.Copy 不能绕过字节限制、三个链接目标不变；Windows 链接子项因权限跳过，WSL 全部通过；不声称抵御同用户恶意 TOCTOU 或证明任意 Windows ACL |
| H17 | 按摘要跨实例 OS 加锁；未完成文件保持 partial，完整校验后同步文件、原子发布并确认目录，再返回原字节；失败不返回成功或重置损坏数据 | TestCacheResumesPartialAcrossInstancesAndReusesCompleteDocumentOffline、TestPublicationFailureDoesNotAcknowledgeAndRetryUsesVerifiedBytes、TestIndependentCachesSerializeDownloadsAndCancellationOnlyStopsWaiting、TestCorruptDocumentsAndPrefixesArePreservedWithoutReset 覆盖断点、发布前后错误、离线命中、并发等待取消和摘要损坏；采用两套缓存对象和真实文件锁，不是两个桌面进程 |
| H18 | 每轮同步先读本地来源、GET Probe 核对实际 node_id/installation_id，再从持久游标最多读 20 页，每页最多 200 事件，总 context 为 5 分钟；空页 has_more=true 拒绝，不推进游标；MoreEvents 只表示本轮页面尾，不代表执行收尾完成 | TestPassBoundsPaginationAndResumesCommittedCursor 使用有界源端口验证两轮连续游标、来源切换、错安装拒绝和坏空页；三类真实 Core/SQLite/HTTP 同步在 TestSynchronizationPassImportsAllKindsAndReplaysCachedDocuments 通过；后台任务发现和调度仍待实现 |
| H19 | 缓存持久返回后才登记文档导入，完整性能结果先于归档；重复同步核对完整缓存与数据库，断网或观察者取消不向远端发控制命令或重新提交 | TestSynchronizationResumesTruncatedArchiveAfterDatabaseAndCacheReopen 截断归档响应到 17 字节，确认完整结果先导入而归档 pending；关闭数据库并重开后以 Range=17 续传，完整结果不再下载，原指标及缓存离线可读；节点断开仍保留 completed，Mock 提供商请求计数不增加 |
| H20 | 最多 64 个节点观察者，旧观察者退出前仍计入上限；每节点发现页最多 25 个 Job，每次连接处理至多四项，完整同步全局最多四份；单次连接两分钟，默认五秒轮询、十秒刷新配置 | TestWatcherBoundsConcurrentFullSyncAndCancelsGateWaiters、TestWatcherIndependentNodesRevisionCancellationAndShutdown 覆盖慢节点隔离、配置 revision 取消、删除与关闭等待；TestWatcherContinuesPendingPageAcrossFailuresAndWrapsAfterTail 使用资源 fixture 验证坏任务不阻塞后续任务、保留页内位置及尾部重扫；不是领域日志有效性证明 |
| H21 | 只读后台发现从零重扫共享队列，但事件从持久游标续读；相同序号的根快照必须一致，未导入文档继续补齐；关闭观察者不提交、不停止或取消节点任务 | TestBackgroundWatcherDiscoversSharedJobsAndReopensWithoutControllingExecution 使用实际三类 Core、HTTP、SQLite 验证其他客户端任务、观察者和数据库关闭后继续执行、重开导入及后续新增任务；TestBackgroundWatcherRevisitsAnExistingJobForLateReports 验证同一观察者补齐迟到报告；SSH 与实际桌面进程验收仍待进行 |
| H22 | 配置读取超时或无效时公布固定同步错误并暂停旧观察，下一轮恢复有效当前配置后继续；不支持格式仍显式拒绝且保留原字节 | TestWatcherRetriesSourceTimeoutAndSuspendsOldObserversUntilRecovery、TestManagerMonitorResumesAfterCurrentConfigurationReadFailure 覆盖循环存活、连接释放、有效配置恢复和不支持配置不变；没有历史格式读取或自动迁移 |
| H23 | 桌面同步只打开已启用、已固定主机密钥且安装操作已解决的绑定；SSH 与节点 token 租约使用同一配置 revision，核对 node/install 后重连；退出先等待同步，再关闭节点管理和数据库 | TestProductionSynchronizationReopensSavedAuthorizationAndJoinsBeforeDatabaseClose 使用临时配置、MemoryStore 和连接端口验证重开及资源顺序；TestProductionRunnerOpeningRejectsChangedIdentityBeforeSSH 验证错来源先拒绝；没有真实 OS Store、SSH 或提供商行为证明 |

生产 runner 声明 `performance_archives` 能力。客户端连接仍不比较构建号或自动覆盖 runner；后续 Coordinator 对需要该能力的性能闭环在提交前核对能力，不用缺失的统计归档替代完整结果。完整任务自动同步、媒体文件与回收接口仍按实施方案保留待实现，不能用以上基础路由代替完整管理接口验收。

执行记录：2026-10-16，本地 `go test -p 1 ./internal/adapters/runnerhttp ./internal/persistence/sqlite ./internal/jobcontract ./internal/application/jobhost ./apps/desktop` 及管理接口、SQLite、契约、宿主的 `go vet` 通过。管理接口 `TestHTTP` 同时通过 Ubuntu 24.04 WSL 的 Linux 测试二进制验证。接口测试使用真实 JobHost、Core、临时 SQLite 和本地 Mock HTTP，不消耗提供商额度；还没有生产 listener、SSH 隧道或 systemd 进程。

## 独立 Linux 进程边界

| 行 | 当前规则 | 执行证据与剩余边界 |
| --- | --- | --- |
| P01 | 状态目录必须为绝对规范路径、当前用户所有且其他用户不可读写；私有文件不跟随符号链接；运行锁为独立 flock，退出后保留 inode | partial：runnerstate 的两个进程互斥、强杀释放、stale 文件复用、身份不覆盖、权限和 symlink 拒绝通过 Linux 测试；服务器安装锁和安装目录身份仍待 C 阶段 |
| P02 | runner.json 只接受当前 contract、全部必填且精确大小写的字段、唯一键、规范 base64 承诺密钥和 16 KiB 上限；保存节点级 token 的 SHA-256，不保存渠道凭据 | partial：runnerconfig 的当前格式、身份/策略/存储边界、未知/缺失/重复/null/超限与不支持 contract 拒绝；CLI stdin 初始化重试不改原身份，不支持配置不创建数据库；真实节点凭据接入与共享配对仍待实施 |
| P03 | 独立进程从自己的配置与 SQLite 装配实际 Core、Host、HTTP；只监听 127.0.0.1，报告实际运行 inode 的 SHA-256，发布版本不参与客户端匹配 | partial：TestIndependentRunnerPublishesActualBuildAndLoopbackMetadata、TestIndependentRunnerRecoversKilledOwnerWithoutRepeatingPaidWork；强杀后同身份重启只收尾，保留部分观察与 runner_interrupted，Mock 上游请求仍为 1；尚未部署物理节点 |
| P04 | READY 在配置、存储、恢复和 listener 就绪后发送；每 5 秒验证磁盘和 SQLite writer 再发 WATCHDOG；4 秒探针超时跳过本轮，明确错误隔离 Host 并退出失败 | partial：TestNotifyReadinessWatchdogAndDiskBudgetFailure 使用实际子进程与 Unix datagram，验证 READY/WATCHDOG 和超预算退出；TestRunnerWriteProbeRejectsReadonlyStorageWithoutChangingTasks 验证只读失败且事实不变；零行写探针不证明脏页落盘，实际 systemd 重启和 watchdog 超时仍待 R4 |
| P05 | 默认状态预算 20 GiB，保留 512 MiB 可用空间，每接收 Job 预留 128 MiB；配置允许显式调整；计入既有文件和其他 reservation，结束只释放一次 | partial：TestDiskAdmissionReservesHeadroomAndReleasesExactlyOnce 验证大小、并行预留、重复释放、超预算与取消；这是接收与周期检查，不是文件系统硬配额。assets 能力尚未声明，文件输入验证与绑定仍待补齐 |
| P06 | 私有维护记录必须显式包含 paused，绑定当前安装；同步文件及目录后才确认；正常重启保留暂停；暂停不降低 Healthy，不停止 watchdog；未知格式先于数据库访问拒绝 | partial：TestMaintenancePersistsAcrossRunnerRestartAndKeepsWatchdogHealthy、TestUnsupportedMaintenanceFormatIsRejectedBeforeDatabaseAccess、TestMutablePrivateControlReplacementRejectsLinksAndRetainsRejectedData 通过 WSL；真实物理节点 unit 升级仍待 C/R15 |

`deploy/runner/llm-test-runner.service` 提供专用用户、私有状态目录、只读程序区域、30 秒 watchdog、重启限速与资源上限。2026-10-16 已在用户 amd64 节点通过产品 installer 安装并启用，验证专用身份、实际执行摘要、双客户端不重启复用，以及 SIGKILL 后自动恢复和 watchdog 推进；完整 R2/R15 的 arm64、第二节点、并发安装与显式升级仍未完成。

## SSH 传输边界

| 行 | 当前规则 | 执行证据与剩余边界 |
| --- | --- | --- |
| S01 | 首次观察公钥返回 key type 与 SHA-256，认证前停止；已固定的密钥类型和指纹必须一致，不跳过校验；RSA 只协商 SHA-2 签名 | partial：TestHostPinIsConfirmedBeforeAnyAuthenticationAndChangesAreBlocked、TestSSHEndpointRejectsURLsCommandsAndMalformedPins 通过；真实节点固定 Ed25519 指纹连接通过；桌面首次确认与配置持久化仍待 C/D |
| S02 | 密码、未加密或加密私钥来自内存 Lease，格式化脱敏且拒绝 JSON 序列化；握手结束后不依赖请求 context 或 lease | partial：TestSSHAuthenticationIsRedactedAndWrongSecretsFailSafely、TestSSHPrivateKeyAuthenticationSupportsPlainAndEncryptedKeys 通过；专用 CredentialPurpose 与 OS Store 节点管理接入待 C |
| S03 | HTTP 仅通过 SSH 到指定 127.0.0.1 端口，无代理、不跟随 redirect；请求、握手和通道建立有界；悬挂通道建立取消时关闭并回收父连接 | partial：TestSSHTunnelUsesOnlyRunnerLoopbackAndDoesNotFollowRedirects、TestSSHContextCancelsStalledHandshakeAndCommand、TestRunnerChannelOpenCancellationClosesAnOtherwiseResponsiveSSHPeer 通过；已验证 keepalive 仍响应但 channel-open 不回应的取消；runner 实际任务与桌面隧道断线恢复待 R3/R7 |
| S04 | 每 15 秒 keepalive，5 秒无确认关闭连接；SSH failure reply 仍表明连接存活；固定只读 probe 的输出最多 64 KiB | partial：TestSSHKeepaliveAcceptsFailureRepliesAndClosesAnUnresponsivePeer、TestFixedCommandOutputCannotBypassItsByteBudgetThroughIOCopy 通过；测试用短 ticker 执行同一 owner；真实长时间网络故障与桌面状态更新待 C/D |
| S05 | SFTP 上传最多 256 MiB，在私有临时目录中只创建新目标；核对长度、本地摘要、文件 fsync 及独立回读摘要；拒绝覆盖、symlink 父路径和非私有配对文件 | partial：Linux 的 TestSSHPrivateReadRejectsPublicFilesAndMissingFsyncFailsExplicitly、真实节点 TestLiveSSHProbeAndVerifiedSFTPTransfer 通过；物理节点验证 184320 字节、错摘要拒绝、不覆盖与回读，精确测试路径已清理；发布目录持久化、安装锁、操作日志和 current 激活由后续 installer 完成 |
| S06 | SSH provision 仅接受已核验平台的产物，固定命令及 stdin 请求；输出有界并校验操作身份；网络确认不明可重查操作记录 | partial：TestProvisionOutputHandlesFragmentsAndRejectsUntrustedControlData、TestProvisionStopsBeforeExecutionWhenSFTPLacksDurableUpload、真实 TestLiveProvisionSharedRunner 通过；19.5 MB 产物经 512 KiB 缓冲和 16 请求上限上传与回读，实例安装确认可重查；物理节点安装期间断网后的全流程重连仍待验证 |
| S07 | 配对只读 root 私有身份/令牌并核对摘要；实时元数据必须匹配已配对安装；Close 释放 lease 与连接池，不取消已接收任务 | partial：TestSharedNodePairingValidatesFreshIdentityAndReleasesPools 连续 8 次连接/关闭、换身份、秘密格式化与外部目的地拒绝通过；物理双连接共用节点、兼容产物不覆盖且不重启通过；桌面 OS Store 尚待接入 |
| S08 | 断网后暂存目录只按已报告的严格路径和 root 私有已知内容清理；未知文件和权限异常原样保留 | partial：root WSL 的 TestBootstrapCleanupPreservesUnknownContentsAndRejectsPublicFiles 通过；真实首次上传超时目录已核验并清理，无递归删除 |

SFTP API 依据 [pkg/sftp v1.13.11 文档](https://pkg.go.dev/github.com/pkg/sftp@v1.13.11)，SSH 通道实现核对当前引入的 x/crypto v0.54.0 源码，查阅日期 2026-10-16。以上是传输模块验证，不表示自动部署、systemd 或真实提供商 e2e 已验收。
