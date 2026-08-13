# ISCP Bridge

> 语言：[English](../../docs/iscp-bridge.md) | 简体中文

SparkClaw ISCP Bridge 是独立的传输进程，用于把通过认证的 ISCP v2 会话转换为本地 Gateway
拥有的 provider-neutral `agent.*.v1` API。它不会直接调用 Agent Runtime 或 ToolHub，也不接收
或保存 ITES access/refresh token。

本文描述当前共享 Bridge。LocalMind 使用的 enrollment 方向属于旧链路：SparkClaw 发送
enrollment request，再由外部 LocalMind controller 返回 Bridge 使用的 bundle 与 grant。
目标方案不会通过新增 SparkClaw-specific ISCP credential service 来反转该旧 flow。SparkClaw 在
本地通过标准 ISCP pairing 能力展示一次性 Pairing Ticket；LocalMind Access Gateway 连接 ISCP，
通过 Provisioning 兑换该凭证，作为新 device 加入 SparkClaw ISCP Domain。ticket 的定义、签名、
校验和消费，以及 device admission、Trust Grant、Relay credential、session security、rotation
和 transport revocation 均由 ISCP 负责。认证 ISCP session ready 后，SparkClaw 通过该 session
独立签发并消费单次使用 MCP Access Ticket，以激活持久本地 Route MCP Binding。该应用 ticket
不会让 device 加入 ISCP，也不是公网 claim service。本文不决定 JingSi 未来的 credential flow。

替代架构使用一个经通用 ISCP MCP Access Gateway 传输的 Route MCP Service。SparkClaw 自身
负责的本地 runtime 与加密 Bridge dispatch 已实现；生产 PairingTicket/Provisioning 集成、
可部署 external gateway 和真实 Relay 验证仍待完成。详见
[统一第三方 ISCP MCP 接入](unified-third-party-access-design.md)。不得再为旧 Bridge 增加
LocalMind caller、capability 或 compatibility feature。
LocalMind 通过新链路后，删除其 Bridge registration、grant、branch、fallback、config、test 和
guidance。只冻结保留 JingSi 当前仍需的最小共享 surface；JingSi 不接入 MCP，最终 Bridge 退役由
其后续独立绑定项目负责。SparkClaw 主动访问 LocalMind workspace 的出站 MCP client 不在删除
范围内。

## 已支持范围

Bridge 只声明当前 Gateway 已实现的能力：

- `agent.sessions` v1；
- `agent.conversation` v1；
- `agent.streaming` v1；
- `agent.activities` v1；
- `agent.approvals` v1；
- `agent.notifications` v1。

`agent.notification.deliver.v1` 只接收结构化的 LocalMind `document_mention` 或
`comment_mention`、有界 deep link 和发生时间。Gateway 先把 owner-scoped 收件箱记录持久化，
然后才返回 `status: "ok"`。这条路径不会创建 Agent session、message、run、model call、
tool call 或 approval。原有创建 session 和发送消息 path 只在目标切换前临时存在于旧链路中；
切换时删除 LocalMind 对两条 path 的 authorization 与 dispatch，但不删除 JingSi 当前对共享
request type 的使用。

远程 workspace 和 file 的授权及上传/下载合约尚未端到端实现，因此不会声明。版本化 JSON
Schema 位于
[`packages/protocol/iscp-bridge.v1.schema.json`](../../packages/protocol/iscp-bridge.v1.schema.json)。

## 安全边界

- 生产环境的设备身份密钥只允许使用操作系统 keyring；`file` 后端只用于 `local-lab`，生产配置会拒绝它。
- enrollment 文件和已配置的 Gateway token 文件在 Unix 上必须是 `0600` 普通文件。使用
  keyring 时，长期设备私钥不会写入配置或 enrollment bundle。
- 生产 Relay 必须使用 HTTPS/WSS。每次提交 envelope 除短期 Relay access credential 外，还携带 ISCP proof-of-possession。
- Gateway dispatch 端点只接受 loopback。Gateway 未启用认证时，Bridge 无需占位 token 即可
  dispatch；启用认证时，使用专用配对客户端 token 或 `SPARKCLAW_API_TOKEN` 认证 Bridge。
- Session Hello/Ready 使用已签名对象，并通过公开的 `sparkclaw.iscp.relay_frame.v1` 包装投递。只有 Ready 完成密钥确认后，业务请求、响应和事件才通过 ISCP `SecureEnvelope` 的 `task.invoke` / `task.result` 发送。
- Bridge 校验 peer 身份、Domain、Trust Grant audience、confirmation thumbprint、permission、Relay constraint、revocation epoch、过期时间、Hello 时间窗、endpoint 绑定和 envelope 序号。

## 注册

对 LocalMind 而言，以下流程只记录旧链路的当前运维方式。生成的 request 与外部返回 bundle
不得被目标 MCP gateway 接受，也不得转换为目标 peer binding；切换时，现有 LocalMind device
必须使用新的 ISCP PairingTicket/Provisioning 加入 SparkClaw Domain，再通过认证 session 兑换
新的 SparkClaw MCP Access Ticket。JingSi 在独立替代设计落地前继续使用当前所需流程。

在 GB10 上创建设备身份和公开 enrollment request：

```bash
cd services/gateway
go run ./cmd/iscp-bridge enroll \
  -identity-dir ../../data/iscp-bridge/identity \
  -domain DOMAIN_ID \
  -device DEVICE_ID \
  -hardware gb10 \
  -proof-audience ENROLLMENT_AUDIENCE \
  -proof-challenge SHORT_LIVED_CHALLENGE \
  -output ../../data/iscp-bridge/enrollment-request.json
```

默认把 Ed25519 私钥保存到系统 keyring。一次性的本地测试可添加 `-key-backend file`，并使用
`local-lab` Bridge 配置。audience 与 challenge 必须同时提供；`-proof-nonce` 可省略，此时由
本机生成。生成的 `iscp.device.proof.v2` 可由 enrollment controller 通过
`identity.VerifyProof` 验证，以证明调用方持有设备私钥。

当前 LocalMind enrollment bootstrap 尚不签发 enrollment proof audience/challenge，其严格
request decoder 也尚不接受 `device_proof`。只有兼容该旧路径时才省略 proof 参数。强制设备
证明需要 LocalMind controller 协同升级；SparkClaw 不会声称旧 enrollment 已证明私钥持有权。

JingSi Cloud enrollment 端点应在 App 批准后接收该请求，并返回
`sparkclaw.bridge.enrollment.v1` bundle，至少包含：

- Domain、设备、Relay ID 与 HTTPS/WSS Relay 地址；
- 绑定当前设备的短期 access credential 与可轮换 refresh credential；
- Trust Root 公钥身份；
- 每个获准 App peer 的身份，以及入站和出站 Trust Grant。

ISCP v0.1.0 与 Bridge 需求均未定义 Cloud enrollment URL 及其认证传输，因此实现将它保留为
Cloud 对接接口，不虚构依赖 ITES 的端点。一次性 enrollment grant 绝不能写入长期 bundle。

把 Cloud 返回的 bundle 以 `0600` 写到 `data/iscp-bridge/enrollment.json`。Bridge 通过 ISCP
Relay refresh 端点轮换 access/refresh credential，并原子替换该文件。替换 bundle 后重启服务
即可完成 Domain 变化或重新注册。

## Gateway 与 Bridge 配置

示例配置面向 SparkClaw 默认的 loopback 无认证 Gateway，因此省略 `gateway.token_file`。
Gateway 启用认证时，把 bearer 值或专用配对客户端 token 写入私有文件，并在 Bridge 配置中
加入其路径：

```bash
install -d -m 700 data/iscp-bridge
install -m 600 /dev/null data/iscp-bridge/gateway.token
```

```json
"gateway": {
  "base_url": "http://127.0.0.1:18789",
  "token_file": "../data/iscp-bridge/gateway.token",
  "timeout_seconds": 30
}
```

以 [`configs/iscp-bridge.example.json`](../../configs/iscp-bridge.example.json) 为无密钥配置样例，
分别启动 Gateway 和 Bridge：

```bash
cd services/gateway
go run ./cmd/sparkclaw -config ../../configs/sparkclaw.default.json
go run ./cmd/iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

Gateway container image 已包含 `/usr/local/bin/iscp-bridge`，因此使用部署镜像时无需 Go 或
源码仓库即可获得可执行文件。默认 Compose service 的 entrypoint 仍是 Gateway；Bridge 需要
挂载 enrollment volume、访问 keyring，并作为独立进程或 service 运行和托管。

Bridge 使用有界指数退避重连。Relay/设备撤销会关闭进程，不会继续尝试缓存凭据。App 断线后
Gateway 中的运行仍可继续；`agent.event.resume.v1` 从持久 cursor 恢复，
`agent.operation.status.v1` 用于核对结果未知的 mutation。

## 运维

使用主机服务管理器把 Gateway 和 Bridge 作为两个独立服务运行。Bridge 只需要读取自身的
`0600` enrollment 与 Gateway token 文件；安装新二进制或替换 enrollment bundle 后重启
Bridge。滚动重启 Gateway 是安全的：Bridge 会在本机重连，远端可通过
`agent.operation.status.v1` 核对结果未知的 mutation。

把 stdout/stderr 接入主机日志系统。日志可包含 endpoint、request、session 与 operation ID，
但不得新增 credential 或解密后的消息正文。应对重复 Relay 认证失败、credential 刷新失败、
Trust Grant 拒绝、序号违规和持续重连告警。

Relay access/refresh credential 会自动轮换，并原子更新 enrollment 文件。当前 LocalMind
controller 的 refresh endpoint 只返回这两种 credential，不会续签 Trust Grant 或完整
enrollment bundle。因此 Trust Grant/bundle 到期仍需从 Cloud 获取新 bundle，以 `0600`
原子替换并重启 Bridge；不能把 Relay credential 刷新视为完整的长期 enrollment 续期。密钥或
Domain 轮换使用相同的重新注册流程。撤销设备时，先在 Cloud 撤销再停止服务；Relay 将拒绝
后续连接。仅在永久下线 GB10 时删除操作系统 keyring 中的设备私钥。

## 模拟 Bridge

App CI 可在没有 ISCP 或 Cloud 凭据时运行显式 `local-lab` mock。它通过带认证的 loopback
端点接受同一 schema，并转发给受保护的 Gateway adapter：

```bash
cp configs/iscp-bridge.example.json configs/iscp-bridge.local-lab.json
# 在本地副本中将 profile 设为 local-lab，并将 identity_key_backend 设为 file。
cd services/gateway
go run ./cmd/iscp-bridge mock \
  -config ../../configs/iscp-bridge.local-lab.json \
  -listen 127.0.0.1:18792 \
  -client-token-file ../../data/iscp-bridge/mock-client.token
```

使用 mock client bearer token 向 `POST /v1/requests` 发送请求。mock 不能监听非 loopback
地址，也不能在 production profile 下运行。

## 审批与幂等规则

创建会话、发送消息、取消和解决审批都必须携带 `idempotency_key`。消息 ID 和 run ID 由已认证
endpoint 与该 key 派生；同 key 配合不同输入会返回 `conflict`。

被动投递也必须携带 idempotency key。Gateway 按 `(endpoint_id, idempotency_key)` 跨重启
持久去重；相同 payload 重试会返回已有通知，修改 payload 则返回 `conflict`。deep link 必须是
不含 credential 的绝对 HTTPS URL；只有本地开发可使用 loopback HTTP。WebChat 只会在 owner
明确点击后打开链接。

带认证、按 owner 隔离的通知接口为：

```text
GET  /api/notifications
POST /api/notifications/{id}/read
POST /api/notifications/read-all
GET  /api/notifications/events/stream
```

全局 SSE stream 按 owner 限定。WebChat 先从持久列表初始化，在不切换当前 session 的前提下
展示新记录；read 状态在 Gateway 重启后仍然保留。

审批列表响应包含 `preview_hash`。解决审批必须提交 approval ID、`expected_state: "pending"`、
decision 和当前 hash。已经处理或发生变化的审批返回 `stale_state`；完全相同的重试返回现有
结果，不会再次执行工具。

## 验证

```bash
cd services/gateway
go test ./internal/iscpbridge ./internal/gateway ./cmd/iscp-bridge
go test -race ./internal/iscpbridge
go vet ./...
```

服务测试会使用 ISCP SDK 完成 Hello/Ready、验证双向 Trust Grant、解密能力声明、发送加密的
`agent.session.list.v1` 请求，并解密 Gateway 响应。
