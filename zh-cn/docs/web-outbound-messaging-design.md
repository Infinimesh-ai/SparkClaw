# Web 端向第三方连接器发送消息设计

> 语言：简体中文 | [English](../../docs/web-outbound-messaging-design.md)

## 1. 状态与目标

本文定义从 WebChat 向已激活第三方消息绑定发送用户自编消息的实现契约。本文是一份设计规范，不表示该功能已经实现。

首个版本必须接受 SparkClaw 统一消息模型中已经存在的全部消息 Part 类型：

- 文本；
- 图片；
- 音频，包括普通音频附件和 `voice_note` 语音消息；
- 文件。

这四类构成首个版本的完整范围。视频和其他二进制格式目前不是一等 `MessagePartKind`，因此按 `file` Part 投递。贴纸、位置、联系人、投票及其他渠道原生对象，需等统一模型正式表示后再纳入范围。

消息从 WebChat、Gateway 到连接器适配器的全过程都必须保持结构化。投递链路不得从 Assistant Markdown 中猜测文件，不得静默丢弃 Part，也不得向浏览器暴露第三方收件人 ID 或凭据。

## 2. 当前基础与缺口

仓库已经具备大部分底层能力，但尚未形成 Web 到第三方连接器的发送链路：

- `app.MessageContent` 和 `app.MessagePart` 已定义 `text`、`image`、`audio`、`file`，以及 `inline`、`attachment`、`voice_note` 三种 disposition。
- `app.DeliveryRequest` 和 `app.DeliveryReceipt` 已定义与渠道无关的投递标识和结果。
- Telegram 客户端已经能够发送文本、图片、文件和语音。
- 微信通知代码已经能够发送文本、图片和文件；当渠道没有原生音频或语音操作时，音频仍可通过文件投递完整保留字节。
- WebChat 已能上传一个 artifact 并附加到 Agent 消息，但不能选择外部目标，也不能直接调用连接器投递。
- `connector.Registration` 目前只暴露面向提醒文本的 `notification.Adapter`；图片和文件发送仍是渠道专用辅助方法。

本功能必须通过单一、与渠道无关的发送契约补齐这些缺口，不能在 Gateway 或 WebChat 中增加渠道分支。

## 3. 产品语义

WebChat Composer 提供两个明确模式：

1. **Agent 对话**：发送到当前 SparkClaw 会话，保留现有流式 Agent 行为。
2. **发送到第三方**：把用户编写的内容直接发送到一个已选择的活动连接器端点，不调用 Agent Runtime。

切换到第三方发送模式后必须选择目标。最终发送动作先打开审核界面，展示目标、渠道、文本、附件、总字节数以及所有已知的渠道降级。用户确认该界面即构成本次直接操作的授权，Gateway 不应针对同一次点击再创建第二条审批记录。由 Agent、定时任务或工具生成的发送仍遵守原有策略和审批边界。

首个版本不支持一次发送到多个目标，也不直接转发已有第三方消息；用户可以重新编写等价内容并选择已有 artifact。

## 4. 统一投递契约

`app` 包继续作为消息与投递类型的唯一事实来源。只有在回执和能力发现确实需要时，才扩展现有投递契约。

```go
type DeliveryCapabilities struct {
    Kinds                 []MessagePartKind         `json:"kinds"`
    Dispositions          []MessagePartDisposition  `json:"dispositions"`
    MaxParts              int                       `json:"max_parts"`
    MaxTotalBytes         int64                     `json:"max_total_bytes"`
    MaxBytesByKind        map[MessagePartKind]int64 `json:"max_bytes_by_kind,omitempty"`
    SupportsCaption       bool                      `json:"supports_caption"`
    SupportsNativeVoice   bool                      `json:"supports_native_voice"`
    SupportsFileFallback  bool                      `json:"supports_file_fallback"`
}

type PartDeliveryReceipt struct {
    PartID         string `json:"part_id"`
    Status         string `json:"status"`
    Representation string `json:"representation"` // native 或 file_fallback
    ProviderRef    string `json:"provider_ref,omitempty"`
    ErrorCode      string `json:"error_code,omitempty"`
}
```

`DeliveryRequest` 保留 `Target`、`Content` 和 `IdempotencyKey`。请求 ID 与创建时间由 Gateway 设置，浏览器传入的这些服务端字段一律忽略。投递回执包含顺序一致的逐 Part 回执，避免一个需要多次渠道调用的请求在丢失部分内容时仍显示成功。

浏览器提交的二进制 Part 只引用 `artifact_id`，不得提交绝对路径、任意 URL 或渠道原生媒体 ID。Gateway 检查所有者、会话、工作区边界、文件类型、实际大小和普通文件状态后，再把 artifact 解析成受控 `ResourceRef`。

## 5. 连接器边界

新增与连接器无关的 outbound 包，最小契约如下：

```go
type Adapter interface {
    Capabilities(context.Context, app.NotificationBinding) app.DeliveryCapabilities
    Deliver(context.Context, app.NotificationBinding, app.DeliveryRequest) (app.DeliveryReceipt, error)
}
```

`connector.Registration` 增加一个可选的 `Outbound Adapter`。Registry 像当前构建 binding、notification 和 runtime router 一样构建 outbound router。Gateway 只依赖该 router，不能按 `telegram`、`weixin`、provider 名称、MIME 扩展名或协议常量分支。

每个适配器必须：

- 校验活动绑定、所有者以及 `message_send_self` scope；
- 只在渠道包内部解析凭据；
- 在第一次渠道调用前预检全部 Part；
- 保持 Part 顺序；
- 使用有界 HTTP client 和请求 context；
- 返回带类型的 blocked/retryable 错误，错误中不得包含 token、原始收件人 ID 或消息正文；
- 为每个输入 Part 返回一条回执。

提醒通知首期可以继续使用当前文本 adapter。后续可把提醒文本包装成 `DeliveryRequest`，但本功能不能让提醒投递依赖 Web API。

## 6. 消息类型映射

所有统一类型都是合法 Web 输入。渠道限制只影响最终表现形式，不影响 Gateway 是否理解该 Part。

| 统一 Part | Telegram | 微信 | 必须行为 |
|---|---|---|---|
| `text/inline` | `sendMessage` | 文本 item | 只按渠道限制拆分，并保持顺序。 |
| `image/attachment` | `sendPhoto`；图片编码或大小不兼容时使用 `sendDocument` | 加密 CDN 图片 item | 渠道支持时保留 caption；文件降级必须在确认前告知。 |
| `audio/attachment` | `sendDocument` | 加密 CDN 文件 item | 保留原始字节、文件名和 content type。 |
| `audio/voice_note` | 兼容原生语音格式时使用 `sendVoice`，否则使用 `sendDocument` | 加密 CDN 文件 item | 无原生语音能力时明确使用 `file_fallback`，不得用转写文本替代。 |
| `file/attachment` | `sendDocument` | 加密 CDN 文件 item | 保留安全显示文件名和全部字节。 |

文本和媒体组合可能需要多次渠道调用。适配器必须按统一 Part 顺序执行并记录部分失败。只有当降级能完整保留字节且审核界面已明确提示时，才允许自动降级。格式转换、重新压缩或用转写替代都需要未来独立的显式转换工作流，不属于直接投递。

## 7. Gateway API

### 7.1 发现端点

`GET /api/delivery-endpoints` 只返回当前所有者名下、状态为 active 且具有 `message_send_self` 的绑定：

```json
{
  "endpoints": [
    {
      "id": "binding:bind_123",
      "binding_id": "bind_123",
      "channel": "telegram",
      "display_name": "Personal bot",
      "capabilities": {
        "kinds": ["text", "image", "audio", "file"],
        "dispositions": ["inline", "attachment", "voice_note"],
        "max_parts": 8,
        "max_total_bytes": 26214400,
        "supports_native_voice": true,
        "supports_file_fallback": true
      }
    }
  ]
}
```

响应不得包含 `external_chat_id`、context token、credential reference、base URL、provider state 或 cursor。

### 7.2 创建投递

`POST /api/deliveries` 接受已明确确认的请求：

```json
{
  "target": "binding:bind_123",
  "idempotency_key": "web-019f...",
  "confirmed": true,
  "content": {
    "parts": [
      {
        "id": "part-text",
        "kind": "text",
        "disposition": "inline",
        "text": "请查看这张图片。"
      },
      {
        "id": "part-image",
        "kind": "image",
        "disposition": "attachment",
        "artifact_id": "obj_123",
        "caption": "最新渲染结果"
      }
    ]
  }
}
```

成功响应为 `201 Created`，返回持久化的 `DeliveryReceipt`。同一所有者使用相同 idempotency key、目标和内容摘要重放时，返回原结果和 `200 OK`；同一个 key 对应不同内容时返回 `409 Conflict`。`confirmed != true` 返回 `400 Bad Request`。

`GET /api/deliveries/{id}` 用于读取可重试或部分完成请求的状态。首个版本在有界 POST 请求内完成发送，不引入无界后台队列。渠道超时必须生成已持久化的 failed/retryable 回执，不能只留下未知的 HTTP 断开。

### 7.3 上传

在浏览器统一入口仍限制为 25 MiB 时，WebChat 可以复用现有受控 artifact 上传。`/api/deliveries` 对二进制内容只接受上传响应中的 `artifact.id`。多个文件逐个上传，再由前端按顺序组装为多个 Part。

预检使用所选端点声明的限制。如果目标的原生媒体限制更低，可以声明并使用完整保留字节的文件降级；否则 WebChat 禁止进入确认步骤，并展示带类型的限制错误。

## 8. 持久化与幂等

持久化投递请求、内容元数据、状态、尝试次数、逐 Part 回执、渠道引用、错误码、时间戳和 SHA-256 内容摘要。投递状态中不得持久化连接器凭据，也不得重复保存 artifact 字节。

任何 Store 接口扩展都必须同时实现 memory、file 和 PostgreSQL 三个后端，加入 file `Snapshot`，并写入核心 migration。默认 file 后端是发布门槛。

进程崩溃或客户端重试不得在成功回执已持久化后再次生成外部消息。如果渠道在接受请求后超时且没有幂等能力，回执保持 `failed` 并使用 `outcome_unknown` 错误码；禁止自动重试，由 UI 请求用户明确重试。有渠道原生幂等键时必须使用。

## 9. WebChat 体验

第三方发送模式提供：

- 从 `/api/delivery-endpoints` 获取的目标菜单；
- 支持图片、音频和任意文件的多 Part 附件托盘；
- 音频 Part 的 disposition 控件（`音频文件` 或 `语音消息`）；
- 每个 Part 的文件名、类型、大小、caption、移除和排序控件；
- 进入审核步骤前的能力与大小校验；
- 审核对话框，并在对应 Part 旁明确标出 `file_fallback`；
- pending、sent、partially sent、failed、retryable 和 outcome unknown 状态；
- 仅当回执确认安全时，才允许只重试失败 Part。

现有麦克风转写仍是 Agent 对话中的草稿能力，不能静默变成语音附件。用户可以上传音频并标记为语音消息；未来录音模式必须显式保留并审核音频后才能调用此投递 API。

切换会话或 Composer 模式时，草稿 Part 不得泄漏到其他会话或目标。绑定一旦撤销或过期，其端点必须立即失效；即使 WebChat 缓存了旧端点也不得发送。

## 10. 安全、策略与审计

- 只有已认证的 owner client 能列出端点或创建投递。
- Gateway 推导 owner 并解析 binding；浏览器不能覆盖收件人、thread、credential、base URL 或 provider。
- Binding scope 必须包含 `message_send_self`；只有 `reminder_send_self` 不足以发送普通消息。
- 已有仅提醒绑定不得被静默升级。Gateway 只有在 WebChat 请求用户明确开启普通消息发送后，才能增加 `message_send_self`；新建绑定在绑定审核中分别请求提醒发送与普通消息发送 scope。
- 每个 artifact 必须属于同一 owner 且位于允许的工作区根目录内。符号链接、文件缺失、目录以及大小或 hash 已变化的文件，在渠道调用前拒绝。
- 直接 Web 确认记录为审批来源；来自 API 的 Agent 或定时发送不能继承该授权。
- 审计事件记录 delivery ID、目标 endpoint ID、类型、disposition、字节数、状态、降级表现和脱敏错误码；不记录消息正文、原始收件人、context token、credential 或渠道响应正文。
- 在每个必需 Part 都有回执前，外部发送不能显示成功。部分成功必须在历史中可见且不可篡改。

## 11. 类型化失败

Gateway 和适配器使用稳定错误码：

| 错误码 | 含义 | HTTP 行为 |
|---|---|---|
| `delivery_binding_unavailable` | Binding 不存在、已过期、已撤销或状态过旧。 | `409` |
| `delivery_scope_denied` | Binding 缺少 owner 发送 scope。 | `403` |
| `delivery_part_unsupported` | 不存在原生投递或完整保留字节的降级。 | `422` |
| `delivery_payload_too_large` | 单个 Part 或总大小超过有效限制。 | `413` |
| `delivery_artifact_invalid` | Artifact 所有权、路径、hash 或文件状态校验失败。 | `422` |
| `delivery_idempotency_conflict` | 同一个 key 被用于不同摘要或目标。 | `409` |
| `delivery_provider_retryable` | 有界渠道失败，确认可安全重试。 | `502` 或 `503` |
| `delivery_outcome_unknown` | 渠道可能已接收，自动重试不安全。 | `502` |

WebChat 可见错误必须可操作且不包含秘密信息。

## 12. 实现顺序

1. 完成 `app` 能力与逐 Part 回执类型，以及验证测试。
2. 增加 outbound adapter/router 契约并注册 Telegram、微信实现，Gateway 中不出现渠道分支。
3. 增加投递记录及三个 Store 后端实现。
4. 增加端点发现和投递 API，完成所有权、scope、artifact、预检、审计和幂等校验。
5. 增加 WebChat 第三方发送模式、多 Part 编排、审核和回执状态。
6. 增加适配器/API/UI 聚焦测试，再执行完整项目验证。

## 13. 验收标准

- WebChat 可以向 Gateway 返回的每个活动端点发送文本、图片、普通音频、语音消息音频和文件 Part。
- Telegram 和微信测试覆盖映射表，包括完整保留字节的降级和 Part 顺序。
- 覆盖不支持、过大、绑定失效、错误 owner、错误 scope、无效 artifact、渠道超时、部分失败和幂等重放路径。
- Gateway 和 WebChat 中不存在 provider 分支。
- 不从 Markdown 推断消息 Part，也不静默丢弃任何 Part。
- Memory、默认 file 和 PostgreSQL Store 通过相同投递契约测试。
- WebChat build 和聚焦 UI 测试在桌面与移动布局通过。
- `go build ./...`、`go vet ./...`、`go test ./...`、WebChat build、doctor 和 mock golden eval 均无新增失败。

## 14. 并行执行

实现过程由 [Web 外发消息 Worktree 计划](web-outbound-messaging-worktree-plan.md) 协调。该计划定义消息收发层、路由与 Workflow 层、最终集成三个用户可见的 Codex 任务和 worktree。在该计划与本文获得确认前，不得在任何 worktree 中开始功能实现。
