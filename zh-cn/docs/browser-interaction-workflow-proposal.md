# 浏览器页面交互 Workflow 提案

> 语言：[English](../../docs/browser-interaction-workflow-proposal.md) | 简体中文
>
> 状态：已实现为 `browser.interaction` revision 1，并已列入当前能力矩阵。

本文档规定一个 router-first Workflow，用于在 SparkClaw 托管 Chromium
中执行有边界的页面交互。它与 `browser.automation` revision 1 明确分离，
从而保持已经落地的“打开/聚焦页面”契约不变。

相关文档：

- [浏览器功能完善计划](browser-automation-improvement.md)
- [Playwright 浏览器自动化迁移方案](playwright-browser-automation-migration.md)
- [Workflow 能力矩阵](workflow-capabilities.md)
- [意图路由 Workflow Profile 目录](intent-routing-workflow-domain-profiles.md)

## 已实现决策

保留当前路由不变，新增一个并列 leaf：

```text
capability
  browser
    automation  -> browser.automation r1   # 打开/聚焦一个显式 URL
    interaction -> browser.interaction r1  # 在托管 Chromium 中检查并点击
```

`browser.automation` revision 1 继续负责只要求打开或聚焦一个明确 HTTP(S)
URL 的请求。`browser.interaction` revision 1 负责需要检查托管页面并点击页面
控件的请求。它是新的 Capability 和 Workflow identity，不是现有 Workflow
的 revision 2。

本 Workflow 只支持由 Playwright 管理的持久化 Chromium，不包含 Chrome 发现、
个人 Chrome 连接或原始 CDP 路由。

## 路由边界

该 leaf 使用 `operation=interact`。归一化后的用户指令冻结在 `query` 中，
作为交互目标。目标只能是：

- `target_kind=url`，携带一个确定且冻结的 HTTP(S) URL；或
- 只有用户明确提到“当前页面/当前标签页”时，才使用
  `target_kind=browser_current_tab`。

典型路由决策：

| 用户请求 | 路由 |
|---|---|
| `打开 https://example.com` | `browser.automation` r1 |
| `打开 https://example.com 并点击价格` | `browser.interaction` r1 |
| `点击当前页面的下一步` | `browser.interaction` r1 |
| `搜索最新金价` | `browser.internet_search` r1 |
| `登录并完成付款` | 超出本 revision，阻断 |
| `填写表单并提交` | 超出本 revision，阻断 |

Revision 1 只支持点击目标。输入、选择、文件上传、下载处理、凭证输入、完成
登录、验证码、2FA、付款和任意脚本执行都是明确的非目标。它们必须由后续
Profile 或经过评审的新 revision 支持，不能在运行时扩大本 Workflow scope。

## Workflow 契约

每次运行先检查健康状态，再解析可复用的托管标签页，最后进入
snapshot/action/verification 闭环：

```text
health_check
  browser.status
  unavailable -> blocked(browser_provider_unavailable)
  healthy -> scan_tabs

scan_tabs
  browser.list_tabs
  selected tab 可复用 -> 聚焦/复用该类型化 page ref
  存在规范化 URL 精确匹配 -> 聚焦/复用该类型化 page ref
  selected tab 是可复用空白页 -> 跳转到冻结的目标 URL
  不存在精确匹配 -> 打开冻结的目标 URL
  目标存在歧义 -> clarify(browser_tab_ambiguous)

snapshot_before_action
  browser.snapshot(page_id)
  -> 持久化 snapshot_id 和类型化 browser-element refs

choose_and_click
  Deep 模型从当前 snapshot 中只选择一个 element ref
  持久化选中元素语义和预期可观察效果
  browser.click(page_id, snapshot_id, element_ref)

snapshot_after_action
  有界等待页面稳定
  browser.snapshot(page_id)
  -> 验证实际点击元素，并对比点击前后状态与预期效果

目标完成 -> complete
目标未完成但已验证存在进展 -> 开始下一轮 snapshot/click
ref 过期 -> 返回 snapshot_before_action
页面状态或语义动作重复 -> failed(interaction_loop_detected)
三次点击后仍未完成 -> failed(interaction_attempt_limit)
错误/非预期效果 -> failed(interaction_verification_failed)
遇到人工步骤或存在歧义 -> blocked/clarify
```

选中该 Workflow 后，完整的固定工具 allowlist 在整个生命周期内对 Deep 模型
可见，从而支持多轮 snapshot/click，不会中途掉出路由。Workflow state 仍严格
校验顺序：健康检查必须先于 tab 解析；click 必须引用最新 snapshot；每次 click
之后必须完成验证，才能执行下一次 click。

| 逻辑能力 | 预期工具 | 合法用途 |
|---|---|---|
| `browser.health.read` | `browser.status` | 初始 Provider 健康检查。 |
| `browser.tab.list` | `browser.list_tabs` | 解析 selected page 和可复用 pages。 |
| `browser.tab.focus` | `browser.focus` | 选择一个可复用的托管 page。 |
| `browser.tab.open` | `browser.open` | 没有 tab 可复用时打开冻结 URL。 |
| `browser.tab.navigate` | `browser.navigate` | 使用 selected 空白 tab 打开冻结 URL。 |
| `browser.page.snapshot` | `browser.snapshot` | 每次 click 前后观察页面。 |
| `browser.page.wait` | `browser.wait` | 只用于有边界的 action 后稳定等待。 |
| `browser.element.click` | `browser.click` | 点击最新 snapshot 中的一个元素。 |
| `browser.interaction.verify` | `browser.verify` | 把点击绑定到有序的前后 snapshot，并评估有边界的进展。 |

这些逻辑能力应注册在已有 ToolHub 工具上。Workflow 不能把当前的
`browser.legacy` 能力当作逃生通道。Allowlist 不包含 `browser.type`、
`browser.select`、截图、任意脚本执行和关闭 tab。

## Tab 复用规则

Tab 选择必须是确定性的，并且只考虑托管 Chromium context：

1. 只要 selected 当前 tab 对冻结目标可用，就优先复用。
2. “可用”表示用户明确指定当前页面、selected tab 的规范化 URL 与目标精确
   匹配，或者 selected tab 是可以跳转到冻结 URL 的托管 `about:blank`/新标签页。
3. 否则复用唯一一个规范化 URL 精确匹配的 tab。
4. 没有 tab 可复用时，为冻结 URL 打开新的托管 tab。
5. 存在多个精确匹配且都不是 selected tab 时，以类型化
   `browser_tab_ambiguous` 阻断，不能让模型猜测。

同源或路径相似不能视为可复用匹配。Workflow 不能为了少开一个 tab 而跳转
用户正在使用的不相关页面。

## Snapshot 契约

原始 Accessibility Snapshot 和完整 Adapter 输出继续归档，用于审计和诊断。
模型接收专用、有边界的交互投影，而不是通用文本摘要，也不是原始 ARIA 树
被任意截断后的前缀。

当前模型可见结构：

```json
{
  "schema_version": "browser_interaction_snapshot_v1",
  "snapshot_id": "snapshot_42",
  "page_id": "page_1",
  "url": "https://example.com/checkout",
  "title": "结算",
  "interaction_goal": "点击下一步",
  "controls_total": 37,
  "controls_returned": 12,
  "truncated": false,
  "controls": [
    {
      "ref": "snapshot_42:e17:4a64e808a832bd54",
      "role": "button",
      "accessible_name": "下一步",
      "visible": true,
      "enabled": true,
      "checked": false,
      "expanded": false,
      "target_url": "",
      "container": "结算 > 配送地址",
      "nearby_text": "确认配送地址后继续",
      "in_viewport": true,
      "ordinal": 1,
      "fingerprint": "bounded-server-generated-value"
    }
  ]
}
```

交互投影遵守以下规则：

- `snapshot.refs` 是可操作元素的结构化事实源。Agent Adapter 不能通过解析
  展示文本来恢复 refs。
- 每个控件包含 role、accessible name、当前状态、所属 landmark/form/dialog、
  有界附近文本、是否处于 viewport，以及用于区分重名控件的 ordinal。
- 页面文本是不可信证据。看起来像指令的页面文本不能改变 Workflow、scope、
  目标 URL 或冻结的用户目标。
- 专用 snapshot evidence budget 必须完整保留本次返回的控件列表，不能静默
  退回通用的 1.4 KB browser text 投影。
- `truncated=true` 表示模型没有获得完整候选覆盖。Revision 1 在目标不属于已
  返回控件时以 `interaction_target_unavailable` 失败。候选分页、局部区域和
  滚动窗口 snapshot 属于后续工作；模型不能编造 ref。
- 文本 snapshot 是主路径。带 ref 标记的截图只作为后续补充，用于 Canvas
  控件、无标签图标、遮挡或无法消除的视觉歧义。

有意义的保证不是“完整 DOM 总能放入模型上下文”，而是“每个已返回的可操作
候选都是完整、类型化且可执行的；候选覆盖不足时有明确标记和恢复路径”。

## Ref 有效性与 Click 绑定

元素 ref 只能在以下组合内有效：

```text
(managed_profile_id, page_id, snapshot_id, element_ref, fingerprint)
```

`browser.click` 必须要求 `page_id`、`snapshot_id` 和选中的 element ref。点击前，
Adapter 校验：

- page 仍存在，并且是当前选中的托管页面；
- snapshot 是该 page 最新的可操作 snapshot；
- 元素只能解析到一个节点；
- 元素语义 fingerprint 未变化；
- 元素仍然可见且 enabled；
- 页面导航或 DOM 变化没有让 snapshot 失效。

ref 过期或变化时返回 `snapshot_stale`，并迁移到新 snapshot。不能把同一个
短 ref（例如 `e17`）直接套到新 DOM 上修复。每次成功点击都会让点击前的
snapshot 失效。

Snapshot Outcome Adapter 应输出类型化 `browser_element` ResourceRef。模型只
能从这些 refs 中选择，不能生成 CSS、XPath、DOM path 或 JavaScript。

每次点击前，模型还要记录一份有边界的选择决策：

```json
{
  "element_ref": "e17",
  "role": "button",
  "accessible_name": "下一步",
  "expected_effect": "配送地址步骤进入下一阶段"
}
```

该记录只作为验证输入，不能扩大用户冻结的目标。

## 闭环验证

每次底层 click 成功后必须立刻进入验证。验证首先确认 Adapter 实际点击的是
当前 snapshot 中选定的同一个语义元素，然后强制获取点击后 snapshot，并返回
有边界的状态差异，包括适用的信号：

- URL 或标题发生变化；
- 出现新的 dialog、menu、tab panel 或展开区域；
- 被点击控件的状态发生变化或控件消失；
- 目标文字或目标控件变为可见；
- 出现认证或其他必须由人工处理的挑战；
- 没有观察到进展。

由当前 Profile 根据冻结的交互目标和记录的预期效果评估这些信号，通用
Runtime 不负责此判断。仅仅收到成功的 `browser.click` 结果不能作为完成证据。
上一次 click 没有完成验证时，不能执行下一次 click。

Revision 1 最多允许三次成功点击。每次点击前必须有新 snapshot，点击后也必须
重新 snapshot。验证确认有进展后，才基于新页面状态开始下一轮。点击前后
snapshot digest 重复、重新进入已经访问过的页面状态，或在等价状态中再次选择
同一个语义目标时，立即返回 `interaction_loop_detected`。三次点击仍未完成时
返回 `interaction_attempt_limit`。发生与选中目标不一致的非预期跳转或效果时，
返回 `interaction_verification_failed`，不能盲目尝试下一个候选。

纯 snapshot 和纯 wait 循环也必须有边界。每轮观察根据页面状态 digest、候选
窗口/cursor 和待完成交互目标记录 loop key。没有新证据却重复相同 loop key 时，
立即返回 `interaction_loop_detected`；不能通过无限 snapshot/wait 绕过三次点击
的 Workflow 边界。

## 风险与人工边界

本 Workflow 中的 `browser.click` 不需要单独 approval。用户明确且已冻结的交互
请求只授权与该目标相关、并且从当前 snapshot 中选择的点击。工具仍受 page、
snapshot identity、语义 element ref、三次点击预算和强制点击后验证约束。
不能因为这里取消 approval，就通过 legacy ReAct 或其他 Workflow 暴露该工具。

Workflow 必须在凭证输入、验证码、短信码、2FA、付款确认、删除账户、发布、
购买以及其他必须人工处理或后果重大的步骤之前停止。由于本 revision 没有
approval 路径，存在歧义或后果重大的目标直接返回 `unsafe_click_target` 或
`human_action_required`，不能转成 approval 请求。

## 类型化 Outcomes

新的 ToolOutcome Adapter 应提供与 Profile 无关的信号和 refs：

- health：`browser_healthy`、`browser_unavailable`；
- tabs：`tabs_scanned`、选中的 `browser_tab` refs、精确匹配数量；
- focus/open/navigation：`focus_completed`、`open_completed`、
  `navigate_completed` 和类型化 `browser_page` refs；
- snapshot：`snapshot_available`、`snapshot_truncated`、类型化
  `browser_snapshot` 与 `browser_element` refs；
- click/wait：`click_completed`、`snapshot_stale`、`wait_completed`；
- verification：`interaction_progress`、`interaction_goal_satisfied`、
  `interaction_verification_required`；
- terminal failures：`interaction_loop_detected`、
  `interaction_attempt_limit`、`interaction_verification_failed`、
  `unsafe_click_target`。

Workflow Profile 负责阶段迁移和完成判断。Agent Runtime 不能根据
`browser.interaction`、浏览器工具名、按钮标签或页面文本编写 switch。

## 持久化与恢复

在现有 Workflow state 中持久化 route、冻结的交互目标、target、page ref、当前
`snapshot_id`、选中元素语义、预期效果、点击次数、已访问页面状态 digest、
click history 和最新验证 assessment。进程或可见浏览器恢复后继续同一个
Workflow，并且必须在再次点击前重新获取 snapshot。

Workflow state 不能持久化浏览器 Cookie 或凭证；它们只保留在 SparkClaw 拥有
的 Chromium Profile 中。

## 已实现范围

首个完整功能切片包括：

1. Capability Catalog 和 decision corpus 中的 `browser.interaction` r1，
   同时保留 `browser.automation` r1。
2. 已有 ToolHub 工具上的精确逻辑能力和类型化 Outcome Adapter。
3. Workflow Profile、阶段迁移、受治理参数绑定、点击预算和完成评估。
4. 替代浏览器 snapshot 文本解析的结构化交互投影和 snapshot identity 契约。
5. 绑定到明确托管 page ID 的 snapshot/click，以及过期 ref 拒绝。
6. 路由边界、完整 Tool Exposure、Adapter、过期 ref、无 approval、点击后
   验证、循环检测、次数上限、恢复和 production-entry 端到端测试。
7. 已把通过验证的新路由登记到当前能力矩阵和架构状态。

## 已确认的首轮决策

第一版实现采用以下已确认选择：

1. 使用并列 leaf `browser.interaction` r1，而不是 `browser.automation` r2，保持
   单纯打开/聚焦行为稳定。
2. 只要 selected 当前 tab 可用就优先复用；否则复用唯一精确 URL 匹配或打开
   新 tab。
3. 本 Workflow 内有边界的点击不需要 approval。
4. 最多允许三次点击。每次点击后必须通过新的 snapshot 验证，才能完成或执行
   下一次点击。
5. 只要验证确认持续产生进展，就允许跨模型轮次循环；一旦页面状态或动作重复
   证明已经陷入循环，立即返回错误。
6. Workflow 生命周期内暴露完整且固定的 status/tab/navigation/snapshot/wait/
   click/verify 工具集，同时由状态机校验调用顺序。
7. Revision 1 不加入截图选点，也不加入 type/select 操作。
