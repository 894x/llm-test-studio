# ADR-0002：版本化 Case Type 注册表

> Historical implementation record. The current Case contract and native text protocols are described in [multi-protocol-cases.md](../design/multi-protocol-cases.md); the definition envelope below is superseded.

- 状态：Accepted
- 日期：2026-09-04
- 决策范围：测试用例定义、创建、持久化与执行

## 背景

初版 Test Case 把请求、期望和断言固定在一个通用结构中，并把部分执行选择藏在 `custom` assertion 内。这个结构能表达单请求验证，却不能自然表达输入 token 阶梯、每档预热与采样、缓存模式等由用例自身拥有的调度语义。继续增加特殊字段会让目录、表单和执行器同时出现分支。

当前仍处于初版开发阶段，允许直接替换 v1 case 文档，不保留运行时兼容分支。

## 决策

1. Test Case definition v2 只包含 `schema_version`、`type`、`type_version` 和 `spec`。通用目录层不解释 Spec。
2. 后端 Case Type 注册表是类型能力的权威来源，公开标签、分类、支持协议、调度归属、是否可创建和默认 Spec，并在创建、更新、文件导入及执行前校验。
3. 执行路由按 Case Type 分组，将同一计划中的不同类型交给各自 Driver；Driver 负责解释 Spec 和拥有其内部调度。
4. `request.single@1` 继续使用 Plan 的负载配置；`latency.input_ladder@2` 由 Case 自身拥有 token 阶梯、预热、采样、超时和缓存模式。顶层 `warmups_per_step` 与 `samples_per_step` 提供统一默认值，单个 `stages[]` 可通过 `warmups`、`samples` 进行覆盖。
5. Result 增加稳定字符串 Dimensions。延迟阶梯请求至少记录 `case_type`、`stage`、`stage_index`、`input_tokens_target`、`stage_warmups`、`stage_samples`、`sample` 和 `cache_mode`；数值指标继续记录 TTFT、E2E、实际 prompt/completion tokens 等事实。
6. 前端从目录快照读取 Case Type 描述和默认 Spec。已知类型可提供专用编辑器；未知但可创建的类型仍可通过通用 JSON Spec 编辑器工作。
7. 仓库内置 case 文件整体迁移至 shareable schema v2；导入器拒绝 v1 文档。

## 结果

- 新增普通类型只需注册 Descriptor、Validator 和 Driver；通用文件目录、Wails 命令和通用 JSON 编辑器无需增加字段。
- 需要高质量交互的类型可以增量增加专用前端编辑器，而不会改变持久化契约。
- Case Type 和 Type Version 成为不可变运行语义的一部分，Run 仍通过固定 Test Case revision 保证可复现。
- 不兼容代价是旧 case JSON 和旧 definition 文档不能直接读取；本阶段通过一次性迁移仓库内置文件接受该代价。

## 备选方案

### 继续扩展通用 TestCaseDefinition

不采用。阶梯、对话、多模态和异步任务需要的字段与调度语义不同，通用结构最终会成为大量可选字段和跨层条件分支。

### 使用 custom assertion 选择 Driver

不采用。它把执行类型隐藏在断言配置中，无法在创建前声明能力，也无法让 UI、校验和路由共享同一权威定义。

### 每种类型建立独立持久化结构

不采用。类型专用文件或数据表会让新增 Case Type 必须修改通用持久化契约，并增加跨类型查询和版本迁移成本。
