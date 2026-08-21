# SessionRepository 迁移设计

> Language: 简体中文 | [English](../../docs/store-session-repository-design.md)

> 状态：2026-08-21 S3 SessionRepository 设计审查候选。本精确双语设计获得记录的
> `GO` 前不得开始实现。Connector 与集成 Credential 生命周期已在 `a9a5ab9` 获得
> implementation `GO`。

## 范围

本阶段迁移 S0 冻结的 6 个 Session Store 方法、三个后端和全部调用方，关闭当前 File/
PostgreSQL 静默失败，并保证 session row 与 lifecycle record 原子提交。S0 明确把
`DeleteSession` 留在 SessionRepository，即使它会删除后续 repository 持有的记录；
因此本设计把删除视为一个显式跨记录事务，而不是普通 CRUD。

本阶段不迁移 Conversation、Run、Document、Approval、Schedule、External Chat、
Browser State、Memory、Audit 或 Artifact Metadata interface；不拆 Store 大文件，不改
File snapshot 形状，不新增 PostgreSQL migration，不改 PostgreSQL CI/DSN 行为，不加
Runtime 监管，也不混入 ASR。

## Repository 契约

```go
type SessionRepository interface {
    CreateSession(context.Context, string) (app.Session, error)
    CreateSessionWithScope(context.Context, string, string, string, string, bool) (app.Session, error)
    ListSessions(context.Context) ([]app.Session, error)
    GetSession(context.Context, string) (app.Session, bool, error)
    UpdateSessionTitle(context.Context, string, string) (app.Session, error)
    DeleteSession(context.Context, string) (app.Session, error)
}
```

`Store` 只嵌入一次该 interface，Memory/File/PostgreSQL 直接断言。6 个无 context 签名
同时删除；不得保留 compatibility adapter、optional type assertion、动态 repository
lookup 或丢弃错误的 wrapper。

`CreateSession` 保留为 S0 冻结的 default-owner/default-webchat 便利方法。它与 scoped
create 共用 private implementation，但拥有独立的有限 operation ID 和错误归属。

## 记录契约

Create 在 backend submission 前生成新 `s_` ID；调用方不提供 ID 或 lifecycle time。
空 title 变为 `New SparkClaw Session`。owner/source trim 后为空时分别使用
`app.DefaultOwnerID`/`webchat`，workspace root 也 trim；非空 title 内容为兼容而保留。
CreatedAt/UpdatedAt 相同、为 UTC，并兼容 PostgreSQL 微秒精度。

持久化 session 要求非空 ID/owner/title/source、ID 不超过 256 bytes、owner/source/
workspace 已规范化、创建/更新时间非零 UTC，且 UpdatedAt 不早于 CreatedAt。Update
只能改变 trim 后非空 title 和 repository 分配的 UpdatedAt，并使用 Store 时间高水位，
避免时钟回退破坏排序。

List 只返回 `Hidden == false`，按 UpdatedAt 降序、ID 升序稳定排序。Get 精确按 ID，
可以返回 hidden session。只有 Get 的正常缺失为 `(zero, false, nil)`；backend、scan、
validation、context 或 row iteration 失败不得变成缺失或空列表。

File 启动先执行已发布的 linked MCP/external-chat 兼容规范化；只允许旧空 owner 变为
default owner、旧空 source 变为 `webchat`，其他损坏不得修复，map key 必须等于内嵌
ID。PostgreSQL 在已有 adoption normalizer 后验证全部 session row 才 ready。

## 受保护 Session

MCP binding 持有其可见 conversation 的 title 与保留 history。normalized source 为
`mcp` 时，UpdateSessionTitle/DeleteSession 返回 typed `conflict`。Gateway 保留提前
公共检查以提供清晰 UX，但 repository 规则阻止其他 caller 绕过。MCPRepository 仍是
MCP binding cleanup 唯一 owner，不调用普通 Session delete。

hidden Telegram/Weixin session 保持现状：公共 list 排除，内部 exact Get 可用，获得
明确授权的内部 delete 使用同一完整事务。

## Lifecycle 原子性

Create 原子提交 session、一个 `session.created` audit 和一个同名 event。Update 原子
提交 title/time、一个 `session.updated` audit 和 event。lifecycle 写失败就是命令失败，
不能返回成功 session mutation。

Delete 返回精确删除的 session，并原子删除 S0 分配给该命令的全部状态：

- session 与 messages；
- Agent runs、run feedback、model/tool calls 和 episode summaries；
- session 范围的 document records 与 approvals；
- reminders 及其 delivery rows；
- memory candidates，以及 source run 属于该 session 的 memories；
- browser login blocks；
- artifact metadata 与内存 URI index；
- linked external-chat sessions 及 messages；
- 被删 session 的旧 audit/event rows。

同一事务在被删 session scope 之外追加一个替代 `session.deleted` audit/event，只携带
deleted session projection 与非秘密 ID/title 证据。它不删除 delivery/receive record、
connector binding、passive notification、evaluation、browser auth 或其他记录。测试
同时证明目标完整删除和跨 session 隔离。

PostgreSQL 必须先删 reminder deliveries 再删 reminders，并在 runs/session 前删除所有
foreign-key child；检查每个 statement 错误，并要求恰好删除一条 session。这会关闭
Memory/File 删除 reminder 而 PostgreSQL 可能保留的现有差异。

## Operation 与失败契约

| Operation ID | 模式 | 超时 | 协调证明 |
|---|---|---|---|
| `session.create` | write transaction | transaction | exact Get 等于 candidate |
| `session.create_with_scope` | write transaction | transaction | exact Get 等于 candidate |
| `session.list` | read | read | 无 |
| `session.get` | read/barrier | read | self |
| `session.update_title` | write transaction | transaction | exact Get 等于 candidate |
| `session.delete` | write transaction | transaction | exact Get 缺失 |

caller deadline 优先，fallback 使用现有 Store operation setting。admission/backend 前取消
不得做任何工作。Memory 在加锁前和锁内检查 context；File 使用 migrated admission；
PostgreSQL 使用 owned connection、显式事务和 domain-separated session-ID advisory
barrier 保护 Get/全部 mutation；List 使用一个 read transaction 并检查 `rows.Err()`。

input/shape 为 `invalid`；update/delete 缺失为 `not_found`；受保护 MCP mutation 为
`conflict`；取消/超时为 `canceled`/`timeout`；持久化损坏为 `corrupt`。File pre-submit
失败为 `durability_failed`，不确定 submitted replacement 在 accepted fence 后返回
`unknown_outcome`。

PostgreSQL safe business/server rejection rollback 后 release。submission 后不安全的
transport、statement、context 或 commit failure 为 `unknown_outcome`，terminate session
且不 release。rollback failure terminate，并保留 primary/rollback/termination cause。
`pgx.ErrNoRows` 只对 Get 表示正常缺失，对 update/delete 映射 typed `not_found`。

unknown create/update 返回的非零 candidate 只是证据，只有 exact Get equality 证明
commit；unknown delete 返回的 removed candidate 也只是证据，只有同 barrier 后 Get
缺失证明原子删除。其他 present/different/unresolved read 保留原 unknown。caller 不得
把 candidate 当成功，也不得在证明前自动 retry。共享 Store helper 集中这三种协调，
避免调用方各自拼接。

## 后端实现

Memory 在同一锁内完成 create/update/delete、lifecycle 和 index 变化，并返回错误。
Session 没有 mutable reference field，但所有输出仍为 value copy。

File 将 6 条 `admitLegacy*`/`persist()` 路径替换为 migrated admission 和
`runFileCommand`。确定失败恢复完整 pre-snapshot，包括 event sequence、audit、artifact
URI index 和被删 child map；timestamp high-water 不回滚。unknown rename/directory sync
安装标准 fence，后续 Session 或其他 migrated read 协调后才能观察。Snapshot JSON
形状保持兼容。

PostgreSQL 在一个 transaction 内 create/update/delete session 和所需 lifecycle row；
不得使用 `context.Background()`、忽略 Exec、post-commit best-effort lifecycle append，
也不得在 transaction 前读取 delete target。Create/update 使用 `RETURNING`；delete
`FOR UPDATE` 读取受保护 target，按依赖顺序删除、插入替代 lifecycle，再一次 commit。

不允许机械移动无关方法。可以新增窄 Session contract/helper/test 文件，但本阶段不拆
现有 Store 大文件。

## 调用方迁移

全部生产 caller 传递 request/run/worker/startup/shutdown context 并处理 typed error。
HTTP session handler 不再匹配错误字符串：invalid 400、not_found 404、conflict 409，
unavailable/durability/unknown 503；绝不序列化 uncertain candidate。

Gateway、Agent、ToolHub、ISCP Bridge、message control、delivery、Telegram、Weixin 在
S4 前可以保留 consumer-owned broad composite，但 Session surface 必须是上述 exact
repository。Create 失败会阻止该调用继续做 message/external-chat linking。读取失败
不得变成 missing session、default owner/workspace、空 catalog 或继续执行。

测试中 session 仅为 fixture 时使用 `storetest` must-helper；production 不提供 no-error
便利 wrapper。

## 验证门禁

Implementation `GO` 要求：

- exact interface/signature/operation/source guard、Store 只嵌入一次、三个后端直接断言；
- Memory/File 在 normalization、hidden filter、deterministic order、exact absence、MCP
  保护、timestamp high-water、atomic created/updated/deleted lifecycle 方面一致；
- 完整 delete fixture 覆盖全部 S0-owned record/index，另有第二 session 证明隔离；
- File 在 encode、write、file sync、close、rename、directory open/sync/close、rollback、
  unknown fence、restart、cancellation 的确定性注入，且无 Session `persist()`；
- PostgreSQL 覆盖 acquisition、begin、barrier、select/scan、create/update、每个 delete
  statement、lifecycle insert、rows affected、list iteration、commit、rollback、release、
  termination；
- 通过 DSN skip 的真实 PostgreSQL round-trip/concurrent update-delete，CI service 和
  `SPARKCLAW_TEST_POSTGRES_DSN` 不变；
- 生产 caller error/context 测试，以及 compile-fail/source fixture 外零无 context
  Session call；
- affected package、完整 Go build/test/vet、聚焦 Store/Gateway/Agent/connector race、
  未改变的 WebChat test/build，以及双语 docs CI 全绿。

## 提交边界

1. 本双语设计与独立设计门禁。
2. Session contract/operation、三个 backend implementation 和 repository contract/
   failure test。
3. 生产/测试 caller 迁移和公共 typed error mapping。
4. 独立 implementation gate 记录。

不得混入其他 repository migration、PostgreSQL schema change、Runtime 监管、ASR CI
或大文件拆分。

## 审查记录

| 审查 | 修订 | 结论 | 证据 | 审查人/日期 |
|---|---|---|---|---|
| SessionRepository design | pending | pending | pending | pending |
