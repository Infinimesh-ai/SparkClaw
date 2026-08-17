# 通用外部 MCP 安全防护设计

> 语言：[English](../../docs/generic-mcp-safeguards-design.md) | 简体中文

> 状态：[issue #10](https://github.com/Infinimesh-ai/SparkClaw/issues/10) 已按
> Resolved Decisions 中记录的安全默认值实现。

## 决策摘要

通用 external MCP client 与 LocalMind workspace MCP client 共同使用一套远端 tool 可见性、
ToolDefinition 转换、结果脱敏和有界 state/archive 投影，以及 approval argument 持久化防护。
Provider adapter 可以增加更窄的 capability qualifier 和执行行为，但不能削弱这些共享防护。

两个保持依赖方向的 owner package 执行该契约：

| Package | 职责 |
|---|---|
| `internal/mcptools` | 规范化远端 tool metadata，应用 allow/deny 与 mutation policy，分类 risk/effect，并把一个已发现 MCP tool 转为 `app.ToolDefinition`。 |
| `internal/mcpsafety` | 检测敏感 key、Bearer 值、签名 URL 和大段 base64；生成脱敏且有字节上限的 state/archive 投影；拒绝不安全的 approval argument。 |

`mcpintegration`、`localmind` 和 `agent` 使用这些 package。共享 package 均不导入 adapter、
Gateway handler、Store 实现或 Agent runtime，因此不会破坏现有依赖方向。

## 问题与威胁模型

配置的 MCP endpoint、credential 和 namespace 由 operator 控制。Endpoint 返回的一切仍是不可信
输入，包括 tool name、description、schema、annotation、result content 与 error text。

当前通用路径在四处破坏了该边界：

1. `list_` 或 `get_` 前缀能把无 annotation 的 tool 变成 `RiskRead` 并移除审批。远端因此可以把
   `get_wipe_workspace` 这类破坏性工具伪装成无需审批的 read。
2. 通用 server 配置拒绝 `tool_allow`、`tool_deny` 和 `allow_mutations`，operator 无法收窄发现的 catalog。
3. 通用结果进入持久 state 和 observation 前，没有经过 LocalMind 已有的脱敏及 state/archive 大小分离。
4. Approval 持久化只对 LocalMind ToolDefinition 拒绝不安全参数，通用 external MCP mutation
   却会持久化相同的 approval 和 tool-call record。

本设计假设已认证 MCP server 仍可能被攻陷或配置错误。远端内容不得为自身授予权限、把 credential
泄漏到持久状态，或制造无界 observation。

## 安全不变量

- Tool name 和 description 绝不能降低 risk 或移除 approval。
- `tool_allow` 与 `tool_deny` 对 remote tool name 做精确匹配。空 allow list 表示“不增加 allow
  限制”；deny 在 allow 后应用，allow/deny 重叠属于配置错误。
- `allow_mutations=false` 时，mutation-classified tool 不注册到 ToolHub。这是 exposure gate，
  不只是执行时检查。
- 每个已注册 mutation 都需要 owner approval。Dangerous 或 open-world tool 继续通过现有 Policy
  路径进行 deep verification。
- 只有显式 `readOnlyHint=true` 可以把 tool 分类为 read 并移除审批。Name 与 description 绝不暗示
  read-only；destructive 或 open-world annotation 会覆盖矛盾的 read-only annotation。
- 成功结果和 `isError` 结果在进入 tool-call state、summary 或 artifact 前，都使用同一套脱敏与限长投影。
- 有界 state projection 用于 Workflow 推理；单独限长的 archive projection 保留可检查的已脱敏 MCP envelope。
- 所有 external MCP capability 在持久化 approval argument 或 pending ToolCall 前，都会拒绝敏感
  key、Bearer 值、签名 URL 和大段 base64。
- Refresh 原子替换某 provider 的可见 ToolHub 集合。Status 返回过滤后的可见 tool 数量，而不是
  未过滤的远端数量。
- Adapter-owned direct call 必须通过最近一次 discovery 的同一套 policy。Typed service 不能通过
  直接调用 MCP client 绕过 allow/deny 或 mutation policy。

## 配置契约

通用 `mcp_servers.<name>` entry 接受已有字段 `allow_mutations`、`tool_allow` 与 `tool_deny`。
其规范化和冲突校验与 LocalMind 完全一致。Filter 在 namespace 转换前匹配 remote MCP tool name。

建议的通用配置示例：

```json
{
  "mcp_servers": {
    "happy-tasks": {
      "url": "https://happy.example.com/v1/team/mcp",
      "token_env": "HAPPY_TEAM_MCP_TOKEN",
      "expected_server_name": "happy-team-tasks",
      "allow_mutations": true,
      "tool_allow": ["list_tasks", "get_task", "create_task", "cancel_task"],
      "tool_deny": []
    }
  }
}
```

配置加载器仍会拒绝通用 entry 使用 LocalMind 专属 endpoint、identity、refresh 与 projection-tuning
字段。通用 state/archive projection 固定使用 16 KiB/16 MiB 安全边界。

## 共享 Tool 转换

`internal/mcptools` 接收 discovered tool 与 adapter-owned translation option，返回两个 typed value：
visibility/classification decision 与 `app.ToolDefinition`。Visibility、`Risk`、`RequiresApproval`、
`Idempotent`、directory effect 和 capability mode 都由同一个分类结果驱动，避免字段互相矛盾。

共享 translator 负责：

- 限长后的远端 title 与 description metadata；
- input/output schema 拷贝和 nil input schema 规范化；
- annotation 解析；
- risk、approval、idempotency 与 effect 映射；
- 通用 external MCP origin metadata。

Adapter 仍负责：

- local name 与 dynamic registration source；
- capability name 与 provider/snapshot qualifier；
- provider-specific directory 文案；
- Happy `wait_for_idle` 等 timeout 特例；
- execution closure、refresh/retry policy 与 coded transport error。

LocalMind capability snapshot 仍是更强的 provider-specific scope check。通用路径不会伪造 LocalMind
workspace scope。

## 共享结果投影

`internal/mcpsafety` 提供两条 adapter 共用的 canonical MCP result projection：

1. 存在 `structuredContent.result` 时优先使用它。
2. 否则保留非空 `structuredContent`。
3. 否则合并 text content；仅有一个 JSON text block 时可以解码为 JSON value。
4. 两种 projection 都先递归脱敏，再测量和持久化。
5. 已脱敏 projection 超过上限时，生成包含 byte count 与 SHA-256 的紧凑 truncation record。

State projection 只包含 canonical result。Archive projection 包含 provider/source identity、remote tool
name、content、`structuredContent`、`isError`、metadata 与 `untrusted: true`。Projection 失败后绝不
退回 raw remote payload。

Error message 只暴露短小的脱敏摘要；完整但已脱敏、限长的 error observation 仍通过正常 artifact
路径保留。

## Approval 持久化

Agent guard 通过 typed capability 识别 external MCP definition，同时覆盖
`ToolCapabilityExternalMCPWorkspace` 与 `ToolCapabilityMCPExternal`，不再只检查 LocalMind provider
qualifier。敏感值检测移到 `internal/mcpsafety`，删除重复的 key、signed URL 和 base64 判断。

拒绝时 ToolCall 只保留 `{ "persistence_rejected": true }`，不创建 Approval，并通过 provider-neutral
文案返回现有 typed `mcp_persistence_unsafe` failure。

## 请求流程

```text
configured MCP server
  -> bounded discovery
  -> exact allow/deny filter
  -> mutation exposure gate
  -> shared classification and ToolDefinition translation
  -> atomic ToolHub replacement
  -> Workflow -> Policy -> Approval
  -> approval argument persistence guard
  -> remote call
  -> shared redacted state/archive projection
  -> ToolCall state + bounded observation artifact
```

Happy plan 同步继续使用专用 typed `happyapproval` service。其固定的 `list_tasks`、`get_task_plan`、
`get_task`、`approve_plan` 和 `reject_plan` 调用不是模型选择的 ToolHub call，必须继续由现有显式
workflow 和测试覆盖。`mcpintegration.Manager.CallTool` 仍会根据最近一次成功 discovery 的 remote
name 与共享 policy decision 授权每次直接调用。被过滤、未发现或 mutation-disabled 的
tool 会在 transport 前 fail closed。因此，启用 Happy plan 决议时，operator 必须暴露固定 read
tool 并显式允许 mutation。

## 兼容性与上线

这是有意的安全行为修改。移除 name-prefix 信任后，根据最终确认的 policy，tool 可能新增审批或被隐藏。
如果通用 mutation 改为 opt-in，现有配置未写 `allow_mutations` 时，Happy create/message/stop/cancel
tool 可能不再可见。

实现会同步更新中英文 integration 示例和 tuning-key 文档。不得根据已知 server name 静默推断
policy，也不得新增 per-provider 硬编码 tool list；operator 必须通过配置表达例外。

本次不需要 Store schema 或 backend interface 修改。现有持久 approval 不重写；新 guard 在创建
新 record 前生效。Dynamic discovery refresh 会原子应用新 policy。

## 实现

1. `internal/mcpsafety` 负责经过 table-driven test 的 sensitive-value primitive、LocalMind/通用
   result projection 与 Agent approval persistence guard。
2. `internal/mcptools` 负责 classification、filter、effect 与 definition translation；LocalMind
   和通用 registration 都使用它。
3. `internal/config` 规范化通用 allow/deny/mutation setting，`internal/mcpintegration` 则把它们
   应用于 discovery、status、dynamic registration 与 direct call。
4. Agent workflow/manual test 与通用 MCP test 覆盖 persistence、projection、classification、
   refresh 和 direct-call boundary。
5. `docs/integrations.md` 及中文镜像记录默认值、迁移、projection bound 与 direct-call 行为。

Production 职责范围：

| 区域 | 修改 |
|---|---|
| `internal/mcpsafety` | 新增共享 sanitizer、projection 与 persistence guard。 |
| `internal/mcptools` | 新增共享 visibility、classification 与 ToolDefinition translator。 |
| `internal/config` | 接受并规范化通用 safeguard setting。 |
| `internal/localmind` | 用共享调用替换重复的 translation/projection helper。 |
| `internal/mcpintegration` | 注册与 direct call 都执行 policy；投影所有动态工具结果。 |
| `internal/agent` | 对每个 typed external MCP tool 应用 approval persistence 防护。 |
| `docs/integrations.md` 及镜像 | 记录 operator 配置与安全行为。 |

WebChat、Store、MCP protocol 与 Workflow Profile 均无需修改。

## 验证方案

Focused test 覆盖：

- 通用与 LocalMind 配置规范化、去重、精确 filter、allow/deny 冲突与 mutation 默认值；
- 伪造的 `get_wipe_workspace`、缺少 annotation、矛盾 annotation、dangerous/open-world annotation
  与 idempotency 映射；
- 通用与 LocalMind adapter 的一致 translation invariant；
- secret key、内联 secret、Bearer、signed URL、base64、malformed/non-JSON、structured、text fallback、
  `isError`、state truncation 与 archive truncation result；
- Workflow 与 manual invocation 两条路径上，通用和 LocalMind external tool 的 approval persistence 拒绝；
- 原子 refresh、过滤后的 status count 与 server 独立降级；
- 对 filtered、missing 或 mutation-disabled Happy tool 的 direct-call denial，且不削弱 typed
  Happy approval lifecycle；
- 现有 Happy read/mutation Workflow 选择及 Happy plan approval 同步。

最终 gate：

```bash
cd services/gateway && go build ./...
cd services/gateway && go test ./...
cd services/gateway && go vet ./...
npm --workspace @sparkclaw/webchat test
npm --workspace @sparkclaw/webchat run build
bash scripts/run-eval.sh
bash scripts/doctor.sh
```

中英文文档的 mirror/link CI 检查也必须通过。

## Resolved Decisions

1. 信任 MCP 协议中显式的 `readOnlyHint=true`，但移除全部 name-prefix 推断。无 annotation 的 tool
   默认按 mutation 处理；启用时需要审批，mutation 关闭时保持隐藏。
2. 通用 `allow_mutations` 未配置时立即默认 `false`。可信 Happy 配置必须显式 opt in。
3. 通用结果固定使用 16 KiB state 与 16 MiB archive 上限。LocalMind 保留已有可配置上限，但共享
   同一 projection 实现。
