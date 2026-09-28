# 浏览器 Runtime

> Language: [English](../../docs/browser-runtime.md) | 简体中文

> 架构决策（2026-09-21）：唯一目标为 Electron 自带 Chromium、原生浏览视图，以及
> 现有 Controller / Playwright 控制链的 Electron 适配层。以已确定的
> [桌面浏览器设计](desktop-client-embedded-browser-design.md)为准。
> 本页描述切换前的独立浏览器部署基线，不是另一套目标方案，也不代表 Electron 已部署。

本文描述当前生产浏览器实现。SparkClaw 使用一个持久的 Owner Session Chromium Profile、
校验和固定的 SparkClaw Browser Bridge，以及 Owner-scoped Playwright Controller。旧的
browserd、Host-CDP 和 `agent-browser` 路径已在 Phase 6 原子切换中删除。

## Runtime 拓扑

```text
Browser 或 Email Workflow
  -> Gateway browserautomation / emailautomation
  -> Owner-only Controller Unix socket
  -> 固定 Playwright MCP 或 CLI Client
  -> SparkClaw Browser Bridge Native Connection
  -> 持久 SparkClaw Chromium 中 Task-owned Tab
```

Chromium 和 Controller 都是 systemd user service。Gateway 仅把 Controller Runtime
Directory 以只读方式挂载到 `/run/sparkclaw/browser-controller`，不会获得浏览器 Profile、
Native Host Manifest、Display Socket 或 Browser Executable。Gateway 镜像不包含 Chromium、
Xvfb 或浏览器自动化引擎。

| 组件 | 位置 |
|---|---|
| Browser Service | `sparkclaw-browser.service` |
| Controller Service | `sparkclaw-browser-controller.service` |
| Browser Config | `~/.config/sparkclaw/browser.json` |
| 持久 Profile | `~/.local/share/sparkclaw/browser/default/user-data` |
| Controller Runtime | `${XDG_RUNTIME_DIR}/sparkclaw/browser-controller` |
| Controller Socket | `${XDG_RUNTIME_DIR}/sparkclaw/browser-controller/controller.sock` |
| Desktop Launcher | `~/.local/share/applications/sparkclaw-browser.desktop` |

固定兼容组合为 Browser Bridge `1.0.28`、Playwright MCP `0.0.80`、Playwright CLI
`0.1.19`、Playwright Library `1.63.0-alpha-2026-08-31` 和 Chromium
`148.0.7778.0`。Bridge Source Closure 记录在 `configs/browser-bridge-artifacts.json`，
安装时拒绝发生修改或出现额外文件的 Source Tree。

## Browser Process

`sparkclaw-browser.service` 是默认 Profile 唯一的长驻 Owner。它以固定 User Data
Directory 和 Unpacked Bridge 启动正常的 Headed Chromium。命令行有意不包含
浏览器级 remote-debugging endpoint、`--enable-automation` 或 headless Flag；同时使用
Chromium 支持的 `--silent-debugger-extension-api`，使任务范围内的 Bridge 附着不再在页面
上方增加浏览器级调试状态栏。

从桌面 Launcher 打开 **SparkClaw Browser**，或运行：

```bash
npm run open:browser
```

显式 Open Command 会把已有浏览器窗口带到前台，供 Owner 完成登录或 Human Verification，
不会再启动一个仅有 `about:blank` 的新窗口。
首个后台任务会创建一个不聚焦的专用浏览器窗口，后续所有任务都复用该窗口，不再向
Owner 窗口添加 Tab；只有显式 Owner Handoff 才允许自动化把 Task Tab 移入聚焦窗口。

在 X11 上，原生宿主会在每次任务连接前记录浏览器窗口。如果恰好新增一个浏览器窗口，
宿主会把它移到当前桌面窗口之后；只有 Chromium 意外激活任务窗口时才恢复先前的活动窗口。
已有浏览器窗口的层叠顺序不会被改动。

后台连接和清理保留操作系统当前的窗口焦点。恢复 Owner Tab 时不得让浏览器窗口失焦：
用户此前从其他应用切入浏览器，不代表开始检查登录时应将那个应用重新置于前台。
任务标签页组继续作为 Owner 可见的控制边界。关闭一个受控任务标签页会立即撤销该标签页
的控制；关闭最后一个任务标签页还会结束对应 Bridge 连接。两种操作都不会关闭 Chromium，
也不会修改 Owner 标签页。

认证只保留在持久 Profile 内。SparkClaw 不复制 Cookie、不导出 Storage State、不把
Profile 挂入容器，也不附着其他浏览器 Profile。

## Bridge 与 Controller

Browser Bridge 从已资格验证的上游 Playwright Extension Source 独立打包。SparkClaw 增加
Attachment-time Task-tab Allowlist、Native Controller Version Handshake、Stale Session
Cleanup 和后台不抢焦点行为。Extension ID 与完整文件 Hash 均已固定。

Controller 拥有私有 Unix Socket，并监管有界 MCP 与 CLI Process。每次 Acquisition 只创建
一个 Task Page，并绑定 Controller、Session、Page 和 Credential Generation。每次 Observation
和 Action 执行前都会检查所有权。Owner Tab 永远不会被选中、读取、修改或关闭。

MCP 承载通用 Browser Adapter。CLI 只运行六个已注册 Provider Handler：QQ 邮箱、Outlook
和 Gmail 各自的 Probe 与 Send Revision 1。Caller 不能提供 Playwright Code、Selector、
JavaScript、Command、Storage Access、Network Interception 或任意文件路径。

MCP 与 CLI Session Detach 时不会关闭 Chromium。Cancellation、Replacement、Credential
Removal、Browser Restart、Controller Restart、Gateway Shutdown 和正常完成都会使有界
Identity 失效，并回收 Subprocess 与私有输出。Stale Identity 绝不会被静默重新绑定。

## Credential 边界

Browser Control 使用 `playwright-extension-token-v1` Credential。Owner 在
`通讯工具 > 浏览器控制` 中输入 Token；Gateway 完成一次新的 Bridge Handshake 后才保存
加密 Vault Ciphertext。表单不会返回或预填 Token，并在每次保存尝试后清空输入。

Raw Token 不会保存到 Compose 文件、仓库配置、Log、Trace、Artifact、命令参数或 Model
Context。Controller Service 不持有该 Token。替换或删除 Credential 会使旧 Credential
Generation 的 Session 失效，但不会修改浏览器认证状态。

## 配置

唯一生产 Provider 是 `playwright-extension`，配置加载会拒绝其他 `provider` 值。
`startupTimeoutMs` 限定任务向 Controller 获取浏览器 Session 的等待时长（500 到 30000 毫秒）：

```json
{
  "tools": {
    "browserAutomation": {
      "enabled": true,
      "provider": "playwright-extension",
      "profile": "default"
    }
  },
  "adapters": {
    "browserAutomation": {
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "settleTimeoutMs": 15000,
      "settleQuietPeriodMs": 500,
      "settlePollIntervalMs": 100,
      "routeRebindLimit": 2,
      "playwrightExtension": {
        "controllerSocket": "/run/sparkclaw/browser-controller/controller.sock",
        "profileID": "default",
        "connectTimeoutMs": 20000
      }
    }
  }
}
```

部署可在所选 mode-`0600` 环境文件中设置以下 Machine-specific 值：

| 变量 | 用途 |
|---|---|
| `SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST` | 以只读方式挂入 Gateway 的宿主 Controller Runtime Directory |
| `SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET` | Gateway 内的 Controller Socket 路径 |
| `SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST` | Setup、Doctor 和资格验证直接使用的宿主 Socket |
| `SPARKCLAW_BROWSER_EXTENSION_PROFILE_ID` | 固定 Profile Identity，必须为 `default` |
| `SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS` | 有界 Acquisition/Handshake Timeout |

配置加载会拒绝已退役的 Browser Automation Command、Transport Selector 和 CDP Variable。
不存在 Runtime Fallback。

## 安装与运维

Local 与 Remote 部署都在 Compose 前调用同一个 Browser Setup：

```bash
npm run setup:browser
npm run check:browser-controller
systemctl --user status sparkclaw-browser.service
systemctl --user status sparkclaw-browser-controller.service
```

`setup:browser` 验证或安装固定 Chromium 与 Bridge，写入 Browser Service 和 Desktop
Launcher，以禁用 Browser Download 的方式安装 Controller Dependency，写入 Native Host
Manifest，启动两个 User Service，验证已加载 Bridge Version，并检查 Private Socket。
Local 和 Remote 启动路径会重复该检查，并在 Gateway Ready 后从容器内运行
`browser_controller_smoke.mjs`。

需要完成或刷新浏览器登录时，打开持久浏览器并使用 WebChat 的 Provider Login Action。
由于同一 Owner-only Profile 始终保留，登录状态可跨 Gateway、Controller 和 Chromium Restart
持久化。

## 验证

Bridge `1.0.21` 将任务页的原生下载事件接入 Playwright 的 `Download` 文件。Controller
安装时对固定版本的 Playwright Core relay 应用带版本检查的补丁；安装检查会拒绝缺少
补丁或上游代码变化。支持现有持久默认上下文的 `allowAndName` 配置，其他上下文和行为
明确拒绝。Bridge 保留浏览器全局下载设置，只关联任务时间范围内唯一的原生下载记录。
宿主校验普通文件并复制到私有暂存目录后，才清理原临时下载文件。下载上限为 110 MiB，
不会覆盖已有目标文件。邮件导出弹窗转为任务页内导航，通过 `waitForEvent('download')`
和 `saveAs()` 完成保存，MIME 字节不经过 CLI 文本输出。验证与范围见
[邮件数据设计](email-read-design.md)。

```bash
python3 -m unittest scripts/test_browser_bridge.py
npm test --prefix tools/browser-bridge
npm test --prefix tools/browser-controller
npm run test:email-scripts
cd services/gateway && go test ./internal/browserautomation ./internal/browsercontrol ./internal/emailautomation ./internal/gateway ./internal/toolhub
```

Controller 与 Gateway 解析 Playwright Tab List、Snapshot 和 CLI 错误输出的代码都以
`tools/browser-controller/test/fixtures/playwright-golden.json` 为准，该文件是从固定
版本的 MCP 与 CLI 包录制的真实输出，离线 Fake 也回放同样的形态。修改 Playwright 版本
Pin 后，用本机 Headless 浏览器重新录制并审阅差异：

```bash
node tools/browser-controller/test/fixtures/record-playwright-golden.mjs
```

Live Acceptance 还会检查 Startup/Restart、Bridge Pairing/Detach、Profile Persistence、
No-handoff Focus Isolation、Explicit Handoff、Generic Adapter Interaction、三个已登录账户的
Provider Probe、Process Cleanup，以及不存在禁止的 Browser Flag。Provider Qualification
只运行 Probe：

```bash
npm run qualify:playwright-email -- --profile remote
```

不得使用资格验证发送真实邮件。Email Send 继续保留 Exact-content Approval、One-attempt
Execution 和 Terminal Unknown-outcome Handling。

## 安全不变量

- 即使 SparkClaw 把每个 Client 限制在 Task-owned Tab，仍要把 Bridge 视为 Browser-wide
  Privileged Code。
- Browser Profile、Native Host、Runtime Directory、Socket 和 Vault Credential 必须保持
  Owner-only。
- Provider Origin 和 Controller Operation 必须使用 Allowlist。
- 拒绝任意 Code、Selector、Command、Storage Export、File URL 和 Network Interception。
- Page Evidence 与 Diagnostic 到达 Trace 或 Model Input 前必须脱敏。
- 不得引入 Container Chromium、Profile Copy、Permanent CDP 或兼容 Backend。

迁移决策见 [Playwright Extension 浏览器设计](playwright-extension-browser-design.md)，Provider
与 Approval 语义见[浏览器邮箱 Workflow](browser-email-workflow-design.md)。

Bridge `1.0.28` 为所有 Bridge Client 串行创建并复用一个非聚焦的专用任务窗口，使 Task Tab 绝不进入 Owner 正在使用的窗口；窗口 ID 保存在扩展 Session Storage 中，因此 Manifest V3 Worker 重启后仍会复用同一存活窗口。窗口已删除或不再包含任务归属标签时不会误复用，显式 Handoff 仍会创建并聚焦 Owner 可见窗口。Chromium 会先创建空的专用窗口，Bridge 再在该精确窗口内创建非激活连接页并移除占位页，既避免前台焦点被抢，也规避把扩展 URL 直接传给窗口创建接口时的 Chromium 拒绝。Bridge 同时串行处理任务分组，关闭任务前等待尚未完成的分组操作。每个任务组均带有由扩展本地存储佐证的随机归属标记，因此 Chromium 重启后即使重分配了数字组 ID，仍可验证恢复组的归属；每次原生连接请求都会先收敛已验证的残留组，再创建新任务页。原生关闭失败时保留归属记录并有限重试清理，同时保护活动连接及用户明确接管的页面，普通用户组不会仅因可读标题相同而被删除。Controller Service 为有界的任务页与 CLI 清理预留 60 秒，之后 systemd 才会终止进程。

创建任务标签组时必须显式把 `createProperties.windowId` 绑定到任务标签页所在的
窗口。省略该值会让 Chrome 在当前窗口创建组，导致原本正确建在后台的任务页
被移动到用户窗口。参见 [Chrome tabs.group API](https://developer.chrome.com/docs/extensions/reference/api/tabs#method-group)。

连接页准入只能使用一次，且绑定当前 worker 通过 native broker 新建的确切标签页。
恢复、复制或重新加载的旧连接页即使仍带有有效凭据，也不能再次取得任务控制权；
未使用的连接页到期关闭。任务继续通过完成、取消、断连路径清理，登录检查的一次性
任务页在成功和失败时均关闭。窗口只要为空或含任一个个人页、已释放页、交接页，
后续任务就必须另建后台窗口。邮件常驻监听和有界读取租约仍属于活跃工作，不能
仅因多个 task 标签可见就当作遗留；个人登录页永远不会成为自动任务目标。

可通过以下只读命令核查窗口归属，不输出页面 URL、标题或凭据：

```bash
node tools/browser-controller/src/browser-bridge-launcher.mjs --task-status
```

结果逐窗口列出活跃任务页、可验证的遗留任务页、其他页面数量。查询不会接管或
关闭页面；实际清理仍需 Bridge 的归属记录。
