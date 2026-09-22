# 桌面版发布与切换方案

> 语言：简体中文 | [English](../../docs/desktop-release-plan.md)
>
> 候选状态：2026-09-22（Asia/Shanghai）已重新构建并通过资格验证。本方案只做准备，不构成部署、生产切换、删除 Profile 或执行真实 Provider 副作用的授权。

[共享本机后端设计](local-shared-backend-design.md)已包含在本候选版本中：局域网 Web 与本机桌面共用一个
工作台端口，各自使用独立 Client credential，不再需要 `18795`。生产发布或切换仍须完成下文验收并取得明确授权。

## 候选版本身份

候选版本为 Linux/ARM64 SparkClaw Desktop `0.1.0`，使用 Electron `44.4.3`、自带 Chromium `152.0.7977.130` 和 Electron Node `24.21.0`。控制兼容基线为 Browser Bridge `1.0.26`、Playwright MCP `0.0.80`、CLI `0.1.19` 和 Playwright `1.63.0-alpha-2026-08-31`。

| 制品 | 字节 | SHA-256 |
|---|---:|---|
| `SparkClaw-0.1.0-linux-arm64.AppImage` | 126,602,324 | `5503b59447bf548259b87e030f4f6636bdef9e3ed8b5c27411816d6f9c9cc3e5` |
| `SparkClaw-0.1.0-linux-arm64.deb` | 94,339,016 | `0a40de431b3e1715048b472bce37470c1d7f527e76db687f72f3f7b40691aa7f` |

生成后的事实来源是 `apps/desktop/dist/release-manifest.json` 和 `apps/desktop/dist/SHA256SUMS`。重新构建会改变哈希，发布前必须重新生成本表。候选版本目前使用 Electron 默认图标，因此品牌图标确认仍是发布门槛。

## 已完成证据

- `npm run qualify:desktop` 使用 runner 自有 Xvfb 和临时 user-data，覆盖真实 MCP／CLI、任务／个人隔离、嵌套 target／popup、原生输入排除、批准的自动化、下载、托管脚本、后台执行、关闭隐藏、renderer／主进程崩溃、浏览状态持久化、generation 失效，以及不自动重放结果未知的副作用。
- `npm run qualify:desktop-artifacts` 校验两份哈希和 DEB 架构，分别独立解包并以不同临时 user-data 启动，验证打包 WebChat、有界桌面 IPC、安装时生成的 Gateway identity 与 HTTP／SSE 代理、语音 WebSocket Origin 改写和浏览器面板建页。
- `npm run qualify:local-shared-backend` 使用一次性 PostgreSQL 和 Docker 网络中的 LAN client，验证同一 Owner 下独立 desktop／Web identity、空启动、双向 CRUD、两秒内失效通知、文件字节一致及伪造本地来源拒绝。
- 以上是隔离的软件渲染测试，不能证明目标干净系统、真实 GPU／DPI／原生 IME／音视频设备、操作系统麦克风提示或 Provider 登录政策。

## 安装、更新与卸载策略

使用任一制品前先执行 `sha256sum -c SHA256SUMS`。

系统包使用 `sudo apt install ./SparkClaw-0.1.0-linux-arm64.deb` 安装。更新时校验并安装完整的新 DEB，不混用不同版本的应用文件。回滚时应先停止候选版本，再显式安装保留且已校验的完整旧 DEB。

便携试用可为 AppImage 添加执行权限后直接启动。更新时以完整、已校验的新 AppImage 原子替换。旧文件只作为运维人员控制的整版回滚候选；应用不提供引擎选择，也不会静默回退。

`sudo apt remove sparkclaw` 只移除 DEB 应用包；删除 AppImage 也只移除该文件。两种卸载默认都必须保留 Electron user-data 与浏览 Session 数据。删除数据属于另一项破坏性操作，必须由用户明确授权并先解析出精确 user-data 路径；安装／更新／卸载都不得复制、导入或删除旧独立 Chromium Profile。

本候选版本没有自动更新器；分发、更新和回滚均由运维人员控制。

## 切换前验收

改变生产路由前，在目标桌面完成：

1. 校验制品哈希和 ARM64 架构；安装或运行候选版本，但不修改现有浏览器服务。
2. 验证正常启动、关闭隐藏与重开、高 DPI 布局、GPU 渲染、原生 IME、剪贴板、下载显示／取消、音视频设备和操作系统麦克风提示。
3. 在“我的浏览”中正常登录每个必需 Provider，重启应用验证持久化，再测试退出／换号和既有任务账号不匹配拒绝。逐 Provider 记录挑战；不导入 Cookie、不换引擎。
4. 连接在线 Gateway 和语音端点，用非破坏性测试数据验证桌面自动 identity、独立签发的 Web Client、认证 HTTP／SSE／制品和麦克风转写。
5. 同时设置 `SPARKCLAW_ELECTRON_ADAPTER_SOCKET` 与 `SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE` 为桌面 runtime 的 owner-only 路径后启动 Browser Controller；执行只读 smoke，并确认精确 Electron runtime generation。
6. 确认应用图标／桌面入口，取得用户明确的 go/no-go。

任一门槛失败即停止切换。保存日志时不得包含凭据、token、Cookie、消息正文或 Adapter secret。

## 原子切换

仅在明确取得发布授权后执行：

1. 记录当前部署版本、服务状态、`open:browser` 行为、配置和回滚命令；按既有运维手册备份配置及数据，不转换或合并浏览器 Profile。
2. 停止接纳新的浏览器任务，让已接纳任务到达已知终态；未知结果继续标记 unknown，不重放。
3. 安装已校验候选版本并启动 SparkClaw Desktop；通过用户正常登录建立所需网站 Session。
4. 为 Browser Controller 配置 Electron Adapter socket／secret-file 路径，只重启受影响的 Controller 服务，并通过只读 smoke。
5. 以一个发布步骤将唯一生产 `open:browser`／服务入口切到 Electron；不暴露引擎选择器，不保留自动旧引擎回退。
6. 验证 Gateway readiness、普通 WebChat、个人浏览、任务观察、一项经批准的非破坏性任务、下载、语音、生命周期和审计／错误报告。
7. 全部门槛通过后再发布 release／status；旧应用数据保持不动，直到另行取得保留／删除授权。

## 回滚

回滚必须显式且按整版执行。停止新任务准入，保留运行中 unknown outcome 的证据，恢复记录的旧包和 Controller／服务配置，重启受影响服务并重新执行 readiness 与只读 smoke。不得自动重新打开或重放中断任务的副作用；回滚期间不删除 Electron Session 或旧 Chromium Profile。若需要数据迁移，另立经过评审的操作。

## 最终验收记录

用户应记录目标主机／系统、已安装制品哈希、GPU／DPI／IME／音视频结果、Provider 登录／持久化／换号结果、在线连通 smoke、图标确认、安装／更新／卸载结果和切换决定。在记录和发布授权齐全前，当前生产浏览器路径继续是权威入口。
