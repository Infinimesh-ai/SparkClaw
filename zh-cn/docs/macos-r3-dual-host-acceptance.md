# SparkClaw R3 Mac/Linux 隔离 LAN 实机验收

> 语言：简体中文 | [English](../../docs/macos-r3-dual-host-acceptance.md)

更新：2026-10-04，Asia/Shanghai。下列实机执行证据主要取得于 2026-10-03。分支：`codex/sparkclaw-r3`。本记录接续[首轮 Mac 记录](macos-r3-acceptance.md)。用户授权当前 Mac 与 `infinimesh@192.168.20.252` 双端验收，并明确 Mac 仅用于内部测试。**已执行证据如下；完整 M01–M12 尚未全部通过。**

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
- **验证与包：**最终双端各 90 项桌面测试通过；Mac Electron Node／SQLite 内 50 项相关测试通过；最终 Mac 原生凭据三进程检查及双端原生 Host 检查通过。ARM64 DMG／ZIP 本机重新构建，afterPack 允许列表审计与 `hdiutil verify` 通过。实际安装包面对私有未来 Schema 5 数据库以退出码 1 拒绝启动，数据库与保存文件字节摘要未变。已知自动化测试 Token 不在 Git、ASAR、日志、后端内容或二进制中，人工 GUI Token 未解密用于扫描。

Mac 产品与 Linux 产品前端一致：实际构建的三个资源文件 SHA-256 逐个相同。主 JS `index-BC08-68r.js` 为 `31738392ed82e657f277b9397fb73245224d211c303a904a1c47f9576bf0157c`。这证明使用相同产品前端资源，不代表未执行的 Linux 物理 GUI 检查已通过。

## 真实邮箱及现有采集环境

用户只授权三封新邮件，各路线只尝试一次，主题前缀 `SCW-mail-R3-20261003-405d1b61`，无附件，限定已有三个 Profile 账户；没有重发。

| 路线 | 实际结果 |
|---|---|
| QQ→Outlook | `email_draft_verification_failed`；发送未确认，收件端限定新时间窗候选为 0；FAIL／不重放 |
| Outlook→Gmail | `email_page_contract_changed`；发送未确认，收件端限定新时间窗候选为 0；FAIL／不重放 |
| Gmail→QQ | 发送确认，QQ 新时间窗唯一候选；产品真实原件采集与 MIME 解析通过，Mac/Linux 原生同步通过（Mac 使用独立原生验收 Profile，GUI Profile 不复制凭据或缓存）；Mac 产品 GUI 同步待恢复登录后补验 |

Mac 完全退出后，GB10 再次采集了唯一新 QQ 邮件：`cap_072ff1b4c907243de56194bdded252ea`，原件摘要 `sha256:7eee34a1df4657c40ee06a497981329fc0f900445efe2372a545bca969a76e2d`。产品验证 receipt／manifest／原件后，经隔离类型化仓库 discovery/page-batch 租约发布并调用实际 MIME parser；没有用构造正文冒充真实邮件。隔离后端 Mail ID `d49178ad82a4f96227e580038f9c4a19814d64f292490c2b00725fd5446edaef`；双端原生缓存正文 SHA-256 `9de3b3bc68a6eef8026b871befc5381b35290f46b4eec11a999e29a464111c83`。这是一次实际采集操作的 Mac 退出独立性证据，不冒充长期定时采集验收。

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

操作失误也如实记录：裸 Electron 夹具读取普通 SparkClaw Profile 时不能使用同一 Keychain 身份，旧逻辑清除了 GUI 保存的登录。没有读出 Token，也没有删除本地历史；上述最后一项修复已保护后续解密失败。2026-10-04 用户确认原 Token 已丢失。旧 Mac GUI Client `client_web_sigBFXKm2wzNAefA-Kz0wJCN` 已于 13:04:19.696（Asia/Shanghai）经隔离管理 socket 撤销，服务端元数据复核成功；新凭据尚未领取。`recover-mac-gui.sh` 已准备并通过 shell 语法检查，只由用户在真实终端领取、输入产品 GUI；没有把未恢复的登录写成通过。原安装 UUID、本地对话、未提交请求与文件摘要再次核对完整。新 Client 使用新的本地作用域，原历史保持原作用域，不做跨 Client 迁移。

## M01–M12 当前状态

`PASS（限定范围）`仅认证列明的内部非生产场景。未执行不计通过；`N/A` 按用户本轮限制判断，并不等于正式发布验收通过。

| 用例 | 当前证据／状态 | 剩余项 |
|---|---|---|
| M01 | PASS（限定范围）：直接 LAN HTTPS/WSS、独立身份、完整证书验证及错误身份拒绝 | 生产证书／部署不在本轮 |
| M02 | PARTIAL：进程重启／升级保留本地全部测试内容，Linux 无副本 | 实际 macOS 系统重启；核对脚本已准备 |
| M03 | PARTIAL：合成邮件双端删除／游标恢复通过；真实 Gmail→QQ 原件、GB10 独立采集、产品解析与双端原生同步通过 | 原登录恢复后的 Mac GUI 真实邮件同步；QQ／Outlook 两条站点路线失败；当前 R3 采集运行版本尚未实机联测 |
| M04 | PARTIAL：共享 Broker 双端输入／导航／读取，普通 Gateway 双端真实公开站点工作流通过 | 完整支持站点矩阵／当前 R3 GB10 采集与 Mac Host 角色联测 |
| M05 | PASS（限定范围）：真实 Host 受管页面拒绝系统浏览器／独立弹窗，未复制 Profile／用后台页替换 | 授权子页当前未受支持，不将能力缺口记为通过 |
| M06 | PARTIAL：A/B、同页再获取、租约释放后归属、实际崩溃新页及旧引用拒绝通过 | 授权子页的完整站点矩阵未执行 |
| M07 | PARTIAL：实际合盖／休眠、45 秒断网、本地保留、Cmd+Q、原生撤销／退出、实际 30 秒租约到期、写响应丢失围栏通过 | 修复后 GUI 断网恢复及 GUI 自我撤销／退出组合 |
| M08 | PARTIAL：真实受限 32 MiB HFS+ ENOSPC、ACK 丢失、后端／客户端重启不重发通过 | 真实 24 小时结果过期进行中；物理填满系统盘／断电不是原 M08 的额外必需条件 |
| M09 | PARTIAL：普通 GUI、用户中文 IME、内置 2× Retina、复制及原生权限检查通过 | 完整支持站点矩阵；外接屏 N/A（无设备），麦克风 N/A（本轮本地工作台未启用） |
| M10 | PASS（内部限定范围）：ARM64 本机包、审计、DMG 实际安装、应用升级保留、真实未来 Schema 启动拒绝 | Developer ID／公证／正式 Gatekeeper 分发 N/A（用户无证书，仅内部测试） |
| M11 | PARTIAL：普通 GUI 登录／此前 Keychain 恢复、原生错误凭据／退出／撤销通道停止、本地保留 | 此次误清登录的恢复、产品 GUI 自我撤销／退出完整矩阵、macOS 重启恢复 |
| M12 | PARTIAL：TTY 单次领取／恢复、独立 Mac 身份、GUI 单次签发／复制／撤销、重连复用、包／日志扫描 | 凭据解密失误后的 GUI 恢复待补；生产上线不在本轮 |

## 24 小时检查与安装包

两端各一个结果以真实时间保留，至今每端仅提交 1 次、ACK 0 次；实际 Gateway 重启后保留相同 request ID、digest 与期限。未调整时钟或缩短 TTL。

- Linux 期限：2026-10-04 16:23:47.870（Asia/Shanghai）。
- Mac 期限：2026-10-04 16:24:33.787（Asia/Shanghai）。
- 夹具在期限后再等 65 秒检查 `delivery_expired` 且 result 不可读取；最早 16:25:39 可收齐两端结果。当前为 `waiting_real_24_hours`，不是 PASS。

最终包源码为 `86176211a7c51abb6ab8680f14e884fc06934430`；后续记录提交不改变该构建源码。

| `apps/desktop/dist/` 本地文件 | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `5bcd1995dfa44b22d81d30c8039e4879e10f55585fe2c87532726828f03cf278` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `c1c78688c25192ef7cd4a085033b2c2c1996f1c067d0410fbb1392a2b71d677b` |

忽略的私有证据位于 `.cache/dual-host-acceptance/`：构建／原生测试／网络／页面崩溃／租约／真实邮件日志、安装包 manifest、夹具源及摘要、原件 receipt；临时自动化凭据仅在私有 mode-600 文件中，不进入 Git 或安装包。`start-product.sh` 启动实际内部安装包。待用户完成独立凭据恢复登录与 Mac 邮件同步后，使用 `prepare-recovered-manual-state.mjs` 创建全新验收内容（不复制旧内容），刷新 `reboot-baseline.json` 的活跃身份、预期文件、加密 vault 摘要并置为 `ready_for_os_reboot`，实际重启 macOS，再以 Electron Node 运行 `verify-post-reboot.mjs`；脚本要求系统 boot epoch 改变，验证本地消息／文件／未提交请求／邮箱缓存／加密 vault，不会用应用重启代替 M02。最后仍需观察实际 GUI Keychain 解锁及核对 Linux 无副本。

`25544` facade 已关闭；隔离 `25543` Gateway、两个真实 24 小时检查进程及 Mac 测试 Profile 暂保留。没有新增开机服务或生产部署；测试证书两天后过期。
