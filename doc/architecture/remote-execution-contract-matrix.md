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
| C13 | TestAssetManifestBoundaries；P1 内容上传见 U01–U13 | partial：manifest 单文件及总字节边界通过，P1 独立上传已验证长度/摘要；非空 Job assets 仍422，任务绑定及实际文件接收验证未接通 |
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
| H09 | GET /jobs 按 accepted_sequence 升序分页；limit 必填 1..200；after 可省略为 0，有值时须为规范非负 int64；next_cursor 为十进制字符串；按将校验的 spec 原始字节选取 1 MiB 软预算前缀，合法大项可独占一页，8 MiB 单项硬限制不变 | 空页、1/200/201、错误类型与重复参数；数量及字节前瞻不推进未返回项；相同时间和其他 owner 造成的序列空洞不乱序；末页后新接收仍可发现；入页项保留完整 row/spec/hash/origin/owner/Snapshot 校验 | partial：既有 HTTP/201 项 owner 空洞分页及 TestJobListSpecBudget* 验证混合大小、精确边界、最大合法单项、硬超限不修改、前瞻损坏在其页拒绝及身份校验；5 秒 HTTP 存储期限不变，弱节点修复后延迟仍待实机验证 |
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
| H18 | 每轮同步先读本地来源、GET Probe 核对实际 node_id/installation_id，再从持久游标最多读 20 页，每页最多 200 事件，总 context 为 5 分钟；空页 has_more=true 拒绝，不推进游标；MoreEvents 只表示本轮页面尾，不代表执行收尾完成 | TestPassBoundsPaginationAndResumesCommittedCursor 使用有界源端口验证两轮连续游标、来源切换、错安装拒绝和坏空页；三类真实 Core/SQLite/HTTP 同步在 TestSynchronizationPassImportsAllKindsAndReplaysCachedDocuments 通过；后台调度另见 H20-H22，长耗时历史下新任务的及时性不由这些测试证明 |
| H19 | 缓存持久返回后才登记文档导入，完整性能结果先于归档；Imported=true 不重复读取缓存或导入。保留首个错误并继续其余独立文档；性能归档只在 sealed result 已导入后处理；context 取消停止后续文档并保留原错误。断网或观察者取消不向远端发控制命令或重新提交 | 既有真实 Core/SQLite/缓存/HTTP 截断与 Range 恢复通过；TestPassImportedDocumentCannotConsumePendingDocumentBudget、TestPassImportsIndependentReportsAfterDocumentFailures、TestPassPerformanceArchiveWaitsForImportedSealedResult、TestPassCancellationStopsLaterDocumentsAndPreservesFirstFailure 验证重复前缀预算、失败隔离、依赖与取消。导入标记只证明数据库文档，不证明外部删除后的证据文件仍在 |
| H20 | 最多 64 节点观察者，旧观察者退出前计入上限；每节点同一 session 两分钟、tick 五秒，tick、metadata 返回与 job 完成各自推进，不等整批 join。node inflight≤4（含等 gate），每 tick 新启动≤4，priority page/inflight≤2、history inflight≤1，tail/history pending 各≤25；全局 4 槽覆盖完整 GET、needsSync 与 Pass，priority SQL 保持 gate 与五秒期限。取消先 join 全部 metadata/job 再 close 一次；正常 deadline 立即重连并保留 scan，不报 remote error；配置 generation 变化则取消/join 后 reset，旧结果不能污染新状态 | a91f1d8 全仓 Go/vet、watcher 独立 review、Windows 与真实 WSL Linux sync10轮/HTTP 全包及最终源码定向测试通过；AppliedAt 仅真实应用 callback 更新，session 首个真实错误保持至下一 session，metadata 成功不清错。r3 实机冷准备239654ms，后续新 foreign 发现4187ms，整轮 false；单样本不是通用25秒 SLA，大 fullsuite/多 client 公平性仍待 |
| H21 | tail admission cursor 单调前进，末页不归零，首次发现 terminal 仍导入；history 独立 wrapping，每轮固定 tail 高水位，持续新增不拖无限尾。ListJobs 只有 page cursor，边界最多有一页 25 项越过高水位；事件仍从持久游标续读，相同序号根快照须一致，known refs 未导入继续补齐，迟到报告由 history 找回。关闭观察者不 Submit/Command，也不停止节点任务 | 三类 Core/HTTP/SQLite 的共享发现、关闭重开、late report 与 partial document 回归通过；新增独立 session/游标/取消 join/错误保留测试，WSL 最终 partial-document 测试通过。r3 产品 watcher 已冷导入12旧Job/全部pending文档并发现新 foreign（submission:null、真实GetJobControls空）；随后 wrapper 失败，未执行 control/C查询/terminal/reopen，r2/r3 false保留；旧 c2 进程 PASS 不代替新共享控制验收 |
| H22 | 配置读取超时或无效时公布固定同步错误并暂停旧观察，下一轮恢复有效当前配置后继续；不支持格式仍显式拒绝且保留原字节 | TestWatcherRetriesSourceTimeoutAndSuspendsOldObserversUntilRecovery、TestManagerMonitorResumesAfterCurrentConfigurationReadFailure 覆盖循环存活、连接释放、有效配置恢复和不支持配置不变；没有历史格式读取或自动迁移 |
| H23 | 桌面同步只打开已启用、已固定主机密钥且安装操作已解决的绑定；SSH 与节点 token 租约使用同一配置 revision，核对 node/install 后重连；退出先等待同步，再关闭节点管理和数据库 | TestProductionSynchronizationReopensSavedAuthorizationAndJoinsBeforeDatabaseClose 使用临时配置、MemoryStore 和连接端口验证重开及资源顺序；TestProductionRunnerOpeningRejectsChangedIdentityBeforeSSH 验证错来源先拒绝；没有真实 OS Store、SSH 或提供商行为证明 |
| H24 | 桌面 submission 只保存冻结 JobSpec、摘要、配置身份、实际 runner 和确认状态；不创建 execution owner，不保存临时凭据；prepared → pending 的 revision CAS 只能有一个胜者，accepted 不回退 | TestSubmissionRecordsReopenAllKindsWithoutCreatingExecutionOwnership 覆盖本地 Run、远程 Performance/Comparison、数据库重开与确认记录；TestSubmissionAdmissionAndCASUseOneFixedDestinationAcrossRepositories 验证两套仓库接收幂等、发送意图竞争、固定目标与 runner 信息；尚未调用真实 Backend |
| H25 | unreachable/not_found/identity_changed/rejected 是 pending 的有限观察，不能生成执行终态、换节点或让任务重新变成 prepared；错误确认、冻结输入变化和落盘失败拒绝且不推进 revision | TestSubmissionRejectsInvalidChangesAndPreservesRowsOnWriteFailure 验证固定来源、原时间、摘要、任意错误文本、revision 与 receipt 身份；注入 UPDATE 失败后完整行保持 prepared；发送与查询仍待 Coordinator 实施 |
| H26 | 待确认列表按 job_id keyset 分页，最多 25 条及 16 MiB 原始记录文档；Get/List 在 SQL 投影中先限制单文档大小，精确检查列身份、当前外层字段、重复、大小写和 null；拒绝数据不修复 | TestSubmissionPageBoundsCompleteFrozenDocumentsByBytes 使用六份各含 3 MiB 原生 body 的有效冻结输入验证五条后按字节分页且尾项可续读；TestSubmissionDecoderRejectsDuplicateAliasesAndHugeDocumentsWithoutRewriting 覆盖重复准备/spec/receipt、别名、null、9 MiB 数据及 owner 列损坏；分页和读取不修改拒绝字节 |
| H27 | 当前 Spec/Submission/Freeze 在 typed 解码前共用 owned JSON 扫描：32768 个集合成员、每数组 131072 项、128 层；保留现有精确 assets/secrets/comparison.runs 上限，原生 RawMessage 叶子继续 opaque，超资源预算返回 submission_too_large | TestSpecOwnedCollectionsRejectBeforeMalformedTailAndNativeBodiesRemainOpaque 验证 entries/case_definitions/assertions/case_inputs 在损坏尾部前拒绝、超深拒绝与有效原生大数组；原结果预算测试继续验证事件、Run 报告和大型 Performance 文档；只保留一个当前扫描实现 |
| H28 | Coordinator 必须明确选择 Location，每次 Prepare/Submit fresh probe 当前节点；新提交固定 node/install/实际 inode build，ready 且满足任务能力后才发送；Performance 额外要求 performance_archives；不比较客户端发布/构建，不自动配置升级或回落本地；单操作总 context 最多 30 秒 | TestCoordinatorRejectsMissingPerformanceArchiveCapabilityAndUnavailableRemote 验证缺少归档能力、不支持管理契约、不可达及普通 Run 仍可准备；TestCoordinatorDoesNotSendOnChangedExecutorOrUnconfirmedOutboxCommit 验证构建/节点/安装变化时无 POST、记录仍 prepared |
| H29 | 只有 prepared → pending 的持久 CAS 明确成功才允许 POST；落盘确认丢失不推断自己赢得发送权；同一待确认记录不自动重新提交或换目的地 | TestIndependentCoordinatorsSendOnlyAfterWinningDurableIntent 使用两个协调器、两个 SQLite 仓库和实际 HTTP，验证一胜一冲突且只有一个 POST；发送意图写前失败和提交后丢确认都不发 POST；后者的真实 GET 404 仍为 pending |
| H30 | Reconcile 只查询原节点和 job_id；pending 的 404、身份变化或不兼容不会生成新任务；当前管理契约兼容时，即使查询时 runner 构建变化或暂停接收，也可找回原执行确认，不能覆盖原执行构建 | TestCoordinatorQueriesOriginalAcceptanceAcrossBuildAndMaintenanceChanges 的任务、GET 和 SQLite 均为实际组件，查询时构建/暂停元数据通过 Probe 端口替换验证策略，不代表物理升级；TestCoordinatorPreservesConcurrentConfirmationWhenSubmitReturnsLateError 验证已持久确认不会被晚到网络错误覆盖 |
| H31 | 提交路径接管全部传入 lease，所有出口释放；slot、完整请求字节和新提交前置检查均先于发送意图；接收响应不完整时保留 pending，关闭管理连接不取消节点执行 | TestCoordinatorRejectsSecretSlotErrorsAndClosesEveryLeaseBeforeConnecting 验证错 slot、额外/已关闭 lease、取消调用无新连接或上游请求；TestCoordinatorAutomaticallyReconcilesLostHTTPAcceptanceAfterReopenAndImportsOriginalReports 对 Run/Comparison/Performance 截断已接收的 HTTP 响应，关闭并重开第二数据库，由后台 GET 恢复同 ID 确认并导入全部预期 JSON 文档；只有一次 POST，重查/同步不增加 Mock 渠道请求 |
| H32 | 自动确认扫描只查询有持久发送意图的 pending；prepared 仍推进 keyset 但保持原记录；每页最多四项、读取 5 秒、查询 30 秒，失败继续后页并在尾部回绕；关闭等待全部查询结束 | TestConfirmationWatcherReachesLaterPendingJobsAfterAnUnavailablePage 使用真实 SQLite 的首项 prepared、七项 pending 和首个不可达页面，后页实际 GET 404，未发送原准备；TestConfirmationWatcherBoundsQueriesAndJoinsCanceledCalls 验证四项上限、重复 Run 拒绝和取消 join；无效分页先于连接拒绝 |
| H33 | 生产提交和同步使用同一个保存绑定连接器；禁用仅阻止 SubmissionAccess，ObservationAccess 仍可读；不读取新的服务器配对、不配置升级或回退本地；退出先结束确认和同步，再关闭节点及数据库 | 生产装配测试验证 disabled 节点仍观察、新提交无 SSH、保存授权重开、连接错误不调用 local、当前 404/升级错误映射、自动确认启动和幂等关闭；连接器端口验证不替代真实 SSH、OS 安全存储与 Wails 进程验收 |
| H34 | LocalBackend 借用应用 Host；Close 关闭管理 handle，不关闭已接收工作；Probe 报告实际本机平台与注入预算，提交与远程使用同一 Core 能力源；查询核对原 target/install/spec，保留原构建，拒绝远程身份和不支持格式 | TestLocalBackendCoordinatesAllKindsAndConnectionCloseKeepsAcceptedWork 使用 Coordinator、实际三类 Core、SQLite 与 Mock 渠道完成原任务和报告；TestLocalBackendUsesTheInjectedConcurrentJobPolicy 验证双活动槽；原确认跨 owner/build 变化重查不再执行，foreign target、closed handle 与不支持 contract 拒绝并清理秘密；尚未替换桌面公开入口 |
| H35 | 桌面数据库访问前必须取得进程生命周期锁；初始化失败和退出均先结束 Host/Core，再关闭数据库、释放锁；本地身份只创建一次，已有任务或提交记录旁身份缺失、损坏均拒绝，不能重建；旧 unmanaged 恢复排除两类 Job 所属 Run，NULL owner 不影响其他 Run | 生产测试通过实际工厂、Core、SQLite 与 Mock 渠道验证第二 owner 在恢复前拒绝、关闭收尾和重开身份稳定、失败后锁可重取；SQLite 测试验证八个初始化调用、重开、类型/大小/UUID/密钥损坏原样保留、取消传播与 NULL owner 回归；生命周期锁和 Linux 实际构建读取通过 WSL，不代表原生 Wails 进程或真实服务器验收 |
| H36 | Launcher 要求一个当前准备种类、唯一对应 payload 和显式 Location；准备总时限一分钟，所有出口清理取得的 lease；沿同一个 Coordinator 准备/提交，pending 保留原 ID，不在启动层重试或更换目的地 | TestLauncherRoutesEveryPreparationThroughOneLocalOwner 验证四种准备输出经实际 Core/SQLite/Mock 封存；失败、取消、远程不可用不产生提交记录；部分 Comparison lease 和 Performance 错误/取消均释放；确认丢失测试保留原 pending ID，GET 找回且只有一次 Submit；生产真实 Preparer 完整 84-case K3 Chat 与三请求性能测试通过，公开绑定仍待替换 |
| H37 | Remote Wails 边界只返回当前展示投影；没有凭据引用、配对材料或安装路径；保存从 Go 状态保留 pin，独立确认只接受新鲜的已观察指纹；默认远程使用配置 ID；方法纳入桌面 lease，移除仍由应用 guard 决定 | TestDesktopRemoteNodeBindingsKeepPinsAndCredentialsInGo 使用实际 Manager/FileStore 和 MemoryStore、SSH 连接器 fixture，验证首次确认、空凭据编辑保留、超限拒绝不改节点、默认位置/移除拒绝和退出；FE 严格解析字段、集合及来源 ID，拒绝未知私有字段和不支持格式，原数据不变；typed client 使用新增七个当前方法，无 fixture 远程成功路径；尚无可见面板或原生验收 |
| H38 | Query 当前入口为 Job/Jobs/PerformanceResult/JobControls；JobRead 在同一只读事务核验 Spec、Location、nullable Submission 及 actual Execution。JobView 必填 nullable submission，承载 delivery_revision/delivery/observation/prepared_at/delivery_updated_at/confirmation；这些旧顶层字段删除。submission:null 只允许 remote 且 actual execution 非 null，不伪造 delivery；own prepared 不可有 execution，own pending 可有实际执行，确认领先 mirror 仍保持原语义；getter 不联网或写 outbox | partial：TestSharedJobViewUsesActualExecutionWithoutInventingSubmission、TestJobReadRejectsUnsupportedOrConflictingFacts、TestSharedJobQueriesExposeRealForeignRunAndComparisonWithoutSubmission、TestSharedJobRejectsConflictingSubmissionAndMirrorWithoutMutation 验证当前形状、来源冲突与拒绝不变；七个 Wails 名称不变，旧顶层 shape 明确拒绝；shared 新契约实机未跑 |
| H39 | 性能进度最多 64 KiB，SQL 先限字节再验证当前 envelope、序号、时间及 typed payload；完整结果就绪仅表示存在本地封口结果或已验证导入，完整读取继续核对来源、字节、SHA 与日志；not_started/interrupted 不替换为统计归档 | TestSubmittedPerformanceQueryPreservesIncompleteAndReturnedObservations 保留三种实际观察；TestSubmittedRemotePerformanceQueryRequiresVerifiedImport 验证引用未下载、标记/文档不一致及同长度有效文档篡改；本地四模式 Core 与远程 HTTP 丢 ACK/重开/离线原报告链路扩展查询断言通过；尚无公开绑定、UI 或物理渠道验收 |
| H40 | ControlInput 仍为 job_id/kind/expected_revision；ControlRecord 必填 Target，冻结 job/location/node/install/specSHA 及必填 nullable acceptance(origin+accepted_at)。foreign 从 actual receipt 冻结且 acceptance 不得 null；prepared 拒绝，own unconfirmed pending 允许 null 并永久保留该命令原 null，不因后来接收而改写或转为 foreign。重传沿原 commandID/revision/Target，连接只核物理 node/install 和当前协议能力，不比较历史 executor 与 daemon build 相等 | partial：TestForeignControlOfflineReplayUsesFrozenTargetAndOriginalCommand、TestControlTargetFreezesAcceptanceAndKeepsPendingUnknownAcceptance、TestControlRejectsReplacementInstallationAndChangedConfirmationOrigin 及原 HTTP 丢 ACK/离线重开测试通过；已知 R4 执行来源与 R7 daemon 合法，新 shared 实机控制仍待验收 |
| H41 | 当前控制文档 schema1，Target 上限 2048 字节；Create 在同 write TX 重读真实 source、核对 proposal Target Equal、创建/复用不可变 anchor 并写 outbox。每 command anchor 恰有 Submission 或 Mirror 真实 FK，outbox 以(command_id,job_id)引用 anchor；Create/replay/CAS/Get/页读均核 Target+anchor+当前 source。同 ID 同内容返回原记录，冲突拒绝；保存过的 null acceptance 不自动提升，原 Target/Command/创建时间不可变 | partial：TestSharedControlFreezesForeignTargetAndRetainsRealMirrorFK、TestSharedControlCreationRevalidatesSourcesAndRollsBackAnchor、TestSharedControlsKeepEarlierUnknownAcceptanceAndFreezeLaterCommands、TestSharedControlRejectsMissingOrChangedAnchorWithoutMutation 与原 outbox CAS/重开/坏文档测试通过；旧缺 Target 文档明确拒绝，无兼容 reader 或自动重建 command |

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
| S08 | 创建远端暂存目录前先持久保存严格路径与操作归属，确认丢失仍可查询原意图；只清理 root 私有已知内容，未知文件和权限异常原样保留 | partial：bootstrap ownership 持久化、确认丢失与重开回归及 root WSL 私有文件/未知内容保护通过；真实首次上传超时目录已核验并清理，无递归删除；完整安装中断重连仍待实机验证 |
| S09 | HTTP response 观察到纯 io.EOF 后自动关闭底层、取消请求并回收 owner，后续尾部探针仍返回 EOF；显式 Close 与非 EOF 断流不变为成功，不持元数据锁跨实际 I/O | partial：TestRunnerResponse* 与 TestRunnerHTTPCurrentReportDownloadsPreserveTailEOF 覆盖两种 EOF、显式/并发关闭、Run/PerformanceArchive 全量和 Range、截断不重试及池复用；exact451 六项 ErrIntegrity RED、a91 六项 GREEN，修复后 Windows client/SSH、WSL root 两包及 main868 全仓 Go/vet 通过。这里只确认尾部探针缺陷，不将 r4 冷失败或 d629 seq400 唯一归因于它；r5 已执行，400秒冷基线仍超时失败 |

SFTP API 依据 [pkg/sftp v1.13.11 文档](https://pkg.go.dev/github.com/pkg/sftp@v1.13.11)，SSH 通道实现核对当前引入的 x/crypto v0.54.0 源码，查阅日期 2026-10-16。以上是传输模块验证，不表示自动部署、systemd 或真实提供商 e2e 已验收。

## JSON 传输字节边界

当前管理 JSON 使用正常 HTTP 内容协商。typed JSON 客户端显式请求 gzip；服务端仅在明确接受 gzip 时压缩，同一当前 JSON 也可使用 identity。此协商不改变管理契约、任务格式、canonical 文档或解码后的摘要，不选择历史实现。Node.Info 和报告下载仍请求 identity；Range 不能与 JSONGzip 同时设置，报告原字节、摘要及续传位置保持一致。

客户端分别限制线上压缩字节和解压后的文档：gzip wire 最多为该操作原字节上限加 64 KiB，解压后仍受原操作上限约束；30 秒请求期限不变。只接收单个 gzip member，CRC、截断、拖尾、重复或未知 Content-Encoding、长度不符和任一预算超限均拒绝。不能用 HTTP transport 的透明解压跳过 wire 校验。

TestJSONGzipPreservesCurrentTypedStateAndFixedErrors、TestReadJSONRejectsInvalidCompressedFramingAndBudgets、TestJSONGzipKeepsTransportErrorsSeparateAndDownloadIdentity、TestReportHTTPRangeStaysIdentityEvenWhenGzipIsRequested 与 TestJSONGzipRequiresExplicitAcceptableCoding 验证 typed/canonical 一致、固定错误、压缩边界、协商与 Range。合成大页面的压缩比测试只证明传输及 envelope，不代替真实领域进度、物理 SSH 或完整桌面验收。长耗时历史下新任务的及时性与本机 outbox 优先调度仍待完善。

## 当前桌面任务与执行来源

| 行 | 当前规则 | 执行证据与剩余边界 |
| --- | --- | --- |
| D01 | LaunchJob、GetJobs、GetJob、GetJobPerformanceResult、ControlJob、GetJobControls、RetryJobControl 是唯一桌面执行/控制路径；Launch 必须显式位置和唯一 payload，接收后确认存储失败仍返回原 pending ID | Go 桌面契约测试、真实 Core/HTTP 的确认读写失败及三次 CAS 竞争验证一次 POST，重开只读确认；旧六个绑定和性能事件移除，不自动重提或降级本地 |
| D02 | Jobs 在同只读 TX 对 Submission/Mirror 做 UNION、UUID 去重/keyset，页大小 1..25；16MiB 软页预算包含 spec/snapshot/sealed result/相关 events/ref，合法大首项可独占前进，各文档硬上限及前瞻校验保留，拒绝不回写。Job/性能进度和结果基于 common JobRead.Spec，不解引用 nullable Submission。GetJobControls 仅本 client 持久投递记录，不伪造 foreign outbox 或他人未投递意图；他人 confirmed 控制事实来自 remote events | partial：TestSharedJobPagesUnionDeduplicatesAndResumesAcrossSources、TestSharedJobPageAdvancesWhenValidSourcesTogetherExceedPageBudget、TestSharedJobPageBudgetsPerformanceResultBeforeProjection 及当前 typed FE/浏览器 fixture 通过；foreign 不显示 delivery，own 仅从 submission 显示，控制确认不覆盖执行状态；native/真实共享控制未验收 |
| D03 | Receipt 的 Origin 为服务端必填 kind、node_id、installation_id、engine_build_id、runner_version、os、arch；与 Spec 的目标前置条件核对，接受后全值不可变；要求 execution_origin 能力，不比较客户端发布版本 | local/remote Host、HTTP、导入、历史控制、报告及温缓存篡改负例通过；Accepted 复用唯一 DecodeSpec 和预算；兼容升级后的已有工作通过 GET 保留历史来源，旧构建 POST 仍拒绝 |
| D04 | Report 和 PerformanceReport 当前形状必须包含 execution_origin；合法 unmanaged 明确 null，managed 报告必须与 owner/mirror 同源；checkpoint 可 null，有值必须与 terminal 同源，统计归档独立复制 terminal Origin | 生成器、SQLite、typed frontend parser 对合法 null、错误身份/平台/构建、缺失/旧字段和对象隔离测试通过；准备 Environment 不改写成执行平台；没有历史格式适配或运行时转换 |
| D05 | 完整 accepted 文档按全部原字节 SHA256 缓存纯校验结果，最多 128 项；小 receipt、各 Run 身份和 canonical snapshot 摘要合计每项不超过 64 KiB；每读仍核对当前 row、实际 Spec 字节、namespace、position、PlanID 和当前快照，任何字节变化完整重验，错误不缓存 | 84 Case 的结果写入与 230 Case 的报告收尾均验证；两处报告来源校验复用本事务已完整验证的 Run，230 Case 终态至收尾由 3.694s 降至 2.116s；阈值和 DDL 未改，普通及大整数同长篡改、容量、克隆、并发和重开测试通过，未运行 race detector |
| D06 | 节点移除使用本地只读事实；未决提交、未收尾任务、pending control（含 foreign Target）、未导入文档或未知 Evidence/附件下载均拒绝。foreign 控制通过真实 Mirror anchor 保留绑定，不要求伪造 Submission；提交/新控制意图与移除检查串行，Get/Retry 不持该门闩 | SQLite 共享 control anchor/usage 与 Desktop 移除竞争回归通过；TestSharedControlFreezesForeignTargetAndRetainsRealMirrorFK、TestRemoteNodeRemovalWaitsForNewControlIntentToBeSaved 验证 foreign pending 保留与串行。移除不是服务器卸载，新 shared 实机仍待 |

## 共享任务当前存储与执行边界（2026-10-16）

当前主树94f1的SQLite唯一checksum为 `1db91d4ba40e06d3b5d3a8a7730d5f57702d63f3c39cb3e4d3a80b678c64fa68`，version1/name `0001_protocol_runtime`不变，声明对象80；P3基础的历史影响见下方新节。此前626ccdd的shared/foreign阶段由7afb替换为ADC，新增per-command job_control_targets及outbox复合FK，声明对象55→56；当时TEMP三类7afb fixture及766历史源私有副本完成的是直接到ADC的转换，9 controls、104份归档与关系按当时ADC校验，不是本次1db验收。正常runtime只接受唯一current、拒绝旧checksum并保留数据，无双读、自动迁移或缺字段降级；原用户库未写入，真实切换/恢复仍pending，旧工具不能串联充当当前历史升级入口，详见[验证记录](remote-execution-validation.md)。

本轮 51 源码文件在 78581 全仓 Go 测试及 vet 前后摘要相同，均 exit0。上面新共享契约的本地 Core/SQLite/Mock、typed FE 及浏览器 fixture 通过；后续 r2/source519 实机 4278 exit1，A 一次 Launch 已接收，B 未发现该新 foreign Job，未进入控制及 C 查询。75284 纯 GET 仅验证原 Job 已 completed、sealed 结果及原身份一致，未启动 B watcher、导入或查询 live controls，不能把 diagnostic_complete 当 r2 PASS。native=false，原 R7/c2 进程证据仍不能替代新 shared 控制验收。

clean626 历史转换副本已通过两轮生产 DesktopApp 初始化/查询/关闭/重开，61 Run 报告、43 性能报告及 4 comparison details 原 ID 可读，原用户库和转换母库不变；真实切换/恢复仍 pending。quota/content 基础与后续 P1 内容 PUT 已提交；ErrCapacity 只软拒新提交，真实存储故障仍隔离，Job assets/附件/release 与 assets 能力仍未交付，未新增 schema 或部署到 R7。scheduler/SQL 已提交 a91f1d8，H20/H21 保持当前规则；P1 及实际执行证据见下表，实机控制、公平性及其他待办保留。

priority 唯一只读端口为 `ListRemoteSyncPriorityIDs`，替换旧方法且无 alias：纳入 own pending/accepted 且无 mirror、own/foreign 非 terminal 或 known refs 未 imported；排除 own prepared，终态全部 known refs imported 后退出 priority，不称 settled，迟到报告仍靠 history。单 statement 严格校验顶层 UUID 顺序、当前 Snapshot/Receipt、小身份及双源冲突，Go 在 limit+1 个 eligible 后停止；SQL 仍可能 scan/sort 整个节点 scope 的小 header/snapshot，工作非 O(limit)，不宣称全仓 constant memory。完整 spec 不传 Go，实际同步的完整校验不弱化；WAL 双仓库 snapshot 一致性回归与真实 B 私有 clone 零 priority 查询通过，未启动原 B watcher。schema/wire 不变，无新增转换要求，既有 adc 用户库真实切换/恢复仍 pending。

后续 r3/session27088 exit1：两个独立 OS client 一次 Launch/Submit、paid0，实际 daemon 仍 R7；B 空 workspace 的生产 watcher 已完成冷导入和新 Job50620b5e 的 public/typed/SQL foreign 查询。整轮止于 wrapper_operation_failed，commandID 空，control0，未验证终态/封口/重开；清理未完成且原 mock_process_after:null，不能声称优雅关闭或 runner after 状态已证。离线工具复现同 channel status timeout 影响后台 reader 的相符缺陷，但原 raw error 未保存，不能追认为唯一根因。后续 r4 已实际执行并在 B13 冷导入400.022s失败，未创建 A/Mock/Job/control；原 false 保留，完整证据见[验证记录](remote-execution-validation.md)。

## P1 内容上传当前边界（4512709，EOF 修复8683518，非 Job assets）

新增可选 feature `content_upload`，仍使用当前 contract1 和既有 Features，不新增必填 RunnerInfo 字段或要求同 build。`jobcontract.ContentIdentity` 替换原 runnerstate 类型，无 alias。下列为独立内容 PUT 的本地/WSL 证据，不把 U01–U13 的有界 P1 覆盖当成媒体执行完成；细分矩阵 `%TEMP%/llm-content-http-p1-c962-20261016/p1-boundary-matrix.safe.json` SHA `bcd77697dff1ac9725de8d62fec4671251588289ecef2ef889b70b7974bc6344`。

| 行 | 当前规则 | 执行证据与边界 |
| --- | --- | --- |
| U01–U02 | PUT /assets/{lowerSHA64}；Bearer+当前契约先于 reservation/body；规范路径、无 query/别名 | TestAssetUploadValidatesAllMetadataBeforeReadingOrCallingStorage、真实 TCP unread-large-request 拒绝验证401/426/400且不读body/调用store |
| U03–U04 | application/octet-stream、identity，已知 Content-Length 1..1 GiB；拒绝 chunked/未知长度、重复header及错表示 | TestNodeContentUsesExactBinaryWireAndCanonicalRoute、TestAssetRequestSizeBoundaryDoesNotAllocateBody 验证 wire与0/1/max/max+1；没有实际1 GiB传输 |
| U05–U06 | 固定缓冲核验全部输入长度/SHA，短/长/错摘要422；重复仍核验输入与既有文件，原子不覆盖且持久确认后才201/200 | streaming max+1、并发重复、fsync/close故障和 TestAssetUploadReturnsTypedDurableReceiptsAndVerifiesDuplicateBodies 通过；失败partial保留并计费 |
| U07–U08 | 输入错误/取消不隔离，实际存储故障503并隔离；容量429软拒。与Job共享20 GiB/minfree512 MiB reservation，upload槽4、保留完整文件/partial上限16384，保守计费，无GC/delete | source/error分类、磁盘损坏隔离、Job/upload并发预算、槽/文件数和reservation exactly-once回归通过 |
| U09–U10 | upload10分钟、bulk header2分钟，普通JSON30秒与原pool25秒/2分钟不扩大；SSH endpoint owner≤4，每个固定management/bulk两role、transports≤8；关闭取消/join并释放源 | 独立Linux进程131072B TCP31.041710264s、普通JSON并行、阻塞上传SIGTERM join通过；client/SSH完整包及closed-owner Read0/Close1通过，无31秒跨SSH全链或race证据 |
| U11–U12 | receipt精确绑定contract/node/install/hash/size，JSON≤1024B；2xx坏receipt仍拒绝；丢确认只允许显式同内容完整重传，不新建Job | TestAssetReceiptRequiresEveryCurrentFieldAndNoAliases、TestUploadContentStrictlyBindsReceiptAndRejectsUntrustedResponses、lost-ack完整重传测试通过 |
| U13 | serviceMu原子互斥：活跃upload令pause409 upload_in_progress/retryable/noApply，pause先赢则upload423；installer仅严格当前SafeError busy码映射ErrBusy | upload/maintenance生命周期竞争与TestUpgradeMaintenanceClassifiesUploadBusyWithoutConfirmingPause通过，无旧409 fallback |

P1 已提交 main4512709、未部署R7，主a91+P1全仓 session86996 已test/vet exit0，616 Go/module输入前后逐一不变；最终proof记录初冻结Windows全仓与最后两文件client/SSH修复的不同边界。Linux nonroot SSH失败在exact c962旧基线同样复现，最终root完整SSH及其余affected包/vet通过，无fixture或生产权限放宽，不称所有Linux环境均通过。assets仍false、非空Job assets仍422/0Execute；HTTP asset GET、Job binding/refDB、附件、manifest、release及P2–P5闭环尚未交付，schema/DTO/FE/Wails无变更，无新增数据升级。详见[验证记录](remote-execution-validation.md)。

后续 EOF 修复已从 secondary689d58e 整合 main `868351809964a495d8758dbb44ae3d255304e8aa`，不改下载 SHA/长度/额外 EOF 校验、期限、重试或 wire/schema。父级 clean868 全仓 Go test/vet 均0，171.043s，617个Go/module输入前后摘要相同。通用直接 gzip reader 回归与生产 readJSON 的先 wireReadAll、再 Multistream(false) 分开，不据前者推断真实 JSON 同步根因。

r4 保留副本13个镜像齐全，但 d1cce52e 的 performance_result/report 两引用仍 pending，d629 仍 running/rev3/seq400；27次公开/typed离线查询13.006244s全绿且原DB/副本SHA不变，不等于历史文档已全部导入。后续r5 exact868/session82376/run-vvyq5fzo已exit1，400秒cold baseline再次超时，未进入A/Mock/新Job/control；B34176父级清理后实际exit1，进程elapsed400075ms、cleanup_completed=true，BDB56012800字节。初始13个ID存在于cold_import事件，summary空数组不表示服务端没有历史Job；原r5 false保留，不预判其pending/getter或seq400原因。r5终态记录时P2a仅在独立树提交1467e19f，后续主整合见下一节；其他文件闭环与既有待验范围保持。

## P2a 当前文件声明与桌面 selection（cbef518，禁止 fileLaunch）

P2a的49文件已由独立1467e19f整合main cbef518。当前 `Input.type=file` 必须带 `file:{encoding,media_types,max_bytes}`：encoding仅声明image_data_url，media_types为不重复的PNG/JPEG非空子集，max_bytes为1..1 MiB；禁止default/enum/minimum/maximum/unit。FileBinding仅含逻辑asset_ref，不含本地路径、selection或实际内容；Input/SuiteInput/FileBinding严格拒绝未知、重复、错大小写、null字段，Clone隔离可变成员，Suite仍用显式Case绑定。

`testspec.ValidateInputs` 的Go返回值已替换为 `ResolvedInputs{JSONValues,Files}`，受影响调用者已更新，无旧map包装或兼容路径。两个当前Wails绑定为 `CreateLocalFileSelection(document)`、`ReleaseLocalFileSelection(selectionID)`；只接严格data_base64/media_type DTO，不接路径。selection单份1..1 MiB、最多8份/合计8 MiB、DTO最多1400000字节，PNG/JPEG配置维度各不超过4096、像素不超过16777216；会话owner管理独立borrow reader及释放，临时selection不进入draft或Launch。

主树Wails 2.15.0生成、FE757测试/tsc/lint/build和全仓Go/vet已通过；详细输入锁定、既有warnings及证据只列于[验证记录](remote-execution-validation.md)。这不包含文件Job执行、Stage/请求编码/capture/wire metadata或KindPerformance.native_case；无admitted resolver时仍拒绝fileLaunch，assets=false。P3真实admission/pins/resolver与P4 manifest/release未交付；P2a记录时P3 types/ports和16表基础尚在别树，后续主整合见下一节。P2a本身无schema/actualDB/转换变更，也无native/browser/physical/provider验收。

## P3 foundation 与冷查询已主整合（94f1，执行能力仍关闭）

P3首片31文件由secondary40b296e整合main00b1802，冷查询11文件由secondary2193d75整合main94f1。当前仅严格manifest/holding/release types、Ledger ports、空内容接收及16表/8索引基础；Acceptance.ContentClaims必显式非nil，当前只接受[]，非空claims/assets仍拒绝，metadata不构成physical pin。Manager A、真实pins/permit、Core resolver与P2b仍在别树开始，assets/fileLaunch及manifest/release/physicalManager均未开放。

冷查询在同一只读TX捕获有界raw，关闭游标、归还pool1连接后再纯投影，保留同快照revision及完整坏数据拒绝；确定性writer1秒timeout RED与暂停投影writer成功GREEN已通过，预算不变。最终clean94f1全仓Go/vet通过，31个P3 blobs与9个冷查询纯文件无漂移、两merge文件复核；无FE/bindings或现管理协议变化。父级原d629/e001/b285各一次GET只证明completed/rev4/seq469 Snapshot读取和Close/EOF成功，不等于冷同步/报告导入通过；原r5 false不变，该次记录时r6仅TEMP准备。

唯一current schema替换为1db/80，仅P3改变schema；旧ADC正常打开将拒绝且不改数据。固定ADC→1db的TEMP工具仅验证已登记私有fixtures，尚未转换/切换真实用户或服务器DB，也不支持766/7afb等其他历史入口；工具位置与当前证据集中见[验证记录](remote-execution-validation.md)。

后续r6 fixedsource94f1/session80178已实际exit1，run-jgyxsecl止于400秒b_cold_preparation，phase400020ms；B57424清理后实际exit1/400077ms、readerEOF、cleanup_completed=true，未到A/Mock/Launch/control，新Job与command均空、provider0。父级只读副本仅4/13镜像、54events、8条已观测refs全imported/4perfresults，缺余9项，不是冷同步完成；summary初始ID空数组不表示服务端无13历史ID。连接释放机制的确定回归与主Go PASS保持，但不足排除其他不相容/导入失败，根因未定；原false、资产关闭和真实数据切换边界不变，详见[验证记录](remote-execution-validation.md)。
