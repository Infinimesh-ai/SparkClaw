# 暂缓的邮件、日历与 Knowledge 能力

> Language: 简体中文 | [English](../../docs/deferred-email-calendar-knowledge.md)

## 当前状态

邮件、日历和 Workspace Knowledge/RAG 已于 2026-07-16 从 SparkClaw 当前架构中移除。它们此前只是原型空壳，并不是经过完整设计的产品能力；继续注册会让 Runtime、存储层、Policy、UI 和评测矩阵呈现出并不存在的完成度。

这些能力不再出现在 ToolHub、Agent Runtime 路由、Skill、公开配置、WebChat 设置和 Golden Eval 中。Owner Profile 仍可保留 email 形式的身份字段；该字段仅是资料元数据，不代表邮件集成。受治理的 Browser Workflow 仍可访问 Owner 已授权的个人网站，但这不会恢复专用邮件或日历 Connector。

独立的 Embedding 模型 lane 继续保留在架构和配置中，供 semantic routing 使用。已删除的 Knowledge 原型不拥有也不扩展该 lane。

## 已移除范围

移除内容包括：

- 工具契约与执行器：`email.search`、`email.read_thread`、`email.draft_reply`、`email.send`、`calendar.read`、`calendar.propose_event`、`calendar.create`、`knowledge.index_workspace` 和 `knowledge.search`。
- `internal/personaldata` 下基于文件 fixture 和假定 HTTP API 的适配器，以及 mock outbox 和 created-event 日志。
- 本地关键词索引、Embedding/Reranking 拼装逻辑、`DocumentStore`、Document/Chunk 状态和 PostgreSQL Document/Vector Schema。
- 专用于这些工具的路由启发式、Mock Model Action、Grounded Answer Formatter、Schema/Repair 特例和审批文案。
- `email_triage`、`calendar_assistant` Skill，个人数据 fixture、WebChat Adapter 展示、环境变量、Policy 项和专用 Unit/Golden Case。

Git 历史保留了代码备份；本文保存原边界及其不能被零散恢复的原因。

## 原型不足之处

### 邮件

File Adapter 只会搜索 JSON fixture，并把“发送”追加到本地 JSONL。HTTP Adapter 假定三个端点，却没有定义账号授权、邮箱身份、分页与 Cursor、MIME 和附件、草稿同步、投递与幂等语义、Provider 错误映射，以及发送结果不确定时的对账机制。给 mock 文件追加操作套上审批，并不会使其成为真实邮件能力。

### 日历

File Adapter 只按字符串过滤 fixture 并追加新事件；HTTP Adapter 假定一个通用事件端点。实现没有账号生命周期、Provider Capability Model、时区与夏令时契约、重复事件模型、参与者与更新语义、冲突策略、幂等创建键，以及部分失败后的对账机制。

### Knowledge/RAG

原实现把 Workspace 遍历、文本切块、本地 JSON 索引、可选 Embedding、Reranking、Artifact 归档和三种存储后端塞进两个工具。它缺少 Corpus/Collection 模型、来源所有权与访问规则、格式策略、增量更新/删除生命周期、Embedding 迁移策略、质量与延迟预算、运维可观测性，以及稳定的引用契约，最终形成了大量跨层耦合，却没有产品级设计。

## 现有数据

本次移除不会自动删除用户数据。

- 旧 File State 中的 `documents` 和 `document_chunks` 字段在加载时会被忽略。
- 已存在的 `.sparkclaw/knowledge.json`、Personal Data Fixture、Draft、Outbox/Event Log 和已归档 Knowledge Artifact 都会作为普通文件保留，直到 Operator 完成备份或手动清理。
- 已存在的 PostgreSQL `documents` 和 `document_chunks` 表不会被启动迁移自动删除；新数据库不再创建它们。Operator 应先导出所需数据，再手动删表。

## 重新引入门槛

未来若重新实现，应先编写聚焦的设计文档，并以完整纵向切片落地。至少需要定义：

1. Owner/Account 身份、授权、Credential 存储、Connector 生命周期和明确的 Trust Boundary。
2. 类型化且 Provider-neutral 的契约、错误分类、Timeout/Retry/Idempotency 规则，以及外部效果不确定时的 Reconciliation。
3. 基于真实 Provider 行为的 Policy 和 Approval 语义，而不是基于 Mock 文件写入。
4. 默认 File Backend、Memory 和 PostgreSQL 的存储归属与迁移，包括删除和升级行为。
5. Intent/Profile/Tool Exposure 集成，不能重新增加平行工具名路由清单。
6. 默认配置下的端到端测试、Connector Contract Test 和 Operator 可见的健康状态。
7. Knowledge 还必须定义 Corpus 生命周期、支持格式、增量索引、Embedding/Version 迁移、检索质量评测、引用保证和资源预算。

在满足这些门槛前，应继续把现有 File Search/Read、Browser Workflow 和 Memory 作为彼此独立且有边界的能力使用，不要用原工具名重新创建占位实现。
