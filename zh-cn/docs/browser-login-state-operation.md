# 浏览器登录态操作文档

> 语言： [English](../../docs/browser-login-state-operation.md) | 简体中文

本文档定义 SparkClaw 在自主模式和协作模式下处理需要登录的浏览器页面时的操作规则。
它补充 [浏览器功能完善计划](browser-automation-improvement.md)、
[浏览器模式代码编写指南](browser-modes-coding-guide.md) 和
[隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)。

## 当前状态

截至 2026-07-09：本文档是下一阶段登录态能力的目标操作契约。浏览器模式拆分和隐藏
provider 基础已经存在，但持久化浏览器登录记录、首次登录交接流程还没有实现。

## 产品契约

所有浏览器工具都必须用同一套规则处理登录态。该规则适用于 `browser.read`、
`browser.snapshot`、`browser.navigate`、`browser.open`、`browser.click`、
`browser.wait`，以及未来所有会访问登录后页面的浏览器工具。

自主模式下，普通信息任务应保持浏览器界面隐藏。检测到页面需要登录时：

1. 如果当前 owner、浏览器 profile 和站点是第一次登录，SparkClaw 打开可见登录交接页，
   让用户完成登录；策略允许时持久化登录态；随后关闭或隐藏交接界面，并回到隐藏浏览器
   session 继续原任务。
2. 如果数据库里已有同一 owner、浏览器 profile、站点和账号的有效登录记录，SparkClaw
   在不显示窗口的情况下恢复或刷新登录态，验证页面已处于登录后状态，然后继续隐藏执行。
3. 如果记录不存在、过期、被站点拒绝，或者仍需要密码、验证码、短信码、2FA、授权确认、
   支付确认等 human-only 步骤，则回退到首次登录的可见交接流程。

协作模式使用已经可见的浏览器界面，不需要自主模式的弹出和隐藏循环。需要登录时，用户在
可见浏览器中完成登录，SparkClaw 再从该页面状态继续。策略允许时，协作模式也可以保存或
复用同一套数据库登录记录，但不能突然切换用户正在看的浏览器界面。

## 身份和 Profile 键

SparkClaw 现在是多用户、多配置文件模型。浏览器登录数据必须同时按以下键隔离：

| Key | 含义 |
|---|---|
| `owner_id` | SparkClaw owner profile ID，来自 `OwnerProfile`。 |
| `browser_profile_id` | 逻辑浏览器 profile，例如 `default`、`work`、`school` 或 `personal`。 |
| `site_origin` | 规范化 origin，例如 `https://example.com`。 |
| `site_realm` | 可选的更细登录域，用于一个 origin 承载多个应用的情况。 |
| `account_hint` | 可选的非敏感账号标签，例如邮箱域名或脱敏用户名。 |
| `auth_strategy` | `session_restore`、`credential_login`、`sso_handoff` 或 provider-specific 策略。 |

除非用户明确关联 profile，否则记录不得跨 owner 或逻辑浏览器 profile 共享。
`owner_a/work` 查不到记录时，不能自动退到 `owner_a/personal` 或 `owner_b/work`。

runtime 应从当前 session/client 绑定推导 `owner_id`，从显式工具参数、owner 偏好或
`tools.browserAutomation.profile` 推导 `browser_profile_id`，默认值为 `default`。

## 持久化登录记录

store 应新增一个 typed record，并一次性实现 memory、file、Postgres 三个后端：

```go
type BrowserAuthRecord struct {
    ID               string     `json:"id"`
    OwnerID          string     `json:"owner_id"`
    BrowserProfileID string     `json:"browser_profile_id"`
    SiteOrigin       string     `json:"site_origin"`
    SiteRealm        string     `json:"site_realm,omitempty"`
    AccountHint      string     `json:"account_hint,omitempty"`
    AuthStrategy     string     `json:"auth_strategy"`
    Status           string     `json:"status"` // active, expired, revoked, failed
    SessionRef       string     `json:"session_ref,omitempty"`
    CredentialRef    string     `json:"credential_ref,omitempty"`
    CookieJarRef     string     `json:"cookie_jar_ref,omitempty"`
    LastVerifiedAt   time.Time  `json:"last_verified_at,omitempty"`
    ExpiresAt        *time.Time `json:"expires_at,omitempty"`
    LastError        string     `json:"last_error,omitempty"`
    CreatedAt        time.Time  `json:"created_at"`
    UpdatedAt        time.Time  `json:"updated_at"`
    RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}
```

密码、cookie、refresh token、导出的 storage state 等 secret value 必须放在加密的
`CredentialSecret` 或 artifact 引用后面，不能出现在工具参数、trace、audit metadata
或明文 JSON summary 中。file 后端必须遵守现有 state encryption 配置。

## 自主模式流程

```text
browser tool 检测到 auth challenge
  -> normalize owner_id + browser_profile_id + site_origin + optional realm
  -> lookup BrowserAuthRecord
  -> 如果 active record 存在：
       在 hidden provider 中 restore session/credential
       验证 DOM 已处于登录后状态
       继续隐藏浏览器操作
  -> 如果没有可用记录：
       打开可见登录交接窗口/页面
       等待用户完成登录信号
       验证 DOM 已处于登录后状态
       持久化 BrowserAuthRecord 和 secret refs
       关闭或隐藏可见交接界面
       在 hidden provider 中继续原操作
```

可见交接只服务于登录步骤。认证验证成功后，最终页面读取、snapshot 或 navigation 继续使用
`browser_mode=autonomous`、`presentation=hidden` 和 `surface_visible=false`。

如果登录成功但 hidden provider 无法导入或复用 session，应返回明确的
`hidden_auth_restore_failed`，并保持用户可见交接页打开；不能假装隐藏任务已经成功。

## 协作模式流程

```text
browser tool 检测到 auth challenge
  -> 保持当前可见浏览器界面
  -> 请用户在浏览器中完成登录
  -> 验证 DOM 已处于登录后状态
  -> 可选地持久化或更新 BrowserAuthRecord
  -> 在同一个可见页面/session 上继续使用协作工具
```

协作模式不会额外打开登录弹窗，除非用户要求新窗口。登录后也不会隐藏或关闭浏览器，除非用户
要求，或该页面本来就是一次性任务 tab。

## 检测和输出字段

涉及登录态的浏览器 observation 应尽量包含以下字段：

- `auth_challenge_detected`
- `auth_challenge_kind`
- `auth_site_origin`
- `auth_site_realm`
- `browser_auth_status`：`none`、`challenge`、`restored`、`handoff_required`、
  `handoff_waiting`、`verified`、`expired`、`failed`
- `browser_auth_record_id`
- `browser_profile_id`
- `owner_id`
- `login_surface`：`hidden`、`visible_handoff`、`collaborative_visible`
- `login_handoff_required`

登录检测应基于页面证据，例如 password 字段、登录文案、HTTP redirect、正文被登录墙遮挡、
账号菜单是否出现以及 provider-specific 信号。页面只显示登录墙时，不能推断登录后私有内容。

## 安全规则

- 不要求用户把密码、短信码或 2FA code 粘贴到聊天里。
- 不自动化验证码、短信码、2FA、支付确认、授权同意、购买、账号安全或改密步骤。
- 不把原始密码或 cookie 存入 trace、audit event 或模型可见 observation。
- 未经用户明确操作，不跨 owner、profile、origin 或 account hint 复用登录记录。
- restore 后落回登录墙时，将记录标记为 expired 或 failed。
- 必须提供撤销浏览器登录记录并删除其 secret refs 的路径。

## Audit Events

gateway 应审计以下状态转换，但不写入 secret value：

- `browser_auth.challenge_detected`
- `browser_auth.record_lookup`
- `browser_auth.restore_attempted`
- `browser_auth.restore_succeeded`
- `browser_auth.restore_failed`
- `browser_auth.handoff_started`
- `browser_auth.handoff_verified`
- `browser_auth.record_saved`
- `browser_auth.record_revoked`

事件应包含 `owner_id`、`browser_profile_id`、`site_origin`、`site_realm`、
已知时的 `account_hint`、选中的 `browser_mode`、provider，以及适用的失败原因。

## 失败语义

使用明确的工具错误或输出状态：

- `browser_auth_record_missing`
- `browser_auth_record_expired`
- `browser_auth_restore_failed`
- `browser_auth_handoff_required`
- `browser_auth_handoff_timeout`
- `browser_auth_handoff_canceled`
- `browser_auth_verification_failed`
- `browser_auth_profile_mismatch`

自主模式遇到 `missing`、`expired` 和 `restore_failed` 时，在策略允许的情况下启动可见登录
交接。协作模式应报告页面状态，并等待用户在可见浏览器中完成登录。

## 验收测试

最小实现测试：

- 自主模式首次登录检测登录墙，打开可见交接页；验证登录成功后保存记录；关闭或隐藏交接界面；
  然后继续隐藏读取原页面。
- 自主模式同一 owner/profile/site 的第二次登录从数据库记录恢复，并且不打开可见界面。
- 过期或被拒绝的存储状态会被标记为 failed/expired，并回退到可见交接。
- 协作模式保持可见页面，不执行自主模式的弹出和隐藏循环。
- 不同 owner 和浏览器 profile 的记录不能交叉使用。
- store 接口变更同时覆盖 memory、file 和 Postgres 后端，并覆盖 snapshot/file encryption。
- audit event 包含登录态转换 metadata，但不包含 secret value。

