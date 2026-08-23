# Store

> 语言：[English](../../docs/store.md) | 简体中文

Store package 是 SparkClaw 的 durable state 边界。它向业务 owner 暴露小型、类型化
repository，在 memory、file 与 PostgreSQL backend 上实现同一契约，并向 Gateway 组装层
提供单一 Runtime，统一负责 backend 构造、监管、readiness、指标、恢复探针与 shutdown。

本文描述当前实现，并取代已经完成的 Store 迁移方案与验收记录。

## 职责边界

Repository interface 位于 `services/gateway/internal/store/store.go`。消费者只依赖自身使用的
repository；代码中已不存在宽泛的 `store.Store` interface。当前 repository 集合为：

- identity 与 access：`OwnerRepository`、`ClientRepository`、
  `CredentialRepository`、`SessionRepository`；
- messaging：`ConversationRepository`、`ConnectorRepository`、
  `DeliveryRecordRepository`、`ExternalChatRepository`、
  `PassiveNotificationRepository`；
- execution 与 governance：`RunRepository`、`ApprovalRepository`、
  `AuditRepository`、`MCPRepository`、`ISCPOnboardingRepository`；
- owner data：`DocumentRepository`、`MemoryRepository`、`ScheduleRepository`；
- support record：`EvaluationRepository`、`ArtifactMetadataRepository`、
  `BrowserStateRepository`。

`store.Runtime` 仅用于组装。`cmd/sparkclaw` 选择一个 backend，从 Runtime 取得类型化
repository，再把最小必要 interface 注入 Agent、Gateway、ToolHub、connector、delivery、
schedule 等 owner。Runtime 不转发 repository method，也不得离开 assembly package。

## 风险与聚合策略

可靠性规格由 operation 的 effect 决定，而不是由 repository 大小决定。不要对每条记录一律
使用成本最高的协议。

| 级别 | Operation | 必需证据 |
|---|---|---|
| P0 | Run/ToolCall、delivery、MCP、credential、connector、approval 与 session 删除 | 当一个本地 invariant 跨记录时使用 transaction，稳定 idempotency 或 condition identity，显式 unknown-outcome recovery，确定性 failure injection，真实 PostgreSQL 与 race 覆盖 |
| P1 | Document、schedule、external chat 与 passive notification | 显式 error、caller context 传播、三 backend 一致性；只在跨记录 invariant 上使用 transaction |
| P2 | 普通配置、展示 metadata 与低风险 query | 小型 typed repository、backend contract coverage；没有已证明 invariant 时不设计 recovery protocol |

一个 repository 可以同时包含不同级别的方法。例如 session 删除会闭合依赖 lifecycle state，
因此属于 P0；session lookup 则是普通 read。新增 transaction 前必须指出需要共同变化的准确
record；新增 idempotency 或 reconciliation 前必须指出可证明结果的稳定 operation/candidate
identity。

## Repository 契约

每个 repository method 都以 `context.Context` 为首个参数，以 `error` 为最后返回值。Caller
传播 request、worker 或 shutdown context 并处理 error；production code 不得替换为
`context.Background()`，也不得丢弃 error。

package 通过 `StoreError` 暴露有限 error code：

| Code | 含义 |
|---|---|
| `not_found` | 必需 record 不存在；可选 lookup 通常使用 `(record, found, error)`。 |
| `conflict` | version、condition、idempotency key 或 lifecycle precondition 已不匹配。 |
| `invalid` | command 或持久状态违反类型化契约。 |
| `canceled` / `timeout` | caller 取消 operation，或有限 Store budget 到期。 |
| `unavailable` | backend 拒绝 operation，或无法启动一个可安全 retry 的 operation。 |
| `durability_failed` | mutation 可以确定没有成为 durable Store state。 |
| `unknown_outcome` | submission 可能已经 commit；caller 必须按稳定 identity reconciliation 后才能 retry。 |
| `corrupt` | 持久状态与所有有效预期状态均不一致。 |
| `internal` | 无法归入更具体 public code 的已分类内部失败。 |

Mutation 返回 canonical persisted record。如果 backend 已产生 candidate，但不能证明是否 commit，
则随 `unknown_outcome` 返回该 candidate；repository-specific reconciliation 通过稳定 ID、version、
idempotency key 或完整 normalized record 比较后才报告成功。Definite failure 不返回 candidate，
并恢复先前本地状态。

validation、normalization、clone、conditional command、replay comparison 与 reconciliation
位于 `*_contract.go`，确保所有 backend 实现同一语义。Backend 文件负责存储机制，不得重新定义
竞争性的 domain rule。

## Backend

### Memory

`MemoryStore` 是语义参考实现与确定性测试 backend。它在锁保护下持有 normalized in-memory map，
对包含可变数据的 record 返回 defensive copy。它有意不提供 durability，因此 durability outcome
不会让 memory Runtime 进入 unready。

### File

`FileStore` 是 `MemoryStore` 的 write-through decorator。所有 operation 进入同一个
context-aware admission gate：read 获取共享容量，command 获取全部容量。Command 捕获完整
rollback state，执行 memory mutation，编码一份 snapshot，再通过同目录 temporary file 提交：

```text
encode -> create temp -> write all -> fsync temp -> close
       -> rename over destination -> fsync parent directory
```

replacement 前失败属于 definite failure，会恢复捕获的状态。replacement 时或之后失败属于
`unknown_outcome`，并安装 in-process fence。下一个获准 operation 会读取 destination，将 digest
与 candidate/previous snapshot 比较，随后接受 candidate、恢复 previous state，或把意外第三种
状态标为 `corrupt`。Reconciliation 完成前，任何 operation 都不能越过 fence。

可选 AES-256-GCM snapshot encryption 在 startup 配置。启用 encryption 时仍可读取已有 plaintext
snapshot，并在下一次 mutation 写成 encrypted envelope；没有 encryption 配置时会拒绝 encrypted
snapshot。

File admission 只协调一个 Gateway process。两个 process 不得并发写同一 state path；需要多个
writer 时使用 PostgreSQL。

### PostgreSQL

`PostgresStore` 使用共享 `pgxpool`。当本地 invariant 需要时，跨记录 P0 command 在同一个
acquired session 与 transaction 内执行。Error classifier 会区分确认尚未 submission 的失败和
可能已 submission 的失败。不确定的 session 会被终止而不是放回 pool；存在 candidate 时，command
会随 `unknown_outcome` 返回 candidate。

Gateway 是 application schema 的唯一 authority。`internal/store/migrations` 下的有序 SQL
内嵌进 binary。Startup 会：

1. 获取固定 PostgreSQL advisory lock；
2. 创建或读取 `sparkclaw_schema_migrations`；
3. 验证已记录 version 是完整 prefix，filename 与 SHA-256 checksum 不可变；
4. 在一个 transaction 中应用 pending SQL 与受支持的 pre-ledger compatibility adoption；
5. 在 scratch schema 构造 expected catalog，并比较 table、column、constraint、index 与 predicate；
6. 写入 ledger row 并 commit；commit 不确定时先 reconciliation 一次，再进行一次有限 retry。

checksum 漂移、未知或缺口 version、歧义 legacy identity 与 catalog drift 都会令 startup 失败。
PostgreSQL integration test 保留原有 `SPARKCLAW_TEST_POSTGRES_DSN` gate；未配置时继续 skip。

## Runtime 与监管

`store.NewRuntime` 只构造一个 backend 与一组 repository。Backend probe 通过后 startup 才成功。
Runtime 负责：

- 默认 10、30、60 秒的 read、write、transaction budget；
- 将每个 method 映射到 repository、mode、timeout class 的单一 finite-operation registry；
- active-operation admission、有限 outcome counter 与总 duration；
- readiness state 与周期 recovery probe；
- close admission、drain 与 backend cleanup。

状态为 `starting`、`ready`、`unready`、`closing`、`closed`。`corrupt` outcome 会立即使 Runtime
unready。对 durable backend，`durability_failed` 或 `unknown_outcome` 也会使其 unready。同一
operation 连续三次 `timeout` 或 `unavailable` 后进入 unready。Probe 成功会恢复 readiness 并清除
failure streak。

Memory probe 检查 map 已初始化。File probe 会在 snapshot 同目录执行并清理一次
write/fsync/rename/directory-fsync/read cycle。PostgreSQL probe 会 ping pool 并验证完整 migration
ledger。Probe diagnostic 留在内部；public readiness 只暴露有限 runtime state 与 reason code。

Store unready 时 `/readyz` fail closed。`/metrics` 通过 `sparkclaw_store_*` metric 只导出有限的
backend、repository、operation、mode、outcome label，绝不导出 path、DSN、owner、record ID
或 raw error。

Shutdown 时 Runtime 停止 recovery、拒绝新 operation、在 close context 内等待已获准 operation，
最后关闭 backend。Caller 不得在 Runtime shutdown 后继续持有 repository。

## Source 布局

Store code 按 repository 与 backend 组织：

```text
store.go                         repository interface 与 compile-time parity
operation.go                     finite operation/error registry
runtime.go / supervisor.go       assembly 与 lifecycle supervision
probe.go                         backend probe
<repository>_contract.go         shared semantics 与 command type
<repository>_memory.go           Memory implementation
<repository>_file.go             File admission 与 durable command wrapper
<repository>_postgres.go         PostgreSQL implementation
<repository>_*_test.go           contract、durability、failure 与 guard test
file.go / postgres.go            backend construction 与 shared primitive
file_durability.go               replacement、rollback、fence、reconciliation
postgres_migrations.go           embedded schema ledger 与 catalog validation
```

当拆分会破坏单一 authority 时，大型但内聚的 registry 可以超过普通文件大小准则。当前明确例外为
`operation.go`、`mcp_access.go` 与 `mcp_access_postgres.go`；不得向其中加入无关 repository
behavior。

## 修改 Store

新增 method 或 record 时：

1. 分配 operation 风险级别并说明 aggregate invariant；
2. 新增或扩展最小 repository interface 与 shared contract；
3. 注册唯一 operation ID、mode、timeout class 与 repository owner；
4. 在同一次 change 中实现 Memory、File、PostgreSQL；
5. snapshot-backed record 同步更新 File `Snapshot`；
6. caller 迁移到 narrow interface 与 propagated context；
7. 先补 parity test，再只补该风险级别要求的 failure injection、reconciliation、PostgreSQL 与
   race evidence；
8. 保留 source guard，拒绝 broad Store dependency、backend method 缺失、ignored error 与
   unbounded context。

不得通过 optional type assertion 静默移除 capability，不得恢复第二个 broad Store facade，
不得让 caller 按 backend 分支，也不得在缺少稳定 proof identity 时设计 recovery protocol。

## 验证

按比例执行的本地 gate 为：

```bash
cd services/gateway
go test ./internal/store
go test -race ./internal/store
go build ./...
go vet ./...
go test ./...
```

PostgreSQL 可用时保留现有 opt-in contract：

```bash
SPARKCLAW_TEST_POSTGRES_DSN='postgres://...' go test ./internal/store
```

影响 caller 的 change 还要运行对应 focused package test。最终 repository gate 包含完整 Go race
suite、WebChat test 与 production build、ASR fake-model protocol test，以及双语文档 mirror/link
检查。
