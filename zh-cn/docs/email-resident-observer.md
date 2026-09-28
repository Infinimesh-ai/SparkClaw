# 邮箱常驻观察器

Controller 为 QQ、Gmail 和个人 Outlook 注册了独立的 `observe` 操作：建立自有后台邮箱页并核实账号后立即返回，CLI daemon 保持连接，通过页面 binding 和仅当前用户可访问的 Unix socket 推送事件。空闲观察不占用提供商预留，也不占用 Reader 页面租约；列表查询与原文获取继续使用原有 Reader。

## 生命周期与边界

调用对应 `*.observe` 脚本，revision=1；输入必须且只能包含 `schema_version:1`、`action`、`account_address`、`owner_scope`。`action` 支持 `start/status/stop`；`owner_scope` 是原有的 64 位十六进制 Owner 摘要。绑定同时覆盖提供商、邮箱地址、Owner、浏览器凭据代次与凭据身份；不同绑定不能接管观察器。健康状态重复 start 复用原任务，degraded 可以显式重建。账号不匹配进入登录处理，打开提供商登录流程会先停掉其观察器。

页面每 15 秒发送本地存活信号，不重复执行 CLI eval，也不为观察轮询邮箱 HTTP。超过 45 秒失联后进入 degraded，经有界退避仅重建自有任务，导航前重新安装 hook；每租约最多成功重建三次，重建失败后等待显式重试。必须通过现有进程归属检查才能移除运行目录；退出时尝试清理全部提供商，单家错误不会跳过其他家。

文档代次与递增序号用于拒绝旧页、重复事件；换页、断流、序号缺口或未知通知形状要求补查。事件环最多 256 条／32 KiB，单事件、解析缓冲、socket 写缓冲及未完成 binding 调用均有上限。原生调用、消息和返回值继续交给网页；Outlook 不克隆或读取 fetch 响应流。脱敏协议形状仅在隔离 Controller 显式设置 `SPARKCLAW_MAIL_OBSERVER_EVIDENCE=1` 时采集，默认关闭；通常状态输出不包含邮箱地址、邮件内容、提供商邮件 ID 或凭据。

Browser Bridge 生产链路现已实现 Gateway 意图协调与持久通知接纳。`qualified:false` 继续关闭五分钟静默档，实际 60 秒兜底检查保持启用；Electron 仍需独立验收。

## Gateway 投递

仅当前用户可访问的 Controller socket 提供三条固定鉴权 POST 路由：`/v1/mail-observers/reconcile`、`/v1/mail-observers/events`、`/v1/mail-observers/ack`。凭据只在 JSON 请求体中传递；新凭据通过原生 Bridge 验证，日常续租和等待通知均不占 Reader 通道。最长 25 秒的本地长轮询在有事件时立即返回；每次协调续租 90 秒，Gateway 消失后回收其自有观察任务。

Gateway 从启用且 ready 的提供商设置、有效收信绑定派生观察意图，并在等待后再次核查。Store 在 Owner 事务内核验邮箱绑定代次、已登记 Controller epoch 和凭据代次，将去重游标、信号版本与原有 discover 任务一起提交，之后才 ACK。重复投递不能再次提前空闲截止时间；Reader 查询期间的新通知保留更高信号版本；错误重试的退避不被通知绕过。

投递环最多 128 条脱敏元数据，与本地诊断环分开。溢出时以每个当前绑定一条补查提示替换丢失历史；新注册、页面换代也触发补查。未确认事件重连后重投，已确认事件才能清理。Controller 重启生成新的投递 epoch，旧实例事件不能复活邮箱。断流将观察状态降级，周期 Reader 继续执行；重连状态快照恢复健康状态，但不推进事件游标。

收信开关仍是权威：关闭的邮箱不订阅，退役绑定的事件只确认、不接纳新工作。同一专用浏览器每家仍只能证明一个账号；多个 Owner 竞争同家时仅观察一个合格绑定，其余绑定保留周期核验。

## 识别规则 v1

| 邮箱 | 认可的变更提示 | 排除的对照信号 |
| --- | --- | --- |
| QQ | `wx.mail.qq.com/socket` 原生 WebSocket 的 `cmd=1`、`scene=notify`、base64 JSON，解码后 `type="0"` 且 `mid` 合法；ID 只在页面内去重 | `cmd=0` 初始化；畸形或未知变更形状不合格 |
| Gmail | 原生 `signaler-pa.clients6.google.com/punctual/multi-watch/channel` 长度前缀 JSON，topic 的 payload[1] 有非空 invalidation body | 建连、标量 ACK、订阅 ACK，以及 payload[2] 中约每 20 秒一次的心跳 |
| 个人 Outlook | Worker／MessageChannel 中归属于 `subscribeToRowNotifications` 的回调：`RowAdded`，或已知行的投递签名变化 | `Reload`、目录／日历通知、普通查询结果、重复行与只改变已读标志的 `RowModified` |

Outlook 同时识别 `Conversation`（会话 ID、投递时间、邮件数量、ItemIds）与收到的 `Item`（邮件 ID、接收时间、明确的 To/Cc-me 标志，排除草稿）。这些签名留在页面内；首次遇到的未知 RowModified 要求补查，不能断言来信。移动邮件也可能使列表失效。通知只提示 Reader 对账，不等于一封新邮件或覆盖水位；Gmail 的 invalidation 尤其不能被命名成“必然新来信”。

## 2026-09-24 现场对照

复用专用 Browser Bridge 登录态，并重新证明三家账号身份。Outlook 收件地址来自真实发出邮件的已验证原文 From，而非登录别名。测试邮件均使用唯一 `SCW-mail-*` 标记，不重复发送结果不确定的邮件。

两轮完整的三家无发信／Reader 对照均为零提示。第二轮同一 Controller 的 QQ、Gmail、Outlook 查询分别为 4.36／9.24／17.22 秒，观察器全程常驻。前一轮驻留测试持续 1,049 秒并清理了自有任务。新版 QQ 与 Gmail 各产生一次提示，且 Reader 原文核验通过：

- Gmail→QQ：`SCW-mail-935680acf76487f1`，`qq_inbound_envelope`。
- QQ→Gmail：`SCW-mail-025becc34080122c`，`gmail_topic_invalidation`。
- 自动重建后的 QQ→Outlook：`SCW-mail-fed11e7136b0d61d`，一次 `outlook_delivery_change` 且原文核验通过。随后仅将该测试邮件显式改为已读，真实 `RowModified` 的 UnreadCount 从 1 变为 0，没有额外提示。
- Outlook→QQ：`SCW-mail-51bac6b718e87c76`，真实原文核验，用于确认 Outlook 实际发信地址。

Gmail 发出测试邮件，以及仅打开该次新测试邮件，均未增加观察器提示。QQ→Outlook 的 `SCW-mail-caf877ff1057662d` 已核验原文，并提供了实际 `RowAdded`／`RowModified` 会话样本。Gmail→Outlook 的 `SCW-mail-82a4fa1a8b1dff80` 则进入垃圾邮件，暴露了 `Item` 结构；虽已按唯一标记在网页确认，限定窗口内未核验到 Reader 收件箱原文，因此不计为 Reader 成功收件。这是实际文件夹覆盖边界，不能写成邮件未送达。

主动关闭自有观察页发现了恢复缺陷：旧页清理报错会在 daemon 已安全回收后仍阻止重建。现已区分已退役页面和进程归属检查失败。最终恢复与来信验证，以及紧凑的现场指标，记录在[证据文件](../../docs/email-resident-observer-validation.json)。

## 复验入口

`scripts/qualify-playwright-email.sh --help` 说明私密配置和隔离 Controller 参数。`SPARKCLAW_TEST_RESIDENT_NOTIFICATION=1` 会启动观察器、空闲 30 秒、执行同 Controller 的 Reader 对照、断言 watching 且零提示，并清理自有观察器。`SPARKCLAW_TEST_RESIDENT_HOLD_SECONDS=1..900` 支持授权互发期间的有界驻留；常驻测试默认不发邮件。

驻留时另以 `SPARKCLAW_TEST_NOTIFICATION_RESIDENT_SEND=1` 和通知测试的 route 参数单次发送并对账，无需启动旧的阻塞观察探针。Outlook 目标必须使用已验证发出原文导出的私密 JSON 地址证明。原有 Outlook 发信 helper 在该浏览器未通过草稿校验；成功的发信证明来自原生写信界面，失败尝试不计作成功发信。

回归覆盖原生消息透传、Gmail 分片拼接、异常流、分类与已读排除、缓存上限、账号／Owner 隔离、旧文档拒绝、释放提供商预留和恢复进程归属检查。

第二次长驻留在对照完成后触发 Go test 默认十分钟超时；共用验收入口现已显式设置 `-timeout=30m`。证据保留这次 harness 失败，不计为成功长跑。

## 2026-09-24 生产验证

Gateway 镜像与 Host Controller 已部署，最终启动时间为 UTC 10:51:14。现有部署身份及客户端设置已保留。三家收信现均已开启，生产观察器处于 watching；本次按用户明确授权，通过 Gateway 正常鉴权设置接口开启 Gmail／Outlook，并完成下述端到端实测。

两封真实 Gmail→QQ 测试邮件均经过生产 Gateway；发送后验收没有调用 Reader discovery／capture。`SCW-mail-349cb322e46cbbc1` 在 UTC 10:47:23.457499 入库通知，10:47:23.597253 开始 Reader 查询，10:47:24.239137 提交原件，分别相隔约 140 ms／782 ms，原文 SHA-256 独立核验通过。Gateway／Controller 重启后，`SCW-mail-23343fa87c5b040a` 再次触发持久提示并取得生产原件；该次保留了已有错误退避，因为定时 Reader 曾与测试发信的独占操作冲突。最终信号／对账版本均为 11，观察状态 watching。

重启检查发现：清理报错会让 HTTP listener 继续存活，直到 systemd 60 秒超时。现已保证清理失败时仍关闭 listener／socket，并尝试观察器和 Reader 两类清理。随后的重启记录正常 Controller 退出，耗时 0.22 秒。

完整部署入口的只读检查拒绝了该机器旧式整合 `.env.local`；本次沿用现有 Compose 配置定向替换 Gateway、重启 Host Controller，保留部署身份、桌面能力文件路径、搜索设置和模型服务。旧 Gateway 镜像保留为 `sparkclaw-gateway:before-mail-observer-20260924`，可用于回滚。

生产复验在已授权 resident-send 测试上增加 `SPARKCLAW_TEST_NOTIFICATION_GATEWAY_RECEIPT=1`。单次发送后仅读取 Store 与本地原文，核对已分类提示和 SHA-256；测试不创建收信订阅、不开启收信开关。

### Gmail 与 Outlook 生产启用

此前正例验证了识别规则与 Reader 行为；以下追加测试开启两家的收信开关，验证已部署 Gateway 链路。发送后验收程序没有调用 Reader discovery／capture：

| 方向／唯一标记 | 通知持久化（UTC） | 原文提交（UTC） | 通知至原文 | 结果 |
| --- | --- | --- | --- | --- |
| QQ → Gmail／`SCW-mail-744e2d9d42900d56` | 11:04:41.055607 | 11:05:41.143568 | 60.088 秒 | 原文 SHA-256 通过；信号／对账版本 3/3 |
| QQ → Outlook／`SCW-mail-749af8363e730259` | 11:11:29.875987 | 11:12:04.987498 | 35.112 秒 | 原文 SHA-256 通过；信号／对账版本 3/3 |

两次分类通知入库时，均保留了已有 Reader 重试截止时间。测试发信需要浏览器独占操作，可能与定时 Reader 冲突；调度器维持原有错误退避。这些是现场样本耗时，不是时延上限承诺。观察器持续处于 `watching`，等待通知时不占 Reader 预留。

Gmail 启用时的初始历史区间超过 50 封，现有溢出策略记录了一个持久 `coverage_gap` 和一个未确认警告，随后继续处理当前区间。真实新测试邮件已成功采集；不宣称历史完整，本次没有清除或确认该警告。

首轮 45 秒生产无发信对照（UTC 11:02:33–11:03:18）三家分类提示均为零，观察状态均健康。三家收信继续保持开启，保留既有 60 秒兜底。

第二轮 90 秒生产无发信对照（UTC 11:12:16–11:13:47）也通过：三家分类提示均为零，开始、中点和结束时观察器均为 `watching`，三家生产 Reader 的采集水位均推进。结束时均无当前同步错误；Gmail 保留上述历史覆盖警告。

## 2026-09-28 浏览器资源避让

Reader 请求现在先等待 Controller 资源最多 2 秒；Gateway 提前返回的 `browser_busy` 也在同一有界等待中重试，仅重试明确的资源占用。仍被占用时，以 `email_browser_busy` 释放任务租约，持久安排 2 秒后续跑；不记录提供商／列表故障，不改变登录健康状态，不消耗失败次数，不结算通知修订号，也不结束本轮。冻结区间和末检意图在避让及 Store 重启后保留；Begin 命令身份包含执行租约，反复领取同一冻结检查点时不会与此前提出的查询上界产生幂等冲突；避让期间的新通知继续待处理，恢复后完成末轮复查。

这修正了此前将 `browser_busy` 映射为 `email_provider_unavailable`、从而进入一分钟提供商错误等待的行为。真正的脚本超时与提供商故障仍按原规则处理；发信结果未知或传输异常不自动重放。回归覆盖取消、有界占用、避让期间来通知、末检时占用，以及超过常规重试次数的连续避让，Memory／File／隔离 PostgreSQL 均验证。
