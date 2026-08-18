# SparkClaw 架构

> 语言： [English](../../docs/architecture.md) | 简体中文

本文档是当前系统事实来源。专项契约从[文档索引](index.md)进入；已完成计划和被替代设计
不会继续留在当前文档集合中。

## 产品边界

SparkClaw 是面向一个 owner、一个本地 Gateway 和 DGX Spark 级硬件的 local-first personal
agent runtime。当前产品表面包括：

- 本地文件、结构化文档和 approval-gated output-copy edit；
- 公开搜索、直接天气卡片、托管浏览器 open/focus 与页面读取、受限验证 click 和经审批的
  可逆表单草稿；
- 基于稳定请求/上下文证据的普通聊天回答；
- 到期 payload 重新进入正常路由的定时消息；
- personal memory candidate 和 approval-gated sensitive memory；
- 可选 WebChat speech transcription、Telegram/微信消息和 Infinimesh Info evidence；
- 可选 fixed-session JingSi 文本呈现，只在显式绑定的 private-LAN port 发布；
- 可选 Happy Team 任务与个人 bridge MCP 接入，并把 supervised plan 决议同步到持久化人工
  审批收件箱；
- 可选、按 workspace 限定的 LocalMind MCP 接入和被动 ISCP 提及收件箱；
- trace、artifact、eval、Policy、approval、auth 和 durable state。

准确可执行叶子见 [Workflow 能力矩阵](workflow-capabilities.md)。邮件、日历和内置 workspace
knowledge/RAG 不是 active capability，见[暂缓能力](deferred-email-calendar-knowledge.md)。

SparkClaw 不是无限制 autonomous agent 或公开 multi-tenant SaaS。它不允许静默外部发送、
创建、删除、任意浏览器交互、隐藏 tool execution 或未经 eval 的模型声明。

## 原则

- local-first state 和显式外部 adapter 边界；
- 注册 capability/tool contract 优先于模型临场发挥；
- 每个请求只执行一个 routed leaf Workflow，歧义或失败必须明确；
- 确定性 resource binding 和由 Policy 管理 effect；
- approval 是 reversible/dangerous action 的持久化产品表面；
- external/file/browser observation 都是不可信 evidence；
- 可追踪执行，模型或契约变化前先评测。

## Runtime 拓扑

```text
WebChat / Telegram / 微信 / Timer
  -> Gateway + Message Plane
      -> owner 原始请求 + current/recent 资源解析
      -> semantic intent router
          -> embedding channel：仅当前 owner 问题
          -> Fast/Tree channel：同一问题 + 有界 typed context
          -> weighted fusion + Top-2 decision
      -> Catalog validation 和一个 Workflow Profile
      -> Workflow Runtime
          -> stage-scoped Tool Exposure
          -> Model Router（由 Profile 选择执行通道）
          -> ToolHub -> Policy -> Approval
      -> WorkflowResult
      -> Delivery Gateway -> Web 或 connector Provider

基础设施：
  Store、trace、artifact、auth、pairing、config、evaluator
```

Gateway、Agent Runtime、Model Router、ToolHub、Message Control 和 Delivery 运行在一个 Go
binary 中。Compose 分离 WebChat、Gateway、sandbox、Postgres、MinIO 和可选模型端点，使进程
拓扑可以变化而不改变领域契约。

## 服务职责

### WebChat

WebChat 是 owner 工作台，展示 chat、schedule、direct delivery、connector activation/binding、
tool、approval、被动协作通知、memory、trace、artifact、eval 和 runtime setting。它发送
typed action，但不决定 route、Policy 或 delivery。见 [WebChat](webchat.md)。

### Gateway 与 Message Plane

Gateway 负责 HTTP/event API、auth、pairing、rate limit、session、public projection 和 service
assembly。Message Plane 把 Web、connector 和 Timer 输入转换为统一 provider-neutral
`MessageEnvelope`，保留有序 part、source identity、authorization 和 `ReturnRoute`。

每个 run 持久化 untouched owner request、deterministic resource context、route evidence、
owner/actor identity 和 return route。系统不存在 canonical execution request、请求规范化模型
调用或 normalization audit 结构。resume/replay 从持久化消息恢复 owner 原始请求，不重新解释。

### Capability 与意图路由

`capability.Catalog` 是 branch、leaf、route contract、operation、target policy 和 Workflow
reference 的结构权威。Workflow Profile 负责自己叶子的 semantic example 和 Tree distinction。

自然语言识别在同一编译图上使用非对称双通道。Embedding 只接收当前 owner 编写的问题。
Fast/Tree 接收同一问题以及有界 typed context，其中可以包含近期对话，以及文档记录中的
名称、格式、来源和最近活动等元数据。该契约适用于全部自然语言意图，不只适用于文档。
Fast 负责消解歧义并给候选评分，不能改写请求、绑定资源或输出 `RouteDecision`：

```text
fusion_score = alpha * embedding_score
             + (1 - alpha) * tree_score
```

两个通道都会为所有 eligible candidate 评分。Weighted fusion 对完整集合排序并保留最终
Top-2，由此产生 clear、ambiguous 或 low coverage；只有 clear 且 eligible 的候选会组装为
一个 `RouteDecision`。typed UI action 和 persisted resume 绕过 semantic classification，
但不绕过 Catalog 校验。见[意图路由](intent-routing.md)。

### Workflow Runtime 与 ToolHub

Workflow Registry 为匹配叶子解析唯一一个 versioned Profile。Profile 创建带有有界 node、
transition、completion evidence、risk 和 argument binding 的冻结 plan。每个 active node 只
materialize 当前 stage 所需 ToolHub capability。

ToolHub registration 是 tool 的 execution/schema/risk/effect 权威。Policy/Approval 在 Workflow
选择之后、effect 之前执行。模型不能增加 tool、修改冻结 resource binding 或绕过 approval。
匹配 Workflow 失败保持显式，不回退到另一 router 或任何通用回退循环；workflow 步骤循环是唯一的执行原语，旧 ReAct 路径已删除。

动态 MCP provider 原子替换自己 namespace 下的 ToolHub entry，不能覆盖 static tool 或其他
provider。它们不含 schema 的 directory entry 进入同一个有界搜索，只有一个持久化选择会物化
完整 server-advertised schema。source 和原始 remote name 可供 audit 查询。capability
snapshot revision 绑定 endpoint identity、server metadata、tool schema/annotation 和
credential scope，因此刷新会使 stale view 失效。

文档格式差异在三个 owner boundary 内分别注册。ToolHub format provider 负责 parser、editor、
schema、调用校验、locator 和结果投影；`document` package 负责 normalization、固定 lifecycle
hook 和准确 `(format, operation)` preservation policy；Agent format policy 负责 route grounding、
directory scope、decision evidence、argument binding、schema materialization 和结果 evidence
投影。三套 registry 仅通过规范 capability `format`/`operation` qualifier 连接，不共享实现类型，
也不形成跨 package mega-registry。公共 Workflow、Policy/Approval、inspection、output-copy、
cleanup 和 audit 路径继续保持格式无关。

有序、可执行的 format/operation 矩阵只在
`internal/app.DocumentFormatOperationSpecs` 定义一次。ToolHub、`document` 和 Agent
仍各自持有行为 registry，但每套 registry 在构造时都必须与该目录精确连接；缺少格式、缺少
operation、存在额外实现、重复 key 或顺序漂移时，会带具体错误 key 直接拒绝。目录只包含
text、DOCX、XLSX、PPTX 与 PDF operation；Agent 中不可执行的 image 路由 policy 不属于该
目录。

Office mutation schema 统一使用公共整文件参数 `source_sha256`，且每个 DOCX、XLSX、PPTX
edit 都必须提供。Agent 从当前 Workflow 定位 observation 绑定该参数，并把
`source_evidence` 与 `evidence_targets` 保持为仅 Runtime 使用的 provenance；它们不是
ToolHub schema 输入，直接调用方不能借此获得额外权限。只有 `document.Pipeline.Edit` 会把
绑定的整文件 hash 与最新 inspection metadata 比较；format provider 只保留目标级 evidence
校验。Pipeline 随后的再次 inspection 是同一次调用内独立的 TOCTOU 防护。text edit 与 PDF
transform 不要求源 hash。

ToolHub 注册 schema 继续作为执行权威，每个模型 stage 只接收派生的 schema 投影。Runtime
从该投影移除冻结 binding 与可证明的 operation 专属字段，把剩余模型可见参数记录为
`semantic_variables`，然后在 ToolHub 校验与 Policy 前回绑选中 capability qualifier、path、
identity、locator、hash、generation 和其他权威值。确定性 acquisition 或单 entry 决策只会跳过
当前模型判断；后续目标判断、工具选择、内容生成、approval 与 effect 节点仍留在同一 Workflow。

`ContextBuilder` 从带优先级、可降级的小节组装每个有界 model/tool loop。本轮
observations 使用统一的小信封，按因果顺序只出现一次并保留 artifact 引用。阶段可将
声明的、按消费者定尺的持久化证据切片物化到 `PROVISIONED_EVIDENCE` 小节；声明切片
不足时，冻结的通用 `SupportRequirements` entry 可暴露 `observation.read`，提供当前
session 内的有界回读。support entry 走普通 exposure、selection、Policy 和持久化 scope
校验；旧 plan 恢复时不会被扩大。Prompt 准入复用与实际
执行相同的 Router task policy 选择 model profile，采用 85% context window 安全系数
和离线标定的保守 token 估算；依次降级 session/tool 上下文、供给切片、较旧
observations，并始终保留最新两条，output contract 仍是 user prompt 尾部；固定尾部超限
会在模型调用前失败。run 级 observation 在 36,000 byte 开始压缩，但达到 48,000 byte 时
会先硬停止，不再尝试压缩。support read 有独立的每阶段两次执行配额，不消耗 business
tool-call 或重复调用预算。
执行失败把类型化原因与内部诊断分开；run summary、assistant message 和
`WorkflowResult` 只接收稳定安全文案，原始诊断只保留在审计中。
在已迁移的文档与浏览器控制视图中，当事实已经由 Runtime 绑定时，模型视图还会移除受治理 path、源/目标 hash、
page/snapshot identity、URL、generation 与 digest；只保留当前语义变量所需的 coverage/
omission metadata、candidate 局部内容与结构、opaque 可选 ref 和 eligible operation 描述。

Agent 所有的统一 `workflow_evidence_projection_record_v1` 抽象负责文档决策、文档/浏览器模型
stage 和最终化的消费者投影遥测。持久化 audit event 会记录 source/derived lineage、consumer/
stage/semantic variable、全部 coverage 维度、实际 model payload digest/bytes、archived bytes、
binding 引用、repair error 和 reuse。领域 policy 只构造类型化单元与 invariant，不建立平行
projection store 或各自独立的 audit 格式。

### Model Router

| Lane | 作用 |
|---|---|
| `fast` | Tree routing、文档读取/编辑推理与终结，以及其他有界快速推理 |
| `deep` | 非文档 Workflow 的默认 planning、assessment、repair 和 final answer 通道 |
| `embedding` | 启动语义图索引和 embedding channel query |
| `guard` | routing 或 tool execution 前执行 prompt moderation 的专用 Qwen3Guard |
| `mock` | 确定性本地开发/eval |

Gateway 选择逻辑 lane，模型输出不能自选。当前 `single-fast-v1` 部署把两个逻辑 chat
profiles 都解析到 Fast endpoint，因此不会加载 Deep 模型进程；为保持 Workflow 兼容，
trace 中仍保留 lane 标签。加载和容量策略见[模型加载](model-loading.md)。

### Message Control 与 Delivery

Endpoint Registry 解析 Web/第三方 destination。Schedule Registry 持久化 schema-v2 到期消息，
并执行 compare-and-swap 修改。Timer 只 claim 到期任务并通过 Message Runtime 重新发布。

每个 terminal route 产生一个 channel-neutral `WorkflowResult`。普通结果和显式 Web direct
send 都创建 `DeliveryRequest` 并调用 Delivery Gateway。`LocalWebDelivery` 是同一 Gateway
中的 Web provider adapter，不是平行 Web send API。见[消息与定时任务](messaging-and-scheduling.md)。

Connector Registry 为所有已注册第三方消息渠道提供统一 provider-neutral control plane。
持久化 owner `ConnectorSetting` 控制 inbound Runtime、Endpoint Registry 可见性和 outbound
Provider 访问。binding record 和加密 credential 是独立保留的账号状态，绝不表示渠道已开启。

启动时，Registry 会在 Gateway listen 前加载全部 owner setting。静态 channel `Enabled` 只作为
没有记录的 owner 默认值，显式 owner setting 可以覆盖它。进程内 write-through cache 是读取权威，
Connector Registry 是唯一受支持 writer；预加载错误会令启动失败，而不是回退静态状态。

Telegram 与微信各自保持每 channel 一个物理 worker。当静态默认值或任一持久化 owner 需要时，
worker 保持运行，并在 acquisition 与 dispatch 前通过 owner gate 过滤。关闭一个 owner 后，只会立即
阻断该 owner 的新 endpoint 解析、binding setup、polling 和 outbound send。已经 dispatch 的工作会
通过其已接纳 source reply 排空；已持久化但未 dispatch 的工作保持 pending，直到重新开启。这是同一
可信 Gateway 内的家庭逻辑隔离，不是 hostile-tenant 进程或 Store 边界。见
[按 Owner 的 Connector 启用](connector-owner-runtime-design.md)。

### 浏览器、文档与集成

浏览器使用固定 agent-browser 和 SparkClaw-owned Chromium profile，没有备用 browser backend。
现有 destination registry 是 candidate-independent 的命名目标 fast path；它 miss 且 browser
leaf 选定后，Workflow 可以使用 Info 的有序结构化 URL，不增加第二个 semantic classifier。

Info 搜索响应以单个版本化 aggregate 进入 ToolHub，不再生成多份平行 answer 副本。
`websearch` owner 只校验一次 source graph，再派生两个只读视图：一个保持顺序、按完整单元
加入并用于确定性 grounded rendering 的回答 projection，以及一个供浏览器目标识别独立使用的
有序 URL 视图。上游 action 建议只保留为 raw 不可信数据；conflict、freshness、uncertainty
和 projection omission 则保留为类型化回答限制。持久化 legacy result 只在 decoder 边界规范化。

页面读取保持全程 hidden 且必须
使用托管 session；click 和已审批 form draft 绑定 fresh page-generation evidence，并对 visible
结果进行验证。点击后的语义评估接收 action/transition 投影；Runtime 会拒绝重复已经验证的
语义 action。当 visible 结果的 profile、route 和 rendered content 等价时，通过派生 assertion
复用 hidden verdict；存在实质差异时才重新评估。见[浏览器 Runtime](browser-runtime.md)。

文档拥有独立于解析内容的持久化一等 `DocumentRecord` 身份。读取/编辑使用最近记录解析、
确定性 format inspection、可追溯的 `confirm_document_target` Workflow 节点、structured
parsing 和显式 `select_edit_operation` 决策节点。编辑定位节点使用冻结 path 直接且仅调用
一次按格式限定的 reader；读取前不存在模型工具选择步骤。当前所有文档模型调用都使用 Fast，
包括读取终结、多候选编辑决策和 editor 参数生成。多候选决策使用规范化投影内 candidate、
类型化 selected/no-match 输出和一次同投影修复，然后在 editor materialize 前持久化一个精确
ToolHub entry。PPTX 语义生成同样会在 approval 前过滤 no-op，并对无效 mutation output 提供
一次类型化修复；原内联目录二次路由仍保持删除。
approval、output-copy write 和 post-edit preservation check 继续走共享路径。解析
representation 可以不完整、被替换或重新生成，而不会丢失文档身份和活动谱系。
XLSX 读取还会投影带稳定 source hash 的有界类型化 sheet/row/cell 证据。revision 7 在
approval 前把每次电子表格修改绑定到该读取，并且只有通过 OOXML feature gate、类型化重读和
package part 保真校验后才接纳成功输出；不支持的特性或未声明 package drift 会失败关闭且不留下
输出副本。
可选的有界 `internal/documentocr` adapter 会把选中的 page image 发送给 OvisOCR2，并把
Markdown 作为不可信证据保留。扫描 PDF 页在 page/byte budget 内栅格化，成功 OCR 会提升到
稳定 PDF page block。OCR 不是 Workflow 选择的 chat lane；失败时读取明确保持 partial。
文档读取最终化分别记录 source coverage 与 claim coverage；source partial 或 finalizer 投影截断
时必须明确说明限制，并禁止整篇或否定性声明。
见[文档 Workflow](document-workflows.md)。

LocalMind 在 workspace-scoped manager 后复用 MCP 2025-06-18 Streamable HTTP client。
manager 每次刷新都重新解析环境 credential，校验固定 server identity 和 workspace endpoint，
先发现 scope 再原子注册，并在刷新失败时移除 stale scoped tool。Resource/tool result 进入有界、
不可信 observation/artifact 路径。Policy 把远端 write 视为 remote effect：保留 SparkClaw
approval，但不声称在本地 sandbox 执行。

当前旧反向链路使用 ISCP：通过认证的 LocalMind peer 可通过
`agent.notification.deliver.v1` 提交结构化提及。Gateway 校验不可信 deep link，并在确认前
按 peer 和 idempotency key 持久去重。这条被动路径进入 owner-scoped 全局 WebChat 收件箱与
SSE stream，不创建 conversation 或 Agent Runtime state。旧的 session/message 请求对只在目标
切换前临时向 LocalMind 开放；切换时删除 LocalMind 对两条路径的访问。JingSi 仍需要的共享
request type 在其当前 Bridge path 保留期间保持不变。已实现 SparkClaw 侧的
[JingSi 局域网 Web 客户端设计](jingsi-lan-connection-design.md)通过专用 allowlisted
presentation port 把一个 configured Web-visible session 绑定给 JingSi，只提供文本发送和持久
session message event 的过滤投影，不开放 mobile session/history API。它不是
connector/provider；JingSi client 改造与实体 LAN 验证仍待完成。

Telegram、微信、speech、Infinimesh Info 和 LocalMind 是共享 connector、delivery、
transcription、search 或 MCP contract 后的可选 adapter。见[外部集成](integrations.md)。

JingSi App 与 LocalMind 当前通过独立部署的 [ISCP Bridge](iscp-bridge.md) 接入。Bridge 终止 ISCP
transport，并调用一个 loopback Gateway API；session state、execution、Policy、approval、
被动通知、event cursor 和 audit 仍由 Gateway 负责。Gateway 启用 auth 时要求 bearer；默认
无 auth Gateway 也只接受 loopback Bridge dispatch。LocalMind 使用该 path 属于旧链路；其
bootstrap 等待外部 LocalMind controller 返回 SparkClaw Bridge 使用的 bundle，凭证权威方向与
LocalMind 目标方案相反。JingSi direct-LAN Web 客户端迁移属于独立设计；SparkClaw surface 已
实现，但在 JingSi client 改造与实体 LAN 验证完成前仍保留当前 Bridge path。

### 统一第三方接入（SparkClaw 管理表面已实现）

目标入站架构为 LocalMind 和未来选择该 contract 的第三方提供一个 provider-neutral 普通对话
MCP surface，不再为每个 provider 增加 adapter 或 API。通用 ISCP MCP Access Gateway 在已
enrollment 的外部 gateway device 与 SparkClaw 之间传输 MCP session。本地 service 只暴露一个
业务 tool `sparkclaw.conversation.send`；消息进入普通语义路由，选中的 Workflow 再通过现有
Runtime、Policy、approval 和 audit 核心执行。

JingSi 不在本设计范围内，因为它是 WebChat 移动客户端，而非第三方 MCP caller。它不接收 MCP
enrollment、tool projection、第三方 endpoint 或 sender。已实现的 SparkClaw direct-LAN
surface 复用 Web session、Web message ingress 与 LocalWebDelivery；JingSi client 改造仍待
完成。本项目在验证前保持 JingSi 当前所需最小 Bridge path 不变。

MCP protocol negotiation 与 capability listing 保留在专用 adapter 中，但 MCP business call
进入统一受管理的第三方链路。`tools/call` 创建 `third_party_device` `MessageEnvelope`、
owner-scoped MCP source endpoint 和冻结的来源 `ReturnRoute`。统一 router 使用服务端持有的
leaf binding 执行确定性 Top-1 selection；Runtime、Policy、approval、Store 和 audit 保持共用。

waiting 与 terminal result 都成为普通 `WorkflowResult` 和 `DeliveryRequest`。Delivery Gateway
解析 MCP source endpoint 并调用一个通用 MCP sender/provider，再把结果映射为经 ISCP 返回的
关联 MCP result、progress 或 Binding-scoped SparkClaw operation frame。第一版保持 MCP
`2025-06-18`，不声明标准 MCP Tasks。MCP 也复用第三方统一启用/暂停与
endpoint/provider 管理，但不必复用 polling 等不适用的 connector 内部实现。这样业务接收、
运行和发送 lifecycle 保持一致，同时不会把 MCP control traffic 伪装成 chat message。

SparkClaw 在本地发起 ISCP pairing flow，并向 owner 展示由此生成的一次性 Pairing Ticket。
owner 将其一次性交给 external Access Gateway；该 gateway 连接 ISCP，通过标准
PairingTicket/Provisioning 兑换凭证，加入 SparkClaw 所在的同一个 ISCP Domain。ticket 的定义、
签名、校验和消费，以及 Device Proof 校验、Trust Grant 与 Relay credential 签发、加密 session
建立、credential 轮换和 transport revocation 均由 ISCP authority 负责，SparkClaw 不重复实现
这些协议服务。认证 ISCP session ready 后，SparkClaw 独立签发短期、单次使用 MCP Access
Ticket。已入网 external device 只能通过该 session 兑换，SparkClaw 原子消费后激活本地 owner
批准的持久 conversation-scoped MCP Binding。普通 MCP 使用依赖 session identity
加 Binding，不复用任一 ticket。外部 device 保留为 requester/source provenance，SparkClaw 仍是
Workflow executor。两个 gateway role 都通过出站连接访问 Relay，因此 ISCP 不开放公网入站端口。
Owner 可以独立开启局域网直连 MCP，使其使用 WebChat `18790` 入口的 `/mcp` route；WebChat
把该精确路由代理到 Docker 内部 Gateway，该开关关闭时 route 不存在。

完整 trust、对话能力、调用、LocalMind 迁移和验收 contract 见
[统一第三方 ISCP MCP 接入](unified-third-party-access-design.md)。Owner-facing External MCP
设置表面与可配置 authority adapter 已实现。adapter 只通过一个带认证和边界的出站请求获取
`iscp.pairing_ticket.v2`，从不持有 Trust Root 签名材料。memory、file 或 PostgreSQL 只保留
非秘密 onboarding receipt；签名 ticket 仅返回一次。本地可以列出或撤销 MCP Access Ticket 与
conversation-scoped Binding，其中不包含 Catalog grant。

SparkClaw 自身负责的本地
runtime 已实现：严格 MCP `2025-06-18`、只存 hash 的单次 MCP Access Ticket、持久 peer
Binding schema v2 conversation scope、唯一业务工具 `sparkclaw.conversation.send`、普通语义路由、
经 discovery 前 owner approval 保护的有界纯文件名 Top-1 response-media 查询、共享 Delivery、Binding-scoped operation
恢复、默认关闭 channel gate 和脱敏 lifecycle audit。每个 Binding 拥有一个可见的
`AI · <设备短 ID>` 对话，其标题与内容生命周期不能通过普通 session control 修改；请求只经
authenticated Binding 进入，WebChat 将该对话作为只读界面展示。Inbound media locator 以尚未
验证且不可下载的要求展示；Binding revoke 或记录删除仍保留只读会话历史。Workspace approval
提供派生的人类可读审阅投影，而
授权仍只绑定冻结的 tool argument 与 authenticated policy context。Approval resolution 在
decision 持久化后立即返回；脱离 HTTP request 的 Gateway work 让 MCP operation 从
`approval_required` 经 `running` 进入独立的 execution 或 delivery 结果，同时仍受 operation
cancel、Binding revoke、invocation deadline 与 Gateway shutdown 约束。加密 Bridge 测试已在建立好的 ISCP session
中传输 MCP 请求和响应。生产 external onboarding 仍需真实 ISCP authority 实现已配置的
PairingTicket endpoint、可部署 external Access Gateway 和真实 Relay 验证，因此
现有 `agent.*.v1` Bridge 只临时保持可执行。LocalMind 使用新的 ISCP PairingTicket/Provisioning
加入 SparkClaw Domain、随后兑换 SparkClaw MCP Access Ticket 并通过新链路后，必须删除其外部
enrollment bundle flow、manifest entry、dispatch branch、fallback、config、test 和 guidance。
JingSi 仍需要的共享 Bridge
component 冻结保留，直到其共享 Web 客户端连接实现并通过验证；其中不得隐藏保留 LocalMind
fallback。
SparkClaw 主动访问 LocalMind workspace 的出站 MCP client 属于独立方向，保持不变。

## State 与 Artifact

Store interface 负责 session、message、Agent run、route/fusion evidence、Workflow state、
tool/model call、持久文档记录与谱系、approval、schedule、endpoint、delivery、connector
binding、connector setting、inbox、被动通知及 read state、memory、eval 和 audit event。
memory、file snapshot 和 PostgreSQL 后端实现相同的 durable state contract。

Artifact 保存大型或可检查输出，例如 tool observation、browser evidence、generated
document/media、可替换的文档解析 observation、memory export、rollback file 和 eval failure
archive。filesystem 与 S3-compatible backend 共用 metadata contract。secret 和 raw speech
audio 不是 artifact。

## 信任与安全边界

- 产品 Compose 将 Gateway 保持在 Docker-internal `gateway:18789`，并在 `18790` 发布
  WebChat；`/mcp` 的直连 ingress 仍由 owner 控制且默认关闭。可选 JingSi overlay 只在一个指定
  RFC1918 address 的 `18793` 发布精确 presentation allowlist。Host-process Gateway 调试继续只
  绑定 loopback。
- authenticated request 携带一个 owner/actor principal；endpoint/schedule query 按 owner 限制。
- ToolHub registration 是审批基线权威。Policy 只能通过类型化 execution context 加强
  `RequiresApproval` 与 risk decision；requester text、channel、destination 和本地模型参与都不能
  降低或独立提高该基线。
- 持久化 inbound MCP invocation 代表 external-AI principal。它访问 SparkClaw workspace 的原始或
  派生数据时，必须在文件/索引发现、metadata 检查、symlink 解析、hash、parse 或内容读取前取得
  owner approval。该 approval 绑定 MCP identity、local owner/authorized principal、locator/query、
  Workflow plan、output class 和 return route。
- Inbound MCP run 不把先前 session message、tool summary、memory、image 或 episode summary
  作为隐式 model context。已审批的当前 run evidence 仍可用；明确 workspace locator 必须进入
  governed access contract，而不能继承 cached derivative。
- 一个已批准的 external-MCP workspace data contract 覆盖其精确绑定 read 和冻结的 return/send，
  Delivery 不再二次审批。identity、locator、Workflow、output 或 target 改变会 fail closed；工具
  自身原有 reversible/dangerous approval 仍然适用，且不能被降低。
- 冻结的跨渠道结果在 target delivery 后更新原始 durable MCP operation。对 target 抑制的 waiting
  result 仍会记录 `approval_required`，因此 operation polling 不会一直停在 `running`；MCP result
  不会复制已经投递到其他目标的 payload。
- authenticated human 明确要求发送 text、image、audio、file、mixed 或 multipart 内容时已经完成
  授权，不增加 destination-only approval。持久化 legacy `message_control.external_send` approval
  不能恢复 delivery。
- reversible/dangerous effect 需要 Policy approval；shell 默认 sandboxed 且 network-disabled。
- browser URL、artifact path、workspace path 和 provider destination 确定性 normalize/validate。
- LocalMind MCP endpoint 只能来自 operator 配置，绑定到一个 workspace path，拒绝 redirect；
  除非显式允许 private HTTP，否则必须使用 HTTPS。
- external content、document、browser page 和 tool output 保持不可信并带 provenance。
- credential 通过环境/文件或加密 vault envelope 注入，不进入 public config、log、trace 或 artifact。
- timeout、size/concurrency limit、retry、idempotency 和明确 unknown outcome 是 adapter contract 的一部分。

## 稳定数据契约

重要跨 package 契约位于 `internal/app`：

- `MessageEnvelope`、`MessageContent`、`MessageIngressContext`、`ReturnRoute`；
- `RouteDecision`、`IntentFusionDecision`、`IntentEnvelope`；
- `WorkflowPlan`、`WorkflowState`、`WorkflowResult`；
- `ToolCall`、`ToolOutcome`、`Approval`；
- `DocumentRecord`；
- `MessageEndpoint`、`MessageSchedule`、`DeliveryRequest`、`DeliveryReceipt`；
- `ConnectorSetting`、`ConnectorStatus`、`NotificationBinding`、
  `PassiveNotification`；
- `ArtifactObject`、trace、audit event 和 model call。

Provider/UI 通过 owner package 和 public projection 消费这些契约，不能维护竞争 literal map 或重复 Store。

## 端口

| 服务 | 默认值 |
|---|---|
| Gateway | `gateway:18789`（Docker 内部，不发布 host port） |
| WebChat | `0.0.0.0:18790` |
| JingSi LAN presentation | `<指定 RFC1918 host>:18793`（仅可选 overlay） |
| Browser eval fixture | `127.0.0.1:18791` |
| Sandbox runner | `127.0.0.1:18889` |
| Fast / Deep / Embedding / Guard | `8001` / `8002` / `8003` / `8005` |
| 可选 ASR / OvisOCR2 | `8006` / `8007` |
| PostgreSQL / MinIO | `15432` / `19000`（`19001` console） |

## 扩展规则

1. wiring adapter 前先定义 owner package 和 typed contract。
2. 用户可见执行需要注册 capability topology 和唯一 Workflow Profile；只注册 ToolHub tool 不够。
3. 为每种 resource 增加 deterministic grounding 和 argument binding。
4. Policy、approval、delivery 和 persistence 必须走共享路径。
5. 按行为风险增加 focused contract test、必要时的 backend parity test 和 eval case。
6. 同一改动中更新 [Workflow 能力矩阵](workflow-capabilities.md)、相关专项手册，以及需要时的
   [开发](development.md)或[部署](deployment.md)。
