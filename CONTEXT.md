# LLM Test Studio 领域语言

本仓库只有一个核心业务上下文：以可复现、可比较的方式验证 LLM 接口。以下术语是 GUI、CLI、本地执行与未来远程执行共同使用的领域语言。

## Language

**Model（逻辑模型）**:
用户希望测试和比较的模型身份及能力集合，不等同于任一服务商暴露的模型名称。
_Avoid_: Provider Model、Upstream Model

**Channel（渠道）**:
一条可调用的 LLM 服务通道，描述服务位置、协议、启用状态和凭据引用。
_Avoid_: Endpoint、Provider Account

**Channel Model（渠道模型映射）**:
逻辑模型在特定渠道中对应的上游模型名称。
_Avoid_: Model Alias

**Credential（凭据）**:
调用渠道或外部系统所需的秘密身份材料；领域数据只持有其安全存储引用和脱敏元数据。
_Avoid_: API Key Record、Plaintext Secret

**Test Case（测试用例）**:
一个可版本化的请求、期望和断言集合；历史执行永远引用当时版本。
_Avoid_: Prompt、Request Sample

**Suite（测试套件）**:
一组明确版本的测试用例，可被多个执行计划复用。
_Avoid_: Case Folder、Latest Cases

**Plan（执行计划）**:
可复用的测试意图，指定用例、负载方式、通过阈值，以及可选的模型/渠道候选范围。候选范围留空时，模型和渠道在执行前选择；运行创建后会把目标的精确修订固定到 Run 快照。
_Avoid_: Run Config、Job

**Run（执行）**:
一次计划的实际执行及其不可变输入快照，无论发生在本机还是远程执行器。
_Avoid_: Task、Session

**Result（结果）**:
请求级或用例级的事实记录，分别表达传输、协议、语义与 SLA 是否成功。
_Avoid_: HTTP Success、Response

**Evidence（证据）**:
支撑结果与结论的、可校验且已脱敏的原始材料或材料引用。
_Avoid_: Raw Log、Full Response

**Report（报告）**:
从一次执行及其结果生成的统一结论模型，是 JSON、HTML、PNG 与 PDF 的共同内容来源。
_Avoid_: Export、Dashboard Snapshot

**Integration（集成）**:
与 new-api 等外部管理系统的连接身份和非秘密配置，独立于普通测试渠道。
_Avoid_: Channel、Admin Channel
