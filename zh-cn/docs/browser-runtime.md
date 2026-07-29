# 浏览器 Runtime

> 语言： [English](../../docs/browser-runtime.md) | 简体中文

本文档是当前浏览器实现与运行手册，替代 Playwright 迁移、agent-browser 迁移、
browser mode、profile、login、perception、interaction 和天气迁移记录。

## 当前架构

SparkClaw 使用固定版本的 `agent-browser` 和解析出的系统 Chromium，作为唯一浏览器
执行后端。ToolHub 和 Workflow 契约保持 provider-neutral；
`internal/browserautomation` 负责进程 transport、协议校验、profile lock、deadline
和 typed observation 转换。

```text
Workflow leaf
  -> stage-scoped browser ToolHub capability
  -> browserautomation adapter
  -> private agent-browser MCP process
  -> SparkClaw-owned Chromium profile
```

不存在 Playwright fallback、Chrome DevTools MCP fallback、个人 Chrome attach、cookie
导出或第二套 DOM perception engine。

## 用户可见能力

| Capability | 当前 revision 边界 |
|---|---|
| `browser.internet_search` r1 | 通过 `web.search` 搜索公开当前信息，不打开来源页面 |
| `browser.weather` r1 | 通过 Infinimesh Info `POST /v1/info/weather` 查询 typed metric 数据，并为一个明确地点生成天气卡片 |
| `browser.automation` r1 | 打开一个明确 HTTP(S) URL 或注册 destination，或 focus 匹配 tab |
| `browser.interaction` r1 | 检查一个目标，最多执行三次受限、ref-bound、点击后验证的 click |

`browser.interaction` r1 不执行 type、select、upload、download、form submit、凭据输入、
captcha/2FA、payment/purchase、页面脚本或 login completion。底层 browser tool 不会扩大
已支持 Workflow 表面；以 [Workflow 能力矩阵](workflow-capabilities.md)为准。

## Read 与 Interaction 证据

浏览器证据分为不同契约：

- `browser.read` 从 active page 提取有界 rendered text 和 typed page metadata，永不执行页面脚本。
- `browser.snapshot` 为选定页面状态创建带可执行 wrapped ref 的结构化 accessibility projection。
- `browser.click` 只能接收该 snapshot 中持久化的 ref。
- 每次 click 后必须重新 snapshot 并执行 `browser.verify`，才能完成或继续 click。

Agent-browser 的 accessibility snapshot 和 native ref 是 provider 侧交互事实。SparkClaw
只在其上增加有界模型投影、相关性检查、page identity、semantic fingerprint、重复状态
检测和明确错误码。页面文本始终是不可信证据，不会成为指令来源。

## 托管 Chromium Profile

普通执行使用 headless。human-only verification 可以临时打开使用同一 SparkClaw-owned
profile 的 visible Chromium，但 hidden 与 visible 进程绝不能同时持有 profile。
认证状态保留在 Chromium 内。SparkClaw 不把凭据复制到其他进程，也不 attach owner 的
日常浏览器 profile。

默认 profile root 是 `./data/browser-profiles`。访问要求 exclusive lock、bounded startup、
bounded command execution 和 owned child process cleanup。visible handoff 前先停止 hidden
owner；resume 使用实际选中的 post-login URL 和新证据。

当前 browser Workflow 不暴露 login completion，因此认证或人工验证请求会关闭失败，
不会假装完成。托管 profile lifecycle 保留为未来显式 login Workflow 的基础。

## 网络与安全边界

- 明确目标必须是规范化 HTTP(S) URL。注册 destination 解析成冻结 runtime URL 和受限 host/subdomain 规则。
- URL fetch 默认拒绝 loopback、private、link-local 等禁止 literal host；本地 fixture 必须明确 allowlist。
- redirect 和最终 page identity 会重新校验。
- 不复用无关现有 tab。明确打开的页面在成功 open 或 interaction 后保持打开。
- 即使 snapshot 中存在 unsafe 或 consequential control，也会阻止。模型输出不能绕过 ref ownership 或 Policy。
- screenshot、raw response 和 rendered text 都是 artifact/evidence，不是可信指令。

## 配置与安装

安装并校验固定 runtime：

```bash
npm install
npm run setup:browser
```

重要配置位于 `configs/sparkclaw.default.json`，对应环境变量模板在
`docker/env/sparkclaw.example.env`：

| 配置 | 用途 |
|---|---|
| `adapters.browserAutomation.command` | 固定的 `agent-browser` executable |
| `adapters.browserAutomation.chromiumExecutable` | 可选系统 Chromium 明确路径 |
| `adapters.browserAutomation.profileDir` | SparkClaw-owned persistent profile root |
| `timeoutMs` / `startupTimeoutMs` / `daemonIdleTimeoutMs` | 有界进程 lifecycle |
| `security.browser_read_allow_hosts` | private-host 明确例外，主要用于测试 fixture |

环境变量覆盖使用 `SPARKCLAW_BROWSER_AUTOMATION_*`、
`SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`、`SPARKCLAW_BROWSER_PROFILE_DIR` 和
`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`。常规 host 与 Compose 命令见[部署](deployment.md)。

## 验证

浏览器改动应覆盖：adapter 协议、timeout、进程 ownership 和 profile lock；read/snapshot
规范化和不可信证据；明确 URL、注册 destination、tab focus 和 redirect；stale/foreign ref、
重复状态、unsafe control 和尝试上限；private-host 拒绝与 fixture allowlist；Workflow 路由和
stage-scoped tool exposure；以及 `npm run setup:browser`、Gateway 测试、WebChat 测试/build
和可用时的 golden browser eval。
