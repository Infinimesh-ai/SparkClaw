# Playwright 浏览器自动化迁移方案

> Language: 简体中文 | [English](../../docs/playwright-browser-automation-migration.md)

本文定义 SparkClaw 浏览器自动化从 Chrome DevTools MCP 子进程迁移到
Microsoft 官方 Playwright Library 的方案。本文是本次迁移的实现契约；对外
ToolHub 浏览器 API 与托管 Profile 产品契约保持稳定。

相关文档：

- [托管共享 Chromium Profile](managed-persistent-browser-profile.md)
- [浏览器自动化完善计划](browser-automation-improvement.md)
- [浏览器模式编码指南](browser-modes-coding-guide.md)
- [浏览器登录态操作指南](browser-login-state-operation.md)

## 决策

SparkClaw 使用固定到已验证版本的官方 Node.js `playwright` 库，安装、启动并操作
与该版本匹配的本地 Chromium。Operator 可以显式配置自定义 Chromium Executable，
但默认使用 Playwright 管理的版本匹配 Browser，以获得稳定行为。Go Gateway 通过
一个很小的换行分隔 JSON 协议与 SparkClaw 自己管理的 Node Driver 通信。该 Driver
只是实现细节，不是模型可见的 Tool Server。

新实现禁止：

- 启动 `chrome-devtools-mcp` 或使用 MCP/JSON-RPC；
- 使用 `connectOverCDP`、远程调试端口、Browser WebSocket Endpoint 或调用方提供的
  DevTools Endpoint；
- 连接 Owner 日常使用的 Chrome Profile；
- 修改公开 `browser.*` ToolHub 名称、Schema、风险等级、审批规则或结果 Envelope。

## 为什么使用 Playwright

旧 Adapter 先把每个 SparkClaw 操作翻译成 Chrome DevTools MCP Tool，再在 Go 中
修补 Provider 专用 Output、选中页面文本与 `about:blank` 行为。MCP Response 卡住时
会阻塞串行 Session，页面交互还依赖 Provider 生成的 Ref。

Playwright 直接管理 Browser Lifecycle、Page 和 Context，Locator 自带可操作性检查
与自动等待，导航 API 也有明确边界。SparkClaw 仍负责 Orchestration、Authorization、
Timeout、Evidence 和 Profile Presentation 切换。

## 依赖与运行时布局

- 仓库根目录声明精确版本的 `playwright` Runtime Dependency。
- 现有项目 `npm install` 安装 Node Driver 依赖，`npm run setup:browser` 安装与其版本
  匹配的本地 Chromium。
- 运行时使用 Playwright Library，不使用 Playwright Test。
- Node Driver Source 嵌入 Go Binary，由配置的 Node Command 执行；模块从安装了
  `playwright` 的仓库 Workspace 解析。
- 默认启动不传 `executablePath`，由 Playwright 解析其固定版本对应的已安装 Browser。
- `chromiumExecutable` 是 Managed Environment 的显式覆盖；设置后 SparkClaw 校验该
  Path，并作为 `executablePath` 传入。

配置仍只有一个浏览器 Adapter 边界：

```json
{
  "adapters": {
    "browserAutomation": {
      "nodeCommand": "node",
      "timeoutMs": 30000,
      "chromiumExecutable": "",
      "profileDir": "./data/browser-profiles"
    }
  }
}
```

删除 `mcpCommand` 和 `mcpArgs`。模型或 Tool 参数不能覆盖 Node Command、Executable
Path、Profile Root 或 Playwright Launch Option。

## 进程与 Profile 所有权

Go Adapter 串行化所有浏览器操作，并且最多拥有一个 Playwright Driver Process。
Driver 对一个 Owner、Logical Profile 和 Presentation 只持有一个 Persistent Chromium
Context。

```text
Go PlaywrightAdapter
  -> embedded Node Playwright driver
      -> chromium.launchPersistentContext(userDataDir, options)
          -> BrowserContext
              -> Page[]
```

继续使用现有 Profile 布局：

```text
<profile-root>/<owner-hash>/<profile-hash>/user-data/
```

Hidden 与 Visible Presentation 互斥。Owner、Logical Profile 或 Presentation 改变时，
先关闭当前 Context 和 Driver，等待 Chromium 释放 Profile，再启动替代进程。Gateway
Shutdown 会关闭 Driver 与 Persistent Context。SparkClaw 不删除 Chromium Lock File。

Launch Option 由 Adapter 独占：

| Presentation | Playwright Option |
|---|---|
| `hidden` | `headless: true`，稳定 `viewport: 1365x768` |
| `visible` | `headless: false`，`viewport: null` |

两种模式使用同一个已解析 Browser Revision、绝对 `userDataDir`、有界默认 Timeout
和 HTTPS Error Policy。默认不传 `executablePath`，由 Playwright 使用与当前 Package
匹配的已安装 Chromium。若显式配置自定义 `chromiumExecutable`，两种 Presentation
使用同一条已校验 Path，其兼容性由 Operator 负责。

## Driver 协议

Driver 从 stdin 每行读取一个 JSON Object，并向 stdout 每行写一个 JSON Response。
每个 Request 和 Response 都带数字 `id`。

```json
{"id":1,"method":"list_pages","params":{}}
{"id":1,"result":{"pages":[]}}
```

错误与成功 Output 明确分离：

```json
{"id":1,"error":{"code":"playwright_action_failed","message":"..."}}
```

Driver Diagnostic 只写 stderr，并由 Go Adapter 有界保存。Driver 不向 stdout 输出
Log。Go Request 取消或超时时终止 Driver，防止迟到 Response 污染下一次请求。

## 公开 Tool 映射

公开 ToolHub 契约保持 Provider-neutral：

| SparkClaw Tool | Playwright Operation |
|---|---|
| `browser.status` | 确保 Context 可用并报告 Driver/Browser Health |
| `browser.list_tabs` | `context.pages()` 加 Session 内稳定 Page ID |
| `browser.open` | 复用唯一 Blank Page 或 `context.newPage()`，再执行 `page.goto()` |
| `browser.focus` | 选择 Page 并执行 `page.bringToFront()` |
| `browser.close` | `page.close()`，再选择一个剩余 Page |
| `browser.navigate` | `page.goto()`、`goBack()`、`goForward()` 或 `reload()` |
| `browser.snapshot` | Accessibility Snapshot 加有界交互元素 Ref Table |
| `browser.screenshot` | `page.screenshot()` 返回 Base64 Evidence |
| `browser.wait` | 使用操作 Timeout 等待 Locator/Text |
| `browser.click` | Locator Click，使用 Playwright Actionability 与 Auto-wait |
| `browser.type` | Locator `fill()` 或对当前 Focus 执行 Keyboard Typing |
| `browser.select` | Locator `selectOption()` |
| `browser.read` | `page.goto()` 后执行有界 DOM Evaluate，可选 Snapshot |

导航等待 `domcontentloaded`，然后进行短且有界的 Client Render Settle。不会把
`networkidle` 作为通用完成条件，因为长期 Network Connection 会使其不可靠。

## Page 与 Element Ref

Page ID 是 Driver 在 Context 生命周期内分配的稳定数字 ID，不暴露 Playwright Object
或 DevTools Target ID。

每次 `browser.snapshot` 都替换所选 Page 的上一份 Element Ref Table。Driver 枚举可见
交互元素，分配 `e1` 等有界 Ref，并返回 Role、Accessible Name、Element Type 与
Selected State。`browser.click`、`browser.type` 和 `browser.select` 只能解析最新
Snapshot 的 Ref。Ref 缺失、过期、隐藏、Detached 或 Ambiguous 时明确失败，调用方
必须重新 Snapshot。

这样既保证一个 Observation 内的 Model-visible Ref 稳定，又能使用 Playwright Locator
Actionability Check 处理渲染竞争。

## Read 与 Evidence 契约

`browser.read` 保持现有 Output Schema。Driver 返回 Rendered URL、Title、Language、
Ready State、Content Type、有界 HTML、Visible Text、Scroll Height、Truncation Signal
和 Authentication Indicator。ToolHub 继续在 Browser 外运行 Readability，并把 Browser
Output 作为 Untrusted Evidence 归档。

Snapshot 仍按需执行。普通 Read 流程为：

```text
page.goto -> bounded render settle -> page.evaluate -> Readability
```

显式或 Diagnostic Snapshot 再增加 Accessibility 与 Element Ref Evidence。任何页面
内容都不能修改 Driver Command、Launch Configuration、Policy 或 Approval Decision。

## Timeout 与恢复

- 每个 Driver Request 使用 Caller Deadline 或配置的 Adapter Timeout；Browser Launch
  在该边界内预留一小段 Cleanup 时间。
- Playwright Context 和 Page 默认 Timeout 使用同一个配置边界。
- Timeout、Malformed Response、EOF 或 Driver Crash 都会 Reset Session，并终止 Driver
  Process Group，避免遗留 Chromium Child Process。
- 下一次调用可以为同一 Managed Profile 启动一个干净 Driver。
- Stale Ref、Navigation Error、Login Wall、Profile Lock Error 等业务失败保持可区分。
- Mutating Interaction 不自动重试；只读 Session Startup 只能在 Reset 后由后续调用重试。

## 安全与登录接管

Managed Profile 与 Human Verification 规则不变：

- Chromium 仍是 Cookie、Storage、IndexedDB、Service Worker 与 Login State 的事实源。
- SparkClaw 不导出 Cookie 或 Credential。
- Password、Captcha、SMS、2FA、Permission、Payment 与 Account Security 操作保持
  Visible 且由人完成。
- Hidden Login Challenge 必须先关闭 Hidden Driver，再用同一个 Profile 启动 Visible。
- Resume 获取选中 Visible Page URL，关闭 Visible Context，用同一 Profile 恢复 Headless，
  验证页面后继续原 Run。

## 迁移顺序

1. 新增本文与中文镜像。
2. 固定 `playwright` 版本，并增加 Browser Setup/Doctor Check。
3. 新增嵌入式 Node Driver 与协议测试。
4. 用 `PlaywrightAdapter` 替换 `ChromeDevToolsAdapter` 与 MCP stdio 代码。
5. 更新 Provider Name、Tool Description、Configuration 与 Browser 文档。
6. 用 Provider-neutral 和 Playwright Protocol Test 替换 MCP Shape Test，同时保留
   ToolHub Behavior Test。
7. 执行 Fixture、Hidden Chromium、Visible Lifecycle、Screenshot、Interaction、
   Profile Switch、Shutdown 与 Login Handoff 验证。

## 验收标准

- Production 不再引用 `chrome-devtools-mcp`、MCP Browser Tool、`connectOverCDP`、
  DevTools WebSocket 或 Remote Debugging Port。
- Clean Setup 安装精确 Playwright 版本，并由 `scripts/doctor.sh` 校验。
- Hidden 与 Visible 操作通过 `launchPersistentContext` 启动与 Playwright 版本匹配的
  本地 Chromium（或同一个显式 Custom Override），并串行复用同一个 Managed Profile。
- Open/List/Focus/Close/Navigate/Read/Snapshot/Screenshot/Wait/Click/Type/Select 保持
  公开 ToolHub 契约。
- Locator Interaction 自动等待，并明确拒绝过期 Snapshot Ref。
- Timeout 与 Shutdown 不遗留 Driver 或 Chromium Child Process。
- 现有 Direct HTTP Fallback、Readability、Untrusted Evidence、Policy、Approval 与
  Login Handoff 行为保持不变。
- Browser Unit Test、ToolHub Browser Scenario、Gateway Build/Vet 与真实 Local Chromium
  Smoke Test 全部通过。

## 官方参考

- [Microsoft Playwright 仓库](https://github.com/microsoft/playwright)
- [Playwright Library 文档](https://playwright.dev/docs/library)
- [BrowserType.launchPersistentContext](https://playwright.dev/docs/api/class-browsertype#browser-type-launch-persistent-context)
- [Playwright Actionability 与自动等待](https://playwright.dev/docs/actionability)
- [Locator API](https://playwright.dev/docs/api/class-locator)
