# SparkClaw 理念与架构收敛设计

版本：v0.1
日期：2026-05-22
参考项目：

- NousResearch/hermes-agent: https://github.com/NousResearch/hermes-agent
- openclaw/openclaw: https://github.com/openclaw/openclaw

## 1. SparkClaw 的核心定位

SparkClaw 不是 Hermes Agent 的学习型通用代理复刻，也不是 OpenClaw 的多渠道个人助手复刻。

SparkClaw 的定位是：

> 面向本地 AI 工作站的可靠 Agent Runtime，用受控工具、私有记忆、可审计执行和评测闭环，把本地模型变成可长期使用的个人工作流系统。

它的产品重心不是“模型能说什么”，而是“模型能在边界内可靠做什么”。

## 2. 从参考项目继承什么

### 2.1 从 Hermes Agent 继承的思想

Hermes Agent 的重要启发是“Agent 会随使用成长”：它强调内建学习闭环、从经验生成技能、使用中改进技能、主动沉淀记忆、跨会话检索历史、构建用户模型、定时任务、子代理并行和轨迹压缩。

SparkClaw 应继承以下设计：

- **经验闭环**：每次任务都留下 trace、工具调用、失败原因、修复过程和用户反馈。
- **记忆候选机制**：Agent 不直接写入长期记忆，而是提出 memory candidate，由用户或策略确认。
- **技能沉淀**：复杂任务完成后，系统可生成 workflow recipe，但进入可执行技能前必须通过评测。
- **轨迹压缩**：长任务历史不直接塞进上下文，而是压缩成结构化 episode summary。
- **可训练数据资产**：trace 不只是日志，也是后续工具调用微调、路由优化、失败分类的数据来源。

SparkClaw 不继承以下部分作为 MVP 默认能力：

- 不默认自主生成可执行技能并立即启用。
- 不把远程云 VM、serverless sandbox 作为主路径。
- 不追求大量外部聊天渠道优先覆盖。
- 不允许 Agent 无确认地扩大自身权限。

### 2.2 从 OpenClaw 继承的思想

OpenClaw 的重要启发是“Gateway 是控制平面，产品是助手体验”：它强调单用户本地优先、跨渠道入口、设备节点、配对、allowlist、安全默认值、工作区、技能目录、会话模型和工具/自动化体系。

SparkClaw 应继承以下设计：

- **Gateway 控制平面**：所有会话、身份、事件流、approval、审计、工具策略都经过 Gateway。
- **单用户 trust boundary**：默认一个 owner，一个本地 Gateway，一个明确授权的 workspace 集合。
- **配对与 allowlist**：外部入口、设备节点、API client 不能天然可信，必须通过 pairing 或本地 token。
- **Workspace-first**：Agent 的读写边界以 workspace 为中心，而不是默认访问整个主机。
- **技能注册表**：技能是声明式能力包，包含说明、输入 schema、风险等级、依赖、评测用例。
- **可诊断运行时**：内置 doctor、healthz、readyz、trace viewer 和配置检查。

SparkClaw 不继承以下部分作为 MVP 默认能力：

- 不把所有即时通讯渠道作为第一优先级。
- 不把移动端节点、语音、Canvas 作为初期核心。
- 不默认运行在开放网络环境。
- 不把任意第三方技能市场作为默认信任来源。

## 3. SparkClaw 自己的设计原则

### 3.1 Local-first, not local-only

默认本地运行，本地文件、记忆、任务历史、索引、trace 和审批记录留在本机。云模型或外部服务只能作为显式配置的 fallback。

### 3.2 Bounded autonomy

SparkClaw 不追求无限自主，而追求“在明确边界内可执行”。系统必须清楚区分：

- read：只读，无外部副作用。
- draft：生成草稿或候选项，不提交。
- reversible：可回滚变更，需要记录 patch 或版本。
- dangerous：外发、删除、支付、提交表单、host shell、敏感记忆写入，必须审批。

### 3.3 Evaluation before fine-tuning

先建设 runtime、tool schema、policy、trace、golden tasks、chaos tests，再考虑微调。没有评测闭环的微调只会放大不可观测风险。

### 3.4 Tool contracts over prompt magic

所有工具必须有稳定契约：

- JSON Schema。
- 输入输出类型。
- 风险等级。
- 超时。
- 幂等性说明。
- sandbox 要求。
- 审计字段。
- golden task 覆盖。

### 3.5 Approval is a product surface

审批不是弹窗补丁，而是 SparkClaw 的核心产品界面。用户应能看到：

- Agent 想做什么。
- 为什么需要做。
- 会影响哪些资源。
- 可回滚性如何。
- 原始工具参数是什么。
- 批准、拒绝或修改后的执行结果。

## 4. 技术栈收敛

目标技术栈：

```text
Frontend: Vite 8 + React + TypeScript
Backend: Go
Database: PostgreSQL + pgvector
Object Storage: S3-compatible storage, local MinIO by default
Runtime: Docker Compose first
Model API: OpenAI-compatible endpoints
```

### 4.1 为什么后端用 Go

SparkClaw 的核心后端更像本地控制平面和执行系统，而不是单纯 Web API。Go 适合承担：

- Gateway WebSocket / SSE。
- 高并发事件流。
- 工具执行调度。
- Docker sandbox orchestration。
- 本地文件索引任务。
- 审计日志写入。
- 单二进制部署。

Go 后端应避免把 Agent 逻辑写成不可观察的长函数。推荐按状态机建模：

```text
received -> routed -> planned -> tool_pending -> observing -> repairing -> approval_pending -> completed / failed
```

### 4.2 为什么前端用 Vite 8

WebChat 和控制台是 SparkClaw 的主要交互面。Vite + React 适合快速实现：

- Chat timeline。
- Tool call timeline。
- Approval inbox。
- Memory editor。
- Trace viewer。
- Eval report viewer。
- Settings / model profiles / tool policy editor。

前端不要做成营销页。第一屏应是可用的 Agent 工作台。

### 4.3 PostgreSQL 的角色

PostgreSQL 是系统事实源，负责：

- users / owner profile。
- sessions。
- messages。
- tool_calls。
- approvals。
- audit_events。
- memories。
- documents metadata。
- eval_runs。
- traces metadata。

pgvector 用于语义记忆和文档 chunk embedding。大对象不要直接塞进 PostgreSQL。

### 4.4 对象存储的角色

对象存储保存不可轻易结构化或体积较大的内容：

- 上传文档原文。
- 网页快照。
- tool observation 原始输出。
- trace archive。
- eval artifact。
- sandbox 运行产物。
- 导出的报告、patch bundle。

本地默认使用 MinIO，生产或远端部署可替换为任意 S3-compatible storage。

### 4.5 Docker 编排边界

Docker Compose 是 MVP 主路径。至少包含：

- `webchat`
- `gateway`
- `agent-runtime`
- `model-router`
- `toolhub`
- `postgres`
- `minio`
- `sandbox-runner`
- `evaluator`

模型服务独立 profile：

- `mock-model`：无 GPU 开发。
- `external-model`：连接外部 OpenAI-compatible endpoint。
- `local-models`：连接本地 vLLM / SGLang / Ollama profile。

## 5. 服务边界

### 5.1 WebChat

职责：

- 会话入口。
- 流式响应展示。
- 工具调用时间线。
- 审批队列。
- 记忆候选确认。
- 设置与诊断。

不直接调用模型，不直接执行工具。

### 5.2 Gateway

职责：

- 身份与本地 owner。
- session lifecycle。
- WebSocket / SSE event stream。
- approval queue。
- audit log。
- client pairing。
- rate limit。
- policy dispatch。

Gateway 是唯一对外暴露的控制平面。

### 5.3 Agent Runtime

职责：

- 构造上下文。
- 调用 model router。
- 解析工具调用。
- observation 压缩。
- repair loop。
- verifier。
- final response composer。

Agent Runtime 不直接绕过 ToolHub 操作外部资源。

### 5.4 Model Router

职责：

- OpenAI-compatible client。
- fast / deep / embedding / reranker 选择。
- fallback 策略。
- latency、tokens、错误率统计。
- 上下文预算控制。

推荐路由：

| 场景 | 默认 lane |
|---|---|
| 闲聊、短摘要、文件只读、邮件 triage | fast |
| 代码修改、复杂规划、工具修复、风险复核 | deep |
| 文档索引、语义记忆 | embedding |
| RAG evidence 重排 | reranker |

### 5.5 ToolHub

职责：

- 工具注册。
- schema validation。
- risk classification。
- policy check。
- sandbox dispatch。
- result normalization。

工具按能力域拆分：

- `files.search`
- `files.read`
- `files.write_draft`
- `file.delete`
- `code.apply_patch`
- `shell.exec_sandboxed`
- `browser.read`
- `memory.search`
- `memory.propose`
- `memory.write_sensitive`
- `email.search`
- `email.draft_reply`
- `calendar.read`
- `calendar.propose_event`

### 5.6 Memory Service

职责：

- profile memory。
- episodic memory。
- semantic memory。
- procedural memory。
- memory candidate review。
- embedding / rerank。
- 用户可编辑、可删除、可导出。

默认策略：先候选，后确认，再持久化。

### 5.7 Evaluator

职责：

- golden tasks。
- tool JSON validity。
- prompt injection tests。
- dangerous action auto-execution tests。
- routing tests。
- regression report。

Evaluator 应能在 Docker profile 中一键运行。

## 6. 数据模型草案

核心表：

```text
owners
clients
sessions
messages
agent_runs
model_calls
tool_calls
approvals
audit_events
memories
memory_candidates
documents
document_chunks
objects
eval_runs
eval_cases
eval_results
```

关键约束：

- `tool_calls.risk_level` 必填。
- `tool_calls.approval_id` 对 dangerous 工具必填。
- `audit_events` append-only。
- `memories.source_run_id` 必填。
- `documents.object_key` 指向对象存储。
- `document_chunks.embedding` 存 pgvector。

当前实现中，`knowledge.index_workspace` 会在写入本地 `.sparkclaw/index/knowledge.json` 的同时，把每个被索引的源文档归档为 `knowledge_document` artifact，把生成的索引归档为 `knowledge_index` artifact；PostgreSQL 后端持久化 `documents` 时会写入对应的 `object_key`。
长期记忆遵循 `memory.retention_days` 保留策略；Gateway 与 ToolHub 在记忆查询、导出等入口前会裁剪过期已确认记忆，并记录 `memory.pruned` 审计与事件。
`browser.read` 会把清洗后的网页内容作为 tool observation 进入 trace，同时把原始 HTTP 响应体归档为 `browser_snapshot` artifact，保留“外部内容原文可审计、模型只消费清洗后数据”的边界。

## 7. API 草案

```text
POST   /api/sessions
GET    /api/sessions/:id
POST   /api/sessions/:id/messages
GET    /api/sessions/:id/events

GET    /api/clients
POST   /api/clients/:id/revoke

GET    /api/approvals
POST   /api/approvals/:id/approve
POST   /api/approvals/:id/reject
POST   /api/approvals/:id/modify

GET    /api/tools
GET    /api/tool-calls/:id

GET    /api/runs/:id/feedback
POST   /api/runs/:id/feedback

GET    /api/memories
GET    /api/memories/export
POST   /api/memories/export
POST   /api/memory-candidates/:id/accept
POST   /api/memory-candidates/:id/reject

GET    /api/traces/:run_id
POST   /api/evals/run
GET    /api/evals/:id

GET    /healthz
GET    /readyz
```

## 8. 推荐仓库结构

```text
sparkclaw/
  apps/
    webchat/
  services/
    gateway/
    agent-runtime/
    model-router/
    toolhub/
    memory/
    evaluator/
  packages/
    protocol/
    policy/
    tool-schema/
  configs/
    sparkclaw.default.yaml
    tools.policy.yaml
    model.profiles.yaml
    sandbox.policy.yaml
  docker/
    compose.yaml
    compose.dev.yaml
    compose.eval.yaml
    images/
  migrations/
  eval/
    golden/
    chaos/
  docs/
```

## 9. MVP 收敛范围

第一阶段只做四条闭环：

1. WebChat 到 Gateway 到 Agent Runtime 到 mock model 的基础会话闭环。
2. `files.search/read/write_draft` 和 `memory.search/propose` 的低风险工具闭环。
3. dangerous 工具进入 approval queue 的策略闭环。
4. golden tasks + trace report 的评测闭环。

暂缓：

- 多 IM 渠道。
- 移动端节点。
- 语音。
- 第三方技能市场。
- 自主技能生成后自动启用。
- host shell 自动执行。

## 10. 一句话设计取舍

Hermes 让 SparkClaw 学会“经验要变成记忆、技能和训练数据”。
OpenClaw 让 SparkClaw 学会“Gateway、渠道、配对、工作区和安全默认值是个人助手的骨架”。
SparkClaw 自己的取舍是“先把本地有限模型放进可靠、可审计、可评测的执行系统里”。
