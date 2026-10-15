# 远程执行当前提交契约与验证矩阵

范围：自有 runner 管理协议的 JobSpec、ExecutionTarget、Submission、asset manifest。规则来源为[远程执行方案](remote-execution-plan.md)，记录日期 2026-10-15；这是管理协议，不定义提供商参数。全部用例为 T0 本地验证，不消耗提供商额度。HTTP 路由、身份鉴权、队列、游标、操作命令和结果投影在 B 阶段补齐，其执行证据当前缺失。

结构清单：Submission 只有 job 与 secrets；job 包含 contract、job_id、node_installation_id、engine_build_id、target、kind、spec、assets；target 包含 kind、node_id；spec 恰有 run、performance、comparison 之一。秘密条目只有 slot 与 value，value 为 JSON base64 字节。asset 条目只有 ref、sha256、size、media_type。run 与 performance 直接复用 A1 至 A3 的准备值；comparison 包含 meta、plan、model、runs，复用 A4a 的整组准备值。

新字段没有历史默认或旧格式回退。assets 必须为数组，没有文件时显式传 []。本地和远程都携带安装身份与精确构建；本地 target 禁止 node_id，remote target 必须携带配置节点 UUID。资产 ref 是 1 至 80 字节的小写字母数字及点、下划线、连字符，首字符须是字母数字；禁止任意文件路径。media_type 是无参数的规范小写 MIME 类型，最多 127 字节。

| 行 | 字段 / 类型 / 存在性 | 默认、有效与无效分区 | 必需场景与边界 | 依赖与结果 | 设计 / 当前执行 |
| --- | --- | --- | --- | --- | --- |
| C01 | job.contract / int / 必填 | 只接受当前 1；缺失、0、2、字符串拒绝 | 缺失、1、非当前、类型错误 | admission 拒绝 unsupported_contract；不解析历史版本 | 已设计 / 见下方证据 |
| C02 | job_id、node_installation_id / string / 必填 | 规范小写 UUID；空、错误长度、非十六进制、大小写变体拒绝 | 每字段有效、缺失、错误 UUID | admission 拒绝 invalid_spec | 已设计 / 见下方证据 |
| C03 | engine_build_id / string / 必填 | 64 字节小写 SHA-256；空、63、64、65、非十六进制、大小写变体 | 长度和字符边界；实际构建相等在宿主核验 | admission 拒绝 invalid_spec 或 build_mismatch | 已设计 / 见下方证据 |
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

自有 envelope 字段采用精确大小写并拒绝重复字段，避免 Go 默认大小写不敏感匹配绕过集合限制。领域快照及原生请求 JSON 继续使用其当前 decoder；不能据此声称 R11/R12 的所有边界已经验收。
