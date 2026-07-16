# 消息控制与投递迁移

> 语言：简体中文 | [English](../../docs/message-control-delivery-migration.md)

本文记录已实现的消息控制与投递边界、现有 Reminder/Connector 数据的兼容行为，
以及未来第三方通讯 Provider 的唯一注册路径。

## Runtime 边界

- `internal/app/message_control.go` 定义 Provider 无关的 Endpoint 与 Schedule
  契约；现有 `MessageContent`、`WorkflowResult` 和 `DeliveryRequest` 仍是唯一的
  结果与投递契约。
- `internal/messagecontrol` 基于现有 Store 方法实现 Endpoint Registry、Schedule
  Registry 与 Return Route Resolver。
- `internal/delivery` 实现 Delivery Gateway、Provider Registry、整包能力协商和
  受治理 Artifact 解析。
- `internal/reminder` 现在是 Timer Runtime 兼容包。生产 Poll Loop 只 Claim 并
  放入有界队列，四个固定 Worker 负责 Provider 调用和定时 Agent 执行。

DeliveryRequest 携带 Owner 授权。Schedule 创建和最终投递都会拒绝属于其他
Principal 的 Endpoint，该校验位于控制层和 Gateway，不依赖 Provider 行为。

## Provider 注册

新增第三方通讯系统只能在 Connector 组合点完成，不能修改 Delivery Gateway：

1. 按需实现现有 Binding 与入站 Runtime 端口。
2. 实现 `delivery.Provider`：稳定 Key、声明内容能力，并按顺序投递完整请求。
3. 第一次外部发送前解析全部二进制 Part；不支持或无法解析的 Part 必须让整次
   请求明确失败。
4. 通过 `connector.Registration.Provider` 单点注册 Adapter。

Endpoint Registry 保存注册 Key 与 Binding Reference。Provider 凭据和原生 Client
保留在 Adapter 内。当前输出能力如下：

| Adapter | 文字 | 图片 | 音频/语音 | 文件 |
|---|---:|---:|---:|---:|
| Web 本地兼容实现 | 支持 | 支持 | 支持 | 支持 |
| 微信 | 支持 | 支持 | 不支持并明确失败 | 支持 |
| Telegram | 支持 | 支持 | 支持 | 支持 |

## Schedule 兼容

`Reminder` 保留为持久化兼容记录和公开 API 形态。新记录内嵌版本化
`ScheduleSpec`，包含：

- Payload 模式：`literal` 或 `request`；
- 有序多模态内容；
- Owner/Actor 授权；
- ReturnRoute 与可选的预期能力家族。

没有 `ScheduleSpec` 的旧记录会投影为 literal 文本 Schedule，投递前从 Binding
或 Session 推导 Owner。Memory 和 File 状态无需增加集合；Postgres 在 Runtime
Schema 与 `migrations/0001_core.sql` 中增加可空 `schedule_spec JSONB`。

`reminders.create` 工具新增可选 `payload_mode` 和 `expected_capability`，旧调用默认
为 `literal`。原有 List、Update、Cancel、Recurrence、Retry 和 Delivery History
行为保持兼容。

## Workflow 集成

当前组合层把定时 request Envelope 适配到旧 Agent 入口，再把完成的 Agent 结果
转换回共享的 `WorkflowResult -> DeliveryRequest -> Gateway` 链路。处于
`approval_pending` 或 `browser_login_blocked` 的 Run 不会被伪装成已完成结果。

Workflow 分支集成后，只需把 `cmd/sparkclaw/scheduled_messages.go` 替换为公共消息/
工作队列 Publisher。Schedule Registry、Timer Worker、Return Route Resolver、
Delivery Gateway 与 Provider Adapter 保持不变。

Web 投递当前保留旧的本地 Receipt 行为。后续可以通过端口把
`delivery.LocalWebDelivery` 替换为持久化 Web Event/Streaming，而无需修改
Workflow 或 Provider 代码。
