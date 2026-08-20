# Store Client Repository 设计

> 语言：[English](../../docs/store-client-repository-design.md) | 简体中文

> 状态：基于已接受的 Owner 实现 `0b85cc4` 与 gate 记录 `fc5acba` 的 S3
> 设计候选。在这个精确合同获得 context-isolated 设计 GO 前，不授权 Client
> 代码实现。

## 边界修正

S0 把九个 legacy method 分配给 `ClientRepository`。Production 中只有一个
`SaveClient` caller：Gateway pairing 先保存带 token 的 client，再调用
`ClaimPairingCode`。这两个调用无法让 client、pairing state、audit 与 event
原子提交；claim 失败会留下 orphan client，且 PostgreSQL 目前会丢弃任一步的
写入错误。

因此本波次把 legacy `SaveClient` 与 `ClaimPairingCode(id, clientID)` 合并为
单一 `ClaimPairingCode(ctx, pairingID, client)` command。删除旧 standalone save
signature，不保留 test-only API；client normalization 变成 backend private
helper。当前 Store method catalog 从 141 降为 140，但 repository ownership
不变，也不迁移其他 repository。

## 接口

```go
type ClientRepository interface {
    GetClient(context.Context, string) (app.Client, bool, error)
    ListClients(context.Context) ([]app.Client, error)
    RevokeClient(context.Context, string) (app.Client, error)
    FindClientByTokenHash(context.Context, string) (app.Client, bool, error)
    TouchClient(context.Context, string) (app.Client, bool, error)
    SavePairingCode(context.Context, app.PairingCode) (app.PairingCode, error)
    GetPairingCode(context.Context, string) (app.PairingCode, bool, error)
    ClaimPairingCode(context.Context, string, app.Client) (app.PairingCode, app.Client, error)
}
```

`Store` 只嵌入一次该接口，并删除全部九个 legacy signature。每个 backend
独立断言 `ClientRepository`；不引入 dynamic repository lookup 或兼容接口。

## 稳定语义

- 每个方法把 caller context 与已接受的 read、write 或 transaction timeout
  组合；cancellation 与 timeout 保持 typed error。
- 普通 absence 是 `(zero, false, nil)`。read 的空 ID 与空 token hash 是普通
  absence。backend、scan、decode 与 row-iteration failure 绝不变成 absence
  或成功空列表。
- 列表顺序为 `CreatedAt DESC`，再按 `ID ASC`。
- `LastSeenAt` 与 `RevokedAt` 在 input、output、snapshot capture/load 全部 clone，
  backend 不与 caller 共享 mutable time pointer。
- 新分配时间使用 UTC PostgreSQL microsecond precision。已有 persisted
  `CreatedAt` 精确保留，包括 legacy File precision。revoke、touch、pairing
  creation 与 claim candidate 使用 per-record non-rollback high-water clock。
- trim client ID、owner ID、actor ID、name 与 token hash。空 client ID 由
  repository 生成；空 owner 默认 `app.DefaultOwnerID`；空 actor 默认 owner。
  新 client 必须有非空 name 与 token hash；client ID 和非空 token hash 唯一。
- `FindClientByTokenHash` 只返回未 revoked client。`TouchClient` 只更新并返回
  仍 active 的 client；missing 或并发 revoked 是普通 absence。它不写 audit/event。
- `RevokeClient` 要求 client 已存在，分配新 `RevokedAt`，并原子追加一条
  `client.revoked` audit 与 event。重复 revoke 是使用更晚 candidate 的新显式
  command，不复用 uncertain candidate。
- pairing ID 与 code hash 唯一。`SavePairingCode` 是 create-only，只接受没有
  claim field 的 normalized pending record，并要求非零 expiry。repository 用
  自己的 high-water time 替换 caller `CreatedAt`，并原子追加
  `pairing_code.created` audit/event。重复 ID/hash 为 `conflict`，已消费 code
  不能通过 save 重新打开。
- `GetPairingCode` 无副作用。已过期的 pending record 仍以 pending 持久化；
  caller 同时判断 status 与 expiry。claim 过期 code 返回 `conflict`，不做隐藏写入。
- `ClaimPairingCode` 要求 code 存在、pending、未过期，且 client ID/token hash
  全新。它分配 client creation time 与 pairing claim time，原子创建 client、
  标记 code claimed，并按 `client.saved`、`pairing_code.claimed` 顺序追加两组
  audit/event。任何 backend 都不能暴露或持久保留 partial command。

## Operation Registry

| Operation ID | Mode | Timeout | Reconciliation |
|---|---|---|---|
| `client.get` | read | read | self/barrier |
| `client.list` | read | read | none |
| `client.revoke` | write | transaction | exact client candidate |
| `client.find_token_hash` | read | read | none |
| `client.touch` | write | transaction | exact active client candidate |
| `pairing_code.save` | write | transaction | exact pairing candidate |
| `pairing_code.get` | read | read | self/barrier |
| `pairing_code.claim` | write | transaction | exact pairing/client candidate |

现有 timeout 配置与 PostgreSQL CI topology 不变。

## Durability 与 Outcome

Memory 在取得 lock 前与 lock 内应用 effective context；每个 command 在同一个
lock 内修改所有 owned record 和 lifecycle entry。

File 复用已接受的 admission、full-snapshot rollback、atomic replacement、
unknown-outcome fence 与 read reconciliation。client/pairing command 使用
`runFileCommand`，不再对这些 field 使用 legacy `persist()`。startup 校验 map
key/embedded-ID、非空 hash 唯一性、pairing status/claim-field 一致性及 claimed
code 的 client reference。legacy empty-token client 可加载，但不能认证；File
不 normalize corrupt persisted identity。

PostgreSQL command 获取 owned connection 并开启显式 transaction。client
command 使用 client-ID advisory lock；pairing command 使用 pairing-ID lock，
claim 还锁定生成的 client ID。resolution barrier read 显式使用
`READ COMMITTED`，先在独立 statement 取对应 lock，再 query。unique violation
映射为 `conflict`，其他 server rejection 映射为 `internal`。

获取 connection 后的 unsafe statement、transport、context 或 commit failure
返回 `unknown_outcome`，terminate owned session 且绝不 release。candidate 形成前
返回 zero candidate。Safe-to-retry 与 server-rejected failure rollback 后返回
definite typed error；rollback failure terminate 且不 release，并保留全部 cause。

reconciliation 只在全部 persisted field 精确相等时成功。pair creation 是
create-only，claim 是 single-use，因此 matching barrier 后的 exact candidate
可证明完整 atomic transaction。revoke/touch candidate 使用 non-rollback
high-water time。different record、absence 或 read failure 保持 unresolved，
绝不自动 retry 或报告 success。

## Gateway 迁移

- 所有调用使用 `r.Context()` 或 authentication request 的 owned context。
- client list failure 映射稳定 504 timeout 或 503 unavailable copy；revoke 仅对
  typed `not_found` 保留 404。
- pairing start 仅在 exact pairing candidate durable 或 reconciled 后披露 plaintext code。
- pairing claim 只执行一个 repository command，仅在 pairing/client candidate
  durable 或 reconciled 后披露 bearer token。
- bearer authentication 区分 invalid/revoked credential 与 Store failure。
  invalid/revoked 保持 401；lookup/touch timeout 为 504，其他 Store failure 为
  503；Touch 必须确认 client 仍 active。
- migrated Client path 不使用 `context.Background()`，也不忽略 repository error。

## Gate 与 Commit 边界

本实现是在双语 design commit 后的一个 behavior commit。Gate 要求：

- shared Memory/File success、absence、ordering/tie-break、pointer clone、
  cancellation、timeout、uniqueness、revoke/touch、expiry 与 atomic claim 测试；
- 覆盖 client、pairing、audit、event 的 File rollback/fence/reconciliation/
  restart/encryption/failure-injection/race evidence；
- PostgreSQL statement/commit classification、terminate-not-release、显式
  barrier、query/scan/rows propagation、unique conflict 与 atomic rollback；
- Gateway no-code/no-token-disclosure、atomic claim、safe error projection、
  authentication Store failure 与 owned-context 测试；
- one embedded repository、删除 `SaveClient`/legacy signature、无 ignored Client
  error、无 migrated background context 的 source guard；
- full Go test/build/vet、focused Store race、default File entry、WebChat
  test/build、44 项 Python script、default Compose、双语 docs CI，以及 disposable
  configured real-PostgreSQL full/race run。

在 exact Client 实现获得 independent context-isolated GO 前，不开始 Credential
或 Session 设计。

## 审查记录

| 审查 | Revision | 决定 | 证据 | Reviewer/date |
|---|---|---|---|---|
| Client contract | pending | pending | boundary、atomicity、outcomes、caller 与 gate | pending |
