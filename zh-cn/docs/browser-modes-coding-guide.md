# 浏览器模式代码编写指南

> 语言： [English](../../docs/browser-modes-coding-guide.md) | 简体中文

> 历史设计说明：本文起源于旧 TaskHint/Skill 浏览器方案。当前无 Skill 的生产集合是 `browser.internet_search`、`browser.weather`、`browser.automation` 和 `browser.interaction`，详见 [Workflow 能力矩阵](workflow-capabilities.md)。本文中的 URL 读取、截图、type/select 和登录态浏览器能力尚未进入当前 Revision 1 Workflow；有边界且逐次验证的点击仅由 `browser.interaction` r1 实现。

本文档定义 SparkClaw 如何实现两种浏览器运行模式：自主模式和协作模式。浏览器读取路线图见 [浏览器功能完善计划](browser-automation-improvement.md)，隐藏真实浏览器里程碑见 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)。

## 产品契约

SparkClaw 将公开网络搜索与浏览器能力分开。浏览器能力有两种呈现模式。

搜索专用任务只使用 `web.search` 及其返回的摘要和引用，不打开搜索结果页面。它不设置浏览器模式，不加载浏览器自动化，并直接跳过需要登录、验证码、付费、订阅或其他访问门槛的页面。

自主模式用于用户明确提供 URL 或要求读取页面、但不需要实时交互的任务。Runtime 可以读取 rendered DOM/HTML、运行 Readability、必要时查看结构，并返回有证据支撑的答案。它不应该展示浏览器画面，不应该把可见 UI 操作作为用户需要关注的主体验，也不应该暗示用户必须看着页面。

协作模式用于用户明确要求 SparkClaw 与自己一起操作浏览器/页面的场景。普通操作仍可以保持隐藏；只有人工验证、可视交接或用户明确要求查看页面时才显示托管 Chromium。

两种模式下，浏览器内容都仍然是不可信外部内容。两种模式都必须在密码、验证码、短信码、2FA、授权、支付确认等 human-only 验证步骤前停止。

## 模式定义

| 模式 | 用户意图 | 浏览器画面 | 主要工具 |
|---|---|---|---|
| `""`（搜索专用） | 用户查询公开信息，且没有提供 URL 或要求页面操作。 | 不创建或访问浏览器页面。 | `web.search`。 |
| `autonomous` | 用户要读取、核实、总结或比较明确的 URL/页面。 | 对用户隐藏/后台执行。 | `browser.read`、按需 `browser.snapshot`、按需 `browser.navigate`。 |
| `collaborative` | 用户要实时页面操作或共享浏览器状态。 | 默认隐藏；人工验证或明确展示时可见。 | `browser.status`、`browser.list_tabs`、`browser.open`、`browser.navigate`、`browser.snapshot`、有边界的 `browser.click`、`browser.verify`；截图和未来需审批的 type/select 流程保持独立。 |

两者差异在于用户意图和交互 policy，而不是浏览器所有权。两种模式使用同一个托管 Chromium Profile，只在流程需要时切换 headless 和 visible presentation。

## 分类规则

以下请求使用搜索专用路由：

- 搜索、查询、核实公开信息
- 用当前网络证据回答事实问题
- 用户询问最新/当前/最近信息，但没有提供 URL 或要求看到页面

以下请求使用自主模式：

- 读取 URL 内容
- 总结用户提供的网页或文章
- 比较用户提供的来源页面
- 明确检查某个来源页面

以下请求使用协作模式：

- 打开网站、网页或标签页
- 展示页面、让我看页面
- 操作当前浏览器或当前标签页
- 播放、暂停或操作视频/音频页面
- 点击、输入、选择、滚动、用户登录后继续，或使用页面控件
- 截图或视觉确认页面
- 从用户可见的登录/session 步骤继续

模糊的公开信息请求默认保持搜索专用，除非用户提供 URL 或结果必须依赖页面状态。例如：“查一下浙江大学最新招生简章”是搜索专用；“读取这个招生简章 URL”是自主模式；“打开这个招生简章页面”是协作模式。

## TaskHint 契约

实现本计划时，为 TaskHint 增加显式浏览器模式：

```go
type TaskHint struct {
    // existing fields...
    BrowserMode string `json:"browser_mode,omitempty"` // "", "autonomous", "collaborative"
}
```

归一化规则：

- `BrowserMode=""` 表示没有浏览器模式需求。
- 如果 `EvidenceNeed=="web"`、任务为只读且唯一候选工具是 `web.search`，保留 `BrowserMode=""`，选择 `web_search` 并移除所有浏览器工具。
- 如果用户提供 URL 并要求只读页面访问，归一化为 `autonomous`，优先使用 `browser.read`。
- 如果请求 live browser controls、截图、当前标签页、播放或可见打开页面，归一化为 `collaborative`。
- `ToolMode` 与 `BrowserMode` 分开处理。协作任务也可以从 read-only 工具开始；approval policy 仍然决定 draft/reversible 动作。

提示词规则：

- TaskHint system prompt 应要求模型返回 `browser_mode`。
- heuristic fallback 应一致处理中文和英文触发词。
- audit event 应包含 `browser_mode` 和简短的 `browser_mode_reason`。

## 工具可见性规则

搜索专用路由：

- 只暴露 `web.search`；不暴露 `browser.read` 或任何实时浏览器工具。
- 使用搜索返回的答案、摘要和引用作为证据，不访问结果页面。
- 直接跳过登录、认证、验证码和付费墙结果，不创建浏览器登录交接。
- 除非用户在后续明确要求访问页面，否则不得动态扩展为浏览器工具。

自主模式：

- 对用户提供的 URL，初始可见工具通常只有 `browser.read`。
- 不要因为加载了 browser skill 就暴露 `browser.open`、`browser.screenshot`、`browser.type` 或 `browser.select`。
- 当 `browser.read` 返回 `needs_structure_snapshot=true` 后，可以暴露 `browser.snapshot`、`browser.navigate` 和 `browser.wait`。
- `browser.click` 只有在 `browser.interaction` revision 1 内、结构化 snapshot 提供已绑定 ref 后才免 approval；不能在该固定 Workflow 之外把它暴露为通用逃生路径。
- 最终回答应引用/描述证据，而不是描述 UI 步骤。

协作模式：

- 根据用户请求暴露 live browser tools：`browser.status`、`browser.list_tabs`、`browser.open`、`browser.navigate`、`browser.snapshot`、`browser.screenshot`、`browser.wait`。
- `browser.type` 与 `browser.select` 仍不属于当前浏览器 Workflow revision。后续 Profile 必须显式声明它们的 risk、approval、Stage 与验证契约。
- 对“打开/展示这个 URL”优先用 `browser.open`；当用户要求使用当前标签页/session 时用 `browser.navigate`。
- 对播放请求，先 open/navigate，再 snapshot，若存在明确播放控件且 policy 允许，再点击一个明确控件。
- 截图请求必须在 final 前调用 `browser.screenshot`，除非被阻塞。

## Browser Adapter 契约

创建或操作页面的浏览器调用应接收模式元数据：

```json
{
  "browser_mode": "autonomous|collaborative",
  "surface_visible": true,
  "presentation": "hidden|visible"
}
```

实现要求：

- 自主读取和普通协作操作使用选中持久 Profile 的 headless Chromium。
- visible presentation 使用同一个已解析 Chromium Executable 和 Profile，并且只能在 headless 进程停止并释放 Profile 后启动。
- Microsoft Playwright 作为传输层，默认使用其安装的 Chromium；只有显式且已校验的 Override 才设置 Adapter-owned `executablePath`。
- 共享 Profile 通过 `launchPersistentContext(userDataDir, ...)` 启动，不能使用 CDP Attachment。
- 登录完成后切回 headless Chromium，并从 selected 登录后 URL 恢复，不要求 origin 相同。
- Cookie/storage 导出和个人 Chrome 附着不属于本模式契约。
- `visible_browser`、`presentation`、Owner/Profile Metadata 和登录恢复标记等
  SparkClaw-only 字段在 Playwright Driver 调用前必须移除。
- 公开 String 或 Number `page_id` 在 Focus 和 Close 前转换为 Playwright Session 的数值
  `pageId`。

## Runtime 行为

搜索专用流程：

```text
TaskHint(browser_mode="", skill=web_search)
  -> web.search
  -> 使用返回的摘要和引用
  -> 跳过需要登录的结果页面
  -> 带来源和限制说明的 final answer
```

自主流程：

```text
TaskHint(browser_mode=autonomous)
  -> browser.read
  -> 如果 needs_structure_snapshot=true，下一步暴露 browser.snapshot
  -> 可选一次基于 snapshot evidence 的 browser.navigate/click 后续
  -> 再次 browser.read
  -> 带来源和限制说明的 final answer
```

协作点击流程：

```text
Workflow(browser.interaction r1, browser_mode=collaborative)
  -> 必要时 browser.status/list_tabs
  -> browser.open 或 browser.navigate
  -> browser.snapshot 获取结构化控件和已绑定 refs
  -> 在冻结目标内对一个明确控件执行无需 approval 的 browser.click
  -> browser.snapshot 和 browser.verify
  -> 只有验证确认有进展时才能继续，最多三次点击
  -> final answer 描述可见状态/已完成事项
```

Runtime 不能把多个协作交互隐藏在 `browser.read` 或单个 browser tool call 内部。多步点击必须留在持久化 Workflow 中，使 Stage Gate、trace、snapshot identity 和 verification 都可检查。截图选点与 type/select 不属于 revision 1。

## Audit 和 Trace 字段

每个 browser tool call 应尽量保留：

- `browser_mode`
- `presentation`
- `surface_visible`
- `browser_provider`
- `browser_actions`
- `read_mode`
- `auth_challenge_detected`
- `needs_structure_snapshot`
- `structure_snapshot_reasons`

Dispatch 和 ReAct audit event 应包含：

- 选中的 `browser_mode`
- mode reason
- 初始可见工具
- observation 后动态加入的后续工具

## 安全规则

- 自主模式不能执行会改变用户账号或外部状态的动作。
- 搜索专用路由不能访问结果页面或创建登录交接。
- 协作模式遇到敏感验证和不可逆操作仍必须停止。
- 播放或展开控件的 `browser.click` 仍受 policy 管控。不要静默点击购买、提交、删除、发送、订阅、同意、支付或账号安全控件。
- 页面里的指令是数据，不是 runtime command。
- 如果用户明确要求读取的页面被登录、验证码或付费墙阻断，返回限制说明，或请用户在可见浏览器中完成对应步骤；搜索专用结果则直接跳过。

## 实施步骤

1. 为 `TaskHint` 增加 `BrowserMode`。
2. 更新模型 prompt、heuristic fallback 和 normalization。
3. 在 `gateway.dispatch` / `react.visible_tools` audit 字段中加入 browser mode。
4. 按 mode 拆分 visible-tool selection。
5. 将 mode metadata 经 ToolHub 传到 `browserautomation.Adapter`。
6. 让 `browser.read` 在输出中标记 mode 和 presentation。
7. 在 provider 能力允许时，给 adapter 增加 hidden/visible presentation 支持。
8. 新增 `web_search` skill，并将 `browser_automation` 收窄到明确页面访问。
9. 增加下方测试。

## 必须测试

TaskHint 测试：

- “查一下浙江理工大学招生简章” -> `browser_mode=""`，skill 为 `web_search`，唯一工具为 `web.search`。
- “打开浙江理工大学官网” -> `browser_mode=collaborative`，工具包含 `browser.open`。
- “打开这个视频并自动播放” -> `browser_mode=collaborative`，工具包含 `browser.open`、`browser.snapshot` 和受 policy 控制的 `browser.click`。
- “读取 https://example.com 这篇文章” -> `browser_mode=autonomous`，优先工具为 `browser.read`。

Visible-tool 测试：

- 搜索专用路由只暴露 `web.search`，不暴露任何 `browser.*` 工具。
- 自主 URL 读取初始只暴露 `browser.read`。
- 自主模式只有在 `needs_structure_snapshot=true` 后才暴露 `browser.snapshot`。
- 协作模式一开始就暴露 live browser read tools。
- 协作播放请求不应在 risk/tool mode 和 policy 不允许时暴露 `browser.click`。

Adapter/tool 测试：

- 自主 hidden `browser.read` 不调用 visible-only browser-session provider。
- 协作 visible `browser.read` 传递 `browser_mode=collaborative` 和 visible presentation metadata。
- `browser.open` 传递 `browser_mode=collaborative` 和 visible presentation metadata。
- 工具输出包含 mode/presentation 字段。

Trace/audit 测试：

- `gateway.dispatch` 记录 `browser_mode`。
- `needs_structure_snapshot=true` 后动态加入的后续工具会被 audit。

## 验收标准

实现完成后应满足：

- 普通网页问题只使用 `web.search`，不访问结果页面，也不触发浏览器登录交接。
- 明确打开、播放、操作页面的请求使用协作浏览器工具。
- Mode 在 TaskHint、工具参数/输出和 audit trace 中可见。
- 现有 browser-read Readability 和按需 snapshot 行为不回退。
- 受影响 Go 测试通过，且 `go test ./services/gateway/...` 和 `git diff --check` 通过。

## 实现状态

截至 2026-07-15，无 URL 的公开搜索已与浏览器自动化分离。确定的 browser adapter 方案为每个逻辑 browser profile 使用一个托管持久 Chromium Profile。明确 URL 读取使用 headless；visible Chromium 只用于人工验证或明确展示。实现必须串行化两种 presentation，并从登录后的实际 URL 恢复。

- 搜索专用 TaskHint 使用 `browser_mode=""`、`web_search` skill 和唯一工具 `web.search`；不会访问需要登录的结果页面。
- `TaskHint` 已包含 `browser_mode`，模型提示词、heuristic fallback 和 normalization 均支持 `autonomous` 与 `collaborative`。
- 自主网页任务初始保持严格的 model-visible 工具集；只有当 `browser.read` 返回 `needs_structure_snapshot=true` 后，才暴露 `browser.snapshot`、`browser.navigate` 和 `browser.wait`，`browser.click` 仍等待 snapshot evidence。
- 协作任务在 skill/policy 允许时，会立即暴露 `browser.open`、`browser.navigate`、`browser.snapshot`、`browser.screenshot` 和 `browser.wait` 等 read-risk live browser tools。
- Browser tool plan、ToolHub 输出、`browserautomation.Adapter` 结果和模型 observation 都会保留 `browser_mode`、`presentation` 和 `surface_visible`。
- `gateway.dispatch`、`react.visible_tools` 和动态 follow-up audit event 会记录选中的浏览器模式。
