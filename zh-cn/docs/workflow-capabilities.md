# Workflow 能力矩阵

> 语言：[English](../../docs/workflow-capabilities.md) | 简体中文

本文档列出当前 Workflow Runtime 实际对用户可用的能力，只统计已经注册、能够被选择并完成的 Workflow Profile。某个工具存在于 ToolHub，并不代表对应功能已经可用。

## 统一执行契约

- 每个自然语言 Owner 问题都通过非对称双通道进入同一语义图。Embedding 只接收该问题；
  Fast/Tree 接收同一问题加有界 typed context，只给候选评分，不改写请求或选择资源。
  候选无关的 Grounding、加权 Fusion 和最终 Top-2 决策最多选择一个经 Catalog 校验的叶子。
- 一旦选中 Workflow，后续每次模型调用都统一使用 Deep 模型通道。
- 对话上下文、附件、ToolResult 消息、Observation 顺序、压缩、Grounding 和渠道投递继续复用原有统一 Runtime 流程。
- Workflow Plan 不再包含 Skill ID，Workflow 执行也不加载任何 Skill 文本。版本化 Profile、活动 Capability Scope、Argument Binding、ToolHub Metadata 与 Policy 共同构成完整执行边界。
- Query、URL、Workspace Path、Location、Output Path 和 Outcome Ref 都从持久化状态物化；模型不能在后续 Stage 中替换这些冻结资源。
- 已匹配 Workflow 如果失败，会明确失败或阻断，不会回退到 TaskHint/ReAct 或其他能力。
- `unmatched` 路由会终止为 `router.blocked`，不会暴露 TaskHint Candidate、工具或 ReAct。
- 浏览器、天气或文档请求如果超出当前 Profile Revision，会从 `unmatched` 转为 `blocked`，不能通过旧 ReAct 恢复。

## 当前 Profile

| Capability / Profile | 当前可实现 | 固定执行链 | 当前边界 |
|---|---|---|---|
| `conversation.answer` r1 | 根据 Owner Request 与对话上下文回答问候、稳定常识和简单解释。 | 单次无工具 Deep 模型回答，记录为 `workflow_answer`。 | 不能使用当前互联网事实、Workspace 证据、账户数据、工具、审批或动作；这些请求必须匹配其他已注册能力。 |
| `browser.internet_search` r1 | 用冻结 Query 查询公开、时效性的互联网信息。天气预警、新闻、官方来源发现、多来源比较和空气质量调研也走这条通用搜索流程。 | 单阶段 `web.search`；启用时当前由 Infinimesh Info 提供结果。 | 不会打开搜索结果页面、读取已知 URL、操作登录态浏览器，也不会把直接天气问题渲染成卡片。Provider 必须已配置并返回有界结果。 |
| `browser.weather` r1 | 针对明确地点生成一张天气 PNG 卡片。卡片使用有依据的当前天气状况和当前温度、可选的当日最低/最高温，以及零到五条可获得的未来小时天气状况和温度。 | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`；每个 Stage 同时只能看到一个工具。 | Info 是唯一天气数据来源。每个值都必须带有精确 Evidence Ref 和来源原文子串。当前天气、当前温度、当日温度范围或小时数据缺失时，必须写入 `missing_fields`，不能推测。纯文字天气、缺少地点、预警/新闻/来源比较和 AQI 不属于本 Profile。 |
| `browser.automation` r1 | 打开一个明确的 HTTP(S) URL 或运行时注册的站点，或者聚焦已经符合冻结目标的标签页。明确 URL 仍要求规范化后完全相等；注册站点还可以在注册表约束下匹配站点跳转后的主机或真实子域。初始注册项为 QQ 邮箱（`https://mail.qq.com/`）。 | `browser.list_tabs` -> 存在合格目标页时 `browser.focus`，否则 `browser.open`。注册名称解析为运行时冻结的 URL，而不是模型提供的值。 | 本 Profile 仍只负责打开/聚焦，不会复用无关的现有标签页，显式打开的结果在完成后继续保留。页面交互由 `browser.interaction` 负责；输入、选择、截图、网页读取、登录认证和多 URL 操作仍不属于本 revision。未知站点名称仍需提供明确 URL。 |
| `browser.interaction` r1 | 检查一个托管的当前标签页、一个明确 HTTP(S) URL，或带页内目标的已注册站点，并执行最多三次经过验证的点击。优先聚焦已经符合目标的标签页；只有不存在目标匹配时，selected 空白页才作为后备。 | `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> 结构化 `browser.snapshot` -> `browser.click` -> 点击后 snapshot -> `browser.verify`。验证确认有进展时可继续点击闭环；Tool Exposure 持久化固定九工具边界，而每轮模型只看到活动 Stage 的子集。 | 所有参数都绑定到持久化 ref。显式打开/进入/访问的结果在成功后保持打开，本 Workflow 不会自动执行 `browser.close`；无关的现有标签页不会被复用。重复状态立即失败，第三次仍为 progress 时返回 `interaction_attempt_limit`。输入、选择、上传/下载、登录认证、凭证、验证码/2FA、付款/购买、表单提交、截图、任意脚本和不安全的后果性操作不属于 r1。 |
| `document.read` r2 | 读取或总结一个从当前请求或最近文档记录解析出的明确受治理 Workspace 文件。支持识别文本、DOCX、XLSX、PPTX、PDF 和图片。 | 最近文档解析和确定性路径/类型 preflight -> 持久化 `confirm_document_target` 证据 -> 按冻结格式使用 `files.read`、`pdf.extract_text` 或 `images.inspect`。 | 唯一的最近文档可以在无需再次附加的情况下满足追问。必须持久化身份、来源和活动元数据，但不要求精确保存解析结果。路径必须指向 Workspace 内已存在的普通非符号链接文件。不支持文件搜索、多文件比较、修改和任意外部路径。 |
| `document.edit` r5 | 对一个受治理的 text、DOCX、XLSX、PPTX 或 PDF 执行一次受支持的有界编辑，并写入一个或多个可追溯输出副本。 | 确定性路径/类型 preflight -> `confirm_document_target` -> Runtime 在 `document_locate_evidence` 中按冻结格式直接调用一次 reader -> 显式且有重试上限的 `select_edit_operation` 决策 -> `document_edit` 只物化已持久化的格式/operation entry。默认输出路径冻结为 `<name>-sparkclaw-edit.<ext>`；已有副本及其后续编辑会依次递增为 `-2`、`-3` 等。 | 定位阶段不会询问模型是否调用 reader，也不会重复读取。多候选 operation 选择只在证据产生后由 Deep 完成，单候选格式确定性选择；决策缺失或无效时显式 block。可逆写入需要审批，每个输出都关联 parent 和 activity。唯一的最新输出只在当前请求选中文档 Workflow 时绑定；无关对话不会继承它。 |
| `schedule.manage` r2 | 通过对话或类型化 WebChat 任务栏操作创建、列出、修改或取消定时任务。 | Create/Read 为单阶段；Edit/Delete 执行 `reminders.list` -> 唯一 pending 目标与冻结版本 -> `reminders.update` 或 `reminders.cancel`；全部写入使用 `ScheduleRegistry` Compare-and-Swap。 | 任务栏 ID 只是 Hint，必须存在于最新 owner 范围列表。到期内容重新进入普通 Message Runtime，Timer 不选择能力也不直接发送；编辑保持原提醒端。 |

## 文档编辑操作

| 格式 | 支持的 Edit Operation |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`、`replace_paragraph`、`insert_paragraph`、`delete_paragraph`、`set_text_style` |
| XLSX | `replace_text`、`update_cell`、`insert_row`、`delete_row`、`update_row`、`append_row` |
| PPTX | `replace_text`、`add_slide`、`update_slide`、`duplicate_slide`、`delete_slide` |
| PDF | `extract_pages`、`delete_pages`、`rotate_pages`、`split` |

## 未迁移能力

代码/命令辅助、图片检查、记忆和其他没有注册 Workflow 的领域会终止为 `unmatched`。Legacy Skill 与 ReAct 恢复代码仅保留用于兼容已持久化运行。当前 Workflow 矩阵之外的已注册工具继续保留在 ToolHub 中等待后续迁移，但不能作为可用功能对外宣称。
