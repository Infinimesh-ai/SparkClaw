# 设置与集成配置

> 语言：[English](../../docs/settings-integration-configuration-design.md) | 简体中文

状态：已接受，并在 `codex/server-deployment` 实现。

## 范围

本设计把 WebChat 原有的单列长设置页改为分类目录，并为 Infinimesh Info
和出站 LocalMind MCP 增加可在线生效的家庭级配置。

SparkClaw 作为受信任的家庭服务部署。对话记录仍可有 owner，但服务集成
属于家庭全局资源：

- 所有通过认证的家庭控制客户端都可以查看状态和修改服务凭据；
- Info 与 LocalMind 凭据不按个人或 owner 隔离；
- 外部 MCP principal 与 Bridge peer 没有独立的设置入口；
- Gateway 原有的控制面认证仍是权限边界。

具体凭据数量限制与多人同时编辑的 revision 冲突不在本期范围内。

## 设置导航

设置 inspector 使用四个分类和逐层进入的 detail view：

```text
设置
|- 账户
|  |- Owner 资料
|  `- 已配对客户端
|- 连接
|  |- 消息渠道：Telegram、微信
|  |- 数据服务：Infinimesh Info
|  |- 出站 MCP：LocalMind
|  `- 入站 MCP：外部 MCP 访问
|- Agent
|  |- 工具策略
|  `- 模型配置
`- 系统
   `- 运行边界
```

目录只显示图标、名称和有界状态。点击条目后以 detail view 替换目录，返回
按钮回到当前分类。原有 connector、策略、模型、Owner、客户端和外部 MCP
控制全部保留且可达。

LocalMind 与外部 MCP 必须分开：LocalMind 是固定的出站 task client；外部
MCP 是其他 AI 调用 SparkClaw 的入站入口，信任方向相反。

## 家庭凭据模型

每个集成在现有 Credential Vault 中保存一个加密、带版本的 bundle：

```text
integration:infinimesh-info
  kind: infinimesh-info-credential-bundle-v1

integration:localmind
  kind: localmind-credential-bundle-v1
```

解密后的逻辑结构为：

```json
{
  "version": 1,
  "active_credential_id": "info_cred_b",
  "credentials": [
    {
      "id": "info_cred_a",
      "label": "家庭账号",
      "validated_at": "2026-08-27T10:00:00Z",
      "last_checked_at": "2026-08-27T10:00:00Z",
      "state": "ready",
      "payload": {
        "license_id": "...",
        "license_key": "..."
      }
    }
  ]
}
```

包括 label、endpoint、标识符和 secret 在内的整个 bundle 作为一个认证值
加密。Repository 只能看到不透明 Vault ref、credential kind 和
AES-256-GCM envelope。Vault 使用 repository 的 compare-and-swap replace，
不会先删后建；create、replace、delete 的未知结果必须先确认，才能接受下一
次变更。

公开响应只包含：

- 不透明 credential ID；
- 用户填写的 label；
- 校验/检查时间；
- 有界状态和错误码；
- 是否为当前凭据。

公开响应绝不包含 License ID/key、LocalMind endpoint/token、Vault ref、
加密 envelope、环境变量名或文件路径。Secret 输入框从不回填，并在每次
保存请求结束后清空，无论成功还是失败。

## 保存、选择与删除语义

保存和选择是两个独立操作：

1. Gateway 校验类型化的本地格式；
2. 执行该集成真实的在线校验；
3. 只有校验通过的凭据才加入加密 bundle；
4. 当前凭据不变；
5. 用户显式选择一个已保存凭据，或选择虚拟的 operator 配置。

有效来源为：

```text
用户选择的家庭 Vault 凭据 > 用户选择的 operator 配置 > 未配置
```

这是选择优先级，不是代码回退规则。家庭凭据一旦被选择，后续认证失败或
服务不可用时仍保持选中并报告失败。SparkClaw 不会自动改用其他已保存凭据，
也不会自动退回 operator 配置。

校验失败发生在持久化之前，因此不改变当前来源。删除活动凭据会被拒绝；
用户必须先显式选择另一个已保存凭据或可用的 operator 配置。非活动凭据可
直接删除。

## 运行时切换

每个实时集成运行时都有单调递增的 credential generation。Agent run 首次
使用集成时记录 generation。一次已提交的选择变更会：

1. 把集成标记为 updating；
2. 取消使用旧 generation 的直接调用和完整 agent run；
3. 被中止调用返回 `info_credentials_changed` 或
   `localmind_credentials_changed`；
4. 只发布由新来源构造的 client；
5. generation 加一；即使后续 runtime refresh 失败，新选择仍保持提交。

发布窗口中的新调用返回 `info_updating` 或 `localmind_updating`。暂停后恢复的
run 也不能跨 generation 混用凭据。

系统不实现兼容排空、双 client 过渡或运行时自动回滚。

## Infinimesh Info 契约

Info 凭据包含有长度限制的 License ID 和 License Key。Key 必须符合
`ilk_v1.<license-id>.<secret>` wire 格式，并嵌入相同 License ID。

保存前或显式检查时，SparkClaw 构造临时 client，执行且只执行一次固定的
低费用查询：

```text
query: SparkClaw connection check
max_sources: 1
token batch: 1
private context: false
```

响应内容被丢弃，设置 API 只返回类型化的成功、认证失败或暂时不可用。

`web.search` 仍由显式 Web Search 工具配置/策略控制；`weather.lookup` 不依赖
凭据是否存在，始终注册。凭据不决定 Info 工具是否存在。未选择来源时调用
会返回 `info_not_configured`；显式工具策略仍可以拒绝已注册工具。

## LocalMind 契约

LocalMind 凭据包含有长度限制的 workspace MCP endpoint 和 bearer token。
Endpoint 必须是绝对 HTTP(S) URL，不得包含 user info、query 或 fragment，
并以 `/api/workspaces/<workspace-id>/mcp` 结尾。只有 operator 已允许 private
HTTP，且目标为 loopback、private 或容器内 host 时，才允许明文 HTTP。

在线校验复用完整的固定 LocalMind task 契约：

1. 协商 MCP `2025-06-18`；
2. 要求 server name 为 `localmind-ai`；
3. 拒绝 Resources；
4. 要求精确的三个远端 task tool 及 schema；
5. 准备四个受治理的本地 tool registration。

凭据校验不会发布工具。激活时 refresh，并原子发布四个 registration。Refresh
失败会清除旧 LocalMind 工具，同时保持刚选择的凭据，不做回退。

只要存在固定 LocalMind server 配置块，即使 operator 环境变量为空也会构造
manager，从而允许稍后激活家庭凭据。没有选中来源时，周期 refresh 保持空闲。

## Gateway API

认证后的控制 API 按 provider 使用类型化请求：

```text
GET    /api/integrations
GET    /api/integrations/{id}
POST   /api/integrations/infinimesh-info/credentials
POST   /api/integrations/localmind/credentials
PUT    /api/integrations/{id}/active-credential
POST   /api/integrations/{id}/credentials/{credential_id}/check
DELETE /api/integrations/{id}/credentials/{credential_id}
```

Credential 请求体限制为 16 KiB，拒绝未知字段，并且只接受一个 JSON 值。
错误响应只含稳定错误码和有界信息。Controller 内部串行化变更；审计只记录
integration ID、operation、source、state 和有界错误码。本家庭 API 不加入
多人编辑用的 optimistic revision 字段。

## 公开状态词汇

| 状态 | 含义 |
|---|---|
| `not_configured` | 未选择来源，且没有可用 operator 来源 |
| `configured` | 已选择并可在本地使用，但不声明刚完成实时检查 |
| `checking` | 正在执行显式在线检查 |
| `ready` | 所需查询或握手已成功 |
| `needs_attention` | 认证、身份、契约或永久校验失败 |
| `temporarily_unavailable` | 有界、可重试的外部检查失败 |
| `vault_unavailable` | 无法安全读取或修改加密 bundle |

Operator 配置仅以 availability flag 和虚拟可选择行呈现，其具体值和来源位置
保持私密。

## 验证要求

测试覆盖 Vault CAS replace 与未知结果、只持久化 ciphertext、多凭据保留、
失败校验不落库、显式激活、禁止删除活动凭据、活动失败不回退、generation
中止、Info 工具与凭据解耦注册、固定 Info 查询、LocalMind 固定契约校验、
API 脱敏，以及 WebChat 导航、secret 清空和切换确认。
