# 导出 AI 平台对话：登录、脚本控制与 workspace 保存设计

> 2026-09-10 更新：新增 [RevivalStack 四平台时间线批量导出](../../docs/ai-conversation-batch-export.md)脚本及独立自动化入口；已通过[四平台真实短对话评测](../../docs/ai-conversation-live-eval-20260910.md)，当前覆盖可见历史，未证明账号全部历史完整性，尚未接入 Gateway 自然语言批量路由。下文单条范围继续描述原有工作流。


> 语言：简体中文 | [English](../../docs/ai-conversation-export-design.md)

日期：2026-09-10。状态：**四分支能力与单条 JSON 导出／workspace 保存已在代码中实现，尚未部署；四平台真实账号验收未完成**。本阶段不做摘要、记忆提取、向量化或记忆服务接线。此前批量与记忆流水线设计以本文范围为准；批量可在单条闭环稳定后扩展，不作为当前闭环的前置条件。

## 1. 当前交付目标

SparkClaw 在专用 Chromium 的已登录平台对话页控制 RevivalStack 油猴脚本，触发导出，确认取得真实文件，并将原始结果保存到**当前 SparkClaw 会话的 workspace**，返回可供后续工具使用的文件路径。

完整链路为：`定位对话 → 检查脚本 → 触发 JSON 导出 → 等待下载完成 → 校验文件 → 保存到 workspace → 返回文件回执`。

默认保存原始 JSON，按需额外保存原始 Markdown。保存阶段不改写正文、不生成摘要；脚本输出保持原样，来源、校验和与缺口另写清单。用户关心的是对话文字、角色、顺序与流程；不要求排版与网页完全一致，但不能主动删除可取得的代码／表格文字。

## 2. 采用方案与现有证据

唯一导出基础是 [RevivalStack / ai-chat-exporter](https://github.com/revivalstack/ai-chat-exporter)。已检查基线为 v3.1.0、提交 [`4ce218d1290956703489870aafa2adfdf4448f38`](https://github.com/revivalstack/ai-chat-exporter/tree/4ce218d1290956703489870aafa2adfdf4448f38)，MIT 许可允许保留版权／许可后修改分发。先控制现有脚本；若自动化入口或状态不足，再维护独立名称、版本和上游来源记录的派生脚本，不以另建完整脚本为第一步。

已在专用 Chromium 中真实点击 RevivalStack 的 Markdown 和 JSON 按钮，取得 ChatGPT 短对话的两个文件：Markdown 1232 字节、JSON 1174 字节，保留一问一答、3 个代码块正文和表格文字。JSON 的 `count=1` 表示一个问答组，实际含 2 条消息。表格行内代码样式和一个代码块语言标记丢失，不影响本样本的主要文字内容。未证明长对话、其他平台或批量完整性。

当时两个导出脚本同时启用，连续点击曾未产生新文件，换任务页后成功；之后 Controller 返回 `browser_extension_unavailable`。原因未确定，因此不能把一次文件下载成功等同于稳定自动化已完成。

本次将此前真实下载的 RevivalStack JSON／Markdown 逐字节复制到本项目开发 workspace：`data/workspaces/ai-chat-exports/chatgpt/6aa22545-5ecc-83e8-ae2e-76c55897cf15/20260910-live-sample/`，附 `manifest.json` 和说明。此为已有证据的归档，不是本次重新导出，也不是生产保存流程已接通。

## 3. 控制方式与组件分工

沿用[浏览器运行时](browser-runtime.md)和[浏览器控制设计](playwright-extension-browser-design.md)的任务隔离机制。油猴负责运行脚本；脚本负责读取页面并生成文件；SparkClaw 负责页面操作、下载接收、workspace 保存和回执。

使用选定浏览器 profile 的登录态，在任务专属标签页打开用户指定的对话链接。用户当前页可作为来源，任务不接管、导航或关闭用户原标签页。无需额外平台 API Key，不复制 Cookie 或令牌。

先使用现有快照、点击和原生下载能力识别 `↓ JSON`／`↓ Export MD` 控件。每次操作前刷新快照，不复用过期 ref。自动化测试只启用待测导出脚本。读取前核对来源平台、对话 ID、页面加载／生成状态，以及导出消息选择范围，避免手动筛选造成漏导。

如现有脚本无法可靠声明范围和状态，最小改造为增加固定的“导出当前对话全部已加载消息”入口、导出任务标记及状态／错误提示。它不能把“全部已加载”冒充“全部历史”。现有 Controller 不提供任意脚本执行或油猴管理接口，不假定这些能力存在；后续固定接线仍遵守任务归属。

脚本输出和网页内容作为数据处理。当前功能不编辑、删除、归档或分享网站对话，不连接记忆服务；不新增跨项目接口。

## 4. 单条执行流程

1. **定位**：取得当前会话的 workspace 根目录与目标对话 URL，建立任务页并验证平台／对话身份。
2. **检查**：确认登录、脚本控件和选择范围可用，等待正文加载完成；按钮不存在时报告脚本未就绪，不能输出空文件作为成功。
3. **接收准备**：先登记本次任务的下载等待，再点击导出。将下载绑定到任务页和本次操作，不能扫描全局 Downloads 目录并把其他文件当成本次结果。
4. **导出**：默认触发 JSON；Markdown 为独立的可选导出操作。网站切换或多个任务并发不能混用回执。
5. **校验**：下载必须完成且为可读普通文件，JSON 可解析、消息结构有效、来源 URL／对话 ID 匹配；记录消息数与采集范围。解析成功本身不证明正文完整。
6. **保存**：在 workspace 内创建独立采集目录，原字节写入临时文件，核对大小和 SHA-256 后原子提交，写入清单；在释放浏览器任务会话前完成持久保存。
7. **回执**：返回 workspace 相对路径、格式、字节数、哈希、消息数、来源及 `saved / partial / failed` 状态。成功必须对应实际存在的文件。

已保存文件可直接被后续读取、搜索、处理工具使用。后续处理不属于本阶段，也不因保存动作自动触发。

## 5. workspace 文件布局与回执

产品代码使用当前会话解析出的 `WorkspaceRoot`，不可硬编码开发仓库、浏览器下载目录或模型临时产物目录。所有 AI 平台对话导出集中在 workspace 下独立的 `ai-chat-exports/` 文件夹，不写入 `uploads/`、`media/`，也不把 JSON 散放到 workspace 根目录。建议布局：

```text
<WorkspaceRoot>/ai-chat-exports/<platform>/<conversation-key>/<capture-id>/
  <对话标题>-<平台名>.json
  <对话标题>-<平台名>.md  # 后续可选
  manifest.json
```

JSON 文件名取脚本原始 JSON 的 `title`，例如 `旅行计划-ChatGPT.json`；平台显示名固定为 ChatGPT、Claude、Gemini、Grok。标题缺失／空白时使用 `未命名对话`。替换路径分隔符、控制字符及不可移植文件名字符，按 UTF-8 边界将标题限制在 180 字节；原始 JSON 内容保持不变。回执与清单使用实际文件名。同名对话与重复采集由独立子目录隔离，不覆盖已有文件。

`conversation-key` 使用经路径安全处理的对话标识；跨账号需要隔离作用域，不能只用标题或 profile 名称推断账号。`capture-id` 为唯一采集标识，防止同名或重复导出覆盖旧结果。限制文件名长度，拒绝路径穿越、符号链接逃逸和 workspace 外写入，复用已有 workspace 文件安全机制。

清单记录：格式版本、平台、对话 ID、来源 URL、可确认的账号作用域、脚本版本、导出／保存时间、任务标识、文件相对路径、字节数、SHA-256、消息数、覆盖和告警。脚本版本或源消息时间未知时保持未知；导出时间不冒充原消息时间。

JSON 和 Markdown 均为**脚本原始输出**，不是无损网页快照。若生成规范化记录，应在未来阶段另存，不能覆盖原文件。本阶段以独立采集目录避免覆盖，不要求实现语义增量去重。

保存成功与采集完整性分开表示：文件可以已保存但正文覆盖为 `partial / unknown`。下载未完成、文件校验失败或 workspace 写入失败均不能返回 `saved`。部分文件可保留为明确标记的失败证据，不发布为正式结果。

## 6. 异常与生命周期

状态为 `pending → page_ready → exporting → downloaded → validating → saving → saved`，另有 `partial / failed / canceled`。逐次操作记录状态、错误和产物引用，日志不写对话正文或凭据。

- 登录失效、账号变化、无权限：暂停受影响操作，重新验证来源后继续。
- 脚本未就绪或页面结构变化：明确报错，不能用整页文字代替脚本结果。
- 点击后无下载：超时后报告 `download_timeout`，诊断下载限制与脚本状态；不无限点击。
- 控制连接丢失：报告 `browser_extension_unavailable`，重新获得任务页和新快照后有界重试。
- 下载成功但保存失败：保留可用暂存文件，重试保存；不能先释放会话再尝试访问已被清理的目录。
- 保存成功后回执失败：按采集 ID 查找并校验现有清单，返回已有结果，避免重写覆盖。
- 取消：停止后续操作并清理未完成临时文件，保留已成功保存的结果。

首版设置可配置的下载等待、文件大小和重试上限，触顶报告原因。若暂存只存在于 Controller 会话目录，必须明确其释放后可能消失；workspace 持久化完成前不视为交付。

## 7. 当前验收与后续边界

| 当前验收项 | 要求 |
|---|---|
| 脚本控制 | 专用 Chromium 上实际触发 RevivalStack；目标／范围正确；不干扰用户原标签页 |
| 真实文件 | 原生下载完成，原始 JSON 可解析，来源匹配，短样本角色／顺序／文字正确 |
| workspace 保存 | 文件在会话 workspace 中可读取，原字节及哈希一致，释放浏览器会话后仍存在 |
| 回执可靠 | 返回实际文件路径；没有文件时明确失败；同名和重复操作不覆盖既有结果 |
| 异常恢复 | 脚本未加载、超时、连接丢失、写入失败和取消均有明确结果；不能静默漏文件 |
| 重复验证 | 固定样本连续运行至少三次，记录成功／失败与耗时，再验证一次中断恢复 |

先完成 ChatGPT 单条“控制—下载—workspace 回执”闭环。其他平台沿用同一保存链路，但分别验证页面与脚本兼容性。长对话完整性、历史列表发现、批量调度及增量去重列为后续扩展；记忆处理不在当前实施或验收范围。

当前代码实现见第 8 节。开发 workspace 样例来自此前真实下载；本轮 Chromium 下载链路测试使用合成页面，不能作为四平台真实账号验收。未部署或改动浏览器全局下载目录。

## 8. 已实现的四分支功能

能力目录顶层的产品名称确定为 **导出 AI 平台对话**，与浏览器、文档同级，下设四个独立平台分支。本轮已将代码中的顶层描述同步为“导出 AI 平台对话”，内部 `ai_chat` 与既有能力／Workflow ID 保持不变：

| 分支 | 能力／Workflow ID | 首版接受的来源 |
|---|---|---|
| ChatGPT | `ai_chat.chatgpt` | `https://chatgpt.com/c/<id>` |
| Claude | `ai_chat.claude` | `https://claude.ai/chat/<id>` |
| Gemini | `ai_chat.gemini` | `https://gemini.google.com/app/<id>` |
| Grok | `ai_chat.grok` | `https://grok.com/c/<id>` |

示例请求：“导出这条 ChatGPT 平台对话并保存到 workspace：https://chatgpt.com/c/<id>”。首版要求明确对话链接；未给出时需要补充链接。仅打开 AI 网站、向 AI 发送问题、普通网页阅读不属于该能力。四个分支绑定各自 provider，不能把另一个平台的链接交给该分支。

执行链为：能力目录 → 固定直接执行 Workflow → `ai_chat.export` 工具 → BrowserControl 任务会话 → Controller 固定 `ai_chat.export` 操作 → 原生下载监听 → Gateway 校验 → 会话 workspace 文件回执。工具不接受模型指定的 workspace、任意文件路径、脚本代码或选择器。

Controller 检查 RevivalStack 固定控件、全选状态和空搜索条件；存在筛选、已识别的生成中状态或控件缺失时拒绝导出。操作不修改用户选择，也不绕过登录。内部使用固定随包 Playwright 代码登记 `download` 监听后点击 JSON 按钮，调用 `saveAs` 保存至任务暂存目录，读取完成文件后清理。没有修改全局下载目录，也没有扫描 Downloads 猜测结果。

Controller 与 Gateway 可能位于不同文件系统，因此通过已认证本机控制通道传递至多 **4 MiB** 的原始 JSON（Base64 封装及 SHA-256），而不是将宿主路径交给容器读取。正文不进入工具响应／模型上下文：Gateway 工具只返回文件回执。下载等待 25 秒、脚本控件等待 15 秒、固定 MCP 操作超时 60 秒、整个导出上下文 90 秒；错误向上返回，首版 Workflow 不盲重试，用户可重新执行。

Gateway 再次校验 provider、来源 URL、非空消息结构和校验和；使用会话 `WorkspaceRoot` 与 `os.Root` 限定文件访问。独立采集目录先写 `.pending-*` 中的 `<对话标题>-<平台名>.json` 与 `manifest.json`，文件同步完成后重命名发布，失败清理未完成目录。重复导出使用新采集 ID，不覆盖既有结果。首版只自动导出 JSON，Markdown 保留为后续可选能力。

回执包含 `status`、`path`、`manifest_path`、`sha256`、`bytes`、`message_count`、`provider`、`source_url`、`coverage`。路径相对会话 workspace；正文覆盖固定为 `unknown`，明确当前只证明取得脚本输出，不保证长历史完整。当前还不支持账号历史发现、批量、当前用户标签页接管、摘要或记忆处理。

实现位置：`internal/capability/catalog.go` 注册分支，`internal/agent/ai_chat_workflow.go` 负责路由与固定执行，`internal/toolhub/ai_chat.go` 绑定会话，`internal/aichatexport/` 校验保存，`tools/browser-controller/src/ai-chat-export.mjs` 监听脚本下载。实际发行需更新并重启 Gateway 与对应 Controller，当前未执行部署。

验证覆盖四分支路由及跨平台链接拒绝、会话 workspace 绑定、调用方路径拒绝、原字节／哈希、重复文件不覆盖、符号链接逃逸和取消清理、下载先监听后点击、筛选状态拒绝。使用隔离 Chromium 合成页面连续三次真实 Blob 下载通过；该测试验证下载传输机制，不验证真实网站或油猴安装状态。真实四平台需在安装脚本并登录后逐一验收。

## 9. 产品名称与意图识别描述

顶层功能名固定为 **导出 AI 平台对话**，不使用含义过宽的“AI 对话”。层级为：

```text
功能
├─ 浏览器
├─ 文档
└─ 导出 AI 平台对话
   ├─ ChatGPT
   ├─ Claude
   ├─ Gemini
   └─ Grok
```

顶层介绍：**导出 ChatGPT、Claude、Gemini、Grok 网站上已有的对话，通过 RevivalStack 获取原始 JSON，并保存到当前 workspace，返回文件路径。**

| 分支 | 用于功能目录与意图识别的介绍 |
|---|---|
| ChatGPT | 导出 ChatGPT 网站上已有的指定对话，将原始 JSON 保存到当前 workspace。 |
| Claude | 导出 Claude 网站上已有的指定对话，将原始 JSON 保存到当前 workspace。 |
| Gemini | 导出 Gemini 网站上已有的指定对话，将原始 JSON 保存到当前 workspace。 |
| Grok | 导出 Grok 网站上已有的指定对话，将原始 JSON 保存到当前 workspace。 |

路由依据同时考虑**导出已有对话的动作、平台、目标对话**。正例包括“导出这条 ChatGPT 对话”“把 Claude 平台这段历史对话下载到工作区”“保存这个 Gemini 聊天链接的对话记录”“导出 Grok 对话为 JSON”。平台由用户明确名称或规范化对话链接确定；二者冲突时澄清，不猜测。缺少平台／链接时只补问缺失项，不强制用户重新描述整个任务。

“和我聊聊 AI”属于普通对话，“打开 ChatGPT”属于浏览器，“向 Claude 提问”不是导出，“总结 workspace 中已有的聊天 JSON”属于文档读取／后续处理，“登录 Gemini／检查 Grok 登录状态”属于平台登录设置操作。不能只因出现 AI 或平台名就路由到导出。名称、目录介绍和路由语义例句已在本轮同步，并更新能力目录版本，使分类使用新描述；内部 ID 保持不变。

## 10. 设置中的四平台登录与检测

在**配置设置 → 连接 → AI 平台登录**增加独立入口，与现有浏览器控制、浏览器邮件设置相邻。介绍为：**复用专用浏览器登录状态，将 AI 平台已有对话导出到 workspace。** 页面有 ChatGPT、Claude、Gemini、Grok 四张平台卡，分别提供“打开登录页”“检测状态”，以及统一的“检测全部”“刷新状态”。

| 平台 | 固定官方登录入口 |
|---|---|
| ChatGPT | `https://chatgpt.com/` |
| Claude | `https://claude.ai/` |
| Gemini | `https://gemini.google.com/` |
| Grok | `https://grok.com/` |

登录、检测和导出复用 SparkClaw 专用 Chromium 的同一持久 profile。已登录时直接使用，不创建隐身 profile，不从其他浏览器复制凭据。设置页不输入、保存或展示平台密码、Cookie、session token 或 API Key。

“打开登录页”复用现有 Controller 的显式浏览器登录入口，以同一 `user-data-dir` 打开固定官方页面供用户操作。登录完成后用户点击“检测状态”。不导航或关闭用户已有页面，也不把“页面已打开”当作登录成功。验证码、账号选择和账号补全留给用户处理。

“检测状态”通过固定 `ai_platform.check` 操作取得隔离任务页，只读查看可见的登录按钮、账号菜单和输入框等认证界面证据，完成后释放自己的任务页。最多短暂等待界面加载；仅 URL 可访问不足以判断登录。检测不发送消息、不导出对话、不修改账号设置，也不检查油猴脚本或导出按钮。“检测全部”顺序检测四个平台，单个平台失败仍继续其余平台并展示错误。

页面只显示两类状态：共享的**浏览器控制状态**和各平台的**登录状态**。登录状态为未检测／检测中／已登录／未登录／需要用户操作／无法确认，附最近检测时间与固定错误码。**不提供脚本就绪状态、脚本检查按钮或脚本诊断提示。** 导出时仍须找到 JSON 按钮才能执行，这属于导出操作本身，不是设置中的状态检测。

登录探针采用保守判定：明确的登录页／登录按钮判未登录，明确的账号菜单与聊天输入区域同时存在才判已登录；跳到支持的身份验证站点或显示挑战时提示用户操作；证据不足判无法确认。平台 DOM 更新可能需要调整探针，不能将无法确认等同于未登录。当前不提取或保存账号显示名。

结果仅在 Gateway 内存中保留 5 分钟供展示；重启后恢复未检测。凭据 generation、profile 或 Controller generation 变化以及浏览器非 ready 会清空旧结果；打开某个平台登录页使该平台结果失效。不会把缓存的已登录状态作为导出的永久许可，每次导出仍验证实际目标页和来源。账号注销／切换未被主动检测时，旧结果最多作为带时间戳的历史状态存在，用户可重新检测。

本轮实现三个复用既有浏览器认证边界的入口：`GET /api/browser/extension/ai-platforms` 返回状态，`POST /api/browser/extension/ai-platforms/{provider}/login` 打开登录页，`POST /api/browser/extension/ai-platforms/{provider}/check` 检测。拒绝未知 provider、查询参数和额外请求字段，不接收任意 URL 或 profile。并发登录／检测有忙碌保护，不抢占导出任务。

实现位置：`internal/aichatexport/login.go` 管理登录检测与短期状态，`internal/gateway/ai_platform_endpoints.go` 提供认证 API，`tools/browser-controller/src/ai-platform-login.mjs` 定义固定入口与只读探针，WebChat `settingsAIPlatforms.tsx` 提供四平台卡片及双语文案。设置与登录检测代码已接通，尚未部署或完成四平台真实登录态验收。验收包括不检查脚本、状态独立、已登录复用、登录页不代表成功、缓存失效、探针保守判定、错误不阻断全部检测及原页面保留。

油猴及两种导出用户脚本现已纳入产品组件管理：每次 Local／Remote 部署均按[浏览器组件管理](browser-components.md)安装并同步固定版本，不属于个性化扩展配置。
