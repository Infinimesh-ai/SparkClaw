# 浏览器邮箱 Workflow 设计

> Language: [English](../../docs/browser-email-workflow-design.md) | 简体中文

状态：已实现。QQ 邮箱、Outlook 和 Gmail 通过生产 SparkClaw Browser Bridge 运行确定性
Playwright CLI Handler。Phase 6 原子切换删除了原 Host-CDP Runner，未改变仅发送 Workflow、
Approval 或 Unknown-outcome 语义。

## 决策

SparkClaw 在 Browser Branch 下拥有浏览器邮箱：

```text
browser
`- browser.email
   `- browser.email#send
```

`browser.email` 只有 Revision 1 Workflow Profile 和 `send` Operation。Email Read 未注册；
检查 Inbox、读取未读邮件、Reply、Forward 或管理 Draft 的请求不会获得 Email Tool。

Model 只选择受支持 Function，并提供一个 Recipient、可选 Subject 和 Plain-text Body。
Runtime 拥有 Provider/Account Selection、Login Admission、Browser Control Credential
Generation、Provider Script Revision、Approval、Invocation Identity 与 Result Validation。

## 支持范围

当前能力支持：

- 向一个 Recipient 发送一封新的 Plain-text Message；
- 可选的单行 Subject；
- 每个 Provider 一个有效的已登录账户；
- QQ 邮箱、Outlook 和 Gmail；
- 在持久 SparkClaw Browser 中手动登录；
- 确定性只读 Login Probe 与 One-attempt Send Handler；
- 外部发送 Effect 前的一次 Exact-content Approval。

不支持 Read/Search/List、Reply、Forward、Delete、Archive、Read-state Change、复用 Draft、
多 Recipient、CC/BCC、Attachment、HTML、Signature 或每个 Provider 多账户。实现不使用
OAuth、IMAP、SMTP、Gmail API、Microsoft Graph、Cookie Export、Profile Copy、Container
Chromium 或备用 Browser Backend。

## Routing 与 Provider Resolution

Semantic Graph 只有一个用于 `send` 的 `browser.email` Candidate。Hard Negative 包含 Inbox
检查、Email Read、Attachment、Reply、Login Check 和只要求打开 Provider Website 的请求。

Provider Selection 是确定性的：

1. 请求明确命名一个已注册 Provider Alias 时，Runtime 选择该 Provider。
2. 否则 Runtime 选择唯一启用的 Default Provider。
3. 缺少配置、命名多个 Provider 或 Default 歧义时，在 Workflow 创建前阻断。

Provider ID、Alias、Login URL、Allowed Origin、Handler Path、Source Closure Hash、Revision、
Deadline、Result Verifier 和 Send-effect Selector 位于 Controller Provider Registry。Runtime
只映射这些固定 Handler；Caller 不能提供 Script Path 或 Selector。

QQ 邮箱不是 Generic Browser Destination。只要求打开 QQ 邮箱的请求不会获得 Email-send
Authority。

## Login 配置

WebChat 的 `设置 > 连接 > 浏览器邮箱` 提供 QQ 邮箱、Outlook 和 Gmail。每个 Provider 可
Enable/Disable、设为唯一 Default、打开手动登录并运行只读 Probe。Panel 显示有界 Readiness
Metadata 和可选 Masked Account Hint。

Gateway API：

```text
GET   /api/email/providers
PATCH /api/email/providers/{provider}
POST  /api/email/providers/{provider}/login-browser
POST  /api/email/providers/{provider}/check
```

固定 Login Origin：

| Provider | Login URL |
|---|---|
| `qq_mail` | `https://wx.mail.qq.com/` |
| `outlook` | `https://outlook.live.com/mail/` |
| `gmail` | `https://mail.google.com/` |

`打开登录浏览器` 创建 Provider Task Tab，并显式 Handoff 给 Owner。Owner 直接在浏览器中
输入 Credential 和完成 CAPTCHA/2FA，SparkClaw 不接收这些值。认证保留在 Owner-only
Persistent Profile 中，后续 Task Tab 无需 State Export 即可复用。

Durable Provider Setting 只包含 Owner ID、Provider ID、Enabled/Default Flag、固定
`default` Account ID、可选 Masked Account Hint、Readiness、Last-check Metadata、Bounded
Error Code、Version 和 Update Metadata。Memory、File 和 PostgreSQL 实现相同 Repository
Contract。

## Login Probe 边界

Login Probe 是配置与 Admission Logic，不是 Workflow Node 或 Model-visible Tool。Probe：

1. 使用共享 Browser Control Credential 获取新的 Controller CLI Session；
2. 创建一个 Allowlisted Task-owned Provider Tab；
3. 打开固定 Provider URL；
4. 检查确定性 Signed-in/Signed-out Page Marker；
5. 返回 `ready` 和可选 Masked Account Hint；
6. 只关闭自己的 Tab，并在不关闭 Chromium 的情况下 Detach。

Probe 不能 List、Open、Read、Compose 或 Send Mail。冲突 Evidence、Unexpected Origin、
Stale Task Ownership 或不完整 Page State 均 Fail Closed。已保存的 `ready` 只是历史状态，
不能绕过发送前的新 Probe。

## Workflow 前的新 Admission

每个明确 Send Request 都在 Workflow 创建前运行新 Probe。成功后冻结以下 Runtime-owned
Fact：Provider、固定 Account ID、可选 Masked Account Hint、Provider-setting Version、
Browser Control Vault Credential Generation、Probe/Send Handler Revision、Validation Time
和 Unique Send Invocation ID。

Login Required、Controller/Bridge 不可用、Handler Output 无效、Provider 歧义、配置冲突
或 Stale Credential 都会在 Workflow 前停止，不暴露 Email Tool 或 Send Approval。

## 单节点 Workflow

```text
Owner Request
  -> Semantic Route: browser.email#send
  -> Deterministic Provider Resolution
  -> Workflow 外的新只读 Login Admission
  -> browser.email r1 / email_send
       -> Model 提供 Recipient、可选 Subject 和 Body
       -> Runtime 恢复全部 Frozen Admission Binding
       -> Exact-content Owner Approval
       -> email.send
  -> Grounded Send Receipt
```

`email_send` Node 只有一次 Attempt、Dangerous Risk、Evidence Completion，Scope 仅包含
`browser.email.send`。Model-visible Schema 不包含 Provider、Account、Setting Version、
Credential Generation、Script Revision、Validation Time 和 Invocation ID；Runtime 在
ToolHub Validation 与 Policy 前恢复它们。

Model 不能选择 Provider、Account、Executable、URL、Tab、Browser Action、Retry 或 Alternate
Tool。Workflow 不包含 Login、Probe、Re-login 或 Generic Browser Node。

## Approval 与 Send 语义

`email.send` 是 Dangerous、Non-idempotent、Approval-required，并受 90 秒 Tool Deadline
限制。Approval 展示 Provider、Masked Account Hint、Recipient、Subject 和完整 Body，绑定
包含全部 Runtime-owned Admission Fact 的完整 Argument Object。Approval 后任何变化都会
阻断执行。

调用 Script 前，Runtime 再次检查 Provider 仍 Enabled/Ready，且 Account、Setting Version
和 Browser Control Credential Generation 与 Approval 相同。发生 Drift 时必须重新请求和
审批。

每个 Send Handler 在 Effect 前验证 Recipient、Subject、Body 和唯一 Send Control。Send
Action 最多尝试一次。注册 Effect Selector 可能被激活后出现 Timeout、Target Loss、Invalid
Result、Context Loss 或 Cleanup Failure 时，结果为 `email_send_outcome_unknown`。由于邮件
可能已经发送，该结果是 Terminal 且不可重试。

Success 不返回 Subject 或 Body，只包含 Provider、`sent` Status、精确 Recipient 的 SHA-256
Digest、可选 Opaque Provider Message ID、Browser Credential Generation 和 Handler Revision。

## Script Registry 与隔离

固定 Registry 为每个 Provider 注册一个 Probe 和一个 Send Revision。六个真实 Handler 都
通过注入的 Playwright Task Runtime 运行；Handler 内不存在 Standalone stdin Entry Point 或
Process/CDP Fallback。

Controller 通过 Owner-only `0600` Ephemeral Input File 传递 Message Value。Recipient、
Subject、Body 和 Extension Credential 不出现在 argv、Log、Artifact 或 Model Output。Input
和 Result 使用严格有界 JSON Contract；Unknown Field 与 Malformed Output 会被拒绝。

每次 Invocation 拥有一个 Task Tab。Handler 只能使用注入 Runtime 提供的 Page Operation
与 Locator。Existing Owner Tab 和 Former Task Tab 不会被复用。Completion 与 Cancellation
会 Detach CLI Session、回收 Subprocess，并删除 Private Runtime Directory。

Probe Request：

```json
{
  "schema_version": 1,
  "operation": "probe",
  "invocation_id": "opaque-runtime-id",
  "provider": "gmail",
  "account": "default"
}
```

Send Request：

```json
{
  "schema_version": 1,
  "operation": "send",
  "invocation_id": "opaque-runtime-id",
  "provider": "gmail",
  "account": "default",
  "message": {
    "recipient": "recipient@example.com",
    "subject": "Optional subject",
    "body": {"format": "text", "content": "Message body"}
  }
}
```

## Failure Contract

| Code | 含义 |
|---|---|
| `email_not_configured` | 请求的/默认 Provider 未启用。 |
| `email_login_required` | 需要手动登录。 |
| `email_account_ambiguous` | Provider 或 Default Selection 有歧义。 |
| `email_provider_unavailable` | Controller、Bridge 或 Script Runtime 不可用。 |
| `email_page_contract_changed` | Provider Evidence 不再匹配 Handler Contract。 |
| `email_invalid_input` | Recipient、Subject、Body 或 Runtime Binding 无效。 |
| `email_draft_conflict` | Existing Draft State 使新发送不安全。 |
| `email_draft_verification_failed` | 无法验证 Compose Value，不激活 Send。 |
| `email_send_control_unverified` | 无法唯一验证 Send Control。 |
| `email_send_outcome_unknown` | 邮件可能已发送，绝不自动重试。 |
| `email_script_timeout` | Send Effect 前 Probe Timeout。 |
| `email_script_invalid_output` | Provider Handler 违反严格 Result Contract。 |
| `email_admission_stale` | Admission 后配置或 Credential Generation 发生变化。 |

Selector、Page Text、Raw Diagnostic、Profile/Socket Path、Page ID、Task Identity、Token 和
Cookie 不进入 Public Setting 或 Error Payload。

## 验收边界

- Catalog 与 Semantic Routing 只暴露 Send；Email Read 保持不可用。
- Provider Resolution 和 Admission Fact 保持 Runtime-owned。
- Workflow 创建前必须通过新的只读 Probe。
- Workflow 只暴露一个 Email Tool，不暴露 Generic Browser Tool。
- Approval 绑定 Exact Content 与全部 Frozen Admission Fact。
- Approval 后重新检查 Provider Setting 与 Credential Generation。
- Send 最多尝试一次，Unknown Outcome 绝不重试。
- 每个 Provider Operation 使用一个 Bridge-allowlisted Task Tab。
- Existing Owner Tab 永远不会被选中、读取、修改或关闭。
- 不存在 Container Chromium、Cookie/Profile Copy、CDP Path 或 Browser Fallback。
- QQ 邮箱不进入 Generic Destination Registry。
- Provider Setting 在 Memory、File 与 PostgreSQL Store 中行为一致。

Email Read、Attachment、Reply、Draft 或 Multi-account 支持需要独立 Capability 与 Product
Contract，不得作为未经评审的模式加入 `browser.email` r1。
