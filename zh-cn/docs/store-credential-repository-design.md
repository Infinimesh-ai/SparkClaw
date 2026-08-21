# Store Credential Repository 设计

> 语言：[English](../../docs/store-credential-repository-design.md) | 简体中文

> 状态：S3 设计修订 8 在审查 1-7 返回 `REVISE` 后，于 2026-08-20 在
> `b0884f6` 获得独立 `GO`。该 GO 只授权下述 live Credential foundation checkpoint，随后迁移
> ConnectorRepository lifecycle，最后再执行 Credential integration gate。

## 边界与现有缺陷

S0 把三个方法分配给 CredentialRepository。它们持久化 opaque secret value
及其 kind/ref metadata；加密 sealing 属于 `credential` package，而不属于 Store。
当前边界存在多个相互独立的安全问题：

- File 先修改 Memory，随后丢弃所有 `persist()` error；
- PostgreSQL 丢弃 save 与 audit failure，把所有 get failure 映射为缺失，并且
  没有原子提交 secret 与 audit；
- Memory 在 overwrite 时替换 `CreatedAt`，PostgreSQL 则保留已存值但返回与之
  不一致的 caller candidate；
- Store method 丢弃 caller context；
- Telegram 写入 AES-GCM Vault envelope，但 Weixin QR 直接通过 Store 写入和读取
  raw bot token；以及
- `CredentialSecret.Value` 可以被 public JSON serialization，尽管任何 public
  response 都无权披露它。

本合同修复完整 credential 边界。生产切换还要求在 seal 任何 credential 前先持久化
NotificationBinding identity。该 lifecycle 由 ConnectorRepository 负责，不能偷偷
在 CredentialRepository 中增加第二份 binding log。因此路线图先冻结本合同，实现
已被生产使用的 Credential repository/Vault primitive，再迁移 ConnectorRepository，
最后审查集成后的 caller cutover。本合同不声称 Store 与外部 provider 之间存在新的
原子事务。

## 接口

```go
type CredentialRepository interface {
    SaveCredentialSecret(context.Context, CredentialSaveCommand) (app.CredentialSecret, error)
    GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error)
    DeleteCredentialSecret(context.Context, CredentialDeleteCondition) (app.CredentialSecret, error)
}
```

`Store` 只 embed 该接口一次，并删除三个 legacy signature。MemoryStore、FileStore
和 PostgresStore 各自直接 assertion 该接口。`credential.Vault` 只依赖这个
repository。还需要其他 state 的 consumer 使用自己的显式 composite；不引入
type assertion、dynamic repository lookup 或 optional capability。

## 稳定语义

- 每个方法先把 caller context 与已接受的 read 或 transaction timeout 组合。
  cancellation 和 timeout 保持为 typed Store error。
- standalone ref 与 candidate `Ref`/`Kind` field 会 trim。`Value` 是保持 byte
  不变的 opaque string：绝不 trim、记录日志、写入 error、audit field、event
  或 public JSON value。
- Save 只接受由 Store package 构造的 `CredentialSaveCommand`。create command
  要求记录缺失；replace command 携带 opaque expected condition，并要求 exact
  current version。两者都要求非空 ref、kind 与 value。Store 不生成 credential
  ref，并忽略 caller 的 `CreatedAt`/`UpdatedAt`。
- 新 ref 使用一个 UTC PostgreSQL-microsecond command timestamp 同时赋给
  `CreatedAt` 与 `UpdatedAt`。conditional replace 精确保留已有 `CreatedAt`，并通过 per-ref
  non-rollback high-water mark 赋予严格更新的 `UpdatedAt`。
- Save 是显式 create-or-conditional-replace，而不是 unlocked upsert。existing-row
  create 或 stale replace condition 返回 `conflict` 且不修改。成功 replace 保留
  current `CreatedAt`。成功时原子持久化完整 secret，并且只写入一条
  `credential_secret.saved` audit，其中仅包含 ref 与 kind；不发出 general event。
- Get 无副作用。空 ref 或不存在的 ref 是普通 `(zero, false, nil)` 缺失。
  backend、scan、decode、validation 或 context failure 绝不能变成缺失。
- Delete 是 exact conditional command。Vault 从先前由 Get 或 Save 返回的完整
  candidate 派生 opaque `CredentialDeleteCondition`。它包含 normalized ref 与覆盖
  candidate 每个 field 的 domain-separated SHA-256 version，但不包含 value 或可逆
  plaintext。空/无效 condition 返回 `invalid`，缺失返回 typed `not_found`，任一
  persisted-field digest 不匹配则返回 `conflict` 且不修改。成功时返回 exact deleted
  candidate，原子删除 secret，并且只追加一条仅包含 ref 的
  `credential_secret.deleted` audit；不发出 general event。
- `app.CredentialSecret.Value` 改为 `json:"-"`。File persistence codec 使用
  private wire type 保留 `value`；普通 JSON encoding、log、audit payload 和 HTTP
  response 仍保持 redacted。

### 确定性结果

validation 顺序为：effective context、standalone/candidate normalization、
candidate structural validation、target lookup、persisted-row validation，然后才是
business mutation。损坏的 stored row 不能被后续 not-found、overwrite 或 delete
结果隐藏。

| 方法或条件 | 精确 repository 结果 |
|---|---|
| Get 使用空 ref 或不存在的 ref | `(zero, false, nil)` |
| Save 使用无效 command 或 normalization 后为空的 ref/kind/value | `invalid` |
| Create 使用已存在的 ref | `conflict` 且不修改 |
| Replace 的 current row 缺失或与 expected version 不同 | `conflict` 且不修改 |
| Delete 使用无效 expected condition | `invalid` |
| Delete 使用有效 condition 但 ref 不存在 | `not_found` |
| Delete 的 current row 与 expected version 不同 | `conflict` 且不修改 |
| Create 缺失 ref 或条件 replace exact current row | 已持久化的 normalized candidate |
| Delete 精确匹配的 current row | exact deleted candidate |
| Backend/scan/decode failure | typed non-absence error |
| compatibility rule 之外的 persisted invariant violation | `corrupt` |
| candidate 形成后的不安全 submission/commit | `unknown_outcome`，携带 exact save/deleted candidate |

`NewCredentialCreate`、`NewCredentialReplace` 与
`NewCredentialDeleteCondition` 是仅有的 command/condition constructor。Create
保留 proposed secret；Replace 保留 proposed ref/kind/value 与从 prior row 派生的
opaque condition；任何 constructor 都不允许 caller 提供 timestamp 或 digest。
condition digest 使用 length-delimited normalized Ref/Kind、byte-exact Value，
并为两个时间使用 UTC civil component：signed year、month、day、hour、minute、
second 与 nanosecond。它不使用 `UnixNano`，因此 File 或 PostgreSQL 接受的每个 Go
`time.Time` 都有不溢出的表示。condition 只保留在 process memory，绝不
serialize、log、persist、audit、trace 或 project。

确定的 save/delete failure 不返回 candidate 或 secret value。Delete 尚未读取、
验证并匹配 current row 前发生的不安全 failure 也不返回 candidate。Store validation
error 不插入不受信 ref、kind、opaque value、SQL 或 snapshot byte。raw backend
cause 只保留在 internal error chain；Vault public projection 绝不披露它们。

## 兼容性与 Secret Format

Repository validation 把 `Value` 作为 opaque value，从不解析 Vault envelope。
File startup 与 PostgreSQL readiness 强制以下约束：

- trim 后 ref 非空；对于 File，map key 必须与 embedded `Ref` 完全相等；
- `CreatedAt` 与 `UpdatedAt` 都非 zero；clock rollback 属于兼容情况，因此不虚构
  两者的 chronological order；
- legacy blank kind/value row 仍可加载和读取，但新的 save 不得创建它们；以及
- 除上述显式 ref/kind projection 外，所有 scalar value 都精确复制。

Vault 负责 format compatibility。AES-256-GCM envelope version 1 仍是唯一正常
format。只有 exact legacy kind `openclaw-weixin-bot-token` 可以携带迁移前的 raw
token。首次 `Open` 时，Vault 在同一 ref 上 seal 该值，持久验证或 reconcile
replacement，清零临时 byte copy，然后才返回 plaintext。所有其他 kind 的 raw
value 仍作为 invalid/unauthenticated envelope fail closed。新的 Weixin credential
绝不使用 legacy representation。

当 File encryption 未配置时，读取 encrypted File state envelope 必须在 Snapshot
decode 前拒绝。不能因为忽略未知 JSON field 而把它接受为空 Snapshot，也不能由
下一个 command 覆盖。已配置 encryption 以及 plaintext snapshot 的兼容行为保持
不变。

## 操作与持久性规则

| Operation ID | Mode | Timeout | Reconciliation |
|---|---|---|---|
| `credential_secret.save` | write | transaction | ref barrier 后的 exact candidate |
| `credential_secret.get` | read | read | 解析 command 时使用 ref barrier |
| `credential_secret.delete` | write | transaction | ref barrier 后的缺失、exact prior candidate 或 conflicting replacement |

Memory 在一把 lock 前后应用 context。secret mutation 与 audit append 都在该 lock
下完成。

File 使用已接受的 migrated admission 与 `runFileCommand`；credential path 不调用
legacy `persist()`。一个 command snapshot 完整 pre-state，提交一份 encoded
replacement，在确定 failure 时恢复 secret 与 audit state，但刻意保留 failed
candidate 的 non-rollback high-water mark；并在未知的 rename/directory-sync
outcome 上安装已接受的 fence。read 不能跨越该 fence。startup 在加载 Memory
前验证 private secret wire representation。

PostgreSQL save/delete 先为 high-water ownership 获取一个 context-aware、容量为一
的 process admission，再取得 owned connection 并开启 explicit transaction。
它们获取由 ref 派生的 advisory transaction lock，读取 current row，形成 candidate，
在该 lock 下验证 create absence 或 replace/delete condition，并在该 transaction
内写入 secret 与 audit。resolution read 显式使用 `READ COMMITTED`，在一条
statement 中获取同一 advisory lock，并在后一条 statement 中 query。除适用的
uniqueness/business code 外，server rejection 是确定的 `internal`；safe-to-retry
transport failure 是确定 failure。connection acquisition 之后发生的 unsafe
statement、context 或 commit outcome 必须 terminate 而不 release，并返回
`unknown_outcome`。rollback failure 同样 terminate 而不 release，并保留全部 cause。

Save reconciliation 只有在每个 persisted field 都与 exact candidate 相等时才成功。
Delete reconciliation 给出三种不同 proof：最终缺失表示目标 command 已完成；
exact prior candidate 表示没有 commit，可以再次条件删除；不同 row 是
`conflict`，pending command 绝不能删除它。失败的 barrier 保持 unresolved。
任何 reconciliation 结果都不能由未加锁 read 推断。

## Vault 与 Consumer 迁移

Vault 把每个 method context 传入每次 repository call，并且不做 string matching，
映射 typed Store failure。`CredentialVault.Seal` 携带显式、有界、非 secret 的
one-shot binding identity：

```go
type CredentialVault interface {
    Ready() error
    BindLifecycle(context.Context)
    Seal(context.Context, string, string, []byte) (string, error) // binding ID, kind, plaintext
    Open(context.Context, string) ([]byte, error)
}

type CredentialLifecycleRecovery interface { // 随 Connector proof path 引入
    Delete(context.Context, string) error // ref
    AbortSeal(context.Context, string, string) error // binding ID, kind；随 Connector proof path 增加
}
```

Connector recovery 接收由这两个 interface 组成的显式 consumer-owned composite，
绝不通过 type assertion 或 optional capability 发现 recovery。foundation 只定义和
使用 `CredentialVault`；`CredentialLifecycleRecovery`、两个 concrete method 与各自
第一个 durable proof-bearing caller 在 Connector wave 同时落地。foundation repository
仍实现 `DeleteCredentialSecret`：Vault 的 private pending create/orphan reconciliation
使用该方法，但不暴露 public delete capability。

seal identity 是 adapter 启动或 Vault 接收 plaintext 前已经持久提交的 immutable
binding ID。它会 trim、必须非空、最多 256 bytes。已接受的 ConnectorRepository
合同必须保证这个顺序；当前 best-effort `SaveNotificationBinding` 并不能保证。
binding record 仍是 identity persistence 与 terminal reuse prevention 的权威；
Vault 不创建额外 identity log、receipt、audit 或 public field。Vault 使用
HMAC-SHA-256(master key, `sparkclaw-credential-ref-key-v1`) 派生独立 ref key，再以 domain
`sparkclaw-credential-ref-v1` 对 length-delimited binding ID 计算 base64url
HMAC-SHA-256；每个新 ref 精确为 `cred_` 加该完整 HMAC 的无 padding base64url
encoding。kind 不参与派生，因此相同 binding ID 改用另一 kind 时仍指向同一
row，并被检测为冲突，而不是生成另一个 ref。

Seal 先解析 pending work，再派生 ref 并读取它。缺失时使用 create command。已存在
且 authenticated envelope 的 kind 相同、plaintext constant-time-equal 时，表示
completed replay，直接返回同一 ref，不再 save/audit。其他任何 existing row 都是
稳定 `credential_invalid`，绝不覆盖。因此 immediate success 可以清理 volatile
state：caller 从 durable nonterminal binding 恢复同一 ID 后，即使跨 process restart
也能重建 exact ref；另一 binding ID 下相同 kind/plaintext 仍拥有独立 ref，不能共享
或清理第一份 credential。只存在于 process memory 的 ID 没有 replay 保证。
foundation checkpoint 可以在显式 non-final gate 下暂时保留该既有 failure window；
最终接受的 production path 不得在该状态调用 Seal。

这刻意是 live-binding replay contract，而不是 durable generic operation ledger。
confirmed public Delete 或 AbortSeal 必须与 durable terminal binding transition 配对。
ConnectorRepository 保留该 record、拒绝复用 ID，并阻止 terminal ID 再次进入 Start、
Poll 或 Seal。Delete 不接受 caller-supplied operation ID，因此不会在 stale identity
下 replay 到另一 ref。Audit 不是 lifecycle receipt，也不能替代原子性。

### Durable Binding 前置条件

在单独审查的 ConnectorRepository 合同与实现满足以下全部规则前，不允许接受最终的
restart-safe Credential lifecycle：

1. Gateway 创建 fresh immutable binding ID，并在调用 adapter 前 durably commit
   `starting` binding。binding create 的 definite 或 unresolved failure 都不能进入
   provider verification 或 Seal。
2. Telegram 只能从 exact `starting` version 调用 Seal。Seal 成功后，以返回 ref
   conditional transition 到 `active`。
3. Weixin 先把 `waiting_scan`/`waiting_confirm` conditional advance 到不可 poll 的
   `credential_pending` version，持久化 non-secret provider metadata，并确认 transition
   后才把返回 token 交给 Seal。因此 compensated 或 restarted record 不能再次 Poll 并
   用同一 retired ID Seal。
4. 未知的 `active` transition 必须在 binding Store barrier 后 reconcile。exact
   `active` state 证明成功；只有 exact pre-active state 才证明允许 AbortSeal；
   unresolved 或不同 state 绝不能 cleanup。
5. connector worker 启动或 Gateway listen 前，recovery 扫描 `starting`、
   `credential_pending` 与 `revoking` record，按情况调用
   `AbortSeal(bindingID, kind)` 或 `Delete(ref)`，再 conditional commit `failed` 或
   `revoked`。recovery failure 保留不可 poll state，并使 connector readiness 失败，
   不能激活 binding 或放弃 cleanup ownership。
6. revoke 先提交保留 ref 的 non-active `revoking` state，再删除 exact credential，
   最后提交 `revoked`。restart 只重复 pending delete。terminal binding record 保留，
   binding ID 永不复用。

`AbortSeal` 从 binding ID 派生 deterministic ref，要求 exact expected kind，认证已有
Vault envelope，并 conditional delete exact candidate。absence 是 success。不同 kind、
invalid envelope、replacement 或 unresolved Store result 返回稳定 unavailable，绝不
删除。该可重建 cleanup 不依赖 Vault volatile pending coordinator，但授权只能来自
已证明的 Connector pre-active state。

该依赖通过以下顺序实现，不引入 dead code 或循环 gate：

1. **Credential foundation checkpoint。** 本设计 GO 后，迁移三个 repository
   method、三个 backend、File codec/fail-closed loading、deterministic Vault Seal、
   private conditional repository delete/rewrap、lifecycle binding 与全部当前
   credential caller。
   Telegram 与 Weixin 使用 Seal；Notification 与 Syncer 使用 Open。
   `CredentialLifecycleRecovery` 刻意不在该 checkpoint 定义或实现。legacy unconditional binding save
   的 mismatch 或 failure 属于 ambiguous，返回稳定 unavailable，但不删除 sealed
   credential。带 ref 的 legacy revoke 同样无法证明 binding transition 已 durable：
   foundation 取消本地工作、保留 credential，并返回稳定 unavailable，不报告最终
   revoke success，也不调用 public Delete。该 checkpoint 修复 Store durability 与
   plaintext handling，但不是
   Credential implementation GO：当前 Connector method 尚不能跨 restart 证明
   pre-Seal identity 或 safe compensation。
2. **ConnectorRepository migration。** focused foundation review 必须证明 live
   primitive 与 backend contract 符合本设计；它可以授权 Connector wave，而不宣称
   Credential 已完成。Connector 随后迁移自己的 interface/backend/caller，并增加
   上述 durable state、barrier、startup recovery 与 terminal ID retention。该
   repository wave 同时增加 public Delete、AbortSeal 与各自第一个 production caller；
   revoking barrier 授权 Delete，exact pre-active barrier 授权 AbortSeal；不修改
   Credential repository method。
3. **最终 Credential integration gate。** Connector implementation GO 后，重新运行
   完整 Credential/Connector failure-window 与 restart matrix，删除 process-local
   Seal identity 的所有临时 allowance，并审查 exact integrated candidate。只有该
   决定才是 Credential implementation GO，并解锁 SessionRepository。

每个 code step 只激活一个 Store repository implementation。foundation checkpoint
不是 partial repository scaffold：Connector 代码开始前，三个 Credential backend 与
全部可安全调用的 credential consumer 都已 live。public Delete 与 AbortSeal 是刻意
延后的 lifecycle capability；Connector 把 interface、method 与 durable proof-bearing
caller 一起增加，因此它们既不会成为 dead code，也不能在缺少所需授权时调用。

Vault 使用一个容量为一的 in-memory command coordinator，并定义三个互斥 pending
mode：

1. **Create** 保留 binding ID、kind、plaintext fingerprint、ref、sealed payload，
   以及 Store 返回的 normalized candidate（如有），绝不保留 plaintext。exact
   committed candidate 完成 matching operation。不同 operation 继续前，先条件删除
   exact undisclosed orphan。absence 证明 rollback，并使用相同 create payload
   重试。derived ref 上的不同 row 是 identity conflict，绝不删除或覆盖。
2. **Delete/cleanup** 由 ref 标识，并保留 opaque delete condition，绝不保留
   candidate value。absence 证明完成；exact prior version 证明 rollback，允许不经
   unlocked get/delete gap 再执行一次 conditional delete；replacement version 是
   conflict，pending command 绝不删除它。
3. **Legacy rewrap** 由 ref 与 opaque raw-prior condition 标识；它绝不是 orphan，
   也绝不进入 cleanup deletion。它只保留 sealed replacement payload 与 Store 返回
   的 normalized encrypted candidate（如有）。exact encrypted candidate 证明完成；
   exact raw prior 证明 rollback，并允许用相同 sealed payload 再次 conditional
   replace；row 缺失时清理 pending 并返回 unseal failure；同 ref/kind/plaintext 的
   另一个 authenticated envelope 也证明 rewrap 已完成；其他 replacement 是 conflict，
   既不覆盖也不删除。read/barrier failure 保留 pending。用于比较 authenticated
   replacement 的临时 plaintext 在释放 coordinator 前清零。

之后每个 Vault mutation 都先解析对应 pending mode。未解析的 create cleanup 或
delete 会阻止生成另一个 ref；未解析 rewrap 会阻止 mutation，但绝不删除 active
credential。restart 后由 Connector recovery 从 durable binding ID 重建 cleanup，
不依赖 volatile state。immediate success 与最终解析按 generation 清理 matching
state；迟到 cleanup 不能清理 replacement generation。Repository candidate 与 error
绝不跨越 public API。

composition root 构造唯一 production Vault。启动 connector worker 前，
`gatewayServices.Start` 调用 `BindLifecycle(ctx)`。每次 bind 增加 generation 并启动
一个 cancellation watcher；只有 generation 仍匹配时，cancellation 才清理 pending
material。rebind 先使旧 generation 失效。Server 不构造 fallback Vault。

foundation 把所有新的 Weixin QR credential 迁移到 `CredentialVault.Seal`，并让
Notification 与 Syncer 使用其拥有的 request/work context 调用
`CredentialVault.Open`，不再读取 `CredentialSecret.Value`。Connector wave 之后，
Seal 接收 durably persisted immutable binding ID：Gateway 先持久化
`credential_pending`，随后只通过 conditional `active` transition 赋予返回的 durable
ref。operator 静态配置的 Weixin token 仍是配置输入，不进入 Store。

Connector 获得 durable authorization 后，Gateway 必须检查 credential cleanup error。
在同一 running process 内，Vault
coordinator 保留 conditional command material 直到解析，后续 mutation 必须先解析。
跨 cancellation 或 restart 时，durable Connector `starting`、
`credential_pending` 或 `revoking` record 是 cleanup owner；startup recovery 必须在
worker 前解析它。binding-start compensation 与 revoke 可以在 durable record 仍为
non-active/retryable 时返回稳定 unavailable。foundation 期间，ambiguous legacy binding
save 与所有带 ref 的 legacy revoke 都保留 credential 并返回稳定 unavailable；此时尚无
public cleanup method。不返回任何 raw Store、Vault、envelope、
ref、token 或 backend error，也不把 Audit 或 volatile Vault state 当作
cross-repository commit record。

### 稳定 Error Projection

Vault 绝不返回 raw Store error。它只在 `Unwrap` 后保留 cause，并且不做 string
matching，按以下规则映射结果：

| 来源 | Vault code | Public/worker behavior |
|---|---|---|
| 无效 binding ID/kind/value，或 live binding-ID input conflict | `credential_invalid` | HTTP 400；稳定 validation copy |
| caller cancellation | `credential_canceled` | response 仍可写时返回 HTTP 408；worker 停止其 owned item |
| Store timeout/unavailable/durability/unknown/internal/corrupt、未解析 conditional conflict 或本地 random/encryption failure | `credential_unavailable` | HTTP 503；worker 返回 retryable unavailable，不把它当作 absence |
| key 缺失或不可用 | `credential_key_unavailable` | HTTP 503；connector unavailable |
| Open 时 ref 缺失、envelope 无效、wrong key/AAD 或 authentication failure | `credential_unseal_failed` | 稳定 credential failure；绝不包含 ref、kind、value 或 cause |

随 Connector 引入后，public `Delete` 只把 typed Store `not_found` 视为 idempotent
success。它只在 Store 中 kind 为旧 Weixin kind 时接受已发布的旧 Weixin QR
`provider:openclaw-weixin-qr:<bindingID>` 命名空间；在 Connector 的 durable
`revoking`、匹配的 binding-ID ref 后缀与全局唯一 ref 证明下，条件删除精确的迁移前
明文值或已认证的重新封装 envelope。其他 provider ref 或明文 ref 一律 fail closed。
Notification 与
Syncer 区分 unavailable 和正常 credential absence，只披露稳定 Vault message，绝不
把 raw Store diagnostic 复制到 binding `last_error`。

## 门禁与 Commit 边界

实现按该已接受设计拆成可独立审查的 checkpoint：

1. Credential foundation commit：interface、operation、三个 backend、private File
   wire format 与 compatibility validation；随后实现 live Vault seal identity、
   coordinator、lifecycle binding、legacy Weixin rewrap、consumer/context
   migration 与安全 Gateway projection；不包括 public Delete 或 AbortSeal。File
   encrypted-envelope fail-closed defect
   可为便于 bisect 保持为单独 commit。
2. foundation checkpoint review 授权单独文档化的完整 ConnectorRepository 设计与
   实现。该 wave 增加 public Delete、AbortSeal 与各自 exact barrier-proven recovery caller，
   但不把 Credential 标记为 GO。
3. Connector GO 后，对 exact integrated Credential/Connector candidate 做最终
   Credential implementation review。

实现门禁要求：

- shared Memory/File create/conditional-replace/delete、conflict、absence、
  validation precedence、timestamp/high-water、cancellation/timeout、audit
  atomicity、digest time extreme 与 redaction；
- File rollback、fence、final reconciliation、encrypted/plain restart、corrupt
  state、encrypted-without-key rejection、failure injection 与 race evidence；
- PostgreSQL acquire/begin/statement/commit/rollback classification、
  terminate-not-release、context-aware admission、barrier isolation、scan
  propagation、atomic audit、startup validation 与 real-DSN evidence；
- foundation checkpoint 证明其实现的每个 method 都有 production caller，三个
  Credential backend 同步迁移，并证明 ambiguous legacy binding-save result 绝不
  调用 Delete 或 AbortSeal；带 ref 的 legacy revoke 在缺少 durable proof 时绝不删除
  credential 或报告最终成功；repository Delete 只由 private Vault reconciliation 使用，
  且不存在 public recovery interface；它记录尚未迁移的 Connector restart identity，而不声称
  虚假的最终 GO；
- Connector gate 证明 public Delete 与 AbortSeal 和各自第一个 production caller 同时
  引入，并分别只在 exact revoking 或 pre-active barrier result 后调用；concurrent 或
  unresolved binding state 绝不授权 credential deletion；
- final gate 证明 Vault 从 durable recovered nonterminal binding 进行 deterministic replay、caller
  boundary 拒绝 process-local ID、live-binding different-input conflict、独立
  identical plaintext binding、create/delete immediate 与 next-call
  reconciliation、conditional orphan cleanup、restart 重建 AbortSeal、legacy rewrap
  所有 state transition 且不执行 orphan deletion、generation-safe lifecycle
  cleanup、安全 typed error、wrong-key failure 与 non-Weixin plaintext rejection；
- failure-injection 与 restart test 覆盖 durable binding create 后、Seal 后但 active
  transition 前、unknown active transition 后，以及 revoke 后但 Delete/final revoke
  前的 crash；Weixin 证明 durable `credential_pending` record 不可 poll，terminal
  compensation 不能再次使用同一 ID 进入 Poll 或 Seal；
- Gateway/Notification/Syncer test 证明 adapter 不能替换 binding ID、Seal 失败时没有
  active binding、没有 raw Store access、owned context propagation、cleanup error
  handling、worker 前完成 startup recovery，并且不披露 token/ref/error；
- source guard：只 embed 一个 repository、exact signature、opaque command factory
  与三个 implementation、无 legacy method、无 ignored result、无已迁移
  `context.Background()`、
  `CredentialSecret.Value` JSON redaction，以及 Vault 的最小 repository dependency；
  Connector recovery 使用包含 `CredentialLifecycleRecovery` 的显式 composite，且没有
  type assertion；以及
- 完整 Go test/build/vet、聚焦 Store/credential/Gateway/Weixin race、默认 File
  production entry、WebChat test/build、44 个 Python script test、default Compose、
  双语 docs CI，以及一次性配置的真实 PostgreSQL full 与 race run。现有 PostgreSQL
  CI topology 和 DSN skip behavior 保持不变。

本设计获得 GO 后，只授权 Credential foundation checkpoint。其 focused review 可以
授权 ConnectorRepository 设计与实现，但不能宣称 Credential 已完成。Connector 获得
GO 且恢复后的 exact integrated Credential candidate 获得自己的 context-isolated
implementation GO 前，不开始 SessionRepository 设计。

## 审查记录

| 审查 | 修订 | 结论 | 证据 | 审查人/日期 |
|---|---|---|---|---|
| Credential contract review 1 | `de4cd93` | `REVISE` | Seal 缺少 logical operation identity；ref-only Delete 可能删除 replacement 且没有 pending cleanup owner；File high-water rollback 与 non-rollback timestamp 冲突；lifecycle 与 public error mapping 不完整 | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 2 | `1d646f0` | `REVISE` | immediate success 清除全部 replay identity；legacy rewrap 缺少独立 non-orphan state machine；delete digest 使用可能溢出的 UnixNano encoding | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 3 | `b6def5d` | `REVISE` | deterministic ref 只在 row 存在时保留 identity，但文档仍承诺 generic post-delete operation-ID conflict；Delete 也接受没有 durable binding 的可复用 caller operation ID | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 4 | `30cbf24` | `REVISE` | Gateway 仅在 memory 保留 binding ID，直到 adapter Start/Seal 后才持久化，因此 crash 会丢失 replay identity 并遗留 orphan credential；compensated Weixin waiting record 也没有阻止 Poll/Seal reuse 的 durable terminal transition | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 5 | `4d54acf` | `REVISE` | Credential code 被阻塞到 Connector GO，但 Connector recovery 已要求尚未获授权的 AbortSeal，形成 sequencing cycle；旧文本仍把 cleanup 单独交给 volatile Vault state | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 6 | `3c86739` | `REVISE` | foundation 在没有 Connector exact pre-active proof 时调用 AbortSeal，因此并发 Weixin activation 可能使 stale compensation 删除 active credential；路线图也重复安排了 Connector | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 7 | `8ef063f` | `REVISE` | 路线图仍把 AbortSeal 分给 foundation，legacy revoke 也可能在没有 durable binding-transition proof 时删除 credential；推迟该调用还暴露出 public Delete 成为 dead code | Context-isolated gatekeepers / 2026-08-20 |
| Credential contract review 8 | `b0884f6` | `GO` | foundation 不暴露 public cleanup；private repository delete 有 live reconciliation caller；ambiguous start/revoke 保留 credential；Connector 只把 Delete/AbortSeal 与 exact durable barrier 及 live caller 同时引入；双语路线图一致 | Context-isolated gatekeeper / 2026-08-20 |
