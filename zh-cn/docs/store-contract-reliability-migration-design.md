# Store 可靠性迁移路线图

> 语言：[English](../../docs/store-contract-reliability-migration-design.md) | 简体中文

> 状态：S2 已在 `42b62bd` 获得接受，S3 OwnerRepository 已在 `0b85cc4`
> 获得接受，S3 ClientRepository 已于 2026-08-20 在 `a4ddc83` 获得接受。
> CredentialRepository 合同修订 8 在审查 1-7 返回 `REVISE` 后，于 `b0884f6`
> 获得 `GO`。该 GO 授权 live Credential foundation checkpoint，随后完成
> ConnectorRepository lifecycle migration，最后执行 integrated Credential gate。
> 2026-08-21，owner 采用 Repository 迁移设计中的 S3 风险分级策略；已完成波次
> 保持最终状态。

## 目的

Store 可靠性工作拆分为多个可审查的问题，不再使用一份覆盖所有实现的计划。
本路线图只负责顺序、依赖和阶段门禁；每份关联设计分别负责自己的契约。

Store 工作将修复 File/PostgreSQL 静默丢弃错误的问题，引入领域 repository
小接口，删除宽泛的 `store.Store`，然后才在组装层增加 Runtime 统一监管。
PostgreSQL schema/配置是 Store 的前置工作。

大型文件拆分明确不在当前范围内。Store 迁移和监管全部完成后，再重新盘点
仓库并为必要的职责拆分编写新设计。本路线图不预先批准任何文件拆分。

## 文档集合

| 文档 | 负责的决策 |
|---|---|
| [Store 契约基础](store-contract-foundation-design.md) | 现有行为刻画、repository 归属、错误/context 规则，以及 mutation/reconciliation 矩阵 |
| [S0 PostgreSQL 协调清单](store-s0-postgresql-reconciliation-manifest.md) | 根 migration 与 Go 内嵌 schema 的约束级静态比较，以及显式解析边界 |
| [File Store 持久性](store-file-durability-design.md) | 读隔离、串行提交、崩溃阶段、回滚和确定性失败注入 |
| [PostgreSQL schema 与状态配置](store-postgresql-schema-config-design.md) | 内嵌迁移权威、现有数据库接管、严格状态配置和保持不变的 DSN 门控测试 |
| [Repository 迁移](store-repository-migration-design.md) | 单 repository 实现波次、临时宽接口规则和最终删除 `store.Store` |
| [CredentialRepository contract](store-credential-repository-design.md) | Credential 持久化语义、redaction、Vault/Weixin 迁移、reconciliation 与全 backend 实现门禁 |
| [Runtime 与统一监管](store-runtime-supervision-design.md) | 迁移后的 Runtime 组装、有限操作监管、健康、指标、探针和生命周期 |

## 审查协议

每个 Store 阶段都有两个强制决策：

1. **设计审查**：确认范围、不变量、失败语义、测试计划、回滚和兼容性。
   只有记录为 `GO` 后，才开始该阶段代码。
2. **实现审查**：根据已接受设计检查 diff 和证据。只有记录为 `GO` 后，
   才开始下一阶段。

强制证据的数量由 Repository 迁移设计中的 P0/P1/P2 operation 等级决定。P2
审查不得仅为形式统一而要求 P0 的恢复、故障注入、configured PostgreSQL 或 race
证据。

审查结果只能是：

- `GO`：所有强制证据齐全，且没有跨越下一阶段边界的未解决正确性问题；
- `REVISE`：阶段保持进行中，必须修改设计或代码；
- `STOP`：关键假设失效，必须重新规划后续阶段。

审查记录包含日期、被审查的 commit 或文档修订、结论、证据、未解决风险和
审查人。口头假设、仅通过 build，或缺少 PostgreSQL DSN，都不构成 `GO`。

S2 之后，owner 把中间阶段的 `GO`/`REVISE` 决策授权给 primary implementation
agent。每个被授权的决定仍必须具备已接受契约、完整自动化证据和 context-isolated
gatekeeper 审查。跨阶段风险继续保留记录，并在完整计划收口时统一交给 owner
审查；只有新发现的产品边界决策才会在此之前返回 owner。

## Store 阶段顺序

| 阶段 | 交付物 | 进入条件 | 退出条件 |
|---|---|---|---|
| S0 | 契约基础 | 路线图已审查 | repository 目录和命令矩阵已接受；行为刻画证据为绿色 |
| S1 | PostgreSQL schema/状态基础 | S0 实现 `GO` | 唯一迁移权威、严格 Store 配置、全新/当前数据库证据 |
| S2 | File 事务隔离和 pilot repository | S1 实现 `GO` | 所有 File 方法经过同一事务 gate；一个已接受的低风险 repository 证明提交/回滚和三后端迁移 |
| S3 | 风险分级的剩余 Repository 波次 | S2 实现 `GO` | 每个剩余领域 repository 已按自身 P0/P1/P2 门禁同步迁移 Memory、File、PostgreSQL 和调用者 |
| S4 | 删除宽泛 Store | 最后一个 S3 repository `GO` | 删除 `store.Store`；消费者只使用最小 repository 或本地组合接口 |
| S5 | Runtime/Supervisor | S4 实现 `GO` | 组装层专用 Runtime、有界监管、健康、指标、探针和关闭逻辑已接受 |
| S6 | 职责拆分与 Store 收尾 | S5 实现 `GO` | 复杂 Store module 已按接受的职责边界拆分；长期规则已并入当前手册；临时计划已删除 |

阶段标签表示依赖，不表示可以合并 commit。行为修复、接口迁移、schema 变更
和机械移动仍然是不同主题。

## 全局不变量

- 每个已迁移 repository 的 Memory、File、PostgreSQL 必须同步修改。
- 已迁移查询不得把后端失败转换为缺失或空列表。
- File 读取不得观察到仍可能回滚的 mutation。
- 持久命令成功意味着权威状态及其必需生命周期记录已经持久化。
- P0 未知结果必须先 reconciliation 再重试。P1/P2 传播真实的 unknown outcome，
  且只能通过已有 idempotency/CAS key 重试；不增加专属恢复协议。
- 生产消费者不得通过类型断言发现 repository，也不得持有
  `*store.Runtime`。
- PostgreSQL CI 配置和 `SPARKCLAW_TEST_POSTGRES_DSN` skip 行为保持不变。P0
  必须运行 configured PostgreSQL；P1/P2 只有修改 PostgreSQL schema 或 concurrency
  semantics 时才要求单波次 configured run；所有等级仍执行最终 configured
  integration gate。
- Store 行为修复完成前，不设计也不实现按职责的大型文件拆分。

## 范围边界

包含：

- Store 契约、调用者、三个后端、File snapshot 持久性、PostgreSQL
  migrations、Store 专属配置、操作监管、readiness 和 Store 生命周期；
- 当前由 Store 管理的 artifact metadata 记录。

S6 前或需要另行设计的范围：

- S6 前拆分 `memory.go`、`file.go`、`postgres.go`；
- 拆分 Gateway handlers、`useVoiceInput.ts` 或通用配置文件；
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

S6 首先重新进行文件尺寸与职责盘点，只拆分在 S4/S5 后 ownership boundary 已经
稳定的 Store module；pure move 与 behavior change 仍使用独立 commit。

## 当前审查记录

| 审查 | 修订/commit | 结论 | 证据和未解决风险 | 审查人/日期 |
|---|---|---|---|---|
| S0 设计/启动授权 | S0 计划 | 仅 S0 `GO` | 已授权人工辅助 S0 实现；排除所有后续阶段 | 用户 / 2026-08-20 |
| S0 实现 | `207462154fa2377ed786af671f41e0f353d11ba9` | `GO` | 清单、manifest、测试、基线和已分配残余风险已接受；可开始 S1 | 用户 / 2026-08-20 |
| S1 设计 | `361612c` | `GO` | 三轮独立设计审查接受 migration 权威、adoption、配置、失败和 PostgreSQL 验证契约 | 独立 gatekeeper；用户授权实现 / 2026-08-20 |
| S1 实现 | `b2f9115` | `GO` | 独立实现审查发现并闭合 legacy 重复键阻断；用户接受绿色实现并授权 S2 设计 | 独立 gatekeeper 和用户 / 2026-08-20 |
| S2 设计 | `49b0858` | `GO` | 四轮审查关闭 File fence admission、pending authority-ticket retry、PostgreSQL 最终对账和 isolation-default blocker；可开始实现 | 独立 gatekeeper / 2026-08-20 |
| S2 实现初次审查 | `9d86c50` | 已被取代的 `GO` | 初次审查接受了实现证据；之后的新审查取代了这一决定 | 独立 gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S2 实现重新审查 | `9d86c50` | `REVISE` | 持久化/对账后仍使用请求开始时间判断 ticket 过期，且新审查缺少已记录的真实 DSN 证据 | Context-isolated gatekeeper / 2026-08-20 |
| S2 修复实现 | `bc1bfb4`, `6f4c1bf`, `437e4bc`, `42b62bd` | `GO` | ticket 披露前立即重读 live clock；新增同一调用内过期及 File destination/directory 缺失故障覆盖；把 read/write timeout override 转发给 Compose 并增加展开测试；focused/full/race/default-File/WebChat/docs/Compose 和独立重复的一次性真实 PostgreSQL 门禁通过 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Owner 实现 | `0b85cc4`，gate record `fc5acba` | `GO` | context-isolated 修复审查关闭 unsafe pre-candidate ownership 与 terminate-not-release 证据，完整本地及 disposable PostgreSQL gate 通过 | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Client 实现 | `1acdd2f`，修复 `a4ddc83` | `REVISE` 后 `GO` | 修复替换不可取消的 PostgreSQL admission，并纠正 acquired-session `Begin` classification；exact 修复候选通过 full normal/race 与 configured PostgreSQL full/race gate | Context-isolated gatekeeper 和获 owner 授权的 primary agent / 2026-08-20 |
| S3 Credential contract 审查 1 | `de4cd93` | `REVISE` | 修订 1 缺少 operation identity、conditional/pending deletion、File non-rollback high-water 一致性、lifecycle ownership 与精确安全 error projection | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 2 | `1d646f0` | `REVISE` | 修订 2 未保留 success 后的 operation replay、未把 active rewrap 与 orphan cleanup 分开，也未在 delete version 中编码全部已接受时间 | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 3 | `b6def5d` | `REVISE` | 修订 3 在没有 durable tombstone 时过度承诺 post-delete operation identity，并允许 Delete 把 caller identity 复用于另一 ref | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 4 | `30cbf24` | `REVISE` | binding identity 在 adapter Start/Seal 后才从 process memory 持久化，因此 crash 会遗留 secret 并丢失 replay identity；Weixin compensation 也没有阻止 Poll/Seal reuse 的 durable terminal transition | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 5 | `4d54acf` | `REVISE` | Credential code 被阻塞到 Connector GO，但 Connector recovery 已要求新的 AbortSeal，并且旧文本仍把 cross-restart cleanup 交给 volatile Vault state | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 6 | `3c86739` | `REVISE` | foundation 在没有所需 exact Connector proof 时调用 AbortSeal，因此 concurrent stale compensation 可能删除 active credential；repository 顺序也重复安排了 Connector | Context-isolated gatekeeper / 2026-08-20 |
| S3 Credential contract 审查 7 | `8ef063f` | `REVISE` | migration roadmap 仍授权 foundation AbortSeal；legacy revoke 可在没有 durable transition proof 时删除 credential，推迟后 public Vault Delete 又没有合法 caller | Context-isolated gatekeepers / 2026-08-20 |
| S3 Credential contract 审查 8 | `b0884f6` | `GO` | foundation 没有 public cleanup dead code，private reconciliation 保持 live；ambiguous legacy start/revoke 保留 credential；Connector 以 exact durable barrier 拥有 Delete/AbortSeal | Context-isolated gatekeeper / 2026-08-20 |
| S3 验证策略 | owner 指示 | `GO` | 后续波次使用 P0/P1/P2 operation 风险和 aggregate boundary；已完成波次不重开，P1/P2 不再继承最高规格恢复与证据 | 用户 / 2026-08-21 |
