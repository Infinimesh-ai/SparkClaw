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
| `browser.internet_search` r2 | 通过 `web.search` 搜索公开当前信息，不打开来源页面 |
| `browser.weather` r2 | 通过 Infinimesh Info `POST /v1/info/weather` 查询 typed metric 数据，并为一个明确地点生成天气卡片 |
| `browser.automation` r3 | 取得明确 URL、注册 destination 或由 Info 识别的命名公网目标，在 hidden Chromium 校验后，再在 visible Chromium 呈现并验证 |
| `browser.page_read` r1 | 执行固定 hidden health -> open -> session-required read 链，从一个明确或经 Info 识别的 URL 返回有界内容 |
| `browser.interaction` r3 | 在托管 acquisition/presentation 链中执行最多三次受限、ref-bound click，并独立验证 transition 与目标 |
| `browser.form_draft` r2 | 先在 hidden Chromium 中发现并评估普通可逆字段，再在同一 visible session 中 type 或 select 最多五个经独立审批、由 owner 原文提供的精确值，并原位验证未提交草稿 |

`browser.interaction` r3 保持 click-only。`browser.form_draft` r2 只在 action stage 暴露
type/select，绝不暴露 click、submit、send、publish、upload、download、凭据、captcha/2FA、
payment/purchase 或页面脚本动作。登录和人工验证使用显式 owner handoff。底层 browser
tool 不会扩大已支持 Workflow 表面；以
[Workflow 能力矩阵](workflow-capabilities.md)为准。浏览器 r1 profile 及其完成后呈现
兼容路径已经退役。

## 页面、草稿与视觉证据

浏览器证据分为不同契约：

- `browser.read` 提取有界 rendered text 和 typed page metadata。`browser.page_read`
  Profile 只会在 hidden health/open 之后调用，并设置 `require_browser_session=true` 和
  `reuse_active_page=true`；托管 session 失败会明确返回，绝不回退 direct HTTP。
- `browser.wait` 根据有界可观察 readiness signal 等待 navigation 或 interaction 稳定；
  timeout、renderer failure 或 caller cancellation 会明确终止当前阶段。
- `browser.snapshot` 为选定页面状态创建带可执行 wrapped ref、page identity、
  presentation mode、session generation 和 page generation 的结构化 accessibility
  projection。snapshot ID 包含 session generation；navigation 和成功 interaction 会推进
  page generation，并使旧 action/visual evidence 失效。
- Runtime 保留这份带完整 identity 的 snapshot，用于 freshness 与执行 binding。目标/control
  模型调用只接收另一份投影，其中包含有界 title/count/omission 状态，以及 candidate 局部
  role、label、state、nearby context、options 和当前 snapshot 内的短 `ref`；Runtime 会在
  validation 前展开该 ref。page/snapshot ID、URL、digest、fingerprint、generation 和 ordinal
  不会进入该投影。
- 目标判定 citation 只能选择当前 snapshot 返回的短 ref；tool-call ID 和 artifact URI 不是
  浏览器证据。
- `browser.click` 只能接收该 snapshot 中持久化的 ref。
- `browser.type` 与 `browser.select` 只有在 `browser.form_draft` 中才能作为页面 mutation
  使用。Runtime 在 approval 前和已审批 call 执行时都会校验 active Profile、latest
  snapshot、page/ref identity、session/page generation、普通字段 allowlist、禁用 control
  metadata 和 owner 原文中的精确值。每次 action 独立审批；public summary 和持久化浏览器
  projection 会脱敏 value。
  模型可见 type/select schema 保留有界当前 `uid` enum 与语义 value field；Runtime 移除并在
  执行前回绑 page ID、snapshot ID 与 session/page generation 参数。
- Workflow-only `browser.visual_inspect` 先校验最新 structured snapshot，捕获 screenshot，
  复用 Fast 图片检查，再捕获一份 structured snapshot。session/page generation、page ID、
  URL 或 snapshot digest 任一变化都会返回 `visual_evidence_stale`。其有界不可信输出不包含
  coordinate 或 executable ref。当前 Profile 只有在 owner 明确要求 screenshot 或视觉确认
  时才暴露该 stage。
- `browser.validate_transition` 对比持久化 before/after snapshot。点击之后，
  `browser.assess_goal` 接收以 transition 为中心的投影，其中包含 action 的语义 label/role、
  显式确定性 transition assertion 和有界的相关 after-state control。Runtime 要求每个
  transition boolean 都有明确的 true 断言，并继续把完整 ref、URL、digest 和 generation
  排除在模型契约之外。
- 每次 navigation 与 click 后都必须 settle 并生成 fresh snapshot。stale generation/ref、
  repeated state、重复已验证的语义 action、route divergence 或 semantic evidence 缺失都会
  通过类型化 outcome fail closed。

settle 与 snapshot 使用同一份 `content_digest`：只对 rendered title 与 body 计算摘要。
URL 独立校验且不进入摘要，因此仅有 hash route 或地址栏变化不能证明页面状态已更新。
目标评估也不会把名称匹配的可点击控件当作完成证据；在任何已验证点击之前，如果证据
全部只是 actionable ref，结果会被确定性降级为 `progress/action_required`。

Agent-browser 的 accessibility snapshot 和 native ref 是 provider 侧交互事实。SparkClaw
只在其上增加有界模型投影、相关性检查、page identity、semantic fingerprint、重复状态
检测和明确错误码。页面文本始终是不可信证据，不会成为指令来源。

## 注册目标与动态公网目标

现有 destination registry 及其匹配行为保持不变，并始终作为命名目标的第一层 lookup。
registry hit 保留原有 descriptor、host scope、route hint 和认证处理。向 registry 增加网站
只是数据维护，不会增加 Catalog leaf 或 semantic candidate，因此不会增加 Top-2 意图选择成本。

托管浏览器 leaf 选定后，未注册的命名公网目标执行一次有界 Info-backed `web.search`。
Runtime 只消费已持久化、有序的 `results[].url` 字段，并选择第一个通过强制安全检查的 URL。
它不会从 answer prose 或 snippet 解析 URL，不会调用 Fast/embedding、重排相关性，也不会把
动态结果写回 registry。识别延迟是一轮 Info 请求加有界 DNS/redirect 检查；明确 URL、当前
tab 和 registry hit 都会跳过该阶段。

动态目标必须使用无 userinfo 的 HTTPS。hostname、每个 resolved address、每次 redirect 和
最终 URL 都必须位于公网；loopback、private、link-local、multicast 和 unspecified address
会被拒绝。Provider 不可用、无可用有序结果和全部结果不安全会产生不同 typed failure，不会猜 URL。

## 托管 Chromium Profile

普通执行使用 headless。human-only verification 可以临时打开使用同一 SparkClaw-owned
profile 的 visible Chromium，但 hidden 与 visible 进程绝不能同时持有 profile。
认证状态保留在 Chromium 内。SparkClaw 不把凭据复制到其他进程，也不 attach owner 的
日常浏览器 profile。

在 Linux ARM64 Compose runtime 中，visible session 使用 owner 的真实 X11/XWayland
桌面。desktop bridge 是一个 opt-in Compose overlay：
`docker/compose.visible-browser.yaml`；基础 `docker/compose.yaml` 不挂载 X socket，
也不向 Gateway 传递任何 display 环境变量。`npm run dev` 自动发现唯一的本地 display
及其 Xauthority 文件，并应用该 overlay，把 X socket 和 authority 挂载进 Gateway；
headless 主机上则以不带 overlay 的相同 stack 启动，只提供 hidden 自动化。
adapter 对 visible session 禁用 agent-browser 的 Xvfb
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
下方关闭并重新拉起 Chromium。visible session 使用单独的六倍有限 idle bound；默认配置下为
两小时，因此被遗弃的 presentation process 不会永久存活。

Binding-scoped 微信 QR 登录窗口使用更严格的 ToolHub lifecycle。每次成功 open 或 navigate 都会
获得固定 10 分钟 sliding lease，并由更早的 binding expiry 截短。Janitor 每 30 秒清理过期
session，不会只为检测 owner 手动关闭窗口而轮询 tab。Registry 为每个
`(owner_id, binding_id)` 使用一个 operation lock，因此无关 QR open 不会在 browser round trip
后方串行。Poll 观察到 terminal state 或 revoke 时仍立即释放。Graceful ToolHub shutdown 会
停止 janitor，并在关闭 adapter 前排空所有 tracked window；ungraceful exit 则在下次
acquisition 时依赖 deterministic leaked-profile recovery。

session startup 取得 exclusive profile lock 后，还会校验 Chromium 原生的
`SingletonLock`、`SingletonSocket` 和 `SingletonCookie`。同主机 PID 仍存活或 Unix
socket 可连接时继续报告 busy；只有确认没有存活 owner 的失效符号链接才会被移除。
格式异常条目、普通文件和无法判定的 ownership 一律 fail closed。这样，重建后的 Gateway
可以回收已终止容器遗留的 profile，而不会抢占仍在运行的浏览器。

browser automation 和 interaction 会在 hidden Chromium 中完成 acquire、navigate、settle、
snapshot 与 action，再进入必需的 visible 结果呈现。form draft 只在 hidden Chromium 中取得
目标、捕获初始结构化 control 并评估剩余动作；Runtime 会在任何已审批 `browser.type` 或
`browser.select` 之前以 visible 方式打开目标。之后每次已审批 mutation、settle、更高 generation
snapshot 与目标评估都留在同一个 visible session 中，最终原位验证未提交状态，不会重新打开
目标而丢失草稿。

visible 呈现是每个适用冻结 Workflow 的必需节点：Runtime 等待结果稳定、获取 visible
snapshot 并重新校验冻结 route。对于 interaction，owner/profile identity、route 和
rendered-content digest 都匹配时，Runtime 会生成类型化 presentation-equivalence assertion，
并复用已经验证的 hidden goal verdict，不再调用模型；visible 结果存在实质差异时仍会执行一次
独立的有界目标评估。Form draft 继续原位验证它修改后的 visible 状态。没有 visible evidence
或对应的等价 assertion，run 不能成功。`browser.page_read` 有意保持不同：其 health/open/read 全链路都是
hidden，成功读取不会创建 visible 结果窗口。全新的 visible session 会直接导航目标，不先暴露
启动时的 `about:blank` tab；已经初始化且可复用的 profile 不会被替换为空白登录提示。已验证
visible 结果页面在 Workflow 完成后继续保持打开，但仍受更长的 visible-session idle bound
约束；生产完成流程不会调用 `browser.close`。

持久化的安全结果 descriptor 保存 origin、path、路由型 fragment（`#/...` 页内路由；
携带值的 fragment 如 OAuth `#access_token=...` 会被丢弃）和 query provenance，不保存
provider session token。QQ 邮箱等应用可能在新进程中替换易失 `sid`；Runtime 保留新的
session query，只重新应用已验证的同源 hash route，并从 artifact、audit、episode 和 API
响应中移除 provider 注入 token。owner 明确提供的 query parameter 仍属于冻结目标。
fresh visible process 需要重新应用这类 route 时，Runtime 会执行一次原生 reload，并要求
rendered content digest 确实发生变化且稳定后，才允许继续呈现。

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

- 明确目标必须是规范化 HTTP(S) URL。注册 destination 解析成冻结 runtime URL 和受限
  host/subdomain 规则；动态 Info 目标要求公网 HTTPS，并保留 result-order provenance。
- URL fetch 默认拒绝 loopback、private、link-local 等禁止 literal host；本地 fixture 必须明确 allowlist。
- redirect 和最终 page identity 会重新校验。
- 不复用无关现有 tab。成功 open 或 interaction 后，最终结果页面保持打开；关闭 tab
  只用于测试清理。
- `browser.status` 是被动检查：它验证固定 provider、system Chromium 版本与 AArch64
  ELF、profile lock 可用性、UTF-8/CJK 支持，以及需要 visible 时的 DISPLAY socket 和
  Xauthority 文件，不启动 Chromium，也不创建 `about:blank`。
- 即使 snapshot 中存在 unsafe 或 consequential control，也会阻止。模型输出不能绕过 ref ownership 或 Policy。
- screenshot、raw response 和 rendered text 都是 artifact/evidence，不是可信指令。
- 基础 Compose 文件不向 Gateway 暴露任何 X11 socket 或 Xauthority。
  `docker/compose.visible-browser.yaml` overlay 以 read-only 方式挂载两者，但仍会
  授予 Gateway 访问 owner 桌面 display 的权限。只在受信任、单 owner 的本地 runtime
  中应用该 overlay。

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
| `SPARKCLAW_BROWSER_DISPLAY` | 仅用于 visible-browser overlay 的 Linux host display，例如 `:1` |
| `SPARKCLAW_BROWSER_XAUTHORITY` | 仅用于 visible-browser overlay 的可读 host Xauthority 文件（无默认值） |

环境变量覆盖使用 `SPARKCLAW_BROWSER_AUTOMATION_*`、
`SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`、`SPARKCLAW_BROWSER_PROFILE_DIR` 和
`SPARKCLAW_BROWSER_READ_ALLOW_HOSTS`。常规 host 与 Compose 命令见[部署](deployment.md)。

`npm run dev` 与 `scripts/start_cloud_compose.sh` 都会自动解析这两个 desktop 值并应用
`docker/compose.visible-browser.yaml` overlay；没有本地 display 时，cloud 脚本保留
hidden-only 运行。直接调用 Compose 时先导出，并显式叠加 overlay：

```bash
mapfile -t browser_display < <(scripts/resolve-browser-display.sh)
export SPARKCLAW_BROWSER_DISPLAY="${browser_display[0]}"
export SPARKCLAW_BROWSER_XAUTHORITY="${browser_display[1]}"
docker compose --env-file .env -f docker/compose.yaml \
  -f docker/compose.visible-browser.yaml --profile models-local up -d gateway
```

不带 overlay 时，同一命令启动的是完全 headless、无法访问 host 桌面的 Gateway。

## 验证

浏览器改动应覆盖：adapter 协议、timeout、进程 ownership 和 profile lock；managed QR-window
lease 续期/过期、per-key locking、janitor retry、stale generation 与 shutdown ordering/race；被动 Linux
ARM64 environment preflight 与 reason code；settle timeout/cancellation、snapshot
规范化和不可信证据；明确 URL、注册 destination、tab focus 和 redirect；stale
generation/ref、重复状态、unsafe control 和尝试上限；固定 hidden page-read 顺序、active-page
reuse、required-session 禁止 HTTP fallback、final URL 校验、有界原文 fallback 和 login resume；
Info 有序结果消费、不安全结果跳过、仅结构化 URL 绑定、provider failure、DNS/IP/redirect
安全与 registry fast path；form-draft hidden discovery 后在同一 visible session 中执行
mutation、精确值、独立 approval、public redaction、禁用字段、五次上限、无 click exposure、
approval 前后 freshness，以及不重新打开表单的原位最终验证；visual inspection fresh/stale、
generation/digest 绑定及不输出 coordinate/executable ref；visible/hidden transfer、owner reply
classification、restart recovery、CAS conflict 和 post-login 页面匹配/不匹配；UTF-8/CJK
与 QQ 邮箱中文 snapshot/ref/auth evidence 往返；private-host 拒绝与 fixture allowlist；
Workflow 路由和 stage-scoped tool exposure；以及 `npm run setup:browser`、Gateway 测试、
WebChat 测试/build 和可用时的 golden browser eval。
