# Store Credential Repository 设计

> 语言：[English](../../docs/store-credential-repository-design.md) | 简体中文

> 状态：S3 设计候选，2026-08-20。在独立、上下文隔离的设计审查获得 GO
> 之前，不授权编写 CredentialRepository 代码。

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
    DeleteCredentialSecret(context.Context, string) error
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
- Delete 要求 ref 已存在。缺失返回 typed `not_found`。成功时原子删除 secret，
  并且只追加一条仅包含 ref 的 `credential_secret.deleted` audit；不发出
  general event。
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
| Delete 使用空 ref 或不存在的 ref | `not_found` |
| Save 新 ref 或 overwrite 有效 existing ref | 已持久化的 normalized candidate |
| Backend/scan/decode failure | typed non-absence error |
| compatibility rule 之外的 persisted invariant violation | `corrupt` |
| 不安全的 submission/commit result | `unknown_outcome`；只有 Save 携带 save candidate |

确定的 save/delete failure 不返回 candidate 或 secret value。Store error string 与
public projection 绝不包含不受信 caller 提供的 ref value、kind、opaque value、
SQL、snapshot byte 或 backend cause。

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
| `credential_secret.delete` | write | transaction | ref barrier 后的最终缺失 |

Memory 在一把 lock 前后应用 context。secret mutation 与 audit append 都在该 lock
下完成。

File 使用已接受的 migrated admission 与 `runFileCommand`；credential path 不调用
legacy `persist()`。一个 command snapshot 完整 pre-state，提交一份 encoded
replacement，在确定 failure 时恢复全部 secret/audit/high-water state，并在未知的
rename/directory-sync outcome 上安装已接受的 fence。read 不能跨越该 fence。
startup 在加载 Memory 前验证 private secret wire representation。

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
Delete reconciliation 只有最终缺失时才成功。不同 row 或失败的 barrier 保持
unresolved，绝不报告成功。

## Vault 与 Consumer 迁移

`credential.Vault` 把 method context 传入每次 repository call，并在不做 string
matching 的情况下把 typed Store failure 映射为稳定的 seal/unseal error。`Delete`
只把 typed `not_found` 视为 idempotent success。

Seal 为 unresolved save 保留一个容量为一的 in-memory pending coordinator。它只
保留 plaintext fingerprint 与 sealed candidate，绝不保留 plaintext。同一个
logical request 执行 reconciliation 并恢复原 ref；不同 request 必须先解析该状态，
必要时删除已提交的 orphan candidate，然后才能生成另一个 ref。immediate success
或 final absence 会清理 pending state。Gateway lifecycle cancellation 清理 volatile
pending material。Repository candidate 与 error 绝不跨越 public API。

所有新的 Weixin QR credential 都调用 `CredentialVault.Seal`；只有 Seal 成功后，
Gateway 才把返回的 durable ref 赋给 binding。Notification 与 Syncer 使用其拥有的
request/work context 调用 `CredentialVault.Open`，不再读取
`CredentialSecret.Value`。operator 静态配置的 Weixin token 仍是配置输入，不进入
Store。

Gateway 必须检查 credential cleanup error。binding-start compensation 与 binding
revocation 可以在 binding operation 已完成后返回稳定 unavailable response；retry
会重复执行 idempotent Vault deletion。不返回任何 raw Store、Vault、envelope、ref、
token 或 backend error。ConnectorRepository 后续决定 binding 与 credential
lifecycle 是否组成一个 cross-repository command。

## 门禁与 Commit 边界

实现按该已接受设计拆成可独立审查的 commit：

1. interface、operation、三个 backend、private File wire format 与 compatibility
   validation 中的 repository behavior；
2. Vault reconciliation、legacy Weixin rewrap、consumer/context migration 与安全
   Gateway projection；以及
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
- Vault save/delete immediate 与 next-call reconciliation、不得第二次生成
  ref/value、pending cleanup、安全 typed error、wrong-key failure、legacy Weixin
  rewrap 与 non-Weixin plaintext rejection；
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
| Credential contract review 1 | pending | pending | pending | pending |
