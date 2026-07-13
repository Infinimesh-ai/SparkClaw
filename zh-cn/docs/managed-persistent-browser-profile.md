# 托管共享 Chromium Profile 方案

> 语言：[English](../../docs/managed-persistent-browser-profile.md) | 简体中文

本文档是 SparkClaw 浏览器 Profile 所有权、浏览器可见性和登录态复用的事实来源。
SparkClaw 的隐藏自动化与可见人工验证统一使用 Chromium，不连接用户日常 Chrome Profile，
也不在浏览器进程之间复制认证凭据。

相关文档：

- [浏览器登录态操作文档](browser-login-state-operation.md)
- [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)
- [浏览器模式代码编写指南](browser-modes-coding-guide.md)

## 方案决策

截至 2026-07-10：本方案取代个人 Chrome 附着和 JavaScript cookie/storage 导出方案。

对于每个 owner 和逻辑 `browser_profile_id`，SparkClaw 管理一个持久 Chromium user data
directory。同一个目录只会以以下两种互斥形态之一运行：

| 形态 | Chromium 进程 | 窗口 | 用途 |
|---|---|---|---|
| `hidden` | headless Chromium | 无 | 普通搜索、读取、导航和认证后自动化 |
| `visible` | headed Chromium | 可见 | 密码、验证码、短信、2FA、授权、支付或其他人工验证 |

可见和隐藏进程不能并发占用同一个 Profile。切换形态时必须停止当前 Chromium/MCP 进程，
等待 Profile 释放，再使用同一个 Profile 启动另一种形态。

## 产品契约

1. 两种形态启动同一个已配置 Chromium executable。
2. 两种形态使用同一个 SparkClaw-owned 持久 Profile。
3. 普通浏览器任务保持隐藏；只有需要人工验证或用户明确要求查看浏览器时才显示 Chromium。
4. Cookie（包括 HttpOnly Cookie）、storage、IndexedDB、Service Worker 和浏览器 session
   状态始终由 Chromium Profile 管理。
5. SparkClaw 不导出 `document.cookie`，不注入 Cookie，也不把浏览器认证状态保存到
   `CredentialSecret`。
6. Profile 切换必须串行：启动可见进程前先停止隐藏进程，切回隐藏前先停止可见进程。
7. 是否登录成功通过读取登录后的实际页面判断，不比较登录前后的 origin。

因此本方案支持 SSO、WebVPN 等登录成功后跳转到不同域名的流程。

## Profile 目录

Profile 由 runtime 管理，并从可信标识生成：

```text
<profile-root>/
  <owner-id-hash>/
    <browser-profile-id-hash>/
      user-data/
```

要求：

- 不能指向 Chrome 或 Chromium 的日常用户 Profile。
- 通过 typed config 解析路径并拒绝路径穿越。
- 平台支持时使用仅 owner 可访问的目录权限。
- 不在模型可见 observation 或聊天中暴露真实路径。
- 不归档、复制或提交正在使用的 Profile。
- 可见和隐藏启动必须使用同一个 Chromium executable。

即使 SparkClaw 不提取单个 secret，完整 Profile 目录仍是敏感本地状态。

## 启动契约

`--executablePath` 和 `--userDataDir` 由 adapter 管理，模型和工具参数不能覆盖。

```text
隐藏：
  chrome-devtools-mcp@<validated-version>
  --executablePath=<configured Chromium>
  --userDataDir=<resolved shared profile>
  --headless
  --viewport=1365x768
  --no-usage-statistics

可见验证：
  chrome-devtools-mcp@<validated-version>
  --executablePath=<same configured Chromium>
  --userDataDir=<same resolved shared profile>
  --chromeArg=<fresh visible session 的 handoff URL>
  --no-usage-statistics
```

共享 Profile 启动不能包含 `--isolated`、`--autoConnect`、`--browserUrl`、
`--wsEndpoint` 或用户提供的 data directory。
当新 Chromium session 已知目标 URL 时，adapter 把目标作为 Chromium startup hint。由于
Chrome DevTools MCP 仍可能把 selected 页面初始化成 `about:blank`，adapter 必须立即在同一个
页面调用 `navigate_page`，而不是再调用 `new_page`，并清理历史 blank 页面。用户不能在目标
页面前得到一个独立且长期存在的 `about:blank` 标签页。只有已运行 session 的明确新标签页
请求才使用 `new_page`。

## Runtime 流程

### 普通任务

```text
选择 owner + browser_profile_id
  -> 使用持久 Profile 启动 headless Chromium
  -> 打开、读取和操作网页
  -> 浏览器界面保持隐藏
```

### 登录或人工验证

```text
隐藏 Chromium 检测到登录或验证墙
  -> 保存 BrowserLoginBlock 和原 run 状态
  -> 停止隐藏 Chromium 并释放 Profile
  -> 使用同一个 Profile 启动可见 Chromium
  -> 打开交接 URL
  -> 用户完成人工验证
  -> 用户回复登录完成
  -> 检查当前选中的可见页面并取得实际 URL
  -> 使用登录后的 URL 作为恢复目标
  -> 停止可见 Chromium 并释放 Profile
  -> 使用同一个 Profile 启动 headless Chromium
  -> 打开并验证登录后的 URL
  -> 恢复原 run
```

恢复目标是用户登录后当前选中的已认证页面，不能要求它与原始 URL 同源。

## 登录后 URL 规则

- 优先使用用户完成登录后当前选中标签页的 URL。
- 忽略 `about:blank` 和浏览器内部页面。
- 用登录后页面更新 login block 的 handoff URL、resume URL 和当前 site origin。
- 原始 goal 和原始 URL 单独保留，继续作为任务上下文。
- 当前页面仍是登录或验证墙时，block 保持 waiting。
- 已有选中的交接标签页可用时，不能再次打开可见标签页。
- 认证检测依据可见的密码/验证控件和明确登录提示；已认证正文中的 `login`、`需登录`、
  `退出登录` 等普通词语不能单独触发未登录判定。
- `sign out`、`log out`、`退出登录` 或可用的认证后应用正文应阻止纯文本登录误报。

## 生命周期规则

- 一个共享 Profile 最多由一个 Chromium/MCP 进程占用。
- 模式转换在 adapter lock 下执行，并有明确 timeout。
- 停止 session 后等待 MCP 和 Chromium child 退出，再启动另一种形态。
- Gateway 关闭时关闭当前存活的形态。
- Profile 被占用时返回明确错误，不能删除 Chromium 锁文件强制恢复。
- 不同逻辑 browser profile 使用不同目录和 coordinator。

## 登录态

共享 Profile 取代认证导出/导入：

- `ExportAuthState` 和 `ImportAuthState` 不属于托管共享 Profile 流程。
- 不创建 cookie/storage `CredentialSecret`。
- 现有已导出的 auth record 是 legacy 数据，不导入共享 Chromium Profile。
- 可以保留非 secret 的验证 metadata，但持久 Profile 是可复用认证状态的唯一来源。

## 安全规则

- 密码、验证码、短信、2FA、授权、支付和账号安全操作保持可见并由用户完成。
- 可见和隐藏模式中的浏览器变更操作继续遵守现有 policy 和 approval。
- Profile reset/delete 需要 approval，且只能在没有 Chromium 进程占用时执行。
- Profile 文件不能进入 ToolHub 文件访问、artifact、trace、静态服务或训练数据。
- 推荐使用全盘加密保护持久浏览器状态。

## 失败语义

- `browser_shared_profile_busy`
- `browser_shared_profile_start_failed`
- `browser_shared_profile_stop_timeout`
- `browser_shared_profile_path_invalid`
- `browser_shared_profile_chromium_missing`
- `browser_shared_profile_login_required`
- `browser_shared_profile_visible_verification_failed`
- `browser_shared_profile_hidden_verification_failed`

Profile 生命周期失败和网站登录失败必须分开，以便给用户正确的下一步提示。

## 验收测试

- 可见和隐藏启动使用同一个 Chromium executable 和 Profile。
- 隐藏启动包含 `--headless` 且不包含 `--isolated`。
- 可见启动不包含 `--headless`，并且必须等隐藏 Chromium 停止后才能开始。
- 登录完成后使用当前选中的实际 URL，即使它与原始 URL 不同源。
- WebVPN/SSO 跳转不会产生 origin mismatch。
- 复用已有可见交接标签页，不创建重复标签页。
- 切回隐藏模式后继续复用 Chromium 管理的登录态。
- 不创建浏览器 cookie/storage secret。
- 普通浏览器任务保持隐藏。
- 验证码、短信、2FA、支付等步骤触发可见交接。
