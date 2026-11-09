# 客户端内嵌 Browser Controller 经 ISCP 设计

> 语言：简体中文 | [English](../../docs/desktop-embedded-browser-controller-iscp-design.md)
>
> 日期：2026-11-09。状态：设计提案，未实施；所有者决定 D1–D3 已于 2026-11-09 记录（§12）。
> 源码基线：`main` 的 `b5c21e14`；其浏览器、ISCP 和桌面源码与 `cddda7d1` 相同。
> 范围：经 ISCP 连接的 SparkX 客户端的通用浏览器自动化。邮件、provider 脚本和 AI 对话导出
> 明确不在范围内，保持现有路径。

## 1. 决策

保留现有控制链路，只改变其中一跳的运行位置和承载方式：

```text
Gateway 浏览器工具
  -> browserhost ScopedAdapter（工具语义不变）
  -> PlaywrightExtensionAdapter（不变）
  -> browsercontrol ControllerClient        <-- 新增 ISCP 实现
  -> ISCP 反向 browser.controller.*         <-- 新传输，复用同一已认证设备会话
  -> SparkX 主进程转发器（仅固定路由）
  -> 随包 Browser Controller（同一份源码，仅通用 MCP 通道）
  -> Playwright MCP
  -> Electron Browser Adapter（已有，改为生产环境启动）
  -> 任务 WebContentsView 的 debugger
```

Controller、Playwright MCP 和 Electron adapter 的代码与契约不变。ISCP 只替换 Gateway 与
Controller 之间仅限本机的 Unix socket。切点选在 Gateway–Controller 这一跳，因为每次工具调用只需
一次往返。改为隧道传输 Playwright–Electron 的 CDP 一跳的方案已否决：CDP 每个动作需要多次串行往返，
每次页面加载产生数百条事件，并且会让远端主机在与个人登录 Session 共享的任务页上执行任意
`Runtime.evaluate`。

## 2. `main` 上的现状

| 角色 | 当前路径 |
|---|---|
| 后端采集（work2） | `AcquisitionAdapter` → `BackendHost` → `PlaywrightExtensionAdapter` → `browsercontrol.Service` → 经 Controller Unix socket 的 `HTTPControllerClient` → work2 Controller → 后端浏览器 |
| 经 ISCP 的客户端内嵌 | `Broker.ForScope` → `ScopedAdapter` → `Broker.Dispatch` → 反向 `browser.command` → `host-agent.mjs` → `native-page-commands.mjs` |

客户端路径上没有 Playwright。`native-page-commands.mjs` 用 `executeJavaScript` 实现八个操作
（navigate、read、snapshot、click、fill、select、screenshot、wait）：最多 120 个控件，点击用
`HTMLElement.click` 而非输入事件，不处理 frame 和弹窗，截图超过 39 KiB 即失败。同一个模型工具在
后端和客户端行为不同，与 [客户端／后端 R3 设计](client-backend-architecture-design.md) 的
等价语义要求（A08）相悖。

本设计依赖的事实：

- `browsercontrol.ControllerClient` 已抽象 Controller（`Acquire`、`Execute`、`Release`、
  `RunScript`、`OpenProviderLogin`、`ValidateToken`）；`PlaywrightExtensionAdapter` 通过
  `Status` 和 `AcquireSession` 使用它。
- `browserhost.BrowserHostAdapter` 是 `Broker`（客户端内嵌）和 `BackendHost`（后端采集）共用的
  角色边界。
- Controller 通用通道接受 `page.click/fill/info/navigate/read/screenshot/select/snapshot/type/wait`
  和 `tabs.close/handoff/list/new`，以及导出操作 `ai_chat.export`、`ai_platform.check`。同一时间只有
  一个活动会话。
- `ElectronAdapterServer`（`apps/desktop/src/browser/adapter-server.mjs`）只在 Linux 验收模式启动，
  macOS 生产环境从不启动。因此 Playwright 经 adapter 从未在 macOS 上运行过。
- 每个 ISCP workbench 操作的单条消息上限为 63,488 字节，ISCP 调用 30 秒超时，桌面浏览器命令截止
  时间为 25 秒。经 TEST1 Relay 的生产读取实测每次 219–537 ms
  （[公网 ISCP 执行记录](public-iscp-node-deployment.md)）。
- Electron 44.4.3 内置 Node 24.21.0；Controller 包声明 `node >=26 <27`。

## 3. 范围

范围内：

- 在 SparkX（macOS 和 Linux）中打包 Browser Controller 通用通道和 Playwright MCP。
- 该通道的 ISCP 操作、Gateway 客户端和桌面转发器。
- 当前由 `ScopedAdapter` 提供的模型浏览器工具：`browser.status`、`open`、`navigate`、`read`、
  `snapshot`、`click`、`type`、`select`、`screenshot`、`wait`、`list_tabs`、`focus`、`close`。
- 所有 SparkX 客户端角色，包括与 work2 同机的 Linux 客户端（D3）。
- 移除旧的客户端路径：反向 `browser.command`、`native-page-commands.mjs` 和 LAN 直连浏览器 host（D2）。

范围外（保持不变，客户端不打包或不开放）：

- `RunScript`、provider 脚本和 App-CLI runtime；QQ／Gmail／Outlook 探测与发送。
- 邮件观察器（`/v1/mail-observers/*`）及 work2 上的常驻邮件采集。
- `OpenProviderLogin`、`ai_chat.export`、`ai_platform.check`。
- `tabs.handoff`（Electron 任务页已拒绝）。
- 后端采集角色、work2 Controller 服务及其 Unix socket。按 D3，它们只服务后端采集，不再承载任何客户端任务。

## 4. Gateway 改动

### 4.1 `ISCPControllerClient`

新的 `browsercontrol.ControllerClient` 实现，绑定到一个 ISCP 会话：

| 方法 | ISCP 反向操作 | 说明 |
|---|---|---|
| `Acquire` | `browser.controller.acquire` | 不发送 token，由桌面注入本地 token |
| `Execute` | `browser.controller.execute` | 仅 §3 列出的通用操作 |
| `Release` | `browser.controller.release` | |
| status | `browser.controller.status` | 只报告就绪状态和固定版本 |
| `RunScript`、`OpenProviderLogin`、`ValidateToken` | 无 | 不发起调用，直接返回类型化的 `browser_operation_unavailable` |

`ReverseSession` 增加按操作名调用的方法（目前只能发送 `browser.command`）。超过一条消息的响应通过
`browser.controller.chunk`（§6.2）读取，并在 `HTTPControllerClient` 现有的 10 MiB 响应上限内重组。

### 4.2 会话绑定的 controller service

`browsercontrol.Service` 依赖 Gateway Vault 中的 Bridge token。客户端路由改用会话绑定的
`PlaywrightController`（`Status`、`AcquireSession`）实现，不使用 Vault：凭据代次取 ISCP
`authorization_revision`，controller 代次取桌面上报的值。每个 ISCP 会话一个实例，随会话关闭。

### 4.3 `ClientControllerHost`

角色为 `client_embedded` 的 `BrowserHostAdapter`，包装 `PlaywrightExtensionAdapter(cfg, 会话绑定 service)`，
方式与 `BackendHost` 包装后端 adapter 相同。绑定与现在一样由 ISCP 会话派生（`connection_epoch` =
会话 ID，`runtime_generation` = `iscp_<会话 ID>`，`host_id` 和 `authorization_digest` 取哈希），因此
`ScopedAdapter`、冻结目标路由规则和 fence 处理不变。对每个 ISCP 客户端会话，`Broker.ForScope` 都返回
`ScopedAdapter(ClientControllerHost)`。没有运行时选择器，也没有回退：成对发布，controller 失败如实报告；
未声明 `browser.controller.v1` 的客户端，浏览器能力视为不可用，原入口禁用。

## 5. 桌面改动

### 5.1 打包

- 构建脚本生成 `apps/desktop/bin/browser-controller/`：Controller 源码子集；以
  `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` 安装的 `playwright`、`playwright-core`、`@playwright/mcp`
  生产依赖；构建时应用并按哈希校验现有 `install-playwright-downloads.mjs` 补丁；文件哈希清单。
- `electron-builder` 将其复制到 `extraResources/browser-controller`（与 `iscp-workbench` 一样在
  asar 之外）。不下载任何浏览器，Electron 自带的 Chromium 是唯一引擎。
- 不打包 `@playwright/cli`、App-CLI runtime、provider 脚本和 userscripts。
- macOS 包（`dist:mac-arm64`、`dist:mac-x64`）和 Linux ARM64 包（`package:desktop`）都包含该目录；
  work2 同机桌面安装同一个 Linux 包（D3）。
- `audit-package.mjs` 将该目录加入白名单；release manifest 记录 Controller、MCP 和 Playwright
  版本及哈希。预计约 20 MB。

### 5.2 客户端 Controller 入口

新增 `tools/browser-controller/src/client-main.mjs`，只装配 MCP 工厂和 Electron adapter 配置，不引入
App-CLI、脚本工厂和邮件观察器，HTTP 服务只开放 health、acquire、execute、release。
`controller.mjs`、`mcp-client.mjs`、`electron-adapter-client.mjs` 原样复用。

### 5.3 进程与 adapter 生命周期

- 主进程在第一次 `browser.controller.acquire` 时启动 Controller，采用现有的 `ELECTRON_RUN_AS_NODE`
  子进程方式（与本地预览相同）。Controller 通过 `process.execPath` 并带同一变量启动 Playwright MCP。
- 私有运行目录使用短路径（`/tmp/sparkx-<uid>/bc-<random>`，权限 0700），使 socket 路径低于 macOS
  的 104 字节上限；socket 权限 0600。
- `ElectronAdapterServer` 在两个平台的生产环境启动，socket 和 secret 位于该目录。其 `qualification*`
  操作在验收模式之外保持禁用。
- Controller token 和 adapter secret 每次启动重新生成，由主进程持有，不发送给 Gateway、渲染进程或
  远端页面。
- Controller 或 MCP 退出时，主进程提升代次、关闭其任务页，并把在途写操作报告为 unknown。只在下一次
  acquire 时重启。
- ISCP 会话丢失、设备撤销或授权变更时，停止受理、释放租约并取消在途调用，与现在的 `browser.command`
  相同。

### 5.4 反向转发器

`iscp-transport.mjs` 在反向白名单中用 `browser.controller.*` 操作取代 `browser.command`；其他操作仍以
403 拒绝且不结束会话。转发器：

- 按精确 schema 校验每个请求体，并映射到一个固定的 Controller 路由（不接受路径、URL、脚本或 CDP
  字段）；
- 在到达 Controller 之前拒绝导出操作和 `tabs.handoff`；
- 在 acquire 时注入本地 token；
- 每个写类 execute 先按命令 ID 和参数摘要记入现有 `HostJournal` 再转发，使 `browser.receipt`、
  `browser.reconcile` 无需改动（删除旧路径时，journal 从 `host-agent.mjs` 移到转发器）；
- 执行 25 秒的桌面截止时间（§6.1）；
- 将获取的任务页绑定到反向请求体中的会话和任务，使其像现在的 `browser.command` 页面一样出现在原有
  浏览器面板中。

## 6. 限制、超时与结果

### 6.1 截止时间

| 项 | 值 |
|---|---|
| ISCP 调用超时 | 30 s（不变） |
| 桌面转发器截止 | 25 s，确保桌面自身结果先到 |
| 客户端 Controller 导航超时 | 20 s（`SPARKCLAW_BROWSER_NAVIGATION_TIMEOUT_MS`） |
| 客户端 Controller 动作超时 | 10 s（现有默认值） |
| `page.wait` | 每次最多 15 s |

读操作超时或返回 `outcome_unknown` 时报错，模型可以重试。写操作（`page.click/fill/navigate/select/type`、
`tabs.new/close`）进入以命令 ID 和摘要为键的 unknown fence，绝不重发。只有执行前的拒绝（无会话，或桌面
自身的 403/503）才能证明写操作未执行。

### 6.2 大结果

不超过消息预算的结果直接内联返回。更大的结果由桌面暂存在有界缓冲区（总计 8 MiB，存活 60 秒，读完或
会话结束即释放），回复中携带 `result_id`、大小和 SHA-256。Gateway 用 `browser.controller.chunk`
（只读，按偏移和长度，最多四块并行）读取并校验哈希。这同时解除了现在 39 KiB 的截图上限。分块吞吐在
P0 实测；若过慢，截图改为固定质量的 JPEG，而不是新增通道。

### 6.3 并发

Controller 同一时间只有一个活动会话。同一客户端上的并发浏览器任务在 acquire 处排队，最长等待
Controller 的等待超时；而现在的 `Broker` 允许每个 host 有多个任务页。所有者已接受排队（D1）。等待超时的
任务以 Controller 现有的 busy 错误失败，不会进入第二个会话。

## 7. ISCP 操作注册

在 `services/gateway/internal/iscpworkbench/operations.json` 中新增以下条目，由
`scripts/sync-iscp-operations.mjs` 投影到桌面：

| 操作 | 方向 | scope | 写操作 | 恢复方式 |
|---|---|---|---|---|
| `browser.controller.status` | reverse | `browser.control` | 否 | read |
| `browser.controller.acquire` | reverse | `browser.control` | 是 | operation_receipt |
| `browser.controller.execute` | reverse | `browser.control` | 按操作 | operation_receipt |
| `browser.controller.release` | reverse | `browser.control` | 是 | operation_receipt |
| `browser.controller.chunk` | reverse | `browser.control` | 否 | read |

全部使用 `limit_bytes` 63,488 和容量类别 `business`。复用 `browser.receipt` 和 `browser.reconcile`。
`browser.command` 条目在同一版本中删除（D2）。
开关仍是 Gateway 的 `browser` 能力族和桌面的 `capabilities.browser`。

## 8. 安全

- 授权即 ISCP 设备会话，与 `browser.command` 相同。不新增 grant、心跳或轮询。
- 远端输入只能经 Controller 校验过的通用操作到达 Playwright；模型仍然拿不到 selector、JavaScript、
  CDP 或 Playwright 代码类工具。
- 任务页隔离仍由 Electron adapter 负责：任务级 debugger，不可访问个人页或 workbench 目标，阻止全局
  Cookie 访问，拒绝过期代次。
- Controller 和 adapter 只监听私有 socket；渲染进程和远端页面无法访问。
- 打包文件按哈希固定并经审计；Controller 不能加载其目录之外的代码。

## 9. 界面

不改任何页面、菜单、布局、样式和交互。浏览器面板、任务标签、只读任务视图、显式选择，以及带核对操作的
未知写入提示都保持原样。随包 Controller 不可用时，禁用原有浏览器入口。失败和恢复提示只在需要用户处理时
显示。不增加常驻技术状态。

## 10. 阶段

| 阶段 | 工作 | 完成证据 |
|---|---|---|
| P0 可行性 | 在 Electron Node 24.21 下运行 Controller（按 `>=26` 审计 API）；在 macOS 启动 adapter；在 fixture 页上跑通 MCP 读／填／点／snapshot／截图；确认 `BrowserController` 可在没有脚本工厂时构造；在 Mac 与 work2 之间实测单次 ISCP 反向往返和分块吞吐 | 已安装的 Mac 包上 fixture 通过；记录延迟数据 |
| P1 打包与本地进程 | 构建脚本、`client-main.mjs`、进程监管、生产环境启动 adapter、审计与 manifest | 已安装 DMG 在无网络时本地通过 Controller fixture |
| P2 ISCP 传输 | 注册条目、`ISCPControllerClient`、会话绑定 service、`ClientControllerHost`、转发器、journal、分块 | 本地 Docker Relay 隔离测试；注入丢失后只有一次效果和一个 unknown fence；直连 HTTP/WS 为零 |
| P3 真机验收 | 已安装的 Mac 和 Linux 客户端经 TEST1 连接真实 work2 | 满足 [ISCP 能力扩展设计](desktop-iscp-capability-expansion-design.md) 的 G1–G4；固定场景延迟与 `browser.command` 对比；原界面交互逐项验证 |
| P4 切换 | Gateway 与客户端成对发布（Mac 客户端与 work2 Linux 桌面同时）；按 D2 删除旧路径 | 源码扫描无残留引用；记录整版回滚步骤 |

每次推送、部署和安装都需要所有者单独授权。

## 11. 验收

- **业务：** 在真实页面上经新路径完成打开、snapshot、填写、点击、选择和截图，并核实页面状态；现有路径
  失败而 Playwright 能处理的 frame 和弹窗场景通过。
- **恢复：** 写响应丢失、Controller 崩溃、MCP 崩溃、ISCP 重连、Gateway 重启、设备撤销，每种情况都没有
  重复效果，fence 正确。
- **传输：** 两个进程都没有直连业务 HTTP/WS；封锁 Relay 时调用失败且不回退。
- **隔离：** 不能访问个人页、workbench 或其他任务的目标；导出、脚本和 handoff 操作在转发器处被拒绝。
- **界面：** 原有面板、标签、选择、隐藏、加宽和核对操作行为与之前一致。

## 12. 决定

2026-11-09 由所有者确定。

| 编号 | 决定 | 影响 |
|---|---|---|
| D1 并发 | 接受每个客户端同时只有一个活动浏览器任务，其余在 acquire 处排队 | Controller 不改；排队超时的任务以现有 busy 错误失败 |
| D2 旧路径 | P3 之后删除 | 一并删除：反向 `browser.command`（注册条目、`Broker` 反向分发、桌面反向处理）、`native-page-commands.mjs`，以及 LAN 直连浏览器 host（`/api/v1/browser/hosts` 的 grant、connect、revoke、fences 路由，`Broker.ServeHost`／`IssueGrant`，桌面 `host-socket.mjs`）。仅 LAN 连接时浏览器自动化不可用，入口禁用。保留 `browser.receipt`、`browser.reconcile` 和 `HostJournal` |
| D3 Linux 客户端 | work2 同机桌面也经 ISCP 使用随包 Controller | work2 的 systemd Controller 只服务后端采集；Linux 包携带 Controller，在 P3 验收 |
