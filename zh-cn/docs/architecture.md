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

Web、第三方设备和 Timer Event 都通过同一个 Provider-neutral 消息边界进入。`MessageEnvelope` 记录来源 Endpoint、原生消息/Thread Identity、Owner Authorization、有序 `MessageContent` Part 和 `ReturnRoute`。文字、图片、音频与文件是消息 Part 类型，不是 Agent Capability。Owner 编写的文字/caption 与受治理资源会分别投影：资源元数据使用 JSON 编码的可信数据表示，绝不作为自然语言或操作指令拼入 Owner Request。每条进入路由的请求都会生成一次 pre-route `RequestNormalization`，其中保存未覆盖的原文、经过确定性事实校验的 Canonical Request、确定性 Resource Context 和规范化来源。规范化必须保留原始语言与书写系统；最终回答渲染必须依据未改写的原文确定回复语言，而不是依据 Canonical Execution Query。该记录连同 Owner、Authorization、Route Decision 和 Return Route 一起持久化到 `AgentRun`，因此幂等重放以及审批/浏览器登录恢复都会复用相同的请求、身份、资源和返回目标。

每条终态路由都生成 Channel-neutral `WorkflowResult`。已匹配 Workflow 的失败仍是明确结果；只有 `unmatched` 路由可以进入过渡 ReAct。`WorkflowResult -> DeliveryRequest -> Delivery Gateway` 是普通结果唯一的外发链路。Gateway 解析 Endpoint、校验 Owner Authorization、在首次发送前协商整包多媒体能力，再交给已注册 Provider 或 Web 端口。Typing、审批按钮等 Provider Control Message 仍由 Connector 自己处理，但不构成第二条结果链路。

Web 端口通过一个通用投影消费同一组有序 Part，并写入持久化的 Assistant `Message`：文字 Part 进入 `content`，由受治理 `workspace_file` Ref 支撑的图片、音频和文件 Part 进入 `attachments`。WebChat 按消息所属 Session 解析附件工作区，并通过需要鉴权的 Document Endpoint 获取文件。Provider 专用或 Tool 专用的 Summary 语法不属于该契约；Markdown Media 解析只保留用于兼容旧的持久化消息。

Timer 是一种消息来源，不是只会发送提醒文字的功能。`ScheduleSpec` 保存 Literal Content 或稍后执行的 Request，并携带 Authorization 与 Return Route。Poll Loop 只 Claim 到期任务；有界 Worker Pool 执行 Request，结果仍通过同一个 Delivery Gateway 返回。

### Telegram

Telegram 是可选且默认关闭的 owner-authorized connector。系统允许多个 Bot binding 共存，也允许不同外部 Telegram 用户分别认领。每个 Bot token 使用前先验证，并分别由 credential vault 密封；file 与 PostgreSQL state 只保存 ciphertext envelope，不保存 plaintext token。已验证 Bot 立即 active 但尚无 recipient，随后由首条新私聊原子认领 user 和 chat；历史 update 与群聊不能认领。每条 binding 都有独立的 cursor、inbox identity 和私聊鉴权。Long polling、inbox persistence、per-chat ordering、retry 与 outbound delivery 均有界。已授权私聊可发送 text、受支持 attachment 与 voice note；voice 委托给共享 speech transcriber，不创建第二个 ASR client。

### Infinimesh Info

Infinimesh Info 是 `web.search` 与直接问题边界 `info.query` 共用的可选 production provider。Credential 从 environment variable 或 file 注入，不进入 public config。One-shot token 只保存在内存中，outbound request 有有界 retry 与 response size。SparkClaw 映射去除首尾空白后的 `answer_context.summary`、非空的 `answer_context.key_facts[].claim`、公开 source 元数据与 snippet，以及 citation。这些内容分别作为 `summary:0`、`fact:N` 和 `source:N:snippet:M` 下的 untrusted evidence；状态型或缺失的 summary 不会遮蔽可用的结构化证据。Provider request ID 与冻结 query 用于追踪。进入任何模型调用前，通用搜索与天气直接提取都生成有硬性字节上限且与 query 相关的投影；完整的已映射结果保留在工具存档中，供确定性校验使用。Token exhaustion、transport error 与 cloud 5xx 只使当前请求失败，不会关闭 local chat 或消息连接器。

### Agent Runtime

Runtime 通过有界的 Router-first 循环处理消息：

1. 归一化来源、多媒体 Content、Owner Authorization 和 Return Route。
2. 创建 Agent Run 并持久化 Message Context。
3. 对每条 Owner Request 只规范化一次，校验确定性事实，并在路由前持久化 Canonical Request。
4. 使用 Owner 原始内容请求 Guard Lane 给出 Safety Verdict。
5. 使用 Canonical Request 和单独的类型化资源请求 Fast Router 给出严格的能力树决策。
6. 把已匹配叶子派发到精确 Workflow 与固定 Tool Scope。
7. 仅在路由明确为 `unmatched` 时使用过渡 ReAct。
8. 审批或浏览器登录时使用已持久化 Canonical Request 暂停并恢复同一个 Workflow。
9. 生成唯一 `WorkflowResult`，包含已声明的文件/图片输出。
10. 持久化 Trace，并通过已解析 Endpoint 返回结果。

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

Fast Router 只输出严格 `RouteDecision`：状态、Catalog Revision、已注册能力路径、类型化 Slot、Confidence、Reason 和确定性 Fact。它不能输出工具、Skill、Workflow ID、风险、模型 Lane 或任意字段。在 Fast Router 运行前，Fast Normalization Pass 会覆盖每条 Owner Request。只有 URL、路径、数字、引号字面量、否定语义和显式外发目标都保持一致时，模型改写才会被接受；否则 Runtime 使用确定性的原文回退。相对时间公开搜索只能增加系统提供的当前日期。通过校验的请求与资源投影会持久化，并由 TaskHint、Workflow/ReAct 执行、最终 Grounding、审批恢复和浏览器登录恢复共同复用。搜索查询还会从冻结的 Route Slot 物化到工具调用中，因此 Workflow 执行阶段的模型不能改写它。Catalog 校验路径每条边和叶子 Operation。

Request Normalization 与 Capability Routing 始终使用 Fast Lane。已匹配 Capability 派发后，该持久化 Workflow 内的每个模型步骤都使用 Deep Lane，包括后续 Stage 和 Approval Resume。该 Lane 边界只改变模型选择；Workflow/ReAct 的 Context 构造、ToolResult Message、Observation 顺序、压缩和 Grounding 继续复用原有执行流程。只有明确为 `unmatched` 的请求继续使用该请求由 TaskHint/ReAct 选择的过渡 Lane。

Catalog revision `2026-07-21.v6` 有七个生产叶子：`browser.internet_search`、`browser.weather`、`browser.automation`、`browser.interaction`、`document.read`、`document.edit` 和 `schedule.manage`。`WorkflowProfileRegistry.Resolve` 把 `document.edit` 映射到 revision 2，其余叶子映射到 revision 1，不再执行意图匹配。Dispatcher 持久化 `RouteDecision`、`ReturnRoute`、已校验 Plan Digest 与 Node State。

`schedule.manage` 是唯一的定时任务管理叶子。类型化的 `create`、`read`、`edit` 和 `delete` 操作分别只暴露 `reminders.create`、`reminders.list`、`reminders.update` 或 `reminders.cancel`。这些工具继续通过现有 `ScheduleRegistry` 持久化 `ScheduleSpec`，由 Timer Worker 执行到期任务，再由 Delivery Gateway 返回结果。WebChat 定时任务栏只是当前 Principal 的 `pending` 与 `sending` 记录只读投影，不能修改任务状态。

`browser.internet_search` 归档所有答案依赖当前互联网状态的只读事实，包括金价、汇率、股票或指数行情、即时新闻、当前比赛结果和日程。这些例子不会成为垂直 leaf。Fast 使用类型化 `fact_scope=current_internet_state` 表达该边界；静态常识保持 `unmatched`。

`browser.weather` 是固定三阶段 Workflow。路由前的天气规范化只追加一次卡片取数要求：当前天气状况和温度、可选的当日最低/最高温，以及从当前时刻起可获得的零到五个未来小时日期时间/天气状况/温度。随后它把冻结的 Route Query 原样交给 `info.query`；已映射 Info 证据中与 query 相关的有界投影以稳定的 `summary:0`、`fact:N` 和 `source:N:snippet:M` ref 进入下一次 Deep 调用。Deep Lane 只能向 `weather.structure_payload` 提交由对应证据项引用文本支持的字段；校验允许仅去除 Markdown 排版标记或折叠空白，但字段值与单位必须保持一致。工具再回到完整持久化结果校验引用与原文，最后把生成的受治理引用交给无网络访问的 `media.render_weather_card`。Runtime 在执行前从持久化状态物化 query、location 和两个 outcome ref。当前天气状况、当前温度、当日范围和未来小时每一类都必须包含有依据的值，或进入明确的 `missing_fields`；无依据的可选 daily/hourly 段会被整体删除并标记缺失，不会丢弃已独立验证的当前数据。缺失值按“暂无数据”渲染，不能推测或用其他数值替代。成功结果通过普通 Delivery Gateway 只投影一张图片；失败不会回退到搜索、Open-Meteo 或 ReAct。天气预警、新闻、多来源比较和空气质量调研仍走 `browser.internet_search`；直接天气查询缺少有依据的地点时返回 clarify。

`browser.automation` revision 1 继续保持狭窄的打开/聚焦契约。`browser.interaction` revision 1 负责针对一个托管当前标签页或一个冻结 URL 的明确点击请求。它先检查 Playwright 健康状态并解析可复用标签页，再执行结构化 snapshot/click/点击后 snapshot/verification 闭环。Tool Exposure 在 Workflow 生命周期内持久化固定十工具边界，模型只看到当前 Stage 的 Capability 子集，Runtime Stage Capability Rule 仍会拒绝顺序错误的调用。Snapshot ref 绑定 page、snapshot、element fingerprint 和归档结果；成功点击后其来源 snapshot 立即失效。每次点击无需 approval，但必须先验证才能再次点击。Workflow 自己新建的标签页会在验证成功后关闭，复用的用户标签页保持打开。重复状态立即失败，第三次仍为 progress 时返回 `interaction_attempt_limit`。输入、选择、登录、凭证、上传/下载、付款、表单提交、截图和任意脚本仍不属于本 revision。

ToolHub Capability Metadata 是模型可见性权威。Tool Exposure 在 Policy 约束下物化所选 Workflow 的完整固定 Scope。Workflow Plan 不包含 Skill ID，Workflow 模型 Prompt 也不加载 Skill 文本；TaskHint Candidate 和 Outcome 不能扩权。Outcome Adapter 产生类型化 Fact，活动 Profile 判断完成或只激活预先声明的 Transition。审批和浏览器登录恢复使用已持久化路由与精确 Workflow Scope。

Capability 缺失、状态过期、Plan 非法、资源不匹配和已匹配执行失败时必须明确 Block 或 Fail，不能回退。只有 Router 状态为 `unmatched` 时，未迁移领域才进入过渡 ReAct。旧 Web/Workspace Workflow ID 仅作为持久化标识保留，并关闭失败。详细说明见[重构方案](intent-routing-workflow-refactor-plan.md)、[工具暴露契约](intent-routing-tool-exposure-contract.md)、[Profile 目录](intent-routing-workflow-domain-profiles.md)和[当前能力矩阵](workflow-capabilities.md)。

### ToolHub

ToolHub 注册有边界的工具，并校验成功输出是否符合声明 contract。当前工具：

| Area | Tools |
|---|---|
| Files | `files.search`, `files.read`, `files.write_draft`, `file.delete` |
| Documents | `text.replace_text`、`office.replace_text`、`docx.*`、`xlsx.*`、`pptx.*`、`pdf.extract_text`、`pdf.transform` |
| Memory | `memory.search`, `memory.write_candidate`, `memory.propose`, `memory.write_sensitive` |
| Browser 与 Info | `web.search`, `info.query`, `weather.structure_payload`, `media.render_weather_card`, `browser.read`, `browser.status`, `browser.list_tabs`, `browser.open`, `browser.focus`, `browser.close`, `browser.navigate`, `browser.snapshot`, `browser.screenshot`, `browser.wait`, `browser.click`, `browser.verify`, `browser.type`, `browser.select` |
| Code/shell | `shell.exec_sandboxed`, `code.apply_patch` |
| Approval/notify | `notify.ask_approval` |

Risk levels 为 `read`、`draft`、`reversible` 和 `dangerous`。Read/draft tools 在 policy 允许时可以运行。Reversible 和 dangerous tools 在 MVP 中需要 approval。

### 文档 Workflow

无论底层策略如何变化，`document.read` 与 `document.edit` 都保持同一份固定编排合同：

明确的内容读取、图片分析和总结请求进入 `document.read`。只要检测到对现有受治理文档内容的变更，就统一进入 `document.edit` revision 2，Router 不再把追加、插入、删除、行、单元格、段落、幻灯片或页面词语映射为具体 operation。结构化读取完成后，Runtime 只搜索检测格式对应的已注册 editor entry，并选择一个精确、带 operation qualifier 的 capability。不支持的 operation 在此明确 block，不能降级成只读结果，也不能被强制套用到其他 editor。文件新建、删除、重命名和移动仍位于内容编辑边界之外。

1. 解析唯一权威附件真实路径，检查受治理的普通文件，校验带签名的格式，并记录大小、媒体类型、修改时间和 SHA-256 元数据；
2. 按规范化后的已检测格式选择注册 reader；直接 PNG/JPEG/GIF/WebP 分析只选择 `images.inspect` 和 Fast 多模态模型，结构化文档则选择对应 parser；
3. 把完整解析结果规范化为 `structured_document_v1`，为 document、block、paragraph、section、sheet、row、cell、slide 和 page 生成稳定 ID，并保留来源位置与必要格式元数据；parser 可见的 assets、annotations、layout、extensions、coverage 和编辑策略保存在可选的 `document_enrichment_v1` envelope 中；
4. 按来源关系和 SHA-256 登记每个高层库可见的嵌入图片，把二进制写入 ArtifactStore，并且只对显式目标图片或有界的全文视觉理解调用 Fast 多模态模型；
5. 解析用户要求的文本或结构位置（例如 `document.p[25]`），对未找到、歧义或命中数不符的结果明确失败；row 和 slide locator 选择一个稳定结构实体，而不是展开成子 block；XLSX 的内容末尾插入以结构化结果中的最高非空行为准，仅带格式的空白行不能移动编辑位置或产生空洞；
6. 形成有边界的修改参数，并在任何 reversible editor 执行前持久化可恢复 Policy approval；
7. 只对受约束目标生成一个或多个新输出副本，通过同一 parser 完整重读每个输出，校验请求的修改后值和 operation-specific 内容差异，比较 parser 可见的 evidence-only 指纹，再重新计算输入哈希并返回带 `output_paths` 的可审计 `change_summary`，证明原件未被修改。`WorkflowResult` 提升该摘要，并通过统一 content interface 返回新文件。Web 投影把修改后的文档返回为文件附件，把图片返回为完整 inline image part；受治理 path 只保留在 reference 与审计数据中，不再作为可见成功文本。保真不匹配、无效输出或零变更都会清理生成的副本；成功摘要区分已验证的高层保真与未知的包级保真。

当前 `small_file_v1` 策略接受最大 8 MiB 的源文件和最大 200,000 bytes 的完整抽取表示。读取支持 text、DOCX、XLSX、PPTX 和文本型 PDF；修改支持已注册的纯文本/Markdown、DOCX、XLSX、PPTX 与 PDF 副本操作。直接图片读取仍处于同一个 `document.read` Workflow，但使用 `images.inspect` 的 12 MiB 原图限制，不经过 `small_file_v1`。超过相应阈值会返回类型化错误；不支持的格式和 locator 返回各自的类型化错误。Adapter 不得截断后报告成功。

`internal/document.Pipeline` 负责固定阶段顺序，strategy 负责 parser/editor registry。其活动 `DocumentEnricher` registry 当前在规范化后运行 Fast 图片语义实现；后续包清单富化器沿用同一边界。后续分块、流式、索引或惰性策略实现相同的 `Strategy` interface，Profile 和 Runtime 不按策略分支。ToolHub registration 仍是唯一模型可见 capability 边界，文档内容始终是不可信证据。结构化结果沿用现有 ToolCall observation 和 ArtifactStore 归档；本次小文件实现不引入 optional DocumentStore capability。

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
- `RequestNormalization`
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

Browser web access 使用 `web.search` 做发现，用 `browser.read` 做 read-only 来源页正文提取。Browser automation 使用 Microsoft Playwright `launchPersistentContext` 启动其安装且版本匹配的 Chromium（或一个显式 Custom Override），并使用 SparkClaw-owned 持久 Profile：普通任务使用 headless，登录、验证码、2FA、支付等人工步骤临时把同一个 Profile 切换到可见 Chromium。可见和隐藏进程不能并发占用 Profile。登录态始终保留在 Chromium 中，不通过 JavaScript Cookie 导出；恢复时使用 selected 登录后 URL，即使它与原页面不同源。`browser.read` 等待 rendered DOM、抓取 HTML，再交给 Readability 提取正文。结构快照只在正文不足或页面控件影响答案时按需调用。`browser.interaction` 消费有界的结构化控件投影，把页面文本视为不可信 evidence，并在完成或再次点击前强制获取新的点击后 snapshot 并执行 `browser.verify`。专项路线图见 [浏览器功能完善计划](browser-automation-improvement.md)，传输层契约见 [Playwright 浏览器自动化迁移方案](playwright-browser-automation-migration.md)，Profile 生命周期见 [托管共享 Chromium Profile 方案](managed-persistent-browser-profile.md)。涉及 URL 获取的浏览器观察默认拒绝 loopback/private literal hosts，存档 rendered HTML/raw response 或截图，并始终作为不可信证据处理。本地 fixture hosts 如 `127.0.0.1` 或 `host.docker.internal` 必须显式 allowlist。Runtime 必须停在人工验证步骤，不能伪造登录态证据。

Fast Router 只能选择已注册语义 leaf，并保持工具中立。Normalizer 冻结当前 owner 的搜索 query、校验天气地点、拒绝未知字段和未注册 edge，且绝不允许 Fast 发明 URL/path fact。对于纯语义查询 leaf，没有 Workflow 绑定的类型化资源元数据会在 Catalog 校验前丢弃，而不会阻断查询。精确 leaf identity 仍然解析到精确 Workflow；非法或失败的 matched route 不会回退到其他 Workflow 或 ReAct。

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
