# Store 可靠性迁移路线图

> 语言：[English](../../docs/store-contract-reliability-migration-design.md) | 简体中文

> 状态：分阶段路线图草案，2026-08-19。本文档本身不授权任何 runtime
> 变更。每份关联设计和每个实现结果都必须得到独立且有记录的审查结论，
> 才能开始下一个 Store 阶段。

## 目的

Store 可靠性工作拆分为多个可审查的问题，不再使用一份覆盖所有实现的计划。
本路线图只负责顺序、依赖和阶段门禁；每份关联设计分别负责自己的契约。

Store 工作将修复 File/PostgreSQL 静默丢弃错误的问题，引入领域 repository
小接口，删除宽泛的 `store.Store`，然后才在组装层增加 Runtime 统一监管。
PostgreSQL schema/配置是 Store 的前置工作。ASR CI 是独立交付，不作为
Store 迁移门禁。

大型文件拆分明确不在当前范围内。Store 迁移和监管全部完成后，再重新盘点
仓库并为必要的职责拆分编写新设计。本路线图不预先批准任何文件拆分。

## 文档集合

| 文档 | 负责的决策 |
|---|---|
| [Store 契约基础](store-contract-foundation-design.md) | 现有行为刻画、repository 归属、错误/context 规则，以及 mutation/reconciliation 矩阵 |
| [File Store 持久性](store-file-durability-design.md) | 读隔离、串行提交、崩溃阶段、回滚和确定性失败注入 |
| [PostgreSQL schema 与状态配置](store-postgresql-schema-config-design.md) | 内嵌迁移权威、现有数据库接管、严格状态配置和保持不变的 DSN 门控测试 |
| [Repository 迁移](store-repository-migration-design.md) | 单 repository 实现波次、临时宽接口规则和最终删除 `store.Store` |
| [Runtime 与统一监管](store-runtime-supervision-design.md) | 迁移后的 Runtime 组装、有限操作监管、健康、指标、探针和生命周期 |
| [ASR runtime CI](asr-runtime-ci-design.md) | 独立的 fake-model ASR 依赖和 CI 契约 |

## 审查协议

每个 Store 阶段都有两个强制决策：

1. **设计审查**：确认范围、不变量、失败语义、测试计划、回滚和兼容性。
   只有记录为 `GO` 后，才开始该阶段代码。
2. **实现审查**：根据已接受设计检查 diff 和证据。只有记录为 `GO` 后，
   才开始下一阶段。

审查结果只能是：

- `GO`：所有强制证据齐全，且没有跨越下一阶段边界的未解决正确性问题；
- `REVISE`：阶段保持进行中，必须修改设计或代码；
- `STOP`：关键假设失效，必须重新规划后续阶段。

审查记录包含日期、被审查的 commit 或文档修订、结论、证据、未解决风险和
审查人。口头假设、仅通过 build，或缺少 PostgreSQL DSN，都不构成 `GO`。

## Store 阶段顺序

| 阶段 | 交付物 | 进入条件 | 退出条件 |
|---|---|---|---|
| S0 | 契约基础 | 路线图已审查 | repository 目录和命令矩阵已接受；行为刻画证据为绿色 |
| S1 | PostgreSQL schema/状态基础 | S0 实现 `GO` | 唯一迁移权威、严格 Store 配置、全新/当前数据库证据 |
| S2 | File 事务隔离和 pilot repository | S1 实现 `GO` | 所有 File 方法经过同一事务 gate；一个已接受的低风险 repository 证明提交/回滚和三后端迁移 |
| S3 | 剩余 Repository 波次 | S2 实现 `GO` | 每个剩余领域 repository 已同步迁移 Memory、File、PostgreSQL 和调用者 |
| S4 | 删除宽泛 Store | 最后一个 S3 repository `GO` | 删除 `store.Store`；消费者只使用最小 repository 或本地组合接口 |
| S5 | Runtime/Supervisor | S4 实现 `GO` | 组装层专用 Runtime、有界监管、健康、指标、探针和关闭逻辑已接受 |
| S6 | Store 收尾 | S5 实现 `GO` | 长期规则已并入当前手册；临时计划已删除 |

阶段标签表示依赖，不表示可以合并 commit。行为修复、接口迁移、schema 变更
和机械移动仍然是不同主题。

## 全局不变量

- 每个已迁移 repository 的 Memory、File、PostgreSQL 必须同步修改。
- 已迁移查询不得把后端失败转换为缺失或空列表。
- File 读取不得观察到仍可能回滚的 mutation。
- 持久命令成功意味着权威状态及其必需生命周期记录已经持久化。
- 未知结果必须先 reconciliation 再重试，不能报告为成功或确认回滚。
- 生产消费者不得通过类型断言发现 repository，也不得持有
  `*store.Runtime`。
- PostgreSQL CI 配置和 `SPARKCLAW_TEST_POSTGRES_DSN` skip 行为保持不变。
  需要 PostgreSQL 证据的阶段在记录真实配置运行前不得通过审查。
- Store 行为修复完成前，不设计也不实现按职责的大型文件拆分。

## 范围边界

包含：

- Store 契约、调用者、三个后端、File snapshot 持久性、PostgreSQL
  migrations、Store 专属配置、操作监管、readiness 和 Store 生命周期；
- 当前由 Store 管理的 artifact metadata 记录；
- 通过独立文档交付的 ASR fake-model CI。

完成后另行设计的范围：

- 拆分 `memory.go`、`file.go`、`postgres.go`、Gateway handlers、
  `useVoiceInput.ts` 或通用配置文件；
- 全局替换所有宽松环境变量解析器；
- Store metadata 之外的 artifact 对象后端构造；
- ORM、event sourcing、分布式事务、依赖注入框架或通用 repository 生成器；
- 修改 PostgreSQL CI 服务拓扑。

## 回滚

S0-S4 保持 File snapshot 布局不变。只要 repository 波次不包含持久 schema
变更，就可以独立回滚。PostgreSQL migration 只能前进，必须是增量、事务化
并受 checksum 保护；应用回滚不得删除已成功迁移的数据库内容。

任何后续阶段回滚都不得恢复静默丢弃持久化错误的行为或第二份 schema 权威。

## 完成条件

只有 S6 完成后 Store 工作才结束。长期规则随后并入 `architecture.md`、
`development.md`、部署文档和 Store 专项手册，再同时删除这些临时 Store
设计及中文镜像。

只有完成上述收尾后，新的文件尺寸和职责盘点才能决定是否需要拆分模块。
