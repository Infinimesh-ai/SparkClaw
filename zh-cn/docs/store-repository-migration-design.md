# Store Repository 迁移设计

> 语言：[English](../../docs/store-repository-migration-design.md) | 简体中文

> 状态：S2 pilot 实现经过独立审查后，已于 2026-08-20 在 `9d86c50`
> 获得接受。S3 已激活，当前只有 `OwnerRepository` 一个 repository 波次在进行。

## 目标与阶段边界

把 141-method `store.Store` 以小型、可审查的 domain repository 分波迁移。
S2 只迁移已接受的 `ISCPOnboardingRepository` pilot，同时证明 File transaction
model。S3 逐个迁移其余 repository，S4 删除临时 broad interface，Runtime 和
Supervisor 仍属于 S5。

S2 不按职责拆分 `file.go`、`memory.go`、`postgres.go` 或其他大型 Store module。
只有 Store 迁移和监督完成后，才重新审查文件尺寸与职责拆分。

## 迁移单元

一个 repository 是一个实现阶段，通常对应一个 behavior-change commit。完成的
阶段必须：

1. 确认 S0 接受的方法、command、reconciliation 与 consumer row；
2. 定义一个 typed repository interface 和全部 backend assertion；
3. 为每个可能 backend failure 的方法增加 caller context 与 error result；
4. 仅在 record shape 需要时更新 Memory、File、PostgreSQL 与 File `Snapshot`；
5. 更新每个 caller，使其传递 request、operation、worker、startup 或 shutdown
   context；
6. 按需新增 shared contract、File failure、PostgreSQL classification、timeout、
   cancellation 与 race test；
7. 删除该 repository 的旧签名；
8. 在开始下一个 repository 前获得实现审查。

完成的 repository path 不得保留 compatibility adapter、optional type assertion、
duplicate method、dynamic repository map、string-based dispatch 或
`context.Background()`。

## S2 Pilot Contract

在 `internal/store` 中引入：

```go
type ISCPOnboardingRepository interface {
    SaveISCPOnboarding(context.Context, app.ISCPOnboarding) (app.ISCPOnboarding, error)
    GetISCPOnboarding(context.Context, string) (app.ISCPOnboarding, bool, error)
    ListISCPOnboardings(context.Context, string) ([]app.ISCPOnboarding, error)
}
```

contract 如下：

- save 校验并 normalize receipt，以 ID 只创建一次；duplicate 继续满足
  `errors.Is(err, ErrISCPOnboardingConflict)`；
- get 只有 normal absence 才返回 `(zero, false, nil)`；
- list 只有成功且确实为空时才返回 empty non-nil slice 和 nil；按 `CreatedAt`
  newest-first，并用 ID 作确定性 tie-break；
- cancellation 和 backend failure 都是 error，不能变成 absence 或成功空列表；
- receipt 仅有 scalar/time field，不暴露 mutable backend alias；
- repository 不拥有 audit row；`iscppairing.Service` 仍在 receipt save 成功后由
  caller append `iscp.onboarding.ticket_issued`。

ID 是 idempotency/conflict 边界。uncertain save 期间，由
`iscppairing.Service` 而非 HTTP caller 持有 ID 和尚未披露的 issued ticket。它用
`GetISCPOnboarding(ctx, id)` 对账；完成前不能为该 owner 再调用 authority 或生成
另一次 save。

## 有限 Operation 边界

S2 引入 package-private finite operation metadata，并由 pilot 立即消费：

| Operation ID | Mode | Timeout class | Reconciliation |
|---|---|---|---|
| `iscp_onboarding.save` | write | write | 按 ID get |
| `iscp_onboarding.get` | read | read | 自身 |
| `iscp_onboarding.list` | read | read | 无 |

`OperationSpec` 绑定 ID、repository ID、method/mode 和 timeout class。registration
test 拒绝 duplicate ID、缺少 pilot method、未知 timeout class 和未引用 spec。
Operation ID 是常量；record ID、owner ID、query、path、DSN 与 content 不能成为
operation name 或 label。

S2-S3 期间，该边界只负责 deadline composition 与 typed error classification，
不暴露 health、metrics、repository lookup、Runtime 或 Supervisor。S5 包装这些
原有 call site，不再改 repository 签名。

## Timeout 配置

S2 只引入 pilot 实际消费的 timeout class：

| Typed field | JSON | Environment | Default | Valid range |
|---|---|---|---|---|
| `StateConfig.ReadTimeoutSeconds` | `state.read_timeout_seconds` | `SPARKCLAW_STATE_READ_TIMEOUT_SECONDS` | 10 秒 | 1-900 秒 |
| `StateConfig.WriteTimeoutSeconds` | `state.write_timeout_seconds` | `SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS` | 30 秒 | 1-900 秒 |

环境变量格式错误时，`config.Load` 失败并指出精确变量；配置文件或环境值越界时，
normalization 失败并指出 JSON field。两个值在同一 pilot commit 中加入
`config.Default`、`configs/sparkclaw.default.json`、File/Memory/PostgreSQL backend
option、command assembly 与 config test。它们不是 credential；只有现有 public
redacted config 已暴露 `state` object 时才可随之出现，S2 不新建 public Store
settings endpoint。

effective deadline 取非零 caller deadline 与 configured fallback 中更早者。
`context.Canceled` 绝不重新标记为 timeout。60 秒 multi-record transaction setting
在 S2 不增加，因为 pilot 只修改一个 record。PostgreSQL 对下述 transaction-
scoped resolution barrier 使用 30 秒 write context；multi-record setting 由第一个
实际消费它的 S3 repository 引入。已接受的 180 秒 startup setting 保持不变。

现有 convenience constructor 使用已接受 default，让 test 和 local caller 保持
确定性。生产 assembly 通过 backend option 显式传入 validated config；不存在
package-global mutable timeout。

## Typed Error Contract

pilot 引入 `StoreError` 与符合 S0 contract 的 finite `StoreErrorCode`：

| Code | Pilot 用法 |
|---|---|
| `not_found` | 预留给要求 target 的 command；onboarding get 使用 normal absence |
| `conflict` | duplicate onboarding ID，并保留 `ErrISCPOnboardingConflict` |
| `invalid` | 确定性 receipt contract violation |
| `canceled` | 已知 effect 完成前 caller context canceled |
| `timeout` | 已知 effect 完成前超过 effective deadline |
| `unavailable` | backend 当前无法服务 operation |
| `durability_failed` | File candidate 确定在提交前/提交点失败，且 Memory 已恢复 |
| `unknown_outcome` | 已提交 File/PostgreSQL effect 需要对账 |
| `corrupt` | persisted payload 无法 decode 或违反 receipt invariant |
| `internal` | 未分类失败；fail closed 并审查分类 |

`StoreError` 包含 code、finite operation ID 与 wrapped cause，并实现 `Unwrap`；
helper 支持 `errors.As`、stable code extraction 和无需解析字符串的安全分类。
label 或 public copy 不能包含 record data。domain sentinel 保留在 error chain 中；
`pgconn.PgError`、filesystem path、DSN 与 raw payload 等 backend error 只作内部
cause。

## Backend 规则

### Memory

- 在获取 lock 前检查 effective context，并在 lock 内、mutation 或 read 前再次
  检查；
- 保留 normalization、duplicate、ordering 与 scope 行为；
- 在一个 lock 下只修改 receipt，不修改 caller-owned audit；
- 成功空列表返回 empty non-nil slice；
- 不返回 backend-owned mutable data。

pilot 不替换 Memory 现有 mutex。其 critical section 只在进程内执行且无 I/O；
等待期间发生的 cancel 会在任何 effect 前于 lock 内观察到。

### File

- 使用已接受的 [File Store 持久性](store-file-durability-design.md) gate 与 command
  state machine；
- 使用 effective caller context 获取 read/write admission；
- 只有 replacement 和 parent-directory sync 成功后才返回成功；
- 确定的 pre-submit failure 恢复完整 pre-snapshot 与 volatile sidecar；
- 不确定的 submitted replacement 返回并 fence `unknown_outcome`；
- get/list 在返回 data 前执行已定义的 reconciliation；
- 拒绝 missing state path，且保持 snapshot/encryption schema。

### PostgreSQL

- 把 effective context 传给 pool acquisition、每个 `Exec`、`Query`、`QueryRow`、scan loop 与
  reconciliation call；pilot 文件不含 `context.Background()`；
- SQLSTATE `23505` 映射为 conflict，同时保留
  `ErrISCPOnboardingConflict`；
- 只在 get 中把 `pgx.ErrNoRows` 映射为 normal absence；
- 返回 query、scan、JSON decode/validation 与 `rows.Err()` failure；
- 失败 query 绝不能变成成功空列表；
- 一行 save 及其 resolution lock 使用一个显式 transaction，不包含 caller-owned
  audit；
- 同时依据 effective context 与 PostgreSQL cause 分类 context outcome，不使用
  string matching。

save 与 get 共享按 onboarding ID keyed 的 transaction-scoped PostgreSQL advisory-
lock protocol。key 是对固定 namespace 加 ID 做 SHA-256 后，前八个 bytes 构成的
signed 64-bit value。collision 只会串行化无关 ID，不能合并或授权其数据。

save 不直接调用 `pgxpool.Exec`，而是用 effective write context 获取一个 pool
connection，begin 显式 transaction，获取 `pg_advisory_xact_lock($1)`，insert
receipt，然后 commit。transaction 只修改一个 record，因此使用 30 秒 write
class，不激活未来 multi-record transaction setting。生产分类冻结为：

| 阶段/结果 | 分类 | Retry 规则 |
|---|---|---|
| context 结束，或 transaction 存在前 pool acquire/begin 失败 | `canceled`、`timeout` 或 `unavailable` | insert transaction 不存在，允许普通 retry |
| lock 或 insert 返回服务端 `*pgconn.PgError` | SQLSTATE `23505` 为 `conflict`，其他服务端拒绝映射为对应 definite code，然后 rollback | PostgreSQL 拒绝 statement，不是 unknown commit |
| pre-commit error 满足 `pgconn.SafeToRetry(err)` | 依据 context/cause 为 `canceled`、`timeout` 或 `unavailable`，然后 rollback | failing statement 未发送；insert 无后续 commit 就无法生效 |
| unsafe lock/insert transport error 或任何 commit error | terminate owned session 并返回 `unknown_outcome` | transaction state 或 commit 不确定；必须 barrier get-by-ID |
| commit 成功 | success | transaction 已完成 |

每个 definite pre-commit branch 都尝试 rollback。若 rollback 自身失败，Store 在
返回前 hijack 并 close session；由于这些 branch 不存在成功 insert 后再 commit 的
路径，其 record effect 仍是 definite，cleanup failure 只保留为 cause。

unsafe error 时，Store 用 `pgxpool.Conn.Hijack` 接管 ownership，再用从 operation
context 的 `context.WithoutCancel` 派生的 5 秒 context 调用 `PgConn.Close`。
即使 clean-close context 失败，`PgConn.Close` 也总会关闭底层 network connection。
close result 只保留为内部 diagnostic evidence，不能证明 commit 或 rollback。

get-by-ID 是 resolution barrier。它获取另一个 pool connection，并且无论 DSN、
role 或 database default isolation 如何，都显式使用
`pgx.TxOptions{IsoLevel: pgx.ReadCommitted}` begin。它用第一条独立 statement 获取
同一 transaction-scoped advisory lock，再用第二条独立 statement select 并
validate row。原 transaction/session 释放 lock 前 PostgreSQL 不会授予该 lock；
显式 `READ COMMITTED` 使第二条 statement 在 lock 释放后取得新 snapshot。因此，
拿到 lock 后找到 row 才是 committed result，拿到 lock 后 absence 才是最终
rollback/absence，即使 configured server default 是 `REPEATABLE READ` 或
`SERIALIZABLE` 也一样。

禁止把 lock 与 query 合并成一条 statement、忽略 `TxOptions` 或接受 pre-lock
snapshot。若 acquire、lock、query 或 transaction completion 无法在 effective
read context 内完成，get 返回 error，service 保留 pending `unknown_outcome`；
绝不能报告 pre-barrier absence。

尤其是 transaction 已存在后的 deadline/cancellation error，在 `SafeToRetry` 为
false 时不能标为 `timeout`/`canceled`；没有 lock barrier 就不能信任 immediate
negative reconciliation。package-private acquire/begin/lock/exec/commit seam 覆盖
此 protocol 每一行，但不替代 real-DSN evidence。configured integration test
验证真实 insert、duplicate、barrier absence/found result、list/rows handling、pool
acquisition 前 cancellation 与 restart。

## Consumer 与 Assembly 迁移

`iscppairing.Service` 不再保存 `store.Store`。它接受 consumer-owned minimal
composite，其中包含 `store.ISCPOnboardingRepository` 和现有 caller-owned audit
append capability。audit 部分只是等待 S3 `AuditRepository` 的临时依赖，不能让
service 获得无关 Store 方法。

consumer 精确变化：

- `Start` 把 request context 传给 save；
- `Start` 在任何 authority call 前获取 context-aware capacity-one service
  admission；SparkClaw 是 single-owner 产品，一个全局 admission 即可，并可阻止
  并发 request 平行签发 ticket；
- authority 返回后、save 开始前，service publish 一个 in-memory pending record，
  其中保存 owner、onboarding ID、normalized request fingerprint、receipt 和尚未
  披露的 signed ticket；该值绝不持久化、log、audit 或 public projection。
  fingerprint 是 canonical owner、normalized display name、effective TTL、
  configured domain 与 expected ticket type 的 SHA-256 digest；
- definite save failure 清除 pending、丢弃 signed ticket，并且不再调用 authority
  就返回；之后一次显式 Start 是新的 logical attempt，可以签发 fresh ticket；
- save 返回 `unknown_outcome` 时，`Start` 立即在剩余 request context 内按 ID get
  对账。确认 receipt 后完成原 operation、append caller-owned audit、只返回一次
  原 ticket 并清除 pending；确认 absence 后清除 pending，返回 definite
  persistence failure，且不再调用 authority；仍无法对账则保留 pending，返回
  safe unavailable；
- 每个后续 `Start` 都在 service admission 下先检查 pending 再调用 authority。
  相同 normalized request 执行对账，并在 commit 后获得 retained original ticket；
  不同 request 在 pending operation 解决前收到 stable conflict。pending 存在时
  不存在签发第二张 authority ticket 的路径；
- pending 受 authority ticket expiry 限制。若到 expiry 后才首次对账成功，receipt
  保持可见，但丢弃 expired signature，并报告需要一次新的显式 start；该
  reconciliation call 自身不能签发 fresh ticket；
- `List` 变成 `List(ctx, ownerID) ([]app.ISCPOnboarding, error)`；
- GET handler 传入 `r.Context()`，且不能把 backend failure 序列化成空 `200`
  list；
- backend timeout 映射为 stable gateway-timeout response；unavailable、durability、
  unknown、corrupt 与 internal failure 映射为不暴露 raw Store cause 的 stable
  service-unavailable response；
- conflict/invalid 保持 stable client/domain failure；
- assembly 在 `newISCPPairingService` 接受 minimal composite；只有 backend factory
  和临时 broad assembly value 继续保留 `store.Store`。

authority call 仍先于 receipt save，因为 receipt 来自已签名 authority response。
pending coordinator 关闭了同一进程内 unknown-outcome retry window，并能在成功
对账后返回原 ticket。authority contract 仍没有 revocation 或 idempotent request
recovery。因此，确定的 local save failure、authority issuance 后的 process
crash/restart，或对账前 ticket expiry 仍可能留下未披露 remote ticket。解决这些
remote/local atomicity 与 crash-recovery case 需要 authority protocol 变化或可恢复
authority request contract，不能藏进 Store。

audit append 仍发生在 receipt persistence 成功之后，并在 `AuditRepository` 迁移
前保留 legacy failure behavior。S0 已明确把 audit 分配给 caller，因此 pilot 不
宣称 receipt 与 audit 原子。

## 临时 Broad Interface

S2 期间，broad interface 只 embed 一次 `ISCPOnboardingRepository`，并只声明其余
138 个 legacy method，不重复三个 migrated signature。`MemoryStore`、`FileStore`
和 `PostgresStore` 分别 assertion small interface，同时继续 assertion 临时 broad
interface。

新生产 consumer 不能接受 `store.Store`。source guard 证明 onboarding 旧签名和
`context.Background()` 已消失，并证明 `iscppairing.Service` 没有 broad Store
field 或 constructor parameter。

## S3 规划顺序

S2 实现并经人工验收后，推荐风险顺序保持为：

1. Owner、Client、Credential 与 Session；
2. Conversation、Run、Document、Approval、Audit、Evaluation 与 artifact
   metadata；
3. Schedule、Connector、Delivery Record、Passive Notification 与 External Chat；
4. MCP、Browser State 与 Memory。

每次只激活一个 repository。Session deletion、MCP redemption 和其他 cross-record
command 必须有显式 transaction case；小型 interface 名称不代表它们只是简单
CRUD。

## Pilot 验证与 Commit 边界

S2 实现使用两个可独立审查的 commit：

1. 全 File method 的机械 admission，不改 pilot 签名或 error behavior；
2. timeout/error operation boundary，以及 onboarding repository 在三个 backend、
   consumer、assembly 和 test 中的完整迁移。

pilot gate 要求：

- Memory/File shared contract test：success、absence、order/scope、duplicate
  conflict、cancellation、timeout 与 non-nil empty list；
- File 设计规定的默认 File injected-failure、rollback、fence、reconciliation、
  encryption、restart 与 race test；
- PostgreSQL unit classification：query/scan/rows/context 与 uncertain submission，
  包括每个 acquired-connection/`SafeToRetry` table row、owned-session termination
  和两个 advisory-lock barrier result，再加 real-DSN integration evidence。
  barrier test 把 session/server default 设置为 `REPEATABLE READ` 与
  `SERIALIZABLE`，仍要求显式 `READ COMMITTED` 两 statement 的结果；
- `iscppairing` service test：context propagation、safe failure copy、persistence
  definite failure 后不披露 ticket、immediate 和 next-request unknown
  reconciliation、无第二次 authority call、different-request conflict、pending
  expiry、并发 Start serialization，以及 caller-owned audit order；
- Gateway test：`r.Context()` propagation 和 list failure 非 200；
- 两个新 timeout 的 config default/environment/range/assembly test；
- source guard：interface 只 embed 一次、无 pilot 旧签名、无 pilot
  `context.Background()`、无 ignored pilot persistence/rows error，consumer 无 broad
  Store dependency；
- `go test ./...`、`go build ./...`、`go vet ./...`、聚焦 Store race、默认 File
  production-entry test、WebChat test/build 与双语 docs CI；
- 使用现有 `SPARKCLAW_TEST_POSTGRES_DSN` opt-in 的真实 PostgreSQL run；CI service
  topology 和 skip behavior 保持不变。

只有 interface scaffolding、operation registry 或 File gate 不能获得 S2 实现
`GO`；生产 caller 与三个 backend 必须全部使用新 contract。

## S4 Broad Store 删除

每个 S3 repository 实现都获得 `GO` 后：

1. 用 minimum repository 或 consumer-owned composite 替换其余 constructor
   parameter 与 field；
2. 删除 broad interface 与 global backend assertion；
3. 保留三个 backend 的 per-repository assertion；
4. 要求生产代码零 `store.Store` reference、零 repository type assertion、零
   dynamic repository map；
5. 验证 assembly 仍构造一个 selected backend，且没有 service locator。

S4 独立审查。最后一个 repository 仅仅 compile 不能开始 S5 supervision。

## 回滚

pilot 不修改 File snapshot 或 PostgreSQL schema shape。若被拒绝，可以回退
behavior commit 而不删除已独立接受的 mechanical gate，但仍以其单独审查决定为
准。S1 forward PostgreSQL migration 保持不变。

## 审查记录

| 审查 | Revision/commit | 决定 | 证据与未解决风险 | Reviewer/date |
|---|---|---|---|---|
| S2 pilot/S3 设计审查 1 | `3aff151` | `REVISE` | uncertain save 未阻止 retried Start 签发第二张 authority ticket，且 PostgreSQL autocommit 缺少可验证 submission classifier | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 设计审查 2 | `4f8b2e5` | `REVISE` | uncertain autocommit 后立即 negative query 不具最终性，因为原 backend transaction 可能稍后 commit | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 设计审查 3 | `d88d321` | `REVISE` | reconciliation 未冻结 `READ COMMITTED`；更高 default isolation 可能保留 pre-lock snapshot 并返回 false absence | Independent gatekeeper / 2026-08-20 |
| S2 pilot/S3 设计审查 4 | `49b0858` | `GO` | 显式 `READ COMMITTED` 与独立 advisory-lock/query statement 使 found/absence 不受 server isolation default 影响而具最终性；更早的 fence、pending-ticket 与 submission finding 均已关闭 | Independent gatekeeper / 2026-08-20 |
| S2 pilot 实现 | `9d86c50` | `GO` | 完整 File admission 与 onboarding 迁移通过已接受的 focused/full/race/default-File/real-PostgreSQL matrix；独立审查未发现 actionable finding | Independent gatekeeper and primary agent under owner-delegated authority / 2026-08-20 |
| 每个 repository 实现 | pending | pending | 迁移期间为每个已接受 repository 增加一行 | pending |
| S4 Store 删除 | pending | pending | pending | pending |
