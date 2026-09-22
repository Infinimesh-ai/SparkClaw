# Electron 桌面浏览器实施交接

> 语言：简体中文 | [English](../../docs/desktop-electron-implementation-handoff.md)
>
> 更新：2026-09-21（Asia/Shanghai）。无需用户凭据、目标硬件验收或发布授权即可完成的四个实施阶段均已实现并通过资格验证；候选版本尚未部署或切换生产。

## 新对话从这里开始

工作目录：`/home/infinimesh/Documents/SparkClaw-workbench-page`。当前分支 `codex/sparkclaw-workbench-page`，交接时 HEAD 为 `718e1e720f413d46a405016228842560e244d4ae`。工作区有尚未提交的前序修改，必须先检查并保留，不能 reset 或覆盖。

先读根目录 `AGENTS.md`、本交接及[桌面客户端与内置浏览器设计](desktop-client-embedded-browser-design.md)。设计文档是完整要求，本交接用于确定实施起点和顺序。按 `AGENTS.md` 读取 InfiniCenter 簇清单、注册表、信箱和 proposed 决策；交接时没有未归档 SparkClaw 信件或待评审 proposed 决策，新对话仍需重新检查。

直接沿已确定方案实施，不需要再次询问是否使用 Electron、是否分仓或是否允许用户接管任务页。实际遇到新的产品取舍再提出，独立工作可以继续。当前没有提交、推送、部署、真实邮件发送或删除旧 Profile 的任务。

## 已确定的要求

1. **唯一引擎为 Electron 自带 Chromium**，用 `WebContentsView` 显示个人页和任务页。由 Chromium 管理渲染及辅助进程，不要求一标签一操作系统进程。
2. **同仓维护、按职责拆分**：复用 `apps/webchat` 的 React/Vite 工作台；新增 `apps/desktop` 放主进程、浏览器适配、脚本宿主及打包；WebChat 通过可选能力接入。普通浏览器中的 WebChat 继续可用。
3. **保留现有控制方式**：Gateway → Browser Controller → Playwright MCP / 固定 CLI 脚本 → 任务范围 Bridge。通过 Electron Browser Adapter 迁移 Bridge 行为，不假设现有 Chrome 扩展和 Tampermonkey 能直接运行，也不增加给模型使用的任意 JS/CDP 工具。
4. **右侧“我的浏览 / 任务观察”**：个人页可操作；任务页仅观察，无接管／交还／恢复功能。用户可切换观察任务标签页，不能改变 Controller 自动化目标、lease、任务 viewport 或焦点。个人页与任务页使用独立 WebContents 和归属注册表。
5. **共享网站登录态**：个人页与任务页共享独立的持久化 Electron 浏览 Session；特权工作台使用另一 Session。新 Session 通过正常网页登录建立，不复用、复制或导出旧 Chromium Profile、Cookie、密码。换号／退出影响任务登录态，需保持现有账号校验。
6. **登录讨论的结论**：当前流程是用户登录网站、浏览器保存 Cookie，不是 SparkClaw 获取 OAuth API token。不能单凭 OAuth 嵌入式授权政策断言该流程失败；Electron 实际网页登录与持久化尚未验证。按服务商实测并记录结果，不擅自换引擎、改为 API 工作流或导入 Cookie。
7. **生命周期**：关闭工作台窗口隐藏窗口并保留 runtime／浏览页面；重开复用同一 runtime。主进程崩溃或明确退出可能中断任务，应撤销 generation、如实报告，不能自动重放结果未知的发送等副作用。
8. **旧路线已经退出**：外部 Chromium 嵌入、`surface-host`、X11 reparent、专用／嵌套 X Server、Xephyr/Xpra、浏览器画面串流、CEF、自定义 Chromium 不再是阶段或回退选项。隔离测试用 Xvfb 不属于产品架构。

布局沿用主设计：初始 1440×900、最小 1180×720；侧栏 244 px、会话最小 520 px、分隔条 6 px、右侧默认 640 px（480–760 px）。空间不足进入浏览聚焦布局，不挤压运行中任务 viewport。

## 已完成与未完成

实施前已完成：双语主设计与相关运行时／组件／迁移文档收敛为 Electron 唯一路线。退役 X11 Helper 现有一套仅作诊断、由 runner 自建 Xvfb 的资格测试，覆盖 Electron 锚点身份、焦点、裁剪及 detach／崩溃行为。

**实施检查点（2026-09-21）**：`apps/desktop` 锁定 Electron `44.4.3`、ARM64 Chromium `152.0.7977.130` 与 Electron Node `24.21.0`。产品采用 sandboxed `WebContentsView`、独立工作台／浏览器 Session、不透明归属与 generation、任务范围 `webContents.debugger`，以及 owner-only Unix socket 和短时单次凭据。现有 Controller 可选择新 Adapter，旧扩展路径继续保留。托管脚本宿主在 document-start 安装精确的 QQ／Gmail／Outlook Reader，并以受限 GM 存储／菜单能力安装固定 AI 导出器；任务下载和个人下载均有明确归属、限制、取消及回执或 UI 语义。

WebChat 的可选桌面能力已提供“我的浏览 / 任务观察”、受限个人导航、只读任务选择、权限和下载。主进程 IPC 验证精确工作台 frame、自定义 origin、不透明引用、generation／revision 和 bounds。打包资源经 `sparkclaw-app://` 提供；同源代理仅向受控回环 Gateway 注入安装时生成的 Client，并覆盖 HTTP／SSE／文件，语音使用同一配置 origin。关闭隐藏、工作台／任务／主进程故障、浏览 Session 持久化和未知副作用不重放均已验证。

隔离 runner 已覆盖真实 MCP `0.0.80`、CLI `0.1.19` 和 Playwright `1.63.0-alpha-2026-08-31`；worker／OOPIF／popup 范围；软件可测的完整原生输入边界；后台执行；固定 `640×720` 任务 viewport；托管脚本、下载与生命周期恢复。ARM64 AppImage／DEB、SHA-256 记录及两个成品的独立解包启动探针均已完成，详见[桌面发布与切换方案](desktop-release-plan.md)。

**仍需用户最终验收**：真实服务商网页登录、持久化与换号；真实 GPU／DPI／原生 IME／音视频及操作系统麦克风权限；目标干净主机的安装／更新／卸载；品牌图标；在线 Gateway／Controller smoke，以及明确的生产切换授权。这些是验收／发布门槛，不能由 Xvfb 夹具冒充。没有修改生产服务、旧浏览器 Profile 或登录数据。

仍保留的未提交修改包含：

- 双语设计／索引／Changelog／运行时说明；新主设计文件尚未跟踪。
- `scripts/install-browser.sh`、`scripts/sparkclaw-browser-launcher.sh`、`scripts/test_browser_bridge.py`、`tools/browser-bridge/test/relay-connection.test.mjs`：前序独立 Chromium 的调试提示／生命周期改动，不能随手覆盖或当成新 Electron 实现。
- `.gitignore` 和未跟踪的 `tools/browser-surface-host/`、中文镜像：仅作历史诊断；只能通过仓库内 Xvfb runner 测试，不得连接生产环境。

本机为 Linux ARM64；宿主 Node `v26.2.0`、npm `11.17.0`、Electron `44.4.3`、其自带 Chromium `152.0.7977.130`、Electron Node `24.21.0`。Xvfb 仅用于一次性资格测试。历史锚点夹具与产品运行时目前使用同一 Electron 版本，但用途和依赖范围仍相互独立。

## 代码入口与迁移注意点

以下为仓库根相对路径：

| 入口 | 当前事实与下一步 |
|---|---|
| `package.json`、`apps/desktop/package.json`、`apps/webchat/package.json` | 根目录现已纳入 desktop 与 Bridge workspace，并有独立桌面开发／测试／资格入口；生产启动链未改变 |
| `apps/webchat/src/App.tsx`、`apps/webchat/src/desktop/` | 普通 WebChat 保留原 Inspector；可选类型化桌面能力提供个人浏览、只读任务观察、下载和权限 |
| `apps/webchat/src/api/client.ts`、`apps/webchat/src/audio/realtimeSpeech.ts` | 普通浏览器 URL 不变；打包桌面端使用带认证的同源自定义 scheme HTTP／SSE／文件代理及配置后的语音 WebSocket base |
| `apps/desktop/src/main/desktop-capability.mjs`、`owner-browser-services.mjs` | 精确 frame 的有界 IPC、布局、个人权限和 Owner 下载；WebChat 不获得任意 CDP／源码／进程能力 |
| `tools/browser-controller/src/mcp-client.mjs`、`electron-adapter-client.mjs` | MCP 启动前登记精确任务／generation 绑定与 token hash；可选 Electron 路径使用单次凭据，旧扩展路径继续保留 |
| `tools/browser-controller/src/cli-client.mjs`、`cli-task.mjs` | CLI 与个人页启动路径使用同一可选 Adapter 登记，并已在本地夹具上独立于 MCP 通过资格验证 |
| `apps/desktop/src/browser/protocol.mjs`、`adapter-server.mjs` | Electron 通道有独立 runtime kind/version/generation、owner-only socket／secret、严格请求 schema 和单次凭据，不伪装扩展 |
| `tools/browser-controller/src/mcp-tabs.mjs`、`cli-page-guards.mjs` | 已识别 Electron 内部连接 URL，同时不放宽任务拓扑，也不接管个人页 |
| `tools/browser-bridge/src/relay-connection.mjs` | Electron facade 复用任务范围 relay，禁用 handoff，使改变焦点的 `bringToFront` 为空操作，并拒绝直接的全局 target 操作 |
| `tools/browser-bridge/src/protocol.mjs` | 有 handoff／background-input marker；Electron 任务路径须拒绝 handoff，后台操作不转移用户焦点 |
| `tools/browser-controller/src/extension-downloads.cjs`、`install-playwright-downloads.mjs` | pinned Playwright 下载补丁已存在；Electron 下载要保持现有有界制品／取消／回执语义 |
| `tools/browser-controller/src/provider-scripts.mjs`、`scripts/email/userscripts/`、`tools/browser-userscripts/` | 托管脚本注册、源码和 bundle；迁移文档起始注入、page world 网络 hooks、origin/frame、readiness、revision/hash 和 AI 导出器 GM 能力 |
| `tools/browser-controller/test/fixtures/adapter-live.html` | 可复用本地页面夹具；fake MCP/CLI 仅适合单测，不能代替实际 pinned 客户端连通证据 |

当前兼容性基线为 Bridge `1.0.26`、MCP `0.0.80`、CLI `0.1.19`、Playwright `1.63.0-alpha-2026-08-31`。旧独立 Chromium pin 不适用于 Electron；实际安装状态在实施时复核。

## 按顺序实施

### 1. 最小 Electron runtime 与真实控制验证

- [x] 新增桌面 workspace、锁定 Electron、建立临时 user-data 与隔离显示测试入口；保留当前生产服务和浏览器。
- [x] 主进程建立个人／任务 page registry，引用绑定 owner、role、task/session、runtime/page generation；外部页面 sandbox、context isolation、禁 Node，工作台使用独立 preload／Session。
- [x] 实现 owner-local 认证通道和 launcher。凭据不进入页面／日志；连接绑定身份、任务、generation 和一次性凭据。旧 relay 方法名仅作兼容 envelope，不开放浏览器全局 remote-debugging port。
- [x] 将任务限定的 attach/detach/sendCommand/events 转成 `webContents.debugger`，并验证 worker／OOPIF／popup 范围以及个人／过期／外部／全局 target 拒绝。
- [x] 用**实际 pinned MCP 和 CLI** 在本地夹具完成建页、导航、snapshot、fill/click/read、截图、开关页及 teardown；两条通道均不是 facade mock。
- [x] 验证软件可测的原生输入边界：鼠标、键盘／IME 键序列、快捷键、粘贴、拖放、右键菜单、对话框和 DevTools 被阻断，批准的自动化仍可用；真实原生 IME／硬件行为留作用户验收。
- [x] 验证观察切换保持精确不透明引用和固定 `640×720` viewport，隐藏／后台自动化继续执行。
- [ ] 尽早验证所需网站的正常登录和 Session 持久化。本地网站 Cookie 共享通过；真实服务商尚未测试，且需要用户亲自输入凭据。

**这一阶段的交付**：最小可运行 Electron + 两条真实控制路径 + 隔离／只读证据。不能用一个仅能打开页面的壳宣称兼容验证完成。

### 2. 脚本、账号和下载能力

- [x] 为 QQ／Outlook／Gmail Reader 实现精确 origin/frame 匹配、document-start main-world 注入、readiness/version 和固定 hash。
- [x] 为 AI chat exporter 实现真实且受限的 `GM_getValue`、`GM_setValue`、`GM_registerMenuCommand` 持久化／菜单语义，不暴露文件系统或任意 IPC。
- [x] 生成并校验精确脚本 registry、revision 和 hash，保留已有账号检查与 provider gate。
- [x] 将任务下载绑定到精确连接，包含大小／数量限制、取消、源清理和回执；个人下载另以有界引用投影到 UI。
- [x] 在本地夹具验证共享持久登录态和 fail-closed 任务边界；真实 Provider 登录／换号不匹配／挑战效果仍需用户批准后验收。

### 3. 工作台、界面和生命周期

- [x] 在 `apps/webchat/src/desktop/` 暴露可选类型化能力，接入“我的浏览 / 任务观察”、个人导航、下载／权限 UI 和只读任务选择；普通 WebChat 继续可用。
- [x] main 验证 IPC sender、origin、role、opaque refs、generation、bounds 和 revision；workbench 不持有任意 CDP、可执行源码或进程控制能力。
- [x] 个人页随布局调整；任务页固定 viewport，空间不足时报告状态。观察 UI 不触发 handoff、任务焦点／控制或 debugger 重绑。
- [x] 实现打包资源、安装时生成 Client 的 Gateway HTTP／SSE／文件认证及语音 WebSocket 连接，不再使用工作台 pairing。麦克风仅允许可信工作台 audio frame；真实硬件／系统权限仍需验收。
- [x] 实现关闭隐藏、同 runtime 重开、个人 Session 持久化，并分别验证工作台／任务／主进程故障、generation 撤销和未知副作用不重放。

### 4. 打包与切换准备

- [x] 生成 ARM64 `.deb`／AppImage、校验和与依赖／版本记录，记录安装／更新／卸载策略，并独立解包启动两个成品；真实目标主机 GPU／DPI／IME／音视频验收仍待完成。
- [x] 准备安装器、服务、`open:browser`、登录入口和文档的原子迁移步骤，不增加引擎选择器或自动旧引擎回退；步骤尚未执行。
- [x] 交付证据及具体[发布／切换方案](desktop-release-plan.md)。部署、删除旧 Profile 和真实效果不在本轮范围，回滚需后续授权并以整版为单位执行。

## 测试和桌面故障边界

此前旧 X11 试验在活动 `DISPLAY=:1` 操作可见性、reparent、焦点和堆叠。这些运行期间，系统日志反复出现已 disposed 的 `MetaWindowActorX11`、stage／allocation 错误和 Clutter 断言，并在 `2026-09-21T10:10:40.586013+08:00` 明确记录 `GNOME Shell crashed with signal 11`。Xorg 之后正常结束；同一时段没有 NVIDIA Xid、GPU reset 或 OOM 证据。因此直接故障是活动 Display 窗口操纵路径触发 Mutter／GNOME Shell 合成器崩溃，不是编译错误或已证实的 GPU 故障；日志没有分离出具体 X11 操作或 Mutter 内部缺陷。之后的修订还曾直接操纵窗口管理器 Frame，但它晚于上述崩溃，只能额外证明测试路径不安全，不能说是该次崩溃的触发点。

所有故障注入先在独立 Display、临时 user-data、本地夹具和受控子进程中执行。可使用 Xvfb；仅设置某个“isolated”环境变量不等于已经隔离。不得在用户活动 `DISPLAY` 上运行会移动／隐藏／重挂载／强杀窗口的测试，也不操作用户桌面 WM。退役 surface-host 只能由仓库 runner 启动；runner 会创建并验证自有 Xvfb 进程。清理仅针对本次创建的进程和临时文件；应用故障应能在应用／服务层恢复，不依赖 Linux 注销。

验收必须覆盖：真实 MCP+CLI、并发任务与个人页隔离、过期引用及嵌套目标拒绝、用户输入阻断但自动化可用、观察切换不改目标、背景执行和固定 viewport、脚本起始注入与 GM、网站 Session 登录／换号、下载、窗口关闭保活、崩溃失效与副作用不重放、打包连接、普通 WebChat 回归。通过一项不代表其他项通过。

已有回归命令（执行前检查环境；这些不是 Electron 验收命令）：

```sh
npm test --prefix tools/browser-bridge
npm test --prefix tools/browser-controller
npm run test:email-scripts
npm run test:webchat
npm run build:webchat
npm run test:desktop
npm run qualify:desktop
npm run package:desktop
npm run qualify:desktop-artifacts
git diff --check
```

与在线 Bridge 及生产 Profile 隔离的历史原生窗口边界诊断：

```sh
npm ci --prefix tools/browser-surface-host/test/electron-anchor
make -C tools/browser-surface-host test
make -C tools/browser-surface-host test-electron-anchor
```

新增 Electron 测试入口及 CI 时明确隔离 Display 和临时数据目录；必要的 Gateway 测试按实际修改范围运行。Markdown 使用 `.github/workflows/ci.yml` 的 Docs 镜像／链接检查；新增文档需有中英文对应文件。

## 仍需用户验收与发布授权的门槛

自动化可回答的实施问题已经有证据。最终仍需用户凭据、真实硬件或发布权限：逐 Provider 的网页登录、持久化、退出／换号及人机挑战；真实 GPU／DPI／原生 IME／音视频／麦克风；目标干净主机安装／更新／卸载；品牌图标确认；在线 Gateway／Controller smoke，以及生产切换 go/no-go。失败时记录精确版本与受影响门槛，不换引擎、不导入旧 Profile，也不静默缩减范围。

InfiniCenter 路径为 `/home/infinimesh/InfiniCenter/clusters/ProjectGroup-2/`。结束阶段更新 `status/sparkclaw.md`。保持 SparkClaw--JingSi Runtime v1 及 SparkClaw--IMMS evidence v2 契约不变；若确需跨项目接口修改，先走 accepted 决策。当前没有需要其他项目配合的接口变更。

## 可复制到新对话的指令

> 请先阅读根目录 AGENTS.md、zh-cn/docs/desktop-electron-implementation-handoff.md 和 zh-cn/docs/desktop-client-embedded-browser-design.md，检查并保留当前未提交修改，然后从交接中的第 1 阶段开始实施。唯一方案是 Electron 自带 Chromium + WebContentsView，复用 WebChat 与现有 Controller／Playwright 控制链。不要恢复旧 X11 嵌入方案，不在活动桌面做破坏性测试，不切换生产环境。架构已经确认，遇到新的实际疑问再提出；报告实际实现和测试证据，不把待验证能力算作完成。
