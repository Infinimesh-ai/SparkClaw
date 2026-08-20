# Store Client Repository 设计

> 语言：[English](../../docs/store-client-repository-design.md) | 简体中文

> 状态：基于已接受 Owner 实现 `0b85cc4` 的第二个 S3 设计修复候选。
> `bae3623` 与 `9ff7c14` 审查结论均为 REVISE。在本修复合同获得 fresh
> context-isolated 设计 GO 前，不授权 Client 代码实现。

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
- Client `LastSeenAt`/`RevokedAt` 与 pairing `ClaimedAt` 在 input、output、event
  payload、snapshot capture/load 全部 clone；backend 不与 caller 或其他 stored
  record 共享 mutable time pointer。
- 新分配时间使用 UTC PostgreSQL microsecond precision。已有 persisted
  `CreatedAt` 精确保留，包括 legacy File precision。revoke、touch、pairing
  creation 与 claim candidate 使用 per-record non-rollback high-water clock。
  每个 backend 从 client created/last-seen/revoked 与 pairing created/claimed
  timestamp 初始化 mark，绝不使用未来 expiry deadline。
- trim client ID、owner ID、actor ID、name 与 token hash。空 client ID 由
  repository 生成；空 owner 默认 `app.DefaultOwnerID`；空 actor 默认 owner。
  新 client 必须有非空 name 与 token hash；client ID 和非空 token hash 唯一。
- `FindClientByTokenHash` 只返回未 revoked client。`TouchClient` 只更新并返回
  仍 active 的 client；missing 或并发 revoked 是普通 absence。它不写 audit/event。
- `RevokeClient` 要求 client 已存在，分配新 `RevokedAt`，并原子追加一条
  `client.revoked` audit 与 event。重复 revoke 是使用更晚 candidate 的新显式
  command，不复用 uncertain candidate。
- pairing ID 与 code hash 唯一。`SavePairingCode` 是 create-only，只接受没有
  claim field 的 normalized pending record，并要求非零 expiry/code hash；空 ID
  由 repository 生成。repository 用自己的 high-water time 替换 caller
  `CreatedAt`，并原子追加 `pairing_code.created` audit/event。重复 ID/hash 为
  `conflict`，已消费 code 不能通过 save 重新打开。
- `GetPairingCode` 无副作用。已过期的 pending record 仍以 pending 持久化；
  caller 同时判断 status 与 expiry。claim 过期 code 返回 `conflict`，不做隐藏写入。
- `ClaimPairingCode` 要求 code 存在、pending、未过期，且 client ID/token hash
  全新。它分配 client creation time 与 pairing claim time，原子创建 client、
  两者使用严格大于两个 record high-water mark 的同一个 command timestamp；
  然后标记 code claimed，并追加一组 `client.saved` 与 `pairing_code.claimed`
  audit/event。event sequence 严格为 client-saved 后 pairing-claimed。Audit
  contract 没有 sequence column，因此两条 audit 是 unordered atomic set；测试
  比较 exact type 与 shared command timestamp，不依赖 equal-time row order。任何
  backend 都不能暴露或持久保留 partial command。

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
`runFileCommand`，不再对这些 field 使用 legacy `persist()`。startup 使用以下
显式 compatibility matrix：

- client/pairing map key 必须等于非空 embedded ID；
- legacy blank client owner backfill 为 `app.DefaultOwnerID`，blank actor backfill
  为该 owner，与已发布 schema compatibility 一致；
- legacy blank client name/token hash 或 pairing code hash 可加载，但 empty hash
  绝不认证或 claim，且允许多个 empty hash。PostgreSQL 现有 unique constraint
  使其每种 hash 最多一个 blank，而 File 可保留多个；
- non-empty client token hash 与 pairing code hash 必须唯一；
- client creation time 必须非零；每个 present client last-seen/revoked 或 pairing
  claimed pointer 也必须包含非零时间。legacy clock rollback 合法，因此 startup
  不为这些非零值虚构 chronological ordering constraint；
- pairing status 只能是 pending、claimed 或 expired。pending/expired 不带
  claim time/client；claimed 必须同时具备二者且引用 present client；pairing
  creation/expiry time 必须非零。

所有 accepted pointer 均 clone。除此之外不 normalize identity、status、relation
或 time；违反者是 corrupt startup state。

PostgreSQL command 获取 owned connection 并开启显式 transaction。client
command 使用 client-ID advisory lock；pairing command 使用 pairing-ID lock，
claim 还锁定生成的 client ID。resolution barrier read 显式使用
`READ COMMITTED`，先在独立 statement 取对应 lock，再 query。unique violation
映射为 `conflict`，其他 server rejection 映射为 `internal`。

readiness 前，PostgreSQL 通过同一个 compatibility validator 扫描所有 client/
pairing row，包括 claimed-client existence。legacy blank owner/actor 只做上述
projection backfill，GET 保持 read-only。后续每个 get/list/find/claim scan 也
验证 row，invariant violation 映射 `corrupt` 而非 absence。这是 File startup
validation 的 PostgreSQL 等价物；不需要 schema migration 或改变 CI service topology。

获取 connection 后的 unsafe statement、transport、context 或 commit failure
返回 `unknown_outcome`，terminate owned session 且绝不 release。candidate 形成前
返回 zero candidate。Safe-to-retry 与 server-rejected failure rollback 后返回
definite typed error；rollback failure terminate 且不 release，并保留全部 cause。

candidate 形成后，unknown pairing save 返回 normalized pairing candidate，unknown
claim 返回两个 normalized candidate，unknown revoke/touch 返回 client candidate。
除上文显式 typed business result 外，definite failure 返回 zero candidate。

reconciliation 只在全部 persisted field 精确相等时成功。pair creation 是
create-only，claim 是 single-use，因此 matching barrier 后的 exact candidate
可证明完整 atomic transaction。revoke/touch candidate 使用 non-rollback
high-water time。different record、absence 或 read failure 保持 unresolved，
绝不自动 retry 或报告 success。

## Pending Secret Coordinator

Gateway 拥有一个覆盖 start 与 claim 的 capacity-one pairing coordinator。它是
process-local，绝不 persist、log、audit、trace 或 project plaintext code/token。

coordinator 串行化每个完整 transition：检查或安装 pending intent、调用
repository、附加所有返回 candidate、reconcile，并 retain、disclose 或 clear
secret。每个 installed intent 都有 process-local 单调递增 generation；timer 只能
清除该 generation，late callback 绝不能清除 replacement。timer 也监听
`gatewayServices.Start` 绑定的 lifecycle context；lifecycle cancellation 清除
matching generation 并退出，不再调用 Store。

- `SavePairingCode` 前，Gateway 先生成 non-empty pairing ID 与 code hash；start
  在持有 coordinator gate 时保存 plaintext code、owner、canonical request
  fingerprint、expiry 及完整 attempted pairing command identity。repository
  仍支持为其他 caller 生成 ID，但本 flow 绝不使用。repository 返回的 normalized
  candidate 在 success 或 unknown reconciliation 前附加。unknown save 使用保留的
  attempted ID 立即执行 get barrier。unresolved 时保留 pending；后续同一 start
  可 reconcile 并取得原 code，不同 request conflict。pending 时不生成第二个
  code/save。
  zero-candidate unknown 只能通过 barrier absence 证明 rollback；任何 present
  record 都不匹配，因此 conflict，绝不披露 code。
- 校验 submitted pairing code 后、repository claim 前，Gateway 生成 non-empty
  client ID 与 token hash。claim 保存 plaintext bearer token、完整 attempted
  client command identity、owner/pairing
  ID/submitted code hash/normalized client name 的 canonical fingerprint，以及
  pre-command pairing record/expiry。repository 仍支持为其他 caller 生成 client
  ID，但本 flow 绝不使用。repository 返回的 normalized pairing/client candidate
  在 success 或 unknown reconciliation 前附加。后续同 pairing request 先针对
  retained pre-command code hash 重复 constant-time comparison；invalid code 仍为
  unauthorized。然后只有 exact fingerprint 才能 reconcile 并恢复原 token；
  不同的 valid request conflict，pending 时不生成第二个 token/client/claim。
  zero-candidate unknown 使用保留的 attempted client ID 做 absence barrier，绝不
  从 repository result 推断该 ID。
- definite failure 清除对应 pending secret。start 的 barrier absence 证明 rollback；
  claim 的 exact pre-command pending pairing 加 client absence 证明 rollback。两者都
  清除 pending 并报告 persistence failure，不自动 retry。exact command candidate
  完成；different state 清除并 conflict；unresolved backend error 保留 pending。
- completion 在 disclosure 前立即读取 live Gateway clock。达到/超过 expiry 时
  清除 plaintext 并返回 expired。已 commit 但恢复前过期的 pending claim 可能
  留下 visible active client，其 random token 从未披露；owner 使用现有 client
  list/revoke surface 修复，不静默 retry 或隐藏。
- owned expiry timer 在 pairing expiry 主动清除 pending plaintext，无需下一请求。
  replacement/completion 停止之前的 timer，Gateway lifecycle cancellation 清除
  pending 并停止 timer。process crash 或 response loss 仍可能留下 undisclosed
  client，因为 plaintext 有意不 durable。startup 无法区分它与已成功 delivered
  response 的 client，因此不猜测 revoke；使用相同 visible remediation。

## Gateway 迁移

- 所有调用使用 `r.Context()` 或 authentication request 的 owned context。
- client list failure 映射稳定 504 timeout 或 503 unavailable copy；revoke 仅对
  typed `not_found` 保留 404。
- pairing start 仅在 exact pairing candidate durable/reconciled，且 live completion
  clock 仍早于 `ExpiresAt` 后披露 plaintext code。
- pairing claim 只执行一个 repository command，仅在 pairing/client candidate
  durable/reconciled，且 live completion clock 仍早于 pairing expiry 后披露
  bearer token。
- bearer authentication 区分 invalid/revoked credential 与 Store failure。
  invalid/revoked 保持 401；lookup/touch timeout 为 504，其他 Store failure 为
  503；Touch 必须确认 client 仍 active。
- 现有 bootstrap validation 保持稳定：pairing disabled 为 400
  `pairing is not required`，non-local start 为 403
  `pairing can only be started locally`，malformed input 为 400，absent claim 为
  400 `pairing code not found`，non-pending/expired claim 为 400
  `pairing code is not active`，code mismatch 为 401 `invalid pairing code`。
  不同 pending fingerprint 为 409 `another pairing request is pending`；different
  reconciled state 为 409 `pairing state changed`。start 在 disclosure 前即时
  expiry 为 503 `pairing is temporarily unavailable`；claim 此时使用同一个稳定
  inactive 结果返回 400。start、claim 或 reconciliation 的 Store timeout 均为
  504 `pairing request timed out`；definite persistence failure、unresolved unknown
  outcome、response 仍可写时的 canceled operation、corruption、internal failure
  或其他 Store failure 均为 503 `pairing is temporarily unavailable`。response
  绝不包含 raw Store error、candidate、code/token hash 或 plaintext secret。
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
- pending start/claim immediate/next-request reconciliation、same-request secret
  recovery、different-request conflict、no second generation/command、live-clock
  expiry、cleanup 与 process-loss orphan/remediation evidence；
- File/PostgreSQL compatibility-matrix/corrupt-row、pairing `ClaimedAt` alias、
  event sequence 与 unordered audit-set 测试；
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
| Client contract review 1 | `bae3623` | `REVISE` | unknown claim 可能丢失 bearer token；legacy/corrupt backend parity、pairing pointer isolation、audit ordering 与 live expiry disclosure 不完整 | Context-isolated gatekeeper / 2026-08-20 |
| Client contract review 2 | `9ff7c14` | `REVISE` | zero-candidate recovery 缺 attempted command identity；pairing public error projection 未冻结；roadmap 状态过期 | Context-isolated gatekeeper / 2026-08-20 |
| Client contract repair 2 | pending | pending | pending | pending |
