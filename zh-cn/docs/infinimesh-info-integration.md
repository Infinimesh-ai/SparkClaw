# Infinimesh Info Web Search 接入

> 语言：[English](../../docs/infinimesh-info-integration.md) | 简体中文

本文定义 SparkClaw 如何用已部署的 Infinimesh Info 服务取代旧 Parallel Free
Web Search 引擎，同时保持 Agent 侧 `web.search` 契约稳定。本文也是本次迁移的实现
与验收契约。

## 范围与边界

本次接入仅包括：

- 新增 `internal/infinimeshinfo` HTTP 客户端和纯内存 token wallet；
- 复用现有 `internal/websearch` 适配边界；
- 调整 ToolHub `web.search` 适配、配置、公开配置状态、doctor 和聚焦测试；
- 只有在生产云端凭据门控 smoke 通过后，才移除旧 Parallel Free 适配器。

Agent 可见工具名保持 `web.search`。现有输入字段继续为 `query`、`max_results`
和 `freshness`；主要输出字段继续为 `query`、`answer`、`provider`、`count`、
`results`、`citations`、`took_ms` 和 `untrusted`。`browser.read` 及全部 browser
automation 工具不变。本次不接入异步深度研究 API。

ToolHub 已根据加载后的配置自行构造 Web Search adapter，因此无需修改
`main.go`、`App.tsx` 或 store backend。后续若确需修改这些文件，必须先在本文中
补充论证。

## 运行时架构

```text
Agent / ToolHub
  -> web.search
     -> internal/websearch adapter
        -> internal/infinimeshinfo.Client
           -> 内存 TokenWallet
              -> POST /v1/info/tokens/issue
           -> POST /v1/info/query
```

`internal/infinimeshinfo` 负责云端请求/响应类型、认证头、token 生命周期、重试
分类、request ID、响应大小限制和脱敏错误。`internal/websearch` 只负责稳定
SparkClaw 契约与 Infinimesh Info 契约之间的映射。ToolHub 继续暴露稳定工具定义，
并将所有返回证据标记为不可信。

首个生产路径使用 `token_mode=internal_opaque`。Wallet API 按 token 类型设计，
使未来 VOPRF 实现可以替换签发与 finalize 逻辑而不改变 `web.search`；但本次只
实现并生产调用同步 `/v1/info/query` 所需的 `info.basic` 路径，不加入无调用的
其他 token 类型实现。

## 云端 API 契约

生产 Base URL 为 `https://info.infinimesh.cn`。Base URL 可配置，以支持 contract
测试和私有部署；生产 smoke 除非显式覆盖，否则必须访问已部署 HTTPS 地址。

### Token 领取

SparkClaw 调用：

```http
POST /v1/info/tokens/issue
Authorization: Bearer <client-entitlement-proof>
Content-Type: application/json
Accept: application/json
X-Request-Id: <random ID>
```

首轮请求为：

```json
{
  "device_attestation": "<injected proof>",
  "license_proof": "<injected proof>",
  "epoch": "<current UTC date>",
  "token_mode": "internal_opaque",
  "requested_tokens": [
    {"type": "info.basic", "count": "<configured batch size>"}
  ],
  "blinded_token_requests": []
}
```

只有类型为 `info.basic`、模式为 `internal_opaque` 且 `expires_at` 有效并晚于当前
时间的 token 才能进入 wallet。成功响应为空或格式不兼容时必须报错，不得作为
“部分配置成功”继续运行。

### 同步查询

SparkClaw 调用：

```http
POST /v1/info/query
Authorization: PrivateToken <one reserved anonymous token>
Content-Type: application/json
Accept: application/json
X-Request-Id: <random ID>
```

请求映射为：

```json
{
  "request_id": "<random ID>",
  "product": "sparkclaw",
  "task_type": "general_research",
  "query": "<web.search query>",
  "context_policy": {
    "include_private_context": false,
    "local_context_summary": null
  },
  "requirements": {
    "freshness": "<high|medium|low>",
    "citation_required": true,
    "max_sources": "<bounded max_results>",
    "language": "<configured default>",
    "response_mode": "agent_context"
  }
}
```

本版本将 `include_private_context` 固定为 `false`。Adapter 不接受私有上下文参数，
也不会转发 session、user、device、workspace 或稳定客户端标识。每次 HTTP 尝试
都从密码学随机源生成 request ID；它不从 SparkClaw session、run、user、device、
query、时间戳或主机名派生。

## Token Wallet 状态机

Wallet 仅存在于当前进程内存中。Token 不会序列化进 SparkClaw 配置、状态后端、
trace、artifact、日志或测试 fixture。

```text
EMPTY --reserve--> ISSUING --issue 成功--> AVAILABLE
  ^                    |                       |
  |                    +--issue 失败-----------+
  |                                            |
  +--清理过期 token---------- AVAILABLE --reserve--> DESTROYED
```

实现规则如下：

1. `Reserve(ctx, info.basic)` 是取得 token 的唯一方式。
2. mutex 保护 token slice 与 issuance 状态；最多一个 goroutine 领取新批次，其他
   并发调用等待同一领取结果。
3. 每次 reserve 前清理过期 token。
4. Reserve 原子地从 wallet 移除且只移除一枚 token；不存在 release、return、peek
   或 reuse 操作。
5. Token 一旦 reserve 即视为销毁，即使后续 JSON 编码、构造请求、transport、
   timeout、cancel 或响应解析失败，也不得放回。这一保守规则直接证明 HTTP 重试
   不可能复用 token。
6. 每次重试必须 reserve 不同 token，并生成不同的随机 `request_id`。
7. 进程退出后剩余 token 随内存消失。首版明确不持久化，也不接系统凭据库。

批量领取避免每次搜索都访问 Entitlement 服务。批量大小有上限且可配置。Wallet
接口按 token 类型设计，是为未来 `info.news`、`info.verify` 和 VOPRF 留出演进点，
但本次不包含这些未被生产路径调用的实现。

## 错误与重试语义

客户端把 Infinimesh Info 通用错误 envelope 解码为类型化、已脱敏错误，仅包含
HTTP status、code、retryable 和 message。错误字符串不得包含认证头、proof、
响应原文或完整 query。

查询尝试次数有明确上限。以下情况可做指数退避与 jitter 重试，且必须先销毁上一枚
token：

- 尝试开始后的 transport error 和客户端 timeout；
- `408 REQUEST_TIMEOUT`；
- `429 RATE_LIMITED`；
- `500 INTERNAL_ERROR`、`502 UPSTREAM_ERROR`、`503 SERVICE_DEGRADED`；
- 其他响应中显式标记 `retryable=true` 的错误。

`TOKEN_INVALID`、`TOKEN_EXPIRED` 和 `TOKEN_REDEEMED` 可使用新 token 做一次有界
恢复；wallet 批次过期时先清理并重新领取。`INVALID_REQUEST`、`QUOTA_EXCEEDED`
和 `POLICY_DENIED` 不重试。Context cancel 会立即终止 backoff 和领取。

Token issue 在发生结果不明的 timeout 后不做静默自动重试，因为丢失一个实际成功
返回的批次后再次领取，可能重复消耗额度。后续调用可以重新开始一次领取操作。
SparkClaw 在领取或查询失败时绝不自动回退到其他 Web Search provider。

## 配置与凭据注入

非敏感默认值进入 SparkClaw 配置：

- provider：`infinimesh-info`；
- Base URL：`https://info.infinimesh.cn`；
- token 批量大小；
- 最大查询尝试次数与重试基础延迟；
- 请求 timeout 与响应 body 上限；
- 默认语言与最大来源数量。

三个生产 proof 不能从 JSON 加载，只允许直接环境变量或由环境变量指定的文件注入：

| Secret | 直接环境变量 | 文件环境变量 |
|---|---|---|
| entitlement proof | `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF` | `SPARKCLAW_INFINIMESH_INFO_ENTITLEMENT_PROOF_FILE` |
| device attestation | `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION` | `SPARKCLAW_INFINIMESH_INFO_DEVICE_ATTESTATION_FILE` |
| license proof | `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF` | `SPARKCLAW_INFINIMESH_INFO_LICENSE_PROOF_FILE` |

直接值优先于文件引用。文件内容读取到内存后 trim；文件路径不对外暴露；显式配置但
无法读取的文件会令 config load 失败。任何已提交示例都不得包含真实 proof。

`GET /api/config` 对 Infinimesh Info Web Search 只暴露 `enabled`、`provider` 和一个
布尔 `configured` 状态，不暴露 proof、proof 文件路径、token 数量、匿名 token、
header 或 token 响应元数据。Doctor 只报告 missing/configured，不得把值读回 stdout。

## 隐私边界

SparkClaw 只向 `/v1/info/tokens/issue` 发送 entitlement proof、device attestation
和 license proof；只向 `/v1/info/query` 发送单枚匿名 token 与业务 query。查询
请求绝不包含三个签发 proof，也不包含 SparkClaw session/user/device 标识。

客户端 package 不依赖 logger，也不创建任何持久化依赖。它不记录 token、proof、
认证头、完整请求 body 或完整 query。脱敏错误只使用 endpoint 名称和错误码。测试
通过 sentinel 断言 query 请求不含签发凭据，并断言公开配置不含任何 secret。

此边界不宣称绝对网络匿名：直连 HTTPS 仍会向已部署服务暴露网络元数据。它实现
权威文档中的匿名授权分离；OHTTP/VOPRF 留给未来独立评审的变更。

## 输出映射

`internal/websearch` adapter 按下表映射成功的 agent-context 响应：

| Infinimesh Info | 现有 `web.search` 输出 |
|---|---|
| `answer_context.summary` | `answer` |
| `sources[].title` | `results[].title` |
| `sources[].url` | `results[].url` |
| 有界拼接 `sources[].snippets` | `results[].snippet` |
| `sources[].source_type` | `results[].source` |
| `sources[].published_at` | `results[].published_at` |
| `answer_context.key_facts[].sources` 引用的 URL | `citations` |
| 响应 source 数量 | `count` |
| 客户端耗时 | `took_ms` |
| 常量 `infinimesh-info` | `provider` |
| 常量 `true` | `untrusted` |

Citation ID 通过 `sources[].id` 解析，按响应顺序去重，并转换成 source URL，因为
现有 SparkClaw citation 契约为 `[]string`。若响应没有被引用的 ID，则 citations
回退为全部有效 source URL。无效 URL 或不完整 source 不得导致 adapter 崩溃；它们
在请求结果上限内被忽略。只有存在有效来源证据时才允许 summary 为空，此时 adapter
可生成简短的证据导向 answer，但不得编造事实。

## 旧引擎迁移

生产 smoke 测试通过后，迁移已按门禁完成：

1. 新增并 contract-test `internal/infinimeshinfo` 与 `websearch` 映射。
2. 显式选择 `infinimesh-info`，不得加入 provider 自动 fallback。
3. 使用凭据门控测试访问 `https://info.infinimesh.cn`，完成
   issue -> reserve -> query -> mapping。
4. smoke 成功后，已删除旧 adapter 与测试、plugin 配置、环境变量和公开配置字段。
5. 将 `infinimesh-info` 设为 `web.search` 唯一支持 provider 并更新默认值/测试。
   配置旧 provider 时必须得到明确启动或调用错误，绝不静默转换。

这是用户可见行为变化：启用 `web.search` 需要三个 Infinimesh Info 凭据；云端
失败会直接暴露，不再回退到免费搜索。

## 测试与验收标准

Mock/contract 测试必须证明：

- issue/query 的 method、path、header、body 字段和 `internal_opaque` token mode 与
  本地 OpenAPI 一致；
- wallet 在并发 reserve 下无数据竞争，且不会返回同一 token 两次；
- 过期 token 会清理、最多一批正在领取、reserve 后 token 不能归还；
- 每次 retry 使用不同 `PrivateToken` 与随机 `request_id`；
- retryable / non-retryable error code 遵守上文表格；
- `include_private_context` 始终为 false，query 请求不含签发 proof 或稳定身份字段；
- summary、sources、citations 映射到稳定 ToolHub 输出且 `untrusted=true`；
- `/api/config`、doctor 输出、错误和测试失败信息不含 token、entitlement proof、
  license proof、device attestation 或完整 query；
- `browser.read` 与 browser automation 的注册和测试不变；
- 选择 `infinimesh-info` 时绝不调用 Parallel endpoint。

Live smoke 默认 skip，只有显式启用且三个凭据齐全时才运行。它必须仅使用环境/文件
注入，访问已部署生产 Base URL，领取一个小批次 `info.basic` token，执行无敏感
公共查询，并断言映射后 answer 或 sources 非空且 `untrusted=true`。它不得打印凭据、
匿名 token、完整 query 或 raw response body。

最终验证命令：

```bash
cd services/gateway
go build ./...
go vet ./...
go test ./...
go test -race ./internal/infinimeshinfo ./internal/websearch ./internal/toolhub
cd ../..
bash scripts/doctor.sh
npm --workspace @sparkclaw/webchat run build
```

还必须通过 `.github/workflows/ci.yml` 中的文档镜像检查，记录 live smoke 结果，确认
最终 diff 不含凭据或测试产物，并在按 topic 提交后保持 worktree clean。
