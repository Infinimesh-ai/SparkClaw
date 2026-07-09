# 隐藏 Chromium 浏览器访问计划

> 语言： [English](../../docs/browser-hidden-chromium-access.md) | 简体中文

本文档定义 SparkClaw 浏览器访问能力的下一阶段目标：自主模式应能以真实
Chromium/Chrome 浏览器身份访问网页，但不向用户展示窗口。本文档补充
[浏览器功能完善计划](browser-automation-improvement.md) 和
[浏览器模式代码编写指南](browser-modes-coding-guide.md)。

## 来源关系

实现应兼容 Chromium 浏览器模型。公开参考来源是 Chromium 源码的官方
GitHub 镜像：[chromium/chromium](https://github.com/chromium/chromium)。

SparkClaw 不应在 gateway 中 vendor 或编译 Chromium 源码。应使用已安装的
Chrome/Chromium/Chrome for Testing 二进制，或已有 DevTools-capable
provider，并通过当前 browser automation adapter 边界驱动。

## 目标

自主网页读取应具备真实渲染浏览器路径：

```text
web.search
  -> browser.read(browser_mode=autonomous, presentation=hidden)
  -> 隐藏 Chromium/Chrome 页面加载 URL
  -> JavaScript、重定向、cookie、常规渲染和懒加载完成
  -> evaluate_script 提取 DOM/HTML/text/page metadata
  -> ToolHub 运行 @mozilla/readability
  -> autonomous structure snapshot 查看链接、按钮和页面 affordances
  -> 可选的有界隐藏 navigate/read 后续
  -> 带来源证据的 final answer
```

普通信息任务不应让用户看到浏览器窗口、标签页、截图或 UI 操作叙述。

## 非目标

- 不绕过登录、验证码、2FA、付费墙、bot 检测或支付流程。
- 不在自主读取中静默使用用户可见 Chrome profile。
- 不让 `browser.snapshot` 依赖当前可见浏览器刚好聚焦的 tab。
- 不把多步账号状态变更操作隐藏进自主工具。
- 不新增依赖开发者机器固定 Chromium 路径的硬编码。

## Provider 契约

SparkClaw 保持一个浏览器能力，但分成两种 provider surface：

| Surface | Mode | Window | Profile | Primary use |
|---|---|---|---|---|
| hidden Chromium provider | `autonomous` | 无窗口/headless | 默认隔离 runtime profile | 搜索、读取、渲染、结构提取 |
| visible DevTools provider | `collaborative` | 可见 | 仅在允许时使用配置的用户 profile | 打开/展示/点击/输入/截图/登录交接 |

隐藏 provider 可以用两种方式实现：

- 如果现有 ChromeDevTools MCP subprocess 支持所需启动参数和生命周期控制，
  就配置它启动/使用 headless Chrome/Chromium；或
- 在现有 `browserautomation.Adapter` 边界后增加新的 adapter 实现，由它启动
  headless Chrome/Chromium 并直接使用 Chrome DevTools Protocol。

两种选择都必须复用现有 ToolHub 和 ReAct 路由契约。模型仍调用
`web.search`、`browser.read`、`browser.snapshot`、`browser.navigate` 等工具；
provider 选择发生在 ToolHub 下层。

## 当前实现说明

第一版实现复用现有 ChromeDevTools MCP adapter，并为自主隐藏访问启动第二个
MCP stdio session。协作可见 session 继续使用原配置的 MCP 参数；隐藏
session 会在未显式配置时追加 provider 启动参数：`--headless`、
`--isolated`、`--viewport=1365x768` 和 `--no-usage-statistics`。

`browser.read(browser_mode=autonomous, presentation=hidden)` 现在会先尝试这条
隐藏浏览器 session，并返回 `read_mode=hidden_browser_session`。如果隐藏
provider 无法启动或导航，现有 direct HTTP 路径仍作为 fallback，并返回
`read_mode=direct_http_fallback`。

带 URL 的自主隐藏 `browser.snapshot` 会先通过隐藏浏览器 session 读取该 URL，
再调用 `take_snapshot`。如果没有提供 URL 或未来的页面引用，它会明确失败，而
不是对当前可见 tab 做 snapshot。

隐藏 snapshot 之后，如果调用携带 `browser_mode=autonomous`、
`presentation=hidden` 和 `surface_visible=false`，`browser.open`、
`browser.navigate`、`browser.click`、`browser.wait` 也会继续留在隐藏
session。此类后续调用会补充轻量 current-page state（`current_url`、
`current_title`、`current_ready_state`），方便模型判断是否再次
`browser.read` 或停止。

## 启动要求

隐藏 Chromium 进程必须有隔离且归属明确的生命周期：

- 以 cancellable context 为根，并由 `Adapter.Close()` 关闭
- 每个页面操作都套用 gateway timeout
- 使用 runtime 拥有的 user data directory，不使用用户日常 Chrome profile
- 允许显式配置 Chrome/Chromium executable，并提供跨平台发现 fallback
- 不聚焦、不展示窗口
- 捕获 stderr/stdout 供诊断使用，但不要把页面内容泄漏到日志
- 按 storage policy 删除或过期临时 profile

典型启动意图：

```text
chrome-or-chromium
  --headless=new
  --remote-debugging-port=0 或 --remote-debugging-pipe
  --user-data-dir=<runtime-owned-profile>
  --no-first-run
  --no-default-browser-check
```

具体 flags 应按 provider 实现决定，并在 macOS/Linux 上测试后再作为默认值。

## 读取路径

自主读取时，`browser.read` 应在可用时选择隐藏浏览器 session：

```text
browser.read
  -> autonomous/hidden 选择 hidden provider
  -> 创建或复用 hidden page context
  -> navigate 到 URL
  -> 在 timeout 内等待 load/domcontentloaded/network-idle
  -> 小幅滚动触发正文懒加载
  -> evaluate DOM extraction script
  -> 返回 rendered HTML/text 和页面 metadata
  -> ToolHub 运行 Readability
```

预期新增输出：

- `read_mode=hidden_browser_session`
- `rendered=true`
- `browser_mode=autonomous`
- `presentation=hidden`
- `surface_visible=false`
- `browser_provider=chromium-headless` 或具体 provider 名
- `browser_actions` 如 `new_hidden_page`、`navigate`、`evaluate_script`
- `browser_page_ref` 或等价的稳定 hidden session reference
- 出现登录/验证码/密码迹象时返回 `auth_challenge_detected`

如果隐藏 provider 不可用，当前 direct HTTP 路径继续作为 fallback。它必须继续
标记 `read_mode=direct_http` 或 `direct_http_fallback`。

## 自主 Snapshot

自主 `browser.snapshot` 不能对可见浏览器当前 tab 调用 `take_snapshot`。它应按
以下优先级选择来源：

1. 最新 `browser.read` 引用的隐藏 Chromium 页面
2. 模型/runtime 提供的 `browser_page_ref`
3. `snapshot_ref` 归档的 raw HTML
4. 对请求 URL 做一次新的隐藏/直接读取

snapshot 应描述页面结构，而不仅是正文：

- document title 和 final URL
- canonical URL 和 meta description
- headings
- links：文本、绝对 URL、站内/站外分类
- buttons 和 button-like elements
- forms 和 inputs，但不包含敏感值
- tables：caption/header 摘要和行列数
- attachments 和下载链接
- pagination、上一页/下一页
- `展开`、`更多`、`阅读全文`、`显示全部` 等 expand/read-more affordances
- 登录、验证码、付费墙、auth hints

自主 snapshot 可以给结构结果分配稳定 ID，但这些 ID 不等同于可见浏览器
accessibility refs。它们可以指导 `browser.navigate` 或再次 `browser.read`；
不能被视为点击敏感控件的许可。

## 隐藏交互闭环

第一版隐藏交互闭环应保持保守：

```text
browser.read
  -> needs_structure_snapshot=true
  -> browser.snapshot 使用 hidden page 或 archived HTML
  -> 选择一个安全内部链接或一个安全展开/阅读全文控件
  -> browser.navigate 或需要 approval 的 browser.click
  -> browser.read again
  -> final
```

允许的自主动作：

- 跟随公开内部链接
- 展开非敏感正文
- 进入公开文章/列表下一页
- 通过现有文档工具下载/读取公开附件

当下一步是登录、密码、验证码、短信码、2FA、同意、购买、提交、删除、发送、
订阅或账号安全操作时，应停止而不是执行。

## 模式路由

ToolHub 按 metadata 路由：

```text
browser_mode=autonomous + presentation=hidden
  -> hidden Chromium provider if available
  -> direct HTTP fallback if unavailable

browser_mode=collaborative or presentation=visible or surface_visible=true
  -> visible DevTools provider
```

forced-session 参数可以保留，但必须显式且被 audit。如果强制自主 browser
session 会创建可见窗口，ToolHub 应拒绝或降级到 direct HTTP，而不是让用户意外看到窗口。

## 失败语义

隐藏浏览器路径应返回明确失败：

- `hidden_browser_unavailable`
- `navigation_timeout`
- `render_timeout`
- `auth_challenge_detected`
- `captcha_detected`
- `snapshot_source_missing`
- `provider_opened_visible_surface`

基于 URL 读取后出现 `about:blank` snapshot 不是有效证据。它应被视为 snapshot
失败并触发修复路径，而不是普通 completed observation。

## 可观测性

每次 hidden browser 调用都应保留：

- 选中的 provider
- launch mode
- profile mode
- page ref
- final URL
- ready state
- DOM/text 长度和截断状态
- JavaScript 是否完成执行
- 是否检测到 auth/captcha/paywall
- 是否发生 fallback

Audit event 应清楚说明读取使用的是 hidden browser rendering、direct HTTP，还是
visible collaborative browser automation。

## 实施阶段

1. 文档和契约。本文档及中文镜像被浏览器 roadmap 链接后完成。
2. Provider capability detection。
   - 检测当前 ChromeDevTools MCP 是否能启动/使用 headless Chrome。
   - 如果不能，在 `browserautomation.Adapter` 后引入独立 hidden Chromium adapter。
3. Hidden read path。
   - 实现自主 hidden `browser.read`。
   - 返回 rendered HTML/text，并标记 `read_mode=hidden_browser_session`。
4. Autonomous structure snapshot。
   - 将 hidden DOM 或 archived HTML 解析成 links/buttons/forms 等结构。
   - 自主 hidden snapshot 停止使用 visible `take_snapshot`。
5. Hidden follow-up loop。
   - 支持一次安全 navigate/expand/read retry。
   - 保持 approvals 和 sensitive-action stop。
6. 登录和反爬扩展点。
   - 保留显式交接到协作模式，让用户完成登录。
   - 反爬处理保持为未来 policy-governed 工作。

## 必须测试

- 自主 hidden `browser.read` 从 fixture 返回 JS 渲染后的内容，且不打开 visible provider。
- 自主 hidden `browser.read` 返回 `read_mode=hidden_browser_session`、`rendered=true`、
  `presentation=hidden` 和 `surface_visible=false`。
- 自主 `browser.snapshot` 在 hidden/direct `browser.read` 之后使用目标页面或归档 HTML，
  不使用无关的 `about:blank` tab。
- Structure snapshot 能从 fixture HTML 提取 links、buttons、headings、forms、tables、
  attachments 和 pagination。
- hidden browser launch 失败时，`browser.read` fallback 到 direct HTTP，并带明确 metadata。
- 协作可见工具仍使用 visible provider，截图能力不回退。
- `Adapter.Close()` 会终止 hidden browser subprocess。
- `go test ./services/gateway/internal/browserautomation -count=1`
- `go test ./services/gateway/internal/toolhub -count=1`
- `go test ./services/gateway/internal/agent -count=1`
- `go test ./services/gateway/...`
- `git diff --check`

## 验收标准

当普通微信/网页信息问题可以通过真实浏览器引擎加载 JavaScript 渲染页面、抽取正文并检查页面结构，且不展示浏览器窗口时，本里程碑完成。明确要求打开、展示、播放、点击或截图的请求仍必须使用协作可见浏览器工具。
