# 统一第三方 ISCP MCP 接入设计

> 语言：[English](../../docs/unified-third-party-access-design.md) | 简体中文
>
> 状态：SparkClaw 负责的本地 runtime 与 owner 管理面已实现；生产 ISCP authority 接入、
> 外部 Access Gateway 部署、Relay 验证、LocalMind 切换和旧链路删除仍属于外部落地工作。
> 下文“实现状态”是该边界的权威说明。

## 决策

SparkClaw 将为 LocalMind 及未来选择该 MCP contract 的第三方提供一条 provider-neutral MCP
接入路径：

1. SparkClaw 拥有一个 Route MCP Service，把符合条件且已注册的 Workflow 路由叶子投影为
   MCP tool。
2. 通用 ISCP MCP Access Gateway 通过已认证的 ISCP session 与 `SecureEnvelope` 传输 MCP
   请求和结果。
3. LocalMind Access Gateway 先使用一次性 ISCP Pairing Ticket，作为设备加入 SparkClaw device
   所在的同一个 ISCP Domain。该 ticket 由 ISCP 定义、签名、校验和消费；设备准入、Trust
   Grant、Relay credential、session、加密、轮换和 transport revocation 均由 ISCP 负责，
   SparkClaw 不重复实现该控制面。
4. 端到端通道建立后，SparkClaw 另行签发一个一次性 MCP Access Ticket。已入网外部设备只能
   通过已认证 ISCP session 兑换它，以激活持久、经 owner 批准的 MCP Binding。ISCP ticket
   授予网络成员资格；SparkClaw ticket 授予 MCP 应用使用权。普通 MCP call 不使用任一 ticket。
5. 所有参与该方案的 provider 共用同一套 MCP、ISCP、Policy、approval、event 和 audit
   contract。SparkClaw 不新增 LocalMind 专用或按 provider 名称分支的访问路径。
6. MCP 保留自己的协议 adapter，但与 Web、微信、Telegram 和 Timer 共用受管理的业务链路。
   `tools/call` 进入统一接收层，经统一 Workflow/Policy 核心执行，并由已注册的通用 MCP
   sender/provider 通过统一结果发送层返回。

该目标适用于第三方访问 SparkClaw 的入站方向。现有 SparkClaw 访问 LocalMind workspace 的
出站集成仍是 MCP client 集成，不经过该 gateway。

JingSi 明确不在本设计范围内。它不实现该 MCP client，不为此 MCP path 接收 ISCP Pairing
Ticket，也不参与
MCP tool projection、invocation、result delivery、migration 或 acceptance。JingSi 后续将通过
另行设计的机制绑定 SparkClaw；在该方案获批并实现前，本项目保持 JingSi 当前所需 path 不变，
也不增加任何 JingSi 行为。

## 实现状态

SparkClaw 负责的本地 runtime 与 owner 管理面已经实现：

- 通用 `mcp` connector 默认关闭，并对 ticket 签发、认证 ingress、endpoint 可见性和结果
  delivery 全链路 fail closed；
- SparkClaw 签发短期、单次使用 MCP Access Ticket，只在创建响应返回一次 secret，仅持久化其
  SHA-256 digest，并且只能结合已认证 ISCP peer identity 原子兑换为持久、绑定设备的 MCP
  Binding；
- 严格协商 MCP `2025-06-18`，`tools/call` 返回标准 `CallToolResult`，不声明标准 Tasks，延迟
  状态只使用 Binding-scoped `sparkclaw.operation.*` tool；
- 投影 tool 以 confidence 1 选择唯一 Catalog 叶子，不运行 embedding 或 Tree 评分，然后进入
  现有 Message、Workflow、Policy、Store 和 Delivery 链路；external requester 与本地 executor
  identity 保持分离；
- memory、file 和 PostgreSQL Store 均实现 operation idempotency、有界 deadline、取消、审批
  恢复、Binding 撤销终止、终态 CAS、stale 叶子过滤、重启恢复和脱敏 MCP lifecycle audit；
- ISCP Bridge 识别 `sparkclaw.mcp.iscp.v1`，注入认证后的 Domain/device/key/session identity，
  并通过仅限 loopback 的 Gateway API，在已建立加密 ISCP session 中传输请求和响应。
- 默认关闭的 ISCP authority adapter 只通过带认证、超时和响应大小边界的出站调用请求标准
  `iscp.pairing_ticket.v2` 对象；WebChat 已提供 authority readiness、Pairing Ticket 单次展示、
  从 Catalog 派生的授权选择、独立 MCP Access Ticket 签发和 ticket/Binding 撤销；
- memory、file 和 PostgreSQL 只持久化非秘密 ISCP onboarding receipt；签名 Pairing Ticket
  只存在于创建响应，不进入列表、审计、重启恢复或公共配置表面。

该实现还不是可供外部生产接入的完整 onboarding path。已配置 ISCP authority 仍需提供并接入
用于把新 external gateway 加入 SparkClaw Domain 的确切 PairingTicket/Provisioning bootstrap；
可部署的外部 Access Gateway、真实 Relay 验证、LocalMind 切换和删除其旧 `agent.*.v1`
authorization 仍待完成。当前 ISCP 仓库定义了 Pairing Ticket 与加密原语，但没有生产 authority
HTTP endpoint，因此在真实 Domain authority 实现已配置 adapter contract 前不能声明 ready。
SparkClaw 不签名、校验、消费或持久化该协议 ticket，也不能用私有 credential 协议替代缺失的
authority 能力。JingSi 与 SparkClaw 主动访问 LocalMind 的出站 MCP client 保持不变。

仅在 ISCP 上线前的集成测试中，可以通过一个显式、默认关闭的 LAN transport，让同一可信
局域网中的 SparkClaw 与外部 MCP client 暂时用局域网替代 ISCP 可达性。它不模拟、也不宣称
具备 ISCP 安全性。SparkClaw MCP Access Ticket、原子兑换、持久 Binding、已授权 route leaf、
Workflow 执行、approval、operation、Message 与 Delivery contract 全部保持不变。LAN endpoint
必须进行路径隔离，测试结束后关闭，也不能被当作下文的生产 topology。

## 为什么同时需要 MCP 与 ISCP

MCP 定义能力表面：发现、输入/输出 schema、tool call、resource 和 progress。ISCP 定义可信
设备网络：同 Domain 设备 provisioning、peer identity、私钥持有证明、Trust Grant、Relay
可达性、端到端加密、防重放、credential rotation 和 transport revocation。SparkClaw 负责额外
的应用准入步骤，把一个已认证 ISCP device 转换成经 owner 批准的 MCP client。ISCP session
重连属于 transport 行为；MCP 业务操作恢复仍属于 MCP-over-ISCP 应用 contract。

两层不能互相替代：

- 仅发布一个公网 SparkClaw MCP endpoint 会要求直接暴露网络，并把长期授权退化为 bearer
  secret；
- 仅使用当前 `agent.*.v1` ISCP API 会继续保留第二套能力词汇，也不能通过标准工具协议暴露
  已注册的 Workflow 叶子 Catalog；
- 在已认证 ISCP session 上传输 MCP，可让外部产品共用标准工具接口，同时不公开本地
  Gateway。

## ISCP Domain 与授权方向

external Access Gateway 作为设备 provision 到 SparkClaw ISCP Domain 中。目标方案不依赖当前
ISCP 尚未实现的跨 Domain Trust Root federation。external gateway 与本地 SparkClaw gateway
使用同一 Domain 的 identity、Trust Grant、Relay credential、Hello/Ready 和
`SecureEnvelope`。

ISCP 负责 device admission 和加密 peer session 的粗粒度权限。SparkClaw 负责 MCP 应用准入：
它签发并消费自己的一次性 MCP Access Ticket，把已认证 ISCP device 绑定到本地 owner，并记录
允许的路由叶子、operation、effect 和 approval 权限。方向如下：

```text
SparkClaw owner
  -> 在本地 SparkClaw 发起 ISCP 配对
      -> SparkClaw 调用已配置的 ISCP pairing 能力
          -> SparkClaw 在本地展示一次性 ISCP Pairing Ticket
              -> owner 将其一次性交给 LocalMind / 其他 external gateway
                  -> external gateway 通过 ISCP Provisioning 兑换凭证
                      -> gateway 加入 SparkClaw ISCP Domain
                          -> ISCP 签发后续运行所需的协议凭证
                              -> 已认证端到端 ISCP session ready
                                  -> owner 选择 MCP capability 限制
                                      -> SparkClaw 签发一次性 MCP Access Ticket
                                          -> 已入网 device 通过 ISCP 兑换
                                              -> SparkClaw 激活持久 MCP Binding
```

LocalMind 或其他参与方案的 external controller 不得签发 enrollment bundle、bearer token、
Trust Grant 或其他对象，使自己加入 SparkClaw Domain 或自行获得 SparkClaw capability。ISCP
Trust Root 签署 Trust Grant，Relay 签发 access/refresh credential；SparkClaw 不签署这些协议
对象，也不实现平行的 ISCP claim 或 refresh endpoint。类似 `sparkclaw.peer.connect` 的粗粒度
ISCP permission 只允许加密 session，不授予 MCP discovery、invocation 或 Workflow 叶子；只有
独立 SparkClaw MCP Access Ticket 被消费并激活 MCP Binding 后，这些权限才生效。

当前 LocalMind Bridge path 的 bootstrap 方向相反：SparkClaw 生成 enrollment request，再等待
LocalMind controller 返回含 Relay credential 与 peer grant 的 `sparkclaw.bridge.enrollment.v1`。
这是旧链路的错误 trust direction，目标实现不得继承。既有 Bridge bundle、grant、refresh
credential 与 provider enrollment record 不能导入或转换为新 peer binding。所有现有
LocalMind Access Gateway 都必须使用新的 ISCP Pairing Ticket，重新加入 SparkClaw Domain，
随后还必须兑换新的 SparkClaw MCP Access Ticket。
本规则不决定 JingSi 未来如何绑定。

这不改变 SparkClaw 主动访问 LocalMind workspace 的出站 MCP client：此时被访问资源属于
LocalMind，LocalMind 仍可向 SparkClaw 签发访问 credential。两个方向必须使用相互独立的
binding 与 credential。

## 目标拓扑

```text
LocalMind / 其他兼容 MCP 的第三方
  -> 标准 MCP client
  -> 通用 ISCP MCP Access Gateway（外部角色）
      -> ISCP Relay（只路由，不接触业务明文）
          -> 通用 ISCP MCP Access Gateway（SparkClaw 角色）
              -> loopback 或 Unix socket Route MCP Service
                  -> MCP receive adapter -> MessageEnvelope
                      -> 已认证 tool-to-leaf binding
                          -> 确定性 Top-1 Catalog 叶子
                              -> 精确 Workflow Profile
                                  -> Workflow Runtime -> ToolHub -> Policy/Approval
                                      -> WorkflowResult -> DeliveryRequest
                                          -> MCP sender/provider
                                              -> 加密 MCP result/progress/operation status
```

Access Gateway 是同一个产品无关组件，具有外部和 SparkClaw 两种角色。它可作为 service 或
SDK sidecar 分发，但所有 provider 使用完全相同的 wire contract 与 identity 规则。端到端 ISCP
安全边界位于同一 ISCP Domain 中已 enrollment 的外部 gateway device 与 SparkClaw device
之间。Relay 只转发密文，不终止 MCP 业务 session。两个 gateway role 都通过出站连接访问
Relay，不开放 Route MCP 公网入站端口。

Route MCP Service 只允许本地访问，不得监听公网或 LAN。现有 Gateway、Catalog、Workflow
Runtime、Policy、Store、artifact 和 owner approval UI 继续保持权威。

MCP control traffic 留在专用协议 adapter 中，MCP business traffic 则使用统一受管理链路：

```text
Web / 微信 / Telegram / Timer
  -> Message Plane -> intent routing -> Workflow Core -> Delivery Plane

外部 MCP client
  -> MCP protocol adapter -> Message Plane -> bound-leaf routing
      -> Workflow Core -> Delivery Plane -> MCP sender/provider
```

`initialize`、`ping`、MCP Access Ticket 兑换、capability listing 和 SparkClaw operation
status/control 不会变成 message。合法 `tools/call` 会创建
`MessageEnvelope`、source endpoint、`ReturnRoute`、Workflow run、`WorkflowResult`、
`DeliveryRequest` 和 delivery receipt。MCP 专用 request/operation correlation 作为类型化 metadata
保存在这些共享记录旁，不另建一套业务 lifecycle。

## 组件职责

### Route MCP Service

Route MCP Service 是唯一的入站能力事实来源。它：

- 从已注册且可执行的 Catalog 叶子生成 MCP tool definition；
- 只暴露明确标记为允许远程投影的叶子；
- 把每个 MCP tool 绑定到一个 leaf ID 和一个 Workflow Profile revision；
- 在创建 run 前校验类型化 ingress schema；
- 把 `tools/call` 映射到统一 receive contract 和一个 provider-neutral `MessageEnvelope`；
- 通过 Message Runtime 启动精确 Workflow，绝不直接调用 ToolHub；
- 通过 Delivery Gateway 和已注册 MCP sender/provider 返回有界、provider-neutral 的 result、
  activity、approval 或 operation status；
- 不暴露原始 ToolHub registration、内部路径、secret、model prompt、未经投影的 observation 或
  未注册 runtime function。

MCP tool call 通过 tool identity 绑定叶子。本地 server 在认证 peer 并校验 grant 后，从当前
Catalog entry 生成类型化 binding；caller 不能通过自由文本或任意 argument 提供 leaf marker。
路由把 eligibility 限制为该唯一叶子，并记录为确定性 Top-1 evidence。SparkClaw 不对其他叶子
执行 embedding 或 Fast/Tree scoring，也不能静默改变请求的 capability。绑定叶子内部的确定性
资源 grounding、Workflow decision、Policy 和 finalization 继续正常执行。

### ISCP MCP Access Gateway

Access Gateway：

- 向外部产品提供标准 Streamable HTTP MCP；
- 把 MCP JSON-RPC request、response、progress 和 cancellation 映射到版本化 MCP-over-ISCP
  envelope contract；
- 只在 peer identity 与 Trust Grant 校验通过后完成 ISCP Hello/Ready；
- 只有该认证 device 已兑换有效 SparkClaw MCP Access Ticket 且 MCP Binding active，才允许 MCP
  discovery 与 call；
- 在转换中保留 MCP request ID、ISCP sequence、deadline、idempotency key、binding ID 和
  Catalog revision；
- 重连后恢复 read/status 流量，绝不盲目重放结果未知的 mutation；
- 转发前执行 request、response、concurrency 和 session 边界；
- 不包含 provider 名称分支或 provider 专用 capability map。

外部角色可运行在 LocalMind 或其他参与方案的 MCP client 旁。SparkClaw 角色运行在本地 Gateway
旁，只通过 loopback 或 Unix socket 访问 Route MCP Service。

### ISCP 基础设施

现有 ISCP 协议及其合规 Domain service 负责：

- Pairing Ticket 的签发、校验、过期、使用次数和消费；
- device identity 提交、私钥持有证明和同 Domain enrollment；
- Trust Root 授权，以及 Trust Grant 的签发、过期和撤销；
- Relay access/refresh credential 的签发和轮换；
- Hello/Ready session 建立、`SecureEnvelope`、防重放、密文路由和 transport revocation。

SparkClaw 通过 ISCP SDK 与 service API 使用这些保证，不新增 SparkClaw ISCP invitation format、
ISCP claim endpoint、Trust Grant signer、Relay credential issuer 或跨 Domain compatibility
path。这不禁止独立 SparkClaw MCP Access Ticket：它只由 SparkClaw 通过已认证 ISCP session
消费，绝不让 device 加入 Domain。所选
ISCP deployment 缺少必需能力时，应修复该 deployment 或上游 ISCP，SparkClaw 必须失败关闭。
ISCP 不授权 Workflow 叶子，Relay 也不能看到解密后的 MCP 参数或结果；叶子授权仍保存在
SparkClaw 本地 external-peer binding 中。

### 本地授权控制器

SparkClaw 负责 MCP 应用准入和授权，而不负责 ISCP protocol provisioning。owner-facing 控制面
调用已配置的 ISCP pairing 能力，并在本地展示 ISCP authority 生成的 Pairing Ticket。权威
ISCP enrollment 和 Hello/Ready 完成后，它再针对 owner 选择的 capability 限制创建短期、单次
使用的 MCP Access Ticket。SparkClaw 只保存该 secret 的 hash，通过已认证 ISCP session 校验并
原子消费它，将其绑定到该 session 的 device identity 与公钥 thumbprint，并激活持久 MCP
Binding。SparkClaw 还负责列出、收窄、暂停和撤销这些 Binding。它不定义、签署或校验 ISCP
ticket，也不提供公网 claim service。第三方 service 不能为自己增加叶子、改变 effect 边界或
批准自己的 pending action。

## 路由叶子投影

Catalog 继续作为用户可见能力的唯一 registry。MCP exposure 是投影，不是另一份手工维护的
tool list。

只有同时满足以下条件的叶子才会出现在 `tools/list`：

1. 叶子及精确 Workflow Profile revision 已注册且可执行；
2. 叶子具有版本化 remote ingress 与 result schema；
3. metadata 明确允许 remote MCP exposure；
4. 本地 owner 已向该 peer 授予该叶子及允许的 operation；
5. 所需 runtime provider 当前 ready；
6. 资源 contract 无需任意 host path、credential 或不受治理的二进制传输即可表达。

因此，可见 Catalog 是当前 runtime 能力、remote-exposure policy 与 peer grant 的交集。Catalog
或 grant revision 发生变化后，旧 listing 失效；caller 必须重新 list 后才能调用发生变化的
tool。

Catalog 变化本身不会自动扩大授权，也不会让全部 grant 同时失效。升级按已授权叶子分别处理：

- 新增无关叶子不会自动授权，也不影响已有 grant；
- 仅展示 metadata 变化时，重新 list 后可跟随当前 Catalog；
- provider 暂时不可用只返回 `unavailable`，不会把 grant 标记为 stale；
- 叶子被删除或不再允许 remote exposure 时立即隐藏并拒绝；
- input/output schema、operation、effect、approval rule、受治理资源 contract 或影响安全语义的
  Workflow Profile 变化时，只把该叶子的 grant 标记为 stale；
- stale 叶子保持隐藏和拒绝，直到本地 owner 审查新 contract 并创建新的 authorization revision。

任何影响安全语义的能力扩张都不得随 Catalog/Profile revision 自动生效。同一 MCP Binding 中
不相关的叶子继续可用。

tool 名称使用从 leaf ID 派生的稳定 provider-neutral namespace，例如
`sparkclaw.route.browser.internet_search`。provider 名称不得进入 tool 名称或 dispatch 逻辑。
tool definition 包含精确 leaf/profile revision、read/write effect、approval 行为和有界 schema。

并非每个现有叶子都自动适合远程使用。接收受治理本地资源的叶子必须使用 owner 授权的不透明
resource reference，不能接受外部 caller 提交的 host path。没有该 contract 的叶子在端到端
实现完成前保持不可见。

## 两个一次性 Ticket，随后持久绑定

目标方案有意使用两个不同的一次性凭证。它们的签发者、消费者、scope 和结果都不同，绝不能
统称为一个 token：

| 凭证 | 签发者与消费者 | 用途 | 持久结果 |
|---|---|---|---|
| ISCP `iscp.pairing_ticket.v2` | 由合规 ISCP Domain service 签发和消费 | 让 device 加入 ISCP Domain，并初始化认证端到端通道 | ISCP device membership、轮换协议 credential 和 Trust Grant |
| SparkClaw MCP Access Ticket | 由本地 SparkClaw 签发和消费 | 让一个已入网、已认证 ISCP device 激活经 owner 批准的 MCP 使用权 | 持久 SparkClaw MCP Binding |

两者都是短期、单次使用，均不是永久 bearer credential，普通 MCP discovery/invocation 也不携带
任一 ticket。“持久”描述产生的 device membership 与 MCP Binding，而不是任一 secret。

### ISCP Pairing Ticket 与 Provisioning

本地 owner 从 SparkClaw 发起 onboarding。SparkClaw 调用已配置 ISCP Domain 的 pairing 能力，
并在本地展示由此生成的已签名 Pairing Ticket，例如只允许复制一次的值或 QR 表示。owner 将该
凭证一次性交给外部设备。本地展示是 SparkClaw 的产品体验；协议签发仍属于 ISCP 操作。

标准 ISCP Pairing 与 Provisioning 负责 ticket format、签名、Domain/Relay/Trust Root 绑定、
过期、使用次数、私钥持有证明校验、消费和 device-bound provisioning material 投递。
SparkClaw 只保存非 secret ISCP onboarding transaction reference；它不保存可复用 ISCP ticket、
不自行校验 ISCP ticket，也不实现 ISCP claim endpoint。

external Access Gateway 在本地生成长期 device key，且不导出私钥。它连接 ISCP，并通过
Provisioning 提交 Pairing Ticket 与 ISCP Device Proof，以加入 SparkClaw Domain；它不直接连接
SparkClaw 的公网 enrollment endpoint。明确拒绝永不过期且可复用的 bearer token；正常连接
使用持久 device binding、轮换的 ISCP credential 与已认证 ISCP session。

### SparkClaw MCP Access Ticket

已入网 external gateway 与 SparkClaw 建立认证 ISCP session 后，本地 owner 选择 MCP
leaf/effect 限制。SparkClaw 创建密码学随机的 opaque MCP Access Ticket，在本地以 copy-once 值
或 QR 表示展示，并只持久化其 hash 及以下字段：

- ticket ID、owner ID、待生效 authorization revision 和预期 ISCP Domain；
- 已授权 leaf ID、operation、effect 和 approval permission；
- 签发时间、短期 expiry、`max_uses = 1` 和 pending/consumed/expired/revoked 状态。

external gateway 只能通过已认证、已加密 ISCP session 提交该 secret。SparkClaw 从 ISCP 推导
requester device ID、公钥 thumbprint、Domain ID 和 session identity，不能接受 caller 自行填写
这些字段。它校验 hash、expiry、状态、Domain 与待生效 owner authorization，并原子消费 ticket；
并发或重放兑换必须失败。SparkClaw 不暴露公网 MCP ticket claim 端口。

MCP Access Ticket 只授权创建有界 MCP Binding。它不能兑换 ISCP credential、不能让 device 加入
Domain、不能作为 MCP bearer authentication 重复使用，也不得保存在 log、trace、artifact 或最终
Binding 中。

### 持久 SparkClaw MCP Binding

MCP Access Ticket 成功兑换后，SparkClaw 激活持久 MCP Binding，其中包含：

- owner ID 与 SparkClaw ISCP Domain ID；
- 外部 device ID 与公钥 thumbprint；
- 已授权 leaf ID、operation、effect 和 approval permission；
- binding 与 Catalog revision；
- status、创建/使用时间和 revocation state。

该 Binding 不是 ISCP credential，也不取代 Trust Grant。只有当前 ISCP device/session 授权与
当前 SparkClaw MCP Binding 都通过时才接受 MCP call。Relay credential 与 Trust Grant 由各自的
ISCP authority 签发、轮换、续期和撤销；SparkClaw 可通过 ISCP API 请求 lifecycle action，但
不自行实现。

ISCP device membership 与 SparkClaw MCP Binding 可跨重启和普通 ISCP credential rotation
持续存在，直到显式撤销或影响安全语义的 leaf revision 变为 stale。新 external device 或丢失
device key 时，需要新的 ISCP Pairing Ticket 和新的 SparkClaw MCP Access Ticket。对同一认证
device 收窄或重新授权时创建新的 MCP authorization revision，绝不重新启用已消费 secret。

本地 owner 撤销 Binding 时，会原子地把该 Binding 的全部非终态 operation 标记为 `revoked`。
SparkClaw 同时取消仍在本进程运行的 execution context，并拒绝相应 pending approval；既有终态
保持不可变，迟到的 Workflow 或 delivery result 不能覆盖 revoked 状态。

## 配对流程

```text
1. owner 选择“添加外部 MCP client”，并启动 ISCP device pairing。
2. SparkClaw 记录非 secret ISCP onboarding reference，并调用已配置的 ISCP pairing 能力。
3. SparkClaw 在本地向 owner 展示由此生成的短期、一次性 ISCP Pairing Ticket。
4. owner 将 ticket 一次性交给 external generic Access Gateway；gateway 在本地创建设备密钥。
5. gateway 连接 ISCP，使用标准 Provisioning 与 Device Proof 兑换 ticket，并加入 SparkClaw
   Domain。
6. ISCP 校验并消费 ticket、准入 device，并由 Trust Root 与 Relay 签发各自的协议凭证。
7. 两端 device 通过出站连接访问 ISCP Relay 并完成 Hello/Ready；后续 onboarding 和 MCP 流量
   端到端加密。
8. owner 选择 MCP leaf/effect 限制。SparkClaw 创建短期、单次使用 MCP Access Ticket 并在
   本地展示。
9. owner 将该独立 ticket 一次性交给已入网 external gateway。
10. gateway 通过已认证 ISCP session 兑换。SparkClaw 原子消费 ticket，并把 session 已证明的
    device identity 与公钥 thumbprint 绑定到待生效 owner authorization。
11. SparkClaw 激活持久 MCP Binding。普通 `tools/list` 和 `tools/call` 使用 ISCP session identity
    加该 Binding，绝不使用任一一次性 ticket。
12. MCP Binding 保持 active 时，ISCP 轮换其协议 credential。
```

ISCP enrollment 失败时不得兑换 MCP Access Ticket。若 ISCP enrollment 成功但 MCP 兑换或
Binding 激活失败，该 device 没有 SparkClaw 叶子授权，所有 MCP discovery/invocation 都失败
关闭。ISCP 拒绝其 Pairing Ticket 的重放和竞态；SparkClaw 分别拒绝 MCP Access Ticket 的重放、
过期、撤销、identity 替换或竞态。

## MCP 渠道契约

MCP adapter 先区分协议控制流量与业务流量，再使用统一接收和发送层：

| MCP operation | SparkClaw 行为 |
|---|---|
| `initialize`、`ping`、session negotiation | 仅 transport control；不创建业务消息或 Workflow run |
| `tools/list` | 返回当前按 peer 过滤的 remote Catalog 投影 |
| 投影 Route tool 的 `tools/call` | 创建一个 message ingress，并执行其绑定 Workflow 叶子 |
| progress 与 MCP request cancellation | 当前 MCP exchange 仍 active 时观察或取消 in-flight request |
| `sparkclaw.operation.get` | 返回当前 MCP Binding 所属一个 operation 的持久状态 |
| `sparkclaw.operation.result` | operation 可用时返回其有界 terminal result |
| `sparkclaw.operation.cancel` | 请求取消 operation，不表示删除或保证 rollback |
| `resources/list` | 返回明确投影 resource 的有界 metadata |
| 受保护 `resources/read` | 只通过已授权 remote read leaf 执行；绝不直接读取本地 storage |

三个保留的 `sparkclaw.operation.*` tool 是初始 MCP 版本的应用层 control tool。它们不是 Route
Catalog 叶子，不执行 semantic 或 bound-leaf routing，也不创建新业务 message 或 Workflow。
adapter 按当前 MCP Binding 与既有 operation record 授权。其 schema 属于一套版本化 adapter
contract，不是第二套用户能力 registry。

MCP adapter 负责 protocol session negotiation、request ID、progress、当前 request cancellation
和 operation-control 转换。共享链路负责本地执行授权、requester provenance、message/run
持久化、idempotency、Workflow state、Policy、approval、result state、endpoint 解析、发送尝试
和 audit。MCP 是注册的通用第三方 channel type，不是按产品名称分支。

为便于统一管理，MCP 在现有第三方控制面中注册一个通用 channel definition。`ConnectorSetting`
或其后续统一设置负责启用/暂停、Endpoint Registry 可见性、入站访问和 outbound Provider 可用性；
持久 peer binding 只代表已配对授权，不能隐式启用渠道。MCP 不需要复用 polling、账号登录等不适用
的 connector 内部实现，复用的是统一管理 contract 和业务 lifecycle。

每次 `tools/call` 都创建 source endpoint 和规范化 message ingress：

| 共享字段 | MCP 映射 |
|---|---|
| source kind | `third_party_device` |
| adapter/provider key | 稳定值 `mcp`，绝不使用 `localmind` 等参与产品名称 |
| source endpoint | 根据已认证 peer binding 派生的 owner-scoped endpoint |
| native message/thread | MCP request ID 及已认证 MCP session/operation correlation |
| owner | 从持久 MCP Binding 解析的本地 owner |
| actor/authorization principal | 本地 SparkClaw execution principal；绝不是外部 ISCP device |
| requester | 单独保存在 `MCPInvocationContext` 的已认证外部 ISCP device |
| content | 已校验 MCP argument 与受治理 resource reference 的有界类型化投影 |
| return route | 冻结到同一个来源 MCP endpoint |
| idempotency | peer binding 与 caller idempotency key 的组合 |

共享 message/run 记录旁持久化一个版本化 `MCPInvocationContext`，至少包含：

```text
MCP request/session ID + deferred 时的 SparkClaw operation ID
requester device ID/public-key thumbprint + durable MCP Binding/revision
local owner ID + local SparkClaw execution principal
tool name + leaf ID + Workflow/Profile revision
Catalog revision + allowed operation/effect
validated argument digest + bounded arguments/resource refs
idempotency key + deadline
message/run/delivery IDs
```

结构化 MCP argument 始终保持结构化。它们按发布 schema 校验，保存在类型化 MCP context 中，
并绑定到 Workflow input，不压成虚构的自然语言 owner message。任意远端 host path 和未解析
本地 resource name 都被拒绝。

external gateway 是已认证 requester/source，不是执行 SparkClaw capability 的 actor。它提交有界
请求；SparkClaw 本地 execution principal 在 owner 与 MCP Binding 限制下创建并执行 Workflow。
requester device identity 仍是 authorization、idempotency、revocation、source reply 和 audit 的
必需 provenance，但绝不能被提升为本地 owner 或 SparkClaw execution principal。

因此，MCP source reply 使用专门的 Binding-aware 授权规则。冻结 `ReturnRoute` 必须解析到原始
MCP source endpoint；该 endpoint 的 `OwnerID` 与 `BindingRef` 必须分别匹配 result owner 与当前
MCP Binding；其 requester device identity 必须同时匹配 invocation context 和当前 ISCP session
认证出的 device。本地 execution principal 仍须对该 owner 有效授权。MCP 不复用要求 requester
device、owner 和 actor 相等的 same-principal 规则，实现也不得全局放宽其他第三方 channel 现有的
source-reply 规则。

## 确定性 Top-1 叶子选择

MCP `tools/call` 已经命名 SparkClaw 发布的功能。开放式 intent recognition 只会增加歧义，且
可能偏离已发布 contract。因此，统一 router 接收来自已认证 MCP receive adapter 的服务端 binding
signal，并按以下方式选择叶子：

1. 在当前按 grant 过滤的 remote Catalog 中解析 tool name。
2. 重新校验 peer binding、Catalog/Profile revision、schema、operation、effect 和 runtime
   readiness。
3. 将 routing graph 与绑定叶子取交集，必须精确剩下一个 candidate。
4. 记录 `mcp_tool_binding=1.0` 类型化确定性 selection signal；它不是伪造的 embedding 或 Tree
   semantic score。
5. 将 candidate 持久化为 Top-1，并实例化其精确 Workflow Profile。

不执行 embedding 或 Fast/Tree route-scoring call。route trace 仍记录 binding source、Catalog
与 binding revision、candidate、reason code 和语义 channel 被有意 bypass 的事实。binding stale、
已撤销、未授权、不可用或不唯一时显式失败；绝不回退到自然语言 routing 或其他 leaf。

## 调用流程

```text
1. 外部 MCP client 通过通用 Access Gateway 调用 tools/list。
2. SparkClaw 只返回当前 grant 过滤后的叶子投影。
3. client 使用 idempotency key 和 deadline 调用一个精确叶子 tool。
4. 两端 gateway 把 MCP request 放入 ISCP SecureEnvelope。
5. SparkClaw 认证 peer，并重新校验 binding、grant、Catalog revision、schema、effect 和
   runtime readiness。
6. MCP receive adapter 创建一个 `third_party_device` `MessageEnvelope`、source MCP endpoint、
   source `ReturnRoute` 和服务端持有的 tool-to-leaf binding。
7. bound-leaf routing 在不执行 embedding 或 Tree scoring 的情况下将该 candidate 选为确定性
   Top-1。
8. Message Runtime 启动精确 Workflow Profile。本地 Policy 执行 read，或把 effect 停放到
   本地 owner approval。
9. waiting 或 terminal `WorkflowResult` 成为发送到冻结来源 MCP endpoint 的
   `DeliveryRequest`。
10. Delivery Gateway 调用 MCP sender/provider，把结果投影为即时 MCP result、progress
    notification 或 SparkClaw operation handle/status，并通过加密 ISCP session 返回。
```

外部内容始终是不可信输入。一次调用的结果不能扩展 grant、选择另一个叶子、授权 mutation 或
解决自身 approval。

## MCP 结果 Sender

统一发送层负责 result lifecycle，并为来源 endpoint 调用已注册、provider-neutral 的 MCP
sender/provider。该 adapter 把受治理 `WorkflowResult` 与 delivery state 映射为：

- 有界已完成操作的即时 MCP `tools/call` result；
- 运行中操作的 progress notification；
- deferred running 或 `approval_pending` operation 的应用层 operation handle；
- 通过 `sparkclaw.operation.get` 返回 completed、blocked、failed、canceled、revoked 或
  unknown-outcome operation state；
- 有界 content、structured data、受治理 resource handle 和 reference。

MCP sender/provider 是 Delivery Gateway 的一部分；Workflow Runtime 绝不直接写 MCP stream。
它使用原始 MCP request ID 和 SparkClaw operation ID 关联 delivery，并记录类型化 delivery
receipt。断连不会把 MCP result 改送 Web、微信或其他 connector。结果保持绑定来源 MCP
endpoint，并可通过 Binding-scoped SparkClaw operation contract 恢复。只有确认未送达的结果
才允许重试。

发起调用的 peer 不能批准自身 pending effect，除非另有显式 owner-device approval grant。
approval 继续进入本地持久 owner inbox；approval 解决后，同一个 external invocation 继续，
统一发送层为同一 endpoint 与 Binding 持久化新的 operation state。

## MCP-Over-ISCP Contract

实现必须为 ISCP 内的 MCP 流量定义一套版本化 schema，至少覆盖：

- initialization 与协商出的 MCP/protocol version；
- `tools/list` 与 `tools/call`；
- cancellation 与 progress notification；
- 保留的 `sparkclaw.operation.get`、`sparkclaw.operation.result` 和
  `sparkclaw.operation.cancel` control tool；
- 仅面向明确投影 resource 的有界 resource list/read；
- request ID、session ID、peer binding ID、idempotency key、deadline、Catalog revision 和
  operation ID；
- terminal result、approval-pending、retryable failure、revoked、stale-catalog 和
  unknown-outcome 状态；
- 重连后的 Binding-scoped operation-status/result 恢复。

第一版固定 MCP `2025-06-18`，与 SparkClaw 当前 client 实现一致。该协议版本没有标准 MCP
Tasks API，因此第一版不得声明或发出 `tasks/get`、`tasks/result`、`tasks/cancel`、task-augmented
`tools/call`，也不得把 proprietary frame 宣称成标准 MCP Tasks。持久 approval、reconnect 和
restart recovery 使用上述 SparkClaw operation tool。未来实现可协商双方支持且具备 Tasks 的
更新 MCP 版本，并把同一内部 operation record 映射到标准 Tasks；协议协商必须显式失败，不能
静默降级。

read-only call 只有在确定操作尚未执行时才能重试。mutation 在 timeout 或认证刷新后绝不自动
重放；结果不确定时，必须先通过 operation ID 查询后才能发起新调用。

## Policy、Approval 与数据边界

- 已认证 ISCP peer 是 external requester/source。SparkClaw 是 executor，MCP Binding 决定本地
  owner、本地执行授权和允许的叶子。
- requester device identity 保存在 source endpoint 与 `MCPInvocationContext`，不得复制进本地
  `ActorID` 或用来冒充 owner。
- listing permission 和 invocation permission 与 approval resolution permission 相互独立。
- reversible 与 dangerous effect 继续通过 SparkClaw Policy 和本地持久 approval inbox。
- 默认情况下，发起调用的第三方不能批准自己的 action。未来 owner-device approval 能力需要
  独立显式 grant，不能从 MCP access 推导。
- result 只暴露 consumer-sized 投影。local path、secret、raw credential、hidden prompt、
  private trace 和无界 artifact 均留在本地。
- 每次 list、invocation、denial、approval、result、reconnect、grant change 和 revocation 都
  记录 requester device、本地 execution principal、Binding revision、leaf/profile revision、
  operation ID 与 outcome，但不记录 secret material。
- revocation 立即移除 peer 的有效 Catalog、拒绝新 call、关闭 active session，并禁止 credential
  renewal。

## 当前边界与目标边界

| 范围 | 当前实现 | 目标设计 |
|---|---|---|
| 第三方入站 API | legacy `agent.*.v1` 加已实现的本地 Route MCP dispatch | 通过已部署外部 gateway 提供 provider-neutral Route MCP Service |
| Runtime plane | MCP receive adapter 已进入统一 Message/Workflow 链路 | 使用同一链路完成真实 external Relay 验证 |
| 能力来源 | 已实现已注册 Catalog 叶子的过滤投影 | 无 provider 分支的 owner-managed remote Catalog 扩展 |
| 参与方案的集成 | LocalMind 仍使用 legacy path | 一个通用 Access Gateway，无 provider 分支 |
| 渠道管理 | 通用 `mcp` setting 与 External MCP owner UI 已控制 pairing、ticket 签发、ingress、endpoint 可见性、sender 可用性和撤销 | 针对已部署 authority 与 external gateway 的实机运行 |
| 凭证权威 | LocalMind controller 返回其 legacy Bridge path 使用的 bundle | ISCP authority 签发其 Pairing Ticket、Trust Grant 和 Relay credential；SparkClaw 独立签发并消费 MCP Access Ticket |
| Enrollment | 外部提供 enrollment bundle | 外部 device 先兑换 ISCP Pairing Ticket 加入 Domain，再通过该认证 session 兑换 SparkClaw MCP Access Ticket |
| 长期授权 | 有期限 bundle 与 Trust Grant | ISCP credential lifecycle + 独立持久、owner 批准的 SparkClaw MCP Binding；两个一次性 ticket 都不复用 |
| 网络暴露 | 独立 Bridge 连接 Relay | 同样只出站连接 ISCP；本地 MCP 保持私有 |
| 结果返回 | 通用 MCP sender/provider 与持久 operation tool 已在本地实现 | 真实 external reconnect 与 Relay 结果恢复验证 |
| SparkClaw -> LocalMind | workspace-scoped MCP client | 不变 |
| JingSi | 当前 path 等待独立替代设计 | 本设计不处理；没有 MCP 配对、tool、sender、migration 或 acceptance 依赖 |

当前 LocalMind 使用 ISCP Bridge 和 `agent.*.v1` 入站访问 SparkClaw 的方式，是本设计要替换的
旧链路。它只在 LocalMind 通过通用 MCP path 完成验证和切换前保持可执行，也不得再增加
LocalMind caller 或 capability。JingSi 当前仍需的共享 Bridge code 不由本设计迁移，只保留
当前 JingSi 运行所需的最小冻结 surface。迁移期间，文档必须区分已实现的本地 runtime 阶段与
仍不可用的生产外部 onboarding path。

## 强制删除 LocalMind 旧链路

对 LocalMind 而言，目标是替换，不是永久双轨。LocalMind 通过新链路完成端到端验证后，只有
删除其旧入站 capability path，才算实现完成。若下列 component 与 JingSi 共用，则删除
LocalMind registration、authorization、manifest entry 和 dispatch branch，只保留当前 JingSi
运行所需的最小冻结 surface。LocalMind 删除范围包括：

- LocalMind peer identity、provider enrollment、外部签发 grant、Relay refresh material 与
  LocalMind-specific Bridge config；
- 已被 MCP channel 替代的 LocalMind capability-manifest entry 与 `agent.*.v1` notification、
  conversation、approval、event 或 status path；
- 仅由 LocalMind 使用的 Gateway dispatch 和 `internal/iscpbridge` branch，以及对应 schema、
  mock、test、deployment instruction 与 health check；
- 通用 MCP channel 不再使用的 LocalMind fallback 与 provider-name branch；
- 当前状态合并进通用 MCP/ISCP 指南后的 LocalMind legacy guidance。

如果 JingSi 仍依赖完整 `cmd/iscp-bridge`、共享 Gateway handler、schema 或 config，不能仅因
LocalMind 已迁移就删除它们；反过来，JingSi 的临时依赖也不能成为隐藏保留 LocalMind fallback
的理由。剩余 Bridge 与 `agent.*.v1` surface 的彻底删除属于 JingSi 未来独立绑定项目，不属于
本项目。

LocalMind 切换时先禁止新的 legacy LocalMind ingress，再让已经 acknowledge 的 LocalMind
operation 完成或进入显式 terminal state，撤销其旧外部签发 bundle 与 Relay refresh material，
随后删除 LocalMind-only code 与 registration。历史 message、notification、run、receipt 和
audit record 可以按原 ID 保持只读，不得因此为 LocalMind 保留旧 transport 或 API。系统不提供
LocalMind 自动 credential migration、protocol fallback 或跨协议 resume。

## 实现顺序

1. 定义 Catalog remote-exposure metadata 与版本化 ingress/result schema，不能新增第二个
   capability registry。
2. 在共享 message/run contract 中定义 MCP receive mapping、类型化 invocation context、
   tool-to-leaf binding、与本地 execution principal 分离的 requester provenance、确定性 Top-1
   evidence、source endpoint 和 return route。
3. 针对精确 Workflow 叶子、Policy、approval、全部 Store backend 和统一 result projection，
   实现并测试仅本地 Route MCP Service。
4. 注册通用 MCP channel definition 和 sender/provider，使其启用/暂停 gate 独立于 peer
   binding，并覆盖 `WorkflowResult` -> `DeliveryRequest` -> MCP result/progress/SparkClaw
   operation 和 receipt 语义。
5. 集成标准 ISCP PairingTicket/Provisioning confirmation；SparkClaw 不实现 ISCP ticket 的签名、
   校验、消费或 protocol credential 签发。
6. 在所有 Store backend 中实现独立 opaque SparkClaw MCP Access Ticket、只存 hash 的持久化、
   仅允许已认证 ISCP session 原子兑换、持久 MCP Binding、撤销和 owner 控制面。
7. 定义并测试 MCP-over-ISCP schema、通用 Access Gateway、Relay 重连、idempotency 和
   unknown-outcome 行为。
8. 完成 external gateway -> Relay -> MCP receive -> Message Plane -> Workflow Core ->
   Delivery Gateway -> MCP sender 端到端测试，覆盖 read、approval-pending mutation、重连和撤销。
9. 在 SparkClaw 本地展示新的 ISCP Pairing Ticket，将其一次性交给 LocalMind Access Gateway，
   再把该 gateway 作为新 device 加入 SparkClaw ISCP Domain；随后通过该认证 ISCP session
   独立展示并兑换新的 SparkClaw MCP Access Ticket。激活 owner 批准的 MCP Binding 后迁移到
   通用 gateway；不增加 provider-specific server code，也不导入旧 enrollment bundle 或 grant。
10. 禁止 legacy LocalMind ingress，让其已 acknowledge work 完成或显式 terminalize，并要求
   LocalMind 只通过新链路通过 discovery、read、approval-pending mutation、reconnect 和
   revocation 验证。
11. 删除上述 LocalMind legacy surface，并证明不存在 LocalMind fallback。保持 JingSi 不变，
    剩余 JingSi 所需 Bridge surface 的删除延后到其独立绑定项目。

## 验收标准

- 新第三方接入无需修改 SparkClaw 代码，也无需增加按 provider 名称分支的配置。
- 只有已配置的 ISCP authority 可以签发协议 credential 与 Trust Grant。external controller
  不能自行加入 SparkClaw ISCP Domain；只有本地 SparkClaw owner 能激活叶子/effect 授权。
- SparkClaw 在本地展示 ISCP authority 的短期 Pairing Ticket，owner 只传递一次。认证 ISCP
  session ready 后，SparkClaw 再展示独立短期 MCP Access Ticket，owner 也只传递一次。产生的
  device membership 与 MCP Binding 持续到撤销或 stale；普通使用不复用任一 secret。
- Relay operator 无法解密 MCP 参数或结果。
- `tools/list` 精确等于当前 Catalog、remote-exposure policy、peer grant 与 runtime readiness 的
  交集。
- 每次 `tools/call` 精确创建一个 message ingress，在不执行语义评分的情况下选择一个
  server-bound Top-1 叶子，并且只通过 Delivery Gateway 向来源 MCP endpoint 尝试发送一次。
- MCP control operation 不创建业务消息；MCP business call 与 result 复用统一
  message/run/delivery ownership 和 audit record。
- 第一版协商 MCP `2025-06-18`，不声明标准 MCP Tasks；deferred approval、reconnect 和 restart
  状态只通过 Binding-scoped `sparkclaw.operation.*` tool 恢复。
- 外部 ISCP device 保留为 requester/source provenance，本地 SparkClaw principal 仍是 Workflow
  executor；两者都不得冒充本地 owner。
- 一个通用 `mcp` channel setting 管理 ingress、endpoint 可见性和 sender 可用性；完成配对或
  保留 peer binding 绝不表示渠道已启用。
- 外部 caller 不能调用未列出的叶子、通过输入或输出扩大 scope、使用任意本地路径，或批准
  自己的 effect。
- peer 撤销后失去 discovery、invocation、event resume 和 renewal 权限；该 Binding 下所有
  running 或 approval-required operation 进入不可覆盖的 `revoked` 终态。
- mutation timeout/reconnect 绝不造成自动重复执行。
- file 与 PostgreSQL state backend 对非 secret onboarding reference、只存 hash 的 pending MCP
  ticket、Binding、idempotency、audit、原子消费和 revocation 保持相同语义；两者都不保存或
  把任一 Ticket 当作可复用 credential。
- SparkClaw 在本地展示新的 ISCP Pairing Ticket，LocalMind 通过 ISCP Provisioning 兑换并加入
  SparkClaw Domain，再通过该认证 session 兑换新的 SparkClaw MCP Access Ticket 后通过目标
  链路；其旧外部签发 enrollment material 必须被拒绝。
- 影响安全语义的 leaf/Profile contract 变化时，只将该叶子 grant 标记 stale 并停止使用，直到
  owner 重新授权；既不自动扩大授权，也不禁用无关已授权叶子。
- 即使共享 Bridge component 因 JingSi 暂时保留，切换后的 production source、config、schema、
  image 和 active documentation 也不存在 LocalMind inbound fallback。
- 本设计不会为 JingSi 创建 MCP channel setting、用于该路径的 Pairing Ticket、peer binding、
  projected tool、endpoint、sender、migration step 或 acceptance dependency。

## 待实现评审决定

本设计固定 trust 与 ownership 模型，但以下部署选择留待实现评审：

- 外部 gateway 以 standalone sidecar、SDK component 或两者同时提供；
- 已配置 adapter 获取 Pairing Ticket、未 enrollment external gateway 兑换该 ticket 所使用的
  精确生产 authority 与 ISCP Provisioning/Relay bootstrap endpoint；
- active durable binding 的 Trust Grant 有效期与自动续签周期；
- 第一批资源 contract 足够安全、可远程暴露的叶子集合。
