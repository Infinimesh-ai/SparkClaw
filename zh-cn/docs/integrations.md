# 外部集成

> 语言： [English](../../docs/integrations.md) | 简体中文

本文档总结当前有效的可选集成边界。环境默认值见
`docker/env/sparkclaw.example.env`，启动命令见[部署](deployment.md)。

## 共同规则

- 所有第三方消息 connector 出厂默认关闭；owner 必须先在 WebChat 中显式开启已注册渠道，
  再开始账号设置。
- credential、readiness 或 capability 检查失败时，集成保持 disabled 或关闭失败。
- secret 从环境变量或文件注入，不出现在公开 Gateway 配置中。
- 外部内容是不可信证据，绝不成为 system instruction。
- outbound call 有明确 host allowlist、deadline、body limit、retry limit 和 audit record。
- messaging provider 进入 Connector/Delivery Registry；data provider 进入 typed adapter contract。

## LocalMind Workspace MCP

LocalMind 是可选、按 workspace 限定的 MCP 集成，出厂默认关闭。通过在 Gateway 配置中加入
固定 `localmind` entry 启用；默认配置保持 `mcp_servers` 为空：

```json
{
  "mcp_servers": {
    "localmind": {
      "transport": "streamable-http",
      "url_env": "LOCALMIND_MCP_URL",
      "bearer_token_env": "LOCALMIND_MCP_TOKEN",
      "namespace": "localmind",
      "expected_server_name": "localmind-workspace",
      "protocol_version": "2025-06-18",
      "allow_mutations": false,
      "allow_private_http": false,
      "tool_allow": [],
      "tool_deny": []
    }
  }
}
```

把 `LOCALMIND_MCP_URL` 设为准确的 `/api/workspaces/<workspace-id>/mcp`
endpoint，并把 `LOCALMIND_MCP_TOKEN` 设为绑定该 workspace 的 credential。建议先使用
LocalMind read-only credential，并保持 `allow_mutations` 为 false。URL/token 会在每次刷新时
从环境重新解析，不进入 Gateway 公开配置，也不能由 owner utterance 指定。`tool_allow` 和
`tool_deny` 只能缩小 credential 可见 catalog。

Gateway 通过共享 MCP 2025-06-18 Streamable HTTP client 初始化，校验
`localmind-workspace`，发现 credential scope，并原子刷新 namespaced `localmind.*`
ToolHub entry。模型最多看到 16 个匹配的 directory entry，且只物化被选中 tool 的完整
schema。read-only operation 无需审批；所有 mutation 都需要 owner approval；destructive 或
open-world operation 还需要 deep verification。远端执行继续受 LocalMind authorization、
DLP 和 audit 约束，绝不表示为运行在 SparkClaw 本地 sandbox 中。

规范结果来自 `structuredContent.result`；MCP `isError` 仍是失败调用。text fallback、
Resource 和归档的大结果都有边界，并作为不可信 evidence 处理。认证或 scope 变化会触发
重新发现；read 在刷新成功后最多重试一次，mutation 绝不自动重放。

应尽量使用公网 HTTPS。从 Gateway container 看，`localhost` 指向该 container，而不是
host。请使用共享 Compose network 上的 LocalMind service name、`host.docker.internal` 或
公网 HTTPS endpoint。明文 HTTP 必须设置 `allow_private_http: true`，且只接受 loopback、
private-network 或 container-service host；redirect 会被拒绝。

## Telegram

Telegram 是可选 private-chat connector，出厂默认关闭。先通过 WebChat 的统一 connector
控制开启渠道，再提供 Bot token 创建独立账号 binding。可同时存在多个 Bot binding。每个
Bot token 在绑定前先验证，再通过 credential vault 独立加密；持久化状态只保存 ciphertext
envelope。

`tools.notifications.channels.telegram.enabled` 和 `SPARKCLAW_TELEGRAM_ENABLED` 仍可作为
尚无 owner 持久化选择时的部署启动默认值；它们不会覆盖之后的 WebChat 选择，已保存的 Bot
binding 也绝不会自行开启 Telegram。

已验证 Bot 初始没有 recipient。第一条 fresh authorized private message 原子 claim user/chat；
历史 update 和 group 不能 claim。每个 binding 独立拥有 cursor、inbox identity、ordering 和
recipient authorization。long polling、global concurrency、pending work、attachment size/count
和 voice duration 都有边界。

Inbound text/media 进入共享 Message Runtime，voice note 委托共享 speech transcriber。
Outbound text/media、定时结果和 approval prompt 使用[消息与定时任务](messaging-and-scheduling.md)
中的同一个 Delivery Gateway。

主要配置使用 `SPARKCLAW_TELEGRAM_*`。Bot API 默认官方 endpoint，polling 有界，并只允许 private chat。

## 微信

微信是通过同一 provider-neutral 接口注册的可选 connector。QR/binding lifecycle、polling/media、
address 和 acknowledgement 留在微信 package 内。Agent Runtime、Timer 和 Delivery Gateway
不按微信名称分支。

微信同样出厂默认关闭，必须先用相同 WebChat 控制开启，再开始 QR 设置。notification channel
block 和对应环境变量只在尚无 owner 持久化选择时作为启动默认值。被撤销或不可用 binding
仍可见，但不能选作 delivery target。

对于 QR provider，WebChat 不再用 owner 的默认浏览器打开链接，而是通过绑定专属的 visible
Chromium profile 打开持久化的 provider 登录 URL。Gateway 只允许当前 owner 仍处于待处理状态的
微信 binding 执行该动作，并且只接受 provider 的 HTTPS `liteapp.weixin.qq.com` URL；client
不能提交 URL。重复点击会复用同一个 binding-scoped 窗口。polling 观察到绑定已激活、过期或失败，
或者 owner 撤销 binding 时，Gateway 会释放对应的受管 browser session 并关闭 Chromium 窗口。
该功能要求可信桌面 runtime 启用 `docker/compose.visible-browser.yaml` overlay；没有 visible
display 时会明确失败，绝不回退到默认浏览器。

## 语音转写

Speech 是 WebChat microphone 和 Telegram voice note 共享的可选 OpenAI-compatible
transcription adapter。WebChat 录制有界 mono 16 kHz PCM16 WAV，并调用：

```text
GET  /api/speech/status
POST /api/speech/transcriptions
```

Gateway 在调用配置的 allowlisted endpoint 前校验 media type、WAV structure、duration、
upload size、request ID、session 和 language。adapter 默认关闭，endpoint 和 allowlist
默认为空；只有显式配置 service URL、allowed host 和 served model 后才能启用。

WebChat transcript 只插入当前 draft，绝不自动发送。转写不创建 chat message、Agent run、
Tool Call、approval 或 artifact。audio byte 不保留，audit 只记录有界 metadata 和 outcome。
queue/concurrency 超限返回明确 busy 或 unavailable 状态。

配置使用 `SPARKCLAW_SPEECH_*`，包括 endpoint、allowlist、model、language、timeout、duration、
upload、concurrency、pending 和 expected runtime version。

## Infinimesh Info

Infinimesh Info 是 `web.search` 和现有 `browser.weather` Workflow 的可选生产 provider。
公开搜索使用 `POST /v1/info/query`；天气只使用结构化 `POST /v1/info/weather`，不再保留
通用 query fallback 或自由文本天气解析。两条路径都保留 request ID，通过原有内存 wallet
获取 one-shot `info.basic` token，以 `PrivateToken` 传递，并限制 retry、deadline 和
response size。

Info query 结果已经在上游完成聚合。SparkClaw 将其持久化为
`info_search_result_v2`，保留 Info 的 summary、fact、conflict、freshness、uncertainty、
source-ID 边、usage metadata 和最终 `sources[]` 顺序，不再对这些单元重排或重新合成。
上游 `recommended_next_actions` 只留在不可信 raw result 中，绝不进入模型 evidence、
Workflow control 或用户回答。

回答 projection 为 `info_aggregate_projection_v4`。它校验 source ID 唯一性与 citation 边，
按 Info 顺序加入完整 fact 和 conflict viewpoint，不包含 snippet，并把容量或无效引用造成的
省略标为 `partial`。Fact 与 viewpoint 保留各自 citation marker；freshness、uncertainty 和
不可链接 citation label 对用户可见。确定性 renderer 完成 `browser.internet_search`，不增加
第二个模型 finalizer。浏览器目标识别独立读取 raw 有序 source 视图，跳过不可链接 entry，
继续执行 HTTPS、DNS/IP 和 redirect 安全门。只读 decoder 支持持久化的 pre-v2 搜索结果；
新的 ToolHub call 只写 v2。

天气 adapter 则校验固定 metric 的 current/hourly/daily 字段和规范化 condition 词表，
随后只暴露 typed 卡片 payload。provider 坐标在进入 ToolHub output、trace 或卡片渲染前
被丢弃；malformed 或不完整天气响应会明确失败。

配置使用 `SPARKCLAW_INFINIMESH_INFO_LICENSE_ID` 与
`SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY`（或
`SPARKCLAW_INFINIMESH_INFO_LICENSE_KEY_FILE`）。Token 签发通过
`Authorization: Bearer <ilk_v1...>` 认证；已退役的 entitlement proof、device attestation
和 license proof 环境变量不再接受。key 内嵌的 license ID 必须与配置一致，且 key 绝不能进入
public config、log、trace 或 artifact。

## ISCP Bridge

可选 ISCP Bridge 是当前位于 JingSi App 与 loopback Gateway 之间的旧入站进程。它使用 ISCP v0.1.0
Core SDK 处理设备身份、Trust Grant、Session Hello/Ready、proof-of-possession 和
SecureEnvelope。Bridge 把加密的 `agent.*.v1` 请求映射到一个 loopback Gateway 端点；
session、run、policy、approval、event、被动通知收件箱和 audit 仍由 Gateway 统一负责。
Gateway 启用 auth 时该端点使用 bearer 认证；关闭 auth 时明确支持无 token 的 loopback
dispatch。

Bridge 不接收 ITES token，也不暴露无认证的局域网 listener。生产设备身份密钥保存在操作系统
keyring，Relay credential 独立轮换，Gateway 不支持的能力不会进入 manifest。注册、版本化
`agent.notification.deliver.v1` 可在不启动 Agent Runtime 的情况下保存 LocalMind 文档/评论
提及；原有 conversation 投递只在目标切换前作为临时 legacy fallback。Gateway 通过 list/read
API 和全局、带认证 SSE stream 暴露 owner-scoped 收件箱。注册、版本化 schema、当前
LocalMind DeviceProof/Trust Grant 续期限制、App CI mock 和 GB10 运维见
[ISCP Bridge](iscp-bridge.md)。

目标设计不保留 LocalMind 的 external-controller enrollment 方向。SparkClaw 在本地展示由 ISCP
pairing 能力生成的一次性 Pairing Ticket；LocalMind Access Gateway 连接 ISCP，通过标准
Provisioning 兑换该凭证并加入 SparkClaw ISCP Domain。ticket 与 protocol admission、Trust
Grant、Relay credential、secure session、rotation 和 transport revocation 由 ISCP 负责；
该认证通道 ready 后，SparkClaw 独立签发单次使用 MCP Access Ticket；LocalMind 通过 ISCP 兑换
它，以激活本地 owner 批准的 conversation-scoped MCP Binding。普通 MCP call 不复用任一 ticket。LocalMind
新入网并通过通用 ISCP MCP gateway 验证后，删除其 Bridge manifest entry、grant、dispatch branch、
passive/conversation fallback、config 和 test。JingSi 仍需要的共享 Bridge component 冻结保留；
JingSi 不接入 MCP，后续另行设计绑定方式。上文 LocalMind Workspace MCP 属于相反的出站方向，
继续保留。

SparkClaw 自身负责的入站 MCP 阶段已在默认关闭的通用 `mcp` connector 后实现：严格 MCP
`2025-06-18`、结合认证 ISCP identity 兑换只存 hash 的单次 MCP Access Ticket、持久 Binding 与
operation 恢复、唯一业务工具 `sparkclaw.conversation.send`、普通消息路由、有界 workspace 文件名
解析、共享 Delivery，以及加密 Bridge 请求/响应 dispatch。
这还不是可供 LocalMind 使用的生产连接：完成标准 ISCP PairingTicket/Provisioning 集成、可部署
external Access Gateway 和真实 Relay 验证后，才能切换并删除旧链路。

## MCP 与 Happy

Gateway 可以从配置的无状态 Streamable HTTP MCP server 发现并执行工具。每个 server
使用 MCP `2025-06-18` 独立初始化；发现与调用每次 POST 只发送一条 JSON-RPC 消息，并带上
`Accept: application/json, text/event-stream`。客户端同时兼容纯 JSON 响应和用 SSE
`data:` 帧包装的单条响应，保留服务端返回的 `Mcp-Session-Id`，并限制 deadline 与响应大小。

Happy 使用两个彼此独立的端点。Happy Team 提供 Cloud Agent 任务工具；个人 bridge 只有在
成员机器开机且 `happy-agent mcp` 正在运行时才可达：

```json
"mcp_servers": {
  "happy-tasks": {
    "url": "https://happy.example.com/v1/team/mcp",
    "token_env": "HAPPY_TEAM_MCP_TOKEN",
    "expected_server_name": "happy-team-tasks"
  },
  "happy-bridge": {
    "url": "http://127.0.0.1:8790/",
    "token_file": "~/.happy/mcp.token",
    "expected_server_name": "happy-bridge"
  }
}
```

发现结果以 `mcp.<server-name>.<remote-tool-name>` 原子注册，并保留 input/output schema
与 MCP annotation。读取工具可以进入 `coding.agent_manage` Workflow；远端修改会停在正常
审批边界。`approve_plan` 与 `reject_plan` 使用独立 capability，绝不会暴露给聊天模型选工具。

两个端点独立降级。个人 bridge 离线不会移除或拖垮 Happy Team 任务端点。Team 端点返回
401 时会要求 owner 重新签发个人 MCP token；bridge 返回 401 时会要求核对本地 token
文件。`GET /api/mcp-servers` 返回当前状态，
`POST /api/mcp-servers/{name}/refresh` 可以单独重新发现一个 server。

任务详情、状态转移历史、计划、session metadata 和 transcript 都是不可信观察。它们通过
正常 observation 路径归档和摘要，绝不会成为下一次工具调用的指令或授权。
`wait_for_idle` 的调用 deadline 会长于请求的等待时间；调用方也可以改用有界
`get_session` 轮询。

配置 `happy-tasks` 后，一个有界 worker 每 60 秒轮询
`list_tasks {"status":"WAITING_APPROVAL"}`，并按 Happy task ID 创建 typed approval。新项目会
调用 `get_task_plan`；成员机器离线期间会持续重试。收件箱展示任务标题、目标和计划；计划不可用
时不能批准或编辑。owner 只能修改计划文本。批准固定调用
`approve_plan {taskId, editedPlan?}`，拒绝固定调用 `reject_plan {taskId}`。Gateway 只有在
远端动作成功后才更新本地状态。business error 会触发权威 `get_task` 复核；任务已不在
`WAITING_APPROVAL` 时按“已在其他位置处理”关闭。后续 waiting list 中消失的项目也执行相同复核。

### MCP 调优键

通用 `mcp_servers.<name>` 条目接受 `request_timeout_seconds`（默认 30，上限
3600）、`discovery_refresh_seconds`（默认 60，上限 86400）与
`response_body_max_bytes`（默认 4 MiB，上限 32 MiB）。专用 `localmind` 条目
则接受 `request_timeout_seconds`（默认 30）、`long_call_grace_seconds`（默认
10）、`refresh_interval_seconds`（默认 300）、`max_response_bytes`、
`state_output_max_bytes`（默认 16 KiB）与 `archive_output_max_bytes`（默认
16 MiB）。两组键有意互斥；用错组的键会在配置加载时被拒绝。

入站 MCP 访问通过 `mcp_access.local_domain_id`（默认 `sparkclaw-local`）
做域隔离：签发的访问票据绑定该域，来自其他 ISCP 域的对端兑换会被拒绝。
主机加入多域 ISCP 拓扑前应先改好该值再签发票据。

## Connector 控制、Binding 与状态 API

WebChat 通过统一、版本化 API 发现已注册渠道并管理显式 opt-in；账号设置保持独立 lifecycle：

```text
GET    /api/connectors
PATCH  /api/connectors/{channel}
GET    /api/notification-bindings
POST   /api/notification-bindings/{channel}/start
GET    /api/notification-bindings/{id}
POST   /api/notification-bindings/{id}/browser
DELETE /api/notification-bindings/{id}
GET    /api/delivery-endpoints
```

PATCH body 包含 `enabled` 和最后观察到的 `expected_version`。关闭渠道会取消 inbound runtime，
并阻止 outbound Provider 和 Endpoint Registry 项；加密 credential 与 binding 会保留，owner
重新开启时不必重复设置。现有 binding 绝不表示已 opt-in；持久化的开启选择会在 Gateway
重启时恢复。

UI 展示 Endpoint Registry 提供的软件、账号、接收人、会话、capability 和 status，不从
channel name 推断 destination，也不暴露 native recipient ID。

## 验证

集成改动需要覆盖 disabled/unavailable、secret redaction、host/timeout enforcement、Store
backend parity、binding lifecycle、authorization isolation、payload limit、retry、connector
shutdown 和端到端 Message Runtime/Delivery Gateway。credential-gated live check 只能补充，
不能替代确定性本地测试。
