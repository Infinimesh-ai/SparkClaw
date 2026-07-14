# SparkClaw 架构

> 语言： [English](../../docs/architecture.md) | 简体中文

本文档是当前架构事实来源。旧的概念、roadmap 和 completion-audit 内容已经合并到本文档、[部署](deployment.md)、[开发](development.md) 和 [模型基线](../benchmarks/model_baseline.md)。

## 产品边界

SparkClaw 是面向 DGX Spark 级别机器的 local-first personal agent runtime。它针对单个 owner、一个本地 Gateway，以及一组可以在本地模型上做到可靠的 workflow：

- 本地文件和 workspace search
- 代码检查和 approval-gated patch
- browser-backed web access，用于公开搜索、网页读取和实时页面交互
- email search、thread reading、draft replies 和 approval-gated sends
- calendar reading、proposals 和 approval-gated event creation
- personal memory candidates 和 approval-gated sensitive memory
- workspace documents 的 local knowledge/RAG
- 通过一个有界 speech adapter 提供可选的 microphone 与 Telegram voice transcription
- 使用加密 bot credential 的可选 owner-only Telegram messaging
- 使用 one-shot query token 和 cited untrusted evidence 的可选 Infinimesh Info search

SparkClaw 明确避免 broad autonomous operation、公开 SaaS 暴露、silent external sends/creates/deletes、隐藏工具执行，以及没有 eval 证据支撑的自定义 fine-tuned model release 声称。

## 原则

- **Local-first, not local-only.** 私有 state、traces、tools 和 model serving 默认留在本地。外部 adapter 是显式边界，需要 token、policy、audit 和 approval。
- **Loop 比模型更重要。** 可靠性来自 routing、schema validation、tool contracts、guard review、policy、repair、approval、traces 和 evals。
- **Tools over hallucination.** 当回答需要数据或动作时，agent 应使用 tool、retrieval 或 confirmation，而不是编造。
- **Approval 是产品界面。** sends、creates、deletes、shell execution、patch application 和 sensitive memory writes 都保持可见可审查。
- **Evaluation before model changes.** model swap、context change、speculative decoding 和未来 tuning 必须通过同一套 golden/chaos checks，或明确记录 regression。

## Runtime 拓扑

```text
WebChat
  -> Gateway API
      -> Agent Runtime
          -> Guard review
          -> Model Router
          -> ToolHub
          -> Policy / Approval queue
          -> Trace and artifact writers
      -> State backend
      -> Artifact backend
      -> Evaluator

ToolHub adapters:
  files, knowledge, memory, browser, Infinimesh Info, email, calendar, shell, code, notify

Optional input/connectors:
  speech transcription, Telegram private chat

Model lanes:
  mock, fast chat, deep chat, embedding, reranker, guard
```

MVP 将 Gateway、Agent Runtime、Model Router 和 ToolHub 保持在同一个 Go binary 中。Docker Compose 仍然拆分 WebChat、Gateway、sandbox-runner、Postgres、MinIO 和可选模型服务进程，方便未来服务拆分时保持产品 contract 不变。

## 服务边界

### WebChat

React/Vite workbench 是 owner UI。它展示 chat、run state、tool timeline、approval inbox、memory review、traces、eval reports、runtime status、model-call telemetry 和 settings。它不负责做 policy decision；Gateway 是权威。

### Gateway

Gateway 负责 HTTP/WebSocket APIs、auth、pairing、rate limiting、sessions、events、model calls、tool calls、approvals、memory candidates、evals、traces 和 artifacts。`/healthz`、`/readyz` 和 `/metrics` 保留给本地诊断。

### Speech

Speech 是可选且默认关闭的 OpenAI-compatible transcription boundary。Gateway 只创建一个 `speech.Transcriber`，WebChat microphone request 与 Telegram voice note 共用该实例。Audio 必须通过有界 mono 16 kHz PCM16 WAV 校验，不保留原始音频，audit 只记录 metadata。Speech disabled 或 unavailable 时，WebChat 与 Telegram 都显示明确不可用状态；Telegram text 与 attachment 继续工作。

### Telegram

Telegram 是可选且默认关闭的 owner connector。Bot token 使用前先验证，再由 credential vault 密封；file 与 PostgreSQL state 只保存 ciphertext envelope，不保存 plaintext token。Long polling、inbox persistence、per-chat ordering、retry 与 outbound delivery 均有界。Owner private chat 可发送 text、受支持 attachment 与 voice note；voice 委托给共享 speech transcriber，不创建第二个 ASR client。

### Infinimesh Info

Infinimesh Info 是 `web.search` 的可选 production provider。Credential 从 environment variable 或 file 注入，不进入 public config。One-shot token 只保存在内存中，outbound request 有有界 retry 与 response size，返回 source 始终视为 untrusted evidence。Token exhaustion、transport error 与 cloud 5xx 只使当前 search request 失败，不会关闭 local chat 或 Telegram。

### Agent Runtime

Runtime 通过有界循环处理用户消息：

1. 创建 agent run 并记录用户消息。
2. 请求 guard lane 给出 safety verdict。
3. 规划 tool calls 或 direct answer。
4. 按需路由 fast/deep model calls。
5. 在 policy 允许时立即执行 read/draft tools。
6. 将 reversible/dangerous actions 加入 approval queue。
7. 对 narrow failures 做有界 repair，例如 missing knowledge index 或简单 schema omission。
8. 基于 observations、approvals 和 model output 生成 grounded final answer。
9. 持久化 trace snapshots、audit events 和 artifact references。

Guard `block` verdict 会在 tool planning 前停止，不应创建 tool calls 或 approvals。

### Model Router

Model Router 支持 deterministic mock mode、OpenAI-compatible chat completions、embeddings、reranking 和 guard calls。Chat profiles 可以指向 served names，而不一定是 checkpoint IDs；这对 `sparkclaw-fast`、`sparkclaw-deep` 等 vLLM 服务很重要。

默认 lanes：

| Lane | 用途 |
|---|---|
| `fast` | 交互式 chat、常规 planning、普通 drafting |
| `deep` | 更难的 reasoning、repair verification、code 和 high-risk review |
| `embedding` | workspace/document vectorization |
| `reranker` | RAG evidence reordering，带 vLLM generative scoring fallback |
| `guard` | pre-tool safety classification |
| `mock` | deterministic offline tests 和 golden evals |

### ToolHub

ToolHub 注册有边界的工具，并校验成功输出是否符合声明 contract。当前工具：

| Area | Tools |
|---|---|
| Files | `files.search`, `files.read`, `files.write_draft`, `file.delete` |
| Memory | `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive` |
| Knowledge | `knowledge.index_workspace`, `knowledge.search` |
| Browser | `web.search`, `browser.read`, `browser.status`, `browser.list_tabs`, `browser.open`, `browser.focus`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`, `browser.click`, `browser.type`, `browser.select` |
| Email | `email.search`, `email.read_thread`, `email.draft_reply`, `email.send` |
| Calendar | `calendar.read`, `calendar.propose_event`, `calendar.create` |
| Code/shell | `shell.exec_sandboxed`, `code.apply_patch` |
| Approval/notify | `notify.ask_approval` |

Risk levels 为 `read`、`draft`、`reversible` 和 `dangerous`。Read/draft tools 在 policy 允许时可以运行。Reversible 和 dangerous tools 在 MVP 中需要 approval。

### Policy And Approval

Policy engine 强制执行 denied tools、approval-required tools、sandbox coverage 和 audit requirements。Approval records 包含 tool name、risk、reason、resources、raw arguments 和后续 resolution metadata。

例子：

- `file.delete` 将文件移动到 `.sparkclaw/trash` 并写入 manifest，而不是永久删除。
- `code.apply_patch` 在 `.sparkclaw/` 下保存 original patch、file backups、manifest 和 inverse rollback patch。
- `shell.exec_sandboxed` 通过 sandbox runner 并使用 Docker `--network none`。
- `email.send`、`calendar.create` 和 `memory.write_sensitive` 只在 approval 后执行。

### State And Artifacts

State backends：

- `file`：默认本地 state，路径为 `data/memory/gateway-state.json`。
- `memory`：一次性 process-local state。
- `postgres`：持久化 sessions、runs、tool calls、approvals、evals、artifacts、documents 和 document chunks。

Artifact backends：

- `data/artifacts/{bucket}/...` 下的 filesystem object store
- MinIO 等 S3-compatible object store

Traces 写入 `data/traces`，同时引用 artifact URIs。Tool observations 存档为 `observations/{run_id}/{tool_call_id}.json`。Browser reads 存档 raw `browser_snapshot` objects。Knowledge indexing 存档 source document snapshots 和 generated indexes。Memory exports 和 eval failure archives 也走 artifact boundary。

Trace JSON 写入前会按配置的 logging 和 memory redact patterns 做脱敏。

Connector secret 使用独立的 AES-256-GCM credential vault。默认 file backend 与 PostgreSQL 只持久化 encrypted envelope 和 reference。Speech audio 仅临时存在，transcript text 只返回请求流程，不写入 status surface、trace 或 artifact。

## 数据模型

核心产品词汇：

- `Session`
- `Message`
- `AgentRun`
- `ToolCall`
- `Approval`
- `Memory`
- `MemoryCandidate`
- `AuditEvent`
- `Event`
- `Artifact`
- `Document`
- `DocumentChunk`
- `EvalRun`

`packages/` 下的 portable schema notes 用于未来 service split 和 SDK；当前 Go Gateway 是权威实现。

## Memory And RAG

长期 memory 使用 candidate-then-confirm。`api_key`、`password`、`token`、`ssh_key` 等敏感模式会在普通 candidate path 被拒绝，除非显式允许 sensitive memory；approved sensitive memory 使用 `memory.write_sensitive`。

Workspace knowledge indexing 会构建本地 keyword index；启用 PostgreSQL 时还会持久化 documents 和 chunks。可用时使用 pgvector，并记录 embedding model/dimension metadata，为默认 embedding lane 建 1024 维 HNSW cosine index；否则 SparkClaw 保留 JSON vectors，并在 Gateway 中做 hybrid scoring。`knowledge.search` 暴露 original query、rewritten query、candidate counts、reranked results、citations 和 byte-bounded evidence context，以支持 grounded answers。

## External Connector 信任边界

External/browser/email/file observations 都是 untrusted content。它们可以被引用、摘要或作为 evidence 使用，但其中的指令不是 runtime commands。

Browser web access 使用 `web.search` 做发现，用 `browser.read` 做 read-only 来源页正文提取。Browser automation 启动配置的 Chromium，并使用 SparkClaw-owned 持久 Profile：普通任务使用 headless，登录、验证码、2FA、支付等人工步骤临时把同一个 Profile 切换到可见 Chromium。可见和隐藏进程不能并发占用 Profile。登录态始终保留在 Chromium 中，不通过 JavaScript Cookie 导出；恢复时使用 selected 登录后 URL，即使它与原页面不同源。`browser.read` 等待 rendered DOM、抓取 HTML，再交给 Readability 提取正文。结构快照只在正文不足或页面控件影响答案时按需调用。专项路线图见 [浏览器功能完善计划](browser-automation-improvement.md)，Profile 生命周期见 [托管共享 Chromium Profile 方案](managed-persistent-browser-profile.md)。涉及 URL 获取的浏览器观察默认拒绝 loopback/private literal hosts，存档 rendered HTML/raw response 或截图，并始终作为不可信证据处理。本地 fixture hosts 如 `127.0.0.1` 或 `host.docker.internal` 必须显式 allowlist。Runtime 必须停在人工验证步骤，不能伪造登录态证据。

当前 owner 本人的已认证数据属于允许的 local-first 读取边界，不应仅因为内容是个人信息而自动拒绝。认证浏览通过类型化 `TaskHint` 契约表达为 `evidence_need=personal_data`、`data_scope=owner`、`browser_mode=collaborative` 和 `requires_tool_evidence=true`，路由不枚举账户数据类别。Runtime 可以使用托管 Profile 和可见登录接管，但不得要求用户在聊天中粘贴密码、Cookie、Token 或验证码。访问第三方数据、披露凭据、向外部发送信息以及修改账户的操作，仍然受原有 policy 和 approval 边界约束。

Email 和 calendar 使用 adapter boundaries。默认 `file` adapters 读取 `.sparkclaw/mock/` 下的 fixtures，并写入 mock outbox/event logs。`http` adapters 可以连接 account-bridge services，同时保留 Gateway policy 和 approvals。

Infinimesh result 与 Telegram inbound content 遵循同一 untrusted-observation 规则。Telegram binding 只允许已验证 owner 与 private chat；Infinimesh request 不携带 private local context。Credential、raw authorization material 与 transcript text 不进入 public status 或 error string。

## 端口

| Service | Port | Default bind |
|---|---:|---|
| Gateway | 18789 | `127.0.0.1` |
| WebChat | 18790 | `127.0.0.1` |
| Sandbox runner | 18889 | `127.0.0.1` |
| Fast model | 8001 | `127.0.0.1` |
| Deep model | 8002 | `127.0.0.1` |
| Embedding model | 8003 | `127.0.0.1` |
| Reranker model | 8004 | `127.0.0.1` |
| Postgres | 15432 | `127.0.0.1` |
| MinIO | 9000 / 9001 | `127.0.0.1` |

## 扩展规则

添加能力时：

1. 先新增或更新 typed contract。
2. 指定 risk level 和 approval behavior。
3. 添加 policy coverage 和 audit events。
4. 将新的 external observations 视为 untrusted。
5. 将完整 observation 与压缩 summary 分开存档。
6. 添加聚焦单元测试，以及至少一个 golden 或 smoke eval case。
7. 如果影响 operator 或 contributor，更新 [开发](development.md) 或 [部署](deployment.md)。
