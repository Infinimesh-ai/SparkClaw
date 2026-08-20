# Store Credential Repository 设计

> 语言：[English](../../docs/store-credential-repository-design.md) | 简体中文

> 状态：S3 设计修订 2，2026-08-20。审查 1 返回 `REVISE`；在独立、
> 上下文隔离的设计审查获得 GO 之前，不授权编写 CredentialRepository 代码。

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

本波次修复完整 credential 边界。它不迁移 NotificationBinding persistence，
也不把 credential 与 binding 变成新的 cross-repository transaction；后续由
ConnectorRepository 负责该 command。

## 接口

```go
type CredentialRepository interface {
    SaveCredentialSecret(context.Context, app.CredentialSecret) (app.CredentialSecret, error)
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
- 新 save 要求非空 ref、kind 与 value。Store 不生成 credential ref，并忽略
  caller 的 `CreatedAt`/`UpdatedAt`。
- 新 ref 使用一个 UTC PostgreSQL-microsecond command timestamp 同时赋给
  `CreatedAt` 与 `UpdatedAt`。overwrite 精确保留已有 `CreatedAt`，并通过 per-ref
  non-rollback high-water mark 赋予严格更新的 `UpdatedAt`。
- Save 是显式 upsert。它原子持久化完整 secret，并且只写入一条
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
| Save 使用 normalization 后为空的 ref/kind，或空 value | `invalid` |
| Delete 使用无效 expected condition | `invalid` |
| Delete 使用有效 condition 但 ref 不存在 | `not_found` |
| Delete 的 current row 与 expected version 不同 | `conflict` 且不修改 |
| Save 新 ref 或 overwrite 有效 existing ref | 已持久化的 normalized candidate |
| Delete 精确匹配的 current row | exact deleted candidate |
| Backend/scan/decode failure | typed non-absence error |
| compatibility rule 之外的 persisted invariant violation | `corrupt` |
| candidate 形成后的不安全 submission/commit | `unknown_outcome`，携带 exact save/deleted candidate |

`NewCredentialDeleteCondition` 是唯一 condition constructor。digest 使用
length-delimited normalized Ref/Kind、byte-exact Value，以及 UTC UnixNano
CreatedAt/UpdatedAt。condition 只保留在 process memory，绝不 serialize、log、
persist、audit、trace 或 project。

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
并在该 transaction 内写入 secret 与 audit。resolution read 显式使用
`READ COMMITTED`，在一条 statement 中获取同一 advisory lock，并在后一条 statement
中 query。除适用的 uniqueness/business code 外，server rejection 是确定的
`internal`；safe-to-retry transport failure 是确定 failure。connection acquisition
之后发生的 unsafe statement、context 或 commit outcome 必须 terminate 而不 release，
并返回 `unknown_outcome`。rollback failure 同样 terminate 而不 release，并保留
全部 cause。

Save reconciliation 只有在每个 persisted field 都与 exact candidate 相等时才成功。
Delete reconciliation 给出三种不同 proof：最终缺失表示目标 command 已完成；
exact prior candidate 表示没有 commit，可以再次条件删除；不同 row 是
`conflict`，pending command 绝不能删除它。失败的 barrier 保持 unresolved。
任何 reconciliation 结果都不能由未加锁 read 推断。

## Vault 与 Consumer 迁移

Vault 把每个 method context 传入每次 repository call，并且不做 string matching，
映射 typed Store failure。`CredentialVault` 修改 mutation method，使其携带显式、
有界、非 secret 的 operation identity：

```go
type CredentialVault interface {
    Ready() error
    BindLifecycle(context.Context)
    Seal(context.Context, string, string, []byte) (string, error) // operation ID, kind, plaintext
    Open(context.Context, string) ([]byte, error)
    Delete(context.Context, string, string) error // operation ID, ref
}
```

operation ID 会 trim、必须非空、最多 256 bytes，只保留在 process memory，绝不
log、persist、audit 或 project。Binding flow 从已创建的 binding ID 与 exact action
（`seal`、`start-compensation` 或 `revoke`）派生它。只有 operation ID、kind 和
constant-time plaintext fingerprint 全部相同时，两次调用才属于同一 logical
request。相同 operation ID 使用不同输入是稳定 conflict；不同 operation ID 下即使
kind/plaintext 相同也相互独立，绝不能共享 ref 或清理对方的 candidate。

Vault 使用一个容量为一的 in-memory command coordinator，覆盖 unresolved Seal、
conditional Delete、orphan cleanup 与 legacy Weixin rewrap。pending save 保留
operation ID、kind、plaintext fingerprint 和 sealed candidate，绝不保留 plaintext。
pending delete 保留 operation ID、ref 与 opaque delete condition，绝不保留 candidate
value。之后每个 Vault mutation 都先解析 pending state：

- exact committed save 只返回给 matching operation；不同 operation 继续前，Vault
  先条件删除该 exact orphan；
- save absence 证明 rollback 并清理 pending；
- delete absence 证明完成并清理 pending；
- exact prior delete candidate 证明 rollback，并允许再次执行一次 conditional
  delete，不引入未加锁 get/delete gap；以及
- replacement row 是 conflict，pending command 绝不删除它；在返回稳定 failure
  前清理该 stale command。

未解析的 cleanup 保持 pending，并阻止生成另一个 ref。因此后续 binding start
无需接收或披露旧 ref，也能完成此前 compensation。immediate success 与最终解析
按 generation 清理匹配 state；迟到 cleanup 不能清理 replacement generation。
Repository candidate 与 error 绝不跨越 public API。

composition root 构造唯一 production Vault。启动 connector worker 前，
`gatewayServices.Start` 调用 `BindLifecycle(ctx)`。每次 bind 增加 generation 并启动
一个 cancellation watcher；只有 generation 仍匹配时，cancellation 才清理 pending
material。rebind 先使旧 generation 失效。Server 不构造 fallback Vault。

所有新的 Weixin QR credential 都使用 binding seal operation ID 调用
`CredentialVault.Seal`；只有 Seal 成功后，Gateway 才把返回的 durable ref 赋给
binding。Notification 与 Syncer 使用其拥有的 request/work context 调用
`CredentialVault.Open`，不再读取
`CredentialSecret.Value`。operator 静态配置的 Weixin token 仍是配置输入，不进入
Store。

Gateway 必须检查 credential cleanup error。binding-start compensation 与 binding
revocation 可以在 binding operation 已完成后返回稳定 unavailable response；Vault
coordinator 持续拥有 conditional deletion 直到解析，后续 mutation 必须先解析它。
不返回任何 raw Store、Vault、envelope、ref、token 或 backend error。
ConnectorRepository 后续决定 binding 与 credential lifecycle 是否组成一个
cross-repository command。

### 稳定 Error Projection

Vault 绝不返回 raw Store error。它只在 `Unwrap` 后保留 cause，并且不做 string
matching，按以下规则映射结果：

| 来源 | Vault code | Public/worker behavior |
|---|---|---|
| 无效 operation ID/kind/value，或 operation-ID input conflict | `credential_invalid` | HTTP 400；稳定 validation copy |
| caller cancellation | `credential_canceled` | response 仍可写时返回 HTTP 408；worker 停止其 owned item |
| Store timeout/unavailable/durability/unknown/internal/corrupt 或未解析 conditional conflict | `credential_unavailable` | HTTP 503；worker 返回 retryable unavailable，不把它当作 absence |
| key 缺失或不可用 | `credential_key_unavailable` | HTTP 503；connector unavailable |
| Open 时 ref 缺失、envelope 无效、wrong key/AAD 或 authentication failure | `credential_unseal_failed` | 稳定 credential failure；绝不包含 ref、kind、value 或 cause |

`Delete` 只把 typed Store `not_found` 视为 idempotent success。Notification 与
Syncer 区分 unavailable 和正常 credential absence，只披露稳定 Vault message，绝不
把 raw Store diagnostic 复制到 binding `last_error`。

## 门禁与 Commit 边界

实现按该已接受设计拆成可独立审查的 commit：

1. interface、operation、三个 backend、private File wire format 与 compatibility
   validation 中的 repository behavior；
2. Vault operation identity、save/delete coordinator、lifecycle binding、legacy
   Weixin rewrap、consumer/context migration 与安全 Gateway projection；以及
3. 独立的 File encrypted-envelope fail-closed defect fix；若为了便于 bisect 而与
   repository behavior 分离，则使用该 commit。

实现门禁要求：

- shared Memory/File success、overwrite、absence、validation precedence、
  timestamp/high-water、cancellation/timeout、audit atomicity 与 redaction；
- File rollback、fence、final reconciliation、encrypted/plain restart、corrupt
  state、encrypted-without-key rejection、failure injection 与 race evidence；
- PostgreSQL acquire/begin/statement/commit/rollback classification、
  terminate-not-release、context-aware admission、barrier isolation、scan
  propagation、atomic audit、startup validation 与 real-DSN evidence；
- Vault save/delete immediate 与 next-call reconciliation、same/different operation
  identity、独立 operation 下的 identical plaintext、conditional orphan cleanup、
  不得第二次生成 ref/value、generation-safe lifecycle cleanup、安全 typed error、
  wrong-key failure、legacy Weixin rewrap 与 non-Weixin plaintext rejection；
- Gateway/Notification/Syncer test，证明 Seal 失败时没有 active binding、没有 raw
  Store access、owned context propagation、cleanup error handling，并且不披露
  token/ref/error；
- source guard：只 embed 一个 repository、exact signature 与三个 implementation、
  无 legacy method、无 ignored result、无已迁移 `context.Background()`、
  `CredentialSecret.Value` JSON redaction，以及 Vault 的最小 repository dependency；
  以及
- 完整 Go test/build/vet、聚焦 Store/credential/Gateway/Weixin race、默认 File
  production entry、WebChat test/build、44 个 Python script test、default Compose、
  双语 docs CI，以及一次性配置的真实 PostgreSQL full 与 race run。现有 PostgreSQL
  CI topology 和 DSN skip behavior 保持不变。

在 exact Credential implementation 获得独立、上下文隔离的 GO 之前，不开始
SessionRepository 设计。

## 审查记录

| 审查 | 修订 | 结论 | 证据 | 审查人/日期 |
|---|---|---|---|---|
| Credential contract review 1 | `de4cd93` | `REVISE` | Seal 缺少 logical operation identity；ref-only Delete 可能删除 replacement 且没有 pending cleanup owner；File high-water rollback 与 non-rollback timestamp 冲突；lifecycle 与 public error mapping 不完整 | Context-isolated gatekeeper / 2026-08-20 |
| Credential contract review 2 | pending | pending | pending | pending |
