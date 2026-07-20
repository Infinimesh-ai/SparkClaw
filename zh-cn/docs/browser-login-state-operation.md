# 浏览器登录态操作文档

> 语言：[English](../../docs/browser-login-state-operation.md) | 简体中文

本文档定义 SparkClaw 如何在人工登录时暂停浏览器任务，并使用同一个托管 Chromium Profile
恢复任务。Profile 生命周期见
[托管共享 Chromium Profile 方案](managed-persistent-browser-profile.md)。

## 当前状态

截至 2026-07-10：托管共享 Chromium Profile 是确定的登录态方案。Cookie/storage 导出、
个人 Chrome 附着，以及要求登录前后 origin 相同，均属于 legacy 行为，新的登录恢复流程不能
继续使用。

## 产品契约

- 浏览器任务默认在 headless Chromium 中执行。
- 登录、验证码、短信、2FA、授权、支付或其他人工步骤会创建 `BrowserLoginBlock`，并把
  同一个 Profile 切换到可见 Chromium。
- 同一 session 中用户的下一条消息用于恢复被阻塞的原 run，不是新目标。
- 登录后把当前选中可见页面的实际 URL 记录为 handoff 目标。已持久化 Workflow 保留原 route
  和 resource；selected 页面只用于建立认证状态，受治理读取仍使用冻结的 `TargetRef`。
- 已认证页面可以与原始页面处于不同 origin。
- 普通自动化继续前，使用同一个 Profile 切回 headless Chromium。
- 浏览器凭据始终保留在 Chromium Profile 中；Gateway 不导出、注入或保存 Cookie/storage。

## Browser Login Block

未完成任务必须保存在 durable runtime state 中：

```go
type BrowserLoginBlock struct {
    ID                  string         `json:"id"`
    SessionID           string         `json:"session_id"`
    RunID               string         `json:"run_id"`
    Status              string         `json:"status"`
    OriginalGoal        string         `json:"original_goal"`
    ResumeTool          string         `json:"resume_tool"`
    ResumeArgs          map[string]any `json:"resume_args"`
    LoginHandoffURL     string         `json:"login_handoff_url,omitempty"`
    LoginHandoffPageID  string         `json:"login_handoff_page_id,omitempty"`
    LastVisiblePageID   string         `json:"last_visible_page_id,omitempty"`
    OwnerID             string         `json:"owner_id"`
    BrowserProfileID    string         `json:"browser_profile_id"`
    SiteOrigin          string         `json:"site_origin"`
    LastUserReply       string         `json:"last_user_reply,omitempty"`
    LastError           string         `json:"last_error,omitempty"`
    CreatedAt           time.Time      `json:"created_at"`
    UpdatedAt           time.Time      `json:"updated_at"`
    ResolvedAt          *time.Time     `json:"resolved_at,omitempty"`
}
```

`OriginalGoal` 和原始 URL 保持不变，用作任务上下文。登录完成后可以用实际页面更新
`LoginHandoffURL`、`ResumeArgs.url` 和 `SiteOrigin`。这些 handoff 字段不能替换已持久化
Workflow 的 route、plan digest 或冻结 resource binding。

## 首次登录或登录失效

```text
headless Chromium 遇到认证或验证墙
  -> 创建 BrowserLoginBlock
  -> 原 run 保持 browser_login_blocked
  -> 停止 headless Chromium
  -> 使用同一个 Profile 启动可见 Chromium
  -> 打开或复用交接页面
  -> 请用户完成人工步骤
```

Runtime 不能把原 run 标记为完成，也不能让用户重复说明目标。

## 用户确认后的恢复流程

用户回复 `login completed`、`logged in`、`登录完成`、`登录成功` 或 `已经登录成功` 时：

1. 保留原 run 和 block。
2. 从当前可见 Chromium session 获取标签页列表。
3. 优先选择当前 login block 记录的 handoff/last-visible page ID；该页面已不存在时，
   才使用 provider 标记为 selected 的当前标签页。
4. 读取当前 URL，并忽略 `about:blank` 和浏览器内部页面。
5. 使用登录后的 URL 更新 block 和 resume arguments。
6. 停止可见 Chromium，等待共享 Profile 释放。
7. 使用同一个 Profile 启动 headless Chromium。
8. 未匹配任务读取登录后的 URL；已匹配任务读取持久化 Workflow 的冻结 target。Tab discovery
   和认证检查属于 Runtime preflight，不消耗 Workflow scope。
9. 页面已认证时 resolve block，并把登录前被中断的 tool outcome 应用到原持久化 Workflow。
10. 页面仍是登录或验证墙时，重新切回可见模式并保持 block waiting。

Runtime 必须复用已有的 selected 交接标签页，不能因为登录前后 origin 不同就再打开一个可见
页面。
多个对话共享同一 browser profile 时，只要 login block 自己的 handoff 页面仍存在，另一个
任务的 selected 标签页就不能覆盖它。

认证验证必须优先使用页面状态，而不是宽泛关键词。可见密码框、验证码/2FA 控件或明确的
“请登录”提示属于 challenge 证据；`sign out`、`log out`、`退出登录` 或可用的认证后应用
正文属于已认证证据。“激活需登录 VPN”之类导航文字不能重新打开 login block。

### 分层认证评估

登录态是逐步确认结果，不是一次字符串判断。Runtime 使用带置信度和 evidence signals 的
`unknown`、`challenged`、`authenticated` 三态评估。该流程参考 OpenClaw browser
automation loop 的原则：固定 Profile 和标签页、操作前检查可见 UI、页面变化后重新
snapshot、避免盲等，并且只报告真实的人工阻塞。

证据优先级如下：

1. Provider 结构化状态，例如 `profile_verified` 或明确 auth challenge；
2. 可见控件和明确指令，例如密码、验证码、2FA 控件，或退出登录、账户控件；
3. 组合页面证据，例如“资源受限”与明确 VPN/登录要求同时出现；
4. 普通页面文字和导航标签，单独出现时证据不足。

Browser provider 必须用结构化 `state`、`confidence` 和 `signals` 输出页面级评估；
`auth_challenge_detected` 只作为 `state=challenged` 的兼容投影。Provider 按以下层次评估：

1. **明确挑战控件**：可见验证码/OTP 控件，或位于登录语境 form/dialog 中、同时存在匹配
   登录动作的可见凭据控件。密码输入框本身不能代表登录墙，因为已登录应用也可能包含
   文件夹解锁、支付确认、账户设置或凭据管理控件。
2. **明确已认证控件**：可见退出控件、账户身份控件，或其他只在认证后应用中可用的控件。
3. **认证后应用连续性**：用户确认登录后，同一个托管 Profile 到达可用的非登录应用路由，
   页面具有足够的可见应用壳且不存在明确挑战。这是正向证据，不只是“没有发现登录关键词”。
   Provider 只报告应用壳信号；ToolHub 必须把它与已确认的共享 Profile 切换组合后，才能升级为
   `authenticated`。
4. **文本兜底**：页面级明确登录指令只有来自可见页面文本时才能支持 challenge。
   `script`、`style`、`template`、隐藏节点、metadata 或无关导航中的文字都不是认证证据。

Provider 必须判断证据组合，不能独立匹配关键词。类似登录的路由或标题只能增强可见登录
控件，不能单独决定状态；同样，没有 Profile 连续性或认证控件的普通应用壳不足以证明账户
已登录。同一页面同时出现强 challenge 和 authenticated 信号时，必须返回带冲突 signals 的
`unknown` 并重新 snapshot/read，不能压成一个 Boolean 结果。

这些规则必须保持域名无关。生产认证规则不得依赖站点 hostname、邮箱品牌、学校名称或
账户专用 Cookie 名称。测试可以使用具备真实站点形态的 fixture，但实现必须依据可复用的
DOM、路由、Profile 连续性和控件证据。

冲突证据返回 `unknown`，不能静默选择未登录或已登录。恢复确认先验证 selected handoff
标签页，再执行基于 Profile 的页面读取。只有 `authenticated` 才能 resolve block；
`unknown` 以证据不足原因继续 waiting，`challenged` 则携带实际 challenge 证据继续
waiting。低优先级文本绝不能覆盖 Provider 的结构化验证结果。

## 登录后域名语义

Origin 相同不是认证成功的必要条件。

例如：

```text
原始请求：   https://s.example.edu
可见登录：   https://vpn.example.edu
认证后页面： https://sso-app.vpn.example.edu/home
恢复目标：   https://sso-app.vpn.example.edu/home
```

最终选中的 URL 成为实际操作目标；原始 URL 仍保留在 `OriginalGoal` 和历史工具调用中。

只有空 URL、`about:blank`、浏览器内部 scheme 或被 host policy 阻止的 URL 才应被拒绝。

## 使用 Profile 而不是 Cookie 导出

托管共享 Profile 不使用浏览器 auth payload：

- 不通过 `document.cookie` 导出认证状态；
- 不调用 `ExportAuthState` 或 `ImportAuthState`；
- 不创建 browser-auth `CredentialSecret`；
- 不比较导出 origin 和原始 site origin；
- 不使用 JavaScript 重建 Cookie。

HttpOnly Cookie、Cookie 属性、IndexedDB、Service Worker 和跨域 SSO 状态都由 Chromium
完整 Profile 原生保留。

## 已有登录态

后续任务由 headless Chromium 使用持久 Profile 打开目标页面：

- 页面已认证：继续隐藏执行；
- 登录过期或出现新的验证墙：创建或重新打开 login block，并切换到可见 Chromium；
- 登录成功后站点仍拒绝 headless：保持可见协作模式并明确说明限制。

## 页面错误和标签页缺失

- `wrong page` 或 `页面不对`：block 保持 waiting，并尽量在现有可见标签页导航到正确 URL。
- 没有 selected 可用标签页：返回 `browser_login_block_missing_tab`。
- selected 页面仍是认证墙：返回 `browser_login_block_still_unauthenticated`。
- selected 页面证据冲突或不足：返回 `browser_login_auth_evidence_inconclusive`，记录 provider
  signals，不应立即要求用户重复登录。
- Profile 转换失败：保持 block waiting，并把生命周期失败与网站登录失败分开报告。

## Audit 事件

不记录 secret 的前提下审计：

- `browser_login_block.created`
- `browser_login_block.resume_requested`
- `browser_login_block.current_tabs_checked`
- `browser_login_block.post_login_target_selected`
- `browser_profile.switch_visible`
- `browser_profile.switch_hidden`
- `browser_login_block.resolved`
- `browser_login_block.reopened`
- `browser_login_block.still_waiting`

记录 owner/profile、presentation、可用时的 selected page id、原 site origin、登录后 site origin
和失败原因。不能记录 Cookie、token、storage value 或真实 Profile 路径。

## 失败语义

- `browser_login_block_missing_tab`
- `browser_login_block_still_unauthenticated`
- `browser_login_post_login_url_missing`
- `browser_shared_profile_busy`
- `browser_shared_profile_start_failed`
- `browser_shared_profile_stop_timeout`
- `browser_shared_profile_visible_verification_failed`
- `browser_shared_profile_hidden_verification_failed`

## 验收测试

- 登录墙创建 durable block，并使用此前 headless 使用的同一个 Profile 打开可见 Chromium。
- `登录成功` 会被识别为完成信号。
- 登录完成后复用当前 selected 可见标签页。
- WebVPN/SSO 跨 origin 跳转会更新恢复 URL，不产生 origin mismatch。
- 可见 Chromium 停止后才启动 headless Chromium。
- headless Chromium 复用 Profile 登录态并继续原 run。
- 已匹配 Workflow 的登录恢复保留 route、plan digest 和冻结 resource；Runtime 登录 preflight
  不生成替代 Workflow outcome。
- 类 QQ 邮箱 SPA fixture 在 Profile 已持久化、邮箱应用壳可用，但隐藏/style 内容仍含密码或
  登录字符串时，必须保持 authenticated。
- 只有文件夹解锁或账户设置密码框、但没有登录 form 的页面不能创建 login block。
- 可见密码框、明确登录动作和登录语境文字同时存在的真实登录 form 仍为 challenged。
- 强 challenge 与 authenticated 控件冲突时返回 `unknown`，审计中保留两组 signals。
- 不创建重复可见交接标签页。
- 不创建 browser auth `CredentialSecret`。
- 验证码、短信、2FA、授权和支付步骤仍由用户完成。
- Profile 形态切换过程中保留原始目标。
