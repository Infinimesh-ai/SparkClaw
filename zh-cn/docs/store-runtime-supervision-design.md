# Store Runtime 与统一监管设计

> 语言：[English](../../docs/store-runtime-supervision-design.md) | 简体中文

> 状态：S5 设计审查草案，2026-08-19。S4 证明 `store.Store` 已删除前，
> 不得合并 Runtime 或健康抽象。

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
| 设计 | pending | pending | pending | pending |
| 实现 | pending | pending | pending | pending |
