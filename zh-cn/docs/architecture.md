# SparkClaw 架构

> 语言： [English](../../docs/architecture.md) | 简体中文

本文档是当前系统事实来源。专项契约从[文档索引](index.md)进入；已完成计划和被替代设计
不会继续留在当前文档集合中。

## 产品边界

SparkClaw 是面向一个 owner、一个本地 Gateway 和 DGX Spark 级硬件的 local-first personal
agent runtime。当前产品表面包括：

- 本地文件、结构化文档和 approval-gated output-copy edit；
- 公开搜索、直接天气卡片、托管浏览器 open/focus 和受限验证页面交互；
- 基于稳定请求/上下文证据的普通聊天回答；
- 到期 payload 重新进入正常路由的定时消息；
- personal memory candidate 和 approval-gated sensitive memory；
- 可选 WebChat speech transcription、Telegram/微信消息和 Infinimesh Info evidence；
- trace、artifact、eval、Policy、approval、auth 和 durable state。

准确可执行叶子见 [Workflow 能力矩阵](workflow-capabilities.md)。邮件、日历和 workspace
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

WebChat 是 owner 工作台，展示 chat、schedule、direct delivery、connector binding、tool、
approval、memory、trace、artifact、eval 和 runtime setting。它发送 typed action，但不决定
route、Policy 或 delivery。见 [WebChat](webchat.md)。

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

每个有界 model/tool loop 在开始时冻结 full 和 compact 两个 system prompt。本轮
observations 按因果顺序只在 user prompt 中出现一次，随后是 output contract。Prompt
准入复用与实际执行相同的 Router task policy 选择 model profile，并使用 85% context
window 安全系数和离线校准的保守 token 估算。Compact fallback 只切换冻结的 system
variant，不压缩本轮 observations。

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

### 浏览器、文档与集成

浏览器使用固定 agent-browser 和 SparkClaw-owned Chromium profile，没有备用 browser backend。
见[浏览器 Runtime](browser-runtime.md)。

文档拥有独立于解析内容的持久化一等 `DocumentRecord` 身份。读取/编辑使用最近记录解析、
确定性 format inspection、可追溯的 `confirm_document_target` Workflow 节点、structured
parsing 和显式 `select_edit_operation` 决策节点。编辑定位节点使用冻结 path 直接且仅调用
一次按格式限定的 reader；读取前不存在模型工具选择步骤。当前所有文档模型调用都使用 Fast，
包括读取终结、多候选编辑决策和 editor 参数生成。决策会在 editor materialize 前持久化一个
精确 ToolHub entry；原内联目录二次路由仍保持删除。
approval、output-copy write 和 post-edit preservation check 继续走共享路径。解析
representation 可以不完整、被替换或重新生成，而不会丢失文档身份和活动谱系。
见[文档 Workflow](document-workflows.md)。

Telegram、微信、speech 和 Infinimesh Info 是共享 connector、delivery、transcription 或 search
contract 后的可选 adapter。见[外部集成](integrations.md)。

JingSi App 通过独立部署的 [ISCP Bridge](iscp-bridge.md) 接入。Bridge 终止 ISCP transport，
并调用一个带认证的 loopback Gateway API；session state、execution、Policy、approval、event
cursor 和 audit 仍由 Gateway 负责。

## State 与 Artifact

Store interface 负责 session、message、Agent run、route/fusion evidence、Workflow state、
tool/model call、持久文档记录与谱系、approval、schedule、endpoint、delivery、connector
binding、inbox、memory、eval 和 audit event。memory、file snapshot 和 PostgreSQL 后端实现
相同的文档记录契约。

Artifact 保存大型或可检查输出，例如 tool observation、browser evidence、generated
document/media、可替换的文档解析 observation、memory export、rollback file 和 eval failure
archive。filesystem 与 S3-compatible backend 共用 metadata contract。secret 和 raw speech
audio 不是 artifact。

## 信任与安全边界

- Gateway 默认只绑定 loopback，WebChat 是唯一默认 LAN surface。
- authenticated request 携带一个 owner/actor principal；endpoint/schedule query 按 owner 限制。
- reversible/dangerous effect 需要 Policy approval；shell 默认 sandboxed 且 network-disabled。
- browser URL、artifact path、workspace path 和 provider destination 确定性 normalize/validate。
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
- `ArtifactObject`、trace、audit event 和 model call。

Provider/UI 通过 owner package 和 public projection 消费这些契约，不能维护竞争 literal map 或重复 Store。

## 端口

| 服务 | 默认值 |
|---|---|
| Gateway | `127.0.0.1:18789` |
| WebChat | `0.0.0.0:18790` |
| Browser eval fixture | `127.0.0.1:18791` |
| Sandbox runner | `127.0.0.1:18889` |
| Fast / Deep / Embedding / Guard | `8001` / `8002` / `8003` / `8005` |
| PostgreSQL / MinIO | `15432` / `19000`（`19001` console） |

## 扩展规则

1. wiring adapter 前先定义 owner package 和 typed contract。
2. 用户可见执行需要注册 capability topology 和唯一 Workflow Profile；只注册 ToolHub tool 不够。
3. 为每种 resource 增加 deterministic grounding 和 argument binding。
4. Policy、approval、delivery 和 persistence 必须走共享路径。
5. 按行为风险增加 focused contract test、必要时的 backend parity test 和 eval case。
6. 同一改动中更新 [Workflow 能力矩阵](workflow-capabilities.md)、相关专项手册，以及需要时的
   [开发](development.md)或[部署](deployment.md)。
