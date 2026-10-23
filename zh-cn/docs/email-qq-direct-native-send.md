# QQ 邮箱直接原生发送：维护模板与轻量发送流程

> 语言：简体中文 | [English](../../docs/email-qq-direct-native-send.md)

- 状态：**已在本地实现，尚未发布**（2026-10-23）。设计为第二版（按负责人十条意见修订）。实现：App-CLI 分支 `claude/mail-qq-direct-send`（`322fd94`，文档 `b6f334d`），SparkClaw 分支 `claude/mail-qq-direct-send`（Browser Controller `a5135b58`、Gateway `89a30aa6`），见第 17 节。没有发布、没有部署，也没有发送邮件。
- 基线：App-CLI `d8b2a01`（35 版）、SparkClaw `a78433c3`、Tampermonkey 5.5.0.18
- 背景：[网络原生邮件架构](email-network-native-architecture.md) 第 9.3–9.6、9.10 节；[邮件发信耗时](email-send-latency.md)
- 范围：以 QQ 邮箱的新撰写发送作为**锚点**，先在 QQ 上完整实现这一模式，再推广到 Gmail 和 Outlook（第 15 节）。本文的第一个实施版本只改变 QQ 新撰写发送的行为；Gmail、Outlook、回复、附件和标记已读在各自完成推广前保持现状。改动涉及 **App-CLI、Gateway 和 Browser Controller**，其中通用部分要按与提供方无关的接口实现。

## 1. 目标

有四个前提：浏览器已登录，发件账号明确，邮件信息完整，请求模板有效。满足时，由已登录页面里的浏览器 `fetch` **提交一次** QQ 网页版自己的 `/send/sendmail` 请求，并根据 QQ 的响应判断结果。正常路径不做下面这些事：

- 发送前单独做登录探测；
- 预先读取“已发送”列表；
- 设置没有依据的固定等待；
- 逐封重新初始化页面或脚本；
- 发送成功后再花几秒整理页面。

请求模板不再从一次 DOM 发信中学习，改为在代码里维护、带版本号的模板 `qq_sendmail_v1`（第 3、4 节）。邮件内容不再出现在任何进程的命令行参数里（第 5 节）。

## 2. 35 版现状

### 2.1 当前热页原生发送的步骤

| # | 步骤 | 位置 | 代价（work2，35 版，页面传输关闭） |
|---|---|---|---|
| 1 | Gateway `Admit` 每封邮件前跑一次完整 `probe` 任务（120 秒复用窗口实机未生效） | `emailautomation/controller.go`、`send_admission_cache.go` | 执行器 1.85–2.0 秒，API 约 2.7 秒 |
| 2 | `setSecrets` 写入收件人、主题和正文的私有槽 | `handlers.mjs` `prepare()` | 1 次页面调用 |
| 3 | `prepareBackgroundPage` | 同上 | 1 次页面调用 |
| 4 | `prepareMailRound`：校验文档 nonce，执行 `resetRound` | 同上 | 1 次页面调用 |
| 5 | `prepareNativeSend`：校验模板，读取“已发送”基线 | `managed-send.mjs`、`qq-send-prototype.mjs` | 1 次页面调用 + 1 次网络读取 |
| 6 | 释放 dispatch，写入 `dispatching` 日志 | 执行器、`send-journal.mjs` | 锚点 CAS + fsync |
| 7 | `sendNative`：`fetch` 发送，**固定等待 1 秒**，再每 750 毫秒轮询“已发送”，最长 12 秒 | `qq-send-prototype.mjs` | 请求 518–566 毫秒；Sent 证明 1.7–4.6 秒 |
| 8 | 写入 `sent` 日志 | `send-journal.mjs` | fsync |
| 9 | `parkSendTemplate`：`prepareMailRound`、清理 DOM 的 eval、`setSecrets({})` | `mail-page.mjs` | 3 次页面调用 |

[第 9.10 节](email-network-native-architecture.md) 的实测：原生发送 API 18.6–19.1 秒，其中执行器 6.4–7.0 秒。页面传输关闭时，每次页面调用都会启动 `playwright-cli.js` 进程，单次进程启动约 0.42 秒。每次 `runReadCode` 前后还各有一次 `tab-list` 拓扑检查进程。

### 2.2 模板生成路径的问题

现在模板只能这样得到：先完成一次约 80 秒的 DOM 发信，在学习窗口里观察 QQ 自己的 `func=1` 请求，再用独立的“已发送”读取确认。学到的模板只在这个页面的内存里存 15 分钟。页面重载、文档替换、模板过期、账号变化或一次未证实的重放，都会让下一封邮件退回 80 秒的 DOM 教学发信。

### 2.3 邮件内容经命令行参数暴露

发送页不是只读页面。`HostPage.runReadCode` 会执行 `playwright-cli run-code <代码>`（`tools/browser-controller/src/host-page.mjs`），原生路径又把收件人、主题和正文以 JSON 形式拼进这段代码，所以邮件内容出现在子进程的命令行参数里。work2 的 `/proc` 没有启用 `hidepid`，本机任何进程都能读到这些参数。此外，页面代码有 64 KiB 上限，35 版因此把原生发送的邮件内容限制在约 48 KB 以内。

## 3. 模板可行性核对

### 3.1 证据来源

work2 生产浏览器 HTTP 缓存的 SQL 索引里，记录着 QQ 网页版加载过的脚本地址。我只读扫描了这份索引，然后在 Mac 上不带任何 Cookie，从公开 CDN `res.wx.qq.com/t/webmail/webmail/res/js/` 下载了同名静态脚本。过程中没有接触在线页面，没有打开调试端口，也没有发信。

| 脚本 | Last-Modified |
|---|---|
| `home-web.d0d4cbddfb2d7d06.js` | 2026-09-22 |
| `home-web.18ed04099703ed2d.js` | 2026-09-24 |
| `home-web.e91d36e6686b314c.js` | 2026-10-22（34/35 版实测期间加载的版本） |
| `home-web.2a8c6bb7ea4212ee.js` | 2026-10-23（当前） |
| `chunk/XmailEditor_14e7.73850030e0dda947.js`、`…dd7a932fbf4c0fd5.js` | 2026-09-22、2026-10-22（编辑器，见 4.2） |

### 3.2 QQ 自己如何构造 `/send/sendmail`

发送函数 `sendMail` 调用 `post({cgi:"/send/sendmail", params})`。参数由 `k(e)` 构造，取值为 `null` 或 `undefined` 的键会被删掉。

| 字段 | QQ 源码取值 | 普通新撰写时 |
|---|---|---|
| `func` | `e.type` | `1`（发送；`2` 草稿；`4` 修改定时） |
| `mailid` | `e.mailId` | `''`（34/35 版重放已被 QQ 接受） |
| `time_zone` | `-new Date(2009,0,1).getTimezoneOffset()/60*3600` | 在页面内计算 |
| `time_zone_dst` | `new Date(2009,0,1).getTimezoneOffset()/60*3600-(new Date).getTimezoneOffset()/60*3600` | 在页面内计算 |
| `calendar` | `e.schedule` | `''` |
| `subject`、`content` | 主题、正文 HTML | 见 4.2 |
| `is_text`、`is_urgent`、`is_resume`、`need_receipt` | 对应布尔值 | `false`（是否改用 `is_text=true` 待验证，见 4.2） |
| `from` | `S(sender)`：有昵称时 `"昵称"<地址>`，否则裸地址 | 见 3.4 |
| `save_to_send` | `!isReadReceipt` | `true` |
| `separated_copy` | 群发单显 | `false` |
| `to`、`cc`、`bcc` | 每个地址经 `S()` 格式化 | 数组每个元素各占一个同名键 |
| `send_time`、`manual_save`、`re_mailid`、`fw_mailid`、`sent_mailid`、`is_receipt`、附件字段 | 普通撰写时为空 | 不发送 |

共享请求库 `post()` 统一加三个公共参数，并从 URL 中删掉同名参数：

- `language=window.lang`；
- `r=(1e13*Math.random()).toFixed(0)+Date.now()`；
- `sid=ee()`，取法见 4.3。

请求是 `POST`，`Content-Type: application/x-www-form-urlencoded`，带凭据，`responseType=json`，超时 30 秒。

只有 `head.ret==="0"` 才算成功。成功时 QQ 返回 `body.mailid` 和 `body.messageid`，`openJustSendMail` 直接用 `mailid` 打开刚发出的邮件。邮件模型的 `id` 来自列表行的 `emailid`，因此可以推断 `mailid` 与“已发送”行的 `emailid` 在同一个 ID 空间里（第 6.4 节规定了启用条件）。QQ 网页版错误码表中与发信相关的部分列在第 7.2 节。

### 3.3 稳定性

四个版本之间，下面三段代码逐字节相同：

- 参数构造段（1,689 个字符，SHA-256 前缀 `664a4ef1db37`）；
- 发件人格式化函数 `S()`（`4de39330d3d5`）；
- 请求库公共参数段（`23e939dfb7bd`）。

`sendMail` 在 9 月和 10 月版本之间只有压缩器生成的变量名不同。34/35 版三次实机重放的学习字段集合与上表完全一致，`mailid=''` 和逐行 `<div>` 的正文都被接受，三次都返回 `head.ret=0` 和 `messageid`，并各与一条独立的“已发送”行对应。

### 3.4 结论与剩余实机项

模板**可行**：每个字段要么是常量，要么按 QQ 自己的公式在页面内计算，要么从页面取得。仍需在首次合格实机发送时确认三项：

1. **`from` 的显示名。** v1 用裸地址，这是 QQ 在发件人没有昵称时自己会发出的合法形式。需要确认收件端看到的显示名与 DOM 发信一致；如果不一致，v1.1 改为读取页面上的发件昵称，并按 `encodeNick` 生成 `"昵称"<地址>`。
2. **`mailid` 是否等于“已发送”行的 `emailid`。** 结论影响回执，见第 6.4 节。
3. **正文显示。** 确认收件端看到的空白与 DOM 发信一致，见第 4.2 节。

## 4. 维护模板 `qq_sendmail_v1`

模板放在 App-CLI `applications/mail/userscripts/lib/` 下，由 Reader 打包，取代 `qq-send-prototype.mjs` 里的学习和确认逻辑。

```text
POST https://wx.mail.qq.com/send/sendmail        （URL 不带查询参数）
Content-Type: application/x-www-form-urlencoded
credentials: same-origin, redirect: error, cache: no-store

func=1  mailid=  calendar=  is_text=false  is_urgent=false  is_resume=false
need_receipt=false  save_to_send=true  separated_copy=false
time_zone=<页面计算>  time_zone_dst=<页面计算>
from=<已校验的账号地址>  to=<收件人>
subject=<主题>  content=<按 4.2 转换的正文>
language=<window.lang>  r=<QQ 公式>  sid=<按 4.3 取得>
```

### 4.1 版本、前置条件与范围

- **版本与维护。** 模板带 `template_version`。QQ 改动字段时，由开发者按第 3.1 节的方法更新模板，随配对版本发布；运行时不重新学习。回归测试固定字段集合和每条取值规则。
- **页面前置条件。** 全部满足才允许提交：`location.origin` 是 `https://wx.mail.qq.com`；路径是 `/home/index`；能按 4.3 取得 sid（不超过 4,096 字节）；`window.lang` 是短字符串；Reader 账号可读，并且等于发件账号。
- **漂移信号。** 记录页面的 `__WEB_MANIFEST__.version` 和 `home-web` 文件名，只用来对照 QQ 前端版本，不作为放行条件。
- **v1 范围。** 新撰写，1 个收件人，无抄送，无附件。正文上限取决于第 5 节的 Browser Controller 改动：落地后沿用现有的 200 KiB；未落地前，邮件内容（收件人、主题、正文的 JSON）限制在约 48 KB 以内。多收件人和抄送要单独合格一次后才开放。
- **退役项。** 删除学习窗口的 `capture_send_template`、`observe()` 对 `/send/sendmail` 的模板捕获、`confirm()`、15 分钟 TTL、消费后续期、`sendNativeReady` 和 `parkSendTemplate`。学习窗口本身保留，继续服务标记已读和形状证据。
- **DOM 路径。** 不在 v1 范围内的邮件（回复、抄送、附件）照常走 DOM 流程。v1 范围内的邮件**不**用 DOM 发信生成模板，模板失效时也**不**回退到 DOM 发信（第 7 节）。

### 4.2 正文：纯文本转 HTML

规则与 QQ 编辑器“以纯文本粘贴”时完全一致。它出自 `XmailEditor` 的 `gu()`，两个编辑器版本逐字节相同。转换按行进行，按 `\n` 切分，每一行依次处理：

1. 删除 `\r`；
2. 行首的一个空格换成 U+00A0（不换行空格）；
3. 行尾的一个空格换成 U+00A0；
4. 把每两个连续空格换成“空格 + U+00A0”（全局、不重叠），连续空格因此得以保留；
5. 制表符展开到下一个 6 列制表位，用 U+2002（EN SPACE）填充，列号按 UTF-16 长度从行首起算。

然后做 HTML 编码，与浏览器 `innerHTML` 序列化文本节点的结果一致：

- `&` → `&amp;`、`<` → `&lt;`、`>` → `&gt;`、U+00A0 → `&nbsp;`；
- U+2002 原样保留；
- 引号不转义。

每行写成 `<div>…</div>`，空行写成 `<div><br></div>`；这是 34/35 版实测已被接受的结构。

`is_text=true`（直接提交纯文本）列为**待验证项**：QQ 前端支持这个参数，但我们没有实测过它对收件端显示和“已发送”原件的影响。v1 不使用它。

### 4.3 sid 的取法

QQ 前端的 `ee()` 按下面的顺序取 sid：

1. 确定当前账号 uin：先看 `<head xmail_uin>`；其次在 Cookie `xm_muti_sid` 中查找 sid 与地址栏 sid 相同的条目，取它的 uin；最后看 Cookie `xm_uin`。
2. 在 Cookie `xm_muti_sid`（格式 `uin&sid|…`）中取这个 uin 对应的 sid；没有这个 Cookie 时，用 `xm_uin` + `xm_sid`。
3. 都取不到时，才用地址栏的 `sid`。

当 Cookie 里的 sid 与地址栏不一致时，QQ 用 `history.replaceState` 改写地址栏，并通过 `BroadcastChannel("updateUrlSid")` 通知其他标签页。

**我们的取法：** 在页面内按 QQ 的同一优先级读取，Cookie 优先，地址栏兜底。sid 只在本次请求体里使用：不返回给 Node，不写日志，不改地址栏，也不读其他 Cookie。两处 sid 不一致时以 Cookie 为准，这一情况只记录为漂移信号。

**取舍：** 我们因此多了对三个 Cookie 名和一个 `<head>` 属性的依赖，它们与模板一起维护，并用第 3.1 节的方法核对。换来的是：其他标签页刷新 sid、而本页地址栏尚未同步时，不会因为用了旧 sid 被 QQ 返回 `-20002`，进而被误判为登录失效。如果这些 Cookie 将来变成 HttpOnly、页面脚本读不到，就退回地址栏 sid，并接受这种误判的风险。Reader 现有的列表读取仍用地址栏 sid，本方案不改。

## 5. 内容传递：Browser Controller 私有请求文件

**原则：** 原生发送不得把收件人、主题、正文嵌进任何进程的命令行参数，并且一次发送中邮件内容只传一次。

- **沿用只读页面的做法。** 只读页面的 `runReadCode` 已经把代码写进 `read-batch-<uuid>.json`（`wx` 打开，权限 `0600`，放在任务状态目录下）。命令行参数里只有 `--batch-request <路径>`，由 `cli-read-batch.mjs` 在一个进程内完成三步：拓扑检查、`run-code`、复检。文件在 `finally` 中删除。
- **新增：受保护代码请求（变更页面用）。** Browser Controller 为声明了变更活动的页面新增同类请求，请求文件里有两个字段：
  - `code`：固定的小段页面函数，不含邮件内容，仍受 72 KiB 限制；
  - `argument`：邮件内容 JSON，上限 512 KiB。

  `argument` 只在 guarded 进程内与代码组合，然后经 daemon 的 Session 套接字交给浏览器，不经过任何命令行参数。不使用只读包装，但保留拓扑检查和来源检查。请求文件一次性使用，用完立即删除；页面启动时清理遗留的请求文件。
- **常驻传输开启时。** 同一请求经 `cli-page-transport.mjs` 的标准输入传递（`{"code_request":…}`），不写文件。传输不可用时退回私有请求文件，绝不退回命令行参数。
- **只传一次。** `preflight` 不携带邮件内容；邮件内容只随 `dispatch` 传一次（第 6 节）。
- **上限。** 这项改动落地后，原生发送不再受 64 KiB 页面代码上限约束，v1 沿用 200 KiB 正文上限；未落地前按第 4.1 节限制在约 48 KB 以内。
- **待核对。** 需要确认 Playwright daemon 不会把 `run-code` 的代码文本写入日志或 trace；如果会，相应的日志开关要在任务页上关闭。

## 6. 正常发送流程

### 6.1 在页面内核对账号

- **Gateway：** QQ 发送脚本声明 `send_admission: in_page`，`Admit` 不再调用 `probe`，只核对已持久化的状态：
  - 设置为 `ready`，且没有错误码；
  - 设置版本和账号一致；
  - 浏览器凭据代次一致；
  - 邮箱观察器没有报告 `login_required` 或错误。

  任一项不满足，就照常做完整 probe。`SendForOwner` 仍会重查设置版本和凭据代次。
- **页面：** 一次页面调用 `preflight`（不含邮件内容）依次完成：
  1. 校验文档 nonce；
  2. 冷页面时，在调用内有界等待 Reader 和账号就绪，最长 5 秒；
  3. 比较登录账号与发件账号（不区分大小写）；
  4. 检查第 4.1 节的前置条件，并按 4.3 预取 sid 是否可得。
- **结果：**
  - 一致：返回一次性票据 `{token, nonce, account, expires_at}`，有效期不超过 60 秒，不含邮件内容。
  - 不一致：`email_account_identity_mismatch`。
  - 登录页可见：`email_login_required`。
  - 限定时间内读不到账号：`email_account_identity_unavailable`（“账号无法确定，请处理”）。

  以上失败都发生在释放 dispatch 之前，因此一定没有发出。

### 6.2 在本地组装请求

- App-CLI 在 Node 侧完成全部确定性校验：地址、主题、正文、大小和范围，规则与现有 `validateManagedSend` 相同。全部通过后才释放 dispatch。
- `dispatch` 调用通过第 5 节的私有请求传入票据和邮件内容。页面在局部变量里按第 4 节组装表单，包括正文转换、时区、`language`、`r` 和 sid，登录凭据由浏览器按同源规则携带。
- 已就绪的页面和 Reader 会复用：不逐封 `setSecrets`、不 `resetRound`、不读“已发送”基线、不整理 DOM。原生路径不往 DOM 写任何邮件内容。

### 6.3 直接提交一次

执行器释放 dispatch 并写入 `dispatching` 日志（含写入时刻），然后发起 `dispatch` 页面调用：

1. 页面先核对票据：token、nonce、账号和有效期。如果票据在释放之后才失效（极少见），不提交请求，返回 `refused_before_request`，按“发送结果待确认”处理，不新增证明种类，交由现有的结案入口确认未发送。
2. 执行一次 `fetch`，超时约 **25 秒**，小于页面调用的 30 秒预算。
3. 没有登录探测，不读“已发送”，没有固定等待，任何情况下都不自动重发。

### 6.4 根据响应判断结果

同时满足以下全部条件，才算 **QQ 已接受发送**：

1. HTTP 200，`response.url` 就是请求 URL（同源、无重定向）；
2. 响应是不超过 1 MiB 的 JSON，且 `head.ret === 0`；
3. `body.messageid` 和 `body.mailid` 都是合格的提供方 ID；
4. 响应返回后，页面账号仍是发件账号。

此时：

- **回执与 ID 启用：** 实机确认 `mailid` 等于“已发送”行的 `emailid` 之前，回执是 `{status:"sent"}`，**不带** `provider_message_id`，草稿显示现有的“等待已发送邮件原件核对”。`mailid`、`messageid`、`template_version` 和耗时只写进私有日志。确认后，通过发布配置开关启用：`provider_message_id = body.mailid`。
- **文案：** 沿用现有的“服务商已确认发送”，意思是 QQ 已接受发送，不表述为收件人已收到。界面不改。
- 只拿到 HTTP 200 不算成功。`ret=0` 但缺少 ID 时，按“发送结果待确认”处理（第 7 节）。

### 6.5 轻量结束

成功后只做三件事：写入 `sent` 日志（文件和目录各 fsync 一次）、清空页面票据、以停放状态归还页面（`retain:true`）。没有 `parkSendTemplate` 的三次页面调用，没有导航，也不等页面整理。私有请求文件在 guarded 进程内已经删除。

## 7. 特殊情况

### 7.1 分类与处理

| 情况 | 识别（只认明确信号） | 处理 |
|---|---|---|
| 登录明确失效 | 发送前：登录页可见。发送后：返回登录类错误码 `-5012`、`-20002`、`-20062`、`-20071`、`-21021` | 发送前：`email_login_required`，未发出。发送后：`not_sent`，`kind:"provider_rejected"`，原因为登录类。Gateway 把设置置为 `login_required`，界面用现有入口提示重新登录。不自动刷新 sid，也不重发。 |
| 账号无法确定 | 限定时间内读不到账号（页面未渲染完、DOM 变化等） | `email_account_identity_unavailable`，未发出，提示“账号无法确定，请处理”。不判为登录失效。 |
| 当前账号不一致 | `preflight` 比较不一致 | `email_account_identity_mismatch`，未发出，由用户确认或切换账号。 |
| 请求参数错误 | 发送前本地校验失败；发送后返回参数类错误码 `-20011`、`-21101`、`-5151`、`-21501`、`-21132` | 发送前：`email_send_invalid_input`，指出具体字段。发送后：`not_sent`/`provider_rejected`，附具体错误码。`-20011` 意味着模板可能漂移：报告 `template_drift`，修复脚本。 |
| 暂时拒绝 | `-21519`（草稿保存中）、`-20003`（操作太频繁） | `not_sent`/`provider_rejected`，原因为暂时拒绝，由用户稍后重新发送。 |
| 其他非零错误码 | 结构完整的 JSON，`head.ret` 为不在上述清单中的非零整数 | 一律按“发送结果待确认”处理（7.3）。 |
| 页面、脚本或模板失效 | Reader 缺失或版本不符、nonce 变化、前置条件不满足 | 进入显式恢复，此时还没有释放 dispatch：先把页面导航回邮箱一次，再执行一次 `preflight`。仍失败则返回 `email_native_send_unavailable`，附原因（`reader_unavailable`、`sid_missing`、`template_drift` 等）。**不回退到 DOM 发信，也不给用户提供改用页面发送的入口。** |
| 已提交但结果无法判断 | 网络错误、超时、非 200、非 JSON、`ret=0` 但缺 ID、响应后账号改变、其他非零码、`refused_before_request` | 标记“发送结果待确认”（现有 `unknown` 语义和文案），再单独做一次“已发送”查询（7.3）。 |

**v1 对任何错误码都不自动重试**，包括 `-21519`。

### 7.2 `provider_rejected` 的判定依据

只有明确列出的 12 个错误码才判为未发送：

- 登录类 5 个：`-5012`、`-20002`、`-20062`、`-20071`、`-21021`；
- 参数类 5 个：`-20011`、`-21101`、`-5151`、`-21501`、`-21132`；
- 暂时拒绝 2 个：`-21519`、`-20003`。

依据是 QQ 前端对 `ret≠0` 的处理：一律视为失败并保留撰写内容；遇到登录类码时，先刷新会话再重发。这说明服务端并未接受这封邮件。这个判断来自对前端行为的推断，所以只对上述清单生效，清单以外的非零码一律按“待确认”处理。

### 7.3 “发送结果待确认”后的“已发送”查询

- **时机：** `dispatch` 返回后，单独占用一次页面调用，在页面调用预算内有界轮询，最长 12 秒（实测 QQ 在接受后 1.5–4.6 秒列出）。这条路径只在结果待确认时才走，正常路径不走。
- **有 `messageid` 时：** 按 `messageid` 精确匹配，同时要求 `dirid=3`，主题、发件人和唯一收件人都相同。
- **没有 `messageid` 时：**
  - 以 QQ 服务器时间为准：用本次 `/list/maillist` 响应的 `Date` 头估计本地时钟与服务器的偏差；
  - 时间下限 = 写入 `dispatching` 的时刻（换算成服务器时间）减去允许的时钟偏差（暂定 120 秒）；
  - 只考虑 `fromtime` 不早于下限的行，再按主题、发件人和唯一收件人匹配。
- **结果：** 只有唯一一条匹配才转为 `sent`，证据为 `qq_sent_list_v1`。零条或多条都保持待确认，永不自动重发；是否重发由用户核对后，通过现有的“核对发送结果”和结案入口决定。

## 8. 执行器影响评审：发出后返回 `not_sent`

- **释放之后的状态。** `releaseDispatch()` 之后，账本行带有 `effect=1`，dispatch 处于 `released`。此后处理器只要抛错，或者返回 `blocked`/`waiting_confirmation`，执行器都会判为 `uncertain`（`executor.mjs` 的 `finish` 分支）。因此 `provider_rejected` 必须以 `status:"completed"` 返回，数据为 `{status:"not_sent", not_sent:{kind:"provider_rejected",…}}`。
- **dispatch 阶段。** `completed` 的外部写入会被写成 `dispatch:"sent"`。这个值的含义是“已释放的 dispatch 已有终局业务结果”，不等于“邮件已发出”。账本 schema 不用改，但需要审查所有读取 `dispatch` 的地方（生命周期 lookup、锚点、GC、诊断），确认没有把它当成发信结果；业务结果以 `data` 为准。
- **输出校验。** `spec.output` 校验 `data`，所以输出 schema 的 `not_sent.kind` 要允许 `provider_rejected`，原因限定为第 7.2 节的清单。`not_sent` 继续禁止带 `provider_message_id`。证明需要的 `executionEvidence`（task_id、各 digest、ledger_epoch）在普通模式下也能拿到。
- **ACK。** Gateway 现在只在 `output.Status=="sent"` 时设置 `SendResult.Lifecycle`（`playwright_runner.go`），也只有这时才 ACK。`not_sent` 的终局结果如果不 ACK，会一直留在执行器，直到保留期结束。所以要扩展为对 `not_sent` 同样设置并 ACK。
- **日志与对账。** 日志允许 `dispatching → not_sent` 的原子替换。之后的对账（reconcile）直接返回已保存的 `not_sent` 回执，现有逻辑已经支持。
- **收尾。** 登录类拒绝返回 `retain:false` 并让页面失效，下一封从冷页面开始；其他拒绝照常停放页面。`reclaim` 删除日志的逻辑不变。
- **崩溃。** 如果在拿到响应之后、写入日志之前崩溃，行仍是 `uncertain`，日志停在 `dispatching`，按“待确认”处理，不重发，与现在相同。
- **纪元。** epoch 的排空和封存不受影响。

## 9. 与既有发件优化方案的关系

| 既有方案 | 现状 | 在新流程中 |
|---|---|---|
| 常驻页面传输 `cli-page-transport.mjs`（[发信耗时](email-send-latency.md) 2.1） | 35 版已部署，work2 已关闭 | **可用，推荐作为加速。** 请求走标准输入，不进命令行；一个 helper 同时承担拓扑检查和 `run-code`，每次页面调用不再起进程。当初关闭它，是因为 QQ **DOM** 发信在“已发送”目录渲染之前就点击了它（`sent_baseline` 竞态）；新原生路径没有 DOM 点击，不受影响。但同一个 QQ 发送页仍要承担 v1 范围外的 DOM 发信，所以重新开启的前提是先修复 DOM 流程的页面就绪（点击前等待目录出现）。在那之前，原生路径用私有请求文件也能工作。 |
| 只读批次 `cli-read-batch.mjs` | 已部署 | **直接复用**，作为第 5 节受保护代码请求的基础：在一个进程内完成拓扑检查、`run-code` 和复检，请求放在 0600 文件里。需要新增非只读变体和独立的 `argument` 字段。 |
| Gateway 登录检查复用（120 秒） | 已部署，实机未生效 | 对 QQ 发送**不再需要**，由页内核对取代。Gmail 和 Outlook 在推广到页内核对之前暂时保留，三家都切换后退役。未生效的原因仍未查明，但不在新路径上。 |
| 变更通道（[架构](email-network-native-architecture.md) 9.4） | 已部署 | **保留**，是前提：发送不阻塞收信。 |
| 发送页热复用（`reuse_idle_ms` 15 分钟） | 已部署 | **保留**。冷页面的代价已经降低，是否延长空闲时间按实测再定。 |
| 把一次发送的受保护步骤合并进一个进程（9.4 后续） | 未单独实施 | 已被常驻传输加受保护代码请求覆盖，**不再单独做**。 |

## 10. 预期效果（估算，需实测替换）

| 项目 | 35 版热页原生 | 新流程 |
|---|---|---|
| 发送前 probe | 每封 1 次（约 2.7 秒） | 0 次（状态有疑点时才做） |
| 页面调用 | 约 8 次，每次前后另有拓扑检查进程 | 2 次（`preflight`、`dispatch`）；只有待确认时才多 1 次查询 |
| “已发送”读取 | 基线 1 次加轮询 | 0 次（只在待确认时） |
| 固定等待 | 1 秒加轮询间隔 | 0 |
| 冷页面首封 | DOM 教学，约 80 秒 | 导航加 2 次页面调用 |

- **执行器约 2–3 秒，前提是：** 每次页面调用只起一个 guarded 进程，拓扑检查在这个进程内完成（第 5 节）；或者开启常驻传输。work2 上每次页面调用约 0.5–1 秒，请求本身约 0.5 秒。如果仍按现在的逐命令方式运行，每次页面调用前后还要各起一个 `tab-list` 拓扑检查进程，执行器会再多约 1–2 秒。
- **API 耗时：** 35 版热页 API 18.6 秒，其中执行器 6.4 秒；执行器之外约 12 秒，属于 Gateway 准备、审批绑定、调度、日志与确认，其中包含约 2.7 秒的 probe。本方案只去掉 probe，这约 12 秒中其余的开销**不在本方案范围内**。按这样拆分，热页面 API 预计约 10 秒（粗算区间 10–12 秒）。
- 这些都是估算，不是结果，必须用实测数字替换。

## 11. 组件变更

下表中准入声明、受保护代码请求、`provider_rejected` 证明、ACK 和日志转换都是通用接口，QQ 只是第一个使用者；模板、拒绝码清单和正文规则放在 QQ 专用模块里（第 15 节）。

| 组件 | 变更 |
|---|---|
| App-CLI Reader（维护模板模块） | 删除学习、确认、TTL 和基线逻辑。新增 `preflight`、`dispatch` 和待确认后的查询，实现第 4.2 节正文规则和第 4.3 节 sid 取法，记录漂移信号。 |
| App-CLI `managed-send.mjs`、`handlers.mjs`、`mail-page.mjs` | 原生分支只做 2 次页面调用，邮件内容只随 `dispatch` 传一次。v1 范围内跳过 `setSecrets`、`prepareMailRound` 和 `parkSendTemplate`。冷页面也走原生分支。模板失效时显式报告，不回退 DOM。用发布配置开关取代 `APP_CLI_MAIL_NATIVE_SEND`，`mailid` 回执另设开关。 |
| App-CLI `send-journal.mjs`、输出 schema、`send-recovery.mjs` | 日志支持 `dispatching → not_sent(provider_rejected)`；新增证明种类 `provider_rejected`，原因限定为固定清单。 |
| Browser Controller `host-page.mjs`、`cli-read-batch.mjs`、`cli-page-transport.mjs` | 为变更页面新增受保护代码请求（`code` + `argument`、0600 一次性文件或传输的标准输入），页面启动时清理遗留文件。邮件内容不进入任何命令行参数。 |
| Gateway `app/email_send_proof.go`、`playwright_runner.go` | 普通模式接受 `not_sent` + `provider_rejected`，并且必须绑定原调用；对 `not_sent` 也设置 `Lifecycle` 并 ACK。 |
| Gateway `emailmanagement/compose.go` | `provider_rejected` 映射为草稿 `failed` 并附具体错误码；登录类原因同时把设置置为 `login_required`。 |
| Gateway `Admit` | 对声明 `send_admission: in_page` 的提供方跳过 probe。 |
| 界面 | 不改。 |

## 12. 不变的安全约束

- 每次发送最多一个 `func=1` 请求。结果不明时不重发，也不回退到其他路径；v1 对任何错误码都不自动重试。
- dispatch 释放和 `dispatching` 日志都在请求之前完成。在此之前的任何失败都证明邮件没有发出。
- 只在同源、经过账号校验的已登录页面里由浏览器发出请求。不用 IMAP、SMTP、OAuth，也不用任何客户端凭据。
- 邮件内容不进入命令行参数。sid、正文、收件人原文和原始响应都不记录；证据只保留哈希、错误码和耗时。
- 历史 `unknown` 发送一律不重发。

## 13. 实施与验证计划

1. **本地实现**（需负责人批准后才改代码）：完成第 11 节的变更，补回归测试。测试至少覆盖：
   - 字段集合和每条取值规则；
   - 时区（含夏令时时区）；
   - 正文转换：行首和行尾空格、连续空格、制表位、`&<>`、空行；
   - sid 的优先级与两处不一致的情况；
   - 成功条件的每条反例；
   - 12 个错误码清单与清单外的非零码；
   - 账号读不到与登录页可见的区分；
   - `refused_before_request`；
   - 待确认后查询的时间下限与唯一性；
   - 不回退 DOM；
   - 命令行参数中不出现邮件内容（检查 `/proc/<pid>/cmdline`）；
   - 请求文件在成功和失败时都被删除；
   - `not_sent` 的 ACK；
   - 免 probe 准入的每个失效信号。
2. **配对发布**：App-CLI、SparkClaw（含 Browser Controller）与新的 Tampermonkey 版本一起发布。
3. **实机合格**（每封都需负责人明确批准，只发新的简短测试邮件）：
   - **冷页面首封：** 确认免 probe、2 次页面调用、`ret=0`，命令行参数中没有邮件内容。用一次独立的“已发送”读取核对 `mailid == emailid`、`from` 显示名和正文空白。
   - **热页面第二封：** 记录 API、执行器和页面调用的耗时。
   - **页面重载后的第三封：** 确认不需要 DOM 教学。
   - **每一步：** 确认历史 `unknown` 数量不变，没有任何邮件发出两次。
4. **启用与记录：** `mailid` 核对通过后启用 `mailid` 回执开关；按实测结果更新本文、架构文档第 9 节和簇状态。

## 14. 跨项目影响

只涉及 SparkClaw（Gateway 和 Browser Controller）及其自有的 App-CLI 分支，不改 ISCP 或其他项目的接口。实施时在 InfiniCenter 决策 0049 的评审意见中登记。

## 15. 推广到 Gmail 与 Outlook

QQ 是这一模式的锚点，不是终点。实现时要把通用部分和 QQ 专用部分分开，避免把 QQ 的细节写死在通用路径里。

### 15.1 通用部分（三家共用，QQ 是第一个实现）

- **准入。** 页内核对账号，按提供方声明的 `send_admission: in_page` 跳过发送前 probe（6.1）。
- **内容传递。** 走私有请求文件或传输的标准输入，邮件内容只传一次，不进入命令行（第 5 节）。Browser Controller 的受保护代码请求对所有变更页面通用。
- **调用结构。** 两次页面调用（`preflight`、`dispatch`）和一次性票据；dispatch 先释放、日志先写。
- **结果判定。** 只按服务商响应判定：成功要有服务商自己的成功标志和有效邮件标识，只拿到 HTTP 200 不算；明确拒绝码按清单归为 `provider_rejected`，清单以外一律待确认（第 7 节）。
- **待确认处理。** 单独一次有界的“已发送”查询，结果必须唯一，永不自动重发。
- **执行器、ACK、日志与收尾**（第 8 节）。
- **失效处理。** 显式报告原因，不回退 DOM 发信。
- **模板管理。** 维护模板带版本号，并记录漂移信号；新的回执 ID 在实机核对前由开关关闭。

### 15.2 提供方专用部分（每家各自维护）

| 项目 | 每家要单独确定 |
|---|---|
| 维护模板 | 发送端点、字段、编码，以及会话参数（QQ 是 sid） |
| 账号来源 | 页面上可信的当前账号与发件账号 |
| 成功判定 | 成功标志和邮件标识字段 |
| 明确拒绝码 | 登录类、参数类和暂时拒绝的清单 |
| “已发送”查询 | 查询请求、匹配字段，以及服务器时间来源 |
| 正文转换 | 与该家编辑器一致的纯文本转 HTML 规则 |

`provider_rejected` 的原因清单和输出 schema 都按提供方分表，Gateway 的映射也按提供方配置。

### 15.3 每家推广前的可行性核对

做法与第 3 节相同：

1. 从该家公开的前端脚本中核对发送请求的构造、响应解读和错误码，证据只取公开静态资源和只读的缓存索引；
2. 与学习窗口记录的请求形状、已有 DOM 发信的实测结果对照；
3. 写出维护模板和明确拒绝码清单；
4. 各自完成实机合格，每封都需负责人批准。

已知的出发点：Outlook 的部分写操作走 Web Worker 和 MessagePort 通道，学习窗口已经能观察这条通道的形状（[架构](email-network-native-architecture.md) 9.3）；Gmail 和 Outlook 目前都用 DOM 流程发信，还没有发送请求模板的核对证据。核对之前，这两家不做可行性结论。

## 16. 未决问题

- `from` 是否需要带昵称（3.4 第 1 项）。
- `is_text=true` 能否替代 HTML 正文（4.2）。
- 是否增加发布前的离线检查：用第 3.1 节的方法，比对公开 CDN 上新版 `home-web` 和 `XmailEditor` 的参数构造、正文规则与 sid 取法。
- 多收件人和抄送的开放节奏。
- 常驻页面传输的重新开启时机，取决于 DOM 发信页面就绪问题的修复（第 9 节）。
- Gmail 与 Outlook 的推广顺序，以及推广时是否把回复和附件一并纳入（第 15 节）。

## 17. 实现状态（2026-10-23，本地）

| 组件 | 实现方式 |
|---|---|
| 通用原生发送 | App-CLI `lib/native-send.mjs`：preflight（失败时导航恢复一次）、放行发送并写 `dispatching` 日志、一次 dispatch、按响应判定、一次有界查询；无 DOM 回退，不重发。 |
| 发布开关 | `lib/native-send-config.mjs`（`enabled`、`receipt_provider_message_id`）；`APP_CLI_MAIL_NATIVE_SEND` 退役。QQ 已启用，`mailid` 回执关闭。 |
| QQ 模块 | `lib/qq-native-send.mjs`（范围、恢复页面、12 个码）与 `userscripts/lib/qq-sendmail-v1.mjs`（字段、时区、`r`、正文规则、sid、查询）。删除 `qq-send-prototype.mjs`。 |
| 日志、输出 schema、恢复 | `dispatching → not_sent(provider_rejected)` 原子替换；`provider_rejected` 带类别 `reason` 和清单内的 `provider_code`。 |
| 处理器 | 原生发送跳过 `setSecrets`、后台页准备、`prepareMailRound` 和 `parkSendTemplate`；结果确定后页面停驻，登录类拒绝使页面失效。 |
| Browser Controller | `HostPage.runCodeRequest`（code ≤ 72 KiB，argument ≤ 512 KiB），经常驻传输 stdin（`code_request`）或一次性 0600 的 `code-request-*.json` 文件，受保护进程先删除文件再执行；页面启动时清除残留文件。没有 Linux `/proc` 证明时拒绝。 |
| Gateway | 校验 `provider_rejected` 证明；正常模式接受并 ACK `not_sent`；草稿错误码 `email_send_rejected_login_required` / `_invalid_parameter` / `_temporarily` 与 `email_send_template_drift`；登录类拒绝把设置置为 `login_required`；`send_admission: in_page` 时发送前不再探测。 |
| 界面 | 不变。新的草稿错误码沿用现有的通用发送失败文字。 |

配套发布已准备、未部署（2026-10-23）：App-CLI 第 36 版（冻结提交 `da62e87`，并从该提交可复现地重新构建）、Tampermonkey 5.5.0.19，以及 SparkClaw 的版本固定、vendor 的 wheel 与运行时包、controller 依赖锁、`provider_scripts.json`（`send_admission: in_page`）和桌面端托管脚本预加载。装上 .36 运行时后，controller 测试、投影与托管脚本检查、配套发布资格检查和一次性安装均通过。配对时还把 `email_native_send_unavailable`（发生在任何放行之前，未发出）映射为服务商不可用，并删除了只有原型才会产生的 `email_network_sent_unqualified`。

尚未完成：部署、第 5 节中守护进程日志/trace 的核查，以及 13.3 的实机验收。preflight 阶段的登录或账号失败目前到达草稿时仍是 `email_send_not_dispatched`，因为运行时对未放行的发送统一这样报告；只有发送后的登录类拒绝或下一次探测才会把设置置为 `login_required`。

