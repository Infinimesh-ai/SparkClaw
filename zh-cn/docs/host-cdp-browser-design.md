# Host-CDP 浏览器设计

> 语言： [English](../../docs/host-cdp-browser-design.md) | 简体中文

## 状态

本方案于 2026-09-02 完成实施，记录替代旧实现的浏览器 transport、宿主机进程生命周期、
标签页所有权模型和认证 handoff。它不定义任何业务专用的浏览器能力。

Host-CDP 现为唯一受支持的浏览器 runtime。两条正式部署入口
`scripts/deploy_local.sh` 和
`scripts/deploy_remote.sh` 统一配置唯一的 Host-CDP transport，并在宿主机安装或校验
Chromium。Gateway 镜像不再包含 Chromium。Container-managed 的实现、配置、打包、
Compose wiring、启动逻辑、Profile ownership 代码和专用测试全部删除，不保留
compatibility image 或 overlay。

## 背景

本次 cutover 之前，SparkClaw 在 Gateway 容器内启动固定版本的 `agent-browser` 和
Chromium。持久 Profile 位于宿主机并挂载进容器。Hidden 与 visible Chromium 进程按
顺序独占该 Profile，不能并发运行。

该设计适合隔离自动化和云部署，但无法提供一份普通桌面浏览器，让 owner 正常使用
的同时，SparkClaw 在另一标签页复用相同认证状态。期望的桌面行为是：

- owner 在正常的 SparkClaw 专用 Chromium 窗口中登录；
- 一个宿主机 Chromium 进程独占一份专用 Profile；
- `agent-browser` 通过 CDP attach 到该进程；
- owner 标签页和 SparkClaw 标签页共享认证，但有明确所有权边界；
- 现有受治理 browser operation 继续通过 `agent-browser` 和 provider-neutral browser
  adapter 执行。

## 目标

1. 在不复制 Cookie、密码或 Storage 记录的情况下复用登录态。
2. 每份 SparkClaw Profile 始终只有一个 Chromium 进程。
3. 保持 `agent-browser` 为唯一浏览器自动化后端。
4. 有桌面 display 时提供带地址栏、标签页、下载、书签和普通 owner 导航的正常
   宿主机浏览器；无 display 的服务器仍保留宿主机拥有的 headless 运行。
5. 防止自动接管无关 owner 标签页。
6. 保持现有 Workflow、ToolHub、Policy、Approval、证据和 typed failure 边界。
7. 同时支持直接宿主机开发和 Compose Gateway，且不把无认证 CDP endpoint 暴露
   到局域网。
8. Host-CDP 通过资格验证后，完整退役 container-managed 浏览器。

## 非目标

- Attach 到 owner 日常 Chrome 或 Chromium 的默认 Profile。
- 导入或导出 Cookie、密码、Passkey 或 Browser Storage。
- 让两个 Chromium 进程使用同一 Profile。
- 把一段时间没有鼠标键盘输入视为接管 owner 标签页的授权。
- 增加 Playwright、第二套 DOM collector 或模型生成的浏览器代码。
- 在 browser transport 之上定义业务专用 provider、script、setting、tool 或 Workflow。

## 决策

浏览器自动化迁移到唯一受支持的 runtime：

| Runtime | Chromium 所有者 | Profile 所有者 | agent-browser transport |
|---|---|---|---|
| `host-cdp` | 宿主机 `sparkclaw-browserd` | 宿主机 browser daemon | 通过 CDP attach 到现有 Chromium |

资格验证期间，现有实现可以位于显式的仅迁移 selector 后，以保证当前 release 仍可
运行。该 selector 不进入最终产品配置。Browserd 或 endpoint 不可用时，Host-CDP
fail closed，绝不能在 Gateway 内启动替代 Chromium。

通过 rollout gate 后执行的是删除，而不是无限期共存。正式 DGX Spark 与 Ubuntu VM
部署只使用 Host-CDP，仓库不再交付或测试 container-managed runtime。

## 目标拓扑

```text
宿主机桌面
  -> SparkClaw Browser launcher
  -> sparkclaw-browserd
       -> 唯一普通 Chromium 进程
            -> SparkClaw 专用 Profile
            -> owner 标签页
            -> SparkClaw-owned 标签页
       -> 私有 Chromium CDP endpoint
       -> capability-gated CDP proxy

Gateway 容器
  -> browserautomation adapter
  -> 私有 agent-browser MCP 进程
       AGENT_BROWSER_CDP=<browserd capability WebSocket URL>
  -> browserd CDP proxy
  -> 现有宿主机 Chromium 进程
```

Chromium 进程和 Profile 生命周期长于单次 Gateway 与 `agent-browser` MCP 进程。
重启或停止 Gateway 只断开自动化，不关闭 owner 浏览器。

## 宿主机 Browser Daemon

`sparkclaw-browserd` 是 user-scoped 宿主机服务，也是专用浏览器 Profile 唯一的进程
所有者。`SparkClaw Browser` 指经过资格验证的宿主机 Chromium 二进制，加上 SparkClaw
launcher、daemon 和专用 Profile，不是 Chromium 私有 fork。Daemon 必须：

1. 解析并校验受支持的 Chromium executable；
2. 使用专用非默认 User Data Directory 和临时 loopback remote-debugging port 启动
   Chromium；
3. 保留唯一进程身份，并拒绝同一 Profile 的第二个 owner；
4. 从 Chromium runtime 状态发现 browser-level CDP WebSocket endpoint，而不是假定
   固定端口；
5. 通过本地 control socket 提供有界 `status`、`open-or-focus` 和 `shutdown`；
6. 为 Gateway 提供 capability-gated CDP proxy，且不发布原始 Chromium debugging
   port；
7. browserd 每次启动时选择新的不透明浏览器 generation epoch，并在每次替换浏览器
   进程时递增 generation、轮换 proxy capability；
8. 确保 browser log、Profile path 和 CDP URL 不含携带凭据的 query value。

Daemon 以桌面 owner 身份运行，不使用 root。名为 `SparkClaw Browser` 的桌面启动器
调用 `open-or-focus`；重复启动只聚焦现有窗口，不创建第二个进程。Kiosk 和 app mode
不作为默认，因为这是一份 owner 可正常使用的工作浏览器。

Browserd presentation 使用 `auto`：能够校验 owner 桌面 display 时启动普通 headed
Chromium，否则启动宿主机拥有的 `--headless=new` Chromium。Headless 与 headed 之间
切换时，由 browserd 按顺序重启并继续使用同一 Profile，绝不并发启动第二个进程。
无 display 的服务器保留 hidden browser automation，但在出现可用桌面前不能执行
visible owner handoff。

## CDP Transport 边界

Chromium 原始 debugging listener 只绑定宿主机 loopback。Browserd 在本地连接它，
再把独立 WebSocket proxy 暴露到 Gateway 所需的最小接口：

- Gateway 直接运行在宿主机：只使用 loopback；
- Compose Gateway：只使用 SparkClaw 专用 Docker bridge 地址。

Proxy URL 包含高熵、短期 capability path。当前 capability 写入权限为 `0600` 的
runtime 文件，并以只读方式挂载到 Gateway。Endpoint 与 capability 必须从 API
response、log、trace、artifact 和 health detail 中脱敏。其他容器不接入浏览器控制
网络，宿主机防火墙拒绝局域网访问。

CDP 具有浏览器级完整权限；proxy 是认证与网络 containment 边界，不是细粒度授权层。
产品级授权仍由 SparkClaw 的标签页所有权、Workflow、ToolHub、Policy 和 Approval
负责。

## agent-browser 集成

固定版本的 `agent-browser` 继续作为唯一 execution 与 perception 后端。`host-cdp`
模式下，Gateway 使用以下环境启动私有 MCP 子进程：

```text
AGENT_BROWSER_CDP=<browserd browser-level WebSocket URL>
AGENT_BROWSER_NAMESPACE=<SparkClaw namespace>
AGENT_BROWSER_SESSION=<single host-CDP session>
```

该模式不得设置 `AGENT_BROWSER_PROFILE`、`AGENT_BROWSER_EXECUTABLE_PATH`、
`AGENT_BROWSER_HEADED` 或 Chromium launch argument。这些决策属于 browserd。

正常 adapter shutdown 会先针对唯一 SparkClaw session 调用 `agent_browser_close`，再停止
MCP 子进程。在 Host-CDP 模式下，该命令回收 session daemon 与 socket，只从外部 owner 的
Chromium detach；browserd 与 Chromium 保持运行。如果 MCP transport 已不健康，则直接
abort，并依赖有界 daemon idle timeout 回收。`browser.close` 只能关闭登记为
SparkClaw-owned 的标签页；关闭最后一个此类标签页时，不得关闭 Chromium 或 owner 标签页。

Adapter 不再获取现有 Profile lease，也不再执行 Chromium singleton cleanup，改由
browserd 提供 Profile/process health。Cutover 时删除旧 lease、singleton cleanup 和
本地 Chromium launch 路径。

## 标签页所有权

一个打开的 Chromium 进程不是授权边界。每个 Page Target 在自动化前必须分类：

| 分类 | 含义 | 自动化规则 |
|---|---|---|
| `owner` | 已存在或 owner 创建的普通标签页 | 不得自动选择或修改 |
| `sparkclaw` | SparkClaw 为冻结任务创建的标签页 | 可在有效 lease 下操作 |
| `handoff` | 暂时交给 owner 控制的 SparkClaw 标签页 | 自动化暂停，等待显式完成 |
| `released` | 已归还普通 owner 使用的原 SparkClaw 标签页 | 按 `owner` 处理，除非再次显式交接 |

Registry 保存稳定 CDP target ID、Profile ID、creator、分类、Workflow/run owner、
generation、lease deadline、最后验证 URL 与 content identity。数字标签页位置只用于
展示，不得作为持久 identity。Target 丢失或被替换时显式失败，不得 fallback 到 active
或第一个标签页。

系统不根据 inactivity 推断 owner consent。首期实现不接管任意 owner 标签页。未来可
增加显式 handoff，但必须指明一个当前 Target 并创建有界 lease。Owner interaction
detection 只是 defense in depth，不是授权来源。

普通自动化由 SparkClaw 在共享 Profile 中创建独立标签页。这样既能保留 Cookie 与
origin storage，又不会干扰 owner 当前页面。只有当 provider script 能证明所需认证
状态是 tab-local，且该标签页已经属于 SparkClaw 时，才允许请求 same-tab continuation。

## 认证 Handoff

Host-CDP 保持现有原则：认证是显式 owner action，浏览器状态始终留在 Chromium 中：

1. SparkClaw 为冻结 destination 创建 task-owned 标签页并切到前台。
2. 标签页进入 `handoff`，该 Target 上的自动化暂停。
3. Owner 在 SparkClaw Browser 中完成登录、captcha、2FA、consent 或其他人工交互。
4. Owner 通过现有 handoff surface 显式确认完成。
5. Runtime 重新选择同一个稳定 target ID，执行 settle、捕获新鲜证据，并同时校验认证
   状态与冻结 destination。
6. 校验成功后，标签页回到 `sparkclaw` 所有权，供等待中的 Workflow 继续；取消或显式
   release 后归为 `owner`。
7. Cookie、密码、Passkey、Local Storage 与 CDP credential 始终留在 Chromium 或
   browserd runtime state，不复制到 SparkClaw Store。

Owner 交互后，handoff 前的 ref 与 page generation 全部失效。Resume 必须使用新鲜
snapshot。无关、缺失或未认证页面继续保持暂停，不能用另一个打开的标签页替代。

## 配置形态

目标配置结构为：

```json
{
  "adapters": {
    "browserAutomation": {
      "hostCDP": {
        "endpointFile": "/run/sparkclaw/browserd/cdp-endpoint",
        "profileID": "default",
        "connectTimeoutMs": 10000
      }
    }
  }
}
```

最终配置不保留 browser mode enum，`hostCDP` 是唯一浏览器 transport 配置。产品配置
不接受静态 CDP WebSocket URL，因为它属于 credential-like、需要轮换的 runtime value。
配置校验必须拒绝缺失 endpoint file、所有人可读的 endpoint file、使用普通浏览器默认
Profile 的请求，以及 `mode=container-managed`、executable path、Profile launch
setting、headed flag 或 Chromium argument 等旧字段。部署迁移会删除这些字段；仍残留
的旧配置必须以明确的 Host-CDP migration error 拒绝启动。

Host-CDP 连接失败时报告 unavailable，绝不触发 container Chromium fallback。

## 部署与打包迁移

资格验证期间可以保留旧 release 可运行，但生产 cutover 必须把两条正式部署入口及其
共享启动路径作为一次原子迁移。若在 host browserd 与 CDP startup 尚未就绪时提前从
Gateway 镜像删除 Chromium，会直接破坏全部现有 browser Workflow，因此禁止分步切断。
全部资格验证门槛通过后，也禁止继续保留旧 runtime；最终验收还必须证明删除工作完整。

### 共享宿主机 Installer

两个部署脚本调用一份共享、幂等的宿主机 installer，不复制 package logic。目标 helper
为 `scripts/install-host-browser.sh`。它必须：

1. 根据宿主机 architecture，从版本控制的 manifest 解析经过批准并固定版本的
   SparkClaw Chromium artifact；
2. 在宿主机下载并校验 checksum，安装到
   `/opt/sparkclaw/chromium-<version>/chrome` 等带版本的 SparkClaw 专用路径；
3. 产品 runtime 不得使用 Ubuntu `/usr/bin/chromium-browser` Snap launcher，也不得
   使用 owner 日常系统浏览器；
4. 校验 executable type、architecture、version、sandbox startup、UTF-8、CJK/emoji
   font，以及使用全新专用 Profile 的启动；
5. 安装 `sparkclaw-browserd`、user service、桌面 launcher、runtime directory 和只允许
   owner 访问的 Profile directory；
6. 为 browserd health 记录已资格验证的 executable path 与 version，但不把这些值写入
   Gateway container 配置。

Installer 使用每个受支持 architecture 已资格验证的固定 artifact。已存在且校验通过的
相同版本会被复用，不在部署时下载任意 latest binary。版本变更需要执行与 agent-browser
版本变更相同等级的 browser qualification test。

### 正式部署入口

| 脚本 | 必须实现的 host-CDP 行为 |
|---|---|
| `scripts/deploy_local.sh` | 在 DGX Spark 宿主机下载或校验固定版本的 SparkClaw Chromium，为部署用户安装并启动 browserd，写入 Host-CDP endpoint-file 配置，并在启动产品容器前等待受保护 endpoint |
| `scripts/deploy_remote.sh` | 在受支持 Ubuntu 宿主机下载或校验固定版本的 SparkClaw Chromium，为 owner 安装并启动 browserd，无桌面时保留宿主机 headless 运行，写入 Host-CDP endpoint-file 配置，并在启动产品容器前等待受保护 endpoint |

两个脚本的 `--check` 路径保持只读。它们校验宿主机 executable、browserd 安装、Profile
权限、service definition、endpoint-file 配置、Docker bridge/proxy 连通前提和旧
container-browser setting 已不存在，但不安装 package 或启动 process。

这些入口调用的 start/reconcile script 也必须迁移 browser smoke test：

- `scripts/start_local_compose.sh` 与 `scripts/start_remote_compose.sh` 在 Gateway startup 前
  校验 browserd，并在 Gateway ready 后通过 `AGENT_BROWSER_CDP` 执行现有
  agent-browser smoke；
- 两条启动路径都不在 Gateway 内执行 Chromium，也不叠加 Gateway X11 overlay；它们先
  校验 headed 或 headless browserd，再从 Gateway 容器执行同一 CDP smoke；
- 部署成功必须同时满足 browserd health 和 agent-browser 对宿主机 Chromium 的
  open/snapshot round trip。

### Gateway 镜像与 Compose

`docker/images/gateway.Dockerfile` 删除 `chromium` 与 `xvfb` package、container
Chromium executable 环境变量和 container-owned browser Profile directory。Font 或
media tool 只有在其他 Gateway capability 仍需要时才保留。

产品 Compose 不再把 browser Profile 或 X11/Xauthority 挂载进 Gateway，而是提供：

- 只读 browserd endpoint capability file；
- 仅能访问专用 browser control bridge/proxy；
- 唯一 Host-CDP transport 的 endpoint-file 配置；
- 不暴露原始 Chromium remote-debugging port。

Cutover 删除所有包含 Chromium 的 Gateway image target 和 browser compatibility
overlay，同时删除 container Chromium launch 环境变量、browser Profile bind mount、
Profile lease 与 singleton cleanup wiring、Gateway X11/Xauthority overlay，以及所有可
选择旧 runtime 的部署分支。

### 部署验证

部署测试必须证明：

- Chromium 安装并运行在宿主机，进程属于部署 owner；
- Profile path 属于 SparkClaw，不是普通浏览器默认 Profile；
- Gateway 包含 `agent-browser`，但没有 Chromium executable 或 Xvfb package；
- Gateway shutdown 后 browserd 与 Chromium 保持运行；
- Browserd shutdown 使 Gateway browser health 变为 unavailable，且不启动 replacement
  container browser；
- direct-host、desktop Compose 与 headless VM 分别执行预期 browserd presentation；
- container-side agent-browser MCP 只能连接 capability-gated host CDP proxy，并完成
  open/snapshot/tab smoke；
- 仓库与展开后的 Compose 中不存在 container Chromium launcher、含 Chromium 的 Gateway
  target、browser Profile mount、Gateway X11 overlay、旧 mode fallback 或仅针对
  container browser 的测试。

## 失败语义

Transport 提供稳定 typed failure，包括：

- `browser_host_unavailable`；
- `browser_cdp_unauthorized`；
- `browser_cdp_version_unsupported`；
- `browser_profile_busy`；
- `browser_target_missing`；
- `browser_tab_not_owned`；
- `browser_handoff_required`；
- `browser_session_replaced`；
- `browser_connection_not_authenticated`。

Gateway restart、browserd restart、Chromium replacement 和 target loss 都会使旧 session
与 page generation 失效。Workflow resume 必须重新取得 SparkClaw-owned target 和新鲜
证据，不能复用重启前 ref。

## 推进计划

### 阶段 0：资格验证

- 验证固定 `agent-browser` 通过 `AGENT_BROWSER_CDP` 的 MCP 操作。
- 在一个现有 Chromium 进程上证明 snapshot、read、open、tab create/switch/close、
  type、select 和 screenshot 行为。
- 证明修改 adapter shutdown 后，停止 MCP 不会关闭 Chromium。
- 测量 navigation、SPA route、login、Gateway restart 和 browserd reconnect 时稳定
  target identity。
- 校验受支持宿主机 Chromium 版本和 Compose bridge 连通性。

### 阶段 1：浏览器基础

- 增加 Host-CDP 配置和 fail-closed 校验。临时的显式 qualification selector 只能存在
  到 cutover，不能保留在最终 schema 中。
- 实现 browserd、桌面 launcher、systemd user service、capability proxy、endpoint
  rotation 和 health reporting。
- 增加 Host-CDP adapter lifecycle；资格验证期间不改变已发布的 container runtime。
- 增加 stable target-ID tab ownership 和禁止触碰 owner tab 的测试。
- 增加共享 host-browser installer 与 Host-CDP deployment smoke path，但暂不将其选为
  正式 runtime。

### 阶段 2：标签页所有权与 Handoff

- 只为需要 restart recovery 的活动 browser handoff 持久化稳定 target identity 与
  ownership。
- 实现 task-tab 创建、显式 owner handoff、完成校验、取消、release 和 lease expiry。
- 证明 owner tab 不能通过 active-tab 或 numeric-index fallback 被选择。
- 验证 Gateway restart、Chromium restart 与 browserd reconnect 时的认证连续性，且
  不导出 credential。

### 阶段 3：切换与删除

- 增加 host service 安装、桌面 launcher、diagnostic 与运维文档。
- 在直接宿主机与 Compose host-CDP 环境中验证现有 browser Workflow Profile。
- 在现有 browser validation matrix 全绿且实际演练 operational recovery 前，保持
  host-CDP 为 opt-in。
- 随后原子切换两条正式部署脚本和全部启动路径到 Host-CDP，从 Gateway 删除 Chromium
  与 Xvfb，并删除旧 Profile 和 X11 mount。
- 删除 container-managed adapter lifecycle、launch logic、Profile lease、singleton
  cleanup、旧配置字段、fallback branch、compatibility image/Compose artifact 和仅针对
  container browser 的测试。
- 最终只支持 Host-CDP；残留旧配置必须返回已记录的 migration error。

## 验收标准

- Owner 保持 SparkClaw Browser 打开时，SparkClaw 可在同一 Chromium 进程的独立标签页
  工作。
- 登录通过专用持久 Profile 跨 Gateway restart 与 Chromium restart 保留。
- 不允许第二个 Chromium 进程取得该 Profile。
- Gateway shutdown 后宿主机浏览器与 owner 标签页保持打开。
- Browserd 或 CDP 丢失产生 typed failure，不启动 fallback browser。
- 未经显式 handoff，现有 owner 标签页不会被选择、修改或关闭。
- 普通 navigation 后标签页 identity 仍绑定稳定 target ID。
- CDP endpoint 与 capability 不进入公开 state、log、trace 或 artifact。
- Source tree、配置 schema、Gateway image、Compose 展开结果和部署脚本中不存在
  container-managed 浏览器 runtime 或 fallback。
- 旧 container-browser 测试被删除或改写为 Host-CDP 测试，只保留明确拒绝旧配置的
  migration coverage。
- `scripts/deploy_local.sh` 与 `scripts/deploy_remote.sh` 在 cutover 后安装或校验宿主机
  Chromium，并且只配置 Host-CDP endpoint。
- Gateway image 包含 agent-browser，但不包含 Chromium 或 Xvfb；部署 smoke
  证明 agent-browser 实际控制宿主机进程。
- Headless VM 运行一个宿主机拥有的 headless Chromium；desktop host 运行一个宿主机
  拥有的 headed Chromium，两者遵守相同专用 Profile 规则。
- 通用 owner 登录 handoff 只能以新鲜的 handoff 后证据恢复同一个冻结 task-owned
  Target。
- 现有 browser Workflow Profile 在 host-CDP 模式中保持 Policy、Approval、Evidence
  和 typed failure 行为。

## 被拒绝的替代方案

### Attach 到 Owner 日常浏览器 Profile

拒绝，因为这会把无关已登录网站、标签页、历史、extension 和 credential 暴露给
SparkClaw。现代 Chromium 也限制默认数据目录的 remote debugging。SparkClaw 使用
专用工作 Profile。

### 两个 Chromium 进程共享一个 Profile

拒绝，因为 Chromium Profile singleton lock 禁止并发 owner，并发写入可能损坏状态。
唯一 browserd-owned 进程是不变量。

### 导出或同步 Cookie

拒绝，因为登录态可能包含 device-bound token、Passkey、证书、extension state 和
provider-specific storage。只复制一部分既不可靠，也会扩大 credential boundary。

### 用直接 CDP 代码替换 agent-browser

拒绝，因为这会创建第二套 execution 与 perception backend。新模式只改变 process
transport 与 ownership；`browserautomation.Adapter` 后仍由 agent-browser 实现。

### 根据 Inactivity 推断标签页交接

拒绝，因为 inactivity 不能证明 consent 或 abandonment。必须显式绑定 Target 进行
handoff。

### 永久支持双 Runtime

拒绝，因为同时维护 container-managed 与 Host-CDP 会重复 process ownership、配置、
打包、部署和测试契约。旧 runtime 只作为临时迁移辅助，Host-CDP 通过资格验证后即删除。

## 与现有文档的关系

- [浏览器 Runtime](browser-runtime.md)在本方案落地前继续作为已交付浏览器行为的权威。
- [架构](architecture.md)继续作为跨组件 ownership 与受支持产品表面的权威。
- [Workflow 能力矩阵](workflow-capabilities.md)继续作为向用户暴露的可执行 browser
  行为权威。
- 宿主机服务实施后，由[部署](deployment.md)负责安装与运行命令。
