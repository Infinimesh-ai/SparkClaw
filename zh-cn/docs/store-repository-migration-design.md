# Store Repository 迁移设计

> 语言：[English](../../docs/store-repository-migration-design.md) | 简体中文

> 状态：S2 pilot 和 S3/S4 设计审查草案，2026-08-19。只有 S0-S1 实现审查
> 和 File 设计得到 `GO` 后才开始 pilot；其余 repository 只有在 S2 实现
> `GO` 后才开始。

## 目标

通过小型实现波次，用经过审查的领域 repository 替换拥有 141 个方法的
`store.Store`。S2 在证明 File 事务模型时迁移一个已接受的低风险 pilot，
S3 迁移所有剩余 repository。每个 repository 同步修改所有调用者和三个后端。
最后一个 repository 完成后，S4 先删除宽接口，再开始 Runtime/Supervisor。

## 迁移单元

一个 repository 是一个实现阶段，通常也是一个行为变更 commit。下面的规划
分组不授权多 repository commit。

每个 repository 阶段包括：

1. 确认 S0 接受的方法和消费者矩阵；
2. 增加 repository 接口和编译期断言；
3. 所有方法改为接收 context 并返回后端失败；
4. 同步更新 Memory、File、PostgreSQL，以及适用时的 File `Snapshot`；
5. 所有调用者传入 request、operation、worker、startup 或 shutdown context；
6. 按需增加共享契约、File 失败、PostgreSQL 分类、timeout、cancellation 和
   race 测试；
7. 删除该 repository 的旧签名；
8. 在开始下一个 repository 前完成实现审查。

完成阶段后不得残留 compatibility adapter、可选类型断言、重复方法、动态
repository map 或基于字符串的 dispatch。

## 稳定操作边界

S2 pilot 引入 package-private 类型化操作边界，并立即由已迁移
方法使用：

- 有限 `OperationID` 标识 repository 和方法；
- operation spec 选择 read、write、transaction 或 startup fallback timeout；
- 调用者更早的 deadline 始终优先；
- 边界保留 domain error 并分类 backend error；
- label 不得包含 owner ID、record ID、query、path 或 content。

S2-S3 期间该边界只负责 deadline 组合和分类，不暴露 health、metrics、repository
lookup 或 Runtime。S5 在增加监管时复用相同 operation ID 和调用点，避免再次
改写所有 repository 方法。

## 规划顺序

精确 repository 名称由 S0 冻结。首选风险顺序为：

1. S0 从 ISCP onboarding、MCP access 等已经较强且范围有限的领域接受一个
   pilot，在 S2 用较少调用者建立 context/error plumbing；
2. Owner、Client、Credential 和 Session；
3. Conversation、Run、Document、Approval、Audit、Evaluation 和 artifact
   metadata；
4. Schedule、Connector、Delivery Record、Passive Notification 和 External
   Chat；
5. Browser State 和 Memory。

一个规划组中同时只能有一个 repository 处于活动状态。Session 删除等跨记录
命令有独立显式事务用例，不能因为接口名称较小就按普通 CRUD 处理。

## 后端规则

### Memory

- 保持当前成功路径的顺序、scope、CAS 和 event 语义；
- 返回 clone 数据，不暴露后端拥有的可变引用；
- mutation 前检查取消；
- record 和必需 lifecycle 变更在同一锁内应用。

### File

- 使用已接受的 [File Store 持久性](store-file-durability-design.md) gate 和
  command 状态机；
- 只有 durable replacement 和 directory sync 完成才返回成功；
- 对确认的提交前失败恢复完整 pre-snapshot；
- submitted replacement 结果不确定时返回 `unknown_outcome`；
- 默认 Go test gate 中通过确定性失败用例。

### PostgreSQL

- 普通 repository 操作不得使用 `context.Background()`；
- 返回所有 `Exec`、`Query`、`QueryRow`、begin、scan、rows、commit 和
  rollback 失败；
- `pgx.ErrNoRows` 只在 lookup 中映射为正常缺失；
- command 与必需 event/audit 在同一 transaction 中写入；
- 不确定 commit 报告 `unknown_outcome` 并要求 reconciliation。

## 临时宽接口

仍有未迁移方法时，宽接口嵌入已完成 repository，只声明剩余 legacy 方法，
不得重复已迁移签名。完成的 repository 只有一条实现路径。

临时接口只是迁移脚手架，不是受支持抽象。新的生产消费者不得接收它。

## 单 Repository 审查门禁

设计确认检查：

- 方法归属和消费者最小依赖；
- command transaction/reconciliation 行；
- 预期行为变更和回滚；
- 聚焦验证命令。

实现 `GO` 要求：

- 该 repository 不再有旧签名或无界 context；
- 三后端编译期断言齐全；
- 契约和默认 File 注入失败测试通过；
- 受影响 package test、Go build、vet，以及并发处的 race 测试通过；
- SQL 行为变化时有已配置 PostgreSQL 证据；
- diff 审查确认没有无关 repository 或机械拆分。

## S4：删除 `store.Store`

最后一个 repository 实现得到 `GO` 后：

1. 用最小 repository 或消费者自有组合替换剩余 constructor 参数和 field；
2. 删除宽接口和全局后端断言；
3. 保留 Memory、File、PostgreSQL 的逐 repository 断言；
4. 增加 source guard，要求生产代码中 `store.Store`、repository 类型断言和
   动态 repository map 都为零；
5. 验证组装根仍然只构造一个选定后端，且没有 Runtime service locator。

S4 独立审查。最后一个 repository 能编译并不等于可以开始 S5 监管。

## 回滚

Repository 阶段不改变 File snapshot 形态。失败阶段作为一个主题回滚，不
回滚此前已接受 repository。S1 的前向 PostgreSQL migration 保持原位且为增量。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| S2 pilot/S3 设计 | pending | pending | pending | pending |
| 每个 repository 实现 | pending | pending | 迁移期间为每个已接受 repository 增加一行 | pending |
| S4 Store 删除 | pending | pending | pending | pending |
