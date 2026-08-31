# 有界上下文历史读取与 Invocation 快照设计

> 语言：[English](../../docs/context-history-query-design.md) | 简体中文

状态：设计经接受与最小架构复审后，于 2026-08-31 完成实施。

本文解决 Agent 上下文组装中的无界 Store 读取问题。SparkClaw 继续保存完整持久化 session 历史，
但一次模型 invocation 只读取有界的近期候选，并且只选择一次现有固定上下文：8 条消息、6 条工具
调用、4 条 episode summary 和 3 张图片。

[模型容量契约](model-capacity-contract-design.md)负责 token admission；
[上下文组装方案](context-assembly-plan.md)负责语义 section 与渲染。本文只负责历史获取、选择为一份
invocation-owned 值，以及复用该值。

## 1. 已接受决策

| 关注点 | 已接受决策 |
|---|---|
| 持久化历史 | 完整保留 message、tool call、episode、run 与 audit；不能为了优化 prompt 删除产品历史 |
| 现有完整列表 API | 保留 `ListMessages`、`ListToolCalls` 和 `ListEpisodeSummaries`，供确实需要完整历史的消费者使用 |
| Agent 热路径 | 增加三个要求 session、按新到旧、一次性返回且具有硬 scan limit 的 recent query |
| 分页 | 不新增通用 cursor 或 page 抽象；每个历史流在一次 invocation 中最多执行一次有界查询 |
| 选择 | 保持当前语义过滤和固定的 8/6/4/3 选择数量 |
| 快照 | 构建一份不可变进程内历史值，传给 Tree、Workflow、最终回答组装与 recent-document resolution |
| Resume | 每次恢复 invocation 使用原 run cutoff 和已持久化 source-turn ID 最多重建一次；不持久化重复 prompt 或 history anchor |
| Backend | PostgreSQL 使用 `ORDER BY ... LIMIT`；内存 backend 维护派生 session 顺序；File 从现有 snapshot 重建派生状态 |
| Memory 产品 | 永不查询 `MemoryRepository`；当前记录只是空壳，不进入 Agent 上下文 |
| External MCP | 在发起任何历史查询前返回空 snapshot |

## 2. 当前问题为何出现

问题不是数据库记录了每一次事件。完整持久化记录服务于会话展示、审计、恢复、投递、反馈等产品
行为，本身应当保留。真正的问题是模型上下文构建调用完整列表 repository method，所有匹配记录越过
Store 边界后，才应用很小的上下文限制：

```text
完整持久化 session 历史
        |
        | ListMessages / ListToolCalls / ListEpisodeSummaries
        v
读取、排序、传输并解码全部匹配记录
        |
        v
Agent 再过滤到 8 条消息 / 6 条工具 / 4 条 episode / 3 张图片
```

Session 越长，PostgreSQL 的排序和结果传输越大；内存 backend 会复制并排序完整集合；File backend
载入 snapshot 后又委托给同一内存路径。Tree、Workflow、最终回答与 recent-document fallback 还
可能在一个 run 中重复构造或重复读取重叠历史。

扩大模型上下文窗口不能解决它，因为浪费发生在 token admission 之前，而且产品仍只选择固定近期
上下文。正确边界是有界的数据访问契约，而不是删除持久化记录，也不是扩大模型可见历史。

## 3. 范围与非目标

本文覆盖 `buildAgentContextSnapshot` 和 recent-document fallback 当前使用的历史输入：

- 同 session 的先前 owner/assistant message；
- 从这些 message 派生的图片附件；
- 先前 run 的 terminal tool call；
- 已持久化 episode summary；
- 一次 invocation 内对这些候选与选中记录的复用。

本文不做以下事项：

- 不改变 message、tool call、episode、run 或 audit 的保留规则；
- 不为 UI、export 或管理功能增加通用 Store 分页；
- 不做语义搜索、embedding retrieval、RAG 或历史摘要；
- 不增加固定的 8/6/4/3 上下文选择；
- 不持久化渲染 prompt、选中记录副本、cursor、selection revision 或 run-history anchor；
- 不查询产品 Memory repository；
- 不定义 ContextBuilder 降级或模型 token 预算。

## 4. 最小 Repository 契约

Agent 热路径在已有记录所属 repository 上增加以下窄接口：

```go
ListRecentMessages(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    excludeMessageID string,
    scanLimit int,
) ([]app.Message, error)

ListRecentToolCalls(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    excludeRunID string,
    scanLimit int,
) ([]app.ToolCall, error)

ListRecentEpisodeSummaries(
    ctx context.Context,
    sessionID string,
    cutoff time.Time,
    scanLimit int,
) ([]app.EpisodeSummary, error)
```

每个方法只有一种语义：

- `sessionID` 必填且精确匹配；空值非法；
- `cutoff` 必填且为包含边界；
- Agent 获取时 `excludeMessageID` 与 `excludeRunID` 必填；exclusion 在 Store 内、`LIMIT` 前应用；
- `scanLimit` 是必填正数实现限制，并受配置最大值约束；零值非法，绝不表示 unlimited；
- 结果按确定性的 timestamp + ID 顺序从新到旧返回；
- 返回值遵循现有 repository ownership 规则进行 clone；
- cancellation、timeout 和类型化 Store error 与现有 repository 契约一致。

不新增 `HistoryCursor`、`Page`、`Next` 或 offset。Agent 不需要通用浏览协议，只需要一次有界的近期
候选集合。符合语义条件的记录稀疏时，selection 可以少于配额，但不能为寻找更老记录而无界扫描。

初始 scan ceiling 是独立于模型容量的实现常量：

| 历史流 | 最多读取候选 | 最多模型可见选择 |
|---|---:|---:|
| Message 及其图片 | 256 | 8 条 message、3 张图片 |
| Tool call | 128 | 6 条 tool call |
| Episode summary | 64 | 4 条 episode |

这些 ceiling 为语义过滤留出空间，同时阻止上下文选择退化为完整历史扫描。只有 Store latency 与
长 session 引用证据才能推动修改；改变模型物理窗口不能修改它们。

## 5. Eligibility 与稳定边界

首次执行时：

```text
cutoff            = AgentRun.StartedAt
excludeMessageID  = current userMessage.ID
excludeRunID      = current AgentRun.ID
```

Workflow 恢复 invocation 时：

```text
cutoff            = original AgentRun.StartedAt
excludeMessageID  = Workflow Intent.SourceTurnID
excludeRunID      = original AgentRun.ID
```

使用原始 cutoff，防止执行开始后创建的 message、episode 或其他 run 活动进入恢复 prompt。显式排除
source turn，防止同一 owner 问题同时出现在 fixed current input 和历史对话中。排除当前 run，防止
当前 run 工具结果泄漏到跨 run 上下文；它们仍通过 current-run observation 路径提供。

Store 侧顺序与 eligibility 为：

- message：`created_at <= cutoff`，排除 `excludeMessageID`，按
  `(created_at DESC, id DESC)` 排序；
- tool call：先前 run 的 terminal 记录，`started_at <= cutoff` 且 `completed_at <= cutoff`，排除
  `excludeRunID`，按 `(started_at DESC, id DESC)` 排序；
- episode：`created_at <= cutoff`，按 `(created_at DESC, id ASC)` 排序，以保持现有 repository
  契约。

只有具有 content 或合法 attachment 的 `user`/`assistant` message 进入选中对话。现有图片类型判断、
工具投影规则和 episode 有效性规则仍属于 Agent 语义。Repository 只提供有界候选，不学习 prompt
policy。

在正常不可变历史与 terminal tool record 下，该边界是确定性的。对 cutoff 之前记录进行管理性
backfill 或纠正，可能改变恢复时重建的 snapshot；精确 audit replay 不是该临时上下文值的目标，
应使用持久化 run/audit record 完成。

## 6. Backend 实现

### PostgreSQL

每个方法执行一条带索引的 `ORDER BY ... LIMIT` 查询。查询在排序和 limit 前应用 session、cutoff、
exclusion 与 terminal-state predicate。代表性形态为：

```sql
SELECT ...
FROM messages
WHERE session_id = $1
  AND created_at <= $2
  AND id <> $3
ORDER BY created_at DESC, id DESC
LIMIT $4;
```

```sql
SELECT ...
FROM tool_calls
WHERE session_id = $1
  AND run_id <> $2
  AND started_at <= $3
  AND completed_at IS NOT NULL
  AND completed_at <= $3
ORDER BY started_at DESC, id DESC
LIMIT $4;
```

索引应匹配查询实际使用的 filter 与 order。Migration 必须用长 session fixture 的
`EXPLAIN (ANALYZE, BUFFERS)` 证明；不能仅因设计文档提到索引就增加索引。

### 内存 Store Backend

内存 Store backend 为 message、terminal tool call 与 episode 维护按 session 分组的派生顺序。普通
单调写入走 append；replay/backfill 在正确位置插入；update 不得重复 ID。Recent read 定位 cutoff，
只遍历到 `scanLimit`，并且只 clone 返回记录。

这是 backend 索引，不是产品 Memory 功能。Agent 上下文仍不使用 `MemoryRepository`。

### File Store Backend

File 继续以当前 durable snapshot 作为唯一持久化表示。Load 或 replacement 时，重建与 MemoryStore
相同的派生内存顺序。不持久化第二份历史索引、cursor 或选中 snapshot，避免新增 reconciliation 与
durability 问题。

三个 backend 共用 ordering、cutoff、exclusion、limit、clone、cancellation 与空结果 repository
contract test。

## 7. Invocation-Owned Snapshot

Owner-question gate 通过后，Runtime 最多获取一次有界候选，并构造一份不可变值：

```go
type InvocationHistory struct {
    MessageCandidates  []app.Message
    ToolCandidates     []app.ToolCall
    EpisodeCandidates  []app.EpisodeSummary
    Selected           agentContextSnapshot
}
```

实际类型可以继续是 `agent` 私有类型；重要的是所有权契约：

1. 在任何 Store query 前拒绝 external-MCP 历史继承；
2. 解析首次执行或 resume 的 cutoff 与 exclusion；
3. 每个历史流执行一次有界查询；backend operation ownership 允许时可并行；
4. 只运行一次现有语义 filter，并冻结选中的 8/6/4/3 slice；
5. 把 `Selected` 显式传给 Tree、Workflow step、conversation/final answer 和其他模型上下文 builder；
6. recent-document fallback 检查已经加载的有界候选，不再次调用 `ListToolCalls`。

因此 Tree 与 Workflow 引用相同的选中历史记录，但不要求渲染出逐字节一致的 prompt：每个消费者
仍拥有自己的语义 section、label、schema 与 fixed instruction。

该值只活在当前 Runtime invocation 中，不是全局 cache，也没有 invalidation protocol。Workflow
各 step 复用它；current-run observation 在独立路径上继续增长。

## 8. Resume 语义

Approval 或 login resume 开始新的 Runtime invocation，原进程内值可能已经消失。Runtime 使用
Workflow 已经需要的持久化事实，最多重建一次 `InvocationHistory`：

- 原始 `AgentRun.StartedAt` 提供 cutoff；
- `Intent.SourceTurnID` 提供当前 owner message exclusion；
- 原始 `RunID` 提供 current-run exclusion。

不向持久化 schema 增加 `RunHistoryAnchor`、cursor、selection revision 或 prompt 副本。这样 resume
正确性仍绑定现有 run identity，不引入第二份 durable history 表示。

Workflow resume 不重新运行 Tree。重建的选中 snapshot 只供恢复的 Workflow 与其最终回答使用。
所需持久化事实缺失或非法时，以类型化 internal-state error 失败，不能退回完整历史读取。

## 9. 失败与可观测性

Recent-query 失败时，context acquisition 按对应现有 Store failure 失败。除有意的 external-MCP
隔离规则外，系统不能静默替换为空 snapshot。

有界 telemetry 只记录安全元数据：

- backend 与 stream；
- 返回候选数与配置 scan ceiling；
- 选中的 message、tool、episode 与 image 数量；
- 是否达到候选 ceiling；
- initial 或 resume invocation；
- query latency 与类型化 failure code。

不得记录历史正文、attachment path、document name、含 owner text 的 query argument 或选中 record
ID。达到候选 ceiling 是预期的有界行为，不自动算作错误，也不能因此继续分页。

## 10. 实施记录

### 已实施：特征锁定与注册

- 锁定当前语义 eligibility 与 8/6/4/3 selection fixture；
- 注册三个 recent-query Store operation 及 timeout/risk metadata；
- 在修改 Agent caller 前增加共享 contract case。

### 已实施：Backend Query

- 实现 PostgreSQL 有界 query 并验证执行计划；
- 为内存 backend 增加按 session 派生顺序；
- 从 File snapshot 重建派生状态，不增加持久化数据；
- 其他消费者的完整列表 method 保持不变。

### 已实施：单一获取路径

- 在 owner-question gate 后构建 `InvocationHistory`；
- 把选中 snapshot 传给 Tree、Workflow 与 final-answer assembly；
- 让 recent-document fallback 消费同一有界候选；
- 删除 Agent 热路径对完整历史列表 method 的调用。

### 已实施：Resume

- 使用 `AgentRun.StartedAt`、`Intent.SourceTurnID` 和 `RunID` 最多重建一次；
- 增加 missing-state 与 no-unbounded-fallback 测试。

## 11. 验证与验收

实施只在以下条件全部满足时验收：

- 长 session 保留完整持久化记录，同时 Agent 对每个流的读取不超过候选 ceiling；
- PostgreSQL 执行计划使用有界索引访问，不排序或传输完整 session；
- 内存与 File read 不 clone 完整 session collection；
- 三个 backend 返回相同的确定性顺序和 exclusion 结果；
- 固定的 8/6/4/3 模型可见选择不变；
- Tree 与 Workflow 消费相同选中 record identity，但不要求字节等价；
- recent-document fallback 不产生第二次 tool-history 读取；
- resume 最多构建一份 snapshot，且不使用完整列表 fallback；
- external-MCP invocation 产生零次历史 query；
- Memory repository 记录永不进入 Agent 上下文；
- owner-question rejection 发生在历史获取前；
- 现有 Policy、Approval、artifact authorization 与 external-MCP isolation 测试保持通过。

运行聚焦的 Store contract 与 Agent snapshot 测试、完整 Gateway build/test/vet、默认 File coverage、
可用时的 PostgreSQL integration、routing golden test 和双语文档检查。

## 12. 所有权边界

- `ConversationRepository`：有界 recent-message 读取；
- `RunRepository`：有界 recent-tool 与 recent-episode 读取；
- MemoryStore/FileStore/PostgreSQL：backend-specific 有界访问与共享 repository 语义；
- `internal/agent`：候选 eligibility、固定选择、invocation 值与显式消费者复用；
- `internal/app`：只使用已有 run start 与 source-turn 事实；不增加历史持久化 schema；
- 容量与 ContextBuilder 文档：在本有界获取之后负责 token admission 与渲染。
