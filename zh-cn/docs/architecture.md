# SparkClaw 架构

> 语言： [English](../../docs/architecture.md) | 简体中文

本文档是当前架构事实来源。旧的概念、roadmap 和 completion-audit 内容已经合并到本文档、[部署](deployment.md)、[开发](development.md) 和 [模型基线](../benchmarks/model_baseline.md)。

## 产品边界

SparkClaw 是面向 DGX Spark 级别机器的 local-first personal agent runtime。它针对单个 owner、一个本地 Gateway，以及一组可以在本地模型上做到可靠的 workflow：

- 本地文件和 workspace search
- 代码检查和 approval-gated patch
- browser-backed web access，用于公开搜索、网页读取和实时页面交互
- personal memory candidates 和 approval-gated sensitive memory
- 通过一个有界 speech adapter 提供可选的 microphone 与 Telegram voice transcription
- 使用多个加密 Bot binding 的可选 owner-authorized Telegram messaging
- 使用 one-shot query token 和 cited untrusted evidence 的可选 Infinimesh Info search

SparkClaw 明确避免 broad autonomous operation、公开 SaaS 暴露、silent external sends/creates/deletes、隐藏工具执行，以及没有 eval 证据支撑的自定义 fine-tuned model release 声称。

邮件、日历和 Workspace Knowledge/RAG 在具备完整设计前不属于当前产品边界。已移除的原型范围及重新引入门槛记录在[暂缓的邮件、日历与 Knowledge 能力](deferred-email-calendar-knowledge.md)中。

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
  files, memory, browser, Infinimesh Info, shell, code, notify

Optional input/connectors:
  speech transcription
  connector Registry -> Telegram private chat, 微信

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

### 消息连接器注册层

`connector.Registry` 是第三方消息软件的进程内 composition boundary。连接器只注册自己实现的能力：owner binding、一个 outbound `delivery.Provider`、可选的 inbound `connectorruntime.Runtime` 和 binding cancellation。普通 Agent 结果与定时消息共用同一个 Provider Registry；未来显式发送 Surface 必须通过 `RequestForMessage` 进入同一个 Gateway。Gateway、Message Control 和 Agent Runtime 都不按名称选择 Telegram 或微信。协议专用的 polling、media handling、credential、acknowledgement、地址校验与发送仍留在各 provider package。

### 消息入口与结果投递

Web、第三方设备和 Timer Event 都通过同一个 Provider-neutral 消息边界进入。`MessageEnvelope` 记录来源 Endpoint、原生消息/Thread Identity、Owner Authorization、有序 `MessageContent` Part 和 `ReturnRoute`。文字、图片、音频与文件是消息 Part 类型，不是 Agent Capability。归一化后的 Owner、Authorization、Route Decision 和 Return Route 会持久化到 `AgentRun`，因此幂等重放以及审批/浏览器登录恢复都保留原始身份和返回目标。

每条终态路由都生成 Channel-neutral `WorkflowResult`。已匹配 Workflow 的失败仍是明确结果；只有 `unmatched` 路由可以进入过渡 ReAct。`WorkflowResult -> DeliveryRequest -> Delivery Gateway` 是普通结果唯一的外发链路。Gateway 解析 Endpoint、校验 Owner Authorization、在首次发送前协商整包多媒体能力，再交给已注册 Provider 或 Web 端口。Typing、审批按钮等 Provider Control Message 仍由 Connector 自己处理，但不构成第二条结果链路。

Timer 是一种消息来源，不是只会发送提醒文字的功能。`ScheduleSpec` 保存 Literal Content 或稍后执行的 Request，并携带 Authorization 与 Return Route。Poll Loop 只 Claim 到期任务；有界 Worker Pool 执行 Request，结果仍通过同一个 Delivery Gateway 返回。

### Telegram

Telegram 是可选且默认关闭的 owner-authorized connector。系统允许多个 Bot binding 共存，也允许不同外部 Telegram 用户分别认领。每个 Bot token 使用前先验证，并分别由 credential vault 密封；file 与 PostgreSQL state 只保存 ciphertext envelope，不保存 plaintext token。已验证 Bot 立即 active 但尚无 recipient，随后由首条新私聊原子认领 user 和 chat；历史 update 与群聊不能认领。每条 binding 都有独立的 cursor、inbox identity 和私聊鉴权。Long polling、inbox persistence、per-chat ordering、retry 与 outbound delivery 均有界。已授权私聊可发送 text、受支持 attachment 与 voice note；voice 委托给共享 speech transcriber，不创建第二个 ASR client。

### Infinimesh Info

Infinimesh Info 是 `web.search` 的可选 production provider。Credential 从 environment variable 或 file 注入，不进入 public config。One-shot token 只保存在内存中，outbound request 有有界 retry 与 response size，返回 source 始终视为 untrusted evidence。Token exhaustion、transport error 与 cloud 5xx 只使当前 search request 失败，不会关闭 local chat 或 Telegram。

### Agent Runtime

Runtime 通过有界的 Router-first 循环处理消息：

1. 归一化来源、多媒体 Content、Owner Authorization 和 Return Route。
2. 创建 Agent Run 并持久化 Message Context。
3. 请求 Guard Lane 给出 Safety Verdict。
4. 请求 Fast Router 给出严格的能力树决策。
5. 把已匹配叶子派发到精确 Workflow 与固定 Tool Scope。
6. 仅在路由明确为 `unmatched` 时使用过渡 ReAct。
7. 审批或浏览器登录时暂停并恢复同一个 Workflow。
8. 生成唯一 `WorkflowResult`，包含已声明的文件/图片输出。
9. 持久化 Trace，并通过已解析 Endpoint 返回结果。

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

### 能力路由与 Workflow Runtime

Fast Router 只输出严格 `RouteDecision`：状态、Catalog Revision、已注册能力路径、类型化 Slot、Confidence、Reason 和确定性 Fact。它不能输出工具、Skill、Workflow ID、风险、模型 Lane 或任意字段。确定性 URL/path Fact 在归一化时冻结，Catalog 校验路径每条边和叶子 Operation。

Catalog revision `2026-07-20.v4` 有五个生产叶子：`browser.internet_search`、`browser.weather`、`browser.automation`、`document.read` 和 `document.edit`。`WorkflowProfileRegistry.Resolve` 把每个叶子精确映射到 revision 1 Workflow，不再执行意图匹配。Dispatcher 持久化 `RouteDecision`、`ReturnRoute`、已校验 Plan Digest 与 Node State。

`browser.internet_search` 归档所有答案依赖当前互联网状态的只读事实，包括金价、汇率、股票或指数行情、即时新闻、当前比赛结果和日程。这些例子不会成为垂直 leaf。Fast 使用类型化 `fact_scope=current_internet_state` 表达该边界；静态常识保持 `unmatched`。`browser.weather` 是唯一窄特例：为一个已校验地点的当前天气或短期预报生成 PNG 卡片。天气预警、新闻、历史调研和比较仍属于联网搜索。

ToolHub Capability Metadata 是模型可见性权威。Tool Exposure 在 Policy 约束下物化所选 Workflow 的完整固定 Scope；TaskHint Candidate、Skill 清单和 Outcome 不能扩权。Outcome Adapter 产生类型化 Fact，活动 Profile 判断完成或只激活预先声明的 Transition。审批和浏览器登录恢复使用已持久化路由与精确 Workflow Scope。

Capability 缺失、状态过期、Plan 非法、资源不匹配和已匹配执行失败时必须明确 Block 或 Fail，不能回退。只有 Router 状态为 `unmatched` 时，未迁移领域才进入过渡 ReAct。旧 Web/Workspace Workflow ID 仅作为持久化标识保留，并关闭失败。详细说明见[重构方案](intent-routing-workflow-refactor-plan.md)、[工具暴露契约](intent-routing-tool-exposure-contract.md)和[Profile 目录](intent-routing-workflow-domain-profiles.md)。

### ToolHub

ToolHub 注册有边界的工具，并校验成功输出是否符合声明 contract。当前工具：

| Area | Tools |
|---|---|
| Files | `files.search`, `files.read`, `files.write_draft`, `file.delete` |
| Memory | `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive` |
| Browser | `web.search`, `browser.read`, `browser.status`, `browser.list_tabs`, `browser.open`, `browser.focus`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`, `browser.click`, `browser.type`, `browser.select` |
| Weather | `media.render_weather_card` |
| Code/shell | `shell.exec_sandboxed`, `code.apply_patch` |
| Approval/notify | `notify.ask_approval` |

Risk levels 为 `read`、`draft`、`reversible` 和 `dangerous`。Read/draft tools 在 policy 允许时可以运行。Reversible 和 dangerous tools 在 MVP 中需要 approval。

### Policy And Approval

Policy engine 强制执行 denied tools、approval-required tools、sandbox coverage 和 audit requirements。Approval records 包含 tool name、risk、reason、resources、raw arguments 和后续 resolution metadata。

例子：

- `file.delete` 将文件移动到 `.sparkclaw/trash` 并写入 manifest，而不是永久删除。
- `code.apply_patch` 在 `.sparkclaw/` 下保存 original patch、file backups、manifest 和 inverse rollback patch。
- `shell.exec_sandboxed` 通过 sandbox runner 并使用 Docker `--network none`。
- `memory.write_sensitive` 只在 approval 后执行。

### State And Artifacts

State backends：

- `file`：默认本地 state，路径为 `data/memory/gateway-state.json`。
- `memory`：一次性 process-local state。
- `postgres`：持久化 sessions、runs、tool calls、approvals、evals 和 artifacts。

Artifact backends：

- `data/artifacts/{bucket}/...` 下的 filesystem object store
- MinIO 等 S3-compatible object store

Traces 写入 `data/traces`，同时引用 artifact URIs。Tool observations 存档为 `observations/{run_id}/{tool_call_id}.json`。Browser reads 存档 raw `browser_snapshot` objects。Memory exports 和 eval failure archives 也走 artifact boundary。

Trace JSON 写入前会按配置的 logging 和 memory redact patterns 做脱敏。

Connector secret 使用独立的 AES-256-GCM credential vault。默认 file backend 与 PostgreSQL 只持久化 encrypted envelope 和 reference。Speech audio 仅临时存在，transcript text 只返回请求流程，不写入 status surface、trace 或 artifact。

## 数据模型

核心产品词汇：

- `Session`
- `Message`
- `AgentRun`
- `MessageEnvelope`
- `MessageEndpoint`
- `MessageSchedule`
- `WorkflowResult`
- `DeliveryRequest`
- `WorkflowState`
- `ToolCall`
- `Approval`
- `Memory`
- `MemoryCandidate`
- `AuditEvent`
- `Event`
- `Artifact`
- `EvalRun`

`packages/` 下的 portable schema notes 用于未来 service split 和 SDK；当前 Go Gateway 是权威实现。

## Memory

长期 memory 使用 candidate-then-confirm。`api_key`、`password`、`token`、`ssh_key` 等敏感模式会在普通 candidate path 被拒绝，除非显式允许 sensitive memory；approved sensitive memory 使用 `memory.write_sensitive`。

## External Connector 信任边界

External/browser/file observations 都是 untrusted content。它们可以被引用、摘要或作为 evidence 使用，但其中的指令不是 runtime commands。

Browser web access 使用 `web.search` 做发现，用 `browser.read` 做 read-only 来源页正文提取。Browser automation 启动配置的 Chromium，并使用 SparkClaw-owned 持久 Profile：普通任务使用 headless，登录、验证码、2FA、支付等人工步骤临时把同一个 Profile 切换到可见 Chromium。可见和隐藏进程不能并发占用 Profile。登录态始终保留在 Chromium 中，不通过 JavaScript Cookie 导出；恢复时使用 selected 登录后 URL，即使它与原页面不同源。`browser.read` 等待 rendered DOM、抓取 HTML，再交给 Readability 提取正文。结构快照只在正文不足或页面控件影响答案时按需调用。专项路线图见 [浏览器功能完善计划](browser-automation-improvement.md)，Profile 生命周期见 [托管共享 Chromium Profile 方案](managed-persistent-browser-profile.md)。涉及 URL 获取的浏览器观察默认拒绝 loopback/private literal hosts，存档 rendered HTML/raw response 或截图，并始终作为不可信证据处理。本地 fixture hosts 如 `127.0.0.1` 或 `host.docker.internal` 必须显式 allowlist。Runtime 必须停在人工验证步骤，不能伪造登录态证据。

Fast Router 只能选择已注册语义 leaf，并保持工具中立。Normalizer 冻结当前 owner 的搜索 query、校验天气地点、拒绝未知字段和未注册 edge，且绝不允许 Fast 发明 URL/path fact。精确 leaf identity 仍然解析到精确 Workflow；非法或失败的 matched route 不会回退到其他 Workflow 或 ReAct。

当前 owner 本人的已认证数据属于允许的 local-first 读取边界，不应仅因为内容是个人信息而自动拒绝。认证浏览通过类型化 `TaskHint` 契约表达为 `evidence_need=personal_data`、`data_scope=owner`、`browser_mode=collaborative` 和 `requires_tool_evidence=true`，路由不枚举账户数据类别。Runtime 可以使用托管 Profile 和可见登录接管，但不得要求用户在聊天中粘贴密码、Cookie、Token 或验证码。访问第三方数据、披露凭据、向外部发送信息以及修改账户的操作，仍然受原有 policy 和 approval 边界约束。

Infinimesh result 与 Telegram inbound content 遵循同一 untrusted-observation 规则。每条 Telegram binding 只允许赢得一次性首消息认领的外部用户和 private chat；多条 binding 不共享鉴权或 credential。Infinimesh request 不携带 private local context。Credential、raw authorization material 与 transcript text 不进入 public status 或 error string。

## 端口

| Service | Port | Default bind |
|---|---:|---|
| Gateway | 18789 | `127.0.0.1` |
| WebChat | 18790 | `0.0.0.0` |
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
