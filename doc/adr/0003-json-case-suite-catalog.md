# ADR-0003：Case 与 Suite 采用 JSON 文件目录

- 状态：Accepted
- 日期：2026-09-04
- 决策范围：内置与用户 Test Case、Suite 的定义、发现和编辑

## 背景

Test Case 原本由仓库 JSON 描述，但桌面启动流程曾把内置 Case 导入 SQLite，并进一步在启动时创建内置 Kimi Suite 记录。这产生了两套来源：文件负责作者配置，数据库又成为运行时目录。并发启动时，两个进程还可能同时写入同一个确定性 Suite ID，触发 revision conflict，使工作台无法打开。

用户创建的 Suite 与内置 Suite 本质相同，都是可分享、可审阅的测试配置，不应因为来源不同而采用数据库实体生命周期。

## 决策

1. 内置和用户 Test Case 均使用严格校验的 `case.json`；内置和用户 Suite 均使用严格校验的 `suite.json`。
2. Suite 按目标模型组织。每个 Suite 必须声明稳定 key、协议、精确 `model_target` 和有序 `case_keys`；不同模型使用不同 Suite 文件，即使当前用例集合相同。
3. 内置文件嵌入应用，用户文件放在可执行文件相邻的目录。读取时合并两者；同一协议和 key 的用户文件可覆盖内置文件。
4. 桌面启动只发现并读取 Case/Suite 文件，不创建、更新或 seed SQLite Case/Suite 记录。缺失、重复或无效文件使目录加载明确失败。
5. Wails 的 Case/Suite 创建、更新和删除操作直接原子写入用户 JSON 文件，随后重新读取目录得到 ID 与 revision。
6. Suite 通过 `case_keys` 解析当前 Case 文件。Suite 自身不会为了建立引用而向 SQLite 复制 Test Case snapshot。
7. SQLite 不作为 Case/Suite 作者配置的事实来源。执行期如需保存请求、结果或不可变运行快照，属于运行数据边界，不得反向成为 Case/Suite 的编辑来源。
8. 当前处于初版阶段，不保留旧 SQLite Case/Suite 目录的兼容读取或 ID 迁移。

## 结果

- 内置和用户 Case/Suite 共享一套格式、校验和生命周期，可直接复制、版本控制和打包。
- 启动不再因 Case/Suite seed 的并发写入发生 SQLite revision conflict。
- 每个 Kimi 模型都有独立 Suite 标识，运行和报告可以明确指出被测模型套件。
- 修改 Case 会改变其内容 revision；引用旧 revision 的计划必须重新选择当前配置，或由执行快照机制显式保留历史，而不是静默读取最新内容。
- SQLite 中仍存在的旧 Case/Suite 表和方法只属于待删除的历史持久化实现，生产目录不得调用它们。

## 备选方案

### 启动时把内置 JSON seed 到 SQLite

不采用。它制造双重事实来源、启动写放大和多进程竞争，并让只读配置错误表现为数据库冲突。

### 内置 Suite 用 JSON、用户 Suite 用 SQLite

不采用。同一领域对象出现两套生命周期会让覆盖、导出、导入、校验和故障诊断持续分叉。

### Suite 只保存 Case UUID 与 revision

不采用。文件应保持可读、可移植，并能在不同工作区稳定解析；因此保存协议作用域内的稳定 `case_keys`，运行时再解析为精确引用。
