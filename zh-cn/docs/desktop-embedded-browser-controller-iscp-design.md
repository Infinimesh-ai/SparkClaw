# 客户端内嵌 Browser Controller 经 ISCP 设计

> 语言：简体中文 | [English](../../docs/desktop-embedded-browser-controller-iscp-design.md)
>
> 日期：2026-11-09。状态：实施推进中。§14 记录源码接入、Mac 生命周期和隔离加密 Relay 证据；
> 安装包／远程验收和最终切换仍未完成。§12 保留所有者决定 D1–D3。
> 已核对源码：`main` 的 `e031a453`（初版提案）；浏览器／ISCP／桌面实现基线仍为 `b5c21e14`。
> 本次仅涉及 SparkClaw，不执行项目簇协调流程。
> 范围：SparkX 经 ISCP 的通用浏览器自动化。邮件、provider 脚本和 AI 对话导出保持现有路径。

## 1. 决策

将通用 Browser Controller 和 Playwright MCP 放入每个 SparkX 安装包，使用现有已认证的
ISCP 设备会话承载 Gateway–Controller 边界：

```mermaid
flowchart LR
  G[Gateway 浏览器工具] --> S[ScopedAdapter：可信任务作用域]
  S --> H[ClientControllerHost + PlaywrightExtensionAdapter]
  H --> C[ISCPControllerClient]
  C <-->|browser.controller.*| F[SparkX 主进程：转发器与 journal]
  F -->|私有 Unix socket| B[随包 Controller]
  B --> M[本地 Playwright MCP]
  M --> A[本地 Electron adapter]
  A --> V[任务 WebContentsView debugger]
```

复用 Controller 执行契约、Playwright 操作和 Electron 隔离核心；作用域／呈现边界、进程装配和
生命周期需要明确适配。“复用”不代表相关文件原样不动。后端采集 Controller 及其 Unix socket
保持独立。

每次 **Controller 请求**跨 ISCP 一次，分块读取另计。一个公开浏览器工具可能发起多次 Controller
请求，现有打开、读取、snapshot 和稳定状态等待已经如此。验收测完整工具延迟，不能假设每次模型工具
只需一次往返。CDP 保持本地：跨网传输会增加大量串行往返，也会暴露更宽的远端执行接口。
不新增远端 JavaScript、CDP、selector 或 Playwright 代码 API。

## 2. 源码基线与缺口

| 边界 | 当前源码事实 | 必需改动 |
|---|---|---|
| 后端采集 | `AcquisitionAdapter` → `BackendHost` → `PlaywrightExtensionAdapter` → `browsercontrol.Service` → Unix socket 上的 `HTTPControllerClient` | 保留角色和路径 |
| 客户端内嵌 | `Broker.ForScope` → `ScopedAdapter` → 反向 `browser.command` → `BrowserHostAgent` → `native-page-commands.mjs` | 更换执行器和传输 |
| 原生执行器 | 八个页面操作；最多 120 个控件；DOM click；缺少等价 frame／弹窗支持；截图上限 39 KiB | 验证共享 MCP 语义，不能假设所有点击行为都会自动改善 |
| Scoped adapter | 裁剪参数、合成单页列表、本地处理 focus，并保留冻结目标的 settle 规则 | 保留公开契约，转发完整且已验证的工具参数（§4.3） |
| Controller | 通用通道只有一个活动会话；允许 `scriptFactory=null`，但 `controller.mjs` 仍导入并构造 `MailObserverFeed` | 分离可选装配，保持后端邮件行为 |
| MCP 模块 | `mcp-client.mjs` 静态导入 AI 登录／导出模块 | 提取／注入可选处理器，使客户端包不依赖这些模块 |
| HTTP 装配 | `http-server.mjs` 静态导入 App-CLI 协议并开放 application／mail 路由 | 共享有界 Unix server 基础能力，仅装配四个客户端路由，不带该导入 |
| Electron adapter | `main.mjs` 仅在 Linux 验收模式启动；任务连接没有工作台会话归属 | 两个平台生产启动，增加可信本地绑定映射 |
| 页面释放 | Host 路径保留会话视图；MCP 清理会关闭持有的标签页 | 正常释放时保留原视图（§5.4） |
| 恢复 | `HostJournal` 和 Gateway fence 只持久化 ID／摘要／状态，不保存结果；`browser.receipt` 读取 Gateway fence | 复用存储并扩展准入／状态处理，不能假设已有结果重放 |
| 传输／安装包 | 操作消息预算 63,488 字节；RPC 30 s；桌面截止 25 s；Electron 固定 44.4.3；Controller 包声明 Node `>=26 <27` | 验证随包 Electron runtime（提案基线 Node 24.21）、有界传输及冷启动 |

角色分工以[客户端／后端 R3 设计](client-backend-architecture-design.md)为准。
[桌面浏览器设计](desktop-client-embedded-browser-design.md)保留历史设计依据；新客户端控制一跳以本文为准。
§13 列出源码位置。

## 3. 范围

包含：macOS ARM64／x64 和 Linux ARM64 打包、生产 Electron adapter 启动、ISCP 转发器／客户端／
service，以及现有 `browser.status`、`open`、`navigate`、`read`、`snapshot`、`click`、`type`、
`select`、`screenshot`、`wait`、`list_tabs`、`focus`、`close` 工具、客户端任务页呈现与恢复。
work2 同机桌面也属于客户端（D3）。

不包含：`RunScript`、App-CLI、provider 脚本、邮件观察器、QQ／Gmail／Outlook 操作、
`OpenProviderLogin`、`ai_chat.export`、`ai_platform.check`、远端 `tabs.handoff`。
现有个人页脚本／导出功能和后端邮件采集保持原路径。不纳入该包不等于删除已有产品入口。

P3 后删除反向 `browser.command`、原生执行器和 LAN 直连浏览器 host（D2）。仅 LAN 连接时
浏览器自动化不可用；其他 LAN 能力和个人浏览保持原有授权行为。不回退到 work2 的采集浏览器。

## 4. Gateway 集成

### 4.1 `ISCPControllerClient`

为一个确定的 ISCP 会话实现 `browsercontrol.ControllerClient`。`Acquire`、`Execute`、`Release`
映射到同名反向操作，session service 通过 `browser.controller.status` 获取就绪信息。
`Status` 当前不是 `ControllerClient` 方法：探测放在新 service／transport，不给后端接口增加无用方法。
`RunScript`、`OpenProviderLogin`、`ValidateToken` 在本地返回类型化 `browser_operation_unavailable`。
Vault token 不跨网。

扩展 `browserhost.ReverseSession`，使其接受操作名；下层 `iscpworkbench.PeerSession.Command`
已经支持。每次调用固定到原 ISCP 会话，重连不能将进行中的命令改投新会话。
校验外层信封与分块后，按原 Controller 结果 schema 解码，执行响应仍不超过已有 10 MiB 上限。

### 4.2 Session service 与代次

每个 ISCP 会话的 manager 持有传输、就绪状态、租约和串行化控制；每个任务单独持有 Playwright
adapter／snapshot 状态。实现不依赖 Vault `Service` 的 `PlaywrightController.Status/AcquireSession`
及 `browsercontrol.Session`。不能让多个排队任务共享一个可变 Playwright adapter，该 adapter
当前会拒绝与活动任务不同的 scope。

凭据代次取正数且 JavaScript 可安全表示的 `authorization_revision`，超出范围则协商失败。
Controller／session／page 代次来自真实 Controller 返回值，校验后才能更新缓存。
它们与外层 host 的 `runtime_generation=iscp_<会话 ID>`、桌面进程／adapter 启动代次分别管理。
即便 ISCP 仍连接，Controller 重启也必须使旧租约失效。

保留现有已认证 host ID 和授权摘要派生。外层 `Binding` 是不可变归属句柄；Controller 页面代次和
真实 page ID 存在其私有租约映射中。不能用 host 代次覆盖 Controller page generation，重新 acquire
后不能恢复旧 snapshot。Controller TTL 为 10 分钟，与现有 adapter 一致，不延长设备授权。
到期使租约和 ref 失效；后续调用只有在清理完成、检查未知写入后才能重新 acquire，不能重放旧操作。

### 4.3 作用域与工具语义

`Broker.ForScope` 为已认证 installation 解析 `ScopedAdapter(ClientControllerHost)`。
Scoped 边界继续从任务派生身份、拒绝外部页面并检查 fence。扩展内部强类型转发路径，参考后端已有的
`incumbentCall`，让客户端 Playwright adapter 收到原始且已验证的参数。
不能把当前有损的 `ref/value` 原生命令投影直接送入 Playwright，也不能信任模型填写的 scope。

| 公开行为 | 客户端实现要求 |
|---|---|
| status | 本地协商就绪状态加无副作用 status 探测；读取状态不能创建页面、子进程或 acquire |
| open／navigate | 共享 Playwright URL／标签页规则；只映射租约持有页面，包括已接纳任务弹窗 |
| read／snapshot | 保留读取窗口、snapshot 身份、ref 校验、认证证据和代次检查 |
| click／type／select | 保留共享参数语义，包括 fill 与 type／submit 的差异；每个写类 Controller 子调用单独记 journal |
| screenshot | 保留图片 MIME 与内容，使用有界传输，解除原生 39 KiB 上限 |
| wait | 保留冻结目标和有界同源重绑定规则；共享 settle 逻辑，完整保留强类型等待字段 |
| list_tabs | 没有任务租约时不能隐式 acquire；有租约后仅投影其任务页，排除连接／引导页 |
| focus | 保留当前客户端逻辑 focus 和用户显式呈现，不能走共享 adapter 的 `tabs.handoff` 或更改选中会话 |
| close | 保留当前客户端 close／release 行为，不引入后端 adapter 的替补空白页处理 |

公共 host 接口继续显式区分角色。旧传输删除后，就绪、释放、租约有效性检查和 fence 服务仍须可用。
“界面／工具语义不变”是验收要求，不代表现有 `ScopedAdapter` 已经具备这些适配能力。

## 5. 桌面打包、进程与页面归属

### 5.1 可复现安装包

用版本化的客户端专用 manifest／lockfile 和显式传递源码列表构建
`apps/desktop/bin/browser-controller/`。固定当前 MCP `0.0.80`、Playwright／playwright-core
`1.63.0-alpha-2026-08-31`，构建时运行现有下载／adapter 补丁并核对补丁后文件哈希。
安装时设 `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`；排除下载的浏览器、`@playwright/cli`、App-CLI、
provider 脚本和 userscripts。不能在后端 manifest 上运行 `npm ci` 后临时删依赖来凑客户端包。

复制到 asar 外的 `resources/browser-controller`，同时从通用和 macOS asar 文件列表排除
`bin/browser-controller`，避免双份打包。接入 Mac 打包脚本和 Linux `package:desktop`，审计展开目录。
release manifest 记录源码版本、Electron／Node／MCP／Playwright 版本、补丁哈希、文件哈希和实测体积。
初版的 20 MB 仅为估计；manifest 校验失败则该能力不可用。

### 5.2 装配与 runtime 验证

新增 `tools/browser-controller/src/client-main.mjs`：固定 `client_embedded` profile、MCP factory、
私有 Electron adapter，HTTP 只开放 health／acquire／execute／release。
从核心装配提取可选邮件观察器和 AI 处理器；后端 `main.mjs` 注入原有处理器，客户端入口不导入。
共享通用执行与后端契约继续有测试覆盖，不维护第二份分叉 Controller 操作实现。
现有 HTTP server 也不能原样复制：App-CLI decoder 导入应留在后端路由装配中。
复用有界请求解析／socket 助手，仅安装四个固定客户端路由。

Controller 和 MCP 均以 Electron 的 `process.execPath` 加 `ELECTRON_RUN_AS_NODE=1` 启动。
环境变量采用最小白名单，固定入口／cwd／输出目录，不继承 `NODE_OPTIONS`、loader 或浏览器／profile
覆盖值。构建机 Node 26 通过不代表 Electron 内置 Node 可用。
P0 验证真实 Mac／Linux 安装包，记录 `process.versions`，核对所用 API，确认应用签名／fuse 配置允许
这种启动方式。若不兼容，先停止该阶段并记录修订后的打包决定，再继续实施；不能默默依赖系统 Node
或运行时下载 Node。

### 5.3 进程监管

状态流：`stopped → starting → ready/busy → stopping → stopped`；启动或完整性失败报告 unavailable。
文件已验证且尚未启动的惰性进程可以接受 acquire；status 不代表一次真实业务验证成功。
首次 acquire 合并为一次启动。在已验证属主的私有 `/tmp/sparkx-<uid>` 下创建随机短目录（0700），
拒绝符号链接／错误属主，socket 从创建起即为 0600。按编码后的字节长度检查 macOS socket 路径上限。
每次启动更新 token 和 adapter secret，只存于 main／私有子进程配置，不能进入 renderer、远端页面、
Gateway 或日志。

两个平台均生产启动 adapter，生产环境仍拒绝 qualification 方法。
任务／会话终止顺序：停止准入，取消排队，撤销注册和 debugger 权限，释放活动 Controller session，
有界等待 MCP／Controller 退出，终止剩余的自有子进程，再清理 runtime 文件。socket 关闭不证明清理完成。
清理未经证实时继续阻止准入。Controller／MCP 崩溃使该启动代次及在途写入失效，仅后续 acquire 才重启。
ISCP 丢失、授权变更、锁定或撤销使对应租约失效，正常授权恢复不能复活旧租约。
不删除个人页／profile 数据。

### 5.4 会话归属与视图保留

转发 acquire 前，main 建立一次性待绑定映射，从本地 Controller task token 指向
`(owner, client, installation, local conversation, local task, host lease, ISCP session,
supervisor incarnation)`。token 是符合 Controller 128 字符限制的不透明 ID，不截断用户 ID。
adapter 的 `registerConnection/openConnection` 必须消费该映射，绑定返回的 Controller session
及本地 connection ID。映射缺失、重复或过期均拒绝。
远端页面和模型参数均不能注册会话归属；超时／取消撤销待绑定项。

`PageRegistry` 增加上述 main 持有的关联。任务弹窗／frame 继承同一连接白名单；真实页面映射到
不透明的公开任务 page ID，引导页隐藏。通过现有 `conversationPages`、页面投影和呈现 hooks 展示。
后台创建任务页不能选中其会话或把窗口切到前台。

正常任务 release 像现有 `releaseHostPage` 一样保留当前会话视图，同时移除全部远端权限：撤销凭据、
解除 debugger／facade、丢弃 lease／ref 映射。实现明确的本地 begin-release／retain 和
acquire／adopt hooks，防止 MCP 原有 `closeTaskPage` 意外销毁保留视图；其余任务标签和连接页仍须清理。
之后的新租约只有为同一会话建立新绑定后才能接管保留视图，并获取全新 snapshot ref。
显式 close 仍关闭指定任务视图。崩溃、撤销和会话丢失保留原有受保护页面的处理行为，不能把任务页
变成可控制的个人页。原生测试分别验证正常完成、显式关闭和异常清理。

### 5.5 转发器职责

反向白名单改为精确注册操作名，不接受通配前缀。转发器校验 schema、scope、摘要、截止时间和当前授权，
写 journal，再映射到固定本地路由。拒绝调用者指定传输路径／host、token、脚本、CDP 和未知字段。
已有 URL 策略下的导航地址仍允许出现在 `page.navigate`／`tabs.new` 强类型参数中；“禁止任意 URL”
指传输目标，不能误禁页面导航。

按租约串行处理改变 Controller 状态的调用；chunk／status 不占该执行槽。
删除 `host-agent.mjs` 前先迁出 journal、fence 刷新和 reconcile 的职责，保留现有 IPC／UI 投影。
release／核对不受 acquire 队列容量阻塞，读取已有 fence 不要求 Controller 正常。

## 6. 传输契约、预算与恢复

### 6.1 请求与响应 schema

下列新增字段是设计契约，尚未上线。各请求体使用 `schema_version: 1`。
任务调用携带 `command_id`、`digest`、`binding`（完整现有 `browserhost.Binding`）、`timeout_ms`
及下表的操作字段。`timeout_ms` 为不超过 25,000 的正数预算，调用方截止时间可以进一步缩短。
`status` 使用已认证 installation／session，不要求任务绑定。`chunk` 使用该 session 和原 binding、
command／result ID，不能触发重新执行。

| 反向操作 | 附加输入 | 成功响应负载 |
|---|---|---|
| status | 无 | 协议 `browser.controller.v1`、available／进程状态、监管启动代次、已启动时的 Controller generation、固定版本及限制 |
| acquire | `wait_timeout_ms`（0–30,000，再受剩余预算约束）、`session_ttl_ms`（1–600,000） | Controller `SessionLease`；profile／lane／token／task token／凭据代次均由转发器固定 |
| execute | `lease`（Controller lease）、`operation`、`arguments` | Controller `ExecutionResult`，内联或结果描述符 |
| release | `lease` | Controller `ReleaseResult`；释放身份取自已验证租约映射 |
| chunk | `command_id`、`result_id`、`offset`、`length` | 匹配的 ID、偏移、总字节数、base64 字节，无可变游标 |

任务响应回显 `command_id`、`digest`、`binding` 和监管启动代次，携带
`outcome: not_dispatched | completed | failed | unknown`、可选类型化 `error_code`；成功时内联
`payload` 与 `result` 描述符恰有一个。描述符为 `{result_id, total_bytes, sha256, expires_at}`，
对应不可变的 Controller 响应 UTF-8 JSON 字节。身份缺失／不匹配、负载格式错误都不能当作成功。
chunk 校验保留结果的 scope，不要求其旧页面代次仍为当前代次。

请求体拒绝未知键。`arguments` 使用共享 MCP 操作 schema，最多 32 KiB；注入本地字段后的 Controller
请求必须满足其 48 KiB 上限，远端消息必须满足协商上限。记 journal／分发前同时检查两项。
公开 ID 保留已有长度限制，仅生成的本地 Controller token 使用更窄的 ID 语法。
token、profile、lane 和传输路由均不能作为调用者自由配置。

摘要对 `{operation: <外层 ISCP 操作>, body: <令 digest="" 的请求体>}` 的规范化字节计算 SHA-256，
沿用递归排序 JSON／Go 兼容转义规则，覆盖 binding、预算以及内层 execute 操作。
增加 Go／JS 共用的 Unicode、转义、数组和安全整数测试向量。RPC ID 仅属于传输；每个写类 Controller
子调用有自己的稳定 command ID，重试层不能换 ID 绕过 fence。同 ID 不同输入必须在执行前拒绝。
在等待 journal I/O 前原子占用每个 ID 的执行槽；`HostJournal` 的文件串行写入本身不能阻止并发重复准入。

### 6.2 截止时间与排队

| 预算 | 规则 |
|---|---|
| RPC | 最多 30 s，同时受 caller context 限制，重连不重置 |
| 桌面调用 | 从接收起最多 25 s，包含排队／启动／执行／序列化 |
| 导航／动作／显式等待 | 上限分别 20 s／10 s／15 s，并受调用剩余时间限制 |
| Acquire | 桌面 FIFO，每客户端最多 32 个等待请求；等待包含冷启动；取得槽位后给本地 Controller 的 `wait_timeout_ms=0` |
| 结果组装 | 从取得描述符起最多 30 s，且不超过原调用方剩余截止时间 |
| 清理 | 独立有界清理预算，强制终止子进程前给予 5 s 宽限；清理期间不允许继续远端输入 |

排队到期／取消必须在 acquire 前移除队列项。分发前再次检查任务、授权、启动代次和剩余预算。
保留清理／响应余量；到期不能只是停止等待却让子进程继续执行。迟到 acquire 必须释放并阻断，不能交给
新 ISCP 会话。取得租约期间保留原请求截止时间：现有 Playwright adapter 使用 `context.WithoutCancel`，
客户端集成必须分离 acquire context 和较长寿命的 session context，不能因此丢掉预算。
D1 仍是一客户端一个活动任务；队列满／等待到期在分发前返回已有 `browser_busy`。
已分发调用到期时，取消本地工作并撤销该租约的 adapter 权限；若子进程无法停止操作，在准入新任务前
终止其自有 MCP 执行。只返回超时不够；该清理不能关闭其他健康的 ISCP 调用。

### 6.3 大结果

协商后的操作上限最多 63,488 字节。计量**最终编码的整条消息**，包含外层包装／转义；48 KiB 内联负载
只是初筛阈值。更大的响应使用结果描述符。每个解码后的 Controller 响应最多 **10 MiB**，包含
JSON／base64 开销；每桌面保留结果字节总预算为 **20 MiB**，修正原提案总共 8 MiB 与单结果上限冲突的问题。
分发前预留容量，响应解析和分配计数有界，不能驱逐存活结果来准入新写操作。
容量不足时在分发前返回类型化容量错误；超过 10 MiB 明确失败，不截断或偷偷改变格式。
大结果预留只针对 execute。status／acquire／release 使用符合消息预算的有界小响应，不预留 blob；
结果缓存满不能阻碍 release 或只读回执／核对。临时解析／base64 缓冲区同样有界，最多一个 execute
和四个 chunk 同时在途。

每块最多 **32 KiB 原始字节**，base64 约 43 KiB；更小协商上限下继续缩小并核对最终信封。
最多四块并行，并进一步受协商容量限制。校验整数范围、边界、ID、每块精确长度、最终大小和 SHA-256。
同偏移重复读取必须得到相同字节，拒绝其他会话的 result ID。只有完整校验后才解析结果，不能发布部分成功。

不可变字节从创建起保留 60 s，ISCP 会话结束／撤销时提前释放；重复读取不延长 TTL。
**不能“读到最后一块即删除”**，否则最后一个响应丢失就无法重取。不新增结果 ACK 操作。
过期返回类型化不可用，重新取结果不能重新执行原写操作。
区分动作结果与负载可用性：拉取分块前先持久化已验证的完成回执，之后负载丢失／哈希失败报告结果不可用，
不能声称动作失败或重放。MCP 现有 8 MiB 输出预算是独立的内层限制。
P0 测量最坏情况传输能否满足 TTL 和调用预算；改 JPEG 等格式需明确修订契约并验收图像质量，不能自动作为
延迟问题的替代方案。

### 6.4 持久化结果与核对

写操作包括 acquire／release、`page.click/fill/navigate/select/type`、`tabs.new/close`。
`page.info/read/snapshot/screenshot/wait` 和 `tabs.list` 在本边界为只读。
因此公开 read／snapshot 若先执行导航，导航仍是独立记 journal 的写操作。
分类放在一份共享操作契约中，测试 Go 与 JS 使用方的一致性。

| 观察结果 | 持久化处理 |
|---|---|
| 证实在向本地 Controller 分发前拒绝 | `not_dispatched`；不声称发生业务效果；只在当前授权有效且无已有 fence 时重试 |
| 可能已分发，响应丢失／格式错／取消／超时 | Gateway fence 与桌面 journal 保留 `unknown`，阻止受影响任务 scope 的后续写入 |
| 有效完成响应 | 回应前持久化 `completed`，释放写操作槽；输出传输仍可能独立失败 |
| 分发后报错 | 只有结构化证据证明未产生效果才能记 `failed`，否则记 `unknown`，HTTP 4xx／5xx 或 Controller retryable 码也不例外 |
| 重复 ID／摘要 | 返回存储状态／仍存活的缓存结果，绝不重新执行；完成结果已丢失则明确报告不可用 |
| 重启发现 `dispatched` 行 | 按 `unknown` 加载，保留原 ID／摘要和 installation scope |
| 所有者核对 | 使用已有 `observed_completed`／`observed_not_applied` 操作并匹配摘要，不自动重放动作 |

发送前持久化 Gateway fence，本地分发前执行 `HostJournal.begin`；磁盘／容量失败阻止准入。
journal 完结持久化失败不能返回成功。保留现有无内容 journal 格式和历史记录，不按 TTL 删除去重历史。
空 receipt 列表、404、缓存到期、socket 写成功均不能证明业务结果。
通用 status 不能解决某个命令的状态。`browser.receipt` 仍是**正向**查询 Gateway fence，不是反向查询
桌面结果。核对保持幂等，原有本地／远端记录均收敛后才重新允许写入。
人工核对使用当前授权下已有的用户可见页面／个人浏览。如果自动观察需要新 acquire 或导航写操作，
仍须等所有者解决 fence，不能新增可绕过 fence 或复活旧写租约的恢复开关。

## 7. 注册表与协商

`services/gateway/internal/iscpworkbench/operations.json` 是注册来源，
`scripts/sync-iscp-operations.mjs` 生成投影。新条目均为 v2、`browser.control`、`business`，
上限 63,488，不映射业务 HTTP：

| 操作 | 方向 | `mutation` | 恢复 |
|---|---|---|---|
| browser.controller.status | reverse | false | read |
| browser.controller.acquire | reverse | true | operation_receipt |
| browser.controller.execute | reverse | true | operation_receipt |
| browser.controller.release | reverse | true | operation_receipt |
| browser.controller.chunk | reverse | false | read |

现有注册表的 `mutation` 是布尔值，execute 必须保守声明 `true`，不能填写“按操作”。
内层操作表决定只读重试及写 journal。注册表 `operation_receipt` 是策略声明，不代表通用反向持久化
重放已经实现；实际行为由 §6.4 提供。保留 `browser.receipt`（forward、`browser.control`）和
`browser.reconcile`（forward、原有 `browser.read` scope），不能顺手扩大或改名。

`browser.controller.v1` 是 status 返回的协议值，不是虚构的 ISCP 操作名或新 Relay 能力。
要求已认证的协商操作集中包含全部五项，且 status schema／限制匹配。
Gateway qualification／能力投影与桌面 surface 依赖同步更新。继续以 `capabilities.browser` 和 Gateway
browser 能力族为开关，连接／使用时检查可用性，不增加常驻轮询。
引导阶段 status 根据已协商会话／操作 scope 授权，在启用浏览器 UI 能力前执行；不能反过来依赖
需要它来确立的进程就绪 browser 标志。
老版本／混合版本仅关闭浏览器自动化，其他能力继续工作。格式错误的应用请求返回类型化拒绝，
不能破坏健康的整个会话。

## 8. 安全与流量边界

授权复用现有设备会话，不新增设备 grant、心跳、远端调试端口或传输旁路。
每次排队分发／chunk 都检查 owner／client／installation、确定的 ISCP session、授权代次、任务／租约
及相关启动代次。token、本地 socket 路径、连接凭据不能进入远端 schema 或 renderer IPC。

Electron adapter 将 debugger 限制在任务持有的页面／frame／弹窗，阻断个人页／工作台／其他任务目标和
全局 Cookie 访问。本地受审计 MCP 内部可执行固定页面读取逻辑，调用者不能通过通用参数提供代码。
固定源码／导入闭包和可执行文件路径，拒绝 runtime loader 和包路径逃逸；文件哈希证明包完整性，
不构成新的操作系统沙箱。

“零直连业务 HTTP／WS”指 Gateway 到客户端的控制路径及客户端到 Gateway 的业务不能绕过 ISCP。
本地 UDS 上的 Controller HTTP、本地 Playwright extension 回环 WS、浏览器正常访问网站的 HTTPS
均为预期流量。验收记录目标地址和进程归属，避免把正常流量误判为旁路。
封锁 ISCP 后远端控制必须不可用，个人浏览仍按已有策略工作。

## 9. 保留原界面

保留原组件、菜单、布局、样式和交互。在现有浏览器面板／任务标签、显式会话选择、只读任务视图、
隐藏／加宽控件和核对操作下接入适配器。保留任务完成后的会话视图、macOS 关闭／Dock 重开行为。
工作台导航／崩溃时隐藏原生视图，直到新的显式选择和布局边界到达，不能借此给新 Controller 授权旧页。

不支持或暂时不可用的自动化保留原入口并禁用。忙任务使用现有任务活动呈现，不新增常驻技术状态、
传输选择器或简化页面。失败／恢复文案仅在用户需要处理时出现。
同时验收原交互与新链路；功能测试通过不能证明原界面已保留。

## 10. 实施阶段与切换

| 阶段 | 工作与完成证据 | 当前状态 |
|---|---|---|
| P0 可行性 | 真实随包 Electron Node／API／fuse 验证；Mac adapter；无可选导入的通用 MCP；frame／弹窗／ref／输入／截图 fixture；Mac ↔ work2 冷启动、完整工具与分块计时 | 源码及 Mac／Linux 随包 runtime 通过；完整远程计时门槛仍开放 |
| P1 本地 runtime | 可复现 manifest、客户端入口、进程监管、归属映射和保留视图生命周期；已安装包离线跑通本地 fixture | 源码生命周期及隔离 Mac／work2 x64 包通过；完整安装／离线矩阵待验 |
| P2 ISCP | 注册／投影、service／client／host、严格转发器、journal／fence、分块；隔离 Relay 故障注入与无业务旁路 | 已接入；故障恢复门槛未关闭 |
| P3 原生验收 | 已安装 Mac 和 work2 Linux 桌面经 TEST1 连接真实 work2；满足 [ISCP 扩展设计](desktop-iscp-capability-expansion-design.md) G1–G4、§11 矩阵，并与旧路径记录比较延迟 | 待完成 |
| P4 切换 | Gateway 与 Mac／Linux 包成对发布，删除旧路径，验证回滚与数据保留 | 待完成 |

删除前记录旧路径性能／UI 基线。P0–P3 可使用隔离候选构建；生产不保留双路径 runtime 选择器。
所有者已要求持续实施并保留最终验收；候选版本准备正在推进，生产发布遵循执行时的所有者授权。

P4 删除活动 `browser.command` 注册／生成项、`Broker` 反向分发、桌面处理器／原生执行器／
`host-socket.mjs`、LAN host grant／connect／revoke／fences 路由及专属 grant／心跳机制。
**保留** broker scope／receipt／fence 服务、`HostJournal`、工作台 IPC／核对 UI、后端 `BackendHost`
和共享页面／隔离助手。先迁移继续使用的助手，以新归属／恢复覆盖替换过时测试。
只有正向 ISCP receipt／reconcile 不再依赖 LAN HTTP handler 后才能删除这些端点。
活动 runtime／构建引用必须清零，历史文档／证据无需抹除。

上线顺序：停止浏览器新准入，等待活动任务结束，保留未决 fence 及原 ClientStore／profile／installation
绑定，安装匹配构件，能力验证完成后启用。
回滚顺序：停止准入和子进程，恢复整套前版 Gateway／客户端／helper 与兼容回执数据，再验证原连接／UI。
旧程序不能把新版本未决写入解释为可重试；若无法证明兼容，浏览器保持禁用，先完成前向修复／核对。
不能用清空数据、新测试身份或单命令回退替代整版回滚。

## 11. 验收矩阵与证据

下列每行都需要完整验收证据；§14 区分当前源码／fixture 覆盖与剩余门槛。记录源码／包哈希、架构、Electron／Node 版本、fixture／provider、
脱敏的精确请求 ID、预期／实际结果、延迟和失败产物。
源码测试、原生 fixture、已安装包、真实 provider 是不同证据范围。报告失败和跳过，不可用平台不能记为通过。

| ID | 场景 | 必须观察到 |
|---|---|---|
| A01 | 新旧 peer／缺能力、仅 LAN、Controller 缺失 | 原入口保留并禁用、类型化不可用、不回退后端、其他功能不受影响 |
| A02 | 打开／读取／snapshot／fill／type／click／select／wait／截图 | 公开 schema 一致且页面确有对应效果；过期 ref 被拒；重定向／冻结目标规则保留 |
| A03 | iframe、跨域 frame、弹窗、动态控件 | 目标归属正确、ref／效果正确、不枚举个人／其他任务目标；不支持案例明确阻止发布 |
| A04 | 两任务／会话、队列满／到期／取消、冷启动 | 一个活动 Controller session，过期请求不延后启动，不切会话，队列和清理有界 |
| A05 | 同 ID 并发、变摘要、响应丢失、journal 完结失败 | 最多执行一次，持久状态／fence 正确，不重放，换 ID 不能绕过未决任务 fence |
| A06 | Controller／MCP 崩溃、ISCP 重连、Gateway／桌面重启、撤销／锁定 | 拒绝旧代次，未决写为 unknown，无迟到准入／孤儿进程，保留个人数据 |
| A07 | 内联边界、转义 JSON、>39 KiB 截图、10 MiB 上限、缓存满／到期 | 完整消息符合上限，大小／哈希／MIME 正确，写前容量拒绝，不静默截断 |
| A08 | 最后分块丢失／重复／乱序，伪造 scope／偏移／哈希 | 到期前字节稳定可重取，拒绝损坏／跨域访问，结果丢失不重做完成的写入 |
| A09 | 正常完成、显式关闭与异常清理 | 完成后保留会话视图，接管时全新 ref，显式关闭生效，旧 debugger 凭据失效 |
| A10 | 原面板／标签／选择／隐藏／加宽／核对、renderer 崩溃、Mac Dock 重开 | 原视觉、几何与原生交互保留，无新增常驻状态 |
| A11 | 已安装离线包与进程退出 | 不依赖系统 Node／下载，自有子进程和 socket 文件清理，manifest／权限失败关闭能力 |
| A12 | Mac 与同机 Linux 经 TEST1 到真实 work2；封锁 Relay | 正确设备执行，无直连业务 HTTP／WS 回退，后端邮件路径和个人脚本仍正常 |
| A13 | 有未决 fence 时成对升级／回滚 | 原 installation／profile／数据保留，新旧写入不能逃过核对，旧活动源码引用清零 |

随实现改动运行 Go `browsercontrol/browserhost/browserautomation/iscpworkbench/gateway` 定向测试、
Controller Node 测试、桌面 transport／journal／registry／presentation 测试、注册投影检查、包审计及
双语文档检查。可选装配提取需补后端邮件／AI 处理器回归。
实现集成以全仓检查为门槛；初始文档修订不建立原生测试结论，当前实现证据范围见 §14。
P0 计时记录样本数、冷／热启动、完整工具子调用数、负载大小、中位／尾部延迟与分块吞吐；
单次 RPC 成功不是端到端性能结论。

## 12. 已记录的所有者决定

记录于 2026-11-09；本次不增加新的所有者批准。

| ID | 决定 | 影响 |
|---|---|---|
| D1 并发 | 每客户端同时一个活动浏览器任务，其他在 acquire 排队 | 保持 Controller 单会话模型；桌面有界准入，沿用 busy 失败（§6.2） |
| D2 旧路径 | P3 之后删除 | 反向 `browser.command`、原生执行器和 LAN 直连 host 一并切换删除；保留 journal／receipt／reconcile 与原 UI（§10） |
| D3 Linux 客户端 | work2 同机桌面也经 ISCP 使用随包 Controller | 后端 systemd Controller 仅采集；Linux 客户端包与 Mac 同步验收发布 |

P0 未知项属于验证门槛，不是缺少产品决定：随包 Electron runtime 兼容、导入闭包、任务视图保留和真实
传输／完整工具性能。失败必须记录技术修订，不能用“设计完成”或单元测试通过替代。

## 13. 实施归属与源码索引

以下为仓库相对路径；Controller 客户端、host、监管器与转发器已进入源码，打包和原生验收仍是独立门槛。

| 区域 | 源码归属 |
|---|---|
| 客户端契约 | `services/gateway/internal/browsercontrol/client.go`、`runtime_session.go`；`iscp_client.go` 与 `iscp_session.go` |
| 作用域、代次、fence | `services/gateway/internal/browserhost/adapter.go`、`reverse.go`、`fences.go`；`client_controller_host.go` |
| 共享工具语义 | `services/gateway/internal/browserautomation/playwright_extension_{adapter,session,snapshot,settle}.go` |
| 传输集成 | `services/gateway/internal/gateway/workbench_iscp_browser.go`、`internal/iscpworkbench/{reverse_session.go,operations.json,negotiation.go}` |
| Main 归属 | `apps/desktop/src/main/{main,iscp-transport,iscp-capabilities}.mjs`；`browser/{controller-runtime,controller-forwarder}.mjs` |
| 页面／journal 生命周期 | `apps/desktop/src/browser/{adapter-server,page-registry,host-agent,host-journal}.mjs` |
| Controller 装配 | `tools/browser-controller/src/{controller,mcp-client,http-server,electron-adapter-client}.mjs`；`client-main.mjs` 与客户端包 manifest／lockfile |
| 打包 | `apps/desktop/package.json`、`apps/desktop/scripts/{package-macos,audit-package}.mjs`、根目录桌面构建脚本 |

依赖顺序为 P0 → 本地装配／生命周期 → 传输／回执 → 原生验收 → 删除旧路径。
可选处理器的机械提取与传输／行为改动分开提交，全程保持后端采集测试通过。

## 14. 实施检查点 — 2026-11-09

通用 Controller／MCP 核心和 Unix 服务基础能力已由明确的后端、客户端装配共享；后端继续注入
邮件与 AI 处理器。客户端 lockfile 只锁定 MCP、Playwright 和 Playwright Core。
`build:client-controller` 构建专用载荷；Mac／Linux 打包入口将其放在 ASAR 外，`afterPack`
检查文件清单、执行权限、锁定版本和 manifest 哈希。尚未发布安装包。

`ClientControllerRuntime` 在惰性启动前校验文件，以固定环境使用 Electron 的 Node，检查私有短
socket 路径，通过独立进程组执行有界退出。生产模式 adapter 只接受 main 签发的一次性任务关联。
PageRegistry 支持显式正常释放保留、新 lease 接管，以及不切换前台焦点的逻辑选页。这些组件已由
原生 fixture 验证；该初始检查点早于下文记录的 `main.mjs` 和 Gateway 接入。

Mac ARM64 本地证据使用 Electron 44.4.3／Node 24.21.0／Chromium 152.0.7977.130，隔离 profile，
子进程 PATH 为 `/usr/bin:/bin`。12 项原生检查全部通过：读取／填写／点击／选择／快照／截图、
renderer sandbox、不依赖系统 Node、iframe 引用及效果、归属弹窗、正常释放保留、新 lease 接管、
显式关闭、崩溃撤权与子进程清理、新 incarnation 和正常退出。这是单个源码运行时场景，
不代表安装包或延迟验收完成。

保留初始失败证据：JS 与 Go 均拒绝 `f1e2` 框架引用；macOS 长临时路径被拒绝；非活动弹窗覆盖了
应保留视图；acquire 将接管的视图导航到 `about:blank`。修复保留 frame 命名空间、使用规定短路径、
跟踪逻辑选页而不激活窗口，并只在客户端装配中省略首次导航。后端任务页面创建行为不变。

验证：Controller 358 通过／151 跳过；桌面 632 通过／1 跳过；共享 MCP、relay、关联、包、运行时和
页面生命周期定向检查通过；Go browserautomation 含框架引用覆盖通过。上述为传输接入前的本地装配计数。

源码链路现已使用 `ClientControllerHost` 与保留完整参数的共享 Playwright adapter，经
`ISCPControllerClient`、五个已注册的 `browser.controller.*` 操作和桌面转发器执行。
生产 main 惰性启动随包运行时；就绪探测不获取页面，旧 peer 不回退 LAN host。
旧命令／原生执行实现暂留至 P3 删除门槛；后端采集／邮件装配和原 UI 组件保留。

线协议验证完整可信作用域、代次、incarnation 和规范摘要。写入前持久化 fence，排队与执行共用
截止时间。结果采用 10 MiB／20 MiB 执行／缓存上限，写前预留容量、不可变分块与有界下载并发。
完成回执之后丢失输出仍保持 completed，不能重做；内联重取也共享同一有界字节缓存。
journal 完结落盘失败时保留可见 unknown fence，同时允许 release 清理。

普通 Quit 现在等待自有资源清理；Electron 本身不会等待异步 `before-quit` 回调。
私有父进程存活管道也会在桌面主进程死亡时结束 Controller／MCP。源码原生检查已覆盖普通退出
和强制终止主进程；一次强制终止样本中进程组约 52 ms 消失，不能视为延迟分位数。
本地转发器 fixture 已通过 14 项检查，包括真实大截图的分块重组／哈希及末块重读。

一次性 Docker Relay／Gateway 配合真实桌面 main，以 mock 模型完成经加密 ISCP 的打开和填写。
原有每字段审批约束单次 fill；DOM 输入值和显示文本均变为 `Alice`。释放后保留视图并解绑
调试器，sandbox 保持启用，普通 Quit 结束 Controller。另一个大页面场景成功执行 84 次分块读取，
直连业务 HTTP 尝试为零。fixture 沿用原 installation，保留早期未决 fence；成功运行前后该记录
完全一致，没有新增未知写入。这些证据覆盖传输和原生效果，不代表真实模型质量、已安装包、
远程 TEST1／work2 或完整原 UI 矩阵已经通过。

保留的失败尝试暴露了两个独立的 mock／工作流缺陷：向模型可见参数输出旧运行时字段，以及在
尚未填写时把可见草稿内容未变化当成目标完成。mock 参数现遵循当前短 ref schema；表单草稿
始终先评估可见目标，再选择或完成字段操作。回归保留既有审批和不提交边界。
新建 fixture profile 也曾未能进入工作台；测试继续沿用原身份并保留旧 fence。

当前自动化证据：Linux Go 6270 个测试节点通过／216 跳过，build、vet 通过；浏览器相关包 race
通过；桌面 649 通过／1 跳过；WebChat 596 通过且构建通过；Controller 358 通过／151 跳过。
Controller 重跑使用规范化私有临时目录；WebChat 在 Node 26 禁用原生 Web Storage，使 jsdom
持有 `localStorage`。初始环境失败记录保留。剩余门槛包括完整故障矩阵、Mac／Linux 安装包及
原界面验收、TEST1／work2 计时、成对回滚数据保留，以及 P3 之后删除旧路径。

首次注入 31 秒写入回执延迟失败：长进度流结束后的迟到 cancel 被套用新建订阅时间窗口，导致
会话断开。现按当前认证会话保留已结束订阅的原始开启时间边界；完全匹配的迟到取消可安全结束，
身份变化和未知的过期取消仍被拒绝。完整 `iscpworkbench` race 通过。修正后的原生重跑通过
11 项检查：真实 fill 仅一次、任务进入 `unknown`、不重放、其他加密读取成功；观察到实际效果后，
通过原 IPC 只核对本次注入命令，早期未决 fence 完整保留。fixture 初始错误（等待 delivered 而非
unknown、把无序 fence 集合按数组顺序比较）也保留记录。本次已关闭观察到的延迟缺陷，完整故障
矩阵和安装包／远程验收仍为独立门槛。

Mac 候选打包发现并修复了构建器漏包：必须为 Controller 根目录下的 `node_modules` 单独设置
资源复制规则。回归直接使用实际构建器过滤器；Mac 和 Linux 审计均验证全部 207 个文件、
18,734,326 字节。Linux ARM64 AppImage／deb 已构建，包内 Electron 在断网 Linux 容器通过
Node／Chromium 版本、SQLite、fetch 和 WebSocket 检查；这不代表 Linux GUI 已验收。

Mac 候选包经独立一次性加密 Relay 通过六组原生检查：实际包内 main／资源、运行中的随包
Controller／MCP、一次审批后填写、保留的沙箱任务页、基线 fence、原面板／标签／隐藏／重开／
加宽／恢复，以及 Quit 清理（按组计数）。重载后经原侧栏选择会话；原生 AX 和视觉检查显示
原只读面板中的任务页已填写 `Alice`。保留初始 harness 失败：开发版 profile 凭据解锁、误将
shield target 当作首个窗口、要求正常 release 后 MCP 继续存活、未刷新侧栏、在面板内寻找
已 portal 到标题栏的控件，以及视觉观察等待超时。

原生检查还发现真实选页竞态：待创建的个人空白页会覆盖后续显式任务选择并隐藏它。现有组件
拒绝迟到的选择回调，同时保留已创建标签；新回归在旧源码失败、修复后通过，全部 596 项
WebChat 测试通过。work2 只读预检确认 x86_64 且桌面会话运行，已增加匹配的 x64 打包入口和
分架构清单。远程候选验证继续进行；尚未替换生产服务或已安装 Mac 应用，D2 删除仍受 P3 门槛约束。

源码原生崩溃注入现通过 12 项检查：观察审批后的实际填写，在持久完成前丢弃本地回执并强制终止
真实 Controller，保留未知结果且不重放，只关闭自有任务视图，个人标签和早期 fence 均保留；
经原 IPC 核对观察到的效果后，新 incarnation 可以执行后续任务。首次崩溃运行在远程 fence
同步完成前采集基线，导致基线断言失败；fixture 现明确等待同步，失败证据继续保留。

实际 Mac 包另通过封锁 Relay／重连场景（七组检查）。work2 的 x64 AppImage 经哈希校验后解包，
通过其独立 Relay／Gateway 完成八组检查：随包 Controller／MCP、审批及原生效果、原面板交互、
fence 保留、传输受阻且无业务 HTTP 救援、原 profile 恢复及 Quit 清理。Linux 使用
`gnome_libsecret`；实际任务 renderer 启用 Seccomp filter、NoNewPrivs，且用户命名空间与 main
不同。密钥环、Go 和 buildx 依赖仅解包到私有测试目录；临时 AppArmor 规则只匹配候选程序路径，
未使用 `--no-sandbox` 或明文凭据回退。初始缺少 BuildKit、Go 冷构建超时及沙箱启动失败均保留。
Gateway／WebChat／PostgreSQL 的 ID、镜像、配置、完整挂载记录、网络和启动时间与测试前快照
一致；仅挂载数组顺序不同造成的初始比较失败单独保留。这些场景使用 mock 模型和一次性 Relay，
不代表 TEST1 或真实 provider 已验收。

work2 原有 GNOME 会话持续锁屏，其截图接口无法捕获锁定桌面；主 WebContents 截图也不包含
原生子视图。因此另用私有 X11 显示取得完整窗口视觉证据：原侧栏、会话、浏览器标签、只读标题
和已填写 `Alice` 的原生表单同时可见。同一候选包及 profile 在该显示中通过全部八组检查，
未解锁用户现有桌面。

补充恢复证据：真实 MCP 子进程崩溃通过 12 项原生检查；注入两次终态 journal 写入拒绝通过
13 项，包含磁盘 `dispatched` fence、可见 `unknown`、不重放，以及显式核对后的新 incarnation。
初始注入器错误地同步抛出异常，未保持 journal API 的 Promise 拒绝语义；该失败及其未决 fence
保留，修正后的运行完整保留所有既有 fence。Gateway 分块 race 测试另覆盖末块丢失、伪造命令／
结果 ID、错误偏移和总长、重复内容，以及明确错序的重组；已完成变更仍保持完成且只执行一次。

随后 work2 的同一候选包／profile 使用现有 `sparkclaw-product-v1` 真实 provider（`mock: false`），
仍经一次性加密 Relay 验证。全部八组通过，包括审批填写、原控件和 Relay 封锁恢复。单次完整
流程耗时 32,828 ms，包含模型调用、自动化审批及核对轮询；这不是传输百分位或旧／新路径性能
比较。首次真实 provider 启动因隔离网络无法解析 provider 而失败；仅将自有候选 Gateway 接入
本 lab 的入口网络后重启，未发布业务端口。之后三个生产容器的配置／挂载／网络／启动基线仍
一致。TEST1 认证、Mac 到 work2 的真实 provider 覆盖、完整性能对比、剩余安装／离线与故障
场景，以及成对回滚／切换仍待完成。
