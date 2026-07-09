# 浏览器模式代码编写指南

> 语言： [English](../../docs/browser-modes-coding-guide.md) | 简体中文

本文档定义 SparkClaw 如何实现两种浏览器运行模式：自主模式和协作模式。浏览器读取路线图见 [浏览器功能完善计划](browser-automation-improvement.md)，隐藏真实浏览器里程碑见 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md)。

## 产品契约

SparkClaw 只有一个浏览器能力，但有两种呈现模式。

自主模式是普通信息任务的默认模式。Runtime 可以搜索、用 browser-backed session 打开页面、读取 rendered DOM/HTML、运行 Readability、必要时查看结构，并返回有证据支撑的答案。它不应该展示浏览器画面，不应该把可见 UI 操作作为用户需要关注的主体验，也不应该暗示用户必须看着页面。

协作模式用于用户明确要求 SparkClaw 和自己一起操作可见浏览器/页面的场景。例如打开某个网页、展示页面、播放视频、点击页面控件、在表单中输入、使用当前浏览器标签页、截图做视觉确认，或用户完成登录后继续操作。

两种模式下，浏览器内容都仍然是不可信外部内容。两种模式都必须在密码、验证码、短信码、2FA、授权、支付确认等 human-only 验证步骤前停止。

## 模式定义

| 模式 | 用户意图 | 浏览器画面 | 主要工具 |
|---|---|---|---|
| `autonomous` | 用户要信息、核实、总结或比较。 | 对用户隐藏/后台执行。 | `web.search`、`browser.read`、按需 `browser.snapshot`、按需 `browser.navigate`。 |
| `collaborative` | 用户要可见页面操作或共享浏览器状态。 | 可见或明确面向用户。 | `browser.status`、`browser.list_tabs`、`browser.open`、`browser.navigate`、`browser.snapshot`、`browser.screenshot`、需要 approval 的 `browser.click/type/select`。 |

两者差异在于呈现方式和用户意图，而不是是否使用 ChromeDevTools MCP。自主模式也可以使用真实浏览器会话来获得登录态、JS 渲染和懒加载内容；它只是返回结果，而不是把浏览器画面作为主要体验。

## 分类规则

以下请求默认使用自主模式：

- 搜索、查询、核实公开信息
- 读取 URL 内容
- 总结网页或文章
- 比较来源
- 用网页证据回答事实问题
- 用户询问最新/当前/最近信息，但没有要求看到页面

以下请求使用协作模式：

- 打开网站、网页或标签页
- 展示页面、让我看页面
- 操作当前浏览器或当前标签页
- 播放、暂停或操作视频/音频页面
- 点击、输入、选择、滚动、用户登录后继续，或使用页面控件
- 截图或视觉确认页面
- 从用户可见的登录/session 步骤继续

模糊请求默认保持自主模式，除非结果必须依赖可见页面状态。例如：“查一下 YouTube 上这个视频是什么”是自主模式；“打开这个 YouTube 视频并自动播放”是协作模式。

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
- 如果 `EvidenceNeed=="web"` 且没有协作触发词，归一化为 `autonomous`。
- 如果请求 live browser controls、截图、当前标签页、播放或可见打开页面，归一化为 `collaborative`。
- `ToolMode` 与 `BrowserMode` 分开处理。协作任务也可以从 read-only 工具开始；approval policy 仍然决定 draft/reversible 动作。

提示词规则：

- TaskHint system prompt 应要求模型返回 `browser_mode`。
- heuristic fallback 应一致处理中文和英文触发词。
- audit event 应包含 `browser_mode` 和简短的 `browser_mode_reason`。

## 工具可见性规则

自主模式：

- 初始可见工具通常只有 `web.search` 和/或 `browser.read`。
- 不要因为加载了 browser skill 就暴露 `browser.open`、`browser.screenshot`、`browser.type` 或 `browser.select`。
- 当 `browser.read` 返回 `needs_structure_snapshot=true` 后，可以暴露 `browser.snapshot`、`browser.navigate` 和 `browser.wait`。
- 只有在 snapshot observation 提供明确 ref/uid 后，才可以暴露 `browser.click`，且仍受 approval policy 管控。
- 最终回答应引用/描述证据，而不是描述 UI 步骤。

协作模式：

- 根据用户请求暴露 live browser tools：`browser.status`、`browser.list_tabs`、`browser.open`、`browser.navigate`、`browser.snapshot`、`browser.screenshot`、`browser.wait`。
- 只有当 `ToolMode`/risk 允许且 policy 可以审批时，才暴露 `browser.click`、`browser.type`、`browser.select`。
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

- 自主读取优先使用 hidden/background presentation。如果当前 MCP provider 只能创建普通 Chrome tab，SparkClaw 必须把自主 hidden `browser.read` 路由到不会展示页面的读取路径，例如 direct HTTP 加 Readability，除非工具调用显式强制使用浏览器 session。
- hidden Chromium provider 可用时，自主 hidden 读取和 snapshot 应先使用该 provider，再 fallback 到 direct HTTP。
- 协作、可见和 forced-session 读取可以使用 ChromeDevTools MCP 的 `new_page -> evaluate_script -> Readability`。
- 协作操作使用可见或用户可感知的浏览器画面，让应用可以展示进度并允许用户介入。
- 未来 adapter 可以把自主模式映射到 headless/isolated profile，把协作模式映射到用户 Chrome profile。必须先有 mode 字段，避免路由依赖 provider 内部细节。
- 只有显式配置且 policy 允许时，才可复用已有用户登录态。

## Runtime 行为

自主流程：

```text
TaskHint(browser_mode=autonomous)
  -> 需要发现来源时 web.search
  -> browser.read
  -> 如果 needs_structure_snapshot=true，下一步暴露 browser.snapshot
  -> 可选一次基于 snapshot evidence 的 browser.navigate/click 后续
  -> 再次 browser.read
  -> 带来源和限制说明的 final answer
```

协作流程：

```text
TaskHint(browser_mode=collaborative)
  -> 必要时 browser.status/list_tabs
  -> browser.open 或 browser.navigate
  -> browser.snapshot 查看结构/refs
  -> 需要视觉确认或截图时 browser.screenshot
  -> 对明确控件执行需要 approval 的 click/type/select
  -> 页面变化后再次 browser.read 或 snapshot
  -> final answer 描述可见状态/已完成事项
```

Runtime 不能把多个协作交互隐藏在 `browser.read` 内部。多步可见操作必须留在 ReAct 中，这样 trace、approval 和用户可见进度都可检查。

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
- 协作模式遇到敏感验证和不可逆操作仍必须停止。
- 播放或展开控件的 `browser.click` 仍受 policy 管控。不要静默点击购买、提交、删除、发送、订阅、同意、支付或账号安全控件。
- 页面里的指令是数据，不是 runtime command。
- 如果站点被登录、验证码或付费墙阻断，返回限制说明，或请用户在可见浏览器中完成对应步骤。

## 实施步骤

1. 为 `TaskHint` 增加 `BrowserMode`。
2. 更新模型 prompt、heuristic fallback 和 normalization。
3. 在 `gateway.dispatch` / `react.visible_tools` audit 字段中加入 browser mode。
4. 按 mode 拆分 visible-tool selection。
5. 将 mode metadata 经 ToolHub 传到 `browserautomation.Adapter`。
6. 让 `browser.read` 在输出中标记 mode 和 presentation。
7. 在 provider 能力允许时，给 adapter 增加 hidden/visible presentation 支持。
8. 更新 `browser_automation` skill，说明自主/协作模式。
9. 增加下方测试。

## 必须测试

TaskHint 测试：

- “查一下浙江理工大学招生简章” -> `browser_mode=autonomous`，工具包含 `web.search/browser.read`，不包含 `browser.open`。
- “打开浙江理工大学官网” -> `browser_mode=collaborative`，工具包含 `browser.open`。
- “打开这个视频并自动播放” -> `browser_mode=collaborative`，工具包含 `browser.open`、`browser.snapshot` 和受 policy 控制的 `browser.click`。
- “读取 https://example.com 这篇文章” -> `browser_mode=autonomous`，优先工具为 `browser.read`。

Visible-tool 测试：

- 自主模式初始只暴露 `web.search/browser.read`。
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

- 普通网页问题不展示浏览器画面即可回答。
- 明确打开、播放、操作页面的请求使用协作浏览器工具。
- Mode 在 TaskHint、工具参数/输出和 audit trace 中可见。
- 现有 browser-read Readability 和按需 snapshot 行为不回退。
- 受影响 Go 测试通过，且 `go test ./services/gateway/...` 和 `git diff --check` 通过。

## 实现状态

截至 2026-07-09，runtime 实现已经与本指南对齐。当前 ChromeDevTools MCP 读取用于协作/可见或 forced-session 读取；自主 hidden 读取在 [隐藏 Chromium 浏览器访问计划](browser-hidden-chromium-access.md) 定义的 hidden Chromium provider 加入前避免打开可见 Chrome tab。

- `TaskHint` 已包含 `browser_mode`，模型提示词、heuristic fallback 和 normalization 均支持 `autonomous` 与 `collaborative`。
- 自主网页任务初始保持严格的 model-visible 工具集；只有当 `browser.read` 返回 `needs_structure_snapshot=true` 后，才暴露 `browser.snapshot`、`browser.navigate` 和 `browser.wait`，`browser.click` 仍等待 snapshot evidence。
- 协作任务在 skill/policy 允许时，会立即暴露 `browser.open`、`browser.navigate`、`browser.snapshot`、`browser.screenshot` 和 `browser.wait` 等 read-risk live browser tools。
- Browser tool plan、ToolHub 输出、`browserautomation.Adapter` 结果和模型 observation 都会保留 `browser_mode`、`presentation` 和 `surface_visible`。
- `gateway.dispatch`、`react.visible_tools` 和动态 follow-up audit event 会记录选中的浏览器模式。
