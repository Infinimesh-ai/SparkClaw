# 浏览器邮箱 Workflow 设计

> 语言：[English](../../docs/browser-email-workflow-design.md) | 简体中文

状态：已于 2026-09-03 实现为仅发送的 Host-CDP 浏览器能力。其与 Transport 绑定的设计
已于 2026-09-04 被
[Playwright 扩展浏览器迁移设计](playwright-extension-browser-design.md)取代，供后续实现
使用。本文在原子切换前继续记录当前代码，但不得作为新浏览器 Transport 的目标契约。

## 决策

SparkClaw 把基于浏览器的邮箱能力放在现有 browser 分支下：

```text
browser
`- browser.email
   `- browser.email#send
```

`browser.email` 只有一个 revision 1 Workflow Profile 和一个 `send` 操作。邮件读取
没有注册。查看收件箱、读取未读邮件、回复、转发或管理草稿的请求不会获得邮箱工具。

QQ 邮箱、Outlook 和 Gmail 通过第一方提供方脚本操作 SparkClaw 专用 Chromium
Profile。模型只选择已支持的功能，并提供收件人、可选主题和纯文本正文。Runtime
负责提供方选择、账户选择、登录准入、浏览器身份、脚本 revision、审批、调用和结果校验。

该能力不会恢复已退役的通用 Personal Data 邮箱 Connector；它是具有全新执行和失败
契约的有界 Host-CDP 浏览器 Workflow。

## 支持范围

当前切片支持：

- 新发送一封纯文本邮件；
- 恰好一个收件人；
- 可选单行主题；
- 每个提供方一个有效登录账户；
- QQ 邮箱、Outlook 和 Gmail；
- 在 headed SparkClaw Chromium 中由用户手动登录；
- 在 headless SparkClaw Chromium 中执行登录探针和发送；
- 外部发送效果前的一次精确内容审批。

当前不支持：

- 读取、搜索、列出、回复、转发、删除、归档或标记邮件；
- 只写草稿的请求，或复用提供方内已有草稿；
- 多收件人、抄送/密送、附件、HTML 编写或签名；
- OAuth、IMAP、SMTP、Gmail API、Microsoft Graph 或提供方访问令牌；
- 同一提供方内的多个账户；
- Cookie 导出、Profile 复制、Playwright、容器 Chromium 或回退到其他浏览器 Runtime。

### 仅资格验证的读取脚本

仓库保留从 Provider worktree 整合的 `scripts/email/qqmail-read-unread.mjs`，作为
确定性的 QQ 邮箱资格验证工具。它没有注册进 Capability Catalog、Workflow Registry、
ToolHub、Gateway API 或 WebChat，因此该脚本的存在不会在 SparkClaw 中启用邮件读取。

该工具只接受版本化的 `read_first_unread` JSON stdin 契约，使用唯一的任务所有
Host-CDP 标签页，只打开一次首封未读邮件，并返回发件人、主题、显示时间、正文和有界
附件元数据。打开邮件会将其标记为已读；点击后的任何浏览器失败都返回
`read_outcome_unknown`，调用方不得自动重试。

## 路由与提供方解析

语义图只包含一个 `browser.email` 候选操作：`send`。其硬负例包括读取邮件、查看
收件箱、附件、回复、登录检查，以及仅打开邮箱网站的请求。

提供方选择是确定性的，绝不交给模型：

1. Owner 请求精确命中一个已注册提供方别名时，Runtime 选择该提供方；
2. 否则 Runtime 选择唯一启用的默认提供方；
3. 未配置、同时命名多个提供方或默认项有歧义时，在 Workflow 创建前阻断。

提供方 ID、别名、登录 URL、Origin、脚本命令、revision 和 timeout 只存在于
`internal/emailautomation.Registry`，不会复制为平行路由或 dispatch switch。

QQ 邮箱不再是通用浏览器注册站点。仅要求打开 QQ 邮箱时，仍是普通公网命名目标浏览器
请求，不会因此获得邮箱发送权限。

## 登录配置

WebChat 在 `设置 > 连接 > 浏览器邮箱` 中提供 QQ 邮箱、Outlook 和 Gmail 三项。
每项支持：

- 启用或禁用；
- 设为唯一默认提供方；
- 打开专用登录浏览器；
- 执行登录状态检查；
- 展示当前就绪状态、最近检查时间、受限错误码、版本和可选脱敏账户提示。

Gateway API 为：

```text
GET  /api/email/providers
PATCH /api/email/providers/{provider}
POST /api/email/providers/{provider}/login-browser
POST /api/email/providers/{provider}/check
```

三个登录地址由提供方注册表固定：

| 提供方 | 登录 URL |
|---|---|
| `qq_mail` | `https://wx.mail.qq.com/` |
| `outlook` | `https://outlook.live.com/mail/` |
| `gmail` | `https://mail.google.com/` |

“打开登录浏览器”要求 browserd 切换为 headed presentation，并打开固定提供方 URL。
用户自行输入凭据、完成 CAPTCHA 或 2FA，然后关闭 headed 浏览器。SparkClaw 不接收
这些凭据。

后续检查要求 browserd 使用同一专用 Profile 切换到 headless presentation。Headed
和 headless Chromium 只能按顺序使用该 Profile，绝不能并发。认证状态保留在 Chromium
Profile 中；SparkClaw 不把 Cookie 或 Token 复制进 Gateway 状态。

持久化提供方设置只包含 Owner ID、提供方 ID、启用/默认标记、固定 `default` 账户 ID、
可选脱敏账户提示、就绪状态、最近检查信息、错误码、版本和更新信息。Memory、File 与
PostgreSQL 后端实现相同 Repository 契约。

## 登录探针边界

登录探针属于配置和准入逻辑，不是 Workflow 节点，也不是模型可见工具。探针：

1. 要求 browserd 报告真实的 `headless` presentation；
2. 创建新的任务所有提供方标签页；
3. 打开注册的提供方 URL；
4. 检查确定性的已登录和未登录页面标记；
5. 返回 `ready` 和可选脱敏账户提示；
6. 只关闭自己创建的标签页。

探针不能列出、打开、读取、编写或发送邮件。登录证据冲突或提供方页面契约不完整时
失败关闭。历史 `ready` 状态只是状态记录，绝不能绕过新的探针。

## Workflow 前置新鲜准入

每个明确发送请求都在 Workflow Registry 创建 Plan 前执行新鲜探针。准入成功后冻结
以下 Runtime 所有的路由事实：

- 提供方和固定账户 ID；
- 可选脱敏账户提示；
- 提供方 Setting Version；
- Headless Browser Generation；
- Probe Script Revision；
- Send Script Revision；
- 校验时间；
- 唯一发送 Invocation ID。

如果探针报告需要登录、浏览器控制不可用、脚本输出无效、提供方有歧义或配置冲突，
请求会在 Workflow 创建前终止，不暴露邮箱工具，也不创建发送审批。

## 单节点 Workflow

`browser.email` r1 创建一个名为 `email_send` 的节点：

```text
Owner 请求
  -> 语义路由：browser.email#send
  -> 确定性提供方解析
  -> Workflow 外的新鲜 Headless 登录准入
  -> browser.email r1 / email_send
       -> 模型提供收件人、可选主题和正文
       -> Runtime 恢复全部冻结准入绑定
       -> Owner 精确内容审批
       -> email.send
  -> Grounded 发送回执
```

该节点只有一次尝试，Risk 为 Dangerous，以 Evidence 完成，Scope 中只包含
`browser.email.send`。模型可见 Schema 不包含提供方、账户、Setting Version、Browser
Generation、脚本 Revision、校验时间和 Invocation ID。Runtime 在 ToolHub 校验与 Policy
之前恢复这些值。

模型不能选择提供方、账户、可执行文件、URL、标签页、浏览器动作、重试或其他工具。
Workflow 不包含登录、探针、重新登录或通用浏览器节点。

## 审批与发送语义

`email.send` 是 Dangerous、Non-idempotent、需要审批的工具，Deadline 为 90 秒。审批
展示提供方、脱敏账户提示、收件人、主题和完整正文，并绑定完整参数对象，包括 Runtime
所有的准入事实。审批创建后任一参数发生变化，执行都会因 Policy Block 失败。

调用脚本前，Controller 再次校验提供方仍启用且 Ready，账户和 Setting Version 仍与
审批中的准入一致。Runner 随后要求 browserd 返回同一个 Headless Browser Generation。
配置或浏览器发生漂移时，必须由用户重新发起请求并重新审批。

每个提供方脚本在产生效果前校验实际写入的收件人、主题、正文和“发送”控件。脚本最多
尝试点击一次“发送”。点击后的 timeout、无效错误信封、target 丢失、清理失败或缺少明确
发送成功证据都变为 `email_send_outcome_unknown`。由于提供方可能已经发送邮件，该结果
是终态且不可重试。

成功输出不包含主题或正文，只返回提供方、`sent` 状态、精确收件人的 SHA-256 摘要、
可选提供方不透明消息 ID、Browser Generation 和 Send Script Revision。

## 脚本注册表与隔离

提供方注册表为每个提供方拥有一个 Probe 脚本和一个 Send 脚本。脚本使用严格的
stdin/stdout JSON 协议，并作为有界子进程运行。邮件内容只通过 stdin 传递，不进入 argv。
未知输入/输出字段会被拒绝，stdout/stderr 都有大小限制。

Runner 所有的环境只包含此次调用需要的私有 Host-CDP 连接、派生出的唯一 agent-browser
Session、Browser Generation 与固定 agent-browser 可执行文件。继承的 agent-browser
Profile、Restore、State、Auto-connect、Headed、Extension 和 Init Script 设置会被删除或拒绝。

每次调用都创建唯一任务所有标签页，后续动作全部绑定到该标签页。脚本只能关闭自己创建
的标签页。现有 Owner 标签页、之前的登录标签页和停止操作的标签页都不会复用；用户停止
操作不会转移标签页所有权。

探针请求：

```json
{
  "schema_version": 1,
  "operation": "probe",
  "invocation_id": "opaque-runtime-id",
  "provider": "gmail",
  "account": "default"
}
```

探针成功：

```json
{
  "schema_version": 1,
  "status": "ready",
  "provider": "gmail",
  "account_hint": "us***@gmail.com"
}
```

发送请求：

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

发送成功：

```json
{
  "schema_version": 1,
  "status": "sent",
  "provider": "gmail",
  "recipient_digest": "sha256:...",
  "provider_message_id": "optional-provider-opaque-id"
}
```

## 失败契约

稳定公共错误码包括：

| 错误码 | 含义 |
|---|---|
| `email_not_configured` | 请求或默认提供方未启用。 |
| `email_login_required` | 需要用户手动登录。 |
| `email_account_ambiguous` | 提供方或默认项选择有歧义。 |
| `email_provider_unavailable` | browserd、Host-CDP 或脚本 Runtime 不可用。 |
| `email_page_contract_changed` | 确定性提供方页面证据不再符合脚本契约。 |
| `email_invalid_input` | 收件人、主题、正文或 Runtime 绑定无效。 |
| `email_draft_conflict` | 提供方草稿状态导致新发送不安全。 |
| `email_draft_verification_failed` | 无法校验编写值，不点击“发送”。 |
| `email_send_control_unverified` | 无法唯一校验“发送”控件。 |
| `email_send_outcome_unknown` | 邮件可能已发送，绝不能自动重试。 |
| `email_script_timeout` | Probe 在任何发送效果前超时。 |
| `email_script_invalid_output` | 提供方脚本违反严格结果契约。 |
| `email_admission_stale` | 准入后配置或 Browser Generation 已变化。 |

Selector、页面文本、原始脚本诊断、Profile 路径、CDP Port、Capability WebSocket URL、
Target ID 和 Cookie 都不会进入公开设置或用户可见错误 Payload。

## 当前验收边界

仅在以下不变量全部保持时，当前实现才算完整：

- Catalog 与语义图只暴露 Send；
- 邮件读取保持不可用；
- 提供方解析与全部准入事实由 Runtime 所有；
- Workflow 创建前成功执行一次新鲜 Headless Probe；
- Workflow 只暴露一个邮箱工具，不暴露通用浏览器工具；
- 审批精确绑定提供方、账户、收件人、主题、正文、Setting Version、Browser Generation、
  脚本 Revision、校验时间与 Invocation ID；
- 审批后再次校验提供方设置和 Browser Generation；
- Send 最多尝试一次，未知结果绝不重试；
- 所有提供方操作都使用任务所有 Headless 标签页；
- Headed Chromium 只用于人工配置登录；
- 不存在容器 Chromium、Playwright、Cookie 复制、Profile 复制或浏览器回退；
- QQ 邮箱不在通用浏览器 Destination Registry 中；
- 提供方设置在 Memory、File 和 PostgreSQL Store 中语义一致。

未来的邮件读取、附件、回复、草稿或多账户支持需要独立 Capability 和产品契约，不能
作为未经评审的新脚本模式塞进 `browser.email` r1。
