# SparkClaw R3 Mac／Linux 隔离 LAN 验收记录

> 语言：简体中文 | [English](../../docs/macos-r3-dual-host-acceptance.md)

日期：2026-10-03，Asia/Shanghai。分支：`codex/sparkclaw-r3`。这是用户提供 `infinimesh@192.168.20.252` 并授权双端验收后，对[首轮 Mac 记录](macos-r3-acceptance.md)的补充。**下述隔离范围内检查通过，完整 M01–M12 发布验收仍未全部通过。**

## 环境与准确版本

| 项目 | 实际版本／位置 |
|---|---|
| 交付基线 | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| Gateway 构建源码 | `40986ababcafd390b4501686f670fec7395d4d04`；其 Go 产品源码与最终客户端版本相同 |
| 最终客户端／安装包源码 | `f7074ba5116d2724a3dc0781b49e142185f8380b` |
| HTTPS DELETE 修复 | `da851139a9879499be57949a02050d0f8b4ab131` |
| 工作台复制修复 | `f7074ba5116d2724a3dc0781b49e142185f8380b` |
| Mac | ARM64 macOS 26.6.2（25G83），Node 26.2.0／npm 11.13.0；安装包以普通产品模式运行 |
| Linux | `gx10-7660`，ARM64 Linux 6.17.0-1032-nvidia；Go 1.25.5、Node 26.2.0；原生 Electron 客户端使用私有 Xvfb 和已登录用户的 GNOME Secret Service |
| 双端原生运行时 | Electron 44.4.3，Chromium 152.0.7977.130 |
| Gateway 二进制 SHA-256 | `e1d18fd6c33db271487879d4dd7f9cb88edaf8a5bf5b606d856d0b8a9419c9d2` |
| 新建 Linux 根目录 | `/home/infinimesh/.cache/sparkclaw-r3-dual-FZMMvi` |
| 新建 Mac Profile | 仓库 `.cache/dual-host-acceptance/mac-profile` |
| HTTPS origin／部署／Owner | `https://192.168.20.252:25543`／`dual-host-r3-20261003-FZMMvi`／`owner` |
| 叶证书 SHA-256 | `1a2e2668378f11dcf44f0027a15f8318d55604d69ceb85f6fe432cca2d4b32cd` |

SSH 仅用于检查、传源码和启动测试。客户端 HTTPS／WSS 直接经过 LAN，没有 SSH 隧道。全新公开 v2 描述包含专用公开 CA，保留证书链、主机名及叶证书指纹校验。证书有效期两天，仅供验收使用。

Linux 现有仓库位于交付基线，但现有 Gateway 容器重启中且不健康，HTTP `18790/healthz` 返回 502，运行目录缺少新凭据管理文件。没有重启、修改或修复现有服务。新实例的状态、工作区、产物、TLS 密钥及日志全部位于专用目录；使用 mock 模型，关闭外部 MCP／集成，使用不存在的私有浏览器控制路径，合成邮箱关闭提供商采集。未导入现有邮件、浏览器 Profile、Token、Owner 数据或生产数据库。未接入 InfiniCenter／等待 0031，未合并 main。

## 实际执行证据

- **双端原生邮箱同步：**两个独立签发的 Client 和安装 UUID 登录真实 Gateway。通过既有 discovery／capture／parse／classification／assignment 类型化仓库合同，在启动前写入两封新合成邮件、结构化预览及有完整性校验的中文附件。两端同步同一后端邮箱；权威 HTTP 删除拒绝错误版本，相同命令键重试不重复删除，持久 tombstone 传到两个缓存，包括最终空邮箱。合法但 epoch 已过期的游标触发服务端重置，两端重新建立缓存。格式损坏的游标正确返回 400，没有把它记成过期游标恢复通过。
- **本地保留及独立性：**新 Electron 进程恢复各端自己的中文对话、不可变 request ID、本地文件和已保存附件。停止隔离 Gateway 后，两端保留完整邮箱缓存／游标及本地输入；重新连接复用已持久化身份。Linux 后端及另一端存储中没有 Mac 非邮箱标记，Mac 非邮箱存储中没有 Linux 标记。删除后端邮件不删除已保存的本地附件副本。这是应用／进程重启，未重启 macOS。
- **同一个 LAN Broker：**围绕真实 Go `r3browser.Broker` 的临时验收 facade 在 `25544` 监听，两端使用不同 Host 身份。两台实机通过同一适配层操作真实 WebContents，完成导航、输入、点击、读取、20 次 A／B 切换、同页释放再获取、迟到 A 结果不覆盖 B、拒绝系统浏览器命令和 Host 独立弹窗，以及真实点击后的响应丢失在双方形成 unknown 写围栏。facade 是测试入口，未增加生产 Gateway API；测试后已停止监听。没有执行后端采集角色授权及真实支持站点验收。
- **真实交付 ACK 丢失与重启：**两端分别向真实 Gateway 的 mock 模型工作流显式提交一个新普通请求。夹具在真实 Gateway 接受首个 ACK 后丢弃响应，本地持久 receipt 保持未确认。实际重启隔离 Gateway，再启动新客户端进程，对账只发送一次 ACK、零次执行提交；保留相同 digest 和恰好两条本地消息，receipt 转为已确认。执行上下文标记未进入后端持久状态／工作区／trace／日志。未以此代替结果过期或断电验收。
- **凭据初始化／遗失恢复：**真实 `credentials.mjs` CLI 在 Linux 私有真实 TTY 中访问私有管理 socket。首次领取／恢复只显示一次，重复已完成领取不再次显示；journal 只有元数据。Mac／Linux 使用分别签发的凭据，没有复用预置 Linux Desktop Token。Linux 真实恢复吊销旧 Client，后续认证 invalid／401；新凭据以新 Client 登录、同步，安装 UUID 和旧本地数据保留。旧数据仍属原 Client 作用域，未执行跨 Client 数据迁移。
- **Mac 产品 GUI：**用户在终端完成恢复，把一次性凭据直接输入打包应用。Settings → Devices & credentials 真实可达，GUI 签发和吊销成功。已吊销项保留在列表中并标为 `revoked` 和时间，属于设备记录。用户最初报告 Copy 失败；修复、重新构建及重启后，产品直接从自身 Keychain vault 恢复登录，无需再次输入 Token，用户确认复制／粘贴复验成功。复验新增设备实际名称是 `My device`，后端元数据确认已吊销，没有用问询中的建议名称替代实测名称。没有在聊天／截图中采集 GUI 一次性 Token。
- **安全存储／TLS：**Mac Keychain 和 Linux `gnome_libsecret` 加密 vault 可跨原生进程恢复，拒绝 `basic_text`。错误指纹、部署、Owner 和无效凭据被阻断。退出中断真实受保护事件流并保留本地数据，真实恢复／吊销清除旧 vault。GUI 当前设备自我吊销及完整休眠／租约／退出组合尚未执行。
- **验证：**双端各自 88 项桌面测试全部通过；49 项 store／execution／approval／schedule／mail 测试在 Mac Electron 自带 Node／SQLite 下通过。Mac 和 Linux 原生系统剪贴板写入检查通过，读取权限仍拒绝。最终 ARM64 构建、afterPack 允许列表审计及 `hdiutil verify` 通过。已知自动化测试 Token 不在 Git 跟踪文件、实际 ASAR、日志、后端内容或二进制中。安装包允许列表排除私有 Profile、后端凭据及其他设备状态；未解密人工输入的 GUI 凭据进行检查。

## 联测发现的源码修复

原 pinned HTTPS 实现对 DELETE 写正文但未指定长度。Node 对此方法没有自动设置 JSON 正文长度，Gateway 因空请求而返回 400。修复计算编码后的字节数，覆盖冲突的 Content-Length，删除冲突的 Transfer-Encoding 后发送正文。真实 HTTPS 回归检查包含中文 UTF-8 JSON；修复后通过 LAN 完成真实 Gateway 权威删除、版本冲突及重试检查。

工作台权限处理原先只允许音频，统一拒绝了剪贴板写入。现在仅向可信工作台主 frame 允许 `clipboard-sanitized-write`；读取、其他 WebContents、子 frame 和其他 origin 仍拒绝，保留原音频／视频规则。权限边界单测、双端实际 Electron／系统剪贴板检查及用户的 Mac 安装包 Copy 复验通过。两处修复均提交并推送到 R3 分支。

## 更新后的 M01–M12 范围

`PASS（限定范围）` 仅指本轮新建非生产 LAN／测试数据流程；`PARTIAL` 不代表完整项目通过。首轮记录中的 NOT_RUN 是当时本机验收的历史状态，不是本次双端结果。

| 项目 | 当前证据／状态 | 尚未执行 |
|---|---|---|
| M01 | PASS（限定范围）：真实 LAN HTTPS，独立凭据／安装身份，身份／指纹拒绝 | 正式生产证书／分发部署 |
| M02 | PARTIAL：双端实机原生客户端、进程重启、非邮箱独立，本地副本不随后端邮件删除 | macOS 系统重启 |
| M03 | PASS（限定范围）：双端类型化邮箱同步、权威删除／tombstone、离线缓存、过期游标恢复 | 真实提供商账号／连续采集和邮箱支持站点验收 |
| M04 | PARTIAL：同一 Go Broker 通过 LAN 操作真实 Mac／Linux 内嵌页 | 后端采集角色授权和产品工作流／站点集成 |
| M05 | PARTIAL：双端拒绝系统浏览器命令和独立弹窗 | 完整支持站点／Profile／替换矩阵 |
| M06 | PARTIAL：双端 A／B 精确归属、同页再获取 | 授权子页和页面崩溃／重启矩阵 |
| M07 | PARTIAL：真实退出／吊销控制、Gateway 中断、真实写响应丢失 unknown 围栏 | 物理断网、合盖／休眠、完整 GUI 退出／租约到期组合 |
| M08 | PARTIAL：真实 ACK 响应丢失和后端／客户端重启不重发，首轮真实 ENOSPC | 24 小时结果过期、物理磁盘／断电场景 |
| M09 | PARTIAL：普通 Mac GUI 解锁／设置、首轮中文输入法、系统剪贴板成功 | 外接／多屏、完整 Retina／权限、启用麦克风、支持站点 |
| M10 | PARTIAL：重新构建并审计的 ARM64 开发 DMG／ZIP、原生 schema 保护 | Developer ID／公证／Gatekeeper、正式安装／升级／回退 |
| M11 | PARTIAL：真实 LAN Mac GUI 登录／Keychain 重启恢复，原生错误凭据／退出／吊销 | GUI 自我吊销／退出和完整受保护通道矩阵 |
| M12 | PASS（限定范围）：真实 TTY 初始化／恢复、独立身份、GUI 单次签发／复制／吊销、复用及包／日志检查 | 正式生产上线不属于本轮 |

## 最终产物与保留环境

安装包构建源为 `f7074ba5116d2724a3dc0781b49e142185f8380b`，之后的文档提交不改变其源码；已替换首轮记录中同名的本地安装包。签名／公证仍未执行，开发包不代表正式分发通过。

| `apps/desktop/dist/` 中本地文件 | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `87fd3e2b899f4d4bebd90719e988f1cfe0aba06501b4cb9d0e36ec19b36f1c94` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `15b33a20cf8d7be787d861aba12b5ac9485ddabbeb30fef233d890dd09d724b7` |

被 Git 忽略的 `.cache/dual-host-acceptance/` 保存各阶段 JSONL、不带 Token 的设备元数据、双端测试／构建／DMG 日志、`artifact-manifest.json`、私有测试夹具及其 `fixture-manifest.json` 哈希。夹具可能在私有测试目录的 600 文件中保留临时合成凭据，它们不属于部署日志、Git 或安装包。SSH 端邮件 seed 和 Browser facade 是测试专用内容，不是源码修复或交付的产品入口。

`25544` Broker facade 已停止；隔离 `25543` Gateway 和 Mac Profile 暂保留供后续验收，没有新增开机服务或生产部署。测试证书两天后过期。现有 Linux 生产服务／数据未修改。完整验收仍需按上表未执行项继续，并仅使用新授权的测试数据。
