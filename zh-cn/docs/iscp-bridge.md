# ISCP Bridge

> 语言：[English](../../docs/iscp-bridge.md) | 简体中文

SparkClaw ISCP Bridge 是独立的传输进程，用于把通过认证的 ISCP v2 会话转换为本地 Gateway
拥有的 provider-neutral `agent.*.v1` API。它不会直接调用 Agent Runtime 或 ToolHub，也不接收
或保存 ITES access/refresh token。

## 已支持范围

Bridge 只声明当前 Gateway 已实现的能力：

- `agent.sessions` v1；
- `agent.conversation` v1；
- `agent.streaming` v1；
- `agent.activities` v1；
- `agent.approvals` v1。

远程 workspace 和 file 的授权及上传/下载合约尚未端到端实现，因此不会声明。版本化 JSON
Schema 位于
[`packages/protocol/iscp-bridge.v1.schema.json`](../../packages/protocol/iscp-bridge.v1.schema.json)。

## 安全边界

- 生产环境的设备身份密钥只允许使用操作系统 keyring；`file` 后端只用于 `local-lab`，生产配置会拒绝它。
- enrollment 和 Gateway token 文件在 Unix 上必须是 `0600` 普通文件。使用 keyring 时，长期设备私钥不会写入配置或 enrollment bundle。
- 生产 Relay 必须使用 HTTPS/WSS。每次提交 envelope 除短期 Relay access credential 外，还携带 ISCP proof-of-possession。
- Gateway dispatch 端点只接受 loopback，并在 Gateway 未启用认证时拒绝请求。Bridge 可使用专用配对客户端 token 或 `SPARKCLAW_API_TOKEN`。
- Session Hello/Ready 使用已签名对象，并通过公开的 `sparkclaw.iscp.relay_frame.v1` 包装投递。只有 Ready 完成密钥确认后，业务请求、响应和事件才通过 ISCP `SecureEnvelope` 的 `task.invoke` / `task.result` 发送。
- Bridge 校验 peer 身份、Domain、Trust Grant audience、confirmation thumbprint、permission、Relay constraint、revocation epoch、过期时间、Hello 时间窗、endpoint 绑定和 envelope 序号。

## 注册

在 GB10 上创建设备身份和公开 enrollment request：

```bash
cd services/gateway
go run ./cmd/iscp-bridge enroll \
  -identity-dir ../../data/iscp-bridge/identity \
  -domain DOMAIN_ID \
  -device DEVICE_ID \
  -hardware gb10 \
  -output ../../data/iscp-bridge/enrollment-request.json
```

默认把 Ed25519 私钥保存到系统 keyring。一次性的本地测试可添加 `-key-backend file`，并使用
`local-lab` Bridge 配置。

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

启用 Gateway 认证，并把相同 bearer 值或专用配对客户端 token 写入私有文件：

```bash
install -d -m 700 data/iscp-bridge
install -m 600 /dev/null data/iscp-bridge/gateway.token
```

以 [`configs/iscp-bridge.example.json`](../../configs/iscp-bridge.example.json) 为无密钥配置样例，
分别启动 Gateway 和 Bridge：

```bash
cd services/gateway
go run ./cmd/sparkclaw -config ../../configs/sparkclaw.default.json
go run ./cmd/iscp-bridge run -config ../../configs/iscp-bridge.example.json
```

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

短期 credential 会自动刷新。轮换密钥或 Domain 时，从 Cloud 获取新 bundle，以 `0600` 原子
替换 enrollment 文件后重启 Bridge。撤销设备时，先在 Cloud 撤销再停止服务；Relay 将拒绝
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
