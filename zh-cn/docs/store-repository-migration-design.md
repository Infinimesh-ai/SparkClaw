# Store Repository 迁移设计

> 语言：[English](../../docs/store-repository-migration-design.md) | 简体中文

> 状态：S2 pilot 已在 `42b62bd` 获得接受，S3 OwnerRepository 已在
> `0b85cc4` 获得接受，S3 ClientRepository 已于 2026-08-20 在 `a4ddc83`
> 获得接受。CredentialRepository 是下一设计波次；其设计获得 GO 前不授权实现。

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

### 已接受波次：OwnerRepository

第一个 S3 波次把 S0 已接受的六个 Owner method 冻结为一个 repository：

```go
type OwnerRepository interface {
    GetOwnerProfile(context.Context) (app.OwnerProfile, error)
    UpdateOwnerProfile(context.Context, app.OwnerProfile) (app.OwnerProfile, error)
    GetOwnerProfileByID(context.Context, string) (app.OwnerProfile, bool, error)
    SaveOwnerProfile(context.Context, app.OwnerProfile) (app.OwnerProfile, error)
    ListOwnerProfiles(context.Context) ([]app.OwnerProfile, error)
    FindOwnerProfileByExternalRef(context.Context, string, string) (app.OwnerProfile, bool, error)
}
```

稳定 repository 语义如下：

- 所有 read 都无副作用。PostgreSQL default owner 只在 startup seed，绝不由
  `GET` 或其他 repository read 写入；
- 空 owner ID 解析为 `app.DefaultOwnerID`；正常缺失是
  `(zero, false, nil)`，空 source 或 external ref 也是正常缺失；
- backend、cancellation、timeout、corrupt data 以及 query/scan/`rows.Err()`
  failure 必须返回 error，不能伪装为缺失或成功的空 list；
- preferences 在每次 input/output 以及 File snapshot capture/load 时都复制，
  caller 与 backend snapshot 不能共享可变 map；
- list 顺序为 `UpdatedAt DESC`，再按 `ID ASC`；external-ref lookup 使用同样的
  newest-first 与 ID tie-break；
- save 按 ID overwrite 并保留已有 `CreatedAt`；update 强制使用
  `app.DefaultOwnerID`；
- candidate normalization trim 每个 string field，把空 ID default 为
  `app.DefaultOwnerID`，把 default owner 的空 source default 为 `web`，把每个
  owner 的空 display name default 为 `Owner`，把 nil preferences 变成 cloned
  non-nil empty map。仅新 row 可
  使用 caller 提供的 non-zero `CreatedAt`，否则使用 repository clock；已有 row
  永远精确保留其 `CreatedAt`，包括 legacy File/Memory nanosecond precision，不能
  通过 preservation 静默改写历史时间。只有 repository 新分配的持久化时间使用
  UTC PostgreSQL microsecond precision；
  `UpdatedAt` 由 repository 分配，必须严格大于 current persisted value 与该 owner
  的 process-local last-issued high-water mark，包括之后失败或保持 unknown 的
  candidate；high-water mark 永不随 rollback 回退；
- save/update 原子持久化 owner profile、一条 `owner_profile.updated` audit 和
  一条 `owner_profile.updated` event。任何 backend 都不能报告 profile-only
  success。

operation registry 原地扩展：

| Operation ID | Mode | Timeout class | Reconciliation |
|---|---|---|---|
| `owner_profile.get` | read | read | self |
| `owner_profile.update` | write | transaction | get by ID |
| `owner_profile.get_by_id` | read | read | self |
| `owner_profile.save` | write | transaction | get by ID |
| `owner_profile.list` | read | read | none |
| `owner_profile.find_external_ref` | read | read | none |

首个 multi-record S3 command 新增 `StateConfig.TransactionTimeoutSeconds`、JSON
`state.transaction_timeout_seconds` 和环境变量
`SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS`，默认 60 秒，有效范围 1-900 秒。
它同步传播到 defaults、受控 JSON config、example environment、Compose、公开
redacted state projection、Memory/File/PostgreSQL options、assembly 和 tests。
caller deadline 更早时仍优先；现有 read、write 和 180 秒 startup class 不变。

Memory 在 lock 前及 lock 内检查 effective context，在两个边界复制 profile，并在
同一把锁内完成 record/audit/event mutation。File 先用纯机械 commit 泛化 S2 已
接受的 command helper，再复用完整 snapshot rollback、durable replacement、
unknown-outcome fence 和 read reconciliation，且不改变 snapshot schema。File
startup 在 `Snapshot.OwnerProfiles` 存在时以它为 authority；legacy
`Snapshot.OwnerProfile` 只是 default row 的 compatibility copy。startup 拒绝每个
map-key/embedded-ID mismatch，要求 map 包含 default row，并要求该 row 与 legacy
copy 的每个 persisted field 和 preference entry 完全一致。唯一例外是旧 constructor
调用两次 `DefaultOwnerProfile` 写出的 snapshot：两个 profile 都仍是未编辑的 stock
default owner 时可有不同初始化 timestamp，且仍以 map entry 为 authority。map
缺失时，只有 legacy copy 的 embedded ID 精确等于 default ID 才能提升它；若两个
legacy owner field 都完全缺失，则该 snapshot 早于 owner schema，startup 在内存中
seed 一个 stock default owner。除此之外不能 trim、default 或以其他方式正常化
损坏的 persisted identity。

PostgreSQL save/update 获取一条 owned connection，开始显式 transaction，以 owner
ID 获取 transaction-scoped advisory lock，读取 current row 以保留 `CreatedAt`，
upsert owner，append audit/event 后 commit。Commit 是 effect-submission point。
safe pre-submit failure 是确定结果；unsafe statement/transport failure 或 commit
failure 必须终止 owned session；candidate formation 已完成时返回
`unknown_outcome` 及 normalized candidate，candidate formation 前失败则返回 zero
candidate。Get-by-ID 是 resolution barrier：显式使用 `READ COMMITTED`，用一个
statement 获取相同 advisory lock，再用独立 statement 读取 owner。Owner
preferences JSON decode failure 是 `corrupt`，不能变成空 map 或 absence。

只有 barrier 返回的 profile 在每个 persisted string、timestamp 和 preference entry
上与 unknown candidate 完全一致时，对账才接受 success。candidate allocation 按
owner serialize；non-rollback high-water mark 防止同一 Gateway process 的后续
command 在 fixed/backward clock 下重新生成 uncertain candidate，因此 exact match
证明 profile 来自本次 atomic owner/audit/event transaction。candidate 只在内部
存在且不跨 Gateway process；process loss 也会终止拥有该 proof 的 request。
不同 profile、absence、zero unknown candidate 或 reconciliation error 都保持
unresolved：later writer 可能已经 interpose，因此 caller 不能自动 retry 或报告
success；它返回安全 unavailable/conflict copy，并要求 uncertain call 结束后再发起
fresh explicit command。File global fence 可以在内部证明 candidate 或 rollback，
但生产 caller 仍使用该保守 candidate-match rule。

PostgreSQL startup 使用现有 180 秒 startup context，以 `ON CONFLICT DO NOTHING`
插入 default owner，然后读取并验证该 row。它不 overwrite 已有 owner，也不产生
save/update audit/event，因为这是 readiness invariant establishment，不是 Owner
command。insert 或 confirmation failure 使 readiness 保持 false。若 invariant default
row 之后消失，read-only `GetOwnerProfile` 返回 `corrupt`；tests 证明 read 不会发出
`INSERT`、`UPDATE` 或 lifecycle append。

生产 caller 传递自身拥有的 context：Gateway handler 使用 `r.Context()`；ISCP
Bridge 把 `Dispatch` context 传进 session creation；Telegram 使用 `HandleUpdate`
worker context；Weixin 使用 `HandleInbound` worker context。迁移后的 Owner path
不得引入 `context.Background()`；helper 必须返回 error，不能忽略 repository
failure。Gateway 将 timeout 映射为稳定 504，将 unavailable、durability、unknown、
corrupt 或 internal 映射为不含 backend cause 的安全 503。Connector/Bridge path
返回稳定、可重试的 unavailable error。save/update 收到 `unknown_outcome` 的 caller
在声明 success 前使用上述 exact candidate rule，unresolved result 不自动 retry。
Weixin 用确定性 owner ID 对账，只为历史非确定性 profile 保留 external-ref lookup。
`Syncer.processBatch` 把 batch context 传给 pre-download `ensureChatSession`；repository
failure 可重试，且阻止 provider cursor advancement。

Owner 波次门禁要求：Memory/File shared success、absence、ordering、scope、clone、
cancellation 与 timeout tests；确定性 external-ref tie-break parity；File 对
owner/audit/event 的 rollback、fence、reconciliation、restart、encryption 与 race
证据；PostgreSQL statement/commit classification、session termination、atomic
rollback、startup seed、read-only GET、corrupt JSON、query/scan/rows propagation 与
exact candidate reconciliation、显式 `READ COMMITTED` barrier 证据；startup seed
missing/existing/failure tests 与 GET-no-Exec proof；real-DSN round-trip/restart/race
tests；caller context、normalization parity、safe error projection 与 Weixin
no-cursor-advance tests；fixed/backward-clock 与 failed-candidate high-water tests；
以及一个 embedded repository、无 legacy signature、无
ignored Owner error、无迁移后 `context.Background()` 的 source guards。现有
PostgreSQL CI topology 与 `SPARKCLAW_TEST_POSTGRES_DSN` skip behavior
不变，但实际 configured PostgreSQL run 是 `GO` 的强制证据。

本波次有三个可审查 commit boundary：双语 contract freeze；保持 onboarding
behavior byte-identical 的机械 File helper 泛化；以及跨所有 backend/caller 的完整
Owner behavior migration。精确 Owner candidate 完成独立、context-isolated 实现
审查前，不开始下一个 repository。

### 已接受波次：ClientRepository

Owner 实现在 `0b85cc4` 获得独立 GO，gate record 为 `fc5acba`。Client 的精确
边界、合并后的 pairing claim command、pending-secret recovery、backend
compatibility 与实现 gate 由 [ClientRepository contract](store-client-repository-design.md)
负责；其完整实现已在 `a4ddc83` 获得 GO。

### 下一波次：CredentialRepository

CredentialRepository 是唯一获授权的设计波次。其精确 method、secret-redaction、
overwrite/delete、durability、reconciliation、consumer 与 backend compatibility
合同由 [CredentialRepository contract](store-credential-repository-design.md) 定义，
并且必须先获得独立 GO 才能开始代码。Credential 实现获得自身 GO 前，Session
及后续 repository 设计仍保持阻塞。

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
| S2 pilot 实现初次审查 | `9d86c50` | 已被取代的 `GO` | 完整 File admission 与 onboarding 迁移通过初次证据审查；之后的新审查取代了这一决定 | 独立 gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S2 pilot 实现重新审查 | `9d86c50` | `REVISE` | ticket 可能在持久化/对账期间过期，但 completion 重用了请求开始时间，仍会披露它 | Context-isolated gatekeeper / 2026-08-20 |
| S2 pilot 修复实现 | `bc1bfb4`, `6f4c1bf`, `437e4bc`, `42b62bd` | `GO` | completion 在披露前立即读取 live clock，并新增同一调用内过期覆盖、独立重复的 disposable real-PostgreSQL full/race run、完整 File failure evidence，以及经过验证的 Compose read/write timeout override 转发 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Owner contract review 1 | `57d5b6d` | `REVISE` | existing-row unknown outcome 缺少 commit proof；startup seed、candidate normalization、legacy File owner precedence 与 Weixin pre-download context path 定义不足 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Owner contract review 2 | `08a327b` | `REVISE` | exact candidate matching 仍允许后续相同 writer 重新生成 rolled-back microsecond timestamp，且 blank display-name default 与 accepted behavior 分叉 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Owner contract review 3 | `00d9a11` | `REVISE` | non-rollback candidate uniqueness 与 display-name parity 已关闭，但精确保留已有 `CreatedAt` 与无条件 microsecond canonicalization 冲突 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Owner contract repair | `0caaea7` | `GO` | 仅对新分配时间使用 UTC microsecond canonicalization，并精确保留 legacy existing `CreatedAt`；更早的 outcome-proof、normalization、startup、File compatibility 和 caller-context finding 均已关闭 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Owner 实现审查 | `7dc70ed` | `REVISE` | candidate 形成前的 unsafe advisory-lock 与 current-row failure 被返回为 definite unavailable，并可能释放 uncertain session | Context-isolated gatekeeper / 2026-08-20 |
| S3 Owner 实现修复审查 | `3597b3f` | `REVISE` | production classification 与 termination 已修复，但测试未证明 terminated session 绝不会被 release 回连接池 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Owner 实现最终审查 | `0b85cc4` | `GO` | unsafe pre-candidate failure 返回 zero-candidate `unknown_outcome`，terminate 且不 release，并保留 definite PgError、retry-safe、corrupt-row、rollback 与 cleanup 分类。focused/full Store 与 race、全仓 build/test/vet、WebChat、44 项脚本测试、Compose、双语文档和 disposable real-PostgreSQL full/race 证据均通过 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Client 实现审查 | `1acdd2f` | `REVISE` | acquired-session `Begin` failure 与不可取消的 PostgreSQL command admission 违反已接受的 ownership/deadline 合同 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Client 实现最终审查 | `a4ddc83` | `GO` | context-aware admission 与精确 `Begin` classification 关闭两项 finding；修复候选的 full normal/race 和 disposable configured PostgreSQL full/race gate 均通过 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| 每个 repository 实现 | pending | pending | 迁移期间为每个已接受 repository 增加一行 | pending |
| S4 Store 删除 | pending | pending | pending | pending |
