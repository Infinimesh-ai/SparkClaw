# Workflow 能力矩阵

> 语言：[English](../../docs/workflow-capabilities.md) | 简体中文

本文档列出当前 Workflow Runtime 实际对用户可用的能力，只统计已经注册、能够被选择并完成的 Workflow Profile。某个工具存在于 ToolHub，并不代表对应功能已经可用。

## 统一执行契约

- 每个自然语言 Owner 问题都通过非对称双通道进入同一语义图。Embedding 只接收该问题；
  Fast/Tree 接收同一问题加有界 typed context，只给候选评分，不改写请求或选择资源。
  候选无关的 Grounding、加权 Fusion 和最终 Top-2 决策最多选择一个经 Catalog 校验的叶子。
- 每个 Workflow Profile 选择自己的执行通道。文档读取/编辑的模型调用当前使用 Fast；
  其他 Profile 保持 Deep 默认值。
- 对话上下文、附件、ToolResult 消息、Observation 顺序、压缩、Grounding 和渠道投递继续复用原有统一 Runtime 流程。
- Workflow Plan 不再包含 Skill ID，Workflow 执行也不加载任何 Skill 文本。版本化 Profile、活动 Capability Scope、Argument Binding、ToolHub Metadata 与 Policy 共同构成完整执行边界。
- Query、URL、Workspace Path、Location、Output Path 和 Outcome Ref 都从持久化状态物化；模型不能在后续 Stage 中替换这些冻结资源。
- 已匹配 Workflow 如果失败，会明确失败或阻断，不会回退到通用执行器或其他能力。
- `unmatched` 路由会终止为 `router.blocked`，不会暴露工具或任何回退执行器。
- 浏览器、天气或文档请求如果超出当前 Profile Revision，会从 `unmatched` 转为 `blocked`，不存在可恢复的旧回退路径。

## 当前 Profile

| Capability / Profile | 当前可实现 | 固定执行链 | 当前边界 |
|---|---|---|---|
| `conversation.answer` r2 | `answer` 变体回答问候、稳定常识和简单解释；`publish` 变体把当前有序 text、image、audio 和 file part 作为一条普通消息返回。 | `answer` 执行一次无工具 Deep 模型回答；`publish` 不调用模型或工具，只治理 workspace 附件，并把规范化后的请求 `MessageContent` 冻结为 Workflow result。只有媒体 part 的 Web 请求无需合成文本或调用语义模型，确定性选择 `publish`；存在媒体时移除命令文本，只保留 image/audio/file part。 | `answer` 不能使用当前事实、受治理资源或动作；`publish` 不能检查、转换、编辑或定时发送 message part。delivery target 只存在于 `ReturnRoute`。发往当前选中第三方 endpoint 的纯媒体消息无需审批，也不生成来源 WebChat assistant result；外部纯文本 result 仍需发送审批。 |
| `external_mcp.workspace` r1 | 在显式配置的 LocalMind workspace 中读取、搜索、创建、编辑、删除或交互，操作范围受 workspace-bound MCP credential 的可见能力限制。 | 直接刷新 capability -> 在最多 16 个不含 schema 的匹配 directory entry 中选择 operation -> 持久化唯一选择 -> 只物化并执行该 `localmind.*` tool -> 基于有界、不可信 evidence 做模型终结。Resource list/template/read wrapper 进入 read route。 | 必须显式配置 LocalMind，且 server identity、protocol、endpoint path、scope snapshot 和 tool catalog 必须一致。`allow_mutations` 默认 false。read 无需审批；启用的 mutation 全部需要 owner approval，destructive/open-world operation 还需要 deep verification。返回内容不能扩大 scope 或授权其他 operation。目前只支持 LocalMind；认证刷新后绝不重放 mutation。 |
| `browser.internet_search` r1 | 用冻结 Query 查询公开、时效性的互联网信息。天气预警、新闻、官方来源发现、多来源比较和空气质量调研也走这条通用搜索流程。 | 单阶段 `web.search`；启用时当前由 Infinimesh Info 提供结果。 | 不会打开搜索结果页面、读取已知 URL、操作登录态浏览器，也不会把直接天气问题渲染成卡片。Provider 必须已配置并返回有界结果。 |
| `browser.weather` r1 | 针对明确地点生成一张天气 PNG 卡片。卡片使用规范化的当前天气状况和温度、匹配日期的最低/最高温，以及最多五条可用的未来小时数据。 | `weather.lookup` -> `media.render_weather_card`。冻结 route location 绑定到 typed `POST /v1/info/weather` 请求；渲染 Stage 只接收已完成 lookup 的 ref。 | Infinimesh Info 是唯一天气来源，并固定使用 metric 单位。malformed、不完整、不支持单位或鉴权失败会明确失败，不回退到通用 query/search 或解析路径。坐标、AQI 和 alert 不进入卡片 payload。纯文字天气、缺少地点、预警/新闻/来源比较和空气质量调研不属于本 Profile。 |
| `browser.automation` r2 | 取得一个明确 HTTP(S) URL、运行时注册站点或命名公网网站，用 hidden 与 visible snapshot 证明结果，并保持已验证的可视页面打开。现有 destination registry 始终先查询；命名目标未命中时使用 Info 有序结构化结果中首个通过安全校验的 URL。 | 命名目标未命中时先 hidden 执行 `web.search` -> `browser.identify_public_target`，然后被动 `browser.status` -> `browser.list_tabs` -> focus/navigate/open -> settle -> hidden snapshot 与 route 校验 -> 可选 owner 明确请求的视觉检查 -> visible open/focus -> settle -> visible snapshot 与 route 校验。 | Runtime 信任 Info 的相关性排序，但所选 URL 和每次 redirect 都必须是公网 HTTPS。不复用无关 tab；visible 校验前 run 不能成功，生产流程不会关闭结果 tab。click、输入、表单、任意网页读取和多 URL 操作不属于本 Profile。浏览器 r1 已退役。 |
| `browser.page_read` r1 | 在托管 headless Chromium 中读取、总结或提取一个明确 URL 或命名公网页面的有界内容。 | 可选 Info 目标识别 -> hidden `browser.status` -> 对冻结 URL hidden `browser.open` -> hidden `browser.read`，同时设置 `require_browser_session=true` 并复用 active page -> 校验最终 URL 和内容。 | 固定 health/open/read 链不能跳过浏览器取得、不能进入 visible presentation，也不能回退 direct HTTP。登录或人工验证进入共享 owner handoff。开放式调研仍属于 `browser.internet_search`；页面修改和多 URL 不属于本 Profile。成功读取后若最终模型生成不可用，Runtime 会把有界提取内容作为不可信证据返回。 |
| `browser.interaction` r2 | 检查一个托管当前 tab、明确 HTTP(S) URL、注册站点或经 Info 识别的公网网站，执行最多三次独立验证、ref-bound click，并保持已验证的可视结果打开。 | 必要时先识别目标 -> automation r2 acquisition -> hidden `browser.assess_goal` -> 有界 `browser.click` -> settle -> fresh snapshot -> `browser.validate_transition` -> `browser.assess_goal`；有进展时可重复 action loop。完成时可加入一次 owner 明确请求的视觉检查，再执行 visible open/focus -> settle -> fresh snapshot -> 第二次 `browser.assess_goal`。 | 所有参数都绑定到持久化 session/page generation 和 fresh ref。stale evidence、重复状态、route divergence、transition failure 或第三次 progress 都会 fail closed。登录和人工验证进入持久化 owner handoff。type、select、upload/download、凭据、captcha/2FA、payment/purchase、form submit、任意脚本和 unsafe consequential action 不属于 r2。 |
| `browser.form_draft` r1 | 在普通可逆字段中 type 或 select 最多五个由 owner 原文提供的精确值，不提交表单。目标可以是当前托管 tab、明确 URL、注册站点或经 Info 识别的公网网站。 | hidden interaction 风格 acquisition -> fresh structured snapshot -> 初始 `browser.assess_goal` -> visible open/focus -> settle -> fresh visible snapshot 与评估 -> 一次独立审批的 visible `browser.type` 或 `browser.select` -> settle -> fresh 且 page generation 更高的 visible snapshot -> draft 校验；在同一 visible session 中按五次上限重复，并在不重新打开目标的情况下原位验证最终草稿。 | draft stage 不暴露 click 或可提交 tool。每个 approval 绑定一个字段、operation、精确值、snapshot digest、page ID 和 session/page generation；approval 前和执行时都会复核 freshness 与字段安全。password、passkey、token、OTP、captcha、payment、purchase、delete、upload、submit、send 和 publish control 一律拒绝。值在 approval summary 与持久化浏览器 projection 中会被脱敏。 |
| `document.read` r4 | 读取、总结一个从当前请求或最近文档记录解析出的明确受治理 Workspace 文件，或逐字提取图片内原文。支持识别文本、DOCX、XLSX、PPTX、PDF 和图片。 | 最近文档解析和确定性路径/类型 preflight -> 持久化 `confirm_document_target` 证据 -> Runtime 按冻结格式直接调用一次 `files.read`、`pdf.extract_text` 或 `images.inspect` -> Fast 基于已完成证据生成最终回答。`images.inspect` 并行运行可选 OCR 与 Fast 视觉语义，并明确分类有文字/无文字。PDF 页面先经过确定性原生文字分类，只有 empty/degraded 页面进入有界 OCR；finalization 接收类型化 complete/partial/unavailable coverage。 | 唯一的格式限定 reader 是 `direct_once`，模型不决定是否调用它。OCR 始终是不可信证据，不是模型 lane 或独立 Workflow。图片 OCR 禁用或失败时会明确降级到 Fast 视觉证据。PDF partial read 会精确列出缺失页面，不能声称完整覆盖。OCR readiness 区分配置状态和构造状态；有界、owner 隔离的进程缓存会合并相同 miss，fresh call 则持久化 `document_ocr` model-call provenance。唯一的最近文档可以在无需再次附加的情况下满足追问。路径必须指向 Workspace 内普通非符号链接文件。不支持文件搜索、多文件比较、修改和任意外部路径。 |
| `document.edit` r6 | 对一个受治理的 text、DOCX、XLSX、PPTX 或 PDF 执行一次受支持的有界编辑，并写入一个或多个可追溯输出副本。XLSX 支持类型化 cell/row 证据、只更新前缀的行修改和已验证 OOXML package preservation；PPTX 支持精确文本、单页、有界整份及结构 scope。 | 确定性路径/类型与格式专属 scope preflight -> `confirm_document_target` -> Runtime 在 `document_locate_evidence` 中按冻结格式直接调用一次 reader -> 显式且有重试上限的 `select_edit_operation` 决策 -> `document_edit` 只物化已持久化的格式/operation entry。XLSX 选择与执行共享有界 `xlsx_sheet_evidence_v1`，Runtime 在 approval 前绑定工作簿和目标 hash；PPTX 绑定当前读取中的 slide、shape、layout 与 template 证据；PDF transform 使用只包含已选 operation 字段的 strict schema。默认输出为 `<name>-sparkclaw-edit.<ext>`，后续副本依次递增为 `-2`、`-3` 等。 | 所有由模型驱动的文档阶段都使用 Fast。定位阶段不会询问模型是否调用 reader，也不会重复读取。operation/target 证据缺失、无效、不受支持、过期或有歧义时，在修改前显式 block。含未验证特性的 XLSX package 在 approval 前阻断，写后出现未声明 part drift 时删除输出。PPTX scope 歧义会澄清，SmartArt、动画、图表数据、母版和宏目标会阻断。PDF 页码数组是唯一的正整数且从 1 开始，rotation 是六值 enum，`merge` 不可用。可逆写入需要审批，每个输出都关联 parent 和 activity；无关对话不会继承最新文档。 |
| `schedule.manage` r2 | 通过对话或类型化 WebChat 任务栏操作创建、列出、修改或取消定时任务。 | Create/Read 为单阶段；Edit/Delete 执行 `reminders.list` -> 唯一 pending 目标与冻结版本 -> `reminders.update` 或 `reminders.cancel`；全部写入使用 `ScheduleRegistry` Compare-and-Swap。 | 任务栏 ID 只是 Hint，必须存在于最新 owner 范围列表。到期内容重新进入普通 Message Runtime，Timer 不选择能力也不直接发送；编辑保持原提醒端。 |
| `coding.agent_manage` r1 | 通过已配置的 Happy Team 与个人 bridge MCP 端点读取和管理编码 Agent 的任务与会话。 | 单个活动 Stage 只暴露已发现的 `mcp.external` 工具。读取请求可以调用任务/会话列表、详情、计划、机器和 transcript 工具；创建、启动、发消息、停止和取消走正常审批路径。 | MCP observation 是不可信数据，使用前必须摘要。个人 bridge 离线不会停用 Team 任务工具。计划批准/拒绝不进入聊天，只属于人工审批收件箱。 |

## 文档编辑操作

| 格式 | 支持的 Edit Operation |
|---|---|
| Text | `replace_text` |
| DOCX | `replace_text`、`replace_paragraph`、`insert_paragraph`、`delete_paragraph`、`set_text_style` |
| XLSX | `replace_text`、`update_cell`、`insert_row`、`delete_row`、`update_row`、`append_row` |
| PPTX | `replace_text`、`add_slide`、`update_slide`、`update_deck`、`duplicate_slide`、`delete_slide` |
| PDF | `extract_pages`、`delete_pages`、`rotate_pages`、`split` |

PPTX 文本编辑会保留受支持的 paragraph、run、bullet 与 hyperlink 结构，并拒绝含 field 的目标。
整份更新是原子的，上限为 12 页、64 个形状与 32 KiB replacement text。新增页使用当前读取中的
layout 或 template-slide 引用及证据绑定位置；含 notes 的 clone 来源会被拒绝。所有 PPTX 编辑
共用 125,000 ms 端到端 deadline，超时会清理部分输出并返回
`document_operation_timeout`。共享 Pipeline 尚不能精确区分 `reread`/`preserve` 超时 stage。

## 未迁移能力

代码/命令辅助、图片检查、记忆和其他没有注册 Workflow 的领域会终止为 `unmatched`。旧的通用循环已删除；恢复迁移前持久化的运行会以明确的"旧运行时已下线"消息终止。当前 Workflow 矩阵之外的已注册工具继续保留在 ToolHub 中等待后续迁移，但不能作为可用功能对外宣称。
