# 消息与定时任务

> 语言： [English](../../docs/messaging-and-scheduling.md) | 简体中文

本文档是消息进入、结果 delivery、第三方直接发送和定时消息的当前契约，替代 Message
Control 迁移、connector/Gateway assembly 计划、Web outbound 设计、worktree 计划和
定时任务设计记录。

## 一套消息架构

Web、第三方 connector 和 Timer 都是同一个 Message Runtime 的输入来源，不拥有独立 Agent loop。

目标[统一第三方 ISCP MCP 接入](unified-third-party-access-design.md)把 MCP 注册为受管理的通用
第三方 channel，同时保留专用 MCP protocol adapter。`initialize`、`ping` 和 `tools/list` 留在
该 control plane，MCP Access Ticket 兑换和第一版 `sparkclaw.operation.*` control tool 也留在
其中；`sparkclaw.conversation.send` `tools/call` 以 `third_party_device` `MessageEnvelope`
进入统一接收层，并使用普通 intent routing。结果通过 Delivery Gateway 和通用 MCP
sender/provider 返回。启用/暂停、endpoint 可见性和 provider 可用性复用第三方统一管理 contract，
但无需复用 polling 等不适用的 connector 内部实现。这样既复用业务 lifecycle，也不会把 MCP
control traffic 伪装成 chat message。

```text
Web/provider input 或 Timer claim
  -> MessageEnvelope + authorization + ReturnRoute
  -> normalization 和 intent routing
  -> 一个 WorkflowResult
  -> DeliveryRequest
  -> Delivery Gateway
  -> 解析后的 Endpoint 和注册 Provider
```

`MessageContent` 保留有序 text、image、audio 和 file part。`MessageEnvelope` 保留来源
endpoint、native message/thread identity、owner/actor authorization 和 return route。
对目标 MCP ingress，外部 device 作为 requester/source provenance 保存在类型化 MCP context，
本地 SparkClaw principal 仍是 Workflow actor。
这些契约位于 `internal/app`，与 provider 无关。

普通回答或发布请求使用 `conversation.answer` revision 3。第一个节点
`detect_response_media` 冻结结果是纯文本还是包含受治理 workspace 媒体；依赖它的 `answer`
节点不能再次搜索或替换文件。相对路径直接绑定；精确 basename 匹配区分大小写，未命中或名称
不完整时执行一次有界、只匹配文件名的查询，并选择稳定 Top-1。结果可以包含文本加媒体，纯
publish 只按原顺序返回治理后的媒体 part。Web 消息只有媒体 part 时，Message Runtime 直接按 typed content 选择同一
个已注册 `publish` 变体，不合成 owner 文本，也不创建独立的文件发送请求。

附件中的 workspace part 成为 result 前，Workflow 会在来源 session workspace 内校验它，
拒绝路径逃逸与符号链接，核对实际大小和 SHA-256，并注册或复用受治理的
`ArtifactObject`。该步骤发生在外部分发或任何适用审批之前，因此 Delivery Gateway 始终解析
准确的来源 session artifact，而不会按目标 endpoint 的 workspace 重新解释文件。外部 MCP
client 不能独立调用内部查询、列出 workspace 或获得本地路径。完整零结果会要求提供更明确名称
或直接附件；不完整查询不会发送临时候选文件。

## 职责

| 组件 | 职责 |
|---|---|
| Message Plane | 把 Web、connector 和 Timer 输入规范化为同一种 envelope |
| Endpoint Registry | 解析 owner-scoped Web/第三方目标及其状态 |
| Schedule Registry | 持久化 schema-v2 schedule，并执行 compare-and-swap 修改 |
| Timer Runtime | claim 到期 schedule，并通过 Message Runtime 重新发布 payload |
| Delivery Gateway | 解析 endpoint、预检完整 payload，并调用一个 Provider |
| Provider Registry | 保存 Web、Telegram、微信和未来 connector 的 live delivery capability |
| Connector Runtime | 负责 provider polling、inbound media、ack 和 ordering |

`LocalWebDelivery` 是注册在同一个 Provider Registry 中的 Web port adapter，负责把
channel-neutral result 投影成持久化 assistant message。它不是 Web 专用消息发送接口，
也不会绕过 Delivery Gateway。第三方 adapter 为各自协议实现相同的 `delivery.Provider` 边界。

## Delivery 规则

所有普通 Agent result 都进入 Delivery Gateway。发送第一个 part 前，Gateway：

1. 按持久化 owner 和 actor 解析目标 Endpoint。
2. 校验 endpoint 状态和 authorization scope。
3. 解析受治理的 artifact ref。
4. 用 provider capability 校验整个 multipart payload。
5. 带 idempotency key 分发一次，并记录 typed receipt。

Web delivery 把 text 保存为 assistant content，把受治理 image/audio/file 保存为 message
attachment。typing state、approval button 等 connector control traffic 留在 provider 内，
不是第二条 result path。

WebChat 的 delivery target picker 随普通 session message 提交可选的 opaque
`target_endpoint_id`。Message Plane 保持同一份 text、attachment、source、authorization、
normalization、routing 和 Workflow path，只冻结 result 的 `ReturnRoute`。向当前选中的第三方
endpoint 发布纯 image、audio 或 file 消息无需发送审批，只为该精确 endpoint 创建一个
`DeliveryRequest`，也不会在来源 WebChat 持久化或流式显示对应的 assistant result。纯文本和
其他第三方 Workflow result 仍保留既有 send-approval 边界。picker 不会把 attachment 转换成
direct-send draft，也不会调用 `/api/deliveries`。

Gateway 另外为已经持有最终 message content 的 client 提供显式 direct send API。该 API
校验全部 part，要求 confirmation，创建 durable delivery record，然后调用同一个 Delivery
Gateway。只有旧 receipt 明确哪些 part 失败且 outcome 已知时才允许 retry。

主要 API：

```text
POST /api/sessions/{id}/messages
POST /api/sessions/{id}/messages/stream
GET  /api/delivery-endpoints
GET  /api/deliveries
POST /api/deliveries
GET  /api/deliveries/{id}
POST /api/deliveries/{id}/retry
```

## 定时消息契约

`schedule.manage` revision 2 是唯一 lifecycle Workflow。schema-v2 `ScheduleSpec` 保存：

- 到期时的 `MessageContent`，不包含 `literal` 或 `ExpectedCapabilityPath`；
- owner、actor 和 authorization context；
- session correlation 和 recurrence；
- 指向 Web、一个已解析第三方 endpoint 或 nowhere 的冻结 `ReturnRoute`。

到期后 Timer 只 claim 工作，并把保存内容作为 `source=timer` 的新 `MessageEnvelope` 重新发布。
当前路由随后选择 payload 的业务 Workflow。Timer 不选择 capability，也不直接调用 Delivery Gateway。

因此 schedule 可以执行普通聊天提醒、天气查询、公开搜索或其他当前已注册 Workflow，
不需要把 Workflow 编码进 schedule record。

## 创建、查看、编辑与删除

自然语言 create/read/edit/delete 都路由到同一个 `schedule.manage` 叶子的不同变体。
edit/delete 使用两阶段 Workflow：

```text
reminders.list（fresh、pending、owner-scoped）
  -> 唯一解析目标
  -> 冻结 schedule ID + updated_at version
  -> reminders.update 或 reminders.cancel
  -> Schedule Registry compare-and-swap
```

模型不能修改猜测出的 ID。无匹配或多匹配会要求澄清。`updated_at` 过期会阻止 mutation，
避免覆盖并发修改。

WebChat 任务栏对 edit/delete 使用 typed `schedule_action`。它先加载 `GET /api/schedules`，
展示当前任务和提醒端（WebChat 或具体第三方软件/账号/接收人），再提交选定 ID 和观察到的
version。Workflow 仍执行 fresh owner-scoped list 和 compare-and-swap。编辑保留现有提醒端；
更换 delivery target 是另一项显式操作。

Schedule list 只返回当前 owner/actor 的 pending 或 sending 记录，包含 `editable`、
`cancelable` 和 endpoint status。endpoint 不可用时明确展示，绝不静默改成 Web。

## Connector 注册

每个可选 connector 只注册自身实现：binding lifecycle 和 credential ownership；一个
outbound `delivery.Provider` 及 capability declaration；可选 inbound
`connectorruntime.Runtime`；shutdown 和 binding cancellation。

Gateway assembly 遍历 registry。Message Control 和 Agent Runtime 不按 Telegram 或微信名称
分支。新 provider 必须复用现有 Endpoint、MessageContent、DeliveryRequest、receipt、Policy、
audit 和 Store 契约。provider 细节见[外部集成](integrations.md)。

同一个 registry 负责渠道 lifecycle control。`ConnectorSetting` 独立于账号 binding 保存 owner
的版本化 opt-in。静态 channel 配置只在 setting 不存在时作为初始回退。开启渠道会启动可选
inbound Runtime；关闭会取消该 Runtime，并 gate endpoint resolution 与 outbound delivery，
同时保留 binding。创建或发现 binding 绝不会改变 setting。

## 失败与 Audit 语义

- endpoint 缺失、撤销、未授权或不可用时，在发送前阻止。
- 不支持的 multipart payload 在发送第一个 part 前原子阻止。
- receipt 区分 delivered、可重试已知失败、未知 outcome 和不可重试失败。
- 同一 idempotency key 对应不同 target/content 时冲突。
- Timer claim、Workflow run、delivery attempt、endpoint resolution 和 schedule mutation 都持久化。
- 定时发送使用冻结 return route，不回退到碰巧 active 的 Web session。

## 验证

改动应覆盖 Endpoint/Provider registry、Web projection、保持 multipart ingress 不变的 target-picker
return route、multipart preflight、direct-send confirmation/idempotency、retry、schema-v2
persistence、Timer worker bound、到期路由、schedule target resolution、optimistic concurrency、
任务栏 API type 和 Web/第三方 return route。
