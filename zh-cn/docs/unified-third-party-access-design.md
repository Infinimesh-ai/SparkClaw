# 统一第三方 ISCP MCP 接入设计

> 语言：[English](../../docs/unified-third-party-access-design.md) | 简体中文

> 状态：SparkClaw 自身负责的本地 Runtime、owner 管理面、Binding schema、conversation tool 与
> MCP result provider 已在当前工作树实现。生产 ISCP authority 接入、可部署 external Access
> Gateway、真实 Relay 验证、LocalMind 切换和旧链路删除仍属于上线工作。

## 决策

入站外部 MCP 是一条 provider-neutral 的普通对话 channel。它不会向远端 client 投影
SparkClaw Catalog 叶子、Workflow Profile、ToolHub tool 或 approval grant。

有效 Binding 只暴露一个业务 tool：`sparkclaw.conversation.send`。该 tool 提交 owner 编写的
文本，以及可选的现有 workspace 多媒体 locator，随后由 Message Runtime 执行普通语义路由。
MCP 初始化、ticket 兑换、operation 状态/结果、取消和 Binding 撤销属于协议或生命周期控制，
不是额外业务能力。

本文只适用于第三方访问 SparkClaw 的入站方向。SparkClaw 主动访问 LocalMind workspace 的
MCP client 属于相反方向，保持不变。JingSi 不加入该 MCP contract；其当前共享 Bridge surface
保持冻结，直到独立绑定设计替代它。

## 为什么同时需要 MCP 与 ISCP

ISCP 负责网络成员资格和认证传输。其 authority 定义、签名、校验和消费 Pairing Ticket，校验
Device Proof，签发 Trust Grant 与 Relay credential，建立加密 session，轮换 credential，并执行
transport revocation。

SparkClaw 负责应用授权。认证 ISCP session 建立后，SparkClaw 另行签发短期、单次使用的 MCP
Access Ticket。外部 device 只能通过该认证 session 兑换它，以激活持久 conversation-scoped
MCP Binding。

两种 ticket 不能互换：

| 凭证 | 权威 | 用途 | 持久结果 |
|---|---|---|---|
| ISCP Pairing Ticket | ISCP authority | 允许 device 加入 SparkClaw ISCP Domain | ISCP device/session credential |
| MCP Access Ticket | 本地 SparkClaw | 授权入站普通对话 | SparkClaw MCP Binding |

普通 MCP call 不复用任一 ticket，而是依赖当前认证 peer identity 与有效 Binding。

## 目标拓扑

```text
外部 MCP client
  -> external ISCP MCP Access Gateway
  -> authenticated ISCP session / SecureEnvelope / Relay
  -> SparkClaw-side ISCP MCP Access Gateway
  -> local MCP protocol adapter
  -> MessageEnvelope (third_party_device)
  -> 普通路由与 Workflow Runtime
  -> WorkflowResult -> Delivery Gateway -> MCP Provider
  -> 关联的持久 MCP operation result
```

两个 gateway role 都通过出站连接访问 Relay；ISCP 模式下，本地 SparkClaw MCP service 不开放
公网应用入站端口。Owner 也可以显式开启局域网直连，使 MCP 使用现有 WebChat `18790` 入口的
`/mcp`；Nginx 只把该精确路由转发到 Docker 内部 Gateway。该选项只替换 transport，不放宽
Binding、Runtime、Store 或 Delivery contract，且开关关闭时 endpoint 不存在。

## 组件职责

| 组件 | 职责 |
|---|---|
| ISCP authority 与 Relay | device 准入、认证加密 session、credential 轮换与 transport revocation |
| External Access Gateway | 持有外部 device identity、完成 pairing、实现 MCP `2025-06-18` 并通过 ISCP 传输 frame |
| SparkClaw MCP adapter | 校验 MCP envelope、兑换 MCP Access Ticket、认证 Binding 并管理持久 operation |
| Message Runtime | 把调用规范化为普通 third-party-device 输入并执行普通语义路由 |
| Workflow Runtime | 执行选中的已注册 Workflow；普通对话使用 `conversation.answer` r3 |
| Store | 在 memory/file/PostgreSQL 中持久化只存 hash 的 ticket、schema-v2 Binding、invocation provenance、operation 状态与脱敏 audit |
| Delivery Gateway 与 MCP Provider | 校验完整 multipart result，并原子编码关联 MCP content |

外部 requester 保留为 source provenance。本地经 owner 授权的 SparkClaw principal 仍是 Workflow
actor 与 executor。

## 配对与 Binding

1. Owner 启用默认关闭的通用 `mcp` connector。
2. SparkClaw 通过配置的 authority adapter 请求 `iscp.pairing_ticket.v2`，只展示一次签名 ticket；
   SparkClaw 只保留非秘密 onboarding receipt。
3. Owner 将 ticket 一次性交给 external Access Gateway。
4. Gateway 通过标准 ISCP PairingTicket/Provisioning 兑换它，并在 SparkClaw Domain 中建立认证
   session。
5. SparkClaw 另行签发固定 `scope: conversation` 的 copy-once MCP Access Ticket。
6. 认证 external peer 兑换该 ticket。SparkClaw 原子消费 secret hash，创建 schema version 2 的
   MCP Binding，绑定认证 domain/device/key thumbprint、关联本地 session、owner/actor、conversation
   scope 与 authorization revision。
7. 重连必须提供相同认证 peer identity。轮换为新 device key 时必须重新 pairing 和 MCP 授权。

低于 schema v2 或非 conversation scope 的 Binding fail closed，绝不会静默放宽为 conversation
access。connector 关闭、Binding suspend/revoke、owner 不匹配、peer 不匹配或 linked-session 不
匹配都会阻断 ingress 与 delivery。

## MCP Channel Contract

本地 service 严格使用 MCP `2025-06-18`。首版保留 SparkClaw 持久 operation control，不声明
标准 MCP Tasks。

业务 surface：

- `sparkclaw.conversation.send`

Binding-scoped 生命周期控制：

- `sparkclaw.operation.get`
- `sparkclaw.operation.result`
- `sparkclaw.operation.cancel`

不列出或允许调用任何 `sparkclaw.route.*`、`files.search`、目录列表、resource browser、
workspace reader、Catalog projection 或远程 approval tool。client 不能选择 route、operation、
effect、Workflow revision、model lane、owner、session、endpoint、MIME type、artifact identity、
hash 或 workspace root。

每个 `tools/call` 都需要有界 deadline 与 idempotency key。Binding 和 key 派生稳定 invocation、
message、run 与 operation identity。相同请求重试观察同一持久 operation；相同 key 下 fingerprint
不同则失败。取消是终态但不承诺 rollback。重启恢复保持 operation/result 关联。

## 普通对话与 Workspace 多媒体

`sparkclaw.conversation.send` 接受非空文本、一个到八个有序 media locator，或两者同时存在。
每个 locator 必须且只能提供以下一个字段：

- `path`：精确 workspace 相对路径；
- `name`：完整、区分大小写的 basename；
- `query`：不完整文件名或 owner 编写的简短描述。

Adapter 只校验语法，不解析文件。消息进入普通路由。当选中 `conversation.answer` r3 时，固定
Workflow 为：

```text
detect_response_media -> answer
```

检测节点依次复用已治理的直接附件/path、尝试精确 basename，再在精确未命中或描述不完整时
使用共享、有界且只匹配文件名的查询。查询不读取文件内容，也不返回 preview。它只对正文件名
匹配排序，并以 workspace 相对路径字典序作为最终稳定 tie-break。每个 locator 只选择 Top-1；
显式多个 locator 保持输入顺序。

完整零结果返回 `file_not_found`，要求 owner 提供更明确名称或直接附件。失败、超时、截断、权限
不完整、不安全或只完成部分遍历的查询返回阻断 reason，且不发送临时候选。绝对路径、路径逃逸、
symlink、目录、特殊文件、重复项、跨 workspace 对象和已变化对象都会原子失败。

检测阶段冻结 workspace 相对 resource ref、实际 byte count、content type、artifact identity 与
SHA-256。answer 节点不能搜索、增加、删除、刷新或替换资源，并在完成前重新校验冻结对象。普通
回答可以返回文本加冻结媒体；纯 publish 可以只返回媒体。

完整 schema 与决策 contract 见[外部 MCP 普通对话能力收敛设计](external-mcp-conversation-design.md)。

## 结果 Delivery

每个终态 route 都生成共享 `WorkflowResult` 与 `DeliveryRequest`。Endpoint Registry 解析
Binding-scoped MCP source，Delivery Gateway 调用一个通用 MCP Provider。

Provider 按以下方式映射有序 result part：

| SparkClaw part | MCP content |
|---|---|
| text | `text` |
| image | 带 base64 data 与 MIME type 的原生 `image` |
| audio | 带 base64 data 与 MIME type 的原生 `audio` |
| 其他 file | 使用 operation/part URI 的内嵌 `resource` blob |

本地路径与 `workspace://` identity 绝不跨越协议边界。每个二进制对象必须属于 Binding 关联
session，并仍匹配冻结 byte count 与 hash。所有 part 在 Store mutation 前完成准备；任一 part
无效都不会留下部分 operation result。

原始二进制媒体限制在 3 MiB 以下，为 base64 膨胀、有界文本、content metadata 与 structured
operation projection 在 4 MiB 编码后 MCP result envelope 内预留空间。持久化前仍以最终编码
envelope 检查为准。

## Policy、Audit 与管理面

`mcp` connector 默认关闭。Owner setting 同时 gate Pairing Ticket 签发、MCP ingress、endpoint
可见性与 Provider 可用性。保留的 ticket 或 Binding 不代表 connector 已启用。

WebChat 展示 connector 状态、copy-once ISCP/MCP ticket flow、固定 conversation scope、关联
Binding 状态与 ticket/Binding revoke，不再包含 Catalog grant picker。

Audit 覆盖 ticket 签发/兑换/重放/撤销、Binding 激活与撤销、peer denial、tool list/invocation、
response-media 决策与有界查询 outcome、operation 创建/重放/冲突/取消/终态，以及 result delivery。
记录使用稳定 reason code，并省略 secret、绝对路径、文件内容和 raw result blob。

## LocalMind 切换与 JingSi 边界

LocalMind 的旧 external-controller enrollment、`agent.*.v1` conversation fallback 与 passive
Bridge path 保持临时可用，直到其 Access Gateway 重新配对加入 SparkClaw Domain、兑换新的
conversation-scoped MCP Access Ticket，并通过真实 Relay E2E。随后删除 LocalMind Bridge
manifest entry、grant、dispatch branch、fallback、config、test 与 guidance。不得向旧链路新增
compatibility feature。

JingSi 仍需要的 Bridge component 保持不变，直到 JingSi 独立绑定项目负责替换。这不保留隐藏
LocalMind fallback，也不让 JingSi 加入 MCP。

## 验收标准

- 有效 Binding 只列出一个业务 tool 与三个 operation control；远端不能调用 route leaf 或
  workspace search tool。
- 纯文本调用使用普通语义路由与共享 Message/Runtime 链路。
- 直接 path、精确 basename 与不完整文件名查询使用同一个只匹配文件名的实现和稳定 Top-1，
  选择受治理 workspace 媒体。
- 零结果澄清，不完整查询阻断；两者都不返回候选或部分媒体。
- `conversation.answer` r3 先检测再回答，并在构造结果前发现对象变化。
- MCP result 保持有序 text/image/audio/file content，不泄露本地路径，part 或 envelope 失败时
  保持原子性。
- memory、file 与 PostgreSQL backend 的 ticket、Binding、invocation 与 operation 行为一致。
- connector 关闭、revoke、idempotency、deadline、cancellation、recovery、endpoint isolation 与
  脱敏 audit 全部 fail closed。
- 生产上线前 Gateway build/test/vet、WebChat test/build、双语文档检查、Compose、doctor 与聚焦
  live ISCP 验证全部通过。
