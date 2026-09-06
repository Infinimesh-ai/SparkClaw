# Playwright 扩展浏览器迁移设计

> 语言：简体中文 | [English](../../docs/playwright-extension-browser-design.md)

## 状态

本文于 2026-09-04 提出，并于 2026-09-05 完成。全部阶段和切换门槛均已实施。
固定校验和的 SparkClaw Browser Bridge `1.0.18`、固定 Chromium
`148.0.7778.0`、Owner-scoped Controller、Playwright MCP 和 Playwright CLI
现已组成唯一生产浏览器 Runtime。browserd、Host-CDP、`agent-browser` 和 Migration
Selector 均已删除。

最终真实资格验证覆盖后台不抢焦点、显式任务标签页 Handoff、通用表单交互、重启与
Detach 清理，以及 QQ 邮箱、Outlook 和 Gmail 三个已登录账户的只读 Probe。过程中没有
复制或导出浏览器认证或 Bridge Credential，也没有调用邮件 Send。

新的开发会话开始修改前，应先阅读包含当前状态基线和精确开发起点的
[Playwright Extension 迁移交接文档](playwright-extension-migration-handoff.md)。

本文是 Playwright 实现及其迁移门槛的规范记录。
[浏览器邮箱 Workflow 设计](browser-email-workflow-design.md)与
[浏览器 Runtime](browser-runtime.md)描述切换后的当前实现。本文保留的 Host-CDP 内容
只用于解释已退役的源架构和切换原因。

迁移遵循固定的扩展实现顺序。SparkClaw 首先集成未经修改的官方 Playwright Extension，
验证浏览器、Controller、MCP、CLI、设置、Credential 和任务标签页契约。在这些产品契约
完成并冻结后，SparkClaw 再基于上游源码派生独立打包的扩展，实现后台不抢焦点和显式
Handoff 行为。官方扩展仍是集成基线，不是生产扩展。

## 决策摘要

SparkClaw 已验证一套 Playwright Extension 架构：Owner 日常使用正常启动的浏览器，
SparkClaw 仅在拥有任务标签页时临时连接。浏览器启动时不得暴露 remote-debugging
端口，也不得增加 automation 启动参数。

当前实现把浏览器控制分为两条通道：

| 通道 | 后端 | 用途 |
|---|---|---|
| 受治理的通用浏览器 | Playwright MCP + Playwright Extension | 模型通过现有 provider-neutral 浏览器工具进行观察和有界交互 |
| 确定性 Provider 脚本 | Playwright CLI + Playwright Extension | 固定登录探针和 Effect 脚本；模型只选择功能和提供语义值，不编写浏览器动作 |

两条通道连接同一个运行中的浏览器，因此自然使用其当前 Profile、Cookie、Local
Storage、Passkey 和登录 Session，不把这些内容复制到 SparkClaw。两条通道必须使用
不同任务标签页，绝不能并发控制同一标签页。

扩展采用经历了两个明确阶段：

1. SparkClaw 设计和实现外围产品边界时，原样使用官方 Web Store 扩展。其已知的前台
   抢焦点行为只作为开发和资格验证阶段的限制接受。
2. Controller 和浏览器契约稳定后，SparkClaw 构建并验证独立分发的 **SparkClaw
   Browser Bridge**。它保持与上游 Playwright 的兼容性，同时移除无条件聚焦，允许后台
   任务标签页，并且只在 Owner 显式 Handoff 时切到前台。

API、Credential 抽象、Profile Identity、任务所有权、Provider Registry 以及 MCP/CLI
请求契约不得依赖当前安装的是哪一个扩展包。迁移到 Browser Bridge 是扩展实现替换，
不是 Gateway 或 Workflow 重构。

Playwright Library 是 MCP 和 CLI 下层的共同实现引擎。SparkClaw 未增加第三套直接
Library 后端。只有新证据证明 CLI 的进程或结果契约不足时，后续才允许用有类型的
Library 常驻 Worker 替换 CLI。

## 官方扩展的隔离限制

对固定版本官方扩展路径的源码检查发现：它会对所有已知浏览器标签页启用自动 Debugger
Attachment，为全部 Page 初始化 MCP Tab Wrapper，并可能在构建 Wrapper 时读取已有
Console 和 Request Metadata。Tab List Operation 还可能渲染全部已连接 Page 的 Header。
因此，仅新建一个 SparkClaw 任务标签页并不能让官方基线扩展成为 Per-Tab Privacy
Boundary。

这改变了资格验证姿态，但不改变生产契约：

- 官方扩展只在不包含普通 Owner 标签页或生产凭据的一次性资格验证 Profile 中使用；
- Phase 1 设置和 Controller 集成仅为 Preview，未使用 Owner 日常 Profile 验证；
- 官方资格验证期间，Gateway 只请求一个 Neutral Task Tab，且不会主动选择 Owner Tab，
  但这不足以阻止 Extension-level Attachment 或 Observation；
- 独立的 SparkClaw Browser Bridge 是生产必需项，现已在 Attachment 前执行通过验证的
  Task-Tab Allowlist，而不仅是取消前台抢焦点。

## 背景

Host-CDP 解决了容器浏览器所有权和 Profile 持久化问题，但要求 Chromium 在启动时
开放浏览器级 remote-debugging endpoint。SparkClaw 需要不同的 Owner 体验：

- 浏览器作为日常浏览器正常启动和运行；
- Owner 可以在 SparkClaw 连接前正常登录 Gmail 等网站；
- SparkClaw 新建隔离任务标签页，不接管 Owner 标签页；
- 自动化只在任务期间连接，结束后断开；
- 登录状态始终留在浏览器 Profile；
- Provider 操作保持确定性并受 Approval 治理。

Playwright Extension 是连接边界。MCP 和 CLI 是该边界的客户端，不代表不同的浏览器
Profile。MCP 提供适合模型迭代观察和操作的结构化工具协议；CLI 提供命名 Session 和
适合确定性 Provider 代码的固定脚本执行。

## 目标

1. 允许 Owner 在 SparkClaw 任务之前、期间和之后正常使用浏览器。
2. 复用当前浏览器认证状态，不导出 Cookie、密码、Token 或 Profile。
3. 移除永久浏览器级 CDP 暴露和 automation 启动参数。
4. 保持现有 `browserautomation.Adapter`、ToolHub、Workflow、Policy、Approval、
   Evidence 和公开 Failure 边界。
5. 模型权限停留在语义功能层，不允许模型输出 Playwright 代码或任意 CLI 命令。
6. 每个任务都有明确标签页 Owner、有界生命周期和确定性清理路径。
7. 通用网页探索与确定性 Provider 探针和 Effect 保持分离。
8. 只有真实浏览器、部署、邮箱和回归门槛全部通过后，才退役 Host-CDP、browserd 和
   `agent-browser`。

## 非目标

- 复制 Cookie、Storage State、浏览器数据库、密码或 Passkey。
- 绕过 Provider 安全检查、CAPTCHA、2FA 或浏览器完整性控制。
- 运行模型生成的 JavaScript 或 Playwright 代码。
- 因 Owner 暂时没有操作就自动接管其标签页。
- 给予 Gateway 对 Owner Profile 全部标签页的无限制访问。
- 让 MCP 和 CLI 同时操作同一个页面。
- 切换后长期保留 Host-CDP 作为 fallback。
- 在本文中重新设计 Provider Selector、邮件正文生成或邮箱 Workflow 语义。

## 术语与职责

| 组件 | 职责 | 不得拥有 |
|---|---|---|
| 日常浏览器 | 用户浏览、Profile、认证状态、扩展、浏览器进程 | Workflow、Approval、Provider 选择 |
| Extension Bridge | 集成阶段使用官方 Playwright Extension，生产阶段使用 SparkClaw Browser Bridge；临时连接运行中的浏览器和客户端专属任务标签页 | SparkClaw 授权或业务 Policy |
| Playwright MCP | 为通用浏览器 Adapter 提供结构化观察和动作工具 | Provider Effect 或模型生成的无限制代码 |
| Playwright CLI | 命名连接 Session 和执行仓库拥有的脚本 | 语义路由或任意模型命令 |
| Browser Host Controller | 扩展配对、子进程生命周期、私有 Transport、Health 和清理 | 页面内容决策 |
| Gateway Adapter | 标签页所有权、Snapshot Generation、归一化、Timeout 和类型化 Failure | 浏览器 Profile 或凭据 |
| Provider Registry | 固定脚本、Origin、Revision、结果 Schema 和 Deadline | 模型选择的可执行文件或 URL |
| Workflow 与 Policy | 能力选择、冻结参数、Approval、Effect 和完成 Evidence | 临时发明底层 Selector |

Browser Host Controller 是产品边界，不是第二套浏览器自动化引擎。它以桌面 Owner
身份运行并监管固定 Playwright 客户端，因为扩展连接属于宿主机浏览器 Session。
Gateway 容器通过私有、带认证、带版本的本地 Transport 与其通信。

本文中的**官方扩展**是指初期集成所用、未经修改的上游扩展。**Browser Bridge** 是指
后续由 SparkClaw 独立打包的派生版本。通用名称 **Extension Bridge** 表示两者共同的
协议边界。

## 当前生产拓扑

```text
Owner 桌面 Session
  -> 正常启动的固定 SparkClaw Chromium 制品
       -> 一个持久化的固定 SparkClaw Profile
       -> SparkClaw Browser Bridge
       -> 普通 Owner 标签页
       -> SparkClaw 任务标签页组

宿主机用户服务：sparkclaw-browser-controller
  -> 扩展配对和有界 Credential Relay
  -> 通用通道：固定 Playwright MCP Server
  -> 脚本通道：固定 Playwright CLI Session
  -> 私有、带版本的 Capability Endpoint
  -> 有界进程、Session 和标签页清理

Gateway 容器
  -> browserautomation.Adapter
       -> 通过 Host Controller 调用 allowlist MCP Operation
  -> 确定性 Provider Runner
       -> 通过 Host Controller 提交注册脚本请求
  -> Workflow -> ToolHub -> Policy -> Approval
```

已完成的 PoC 让 MCP 和 CLI 客户端以浏览器相同的宿主机桌面 Owner 身份运行，且不假设
容器能够直接发现扩展。本地连接通过后，私有 Gateway-to-controller Transport 已完成实现
和验证。

## 浏览器与 Profile 所有权

浏览器作为普通用户应用启动。扩展模式的启动契约禁止：

- `--remote-debugging-port` 和 `--remote-debugging-pipe`；
- `--enable-automation`；
- 对共享日常 Profile 使用 Headless 模式；
- 对同一 Profile 启动第二个浏览器进程；
- 为已配置账号操作使用自动化临时 Profile。

该 Transport 不要求浏览器必须带 SparkClaw 品牌。资格验证使用部署流程已经拥有并验证的
固定 SparkClaw Chromium 制品。官方扩展文档只列出 Chrome 和 Edge，因此切换前已明确
证明 SparkClaw Chromium 的兼容性。

所选 Profile 仍归浏览器所有。SparkClaw 只保存有界的 Profile Identity、Browser
Generation、Extension Pairing State 和 Readiness Metadata，绝不保存浏览器认证材料。

## 扩展配对与控制边界

扩展配对是每个浏览器 Profile 执行一次的本地 Owner 操作。扩展连接 Token 具有凭据
属性，必须：

- 使用高熵生成；
- 只通过经过认证的家庭设置界面或等价 Owner-only Bootstrap Path 输入；
- At-rest 只以经过认证的加密值保存在现有 SparkClaw Credential Vault；
- 仅在 Gateway Browser Credential Manager 和 Host Controller 内存中为一次有界连接
  尝试解密；
- 不进入 Controller 文件或重复的明文配置；
- 不进入 Gateway 公开 API、日志、Trace、Artifact、模型 Context、其他用户可见的
  Subprocess 参数或支持包；
- 无需删除浏览器 Profile 即可轮换。

Host Controller 向 Gateway 暴露另一份短期 Capability。仅持有扩展 Token 不代表获得
标签页授权；Gateway Adapter 继续独立执行任务所有权、Workflow Scope 和 Policy。

Controller 拒绝未固定版本的 Client、未配对扩展、错误桌面 Owner、同一 Profile 的多个
Controller，以及过期 Browser 或 Extension Generation。Health 只返回类型化状态，不
返回 Secret。

## 配置界面与 Secret 持久化

WebChat 在 Browser Email 之外提供一个独立 Connection Entry：

```text
Settings
`- Connections
   |- Browser control
   |  `- SparkClaw Browser Bridge
   `- Browser email
      |- QQ Mail
      |- Outlook
      `- Gmail
```

`Browser control` 拥有共享浏览器连接。三个邮箱 Entry 继续只负责 Provider Enable、打开
登录页面和 Provider Readiness，不分别保存 Extension Token。

官方扩展集成阶段的 Detail View 显示 `Playwright Extension（预览）`。生产环境现在显示
`SparkClaw Browser Bridge`，API Path 和 Provider Setting 保持不变。独立打包的扩展具有
自己的 Identity 和 Storage，因此其 Token 已作为新 Credential 重新录入；SparkClaw 没有
复制或静默复用官方扩展 Token。

Browser Bridge Detail View 包含：

- Browser/Profile Identity 和当前非 Secret 状态；
- 一个永不预填的 Password-style Token Input；
- Save or Replace、Check Connection 和 Remove Action；
- Last Successful Validation Time、Credential Generation、有界 Browser Version 和
  Stable Error Code；
- 指向获准扩展商店页面的 Owner-visible Link。

Token 保存遵循 Validate-before-persist：

1. WebChat 通过经过认证的 Control API 一次性提交 Token，不把它放入 URL、Browser
   Storage、Analytics 或 Client Log。
2. Gateway 拒绝 Unknown JSON Field、Trailing Value、Control Character、首尾空白和
   超出有界长度的 Opaque Token。
3. Gateway 只在内存中把 Candidate 交给 Host Controller。
4. Controller 执行一次有界 Extension Handshake，请求新建并关闭一个 Neutral Task Tab，
   随后 Detach。官方基线只在一次性资格验证 Profile 中运行，因为上游可能连接全部已知
   Page；生产 Browser Bridge Test 已证明 Owner Tab 从未被连接或检查。
5. 只有成功 Handshake 才以 `playwright-extension-token-v1` 封装进入现有 Credential
   Vault。失败 Candidate 不替换当前 Credential。
6. WebChat 在每个终态后清空 Input。任何 Response 都不返回提交的 Token。

经过认证的 API 为：

```text
GET    /api/browser/extension
PUT    /api/browser/extension/token
POST   /api/browser/extension/check
DELETE /api/browser/extension/token
```

Gateway 未配置认证时，这四个 Route 全部 Fail Closed。本地 No-auth 开发模式不能登记、
检查、替换或删除该 Credential。

Public State 只包含 `configured`、Connection State、Profile Identity、Credential
Generation、有界 Browser/Extension Version、Validation Time 和 Stable Error Code。
绝不包含 Token、Ciphertext、Vault Ref、Controller Capability、Endpoint Path、Process
Identifier 或 Extension Debug Detail。

替换或删除 Token 会递增 Credential Generation，阻止新 Browser Admission，Cancel 并
Detach 使用旧 Generation 的 Session，然后原子发布新状态。Paused 或 Resumed Task 不得
跨越 Credential Generation。删除 Token 会禁用新的 Browser 和 Email Automation，但
不会删除浏览器 Profile、Cookie 或登录状态。

建议的 Public State 为 `not_configured`、`checking`、`ready`、`needs_attention`、
`temporarily_unavailable` 和 `vault_unavailable`。之前的 `ready` 只代表历史 Evidence；
每个任务仍需执行自身要求的新鲜 Attachment 或 Provider Probe。

## 通用浏览器通道：MCP

通用 `browser.*` Surface 继续经过现有 provider-neutral Adapter。模型不会获得完整的
上游 Playwright MCP Catalog。Adapter 只暴露实现当前 SparkClaw 契约所需的 Operation：

- 创建、枚举、选择和关闭任务所属标签页；
- 导航、刷新和等待；
- 获取 Accessibility Snapshot 和有界可读页面内容；
- 依据新鲜 Opaque Ref 执行 Click、Fill、Type 和 Select；
- 获取当前 URL、Title 和有界 Screenshot Evidence。

Adapter 把这些调用转换为 allowlist Playwright MCP Profile，并把结果归一化为现有
SparkClaw 类型。上游工具名、原始 Locator 语法、任意 Evaluate、Tracing、Recording、
文件访问、网络拦截、Storage 修改和无限制 Playwright 代码都不向模型暴露。

MCP State 只是 Transport State。Task Identity、Page Identity、Snapshot Generation、
Content Fingerprint、Ref 过期、Approval 和 Completion Evidence 仍以 SparkClaw 为
权威。

## 确定性脚本通道：CLI

Provider 登录探针和 Effect 通过固定 Playwright CLI 执行仓库拥有的脚本。模型只能选择
已注册功能并提供 Workflow 声明的语义字段，不能选择：

- CLI 命令；
- Session Name；
- Script Filename；
- 浏览器标签页；
- URL、Selector、Locator 或 Timeout；
- Retry 或替代 Provider 实现。

Provider Registry 绑定 Script Revision、Allowed Origin、Deadline、Input Schema、
Output Schema、Risk 和 Result Verifier。Host Controller 创建唯一且有界的 CLI 连接
Session，创建一个任务标签页，只运行注册脚本，验证结果 Envelope，然后 Detach CLI
Session，并根据 Operation 契约关闭或释放任务标签页。

私有 Controller API 通过 `POST /v1/run-script` 接收精确注册的 Script ID 与 Revision，
通过 `POST /v1/open-provider-login` 接收固定 Provider Identity。两个 Route 都不接受 Gateway
提供的 Script Path、URL、Selector、Executable 或 Browser Option。当前 Registry 仅包含
QQ 邮箱、Outlook 和 Gmail 的 Probe/Send Revision 1 条目，并在 Controller 启动时计算每个
条目仓库源码闭包的 SHA-256。

CLI 子进程 Failure 只投影为固定 Command Category、类型化 Failure Class、Output Stream、
Secret Match Count 和 Residual Byte Count。Provider-owned Targetless URL Read 与 Evaluation
遇到 Execution Context Destroyed 时可以按 250 ms 间隔重试最多四次，并在每次 Evaluation
前重新校验当前 Origin。Element-targeted Click、Fill、Press 和其他 Effect Operation 不使用
该重试机制。

脚本通过 stdin 或等价私有 Pipe 接收结构化输入。邮件内容和凭据不得出现在 argv。
`run-code --filename` 只能引用仓库拥有且经过 Checksum 验证的文件。禁止 Inline 模型
生成代码和任意 `run-code` 内容。

邮件发送保持当前单次尝试 Effect 规则。一旦脚本可能已点击 Send，Timeout、Transport
丢失或缺少正向确认都属于未知终态，绝不能自动重试。

## 任务标签页、分组与并发

每个 MCP 或 CLI Connection 获得客户端专属任务标签页组。一个标签页同时只能有一个
Automation Owner：

| 分类 | 规则 |
|---|---|
| `owner` | 绝不自动读取、修改、聚焦或关闭 |
| `sparkclaw_mcp` | 仅由创建它的通用 MCP Session 控制 |
| `sparkclaw_cli` | 仅由创建它的确定性 CLI Invocation 控制 |
| `handoff` | Owner 执行显式操作时暂停自动化 |
| `released` | 不再受控，按 Owner 标签页处理 |

普通任务新建后台标签页，不选择已有 Owner 标签页。浏览器始终为 Headed 且 Owner
可见；“后台”表示标签页不抢占焦点，不代表隐藏或 Headless 浏览器。只有显式 Owner
Handoff 才可以请求前台聚焦。

MCP 和 CLI 不得并发连接同一标签页。每个 Profile 的 Admission 会串行化冲突 Provider
Effect；只有 PoC 证明不同扩展标签页组相互隔离后，才允许独立通用任务并发。初始实现
每个 Profile 同时只允许一个 Automation Client。

Owner 与任务标签页交互时，自动化暂停，当前 Snapshot 失效，恢复前必须获取新鲜状态。
Owner 无操作绝不产生对 Owner 标签页的控制授权。

## 认证与登录验证

账号登录应尽可能在没有活动自动化连接时，通过日常浏览器完成：

1. 配置界面使用已配置浏览器 Profile 打开注册的 Provider URL。
2. Owner 手动完成登录、CAPTCHA、2FA、Consent 或账号恢复。
3. SparkClaw 不观察按键，也不接收凭据。
4. 后续显式检查在同一 Profile 新建 CLI 所属后台标签页。
5. 固定 Provider Probe 用确定性 Evidence 区分已登录、未登录、证据冲突和页面变化。
6. Probe 只关闭自己的标签页，并持久化有界 Readiness Metadata。

登录验证仍属于配置和 Workflow 前 Admission，不是模型可见 Tool，也不成为 Workflow
Node。每个外部 Provider Effect 仍按 Provider 契约执行新鲜 Probe。

## 目标邮箱 Workflow 绑定

当前实现保留旧 Workflow 的以下邮箱业务不变量：

- 只支持发送；读取、搜索、回复、转发、附件和草稿管理仍不可用；
- Provider 和 Account 选择保持确定性并由 Runtime 所有；
- 登录验证仍在 Workflow 和模型 Context 之外；
- 模型只提供收件人、可选主题和纯文本正文；
- 外部 Effect 前必须对精确 Provider、Account Hint、收件人、主题和完整正文进行 Approval；
- Send 最多尝试一次，未知结果为终态且绝不自动重试；
- Script 仍为第一方、带 Revision、有界并使用严格 Schema。

以下与 Transport 绑定的旧要求被废止：

| 旧 Host-CDP 要求 | Playwright Extension 目标 |
|---|---|
| browserd 在 Headed 与 Headless Chromium 间切换同一 Profile | 一个正常启动的 Headed 浏览器持续供 Owner 使用 |
| Probe 和 Send 要求 Headless Presentation | Probe 和 Send 使用 CLI 所属的后台任务标签页 |
| Runner 接收 Host-CDP Endpoint 和 `agent-browser` Session | Host Controller 创建有界 Playwright CLI Extension Session |
| Admission 冻结 Headless Browser Generation | Admission 冻结 Provider Setting 与 Browser Credential Generation；Connection 和 Tab Generation 只在单次 Invocation 内有效 |
| Approval 后要求复用之前的 Browser Generation | Approval 后创建新 CLI Session，并在写入正文前立即重新执行确定性登录验证 |
| 邮箱配置可能隐含 Browser Process/Profile 配置 | 邮箱配置只引用共享 Browser Control Credential，不保存 Extension Token |
| 禁止 Playwright | Playwright CLI + Extension 是目标 Provider Script 的唯一后端 |

当前发送顺序为：

1. 确定性解析一个已配置 Provider 和 Account。
2. 要求共享 Browser Control Credential 处于 Ready。
3. 请求 Host Controller Attach 有界 CLI Session，新建后台 Provider Tab，运行固定 Login
   Probe，关闭该 Tab 并 Detach。
4. Workflow 创建前冻结 Provider、Account、Provider Setting Generation、Browser
   Credential Generation、Probe/Send Script Revision、Validation Time 和 Invocation ID。
5. 单节点 Workflow 收集消息字段并获得精确内容 Approval；等待期间不保持 Browser
   Session 或 Task Tab。
6. Approval 后重新检查 Provider Setting 和 Browser Credential Generation。
7. 新建有界 CLI Session 和 Task Tab，在同一 Session 内再次运行确定性 Login Probe，
   然后写入、校验并最多尝试一次 Send。
8. 校验 Provider 结果，只关闭任务 Tab，Detach 并返回有界 Receipt。可能点击 Send 后的
   任何不确定性都进入现有不可重试 Unknown Outcome。

Browser、Controller、Connection 和 Tab Generation 仍用于单次 Invocation 内的 Stale
State 检查，但不跨越人工 Approval 等待。Credential 或 Provider Setting 变化会使
Approval Binding 失效；普通 Detach 和 Reattach 本身不会要求 Owner 对未改变的消息内容
重新审批。

## Adapter 兼容契约

替换后端不得改变公开浏览器 Tool，也不得静默削弱当前安全不变量。Playwright Adapter
必须保持：

- 不依赖可见 Tab 顺序的稳定 SparkClaw Page ID；
- 受 Generation 约束的 Snapshot ID；
- 绑定到一个 Page 和一个新鲜 Snapshot 的 Opaque Element Ref；
- 上游 Ref 无害重编号时仍稳定的 Semantic Fingerprint；
- Navigation、Mutation、Handoff、Reconnect 或 Browser Generation 变化后的 Stale Ref
  拒绝；
- Rendered Content Settle 和有界 Retry；
- 在 List Limit 或 Active Tab 选择前过滤任务所有权；
- 类型化 Unavailable、Timeout、Stale、Ambiguous 和 Unknown Outcome Failure；
- 作为 Untrusted Content 处理的有界 Screenshot 和页面文本。

迁移不能只替换命令名称。现有 Adapter Characterization Test 是兼容目标。任何有意的
公开行为变化都需要单独的 Accepted Design 和 Contract 更新。

## 生命周期与清理

Host Controller 拥有全部 Playwright Subprocess 和 Session。每个 Session 包含 Profile
Identity、Lane、Task ID、Browser Generation、Start Time、Deadline 和 Cleanup State。

正常完成时按顺序：

1. 停止接收该任务的新 Action；
2. Operation 契约允许时关闭任务标签页；
3. Detach MCP 或 CLI Client，不关闭日常浏览器；
4. 终止并回收 Invocation 所属 Subprocess；
5. 删除临时 Pipe、Output File 和 Capability；
6. 记录有界且不含 Secret 的 Cleanup Evidence。

Gateway Cancel 和 Shutdown 走相同清理路径。Controller Reconciler 会回收过期 Session
和孤儿 Subprocess，但绝不关闭未分类标签页或 Owner 浏览器。浏览器重启会轮换 Browser
Generation，使全部任务授权失效，并要求重新连接和获取新鲜页面 Evidence。

## 配置形态

已实施的逻辑配置为：

```json
{
  "adapters": {
    "browserAutomation": {
      "timeoutMs": 30000,
      "startupTimeoutMs": 10000,
      "settleTimeoutMs": 15000,
      "settleQuietPeriodMs": 500,
      "settlePollIntervalMs": 100,
      "routeRebindLimit": 2,
      "playwrightExtension": {
        "controllerSocket": "/run/sparkclaw/browser-controller/controller.sock",
        "profileID": "default",
        "connectTimeoutMs": 20000
      }
    }
  }
}
```

Playwright、MCP、CLI 和 Extension 的精确版本已一并固定。拒绝 `latest`、浮动
扩展、混合 Playwright 版本、原始 Extension Token、任意 Endpoint、Executable
Override、Profile Path 和模型控制的 Option。

官方扩展版本作为已完成的集成基线固定。Browser Bridge 独立版本化并固定 Checksum；
兼容性测试已证明相同的 MCP 和 CLI 协议行为。

Extension Token 有意不出现在该 JSON Configuration。通过 WebChat 输入的 Token 保存于
加密 Credential Vault，API 只投影脱敏状态。Controller 仅在验证或打开按需 Playwright
Connection 时，通过带认证的私有 Transport 接收解密值。

Migration Selector 已在切换时删除。嵌套的 `playwrightExtension` Block 只登记私有
Controller Socket、固定 `default` Profile Identity 和连接 Timeout。遗留 Host-CDP
环境配置返回已记录的 Migration Error，且不能建立 Runtime Fallback。

## 部署与打包

Local 和 Remote 部署入口使用同一 Browser Setup 路径。生产 Setup：

- 在宿主机安装或验证合格的日常浏览器，绝不放入 Gateway Image；
- 通过 Owner 可见、可审计流程安装经过 Checksum 验证的 SparkClaw Browser Bridge；
- 在 Host Controller Runtime 安装固定 Playwright MCP、CLI 和兼容 Library Dependency；
- 创建 Owner-scoped Controller Service、私有 Runtime Directory 和 Capability Endpoint；
- 不把 Extension Token 写入 Compose Environment 或仓库文件；
- 验证浏览器正常启动且不带 CDP 或 Automation Flag；
- 启用浏览器能力前验证 Gateway 到 Controller 的可达性。

扩展模式要求持久 Owner Browser Session。无 Display 的 Remote Host 不会被静默转换为
Headless Playwright 或 Host-CDP。缺少受支持的 Owner Session 会产生部署错误。

Gateway Image 不包含 Chromium 或 Playwright Browser Binary。是否保留小型 MCP Client
Library 属于实现细节；所有 Browser 和 Extension Process Ownership 都留在宿主机。

使用共享入口安装或校验生产 Browser、Bridge、Controller 和持久 Profile：

```bash
npm run setup:browser
bash scripts/setup-browser.sh --check
npm run open:browser
```

Remote 部署通过同一 Setup 绑定对应的私有环境文件：

```bash
SPARKCLAW_BROWSER_ENV_FILE=.env.remote bash scripts/setup-browser.sh
SPARKCLAW_BROWSER_ENV_FILE=.env.remote bash scripts/setup-browser.sh --check
```

打开命令使用持久 `default` Profile。Controller 将固定宿主浏览器制品标识为
`chromium` 而非 `chrome`，并调用 Bridge Native Launcher，不会启动另一个浏览器。
Controller Service 不保存 Extension Token；Token 仍只保存在 Gateway Credential Vault，
并且只在一次有界校验或获取期间通过 Owner-only Unix Socket 传递。

## 安全边界

- 即使扩展提供客户端专属标签页组，也按 Browser-wide Privileged Access 对待扩展控制。
- Extension 和 Controller Capability 必须保持本地、Owner-scoped、可轮换，且不进入
  公开 API。
- 在 SparkClaw 边界 Allowlist MCP Operation 和 Provider Origin。
- 每次观察或动作前检查任务标签页所有权。
- 拒绝任意 JavaScript、任意 CLI 命令、Raw Locator Injection、File URL Access、
  Storage Export 和 Network Interception。
- 凭据留在浏览器内，页面内容和诊断信息遵循现有 Evidence Policy 脱敏。
- 外部发送保持精确内容 Approval 和全部 Unknown Outcome 规则。
- 不把 Playwright Allowed-Origin Option 当作安全边界；SparkClaw 授权和网络隔离仍为
  强制要求。

## 已完成迁移计划

### Phase 0：兼容性 PoC（已完成）

- 未修改的官方扩展已在不含普通 Owner Tab 或生产账户状态的一次性 Profile 中完成验证。
- 固定 SparkClaw Chromium 启动时不带 Remote-Debugging、Automation 或 Headless
  Flag；连接前已完成普通 Gmail 手动登录。
- MCP 和 CLI 分别完成连接、任务标签页操作和 Detach，浏览器与登录状态保持不变。
- Credential Rotation 与 One-client-per-tab Ownership 已通过；Chromium
  `148.0.7778.0` 获得固定产品制品的 GO 结论。

本阶段未修改生产默认值。

### Phase 1：Host Controller（已完成）

- Owner-scoped Controller、版本化私有协议、Authentication、Health、Process
  Supervision、Deadline、Generation 处理和 Cleanup Reconciliation 已实施并安装。
- 加密的 `playwright-extension-token-v1` Credential、经过认证且脱敏的 API，以及
  `Settings > Connections > Browser control` View 已加入。
- Fake Client、Protocol、Lifecycle、WebChat、Gateway 与真实宿主机覆盖已使用官方基线
  验证固定 Playwright 组合。

### Phase 2：通用 MCP Adapter（已完成）

- Provider-neutral Playwright Adapter 已在不改变 ToolHub Contract 的前提下替换旧实现。
- Snapshot、Ref、Generation、Settle、Task-tab Ownership、Action、Screenshot、Close、
  Detach 和 Failure Characterization 已在线下和真实环境通过。
- 临时 Host-CDP Qualification Default 已在 Phase 6 删除。

### Phase 3：确定性 CLI 脚本（已完成）

- QQ 邮箱、Outlook 和 Gmail 的 Probe 与 Send 已迁移为六个固定且经过 Checksum 验证的
  CLI Handler，不改变 Workflow、Approval、One-attempt 或 Unknown-outcome 语义。
- Provider Fixture 保留 Signed-out 分类、有界 Context-destroyed Recovery、Origin
  Validation 和 Fail-closed Redirect。
- 2026-09-05，`npm run qualify:playwright-email -- --profile remote` 通过 Browser Bridge
  `1.0.18` 在同一次只读运行中依次通过三个已登录账户。浏览器保持运行，Cleanup 未留下
  CLI Daemon 或 Session Directory，焦点未移到浏览器，也没有运行 Send Handler。

### Phase 4：SparkClaw Browser Bridge（已完成）

- Extension Handshake、Credential、Generation、Task-tab Ownership、后台操作和显式
  Handoff 契约已经冻结。
- SparkClaw Browser Bridge `1.0.18` 已从通过资格验证的上游源码独立打包，保留 License、
  Attribution、版本化 Service Worker 入口与 Checksum Closure。
- 兼容性测试保持官方基线的 MCP 和 CLI 行为。后台连接和操作不聚焦浏览器，也不暴露
  Owner Tab；只有 Owner 显式 Handoff 会聚焦所请求的任务 Tab。
- 生产环境已录入新生成的 Bridge Credential，官方扩展 Credential 未被复用或迁移。

### Phase 5：部署资格验证（已完成）

- Local 和 Remote 部署入口及 Doctor Check 已使用共享 Bridge-only Browser Setup，
  Migration Selector 不再存在。
- Owner Session 启动、Service Restart、Pairing、Detach、Generation 失效、Profile
  Persistence 与 Local/Remote Browser Check 已通过。
- 经 X11 监控的 No-handoff 与 Explicit-handoff 场景已证明焦点契约。Gateway、WebChat、
  Compose、Docs、Browser、Provider、Shell 与 Security Suite 已在下述环境限制内通过。

### Phase 6：原子切换与删除（已完成）

- SparkClaw Browser Bridge 已成为唯一生产 Extension Transport。
- browserd、Host-CDP Endpoint/Proxy/Configuration 和部署 Wiring、`agent-browser`、其
  MCP Adapter 与 Daemon Cleanup、旧 Test、Package Pin、Migration Selector 和 Fallback
  Path 均已删除。
- 当前 Architecture、Runtime、Deployment、Development、Email、README 与 Capability
  文档已同步更新中英文版本。
- Gateway Image 与依赖检查确认不会下载 Chromium 或 Playwright Browser Binary。

## 已完成验收门槛

切换已通过全部浏览器迁移门槛：

- 手动登录和认证复用无需导出 State 即可工作；
- 共享 Profile 的 Browser Command Line 不含 Remote-Debugging、Automation 或 Headless
  Flag；
- MCP 和 CLI 能分别连接并 Detach，且不关闭浏览器；
- 官方集成基线和 Browser Bridge 兼容性 Suite 均已通过；
- 后台工作不聚焦浏览器或选择 Owner Tab，显式 Handoff 只聚焦请求的任务 Tab；
- Owner Tab 始终不属于任务，MCP 与 CLI 不能并发控制同一个任务 Tab；
- Settings 与 Gateway Test 已证明 Token Non-prefill、Clear-after-save、
  Handshake-before-persist、Ciphertext-only Storage、Rotation、Deletion 和
  Stale-generation Rejection，且不删除浏览器认证；
- Browser Restart 会使旧 Task 与 Snapshot Identity 失效，同时保留 Profile 与 Pairing；
- Subprocess 和 Session Cleanup 未留下持久 Playwright Process 或 Runtime Directory；
- 通用 Adapter Characterization、真实表单交互和三个已登录 Provider Probe 均已通过，
  且未调用 Send；
- Send Handler 保持 Exact Approval、One-attempt 和 Unknown-outcome 规则；
- 共享 Local/Remote Browser Setup、Compose 与 Deployment Contract 已通过，完整 Local
  与 Remote Deployment Preflight 也已通过；
- Gateway、WebChat、Compose、Docs 和 Security Validation 已通过；
- 删除验证未发现活动 `agent-browser`、browserd、Host-CDP、Container Chromium 或可执行
  Compatibility Path。

## 已解决的资格问题

1. 固定 Chromium `148.0.7778.0` 可在 Playwright Extension Channel 声明为
   `chromium` 时工作；Chrome 与 Edge 不是产品备选项。
2. Pairing 是 WebChat 中 Owner 可见的 Credential Enrollment，随后执行真实且有界的
   Handshake。Token 不会再次显示，连接也不会获得 Controller Task Ownership 之外的 Tab。
3. MCP 和 CLI 获得不同的 Controller Session 和 Task Grant；Ownership Check 会拒绝
   Cross-task 和 Concurrent Same-tab Control。
4. User Service 使用持久 `default` Profile 重启固定 Browser 和 Controller，不添加
   Automation Flag。Readiness 恢复 Pairing 和认证，同时轮换 Task/Session Generation。
5. Browser Bridge 兼容性测试和经 X11 监控的真实场景证明 Attach 与普通 Action 保持后台；
   只有 `tabs.handoff` 会激活任务 Tab。
6. 最终生产组合为 SparkClaw Browser Bridge `1.0.18`、Playwright MCP `0.0.80`、
   Playwright CLI `0.1.19`、Playwright Library/Core
   `1.63.0-alpha-2026-08-31` 和 Chromium `148.0.7778.0`。官方 Extension `0.4.0`
   只保留为已完成的兼容性基线。

## 提案时版本证据

截至 2026-09-04，查询到的 npm `latest` 分别为 `playwright` `1.62.1`、
`@playwright/mcp` `0.0.80` 和 `@playwright/cli` `0.1.19`。这些版本仅是 PoC 候选。
已完成实现固定上述相互兼容的生产版本组合，且不会安装浮动的 `latest`。

## 参考资料

- [Playwright Library](https://playwright.dev/docs/library)
- [Playwright MCP](https://github.com/microsoft/playwright-mcp)
- [Playwright CLI](https://github.com/microsoft/playwright-cli)
- [Playwright Extension](https://github.com/microsoft/playwright/tree/main/packages/extension)
- [当前浏览器 Runtime](../../docs/browser-runtime.md)
- [已实施 Host-CDP 设计](../../docs/host-cdp-browser-design.md)
- [当前浏览器邮箱 Workflow](../../docs/browser-email-workflow-design.md)
