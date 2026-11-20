# Workbench 退役迁移

> 语言：[English](../../docs/workbench-retirement-migration.md) | 简体中文

日期：2026-11-20。M0–M5 已实现，验证结果见第 9 节。最初的代码调查基于 `6b26eb2e`。

本文规划如何移除仍夹在连接层和业务代码之间的旧 workbench 传输层。它承接[客户端连接层设计](client-link-layer-design.md)，该文第 9 节记录了业务域经桥接运行、没有改写。连接层契约见[连接契约](link-contract.md)。

## 0. 前提

1. 产品仍在开发阶段，未投入使用。没有需要保持兼容的已安装客户端，也没有需要保护的会话数据。
2. 三端和后端一起构建、一起安装，契约改动可以一步落到所有端。
3. 宿主 WebChat 保留。它继续使用 HTTP 接口，因此业务域的 HTTP 处理函数也保留。
4. 遵守本仓库的 UI 变更边界：页面、菜单、布局和交互不变，只改已有组件下面的数据路径。
5. SparkX 内置浏览器的控制行为不变，只改它的授权来源和承载方式。

## 1. 代码现状

### 1.1 现在一次业务调用的路径

桌面端一次业务调用（例如读邮箱列表）经过这些步骤：

1. 渲染层发出 HTTP 形状的请求。
2. 主进程里原有的 ISCP 请求管线把路由映射成 v2 操作名（`iscp-routes.mjs`、`iscp-transport.mjs`）。
3. 管线写给 `LinkDomainWire`，一个模仿旧 helper 子进程的对象（`link-domain-wire.mjs`）。
4. `LinkDomainWire` 发出一次连接层的 `domain.call`。
5. Gateway 构造一个 `iscpworkbench.Request`，并伪造一个带协商结果的 `iscpworkbench.SessionInfo`（`link_domain_bridge.go`）。
6. 旧的域适配器运行（`workbench_iscp_domain.go`）。其中 92 个操作再构造一个 `http.Request` 去调 HTTP 处理函数。
7. 结果以 HTTP 状态码加 body 的形状返回设备。

第 2、3、5、6 步的存在只是为了让旧 workbench 代码不改就能运行。

### 1.2 清单

| 部分 | 位置 | 规模（不含测试） |
|---|---|---|
| Gateway 域适配器 | `services/gateway/internal/gateway/workbench_iscp*.go`，28 个文件 | 约 4100 行 |
| 对应测试 | `workbench_iscp*_test.go`，22 个文件 | 约 4200 行 |
| 操作表和旧传输包 | `services/gateway/internal/iscpworkbench` | 约 4900 行 |
| 连接层桥接 | `link_domain_bridge.go` | 216 行 |
| 桌面请求管线 | `apps/desktop/src/main/iscp-*.mjs`，16 个文件 | 约 2000 行 |
| 桌面旧启动路径 | `desktop-auth.mjs`、`execution-client.mjs` | 约 1200 行 |
| WebChat 传输分支 | `apps/webchat/src/desktop/LocalWorkbench.tsx` 及另外 11 个文件 | `LocalWorkbench.tsx` 里 13 处 `iscp` 条件 |
| 旧 helper 程序 | `services/gateway/cmd/iscp-workbench` | 只有资格验证启动器在用 |

操作表共 159 个操作：

| 分组 | 操作数 | 经连接层的状态 |
|---|---|---|
| `mail` | 43（40 个为 HTTP 形状） | 桥接 |
| `settings` | 32（全部为 HTTP 形状） | 桥接 |
| `medicine` | 9（全部为 HTTP 形状） | 桥接 |
| `speech` | 8 | 桥接 |
| `notifications` | 3（全部为 HTTP 形状） | 桥接 |
| `tools` | 2 | 桥接，且总是被拒绝（见 1.3） |
| `browser` | 7 | 2 个正向操作桥接；5 个反向操作由连接层反向调用承载 |
| `mcd` | 9 | 3 个正向操作桥接；6 个反向操作由连接层反向调用承载 |
| `events` | 6 | 桥接；与连接层事件重复 |
| `object`、`transfer` | 8 | 桥接；与连接层 `object.*` 重复 |
| `operations` | 1 | 桥接；与连接层回执重复 |
| `presentation` | 3 | 已注册为直接的域方法 |
| `execution`、`approvals`、`phone`、`state`、`capabilities`、`workbench`、`installation`、`audio`、`clients`、`authorizations` | 28 | 被桥接排除；已由连接层核心操作或官网取代 |

### 1.3 桥接造成的缺陷

以下是 2026-11-20 阅读代码得出的，没有在运行的系统上复现。

- **授权取自一个已经不存在的 ISCP 会话。** 会话没有 `CheckAuthorization` 时，`workbenchExecutionAuthorization` 返回空授权（`execution_authorization.go:31`）。桥接伪造的会话正好没有，所以 `tools.list` 返回空列表，`tools.invoke` 返回 403。
- **经 `message.send` 发起的运行不带执行授权。** `executionMCDSelection` 需要它（`execution_mcd.go:13`），所以麦当劳点餐资源永远不会挂到连接层的运行上。该功能默认关闭，问题因此没有暴露。
- **两套事件机制并行。** 桌面端仍通过 `domain.call` 轮询 `events.pull`、`events.ack`、`events.snapshot`（`iscp-event-client.mjs`），同时连接层在推送自己的同步和审批事件。
- **两套大对象机制、两套回执日志并存**：旧的 `object.*`/`transfer.*` 和域回执日志，以及连接层的 `object.*` 和 `receipts.json`。
- **桌面端的域调用有 64 KiB 的响应上限**（`link-domain-wire.mjs:8`），而连接层一帧允许 600 KiB。
- **v2 信封不在一致性向量里。** 第二种客户端语言要用业务域，就得照着 Gateway 源码重新实现 `params`、`body`、`expected_revision` 和 `input_digest`。

## 2. 目标

```
 设备 ──(局域网 / Relay)──▶ 连接器 ──▶ 连接层入口 ──▶ 域方法 ──▶ 业务函数
                                                                    ▲
 宿主 WebChat ──(HTTP)──▶ HTTP 处理函数 ────────────────────────────┘
```

- 一个业务域只有一组业务函数。HTTP 处理函数和连接层域方法是两个薄入口，只解析输入，然后调用同一个函数。
- 连接层域方法接收有类型的参数，返回有类型的结果。失败使用连接层错误码，HTTP 状态码不再传到设备。
- 授权只有一个来源：Principal 及其设备条件。
- 事件、大对象、幂等回执各只有一套机制，即连接层的那一套。
- 桌面主进程直接调用 `link.request`，不再有 ISCP 管线和模仿的 helper。
- 手机和手表暂不使用业务域（决定 7.3）。域方法是有类型的，以后它们要接入某个域时，只需为那个域补向量和设备投影，不必返工 Gateway。
- `internal/gateway` 的客户端路径不再引用任何 `iscp*` 包。服务于 JingSi 和 LocalMind 的包（`iscpbridge` 及其依赖）不在本计划内。

## 3. 迁移期间的规则

1. **操作表不再新增条目。** 从 M0 起，新的业务操作用 `registerLinkDomain` 注册，并写进连接契约。
2. **一次迁一个域。** 一个域要整体迁完：Gateway 方法、桌面调用、测试、契约文字和向量。它的适配器文件和路由映射在同一次改动里删除。
3. **没迁的域继续走桥接。** 任何一个阶段结束后都可以暂停，不会留下迁到一半的域。
4. **测试是改写，不是丢弃。** 删除 `workbench_iscp_<域>_test.go` 之前，其中每个用例都要搬到域方法或业务函数的测试里。
5. **界面要对照原有交互验证**，不能只看新函数的测试。
6. **客户端和后端一起走。** 每个阶段以一次配对构建收尾：Gateway 镜像、连接器、SparkX；契约对手机、手表有变化时还包括它们的构建。

## 4. 阶段

| 阶段 | 内容 | 结束时删除 |
|---|---|---|
| M0 | 修复已确认的连接层缺陷；冻结操作表；删除 `tools` 操作 | `tools.list`、`tools.invoke`、`workbench_iscp_tools.go` |
| M1 | 授权改为取自 Principal | 伪造会话在授权中的作用 |
| M2 | 事件、大对象、回执各收敛为一套 | `events.*`、`object.*`、`transfer.*`、`operations.receipt`；桌面的事件、对象和日志客户端 |
| M3 | 桌面主进程直接调用连接层；资格验证启动器迁到连接层 | `LinkDomainWire`、`iscp-transport.mjs`、`iscp-routes.mjs`、`iscp-capabilities.mjs`；WebChat 的 `iscp` 分支；`DesktopAuth`、`execution-client.mjs`、`cmd/iscp-workbench` 和 `iscpnode*` 客户端包 |
| M4 | 业务域逐个迁移 | 每个域一个 `workbench_iscp_<域>.go` 及其测试 |
| M5 | 清除剩余部分 | `link_domain_bridge.go`、操作表、`iscpworkbench` 的客户端部分 |

### M0. 先修连接层缺陷

在有缺陷的底座上迁移，出了问题很难归因。下面这些在任何迁移之前修掉：

| 缺陷 | 位置 |
|---|---|
| 运行事件每条起一个 goroutine 发送，且不带序号，增量可能乱序到达 | `link_runs.go:398-400` |
| 桌面端在局域网和 ISCP 之间切换路径时不重新订阅 | `link-conversations.mjs:77`、`link-auth.mjs:163` |
| 重试未收到回复的发送时丢了附件 | `link-conversations.mjs:392` |
| "是否已发出"靠匹配 Go 报错文字判断 | `link-conversations.mjs:344`、`link-domain-wire.mjs:44` |
| 反向调用不优先走局域网连接 | `link_reverse.go:27-31` |
| 运行状态只覆盖经连接层发起的运行 | `link_runs.go:261` |
| 请求窗口占满时读循环阻塞，取消帧和反向调用的回复也被卡住 | `internal/link/service.go:397` |

`tools.list` 和 `tools.invoke` 在本阶段删除（决定 7.2）：操作表条目、`workbench_iscp_tools.go` 和桌面端的条目副本。它们没有调用方。以后需要直接调用工具时，再以域方法的形式添加。

完成检查：有自动化测试在运行过程中切换路径，流式输出不中断；有测试成批发送增量并按顺序收到；对 `tools.*` 的 `domain.call` 返回 `unsupported_capability`。

### M1. 授权取自 Principal

- 把 `workbenchExecutionAuthorization(session)` 换成一个以连接层 Principal 和设备条件为输入的函数。
- 把它挂到 `message.send` 发起的运行和域方法上。
- 麦当劳和浏览器的权限取决于是否有浏览器界面，以及设备声明的反向操作，不再取决于协商列表。

完成检查：经 `message.send` 发起的运行拥有完整工具集；在测试配置里启用麦当劳选择后，连接层的运行能拿到点餐资源。

### M2. 各收敛为一套

| 旧机制 | 替代 |
|---|---|
| `events.pull`、`events.ack`、`events.snapshot`、`events.subscribe`、`events.resume`、`events.unsubscribe` | 连接层事件只做加速，加上每个域的游标读取。邮件、通知、药箱的失效通知改为连接层主题 |
| `object.*`、`transfer.*` | 连接层的 `object.upload.*`、`object.describe`、`object.read` |
| `operations.receipt` 和域回执日志 | 连接层回执，按设备、方法和幂等键索引 |

域回执日志承载着"结果未知"的语义，邮件发送和邮件收取都依赖它。删除日志之前，连接层回执必须能给出同样的三种回答：已完成并返回原结果、输入冲突、结果未知。

完成检查：桌面端不再发出任何 `events.*`、`object.*`、`transfer.*`、`operations.*` 调用；回复丢失的一次邮件发送能对账到原结果。

### M3. 桌面直接调用连接层

- 主进程的请求层直接调用 `link.request("domain.call", …)`，不经 ISCP 管线，也不经 `LinkDomainWire`。
- 64 KiB 上限取消，改用连接层的帧上限。
- WebChat 组件的属性和行为不变。`operationBackend` 和 `LocalWorkbench.tsx` 里的 `iscp` 条件合并成一条路径。
- 通知不再轮询，改为跟随连接层事件。连接层下不再设置 `activityUnavailable`。

**资格验证启动器（决定 7.1）。** `npm run qualify` 用 `--qualification` 启动 Electron，目前会选中 `DesktopAuth` 和旧的 `iscp-workbench` helper，而普通启动已经不走这条路径。

- 夹具后端改为一个带局域网连接器和本地设备目录的 Gateway，不需要官网和 Relay。
- 启动器在隔离的用户目录里用 `LinkAuth` 加入，浏览器控制、原生输入和崩溃恢复的检查因此都跑在连接层上。
- 这次运行通过后，删除 `DesktopAuth`、`execution-client.mjs`、`cmd/iscp-workbench` 以及 `iscpnodeclient`、`iscpnodeissuer`、`iscpnodeenroll` 三个包。
- 812 行脚本里有多少依赖旧连接，目前没有统计，在 M3 开始时评估。

完成检查：桌面每个界面在局域网和 ISCP 两条路径上的表现都与原来一致；打包后的应用里没有 `iscp-transport.mjs`；`npm run qualify` 在连接层上通过。

### M4. 业务域

顺序从小到大，每行是一次改动。

| 顺序 | 域 | 操作数 | 删除的 Gateway 文件 | 说明 |
|---|---|---|---|---|
| 1 | `medicine` | 9 | `workbench_iscp_medicine.go` | 最小的 HTTP 形状域，用来确立做法 |
| 2 | `notifications` | 3 | `workbench_iscp_notifications.go` | |
| 3 | `speech` | 8 | `workbench_iscp_speech.go`、`workbench_iscp_speech_recovery.go` | 需要麦克风条件；使用流式帧 |
| 4 | `browser`、`mcd` 的正向操作 | 5 | `workbench_iscp_browser.go`、`workbench_iscp_mcd.go`、`workbench_iscp_mcd_host.go` | 反向操作已经是连接层反向调用 |
| 5 | `settings` | 32 | `workbench_iscp_settings.go`、`workbench_iscp_connections.go`、`workbench_iscp_mcp_settings.go` | |
| 6 | `mail` | 43 | `workbench_iscp_mail*.go`、`workbench_iscp_intake_recovery.go` | 最大；必须用真实邮箱验证 |

每个域的做法：

1. HTTP 处理函数里还留有业务逻辑的，先把业务函数抽出来。
2. 用 `registerLinkDomain` 注册有类型的域方法。
3. 把方法、参数、结果和错误写进连接契约，并补桌面端需要的向量。
4. 把桌面端的调用指向新方法。
5. 搬完测试后，删除适配器文件、它的测试和路由映射。

`browser` 和 `mcd` 的正向操作需要浏览器界面，仍然只对桌面开放（连接层设计前提 5）。

**以后手机或手表需要某个域时**，为那个域补完整的一致性向量（成功、每种失败码、写操作的幂等重放）和设备投影（手机收到什么；手表收到精简的纯文本、更少的条目、附件只给文件名）。这是单独的工作，不属于本迁移。

### M5. 剩余部分

- 删除 `link_domain_bridge.go`、`workbench_iscp_domain.go`、回执相关文件、`operations.json` 及其生成代码，以及桌面端的副本 `apps/desktop/src/shared/iscp-operations.json`。
- 把 `iscpworkbench` 缩减到其他入口仍在引用的部分，或整体删除。
- 更新连接层设计第 9 节和本文的状态。

完成检查：在 `services/gateway/internal/gateway` 和 `apps/desktop/src` 下 `grep` 不到 `iscpworkbench` 的引用。

## 5. 验证

| 层级 | 运行什么 |
|---|---|
| Gateway | `go build`、`go vet`、针对性的竞态测试；连接层测试走局域网，有 Relay 测试环境时再走 ISCP |
| 连接层模块 | 在 `services/link` 下 `go test ./...`；为 `linkclient` 的路径切换补测试 |
| 桌面 | 桌面和 WebChat 的测试套件；在两条路径上手动走一遍每个原有界面 |
| 真实服务 | 邮件：在 work2 上用真实邮箱验证发送、草稿、附件和收取。语音：一次真实转写 |
| 手机和手表 | 契约改动影响到它们时，重新构建并跑一次对话和一次审批 |

本工作之前就存在的本地测试失败记录在 SparkClaw SOP 里，不算回归。

## 6. 风险和成本

- **测试改写期间保护会变弱。** 规则 4 把范围限制在一次一个域。
- **邮件无法只靠单元测试证明。** 它的幂等和结果未知行为需要真实邮箱。
- **工作量是实打实的**：约 4100 行适配器、4900 行旧包、桌面约 3200 行，另有同等规模的测试。
- **每个阶段都要配对构建**，包括每次未签名的 SparkX 重建都会触发的钥匙串提示。

## 7. 决定

用户于 2026-11-20 作出以下决定。

1. **资格验证启动器迁到连接层。** 在 M3 完成。
2. **删除 `tools.list` 和 `tools.invoke`**，在 M0 完成。以后有需要时再以域方法的形式添加。
3. **手机和手表暂不接入业务域。** 等后续开发需要某个域时再接入。M4 只服务桌面端，并保持方法有类型，使以后的接入只是增量工作。

## 8. 不在范围内

- JingSi 和 LocalMind 使用的 Bridge `agent.*.v1` 入口。
- 宿主 WebChat 的 HTTP 接口。
- 自建官网和官网适配器。
- 页面、菜单、布局或交互的任何改动。

## 9. 实现情况（2026-11-20）

六个阶段全部实现。

| 阶段 | 结果 |
|---|---|
| M0 | 运行事件带上按会话编号的 `seq`，每个订阅由一个发送者按序发出；电脑端在路径切换时重新订阅，发现缺口时重新同步，重发消息时带上附件和 `replay_only`；用 `not_sent` 取代按错误文本判断；反向调用优先走局域网连接；`run.status` 能从已存运行中回答；请求窗口满时不再阻塞读循环（超出的请求返回带 `not_sent` 的 `busy`）。`tools.*` 已删除 |
| M1 | `linkExecutionAuthorization` 从 Principal 及其设备条件得出授权；`message.send` 发起的运行在开启时获得麦当劳选择 |
| M2 | 业务失效通知改为连接层 `domain` 事件；大的输入输出改为连接层业务域对象（`purpose`）；业务域修改使用带围栏的连接层回执，提供三种答复（原结果、冲突、`outcome_unknown`）以及 `replay_only` |
| M3 | 电脑端经 `LinkDomainClient` 调用 `domain.call`；ISCP 请求管线、`LinkDomainWire`、`DesktopAuth`、`execution-client.mjs`、`cmd/iscp-workbench` 和 `iscpnode*` 包已删除。资格测试启动器改为启动 `link-qualification-backend` |
| M4 | 药箱、通知、语音、浏览器/麦当劳、设置和邮件都是类型化的业务域方法（连接层契约第 4 节）。设置中的绑定、浏览器扩展、MCP 和配对处理函数以及邮件的错误输出被提取为返回 `domainReply` 的业务函数，由 HTTP 处理函数和链路方法共用；邮件方法直接调用邮件服务 |
| M5 | `link_domain_bridge.go`、全部 `workbench_iscp*.go`、业务域回执日志及其清理程序、`operations.json` 及其生成脚本和电脑端副本、`iscpworkbench` 和 `iscpobjects` 包已删除。`services/gateway` 和 `apps/desktop/src` 下没有文件再 import `iscpworkbench` |

**与本方案的差异**

- **设置和邮件返回业务结果 `{status, body}`**，即宿主 WebChat 对同一调用给出的状态码和正文，而不是类型化的值加链路错误。这些界面依据 HTTP 状态码和正文分支，而手机和手表暂不使用这两个业务域（决定 7.3）。等它们接入时，再为相应方法加类型化投影。
- **类型化参数忽略未知字段**，与所有连接层操作一致；原适配器会拒绝。主人身份总是取自 Principal，多余字段无法扩大调用范围。只保留一处封闭输入：收信设置的修改（`mail.intake.update`）拒绝发信服务商的字段。
- **`mail.intake.reconcile` 的参数改为 `{idempotency_key, original}`**，即原修改自己的参数。它会等待仍在进行中的尝试，而不是与之竞争，所以迟到的原请求不会再覆盖已了结的答复。
- **连接层回执按条数限界**（4096 条，最旧的先淘汰），取代原日志按时间的保留和清理程序。
- **删除了局域网邮件对象路由 `/api/v1/mail/attachments*`** 及 `iscpobjects`。只有已删除的局域网电脑端客户端用过它们；宿主 WebChat 没有设备文件，因此从不声明工作区附件。
- **WebChat 保留 `activityUnavailable`**：活动数据无法经连接层获取，按界面规则保留原入口并禁用。
- **已退役的测试工具**：`cmd/iscp-workbench`、`scripts/iscp-*` 和 `scripts/lib/iscp-*` 下的 ISCP 实验脚本、`qualify:desktop-mac-client`，以及电脑端原 ISCP 和局域网客户端的测试。

**验证**

| 层级 | 结果 |
|---|---|
| Gateway | 使用 pypdfium2 5.15.2，构建、vet 和 Go 全套测试通过。三个安装执行器的成功用例也已在 Linux arm64 的真实 tmpfs 上通过。macOS 验证执行器拒绝使用磁盘工作区。测试夹具改用物理路径后，使用默认 macOS 临时目录复跑，77 个有测试的包也全部通过。 |
| 连接层模块 | `services/link` 的 `go test ./...` 通过 |
| 电脑端 | 桌面套件和显式开启的 `link-backend-integration.test.mjs` 通过。集成测试经真实 Gateway 和局域网连接器验证了设置 revision、业务域事件、有序运行和 `replay_only`。 |
| WebChat | `5c6e3b05` 上的 95 个测试文件、626 项测试全部通过。 |
| 配对发布 | Mac arm64 已安装客户端、work2 Gateway 和 Linux 连接器均从 `5c6e3b05` 构建。重启后就绪探针、PostgreSQL、中继连接、局域网证书指纹及 11 台设备的目录同步均通过。前两版均有冷备。 |
| 上线后的发件检查 | 新建的 QQ 自投邮件返回 409 和 `send_disposition=not_started`。已安装客户端提示需要登录，保留可编辑草稿，服务商没有收到发送请求。QQ 重新登录后，另一封自投邮件 QQ05 生成一条发送快照，回执为 HTTP 200、服务商已确认发送。真实 QQ 收件箱和 SparkX 均收到邮件，下载的 72 字节附件与发送前文件的 SHA-256 一致。work2 的 Gmail 账号检查另行返回 503。 |
| Mac 已安装客户端 | 在 `8340a316` 的原界面实际点验了设置、邮件、药箱、通知、麦克风选择与测试、浏览器标签页和麦当劳账号选择器。配对部署后，客户端重新打开了原有用户配置和邮件草稿。 |
| Linux 资格运行 | 两个部署版本均在独立 Debian arm64 环境使用真实 Link 资格后端、局域网连接器、App CLI 和浏览器通过资格运行。最终版本观测到的五个渲染进程均启用 seccomp，浏览器和渲染进程崩溃后的恢复测试均通过。 |
| work2 邮件 | 重启后仍可读取原草稿版本和 72 字节附件。QQ 发件在登录检查返回 409，Outlook 发件在页面检查返回 422，两次均未调用服务商执行发送。另用 Gmail 实发的邮件和附件已到达 QQ 网页收件箱。work2 首次手动同步未能收录该邮件。QQ 重新登录后账号检查返回 200，已安装的 SparkX 中可打开同步进来的邮件并看到附件。 |
| 语音 | 独立局域网 Gateway 接入真实 Whisper 模型，46 帧音频全部获确认，返回四条中间结果和一条最终转写。Mac 客户端已枚举并测试麦克风。work2 当前 ASR 服务报告不支持流式转写，上传 WAV 也被拒绝。 |
