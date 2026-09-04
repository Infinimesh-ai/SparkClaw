# 暂缓的邮箱扩展、日历与 Knowledge 能力

> 语言：[English](../../docs/deferred-email-calendar-knowledge.md) | 简体中文

## 当前状态

通用邮箱、日历和 Workspace Knowledge/RAG 原型已于 2026-07-16 从 SparkClaw 活动架构中
移除。它们是跨层占位实现，不是完整产品能力。

2026-09-03，SparkClaw 引入了一个全新且刻意收窄的活动邮箱切片：`browser.email` r1
可以通过刚刚校验的已配置 QQ 邮箱、Outlook 或 Gmail 浏览器账户，发送一封经审批的纯文本
邮件。其登录、Host-CDP、提供方脚本、审批和未知结果契约见
[浏览器邮箱 Workflow](browser-email-workflow-design.md)。

该仅发送能力不会重新启用旧 Personal Data Connector。邮件读取和更广泛的邮箱操作仍保持
暂缓；日历和内置 Workspace Knowledge/RAG 也保持暂缓。

独立 Embedding Lane 继续用于 Semantic Routing，不归已移除的 Knowledge 原型所有，也不由其扩展。

## 已退役原型范围

2026-07-16 的移除包括：

- 搜索、读取 Thread、生成回复草稿和 Mock Send 等旧 Personal Data 邮箱操作；
- 日历 Read、Propose Event 和 Create Event 操作；
- Knowledge Workspace Index 与 Search 操作；
- `internal/personaldata` 下基于文件 Fixture 和假定 HTTP API 的 Adapter；
- Mock Outbox 与 Created-event Log；
- 本地关键词索引、可选 Embedding/Reranking 拼装、Document/Chunk 状态和 PostgreSQL
  Vector Schema；
- 专用路由启发式、Mock Model Action、Answer Formatter、Skill、配置项、Policy 项和
  Golden Case。

当前 ToolHub 名称 `email.send` 是新的严格 Browser-bound 工具，只能在新鲜准入和 Owner
精确内容审批后的 `browser.email` 中执行，与已退役的 Mock/HTTP Adapter 契约不兼容。

## 旧原型为何不足

### 通用邮箱

File Adapter 只会查询 JSON Fixture，并把发送追加到本地 JSONL。HTTP Adapter 假设存在
提供方 Endpoint，却没有定义账户授权、邮箱身份、分页、MIME、附件、草稿状态、投递语义、
提供方错误映射或发送结果未知后的对账。在 Mock Append 外套一层审批并不会得到真实邮箱系统。

当前活动浏览器发送切片只完成了有界的单收件人发送契约，不表示收件箱读取、搜索、回复、
附件、草稿同步或多账户语义已经完成设计。

### 日历

File Adapter 只按字符串过滤 Fixture 并追加事件；HTTP Adapter 假定一个通用事件 Endpoint。
实现没有账户生命周期、Provider Capability Model、时区/夏令时契约、重复事件模型、参与者与
更新语义、冲突策略、幂等创建键，以及部分失败后的对账机制。

### Knowledge/RAG

原实现把 Workspace 遍历、文本切块、本地 JSON 索引、可选 Embedding、Reranking、Artifact
归档和三种 Store Backend 塞进两个工具。它缺少 Corpus Model、来源所有权与访问规则、支持
格式策略、增量更新/删除生命周期、Embedding 迁移、质量与延迟预算、运维可观测性，以及稳定
引用契约。

## 现有数据

2026-07-16 的移除没有自动删除历史数据：

- 旧 File State 中的 `documents` 和 `document_chunks` 字段在加载时会被忽略；
- 已存在的 `.sparkclaw/knowledge.json`、Personal Data Fixture、Mock Draft、Outbox/Event
  Log 和已归档 Knowledge Artifact 会作为普通文件保留，直到 Operator 备份或清理；
- 已存在的 PostgreSQL `documents` 和 `document_chunks` 表不会被启动迁移自动删除，
  新数据库不会创建这些表。

活动浏览器邮箱的提供方设置是独立的非敏感记录，只包含启用/默认状态、脱敏就绪信息和版本；
提供方认证仍只保留在专用 Chromium Profile 中。

## 未来扩展门槛

任何新的邮件读取/邮箱管理能力、日历能力或 Workspace Knowledge 能力，都必须从聚焦设计开始，
并作为完整纵向切片落地。至少需要定义：

1. Owner/Account 身份、授权、Credential 归属、生命周期和 Trust Boundary；
2. Provider-neutral 契约、错误分类、Deadline、Retry/Idempotency 规则，以及外部效果不确定时
   的 Reconciliation；
3. 基于真实 Provider 行为的 Policy 和 Approval 语义；
4. Memory、默认 File Backend 与 PostgreSQL 的存储归属和 Migration，包括删除与升级行为；
5. Catalog、Semantic Routing、Workflow Profile、Tool Exposure 和结果投影，不能增加平行
   名称清单或通用回退执行；
6. 默认配置下的端到端测试、确定性 Provider Contract Test 和 Operator 可见健康状态；
7. 邮箱扩展还必须定义读取副作用、Mailbox/Message 身份、MIME 与附件边界、Reply/Draft 行为
   和账户选择；
8. Knowledge 还必须定义 Corpus 生命周期、格式、增量索引、Embedding/Version 迁移、检索
   评测、引用保证和资源预算。

在满足这些门槛前，应把活动浏览器邮箱发送切片、浏览器页面 Workflow、文件/文档 Workflow、
日历请求和 Memory 保持为彼此独立的有界领域，不要重新创建已退役原型作为占位实现。
