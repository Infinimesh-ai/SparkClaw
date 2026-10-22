# 网络原生邮件架构（v4）

> 语言：简体中文 | [English](../../docs/email-network-native-architecture.md)

- 状态：**设计提案；N1–N3 及 N4 中 SparkClaw/ISCP 部分已于 2026-10-22 部署；实机门禁部分未完成（第 9.2 节）**
- 日期：2026-10-22
- 基线：SparkClaw `456b9fb3`，App-CLI `a91396e`（release 31），ISCP `2a252fe`
- 方向上取代以下文档（保留其全部已接受不变量）：
  [邮件全链路优化](email-pipeline-optimization-design.md)、
  [时间线增量同步](email-timeline-incremental-sync-design.md)、
  [通知唤醒](email-notification-wakeup-design.md)、
  [跨轮复用](email-cross-round-reuse.md)，以及
  [App-CLI 扩展设计](app-cli-email-extraction-design.md) 中的邮件部分。

本文围绕一个事实重组邮件系统：**已登录的网页客户端本身已经通过自己的网络接口
完成 SparkClaw 需要的每个操作，并且邮件变化时已经通知页面。** SparkClaw 应监听
这些通知，把网页客户端自己的请求作为模板复用，用网页客户端自己的响应证明每个
结果，并在页面外只保留一个常驻的小型控制面。不再给能推送的页面套一层轮询循环，
不再通过界面驱动网络请求，不再为每条控制消息付出进程与 fsync 成本。

## 1. 范围与约束

- **只用浏览器会话。** 2026-10-22 负责人否决了标准协议连接器（用授权码或应用
  专用密码的 IMAP/SMTP、Gmail API、Microsoft Graph）及任何 OAuth 注册：它们带来
  请求限额与凭据处理。邮件继续在专用已登录浏览器配置中完成，与
  [浏览器邮件设计](browser-email-workflow-design.md) 和 [外部集成](integrations.md)
  一致。不导出 cookie，不保存凭据。
- **从浏览器自身请求出发。** 每个监听都建立在网页客户端已收到的通知上；每个
  获取或变更脚本都建立在网页客户端自己发出的请求上。DOM 交互只允许作为尚未
  学习到的原生请求的显式*触发器*，绝不作为证据来源。
- **不变量保持**（第 6 节）：结果未知的发送绝不重发；每次调用核对确切账号；
  Gateway Store 仍是业务状态唯一持久化所有者；离开页面的证据有界且不含隐私；
  原有界面与入口保留；未合格的覆盖范围保持可见。

## 2. 现有架构（实际实现）

### 2.1 拓扑更正

邮件获取**不在** Mac 上运行。系统分为两个平面：

| 平面 | 位置 | 路径 |
|---|---|---|
| 获取平面 | work2（Linux），全部本地 | Gateway（容器）→ Controller unix socket → `tools/browser-controller` → App-CLI `MailboxClient` → `ProductClient` → `python -m app_cli --machine` → `node client.mjs` → `executor.sock` → 邮件 handler → HostPort HTTP 回到 Controller → Playwright CLI → Browser Bridge → Chromium 页面 → 受管油猴 Reader → 服务商接口 |
| 展示平面 | Mac SparkX ↔ work2，经 ISCP | Electron → Go helper → Relay（TEST1）→ work2 `iscpworkbench` → Gateway HTTP handler；镜像走 `mail.sync`；原件／附件作为 8 KiB ISCP 对象传输 |

ISCP 从不承载服务商流量。Mac 桌面端 `BrowserHostAgent` 不在任何邮件路径上。

### 2.2 页面已经做好的部分

收信已基于网络：Reader 接管 `fetch`/XHR，保留网页客户端自己的列表请求作为模板
并在页面内重放；原件来自原生接口（QQ `/read/readmail?func=5`、Gmail
`view=att…disp=comp`、Outlook `service.svc/s/DownloadMessage` 并使用页面自身的
请求头令牌）。推送通道已被监听：QQ `wss://wx.mail.qq.com/socket`、Gmail
`punctual/multi-watch/channel`、Outlook worker `subscribeToRowNotifications`。

### 2.3 实测表现

| 测量（来源） | QQ | Gmail | Outlook |
|---|---:|---:|---:|
| 热页面空轮／1 封新信／N 封新信，秒（[跨轮复用](email-cross-round-reuse.md)） | 1.93 / 2.71 / 10.29 | 2.16 / 4.65 / 15.13 | 6.10 / 13.59 / 22.65 |
| 监听常驻时的时间范围查询，秒（[通知唤醒](email-notification-wakeup-design.md)） | 4.36 | 9.24 | 17.22 |
| 服务商收信 → 发现 → 提交，秒（2026-10-15 经验记录） | 9.34 → 11.43 | — | 23.88 → 31.51 |
| 登录检查，秒（2026-10-21，release 31） | 15.10 | 7.88 | 11.24 |

QQ 单封轮次中，页面准备 2.94 秒、发现 0.76 秒、取原件 0.86 秒、写盘不到 10 毫秒——
瓶颈不在持久化，而在编排。

### 2.4 事故画像

2026-09 至 2026-10-21 共记录 73 个邮件问题：围栏／未知结果 18、DOM／页面契约 15、
发行配对 12、ISCP 传输 11、延迟 8、身份 7。反复出现的模式：通用错误码掩盖原因
（4 次）、Reader 部署漂移（5 次）、已关闭页面留下清理围栏（4 次）、把"完成"当作
下一步的证明、Outlook 账号 ID 不等于投递地址。

## 3. 诊断：六个结构性原因

**D1 —— 给能推送的页面套了轮询循环。** 服务商已经告诉页面到达了什么（QQ 通知带
新邮件 `mid`；Outlook `RowAdded` 带 `ItemId` 与 `DateTimeReceived`），但观察器把它
压缩成一个空的 `mailbox_changed` 提示（`App-CLI …/runtime/mail-notification-rules.mjs:7-15`）。
提示随后要等待 Controller 每 5 秒一次的 `tick()` 与一次进程派生
（`tools/browser-controller/src/mail-observer-feed.mjs`）、Gateway 作业认领、收信准入、
2 秒增量间隔和完整区间发现，之后才取原件。Outlook 实测从 Gateway 收到通知到发现
耗时 20.6 秒。

**D2 —— 通过界面驱动网络请求。** Outlook 每一轮区间发现都在 `#topSearchInput`
里输入查询并按回车，只为得到一个可改写的搜索请求（`network-reader.mjs:107-120`）。
Gmail 没有模板时退回到 hash 导航。发送、标记已读、登录探测与账号身份都依赖 DOM
选择器和中英文文案正则（`managed-send-dom.mjs`、`qqmail-mark-read.mjs`、
`*-login-probe.mjs`），这是 `email_page_contract_changed` 与未知结果的主要来源。

**D3 —— 控制面按消息派生进程。** 每条控制消息派生一次 Python：重新校验 82 个
发行文件（约 1 MB 哈希）、重建 Registry 与校验器，再派生 Node 连接 `executor.sock`
（`product-client.mjs:43`、`runtime_transport.py:18`）。`ProductClient.wait` 每 250 毫秒
重复一次，10 秒的读取约派生 85 次进程。每个账本事务另有约 3 次 fsync。

**D4 —— 粗粒度串行化。** Gateway 的服务商锁跨用户覆盖准入、收集、标记已读与发送，
`collect_page` 预算可长达 1 800 秒；同一用户同一服务商的全部操作共用一个 executor
FIFO 通道。

**D5 —— 变更缺少证明。** Gmail 发送只靠约 3 秒内的提示文字匹配；QQ 与单收件人
Outlook 靠已发送列表新首行；Gmail 与 Outlook 标记已读始终返回 `unknown`；回复目标
未在原生编辑器中得到证明。每个缺口都变成 `unknown`、围栏与人工处理。

**D6 —— 展示平面传输成本。** ISCP 对象以 8 KiB 分块严格逐个 RPC 传输（10 MiB 邮件
1 280 次往返，103.7 秒）；每个信封三次重写加密 Relay 日志；work2 每次邮件变更都在
同一互斥锁下列举并 stat 整个回执目录（约 7.1 万文件）；Relay 每 25 毫秒只投递一条
消息，且空闲时每次都开启认领事务。

## 4. 目标架构

### 4.1 原则

1. **页面是网络契约的所有者。** 页面内一个服务商模块统一负责监听、模板、目标
   提取与原生调用；Node 代码不再重构服务商请求。
2. **每个操作 = 原生请求模板 + 原生响应证明。** 模板学习自网页客户端自己发出的
   请求，按形状校验，在页面内用页面自身凭据重放。结果由服务商响应（状态、服务商
   返回码、返回的标识）证明，而不是界面文字。
3. **推送优先，扫描兜底。** 通知携带确切目标并立即抓取；区间扫描仍是完整性保证，
   以兜底节奏运行。
4. **常驻控制面。** 用长生命周期会话取代按消息派生的进程；等待廉价；只在恢复
   需要的地方付出持久化成本。
5. **单一持久化所有者。** Gateway Store、批次日志与 App-CLI 账本职责不变；页面内
   不引入新的持久状态。

### 4.2 组件视图

```
服务商推送通道 ──► 页面监听 ──► 目标提取 ───────────────┐
网页客户端自己的列表／同步响应 ─► 被动记录 ─────────────┤
                                                       ▼
                     页面代理（Reader + 观察器，按服务商一个模块）
                     ├─ 模板注册：learned → qualified → stale
                     ├─ 原件获取队列（服务商节流）
                     └─ 原生变更调用 + 响应证明
                                                       │ 有界事件／结果
executor watch 任务 ──► Controller feed（事件驱动）──► Gateway 观察器
Gateway 推送目标集合 ──► 确切目标抓取轮次 ──► CommitEmailSync
区间扫描（兜底节奏）──────────────────────► CommitEmailSync
```

### 4.3 页面代理

- **监听。** 每个服务商的推送通道（接口不变），加上已在解析网页客户端自身列表／
  同步响应的被动记录器。
- **目标提取。** 每个通知最多产生 16 个*确切目标*：

  | 服务商 | 来源 | 目标字段 |
  |---|---|---|
  | QQ | 通知信封 `type:'0'`、字段 `e`（`mid` 仅用于去重） | `provider_message_id=e`，即列表 `emailid`；选择即邮件本身；文件夹在已知时取自下一条列表行 |
  | Outlook | 带 `Item.ItemId` 与 `DateTimeReceived` 的 `RowAdded`/`RowModified`，或会话 `ItemIds` | 经已记录行解析得到的 `provider_message_id`（ImmutableId 形式）、`received_at` |
  | Gmail | topic 失效通知不含内容；取页面随后自身 `bv`/同步响应中比上次发出的行更新的行 | `provider_message_id`、`provider_native_id`、会话 |

  目标只是*候选*：必须满足与重试目标相同的 `validateMailTarget` 规则，原件头部
  也必须通过现有校验才被接受。页面绝不把主题、地址或正文文字发给宿主。
- **模板注册。** 对每类原生请求（列表、区间搜索、原件、标记已读、发送、身份），
  代理保存网页客户端最近一次发出的请求，按形状指纹（方法、路径、请求头名称、请求
  体键结构）索引。状态：`learned`（见过一次）、`qualified`（合格运行证明了响应契约）、
  `stale`（401/403/形状不符）。模板失效只触发一次界面重新布置以重新学习，绝不盲目重试。
- **原件获取队列。** 每个页面同时只有一个原件在取；QQ 保留 1 秒节流（`-20003`
  反滥用）。推送目标与扫描目标共用队列。

### 4.4 收信事件路径

1. 观察器发出带 `targets` 的 `mailbox_changed`。
2. executor watch 在任务事件中转发目标（有界，按文档去重）。
3. 控制面常驻（N1）后，Controller feed 读取 watch 事件的节奏由 5 秒改为 1 秒，
   并立即发布。
4. **先合格（已实现）。** Gateway 接受可选的 `targets` 字段，在内存中按邮箱保存候选
   （上限 64，15 分钟过期），每次提交轮次后记录每个候选是否命中轮次成员——按邮件 ID、
   条目 ID 或收信时间——以及延迟。日志绝不包含标识；轮次抓取内容不变。原因是标识
   尚未证明一致：Outlook 通知携带可变的 `ItemId`，而合格抓取以 `ImmutableId` 为键；
   Gmail 失效通知不带任何标识。
5. **门禁之后（计划）。** 对候选稳定命中的服务商（第 8 节），Gateway 在递增
   `SignalRevision` 的同一事务中把候选持久化为推送目标集合（有界、绑定代次），下一次
   增量作为确切重试目标传入，即使时间范围发现漏掉也能抓取。Gmail 垃圾邮件仍按与发现
   相同的文件夹策略排除。
6. 区间扫描不变，仍对覆盖范围负责。某服务商推送路径合格后，其周期节奏可使用已有的
   五分钟静默档。通知与新计划现在会立即唤醒空闲的邮件 worker，不再等待一秒空闲休眠。

完全驻留在页面内的抓取流（由 watch 直接产出字节、无需 Gateway 轮次即可提交）是
下一步（N5）。它需要三个存储后端都新增 Store 提交命令，并会重新打开耗费 18 次事故
才稳定下来的围栏语义，因此推迟到 N1–N4 完成测量之后。

### 4.5 原生变更路径

- **学习窗口。** 在 DOM 触发的变更（标记已读点击、发送点击）之前，代理布置一个
  有界窗口，记录窗口内网页客户端自己发出的同源请求：方法、路径、状态、服务商结果码
  以及请求体*键形状*，证据绝不包含值。
- **网络确认结果。** 一旦识别出服务商的变更请求，其响应就成为结果证明：服务商
  自己的发送响应成功并返回邮件标识即为 `sent`，在接受前失败即为 `not_sent`。在此
  之前，提示文字与已发送行证明保留为辅助证据。
- **模板重放。** 合格运行证明请求契约后，用在页面内重放已学习请求（替换目标）
  取代点击；先做标记已读（风险低、幂等）。发送重放只在标记已读重放稳定后按服务商
  逐个提升，响应不明确时绝不重试。
- **回复。** 回复目标是已存储的原邮件服务商标识；用原生回复请求（或直接打开原生
  回复路由）取代在界面中选择目标。

### 4.6 来自网络的身份

账号身份取自网页客户端自身的引导／启动响应（Outlook `startupdata.ashx` 的
`SessionSettings.UserEmailAddress`，已作为兜底使用），DOM 文案只作为合格的兜底。
QQ 与 Gmail 的网络来源通过学习窗口识别，合格后提升。

### 4.7 常驻控制面

- **会话池。** `python -m app_cli --machine --session` 每行读一个 JSON 请求、每个
  请求回一行，每个进程只校验一次发行并构建一次 Registry。`ProductClient` 维持一个
  小型会话进程池（每个进程同时只处理一个请求，因此 Registry 无需线程安全），会话
  失败时退回一次性入口。
- **直连 executor。** Python runtime adapter 可配置 executor socket 路径，直接交换
  现有的 EOF 分隔消息，去掉 Node client 派生。线上消息、授权解析与校验不变。
- **廉价等待。** 会话常驻后，状态轮询只是一次 socket 往返；`wait` 自适应轮询
  （50 毫秒逐步增至 250 毫秒）。
- 公共执行入口、授权模型与 lifecycle 3.0 线上协议不变；改变的是进程生命周期，
  不是协议。

### 4.8 并发

通道按用户、服务商与页面类别划分：共享收信页面（watch、发现、抓取）与变更页面
（发送、标记已读）不再互相排队。Gateway 服务商锁收窄为单个页面操作的准入，而不是
整个收集预算。

### 4.9 展示平面（ISCP）

- **对象流水线。** 服务端已公布信用窗口；桌面客户端按公布窗口并发发送／接收，
  服务端窗口由 2 增至 8。旧客户端继续顺序工作。
- **回执容量计数。** work2 在内存中维护回执数量与字节数，启动时重建、由现有每小时
  调度对账，不再在每次变更时扫描目录。
- **Relay 投递排空（ISCP）。** 成功投递后在同一 tick 内（有界）继续排空待投递
  消息，准入时唤醒接收连接的投递循环，空闲 tick 延长。无线上协议变更。
- **授权复用（需决策）。** 每个桌面 RPC 做两次全新 issuer 检查（单独测量约 1.1 秒
  一次）。类似现有流授权快照的有界复用会改变 ISCP–SparkClaw 契约，另行提案，此处
  不实施。

## 5. 服务商矩阵

| 操作 | QQ 邮箱 | Gmail | Outlook |
|---|---|---|---|
| 变化监听 | WebSocket 通知 → **确切 `e`（emailid）目标** | multi-watch 失效 → **自身同步响应中的行** | `subscribeToRowNotifications` → **确切 ItemId 目标** |
| 发现 | `/list/search` 重放（保留） | `bv` 重放（保留） | 界面输入搜索 → **重放已学习的搜索请求**，界面只用于重新学习 |
| 原件 | `/read/readmail func=5`（保留，1 秒节流） | `view=att`（保留） | `DownloadMessage` + 页面请求头令牌（保留） |
| 标记已读 | DOM 行点击 + 搜索确认 → **已学习的原生请求** | `unknown` → **已学习的原生请求** | `unknown` → **已学习的原生请求** |
| 发送证明 | 已发送首行 → **原生发送响应** | 提示文字正则 → **原生发送响应** | 已发送行／提示 → **原生发送响应** |
| 身份 | DOM 页头文字 → **合格后改用网络来源** | DOM 账号按钮 → **合格后改用网络来源** | 启动响应（保留）+ DOM 兜底 |

加粗项为变化，每项都需要第 8 节的证据门禁。

## 6. 安全不变量（不变）

1. 结果不明确的发送绝不重发；对账只依据证据。模板重放继承此规则。
2. 每次页面调用都重新核对确切账号，不一致即阻断。
3. 推送目标只是确切抓取的提示；只有已提交的抓取改变邮箱，只有扫描推进 `PollThrough`。
4. 页面只暴露有界的标识、哈希、大小、状态码与键形状；绝不暴露主题、正文、地址、
   请求头或令牌。
5. App-CLI lifecycle 3.0（epoch、派发释放、ACK、用户处理）与 Gateway 日志／提交
   语义不变。
6. 原有界面、菜单与入口保留；不增加常驻技术状态显示。

## 7. 否决的方案

| 方案 | 原因 |
|---|---|
| IMAP/SMTP、Gmail API、Graph、任何 OAuth | 负责人 2026-10-22 决定：限额与凭据；超出浏览器会话边界 |
| 通过界面导出／下载原件 | 更慢更脆弱；原生原件接口已可用 |
| Playwright/CDP `response` 监听 | 2026-09 已复现 Gmail/Outlook 页面丢失；页面内监听稳定 |
| 先重写提交模型（页面驻留流） | 风险最高；放到 N5，以测量结果为前提 |

## 8. 门禁与测量

| 门禁 | 证据 |
|---|---|
| 推送目标（每个服务商） | 三次自然到达中推送目标先于扫描提交；目标 ID 等于扫描行 ID；无重复抓取 |
| 已学习搜索（Outlook） | 连续十个热轮次无界面输入；强制 401 后恰好重新学习一次 |
| 原生标记已读（每个服务商） | 记录学习窗口形状；重放经独立列表读取确认；不明确时不重复 |
| 网络发送证明（每个服务商） | 负责人批准的真实发送；服务商响应标识与送达原件一致 |
| 控制面 | 稳态下每条控制消息不再派生 Python/Node；现有生命周期测试通过 |

N1–N4 后的目标：通知 → 提交 p95 QQ、Gmail ≤ 5 秒，Outlook ≤ 8 秒；热页面空扫描
QQ、Gmail ≤ 1 秒，Outlook ≤ 2 秒；work2 上控制消息 ≤ 20 毫秒；10 MiB 对象传输 ≤ 30 秒。

## 9. 阶段

| 阶段 | 范围 | 项目 | 状态 |
|---|---|---|---|
| N1 | 常驻控制面：会话池、直连 executor socket、自适应等待；Controller watch 事件节奏 1 秒 | App-CLI、SparkClaw | 已在本地实现 |
| N2 | 推送目标：观察器提取、watch 转发、feed 字段与 Gateway 命中测量；门禁后作为确切重试目标 | App-CLI、SparkClaw | 测量已实现；抓取使用待定 |
| N3 | Outlook 已学习搜索重放；学习窗口记录器（只作证据） | App-CLI | 已在本地实现 |
| N4 | 展示平面：对象流水线、回执计数；Relay 排空（ISCP） | SparkClaw、ISCP | 已在本地实现 |
| N5 | 页面驻留抓取流与专用 Store 提交 | App-CLI、SparkClaw | 待 N1–N4 测量后再定 |
| N6 | 按服务商的原生标记已读／发送重放与网络身份 | App-CLI、SparkClaw | 实机合格后 |

任何阶段被宣布验收前，都需要在 work2 上实机合格（自然到达、负责人批准的测试发送）；
自动化测试不能证明服务商行为。

### 9.1 实施记录（2026-10-22）

| 改动 | 提交 | 本地证据 |
|---|---|---|
| 常驻会话、直连 socket、自适应等待 | App-CLI `f391ccc` | 20 次状态读取：一次性 2 056 毫秒，常驻 40 毫秒；Python 90 |
| Outlook 已学习搜索重放 | App-CLI `26a0a44` | 40 个区间用例与 4 个 Reader 回退用例 |
| 通知候选目标 | App-CLI `22754ce`；SparkClaw `5fb241be`、`fe89ca9a` | 真实 executor/Registry watch 用例、严格解码契约用例 |
| 学习窗口记录器 | App-CLI `dee39a1` | 只记录形状、脱敏与上限用例 |
| 发行 `.32`、worker r15 | App-CLI `c718108`；SparkClaw `fe89ca9a`、`57e7ad6f` | runtime 477（启用恢复用例）；Controller 291 通过 / 126 跳过；受影响 Go 包与 vet 在 Linux 通过 |
| 回执容量缓存 | SparkClaw `7dc9b003` | 保存／覆盖／退役后缓存与全新扫描一致 |
| 对象流水线、窗口 8 | SparkClaw `9595f139` | Desktop 510；续传与已完成传输回归 |
| Relay 排空与唤醒 | ISCP `f603722` | 关闭空闲 tick 时 24 条连续准入 217 毫秒送达；批次为 1 时失败 |

负责人于 2026-10-22 批准上线及真实测试发信，见 9.2。

### 9.2 部署与实机门禁（2026-10-22）

已部署：TEST1 Relay 为 ISCP `f603722`（仅替换程序，停机 1.5 秒；40 秒内 12 个设备投递
317 条消息）；work2 为源码 `dceb0308`，配 App-CLI `.32`、Tampermonkey 5.5.0.15（r15）与
Bridge 1.0.29（03:51 UTC；41 张表、2 132 个工作区文件、75 815 个回执保全；92 条历史
unknown 执行记录不变）；Mac SparkX 由 `8701f4e7` 构建，该提交重新生成了 `.32` 遗留过期的
桌面托管阅读脚本 preload（6 个 profile、97 张表、22 条消息保全）。work2 上 Tampermonkey
原生导入两次超时，原因是 Chromium 托管策略在 worker 重试预算之后才可读；第三次启动导入成功。

work2 共真实发信 9 封（三轮，每个服务商各发、收三次），全部 `sent`，无结果不明、无重发。

| 门禁 | 结果 |
|---|---|
| 推送目标 | **未通过。** 候选已到达 Gateway，但无一命中：QQ 通知 `mid` 与列表 `provider_message_id` 属于不同标识空间；Outlook `ItemId` 候选未命中；Gmail 无候选。抓取使用继续阻断。随后实测信封显示 QQ 自身的 `e` 字段等于列表 `emailid`（SHA-256 一致）；App-CLI `a849d2d`（未发行）改为发出 `e`，并把 Outlook 搜索行的 ItemId 作为原生定位符返回，发行后可重新测量。 |
| 学习搜索（Outlook） | **部分满足。** 连续 13 轮（04:22–04:35 UTC）搜索框不变，即均未在 UI 输入；强制 401 后重学未验证。 |
| 原生标记已读 | **仅形状。** QQ 记录到 `/read/readmail` 等请求；Gmail、Outlook 尚未发出。重放属 N6。 |
| 网络发信证明 | **仅形状。** QQ 记录到 `POST /send/sendmail`，Gmail 为 `/sync/u/:id/i/s`；Outlook 发信未留下同源页面请求。标识比对属 N6。 |
| 控制面 | **部分满足。** 常驻 `--machine --session` 与直连 executor socket 已生效，控制消息不再逐条新起 Python/Node；但每条 Playwright 页面命令仍新起 `playwright-cli.js`（约 24 次/分钟）。单条消息耗时无法单独测出。 |

对照目标：通知→提交 QQ 为 5.1–8.0 秒，Gmail 为 2.4–11.3 秒（发信占用浏览器时为 85 秒）；
Outlook 未能提交，因其原件解析停在 `modules` 阶段（网页客户端分包编号已变化，非 `.32`
引起），Outlook 来信只停留在已发现、无原件。Outlook 每轮 13–15 秒，每次发信 60–100 秒；
发信会阻塞所有服务商的收信（4.8 节尚未生效）。10 MiB 对象传输未测。

### 9.3 本地学习窗口与 QQ 重放原型（2026-10-22）

App-CLI `claude/mail-native-send` 分支的 `874d1ff` 接续了中断工作。
学习窗口现覆盖有界 XHR/fetch 响应形状、Outlook Worker/MessagePort 回包；
Outlook 发送页也会在导航前安装早期钩子。请求与回包必须属于同一当前学习窗口。

QQ 原型通过 `APP_CLI_MAIL_NATIVE_SEND=qq_mail` 显式开启，仅支持同账号、单收件人、
纯文本新建邮件。独立确认的 DOM 发信可学习一份页面内存模板；过期或账号变化即清除。
旧邮件字段和草稿标识会清空，拒绝不支持的字段与模式；单次派发仍经过既有授权释放和持久 journal。
真实响应标识与独立原生已发送读取尚未合格，故所有重放结果保持 `unknown`，包括 HTTP 200 / `ret=0`。
丢失回包、重启和对账均不重发。原型要求复用仍打开的变更页面，本次未新增页面常驻；冷页面沿用 DOM 流程。

macOS 本地验证：Node 26.2.0 runtime 在文件串行运行且启用恢复用例时 **508/508**、零跳过；
Python 3.11 **90/90**；生成 userscript/binding 与公开树检查通过。基线与候选并行运行均出现两个
短时限 executor 失败，候选完整串行运行通过；规范 `TMPDIR=/private/tmp` 避免基线已有的符号链接路径失败。
新增测试全部使用合成服务商传输，没有真实发信、推送、发行、合入 main、Mac/work2 升级或 UI 修改。
独立的发信/收信通道提交仍待未来配对发行整合与验收。负责人先前要求“部署和真实发信前问我”继续有效；
这些结果不代表 N6 完成，也不代表已获得实机提速。


### 9.4 发信阻塞收信：根因与修复（2026-10-22）

work2 04:00–04:50 UTC 的 executor 账本记录了实际阻塞：04:02:05.8 开始的一次
QQ 发信持续 77 秒，同秒创建的 Gmail、Outlook `collect_page` 分别在 78.8 秒和
78.6 秒后完成，平时热页面轮询分别只需 1.5–3 秒和 10–13 秒。每次发信和 QQ
标记已读（33–38 秒）都有类似现象；发信后的首轮收信因 Reader 页已冷却，耗时 13–29 秒。

不同供应商使用独立 executor 通道，但浏览器整体预留仍将它们串行化，同一供应商的
executor 工作也共用通道。阻塞来自四层：

| 层 | 修复前的位置 | 影响 |
|---|---|---|
| Binding | App-CLI `applications/mail/build-bindings.mjs` | send/mark-read 在 `send`/`mark_read` family 中声明 `activity: 'exclusive'` |
| Browser Controller | `host-driver.mjs` → `controller.mjs` | 独占整个浏览器配置，其他供应商预留等待整个发信过程，`drainIdle()` 关闭所有停驻 Reader 页 |
| Executor | App-CLI `executor.mjs` | `owner + spec.resource` 通道中，send 的 resource 原为供应商，因此同供应商收信排在其后 |
| Gateway | `SendForOwner`、`MarkReadForOwner`、`provider_lock.go` | 操作全程持有同一供应商锁 |

配对 mutation-lane 改动将 send/mark-read 的 `resource` 和 `host.family` 设为
`<provider>.mutation`，并在 binding schema 增加 `mutation` activity。Controller
通过 `reservationPolicy()` 读取 binding：mutation 只预留该供应商的变更通道，
不排空 Reader 页，同时仍让登录、连接验证和通用浏览器访问等待。Gateway 也拆分
inbound/mutation 锁。同一供应商的变更仍跨 owner 串行，取消的等待者不进入浏览器。
任务页位于 Bridge 的非焦点任务窗口，未授权交接时禁止 `Page.bringToFront`，
发信页与 Reader 页不争夺焦点。

单次发信耗时 60–100 秒的主要原因是新建进程。一次 compose 在夹具中执行 23–27
个页面调用；`host-page.mjs` 每次调用单独启动 `playwright-cli.js`，每条命令先
`tab-list` 核对拓扑，click/fill/press 后再 eval `location.href`，总计约 70–85
次进程启动。work2 单次 CLI 冷启动约 0.42 秒；证明等待较短（Gmail 最多 12 × 250 ms，
QQ 列表最多 10 秒）。将受控变更步骤批量执行是另一个改进方向，通道修复使其开销
只影响发信，不再阻塞收信。本节的本地集成不等于部署或实机并发验收。

## 10. 跨项目影响

- App-CLI（SparkClaw 维护的分叉）以配对发行接收 N1–N3 运行时改动；SparkClaw
  通过 `configs/app-cli-release.json` 固定版本。
- ISCP：N4 的 Relay 排空是内部性能改动，无线上协议或契约变化，发往 ISCP inbox
  请求评审。授权复用（4.9）会改变 ISCP–SparkClaw 契约，须先立决策。
- 没有其他项目消费这些邮件操作。
