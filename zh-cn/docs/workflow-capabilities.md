# Workflow 能力矩阵

> 语言：[English](../../docs/workflow-capabilities.md) | 简体中文

本文档列出当前 Workflow Runtime 实际对用户可用的能力，只统计已经注册、能够被选择并完成的 Workflow Profile。某个工具存在于 ToolHub，并不代表对应功能已经可用。

## 统一执行契约

- 每条 Owner Request 都先在 Fast 模型通道完成规范化与能力路由。
- 一旦选中 Workflow，后续每次模型调用都统一使用 Deep 模型通道。
- 对话上下文、附件、ToolResult 消息、Observation 顺序、压缩、Grounding 和渠道投递继续复用原有统一 Runtime 流程。
- Workflow Plan 不再包含 Skill ID，Workflow 执行也不加载任何 Skill 文本。版本化 Profile、活动 Capability Scope、Argument Binding、ToolHub Metadata 与 Policy 共同构成完整执行边界。
- Query、URL、Workspace Path、Location、Output Path 和 Outcome Ref 都从持久化状态物化；模型不能在后续 Stage 中替换这些冻结资源。
- 已匹配 Workflow 如果失败，会明确失败或阻断，不会回退到 TaskHint/ReAct 或其他能力。
- 浏览器、天气或文档请求如果超出当前 Profile Revision，会从 `unmatched` 转为 `blocked`，不能通过旧 ReAct 恢复。`files.read` 等共享读取工具只保留给代码检查等真正尚未迁移的领域。

## 当前 Profile

| Capability / Profile | 当前可实现 | 固定执行链 | 当前边界 |
|---|---|---|---|
| `browser.internet_search` r1 | 用冻结 Query 查询公开、时效性的互联网信息。天气预警、新闻、官方来源发现、多来源比较和空气质量调研也走这条通用搜索流程。 | 单阶段 `web.search`；启用时当前由 Infinimesh Info 提供结果。 | 不会打开搜索结果页面、读取已知 URL、操作登录态浏览器，也不会把直接天气问题渲染成卡片。Provider 必须已配置并返回有界结果。 |
| `browser.weather` r1 | 针对明确地点生成一张天气 PNG 卡片。卡片使用有依据的当前天气状况和当前温度、可选的当日最低/最高温，以及零到五条可获得的未来小时天气状况和温度。 | `info.query` -> `weather.structure_payload` -> `media.render_weather_card`；每个 Stage 同时只能看到一个工具。 | Info 是唯一天气数据来源。每个值都必须带有精确 Evidence Ref 和来源原文子串。当前天气、当前温度、当日温度范围或小时数据缺失时，必须写入 `missing_fields`，不能推测。纯文字天气、缺少地点、预警/新闻/来源比较和 AQI 不属于本 Profile。 |
| `browser.automation` r1 | 打开一个明确的 HTTP(S) URL 或运行时注册的站点；如果已经存在规范化 URL 完全相同的标签页，则聚焦该标签页。初始注册项为 QQ 邮箱（`https://mail.qq.com/`）。 | `browser.list_tabs` -> 精确命中时 `browser.focus`，否则 `browser.open`。注册名称解析为运行时冻结的 URL，而不是模型提供的值。 | 本 Profile 仍只负责打开/聚焦。页面交互由 `browser.interaction` 负责；输入、选择、截图、网页读取、登录认证和多 URL 操作仍不属于本 revision。未知站点名称仍需提供明确 URL。 |
| `browser.interaction` r1 | 检查一个托管的当前标签页、一个明确 HTTP(S) URL，或带页内目标的已注册站点，并执行最多三次经过验证的点击。优先复用可用的当前、精确匹配或空白标签页，再决定是否新开标签页。 | `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> 结构化 `browser.snapshot` -> `browser.click` -> 点击后 snapshot -> `browser.verify`。验证确认有进展时可继续点击闭环；固定九工具集全程可见，但每个 Stage 仍受 Capability Gate 约束。 | 点击不需要 approval，但每次都必须绑定最新 page/snapshot/ref identity，且成功后旧 ref 立即失效。重复状态立即失败，第三次仍为 progress 时返回 `interaction_attempt_limit`。输入、选择、上传/下载、登录认证、凭证、验证码/2FA、付款/购买、表单提交、截图、任意脚本和不安全的后果性操作不属于 r1。 |
| `document.read` r1 | 读取或总结一个明确的受治理 Workspace 文件。支持识别文本、DOCX、XLSX、PPTX 和 PDF。 | 先确定性校验路径与类型，再对文本/Office 使用 `files.read`，对 PDF 使用 `pdf.extract_text`。 | 路径必须位于配置的 Workspace 内，并指向一个已存在的普通非符号链接文件；扩展名必须与文件签名或包类型一致。不支持文件搜索、多文件比较、修改和任意外部路径。 |
| `document.edit` r1 | 对一个受治理的 DOCX、XLSX、PPTX 或 PDF 执行一次受支持的有界编辑，并在同目录写入新副本。 | 先确定性校验路径、类型和操作，再只物化一个匹配格式与操作的编辑工具。输出路径冻结为 `<name>-sparkclaw-edit.<ext>`。 | 操作必须明确，输入必须通过与文档读取相同的 Workspace 校验，输出文件不能已经存在，且可逆写操作需要审批。不支持纯文本编辑、新建文档、通用文件删除、多文件编辑和未声明操作。 |

## 文档编辑操作

| 格式 | Revision 1 支持的操作 |
|---|---|
| DOCX | `replace_paragraph`、`insert_paragraph`、`delete_paragraph`、`set_text_style` |
| XLSX | `update_cell`、`insert_row`、`delete_row`、`update_row`、`append_row` |
| PPTX | `add_slide`、`duplicate_slide`、`delete_slide`、`replace_text` |
| PDF | `extract_pages`、`delete_pages`、`rotate_pages`、`split` |

## 过渡能力

代码/命令辅助、图片检查、记忆、提醒和其他尚未迁移的领域仍使用过渡 TaskHint/ReAct 链路，对应 Legacy Skill 暂时保留。已经迁移的浏览器、公开搜索、天气和文档 Skill 包已删除，旧 Skill Candidate 也不能重新暴露这些能力。尚未列入当前 Workflow 的已注册工具继续保留在 ToolHub 中，等待后续迁移，但不能作为已经可用的 Workflow 功能对外宣称。
