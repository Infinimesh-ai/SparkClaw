# PostgreSQL Schema 与 Store 配置设计

> 语言：[English](../../docs/store-postgresql-schema-config-design.md) | 简体中文

> 状态：S1 设计审查草案，2026-08-19。本阶段先于 File 和 repository 迁移，
> 先为 PostgreSQL 建立唯一且已知的 schema 权威。

## 目标

用一份内嵌、版本化 migration 来源替代重复的 PostgreSQL schema，并让
Store 专属配置在 `config.Load` 阶段失败，而不是到第一次持久化操作时才失败。

本阶段不修改 PostgreSQL CI 服务、`SPARKCLAW_TEST_POSTGRES_DSN` 名称或
测试 skip 行为。

## Schema 权威

`services/gateway/internal/store/migrations/*.sql` 下按顺序排列的 SQL 文件
成为唯一 domain-schema 权威。

1. 将根目录 `migrations/0001_core.sql` 按字节完全相同地移动到 Store package，
   并冻结 checksum。
2. 增加下一份幂等 migration，协调当前只存在于 Go `postgresSchema` 字符串
   中的所有表、column、index 和 constraint。
3. 使用 `go:embed` 内嵌有序文件，并由 Gateway Store startup 执行。
4. 删除 Go `postgresSchema` domain DDL，并停止把另一份 schema 目录复制
   进 PostgreSQL image。
5. 全新数据库和现有 SparkClaw 数据库使用同一个 runner。

runner 只能包含最小 migration ledger bootstrap DDL，不得在 Go 中重述应用表
或索引。

## Migration Ledger

`sparkclaw_schema_migrations` 保存不可变 version、filename、SHA-256
checksum 和应用时间。

- migration 在事务中按 lexical/version 顺序应用；
- 已应用版本 checksum 不同则 startup 失败；
- 出现未知已应用版本则 startup 失败；
- migration 失败时不得写入 ledger row；
- 成功后重启必须幂等；
- migration 和 migration 后校验通过前 readiness 保持 false。

对于没有 ledger 的现有数据库，runner 应用幂等 baseline 和 reconciliation
migration，验证最终 schema 后再记录它们。不得只因某一张表存在就推断成功。

S0 盘点提供精确 reconciliation manifest。删除任一旧来源前，migration 审查
必须将 manifest 同时与旧根 SQL 和当前 `postgresSchema` 比较。

## PostgreSQL 操作边界

startup migration 使用调用者 startup context 和已验证的 fallback deadline。
pool acquisition 和每条 migration statement 都继承该 context。服务端
`statement_timeout` 和 `lock_timeout` 只是后备约束，不能替代调用者取消。

普通 read/write/transaction timeout 字段只在 S2 pilot repository 实际使用
时引入，避免配置声明无效行为。接受的名称和初始值来自 S0。

## Store 配置校验

本阶段的 `config.Load` 只校验 Store 拥有的 setting：

- state backend 必须严格为 `memory`、`file` 或 `postgres`；
- File 要求非空且 normalize 后的 path；
- PostgreSQL 要求非空 DSN；
- File encryption 按现有优先级契约要求一个可用 key source；
- startup timeout 为正且有上界；
- 畸形 Store bool/int override 返回包含准确环境变量名称的错误；
- DSN 和 key 不得进入公共投影或日志。

全局清理所有宽松环境变量解析器，以及 artifact 对象后端构造，都属于独立
工作。本阶段不得扩张为整个 config 的重写。

## 数据库失败契约

migration runner 返回并保留所有 pool、begin、execution、scan、rows、commit
和 rollback 错误。rollback 错误与主诊断 join，但不替代主错误。commit 提交
后 transport 失败属于 `unknown_outcome`；startup 必须通过 migration ledger
和 checksum reconciliation 决定是否重试。

## 验证

需要以下确定性和已配置 PostgreSQL 证据：

- 内嵌 migration 顺序和 checksum unit test；
- source guard 证明 Go 中不再存在 domain DDL；
- 全新空数据库达到预期 manifest；
- 当前无版本数据库在不丢数据的情况下接管 ledger；
- 当前有版本数据库重启时无 DDL churn；
- checksum 变化和未知版本导致 startup 失败；
- 强制 migration 失败会回滚且不留下 ledger row；
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
| 设计 | pending | pending | pending | pending |
| 实现 | pending | pending | pending | pending |
