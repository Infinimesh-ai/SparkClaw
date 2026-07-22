# Agent-Browser 浏览器自动化迁移设计

> 语言：[English](../../docs/agent-browser-automation-migration.md) | 简体中文

状态：待实施的设计契约。本文不表示运行时代码已经完成替换。

本文规定 SparkClaw 如何使用
[vercel-labs/agent-browser](https://github.com/vercel-labs/agent-browser)
替换 Playwright 浏览器执行后端，同时不改变 Router-first 能力架构、ToolHub
公共契约、托管 Profile 所有权、审批策略和浏览器 Workflow 语义。

本设计基于 agent-browser `v0.32.3`、提交
`81c336c1c20b80ac648e0416a7b6e0c0ae7878bb` 完成评估。实施时必须锁定经过
验证的精确版本，禁止安装或执行无版本边界的 `latest`。

相关契约：

- [架构](architecture.md)
- [浏览器自动化改进计划](browser-automation-improvement.md)
- [浏览器交互 Workflow](browser-interaction-workflow-proposal.md)
- [浏览器感知可靠性优化](browser-perception-reliability-design.md)
- [托管共享 Chromium Profile](managed-persistent-browser-profile.md)
- [Playwright 浏览器自动化迁移](playwright-browser-automation-migration.md)

Playwright 迁移文档仍是当前实现的历史依据。只有在本文的迁移验收条件全部
通过后，本文才取代其中关于执行后端的决策。

## 决策

SparkClaw 将以 agent-browser 作为唯一的生产浏览器执行后端。
agent-browser 通过 Rust CDP daemon 启动并控制本地 Chrome/Chromium。
SparkClaw 启动一个启用 `core` 工具集的 agent-browser MCP stdio 服务作为内部
子进程，并通过它执行浏览器操作。

```text
Fast 语义路由
  -> 版本化浏览器 Workflow
      -> ToolHub、Policy、Approval、Audit 和 Trace
          -> browserautomation.Adapter
              -> AgentBrowserAdapter
                  -> agent-browser mcp --tools core
                      -> 隔离的 agent-browser daemon/session
                          -> SparkClaw 托管的 Chrome Profile
```

在本设计中，MCP 只是 Adapter 的内部传输协议。agent-browser 工具不会被直接
注册到 ToolHub，不会暴露给 Fast 或 Deep，也不会成为第二套模型可见能力注册表。

最终实现会删除 Playwright 运行时依赖、内嵌 Node driver、Playwright Adapter
和 Playwright 专用安装流程。生产环境不会长期保留两个浏览器后端，也不会在
agent-browser 失败时静默回退到 Playwright。

## 保持不变的架构边界

本次迁移改变执行机制，不改变产品架构。以下边界保持权威且与 provider 无关：

- Fast 只能选择已注册的语义能力叶子。
- 版本化 Workflow 负责阶段顺序和模型可见工具集合。
- ToolHub 是浏览器工具唯一的注册和执行边界。
- Policy 与 Approval 继续对有后果的操作拥有最终决定权。
- 每次浏览器操作继续生成现有 ToolCall、审计、Trace、结果和 Artifact 记录。
- 浏览器观测继续作为不可信外部证据处理。
- `browserautomation.Adapter`、`Result` 和 `PageReadResult` 继续作为 Go
  侧所有权边界，除非发现与 provider 无关的契约缺陷。
- 公共 `browser.*` 名称、输入输出 Schema、风险等级和审批语义保持不变。
- `browser.automation r1` 与 `browser.interaction r1` 保持现有路由和阶段契约。
- 登录、验证码、短信、2FA、支付和安全操作继续使用可见人工接管契约。

任何 Workflow 都不能调用任意 agent-browser 命令，只允许
`AgentBrowserAdapter` 内显式声明的映射。

## 为什么选择 MCP 传输

agent-browser 同时提供 CLI、MCP stdio 服务和内部 client-daemon 协议。
SparkClaw 选择 MCP stdio，原因如下：

- 它是公开、有类型、具有显式初始化和错误语义的接口；
- 长生命周期子进程避免每次浏览器调用都启动新 shell 进程；
- `core` 工具集包含导航、快照、交互、等待、截图、标签页、执行脚本和关闭；
- SparkClaw 不需要依赖私有 daemon socket 协议；
- 请求 ID 可以严格匹配响应并拒绝迟到响应。

Adapter 禁止解析面向人的 CLI 输出，也不通过 shell 字符串启动命令。它使用参数
数组直接启动配置的可执行文件：

```text
agent-browser mcp --tools core
```

Go 侧只实现 Adapter 需要的最小 MCP 生命周期：

1. 使用有界 stdout、stderr 和可取消 Context 启动进程；
2. 发送 `initialize`，校验协商协议版本和服务身份；
3. 获取并校验所需 core 工具 Schema；
4. 使用单调递增 ID 发送 `tools/call`；
5. 区分 JSON-RPC、MCP tool、agent-browser 业务错误和进程错误；
6. 终止子进程前关闭当前拥有的 session。

该 MCP client 不是项目级通用 MCP 框架。在出现第二个真实生产使用者之前，它只
存在于 `internal/browserautomation` 内部。

## 依赖与配置

根 package manifest 将用精确版本的 agent-browser 依赖替换 Playwright 依赖。
`npm run setup:browser` 调用本地锁定版本的 agent-browser，安装其兼容的 Chrome
for Testing。`scripts/doctor.sh` 必须验证：

- 实际解析到的 agent-browser 版本与锁定版本一致；
- 当前主机架构上的 native binary 可执行；
- 配置的 Chrome 可执行文件可用，或已安装托管 Chrome for Testing；
- 有界的 headless 启动、快照、关闭 Smoke 测试成功；
- Smoke 结束后不存在残留的 SparkClaw 浏览器 session。

目标 Adapter 配置只在现有 Adapter 边界下包含 provider 细节：

```json
{
  "tools": {
    "browserAutomation": {
      "enabled": true,
      "provider": "agent-browser",
      "profile": "default"
    }
  },
  "adapters": {
    "browserAutomation": {
      "command": "agent-browser",
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "daemonIdleTimeoutMs": 60000,
      "chromiumExecutable": "",
      "profileDir": "./data/browser-profiles"
    }
  }
}
```

默认命令解析器优先使用工作区中锁定版本的二进制。打包部署可以显式配置
`command`，但启动时必须解析路径并校验版本。模型或工具参数不能覆盖命令、MCP
工具集、浏览器可执行文件、Profile 根目录、namespace、provider、CDP URL、
extension、恢复状态或启动参数。

实现会删除 `nodeCommand`、`SPARKCLAW_BROWSER_RUNTIME_DIR`、Playwright 依赖、
`PLAYWRIGHT_BROWSERS_PATH` 和 Playwright 安装检查。现有面向运维的 Profile 与
Chromium 覆盖配置可以保留 provider-neutral 名称：

- `SPARKCLAW_BROWSER_CHROMIUM_EXECUTABLE`
- `SPARKCLAW_BROWSER_PROFILE_DIR`
- `SPARKCLAW_BROWSER_AUTOMATION_TIMEOUT_MS`

如确实需要新增命令覆盖，名称为
`SPARKCLAW_BROWSER_AUTOMATION_COMMAND`。不提供用于注入任意 agent-browser
参数的环境变量。

## 进程、Session 与 Profile 所有权

`AgentBrowserAdapter` 与当前 Adapter 一样串行化浏览器操作。它最多拥有一个
活动元组：

```text
(owner_id, browser_profile_id, presentation)
```

针对该元组，Adapter 派生：

- 与当前 SparkClaw Gateway 实例隔离的 agent-browser namespace；
- 根据 owner、逻辑 Profile 和 presentation 哈希得到的不透明 session 名；
- SparkClaw 托管的绝对 Profile 路径
  `<profile-root>/<owner-hash>/<profile-hash>/user-data/`。

namespace 和 session 名禁止包含原始 owner 标识。

MCP 进程只能接收 Adapter 自己控制的环境变量和参数。Session 使用托管 Profile
路径、可选的已校验 Chromium 可执行文件、有界输出和一个固定 presentation：

| Presentation | agent-browser 启动行为 |
|---|---|
| `hidden` | 默认 headless Chrome，固定 `1365x768` viewport |
| `visible` | `headed=true`，本地可见窗口 |

切换 owner、逻辑 Profile 或 presentation 时执行独占交接：

1. 使所有 page 和 snapshot 句柄失效；
2. 对当前拥有的 session 调用 `agent_browser_close`；
3. 终止并回收 MCP 子进程；
4. 等待托管 Profile 锁释放；
5. 启动并初始化新的 MCP 子进程；
6. 使用相同或新的托管 Profile 启动替代 session。

hidden 和 visible session 不能并发拥有同一个 Profile。SparkClaw 禁止使用
agent-browser `--auto-connect`、`--cdp`、日常 Chrome 的命名 Profile、状态导入、
`--restore`、auth vault 或用户提供的浏览器参数。持久化 Profile 本身继续作为
登录状态的事实来源。

Gateway 关闭时只关闭自己派生的 session，禁止使用 `close --all`。有界的
agent-browser idle timeout 只用于防御 Gateway 非正常退出，不能替代显式关闭和
进程回收。

## 公共工具映射

Adapter 只在初始化时校验一次 MCP 工具列表，并且只能调用以下固定映射：

| SparkClaw 工具 | agent-browser core 操作 | Adapter 职责 |
|---|---|---|
| `browser.status` | initialize、`agent_browser_open`、`agent_browser_get_url` | 延迟启动 `about:blank`，报告版本和 session 健康状态 |
| `browser.list_tabs` | `agent_browser_tab_list` | 将稳定 `tN` 归一化为 `page_N` |
| `browser.open` | `agent_browser_open` 或 `agent_browser_tab_new` | 复用唯一空白页或新建标签页，并校验最终 URL |
| `browser.focus` | `agent_browser_tab_switch` | 只解析当前 Adapter 的 page ID |
| `browser.close` | `agent_browser_tab_close` | 关闭单个标签页并选择有效剩余页面 |
| `browser.navigate` | open/back/forward/reload core 工具 | 只映射已声明的导航模式 |
| `browser.snapshot` | `agent_browser_snapshot` | 生成 SparkClaw 快照和私有 ref 映射 |
| `browser.screenshot` | `agent_browser_screenshot` | 归一化 ArtifactStore 需要的图片证据 |
| `browser.wait` | wait-for-text/selector/load 或有界毫秒等待 | 拒绝不支持的等待参数组合 |
| `browser.click` | `agent_browser_click` | 将一个当前 SparkClaw ref 转为私有 `@eN` |
| `browser.type` | `agent_browser_fill` 或 `agent_browser_type` | 保持 fill 与当前焦点 type 的语义 |
| `browser.select` | `agent_browser_select` | 转换一个当前 SparkClaw ref 和 value |
| `browser.read` | open 加固定的 `agent_browser_eval` | 保持渲染 HTML 和 Readability 证据流 |

Page ID 继续与 provider 无关。模型或工具输入不能直接提交 agent-browser tab ID。
打开、切换、关闭、导航和意外新标签页都会重新对齐 page map，并使受影响快照
失效。

如果必要 MCP 工具缺失，或其必要字段与锁定契约不兼容，Adapter 必须启动失败。
禁止按相似名称寻找替代工具，也禁止用原始 `extraArgs` 作为兼容回退。

## 浏览器读取与证据

`browser.read` 不会把产品语义委托给 `agent_browser_read`。SparkClaw 必须保留
现有渲染页面与证据契约：

```text
agent_browser_open / agent_browser_tab_new
  -> 有界渲染稳定等待
  -> agent_browser_eval 执行 SparkClaw 自有固定读取函数
      - 渲染后的 outerHTML 和可见文本
      - title、URL、language、content type 和 ready state
      - 长度与截断诊断
      - 登录状态信号
  -> ToolHub Readability 提取
  -> 不可信 Observation 和可选 Artifact 归档
```

只有内嵌的固定读取函数可以调用 `agent_browser_eval`。模型文本、页面文本和普通
工具参数不能成为 JavaScript 源码。

Direct HTTP fallback、URL 安全校验、本地主机 allowlist、Readability、截断、登录
状态评估和 Artifact 行为继续位于 agent-browser 之外，并保持现有契约。

## 快照与 Ref 安全

agent-browser 快照包含文本无障碍树和 `e1` 等短 ref 的结构化映射。这些 ref 是
session 内部执行句柄，不是 SparkClaw 标识。agent-browser 每次生成快照都会重建
ref map，因此后续快照中的同一个 `@eN` 可能已经指向另一个元素。

Adapter 必须保留 SparkClaw 更严格的快照契约：

1. 请求 interactive 且包含 URL 信息的 agent-browser 快照；
2. 校验结构化 `refs` 对象和有界 tree payload；
3. 生成绑定活动 page ID 与 URL 的新 SparkClaw `snapshot_id`；
4. 根据 role、accessible name、tree context、可用 URL 和重复序号生成有界控件
   描述与语义指纹；
5. 私下保存每个 SparkClaw ref 到当前原始 `@eN` 的映射；
6. 只向 Workflow 和模型返回带指纹的 SparkClaw ref；
7. click/type/select 前严格校验 snapshot ID、page ID、ref、指纹、活动标签页和
   尚未执行动作的状态；
8. 一次变更动作成功后，或发生后续快照、导航、标签页、Profile、presentation
   变化后，立即使映射失效。

边界示例：

```text
模型可见：
  snapshot_42:e5:8bc8f32d...

Adapter 内部有效至失效前：
  snapshot_42:e5:8bc8f32d... -> @e5
```

Adapter 不能对 SparkClaw ref 使用 agent-browser 的 selector fallback。未知、过期、
脱离 DOM、重新编号、歧义、隐藏、被遮挡或禁用目标必须显式失败，并要求重新执行
`browser.snapshot`。

这条规则专门阻止以下过期 ref 故障：快照 A 中 `@e5` 是“草稿箱”，新快照把
`@e5` 重新编号为“收件箱”，迟到的点击却激活了收件箱。SparkClaw 必须在请求到达
agent-browser 前拒绝快照 A 的 ref。

现有相关度排序和有界控件投影继续由 SparkClaw 负责。完整原始树可以作为不可信
证据归档，模型可见投影保持现有大小限制。如果锁定版本的 agent-browser 结构化
快照缺少必要语义，Adapter 必须关闭失败，禁止从任意自然语言输出中猜测。

## 登录接管

产品层的协作式登录流程保持不变：

```text
hidden session 检测到必须人工处理的挑战
  -> 快照失效并关闭 hidden session
  -> 使用同一个托管 Profile 打开 visible session
  -> 用户在聊天外完成密码/验证码/2FA
  -> 捕获用户选中的登录后 URL
  -> 关闭 visible session 并释放 Profile
  -> 使用同一个 Profile 重新以 headless 启动
  -> 导航到选中 URL，重新评估登录状态并继续
```

SparkClaw 不复制 Cookie，不在聊天中索要凭据，也不把文本声明作为已登录证据。
Profile lock 失败必须显式返回，禁止通过删除 Chromium lock 文件处理。

## 安全与策略

以下限制为强制要求：

- 只启用 `core` MCP 工具集；
- Adapter 代码只能调用上文固定映射；
- 拒绝任意 CLI `extraArgs`、CSS/XPath selector、CDP endpoint、auto-connect、
  provider plugin、extension、state replay、auth vault 和 agent-browser chat；
- 导航前执行公共 URL 校验；
- Profile 根目录和 Chromium 可执行路径由配置拥有并完成规范化；
- stderr 和 MCP payload 有界，写入 Trace 或 Error 前完成脱敏；
- 截图和原始快照通过 ArtifactStore 与现有脱敏规则处理；
- 变更操作禁止自动重试；
- agent-browser 的确认提示不能取代 SparkClaw Approval。

agent-browser 的 `allowed-domains` 启动选项不能作为本次迁移的通用策略层，因为它
的隔离模式会主动拒绝 Chrome Profile 复用和 restore/state replay。SparkClaw 为
保持登录连续性需要持久化托管 Profile，因此继续以现有 URL 与网络信任校验为
权威。未来无状态浏览器 Profile 可以在独立版本化契约下使用 agent-browser 域名
隔离作为纵深防御。

## 错误、超时与恢复

Adapter 至少区分以下错误类别：

- executable/version/setup 不可用；
- MCP 初始化或 Schema 不兼容；
- daemon/session 启动失败；
- 托管 Profile 被锁定；
- 请求超时或 Context 取消；
- MCP 响应格式错误或超限；
- agent-browser 进程崩溃或 EOF；
- 导航或业务失败；
- SparkClaw 快照 ref 过期或无效；
- 需要人工验证。

每个 MCP 请求使用调用方 deadline 或配置的 Adapter timeout。超时、格式错误、ID
不匹配、意外 EOF 或 MCP 进程崩溃会使全部 session/page/snapshot 状态失效。恢复
流程在可行时通过独立有界命令关闭当前拥有的 session，终止 MCP 进程组，并允许
后续调用启动干净 session。

只读启动失败可以由后续调用恢复。Adapter 禁止自动重试 click、fill、type、
select、submit 或其他变更交互。已放弃进程的迟到响应不能满足新一代进程中的请求。

## 预期行为变化

公共 Tool 与 Workflow 契约保持不变，但以下实现细节对外可观察：

- `provider` 从 `microsoft-playwright-*` 变为 `agent-browser-*`；
- Setup 和 Doctor 输出从 Playwright/Playwright Chromium 变为 agent-browser/
  Chrome for Testing；
- 底层动作错误文案变化，但会归一化为稳定 SparkClaw Error Code；
- 快照树的文本格式可能变化，但结构化 SparkClaw 快照 Schema 和过期 ref 保证
  保持稳定；
- 动作就绪性由 agent-browser CDP 行为提供，而不再由 Playwright Locator
  actionability 提供。

文档和测试不应承诺 provider 文本逐字节相同。行为测试应断言稳定的 SparkClaw
契约和类型化结果。

## 迁移顺序

### 阶段 0：冻结契约

1. 合入本文及中文镜像。
2. 记录现有浏览器单元测试、Fixture、Golden、可见登录和真实 Chromium 基线。
3. 在切换 provider 前加入 ref 重新编号回归 Fixture。

### 阶段 1：依赖与传输

1. 在根 manifest 和 lockfile 中锁定已验证的 agent-browser 版本。
2. 更新 `setup:browser`、Doctor、Docker image、Compose 和部署文档。
3. 增加私有有界 MCP stdio client 与协议 Fixture 测试。
4. 增加显式版本与必要 Tool Schema 校验。

### 阶段 2：Adapter 对齐

1. 在现有 `Adapter` 接口下实现 `AgentBrowserAdapter`。
2. 实现 session、page、Profile、presentation、read、snapshot、screenshot、
   interaction、timeout 和 close 的归一化。
3. 保留 SparkClaw snapshot ID 与私有 raw-ref 映射。
4. 使用新 Adapter 运行 provider-neutral ToolHub 与 Workflow 测试。

### 阶段 3：切换

1. 把默认 provider 改为 `agent-browser`。
2. 运行完整浏览器 Fixture 与真实托管 Chrome 矩阵。
3. 在 Host 和 Docker 中验证 hidden/visible Profile 接管与进程清理。
4. 更新架构、开发、浏览器路线图、部署和运维文档。

### 阶段 4：移除 Playwright

1. 删除 `PlaywrightAdapter`、Playwright stdio 代码和内嵌
   `playwright_driver.cjs`。
2. 删除 Playwright package、浏览器安装、环境变量、Docker layer、provider
   字符串和 Playwright 专用测试。
3. 只保留 provider-neutral 测试、agent-browser 协议测试和真实浏览器测试。
4. 确认生产代码不再引用 Playwright。

对齐阶段的临时开发分支可以提供仅测试用的 provider 切换。最终合入状态只有一个
生产 provider，不存在掩盖 agent-browser 失败的运行时回退。

## 验证矩阵

必须提供以下自动化证据：

| 范围 | 必要证据 |
|---|---|
| MCP 生命周期 | initialize、Tool 校验、请求 ID、Tool Error、Timeout、EOF、stderr 上限、Close |
| 映射 | 每个公共浏览器 Tool 只映射到一条声明的 Adapter 路径 |
| 标签页 | Open/List/Focus/Close、Popup 对齐、稳定 Page ID |
| 快照 | 有界控件、相关度排序、指纹、重复名称、Iframe 行为 |
| Ref 安全 | 重新快照编号、过期 ID、错误 Page、重复 Action、导航后失效 |
| Read | 渲染 HTML/Text、Readability、Lazy Content、Truncation、Auth Signals |
| 交互 | Click/Fill/Type/Select、遮挡目标、脱离目标、动作后失效 |
| Profile | Owner 隔离、逻辑 Profile 隔离、Lock 处理、持久化 |
| Presentation | Hidden/Visible 互斥和登录接管 |
| 恢复 | 挂起调用、Daemon 崩溃、Gateway 关闭、无残留 Chrome |
| 安全 | URL 拒绝、无 Raw Args/Selector/CDP/State Import、脱敏 |
| 产品 | 现有 ToolHub、Workflow、Gateway Golden 和 WebChat 行为 |

真实浏览器验收除现有托管 Chromium 场景外，还必须包含一个 Fixture：在两次快照
之间故意把相同原始 `@eN` 重新编号到不同元素。旧 SparkClaw ref 必须在
agent-browser 收到 Click 前失败。

## 验收条件

以下条件全部满足后才算迁移完成：

- agent-browser 已锁定版本，并可在支持的 Host 与 Container 架构上重复安装；
- Playwright 不再承担生产依赖、Adapter、Driver、Setup、Environment、Provider
  String 或浏览器二进制职责；
- 所有公共 `browser.*` ToolHub Schema、风险等级和 Workflow 暴露保持不变；
- Fast 继续路由到相同的已注册浏览器能力叶子；
- Browser Read、Tab、Snapshot、Screenshot、Wait、Click、Type 和 Select 通过
  provider-neutral 测试；
- 原始 agent-browser ref 永不穿过 Adapter 边界；
- 成功动作会使快照失效，过期或重新编号的 ref 会关闭失败；
- Hidden 与 Visible 只以串行方式复用同一个托管 Profile；
- 登录状态能够持久化，且无需导出 Cookie 或凭据；
- Timeout 和 Shutdown 后没有 SparkClaw 拥有的 MCP 或 Chrome 进程；
- Gateway 全量测试、WebChat 测试与构建、Doctor、Mock Golden Eval、文档镜像、
  Docker Config 和真实浏览器 Smoke 全部通过。

## 回滚

回滚以 Release 为单位，不是自动运行时 fallback。第一个 agent-browser Release
期间保留上一版已验证的 Playwright Release Artifact，并保持状态 Schema 兼容。
如出现阻塞回归，先停止 Gateway，确认托管 Profile 未被 agent-browser session
占用，再使用同一 Profile 根目录部署上一版本。

禁止让 Playwright 与 agent-browser 同时操作同一个托管 Profile，禁止通过删除
Profile lock 强制回滚。

## 上游资料

- [agent-browser 仓库](https://github.com/vercel-labs/agent-browser)
- [agent-browser MCP 服务](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#mcp-server)
- [agent-browser Session 与 Profile](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#authentication)
- [agent-browser 快照选项](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#snapshot-options)
- [agent-browser 架构](https://github.com/vercel-labs/agent-browser/blob/81c336c1c20b80ac648e0416a7b6e0c0ae7878bb/README.md#architecture)
