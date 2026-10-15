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
| H03 | GET /runner 返回安装/节点身份、实际构建、runner 版本、平台、当前能力、预算与状态 | 身份来自宿主，不能由客户端覆盖；元数据副本隔离；缺任务能力拒收；重连不修改版本、token 或策略 | partial：TestRunnerInfoValidatesIdentityCapabilitiesAndExactBudgetBoundaries、TestHTTPSharedNodeMetadataDoesNotRequireMatchingClientVersions、TestHTTPRejectsMissingTaskCapabilitiesBeforeAdmission；全部元数据字段类型和各 Run 能力组合仍待补齐 |
| H04 | POST /jobs 使用当前 Submission，Content-Type 为 application/json，可带 charset=utf-8 | 有效首次 202、同内容 200；错类型 415；坏 JSON/组合拒绝；超过 8 MiB 返回 413；客户端退出不撤销已接收 Job | partial：TestHTTPSubmissionContentAndByteBoundariesDoNotExecuteRejectedRequests、TestHTTPSubmitIsIdempotentAndIndependentOfSubmissionConnection、TestHTTPFailedReceiptWriteDoesNotAbandonOrDuplicateAcceptedExecution；精确上限、响应写失败、秘密冲突均验证；三类任务全部 HTTP 输入分区仍待补齐 |
| H05 | GET /jobs/{id} 只读取当前节点 installation 与共享 node_id 的事实 | 有效、未知、错误 UUID、其他 owner；节点升级后的同当前格式历史构建仍可读，不以当前 build 拒绝旧执行事实 | partial：TestHTTPOwnerChecksAllowPreviousRunnerBuildButExcludeOtherInstallations；当前只返回根状态，完整进度、子事实与报告引用投影仍待实现 |
| H06 | events 必填 after_sequence 与 limit，limit 1..200，wait_ms 可省略为 0，范围 0..20000 | 0/1/200/201、负数、重复/未知参数、未来序号；空页长轮询到期仍 200，取消只停止观察，不取消执行 | partial：TestHTTPEventsRejectInvalidQueriesAndAllowEmptyLongPoll、TestHTTPEventObservationCancellationDoesNotChangeSettledJob；上限等待和活动任务观察中断尚待验证 |
| H07 | stop-sending/cancel 的 body 为当前 Command，path job_id 与 kind 必须对应 | path/body 不符不写命令；4 KiB 边界；首次确认与原命令重传；终态、revision 竞争仍由同一 Host 决策 | partial：TestHTTPControlsConfirmDurableCommandsAndDoNotRepeatRequests、TestHTTPCommandPathAndByteRejectionsLeaveJobAndJournalUnchanged；不匹配与 4 KiB+1 不改状态/命令记录，精确 4 KiB 接受；HTTP 并发 revision 竞争仍待补齐 |
| H08 | Performance 完整结果下载使用存储的 canonical bytes，报告 ID 必须属于该 Job | 404 未就绪/未知，跨 Job 不泄露；SHA/size 与事件引用一致；支持完整和 Range 下载；不重编码或重跑请求 | partial：TestHTTPPerformanceDownloadUsesOriginalCanonicalBytesAndRanges、TestHTTPRejectsCorruptReportBytesWithoutRepairOrProviderTraffic；原始字节、摘要、四类合法/非法 Range 与损坏拒绝通过；跨两个实际 Job、未就绪与传输中断续传仍待补齐 |
| H09 | GET /jobs 按 accepted_sequence 升序分页；limit 必填 1..200；after 可省略为 0，有值时须为规范非负 int64；next_cursor 为十进制字符串 | 空页、1/200/201、错误类型与重复参数；limit+1 不推进未返回项；相同时间和其他 owner 造成的序列空洞不乱序；末页后新接收仍可发现 | partial：TestHTTPJobListingUsesStableCursorAndRejectsMalformedPageQueries、TestJobListKeysetKeepsAcceptanceOrderAcrossOwnerGapsAndNewAdmissions；201 条本 owner 与两个其他 owner、上限、非法查询和末页后新增通过；取消、损坏及两个客户端同时追加的分页仍待补齐 |
| H10 | Run/Comparison 子报告必须属于同一 Job；唯一当前格式，原始字节最多 32 MiB；同事务校验冻结输入、存储事实、seal 与唯一报告日志 | 完整下载字节和摘要匹配事件；其他实际 Job 拒绝；报告日志损坏明确失败且不修复；不再次执行上游请求 | partial：TestHTTPRunAndComparisonReportsMatchJournalAndOriginalStoredBytes、TestOwnedRunFactsAndReportsCannotCommitWithoutTheirJournal；完整 Run/Comparison 报告与跨实际 Run Job 拒绝、损坏摘要保留通过；Run 专项 Range/中断续传及大报告上限仍待补齐 |
| H11 | POST /runner/maintenance 为当前 contract、UUID operation_id 和 pause/resume；4 KiB 上限；暂停与接收使用同一 Host 门，已接收重试仍能确认；仅同一当前操作可恢复 | 当前动作、非法形状/未知字段/大小写/重复键/错误版本/UUID、精确上限与超限；不同操作争用；接收竞争、取消后报告收尾；持久性不明时隔离 | partial：MaintenanceCommandCurrentFieldsAndByteLimits、MaintenanceOwnerAndLostConfirmationDoNotRepeatWrites、MaintenancePauseIncludesAnAdmissionAlreadyHoldingTheHostGate、MaintenancePendingIncludesCancelledQueueSettlement、VisibleMaintenanceWriteWithoutDurabilityIsNotAcknowledged、HTTPMaintenanceBlocksNewWorkAndAllowsAlreadyAcceptedWorkToFinish 通过；真实安装器持锁升级与历史操作日志仍待 C/R15 |

完整任务投影、媒体文件与回收接口仍按实施方案保留待实现，不能用以上基础路由代替完整管理接口验收。

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

`deploy/runner/llm-test-runner.service` 提供专用用户、私有状态目录、只读程序区域、30 秒 watchdog、重启限速与资源上限的模板。模板存在不表示已在真实服务器安装、启用或验证保活。
