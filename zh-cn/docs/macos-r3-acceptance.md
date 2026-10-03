# SparkClaw R3 Mac 原生构建与隔离验收记录

> 语言：简体中文 | [English](../../docs/macos-r3-acceptance.md)

日期：2026-10-03，Asia/Shanghai。分支：`codex/sparkclaw-r3`。

本页保留首轮本机验收历史。后续双端 LAN 证据、修复及替换安装包哈希见[Mac／Linux 补充记录](macos-r3-dual-host-acceptance.md)，当前状态请以上述补充矩阵为准。

本机 ARM64 开发包构建、启动及下列隔离检查通过。**M01–M12 完整验收尚未通过**：用户确认暂时没有非生产 Linux R3 后端及 Linux 客户端，本轮只完成 Mac 构建和本机隔离验收。没有接入 InfiniCenter、等待 0031、合并 main、迁移旧测试数据或操作生产数据。

## 版本与环境

| 项目 | 实际值 |
|---|---|
| Linux／共享交付基线 | `d797d6193b3679125dfccd1c274dd990e5cd9c8f` |
| 验收工具修复与最终包构建源码 | `57616e26dac0449ee12b67aa848eb82bacc59590` |
| 本机 | macOS 26.6.2，Build 25G83，ARM64 |
| 构建工具 | Node 26.2.0 `darwin/arm64`，npm 11.13.0，electron-builder 26.15.3 |
| 应用运行时 | Electron 44.4.3，Chromium 152.0.7977.130，内置 Node 24.21.0，ARM64 |
| 原生工具 | `/Library/Developer/CommandLineTools`；Go 1.25.12 `darwin/arm64` 只编译临时 Broker 夹具 |
| Node 工具链位置 | `/Users/dev/.cache/sparkclaw-r3/toolchains/node-v26.2.0-darwin-arm64/bin`；官方归档 SHA-256 核验通过，未替换全局 Node 22 |
| 应用验收数据目录 | `/Users/dev/Documents/ChatGPT/sprakclaw-r3/.cache/mac-acceptance/product-profile`，全新且专用 |
| 本地证据目录 | 仓库下 `.cache/mac-acceptance/`，被 Git 忽略；没有真实 Token／邮件正文 |

最终包实际由上表的 `57616e26…` 构建，后续文档提交不改变客户端源码。旧的 Linux 实施记录是基线证据，不能替代本记录。

## 已执行的构建与检查

会话内为构建命令设置独立 Node 工具链的 PATH，按锁文件执行 `npm ci`，然后执行：

```sh
npm --workspace @sparkclaw/desktop run dist:mac-arm64
npm run test:desktop
npm run qualify:desktop-r3
npm run qualify:desktop-mac-client
```

| 检查 | 实际结果及证据范围 |
|---|---|
| 基线及修复提交 ARM64 打包 | PASS；UI 构建、受管资产检查、afterPack 白名单审计及 DMG／ZIP 生成成功 |
| 安装包结构／架构 | PASS；主程序为 Mach-O ARM64，实际 ASAR 60 个条目、UI 5 个文件；无 Gateway、服务端数据／凭据或未批准依赖 |
| 最终 DMG | PASS；`hdiutil verify` 校验通过 |
| 打包应用启动 | PASS；使用普通产品入口而非 `--qualification`，首次启动和重建后的启动均到达凭据解锁页；首次数据为空，schema 4，应用重启保留安装身份 |
| Node 26 桌面测试 | PASS，87 项；本机执行，不能据此声称所有真实环境用例完成 |
| 实际 Electron Node／SQLite | PASS，49 项；以 Electron 内置 Node 执行 store／capability／execution／approval／schedule／mail 测试 |
| Mac 原生 Host | PASS（隔离）；真实 TLS／WSS Broker 与 WebContents；导航、点击、填写、读取；两个独立页面、20 次 A/B 切换、释放／再获取复用同页、A 迟到结果不选择 B；系统浏览器命令拒绝、Host 弹窗阻断；写入已发生后丢响应，双方记为 unknown 而不重放 |
| Mac 安全存储／本地数据 | PASS（隔离）；三个独立 Electron 进程，真实 safeStorage／Keychain 加密和重启解密；错误凭据拒绝、安装绑定、退出中止流、模拟 401 撤销清除凭据；本地中文对话、请求 ID 和文件保留，awaiting_runtime 不自动提交；SQLite WAL／FULL、schema 4；夹具后端仅接收身份／安装控制，不接收业务内容 |
| Mac 文件系统容量耗尽 | PASS（隔离）；32 MiB HFS+ 镜像填满至真实 ENOSPC，原子文件写入失败、交付事务失败且没有 receipt；释放空间并重新打开后输入保留、无孤立文件，SQLite integrity_check 为 ok；镜像已卸载。不是物理主磁盘满或断电验收 |
| 中文 GUI／人工输入 | 中文界面和中文／emoji 粘贴显示正常；用户以系统中文输入法完成“星爪验收”，随后 AX 树确认该文字。此人工步骤在基线开发包执行；最终提交只改验收工具／npm 入口，产品 src 没有变化，最终包另行启动检查通过。未推定外接多屏通过 |

Electron Node 检查命令：

```sh
ELECTRON_RUN_AS_NODE=1 node_modules/electron/dist/Electron.app/Contents/MacOS/Electron --test \
  apps/desktop/test/client-store.test.mjs apps/desktop/test/client-store-capability.test.mjs \
  apps/desktop/test/execution-client.test.mjs apps/desktop/test/execution-approval.test.mjs \
  apps/desktop/test/schedule-client.test.mjs apps/desktop/test/mail-sync.test.mjs
```

产品入口使用 `SPARKCLAW_DESKTOP_USER_DATA_DIR` 指向上表专用目录。该目录保留供后续验收；自动夹具只创建全新的合成数据，结束后删除其临时 profile、证书和 Broker 控制目录。磁盘容量夹具、运行日志和本地构建产物不进入 Git。

## 初次失败、修复及开放项

基线 `qualify:desktop-r3` 首次失败为 `browser control path is unsafe`：macOS 默认临时根位于 `/var` 符号链接下。该脚本还写死 Linux Xvfb 和 Electron 二进制路径。修复先解析临时根的真实路径，再按平台选择 Linux Xvfb 或 Mac 登录桌面，并使用 Electron 包提供的可执行路径；Mac 保留原生 sandbox／GPU，不套用 Linux 专用启动参数。生产路径安全检查未放宽。

基线桌面测试受相同临时路径问题影响，报告 73 通过、13 失败；鉴权测试失败后有未关闭的流，结束该夹具子进程后总计只报告 86 项，此首次运行不能算完整通过。三个涉及严格真实路径的测试夹具改用 canonical 临时目录，随后完整 87 项通过。新增 Mac 安全存储夹具和强化的原生弹窗／系统浏览器拒绝检查也通过。

打包使用现有 `mac.identity=null`。本机 `security find-identity -v -p codesigning` 为零个有效身份；应用只带 Electron 原有的 ad-hoc/linker 签名，无 TeamIdentifier 和资源封印。`spctl --assess --type execute` **拒绝**该开发包（`code has no resources but signature indicates they must be present`）。未签名／公证，未绕过 Gatekeeper，不能作为正式发行验收通过。

构建仍提示默认 Electron 图标及 UI chunk 超过 500 KiB，均未阻止构建。`npm ci` 报告既有开发工具链 8 项 high 漏洞；本轮没有升级依赖或声称消除它们。ARM64 之外的 CPU、正式安装／升级发行流程、物理断电及真实 24 小时到期 soak 未执行。

## M01–M12 实际矩阵

`PARTIAL` 仅表示本机部分证据已取得；整项必需证据仍缺失。`NOT_RUN` 不表示通过。

| 用例 | 状态 | 已取得证据 | 未执行／仍需证据 |
|---|---|---|---|
| M01 | PARTIAL | Mac 本机 HTTPS／WSS 固定证书及身份拒绝检查 | Linux LAN HTTPS、真实部署／客户端绑定、不依赖 SSH 的端到端连接 |
| M02 | PARTIAL | 原生进程重启保留中文对话／任务／文件；退出／模拟撤销后保留；产品重启保留安装 ID | Mac 系统重启、两台实机非邮箱数据独立，以及真实 Linux 后端无副本 |
| M03 | NOT_RUN | 无本轮真实双客户端邮箱证据；49 项中有合成 mail 单元检查 | 两端真实邮箱同步、删除、游标恢复、常驻收信；不用单元检查替代 |
| M04 | PARTIAL | 同一真实 Broker／Host 适配链操作 Mac 的实际内嵌页 | Linux 内嵌端、专用后端采集角色与真实部署的配对检查 |
| M05 | PARTIAL | Mac Host 系统浏览器命令拒绝、弹窗无独立窗口、真实内嵌页运行 | 实际支持站点流程、Profile 隔离和无后端替代的完整实机观察 |
| M06 | PARTIAL | A/B、20 次切换、同页再获取、迟到响应隔离、Host 弹窗保守拒绝 | 获准子页面流程、真实站点弹窗、页面崩溃／重启及代际矩阵 |
| M07 | PARTIAL | 原生写入丢响应后 suspend／unknown fence；退出中止请求、模拟 401 撤销 | 实际断网、合盖、休眠、GUI 退出、租约到期及真实设备吊销完整矩阵 |
| M08 | PARTIAL | Mac 原生 Electron 交付／ACK／故障测试；隔离卷真实 ENOSPC，无 receipt，恢复后完整 | 真实后端重启、端到端 ACK 丢失、24 小时结果过期；物理断电／主磁盘满 |
| M09 | PARTIAL | 打包 GUI、中文渲染／粘贴、人工系统中文输入、当前屏幕截图 | 解锁后的工作台、外接／多屏、完整 Retina 检查、权限、启用时麦克风及支持站点 |
| M10 | PARTIAL | ARM64 DMG／ZIP、包审计、schema 升级及拒绝更高 schema 且保留数据的原生 SQLite 检查 | Developer ID 签名／公证和 Gatekeeper 接受、真实安装／升级／回退；Intel 未执行 |
| M11 | PARTIAL | 产品全新解锁页；真实 Mac safeStorage 跨进程恢复、错误凭据拒绝、退出／模拟撤销保留合成历史 | 真实 LAN 产品登录、Keychain 恢复、真实设备撤销及受保护通道矩阵 |
| M12 | NOT_RUN | 无真实后端领取／设备管理证据；实际包白名单检查通过 | 后端初始化／设置签发、一次性显示／复制、设备复用、遗失恢复／撤销和日志检查 |

## 后续人工步骤

1. 准备独立的非生产 Linux R3 后端和 Linux 客户端，记录其准确 SHA，使用公开 v2 HTTPS 连接描述和专门签发的 Mac 设备凭据；Token 只输入应用，按[连接指南](macos-connection-guide.md#51-凭据从哪里取得)领取。
2. 在本轮保留的专用 Mac profile 完成登录、宿主独立授权及 M01／M11／M12，再做两端非邮箱隔离和邮箱同步。只使用本轮新建数据，不导入旧测试历史。
3. 在无副作用页上保持活动租约，分别执行断网、睡眠／合盖、退出／重启、撤销和写入丢响应，观测未知结果并明确核对；缺少真实租约时不宣称完成 M07。
4. 补录显示器、权限、麦克风（确需启用时）和支持站点结果。正式发行需要用户提供适用的 Developer ID／公证配置，再独立执行签名发行流程和升级／回退；不要通过绕过 Gatekeeper 记为通过。

## 本机产物

目录：`apps/desktop/dist/`。最终构建 SHA 为 `57616e26dac0449ee12b67aa848eb82bacc59590`；产物只在本机，不发布到生产或 Git。

| 文件 | SHA-256 |
|---|---|
| `SparkClaw-0.1.0-mac-arm64.dmg` | `2e24930cf0a9b3fed8758cf3d8b4fa9fc898223224442f38178da9e134bd454d` |
| `SparkClaw-0.1.0-mac-arm64.zip` | `35cebe96054a003a3749f92e3dd4126b614280ab5913d1642fc2c2aff48a5528` |

证据文件：`build-arm64-baseline.log`、`build-arm64-57616e26.log`、`desktop-node-tests.log`、`desktop-node-tests-fixed.log`、`electron-node-tests.log`、`r3-native-baseline.log`、`r3-native-final.log`、`mac-client-native.log`、`fs-full.log`、`dmg-final-verify.log`、`packaged-first-launch.log`、`packaged-launch-57616e26.log`、`packaged-profile-first.json` 和 `packaged-profile-restarted.json`，均在本地证据目录。GUI AX／截图和人工输入反馈保留在本次 Mac 会话工具记录中。
