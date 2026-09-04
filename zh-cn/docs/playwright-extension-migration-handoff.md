# Playwright Extension 迁移交接文档

> 语言：简体中文 | [English](../../docs/playwright-extension-migration-handoff.md)

## 状态快照

本文记录截至 2026-09-04 已接受的决策和已验证的实现状态，使新的开发会话无需从早期浏览器、
Host-CDP 或邮箱讨论中推断当前方案。

权威顺序：

1. [Playwright Extension 浏览器迁移设计](playwright-extension-browser-design.md)定义目标架构和切换门槛。
2. 当前源码定义已经实现的内容。
3. [浏览器 Runtime](browser-runtime.md)描述仍在运行的 Host-CDP 生产实现。
4. 更早的对话提案和 Host-CDP 邮箱设计只属于历史背景，不是 Playwright 目标方案的权威。

## 已接受的产品决策

- 浏览器制品固定为安装在宿主机上的 **SparkClaw Chromium**。不得替换为 Chrome 或 Edge，
  不得在 Gateway 中下载 Chromium，也不得在产品设置中提供浏览器选择。
- Browser Channel Identity 必须是 `chromium`。这是内部实现事实，不是 Owner 可选设置。
  Executable Path、Channel Identity 和 Profile Identity 必须一致。
- 最终产品使用一个固定、持久的 SparkClaw 浏览器 Profile。Owner 可以日常使用该浏览器；
  SparkClaw 只在有界任务期间连接、新建任务自有标签页，并在结束后断开。
- 后台任务不得激活浏览器窗口、抢夺焦点、替换当前 WebChat 标签页或使用 Owner 已打开的
  标签页。只有 Owner 显式 Handoff 才允许把指定任务标签页切到前台。
- 通用模型引导浏览通过现有 Provider-neutral 浏览器接口使用 Playwright MCP。确定性的
  Provider 登录探针和 Effect 使用仓库拥有的固定 Playwright CLI 脚本。模型只选择功能并
  提供语义值，不得编写浏览器动作、Selector、JavaScript 或 CLI 命令。
- 浏览器认证信息只保留在浏览器 Profile。SparkClaw 不复制 Cookie、Storage State、密码、
  Passkey、浏览器数据库或 Profile。
- Playwright Extension Credential 只在 Browser control 中配置一次，加密保存在 Gateway
  Credential Vault，邮箱 Provider 不重复保存。Provider 配置只保存 Provider/Account 状态。
- 官方 Playwright Extension 只作为资格验证基线。固定版本上游扩展可以连接全部已知 Page，
  并可能触发前台聚焦，因此只能用于一次性资格验证 Profile。生产必须使用独立打包的
  SparkClaw Browser Bridge，在 Attachment 前执行 Task-Tab Allowlist，并实现后台不抢焦点。
- 全部迁移门槛通过前，Host-CDP 保持生产路径。最终切换必须是原子的：Playwright Extension
  成为唯一 Transport，同时删除 browserd、Host-CDP、`agent-browser`、相关配置、测试、打包和
  fallback 文案。切换后不保留长期兼容 fallback。

## 已实现并验证

生产执行路径**尚未迁移**，当前仍为：

```text
浏览器和邮箱 Workflow
  -> agent-browser 0.32.3
  -> 受保护的 Host-CDP Endpoint
  -> sparkclaw-browserd
  -> 宿主机 SparkClaw Chromium
```

Playwright 预览控制面已经实现：

- Owner-scoped `sparkclaw-browser-controller.service`；
- Owner-only Unix Socket 和有界 Controller Protocol；
- 加密的 `playwright-extension-token-v1` Vault Credential；
- 经过认证且脱敏的 Gateway 状态、保存、检查和删除 API；
- `Settings > Connections > Browser control` WebChat 界面；
- 一次性 `extension-qualification` Chromium Profile；
- 有界任务页面创建、关闭、Detach、子进程终止和回收；
- 固定官方 Extension `0.4.0`、Playwright MCP `0.0.80`、Playwright
  `1.63.0-alpha-2026-08-31` 和 Chromium `148.0.7778.0`。

已保存的真实 Credential 已通过安装后的 systemd User Service 完成新鲜握手。Gateway 和
Controller 只暴露 Generation、版本、时间戳和类型化状态；原始 Credential 不进入 Compose
或仓库文件，新的开发会话也不得从聊天记录复制该值。

资格验证浏览器 Profile：

```text
~/.local/share/sparkclaw/browser/extension-qualification/user-data
```

Controller 必须使用 `--browser chromium` 调用 Playwright MCP。把固定 Chromium 制品声明为
`chrome` 会选择错误的 Linux 启动路径，使短生命周期连接页面在 Ubuntu AppArmor 下以
`No usable sandbox` 失败。默认值、生成的 systemd Unit、安装检查和测试现在都约束
`chromium` Identity。

当前同时运行 `sparkclaw-browserd.service` 和
`sparkclaw-browser-controller.service` 是设计内状态。Browserd 负责生产 Host-CDP 执行；
Controller 只负责 Preview。两条路径互不 fallback。

## 尚未完成的迁移工作

阶段状态：

| 阶段 | 2026-09-04 状态 |
|---|---|
| 0. 兼容性 PoC | MCP 握手已证明；独立 CLI 连接、真实 Gmail 手动登录保持、Token 轮换和一个 Client 对应一个 Tab 仍需真实资格验证 |
| 1. Host Controller | Preview 控制面基本完成并已真实验证 |
| 2. 通用 MCP Adapter | 未实现；生产浏览器工具仍实例化 Host-CDP `agent-browser` Adapter |
| 3. 确定性 Provider 脚本 | 未迁移；QQ 邮箱、Outlook 和 Gmail 脚本仍要求 `AGENT_BROWSER_CDP` 并执行 `agent-browser` |
| 4. SparkClaw Browser Bridge | 未开始；官方扩展仍只用于资格验证 |
| 5. 部署资格验证 | Playwright 生产路径尚未开始；Local 和 Remote 启动仍要求 Host-CDP |
| 6. 原子切换与删除 | 未开始；browserd、Host-CDP 和 `agent-browser` 仍是生产必需组件 |

下一项实现任务是 Phase 2，不是邮箱工作，也不是删除旧 Runtime：

1. 在当前 `browserautomation.Adapter` 边界后实现 Provider-neutral Playwright Extension Adapter。
2. 资格验证期间保持 Host-CDP 为显式默认值，不增加自动 fallback。
3. 映射现有浏览器 Tool Surface，不改变公开 ToolHub、Workflow、Policy、Approval、Evidence、
   Snapshot、Ref、Settle 或类型化 Failure 契约。
4. 每次 List、Read、Snapshot、Focus、Click、Close 和 Cleanup 前都保持 Task-Tab Ownership。
5. 对两个实现运行现有 Adapter Characterization Suite，并增加 Controller-backed Fake 和真实场景。
6. 只有通用 Adapter 通过后，Phase 3 才把三个邮箱 Provider 的 Host-CDP 脚本替换为固定
   Playwright CLI 脚本。
7. 完成 Browser Bridge 和部署资格验证后，才能修改生产 Selector 或删除旧实现。

## 新开发会话的约束

- 开始时先阅读本文、目标设计、当前 Browser Runtime、实际配置与 Assembly 代码。
- 当前 Worktree 包含大量未提交的浏览器、邮箱和部署整合改动，不得 reset、丢弃或覆盖无关改动。
- 不得把 Preview Credential 的 `Ready` 状态解释为生产切换完成。
- 不得在日常或生产账号 Profile 使用官方扩展；它不是 Per-Tab Privacy Boundary。
- 不得增加容器 Chromium、Xvfb、Profile 复制、Cookie 导出、永久 CDP、任意 Playwright 代码或
  模型编写的 Selector。
- 不得把 Browser Channel、Executable Path、Profile Path、Extension Token、Controller Socket
  或 Transport 内部参数暴露为模型可控参数。
- Phase 6 门槛通过前，不得删除 Host-CDP 或 `agent-browser`。
- Extension Credential 绝不能进入 Shell Argument、Environment File、Repository File、Log、
  Trace、Artifact、Model Context 或 Test Fixture。

## 验证基线

当前 Preview 基线已经通过：

- Browser Controller Node 测试；
- Host-CDP、Controller 和部署 Python 测试；
- Gateway Browser-control、Browser-automation 测试及聚焦 Race 测试；
- 三个邮箱 Provider 脚本测试；
- WebChat 测试和生产 Build；
- Local/Remote Compose 展开、Remote Doctor、Shell 语法、中英文 Markdown Mirror/Link、
  `git diff --check` 和改动文件密钥扫描；
- 真实 systemd Extension 握手、Gateway 重启持久性、Controller/Chromium 存活以及子进程
  Orphan 检查。

本机执行 `go test ./...` 时，`internal/contracttest` 存在一个仅由环境导致的失败：所需的同级
InfiniCenter 仓库和 `SparkClaw--JingSi` 中央 Conformance Manifest 不存在。其余 Gateway
Package 均通过。新的开发会话必须区分该协调仓库缺失与 Playwright Regression；InfiniCenter
可用后，必须处理其 Inbox、Decision、Contract 和 Status。
