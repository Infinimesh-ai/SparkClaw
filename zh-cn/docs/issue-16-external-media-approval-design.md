# Issue #16 工具策略审批设计

> Language: 简体中文 | [English](../../docs/issue-16-external-media-approval-design.md)

> 状态：针对 [issue #16](https://github.com/Infinimesh-ai/SparkClaw/issues/16)
> 的已实现设计。Owner 于 2026-08-17 确立 ToolDefinition 加 Policy 为审批
> 权威。“外部 MCP”指 SparkClaw 暴露给 external AI 的 MCP surface，而不是 SparkClaw 接入
> 的 MCP server，也不指 SparkClaw local model。实现所需产品边界均已裁决。

## 决策摘要

审批是 execution property，不是 delivery-destination property。每个 Workflow 使用已注册
tool 的 `Risk`、`RequiresApproval` 加共享 Policy decision。Requester、ingress adapter、
return route 与 result endpoint 不能仅因自身不同就覆盖这条 baseline。

Owner 人类明确请求自己的数据，构成对该次有界数据操作的同意。通过 SparkClaw 对外暴露的
MCP 进入的调用不同：它的直接 caller 是外部 AI principal，而不是 owner 人类。AI 不能在
prompt text 中自行声明出人类同意；当它请求 SparkClaw workspace data 时，Policy 必须在
protected access 前暂停并取得 owner 对这次安全边界跨越的审批。

Human owner 明确的 send/publish 指令授权该次精确发送，不需要审批。SparkClaw local model
参与 routing 或 tool selection 不改变这份 authority，也不是本文所说的“external AI”。如果
external AI 通过 SparkClaw exposed MCP 请求 workspace data，唯一一次 workspace-data approval
绑定完整 operation、output class 以及 requested return/send target；审批后 Delivery 不再二次
询问。Text、image、audio、file、mixed 与 multipart content 规则一致。

例如：

- 无论谁请求天气、普通结果返回到哪里，`weather.lookup` 都保持 safe read 且不审批；
- 已标记为 approval-required 的 tool 无论由谁调用都保持审批；
- 外部 AI 通过 SparkClaw 的 inbound MCP 访问 owner workspace data，是第一条已接受的
  contextual escalation，必须审批；
- human-explicit send 不会仅因 local model routing/execution 而审批；
- external-AI MCP workspace-data operation 生成一条覆盖 frozen output/return/send 的 approval，
  不叠加 prompt；
- 其他 tool 都保持其注册的审批行为。

Principal 或 transport 单独出现并不触发 escalation：external MCP 的天气查询及普通 protocol
response 仍然不审批。已接受的 contextual escalation 是经过 SparkClaw exposed MCP 认证的
external AI 访问 governed SparkClaw workspace data。上下文规则只能加强 ToolDefinition
decision，绝不能把需要审批的 tool 降级为安全、从展示文字推断风险或 consent，或建立第二套
按渠道区分的 tool catalog。

## 已实现边界

Runtime 现在把类型化 `PolicyExecutionContext` 持久化到 context-bound tool call 与 approval，
memory、file 和 PostgreSQL store 均保留该契约。隐藏的 `workspace.data.access` ToolHub capability
进入普通 Policy/Approval 链路，但不向模型暴露。external-MCP response-media 查询和明确文档
路径都在 filesystem/index access 前进入该 gate；已批准的文档读取复用同一冻结契约，不产生
第二个数据审批。没有覆盖契约时，普通 workspace tool 仍按相同 context rule 升级审批。

Inbound MCP run 不会把先前 session message、tool summary、memory、image 或 episode summary
作为隐式 model context 继承，因为这些 store 可能包含不属于当前冻结请求的 workspace derivative
及其 lineage。当前 run 中已审批的 evidence 仍然可用；需要 workspace data 的 caller 必须在当前
请求中提供由 approval contract 治理的 locator。该 contract 除 MCP device/binding identity 外，
还绑定 local owner 与 authorized principal。跨渠道 return 在冻结 target delivery 后仍会更新原始
durable MCP operation；被抑制的 waiting result 只记录 `approval_required`，不会向该 target 发送内容。
跨渠道 operation polling 只记录状态，不会把已投递的 workspace payload 再复制到 MCP result。

destination-owned `message_control.external_send` approval creation 已移除。human-explicit text/media
delivery 通过共享 Workflow 与 Delivery 链路直接执行，持久化 legacy approval 则 fail closed。
context-bound approval 修改返回 conflict，审批恢复时重新校验 argument、MCP identity、Workflow
plan、output class 与 return route。

### Owner 审阅界面与恢复生命周期

每个 MCP Binding 拥有一个可见对话，标题为 `AI · <设备短 ID>`。标题从经过认证的 device
identity 派生；普通 session rename、delete 与 message API 都拒绝写入 MCP 对话，请求只能经
authenticated Binding 进入。File Store 加载与 PostgreSQL migration 会把既有隐藏的
`External MCP` 对话规范化。WebChat 因而把该对话作为只读审阅界面，只在 session title 中标识
一次 external AI；对话内的 inbound request 使用中性作者标签“要求”，仅展示具体文本与媒体请求
条件。Revoke 或删除 access Binding 会停止访问，但保留该只读会话历史；删除历史需要另一个明确的
数据清理动作。

MCP `media` locator 以 `requested_media` 持久化在 user message 上。它们显示为不可点击、尚未
验证的请求条件，绝不投影成可下载附件。因此 pure-media call 也有可见消息，同时不会暗示
SparkClaw 已找到、打开或授权该文件。

Approval API 从冻结的 approval argument 与 authenticated `PolicyExecutionContext` 派生只读
presentation，展示受管理的 AI 对话标题、未验证 locator、access class、output class、return
route 与 single-operation scope；raw argument 折叠在技术详情中。该 presentation 不作为持久化
授权输入，恢复执行时也绝不读取它。

Owner decision 持久化后，MCP approval 返回 `202 Accepted`，并分别给出
`approval_status=approved` 与 `execution_status=running`。Tool execution、Workflow resume 与
delivery 在 Gateway lifecycle context 中继续，不依赖 approval HTTP request。恢复后的工作还会
重新登记到原 MCP operation 的可取消生命周期中；operation cancel、Binding revoke、原始
invocation deadline 或 Gateway shutdown 都会取消后台 context，阻止后续 Workflow 或 delivery
继续。Durable operation 因此遵循：

```text
running -> approval_required -> running -> succeeded | failed
```

Reject、resume failure 与 delivery failure 使用不同的 operation error code。已成功的 approval
不会仅因后续 execution/delivery failure 被改写成审批失败；迟到的 waiting result 也不能把已经
批准的 execution 退回 `approval_required`。

## 为什么否决按目标审批

Receive 与 send adapter 刻意相互独立。请求可以从 Web、微信、Telegram 或 inbound MCP
进入，调用同一个 Workflow/tool，再通过相同或不同 transport 返回。Endpoint kind 只描述
transport，不描述 execution risk。Human consent 与 external-AI delegation 是经过认证的
principal fact，不是 channel name 或 delivery destination 的别名。

把 `EndpointKindThirdPartyDevice` 作为审批开关会错误地：

- 仅因为天气结果通过 MCP 或 connector 返回，就要求 safe weather lookup 审批；
- 让同一个 tool 根据 transport 在安全与危险之间切换；
- 把 local model participation 错当成新的 external principal；
- 把安全策略散落到 Message Control、Agent、Timer 与 Delivery。

上一版草案的 endpoint-kind 方案已经被替代，不得实现。

## 当前架构缺口

ToolHub definition 已携带 `Risk` 与 `RequiresApproval`。Policy 对 model-selected、direct-once
和 manual tool call 一致应用这些事实。例如，`weather.lookup` 与 `files.read` 当前都是
read/no-approval，文档 mutation 与 dangerous tool 则需要审批。

Issue #16 位于普通路径之外：

1. `message_control.external_send` 是 Agent 创建的 confirmation action，不是经普通 Policy
   判断的已注册 business ToolDefinition；按最终模型，destination 单独出现不是审批原因。
2. `conversation.answer#publish` 可以不调用 ToolHub 而确定性完成，因此 text/media
   publication effect 没有 tool definition。Authenticated human-explicit publish instruction
   即使由 local model 识别/routing，也可以这样执行。
3. 纯媒体又有两个显式早退，绕过文本外发使用的额外 post-result approval。
   这种不一致确实存在，但 content type 与 destination 都是错误的 policy input。
4. Inbound MCP response-media resolution 可以代表 external AI 在确定性 Workflow node 中读取
   workspace filename、metadata、hash 和 bytes，同样不经过普通 tool Policy。
5. `policy.Engine.Decide` 当前只接收 ToolDefinition 与 argument，没有类型化
   invocation/resource context 表达已接受的 external-MCP workspace escalation。

只删除两个媒体早退仍会保留错误的 destination policy。Issue #16 的正确修复是删除
destination-owned post-result approval，并在 protected discovery/read 前增加 inbound-MCP
workspace-data escalation，在该数据边界对所有 content type 一致处理。

## 目标策略模型

### 基础工具权威

已注册 ToolDefinition 仍是 baseline 与唯一事实来源：

- `RequiresApproval=true` 始终需要审批；
- dangerous risk 保持 configured dangerous-tool approval 与 deep verification；
- configured `approval_required_tools` 继续提升指定 tool；
- safe tool 保持无审批，除非类型化上下文规则提升它。

不会按 requester clone 或 mutate tool definition。Workflow stage 中模型可见的 risk metadata
保持稳定。

### 类型化执行上下文

Policy execution 除 definition 与精确 argument 外，还接收一份很小的 typed context，包含
Runtime 已冻结的安全事实，例如：

- authenticated principal class，区分 owner human 与通过 SparkClaw exposed MCP 调用的
  external AI；
- persisted inbound MCP invocation 与 requester identity；
- Workflow/run identity；
- governed resource class，包括 `sparkclaw_workspace_data`；
- requested access/effect class，包括读取 source、transform，以及披露已有 derivative。

Runtime 只从经过认证并持久化的 MCP invocation（`MessageRunContext.MCP` 及其 requester
identity）推导 external-AI fact，绝不从 provider name、prompt text、endpoint ID 或 model
output 推导。MCP request 仍在绑定的 local owner authorization 下执行，但这并不能证明 owner
本人批准了这一次 workspace disclosure。Approval 前，Runtime 只能对 external AI 提供的
locator/query 做纯语法与词法 workspace scope 校验，不能访问 filesystem。Policy 只消费这份
typed request classification；它不解析 host path，也不直接查询 Store。

Context decision 保持单调：

```text
effective_requires_approval =
    tool_definition_requires_approval
    OR configured_tool_escalation
    OR dangerous_tool_policy
    OR (external_mcp_ai_principal AND sparkclaw_workspace_data_access)
```

不存在 contextual downgrade。

### Workspace Data 内容等价

Governed boundary 同时覆盖 workspace 原始内容及其每一种派生表示，包括 raw file/bytes、
extracted text、summary、excerpt、基于内容生成的 answer、OCR、image preview/thumbnail、
audio transcription、transcoding、embedding，以及其他 transformed 或 cached representation。
改变输出格式不会使数据脱离受保护分类。

如需新建派生结果，必须在第一次读取 source content 前审批。如果 derivative 已存在于 cache、
artifact、index 或先前 Workflow state 中，则必须在向 external AI 披露该 derivative 前审批。
先前经过 human authorization 的读取不能授权后续 external-AI disclosure。Approval 必须绑定
精确且已冻结的 MCP invocation、requested locator/query、deterministic resolution contract、
operation 与 output class。审批后由 resource owner 仅解析一次，校验权威 workspace root 与
symlink boundary，并在读取前冻结 selected resource identity/version。任何 ambiguity、replan
或 resource change 都必须 fail closed 或重新审批；审批不能成为整个 workspace 的 blanket
grant。

### Approval 前禁止 Workspace Discovery

Approval 前不能访问 filesystem 或 index：不得检查 existence、枚举 filename/directory、跟随
symlink、调用 `stat`、判断 MIME type、读取 timestamp/size、计算 hash、搜索 index、检查
cached derivative 或读取 content。这些信息本身也属于 workspace data。

Approval display 只能展示 external AI 提供且尚未验证的 locator/query、已认证 MCP requester、
intended operation、bounded resolution rule 与 output class。审批后才执行 deterministic bounded
discovery，选择不超过 contract 允许范围的 resource set，并在第一次 content access 前冻结。
除非属于已批准 operation，否则不得向 external AI 披露 discovery result。

### 确定性 Workflow 效果

任何代表 external AI 访问 governed workspace data 的确定性 Workflow step，都必须进入同一
套 Policy/approval 机制。它可以由 Runtime 直接调用 registered capability，不需要把 tool
choice 暴露给模型；response-media lookup、read、transformation 与 export 都适用。

`conversation.answer#publish` 对 authenticated human-explicit instruction 保持确定性、
consent-bearing Workflow operation。Semantic model routing 可以识别这条指令，但 local model
participation 不会建立 external-AI principal 或第二次审批。Normalized source instruction、
content/output class 与 requested target 必须在 untrusted data 影响执行前冻结。

对于调用 SparkClaw exposed MCP 的 external AI，persisted MCP invocation 就是 principal
boundary。如果 operation 访问 workspace data，唯一一次 approval 包含所有 frozen data 与
output/return/send fact；任何变化都是新 operation，不能复用。Data-boundary approval 后不再
增加 send approval。Issue #16 既不能在该边界保留 media-specific bypass，也不能保留
destination-specific gate。Text、image、audio、file、mixed 与 multipart data 使用同一规则。
Audio 属于媒体。

## 初步适用矩阵

| Invocation | Tool/effect | 审批 |
|---|---|---|
| 任意 requester/channel | Safe weather lookup | 否 |
| Human owner request | 通过当前 safe read tool 读取自己的 workspace data | 不额外审批；该有界请求即 consent |
| Human owner instruction | 显式发送/发布 text 或任意 media | 否；authenticated instruction 即 consent |
| Human owner Workflow | local model 为该 request routing/select tools | 不按 origin 升级；使用各 tool 现有 policy |
| Inbound SparkClaw MCP，external AI | weather 等 safe non-workspace lookup | 否 |
| Inbound SparkClaw MCP，external AI | SparkClaw workspace 原始或派生数据 | 是，contextual escalation |
| Inbound SparkClaw MCP，external AI | 已批准 workspace data 后执行 frozen return/send | 总计一次 workspace-data approval；不再 send approval |
| 任意 invocation | 对同一 invocation 的 ordinary response | 不产生 send approval；仍应用 underlying tool/data policy |
| 任意 requester/channel | 已标记 approval-required 的 tool | 是 |
| 任意 requester/channel | 其他 safe tool | 否 |
| 仅 destination 变化 | tool/effect 不变 | 审批不变 |

## 安全不变量

- ToolDefinition 加 Policy 是唯一审批权威。
- Requester、source channel、return route 与 endpoint 绝不降低审批；transport 单独变化也不
  提高审批。
- Context rule 类型化、集中、可审计，并且只能提升。
- External-AI identity 来自已认证并持久化的 inbound MCP invocation，不来自 user text、
  所谓的人类指令或 transport label。
- Human consent 只覆盖 owner 精确请求的 operation；local model participation 不会使其失效或
  扩大。
- Authenticated human-explicit send/publish instruction 授权该次精确且冻结的 send。
- Workspace classification 依据权威 governed root 解析；仅凭 path string 不能触发或逃避规则。
- 在精确 request/locator 与 resolution-contract binding 后审批，但必须早于任何 filesystem/
  index lookup、content access、disclosure 或 effect。
- 原始内容及每一种 derivative 保持相同的 governed classification；cached derivative 不能绕过
  审批。
- 先前 session message、tool result、memory、image 与 episode summary 不会成为 inbound MCP
  run 的隐式 context；workspace-derived 的当前 run evidence 只能在绑定审批后进入。
- Approval 前不得检查 workspace existence、name、metadata、hash、index match 或 cached
  derivative presence。
- Approval resume 重新校验同一 tool、argument、context、locator/query 与已经绑定的 governed
  resource identity；不一致即阻断。
- 包括 audio 在内的任何 media kind 都没有特殊旁路。
- External-MCP-AI workspace access 及其 frozen requested output/return/send 只生成一条
  approval record，不叠加 data 与 delivery approval。
- Provider 与 Delivery adapter 不作 Policy decision。
- Destination-owned `message_control.external_send` 不再作为 approval source；由 typed inbound-
  MCP workspace-data rule 接管其 security role。

## 预期实现范围

- `internal/policy`：增加 typed execution context 与 monotonic contextual escalation，同时
  保留现有 definition/config rule；
- `internal/agent`：在所有 Workflow tool execution path 中传递持久化 principal/run/MCP
  context，治理 deterministic workspace access，并删除 destination-owned
  `message_control.external_send` 及其 media exception；
- `internal/toolhub` 加 owning Workflow：注册或直接调用接入 shared Policy 所需的 protected
  workspace-data capability；不能仅因 routing 创建 local-model 或 external-send approval tool；
- `internal/app`：只增加这些 owner boundary 所需的最小 typed context/resource contract；
- tests/docs：覆盖 local、connector、Timer、inbound MCP、text、image、audio、file、mixed 与
  multipart case，不增加 provider-name branch。

如果 interface 或 durable context 变化，memory/file/PostgreSQL 路径必须完整。不能仅为内部
审批步骤暴露一个新的 external-MCP tool。已有且未完成的 legacy external-send approval 不得
自动投递；标记 stale/blocked，并要求重新发出指令。

## 验证方向

- 同一个 weather Workflow 从 Web、connector、Timer 与 inbound MCP 进入时都不审批；
- 所有现有 approval-required tool 从任何 origin 进入都保持审批；
- human owner 明确请求的有界 safe workspace read 保持 tool 的现有行为；
- 已接受的 external-MCP workspace access case 在 protected access 前暂停，只持久化一个
  approval，并恢复完全相同的冻结 operation；
- approval 前的 exact-path 与 fuzzy-query case 都保持零 filesystem/index read；approval 只展示
  unverified request 与 bounded operation；
- approval 后的 resolution 冻结 deterministic resource identity/version；ambiguity、resource
  change 或 replan 都不能复用审批；
- raw file、summary、excerpt、OCR、thumbnail、audio transcript、transcode、embedding、
  cached derivative 与 answer-from-content case 都触发相同 contextual escalation；
- reject 或 mismatch approval 不暴露任何 workspace content/media；
- Web/connector 上 authenticated human-explicit text/image/audio/file/mixed/multipart send 都不
  增加审批；local model execution 不改变该结果；
- 包含 frozen return/send 的 external-MCP-AI workspace operation 只生成一条 data-boundary
  approval，reject 或 mismatch 后不得 delivery；
- locator、query、output class 或 target 变化都不能复用该 approval；
- 不再创建 destination-owned `message_control.external_send` approval，未完成的 legacy
  approval 不能自动 delivery；
- direct-once、model-selected、manual 与 deterministic Runtime invocation 使用同一个 Policy
  decision API。

实现需通过聚焦与完整 Gateway build/test/vet、golden eval 和双语文档检查。provider-neutral
测试是权威证据；live MCP、微信或 Telegram credential 只作为补充。

## 已确认决策

Owner 于 2026-08-17 确认：

- 审批依据 tool/effect risk，不依据最终 destination；
- weather lookup 等 safe tool 不因 caller 不同而要求审批；
- dangerous 或 data-sensitive execution 不得绕过审批；
- “external MCP”指 SparkClaw 暴露给 external AI 的 MCP surface，而不是 SparkClaw 调用的
  generic/LocalMind MCP server；
- human owner 请求自己的数据，构成对该次 bounded request 的 consent；
- external AI 通过 inbound MCP 访问 SparkClaw workspace data，是第一条 contextual
  approval escalation；
- workspace 原始内容与所有 derivative 都必须在 source read 或 derivative disclosure 前触发
  该 escalation；
- approval 前只允许纯 locator/query syntax 与 lexical scope validation；所有 discovery 与
  metadata access 都在审批后发生；
- authenticated human-explicit send/publish instruction 授权任何 text/media type 的该次精确
  send，即使由 local model routing/execution；
- 本 contextual rule 中的 “AI” 仅指调用 SparkClaw exposed MCP 的 external AI，绝不指
  SparkClaw local model；
- external-MCP-AI workspace-data operation 只有一条 approval，包含 frozen output 与 requested
  return/send target，不再增加 send approval；
- 删除 destination-based `message_control.external_send` gate；未完成的 legacy approval fail
  closed，不能投递；
- 其他 tool 保持其 registered approval setting；
- audio 属于媒体集合。

## 决策历史与就绪状态

Issue 最初的 owner comment 要求恢复 image/file 与 text 相同的 external-send approval，只豁免
same-session Web。后续 owner 裁决取代 destination-wide gate：human-requested send 已经获得
授权，SparkClaw local model 只是 local executor，不是 external AI。受保护的 AI boundary
特指 external AI 通过 SparkClaw exposed MCP 请求 workspace data；所有 content type 在该边界
继续受保护，而 destination 与 media kind 都不是审批输入。

已无待决产品问题。Approval boundary 与 owner 审阅界面已经按此 contract 实现。
