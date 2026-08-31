# Workflow 执行 Runtime

> 语言：[English](../../docs/workflow-execution.md) | 简体中文

本文是 `services/gateway/internal/agent` 中 workflow 原生执行 runtime 的贡献者
手册，覆盖每个匹配请求的执行路径、唯一的模型/工具步骤原语、预算与协议、恢复
语义，以及新增 workflow 代码的扩展点。旧的通用 ReAct 循环已彻底删除：工具只能
通过 workflow 执行，其背后不存在任何回退执行器。

相关文档：系统边界见[架构](architecture.md)，当前注册能力见
[Workflow 能力矩阵](workflow-capabilities.md)，逐步扩展流程见
[开发](development.md)，请求如何选中叶子见[意图路由](intent-routing.md)，正在迁移的
[Workflow 证据所有权与复用](workflow-evidence-ownership.md)设计说明如何把确定性事实
与 locator 固化进 Runtime，让消费者复用同一次采集事件，同时不合并独立事件。

## 执行流水线

每条入站消息走同一条流水线（`agent.go` 的 `handleMessage`）：

1. **归一化** —— message plane 生成渠道无关的 envelope 和资源投影。
2. **Guard + 路由** —— 安全 guard 可直接终止运行；意图路由返回唯一的
   `RouteDecision`。`clarify` / `blocked` / `unmatched` 是终态：直接产出结果，
   绝不执行工具。
   Web 请求没有 owner 文本且只有 image/audio/file part 时，typed media-content route
   直接选择已注册的 `conversation.answer#publish` candidate，不合成文本，也不调用语义路由模型。
3. **分发** —— `dispatchMatchedWorkflow`（`workflow_dispatcher.go`）把匹配叶子
   解析为一个带版本的 Workflow Profile，冻结计划（节点、迁移、参数绑定、完成
   证据、计划摘要），持久化到运行记录上，并为第一个 active scope 物化工具。规范化后的
   请求 `MessageContent` 也随 run 持久化，使无工具发布可以治理媒体、保持媒体 part 顺序并
   移除命令文本。
4. **阶段循环** —— `runWorkflowStream`（`workflow_runtime.go`）驱动有界阶段，
   直到 workflow 成功、阻断，或等待审批 / 浏览器登录交接。每个阶段执行四种节
   点调用形态之一：
   - **无工具模型回答**节点（`conversation_workflow.go`）；
   - **无工具消息完成**节点，不调用模型，只治理并冻结规范化后的 multipart 请求；
   - **直接工具调用**节点（`runWorkflowDirectToolOnce`），不经过模型步骤直接
     调用唯一绑定的工具；
   - 经 `runWorkflowModelStep` 的**模型步骤**，即共享步骤循环。
5. **评估 + 迁移** —— 每次 workflow 工具调用都被适配为类型化的
   `ToolOutcome`，由 Profile 评估并应用到持久化的节点状态；Profile 决策与迁移
   指令进入下一阶段的 observation。工具物化按 scope revision 重新计算。
6. **终结** —— 成功的 workflow 按冻结的 Profile 契约，通过 grounded 结果适配器投影
   类型化结果、为消息完成保留受治理的 multipart 请求内容，或用模型合成最终回答
   （`synthesizeWorkflowFinalAnswer`）。

### 流式执行所有权

`message.stream.started` 刷出后，已接受的 Workflow 使用 Gateway 生命周期上下文
运行，不再依赖 HTTP 请求上下文。浏览器刷新、页面跳转或 SSE 连接中断只停止事件
传输；Workflow 会继续运行到持久化结果、审批或模型超时，WebChat 通过现有轮询恢
复这些状态。每次 Fast 或 Deep 请求的上限仍由模型路由的 `http_timeout_seconds` 控制。

Gateway 关闭时会取消生命周期上下文，并等待脱离流连接的后台任务退出。真正的生命
周期取消只停止 Workflow 一次，并把运行记为 `cancelled`；模型超时或其他执行错误
记为 `failed`。两者都不得投影为 `completed`，只有持久化 Workflow 状态已经是
`succeeded` 时，运行才算完成。

脱离连接的执行绝不会直接跑在裸生命周期上下文上：Gateway 会用
`workflow_run_max_duration_seconds` 加模型 HTTP 超时和固定宽限推导出的硬截止
时间包裹它（`detachedExecutionContext`）。下文的运行预算才是优雅停止；这个截止
时间只兜底预算触发时仍在途的请求。

## Workflow 步骤循环

`workflow_step_loop.go` 持有唯一的模型/工具执行原语：

- `runWorkflowModelStep` 是唯一入口。它采用 Profile stage context 指定的通道，只在
  未指定通道时默认使用 Deep，然后调用 `runWorkflowStepLoop`。
- 一次 `ContextBuilder` 准入统一管理有序 system/user section。section 只能是 fixed，或按已注册、
  结构完整的命名 variant 降级。Owner question 与最新两条 current-run observation 固定不变；
  current-run observation 只按因果顺序出现一次，固定步骤输出契约始终是 user prompt 的精确末尾。
- Prompt 准入从所选容量 profile 获取 typed operation budget。明显可容纳的请求使用 Router 保守
  counter，边界请求由所选模型 tokenizer 精确计数。ContextBuilder 只通过已注册 variant 降级
  低价值 session context、供给 evidence、schema 与较旧 observation。
  每次成功模型调用都不超过有效输入阈值；若固定 section 本身超限，则在模型调用前返回
  `workflow_prompt_fixed_sections_oversized`。降级决策记录为
  `workflow_step.prompt_compressed`，但不记录被丢弃的原文。阈值始终为物理
  `context_tokens` 减去 operation 所属的 profile output-class budget；Model Router 会在
  provider dispatch 前对完整渲染请求重复硬检查。

### 工具结果与证据

每个工具结果都会完整归档，而模型可见 observation 不再按工具名放宽，统一使用
`observation_summary_max_bytes` 信封（默认 2400）。截断信封保留 artifact URI，并
提示模型使用 `observation.read`；该辅助工具只读取当前 session 所属 artifact 的有界、
UTF-8 安全窗口，单次最多 32,768 字节。模型节点通过冻结的
`CapabilityScope.SupportRequirements` 声明它；普通
exposure 与精确 directory selection 会把 support entry 与 primary business entry 一起
持久化。缺少该 requirement 的旧持久化 plan 在恢复时不会自动获得它。直接节点只投影
primary entry，模型节点投影 primary 加已选择的 support entry。

默认每个阶段最多执行两次 support read。ToolHub 中 completed 或 failed 的执行都会消耗
该配额和 observation byte，但不消耗 run-wide business tool-call 或重复调用预算。达到
配额后，下一个模型投影不再包含该 helper；首次越限尝试产生类型化协议 observation，重复
违反则以 `observation_read_limit_exceeded` 阻断。Runtime 自行评估 support outcome，
不会调用 Profile 的 business `Assess`，也不会推进节点。
文档与浏览器 tool-message 的 summary/structured field 是消费者投影：受治理 path、源 byte
metadata、page/snapshot identity、URL、generation 和 digest 留在归档 Runtime 状态中，不再
复制进模型消息。历史持久化 envelope 会在模型 context 边界重新投影；无法解析的旧文档/浏览器
summary 会降级为 tool 与 status，不会回放无界 locator 文本。

Profile 在阶段上下文中声明必需或可选的证据来源。模型调用前，Runtime 从已完成节点或
当前 workflow resource 解析来源、读取归档完整输出，并在 output contract 前插入有界
`PROVISIONED_EVIDENCE` 小节。文档切片保留完整段落或行，浏览器结构化切片保留完整的
opaque control ref；Runtime-owned 文档 hash/path 与浏览器 snapshot metadata 会被移除，
coverage、omission 数量、candidate 局部内容/结构和 eligible operation 描述继续保留。缺少
必需证据时在模型调用前阻断。`ContextBuilder` 对联合 prompt 依次压缩
session/tool 上下文、缩小供给切片、压缩较旧的本轮 observations；最新两条 observation
与末尾 output contract 保持不变。

### 步骤协议

每个步骤发送以 `WORKFLOW_STEP_REQUEST` 开头的 user prompt,要求模型只返回一个
JSON 对象：

```json
{"type":"action","tool":"tool.name","arguments":{},"reason":"short reason"}
{"type":"final","answer":"answer for the user"}
```

`parseWorkflowStepOutput`（`workflow_step_output.go`、`action_parser.go`）校验
封套并拒绝模型不可见的工具（`tool_not_visible`）。解析失败是可恢复的：循环追
加一条 `workflow_step.parse_error` observation，审计
`workflow_step.parse_failed`，让模型自行纠正；坏 action 绝不会被执行。

在 workflow scope 内,每次工具调用后循环都返回 workflow runtime，让结果先被评
估,同一 scope revision 下才允许下一次工具调用。若阶段要求工具证据
（`workflowStageContext.RequiresToolEvidence`），过早的 `final` 会被
`workflow_protocol_violation` observation 拒绝（审计
`workflow.required_tool_not_called`）；两次被拒后节点以
`required_tool_not_called` 原因阻断。

### 预算与停止条件

预算分两个作用域。**阶段预算**（`newWorkflowStageBudget`）在每次进入步骤循环
时重新开始，约束单个 scope revision 内的模型重试循环。**运行预算**
（`newWorkflowRunBudget`）由 `runWorkflowWithSeedAndStream` 每次运行只创建一
次，并穿过所有阶段——模型步骤与直接工具调用同样计入——因此其计数器能跨越
"每阶段一次工具调用"的边界。审批后恢复时，种子调用会重放进新的运行预算，而
运行墙钟重新计时（owner 的决策时间不计入预算）。

阶段预算停止步骤循环（审计 `workflow_step.budget_stopped`）：

| 配置键（`runtime` 段） | 默认值 | 触发停止的条件 |
|---|---|---|
| `workflow_stage_max_duration_seconds` | 180 | 该阶段墙钟时间耗尽 |
| `workflow_stage_max_no_progress_actions` | 3 | 连续动作未产生新证据 |
| `workflow_stage_max_observation_reads` | 2 | 已执行的 `observation.read` support call 达到阶段配额 |

`workflow_stage_evidence_max_bytes`（默认 8000）限制单阶段供给的持久化证据总量，同时
限制发给最终化阶段的文档读取证据。必需来源缺失、为空或无法装入预算时，阶段 fail closed
阻断。托管 256K Fast profile 将它设为现有文档最大抽取合同的 200,000 字节；浏览器 requirement
仍保留组件自身的 8,000 字节上限。环境变量
`SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES` 可覆盖该上限。

运行预算停止整个运行，既在步骤循环内检查，也在每个阶段开始前检查（后者审计
`workflow_run.budget_stopped`）：

| 配置键（`runtime` 段） | 默认值 | 触发停止的条件 |
|---|---|---|
| `workflow_run_max_duration_seconds` | 1800 | 整个运行的墙钟时间耗尽 |
| `workflow_run_max_tool_calls` | 32 | 所有阶段累计的已执行 business 工具调用达到上限 |
| `workflow_run_observation_compaction_bytes` | 36000 | 较旧且合格的 observation 开始滚动压缩 |
| `workflow_run_max_observation_bytes` | 48000 | 当前模型可见 observation 在压缩前达到硬停止线 |
| `workflow_run_max_repeated_tool_calls` | 3 | 同一工具在连续已执行调用中以相同指纹重复，可跨阶段累计 |

`SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES` 与
`SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES` 可覆盖两条 observation 边界。
Runtime 先检查 48,000 byte 硬上限，达到后直接停止，不再尝试压缩；低于硬上限时，达到
36,000 byte 才压缩合格的较旧条目，并保留最新两条及因果顺序。旧配置省略较低阈值时，
按已解析硬上限的 75% 派生；两值都显式配置时必须满足 `0 < compaction < maximum`。
压缩状态由执行器类型化字段持有，不从不可信 observation 文本标记推断。已废弃的
`workflow_step_max_*` 与
`react_max_*` 键（以及
`SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` /
`SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` 覆盖）仍作为回退加载（多个同时存在时
最新命名优先；旧的 step duration 只回填阶段时长，绝不回填运行时长）；新配置必
须使用 `workflow_stage_max_*` / `workflow_run_max_*` 命名。

### Scope 约束

模型输出无法扩大 workflow 边界：

- `materializedWorkflowCapability` 把所选工具映射到 active 节点/scope revision
  已物化的能力，否则该步骤失败。
- Primary `Requirements` 和通用 `SupportRequirements` 走同一套 exposure、Policy、
  精确 directory-entry、qualifier 与 active-scope 校验；工具名不会形成授权例外。
- `validateWorkflowToolPlan`（`workflow_runtime.go`）在执行前依据持久化的计划
  摘要、active 节点状态、阶段能力规则、qualifier 绑定和冻结参数绑定重新校验每
  个计划。
- `materializeWorkflowBoundArguments` / `bindWorkflowToolArguments` 用持久化的
  intent/route/outcome 状态覆写参数值，后续阶段无法偷换 query、URL、路径或元
  素引用。
- `workflowModelToolProjection` 从模型 schema 移除 Runtime-owned qualifier、冻结
  `ArgumentBinding` 值和 format-policy proof argument；Runtime 会在执行前回绑这些值，
  再使用未改变的 ToolHub 注册 schema 校验。

## 运行状态、模型调用与审计事件

运行状态：`received` → `routing` → `executing` → `workflow_step`（每个模型步
骤置位），以及终态/等待态 `completed`、`blocked`、`failed`、`cancelled`、
`clarification_required`、`approval_pending`、`browser_login_blocked`。

步骤循环产生的模型调用使用 operation `workflow_step_<n>`；恢复门控
（`hasWorkflowStepModelCall`）同时识别持久化数据中改名前的
`react_step_<n>`。

执行器发出的关键审计事件：

| 类型 | 含义 |
|---|---|
| `workflow.dispatched` / `gateway.dispatch` | 匹配叶子绑定到冻结的 workflow 契约 |
| `workflow_step.output` | 解析出一个步骤封套（action 或 final） |
| `workflow_step.parse_failed` | 可恢复的步骤协议解析失败 |
| `workflow_step.prompt_compressed` | 模型调用前替换为 compact system prompt |
| `workflow_step.evidence_provisioned` | 已为 active 阶段解析并切片持久化证据 |
| `workflow_step.evidence_blocked` | 必需阶段证据无法解析或装入预算 |
| `workflow_step.observations_compacted` | 预算检查前已压缩较旧的 run observations |
| `workflow_step.observation_read_limited` / `workflow_step.support_assessed` | Support read 配额执行与 Runtime 自有评估 |
| `workflow_step.budget_stopped` | 阶段或运行预算停止了步骤循环 |
| `workflow_run.budget_stopped` | 运行预算在阶段开始前停止了阶段循环 |
| `workflow.required_tool_not_called` | 拒绝了跳过必需工具证据的最终回答 |
| `workflow.transitioned` | 工具结果被评估并应用到节点状态 |
| `workflow.direct_tool_invoked` | 直接调用节点执行了唯一绑定工具 |
| `workflow.model_answer_completed` | 无工具模型回答 workflow 完成 |
| `workflow.message_content_governed` | 来源 session 的 multipart 请求 part 已校验并绑定到受治理 artifact |
| `workflow.message_completed` | 无工具普通 multipart 消息 workflow 完成 |
| `workflow.blocked` / `workflow.protocol_blocked` | 装配或协议失败阻断了 workflow |
| `workflow.execution_cancelled` | Gateway 关闭取消了 active Workflow |
| `workflow.finalization_failed` | 已完成证据无法渲染为最终回答 |
| `workflow.legacy_resume_retired` / `workflow.legacy_login_resume_retired` | 迁移前的持久化运行被关闭而非恢复 |

## 公开失败投影

执行失败分别携带类型化内部 `FailureCode` 与诊断。原始模型输出、schema payload、provider
错误、artifact body、主机路径和 wrapped error 只保留在审计或 model-call 记录中。Runtime
在创建 `run.Summary`、assistant message 或 `WorkflowResult` 前，用稳定且对 owner 安全的
文案替换失败，并通过 `WorkflowResult.Error.Code` 暴露类型化代码。

明确协议代码包括 `required_evidence_unavailable`、`tool_outside_active_scope`、
`semantic_preflight_failed`、`semantic_output_invalid`、
`workflow_prompt_fixed_sections_oversized` 与 `observation_read_limit_exceeded`。新增失败
路径必须增加类型化代码和安全投影，不能把 `err.Error()` 赋给 `FinalAnswer`。

## 浏览器 Revision 3 执行

`browser.automation` 与 `browser.interaction` 注册 revision 3；revision 3 增加冻结的 support
capability 契约，但不改变 business tool 链。持久化的浏览器 r1 plan 会作为未注册契约被
拒绝，不会由当前代码重新解释。共享 plan 负责 acquisition、
evidence、interaction 与 presentation：

- Runtime 直接调用被动 environment preflight、tab discovery、focus/open/navigation、
  settle、snapshot 和 visible presentation 阶段。
- hidden acquisition 总是在 semantic validation 前完成 settle 与 snapshot。每次
  navigation 或 click 都会让旧 ref 失效，必须重新 settle 并生成 generation-scoped
  snapshot。
- interaction 使用独立的 `browser.validate_transition` 和 `browser.assess_goal`
  capability。目标评估发生在 action 前、每次验证后的 action 后，以及最终 visible 结果上。
- presentation 是必需 Workflow 阶段，不是 completion callback。它把 managed profile
  转交 visible Chromium，open/focus 精确结果，完成 settle、snapshot 和校验。持久化
  result record 绑定 hidden 与 visible evidence；缺少 visible evidence 时 run 不能成功，
  且成功后保留该页面打开。

失败边界保持独立：被动 preflight、acquisition、settle、snapshot identity/generation、
route validation、transition validation、goal assessment、profile transfer 和
presentation 都可以分别 block。retry 由 plan transition 限制，并针对持久化状态幂等。

## 恢复语义

`ResumeRunAfterApproval`（`agent.go`）处理审批已解决的运行：

- **外部发送审批**走专用发送路径恢复。
- **Workflow 运行**经 `resumeMatchedWorkflowAfterApproval`
  （`workflow_dispatcher.go`）恢复：已批准的种子调用被重新评估进持久化计划，
  阶段循环在同一冻结 scope 内继续。
- **迁移前的持久化运行**：若被批准的动作是终结性的，运行以 grounded 摘要完
  成；否则以"旧运行时已下线"消息关闭，审计
  `workflow.legacy_resume_retired`。无 workflow 计划的运行在浏览器登录恢复时
  同理（`workflow.legacy_login_resume_retired`）。不存在重新进入通用循环的路
  径。

浏览器登录交接是持久化 revision-2 状态机。处于 `waiting_owner` 时，歧义回复不会执行
browser call，取消会保留 visible 页面，wrong-page 回复会重新打开冻结目标。只有明确
确认完成才会取得 transition lease 并进入 `validating_visible`。

Runtime 随后列出 visible tab，选择并 settle handoff 页面，生成 fresh visible snapshot，
再独立验证 route、认证状态和冻结任务页面。不匹配时回到 `waiting_owner`，向 owner 明确
说明原因。成功后把 exclusive managed profile 转交 hidden Chromium，重新取得并 settle
选中页面，再生成 fresh hidden snapshot，之后进入 `resuming_workflow`。登录前 ref 会被
丢弃，click budget 保持不变；profile 连续性丢失时回到 `waiting_owner`。

每次 transition 都通过 compare-and-swap 持久化，并带 transition owner 和有界 lease。
第二个 Runtime 不能重复执行活动 transition；Gateway 重启后，新 Runtime 可以接管已过期
transition。memory、file 和 PostgreSQL store 实现同一契约。

## 扩展点

按[开发](development.md)中的扩展流程执行；代码锚点如下：

- **能力叶子与路由** —— `internal/capability` catalog，以及由 Profile 注册表
  编译出的语义图。
- **Workflow Profile** —— 实现 Profile 并在 `defaultWorkflowProfileRegistry`
  注册（`workflow_profiles.go`、`workflow_registry.go`）。Profile 拥有计划形
  态（节点、阶段、迁移、供 `workflowStageLimit` 使用的
  `MaxAttempts`/`MaxActivations` 上界）、`Assess`/`TransitionInstruction`/
  `StageContext` 函数、决策，以及终结模式（`workflowFinalizationModel` 或 grounded
  投影）。
- **节点调用形态** —— 默认模型步骤；设置
  `InvocationMode: app.WorkflowInvocationDirectOnce` 表示免模型的单次工具调
  用；无工具会话 Profile 按冻结 completion rule 使用模型回答或消息完成节点。
- **工具** —— 在 `internal/toolhub/registry.go` 注册（一致性测试禁止按名
  switch 注册）。声明带 qualifier 的能力,使物化与阶段规则可以绑定它们；当
  workflow 需要从工具结果提取类型化信号时,补充结果适配器
  （`workflow_outcome.go`、`tool_result_adapter.go`）。
- **参数绑定** —— 把工具参数绑定到 intent target、route slot、route fact 或先
  前的 outcome ref，让取值从持久化状态物化，而不是信任模型输出。
- **语义变量** —— 模型 schema 只保留尚未解决的目标判断、eligible tool 选择或内容参数；
  不要仅为了让模型复制回来而暴露 Runtime fact。
- **证据需求** —— 在 `StageContext` 中声明持久化来源节点或当前 workflow
  resource kind，选择 `head` 或 `structured` 切片，并让必需来源保持 fail closed。
- **预算** —— 按部署在 `runtime` 配置段调整 `workflow_stage_max_*` /
  `workflow_run_max_*` 键；不要为单个 workflow 开旁路。

修改 prompt 组装时,保持 `agent_test.go` 覆盖的不变量：observation 在 user
prompt 中只出现一次、步骤输出契约位于 user prompt 尾部、system prompt 各节保
持稳定顺序以利缓存前缀。

## 旧版迁移备注

2026-07 的迁移重命名了执行面并删除了通用循环。旧标识符只出现在持久化数据兼容
垫片中：

| 旧 | 新 |
|---|---|
| `react.go` / `react_output.go` | `workflow_step_loop.go` / `workflow_step_output.go` |
| `runReActLoop` / `runReActLoopWithSeed` | 已删除（`runWorkflowModelStep` 是唯一入口） |
| Prompt 标记 `REACT_OUTPUT_REQUEST` | `WORKFLOW_STEP_REQUEST` |
| Observation 标记 `react.parse_error` | `workflow_step.parse_error` |
| 审计事件 `react.*` | `workflow_step.*` |
| 运行状态 `reacting` / `react_step` | `executing` / `workflow_step` |
| 模型调用 operation `react_step_<n>` | `workflow_step_<n>`（恢复时仍识别旧前缀） |
| 配置 `react_max_*` 与 `workflow_step_max_*`、`SPARKCLAW_REACT_MAX_OBSERVATION_BYTES` / `SPARKCLAW_WORKFLOW_STEP_MAX_OBSERVATION_BYTES` | `workflow_stage_max_*` / `workflow_run_max_*`、`SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES`（旧键作为回退加载；旧 step duration 只映射到阶段时长） |
| unmatched 契约引用 `react.unmatched` | `legacy.unmatched` |
