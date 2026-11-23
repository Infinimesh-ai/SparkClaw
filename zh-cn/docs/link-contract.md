# 连接契约 `sparkclaw.link.v1`

> 语言：[English](../../docs/link-contract.md) | 简体中文

日期：2026-11-19。状态：已实现。本文是[客户端连接层设计](client-link-layer-design.md)第 3.1 节所述、与传输无关的契约的规范。

- Go 定义：`services/link/contract`。
- 一致性向量：`services/link/contract/testdata/vectors.json`。各语言的客户端实现（Go、Java/Kotlin、Swift）都要用它自检。

## 1. 帧

每条消息是一个 JSON 对象，即一帧。

| 字段 | 适用类型 | 含义 |
|---|---|---|
| `type` | 全部 | `hello`、`request`、`response`、`event` 或 `cancel` |
| `id` | 全部 | 长 1–128 字节，不含 NUL 或换行。响应沿用其请求的 id |
| `hello` | hello | 见第 2 节 |
| `op`、`params` | request | 操作名（不超过 96 字节）及其参数 |
| `ok`、`result`、`error` | response | `ok` 必填；失败时带 `error` |
| `topic`、`subscription`、`data` | event | `topic` 必填 |

- **大小上限**：编码后的帧不超过 600 KiB。大对象走分块的对象操作，每块原始数据不超过 256 KiB。
- **无效帧**：`type` 未知，或缺少必填字段的帧无效。
- **未知操作**：请求的 `op` 未知或对本设备不可用时，返回 `unsupported_capability`，**连接保持**。
- **向前兼容**：`params` 中的未知字段会被忽略，所以新版客户端连旧版后端仍能工作。
- **id 前缀**：后端发出的请求（反向调用）id 以 `s` 开头，客户端不得使用该前缀。

## 2. Hello

客户端先发 `hello`，包含：

- `versions`：客户端支持的版本；
- `profile`：`duplex` 或 `pull`；
- `device`：设备条件，格式为 `{"class","name","app","conditions":{"browser_surface","attachments","microphone","screen"}}`；
- `reverse`：设备提供的反向操作。

后端回一个 `hello` 帧，包含：

- `version`；
- `profile`；
- `features`：本设备可用的操作，按设计文档 3.9 的规则由设备条件和访问方式算出；
- `principal`：`owner_id`、`device_id`、`class`、`name`；
- `server_time`；
- `reverse`：后端接受的反向操作；
- `lan`：后端当前的局域网连接器，格式为 `{"endpoints":[...],"tls_pin"}`；后端没有局域网连接器时不带。两种传输下内容相同。设备把它作为局域网候选保存，因此经 Relay 连上后也能找到局域网。

应答与传输无关：同一台设备经局域网和经 ISCP Relay 得到相同的 `features`、`principal` 和 `reverse`。

设备类别以官网的设备记录为准：

- hello 中的类别与记录不符时，后端拒绝。
- 设备类别还限定了设备能声明的条件：只有电脑端保留内置浏览器界面，手表没有附件传输。
- 重复发送 hello，得到相同的应答。

## 3. 访问方式

| 方式 | 连接 | 实时输出 |
|---|---|---|
| `duplex` | 长连接，可推送事件和反向调用 | `run.subscribe` |
| `pull` | 只有请求和响应，不订阅，不推送 | 轮询 `run.status` |

事件只用于加速。事件能送达的内容，都能通过 `sync.changes`、`session.messages` 和 `run.status` 读到。

目前的客户端在 pull 方式下，应用处于前台时仍会保持一条连接。后端在这条连接上不推送事件，也不接受订阅。

## 4. 操作

### 核心

所有会话操作都只作用于调用方设备自己的会话，即它用 `session.create` 创建的会话（[设计 4.4](client-link-layer-design.md)）。访问其他设备的会话，或由宿主 WebChat、消息渠道创建的会话，返回 `not_found`；对这类会话调用 `session.delete` 返回 `deleted: true`，但不删除任何内容。

| 操作 | 参数 → 结果 | 说明 |
|---|---|---|
| `ping` | — → `{time}` | 存活检测 |
| `device.describe` | — → `{principal, features, domains, backend}` | |
| `sync.changes` | `{cursor, limit}` → `{sessions[], cursor, more, scope}` | 返回本设备 `revision > cursor` 的会话头，按 revision 排序；删除标记为 `deleted: true`。revision 由所有设备共用，所以一台设备看到的 revision 有空档。`scope` 为 `device`；缓存不是在这个范围下建立（或建立时没有范围）的客户端清空一次会话缓存，从游标 0 重新读取 |
| `session.create` | `{title, client_request_id}` → 会话头 | 按 `client_request_id` 幂等 |
| `session.rename` | `{session_id, title}` → 会话头 | |
| `session.archive` | `{session_id, archived}` → 会话头 | |
| `session.delete` | `{session_id}` → `{session_id, deleted}` | 重复删除视为成功；会话中上传的附件一并删除 |
| `session.messages` | `{session_id, before_seq?, after_seq?, limit}` → `{session, messages[], more}` | 按时间从旧到新；`before_seq: 0` 返回最新一页 |
| `message.send` | `{session_id, client_message_id, content, attachments[], client_timezone, client_presentation?, replay_only?}` → `{message, run_id, replayed}` | 相同的 `client_message_id` 返回原结果。带 `replay_only` 时后端只回答消息是否到达：返回原回复；从未到达则返回带 `not_sent` 的 `not_found`，不会补跑。运行中再发送返回 `busy`。电脑端可声明 `client_presentation {schema_version, schedule_show, timezone, requested_at, local_conversation_id}`；后端随后可在最终消息的 `extra.client_action`（`schedule.show`）里交回本机日程展示，而不读取本机日程 |
| `run.subscribe` | `{session_id}` → `{subscription, status}` | 仅 duplex；先返回当前快照，之后推送 `run` 事件 |
| `run.unsubscribe` | `{session_id}` → `{ok}` | 仅 duplex |
| `run.status` | `{session_id}` → RunStatus | `state` 取 idle、running、approval_pending、completed、failed、cancelled 或 blocked；另带 `preview`、`progress`、`final_message`、`pending_approvals`，以及 `seq`：快照包含的最后一个运行事件。已结束的运行从已存会话中回答 |
| `run.cancel` | `{session_id, run_id?}` → RunStatus | |
| `approval.list` | `{session_id?, pending?}` → `{approvals[]}` | 只返回本设备会话里产生的审批；`approval.resolve` 也只接受这些审批 |
| `approval.resolve` | `{approval_id, decision: approve\|reject, inputs?, idempotency_key}` → `{approval}` | |

### 对象（需要设备条件 `attachments`）

| 操作 | 说明 |
|---|---|
| `object.upload.begin` | 参数 `{upload_key, name, size, content_type, sha256, session_id}`；业务域对象改用 `{…, purpose}`，不带 `session_id`。返回 `{upload_id, received, chunk_size}`。相同的 `upload_key` 从断点续传 |
| `object.upload.chunk` | 参数 `{upload_id, offset, data}`，`data` 为 base64 |
| `object.upload.commit` | 校验 SHA-256 后返回 `object_id` |
| `object.describe` | 不要求 `attachments` 条件 |
| `object.read` | 参数 `{object_id, offset, length}`，返回 `{object, offset, data, eof}` |

业务域对象（带 `purpose` 上传，或由业务域方法返回）属于上传或接收它的设备，只有该设备能读，不进入工作区，24 小时后过期，id 是 UUID。目前使用的用途：`mail_send_attachment`、`speech_recording`、`domain_args`、`domain_result`、`mail_attachment`。

配额：

- 单个文件不超过 25 MiB；
- 每台设备未完成的上传不超过 8 个；
- 暂存总量不超过 512 MiB；
- 暂存的上传 24 小时后过期。

### 业务域

`domain.call` 的参数是 `{domain, method, args, args_object?, idempotency_key?, replay_only?}`，返回 `{result}`：

- `method` 是类型化的业务域方法，例如 `mail.conversations.list`；`domain` 是它的第一段。`device.describe` 列出后端提供的业务域。
- `args` 是方法自己的类型化字段。电脑端路由指名的记录（一封邮件、一个会话、一个绑定）也是字段，绝不是路径。未知字段会被忽略。
- 超过一帧的参数先作为用途 `domain_args` 的业务域对象上传，用 `args_object` 指名，代替 `args`。调用及其回执都按参数内容计算。
- 修改类操作必须带 `idempotency_key`（UUID）。后端在执行前记下这次尝试；用同一键重试，得到原结果、`conflict`（同一键但内容不同）或 `outcome_unknown`（尝试开始后没有结束），绝不会执行两次。
- `replay_only: true` 只返回修改类操作的原结果，从不启动它；带 `not_sent` 的 `not_found` 表示它从未执行。

结果有两种形态：

- **类型化结果**（`medicine`、`notifications`、`speech`、`browser`、`mcd`）：方法的值。失败是链路错误，`reason` 是业务域自己的原因（例如 `medicine_sync_reset_required`）；模型故障是 `backend_unavailable`，消息为“模型暂时不可用”。
- **业务结果**（`settings`、`mail`、`presentation`）：`{status, body, body_object?}`，即宿主 WebChat 对同一业务调用给出的状态码和 JSON 正文，电脑端原有界面原样读取。文件或超过一帧的正文以 `body_object` 返回，这是一个用 `object.read` 读取的业务域对象。业务失败是带状态码的结果（例如 `409 revision_conflict`）；链路错误只用于调用本身。

| 业务域 | 方法 |
|---|---|
| `medicine` | `medicine.sync`、`.operations.get`、`.items.create`、`.items.update`、`.items.delete`、`.items.batch`、`.import`、`.recognize`、`.parse` |
| `notifications` | `notifications.list` `{limit?, after?}`（带密封的 `watermark`）、`.read` `{id}`、`.read_all` `{watermark}` |
| `speech`（条件 `microphone`） | `speech.status`、`.transcribe` `{session_id, request_id, language, audio_object}`、`.cancel`、`.session.open`、`.session.frame`、`.session.events`、`.session.finish`、`.session.cancel` |
| `browser`、`mcd`（条件 `browser_surface`） | `browser.receipt`、`browser.reconcile`；`mcd.session.provision`、`.revoke`、`.status` |
| `settings` | `settings.owner.get`/`.patch`、`.connectors.list`/`.patch`、`.integrations.list`、`.credentials.add`/`.activate`/`.check`/`.delete`、`.bindings.list`/`.start`/`.get`/`.poll`/`.revoke`/`.login`、`.browser.get`/`.save`/`.check`/`.delete`、`.mcp.catalog`/`.transports`/`.tickets.*`/`.bindings.*`/`.records.delete`/`.pairing.*` |
| `mail` | `mail.providers.*`、`.intake.update`、`.intake.reconcile`、`.mailboxes`、`.sync`、`.attachment`、`.message`、`.conversations.*`、`.pending`、`.notifications`、`.interaction`、`.sent_sources`、`.sender_rules.*`、`.verification`、`.render_preview`、`.file`、`.presentations.*`、`.compose.capabilities`、`.sync.*`、`.drafts.*`、`.send`、`.replies.polish`、`.source.cleanup`、`.classification`、`.assignment`、`.viewed`、`.reanalyze` |
| `presentation` | `presentation.config`、`.owner`、`.ready` |

设置中的主人资料、连接器和集成的值以 `{revision, value}` 返回；修改时带上它所依据的 `expected_revision`，版本已过期则返回 `409 revision_conflict`。

收信设置的修改（`mail.intake.update`）若回复丢失且结果未知，用 `mail.intake.reconcile` `{idempotency_key, original}` 了结，其中 `original` 是该修改自己的参数：由邮箱判断修改是否生效，这个答复随即成为该修改的回执（`{state: "completed", idempotency_key, response}`）。邮箱登录（`mail.providers.login`）在服务商失败时保持 `outcome_unknown`，因为登录窗口可能已经打开。

### 投影

`projection.snapshot` 和 `projection.activity` 取代原来的 `phone.snapshot.get` 和 `phone.activity.list`。

### 设备投影

后端给手表返回的数据会被裁剪：

- 消息正文：转为纯文本，最多 2000 字；
- 列表预览：最多 120 字；
- 附件：只给文件名；
- 历史页：每页最多 20 条。

## 5. 事件

| 主题 | 数据 |
|---|---|
| `run` | `{session_id, run_id, seq, kind: state\|delta\|progress\|final, text?, status?}`。`seq` 在一个会话的所有运行中逐一递增，一个订阅按顺序收到。出现缺口说明有事件丢失，应重新读取 `run.status` |
| `sync` | `{cursor}`：本设备的某个会话发生变化，客户端应调用 `sync.changes` |
| `approval` | `{pending}`：本设备会话里待处理的审批有变化 |
| `domain` | `{domains[]}`：某个业务域的状态有变化（例如 `settings`、`mail`、`medicine`），应重新读取 |
| `device` | `{state: "revoked"}`：后端随后会关闭连接 |

## 6. 错误

`error` 的格式是 `{code, message, retryable, not_sent?, reason?}`。`not_sent` 表示请求从未到达处理方（没有可用路径、帧没能写出，或后端在执行前拒绝）：它没有产生任何效果，可以再次发送。`reason` 是业务域自己的原因。错误码如下：

- `unsupported_capability`
- `invalid_request`
- `not_found`
- `conflict`
- `unauthorized`
- `revoked`
- `backend_unavailable`
- `too_large`
- `busy`
- `cancelled`
- `internal`
- `quota_exceeded`
- `timeout`
- `outcome_unknown`：重试的修改类操作，其原尝试开始后没有结束。它不会再执行，由客户端核对业务状态

`backend_unavailable` 表示后端不可达：客户端立即报错，不排队（设计文档 3.12）。请求已发出但没有收到回复时，用同一幂等键重试，目的只是查明结果。

## 7. 反向调用

带内置浏览器的电脑端在 hello 中声明以下反向操作：

- `browser.controller.status`、`.acquire`、`.execute`、`.release`、`.chunk`
- `mcd.account.ensure`
- `mcd.checkout.prepare`、`.commit`、`.abort`
- `mcd.operation.status`
- `mcd.orders.query`

后端发出 id 以 `s` 开头的 `request` 帧，客户端用 `response` 帧回复。请求和回复的正文与原传输完全相同。

## 8. 传输

### 8.1 局域网连接器

- **地址**：`wss://<主机>:<端口>/link/v1`，子协议 `sparkclaw.link.lan.v1`。
- **证书校验**：客户端按 `tls_pin` 固定后端证书。`tls_pin` 是叶证书 DER 的 SHA-256，用 base64url 编码，由官网在后端设备记录中发布。

在任何连接帧之前，双方先完成认证：

1. 服务端发送 `{"type":"challenge","version","host":<后端设备 ID>,"nonce"}`。
2. 客户端核对 `host` 后回复 `{"type":"auth","device_id","proof"}`。`proof` 是 ISCP 设备证明，受众为后端设备 ID，挑战为 `sparkclaw.link.lan:` 加 nonce。
3. 服务端按本地保存的设备目录校验证明，回复 `{"type":"authenticated","principal"}` 或 `{"type":"error","code","message"}`。

认证通过后，每条 WebSocket 文本消息承载一帧。

服务端每 10 秒 ping 一次；连接 35 秒内没有收到任何数据（包括 pong）就关闭。客户端 40 秒内没有收到任何数据也会关闭连接。

### 8.2 ISCP Relay

每台设备与后端保持一个端到端 ISCP 会话。所有载荷都放在 ISCP 安全信封里：

| 载荷类型 | 加密 | 内容 |
|---|---|---|
| `sparkclaw.link.grant.v1` | 明文 | 设备当前的 Grant；每次握手时先于 Hello 发送 |
| `iscp.session.hello.v2` / `iscp.session.ready.v2` | 明文 | 标准 ISCP 握手；设备的 Hello 注明所用的 Grant |
| `sparkclaw.link.frame.v1` | 加密 | 一帧连接帧 |
| `sparkclaw.link.reset.v1` | 明文 | `{reason}`，设备须重新握手；原因包括 `unknown_session`、`grant_invalid`、`revoked`、`superseded` |

后端对每一帧做两项检查：

- 用官网签名者校验 Grant：主体、受众、权限 `sparkclaw.link`、Relay、有效期和撤销纪元；
- 按设备目录检查该设备。

设备在以下情况重新握手：收到 reset 后、Grant 续期后，以及距上次握手满 6 小时。

### 8.3 连接器套接字

ISCP 连接器为每个设备会话与 Gateway 建立一条 Unix 套接字连接。

- **分帧**：每帧前加 4 字节大端长度。
- **连接**：连接器先发 `{"device_id","transport","session_id"}`，Gateway 回复 `{"ok","code","message","principal"}`，之后传送连接帧。
- **Gateway 重启**：连接器重新连接 Gateway，并重放设备原来的 hello。
