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
- 已匹配 Workflow 如果失败，会明确失败或阻断，不会回退到 TaskHint、通用回退循环或其他能力。
- `unmatched` 路由会终止为 `router.blocked`，不会暴露 TaskHint Candidate、工具或任何回退执行器。
- 浏览器、天气或文档请求如果超出当前 Profile Revision，会从 `unmatched` 转为 `blocked`，不存在可恢复的旧回退路径。

## 当前 Profile

| Capability / Profile | 当前可实现 | 固定执行链 | 当前边界 |
|---|---|---|---|
| `conversation.answer` r1 | 根据 Owner Request 与对话上下文回答问候、稳定常识和简单解释。 | 单次无工具 Deep 模型回答，记录为 `workflow_answer`。 | 不能使用当前互联网事实、Workspace 证据、账户数据、工具、审批或动作；这些请求必须匹配其他已注册能力。 |
| `browser.internet_search` r1 | 用冻结 Query 查询公开、时效性的互联网信息。天气预警、新闻、官方来源发现、多来源比较和空气质量调研也走这条通用搜索流程。 | 单阶段 `web.search`；启用时当前由 Infinimesh Info 提供结果。 | 不会打开搜索结果页面、读取已知 URL、操作登录态浏览器，也不会把直接天气问题渲染成卡片。Provider 必须已配置并返回有界结果。 |
| `browser.weather` r1 | 针对明确地点生成一张天气 PNG 卡片。卡片使用有依据的当前天气状况和当前温度、可选的当日最低/最高温，以及零到五条可获得的未来小时天气状况和温度。 | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`；每个 Stage 同时只能看到一个工具。 | Info 是唯一天气数据来源。每个值都必须带有精确 Evidence Ref 和来源原文子串。当前天气、当前温度、当日温度范围或小时数据缺失时，必须写入 `missing_fields`，不能推测。纯文字天气、缺少地点、预警/新闻/来源比较和 AQI 不属于本 Profile。 |
| `browser.automation` r2 | 取得一个明确 HTTP(S) URL 或运行时注册的站点，用 hidden 与 visible snapshot 证明结果，并保持已验证的可视页面打开。明确 URL 要求规范化后完全相等；注册站点还可以按 registry 约束匹配站点跳转后的主机或真实子域。初始注册项为 QQ 邮箱（`https://mail.qq.com/`）。 | 被动 `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> settle -> hidden snapshot 与 route 校验 -> visible open/focus -> settle -> visible snapshot 与 route 校验。结构性阶段由 Runtime 直接调用，每次 navigation 后都有新的 settled snapshot。 | 不复用无关 tab。可复用且已初始化的 profile 会直接打开目标，不暴露 `about:blank` 或重新要求登录。visible 校验前 run 不能成功，生产流程不会关闭结果 tab。click、输入、表单、截图、任意网页读取和多 URL 操作不属于本 profile。浏览器 r1 已退役。 |
| `browser.interaction` r2 | 检查一个托管当前 tab、明确 HTTP(S) URL 或带页内目标的注册站点，执行最多三次独立验证、ref-bound click，并保持已验证的可视结果打开。优先 focus 合格目标 tab；只有无目标匹配时，一个 selected 空白 tab 才作为后备。 | automation r2 acquisition 链 -> hidden `browser.assess_goal` -> 有界 `browser.click` -> settle -> fresh snapshot -> `browser.validate_transition` -> `browser.assess_goal`；有进展时可重复 action loop。完成时再执行 visible open/focus -> settle -> fresh snapshot -> 第二次 `browser.assess_goal`。持久化十 capability 边界按 active stage 投影。 | 所有参数都绑定到持久化、generation-scoped ref。stale ref/generation、重复状态、route divergence、transition failure 或第三次 progress 都会 fail closed。登录和人工验证进入持久化 owner handoff；歧义回复不调用浏览器，恢复要求可视认证/任务证据匹配，并生成 fresh hidden evidence。type、select、upload/download、凭据、captcha/2FA、payment/purchase、form submit、截图、任意脚本和 unsafe consequential action 不属于 r2。 |
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

代码/命令辅助、图片检查、记忆和其他没有注册 Workflow 的领域会终止为 `unmatched`。旧的通用循环已删除；恢复迁移前持久化的运行会以明确的"旧运行时已下线"消息终止。当前 Workflow 矩阵之外的已注册工具继续保留在 ToolHub 中等待后续迁移，但不能作为可用功能对外宣称。
