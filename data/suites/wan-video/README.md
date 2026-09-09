# Wan 3.0 场景 Suite

[English](README.en.md) | [简体中文](README.md)

Wan 3.0 标准版与 Prime 共用当前协议 Case。Suite schema 2 保存引用和输入映射，上游模型与凭据由 Run 绑定。4 个修改模型的 Case 已停用并移出 Suite。每个命名模型场景仍保留以下五类：

| 目录后缀 | Suite key 后缀 | Case 数 | 可直接运行 | 场景 |
|---|---|---:|---|---|
| `-connectivity` | `.connectivity` | 1 | 是 | 最低成本连通性与异步任务闭环 |
| `-basic` | `.basic` | 6 | 是 | 基础生成、比例、音频、Prompt 扩写、水印与必填校验 |
| `-negative` | `.negative` | 35 | 是 | 参数类型、枚举、必填和数值边界拒绝 |
| `-automatic` | `.automatic` | 70 | 是 | 完整自动合同测试，35 正向 + 35 负向 |
| 无后缀 | 无后缀 | 187 | 否 | 完整合同矩阵，包含 117 个禁用素材/环境/oracle 模板 |

层级关系为：连通性 ⊂ 基本功能 ⊂ 完整自动测试 ⊂ 完整矩阵；参数拒绝 Suite 是完整自动测试中的负向子集。

选中 Suite 并开始运行后会直接执行，无需额外付费勾选。`-automatic` 包含高时长成功 Case，不应把它当成低成本烟测；187 Case 的完整矩阵只有在禁用模板逐项满足前置条件并启用后才能转为可运行 Plan。
