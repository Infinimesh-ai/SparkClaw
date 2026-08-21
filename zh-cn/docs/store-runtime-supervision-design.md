# Store Runtime 与统一监管设计

> 语言：[English](../../docs/store-runtime-supervision-design.md) | 简体中文

> 状态：S5 设计与实现已于 2026-08-22 获得 `GO`。接受的实现为
> `847a470`。

## 目标

在消费者已经依赖小型 repository 后，增加一个组装层拥有的 Store Runtime
和一个有限 Supervisor。本阶段集中运维策略，但不重新创造业务 mega-interface
或 service locator。

## Runtime 边界

`store.Runtime` 只由 `cmd/sparkclaw` 组装层持有，负责：

- 选定 Memory、File 或 PostgreSQL 后端及其生命周期；
- 只在组装时使用的类型化 repository accessor；
- 一个 Supervisor，以及由 S2 pilot 建立并在 S3 补全的有限 operation registry；
- startup probe、readiness projection、recovery probe 和有界 close。

生产 handler、Agent、scheduler、connector、adapter 和 registry 直接接收
repository 接口，绝不接收 `*store.Runtime`。
accessor 由静态填充的 repository set 支撑。Runtime 不通过 type assertion
发现 capability，不提供按名称 lookup，也不生成转发全部 repository method 的实现。

## Supervisor 边界

Supervisor 包装每个已迁移方法现有的 backend 边界，负责：

- caller/fallback deadline 组合；
- 在保留 domain error 的同时分类 backend error；
- 有界 duration 和 result metric；
- 不包含 record content 的安全结构化诊断；
- backend health state 和 readiness projection；
- recovery probe 协调。

Supervisor 不负责 validation、CAS rule、transaction content、record schema、
routing、Policy、approval、delivery、audit persistence 或 retry decision，也不
接受任意 operation name。

Telemetry 不得通过被监管的 Store 写入。

监管接入现有 `operationContext` 和 typed-error 构造边界。File 委托 Memory 时
保留同一个最外层 operation span，因此嵌套实现调用不会重复计数。所有 repository
method 签名保持不变，也不会把已经删除的 broad Store 以 decorator 形式重建。
监管只观察结果；不会在已接受的 P0/P1/P2 repository contract 之外增加恢复、
transaction 或 idempotency protocol。

## 有限 Operation Registry

每个 `OperationID` 只有一个静态 spec：

```go
type OperationSpec struct {
    ID               OperationID
    Repository       RepositoryID
    Mode             OperationMode
    TimeoutClass     TimeoutClass
    AffectsReadiness bool
}
```

注册拒绝重复、缺失或未引用 ID。metric 只使用有界 spec 字段、backend kind
和分类结果。ID、owner、path、query、DSN 和 content 不得成为 label。

最外层 operation 返回时，Supervisor 记录一次 count 与 duration 结果。
`StoreError` 构造把 typed outcome 记录到该 operation span；只有缺少 terminal
outcome 时，cancel 才记录 context 结果。source guard 与 contract test 保证 public
method 继续使用这两个边界，避免把原始 backend error 误报为 success。

S2-S3 操作边界原地升级；repository 方法签名和调用点不再修改。

## 健康状态机

- `not_found`、`conflict`、`invalid` 和 `canceled` 不影响 health。
- `durability_failed`、`unknown_outcome` 和 `corrupt` 让持久后端立即 unready。
- 影响 readiness 的 `timeout` 或 `unavailable` 连续三次后让后端 unready。
- 无关操作成功不能清除降级。
- 只有显式且成功的有界 recovery probe 才能恢复 ready。
- Memory 报告 `durable=false`，但自身不变量 probe 通过时保持 ready；有意的
  非持久性不是 incident。

阈值和 transition 必须确定且经过 race test。公共 readiness 只包含安全状态
和时间，不包含基础设施 secret。

assembly 在发布 repository 前执行 startup probe。一个有界后台协调器只在
unready 时执行 probe；`/readyz` 只读取当前 projection，不执行文件系统或数据库
mutation。

## 探针

File recovery 使用隔离的同目录临时 write、file sync、rename、directory sync、
verify 和 cleanup 流程，不修改 state snapshot。

PostgreSQL recovery 获取 pool，执行 `SELECT 1`，读取 migration ledger，并
验证预期最新 version/checksum。

startup 使用相同有界 primitive，但 backend 构造、migration/load 和 probe
全部通过前不得报告 ready。

## 生命周期

Runtime 构造失败时返回 error，不发布部分 repository。`Close(ctx)` 必须幂等
且有界：拒绝新操作，在 deadline 内等待已 admission 操作，关闭 backend
resource，并 join 相关错误。Gateway shutdown 提供 close context。

close 开始后，共享 operation boundary 会拒绝新工作，包括已经交给 consumer 的
repository interface。shutdown 先停止 recovery coordinator，再关闭 backend，
PostgreSQL pool 只在一个位置拥有 close ownership。

## 验证

强制证据：

- source guard 限制 Runtime 只能在组装层使用；
- finite registry 完整且没有任意 label；
- 更早 caller deadline 和全部 fallback class；
- operation finish 恰好记账一次；
- 即时和阈值式 readiness 降级；
- probe 成功恢复、probe 失败保持降级；
- File/PostgreSQL probe 隔离；
- 没有递归 Store telemetry 或敏感日志；
- 有 in-flight operation 时 close 仍有界且幂等；
- 包装后 Memory/File/PostgreSQL repository 语义一致；
- 聚焦和完整 race test。

## S5 审查门禁

设计 `GO` 要求 ownership、registry、health transition、probe、lifecycle 和
公共投影已接受。实现 `GO` 要求以上验证全部完成，并证明没有消费者重新获得
宽泛 Store 访问。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| S4 前置门禁 | `a6fb4de` | `GO` | broad `store.Store` 与可选 MCP capability discovery 已删除；consumer 使用静态 minimum composite；独立 build/test/vet 与源码抽查均为绿色 | primary agent 在隔离审查 worktree / 2026-08-22 |
| 设计 | `a895c5d` | `GO` | Close 在共享 operation admission 边界拒绝新工作，先停止 recovery 再关闭 backend，并受 caller context 限制；Runtime 保持仅供 assembly 使用 | 获 owner 授权的 primary agent / 2026-08-22 |
| 实现 | `847a470` | `GO` | 独立 worktree 检查未发现 Runtime 逃逸、转发 mega-interface、动态 lookup 或无界 label；build、full test、vet、focused race 和完整 `go test -race ./...` 均通过 | primary agent 在隔离审查 worktree / 2026-08-22 |
