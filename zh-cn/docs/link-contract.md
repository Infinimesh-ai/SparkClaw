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
- `reverse`：后端接受的反向操作。

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

| 操作 | 参数 → 结果 | 说明 |
|---|---|---|
| `ping` | — → `{time}` | 存活检测 |
| `device.describe` | — → `{principal, features, domains, backend}` | |
| `sync.changes` | `{cursor, limit}` → `{sessions[], cursor, more}` | 返回 `revision > cursor` 的会话头，按 revision 排序；删除标记为 `deleted: true` |
| `session.create` | `{title, client_request_id}` → 会话头 | 按 `client_request_id` 幂等 |
| `session.rename` | `{session_id, title}` → 会话头 | |
| `session.archive` | `{session_id, archived}` → 会话头 | |
| `session.delete` | `{session_id}` → `{session_id, deleted}` | 重复删除视为成功；会话中上传的附件一并删除 |
| `session.messages` | `{session_id, before_seq?, after_seq?, limit}` → `{session, messages[], more}` | 按时间从旧到新；`before_seq: 0` 返回最新一页 |
| `message.send` | `{session_id, client_message_id, content, attachments[], client_timezone}` → `{message, run_id, replayed}` | 相同的 `client_message_id` 返回原结果；运行中再发送返回 `busy` |
| `run.subscribe` | `{session_id}` → `{subscription, status}` | 仅 duplex；先返回当前快照，之后推送 `run` 事件 |
| `run.unsubscribe` | `{session_id}` → `{ok}` | 仅 duplex |
| `run.status` | `{session_id}` → RunStatus | `state` 取 idle、running、approval_pending、completed、failed、cancelled 或 blocked；另带 `preview`、`progress`、`final_message`、`pending_approvals` |
| `run.cancel` | `{session_id, run_id?}` → RunStatus | |
| `approval.list` | `{session_id?, pending?}` → `{approvals[]}` | |
| `approval.resolve` | `{approval_id, decision: approve\|reject, inputs?, idempotency_key}` → `{approval}` | |

### 对象（需要设备条件 `attachments`）

| 操作 | 说明 |
|---|---|
| `object.upload.begin` | 参数 `{upload_key, name, size, content_type, sha256, session_id}`，返回 `{upload_id, received, chunk_size}`。相同的 `upload_key` 从断点续传 |
| `object.upload.chunk` | 参数 `{upload_id, offset, data}`，`data` 为 base64 |
| `object.upload.commit` | 校验 SHA-256 后返回 `object_id` |
| `object.describe` | 不要求 `attachments` 条件 |
| `object.read` | 参数 `{object_id, offset, length}`，返回 `{object, offset, data, eof}` |

配额：

- 单个文件不超过 25 MiB；
- 每台设备未完成的上传不超过 8 个；
- 暂存总量不超过 512 MiB；
- 暂存的上传 24 小时后过期。

### 业务域

`domain.call` 的参数是 `{domain, method, args, idempotency_key}`，返回 `{result}`：

- `method` 是业务域操作的名称，例如 `mail.mailboxes.list`；`domain` 是它的第一段。
- `args` 是该操作的类型化字段：`{params, body, body_object, expected_revision, input_digest, request_id}`。
- `result` 原样返回业务域自己的结果：`{status, body, body_object, error_code, error, retryable}`。
- 修改类操作必须带 `idempotency_key`（UUID）。重试时从回执日志返回原结果。
- 大的请求体走 `transfer.*` 方法上传；大的结果以 `body_object` 返回，再用同一通道的 `object.*` 方法读取。

以下操作已由核心操作取代或归官网管理，通过 `domain.call` 调用会被拒绝并给出原因：`execution.*`、`approvals.*`、`clients.*`、`authorizations.*`、`installation.*`、`phone.*`、`capabilities.*`、`workbench.*`、`state.*`、`audio.*`。

`mcd.*` 和 `browser.*` 方法要求设备有内置浏览器界面。

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
| `run` | `{session_id, run_id, kind: state\|delta\|progress\|final, text?, status?}` |
| `sync` | `{cursor}`：有会话发生变化，客户端应调用 `sync.changes` |
| `approval` | `{pending}`：待处理的审批有变化 |
| `device` | `{state: "revoked"}`：后端随后会关闭连接 |

## 6. 错误

`error` 的格式是 `{code, message, retryable}`。错误码如下：

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
