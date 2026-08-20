# PostgreSQL Schema 与 Store 配置设计

> 语言：[English](../../docs/store-postgresql-schema-config-design.md) | 简体中文

> 状态：S1 实现于 2026-08-20 经一次实现 `REVISE`、修复提交 `74b7c5e`
> 和独立复审 `GO` 后通过。当前等待用户验收本阶段；仍不授权 S2。

## 目标

用一份内嵌、版本化 migration 来源替代重复的 PostgreSQL schema，并让
Store 专属配置在 `config.Load` 阶段失败，而不是到第一次持久化操作时才失败。

本阶段不修改 PostgreSQL CI 服务、`SPARKCLAW_TEST_POSTGRES_DSN` 名称或
测试 skip 行为。

## Schema 权威

`services/gateway/internal/store/migrations/*.sql` 下按顺序排列的 SQL 文件
成为唯一 domain-schema 权威。

1. 将根目录 `migrations/0001_core.sql` 按字节完全相同地移动到 Store package，
   并冻结 SHA-256
   `d16479c0830460418d27d3595a513232a688cb8bc75173b53f2f7f068f6c5382`。
2. 从原 `postgresSchema` SQL 正文精确生成 `0002_reconcile_current.sql`。
   它保持幂等并保留五条兼容 DML，同时加入下述 preflight 和 postcondition。
   其冻结 SHA-256 为
   `2c1cdfc20123dfdeffe6ef72c20decef78ca2fa75287b5985ed18e36e18dd0ed`。
3. 在不修改已应用 migration 0002 的前提下追加
   `0003_validate_legacy_chat_keys.sql`，拒绝 legacy adoption 产生的 session
   和非空 message 自然键歧义。其冻结 SHA-256 为
   `709cdc2063f99ded33ed0714a3e7e418ec936fdfeec7fb50b596f9e8aee5addc`。
4. 使用 `go:embed` 内嵌有序文件，并由 Gateway Store startup 执行。
5. 删除 Go `postgresSchema` domain DDL，并停止把另一份 schema 目录复制
   进 PostgreSQL image。
6. 全新数据库和现有 SparkClaw 数据库使用同一个 runner。

runner 只能包含最小 migration ledger bootstrap DDL，不得在 Go 中重述应用表
或索引。删除 Go 常量前，S0 manifest 守卫由根 SQL 与 Go 的比较改为验证有序的
内嵌 migration 集合。

## Migration Ledger

`sparkclaw_schema_migrations` 保存不可变 version、filename、SHA-256
checksum 和应用时间。

- 文件名严格为 `NNNN_name.sql`；数字版本唯一且与 lexical 顺序一致；
- 已应用版本 checksum 不同则 startup 失败；
- 出现未知已应用版本则 startup 失败；
- 已应用集合必须为完整前缀，存在缺口则 startup 失败；
- 所有待应用 SQL、DML postcondition、最终 schema 校验及对应 ledger row
  在同一事务提交，失败的 adoption 不留下部分 ledger 或 schema 变更；
- 成功后重启必须幂等；
- migration 和 migration 后校验通过前 readiness 保持 false。

runner 先取得一条专用 pool connection 和 session advisory lock key
`6003381055699113777`，然后才能创建或读取 ledger。lock acquisition、ledger
检查、adoption、校验和 commit 全部使用 startup context。锁覆盖完整 migration
集合。unlock 使用独立五秒 cleanup context；如果无法确认解锁，则关闭物理
connection，不能把它还给 pool。

S1 是非滚动 Store 升级：所有旧 Gateway process 必须先停止，新 Gateway 才能
开始 migration。作为可执行后备，`0002_reconcile_current.sql` 在创建兼容表后、
compatibility preflight 前，对 `weixin_chat_sessions`、`weixin_chat_messages`、
`external_chat_sessions` 和 `external_chat_messages` 获取
`SHARE ROW EXCLUSIVE` transaction lock。锁在 ledger commit 前阻塞并发 insert、
update 和 delete。runner advisory lock 串行化新 runner；table lock 约束违反部署
规则的旧 writer。

对于没有 ledger 的现有数据库，runner 将其视为无版本 adoption 候选，在同一
事务中应用完整内嵌 migration 集合，使用同一内嵌 SQL 构造 scratch schema 并
比较最终 catalog，全部通过后才插入每一条 ledger row。catalog 比较精确覆盖
预期 table、column、type、default、nullability、PK/FK/UNIQUE/CHECK constraint
和命名 index 定义。预期对象上的额外 column 或定义差异会 fail closed；无关
table 不被接管或删除。scratch schema 随事务清理，不构成第二 schema 权威。
任何单张表的存在都不能证明 adoption 成功。

### 兼容 DML Adoption

`0002_reconcile_current.sql` 拥有五条历史 DML，
`0003_validate_legacy_chat_keys.sql` 拥有追加的歧义 postcondition。两者共同
明确以下接管契约：

- target primary-key 是否存在是 already-copied 判据。target ID 一旦存在，
  `external_chat_*` 即为权威；migration 不比较或覆盖其 owner/workspace/
  binding/channel/provider/external ID、display/status/cursor/context/content/
  error/run-link 或 timestamp 字段；
- 缺失 session target ID 时从 legacy source 复制。在 insert 前，若 canonical
  `(binding_id, external_chat_id=external_user_id, external_thread_id='')`
  已由不同 target ID 占用，则 adoption 失败；
- 缺失 message target ID 时从 legacy source 复制。在 insert 前，若非空
  canonical `(chat_session_id, external_message_id)` 已由不同 target ID 占用，
  则 adoption 失败；
- copy 后，0003 通过 legacy source ID 关联 canonical target，仅当 canonical key
  仍等于 legacy projection 时拒绝重复 session key 或重复非空 message key。
  canonical key 已演进的同 ID target 仍为权威，不参与此检查；
- 两条 copy 仍使用 `ON CONFLICT (id) DO NOTHING`，随后要求每个 source ID
  恰好存在一个 target ID，并要求匹配数等于 source 总数。transaction lock
  保证每个新 target 都精确来自 canonical `INSERT ... SELECT`；此前已存在的
  target 可在所有可变字段上合法分歧，不属于 copy postcondition；
- `waiting -> waiting_owner/schema_version=2` 和
  `resuming -> validating_visible/schema_version=2` 是可重复映射；postcondition
  拒绝残留 legacy status，以及 schema version 不为 2 的目标 status；
- `version <= 0 -> 1` 可重复，postcondition 拒绝任何残留非正 version。

所有 preflight、DML、postcondition、schema 校验和 ledger row 都位于同一事务。
冲突时保留无版本数据库，并返回安全 startup 错误，而不是静默接管分歧数据。

S0 盘点提供精确的
[约束级协调清单](store-s0-postgresql-reconciliation-manifest.md)。删除任一旧
来源前，migration 审查必须将 manifest 同时与旧根 SQL 和当前
`postgresSchema` 比较。

## PostgreSQL 操作边界

startup migration 使用调用者 startup context。`StateConfig` 增加
`startup_timeout_seconds`，环境变量为
`SPARKCLAW_STATE_STARTUP_TIMEOUT_SECONDS`，默认 180 秒，允许范围为 1 到
900 秒（含端点）。`config.Load` 完成校验；assembly root 构造有界 startup
context，并经 `newStore` 传给 `NewPostgresStore`。直接调用 constructor 且没有
更早 deadline 时使用相同的 180 秒 fallback。pool 或 lock 获取前取消时不得
启动 migration 工作。

pool acquisition、advisory-lock acquisition、begin、每条 statement、catalog
scan、校验、commit 和 reconciliation 都继承有效 context。每个 migration
事务把服务端 `statement_timeout` 设置为不超过剩余 startup deadline，并把
`lock_timeout` 设置为 30 秒。这些只是后备约束，不能替代调用者取消；advisory
lock 等待直接受 startup context 限制。

普通 read/write/transaction timeout 字段只在 S2 pilot repository 实际使用
时引入，避免配置声明无效行为。接受的名称和初始值来自 S0。

## Store 配置校验

本阶段的 `config.Load` 只校验 Store 拥有的 setting：

- `state.backend` 和 `SPARKCLAW_STATE_BACKEND` 先 normalize 大小写和空白，
  随后必须严格为 `memory`、`file` 或 `postgres`；
- File 要求非空且转换为绝对路径的 `state.path`；
- PostgreSQL 要求非空且 trim 后的 DSN。先加载 canonical
  `SPARKCLAW_STATE_DSN`；两者都存在时，legacy `SPARKCLAW_POSTGRES_DSN`
  保持当前的 override 优先级；
- File encryption 启用时，direct key 和 key-file source 必须恰好设置一个。
  key file 在 `config.Load` 中完成路径 normalize、可读性和非空校验；两者均有
  或均无都失败；
- `state.startup_timeout_seconds` 默认 180 秒，范围为 1..900；
- 畸形 Store bool/int override 返回包含准确环境变量名称的错误；
- encryption bool 大小写不敏感地接受现有 true 形式
  `1/true/yes/on/required` 和 false 形式 `0/false/no/off`，其余值均拒绝；
- DSN 和 key 不得进入公共投影或日志。

全局清理所有宽松环境变量解析器，以及 artifact 对象后端构造，都属于独立
工作。本阶段不得扩张为整个 config 的重写。

## 数据库失败契约

migration runner 返回并保留所有 pool、lock、begin、execution、scan、rows、
校验、commit 和 rollback 错误。rollback 错误与主诊断 join，但不替代主错误。

commit 提交后返回 transport 或 cancellation 错误时，销毁旧物理 connection，
防止其 session lock 泄漏。使用剩余 startup context 获取新 connection 和同一
advisory lock，然后读取完整 ledger 并重新运行只读 final catalog/DML
postcondition：

- version、filename、checksum 和 postcondition 均精确匹配表示 `committed`；
- 重新取得锁后没有新增 ledger row，表示 PostgreSQL 已将原子事务解析为
  `not_committed`，允许一次有界重试；
- ledger 部分写入/不匹配、postcondition 失败，或无法重新取得并检查，仍为
  `unknown_outcome`，startup 失败且不得重试。

`normalizeMCPBindingSessions` 仍是 schema runner 之后独立、幂等的 startup 数据
normalization。它使用同一有界 context，失败时 startup 失败，但不作为 schema
migration 或 ledger row。

## 验证

需要以下确定性和已配置 PostgreSQL 证据：

- 内嵌 migration 顺序和 checksum unit test；
- source guard 证明 Go 中不再存在 domain DDL；
- 全新空数据库达到预期 manifest；
- 当前无版本数据库在不丢数据的情况下接管 ledger；
- Weixin copy 的 missing-ID natural-key 冲突在不写 ledger 的情况下失败；
- 重复 legacy session key 和重复非空 legacy message key 在不写 ledger 的情况下
  使 adoption 失败；
- 演进后的 target row 在 adoption 后保持不变，而缺失 target ID 与另一 target
  的 canonical natural key 冲突时 fail closed；
- 五条兼容 DML 的 postcondition 均得到证明，重复 adoption 保持稳定；
- 并发 startup runner 在固定 advisory lock 上串行；
- compatibility table 写入阻塞到 migration commit，且部署流程要求新 binary
  启动前停止旧 Gateway；
- 当前有版本数据库重启时无 DDL churn；
- checksum 变化和未知版本导致 startup 失败；
- 强制 migration 失败会回滚且不留下 ledger row；
- 注入 commit 不确定性，证明 committed、not-committed 有界重试和未解决
  unknown-outcome 分支；
- DDL 权限不足会在 readiness 前失败；
- 默认 File 和 Memory 配置仍可加载；
- 非法 backend、缺失 File path、缺失 DSN、畸形 timeout 和无效 encryption
  配置返回安全错误；
- 现有 DSN 门控 Store 测试在配置环境中通过。

PostgreSQL 集成证据记录测试 commit、server version、migration 起始状态和
命令结果。没有 DSN 时阶段保持未批准，必须报告为未运行而不是通过。

## S1 审查门禁

设计 `GO` 要求 reconciliation manifest、ledger 接管规则、配置范围、回滚和
测试环境已接受。实现 `GO` 要求上述验证全部完成、两份旧 schema 权威均被
删除，并在保持现有 CI gate 的同时完成真实 PostgreSQL 运行。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| 设计审查 1 | 2026-08-19 草案 | `REVISE` | 缺少兼容 DML adoption 判据、advisory lock、commit 不确定结果状态机和精确配置/context 入口 | 独立 gatekeeper / 2026-08-20 |
| 设计审查 2 | 2026-08-20 修订 1 | `REVISE` | legacy copy 全字段相等会拒绝合法演进 target；runner lock 未排除旧 Gateway compatibility 写入 | 独立 gatekeeper / 2026-08-20 |
| 设计审查 3 | 2026-08-20 修订 2 | `GO` | target-PK already-copied 权威和非滚动/四表锁协议闭合剩余 adoption 与旧 writer 窗口；未引入第二 schema 权威 | 独立 gatekeeper；用户授权阶段推进 / 2026-08-20 |
| 实现审查 1 | `0c557ee` 至 `3f098f3` | `REVISE` | 静态审查和 PostgreSQL 18.4 测试发现两个缺失 legacy ID 可共享一个 session 或非空 message 自然键，并同时进入非唯一 canonical index | 独立 gatekeeper / 2026-08-20 |
| 实现审查 2 | 修复 `74b7c5e` | `GO` | 保持 0002 不可变；transactional 0003 拒绝两类重复且 ledger 保持为空，同时保留已演进同 ID canonical target 的权威。完整 Go build/test/vet、Store race、WebChat、Compose/script、双语文档和真实 PostgreSQL 证据均通过。剩余低风险：重复 source 与 evolved target 的组合由 SQL 谓词和独立测试共同覆盖，未单设组合用例 | 独立 gatekeeper / 2026-08-20 |
