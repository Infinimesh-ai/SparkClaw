# 浏览器登录态操作文档

> 语言： [English](../../docs/browser-login-state-operation.md) | 简体中文

本文档定义 SparkClaw 在自主模式和协作模式下处理需要登录的浏览器页面时的操作规则。
它补充 [浏览器功能完善计划](browser-automation-improvement.md)、
[浏览器模式代码编写指南](browser-modes-coding-guide.md) 和
[隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)。

## 当前状态

截至 2026-07-10：SparkClaw 已经会持久化按 scope 隔离的浏览器登录记录，并能在自主
读取遇到登录墙时打开可见登录交接页。runtime 还必须把未完成任务记录为专门的浏览器
登录阻塞态；登录墙不是已完成回答，恢复流程不能依赖普通 prompt 上下文拼接。provider
当前只覆盖浏览器可见的 cookie/storage 状态；密码、验证码、短信码、2FA 和支付确认仍
必须停下来交给用户。

## 产品契约

所有浏览器工具都必须用同一套规则处理登录态。该规则适用于 `browser.read`、
`browser.snapshot`、`browser.navigate`、`browser.open`、`browser.click`、
`browser.wait`，以及未来所有会访问登录后页面的浏览器工具。

自主模式下，普通信息任务应保持浏览器界面隐藏。检测到页面需要登录时：

1. 如果当前 owner、浏览器 profile 和站点是第一次登录，SparkClaw 打开可见登录交接页，
   为未完成 run 记录 `BrowserLoginBlock`，并请用户在浏览器里完成登录。用户下一次回复后，
   SparkClaw 先验证当前可见浏览器状态；策略允许时持久化登录态；随后关闭或隐藏交接界面，
   并回到隐藏浏览器 session 继续原任务。
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

store 应新增一个 typed auth record，并一次性实现 memory、file、Postgres 三个后端：

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

## 浏览器登录阻塞记录

`BrowserAuthRecord` 只描述可复用的已认证状态。它不能作为“某个任务正在等待用户完成登录”
的事实来源。自主登录交接需要另一个 typed block：

```go
type BrowserLoginBlock struct {
    ID                string         `json:"id"`
    SessionID         string         `json:"session_id"`
    RunID             string         `json:"run_id"`
    Status            string         `json:"status"` // waiting, resuming, resolved, canceled, failed
    OriginalGoal      string         `json:"original_goal"`
    ResumeTool        string         `json:"resume_tool"` // 通常是 browser.read
    ResumeArgs        map[string]any `json:"resume_args"`
    LastToolCallID    string         `json:"last_tool_call_id,omitempty"`
    LoginHandoffURL   string         `json:"login_handoff_url,omitempty"`
    OwnerID           string         `json:"owner_id"`
    BrowserProfileID  string         `json:"browser_profile_id"`
    SiteOrigin        string         `json:"site_origin"`
    SiteRealm         string         `json:"site_realm,omitempty"`
    AccountHint       string         `json:"account_hint,omitempty"`
    BrowserAuthStatus string         `json:"browser_auth_status,omitempty"`
    LastUserReply     string         `json:"last_user_reply,omitempty"`
    LastError         string         `json:"last_error,omitempty"`
    CreatedAt         time.Time      `json:"created_at"`
    UpdatedAt         time.Time      `json:"updated_at"`
    ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
}
```

active block 属于 runtime 状态，不是额外 prompt 上下文。对应 run 应进入
`browser_login_blocked`，且 `completed_at` 为空；block 保存原始目标、工具名、工具参数，
以及恢复所需的 owner/profile/site key。同一个 session 的下一条用户消息，在普通 TaskHint
生成前优先路由给这个 block。

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
       保存包含原 run、目标、工具和参数的 BrowserLoginBlock
       将原 run 状态设为 browser_login_blocked
       等待用户下一条消息
```

`BrowserLoginBlock` 的创建以工具结果中的登录交接字段为准，而不是绑定某一个工具名。
`browser.read` 是最常见路径，但如果 `browser.open`、`browser.navigate` 或
`browser.snapshot` 这类可见浏览器工具返回了
`browser_auth_status=handoff_waiting|handoff_required`、
`login_handoff_opened=true`，或
`auth_challenge_detected=true` 且 `login_handoff_required=true`，也必须创建 block。

可见交接只服务于登录步骤。认证验证成功后，最终页面读取、snapshot 或 navigation 继续使用
`browser_mode=autonomous`、`presentation=hidden` 和 `surface_visible=false`。

如果登录成功但 hidden provider 无法导入或复用 session，应返回明确的
`hidden_auth_restore_failed`，并保持用户可见交接页打开；不能假装隐藏任务已经成功。

## 用户回复后的恢复流程

当 session 有 active `BrowserLoginBlock` 时，下一条用户消息是阻塞态回复，不是新的无关任务。
runtime 应在普通 planning 前用简单规则识别：

- 登录完成：例如 “done”、“logged in”、“I signed in”、“登录完成”、“已登录”、“登好了”、
  “好了”。
- 页面错误：例如 “wrong page”、“not this page”、“页面错了”、“不是这个页面”、“你打开错了”，
  或者用户给了修正后的 URL。
- 模糊回复：active block 存在时的其他回复。

三种情况都要先用 `browser.list_tabs` 或等价 provider 调用检查当前可见浏览器状态。然后：

1. 如果用户说页面错了并提供 URL，更新 block 的恢复 URL，打开该 URL 的可见交接页，block
   继续等待。
2. 如果用户说页面错了但没提供 URL，重新打开已记录的 `login_handoff_url` 或原始恢复 URL，
   block 继续等待，并说明重新打开了哪个页面。
3. 如果用户说登录完成，或回复较模糊，使用已存恢复操作，并附带
   `login_handoff_completed=true`、`persist_browser_auth=true`、
   `browser_mode=autonomous`、`presentation=hidden`、`surface_visible=false`。
4. 如果恢复读取验证已登录，标记 block 为 `resolved`，保存或更新 `BrowserAuthRecord`；
   provider 支持时关闭或隐藏一次性交接页；然后使用原始 goal、原工具历史和新的浏览器
   observation 继续原 ReAct run。
5. 如果没有可见标签页、provider 无法导出登录态，或恢复读取仍落在登录墙，block 保持
   `waiting`，更新 `last_error`，并结合用户回复、原目标和恢复 URL 决定是重新打开交接页
   还是提示缺少哪个 human-only 步骤。

不能丢失上文任务和进度。恢复运行使用原 `run_id`、`original_goal`、此前工具 observations
和已存 `resume_args`；用户回复只作为解除阻塞或修正交接页的信号。

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
- `browser_login_block.created`
- `browser_login_block.resume_requested`
- `browser_login_block.current_tabs_checked`
- `browser_login_block.reopened`
- `browser_login_block.resolved`
- `browser_login_block.still_waiting`

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
- `browser_login_block_missing_tab`
- `browser_login_block_still_unauthenticated`

自主模式遇到 `missing`、`expired` 和 `restore_failed` 时，在策略允许的情况下启动可见登录
交接。协作模式应报告页面状态，并等待用户在可见浏览器中完成登录。

## 验收测试

最小实现测试：

- 自主模式首次登录检测登录墙，打开可见交接页；创建 `BrowserLoginBlock`；将原 run 标记为
  `browser_login_blocked`；且不声称任务完成。
- 用户回复“登录完成”后，先检查当前标签页；验证登录成功后保存记录；provider 支持时关闭或
  隐藏交接界面；随后恢复原 run 并继续隐藏读取原页面。
- 用户回复“页面错了”后，重新打开或修正交接 URL，同时保留原阻塞任务和进度。
- 如果没有可见标签页，或者页面仍未登录，active block 保持等待，下一条回答说明缺少的具体步骤。
- 自主模式同一 owner/profile/site 的第二次登录从数据库记录恢复，并且不打开可见界面。
- 过期或被拒绝的存储状态会被标记为 failed/expired，并回退到可见交接。
- 协作模式保持可见页面，不执行自主模式的弹出和隐藏循环。
- 不同 owner 和浏览器 profile 的记录不能交叉使用。
- store 接口变更同时覆盖 memory、file 和 Postgres 后端，并覆盖 snapshot/file encryption。
- audit event 包含登录态转换 metadata，但不包含 secret value。
