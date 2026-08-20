# Store S0 基线与验收报告

> 语言：[English](../../docs/store-s0-acceptance-report.md) | 简体中文

> 状态：2026-08-20 人工辅助 S0 实现审查候选。用户已于 2026-08-20 授权
> 开始 S0。实现结论仍为 `pending`；本报告不授权 S1 或 S2。

## 结论

S0 已具备完整、可执行的 141 方法/20 repository 目录、生产消费者矩阵、
命令/reconciliation 证据、可执行 PostgreSQL 源码清单，以及受守卫的完整
20 repository × 10 维 characterization 矩阵。消费者守卫覆盖 58 个直接声明
位置和 10 个局部接口。候选建议是**进入人工 S0 实现审查的 GO**，但实际审查
记录有意保持 pending。在用户记录 `GO`、`REVISE` 或 `STOP` 之前，工作必须
停在本候选，不进入下一阶段。

## 进入基线

基线在编辑 S0 代码或证据文档之前记录。

| 项目 | 结果 |
|---|---|
| Commit | `df05cf58a6804c8eb4b1434a18044728c3ec2c8e` (`df05cf5`)，detached worktree |
| 预先存在的 worktree 变更 | 已修改的双语文档索引和 14 份未跟踪的分阶段设计文档（七份英文、七份中文）；全程保留 |
| Host | Linux ARM64；Go `1.25.5`；Node `26.2.0`；npm `11.17.0`；Python `3.12.3` |
| 文档工具准备 | `npm run setup:document-tools` 通过；安装 179 个 Node package，并确认 Python 文档依赖 |
| 后端 build | `cd services/gateway && go build ./...` 通过 |
| 后端完整测试 | `cd services/gateway && go test ./...` 通过；document ToolHub package 用时 44.260 秒 |
| Store 基线 | `go test ./internal/store -count=1 -v` 的 Memory/File 通过；全部 9 个现有 PostgreSQL 测试按未改变的 DSN gate 跳过 |
| PostgreSQL gate | `SPARKCLAW_TEST_POSTGRES_DSN` 未设置；PostgreSQL 是**未运行**，不是通过 |
| WebChat build | `npm --workspace @sparkclaw/webchat run build` 通过；Vite 产出 55.93 kB CSS 和 380.59 kB JS |

基线未发现失败。PostgreSQL 证据缺口保留为风险，不转换成成功结论。

## S0 行为刻画证据

`s0_contract_characterization_test.go` 增加代表性后端无关 harness 和静态契约
检查。`s0_repository_characterization_test.go` 在 Memory/File 上为全部 20 个
repository 运行成功和正常缺失用例，记录当前可变别名缺陷，并补齐两个此前缺失
的 File 重启用例。`s0_repository_evidence_test.go` 拥有完整矩阵及测试名称/文档
守卫。`s0_postgres_manifest_test.go` 比较当前两个 schema 权威，但不修改任一来源。

共享 harness 具有代表性，但不替代已接受的逐 repository 门禁。S0 清单包含
完整的 20 repository × 10 维适用性/证据矩阵，并由可执行完整性和测试名称
守卫支持。共享 harness 未覆盖的 repository 证据由现有专项测试提供。

| 契约 | 证据 |
|---|---|
| 完整归属 | Reflection 证明恰好有 141 个 `Store` 方法，且每个方法在 20 个 repository 中只有一个 owner |
| 后端完整性 | 现有编译断言覆盖 Memory、File、PostgreSQL；实现文件映射位于 S0 清单 |
| 生产消费者 | AST 守卫冻结 58 个直接 constructor/field/helper/worker 声明位置和 10 个展开后的局部 Store-compatible 接口 |
| 逐 repository 成功与正常缺失 | 具名 Memory/File 子测试覆盖全部 20 个已接受 repository；受守卫矩阵链接更丰富的适用证据 |
| 成功、顺序、过滤、owner scope | Repository 专项证据覆盖 document、owner、client、message、schedule、connector、passive、external-chat、delivery、run、audit、artifact 等适用查询契约 |
| Clone / alias 行为 | Owner preference 与 MCP 嵌套值已隔离；缺陷证据记录其余 12 个 repository 当前在 Memory 和运行中的 File decorator 上存在可变 alias 逃逸 |
| 幂等 | 相同 MCP binding/key/fingerprint 复用同一 operation；fingerprint 变化时 conflict |
| CAS | MCP operation update 增加 version，并拒绝 stale expected version |
| Event | Message event 按 session 隔离、有序，并暴露正确 head |
| 重启 | 显式断言证明 File session、message、message event/head、owner map、MCP operation、嵌套 invocation argument、result bytes、reminder/delivery 和 notification binding 在 reload 后保留；专项测试覆盖其余 repository、加密和旧格式规范化 |
| 并发 | Memory/File 上 16 个同时发生的相同 operation create 只产生一次创建和一个稳定 ID |
| Snapshot 兼容性 | Reflection 冻结全部 38 个字段名和 JSON tag，包括旧 Weixin 兼容字段 |
| PostgreSQL 源码协调 | 约束级可执行 manifest 冻结根侧 18/16 个表/索引与 Go 侧 37/42 个对象；比较完整共享定义，并对 19 张仅 Go 侧表的每个列、类型、default、nullability、inline/table constraint 和索引执行冻结与文档检查 |
| PostgreSQL DSN suite | 现有 9 个测试及其 skip 行为未改变；本候选没有 DSN |
| `rows.Err()` | 静态缺陷证据列出 9 个函数中的 10 个未检查 row loop；`ListAllConnectorSettings`、binding revoke、delete、normalization 和 message-event paging 等已检查路径保持区分 |
| 已知生产缺陷 | 静态证据冻结 48 个 File 丢弃错误点、33 个 PostgreSQL 显式丢弃结果、10 个未检查 row loop，以及 12 个 repository 的可变 alias 逃逸，留待后续用失败/隔离断言替换 |

S0 测试刻画当前成功契约。缺陷证据测试有意命名为
`TestS0DefectEvidence...`；S1-S3 修复所属行为时必须删除或替换每个断言。

## Context Timeout 依据

初始 fallback 类别保持 read 10 秒、write 30 秒、多记录 transaction 60 秒、
startup/schema 180 秒。S0 将其接受为保守的**初始边界**，而不是实测
PostgreSQL SLO。

| 类别 | 初始值 | 证据与限制 |
|---|---:|---|
| Read | 10 s | 本地 Memory/File 行为刻画在毫秒内完成；10 秒为 filesystem/pool acquisition 留出多个数量级余量，也与现有 10 秒 browser-startup/Gateway-shutdown 尺度一致。调用者更早的 deadline 仍优先。 |
| Write | 30 s | 与现有 30 秒外部 request/delivery 边界一致，并允许瞬时本地争用下的一次 fsync/rename 或普通数据库写入；是 read fallback 的三倍。 |
| Transaction | 60 s | 允许跨记录 session delete、due-claim、MCP revoke、rollback 和 reconciliation 共用一个边界，不继承短 request 默认值；是普通 write fallback 的两倍。 |
| Startup/schema | 180 s | Startup 可在 readiness 前获取冷本地 pool 并应用增量 DDL。三分钟仍有限且远低于 host recovery 边界，但 S1 必须测量全新和接管数据库后才能验证此值。 |

这些值使用前必须通过配置校验。S2 pilot 真正使用相关值之前，不得展示普通
read/write/transaction 配置字段。S1 必须记录 server version 和全新/接管
migration 时长；如果配置的本地 PostgreSQL p99 接近某个 fallback 的三分之一，
应审查该值，而不是自动扩大。

## 精确 S1 验证命令

使用独立的一次性 PostgreSQL 数据库，因为现有 integration test 会 truncate
Store 表。环境变量和 skip 行为保持不变。

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/store -run '^TestS0PostgresReconciliationManifest$' -count=1 -v
go test ./internal/config -count=1
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test ./internal/store -run '^TestPostgres' -count=1 -v
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test -race ./internal/store -run '^TestPostgres' -count=1
go test ./...
go vet ./...
```

S1 还必须按精确名称运行已接受的 migration 测试：

```bash
cd services/gateway
go test ./internal/store -run '^(TestPostgresMigrationsOrderedAndChecksummed|TestPostgresMigrationFreshDatabase|TestPostgresMigrationAdoptsUnversionedDatabase|TestPostgresMigrationRestartsWithoutDDLChurn|TestPostgresMigrationRejectsChangedChecksum|TestPostgresMigrationRejectsUnknownVersion|TestPostgresMigrationRollback|TestPostgresMigrationRejectsInsufficientPrivilege)$' -count=1 -v
go test ./internal/config -run '^(TestLoad.*StateBackend|TestLoad.*StatePath|TestLoad.*StateDSN|TestLoad.*StateEncryption|TestLoad.*StoreStartupTimeout)' -count=1 -v
```

这些测试在 S0 中尚不存在；名称用于保留精确 S1 验收面，不表示已经实现。

## 精确 S2 Pilot 验证命令

S2 必须先机械证明所有 File 方法的 transaction gate，再证明选定的 ISCP
onboarding pilot。PostgreSQL 命令保持与当前完全相同的 DSN gate。

```bash
npm run setup:document-tools
cd services/gateway
go test ./internal/store -run '^(TestS0StoreMethodCatalogCharacterization|TestS0RepositoryCharacterizationMatrixCompleteness|TestS0BackendNeutralRepositorySuccessAndAbsence|TestS0SnapshotShapeCharacterization|TestS0BackendNeutralContractCharacterization|TestFileStoreTransactionGate.*|TestFileStoreRollback.*|TestFileStoreUnknownOutcome.*|TestISCPOnboardingRepository.*)$' -count=1 -v
go test -race ./internal/store -run '^(TestS0BackendNeutralRepositorySuccessAndAbsence|TestS0BackendNeutralContractCharacterization|TestFileStoreTransactionGate.*|TestFileStoreRollback.*|TestISCPOnboardingRepository.*)$' -count=1
SPARKCLAW_TEST_POSTGRES_DSN='postgres://sparkclaw:sparkclaw@127.0.0.1:15432/sparkclaw_test?sslmode=disable' go test ./internal/store -run '^(TestPostgresStorePersistsOnlyISCPOnboardingReceipt|TestPostgresISCPOnboardingRepository.*)$' -count=1 -v
go test ./...
go vet ./...
```

与 S1 相同，未来 gate/failure 测试名称是验收要求，不是 S0 实现声明。

## 文档验证

每次编辑英文/中文后都运行 `.github/workflows/ci.yml` 中 `Docs` job 的精确
inline Python。最终 S0 验证还运行：

```bash
git diff --check
git status --short
cd services/gateway
go test ./internal/store -run '^TestS0' -count=1 -v
go test -race ./internal/store -run '^TestS0(BackendNeutralContractCharacterization|BackendNeutralRepositorySuccessAndAbsence)$' -count=1
go test ./...
go vet ./...
```

## 未解决风险

- 因缺少 DSN，未执行 PostgreSQL runtime 行为刻画。静态协调和未改变的 skip
  suite 不能替代已配置数据库运行。
- PostgreSQL DML 效果、生成的约束名、已部署数据库状态和冲突对象的
  `IF NOT EXISTS` 接管明确不在静态解析器范围内。manifest 会把这些与已解析
  且无差异或无实例的类别区分开。
- 48 个 File 和 33 个 PostgreSQL 静默失败点按 S0 范围保留为生产缺陷。
  未检查 row-loop 和 lookup-to-absence 路径也仍存在。
- Client、Conversation、Run、Approval、Schedule、Connector、
  PassiveNotification、DeliveryRecord、BrowserState、Memory、Audit 和
  Evaluation 记录当前会从 Memory 与运行中的 File decorator 暴露进程内
  可变 alias。S0 只记录不修复；每个所属 repository wave 必须用隔离断言替换
  该缺陷证据。
- Timeout 默认值是基于推理的保守边界；只有 S1/S2 配置环境证据才能验证或
  修订。
- `DeleteSession` 是宽跨 repository 事务。S1/S3 不得让 FK 行为或 repository
  提取拆散该事务。
- 旧 Weixin Snapshot 字段、PostgreSQL 表和 copy-forward SQL 保持兼容状态，
  直到单独审查的 migration 证明可以删除。
- 生产消费者矩阵按当前设计很宽。Repository 提取必须引入消费者自有
  composite，不得使用 optional type assertion 或 runtime service locator。
- 共享 S0 harness 具有代表性；受守卫的逐 repository 矩阵才是完整性权威。
  S2/S3 迁移各 repository 时必须增加失败契约证据，且不得弱化 S0
  applicability 记录。

## 审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| 设计/启动授权 | 用户授权 | 仅 S0 `GO` | 已授权 S0 范围和人工辅助阶段验收；不授权 S1 | 用户 / 2026-08-20 |
| 实现 | 候选 SHA 待填写 | `pending` | 清单、测试、基线和风险记录等待人工检查 | pending |

## 链接

- [S0 契约清单](store-s0-contract-inventory.md)
- [S0 PostgreSQL 协调清单](store-s0-postgresql-reconciliation-manifest.md)
- [Store 契约基础](store-contract-foundation-design.md)
- [Store 可靠性路线图](store-contract-reliability-migration-design.md)
- [Repository 迁移设计](store-repository-migration-design.md)
