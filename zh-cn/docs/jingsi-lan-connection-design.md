# JingSi 局域网 Web 客户端互联设计

> 语言：[English](../../docs/jingsi-lan-connection-design.md) | 简体中文

| 字段 | 值 |
|---|---|
| 状态 | SparkClaw 侧已实现并通过测试；JingSi 实现与实体 LAN 验证待完成 |
| 决策日期 | 2026-08-14 |
| SparkClaw 基线 | `76a72aa` |
| JingSi Android 基线 | `ZZZZJJJ0928/JingSi-Windows` 的 `1708fd9` |
| 首期结果 | 一个服务端绑定的文本对话与空闲实时更新 |
| LAN 呈现端口 | 实验性 `18793`，默认关闭且不发布 |
| 实时传输 | 基于现有 session event log 的 SSE 与 cursor 补拉 |
| 鉴权 | 延后 |
| ISCP | 不在范围内 |

## 决策

JingSi 是 SparkClaw WebChat 的移动呈现客户端，不是 Telegram、微信一类第三方
connector。首期只证明可信局域网内一个文本对话可以双向互通。JingSi 空闲时也必须收到
configured conversation 中的新消息；接收不能依赖 JingSi 先发送消息。

JingSi 当前没有会话管理页面，因此不 list、create、select、rename、delete session，也不接收
session ID。服务端把 LAN presentation surface 绑定到一个现有 visible WebChat session。手机只
看到文本发送 operation 和新消息 event。

同时不提供 message-history endpoint。首次连接时 JingSi 从当前 event head 开始，不接收更早的
transcript。已保存的 event cursor 只用于补回上一次连接之后到达的消息。

专用 host 端口 `18793` 是现有 Gateway 前的精确 allowlist。它不是第二个 application service、
message ingress、delivery provider 或 message store。消息与结果继续走 SparkClaw 已有的
WebChat 链路。

```text
WebChat browser -> :18790 -> 完整 WebChat surface ------------+
                                                              |
JingSi -> :18793 -> 固定 session presentation allowlist ------+
                                                              v
                                                   Gateway :18789
                                                              |
  server-configured WebChat session ---------------------------+
  -> webMessageIngress
  -> MessageEnvelope
  -> semantic routing + Workflow Runtime
  -> WorkflowResult
  -> DeliveryRequest
  -> Delivery Gateway
  -> LocalWebDelivery
  -> 现有 app.Message + 现有 session message.created event
  -> WebChat 与 JingSi presentation client
```

port、route name 和 projection 仍属于实验性接口。通过 LAN 验证不代表它们成为稳定 public
SparkClaw API。

## 当前局域网基线

默认 Compose topology 在 host `18790` 发布 WebChat。WebChat 的 Nginx listener 提供 SPA，
并将宽泛的 `/api/` surface 代理到 Docker 内部网络的 Gateway `18789`；Gateway 不直接发布。
已实现的可选 overlay 只有在 operator 提供一个 RFC1918 host address 和一个现有 visible WebChat
session 后，才向同一个 Nginx container 增加 `18793`。base stack 不发布该端口。

现有 WebChat API 包含 session 管理、message history、message send 和 session event：

```text
GET    /api/sessions
POST   /api/sessions
GET    /api/sessions/{id}
GET    /api/sessions/{id}/messages
POST   /api/sessions/{id}/messages/stream
GET    /api/sessions/{id}/events
GET    /api/sessions/{id}/events/stream
```

JingSi 不得连接这一完整 surface。其 Android cleartext policy 当前也只允许 loopback 和 emulator
address，所以实体手机还不能使用 SparkClaw 的 RFC1918 HTTP 地址。

SparkClaw 已有首期所需的内部 primitive：

- `webMessageIngress` 构造 Web source endpoint 和 `ReturnToSource` route；
- Agent Runtime 与 `PersistentWebDelivery` 持久化或幂等复用正常 `app.Message` result；
- `Store.AddMessage` 追加 session-scoped `message.created` event，其中携带已持久化 message；
- `/api/sessions/{id}/events/stream` 可以从 cursor replay，再继续 SSE。

这些 primitive 全部复用。raw session event API 不对手机开放，因为它接受 session ID、返回内部
event shape、没有结果边界，而且 memory 与 PostgreSQL 对 unknown cursor 的行为不一致。

`18791` 已用于 browser evaluation fixture，`18792` 已用于 ISCP Bridge，因此 `18793` 是第一个
不冲突的临时端口。

## 已实现的 SparkClaw 表面

### 服务端绑定

启用 presentation listener 时，必须由 typed server configuration 指定一个现有 WebChat
session。被选 session 必须 visible 且属于 `DefaultOwnerID`。Gateway 在 send 和 event
projection 前解析并重新校验它。

JingSi 不接收、不保存、不提交、不创建也不选择 session ID。不存在自动创建 session，也不
fallback 到 latest session。若 configured session 缺失、hidden 或 owner 发生变化，`18793`
保持 unavailable，直到 operator 修正配置。

### 精确 Route Allowlist

所有 JingSi route 都位于单一 `/api/jingsi/` 前缀之下，以 `/api/jingsi/v0/` 版本化。
因此 `18790` 上的宽 WebChat proxy 只需一条规则屏蔽整个前缀，而不必跟踪各个
path；Gateway 也能在不查路由清单的情况下守护同一前缀。

临时 `18793` listener 只暴露：

| Method 与 path | 用途 |
|---|---|
| `GET /api/jingsi/v0/readyz` | 确认所选 SparkClaw process 和 binding ready |
| `POST /api/jingsi/v0/messages/stream` | 通过 Web ingress 向服务端绑定 session 发送纯文本 |
| `GET /api/jingsi/v0/client-events/head` | 首次连接时获取当前 visible event cursor |
| `GET /api/jingsi/v0/client-events?after={cursor}&limit=100` | 补回 saved cursor 之后的新 message event |
| `GET /api/jingsi/v0/client-events/stream?after={cursor}` | 从 cursor replay，随后在空闲时接收新消息 |

刻意不提供 session route 和 message-history route。具体来说，`/api/sessions`、
`/api/sessions/*`、`/api/messages`、`/mcp`、SPA、configuration、connector、tool、trace、
schedule、file、approval 和 delivery administration 均返回 `404`；allowlisted path 上不支持
的 method 返回 `405`。listener 没有 catch-all `/api/` proxy。

已实现的 presentation listener 通过内部网络将准确的 method/path 映射到 Gateway-owned handler，不能
把 caller 提交的 session ID 插入 upstream path。

### Gateway 层隔离

Nginx allowlist 是打包方式，不是安全边界。Gateway 在每条 `/api/jingsi/` route 上
自行实施同样的隔离：surface 关闭时每条 route 都保持不可区分的 `404`；启用后，直接
TCP peer 不是 loopback 或私有地址的请求返回 `403`，浏览器 `Origin` header 也必须
指向 loopback 或私有地址 origin。因此即使 Gateway 端口被误配置或直接暴露，也仍会
拒绝公网调用方；公网网页也无法借助位于 LAN 内的浏览器读取 fixed-session feed。

### 文本发送

JingSi 发送：

```http
POST /api/jingsi/v0/messages/stream
Content-Type: application/json

{"content":"Reply with exactly: SparkClaw LAN connected"}
```

`Content-Type` 必须为 `application/json`。body 只接受一个非空且有长度上限的 `content`
string。在创建 message 或 run 前拒绝 attachment、
caller-supplied session/owner ID、target endpoint、schedule action、unknown field 和 oversized
text。body validation 属于 Gateway handler；Nginx path allowlist 无法校验 JSON field。

handler 解析 configured session，并调用与 WebChat 相同的 `webMessageIngress` 和 streamed
runtime path。它不直接调用 Agent，也不新增 JingSi-specific ingress 或 callback。

response 是一条很小的 SSE stream。`message.stream.started` 表示 Gateway 已接管 detached run；
`message.stream.final` 报告完成状态和 final message ID；`error` 报告 execution failure。本接口
刻意不转发 model token delta。权威 conversation row 始终来自独立 client-event stream 中持久化
的 `message.created` projection。

首期没有 idempotency key。若 connection 在 client 观察到 `message.stream.started` 前断开，
outcome 属于 unknown：保留 draft，但不得自动 retry，因为 acceptance frame 可能在 server 已启动
run 后丢失。`201` 之前明确返回的 validation error 修正后可以安全 retry。

### 实时消息投影

Gateway 读取 configured session 的现有 Store event log，不新增 owner-wide journal、mobile
mailbox 或重复 message table。需要给 Store 增加有界 query，使 memory、file 和 PostgreSQL
具有一致的 cursor validation 与 pagination，同时继续使用 `Store.AddMessage` 已写入的现有
event。

`18793` 只公开 `message.created`，每条 event 投影为：

```json
{
  "cursor": "evt_01...",
  "type": "message.created",
  "message": {
    "id": "m_01...",
    "role": "assistant",
    "text": "SparkClaw LAN connected",
    "created_at": "2026-08-14T08:00:00Z"
  }
}
```

response 不含 session ID、owner ID、run ID、attachment、resource metadata、tool argument、
audit payload、workspace path 或 credential。首期只呈现文本。对于不支持的 non-text content，
可以返回一个有界 unavailable-content marker，使 cursor 仍能推进。

event cursor 是 configured session 现有 event log 中的不透明位置，不是 timestamp 或 message
history offset。presentation query 必须拒绝 malformed cursor，以及不属于 configured session
的 cursor。它不返回 cursor 本身及以前的 event，每 page 最多返回 100 条 projected event。若
session 还没有 message event，`head` 返回 opaque、session-bound 的 empty cursor，而非全局
genesis value；因此 server binding 改变后，即使原会话为空，旧 cursor 也会失效。

持久化 `app.Message` 与它的现有 `message.created` event 必须继续表现为一次可观察 mutation。
memory/file 在当前 critical-section/snapshot boundary 内同时保留两者；PostgreSQL 实现必须在
同一 transaction 中写 message 和 event。这会修复当前 durability gap，但不会创建第二套消息
系统。

### 首次连接与重连

首次连接刻意不恢复 transcript：

1. JingSi 读取 `/api/jingsi/v0/client-events/head` 并保存 cursor `C0`。
2. JingSi 从 `C0` 打开 SSE。
3. 只显示 `C0` 之后创建的消息。

重连属于 queue-like recovery，不是 history browsing：

1. JingSi 从 saved cursor 之后读取有界 event page。
2. 按 message ID 幂等应用每条 event 包含的 message projection。
3. 所有 projected event 应用成功后，持久化 page 的 `next_cursor`。该 cursor 也可能跨过由
   server 过滤的 message role，因此只保存最后一个 visible event cursor 是错误的。
4. catch-up 为空后，从最后已应用 cursor 打开 SSE。
5. SSE 先 replay race window 内的 event，再等待新 event。

每个 client 保存自己的 cursor。读取 event 不会在 server 端 acknowledge 或删除它，一个 client
不能消费另一个 client 的 update。

若 cursor unknown 或属于其他 session，server 返回 `cursor_reset_required`。JingSi 保留当前
local display，移动到 current head，并提示缺失区间无法重建；不 fallback 到 history endpoint。

## JingSi 实施说明

SparkClaw 实现没有修改任何 JingSi 源码。Android 侧应在现有 Happy/ISCP transport 旁增加一个
direct-LAN transport；不得改造 `HappyWireClient` 来承载此协议，不得请求 `sessions.list`，也
不得为此 profile 显示现有 Happy session chip。

### 当前 Android 工程中的接入点

- 新增 `SparkClawLanClient`，使用项目已有的 JDK/Android HTTP 能力和严格 `JsonCompat` decode，
  不需要引入 Gradle dependency。
- 新增管理 lifecycle 的 `SparkClawLanManager`，通过 callback 报告 connection state、
  accepted/unknown send state 与 projected message。它拥有 catch-up worker、一个长期 SSE
  worker、cancellation 和 capped reconnect scheduling。
- `MainActivity` 在 conversation UI active 时启动同步，在 lifecycle exit 时停止，并通过该
  manager 发送。LAN mode 不得调用 `HappyManager.refreshSessions` 或 `selectSession`。
- `AppState.Message` 已包含 `id`、`user` 和 `text`；直接用 server message ID 去重。LAN mode
  只保留一个固定 message list，profile 首次 active 时移除 sample row。
- `JingSiView.drawChat` 呈现该单一 list 和 connection state，省略 Happy session selector 及
  history/refresh control。screen 永远不显示或保存 SparkClaw session ID。
- 修改 `MainActivity.sendChatInput`：不能仅因按下 send button 就清空 draft；收到
  `message.stream.started` 后再清空，validation failure 或 unknown outcome 时保留。

不得插入 optimistic chat row。send surface 没有返回 persisted user message 的 client
correlation ID，用 content matching 对账会产生歧义。UI 应显示独立 sending indicator；user row
和 assistant row 都只在权威 `message.created` event 到达后呈现。

### Profile 与 URL 校验

例如用独立 `SharedPreferences` 只持久化 normalized base URL，以及按该 URL 绑定的一个 cursor。
profile 不含 session ID、owner ID、bearer token、ISCP material 或 cached server transcript。
base URL 改变时清除旧 cursor 和 local row。

首期 validator 只接受 `http://A.B.C.D:18793`；decimal IPv4 literal 必须属于 loopback、
`10.0.0.0/8`、`172.16.0.0/12` 或 `192.168.0.0/16`。任何 request 之前先拒绝 hostname、IPv6、
user info、fragment、query string、non-root path、缺失或其他 port、含糊 octet 和 public
address。可接受并移除一个末尾 `/`。

Android network-security XML 无法表达 RFC1918 CIDR range 或 runtime IP selection。这个仅用于
development 的首期必须在 `res/xml/network_security_config.xml` 的 base level 允许 cleartext，
并以严格 literal-IP validator 作为 application boundary。后续鉴权生产设计不得沿用该 policy。

### Wire Model

只 decode 下列 shape，并拒绝字段缺失、unknown field 或错误 type：

```text
Ready       { ok: true, event_version: "v0", session_ready: true }
Head        { version: "v0", cursor: string }
Catch-up    { version: "v0", events: ClientEvent[],
              next_cursor: string, has_more: boolean }
ClientEvent { cursor: string, type: "message.created", message: Message }
Message     { id: string, role: "user" | "assistant", text: string,
              created_at: RFC3339 timestamp }
```

SSE 只接受 `event: message.created`、非空 `id` 和一个 `data` object，并要求 `data.cursor` 与
`id` 完全相同。comment heartbeat 直接忽略。ordinary JSON body 和单个 SSE frame 都设置 byte
上限；malformed 或 oversized input 必须关闭并重连，不呈现 partial data。

send connection 只识别 `message.stream.started`、`message.stream.final`、`error` 与 comment
heartbeat。收到 `201` 但没有 started event 不算 accepted。client 只发送包含唯一 `content`
field 的 UTF-8 JSON，绝不发送 session、history、attachment、target 或 schedule field。

### 同步状态机

receive state 使用 `DISCONNECTED -> PROBING -> CATCHING_UP -> STREAMING`；send 是独立
operation，绝不能作为 receive 的前置条件。

1. 校验 saved profile，调用 `GET /api/jingsi/v0/readyz`，要求 event version 为 `v0` 且
   `session_ready=true`。
2. 若没有 cursor，调用 `GET /api/jingsi/v0/client-events/head`，持久化该 cursor，保持 local list
   为空，然后直接进入 streaming。
3. 若已有 cursor，从其后分页 catch up。按 message ID 应用每条 event，随后原子持久化
   `next_cursor`；`has_more=true` 时继续。
4. 打开 `/api/jingsi/v0/client-events/stream?after={cursor}`。每条合法 event 必须先应用并持久化，再读
   下一 frame。stream 会先 replay catch-up 到 SSE 之间 race window 的 event，再等待 idle
   update。
5. 遇到 EOF、heartbeat timeout 或 network change 时，从最后 persisted cursor 重新 catch up，
   再以带 jitter 且最多 15 秒的 exponential backoff 重连。每个 profile 最多保留一个 catch-up
   worker 与一个 SSE worker。
6. 收到 HTTP `409` 且 `code=cursor_reset_required` 时，保留当前 displayed row，获取 current
   head 并持久化，同时显示 non-blocking data-gap state；绝不调用 session 或 history endpoint。

connect timeout 应有界（例如 10 秒），ordinary read timeout 为 30 秒，SSE idle timeout 要大于
server 的 15 秒 heartbeat（例如 45 秒），并设置明确 response/frame byte limit。只有 Android
process 与 connection active 时才能保持 SSE；首期保证该状态下实时并在恢复后 catch up，
suspended 或 force-stopped delivery 继续延后。

## 局域网发布

base Compose product 不发布 `18793`。专用 JingSi-LAN override 将其显式绑定到一个指定 RFC1918
host address，绝不能默认绑定 `0.0.0.0`。手机不打开 listener，WebChat 保持在 `18790`，Gateway
`18789` 保持 Docker-internal。

端口分离和 private-address validation 只能减少意外暴露，不等于鉴权。在 authentication/TLS
完成设计前，该模式只用于可信 LAN development，并在 UI 中明确标识。

### Operator 操作步骤

在 SparkClaw host 上列出现有 WebChat session，选择一个 visible 且 `source=webchat` 的 ID。
这是 operator-only 步骤，该值不会发送给 JingSi：

```bash
curl -fsS http://127.0.0.1:18790/api/sessions | jq -r \
  '.sessions[] | select(.hidden != true and .source == "webchat") | [.id, .title] | @tsv'
```

选择一个实际分配给 SparkClaw host 的 RFC1918 address，再启动可选 overlay：

```bash
ip -4 -o addr show scope global
export SPARKCLAW_JINGSI_LAN_BIND=192.168.1.20
export SPARKCLAW_JINGSI_SESSION_ID=sess_replace_with_selected_id
bash scripts/restart_jingsi_lan_compose.sh
```

script 会拒绝 wildcard、public、hostname 和 malformed bind；使用
`docker/compose.jingsi-lan.yaml` rebuild Gateway/WebChat；并等待
`http://$SPARKCLAW_JINGSI_LAN_BIND:18793/api/jingsi/v0/readyz`。配置手机前确认 allowlist：

```bash
curl -fsS http://192.168.1.20:18793/api/jingsi/v0/readyz
curl -i http://192.168.1.20:18793/api/sessions  # 必须为 404
```

## 失败语义

| 失败 | 必需行为 |
|---|---|
| host/port 错误、binding 失败或 event version 不兼容 | 保持 disconnected，并保留 previous profile |
| SSE 断开 | 从 persisted cursor 补拉，再以 capped backoff 重连 |
| cursor 或 message ID 重复 | 幂等应用，不产生重复 row |
| cursor unknown 或属于错误 session | 返回 `cursor_reset_required`；保留 local display，移动到 current head，并提示不可恢复的 gap |
| event 应用失败 | 不推进 cursor，安全 retry |
| send validation 在 `201` 前失败 | 保留 draft；修正 request 后 retry |
| send connection 在观察到 `message.stream.started` 前断开 | 保留 draft，标记 outcome unknown，绝不自动 retry |
| send 在 `message.stream.started` 后断开 | 不 replay；detached execution 继续，由 event sync 恢复 persisted message |
| Gateway 或 Android 重启 | 从 saved cursor 和现有 durable session event 恢复 |
| configured session 失效 | `18793` 保持 unavailable，直到配置修正 |

普通 log 不得包含 message text、event payload 或 LAN address。有界 event type、cursor、message
ID 和 run ID diagnostics 已足够。

## 实现切片

1. **SparkClaw 已完成：** 为现有 visible WebChat session 与默认关闭的 `18793` presentation
   增加 typed configuration。
2. **SparkClaw 已完成：** memory、file、PostgreSQL 的有界 session event read，以及 PostgreSQL
   message/event 原子写入。
3. **SparkClaw 已完成：** Gateway head、catch-up、SSE 与 content-only send handler，全部复用
   现有 Web ingress/runtime/delivery 链路。
4. **SparkClaw 已完成：** 精确 Nginx allowlist、private-address Compose overlay、startup
   validation 与 deterministic test。
5. **JingSi 待完成：** 上述 profile、strict HTTP/SSE client、state machine、单 view
   reconciliation、lifecycle wiring 与临时 cleartext policy。本仓库不修改 JingSi source。
6. **集成证据待完成：** build/install 单独修改后的 Android app，并执行下方实体 LAN 验证。
   鉴权、ISCP、session 管理、history、attachment 与稳定 public API 仍不在范围内。

## 验收标准

### SparkClaw 证据

- JingSi text send 在 configured WebChat session 中创建普通 user/assistant `app.Message`，并经过
  当前 Web ingress、Workflow Runtime、Delivery Gateway 和 `LocalWebDelivery` 链路。
- 不创建 `jingsi` connector、provider、binding、external chat、receive record、mailbox、新
  message table 或 owner-wide client journal。
- JingSi 空闲且没有先发送时，configured session 中创建的消息仍出现在 `18793`。
- 其他 session 的消息以及所有 non-message internal event 均不进入 presentation feed。
- 首次连接从 current head 开始，不返回更早消息；重连只按顺序返回 saved cursor 之后的 message
  event，display row 不重复。
- catch-up 到 SSE 没有 race gap；pagination、heartbeat、disconnect、slow client、malformed
  cursor 和 wrong-session cursor 均有 focused test。
- memory、file restart 与 PostgreSQL integration 证明 message event ordering、filtering、cursor
  validation 和 atomic persistence 等价。
- `18793` 上 allowlist 之外的所有 route/method 都不可用。negative test 明确覆盖 `/`、
  `/api/sessions`、session message/history route、`/api/config`、`/api/connectors`、
  `/api/schedules`、`/api/deliveries` 和 `/mcp`；`18790` 的 WebChat 行为不变。
- send route 在创建 message/run 前拒绝 attachment、target endpoint、schedule action、
  caller-supplied session/owner ID、unknown field、empty content 和 oversized text。

### 实体 Android 验证

1. 启动 default file-backed SparkClaw stack，在 server configuration 中选择一个现有 visible
   WebChat session，并把 `18793` 绑定到一个 private LAN interface。Gateway 保持 internal，
   WebChat 保持在 `18790`。
2. 在实体 Android device 配置 `http://<sparkclaw-private-ip>:18793`，确认它没有请求 session
   或 message-history route。
3. JingSi 保持打开且空闲，在 configured WebChat session 中创建一条文本消息；确认 JingSi
   显示该消息，且没有由 JingSi 发起一个产生该消息的请求。
4. 从 JingSi 发送 `Reply with exactly: SparkClaw LAN connected`，确认 WebChat 与 JingSi 都显示
   同一条 persisted user message 和准确 assistant reply。
5. 关闭手机 Wi-Fi，在 configured session 中通过 WebChat 或现有 SparkClaw Web delivery 再创建
   两条文本消息；恢复 Wi-Fi 后确认按顺序补回且没有重复。
6. 重启 Gateway 和 JingSi，在 configured session 中再创建一条消息，确认 cursor 持久性与重连。

只有 send、idle receive 和 reconnect catch-up 全部通过才能确认互通。只有 JingSi request 和
它自己的 response 不足以作为互通证据。

## 延后决策

验证成功后，可通过独立设计决定：

- authentication、client identity、enrollment、revocation 和 TLS；
- 是否把临时 surface 变为受支持的 mobile API；
- 是否继续分离 `18793` 以及 route versioning；
- 若 JingSi 以后增加会话管理页面，如何提供 session discovery、create、select 与 message
  history；
- Android foreground 或 OS push delivery；
- attachment、approval、schedule、activity 和 richer projection；
- discovery 与 onboarding；
- JingSi 旧 Bridge/ISCP path 的退役。

这些决策都不能引入第二套 Agent ingress、result path 或 message store。共享 Web message 和
delivery 链路始终是不变量。
