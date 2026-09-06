# Playwright Extension 迁移交接

> Language: [English](../../docs/playwright-extension-migration-handoff.md) | 简体中文

## 快照

Playwright Extension 迁移已于 2026-09-05 完成。SparkClaw Browser Bridge
是唯一生产浏览器传输。browserd、Host-CDP、`agent-browser`、迁移 Selector、
相关部署连线和可执行测试均已删除。

事实优先级：

1. [Playwright Extension 浏览器设计](playwright-extension-browser-design.md)
   记录已接受的架构和迁移门槛。
2. [浏览器 Runtime](browser-runtime.md)记录当前生产实现。
3. 当前源码定义精确协议和行为。
4. [Host-CDP 浏览器设计](host-cdp-browser-design.md)仅作为历史记录。

## 最终架构

- SparkClaw 在桌面宿主机安装一个固定 Chromium 制品。
- `sparkclaw-browser.service` 启动浏览器时不使用 remote-debugging、
  automation 或 headless 参数。
- Owner 使用
  `~/.local/share/sparkclaw/browser/default/user-data` 下的单一持久 Profile。
- 固定校验和的 SparkClaw Browser Bridge 只连接任务拥有的标签页。
- `sparkclaw-browser-controller.service` 通过 Owner-only Unix Socket 管理
  有界 Playwright MCP 和 CLI 进程。
- 通用浏览器任务使用 Provider-neutral Gateway Adapter 和 Playwright MCP。
- QQ 邮箱、Outlook 和 Gmail Probe/Send 使用仓库固定的 Playwright CLI Handler。
- 加密 Gateway Vault 是 SparkClaw 唯一保存 Bridge Credential 的位置；浏览器认证
  始终留在浏览器 Profile。
- 后台连接和操作不聚焦浏览器，也不选择 Owner Tab。只有显式
  `tabs.handoff` 契约可以聚焦任务标签页。

本次切换验证的生产版本：

| 组件 | 版本 |
|---|---|
| SparkClaw Chromium | `148.0.7778.0` |
| SparkClaw Browser Bridge | `1.0.18` |
| Playwright MCP | `0.0.80` |
| Playwright CLI | `0.1.19` |
| Playwright Library/Core | `1.63.0-alpha-2026-08-31` |

Bridge Release 使用带版本的 Service Worker 入口路径。这避免 Unpacked Extension
升级后，在已安装扩展目录和 Native Host 已前进时继续保留旧 Worker Script Cache。

## 阶段状态

| 阶段 | 最终状态 |
|---|---|
| 0. 兼容性 PoC | 完成：MCP/CLI Attach、登录保持、Credential Lifecycle 和 Cleanup Contract 均已通过 |
| 1. Host Controller | 完成：私有协议、Generation、Health、Deadline、Supervision 和 Cleanup 已投入生产 |
| 2. 通用 MCP Adapter | 完成：Provider-neutral 行为和真实表单操作已通过生产 Bridge |
| 3. 确定性 Provider Script | 完成：六个固定 Probe/Send Handler 和三账户只读资格验证已通过 |
| 4. SparkClaw Browser Bridge | 完成：独立打包、保留 Attribution、固定校验和、后台安全和显式 Handoff 均已验证 |
| 5. 部署资格验证 | 完成：共享 Local/Remote Setup、Check、Compose Contract、Restart、Pairing、Detach 和 Profile Persistence 均已通过 |
| 6. 原子切换与删除 | 完成：Bridge-only 配置已启用，全部可执行旧路径已删除 |

迁移 Selector 和 fallback 已不存在。遗留
`SPARKCLAW_BROWSER_AUTOMATION_*` 或 `SPARKCLAW_BROWSER_CDP_*` 值会
以迁移错误 Fail Closed。

## 宿主机切换

已登录的资格验证 Profile 通过目录原地重命名成为生产默认 Profile。过程中没有读取、
复制或导出任何账号数据、Cookie、浏览器数据库、Credential 或 Storage State。

旧默认 Host-CDP Profile 仅作为 Owner-only 归档保留：

```text
~/.local/share/sparkclaw/browser/default-host-cdp-retired-20260905
```

两个 Profile Root 都是 `0700`；Controller 和 Native Bridge Socket 都是
`0600`。已安装浏览器命令行只包含固定浏览器、持久 Profile、已安装 Bridge
和普通显示参数。

源码与部署依赖扫描确认没有活动 Consumer 后，已安装的
`sparkclaw-browserd.service`、browserd 配置、browserd Runtime Directory
和 SparkClaw `agent-browser` Namespace 均已删除。归档 Profile 不是可执行 fallback。

## 真实资格验证

最终 `1.0.18` Bridge 通过已保存的 PostgreSQL Credential Record、加密 Vault、
私有 Controller Socket 和持久默认 Profile 完成全部真实检查：

- `npm run setup:browser` 完成安装、Service Restart、精确 Loaded-version
  Readiness 和 Profile 复用。
- Local 与 Remote 两个私有部署文件都通过 Browser Setup 验证。
- 通用 No-handoff 场景在约 23 秒内完成 Open、Read、Fill、Select、Click、
  Settle、Screenshot、Close 和 Detach。X11 监控证明 Codex 始终保持焦点，
  任务页没有成为可见 Active Browser Tab。
- 同一场景的显式 Handoff 变体在约 30 秒内完成。X11 监控只在精确 Handoff
  Marker 之后观察到所请求任务页成为系统活动焦点窗口。
- 固定只读邮箱资格验证在约 112 秒内依次通过 QQ 邮箱、Outlook 和 Gmail
  三个已登录账户。焦点监控未发现浏览器激活，也没有运行 Send Handler。
- 每次 Detach 后 Browser 和 Controller 均保持运行；MCP/CLI Runtime Directory
  为空，也没有残留 Playwright 子进程。
- 重启两个 User Service 后，浏览器认证和已保存 Bridge Pairing 均保留，
  Task/Session Generation 同时失效。

生产使用的 Bridge Credential 由 Bridge Profile 生成，并在持久化前完成验证。
Gateway 和 WebChat 测试覆盖 Save、失败替换、成功替换、删除、Stale-generation
拒绝、Non-prefill 和 Clear-after-save。原始 Credential 不会从状态 API 返回，
也不会写入 Compose、仓库文件、命令参数、Log、Trace 或 Artifact。

## 验证基线

已完成的切换通过：

- Browser Controller Node 测试：45；
- Browser Bridge Node 测试：25；
- 确定性 Provider Runtime 测试：3；
- 浏览器/部署 Python 测试：48，完整 Compose Python 门禁共 101；
- WebChat：519 个翻译键、28 个测试文件、88 个测试和生产构建；
- Gateway Build 和 Vet；
- 全部 Gateway Package，包括外部中央契约 Package；
- 并发密集 Gateway Race Suite；
- ASR Fake-runtime 测试：11；
- Local/Remote Compose Expansion 和 Shell Syntax；
- Remote Doctor 以及 Local/Remote Deployment Preflight；
- Local/Remote 两个私有环境的 Browser Setup Check；
- 通用后台、显式 Handoff 和三账户邮箱真实 Probe；
- 残留 Process、Session Directory、Legacy Runtime 和 Forbidden Flag 检查。

Local Preflight 已验证 Linux/ARM64、NVIDIA GB10、121 GiB Memory、Model-cache Capacity、
Docker、Permission、Compose 与 Browser Bridge，且未修改 Container。Remote Preflight
已验证五个公网 Model Endpoint、五个 Application Service、Compose 与 Browser Bridge。

恢复预期的同级 InfiniCenter Checkout 后，`go test ./...` 已全部通过，包括
`internal/contracttest`。ProjectGroup-2 Inbox 和 Proposed Decision 均没有待处理的
SparkClaw 工作。本次迁移不改变跨项目 Contract；SparkClaw Status Broadcast 已记录
完成切换，并明确其他成员项目无需跟进。

## 维护规则

- 不得重新加入 Host-CDP、browserd、`agent-browser`、容器 Chromium、Profile
  复制、Storage Export 或自动 fallback。
- 不得弱化 Task-tab Ownership，也不得允许任意 Selector、JavaScript、
  Playwright Code、CLI Command、Extension Endpoint 或 Executable Path。
- 每次 Bridge Release 必须同步提升 Package Version 和版本化 Service Worker
  入口，并重新生成 Source Checksum Closure。
- Loaded Bridge Version Gate 必须先于 Controller Health Check。
- 邮箱 Probe 只用于资格验证。真实 Send 仍要求现有 Exact-content Confirmation，
  并保持 One-attempt 和 Unknown-outcome 语义。
- 绝不读取、输出、导出或复制浏览器认证或 Bridge Credential。
