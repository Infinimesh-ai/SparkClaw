# 邮件发信耗时：常驻页面传输与登录检查复用

> 语言：简体中文 | [English](../../docs/email-send-latency.md)

- 状态：**已随 35 版于 2026-10-22 部署；常驻页面传输暴露 QQ 页面就绪竞态后已在 work2 关闭，登录检查复用实机未生效（见[第 9.10 节](email-network-native-architecture.md)）**
- 日期：2026-10-22
- 基线：SparkClaw `0560d221`，App-CLI `e147d45`（34 版），Tampermonkey 5.5.0.17
- 背景：[网络原生邮件架构](email-network-native-architecture.md) 第 9.4–9.6 节

## 1. 现在的时间花在哪里

work2 上一封日常发信（34 版，DOM 路径）的 API 耗时约 83 秒。其中两项与服务商
页面无关：

| 开销 | 位置 | work2 规模 |
|---|---|---|
| 每条页面命令单独启动一个 `playwright-cli.js` 进程 | `tools/browser-controller/src/host-page.mjs` | 每封信 70–85 条命令，每次进程启动约 0.42 秒 |
| 每次发信前一次完整登录检查 | Gateway `emailautomation.Controller.Admit` | 执行器 `probe` 任务 1.85–2.0 秒（API 层约 2.7 秒） |

App-CLI 的每个页面调用（`click`、`fill`、`waitFor` 等）在 HostPage 中都带有
`tab-list` 拓扑检查，效果操作前后还会读取 `location.href`，过去每条命令都是一个
独立进程。

## 2. 改动

### 2.1 常驻页面传输（Browser Controller）

`cli-page-transport.mjs` 每个任务页一个进程。它以与 `cli-read-batch.mjs`
完全相同的方式证明 daemon 归属（metadata、`/proc/<pid>/cmdline`、固定的
Playwright 版本、registry 条目、socket 属主），然后通过 stdin/stdout 服务请求：

- `{"args": [...]}` 执行一条 CLI 命令，解析器、选项与参数校验、提示文字和退出码
  都与 `playwright-cli.js` 一致，返回该进程本应产生的退出码、stdout 与 stderr；
- `{"batch": {...}}` 执行现有的受控读批处理。

HostPage 的控制流不变：所有守卫、重试、效果标记和错误映射仍在 HostPage 内，每条
命令的结果仍经过与独立进程相同的 `settleProcessOutput`（禁止输出、退出分类、
输出转换）。只有进程边界发生变化。

| 属性 | 行为 |
|---|---|
| 范围 | 仅 Linux、固定的 CLI 入口、无 Electron 适配器；默认只用于活动类型为 `mutation` 的页面（发信、标记已读）。`SPARKCLAW_BROWSER_PAGE_TRANSPORT=all` 扩展到 Reader/watch 页，`off` 恢复逐命令进程。 |
| 生命周期命令 | `attach` 与 `close` 仍各自使用独立进程。 |
| 顺序 | 同一时间只处理一个请求；并发的命令改用自己的独立进程。 |
| 超时 / 中止 | 由父进程结束辅助进程（`browser_script_timeout` / `browser_extension_unavailable`，与被结束的 CLI 进程一致）。 |
| 命令已写出后失败 | 结果终局，绝不重试；丢弃该辅助进程，下一条命令启动新的。 |
| 尚未写出任何命令前失败 | 启动或归属证明失败：该命令改走原有独立进程，本页面不再使用辅助进程（`application_page_transport_unavailable`）。 |
| 空闲 | 30 秒无请求后辅助进程退出，需要时再启动。 |
| 计数 | 页面关闭时 Controller 记录 `application_page_processes`，包含 `cli_processes`、`transport_processes`、`transport_commands`、`transport_fallbacks` 和 `lifetime_ms`。 |

发信生命周期语义不变：效果标记（`beforeEffect`、`effectAttempted`）仍在效果命令
写出之前设置。标记之前的失败仍表示“未发出”，标记处或之后的失败仍表示“可能已发出”。
App-CLI 的 dispatch release、Sent 确认和 unknown 判定均未改动。

### 2.2 登录检查复用（Gateway）

`Admit` 复用本 Gateway 在时间窗口内完成的完整登录检查结果。窗口由
`adapters.browserAutomation.playwrightExtension.mailSendLoginReuseSeconds` 或
`SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS` 配置（默认 120，范围 0–600，0 表示每次
都检查）。只有 `Admit` 或 `Check` 执行并已持久化为 ready 的检查才能作为复用来源；
窗口从该次检查开始计算，复用不会延长窗口。

出现以下任一情况时丢弃缓存，下一次发信重新完整检查：

- 设置变化（版本、账号、启用、状态、错误码），包括打开登录页；
- 探测脚本版本变化；
- 浏览器凭据代次变化；
- 该服务商邮箱的观察器状态：新的注册或 watch epoch、因文档替换或注册而 resync、
  watch 状态变化、邮箱错误、状态无法读取；报告 `login_required` 时一律不复用；
- 登录检查失败（Check、Admit 或收信），或发信未以 `sent` 结束（包括未派发和结果不明）。

发信脚本在任何效果前仍会在页面上证明确切账号；`SendForOwner` 仍会复核设置版本和
凭据代次。

## 3. 预期节省（估算，非测量）

| 项目 | 之前 | 之后 | 每封信节省 |
|---|---|---|---|
| 每个发信页的 CLI 进程 | 70–85 条页面命令 + attach + close | 1 个辅助进程 + attach + close | work2 约 29–35 秒（0.42 秒 × 命令数） |
| 本地夹具：68 条命令的发信形态页面（Linux 容器） | 11.4 秒，68 个进程 | 0.33 秒，1 个进程 | 该机器上 11 秒 |
| 登录检查 | 每封信一次 | 每个 120 秒窗口内的第一封 | 后续发信节省 1.9–2.7 秒 |

DOM 发信（API 83.8 秒）预计降到约 45–55 秒；原生重放（23.9 秒）按其页面命令占比
减少，估计 5–10 秒。服务商页面等待、导航和证明轮询不变。上述数字须以下面的测量替换。

## 4. work2 前后对比测量方法

由部署负责人在配对发行后执行。不需要改代码、调试端口或额外监听器；所有数据源只读。

1. **配置。** 记录部署的源码与版本。确认 Controller 单元未设置
   `SPARKCLAW_BROWSER_PAGE_TRANSPORT`（或设为 `mutation`），并记录 Gateway 实际生效的
   `mailSendLoginReuseSeconds`。
2. **历史基线（之前）。** 不发送任何邮件，以只读方式（`?mode=ro`）读取执行器账本
   `~/.local/share/sparkclaw/app-cli/state/executor/ledger.sqlite`，列出 `send` 与
   `probe` 任务的 `command`、`status`、`created_ms`、`finished_ms`。2026-10-22 快照
   （33/34 版）：DOM 发信 61.6–80.4 秒，原生重放 21.0 秒，每次发信前约 3 秒内都有一次
   1.85–2.0 秒的 `probe`。34 版验收的 API 耗时：83.823 秒（DOM）、23.874 秒（重放）。
3. **经批准的测试发信（之后）。** 经 owner 批准，只发送简短的新邮件（绝不重发历史
   unknown），每个服务商：两封间隔小于 120 秒，窗口过后再发一封。在客户端从 HTTP
   请求开始计时到响应，与 34 版验收方式一致。
4. **进程数。** 读取
   `journalctl --user -u sparkclaw-browser-controller.service --since <开始时间> -o cat`，
   筛选 `application_page_processes`。发信页应为 `transport_processes` 1、
   `cli_processes` 2（attach 与 close）、`transport_fallbacks` 0，`transport_commands`
   接近 70–85。确认没有 `application_page_transport_unavailable`。保温的 QQ 变更页
   只在最终关闭时报告一次，数值为其全部发信的累计。
5. **登录复用。** 账本中，第 3 步第一封发信之前紧挨一次 `probe`；第二封之前没有；
   窗口过后的发信之前再次出现。
6. **安全核对。** 每封测试信都以 `sent` 结束并有正常证明；执行器 unknown 行数不变；
   没有任何邮件被发送两次。
7. **对比。** 按服务商与路径（DOM、原生重放）报告前后的 API 耗时、账本 `send` 时长和
   是否有 `probe`。
8. **无需改代码的回退。** Controller 设置 `SPARKCLAW_BROWSER_PAGE_TRANSPORT=off`、
   Gateway 设置 `SPARKCLAW_MAIL_SEND_LOGIN_REUSE_SECONDS=0`，重启后恢复原行为。

## 5. 验证

- Controller（Linux 容器，Node 26）：对固定 CLI Session 协议夹具做发信形态页面 A/B；
  daemon 命令日志、结果和效果标记完全一致；1 个辅助进程替代 68 个进程；八个失败位置
  （效果前/效果处/效果后、上下文销毁、禁止输出、语法错误、页面已关闭）使两条路径在
  同一调用以相同错误码、原因和命令停止；超时/中止绝不重放命令；归属证明失败时在
  发送前回退；读批处理共用同一辅助进程。
- Gateway：连续发信共用一次检查；窗口不滑动；上述每个失效信号都强制完整检查；
  失败的检查绝不缓存；`Check` 可作为复用来源。
- 未进行任何实机发信、部署或发行版本变更。
