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
| `browser.automation` r2 | 在 hidden Chromium 中取得一个目标并完成 settle/snapshot，再在 visible Chromium 中呈现并独立验证同一结果 |
| `browser.interaction` r2 | 在相同 acquisition/presentation 链中执行最多三次受限、ref-bound click，并独立验证 transition 与目标 |

`browser.interaction` r2 不执行 type、select、upload、download、form submit、凭据输入、
captcha/2FA、payment/purchase、页面脚本或自动登录。登录和人工验证使用显式 owner
handoff。底层 browser tool 不会扩大已支持 Workflow 表面；以
[Workflow 能力矩阵](workflow-capabilities.md)为准。浏览器 r1 profile 及其完成后呈现
兼容路径已经退役。

## Read 与 Interaction 证据

浏览器证据分为不同契约：

- `browser.read` 为非 Workflow 调用者从 active page 提取有界 rendered text 和 typed page
  metadata，永不执行页面脚本，也不是 r2 的第二条 perception 路径。
- `browser.wait` 根据有界可观察 readiness signal 等待 navigation 或 interaction 稳定；
  timeout、renderer failure 或 caller cancellation 会明确终止当前阶段。
- `browser.snapshot` 为选定页面状态创建带可执行 wrapped ref、page identity、
  presentation mode 和 session generation 的结构化 accessibility projection。
- `browser.click` 只能接收该 snapshot 中持久化的 ref。
- `browser.validate_transition` 对比持久化 before/after snapshot；
  `browser.assess_goal` 针对一个精确 snapshot 独立评估冻结目标。
- 每次 navigation 与 click 后都必须 settle 并生成 fresh snapshot。stale generation/ref、
  repeated state、route divergence 或 semantic evidence 缺失都会 fail closed。

Agent-browser 的 accessibility snapshot 和 native ref 是 provider 侧交互事实。SparkClaw
只在其上增加有界模型投影、相关性检查、page identity、semantic fingerprint、重复状态
检测和明确错误码。页面文本始终是不可信证据，不会成为指令来源。

## 托管 Chromium Profile

普通执行使用 headless。human-only verification 可以临时打开使用同一 SparkClaw-owned
profile 的 visible Chromium，但 hidden 与 visible 进程绝不能同时持有 profile。
认证状态保留在 Chromium 内。SparkClaw 不把凭据复制到其他进程，也不 attach owner 的
日常浏览器 profile。

在 Linux ARM64 Compose runtime 中，visible session 使用 owner 的真实 X11/XWayland
桌面。`npm run dev` 自动发现唯一的本地 display 及其 Xauthority 文件，再将 X socket
和 authority 挂载进 Gateway。adapter 对 visible session 禁用 agent-browser 的 Xvfb
fallback：desktop 缺失或不可访问时会明确失败，不再把只能在虚拟显示中看到的浏览器
报告为成功。headless 自动化不依赖该 desktop bridge。Gateway 镜像同时提供 UTF-8
locale 以及 Noto CJK/emoji 字体，使 Chromium 能正确显示 QQ 邮箱等中文应用，而不会
出现缺字方框。

默认 profile root 是 `./data/browser-profiles`。每个 session 在整个进程生命周期内持有
exclusive OS file lock；发生 contention 时明确失败，不会启动第二个 owner。访问同时要求
bounded startup、bounded command execution 和 owned child process cleanup。Compose
Gateway 使用 init 进程回收 browser session 退出后的 Chromium 后代进程。visible handoff
前先停止 hidden owner；转回 hidden 时也遵循相同顺序。

hidden Chromium 默认使用 20 分钟的 daemon idle window。配置加载会要求该窗口覆盖
snapshot 与其绑定 click 之间可能连续出现的两个 model-owned stage，并计入已配置的
model request 与 Workflow step 上限。这样，较慢的模型推理不会在仍有效的 snapshot
下方关闭并重新拉起 Chromium。visible session 不使用 daemon idle timeout，会继续为
owner 保持打开。

session startup 取得 exclusive profile lock 后，还会校验 Chromium 原生的
`SingletonLock`、`SingletonSocket` 和 `SingletonCookie`。同主机 PID 仍存活或 Unix
socket 可连接时继续报告 busy；只有确认没有存活 owner 的失效符号链接才会被移除。
格式异常条目、普通文件和无法判定的 ownership 一律 fail closed。这样，重建后的 Gateway
可以回收已终止容器遗留的 profile，而不会抢占仍在运行的浏览器。

browser automation 和 interaction 在 hidden Chromium 中完成 acquire、navigate、settle、
snapshot 和 interaction。最终呈现是同一冻结 r2 Workflow 内的必需节点：Runtime 以 visible
方式 open/focus 结果 URL，等待稳定，获取 visible snapshot，重新校验冻结 route；interaction
还会独立复核目标。没有这些 visible evidence，run 不能成功。全新的 visible session 会
直接导航目标，不先暴露启动时的 `about:blank` tab；已经初始化且可复用的 profile 不会被
替换为空白登录提示。已验证结果页面不受 headless daemon 空闲超时影响并保持打开，生产
完成流程不会调用 `browser.close`。

持久化的安全结果 descriptor 保存 origin、path、路由型 fragment（`#/...` 页内路由；
携带值的 fragment 如 OAuth `#access_token=...` 会被丢弃）和 query provenance，不保存
provider session token。QQ 邮箱等应用可能在新进程中替换易失 `sid`；Runtime 保留新的
session query，只重新应用已验证的同源 hash route，并从 artifact、audit、episode 和 API
响应中移除 provider 注入 token。owner 明确提供的 query parameter 仍属于冻结目标。

browser tool 检测到登录或人工验证界面后，Runtime 持久化 handoff，暂停原 Workflow，
并要求 owner 在 visible Chromium 中完成验证。歧义回复不会产生任何 browser call；明确
取消会保留 visible 页面；明确报告页面错误会重新打开冻结目标。只有明确确认完成才进入
校验。

校验先列出 visible tab、选择 handoff 页面、等待稳定并获取 fresh visible snapshot，再
独立检查认证证据以及当前页面是否仍满足冻结任务。显式 URL 仍要求精确匹配；registered
destination 只能使用其受限 host/subdomain 规则。页面缺失、未认证或与任务无关时，
Workflow 保持暂停并明确反馈，不启动 hidden 自动化。visible 校验通过后，Runtime 把
profile 转交 hidden，重新取得选中页面、settle 并生成另一个 fresh snapshot。profile
连续性丢失时回到 `waiting_owner`，不会猜测。登录前 ref 全部丢弃，但已完成 click budget
保持不变。

handoff transition 持久化为 `waiting_owner`、`reopening_visible`、
`validating_visible`、`transferring_profile`、`validating_hidden` 和
`resuming_workflow`，最终进入 `resolved`、`canceled` 或 `failed`。Store
compare-and-swap、transition owner 和有界 lease 让 memory、file、PostgreSQL backend
上的 retry 与 Gateway restart recovery 保持单 owner、幂等。login completion 是 Runtime
管理的用户确认 gate，不是 model-visible tool。

## 网络与安全边界

- 明确目标必须是规范化 HTTP(S) URL。注册 destination 解析成冻结 runtime URL 和受限 host/subdomain 规则。
- URL fetch 默认拒绝 loopback、private、link-local 等禁止 literal host；本地 fixture 必须明确 allowlist。
- redirect 和最终 page identity 会重新校验。
- 不复用无关现有 tab。成功 open 或 interaction 后，最终结果页面保持打开；关闭 tab
  只用于测试清理。
- `browser.status` 是被动检查：它验证固定 provider、system Chromium 版本与 AArch64
  ELF、profile lock 可用性、UTF-8/CJK 支持，以及需要 visible 时的 DISPLAY socket 和
  Xauthority 文件，不启动 Chromium，也不创建 `about:blank`。
- 即使 snapshot 中存在 unsafe 或 consequential control，也会阻止。模型输出不能绕过 ref ownership 或 Policy。
- screenshot、raw response 和 rendered text 都是 artifact/evidence，不是可信指令。
- Compose Xauthority 虽然是 read-only mount，但会授予 Gateway 访问 owner 桌面 display
  的权限。只在受信任、单 owner 的本地 runtime 中启用 visible forwarding。

## 配置与安装

安装并校验固定 runtime：

```bash
npm install
npm run setup:browser
```

Linux setup 检查还要求 fontconfig 和已安装的中文字体。Debian 和 Ubuntu 主机可安装
`fontconfig` 与 `fonts-noto-cjk`。

重要配置位于 `configs/sparkclaw.default.json`，对应环境变量模板在
`docker/env/sparkclaw.example.env`：

| 配置 | 用途 |
|---|---|
| `adapters.browserAutomation.command` | 固定的 `agent-browser` executable |
| `adapters.browserAutomation.chromiumExecutable` | 可选系统 Chromium 明确路径 |
| `adapters.browserAutomation.profileDir` | SparkClaw-owned persistent profile root |
| `timeoutMs` / `startupTimeoutMs` / `daemonIdleTimeoutMs` | 有界 lifecycle；hidden idle 覆盖已配置的 model/Workflow reasoning gap |
| `security.browser_read_allow_hosts` | private-host 明确例外，主要用于测试 fixture |
| `SPARKCLAW_BROWSER_DISPLAY` | 仅用于 Compose 的 Linux host display，例如 `:1` |
| `SPARKCLAW_BROWSER_XAUTHORITY` | 仅用于 Compose 的可读 host Xauthority 文件 |

环境变量覆盖使用 `SPARKCLAW_BROWSER_AUTOMATION_*`、
`SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`、`SPARKCLAW_BROWSER_PROFILE_DIR` 和
`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`。常规 host 与 Compose 命令见[部署](deployment.md)。

`npm run dev` 会自动解析这两个仅用于 Compose 的 desktop 值。直接调用 Compose 时先导出：

```bash
mapfile -t browser_display < <(scripts/resolve-browser-display.sh)
export SPARKCLAW_BROWSER_DISPLAY="${browser_display[0]}"
export SPARKCLAW_BROWSER_XAUTHORITY="${browser_display[1]}"
docker compose --env-file .env -f docker/compose.yaml --profile models-local up -d gateway
```

## 验证

浏览器改动应覆盖：adapter 协议、timeout、进程 ownership 和 profile lock；被动 Linux
ARM64 environment preflight 与 reason code；settle timeout/cancellation、snapshot
规范化和不可信证据；明确 URL、注册 destination、tab focus 和 redirect；stale
generation/ref、重复状态、unsafe control 和尝试上限；visible/hidden transfer、owner reply
classification、restart recovery、CAS conflict 和 post-login 页面匹配/不匹配；UTF-8/CJK
与 QQ 邮箱中文 snapshot/ref/auth evidence 往返；private-host 拒绝与 fixture allowlist；
Workflow 路由和 stage-scoped tool exposure；以及 `npm run setup:browser`、Gateway 测试、
WebChat 测试/build 和可用时的 golden browser eval。
