# SparkClaw R3 Mac/Linux 隔离 LAN 实机验收

> 语言：简体中文 | [English](../../docs/macos-r3-dual-host-acceptance.md)

## Mac／Linux 前端与图标同步 — 2026-10-04

用户要求主分支合并后，Mac 已安装前端和图标与 Linux 保持同步。此前虽然打包同一前端源码，R3 Desktop 实际渲染的是简化版 LocalWorkbench，Linux 生产工作台则使用共享侧栏／欢迎页／输入区；Mac 打包还漏配图标，回退到了 `electron.icns`。

产品源码 `5e3814ff266bf0c583188bf86808aea17922a6ca` 已让 LocalWorkbench 复用生产工作台的侧栏、品牌标识、任务搜索、欢迎页、设置布局及输入区样式。邮件通过输入区入口访问，日程使用侧栏入口，浏览器授权置于浏览器面板。首页可直接输入，保存时才创建本机对话；保存失败保留草稿，重试不重复创建对话，也不自动提交执行。保留 R3 本机数据、显式提交、原请求对账及审批边界。

Mac 打包显式使用 Linux 同一 PNG，并在 afterPack 拒绝 Electron 默认图标。已安装 Mac ICNS 的 512×512 解码 RGBA 像素与 Linux 已安装图标完全一致；源 PNG SHA-256 为 `acfd3a6e0cad248aa55c9c6d4a5486008c449bf3b05f4f9c39e40b2144a27365`。重建应用已安装至 `/Applications/SparkClaw.app`，旧应用及安装包保留用于回退。原有安全登录自动恢复，真实 GUI 已显示统一品牌欢迎页／输入区／侧栏，并打开当前 active 设备的设置。

WebChat 194 项、Desktop 90 项通过；最后对“返回同一对话保留草稿”的调整后，LocalWorkbench 定向检查再次通过。原生 ARM64 打包、包审计和 DMG 校验通过；已安装的 33 个桌面源码文件、5 个前端文件与构建一致。Linux 生产 WebChat 镜像已以同一源码重建，readiness 通过；CSS／PNG／PCM 与 Mac 逐字节相同。JS 仅因桌面既有构建禁用浏览器 Token 回退而不同，扣除该处后完全相同，HTML 仅 JS 哈希文件名不同。这是共享界面表现并保留原认证边界，不将 R3 对话改回旧服务端存储。

替换 DMG SHA-256：`47fe2f8123d22f8d2a215652965d5704be6916812292b378ca7707e3dc7b71a4`；ZIP：`687b90f8fa22dff30c289a65e5f34b638cfdbc8f506316e4aa889967e2b8543b`。下文同名旧制品已被替换，当前清单见 `apps/desktop/dist/r3-mac-arm64-release.json` 及 `SHA256SUMS`；验证／回退证据见 Mac `.cache/r3-ui-sync-20261004/` 和 GB10 `/home/infinimesh/.local/share/sparkclaw/r3-ui-sync-20261004`。生产仍使用 HTTPS `18790`，未改后端协议或跨项目契约。

## 生产切换与 Mac 安装 — 2026-10-04

用户随后明确授权直接切换生产、停止独立实例并安装 Mac 包。GB10 生产入口现为 `https://192.168.20.252:18790`，保留部署 `e1fef71f-dbcb-4eac-95b6-6987646f3ade` 和 Owner `owner`。以当前 main 产品源码 `6fa64910`（应用代码与下述安装包源码相同）重建 Gateway/WebChat 镜像；本提交加入本次使用的可选 TLS Compose overlay 及启动集成。既有 PostgreSQL 数据、邮箱 Profile、凭据身份和外部账本均保留。

- 已从校验过的 ARM64 DMG 安装到 `/Applications/SparkClaw.app`；已安装 `app.asar` SHA-256 为 `e7ecff0881b0e229040e0505129386c58ff6253b796c20985e419317f0e048bc`，与构建产物一致。配置公开的生产 v2 连接描述后，用户亲自领取并输入独立的“Mac Production 20261004”凭据；真实 GUI 已进入 Device workspace，设置中确认当前设备 active。仍为同一内部未签名／未公证包。
- 生产入口和私有 Gateway 上游均验证 TLS，Host WSS 升级通过 Nginx。CA 私钥位于证书挂载目录之外；叶证书覆盖 LAN 地址、`127.0.0.1`、`gateway`，SHA-256 为 `ea41dfb21a9c70a0f4b924d5903ab480370283fcb8a5ad6f095d53ec5642dc3e`，有效期至 2027-10-04 05:57:53 UTC。GB10 公开连接描述位于 `/home/infinimesh/.local/share/sparkclaw/production-connection.json`。
- 五个本地模型服务以及 PostgreSQL／Gateway／sandbox／Gotenberg 健康。保留现有浏览器 Profile，通过受支持安装入口更新配套 App-CLI／Controller／Bridge，browser／controller／executor 用户服务均 active。系统自启单元已按当前仓库路径重装，并补充本机 Node 26 和用户 bus 环境；已 enabled，实际 systemd 启动结果为 `Result=success`、`active/exited`。这不等于操作系统重启验收。
- 部署 Python 回归 31 项、shell 语法和真实容器 `nginx -t` 通过。两台机器均验证生产 readiness；独立设备认证／安装绑定、Host WSS welcome、授权邮箱目录、一次真实本地模型执行及客户端持久保存／ACK（`delivered`）通过。测试请求 `aecc9f71-8d49-4bd2-84e9-39ef1a4e445c` 于上海时间 14:17 完成，临时测试设备均已撤销。此前两处夹具错误（遗漏空 JSON 请求体、选错 fetch helper）已修正，失败报告保留，均未提交模型任务。本轮没有新增发信。
- 已停止 `25543` 独立 Gateway 和两台 expiry-soak worker，数据及证据保留于 `/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi`；未结束的 soak 明确标为 `canceled_for_production_cutover`，不记为通过。完整 M01–M12 及真实 24 小时过期验收仍未完成。

切换前已将 PostgreSQL dump、私有运行配置归档和服务／容器元数据以私有权限保存在 `/home/infinimesh/.local/share/sparkclaw/r3-production-backup-20261004-XFxaGw`，构建／启动／测试日志与脱敏生产检查报告也保存在该目录。未迁移旧测试历史。可选 HTTPS 部署配置见[部署指南](deployment.md)。InfiniCenter 状态及 0031 评审记录本次用户授权的 SparkClaw 生产切换；0031 仍 proposed，既有 JingSi／App-CLI Schema 和留存义务保持不变。

## 主分支与 Mac 安装包交付 — 2026-10-04

阶段收尾后，用户要求合并主分支并重新编译 Mac 桌面端。`main` 从 `13755fbc4b3da0220dbb252de5651252681d996b` 快进合入全部 67 个 R3 提交，落点为 `5fe3ce8e540be9d46e0652651484ef15441296cf`；本记录为随后的纯文档提交。安装包准确源码为 `5fe3ce8e…`，包含当前工作台页面。前一阶段“不合并主分支”的范围是历史记录；完整 M01–M12 和生产切换仍未完成。

本次复跑通过：Mac Desktop 90、WebChat 193、凭据 14（设置 `TMPDIR=/private/tmp`）、Electron Node/SQLite 49、原生 R3 Host 和三进程 Mac 安全存储检查。同一 Git 源码快照在 GB10 新建隔离目录通过 Go build/vet/全量测试（57 个有测试的包）。首次在 Mac 执行后端测试因 Linux `/dev/shm` 和 canonical 路径要求失败，默认 Mac 临时目录也触发凭据路径拒绝；初始失败保留在本地日志，不记为 Mac 后端通过。

原生 ARM64 打包、受管资产、安装包白名单及 `hdiutil verify` 通过。包内 34 个源码文件、5 个 UI 文件与工作区逐字节一致。新包使用全新隔离 Profile，从正常产品入口启动（`qualification=false`），实际 GUI 显示凭据登录页并正常退出；用户已有 Profile 和运行后端未升级。本包仍为未签名、未公证的内部开发版。已核对 InfiniCenter 簇／inbox／契约并为 0031 追加评审，状态保持 proposed；不修改既有 JingSi／App-CLI 契约或代替对端接受。

| `apps/desktop/dist/` 下本次替换产物 | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `64543ba57cea7c773ab1f4c00060975ff13b585e97c36bfb2b1f8ca37d8ce9be` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `2b49b932dd543fbaf6635e937985ab4d6a753bd47235edb3a81468ac53e9b954` |

构建及检查日志保留在本地 `.cache/r3-main-release-20261004/`。下文旧安装包哈希只对应上次构建，不代表本次替换产物；本次交付未新增发信、凭据恢复、系统重启或生产切换。

## 前一阶段收尾

更新：2026-10-04，Asia/Shanghai；阶段收尾核对截至 13:20。下列实机执行证据取得于 2026-10-03～04。分支：`codex/sparkclaw-r3`。本记录接续[首轮 Mac 记录](macos-r3-acceptance.md)。用户授权当前 Mac 与 `infinimesh@192.168.20.252` 双端验收，并明确 Mac 仅用于内部测试。**本阶段已收尾；完整 M01–M12 尚未全部通过，后续操作与完成标准见文末。**

## 环境与准确版本

| 项目 | 实际版本／位置 |
|---|---|
| 交付基线 | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| 本轮最终客户端／安装包源码 | `86176211a7c51abb6ab8680f14e884fc06934430` |
| 当前隔离 Gateway 构建源码 | `ab8833dd9d4e418ef5a0d180d1afa8edb5f36e77`；后续修改仅涉及桌面代码，Go 产品源码相同 |
| Gateway 二进制 SHA-256 | `e879343ac14e1db4ccc71945f216e9ccc1bcfde7e4f4dfc0c375b56fb765e64b` |
| Mac | Apple M5／ARM64，macOS 26.6.2（25G83），内置 Retina 2560×1664，scale factor 2；Node 26.2.0／npm 11.13.0 |
| Linux | `gx10-7660`，ARM64 Linux 6.17.0-1032-nvidia；Go 1.25.5／Node 26.2.0；原生客户端使用私有 Xvfb 与该用户 GNOME Secret Service |
| 双端客户端运行时 | Electron 44.4.3／Chromium 152.0.7977.130；Electron Node 24.21.0 |
| 隔离 Linux 根目录 | `/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi` |
| Mac 测试 Profile | 仓库 `.cache/dual-host-acceptance/mac-profile` |
| Mac 内部安装位置 | 仓库 `.cache/dual-host-acceptance/installed/SparkClaw.app`，从本轮 DMG 实际挂载并复制安装 |
| HTTPS origin／deployment／owner | `https://192.168.20.252:25543`／`dual-host-r3-20261003-FZMMvi`／`owner` |
| 叶证书 SHA-256 | `1a2e2668378f11dcf44f0027a15f8318d55604d69ceb85f6fe432cca2d4b32cd` |

SSH 仅用于检查、源码传输与启动验收；客户端 HTTPS/WSS 直接走 LAN，没有 SSH 隧道。使用独立两天有效测试 CA，保留证书链、主机名及叶指纹验证。隔离 Gateway 使用 mock 模型，关闭外部 MCP／集成；合成邮箱采集已暂停。没有修复、重启或重新配置现有 Linux Gateway；初次检查其 HTTP `18790/healthz` 返回 502。

邮箱采集只在 GB10/Linux。Mac 只同步授权邮箱及保留本地缓存，不运行采集器。按用户授权直接复用已有专用浏览器三大邮箱 Profile，没有复制 Profile／邮箱凭据到 Mac，没有导入旧邮件／旧测试历史，没有操作生产业务数据、合并 main 或接入 InfiniCenter／等待 0031。

## 已执行的实机证据

- **双端邮箱契约：**独立 Client 与安装 UUID 经真实 Gateway 同步新建合成邮件及中文附件。HTTP 权威删除拒绝旧版本、同 command key 重试不重复删除，双端接收 tombstone；删除邮件保留另存的本地附件。过期 epoch 游标触发重置并重建完整缓存；非法游标返回 400，没有冒充恢复通过。
- **本地独立与保存：**双端新原生进程恢复各自对话、消息、request ID 与文件。Mac“最终实机验收”的“休眠断网后保留”始终为未显式提交。安装 UUID 为 `0715c02e-fb84-4b14-a9e4-92b8fabdd446`，文件 SHA-256 为 `242a3103061c81ed1e1e1f9c380d9324980b8a3e4129dd829af3b3bdc0255803`。Mac 本地标记和该对话 ID 不在 Linux 后端或 Linux 客户端非邮箱库中。此项尚不包含 macOS 系统重启。
- **实际合盖／断网／退出：**用户合盖后，系统记录 16:42:26→16:44:17 的 Clamshell Sleep，共 111 秒；唤醒后 GUI、本地消息和文件完整。Wi-Fi `en0` 实际断开 45.08 秒，后端 TCP 不可达期间文件摘要与未提交状态完整，恢复后 LAN 可达。GUI Cmd+Q 已确认进程完全退出，开发包升级／重新启动保留原本地数据。首轮断网后的 GUI 邮箱操作暴露安装绑定缺陷，修复后的完整 GUI 断网复验尚待执行。
- **同一适配层与真实产品工作流：**早期 `25544` 临时 facade 使用真实 Go Broker 与双端真实 WebContents 完成导航、输入、点击、读取、20 次 A/B 切换、同页释放再获取、迟到事件隔离、系统浏览器／独立弹窗拒绝与真实点击后响应丢失的 unknown 写围栏。该 facade 已停止。后续直接使用实际 Gateway 的普通工作流，两端都在独立 native Host 内导航并读取 `https://www.google.com`，mock 回答基于真实读取结果；不是 Linux 页面副本或后台页面替换。真实渲染器崩溃后，显式新任务创建新 WebContents／page ref；B 保持选中，旧引用和越对话显示被拒绝。
- **真实租约到期：**双端在实际 Gateway/WSS 与真实内嵌页中抑制两次客户端心跳并延迟已开始的读取结果，保留真实时钟。Mac 29.97 秒、Linux 30.05 秒后任务页关闭，旧引用不可用；迟到结果没有重开页面。每端仅显式提交一次。写结果丢失围栏另由上述真实点击案例验证，不把读取超时冒充写对账。
- **ACK 丢失及重启：**实际 Gateway 接受首个交付 ACK 后夹具丢弃响应。真实重启隔离 Gateway 与原生客户端后，只对账一次 ACK、零次重新执行，digest 相同、本地恰好两条消息。普通执行上下文标记不在后端持久状态／工作区／trace／日志中。
- **GUI 与凭据：**用户实际输入独立 Mac 凭据、打开设备设置、签发／撤销凭据。复制问题修复后，用户确认 Copy／本机粘贴成功。撤销后的设备保留带时间戳的 revoked 元数据属于预期。真实私有 TTY 初始化／遗失恢复只领取一次，重复领取不重新显示；Linux 实际旧身份撤销、新身份登录、原安装 UUID 与旧作用域数据保留，未迁移跨 Client 历史。Mac Keychain、Linux `gnome_libsecret` 原生重启恢复、错误指纹／部署／Owner／Token 拒绝、受保护事件流退出中断均已验证。
- **恢复后的实际 Mac 产品 GUI：**2026-10-04 用户在真实终端领取新凭据并直接解锁产品，设置页当前设备与服务端新 Client 一致、旧 Client 显示已撤销。13:11:17 GUI 同步完成并展开真实 Gmail→QQ 验收邮件，正文摘要与 Linux 相同。随后 Cmd+Q 完全退出并以实际安装包启动，Keychain 自动解锁，无需重新输入 Token；新作用域的“凭据恢复后保留”、文件及未提交请求与邮件缓存在 GUI 中仍可见。加密 vault 为 mode 600，退出前后摘要相同。此项是应用进程重启证据，macOS 系统重启尚未执行。
- **验证与包：**最终双端各 90 项桌面测试通过；Mac Electron Node／SQLite 内 50 项相关测试通过；最终 Mac 原生凭据三进程检查及双端原生 Host 检查通过。ARM64 DMG／ZIP 本机重新构建，afterPack 允许列表审计与 `hdiutil verify` 通过。实际安装包面对私有未来 Schema 5 数据库以退出码 1 拒绝启动，数据库与保存文件字节摘要未变。已知自动化测试 Token 不在 Git、ASAR、日志、后端内容或二进制中，人工 GUI Token 未解密用于扫描。

Mac 产品与 Linux 产品前端一致：实际构建的三个资源文件 SHA-256 逐个相同。主 JS `index-BC08-68r.js` 为 `31738392ed82e657f277b9397fb73245224d211c303a904a1c47f9576bf0157c`。这证明使用相同产品前端资源，不代表未执行的 Linux 物理 GUI 检查已通过。

## 真实邮箱及现有采集环境

用户只授权三封新邮件，各路线只尝试一次，主题前缀 `SCW-mail-R3-20261003-405d1b61`，无附件，限定已有三个 Profile 账户；没有重发。

| 路线 | 实际结果 |
|---|---|
| QQ→Outlook | `email_draft_verification_failed`；发送未确认，收件端限定新时间窗候选为 0；FAIL／不重放 |
| Outlook→Gmail | `email_page_contract_changed`；发送未确认，收件端限定新时间窗候选为 0；FAIL／不重放 |
| Gmail→QQ | 发送确认，QQ 新时间窗唯一候选；产品真实原件采集与 MIME 解析、Mac/Linux 原生同步通过；恢复登录后的 Mac 产品 GUI 也经 HTTPS 实际同步并展开正文，未复制凭据或缓存 |

Mac 完全退出后，GB10 再次采集了唯一新 QQ 邮件：`cap_072ff1b4c907243de56194bdded252ea`，原件摘要 `sha256:7eee34a1df4657c40ee06a497981329fc0f900445efe2372a545bca969a76e2d`。产品验证 receipt／manifest／原件后，经隔离类型化仓库 discovery/page-batch 租约发布并调用实际 MIME parser；没有用构造正文冒充真实邮件。隔离后端 Mail ID `d49178ad82a4f96227e580038f9c4a19814d64f292490c2b00725fd5446edaef`；双端原生缓存与恢复后的 Mac GUI 缓存正文 SHA-256 均为 `9de3b3bc68a6eef8026b871befc5381b35290f46b4eec11a999e29a464111c83`。这是一次实际采集操作的 Mac 退出独立性证据，不冒充长期定时采集验收。

现有专用浏览器 Controller 实际从 `/home/infinimesh/.local/share/sparkclaw/qualification/20260930/pre-extraction-baseline/tools/browser-controller/src/main.mjs` 运行，入口 SHA-256 `e211904e52c1940d207eac32ab0dedc460ea0988c65d03eb0e5becb426ff9b16`；没有当前 R3 的 `AppCLIClientFactory`／release 配置。上述真实站点失败按该运行版本记录，不能当作当前 R3 App-CLI 版本通过或失败的完整结论。没有升级或重启这项现有服务。默认 discover 是账户 bootstrap，候选 0 不等同空邮箱；收件结果来自后续明确限定的新时间窗。

## 本轮发现并已推送的源码修复

| SHA | 问题与修复 |
|---|---|
| `da851139a9879499be57949a02050d0f8b4ab131` | pinned HTTPS DELETE 缺少 UTF-8 字节长度，真实 Gateway 收到空正文；固定 framing 并验证权威删除 |
| `f7074ba5116d2724a3dc0781b49e142185f8380b` | 仅可信工作台主框架获准剪贴板写入；读取与其他来源仍拒绝，用户复验成功 |
| `c8f81144c6b0a3144522ccd579a5deb1aac4a87e` | 普通 GUI 更新邮箱调用已移除的 `executionClient.register`；统一使用 DesktopAuth 已完成的安装绑定，实际 GUI catalog／sync 恢复 |
| `ab8833dd9d4e418ef5a0d180d1afa8edb5f36e77` | ScopedAdapter 缺少旧 ToolHub 所需 result 字段，真实页面已导航却停止读取；补 result contract 与跨层真实工作流回归 |
| `0f24edc2610499ffa5b75ac24735f322b0076fdf` | 租约释放后丢失本地对话归属；保留持久归属并约束显示／导航／弹窗，双端实际崩溃恢复通过 |
| `86176211a7c51abb6ab8680f14e884fc06934430` | 临时安全存储解密失败不得销毁密文；保持锁定且不发受保护请求，后续正常进程可恢复 |

操作失误也如实记录：裸 Electron 夹具读取普通 SparkClaw Profile 时不能使用同一 Keychain 身份，旧逻辑清除了 GUI 保存的登录。没有读出 Token，也没有删除本地历史；上述最后一项修复已保护后续解密失败。2026-10-04 用户确认原 Token 已丢失。旧 Mac GUI Client `client_web_sigBFXKm2wzNAefA-Kz0wJCN` 于 13:04:19.696（Asia/Shanghai）经隔离管理 socket 首次撤销；用户随后执行真实 CLI 恢复，服务端最新撤销时间为 13:09:18.503。恢复 journal 为 `completed`，新 Client `client_web_tZJiI4cjW_StCCX9A6SSD3j6`／`Mac GUI Recovered Acceptance 20261004` 已激活，产品设置页核对一致。Token 仅由用户在真实终端领取并输入 GUI，代理没有获取或解密它。

本次遗失恢复、旧身份撤销、新身份 GUI 解锁、真实邮件同步以及 Cmd+Q 后 Keychain 自动恢复均已完成。原安装 UUID、本地对话、未提交请求与文件摘要仍完整；新 Client 使用新的本地作用域，原历史保持原作用域，不做跨 Client 迁移。完成的恢复命令不会再次显示 Token；后续若再遗失，必须针对当时的活跃 Client 发起新的恢复，不能重复本次旧 Client 的命令。

## M01–M12 当前状态

`PASS（限定范围）`仅认证列明的内部非生产场景。未执行不计通过；`N/A` 按用户本轮限制判断，并不等于正式发布验收通过。

| 用例 | 当前证据／状态 | 剩余项 |
|---|---|---|
| M01 | PASS（限定范围）：直接 LAN HTTPS/WSS、独立身份、完整证书验证及错误身份拒绝 | 生产证书／部署不在本轮 |
| M02 | PARTIAL：进程重启／升级保留本地测试内容，恢复后新身份的消息／文件／未提交请求及缓存也保留，Linux 无副本 | 实际 macOS 系统重启；新身份基线已置为 `ready_for_os_reboot` |
| M03 | PARTIAL：合成邮件双端删除／游标恢复通过；真实 Gmail→QQ 原件、GB10 独立采集、产品解析、双端原生同步与 Mac 产品 GUI 同步通过 | QQ／Outlook 两条站点路线失败；当前 R3 采集运行版本尚未实机联测 |
| M04 | PARTIAL：共享 Broker 双端输入／导航／读取，普通 Gateway 双端真实公开站点工作流通过 | 完整支持站点矩阵／当前 R3 GB10 采集与 Mac Host 角色联测 |
| M05 | PASS（限定范围）：真实 Host 受管页面拒绝系统浏览器／独立弹窗，未复制 Profile／用后台页替换 | 授权子页当前未受支持，不将能力缺口记为通过 |
| M06 | PARTIAL：A/B、同页再获取、租约释放后归属、实际崩溃新页及旧引用拒绝通过 | 授权子页的完整站点矩阵未执行 |
| M07 | PARTIAL：实际合盖／休眠、45 秒断网、本地保留、Cmd+Q、原生撤销／退出、实际 30 秒租约到期、写响应丢失围栏通过 | 修复后 GUI 断网恢复及 GUI 自我撤销／退出组合 |
| M08 | PARTIAL：真实受限 32 MiB HFS+ ENOSPC、ACK 丢失、后端／客户端重启不重发通过 | 真实 24 小时结果过期进行中；物理填满系统盘／断电不是原 M08 的额外必需条件 |
| M09 | PARTIAL：普通 GUI、用户中文 IME、内置 2× Retina、复制及原生权限检查通过 | 完整支持站点矩阵；外接屏 N/A（无设备），麦克风 N/A（本轮本地工作台未启用） |
| M10 | PASS（内部限定范围）：ARM64 本机包、审计、DMG 实际安装、应用升级保留、真实未来 Schema 启动拒绝 | Developer ID／公证／正式 Gatekeeper 分发 N/A（用户无证书，仅内部测试） |
| M11 | PARTIAL：普通 GUI 登录、遗失恢复后实际 Keychain 应用重启自动解锁、原生错误凭据／退出／撤销通道停止、本地保留 | 产品 GUI 自我撤销／退出完整矩阵、macOS 系统重启恢复 |
| M12 | PARTIAL：TTY 单次领取／遗失恢复、旧身份撤销、新 Mac 身份 GUI 解锁、GUI 单次签发／复制／撤销、应用重启／重连复用、包／日志扫描 | 实际 macOS 系统重启复用（与 M02／M11 合验）；生产上线不在本轮 |

## 24 小时检查与安装包

两端各一个结果以真实时间保留，至今每端仅提交 1 次、ACK 0 次；实际 Gateway 重启后保留相同 request ID、digest 与期限。未调整时钟或缩短 TTL。

- Linux 期限：2026-10-04 16:23:47.870（Asia/Shanghai）。
- Mac 期限：2026-10-04 16:24:33.787（Asia/Shanghai）。
- 夹具在期限后再等 65 秒检查 `delivery_expired` 且 result 不可读取；最早 16:25:39 可收齐两端结果。2026-10-04 13:20 实际核对两端均为 `waiting_real_24_hours`，最后状态 `completed`，每端提交 1 次、ACK 0 次，不是 PASS。

最终包源码为 `86176211a7c51abb6ab8680f14e884fc06934430`；后续记录提交不改变该构建源码。

| `apps/desktop/dist/` 本地文件 | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `5bcd1995dfa44b22d81d30c8039e4879e10f55585fe2c87532726828f03cf278` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `c1c78688c25192ef7cd4a085033b2c2c1996f1c067d0410fbb1392a2b71d677b` |

忽略的私有证据位于 `.cache/dual-host-acceptance/`：构建／原生测试／网络／页面崩溃／租约／真实邮件日志、安装包 manifest、夹具源及摘要、原件 receipt；临时自动化凭据仅在私有 mode-600 文件中，不进入 Git 或安装包。本次新增元数据为 `mac-gui-recovery-20261004.json`、`mac-gui-real-mail-sync.json`、`mac-gui-recovered-restart.json`，均不含 Token。`start-product.sh` 启动实际内部安装包。

恢复后已创建全新验收对话“凭据恢复实机验收 2026-10-04”，消息“凭据恢复后保留”，文件“凭据恢复验收文件.txt”（69 字节）。文件 SHA-256 为 `a591d627d1baf584985fcb148512ffcc28f77c4acc0d749207ef3ff9a4ca321f`，request `a8e84f1b-e080-4815-81b7-4c48e8d8a359` 保持未显式提交；新内容未出现在 Linux 后端或 Linux 客户端非邮箱库中。`recovered-manual-state-expected.json` 保存预期值；`reboot-baseline.json` 已于 13:15:13 记录活跃新身份、原安装 UUID、消息／文件／缓存及加密 vault 摘要，状态为 `ready_for_os_reboot`，基线 boot epoch 为 `1788266014`。系统重启及重启后核对脚本尚未执行。

`25544` facade 已关闭；隔离 `25543` Gateway、两个真实 24 小时检查进程及 Mac 测试 Profile 暂保留。没有新增开机服务或生产部署；测试证书两天后过期。

## 阶段收尾与后续工作

本阶段交付为本机 ARM64 内部安装包、已推送的 R3 源码修复及双语实际验收记录。当前实际 GUI 保持已解锁，显示恢复后的新验收对话和真实邮件缓存。按用户要求在此收尾，不在本阶段发起系统重启、扩大站点测试或重复发送邮件。后续先收齐现有证据，再完成以下缺口。

| 顺序／用例 | 下一步与前提 | 完成标准 |
|---|---|---|
| 1／M08 | 2026-10-04 16:25:39 后只读核对 GB10 两端 `*-expiry-soak.json` 与日志；保留现有 Gateway 和检查进程直至完成 | 两端 `phase=passed`、`passed=true`、`last_state=delivery_expired`，原 result 不可读取，原 request／digest／期限一致，提交 1 次、ACK 0 次；回填真实时间和结果，不重发／不调时钟 |
| 2／M02、M11、M12 | 用户选择方便的窗口实际重启 Mac；基线已经准备好。重启前如改变活跃身份或验收内容，先重新准备基线 | boot epoch 确实改变；脚本核对新／旧作用域保留、文件摘要、未提交请求、邮件缓存及加密 vault；实际 GUI 从 Keychain 自动解锁，Linux 无非邮箱副本 |
| 3／M07、M11 | 使用临时独立验收身份补实际 GUI 断网／恢复、退出登录／自我撤销；先让网络核对夹具同时覆盖恢复后的新作用域 | 断网约 45 秒时本地数据和缓存完整，恢复后连接正常；退出／撤销后受保护的执行、浏览器、邮箱及调度通道停止；迟到命令隔离、未知写操作对账，不自动重新执行 |
| 4／M03、M04、M09 | 在 GB10 准备独立、准确 SHA 的当前 R3 pinned App-CLI／采集运行环境，核对支持站点清单；先定位 QQ 草稿验证及 Outlook 页面契约问题。现有旧 Controller 不作版本证明 | 当前 R3 的 GB10 后端采集角色与 Mac/Linux 内嵌 Host 角色联测、完整支持站点 GUI／权限矩阵有逐项证据；采集继续只在 GB10。任何新邮件发送另需明确授权，本次三条路线不重放 |
| 5／M05、M06 | 授权子页目前未受支持；若纳入后续交付，先实现其任务／页面归属，再做站点验收 | 授权子页、A/B 切换、弹窗及崩溃重建保持归属，旧引用／迟到事件／跨对话显示被拒绝；实现前继续记录能力缺口 |
| 6／环境收尾 | 24 小时结果及需要保留的证据收齐后，再核对测试进程、Client 与私有目录并清理 | 仅停止独立根目录内的测试 Gateway／进程，撤销已确认不用的测试身份；保留证据／所需本地数据，生产及现有共享服务不受影响。测试证书约 2026-10-05 15:19 到期，继续 LAN 验收前检查有效期 |

外接显示器、Developer ID 签名／公证及正式 Gatekeeper 分发保持本轮 N/A；本地工作台未启用麦克风。它们不是当前内部测试阶段的待办通过项，未来若改变硬件或发布范围需重新验收。InfiniCenter／0031、main 合并、旧数据迁移与生产操作仍不在本轮。

### 读取现有 24 小时结果

在上述实际期限之后执行以下只读命令。若仍为 waiting 或报错，先检查私有日志和存活进程；不要重建请求、补 ACK 或重复启动发送流程。

```sh
ssh infinimesh@192.168.20.252 'python3 -' <<'PY'
import json, pathlib
root = pathlib.Path('/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi')
for side in ('mac', 'linux'):
    value = json.loads((root / f'{side}-expiry-soak.json').read_text())
    keys = ('side', 'request_id', 'phase', 'passed', 'last_state',
            'expires_at', 'finished_at', 'submission_posts', 'ack_posts')
    print(json.dumps({key: value.get(key) for key in keys}))
PY
```

### 实际系统重启后的核对

用户实际重启并解锁 macOS 后，先执行本地数据核对，再启动产品并观察 GUI 自动解锁。脚本要求 OS boot epoch 大于准备时的值；应用重启不能替代它。这里的 Electron Node 只检查数据库／缓存／密文摘要，不调用安全存储解密或 DesktopAuth；普通 GUI Profile 的登录恢复必须由实际 SparkClaw 安装包完成。

```sh
cd /Users/dev/Documents/ChatGPT/sprakclaw-r3
env ELECTRON_RUN_AS_NODE=1 node_modules/electron/dist/Electron.app/Contents/MacOS/Electron \
  .cache/dual-host-acceptance/verify-post-reboot.mjs
zsh .cache/dual-host-acceptance/start-product.sh
```

完成后保留 `macos-reboot-result.json`，核对 GUI 中“凭据恢复后保留”、69 字节文件、未提交状态和真实邮件缓存，并复查 Linux 无该非邮箱对话／消息／request 标记。不要点击该验收请求的提交按钮。随后按实际证据更新 M02／M11／M12 状态和记录提交 SHA。

### 保留与清理边界

私有 `.cache` 证据与脚本只在本机／GB10，不属于可从 Git 重建的交付；后续会话应先确认这些路径仍存在。本阶段继续保留隔离环境以供核对，不创建自动化日程或开机服务。清理时先确认 `gateway.pid` 对应的 `/proc/<pid>/exe` 正是独立根目录的 `gateway`；现有 `stop.py` 会核对该路径后才停止它。24 小时夹具结束前不得撤销其 Mac/Linux 浏览器 Client 或停止 Gateway。凭据清理先核对名称、ID 和用途，当前新 GUI 身份继续供内部测试使用；没有取得明确归属的身份不自动撤销。
