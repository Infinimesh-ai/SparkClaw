# 仓库内邮件运行时

> 语言：简体中文 | [English](../../docs/local-mail-runtime.md)

自 2026-10-15 起，QQ Mail、Outlook、Gmail 的实现由 SparkClaw 仓库内的
`tools/browser-controller/mail-runtime` 维护。Gateway 继续调用原有鉴权 Controller
API，`LocalMailRuntimeFactory` 通过原有签名 Browser Host、Playwright 和 Bridge
直接执行本地处理器。此链路不再安装 App-CLI 包、Python Registry、独立 Executor
或成套发行。邮件公开 API、账号归属、审批规则、原有 EmailPopup、EmailCompose、
附件、浏览器 Profile 和桌面数据保持原状。

## 源码与生成产物

- Provider 处理器、邮件解析、发送/采集日志与页面钩子：
  `tools/browser-controller/mail-runtime/applications/mail/`。
- Network Reader 源码：上述目录的 `userscripts/`；生成产物：
  `tools/browser-controller/mail-runtime/assets/mail/`。
- 生成绑定：`tools/browser-controller/mail-runtime/bindings/`。
- Gateway 投影：`services/gateway/internal/emailautomation/provider_scripts.json`。

使用 Node 26、npm 11。安装 Controller 固定依赖后，在本仓库修改源码并重建：

```sh
npm ci --prefix tools/browser-controller
node tools/browser-controller/mail-runtime/applications/mail/userscripts/build.mjs
npm run build:mail-bindings
npm run sync:mail-projections
npm run build:desktop-managed-scripts
npm run check:mail-projections
npm run check:desktop-managed-scripts
```

检查覆盖生成绑定、Reader 源码对应的产物字节/摘要/版本、浏览器组件注册和 Gateway
投影。保留原 Reader UUID、名称和页面行为。复用的通用 Host 代码保留 MIT 声明，
邮件实现保留 Apache-2.0 声明，均随源码分发。

## 持久化与恢复

`SPARKCLAW_MAIL_RUNTIME_STATE_DIR` 指定私有持久状态目录。Linux 安装器使用
`$XDG_DATA_HOME/sparkclaw/browser-controller/mail-runtime`，默认数据根为
`$HOME/.local/share`。独立入口默认在同一数据根下按 Profile 分目录。备份和恢复时，
将其与已有 `SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT` 配套保留。

读取、探测和监听释放每次调用的内存，不再累计 App-CLI 任务账本，也不再受其
10,000 条任务/512 MiB 准入额度影响。监听保留独立 Provider 队列、签名租约、续期和
重新同步事件。现有请求大小、附件、资源归属和浏览器操作边界继续生效。

仅发送操作保留私有持久记录，绑定原 owner/Profile/Provider/task、已审批输入摘要、
执行证据和结果；不在其中保存邮件正文或浏览器凭据。创建记录和副作用标记在执行、
派发前落盘。已完成的调用重放回执；中断或结果未知的发送不自动重发。只有明确证明
尚未发生副作用的阻塞操作，才能在原截止时间和身份范围内恢复。对账可使用更新后的
凭据，但必须匹配原审批邮件和附件字节，不重新读取已经失效的上传路径。

工作区已有发送日志继续保留。旧的有效 `sent`、`not_sent` 回执可以在不打开浏览器、
不重发邮件的情况下恢复；未发送证明保留原任务、绑定及证据 epoch。缺少证据时继续显示未知；
历史 legacy 错误且没有日志，不能推断为未发送。
旧 App-CLI 账本保留但不导入。Gateway 不恢复被中断的 legacy 聊天发送；不要绕过
产品流程，直接向 Controller 重放迁移前的 legacy 发送，因为此处没有其旧账本防重记录。

## 安装与验证

`npm run setup:browser-controller` 停止并禁用已有的
`sparkclaw-app-cli-executor.service`，保留其安装、unit、ledger 和现有
workspace/Profile，然后启动使用本地邮件状态的原 Controller。
`npm run check:browser-controller` 校验该状态。不会清空旧账本或发送日志。
为兼容既有传输格式，诊断字段 `versions.app_cli` 现在返回 `local-mail-runtime`。

```sh
node tools/browser-controller/test/qualify-local-mail-deployment.mjs
SPARKCLAW_MAIL_BROWSER_TEST=1 node --test --test-timeout=120000 tools/browser-controller/test/*.test.mjs
python3 -m unittest scripts/test_local_mail_installation.py
```

安装根目录 Electron 依赖（`npm ci`）后可运行真实浏览器验证；Linux 还需隔离显示环境：

```sh
SPARKCLAW_MAIL_BROWSER_TEST=1 node tools/browser-controller/test/qualify-local-mail-browser.mjs
```

以上使用隔离状态及合成邮件/浏览器夹具，不代表新增真实邮件投递验收，也不会升级已
安装的 Gateway 或 SparkX。此前真实 Provider 的投递记录仍属历史证据。本次修改限于
SparkClaw，不修改上游 App-CLI 或其他项目协议。配置的祖先路径中未找到 InfiniCenter，
不宣称取得新的跨项目验收。旧 App-CLI 抽离文档保留为历史记录，当前实现及构建流程以
本页为准。

### 2026-10-15 验证记录

macOS arm64、Node 26.2.0 / npm 11 下，启用合成邮件浏览器测试的 Controller
全套通过 324 项，18 项平台限定或需单独启用的测试跳过。Desktop 通过 373 项；WebChat 通过
318 项及严格生产构建。浏览器安装/组件套件通过 52 项，1 项可选测试跳过，覆盖
全新安装、升级数据保留及旧服务停用失败。绑定、Reader 源码与产物一致性、Gateway
投影及桌面预加载检查通过。WebChat 界面源码及桌面 managed-script 产物与起始版本
`1a4dfb0a` 字节一致。

隔离 Electron 44.4.3 / Chromium 152 验证通过 `Controller.runScript`、本地工厂、
签名 Host 及真实 Playwright CLI 的完整链路，在仅允许回环地址的合成页面上验证
私密字段、并发重试合并、完成后回执复用和正常清理。macOS 测试夹具等待真实 CLI
close，并确认记录的 daemon PID 已退出后，才移除其私有 metadata；这不代表验证了
Linux `/proc` 归属或崩溃回收，生产清理代码保持不变。

受影响的四个 Go 包 `emailautomation`、`browsercontrol`、`emailmanagement`、
`mailsync` 测试通过；全量 `go build ./...` 和 `go vet ./...` 通过。macOS 全量
Go 测试仍有 14 项失败；在 Gateway 源码与 `1a4dfb0a` 完全一致的工作树中逐项重跑，
14 项均复现相同错误位置和内容，涉及临时目录符号链接、Linux `/dev/shm` 及已有
workflow/host 失败。这证明本次没有新增失败，不代表全量 Go 测试已全部通过。
