# 浏览器 Runtime

> Language: [English](../../docs/browser-runtime.md) | 简体中文

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

固定兼容组合为 Browser Bridge `1.0.18`、Playwright MCP `0.0.80`、Playwright CLI
`0.1.19`、Playwright Library `1.63.0-alpha-2026-08-31` 和 Chromium
`148.0.7778.0`。Bridge Source Closure 记录在 `configs/browser-bridge-artifacts.json`，
安装时拒绝发生修改或出现额外文件的 Source Tree。

## Browser Process

`sparkclaw-browser.service` 是默认 Profile 唯一的长驻 Owner。它以固定 User Data
Directory 和 Unpacked Bridge 启动正常的 Headed Chromium。命令行有意不包含
remote-debugging、automation 或 headless Flag。

从桌面 Launcher 打开 **SparkClaw Browser**，或运行：

```bash
npm run open:browser
```

显式 Open Command 会把浏览器带到前台，供 Owner 完成登录或 Human Verification。
后台 Acquisition 和 Task Action 不会聚焦浏览器或替换当前 Owner Tab；只有显式 Owner
Handoff 才允许自动化聚焦 Task Tab。

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
`设置 > 连接 > 浏览器控制` 中输入 Token；Gateway 完成一次新的 Bridge Handshake 后才保存
加密 Vault Ciphertext。表单不会返回或预填 Token，并在每次保存尝试后清空输入。

Raw Token 不会保存到 Compose 文件、仓库配置、Log、Trace、Artifact、命令参数或 Model
Context。Controller Service 不持有该 Token。替换或删除 Credential 会使旧 Credential
Generation 的 Session 失效，但不会修改浏览器认证状态。

## 配置

唯一生产 Provider 是 `playwright-extension`：

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

```bash
python3 -m unittest scripts/test_browser_bridge.py
npm test --prefix tools/browser-bridge
npm test --prefix tools/browser-controller
npm run test:email-scripts
cd services/gateway && go test ./internal/browserautomation ./internal/browsercontrol ./internal/emailautomation ./internal/gateway ./internal/toolhub
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
