# Store 契约基础设计

> 语言：[English](../../docs/store-contract-foundation-design.md) | 简体中文

> 状态：用户已于 2026-08-20 授权 S0 设计/启动。实现审查仍等待人工验收。
> 此状态不授权 S1，也不授权生产 Store 签名变更。

## 目标

建立安全迁移 Store 所需的证据和精确契约。S0 不修复生产持久化行为，而是
刻画当前行为，把每个方法唯一分配给一个 repository，并定义后续阶段必须
实现的错误、context、事务和 reconciliation 规则。

## 必需交付物

1. 完整盘点当前宽接口、所有生产消费者、三个后端方法、File `Snapshot`
   字段和 PostgreSQL 表、列、索引与约束。
2. 消费者矩阵，列出每个 constructor、field、helper 和 worker 所需的最小
   repository。
3. 命令矩阵，列出变化的记录和派生索引、必需 event/audit、原子事务边界、
   幂等或版本证据、效果提交点和 reconciliation 读取。
4. 覆盖 Memory/File 的后端无关行为刻画 harness，以及现有 DSN 门控的
   PostgreSQL suite。
5. 经接受的 repository 目录，其中每个现有 Store 方法只出现一次。
6. 为 S2 选择一个低风险 pilot repository。它必须已经具有显式 domain error、
   有限的调用者范围，以及适合证明 File 状态机的持久 command；当前 ISCP
   onboarding 或 MCP access 方法只是候选，不是预先决定。

## Repository 目录候选

以下目录只是审查候选，在 S0 退出前不具备权威性：

| Repository | 职责 |
|---|---|
| `OwnerRepository` | owner profile 和外部 owner 查找 |
| `ClientRepository` | client、token 查找、撤销、last-seen、pairing code |
| `ISCPOnboardingRepository` | 仅 ISCP onboarding receipt |
| `CredentialRepository` | 仅加密 credential secret metadata |
| `SessionRepository` | session 创建、列表、查找、重命名、删除 |
| `ConversationRepository` | message 和 message/session event 读取 |
| `RunRepository` | Agent run、feedback、model/tool call、episode summary |
| `DocumentRepository` | 持久 document record 和 lineage metadata |
| `ApprovalRepository` | approval 创建、查找、更新、解决、列表 |
| `ScheduleRepository` | reminder、due claim、CAS、delivery history |
| `ConnectorRepository` | connector setting 和 notification binding |
| `PassiveNotificationRepository` | passive inbox、已读、prune、revision |
| `ExternalChatRepository` | 外部 session/message 和 provider 查找 |
| `DeliveryRecordRepository` | receive、delivery、inbox、幂等记录 |
| `MCPRepository` | access ticket、binding、operation、兑换/撤销 |
| `BrowserStateRepository` | browser auth 和 login-block 生命周期 |
| `MemoryRepository` | candidate、已接受 memory、搜索/更新/删除/prune |
| `AuditRepository` | audit 以及不归 Conversation 管理的 event |
| `EvaluationRepository` | evaluation run 保存/查找/列表 |
| `ArtifactMetadataRepository` | artifact metadata 保存/列表/URI 查找 |

S0 只能依据消费者和事务证据拆分或合并候选。组装便利性不是证据。需要多个
repository 的消费者在自己的 package 中声明本地组合接口。

## 方法形态

通用签名为：

```go
Create(ctx context.Context, value T) (T, error)
List(ctx context.Context, filter Filter) ([]T, error)
Get(ctx context.Context, id string) (T, bool, error)
Update(ctx context.Context, command Command) (T, error)
```

规则：

- `found=false, error=nil` 表示正常查询缺失。
- 命令所需目标不存在时返回类型化 `not_found` 错误。
- CAS、去重和创建保留类型化结果 flag，并增加 error。
- 返回值不得暴露后端拥有的可变 map、slice 或 pointer。
- 接口不得包含 pgx、SQL、filesystem、encryption 或 supervisor 类型。
- Repository 接口位于 `store`；多 repository 组合位于消费者 package。

## 错误契约

| Code | 含义 | 对健康状态的影响 |
|---|---|---|
| `not_found` | 命令要求的目标不存在 | 无 |
| `conflict` | version、CAS、幂等或当前状态冲突 | 无 |
| `invalid` | 确定性的 Store 契约违规 | 无 |
| `canceled` | 已知效果完成前调用者取消 | 无 |
| `timeout` | 操作超过有效 deadline | 持久后端按阈值处理 |
| `unavailable` | 后端无法服务操作 | 持久后端按阈值处理 |
| `durability_failed` | 持久提交确认失败 | 立即降级 |
| `unknown_outcome` | 效果可能已提交，需要 reconciliation | 立即降级 |
| `corrupt` | 持久状态无法解码或违反不变量 | 立即降级 |
| `internal` | 尚未分类的实现失败 | fail closed，并复查分类 |

错误保留内部诊断的原始 cause，并支持 `errors.Is`/`errors.As`。公共投影只暴露
稳定安全 code 和文案。`context.Canceled` 不得改名为 timeout。

## Context 契约

每个已迁移方法接收调用者 context。更早的调用者 deadline 优先；否则 Store
应用 fallback deadline。初始审查值为 read 10 秒、write 30 秒、多记录事务
60 秒、startup/schema 180 秒。S0 必须记录接受这些默认值的理由，之后才能
称其为已验证。

已取消 context 不得开始后端工作。gate admission、pool acquisition、query、
rows 收集、commit 和 reconciliation 在底层支持时都使用有界 context。

## 行为刻画用例

每个 repository 候选都要覆盖：

- 成功、正常缺失、顺序、过滤、owner scope 和 clone；
- 重复 ID、幂等复用、CAS 冲突和删除行为；
- 必需 event/audit 创建和 sequence 顺序；
- File 重启行为和保持不变的 snapshot 形态；
- map、slice、pointer 和嵌套记录的 alias 安全；
- 并发 mutation 和进程内 revision 语义；
- 配置 DSN 时 PostgreSQL row scan 与 `rows.Err()` 处理。

行为刻画测试冻结预期的成功行为。只记录已知静默失败的测试必须标记为缺陷
证据，并在对应迁移阶段替换为失败断言。

## S0 审查门禁

设计 `GO` 要求盘点模板、方法/错误/context 规则和测试计划已接受。实现
`GO` 要求矩阵完成、行为刻画测试绿色，并且没有未分配或重复分配的 Store
方法。

只要仍有未解决的事务 owner、reconciliation 路径或 repository 边界，S1
就不能开始。没有已接受 pilot 时，S2 不能开始。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| 设计/启动授权 | S0 计划 | 仅 S0 `GO` | 已授权范围、不变量、矩阵、行为刻画计划和人工辅助阶段验收；排除 S1 | 用户 / 2026-08-20 |
| 实现 | 候选 SHA 待填写 | `pending` | [清单](store-s0-contract-inventory.md)、[PostgreSQL manifest](store-s0-postgresql-reconciliation-manifest.md)、测试、基线和风险等待人工检查 | pending |
